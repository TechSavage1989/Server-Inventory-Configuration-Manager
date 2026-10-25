[CmdletBinding()]
param(
    [string]$SourcePath = (Join-Path $PSScriptRoot '..\Server-Inventory-Configuration-Manager.ps1'),
    [string]$ExpectedVersion = ''
)

$ErrorActionPreference = 'Stop'
$SourcePath = [IO.Path]::GetFullPath($SourcePath)

if (-not (Test-Path -LiteralPath $SourcePath -PathType Leaf)) {
    throw "Source file not found: $SourcePath"
}

$tokens = $null
$parseErrors = $null

[Management.Automation.Language.Parser]::ParseFile(
    $SourcePath,
    [ref]$tokens,
    [ref]$parseErrors
) | Out-Null

if ($parseErrors -and $parseErrors.Count -gt 0) {
    $details = @(
        $parseErrors | ForEach-Object {
            'Line {0}, column {1}: {2}' -f
                $_.Extent.StartLineNumber,
                $_.Extent.StartColumnNumber,
                $_.Message
        }
    ) -join [Environment]::NewLine

    throw ('PowerShell parser errors were found:' + [Environment]::NewLine + $details)
}

$content = [IO.File]::ReadAllText($SourcePath)

function Require-Match {
    param([string]$Pattern,[string]$Description)

    if ($content -notmatch $Pattern) {
        throw "Missing source contract: $Description"
    }
}

Require-Match '\$script:ApplicationId\s*=\s*["'']Server-Inventory-Configuration-Manager["'']' 'application identity'
Require-Match '\$script:StableLaunchFileName\s*=\s*["'']Server-Inventory-Configuration-Manager\.ps1["'']' 'stable launch filename'
Require-Match 'releases/latest/download/update-manifest\.json' 'public updater feed'
Require-Match 'function\s+Begin-UpdateCheck' 'background update check'
Require-Match 'function\s+Begin-UpdateDownload' 'explicit update download'
Require-Match 'function\s+Install-ReadyUpdateAndRestart' 'explicit install/restart'
Require-Match 'function\s+Write-ApplicationHealthMarker' 'healthy startup marker'
Require-Match 'previous\.ps1' 'single rollback file'
Require-Match 'OPEN-FIRST - Server Inventory Report\.html' 'HTML inventory report'
Require-Match 'OPEN-FIRST - Server Inventory Report\.docx' 'Word inventory report'
Require-Match 'OPEN-FIRST - Server Inventory Summary\.txt' 'text inventory summary'

if ($content -match '-ExecutionPolicy\s+Bypass') {
    throw 'ExecutionPolicy Bypass is not allowed in the maintained source.'
}

if ($content -match '(?i)(github_pat_|ghp_[A-Za-z0-9]{20,}|BEGIN PRIVATE KEY)') {
    throw 'Potential credential or private-key material was found in source.'
}

$appMatch = [regex]::Match(
    $content,
    '\$script:AppVersion\s*=\s*["''](?<v>[^"'']+)["'']'
)

$collectorMatch = [regex]::Match(
    $content,
    '\$scriptVersion\s*=\s*["''](?<v>[^"'']+)["'']'
)

if (-not $appMatch.Success) {
    throw 'AppVersion could not be parsed.'
}

if (-not $collectorMatch.Success) {
    throw 'Collector version could not be parsed.'
}

$appVersion = $appMatch.Groups['v'].Value
$collectorVersion = $collectorMatch.Groups['v'].Value

if ($appVersion -ne $collectorVersion) {
    throw "App version $appVersion does not match collector version $collectorVersion."
}

if (
    -not [string]::IsNullOrWhiteSpace($ExpectedVersion) -and
    $appVersion -ne $ExpectedVersion
) {
    throw "Source version $appVersion does not match expected version $ExpectedVersion."
}

Write-Host 'Source validation passed.'
Write-Host "Version: $appVersion"
Write-Host "Source: $SourcePath"
