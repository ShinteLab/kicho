package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DSN で渡した PRAGMA が実際に効いていること。
//
// PRAGMA は接続ごとの設定なので、Open で1回 Exec する形だと接続を張り直された
// ときに既定へ戻る。特に foreign_keys が OFF に戻ると Delete しても
// game_kifu に本文が残る。
func TestOpenAppliesPragmas(t *testing.T) {
	s := newTestStore(t)

	var fk int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1（ON DELETE CASCADE が効かない）", fk)
	}

	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Errorf("journal_mode = %q, want wal", mode)
	}

	var busy int
	if err := s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if busy == 0 {
		t.Error("busy_timeout = 0（ロックに当たると待たずに database is locked になる）")
	}
}

// DSN に `file:` を付けていないこと。付けると URI として解釈され、
// Windows のパスやスペース入りのパスが壊れる。
func TestOpenHandlesAwkwardPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "棋譜 データ #1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "kicho.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	defer s.Close()

	rec, err := s.Save(context.Background(), sampleGame())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := s.Get(context.Background(), rec.ID); err != nil {
		t.Fatalf("Get: %v", err)
	}
	// DSN のクエリ部分がファイル名に混ざっていないこと。
	if _, err := os.Stat(path); err != nil {
		t.Errorf("DB が想定の場所に無い: %v", err)
	}
}

// 自分より新しい版の DB は開かず、理由の分かるエラーを返すこと。
//
// kicho と ikkyoku は別バイナリなので、片方だけ更新した状態で同じ DB を
// 指すと版が食い違いうる。黙って開くと後段が `no such column` で落ちる。
func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kicho.db")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s2, err := Open(path)
	if err == nil {
		s2.Close()
		t.Fatal("新しい版の DB が開けてしまった")
	}
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Errorf("err = %v, want ErrSchemaTooNew", err)
	}
}

// 同じ版なら移行を走らせずそのまま開くこと（現状維持の確認）。
func TestOpenAcceptsSameSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kicho.db")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	var version int
	if err := s2.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Errorf("user_version = %d, want %d", version, schemaVersion)
	}
}
