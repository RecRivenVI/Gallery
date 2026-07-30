[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$Version,

    [string]$OutputDirectory = (Join-Path $PSScriptRoot '..\dist\release'),
    [string]$GoExecutable = $env:GALLERY_GO,
    [string]$NpmExecutable = '',
    [string]$CycloneDXGoModExecutable = $env:GALLERY_CYCLONEDX_GOMOD,
    [ValidateRange(1, 64)]
    [int]$MaxProcessors = 2,
    [string]$SigningCertificateThumbprint = '',
    [string]$TimestampServer = '',
    [switch]$RequireAuthenticode,
    [switch]$SkipWebBuild,
    [switch]$AllowDirty
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$semVerPattern = '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$'
if ($Version -notmatch $semVerPattern) {
    throw "Version 必须是完整 SemVer，实际为：$Version"
}

if (-not [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform(
        [System.Runtime.InteropServices.OSPlatform]::Windows)) {
    throw 'Windows 便携包必须在 Windows 上构建和执行制品 smoke'
}

$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
. (Join-Path $PSScriptRoot 'WindowsHistoricalCompatibility.ps1')
$historicalCompatibility = Get-GalleryHistoricalCompatibility $repoRoot
$outputRoot = [System.IO.Path]::GetFullPath($OutputDirectory, $repoRoot)
$safeVersion = $Version -replace '[^0-9A-Za-z._-]', '_'
$artifactBase = "Gallery-$safeVersion-windows-x64"
$archivePath = Join-Path $outputRoot "$artifactBase.zip"
$archiveChecksumPath = "$archivePath.sha256"

function Resolve-Executable([string]$Configured, [string]$CommandName) {
    if ($Configured) {
        return (Resolve-Path -LiteralPath $Configured).Path
    }
    return (Get-Command $CommandName -ErrorAction Stop).Source
}

function Remove-SafeTree([string]$Path, [string]$Parent) {
    if (-not (Test-Path -LiteralPath $Path)) { return }
    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $fullParent = [System.IO.Path]::GetFullPath($Parent)
    $relative = [System.IO.Path]::GetRelativePath($fullParent, $fullPath)
    if ([System.IO.Path]::IsPathRooted($relative) -or $relative -eq '..' -or $relative.StartsWith("..$([System.IO.Path]::DirectorySeparatorChar)")) {
        throw "拒绝删除输出目录之外的路径：$fullPath"
    }
    Remove-Item -LiteralPath $fullPath -Recurse -Force
}

function Invoke-Checked([string]$Description, [scriptblock]$Command) {
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Description 失败，退出码 $LASTEXITCODE"
    }
}

function Get-RelativeUnixPath([string]$Base, [string]$Path) {
    return [System.IO.Path]::GetRelativePath($Base, $Path).Replace('\', '/')
}

$git = Resolve-Executable '' 'git'
$go = Resolve-Executable $GoExecutable 'go'
$npm = Resolve-Executable $NpmExecutable 'npm'
$cycloneDXGoMod = Resolve-Executable $CycloneDXGoModExecutable 'cyclonedx-gomod'

$commit = (& $git -C $repoRoot rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $commit -notmatch '^[0-9a-f]{40}$') { throw '无法读取精确 Git commit' }
$commitTime = (& $git -C $repoRoot show -s --format=%cI HEAD).Trim()
if ($LASTEXITCODE -ne 0) { throw '无法读取 Git commit 时间' }
$dirty = @(& $git -C $repoRoot status --porcelain=v1 --untracked-files=all)
if ($LASTEXITCODE -ne 0) { throw '无法读取 Git 工作树状态' }
if ($dirty.Count -gt 0 -and -not $AllowDirty) {
    throw '正式便携包只允许从干净工作树构建；开发验证可显式使用 -AllowDirty，制品清单会记录 dirty=true'
}

$goVersion = (& $go version).Trim()
if ($LASTEXITCODE -ne 0 -or $goVersion -notmatch '\bgo1\.26\.5\b') {
    throw "正式构建要求 Go 1.26.5，实际为：$goVersion"
}
$nodeVersion = (& (Get-Command node -ErrorAction Stop).Source --version).Trim()
$npmVersion = (& $npm --version).Trim()

New-Item -ItemType Directory -Path $outputRoot -Force | Out-Null
$stageRoot = Join-Path $outputRoot ('.stage-' + [guid]::NewGuid().ToString('N'))
$packageRoot = Join-Path $stageRoot $artifactBase
$sbomRoot = Join-Path $packageRoot 'sbom'
New-Item -ItemType Directory -Path $sbomRoot -Force | Out-Null

$previousEnvironment = @{
    CGO_ENABLED = $env:CGO_ENABLED
    GOOS = $env:GOOS
    GOARCH = $env:GOARCH
    GOMAXPROCS = $env:GOMAXPROCS
    GOFLAGS = $env:GOFLAGS
    GOTOOLCHAIN = $env:GOTOOLCHAIN
    PATH = $env:PATH
}

try {
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:GOMAXPROCS = [string]$MaxProcessors
    $env:GOFLAGS = '-p=1'
    $env:GOTOOLCHAIN = 'local'
    $env:PATH = "$(Split-Path -Parent $go);$env:PATH"

    if (-not $SkipWebBuild) {
        Push-Location (Join-Path $repoRoot 'web')
        try {
            Invoke-Checked 'npm ci' { & $npm ci }
            Invoke-Checked 'Web 生产构建' { & $npm run build }
        } finally {
            Pop-Location
        }
        $generatedChanges = @(& $git -C $repoRoot status --porcelain=v1 -- internal/webapp/dist web/src/api/schema.gen.ts web/src/design/icons.gen.ts web/src/manage/ruleSchemaValidator.gen.cjs)
        if ($LASTEXITCODE -ne 0) { throw '无法核对 Web 生成资产状态' }
        if ($generatedChanges.Count -gt 0) {
            throw "Web 生产资产与当前提交不一致：`n$($generatedChanges -join "`n")"
        }
    }

    $webManifestPath = Join-Path $repoRoot 'internal\webapp\dist\gallery-web.json'
    if (-not (Test-Path -LiteralPath $webManifestPath -PathType Leaf)) {
        throw '缺少内嵌 Web manifest；请先构建 Web'
    }
    $webManifest = Get-Content -Raw -LiteralPath $webManifestPath | ConvertFrom-Json
    foreach ($field in @('webVersion', 'contractVersion', 'apiVersion')) {
        if (-not $webManifest.$field) { throw "gallery-web.json 缺少 $field" }
    }

    $linkerFlags = "-s -w -X github.com/RecRivenVI/gallery/pkg/galleryversion.Version=$Version"
    $gallerydPath = Join-Path $packageRoot 'galleryd.exe'
    $galleryctlPath = Join-Path $packageRoot 'galleryctl.exe'
    Push-Location $repoRoot
    try {
        Invoke-Checked '构建 galleryd.exe' {
            & $go build -trimpath -buildvcs=true -ldflags $linkerFlags -o $gallerydPath ./cmd/galleryd
        }
        Invoke-Checked '构建 galleryctl.exe' {
            & $go build -trimpath -buildvcs=true -ldflags $linkerFlags -o $galleryctlPath ./cmd/galleryctl
        }
    } finally {
        Pop-Location
    }

    $gallerydVersion = (& $gallerydPath version).Trim()
    if ($LASTEXITCODE -ne 0 -or $gallerydVersion -ne "galleryd $Version") {
        throw "galleryd 版本注入失败：$gallerydVersion"
    }
    $galleryctlVersion = (& $galleryctlPath version).Trim()
    if ($LASTEXITCODE -ne 0 -or $galleryctlVersion -ne "galleryctl $Version") {
        throw "galleryctl 版本注入失败：$galleryctlVersion"
    }

    $goSboms = @(
        @{ Main = 'cmd/galleryd'; Output = (Join-Path $sbomRoot 'galleryd.cdx.json') },
        @{ Main = 'cmd/galleryctl'; Output = (Join-Path $sbomRoot 'galleryctl.cdx.json') }
    )
    foreach ($item in $goSboms) {
        Invoke-Checked "生成 $($item.Main) CycloneDX SBOM" {
            & $cycloneDXGoMod app -json -output-version 1.6 -noserial -notimestamp -licenses -main $item.Main -output $item.Output $repoRoot
        }
        $bom = Get-Content -Raw -LiteralPath $item.Output | ConvertFrom-Json -Depth 100
        if ($bom.bomFormat -ne 'CycloneDX' -or $bom.specVersion -ne '1.6') {
            throw "$($item.Main) SBOM 不是 CycloneDX 1.6"
        }
        if ($null -ne $bom.metadata.component) {
            $bom.metadata.component.version = $Version
        }
        $bom | ConvertTo-Json -Depth 100 | Set-Content -LiteralPath $item.Output -Encoding utf8NoBOM
    }

    $webSbomPath = Join-Path $sbomRoot 'web.cdx.json'
    Push-Location (Join-Path $repoRoot 'web')
    try {
        $webSbomJSON = & $npm sbom --package-lock-only --omit=dev --sbom-format=cyclonedx --sbom-type=application
        if ($LASTEXITCODE -ne 0) { throw "npm sbom 失败，退出码 $LASTEXITCODE" }
        ($webSbomJSON -join "`n") | Set-Content -LiteralPath $webSbomPath -Encoding utf8NoBOM
    } finally {
        Pop-Location
    }
    $webSbom = Get-Content -Raw -LiteralPath $webSbomPath | ConvertFrom-Json -Depth 100
    if ($webSbom.bomFormat -ne 'CycloneDX' -or $webSbom.specVersion -notin @('1.5', '1.6')) {
        throw "Web SBOM 不是受支持的 CycloneDX 规范：$($webSbom.specVersion)"
    }
    # npm 10 的标准 sbom 命令会写入随机 serialNumber 和当前时间。依赖事实全部来自锁文件，
    # 因此移除这两个非事实字段，使同一提交/工具链生成的 Web SBOM 可以逐字节复核。
    $webSbom.PSObject.Properties.Remove('serialNumber')
    if ($null -ne $webSbom.metadata) { $webSbom.metadata.PSObject.Properties.Remove('timestamp') }
    $webSbom | ConvertTo-Json -Depth 100 | Set-Content -LiteralPath $webSbomPath -Encoding utf8NoBOM

    Copy-Item -LiteralPath (Join-Path $repoRoot 'LICENSE') -Destination (Join-Path $packageRoot 'LICENSE')
    Copy-Item -LiteralPath (Join-Path $repoRoot 'THIRD_PARTY_NOTICES.md') -Destination (Join-Path $packageRoot 'THIRD_PARTY_NOTICES.md')

    $signatureStatuses = [ordered]@{}
    if ($SigningCertificateThumbprint) {
        if (-not $TimestampServer) { throw '使用代码签名证书时必须显式提供 TimestampServer' }
        $normalizedThumbprint = $SigningCertificateThumbprint.Replace(' ', '').ToUpperInvariant()
        $certificatePath = "Cert:\CurrentUser\My\$normalizedThumbprint"
        $certificate = Get-Item -LiteralPath $certificatePath -ErrorAction Stop
        if (-not $certificate.HasPrivateKey) { throw '代码签名证书没有可用私钥' }
        foreach ($binary in @($gallerydPath, $galleryctlPath)) {
            $signed = Set-AuthenticodeSignature -FilePath $binary -Certificate $certificate -HashAlgorithm SHA256 -TimestampServer $TimestampServer
            if ($signed.Status -ne [System.Management.Automation.SignatureStatus]::Valid) {
                throw "Authenticode 签名失败：$([System.IO.Path]::GetFileName($binary)) status=$($signed.Status)"
            }
        }
    }
    foreach ($binary in @($gallerydPath, $galleryctlPath)) {
        $status = (Get-AuthenticodeSignature -FilePath $binary).Status.ToString()
        $signatureStatuses[[System.IO.Path]::GetFileName($binary)] = $status
        if ($RequireAuthenticode -and $status -ne 'Valid') {
            throw "正式制品要求有效 Authenticode：$([System.IO.Path]::GetFileName($binary)) status=$status"
        }
    }
    $nonValidSignatures = @($signatureStatuses.Values | Where-Object { $_ -ne 'Valid' })
    $unsignedSignatures = @($signatureStatuses.Values | Where-Object { $_ -eq 'NotSigned' })
    $overallSignature = if ($nonValidSignatures.Count -eq 0) {
        'valid'
    } elseif ($unsignedSignatures.Count -eq $signatureStatuses.Count) {
        'unsigned'
    } else {
        'invalid'
    }
    if ($overallSignature -eq 'invalid') {
        throw "制品包含无效而非单纯缺失的 Authenticode 签名：$($signatureStatuses | ConvertTo-Json -Compress)"
    }
    $releaseStatus = if ($overallSignature -eq 'valid') { '已通过 Authenticode 制品签名检查；其它 RC 门禁仍以项目状态为准' } else { '未签名开发测试制品，不是正式 RC' }

    $portableReadme = Get-Content -Raw -LiteralPath (Join-Path $repoRoot 'packaging\windows-portable\README.zh-CN.md')
    $portableReadme = $portableReadme.Replace('{{VERSION}}', $Version).Replace('{{COMMIT}}', $commit).Replace(
        '{{RELEASE_STATUS}}', $releaseStatus).Replace('{{AUTHENTICODE_STATUS}}', $overallSignature).Replace(
        '{{MINIMUM_CONTROL_SCHEMA}}', [string]$historicalCompatibility.MinimumSupportedControlSchema).Replace(
        '{{CURRENT_CONTROL_SCHEMA}}', [string]$historicalCompatibility.CurrentControlSchema)
    $portableReadme | Set-Content -LiteralPath (Join-Path $packageRoot 'README.zh-CN.md') -Encoding utf8NoBOM

    $manifestArtifacts = foreach ($binary in @($gallerydPath, $galleryctlPath)) {
        [ordered]@{
            path = Get-RelativeUnixPath $packageRoot $binary
            size = (Get-Item -LiteralPath $binary).Length
            sha256 = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash.ToLowerInvariant()
            authenticodeStatus = $signatureStatuses[[System.IO.Path]::GetFileName($binary)]
        }
    }
    $manifest = [ordered]@{
        schemaVersion = 2
        product = 'Gallery'
        version = $Version
        target = [ordered]@{ os = 'windows'; arch = 'amd64'; format = 'portable-zip'; cgoEnabled = $false }
        source = [ordered]@{ commit = $commit; commitTime = $commitTime; dirty = ($dirty.Count -gt 0) }
        toolchain = [ordered]@{ go = $goVersion; node = $nodeVersion; npm = $npmVersion; maxProcessors = $MaxProcessors }
        web = [ordered]@{
            webVersion = $webManifest.webVersion
            contractVersion = $webManifest.contractVersion
            apiVersion = $webManifest.apiVersion
            embedded = $true
        }
        dataCompatibility = [ordered]@{
            currentControlSchema = $historicalCompatibility.CurrentControlSchema
            minimumSupportedControlSchema = $historicalCompatibility.MinimumSupportedControlSchema
            verifiedHistoricalControlSchemas = @($historicalCompatibility.Baselines | ForEach-Object { $_.Schema })
        }
        signature = [ordered]@{ status = $overallSignature; required = [bool]$RequireAuthenticode }
        artifacts = @($manifestArtifacts)
        sboms = @(
            [ordered]@{ path = 'sbom/galleryd.cdx.json'; format = 'CycloneDX'; specVersion = '1.6' },
            [ordered]@{ path = 'sbom/galleryctl.cdx.json'; format = 'CycloneDX'; specVersion = '1.6' },
            [ordered]@{ path = 'sbom/web.cdx.json'; format = 'CycloneDX'; specVersion = $webSbom.specVersion }
        )
        limitations = @(
            '便携无壳形态；不含安装器、自动更新器、桌面生命周期或 CredentialStore 集成',
            $(if ($overallSignature -eq 'valid') { 'Authenticode 已验证；仍不代表其它 RC 门禁通过' } else { 'Authenticode 未签名；仅限开发测试' })
        )
    }
    $manifest | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath (Join-Path $packageRoot 'release-manifest.json') -Encoding utf8NoBOM

    $checksumLines = Get-ChildItem -LiteralPath $packageRoot -File -Recurse |
        Sort-Object FullName |
        ForEach-Object {
            $relative = Get-RelativeUnixPath $packageRoot $_.FullName
            $hash = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
            "$hash  $relative"
        }
    $checksumLines | Set-Content -LiteralPath (Join-Path $packageRoot 'SHA256SUMS') -Encoding ascii

    if (Test-Path -LiteralPath $archivePath) { Remove-Item -LiteralPath $archivePath -Force }
    if (Test-Path -LiteralPath $archiveChecksumPath) { Remove-Item -LiteralPath $archiveChecksumPath -Force }
    Compress-Archive -LiteralPath $packageRoot -DestinationPath $archivePath -CompressionLevel Optimal
    $archiveHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    "$archiveHash  $([System.IO.Path]::GetFileName($archivePath))" |
        Set-Content -LiteralPath $archiveChecksumPath -Encoding ascii
    [pscustomobject]@{
        ArchivePath = $archivePath
        ArchiveChecksumPath = $archiveChecksumPath
        SHA256 = $archiveHash
        Version = $Version
        Commit = $commit
        Authenticode = $overallSignature
        Dirty = ($dirty.Count -gt 0)
    }
} finally {
    foreach ($name in $previousEnvironment.Keys) {
        $value = $previousEnvironment[$name]
        if ($null -eq $value) { Remove-Item "Env:$name" -ErrorAction SilentlyContinue }
        else { Set-Item "Env:$name" $value }
    }
    Remove-SafeTree $stageRoot $outputRoot
}
