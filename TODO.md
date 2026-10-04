# TODO

`kicho` の積み残し。**今すぐやらないが、決めた考え方を忘れないための置き場。**
実装の約束は `AGENTS.md`（と各パッケージの `AGENTS.md`）にある。

## 取得元

- ライブ取得元は**読売（竜王戦）・日本将棋連盟の中継・将棋DB2**の3つ。他サイトは未対応。
  増やすときはスキル `kicho-add-source`（`_docs/skills/`）
- **連盟は棋戦を絞っていない。** ID は中継のパスなので、王位戦以外でも
  中継ページの URL さえ貼れば取れるはず（`oui` 以外は未確認）
- 読売の棋士コメント（`comment`）は取り込んでいない。KIF に載せるなら `*` 行で、
  `num:0` のコメント（対局前の記述）は初手より前に置くことになる。
  **挑戦者決定戦には 1 件も無い**など棋戦によって違う（`_docs/sources/yomiuri.md`）
- 消費時間は保存していない。KIF の消費時間欄も空。
  読売は `spend` の積み上げで累計消費時間を作れる（分単位。`_docs/sources/yomiuri.md`）。
  将棋DB2 は 1 手ごとの消費時間がデータに無い

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
