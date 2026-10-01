# store/AGENTS.md

SQLite への永続化。全体像はルートの `AGENTS.md`。

⚠️ **使う側はこのパッケージを直接呼ばない。** `kicho.Library` を通す
（`Library.Store()` は公開していない）。ここに操作を足したら `Library` にも口を足す。

## ドライバ

**PureGo の `modernc.org/sqlite`**。cgo 不要でクロスコンパイルが通る。
`mattn/go-sqlite3` に置き換えないこと。

## 接続設定（`dsn`）

**PRAGMA は DSN に載せる。`Open` で1回 `Exec` する形にしないこと。**
PRAGMA は接続ごとの設定で、`database/sql` は接続が壊れれば黙って張り直すため、
1回きりの `Exec` だとそのとき既定へ戻る（`foreign_keys` が OFF に戻ると
`Delete` しても `game_kifu` に本文が残り、誰も気づかない）。

| PRAGMA | 理由 |
|---|---|
| `busy_timeout(5000)` | 既定 0 は待たずに `database is locked`。同じ DB を複数のプロセスが開きうるので待たせる |
| `journal_mode(WAL)` | 読みが書きをブロックしない。DB ファイルに永続する設定（`-wal` / `-shm` が並ぶ） |
| `foreign_keys(1)` | `game_kifu` の `ON DELETE CASCADE`（既定は OFF） |

⚠️ **パスに `file:` を付けないこと。** 付けると `SQLITE_OPEN_URI` で URI として
解釈され、Windows のパス（`D:\...`）やスペース・`#` を含むパスが壊れる。
付けなければドライバは最初の `?` より前をパスとしてそのまま渡す
（`open_test.go` の `TestOpenHandlesAwkwardPath` で固定）。

接続は `SetMaxOpenConns(1)` で1本に絞ってある。同一プロセス内の書き込みはこれで
直列化され、プロセスをまたぐ競合は上の WAL と busy_timeout が受け持つ。
そのため**止まらないクエリが1つあると以後が全部待たされる**。呼び出し側は
期限付きの ctx を渡すこと（`_cmd/kicho/kifuservice.go` の `dbContext`）。

## スキーマ

棋譜を溜め込んでいく前提で組んである。定義は `schema.go`。

```
games       メタデータのみ（小さく保つ）
game_kifu   KIF 本文（game_id で 1:1、ON DELETE CASCADE）
games_fts   FTS5(trigram) の外部コンテンツ索引 + 同期トリガ3つ
watches     追跡中の中継（＝仮の一覧。棋譜本文は持たない）
```

`(source, source_id)` に UNIQUE 制約があり、**同じ棋譜を取り直しても重複せず、
kicho 自前の `id` と `created_at` は維持されたまま内容だけ更新される**
（`/kifu/{id}` の URL が変わらないことが重要）。

**設計上ゆずれない点:**

- **時刻は UTC epoch 秒の INTEGER。** TEXT の RFC3339 で持つと、
  オフセット表記が混ざった瞬間に辞書順比較が壊れる
  （`...T10:00:00+09:00` と同時刻の `...T01:00:00Z` が逆転する）。
  `ORDER BY started_at` と範囲検索が索引で効くのはこの形だから。
- **KIF 本文を `games` に戻さない。** 一覧・検索は本文を使わないのに、
  行に本文があるとページあたりの行数が激減してスキャンが遅くなる。
  `Get` / `List` だけが `game_kifu` を join する。
- **`moves` は列として持つ。** 一覧で本文を読まずに手数を出すため。
  KIF の書式を知っているのは呼び出し側なので `store` では数えず受け取る。

### 人が書く欄（`event_edited` / `note`。v5）

直した対局名と備考。書くのは `Annotate` だけ。

- ⚠️ **`Save` はこの 2 列に触らない**（upsert の SET にも INSERT の列にも入れない）。
  入れると取り直して保存した時点で人の編集が黙って消える
  （`store_test.go` の `TestAnnotateSurvivesResave`）
- ⚠️ **取得した値（`event`）を書き換えない。** 直した値は `event_edited` に置き、
  表示だけが `Record.DisplayEvent` で差し替える。**`httpapi` は取得した値のまま配る**
- 2 列とも `games_fts` に入れてある。⚠️ **FTS の列を変えたら `whereClause` の
  LIKE 側も揃える**（語の長さで当たり外れが割れる）

### watches

- 主キーは `(source, source_id)`。`games` の UNIQUE 索引ともフロントの `cardKey` とも
  同じ粒度なので、同じ中継を何度取り直しても増えない
- 並びは `created_at DESC`（**追跡し始めた順**）。⚠️ `updated_at` にすると
  取り直すたびにカードの位置が入れ替わる
- 持つのは取得元とメタ（棋戦名・対局者・開始日時・手数・終局種別）だけ。
  **KIF 本文・手合割・場所・文字コードは持たない**（表示用の写しであって原本ではない）

## マイグレーション

`PRAGMA user_version` で版を管理する（`migrate.go`）。

- 版を上げるときは `schemaVersion` を増やし、移行 SQL を足す
- v1 より前の DB は `user_version` が 0 なので、`games` の形
  （`kif` 列の有無）で「新規」「旧スキーマ」を判定している
- 移行は1トランザクション。失敗すれば元の `games` が残る
- **自分より新しい版の DB は開かない**（`ErrSchemaTooNew`）。
  このライブラリを使うアプリは別々にビルドされるので、片方だけ更新した状態で
  同じ DB を指すと版が食い違いうる。黙って開くと後段が `no such column` で落ちて
  原因が分からなくなるため、`Open` の時点で理由を返す。
  ⚠️ **`version >= schemaVersion` で早期 return する形にしないこと**
- ⚠️ **版を上げると、DB を共用している場合に更新していないアプリが開けなくなる**。
  どちらもビルドし直せば解消する

時刻の変換は SQLite の `strftime('%s', ...)` で行う。RFC3339 のオフセット付き
文字列も正しく解釈され、Go の `time.Parse` と一致することを確認済み。

## 検索

`Search(ctx, Query)` — 棋戦名（取得した値と直した値）・対局者・場所・備考の部分一致、開始日の範囲、
終局済みのみ、件数制限。KIF 本文は返さない。件数は `CountQuery(ctx, Query)`。

**条件の組み立ては `whereClause` に 1 か所だけ置く。** `Search` と `CountQuery` が
共有していて、片方にだけ条件を足すと UI の「N 件中 M 件」が嘘になる。

`Limit 0` は**無制限のまま**にしておく。上限はアプリの一覧（`kicho.Library.Search`）
だけが掛ける。`httpapi` の `GET /kifu` は外部ツールが全件を期待するので通さない。

**trigram は 3 文字以上でないと索引が効かない**（実測: `"竜王戦"` は一致、
`"決勝"` は不一致）。そのため 3 文字未満は `LIKE` へフォールバックする
（`MinTrigramLen`）。UI では 3 文字未満のとき「全件走査になる」旨を出している。

既定の unicode61 トークナイザは日本語では使えない（CJK の連続を 1 トークンに
するため前方一致しか効かない）。**`tokenize='trigram'` を外さないこと。**

FTS のクエリは `escapeFTS` でフレーズとしてくくる（`OR` や `NEAR()` を
検索語として入力されても構文エラーにしないため）。`LIKE` 側は `escapeLike` で
`%` `_` を無効化する。
