# AGENTS.md

kicho（棋帳）。棋譜を取得して保存し、外部ツールへ HTTP で配信する **Wails3 デスクトップアプリ**。
リポジトリ全体の方針はルート（shinte）の `AGENTS.md` を参照。

> 旧称は `kifu`、旧実装は Node.js + express。「棋譜 / kifu」は共有モジュール
> `core/web/kifu.js`・KIF 形式・URL パスなど各所で使う語で紛らわしいため、
> プロジェクト名を `kicho` に改名し、実装を Go + Wails3 に移した。
> **`core/web/kifu.js`（KIF 形式の変換ロジック）と `/ryuoh/kifu/{id}`（棋譜の URL）は
> 改名対象ではない**ので、そのまま `kifu` を使う。

## この AGENTS.md の外に置いてあるもの

**このファイルは概要と、ルートパッケージ（`kicho`）の約束だけを持つ。**

| | 何を置くか | 読むとき |
|---|---|---|
| **AGENTS.md（このファイル）** | 概要・構成・`Library` の約束・棋譜の入り口 | いつも |
| **パッケージの AGENTS.md** | **「⚠️ …しないこと」** —— 破ると壊れる制約 | そのディレクトリを触るとき |
| **スキル**（`_docs/skills/`） | **手順** | その作業を始めるとき |
| **`_docs/`** | **記録**（調査・実データでの確認） | 掘るときだけ |
| **`TODO.md`** | **未対応・今後**（やらないと決めたこと・決めた考え方） | 機能を足すとき |

⚠️ **制約をパッケージの外（`_docs/`）へ出さないこと。** 触ったときに読まれる場所に置く。

| 置き場所 | 中身 |
|---|---|
| `store/AGENTS.md` | DSN と PRAGMA、スキーマ、マイグレーション、検索（FTS5 trigram） |
| `scrape/AGENTS.md` | 取得元ごとの制約と壊れやすい点（読売・連盟・将棋DB2）、文字コード、HTML から `.kif` を辿る |
| `httpapi/AGENTS.md` | エンドポイント、URL の組み立て、bind と LAN 公開 |
| `format/AGENTS.md` | 形式変換（未実装。KI2 は engine が要る） |
| `_cmd/kicho/AGENTS.md` | Wails のビルドと bindings、Taskfile、取得タブ（カード・更新・仮の一覧の復元） |
| スキル `kicho-change-api` | **公開 API を変える**（ikkyoku の追従・`check-consumers.ps1`・スキーマの版上げ） |
| スキル `kicho-add-source` | **ライブ取得元を足す**（触る箇所の一覧） |
| `_docs/sources/*.md` | 取得元の調査記録（実データでの確認・未使用のフィールド・動作確認に使える ID） |

スキルは**ただの Markdown**（`_docs/skills/<名前>/SKILL.md`）。Claude Code で使うなら
手元の `.claude/skills/` からジャンクションを張る（`_docs/skills/README.md`）。

## `ikkyoku` がライブラリとして使っている

**`ikkyoku`（一局。ユーザ環境で動く将棋ソフト）が kicho を直接 import し、
棋譜の取得・登録・蔵書の UI をあちらへ移した。** ユーザが 2 つを立ち上げずに済むようにするため。
**棋譜データベースとしての実装はこちら**で、ikkyoku は利用する側。
**このリポジトリの UI は将来「テスト用のモック」または
「ikkyoku 以外の将棋ソフトからの読み込み口」になる。**

- ⚠️ **公開 API を変えるときは ikkyoku 側も直すこと。** `go build ./...` を kicho で
  通しても ikkyoku はコンパイルされないので、**`.\check-consumers.ps1` を流す**
  （スキル `kicho-change-api`）
- ⚠️ **DB は別の場所が既定。** ikkyoku は `%APPDATA%\ikkyoku\kicho.db`、
  kicho は `%APPDATA%\kicho\kicho.db`。ikkyoku の設定にはファイルピッカーがあるので
  **ユーザは実際に共用できてしまう**。WAL + `busy_timeout` で共用に耐えるようにしてあるが、
  **共用を推奨する状態ではない**
- ⚠️ **`ServerService`（HTTP 配信）は移していない。** ikkyoku はサーバを持たない。
  **外部ツール（ShogiHome 等）へ配るのは今のところこちらの役目**

## 構成

Wails 依存を `_cmd/kicho/` に閉じ込め、ロジックは親モジュール `github.com/ShinteLab/kicho` 側の
通常パッケージに置く（wails3 skill のアーキテクチャ方針）。
`internal/` 相当のロジックに `github.com/wailsapp/wails/v3` を import しないこと。

`core` はタグ未発行のため `replace github.com/ShinteLab/core => ../core` の相対パス参照で引いている。
Wails アプリ側（`_cmd/kicho/`, module `kicho-app`）は `core` と `kicho` の両方を replace で参照する。

| パッケージ | 役割 |
|---|---|
| `kicho`（root） | ユースケース。取得の `Fetcher`、取得→保存・蔵書を束ねる `Library` |
| `scrape/` | 中継サイトからの取得。読売のペイロード評価、連盟の `.kif` 取得、将棋DB2 の LiveView、文字コード判別 |
| `store/` | SQLite への永続化 |
| `httpapi/` | 棋譜配信 HTTP サーバ（起動/停止・bind 設定） |
| `format/` | 形式変換の登録口 |
| `settings/` | 設定の永続化と保存先パスの解決 |
| `_cmd/kicho/` | **Wails3 アプリ**（独立したネストモジュール `kicho-app`） |

```
_cmd/kicho (Wails)  →  github.com/ShinteLab/kicho  →  scrape / store / httpapi / format / settings
```

## コマンド

```powershell
go vet ./...
go test ./...                     # _cmd 配下は対象外（アンダースコア始まりは ./... に入らない）
.\check-consumers.ps1             # 使う側（ikkyoku）が壊れていないか。公開 API を変えたら必ず
.\check-consumers.ps1 -Test       # go test まで
```

Wails アプリのビルドは `_cmd/kicho/AGENTS.md`。

## 保存先

`os.UserConfigDir()/kicho/` に置く（Windows なら `%APPDATA%\kicho\`）。

- `kicho.db` — 棋譜 DB（SQLite。`modernc.org/sqlite`）。WAL なので `-wal` / `-shm` が並ぶ
- `settings.json` — bind アドレス・ポート・自動起動。**UTF-8 BOM を許容**している
  （Windows のメモ帳が既定で BOM を付けるため）

## 公開 API は `Library` に集める（重要）

**蔵書の操作も取得元の知識も `Library`（取得だけなら `Fetcher`）に足すこと。使う側に書かない。**
`Library.Store()` は公開していない —— `*store.Store` を返すと実質の公開 API が
`store` パッケージ全体になり、保存の組み立て（取得元ごとの `source_url`、文字コードの既定、
手数の数え方）が kicho の Wails サービスと ikkyoku に散って、片方だけ直せば黙って挙動が割れる。

| 口 | 用途 |
|---|---|
| `NewFetcher()` | 取得だけの口（**DB 不要**）。`Library` はこれを埋め込んでいる |
| `Fetch(ctx, input)` | 取得。**取得元の判別もここ**（下の「振り分け」） |
| `Refresh(ctx, source, sourceID)` | 取得済みを取り直す。取得元が分かっているので往復が無い |
| `PreviewKIF(text)` / `PreviewURL(ctx, url)` | 取り込み前の確認（保存しない） |
| `ImportKIF(ctx, text)` / `ImportURL(ctx, url)` | 取り込んで保存（貼り付けは毎回新規、URL は同じ URL なら更新） |
| `Save(ctx, Fetched)` | 取得済みを保存。**`source_url` と手数はここが決め直す** |
| `Count` / `Search` / `Get` / `Delete` | 蔵書 |
| `Watches` / `Watch(ctx, Fetched)` / `Unwatch` / `UnwatchAll` | **仮の一覧**（追跡中の中継） |
| `RefetchableURL(source, url)` | その `source_url` を `Fetch` に渡せば同じ棋譜が取れるか。**取れないのは貼り付けだけ**（読売もビューアの URL を `Fetch` が ID に解決する） |

`Fetched` が取得結果の共通形で、**取得元が違っても同じ形**になる。
フロントへ渡す DTO はこれを写すだけにする。

### 取得は DB を要らない（`Fetcher`）

⚠️ **取得を `Library` にだけ置かないこと。** 取得は `store` を 1 度も触らないので、
`Library` のメソッドにすると `kicho.Open`（＝DB を開く）を通らないと呼べなくなる。
**ikkyoku は「棚が開けていなくても URL の棋譜は解析できる」という約束を持っている**ので、
DB が開けないことと取得できないことを繋げてはいけない。

### `Save` は画面の値を全部は信じない

`Fetched` は UI を往復してくるので、**`source_url` は取得元から決め直し
（`resolveSource`）、手数は本文から数え直す**（`countMoves`。どちらも派生値）。
棋戦名・対局者・終局種別は画面の値を使う —— 読売はペイロードが一次情報で、
自分が組み立てた KIF を読み直すより確かなため。

**保存した棋譜が終局済みなら仮の一覧からも外す**（`Unwatch`）。
⚠️ **対局中の保存では外さないこと。** 2日制なら1日目の封じ手時点で保存しても
翌日また同じカードで追う。この分岐を UI 側に置くと kicho と ikkyoku で割れるので `Save` に持たせる。

### 仮の一覧（`watches`）は「サイト」を覚えるもの

2日制の対局で翌日また中継の URL を貼り直さずに済むよう、取得したカードを DB に残す。
⚠️ **残すのは「どのサイトのどの棋譜か」であって棋譜ではない**（KIF 本文は持たない。
メタは表示用の写し）。棋譜そのものを残すのは `Save`（蔵書）の役目で、役割を混ぜないこと。

⚠️ **載せられるのは取り直せるものだけ**（`Watch` が `ErrUnsupportedSource` で弾く。
**弾くのは `paste` だけ**）。復元しても「更新」が必ず失敗するカードを作らないための蓋で、
判定は **`Refresh` が受け付ける取得元と揃えておく**。

### 一覧は件数無制限にしない

`Library.Search` は `MaxSearchRows`（500）で切り、`SearchResult` で
「該当 N 件・全体 M 件・切ったかどうか」を返す。**棋譜を溜め込んでいく前提**なので、
上限が無いと蔵書が増えたぶんだけ全行が JSON に載る（`store` 側の約束は `store/AGENTS.md`）。

### エラーは sentinel で見分けられるようにする

`ErrNoInput` / `ErrEmptyKifu` / `ErrNotKifu` / `ErrUnsupportedSource`（`errors.go`）と
`store.ErrNotFound` / `store.ErrSchemaTooNew`。**画面と文言は使う側にある**ので、
種類で分岐できないと `strings.Contains` になる。判定は `errors.Is`。

⚠️ **「手数 0」は `ErrNotKifu` ではない。** 中継は対局開始前からヘッダだけの棋譜を置いている。
「そもそも KIF ではない」の判定は手数ではなく `kifu.Document.Empty()`（ヘッダも指し手も
取れなかった）で行う。UI は手数 0 のとき「（対局前）」を出す。

## 棋譜の入り口は5つ

| 入り口 | source | source_id | 重複 | source_url |
|---|---|---|---|---|
| 読売（竜王戦）の中継 | `yomiuri` | 読売の棋譜 ID | 取り直すと**同じ棋譜を更新** | 棋譜ビューアの URL |
| 日本将棋連盟の中継 | `shogilive` | 中継のパス（拡張子なし） | 同上 | 中継ページ（HTML）の URL |
| 将棋DB2 | `shogidb2` | 対局ページ `/games/{id}` の id | 同上 | 対局ページの URL |
| URL から取得 | `url` | **正規化した URL**（`sourceIDForURL`） | 同上 | 指定された URL |
| KIF を貼り付け | `paste` | 毎回新しい UUID | **登録のたびに別の棋譜** | 空 |

- **貼り付けだけが「毎回新規」**（取得元が無いので鍵を作れない）。URL は URL 自体を
  `source_id` にして `(source, source_id)` の upsert に乗せる。
  ⚠️ **ハッシュにしないこと** —— そのままの URL なら `Refresh` がそこへ取りに行けるし、
  DB を覗いて何の棋譜か読める
- `source_url` は**人が開いて確認できるほう**を残す（連盟は `.kif` ではなく中継ページ）
- **URL からの取得はスクレイピングではない。** 中身をそのまま KIF として読む
  （他サイトの `.kif` でも、別の kicho の `/kifu/{id}` でも取り込める）。
  HTML が返ってきたらそこに書かれた `.kif` を辿る（`scrape/AGENTS.md`）

### 振り分けは `Fetch` だけが知っている

```
live.shogi.or.jp の URL  → 連盟の中継
shogidb2.com の URL      → 将棋DB2（⚠️ 汎用の URL より先に振り分ける）
yomiuri.co.jp の URL     → 読売（竜王戦）
それ以外の http(s) URL   → その中身を .kif として読む（HTML なら辿る）
URL でない文字列         → 読売の棋譜 ID
```

⚠️ **「それ以外の URL」を読売として解決しないこと。** 他サイトの `.kif` の URL が
「読売の棋譜 ID」扱いになり、意味の分からないエラーで落ちる。
⚠️ **取得元の知識は kicho の側に置く**（使う側に「どのサイトならどちら」を書かせない）。

## 2系統の役割分担（重要）

**ライブ取得とアーカイブは別物として扱う。統合しないこと。**

- **対局中** … 棋譜は随時更新されるので、その都度サイトへ取りに行く。
  これが `GET /ryuoh/kifu/{id}` `GET /shogilive/kifu/{id}` `GET /shogidb2/kifu/{id}`
  （保存しない）と UI の「取得」
- **終局後** … 以後サイトへアクセスしなくて済むように DB へ保存する。
  これが `GET /kifu/{id}` と UI の「保存」

そのため**取得と保存は別操作**（「取得して保存」の一括操作は置かない）。
保存は画面の内容をそのまま書き込み、サイトへ取り直しには行かない。
対局中でも保存はでき、`(source, source_id)` の upsert なので、対局中に保存 →
終局後に取り直して保存、で**同じ ID のまま最新化**される（取り直しはカードの「更新」）。

ライブ取得元は3つ（読売＝ペイロードの評価、連盟＝`.kif` の直取得、将棋DB2＝LiveView）。
取り方は違うが、カードに積む・更新する・保存する・ライブ URL を配る、の扱いは同じにしてある。
`store.Source*` での分岐は kicho 側（`Fetcher.Refresh` / `Library.resolveSource`）と
`ServerService.SourceURLs` にだけあり、UI は取得元を意識しない。

## 原本を保持する（重要）

**棋譜本文は登録された形式の原本をそのまま保存する。整形し直さない。**
整形すると変化（`変化：N手`）・コメント（`*`）・`不成` などの情報が落ちるため。

解析（`core/kifu.Parse`）は**一覧と検索に使うメタデータの抽出だけ**に用いる。
`game_kifu` に `format`（kif/ki2/…）と `encoding`（元の文字コード）を持つ。
本文は UTF-8 に寄せて保存する。

手を DB に正規化して持つ設計は採らない。形式固有の情報が落ちるうえ、
形式ごとに持つと件数 × 形式で膨らむ。変換は決定的なので都度計算で足りる（`format/AGENTS.md`）。

読売・将棋DB2 はサイトが KIF を配っていないので、**kicho が構造化データから組み立てた KIF が原本**。
入力ミスの補正（読売の `打打`）はその組み立て時に行う（`scrape/AGENTS.md`）。

## core との分担

**KIF 形式の仕様は `core/kifu`（Go）に置く。kicho 側に書かない。**

- `core/kifu` … 指し手行の書式・終局判定・ヘッダ付き KIF ドキュメントの組み立て、
  KIF ⇄ USI/SFEN（`StartSFEN` / `DecodeMoves` / `NewNotation` / `FormatMoves`）、CSA → USI（`DecodeCSA`）
- `core/web/kifu.js` … 同じ仕様の JS 実装（ブラウザの棋譜ビューア向け）

指し手行の書式（`FormatLine` / `TerminalMarker`）は Go と JS で挙動を揃える。
片方だけ直さないこと。テストは `go test ./core/kifu` と `node core/web/test.mjs`。

