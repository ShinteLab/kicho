package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sub", "kicho.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleGame() Game {
	return Game{
		Source:    SourceYomiuri,
		SourceID:  "67037f045dcc1abf6b9528e3",
		Event:     "第37期竜王戦七番勝負第２局",
		Handicap:  "平手",
		Place:     "福井県あわら市",
		Black:     "佐々木勇気八段",
		White:     "藤井聡太竜王",
		StartedAt: time.Date(2024, 10, 19, 9, 0, 0, 0, time.UTC),
		EndMark:   "投了",
		Moves:     1,
		Body:      "手合割：平手\n手数----指手---------消費時間--\n1 ７六歩(77)\n",
	}
}

// 手数は列として保持され、KIF 本文を返さない一覧でも取得できること。
func TestListSummaryKeepsMoves(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	g := sampleGame()
	g.Moves = 104
	if _, err := s.Save(ctx, g); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].Moves != 104 {
		t.Errorf("Moves = %d, want 104", list[0].Moves)
	}
	if list[0].Body != "" {
		t.Errorf("ListSummary should still omit the KIF body, got %q", list[0].Body)
	}
}

// 対局中に保存した棋譜は EndMark が空のまま保持されること。
func TestSaveInProgressGame(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	g := sampleGame()
	g.EndMark = ""
	rec, err := s.Save(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Finished() {
		t.Error("Finished() should be false while the game is in progress")
	}

	got, err := s.Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EndMark != "" || got.Finished() {
		t.Errorf("EndMark = %q, Finished = %v", got.EndMark, got.Finished())
	}

	// 終局後に取り直して保存すると終局済みになる。
	g.EndMark = "投了"
	if _, err := s.Save(ctx, g); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Finished() {
		t.Errorf("re-save after the game ended should mark it finished, got %+v", got.Game)
	}
}

// v0(KIF 同居・時刻 TEXT)の DB を v1 へ移行できること。
// 実データが懸かるので、時刻の変換・本文の移動・FTS の構築まで確認する。
func TestMigrateV0ToV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v0.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// 実際に使われていた v0 スキーマ(end_mark / moves は ALTER 済みの状態)。
	_, err = db.Exec(`
        CREATE TABLE games (
            id TEXT PRIMARY KEY, source TEXT NOT NULL, source_id TEXT NOT NULL,
            event TEXT NOT NULL DEFAULT '', handicap TEXT NOT NULL DEFAULT '',
            place TEXT NOT NULL DEFAULT '', black TEXT NOT NULL DEFAULT '',
            white TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL DEFAULT '',
            kif TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
            , end_mark TEXT NOT NULL DEFAULT '', moves INTEGER NOT NULL DEFAULT 0);
        CREATE UNIQUE INDEX idx_games_source ON games(source, source_id);
        CREATE INDEX idx_games_started_at ON games(started_at DESC);

        INSERT INTO games VALUES
          ('id-jst','yomiuri','6a421e0b','第39期竜王戦決勝トーナメント','平手','',
           '柵木幹太五段','斎藤慎太郎八段','2026-07-23T10:00:00+09:00',
           '手合割：平手' || char(10) || '1 ７六歩(77)' || char(10),
           '2026-07-24T22:26:19Z','2026-07-24T22:39:32Z','投了',140),
          ('id-empty','yomiuri','xxxx','開始日時なし','平手','','先手','後手','',
           '手合割：平手' || char(10),
           '2026-07-24T23:18:04Z','2026-07-24T23:18:04Z','',0);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on v0 schema: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// 版が記録されていること(2回目の Open で再移行しない)。
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Errorf("user_version = %d, want %d", version, schemaVersion)
	}

	// 時刻: JST オフセット付き文字列が同じ瞬間の epoch になっていること。
	got, err := s.Get(ctx, "id-jst")
	if err != nil {
		t.Fatalf("Get after migration: %v", err)
	}
	want := time.Date(2026, 7, 23, 10, 0, 0, 0, time.FixedZone("JST", 9*60*60))
	if !got.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, want)
	}
	if !got.CreatedAt.Equal(time.Date(2026, 7, 24, 22, 26, 19, 0, time.UTC)) {
		t.Errorf("CreatedAt = %v", got.CreatedAt)
	}

	// メタデータが保たれていること。
	if got.Event != "第39期竜王戦決勝トーナメント" || got.Black != "柵木幹太五段" ||
		got.EndMark != "投了" || got.Moves != 140 {
		t.Errorf("metadata lost: %+v", got.Game)
	}

	// KIF 本文が game_kifu へ移り、Get で引けること。
	if !strings.Contains(got.Body, "1 ７六歩(77)") {
		t.Errorf("KIF = %q", got.Body)
	}
	var inGames int
	s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('games') WHERE name = 'kif'`).Scan(&inGames)
	if inGames != 0 {
		t.Error("games still has the kif column")
	}

	// 開始日時が空だった行はゼロ値になること。
	empty, err := s.Get(ctx, "id-empty")
	if err != nil {
		t.Fatal(err)
	}
	if !empty.StartedAt.IsZero() {
		t.Errorf("empty started_at = %v, want zero", empty.StartedAt)
	}

	// FTS が構築され、移行済みデータを検索できること。
	found, err := s.Search(ctx, Query{Text: "決勝トーナメント"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != "id-jst" {
		t.Errorf("search after migration = %v", ids(found))
	}

	// 移行後も通常の保存ができること。
	if _, err := s.Save(ctx, sampleGame()); err != nil {
		t.Fatalf("Save after migration: %v", err)
	}
}

// v2（game_kifu.kif、形式・文字コードなし）から v3 へ移行できること。
func TestMigrateV2ToV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// v2 の形（v1 の games に source_url を足したもの + KIF 本文は kif 列）。
	_, err = db.Exec(`
        CREATE TABLE games (
            id TEXT PRIMARY KEY, source TEXT NOT NULL, source_id TEXT NOT NULL,
            event TEXT NOT NULL DEFAULT '', handicap TEXT NOT NULL DEFAULT '',
            place TEXT NOT NULL DEFAULT '', black TEXT NOT NULL DEFAULT '',
            white TEXT NOT NULL DEFAULT '', started_at INTEGER NOT NULL DEFAULT 0,
            end_mark TEXT NOT NULL DEFAULT '', moves INTEGER NOT NULL DEFAULT 0,
            created_at INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL DEFAULT 0,
            source_url TEXT NOT NULL DEFAULT '');
        CREATE UNIQUE INDEX idx_games_source ON games(source, source_id);
        CREATE TABLE game_kifu (
            game_id TEXT PRIMARY KEY REFERENCES games(id) ON DELETE CASCADE,
            kif TEXT NOT NULL);
        INSERT INTO games (id, source, source_id, event, moves, created_at, updated_at)
        VALUES ('v2-id', 'yomiuri', 'abc', 'v2 データ', 3, 1, 1);
        INSERT INTO game_kifu VALUES ('v2-id', '手合割：平手' || char(10));
        PRAGMA user_version = 2;`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on v2 schema: %v", err)
	}
	defer s.Close()

	got, err := s.Get(context.Background(), "v2-id")
	if err != nil {
		t.Fatalf("Get after migration: %v", err)
	}
	if got.Event != "v2 データ" {
		t.Errorf("既存行が失われている: %+v", got.Game)
	}
	// 本文が body 列へ移り、既存データは KIF / UTF-8 として扱われること。
	if !strings.Contains(got.Body, "手合割：平手") {
		t.Errorf("Body = %q", got.Body)
	}
	if got.Format != "kif" || got.Encoding != "utf-8" {
		t.Errorf("Format=%q Encoding=%q, want kif/utf-8", got.Format, got.Encoding)
	}

	var version int
	s.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != schemaVersion {
		t.Errorf("user_version = %d, want %d", version, schemaVersion)
	}
}

// 形式と文字コードが保存・復元されること。
func TestSaveKeepsFormatAndEncoding(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	g := sampleGame()
	g.Format = "ki2"
	g.Encoding = "shift_jis"
	rec, err := s.Save(ctx, g)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Format != "ki2" || got.Encoding != "shift_jis" {
		t.Errorf("Format=%q Encoding=%q", got.Format, got.Encoding)
	}

	// 未指定なら既定に寄る。
	g2 := sampleGame()
	g2.SourceID = "other"
	g2.Format = ""
	g2.Encoding = ""
	rec2, err := s.Save(ctx, g2)
	if err != nil {
		t.Fatal(err)
	}
	got2, _ := s.Get(ctx, rec2.ID)
	if got2.Format != "kif" || got2.Encoding != "utf-8" {
		t.Errorf("defaults = %q/%q", got2.Format, got2.Encoding)
	}
}

// 本文は整形せずそのまま保存されること（原本保持）。
func TestSaveKeepsBodyVerbatim(t *testing.T) {
	s := newTestStore(t)

	// 修飾つき・コメントつき・末尾に余分な空行がある原本。
	body := "棋戦：テスト\n*コメント\n   1 ７八銀右(69)\n   2 ２二角不成(88)\n\n"
	g := sampleGame()
	g.Body = body

	rec, err := s.Save(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != body {
		t.Errorf("本文が変わっている:\n got %q\nwant %q", got.Body, body)
	}
}

// 移行済みの DB を開き直しても再移行せず、内容が保たれること。
func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kicho.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s1.Save(context.Background(), sampleGame())
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()

	for i := 0; i < 2; i++ {
		s2, err := Open(path)
		if err != nil {
			t.Fatalf("reopen #%d: %v", i, err)
		}
		got, err := s2.Get(context.Background(), rec.ID)
		if err != nil {
			t.Fatalf("Get after reopen #%d: %v", i, err)
		}
		if got.Event != sampleGame().Event || got.Body != sampleGame().Body {
			t.Errorf("content changed after reopen #%d", i)
		}
		s2.Close()
	}
}

// end_mark 列を持たない古い DB を開いても移行できること。
func TestMigrateFromOldestSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	// end_mark 抜きの旧スキーマを手で作る。
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
        CREATE TABLE games (
            id TEXT PRIMARY KEY, source TEXT NOT NULL, source_id TEXT NOT NULL,
            event TEXT NOT NULL DEFAULT '', handicap TEXT NOT NULL DEFAULT '',
            place TEXT NOT NULL DEFAULT '', black TEXT NOT NULL DEFAULT '',
            white TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL DEFAULT '',
            kif TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
        CREATE UNIQUE INDEX idx_games_source ON games(source, source_id);
        INSERT INTO games (id, source, source_id, event, kif, created_at, updated_at)
        VALUES ('old-id', 'yomiuri', 'legacy', '旧データ', '手合割：平手\n', '', '');`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on legacy schema: %v", err)
	}
	defer s.Close()

	got, err := s.Get(context.Background(), "old-id")
	if err != nil {
		t.Fatalf("Get after migration: %v", err)
	}
	if got.Event != "旧データ" {
		t.Errorf("existing row was lost: %+v", got.Game)
	}
	if got.EndMark != "" {
		t.Errorf("migrated rows should default to empty EndMark, got %q", got.EndMark)
	}

	// 移行後は新しい列に書けること。
	if _, err := s.Save(context.Background(), sampleGame()); err != nil {
		t.Fatalf("Save after migration: %v", err)
	}
}

func TestSaveAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rec, err := s.Save(ctx, sampleGame())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if rec.ID == "" {
		t.Fatal("Save returned an empty ID")
	}

	got, err := s.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Event != "第37期竜王戦七番勝負第２局" || got.Black != "佐々木勇気八段" {
		t.Errorf("Get returned %+v", got.Game)
	}
	if !got.StartedAt.Equal(sampleGame().StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, sampleGame().StartedAt)
	}
	if got.Body != sampleGame().Body {
		t.Errorf("KIF = %q", got.Body)
	}
}

// 同じ棋譜を取り直しても重複せず、ID が変わらないこと。
func TestSaveIsIdempotentPerSource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, err := s.Save(ctx, sampleGame())
	if err != nil {
		t.Fatal(err)
	}

	updated := sampleGame()
	updated.Event = "第37期竜王戦七番勝負第２局(更新)"
	updated.Body = "手合割：平手\n手数----指手---------消費時間--\n1 ２六歩(27)\n"

	second, err := s.Save(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}

	if second.ID != first.ID {
		t.Errorf("ID changed on re-save: %q -> %q", first.ID, second.ID)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt changed on re-save: %v -> %v", first.CreatedAt, second.CreatedAt)
	}

	n, err := s.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("Count = %d, want 1 (re-save must not duplicate)", n)
	}

	got, err := s.Get(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Event != updated.Event || got.Body != updated.Body {
		t.Errorf("re-save did not update the row: %+v", got.Game)
	}
}

func TestFindBySource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	saved, err := s.Save(ctx, sampleGame())
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.FindBySource(ctx, SourceYomiuri, "67037f045dcc1abf6b9528e3")
	if err != nil {
		t.Fatalf("FindBySource: %v", err)
	}
	if got.ID != saved.ID {
		t.Errorf("ID = %q, want %q", got.ID, saved.ID)
	}

	if _, err := s.FindBySource(ctx, SourceYomiuri, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindBySource(missing) error = %v, want ErrNotFound", err)
	}
}

func TestListOrderedNewestFirst(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i, d := range []time.Time{
		time.Date(2024, 10, 5, 9, 0, 0, 0, time.UTC),
		time.Date(2024, 11, 27, 9, 0, 0, 0, time.UTC),
		time.Date(2024, 10, 19, 9, 0, 0, 0, time.UTC),
	} {
		g := sampleGame()
		g.SourceID = string(rune('a' + i))
		g.StartedAt = d
		if _, err := s.Save(ctx, g); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("len = %d, want 3", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].StartedAt.Before(list[i].StartedAt) {
			t.Errorf("not sorted newest first: %v before %v",
				list[i-1].StartedAt, list[i].StartedAt)
		}
	}
}

func TestListSummaryOmitsKIF(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, sampleGame()); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].Body != "" {
		t.Errorf("ListSummary should omit the KIF body, got %q", list[0].Body)
	}
	if list[0].Event == "" {
		t.Error("ListSummary should still carry the metadata")
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rec, err := s.Save(ctx, sampleGame())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, rec.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, rec.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, rec.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete(missing) = %v, want ErrNotFound", err)
	}
}

func TestGetMissing(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Get(context.Background(), "no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}
}

func TestSaveValidates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cases := map[string]Game{
		"source なし":    {SourceID: "x", Body: "k"},
		"source_id なし": {Source: SourceYomiuri, Body: "k"},
		"KIF が空":       {Source: SourceYomiuri, SourceID: "x"},
	}
	for name, g := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Save(ctx, g); err == nil {
				t.Error("Save succeeded, want error")
			}
		})
	}
}

// 開始日時がゼロ値でも保存・復元できること。
func TestSaveZeroStartedAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	g := sampleGame()
	g.StartedAt = time.Time{}
	rec, err := s.Save(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StartedAt.IsZero() {
		t.Errorf("StartedAt = %v, want zero", got.StartedAt)
	}
}

// 開き直しても内容が残ること(永続化されていること)。
func TestReopenPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kicho.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s1.Save(context.Background(), sampleGame())
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	got, err := s2.Get(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Event != sampleGame().Event {
		t.Errorf("Event = %q", got.Event)
	}
}
