package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/kicho/scrape"
	"github.com/ShinteLab/kicho/store"
)

type fakeFetcher struct {
	game *scrape.Game
	err  error
}

func (f *fakeFetcher) FetchGame(ctx context.Context, id string) (*scrape.Game, error) {
	if f.err != nil {
		return nil, f.err
	}
	g := *f.game
	g.SourceID = id
	return &g, nil
}

// countingFetcher は取得回数を数える(キャッシュしていないことの確認用)。
type countingFetcher struct {
	game  *scrape.Game
	calls int
}

func (f *countingFetcher) FetchGame(ctx context.Context, id string) (*scrape.Game, error) {
	f.calls++
	g := *f.game
	g.SourceID = id
	return &g, nil
}

// fakeLiveFetcher は連盟の中継(KIF をそのまま返す取得元)の代役。
type fakeLiveFetcher struct {
	kif  string
	err  error
	ids  []string // 呼ばれた棋譜 ID(パスの受け渡しを見るため)
	call int
}

func (f *fakeLiveFetcher) FetchKifu(ctx context.Context, id string) (string, error) {
	f.call++
	f.ids = append(f.ids, id)
	if f.err != nil {
		return "", f.err
	}
	return f.kif, nil
}

func sampleFetchedGame() *scrape.Game {
	return &scrape.Game{
		Event:     "テスト棋戦第１局",
		Handicap:  "平手",
		Black:     "先手 太郎",
		White:     "後手 次郎",
		StartedAt: time.Date(2024, 10, 19, 9, 0, 0, 0, time.UTC),
		Moves: []kifu.Move{
			{Num: 1, Name: "７六歩", FromX: 7, FromY: 7},
		},
	}
}

// newTestServer は listen 済みのサーバとベース URL を返す。
func newTestServer(t *testing.T, f Fetcher) (*Server, *store.Store, string) {
	return newTestServerWithLive(t, f, &fakeLiveFetcher{kif: sampleLiveKIF})
}

// sampleLiveKIF は連盟の中継が返す形の KIF(コメント行つき)。
const sampleLiveKIF = `# --- Kifu for Windows Pro V7.20 棋譜ファイル ---
開始日時：2026/07/29 09:00
棋戦：中継テスト棋戦
手合割：平手
先手：先手 太郎
後手：後手 次郎
手数----指手---------消費時間--
*コメント行は手数に数えない
   1 ７六歩(77)   ( 0:16/00:00:16)
`

func newTestServerWithLive(t *testing.T, f Fetcher, live LiveFetcher, opts ...func(*Server)) (*Server, *store.Store, string) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "kicho.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	logger := slog.New(slog.DiscardHandler)
	s := New(st, f, live, logger)
	for _, o := range opts {
		o(s)
	}

	// ポート 0 で空きポートを取らせる。
	if err := s.Start(Config{Host: LoopbackHost, Port: 0}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { s.Stop(context.Background()) })

	running, addr := s.Running()
	if !running {
		t.Fatal("server should be running after Start")
	}
	return s, st, "http://" + addr
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(b)
}

func TestRyuohKifuEndpoint(t *testing.T) {
	_, _, base := newTestServer(t, &fakeFetcher{game: sampleFetchedGame()})

	resp, body := get(t, base+"/ryuoh/kifu/67037f045dcc1abf6b9528e3")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != kifuContentType {
		t.Errorf("Content-Type = %q, want %q", ct, kifuContentType)
	}
	for _, want := range []string{"手合割：平手", "先手：先手 太郎", "1 ７六歩(77)"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\ngot:\n%s", want, body)
		}
	}
}

// ライブ経路は毎回サイトへ取りに行き、外部ツールにもキャッシュさせないこと。
// 対局中の棋譜は随時更新されるため、古いものが返ると使いものにならない。
func TestRyuohKifuIsNotCached(t *testing.T) {
	f := &countingFetcher{game: sampleFetchedGame()}
	_, _, base := newTestServer(t, f)

	for i := 0; i < 3; i++ {
		resp, _ := get(t, base+"/ryuoh/kifu/67037f045dcc1abf6b9528e3")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", cc)
		}
	}
	if f.calls != 3 {
		t.Errorf("fetcher was called %d times, want 3 (must not cache)", f.calls)
	}
}

// 保存済み経路は DB のみを見ること(サイトへ取りに行かない)。
func TestSavedKifuDoesNotFetch(t *testing.T) {
	f := &countingFetcher{game: sampleFetchedGame()}
	_, st, base := newTestServer(t, f)

	rec, err := st.Save(context.Background(), store.Game{
		Source:   store.SourceYomiuri,
		SourceID: "abc",
		Event:    "保存済み棋戦",
		Body:     "手合割：平手\n手数----指手---------消費時間--\n1 ７六歩(77)\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	if resp, _ := get(t, base+"/kifu/"+rec.ID); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if f.calls != 0 {
		t.Errorf("saved kifu must not hit the site, got %d fetches", f.calls)
	}
}

func TestRyuohKifuEndpointFetchError(t *testing.T) {
	_, _, base := newTestServer(t, &fakeFetcher{err: fmt.Errorf("boom")})

	resp, body := get(t, base+"/ryuoh/kifu/whatever")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	if !strings.Contains(body, "kifu error") {
		t.Errorf("body = %q", body)
	}
}

func TestSavedKifuEndpoint(t *testing.T) {
	_, st, base := newTestServer(t, &fakeFetcher{game: sampleFetchedGame()})

	rec, err := st.Save(context.Background(), store.Game{
		Source:    store.SourceYomiuri,
		SourceID:  "abc",
		Event:     "保存済み棋戦",
		StartedAt: time.Date(2024, 10, 19, 9, 0, 0, 0, time.UTC),
		Body:      "手合割：平手\n手数----指手---------消費時間--\n1 ７六歩(77)\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, body := get(t, base+"/kifu/"+rec.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "1 ７六歩(77)") {
		t.Errorf("body = %q", body)
	}
	// 外部ツールが保存するときのファイル名ヒント。
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "20241019") {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

// 拡張子で形式を指定できること。原本と同じ形式ならそのまま返す。
func TestSavedKifuWithExtension(t *testing.T) {
	_, st, base := newTestServer(t, &fakeFetcher{game: sampleFetchedGame()})

	body := "手合割：平手\n手数----指手---------消費時間--\n1 ７六歩(77)\n"
	rec, err := st.Save(context.Background(), store.Game{
		Source: store.SourceYomiuri, SourceID: "abc", Event: "保存済み棋戦",
		Body: body, Format: "kif",
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("拡張子なしは原本のまま", func(t *testing.T) {
		resp, got := get(t, base+"/kifu/"+rec.ID)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if got != body {
			t.Errorf("body = %q, want %q", got, body)
		}
	})

	t.Run(".kif は原本と同形式なのでそのまま", func(t *testing.T) {
		resp, got := get(t, base+"/kifu/"+rec.ID+".kif")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if got != body {
			t.Errorf("body = %q", got)
		}
		if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, ".kif") {
			t.Errorf("Content-Disposition = %q", cd)
		}
	})

	// 未実装の形式は 501。変換を実装したら 200 になる。
	for _, ext := range []string{"ki2", "csa", "usi", "sfen", "jkf", "usen"} {
		t.Run("."+ext+" は未実装なので 501", func(t *testing.T) {
			resp, got := get(t, base+"/kifu/"+rec.ID+"."+ext)
			if resp.StatusCode != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501", resp.StatusCode)
			}
			if !strings.Contains(got, ext) {
				t.Errorf("body should name the requested format: %q", got)
			}
		})
	}

	t.Run("未知の拡張子は ID の一部として扱う", func(t *testing.T) {
		resp, _ := get(t, base+"/kifu/"+rec.ID+".txt")
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestSavedKifuNotFound(t *testing.T) {
	_, _, base := newTestServer(t, &fakeFetcher{game: sampleFetchedGame()})

	resp, body := get(t, base+"/kifu/no-such-id")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if !strings.Contains(body, "not found") {
		t.Errorf("body = %q", body)
	}
}

func TestListEndpoint(t *testing.T) {
	_, st, base := newTestServer(t, &fakeFetcher{game: sampleFetchedGame()})

	rec, err := st.Save(context.Background(), store.Game{
		Source:   store.SourceYomiuri,
		SourceID: "abc",
		Event:    "保存済み棋戦",
		Black:    "先手 太郎",
		White:    "後手 次郎",
		Body:     "手合割：平手\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, body := get(t, base+"/kifu")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var list []struct {
		ID    string `json:"id"`
		Event string `json:"event"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].ID != rec.ID || list[0].URL != "/kifu/"+rec.ID {
		t.Errorf("item = %+v", list[0])
	}
}

// 一覧は棋譜が無くても null ではなく空配列を返すこと(外部ツール側で扱いやすい)。
func TestListEndpointEmptyIsArray(t *testing.T) {
	_, _, base := newTestServer(t, &fakeFetcher{game: sampleFetchedGame()})

	_, body := get(t, base+"/kifu")
	if strings.TrimSpace(body) != "[]" {
		t.Errorf("body = %q, want []", body)
	}
}

func TestStartStopCycle(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "kicho.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	s := New(st, &fakeFetcher{game: sampleFetchedGame()}, &fakeLiveFetcher{kif: sampleLiveKIF},
		slog.New(slog.DiscardHandler))

	if running, _ := s.Running(); running {
		t.Error("should not be running before Start")
	}
	if err := s.Start(Config{Port: 0}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// 二重起動はエラー。
	if err := s.Start(Config{Port: 0}); err == nil {
		t.Error("second Start should fail")
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if running, _ := s.Running(); running {
		t.Error("should not be running after Stop")
	}
	// 停止済みの Stop は no-op。
	if err := s.Stop(context.Background()); err != nil {
		t.Errorf("second Stop: %v", err)
	}
	// 再起動できること。
	if err := s.Start(Config{Port: 0}); err != nil {
		t.Fatalf("restart: %v", err)
	}
	s.Stop(context.Background())
}

// パス組み立て関数が実際に登録されているルートと一致すること。
// UI の URL コピーはこれを使うので、routes() と食い違うと壊れる。
func TestPathBuildersReachTheRoutes(t *testing.T) {
	_, st, base := newTestServer(t, &fakeFetcher{game: sampleFetchedGame()})

	rec, err := st.Save(context.Background(), store.Game{
		Source:   store.SourceYomiuri,
		SourceID: "67037f045dcc1abf6b9528e3",
		Event:    "保存済み棋戦",
		Body:     "手合割：平手\n手数----指手---------消費時間--\n1 ７六歩(77)\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("KifuPath", func(t *testing.T) {
		resp, body := get(t, base+KifuPath(rec.ID))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", KifuPath(rec.ID), resp.StatusCode)
		}
		if !strings.Contains(body, "1 ７六歩(77)") {
			t.Errorf("body = %q", body)
		}
	})

	t.Run("RyuohKifuPath", func(t *testing.T) {
		p := RyuohKifuPath("67037f045dcc1abf6b9528e3")
		resp, body := get(t, base+p)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", p, resp.StatusCode)
		}
		if !strings.Contains(body, "手合割：平手") {
			t.Errorf("body = %q", body)
		}
	})
}

// 連盟の棋譜 ID はスラッシュを含むパスなので、経路とパス組み立てを別に固定する。
func TestShogiLivePathReachesTheRoute(t *testing.T) {
	live := &fakeLiveFetcher{kif: sampleLiveKIF}
	_, _, base := newTestServerWithLive(t, &fakeFetcher{game: sampleFetchedGame()}, live)

	const id = "oui/kifu/67/oui202607290101"
	p := ShogiLiveKifuPath(id)
	resp, body := get(t, base+p)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", p, resp.StatusCode)
	}
	if !strings.Contains(body, "棋戦：中継テスト棋戦") {
		t.Errorf("body = %q", body)
	}
	// スラッシュを含む ID がそのまま取得元へ渡ること。
	if len(live.ids) != 1 || live.ids[0] != id {
		t.Errorf("fetched ids = %q, want [%q]", live.ids, id)
	}
	// 対局中は随時更新されるのでキャッシュさせない。
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// 将棋DB2 の経路とパス組み立て。付けていなければ 501。
func TestShogiDB2PathReachesTheRoute(t *testing.T) {
	const id = "3186d1e7f8f33242990b1e853f53250d443dbe25132936b005068009a201"
	p := ShogiDB2KifuPath(id)

	_, _, bare := newTestServerWithLive(t, &fakeFetcher{game: sampleFetchedGame()}, nil)
	if resp, _ := get(t, bare+p); resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("取得元なしで GET %s = %d, want 501", p, resp.StatusCode)
	}

	db2 := &fakeLiveFetcher{kif: sampleLiveKIF}
	_, _, base := newTestServerWithLive(t, &fakeFetcher{game: sampleFetchedGame()}, nil,
		func(s *Server) { s.WithShogiDB2(db2) })
	resp, body := get(t, base+p)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", p, resp.StatusCode)
	}
	if !strings.Contains(body, "棋戦：中継テスト棋戦") {
		t.Errorf("body = %q", body)
	}
	if len(db2.ids) != 1 || db2.ids[0] != id {
		t.Errorf("fetched ids = %q, want [%q]", db2.ids, id)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// 対局中の棋譜なのでリクエストのたびに取りに行くこと(キャッシュしない)。
func TestShogiLiveKifuIsNotCached(t *testing.T) {
	live := &fakeLiveFetcher{kif: sampleLiveKIF}
	_, _, base := newTestServerWithLive(t, &fakeFetcher{game: sampleFetchedGame()}, live)

	p := base + ShogiLiveKifuPath("oui/kifu/67/oui202607290101")
	get(t, p)
	get(t, p)
	if live.call != 2 {
		t.Errorf("取得回数 = %d, want 2", live.call)
	}
}

func TestPathBuildersEscape(t *testing.T) {
	if got := KifuPath("a/../b"); got != "/kifu/a%2F..%2Fb" {
		t.Errorf("KifuPath = %q", got)
	}
	if got := RyuohKifuPath("a/../b"); got != "/ryuoh/kifu/a%2F..%2Fb" {
		t.Errorf("RyuohKifuPath = %q", got)
	}
	// 連盟はパスが ID なので、スラッシュは区切りとして残しセグメントだけ逃がす。
	if got := ShogiLiveKifuPath("oui/kifu/67/a b"); got != "/shogilive/kifu/oui/kifu/67/a%20b" {
		t.Errorf("ShogiLiveKifuPath = %q", got)
	}
}

func TestConfigIsPublic(t *testing.T) {
	cases := map[string]bool{
		"":            false,
		"127.0.0.1":   false,
		"localhost":   false,
		"::1":         false,
		"0.0.0.0":     true,
		"192.168.1.5": true,
	}
	for host, want := range cases {
		if got := (Config{Host: host}).IsPublic(); got != want {
			t.Errorf("Config{Host:%q}.IsPublic() = %v, want %v", host, got, want)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	if got := DefaultConfig().addr(); got != "127.0.0.1:3000" {
		t.Errorf("DefaultConfig().addr() = %q, want 127.0.0.1:3000", got)
	}
	// Host 未指定はループバック、Port 0 は net.Listen と同じく自動割り当て。
	if got := (Config{}).addr(); got != "127.0.0.1:0" {
		t.Errorf("addr() = %q, want 127.0.0.1:0", got)
	}
	if got := (Config{Host: "0.0.0.0", Port: 8080}).addr(); got != "0.0.0.0:8080" {
		t.Errorf("addr() = %q", got)
	}
}
