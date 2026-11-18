package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestAddRemoveFlags(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec, err := s.Save(ctx, sampleGame())
	if err != nil {
		t.Fatal(err)
	}

	whole, err := s.AddFlag(ctx, rec.ID, 0, "  先後が逆かもしれない ", "user")
	if err != nil {
		t.Fatal(err)
	}
	if whole.Reason != "先後が逆かもしれない" || whole.By != "user" || whole.BodyDigest != rec.BodyDigest {
		t.Errorf("flag = %+v", whole)
	}
	if _, err := s.AddFlag(ctx, rec.ID, 74, "二歩", "ikkyoku"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFlag(ctx, rec.ID, 30, "記録と違う", "user"); err != nil {
		t.Fatal(err)
	}

	flags, err := s.Flags(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 3 || flags[0].Move != 0 || flags[1].Move != 30 || flags[2].Move != 74 {
		t.Fatalf("Flags の並び = %+v", flags)
	}
	for _, f := range flags {
		if f.Stale {
			t.Errorf("付けたばかりなのに Stale: %+v", f)
		}
	}

	got, err := s.Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 棋譜全体の指摘（0）は FlaggedMove に入らない。
	if got.Flags != 3 || got.FlaggedMove != 30 || got.FlagsStale {
		t.Errorf("Flags = %d, FlaggedMove = %d, FlagsStale = %v", got.Flags, got.FlaggedMove, got.FlagsStale)
	}

	if err := s.RemoveFlag(ctx, flags[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFlag(ctx, flags[1].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("二度目の RemoveFlag = %v, want ErrNotFound", err)
	}
	got, _ = s.Get(ctx, rec.ID)
	if got.Flags != 2 || got.FlaggedMove != 74 {
		t.Errorf("外したあと: Flags = %d, FlaggedMove = %d", got.Flags, got.FlaggedMove)
	}
}

func TestAddFlagValidates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec, err := s.Save(ctx, sampleGame())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFlag(ctx, "nothing", 1, "x", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("棋譜が無い: %v", err)
	}
	if _, err := s.AddFlag(ctx, rec.ID, -1, "x", ""); err == nil {
		t.Error("負の手を受けた")
	}
	if _, err := s.AddFlag(ctx, rec.ID, 1, "  ", ""); err == nil {
		t.Error("空の理由を受けた")
	}
	if got, _ := s.Get(ctx, rec.ID); got.Flags != 0 {
		t.Errorf("断ったのに指摘が増えた: %d", got.Flags)
	}
}

// 取り直して保存しても指摘は残る。本文が変われば Stale、同じ本文なら立たない。
func TestFlagsSurviveResaveAndGoStale(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec, err := s.Save(ctx, sampleGame())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFlag(ctx, rec.ID, 1, "記録と違う", "user"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Save(ctx, sampleGame()); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, rec.ID)
	if got.Flags != 1 || got.FlagsStale {
		t.Fatalf("同じ本文で保存し直した: Flags = %d, FlagsStale = %v", got.Flags, got.FlagsStale)
	}

	changed := sampleGame()
	changed.Body += "2 ８四歩(83)\n"
	if _, err := s.Save(ctx, changed); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, rec.ID)
	if got.Flags != 1 || !got.FlagsStale {
		t.Fatalf("本文が変わった: Flags = %d, FlagsStale = %v", got.Flags, got.FlagsStale)
	}
	flags, _ := s.Flags(ctx, rec.ID)
	if len(flags) != 1 || !flags[0].Stale {
		t.Errorf("Flag.Stale が立たない: %+v", flags)
	}
}

// 棋譜を消したら指摘も消える（派生棋譜と違って残さない）。
func TestFlagsAreDeletedWithGame(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec, err := s.Save(ctx, sampleGame())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFlag(ctx, rec.ID, 1, "記録と違う", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM game_flags`).Scan(&n); err != nil || n != 0 {
		t.Errorf("game_flags に %d 件残った (%v)", n, err)
	}
}

// 「要確認」はエラーの印か指摘のある棋譜。Search と CountQuery の件数が揃うこと。
func TestNeedsAttention(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	clean := sampleGame()
	clean.SourceID = "clean"
	if _, err := s.Save(ctx, clean); err != nil {
		t.Fatal(err)
	}
	broken := sampleGame()
	broken.SourceID = "broken"
	broken.ErrorMove, broken.Error = 5, "盤に当てられない"
	brokenRec, err := s.Save(ctx, broken)
	if err != nil {
		t.Fatal(err)
	}
	flagged := sampleGame()
	flagged.SourceID = "flagged"
	flaggedRec, err := s.Save(ctx, flagged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFlag(ctx, flaggedRec.ID, 3, "二歩", "ikkyoku"); err != nil {
		t.Fatal(err)
	}

	q := Query{NeedsAttention: true}
	found, err := s.Search(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.CountQuery(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || n != 2 {
		t.Fatalf("Search = %v, CountQuery = %d, want 2", ids(found), n)
	}
	got := map[string]bool{}
	for _, r := range found {
		got[r.ID] = true
	}
	if !got[brokenRec.ID] || !got[flaggedRec.ID] {
		t.Errorf("Search = %v", ids(found))
	}
	// 他の条件とも組み合わさる（全件は 3、要確認は 2）。
	if all, _ := s.CountQuery(ctx, Query{}); all != 3 {
		t.Errorf("CountQuery(全件) = %d", all)
	}
}

// v6 の DB（game_flags が無い）を開くと v7 になり、既存の棋譜は変わらず指摘を付けられる。
func TestMigrateV6ToV7(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v6.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.Save(context.Background(), sampleGame())
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// v6 の形にする（v7 は v6 に game_flags を足しただけ）。
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE game_flags; PRAGMA user_version = 6;`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	for i := 0; i < 2; i++ { // 2 回開いても壊れない（何度流しても同じ）
		s, err = Open(path)
		if err != nil {
			t.Fatalf("Open on v6 schema: %v", err)
		}
		var version int
		s.db.QueryRow(`PRAGMA user_version`).Scan(&version)
		if version != schemaVersion || schemaVersion != 7 {
			t.Errorf("user_version = %d, schemaVersion = %d", version, schemaVersion)
		}
		got, err := s.Get(context.Background(), rec.ID)
		if err != nil || got.Body != rec.Body || got.Flags != i {
			s.Close()
			t.Fatalf("%d 回目: Body 一致 = %v, Flags = %d, err = %v", i+1, got.Body == rec.Body, got.Flags, err)
		}
		if i == 0 {
			if _, err := s.AddFlag(context.Background(), rec.ID, 1, "記録と違う", ""); err != nil {
				t.Fatal(err)
			}
		}
		s.Close()
	}
}
