<#
Builds receiver boot media: a Windows PE ISO that starts this program's
receiver, so a new HyperCore, Nutanix AHV or Proxmox VE VM, or a physical
server (V2P), can receive a copy straight onto its own disks.

It needs the Windows ADK and its Windows PE add-on on this computer (they are
Microsoft downloads; nothing here redistributes Windows PE). Drivers the target
needs to see its disks and network (VirtIO for KVM targets, the server vendor's
storage and network drivers for physical servers) are added from DriverFolder.

Windows PowerShell 3.0 compatible. Writes progress with [Console]::Out and the
result as one JSON line beginning with RESULT:.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$OutputIso,
    [Parameter(Mandatory = $true)][string]$WorkFolder,
    [Parameter(Mandatory = $true)][string]$ReceiverExe,
    # Driver folders separated by | (a character Windows paths cannot hold).
    [string]$DriverFolders = '',
    [ValidateSet('hyperv', 'scale', 'nutanix', 'proxmox', 'physical')][string]$Target = 'proxmox',
    [ValidateRange(1, 65535)][int]$Port = 8443,
    [string]$Disks = 'blank',
    [string]$AdkRoot = '',
    [switch]$Plan
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2
$DriverFolder = @($DriverFolders.Split('|') | ForEach-Object { $_.Trim() } | Where-Object { $_ })

function Write-Step([string]$Message) {
    [Console]::Out.WriteLine(('{0}  {1}' -f (Get-Date -Format 'HH:mm:ss'), $Message))
}

function Write-Result([hashtable]$Value) {
    $json = New-Object PSObject -Property $Value | ConvertTo-Json -Compress -Depth 4
    [Console]::Out.WriteLine('RESULT:' + $json)
}

function Find-AdkRoot([string]$Override) {
    if ($Override) { return $Override.TrimEnd('\') }
    foreach ($key in @('HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows Kits\Installed Roots', 'HKLM:\SOFTWARE\Microsoft\Windows Kits\Installed Roots')) {
        $item = Get-ItemProperty -Path $key -Name KitsRoot10 -ErrorAction SilentlyContinue
        if ($item -and $item.KitsRoot10) { return (Join-Path $item.KitsRoot10 'Assessment and Deployment Kit') }
    }
    return (Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\Assessment and Deployment Kit')
}

function Test-SafeDisks([string]$Value) {
    if ($Value -eq 'blank') { return $true }
    return ($Value -match '^[0-9]{1,2}(,[0-9]{1,2})*$')
}

$problems = New-Object System.Collections.ArrayList
$adk = Find-AdkRoot $AdkRoot
$peRoot = Join-Path $adk 'Windows Preinstallation Environment\amd64'
$wim = Join-Path $peRoot 'en-us\winpe.wim'
$media = Join-Path $peRoot 'Media'
$oscdimg = Join-Path $adk 'Deployment Tools\amd64\Oscdimg\oscdimg.exe'
$etfsboot = Join-Path $adk 'Deployment Tools\amd64\Oscdimg\etfsboot.com'
$efisys = Join-Path $adk 'Deployment Tools\amd64\Oscdimg\efisys_noprompt.bin'
$dism = Join-Path $adk 'Deployment Tools\amd64\DISM\dism.exe'
if (-not (Test-Path -LiteralPath $dism)) { $dism = Join-Path $env:SystemRoot 'System32\dism.exe' }

if (-not (Test-Path -LiteralPath $oscdimg)) { [void]$problems.Add("The Windows ADK Deployment Tools were not found under $adk. Install the Windows ADK (Deployment Tools).") }
if (-not (Test-Path -LiteralPath $wim)) { [void]$problems.Add("The Windows PE add-on was not found under $peRoot. Install the Windows PE add-on for the ADK.") }
if (-not (Test-Path -LiteralPath $ReceiverExe -PathType Leaf)) { [void]$problems.Add("The receiver program $ReceiverExe was not found.") }
if (-not (Test-SafeDisks $Disks)) { [void]$problems.Add("Disks must be 'blank' or a list of disk numbers such as 1,2.") }
if ([IO.Path]::GetExtension($OutputIso) -ne '.iso') { [void]$problems.Add('The output file must end in .iso.') }
if (Test-Path -LiteralPath $OutputIso) { [void]$problems.Add("$OutputIso already exists; choose a new file name so nothing is overwritten.") }
if ((Test-Path -LiteralPath $WorkFolder) -and @(Get-ChildItem -LiteralPath $WorkFolder -Force).Count -gt 0) { [void]$problems.Add("The work folder $WorkFolder is not empty; choose an empty or new folder.") }
foreach ($folder in $DriverFolder) {
    if (-not (Test-Path -LiteralPath $folder -PathType Container)) { [void]$problems.Add("The driver folder $folder was not found.") }
    elseif (@(Get-ChildItem -LiteralPath $folder -Filter *.inf -Recurse -ErrorAction SilentlyContinue).Count -eq 0) { [void]$problems.Add("The driver folder $folder holds no .inf drivers.") }
}
if ($Target -in @('scale', 'nutanix', 'proxmox') -and $DriverFolder.Count -eq 0) {
    Write-Step 'No driver folder was given. KVM targets usually need the VirtIO storage (viostor or vioscsi) and network (NetKVM) drivers, unless the VM uses SATA/IDE disks and an e1000 network adapter.'
}
if ($Target -eq 'physical' -and $DriverFolder.Count -eq 0) {
    Write-Step "No driver folder was given. Add the server vendor's storage controller and network drivers if Windows PE does not see the server's disks or network."
}

$arguments = "--receiver --disks $Disks --port $Port"
$plan = @{
    adk = $adk; winpe = $wim; oscdimg = $oscdimg; dism = $dism; output = $OutputIso; work = $WorkFolder
    drivers = @($DriverFolder); target = $Target; receiverArguments = $arguments; problems = @($problems)
}
if ($Plan -or $problems.Count -gt 0) {
    $plan.ok = ($problems.Count -eq 0)
    Write-Result $plan
    if ($problems.Count -gt 0) { exit 1 }
    exit 0
}

$mount = Join-Path $WorkFolder 'mount'
$isoRoot = Join-Path $WorkFolder 'media'
$mounted = $false
try {
    Write-Step 'Copying the Windows PE files.'
    New-Item -ItemType Directory -Path $mount -Force | Out-Null
    New-Item -ItemType Directory -Path $isoRoot -Force | Out-Null
    Copy-Item -Path (Join-Path $media '*') -Destination $isoRoot -Recurse -Force
    New-Item -ItemType Directory -Path (Join-Path $isoRoot 'sources') -Force | Out-Null
    $bootWim = Join-Path $isoRoot 'sources\boot.wim'
    Copy-Item -LiteralPath $wim -Destination $bootWim -Force
    Set-ItemProperty -LiteralPath $bootWim -Name IsReadOnly -Value $false

    Write-Step 'Mounting the Windows PE image.'
    & $dism /English /Mount-Image "/ImageFile:$bootWim" /Index:1 "/MountDir:$mount" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "DISM could not mount the Windows PE image (exit $LASTEXITCODE)." }
    $mounted = $true

    foreach ($folder in $DriverFolder) {
        Write-Step "Adding drivers from $folder."
        & $dism /English "/Image:$mount" /Add-Driver "/Driver:$folder" /Recurse | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "DISM could not add the drivers in $folder (exit $LASTEXITCODE)." }
    }

    Write-Step 'Adding the receiver.'
    $receiverFolder = Join-Path $mount 'Receiver'
    New-Item -ItemType Directory -Path $receiverFolder -Force | Out-Null
    $exeName = [IO.Path]::GetFileName($ReceiverExe)
    Copy-Item -LiteralPath $ReceiverExe -Destination (Join-Path $receiverFolder $exeName) -Force
    $exe = 'X:\Receiver\' + $exeName
    $startnet = @(
        '@echo off',
        'wpeinit',
        'wpeutil DisableFirewall',
        'title Server Inventory and Migration Manager receiver',
        'echo.',
        'echo Server Inventory and Migration Manager receiver',
        'echo Waiting for the network...',
        'ping -n 6 127.0.0.1 >nul',
        'ipconfig | find "IPv4"',
        ('start /wait "" "{0}" --receiver --list-disks' -f $exe),
        ('start /wait "" "{0}" {1}' -f $exe, $arguments),
        'echo.',
        'echo The receiver stopped. To start it again, or to choose other disks, run:',
        ('echo   start /wait "" "{0}" --receiver --disks 1 --port {1}' -f $exe, $Port),
        'echo Type wpeutil reboot to restart.'
    ) -join "`r`n"
    [IO.File]::WriteAllText((Join-Path $mount 'Windows\System32\startnet.cmd'), $startnet + "`r`n", (New-Object Text.ASCIIEncoding))

    Write-Step 'Saving the Windows PE image.'
    & $dism /English /Unmount-Image "/MountDir:$mount" /Commit | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "DISM could not save the Windows PE image (exit $LASTEXITCODE)." }
    $mounted = $false

    Write-Step 'Writing the ISO.'
    $bootData = '2#p0,e,b"{0}"#pEF,e,b"{1}"' -f $etfsboot, $efisys
    & $oscdimg -m -o -u2 -udfver102 "-bootdata:$bootData" $isoRoot $OutputIso | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "oscdimg could not write the ISO (exit $LASTEXITCODE)." }
    $stream = [IO.File]::OpenRead($OutputIso)
    try { $hash = -join ([Security.Cryptography.SHA256]::Create().ComputeHash($stream) | ForEach-Object { $_.ToString('X2') }) }
    finally { $stream.Dispose() }
    Write-Step "Receiver media written to $OutputIso."
    Write-Result @{ ok = $true; output = $OutputIso; sha256 = $hash; size = (Get-Item -LiteralPath $OutputIso).Length; target = $Target; receiverArguments = $arguments; drivers = @($DriverFolder) }
}
catch {
    if ($mounted) {
        & $dism /English /Unmount-Image "/MountDir:$mount" /Discard | Out-Null
    }
    Write-Step ('Failed: ' + $_.Exception.Message)
    Write-Result @{ ok = $false; error = $_.Exception.Message }
    exit 1
}
