[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$ArchivePath,
    [Parameter(Mandatory)]
    [string]$ExpectedVersion,
    [switch]$RequireAuthenticode,
    [switch]$AllowLegacyManifest
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
. (Join-Path $PSScriptRoot 'WindowsHistoricalCompatibility.ps1')
$historicalCompatibility = Get-GalleryHistoricalCompatibility $repoRoot

if (-not [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform(
        [System.Runtime.InteropServices.OSPlatform]::Windows)) {
    throw 'Windows 便携制品 smoke 必须在 Windows 上执行'
}

$archive = (Resolve-Path -LiteralPath $ArchivePath).Path
$archiveChecksumPath = "$archive.sha256"
$tempParent = [System.IO.Path]::GetTempPath()
$tempRoot = Join-Path $tempParent ('gallery-release-verify-' + [guid]::NewGuid().ToString('N'))
$extractRoot = Join-Path $tempRoot 'package'
$appRoot = Join-Path $tempRoot 'appdirs'
New-Item -ItemType Directory -Path $extractRoot -Force | Out-Null

function Remove-SafeTemp([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) { return }
    $full = [System.IO.Path]::GetFullPath($Path)
    $parent = [System.IO.Path]::GetFullPath($tempParent)
    $relative = [System.IO.Path]::GetRelativePath($parent, $full)
    if ([System.IO.Path]::IsPathRooted($relative) -or -not $relative.StartsWith('gallery-release-verify-')) {
        throw "拒绝删除非验证临时目录：$full"
    }
    Remove-Item -LiteralPath $full -Recurse -Force
}

function Assert-Checksums([string]$PackageRoot) {
    $checksumPath = Join-Path $PackageRoot 'SHA256SUMS'
    $declared = @{}
    foreach ($line in Get-Content -LiteralPath $checksumPath) {
        if ($line -notmatch '^([0-9a-f]{64})  (.+)$') { throw "非法 SHA256SUMS 行：$line" }
        $relative = $Matches[2]
        if ($relative.Contains('..') -or [System.IO.Path]::IsPathRooted($relative)) {
            throw "SHA256SUMS 包含越界路径：$relative"
        }
        $path = Join-Path $PackageRoot ($relative.Replace('/', [System.IO.Path]::DirectorySeparatorChar))
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "SHA256SUMS 引用缺失文件：$relative" }
        $actual = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $Matches[1]) { throw "文件摘要不匹配：$relative" }
        $declared[$relative] = $true
    }
    $actualFiles = Get-ChildItem -LiteralPath $PackageRoot -File -Recurse |
        Where-Object { $_.FullName -ne $checksumPath } |
        ForEach-Object { [System.IO.Path]::GetRelativePath($PackageRoot, $_.FullName).Replace('\', '/') }
    foreach ($relative in $actualFiles) {
        if (-not $declared.ContainsKey($relative)) { throw "SHA256SUMS 漏记文件：$relative" }
    }
}

function Start-IsolatedGalleryd([string]$Executable, [string]$Root, [string]$PreviousNonce = '') {
    $stdout = Join-Path $tempRoot ('galleryd-' + [guid]::NewGuid().ToString('N') + '.stdout.log')
    $stderr = Join-Path $tempRoot ('galleryd-' + [guid]::NewGuid().ToString('N') + '.stderr.log')
    $process = Start-Process -FilePath $Executable -ArgumentList @('-mode=personal', '-listen=127.0.0.1:0', "-app-root=$Root") `
        -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru
    $descriptorPath = Join-Path $Root 'run\galleryd.json'
    $deadline = [DateTimeOffset]::UtcNow.AddSeconds(60)
    while ([DateTimeOffset]::UtcNow -lt $deadline) {
        $process.Refresh()
        if ($process.HasExited) {
            throw "galleryd 在发布 descriptor 前退出，exitCode=$($process.ExitCode)"
        }
        if (Test-Path -LiteralPath $descriptorPath -PathType Leaf) {
            try {
                $descriptor = Get-Content -Raw -LiteralPath $descriptorPath | ConvertFrom-Json
                if ($descriptor.address -and $descriptor.startupNonce -and $descriptor.startupNonce -ne $PreviousNonce) {
                    return [pscustomobject]@{ Process = $process; Descriptor = $descriptor }
                }
            } catch {
                # descriptor 使用原子替换；仅忽略尚未读到完整 JSON 的瞬间。
            }
        }
        Start-Sleep -Milliseconds 100
    }
    Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
    throw '60 秒内未观察到 galleryd runtime descriptor'
}

$running = $null
try {
    if (-not (Test-Path -LiteralPath $archiveChecksumPath -PathType Leaf)) {
        throw "便携制品缺少外部摘要：$archiveChecksumPath"
    }
    $archiveChecksumLine = (Get-Content -Raw -LiteralPath $archiveChecksumPath).Trim()
    if ($archiveChecksumLine -notmatch '^([0-9a-f]{64})  ([^\\/]+\.zip)$') {
        throw "非法外部制品摘要：$archiveChecksumLine"
    }
    if ($Matches[2] -ne [System.IO.Path]::GetFileName($archive)) {
        throw '外部摘要登记的制品文件名不匹配'
    }
    $actualArchiveHash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualArchiveHash -ne $Matches[1]) { throw '便携 ZIP 外部摘要不匹配' }

    $zip = [System.IO.Compression.ZipFile]::OpenRead($archive)
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
    Expand-Archive -LiteralPath $archive -DestinationPath $extractRoot
    $roots = @(Get-ChildItem -LiteralPath $extractRoot -Directory)
    if ($roots.Count -ne 1) { throw '便携 ZIP 必须只有一个顶层目录' }
    $packageRoot = $roots[0].FullName

    Assert-Checksums $packageRoot
    $manifest = Get-Content -Raw -LiteralPath (Join-Path $packageRoot 'release-manifest.json') | ConvertFrom-Json -Depth 100
    if ($manifest.version -ne $ExpectedVersion) { throw '发行清单版本不匹配' }
    $isLegacyManifest = $manifest.schemaVersion -eq 1
    if ($manifest.schemaVersion -ne 2 -and (-not $isLegacyManifest -or -not $AllowLegacyManifest)) {
        throw '发行清单 schemaVersion 不受支持；旧版 schemaVersion=1 仅可通过 -AllowLegacyManifest 显式复验'
    }
    if ($manifest.target.os -ne 'windows' -or $manifest.target.arch -ne 'amd64' -or -not $manifest.web.embedded) {
        throw '发行清单目标或 Web 嵌入声明不正确'
    }
    $manifestCurrentControlSchema = $null
    $manifestMinimumSupportedControlSchema = $null
    if (-not $isLegacyManifest) {
        $expectedHistoricalSchemas = @($historicalCompatibility.Baselines | ForEach-Object { $_.Schema })
        $manifestHistoricalSchemas = @($manifest.dataCompatibility.verifiedHistoricalControlSchemas)
        if ($manifest.dataCompatibility.currentControlSchema -ne $historicalCompatibility.CurrentControlSchema -or
            $manifest.dataCompatibility.minimumSupportedControlSchema -ne $historicalCompatibility.MinimumSupportedControlSchema -or
            ($manifestHistoricalSchemas -join ',') -ne ($expectedHistoricalSchemas -join ',')) {
            throw '发行清单的 control schema 支持矩阵与当前门禁不一致'
        }
        $manifestCurrentControlSchema = $manifest.dataCompatibility.currentControlSchema
        $manifestMinimumSupportedControlSchema = $manifest.dataCompatibility.minimumSupportedControlSchema
    }
    if (@($manifest.sboms).Count -ne 3) { throw '发行清单必须登记两个 Go 与一个 Web SBOM' }
    foreach ($sbom in $manifest.sboms) {
        $sbomPath = Join-Path $packageRoot ($sbom.path.Replace('/', [System.IO.Path]::DirectorySeparatorChar))
        $document = Get-Content -Raw -LiteralPath $sbomPath | ConvertFrom-Json -Depth 100
        if ($document.bomFormat -ne 'CycloneDX' -or $document.specVersion -ne $sbom.specVersion) {
            throw "SBOM 格式或规范版本与清单不一致：$($sbom.path)"
        }
        if (@($document.components).Count -eq 0) { throw "SBOM 没有登记任何组件：$($sbom.path)" }
    }

    $galleryd = Join-Path $packageRoot 'galleryd.exe'
    $galleryctl = Join-Path $packageRoot 'galleryctl.exe'
    if ((& $galleryd version).Trim() -ne "galleryd $ExpectedVersion") { throw 'galleryd version 与清单不一致' }
    if ((& $galleryctl version).Trim() -ne "galleryctl $ExpectedVersion") { throw 'galleryctl version 与清单不一致' }

    $actualSignatureStatuses = [ordered]@{}
    foreach ($binary in @($galleryd, $galleryctl)) {
        $status = (Get-AuthenticodeSignature -FilePath $binary).Status.ToString()
        $actualSignatureStatuses[[System.IO.Path]::GetFileName($binary)] = $status
        if ($RequireAuthenticode -and $status -ne 'Valid') {
            throw "制品缺少有效 Authenticode：$([System.IO.Path]::GetFileName($binary)) status=$status"
        }
    }
    switch ($manifest.signature.status) {
        'valid' {
            if (@($actualSignatureStatuses.Values | Where-Object { $_ -ne 'Valid' }).Count -ne 0) {
                throw '发行清单声明已签名，但实际 Authenticode 状态不一致'
            }
        }
        'unsigned' {
            if (@($actualSignatureStatuses.Values | Where-Object { $_ -ne 'NotSigned' }).Count -ne 0) {
                throw '发行清单声明未签名，但实际 Authenticode 状态不一致'
            }
        }
        default { throw "发行清单包含不受支持的签名状态：$($manifest.signature.status)" }
    }
    foreach ($binary in @($galleryd, $galleryctl)) {
        $name = [System.IO.Path]::GetFileName($binary)
        $artifact = @($manifest.artifacts | Where-Object { $_.path -eq $name })
        if ($artifact.Count -ne 1) { throw "发行清单必须且只能登记一次二进制：$name" }
        $actualHash = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash.ToLowerInvariant()
        $actualSize = (Get-Item -LiteralPath $binary).Length
        if ($artifact[0].sha256 -ne $actualHash -or $artifact[0].size -ne $actualSize -or
            $artifact[0].authenticodeStatus -ne $actualSignatureStatuses[$name]) {
            throw "发行清单二进制事实与实际文件不一致：$name"
        }
    }

    $running = Start-IsolatedGalleryd $galleryd $appRoot
    $firstNonce = $running.Descriptor.startupNonce
    $baseURL = "http://$($running.Descriptor.address)"
    $health = Invoke-RestMethod -Uri "$baseURL/api/v1/health" -TimeoutSec 15
    if ($health.apiVersion -ne $manifest.web.apiVersion) { throw '实际 health API 版本与发行清单不一致' }
    $web = Invoke-RestMethod -Uri "$baseURL/gallery-web.json" -TimeoutSec 15
    if ($web.webVersion -ne $manifest.web.webVersion -or $web.contractVersion -ne $manifest.web.contractVersion -or $web.apiVersion -ne $manifest.web.apiVersion) {
        throw '实际内嵌 Web manifest 与发行清单不一致'
    }
    $shell = Invoke-WebRequest -Uri "$baseURL/" -TimeoutSec 15
    if ($shell.StatusCode -ne 200 -or $shell.Content -notmatch '<div id="root"></div>') { throw '内嵌用户前端静态壳不可用' }

    Stop-Process -Id $running.Process.Id -Force
    $running.Process.WaitForExit(10000) | Out-Null
    $running = Start-IsolatedGalleryd $galleryd $appRoot $firstNonce
    $baseURL = "http://$($running.Descriptor.address)"
    $recoveredHealth = Invoke-RestMethod -Uri "$baseURL/api/v1/health" -TimeoutSec 15
    if ($recoveredHealth.apiVersion -ne $manifest.web.apiVersion) { throw '强杀后同 AppDirs 重启 health 失败' }

    [pscustomobject]@{
        ArchivePath = $archive
        Version = $manifest.version
        Commit = $manifest.source.commit
        CurrentControlSchema = $manifestCurrentControlSchema
        MinimumSupportedControlSchema = $manifestMinimumSupportedControlSchema
        Authenticode = $manifest.signature.status
        Checksums = 'passed'
        EmbeddedWeb = 'passed'
        ForcedRestartRecovery = 'passed'
    }
} finally {
    if ($null -ne $running -and -not $running.Process.HasExited) {
        Stop-Process -Id $running.Process.Id -Force -ErrorAction SilentlyContinue
        $running.Process.WaitForExit(10000) | Out-Null
    }
    Remove-SafeTemp $tempRoot
}
