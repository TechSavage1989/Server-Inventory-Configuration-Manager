[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][string]$ExecutablePath,
    [Parameter(Mandatory=$true)][string]$ExpectedVersion
)

$ErrorActionPreference='Stop'
$ExecutablePath=[IO.Path]::GetFullPath($ExecutablePath)
if (-not (Test-Path -LiteralPath $ExecutablePath -PathType Leaf)) { throw "Executable not found: $ExecutablePath" }
if ([IO.Path]::GetFileName($ExecutablePath) -ne 'Server-Inventory-Configuration-Manager.exe') { throw 'Release executable has the wrong stable filename.' }

$bytes=[IO.File]::ReadAllBytes($ExecutablePath)
if ($bytes.Length -lt 4096) { throw 'Executable is unexpectedly small.' }
if ($bytes[0] -ne 0x4d -or $bytes[1] -ne 0x5a) { throw 'Missing MZ signature.' }
$peOffset=[BitConverter]::ToInt32($bytes,0x3c)
if ($peOffset -lt 0 -or ($peOffset+94) -ge $bytes.Length) { throw 'Invalid PE header offset.' }
if ($bytes[$peOffset] -ne 0x50 -or $bytes[$peOffset+1] -ne 0x45 -or $bytes[$peOffset+2] -ne 0 -or $bytes[$peOffset+3] -ne 0) { throw 'Missing PE signature.' }
$machine=[BitConverter]::ToUInt16($bytes,$peOffset+4)
if ($machine -ne 0x8664) { throw ('Unexpected PE machine: 0x{0:x4}' -f $machine) }
$optional=$peOffset+24
$magic=[BitConverter]::ToUInt16($bytes,$optional)
if ($magic -ne 0x20b) { throw 'Executable is not PE32+.' }
$subsystem=[BitConverter]::ToUInt16($bytes,$optional+68)
if ($subsystem -ne 2) { throw "Executable subsystem is $subsystem, expected Windows GUI (2)." }

$marker=[Text.Encoding]::UTF8.GetBytes("SICM_BINARY_ID|Server-Inventory-Configuration-Manager|$ExpectedVersion|")
$found=$false
for($i=0;$i -le $bytes.Length-$marker.Length;$i++){
    $match=$true
    for($j=0;$j -lt $marker.Length;$j++){ if($bytes[$i+$j] -ne $marker[$j]){$match=$false;break} }
    if($match){$found=$true;break}
}
if(-not $found){ throw 'Expected application identity/version marker was not found in the executable.' }

$hash=(Get-FileHash -LiteralPath $ExecutablePath -Algorithm SHA256).Hash.ToLowerInvariant()
Write-Host 'Binary validation passed.'
Write-Host "SHA-256: $hash"
$versionInfo=[Diagnostics.FileVersionInfo]::GetVersionInfo($ExecutablePath)
if($versionInfo.FileVersion -ne $ExpectedVersion -or $versionInfo.ProductVersion -ne $ExpectedVersion){throw 'Windows version metadata mismatch.'}
if($versionInfo.ProductName -ne 'Server Inventory Configuration Manager' -or $versionInfo.OriginalFilename -ne 'Server-Inventory-Configuration-Manager.exe'){throw 'Windows application identity metadata mismatch.'}
