# AGENTS.md

kicho（棋帳）。棋譜を取得して保存し、外部ツールへ HTTP で配信する **Go のライブラリ**と、
それを使う **Wails3 デスクトップアプリ**（`_cmd/kicho`）。
リポジトリ全体の方針はルート（shinte）の `AGENTS.md` を参照。

> 旧称は `kifu`、旧実装は Node.js + express。「棋譜 / kifu」は `core/web/kifu.js`・KIF 形式・
> URL パスなど各所で使う語で紛らわしいため `kicho` に改名した。
> **`core/web/kifu.js` と `/ryuoh/kifu/{id}`（棋譜の URL）は改名対象ではない。**

**このファイルには、どのコードを読んでも見えないことだけを置く。**
関数ごとの約束はそのコードのコメントに、パッケージの制約はそのパッケージの `AGENTS.md` にある。

| 置き場所 | 中身 | 読むとき |
|---|---|---|
| スキル `kicho-change-api`（`_docs/skills/`） | 公開 API を変える手順（ikkyoku の追従・`check-consumers.ps1`・スキーマの版上げ） | 公開 API を変えるとき |
| スキル `kicho-add-source`（`_docs/skills/`） | ライブ取得元を足す手順（触る箇所の一覧） | 取得元を足すとき |
| `_docs/sources/*.md` | 取得元の調査記録（実データでの確認・未使用のフィールド・動作確認に使える ID） | 掘るときだけ |
| `TODO.md` | 未対応・今後 | 機能を足すとき |

⚠️ **制約（「⚠️ …しないこと」）を `_docs/` へ出さないこと。** 触ったときに読まれる場所
（コードのコメントかパッケージの `AGENTS.md`）に置く。

## ライブラリとして使われる

kicho は**棋譜の取得と棋譜データベースのライブラリ**で、アプリはそれを使う側の 1 つ。
`_cmd/kicho` もライブラリを使う側であって、ロジックを持たない。
他のアプリからの利用がある（今は `ikkyoku`。棋譜の取得・登録・蔵書の画面をあちらに持つ）。

- ⚠️ **公開 API は `Library`（取得だけなら `Fetcher`）に集める。** 蔵書の操作も取得元の知識も
  ここに足し、使う側に書かない。**`Store()` は公開しない**
  （保存の組み立てが使う側ごとに散り、片方だけ直せば黙って挙動が割れる）
- ⚠️ **公開 API を変えたら使う側が壊れていないか確かめる。** `go build ./...` を kicho で
  通しても使う側はコンパイルされないので、**`.\check-consumers.ps1` を流す**
- ⚠️ **同じ DB を複数のプロセスが開きうる**（使う側が DB のパスを選べるため）。
  共用に耐えるようにはしてある（`store/AGENTS.md`）
- HTTP 配信（外部ツールへ配る口）は `_cmd/kicho` のアプリが持つ

## 構成

Wails 依存を `_cmd/kicho/`（ネストモジュール `kicho-app`）に閉じ込め、
ロジックは親モジュール `github.com/ShinteLab/kicho` の通常パッケージに置く。
ロジック側に `github.com/wailsapp/wails/v3` を import しないこと。

`core` はタグ未発行のため `replace github.com/ShinteLab/core => ../core` で引いている。
`_cmd/kicho` は `core` と `kicho` の両方を replace で参照する。

| パッケージ | 役割 |
|---|---|
| `kicho`（root） | 取得の `Fetcher`、取得→保存・蔵書・仮の一覧を束ねる `Library` |
| `scrape/` | 中継サイトからの取得（読売・連盟・将棋DB2）、文字コード判別 |
| `store/` | SQLite への永続化 |
| `httpapi/` | 棋譜配信 HTTP サーバ |
| `format/` | 形式変換の登録口 |
| `settings/` | 設定の永続化と保存先パスの解決 |
| `_cmd/kicho/` | Wails3 アプリ |

保存先は `os.UserConfigDir()/kicho/`（`kicho.db` と `settings.json`）。

## コマンド

```powershell
go vet ./...
go test ./...                     # _cmd 配下は対象外（アンダースコア始まりは ./... に入らない）
.\check-consumers.ps1             # 使う側（ikkyoku）が壊れていないか。公開 API を変えたら必ず
.\check-consumers.ps1 -Test       # go test まで
```

Wails アプリのビルドは `_cmd/kicho/AGENTS.md`。

## 棋譜の入り口は5つ

| 入り口 | source | source_id | 取り直すと | source_url |
|---|---|---|---|---|
| 読売（竜王戦）の中継 | `yomiuri` | 読売の棋譜 ID | **同じ棋譜を更新** | 棋譜ビューアの URL |
| 日本将棋連盟の中継 | `shogilive` | 中継のパス（拡張子なし） | 同上 | 中継ページ（HTML）の URL |
| 将棋DB2 | `shogidb2` | 対局ページ `/games/{id}` の id | 同上 | 対局ページの URL |
| URL から取得 | `url` | 正規化した URL | 同上 | 指定された URL |
| KIF を貼り付け | `paste` | 毎回新しい UUID | **登録のたびに別の棋譜** | 空 |

`(source, source_id)` が保存（upsert）・仮の一覧・画面のカードに共通の鍵。
入力からどの取得元かを判別するのは `Fetcher.Fetch` だけ。
**URL からの取得はスクレイピングではない**（中身をそのまま KIF として読む）。

## 2系統の役割分担（重要）

**ライブ取得とアーカイブは別物として扱う。統合しないこと。**

- **対局中** … 棋譜は随時更新されるので、その都度サイトへ取りに行く。
  HTTP のライブ経路（保存しない）と UI の「取得」「更新」
- **終局後** … 以後サイトへアクセスしなくて済むように DB へ保存する。
  HTTP の `GET /kifu/{id}` と UI の「保存」

そのため**取得と保存は別操作**（「取得して保存」の一括操作は置かない）。
保存は画面の内容をそのまま書き込み、サイトへ取り直しには行かない。
対局中でも保存はでき、終局後に取り直して保存すれば**同じ ID のまま最新化**される。

**2日制の対局を追う**ために、取得した中継を仮の一覧（`watches`）に残して再起動後も並べ直す。
残すのは「どのサイトのどの棋譜か」であって棋譜ではない（棋譜を残すのは保存の役目）。

ライブ取得元は取り方が違っても、カードに積む・更新する・保存する・ライブ URL を配る、の
扱いを同じにしてある。取得元による分岐は kicho 側と `ServerService.SourceURLs` にだけ置き、
UI は取得元を意識しない。

## 原本を保持する（重要）

**棋譜本文は登録された形式の原本をそのまま保存する。整形し直さない。**
整形すると変化（`変化：N手`）・コメント（`*`）・`不成` などの情報が落ちるため。

- 解析（`core/kifu.Parse`）は**一覧と検索に使うメタデータの抽出だけ**に用いる
- 本文は UTF-8 に寄せて保存し、元の形式と文字コードを記録する
- 手を DB に正規化して持つ設計は採らない。形式固有の情報が落ちるうえ、
  形式ごとに持つと件数 × 形式で膨らむ。変換は決定的なので都度計算で足りる
- 読売・将棋DB2 はサイトが KIF を配っていないので、**kicho が構造化データから
  組み立てた KIF が原本**（`scrape/AGENTS.md`）

## core との分担

**KIF 形式の仕様は `core/kifu`（Go）に置く。kicho 側に書かない。**
JS 実装の `core/web/kifu.js` と挙動を揃えること（指し手行の書式 `FormatLine` / `TerminalMarker`）。
テストは `go test ./core/kifu` と `node core/web/test.mjs`。
KIF ⇄ USI/SFEN、CSA → USI の変換も `core/kifu` にある。
