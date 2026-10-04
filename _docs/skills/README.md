# _docs/skills

このリポジトリの**作業手順**を置く場所。

| ディレクトリ | 中身 |
|---|---|
| `kicho-change-api/` | 公開 API（`Library` / `Fetcher` / `Fetched` / sentinel / `store` の型）を変える。ikkyoku の追従と `check-consumers.ps1` |
| `kicho-add-source/` | ライブ取得元（中継サイト）を足す。触る箇所の一覧 |

- **ただの Markdown**（`SKILL.md`）。どのコーディングエージェントでも、人が読んでもよい
- ⚠️ **リポジトリに `.claude/` を置かない**（Claude で使う前提になるため。`.gitignore` で無視している）
- Claude Code でスキルとして使うなら、手元で `.claude/skills/<名前>` から
  ここへジャンクション（シンボリックリンク）を張る。例（PowerShell、リポジトリ直下で）:

  ```powershell
  New-Item -ItemType Junction -Path .claude\skills\kicho-add-source -Target (Resolve-Path _docs\skills\kicho-add-source)
  ```

- ⚠️ **編集するのはここ**（git で管理しているのはこちらだけ）
