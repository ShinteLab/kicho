package kicho

import (
	"context"
	"errors"
	"testing"

	"github.com/ShinteLab/kicho/store"
)

func TestLibraryFlags(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()
	rec, err := lib.ImportKIF(ctx, importSample)
	if err != nil {
		t.Fatal(err)
	}

	f, err := lib.Flag(ctx, rec.ID, 1, "記録と違う", "ikkyoku")
	if err != nil {
		t.Fatal(err)
	}
	if f.Move != 1 || f.By != "ikkyoku" || f.GameID != rec.ID {
		t.Errorf("Flag = %+v", f)
	}
	got, err := lib.Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 指摘はエラーの印とは別。エラーの印は本文から決まったまま。
	if got.Flags != 1 || got.FlaggedMove != 1 || got.ErrorMove != 0 {
		t.Errorf("Flags = %d, FlaggedMove = %d, ErrorMove = %d", got.Flags, got.FlaggedMove, got.ErrorMove)
	}

	flags, err := lib.Flags(ctx, rec.ID)
	if err != nil || len(flags) != 1 || flags[0].ID != f.ID {
		t.Fatalf("Flags = %+v, %v", flags, err)
	}
	if err := lib.Unflag(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.Unflag(ctx, f.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("二度目の Unflag = %v", err)
	}
	if flags, _ := lib.Flags(ctx, rec.ID); len(flags) != 0 {
		t.Errorf("外したのに残る: %+v", flags)
	}
}

func TestLibraryFlagValidates(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()
	rec, err := lib.ImportKIF(ctx, importSample)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Flag(ctx, rec.ID, 1, " ", ""); !errors.Is(err, ErrNoInput) {
		t.Errorf("空の理由 = %v, want ErrNoInput", err)
	}
	if _, err := lib.Flag(ctx, rec.ID, -1, "x", ""); !errors.Is(err, ErrInvalidMove) {
		t.Errorf("負の手 = %v, want ErrInvalidMove", err)
	}
	if _, err := lib.Flag(ctx, "nothing", 1, "x", ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("棋譜が無い = %v, want store.ErrNotFound", err)
	}
}

// 取り直して保存しても指摘は消えない（Save は指摘に触らない）。
func TestFlagSurvivesLibrarySave(t *testing.T) {
	lib := newTestLibrary(t)
	ctx := context.Background()
	in := Fetched{Source: store.SourceShogiDB2, SourceID: db2ID, KIF: importSample}
	rec, err := lib.Save(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Flag(ctx, rec.ID, 0, "先後が逆かもしれない", "user"); err != nil {
		t.Fatal(err)
	}
	again, err := lib.Save(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != rec.ID || again.Flags != 1 || again.FlaggedMove != 0 || again.FlagsStale {
		t.Errorf("保存し直したあと: ID 同じ = %v, Flags = %d, FlaggedMove = %d, FlagsStale = %v",
			again.ID == rec.ID, again.Flags, again.FlaggedMove, again.FlagsStale)
	}
}
