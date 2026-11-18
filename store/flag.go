package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Flag は棋譜に外から付けた指摘（v7。`gameFlagsSchemaSQL`）。
//
// **エラーの印（Record.ErrorMove）とは別物。** あちらは kicho が本文から見つけ直す値で、
// こちらは人や利用側（ikkyoku）が「盤には当たるが実際と違う」ことを書くもの。
type Flag struct {
	ID     string
	GameID string
	// Move は指摘する手（1 始まり）。0 は棋譜全体についての指摘。
	Move   int
	Reason string
	// By は誰が付けたか（"ikkyoku" / "user" など。空でもよい）。
	By        string
	CreatedAt time.Time
	// BodyDigest は付けた時点の棋譜の本文のダイジェスト。
	BodyDigest string
	// Stale は付けたあとに棋譜の本文が変わったか（読むたびに計算する）。
	// 指摘が古くなっているかもしれない、という印であって、消すかどうかは付けた側が決める。
	Stale bool
}

// AddFlag は棋譜に指摘を付ける。棋譜が無ければ ErrNotFound。
//
// move は 0 以上（0 = 棋譜全体）、reason は空にできない。前後の空白は落とす。
// 付けた時点の本文のダイジェストを一緒に持つ（Flag.Stale のため）。
func (s *Store) AddFlag(ctx context.Context, gameID string, move int, reason, by string) (Flag, error) {
	reason = strings.TrimSpace(reason)
	by = strings.TrimSpace(by)
	if move < 0 {
		return Flag{}, fmt.Errorf("kicho: 指摘する手が負です: %d", move)
	}
	if reason == "" {
		return Flag{}, fmt.Errorf("kicho: 指摘の理由が空です")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Flag{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	var digest, body string
	err = tx.QueryRowContext(ctx, `SELECT g.body_digest, COALESCE(k.body, '')
		FROM games g LEFT JOIN game_kifu k ON k.game_id = g.id WHERE g.id = ?`,
		gameID).Scan(&digest, &body)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Flag{}, ErrNotFound
	case err != nil:
		return Flag{}, fmt.Errorf("lookup game: %w", err)
	}
	// v6 より前に保存したまま一度も保存し直していない棋譜はダイジェストが空のことがある。
	// 空のまま持つと、あとで保存し直したときに本文が同じでも Stale が立つので埋めておく。
	if digest == "" {
		digest = bodyDigest(body)
		if _, err := tx.ExecContext(ctx,
			`UPDATE games SET body_digest = ? WHERE id = ?`, digest, gameID); err != nil {
			return Flag{}, fmt.Errorf("fill digest: %w", err)
		}
	}

	f := Flag{
		ID:         uuid.NewString(),
		GameID:     gameID,
		Move:       move,
		Reason:     reason,
		By:         by,
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
		BodyDigest: digest,
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO game_flags (id, game_id, move, reason, flagged_by, created_at, body_digest)
        VALUES (?,?,?,?,?,?,?)`,
		f.ID, f.GameID, f.Move, f.Reason, f.By, toEpoch(f.CreatedAt), f.BodyDigest); err != nil {
		return Flag{}, fmt.Errorf("add flag: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Flag{}, fmt.Errorf("commit: %w", err)
	}
	return f, nil
}

// RemoveFlag は指摘を外す。無ければ ErrNotFound。
func (s *Store) RemoveFlag(ctx context.Context, flagID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM game_flags WHERE id = ?`, flagID)
	if err != nil {
		return fmt.Errorf("remove flag: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remove flag: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Flags は棋譜の指摘を手の順（同じ手なら付けた順）に返す。無ければ空（棋譜が無くても空）。
func (s *Store) Flags(ctx context.Context, gameID string) ([]Flag, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT fl.id, fl.game_id, fl.move, fl.reason, fl.flagged_by, fl.created_at, fl.body_digest,
               fl.body_digest <> g.body_digest
        FROM game_flags fl JOIN games g ON g.id = fl.game_id
        WHERE fl.game_id = ?
        ORDER BY fl.move, fl.created_at, fl.rowid`, gameID)
	if err != nil {
		return nil, fmt.Errorf("list flags: %w", err)
	}
	defer rows.Close()

	var out []Flag
	for rows.Next() {
		var f Flag
		var createdAt int64
		if err := rows.Scan(&f.ID, &f.GameID, &f.Move, &f.Reason, &f.By, &createdAt,
			&f.BodyDigest, &f.Stale); err != nil {
			return nil, fmt.Errorf("scan flag: %w", err)
		}
		f.CreatedAt = fromEpoch(createdAt)
		out = append(out, f)
	}
	return out, rows.Err()
}
