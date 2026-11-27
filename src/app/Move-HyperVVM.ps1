param(
    [Parameter(Mandatory = $true)][ValidateSet('State', 'Checkpoint', 'TrackingCheckpoint', 'KeepReferencePoint', 'DiskChanges', 'RemoveReferencePoint', 'RemoveCheckpoint', 'Shutdown', 'Start')][string]$Mode,
    [Parameter(Mandatory = $true)][string]$VMId,
    [string]$ComputerName = '',
    [string]$CheckpointId = '',
    [string]$ReferencePointId = '',
    [string]$DiskPath = '',
    [string]$LimitId = '',
    [long]$DiskSize = 0,
    [string]$VMName = '',
    [Parameter(Mandatory = $true)][string]$ResultPath
)

# Host steps for copying a Hyper-V VM while it runs. The only changes it can
# make are: create this application's own checkpoint (named below; a
# recovery checkpoint when change tracking is wanted), remove that same
# checkpoint (found by its ID and name, never any other), keep that same
# recovery checkpoint as a change tracking reference point, remove a
# reference point of this VM whose ID the application recorded when it kept
# it, ask the guest to shut down through the shutdown integration service
# (never a forced power-off), and, for Roll back after a cutover, start the
# old VM again (Start, only when its ID and -VMName still match). State and
# DiskChanges only read. It uses WMI (root\virtualization\v2) with the
# account running it, locally or on -ComputerName, and needs no Hyper-V
# PowerShell module.
# Compatible with Windows PowerShell 3.0 (Windows Server 2012) and later.

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }

$CheckpointName = 'Server Inventory and Migration Manager copy'
$Namespace = 'root\virtualization\v2'

function Write-Line { param([string]$Text) [Console]::Out.WriteLine($Text); [Console]::Out.Flush() }
function Write-Log { param([string]$Message) Write-Line ('{0} {1}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Message) }
function Write-Result {
    param([bool]$Ok, [string]$ErrorText, $Result)
    $json = ConvertTo-Json -InputObject @{ ok = $Ok; error = $ErrorText; result = $Result } -Depth 8 -Compress
    [IO.File]::WriteAllText($ResultPath, $json, (New-Object System.Text.UTF8Encoding($false)))
}

function Get-Wmi {
    param([string]$Class, [string]$Filter = '')
    $arguments = @{ Namespace = $Namespace; Class = $Class }
    if ($Filter) { $arguments.Filter = $Filter }
    if ($ComputerName) { $arguments.ComputerName = $ComputerName }
    return @(Get-WmiObject @arguments)
}

function Get-VM {
    if ($VMId -notmatch '^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$') { throw 'The VM ID is not a Hyper-V VM ID.' }
    $vm = Get-Wmi -Class 'Msvm_ComputerSystem' -Filter ("Name='" + $VMId + "'") | Select-Object -First 1
    if (-not $vm) { throw 'The VM was not found on the Hyper-V host. Read the host again.' }
    return $vm
}

function Get-PowerState {
    param($VM)
    switch ([int]$VM.EnabledState) {
        2 { return 'poweredOn' }
        3 { return 'poweredOff' }
        default { return 'suspended' }
    }
}

# A drive's key is its InstanceID after the first backslash: the same for
# the VM's own settings and for each checkpoint's copy of them.
function Get-DiskKey {
    param([string]$InstanceID)
    $index = $InstanceID.IndexOf('\')
    if ($index -lt 0) { return $InstanceID }
    return $InstanceID.Substring($index + 1)
}

function Get-Disks {
    param([string]$SettingsInstanceID)
    $prefix = $SettingsInstanceID + '\'
    $disks = @()
    foreach ($drive in (Get-Wmi -Class 'Msvm_StorageAllocationSettingData')) {
        if (-not ([string]$drive.InstanceID).StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) { continue }
        if ([int]$drive.ResourceType -ne 31 -or -not $drive.HostResource) { continue }
        $disks += @{ key = (Get-DiskKey ([string]$drive.InstanceID)); path = [string](@($drive.HostResource)[0]) }
    }
    return $disks
}

function Get-WmiClassObject {
    param([string]$Class)
    $server = '.'
    if ($ComputerName) { $server = $ComputerName }
    return [wmiclass]('\\' + $server + '\' + $Namespace + ':' + $Class)
}

# Reference points exist from Windows Server 2016; an older host has no
# class for them, which reads as none.
function Get-ReferencePoints {
    try {
        return @(Get-Wmi -Class 'Msvm_VirtualSystemReferencePoint' -Filter ("VirtualSystemIdentifier='" + $VMId + "'"))
    } catch {
        return @()
    }
}

function Get-Snapshots {
    return @(Get-Wmi -Class 'Msvm_VirtualSystemSettingData' | Where-Object { [string]$_.VirtualSystemIdentifier -ieq $VMId -and ([string]$_.VirtualSystemType) -like 'Microsoft:Hyper-V:Snapshot:*' })
}

function Wait-HyperVJob {
    param($Result, [string]$Action)
    $code = [int]$Result.ReturnValue
    if ($code -eq 0) { return }
    if ($code -ne 4096) { throw ($Action + ' failed (code ' + $code + ').') }
    $job = [wmi]$Result.Job
    while ([int]$job.JobState -eq 3 -or [int]$job.JobState -eq 4) {
        Start-Sleep -Seconds 2
        $job = [wmi]$Result.Job
    }
    if ([int]$job.JobState -ne 7) {
        $text = [string]$job.ErrorDescription
        if (-not $text) { $text = 'state ' + $job.JobState }
        throw ($Action + ' failed: ' + $text)
    }
}

function Get-State {
    $vm = Get-VM
    $current = Get-Wmi -Class 'Msvm_VirtualSystemSettingData' | Where-Object { [string]$_.VirtualSystemIdentifier -ieq $VMId -and [string]$_.VirtualSystemType -eq 'Microsoft:Hyper-V:System:Realized' } | Select-Object -First 1
    if (-not $current) { throw 'The VM''s settings could not be read.' }
    $names = @()
    foreach ($snapshot in (Get-Snapshots)) { $names += [string]$snapshot.ElementName }
    return @{ name = [string]$vm.ElementName; powerState = (Get-PowerState $vm); checkpoints = $names; disks = @(Get-Disks ([string]$current.InstanceID)) }
}

function Assert-NoOwnCheckpoint {
    foreach ($snapshot in (Get-Snapshots)) {
        if ([string]$snapshot.ElementName -eq $CheckpointName) { throw ('The VM already has a checkpoint named "' + $CheckpointName + '". Delete it in Hyper-V Manager first.') }
    }
}

function Get-SnapshotIds {
    $ids = @{}
    foreach ($snapshot in (Get-Snapshots)) { $ids[[string]$snapshot.InstanceID] = $true }
    return $ids
}

# Finds the one checkpoint created since $Before and gives it this
# application's name.
function Complete-Checkpoint {
    param($Before, [bool]$Recovery, [string]$Note)
    $created = @(Get-Snapshots | Where-Object { -not $Before.ContainsKey([string]$_.InstanceID) })
    if ($created.Count -ne 1) { throw ('Hyper-V reported ' + $created.Count + ' new checkpoints; expected one. Check the VM''s checkpoints in Hyper-V Manager.') }
    $snapshot = $created[0]
    $snapshot.ElementName = $CheckpointName
    $management = Get-Wmi -Class 'Msvm_VirtualSystemManagementService' | Select-Object -First 1
    try {
        Wait-HyperVJob ($management.ModifySystemSettings($snapshot.GetText(2))) 'Naming the checkpoint'
    } catch {
        Write-Log ('The checkpoint could not be named; it is removed by its ID. ' + $_.Exception.Message)
    }
    return @{ id = [string]$snapshot.InstanceID; disks = @(Get-Disks ([string]$snapshot.InstanceID)); recovery = $Recovery; note = $Note }
}

function New-Checkpoint {
    param([string]$Note = '')
    $vm = Get-VM
    Assert-NoOwnCheckpoint
    $before = Get-SnapshotIds
    $service = Get-Wmi -Class 'Msvm_VirtualSystemSnapshotService' | Select-Object -First 1
    Write-Log ('Creating checkpoint "' + $CheckpointName + '".')
    # SnapshotType 2: a full checkpoint, of the type the VM's settings choose
    # (production, falling back to standard where the VM allows it).
    Wait-HyperVJob ($service.CreateSnapshot($vm.__PATH, $null, 2)) 'Creating the checkpoint'
    return Complete-Checkpoint $before $false $Note
}

# A recovery checkpoint (SnapshotType 32768, the type Hyper-V backups use)
# can become a change tracking reference point. It is application consistent
# when the guest's backup integration service answers, with a copy backup
# inside the guest so application backup history is untouched, else crash
# consistent. A VM that cannot have one gets an ordinary checkpoint.
function New-TrackingCheckpoint {
    $vm = Get-VM
    Assert-NoOwnCheckpoint
    $before = Get-SnapshotIds
    $service = Get-Wmi -Class 'Msvm_VirtualSystemSnapshotService' | Select-Object -First 1
    $note = ''
    $settingsClass = $null
    try {
        $settingsClass = Get-WmiClassObject 'Msvm_VirtualSystemSnapshotSettingData'
    } catch {
        $note = 'this host has no recovery checkpoints (Windows Server 2016 or later is needed)'
    }
    if ($settingsClass) {
        foreach ($level in @(1, 2)) {
            $settings = $settingsClass.CreateInstance()
            $settings.ConsistencyLevel = $level
            $settings.IgnoreNonSnapshottableDisks = $false
            try { $settings.GuestBackupType = 2 } catch { }
            $kind = 'application consistent'
            if ($level -eq 2) { $kind = 'crash consistent' }
            Write-Log ('Creating checkpoint "' + $CheckpointName + '" as a ' + $kind + ' recovery checkpoint for change tracking.')
            try {
                Wait-HyperVJob ($service.CreateSnapshot($vm.__PATH, $settings.GetText(2), 32768)) 'Creating the recovery checkpoint'
                return Complete-Checkpoint $before $true ''
            } catch {
                $note = $_.Exception.Message
                if (@(Get-Snapshots | Where-Object { -not $before.ContainsKey([string]$_.InstanceID) }).Count -gt 0) { throw }
            }
        }
    }
    Write-Log ('Hyper-V change tracking is not available for this VM (' + $note + '); using an ordinary checkpoint.')
    return New-Checkpoint $note
}

function Get-OwnSnapshot {
    $snapshot = Get-Snapshots | Where-Object { [string]$_.InstanceID -eq $CheckpointId } | Select-Object -First 1
    if (-not $snapshot) { return $null }
    if ([string]$snapshot.ElementName -ne $CheckpointName -and -not ([string]$snapshot.ElementName).StartsWith([string](Get-VM).ElementName)) {
        throw ('The checkpoint with this ID is "' + $snapshot.ElementName + '", not this application''s; it was left alone.')
    }
    return $snapshot
}

function Remove-OwnReferencePoint {
    param([string]$Id)
    $point = Get-ReferencePoints | Where-Object { [string]$_.InstanceID -eq $Id } | Select-Object -First 1
    if (-not $point) { Write-Log 'The earlier reference point is already gone.'; return $false }
    $service = Get-Wmi -Class 'Msvm_VirtualSystemReferencePointService' | Select-Object -First 1
    $in = $service.GetMethodParameters('DestroyReferencePoint')
    $in.AffectedReferencePoint = [string]$point.__PATH
    Wait-HyperVJob ($service.InvokeMethod('DestroyReferencePoint', $in, $null)) 'Removing the reference point'
    Write-Log 'Removed the earlier change tracking reference point.'
    return $true
}

function Convert-CheckpointToReferencePoint {
    if (-not $CheckpointId) { throw 'No checkpoint was named.' }
    $snapshot = Get-OwnSnapshot
    if (-not $snapshot) { throw 'The checkpoint is gone, so it could not be kept for change tracking.' }
    if ([string]$snapshot.VirtualSystemType -ne 'Microsoft:Hyper-V:Snapshot:Recovery') { throw 'The checkpoint is not a recovery checkpoint.' }
    $before = @{}
    foreach ($existing in (Get-ReferencePoints)) { $before[[string]$existing.InstanceID] = $true }
    $service = Get-Wmi -Class 'Msvm_VirtualSystemSnapshotService' | Select-Object -First 1
    $in = $service.GetMethodParameters('ConvertToReferencePoint')
    $in.AffectedSnapshot = [string]$snapshot.__PATH
    Write-Log 'Keeping the checkpoint as a change tracking reference point (Hyper-V merges the checkpoint into the disks).'
    $result = $service.InvokeMethod('ConvertToReferencePoint', $in, $null)
    Wait-HyperVJob $result 'Keeping the reference point'
    $point = $null
    if ($result.ResultingReferencePoint) {
        try { $point = [wmi][string]$result.ResultingReferencePoint } catch { $point = $null }
    }
    if (-not $point) {
        $created = @(Get-ReferencePoints | Where-Object { -not $before.ContainsKey([string]$_.InstanceID) })
        if ($created.Count -ne 1) { throw ('Hyper-V reported ' + $created.Count + ' new reference points; expected one.') }
        $point = $created[0]
    }
    if ([string]$point.VirtualSystemIdentifier -ine $VMId) { throw 'Hyper-V returned a reference point of another VM.' }
    $known = @{}
    foreach ($disk in (Get-Disks ([string]$snapshot.InstanceID))) { $known[([string]$disk.key).ToLowerInvariant()] = $true }
    $current = Get-Wmi -Class 'Msvm_VirtualSystemSettingData' | Where-Object { [string]$_.VirtualSystemIdentifier -ieq $VMId -and [string]$_.VirtualSystemType -eq 'Microsoft:Hyper-V:System:Realized' } | Select-Object -First 1
    if ($current) {
        foreach ($disk in (Get-Disks ([string]$current.InstanceID))) { $known[([string]$disk.key).ToLowerInvariant()] = $true }
    }
    $ids = @($point.VirtualDiskIdentifiers)
    $rcts = @($point.ResilientChangeTrackingIdentifiers)
    $disks = @()
    $unmatched = @()
    for ($index = 0; $index -lt $ids.Count -and $index -lt $rcts.Count; $index++) {
        $key = Get-DiskKey ([string]$ids[$index])
        if ($known.ContainsKey($key.ToLowerInvariant())) {
            $disks += @{ key = $key; rct = [string]$rcts[$index] }
        } else {
            $unmatched += [string]$ids[$index]
        }
    }
    $removed = $false
    if ($ReferencePointId -and $ReferencePointId -ne [string]$point.InstanceID) {
        try {
            $removed = Remove-OwnReferencePoint $ReferencePointId
        } catch {
            Write-Log ('The earlier reference point could not be removed: ' + $_.Exception.Message)
        }
    }
    return @{ id = [string]$point.InstanceID; disks = $disks; unmatched = $unmatched; removedPrevious = $removed }
}

# Lists the areas of a virtual hard disk written since the RCT ID, rounded
# out to whole megabytes and joined, so the answer stays small.
function Get-DiskChanges {
    if (-not $DiskPath) { throw 'No disk was named.' }
    if (-not $LimitId) { throw 'No change tracking ID was given.' }
    if ($DiskSize -le 0) { throw 'The disk size was not given.' }
    [void](Get-VM)
    $service = Get-Wmi -Class 'Msvm_ImageManagementService' | Select-Object -First 1
    $unit = [long]1048576
    $offsets = New-Object 'System.Collections.Generic.List[long]'
    $lengths = New-Object 'System.Collections.Generic.List[long]'
    $position = [long]0
    while ($position -lt $DiskSize) {
        $in = $service.GetMethodParameters('GetVirtualDiskChanges')
        $in.Path = $DiskPath
        $in.LimitId = $LimitId
        $in.ByteOffset = [uint64]$position
        $in.ByteLength = [uint64]($DiskSize - $position)
        $out = $service.InvokeMethod('GetVirtualDiskChanges', $in, $null)
        $code = [int]$out.ReturnValue
        if ($code -eq 4096) {
            Wait-HyperVJob $out 'Listing the changed areas'
        } elseif ($code -ne 0) {
            throw ('Hyper-V could not list the changed areas (code ' + $code + ').')
        }
        $processed = [long]$out.ProcessedByteLength
        if ($processed -le 0) { throw 'Hyper-V reported no progress listing the changed areas.' }
        $changedOffsets = @($out.ChangedByteOffsets)
        $changedLengths = @($out.ChangedByteLengths)
        if ($changedOffsets.Count -ne $changedLengths.Count) { throw 'Hyper-V gave a different number of offsets and lengths.' }
        for ($index = 0; $index -lt $changedOffsets.Count; $index++) {
            $start = [long]$changedOffsets[$index]
            $end = $start + [long]$changedLengths[$index]
            if ([long]$changedLengths[$index] -le 0) { continue }
            $start = $start - ($start % $unit)
            if ($end % $unit -ne 0) { $end = $end + $unit - ($end % $unit) }
            if ($end -gt $DiskSize) { $end = $DiskSize }
            $last = $offsets.Count - 1
            if ($last -ge 0 -and $start -le ($offsets[$last] + $lengths[$last])) {
                $joined = [Math]::Max($offsets[$last] + $lengths[$last], $end)
                $lengths[$last] = $joined - $offsets[$last]
            } else {
                $offsets.Add($start)
                $lengths.Add($end - $start)
            }
        }
        $position += $processed
    }
    Write-Log ('Hyper-V change tracking listed ' + $offsets.Count + ' changed areas.')
    return @{ offsets = $offsets.ToArray(); lengths = $lengths.ToArray() }
}

function Remove-ReferencePoint {
    if (-not $ReferencePointId) { throw 'No reference point was named.' }
    [void](Get-VM)
    return @{ removed = (Remove-OwnReferencePoint $ReferencePointId) }
}

function Remove-Checkpoint {
    if (-not $CheckpointId) { throw 'No checkpoint was named.' }
    $snapshot = Get-OwnSnapshot
    if (-not $snapshot) { Write-Log 'The checkpoint is already gone.'; return @{ removed = $false } }
    $service = Get-Wmi -Class 'Msvm_VirtualSystemSnapshotService' | Select-Object -First 1
    Write-Log 'Removing the checkpoint (Hyper-V merges it into the disks).'
    Wait-HyperVJob ($service.DestroySnapshot($snapshot.__PATH)) 'Removing the checkpoint'
    return @{ removed = $true }
}

function Stop-Guest {
    $vm = Get-VM
    if ([int]$vm.EnabledState -eq 3) { return @{ powerState = 'poweredOff' } }
    if ([int]$vm.EnabledState -ne 2) { throw 'The VM is saved or paused. Start it, or shut it down yourself, then cut over again.' }
    $component = Get-Wmi -Class 'Msvm_ShutdownComponent' -Filter ("SystemName='" + $VMId + "'") | Select-Object -First 1
    if (-not $component) { throw 'The guest has no shutdown integration service. Shut the guest down from inside it, then cut over again.' }
    Write-Log 'Asking the guest to shut down.'
    $code = [int]($component.InitiateShutdown($false, 'Migration cutover by Server Inventory and Migration Manager')).ReturnValue
    if ($code -ne 0 -and $code -ne 4096) { throw ('The guest refused to shut down (code ' + $code + '). Shut it down from inside the guest, then cut over again.') }
    $deadline = (Get-Date).AddMinutes(20)
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Seconds 5
        $vm = Get-VM
        if ([int]$vm.EnabledState -eq 3) { Write-Log 'The VM is off.'; return @{ powerState = 'poweredOff' } }
    }
    return @{ powerState = (Get-PowerState $vm) }
}

# Start-Source starts the old VM for a rollback (rollback.go), after the new
# VM was confirmed off. Its name must still match; nothing else changes.
function Start-Source {
    $vm = Get-VM
    if (-not $VMName -or [string]$vm.ElementName -ne $VMName) { throw ('The VM with this ID is now named ' + [string]$vm.ElementName + '; it was not started.') }
    if ([int]$vm.EnabledState -eq 2) { Write-Log 'The old VM is already running.'; return @{ powerState = 'poweredOn' } }
    Write-Log ('Starting the old VM ' + $VMName + '.')
    Wait-HyperVJob ($vm.RequestStateChange(2)) 'Starting the VM'
    $vm = Get-VM
    return @{ powerState = (Get-PowerState $vm) }
}

try {
    switch ($Mode) {
        'State' { $result = Get-State }
        'Checkpoint' { $result = New-Checkpoint }
        'TrackingCheckpoint' { $result = New-TrackingCheckpoint }
        'KeepReferencePoint' { $result = Convert-CheckpointToReferencePoint }
        'DiskChanges' { $result = Get-DiskChanges }
        'RemoveReferencePoint' { $result = Remove-ReferencePoint }
        'RemoveCheckpoint' { $result = Remove-Checkpoint }
        'Shutdown' { $result = Stop-Guest }
        'Start' { $result = Start-Source }
    }
    Write-Result $true '' $result
} catch {
    $message = $_.Exception.Message
    if ($message -match 'Invalid namespace') { $message = 'Hyper-V is not installed on this host (it has no root\virtualization\v2 namespace).' }
    Write-Log ('Failed: ' + $message)
    Write-Result $false $message $null
}
