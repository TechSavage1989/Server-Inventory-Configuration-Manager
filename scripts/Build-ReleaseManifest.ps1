[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][string]$ExecutablePath,
    [string]$OutputDirectory=(Join-Path $PSScriptRoot '..\dist'),
    [string]$ExpectedVersion=''
)

$ErrorActionPreference='Stop'
$ExecutablePath=[IO.Path]::GetFullPath($ExecutablePath)
$OutputDirectory=[IO.Path]::GetFullPath($OutputDirectory)

& (Join-Path $PSScriptRoot 'Test-Source.ps1') -ExpectedVersion $ExpectedVersion
if([string]::IsNullOrWhiteSpace($ExpectedVersion)){
    $main=[IO.File]::ReadAllText((Join-Path $PSScriptRoot '..\main.go'))
    $ExpectedVersion=[regex]::Match($main,'appVersion\s*=\s*"(?<v>\d+\.\d+\.\d+)"').Groups['v'].Value
}
& (Join-Path $PSScriptRoot 'Test-Binary.ps1') -ExecutablePath $ExecutablePath -ExpectedVersion $ExpectedVersion

if(Test-Path -LiteralPath $OutputDirectory){Remove-Item -LiteralPath $OutputDirectory -Recurse -Force}
New-Item -Path $OutputDirectory -ItemType Directory -Force|Out-Null

$assetName='Server-Inventory-Configuration-Manager.exe'
$assetPath=Join-Path $OutputDirectory $assetName
Copy-Item -LiteralPath $ExecutablePath -Destination $assetPath -Force
$hash=(Get-FileHash -LiteralPath $assetPath -Algorithm SHA256).Hash.ToLowerInvariant()
$checksumPath=$assetPath+'.sha256.txt'
"$hash  $assetName"|Set-Content -LiteralPath $checksumPath -Encoding ASCII

$notesPath=Join-Path (Join-Path $PSScriptRoot '..\release-notes') ($ExpectedVersion+'.md')
$allNotes=@()
if(Test-Path -LiteralPath $notesPath -PathType Leaf){
    $allNotes=@(Get-Content -LiteralPath $notesPath|Where-Object{$_ -match '^\s*-\s+'}|ForEach-Object{($_ -replace '^\s*-\s+','').Trim()}|Where-Object{$_})
}
$notes=@($allNotes|Select-Object -First 3)
$manifest=[ordered]@{
    schema_version=1
    app_id='Server-Inventory-Configuration-Manager'
    version=$ExpectedVersion
    published_at=(Get-Date).ToUniversalTime().ToString('o')
    release_url="https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/tag/v$ExpectedVersion"
    notes=$notes
    read_more=($allNotes.Count -gt $notes.Count)
    asset=[ordered]@{
        name=$assetName
        url="https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/download/v$ExpectedVersion/$assetName"
        sha256=$hash
        size=(Get-Item -LiteralPath $assetPath).Length
    }
}
[IO.File]::WriteAllText((Join-Path $OutputDirectory 'update-manifest.json'),($manifest|ConvertTo-Json -Depth 8),(New-Object Text.UTF8Encoding($false)))
Write-Host "Release assets created in $OutputDirectory"
Write-Host "Version: $ExpectedVersion"
Write-Host "SHA-256: $hash"