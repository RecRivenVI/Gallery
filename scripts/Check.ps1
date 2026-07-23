[CmdletBinding()]
param(
    [switch]$Race
)

$ErrorActionPreference = 'Stop'

if ($env:GALLERY_GO) {
    $go = (Resolve-Path -LiteralPath $env:GALLERY_GO).Path
} else {
    $goCommand = Get-Command go -ErrorAction Stop
    $go = $goCommand.Source
}

$goBin = Split-Path -Parent $go
$env:PATH = "$goBin;$env:PATH"
$gofmtName = if ($IsWindows) { 'gofmt.exe' } else { 'gofmt' }
$gofmt = Join-Path $goBin $gofmtName

# 显式列出仓库自身的包集合：`npm ci` 之后 web/node_modules 中的第三方 Go 源码会进入
# `./...`，使 vet/test/build 的实际范围取决于 node_modules 是否存在（EV-39 的 BLD-1）。
$goPackages = @('./cmd/...', './internal/...', './pkg/...', './tools/...')

& $go mod tidy -diff
if ($LASTEXITCODE -ne 0) { throw 'go.mod/go.sum 不是 tidy 状态' }

$generatedPath = Join-Path $PSScriptRoot '..\pkg\galleryapi\openapi.gen.go'
$generatedBefore = (Get-FileHash -LiteralPath $generatedPath -Algorithm SHA256).Hash
& $go generate ./...
if ($LASTEXITCODE -ne 0) { throw 'go generate 失败' }
$generatedAfter = (Get-FileHash -LiteralPath $generatedPath -Algorithm SHA256).Hash
if ($generatedBefore -ne $generatedAfter) { throw 'OpenAPI 生成文件不是最新状态' }

if (-not $Race) {
    $webPath = Join-Path $PSScriptRoot '..\web'
    $node = Get-Command node -ErrorAction Stop
    $npm = Get-Command npm -ErrorAction Stop
    Write-Host "Node: $(& $node.Source --version); npm: $(& $npm.Source --version)"

    function Get-WebArtifactState {
        $artifactRoots = @(
            (Join-Path $webPath 'src\api\schema.gen.ts'),
            (Join-Path $PSScriptRoot '..\internal\webapp\dist')
        )
        $files = foreach ($artifactRoot in $artifactRoots) {
            if (Test-Path -LiteralPath $artifactRoot -PathType Leaf) {
                Get-Item -LiteralPath $artifactRoot
            } elseif (Test-Path -LiteralPath $artifactRoot -PathType Container) {
                Get-ChildItem -LiteralPath $artifactRoot -File -Recurse
            }
        }
        return ($files | Sort-Object FullName | ForEach-Object {
            "$($_.FullName)=$((Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash)"
        }) -join "`n"
    }

    $webGeneratedBefore = Get-WebArtifactState
    Push-Location $webPath
    try {
        & $npm.Source ci
        if ($LASTEXITCODE -ne 0) { throw 'npm ci 失败' }
        & $npm.Source run typecheck
        if ($LASTEXITCODE -ne 0) { throw 'Web TypeScript 检查失败' }
        & $npm.Source run lint
        if ($LASTEXITCODE -ne 0) { throw 'Web ESLint 检查失败' }
        & $npm.Source run format:check
        if ($LASTEXITCODE -ne 0) { throw 'Web Prettier 检查失败' }
        & $npm.Source test
        if ($LASTEXITCODE -ne 0) { throw 'Web 单元测试失败' }
        & $npm.Source run build
        if ($LASTEXITCODE -ne 0) { throw 'Web 生产构建失败' }
    } finally {
        Pop-Location
    }
    $webGeneratedAfter = Get-WebArtifactState
    if ($webGeneratedBefore -ne $webGeneratedAfter) { throw 'Web OpenAPI 或生产资产不是最新状态' }
}

$unformatted = & $gofmt -l cmd internal pkg tools
if ($LASTEXITCODE -ne 0) { throw 'gofmt 检查失败' }
if ($unformatted) { throw "以下文件尚未 gofmt：$($unformatted -join ', ')" }

& $go vet @goPackages
if ($LASTEXITCODE -ne 0) { throw 'go vet 失败' }

$previousCGO = $env:CGO_ENABLED
try {
    $env:CGO_ENABLED = '0'
    & $go test @goPackages
    if ($LASTEXITCODE -ne 0) { throw 'CGO_ENABLED=0 go test 失败' }
    & $go build ./cmd/...
    if ($LASTEXITCODE -ne 0) { throw 'CGO_ENABLED=0 go build 失败' }
} finally {
    $env:CGO_ENABLED = $previousCGO
}

if ($Race) {
    & $go test -race @goPackages
    if ($LASTEXITCODE -ne 0) { throw 'go test -race 失败' }
}
