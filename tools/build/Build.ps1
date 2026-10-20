[CmdletBinding()]
param(
    [string]$GoExecutable = '',
    [string]$OutputDirectory = '',
    [string]$NpmExecutable = 'npm'
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path
$localPath = Join-Path $PSScriptRoot 'local.json'
if (Test-Path -LiteralPath $localPath) {
    $local = Get-Content -Raw -LiteralPath $localPath | ConvertFrom-Json
    if (-not $GoExecutable) { $GoExecutable = $local.goExecutable }
    if (-not $OutputDirectory) { $OutputDirectory = $local.outputDirectory }
}
if (-not $GoExecutable) { $GoExecutable = 'go' }
if (-not $OutputDirectory) { throw '请指定仓库外 OutputDirectory，或在 tools/build/local.json 中配置。' }
$outputRoot = [System.IO.Path]::GetFullPath($OutputDirectory)
$relative = [System.IO.Path]::GetRelativePath($repoRoot, $outputRoot)
if (-not [System.IO.Path]::IsPathRooted($relative) -and $relative -ne '..' -and -not $relative.StartsWith('..\')) {
    throw '构建输出必须位于仓库之外。'
}

Push-Location $repoRoot
try {
    & $NpmExecutable --prefix web run build
    if ($LASTEXITCODE -ne 0) { throw 'Web 构建失败。' }
    $manifest = Get-Content -Raw -LiteralPath (Join-Path $repoRoot 'web\public\gallery-web.json') | ConvertFrom-Json
    [System.IO.Directory]::CreateDirectory($outputRoot) | Out-Null
    $flags = "-X github.com/RecRivenVI/gallery/internal/version.Version=$($manifest.webVersion)"
    foreach ($command in @('galleryd', 'galleryctl')) {
        & $GoExecutable build -ldflags $flags -o (Join-Path $outputRoot "$command.exe") "./cmd/$command"
        if ($LASTEXITCODE -ne 0) { throw "$command 构建失败。" }
    }
} finally {
    Pop-Location
}
