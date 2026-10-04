package store

import (
	"database/sql"
	"fmt"
)

// migrate は DB を最新スキーマまで持ち上げる。
//
// 版は PRAGMA user_version で管理する。v1 より前の DB には user_version が
// 無い(=0)ため、games テーブルの形を見て「新規」か「旧スキーマ」かを判定する。
//
// **自分より新しい版の DB は開かない**(ErrSchemaTooNew)。kicho と ikkyoku は
// 別バイナリなので、片方だけ更新した状態で同じ DB を指すと版が食い違いうる。
// 黙って開くと後段のクエリが `no such column` で落ち、原因が分からなくなる。
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("%w: この棋譜データベースは新しい版(v%d)で作られています。"+
			"扱えるのは v%d までです。プログラムを更新してください",
			ErrSchemaTooNew, version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}

	exists, err := tableExists(db, "games")
	if err != nil {
		return err
	}

	// まだ何も無い → 最新スキーマをそのまま作る。
	if !exists {
		if _, err := db.Exec(schemaSQL); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
		return setVersion(db, schemaVersion)
	}

	cols, err := columnSet(db, "games")
	if err != nil {
		return err
	}

	// v0（KIF が games に同居）なら先に v1 の形へ。
	if cols["kif"] {
		if err := migrateV0ToV1(db, cols); err != nil {
			return err
		}
		cols, err = columnSet(db, "games")
		if err != nil {
			return err
		}
	}

	// v1 → v2: 取得元 URL の列を足す。
	if !cols["source_url"] {
		if _, err := db.Exec(migrateV1ToV2SQL); err != nil {
			return fmt.Errorf("migrate to v2: %w", err)
		}
	}

	// v2 → v3: 棋譜本文に形式と文字コードを持たせる。
	kifuCols, err := columnSet(db, "game_kifu")
	if err != nil {
		return err
	}
	if !kifuCols["body"] {
		if _, err := db.Exec(migrateV2ToV3SQL); err != nil {
			return fmt.Errorf("migrate to v3: %w", err)
		}
	}

	// v3 → v4: 追跡中の中継（仮の一覧）を足す。既存の棋譜には触らない。
	hasWatches, err := tableExists(db, "watches")
	if err != nil {
		return err
	}
	if !hasWatches {
		if _, err := db.Exec(migrateV3ToV4SQL); err != nil {
			return fmt.Errorf("migrate to v4: %w", err)
		}
	}

	// v4 → v5: 人が書く欄（直した対局名・備考）を足し、検索の索引に含める。
	// 列が揃っていても FTS が古い（4 列の）ままなら作り直す。
	ftsCols, err := columnSet(db, "games_fts")
	if err != nil {
		return err
	}
	if !cols["event_edited"] || !cols["note"] || !ftsCols["event_edited"] || !ftsCols["note"] {
		if err := migrateV4ToV5(db, cols); err != nil {
			return err
		}
	}

	return setVersion(db, schemaVersion)
}

// migrateV4ToV5 は games に人が書く欄を足し、FTS を作り直す。
//
// 途中で失敗しても v4 の形が残るよう、1 トランザクションで行う
// (FTS を捨てたまま列だけ足された DB にしない)。
func migrateV4ToV5(db *sql.DB, cols map[string]bool) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()

	// 先に FTS とトリガを捨てる(古いトリガが残ったまま列を足さないため)。
	if _, err := tx.Exec(dropGamesFTSSQL); err != nil {
		return fmt.Errorf("migrate to v5: drop fts: %w", err)
	}
	for _, c := range v5Columns {
		if cols[c.name] {
			continue
		}
		if _, err := tx.Exec(c.ddl); err != nil {
			return fmt.Errorf("migrate to v5: add column %s: %w", c.name, err)
		}
	}
	if _, err := tx.Exec(rebuildGamesFTSSQL); err != nil {
		return fmt.Errorf("migrate to v5: rebuild fts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

// migrateV0ToV1 は「KIF 同居・時刻 TEXT」の旧スキーマを v1 へ移す。
func migrateV0ToV1(db *sql.DB, cols map[string]bool) error {
	// v0 の中でも古い DB には end_mark / moves が無い。先に揃えてから移す
	// (移行 SQL がこれらの列を参照するため)。
	for _, c := range legacyColumns {
		if cols[c.name] {
			continue
		}
		if _, err := db.Exec(c.ddl); err != nil {
			return fmt.Errorf("add legacy column %s: %w", c.name, err)
		}
	}

	// 途中で失敗しても元の games が残るよう、まとめて1トランザクションで行う。
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(migrateV0SQL); err != nil {
		return fmt.Errorf("migrate to v1: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		name).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("inspect schema: %w", err)
	}
	return n > 0, nil
}

func columnSet(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("inspect columns: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("inspect columns: %w", err)
		}
		out[name] = true
	}
	return out, rows.Err()
}

// setVersion は PRAGMA user_version を更新する(プレースホルダが使えないため直書き)。
func setVersion(db *sql.DB, v int) error {
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}
