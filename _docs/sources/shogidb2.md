# 将棋DB2 の調査記録

取得の制約（壊すと困ること・壊れやすい点）は `scrape/AGENTS.md`。
ここはサイトの作りと取り方の詳細（2026-09-26 に調査）。

## サイトの作り

- `robots.txt` に制限は無い。`.json` / `.kif` / `/api/...` のような直接の口は**無い**（500 / 404）
- ページは **Phoenix LiveView**。棋譜ダイアログ（`#kifu-modal textarea`）はサーバの HTML では
  **空**で、「KIF形式」ボタン（`phx-click="kif"`）を押すとサーバが
  `push_event("kif", {data})` で**構造化データ**を返し、ブラウザの `KifExporter` が文字にしている

## 取り方

1. 対局ページを GET。`<meta name="csrf-token">`、LiveView のルート要素
   （`data-phx-session` を持つ最初の要素）の `id`（`phx-...`）・`data-phx-session`・
   `data-phx-static`、それと **Set-Cookie** を控える
2. WebSocket `wss://shogidb2.com/live/websocket?_csrf_token=…&vsn=2.0.0&locale=ja`
   （Cookie と `Origin: https://shogidb2.com` を付ける）
3. `["1","1","lv:<id>","phx_join",{url, params:{_csrf_token,_mounts:0,locale}, session, static}]`
   → ref `"1"` の `phx_reply`
4. `["1","2","lv:<id>","event",{"type":"click","event":"kif","value":{}}]`
   → ref `"2"` の `phx_reply` の `response.diff.e` に `["kif",{"data":{…}}]`

`scrape/testdata/shogidb2_kif_reply.json` が実際の応答（ALSOK杯第76期王将戦 挑戦者決定リーグ戦
伊藤匠二冠 vs 郷田真隆九段）。

## data から KIF へ

| data | KIF |
|---|---|
| `player1` / `player2` | 先手 / 後手（**player1 が先手**。サイトの KifExporter と同じ） |
| `tournament_detail`（空なら `tournament`） | 棋戦 |
| `place` / `handicap` | 場所 / 手合割 |
| `start_at` | 開始日時。**UTC で来る**ので JST に直す |
| `time`（`"４時間"`。全角数字） | 持ち時間（`width.Fold` で半角に寄せて読む） |
| `moves[].csa`（`"+2726FU"`、最後が `"%TORYO"`） | 指し手 |

指し手は `kifu.StartSFEN(handicap)` → `kifu.DecodeCSA`（CSA → USI。終局名も返す）→
`kifu.FormatMoves`（USI → 日本語表記と移動元）で組み立て、終局名を最後の手に置く。

1 手ごとの消費時間はデータに無い（`time_consumed` は `"145▲240△240"` の合計だけ）。
