param(
    [string]$ComputerName = '',
    [Parameter(Mandatory = $true)][string]$ResultPath,
    [switch]$CredentialFromInput
)

# Reads a Hyper-V host's virtual machines for migration planning. Read only:
# it queries the root\virtualization\v2 CIM namespace and calls only the
# read-only GetVirtualHardDiskSettingData and GetVirtualHardDiskState methods.
# Nothing on the host or its VMs is changed. It needs no Hyper-V PowerShell
# module, so it can read a remote host from any Windows computer.
# With no -ComputerName the local computer is read. With -CredentialFromInput
# the account arrives as one JSON line on standard input, never on a command
# line. The result is written to -ResultPath as {"ok":..,"error":..,"result":..}.
# Compatible with Windows PowerShell 3.0 (Windows Server 2012) and later.

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }

function Write-Line { param([string]$Text) [Console]::Out.WriteLine($Text); [Console]::Out.Flush() }
function Write-Log { param([string]$Message) Write-Line ('{0} {1}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Message) }
function Write-Result {
    param([bool]$Ok, [string]$ErrorText, $Result)
    $json = ConvertTo-Json -InputObject @{ ok = $Ok; error = $ErrorText; result = $Result } -Depth 10 -Compress
    [IO.File]::WriteAllText($ResultPath, $json, (New-Object System.Text.UTF8Encoding($false)))
}

$script:Namespace = 'root\virtualization\v2'
$script:Session = $null
$script:Warnings = New-Object System.Collections.ArrayList

function Add-Warning { param([string]$Text) [void]$script:Warnings.Add($Text); Write-Log ('Warning: ' + $Text) }

function Get-Cim {
    param([string]$Class, [string]$Filter = '', [string]$Namespace = $script:Namespace)
    $arguments = @{ Namespace = $Namespace; ClassName = $Class }
    if ($Filter) { $arguments.Filter = $Filter }
    if ($script:Session) { $arguments.CimSession = $script:Session }
    return @(Get-CimInstance @arguments)
}

function Get-OptionalCim {
    param([string]$Class, [string]$Filter = '')
    try { return Get-Cim -Class $Class -Filter $Filter } catch { return @() }
}

# The VM's GUID is the part of an InstanceID between "Microsoft:" and the
# first backslash or colon after it.
function Get-VmGuid {
    param([string]$InstanceID)
    if ($InstanceID -match '^Microsoft:([0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12})') { return $Matches[1].ToUpperInvariant() }
    if ($InstanceID -match '^Microsoft:GuestNetwork\\([0-9A-Fa-f-]{36})') { return $Matches[1].ToUpperInvariant() }
    return ''
}

# CIM references (Parent, HostResource) are object paths; their InstanceID
# key is what links settings together.
function Get-PathInstanceID {
    param([string]$Path)
    if ($Path -match 'InstanceID="((?:[^"\\]|\\.)*)"') { return ($Matches[1] -replace '\\\\', '\') }
    return ''
}

function Get-PathName {
    param([string]$Path)
    if ($Path -match '\bName="((?:[^"\\]|\\.)*)"') { return ($Matches[1] -replace '\\\\', '\') }
    return ''
}

function Get-EmbeddedProperty {
    param([string]$Xml, [string]$Name)
    try {
        $document = [xml]$Xml
        foreach ($property in $document.INSTANCE.PROPERTY) {
            if ($property.NAME -eq $Name) { return [string]$property.VALUE }
        }
    } catch { }
    return ''
}

function Get-VhdDetails {
    param([string]$Path)
    $details = @{ format = ''; type = ''; maxInternalSize = 0; fileSize = 0; parentPath = ''; error = '' }
    try {
        $service = Get-Cim -Class 'Msvm_ImageManagementService' | Select-Object -First 1
        $arguments = @{ InputObject = $service; MethodName = 'GetVirtualHardDiskSettingData'; Arguments = @{ Path = $Path } }
        if ($script:Session) { $arguments.CimSession = $script:Session }
        $result = Invoke-CimMethod @arguments
        if ($result.ReturnValue -ne 0 -or -not $result.SettingData) { $details.error = 'Hyper-V could not read this disk (code ' + $result.ReturnValue + ').'; return $details }
        $details.format = Get-EmbeddedProperty $result.SettingData 'Format'
        $details.type = Get-EmbeddedProperty $result.SettingData 'Type'
        $size = Get-EmbeddedProperty $result.SettingData 'MaxInternalSize'
        if ($size) { $details.maxInternalSize = [int64]$size }
        $details.parentPath = Get-EmbeddedProperty $result.SettingData 'ParentPath'
        $arguments.MethodName = 'GetVirtualHardDiskState'
        $state = Invoke-CimMethod @arguments
        if ($state.ReturnValue -eq 0 -and $state.State) {
            $fileSize = Get-EmbeddedProperty $state.State 'FileSize'
            if ($fileSize) { $details.fileSize = [int64]$fileSize }
        }
    } catch {
        $details.error = $_.Exception.Message
    }
    return $details
}

function Get-KvpItems {
    param($Component)
    $items = @{}
    if (-not $Component -or -not $Component.GuestIntrinsicExchangeItems) { return $items }
    foreach ($text in $Component.GuestIntrinsicExchangeItems) {
        $name = Get-EmbeddedProperty $text 'Name'
        if ($name) { $items[$name] = Get-EmbeddedProperty $text 'Data' }
    }
    return $items
}

function Read-Host-Inventory {
    $computer = $ComputerName.Trim()
    $remote = $computer -and -not ($computer -eq '.' -or $computer -eq 'localhost' -or $computer -ieq $env:COMPUTERNAME)
    $credential = $null
    $account = ''
    if ($CredentialFromInput) {
        $line = [Console]::In.ReadLine()
        $data = $null
        if ($line) { $data = ConvertFrom-Json -InputObject $line }
        $line = $null
        if (-not $data -or -not [string]$data.user -or -not [string]$data.password) { throw 'The account for the Hyper-V host was not provided.' }
        $account = [string]$data.user
        $credential = New-Object System.Management.Automation.PSCredential($account, (ConvertTo-SecureString -String ([string]$data.password) -AsPlainText -Force))
        $data = $null
    }
    if ($remote) {
        Write-Log ('Connecting to ' + $computer + '.')
        $sessionArguments = @{ ComputerName = $computer; ErrorAction = 'Stop' }
        if ($credential) { $sessionArguments.Credential = $credential }
        try {
            $script:Session = New-CimSession @sessionArguments
        } catch {
            $first = $_.Exception.Message
            Write-Log ('Remote management (WinRM) did not answer; trying DCOM. ' + $first)
            try {
                $sessionArguments.SessionOption = New-CimSessionOption -Protocol Dcom
                $script:Session = New-CimSession @sessionArguments
            } catch {
                throw ('Could not connect to ' + $computer + ': ' + $first)
            }
        }
    } else {
        $computer = $env:COMPUTERNAME
        if ($credential) { Write-Log 'The account is used only for a remote host; this computer is read as the current user.' }
    }

    try {
        # The host itself is also an Msvm_ComputerSystem; VMs are the ones
        # named by a GUID (Caption is localized, so it is not used).
        $systems = @(Get-Cim -Class 'Msvm_ComputerSystem' | Where-Object { [string]$_.Name -match '^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$' })
    } catch {
        $text = $_.Exception.Message
        if ($text -match 'Invalid namespace|namespace') { throw ('Hyper-V is not installed on ' + $computer + ' (it has no root\virtualization\v2 namespace).') }
        if ($text -match 'Access is denied|Access denied') { throw ('Access is denied on ' + $computer + '. Use an account in the host''s Hyper-V Administrators or Administrators group.') }
        throw
    }
    Write-Log ('Found ' + $systems.Count + ' VM(s) on ' + $computer + '.')

    $hostInfo = @{ name = $computer; account = $account }
    try {
        $cs = Get-Cim -Namespace 'root\cimv2' -Class 'Win32_ComputerSystem' | Select-Object -First 1
        $os = Get-Cim -Namespace 'root\cimv2' -Class 'Win32_OperatingSystem' | Select-Object -First 1
        $cpus = Get-Cim -Namespace 'root\cimv2' -Class 'Win32_Processor'
        $hostInfo.dnsName = [string]$cs.DNSHostName
        $hostInfo.domain = [string]$cs.Domain
        $hostInfo.vendor = [string]$cs.Manufacturer
        $hostInfo.model = [string]$cs.Model
        $hostInfo.memoryBytes = [int64]$cs.TotalPhysicalMemory
        $hostInfo.osName = [string]$os.Caption
        $hostInfo.osVersion = [string]$os.Version
        $hostInfo.osBuild = [string]$os.BuildNumber
        $hostInfo.sockets = $cpus.Count
        $cores = 0; $threads = 0
        foreach ($cpu in $cpus) { $cores += [int]$cpu.NumberOfCores; $threads += [int]$cpu.NumberOfLogicalProcessors }
        $hostInfo.cores = $cores
        $hostInfo.threads = $threads
        if ($cpus.Count -gt 0) { $hostInfo.cpuModel = ([string]$cpus[0].Name).Trim(); $hostInfo.cpuMhz = [int]$cpus[0].MaxClockSpeed }
    } catch {
        Add-Warning ('Host hardware details could not be read: ' + $_.Exception.Message)
    }
    try {
        $cluster = Get-Cim -Namespace 'root\MSCluster' -Class 'MSCluster_Cluster' | Select-Object -First 1
        if ($cluster) { $hostInfo.cluster = [string]$cluster.Name }
    } catch { }
    $volumes = @()
    try {
        foreach ($volume in (Get-Cim -Namespace 'root\cimv2' -Class 'Win32_Volume' -Filter 'DriveType=3')) {
            if (-not $volume.Name -or $volume.Name -like '\\?\*') { continue }
            $volumes += @{ name = ([string]$volume.Name).TrimEnd('\'); label = [string]$volume.Label; fileSystem = [string]$volume.FileSystem; capacityBytes = [int64]$volume.Capacity; freeBytes = [int64]$volume.FreeSpace }
        }
    } catch {
        Add-Warning ('The host''s volumes could not be read: ' + $_.Exception.Message)
    }
    $hostInfo.volumes = $volumes

    $switches = @()
    $switchNames = @{}
    foreach ($switch in (Get-OptionalCim -Class 'Msvm_VirtualEthernetSwitch')) {
        $switchNames[[string]$switch.Name] = [string]$switch.ElementName
        $switches += @{ id = [string]$switch.Name; name = [string]$switch.ElementName }
    }

    # Settings are read once per class and grouped by VM GUID.
    $settings = Get-Cim -Class 'Msvm_VirtualSystemSettingData'
    $processors = Get-Cim -Class 'Msvm_ProcessorSettingData'
    $memories = Get-Cim -Class 'Msvm_MemorySettingData'
    $resources = Get-Cim -Class 'Msvm_ResourceAllocationSettingData'
    $storage = Get-Cim -Class 'Msvm_StorageAllocationSettingData'
    $syntheticPorts = Get-OptionalCim -Class 'Msvm_SyntheticEthernetPortSettingData'
    $emulatedPorts = Get-OptionalCim -Class 'Msvm_EmulatedEthernetPortSettingData'
    $connections = Get-OptionalCim -Class 'Msvm_EthernetPortAllocationSettingData'
    $vlans = Get-OptionalCim -Class 'Msvm_EthernetSwitchPortVlanSettingData'
    $guestNetworks = Get-OptionalCim -Class 'Msvm_GuestNetworkAdapterConfiguration'
    $security = Get-OptionalCim -Class 'Msvm_SecuritySettingData'
    $kvps = Get-OptionalCim -Class 'Msvm_KvpExchangeComponent'
    $heartbeats = Get-OptionalCim -Class 'Msvm_HeartbeatComponent'

    $resourceById = @{}
    foreach ($item in $resources) { $resourceById[[string]$item.InstanceID] = $item }

    $vms = @()
    foreach ($system in $systems) {
        $guid = ([string]$system.Name).ToUpperInvariant()
        $vm = @{ id = $guid; name = [string]$system.ElementName; enabledState = [int]$system.EnabledState; checkpoints = @(); disks = @(); nics = @(); media = @() }
        foreach ($setting in $settings) {
            if ([string]$setting.VirtualSystemIdentifier -ine $guid) { continue }
            switch -Wildcard ([string]$setting.VirtualSystemType) {
                'Microsoft:Hyper-V:System:Realized' {
                    $vm.generation = 1
                    if ([string]$setting.VirtualSystemSubType -like '*SubType:2') { $vm.generation = 2 }
                    $vm.version = [string]$setting.Version
                    $vm.secureBoot = [bool]$setting.SecureBootEnabled
                    $vm.notes = (@($setting.Notes) -join ' ')
                    $vm.configurationRoot = [string]$setting.ConfigurationDataRoot
                }
                'Microsoft:Hyper-V:Snapshot:*' {
                    $created = ''
                    if ($setting.CreationTime) { $created = ([datetime]$setting.CreationTime).ToUniversalTime().ToString('o') }
                    $vm.checkpoints += @{ name = [string]$setting.ElementName; created = $created; type = [string]$setting.VirtualSystemType }
                }
            }
        }
        foreach ($processor in $processors) {
            if ((Get-VmGuid $processor.InstanceID) -eq $guid -and $processor.InstanceID -notlike '*Snapshot*') { $vm.cpus = [int]$processor.VirtualQuantity; break }
        }
        foreach ($memory in $memories) {
            if ((Get-VmGuid $memory.InstanceID) -eq $guid) { $vm.memoryMB = [int64]$memory.VirtualQuantity; $vm.dynamicMemory = [bool]$memory.DynamicMemoryEnabled; $vm.memoryLimitMB = [int64]$memory.Limit; break }
        }
        foreach ($item in $security) {
            if ((Get-VmGuid $item.InstanceID) -eq $guid) { $vm.tpm = [bool]$item.TpmEnabled; $vm.shielded = [bool]$item.ShieldingRequested; break }
        }

        # Only the VM's current settings, not those kept with checkpoints:
        # a checkpoint's InstanceID names the checkpoint, not the VM.
        $current = '^Microsoft:' + [regex]::Escape($guid) + '\\'
        foreach ($drive in $storage) {
            if ([string]$drive.InstanceID -notmatch $current) { continue }
            $type = [int]$drive.ResourceType
            $path = ''
            if ($drive.HostResource) { $path = [string](@($drive.HostResource)[0]) }
            $parentDrive = $resourceById[(Get-PathInstanceID ([string]$drive.Parent))]
            $controller = $null
            $location = ''
            if ($parentDrive) { $controller = $resourceById[(Get-PathInstanceID ([string]$parentDrive.Parent))]; $location = [string]$parentDrive.AddressOnParent }
            $controllerType = ''; $controllerAddress = ''
            if ($controller) {
                switch ([int]$controller.ResourceType) { 5 { $controllerType = 'IDE' } 6 { $controllerType = 'SCSI' } default { $controllerType = [string]$controller.ResourceSubType } }
                $controllerAddress = [string]$controller.Address
                if ($controllerType -eq 'SCSI') {
                    # SCSI controllers have no Address; their order among the VM's SCSI controllers is the number.
                    $scsi = @($resources | Where-Object { [string]$_.InstanceID -match $current -and [int]$_.ResourceType -eq 6 } | Sort-Object InstanceID)
                    for ($index = 0; $index -lt $scsi.Count; $index++) { if ($scsi[$index].InstanceID -eq $controller.InstanceID) { $controllerAddress = [string]$index } }
                }
            }
            if ($type -eq 31) {
                $disk = @{ path = $path; controller = $controllerType; controllerNumber = $controllerAddress; location = $location; shared = [bool]$drive.PersistentReservationsSupported }
                if ($path) {
                    $details = Get-VhdDetails -Path $path
                    foreach ($key in $details.Keys) { $disk[$key] = $details[$key] }
                }
                $vm.disks += $disk
            } elseif ($type -eq 16 -and $path) {
                $vm.media += $path
            }
        }
        # Pass-through disks are drive settings pointing at a physical disk.
        foreach ($drive in $resources) {
            if ([string]$drive.InstanceID -notmatch $current -or [int]$drive.ResourceType -ne 17 -or -not $drive.HostResource) { continue }
            $hostResource = [string](@($drive.HostResource)[0])
            if ($hostResource -match 'Msvm_DiskDrive') {
                $controller = $resourceById[(Get-PathInstanceID ([string]$drive.Parent))]
                $controllerType = ''
                if ($controller) { switch ([int]$controller.ResourceType) { 5 { $controllerType = 'IDE' } 6 { $controllerType = 'SCSI' } } }
                $vm.disks += @{ path = ''; passthrough = $true; controller = $controllerType; location = [string]$drive.AddressOnParent; hostDisk = (Get-PathName $hostResource) }
            }
        }

        $ports = @($syntheticPorts | Where-Object { [string]$_.InstanceID -match $current }) + @($emulatedPorts | Where-Object { [string]$_.InstanceID -match $current })
        foreach ($port in $ports) {
            $legacy = $port.CimClass.CimClassName -eq 'Msvm_EmulatedEthernetPortSettingData'
            $nic = @{ name = [string]$port.ElementName; mac = [string]$port.Address; staticMac = [bool]$port.StaticMacAddress; legacy = $legacy; switch = ''; connected = $false; vlan = 0; addresses = @(); dhcp = $true; guestKnown = $false }
            foreach ($connection in $connections) {
                if ((Get-PathInstanceID ([string]$connection.Parent)) -ne [string]$port.InstanceID) { continue }
                $nic.connected = [int]$connection.EnabledState -ne 3
                if ($connection.HostResource) {
                    $switchId = Get-PathName ([string](@($connection.HostResource)[0]))
                    $nic.switch = [string]$switchNames[$switchId]
                }
                foreach ($vlan in $vlans) {
                    if ([string]$vlan.InstanceID -like ([string]$connection.InstanceID + '*') -and [int]$vlan.AccessVlanId -gt 0) { $nic.vlan = [int]$vlan.AccessVlanId }
                }
            }
            $portId = ([string]$port.InstanceID).Split('\')[-1]
            foreach ($config in $guestNetworks) {
                if ([string]$config.InstanceID -like ('*' + $portId)) {
                    $nic.guestKnown = $true
                    $nic.dhcp = [bool]$config.DHCPEnabled
                    $nic.addresses = @($config.IPAddresses | Where-Object { $_ })
                }
            }
            $vm.nics += $nic
        }

        $kvp = $kvps | Where-Object { [string]$_.SystemName -ieq $guid } | Select-Object -First 1
        $items = Get-KvpItems $kvp
        $vm.guestOs = [string]$items['OSName']
        $vm.guestOsVersion = [string]$items['OSVersion']
        $vm.guestHostName = [string]$items['FullyQualifiedDomainName']
        $vm.integrationVersion = [string]$items['IntegrationServicesVersion']
        $heartbeat = $heartbeats | Where-Object { [string]$_.SystemName -ieq $guid } | Select-Object -First 1
        $vm.heartbeat = ''
        if ($heartbeat) {
            $status = @($heartbeat.OperationalStatus)
            if ($status.Count -gt 0) {
                switch ([int]$status[0]) { 2 { $vm.heartbeat = 'ok' } 3 { $vm.heartbeat = 'degraded' } 12 { $vm.heartbeat = 'noContact' } 13 { $vm.heartbeat = 'lostCommunication' } default { $vm.heartbeat = 'status ' + $status[0] } }
            }
            if ([int]$heartbeat.EnabledState -eq 3) { $vm.heartbeat = 'disabled' }
        }
        $vms += $vm
        Write-Log ('Read ' + $vm.name + '.')
    }
    return @{ host = $hostInfo; switches = $switches; vms = $vms; warnings = @($script:Warnings) }
}

try {
    $result = Read-Host-Inventory
    Write-Result $true '' $result
} catch {
    $message = $_.Exception.Message
    Write-Log ('Failed: ' + $message)
    Write-Result $false $message $null
} finally {
    if ($script:Session) { Remove-CimSession -CimSession $script:Session -ErrorAction SilentlyContinue }
}
