package scrape

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// liveKIF は連盟の中継が配信している .kif を写したもの(対局中・第37期王位戦第３局の形)。
//
// 実物の特徴を残してある。
//   - 先頭が `# --- Kifu for Windows ...`
//   - kicho が読まないヘッダ(対局ID・戦型・備考…)が並ぶ
//   - `*` のコメント行が指し手の間に入る
//   - Shift_JIS で配信される
const liveKIF = `# --- Kifu for Windows Pro V7.20 棋譜ファイル ---
対局ID：21190
開始日時：2026/07/29 09:00
棋戦：伊藤園お～いお茶杯第67期王位戦七番勝負第３局
戦型：角換わりその他
持ち時間：8時間
秒読み：60秒
場所：北海道登別市「祝いの宿　登別グランドホテル」
備考：昼休前73手目11分
手合割：平手
先手：藤井聡太王位
後手：伊藤匠二冠
手数----指手---------消費時間--
*本局は７月29、30日の両日に行われる。
   1 ２六歩(27)   ( 0:01/00:00:01)
*立会人が開始を告げた。
   2 ８四歩(83)   ( 0:02/00:00:02)
   3 ２五歩(26)   ( 0:01/00:00:03)
`

// liveKIFFinished は終局まで進んだ .kif の終わり方。
const liveKIFFinished = liveKIF + `   4 投了         ( 0:00/00:00:03)
まで3手で先手の勝ち
`

// newLiveServer は中継サイトの代役を立てて、そこを向いた ShogiLive を返す。
func newLiveServer(t *testing.T, kif string) (*ShogiLive, *httptest.Server, *int) {
	t.Helper()

	hits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/oui/kifu/67/oui202607290101.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.Write([]byte(relayPage))
	})
	mux.HandleFunc("/oui/kifu/67/oui202607290101.kif", func(w http.ResponseWriter, r *http.Request) {
		hits++
		// 連盟は Shift_JIS で配信している。
		w.Header().Set("Content-Type", "text/plain; charset=Shift_JIS")
		w.Write(toShiftJIS(t, kif))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &ShogiLive{
		Client:  &http.Client{Timeout: 5 * time.Second},
		BaseURL: srv.URL,
	}, srv, &hits
}

const liveID = "oui/kifu/67/oui202607290101"

// 中継ページ・.kif・棋譜 ID のどれを渡しても同じ ID に解決されること。
func TestShogiLiveResolveID(t *testing.T) {
	sl, srv, _ := newLiveServer(t, liveKIF)
	ctx := context.Background()

	inputs := map[string]string{
		"中継ページ":     srv.URL + "/oui/kifu/67/oui202607290101.html",
		"kif":       srv.URL + "/oui/kifu/67/oui202607290101.kif",
		"棋譜 ID":     liveID,
		"先頭スラッシュ付き": "/" + liveID,
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			got, err := sl.ResolveID(ctx, in)
			if err != nil {
				t.Fatalf("ResolveID(%q): %v", in, err)
			}
			if got != liveID {
				t.Errorf("= %q, want %q", got, liveID)
			}
		})
	}

	t.Run("空", func(t *testing.T) {
		if _, err := sl.ResolveID(ctx, "  "); err == nil {
			t.Error("エラーにならなかった")
		}
	})
}

// 中継ページからの解決は拡張子の付け替えではなく KIF_FILE_NAME を見ること。
//
// ページが別名の .kif を読んでいてもそちらへ付いていく。
func TestShogiLiveResolveIDUsesKifFileName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><script>
    const KIF_FILE_NAME = "/oui/kifu/67/betsumei.kif";
</script></html>`))
	}))
	defer srv.Close()

	sl := &ShogiLive{BaseURL: srv.URL}
	got, err := sl.ResolveID(context.Background(), srv.URL+"/oui/kifu/67/oui202607290101.html")
	if err != nil {
		t.Fatal(err)
	}
	if got != "oui/kifu/67/betsumei" {
		t.Errorf("= %q, want %q", got, "oui/kifu/67/betsumei")
	}
}

func TestShogiLiveFetchGame(t *testing.T) {
	sl, _, hits := newLiveServer(t, liveKIF)

	g, err := sl.FetchGame(context.Background(), liveID)
	if err != nil {
		t.Fatalf("FetchGame: %v", err)
	}

	if g.SourceID != liveID {
		t.Errorf("SourceID = %q", g.SourceID)
	}
	// 本文は原本のまま(整形し直さない)。コメント行もヘッダも残る。
	if g.KIF != liveKIF {
		t.Errorf("KIF が原本と違う:\n%s", g.KIF)
	}
	if g.Encoding != EncodingShiftJIS {
		t.Errorf("Encoding = %q, want %q", g.Encoding, EncodingShiftJIS)
	}

	// メタデータは解析結果から取る。
	if g.Doc.Event != "伊藤園お～いお茶杯第67期王位戦七番勝負第３局" {
		t.Errorf("Event = %q", g.Doc.Event)
	}
	if g.Doc.Black != "藤井聡太王位" || g.Doc.White != "伊藤匠二冠" {
		t.Errorf("Black=%q White=%q", g.Doc.Black, g.Doc.White)
	}
	if g.Doc.Handicap != "平手" {
		t.Errorf("Handicap = %q", g.Doc.Handicap)
	}
	if g.Doc.StartedAt.IsZero() {
		t.Error("StartedAt が取れていない")
	}
	// コメント行(`*`)やヘッダを手数に数えないこと。
	if len(g.Doc.Moves) != 3 {
		t.Errorf("Moves = %d, want 3", len(g.Doc.Moves))
	}
	// 対局中なので終局していない。
	if g.Finished() {
		t.Errorf("対局中なのに終局扱い: EndMark=%q", g.Doc.EndMark())
	}

	if *hits != 1 {
		t.Errorf("取得回数 = %d, want 1", *hits)
	}
}

// 終局後は EndMark が立つこと(保存してよいかの判断に使う)。
func TestShogiLiveFetchGameFinished(t *testing.T) {
	sl, _, _ := newLiveServer(t, liveKIFFinished)

	g, err := sl.FetchGame(context.Background(), liveID)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Finished() || g.Doc.EndMark() != "投了" {
		t.Errorf("EndMark = %q, want 投了", g.Doc.EndMark())
	}
	if len(g.Doc.Moves) != 4 {
		t.Errorf("Moves = %d, want 4", len(g.Doc.Moves))
	}
}

// 対局中は棋譜が伸びるので、取り直せばそのぶん反映されること(キャッシュしない)。
func TestShogiLiveFetchGameIsNotCached(t *testing.T) {
	sl, _, hits := newLiveServer(t, liveKIF)
	ctx := context.Background()

	if _, err := sl.FetchGame(ctx, liveID); err != nil {
		t.Fatal(err)
	}
	if _, err := sl.FetchGame(ctx, liveID); err != nil {
		t.Fatal(err)
	}
	if *hits != 2 {
		t.Errorf("取得回数 = %d, want 2", *hits)
	}
}

// .kif のつもりで HTML(S3 の 404 ページ等)を掴んだら分かるエラーにする。
func TestShogiLiveFetchGameRejectsHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><head><title>404 Not Found</title></head></html>"))
	}))
	defer srv.Close()

	sl := &ShogiLive{BaseURL: srv.URL}
	_, err := sl.FetchGame(context.Background(), liveID)
	if err == nil {
		t.Fatal("エラーにならなかった")
	}
	if !strings.Contains(err.Error(), "HTML") {
		t.Errorf("HTML を掴んだと分からないエラー: %v", err)
	}
}

func TestShogiLiveURLs(t *testing.T) {
	sl := NewShogiLive()

	if got := sl.KifuURL(liveID); got != ShogiLiveBaseURL+"/"+liveID+".kif" {
		t.Errorf("KifuURL = %q", got)
	}
	// 諸元として残すのは人が開ける中継ページのほう。
	if got := sl.ViewerURL(liveID); got != "http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html" {
		t.Errorf("ViewerURL = %q", got)
	}
	// https では繋がらないので基点は http のままにしてある。
	if !strings.HasPrefix(ShogiLiveBaseURL, "http://") {
		t.Errorf("ShogiLiveBaseURL = %q（https にすると取得できなくなる）", ShogiLiveBaseURL)
	}
}

// 取得タブの入力をどちらの取得元に回すかの判定。
func TestIsShogiLiveURL(t *testing.T) {
	cases := map[string]bool{
		"http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html":   true,
		"https://live.shogi.or.jp/oui/kifu/67/oui202607290101.kif":   true,
		"http://LIVE.SHOGI.OR.JP/oui/":                               true,
		"https://www.yomiuri.co.jp/igoshougi/ryuoh/kifu/20241114-x/": false,
		"66f2539c848c20bac7cb8002":                                   false,
		"":                                                           false,
	}
	for in, want := range cases {
		if got := IsShogiLiveURL(in); got != want {
			t.Errorf("IsShogiLiveURL(%q) = %v, want %v", in, got, want)
		}
	}
}
