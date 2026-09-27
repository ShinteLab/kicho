# format/AGENTS.md

`GET /kifu/{id}.{ext}` の形式変換。**現時点で実装済みの変換は無い。**

- 原本と同じ形式を要求されたらそのまま返す
- 未実装なら `ErrUnsupported` → HTTP **501**（対応済みの形式一覧を本文に出す）
- 増やすときは `Register(from, to, fn)` を呼ぶだけ
- 変換結果は保存しない（変換は決定的なので都度計算で足りる）

## 変換の難易度は形式によって大きく違う

| 変換 | 難易度 | 理由 |
|---|---|---|
| KIF → CSA / USI / SFEN / JKF | 中 | KIF は移動元座標を持つので機械的。`同` の解決と成/不成に注意 |
| **KIF → KI2** | **高** | **合法手生成が必要**。KI2 は移動元を書かず `右/左/上/引/寄/直` で区別するため、「その升に動ける同じ駒が他にあるか」を知る必要がある |
| → USEN | 不明 | ShogiHome 独自形式。仕様確認から |

**KIF → USI/SFEN は `core/kifu` に既にある**（`StartSFEN` / `DecodeMoves`）。
逆方向（USI → 日本語表記）も `NewNotation` / `FormatMoves` にある。

## engine が要る変換はここに置く

合法手生成は `github.com/ShinteLab/engine` にある。ただし**ルート（shinte）の AGENTS.md の
依存規約は `engine → core` の一方向**なので、`core/kifu` から engine は参照できない。
`kicho → engine` は循環しないため、**engine が要る変換（KI2）はこのパッケージに置くこと**
（機械的な変換は `core/kifu` に置いてよい）。
