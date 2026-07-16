[CmdletBinding()]
param(
    [switch]$Race
)

$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'scripts\Check.ps1') -Race:$Race
exit $LASTEXITCODE
