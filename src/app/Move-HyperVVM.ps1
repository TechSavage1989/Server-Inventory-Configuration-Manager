param(
    [Parameter(Mandatory = $true)][ValidateSet('State', 'Checkpoint', 'RemoveCheckpoint', 'Shutdown', 'Start')][string]$Mode,
    [Parameter(Mandatory = $true)][string]$VMId,
    [string]$ComputerName = '',
    [string]$CheckpointId = '',
    [string]$VMName = '',
    [Parameter(Mandatory = $true)][string]$ResultPath
)

# Host steps for copying a Hyper-V VM while it runs. The only changes it can
# make are: create this application's own checkpoint (named below), remove
# that same checkpoint (found by its ID and name, never any other), and ask
# the guest to shut down through the shutdown integration service (never a
# forced power-off), and, for Roll back after a cutover, start the old VM
# again (Start, only when its ID and -VMName still match). State only reads. It uses WMI (root\virtualization\v2)
# with the account running it, locally or on -ComputerName, and needs no
# Hyper-V PowerShell module.
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

function New-Checkpoint {
    $vm = Get-VM
    foreach ($snapshot in (Get-Snapshots)) {
        if ([string]$snapshot.ElementName -eq $CheckpointName) { throw ('The VM already has a checkpoint named "' + $CheckpointName + '". Delete it in Hyper-V Manager first.') }
    }
    $before = @{}
    foreach ($snapshot in (Get-Snapshots)) { $before[[string]$snapshot.InstanceID] = $true }
    $service = Get-Wmi -Class 'Msvm_VirtualSystemSnapshotService' | Select-Object -First 1
    Write-Log ('Creating checkpoint "' + $CheckpointName + '".')
    # SnapshotType 2: a full checkpoint, of the type the VM's settings choose
    # (production, falling back to standard where the VM allows it).
    Wait-HyperVJob ($service.CreateSnapshot($vm.__PATH, $null, 2)) 'Creating the checkpoint'
    $created = @(Get-Snapshots | Where-Object { -not $before.ContainsKey([string]$_.InstanceID) })
    if ($created.Count -ne 1) { throw ('Hyper-V reported ' + $created.Count + ' new checkpoints; expected one. Check the VM''s checkpoints in Hyper-V Manager.') }
    $snapshot = $created[0]
    $snapshot.ElementName = $CheckpointName
    $management = Get-Wmi -Class 'Msvm_VirtualSystemManagementService' | Select-Object -First 1
    try {
        Wait-HyperVJob ($management.ModifySystemSettings($snapshot.GetText(2))) 'Naming the checkpoint'
    } catch {
        Write-Log ('The checkpoint could not be named; it is removed by its ID. ' + $_.Exception.Message)
    }
    return @{ id = [string]$snapshot.InstanceID; disks = @(Get-Disks ([string]$snapshot.InstanceID)) }
}

function Remove-Checkpoint {
    if (-not $CheckpointId) { throw 'No checkpoint was named.' }
    $snapshot = Get-Snapshots | Where-Object { [string]$_.InstanceID -eq $CheckpointId } | Select-Object -First 1
    if (-not $snapshot) { Write-Log 'The checkpoint is already gone.'; return @{ removed = $false } }
    if ([string]$snapshot.ElementName -ne $CheckpointName -and -not ([string]$snapshot.ElementName).StartsWith([string](Get-VM).ElementName)) {
        throw ('The checkpoint with this ID is "' + $snapshot.ElementName + '", not this application''s; it was left alone.')
    }
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
