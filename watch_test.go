package kicho

import (
	"context"
	"errors"
	"testing"

	"github.com/ShinteLab/kicho/scrape"
	"github.com/ShinteLab/kicho/store"
)

// 諸元（source_url）は追跡でも取得元から決め直すこと。
//
// Fetched は UI を往復してくるので画面の値をそのまま信じない
// （復元したカードの「取得 URL」がそこから作られる）。
func TestWatchDerivesSourceURL(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	tests := []struct {
		name string
		in   Fetched
		want string
	}{
		{
			name: "読売は棋譜ビューアの URL",
			in:   Fetched{Source: store.SourceYomiuri, SourceID: "66f2539c848c20bac7cb8002", SourceURL: "https://example.com/でたらめ"},
			want: scrape.ViewerURL("66f2539c848c20bac7cb8002"),
		},
		{
			name: "連盟は中継ページ(HTML)の URL",
			in:   Fetched{Source: store.SourceShogiLive, SourceID: "oui/kifu/67/oui202607290101"},
			want: "http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := lib.Watch(ctx, tt.in)
			if err != nil {
				t.Fatalf("Watch: %v", err)
			}
			if w.SourceURL != tt.want {
				t.Errorf("SourceURL = %q, want %q", w.SourceURL, tt.want)
			}
		})
	}
}

// 追跡できないのは paste だけ。
//
// 復元しても「更新」が必ず失敗するカードを作らないための蓋なので、
// 判定は `Refresh` が受け付ける取得元と揃っていること。
// ⚠️ **url を弾かないこと** —— sourceId が URL そのものになったので取り直せる。
func TestWatchRejectsPasteOnly(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	_, err := lib.Watch(ctx, Fetched{Source: store.SourcePaste, SourceID: "some-uuid"})
	if !errors.Is(err, ErrUnsupportedSource) {
		t.Errorf("paste: err = %v, want ErrUnsupportedSource", err)
	}

	// url は載る。諸元が空でも sourceId（＝URL）で補われること。
	w, err := lib.Watch(ctx, Fetched{
		Source:   store.SourceURL,
		SourceID: "https://example.test/kifu/x.kif",
	})
	if err != nil {
		t.Fatalf("url を追跡できない: %v", err)
	}
	if w.SourceURL != "https://example.test/kifu/x.kif" {
		t.Errorf("SourceURL = %q", w.SourceURL)
	}

	if _, err := lib.Watch(ctx, Fetched{Source: store.SourceYomiuri}); !errors.Is(err, ErrNoInput) {
		t.Errorf("棋譜 ID が空: err = %v, want ErrNoInput", err)
	}
}

// 終局済みを保存したら追跡をやめること。**対局中は残すこと。**
//
// 2日制なら1日目の封じ手時点で保存しても翌日同じカードで追うので、
// そこで一覧から消えては困る。
func TestSaveUnwatchesOnlyFinished(t *testing.T) {
	ctx := context.Background()

	inProgress := Fetched{
		Source:   store.SourceShogiLive,
		SourceID: "oui/kifu/67/oui202607290101",
		Event:    "第67期王位戦七番勝負第３局",
		KIF:      "手合割：平手\n手数----指手---------消費時間--\n   1 ７六歩(77)\n",
	}
	finished := inProgress
	finished.EndMark = "投了"
	finished.KIF = importSample

	t.Run("対局中は残る", func(t *testing.T) {
		lib := newTestLibrary(t)
		if _, err := lib.Watch(ctx, inProgress); err != nil {
			t.Fatal(err)
		}
		if _, err := lib.Save(ctx, inProgress); err != nil {
			t.Fatalf("Save: %v", err)
		}
		list, err := lib.Watches(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 {
			t.Fatalf("len = %d, want 1（対局中の保存では外さない）", len(list))
		}
	})

	t.Run("終局済みは外れる", func(t *testing.T) {
		lib := newTestLibrary(t)
		if _, err := lib.Watch(ctx, inProgress); err != nil {
			t.Fatal(err)
		}
		rec, err := lib.Save(ctx, finished)
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		list, err := lib.Watches(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 0 {
			t.Errorf("len = %d, want 0（終局したら追跡をやめる）", len(list))
		}
		// 棋譜のほうは残っていること。
		if _, err := lib.Get(ctx, rec.ID); err != nil {
			t.Errorf("保存済みの棋譜が消えている: %v", err)
		}
	})

	// 追跡していない棋譜を保存しても失敗しないこと。
	t.Run("追跡していなくても保存できる", func(t *testing.T) {
		lib := newTestLibrary(t)
		if _, err := lib.Save(ctx, finished); err != nil {
			t.Fatalf("Save: %v", err)
		}
	})
}

// 復元用の一覧が取れること（棋譜本文は持たない）。
func TestWatchesRoundTrip(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	in := Fetched{
		Source:   store.SourceShogiLive,
		SourceID: "oui/kifu/67/oui202607290101",
		Event:    "第67期王位戦七番勝負第３局",
		Black:    "先手 太郎",
		White:    "後手 次郎",
		Moves:    40,
		KIF:      "本文は追跡一覧には入らない",
	}
	if _, err := lib.Watch(ctx, in); err != nil {
		t.Fatal(err)
	}

	list, err := lib.Watches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	got := list[0]
	if got.Event != in.Event || got.Black != in.Black || got.White != in.White || got.Moves != 40 {
		t.Errorf("復元用のメタが揃っていない: %+v", got)
	}

	if err := lib.Unwatch(ctx, got.Source, got.SourceID); err != nil {
		t.Fatalf("Unwatch: %v", err)
	}
	if err := lib.Unwatch(ctx, got.Source, got.SourceID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("2 度目の Unwatch: err = %v, want store.ErrNotFound", err)
	}

	n, err := lib.UnwatchAll(ctx)
	if err != nil || n != 0 {
		t.Errorf("UnwatchAll = %d, %v", n, err)
	}
}
