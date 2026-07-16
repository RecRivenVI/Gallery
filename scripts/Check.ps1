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

& $go mod tidy -diff
if ($LASTEXITCODE -ne 0) { throw 'go.mod/go.sum 不是 tidy 状态' }

$generatedPath = Join-Path $PSScriptRoot '..\internal\contract\api\openapi.gen.go'
$generatedBefore = (Get-FileHash -LiteralPath $generatedPath -Algorithm SHA256).Hash
& $go generate ./...
if ($LASTEXITCODE -ne 0) { throw 'go generate 失败' }
$generatedAfter = (Get-FileHash -LiteralPath $generatedPath -Algorithm SHA256).Hash
if ($generatedBefore -ne $generatedAfter) { throw 'OpenAPI 生成文件不是最新状态' }

$unformatted = & $gofmt -l cmd internal
if ($LASTEXITCODE -ne 0) { throw 'gofmt 检查失败' }
if ($unformatted) { throw "以下文件尚未 gofmt：$($unformatted -join ', ')" }

& $go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'go vet 失败' }

$previousCGO = $env:CGO_ENABLED
try {
    $env:CGO_ENABLED = '0'
    & $go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'CGO_ENABLED=0 go test 失败' }
    & $go build ./cmd/...
    if ($LASTEXITCODE -ne 0) { throw 'CGO_ENABLED=0 go build 失败' }
} finally {
    $env:CGO_ENABLED = $previousCGO
}

if ($Race) {
    & $go test -race ./...
    if ($LASTEXITCODE -ne 0) { throw 'go test -race 失败' }
}
