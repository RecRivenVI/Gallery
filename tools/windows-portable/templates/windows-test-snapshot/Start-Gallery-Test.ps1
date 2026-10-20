[CmdletBinding()]
param(
    [ValidateRange(1024, 65535)]
    [int]$Port = 18080,
    [string]$AppRoot = '',
    [string]$PixivRoot = '',
    [switch]$SkipPixivPreset,
    [switch]$NoBrowser
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$galleryd = Join-Path $PSScriptRoot 'galleryd.exe'
if (-not (Test-Path -LiteralPath $galleryd -PathType Leaf)) {
    throw "当前目录缺少 galleryd.exe：$galleryd"
}
if (-not $AppRoot) {
    $packageName = (Split-Path -Leaf $PSScriptRoot) -replace '[^0-9A-Za-z._-]', '_'
    $AppRoot = Join-Path $env:LOCALAPPDATA (Join-Path 'Gallery-Test' $packageName)
}
$AppRoot = [System.IO.Path]::GetFullPath($AppRoot)
$listen = "127.0.0.1:$Port"
$url = "http://$listen/"
$env:GOMAXPROCS = '2'
$env:GOMEMLIMIT = '1536MiB'
$initializer = Join-Path $PSScriptRoot 'Initialize-Pixiv-Preset.ps1'
$ruleFile = Join-Path $PSScriptRoot 'presets\pixiv.rules.json'
$pixivRootFile = Join-Path $PSScriptRoot 'presets\pixiv-root.local.txt'

if (-not $SkipPixivPreset) {
    if (-not (Test-Path -LiteralPath $initializer -PathType Leaf)) {
        throw "测试包缺少 Pixiv 初始化脚本：$initializer"
    }
    if (-not (Test-Path -LiteralPath $ruleFile -PathType Leaf)) {
        throw "测试包缺少 Pixiv 规则：$ruleFile"
    }
    if (-not $PixivRoot -and (Test-Path -LiteralPath $pixivRootFile -PathType Leaf)) {
        $PixivRoot = (Get-Content -Raw -LiteralPath $pixivRootFile).Trim()
    }
    if (-not $PixivRoot) {
        throw '没有配置 Pixiv Source 根目录；可用 -PixivRoot 指定，或用 -SkipPixivPreset 跳过预置。'
    }
    if (-not (Test-Path -LiteralPath $PixivRoot -PathType Container)) {
        throw "Pixiv Source 根目录不存在；可用 -PixivRoot 指定，或用 -SkipPixivPreset 跳过预置。"
    }
    $PixivRoot = [System.IO.Path]::GetFullPath($PixivRoot)
}

Write-Host "Gallery 测试实例：$url"
Write-Host "隔离 AppDirs：$AppRoot"
Write-Host '资源预算：GOMAXPROCS=2，GOMEMLIMIT=1536MiB'
Write-Host '用户前端：/    管理前端：/manage'
if (-not $SkipPixivPreset) {
    Write-Host '首次启动将预置 Pixiv Library、只读 Source 与已暂停规则 Binding；不会启动扫描。'
    Write-Host "预置状态：$(Join-Path $AppRoot 'pixiv-preset-status.json')"
}
Write-Host '结束时按 Ctrl+C，并等待 galleryd_stopped。'

$opener = Start-Job -ScriptBlock {
    param(
        [string]$HealthURL,
        [string]$OpenURL,
        [bool]$PresetPixiv,
        [string]$Initializer,
        [string]$PresetAppRoot,
        [string]$PresetPixivRoot,
        [string]$PresetRuleFile,
        [bool]$OpenBrowser
    )
    $deadline = [DateTimeOffset]::UtcNow.AddSeconds(90)
    while ([DateTimeOffset]::UtcNow -lt $deadline) {
        try {
            $response = Invoke-WebRequest -Uri $HealthURL -TimeoutSec 2 -UseBasicParsing
            if ($response.StatusCode -eq 200) {
                if ($PresetPixiv) {
                    try {
                        & $Initializer `
                            -BaseURL $OpenURL `
                            -AppRoot $PresetAppRoot `
                            -PixivRoot $PresetPixivRoot `
                            -RuleFile $PresetRuleFile | Out-Null
                    } catch {
                        # 初始化脚本已把脱敏失败原因写入 AppRoot；仍打开前端供诊断。
                    }
                }
                if ($OpenBrowser) {
                    Start-Process $OpenURL
                }
                return
            }
        } catch {
            # 启动迁移尚未完成；继续等待 descriptor/HTTP 就绪。
        }
        Start-Sleep -Milliseconds 250
    }
} -ArgumentList "$($url)api/v1/health", $url, (-not $SkipPixivPreset), $initializer, $AppRoot, $PixivRoot, $ruleFile, (-not $NoBrowser)

try {
    & $galleryd -mode personal -listen $listen -app-root $AppRoot
    if ($LASTEXITCODE -ne 0) {
        throw "galleryd 退出码：$LASTEXITCODE"
    }
} finally {
    Stop-Job -Job $opener -ErrorAction SilentlyContinue
    Remove-Job -Job $opener -Force -ErrorAction SilentlyContinue
}
