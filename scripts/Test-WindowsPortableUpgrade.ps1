[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$PreviousArchivePath,
    [Parameter(Mandatory)]
    [string]$CurrentArchivePath,
    [Parameter(Mandatory)]
    [string]$PreviousVersion,
    [Parameter(Mandatory)]
    [string]$CurrentVersion,
    [Parameter(Mandatory)]
    [string]$UpgradeProbeExecutable
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if (-not [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform(
        [System.Runtime.InteropServices.OSPlatform]::Windows)) {
    throw 'Windows 便携包版本切换 smoke 必须在 Windows 上执行'
}
if ($PreviousVersion -eq $CurrentVersion) { throw '上一版本与当前版本必须不同' }

$previousArchive = (Resolve-Path -LiteralPath $PreviousArchivePath).Path
$currentArchive = (Resolve-Path -LiteralPath $CurrentArchivePath).Path
$probe = (Resolve-Path -LiteralPath $UpgradeProbeExecutable).Path
if ($previousArchive -eq $currentArchive) { throw '两个版本不能复用同一个便携 ZIP' }

$tempParent = [System.IO.Path]::GetTempPath()
$tempRoot = Join-Path $tempParent ('gallery-portable-transition-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tempRoot -Force | Out-Null

function Remove-SafeTemp([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) { return }
    $full = [System.IO.Path]::GetFullPath($Path)
    $parent = [System.IO.Path]::GetFullPath($tempParent)
    $relative = [System.IO.Path]::GetRelativePath($parent, $full)
    if ([System.IO.Path]::IsPathRooted($relative) -or -not $relative.StartsWith('gallery-portable-transition-')) {
        throw "拒绝删除非版本切换临时目录：$full"
    }
    Remove-Item -LiteralPath $full -Recurse -Force
}

function Expand-SafePortable([string]$Archive, [string]$Destination) {
    New-Item -ItemType Directory -Path $Destination -Force | Out-Null
    $zip = [System.IO.Compression.ZipFile]::OpenRead($Archive)
    try {
        foreach ($entry in $zip.Entries) {
            if ([System.IO.Path]::IsPathRooted($entry.FullName) -or $entry.FullName.Contains(':') -or
                $entry.FullName -match '(^|[\\/])\.\.([\\/]|$)') {
                throw "ZIP 包含越界条目：$($entry.FullName)"
            }
        }
    } finally {
        $zip.Dispose()
    }
    Expand-Archive -LiteralPath $Archive -DestinationPath $Destination
    $roots = @(Get-ChildItem -LiteralPath $Destination -Directory)
    if ($roots.Count -ne 1) { throw "便携 ZIP 必须只有一个顶层目录：$Archive" }
    return $roots[0].FullName
}

function Get-PortableTreeSeal([string]$Root) {
    $fullRoot = [System.IO.Path]::GetFullPath($Root)
    $entries = @(Get-ChildItem -LiteralPath $fullRoot -Recurse -Force | Sort-Object FullName)
    $facts = foreach ($entry in $entries) {
        $relative = [System.IO.Path]::GetRelativePath($fullRoot, $entry.FullName)
        if ([System.IO.Path]::IsPathRooted($relative) -or $relative -match '(^|[\/\\])\.\.([\/\\]|$)') {
            throw "便携包目录封印遇到越界条目：$($entry.FullName)"
        }
        if ($entry.PSIsContainer) {
            "D`t$relative"
        } else {
            $hash = (Get-FileHash -LiteralPath $entry.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
            "F`t$relative`t$($entry.Length)`t$hash"
        }
    }
    return ($facts -join "`n")
}

try {
    # 每个输入先独立通过完整制品 smoke；因此版本切换链可以把外部/包内摘要、SBOM、
    # Authenticode 清单与内嵌 Web 当作已验证前置，而不是只验证两个任意 exe。
    & (Join-Path $PSScriptRoot 'Test-WindowsPortable.ps1') `
        -ArchivePath $previousArchive -ExpectedVersion $PreviousVersion | Out-Null
    & (Join-Path $PSScriptRoot 'Test-WindowsPortable.ps1') `
        -ArchivePath $currentArchive -ExpectedVersion $CurrentVersion | Out-Null

    $previousRoot = Expand-SafePortable $previousArchive (Join-Path $tempRoot 'previous')
    $currentRoot = Expand-SafePortable $currentArchive (Join-Path $tempRoot 'current')
    $previousBinary = Join-Path $previousRoot 'galleryd.exe'
    $currentBinary = Join-Path $currentRoot 'galleryd.exe'
    $previousTreeBefore = Get-PortableTreeSeal $previousRoot
    $currentTreeBefore = Get-PortableTreeSeal $currentRoot

    $probeOutput = @(& $probe `
        -previous-bin $previousBinary `
        -current-bin $currentBinary `
        -previous-version $PreviousVersion `
        -current-version $CurrentVersion)
    if ($LASTEXITCODE -ne 0) { throw "便携包版本切换 probe 失败，退出码 $LASTEXITCODE" }
    if ($probeOutput.Count -eq 0) { throw '便携包版本切换 probe 没有输出结果' }
    if ((Get-PortableTreeSeal $previousRoot) -ne $previousTreeBefore -or
        (Get-PortableTreeSeal $currentRoot) -ne $currentTreeBefore) {
        throw '版本切换期间便携程序目录发生变化，程序与数据未保持分离'
    }
    $result = $probeOutput[-1] | ConvertFrom-Json
    foreach ($field in @(
            'programDataSeparated',
            'factsSurvivedTransition',
            'backupVerified',
            'restoreAppliedOnRestart',
            'failedRestoreKeptCurrent',
            'failedRestoreRecorded',
            'lockedRestoreKeptCurrent',
            'lockedRestoreRecorded',
            'landingRestoreKeptCurrent',
            'landingRestoreRecorded',
            'landingRestoreBlockedByOS',
            'continuityFailedClosed',
            'continuityPendingRetained',
            'continuityRecovered',
            'continuityBlockedByOS',
            'finalizeResumeKeptCurrent',
            'finalizeResumeRevokedAuth',
            'finalizeResumeCompleted',
            'outcomeWriteFailedClosed',
            'outcomeWriteRetained',
            'outcomeWriteRecovered',
            'pendingDeleteFailedClosed',
            'pendingDeleteRetained',
            'pendingDeleteRecovered',
            'pendingDeleteBlockedByOS',
            'doubleRenameFailedClosed',
            'doubleRenameRetained',
            'doubleRenameRecovered',
            'doubleRenameBlockedByOS',
            'allStopsExitedGracefully'
        )) {
        if ($result.$field -ne $true) { throw "版本切换结果未通过：$field" }
    }
    if ($result.previousVersion -ne $PreviousVersion -or $result.currentVersion -ne $CurrentVersion -or
        $result.backupAppVersion -ne $PreviousVersion) {
        throw '版本切换结果的产品版本身份不一致'
    }

    [pscustomobject]@{
        PreviousVersion = $result.previousVersion
        CurrentVersion = $result.currentVersion
        BackupAppVersion = $result.backupAppVersion
        BackupSchemaVersion = $result.backupSchemaVersion
        RestoreWillMigrate = $result.restoreWillMigrate
        ProgramDataSeparated = 'passed'
        FactsSurvivedTransition = 'passed'
        BackupVerified = 'passed'
        RestoreAppliedOnRestart = 'passed'
        FailedRestoreKeptCurrent = 'passed'
        FailedRestoreRecorded = 'passed'
        LockedRestoreKeptCurrent = 'passed'
        LockedRestoreRecorded = 'passed'
        LandingRestoreKeptCurrent = 'passed'
        LandingRestoreRecorded = 'passed'
        LandingRestoreBlockedByOS = 'passed'
        ContinuityFailedClosed = 'passed'
        ContinuityPendingRetained = 'passed'
        ContinuityRecovered = 'passed'
        ContinuityBlockedByOS = 'passed'
        FinalizeResumeKeptCurrent = 'passed'
        FinalizeResumeRevokedAuth = 'passed'
        FinalizeResumeCompleted = 'passed'
        OutcomeWriteFailedClosed = 'passed'
        OutcomeWriteRetained = 'passed'
        OutcomeWriteRecovered = 'passed'
        PendingDeleteFailedClosed = 'passed'
        PendingDeleteRetained = 'passed'
        PendingDeleteRecovered = 'passed'
        PendingDeleteBlockedByOS = 'passed'
        DoubleRenameFailedClosed = 'passed'
        DoubleRenameRetained = 'passed'
        DoubleRenameRecovered = 'passed'
        DoubleRenameBlockedByOS = 'passed'
        AllStopsExitedGracefully = 'passed'
    }
} finally {
    Remove-SafeTemp $tempRoot
}
