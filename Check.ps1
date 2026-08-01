[CmdletBinding()]
param(
    [switch]$Race,
    [switch]$BackendOnly
)

$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'scripts\Check.ps1') -Race:$Race -BackendOnly:$BackendOnly
exit $LASTEXITCODE
