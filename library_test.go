package kicho

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ShinteLab/kicho/scrape"
	"github.com/ShinteLab/kicho/store"
)

// 諸元（source_url）は取得元から決め直すこと。
//
// Fetched は UI を往復してくるので、画面が持っている値をそのまま信じない。
// 以前は kicho の Wails サービスと ikkyoku がそれぞれ組み立てていて、
// 片方だけ直せば黙って挙動が割れる状態だった。
func TestSaveDerivesSourceURL(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	tests := []struct {
		name   string
		in     Fetched
		want   string
		source string
	}{
		{
			name:   "読売は棋譜ビューアの URL",
			in:     Fetched{Source: store.SourceYomiuri, SourceID: "66f2539c848c20bac7cb8002", SourceURL: "https://example.com/でたらめ", KIF: importSample},
			want:   scrape.ViewerURL("66f2539c848c20bac7cb8002"),
			source: store.SourceYomiuri,
		},
		{
			name:   "連盟は中継ページ(HTML)の URL",
			in:     Fetched{Source: store.SourceShogiLive, SourceID: "oui/kifu/67/oui202607290101", KIF: importSample},
			want:   "http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html",
			source: store.SourceShogiLive,
		},
		{
			name:   "取得元が空でも読売として保存できる",
			in:     Fetched{SourceID: "66f2539c848c20bac7cb8002", KIF: importSample},
			want:   scrape.ViewerURL("66f2539c848c20bac7cb8002"),
			source: store.SourceYomiuri,
		},
		{
			name:   "URL 取り込みは貼られた URL をそのまま残す",
			in:     Fetched{Source: store.SourceURL, SourceID: "any", SourceURL: "https://example.com/a.kif", KIF: importSample},
			want:   "https://example.com/a.kif",
			source: store.SourceURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, err := lib.Save(ctx, tt.in)
			if err != nil {
				t.Fatalf("Save: %v", err)
			}
			if rec.SourceURL != tt.want {
				t.Errorf("SourceURL = %q, want %q", rec.SourceURL, tt.want)
			}
			if rec.Source != tt.source {
				t.Errorf("Source = %q, want %q", rec.Source, tt.source)
			}
		})
	}
}

// 手数は本文から数え直すこと（画面が持っている値を信じない）。
func TestSaveRecountsMoves(t *testing.T) {
	lib := newTestLibrary(t)

	// importSample は 2 手 + 投了。フロントから出鱈目な手数が来ても直す。
	rec, err := lib.Save(context.Background(), Fetched{
		Source: store.SourceYomiuri, SourceID: "x", KIF: importSample, Moves: 999,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := countMoves(importSample); rec.Moves != want {
		t.Errorf("Moves = %d, want %d", rec.Moves, want)
	}
}

// 保存できない取得元は sentinel で返すこと（文言比較で分岐させないため）。
func TestSaveRejectsUnknownSource(t *testing.T) {
	lib := newTestLibrary(t)

	_, err := lib.Save(context.Background(), Fetched{Source: "yahoo", SourceID: "x", KIF: importSample})
	if !errors.Is(err, ErrUnsupportedSource) {
		t.Errorf("err = %v, want ErrUnsupportedSource", err)
	}
}

// 本文が空、棋譜 ID が空はそれぞれ sentinel で返すこと。
func TestSaveValidates(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	if _, err := lib.Save(ctx, Fetched{Source: store.SourceYomiuri, SourceID: "x", KIF: "  \n"}); !errors.Is(err, ErrEmptyKifu) {
		t.Errorf("空の本文: err = %v, want ErrEmptyKifu", err)
	}
	if _, err := lib.Save(ctx, Fetched{Source: store.SourceYomiuri, KIF: importSample}); !errors.Is(err, ErrNoInput) {
		t.Errorf("空の棋譜 ID: err = %v, want ErrNoInput", err)
	}
}

// 取り込みの入り口が sentinel を返すこと。
//
// ⚠️ **手数 0 は ErrNotKifu ではない**（対局前の中継棋譜は正当に存在する）。
func TestImportSentinels(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	if _, err := lib.ImportKIF(ctx, "   "); !errors.Is(err, ErrEmptyKifu) {
		t.Errorf("空: err = %v, want ErrEmptyKifu", err)
	}
	if _, err := lib.ImportKIF(ctx, "<html><body>これは棋譜ではない</body></html>"); !errors.Is(err, ErrNotKifu) {
		t.Errorf("KIF ではない: err = %v, want ErrNotKifu", err)
	}

	// ヘッダだけで 1 手も無い .kif（対局前）は取り込める。
	beforeStart := "棋戦：テスト棋戦\n先手：先手 太郎\n後手：後手 次郎\n"
	rec, err := lib.ImportKIF(ctx, beforeStart)
	if err != nil {
		t.Fatalf("対局前の棋譜が取り込めない: %v", err)
	}
	if rec.Moves != 0 {
		t.Errorf("Moves = %d, want 0", rec.Moves)
	}
}

// Fetch は入力が空なら ErrNoInput（サイトへは行かない）。
func TestFetchRejectsEmptyInput(t *testing.T) {
	lib := newTestLibrary(t)
	if _, err := lib.Fetch(context.Background(), "  "); !errors.Is(err, ErrNoInput) {
		t.Errorf("err = %v, want ErrNoInput", err)
	}
}

// Refresh が弾くのは paste だけ（2026-09-12）。
//
// ⚠️ **url を弾かないことが要点。** あちらの sourceID は取得に使った URL
// そのものなので（`sourceIDForURL`）、もう一度そこへ行けば取り直せる。
// ここを「取り込み系はまとめて弾く」に戻すと、**.kif の URL のカードが
// 「更新」できなくなる。**
func TestRefreshRejectsPasteOnly(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	if _, err := lib.Refresh(ctx, store.SourcePaste, "id"); !errors.Is(err, ErrUnsupportedSource) {
		t.Errorf("paste: err = %v, want ErrUnsupportedSource", err)
	}
	// url は「取り直せない取得元」ではない（取りに行って失敗するのは別の話）。
	if _, err := lib.Refresh(ctx, store.SourceURL, "id"); errors.Is(err, ErrUnsupportedSource) {
		t.Errorf("url を取り直せない扱いにしている: err = %v", err)
	}
	if _, err := lib.Refresh(ctx, store.SourceYomiuri, " "); !errors.Is(err, ErrNoInput) {
		t.Errorf("空の棋譜 ID: err = %v, want ErrNoInput", err)
	}
}

// 一覧は件数無制限にしないこと。**棋譜を溜め込んでいく前提**なので、
// 上限が無いと蔵書が増えたぶんだけ全行が JSON に載る。
func TestSearchIsBounded(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	const n = MaxSearchRows + 5
	for i := 0; i < n; i++ {
		if _, err := lib.Save(ctx, Fetched{
			Source:   store.SourceURL,
			SourceID: fmt.Sprintf("id-%03d", i),
			Event:    "テスト棋戦",
			KIF:      importSample,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Limit 0（無制限のつもり）でも上限が掛かること。
	got, err := lib.Search(ctx, store.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Games) != MaxSearchRows {
		t.Errorf("件数 = %d, want %d", len(got.Games), MaxSearchRows)
	}
	if !got.Truncated {
		t.Error("Truncated = false（上限で切ったのに伝わっていない）")
	}
	if got.Total != n || got.Matched != n {
		t.Errorf("Total = %d, Matched = %d, want %d", got.Total, got.Matched, n)
	}

	// 条件付きの該当件数は全体と別に返ること（UI の「N 件中 M 件」に使う）。
	got, err = lib.Search(ctx, store.Query{Text: "id-001", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != n {
		t.Errorf("Total = %d, want %d", got.Total, n)
	}
	if got.Truncated {
		t.Error("Truncated = true（切っていないのに立っている）")
	}
}

// 「再読み込みで取り直せる URL か」の判断は kicho が持つこと。
//
// ⚠️ **判定は「`Fetch` にその URL を渡せば同じ棋譜が取れるか」**（2026-09-12）。
// 「その URL を .kif として読めるか」ではない —— 読売は .kif を置いていないが、
// `Fetch` がビューアの URL を棋譜 ID に解決するので取り直せる。
// **取り直せないのは貼り付けだけ**（取得元が無い）。
func TestRefetchableURL(t *testing.T) {
	tests := []struct {
		source, url, want string
	}{
		{store.SourceYomiuri, "https://www.yomiuri.co.jp/kifu/s/abc/", "https://www.yomiuri.co.jp/kifu/s/abc/"},
		{store.SourceShogiLive, "http://live.shogi.or.jp/oui/kifu/67/x.html", "http://live.shogi.or.jp/oui/kifu/67/x.html"},
		{store.SourceURL, "https://example.com/a.kif", "https://example.com/a.kif"},
		{store.SourcePaste, "", ""},
	}
	for _, tt := range tests {
		if got := RefetchableURL(tt.source, tt.url); got != tt.want {
			t.Errorf("RefetchableURL(%q, %q) = %q, want %q", tt.source, tt.url, got, tt.want)
		}
	}
}

// PreviewKIF は保存せずに内容だけ返すこと。
func TestPreviewKIFDoesNotSave(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	f, err := lib.PreviewKIF(importSample)
	if err != nil {
		t.Fatal(err)
	}
	if f.Event != "テスト棋戦" {
		t.Errorf("Event = %q", f.Event)
	}
	// 表示する KIF は原本そのまま（組み立て直さない）。
	if f.KIF != importSample {
		t.Error("KIF が原本と違う")
	}
	if !strings.Contains(f.EndMark, "投了") {
		t.Errorf("EndMark = %q, want 投了", f.EndMark)
	}

	n, err := lib.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("保存されている: %d 件", n)
	}
}
