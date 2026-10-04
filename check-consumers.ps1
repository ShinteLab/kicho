<#
.SYNOPSIS
    kicho を **ライブラリとして使う側**（ikkyoku）が、この作業ツリーの kicho で
    ビルドできるかを確かめる。

.DESCRIPTION
    kicho は 2026-09-04 から ikkyoku に import されている（`kicho.Library`）。
    ところが `go build ./...` を kicho で通しても ikkyoku はコンパイルされないので、
    **公開 API を壊しても気づけない。** その穴を埋めるのがこのスクリプト。

    2 つを見る。

    1. replace の取りこぼし
       Go は**メインモジュール以外の replace を読まない。** kicho の go.mod が
       相対パスの replace で引いている依存は、使う側の go.mod にも同じ replace が
       無いとビルドできない。タグを打って proxy 経由に切り替えるまで、
       kicho に依存を足すたびに ikkyoku 側（2 つの go.mod）へ書き足す必要がある。

    2. 使う側がビルドできるか
       go.work を一時的に作って、**この作業ツリーの kicho** を使わせる。
       ikkyoku の replace は `../kicho`（メインのチェックアウト）を指しているので、
       worktree の変更はそのままでは届かないため。

       ⚠️ **go.work は replace より優先される。** つまりビルドが通っても
       replace の取りこぼしは検出できない。だから 1 を別に見ている。

.PARAMETER Consumer
    使う側のリポジトリ。既定は kicho の隣の ikkyoku。
    ⚠️ **worktree から実行すると `..` は shinte ではない**ので、
    見つからなければ git に本体の場所を聞いて探し直す。

.PARAMETER Test
    ビルドと vet に加えて go test も流す。

.EXAMPLE
    .\check-consumers.ps1
    .\check-consumers.ps1 -Test
#>
[CmdletBinding()]
param(
    [string]$Consumer = "",
    [switch]$Test
)

$ErrorActionPreference = "Stop"

$kicho = (Resolve-Path $PSScriptRoot).Path

if ($Consumer -eq "") {
    $Consumer = Join-Path (Split-Path -Parent $kicho) "ikkyoku"
    if (-not (Test-Path $Consumer)) {
        # worktree の中にいる。git に本体（.git の実体）の場所を聞いて、その隣を見る。
        $common = & git -C $kicho rev-parse --path-format=absolute --git-common-dir 2>$null
        if ($LASTEXITCODE -eq 0 -and $common) {
            $main = Split-Path -Parent ($common.Trim())
            $Consumer = Join-Path (Split-Path -Parent $main) "ikkyoku"
        }
    }
}

if (-not (Test-Path $Consumer)) {
    Write-Host "使う側が見つかりません: $Consumer" -ForegroundColor Yellow
    Write-Host "ikkyoku はまだ GitHub に無いのでローカルにしか存在しません。" -ForegroundColor Yellow
    exit 0
}
$consumer = (Resolve-Path $Consumer).Path

# kicho が相対パスの replace で引いているモジュール（= 使う側にも要るもの）。
function Get-RelativeReplaces([string]$goMod) {
    $out = @{}
    foreach ($line in Get-Content -LiteralPath $goMod -Encoding utf8) {
        if ($line -match '^\s*replace\s+(\S+)\s+=>\s+(\S+)') {
            $out[$Matches[1]] = $Matches[2]
        }
    }
    return $out
}

$failed = @()

# --- 1. replace の取りこぼし ------------------------------------------------
Write-Host "replace の確認" -ForegroundColor Cyan

$needed = Get-RelativeReplaces (Join-Path $kicho "go.mod")
$consumerMods = @(
    (Join-Path $consumer "go.mod"),
    (Join-Path $consumer "_cmd\ikkyoku\go.mod")
) | Where-Object { Test-Path $_ }

foreach ($mod in $consumerMods) {
    $have = Get-RelativeReplaces $mod
    foreach ($path in $needed.Keys) {
        if (-not $have.ContainsKey($path)) {
            $rel = $mod.Substring($consumer.Length).TrimStart('\')
            Write-Host "  NG $rel : replace $path が無い" -ForegroundColor Red
            $failed += "replace $path in $rel"
        }
    }
}
if ($failed.Count -eq 0) {
    Write-Host "  OK ($($needed.Count) 件)" -ForegroundColor Green
}

# --- 2. 使う側がビルドできるか ----------------------------------------------
Write-Host "使う側のビルド ($consumer)" -ForegroundColor Cyan

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("kicho-check-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $work | Out-Null
$goWork = Join-Path $work "go.work"

try {
    # ワークスペースに並べるのは**使う側のモジュールだけ**。
    # kicho は use ではなく replace で差し替える —— use に入れると kicho 自身の
    # 相対 replace が worktree 基準で解決されて、使う側の同じ依存と
    # 「同じモジュールが2回出てくる」衝突になるため。
    #
    # `_cmd/kicho`（kicho-app）も入れない。Wails のバージョンが ikkyoku 側と
    # 揃っておらず、同じワークスペースに入れると片方に寄ってしまう。
    $useDirs = @($consumer)
    $cmdDir = Join-Path $consumer "_cmd\ikkyoku"
    if (Test-Path (Join-Path $cmdDir "go.mod")) { $useDirs += $cmdDir }

    # ⚠️ **go.work を手で書かないこと。** Windows のパスは go.work にそのままでは
    # 書けない（`/` 区切りにすると use のディレクトリとして認識されず、`\` を
    # クォートすると invalid quoted string になる）。go 自身に書かせる。
    $env:GOWORK = $goWork
    & go work init
    if ($LASTEXITCODE -ne 0) { throw "go work init に失敗しました" }
    foreach ($d in $useDirs) {
        & go work use $d
        if ($LASTEXITCODE -ne 0) { throw "go work use $d に失敗しました" }
    }
    & go work edit -replace "github.com/ShinteLab/kicho=$kicho"
    if ($LASTEXITCODE -ne 0) { throw "go work edit -replace に失敗しました" }
    foreach ($dir in @($consumer) + @(if (Test-Path (Join-Path $cmdDir "go.mod")) { $cmdDir })) {
        $name = $dir.Substring($consumer.Length).TrimStart('\')
        if ($name -eq "") { $name = Split-Path -Leaf $consumer }

        Push-Location $dir
        try {
            & go build ./...
            if ($LASTEXITCODE -ne 0) { $failed += "go build in $name"; Write-Host "  NG $name : go build" -ForegroundColor Red }
            else { Write-Host "  OK $name : go build" -ForegroundColor Green }

            & go vet ./...
            if ($LASTEXITCODE -ne 0) { $failed += "go vet in $name"; Write-Host "  NG $name : go vet" -ForegroundColor Red }
            else { Write-Host "  OK $name : go vet" -ForegroundColor Green }

            if ($Test) {
                & go test ./...
                if ($LASTEXITCODE -ne 0) { $failed += "go test in $name"; Write-Host "  NG $name : go test" -ForegroundColor Red }
                else { Write-Host "  OK $name : go test" -ForegroundColor Green }
            }
        } finally {
            Pop-Location
        }
    }
} finally {
    Remove-Item Env:\GOWORK -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}

if ($failed.Count -gt 0) {
    Write-Host ""
    Write-Host "失敗: $($failed -join ', ')" -ForegroundColor Red
    exit 1
}
Write-Host ""
Write-Host "すべて通りました。" -ForegroundColor Green
