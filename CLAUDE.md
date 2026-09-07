# CLAUDE.md

kicho（棋帳）。棋譜を取得して保存し、外部ツールへ HTTP で配信する **Wails3 デスクトップアプリ**。
リポジトリ全体の方針はルートの `CLAUDE.md` を参照。

> 旧称は `kifu`、旧実装は Node.js + express。「棋譜 / kifu」は共有モジュール
> `core/web/kifu.js`・KIF 形式・URL パスなど各所で使う語で紛らわしいため、
> プロジェクト名を `kicho` に改名し、実装を Go + Wails3 に移した。
> **`core/web/kifu.js`（KIF 形式の変換ロジック）と `/ryuoh/kifu/{id}`（棋譜の URL）は
> 改名対象ではない**ので、そのまま `kifu` を使う。

## ⚠️ `ikkyoku` がライブラリとして使っている（2026-09-04）

**`ikkyoku`（一局。ユーザ環境で動く将棋ソフト）が `kicho.Library` を直接 import し、
棋譜の取得・登録・蔵書の UI をあちらへ移した。** ユーザが ikkyoku と kicho の
2 つを立ち上げずに済むようにするため。

**棋譜データベースとしての実装はこちらのまま**で、ikkyoku は利用する側。
**このリポジトリの UI は将来「テスト用のモック」または
「ikkyoku 以外の将棋ソフトからの読み込み口」になる。**

- ikkyoku が使うのは `kicho.Open(dbPath, logger)` と `Library` のメソッド、
  それに `scrape` の部品（`DecodeKIF` / `ReadLimited` / `LooksLikeHTML` /
  `KifURLFromHTML`）。**この移行のために kicho 側は 1 行も変えていない**
- ⚠️ **公開 API を変えるときは ikkyoku 側も直すこと**（`_cmd/ikkyoku/kifuservice.go`
  が `_cmd/kicho/kifuservice.go` の移植で、`store.Game` / `store.Query` /
  `store.Source*` をそのまま使っている）
- ⚠️ **DB は別の場所が既定。** ikkyoku は `%APPDATA%\ikkyoku\kicho.db`、
  kicho は `%APPDATA%\kicho\kicho.db`。共用は ikkyoku の設定で明示したときだけ。
  ikkyoku の設定にはファイルピッカーがあるので**ユーザは実際に共用できてしまう**。
  そのため WAL + `busy_timeout` を入れて共用に耐えるようにした（「接続設定」を参照）が、
  **共用を推奨する状態ではない**（別々の DB が既定なのは変えていない）
- ⚠️ **`ServerService`（HTTP 配信）は移していない。** ikkyoku はサーバを持たず、
  「棋譜 URL をコピー」の代わりに「解析する」を置いている。**外部ツール
  （ShogiHome 等）へ配るのは今のところこちらの役目**

## 構成

Wails 依存を `_cmd/kicho/` に閉じ込め、ロジックは親モジュール `github.com/ShinteLab/kicho` 側の
通常パッケージに置く（wails3 skill のアーキテクチャ方針）。

`core` はタグ未発行のため `replace github.com/ShinteLab/core => ../core` の相対パス参照で引いている。
Wails アプリ側（`_cmd/kicho/`, module `kicho-app`）は `core` と `kicho` の両方を replace で参照する。

| パッケージ | import パス | 役割 |
|---|---|---|
| `kicho`（root） | `github.com/ShinteLab/kicho` | ユースケース。取得→保存を束ねる `Library` |
| `scrape/` | `github.com/ShinteLab/kicho/scrape` | 中継サイトからの取得。読売のペイロード評価、連盟の `.kif` 取得、文字コード判別 |
| `store/` | `github.com/ShinteLab/kicho/store` | SQLite への永続化 |
| `httpapi/` | `github.com/ShinteLab/kicho/httpapi` | 棋譜配信 HTTP サーバ（起動/停止・bind 設定） |
| `settings/` | `github.com/ShinteLab/kicho/settings` | 設定の永続化と保存先パスの解決 |
| `_cmd/kicho/` | — | **Wails3 アプリ**（独立したネストモジュール `kicho-app`） |

```
_cmd/kicho/services  →  github.com/ShinteLab/kicho  →  scrape / store / httpapi / settings
     ↑
  Wails 依存はここだけ（main.go, kifuservice.go, serverservice.go）
```

`internal/` 相当のロジックに `github.com/wailsapp/wails/v3` を import しないこと。

## コマンド

```powershell
# ロジック側（kicho/ から。_cmd 配下は go build ./... の対象外）
go test ./...

# Wails アプリ（_cmd/kicho で実行）
cd kicho/_cmd/kicho
npm --prefix frontend install          # 初回のみ
wails3 generate bindings -ts -i -d frontend/bindings
npm --prefix frontend run build
go build -o bin/kicho.exe .            # または wails3 build / wails3 dev
```

- **Go のサービスを変えたら必ず `wails3 generate bindings`** を実行する。
  忘れるとフロントが無言で古い型を使う。
- bindings はインターフェース生成（`-i`）。フロントは `import type` で受ける。
  クラス生成に切り替えるなら Taskfile 側のフラグも合わせること（wails3 skill の落とし穴 12）。
- `frontend/dist` が無いと `//go:embed all:frontend/dist` が失敗して Go のビルドが通らない。
  先にフロントをビルドする。

### Taskfile からモバイルを外してある

`Taskfile.yml` の `includes` で **ios / android をコメントアウト**している。

`build/android/Taskfile.yml` は**読み込み時に評価される `vars:`** で
`uname -m` や `... | tail -1` を呼ぶ。go-task の shell は組み込みで持たないコマンドを
PATH から探すため、Windows では

```
"uname": executable file not found in $PATH
"tail": executable file not found in $PATH
```

が出る。動作自体は壊れないが（変数が空になるだけ）、デスクトップ専用なので読み込まない。
prokishi 側も同じ理由でコメントアウトしてある。

⚠️ `wails3 update build-assets` は `Taskfile.yml` を再生成するので、
実行後は includes を戻し忘れないこと。

## 保存先

`os.UserConfigDir()/kicho/` に置く（Windows なら `%APPDATA%\kicho\`）。

- `kicho.db` — 棋譜 DB（SQLite）。**WAL なので `kicho.db-wal` / `kicho.db-shm` が並ぶ**
- `settings.json` — bind アドレス・ポート・自動起動

DB ドライバは **PureGo の `modernc.org/sqlite`**。cgo 不要でクロスコンパイルが通る。
`mattn/go-sqlite3` に置き換えないこと。

### 接続設定（`store.dsn`）

**PRAGMA は DSN に載せる。`Open` で1回 `Exec` する形に戻さないこと。**
PRAGMA は接続ごとの設定で、`database/sql` は接続が壊れれば黙って張り直すため、
1回きりの `Exec` だとそのとき既定へ戻る（`foreign_keys` が OFF に戻ると
`Delete` しても `game_kifu` に本文が残り、誰も気づかない）。

| PRAGMA | 理由 |
|---|---|
| `busy_timeout(5000)` | 既定 0 は待たずに `database is locked`。ikkyoku との共用があるので待たせる |
| `journal_mode(WAL)` | 読みが書きをブロックしない。DB ファイルに永続する設定 |
| `foreign_keys(1)` | `game_kifu` の `ON DELETE CASCADE`（既定は OFF） |

⚠️ **パスに `file:` を付けないこと。** 付けると `SQLITE_OPEN_URI` で URI として
解釈され、Windows のパス（`D:\...`）やスペース・`#` を含むパスが壊れる。
付けなければドライバは最初の `?` より前をパスとしてそのまま渡す
（`store/open_test.go` の `TestOpenHandlesAwkwardPath` で固定）。

接続は `SetMaxOpenConns(1)` で1本に絞ってある。同一プロセス内の書き込みはこれで
直列化され、プロセスをまたぐ競合は上の WAL と busy_timeout が受け持つ。

### スキーマ

棋譜を溜め込んでいく前提で組んである。定義は `store/schema.go`。

```
games       メタデータのみ（小さく保つ）
game_kifu   KIF 本文（game_id で 1:1、ON DELETE CASCADE）
games_fts   FTS5(trigram) の外部コンテンツ索引 + 同期トリガ3つ
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

### マイグレーション

`PRAGMA user_version` で版を管理する（`store/migrate.go`）。

- 版を上げるときは `schemaVersion` を増やし、移行 SQL を足す
- v1 より前の DB は `user_version` が 0 なので、`games` の形
  （`kif` 列の有無）で「新規」「旧スキーマ」を判定している
- 移行は1トランザクション。失敗すれば元の `games` が残る
- **自分より新しい版の DB は開かない**（`store.ErrSchemaTooNew`）。
  kicho と ikkyoku は別バイナリなので、片方だけ更新した状態で同じ DB を
  指すと版が食い違いうる。黙って開くと後段が `no such column` で落ちて
  原因が分からなくなるため、`Open` の時点で理由を返す。
  ⚠️ **`version >= schemaVersion` で早期 return する形に戻さないこと**

時刻の変換は SQLite の `strftime('%s', ...)` で行う。RFC3339 のオフセット付き
文字列も正しく解釈され、Go の `time.Parse` と一致することを確認済み。

設定 JSON は **UTF-8 BOM を許容**している（Windows のメモ帳が既定で BOM を付けるため）。

### 検索

`store.Search(ctx, Query)` — 棋戦名・対局者・場所の部分一致、開始日の範囲、
終局済みのみ、件数制限。KIF 本文は返さない。

**trigram は 3 文字以上でないと索引が効かない**（実測: `"竜王戦"` は一致、
`"決勝"` は不一致）。そのため 3 文字未満は `LIKE` へフォールバックする
（`MinTrigramLen`）。UI では 3 文字未満のとき「全件走査になる」旨を出している。

既定の unicode61 トークナイザは日本語では使えない（CJK の連続を 1 トークンに
するため前方一致しか効かない）。**`tokenize='trigram'` を外さないこと。**

FTS のクエリは `escapeFTS` でフレーズとしてくくる（`OR` や `NEAR()` を
検索語として入力されても構文エラーにしないため）。`LIKE` 側は `escapeLike` で
`%` `_` を無効化する。

## 棋譜の入り口は4つ

| 入り口 | source | source_id | 重複 |
|---|---|---|---|
| 読売（竜王戦）の中継（取得タブ） | `yomiuri` | 読売の棋譜 ID | 取り直すと**同じ棋譜を更新** |
| 日本将棋連盟の中継（取得タブ） | `shogilive` | 中継のパス（拡張子なし） | 取り直すと**同じ棋譜を更新** |
| URL から取得（登録タブ）※ | `url` | 毎回新しい UUID | **登録のたびに別の棋譜** |
| KIF を貼り付け（登録タブ） | `paste` | 毎回新しい UUID | **登録のたびに別の棋譜** |

※ `.kif` の URL のほか、**棋譜中継ページ（HTML）の URL** も受け付ける（後述）。

**取得タブ（ライブ中継）と登録タブ（単発の取り込み）で扱いが違う。**
中継は取得元での一意な ID があるので取り直しても同じ棋譜として更新される。
URL・貼り付けはその ID が無いため毎回新規登録にしてある
（同じものを2回登録すれば2件になる）。重複は UI から手動で削除する。
`source_url` に取得元 URL を残す。

**URL からの取得はスクレイピングではない。** URL の中身をそのまま KIF として読む。
他サイトの `.kif` でも、別の kicho の `/kifu/{id}` でも取り込める。

#### 棋譜中継ページ（HTML）の URL も辿れる

`.kif` の URL を探して貼り替える手間をなくすため、HTML を渡されたら
そこに書かれた `.kif` を辿る（`scrape/fetch.go` の `KifURLFromHTML`）。
**ページから対局内容を読み取るわけではない**ので、これはスクレイピングではない。
取り込むのはあくまで `.kif` の原本。

- 中継ページは JS の定数 `const KIF_FILE_NAME = "/oui/kifu/67/oui202607290101.kif";` を持つ。
  中継ビューア（`/common/js/kj/kj.js`）がこれを見て棋譜を読む。**この定数が一次情報**で、
  拡張子の付け替え（`.html` → `.kif`）には依存していない
- 見つからなければ `.kif` で終わる `<a href>` を探す（他サイト向けの保険）。
  どちらも無ければエラー
- 相対パスは取得元 URL を基準に解決する

登録タブ側（`Library.FetchKIFFromURL`）では、**HTML が返ってきたときだけ**この解決を挟む。
判定は Content-Type ではなく中身で行う（KIF は `<` で始まらない → `scrape.LooksLikeHTML`）。
`source_url` には貼られたページの URL をそのまま残す（`.kif` に書き換えない）。

⚠️ **連盟の中継（`live.shogi.or.jp`）は登録タブではなく取得タブで扱う。**
対局中は棋譜が伸びていくので、カードとして積んで「更新」で取り直す側でないと使いにくい。
登録タブから入れると `source_id` が毎回 UUID になり、取り込むたびに別の棋譜として増える。

### 指し手が無い棋譜（対局前）はエラーにしない

中継は**対局開始前から棋譜を置いている**。ヘッダ（棋戦・対局者・場所）だけで
指し手がまだ 1 手も無い `.kif` が返る（例: `oui/kifu/67/oui202608180101`）。
読売のペイロードも対局前は `data.kifu.kifu` が空配列で来る。

**「1 手も無い」は取得の失敗ではない。** `core/kifu.Parse` も `scrape` の
ペイロード解析もエラーにせず、手数 0 のまま取得・保存できる。
UI は手数の横に「（対局前）」を出す。

「そもそも KIF ではない」（HTML やただの文章）の判定は手数ではなく
`kifu.Document.Empty()`（ヘッダも指し手も取れなかった）で行う。
登録タブの取り込み（`Library.importDocument`）はこれで弾いている。

### 原本を保持する（重要）

**棋譜本文は登録された形式の原本をそのまま保存する。整形し直さない。**
整形すると変化（`変化：N手`）・コメント（`*`）・`不成` などの情報が落ちるため。

解析（`core/kifu.Parse`）は**一覧と検索に使うメタデータの抽出だけ**に用いる。
`game_kifu` に `format`（kif/ki2/…）と `encoding`（元の文字コード）を持つ。

手を DB に正規化して持つ設計は採らない。形式固有の情報が落ちるうえ、
形式ごとに持つと件数 × 形式で膨らむ。変換は決定的なので都度計算で足りる。

### 形式変換（`kicho/format`）

`/kifu/{id}.{ext}` の変換はここに登録する。**現時点で実装済みの変換は無い**。

- 原本と同じ形式を要求されたらそのまま返す
- 未実装なら `ErrUnsupported` → HTTP **501**（対応済みの形式一覧を本文に出す）
- 増やすときは `format.Register(from, to, fn)` を呼ぶだけ

**変換の難易度は形式によって大きく違う。**

| 変換 | 難易度 | 理由 |
|---|---|---|
| KIF → CSA / USI / SFEN / JKF | 中 | KIF は移動元座標を持つので機械的。`同` の解決と成/不成に注意 |
| **KIF → KI2** | **高** | **合法手生成が必要**。KI2 は移動元を書かず `右/左/上/引/寄/直` で区別するため、「その升に動ける同じ駒が他にあるか」を知る必要がある |
| → USEN | 不明 | ShogiHome 独自形式。仕様確認から |

合法手生成は `github.com/ShinteLab/engine` にある。ただし**ルート CLAUDE.md の依存規約は
`engine → core` の一方向**なので、`core/kifu` から engine は参照できない。
`kicho → engine` は循環しないため、engine が要る変換は `kicho/format` 側に置くこと
（機械的な変換は `core/kifu` に置いてよい）。

### 文字コード

世に出回る `.kif` は **Shift_JIS が多い**（Kifu for Windows 等の既定）。
`DecodeKIF` が UTF-8 として妥当ならそのまま、そうでなければ Shift_JIS として読む。
**本文は UTF-8 に寄せて保存**し、`encoding` には元が何だったかを記録する（主にデバッグ用）。
BOM は `core/kifu` の Parse 側で落とす。

### 諸元（どこから取ったか）

`source_url` に取得元を残す。

- 読売スクレイピング → 棋譜ビューアの URL（`scrape.ViewerURL`）
- 連盟の中継 → 中継ページ（HTML）の URL（`ShogiLive.ViewerURL`）。`.kif` ではなく
  **人が開いて確認できるほう**を残す
- URL 取り込み → 指定された URL
- 貼り付け → 空

## 2系統の役割分担（重要）

**ライブ取得とアーカイブは別物として扱う。統合しないこと。**

- **対局中** … 棋譜は随時更新されるので、その都度サイトへ取りに行く。
  これが `GET /ryuoh/kifu/{id}` `GET /shogilive/kifu/{id}`（保存しない）と UI の「取得」。
- **終局後** … 以後サイトへアクセスしなくて済むように DB へ保存する。
  これが `GET /kifu/{id}` と UI の「保存」。

**ライブ取得元は2つ**（読売＝スクレイピング、連盟＝`.kif` の直取得）。
取り方は違うが、カードに積む・更新する・保存する・ライブ URL を配る、の扱いは同じにしてある。
`KifuService` が `store.Source*` で分岐し、UI 側は取得元を意識しない
（カードに取得元のラベルを出すだけ）。

そのため UI は **取得と保存を別操作**にしてある（「取得して保存」の一括操作は置かない）。
取得 → 内容を確認 → **その表示内容をそのまま保存**する流れで、保存時にサイトへ
取り直しには行かない（`KifuService.Save` は画面の `GameDetail` を受け取る）。

対局中でも保存はできる（禁止していない）。`(source, source_id)` の upsert なので、
対局中に保存 → 終局後に取り直して保存、で**同じ ID のまま最新化**される。
取り直しはカードの「更新」で行う（後述）。

### 取得結果はカードとして積む

**取得は1件しか持たない設計にしない。** 同じ日に2局以上進むこと（竜王戦以外の並行対局、
複数局を交互に追う）があるため、取得のたびに `FetchCard` を1枚追加する（新しいものが先頭）。
カードごとに「保存」「削除」を持ち、削除は**画面からカードを外すだけ**で
保存済みの棋譜（DB）は消さない。「クリア」は入力とカードを全部捨てる。

**同じ棋譜を取り直したときはカードを増やさない。** 対局中は同じ棋譜を繰り返し取りに行くため、
`FetchCard.key` が一致するカードは位置と `saved` を保ったまま中身だけ差し替える。
入力欄からの「取得」もカードの「更新」も、この `mergeCard` を通す。

`key` は **`{source}:{sourceId}`**（`cardKey`）。棋譜 ID の形が取得元ごとに違う
（読売は 24 桁の ID、連盟は中継のパス）ので取得元も含める。
`(source, source_id)` の upsert で保存側が同一視するのと同じ粒度。

### カードの「更新」

**保存は画面の内容をそのまま書き込む**（`Save` はサイトへ取りに行かない）ので、
更新の手段が無いと、対局中に取ったカードを後で保存しても**取得した時点の古い棋譜**が入る。
そのためカードごとに「更新」を置き、`KifuService.Refresh(source, sourceId)` で
取り直して中身を差し替える。

- 入力欄からの `Fetch(input)` と分けてあるのは、更新では**取得元がすでに分かっている**ため。
  入力の判別（`scrape.IsShogiLiveURL`）も、URL から棋譜 ID を引き直す往復
  （読売なら iframe の取得、連盟なら中継ページの取得）も挟まらない
- 手数の変化を案内に出す（`refreshNotice`）。対局が進んだかどうかが知りたいため
- **保存済みのカードで手数が変わったら「保存し直す」よう促す。** 更新しただけでは
  DB は古いままなので、ここを黙っていると更新＝保存だと誤解される

### 取得タブの状態はタブを跨いで保持する

タブは条件レンダリングで切り替えるため、素直に書くと切り替えのたびに
アンマウントされて入力した URL と取得したカードが消える。そのため取得タブの状態
（入力・カードの配列）は `App` 側に持ち上げてある（`FetchState`）。

**ディスクへは永続化しない**（アプリを閉じればクリアされる）。保存後も入力とカードは残す
（URL をコピーしたり、終局後に取り直して保存し直したりできるようにするため）。

終局判定は取得元によって出どころが違うが、`Finished` として UI に渡すところは同じ。
手数の横に「（終局）」を出す。

- 読売 … ペイロードの `end_mark`（終局していれば "投了" 等、対局中は空）
- 連盟 … `.kif` の最終手が終局手かどうか（`kifu.Document.EndMark()`）

UI ではこの2系統に対応する URL をそれぞれコピーできる。

- **棋譜 URL**（`/kifu/{id}`）… 保存済み。サイトへは取りに行かない
- **取得 URL**（`/ryuoh/kifu/{sourceId}` / `/shogilive/kifu/{sourceId}`）…
  開くたびにサイトから取り直す。対局中向け。取得元による出し分けは
  `ServerService.SourceURLs(source, sourceID)`

## HTTP エンドポイント

ShogiHome 等の外部ツールが「URL から棋譜を取得する」機能で使う。

| ルート | 内容 |
|---|---|
| `GET /kifu` | 保存済み棋譜の一覧（JSON）。0 件でも `null` でなく `[]` を返す |
| `GET /kifu/{id}` | 保存済み棋譜を**登録された形式の原本のまま**返す |
| `GET /kifu/{id}.{ext}` | その形式に変換して返す。未実装なら **501** |
| `GET /ryuoh/kifu/{id}` | 読売から直接取得して KIF を返す（**Node 版との互換**。保存はしない） |
| `GET /shogilive/kifu/{id...}` | 連盟の中継から直接取得して KIF を返す（保存はしない） |
| `GET /` | 使い方の案内 |

`/shogilive/kifu/` だけ `{id...}` のワイルドカード。連盟の棋譜 ID は中継のパス
（`oui/kifu/67/oui202607290101`）で**スラッシュを含む**ため。
`httpapi.ShogiLiveKifuPath` はスラッシュを区切りとして残し、各セグメントだけを
エスケープする（`url.PathEscape` を全体にかけると `%2F` になって経路に届かない）。

サイトの `.kif` を直接使わず kicho を経由させているのは、**連盟が Shift_JIS で
配信している**ため。ここを通すと UTF-8 で返る。

### URL の組み立ては Go 側に一本化する

外部ツールへ貼る URL は UI の「URL コピー」で渡す。パスの組み立ては
`httpapi.KifuPath` / `httpapi.RyuohKifuPath` にあり、**フロントにパスを直書きしない**。
ベース URL（ホスト・ポート・LAN アドレス）は `ServerService.KifuURLs` /
`SourceURLs` が付けて返す。

- パス組み立てが `routes()` の登録と食い違うと URL コピーが壊れるため、
  `httpapi` のテストで**実際に到達できるか**を検証している（`TestPathBuildersReachTheRoutes`）。
- **サーバ停止中でも URL は返す**（設定値から組み立てる）。貼り付け先で使う頃に
  起動していればよいため。
- LAN 公開時は localhost 版を先頭に、到達可能な LAN アドレス版も返す。
  コピーされるのは先頭（localhost 版）。bind の `0.0.0.0` 自体は貼っても届かないので出さない。

- 既定は `127.0.0.1:3000`（Node 版と同じポート）。UI の「サーバ」タブで bind とポートを変更できる。
- **`0.0.0.0` にすると認証なしで LAN に公開される。** UI で警告を出している。
  認証を付ける場合は `httpapi` 側に入れること。

## 日本将棋連盟の中継（`scrape/shogilive.go`）

`http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html`

**スクレイピングではない。** 連盟は中継ページの隣に `.kif` をそのまま置いているので、
取りに行くのはその `.kif`。HTML を読むのは「どの `.kif` か」を決めるときだけ
（`KIF_FILE_NAME`。前述）。取得元としては読売と並ぶが、実装の性質は逆で、
**読売は構造化データから KIF を組み立てる／連盟は配信されている KIF が原本**。

### 棋譜 ID は中継のパス

`source_id` は拡張子を落としたパス（`oui/kifu/67/oui202607290101`）。

サイトは **S3 バケットをそのまま公開しているだけ**で、一覧も検索も無く
（`/` は `NoSuchKey` の 404）、パス以外に棋譜を指す ID が存在しない。
ID からは `.kif` / `.html` の URL を組み立て直せる必要がある（カードの「更新」と
ライブ経路で使う）ので、パスをそのまま持つのが素直。

そのため **`source_id` にスラッシュが入る**。HTTP のルートを `{id...}` にしてあるのはこのため。

### http のみ / Shift_JIS

- **https では接続できない**（TLS の待ち受けが無い）。`ShogiLiveBaseURL` を
  `https://` に変えないこと
- 配信は **Shift_JIS**。`DecodeKIF` で UTF-8 に寄せ、`encoding` に元を記録する

### 終局判定は `.kif` の最終手

終局すると `101 投了` の行と `まで100手で後手の勝ち` が付く。
`core/kifu.Parse` は `まで` 行を読み飛ばし、最終手が終局手なら `EndMark()` が返る。

### 手数はコメント行を数えない

連盟の `.kif` は `# --- Kifu for Windows ...` に始まり、指し手の間に `*` の
観戦記コメントが大量に入る。**手数は行数ではなく `core/kifu.Parse` の結果で数える**
（`KifuService.countMoves`）。行を数えるとコメントまで手数に入る。

### 動作確認に使える中継（第67期王位戦七番勝負）

| 局 | パス |
|---|---|
| 第２局（終局済み） | `oui/kifu/67/oui202607150101` |
| 第３局 | `oui/kifu/67/oui202607290101` |

## スクレイピングの仕組み（読売・重要）

### ペイロードは JS 評価が必須

`https://www.yomiuri.co.jp/kifu/s/{id}/_payload.js` は Nuxt のペイロードで、
`export default (function(a,b,...){...}(...))` の IIFE。値が引数リストの変数として
共有（重複排除）されているため、**文字列パースでは解けない**（`handicap:a` のような参照が残る）。

そのため **goja（PureGo の JS エンジン）で評価**している（`scrape/payload.go`）。
`export default ` を剥がして式として `RunString` する。

- 第三者サイトのコードを実行するので、**`vm.Interrupt` による 10 秒のタイムアウトは外さないこと**。
- goja は null/undefined へのプロパティアクセスで panic するため `recover` でエラーに落としている。

### side は player1 の手番

`data.kifu.side` は **player1 から見た手番**。取り違えると保存する棋譜すべてで
先後が反転する。第37期竜王戦第1〜5局で

- `side` が先後交互になること
- 「残り時間が最初に減った手数の偶奇（奇数手＝先手）」と 5/5 一致すること

を実データで確認済み。`scrape/payload_test.go` で両方向を固定している。

### 終局手は "投了打" で来る

ペイロードの最終要素は `move:"投了打"`（`end_mark:"投了"`）。そのまま出すと
`104 投了打(00)` という打ち手のような行になり外部ツールが壊れるため、
`core/kifu` の `TerminalMarker` で終局と判定し `104 投了` として出力する。
Node 版はここが未対応だった。

### 記録係の入力ミスがそのまま配信される

ペイロードの `move` は記録係が入力した文字列で、**校正されていない**。打ちの手に "打" が
二重に付いた `move:"７五桂打打"` が実在する（第39期竜王戦挑戦者決定三番勝負第３局・91手目、
`6a74125e9a7091804958e73e`）。`fr_y:10`（＝駒台。打ちの手はすべてこの値で、`fr_x` が駒種）
なので打ちであること自体は正しく、`打` が1つ余分なだけ。

そのまま出すと `91 ７五桂打打(00)` になり、外部ツールが駒種を読めずそこで止まる。
`打打` は KIF として一意に無効な表記なので、`scrape.normalizeMoveName` で潰している。

**直すのは取り込み時（`parsePayload`）。DB 側に手編集を足すのでは解決しない。**
ライブ経路（`/ryuoh/kifu/{id}`）は DB を通らないので、対局中はそちらが直らないため。
読売は KIF を配信しておらず構造化データから KIF を組み立てるのは kicho 側なので、
ここが原本の生成地点にあたる（「原本を保持する」方針とは矛盾しない）。

同種の表記ゆれが出たらここに足す。

### 棋士コメントは棋戦による

各手の `comment` は**観戦記者の記述**で、`related_link1_url` は関連記事へのリンク。
七番勝負（タイトル戦本戦）には入っているが、**挑戦者決定三番勝負には 1 件も無い**。

| 棋譜 | 手数 | comment | link |
|---|---|---|---|
| 第37期七番勝負第１局 `66f2539c848c20bac7cb8002` | 119 | 38 | 9 |
| 第37期七番勝負第５局 `673ada1ad7cc2b9e792fe87c` | 93 | 43 | 5 |
| 第39期挑決三番勝負第３局 `6a74125e9a7091804958e73e` | 131 | 0 | 0 |

**kicho は現状 `comment` を取り込んでいない**（`parsePayload` が読んでいない）。
KIF に載せるなら `*` 行。`num:0` のコメント（対局前の記述）は初手より前に置くことになる。

### 動作確認に使える棋譜 ID

旧 Node 版の `public/ryuoh/index.html` に載っていたもの（第37期竜王戦七番勝負）。
`GET /ryuoh/kifu/{id}` や UI の「取得」タブの動作確認に使える。

| 局 | 棋譜 ID |
|---|---|
| 第１局 | `66f2539c848c20bac7cb8002` |
| 第２局 | `67037f045dcc1abf6b9528e3` |
| 第３局 | `670e11f0311d0bf2678cb19b` |
| 第４局 | `6729b4c597eea3a079e831dc` |
| 第５局 | `673ada1ad7cc2b9e792fe87c` |

対局ページ URL の例:
`https://www.yomiuri.co.jp/igoshougi/ryuoh/kifu/20241114-SYT8T5999945/`

### 消費時間は分単位までしか無い

各手の `spend`（消費秒数）は**必ず 60 の倍数**。読売は分単位でしか記録しておらず、
秒単位の精度は元データに存在しない。`timelimit` は分、`countdown` は秒。

第37期竜王戦第1〜5局で、**`spend` の合計が「残り時間の減少分」と先後とも完全一致**
することを確認済み（10/10）。したがって KIF の累計消費時間は spend の積み上げで作れる。
（この一致は `side`＝player1 の手番という解釈の裏付けにもなっている。）

### ペイロードで未使用の情報

必要になったらここから足せる。

| フィールド | 内容 |
|---|---|
| `time` | 各手の実時刻（"2024-10-19 09:01:06"） |
| `remainingtime_p1/p2` | 各手時点の残り時間（秒） |
| `comment` | 各手の解説コメント |
| `end_reason` / `end_side` / `end_tesu` | 終局の理由・勝敗・手数 |
| `recordman` / `note` | 記録係・備考 |
| `to_x` / `to_y` / `koma_index` / `prmt` | 移動先・駒種・成りフラグ（KIF は漢字表記のみ使用） |
| `koma_positions` | 各手時点の全駒配置（SFEN 化に使える可能性あり） |
| `related_link1_url` ほか | 関連記事リンク |

### 対局ページ → 棋譜 ID

対局ページの `iframe.p-shougi-iframe` の `src`（`.../kifu/s/{id}/`）から ID を取る。
`golang.org/x/net/html` で解析している（クラス名は部分一致で誤爆しないよう厳密比較）。

外部サイトの構造に強く依存するので、取得が壊れたらまずこの2点（iframe のクラス名、
ペイロードのプロパティパス）を疑うこと。

## core との分担

**KIF 形式の仕様は `core/kifu`（Go）に置く。kicho 側に書かない。**

- `core/kifu` … 指し手行の書式・終局判定・ヘッダ付き KIF ドキュメントの組み立て
- `core/web/kifu.js` … 同じ仕様の JS 実装（ブラウザの棋譜ビューア向け）

指し手行の書式（`FormatLine` / `TerminalMarker`）は Go と JS で挙動を揃える。
片方だけ直さないこと。テストは `go test ./core/kifu` と `node core/web/test.mjs`。

## 未対応 / 今後

- ライブ取得元は**読売（竜王戦）と日本将棋連盟の中継**の2つ。他サイトは未対応。
  増やすときは `scrape` に足し、`store.Source*` に定数を追加して、
  `KifuService.Fetch` / `Refresh` / `Save` と `ServerService.SourceURLs` の分岐に加える。
- **連盟は棋戦を絞っていない。** ID は中継のパスなので、王位戦以外でも
  中継ページの URL さえ貼れば取れるはず（`oui` 以外は未確認）。
- **盤面表示は未実装。** kicho が持つのは KIF（漢字表記）で、`@shinte/web` の
  `<shogi-board>` は SFEN 入力。
  ⚠️ **「engine が要る」という以前の記述は古い**（2026-09-04 に訂正）。
  **`core/kifu` に KIF → USI/SFEN の変換が既に入っている**
  （`StartSFEN` / `DecodeMoves`。**KIF は移動元座標を持つので盤が要らない**）。
  逆方向（USI → 日本語表記）も `NewNotation` / `FormatMoves` にある。
  実際 `ikkyoku` はこれだけで棋譜を読んで盤に出している。
  ⚠️ **engine が要るのは KI2**（移動元を書かないので合法手生成で曖昧性を解く）。
  そちらの変換は `kicho/format` 側に置くこと。
- 消費時間（`spend` / `remainingtime_p1`）は保存していない。KIF の消費時間欄も空。
- 認証なし。LAN 公開は自己責任。
