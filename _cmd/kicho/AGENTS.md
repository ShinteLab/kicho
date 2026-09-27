# _cmd/kicho/AGENTS.md

Wails3 アプリ（独立したネストモジュール `kicho-app`）。全体像はルートの `AGENTS.md`。

**Wails 依存はここだけ**（`main.go` / `kifuservice.go` / `serverservice.go`）。
ロジックは親モジュール `github.com/ShinteLab/kicho` 側に置き、ここに書かない。

⚠️ **このアプリはライブラリを使う側の 1 つ。** 取得・保存の挙動をここにだけ足すと、
同じライブラリを使う他のアプリと割れる。挙動は `kicho.Library` に足す。

## ビルド

```powershell
cd _cmd/kicho
npm --prefix frontend install          # 初回のみ
wails3 generate bindings -ts -i -d frontend/bindings
npm --prefix frontend run build
go build -o bin/kicho.exe .            # または wails3 build / wails3 dev
```

- **Go のサービスを変えたら必ず `wails3 generate bindings`** を実行する。
  忘れるとフロントが無言で古い型を使う
- bindings はインターフェース生成（`-i`）。フロントは `import type` で受ける。
  クラス生成に切り替えるなら Taskfile 側のフラグも合わせること
  （揃っていないと、`wails3 dev` / `wails3 build` が手で作ったのと違う形の bindings を作る）
- `frontend/dist` が無いと `//go:embed all:frontend/dist` が失敗して Go のビルドが通らない。
  先にフロントをビルドする

### Taskfile からモバイルを外してある

`Taskfile.yml` の `includes` で **ios / android をコメントアウト**している。
`build/android/Taskfile.yml` は**読み込み時に評価される `vars:`** で
`uname -m` や `... | tail -1` を呼び、Windows では
`"uname": executable file not found in $PATH` が出る。動作自体は壊れないが
（変数が空になるだけ）、デスクトップ専用なので読み込まない。prokishi 側も同じ。

⚠️ `wails3 update build-assets` は `Taskfile.yml` を再生成するので、
実行後は includes を戻し忘れないこと。

## サービス

- **`KifuService` は DTO の変換と入力チェックだけ。** 取得元ごとの `source_url` の
  決め方・文字コードの既定・手数の数え方を書かないこと（`kicho.Library` / `kicho.Fetched` にある）
- ⚠️ **`context.Background()` をそのまま DB へ渡さないこと。** store は接続を 1 本に
  絞っているので、止まらないクエリが1つあると以後の操作が全部待たされる
  （`dbContext` / `netContext`）
- `ServerService.SourceURLs` が取得元ごとのライブ経路（取得 URL）を出し分ける
- **サーバ停止中でも URL は返す**（設定値から組み立てる。貼り付け先で使う頃に
  起動していればよいため）。LAN 公開時は localhost 版を先頭に、到達可能な LAN アドレス版も
  返す。コピーされるのは先頭。bind の `0.0.0.0` 自体は貼っても届かないので出さない

## 取得タブ（`frontend/src/App.tsx`）

UI は **取得と保存を別操作**にしてある（「取得して保存」の一括操作は置かない）。
取得 → 内容を確認 → **その表示内容をそのまま保存**する流れで、保存時にサイトへ
取り直しには行かない（`KifuService.Save` は画面の `GameDetail` を受け取る）。

### 取得結果はカードとして積む

**取得は1件しか持たない設計にしない。** 同じ日に2局以上進むことがあるため、
取得のたびに `FetchCard` を1枚追加する（新しいものが先頭）。
カードごとに「保存」「削除」を持ち、削除は**画面からカードを外すだけ**で
保存済みの棋譜（DB）は消さない。「クリア」は入力とカードを全部捨てる。

**同じ棋譜を取り直したときはカードを増やさない。** `FetchCard.key` が一致するカードは
位置と `saved` を保ったまま中身だけ差し替える。入力欄からの「取得」もカードの「更新」も、
この `mergeCard` を通す。`key` は **`{source}:{sourceId}`**（`cardKey`）。
棋譜 ID の形が取得元ごとに違うので取得元も含める（保存側の `(source, source_id)` と同じ粒度）。

### カードの「更新」

保存は画面の内容をそのまま書き込むので、更新の手段が無いと、対局中に取ったカードを
後で保存しても**取得した時点の古い棋譜**が入る。そのためカードごとに「更新」を置き、
`KifuService.Refresh(source, sourceId)` で取り直して中身を差し替える。

- 入力欄からの `Fetch(input)` と分けてあるのは、**取得元がすでに分かっている**ため。
  入力の判別も URL から棋譜 ID を引き直す往復も挟まらない
- 手数の変化を案内に出す（`refreshNotice`）
- **保存済みのカードで手数が変わったら「保存し直す」よう促す。** 黙っていると
  更新＝保存だと誤解される

### 仮の一覧（`watches`）から復元する

2日制の対局では翌日また中継の URL を貼り直すことになるので、カードを DB に残して
再起動後に並べ直す（`Watches` / `Watch` / `Unwatch` / `UnwatchAll`）。

- **復元ではサイトへ取りに行かない。** 起動のたびに追跡ぶんの通信が走らないよう、
  メタだけでカードを並べるところで止める。復元したカードは本文が空なので
  保存ボタンを押せなくしてあり（`isRestored`）、「更新」または「すべて更新」で取り直す
- カードの「削除」は `Unwatch`（外さないと再起動で戻ってくる）、「クリア」は `UnwatchAll`。
  終局済みの保存では `Library.Save` が自動で外す。**どれも保存済みの棋譜には触らない**

### 状態はタブを跨いで保持する

タブは条件レンダリングで切り替えるため、取得タブの状態（入力・カードの配列）は
`App` 側に持ち上げてある（`FetchState`）。入力欄だけは永続化しない。
保存後も入力とカードは残す（URL をコピーしたり、終局後に取り直して保存し直したりするため）。

### 表示

- 手数の横に、終局済みなら「（終局）」、手数 0 なら「（対局前）」を出す
- 取得元のラベルは `SOURCE_LABELS`（ikkyoku 側にも同じものがある）
- **棋譜 URL**（`/kifu/{id}`。保存済み）と**取得 URL**（ライブ経路。開くたびにサイトから
  取り直す。対局中向け）をそれぞれコピーできる。取得 URL が出るのは
  `SOURCE_LABELS` にある中継サイトだけ（`isLiveSource`）
