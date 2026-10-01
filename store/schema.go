package store

// schemaVersion は現在のスキーマ版。PRAGMA user_version に記録する。
//
//	1: 棋譜を溜め込む前提の構成に変更
//	   - 時刻を UTC epoch 秒の INTEGER に（TEXT の辞書順比較はオフセットが
//	     混ざると壊れるため。索引も小さくなる）
//	   - KIF 本文を game_kifu に分離（一覧・検索が本文のページを歩かないように）
//	   - FTS5(trigram) で棋戦名・対局者・場所の部分一致検索
//	2: 取得元 URL（source_url）を追加。URL からの取り込みで出所を残すため
//	3: 棋譜本文を「登録された形式の原本」として持つ。
//	   game_kifu に format（kif/ki2/csa/…）と encoding（元の文字コード）を追加。
//	   変換は保存せず、要求時に行う（kicho/format）
//	4: 追跡中の中継（watches）を追加。2日制の対局で翌日また URL を貼り直さずに
//	   済むよう、「どのサイトのどの棋譜か」を再起動を跨いで持つ
//	5: 人が書く欄を games に追加。直した対局名（event_edited）と備考（note）。
//	   取得した値（event）は書き換えず別の列に持つ（Save の upsert が event を
//	   上書きするため。詳しくは gamesSchemaSQL）。検索の索引にも含める
const schemaVersion = 5

// schemaSQL は最新スキーマ。マイグレーションでも同じものを使う。
const schemaSQL = gamesSchemaSQL + gamesFTSSchemaSQL + watchesSchemaSQL

// gamesSchemaSQL は保存済みの棋譜（蔵書）。
const gamesSchemaSQL = `
CREATE TABLE IF NOT EXISTS games (
    id          TEXT PRIMARY KEY,
    source      TEXT NOT NULL,
    source_id   TEXT NOT NULL,
    source_url  TEXT NOT NULL DEFAULT '',
    event       TEXT NOT NULL DEFAULT '',
    handicap    TEXT NOT NULL DEFAULT '',
    place       TEXT NOT NULL DEFAULT '',
    black       TEXT NOT NULL DEFAULT '',
    white       TEXT NOT NULL DEFAULT '',
    started_at  INTEGER NOT NULL DEFAULT 0,
    end_mark    TEXT NOT NULL DEFAULT '',
    moves       INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT 0,
    -- ここから下は人が書く欄。Save は触らない(upsert の SET にも INSERT の列にも入れない)。
    -- event_edited は直した対局名(空 = 直していない)。event に直接書かないのは、
    -- 取り直して保存した時点で upsert が event を上書きして編集が黙って消えるのと、
    -- 「直してあること」が判らなくなるため。
    event_edited TEXT NOT NULL DEFAULT '',
    note         TEXT NOT NULL DEFAULT ''
);

-- 同じ棋譜を取り直しても重複させない。
CREATE UNIQUE INDEX IF NOT EXISTS idx_games_source ON games(source, source_id);
-- 一覧の既定の並び順。
CREATE INDEX IF NOT EXISTS idx_games_started_at ON games(started_at DESC);
-- 対局者の完全一致（部分一致は FTS 側）。
CREATE INDEX IF NOT EXISTS idx_games_black ON games(black);
CREATE INDEX IF NOT EXISTS idx_games_white ON games(white);

-- 棋譜本文は分離する。1局を開くときだけ触るので、一覧・検索の
-- スキャンが本文のページを読まずに済む。
--
-- body は**登録された形式の原本**。整形し直さずそのまま持つ
-- (整形すると変化・コメント・不成などの情報が落ちるため)。
-- 文字コードは保存時に UTF-8 へ寄せ、encoding には元が何だったかを残す(主にデバッグ用)。
CREATE TABLE IF NOT EXISTS game_kifu (
    game_id  TEXT PRIMARY KEY REFERENCES games(id) ON DELETE CASCADE,
    format   TEXT NOT NULL DEFAULT 'kif',
    encoding TEXT NOT NULL DEFAULT 'utf-8',
    body     TEXT NOT NULL
);
`

// gamesFTSSchemaSQL は games の部分一致検索の索引と、同期トリガ 3 つ。
//
// v4 → v5 の移行で作り直すので gamesSchemaSQL から分けてある(rebuildGamesFTSSQL)。
const gamesFTSSchemaSQL = `
-- 部分一致検索。日本語では既定の unicode61 トークナイザが使いものにならない
-- （CJK の連続を1トークンにするため前方一致しか効かない）ので trigram を使う。
-- content= の外部コンテンツ方式にして本文を二重に持たない。
-- 外部コンテンツ方式は列名で games を読むので、列名は games と一致させること。
-- 対局名は取得した値(event)と直した値(event_edited)の両方を引く。
CREATE VIRTUAL TABLE IF NOT EXISTS games_fts USING fts5(
    event, event_edited, black, white, place, note,
    content='games', content_rowid='rowid', tokenize='trigram'
);

CREATE TRIGGER IF NOT EXISTS games_fts_ai AFTER INSERT ON games BEGIN
    INSERT INTO games_fts(rowid, event, event_edited, black, white, place, note)
    VALUES (new.rowid, new.event, new.event_edited, new.black, new.white, new.place, new.note);
END;

CREATE TRIGGER IF NOT EXISTS games_fts_ad AFTER DELETE ON games BEGIN
    INSERT INTO games_fts(games_fts, rowid, event, event_edited, black, white, place, note)
    VALUES ('delete', old.rowid, old.event, old.event_edited, old.black, old.white, old.place, old.note);
END;

CREATE TRIGGER IF NOT EXISTS games_fts_au AFTER UPDATE ON games BEGIN
    INSERT INTO games_fts(games_fts, rowid, event, event_edited, black, white, place, note)
    VALUES ('delete', old.rowid, old.event, old.event_edited, old.black, old.white, old.place, old.note);
    INSERT INTO games_fts(rowid, event, event_edited, black, white, place, note)
    VALUES (new.rowid, new.event, new.event_edited, new.black, new.white, new.place, new.note);
END;
`

// watchesSchemaSQL は追跡中の中継（UI の「仮の一覧」）。
//
// **棋譜本文は持たない。** ここが持つのは「どのサイトのどの棋譜か」
// （source / source_id / source_url）と、一覧で見分けるための最小限のメタだけ。
// 本文を保存するのは「棋譜保存」（games）の役目で、こちらは
// **2日制の対局で翌日また URL を貼り直さずに済むようにする**ためのもの。
//
// 主キーを (source, source_id) にしてあるのは games の UNIQUE 索引と同じ粒度。
// 同じ中継を何度取り直しても増えず、内容だけが最新化される
// （フロントの cardKey も同じ組み合わせでカードを同一視している）。
const watchesSchemaSQL = `
CREATE TABLE IF NOT EXISTS watches (
    source     TEXT NOT NULL,
    source_id  TEXT NOT NULL,
    source_url TEXT NOT NULL DEFAULT '',
    event      TEXT NOT NULL DEFAULT '',
    black      TEXT NOT NULL DEFAULT '',
    white      TEXT NOT NULL DEFAULT '',
    started_at INTEGER NOT NULL DEFAULT 0,
    end_mark   TEXT NOT NULL DEFAULT '',
    moves      INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (source, source_id)
);
`

// migrateV3ToV4SQL は v3（watches 無し）へ追跡中の中継を足す。
const migrateV3ToV4SQL = watchesSchemaSQL

// migrateV0SQL は「KIF 同居・時刻 TEXT」だった旧スキーマを v1 へ移す。
//
// 時刻は SQLite の strftime('%s', ...) で epoch に変換する。RFC3339 の
// オフセット付き文字列も正しく解釈され、Go の time.Parse と一致することを確認済み。
// 未設定（空文字）は NULL になるので COALESCE で 0 に落とす。
const migrateV0SQL = `
DROP INDEX IF EXISTS idx_games_source;
DROP INDEX IF EXISTS idx_games_started_at;
ALTER TABLE games RENAME TO games_old;
` + schemaSQL + `
INSERT INTO games (id, source, source_id, source_url, event, handicap, place,
                   black, white, started_at, end_mark, moves,
                   created_at, updated_at)
SELECT id, source, source_id, '', event, handicap, place,
       black, white,
       COALESCE(CAST(strftime('%s', started_at) AS INTEGER), 0),
       end_mark, moves,
       COALESCE(CAST(strftime('%s', created_at) AS INTEGER), 0),
       COALESCE(CAST(strftime('%s', updated_at) AS INTEGER), 0)
FROM games_old;

INSERT INTO game_kifu (game_id, format, encoding, body)
SELECT id, 'kif', 'utf-8', kif FROM games_old;

DROP TABLE games_old;
`

// legacyColumns は v0 の中でも古い（end_mark / moves を持たない）DB を
// 移行前に揃えるためのもの。列が既にある場合は呼び出し側でスキップする。
var legacyColumns = []struct{ name, ddl string }{
	{"end_mark", `ALTER TABLE games ADD COLUMN end_mark TEXT NOT NULL DEFAULT ''`},
	{"moves", `ALTER TABLE games ADD COLUMN moves INTEGER NOT NULL DEFAULT 0`},
}

// migrateV1ToV2SQL は v1（source_url 無し）へ列を足す。
const migrateV1ToV2SQL = `
ALTER TABLE games ADD COLUMN source_url TEXT NOT NULL DEFAULT '';
`

// migrateV2ToV3SQL は棋譜本文に形式と文字コードを持たせる。
// 既存データは KIF / UTF-8 として扱う（v3 より前は KIF しか登録できなかった）。
const migrateV2ToV3SQL = `
ALTER TABLE game_kifu RENAME COLUMN kif TO body;
ALTER TABLE game_kifu ADD COLUMN format TEXT NOT NULL DEFAULT 'kif';
ALTER TABLE game_kifu ADD COLUMN encoding TEXT NOT NULL DEFAULT 'utf-8';
`

// v4 → v5: 人が書く欄（直した対局名・備考）を games に足し、検索の索引に含める。
//
// 列が無ければ足し(v5Columns)、FTS とトリガは捨てて作り直してから
// 索引を games から組み直す。FTS5 は列を足せないので作り直すしかない。
// 呼び出し側(migrateV4ToV5)が 1 トランザクションで流す。
var v5Columns = []struct{ name, ddl string }{
	{"event_edited", `ALTER TABLE games ADD COLUMN event_edited TEXT NOT NULL DEFAULT ''`},
	{"note", `ALTER TABLE games ADD COLUMN note TEXT NOT NULL DEFAULT ''`},
}

// dropGamesFTSSQL は v4 までの FTS とトリガを捨てる(列が 4 つで、event_edited / note を持たない)。
const dropGamesFTSSQL = `
DROP TRIGGER IF EXISTS games_fts_ai;
DROP TRIGGER IF EXISTS games_fts_ad;
DROP TRIGGER IF EXISTS games_fts_au;
DROP TABLE IF EXISTS games_fts;
`

// rebuildGamesFTSSQL は FTS を作り直し、既存の games から索引を組み直す。
const rebuildGamesFTSSQL = gamesFTSSchemaSQL + `
INSERT INTO games_fts(games_fts) VALUES('rebuild');
`
