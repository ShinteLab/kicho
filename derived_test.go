package kicho

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ShinteLab/kicho/store"
)

// 手番の食い違いで 2 手目から読めない CSA（`TestImportCSABrokenMoves` と同じもの）。
const brokenCSA = "PI\n+\n+7776FU\n+3334FU\n"

// 盤に当てられない手のある本文は、保存は断らずにエラーの印を付ける。
func TestSaveMarksBodyErrors(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	rec, err := lib.ImportKIF(ctx, brokenCSA)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ErrorMove != 2 || rec.Error == "" || rec.Body != brokenCSA {
		t.Errorf("ErrorMove = %d, Error = %q, Body = %q", rec.ErrorMove, rec.Error, rec.Body)
	}

	// Save も画面の値を信じず本文から決め直す。
	f := Fetched{Source: store.SourceURL, SourceID: "https://example.com/a.csa", KIF: brokenCSA, ErrorMove: 0}
	rec, err = lib.Save(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ErrorMove != 2 {
		t.Errorf("Save: ErrorMove = %d, want 2", rec.ErrorMove)
	}
	f = Fetched{Source: store.SourceURL, SourceID: "https://example.com/b.kif", KIF: importSample, ErrorMove: 9, Error: "でたらめ"}
	rec, err = lib.Save(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ErrorMove != 0 || rec.Error != "" {
		t.Errorf("画面の値を信じた: ErrorMove = %d, Error = %q", rec.ErrorMove, rec.Error)
	}

	// 取得の結果（Fetched）にも付く。
	got, err := lib.PreviewKIF(brokenCSA)
	if err != nil {
		t.Fatal(err)
	}
	if got.ErrorMove != 2 || got.Error == "" {
		t.Errorf("PreviewKIF: ErrorMove = %d, Error = %q", got.ErrorMove, got.Error)
	}
}

func TestSaveDerived(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()

	parent, err := lib.ImportKIF(ctx, brokenCSA)
	if err != nil {
		t.Fatal(err)
	}
	// 手だけの KIF で訂正する。対局名などは親の値で埋まる。
	parent, err = lib.Annotate(ctx, parent.ID, Annotation{Note: "親"})
	if err != nil {
		t.Fatal(err)
	}
	fixed := "手合割：平手\n手数----指手---------消費時間--\n1 ７六歩(77)\n2 ３四歩(33)\n"
	child, err := lib.SaveDerived(ctx, parent.ID, store.RelationCorrection, fixed)
	if err != nil {
		t.Fatal(err)
	}
	if child.Source != store.SourceDerived || child.ParentID != parent.ID || child.Relation != store.RelationCorrection {
		t.Errorf("child = %+v", child)
	}
	if child.Body != fixed || child.Format != "kif" || child.Moves != 2 || child.ErrorMove != 0 {
		t.Errorf("Body/Format/Moves/ErrorMove = %q/%q/%d/%d", child.Body, child.Format, child.Moves, child.ErrorMove)
	}
	if child.Handicap != "平手" {
		t.Errorf("Handicap = %q", child.Handicap)
	}
	if child.Note != "" {
		t.Error("人が書く欄まで親から写った")
	}

	kids, err := lib.Children(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 || kids[0].ID != child.ID {
		t.Errorf("Children = %v", kids)
	}

	// 対局名を持つ KIF ならそちらを使い、空の欄だけ親で埋める。
	withEvent := "棋戦：訂正版\n" + fixed
	child2, err := lib.SaveDerived(ctx, parent.ID, store.RelationCorrection, withEvent)
	if err != nil {
		t.Fatal(err)
	}
	if child2.Event != "訂正版" {
		t.Errorf("Event = %q", child2.Event)
	}
}

func TestSaveDerivedMetadataFallback(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()
	parent, err := lib.ImportKIF(ctx, importSample)
	if err != nil {
		t.Fatal(err)
	}
	child, err := lib.SaveDerived(ctx, parent.ID, store.RelationCorrection,
		"手数----指手---------消費時間--\n1 ７六歩(77)\n")
	if err != nil {
		t.Fatal(err)
	}
	if child.Event != "テスト棋戦" || child.Black != "先手 太郎" || child.White != "後手 次郎" ||
		child.Place != "テスト会館" || !child.StartedAt.Equal(parent.StartedAt) {
		t.Errorf("親の値で埋まっていない: %+v", child)
	}
}

func TestSaveDerivedRejects(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()
	parent, err := lib.ImportKIF(ctx, importSample)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SaveDerived(ctx, parent.ID, " ", importSample); !errors.Is(err, ErrNoInput) {
		t.Errorf("関係が空: err = %v", err)
	}
	if _, err := lib.SaveDerived(ctx, "nothing", store.RelationCorrection, importSample); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("親が無い: err = %v", err)
	}
	if _, err := lib.SaveDerived(ctx, parent.ID, store.RelationCorrection, "ただの文章"); !errors.Is(err, ErrNotKifu) {
		t.Errorf("棋譜でない: err = %v", err)
	}
}

// 派生棋譜は Save・追跡・取り直しのどれも通らない（親の無い子・押すと必ず失敗するカードを作らない）。
func TestDerivedIsNotRefetchable(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()
	f := Fetched{Source: store.SourceDerived, SourceID: "x", KIF: importSample}

	if _, err := lib.Save(ctx, f); !errors.Is(err, ErrUnsupportedSource) {
		t.Errorf("Save: err = %v", err)
	}
	if _, err := lib.Watch(ctx, f); !errors.Is(err, ErrUnsupportedSource) {
		t.Errorf("Watch: err = %v", err)
	}
	if _, err := lib.Refresh(ctx, store.SourceDerived, "x"); !errors.Is(err, ErrUnsupportedSource) {
		t.Errorf("Refresh: err = %v", err)
	}
	if got := RefetchableURL(store.SourceDerived, "https://example.com/"); got != "" {
		t.Errorf("RefetchableURL = %q", got)
	}
	if !strings.Contains(RefetchableURL(store.SourceURL, "https://example.com/"), "example.com") {
		t.Error("url まで取り直せなくなった")
	}
}
