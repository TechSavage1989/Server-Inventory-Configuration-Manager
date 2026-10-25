[CmdletBinding()]
param(
    [string]$SourceRoot = (Join-Path $PSScriptRoot '..'),
    [string]$ExpectedVersion = ''
)

$ErrorActionPreference = 'Stop'
$SourceRoot = [IO.Path]::GetFullPath($SourceRoot)

$mainPath = Join-Path $SourceRoot 'main.go'
$updatePath = Join-Path $SourceRoot 'update.go'
$htmlPath = Join-Path $SourceRoot 'index.html'
$collectorPath = Join-Path $SourceRoot 'Export-ServerInventory.ps1'

foreach ($path in @($mainPath,$updatePath,$htmlPath,$collectorPath)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Required source file not found: $path"
    }
}

$main = [IO.File]::ReadAllText($mainPath)
$update = [IO.File]::ReadAllText($updatePath)
$html = [IO.File]::ReadAllText($htmlPath)
$collector = [IO.File]::ReadAllText($collectorPath)

function Require-Match {
    param([string]$Text,[string]$Pattern,[string]$Description)
    if ($Text -notmatch $Pattern) { throw "Missing source contract: $Description" }
}

Require-Match $main 'appName\s*=\s*"Server Inventory Configuration Manager"' 'application name'
Require-Match $main 'appVersion\s*=\s*"(?<v>\d+\.\d+\.\d+)"' 'application version'
Require-Match $main 'collectorVersion\s*=\s*"(?<v>\d+\.\d+\.\d+)"' 'collector version'
Require-Match $update 'stableExecutableName\s*=\s*"Server-Inventory-Configuration-Manager\.exe"' 'stable executable filename'
Require-Match $update 'releases/latest/download/update-manifest\.json' 'public update feed'
Require-Match $update 'If-None-Match' 'conditional update request'
Require-Match $update 'Retry-After' 'retry-after handling'
Require-Match $update 'MoveFileExW' 'atomic target replacement'
Require-Match $update 'previous\.exe' 'single rollback executable'
Require-Match $update 'SICM_BINARY_ID\|Server-Inventory-Configuration-Manager\|' 'binary identity marker'
Require-Match $html 'id="updateCheckButton"' 'manual update check UI'
Require-Match $html 'id="updateDownloadButton"' 'explicit update download UI'
Require-Match $html 'id="updateInstallButton"' 'explicit install/restart UI'
Require-Match $html 'window\.addEventListener\(''focus''' 'focus-return update check'
Require-Match $collector '\$scriptVersion\s*=\s*''(?<v>\d+\.\d+\.\d+)''' 'collector script version'


$appVersion = [regex]::Match($main,'appVersion\s*=\s*"(?<v>\d+\.\d+\.\d+)"').Groups['v'].Value
$collectorVersion = [regex]::Match($main,'collectorVersion\s*=\s*"(?<v>\d+\.\d+\.\d+)"').Groups['v'].Value
$scriptVersion = [regex]::Match($collector,'\$scriptVersion\s*=\s*''(?<v>\d+\.\d+\.\d+)''').Groups['v'].Value

if ($appVersion -ne $collectorVersion -or $appVersion -ne $scriptVersion) {
    throw "Version mismatch. App=$appVersion collector=$collectorVersion script=$scriptVersion"
}
if (-not [string]::IsNullOrWhiteSpace($ExpectedVersion) -and $appVersion -ne $ExpectedVersion) {
    throw "Source version $appVersion does not match expected version $ExpectedVersion."
}

Write-Host 'Source validation passed.'
Write-Host "Version: $appVersion"