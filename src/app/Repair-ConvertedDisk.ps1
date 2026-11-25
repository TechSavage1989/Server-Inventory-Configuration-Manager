# Server Inventory and Migration Manager: offline fix-ups for a converted
# Windows disk (VHD or VHDX), so it boots on its new platform.
#
# Check  mounts the image read-only and reports the Windows installation,
#        boot firmware, storage drivers and VMware Tools.
# Apply  mounts it read-write, saves the SYSTEM hive and a JSON record of
#        every value it will change, adds drivers with DISM, makes the target
#        platform's storage drivers start at boot and can turn off the VMware
#        Tools services.
# Undo   reverses an Apply from its record: removes the drivers it added and
#        puts every changed value back.
#
# It never changes the computer it runs on, never deletes a service or key,
# never writes to an image another process has attached, and refuses an image
# whose disk signature matches a disk already online here (Windows would give
# it a new signature, which can stop it booting).
# Windows PowerShell 3.0 compatible. Logs with [Console]::Out only.

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidateSet('Check', 'Apply', 'Undo')][string]$Mode,
    [Parameter(Mandatory = $true)][string]$ImagePath,
    [Parameter(Mandatory = $true)][string]$ResultPath,
    [Parameter(Mandatory = $true)][string]$WorkPath,
    [ValidateSet('hyperv', 'kvm', 'vmware', 'physical')][string]$Target = 'hyperv',
    [string]$DriverPath = '',
    [switch]$DisableVMwareTools,
    [string]$RecordPath = '',
    [string]$UndoRecord = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2
$script:EngineVersion = '1'
$script:Mounted = $false
$script:AccessPaths = New-Object System.Collections.ArrayList
$script:LoadedHives = New-Object System.Collections.ArrayList
$script:HivePrefix = 'SIMRepair' + $PID

function Write-RepairLog {
    param([string]$Message, [string]$Level = 'INFO')
    [Console]::Out.WriteLine((Get-Date -Format 'yyyy-MM-dd HH:mm:ss') + ' [' + $Level + '] ' + $Message)
}

function Write-JsonFile {
    param([string]$Path, [object]$Value)
    $json = ConvertTo-Json -InputObject $Value -Depth 12
    $temp = $Path + '.tmp'
    [IO.File]::WriteAllText($temp, $json, (New-Object System.Text.UTF8Encoding($false)))
    if (Test-Path -LiteralPath $Path) { Remove-Item -LiteralPath $Path -Force }
    Move-Item -LiteralPath $temp -Destination $Path
}

# Storage drivers each platform boots from. Only services that already exist
# in the image (or that DISM just added) are changed.
$script:BootDrivers = @{
    hyperv   = @('vmbus', 'storvsc', 'intelide', 'pciide', 'atapi')
    kvm      = @('viostor', 'vioscsi', 'storahci', 'msahci', 'intelide', 'pciide', 'atapi')
    vmware   = @('pvscsi', 'LSI_SAS', 'LSI_SCSI', 'storahci', 'msahci', 'intelide', 'pciide', 'atapi')
    physical = @('storahci', 'msahci', 'stornvme', 'intelide', 'pciide', 'atapi', 'LSI_SAS', 'LSI_SAS2i', 'LSI_SAS3i', 'megasas', 'megasas2i', 'megasas35i', 'percsas2i', 'percsas3i', 'SmartPqi', 'HpSAMD', 'arcsas', 'iaStorAVC', 'iaStorV')
}
$script:ReportedDrivers = @('vmbus', 'storvsc', 'netvsc', 'viostor', 'vioscsi', 'netkvm', 'pvscsi', 'vmxnet3ndis6', 'LSI_SAS', 'storahci', 'stornvme', 'intelide', 'pciide', 'atapi')
$script:VMwareServices = @('VMTools', 'VGAuthService', 'vm3dservice', 'VMwareCAFCommAmqpListener', 'VMwareCAFManagementAgentHost', 'vmvss')
$script:VirtIOFolders = @('viostor', 'vioscsi', 'NetKVM', 'Balloon', 'vioserial', 'viorng', 'pvpanic', 'vioinput', 'fwcfg', 'qemupciserial')
$script:EspType = '{c12a7328-f81f-11d2-ba4b-00a0c93ec93b}'
$script:BasicDataType = '{ebd0a0a2-b9e5-4433-87c0-68b6b72699c7}'

function Invoke-Native {
    param([string]$File, [string[]]$Arguments)
    $output = & $File @Arguments 2>&1 | ForEach-Object { [string]$_ }
    return [pscustomobject]@{ ExitCode = $LASTEXITCODE; Output = @($output) }
}

function Mount-Image {
    param([bool]$ReadOnly)
    $existing = Get-DiskImage -ImagePath $ImagePath
    if ($existing.Attached) { throw ('The disk ' + $ImagePath + ' is already attached on this computer. Detach it (or stop the VM using it) first.') }
    $access = 'ReadWrite'
    if ($ReadOnly) { $access = 'ReadOnly' }
    Mount-DiskImage -ImagePath $ImagePath -Access $access -NoDriveLetter | Out-Null
    $script:Mounted = $true
    $disk = $null
    for ($try = 0; $try -lt 40 -and -not $disk; $try++) {
        try { $disk = Get-DiskImage -ImagePath $ImagePath | Get-Disk -ErrorAction Stop } catch { Start-Sleep -Milliseconds 250 }
    }
    if (-not $disk) { throw 'Windows attached the disk but did not report it.' }
    if ($disk.IsOffline) {
        foreach ($other in @(Get-Disk | Where-Object { $_.Number -ne $disk.Number -and -not $_.IsOffline })) {
            $same = $false
            if ($disk.PartitionStyle -eq 'MBR' -and $other.PartitionStyle -eq 'MBR' -and $disk.Signature -and $disk.Signature -eq $other.Signature) { $same = $true }
            if ($disk.PartitionStyle -eq 'GPT' -and $other.PartitionStyle -eq 'GPT' -and $disk.Guid -and $disk.Guid -eq $other.Guid) { $same = $true }
            if ($same) { throw ('The converted disk has the same disk signature as disk ' + $other.Number + ' on this computer. Windows would change it, which can stop the copy booting. Run the fix-ups on another computer.') }
        }
        Set-Disk -Number $disk.Number -IsOffline $false
        $disk = Get-Disk -Number $disk.Number
    }
    if (-not $ReadOnly -and $disk.IsReadOnly) { Set-Disk -Number $disk.Number -IsReadOnly $false; $disk = Get-Disk -Number $disk.Number }
    return $disk
}

function Dismount-Image {
    foreach ($item in @($script:AccessPaths)) {
        try { Remove-PartitionAccessPath -DiskNumber $item.Disk -PartitionNumber $item.Partition -AccessPath $item.Path -ErrorAction Stop } catch { Write-RepairLog ('The mount folder ' + $item.Path + ' could not be removed: ' + $_.Exception.Message) 'WARN' }
        try { if (Test-Path -LiteralPath $item.Path) { Remove-Item -LiteralPath $item.Path -Force } } catch { }
    }
    $script:AccessPaths.Clear()
    if ($script:Mounted) {
        try { Dismount-DiskImage -ImagePath $ImagePath | Out-Null; $script:Mounted = $false } catch { Write-RepairLog ('The disk could not be detached: ' + $_.Exception.Message) 'ERROR' }
    }
}

function Get-BootMode {
    param([object]$Disk, [object[]]$Partitions)
    if ($Disk.PartitionStyle -eq 'GPT') {
        $esp = @($Partitions | Where-Object { [string]$_.GptType -eq $script:EspType })
        return [pscustomobject]@{ PartitionStyle = 'GPT'; Firmware = 'uefi'; SystemPartition = ($esp.Count -gt 0) }
    }
    $active = @($Partitions | Where-Object { $_.IsActive })
    return [pscustomobject]@{ PartitionStyle = [string]$Disk.PartitionStyle; Firmware = 'bios'; SystemPartition = ($active.Count -gt 0) }
}

function Open-Partitions {
    param([object]$Disk)
    $partitions = @(Get-Partition -DiskNumber $Disk.Number -ErrorAction SilentlyContinue)
    $windows = New-Object System.Collections.ArrayList
    foreach ($partition in $partitions) {
        if ($partition.Size -lt 1GB) { continue }
        if ($Disk.PartitionStyle -eq 'GPT' -and [string]$partition.GptType -ne $script:BasicDataType) { continue }
        $folder = Join-Path $WorkPath ('p' + $partition.PartitionNumber)
        New-Item -ItemType Directory -Path $folder -Force | Out-Null
        try {
            Add-PartitionAccessPath -DiskNumber $Disk.Number -PartitionNumber $partition.PartitionNumber -AccessPath ($folder + '\') -ErrorAction Stop
        } catch {
            Write-RepairLog ('Partition ' + $partition.PartitionNumber + ' could not be opened: ' + $_.Exception.Message) 'WARN'
            continue
        }
        [void]$script:AccessPaths.Add([pscustomobject]@{ Disk = $Disk.Number; Partition = $partition.PartitionNumber; Path = ($folder + '\') })
        if (Test-Path -LiteralPath (Join-Path $folder 'Windows\System32\config\SYSTEM')) {
            [void]$windows.Add([pscustomobject]@{ Partition = [int]$partition.PartitionNumber; Root = $folder })
        }
    }
    return [pscustomobject]@{ Partitions = $partitions; Windows = @($windows) }
}

function Mount-Hive {
    param([string]$File, [string]$Name)
    $key = $script:HivePrefix + $Name
    $result = Invoke-Native 'reg.exe' @('load', ('HKLM\' + $key), $File)
    if ($result.ExitCode -ne 0) { throw ('The registry hive ' + $File + ' could not be loaded: ' + ($result.Output -join ' ')) }
    [void]$script:LoadedHives.Add($key)
    return ('Registry::HKEY_LOCAL_MACHINE\' + $key)
}

function Dismount-Hives {
    foreach ($key in @($script:LoadedHives)) {
        $done = $false
        for ($try = 0; $try -lt 10 -and -not $done; $try++) {
            [GC]::Collect(); [GC]::WaitForPendingFinalizers()
            $result = Invoke-Native 'reg.exe' @('unload', ('HKLM\' + $key))
            if ($result.ExitCode -eq 0) { $done = $true } else { Start-Sleep -Milliseconds 300 }
        }
        if (-not $done) { Write-RepairLog ('The registry hive HKLM\' + $key + ' could not be unloaded.') 'ERROR' }
    }
    $script:LoadedHives.Clear()
}

function Get-Value {
    param([string]$Path, [string]$Name)
    try {
        $item = Get-ItemProperty -LiteralPath $Path -Name $Name -ErrorAction Stop
        return $item.$Name
    } catch { return $null }
}

function Get-OsTag {
    param([object]$Windows)
    $build = [int]$Windows.Build
    $server = [bool]$Windows.Server
    if ($Windows.Version -eq '6.1') { if ($server) { return '2k8R2' } else { return 'w7' } }
    if ($Windows.Version -eq '6.2') { if ($server) { return '2k12' } else { return 'w8' } }
    if ($Windows.Version -eq '6.3') { if ($server) { return '2k12R2' } else { return 'w8.1' } }
    if ($server) {
        if ($build -ge 26100) { return '2k25' }
        if ($build -ge 20348) { return '2k22' }
        if ($build -ge 17763) { return '2k19' }
        return '2k16'
    }
    if ($build -ge 22000) { return 'w11' }
    return 'w10'
}

# Newer tags first, so a missing exact folder falls back to the closest
# older one of the same family.
$script:OsTagOrder = @{
    server = @('2k25', '2k22', '2k19', '2k16', '2k12R2', '2k12', '2k8R2')
    client = @('w11', 'w10', 'w8.1', 'w8', 'w7')
}

# Check mode reads copies of the hives (with their logs), because Windows
# cannot load a hive from a read-only disk.
function Get-HiveFile {
    param([string]$Root, [string]$Name)
    $file = Join-Path $Root ('Windows\System32\config\' + $Name)
    if ($Mode -ne 'Check') { return $file }
    $copy = Join-Path $WorkPath ('hive-' + $Name + '-' + [guid]::NewGuid().ToString('N'))
    Copy-Item -LiteralPath $file -Destination $copy
    foreach ($log in @('.LOG1', '.LOG2')) { if (Test-Path -LiteralPath ($file + $log)) { Copy-Item -LiteralPath ($file + $log) -Destination ($copy + $log) } }
    return $copy
}

function Read-Windows {
    param([string]$Root)
    $info = [ordered]@{ Root = $Root; ProductName = ''; Version = ''; Build = 0; Edition = ''; Server = $false; Architecture = 'amd64'; OsTag = ''; ControlSet = ''; Drivers = @(); VMwareTools = $false; VMwareServices = @(); BootDriverStatus = @() }
    if (Test-Path -LiteralPath (Join-Path $Root 'Windows\SysArm32')) { $info.Architecture = 'ARM64' }
    elseif (-not (Test-Path -LiteralPath (Join-Path $Root 'Windows\SysWOW64'))) { $info.Architecture = 'x86' }
    $software = Mount-Hive (Get-HiveFile $Root 'SOFTWARE') 'SOFTWARE'
    $current = $software + '\Microsoft\Windows NT\CurrentVersion'
    $info.ProductName = [string](Get-Value $current 'ProductName')
    $info.Edition = [string](Get-Value $current 'EditionID')
    $build = [string](Get-Value $current 'CurrentBuildNumber')
    if (-not $build) { $build = [string](Get-Value $current 'CurrentBuild') }
    $parsed = 0
    if ([int]::TryParse($build, [ref]$parsed)) { $info.Build = $parsed }
    $major = Get-Value $current 'CurrentMajorVersionNumber'
    if ($major -ne $null) { $info.Version = ([string]$major + '.' + [string](Get-Value $current 'CurrentMinorVersionNumber')) } else { $info.Version = [string](Get-Value $current 'CurrentVersion') }
    $installation = [string](Get-Value $current 'InstallationType')
    $info.Server = ($installation -like 'Server*' -or $info.ProductName -like '*Server*')
    if (-not $info.Server -and $info.Build -ge 22000 -and $info.ProductName -like 'Windows 10*') { $info.ProductName = 'Windows 11' + $info.ProductName.Substring(10) }
    foreach ($uninstall in @($software + '\Microsoft\Windows\CurrentVersion\Uninstall', $software + '\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall')) {
        $parent = Get-Item -LiteralPath $uninstall -ErrorAction SilentlyContinue
        if (-not $parent) { Write-RepairLog ('No installed programs list under ' + $uninstall.Substring($software.Length + 1) + '.'); continue }
        $childNames = @($parent.GetSubKeyNames())
        Write-RepairLog ('Read ' + $childNames.Count + ' installed programs under ' + $uninstall.Substring($software.Length + 1) + '.')
        foreach ($childName in $childNames) {
            $child = $parent.OpenSubKey($childName)
            if (-not $child) { continue }
            try { if ([string]$child.GetValue('DisplayName') -eq 'VMware Tools') { $info.VMwareTools = $true } } finally { $child.Close() }
        }
        $parent.Close()
    }
    $info.OsTag = Get-OsTag ([pscustomobject]$info)
    $system = Mount-Hive (Get-HiveFile $Root 'SYSTEM') 'SYSTEM'
    $selected = Get-Value ($system + '\Select') 'Current'
    if ($selected -eq $null) { throw 'The SYSTEM hive has no current control set.' }
    $info.ControlSet = 'ControlSet{0:000}' -f [int]$selected
    $services = $system + '\' + $info.ControlSet + '\Services'
    $names = @($script:ReportedDrivers + $script:BootDrivers[$Target]) | Select-Object -Unique
    $drivers = @()
    foreach ($name in $names) { $drivers += Get-DriverState $Root $services $name }
    $info.Drivers = $drivers
    $info.VMwareServices = @($script:VMwareServices | Where-Object { Test-Path -LiteralPath ($services + '\' + $_) })
    Dismount-Hives
    return [pscustomobject]$info
}

function Get-DriverState {
    param([string]$Root, [string]$Services, [string]$Name)
    $key = $Services + '\' + $Name
    if (-not (Test-Path -LiteralPath $key)) { return [pscustomobject]@{ Name = $Name; Present = $false; Start = -1; Override = $false; File = $false } }
    $start = Get-Value $key 'Start'
    $image = [string](Get-Value $key 'ImagePath')
    $file = Test-Path -LiteralPath (Join-Path $Root ('Windows\System32\drivers\' + $Name + '.sys'))
    if ($image) {
        $relative = $image -replace '^\\SystemRoot\\', 'Windows\' -replace '^System32\\', 'Windows\System32\' -replace '^\\\?\?\\[A-Za-z]:\\', ''
        if ($relative -notmatch '^[A-Za-z]:') { $file = $file -or (Test-Path -LiteralPath (Join-Path $Root $relative)) }
    }
    $override = $false
    $overrideKey = $key + '\StartOverride'
    if (Test-Path -LiteralPath $overrideKey) { $override = @((Get-Item -LiteralPath $overrideKey).Property).Count -gt 0 }
    $value = -1
    if ($start -ne $null) { $value = [int]$start }
    return [pscustomobject]@{ Name = $Name; Present = $true; Start = $value; Override = $override; File = [bool]$file }
}

function Get-Findings {
    param([object]$Windows, [object]$Boot)
    $findings = New-Object System.Collections.ArrayList
    $add = { param($Severity, $Message) [void]$findings.Add([pscustomobject]@{ Severity = $Severity; Message = $Message }) }
    $state = @{}
    foreach ($driver in @($Windows.Drivers)) { $state[$driver.Name.ToLowerInvariant()] = $driver }
    $present = { param($n) $d = $state[$n.ToLowerInvariant()]; return ($d -and $d.Present -and $d.File) }
    switch ($Target) {
        'hyperv' {
            if ((& $present 'storvsc') -and (& $present 'vmbus')) { & $add 'good' 'The Hyper-V storage drivers (vmbus, storvsc) are in this Windows installation.' }
            else { & $add 'warning' 'The Hyper-V storage drivers were not found. Windows Server 2008 R2 and older need Hyper-V Integration Services; attach the disk to an IDE controller on a generation 1 VM.' }
            if ($Boot.Firmware -eq 'uefi') { & $add 'info' 'The disk boots with UEFI (GPT): create a generation 2 VM.' } else { & $add 'info' 'The disk boots with BIOS (MBR): create a generation 1 VM.' }
        }
        'kvm' {
            if ((& $present 'viostor') -or (& $present 'vioscsi')) { & $add 'good' 'A VirtIO storage driver is in this Windows installation.' }
            elseif ($DriverPath) { & $add 'info' 'The VirtIO drivers will be added from the folder you chose.' }
            else { & $add 'warning' 'No VirtIO storage driver is installed. Choose the VirtIO drivers folder (for example the virtio-win ISO, the Scale Computing guest tools or the Nutanix VirtIO package), or attach the disk as IDE or SATA on the new VM.' }
            if (-not (& $present 'netkvm')) { & $add 'info' 'No VirtIO network driver is installed yet; it is added with the VirtIO drivers folder, or use an e1000 adapter until it is installed.' }
        }
        'vmware' {
            if (& $present 'pvscsi') { & $add 'good' 'The VMware Paravirtual SCSI driver is installed.' }
            else { & $add 'info' 'The VMware Paravirtual SCSI driver is not installed: create the VM with an LSI Logic SAS controller, and install VMware Tools after the first start.' }
        }
        'physical' {
            & $add 'info' 'Add the new server''s storage controller and network drivers (from the server vendor) with the drivers folder, or it may not find its boot disk.'
        }
    }
    if ($Windows.VMwareTools -and $Target -ne 'vmware') {
        if ($DisableVMwareTools) { & $add 'info' 'The VMware Tools services will be turned off. Uninstall VMware Tools after the first start on the new platform.' }
        else { & $add 'info' 'VMware Tools is installed. Turn its services off here, or uninstall it after the first start on the new platform.' }
    }
    if ($Windows.Architecture -ne 'amd64') { & $add 'warning' ('This is a ' + $Windows.Architecture + ' Windows installation. Check that the drivers you add are for ' + $Windows.Architecture + '.') }
    return @($findings)
}

function Resolve-DriverFolders {
    param([object]$Windows)
    $resolved = New-Object System.Collections.ArrayList
    $arch = $Windows.Architecture
    $family = 'client'
    if ($Windows.Server) { $family = 'server' }
    $order = $script:OsTagOrder[$family]
    $start = [Array]::IndexOf($order, $Windows.OsTag)
    if ($start -lt 0) { $start = 0 }
    foreach ($root in @($DriverPath -split '\|')) {
        if (-not $root) { continue }
        if (-not (Test-Path -LiteralPath $root -PathType Container)) { throw ('The drivers folder ' + $root + ' does not exist.') }
        $layout = @($script:VirtIOFolders | Where-Object { Test-Path -LiteralPath (Join-Path $root $_) -PathType Container })
        if ($layout.Count -eq 0) {
            [void]$resolved.Add([pscustomobject]@{ Path = $root; Recurse = $true })
            continue
        }
        foreach ($name in $layout) {
            $found = $null
            for ($i = $start; $i -lt $order.Count -and -not $found; $i++) {
                $candidate = Join-Path (Join-Path (Join-Path $root $name) $order[$i]) $arch
                if (Test-Path -LiteralPath $candidate -PathType Container) { $found = $candidate }
            }
            if ($found) { [void]$resolved.Add([pscustomobject]@{ Path = $found; Recurse = $false }) }
            else { Write-RepairLog ('No ' + $name + ' driver for ' + $Windows.OsTag + ' ' + $arch + ' in ' + $root + '.') 'WARN' }
        }
    }
    return @($resolved)
}

function Get-ImageDrivers {
    param([string]$Root)
    $result = Invoke-Native 'dism.exe' @('/English', ('/Image:' + $Root), '/Get-Drivers', '/Format:Table')
    if ($result.ExitCode -ne 0) { throw ('DISM could not list the drivers in the image: ' + (($result.Output | Where-Object { $_ }) -join ' ')) }
    $names = @()
    foreach ($line in $result.Output) { if ($line -match '^\s*(oem\d+\.inf)\s*\|') { $names += $Matches[1].ToLowerInvariant() } }
    return $names
}

function Add-ImageDrivers {
    param([string]$Root, [object[]]$Folders)
    if ($Folders.Count -eq 0) { return @() }
    $before = Get-ImageDrivers $Root
    foreach ($folder in $Folders) {
        Write-RepairLog ('Adding drivers from ' + $folder.Path + '.')
        $arguments = @('/English', ('/Image:' + $Root), '/Add-Driver', ('/Driver:' + $folder.Path))
        if ($folder.Recurse) { $arguments += '/Recurse' }
        $result = Invoke-Native 'dism.exe' $arguments
        foreach ($line in $result.Output) { if ($line -match 'Installing|installed|Error|error') { Write-RepairLog ('  ' + $line.Trim()) } }
        if ($result.ExitCode -ne 0) { throw ('DISM could not add the drivers from ' + $folder.Path + ' (exit code ' + $result.ExitCode + '). This computer''s DISM must be as new as the Windows version in the disk.') }
    }
    $after = Get-ImageDrivers $Root
    return @($after | Where-Object { $before -notcontains $_ })
}

function Set-RecordedValue {
    param([System.Collections.ArrayList]$Changes, [string]$HiveRoot, [string]$Relative, [string]$Name, [object]$Value)
    $path = $HiveRoot + '\' + $Relative
    $old = Get-Value $path $Name
    if ($old -ne $null -and [string]$old -eq [string]$Value) { return }
    [void]$Changes.Add([pscustomobject]@{ Key = $Relative; Name = $Name; Old = $old; Existed = ($old -ne $null); New = $Value })
    New-ItemProperty -LiteralPath $path -Name $Name -PropertyType DWord -Value $Value -Force | Out-Null
}

function Invoke-Apply {
    param([object]$Disk, [object]$Opened, [object]$Windows)
    if (-not $RecordPath) { throw 'No folder for the fix-up record was given.' }
    New-Item -ItemType Directory -Path $RecordPath -Force | Out-Null
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $backup = Join-Path $RecordPath ('SYSTEM-' + $stamp)
    Copy-Item -LiteralPath (Join-Path $Windows.Root 'Windows\System32\config\SYSTEM') -Destination $backup
    $record = [ordered]@{ Schema = 1; EngineVersion = $script:EngineVersion; Image = $ImagePath; Partition = $Windows.Partition; Target = $Target; CreatedAt = (Get-Date).ToString('o'); SystemHiveBackup = $backup; ControlSet = $Windows.ControlSet; Drivers = @(); Changes = @(); Overrides = @(); Complete = $false }
    $recordFile = Join-Path $RecordPath ('Repair-' + $stamp + '.json')
    Write-JsonFile $recordFile $record
    Write-RepairLog ('Saved the SYSTEM hive and the fix-up record to ' + $RecordPath + '.')

    $folders = @(Resolve-DriverFolders $Windows)
    if ($folders.Count -gt 0) {
        $added = @(Add-ImageDrivers $Windows.Root $folders)
        $record.Drivers = $added
        Write-JsonFile $recordFile $record
        Write-RepairLog ('DISM added ' + $added.Count + ' driver package(s).')
    }

    $changes = New-Object System.Collections.ArrayList
    $overrides = New-Object System.Collections.ArrayList
    $system = Mount-Hive (Join-Path $Windows.Root 'Windows\System32\config\SYSTEM') 'SYSTEM'
    try {
        $services = $Windows.ControlSet + '\Services'
        foreach ($name in $script:BootDrivers[$Target]) {
            $relative = $services + '\' + $name
            $path = $system + '\' + $relative
            if (-not (Test-Path -LiteralPath $path)) { continue }
            $type = Get-Value $path 'Type'
            if ($type -ne $null -and [int]$type -ne 1) { continue }
            $state = Get-DriverState $Windows.Root ($system + '\' + $services) $name
            if (-not $state.File) { Write-RepairLog ('Skipped ' + $name + ': its driver file is not in the image.') 'WARN'; continue }
            Set-RecordedValue $changes $system $relative 'Start' 0
            $overrideKey = $path + '\StartOverride'
            if (Test-Path -LiteralPath $overrideKey) {
                $item = Get-Item -LiteralPath $overrideKey
                foreach ($property in @($item.Property)) {
                    [void]$overrides.Add([pscustomobject]@{ Key = ($relative + '\StartOverride'); Name = $property; Value = (Get-Value $overrideKey $property) })
                    Remove-ItemProperty -LiteralPath $overrideKey -Name $property
                }
            }
        }
        if ($DisableVMwareTools -and $Target -ne 'vmware') {
            foreach ($name in $script:VMwareServices) {
                $relative = $services + '\' + $name
                if (Test-Path -LiteralPath ($system + '\' + $relative)) { Set-RecordedValue $changes $system $relative 'Start' 4 }
            }
        }
        $record.Changes = @($changes)
        $record.Overrides = @($overrides)
        $record.Complete = $true
        Write-JsonFile $recordFile $record
    } finally {
        Dismount-Hives
    }
    foreach ($change in $changes) { Write-RepairLog ('Set ' + $change.Key + ' ' + $change.Name + ' to ' + $change.New + ' (was ' + $(if ($change.Existed) { [string]$change.Old } else { 'not set' }) + ').') }
    foreach ($override in $overrides) { Write-RepairLog ('Removed ' + $override.Key + ' ' + $override.Name + ' (was ' + $override.Value + ').') }
    return [pscustomobject]@{ Record = $recordFile; Drivers = @($record.Drivers); Changes = @($changes); Overrides = @($overrides) }
}

function Invoke-Undo {
    param([object]$Opened)
    if (-not $UndoRecord -or -not (Test-Path -LiteralPath $UndoRecord)) { throw 'The fix-up record was not found.' }
    $record = Get-Content -LiteralPath $UndoRecord -Raw | ConvertFrom-Json
    if ([IO.Path]::GetFullPath([string]$record.Image) -ne [IO.Path]::GetFullPath($ImagePath)) { throw 'The fix-up record belongs to another disk.' }
    $windows = @($Opened.Windows | Where-Object { $_.Partition -eq [int]$record.Partition })
    if ($windows.Count -ne 1) { throw 'The Windows partition named in the fix-up record was not found.' }
    $root = $windows[0].Root
    foreach ($driver in @($record.Drivers)) {
        if ([string]$driver -notmatch '^oem\d+\.inf$') { continue }
        Write-RepairLog ('Removing driver package ' + $driver + '.')
        $result = Invoke-Native 'dism.exe' @('/English', ('/Image:' + $root), '/Remove-Driver', ('/Driver:' + $driver))
        if ($result.ExitCode -ne 0) { Write-RepairLog ('DISM could not remove ' + $driver + ' (exit code ' + $result.ExitCode + ').') 'WARN' }
    }
    $system = Mount-Hive (Join-Path $root 'Windows\System32\config\SYSTEM') 'SYSTEM'
    $restored = 0
    try {
        foreach ($change in @($record.Changes)) {
            if ([string]$change.Key -notmatch '^ControlSet\d{3}\\Services\\[^\\]+$') { continue }
            $path = $system + '\' + $change.Key
            if (-not (Test-Path -LiteralPath $path)) { continue }
            if ($change.Existed) { New-ItemProperty -LiteralPath $path -Name $change.Name -PropertyType DWord -Value ([int]$change.Old) -Force | Out-Null }
            else { Remove-ItemProperty -LiteralPath $path -Name $change.Name -ErrorAction SilentlyContinue }
            $restored++
        }
        foreach ($override in @($record.Overrides)) {
            if ([string]$override.Key -notmatch '^ControlSet\d{3}\\Services\\[^\\]+\\StartOverride$') { continue }
            $path = $system + '\' + $override.Key
            if (-not (Test-Path -LiteralPath $path)) { continue }
            New-ItemProperty -LiteralPath $path -Name $override.Name -PropertyType DWord -Value ([int]$override.Value) -Force | Out-Null
            $restored++
        }
    } finally {
        Dismount-Hives
    }
    Write-RepairLog ('Put back ' + $restored + ' value(s).')
    return [pscustomobject]@{ Restored = $restored; Drivers = @($record.Drivers) }
}

$result = [ordered]@{ Ok = $false; Mode = $Mode; Image = $ImagePath; Target = $Target; Boot = $null; Windows = @(); Findings = @(); Applied = $null; Undone = $null; Error = '' }
try {
    New-Item -ItemType Directory -Path $WorkPath -Force | Out-Null
    $ImagePath = [IO.Path]::GetFullPath($ImagePath)
    $extension = [IO.Path]::GetExtension($ImagePath).ToLowerInvariant()
    if ($extension -ne '.vhd' -and $extension -ne '.vhdx') { throw 'Fix-ups work on VHD and VHDX files. Convert the disk to VHDX first.' }
    Write-RepairLog ('Opening ' + $ImagePath + ' ' + $(if ($Mode -eq 'Check') { 'read-only' } else { 'for changes' }) + '.')
    $disk = Mount-Image ($Mode -eq 'Check')
    $opened = Open-Partitions $disk
    $boot = Get-BootMode $disk $opened.Partitions
    $result.Boot = $boot
    if ($opened.Windows.Count -eq 0) { throw 'No Windows installation was found on this disk. Fix-ups apply to Windows system disks only.' }
    $windowsList = @()
    foreach ($item in $opened.Windows) {
        $info = Read-Windows $item.Root
        $info | Add-Member -NotePropertyName Partition -NotePropertyValue $item.Partition
        $windowsList += $info
    }
    $result.Windows = $windowsList
    if ($Mode -ne 'Undo') { $result.Findings = @(Get-Findings $windowsList[0] $boot) }
    if ($Mode -eq 'Apply') {
        if ($windowsList.Count -ne 1) { throw 'This disk holds more than one Windows installation. Fix-ups need exactly one.' }
        $result.Applied = Invoke-Apply $disk $opened $windowsList[0]
    } elseif ($Mode -eq 'Undo') {
        $result.Undone = Invoke-Undo $opened
    }
    $result.Ok = $true
} catch {
    $result.Error = $_.Exception.Message
    Write-RepairLog $result.Error 'ERROR'
} finally {
    Dismount-Hives
    Dismount-Image
    foreach ($item in @(Get-ChildItem -LiteralPath $WorkPath -ErrorAction SilentlyContinue)) { try { Remove-Item -LiteralPath $item.FullName -Force } catch { } }
}
foreach ($entry in @($result.Windows)) { $entry.PSObject.Properties.Remove('Root') }
Write-JsonFile $ResultPath $result
if ($result.Ok) { exit 0 } else { exit 1 }
