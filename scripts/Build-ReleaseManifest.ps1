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
    $main=[IO.File]::ReadAllText((Join-Path $PSScriptRoot '..\src\app\main.go'))
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

$svgName='Server-Inventory-Configuration-Manager.svg'
$svgPath=Join-Path $OutputDirectory $svgName
Copy-Item -LiteralPath (Join-Path $PSScriptRoot '..\src\app\assets\icons\server-inventory.svg') -Destination $svgPath
$svgHash=(Get-FileHash -LiteralPath $svgPath -Algorithm SHA256).Hash.ToLowerInvariant()
"$svgHash  $svgName"|Set-Content -LiteralPath ($svgPath+'.sha256.txt') -Encoding ASCII

$masterName='Server-Inventory-Configuration-Manager-Illustrator.svg'
$masterPath=Join-Path $OutputDirectory $masterName
Copy-Item -LiteralPath (Join-Path $PSScriptRoot '..\src\app\assets\icons\source\server-inventory-illustrator.svg') -Destination $masterPath
$masterHash=(Get-FileHash -LiteralPath $masterPath -Algorithm SHA256).Hash.ToLowerInvariant()
"$masterHash  $masterName"|Set-Content -LiteralPath ($masterPath+'.sha256.txt') -Encoding ASCII

$previewName='Report-Preview.png'
$previewPath=Join-Path $OutputDirectory $previewName
$previewSource=Join-Path $PSScriptRoot '..\build\report-validation\report-summary.png'
if(-not (Test-Path -LiteralPath $previewSource -PathType Leaf)){throw 'Run report browser validation before building release assets.'}
Copy-Item -LiteralPath $previewSource -Destination $previewPath
$previewHash=(Get-FileHash -LiteralPath $previewPath -Algorithm SHA256).Hash.ToLowerInvariant()
"$previewHash  $previewName"|Set-Content -LiteralPath ($previewPath+'.sha256.txt') -Encoding ASCII

$notesPath=Join-Path (Join-Path $PSScriptRoot '..\docs\release-notes') ($ExpectedVersion+'.md')
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
