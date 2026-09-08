package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func sampleWatch() Watch {
	return Watch{
		Source:    SourceShogiLive,
		SourceID:  "oui/kifu/67/oui202607290101",
		SourceURL: "http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html",
		Event:     "第67期王位戦七番勝負第３局",
		Black:     "先手 太郎",
		White:     "後手 次郎",
		StartedAt: time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC),
		Moves:     40,
	}
}

// 同じ中継を取り直しても増えず、追跡し始めた時刻は維持されること。
//
// **CreatedAt が動くと一覧の並びが取り直すたびに入れ替わる。**
// フロントは取得したカードを先頭に積んでその場で最新化するので、
// 復元もその順のままでないと 2 日目にカードの位置が変わってしまう。
func TestSaveWatchKeepsCreatedAtOnRefetch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, err := s.SaveWatch(ctx, sampleWatch())
	if err != nil {
		t.Fatalf("SaveWatch: %v", err)
	}

	w := sampleWatch()
	w.Moves = 104
	w.EndMark = "投了"
	second, err := s.SaveWatch(ctx, w)
	if err != nil {
		t.Fatalf("SaveWatch(2): %v", err)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", second.CreatedAt, first.CreatedAt)
	}

	list, err := s.Watches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1（取り直しても増えない）", len(list))
	}
	if list[0].Moves != 104 || list[0].EndMark != "投了" {
		t.Errorf("内容が最新化されていない: %+v", list[0])
	}
	if !list[0].Finished() {
		t.Error("Finished = false, want true")
	}
}

// 一覧は追跡し始めたのが新しいものから並ぶこと。
func TestWatchesNewestFirst(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b", "c"} {
		w := sampleWatch()
		w.SourceID = id
		if _, err := s.SaveWatch(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	// 途中で取り直しても位置は動かない。
	w := sampleWatch()
	w.SourceID = "a"
	w.Moves = 99
	if _, err := s.SaveWatch(ctx, w); err != nil {
		t.Fatal(err)
	}

	list, err := s.Watches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range list {
		got = append(got, w.SourceID)
	}
	want := []string{"c", "b", "a"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("順序 = %v, want %v", got, want)
		}
	}
}

// 追跡をやめても保存済みの棋譜は消えないこと（仮の一覧から外すだけ）。
func TestDeleteWatchKeepsSavedGame(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	g := sampleGame()
	rec, err := s.Save(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	w := sampleWatch()
	w.Source, w.SourceID = g.Source, g.SourceID
	if _, err := s.SaveWatch(ctx, w); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteWatch(ctx, g.Source, g.SourceID); err != nil {
		t.Fatalf("DeleteWatch: %v", err)
	}
	if _, err := s.Get(ctx, rec.ID); err != nil {
		t.Errorf("保存済みの棋譜が消えている: %v", err)
	}

	list, err := s.Watches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("len = %d, want 0", len(list))
	}
}

func TestDeleteWatchNotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.DeleteWatch(context.Background(), SourceYomiuri, "ない")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteWatchesClearsAll(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		w := sampleWatch()
		w.SourceID = id
		if _, err := s.SaveWatch(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.DeleteWatches(ctx)
	if err != nil {
		t.Fatalf("DeleteWatches: %v", err)
	}
	if n != 2 {
		t.Errorf("n = %d, want 2", n)
	}
	list, err := s.Watches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("len = %d, want 0", len(list))
	}
}

func TestSaveWatchRequiresSource(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.SaveWatch(context.Background(), Watch{SourceID: "x"}); err == nil {
		t.Error("取得元が空でも保存できてしまった")
	}
}

// v3（watches が無い）DB を開いたら追跡一覧が足され、蔵書は失われないこと。
func TestMigrateV3ToV4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// v3 の形は「最新から watches を抜いたもの」。
	if _, err := db.Exec(gamesSchemaSQL); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
        INSERT INTO games (id, source, source_id, event, moves, created_at, updated_at)
        VALUES ('v3-id', 'yomiuri', 'abc', 'v3 データ', 3, 1, 1);
        INSERT INTO game_kifu (game_id, format, encoding, body)
        VALUES ('v3-id', 'kif', 'utf-8', '手合割：平手' || char(10));
        PRAGMA user_version = 3;`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on v3 schema: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	got, err := s.Get(ctx, "v3-id")
	if err != nil {
		t.Fatalf("Get after migration: %v", err)
	}
	if got.Event != "v3 データ" {
		t.Errorf("既存行が失われている: %+v", got.Game)
	}

	if _, err := s.SaveWatch(ctx, sampleWatch()); err != nil {
		t.Fatalf("SaveWatch after migration: %v", err)
	}

	var version int
	s.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != schemaVersion {
		t.Errorf("user_version = %d, want %d", version, schemaVersion)
	}
}
