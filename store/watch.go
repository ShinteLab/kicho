package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Watch は追跡中の中継1件（UI の「仮の一覧」）。
//
// **棋譜本文は持たない。** 持つのは「どのサイトのどの棋譜か」と、一覧で
// 見分けるための最小限のメタだけ。棋譜そのものを残すのは Save（games）の役目で、
// こちらは**2日制の対局で翌日また URL を貼り直さずに済むようにする**ためのもの。
type Watch struct {
	// Source は取得元(SourceYomiuri / SourceShogiLive)。
	Source string
	// SourceID は取得元での ID。ここへ取り直しに行けるものだけを入れる。
	SourceID string
	// SourceURL は人が開いて確認できる URL。
	SourceURL string

	Event string // 棋戦名
	Black string // 先手
	White string // 後手
	// StartedAt は開始日時(ゼロ値可)。
	StartedAt time.Time
	// EndMark は終局の種別(例 "投了")。対局中は空。
	EndMark string
	// Moves は最後に取得したときの手数。
	Moves int

	// CreatedAt は最初に追跡し始めた時刻。**取り直しても維持される**
	// （一覧の並び順が取得のたびに入れ替わらないようにするため）。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Finished は終局済みかどうかを返す。
func (w Watch) Finished() bool { return w.EndMark != "" }

// SaveWatch は追跡中の中継を登録する。同じ (Source, SourceID) が既にあれば
// 内容を更新し、CreatedAt はそのまま維持する。
func (s *Store) SaveWatch(ctx context.Context, w Watch) (Watch, error) {
	if w.Source == "" || w.SourceID == "" {
		return Watch{}, fmt.Errorf("kicho: Source and SourceID are required")
	}

	now := time.Now().UTC().Truncate(time.Second)

	// 既にあれば追跡し始めた時刻を引き継ぐ（並び順を保つため）。
	var createdAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT created_at FROM watches WHERE source = ? AND source_id = ?`,
		w.Source, w.SourceID).Scan(&createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		createdAt = toEpoch(now)
	case err != nil:
		return Watch{}, fmt.Errorf("lookup watch: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
        INSERT INTO watches (source, source_id, source_url, event, black, white,
                             started_at, end_mark, moves, created_at, updated_at)
        VALUES (?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(source, source_id) DO UPDATE SET
            source_url = excluded.source_url,
            event = excluded.event,
            black = excluded.black,
            white = excluded.white,
            started_at = excluded.started_at,
            end_mark = excluded.end_mark,
            moves = excluded.moves,
            updated_at = excluded.updated_at`,
		w.Source, w.SourceID, w.SourceURL, w.Event, w.Black, w.White,
		toEpoch(w.StartedAt), w.EndMark, w.Moves, createdAt, toEpoch(now))
	if err != nil {
		return Watch{}, fmt.Errorf("save watch: %w", err)
	}

	w.CreatedAt = fromEpoch(createdAt)
	w.UpdatedAt = now
	return w, nil
}

// Watches は追跡中の中継を新しい順（追跡し始めたのが新しいものから）に返す。
//
// **並びの基準が UpdatedAt ではないのは、取り直すたびにカードの位置が
// 入れ替わってしまうため。** フロントは取得したものを先頭に積み、以後は
// その場で最新化する（mergeCard）ので、復元もその順に揃える。
func (s *Store) Watches(ctx context.Context) ([]Watch, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT source, source_id, source_url, event, black, white,
               started_at, end_mark, moves, created_at, updated_at
        FROM watches ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, fmt.Errorf("list watches: %w", err)
	}
	defer rows.Close()

	out := []Watch{}
	for rows.Next() {
		var w Watch
		var startedAt, createdAt, updatedAt int64
		err := rows.Scan(&w.Source, &w.SourceID, &w.SourceURL, &w.Event, &w.Black, &w.White,
			&startedAt, &w.EndMark, &w.Moves, &createdAt, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan watch: %w", err)
		}
		w.StartedAt = fromEpoch(startedAt)
		w.CreatedAt = fromEpoch(createdAt)
		w.UpdatedAt = fromEpoch(updatedAt)
		out = append(out, w)
	}
	return out, rows.Err()
}

// DeleteWatch は追跡をやめる。無ければ ErrNotFound。
//
// **保存済みの棋譜（games）には触らない。** 仮の一覧から外すだけ。
func (s *Store) DeleteWatch(ctx context.Context, source, sourceID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM watches WHERE source = ? AND source_id = ?`, source, sourceID)
	if err != nil {
		return fmt.Errorf("delete watch: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete watch: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteWatches は追跡をすべてやめる（UI の「クリア」）。消した件数を返す。
func (s *Store) DeleteWatches(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM watches`)
	if err != nil {
		return 0, fmt.Errorf("clear watches: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("clear watches: %w", err)
	}
	return int(n), nil
}
