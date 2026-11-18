# TODO

`kicho` の積み残し。**今すぐやらないが、決めた考え方を忘れないための置き場。**
実装の約束は `AGENTS.md`（と各パッケージの `AGENTS.md`）にある。

## 取得元

- ライブ取得元は**読売（竜王戦）・日本将棋連盟の中継・将棋DB2・ふわりと将棋**の4つ。他サイトは未対応。
  増やすときはスキル `kicho-add-source`（`_docs/skills/`）
- **連盟は棋戦を絞っていない。** ID は中継のパスなので、王位戦以外でも
  中継ページの URL さえ貼れば取れるはず（`oui` 以外は未確認）
- 読売の棋士コメント（`comment`）は取り込んでいない。KIF に載せるなら `*` 行で、
  `num:0` のコメント（対局前の記述）は初手より前に置くことになる。
  **挑戦者決定戦には 1 件も無い**など棋戦によって違う（`_docs/sources/yomiuri.md`）
- 消費時間は保存していない。KIF の消費時間欄も空。
  読売は `spend` の積み上げで累計消費時間を作れる（分単位。`_docs/sources/yomiuri.md`）。
  将棋DB2 は 1 手ごとの消費時間がデータに無い。
  ふわりと将棋は手によって「残り」「消費/残り」「消費だけ」と書き方が揃わない
- **ikkyoku はエラーの印（`Fetched.ErrorMove` / `Error`、`Record.ErrorMove` / `Error`）を
  まだ出していない。** 誤記のある中継のカードが、あちらでは普通の棋譜に見える
- **ikkyoku は指摘と訂正をまだ扱っていない。** 反則手（二歩・王手放置・打ち歩詰め）を `legal` で
  見つけたら `Library.Flag(id, 手, 理由, "ikkyoku")` を付ける、指摘（`Record.Flags` /
  `FlaggedMove` / `FlagsStale`）と派生棋譜（`Record.Children` / `ParentChanged`）を表示する、
  訂正した棋譜を `SaveDerived` で子として保存する
- kicho のアプリには指摘を付ける・外す操作が無い（表示だけ）。一覧の「要確認」の絞り込み
  （`Query.NeedsAttention`）も画面にはまだ出していない
- ふわりと将棋の評価値（`92%`）と AI の推奨手（`ai39銀`）は取り込んでいない（解説の行だけ）

## 形式変換

- **実装済みの変換は無い**（`/kifu/{id}.{ext}` は原本と同じ形式以外 501）。
  増やすときは `format.Register`。難易度と置き場所は `format/AGENTS.md`
  - KIF → CSA / USI / SFEN / JKF は機械的（`core/kifu` の `StartSFEN` / `DecodeMoves` が使える）
  - **KIF → KI2 は合法手生成が要る**（engine。`kicho/format` 側に置く）
  - USEN は ShogiHome 独自形式。仕様確認から

## 盤面表示

- **未実装。** kicho が持つのは KIF（漢字表記）で、`@shinte/web` の `<shogi-board>` は SFEN 入力。
  **engine は要らない** —— `core/kifu` に KIF → USI/SFEN の変換があり
  （KIF は移動元座標を持つので盤が要らない）、ikkyoku はこれだけで盤に出している

## HTTP 配信

- **認証なし。** `0.0.0.0` で待ち受けると LAN に公開される（UI で警告を出している）。
  付けるなら `httpapi` に入れる
