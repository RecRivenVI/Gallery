[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$BaseURL,
    [Parameter(Mandatory = $true)]
    [string]$AppRoot,
    [Parameter(Mandatory = $true)]
    [string]$PixivRoot,
    [Parameter(Mandatory = $true)]
    [string]$RuleFile
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$origin = $BaseURL.TrimEnd('/')
$AppRoot = [System.IO.Path]::GetFullPath($AppRoot)
$PixivRoot = [System.IO.Path]::GetFullPath($PixivRoot)
$RuleFile = [System.IO.Path]::GetFullPath($RuleFile)
$markerPath = Join-Path $AppRoot 'pixiv-preset-v1.json'
$statusPath = Join-Path $AppRoot 'pixiv-preset-status.json'
$expectedRuleHash = '67a7a464b50f9fdbab0359fd9d07838cdc905ac71d6cebf5e72a3d096d45b09f'

function Write-PresetStatus {
    param(
        [Parameter(Mandatory = $true)]
        [string]$State,
        [hashtable]$Details = @{}
    )

    $status = [ordered]@{
        schemaVersion = 1
        state = $State
        updatedAt = [DateTimeOffset]::UtcNow.ToString('o')
    }
    foreach ($entry in $Details.GetEnumerator()) {
        $status[$entry.Key] = $entry.Value
    }
    New-Item -ItemType Directory -Force -Path $AppRoot | Out-Null
    $status | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $statusPath -Encoding UTF8
}

if (Test-Path -LiteralPath $markerPath -PathType Leaf) {
    Write-PresetStatus -State 'ready' -Details @{ reused = $true }
    return
}

$webSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$script:csrfToken = ''
$script:initializerSessionID = ''

function Invoke-GalleryRequest {
    param(
        [Parameter(Mandatory = $true)]
        [ValidateSet('GET', 'POST', 'PUT', 'PATCH', 'DELETE')]
        [string]$Method,
        [Parameter(Mandatory = $true)]
        [string]$Path,
        [AllowNull()]
        [object]$Body = $null,
        [string]$IfMatch = ''
    )

    $headers = @{
        Origin = $origin
        'Sec-Fetch-Site' = 'same-origin'
    }
    if ($script:csrfToken) {
        $headers['X-Gallery-CSRF'] = $script:csrfToken
    }
    if ($IfMatch) {
        $headers['If-Match'] = $IfMatch
    }

    $parameters = @{
        Uri = "$origin$Path"
        Method = $Method
        Headers = $headers
        WebSession = $webSession
        TimeoutSec = 30
    }
    if ($null -ne $Body) {
        $parameters['ContentType'] = 'application/json; charset=utf-8'
        $parameters['Body'] = $Body | ConvertTo-Json -Compress -Depth 100
    }
    Invoke-RestMethod @parameters
}

function Get-SingleItem {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [object[]]$Items,
        [Parameter(Mandatory = $true)]
        [scriptblock]$Predicate,
        [Parameter(Mandatory = $true)]
        [string]$Description
    )

    $matches = @($Items | Where-Object $Predicate)
    if ($matches.Count -gt 1) {
        throw "$Description 存在多个匹配项，拒绝猜测应复用哪一个。"
    }
    if ($matches.Count -eq 1) {
        return $matches[0]
    }
    return $null
}

try {
    Write-PresetStatus -State 'initializing'

    if (-not (Test-Path -LiteralPath $PixivRoot -PathType Container)) {
        throw 'Pixiv Source 根目录不存在或不可访问。'
    }
    if (-not (Test-Path -LiteralPath $RuleFile -PathType Leaf)) {
        throw '测试包缺少 Pixiv 规则文件。'
    }
    $actualRuleHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $RuleFile).Hash.ToLowerInvariant()
    if ($actualRuleHash -ne $expectedRuleHash) {
        throw 'Pixiv 规则文件校验失败。'
    }

    $ruleText = Get-Content -Raw -LiteralPath $RuleFile
    $ruleDocument = $ruleText | ConvertFrom-Json
    if (@($ruleDocument.primitives).Count -ne 36) {
        throw 'Pixiv 规则 primitive 数量不是已验证的 36。'
    }

    $bootstrap = Invoke-GalleryRequest -Method GET -Path '/api/v1/bootstrap'
    if ($bootstrap.mode -ne 'personal') {
        throw 'Pixiv 测试预置只支持 Personal 模式。'
    }
    $script:csrfToken = [string]$bootstrap.csrfToken
    $attempt = Invoke-GalleryRequest -Method POST -Path '/api/v1/personal/pairing-attempts'
    $exchange = Invoke-GalleryRequest -Method POST -Path '/api/v1/personal/pair' -Body @{
        credential = [string]$attempt.credential
    }
    $script:csrfToken = [string]$exchange.csrfToken
    $script:initializerSessionID = [string]$exchange.session.id

    $libraryResponse = Invoke-GalleryRequest -Method GET -Path '/api/v1/libraries'
    $library = Get-SingleItem -Items @($libraryResponse.libraries) -Predicate {
        $_.name -eq 'Pixiv'
    } -Description 'Pixiv Library'
    if ($null -eq $library) {
        $library = Invoke-GalleryRequest -Method POST -Path '/api/v1/libraries' -Body @{
            name = 'Pixiv'
        }
    }

    $sourceResponse = Invoke-GalleryRequest -Method GET -Path (
        '/api/v1/sources?libraryId=' + [Uri]::EscapeDataString([string]$library.id)
    )
    $source = Get-SingleItem -Items @($sourceResponse.sources) -Predicate {
        $_.displayName -eq 'pixiv' -and $_.libraryId -eq $library.id
    } -Description 'pixiv Source'
    if ($null -eq $source) {
        $source = Invoke-GalleryRequest -Method POST -Path '/api/v1/sources' -Body @{
            libraryId = [string]$library.id
            displayName = 'pixiv'
            rootPath = $PixivRoot
        }
    }

    $packageResponse = Invoke-GalleryRequest -Method GET -Path '/api/v1/rule-packages'
    $rulePackage = Get-SingleItem -Items @($packageResponse.items) -Predicate {
        $_.ruleSetId -eq $ruleDocument.rule_set_id -and $_.status -ne 'deleted'
    } -Description 'Pixiv RulePackage'
    if ($null -eq $rulePackage) {
        $rulePackage = Invoke-GalleryRequest -Method POST -Path '/api/v1/rule-packages' -Body @{
            ruleSetId = [string]$ruleDocument.rule_set_id
            name = 'pixiv'
            description = '测试快照预置：由已验证的 legacy schema v3 配置转换而来。'
        }
    }

    $semanticHashProperty = $rulePackage.PSObject.Properties['currentSemanticHash']
    $semanticHash = if ($null -ne $semanticHashProperty) {
        [string]$semanticHashProperty.Value
    } else {
        ''
    }
    if (-not $semanticHash) {
        $draftIDProperty = $rulePackage.PSObject.Properties['draftId']
        if ($null -ne $draftIDProperty -and $draftIDProperty.Value) {
            $draft = Invoke-GalleryRequest -Method GET -Path (
                '/api/v1/rule-packages/' + [string]$rulePackage.id + '/draft'
            )
        } else {
            $draft = Invoke-GalleryRequest -Method PUT -Path (
                '/api/v1/rule-packages/' + [string]$rulePackage.id + '/draft'
            ) -IfMatch '"0"' -Body @{
                format = 'json'
                content = $ruleText
                expectedRevision = 0
            }
        }

        $validation = Invoke-GalleryRequest -Method POST -Path (
            '/api/v1/rule-packages/' + [string]$rulePackage.id + '/draft/validate'
        ) -IfMatch ('"{0}"' -f [int]$draft.revision)
        if (-not $validation.valid) {
            throw 'Pixiv 规则未通过服务端校验。'
        }
        $validatedRevision = [int]$validation.draft.revision
        $version = Invoke-GalleryRequest -Method POST -Path (
            '/api/v1/rule-packages/' + [string]$rulePackage.id + '/publish'
        ) -IfMatch ('"{0}"' -f $validatedRevision) -Body @{
            expectedRevision = $validatedRevision
            reason = '测试快照预置 Pixiv 规则'
            confirmImpact = $true
        }
        $semanticHash = [string]$version.semanticHash
    }

    $bindingResponse = Invoke-GalleryRequest -Method GET -Path (
        '/api/v1/source-rule-bindings?sourceId=' + [Uri]::EscapeDataString([string]$source.id)
    )
    $bindings = @($bindingResponse.bindings)
    $binding = Get-SingleItem -Items $bindings -Predicate {
        $_.semanticHash -eq $semanticHash
    } -Description 'Pixiv SourceRuleBinding'
    if ($null -eq $binding) {
        if ($bindings.Count -gt 0) {
            throw 'pixiv Source 已有其它规则 Binding，拒绝自动覆盖。'
        }
        $binding = Invoke-GalleryRequest -Method POST -Path '/api/v1/source-rule-bindings' -Body @{
            sourceId = [string]$source.id
            semanticHash = $semanticHash
            parameters = @{}
            priority = 100
        }
        $binding = Invoke-GalleryRequest -Method PATCH -Path (
            '/api/v1/source-rule-bindings/' + [string]$binding.id
        ) -Body @{
            status = 'paused'
        }
    }

    $marker = [ordered]@{
        schemaVersion = 1
        createdAt = [DateTimeOffset]::UtcNow.ToString('o')
        libraryId = [string]$library.id
        sourceId = [string]$source.id
        rulePackageId = [string]$rulePackage.id
        semanticHash = $semanticHash
        bindingId = [string]$binding.id
        bindingStatus = [string]$binding.status
        primitiveCount = 36
        scanStarted = $false
    }
    $marker | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $markerPath -Encoding UTF8
    Write-PresetStatus -State 'ready' -Details @{
        reused = $false
        bindingStatus = [string]$binding.status
        scanStarted = $false
    }
} catch {
    Write-PresetStatus -State 'failed' -Details @{
        message = $_.Exception.Message
    }
    throw
} finally {
    if ($script:initializerSessionID) {
        try {
            Invoke-GalleryRequest -Method DELETE -Path (
                '/api/v1/sessions/' + [Uri]::EscapeDataString($script:initializerSessionID)
            ) | Out-Null
        } catch {
            # 预置结果已经落盘；初始化 Session 也会按正常过期策略失效。
        }
    }
}
