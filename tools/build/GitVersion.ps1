function Get-GalleryBuildVersion([string]$RepoRoot) {
    $version = & node (Join-Path $RepoRoot 'web\scripts\build-version.mjs')
    if ($LASTEXITCODE -ne 0) { throw '读取 Git 构建版本失败。' }
    return $version.Trim()
}
