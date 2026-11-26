<#
Guest pre-flight and post-cutover fix-up for a Windows computer moving to a
new platform: VMware, Hyper-V or physical to Hyper-V, Scale Computing
HyperCore, Nutanix AHV, Proxmox VE, Azure, Amazon EC2 or a physical server. Embedded in Server Inventory and
Migration Manager and run by it; Windows PowerShell 3.0 compatible.

Modes
  Capture          Records this VM's network, disk, driver and platform state
                   before cutover (read-only).
  Status           Reads the current state and compares it with the capture
                   (read-only).
  ApplyNetwork     Puts captured static IP settings on chosen new adapters.
                   The adapter's current settings are saved first.
  UndoNetwork      Restores the settings saved by the last ApplyNetwork.
  RemoveGhostNics  Removes chosen network adapters that are no longer present
                   (the old platform's adapters).
  RemoveVmwareTools Uninstalls VMware Tools with its own uninstaller.
  OnlineDisks      Brings chosen offline or read-only disks online.
  SaveBitLocker    Before removing a virtual TPM: writes every BitLocker
                   volume's recovery password to a file in ExportPath (never
                   to the log) and suspends BitLocker until it is resumed.

Every changing mode acts only on items named in the plan file, re-checks
each one first, and stops at the first failure.

The application runs this script on this computer, or sends its text to
another Windows computer through Invoke-GuestRemote.ps1 (PowerShell remoting
or PowerShell Direct), so nothing has to be copied into or installed in the
machine being moved.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('Capture', 'Status', 'ApplyNetwork', 'UndoNetwork', 'RemoveGhostNics', 'RemoveVmwareTools', 'OnlineDisks', 'SaveBitLocker', 'Activate')]
    [string]$Mode,
    [Parameter(Mandatory = $true)][string]$WorkPath,
    [Parameter(Mandatory = $true)][string]$ResultPath,
    [string]$PlanPath = '',
    [string]$ExportPath = '',
    # Set by Invoke-GuestRemote.ps1, which runs this script's text inside
    # another computer through PowerShell remoting or PowerShell Direct: the
    # result file is still written, but the script does not call exit, which
    # would end the remote session's pipeline.
    [switch]$Embedded
)

$ErrorActionPreference = 'Stop'
$script:EngineVersion = 1
$script:CaptureName = 'Guest-Capture.json'
$script:RollbackName = 'Network-Rollback.json'

function Write-GuestLog {
    param([string]$Message, [string]$Level = 'INFO')
    $line = (Get-Date -Format 'yyyy-MM-dd HH:mm:ss') + ' [' + $Level + '] ' + $Message
    # Straight to standard output: Write-Output would become part of the
    # calling function's return value.
    [Console]::Out.WriteLine($line)
    [Console]::Out.Flush()
    try { Add-Content -LiteralPath (Join-Path $WorkPath 'Guest-Migration.log') -Value $line -Encoding UTF8 } catch { }
}

function Write-JsonFile {
    param([string]$Path, [object]$Value)
    $json = $Value | ConvertTo-Json -Depth 8
    $encoding = New-Object System.Text.UTF8Encoding($false)
    $temporary = $Path + '.tmp'
    [IO.File]::WriteAllText($temporary, $json, $encoding)
    if (Test-Path -LiteralPath $Path) { Remove-Item -LiteralPath $Path -Force }
    Move-Item -LiteralPath $temporary -Destination $Path
}

function Read-JsonFile {
    param([string]$Path)
    $text = [IO.File]::ReadAllText($Path)
    if ($text.Length -gt 0 -and $text[0] -eq [char]0xFEFF) { $text = $text.Substring(1) }
    return ($text | ConvertFrom-Json)
}

function Get-RegistryValue {
    param([string]$Path, [string]$Name)
    try { return (Get-ItemProperty -LiteralPath $Path -Name $Name -ErrorAction Stop).$Name } catch { return $null }
}

# SetupAPI access for network adapters that are no longer present. PowerShell
# 3.0 has no PnpDevice module, so this lists and removes devices directly.
$script:DeviceHelperLoaded = $false
function Initialize-DeviceHelper {
    if ($script:DeviceHelperLoaded) { return }
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
public static class SicmNetDevices {
    [StructLayout(LayoutKind.Sequential)]
    struct SP_DEVINFO_DATA { public int cbSize; public Guid ClassGuid; public int DevInst; public IntPtr Reserved; }
    const int DIGCF_PRESENT = 0x2;
    const int SPDRP_DEVICEDESC = 0x0;
    const int SPDRP_FRIENDLYNAME = 0xC;
    static readonly IntPtr INVALID_HANDLE_VALUE = new IntPtr(-1);
    static Guid NetClass = new Guid("4d36e972-e325-11ce-bfc1-08002be10318");
    [DllImport("setupapi.dll", SetLastError = true)]
    static extern IntPtr SetupDiGetClassDevs(ref Guid classGuid, IntPtr enumerator, IntPtr parent, int flags);
    [DllImport("setupapi.dll", SetLastError = true)]
    static extern bool SetupDiEnumDeviceInfo(IntPtr set, int index, ref SP_DEVINFO_DATA data);
    [DllImport("setupapi.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern bool SetupDiGetDeviceInstanceId(IntPtr set, ref SP_DEVINFO_DATA data, StringBuilder id, int size, out int required);
    [DllImport("setupapi.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern bool SetupDiGetDeviceRegistryProperty(IntPtr set, ref SP_DEVINFO_DATA data, int property, out int type, byte[] buffer, int size, out int required);
    [DllImport("setupapi.dll", SetLastError = true)]
    static extern bool SetupDiRemoveDevice(IntPtr set, ref SP_DEVINFO_DATA data);
    [DllImport("setupapi.dll", SetLastError = true)]
    static extern bool SetupDiDestroyDeviceInfoList(IntPtr set);

    static string Property(IntPtr set, ref SP_DEVINFO_DATA data, int property) {
        byte[] buffer = new byte[2048]; int type, required;
        if (!SetupDiGetDeviceRegistryProperty(set, ref data, property, out type, buffer, buffer.Length, out required)) { return ""; }
        return Encoding.Unicode.GetString(buffer, 0, Math.Max(0, required - 2)).TrimEnd('\0');
    }
    static List<string[]> Read(bool presentOnly) {
        List<string[]> result = new List<string[]>();
        IntPtr set = SetupDiGetClassDevs(ref NetClass, IntPtr.Zero, IntPtr.Zero, presentOnly ? DIGCF_PRESENT : 0);
        if (set == INVALID_HANDLE_VALUE) { throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error()); }
        try {
            SP_DEVINFO_DATA data = new SP_DEVINFO_DATA(); data.cbSize = Marshal.SizeOf(typeof(SP_DEVINFO_DATA));
            for (int index = 0; SetupDiEnumDeviceInfo(set, index, ref data); index++) {
                StringBuilder id = new StringBuilder(1024); int required;
                if (!SetupDiGetDeviceInstanceId(set, ref data, id, id.Capacity, out required)) { continue; }
                string name = Property(set, ref data, SPDRP_FRIENDLYNAME);
                if (name.Length == 0) { name = Property(set, ref data, SPDRP_DEVICEDESC); }
                result.Add(new string[] { id.ToString(), name });
            }
        } finally { SetupDiDestroyDeviceInfoList(set); }
        return result;
    }
    // Returns instance ID and description of network adapters that are installed but not present.
    public static List<string[]> NonPresent() {
        HashSet<string> present = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        foreach (string[] item in Read(true)) { present.Add(item[0]); }
        List<string[]> result = new List<string[]>();
        foreach (string[] item in Read(false)) { if (!present.Contains(item[0])) { result.Add(item); } }
        return result;
    }
    // Removes one non-present network adapter by instance ID.
    public static void Remove(string instanceId) {
        HashSet<string> present = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        foreach (string[] item in Read(true)) { present.Add(item[0]); }
        if (present.Contains(instanceId)) { throw new InvalidOperationException("The adapter is present and was not removed."); }
        IntPtr set = SetupDiGetClassDevs(ref NetClass, IntPtr.Zero, IntPtr.Zero, 0);
        if (set == INVALID_HANDLE_VALUE) { throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error()); }
        try {
            SP_DEVINFO_DATA data = new SP_DEVINFO_DATA(); data.cbSize = Marshal.SizeOf(typeof(SP_DEVINFO_DATA));
            for (int index = 0; SetupDiEnumDeviceInfo(set, index, ref data); index++) {
                StringBuilder id = new StringBuilder(1024); int required;
                if (!SetupDiGetDeviceInstanceId(set, ref data, id, id.Capacity, out required)) { continue; }
                if (!string.Equals(id.ToString(), instanceId, StringComparison.OrdinalIgnoreCase)) { continue; }
                if (!SetupDiRemoveDevice(set, ref data)) { throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error()); }
                return;
            }
        } finally { SetupDiDestroyDeviceInfoList(set); }
        throw new InvalidOperationException("The adapter was not found.");
    }
}
'@
    $script:DeviceHelperLoaded = $true
}

function Get-Platform {
    $system = Get-CimInstance -ClassName Win32_ComputerSystem
    $bios = Get-CimInstance -ClassName Win32_BIOS
    $assetTag = ''
    try { $assetTag = [string](@(Get-CimInstance -ClassName Win32_SystemEnclosure -ErrorAction Stop)[0].SMBIOSAssetTag) } catch { }
    $text = ([string]$system.Manufacturer + ' ' + [string]$system.Model + ' ' + [string]$bios.Manufacturer + ' ' + [string]$bios.Version)
    $virtual = $true
    $name = 'Other virtual machine'
    if ($text -match 'VMware') { $name = 'VMware' }
    elseif ($text -match 'Scale Computing|HyperCore') { $name = 'Scale Computing HyperCore' }
    elseif ($text -match 'Nutanix') { $name = 'Nutanix AHV' }
    elseif ($text -match 'Amazon EC2' -or ($text -match 'Xen' -and $text -match 'Amazon')) { $name = 'Amazon EC2' }
    elseif ($text -match 'Microsoft Corporation' -and [string]$system.Model -match 'Virtual') {
        # Azure marks its VMs with this chassis asset tag.
        if ($assetTag -eq '7783-7084-3265-9085-8269-3286-77') { $name = 'Microsoft Azure' } else { $name = 'Hyper-V' }
    }
    elseif ($text -match 'Proxmox') { $name = 'Proxmox VE' }
    elseif ($text -match 'QEMU|KVM|Red Hat|oVirt|Bochs') { $name = 'KVM (for example Proxmox VE)' }
    elseif ($text -match 'Xen') { $name = 'Xen' }
    elseif ($text -match 'VirtualBox|Parallels' -or [string]$system.Model -match 'Virtual') { $name = 'Other virtual machine' }
    else { $name = 'Physical computer'; $virtual = $false }
    return [pscustomobject]@{ Name = $name; Virtual = $virtual; Manufacturer = [string]$system.Manufacturer; Model = [string]$system.Model }
}

function Get-DriverStoreStatus {
    $repository = Join-Path $env:SystemRoot 'System32\DriverStore\FileRepository'
    $names = @()
    try { $names = @(Get-ChildItem -LiteralPath $repository -Directory -ErrorAction Stop | ForEach-Object { $_.Name.ToLowerInvariant() }) } catch { }
    $has = { param($prefix) [bool](@($names | Where-Object { $_.StartsWith($prefix) }).Count) }
    return [pscustomobject]@{
        VirtIOStorage = ((& $has 'viostor.inf_') -or (& $has 'vioscsi.inf_'))
        VirtIONetwork = (& $has 'netkvm.inf_')
        HyperVStorage = (& $has 'storvsc.inf_')
        HyperVNetwork = (& $has 'netvsc.inf_')
        AwsEna        = (& $has 'ena.inf_')
        AwsNvme       = ((& $has 'awsnvme.inf_') -or (& $has 'stornvme.inf_'))
        AzureAgent    = [bool](Get-Service -Name 'WindowsAzureGuestAgent' -ErrorAction SilentlyContinue)
        StorageControllers = @(Get-CimInstance -ClassName Win32_SCSIController -ErrorAction SilentlyContinue | ForEach-Object { [string]$_.Name } | Sort-Object -Unique)
    }
}

# What each migration target needs from this Windows installation before
# cutover, from the drivers in the driver store. One line per target.
function Get-TargetChecks {
    param([object]$State)
    $d = $State.Drivers
    $checks = New-Object System.Collections.ArrayList
    $add = { param($Target, $Ok, $Good, $Bad) [void]$checks.Add([pscustomobject]@{ Target = $Target; Severity = $(if ($Ok) { 'good' } else { 'warning' }); Message = $(if ($Ok) { $Good } else { $Bad }) }) }
    $hyperv = ($d.HyperVStorage -and $d.HyperVNetwork)
    $virtio = ($d.VirtIOStorage -and $d.VirtIONetwork)
    & $add 'hyperv' $hyperv 'Hyper-V storage and network drivers are built into this Windows version.' 'Hyper-V drivers were not found in the driver store. Older Windows versions need Hyper-V Integration Services installed before cutover.'
    & $add 'scale' $virtio 'VirtIO storage and network drivers are in the driver store, as HyperCore needs.' 'VirtIO drivers are not installed. Install the Scale Computing guest tools before cutover.'
    & $add 'nutanix' $virtio 'VirtIO storage and network drivers are in the driver store, as Nutanix AHV needs.' 'VirtIO drivers are not installed. Install the Nutanix VirtIO drivers before cutover, or let Nutanix Move install them.'
    & $add 'proxmox' $virtio 'VirtIO storage and network drivers are in the driver store, as Proxmox VE needs for VirtIO disks and adapters.' 'VirtIO drivers are not installed. Install the virtio-win drivers before cutover, or start the VM on SATA and e1000 and switch after installing them.'
    $azure = $hyperv
    $azureText = 'Hyper-V drivers are present, which Azure uses.'
    if ($hyperv -and -not $d.AzureAgent) { $azureText += ' The Azure VM Agent is not installed yet; Azure Migrate can install it, or install it after cutover.' }
    & $add 'azure' $azure $azureText 'Hyper-V drivers, which Azure uses, were not found. Install Hyper-V Integration Services before cutover.'
    & $add 'aws' ($d.AwsEna -and $d.AwsNvme) 'The ENA network and NVMe storage drivers are in the driver store, as EC2 Nitro instances need.' 'The AWS ENA network driver is not installed. AWS Application Migration Service adds it during conversion; install ENA and NVMe drivers first if you use another tool.'
    $controllers = @($d.StorageControllers)
    $physicalText = 'Before moving to a physical server, install its storage controller and network drivers from the server vendor (or inject them during the restore). Storage controllers here now: ' + $(if ($controllers.Count) { $controllers -join ', ' } else { 'none reported' }) + '.'
    [void]$checks.Add([pscustomobject]@{ Target = 'physical'; Severity = 'warning'; Message = $physicalText })
    return @($checks)
}

function Get-VmwareToolsEntry {
    $roots = @('HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall', 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall')
    foreach ($root in $roots) {
        foreach ($key in @(Get-ChildItem -LiteralPath $root -ErrorAction SilentlyContinue)) {
            $name = Get-RegistryValue $key.PSPath 'DisplayName'
            if ([string]$name -eq 'VMware Tools') {
                return [pscustomobject]@{ Installed = $true; Version = [string](Get-RegistryValue $key.PSPath 'DisplayVersion'); ProductCode = $key.PSChildName }
            }
        }
    }
    return [pscustomobject]@{ Installed = $false; Version = ''; ProductCode = '' }
}

function Get-AdapterConfiguration {
    param([object]$Adapter)
    $index = [int]$Adapter.ifIndex
    $ipv4Interface = Get-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4 -ErrorAction SilentlyContinue
    $addresses = @(Get-NetIPAddress -InterfaceIndex $index -ErrorAction SilentlyContinue | Where-Object {
            $_.IPAddress -notlike 'fe80*' -and $_.IPAddress -notlike '169.254.*' -and $_.AddressState -ne 'Invalid'
        } | ForEach-Object {
            [pscustomobject]@{ Address = [string]$_.IPAddress; PrefixLength = [int]$_.PrefixLength; Family = [string]$_.AddressFamily; Origin = [string]$_.PrefixOrigin }
        })
    $gateways = @()
    foreach ($store in @('PersistentStore', 'ActiveStore')) {
        $gateways = @(Get-NetRoute -InterfaceIndex $index -PolicyStore $store -ErrorAction SilentlyContinue | Where-Object {
                ($_.DestinationPrefix -eq '0.0.0.0/0' -or $_.DestinationPrefix -eq '::/0') -and $_.NextHop -ne '0.0.0.0' -and $_.NextHop -ne '::'
            } | ForEach-Object { [pscustomobject]@{ NextHop = [string]$_.NextHop; Metric = [int]$_.RouteMetric; Family = [string]$_.AddressFamily } })
        if ($gateways.Count -gt 0) { break }
    }
    $dns = @(Get-DnsClientServerAddress -InterfaceIndex $index -ErrorAction SilentlyContinue | ForEach-Object { $_.ServerAddresses } | Where-Object { $_ -and $_ -notlike 'fec0:0:0:ffff::*' })
    $client = Get-DnsClient -InterfaceIndex $index -ErrorAction SilentlyContinue
    $wmi = Get-CimInstance -ClassName Win32_NetworkAdapterConfiguration -Filter ('InterfaceIndex=' + $index) -ErrorAction SilentlyContinue
    $vlan = ''
    $jumbo = ''
    try { $vlan = [string](Get-NetAdapterAdvancedProperty -Name $Adapter.Name -RegistryKeyword 'VlanID' -ErrorAction Stop).RegistryValue } catch { }
    try { $jumbo = [string](Get-NetAdapterAdvancedProperty -Name $Adapter.Name -RegistryKeyword '*JumboPacket' -ErrorAction Stop).DisplayValue } catch { }
    $dhcp = $true
    if ($ipv4Interface) { $dhcp = ([string]$ipv4Interface.Dhcp -eq 'Enabled') }
    $manual = @($addresses | Where-Object { $_.Origin -eq 'Manual' })
    return [pscustomobject]@{
        Name                 = [string]$Adapter.Name
        Description          = [string]$Adapter.InterfaceDescription
        MacAddress           = ([string]$Adapter.MacAddress).ToUpperInvariant()
        InterfaceIndex       = $index
        Status               = [string]$Adapter.Status
        Dhcp                 = $dhcp
        Static               = ((-not $dhcp) -or $manual.Count -gt 0)
        Addresses            = $addresses
        Gateways             = $gateways
        DnsServers           = $dns
        DnsSuffix            = if ($client) { [string]$client.ConnectionSpecificSuffix } else { '' }
        RegisterInDns        = if ($client) { [bool]$client.RegisterThisConnectionsAddress } else { $true }
        UseSuffixInDns       = if ($client) { [bool]$client.UseSuffixWhenRegistering } else { $false }
        WinsPrimary          = if ($wmi) { [string]$wmi.WINSPrimaryServer } else { '' }
        WinsSecondary        = if ($wmi) { [string]$wmi.WINSSecondaryServer } else { '' }
        NetbiosOption        = if ($wmi -and $null -ne $wmi.TcpipNetbiosOptions) { [int]$wmi.TcpipNetbiosOptions } else { 0 }
        VlanId               = $vlan
        JumboPacket          = $jumbo
    }
}

function Get-PresentAdapters {
    # Hardware adapters, plus Microsoft loopback adapters, which can hold
    # static addresses (for example for direct server return load balancing).
    $adapters = @(Get-NetAdapter -Physical -ErrorAction SilentlyContinue)
    $indexes = @($adapters | ForEach-Object { [int]$_.ifIndex })
    $adapters += @(Get-NetAdapter -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceDescription -match 'KM-TEST Loopback|Microsoft Loopback' -and $indexes -notcontains [int]$_.ifIndex })
    @($adapters | Sort-Object ifIndex | ForEach-Object { Get-AdapterConfiguration $_ })
}

function Get-DiskState {
    @(Get-Disk -ErrorAction SilentlyContinue | Sort-Object Number | ForEach-Object {
            $disk = $_
            $letters = @(Get-Partition -DiskNumber $disk.Number -ErrorAction SilentlyContinue | Where-Object { $_.DriveLetter } | ForEach-Object { [string]$_.DriveLetter })
            [pscustomobject]@{
                Number         = [int]$disk.Number
                FriendlyName   = [string]$disk.FriendlyName
                SizeBytes      = [int64]$disk.Size
                PartitionStyle = [string]$disk.PartitionStyle
                IsOffline      = [bool]$disk.IsOffline
                IsReadOnly     = [bool]$disk.IsReadOnly
                OfflineReason  = [string]$disk.OfflineReason
                IsBoot         = [bool]$disk.IsBoot
                IsSystem       = [bool]$disk.IsSystem
                DriveLetters   = $letters
            }
        })
}

# Windows activation. The channel decides what to do after a move: KMS and
# Active Directory activation renew by themselves on the network, MAK and
# retail keys activate again (MAK uses a count), OEM licences belong to the
# old hardware, and on a Hyper-V host running Datacenter a guest can use
# Automatic Virtual Machine Activation (AVMA).
$script:WindowsAppId = '55c92734-d682-4d71-983e-d6ec3f16059f'
$script:LicenseStates = @{ 0 = 'Unlicensed'; 1 = 'Licensed'; 2 = 'Initial grace period'; 3 = 'Additional grace period'; 4 = 'Non-genuine grace period'; 5 = 'Notification'; 6 = 'Extended grace period' }
# Published by Microsoft (Automatic Virtual Machine Activation in Windows Server).
$script:AvmaKeys = @{
    '2025|Datacenter' = 'YQB4H-NKHHJ-Q6K4R-4VMY6-VCH67'
    '2025|Datacenter: Azure Edition' = '6NMQ9-T38WF-6MFGM-QYGYM-88J4F'
    '2025|Standard' = 'WWVGQ-PNHV9-B89P4-8GGM9-9HPQ4'
}

function Get-WindowsLicenseProduct {
    Get-CimInstance -ClassName SoftwareLicensingProduct -Filter ("ApplicationID='" + $script:WindowsAppId + "' AND PartialProductKey IS NOT NULL") -ErrorAction Stop | Select-Object -First 1
}

function Get-LicenseState {
    try { $product = Get-WindowsLicenseProduct } catch { return $null }
    if (-not $product) { return $null }
    $description = [string]$product.Description
    $keyChannel = ''
    try { $keyChannel = [string]$product.ProductKeyChannel } catch { }
    $text = ($description + ' ' + $keyChannel).ToUpperInvariant()
    $channel = 'Unknown'
    if ($text -match 'VIRTUAL_MACHINE_ACTIVATION|AVMA') { $channel = 'AVMA' }
    elseif ($text -match 'KMSCLIENT|GVLK') {
        $channel = 'KMS'
        try { if ([string]$product.ADActivationObjectName) { $channel = 'AD' } } catch { }
    }
    elseif ($text -match 'MAK') { $channel = 'MAK' }
    elseif ($text -match 'OEM') { $channel = 'OEM' }
    elseif ($text -match 'RETAIL') { $channel = 'Retail' }
    $kms = ''
    foreach ($name in @('KeyManagementServiceMachine', 'DiscoveredKeyManagementServiceMachineName')) {
        try { if (-not $kms -and [string]$product.$name) { $kms = [string]$product.$name } } catch { }
    }
    return [pscustomobject]@{
        Edition      = [string]$product.Name
        Description  = $description
        Channel      = $channel
        Status       = [string]$script:LicenseStates[[int]$product.LicenseStatus]
        Licensed     = ([int]$product.LicenseStatus -eq 1)
        PartialKey   = [string]$product.PartialProductKey
        KmsHost      = $kms
        GraceMinutes = [int]$product.GracePeriodRemaining
    }
}

function Get-AvmaKey {
    param([string]$Caption)
    $version = ''
    if ($Caption -match '(2012 R2|2016|2019|2022|2025)') { $version = $Matches[1] }
    $edition = ''
    if ($Caption -match 'Azure Edition') { $edition = 'Datacenter: Azure Edition' }
    elseif ($Caption -match 'Datacenter') { $edition = 'Datacenter' }
    elseif ($Caption -match 'Standard') { $edition = 'Standard' }
    elseif ($Caption -match 'Essentials') { $edition = 'Essentials' }
    return [pscustomobject]@{ Version = $version; Edition = $edition; Key = [string]$script:AvmaKeys[$version + '|' + $edition] }
}

# Get-ActivationAdvice says what activation needs on this machine now, from
# the channel recorded before the move when there is one.
function Get-ActivationAdvice {
    param([object]$State, [object]$Capture)
    $now = $State.License
    if (-not $now) { return [pscustomobject]@{ Known = $false; Licensed = $false; Severity = 'info'; Message = 'Windows activation could not be read on this computer.'; CanActivate = $false; AvmaKey = ''; AvmaEdition = '' } }
    $before = $null
    if ($Capture -and $Capture.State -and $Capture.State.License) { $before = $Capture.State.License }
    $channel = $now.Channel
    $server = ([string]$State.OperatingSystem -match 'Server')
    $avma = Get-AvmaKey ([string]$State.OperatingSystem)
    $onHyperV = ([string]$State.Platform.Name -eq 'Hyper-V')
    $message = ''
    $severity = 'good'
    if ($now.Licensed) {
        $message = 'Windows is activated (' + $channel + ' channel).'
    } else {
        $severity = 'warning'
        switch ($channel) {
            'KMS' { $message = 'Windows uses KMS activation' + $(if ($now.KmsHost) { ' (' + $now.KmsHost + ')' } else { '' }) + '. It activates by itself once it reaches the KMS server on the network; Activate now asks at once. A test VM with no network stays unactivated, which is expected.' }
            'AD' { $message = 'Windows uses Active Directory-based activation. It activates by itself once it reaches a domain controller; Activate now asks at once.' }
            'MAK' { $message = 'Windows uses a MAK key. Activate now activates it again over the internet, which uses one activation from the MAK''s count. Do not activate a test VM.' }
            'Retail' { $message = 'Windows uses a retail key. Activate now tries again online; Microsoft may ask for activation by phone after a hardware change.' }
            'OEM' { $message = 'Windows uses an OEM licence, which belongs to the original hardware and cannot move to a new host. Install a licence valid for this VM (a volume key, or AVMA on a Datacenter host).'; $severity = 'danger' }
            'AVMA' { $message = 'Windows uses AVMA. It activates through the Hyper-V host, which must run an activated Windows Server Datacenter with the Data Exchange integration service on for this VM.' }
            default { $message = 'Windows is not activated (' + $now.Status + ').' }
        }
    }
    if ($before -and $before.Channel -and $before.Channel -ne $channel) { $message += ' Before the move it used the ' + $before.Channel + ' channel.' }
    $offerAvma = (-not $now.Licensed) -and $server -and $onHyperV -and $channel -ne 'AVMA'
    if ($offerAvma) { $message += ' On a Hyper-V host running Windows Server Datacenter, this VM can use AVMA instead: no count is used and no internet is needed.' + $(if ($avma.Key) { '' } else { ' Paste the AVMA key for ' + [string]$State.OperatingSystem + ' from Microsoft''s Automatic Virtual Machine Activation page.' }) }
    return [pscustomobject]@{
        Known       = $true
        Channel     = $channel
        Before      = $(if ($before) { $before.Channel } else { '' })
        Licensed    = [bool]$now.Licensed
        Status      = $now.Status
        Severity    = $severity
        Message     = $message
        CanActivate = (-not $now.Licensed) -and $channel -ne 'OEM'
        AvmaKey     = $(if ($offerAvma) { $avma.Key } else { '' })
        AvmaEdition = $(if ($offerAvma) { ($avma.Version + ' ' + $avma.Edition).Trim() } else { '' })
    }
}

function Invoke-Activate {
    $key = ''
    if ($PlanPath -and (Test-Path -LiteralPath $PlanPath)) {
        $plan = Read-JsonFile $PlanPath
        foreach ($item in @($plan.items)) { if ($item.productKey) { $key = ([string]$item.productKey).ToUpperInvariant() } }
    }
    if ($key) {
        if ($key -notmatch '^[A-Z0-9]{5}(-[A-Z0-9]{5}){4}$') { throw 'The product key is not in the form XXXXX-XXXXX-XXXXX-XXXXX-XXXXX.' }
        $service = Get-CimInstance -ClassName SoftwareLicensingService
        Write-GuestLog ('Installing a product key ending in ' + $key.Substring(24) + '.')
        $null = Invoke-CimMethod -InputObject $service -MethodName InstallProductKey -Arguments @{ ProductKey = $key }
        $null = Invoke-CimMethod -InputObject $service -MethodName RefreshLicenseStatus
    }
    $product = Get-WindowsLicenseProduct
    if (-not $product) { throw 'No Windows licence was found on this computer.' }
    $state = Get-LicenseState
    if ($state.Channel -eq 'OEM' -and -not $key) { throw 'An OEM licence cannot be activated on new hardware. Enter a licence valid for this VM.' }
    if ($state.Channel -ne 'AVMA') {
        Write-GuestLog 'Asking Windows to activate.'
        try { $null = Invoke-CimMethod -InputObject $product -MethodName Activate }
        catch { throw ('Windows could not activate: ' + $_.Exception.Message + ' Check that this computer can reach ' + $(if ($state.KmsHost) { $state.KmsHost } elseif ($state.Channel -eq 'KMS') { 'its KMS server' } else { 'the internet' }) + '.') }
    } else {
        Write-GuestLog 'The AVMA key is installed; the Hyper-V host activates this VM within a few minutes.'
    }
    $after = Get-LicenseState
    Write-GuestLog ('Windows activation: ' + $after.Status + ' (' + $after.Channel + ').')
    return $after
}

function Get-GuestState {
    $os = Get-CimInstance -ClassName Win32_OperatingSystem
    $system = Get-CimInstance -ClassName Win32_ComputerSystem
    $firmwareType = Get-RegistryValue 'HKLM:\SYSTEM\CurrentControlSet\Control' 'PEFirmwareType'
    $secureBoot = Get-RegistryValue 'HKLM:\SYSTEM\CurrentControlSet\Control\SecureBoot\State' 'UEFISecureBootEnabled'
    $bitlocker = @()
    try {
        $bitlocker = @(Get-CimInstance -Namespace 'root\cimv2\Security\MicrosoftVolumeEncryption' -ClassName Win32_EncryptableVolume -ErrorAction Stop | Where-Object { $_.ProtectionStatus -ne 0 } | ForEach-Object { [string]$_.DriveLetter })
    } catch { }
    $tpm = $false
    try { $tpm = [bool](Get-CimInstance -Namespace 'root\cimv2\Security\MicrosoftTpm' -ClassName Win32_Tpm -ErrorAction Stop) } catch { }
    $sanPolicy = ''
    if (Get-Command Get-StorageSetting -ErrorAction SilentlyContinue) {
        try { $sanPolicy = [string](Get-StorageSetting -ErrorAction Stop).NewDiskPolicy } catch { }
    }
    $license = Get-LicenseState
    $activation = ''
    if ($license) { $activation = $license.Status + ' (' + $license.Description + ')' }
    $autoServices = @(Get-CimInstance -ClassName Win32_Service -Filter "StartMode='Auto' AND State='Running'" | ForEach-Object { [string]$_.Name } | Sort-Object)
    $routes = @(Get-NetRoute -PolicyStore PersistentStore -ErrorAction SilentlyContinue | Where-Object { $_.DestinationPrefix -ne '0.0.0.0/0' -and $_.DestinationPrefix -ne '::/0' } | ForEach-Object {
            [pscustomobject]@{ DestinationPrefix = [string]$_.DestinationPrefix; NextHop = [string]$_.NextHop; Metric = [int]$_.RouteMetric }
        })
    return [pscustomobject]@{
        ComputerName    = $env:COMPUTERNAME
        Domain          = [string]$system.Domain
        OperatingSystem = [string]$os.Caption
        Version         = [string]$os.Version
        Platform        = Get-Platform
        Firmware        = if ($firmwareType -eq 2) { 'UEFI' } elseif ($firmwareType -eq 1) { 'BIOS' } else { 'Unknown' }
        SecureBoot      = ($secureBoot -eq 1)
        Tpm             = $tpm
        BitLockerOn     = $bitlocker
        SanPolicy       = $sanPolicy
        Activation      = $activation
        License         = $license
        VmwareTools     = Get-VmwareToolsEntry
        Drivers         = Get-DriverStoreStatus
        Adapters        = Get-PresentAdapters
        PersistentRoutes = $routes
        Disks           = Get-DiskState
        RunningAutoServices = $autoServices
    }
}

function Get-GhostAdapters {
    Initialize-DeviceHelper
    @([SicmNetDevices]::NonPresent() | ForEach-Object {
            $id = [string]$_[0]
            $name = [string]$_[1]
            # Only real adapters: skip software (WAN Miniport, ROOT) and Microsoft filter devices.
            if ($id -match '^(PCI|VMBUS|USB)\\') {
                [pscustomobject]@{ InstanceId = $id; Description = $name; Vmware = ($name -match 'vmxnet|VMware|Intel\(R\) (PRO/1000|82574L)|E1000|Hyper-V Network Adapter|VirtIO|Nutanix|Elastic Network Adapter|AWS PV|Xen') }
            }
        })
}

function Get-PreflightFindings {
    param([object]$State)
    $findings = New-Object System.Collections.ArrayList
    $add = { param($Severity, $Message) [void]$findings.Add([pscustomobject]@{ Severity = $Severity; Message = $Message }) }
    $static = @($State.Adapters | Where-Object { $_.Static })
    if ($static.Count -gt 0) { & $add 'info' ("Recorded static IP settings for " + $static.Count + " adapter(s). After cutover, use Put back IP settings on the new adapters.") }
    if (@($State.BitLockerOn).Count -gt 0) { & $add 'warning' ('BitLocker protects ' + (@($State.BitLockerOn) -join ', ') + '. Save the recovery keys and suspend BitLocker before cutover.') }
    if ($State.SanPolicy -and $State.SanPolicy -notmatch 'OnlineAll') { & $add 'info' ('The SAN policy is ' + $State.SanPolicy + ', so data disks may start offline on the new hypervisor. The fix-up can bring them online.') }
    if ($State.Activation -and $State.Activation -notmatch '^Licensed') { & $add 'warning' ('Windows activation: ' + $State.Activation + '.') }
    if ($State.License) {
        $notes = @{
            'KMS' = 'Windows activates through KMS' + $(if ($State.License.KmsHost) { ' (' + $State.License.KmsHost + ')' } else { '' }) + '. After the move it activates again by itself once it reaches the KMS server.'
            'AD' = 'Windows uses Active Directory-based activation. After the move it activates again once it reaches a domain controller.'
            'MAK' = 'Windows uses a MAK key. After the move, activate it again (one activation from the MAK''s count); do not activate test VMs.'
            'Retail' = 'Windows uses a retail key. After the move, activate it again; Microsoft may ask for phone activation.'
            'OEM' = 'Windows uses an OEM licence tied to this hardware. It cannot move: plan a volume licence, or AVMA on a Hyper-V Datacenter host.'
            'AVMA' = 'Windows uses AVMA. The new Hyper-V host must run an activated Windows Server Datacenter.'
        }
        if ($notes[$State.License.Channel]) { & $add $(if ($State.License.Channel -eq 'OEM') { 'warning' } else { 'info' }) $notes[$State.License.Channel] }
    }
    return @($findings)
}

function Invoke-Capture {
    $state = Get-GuestState
    Write-GuestLog ('Recording this computer on ' + $state.Platform.Name + '.')
    $capture = [pscustomobject]@{ Schema = 1; EngineVersion = $script:EngineVersion; CapturedAt = (Get-Date).ToString('o'); State = $state; Findings = Get-PreflightFindings $state; TargetChecks = Get-TargetChecks $state }
    Write-JsonFile (Join-Path $WorkPath $script:CaptureName) $capture
    Write-JsonFile (Join-Path $WorkPath ('Guest-Capture-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.json')) $capture
    if ($ExportPath) {
        try {
            if (-not (Test-Path -LiteralPath $ExportPath)) { New-Item -ItemType Directory -Path $ExportPath -Force | Out-Null }
            Write-JsonFile (Join-Path $ExportPath ('Guest-Capture-' + $env:COMPUTERNAME + '-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.json')) $capture
        } catch { Write-GuestLog ('The copy for the output folder could not be written: ' + $_.Exception.Message) 'WARN' }
    }
    Write-GuestLog ('Captured ' + @($state.Adapters).Count + ' adapter(s) and ' + @($state.Disks).Count + ' disk(s).')
    return $capture
}

function Get-Capture {
    $path = Join-Path $WorkPath $script:CaptureName
    if (-not (Test-Path -LiteralPath $path)) { return $null }
    try { return Read-JsonFile $path } catch { Write-GuestLog ('The saved capture could not be read: ' + $_.Exception.Message) 'WARN'; return $null }
}

function Invoke-Status {
    $state = Get-GuestState
    $ghosts = @()
    try { $ghosts = @(Get-GhostAdapters) } catch { Write-GuestLog ('Hidden adapters could not be listed: ' + $_.Exception.Message) 'WARN' }
    $capture = Get-Capture
    $rollback = Test-Path -LiteralPath (Join-Path $WorkPath $script:RollbackName)
    $missingServices = @()
    $missingAddresses = @()
    if ($capture) {
        $running = @{}
        foreach ($name in @($state.RunningAutoServices)) { $running[$name] = $true }
        $missingServices = @(@($capture.State.RunningAutoServices) | Where-Object { -not $running[$_] -and $_ -notmatch '^(VMTools|VGAuthService|vm3dservice|VMwareCAFManagementAgentHost|vmvss|vmic\w+|WindowsAzureGuestAgent|RdAgent|QEMU-GA|NutanixGuestAgent|AmazonSSMAgent|EC2Launch|Ec2Config)$' })
        $present = @{}
        foreach ($adapter in @($state.Adapters)) { foreach ($address in @($adapter.Addresses)) { $present[[string]$address.Address] = $true } }
        foreach ($adapter in @($capture.State.Adapters | Where-Object { $_.Static })) {
            foreach ($address in @($adapter.Addresses | Where-Object { $_.Origin -eq 'Manual' })) {
                if (-not $present[[string]$address.Address]) { $missingAddresses += [string]$address.Address }
            }
        }
    }
    return [pscustomobject]@{ Schema = 1; State = $state; Ghosts = $ghosts; Capture = $capture; RollbackAvailable = $rollback; MissingAutoServices = $missingServices; MissingAddresses = $missingAddresses; TargetChecks = Get-TargetChecks $state; Activation = Get-ActivationAdvice $state $capture }
}

function Get-PlanItems {
    if (-not $PlanPath -or -not (Test-Path -LiteralPath $PlanPath)) { throw 'The plan file is missing.' }
    return @((Read-JsonFile $PlanPath).items)
}

function Set-AdapterConfiguration {
    param([int]$InterfaceIndex, [object]$Config, [bool]$RestoreDhcp)
    $adapter = Get-NetAdapter -InterfaceIndex $InterfaceIndex -ErrorAction Stop
    Write-GuestLog ('Configuring ' + $adapter.Name + ' (' + $adapter.InterfaceDescription + ').')
    # Clear this adapter's current manual addresses and default routes first.
    Get-NetRoute -InterfaceIndex $InterfaceIndex -ErrorAction SilentlyContinue | Where-Object { $_.DestinationPrefix -eq '0.0.0.0/0' -or $_.DestinationPrefix -eq '::/0' } | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue
    Get-NetIPAddress -InterfaceIndex $InterfaceIndex -ErrorAction SilentlyContinue | Where-Object { $_.PrefixOrigin -eq 'Manual' } | Remove-NetIPAddress -Confirm:$false -ErrorAction SilentlyContinue
    if ($RestoreDhcp) {
        Set-NetIPInterface -InterfaceIndex $InterfaceIndex -AddressFamily IPv4 -Dhcp Enabled
    }
    else {
        Set-NetIPInterface -InterfaceIndex $InterfaceIndex -AddressFamily IPv4 -Dhcp Disabled
    }
    foreach ($address in @($Config.Addresses | Where-Object { $_.Origin -eq 'Manual' })) {
        $family = if ([string]$address.Address -match ':') { 'IPv6' } else { 'IPv4' }
        New-NetIPAddress -InterfaceIndex $InterfaceIndex -AddressFamily $family -IPAddress ([string]$address.Address) -PrefixLength ([int]$address.PrefixLength) -ErrorAction Stop | Out-Null
        Write-GuestLog ('Added ' + $address.Address + '/' + $address.PrefixLength + '.')
    }
    if (-not $RestoreDhcp) {
        foreach ($gateway in @($Config.Gateways)) {
            $prefix = if ([string]$gateway.NextHop -match ':') { '::/0' } else { '0.0.0.0/0' }
            New-NetRoute -InterfaceIndex $InterfaceIndex -DestinationPrefix $prefix -NextHop ([string]$gateway.NextHop) -RouteMetric ([int]$gateway.Metric) -ErrorAction Stop | Out-Null
            Write-GuestLog ('Added default gateway ' + $gateway.NextHop + '.')
        }
    }
    $servers = @($Config.DnsServers | Where-Object { $_ })
    if ($servers.Count -gt 0 -and -not ($RestoreDhcp -and $Config.Dhcp)) {
        Set-DnsClientServerAddress -InterfaceIndex $InterfaceIndex -ServerAddresses $servers
        Write-GuestLog ('DNS servers: ' + ($servers -join ', ') + '.')
    }
    else {
        Set-DnsClientServerAddress -InterfaceIndex $InterfaceIndex -ResetServerAddresses
    }
    Set-DnsClient -InterfaceIndex $InterfaceIndex -ConnectionSpecificSuffix ([string]$Config.DnsSuffix) -RegisterThisConnectionsAddress ([bool]$Config.RegisterInDns) -UseSuffixWhenRegistering ([bool]$Config.UseSuffixInDns)
    $wmi = Get-CimInstance -ClassName Win32_NetworkAdapterConfiguration -Filter ('InterfaceIndex=' + $InterfaceIndex)
    if ($wmi) {
        if ($Config.WinsPrimary -or $Config.WinsSecondary) {
            [void](Invoke-CimMethod -InputObject $wmi -MethodName SetWINSServer -Arguments @{ WINSPrimaryServer = [string]$Config.WinsPrimary; WINSSecondaryServer = [string]$Config.WinsSecondary })
        }
        [void](Invoke-CimMethod -InputObject $wmi -MethodName SetTcpipNetbios -Arguments @{ TcpipNetbiosOptions = [uint32]$Config.NetbiosOption })
    }
}

function Invoke-ApplyNetwork {
    $capture = Get-Capture
    if (-not $capture) { throw 'No capture was found. Run the pre-flight on this VM before cutover, or load a capture file.' }
    $items = Get-PlanItems
    if ($items.Count -eq 0) { throw 'Choose at least one adapter.' }
    $present = @(Get-PresentAdapters)
    $usedIndexes = @{}
    $work = @()
    foreach ($item in $items) {
        $source = @($capture.State.Adapters | Where-Object { $_.MacAddress -eq [string]$item.capture })
        if ($source.Count -ne 1) { throw ('The captured adapter ' + $item.capture + ' is not in the capture.') }
        $target = @($present | Where-Object { $_.InterfaceIndex -eq [int]$item.interfaceIndex })
        if ($target.Count -ne 1) { throw ('The adapter chosen for ' + $source[0].Name + ' is no longer present. Refresh and choose again.') }
        if ($usedIndexes[[int]$item.interfaceIndex]) { throw 'Each new adapter can receive the settings of one old adapter only.' }
        $usedIndexes[[int]$item.interfaceIndex] = $true
        # Never take an address another present adapter is using.
        foreach ($address in @($source[0].Addresses | Where-Object { $_.Origin -eq 'Manual' })) {
            $owner = @($present | Where-Object { $_.InterfaceIndex -ne [int]$item.interfaceIndex -and @($_.Addresses | Where-Object { $_.Address -eq $address.Address }).Count -gt 0 })
            if ($owner.Count -gt 0) { throw ($address.Address + ' is already in use on ' + $owner[0].Name + '. Remove it there first.') }
        }
        $work += [pscustomobject]@{ Source = $source[0]; Target = $target[0]; Rename = [bool]$item.rename }
    }
    $rollback = [pscustomobject]@{ Schema = 1; SavedAt = (Get-Date).ToString('o'); Adapters = @($work | ForEach-Object { $_.Target }) }
    Write-JsonFile (Join-Path $WorkPath $script:RollbackName) $rollback
    Write-GuestLog ('Saved the current settings of ' + $work.Count + ' adapter(s) so this can be undone.')
    $results = @()
    foreach ($entry in $work) {
        Set-AdapterConfiguration -InterfaceIndex $entry.Target.InterfaceIndex -Config $entry.Source -RestoreDhcp $false
        $renamed = ''
        if ($entry.Rename -and $entry.Target.Name -ne $entry.Source.Name) {
            try {
                Rename-NetAdapter -Name $entry.Target.Name -NewName $entry.Source.Name -ErrorAction Stop
                $renamed = $entry.Source.Name
                Write-GuestLog ('Renamed ' + $entry.Target.Name + ' to ' + $entry.Source.Name + '.')
            } catch {
                Write-GuestLog ('The adapter could not be renamed to ' + $entry.Source.Name + ' (a hidden old adapter may still use the name; remove it and rename again): ' + $_.Exception.Message) 'WARN'
            }
        }
        $reachable = $null
        $gateway = @($entry.Source.Gateways | Where-Object { [string]$_.NextHop -notmatch ':' } | Select-Object -First 1)
        if ($gateway.Count -gt 0) {
            Start-Sleep -Seconds 3
            $reachable = [bool](Test-Connection -ComputerName ([string]$gateway[0].NextHop) -Count 2 -Quiet -ErrorAction SilentlyContinue)
            Write-GuestLog ('Gateway ' + $gateway[0].NextHop + ' answered: ' + $reachable + '.')
        }
        $results += [pscustomobject]@{ Adapter = $entry.Target.Name; From = $entry.Source.Name; RenamedTo = $renamed; GatewayAnswered = $reachable }
    }
    return [pscustomobject]@{ Applied = $results }
}

function Invoke-UndoNetwork {
    $path = Join-Path $WorkPath $script:RollbackName
    if (-not (Test-Path -LiteralPath $path)) { throw 'There are no saved settings to restore.' }
    $rollback = Read-JsonFile $path
    foreach ($adapter in @($rollback.Adapters)) {
        $current = Get-NetAdapter -InterfaceIndex ([int]$adapter.InterfaceIndex) -ErrorAction SilentlyContinue
        if (-not $current) { throw ('The adapter ' + $adapter.Name + ' is no longer present.') }
        Set-AdapterConfiguration -InterfaceIndex ([int]$adapter.InterfaceIndex) -Config $adapter -RestoreDhcp ([bool]$adapter.Dhcp)
    }
    Remove-Item -LiteralPath $path -Force
    Write-GuestLog 'Restored the saved adapter settings.'
    return [pscustomobject]@{ Restored = @($rollback.Adapters).Count }
}

function Invoke-RemoveGhostNics {
    $items = Get-PlanItems
    $ghosts = @(Get-GhostAdapters)
    $removed = 0
    foreach ($item in $items) {
        $id = [string]$item.instanceId
        if (@($ghosts | Where-Object { $_.InstanceId -eq $id }).Count -ne 1) { throw ('The hidden adapter ' + $id + ' is no longer listed. Refresh and choose again.') }
        [SicmNetDevices]::Remove($id)
        $removed++
        Write-GuestLog ('Removed hidden adapter ' + $id + '.')
    }
    return [pscustomobject]@{ Removed = $removed }
}

function Invoke-RemoveVmwareTools {
    $platform = Get-Platform
    if ($platform.Name -eq 'VMware') { throw 'This VM is still running on VMware. Remove VMware Tools only after cutover, on the migrated VM.' }
    $entry = Get-VmwareToolsEntry
    if (-not $entry.Installed) { Write-GuestLog 'VMware Tools is not installed.'; return [pscustomobject]@{ ExitCode = 0; RestartRequired = $false; AlreadyRemoved = $true } }
    if ($entry.ProductCode -notmatch '^\{[0-9A-Fa-f\-]{36}\}$') { throw 'The VMware Tools uninstall entry is not a Windows Installer product. Remove it from Programs and Features.' }
    $log = Join-Path $WorkPath 'VMware-Tools-Uninstall.log'
    Write-GuestLog ('Uninstalling VMware Tools ' + $entry.Version + '. The log is ' + $log + '.')
    $process = Start-Process -FilePath (Join-Path $env:SystemRoot 'System32\msiexec.exe') -ArgumentList @('/x', $entry.ProductCode, '/qn', '/norestart', 'REBOOT=ReallySuppress', '/l*v', ('"' + $log + '"')) -Wait -PassThru -WindowStyle Hidden
    $code = [int]$process.ExitCode
    if ($code -ne 0 -and $code -ne 3010 -and $code -ne 1641) {
        throw ('The VMware Tools uninstaller ended with code ' + $code + '. Some versions refuse to uninstall outside VMware; see ' + $log + ' and VMware''s guidance for removing Tools manually.')
    }
    Write-GuestLog ('VMware Tools was removed (exit code ' + $code + ').')
    return [pscustomobject]@{ ExitCode = $code; RestartRequired = ($code -ne 0); AlreadyRemoved = $false }
}

function Invoke-OnlineDisks {
    $items = Get-PlanItems
    $done = 0
    foreach ($item in $items) {
        $number = [int]$item.number
        $disk = Get-Disk -Number $number -ErrorAction Stop
        if ($disk.IsBoot -or $disk.IsSystem) { continue }
        if ($disk.IsOffline) { Set-Disk -Number $number -IsOffline $false; Write-GuestLog ('Disk ' + $number + ' is online.') }
        if ((Get-Disk -Number $number).IsReadOnly) { Set-Disk -Number $number -IsReadOnly $false; Write-GuestLog ('Disk ' + $number + ' is writable.') }
        $done++
    }
    return [pscustomobject]@{ Disks = $done }
}

function Invoke-SaveBitLocker {
    if (-not (Get-Command -Name Get-BitLockerVolume -ErrorAction SilentlyContinue)) {
        Write-GuestLog 'BitLocker is not installed on this computer, so there are no keys to save.'
        return [pscustomobject]@{ NoBitLocker = $true; Volumes = @(); Suspended = @(); KeyFile = '' }
    }
    $volumes = @(Get-BitLockerVolume | Where-Object { [string]$_.VolumeStatus -ne 'FullyDecrypted' })
    if ($volumes.Count -eq 0) {
        Write-GuestLog 'No volume is encrypted with BitLocker.'
        return [pscustomobject]@{ NoBitLocker = $true; Volumes = @(); Suspended = @(); KeyFile = '' }
    }
    if (-not $ExportPath) { throw 'No folder was given for the recovery keys.' }
    if (-not (Test-Path -LiteralPath $ExportPath)) { New-Item -ItemType Directory -Path $ExportPath -Force | Out-Null }
    $lines = New-Object System.Collections.ArrayList
    [void]$lines.Add('BitLocker recovery keys for ' + $env:COMPUTERNAME + ', saved ' + (Get-Date).ToString('yyyy-MM-dd HH:mm') + '.')
    [void]$lines.Add('Keep this file somewhere safe and delete it once the migration is finished. Anyone with these keys can unlock the volumes.')
    [void]$lines.Add('')
    $names = @()
    foreach ($volume in $volumes) {
        $mount = [string]$volume.MountPoint
        $recovery = @($volume.KeyProtector | Where-Object { [string]$_.KeyProtectorType -eq 'RecoveryPassword' })
        if ($recovery.Count -eq 0) {
            # Without a recovery password there would be nothing to type at the recovery screen.
            Write-GuestLog ('Adding a recovery password protector to ' + $mount + '.')
            Add-BitLockerKeyProtector -MountPoint $mount -RecoveryPasswordProtector -WarningAction SilentlyContinue | Out-Null
            $recovery = @((Get-BitLockerVolume -MountPoint $mount).KeyProtector | Where-Object { [string]$_.KeyProtectorType -eq 'RecoveryPassword' })
        }
        foreach ($protector in $recovery) {
            [void]$lines.Add('Volume ' + $mount + '  Key ID ' + [string]$protector.KeyProtectorId + '  Recovery password ' + [string]$protector.RecoveryPassword)
        }
        $names += $mount
    }
    $keyFile = Join-Path $ExportPath ('BitLocker-Recovery-Keys-' + $env:COMPUTERNAME + '.txt')
    [IO.File]::WriteAllLines($keyFile, [string[]]$lines.ToArray(), (New-Object System.Text.UTF8Encoding($false)))
    Write-GuestLog ('Saved the recovery passwords for ' + ($names -join ', ') + ' to ' + $keyFile + '.')
    $suspended = @()
    foreach ($volume in $volumes) {
        if ([string]$volume.ProtectionStatus -eq 'On') {
            Suspend-BitLocker -MountPoint ([string]$volume.MountPoint) -RebootCount 0 | Out-Null
            $suspended += [string]$volume.MountPoint
            Write-GuestLog ('Suspended BitLocker on ' + [string]$volume.MountPoint + ' until it is resumed.')
        }
    }
    return [pscustomobject]@{ NoBitLocker = $false; Volumes = $names; Suspended = $suspended; KeyFile = $keyFile }
}

$exitCode = 0
$result = $null
try {
    if (-not (Test-Path -LiteralPath $WorkPath)) { New-Item -ItemType Directory -Path $WorkPath -Force | Out-Null }
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Administrator rights are required.' }
    switch ($Mode) {
        'Capture' { $result = Invoke-Capture }
        'Status' { $result = Invoke-Status }
        'ApplyNetwork' { $result = Invoke-ApplyNetwork }
        'UndoNetwork' { $result = Invoke-UndoNetwork }
        'RemoveGhostNics' { $result = Invoke-RemoveGhostNics }
        'RemoveVmwareTools' { $result = Invoke-RemoveVmwareTools }
        'OnlineDisks' { $result = Invoke-OnlineDisks }
        'SaveBitLocker' { $result = Invoke-SaveBitLocker }
        'Activate' { $result = Invoke-Activate }
    }
    Write-JsonFile $ResultPath ([pscustomobject]@{ ok = $true; mode = $Mode; result = $result })
}
catch {
    $exitCode = 1
    Write-GuestLog ($Mode + ' failed: ' + $_.Exception.Message) 'ERROR'
    try { Write-JsonFile $ResultPath ([pscustomobject]@{ ok = $false; mode = $Mode; error = $_.Exception.Message }) } catch { }
}
if (-not $Embedded) { exit $exitCode }
