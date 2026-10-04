---
name: kicho-change-api
description: kicho の公開 API（kicho.Library / Fetcher / Fetched / RefetchableURL / sentinel エラー / store の型）を変える、または kicho に依存モジュールを足すときの手順。ikkyoku が kicho をライブラリとして import しているので、kicho で go build が通っても ikkyoku が壊れることがある。「Library にメソッドを足す」「Fetched のフィールドを変える」「store の型を変える」「kicho の go.mod に replace を足す」「スキーマの版を上げる」「check-consumers.ps1 が落ちる」ときに使う。
---

# kicho の公開 API を変える

⚠️ **`go build ./...` を kicho で通しても ikkyoku はコンパイルされない。**
ikkyoku（`../ikkyoku`）が `kicho.Library` などを直接 import している。

## ikkyoku が使っているもの

- `kicho.Open(dbPath, logger)` と `Library` のメソッド（蔵書・保存・取り込み・仮の一覧）
- `kicho.NewFetcher()` の `Fetch` / `Refresh`（**DB を開かずに取得する**。`ikkyoku/app/kifufetch.go`）
- `Fetched` / `RefetchableURL` / `MaxSearchRows` / sentinel エラー（`ErrEmptyKifu` など）
- `store` の型（`Record` / `Query` / `Watch` / `MinTrigramLen` / `ErrNotFound` / `ErrSchemaTooNew`）

`scrape` は直接使っていない。今の使い方は ikkyoku で確かめる:

```powershell
rg -n "kicho\.|store\." ..\ikkyoku --glob "*.go"
```

## 手順

1. **挙動は `Library`（または `Fetcher`）に足す。** 使う側（`_cmd/kicho/kifuservice.go`・
   ikkyoku）に取得元の知識や保存の組み立てを書かない
2. エラーの種類を増やすなら `errors.go` に sentinel を足す（使う側は `errors.Is` で分岐する）
3. kicho で確かめる

   ```powershell
   go vet ./...
   go test ./...
   ```

4. **使う側を確かめる**

   ```powershell
   .\check-consumers.ps1          # replace の取りこぼし + ikkyoku の build / vet
   .\check-consumers.ps1 -Test    # go test まで
   ```

5. ikkyoku が壊れたら ikkyoku 側も直す（`ikkyoku/app/kifuservice.go` が
   `_cmd/kicho/kifuservice.go` の移植）
6. Wails のサービス（`_cmd/kicho/*.go`）の形が変わったら bindings を作り直す
   （`_cmd/kicho/AGENTS.md`）

## `check-consumers.ps1` が見ていること

1. **replace の取りこぼし** —— Go は**メインモジュール以外の replace を読まない**。
   kicho が相対 replace で引く依存は、ikkyoku 側の
   **2 つの go.mod（ルートと `_cmd/ikkyoku`）両方**に同じ replace が要る。
   **kicho に依存を足すたびに発生する**
2. **使う側のビルド** —— 一時的な `go.work` を作り、**この作業ツリーの kicho** を
   `replace` で使わせて `go build` / `go vet` を流す。ikkyoku の replace は
   `../kicho`（メインのチェックアウト）を指すので、worktree の変更は届かないため

- ⚠️ **go.work は replace より優先される**ので、2 が通っても 1 は検出できない（別に見ている）
- ⚠️ **go.work を手で書かないこと。** Windows のパスは go.work の構文でそのままは書けない
  （`/` 区切りだと `use` として認識されず、`\` はクォートすると invalid quoted string）。
  `go work init` / `go work use` / `go work edit` に書かせる
- ikkyoku が見つからなければ何もせず終わる（ikkyoku はローカルにしか無い）

## スキーマの版を上げるとき

`store/AGENTS.md` の「マイグレーション」に従う。加えて、**DB を共用している場合は
更新していない ikkyoku が `ErrSchemaTooNew` で開けなくなる**（別々の DB が既定なので
通常は影響しない）。両方をビルドし直せば解消する。
