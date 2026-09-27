# scrape/AGENTS.md

中継サイトからの取得。全体像はルートの `AGENTS.md`。
取得元ごとの調査の記録（実データでの確認・未使用のフィールド・動作確認に使える ID）は
`_docs/sources/*.md`。取得元を足すときはスキル `kicho-add-source`（`_docs/skills/`）。

**取得元は外部サイトの構造に強く依存する。取得が壊れたら、まず各節の「壊れやすい点」を疑う。**

| 取得元 | ファイル | 性質 |
|---|---|---|
| 読売（竜王戦） | `yomiuri.go` / `payload.go` | ペイロード（JS）を評価し、**構造化データから KIF を組み立てる** |
| 日本将棋連盟の中継 | `shogilive.go` | **配信されている `.kif` が原本**。スクレイピングではない |
| 将棋DB2 | `shogidb2.go` | LiveView の `push_event` から構造化データを取り、KIF を組み立てる |
| 共通 | `fetch.go` | 文字コード判別、HTML から `.kif` を辿る |

⚠️ **取得元の判別（`IsShogiLiveURL` / `IsShogiDB2URL` / `IsYomiuriURL`）はここに置き、
振り分けは `kicho.Fetcher.Fetch` だけが行う。** 使う側に「どのサイトならどちら」を書かせない。

## 共通（`fetch.go`）

### 文字コード

世に出回る `.kif` は **Shift_JIS が多い**（Kifu for Windows 等の既定）。
`DecodeKIF` が UTF-8 として妥当ならそのまま、そうでなければ Shift_JIS として読む。
**本文は UTF-8 に寄せて保存**し、`encoding` には元が何だったかを記録する（主にデバッグ用）。
BOM は `core/kifu` の Parse 側で落とす。

### 棋譜中継ページ（HTML）から `.kif` を辿る

`.kif` の URL を探して貼り替える手間をなくすため、HTML を渡されたら
そこに書かれた `.kif` を辿る（`KifURLFromHTML`）。
**ページから対局内容を読み取るわけではない**ので、これはスクレイピングではない。
取り込むのはあくまで `.kif` の原本。

- 連盟の中継ページは JS の定数 `const KIF_FILE_NAME = "/oui/kifu/67/oui202607290101.kif";` を持つ。
  中継ビューア（`/common/js/kj/kj.js`）がこれを見て棋譜を読む。**この定数が一次情報**で、
  拡張子の付け替え（`.html` → `.kif`）には依存していない
- 見つからなければ `.kif` で終わる `<a href>` を探す（他サイト向けの保険）。
  どちらも無ければエラー
- 相対パスは取得元 URL を基準に解決する

HTML かどうかは Content-Type ではなく中身で判定する（KIF は `<` で始まらない → `LooksLikeHTML`）。
`kicho.Fetcher.FetchKIFFromURL` が **HTML が返ってきたときだけ**この解決を挟む。

### 指し手が無い棋譜（対局前）はエラーにしない

中継は**対局開始前から棋譜を置いている**。ヘッダだけで指し手がまだ 1 手も無い `.kif`
が返り、読売のペイロードも対局前は `data.kifu.kifu` が空配列で来る。
**「1 手も無い」は取得の失敗ではない。** ペイロード解析もエラーにしないこと。

### 終局判定の出どころ

取得元によって違うが、`Finished`（`EndMark` が空でない）として渡すところは同じ。

- 読売 … ペイロードの `end_mark`（終局していれば "投了" 等、対局中は空）
- 連盟・URL … `.kif` の最終手が終局手かどうか（`kifu.Document.EndMark()`）
- 将棋DB2 … `moves[].csa` の最後の終局コード（`%TORYO` 等。`kifu.DecodeCSA` が終局名を返す）

## 読売（竜王戦）

### ペイロードは JS 評価が必須

`https://www.yomiuri.co.jp/kifu/s/{id}/_payload.js` は Nuxt のペイロードで、
`export default (function(a,b,...){...}(...))` の IIFE。値が引数リストの変数として
共有（重複排除）されているため、**文字列パースでは解けない**（`handicap:a` のような参照が残る）。

そのため **goja（PureGo の JS エンジン）で評価**している（`payload.go`）。
`export default ` を剥がして式として `RunString` する。

- 第三者サイトのコードを実行するので、**`vm.Interrupt` による 10 秒のタイムアウトは外さないこと**。
- goja は null/undefined へのプロパティアクセスで panic するため `recover` でエラーに落としている。

### side は player1 の手番

`data.kifu.side` は **player1 から見た手番**。取り違えると保存する棋譜すべてで
先後が反転する。実データでの確認は `_docs/sources/yomiuri.md`。
`payload_test.go` で両方向を固定している。

### 終局手は "投了打" で来る

ペイロードの最終要素は `move:"投了打"`（`end_mark:"投了"`）。そのまま出すと
`104 投了打(00)` という打ち手のような行になり外部ツールが壊れるため、
`core/kifu` の `TerminalMarker` で終局と判定し `104 投了` として出力する。

### 記録係の入力ミスは取り込み時に潰す

ペイロードの `move` は記録係が入力した文字列で、**校正されていない**。打ちの手に "打" が
二重に付いた `move:"７五桂打打"` が実在する（`_docs/sources/yomiuri.md`）。
そのまま出すと `91 ７五桂打打(00)` になり、外部ツールが駒種を読めずそこで止まる。
`打打` は KIF として一意に無効な表記なので、`normalizeMoveName` で潰している。

**直すのは取り込み時（`parsePayload`）。DB 側に手編集を足すのでは解決しない。**
ライブ経路（`/ryuoh/kifu/{id}`）は DB を通らないので、対局中はそちらが直らないため。
読売は KIF を配信しておらず構造化データから KIF を組み立てるのは kicho 側なので、
ここが原本の生成地点にあたる（「原本を保持する」方針とは矛盾しない）。

同種の表記ゆれが出たらここに足す。

### 対局ページ → 棋譜 ID

対局ページの `iframe.p-shougi-iframe` の `src`（`.../kifu/s/{id}/`）から ID を取る。
`golang.org/x/net/html` で解析している（クラス名は部分一致で誤爆しないよう厳密比較）。
棋譜ビューアの URL（`.../kifu/s/{id}/`）が直接貼られたら `KifuIDFromViewerURL` で取る。

### 壊れやすい点

- iframe のクラス名（`p-shougi-iframe`）
- ペイロードのプロパティパス（`data.kifu.*`）

## 日本将棋連盟の中継

`http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html`

**スクレイピングではない。** 連盟は中継ページの隣に `.kif` をそのまま置いているので、
取りに行くのはその `.kif`。HTML を読むのは「どの `.kif` か」を決めるときだけ（`KIF_FILE_NAME`）。

### 棋譜 ID は中継のパス

`source_id` は拡張子を落としたパス（`oui/kifu/67/oui202607290101`）。

サイトは **S3 バケットをそのまま公開しているだけ**で、一覧も検索も無く
（`/` は `NoSuchKey` の 404）、パス以外に棋譜を指す ID が存在しない。
ID からは `.kif` / `.html` の URL を組み立て直せる必要がある（カードの「更新」と
ライブ経路で使う）ので、パスをそのまま持つ。

そのため **`source_id` にスラッシュが入る**（HTTP のルートが `{id...}` なのはこのため）。

### http のみ / Shift_JIS

- **https では接続できない**（TLS の待ち受けが無い）。`ShogiLiveBaseURL` を
  `https://` に変えないこと
- 配信は **Shift_JIS**。`DecodeKIF` で UTF-8 に寄せ、`encoding` に元を記録する

### 終局判定と手数

- 終局すると `101 投了` の行と `まで100手で後手の勝ち` が付く。
  `core/kifu.Parse` は `まで` 行を読み飛ばし、最終手が終局手なら `EndMark()` が返る
- `.kif` は `# --- Kifu for Windows ...` に始まり、指し手の間に `*` の観戦記コメントが
  大量に入る。**手数は行数ではなく `core/kifu.Parse` の結果で数える**
  （`kicho.countMoves`）。行を数えるとコメントまで手数に入る

## 将棋DB2

`https://shogidb2.com/games/3186d1e7f8f33242990b1e853f53250d443dbe25132936b005068009a201`

**スクレイピングだが、HTML は読まない。LiveView の `push_event` を取っている。**
取り方（WebSocket のメッセージ）と data → KIF の対応表は `_docs/sources/shogidb2.md`。

- ⚠️ **サイトの KIF（`#kifu-modal textarea`）は使わない。** 1 手ごとの消費時間を
  持っていないのに `( 0:00/00:00:00)` で埋めた作り物なので原本扱いできない。
  data を取り、**読売と同じく構造化データから KIF を組み立てる**（`ShowTime:false`）
- ⚠️ **サイトの `label`（`"▲26歩(27)"`）は使わない** —— 算用数字で KIF の表記ではない。
  指し手は `moves[].csa` を `kifu.DecodeCSA` → `kifu.FormatMoves` で組み立てる
- ⚠️ **CSA は移動後の駒名で成りを書く**ので、成った手か成駒が動いた手かは盤を見ないと
  分からない（`DecodeCSA` が開始局面から進めて判定する。`core/kifu/csa.go`）
- ⚠️ `start_at` は **UTC で来る**ので JST に直す
- WebSocket は**既に依存している `golang.org/x/net/websocket`**（go.mod を増やさないため）。
  ⚠️ 読み書きは ctx を見ないので、**期限を `SetDeadline` に移し、取り消しでは
  `context.AfterFunc` で接続ごと閉じる**（サーバが黙っても必ず抜ける）
- テストは httptest でページと WebSocket の両方を立てる（実サイトには繋がない）。
  BaseURL の `http` → `ws` / `https` → `wss` で WebSocket の URL を作るのはこのため。
  `testdata/shogidb2_kif_reply.json` が実際の応答

### 壊れやすい点

- **LiveView のプロトコル版 `vsn=2.0.0`**（メッセージが `[join_ref, ref, topic, event, payload]`
  の配列）。サイトの phoenix_live_view が上がると変わりうる
- **csrf とセッション Cookie。** どちらかが欠けると join が `unauthorized` / `stale` で弾かれる
  （エラーには `status` と `response` を載せてある）
- **イベント名 `kif`** と、返りの `diff.e` に入る形。無ければ「kif イベントがありません」
- ルート要素の見つけ方（`data-phx-session` を持つ**最初の**要素）
