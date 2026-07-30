package kicho

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/kicho/httpapi"
	"github.com/ShinteLab/kicho/store"
)

const importSample = `開始日時：2024/10/19 09:00:00
棋戦：テスト棋戦
場所：テスト会館
手合割：平手
先手：先手 太郎
後手：後手 次郎
手数----指手---------消費時間--
   1 ７六歩(77)
   2 ８四歩(83)
   3 投了
`

func newTestLibrary(t *testing.T) *Library {
	t.Helper()
	lib, err := Open(filepath.Join(t.TempDir(), "kicho.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { lib.Close(context.Background()) })
	return lib
}

func toShiftJIS(t *testing.T, s string) []byte {
	t.Helper()
	b, _, err := transform.Bytes(japanese.ShiftJIS.NewEncoder(), []byte(s))
	if err != nil {
		t.Fatalf("encode Shift_JIS: %v", err)
	}
	return b
}

// 世に出回る .kif は Shift_JIS が多い。判別できること。
func TestDecodeKIF(t *testing.T) {
	t.Run("UTF-8", func(t *testing.T) {
		got, enc, err := DecodeKIF([]byte(importSample))
		if err != nil {
			t.Fatal(err)
		}
		if got != importSample {
			t.Error("UTF-8 の内容が変わっている")
		}
		if enc != EncodingUTF8 {
			t.Errorf("encoding = %q, want %q", enc, EncodingUTF8)
		}
	})

	t.Run("Shift_JIS", func(t *testing.T) {
		got, enc, err := DecodeKIF(toShiftJIS(t, importSample))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "棋戦：テスト棋戦") || !strings.Contains(got, "７六歩") {
			t.Errorf("Shift_JIS を復号できていない:\n%s", got)
		}
		// 本文は UTF-8 に寄せ、元の文字コードは記録として残す。
		if enc != EncodingShiftJIS {
			t.Errorf("encoding = %q, want %q", enc, EncodingShiftJIS)
		}
	})

	t.Run("ASCII のみはどちらでも同じ", func(t *testing.T) {
		got, enc, err := DecodeKIF([]byte("1 7g7f\n"))
		if err != nil {
			t.Fatal(err)
		}
		if got != "1 7g7f\n" || enc != EncodingUTF8 {
			t.Errorf("= %q, %q", got, enc)
		}
	})
}

func TestImportKIF(t *testing.T) {
	lib := newTestLibrary(t)

	rec, err := lib.ImportKIF(context.Background(), importSample)
	if err != nil {
		t.Fatalf("ImportKIF: %v", err)
	}

	if rec.Source != store.SourcePaste {
		t.Errorf("Source = %q, want %q", rec.Source, store.SourcePaste)
	}
	if rec.Event != "テスト棋戦" || rec.Black != "先手 太郎" || rec.White != "後手 次郎" {
		t.Errorf("メタデータが取れていない: %+v", rec.Game)
	}
	if rec.Moves != 3 {
		t.Errorf("Moves = %d, want 3", rec.Moves)
	}
	if rec.EndMark != "投了" || !rec.Finished() {
		t.Errorf("EndMark = %q", rec.EndMark)
	}
	if rec.StartedAt.IsZero() {
		t.Error("StartedAt が取れていない")
	}
	if !strings.Contains(rec.Body, "1 ７六歩(77)") {
		t.Errorf("KIF = %q", rec.Body)
	}
}

// 貼り付け登録は毎回別の棋譜として登録する。
func TestImportKIFAlwaysCreatesNewRecord(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	first, err := lib.ImportKIF(ctx, importSample)
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.ImportKIF(ctx, importSample)
	if err != nil {
		t.Fatal(err)
	}

	if first.ID == second.ID {
		t.Error("同じ ID になっている（毎回新規登録のはず）")
	}
	n, err := lib.Store().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("Count = %d, want 2", n)
	}
}

func TestImportKIFRejectsGarbage(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	for _, in := range []string{"", "   ", "これは棋譜ではありません"} {
		if _, err := lib.ImportKIF(ctx, in); err == nil {
			t.Errorf("ImportKIF(%q) が成功した", in)
		}
	}
}

func TestImportURL(t *testing.T) {
	lib := newTestLibrary(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(importSample))
	}))
	defer srv.Close()

	rec, err := lib.ImportURL(context.Background(), srv.URL+"/kifu/x.kif")
	if err != nil {
		t.Fatalf("ImportURL: %v", err)
	}
	if rec.Source != store.SourceURL {
		t.Errorf("Source = %q, want %q", rec.Source, store.SourceURL)
	}
	// 出所が残ること。
	if rec.SourceURL != srv.URL+"/kifu/x.kif" {
		t.Errorf("SourceURL = %q", rec.SourceURL)
	}
	if rec.Event != "テスト棋戦" || rec.Moves != 3 {
		t.Errorf("メタデータが取れていない: %+v", rec.Game)
	}
}

// Shift_JIS で配信される .kif も取り込めること。
func TestImportURLShiftJIS(t *testing.T) {
	lib := newTestLibrary(t)

	body := toShiftJIS(t, importSample)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	rec, err := lib.ImportURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ImportURL: %v", err)
	}
	if rec.Event != "テスト棋戦" {
		t.Errorf("Event = %q", rec.Event)
	}
}

// relayPage は日本将棋連盟の棋譜中継ページ
// (http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html) を写したもの。
// 中継ビューア(kj.js)が読む KIF_FILE_NAME に .kif の在り処が入っている。
const relayPage = `<!doctype html>
<html>
<head>
<meta charset="UTF-8">
<title>2026年7月29日～7月30日　七番勝負　第３局</title>
</head>
<body id="oui">
<main>
  <script language="javascript" type="text/javascript">
    const KJ_DIR = "/common/js/kj/";
    const KIF_FILE_NAME = "/oui/kifu/67/oui202607290101.kif";
    const UPDATE_TIME = 1;
  </script>
  <script src="/common/js/kj/kj.js"></script>
</main>
</body>
</html>
`

// 棋譜中継ページ(HTML)の URL を貼っても取り込めること。
//
// ページから対局内容を読み取るのではなく、そこに書かれた .kif を辿って
// **原本をそのまま**取り込む。
func TestImportURLFollowsRelayPage(t *testing.T) {
	lib := newTestLibrary(t)

	kif := toShiftJIS(t, importSample) // 連盟の .kif は Shift_JIS
	mux := http.NewServeMux()
	mux.HandleFunc("/oui/kifu/67/oui202607290101.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.Write([]byte(relayPage))
	})
	mux.HandleFunc("/oui/kifu/67/oui202607290101.kif", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=Shift_JIS")
		w.Write(kif)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	pageURL := srv.URL + "/oui/kifu/67/oui202607290101.html"
	rec, err := lib.ImportURL(context.Background(), pageURL)
	if err != nil {
		t.Fatalf("ImportURL: %v", err)
	}
	if rec.Event != "テスト棋戦" || rec.Moves != 3 {
		t.Errorf("メタデータが取れていない: %+v", rec.Game)
	}
	// 出所は貼られた中継ページの URL を残す(.kif に書き換えない)。
	if rec.SourceURL != pageURL {
		t.Errorf("SourceURL = %q, want %q", rec.SourceURL, pageURL)
	}
	if rec.Encoding != EncodingShiftJIS {
		t.Errorf("Encoding = %q, want %q", rec.Encoding, EncodingShiftJIS)
	}
}

// .kif へのリンクからも辿れること(KIF_FILE_NAME を持たないサイト向けの保険)。
func TestImportURLFollowsKifAnchor(t *testing.T) {
	lib := newTestLibrary(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/kifu/index.html", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><a href="../download/20260729.kif?v=2">棋譜</a></body></html>`))
	})
	mux.HandleFunc("/download/20260729.kif", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(importSample))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rec, err := lib.ImportURL(context.Background(), srv.URL+"/kifu/index.html")
	if err != nil {
		t.Fatalf("ImportURL: %v", err)
	}
	if rec.Event != "テスト棋戦" {
		t.Errorf("Event = %q", rec.Event)
	}
}

// 中継ページの相対パスが取得元 URL を基準に解決されること。
func TestKifURLFromHTML(t *testing.T) {
	base, err := url.Parse("http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html")
	if err != nil {
		t.Fatal(err)
	}

	got, err := kifURLFromHTML(base, []byte(relayPage))
	if err != nil {
		t.Fatalf("kifURLFromHTML: %v", err)
	}
	const want = "http://live.shogi.or.jp/oui/kifu/67/oui202607290101.kif"
	if got.String() != want {
		t.Errorf("= %q, want %q", got, want)
	}

	t.Run("相対パス", func(t *testing.T) {
		page := []byte(`<html><script>const KIF_FILE_NAME = "oui202607290101.kif";</script></html>`)
		got, err := kifURLFromHTML(base, page)
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != want {
			t.Errorf("= %q, want %q", got, want)
		}
	})

	t.Run("見つからない", func(t *testing.T) {
		if _, err := kifURLFromHTML(base, []byte(`<html><body>棋譜はありません</body></html>`)); err == nil {
			t.Error("エラーにならなかった")
		}
	})
}

func TestLooksLikeHTML(t *testing.T) {
	if !looksLikeHTML([]byte("\n  <!doctype html>")) {
		t.Error("HTML を HTML と判定できていない")
	}
	// KIF は `<` で始まらない（BOM 付きでも）。
	for _, s := range []string{importSample, "# --- Kifu for Windows ---\n", "\ufeff開始日時：2026/07/29\n"} {
		if looksLikeHTML([]byte(s)) {
			t.Errorf("KIF を HTML と誤判定: %.20q", s)
		}
	}
}

func TestImportURLErrors(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer notFound.Close()

	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>棋譜ではない</body></html>"))
	}))
	defer html.Close()

	cases := map[string]string{
		"スキームなし":    "example.com/kifu.kif",
		"file スキーム": "file:///C:/kifu.kif",
		"HTTP エラー":  notFound.URL,
		// HTML が返ってきたら .kif を辿るが、リンクが無ければエラーにする。
		"棋譜リンクの無い HTML": html.URL,
	}
	for name, u := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := lib.ImportURL(ctx, u); err == nil {
				t.Errorf("ImportURL(%q) が成功した", u)
			}
		})
	}
}

// 巨大なレスポンスを掴んだら落とすこと。
func TestImportURLRejectsHugeBody(t *testing.T) {
	lib := newTestLibrary(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := strings.Repeat("あ", 1024)
		for i := 0; i < (maxKifuBytes/len(chunk))+8; i++ {
			w.Write([]byte(chunk))
		}
	}))
	defer srv.Close()

	if _, err := lib.ImportURL(context.Background(), srv.URL); err == nil {
		t.Error("巨大なレスポンスが通ってしまった")
	}
}

// kicho 自身が配信する KIF を kicho が読み戻せること（往復）。
//
// 別の kicho の /kifu/{id} を取り込む使い方を想定している。
// 書き出しと読み取りが食い違うと気付きにくいので、ここで固定する。
func TestImportURLRoundTripThroughOwnServer(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	// スクレイピング相当の棋譜（消費時間つき）を保存しておく。
	origKIF := kifu.Document{
		Event:     "第37期竜王戦七番勝負第２局",
		Place:     "福井県あわら市",
		StartedAt: time.Date(2024, 10, 19, 9, 0, 0, 0, time.FixedZone("JST", 9*60*60)),
		Handicap:  "平手",
		Black:     "佐々木勇気八段",
		White:     "藤井聡太竜王",
		TimeLimit: 8 * time.Hour,
		Countdown: 60 * time.Second,
		ShowTime:  true,
		Moves: []kifu.Move{
			{Num: 1, Name: "７六歩", FromX: 7, FromY: 7},
			{Num: 2, Name: "８四歩", FromX: 8, FromY: 3, Spend: time.Minute},
			{Num: 3, Name: "同　歩", FromX: 2, FromY: 3, Spend: 30 * time.Second},
			{Num: 4, Name: "投了", Spend: 4 * time.Minute},
		},
	}.String()

	saved, err := lib.Store().Save(ctx, store.Game{
		Source: store.SourceYomiuri, SourceID: "orig",
		Event: "第37期竜王戦七番勝負第２局", Moves: 4, EndMark: "投了", Body: origKIF,
	})
	if err != nil {
		t.Fatal(err)
	}

	// kicho の配信サーバを立てて、自分の /kifu/{id} を取り込む。
	if err := lib.Server().Start(httpapi.Config{Host: httpapi.LoopbackHost, Port: 0}); err != nil {
		t.Fatal(err)
	}
	defer lib.Server().Stop(ctx)

	running, addr := lib.Server().Running()
	if !running {
		t.Fatal("server not running")
	}

	rec, err := lib.ImportURL(ctx, "http://"+addr+httpapi.KifuPath(saved.ID))
	if err != nil {
		t.Fatalf("ImportURL: %v", err)
	}

	if rec.Event != "第37期竜王戦七番勝負第２局" {
		t.Errorf("Event = %q", rec.Event)
	}
	if rec.Black != "佐々木勇気八段" || rec.White != "藤井聡太竜王" {
		t.Errorf("Black=%q White=%q", rec.Black, rec.White)
	}
	if rec.Moves != 4 {
		t.Errorf("Moves = %d, want 4", rec.Moves)
	}
	if rec.EndMark != "投了" {
		t.Errorf("EndMark = %q", rec.EndMark)
	}
	if rec.StartedAt.IsZero() {
		t.Error("StartedAt が失われている")
	}

	// KIF テキストが元と一致すること（書き出し ⇄ 読み取りが可逆）。
	if rec.Body != origKIF {
		t.Errorf("KIF round-trip differs:\n got:\n%s\nwant:\n%s", rec.Body, origKIF)
	}
}

// 取り込んだ棋譜が検索にも乗ること。
func TestImportedGameIsSearchable(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	if _, err := lib.ImportKIF(ctx, importSample); err != nil {
		t.Fatal(err)
	}
	got, err := lib.Store().Search(ctx, store.Query{Text: "テスト棋戦"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("検索できない: %d件", len(got))
	}
}
