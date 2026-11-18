package kicho

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ShinteLab/kicho/httpapi"
	"github.com/ShinteLab/kicho/scrape"
	"github.com/ShinteLab/kicho/store"
)

// fetcherWithFuwalete はふわりと将棋の取得だけ記録用の transport に差し替えた Fetcher を返す。
func fetcherWithFuwalete() (*Fetcher, *recordingTransport) {
	rt := &recordingTransport{}
	f := NewFetcher()
	f.fuwalete = &scrape.Fuwalete{Client: &http.Client{Transport: rt}}
	return f, rt
}

const fuwaleteSlug = "ito-365549"

// 記事を REST API で取りに行ったか（記事の HTML ではなく）。
func wentToFuwalete(t *testing.T, rt *recordingTransport) {
	t.Helper()
	if len(rt.urls) != 1 || !strings.HasPrefix(rt.urls[0], "https://fuwalete.com/wp-json/wp/v2/posts?") ||
		!strings.Contains(rt.urls[0], "slug="+fuwaleteSlug) {
		t.Errorf("取りに行った先 = %v", rt.urls)
	}
}

// fuwalete.com の URL は**汎用の URL（.kif として読む）より先に**ふわりと将棋へ回すこと。
func TestFetchRoutesFuwalete(t *testing.T) {
	f, rt := fetcherWithFuwalete()
	_, err := f.Fetch(context.Background(), "https://fuwalete.com/"+fuwaleteSlug+"/")
	if err == nil || !strings.Contains(err.Error(), "ふわりと将棋") {
		t.Fatalf("err = %v, want ふわりと将棋のエラー", err)
	}
	wentToFuwalete(t, rt)

	// 記事でない URL は汎用の口へ落とさず、その場で理由を返す。
	_, err = f.Fetch(context.Background(), "https://fuwalete.com/category/x/")
	if err == nil || !strings.Contains(err.Error(), "記事の URL ではありません") {
		t.Errorf("err = %v", err)
	}
}

// Refresh は slug で直接取りに行く。
func TestRefreshFuwalete(t *testing.T) {
	f, rt := fetcherWithFuwalete()
	if _, err := f.Refresh(context.Background(), store.SourceFuwalete, fuwaleteSlug); err == nil {
		t.Fatal("404 なのに成功した")
	}
	wentToFuwalete(t, rt)
}

// 諸元は記事の URL。保存・追跡のどちらも取得元から決め直し、その URL から slug に戻せる。
func TestFuwaleteSourceURL(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()
	want := "https://fuwalete.com/" + fuwaleteSlug + "/"
	in := Fetched{Source: store.SourceFuwalete, SourceID: fuwaleteSlug, SourceURL: "https://example.com/でたらめ", KIF: importSample}

	rec, err := lib.Save(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Source != store.SourceFuwalete || rec.SourceURL != want {
		t.Errorf("Save: source = %q, url = %q", rec.Source, rec.SourceURL)
	}

	w, err := lib.Watch(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if w.SourceURL != want {
		t.Errorf("Watch: url = %q", w.SourceURL)
	}

	if got := RefetchableURL(store.SourceFuwalete, want); got != want {
		t.Errorf("RefetchableURL = %q", got)
	}
	slug, err := scrape.FuwaleteSlug(want)
	if err != nil || slug != fuwaleteSlug {
		t.Errorf("諸元から slug に戻せない: %q, %v", slug, err)
	}
}

// fuwaleteTestFetcher は scrape/testdata の REST API の応答を返すテスト用サーバに繋いだ Fetcher を返す。
func fuwaleteTestFetcher(t *testing.T, postFile string) *Fetcher {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("scrape", "testdata", postFile))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wp-json/wp/v2/posts" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	f := NewFetcher()
	f.fuwalete = &scrape.Fuwalete{Client: srv.Client(), BaseURL: srv.URL}
	return f
}

// 誤記のある中継: 原本（KI2）は全部の手のまま保存し、エラーの印を付ける。
// 終局はサイトに書かれたまま。/kifu/{id}.kif は盤に当てられたところまでの KIF を配る。
func TestFuwaleteSavesKI2WithErrorMark(t *testing.T) {
	ctx := context.Background()
	got, err := fuwaleteTestFetcher(t, "fuwalete_post_finished.json").Refresh(ctx, store.SourceFuwalete, fuwaleteSlug)
	if err != nil {
		t.Fatal(err)
	}
	if got.Format != "ki2" || got.Moves != 97 || got.EndMark != "投了" || got.ErrorMove != 74 || got.Error == "" {
		t.Fatalf("Fetched: format=%q moves=%d end=%q errorMove=%d error=%q",
			got.Format, got.Moves, got.EndMark, got.ErrorMove, got.Error)
	}

	lib := newTestLibrary(t)
	rec, err := lib.Save(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Format != "ki2" || rec.Moves != 97 || rec.EndMark != "投了" || rec.ErrorMove != 74 || rec.Error == "" {
		t.Fatalf("Record: format=%q moves=%d end=%q errorMove=%d error=%q",
			rec.Format, rec.Moves, rec.EndMark, rec.ErrorMove, rec.Error)
	}
	if rec.Body != got.KIF {
		t.Error("原本がそのまま保存されていない")
	}

	if err := lib.Server().Start(httpapi.Config{Host: httpapi.LoopbackHost, Port: 0}); err != nil {
		t.Fatal(err)
	}
	defer lib.Server().Stop(ctx)
	_, addr := lib.Server().Running()

	body := httpGet(t, "http://"+addr+httpapi.KifuPath(rec.ID)+".kif")
	if !strings.Contains(body, "*74 手目「△８九飛」を盤に当てられないため、73 手目までです") ||
		!strings.Contains(body, "73 同　玉(58)") || strings.Contains(body, "投了") {
		t.Errorf("/kifu/{id}.kif:\n%s", body)
	}
	// 拡張子なしは原本（KI2）のまま。
	if raw := httpGet(t, "http://"+addr+httpapi.KifuPath(rec.ID)); raw != rec.Body {
		t.Errorf("/kifu/{id} が原本と違う:\n%s", raw)
	}
}

// KI2 のテキストも貼り付けで取り込める（原本は KI2 のまま、エラーの印付き）。
func TestImportKI2(t *testing.T) {
	ctx := context.Background()
	lib := newTestLibrary(t)
	text := "棋戦：テスト\n先手：甲\n後手：乙\n▲２六歩\n△８四歩\n▲８九飛\n△投了\n"
	rec, err := lib.ImportKIF(ctx, text)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Format != "ki2" || rec.Body != text || rec.Moves != 3 || rec.ErrorMove != 3 || rec.EndMark != "投了" {
		t.Fatalf("format=%q moves=%d errorMove=%d end=%q", rec.Format, rec.Moves, rec.ErrorMove, rec.EndMark)
	}
	if rec.Event != "テスト" || rec.Black != "甲" {
		t.Errorf("Event=%q Black=%q", rec.Event, rec.Black)
	}

	ok, err := lib.ImportKIF(ctx, "▲７六歩\n△３四歩\n")
	if err != nil {
		t.Fatal(err)
	}
	if ok.Format != "ki2" || ok.Moves != 2 || ok.ErrorMove != 0 || ok.Error != "" {
		t.Fatalf("format=%q moves=%d errorMove=%d error=%q", ok.Format, ok.Moves, ok.ErrorMove, ok.Error)
	}
}

func httpGet(t *testing.T, u string) string {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", u, resp.StatusCode, b)
	}
	return string(b)
}
