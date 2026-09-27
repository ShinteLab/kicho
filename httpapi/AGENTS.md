# httpapi/AGENTS.md

棋譜配信 HTTP サーバ。ShogiHome 等の外部ツールが「URL から棋譜を取得する」機能で使う。
全体像（ライブ取得とアーカイブの役割分担）はルートの `AGENTS.md`。

## エンドポイント

| ルート | 内容 |
|---|---|
| `GET /kifu` | 保存済み棋譜の一覧（JSON）。0 件でも `null` でなく `[]` を返す |
| `GET /kifu/{id}` | 保存済み棋譜を**登録された形式の原本のまま**返す |
| `GET /kifu/{id}.{ext}` | その形式に変換して返す。未実装なら **501**（`format/AGENTS.md`） |
| `GET /ryuoh/kifu/{id}` | 読売から直接取得して KIF を返す（**Node 版との互換**。保存はしない） |
| `GET /shogilive/kifu/{id...}` | 連盟の中継から直接取得して KIF を返す（保存はしない） |
| `GET /shogidb2/kifu/{id}` | 将棋DB2 から直接取得して KIF を返す（保存はしない） |
| `GET /` | 使い方の案内 |

- ライブ経路（`/ryuoh` `/shogilive` `/shogidb2`）は**リクエストのたびに取りに行く**。
  `Cache-Control: no-store` を付ける（対局中は棋譜が伸びるため）
- `GET /kifu` は**件数を切らない**（外部ツールが全件を期待する）。上限を掛けるのは
  アプリの一覧（`kicho.Library.Search`）だけ
- `/shogilive/kifu/` だけ `{id...}` のワイルドカード。連盟の棋譜 ID は中継のパス
  （`oui/kifu/67/oui202607290101`）で**スラッシュを含む**ため
- サイトの `.kif` を直接使わず kicho を経由させるのは、**連盟が Shift_JIS で
  配信している**ため。ここを通すと UTF-8 で返る

## URL の組み立てはここに一本化する

外部ツールへ貼る URL は UI の「URL コピー」で渡す。パスの組み立ては
`KifuPath` / `RyuohKifuPath` / `ShogiLiveKifuPath` / `ShogiDB2KifuPath` にあり、
**フロントにパスを直書きしない**。ベース URL（ホスト・ポート・LAN アドレス）は
`_cmd/kicho` の `ServerService.KifuURLs` / `SourceURLs` が付けて返す。

- ⚠️ パス組み立てが `routes()` の登録と食い違うと URL コピーが壊れるため、
  **実際に到達できるか**をテストしている（`TestPathBuildersReachTheRoutes`）。
  ルートを足したらパス組み立てとテストも足す
- ⚠️ `ShogiLiveKifuPath` はスラッシュを区切りとして残し、**各セグメントだけを
  エスケープする**（`url.PathEscape` を全体にかけると `%2F` になって経路に届かない）

## bind と公開

- 既定は `127.0.0.1:3000`（Node 版と同じポート）。UI の「サーバ」タブで bind とポートを変更できる
- **`0.0.0.0` にすると認証なしで LAN に公開される。** UI で警告を出している。
  認証を付ける場合はこのパッケージに入れること
