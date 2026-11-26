# Real round trip for src/app/Migrate-VmGuest.ps1 on a disposable Windows
# runner. A Microsoft loopback adapter stands in for the VM's network adapter:
# it gets static settings, the VM's settings are recorded, the adapter is
# "migrated" (settings cleared, renamed), the recorded settings are put back
# and undone. Negative controls check that changing modes refuse items that
# are not in the capture or not present. It adds and removes a network
# adapter, so it refuses to run outside CI unless -AllowSystemChanges is given.
[CmdletBinding()]
param(
    [string]$OutputDirectory = (Join-Path $PSScriptRoot '..\build\vmguest-validation'),
    [switch]$AllowSystemChanges
)
$ErrorActionPreference = 'Stop'
if (-not $env:GITHUB_ACTIONS -and -not $AllowSystemChanges) { throw 'Test-VmGuest.ps1 adds a network adapter and changes its settings. Run it only on a disposable machine with -AllowSystemChanges.' }
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Test-VmGuest.ps1 must run elevated.' }

$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
if (Test-Path -LiteralPath $OutputDirectory) { Remove-Item -LiteralPath $OutputDirectory -Recurse -Force }
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$engine = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\src\app\Migrate-VmGuest.ps1'))
$powerShell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
$work = Join-Path $OutputDirectory 'work'
$export = Join-Path $OutputDirectory 'export'
$checks = New-Object Collections.ArrayList
function Assert([bool]$Condition, [string]$Description) { if (-not $Condition) { throw ('FAIL ' + $Description) }; [void]$checks.Add($Description); Write-Host ('PASS ' + $Description) }
$script:run = 0
$fakeClass = 'HKLM:\SYSTEM\CurrentControlSet\Control\Class\{5349434d-0000-4000-8000-000000000001}'
function Invoke-Engine([string]$Mode, [object]$Plan, [string]$Name) {
    $script:run++
    $log = Join-Path $OutputDirectory ('{0:00}-{1}.log' -f $script:run, $Name)
    $result = Join-Path $OutputDirectory ('{0:00}-{1}.json' -f $script:run, $Name)
    $list = @('-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', $engine, '-Mode', $Mode, '-ResultPath', $result, '-WorkPath', $work)
    if ($Mode -eq 'Capture') { $list += @('-ExportPath', $export) }
    # Recovery keys never go to the uploaded output folder.
    if ($Mode -eq 'SaveBitLocker') { $list += @('-ExportPath', (Join-Path $env:TEMP 'SICM-BitLocker-Test')) }
    if ($null -ne $Plan) {
        $planPath = Join-Path $OutputDirectory ('{0:00}-{1}-plan.json' -f $script:run, $Name)
        [IO.File]::WriteAllText($planPath, (ConvertTo-Json -InputObject $Plan -Depth 5), (New-Object Text.UTF8Encoding($false)))
        $list += @('-PlanPath', $planPath)
    }
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { & $powerShell @list *> $log; $code = $LASTEXITCODE } finally { $ErrorActionPreference = $previous }
    Get-Content -LiteralPath $log | ForEach-Object { Write-Host ('  ' + $_) }
    $json = $null
    if (Test-Path -LiteralPath $result) { $json = Get-Content -LiteralPath $result -Raw | ConvertFrom-Json }
    return @{ code = $code; log = (Get-Content -LiteralPath $log -Raw); json = $json }
}

# Creates a Microsoft KM-TEST loopback adapter the way devcon install does.
Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;
public static class SicmLoopback {
    [StructLayout(LayoutKind.Sequential)]
    struct SP_DEVINFO_DATA { public int cbSize; public Guid ClassGuid; public int DevInst; public IntPtr Reserved; }
    [DllImport("setupapi.dll", SetLastError = true)]
    static extern IntPtr SetupDiCreateDeviceInfoList(ref Guid classGuid, IntPtr parent);
    [DllImport("setupapi.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern bool SetupDiCreateDeviceInfoW(IntPtr set, string name, ref Guid classGuid, string description, IntPtr parent, int flags, ref SP_DEVINFO_DATA data);
    [DllImport("setupapi.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern bool SetupDiSetDeviceRegistryPropertyW(IntPtr set, ref SP_DEVINFO_DATA data, int property, byte[] buffer, int size);
    [DllImport("setupapi.dll", SetLastError = true)]
    static extern bool SetupDiCallClassInstaller(int function, IntPtr set, ref SP_DEVINFO_DATA data);
    [DllImport("setupapi.dll", SetLastError = true)]
    static extern bool SetupDiDestroyDeviceInfoList(IntPtr set);
    [DllImport("newdev.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern bool UpdateDriverForPlugAndPlayDevicesW(IntPtr parent, string hardwareId, string infPath, int flags, out bool reboot);
    public static void Create() {
        Guid net = new Guid("4d36e972-e325-11ce-bfc1-08002be10318");
        IntPtr set = SetupDiCreateDeviceInfoList(ref net, IntPtr.Zero);
        if (set == new IntPtr(-1)) { throw new Win32Exception(Marshal.GetLastWin32Error()); }
        try {
            SP_DEVINFO_DATA data = new SP_DEVINFO_DATA(); data.cbSize = Marshal.SizeOf(typeof(SP_DEVINFO_DATA));
            if (!SetupDiCreateDeviceInfoW(set, "Net", ref net, null, IntPtr.Zero, 1, ref data)) { throw new Win32Exception(Marshal.GetLastWin32Error()); }
            byte[] id = Encoding.Unicode.GetBytes("*MSLOOP\0\0");
            if (!SetupDiSetDeviceRegistryPropertyW(set, ref data, 1, id, id.Length)) { throw new Win32Exception(Marshal.GetLastWin32Error()); }
            if (!SetupDiCallClassInstaller(0x19, set, ref data)) { throw new Win32Exception(Marshal.GetLastWin32Error()); }
        } finally { SetupDiDestroyDeviceInfoList(set); }
        bool reboot;
        string inf = Path.Combine(Environment.GetEnvironmentVariable("SystemRoot"), @"INF\netloop.inf");
        if (!UpdateDriverForPlugAndPlayDevicesW(IntPtr.Zero, "*MSLOOP", inf, 1, out reboot)) { throw new Win32Exception(Marshal.GetLastWin32Error()); }
    }
}
'@

$tokens = $null; $errors = $null
[Management.Automation.Language.Parser]::ParseFile($engine, [ref]$tokens, [ref]$errors) | Out-Null
Assert ($errors.Count -eq 0) 'Guest engine parses without errors'

$before = @(Get-NetAdapter | ForEach-Object { $_.ifIndex })
[SicmLoopback]::Create()
$loop = $null
for ($i = 0; $i -lt 30 -and -not $loop; $i++) {
    $loop = Get-NetAdapter | Where-Object { $before -notcontains $_.ifIndex -and $_.InterfaceDescription -match 'Loopback' } | Select-Object -First 1
    if (-not $loop) { Start-Sleep -Seconds 1 }
}
Assert ($null -ne $loop) 'A loopback adapter stands in for the VM network adapter'
$index = [int]$loop.ifIndex
$mac = ([string]$loop.MacAddress).ToUpperInvariant()
try {
    # The "VMware" adapter: static address, gateway on the same subnet, DNS, suffix and a recognisable name.
    Rename-NetAdapter -Name $loop.Name -NewName 'SICM Old NIC'
    Set-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4 -Dhcp Disabled
    New-NetIPAddress -InterfaceIndex $index -IPAddress '198.51.100.77' -PrefixLength 24 -DefaultGateway '198.51.100.1' | Out-Null
    Set-DnsClientServerAddress -InterfaceIndex $index -ServerAddresses @('198.51.100.53', '198.51.100.54')
    Set-DnsClient -InterfaceIndex $index -ConnectionSpecificSuffix 'corp.example.com'

    $capture = Invoke-Engine 'Capture' $null 'capture'
    Assert ($capture.code -eq 0 -and $capture.json.ok) 'Capture succeeds'
    $recorded = @($capture.json.result.State.Adapters | Where-Object { $_.MacAddress -eq $mac })
    Assert ($recorded.Count -eq 1) 'Capture records the adapter by MAC address'
    Assert ([bool]$recorded[0].Static -and @($recorded[0].Addresses | Where-Object { $_.Address -eq '198.51.100.77' -and $_.Origin -eq 'Manual' }).Count -eq 1) 'Capture records the static address as manual'
    Assert (@($recorded[0].Gateways | Where-Object { $_.NextHop -eq '198.51.100.1' }).Count -eq 1) 'Capture records the default gateway'
    Assert ((@($recorded[0].DnsServers) -join ',') -eq '198.51.100.53,198.51.100.54' -and $recorded[0].DnsSuffix -eq 'corp.example.com') 'Capture records DNS servers and suffix in order'
    Assert ([string]$capture.json.result.State.Platform.Name -ne '' -and @($capture.json.result.Findings).Count -gt 0) 'Capture reports the platform and pre-flight findings'
    Assert (@($capture.json.result.State.Disks | Where-Object { $_.IsBoot }).Count -ge 1) 'Capture records the boot disk'
    Assert (($capture.json.result -isnot [Array]) -and $capture.json.result.Schema -eq 1 -and $capture.log -match 'Captured \d+ adapter') 'Log lines go to the log, not into the result'
    Assert ((Test-Path -LiteralPath (Join-Path $work 'Guest-Capture.json')) -and @(Get-ChildItem -LiteralPath $export -Filter 'Guest-Capture-*.json').Count -eq 1) 'Capture is kept in the work folder with a copy in the output folder'

    # "Cutover": the new adapter comes up with DHCP and another name.
    Get-NetRoute -InterfaceIndex $index -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false
    Remove-NetIPAddress -InterfaceIndex $index -IPAddress '198.51.100.77' -Confirm:$false
    Set-DnsClientServerAddress -InterfaceIndex $index -ResetServerAddresses
    Set-DnsClient -InterfaceIndex $index -ConnectionSpecificSuffix ''
    Set-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4 -Dhcp Enabled
    Rename-NetAdapter -Name 'SICM Old NIC' -NewName 'SICM New NIC'

    $status = Invoke-Engine 'Status' $null 'status-after-cutover'
    Assert ($status.code -eq 0 -and $status.json.ok) 'Status succeeds'
    Assert (@($status.json.result.MissingAddresses) -contains '198.51.100.77') 'Status lists the recorded address as missing'
    Assert (-not $status.json.result.RollbackAvailable -and $null -ne $status.json.result.Capture) 'Status includes the capture and no rollback yet'
    Assert (@($status.json.result.State.Adapters | Where-Object { $_.InterfaceIndex -eq $index }).Count -eq 1) 'Status lists the loopback adapter as a target'
    $advice = $status.json.result.Activation
    Assert ($null -ne $advice -and [string]$advice.Message) 'Status says what Windows activation needs'
    if ($advice.Known) { Assert (@('KMS', 'AD', 'MAK', 'Retail', 'OEM', 'AVMA', 'Unknown') -contains [string]$advice.Channel) ('Status names the activation channel (' + [string]$advice.Channel + ')') }

    $bad = Invoke-Engine 'ApplyNetwork' @{ items = @(@{ capture = '00-00-5E-00-53-FF'; interfaceIndex = $index; rename = $false }) } 'apply-unknown-capture'
    Assert ($bad.code -ne 0 -and -not $bad.json.ok -and $bad.json.error -match 'not in the capture') 'Apply refuses an adapter that is not in the capture'
    $bad = Invoke-Engine 'ApplyNetwork' @{ items = @(@{ capture = $mac; interfaceIndex = 999999; rename = $false }) } 'apply-missing-target'
    Assert ($bad.code -ne 0 -and $bad.json.error -match 'no longer present') 'Apply refuses an adapter that is not present'
    Assert (-not (Test-Path -LiteralPath (Join-Path $work 'Network-Rollback.json'))) 'A refused apply saves no rollback'
    Assert (@(Get-NetIPAddress -InterfaceIndex $index -ErrorAction SilentlyContinue | Where-Object { $_.IPAddress -eq '198.51.100.77' }).Count -eq 0) 'A refused apply changes nothing'

    $apply = Invoke-Engine 'ApplyNetwork' @{ items = @(@{ capture = $mac; interfaceIndex = $index; rename = $true }) } 'apply'
    Assert ($apply.code -eq 0 -and $apply.json.ok) 'Apply succeeds'
    Assert (@(Get-NetIPAddress -InterfaceIndex $index -AddressFamily IPv4 | Where-Object { $_.IPAddress -eq '198.51.100.77' -and $_.PrefixLength -eq 24 }).Count -eq 1) 'Apply puts back the static address'
    Assert (@(Get-NetRoute -InterfaceIndex $index -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Where-Object { $_.NextHop -eq '198.51.100.1' }).Count -ge 1) 'Apply puts back the default gateway'
    Assert (((Get-DnsClientServerAddress -InterfaceIndex $index -AddressFamily IPv4).ServerAddresses -join ',') -eq '198.51.100.53,198.51.100.54') 'Apply puts back the DNS servers in order'
    Assert ((Get-DnsClient -InterfaceIndex $index).ConnectionSpecificSuffix -eq 'corp.example.com') 'Apply puts back the DNS suffix'
    Assert ((Get-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4).Dhcp -eq 'Disabled') 'Apply turns DHCP off'
    Assert ((Get-NetAdapter -InterfaceIndex $index).Name -eq 'SICM Old NIC') 'Apply gives the adapter its old name'
    Assert (Test-Path -LiteralPath (Join-Path $work 'Network-Rollback.json')) 'Apply saves the previous settings first'
    $status = Invoke-Engine 'Status' $null 'status-after-apply'
    Assert (@($status.json.result.MissingAddresses).Count -eq 0 -and $status.json.result.RollbackAvailable) 'Status shows nothing missing and Undo available'

    $undo = Invoke-Engine 'UndoNetwork' $null 'undo'
    Assert ($undo.code -eq 0 -and $undo.json.ok) 'Undo succeeds'
    Assert (@(Get-NetIPAddress -InterfaceIndex $index -ErrorAction SilentlyContinue | Where-Object { $_.IPAddress -eq '198.51.100.77' }).Count -eq 0) 'Undo removes the applied address'
    Assert ((Get-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4).Dhcp -eq 'Enabled') 'Undo turns DHCP back on'
    Assert (-not (Test-Path -LiteralPath (Join-Path $work 'Network-Rollback.json'))) 'Undo clears the saved settings'
    $again = Invoke-Engine 'UndoNetwork' $null 'undo-again'
    Assert ($again.code -ne 0 -and $again.json.error -match 'no saved settings') 'A second undo is refused'

    # The automatic fix-up after a cutover: the host's adapter map names the
    # new adapter by MAC; the recorded settings go back with no choices.
    $auto = Invoke-Engine 'AutoFixup' @{ auto = @{ network = $true; ghosts = $false; disks = $true; tools = $true; activate = $false; restart = $false }; adapters = @(@{ capture = $mac; hostAdapter = 'Network Adapter 1'; mac = ($mac -replace '-', '') }) } 'auto-fixup'
    if ([string]$capture.json.result.State.Platform.Name -eq 'VMware') {
        Assert ($auto.code -ne 0 -and $auto.json.error -match 'still runs on VMware') 'The automatic fix-up refuses to run on VMware'
    } else {
        Assert ($auto.code -eq 0 -and $auto.json.ok -and $auto.json.result.NetworkOk) 'The automatic fix-up succeeds'
        $ipStep = @($auto.json.result.Steps | Where-Object { $_.Step -eq 'IP settings' })
        Assert ($ipStep.Count -eq 1 -and $ipStep[0].Status -eq 'done') 'The automatic fix-up reports the IP settings step as done'
        Assert (@(Get-NetIPAddress -InterfaceIndex $index -AddressFamily IPv4 | Where-Object { $_.IPAddress -eq '198.51.100.77' -and $_.PrefixLength -eq 24 }).Count -eq 1) 'The automatic fix-up puts back the static address on the adapter the host named'
        Assert (((Get-DnsClientServerAddress -InterfaceIndex $index -AddressFamily IPv4).ServerAddresses -join ',') -eq '198.51.100.53,198.51.100.54') 'The automatic fix-up puts back the DNS servers'
        Assert (@($auto.json.result.Steps | Where-Object { $_.Step -eq 'VMware Tools' -and $_.Status -eq 'skipped' }).Count -eq 1) 'The automatic fix-up skips VMware Tools when it is not installed'
        Assert (Test-Path -LiteralPath (Join-Path $work 'Network-Rollback.json')) 'The automatic fix-up saves the previous settings first'
        $undo = Invoke-Engine 'UndoNetwork' $null 'undo-auto'
        Assert ($undo.code -eq 0 -and (Get-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4).Dhcp -eq 'Enabled') 'Undo reverses the automatic fix-up'
    }

    $ghost = Invoke-Engine 'RemoveGhostNics' @{ items = @(@{ instanceId = 'PCI\VEN_15AD&DEV_07B0&SUBSYS_07B015AD\SICMTEST' }) } 'ghost-unknown'
    Assert ($ghost.code -ne 0 -and $ghost.json.error -match 'no longer listed') 'Removing a hidden adapter that is not listed is refused'
    $boot = @(Get-Disk | Where-Object { $_.IsBoot } | Select-Object -First 1)
    $disks = Invoke-Engine 'OnlineDisks' @{ items = @(@{ number = [int]$boot[0].Number }) } 'online-boot-disk'
    Assert ($disks.code -eq 0 -and [int]$disks.json.result.Disks -eq 0) 'The boot disk is never changed'
    $tools = Invoke-Engine 'RemoveVmwareTools' $null 'remove-tools'
    if ([string]$capture.json.result.State.Platform.Name -eq 'VMware') {
        Assert ($tools.code -ne 0 -and $tools.json.error -match 'still running on VMware') 'VMware Tools removal is refused on VMware'
    } else {
        Assert ($tools.code -eq 0 -and $tools.json.result.AlreadyRemoved) 'VMware Tools removal reports nothing to remove'
        # A driver VMware Tools left behind (a synthetic service with one of
        # its names) and a synthetic device class listing it as a filter.
        & sc.exe create vmrawdsk type= kernel start= demand binPath= 'System32\drivers\sicm-test-none.sys' | Out-Host
        New-Item -Path $fakeClass -Force | Out-Null
        New-ItemProperty -Path $fakeClass -Name 'UpperFilters' -PropertyType MultiString -Value @('sicmkeep', 'vmrawdsk') -Force | Out-Null
        $left = Invoke-Engine 'RemoveVmwareTools' $null 'remove-tools-left'
        Assert ($left.code -eq 0 -and $left.json.result.Disabled -and $left.json.result.RestartRequired) 'Services and drivers VMware Tools left behind are turned off'
        Assert ([int](Get-ItemProperty -LiteralPath 'HKLM:\SYSTEM\CurrentControlSet\Services\vmrawdsk').Start -eq 4) 'A left-behind VMware driver is set not to start'
        Assert ((@((Get-ItemProperty -LiteralPath $fakeClass).UpperFilters) -join ',') -eq 'sicmkeep') 'The VMware driver is taken out of device filter lists and other filters are kept'
        $record = Get-Content -LiteralPath (Join-Path $work 'VMware-Tools-Disabled.json') -Raw | ConvertFrom-Json
        Assert ((@($record.Services | Where-Object { $_.Name -eq 'vmrawdsk' -and $_.Start -eq 3 }).Count -eq 1) -and (@($record.Filters | Where-Object { @($_.Before) -contains 'vmrawdsk' }).Count -eq 1)) 'The previous start type and filter list are saved first'
        $toolsState = Invoke-Engine 'Status' $null 'status-tools-off'
        Assert ($toolsState.code -eq 0 -and -not $toolsState.json.result.State.VmwareTools.Installed -and @($toolsState.json.result.State.VmwareTools.Running).Count -eq 0) 'Status no longer reports VMware Tools components turned on'
    }
    $bitlocker = Invoke-Engine 'SaveBitLocker' $null 'save-bitlocker'
    Assert ($bitlocker.code -eq 0 -and ($bitlocker.json.result.NoBitLocker -or (Test-Path -LiteralPath ([string]$bitlocker.json.result.KeyFile)))) 'Saving BitLocker keys succeeds, or reports that BitLocker is off'
    Assert ($bitlocker.log -notmatch 'Recovery password [0-9]') 'Recovery passwords never reach the log'
    $plan = Invoke-Engine 'OnlineDisks' $null 'missing-plan'
    Assert ($plan.code -ne 0 -and $plan.json.error -match 'plan file is missing') 'Changing modes need a plan file'
}
finally {
    & sc.exe delete vmrawdsk | Out-Null
    if (Test-Path -LiteralPath $fakeClass) { Remove-Item -LiteralPath $fakeClass -Recurse -Force }
    $device = Get-CimInstance -ClassName Win32_NetworkAdapter -Filter ('InterfaceIndex=' + $index) -ErrorAction SilentlyContinue
    if ($device -and $device.PNPDeviceID) { & pnputil.exe /remove-device $device.PNPDeviceID | Out-Host }
    $checks | Set-Content -LiteralPath (Join-Path $OutputDirectory 'checks.txt')
}
Write-Host ('All ' + $checks.Count + ' guest engine checks passed.')
