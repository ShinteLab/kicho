// Package store は取得した棋譜を SQLite に永続化する。
//
// Wails には依存しない。ドライバは PureGo の modernc.org/sqlite を使うので
// cgo 不要で、クロスコンパイルもそのまま通る。
//
// 棋譜を溜め込んでいく前提の構成にしてある。詳細は schema.go を参照。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// ErrNotFound は該当する棋譜が無いことを表す。
var ErrNotFound = errors.New("kicho: game not found")

// ErrSchemaTooNew は DB のスキーマ版がこのプログラムより新しいことを表す。
// Open が返す。呼び出し側(ikkyoku など)が「更新してください」と案内するために
// 文言比較ではなく errors.Is で判定できるようにしてある。
var ErrSchemaTooNew = errors.New("kicho: database schema is newer than this build")

// 棋譜の取得元。
const (
	// SourceYomiuri は読売サイトからのスクレイピング。SourceID は読売の棋譜 ID で、
	// 取り直しても同じ棋譜として更新される。
	SourceYomiuri = "yomiuri"
	// SourceShogiLive は日本将棋連盟の棋譜中継(live.shogi.or.jp)。
	// SourceID は中継のパス(拡張子なし。例 "oui/kifu/67/oui202607290101")で、
	// 読売と同じく取り直しても同じ棋譜として更新される。
	SourceShogiLive = "shogilive"
	// SourceShogiDB2 は将棋DB2(shogidb2.com)。SourceID は対局ページの
	// `/games/{id}` のハッシュで、取り直しても同じ棋譜として更新される。
	SourceShogiDB2 = "shogidb2"
	// SourceURL は任意の URL から KIF を取得したもの。
	SourceURL = "url"
	// SourcePaste は KIF テキストを直接貼り付けたもの。
	SourcePaste = "paste"
)

// Game は保存する棋譜1局分。KIF テキストは組み立て済みのものを受け取る。
type Game struct {
	Source   string // 取得元(SourceYomiuri / SourceShogiLive / SourceShogiDB2 / SourceURL / SourcePaste)
	SourceID string // 取得元での ID
	// SourceURL は取得元の URL(URL 取り込みのみ。出所を残すため)。
	SourceURL string
	Event     string    // 棋戦名
	Handicap  string    // 手合割
	Place     string    // 対局場所
	Black     string    // 先手
	White     string    // 後手
	StartedAt time.Time // 開始日時(ゼロ値可)
	// EndMark は終局の種別(例 "投了")。対局中に保存した場合は空になる。
	EndMark string
	// Moves は手数。棋譜の書式を知っているのは呼び出し側なので、
	// ここでは本文から数えずに受け取る(一覧表示で本文を読まずに済ませるため)。
	Moves int

	// Body は**登録された形式の原本**。整形し直さずそのまま持つ
	// (整形すると変化・コメント・不成などの情報が落ちるため)。
	Body string
	// Format は Body の形式(kicho/format の Format。空なら kif 扱い)。
	Format string
	// Encoding は元の文字コード(Body 自体は UTF-8 に寄せてある)。主にデバッグ用。
	Encoding string
}

// Finished は終局済みの棋譜かどうかを返す。
func (g Game) Finished() bool { return g.EndMark != "" }

// Record は保存済みの棋譜(Game に kicho 自前の ID と保存時刻を付けたもの)。
type Record struct {
	Game
	ID        string // kicho 自前の ID。HTTP の /kifu/:id で使う
	CreatedAt time.Time
	UpdatedAt time.Time

	// ここから下は**人が書く欄**(Annotate で書く)。Game に置かないのは、
	// Game は Save が書くもので、Save はこの欄に触らないため
	// (取り直して保存しても消えない)。

	// EventEdited は人が直した対局名。空なら直していない。
	// ⚠️ **取得した値(Event)は書き換えない。** 表示には DisplayEvent を使う。
	// httpapi(外部ツールへの配信)は取得した値のまま配る。
	EventEdited string
	// Note は備考。
	Note string
}

// DisplayEvent は画面に出す対局名を返す(直した値があればそれ、無ければ取得した値)。
func (r Record) DisplayEvent() string {
	if r.EventEdited != "" {
		return r.EventEdited
	}
	return r.Event
}

// EventIsEdited は対局名を人が直してあるかどうかを返す。
func (r Record) EventIsEdited() bool { return r.EventEdited != "" }

// Store は棋譜データベース。
type Store struct {
	db *sql.DB
}

// Open は指定パスの DB を開く(無ければ作る)。親ディレクトリも作成する。
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	// 接続は1本に絞る。同一プロセス内の書き込みはこれで直列化される
	// (プロセスをまたぐ競合は WAL と busy_timeout が受け持つ。dsn を参照)。
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// dsn は接続文字列を組み立てる。
//
// ⚠️ **PRAGMA は接続ごとの設定なので、Open で1回 Exec するだけでは足りない。**
// database/sql は接続が壊れれば黙って張り直すため、そのとき設定が既定へ戻る
// (特に foreign_keys が OFF に戻ると Delete しても game_kifu に本文が残る)。
// DSN に載せておけば modernc.org/sqlite が接続を張るたびに適用する。
//
//   - busy_timeout … 既定は 0 で、ロックに当たると待たずに database is locked。
//     kicho と ikkyoku が同じ DB を開く運用があるので待たせる
//   - journal_mode(WAL) … 読みが書きをブロックしない。DB ファイルに永続する設定で、
//     隣に -wal / -shm が並ぶ
//   - foreign_keys … game_kifu の ON DELETE CASCADE を効かせる(既定は OFF)
//
// パスに `file:` を付けないのが要点。付けると SQLITE_OPEN_URI で URI として
// 解釈され、Windows のパス(`D:\...`)やスペースを含むパスが壊れる。
// 付けなければドライバは最初の `?` より前をパスとしてそのまま渡す。
func dsn(path string) string {
	return path + "?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)"
}

// Close は DB を閉じる。
func (s *Store) Close() error { return s.db.Close() }

// 時刻は UTC epoch 秒で持つ(ゼロ値は 0)。
func toEpoch(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}

func fromEpoch(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

// Save は棋譜を保存する。同じ (Source, SourceID) が既にあれば内容を更新し、
// 既存の ID と CreatedAt はそのまま維持する。
func (s *Store) Save(ctx context.Context, g Game) (Record, error) {
	if g.Source == "" || g.SourceID == "" {
		return Record{}, fmt.Errorf("kicho: Source and SourceID are required")
	}
	if g.Body == "" {
		return Record{}, fmt.Errorf("kicho: 棋譜本文が空です")
	}
	if g.Format == "" {
		g.Format = defaultFormat
	}
	if g.Encoding == "" {
		g.Encoding = defaultEncoding
	}

	now := time.Now().UTC().Truncate(time.Second)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	// 既存を探す(あれば ID と CreatedAt を引き継ぐ)。
	// 人が書く欄(event_edited / note)は読むだけ。返す Record に載せるためで、
	// ⚠️ **下の upsert には入れない**(取り直して保存しても編集が消えないように)。
	var id, eventEdited, note string
	var createdAt int64
	err = tx.QueryRowContext(ctx,
		`SELECT id, created_at, event_edited, note FROM games WHERE source = ? AND source_id = ?`,
		g.Source, g.SourceID).Scan(&id, &createdAt, &eventEdited, &note)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id = uuid.NewString()
		createdAt = toEpoch(now)
	case err != nil:
		return Record{}, fmt.Errorf("lookup existing: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
        INSERT INTO games (id, source, source_id, source_url, event, handicap, place,
                           black, white, started_at, end_mark, moves,
                           created_at, updated_at)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(source, source_id) DO UPDATE SET
            source_url = excluded.source_url,
            event = excluded.event,
            handicap = excluded.handicap,
            place = excluded.place,
            black = excluded.black,
            white = excluded.white,
            started_at = excluded.started_at,
            end_mark = excluded.end_mark,
            moves = excluded.moves,
            updated_at = excluded.updated_at`,
		id, g.Source, g.SourceID, g.SourceURL, g.Event, g.Handicap, g.Place,
		g.Black, g.White, toEpoch(g.StartedAt), g.EndMark, g.Moves,
		createdAt, toEpoch(now))
	if err != nil {
		return Record{}, fmt.Errorf("save game: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
        INSERT INTO game_kifu (game_id, format, encoding, body) VALUES (?, ?, ?, ?)
        ON CONFLICT(game_id) DO UPDATE SET
            format = excluded.format,
            encoding = excluded.encoding,
            body = excluded.body`, id, g.Format, g.Encoding, g.Body)
	if err != nil {
		return Record{}, fmt.Errorf("save kifu: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Record{}, fmt.Errorf("commit: %w", err)
	}

	return Record{
		Game:        g,
		ID:          id,
		CreatedAt:   fromEpoch(createdAt),
		UpdatedAt:   now,
		EventEdited: eventEdited,
		Note:        note,
	}, nil
}

// metaColumns は KIF 本文を含まないメタデータの列。
const metaColumns = `g.id, g.source, g.source_id, g.source_url, g.event, g.handicap, g.place,
                     g.black, g.white, g.started_at, g.end_mark, g.moves,
                     g.created_at, g.updated_at, g.event_edited, g.note`

// withKifu はメタデータに棋譜本文(と形式・文字コード)を足した SELECT 句。
const withKifu = metaColumns + `, COALESCE(k.body, ''),
                     COALESCE(NULLIF(k.format, ''), '` + defaultFormat + `'),
                     COALESCE(NULLIF(k.encoding, ''), '` + defaultEncoding + `')`

const joinKifu = ` LEFT JOIN game_kifu k ON k.game_id = g.id`

// 形式・文字コードが記録されていない場合の既定。
const (
	defaultFormat   = "kif"
	defaultEncoding = "utf-8"
)

type scanner interface{ Scan(...any) error }

func scanMeta(sc scanner) (Record, error) {
	var r Record
	var startedAt, createdAt, updatedAt int64
	err := sc.Scan(&r.ID, &r.Source, &r.SourceID, &r.SourceURL, &r.Event, &r.Handicap, &r.Place,
		&r.Black, &r.White, &startedAt, &r.EndMark, &r.Moves,
		&createdAt, &updatedAt, &r.EventEdited, &r.Note)
	if err != nil {
		return Record{}, err
	}
	r.StartedAt = fromEpoch(startedAt)
	r.CreatedAt = fromEpoch(createdAt)
	r.UpdatedAt = fromEpoch(updatedAt)
	return r, nil
}

func scanFull(sc scanner) (Record, error) {
	var r Record
	var startedAt, createdAt, updatedAt int64
	err := sc.Scan(&r.ID, &r.Source, &r.SourceID, &r.SourceURL, &r.Event, &r.Handicap, &r.Place,
		&r.Black, &r.White, &startedAt, &r.EndMark, &r.Moves,
		&createdAt, &updatedAt, &r.EventEdited, &r.Note, &r.Body, &r.Format, &r.Encoding)
	if err != nil {
		return Record{}, err
	}
	r.StartedAt = fromEpoch(startedAt)
	r.CreatedAt = fromEpoch(createdAt)
	r.UpdatedAt = fromEpoch(updatedAt)
	return r, nil
}

// Get は kicho 自前の ID で棋譜を取り出す(KIF 本文つき)。無ければ ErrNotFound。
func (s *Store) Get(ctx context.Context, id string) (Record, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+withKifu+` FROM games g`+joinKifu+` WHERE g.id = ?`, id)
	r, err := scanFull(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("get game: %w", err)
	}
	return r, nil
}

// FindBySource は取得元と取得元 ID で棋譜を探す。無ければ ErrNotFound。
func (s *Store) FindBySource(ctx context.Context, source, sourceID string) (Record, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+withKifu+` FROM games g`+joinKifu+
			` WHERE g.source = ? AND g.source_id = ?`, source, sourceID)
	r, err := scanFull(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("find game: %w", err)
	}
	return r, nil
}

const orderNewestFirst = ` ORDER BY g.started_at DESC, g.created_at DESC`

// List は保存済みの棋譜を新しい順に返す(KIF 本文つき)。
// 一覧表示だけなら ListSummary / Search を使う。
func (s *Store) List(ctx context.Context) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+withKifu+` FROM games g`+joinKifu+orderNewestFirst)
	if err != nil {
		return nil, fmt.Errorf("list games: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		r, err := scanFull(rows)
		if err != nil {
			return nil, fmt.Errorf("scan game: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListSummary は棋譜本文を除いた一覧を新しい順に返す(一覧表示用)。
func (s *Store) ListSummary(ctx context.Context) ([]Record, error) {
	return s.Search(ctx, Query{})
}

// Delete は棋譜を削除する。無ければ ErrNotFound。
func (s *Store) Delete(ctx context.Context, id string) error {
	// game_kifu は ON DELETE CASCADE で消える(Open で foreign_keys を ON にしている)。
	res, err := s.db.ExecContext(ctx, `DELETE FROM games WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete game: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete game: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Annotate は人が書く欄(直した対局名と備考)を書き換える。無ければ ErrNotFound。
//
// eventEdited を空にすると「直していない」に戻る。前後の空白を落とす・
// 取得した値と同じなら空にする、といった整えは呼び出し側(kicho.Library.Annotate)の役目。
//
// ⚠️ **event(取得した値)と updated_at は変えない。** updated_at は取得・保存の
// 時刻であって、人が書いた時刻ではない。
func (s *Store) Annotate(ctx context.Context, id, eventEdited, note string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE games SET event_edited = ?, note = ? WHERE id = ?`, eventEdited, note, id)
	if err != nil {
		return fmt.Errorf("annotate game: %w", err)
	}
	// 値が同じでも一致した行は数えられるので、0 なら該当なし。
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("annotate game: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Count は保存件数を返す。
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM games`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count games: %w", err)
	}
	return n, nil
}

// MinTrigramLen は FTS5 の trigram トークナイザが扱える最小文字数。
// これ未満のクエリはインデックスを使えないので LIKE にフォールバックする。
const MinTrigramLen = 3

// Query は検索条件。ゼロ値は「条件なし」を意味する。
type Query struct {
	// Text は棋戦名(取得した値と直した値の両方)・対局者・場所・備考への部分一致。
	// 3文字以上なら FTS5(trigram)、それ未満は LIKE で走査する。
	Text string
	// From / To は開始日時の範囲(ゼロ値は無制限)。To はその時刻を含む。
	From time.Time
	To   time.Time
	// FinishedOnly が true なら終局済みのみ。
	FinishedOnly bool
	// Limit は取得件数の上限(0 なら無制限)、Offset は読み飛ばす件数。
	Limit  int
	Offset int
}

// escapeFTS は FTS5 のフレーズクエリ用に文字列をくるむ。
// trigram では演算子を使わせず、入力全体を1つのフレーズとして扱う。
func escapeFTS(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// escapeLike は LIKE のワイルドカードを無効化する(ESCAPE '\' と併用)。
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// whereClause は Query を FROM 以降の SQL とプレースホルダの値に落とす。
//
// **Search と CountQuery で共有する。** 片方だけ条件を足すと
// 「一覧に出る件数」と「該当件数」が食い違い、UI の「N 件中 M 件」が嘘になる。
func whereClause(q Query) (sql string, args []any) {
	var (
		sb    strings.Builder
		where []string
	)
	sb.WriteString(` FROM games g`)

	text := strings.TrimSpace(q.Text)
	if text != "" {
		if len([]rune(text)) >= MinTrigramLen {
			// FTS を先に絞ってから join する。
			sb.WriteString(` JOIN games_fts f ON f.rowid = g.rowid`)
			where = append(where, `games_fts MATCH ?`)
			args = append(args, escapeFTS(text))
		} else {
			// trigram は3文字未満を索引化できないため走査するしかない。
			like := "%" + escapeLike(text) + "%"
			// 列は games_fts と揃える(どちらの経路でも同じ列を引くように)。
			where = append(where,
				`(g.event LIKE ? ESCAPE '\' OR g.event_edited LIKE ? ESCAPE '\'
				  OR g.black LIKE ? ESCAPE '\' OR g.white LIKE ? ESCAPE '\'
				  OR g.place LIKE ? ESCAPE '\' OR g.note LIKE ? ESCAPE '\')`)
			args = append(args, like, like, like, like, like, like)
		}
	}

	if !q.From.IsZero() {
		where = append(where, `g.started_at >= ?`)
		args = append(args, toEpoch(q.From))
	}
	if !q.To.IsZero() {
		where = append(where, `g.started_at <= ?`)
		args = append(args, toEpoch(q.To))
	}
	if q.FinishedOnly {
		where = append(where, `g.end_mark <> ''`)
	}

	if len(where) > 0 {
		sb.WriteString(` WHERE `)
		sb.WriteString(strings.Join(where, ` AND `))
	}
	return sb.String(), args
}

// CountQuery は条件に合う棋譜の件数を返す(Limit / Offset は無視する)。
//
// Search を Limit で切ったときに「全 N 件中 M 件」を出すために使う。
func (s *Store) CountQuery(ctx context.Context, q Query) (int, error) {
	from, args := whereClause(q)
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count matching games: %w", err)
	}
	return n, nil
}

// Search は条件に合う棋譜をメタデータのみ(KIF 本文なし)で新しい順に返す。
//
// **Limit 0 は無制限**。件数を絞るのは呼び出し側の責任で、
// アプリの一覧は kicho.Library.Search が上限を掛ける
// (httpapi の一覧は外部ツールが全件を期待するのでここで切らない)。
func (s *Store) Search(ctx context.Context, q Query) ([]Record, error) {
	from, args := whereClause(q)

	var sb strings.Builder
	sb.WriteString(`SELECT ` + metaColumns)
	sb.WriteString(from)
	sb.WriteString(orderNewestFirst)

	if q.Limit > 0 {
		sb.WriteString(` LIMIT ?`)
		args = append(args, q.Limit)
		if q.Offset > 0 {
			sb.WriteString(` OFFSET ?`)
			args = append(args, q.Offset)
		}
	}

	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("search games: %w", err)
	}
	defer rows.Close()

	out := []Record{}
	for rows.Next() {
		r, err := scanMeta(rows)
		if err != nil {
			return nil, fmt.Errorf("scan game: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
