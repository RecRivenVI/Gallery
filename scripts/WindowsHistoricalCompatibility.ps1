function Get-GalleryControlSchemaVersion([string]$SourceRoot) {
    $migrationRoot = Join-Path $SourceRoot 'internal\storage\migrations\control'
    $versions = @(
        Get-ChildItem -LiteralPath $migrationRoot -Filter '*.sql' -File |
            ForEach-Object {
                if ($_.Name -notmatch '^(?<version>[0-9]{5})_.+\.sql$') {
                    throw "control migration 文件名不规范：$($_.Name)"
                }
                [int]$Matches.version
            } |
            Sort-Object
    )
    if ($versions.Count -eq 0) { throw '没有找到 control migration' }
    for ($index = 0; $index -lt $versions.Count; $index++) {
        if ($versions[$index] -ne ($index + 1)) {
            throw "control migration 版本不连续：index=$index version=$($versions[$index])"
        }
    }
    return $versions[-1]
}

function Get-GalleryHistoricalCompatibility([string]$RepoRoot, [string]$ManifestPath = '') {
    if (-not $ManifestPath) {
        $ManifestPath = Join-Path $RepoRoot '.github\windows-historical-baselines.json'
    }
    $resolvedManifestPath = (Resolve-Path -LiteralPath $ManifestPath).Path
    $config = Get-Content -LiteralPath $resolvedManifestPath -Raw | ConvertFrom-Json
    if ($config.schemaVersion -ne 1 -or
        $config.minimumSupportedControlSchema -isnot [long] -or
        $config.minimumSupportedControlSchema -lt 1) {
        throw '历史升级基线 manifest 的 schemaVersion 或 minimumSupportedControlSchema 无效'
    }

    $rows = @($config.baselines)
    if ($rows.Count -eq 0) { throw '历史升级基线 manifest 没有 baselines' }
    $baselines = @()
    $seenCommits = @{}
    $seenSchemas = @{}
    foreach ($baseline in $rows) {
        $commit = [string]$baseline.commit
        $schema = $baseline.controlSchemaVersion
        $label = [string]$baseline.label
        if ($commit -cnotmatch '^[0-9a-f]{40}$' -or $schema -isnot [long] -or $schema -lt 1 -or
            $label -notmatch '^[a-z0-9][a-z0-9-]*$') {
            throw '历史升级基线包含非法 commit、controlSchemaVersion 或 label'
        }
        if ($seenCommits.ContainsKey($commit) -or $seenSchemas.ContainsKey([string]$schema)) {
            throw '历史升级基线的 commit 或 control schema 重复'
        }
        $seenCommits[$commit] = $true
        $seenSchemas[[string]$schema] = $true
        $baselines += [pscustomobject]@{
            Commit = $commit
            Schema = [int]$schema
            Label = $label
        }
    }
    $baselines = @($baselines | Sort-Object Schema)
    $currentSchema = Get-GalleryControlSchemaVersion $RepoRoot
    $minimumSupportedSchema = [int]$config.minimumSupportedControlSchema
    if ($currentSchema -le $minimumSupportedSchema) {
        throw "当前 control schema=$currentSchema 必须高于最低支持 schema=$minimumSupportedSchema"
    }
    $expectedSchemas = @($minimumSupportedSchema..($currentSchema - 1))
    $actualSchemas = @($baselines | ForEach-Object { $_.Schema })
    if (($actualSchemas -join ',') -ne ($expectedSchemas -join ',')) {
        throw "历史升级基线必须连续覆盖 schema $minimumSupportedSchema..$($currentSchema - 1)，实际为 $($actualSchemas -join ',')"
    }

    return [pscustomobject]@{
        ManifestPath = $resolvedManifestPath
        CurrentControlSchema = $currentSchema
        MinimumSupportedControlSchema = $minimumSupportedSchema
        Baselines = $baselines
    }
}
