[CmdletBinding()]
param(
    [string]$BaselineManifest,
    [string]$GoExecutable = $env:GALLERY_GO,
    [ValidateRange(1, 64)]
    [int]$MaxProcessors = 2,
    [switch]$AllowDirty
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if (-not [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform(
        [System.Runtime.InteropServices.OSPlatform]::Windows)) {
    throw '真实历史升级 smoke 必须在 Windows 上执行'
}

function Resolve-Executable([string]$Configured, [string]$CommandName) {
    if ($Configured) {
        return (Resolve-Path -LiteralPath $Configured).Path
    }
    return (Get-Command $CommandName -ErrorAction Stop).Source
}

function Invoke-Checked([string]$Description, [scriptblock]$Command) {
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Description 失败，退出码 $LASTEXITCODE"
    }
}

function Remove-SafeTemp([string]$Path, [string]$Parent) {
    if (-not (Test-Path -LiteralPath $Path)) { return }
    $full = [System.IO.Path]::GetFullPath($Path)
    $fullParent = [System.IO.Path]::GetFullPath($Parent)
    $relative = [System.IO.Path]::GetRelativePath($fullParent, $full)
    if ([System.IO.Path]::IsPathRooted($relative) -or
        $relative -notmatch '^gallery-historical-transition-[0-9a-f]{32}$') {
        throw "拒绝删除非历史升级临时目录：$full"
    }
    Remove-Item -LiteralPath $full -Recurse -Force
}

function Assert-BuildProvenance(
    [string]$Binary,
    [string]$ExpectedCommit,
    [bool]$ExpectedModified,
    [string]$Label
) {
    $metadata = @(& $go version -m $Binary)
    if ($LASTEXITCODE -ne 0) { throw "读取 $Label 构建来源失败" }
    $revision = @($metadata | Select-String -Pattern 'vcs\.revision=(?<value>[0-9a-f]{40})')
    $modified = @($metadata | Select-String -Pattern 'vcs\.modified=(?<value>true|false)')
    if ($revision.Count -ne 1 -or $revision[0].Matches[0].Groups['value'].Value -ne $ExpectedCommit) {
        throw "$Label 没有绑定预期 Git commit"
    }
    $wantModified = if ($ExpectedModified) { 'true' } else { 'false' }
    if ($modified.Count -ne 1 -or $modified[0].Matches[0].Groups['value'].Value -ne $wantModified) {
        throw "$Label 的 vcs.modified 事实不匹配"
    }
}

function Get-ProgramSeal([string[]]$Paths) {
    return @(
        foreach ($path in $Paths) {
            $item = Get-Item -LiteralPath $path
            [pscustomobject]@{
                Name = $item.Name
                Length = $item.Length
                SHA256 = (Get-FileHash -LiteralPath $item.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
            }
        }
    ) | ConvertTo-Json -Compress
}

$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
. (Join-Path $PSScriptRoot 'WindowsHistoricalCompatibility.ps1')
$git = Resolve-Executable '' 'git'
$go = Resolve-Executable $GoExecutable 'go'
$goVersion = (& $go version).Trim()
if ($LASTEXITCODE -ne 0 -or $goVersion -notmatch '\bgo1\.26\.5\b') {
    throw "真实历史升级门禁要求 Go 1.26.5，实际为：$goVersion"
}

$dirty = @(& $git -C $repoRoot status --porcelain=v1 --untracked-files=all)
if ($LASTEXITCODE -ne 0) { throw '无法读取当前工作树状态' }
if ($dirty.Count -gt 0 -and -not $AllowDirty) {
    throw '正式历史升级门禁只允许从干净当前工作树执行；开发验证可显式使用 -AllowDirty'
}

$currentCommit = (& $git -C $repoRoot rev-parse HEAD).Trim()
$compatibility = Get-GalleryHistoricalCompatibility $repoRoot $BaselineManifest
$currentSchema = $compatibility.CurrentControlSchema
$resolvedBaselines = @()
foreach ($baseline in $compatibility.Baselines) {
    $resolvedCommit = (& $git -C $repoRoot rev-parse "$($baseline.Commit)`^{commit}").Trim()
    if ($LASTEXITCODE -ne 0 -or $resolvedCommit -ne $baseline.Commit) {
        throw "无法解析精确历史 commit $($baseline.Commit)；CI 必须 checkout 完整历史"
    }
    if ($resolvedCommit -eq $currentCommit) { throw '历史与当前 commit 必须不同' }
    & $git -C $repoRoot merge-base --is-ancestor $resolvedCommit $currentCommit
    if ($LASTEXITCODE -ne 0) { throw "历史 commit $resolvedCommit 不是当前 HEAD 的祖先" }
    $resolvedBaselines += $baseline
}

$tempParent = [System.IO.Path]::GetTempPath()
$tempRoot = Join-Path $tempParent ('gallery-historical-transition-' + [guid]::NewGuid().ToString('N'))
$historicalRoot = Join-Path $tempRoot 'historical-source'
$binRoot = Join-Path $tempRoot 'bin'
New-Item -ItemType Directory -Path $binRoot -Force | Out-Null

$previousEnvironment = @{
    CGO_ENABLED = $env:CGO_ENABLED
    GOMAXPROCS = $env:GOMAXPROCS
    GOFLAGS = $env:GOFLAGS
    GOTOOLCHAIN = $env:GOTOOLCHAIN
    PATH = $env:PATH
}

try {
    $env:CGO_ENABLED = '0'
    $env:GOMAXPROCS = [string]$MaxProcessors
    $env:GOFLAGS = '-p=1'
    $env:GOTOOLCHAIN = 'local'
    $env:PATH = "$(Split-Path -Parent $go);$env:PATH"

    Invoke-Checked '克隆本地历史对象' {
        & $git clone --local --no-hardlinks --no-checkout $repoRoot $historicalRoot
    }
    $currentBinary = Join-Path $binRoot 'galleryd-current.exe'
    $probe = Join-Path $binRoot 'historical-upgrade.exe'
    Push-Location $repoRoot
    try {
        Invoke-Checked '构建当前 galleryd' {
            & $go build -trimpath -buildvcs=true -o $currentBinary ./cmd/galleryd
        }
        Invoke-Checked '构建历史升级 probe' {
            & $go build -trimpath -buildvcs=true -o $probe ./tools/testlab/cmd/historical-upgrade
        }
    } finally {
        Pop-Location
    }

    Assert-BuildProvenance $currentBinary $currentCommit ($dirty.Count -gt 0) '当前 galleryd'
    foreach ($baseline in $resolvedBaselines) {
        Invoke-Checked "检出 schema $($baseline.Schema) detached 历史提交" {
            & $git -C $historicalRoot checkout --detach $baseline.Commit
        }
        if ((& $git -C $historicalRoot rev-parse HEAD).Trim() -ne $baseline.Commit -or
            @(& $git -C $historicalRoot status --porcelain=v1 --untracked-files=all).Count -ne 0) {
            throw "schema $($baseline.Schema) 历史源码身份或干净状态不正确"
        }
        $historicalSchema = Get-GalleryControlSchemaVersion $historicalRoot
        if ($historicalSchema -ne $baseline.Schema) {
            throw "历史基线 $($baseline.Label) 的 control schema=$historicalSchema，预期 $($baseline.Schema)"
        }

        $historicalBinary = Join-Path $binRoot ("galleryd-historical-schema-{0}.exe" -f $historicalSchema)
        Push-Location $historicalRoot
        try {
            Invoke-Checked "构建 schema $historicalSchema 真实历史 galleryd" {
                & $go build -trimpath -buildvcs=true -o $historicalBinary ./cmd/galleryd
            }
        } finally {
            Pop-Location
        }
        Assert-BuildProvenance $historicalBinary $baseline.Commit $false "schema $historicalSchema 历史 galleryd"
        $programPaths = @($historicalBinary, $currentBinary, $probe)
        $programBefore = Get-ProgramSeal $programPaths

        $probeOutput = @(& $probe `
                -historical-bin $historicalBinary `
                -current-bin $currentBinary `
                -historical-commit $baseline.Commit `
                -current-commit $currentCommit `
                -historical-schema $historicalSchema `
                -current-schema $currentSchema)
        if ($LASTEXITCODE -ne 0) { throw "schema $historicalSchema 真实历史升级 probe 失败，退出码 $LASTEXITCODE" }
        if ($probeOutput.Count -eq 0) { throw "schema $historicalSchema 真实历史升级 probe 没有输出结果" }
        if ((Get-ProgramSeal $programPaths) -ne $programBefore) {
            throw "schema $historicalSchema 历史升级期间程序二进制发生变化，程序与数据未保持分离"
        }

        $result = $probeOutput[-1] | ConvertFrom-Json
        foreach ($field in @(
                'restoreWillMigrate',
                'upgradePreservedFacts',
                'upgradePreservedCredential',
                'downgradeRejected',
                'downgradeLeftDatabaseUntouched',
                'downgradePreservedCredential',
                'currentRestartedAfterDowngrade',
                'allNormalStopsExitedGracefully'
            )) {
            if ($result.$field -ne $true) { throw "schema $historicalSchema 真实历史升级结果未通过：$field" }
        }
        if ($result.historicalCommit -ne $baseline.Commit -or $result.currentCommit -ne $currentCommit -or
            $result.historicalSchemaVersion -ne $historicalSchema -or $result.currentSchemaVersion -ne $currentSchema -or
            $result.historicalBackupSchemaVersion -ne $historicalSchema -or
            $result.currentBackupSchemaVersion -ne $currentSchema) {
            throw "schema $historicalSchema 真实历史升级结果的提交或 schema 身份不一致"
        }

        [pscustomobject]@{
            Baseline = $baseline.Label
            HistoricalCommit = $result.historicalCommit
            CurrentCommit = $result.currentCommit
            HistoricalSchemaVersion = $result.historicalSchemaVersion
            CurrentSchemaVersion = $result.currentSchemaVersion
            HistoricalBackupSchemaVersion = $result.historicalBackupSchemaVersion
            CurrentBackupSchemaVersion = $result.currentBackupSchemaVersion
            RestoreWillMigrate = 'passed'
            UpgradePreservedFacts = 'passed'
            UpgradePreservedCredential = 'passed'
            DowngradeRejected = 'passed'
            DowngradeLeftDatabaseUntouched = 'passed'
            DowngradePreservedCredential = 'passed'
            CurrentRestartedAfterDowngrade = 'passed'
            ProgramDataSeparated = 'passed'
            AllNormalStopsExitedGracefully = 'passed'
        }
    }
} finally {
    $env:CGO_ENABLED = $previousEnvironment.CGO_ENABLED
    $env:GOMAXPROCS = $previousEnvironment.GOMAXPROCS
    $env:GOFLAGS = $previousEnvironment.GOFLAGS
    $env:GOTOOLCHAIN = $previousEnvironment.GOTOOLCHAIN
    $env:PATH = $previousEnvironment.PATH
    Remove-SafeTemp $tempRoot $tempParent
}
