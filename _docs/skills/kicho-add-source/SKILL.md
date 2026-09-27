---
name: kicho-add-source
description: kicho にライブ取得元（棋譜中継サイト）を足す手順と、触る箇所の一覧。読売・日本将棋連盟の中継・将棋DB2 に続く 4 つ目を足すとき、または既存の取得元の判別や URL の扱いを変えるときに使う。「〇〇のサイトの棋譜を取れるようにしたい」「新しい中継サイトに対応」「source を足す」ときに使う。
---

# ライブ取得元を足す

今の取得元は **読売（`yomiuri`）・日本将棋連盟の中継（`shogilive`）・将棋DB2（`shogidb2`）**。
取り方は違っても、**カードに積む・更新する・保存する・ライブ URL を配る、の扱いは同じ**にする。

## 先に決めること

1. **原本はどれか。** サイトが `.kif` を配っているならそれが原本（連盟）。
   構造化データしか無いなら kicho が KIF を組み立て、それが原本になる（読売・将棋DB2）。
   サイトが「KIF 形式」として出す文字列が作り物（消費時間を 0 で埋めている等）なら使わない
2. **`source_id` は何か。** ID から取得先の URL を**組み立て直せる**こと
   （カードの「更新」とライブ経路が ID だけで取りに行く）。人が開ける URL（`source_url`）も
   ID から作れること
3. **URL の判別。** そのサイトの URL を見分ける `IsXxxURL` を作る。
   ⚠️ **汎用の URL（`.kif` として読む口）より先に振り分ける** —— 中継ページは HTML なので、
   汎用の口に落ちると「KIF ではない」で終わる

## 触る箇所

| 場所 | やること |
|---|---|
| `scrape/<name>.go` | 取得本体。`FetchGame`（メタ付き）と `FetchKifu`（KIF 本文だけ。ライブ経路用）、`IsXxxURL`、ID の抽出。テストは httptest で（実サイトに繋がない） |
| `store/store.go` | `Source*` 定数を足す |
| `fetch.go` | `Fetcher` にフィールド、`NewFetcher`、`Fetch` の振り分け、`Refresh` の分岐、`fetchXxx` |
| `game.go` | `fromXxx`（`Fetched` への変換。`SourceURL` もここで） |
| `kicho.go` | `Library.resolveSource`（保存・追跡で `source_url` を決め直す）と、`Open` で `httpapi` に取得元を渡す |
| `httpapi/server.go` | ライブ経路のルートとパス組み立て（`XxxKifuPath`）。**到達テスト**（`TestPathBuildersReachTheRoutes`）も足す |
| `_cmd/kicho/serverservice.go` | `SourceURLs` の分岐 |
| `_cmd/kicho/frontend/src/App.tsx` | `SOURCE_LABELS`（取得タブの入力の案内も） |
| **ikkyoku** | **`SOURCE_LABELS` も足す**（あちらにも同じ表がある） |
| ドキュメント | `scrape/AGENTS.md` に制約と壊れやすい点、`_docs/sources/<name>.md` に調査記録、ルート `AGENTS.md` の入り口の表、`httpapi/AGENTS.md` のエンドポイント表 |

`RefetchableURL` と `Library.Watch` は「paste 以外は取り直せる」判定なので、
取得元を足しても変えなくてよい（取り直せない取得元を足すなら両方を揃えて変える）。

## 確かめる

```powershell
go vet ./...
go test ./...
.\check-consumers.ps1     # 公開 API（Source 定数・Fetched）が変わるので流す
```

Wails 側は bindings を作り直してフロントをビルドする（`_cmd/kicho/AGENTS.md`）。
実サイトでの確認は取得タブに URL を貼る → 「更新」 → 保存 → 取得 URL を外部ツールで開く。
