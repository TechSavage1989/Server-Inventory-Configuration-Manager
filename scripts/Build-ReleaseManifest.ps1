[CmdletBinding()]
param(
    [string]$SourcePath = (Join-Path $PSScriptRoot '..\Server-Inventory-Configuration-Manager.ps1'),
    [string]$OutputDirectory = (Join-Path $PSScriptRoot '..\dist'),
    [string]$ExpectedVersion = ''
)

$ErrorActionPreference = 'Stop'
$SourcePath = [IO.Path]::GetFullPath($SourcePath)
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)

& (Join-Path $PSScriptRoot 'Test-Source.ps1') -SourcePath $SourcePath -ExpectedVersion $ExpectedVersion

$content = [IO.File]::ReadAllText($SourcePath)
$version = [regex]::Match(
    $content,
    '\$script:AppVersion\s*=\s*["''](?<v>[^"'']+)["'']'
).Groups['v'].Value

if (
    -not [string]::IsNullOrWhiteSpace($ExpectedVersion) -and
    $version -ne $ExpectedVersion
) {
    throw 'Version mismatch.'
}

if (Test-Path -LiteralPath $OutputDirectory) {
    Remove-Item -LiteralPath $OutputDirectory -Recurse -Force
}

New-Item -Path $OutputDirectory -ItemType Directory -Force | Out-Null

$assetName = 'Server-Inventory-Configuration-Manager.ps1'
$assetPath = Join-Path $OutputDirectory $assetName

Copy-Item -LiteralPath $SourcePath -Destination $assetPath -Force

$sha = [Security.Cryptography.SHA256]::Create()
$stream = $null

try {
    $stream = [IO.File]::OpenRead($assetPath)
    $hashBytes = $sha.ComputeHash($stream)
    $hash = (($hashBytes | ForEach-Object { $_.ToString('x2') }) -join '')
}
finally {
    if ($stream) { $stream.Dispose() }
    $sha.Dispose()
}

$checksumName = $assetName + '.sha256.txt'
$checksumPath = Join-Path $OutputDirectory $checksumName

"$hash  $assetName" |
    Set-Content -LiteralPath $checksumPath -Encoding ASCII

$notesPath = Join-Path (
    Join-Path $PSScriptRoot '..\release-notes'
) ($version + '.md')

$allNotes = @()

if (Test-Path -LiteralPath $notesPath -PathType Leaf) {
    $allNotes = @(
        Get-Content -LiteralPath $notesPath |
            Where-Object { $_ -match '^\s*-\s+' } |
            ForEach-Object {
                ($_ -replace '^\s*-\s+','').Trim()
            } |
            Where-Object {
                -not [string]::IsNullOrWhiteSpace($_)
            }
    )
}

$notes = @($allNotes | Select-Object -First 3)

$manifest = [ordered]@{
    schema_version = 1
    app_id = 'Server-Inventory-Configuration-Manager'
    version = $version
    published_at = (Get-Date).ToUniversalTime().ToString('o')
    release_url = "https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/tag/v$version"
    notes = $notes
    read_more = ($allNotes.Count -gt $notes.Count)
    asset = [ordered]@{
        name = $assetName
        url = "https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/download/v$version/$assetName"
        sha256 = $hash
        size = (Get-Item -LiteralPath $assetPath).Length
    }
}

$manifest |
    ConvertTo-Json -Depth 8 |
    Set-Content -LiteralPath (
        Join-Path $OutputDirectory 'update-manifest.json'
    ) -Encoding UTF8

Write-Host "Release assets created in $OutputDirectory"
Write-Host "Version: $version"
Write-Host "SHA-256: $hash"
