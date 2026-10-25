[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$ExpectedVersion)
$ErrorActionPreference='Stop'
$base='https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest/download'
$temp=Join-Path $env:TEMP ('SICM-ReleaseVerify-'+[guid]::NewGuid().ToString('N'))
New-Item $temp -ItemType Directory -Force|Out-Null
try{
    $manifestPath=Join-Path $temp 'update-manifest.json'
    Invoke-WebRequest -UseBasicParsing -Uri "$base/update-manifest.json" -OutFile $manifestPath
    $manifest=Get-Content $manifestPath -Raw|ConvertFrom-Json
    if($manifest.app_id -ne 'Server-Inventory-Configuration-Manager'){throw 'Public manifest app identity mismatch.'}
    if($manifest.version -ne $ExpectedVersion){throw "Public manifest version $($manifest.version) does not match $ExpectedVersion."}
    if($manifest.asset.name -ne 'Server-Inventory-Configuration-Manager.exe'){throw 'Public manifest asset name mismatch.'}
    $exe=Join-Path $temp $manifest.asset.name
    Invoke-WebRequest -UseBasicParsing -Uri $manifest.asset.url -OutFile $exe
    if((Get-Item $exe).Length -ne [int64]$manifest.asset.size){throw 'Public asset size mismatch.'}
    $hash=(Get-FileHash $exe -Algorithm SHA256).Hash.ToLowerInvariant()
    if($hash -ne ([string]$manifest.asset.sha256).ToLowerInvariant()){throw 'Public asset SHA-256 mismatch.'}
    & (Join-Path $PSScriptRoot 'Test-Binary.ps1') -ExecutablePath $exe -ExpectedVersion $ExpectedVersion
    $svg=Join-Path $temp 'Server-Inventory-Configuration-Manager.svg'
    Invoke-WebRequest -UseBasicParsing -Uri "$base/Server-Inventory-Configuration-Manager.svg" -OutFile $svg
    $expectedSvg=(Get-FileHash (Join-Path $PSScriptRoot '..\assets\icons\server-inventory.svg') -Algorithm SHA256).Hash
    if((Get-FileHash $svg -Algorithm SHA256).Hash -ne $expectedSvg){throw 'Public editable SVG differs from release source.'}
    $preview=Join-Path $temp 'Report-Preview.png'
    Invoke-WebRequest -UseBasicParsing -Uri "$base/Report-Preview.png" -OutFile $preview
    $expectedPreview=(Get-FileHash (Join-Path $PSScriptRoot '..\build\report-validation\report-summary.png') -Algorithm SHA256).Hash
    if((Get-FileHash $preview -Algorithm SHA256).Hash -ne $expectedPreview){throw 'Public report preview differs from the validated synthetic report.'}
    Write-Host 'Public updater feed and release asset verification passed.'
}
finally{Remove-Item $temp -Recurse -Force -ErrorAction SilentlyContinue}
