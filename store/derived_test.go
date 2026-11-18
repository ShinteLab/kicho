package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// エラーのある親と、その訂正(子)を用意する。
func saveParentAndChild(t *testing.T, s *Store) (parent, child Record) {
	t.Helper()
	ctx := context.Background()
	g := sampleGame()
	g.ErrorMove = 74
	g.Error = "74手目「△89飛」を盤に当てられません"
	parent, err := s.Save(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	fixed := sampleGame()
	fixed.Body = "手合割：平手\n手数----指手---------消費時間--\n1 ２六歩(27)\n"
	child, err = s.SaveDerived(ctx, parent.ID, RelationCorrection, fixed)
	if err != nil {
		t.Fatal(err)
	}
	return parent, child
}

func TestSaveKeepsErrorMarkAndDigest(t *testing.T) {
	s := newTestStore(t)
	parent, _ := saveParentAndChild(t, s)
	got, err := s.Get(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ErrorMove != 74 || got.Error == "" {
		t.Errorf("ErrorMove = %d, Error = %q", got.ErrorMove, got.Error)
	}
	if got.BodyDigest != bodyDigest(sampleGame().Body) {
		t.Errorf("BodyDigest = %q", got.BodyDigest)
	}

	// 直った本文で取り直して保存すればエラーの印は消える。
	g := sampleGame()
	if _, err := s.Save(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(context.Background(), parent.ID)
	if got.ErrorMove != 0 || got.Error != "" {
		t.Errorf("保存し直してもエラーの印が残る: %d %q", got.ErrorMove, got.Error)
	}
}

func TestSaveDerived(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	parent, child := saveParentAndChild(t, s)

	if child.Source != SourceDerived || child.SourceID == "" || child.ID == parent.ID {
		t.Errorf("child = %+v", child)
	}
	if child.ParentID != parent.ID || child.Relation != RelationCorrection {
		t.Errorf("ParentID = %q, Relation = %q", child.ParentID, child.Relation)
	}
	if child.ParentDigest != parent.BodyDigest || child.ParentChanged || child.ParentMissing {
		t.Errorf("ParentDigest = %q, Changed = %v, Missing = %v", child.ParentDigest, child.ParentChanged, child.ParentMissing)
	}

	// 同じ中身でも毎回新しい棋譜になる。
	second, err := s.SaveDerived(ctx, parent.ID, RelationCorrection, sampleGame())
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == child.ID || second.SourceID == child.SourceID {
		t.Error("2 つ目の子が 1 つ目と同じ棋譜になった")
	}

	p, err := s.Get(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Children != 2 {
		t.Errorf("Children = %d, want 2", p.Children)
	}
	kids, err := s.Children(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 2 || kids[0].ID != second.ID {
		t.Errorf("Children = %v, want 新しい順に 2 件", ids(kids))
	}
	// 一覧(検索)でも同じ値が読める。
	found, err := s.Search(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range found {
		if r.ID == parent.ID && r.Children != 2 {
			t.Errorf("Search の Children = %d", r.Children)
		}
		if r.ID == child.ID && r.ParentID != parent.ID {
			t.Errorf("Search の ParentID = %q", r.ParentID)
		}
	}
}

func TestSaveDerivedRejects(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.SaveDerived(ctx, "nothing", RelationCorrection, sampleGame()); !errors.Is(err, ErrNotFound) {
		t.Errorf("親が無い: err = %v", err)
	}
	parent, _ := saveParentAndChild(t, s)
	if _, err := s.SaveDerived(ctx, parent.ID, "", sampleGame()); err == nil {
		t.Error("関係が空でも保存できた")
	}
	// 派生棋譜は Save では保存できない(親の無い子ができる)。
	g := sampleGame()
	g.Source = SourceDerived
	if _, err := s.Save(ctx, g); err == nil {
		t.Error("Save で派生棋譜を保存できた")
	}
}

// 親を取り直して保存し直しても子との繋がりは残り、本文が変わったときだけ ParentChanged が立つ。
func TestParentChanged(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	parent, child := saveParentAndChild(t, s)

	// 同じ本文で保存し直す(取り直したが変わっていない)。
	same := sampleGame()
	same.ErrorMove = 74
	if _, err := s.Save(ctx, same); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, child.ID)
	if got.ParentID != parent.ID || got.ParentChanged {
		t.Errorf("同じ本文: ParentID = %q, ParentChanged = %v", got.ParentID, got.ParentChanged)
	}
	p, _ := s.Get(ctx, parent.ID)
	if p.Children != 1 {
		t.Errorf("保存し直したら子の件数が %d", p.Children)
	}

	// 手が増えた(対局中に取り直した)。
	more := sampleGame()
	more.Body += "2 ８四歩(83)\n"
	if _, err := s.Save(ctx, more); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, child.ID)
	if !got.ParentChanged || got.ParentMissing {
		t.Errorf("本文が変わった: ParentChanged = %v, ParentMissing = %v", got.ParentChanged, got.ParentMissing)
	}
}

// 親を消しても子は残り、繋がりも消さない(親が無いことが分かる)。
func TestDeleteParentKeepsChild(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	parent, child := saveParentAndChild(t, s)
	if err := s.Delete(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, child.ID)
	if err != nil {
		t.Fatalf("子が消えた: %v", err)
	}
	if got.ParentID != parent.ID || !got.ParentMissing || got.ParentChanged {
		t.Errorf("ParentID = %q, ParentMissing = %v, ParentChanged = %v", got.ParentID, got.ParentMissing, got.ParentChanged)
	}
	if got.Body == "" {
		t.Error("子の本文が消えた")
	}
}

func TestMigrateV5ToV6(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v5.db")

	// v5 の形を作る: 最新から v6 の列と索引を外した games。
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
        CREATE TABLE games (
            id TEXT PRIMARY KEY, source TEXT NOT NULL, source_id TEXT NOT NULL,
            source_url TEXT NOT NULL DEFAULT '', event TEXT NOT NULL DEFAULT '',
            handicap TEXT NOT NULL DEFAULT '', place TEXT NOT NULL DEFAULT '',
            black TEXT NOT NULL DEFAULT '', white TEXT NOT NULL DEFAULT '',
            started_at INTEGER NOT NULL DEFAULT 0, end_mark TEXT NOT NULL DEFAULT '',
            moves INTEGER NOT NULL DEFAULT 0,
            created_at INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL DEFAULT 0,
            event_edited TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '');
        CREATE UNIQUE INDEX idx_games_source ON games(source, source_id);
        CREATE INDEX idx_games_started_at ON games(started_at DESC);
        CREATE INDEX idx_games_black ON games(black);
        CREATE INDEX idx_games_white ON games(white);
        CREATE TABLE game_kifu (
            game_id TEXT PRIMARY KEY REFERENCES games(id) ON DELETE CASCADE,
            format TEXT NOT NULL DEFAULT 'kif', encoding TEXT NOT NULL DEFAULT 'utf-8',
            body TEXT NOT NULL);
    ` + gamesFTSSchemaSQL + watchesSchemaSQL + `
        INSERT INTO games (id, source, source_id, event, black, white, moves,
                           created_at, updated_at, note)
        VALUES ('v5-id', 'yomiuri', 'abc', '第39期竜王戦決勝トーナメント',
                '柵木幹太五段', '斎藤慎太郎八段', 1, 1, 1, '備考');
        INSERT INTO game_kifu (game_id, body) VALUES ('v5-id', 'v5 の本文');
        PRAGMA user_version = 5;`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on v5 schema: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	var version int
	s.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != schemaVersion {
		t.Errorf("user_version = %d, want %d", version, schemaVersion)
	}

	got, err := s.Get(ctx, "v5-id")
	if err != nil {
		t.Fatalf("Get after migration: %v", err)
	}
	if got.Event != "第39期竜王戦決勝トーナメント" || got.Note != "備考" || got.Body != "v5 の本文" {
		t.Errorf("既存行が変わった: %+v", got)
	}
	if got.BodyDigest != bodyDigest("v5 の本文") {
		t.Errorf("BodyDigest が埋まっていない: %q", got.BodyDigest)
	}
	if got.ErrorMove != 0 || got.ParentID != "" || got.Children != 0 {
		t.Errorf("ErrorMove = %d, ParentID = %q, Children = %d", got.ErrorMove, got.ParentID, got.Children)
	}

	// 移行した行を親にして子を作れ、検索も効く。
	child, err := s.SaveDerived(ctx, "v5-id", RelationCorrection, sampleGame())
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentChanged {
		t.Error("移行した親で ParentChanged が立った")
	}
	found, err := s.Search(ctx, Query{Text: "決勝トーナメント"})
	if err != nil || len(found) != 1 {
		t.Errorf("Search after migration = %v, %v", ids(found), err)
	}

	// もう一度開いても壊れない(何度流しても同じ)。
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s2.Close()
}
