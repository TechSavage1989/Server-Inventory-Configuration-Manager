#requires -version 3.0
<#
.SYNOPSIS
    Creates a comprehensive inventory and configuration package for a Windows Server.

.DESCRIPTION
    Collects operating system, hardware, storage, networking, installed software,
    Windows roles and features, services, scheduled tasks, shares, permissions,
    local users and groups, applied policy, firewall configuration, certificate
    metadata, and common Windows Server role configuration.

    The script is designed to be tolerant of missing modules and roles. A failed
    collector is logged and the remaining collectors continue. The collected
    information is assembled into a primary Word report, a browser-viewable HTML
    report, and a concise text summary. Raw exports remain as supporting evidence
    and restoration artifacts.

    This is an inventory and configuration export. It is not a substitute for a
    system-state backup, bare-metal or virtual-machine backup, application-native
    backup, data backup, certificate private-key backup, or password vault.

.PARAMETER OutputRoot
    Parent folder in which the inventory package is created.

.PARAMETER InventoryCategoryList
    Semicolon-separated inventory categories selected by the application. Use All
    for the broadest read-only collection. Export selections automatically enable
    their matching inventory category.

.PARAMETER ExportCategoryList
    Semicolon-separated optional configuration backup/export modules. These are
    separate from normal inventory and never perform a restore or import operation.

.PARAMETER IncludeDomainInventory
    On a domain controller or management server with the ActiveDirectory module,
    exports users, groups, memberships, computers, OUs, trusts, sites, subnets,
    replication details, and password-policy metadata. Passwords and password
    hashes are never exported.

.PARAMETER IncludeGpoBackup
    Backs up all domain GPOs and exports reports, permissions, inheritance, and
    SYSVOL scripts when the GroupPolicy module is available. This output can
    contain sensitive scripts and policy data.

.PARAMETER IncludeShareAclBackup
    Runs ICACLS against non-special SMB share paths to save recursive NTFS ACLs.
    This can take a long time on large file servers.

.PARAMETER IncludeEventLogs
    Exports recent Critical, Error, and Warning events from the System and
    Application logs.

.PARAMETER EventLogDays
    Number of days of event history to export when IncludeEventLogs is selected.

.PARAMETER IncludeSensitiveRoleExports
    Enables exports that may contain plaintext or otherwise sensitive role data,
    such as an NPS configuration containing RADIUS shared secrets.

.PARAMETER CreateZip
    Creates a ZIP archive after collection. The default is True.

.EXAMPLE
    .\Export-ServerInventory.ps1

.EXAMPLE
    .\Export-ServerInventory.ps1 -OutputRoot D:\ServerInventory -IncludeDomainInventory -IncludeGpoBackup -IncludeShareAclBackup -IncludeEventLogs -EventLogDays 14

.NOTES
    Run from an elevated 64-bit Windows PowerShell session on the server being
    inventoried. Store the resulting package in a secured administrative location.
#>

[CmdletBinding()]
param(
    [Parameter()]
    [ValidateNotNullOrEmpty()]
    [string]$OutputRoot = 'C:\ServerInventory',

    [Parameter()]
    [string]$PackageName = '',

    [Parameter()]
    [string]$RunId = '',

    [Parameter()]
    [string]$InventoryCategoryList = 'All',

    [Parameter()]
    [string]$ExportCategoryList = '',

    [Parameter()]
    [switch]$IncludeDomainInventory,

    [Parameter()]
    [switch]$IncludeGpoBackup,

    [Parameter()]
    [switch]$IncludeShareAclBackup,

    [Parameter()]
    [switch]$IncludeEventLogs,

    [Parameter()]
    [ValidateRange(1, 365)]
    [int]$EventLogDays = 7,

    [Parameter()]
    [switch]$IncludeSensitiveRoleExports,

    [Parameter()]
    [bool]$CreateZip = $true,

    # BEGIN APPROVED ACL EXPORT PARAMETERS
    [Parameter()][ValidateRange(30, 3600)][int]$AclExportTimeoutSeconds = 120,
    [Parameter()][string]$AclControlPath = ''
    # END APPROVED ACL EXPORT PARAMETERS
)

$ErrorActionPreference = 'Stop'
$scriptVersion = '3.2.11'

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

if (-not (Test-IsAdministrator)) {
    throw 'Run this script from an elevated Windows PowerShell session.'
}

if ([Environment]::Is64BitOperatingSystem -and -not [Environment]::Is64BitProcess) {
    throw 'Run this script from a 64-bit Windows PowerShell session.'
}

$timestamp = Get-Date -Format 'yyyyMMdd_HHmmss'
if ([string]::IsNullOrWhiteSpace($PackageName)) {
    $PackageName = '{0}_ServerInventory_{1}' -f $env:COMPUTERNAME, $timestamp
}
else {
    if ([System.IO.Path]::IsPathRooted($PackageName) -or $PackageName.Contains('\') -or $PackageName.Contains('/')) {
        throw 'PackageName must be a folder name, not a path.'
    }
    foreach ($invalidCharacter in [System.IO.Path]::GetInvalidFileNameChars()) {
        if ($PackageName.Contains([string]$invalidCharacter)) {
            throw ('PackageName contains an invalid file-name character: ' + [string]$invalidCharacter)
        }
    }
}

$packageName = $PackageName
$packagePath = Join-Path $OutputRoot $packageName

New-Item -Path $OutputRoot -ItemType Directory -Force | Out-Null
if (Test-Path -LiteralPath $packagePath) {
    throw ('The inventory package directory already exists: ' + $packagePath)
}

$folderNames = @(
    '00-Summary',
    '01-System',
    '02-Hardware',
    '03-Storage',
    '04-Network',
    '05-Software-Services-Tasks',
    '06-Accounts-Policy-Security',
    '07-Shares-Permissions',
    '08-Role-Specific',
    '09-Event-History',
    '10-Recovery-Reference',
    'Logs',
    'Errors'
)

New-Item -Path $packagePath -ItemType Directory -Force | Out-Null
foreach ($folderName in $folderNames) {
    New-Item -Path (Join-Path $packagePath $folderName) -ItemType Directory -Force | Out-Null
}

$paths = @{
    Summary  = Join-Path $packagePath '00-Summary'
    System   = Join-Path $packagePath '01-System'
    Hardware = Join-Path $packagePath '02-Hardware'
    Storage  = Join-Path $packagePath '03-Storage'
    Network  = Join-Path $packagePath '04-Network'
    Software = Join-Path $packagePath '05-Software-Services-Tasks'
    Security = Join-Path $packagePath '06-Accounts-Policy-Security'
    Shares   = Join-Path $packagePath '07-Shares-Permissions'
    Roles    = Join-Path $packagePath '08-Role-Specific'
    Events   = Join-Path $packagePath '09-Event-History'
    Manual   = Join-Path $packagePath '10-Recovery-Reference'
    Logs     = Join-Path $packagePath 'Logs'
    Errors   = Join-Path $packagePath 'Errors'
}

$script:runLog = Join-Path $paths.Logs 'Collection.log'
$script:collectorResults = New-Object System.Collections.ArrayList
$script:detectedRoles = New-Object System.Collections.ArrayList
$script:transcriptStarted = $false
$script:installedFeatures = @()
$script:consolidatedReportResult = $null
# BEGIN APPROVED ACL EXPORT STATE
$script:aclSkipRemaining = $false
$script:aclExportResults = @()
$script:aclExportsIncompleteCount = 0
$script:aclExportUnsafePending = $false
# END APPROVED ACL EXPORT STATE


function ConvertTo-CategorySet {
    param(
        [Parameter()]
        [AllowNull()]
        [string]$List,
        [Parameter()]
        [switch]$DefaultAll
    )
    $set = @{}
    if (-not [string]::IsNullOrWhiteSpace($List)) {
        foreach ($rawItem in ($List -split '[,;|]')) {
            $item = ([string]$rawItem).Trim()
            if (-not [string]::IsNullOrWhiteSpace($item)) {
                $set[$item.ToLowerInvariant()] = $true
            }
        }
    }
    if (($set.Count -eq 0) -and $DefaultAll) {
        $set['all'] = $true
    }
    return $set
}

$script:inventoryCategorySet = ConvertTo-CategorySet -List $InventoryCategoryList -DefaultAll
$script:exportCategorySet = ConvertTo-CategorySet -List $ExportCategoryList

# Preserve compatibility with older launchers and command lines.
if ($IncludeDomainInventory) { $script:inventoryCategorySet['activedirectory'] = $true }
if ($IncludeGpoBackup) {
    $script:inventoryCategorySet['grouppolicy'] = $true
    $script:exportCategorySet['grouppolicy'] = $true
}
if ($IncludeShareAclBackup) {
    $script:inventoryCategorySet['sharespermissions'] = $true
    $script:exportCategorySet['sharespermissions'] = $true
}
if ($IncludeEventLogs) { $script:inventoryCategorySet['eventlogs'] = $true }

function Test-InventoryCategory {
    param([Parameter(Mandatory = $true)][string[]]$Category)
    if ($script:inventoryCategorySet.ContainsKey('all')) { return $true }
    foreach ($item in $Category) {
        $key = ([string]$item).ToLowerInvariant()
        if ($script:inventoryCategorySet.ContainsKey($key)) { return $true }
        # A selected backup/export module implicitly enables the corresponding read-only inventory.
        if ($script:exportCategorySet.ContainsKey($key)) { return $true }
    }
    return $false
}

function Test-ExportCategory {
    param([Parameter(Mandatory = $true)][string[]]$Category)
    if ($script:exportCategorySet.ContainsKey('all')) { return $true }
    foreach ($item in $Category) {
        if ($script:exportCategorySet.ContainsKey(([string]$item).ToLowerInvariant())) { return $true }
    }
    return $false
}

function Get-CollectorCategories {
    param([Parameter(Mandatory = $true)][string]$Name)
    switch -Wildcard ($Name) {
        'Operating system details' { return @('OperatingSystem') }
        'Computer and firmware details' { return @('Hardware') }
        'Processor and memory details' { return @('Hardware') }
        'PowerShell and locale details' { return @('OperatingSystem') }
        'Windows activation metadata' { return @('OperatingSystem') }
        'Windows roles and features' { return @('WindowsFeaturesRoles') }
        'Windows updates and packages' { return @('WindowsUpdate') }
        'Boot recovery power and pagefile configuration' { return @('Power','OperatingSystem') }
        'Environment variables' { return @('OperatingSystem') }
        'Device and driver inventory' { return @('Hardware') }
        'Disk partition volume and storage inventory' { return @('Storage') }
        'Network adapter and IP configuration' { return @('Networking') }
        'HTTP system bindings and remote management' { return @('RemoteManagement','Networking') }
        'Listening network endpoints' { return @('Networking','Security') }
        'NIC teaming configuration' { return @('Networking') }
        'Windows Time service configuration' { return @('TimeNTP') }
        'iSCSI and MPIO configuration' { return @('Storage') }
        'Installed applications' { return @('InstalledSoftware') }
        'Services and dependencies' { return @('Services') }
        'Scheduled tasks' { return @('ScheduledTasks') }
        'ODBC configuration' { return @('InstalledSoftware','SQLServer') }
        'Local users and groups' { return @('LocalAccounts') }
        'Effective policy and local policy backup' { return @('Security','GroupPolicy') }
        'Windows Firewall configuration' { return @('Firewall','Security') }
        'Windows Defender and security metadata' { return @('Security') }
        'BitLocker status without recovery keys' { return @('BitLocker','Security') }
        'Certificate inventory without private keys' { return @('Certificates','Security') }
        'SMB server and share configuration' { return @('SMB','SharesPermissions','FileServices') }
        'Existing Windows Server Backup metadata' { return @('WindowsServerBackup') }
        'Advanced storage and file service configuration' { return @('Storage','FileServices') }
        'DHCP Server*' { return @('DHCP') }
        'DNS Server*' { return @('DNS') }
        'IIS*' { return @('IIS') }
        'Hyper-V*' { return @('HyperV') }
        'Failover Cluster*' { return @('FailoverClustering') }
        'Print Server*' { return @('PrintServices') }
        'NPS*' { return @('NPS') }
        'Routing and Remote Access*' { return @('RRAS') }
        'Certification Authority*' { return @('Certificates','ADCS') }
        'SQL Server*' { return @('SQLServer') }
        'WSUS*' { return @('WindowsUpdate','WSUS') }
        'DFS*' { return @('DFS','FileServices') }
        'File Server Resource Manager*' { return @('FileServices') }
        'Active Directory*' { return @('ActiveDirectory') }
        'Deep Active Directory*' { return @('ActiveDirectory') }
        'Domain Group Policy*' { return @('GroupPolicy') }
        'Recent System and Application event history' { return @('EventLogs') }
        'Selected inventory registry configuration' { return @('OperatingSystem','Security') }
        'Consolidated inventory report generation' { return @('Always') }
        default { return @('Always') }
    }
}

function Format-ByteSize {
    param([Parameter()][AllowNull()][object]$Bytes)
    if ($null -eq $Bytes) { return '' }
    $value = 0.0
    try { $value = [double]$Bytes } catch { return [string]$Bytes }
    if ($value -lt 0) { return [string]$Bytes }
    $units = @('B','KB','MB','GB','TB','PB')
    $index = 0
    while (($value -ge 1024) -and ($index -lt ($units.Count - 1))) {
        $value = $value / 1024
        $index++
    }
    if ($index -eq 0) { return ('{0:N0} {1}' -f $value, $units[$index]) }
    if ($value -ge 100) { return ('{0:N0} {1}' -f $value, $units[$index]) }
    if ($value -ge 10) { return ('{0:N1} {1}' -f $value, $units[$index]) }
    return ('{0:N2} {1}' -f $value, $units[$index])
}

function New-StorageReadableRows {
    param(
        [Parameter()][AllowNull()][object]$InputObject,
        [Parameter(Mandatory = $true)][ValidateSet('Disk','LogicalDisk','Volume','PhysicalDisk','Pool','VirtualDisk','Partition')][string]$Kind
    )
    $rows = @()
    foreach ($item in @($InputObject)) {
        if ($null -eq $item) { continue }
        switch ($Kind) {
            'LogicalDisk' {
                $size = $item.Size; $free = $item.FreeSpace
                $rows += [pscustomobject]@{
                    Drive = $item.DeviceID; Label = $item.VolumeName; FileSystem = $item.FileSystem; DriveType = $item.DriveType
                    Capacity = Format-ByteSize $size; Free = Format-ByteSize $free
                    Used = if (($null -ne $size) -and ($null -ne $free)) { Format-ByteSize ([double]$size - [double]$free) } else { '' }
                    PercentFree = if ([double]$size -gt 0) { [math]::Round(([double]$free / [double]$size) * 100, 1) } else { $null }
                    CapacityBytes = $size; FreeBytes = $free
                }
            }
            'Volume' {
                $size = $item.Size; $free = $item.SizeRemaining
                if ($null -eq $free) { $free = $item.FreeSpace }
                $rows += [pscustomobject]@{
                    DriveLetter = $item.DriveLetter; Path = $item.Path; Label = if ($item.FileSystemLabel) { $item.FileSystemLabel } else { $item.Label }
                    FileSystem = $item.FileSystem; Health = $item.HealthStatus; Status = $item.OperationalStatus; DriveType = $item.DriveType
                    Capacity = Format-ByteSize $size; Free = Format-ByteSize $free
                    Used = if (($null -ne $size) -and ($null -ne $free)) { Format-ByteSize ([double]$size - [double]$free) } else { '' }
                    PercentFree = if ([double]$size -gt 0) { [math]::Round(([double]$free / [double]$size) * 100, 1) } else { $null }
                    CapacityBytes = $size; FreeBytes = $free
                }
            }
            'Disk' {
                $size = $item.Size
                $rows += [pscustomobject]@{
                    Number = if ($null -ne $item.Number) { $item.Number } else { $item.Index }
                    FriendlyName = if ($item.FriendlyName) { $item.FriendlyName } else { $item.Model }
                    Model = $item.Model; SerialNumber = $item.SerialNumber; BusType = $item.BusType; InterfaceType = $item.InterfaceType
                    PartitionStyle = $item.PartitionStyle; Health = $item.HealthStatus; Status = $item.OperationalStatus
                    Capacity = Format-ByteSize $size; CapacityBytes = $size
                }
            }
            'PhysicalDisk' {
                $size = $item.Size
                $rows += [pscustomobject]@{ FriendlyName=$item.FriendlyName; Model=$item.Model; SerialNumber=$item.SerialNumber; MediaType=$item.MediaType; BusType=$item.BusType; CanPool=$item.CanPool; Health=$item.HealthStatus; Status=$item.OperationalStatus; Capacity=(Format-ByteSize $size); CapacityBytes=$size; PhysicalSectorSize=$item.PhysicalSectorSize; LogicalSectorSize=$item.LogicalSectorSize }
            }
            'Pool' {
                $rows += [pscustomobject]@{ FriendlyName=$item.FriendlyName; Health=$item.HealthStatus; Status=$item.OperationalStatus; IsPrimordial=$item.IsPrimordial; Capacity=(Format-ByteSize $item.Size); Allocated=(Format-ByteSize $item.AllocatedSize); CapacityBytes=$item.Size; AllocatedBytes=$item.AllocatedSize }
            }
            'VirtualDisk' {
                $rows += [pscustomobject]@{ FriendlyName=$item.FriendlyName; ResiliencySettingName=$item.ResiliencySettingName; ProvisioningType=$item.ProvisioningType; Health=$item.HealthStatus; Status=$item.OperationalStatus; Capacity=(Format-ByteSize $item.Size); Footprint=(Format-ByteSize $item.FootprintOnPool); CapacityBytes=$item.Size; FootprintBytes=$item.FootprintOnPool }
            }
            'Partition' {
                $rows += [pscustomobject]@{ DiskNumber=$item.DiskNumber; PartitionNumber=$item.PartitionNumber; DriveLetter=$item.DriveLetter; Type=$item.Type; IsActive=$item.IsActive; IsBoot=$item.IsBoot; IsSystem=$item.IsSystem; Size=(Format-ByteSize $item.Size); SizeBytes=$item.Size; Offset=(Format-ByteSize $item.Offset); OffsetBytes=$item.Offset }
            }
        }
    }
    return $rows
}

function Write-InventoryLog {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Message,

        [Parameter()]
        [ValidateSet('INFO', 'WARN', 'ERROR')]
        [string]$Level = 'INFO'
    )

    $line = '{0} [{1}] {2}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Level, $Message
    $line | Out-File -FilePath $script:runLog -Append -Encoding UTF8
    Write-Host $line
}

function Get-SafeFileName {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name
    )

    $safeName = $Name
    foreach ($character in [System.IO.Path]::GetInvalidFileNameChars()) {
        $safeName = $safeName.Replace([string]$character, '_')
    }
    $safeName = $safeName.Replace('\', '_').Replace('/', '_').Replace(':', '_')
    if ([string]::IsNullOrWhiteSpace($safeName)) {
        $safeName = 'Unnamed'
    }
    return $safeName
}

function Get-SHA256FileHash {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    if (Get-Command Get-FileHash -ErrorAction SilentlyContinue) {
        return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash
    }

    $stream = $null
    $sha256 = $null
    try {
        $stream = [System.IO.File]::OpenRead($Path)
        $sha256 = [System.Security.Cryptography.SHA256]::Create()
        $hashBytes = $sha256.ComputeHash($stream)
        return (($hashBytes | ForEach-Object { $_.ToString('x2') }) -join '').ToUpperInvariant()
    }
    finally {
        if ($stream) { $stream.Dispose() }
        if ($sha256) { $sha256.Dispose() }
    }
}

function Save-ObjectData {
    param(
        [Parameter(Mandatory = $true)]
        [AllowNull()]
        [object]$InputObject,

        [Parameter(Mandatory = $true)]
        [string]$BasePath,

        [Parameter()]
        [switch]$SkipCsv
    )

    $parent = Split-Path -Path $BasePath -Parent
    if (-not (Test-Path -LiteralPath $parent)) {
        New-Item -Path $parent -ItemType Directory -Force | Out-Null
    }

    if ($null -eq $InputObject) {
        'No data returned.' | Set-Content -Path ($BasePath + '.txt') -Encoding UTF8
        return
    }

    # Avoid PowerShell 5.1's argument-type mismatch when array-subexpressing
    # generic List[T] instances. Flatten any enumerable explicitly into a normal
    # ArrayList before exporting. Strings remain scalar values.
    $items = New-Object System.Collections.ArrayList
    if (($InputObject -is [System.Collections.IEnumerable]) -and (-not ($InputObject -is [string]))) {
        foreach ($item in $InputObject) {
            [void]$items.Add($item)
        }
    }
    else {
        [void]$items.Add($InputObject)
    }

    if ($items.Count -eq 0) {
        'No data returned.' | Set-Content -Path ($BasePath + '.txt') -Encoding UTF8
        return
    }

    $items | Export-Clixml -Path ($BasePath + '.clixml') -Force

    if (-not $SkipCsv) {
        try {
            $items | Export-Csv -Path ($BasePath + '.csv') -NoTypeInformation -Encoding UTF8 -Force
        }
        catch {
            ('CSV export was not possible: ' + $_.Exception.Message) |
                Set-Content -Path ($BasePath + '.csv-error.txt') -Encoding UTF8
        }
    }

    try {
        $items | Format-List * | Out-String -Width 4096 |
            Set-Content -Path ($BasePath + '.txt') -Encoding UTF8
    }
    catch {
        ('Text rendering was not possible: ' + $_.Exception.Message) |
            Set-Content -Path ($BasePath + '.text-error.txt') -Encoding UTF8
    }
}

function Invoke-NativeCapture {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Command,

        [Parameter()]
        [string[]]$Arguments = @(),

        [Parameter(Mandatory = $true)]
        [string]$OutputFile,

        [Parameter()]
        [switch]$AllowNonZeroExit
    )

    $parent = Split-Path -Path $OutputFile -Parent
    if (-not (Test-Path -LiteralPath $parent)) {
        New-Item -Path $parent -ItemType Directory -Force | Out-Null
    }

    $resolvedCommand = Get-Command -Name $Command -ErrorAction SilentlyContinue
    if (-not $resolvedCommand) {
        throw ('Native command was not found: ' + $Command)
    }

    $previousErrorActionPreference = $ErrorActionPreference
    $output = @()
    $exitCode = 0
    try {
        # Some Windows utilities write successful informational messages to stderr.
        # Do not promote those records to terminating errors while capturing output.
        $ErrorActionPreference = 'Continue'
        $output = @(& $Command @Arguments 2>&1)
        if ($null -ne $LASTEXITCODE) {
            $exitCode = [int]$LASTEXITCODE
        }
    }
    finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }

    $renderedOutput = @(
        foreach ($outputItem in $output) {
            if ($outputItem -is [System.Management.Automation.ErrorRecord]) {
                [string]$outputItem.Exception.Message
            }
            else {
                [string]$outputItem
            }
        }
    )
    $renderedOutput | Out-File -FilePath $OutputFile -Encoding UTF8 -Width 4096

    if (($exitCode -ne 0) -and (-not $AllowNonZeroExit)) {
        throw ('{0} returned exit code {1}.' -f $Command, $exitCode)
    }

}

# BEGIN APPROVED ACL EXPORT HELPERS
function Initialize-AclExportProcessType {
    if ('ServerInventory.AclExportProcess' -as [type]) { return }
    Add-Type -TypeDefinition @'
using System;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Threading.Tasks;
namespace ServerInventory {
    public sealed class AclExportProcess : IDisposable {
        private Process process;
        private FileStream outputFile, errorFile;
        private Task outputCopy, errorCopy;
        private bool started;
        public bool HasExited { get { return process.HasExited; } }
        public int Id { get { return process.Id; } }
        public int ExitCode { get { return process.ExitCode; } }
        private static string Quote(string value) {
            if (value == null) value = "";
            StringBuilder result = new StringBuilder("\"");
            int slashes = 0;
            foreach (char c in value) {
                if (c == '\\') { slashes++; continue; }
                if (c == '"') { result.Append('\\', slashes * 2 + 1); result.Append(c); slashes = 0; continue; }
                result.Append('\\', slashes); slashes = 0; result.Append(c);
            }
            result.Append('\\', slashes * 2); result.Append('"'); return result.ToString();
        }
        public void Start(string executable, string[] arguments, string output, string error) {
            if (process != null) throw new InvalidOperationException("An ACL export process can only be started once.");
            outputFile = new FileStream(output, FileMode.CreateNew, FileAccess.Write, FileShare.Read, 1, true);
            errorFile = new FileStream(error, FileMode.CreateNew, FileAccess.Write, FileShare.Read, 1, true);
            ProcessStartInfo info = new ProcessStartInfo();
            info.FileName = executable;
            StringBuilder args = new StringBuilder();
            foreach (string argument in arguments) { if (args.Length > 0) args.Append(' '); args.Append(Quote(argument)); }
            info.Arguments = args.ToString(); info.UseShellExecute = false; info.CreateNoWindow = true;
            info.RedirectStandardOutput = true; info.RedirectStandardError = true;
            process = new Process(); process.StartInfo = info;
            if (!process.Start()) throw new InvalidOperationException("The ACL export process could not start.");
            started = true;
            outputCopy = process.StandardOutput.BaseStream.CopyToAsync(outputFile, 65536);
            errorCopy = process.StandardError.BaseStream.CopyToAsync(errorFile, 65536);
        }
        public bool StopOwned() {
            if (process == null || !started) return true;
            if (!process.HasExited) process.Kill();
            return process.WaitForExit(2000);
        }
        public bool FinishOutput() {
            if (!process.HasExited) return false;
            if (!Task.WaitAll(new Task[] { outputCopy, errorCopy }, 2000)) return false;
            return true;
        }
        public void Dispose() {
            if (process != null && started && !process.HasExited) { if (!StopOwned()) throw new InvalidOperationException("Owned ACL process termination is still pending."); }
            Task close = Task.Factory.StartNew(delegate {
                if (outputFile != null) outputFile.Dispose();
                if (errorFile != null) errorFile.Dispose();
                if (process != null) process.Dispose();
            });
            if (!close.Wait(2000)) throw new InvalidOperationException("ACL output cleanup is still pending.");
        }
    }
}
'@ | Out-Null
}

function Test-AclExportSkipRequested {
    if ($script:aclSkipRemaining) { return $true }
    if ([string]::IsNullOrWhiteSpace($AclControlPath) -or [string]::IsNullOrWhiteSpace($RunId)) { return $false }
    try {
        $info = New-Object System.IO.FileInfo($AclControlPath)
        if ($info.Exists -and $info.Length -le 4096) {
            $control = [IO.File]::ReadAllText($AclControlPath) | ConvertFrom-Json
            if (([string]$control.RunId -ceq $RunId) -and ($control.SkipRemaining -eq $true)) {
                $script:aclSkipRemaining = $true
                return $true
            }
        }
    }
    catch { } # An absent, partially written, or stale control file is not a skip.
    return $false
}

function Write-AclExportStatus {
    param([string]$State, [int]$ShareIndex, [int]$ShareCount, [string]$ShareName, [int]$ElapsedSeconds, [int]$TimeoutSeconds)
    $status = [ordered]@{ RunId=$RunId; State=$State; ShareIndex=$ShareIndex; ShareCount=$ShareCount; ShareName=$ShareName; ElapsedSeconds=$ElapsedSeconds; TimeoutSeconds=$TimeoutSeconds }
    Write-Host ('@@ACL_EXPORT@@' + ($status | ConvertTo-Json -Compress -Depth 3))
}

function Invoke-BoundedAclProcess {
    param(
        [Parameter(Mandatory=$true)][string]$Command,
        [Parameter(Mandatory=$true)][string[]]$Arguments,
        [Parameter(Mandatory=$true)][string]$OutputFile,
        [Parameter(Mandatory=$true)][ValidateRange(1,3600)][int]$TimeoutSeconds,
        [int]$ShareIndex=1, [int]$ShareCount=1, [string]$ShareName=''
    )
    Initialize-AclExportProcessType
    $process = New-Object ServerInventory.AclExportProcess
    $watch = [Diagnostics.Stopwatch]::StartNew()
    $state = 'Failed'; $exitCode = $null; $reason = ''; $nextHeartbeat = 0
    try {
        if (Test-AclExportSkipRequested) { $state='Skipped'; $reason='Skipped by the operator before starting.' }
        else {
            $process.Start($Command, $Arguments, $OutputFile, ($OutputFile + '.stderr.txt'))
            while (-not $process.HasExited) {
                $elapsed = [int][Math]::Floor($watch.Elapsed.TotalSeconds)
                if ($watch.Elapsed.TotalSeconds -ge $nextHeartbeat) {
                    Write-AclExportStatus -State 'Running' -ShareIndex $ShareIndex -ShareCount $ShareCount -ShareName $ShareName -ElapsedSeconds $elapsed -TimeoutSeconds $TimeoutSeconds
                    Write-InventoryLog -Message ('Recursive ACL export: share {0}/{1} ({2}), elapsed {3}s, limit {4}s. File traversal progress is unknown.' -f $ShareIndex,$ShareCount,$ShareName,$elapsed,$TimeoutSeconds)
                    $nextHeartbeat = $watch.Elapsed.TotalSeconds + 5
                }
                if (Test-AclExportSkipRequested) { $state='Skipped'; $reason='Operator skipped the remaining recursive ACL exports.'; break }
                if ($watch.Elapsed.TotalSeconds -ge $TimeoutSeconds) { $state='TimedOut'; $reason=('Per-share time limit of {0}s reached.' -f $TimeoutSeconds); break }
                Start-Sleep -Milliseconds 250
            }
            if (-not $process.HasExited) {
                if (-not $process.StopOwned()) { throw 'The owned ACL export process did not stop within two seconds.' }
            }
            $exitCode = $process.ExitCode
            if (-not $process.FinishOutput()) { throw 'ACL export output streams did not finish within two seconds.' }
            if ($state -ne 'Skipped' -and $state -ne 'TimedOut') {
                if ($exitCode -eq 0) { $state='Completed' }
                else { $state='Failed'; $reason=('ICACLS exited with code {0}; the ACL export is incomplete.' -f $exitCode) }
            }
        }
    }
    catch { $state='Failed'; $reason=$_.Exception.Message }
    finally {
        try { $process.Dispose() } catch { $state='Failed'; $reason=('ACL child cleanup failed: ' + $_.Exception.Message); $script:aclSkipRemaining=$true; $script:aclExportUnsafePending=$true }
        $watch.Stop()
    }
    $elapsed = [int][Math]::Floor($watch.Elapsed.TotalSeconds)
    # Final Completed is emitted by the caller only after the ACL file is validated and promoted.
    return [pscustomobject]@{ Status=$state; ExitCode=$exitCode; ElapsedSeconds=$elapsed; Reason=$reason }
}

function Invoke-RecursiveAclExports {
    param([AllowEmptyCollection()][object[]]$Shares, [Parameter(Mandatory=$true)][string]$Destination)
    New-Item -Path $Destination -ItemType Directory -Force | Out-Null
    # Do not Test-Path each share here: a blocked filesystem probe would bypass the native timeout.
    $selected = @($Shares | Where-Object { (-not $_.Special) -and $_.Path })
    $mappingPath = Join-Path $Destination 'ICACLS-Mapping.csv'
    $rows = New-Object System.Collections.ArrayList
    for ($i=0; $i -lt $selected.Count; $i++) {
        $name = '{0:D4}-{1}' -f ($i+1),(Get-SafeFileName -Name $selected[$i].Name)
        [void]$rows.Add([pscustomobject]@{
            ShareName=$selected[$i].Name; SourcePath=$selected[$i].Path; AclFile=($name+'.acl.incomplete')
            Status='Incomplete'; ExitCode=$null; ElapsedSeconds=0; TimeoutSeconds=$AclExportTimeoutSeconds
            Note='Not finished. Do not use an incomplete ACL file for restoration.'
        })
    }
    # Persist unfinished state before starting any child, including when the whole collection is canceled.
    $rows | Export-Csv -LiteralPath $mappingPath -NoTypeInformation -Encoding UTF8
    try {
        for ($i=0; $i -lt $selected.Count; $i++) {
            $row=$rows[$i]; $share=$selected[$i]
            if (Test-AclExportSkipRequested) {
                $row.Status='Skipped'; $row.Note='Operator skipped this recursive ACL export; no complete ACL backup exists.'
                Write-AclExportStatus -State 'Skipped' -ShareIndex ($i+1) -ShareCount $selected.Count -ShareName $share.Name -ElapsedSeconds 0 -TimeoutSeconds $AclExportTimeoutSeconds
            }
            else {
                $partialPath=Join-Path $Destination $row.AclFile
                $logPath=Join-Path $Destination ($row.AclFile + '.log.txt')
                $native=Join-Path $env:SystemRoot 'System32\icacls.exe'
                Write-InventoryLog -Message ('Starting recursive ACL export: share {0}/{1} ({2}), limit {3}s.' -f ($i+1),$selected.Count,$share.Name,$AclExportTimeoutSeconds)
                $result=Invoke-BoundedAclProcess -Command $native -Arguments @([string]$share.Path,'/save',$partialPath,'/t','/c','/q') -OutputFile $logPath -TimeoutSeconds $AclExportTimeoutSeconds -ShareIndex ($i+1) -ShareCount $selected.Count -ShareName $share.Name
                $row.Status=$result.Status; $row.ExitCode=$result.ExitCode; $row.ElapsedSeconds=$result.ElapsedSeconds; $row.Note=$result.Reason
                if ($row.Status -eq 'Completed') {
                    $row.Status='Incomplete'
                    try {
                    if ((Test-Path -LiteralPath $partialPath -PathType Leaf) -and ((Get-Item -LiteralPath $partialPath).Length -gt 0)) {
                        $completeName=$row.AclFile.Substring(0,$row.AclFile.Length-'.incomplete'.Length)
                        Move-Item -LiteralPath $partialPath -Destination (Join-Path $Destination $completeName) -ErrorAction Stop
                        $row.AclFile=$completeName
                        $row.Status='Completed'
                        $row.Note='ICACLS completed successfully. Review its logs and test restore against the correct root before relying on this file.'
                    }
                    else { $row.Status='Failed'; $row.Note='ICACLS did not create a nonempty ACL file. No complete ACL backup exists.' }
                    } catch { $row.Status='Failed'; $row.Note=('ACL file promotion failed: ' + $_.Exception.Message) }
                }
            }
            Write-AclExportStatus -State $row.Status -ShareIndex ($i+1) -ShareCount $selected.Count -ShareName $share.Name -ElapsedSeconds $row.ElapsedSeconds -TimeoutSeconds $AclExportTimeoutSeconds
            $rows | Export-Csv -LiteralPath $mappingPath -NoTypeInformation -Encoding UTF8
            if ($row.Status -ne 'Completed') { Write-InventoryLog -Level 'WARN' -Message ('Recursive ACL export incomplete for {0}: {1}. {2}' -f $row.ShareName,$row.Status,$row.Note) }
            else { Write-InventoryLog -Message ('Completed recursive ACL export: ' + $row.ShareName) }
        }
    }
    finally {
        $script:aclExportResults=@($rows.ToArray())
        $script:aclExportsIncompleteCount=@($script:aclExportResults | Where-Object { $_.Status -ne 'Completed' }).Count
        Write-AclExportStatus -State 'Idle' -ShareIndex 0 -ShareCount $selected.Count -ShareName '' -ElapsedSeconds 0 -TimeoutSeconds $AclExportTimeoutSeconds
    }
}
# END APPROVED ACL EXPORT HELPERS

function Export-RegistryKey {
    param(
        [Parameter(Mandatory = $true)]
        [string]$RegistryPath,

        [Parameter(Mandatory = $true)]
        [string]$DestinationFile
    )

    $parent = Split-Path -Path $DestinationFile -Parent
    if (-not (Test-Path -LiteralPath $parent)) {
        New-Item -Path $parent -ItemType Directory -Force | Out-Null
    }

    & reg.exe export $RegistryPath $DestinationFile /y | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw ('Registry export failed for {0}.' -f $RegistryPath)
    }
}


function Get-InstalledApplicationInventory {
    $applicationLocations = @(
        [pscustomobject]@{ Path = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*'; Architecture = '64-bit or native' },
        [pscustomobject]@{ Path = 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'; Architecture = '32-bit' }
    )
    foreach ($location in $applicationLocations) {
        Get-ItemProperty -Path $location.Path -ErrorAction SilentlyContinue |
            Where-Object { $_.DisplayName } |
            ForEach-Object {
                [pscustomobject]@{
                    DisplayName=$_.DisplayName; DisplayVersion=$_.DisplayVersion; Publisher=$_.Publisher
                    InstallDate=$_.InstallDate; InstallLocation=$_.InstallLocation; InstallSource=$_.InstallSource
                    Architecture=$location.Architecture; ProductCodeOrKey=$_.PSChildName
                }
            }
    }
}

function Invoke-SqlMetadataQuery {
    param(
        [Parameter(Mandatory = $true)][string]$DataSource,
        [Parameter(Mandatory = $true)][string]$Query,
        [Parameter()][string]$Database = 'master',
        [Parameter()][int]$ConnectTimeout = 4,
        [Parameter()][int]$CommandTimeout = 20
    )
    $connection = $null
    $command = $null
    $reader = $null
    try {
        $connectionString = 'Data Source={0};Initial Catalog={1};Integrated Security=SSPI;Connect Timeout={2};Application Name=Server Inventory and Configuration Exporter' -f $DataSource, $Database, $ConnectTimeout
        $connection = New-Object System.Data.SqlClient.SqlConnection($connectionString)
        $connection.Open()
        $command = $connection.CreateCommand()
        $command.CommandText = $Query
        $command.CommandTimeout = $CommandTimeout
        $reader = $command.ExecuteReader()
        $rows = New-Object System.Collections.ArrayList
        while ($reader.Read()) {
            $properties = [ordered]@{}
            for ($i = 0; $i -lt $reader.FieldCount; $i++) {
                $name = $reader.GetName($i)
                $value = if ($reader.IsDBNull($i)) { $null } else { $reader.GetValue($i) }
                $properties[$name] = $value
            }
            [void]$rows.Add([pscustomobject]$properties)
        }
        return $rows
    }
    finally {
        if ($reader) { $reader.Close(); $reader.Dispose() }
        if ($command) { $command.Dispose() }
        if ($connection) { $connection.Close(); $connection.Dispose() }
    }
}

function Get-SqlRegistryInstances {
    $rows = New-Object System.Collections.ArrayList
    $instancePaths = @(
        'HKLM:\\SOFTWARE\\Microsoft\\Microsoft SQL Server\\Instance Names\\SQL',
        'HKLM:\\SOFTWARE\\WOW6432Node\\Microsoft\\Microsoft SQL Server\\Instance Names\\SQL'
    )
    foreach ($instancePath in $instancePaths) {
        if (-not (Test-Path -LiteralPath $instancePath)) { continue }
        try {
            $item = Get-ItemProperty -LiteralPath $instancePath -ErrorAction Stop
            foreach ($property in $item.PSObject.Properties | Where-Object { $_.Name -notlike 'PS*' }) {
                $exists = $false
                foreach ($existing in $rows) {
                    if (($existing.InstanceName -eq $property.Name) -and ($existing.InstanceId -eq [string]$property.Value)) { $exists = $true; break }
                }
                if (-not $exists) {
                    [void]$rows.Add([pscustomobject]@{ RegistryPath=$instancePath; InstanceName=$property.Name; InstanceId=[string]$property.Value })
                }
            }
        } catch { }
    }
    return $rows
}

function Get-SqlInstanceWindowsMetadata {
    param([Parameter(Mandatory = $true)][object]$Instance)
    $instanceName = [string]$Instance.InstanceName
    $instanceId = [string]$Instance.InstanceId
    $baseCandidates = @(
        ('HKLM:\\SOFTWARE\\Microsoft\\Microsoft SQL Server\\' + $instanceId),
        ('HKLM:\\SOFTWARE\\WOW6432Node\\Microsoft\\Microsoft SQL Server\\' + $instanceId)
    )
    $basePath = $baseCandidates | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
    $setup = $null; $server = $null; $tcpAll = $null; $netLib = $null
    if ($basePath) {
        try { $setup = Get-ItemProperty -LiteralPath (Join-Path $basePath 'Setup') -ErrorAction SilentlyContinue } catch { }
        try { $server = Get-ItemProperty -LiteralPath (Join-Path $basePath 'MSSQLServer') -ErrorAction SilentlyContinue } catch { }
        try { $tcpAll = Get-ItemProperty -LiteralPath (Join-Path $basePath 'MSSQLServer\\SuperSocketNetLib\\Tcp\\IPAll') -ErrorAction SilentlyContinue } catch { }
        try { $netLib = Get-ItemProperty -LiteralPath (Join-Path $basePath 'MSSQLServer\\SuperSocketNetLib') -ErrorAction SilentlyContinue } catch { }
    }
    $engineServiceName = if ($instanceName -eq 'MSSQLSERVER') { 'MSSQLSERVER' } else { 'MSSQL$' + $instanceName }
    $agentServiceName = if ($instanceName -eq 'MSSQLSERVER') { 'SQLSERVERAGENT' } else { 'SQLAgent$' + $instanceName }
    $engineService = Get-CimInstance -ClassName Win32_Service -Filter ("Name='" + $engineServiceName.Replace("'","''") + "'") -ErrorAction SilentlyContinue
    $agentService = Get-CimInstance -ClassName Win32_Service -Filter ("Name='" + $agentServiceName.Replace("'","''") + "'") -ErrorAction SilentlyContinue
    [pscustomobject]@{
        InstanceName=$instanceName; InstanceId=$instanceId; RegistryBase=$basePath
        Edition=if($setup){$setup.Edition}else{$null}; Version=if($setup){$setup.Version}else{$null}; PatchLevel=if($setup){$setup.PatchLevel}else{$null}
        SQLPath=if($setup){$setup.SQLPath}else{$null}; SQLDataRoot=if($setup){$setup.SQLDataRoot}else{$null}
        DefaultDataDirectory=if($server){$server.DefaultData}else{$null}; DefaultLogDirectory=if($server){$server.DefaultLog}else{$null}; DefaultBackupDirectory=if($server){$server.BackupDirectory}else{$null}
        AuthenticationMode=if($server -and $null -ne $server.LoginMode){ if([int]$server.LoginMode -eq 2){'Mixed mode'}else{'Windows authentication'} }else{$null}
        TcpEnabled=if($tcpAll -or $netLib){ if($tcpAll){'Configured'}else{'Unknown'} }else{$null}; TcpPort=if($tcpAll){$tcpAll.TcpPort}else{$null}; TcpDynamicPorts=if($tcpAll){$tcpAll.TcpDynamicPorts}else{$null}; ForceEncryption=if($netLib){$netLib.ForceEncryption}else{$null}; HideInstance=if($netLib){$netLib.HideInstance}else{$null}
        EngineService=$engineServiceName; EngineServiceState=if($engineService){$engineService.State}else{$null}; EngineStartMode=if($engineService){$engineService.StartMode}else{$null}; EngineServiceAccount=if($engineService){$engineService.StartName}else{$null}; EngineBinaryPath=if($engineService){$engineService.PathName}else{$null}
        AgentService=$agentServiceName; AgentServiceState=if($agentService){$agentService.State}else{$null}; AgentStartMode=if($agentService){$agentService.StartMode}else{$null}; AgentServiceAccount=if($agentService){$agentService.StartName}else{$null}
    }
}

function Invoke-Collector {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name,

        [Parameter(Mandatory = $true)]
        [scriptblock]$Action
    )

    $collectorCategories = @(Get-CollectorCategories -Name $Name)
    if (($collectorCategories -notcontains 'Always') -and (-not (Test-InventoryCategory -Category $collectorCategories))) {
        $now = Get-Date
        [void]$script:collectorResults.Add([pscustomobject]@{
            Collector = $Name; Status = 'Skipped'; Started = $now; Ended = $now; DurationSec = 0
            ErrorMessage = ('Not selected. Category: ' + ($collectorCategories -join ', '))
        })
        Write-InventoryLog -Message ('Skipped: ' + $Name + ' (not selected)')
        return
    }

    $started = Get-Date
    Write-InventoryLog -Message ('Starting: ' + $Name)

    try {
        & $Action
        $ended = Get-Date
        [void]$script:collectorResults.Add([pscustomobject]@{
            Collector     = $Name
            Status        = 'Success'
            Started       = $started
            Ended         = $ended
            DurationSec   = [math]::Round(($ended - $started).TotalSeconds, 2)
            ErrorMessage  = $null
        })
        Write-InventoryLog -Message ('Completed: ' + $Name)
    }
    catch {
        $ended = Get-Date
        $errorFile = Join-Path $paths.Errors ((Get-SafeFileName -Name $Name) + '.txt')
        $_ | Format-List * -Force | Out-String -Width 4096 |
            Set-Content -Path $errorFile -Encoding UTF8

        [void]$script:collectorResults.Add([pscustomobject]@{
            Collector     = $Name
            Status        = 'Failed'
            Started       = $started
            Ended         = $ended
            DurationSec   = [math]::Round(($ended - $started).TotalSeconds, 2)
            ErrorMessage  = $_.Exception.Message
        })
        Write-InventoryLog -Message ('Failed: {0}. {1}' -f $Name, $_.Exception.Message) -Level 'WARN'
    }
}

function Add-DetectedRole {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Role,

        [Parameter(Mandatory = $true)]
        [string]$DetectedBy,

        [Parameter()]
        [string]$Notes = ''
    )

    $existing = $script:detectedRoles | Where-Object { $_.Role -eq $Role }
    if (-not $existing) {
        [void]$script:detectedRoles.Add([pscustomobject]@{
            Role       = $Role
            DetectedBy = $DetectedBy
            Notes      = $Notes
        })
    }
}

function Get-SelectedCategoryRows {
    param(
        [Parameter(Mandatory = $true)][ValidateSet('Inventory','Export')][string]$Mode
    )

    $definitions = @(
        [pscustomobject]@{ Key='Hardware'; Label='Hardware'; Purpose='System, CPU, memory, firmware, devices, and drivers' },
        [pscustomobject]@{ Key='OperatingSystem'; Label='Operating system'; Purpose='Windows version, build, activation, environment, boot, and platform baseline' },
        [pscustomobject]@{ Key='Storage'; Label='Storage'; Purpose='Disks, partitions, volumes, pools, iSCSI, MPIO, and advanced storage' },
        [pscustomobject]@{ Key='Networking'; Label='Networking'; Purpose='IP addresses, DNS client, gateways, routing, listeners, adapters, and teaming' },
        [pscustomobject]@{ Key='DNS'; Label='DNS Server'; Purpose='Zones, records, forwarders, scavenging, and server settings' },
        [pscustomobject]@{ Key='DHCP'; Label='DHCP Server'; Purpose='Scopes, exclusions, reservations, options, policies, filters, leases, bindings, and failover' },
        [pscustomobject]@{ Key='ActiveDirectory'; Label='Active Directory'; Purpose='Domain, forest, FSMO, topology, objects, sites, trusts, and replication' },
        [pscustomobject]@{ Key='GroupPolicy'; Label='Group Policy'; Purpose='GPO inventory, links, permissions, inheritance, reports, and backups when selected' },
        [pscustomobject]@{ Key='FileServices'; Label='File services'; Purpose='FSRM, NFS, file services, deduplication, Storage Replica, and advanced storage' },
        [pscustomobject]@{ Key='SharesPermissions'; Label='Shares and permissions'; Purpose='SMB shares, share ACLs, root NTFS permissions, and optional recursive ACL export' },
        [pscustomobject]@{ Key='PrintServices'; Label='Print services'; Purpose='Queues, drivers, ports, and optional PrintBRM migration export' },
        [pscustomobject]@{ Key='IIS'; Label='IIS'; Purpose='Sites, applications, pools, bindings, paths, and optional IIS configuration backup' },
        [pscustomobject]@{ Key='SQLServer'; Label='SQL Server'; Purpose='Instances, databases, files, space, configuration, jobs, linked servers, backup history, services, ports, and registry metadata' },
        [pscustomobject]@{ Key='HyperV'; Label='Hyper-V'; Purpose='Host, VMs, disks, switches, network adapters, replication, and paths' },
        [pscustomobject]@{ Key='FailoverClustering'; Label='Failover Clustering'; Purpose='Nodes, groups, resources, networks, CSVs, dependencies, and quorum' },
        [pscustomobject]@{ Key='WindowsServerBackup'; Label='Windows Server Backup'; Purpose='Policy, schedule, status, and available backup versions' },
        [pscustomobject]@{ Key='Certificates'; Label='Certificates / AD CS'; Purpose='Certificate metadata, stores, bindings, and CA configuration without private keys' },
        [pscustomobject]@{ Key='ScheduledTasks'; Label='Scheduled Tasks'; Purpose='Tasks, principals, actions, triggers, state, and optional XML definitions' },
        [pscustomobject]@{ Key='Services'; Label='Services'; Purpose='Service accounts, binaries, startup, dependencies, and recovery configuration' },
        [pscustomobject]@{ Key='InstalledSoftware'; Label='Installed software / ODBC'; Purpose='Applications, SQL components, application folders, ODBC drivers, and data sources' },
        [pscustomobject]@{ Key='WindowsFeaturesRoles'; Label='Windows Features / Roles'; Purpose='Installed Windows roles, features, and management components' },
        [pscustomobject]@{ Key='Firewall'; Label='Firewall / IPsec'; Purpose='Profiles, rules, filters, IPsec, and optional importable firewall policy' },
        [pscustomobject]@{ Key='RRAS'; Label='RRAS / Routing'; Purpose='Routing, VPN, NAT, RemoteAccess, and reconstruction reference' },
        [pscustomobject]@{ Key='NPS'; Label='NPS / RADIUS'; Purpose='NPS configuration summary and optional sensitive full export' },
        [pscustomobject]@{ Key='BitLocker'; Label='BitLocker'; Purpose='Encryption status without recovery keys' },
        [pscustomobject]@{ Key='SMB'; Label='SMB configuration'; Purpose='SMB server/client configuration, shares, protocol settings, and signing/encryption metadata' },
        [pscustomobject]@{ Key='DFS'; Label='DFS'; Purpose='Namespaces, targets, replication groups, memberships, and connections' },
        [pscustomobject]@{ Key='WindowsUpdate'; Label='Windows Update / WSUS'; Purpose='Hotfixes, packages, Windows Update configuration, and WSUS metadata' },
        [pscustomobject]@{ Key='EventLogs'; Label='Event-log summaries'; Purpose='Recent Critical, Error, and Warning events' },
        [pscustomobject]@{ Key='LocalAccounts'; Label='Local users / groups'; Purpose='Local accounts, groups, memberships, and account policy' },
        [pscustomobject]@{ Key='Power'; Label='Power / boot / recovery'; Purpose='Power plans, pagefile, boot configuration, and Windows Recovery Environment' },
        [pscustomobject]@{ Key='TimeNTP'; Label='Time / NTP'; Purpose='W32Time configuration, peers, status, and source' },
        [pscustomobject]@{ Key='Security'; Label='Security configuration'; Purpose='Local policy, audit, Defender, TLS, RDP, and security metadata' },
        [pscustomobject]@{ Key='RemoteManagement'; Label='Remote management'; Purpose='WinRM, PowerShell remoting, and HTTP.sys configuration' },
        [pscustomobject]@{ Key='Registry'; Label='Relevant registry exports'; Purpose='Selected configuration registry branches for migration or recovery reference' }
    )

    $set = if ($Mode -eq 'Inventory') { $script:inventoryCategorySet } else { $script:exportCategorySet }
    $allSelected = $set.ContainsKey('all')
    $rows = New-Object System.Collections.ArrayList
    foreach ($definition in $definitions) {
        $key = ([string]$definition.Key).ToLowerInvariant()
        if ($allSelected -or $set.ContainsKey($key)) {
            [void]$rows.Add([pscustomobject]@{
                Category = $definition.Label
                Purpose = $definition.Purpose
                Mode = if ($Mode -eq 'Inventory') { 'Inventory / read-only collection' } else { 'Backup / export capture' }
            })
        }
    }
    return @($rows)
}

function Get-RoleRecoveryReferenceRows {
    param(
        [Parameter()][AllowNull()][object[]]$RoleRows
    )

    $rows = New-Object System.Collections.ArrayList
    foreach ($roleItem in @($RoleRows)) {
        if ($null -eq $roleItem) { continue }
        $role = [string]$roleItem.Role
        $services = ''
        $ports = ''
        $locations = ''
        $exportCategory = ''
        $considerations = ''
        $restore = ''
        $verify = ''

        switch ($role) {
            'Active Directory Domain Services' {
                $services='NTDS, Netlogon, KDC, DFSR'; $ports='53, 88, 135, 389, 445, 464, 636, 3268/3269 plus dynamic RPC'; $locations='%SystemRoot%\\NTDS; SYSVOL; AD-integrated DNS'; $exportCategory='ActiveDirectory';
                $considerations='Treat System State and forest/domain recovery procedures as authoritative. Preserve FSMO, sites, replication, DNS, time, and SYSVOL dependencies.'
                $restore='Use supported AD DS System State/forest recovery procedures. Inventory data is reference material, not an AD database backup.'
                $verify='DCDIAG, REPADMIN, SYSVOL/NETLOGON, DNS registration, FSMO roles, time hierarchy, authentication, replication, and event logs.'
            }
            'DNS Server' {
                $services='DNS'; $ports='53 TCP/UDP'; $locations='%SystemRoot%\\System32\\DNS for file-backed zones; AD for AD-integrated zones'; $exportCategory='DNS';
                $considerations='Determine whether each zone is AD-integrated, file-backed, secondary, stub, or conditional. Preserve forwarders, scavenging, aging, and delegation dependencies.'
                $restore='Restore using supported DNS/AD procedures. File-backed zone exports may be reusable; AD-integrated zones should normally recover through AD replication or supported AD recovery.'
                $verify='Resolve internal/external names, authoritative answers, forwarders, zone transfers where used, dynamic updates, scavenging, and service event logs.'
            }
            'DHCP Server' {
                $services='DHCPServer'; $ports='67/68 UDP; 647 TCP for DHCP failover where used'; $locations='%SystemRoot%\\System32\\dhcp'; $exportCategory='DHCP';
                $considerations='Preserve scopes, exclusions, reservations, options, filters, policies, bindings, failover relationships, authorization, and leases.'
                $restore='Install/authorize DHCP first, then use the captured Export-DhcpServer/Backup-DhcpServer artifacts through supported Import-DhcpServer or DHCP recovery procedures.'
                $verify='Service state, authorization, bindings, scope state, failover state, leases, reservations, options, relay paths, and client renewal.'
            }
            'IIS Web Server' {
                $services='W3SVC, WAS plus application-specific services'; $ports='Binding-specific, commonly 80/443'; $locations='%SystemRoot%\\System32\\inetsrv\\config; site/application content paths'; $exportCategory='IIS';
                $considerations='Configuration alone does not include content, databases, secrets, application pool credentials, or certificate private keys.'
                $restore='Install matching IIS features first. Restore AppCmd configuration only after reviewing server-specific paths and modules, then restore content, certificates, secrets, and application data separately.'
                $verify='Sites/pools started, bindings, TLS certificates, authentication, application dependencies, logs, HTTP response, and external integrations.'
            }
            'Hyper-V' {
                $services='VMMS, VMCompute and VM-specific services'; $ports='Management/replication dependent'; $locations='VM configuration, checkpoint, VHD/VHDX, paging, smart paging, and default VM/VHD paths'; $exportCategory='HyperV';
                $considerations='Inventory records configuration and paths but is not a VM backup. Preserve virtual disks, checkpoints only when supported, virtual switches, VLANs, replication, and storage dependencies.'
                $restore='Use application-aware VM backup/restore or tested Hyper-V export/import. Recreate switches and storage paths before VM import when required.'
                $verify='VM configuration version compatibility, switches/VLANs, storage paths, VM boot, guest services, application health, replication, backup, and monitoring.'
            }
            'Failover Clustering' {
                $services='ClusSvc plus clustered workload services'; $ports='Workload dependent plus cluster/RPC communication'; $locations='Cluster database and workload-specific storage/configuration'; $exportCategory='FailoverClustering';
                $considerations='Preserve quorum, nodes, networks, CSVs, resources, dependencies, cluster roles, storage, and workload-specific recovery order.'
                $restore='Use workload-specific supported cluster recovery or rebuild procedures. Do not recreate resources blindly from inventory data.'
                $verify='Cluster validation as appropriate, node membership, quorum, CSV ownership, networks, resources online, failover, and workload health.'
            }
            'Network Policy Server' {
                $services='IAS'; $ports='1812/1813 UDP and/or legacy 1645/1646; proxy destinations as configured'; $locations='%SystemRoot%\\System32\\ias'; $exportCategory='NPS';
                $considerations='Full export can include reusable RADIUS shared secrets and is therefore sensitive.'
                $restore='Only use the full NPS export when explicitly selected and stored securely. Import through supported netsh nps procedures and revalidate certificates/shared secrets.'
                $verify='Policies, RADIUS clients, shared secrets, accounting, certificate selection, authentication tests, and event logs.'
            }
            'Routing and Remote Access' {
                $services='RemoteAccess, RasMan and related services'; $ports='VPN/routing protocol dependent'; $locations='RemoteAccess/RRAS registry and routing configuration'; $exportCategory='RRAS';
                $considerations='Preserve interfaces, NAT, static routes, VPN protocols, address assignment, authentication, demand-dial, and firewall dependencies.'
                $restore='Use the captured routing/RRAS reference to reconstruct configuration through supported RRAS/RemoteAccess tools. Review hardware interface names before applying.'
                $verify='Routes, NAT, VPN authentication, address pools/DHCP relay, firewall behavior, external reachability, and event logs.'
            }
            'Print Server' {
                $services='Spooler'; $ports='Printer/protocol dependent'; $locations='%SystemRoot%\\System32\\spool; driver store; print queues/ports'; $exportCategory='PrintServices';
                $considerations='Driver architecture/version compatibility and vendor packages can be more important than queue definitions.'
                $restore='Use PrintBRM migration export when generated, then validate/install required vendor drivers and ports.'
                $verify='Queues, ports, drivers, permissions, published printers, test pages, client deployment, and spooler events.'
            }
            'Active Directory Certificate Services' {
                $services='CertSvc'; $ports='RPC/DCOM and enrollment endpoints as configured'; $locations='%SystemRoot%\\System32\\CertLog; CA registry; certificate/private-key stores'; $exportCategory='Certificates';
                $considerations='A CA database and CA private-key backup are mandatory for real recovery. Inventory intentionally does not export private keys.'
                $restore='Use supported AD CS backup/restore procedures with the protected CA database, CA certificate, and private key. Reapply web enrollment/OCSP dependencies separately.'
                $verify='CA service, CA chain, CRL/AIA publication, templates, enrollment, private-key access, issued certificate validation, and event logs.'
            }
            'Microsoft SQL Server' {
                $services='MSSQLSERVER/MSSQL$Instance, SQLSERVERAGENT/SQLAgent$Instance and related SQL services'; $ports='Instance-specific TCP/static or dynamic ports, commonly 1433; Browser commonly UDP 1434'; $locations='Default data/log/backup directories plus per-database file paths captured in SQL inventory'; $exportCategory='SQLServer';
                $considerations='Inventory is read-only. Preserve SQL-native full/differential/log backups, master/msdb dependencies, encryption certificates/keys, credentials, service accounts, and application connection dependencies.'
                $restore='Restore databases using SQL-native procedures, then recreate or validate instance-level configuration, jobs, linked servers, principals, endpoints, certificates/keys, and service accounts from approved sources.'
                $verify='SQL service/Agent, database states, DBCC/application checks as approved, recovery model, jobs, logins, linked servers, backups, ports, connectivity, and application transactions.'
            }
            'WSUS' {
                $services='WsusService, IIS and SQL/WID dependencies'; $ports='IIS binding-specific, commonly 8530/8531'; $locations='WSUS content directory and SUSDB/WID/SQL data'; $exportCategory='WindowsUpdate';
                $considerations='Decide whether to restore or rebuild WSUS. Content, SUSDB, approvals, and history are not fully represented by inventory metadata.'
                $restore='Follow the chosen WSUS restore/rebuild procedure, preserving content/database consistency when restoring.'
                $verify='Synchronization, products/classifications, upstream settings, approvals, client reporting, IIS health, and service/event logs.'
            }
            default {
                $services='See role-specific service inventory'; $ports='See listening endpoints and role documentation'; $locations='See role-specific configuration and supporting exports';
                $considerations='Review dependencies, data paths, service accounts, ports, certificates, and external integrations before migration or recovery.'
                $restore='Use vendor-supported restore/import procedures and the captured configuration as reference.'
                $verify='Validate services, ports, data, authentication, application workflow, backups, monitoring, and event logs.'
            }
        }

        $exportRequested = (-not [string]::IsNullOrWhiteSpace($exportCategory) -and (Test-ExportCategory -Category @($exportCategory)))
        $exportStatus = if ($exportRequested) { 'Selected; verify generated artifact list' } else { 'Not selected / inventory only' }
        switch ($role) {
            'Active Directory Domain Services' { $exportStatus = 'Inventory only. Supported System State / forest recovery backup required separately.' }
            'Hyper-V' { $exportStatus = 'Inventory only. VM/VHD data backup or tested export required separately.' }
            'Failover Clustering' { $exportStatus = 'Inventory only. Use workload-specific cluster backup/recovery procedures.' }
            'Active Directory Certificate Services' {
                if (Test-ExportCategory -Category @('Certificates')) {
                    $exportStatus = if (Test-Path -LiteralPath (Join-Path $paths.Security 'Public-Certificate-Exports')) { 'Public certificates captured only. Protected CA database and private-key backup remains required separately.' } else { 'Public certificate export selected but not confirmed. Protected CA database and private-key backup remains required separately.' }
                } else { $exportStatus = 'Inventory only. Protected CA database and private-key backup required separately.' }
            }
            'WSUS' { $exportStatus = 'Inventory only. Decide restore versus rebuild and protect SUSDB/content separately when required.' }
            'DHCP Server' {
                if ($exportRequested) { if (Test-Path -LiteralPath (Join-Path $paths.Roles 'DHCP\DHCP-Configuration-and-Leases.xml')) { $exportStatus='Captured: DHCP XML export generated; see artifact list for database backup.' } else { $exportStatus='Selected but reusable DHCP export was not confirmed. Review collector status/errors.' } }
            }
            'DNS Server' {
                if ($exportRequested) {
                    $dnsExportFiles = @(Get-ChildItem -LiteralPath (Join-Path $paths.Roles 'DNS') -Recurse -File -ErrorAction SilentlyContinue | Where-Object { $_.Name -like '*Zone-Export*' -or $_.Name -like 'File-Backed-Zone-*' })
                    $exportStatus = if ($dnsExportFiles.Count -gt 0) { 'Captured where supported: one or more zone export files generated.' } else { 'Selected, but no reusable zone file was confirmed. AD-integrated zones may require AD recovery instead.' }
                }
            }
            'IIS Web Server' {
                if ($exportRequested) { $exportStatus = if (Test-Path -LiteralPath (Join-Path $paths.Roles 'IIS\IIS-Configuration-Backup')) { 'Captured: IIS configuration backup generated.' } else { 'Selected but IIS configuration backup was not confirmed. Review collector status/errors.' } }
            }
            'Network Policy Server' {
                if ($exportRequested) {
                    if (-not $IncludeSensitiveRoleExports) { $exportStatus='Selected but blocked because sensitive exports were not explicitly allowed.' }
                    elseif (Test-Path -LiteralPath (Join-Path $paths.Roles 'NPS\NPS-FULL-CONFIG-SENSITIVE.xml')) { $exportStatus='Captured: sensitive NPS full configuration export generated.' }
                    else { $exportStatus='Selected but full NPS export was not confirmed. Review collector status/errors.' }
                }
            }
            'Routing and Remote Access' {
                if ($exportRequested) { $exportStatus = if (Test-Path -LiteralPath (Join-Path $paths.Roles 'RRAS\RRAS-Restore-Reference.txt')) { 'Captured: RRAS reconstruction reference generated.' } else { 'Selected but RRAS reconstruction reference was not confirmed.' } }
            }
            'Print Server' {
                if ($exportRequested) { $exportStatus = if (Test-Path -LiteralPath (Join-Path $paths.Roles ('Print-Server\' + $env:COMPUTERNAME + '.printerExport'))) { 'Captured: PrintBRM migration package generated.' } else { 'Selected but PrintBRM migration package was not confirmed.' } }
            }
            'Microsoft SQL Server' {
                if ($exportRequested) {
                    $sqlRegistryExports = @(Get-ChildItem -LiteralPath (Join-Path $paths.Roles 'Microsoft-SQL-Server') -Filter '*.reg' -File -ErrorAction SilentlyContinue)
                    $exportStatus = if ($sqlRegistryExports.Count -gt 0) { 'Captured: SQL registry/configuration reference generated. Database contents are not exported.' } else { 'Selected; SQL metadata collected, but registry export was not confirmed. Database contents are never exported by this module.' }
                } else { $exportStatus='Read-only SQL metadata only. SQL-native database backup media required separately.' }
            }
        }
        [void]$rows.Add([pscustomobject]@{
            Role = $role
            Services = $services
            Ports = $ports
            ImportantPathsAndData = $locations
            BackupExportStatus = $exportStatus
            RebuildMigrationConsiderations = $considerations
            RestoreImportGuidance = $restore
            PostRestoreVerification = $verify
        })
    }
    return @($rows)
}

function ConvertTo-ReportAtomicText {
    param(
        [Parameter()]
        [AllowNull()]
        [object]$Value
    )

    if ($null -eq $Value) {
        return ''
    }

    if ($Value -is [string]) {
        return [string]$Value
    }
    if ($Value -is [DateTime]) {
        return $Value.ToString('yyyy-MM-dd HH:mm:ss')
    }
    if ($Value -is [DateTimeOffset]) {
        return $Value.ToString('yyyy-MM-dd HH:mm:ss zzz')
    }
    if ($Value -is [TimeSpan]) {
        return $Value.ToString()
    }
    if ($Value -is [Guid]) {
        return $Value.ToString()
    }
    if ($Value -is [Uri]) {
        return $Value.ToString()
    }
    if ($Value -is [Version]) {
        return $Value.ToString()
    }
    if ($Value -is [System.Security.Principal.SecurityIdentifier]) {
        return $Value.Value
    }
    if ($Value -is [System.Net.IPAddress]) {
        return $Value.ToString()
    }
    if ($Value -is [System.Net.NetworkInformation.PhysicalAddress]) {
        return $Value.ToString()
    }
    if ($Value -is [System.IO.FileSystemInfo]) {
        return $Value.FullName
    }
    if ($Value -is [System.Security.Cryptography.Oid]) {
        if (-not [string]::IsNullOrWhiteSpace([string]$Value.FriendlyName)) {
            return ([string]$Value.FriendlyName + ' (' + [string]$Value.Value + ')')
        }
        return [string]$Value.Value
    }
    if ($Value -is [System.Security.SecureString]) {
        return '[secure value omitted]'
    }
    if ($Value -is [System.Collections.DictionaryEntry]) {
        $entryKey = ''
        $entryValue = ''
        try { $entryKey = [string]$Value.Key } catch { $entryKey = '[unavailable key]' }
        try { $entryValue = [string]$Value.Value } catch { $entryValue = '[unavailable value]' }
        return ($entryKey + '=' + $entryValue)
    }

    $valueType = $Value.GetType()
    if ($valueType.IsPrimitive -or $valueType.IsEnum -or ($Value -is [decimal])) {
        try {
            return [System.Convert]::ToString($Value, [System.Globalization.CultureInfo]::InvariantCulture)
        }
        catch {
            return [string]$Value
        }
    }

    # Complex values are deliberately converted without recursively walking their
    # members. Some CIM and deserialized objects contain circular references, which
    # can exceed the call-depth limit in Windows PowerShell 3.0 and 4.0.
    foreach ($preferredPropertyName in @('DisplayName', 'FriendlyName', 'Name', 'Value', 'Path', 'Id', 'IPAddress', 'Address', 'DistinguishedName')) {
        try {
            $preferredProperty = $Value.PSObject.Properties[$preferredPropertyName]
            if ($preferredProperty) {
                $preferredValue = $preferredProperty.Value
                if (($null -ne $preferredValue) -and (-not [object]::ReferenceEquals($preferredValue, $Value)) -and (-not (($preferredValue -is [System.Collections.IEnumerable]) -and -not ($preferredValue -is [string])))) {
                    $preferredText = [string]$preferredValue
                    if (-not [string]::IsNullOrWhiteSpace($preferredText)) {
                        return $preferredText
                    }
                }
            }
        }
        catch { }
    }

    # Do not call an unknown object's custom ToString implementation here. A
    # vendor type can traverse parent or child references internally and recreate
    # the same stack-overflow condition this function is intended to prevent.
    try {
        return ('[' + $valueType.FullName + '; full value retained in supporting export]')
    }
    catch {
        return '[complex value retained in supporting export]'
    }
}

function ConvertTo-ReportText {
    param(
        [Parameter()]
        [AllowNull()]
        [object]$Value,

        [Parameter()]
        [int]$MaxLength = 2000
    )

    if ($null -eq $Value) {
        return ''
    }

    $text = ''
    if (($Value -is [System.Collections.IEnumerable]) -and -not ($Value -is [string])) {
        $parts = New-Object System.Collections.ArrayList
        $itemCount = 0
        $maximumCollectionItems = 128
        $estimatedLength = 0

        try {
            foreach ($item in $Value) {
                if ($itemCount -ge $maximumCollectionItems) {
                    [void]$parts.Add('[additional values omitted from consolidated report]')
                    break
                }

                $itemText = ''
                if ($null -ne $item) {
                    if ([object]::ReferenceEquals($item, $Value)) {
                        $itemText = '[self-reference omitted]'
                    }
                    elseif (($item -is [System.Collections.IEnumerable]) -and -not ($item -is [string])) {
                        # Do not recursively enumerate nested collections. Recursive
                        # collection conversion caused the report-generation failure
                        # on Windows PowerShell 4.0.
                        $itemTypeName = 'collection'
                        try { $itemTypeName = $item.GetType().FullName } catch { }
                        $itemText = '[' + $itemTypeName + '; nested values retained in supporting export]'
                    }
                    else {
                        $itemText = ConvertTo-ReportAtomicText -Value $item
                    }
                }

                if (-not [string]::IsNullOrWhiteSpace($itemText)) {
                    [void]$parts.Add($itemText)
                    $estimatedLength += $itemText.Length + 2
                }

                $itemCount++
                if (($MaxLength -gt 0) -and ($estimatedLength -gt ($MaxLength + 500))) {
                    [void]$parts.Add('[remaining values omitted from consolidated report]')
                    break
                }
            }
        }
        catch {
            [void]$parts.Add('[collection could not be fully rendered: ' + $_.Exception.Message + ']')
        }

        $text = $parts -join '; '
    }
    else {
        $text = ConvertTo-ReportAtomicText -Value $Value
    }

    if ($null -eq $text) {
        $text = ''
    }
    else {
        $text = [string]$text
    }

    $text = $text -replace '[\x00-\x08\x0B\x0C\x0E-\x1F]', ''
    $text = $text -replace '\s*[\r\n]+\s*', ' | '
    $text = $text.Trim()

    if (($MaxLength -gt 0) -and ($text.Length -gt $MaxLength)) {
        $suffix = ' [truncated in consolidated report; full value is retained in supporting exports]'
        $usableLength = [Math]::Max(1, $MaxLength - $suffix.Length)
        $text = $text.Substring(0, $usableLength) + $suffix
    }

    return $text
}


function ConvertTo-ReportMultilineText {
    param(
        [Parameter()]
        [AllowNull()]
        [object]$Value,

        [Parameter()]
        [int]$MaxLength = 50000
    )

    if ($null -eq $Value) {
        return ''
    }

    if ($Value -is [string]) {
        $text = [string]$Value
    }
    else {
        # Avoid invoking custom ToString implementations on unknown complex types.
        $text = ConvertTo-ReportText -Value $Value -MaxLength $MaxLength
    }
    $text = $text -replace '[\x00-\x08\x0B\x0C\x0E-\x1F]', ''
    $text = $text -replace '\r\n|\r', "`n"
    $text = $text.Trim()

    if (($MaxLength -gt 0) -and ($text.Length -gt $MaxLength)) {
        $suffix = "`n[truncated in consolidated report; complete text is retained in the supporting export]"
        $usableLength = [Math]::Max(1, $MaxLength - $suffix.Length)
        $text = $text.Substring(0, $usableLength) + $suffix
    }

    return $text
}

function ConvertTo-ReportByteSize {
    param(
        [Parameter()]
        [AllowNull()]
        [object]$Value
    )

    if ($null -eq $Value) {
        return ''
    }

    $text = ([string]$Value).Trim()
    if ([string]::IsNullOrWhiteSpace($text)) {
        return ''
    }

    $numericValue = 0.0
    $parsed = [double]::TryParse(
        $text,
        [System.Globalization.NumberStyles]::Any,
        [System.Globalization.CultureInfo]::InvariantCulture,
        [ref]$numericValue
    )
    if (-not $parsed) {
        $parsed = [double]::TryParse($text, [ref]$numericValue)
    }
    if (-not $parsed) {
        return $text
    }

    $kilobyte = 1024.0
    $megabyte = 1048576.0
    $gigabyte = 1073741824.0
    $terabyte = 1099511627776.0
    $petabyte = 1125899906842624.0
    $absoluteValue = [Math]::Abs($numericValue)
    if ($absoluteValue -ge $petabyte) {
        return ('{0:N2} PB' -f ($numericValue / $petabyte))
    }
    if ($absoluteValue -ge $terabyte) {
        return ('{0:N2} TB' -f ($numericValue / $terabyte))
    }
    if ($absoluteValue -ge $gigabyte) {
        return ('{0:N2} GB' -f ($numericValue / $gigabyte))
    }
    if ($absoluteValue -ge $megabyte) {
        return ('{0:N2} MB' -f ($numericValue / $megabyte))
    }
    if ($absoluteValue -ge $kilobyte) {
        return ('{0:N2} KB' -f ($numericValue / $kilobyte))
    }

    return ('{0:N0} bytes' -f $numericValue)
}

function Get-ReportPropertyValue {
    param(
        [Parameter()]
        [AllowNull()]
        [object]$InputObject,

        [Parameter(Mandatory = $true)]
        [string]$PropertyName
    )

    if ($null -eq $InputObject) {
        return $null
    }

    $property = $InputObject.PSObject.Properties[$PropertyName]
    if ($property) {
        # Emit the property as one object, not as a PowerShell pipeline collection.
        # Enumerating here loses collection identity before ConvertTo-ReportText
        # can apply its reference/nesting guards. In particular, the startup table
        # self-test passes a cyclic ArrayList through this helper on Windows 5.1.
        # A unary-comma envelope is unwrapped once by the pipeline; the original
        # value (including an empty or cyclic collection) remains intact.
        return ,$property.Value
    }

    return $null
}

function Get-ReportCellText {
    param(
        [Parameter()]
        [AllowNull()]
        [object]$InputObject,

        [Parameter(Mandatory = $true)]
        [string]$PropertyName,

        [Parameter()]
        [int]$MaxLength = 2500
    )

    try {
        $cellValue = Get-ReportPropertyValue -InputObject $InputObject -PropertyName $PropertyName
        return ConvertTo-ReportText -Value $cellValue -MaxLength $MaxLength
    }
    catch {
        $message = $_.Exception.Message
        if ([string]::IsNullOrWhiteSpace($message)) {
            $message = 'unknown conversion error'
        }
        return ('[value could not be rendered in consolidated report: ' + $message + ']')
    }
}

function Get-ReportData {
    param(
        [Parameter(Mandatory = $true)]
        [string]$BasePath
    )

    # The consolidated report deliberately prefers CSV. CSV contains only flat
    # strings and therefore cannot carry circular CIM/deserialized object graphs
    # into the report engine. The original CLIXML files remain in the package as
    # supporting evidence when exact typed values are needed.
    $csvPath = $BasePath + '.csv'
    if (Test-Path -LiteralPath $csvPath -PathType Leaf) {
        try {
            return @(Import-Csv -LiteralPath $csvPath -ErrorAction Stop)
        }
        catch { }
    }

    # Some collectors may only have CLIXML. When that happens, flatten every
    # property immediately into bounded text. No complex object is returned to
    # the consolidated report layer. This avoids call-depth overflow on Windows
    # PowerShell 3.0/4.0 caused by circular or deeply nested deserialized values.
    $clixmlPath = $BasePath + '.clixml'
    if (Test-Path -LiteralPath $clixmlPath -PathType Leaf) {
        try {
            $rawRows = @(Import-Clixml -LiteralPath $clixmlPath -ErrorAction Stop)
            $flatRows = New-Object System.Collections.ArrayList

            foreach ($rawRow in $rawRows) {
                if ($null -eq $rawRow) {
                    continue
                }

                $flat = [ordered]@{}
                try {
                    foreach ($property in @($rawRow.PSObject.Properties)) {
                        if ($null -eq $property) {
                            continue
                        }

                        $propertyName = [string]$property.Name
                        if ([string]::IsNullOrWhiteSpace($propertyName)) {
                            continue
                        }

                        try {
                            $flat[$propertyName] = ConvertTo-ReportText -Value $property.Value -MaxLength 5000
                        }
                        catch {
                            $flat[$propertyName] = '[value retained in supporting CLIXML export]'
                        }
                    }
                }
                catch {
                    $flat['Value'] = '[complex row retained in supporting CLIXML export]'
                }

                if ($flat.Count -eq 0) {
                    $flat['Value'] = ConvertTo-ReportAtomicText -Value $rawRow
                }

                [void]$flatRows.Add([pscustomobject]$flat)
            }

            return @($flatRows)
        }
        catch { }
    }

    return @()
}

function Get-ReportFileText {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter()]
        [int]$MaxLength = 50000
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return ''
    }

    try {
        $text = Get-Content -LiteralPath $Path -Raw -ErrorAction Stop
        return ConvertTo-ReportMultilineText -Value $text -MaxLength $MaxLength
    }
    catch {
        return ''
    }
}

function Test-ReportTrue {
    param(
        [Parameter()]
        [AllowNull()]
        [object]$Value
    )

    if ($Value -is [bool]) {
        return $Value
    }

    $text = ([string]$Value).Trim()
    return ($text -match '^(True|Yes|1|Enabled)$')
}

function Add-ReportTitle {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Text,

        [Parameter()]
        [string]$Subtitle = ''
    )

    [void]$script:reportBlocks.Add([pscustomobject]@{
        Type     = 'Title'
        Text     = $Text
        Subtitle = $Subtitle
    })
}

function Add-ReportHeading {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Text,

        [Parameter()]
        [ValidateRange(1, 3)]
        [int]$Level = 1,

        [Parameter()]
        [switch]$PageBreakBefore
    )

    [void]$script:reportBlocks.Add([pscustomobject]@{
        Type            = 'Heading'
        Text            = $Text
        Level           = $Level
        PageBreakBefore = [bool]$PageBreakBefore
    })
}

function Add-ReportParagraph {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$Text,

        [Parameter()]
        [switch]$Bold,

        [Parameter()]
        [switch]$Warning
    )

    [void]$script:reportBlocks.Add([pscustomobject]@{
        Type    = 'Paragraph'
        Text    = $Text
        Bold    = [bool]$Bold
        Warning = [bool]$Warning
    })
}

function Add-ReportBullets {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [string[]]$Items
    )

    [void]$script:reportBlocks.Add([pscustomobject]@{
        Type  = 'Bullets'
        Items = @($Items)
    })
}

function Add-ReportPreformatted {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Title,

        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$Text,

        [Parameter()]
        [int]$MaxLength = 50000
    )

    [void]$script:reportBlocks.Add([pscustomobject]@{
        Type      = 'Preformatted'
        Title     = $Title
        Text      = ConvertTo-ReportMultilineText -Value $Text -MaxLength $MaxLength
        MaxLength = $MaxLength
    })
}

function Add-ReportTable {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Title,

        [Parameter()]
        [AllowEmptyCollection()]
        [object[]]$Rows = @(),

        [Parameter(Mandatory = $true)]
        [string[]]$Columns,

        [Parameter()]
        [hashtable]$Labels = @{},

        [Parameter()]
        [int]$MaxRows = 5000,

        [Parameter()]
        [string]$EmptyMessage = 'No data was collected or the component is not present.'
    )

    # Snapshot table data into flat strings at the time the block is created.
    # This prevents CIM, WMI, deserialized, or vendor objects from being retained
    # inside $reportBlocks and later traversed by HTML/DOCX rendering.
    $safeRows = New-Object System.Collections.ArrayList
    $totalRows = 0

    foreach ($row in $Rows) {
        if ($null -eq $row) {
            continue
        }

        $totalRows++
        if (($MaxRows -gt 0) -and ($safeRows.Count -ge $MaxRows)) {
            continue
        }

        $flatRow = [ordered]@{}
        foreach ($column in $Columns) {
            try {
                $flatRow[[string]$column] = Get-ReportCellText -InputObject $row -PropertyName ([string]$column) -MaxLength 2500
            }
            catch {
                $flatRow[[string]$column] = '[value could not be rendered; see supporting export]'
            }
        }

        [void]$safeRows.Add([pscustomobject]$flatRow)
    }

    [void]$script:reportBlocks.Add([pscustomobject]@{
        Type         = 'Table'
        Title        = $Title
        Rows         = @($safeRows)
        Columns      = @($Columns)
        Labels       = $Labels
        TotalRows    = $totalRows
        DisplayRows  = $safeRows.Count
        EmptyMessage = $EmptyMessage
    })
}

function ConvertTo-HtmlText {
    param(
        [Parameter()]
        [AllowNull()]
        [object]$Value
    )

    $text = ConvertTo-ReportText -Value $Value -MaxLength 0
    try {
        return [System.Net.WebUtility]::HtmlEncode($text)
    }
    catch {
        return [System.Security.SecurityElement]::Escape($text)
    }
}


function ConvertTo-HtmlMultilineText {
    param(
        [Parameter()]
        [AllowNull()]
        [object]$Value
    )

    $text = ConvertTo-ReportMultilineText -Value $Value -MaxLength 0
    try {
        return [System.Net.WebUtility]::HtmlEncode($text)
    }
    catch {
        return [System.Security.SecurityElement]::Escape($text)
    }
}

function Write-ConsolidatedHtmlReport {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][System.Collections.IEnumerable]$Blocks,
        [Parameter(Mandatory = $true)][string]$DocumentTitle
    )

    # Presentation only. Keep every original block, row, column and warning.
    # The standalone collector embeds the stylesheet and script at build time.
    $identity = @{}
    $metrics = @{}
    $anchors = @{}
    $entries = New-Object System.Collections.ArrayList
    $currentEntry = [pscustomobject]@{ Id='section-overview'; Title='About this report'; Links=(New-Object System.Collections.ArrayList) }
    [void]$entries.Add($currentEntry)
    $sectionNumber = 0
    $subsectionNumber = 0
    $summaryId = 'section-overview'
    $subtitle = ''
    $blockList = @($Blocks)
    for ($index = 0; $index -lt $blockList.Count; $index++) {
        $block = $blockList[$index]
        if ([string]$block.Type -eq 'Title') { $subtitle = [string]$block.Subtitle }
        if (([string]$block.Type -eq 'Heading') -and ([int]$block.Level -eq 1)) {
            $sectionNumber++
            $subsectionNumber = 0
            $currentEntry = [pscustomobject]@{ Id=('section-' + $sectionNumber); Title=([string]$block.Text); Links=(New-Object System.Collections.ArrayList) }
            [void]$entries.Add($currentEntry)
            if ([string]$block.Text -match 'Executive Summary') { $summaryId = $currentEntry.Id }
        }
        elseif ((([string]$block.Type -eq 'Heading') -and ([int]$block.Level -gt 1)) -or ([string]$block.Type -eq 'Table') -or ([string]$block.Type -eq 'Preformatted')) {
            $subsectionNumber++
            $anchor = 'section-' + $sectionNumber + '-sub-' + $subsectionNumber
            $anchors[$index] = $anchor
            $label = ''
            if ([string]$block.Type -eq 'Heading') { $label = [string]$block.Text }
            else { $label = [string]$block.Title }
            [void]$currentEntry.Links.Add([pscustomobject]@{ Id=$anchor; Title=$label })
        }
        if ([string]$block.Type -eq 'Table') {
            if ([string]$block.Title -eq 'Server identity and baseline') {
                foreach ($row in @($block.Rows)) { $identity[[string]$row.Field] = [string]$row.Value }
            }
            if ([string]$block.Title -eq 'Inventory at a glance') {
                foreach ($row in @($block.Rows)) { $metrics[[string]$row.Area] = [string]$row.Count }
            }
        }
    }
    $serverName = [string]$identity['Computer name']
    if ([string]::IsNullOrWhiteSpace($serverName)) { $serverName = $DocumentTitle }
    $builder = New-Object System.Text.StringBuilder

    function Get-CleanReportTitle {
        param([string]$Text)
        return ($Text -replace '^\d+(\.\d+)*\.?\s+', '')
    }
    function Write-CleanReportSection {
        param([string]$Id, [string]$Title)
        $intro = 'Captured settings and detailed inventory.'
        if ($Id -eq $summaryId) { $intro = 'A quick view of this server and the items that need review.' }
        elseif ($Title -match 'Network') { $intro = 'IP addressing, routing, adapters, and listening services.' }
        elseif ($Title -match 'Storage') { $intro = 'Disks, volumes, paths, and storage configuration.' }
        elseif ($Title -match 'Recovery|Rebuild|Migration') { $intro = 'Review the requirements before planning migration or recovery.' }
        elseif ($Title -match 'Collection Appendix') { $intro = 'Collection results and supporting export references.' }
        [void]$builder.AppendLine('<section id="' + $Id + '" class="report-section">')
        [void]$builder.AppendLine('<h2 class="section-title">' + (ConvertTo-HtmlText (Get-CleanReportTitle $Title)) + '</h2>')
        [void]$builder.AppendLine('<p class="section-intro">' + $intro + '</p>')
        if ($Id -eq $summaryId) {
            [void]$builder.AppendLine('<div class="facts">')
            foreach ($name in @('Operating system','Domain or workgroup','Physical or virtual','Collected')) {
                $value = [string]$identity[$name]
                if ([string]::IsNullOrWhiteSpace($value)) { $value = 'Not collected' }
                [void]$builder.AppendLine('<div><span class="fact-label">' + (ConvertTo-HtmlText $name) + '</span><span class="fact-value">' + (ConvertTo-HtmlText $value) + '</span></div>')
            }
            [void]$builder.AppendLine('</div><div class="metrics">')
            foreach ($metric in @(
                @{Key='Collectors completed successfully'; Label='Collectors completed'},
                @{Key='Collectors failed or unavailable'; Label='Collectors requiring review'},
                @{Key='Detected critical roles and platforms'; Label='Critical roles and platforms'},
                @{Key='Installed applications'; Label='Installed applications'}
            )) {
                $value = [string]$metrics[$metric.Key]
                if ($value -notmatch '^\d+$') { $value = 'Not collected' }
                $class = 'metric'
                if (($metric.Key -eq 'Collectors failed or unavailable') -and ($value -match '^[1-9]')) { $class += ' metric-review' }
                [void]$builder.AppendLine('<div class="' + $class + '"><span class="metric-value">' + (ConvertTo-HtmlText $value) + '</span><span class="metric-label">' + $metric.Label + '</span></div>')
            }
            [void]$builder.AppendLine('</div><div class="review-links">')
            for ($reviewIndex=0; $reviewIndex -lt $blockList.Count; $reviewIndex++) {
                $review = $blockList[$reviewIndex]
                if (([string]$review.Type -eq 'Table') -and ([int]$review.DisplayRows -gt 0) -and ([string]$review.Title -match '^(Collection failures requiring review|Recovery prerequisites and gaps)$')) {
                    $label = 'collection issues'
                    if ([string]$review.Title -eq 'Recovery prerequisites and gaps') { $label = 'recovery prerequisites or gaps' }
                    if ([int]$review.DisplayRows -eq 1) {
                        $label = 'collection issue'
                        if ([string]$review.Title -eq 'Recovery prerequisites and gaps') { $label = 'recovery prerequisite or gap' }
                    }
                    [void]$builder.AppendLine('<button type="button" class="review-link" data-anchor="' + $anchors[$reviewIndex] + '">' + $review.DisplayRows + ' ' + $label + '</button>')
                }
            }
            [void]$builder.AppendLine('</div>')
        }
        [void]$builder.AppendLine('<div class="section-actions"><button type="button" class="quiet-button" data-details-action="expand">Expand section</button><button type="button" class="quiet-button" data-details-action="collapse">Collapse section</button></div>')
    }

    [void]$builder.AppendLine('<!DOCTYPE html><html lang="en" data-report-layout="clean-v1" data-default-section="' + $summaryId + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">')
    [void]$builder.AppendLine('<title>' + (ConvertTo-HtmlText $DocumentTitle) + '</title>')
    $styleBlock = @'
<style>
@page { size: landscape; margin: .5in; }
:root { color-scheme: light; --ink:#182b43; --muted:#617084; --line:#e1e7ee; --blue:#225bc5; --surface:#fff; }
* { box-sizing:border-box; }
html,body { height:100%; margin:0; overflow:hidden; }
body { font:14px/1.55 "Segoe UI",Arial,sans-serif; color:var(--ink); background:#f4f6f9; }
button,input { font:inherit; }
button { cursor:pointer; }
button:focus-visible,input:focus-visible,summary:focus-visible,a:focus-visible { outline:3px solid #85afea; outline-offset:3px; }
button:disabled { cursor:default; opacity:.45; }
.report-app { height:100%; min-height:0; display:flex; flex-direction:column; }
.cover { flex:none; display:flex; align-items:center; gap:24px; padding:22px 30px; background:var(--surface); border-bottom:1px solid var(--line); }
.report-brand { flex:1; min-width:0; }
.eyebrow { color:var(--blue); font-size:11px; font-weight:700; letter-spacing:.12em; text-transform:uppercase; }
h1 { margin:3px 0; font-size:25px; line-height:1.25; letter-spacing:-.025em; overflow-wrap:anywhere; }
.subtitle { margin:5px 0 0; color:var(--muted); font-size:12px; overflow-wrap:anywhere; }
.standalone-tools { display:flex; flex-direction:column; align-items:flex-end; gap:5px; max-width:460px; }
.report-search { display:flex; align-items:center; gap:7px; }
.report-search input { min-width:100px; width:220px; padding:8px 11px; border:1px solid #ccd5e0; border-radius:7px; background:#fff; color:var(--ink); }
.quiet-button,.pager-button,.nav-toggle { border:1px solid #d3dce7; background:#fff; color:#34465d; border-radius:7px; padding:7px 11px; white-space:nowrap; }
.quiet-button:hover,.pager-button:hover:not(:disabled),.nav-toggle:hover { background:#f1f5fc; border-color:#a5bde3; }
.search-controls { display:none; gap:6px; align-items:center; }
.search-message { display:none; font-size:11px; color:var(--muted); min-height:17px; }
.search-active .search-controls { display:flex; }
.search-active .search-message { display:block; }
.nav-toggle { display:none; }
.report-layout { flex:1; min-height:0; display:grid; grid-template-columns:246px minmax(0,1fr); overflow:hidden; }
.report-nav { overflow:auto; min-height:0; background:#fafbfd; border-right:1px solid var(--line); padding:24px 14px; }
.nav-heading { color:#65758c; font-size:10px; font-weight:700; text-transform:uppercase; letter-spacing:.14em; margin:0 12px 12px; }
.nav-group { margin:3px 0; }
.section-nav-button { width:100%; display:flex; gap:10px; align-items:baseline; border:0; border-radius:7px; padding:9px 12px; text-align:left; color:#526176; background:transparent; font-size:12px; line-height:1.4; }
.section-nav-button:hover { color:var(--ink); background:#eef2f7; }
.section-nav-button.active { color:#184caa; background:#e9f0fd; font-weight:650; }
.nav-number { flex:0 0 19px; color:#95a1af; font-size:10px; font-variant-numeric:tabular-nums; }
.active .nav-number { color:#4772b9; }
.subsection-list { display:none; margin:7px 9px 12px 40px; border-left:1px solid #d7e2f3; padding-left:8px; }
.nav-group.active .subsection-list { display:block; }
.subsection-nav-button { width:100%; border:0; background:transparent; color:#65758c; font-size:11px; line-height:1.4; padding:5px 7px; text-align:left; border-radius:4px; }
.subsection-nav-button:hover,.subsection-nav-button.active { color:var(--blue); background:#eef3fc; }
.nav-reference { border-top:1px solid var(--line); margin-top:16px; padding-top:12px; }
.report-content { min-width:0; min-height:0; overflow:auto; padding:26px 34px 36px; scroll-behavior:smooth; }
.reading-width { max-width:1320px; margin:0 auto; }
.section-pager { display:flex; align-items:center; gap:8px; margin-bottom:18px; }
.pager-label { flex:1; color:var(--muted); font-size:11px; }
.pager-button { padding:5px 10px; font-size:11px; }
.report-section { min-width:0; }
.report-js .report-section { display:none; }
.report-js .report-section.active { display:block; }
.section-title { margin:0 0 5px; font-size:25px; line-height:1.3; letter-spacing:-.025em; font-weight:650; }
.section-intro { color:var(--muted); font-size:12px; margin:0 0 22px; max-width:780px; }
.section-actions { display:flex; flex-wrap:wrap; gap:7px; margin:0 0 18px; }
.section-actions .quiet-button { font-size:11px; padding:5px 9px; }
.facts { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:18px; padding:19px 22px; background:#fff; border:1px solid var(--line); border-radius:10px; margin:18px 0; }
.fact-label { display:block; margin-bottom:5px; color:#65758c; font-size:10px; font-weight:650; text-transform:uppercase; letter-spacing:.07em; }
.fact-value { display:block; font-size:12px; font-weight:600; overflow-wrap:anywhere; }
.metrics { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:12px; margin:18px 0; }
.metric { border:1px solid var(--line); border-radius:10px; padding:16px 18px; background:#fff; }
.metric-value { display:block; font-size:28px; line-height:1.15; font-weight:650; letter-spacing:-.035em; margin-bottom:7px; }
.metric-label { font-size:11px; color:var(--muted); }
.metric-review .metric-value { color:#ab620d; }
.review-links { display:flex; flex-wrap:wrap; gap:8px; margin:18px 0 22px; }
.review-links:empty { display:none; }
.review-link { border:1px solid #e9d6b9; color:#875514; background:#fff8ec; padding:7px 11px; border-radius:7px; font-size:12px; }
.subsection-anchor { scroll-margin-top:16px; }
h3 { font-size:17px; font-weight:650; margin:28px 0 12px; letter-spacing:-.01em; }
h4 { font-size:14px; margin:20px 0 10px; }
p { margin:12px 0; overflow-wrap:anywhere; }
ul { padding-left:22px; }
li { margin:7px 0; }
.notice,.info { padding:12px 16px; border-left:3px solid #d69d43; border-radius:0 7px 7px 0; background:#fff8ec; font-size:12px; }
.info { border-color:#7197d1; background:#eef4fc; }
.dataset { background:#fff; border:1px solid var(--line); border-radius:9px; margin:10px 0; scroll-margin-top:16px; }
.dataset>summary { display:flex; align-items:center; gap:12px; list-style:none; padding:15px 18px; cursor:pointer; font-size:13px; font-weight:600; }
.dataset>summary::-webkit-details-marker { display:none; }
.dataset>summary::before { content:""; flex:0 0 6px; width:6px; height:6px; border-right:1.5px solid #7b899b; border-bottom:1.5px solid #7b899b; transform:rotate(-45deg); margin-right:2px; }
.dataset[open]>summary::before { transform:rotate(45deg); margin-top:-3px; }
.dataset[open]>summary { border-bottom:1px solid var(--line); }
.dataset-title { flex:1; min-width:0; overflow-wrap:anywhere; }
.dataset-count { color:#65758c; font-size:11px; font-weight:400; white-space:nowrap; }
.dataset-priority { border-color:#e8d5b3; }
.dataset-priority>summary { background:#fffbf3; border-radius:8px 8px 0 0; }
.dataset-body { padding:0 18px 16px; }
.empty-data { color:#65758c; font-size:12px; padding-top:10px; }
.table-wrap { overflow:auto; margin:0 -18px; }
.dataset-long .table-wrap { max-height:60vh; }
table { width:100%; border-collapse:separate; border-spacing:0; font-size:12px; text-align:left; }
th { position:sticky; top:0; z-index:1; background:#f4f7fb; color:#617087; font-size:10px; font-weight:650; padding:11px 14px; border-bottom:1px solid var(--line); vertical-align:top; }
td { padding:10px 14px; border-bottom:1px solid #edf0f4; vertical-align:top; overflow-wrap:anywhere; min-width:110px; max-width:640px; }
tbody tr:last-child td { border-bottom:0; }
tbody tr:hover td { background:#f8faff; }
.identity-table td:first-child { width:30%; color:#718097; }
.table-note { font-size:11px; color:#8b682f; background:#fff8eb; padding:8px 10px; margin:12px 0 0; border-radius:5px; }
.status { display:inline-block; border-radius:4px; padding:2px 7px; font-size:10px; font-weight:600; background:#edf1f7; color:#5b6c82; }
.status-success { background:#eaf6f0; color:#257452; }
.status-warning { background:#fff1d8; color:#976018; }
.status-error { background:#fceceb; color:#aa4744; }
pre { white-space:pre-wrap; overflow-wrap:anywhere; font:11px/1.65 Consolas,"Courier New",monospace; color:#3e526b; padding:16px; border-radius:6px; background:#f5f7fa; margin:15px 0 0; }
.footer-note { max-width:1320px; margin:26px auto 0; padding-top:14px; border-top:1px solid var(--line); color:#65758c; font-size:11px; }
mark.report-match { background:#fff1b6; color:inherit; padding:0; }
mark.report-current { background:#ffd570; outline:2px solid #d0952a; }
.embedded-report .standalone-tools { display:none; }
.embedded-report .cover { padding:15px 24px; }
.embedded-report h1 { font-size:21px; }
.embedded-report .report-content { padding:22px 28px 30px; }
html:not(.report-js) .report-nav,html:not(.report-js) .section-pager,html:not(.report-js) .section-actions,html:not(.report-js) .standalone-tools { display:none; }
html:not(.report-js) .report-layout { grid-template-columns:minmax(0,1fr); }
@media(max-width:1050px) {
 .cover { padding:18px 22px; gap:16px; }
 .report-search input { width:160px; }
 .report-layout { grid-template-columns:215px minmax(0,1fr); }
 .report-content { padding:22px; }
 .facts { grid-template-columns:repeat(2,minmax(0,1fr)); }
 .metric { padding:14px; }
}
@media(max-width:760px) {
 .cover,.embedded-report .cover { flex-wrap:wrap; padding:14px 16px; gap:12px; }
 h1,.embedded-report h1 { font-size:20px; }
 .nav-toggle { display:block; min-height:40px; }
 .standalone-tools { flex-basis:100%; align-items:stretch; max-width:none; }
 .report-search { flex-wrap:wrap; }
 .report-search input { flex:1; width:100px; }
 .search-controls { flex-wrap:wrap; }
 .report-layout { grid-template-columns:minmax(0,1fr); grid-template-rows:auto minmax(0,1fr); }
 .report-nav { display:none; grid-row:1; border-right:0; border-bottom:1px solid var(--line); max-height:38vh; padding:14px; }
 .nav-open .report-nav { display:block; }
 .report-content,.embedded-report .report-content { grid-row:2; padding:18px 16px 28px; }
 .section-title { font-size:22px; }
 .metrics { grid-template-columns:repeat(2,minmax(0,1fr)); }
 .metric-value { font-size:25px; }
 .section-nav-button { min-height:40px; }
 .dataset>summary { padding:13px 14px; }
 .dataset-count { white-space:normal; text-align:right; max-width:100px; }
 .dataset-body { padding:0 14px 14px; }
 .table-wrap { margin:0 -14px; }
}
@media(prefers-reduced-motion:reduce) { .report-content { scroll-behavior:auto; } }
@media print {
 html,body { height:auto!important; overflow:visible!important; background:#fff; }
 .report-app,.report-layout,.report-content,.reading-width { display:block!important; height:auto!important; max-height:none!important; overflow:visible!important; padding:0!important; }
 .cover { padding:0 0 20px!important; margin-bottom:18px; }
 .report-nav,.section-pager,.section-actions,.standalone-tools,.nav-toggle,.review-links { display:none!important; }
 .report-js .report-section,.report-section { display:block!important; margin:0 0 30px; break-before:page; }
 .reading-width>.report-section:first-of-type { break-before:auto; }
 .dataset,.facts,.metric { box-shadow:none; }
 .dataset { border-radius:0; border-color:#ccc; }
 .dataset>summary { cursor:default; }
 .dataset>summary::before { display:none; }
 .dataset-body,.table-wrap { display:block!important; overflow:visible!important; max-height:none!important; }
 th { position:static; }
 thead { display:table-header-group; }
 tr { break-inside:avoid; }
 td { min-width:0; font-size:9px; padding:6px 8px; }
 th { font-size:9px; padding:7px 8px; }
 .notice,.info { break-inside:avoid; }
 mark { background:none!important; outline:0!important; }
}
</style>
'@
    [void]$builder.AppendLine($styleBlock)
    [void]$builder.AppendLine('</head><body><div class="report-app"><header class="cover"><div class="report-brand"><div class="eyebrow">Server inventory / configuration snapshot</div>')
    [void]$builder.AppendLine('<h1>' + (ConvertTo-HtmlText $serverName) + '</h1><p class="subtitle">' + (ConvertTo-HtmlText $subtitle) + '</p></div>')
    [void]$builder.AppendLine('<button class="nav-toggle" id="toggle-report-nav" aria-controls="report-nav" aria-expanded="false" type="button">Sections</button>')
    [void]$builder.AppendLine('<div class="standalone-tools"><form id="report-search-form" class="report-search" role="search"><input id="report-search-input" type="search" aria-label="Search the full report" placeholder="Search the full report"><button class="quiet-button" type="submit">Find</button><button id="report-print" class="quiet-button" type="button">Print all</button></form>')
    [void]$builder.AppendLine('<div class="search-controls"><button id="report-search-prev" class="quiet-button" type="button" disabled>Previous match</button><button id="report-search-next" class="quiet-button" type="button" disabled>Next match</button><button id="report-search-clear" class="quiet-button" type="button">Clear</button></div><div id="report-search-message" class="search-message" role="status" aria-live="polite">Search every section, including collapsed details.</div></div></header>')
    [void]$builder.AppendLine('<div class="report-layout"><nav class="report-nav" id="report-nav" aria-label="Report sections" tabindex="0"><div class="nav-heading">Inventory sections</div>')
    $orderedEntries = @($entries | Where-Object { $_.Id -eq $summaryId }) + @($entries | Where-Object { ($_.Id -ne $summaryId) -and ($_.Id -ne 'section-overview') -and ($_.Title -ne 'Report Contents') }) + @($entries | Where-Object { ($_.Id -ne $summaryId) -and (($_.Id -eq 'section-overview') -or ($_.Title -eq 'Report Contents')) })
    $referenceStarted = $false
    foreach ($entry in $orderedEntries) {
        if ((-not $referenceStarted) -and (($entry.Id -eq 'section-overview') -or ($entry.Title -eq 'Report Contents')) -and ($entry.Id -ne $summaryId)) {
            [void]$builder.AppendLine('<div class="nav-reference"><div class="nav-heading">Reference</div>')
            $referenceStarted = $true
        }
        $title = Get-CleanReportTitle $entry.Title
        $number = ''
        if ($entry.Title -match '^(\d+)\.') { $number = ([int]$matches[1]).ToString('00') }
        [void]$builder.AppendLine('<div class="nav-group"><button type="button" class="section-nav-button section-main-button" data-section="' + $entry.Id + '" data-title="' + (ConvertTo-HtmlText $title) + '"><span class="nav-number" aria-hidden="true">' + $number + '</span><span>' + (ConvertTo-HtmlText $title) + '</span></button>')
        if ($entry.Links.Count -gt 0) {
            [void]$builder.AppendLine('<div class="subsection-list">')
            foreach ($link in $entry.Links) {
                [void]$builder.AppendLine('<button type="button" class="subsection-nav-button" data-anchor="' + $link.Id + '">' + (ConvertTo-HtmlText (Get-CleanReportTitle $link.Title)) + '</button>')
            }
            [void]$builder.AppendLine('</div>')
        }
        [void]$builder.AppendLine('</div>')
    }
    if ($referenceStarted) { [void]$builder.AppendLine('</div>') }
    [void]$builder.AppendLine('</nav><main class="report-content" tabindex="0" aria-label="Report content"><div class="reading-width"><div class="section-pager"><div id="section-label" class="pager-label"></div><button id="previous-section" class="pager-button" type="button">Previous section</button><button id="next-section" class="pager-button" type="button">Next section</button></div>')
    Write-CleanReportSection -Id 'section-overview' -Title 'About this report'
    $sectionNumber = 0
    $datasetInSection = 0
    $currentId = 'section-overview'
    for ($index = 0; $index -lt $blockList.Count; $index++) {
        $block = $blockList[$index]
        $type = [string]$block.Type
        if ($type -eq 'Title') { continue }
        if (($type -eq 'Heading') -and ([int]$block.Level -eq 1)) {
            [void]$builder.AppendLine('</section>')
            $sectionNumber++
            $datasetInSection = 0
            $currentId = 'section-' + $sectionNumber
            Write-CleanReportSection -Id $currentId -Title ([string]$block.Text)
            continue
        }
        switch ($type) {
            'Heading' {
                $tag = 'h3'
                if ([int]$block.Level -gt 2) { $tag = 'h4' }
                [void]$builder.AppendLine('<' + $tag + ' id="' + $anchors[$index] + '" class="subsection-anchor">' + (ConvertTo-HtmlText $block.Text) + '</' + $tag + '>')
            }
            'Paragraph' {
                $class = ''
                if ($block.Warning) { $class = 'notice' }
                elseif ($block.Bold) { $class = 'info' }
                $text = ConvertTo-HtmlText $block.Text
                if ($block.Bold -and -not $block.Warning) { $text = '<strong>' + $text + '</strong>' }
                [void]$builder.AppendLine('<p class="' + $class + '">' + $text + '</p>')
            }
            'Bullets' {
                [void]$builder.AppendLine('<ul>')
                foreach ($item in @($block.Items)) { [void]$builder.AppendLine('<li>' + (ConvertTo-HtmlText $item) + '</li>') }
                [void]$builder.AppendLine('</ul>')
            }
            { ($_ -eq 'Table') -or ($_ -eq 'Preformatted') } {
                $datasetInSection++
                $priority = ([string]$block.Title -match '^(Collection failures requiring review|Recovery prerequisites and gaps)$') -and ($type -eq 'Table') -and ([int]$block.DisplayRows -gt 0)
                $openByDefault = $priority
                $class = 'dataset'
                $count = 'Text output'
                if ($priority) { $class += ' dataset-priority' }
                if ($type -eq 'Table') {
                    $count = [string]$block.DisplayRows + ' rows'
                    if ([int]$block.DisplayRows -eq 1) { $count = '1 row' }
                    if ([int]$block.TotalRows -gt [int]$block.DisplayRows) { $count = [string]$block.DisplayRows + ' of ' + [string]$block.TotalRows + ' rows' }
                    if ([int]$block.DisplayRows -gt 15) { $class += ' dataset-long' }
                    if (($currentId -ne $summaryId) -and ($datasetInSection -eq 1) -and ([int]$block.DisplayRows -gt 0) -and ([int]$block.DisplayRows -le 8) -and (@($block.Columns).Count -le 5)) { $openByDefault = $true }
                }
                $openText = 'false'
                if ($openByDefault) { $openText = 'true' }
                # Render open in the source for no-script and print fallbacks;
                # the script only collapses the lower-priority screen details.
                [void]$builder.AppendLine('<details open id="' + $anchors[$index] + '" class="' + $class + '" data-default-open="' + $openText + '" data-title="' + (ConvertTo-HtmlText $block.Title) + '"><summary><span class="dataset-title">' + (ConvertTo-HtmlText $block.Title) + '</span><span class="dataset-count">' + $count + '</span></summary><div class="dataset-body">')
                if ($type -eq 'Preformatted') {
                    if ([string]::IsNullOrWhiteSpace([string]$block.Text)) { [void]$builder.AppendLine('<p class="empty-data">No data was collected or the component is not present.</p>') }
                    else { [void]$builder.AppendLine('<pre>' + (ConvertTo-HtmlMultilineText $block.Text) + '</pre>') }
                }
                elseif ([int]$block.DisplayRows -le 0) { [void]$builder.AppendLine('<p class="empty-data">' + (ConvertTo-HtmlText $block.EmptyMessage) + '</p>') }
                else {
                    $tableClass = ''
                    if ([string]$block.Title -eq 'Server identity and baseline') { $tableClass = 'identity-table' }
                    [void]$builder.AppendLine('<div class="table-wrap" tabindex="0" aria-label="' + (ConvertTo-HtmlText $block.Title) + '"><table class="' + $tableClass + '"><thead><tr>')
                    foreach ($column in @($block.Columns)) {
                        $originalLabel = $column
                        $label = ([string]$column -creplace '([a-z0-9])([A-Z])', '$1 $2') -creplace '([A-Z]+)([A-Z][a-z])', '$1 $2'
                        if ($block.Labels -and $block.Labels.ContainsKey($column)) { $label = $block.Labels[$column]; $originalLabel = $label }
                        [void]$builder.AppendLine('<th scope="col" data-original-label="' + (ConvertTo-HtmlText $originalLabel) + '">' + (ConvertTo-HtmlText $label) + '</th>')
                    }
                    [void]$builder.AppendLine('</tr></thead><tbody>')
                    foreach ($row in @($block.Rows)) {
                        [void]$builder.AppendLine('<tr>')
                        foreach ($column in @($block.Columns)) {
                            $value = Get-ReportCellText -InputObject $row -PropertyName $column -MaxLength 2500
                            $text = ConvertTo-HtmlText $value
                            if ($column -match '^(Status|Severity)$') {
                                $status = ''
                                if ($value -match '^(Success|Complete|Completed)$') { $status = 'success' }
                                elseif ($value -match '^(Failed|Error|Critical|High)$') { $status = 'error' }
                                elseif ($value -match '^(Warning|Unavailable|Skipped|Medium)$') { $status = 'warning' }
                                if ($status) { $text = '<span class="status status-' + $status + '">' + $text + '</span>' }
                            }
                            [void]$builder.AppendLine('<td>' + $text + '</td>')
                        }
                        [void]$builder.AppendLine('</tr>')
                    }
                    [void]$builder.AppendLine('</tbody></table></div>')
                    if ([int]$block.TotalRows -gt [int]$block.DisplayRows) { [void]$builder.AppendLine('<p class="table-note">Showing ' + $block.DisplayRows + ' of ' + $block.TotalRows + ' rows. The complete dataset remains in the supporting exports.</p>') }
                }
                [void]$builder.AppendLine('</div></details>')
            }
        }
    }
    [void]$builder.AppendLine('</section></div><footer class="footer-note">Sensitive infrastructure documentation. This inventory is a configuration snapshot, not a backup. Supporting exports retain raw data and reusable artifacts.</footer></main></div></div>')
    $scriptBlock = @'
<script>
(function () {
    'use strict';
    var root = document.documentElement;
    var content = document.querySelector('.report-content');
    var sections = Array.prototype.slice.call(document.querySelectorAll('.report-section'));
    var buttons = Array.prototype.slice.call(document.querySelectorAll('.section-main-button'));
    var details = Array.prototype.slice.call(document.querySelectorAll('details.dataset'));
    var currentIndex = 0, matches = [], matchIndex = -1, printState = null;
    function byId(id) { return document.getElementById(id); }
    function parentSection(element) {
        while (element && element !== document) {
            if (element.classList && element.classList.contains('report-section')) { return element; }
            element = element.parentNode;
        }
        return null;
    }
    function openParents(element) {
        while (element && element !== document) {
            if (element.tagName === 'DETAILS') { element.open = true; }
            element = element.parentNode;
        }
    }
    function indexById(id) {
        for (var i=0; i<buttons.length; i++) { if (buttons[i].getAttribute('data-section') === id) { return i; } }
        return -1;
    }
    function activate(index, updateHash, anchor) {
        if (!buttons.length) { return; }
        currentIndex = Math.max(0, Math.min(index, buttons.length-1));
        var selectedId = buttons[currentIndex].getAttribute('data-section');
        for (var i=0; i<sections.length; i++) {
            var selected = sections[i].id === selectedId;
            sections[i].classList.toggle('active', selected);
            sections[i].setAttribute('aria-hidden', selected ? 'false' : 'true');
        }
        for (var j=0; j<buttons.length; j++) {
            var active = j === currentIndex;
            buttons[j].classList.toggle('active', active);
            buttons[j].setAttribute('aria-current', active ? 'page' : 'false');
            buttons[j].parentNode.classList.toggle('active', active);
        }
        var links = document.querySelectorAll('.subsection-nav-button');
        for (var k=0; k<links.length; k++) { links[k].classList.toggle('active', links[k].getAttribute('data-anchor') === anchor); }
        byId('section-label').textContent = buttons[currentIndex].getAttribute('data-title') + ' / ' + (currentIndex+1) + ' of ' + buttons.length;
        byId('previous-section').disabled = currentIndex === 0;
        byId('next-section').disabled = currentIndex === buttons.length-1;
        if (updateHash) {
            try { window.history.replaceState(null, '', '#' + (anchor || selectedId)); }
            catch (error) { window.location.hash = anchor || selectedId; }
        }
        if (anchor) {
            var target = byId(anchor);
            if (target) { openParents(target); target.scrollIntoView(true); }
        } else if (content) { content.scrollTop = 0; }
        root.classList.remove('nav-open');
        byId('toggle-report-nav').setAttribute('aria-expanded', 'false');
    }
    function revealElement(element) {
        var section = parentSection(element);
        if (!section) { return; }
        var index = indexById(section.id);
        if (index >= 0) { activate(index, false, ''); }
        openParents(element);
        try { element.scrollIntoView({block:'center',inline:'nearest'}); }
        catch (error) { element.scrollIntoView(true); }
    }
    function revealId(id) {
        var element = byId(id);
        if (!element) { return; }
        var section = parentSection(element);
        if (section) { activate(indexById(section.id), true, element === section ? '' : id); }
    }
    function clearSearch() {
        var marks = document.querySelectorAll('mark.report-match');
        for (var i=0; i<marks.length; i++) {
            var parent = marks[i].parentNode;
            parent.replaceChild(document.createTextNode(marks[i].textContent), marks[i]);
            parent.normalize();
        }
        matches = []; matchIndex = -1;
        root.classList.remove('search-active');
        byId('report-search-prev').disabled = true;
        byId('report-search-next').disabled = true;
        byId('report-search-message').textContent = 'Search every section, including collapsed details.';
    }
    function showMatch(index) {
        if (!matches.length) { return; }
        if (matchIndex >= 0) { matches[matchIndex].classList.remove('report-current'); }
        matchIndex = (index + matches.length) % matches.length;
        matches[matchIndex].classList.add('report-current');
        revealElement(matches[matchIndex]);
        byId('report-search-message').textContent = 'Match ' + (matchIndex+1) + ' of ' + matches.length + ' across the full report.';
    }
    function search() {
        clearSearch();
        var query = byId('report-search-input').value.replace(/^\s+|\s+$/g, '');
        if (!query) { return; }
        root.classList.add('search-active');
        var lowerQuery = query.toLowerCase(), nodes = [];
        for (var i=0; i<sections.length; i++) {
            var walker = document.createTreeWalker(sections[i], NodeFilter.SHOW_TEXT, {acceptNode:function(node) {
                var tag = node.parentNode && node.parentNode.nodeName;
                if (tag === 'SCRIPT' || tag === 'STYLE' || tag === 'BUTTON' || tag === 'MARK') { return NodeFilter.FILTER_REJECT; }
                return node.nodeValue.toLowerCase().indexOf(lowerQuery) >= 0 ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT;
            }}, false);
            var node; while ((node = walker.nextNode())) { nodes.push(node); }
        }
        for (var n=0; n<nodes.length; n++) {
            var text = nodes[n].nodeValue, lower = text.toLowerCase(), offset=0, at=lower.indexOf(lowerQuery), fragment=document.createDocumentFragment();
            while (at >= 0) {
                if (at > offset) { fragment.appendChild(document.createTextNode(text.substring(offset,at))); }
                var mark = document.createElement('mark'); mark.className='report-match';
                mark.appendChild(document.createTextNode(text.substr(at,query.length))); fragment.appendChild(mark);
                matches.push(mark); offset=at+query.length; at=lower.indexOf(lowerQuery,offset);
            }
            if (offset < text.length) { fragment.appendChild(document.createTextNode(text.substring(offset))); }
            nodes[n].parentNode.replaceChild(fragment,nodes[n]);
        }
        if (!matches.length) { byId('report-search-message').textContent='No matches for "' + query + '".'; return; }
        byId('report-search-prev').disabled=false; byId('report-search-next').disabled=false; showMatch(0);
    }
    function preparePrint() {
        if (printState) { return; }
        printState = details.map(function(detail) { return detail.open; });
        for (var i=0; i<details.length; i++) { details[i].open=true; }
    }
    function restorePrint() {
        if (!printState) { return; }
        for (var i=0; i<details.length; i++) { details[i].open=printState[i]; }
        printState=null;
    }
    for (var d=0; d<details.length; d++) { details[d].open=details[d].getAttribute('data-default-open') === 'true'; }
    // Put actionable findings ahead of routine datasets without changing the
    // underlying blocks or discarding their original section/anchor identities.
    var summarySection=byId(root.getAttribute('data-default-section'));
    if(summarySection){
        var cursor=summarySection.querySelector('.section-actions');
        var priorityPanels=summarySection.querySelectorAll('.dataset-priority');
        for(var p=0;cursor&&p<priorityPanels.length;p++){
            cursor.parentNode.insertBefore(priorityPanels[p],cursor.nextSibling);cursor=priorityPanels[p];
        }
    }
    for (var b=0; b<buttons.length; b++) {
        (function(index) { buttons[index].addEventListener('click',function() { activate(index,true,''); }); }(b));
    }
    var jumpButtons = document.querySelectorAll('[data-anchor]');
    for (var j=0; j<jumpButtons.length; j++) {
        (function(button) { button.addEventListener('click',function() { revealId(button.getAttribute('data-anchor')); }); }(jumpButtons[j]));
    }
    var expandButtons = document.querySelectorAll('[data-details-action]');
    for (var e=0; e<expandButtons.length; e++) {
        (function(button) { button.addEventListener('click',function() {
            var section=parentSection(button), panels=section.querySelectorAll('details.dataset');
            for (var i=0; i<panels.length; i++) { panels[i].open=button.getAttribute('data-details-action') === 'expand'; }
        }); }(expandButtons[e]));
    }
    byId('previous-section').addEventListener('click',function() { activate(currentIndex-1,true,''); });
    byId('next-section').addEventListener('click',function() { activate(currentIndex+1,true,''); });
    byId('toggle-report-nav').addEventListener('click',function() {
        var open=root.classList.toggle('nav-open'); this.setAttribute('aria-expanded',open ? 'true' : 'false');
    });
    byId('report-search-form').addEventListener('submit',function(event) { event.preventDefault(); search(); });
    byId('report-search-prev').addEventListener('click',function() { showMatch(matchIndex-1); });
    byId('report-search-next').addEventListener('click',function() { showMatch(matchIndex+1); });
    byId('report-search-clear').addEventListener('click',function() { clearSearch(); byId('report-search-input').value=''; });
    byId('report-print').addEventListener('click',function() { window.print(); });
    window.addEventListener('beforeprint',preparePrint); window.addEventListener('afterprint',restorePrint);
    window.addEventListener('hashchange',function() { revealId(window.location.hash.substring(1)); });
    // The native viewer uses this to expose search hits inside collapsed panels.
    window.SICMReport = {revealElement:revealElement,preparePrint:preparePrint,restorePrint:restorePrint};
    root.classList.add('report-js');
    var requested = window.location.hash.substring(1);
    if (requested && byId(requested)) { revealId(requested); }
    else { activate(indexById(root.getAttribute('data-default-section')),false,''); }
}());
</script>
'@
    [void]$builder.AppendLine($scriptBlock)
    [void]$builder.AppendLine('</body></html>')
    $encoding = New-Object System.Text.UTF8Encoding($true)
    [System.IO.File]::WriteAllText($Path, $builder.ToString(), $encoding)
}

function Write-DocxTextRun {
    param(
        [Parameter(Mandatory = $true)]
        [System.Xml.XmlWriter]$Writer,

        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$Text,

        [Parameter()]
        [switch]$Bold,

        [Parameter()]
        [string]$Font = '',

        [Parameter()]
        [int]$FontSizeHalfPoints = 0
    )

    $wordNamespace = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
    $Writer.WriteStartElement('w', 'r', $wordNamespace)
    if ($Bold -or $Font -or ($FontSizeHalfPoints -gt 0)) {
        $Writer.WriteStartElement('w', 'rPr', $wordNamespace)
        if ($Bold) {
            $Writer.WriteStartElement('w', 'b', $wordNamespace)
            $Writer.WriteEndElement()
        }
        if ($Font) {
            $Writer.WriteStartElement('w', 'rFonts', $wordNamespace)
            $Writer.WriteAttributeString('w', 'ascii', $wordNamespace, $Font)
            $Writer.WriteAttributeString('w', 'hAnsi', $wordNamespace, $Font)
            $Writer.WriteEndElement()
        }
        if ($FontSizeHalfPoints -gt 0) {
            $Writer.WriteStartElement('w', 'sz', $wordNamespace)
            $Writer.WriteAttributeString('w', 'val', $wordNamespace, [string]$FontSizeHalfPoints)
            $Writer.WriteEndElement()
            $Writer.WriteStartElement('w', 'szCs', $wordNamespace)
            $Writer.WriteAttributeString('w', 'val', $wordNamespace, [string]$FontSizeHalfPoints)
            $Writer.WriteEndElement()
        }
        $Writer.WriteEndElement()
    }

    $lines = [regex]::Split((ConvertTo-ReportMultilineText -Value $Text -MaxLength 0), '\r\n|\n|\r')
    for ($index = 0; $index -lt $lines.Count; $index++) {
        if ($index -gt 0) {
            $Writer.WriteStartElement('w', 'br', $wordNamespace)
            $Writer.WriteEndElement()
        }
        $Writer.WriteStartElement('w', 't', $wordNamespace)
        $Writer.WriteAttributeString('xml', 'space', 'http://www.w3.org/XML/1998/namespace', 'preserve')
        $Writer.WriteString([string]$lines[$index])
        $Writer.WriteEndElement()
    }
    $Writer.WriteEndElement()
}

function Write-DocxParagraph {
    param(
        [Parameter(Mandatory = $true)]
        [System.Xml.XmlWriter]$Writer,

        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$Text,

        [Parameter()]
        [string]$Style = 'Normal',

        [Parameter()]
        [switch]$Bold,

        [Parameter()]
        [switch]$PageBreakBefore,

        [Parameter()]
        [switch]$KeepWithNext,

        [Parameter()]
        [switch]$Warning,

        [Parameter()]
        [string]$Font = '',

        [Parameter()]
        [int]$FontSizeHalfPoints = 0
    )

    $wordNamespace = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
    $Writer.WriteStartElement('w', 'p', $wordNamespace)
    $Writer.WriteStartElement('w', 'pPr', $wordNamespace)
    if ($Style) {
        $Writer.WriteStartElement('w', 'pStyle', $wordNamespace)
        $Writer.WriteAttributeString('w', 'val', $wordNamespace, $Style)
        $Writer.WriteEndElement()
    }
    if ($PageBreakBefore) {
        $Writer.WriteStartElement('w', 'pageBreakBefore', $wordNamespace)
        $Writer.WriteEndElement()
    }
    if ($KeepWithNext) {
        $Writer.WriteStartElement('w', 'keepNext', $wordNamespace)
        $Writer.WriteEndElement()
    }
    if ($Warning) {
        $Writer.WriteStartElement('w', 'shd', $wordNamespace)
        $Writer.WriteAttributeString('w', 'fill', $wordNamespace, 'FFF2CC')
        $Writer.WriteEndElement()
    }
    $Writer.WriteEndElement()
    Write-DocxTextRun -Writer $Writer -Text $Text -Bold:$Bold -Font $Font -FontSizeHalfPoints $FontSizeHalfPoints
    $Writer.WriteEndElement()
}

function Write-DocxTable {
    param(
        [Parameter(Mandatory = $true)]
        [System.Xml.XmlWriter]$Writer,

        [Parameter(Mandatory = $true)]
        [object]$Block
    )

    $wordNamespace = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
    Write-DocxParagraph -Writer $Writer -Text ([string]$Block.Title) -Style 'Heading3' -KeepWithNext

    if ($Block.DisplayRows -le 0) {
        Write-DocxParagraph -Writer $Writer -Text ([string]$Block.EmptyMessage) -Style 'Normal'
        return
    }

    $Writer.WriteStartElement('w', 'tbl', $wordNamespace)
    $Writer.WriteStartElement('w', 'tblPr', $wordNamespace)
    $columnCount = [Math]::Max(1, @($Block.Columns).Count)
    $tableWidth = 14760
    $columnWidth = [Math]::Max(360, [int][Math]::Floor($tableWidth / $columnCount))

    $Writer.WriteStartElement('w', 'tblW', $wordNamespace)
    $Writer.WriteAttributeString('w', 'w', $wordNamespace, [string]$tableWidth)
    $Writer.WriteAttributeString('w', 'type', $wordNamespace, 'dxa')
    $Writer.WriteEndElement()
    $Writer.WriteStartElement('w', 'tblLayout', $wordNamespace)
    $Writer.WriteAttributeString('w', 'type', $wordNamespace, 'fixed')
    $Writer.WriteEndElement()
    $Writer.WriteStartElement('w', 'tblBorders', $wordNamespace)
    foreach ($borderName in @('top', 'left', 'bottom', 'right', 'insideH', 'insideV')) {
        $Writer.WriteStartElement('w', $borderName, $wordNamespace)
        $Writer.WriteAttributeString('w', 'val', $wordNamespace, 'single')
        $Writer.WriteAttributeString('w', 'sz', $wordNamespace, '4')
        $Writer.WriteAttributeString('w', 'space', $wordNamespace, '0')
        $Writer.WriteAttributeString('w', 'color', $wordNamespace, 'AAB7C4')
        $Writer.WriteEndElement()
    }
    $Writer.WriteEndElement()
    $Writer.WriteStartElement('w', 'tblCellMar', $wordNamespace)
    foreach ($marginName in @('top', 'left', 'bottom', 'right')) {
        $Writer.WriteStartElement('w', $marginName, $wordNamespace)
        $Writer.WriteAttributeString('w', 'w', $wordNamespace, '70')
        $Writer.WriteAttributeString('w', 'type', $wordNamespace, 'dxa')
        $Writer.WriteEndElement()
    }
    $Writer.WriteEndElement()
    $Writer.WriteEndElement()

    $Writer.WriteStartElement('w', 'tblGrid', $wordNamespace)
    foreach ($unusedColumn in @($Block.Columns)) {
        $Writer.WriteStartElement('w', 'gridCol', $wordNamespace)
        $Writer.WriteAttributeString('w', 'w', $wordNamespace, [string]$columnWidth)
        $Writer.WriteEndElement()
    }
    $Writer.WriteEndElement()

    $Writer.WriteStartElement('w', 'tr', $wordNamespace)
    $Writer.WriteStartElement('w', 'trPr', $wordNamespace)
    $Writer.WriteStartElement('w', 'tblHeader', $wordNamespace)
    $Writer.WriteEndElement()
    $Writer.WriteEndElement()
    foreach ($column in @($Block.Columns)) {
        $header = $column
        if ($Block.Labels -and $Block.Labels.ContainsKey($column)) {
            $header = $Block.Labels[$column]
        }
        $Writer.WriteStartElement('w', 'tc', $wordNamespace)
        $Writer.WriteStartElement('w', 'tcPr', $wordNamespace)
        $Writer.WriteStartElement('w', 'tcW', $wordNamespace)
        $Writer.WriteAttributeString('w', 'w', $wordNamespace, [string]$columnWidth)
        $Writer.WriteAttributeString('w', 'type', $wordNamespace, 'dxa')
        $Writer.WriteEndElement()
        $Writer.WriteStartElement('w', 'shd', $wordNamespace)
        $Writer.WriteAttributeString('w', 'fill', $wordNamespace, 'D9E2EC')
        $Writer.WriteEndElement()
        $Writer.WriteEndElement()
        Write-DocxParagraph -Writer $Writer -Text ([string]$header) -Style 'TableText' -Bold
        $Writer.WriteEndElement()
    }
    $Writer.WriteEndElement()

    foreach ($row in @($Block.Rows)) {
        $Writer.WriteStartElement('w', 'tr', $wordNamespace)
        $Writer.WriteStartElement('w', 'trPr', $wordNamespace)
        $Writer.WriteStartElement('w', 'cantSplit', $wordNamespace)
        $Writer.WriteEndElement()
        $Writer.WriteEndElement()
        foreach ($column in @($Block.Columns)) {
            $cellText = Get-ReportCellText -InputObject $row -PropertyName $column -MaxLength 2500
            $Writer.WriteStartElement('w', 'tc', $wordNamespace)
            $Writer.WriteStartElement('w', 'tcPr', $wordNamespace)
            $Writer.WriteStartElement('w', 'tcW', $wordNamespace)
            $Writer.WriteAttributeString('w', 'w', $wordNamespace, [string]$columnWidth)
            $Writer.WriteAttributeString('w', 'type', $wordNamespace, 'dxa')
            $Writer.WriteEndElement()
            $Writer.WriteEndElement()
            Write-DocxParagraph -Writer $Writer -Text $cellText -Style 'TableText'
            $Writer.WriteEndElement()
        }
        $Writer.WriteEndElement()
    }
    $Writer.WriteEndElement()

    if ($Block.TotalRows -gt $Block.DisplayRows) {
        Write-DocxParagraph -Writer $Writer -Text ('Showing {0} of {1} rows. The complete dataset remains in the supporting exports.' -f $Block.DisplayRows, $Block.TotalRows) -Style 'Caption'
    }
}

function Write-ConsolidatedDocxReport {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [System.Collections.IEnumerable]$Blocks,

        [Parameter(Mandatory = $true)]
        [string]$DocumentTitle,

        [Parameter(Mandatory = $true)]
        [string]$SubjectText
    )

    $stagingPath = Join-Path ([System.IO.Path]::GetTempPath()) ('ServerRebuildReport-' + [Guid]::NewGuid().ToString('N'))
    New-Item -Path $stagingPath -ItemType Directory -Force | Out-Null
    New-Item -Path (Join-Path $stagingPath '_rels') -ItemType Directory -Force | Out-Null
    New-Item -Path (Join-Path $stagingPath 'word') -ItemType Directory -Force | Out-Null
    New-Item -Path (Join-Path $stagingPath 'word\_rels') -ItemType Directory -Force | Out-Null
    New-Item -Path (Join-Path $stagingPath 'docProps') -ItemType Directory -Force | Out-Null

    try {
        $contentTypes = @'
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
  <Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/>
  <Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
  <Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>
</Types>
'@
        $rootRelationships = @'
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
  <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>
</Relationships>
'@
        $documentRelationships = @'
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings" Target="settings.xml"/>
</Relationships>
'@
        $styles = @'
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:docDefaults>
    <w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/><w:sz w:val="20"/><w:szCs w:val="20"/></w:rPr></w:rPrDefault>
    <w:pPrDefault><w:pPr><w:spacing w:after="90" w:line="240" w:lineRule="auto"/></w:pPr></w:pPrDefault>
  </w:docDefaults>
  <w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>
  <w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:next w:val="Subtitle"/><w:qFormat/><w:rPr><w:b/><w:color w:val="102A43"/><w:sz w:val="44"/><w:szCs w:val="44"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Subtitle"><w:name w:val="Subtitle"/><w:basedOn w:val="Normal"/><w:qFormat/><w:rPr><w:color w:val="486581"/><w:sz w:val="26"/><w:szCs w:val="26"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="120"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:color w:val="102A43"/><w:sz w:val="32"/><w:szCs w:val="32"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="180" w:after="90"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/><w:color w:val="243B53"/><w:sz w:val="26"/><w:szCs w:val="26"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="120" w:after="60"/><w:outlineLvl w:val="2"/></w:pPr><w:rPr><w:b/><w:color w:val="334E68"/><w:sz w:val="22"/><w:szCs w:val="22"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="TableText"><w:name w:val="Table Text"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:before="0" w:after="0" w:line="180" w:lineRule="auto"/></w:pPr><w:rPr><w:sz w:val="16"/><w:szCs w:val="16"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Caption"><w:name w:val="Caption"/><w:basedOn w:val="Normal"/><w:rPr><w:i/><w:color w:val="52667A"/><w:sz w:val="17"/><w:szCs w:val="17"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="CodeBlock"><w:name w:val="Code Block"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:before="60" w:after="90" w:line="180" w:lineRule="auto"/><w:shd w:fill="F5F7FA"/></w:pPr><w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas"/><w:sz w:val="15"/><w:szCs w:val="15"/></w:rPr></w:style>
</w:styles>
'@
        $settingsXml = @'
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:zoom w:percent="90"/>
  <w:updateFields w:val="true"/>
</w:settings>
'@
        $createdUtc = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
        $coreXml = '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>' +
            '<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">' +
            '<dc:title>' + [System.Security.SecurityElement]::Escape($DocumentTitle) + '</dc:title>' +
            '<dc:subject>' + [System.Security.SecurityElement]::Escape($SubjectText) + '</dc:subject>' +
            '<dc:creator>Server Inventory &amp; Configuration Exporter</dc:creator>' +
            '<cp:lastModifiedBy>Server Inventory &amp; Configuration Exporter</cp:lastModifiedBy>' +
            '<dcterms:created xsi:type="dcterms:W3CDTF">' + $createdUtc + '</dcterms:created>' +
            '<dcterms:modified xsi:type="dcterms:W3CDTF">' + $createdUtc + '</dcterms:modified>' +
            '</cp:coreProperties>'
        $appXml = @'
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">
  <Application>Server Inventory &amp; Configuration Exporter</Application>
  <AppVersion>3.0.0</AppVersion>
</Properties>
'@

        $encoding = New-Object System.Text.UTF8Encoding($false)
        [System.IO.File]::WriteAllText((Join-Path $stagingPath '[Content_Types].xml'), $contentTypes.TrimStart(), $encoding)
        [System.IO.File]::WriteAllText((Join-Path $stagingPath '_rels\.rels'), $rootRelationships.TrimStart(), $encoding)
        [System.IO.File]::WriteAllText((Join-Path $stagingPath 'word\_rels\document.xml.rels'), $documentRelationships.TrimStart(), $encoding)
        [System.IO.File]::WriteAllText((Join-Path $stagingPath 'word\styles.xml'), $styles.TrimStart(), $encoding)
        [System.IO.File]::WriteAllText((Join-Path $stagingPath 'word\settings.xml'), $settingsXml.TrimStart(), $encoding)
        [System.IO.File]::WriteAllText((Join-Path $stagingPath 'docProps\core.xml'), $coreXml, $encoding)
        [System.IO.File]::WriteAllText((Join-Path $stagingPath 'docProps\app.xml'), $appXml.TrimStart(), $encoding)

        $documentPath = Join-Path $stagingPath 'word\document.xml'
        $writerSettings = New-Object System.Xml.XmlWriterSettings
        $writerSettings.Encoding = $encoding
        $writerSettings.Indent = $false
        $writerSettings.CloseOutput = $true
        $writer = [System.Xml.XmlWriter]::Create($documentPath, $writerSettings)
        try {
            $wordNamespace = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
            $writer.WriteStartDocument()
            $writer.WriteStartElement('w', 'document', $wordNamespace)
            $writer.WriteStartElement('w', 'body', $wordNamespace)

            $firstLevelOne = $true
            foreach ($block in $Blocks) {
                switch ([string]$block.Type) {
                    'Title' {
                        Write-DocxParagraph -Writer $writer -Text ([string]$block.Text) -Style 'Title' -KeepWithNext
                        if (-not [string]::IsNullOrWhiteSpace([string]$block.Subtitle)) {
                            Write-DocxParagraph -Writer $writer -Text ([string]$block.Subtitle) -Style 'Subtitle'
                        }
                        Write-DocxParagraph -Writer $writer -Text 'Generated automatically. Treat as sensitive infrastructure documentation.' -Style 'Caption'
                    }
                    'Heading' {
                        $styleName = 'Heading' + [string]$block.Level
                        $breakBefore = [bool]$block.PageBreakBefore
                        if (($block.Level -eq 1) -and $firstLevelOne) {
                            $breakBefore = $false
                            $firstLevelOne = $false
                        }
                        Write-DocxParagraph -Writer $writer -Text ([string]$block.Text) -Style $styleName -PageBreakBefore:$breakBefore -KeepWithNext
                    }
                    'Paragraph' {
                        Write-DocxParagraph -Writer $writer -Text ([string]$block.Text) -Style 'Normal' -Bold:$block.Bold -Warning:$block.Warning
                    }
                    'Bullets' {
                        foreach ($item in @($block.Items)) {
                            Write-DocxParagraph -Writer $writer -Text ('- ' + [string]$item) -Style 'Normal'
                        }
                    }
                    'Preformatted' {
                        Write-DocxParagraph -Writer $writer -Text ([string]$block.Title) -Style 'Heading3' -KeepWithNext
                        if ([string]::IsNullOrWhiteSpace([string]$block.Text)) {
                            Write-DocxParagraph -Writer $writer -Text 'No data was collected or the component is not present.' -Style 'Normal'
                        }
                        else {
                            Write-DocxParagraph -Writer $writer -Text ([string]$block.Text) -Style 'CodeBlock' -Font 'Consolas' -FontSizeHalfPoints 15
                        }
                    }
                    'Table' {
                        Write-DocxTable -Writer $writer -Block $block
                    }
                }
            }

            $writer.WriteStartElement('w', 'sectPr', $wordNamespace)
            $writer.WriteStartElement('w', 'pgSz', $wordNamespace)
            $writer.WriteAttributeString('w', 'w', $wordNamespace, '15840')
            $writer.WriteAttributeString('w', 'h', $wordNamespace, '12240')
            $writer.WriteAttributeString('w', 'orient', $wordNamespace, 'landscape')
            $writer.WriteEndElement()
            $writer.WriteStartElement('w', 'pgMar', $wordNamespace)
            $writer.WriteAttributeString('w', 'top', $wordNamespace, '720')
            $writer.WriteAttributeString('w', 'right', $wordNamespace, '540')
            $writer.WriteAttributeString('w', 'bottom', $wordNamespace, '720')
            $writer.WriteAttributeString('w', 'left', $wordNamespace, '540')
            $writer.WriteAttributeString('w', 'header', $wordNamespace, '360')
            $writer.WriteAttributeString('w', 'footer', $wordNamespace, '360')
            $writer.WriteAttributeString('w', 'gutter', $wordNamespace, '0')
            $writer.WriteEndElement()
            $writer.WriteEndElement()

            $writer.WriteEndElement()
            $writer.WriteEndElement()
            $writer.WriteEndDocument()
        }
        finally {
            if ($writer) {
                $writer.Dispose()
            }
        }

        if (Test-Path -LiteralPath $Path) {
            Remove-Item -LiteralPath $Path -Force
        }
        Add-Type -AssemblyName System.IO.Compression
        Add-Type -AssemblyName System.IO.Compression.FileSystem

        # Build the DOCX archive manually so Open XML part names always use forward slashes.
        # Older .NET Framework versions used by Windows PowerShell 4.0 can emit backslashes
        # when ZipFile.CreateFromDirectory is used, which produces non-standard DOCX entries.
        $docxFileStream = $null
        $docxZipArchive = $null
        try {
            $docxFileStream = [System.IO.File]::Open(
                $Path,
                [System.IO.FileMode]::CreateNew,
                [System.IO.FileAccess]::ReadWrite,
                [System.IO.FileShare]::None
            )
            $docxZipArchive = New-Object -TypeName System.IO.Compression.ZipArchive -ArgumentList @(
                $docxFileStream,
                [System.IO.Compression.ZipArchiveMode]::Create,
                $false
            )

            foreach ($sourceFile in @(Get-ChildItem -LiteralPath $stagingPath -Recurse -File)) {
                $relativeName = $sourceFile.FullName.Substring($stagingPath.Length).TrimStart([char[]]@('\', '/'))
                $entryName = $relativeName.Replace('\', '/')
                $zipEntry = $docxZipArchive.CreateEntry($entryName, [System.IO.Compression.CompressionLevel]::Optimal)
                $sourceStream = $null
                $destinationStream = $null
                try {
                    $sourceStream = [System.IO.File]::OpenRead($sourceFile.FullName)
                    $destinationStream = $zipEntry.Open()
                    $sourceStream.CopyTo($destinationStream)
                }
                finally {
                    if ($destinationStream) { $destinationStream.Dispose() }
                    if ($sourceStream) { $sourceStream.Dispose() }
                }
            }
        }
        finally {
            if ($docxZipArchive) { $docxZipArchive.Dispose() }
            if ($docxFileStream) { $docxFileStream.Dispose() }
        }
    }
    finally {
        if (Test-Path -LiteralPath $stagingPath) {
            Remove-Item -LiteralPath $stagingPath -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

function Get-ReportRelativePath {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter(Mandatory = $true)]
        [string]$Root
    )

    if ($Path.StartsWith($Root, [System.StringComparison]::OrdinalIgnoreCase)) {
        return $Path.Substring($Root.Length).TrimStart('\')
    }
    return $Path
}

function Add-RoleRecoveryGap {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [System.Collections.ArrayList]$List,

        [Parameter(Mandatory = $true)]
        [string]$Severity,

        [Parameter(Mandatory = $true)]
        [string]$Requirement,

        [Parameter(Mandatory = $true)]
        [string]$Reason,

        [Parameter(Mandatory = $true)]
        [string]$Action
    )

    [void]$List.Add([pscustomobject]@{
        Severity    = $Severity
        Requirement = $Requirement
        Reason      = $Reason
        Action      = $Action
    })
}

function New-ConsolidatedServerReport {
    param(
        [Parameter(Mandatory = $true)]
        [string]$DocxPath,

        [Parameter(Mandatory = $true)]
        [string]$HtmlPath,

        [Parameter(Mandatory = $true)]
        [string]$SummaryTextPath
    )

    $script:reportBlocks = New-Object System.Collections.ArrayList
    Write-InventoryLog -Message 'Consolidated report stage: loading collected datasets and assembling report sections.'
    Write-InventoryLog -Message 'Consolidated report stage: snapshotting collector and detected-role state.'

    $collectorRows = @(foreach ($collectorRow in $script:collectorResults) { $collectorRow })
    $failedCollectors = @($collectorRows | Where-Object { $_.Status -ne 'Success' })
    $successfulCollectors = @($collectorRows | Where-Object { $_.Status -eq 'Success' })
    $roleRows = @(foreach ($roleRow in $script:detectedRoles) { $roleRow })
    $roleNames = @($roleRows | Select-Object -ExpandProperty Role)
    $selectedInventoryRows = @(Get-SelectedCategoryRows -Mode Inventory)
    $selectedExportRows = @(Get-SelectedCategoryRows -Mode Export)
    $roleRecoveryReference = @(Get-RoleRecoveryReferenceRows -RoleRows $roleRows)
    Write-InventoryLog -Message 'Consolidated report stage: collector, selection, and role state snapshot completed.'

    $installedRoles = @(Get-ReportData -BasePath (Join-Path $paths.Summary 'Installed-Server-Roles'))
    $installedFeaturesReport = @(Get-ReportData -BasePath (Join-Path $paths.System 'Installed-Windows-Roles-and-Features'))
    $hotfixes = @(Get-ReportData -BasePath (Join-Path $paths.System 'Installed-Hotfixes'))
    $windowsUpdateConfiguration = @(Get-ReportData -BasePath (Join-Path $paths.System 'Windows-Update-Configuration'))
    $windowsUpdateHistory = @(Get-ReportData -BasePath (Join-Path $paths.System 'Windows-Update-History'))
    $pendingRebootIndicators = @(Get-ReportData -BasePath (Join-Path $paths.System 'Pending-Reboot-Indicators'))
    $locale = @(Get-ReportData -BasePath (Join-Path $paths.System 'Locale-TimeZone'))
    $processorsReport = @(Get-ReportData -BasePath (Join-Path $paths.Hardware 'Processors'))
    $memoryModules = @(Get-ReportData -BasePath (Join-Path $paths.Hardware 'Physical-Memory'))
    $disks = @(Get-ReportData -BasePath (Join-Path $paths.Storage 'Disks-Readable'))
    if ($disks.Count -eq 0) { $disks = @(Get-ReportData -BasePath (Join-Path $paths.Storage 'Disk-Drives-Readable')) }
    $partitions = @(Get-ReportData -BasePath (Join-Path $paths.Storage 'Partitions-Readable'))
    $volumes = @(Get-ReportData -BasePath (Join-Path $paths.Storage 'Volumes-Readable'))
    if ($volumes.Count -eq 0) { $volumes = @(Get-ReportData -BasePath (Join-Path $paths.Storage 'Logical-Disks-Readable')) }
    $storagePools = @(Get-ReportData -BasePath (Join-Path $paths.Storage 'Storage-Pools-Readable'))
    $virtualDisks = @(Get-ReportData -BasePath (Join-Path $paths.Storage 'Virtual-Disks-Readable'))
    $iscsiPortals = @(Get-ReportData -BasePath (Join-Path $paths.Network 'iSCSI-Target-Portals'))
    $iscsiSessions = @(Get-ReportData -BasePath (Join-Path $paths.Network 'iSCSI-Sessions'))
    $networkAdapters = @(Get-ReportData -BasePath (Join-Path $paths.Network 'Network-Adapters'))
    $ipAddresses = @(Get-ReportData -BasePath (Join-Path $paths.Network 'IP-Addresses'))
    $dnsClients = @(Get-ReportData -BasePath (Join-Path $paths.Network 'DNS-Client-Server-Addresses'))
    $routes = @(Get-ReportData -BasePath (Join-Path $paths.Network 'Routes'))
    $networkTeams = @(Get-ReportData -BasePath (Join-Path $paths.Network 'LBFO-Teams'))
    $switchTeams = @(Get-ReportData -BasePath (Join-Path $paths.Network 'Switch-Embedded-Teams'))
    $tcpListeners = @(Get-ReportData -BasePath (Join-Path $paths.Network 'TCP-Listening-Endpoints'))
    $udpListeners = @(Get-ReportData -BasePath (Join-Path $paths.Network 'UDP-Listening-Endpoints'))
    $applications = @(Get-ReportData -BasePath (Join-Path $paths.Software 'Installed-Applications'))
    $odbcDataSources = @(Get-ReportData -BasePath (Join-Path $paths.Software 'ODBC-Data-Sources'))
    $customServices = @(Get-ReportData -BasePath (Join-Path $paths.Software 'Custom-Service-Configuration'))
    $scheduledTasks = @(Get-ReportData -BasePath (Join-Path $paths.Software 'Scheduled-Tasks'))
    $customTasks = @($scheduledTasks | Where-Object { ([string]$_.TaskPath) -notlike '\Microsoft\*' })
    $localUsers = @(Get-ReportData -BasePath (Join-Path $paths.Security 'Local-Users'))
    $localGroups = @(Get-ReportData -BasePath (Join-Path $paths.Security 'Local-Groups'))
    $localMemberships = @(Get-ReportData -BasePath (Join-Path $paths.Security 'Local-Group-Memberships'))
    $firewallProfiles = @(Get-ReportData -BasePath (Join-Path $paths.Security 'Firewall-Profiles'))
    $firewallRules = @(Get-ReportData -BasePath (Join-Path $paths.Security 'Firewall-Rules'))
    $enabledFirewallRules = @($firewallRules | Where-Object { Test-ReportTrue -Value $_.Enabled })
    $defenderStatus = @(Get-ReportData -BasePath (Join-Path $paths.Security 'Microsoft-Defender-Status'))
    $bitLocker = @(Get-ReportData -BasePath (Join-Path $paths.Security 'BitLocker-Status'))
    $certificates = @(Get-ReportData -BasePath (Join-Path $paths.Security 'Local-Machine-Certificates'))
    $shares = @(Get-ReportData -BasePath (Join-Path $paths.Shares 'SMB-Shares'))
    $sharePermissions = @(Get-ReportData -BasePath (Join-Path $paths.Shares 'SMB-Share-Permissions'))
    $rootNtfs = @(Get-ReportData -BasePath (Join-Path $paths.Shares 'Share-Root-NTFS-Permissions'))
    $smbServerConfiguration = @(Get-ReportData -BasePath (Join-Path $paths.Shares 'SMB-Server-Configuration'))

    $pagefileSettings = @(Get-ReportData -BasePath (Join-Path $paths.System 'Pagefile-Settings'))
    $startupCommands = @(Get-ReportData -BasePath (Join-Path $paths.System 'Startup-Commands'))
    $machineEnvironment = @(Get-ReportData -BasePath (Join-Path $paths.System 'Machine-Environment-Variables'))
    $machineEnvironmentForReport = @(
        foreach ($environmentItem in $machineEnvironment) {
            $environmentName = [string](Get-ReportPropertyValue -InputObject $environmentItem -PropertyName 'Name')
            $environmentValue = Get-ReportPropertyValue -InputObject $environmentItem -PropertyName 'Value'
            if ($environmentName -match '(?i)(password|passwd|pwd|secret|token|credential|private.?key|api.?key|connection.?string)') {
                $environmentValue = '[value omitted from consolidated report; verify the approved credential or configuration source]'
            }
            [pscustomobject]@{
                Name  = $environmentName
                Value = $environmentValue
            }
        }
    )
    $windowsBackupPolicy = @(Get-ReportData -BasePath (Join-Path $paths.System 'Windows-Backup-Policy'))
    $windowsBackupSchedule = @(Get-ReportData -BasePath (Join-Path $paths.System 'Windows-Backup-Schedule'))
    $windowsBackupSummary = @(Get-ReportData -BasePath (Join-Path $paths.System 'Windows-Backup-Summary'))

    $signedDrivers = @(Get-ReportData -BasePath (Join-Path $paths.Hardware 'Signed-Drivers'))
    $pnpDevices = @(Get-ReportData -BasePath (Join-Path $paths.Hardware 'Plug-and-Play-Devices'))
    $physicalDisks = @(Get-ReportData -BasePath (Join-Path $paths.Storage 'Physical-Disks-Readable'))
    $mpioClaimSettings = @(Get-ReportData -BasePath (Join-Path $paths.Storage 'MPIO-Automatic-Claim-Settings'))
    $advancedStoragePath = Join-Path $paths.Storage 'Advanced-Storage-and-File-Services'
    $dedupVolumes = @(Get-ReportData -BasePath (Join-Path $advancedStoragePath 'Data-Deduplication-Volumes'))
    $dedupSchedules = @(Get-ReportData -BasePath (Join-Path $advancedStoragePath 'Data-Deduplication-Schedules'))
    $dedupStatus = @(Get-ReportData -BasePath (Join-Path $advancedStoragePath 'Data-Deduplication-Status'))
    $storageReplicaGroups = @(Get-ReportData -BasePath (Join-Path $advancedStoragePath 'Storage-Replica-Groups'))
    $storageReplicaPartnerships = @(Get-ReportData -BasePath (Join-Path $advancedStoragePath 'Storage-Replica-Partnerships'))
    $iscsiServerTargets = @(Get-ReportData -BasePath (Join-Path $advancedStoragePath 'iSCSI-Server-Targets'))
    $iscsiVirtualDisks = @(Get-ReportData -BasePath (Join-Path $advancedStoragePath 'iSCSI-Virtual-Disks'))
    $iscsiTargetMappings = @(Get-ReportData -BasePath (Join-Path $advancedStoragePath 'iSCSI-Virtual-Disk-Target-Mappings'))
    $nfsShares = @(Get-ReportData -BasePath (Join-Path $advancedStoragePath 'NFS-Shares'))
    $nfsServerConfiguration = @(Get-ReportData -BasePath (Join-Path $advancedStoragePath 'NFS-Server-Configuration'))
    $nfsClientConfiguration = @(Get-ReportData -BasePath (Join-Path $advancedStoragePath 'NFS-Client-Configuration'))

    $dedupStatusForReport = foreach ($row in $dedupStatus) {
        [pscustomobject]@{
            Volume=$row.Volume; Capacity=(ConvertTo-ReportByteSize $row.Capacity); FreeSpace=(ConvertTo-ReportByteSize $row.FreeSpace); UsedSpace=(ConvertTo-ReportByteSize $row.UsedSpace)
            UnoptimizedSize=(ConvertTo-ReportByteSize $row.UnoptimizedSize); SavedSpace=(ConvertTo-ReportByteSize $row.SavedSpace); SavingsRate=$row.SavingsRate
            OptimizedFilesCount=$row.OptimizedFilesCount; InPolicyFilesCount=$row.InPolicyFilesCount; LastOptimizationTime=$row.LastOptimizationTime
        }
    }
    $storageReplicaGroupsForReport = foreach ($row in $storageReplicaGroups) {
        [pscustomobject]@{ Name=$row.Name; ComputerName=$row.ComputerName; Description=$row.Description; ReplicationMode=$row.ReplicationMode; ReplicationState=$row.ReplicationState; LogSize=(ConvertTo-ReportByteSize $row.LogSizeInBytes); LogSizeBytes=$row.LogSizeInBytes; LogVolume=$row.LogVolume; DataVolume=$row.DataVolume }
    }
    $iscsiVirtualDisksForReport = foreach ($row in $iscsiVirtualDisks) {
        [pscustomobject]@{ Path=$row.Path; DevicePath=$row.DevicePath; Size=(ConvertTo-ReportByteSize $row.Size); SizeBytes=$row.Size; ParentPath=$row.ParentPath; Description=$row.Description; SnapshotIds=$row.SnapshotIds }
    }

    $networkBindings = @(Get-ReportData -BasePath (Join-Path $paths.Network 'Network-Adapter-Bindings'))
    $networkAdvancedProperties = @(Get-ReportData -BasePath (Join-Path $paths.Network 'Network-Adapter-Advanced-Properties'))
    $ipInterfaces = @(Get-ReportData -BasePath (Join-Path $paths.Network 'IP-Interfaces'))
    $connectionProfiles = @(Get-ReportData -BasePath (Join-Path $paths.Network 'Network-Connection-Profiles'))
    $windowsNat = @(Get-ReportData -BasePath (Join-Path $paths.Network 'Windows-NAT'))
    $windowsNatMappings = @(Get-ReportData -BasePath (Join-Path $paths.Network 'Windows-NAT-Static-Mappings'))
    $iscsiConnections = @(Get-ReportData -BasePath (Join-Path $paths.Network 'iSCSI-Connections'))
    $powerShellRemotingEndpoints = @(Get-ReportData -BasePath (Join-Path $paths.Network 'PowerShell-Remoting-Endpoints'))

    $allServices = @(Get-ReportData -BasePath (Join-Path $paths.Software 'Services'))
    $serviceDependencies = @(Get-ReportData -BasePath (Join-Path $paths.Software 'Service-Dependencies'))
    $odbcDrivers = @(Get-ReportData -BasePath (Join-Path $paths.Software 'ODBC-Drivers'))

    $defenderPreferences = @(Get-ReportData -BasePath (Join-Path $paths.Security 'Microsoft-Defender-Preferences'))
    $tlsCipherSuites = @(Get-ReportData -BasePath (Join-Path $paths.Security 'TLS-Cipher-Suites'))
    $tlsEccCurves = @(Get-ReportData -BasePath (Join-Path $paths.Security 'TLS-ECC-Curves'))
    $ipsecRules = @(Get-ReportData -BasePath (Join-Path $paths.Security 'IPsec-Rules'))
    $ipsecMainModeRules = @(Get-ReportData -BasePath (Join-Path $paths.Security 'IPsec-Main-Mode-Rules'))
    $ipsecQuickModeRules = @(Get-ReportData -BasePath (Join-Path $paths.Security 'IPsec-Quick-Mode-Rules'))

    $adDomain = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Active-Directory\Domain'))
    $adForest = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Active-Directory\Forest'))
    $domainControllers = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Active-Directory\Domain-Controllers'))
    $adTrusts = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Active-Directory\Trusts'))
    $adSites = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Active-Directory\AD-Sites'))
    $adSubnets = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Active-Directory\AD-Subnets'))
    $adPasswordPolicy = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Active-Directory\Default-Domain-Password-Policy'))
    $adUsers = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Active-Directory\Deep-Domain-Inventory\Users'))
    $adGroups = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Active-Directory\Deep-Domain-Inventory\Groups'))
    $adMemberships = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Active-Directory\Deep-Domain-Inventory\Direct-Group-Memberships'))
    $adComputers = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Active-Directory\Deep-Domain-Inventory\Computers'))
    $adOus = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Active-Directory\Deep-Domain-Inventory\Organizational-Units'))
    $managedServiceAccounts = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Active-Directory\Deep-Domain-Inventory\Managed-Service-Accounts'))
    $gpoInventory = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Group-Policy\GPO-Inventory'))
    $gpoPermissions = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Group-Policy\GPO-Permissions'))
    $gpoLinks = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Group-Policy\GPO-Link-and-Inheritance-Inventory'))

    $systemEvents = @(Get-ReportData -BasePath (Join-Path $paths.Events ('System-Critical-Error-Warning-Last-' + $EventLogDays + '-Days')))
    $applicationEvents = @(Get-ReportData -BasePath (Join-Path $paths.Events ('Application-Critical-Error-Warning-Last-' + $EventLogDays + '-Days')))
    $recentEvents = @($systemEvents + $applicationEvents | Sort-Object TimeCreated -Descending)

    $recoveryGaps = New-Object System.Collections.ArrayList
    Add-RoleRecoveryGap -List $recoveryGaps -Severity 'Critical' -Requirement 'Current full VM, image, or bare-metal backup' -Reason 'The inventory documents configuration but does not contain the operating system, application data, or server data.' -Action 'Confirm a recent, monitored, restorable backup and record its location and retention.'
    Add-RoleRecoveryGap -List $recoveryGaps -Severity 'Critical' -Requirement 'Passwords, private keys, licenses, and installers' -Reason 'Reusable passwords, private certificate keys, BitLocker recovery keys, and licensing entitlements are intentionally not collected.' -Action 'Confirm these items are stored in approved vaults and repositories.'

    if (($roleNames -contains 'Active Directory Domain Services') -or ($summary.DomainRole -match 'Domain Controller')) {
        Add-RoleRecoveryGap -List $recoveryGaps -Severity 'Critical' -Requirement 'Current domain controller System State backup' -Reason 'AD DS and AD-integrated DNS cannot be safely reconstructed only from inventory tables.' -Action 'Verify a supported System State backup and documented forest or domain recovery procedure.'
    }
    if ($roleNames -contains 'Active Directory Certificate Services') {
        Add-RoleRecoveryGap -List $recoveryGaps -Severity 'Critical' -Requirement 'CA database, CA certificate, and CA private-key backup' -Reason 'The collector records CA configuration but does not export the CA database or private key.' -Action 'Complete and test a supported Certification Authority backup.'
    }
    if ($roleNames -contains 'Microsoft SQL Server') {
        Add-RoleRecoveryGap -List $recoveryGaps -Severity 'Critical' -Requirement 'SQL-native database backups and protected security dependencies' -Reason 'The inventory captures read-only SQL metadata when permissions allow, including databases, jobs, linked servers, principals, file paths, configuration, and backup history. It does not contain database contents, actual backup media, login password hashes, credentials, certificate private keys, or encryption keys.' -Action 'Verify current SQL-native full/differential/log backups as applicable and separately protect required certificates, keys, credentials, master/msdb dependencies, and approved instance-security reconstruction material.'
    }
    if ($roleNames -contains 'Hyper-V') {
        Add-RoleRecoveryGap -List $recoveryGaps -Severity 'Critical' -Requirement 'Hyper-V virtual machine backup or export' -Reason 'The report inventories virtual machines and storage paths but does not contain VHD or VHDX data or guest state.' -Action 'Confirm application-aware VM backups or tested exports for every required VM.'
    }
    if ($roleNames -contains 'IIS Web Server') {
        Add-RoleRecoveryGap -List $recoveryGaps -Severity 'High' -Requirement 'IIS content, application data, secrets, and certificate private keys' -Reason 'The IIS configuration backup does not contain site content, databases, reusable app-pool passwords, or PFX files.' -Action 'Back up site roots, databases, application configuration secrets, and required PFX files.'
    }
    if ($roleNames -contains 'WSUS') {
        Add-RoleRecoveryGap -List $recoveryGaps -Severity 'High' -Requirement 'WSUS restore or rebuild decision' -Reason 'SUSDB, WSUS content, approvals, and reporting history are not contained in this inventory.' -Action 'Document whether WSUS will be restored or rebuilt and preserve required data.'
    }
    if (($roleNames -contains 'Network Policy Server') -and (-not $IncludeSensitiveRoleExports)) {
        Add-RoleRecoveryGap -List $recoveryGaps -Severity 'High' -Requirement 'Complete NPS configuration export' -Reason 'The sensitive export was not selected, so RADIUS shared secrets and complete NPS configuration may be unavailable.' -Action 'Create and encrypt a full NPS export or record all shared secrets in the approved vault.'
    }
    if ($shares.Count -gt 0) {
        Add-RoleRecoveryGap -List $recoveryGaps -Severity 'Critical' -Requirement 'File data backup with NTFS metadata' -Reason 'Share definitions and permissions do not contain the actual data.' -Action 'Confirm file data, ownership, ACLs, timestamps, and required alternate streams are protected by backup.'
    }

    # BEGIN APPROVED ACL EXPORT RECOVERY GAP
    if ($script:aclExportsIncompleteCount -gt 0) {
        Add-RoleRecoveryGap -List $recoveryGaps -Severity 'High' -Requirement 'Complete recursive NTFS ACL export' -Reason ('{0} recursive ACL export(s) were timed out, skipped, failed, or unfinished. Incomplete files are not complete backups.' -f $script:aclExportsIncompleteCount) -Action 'Review the ACL export results and mapping. Obtain and validate complete backups separately; do not restore .incomplete files.'
    }
    # END APPROVED ACL EXPORT RECOVERY GAP

    $restorableArtifacts = New-Object System.Collections.ArrayList
    $artifactDefinitions = @(
        [pscustomobject]@{ Purpose = 'Windows Firewall policy'; Path = (Join-Path $paths.Security 'Windows-Firewall-Policy.wfw'); Caution = 'Review before import on replacement hardware.' },
        [pscustomobject]@{ Purpose = 'Local security policy'; Path = (Join-Path $paths.Security 'Local-Security-Policy.inf'); Caution = 'Review before applying with SecEdit.' },
        [pscustomobject]@{ Purpose = 'Advanced audit policy'; Path = (Join-Path $paths.Security 'Advanced-Audit-Policy.csv'); Caution = 'Import only after validating domain policy precedence.' },
        [pscustomobject]@{ Purpose = 'DHCP configuration and leases'; Path = (Join-Path $paths.Roles 'DHCP\DHCP-Configuration-and-Leases.xml'); Caution = 'Install and authorize DHCP before import.' },
        [pscustomobject]@{ Purpose = 'Print Server migration export'; Path = (Join-Path $paths.Roles ('Print-Server\' + $env:COMPUTERNAME + '.printerExport')); Caution = 'Validate drivers and architecture before restore.' },
        [pscustomobject]@{ Purpose = 'SMB share recreation reference'; Path = (Join-Path $paths.Shares 'SMB-Share-Recreation-REFERENCE.ps1'); Caution = 'Review before use. Does not restore file data or NTFS ACLs by itself.' },
        [pscustomobject]@{ Purpose = 'RRAS routing reconstruction reference'; Path = (Join-Path $paths.Roles 'RRAS\RRAS-Restore-Reference.txt'); Caution = 'Review interface names, routes, VPN/NAT settings, and security before reconstruction.' },
        [pscustomobject]@{ Purpose = 'SQL recovery and restore guidance'; Path = (Join-Path $paths.Roles 'Microsoft-SQL-Server\SQL-Recovery-and-Restore-Guidance.txt'); Caution = 'Metadata and guidance only. This is not a database backup and contains no credentials or private keys.' },
        [pscustomobject]@{ Purpose = 'SQL Server 64-bit registry configuration reference'; Path = (Join-Path $paths.Roles 'Microsoft-SQL-Server\SQL-Server-Registry-64bit.reg'); Caution = 'Configuration reference only. Do not blindly import registry data to a different SQL Server or Windows build.' },
        [pscustomobject]@{ Purpose = 'SQL Server 32-bit registry configuration reference'; Path = (Join-Path $paths.Roles 'Microsoft-SQL-Server\SQL-Server-Registry-32bit.reg'); Caution = 'Configuration reference only. Do not blindly import registry data to a different SQL Server or Windows build.' },
        [pscustomobject]@{ Purpose = 'Sensitive NPS configuration'; Path = (Join-Path $paths.Roles 'NPS\NPS-FULL-CONFIG-SENSITIVE.xml'); Caution = 'May contain plaintext RADIUS shared secrets. Encrypt and restrict.' }
    )
    foreach ($artifact in $artifactDefinitions) {
        if (Test-Path -LiteralPath $artifact.Path) {
            [void]$restorableArtifacts.Add([pscustomobject]@{
                Purpose      = $artifact.Purpose
                RelativePath = Get-ReportRelativePath -Path $artifact.Path -Root $packagePath
                Caution      = $artifact.Caution
            })
        }
    }
    foreach ($directoryArtifact in @(
        [pscustomobject]@{ Purpose = 'DHCP database backup'; Path = (Join-Path $paths.Roles 'DHCP\DHCP-Database-Backup'); Caution = 'Use only with supported DHCP recovery procedures and validate authorization/bindings after restore.' },
        [pscustomobject]@{ Purpose = 'DNS role inventory and zone export files'; Path = (Join-Path $paths.Roles 'DNS'); Caution = 'Review the Zone-* folders. Applicability depends on zone type; AD-integrated zones normally recover through AD rather than file import.' },
        [pscustomobject]@{ Purpose = 'Domain Group Policy backups'; Path = (Join-Path $paths.Roles 'Group-Policy\GPO-Backups'); Caution = 'Restore or import only through supported Group Policy procedures.' },
        [pscustomobject]@{ Purpose = 'IIS configuration backup'; Path = (Join-Path $paths.Roles 'IIS\IIS-Configuration-Backup'); Caution = 'Does not contain site content, databases, secrets, or private keys.' },
        [pscustomobject]@{ Purpose = 'Custom scheduled task XML'; Path = (Join-Path $paths.Software 'Scheduled-Task-XML-Custom'); Caution = 'Task passwords are not included and must be re-entered.' },
        [pscustomobject]@{ Purpose = 'Recursive NTFS ACL backups'; Path = (Join-Path $paths.Shares 'Recursive-ICACLS-Backups'); Caution = 'Review ICACLS-Mapping.csv: only Completed rows reference complete .acl files. Never restore .incomplete files. Test restore against the correct root paths.' },
        [pscustomobject]@{ Purpose = 'Local Group Policy files'; Path = (Join-Path $paths.Security 'Local-Group-Policy-Files'); Caution = 'Review applicability before copying to a replacement server.' },
        [pscustomobject]@{ Purpose = 'Public certificate exports'; Path = (Join-Path $paths.Security 'Public-Certificate-Exports'); Caution = 'Public certificates only. Private keys and PFX files are intentionally not included.' }
    )) {
        if (Test-Path -LiteralPath $directoryArtifact.Path -PathType Container) {
            [void]$restorableArtifacts.Add([pscustomobject]@{
                Purpose      = $directoryArtifact.Purpose
                RelativePath = Get-ReportRelativePath -Path $directoryArtifact.Path -Root $packagePath
                Caution      = $directoryArtifact.Caution
            })
        }
    }

    $platformType = 'Physical server or undetermined'
    if ($summary.IsVirtualMachine) {
        $platformType = 'Virtual machine'
    }

    $identityRows = @(
        [pscustomobject]@{ Field = 'Computer name'; Value = $summary.ComputerName },
        [pscustomobject]@{ Field = 'FQDN'; Value = $summary.FQDN },
        [pscustomobject]@{ Field = 'Domain or workgroup'; Value = $summary.Domain },
        [pscustomobject]@{ Field = 'Domain role'; Value = $summary.DomainRole },
        [pscustomobject]@{ Field = 'Physical or virtual'; Value = $platformType },
        [pscustomobject]@{ Field = 'Manufacturer and model'; Value = (($summary.Manufacturer, $summary.Model) -join ' ') },
        [pscustomobject]@{ Field = 'Serial number'; Value = $summary.SerialNumber },
        [pscustomobject]@{ Field = 'Operating system'; Value = $summary.OperatingSystem },
        [pscustomobject]@{ Field = 'Version and build'; Value = (($summary.Version, $summary.BuildNumber) -join ' / ') },
        [pscustomobject]@{ Field = 'Windows PowerShell'; Value = $summary.PowerShellVersion },
        [pscustomobject]@{ Field = 'Collector compatibility'; Value = $summary.CompatibilityMode },
        [pscustomobject]@{ Field = 'Architecture'; Value = $summary.OSArchitecture },
        [pscustomobject]@{ Field = 'Installed'; Value = $summary.InstallDate },
        [pscustomobject]@{ Field = 'Last boot'; Value = $summary.LastBoot },
        [pscustomobject]@{ Field = 'Memory'; Value = ([string]$summary.TotalMemoryGB + ' GB') },
        [pscustomobject]@{ Field = 'CPU'; Value = ('{0} socket(s), {1} core(s), {2} logical processor(s)' -f $summary.PhysicalProcessorCount, $summary.PhysicalCoreCount, $summary.LogicalProcessorCount) },
        [pscustomobject]@{ Field = 'Collected'; Value = $summary.CollectedAt },
        [pscustomobject]@{ Field = 'Collector version'; Value = $scriptVersion },
        [pscustomobject]@{ Field = 'Package path'; Value = $packagePath }
    )

    $metricRows = @(
        [pscustomobject]@{ Area = 'Collectors completed successfully'; Count = $successfulCollectors.Count; Notes = '' },
        [pscustomobject]@{ Area = 'Collectors failed or unavailable'; Count = $failedCollectors.Count; Notes = 'Review failures before relying on this inventory for recovery, migration, replacement, or rebuild work.' },
        [pscustomobject]@{ Area = 'Detected critical roles and platforms'; Count = $roleRows.Count; Notes = '' },
        [pscustomobject]@{ Area = 'Installed applications'; Count = $applications.Count; Notes = '' },
        [pscustomobject]@{ Area = 'Custom or nonstandard services'; Count = $customServices.Count; Notes = '' },
        [pscustomobject]@{ Area = 'Custom scheduled tasks'; Count = $customTasks.Count; Notes = '' },
        [pscustomobject]@{ Area = 'SMB shares'; Count = $shares.Count; Notes = '' },
        [pscustomobject]@{ Area = 'Local users'; Count = $localUsers.Count; Notes = '' },
        [pscustomobject]@{ Area = 'Local groups'; Count = $localGroups.Count; Notes = '' },
        [pscustomobject]@{ Area = 'Domain users'; Count = $adUsers.Count; Notes = '' },
        [pscustomobject]@{ Area = 'Domain groups'; Count = $adGroups.Count; Notes = '' },
        [pscustomobject]@{ Area = 'Domain computers'; Count = $adComputers.Count; Notes = '' },
        [pscustomobject]@{ Area = 'Group Policy objects'; Count = $gpoInventory.Count; Notes = '' },
        [pscustomobject]@{ Area = 'Certificates inventoried'; Count = $certificates.Count; Notes = 'Private keys are not exported.' },
        [pscustomobject]@{ Area = 'Recent warning, error, and critical events'; Count = $recentEvents.Count; Notes = ('Previous ' + $EventLogDays + ' days') }
    )

    $documentTitle = 'Server Inventory and Configuration Report'
    $documentSubtitle = '{0} | {1} | Collected {2}' -f $summary.ComputerName, $summary.OperatingSystem, (ConvertTo-ReportText -Value $summary.CollectedAt -MaxLength 100)
    Add-ReportTitle -Text $documentTitle -Subtitle $documentSubtitle
    Add-ReportParagraph -Text 'This is the primary engineering reference for documenting and reviewing this server configuration. It can also assist with troubleshooting, migration, recovery, replacement, and rebuild work. Use the supporting files when a restorable import, exact raw value, or deeper verification is required.' -Bold
    Add-ReportParagraph -Text 'This report does not replace System State, bare-metal, virtual machine, database, file-data, private-key, password-vault, or application-native backups.' -Warning

    Add-ReportHeading -Text 'Report Contents' -Level 1
    Add-ReportBullets -Items @(
        'Executive Summary and collection health',
        'Recovery, migration, replacement, and rebuild guidance and prerequisites',
        'Operating system, hardware, storage, and network baseline',
        'Applications, services, scheduled tasks, accounts, policy, and security',
        'Shares, permissions, certificates, and role-specific configuration',
        'Recent event history, recovery checklist, and collection appendix'
    )

    Add-ReportHeading -Text '1. Executive Summary' -Level 1 -PageBreakBefore
    Add-ReportTable -Title 'Server identity and baseline' -Rows $identityRows -Columns @('Field', 'Value') -MaxRows 100
    Add-ReportTable -Title 'Inventory at a glance' -Rows $metricRows -Columns @('Area', 'Count', 'Notes') -MaxRows 100
    Add-ReportTable -Title 'Selected inventory categories' -Rows $selectedInventoryRows -Columns @('Category', 'Purpose', 'Mode') -MaxRows 200
    Add-ReportTable -Title 'Selected backup and export modules' -Rows $selectedExportRows -Columns @('Category', 'Purpose', 'Mode') -MaxRows 200 -EmptyMessage 'No optional backup/export modules were selected. Inventory collection was read-only.'
    Add-ReportTable -Title 'Detected critical roles and platforms' -Rows $roleRows -Columns @('Role', 'DetectedBy', 'Notes') -MaxRows 200
    Add-ReportTable -Title 'Collection failures requiring review' -Rows $failedCollectors -Columns @('Collector', 'Status', 'ErrorMessage') -MaxRows 500 -EmptyMessage 'No collector failures were recorded before consolidated report generation.'
    Add-ReportTable -Title 'Recovery prerequisites and gaps' -Rows $recoveryGaps -Columns @('Severity', 'Requirement', 'Reason', 'Action') -MaxRows 200
    Add-ReportTable -Title 'Restorable or reusable artifacts generated' -Rows $restorableArtifacts -Columns @('Purpose', 'RelativePath', 'Caution') -MaxRows 300

    Add-ReportHeading -Text '2. Recovery, Migration, and Rebuild Guidance' -Level 1 -PageBreakBefore
    Add-ReportParagraph -Text 'The sequence below is a recovery, migration, replacement, and rebuild reference, not the sole purpose of this inventory. Role-specific restore procedures, backup age, replication state, vendor requirements, and change control must be validated before execution.' -Warning
    Add-ReportBullets -Items @(
        '1. Confirm business owner, technical owner, server purpose, recovery point objective, recovery time objective, maintenance window, and rollback authority.',
        '2. Verify all required backups, passwords, private keys, licenses, installers, database exports, and application data before changing or retiring the original server.',
        '3. Build replacement hardware or virtual machine with matching firmware mode, CPU and memory capacity, storage layout, and Windows Server edition.',
        '4. Patch the operating system, configure time, locale, hostname, domain membership, network adapters, IP addresses, DNS, routes, teaming, storage, iSCSI, and MPIO.',
        '5. Install the documented Windows roles, features, management tools, drivers, runtimes, applications, and ODBC components.',
        '6. Restore or recreate role configuration in dependency order. Use supported AD DS, DNS, DHCP, IIS, Hyper-V, clustering, NPS, AD CS, SQL, DFS, FSRM, and print procedures as applicable.',
        '7. Restore application data and file data, then reapply share permissions, NTFS permissions, services, service accounts, scheduled tasks, firewall rules, certificates, and local policy.',
        '8. Validate event logs, services, listening ports, name resolution, authentication, application workflows, backups, monitoring, security tooling, and downstream integrations.',
        '9. Retain the original server or a recoverable backup until acceptance criteria and post-change validation are complete.'
    )
    Add-ReportTable -Title 'Detected role recovery, migration, and restore reference' -Rows $roleRecoveryReference -Columns @('Role','Services','Ports','ImportantPathsAndData','BackupExportStatus','RebuildMigrationConsiderations','RestoreImportGuidance','PostRestoreVerification') -MaxRows 200 -EmptyMessage 'No major server roles were positively detected.'
    Add-ReportParagraph -Text 'Restore/import guidance is intentionally advisory. The application does not automatically execute restore operations. Use vendor- and Microsoft-supported restore procedures, change control, and validated backups.' -Warning

    Add-ReportHeading -Text '3. Operating System and Platform Baseline' -Level 1 -PageBreakBefore
    Add-ReportTable -Title 'Installed server roles' -Rows $installedRoles -Columns @('DisplayName', 'Name', 'InstallState', 'FeatureType', 'Path') -MaxRows 1000
    Add-ReportTable -Title 'Installed Windows roles and features' -Rows $installedFeaturesReport -Columns @('DisplayName', 'Name', 'InstallState', 'FeatureType', 'Path') -MaxRows 3000
    Add-ReportTable -Title 'Locale and time configuration' -Rows $locale -Columns @('TimeZone', 'Culture', 'UICulture', 'SystemLocale', 'HomeLocation') -MaxRows 20
    Add-ReportTable -Title 'Pending reboot indicators' -Rows $pendingRebootIndicators -Columns @('Indicator','Pending','Detail') -MaxRows 50
    Add-ReportTable -Title 'Windows Update configuration' -Rows $windowsUpdateConfiguration -Columns @('RegistryPath','Setting','Value') -MaxRows 500
    Add-ReportTable -Title 'Installed hotfixes' -Rows ($hotfixes | Sort-Object InstalledOn -Descending) -Columns @('HotFixID', 'Description', 'InstalledBy', 'InstalledOn') -MaxRows 1000
    Add-ReportTable -Title 'Windows Update history' -Rows ($windowsUpdateHistory | Sort-Object Date -Descending) -Columns @('Date','Title','Operation','ResultCode','HResult','ClientApplicationID') -MaxRows 500
    Add-ReportTable -Title 'Pagefile settings' -Rows $pagefileSettings -Columns @('Name', 'InitialSize', 'MaximumSize', 'SettingID') -MaxRows 100
    Add-ReportTable -Title 'Startup commands' -Rows $startupCommands -Columns @('Name', 'Command', 'Location', 'User', 'Caption', 'Description') -MaxRows 3000
    Add-ReportTable -Title 'Machine environment variables' -Rows $machineEnvironmentForReport -Columns @('Name', 'Value') -MaxRows 1000
    Add-ReportParagraph -Text 'Values for environment-variable names that indicate passwords, tokens, credentials, connection strings, or private keys are omitted from the consolidated report. Verify those values through the approved secure source.' -Warning
    Add-ReportTable -Title 'Windows Server Backup policy' -Rows $windowsBackupPolicy -Columns @('VolumesToBackup', 'FilesSpecsToBackup', 'FilesSpecsToExclude', 'BMR', 'SystemState', 'VssBackupOptions') -MaxRows 100
    Add-ReportTable -Title 'Windows Server Backup schedule' -Rows $windowsBackupSchedule -Columns @('Hour', 'Minute', 'Second', 'DayOfWeek') -MaxRows 100
    Add-ReportTable -Title 'Windows Server Backup summary' -Rows $windowsBackupSummary -Columns @('LastSuccessfulBackupTime', 'LastBackupTime', 'LastBackupResultHR', 'NextBackupTime', 'DetailedMessage') -MaxRows 100
    Add-ReportPreformatted -Title 'WBAdmin current status' -Text (Get-ReportFileText -Path (Join-Path $paths.System 'WBAdmin-Current-Status.txt') -MaxLength 15000) -MaxLength 15000
    Add-ReportPreformatted -Title 'WBAdmin available backup versions' -Text (Get-ReportFileText -Path (Join-Path $paths.System 'WBAdmin-Backup-Versions.txt') -MaxLength 30000) -MaxLength 30000
    Add-ReportPreformatted -Title 'Boot configuration' -Text (Get-ReportFileText -Path (Join-Path $paths.System 'Boot-Configuration.txt') -MaxLength 30000) -MaxLength 30000
    Add-ReportPreformatted -Title 'Windows recovery environment' -Text (Get-ReportFileText -Path (Join-Path $paths.System 'Windows-Recovery-Configuration.txt') -MaxLength 15000) -MaxLength 15000

    Add-ReportHeading -Text '4. Hardware and Firmware' -Level 1 -PageBreakBefore
    Add-ReportTable -Title 'Processors' -Rows $processorsReport -Columns @('Name', 'Manufacturer', 'SocketDesignation', 'NumberOfCores', 'NumberOfLogicalProcessors', 'MaxClockSpeed', 'VirtualizationFirmwareEnabled') -MaxRows 100
    Add-ReportTable -Title 'Physical memory modules' -Rows $memoryModules -Columns @('BankLabel', 'DeviceLocator', 'Manufacturer', 'PartNumber', 'SerialNumber', 'Capacity', 'Speed', 'ConfiguredClockSpeed') -MaxRows 500
    Add-ReportTable -Title 'BIOS and firmware' -Rows (Get-ReportData -BasePath (Join-Path $paths.Hardware 'BIOS')) -Columns @('Manufacturer', 'Name', 'SMBIOSBIOSVersion', 'Version', 'ReleaseDate', 'SerialNumber') -MaxRows 20
    Add-ReportTable -Title 'Baseboard' -Rows (Get-ReportData -BasePath (Join-Path $paths.Hardware 'Baseboard')) -Columns @('Manufacturer', 'Product', 'Version', 'SerialNumber') -MaxRows 20
    Add-ReportTable -Title 'System enclosure' -Rows (Get-ReportData -BasePath (Join-Path $paths.Hardware 'System-Enclosure')) -Columns @('Manufacturer', 'Model', 'Name', 'SerialNumber', 'SMBIOSAssetTag', 'ChassisTypes', 'SecurityStatus') -MaxRows 20
    Add-ReportTable -Title 'Memory arrays' -Rows (Get-ReportData -BasePath (Join-Path $paths.Hardware 'Memory-Arrays')) -Columns @('Location', 'Use', 'MemoryDevices', 'MaxCapacity', 'MaxCapacityEx') -MaxRows 100
    Add-ReportTable -Title 'Signed device drivers' -Rows ($signedDrivers | Sort-Object DeviceClass, DeviceName) -Columns @('DeviceName', 'DeviceClass', 'Manufacturer', 'DriverProviderName', 'DriverVersion', 'DriverDate', 'InfName', 'IsSigned', 'Signer') -MaxRows 10000
    Add-ReportTable -Title 'Plug and Play devices' -Rows ($pnpDevices | Sort-Object PNPClass, Name) -Columns @('Name', 'PNPClass', 'Manufacturer', 'Service', 'Status', 'ConfigManagerErrorCode', 'PNPDeviceID') -MaxRows 10000

    Add-ReportHeading -Text '5. Storage Configuration' -Level 1 -PageBreakBefore
    Add-ReportTable -Title 'Volumes and free space' -Rows $volumes -Columns @('DriveLetter', 'Drive', 'Path', 'Label', 'FileSystem', 'Health', 'Status', 'Capacity', 'Used', 'Free', 'PercentFree', 'DriveType', 'CapacityBytes', 'FreeBytes') -MaxRows 3000
    Add-ReportTable -Title 'Physical and operating-system disks' -Rows $disks -Columns @('Number', 'FriendlyName', 'Model', 'SerialNumber', 'BusType', 'InterfaceType', 'PartitionStyle', 'Health', 'Status', 'Capacity', 'CapacityBytes') -MaxRows 1000
    Add-ReportTable -Title 'Partitions' -Rows $partitions -Columns @('DiskNumber', 'PartitionNumber', 'DriveLetter', 'Type', 'IsActive', 'IsBoot', 'IsSystem', 'Size', 'Offset', 'SizeBytes', 'OffsetBytes') -MaxRows 3000
    Add-ReportTable -Title 'Storage pools' -Rows $storagePools -Columns @('FriendlyName', 'Status', 'Health', 'IsPrimordial', 'Capacity', 'Allocated', 'CapacityBytes', 'AllocatedBytes') -MaxRows 500
    Add-ReportTable -Title 'Physical disks' -Rows $physicalDisks -Columns @('FriendlyName', 'Model', 'SerialNumber', 'MediaType', 'BusType', 'CanPool', 'Status', 'Health', 'Capacity', 'PhysicalSectorSize', 'LogicalSectorSize', 'CapacityBytes') -MaxRows 3000
    Add-ReportTable -Title 'Virtual disks' -Rows $virtualDisks -Columns @('FriendlyName', 'ResiliencySettingName', 'ProvisioningType', 'Health', 'Status', 'Capacity', 'Footprint', 'CapacityBytes', 'FootprintBytes') -MaxRows 500
    Add-ReportTable -Title 'MPIO automatic claim settings' -Rows $mpioClaimSettings -Columns @('BusType', 'Enabled') -MaxRows 100
    Add-ReportTable -Title 'Data Deduplication volumes' -Rows $dedupVolumes -Columns @('Volume', 'Enabled', 'UsageType', 'MinimumFileAgeDays', 'MinimumFileSize', 'ExcludeFolder', 'ExcludeFileType') -MaxRows 3000
    Add-ReportTable -Title 'Data Deduplication schedules' -Rows $dedupSchedules -Columns @('Name', 'Type', 'Enabled', 'Days', 'Start', 'DurationHours', 'Priority', 'StopWhenSystemBusy') -MaxRows 1000
    Add-ReportTable -Title 'Data Deduplication status' -Rows $dedupStatusForReport -Columns @('Volume', 'Capacity', 'FreeSpace', 'UsedSpace', 'UnoptimizedSize', 'SavedSpace', 'SavingsRate', 'OptimizedFilesCount', 'InPolicyFilesCount', 'LastOptimizationTime') -MaxRows 3000
    Add-ReportTable -Title 'Storage Replica groups' -Rows $storageReplicaGroupsForReport -Columns @('Name', 'ComputerName', 'Description', 'ReplicationMode', 'ReplicationState', 'LogSize', 'LogVolume', 'DataVolume', 'LogSizeBytes') -MaxRows 3000
    Add-ReportTable -Title 'Storage Replica partnerships' -Rows $storageReplicaPartnerships -Columns @('SourceComputerName', 'SourceRGName', 'DestinationComputerName', 'DestinationRGName', 'ReplicationMode', 'ReplicationStatus', 'LastSyncTime') -MaxRows 3000
    Add-ReportTable -Title 'iSCSI target portals' -Rows $iscsiPortals -Columns @('TargetPortalAddress', 'TargetPortalPortNumber', 'InitiatorInstanceName', 'IsDataDigest', 'IsHeaderDigest') -MaxRows 500
    Add-ReportTable -Title 'iSCSI sessions' -Rows $iscsiSessions -Columns @('TargetNodeAddress', 'TargetNodeName', 'InitiatorInstanceName', 'InitiatorPortalAddress', 'IsConnected', 'SessionIdentifier') -MaxRows 1000
    Add-ReportTable -Title 'iSCSI connections' -Rows $iscsiConnections -Columns @('ConnectionIdentifier', 'SessionIdentifier', 'InitiatorAddress', 'TargetAddress', 'TargetPortNumber', 'IsConnected') -MaxRows 3000
    Add-ReportTable -Title 'iSCSI Server targets' -Rows $iscsiServerTargets -Columns @('TargetName', 'InitiatorIds', 'IsConnected', 'Status', 'EnableChap', 'EnableReverseChap', 'Description') -MaxRows 3000
    Add-ReportTable -Title 'iSCSI virtual disks' -Rows $iscsiVirtualDisksForReport -Columns @('Path', 'DevicePath', 'Size', 'ParentPath', 'Description', 'SnapshotIds', 'SizeBytes') -MaxRows 3000
    Add-ReportTable -Title 'iSCSI target mappings' -Rows $iscsiTargetMappings -Columns @('TargetName', 'Path', 'Lun') -MaxRows 5000
    Add-ReportTable -Title 'NFS shares' -Rows $nfsShares -Columns @('Name', 'Path', 'NetworkName', 'Authentication', 'EnableUnmappedAccess', 'AnonymousUid', 'AnonymousGid') -MaxRows 3000
    Add-ReportTable -Title 'NFS server configuration' -Rows $nfsServerConfiguration -Columns @('State', 'LogActivity', 'CharacterTranslationFile', 'DirectoryCacheSize', 'HideFilesBeginningInDot') -MaxRows 100
    Add-ReportTable -Title 'NFS client configuration' -Rows $nfsClientConfiguration -Columns @('State', 'MountType', 'CaseSensitiveLookup', 'DefaultAccessMode', 'RetryCount', 'TimeoutSec') -MaxRows 100

    Add-ReportHeading -Text '6. Network Configuration' -Level 1 -PageBreakBefore

    Add-ReportHeading -Text '6.1 IP Addressing and DNS' -Level 2
    Add-ReportTable -Title 'IP addresses' -Rows ($ipAddresses | Where-Object { $_.IPAddress -notmatch '^fe80:' }) -Columns @('InterfaceAlias', 'AddressFamily', 'IPAddress', 'PrefixLength', 'PrefixOrigin', 'SuffixOrigin', 'AddressState', 'SkipAsSource') -MaxRows 3000
    Add-ReportTable -Title 'DNS client server addresses' -Rows $dnsClients -Columns @('InterfaceAlias', 'InterfaceIndex', 'AddressFamily', 'ServerAddresses') -MaxRows 1000
    Add-ReportTable -Title 'IP interface configuration' -Rows $ipInterfaces -Columns @('InterfaceAlias', 'AddressFamily', 'ConnectionState', 'Dhcp', 'NlMtuBytes', 'InterfaceMetric', 'RouterDiscovery', 'WeakHostSend', 'WeakHostReceive') -MaxRows 3000
    Add-ReportTable -Title 'Network connection profiles' -Rows $connectionProfiles -Columns @('Name', 'InterfaceAlias', 'InterfaceIndex', 'NetworkCategory', 'IPv4Connectivity', 'IPv6Connectivity') -MaxRows 1000
    Add-ReportPreformatted -Title 'Hosts file' -Text (Get-ReportFileText -Path (Join-Path $paths.Network 'Hosts-File.txt') -MaxLength 30000) -MaxLength 30000
    Add-ReportPreformatted -Title 'IPConfig /all' -Text (Get-ReportFileText -Path (Join-Path $paths.Network 'IPConfig-All.txt') -MaxLength 50000) -MaxLength 50000

    Add-ReportHeading -Text '6.2 Routing, Gateways, NAT, and Proxies' -Level 2
    Add-ReportTable -Title 'Routes and default gateways' -Rows ($routes | Where-Object { ($_.DestinationPrefix -eq '0.0.0.0/0') -or ($_.DestinationPrefix -eq '::/0') -or ([string]$_.Protocol -match 'NetMgmt|Static') }) -Columns @('DestinationPrefix', 'NextHop', 'InterfaceAlias', 'RouteMetric', 'Protocol', 'PolicyStore') -MaxRows 3000
    Add-ReportTable -Title 'Windows NAT instances' -Rows $windowsNat -Columns @('Name', 'ExternalIPInterfaceAddressPrefix', 'InternalIPInterfaceAddressPrefix', 'IcmpQueryTimeout', 'TcpEstablishedConnectionTimeout', 'UdpIdleSessionTimeout') -MaxRows 1000
    Add-ReportTable -Title 'Windows NAT static mappings' -Rows $windowsNatMappings -Columns @('NatName', 'Protocol', 'ExternalIPAddress', 'ExternalPort', 'InternalIPAddress', 'InternalPort', 'RemoteExternalIPAddressPrefix') -MaxRows 3000
    Add-ReportPreformatted -Title 'Route print' -Text (Get-ReportFileText -Path (Join-Path $paths.Network 'Route-Print.txt') -MaxLength 50000) -MaxLength 50000
    Add-ReportPreformatted -Title 'WinHTTP proxy' -Text (Get-ReportFileText -Path (Join-Path $paths.Network 'WinHTTP-Proxy.txt') -MaxLength 15000) -MaxLength 15000
    Add-ReportPreformatted -Title 'Port proxy configuration' -Text (Get-ReportFileText -Path (Join-Path $paths.Network 'Port-Proxy.txt') -MaxLength 15000) -MaxLength 15000

    Add-ReportHeading -Text '6.3 Network Adapters and Teaming' -Level 2
    Add-ReportTable -Title 'Network adapters' -Rows $networkAdapters -Columns @('Name', 'InterfaceDescription', 'Status', 'MacAddress', 'LinkSpeed', 'MediaType', 'DriverDescription', 'DriverVersion', 'VlanID') -MaxRows 1000
    Add-ReportTable -Title 'NIC teams' -Rows $networkTeams -Columns @('Name', 'Status', 'TeamingMode', 'LoadBalancingAlgorithm', 'PrimaryAdapter') -MaxRows 100
    Add-ReportTable -Title 'Switch Embedded Teams' -Rows $switchTeams -Columns @('Name', 'NetAdapterInterfaceDescription', 'NetAdapterInterfaceGuid') -MaxRows 100

    Add-ReportHeading -Text '6.4 Listening Services and Remote Management' -Level 2
    Add-ReportTable -Title 'TCP listening endpoints' -Rows ($tcpListeners | Sort-Object LocalPort, ProcessName) -Columns @('LocalAddress', 'LocalPort', 'OwningProcess', 'ProcessName') -MaxRows 3000
    Add-ReportTable -Title 'UDP listening endpoints' -Rows ($udpListeners | Sort-Object LocalPort, ProcessName) -Columns @('LocalAddress', 'LocalPort', 'OwningProcess', 'ProcessName') -MaxRows 3000
    Add-ReportTable -Title 'PowerShell remoting endpoints' -Rows $powerShellRemotingEndpoints -Columns @('Name', 'PSVersion', 'StartupScript', 'RunAsUser', 'Permission', 'Architecture', 'Filename') -MaxRows 1000
    Add-ReportPreformatted -Title 'WinRM listeners' -Text (Get-ReportFileText -Path (Join-Path $paths.Network 'WinRM-Listeners.txt') -MaxLength 30000) -MaxLength 30000
    Add-ReportPreformatted -Title 'WinRM service configuration' -Text (Get-ReportFileText -Path (Join-Path $paths.Network 'WinRM-Service-Configuration.txt') -MaxLength 30000) -MaxLength 30000
    Add-ReportPreformatted -Title 'HTTP.sys SSL certificate bindings' -Text (Get-ReportFileText -Path (Join-Path $paths.Network 'HTTP-SYS-SSL-Certificate-Bindings.txt') -MaxLength 30000) -MaxLength 30000
    Add-ReportPreformatted -Title 'HTTP.sys URL reservations' -Text (Get-ReportFileText -Path (Join-Path $paths.Network 'HTTP-SYS-URL-Reservations.txt') -MaxLength 30000) -MaxLength 30000

    Add-ReportHeading -Text '6.5 Advanced Adapter Configuration' -Level 2
    Add-ReportTable -Title 'Network adapter advanced properties' -Rows $networkAdvancedProperties -Columns @('Name', 'DisplayName', 'DisplayValue', 'RegistryKeyword', 'RegistryValue') -MaxRows 5000
    Add-ReportTable -Title 'Network adapter bindings' -Rows $networkBindings -Columns @('Name', 'DisplayName', 'ComponentID', 'Enabled') -MaxRows 5000

    Add-ReportHeading -Text '7. Installed Applications and Data Connectivity' -Level 1 -PageBreakBefore
    Add-ReportTable -Title 'Installed applications' -Rows $applications -Columns @('DisplayName', 'DisplayVersion', 'Publisher', 'InstallDate', 'InstallLocation', 'InstallSource', 'Architecture', 'UninstallString') -MaxRows 5000
    Add-ReportTable -Title 'ODBC data sources' -Rows $odbcDataSources -Columns @('Name', 'DsnType', 'Platform', 'DriverName', 'Attribute') -MaxRows 2000
    Add-ReportTable -Title 'ODBC drivers' -Rows $odbcDrivers -Columns @('Name', 'Platform', 'Attribute') -MaxRows 2000
    Add-ReportTable -Title 'Application folder inventory' -Rows (Get-ReportData -BasePath (Join-Path $paths.Software 'Application-Folder-Inventory')) -Columns @('Root', 'Name', 'FullName', 'CreationTime', 'LastWriteTime') -MaxRows 3000

    Add-ReportHeading -Text '8. Services and Scheduled Tasks' -Level 1 -PageBreakBefore
    Add-ReportTable -Title 'All Windows services' -Rows $allServices -Columns @('Name', 'DisplayName', 'State', 'StartMode', 'StartName', 'PathName', 'Description', 'ProcessId', 'ExitCode') -MaxRows 10000
    Add-ReportTable -Title 'Service dependencies' -Rows $serviceDependencies -Columns @('Name', 'DisplayName', 'Status', 'ServicesDependedOn', 'DependentServices') -MaxRows 20000
    Add-ReportTable -Title 'Custom or nonstandard service configuration' -Rows $customServices -Columns @('Name', 'DisplayName', 'State', 'StartMode', 'StartName', 'PathName', 'DelayedAutoStart', 'DependOnService', 'Description') -MaxRows 3000
    Add-ReportTable -Title 'All scheduled tasks' -Rows $scheduledTasks -Columns @('TaskPath', 'TaskName', 'State', 'PrincipalUser', 'LogonType', 'RunLevel', 'Actions', 'Triggers', 'LastRunTime', 'NextRunTime', 'LastTaskResult') -MaxRows 10000
    Add-ReportTable -Title 'Custom scheduled tasks' -Rows $customTasks -Columns @('TaskPath', 'TaskName', 'State', 'PrincipalUser', 'LogonType', 'RunLevel', 'Actions', 'Triggers', 'LastRunTime', 'NextRunTime', 'LastTaskResult') -MaxRows 5000
    Add-ReportParagraph -Text 'Reusable service-account and scheduled-task passwords are not available in this report. Confirm all credentials in the approved password vault before rebuilding.' -Warning

    Add-ReportHeading -Text '9. Accounts, Policy, and Security' -Level 1 -PageBreakBefore
    Add-ReportTable -Title 'Local users' -Rows $localUsers -Columns @('Name', 'FullName', 'Enabled', 'Description', 'PasswordRequired', 'PasswordExpires', 'UserMayChangePassword', 'LastLogon', 'SID') -MaxRows 3000
    Add-ReportTable -Title 'Local groups' -Rows $localGroups -Columns @('Name', 'Description', 'SID') -MaxRows 3000
    Add-ReportTable -Title 'Local group memberships' -Rows $localMemberships -Columns @('GroupName', 'MemberName', 'ObjectClass', 'PrincipalSource', 'SID', 'Error') -MaxRows 10000
    Add-ReportPreformatted -Title 'Local account policy' -Text (Get-ReportFileText -Path (Join-Path $paths.Security 'Local-Account-Policy.txt') -MaxLength 15000) -MaxLength 15000
    Add-ReportPreformatted -Title 'Applied computer Group Policy result' -Text (Get-ReportFileText -Path (Join-Path $paths.Security 'GPResult-Computer-Readable.txt') -MaxLength 50000) -MaxLength 50000
    Add-ReportPreformatted -Title 'Local security policy export' -Text (Get-ReportFileText -Path (Join-Path $paths.Security 'Local-Security-Policy.inf') -MaxLength 50000) -MaxLength 50000
    Add-ReportPreformatted -Title 'Advanced audit policy' -Text (Get-ReportFileText -Path (Join-Path $paths.Security 'Advanced-Audit-Policy-Readable.csv') -MaxLength 50000) -MaxLength 50000
    Add-ReportTable -Title 'Windows Firewall profiles' -Rows $firewallProfiles -Columns @('Name', 'Enabled', 'DefaultInboundAction', 'DefaultOutboundAction', 'AllowInboundRules', 'AllowLocalFirewallRules', 'LogFileName', 'LogMaxSizeKilobytes') -MaxRows 20
    Add-ReportTable -Title 'Enabled Windows Firewall rules' -Rows $enabledFirewallRules -Columns @('DisplayName', 'Direction', 'Action', 'Profile', 'PolicyStoreSourceType', 'PrimaryStatus', 'Status', 'Description') -MaxRows 5000
    Add-ReportTable -Title 'Microsoft Defender status' -Rows $defenderStatus -Columns @('AMServiceEnabled', 'AntivirusEnabled', 'AntispywareEnabled', 'BehaviorMonitorEnabled', 'RealTimeProtectionEnabled', 'OnAccessProtectionEnabled', 'NISEnabled', 'AntivirusSignatureLastUpdated', 'QuickScanEndTime', 'FullScanEndTime') -MaxRows 20
    Add-ReportPreformatted -Title 'Microsoft Defender preferences' -Text (Get-ReportFileText -Path (Join-Path $paths.Security 'Microsoft-Defender-Preferences.txt') -MaxLength 50000) -MaxLength 50000
    Add-ReportTable -Title 'TLS cipher suites' -Rows $tlsCipherSuites -Columns @('Name', 'CipherSuite', 'Cipher', 'Hash', 'Exchange', 'Certificate', 'Protocols') -MaxRows 1000
    Add-ReportTable -Title 'TLS elliptic curves' -Rows $tlsEccCurves -Columns @('Name', 'CurveName', 'Type', 'Length') -MaxRows 1000
    Add-ReportTable -Title 'IPsec rules' -Rows $ipsecRules -Columns @('DisplayName', 'Enabled', 'Profile', 'Mode', 'InboundSecurity', 'OutboundSecurity', 'Phase1AuthSet', 'Phase2AuthSet', 'QuickModeCryptoSet') -MaxRows 5000
    Add-ReportTable -Title 'IPsec main mode rules' -Rows $ipsecMainModeRules -Columns @('DisplayName', 'Enabled', 'Profile', 'MainModeCryptoSet', 'Phase1AuthSet', 'PrimaryStatus', 'Status') -MaxRows 5000
    Add-ReportTable -Title 'IPsec quick mode rules' -Rows $ipsecQuickModeRules -Columns @('DisplayName', 'Enabled', 'Profile', 'QuickModeCryptoSet', 'Phase2AuthSet', 'PrimaryStatus', 'Status') -MaxRows 5000
    Add-ReportPreformatted -Title 'SCHANNEL registry configuration' -Text (Get-ReportFileText -Path (Join-Path $paths.Security 'SCHANNEL-Registry.reg') -MaxLength 50000) -MaxLength 50000
    Add-ReportPreformatted -Title 'Remote Desktop registry configuration' -Text (Get-ReportFileText -Path (Join-Path $paths.Security 'Remote-Desktop-Registry.reg') -MaxLength 50000) -MaxLength 50000
    Add-ReportTable -Title 'BitLocker status' -Rows $bitLocker -Columns @('MountPoint', 'VolumeType', 'CapacityGB', 'VolumeStatus', 'EncryptionPercentage', 'EncryptionMethod', 'ProtectionStatus', 'LockStatus', 'AutoUnlockEnabled') -MaxRows 100
    Add-ReportTable -Title 'Local machine certificates' -Rows ($certificates | Sort-Object StorePath, NotAfter, Subject) -Columns @('StorePath', 'Subject', 'Issuer', 'Thumbprint', 'NotBefore', 'NotAfter', 'HasPrivateKey', 'EnhancedKeyUsage', 'DNSNames', 'FriendlyName') -MaxRows 10000
    Add-ReportParagraph -Text 'Certificate private keys and PFX files are not exported. Any certificate showing HasPrivateKey as True must be reviewed for a separate protected PFX or role-specific key backup.' -Warning

    Add-ReportHeading -Text '10. Shares and Permissions' -Level 1 -PageBreakBefore
    Add-ReportTable -Title 'SMB server configuration' -Rows $smbServerConfiguration -Columns @('EnableSMB1Protocol', 'EnableSMB2Protocol', 'EnableSecuritySignature', 'RequireSecuritySignature', 'EncryptData', 'RejectUnencryptedAccess', 'EnableMultiChannel', 'EnableLeasing', 'EnableOplocks', 'AutoDisconnectTimeout', 'MaxSessionPerConnection') -MaxRows 100
    Add-ReportTable -Title 'SMB shares' -Rows $shares -Columns @('Name', 'Path', 'Description', 'ScopeName', 'Special', 'Temporary', 'FolderEnumerationMode', 'CachingMode', 'EncryptData', 'ContinuouslyAvailable') -MaxRows 3000
    Add-ReportTable -Title 'SMB share permissions' -Rows $sharePermissions -Columns @('ShareName', 'AccountName', 'AccessControlType', 'AccessRight', 'Error') -MaxRows 10000
    Add-ReportTable -Title 'Share-root NTFS permissions' -Rows $rootNtfs -Columns @('ShareName', 'Path', 'Owner', 'IdentityReference', 'AccessControlType', 'FileSystemRights', 'InheritanceFlags', 'PropagationFlags', 'IsInherited', 'Error') -MaxRows 20000
    # BEGIN APPROVED ACL EXPORT REPORT TABLE
    Add-ReportTable -Title 'Recursive NTFS ACL export results' -Rows @($script:aclExportResults) -Columns @('ShareName', 'SourcePath', 'Status', 'ElapsedSeconds', 'TimeoutSeconds', 'ExitCode', 'AclFile', 'Note') -MaxRows 3000 -EmptyMessage 'Recursive ACL exports were not selected, or no eligible shares were present.'
    if ($script:aclExportsIncompleteCount -gt 0) { Add-ReportParagraph -Text 'One or more recursive ACL exports are incomplete. See the per-share results and ICACLS-Mapping.csv before considering any restore.' -Warning }
    # END APPROVED ACL EXPORT REPORT TABLE
    Add-ReportParagraph -Text 'Share definitions and permissions are not file-data backups. Confirm data backup and restore testing before relying on this inventory.' -Warning

    Add-ReportHeading -Text '11. Role-Specific Configuration' -Level 1 -PageBreakBefore

    if (($adDomain.Count + $adUsers.Count + $gpoInventory.Count) -gt 0) {
        Add-ReportHeading -Text '11.1 Active Directory Domain Services' -Level 2
        Add-ReportTable -Title 'Domain' -Rows $adDomain -Columns @('DNSRoot', 'NetBIOSName', 'DomainMode', 'DomainSID', 'InfrastructureMaster', 'PDCEmulator', 'RIDMaster', 'DistinguishedName') -MaxRows 20
        Add-ReportTable -Title 'Forest' -Rows $adForest -Columns @('Name', 'ForestMode', 'RootDomain', 'SchemaMaster', 'DomainNamingMaster', 'Domains', 'GlobalCatalogs', 'Sites') -MaxRows 20
        Add-ReportTable -Title 'Domain controllers' -Rows $domainControllers -Columns @('HostName', 'Site', 'IPv4Address', 'IPv6Address', 'OperatingSystem', 'OperatingSystemVersion', 'IsGlobalCatalog', 'IsReadOnly', 'OperationMasterRoles') -MaxRows 500
        Add-ReportTable -Title 'Trusts' -Rows $adTrusts -Columns @('Name', 'Source', 'Target', 'Direction', 'TrustType', 'ForestTransitive', 'IntraForest', 'SelectiveAuthentication') -MaxRows 500
        Add-ReportTable -Title 'Sites' -Rows $adSites -Columns @('Name', 'Description', 'DistinguishedName') -MaxRows 1000
        Add-ReportTable -Title 'Subnets' -Rows $adSubnets -Columns @('Name', 'Site', 'Location', 'DistinguishedName') -MaxRows 5000
        Add-ReportTable -Title 'Default domain password policy' -Rows $adPasswordPolicy -Columns @('ComplexityEnabled', 'LockoutDuration', 'LockoutObservationWindow', 'LockoutThreshold', 'MaxPasswordAge', 'MinPasswordAge', 'MinPasswordLength', 'PasswordHistoryCount', 'ReversibleEncryptionEnabled') -MaxRows 20
        Add-ReportTable -Title 'Domain users' -Rows $adUsers -Columns @('Name', 'SamAccountName', 'UserPrincipalName', 'Enabled', 'DistinguishedName', 'Description', 'Mail', 'PasswordLastSet', 'LastLogonDate', 'WhenCreated', 'WhenChanged', 'SID') -MaxRows 15000
        Add-ReportTable -Title 'Domain groups' -Rows $adGroups -Columns @('Name', 'SamAccountName', 'GroupCategory', 'GroupScope', 'DistinguishedName', 'Description', 'Mail', 'ManagedBy', 'WhenCreated', 'WhenChanged', 'SID') -MaxRows 15000
        Add-ReportTable -Title 'Direct group memberships' -Rows $adMemberships -Columns @('GroupSamAccountName', 'GroupName', 'GroupDistinguishedName', 'MemberDistinguishedName') -MaxRows 30000
        Add-ReportTable -Title 'Domain computers' -Rows $adComputers -Columns @('Name', 'SamAccountName', 'DNSHostName', 'Enabled', 'OperatingSystem', 'OperatingSystemVersion', 'OperatingSystemServicePack', 'IPv4Address', 'Location', 'ManagedBy', 'DistinguishedName', 'LastLogonDate', 'PasswordLastSet', 'ServicePrincipalNames', 'SID') -MaxRows 15000
        Add-ReportTable -Title 'Organizational units' -Rows $adOus -Columns @('Name', 'Description', 'DistinguishedName', 'ProtectedFromAccidentalDeletion', 'ManagedBy', 'gPLink', 'gPOptions') -MaxRows 10000
        Add-ReportTable -Title 'Managed service accounts' -Rows $managedServiceAccounts -Columns @('Name', 'SamAccountName', 'DNSHostName', 'Enabled', 'Description', 'ServicePrincipalNames', 'PrincipalsAllowedToRetrieveManagedPassword', 'ManagedPasswordIntervalInDays', 'KerberosEncryptionType', 'DistinguishedName', 'SID') -MaxRows 5000
        Add-ReportPreformatted -Title 'Domain controller diagnostic summary' -Text (Get-ReportFileText -Path (Join-Path $paths.Roles 'Active-Directory\DCDiag-Verbose.txt') -MaxLength 50000) -MaxLength 50000
        Add-ReportPreformatted -Title 'Active Directory replication summary' -Text (Get-ReportFileText -Path (Join-Path $paths.Roles 'Active-Directory\Replication-Summary.txt') -MaxLength 30000) -MaxLength 30000
        Add-ReportParagraph -Text 'Use supported System State and forest or domain recovery procedures. The tabular AD inventory is for validation and reconstruction assistance, not a password-bearing directory backup.' -Warning
    }

    if ($gpoInventory.Count -gt 0) {
        Add-ReportHeading -Text '11.2 Domain Group Policy' -Level 2
        Add-ReportTable -Title 'GPO inventory' -Rows $gpoInventory -Columns @('DisplayName', 'Id', 'GpoStatus', 'CreationTime', 'ModificationTime', 'Owner', 'DomainName', 'Description') -MaxRows 10000
        Add-ReportTable -Title 'GPO links and inheritance' -Rows $gpoLinks -Columns @('Target', 'GpoInheritanceBlocked', 'GpoLinks', 'InheritedGpoLinks', 'Error') -MaxRows 20000
        Add-ReportTable -Title 'GPO permissions' -Rows $gpoPermissions -Columns @('GpoName', 'GpoId', 'Trustee', 'TrusteeType', 'Permission', 'Inherited') -MaxRows 30000
    }

    $dhcpScopes = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'DHCP\IPv4-Scopes'))
    if ($dhcpScopes.Count -gt 0) {
        Add-ReportHeading -Text '11.3 DHCP Server' -Level 2
        Add-ReportTable -Title 'IPv4 scopes' -Rows $dhcpScopes -Columns @('ScopeId', 'SubnetMask', 'Name', 'State', 'StartRange', 'EndRange', 'LeaseDuration', 'Type', 'Description') -MaxRows 5000
        Add-ReportTable -Title 'IPv4 server options' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DHCP\IPv4-Server-Options')) -Columns @('OptionId', 'Name', 'Type', 'Value', 'VendorClass', 'UserClass', 'PolicyName') -MaxRows 5000
        Add-ReportTable -Title 'IPv4 reservations' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DHCP\IPv4-Reservations')) -Columns @('ScopeId', 'IPAddress', 'ClientId', 'Name', 'Type', 'Description') -MaxRows 20000
        Add-ReportTable -Title 'Scope options' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DHCP\IPv4-Scope-Options')) -Columns @('ScopeId', 'OptionId', 'Name', 'Type', 'Value', 'VendorClass', 'UserClass', 'PolicyName') -MaxRows 20000
        Add-ReportTable -Title 'DHCP allow and deny filters' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DHCP\IPv4-Filters')) -Columns @('List', 'MacAddress', 'Description') -MaxRows 10000
        Add-ReportTable -Title 'DHCP exclusion ranges' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DHCP\IPv4-Exclusion-Ranges')) -Columns @('ScopeId','StartRange','EndRange') -MaxRows 10000
        Add-ReportTable -Title 'DHCP policies' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DHCP\IPv4-Policies')) -MaxRows 10000
        Add-ReportTable -Title 'DHCP scope statistics' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DHCP\IPv4-Scope-Statistics')) -MaxRows 5000
        Add-ReportTable -Title 'DHCP bindings' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DHCP\IPv4-Bindings')) -MaxRows 1000
        Add-ReportTable -Title 'DHCP failover relationships' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DHCP\IPv4-Failover')) -Columns @('Name', 'PartnerServer', 'Mode', 'State', 'ScopeId', 'LoadBalancePercent', 'MaxClientLeadTime', 'AutoStateTransition') -MaxRows 5000
        Add-ReportPreformatted -Title 'DHCP restore guidance' -Text (Get-ReportFileText -Path (Join-Path $paths.Roles 'DHCP\RESTORE-GUIDANCE.txt') -MaxLength 15000) -MaxLength 15000
    }

    $dnsZones = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'DNS\DNS-Zones'))
    if ($dnsZones.Count -gt 0) {
        Add-ReportHeading -Text '11.4 DNS Server' -Level 2
        Add-ReportTable -Title 'DNS server settings' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DNS\Server-Settings')) -Columns @('ComputerName', 'ListeningIPAddress', 'AllIPAddress', 'RoundRobin', 'LocalNetPriority', 'SecureResponses', 'EnableDnsSec', 'EnableEDnsProbes') -MaxRows 100
        Add-ReportTable -Title 'DNS forwarders' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DNS\Forwarders')) -Columns @('IPAddress', 'UseRootHint', 'Timeout', 'EnableReordering') -MaxRows 1000
        Add-ReportTable -Title 'DNS scavenging configuration' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DNS\Scavenging')) -Columns @('ScavengingState', 'ScavengingInterval', 'LastScavengeTime') -MaxRows 100
        Add-ReportTable -Title 'DNS zones' -Rows $dnsZones -Columns @('ZoneName', 'ZoneType', 'IsAutoCreated', 'IsDsIntegrated', 'IsReverseLookupZone', 'IsSigned', 'DynamicUpdate', 'ReplicationScope', 'ZoneFile') -MaxRows 5000
        foreach ($zone in $dnsZones) {
            $zoneName = [string](Get-ReportPropertyValue -InputObject $zone -PropertyName 'ZoneName')
            if (-not [string]::IsNullOrWhiteSpace($zoneName)) {
                $zoneFolder = Join-Path (Join-Path $paths.Roles 'DNS') ('Zone-' + (Get-SafeFileName -Name $zoneName))
                $zoneRecords = @(Get-ReportData -BasePath (Join-Path $zoneFolder 'Resource-Records-Readable'))
                Add-ReportTable -Title ('DNS records - ' + $zoneName) -Rows $zoneRecords -Columns @('HostName', 'RecordType', 'RecordClass', 'TimeToLive', 'Timestamp', 'RecordData') -MaxRows 20000
            }
        }
    }

    $iisWebsites = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'IIS\Websites'))
    if ($iisWebsites.Count -gt 0) {
        Add-ReportHeading -Text '11.5 IIS Web Server' -Level 2
        Add-ReportTable -Title 'Websites' -Rows $iisWebsites -Columns @('Name', 'Id', 'State', 'PhysicalPath', 'ApplicationPool', 'Bindings') -MaxRows 5000
        Add-ReportTable -Title 'Bindings' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'IIS\Bindings')) -Columns @('protocol', 'bindingInformation', 'certificateHash', 'certificateStoreName', 'sslFlags', 'ItemXPath') -MaxRows 10000
        Add-ReportTable -Title 'Web applications' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'IIS\Applications')) -Columns @('Path', 'ApplicationPool', 'Site', 'PhysicalPath', 'EnabledProtocols') -MaxRows 10000
        Add-ReportTable -Title 'Virtual directories' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'IIS\Virtual-Directories')) -Columns @('Path', 'PhysicalPath', 'UserName', 'LogonMethod') -MaxRows 10000
    }

    $hyperVmsRaw = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Hyper-V\Virtual-Machines'))
    $hyperVms = foreach ($vm in $hyperVmsRaw) {
        [pscustomobject]@{ Name=$vm.Name; State=$vm.State; Status=$vm.Status; Generation=$vm.Generation; Version=$vm.Version; ProcessorCount=$vm.ProcessorCount; MemoryAssigned=(ConvertTo-ReportByteSize $vm.MemoryAssigned); MemoryAssignedBytes=$vm.MemoryAssigned; DynamicMemoryEnabled=$vm.DynamicMemoryEnabled; Path=$vm.Path; ConfigurationLocation=$vm.ConfigurationLocation; CheckpointFileLocation=$vm.CheckpointFileLocation; SmartPagingFilePath=$vm.SmartPagingFilePath; AutomaticStartAction=$vm.AutomaticStartAction; AutomaticStopAction=$vm.AutomaticStopAction }
    }
    $hyperVhdRaw = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Hyper-V\VHD-Details'))
    $hyperVhdDetailsForReport = foreach ($vhd in $hyperVhdRaw) {
        [pscustomobject]@{ Path=$vhd.Path; VhdFormat=$vhd.VhdFormat; VhdType=$vhd.VhdType; FileSize=(ConvertTo-ReportByteSize $vhd.FileSize); VirtualSize=(ConvertTo-ReportByteSize $vhd.Size); MinimumSize=(ConvertTo-ReportByteSize $vhd.MinimumSize); FileSizeBytes=$vhd.FileSize; VirtualSizeBytes=$vhd.Size; MinimumSizeBytes=$vhd.MinimumSize; LogicalSectorSize=$vhd.LogicalSectorSize; PhysicalSectorSize=$vhd.PhysicalSectorSize; ParentPath=$vhd.ParentPath; FragmentationPercentage=$vhd.FragmentationPercentage; Error=$vhd.Error }
    }
    if ($hyperVms.Count -gt 0) {
        Add-ReportHeading -Text '11.6 Hyper-V' -Level 2
        Add-ReportTable -Title 'Hyper-V host configuration' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Hyper-V\Host-Configuration')) -Columns @('ComputerName', 'VirtualHardDiskPath', 'VirtualMachinePath', 'LogicalProcessorCount', 'MacAddressMinimum', 'MacAddressMaximum', 'NumaSpanningEnabled', 'EnableEnhancedSessionMode', 'MaximumStorageMigrations', 'MaximumVirtualMachineMigrations') -MaxRows 100
        Add-ReportTable -Title 'Virtual machines' -Rows $hyperVms -Columns @('Name', 'State', 'Status', 'Generation', 'Version', 'ProcessorCount', 'MemoryAssigned', 'DynamicMemoryEnabled', 'Path', 'ConfigurationLocation', 'CheckpointFileLocation', 'SmartPagingFilePath', 'AutomaticStartAction', 'AutomaticStopAction', 'MemoryAssignedBytes') -MaxRows 10000
        Add-ReportTable -Title 'Virtual machine hard disks' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Hyper-V\VM-Hard-Disks')) -Columns @('VMName', 'ControllerType', 'ControllerNumber', 'ControllerLocation', 'Path', 'DiskNumber', 'SupportPersistentReservations') -MaxRows 20000
        Add-ReportTable -Title 'VHD details' -Rows $hyperVhdDetailsForReport -Columns @('Path', 'VhdFormat', 'VhdType', 'FileSize', 'VirtualSize', 'MinimumSize', 'LogicalSectorSize', 'PhysicalSectorSize', 'ParentPath', 'FragmentationPercentage', 'FileSizeBytes', 'VirtualSizeBytes', 'MinimumSizeBytes', 'Error') -MaxRows 20000
        Add-ReportTable -Title 'Virtual switches' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Hyper-V\Virtual-Switches')) -Columns @('Name', 'SwitchType', 'NetAdapterInterfaceDescription', 'AllowManagementOS', 'EmbeddedTeamingEnabled', 'IovEnabled') -MaxRows 5000
        Add-ReportTable -Title 'VM network adapters' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Hyper-V\VM-Network-Adapters')) -Columns @('VMName', 'Name', 'SwitchName', 'MacAddress', 'Status', 'IPAddresses', 'DynamicMacAddressEnabled', 'DhcpGuard', 'RouterGuard') -MaxRows 20000
        Add-ReportTable -Title 'VM VLAN configuration' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Hyper-V\VM-VLAN-Configuration')) -Columns @('VMName', 'VMNetworkAdapterName', 'OperationMode', 'AccessVlanId', 'NativeVlanId', 'AllowedVlanIdList') -MaxRows 20000
        Add-ReportTable -Title 'VM firmware and BIOS' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Hyper-V\VM-Firmware-and-BIOS')) -Columns @('VMName', 'Generation', 'SecureBoot', 'SecureBootTemplate', 'BootOrder', 'NumLockEnabled') -MaxRows 10000
        Add-ReportTable -Title 'VM checkpoints' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Hyper-V\VM-Checkpoints')) -Columns @('VMName', 'Name', 'SnapshotType', 'CreationTime', 'ParentSnapshotName') -MaxRows 20000
        Add-ReportTable -Title 'Hyper-V Replica relationships' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Hyper-V\VM-Replication')) -Columns @('VMName', 'State', 'Health', 'Mode', 'FrequencySec', 'PrimaryServer', 'ReplicaServer', 'LastReplicationTime', 'AverageReplicationSize') -MaxRows 10000
    }

    $clusterRows = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Failover-Clustering\Cluster'))
    if ($clusterRows.Count -gt 0) {
        Add-ReportHeading -Text '11.7 Failover Clustering' -Level 2
        Add-ReportTable -Title 'Cluster' -Rows $clusterRows -Columns @('Name', 'Domain', 'QuarantineDuration', 'QuarantineThreshold', 'CrossSubnetDelay', 'CrossSubnetThreshold', 'DynamicQuorum', 'WitnessDynamicWeight') -MaxRows 100
        Add-ReportTable -Title 'Cluster nodes' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Failover-Clustering\Nodes')) -Columns @('Name', 'State', 'NodeWeight', 'DynamicWeight', 'DrainStatus', 'FaultDomain') -MaxRows 1000
        Add-ReportTable -Title 'Cluster groups' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Failover-Clustering\Groups')) -Columns @('Name', 'State', 'OwnerNode', 'GroupType', 'Priority', 'AutoFailbackType') -MaxRows 5000
        Add-ReportTable -Title 'Cluster resources' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Failover-Clustering\Resources')) -Columns @('Name', 'ResourceType', 'State', 'OwnerGroup', 'OwnerNode', 'IsCoreResource', 'PersistentState') -MaxRows 10000
        Add-ReportTable -Title 'Cluster networks' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Failover-Clustering\Networks')) -Columns @('Name', 'Address', 'AddressMask', 'Role', 'State', 'Metric', 'AutoMetric') -MaxRows 5000
        Add-ReportTable -Title 'Cluster quorum' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Failover-Clustering\Quorum')) -Columns @('Cluster', 'QuorumResource', 'QuorumType') -MaxRows 100
    }

    $printers = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Print-Server\Printers'))
    if ($printers.Count -gt 0) {
        Add-ReportHeading -Text '11.8 Print Server' -Level 2
        Add-ReportTable -Title 'Printers' -Rows $printers -Columns @('Name', 'ShareName', 'Shared', 'Published', 'DriverName', 'PortName', 'Location', 'Comment', 'PrinterStatus', 'Type') -MaxRows 10000
        Add-ReportTable -Title 'Printer drivers' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Print-Server\Printer-Drivers')) -Columns @('Name', 'Manufacturer', 'MajorVersion', 'PrinterEnvironment', 'DriverPath', 'InfPath', 'ConfigFile', 'DataFile') -MaxRows 10000
        Add-ReportTable -Title 'Printer ports' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Print-Server\Printer-Ports')) -Columns @('Name', 'Description', 'PrinterHostAddress', 'PortNumber', 'Protocol', 'SNMPEnabled', 'SNMPCommunity', 'SNMPIndex') -MaxRows 10000
    }

    $radiusClients = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'NPS\RADIUS-Clients-Metadata'))
    if (($radiusClients.Count -gt 0) -or ($roleNames -contains 'Network Policy Server')) {
        Add-ReportHeading -Text '11.9 Network Policy Server' -Level 2
        Add-ReportTable -Title 'RADIUS clients' -Rows $radiusClients -Columns @('Name', 'Address', 'Enabled', 'VendorName') -MaxRows 5000
        Add-ReportPreformatted -Title 'NPS configuration summary' -Text (Get-ReportFileText -Path (Join-Path $paths.Roles 'NPS\NPS-Configuration-Summary.txt') -MaxLength 50000) -MaxLength 50000
    }

    if ($roleNames -contains 'Routing and Remote Access') {
        Add-ReportHeading -Text '11.10 Routing and Remote Access' -Level 2
        Add-ReportPreformatted -Title 'Routing configuration dump' -Text (Get-ReportFileText -Path (Join-Path $paths.Roles 'RRAS\Routing-Dump.txt') -MaxLength 50000) -MaxLength 50000
        Add-ReportPreformatted -Title 'RRAS registered server status' -Text (Get-ReportFileText -Path (Join-Path $paths.Roles 'RRAS\RAS-Registered-Server.txt') -MaxLength 15000) -MaxLength 15000
        Add-ReportPreformatted -Title 'RRAS restore and reconstruction guidance' -Text (Get-ReportFileText -Path (Join-Path $paths.Roles 'RRAS\RESTORE-GUIDANCE.txt') -MaxLength 20000) -MaxLength 20000
    }

    if ($roleNames -contains 'Active Directory Certificate Services') {
        Add-ReportHeading -Text '11.11 Active Directory Certificate Services' -Level 2
        Add-ReportPreformatted -Title 'CA registry configuration' -Text (Get-ReportFileText -Path (Join-Path $paths.Roles 'Active-Directory-Certificate-Services\CA-Registry-Configuration.txt') -MaxLength 50000) -MaxLength 50000
        Add-ReportPreformatted -Title 'Published certificate templates' -Text (Get-ReportFileText -Path (Join-Path $paths.Roles 'Active-Directory-Certificate-Services\Published-Certificate-Templates.txt') -MaxLength 50000) -MaxLength 50000
        Add-ReportParagraph -Text 'A supported CA database and private-key backup is mandatory. This report is not sufficient to restore the certification authority.' -Warning
    }

    $sqlInstances = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'Microsoft-SQL-Server\SQL-Instances'))
    if (($sqlInstances.Count -gt 0) -or ($roleNames -contains 'Microsoft SQL Server')) {
        Add-ReportHeading -Text '11.12 Microsoft SQL Server' -Level 2
        Add-ReportTable -Title 'SQL Server instances' -Rows $sqlInstances -Columns @('InstanceName', 'InstanceId', 'RegistryPath') -MaxRows 500
        Add-ReportTable -Title 'Installed SQL components and features' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Microsoft-SQL-Server\Installed-SQL-Components')) -Columns @('DisplayName','DisplayVersion','Publisher','InstallDate','InstallLocation','Architecture') -MaxRows 5000
        Add-ReportTable -Title 'SQL-related Windows services' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Microsoft-SQL-Server\SQL-Windows-Services')) -Columns @('Name','DisplayName','State','StartMode','StartName','PathName') -MaxRows 1000
        Add-ReportTable -Title 'SQL instance Windows and registry configuration' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Microsoft-SQL-Server\SQL-Instance-Windows-Metadata')) -Columns @('InstanceName','Edition','Version','PatchLevel','DefaultDataDirectory','DefaultLogDirectory','DefaultBackupDirectory','AuthenticationMode','TcpPort','TcpDynamicPorts','ForceEncryption','EngineServiceState','EngineStartMode','EngineServiceAccount','AgentServiceState','AgentStartMode','AgentServiceAccount') -MaxRows 500
        Add-ReportTable -Title 'SQL database-level collection status' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'Microsoft-SQL-Server\SQL-Database-Level-Collection-Status')) -Columns @('InstanceName','DataSource','DatabaseLevelCollection','Error') -MaxRows 500
        foreach ($sqlInstance in $sqlInstances) {
            $sqlName = [string]$sqlInstance.InstanceName
            $safeSqlName = Get-SafeFileName -Name $sqlName
            Add-ReportHeading -Text ('SQL instance: ' + $sqlName) -Level 3
            Add-ReportTable -Title 'Server properties' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles ('Microsoft-SQL-Server\Instance-' + $safeSqlName + '-Server-Properties'))) -Columns @('ServerName','InstanceName','Edition','ProductVersion','ProductLevel','ProductUpdateLevel','ServerCollation','AuthenticationMode','IsClustered','InstanceDefaultDataPath','InstanceDefaultLogPath') -MaxRows 20
            Add-ReportTable -Title 'Databases' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles ('Microsoft-SQL-Server\Instance-' + $safeSqlName + '-Databases'))) -Columns @('DatabaseName','State','RecoveryModel','CompatibilityLevel','DatabaseOwner','CreationDate','Collation','TotalSize','TotalSizeBytes') -MaxRows 5000
            Add-ReportTable -Title 'Database space' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles ('Microsoft-SQL-Server\Instance-' + $safeSqlName + '-Database-Space'))) -Columns @('DatabaseName','DataAllocated','DataFree','LogAllocated','DataAllocatedBytes','DataFreeBytes','LogAllocatedBytes') -MaxRows 5000
            Add-ReportTable -Title 'Database files' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles ('Microsoft-SQL-Server\Instance-' + $safeSqlName + '-Database-Files'))) -Columns @('DatabaseName','LogicalName','FileType','PhysicalPath','FileSize','MaxSize','GrowthValue','IsPercentGrowth','FileSizeBytes') -MaxRows 10000
            Add-ReportTable -Title 'Most recent SQL backups' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles ('Microsoft-SQL-Server\Instance-' + $safeSqlName + '-Backup-History-Summary'))) -Columns @('DatabaseName','LastFullBackup','LastDifferentialBackup','LastLogBackup') -MaxRows 5000
            Add-ReportTable -Title 'SQL Agent jobs' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles ('Microsoft-SQL-Server\Instance-' + $safeSqlName + '-SQL-Agent-Jobs'))) -Columns @('JobName','Enabled','Description','CreationDate','ModifiedDate') -MaxRows 5000
            Add-ReportTable -Title 'Linked servers' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles ('Microsoft-SQL-Server\Instance-' + $safeSqlName + '-Linked-Servers'))) -Columns @('LinkedServerName','Product','Provider','DataSource','Catalog','IsLinked','DataAccessEnabled','RpcOutEnabled') -MaxRows 1000
            Add-ReportTable -Title 'SQL server principals' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles ('Microsoft-SQL-Server\Instance-' + $safeSqlName + '-Server-Principals'))) -Columns @('PrincipalName','PrincipalType','DefaultDatabase','IsDisabled','CreationDate','ModifiedDate') -MaxRows 5000
            Add-ReportTable -Title 'Instance configuration' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles ('Microsoft-SQL-Server\Instance-' + $safeSqlName + '-Server-Configuration'))) -Columns @('ConfigurationName','ConfiguredValue','ValueInUse','Minimum','Maximum','IsDynamic','IsAdvanced','Description') -MaxRows 10000
        }
        Add-ReportPreformatted -Title 'SQL recovery and restore guidance' -Text (Get-ReportFileText -Path (Join-Path $paths.Roles 'Microsoft-SQL-Server\SQL-Recovery-and-Restore-Guidance.txt') -MaxLength 30000) -MaxLength 30000
        Add-ReportParagraph -Text 'SQL-native database backups and separate protection of encryption dependencies remain mandatory. The database-level queries in this report are read-only and use the Windows identity running the collector.' -Warning
    }

    $dfsRoots = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'DFS\Namespace-Roots'))
    $dfsGroups = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'DFS\Replication-Groups'))
    if (($dfsRoots.Count + $dfsGroups.Count) -gt 0) {
        Add-ReportHeading -Text '11.13 DFS Namespaces and Replication' -Level 2
        Add-ReportTable -Title 'Namespace roots' -Rows $dfsRoots -Columns @('Path', 'Type', 'State', 'Description', 'TimeToLiveSec') -MaxRows 5000
        Add-ReportTable -Title 'Namespace folders' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DFS\Namespace-Folders')) -Columns @('Path', 'State', 'Description', 'TimeToLiveSec') -MaxRows 10000
        Add-ReportTable -Title 'Namespace targets' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DFS\Namespace-Folder-Targets')) -Columns @('Path', 'TargetPath', 'State', 'ReferralPriorityClass', 'ReferralPriorityRank') -MaxRows 20000
        Add-ReportTable -Title 'Replication groups' -Rows $dfsGroups -Columns @('GroupName', 'Description', 'DomainName') -MaxRows 5000
        Add-ReportTable -Title 'Replicated folders' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DFS\Replicated-Folders')) -Columns @('GroupName', 'FolderName', 'Description', 'DfsnPath', 'FileNameToExclude', 'DirectoryNameToExclude') -MaxRows 10000
        Add-ReportTable -Title 'Replication memberships' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DFS\Replication-Memberships')) -Columns @('GroupName', 'FolderName', 'ComputerName', 'ContentPath', 'PrimaryMember', 'Enabled', 'StagingPath', 'ConflictAndDeletedPath') -MaxRows 20000
        Add-ReportTable -Title 'Replication connections' -Rows (Get-ReportData -BasePath (Join-Path $paths.Roles 'DFS\Replication-Connections')) -Columns @('GroupName', 'SourceComputerName', 'DestinationComputerName', 'Enabled', 'RdcEnabled', 'Description') -MaxRows 20000
    }

    $fsrmQuotasRaw = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'FSRM\FsrmQuota'))
    $fsrmAutoQuotasRaw = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'FSRM\FsrmAutoQuota'))
    $fsrmQuotas = foreach ($row in $fsrmQuotasRaw) { [pscustomobject]@{ Path=$row.Path; Size=(ConvertTo-ReportByteSize $row.Size); Usage=(ConvertTo-ReportByteSize $row.Usage); SoftLimit=$row.SoftLimit; Status=$row.Status; Description=$row.Description; Template=$row.Template; MatchesTemplate=$row.MatchesTemplate; SizeBytes=$row.Size; UsageBytes=$row.Usage } }
    $fsrmAutoQuotas = foreach ($row in $fsrmAutoQuotasRaw) { [pscustomobject]@{ Path=$row.Path; Size=(ConvertTo-ReportByteSize $row.Size); SoftLimit=$row.SoftLimit; Status=$row.Status; Description=$row.Description; Template=$row.Template; SizeBytes=$row.Size } }
    $fsrmFileScreens = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'FSRM\FsrmFileScreen'))
    $fsrmFileGroups = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'FSRM\FsrmFileGroup'))
    $fsrmStorageReports = @(Get-ReportData -BasePath (Join-Path $paths.Roles 'FSRM\FsrmStorageReport'))
    if (($fsrmQuotas.Count + $fsrmAutoQuotas.Count + $fsrmFileScreens.Count + $fsrmFileGroups.Count + $fsrmStorageReports.Count) -gt 0) {
        Add-ReportHeading -Text '11.14 File Server Resource Manager' -Level 2
        Add-ReportTable -Title 'FSRM quotas' -Rows $fsrmQuotas -Columns @('Path', 'Size', 'Usage', 'SoftLimit', 'Status', 'Description', 'Template', 'MatchesTemplate', 'SizeBytes', 'UsageBytes') -MaxRows 10000
        Add-ReportTable -Title 'FSRM auto quotas' -Rows $fsrmAutoQuotas -Columns @('Path', 'Size', 'SoftLimit', 'Status', 'Description', 'Template', 'SizeBytes') -MaxRows 10000
        Add-ReportTable -Title 'FSRM file screens' -Rows $fsrmFileScreens -Columns @('Path', 'Active', 'IncludeGroup', 'ExcludeGroup', 'Description', 'Template', 'MatchesTemplate') -MaxRows 10000
        Add-ReportTable -Title 'FSRM file groups' -Rows $fsrmFileGroups -Columns @('Name', 'IncludePattern', 'ExcludePattern', 'Description') -MaxRows 10000
        Add-ReportTable -Title 'FSRM storage reports' -Rows $fsrmStorageReports -Columns @('Name', 'Namespace', 'Schedule', 'ReportType', 'ReportFormat', 'Interactive', 'MailTo') -MaxRows 10000
    }

    if ($roleNames -contains 'WSUS') {
        Add-ReportHeading -Text '11.15 Windows Server Update Services' -Level 2
        Add-ReportPreformatted -Title 'WSUS setup registry configuration' -Text (Get-ReportFileText -Path (Join-Path $paths.Roles 'WSUS\WSUS-Setup-Registry.reg') -MaxLength 50000) -MaxLength 50000
        Add-ReportParagraph -Text 'SUSDB, content, approvals, and reporting history are not included. Follow the recovery requirement recorded in the Executive Summary.' -Warning
    }

    Add-ReportHeading -Text '12. Recent Event History' -Level 1 -PageBreakBefore
    Add-ReportParagraph -Text ('The table contains the most recent Critical, Error, and Warning events collected from the System and Application logs during the previous ' + $EventLogDays + ' days. Repeated events may indicate unresolved dependencies that must be addressed before or after recovery or rebuild activity.')
    Add-ReportTable -Title 'Recent critical, error, and warning events' -Rows $recentEvents -Columns @('TimeCreated', 'LogName', 'LevelDisplayName', 'ProviderName', 'Id', 'TaskDisplayName', 'MachineName', 'Message') -MaxRows 1000

    Add-ReportHeading -Text '13. Recovery Checklist' -Level 1 -PageBreakBefore
    Add-ReportTable -Title 'Items requiring manual confirmation' -Rows $manualItems -Columns @('Category', 'Item', 'Required', 'Status', 'Notes') -MaxRows 500
    Add-ReportParagraph -Text 'Complete the Status and Notes fields in the separate CSV checklist or in your service documentation. Do not rely on this inventory as sufficient for recovery or rebuild work until critical gaps are addressed.' -Warning

    Add-ReportHeading -Text '14. Collection Appendix' -Level 1 -PageBreakBefore
    Add-ReportTable -Title 'Collector execution status' -Rows $collectorRows -Columns @('Collector', 'Status', 'Started', 'Ended', 'DurationSec', 'ErrorMessage') -MaxRows 1000
    Add-ReportParagraph -Text 'The raw CSV, CLIXML, XML, registry, policy, role-backup, and native command outputs remain in the numbered folders. They are supporting evidence and import artifacts. The consolidated report should be the engineer starting point for review.'
    Add-ReportBullets -Items @(
        '00-Summary contains collection status, role detection, and completion markers.',
        '01 through 09 contain supporting system, hardware, storage, network, software, security, share, role, and event exports.',
        '10-Recovery-Reference contains the editable recovery checklist and command reference.',
        'Logs contains collection and executable diagnostics. Errors contains collector exception detail.',
        'SHA256-Manifest.csv can be used to validate supporting file integrity.'
    )

    Write-InventoryLog -Message 'Consolidated report stage: preparing concise text summary.'

    $summaryBuilder = New-Object System.Text.StringBuilder
    [void]$summaryBuilder.AppendLine('SERVER INVENTORY SUMMARY')
    [void]$summaryBuilder.AppendLine('================================')
    [void]$summaryBuilder.AppendLine('')
    [void]$summaryBuilder.AppendLine('Server: ' + [string]$summary.ComputerName)
    [void]$summaryBuilder.AppendLine('FQDN: ' + [string]$summary.FQDN)
    [void]$summaryBuilder.AppendLine('Domain role: ' + [string]$summary.DomainRole)
    [void]$summaryBuilder.AppendLine('Operating system: ' + [string]$summary.OperatingSystem + ' ' + [string]$summary.Version + ' build ' + [string]$summary.BuildNumber)
    [void]$summaryBuilder.AppendLine('Hardware: ' + [string]$summary.Manufacturer + ' ' + [string]$summary.Model + '; serial ' + [string]$summary.SerialNumber)
    [void]$summaryBuilder.AppendLine('Memory and CPU: ' + [string]$summary.TotalMemoryGB + ' GB; ' + [string]$summary.PhysicalCoreCount + ' cores; ' + [string]$summary.LogicalProcessorCount + ' logical processors')
    [void]$summaryBuilder.AppendLine('Collected: ' + (ConvertTo-ReportText -Value $summary.CollectedAt -MaxLength 100))
    [void]$summaryBuilder.AppendLine('')
    [void]$summaryBuilder.AppendLine('PRIMARY REPORTS')
    [void]$summaryBuilder.AppendLine('Word report: ' + $DocxPath)
    [void]$summaryBuilder.AppendLine('HTML report: ' + $HtmlPath)
    [void]$summaryBuilder.AppendLine('Supporting package: ' + $packagePath)
    [void]$summaryBuilder.AppendLine('')
    [void]$summaryBuilder.AppendLine('COLLECTION HEALTH')
    [void]$summaryBuilder.AppendLine('Successful collectors: ' + $successfulCollectors.Count)
    [void]$summaryBuilder.AppendLine('Failed or unavailable collectors: ' + $failedCollectors.Count)
    if ($failedCollectors.Count -gt 0) {
        foreach ($failure in $failedCollectors) {
            [void]$summaryBuilder.AppendLine('- ' + [string]$failure.Collector + ': ' + [string]$failure.ErrorMessage)
        }
    }
    else {
        [void]$summaryBuilder.AppendLine('- No collector failures were recorded before consolidated report generation.')
    }
    [void]$summaryBuilder.AppendLine('')
    [void]$summaryBuilder.AppendLine('SELECTED INVENTORY CATEGORIES')
    foreach ($selectedCategory in $selectedInventoryRows) { [void]$summaryBuilder.AppendLine('- ' + [string]$selectedCategory.Category) }
    [void]$summaryBuilder.AppendLine('')
    [void]$summaryBuilder.AppendLine('SELECTED BACKUP / EXPORT MODULES')
    if ($selectedExportRows.Count -gt 0) { foreach ($selectedExport in $selectedExportRows) { [void]$summaryBuilder.AppendLine('- ' + [string]$selectedExport.Category) } } else { [void]$summaryBuilder.AppendLine('- None') }
    [void]$summaryBuilder.AppendLine('')
    [void]$summaryBuilder.AppendLine('DETECTED CRITICAL ROLES AND PLATFORMS')
    if ($roleRows.Count -gt 0) {
        foreach ($role in $roleRows) {
            [void]$summaryBuilder.AppendLine('- ' + [string]$role.Role + ': ' + [string]$role.Notes)
        }
    }
    else {
        [void]$summaryBuilder.AppendLine('- No critical server role was positively detected.')
    }
    [void]$summaryBuilder.AppendLine('')
    [void]$summaryBuilder.AppendLine('INVENTORY COUNTS')
    foreach ($metric in $metricRows) {
        [void]$summaryBuilder.AppendLine('- ' + [string]$metric.Area + ': ' + [string]$metric.Count)
    }
    [void]$summaryBuilder.AppendLine('')
    [void]$summaryBuilder.AppendLine('NETWORK ADDRESSES')
    foreach ($address in @($ipAddresses | Where-Object { $_.IPAddress -and $_.IPAddress -notmatch '^(127\.|::1$|fe80:)' })) {
        [void]$summaryBuilder.AppendLine('- ' + [string]$address.InterfaceAlias + ': ' + [string]$address.IPAddress + '/' + [string]$address.PrefixLength)
    }
    [void]$summaryBuilder.AppendLine('')
    [void]$summaryBuilder.AppendLine('STORAGE VOLUMES')
    foreach ($volume in $volumes) {
        $drive = ConvertTo-ReportText -Value (Get-ReportPropertyValue -InputObject $volume -PropertyName 'DriveLetter') -MaxLength 30
        if (-not $drive) {
            $drive = ConvertTo-ReportText -Value (Get-ReportPropertyValue -InputObject $volume -PropertyName 'DeviceID') -MaxLength 60
        }
        $size = ConvertTo-ReportText -Value (Get-ReportPropertyValue -InputObject $volume -PropertyName 'Capacity') -MaxLength 50
        if (-not $size) { $size = ConvertTo-ReportByteSize -Value (Get-ReportPropertyValue -InputObject $volume -PropertyName 'Size') }
        if (-not $size) { $size = ConvertTo-ReportByteSize -Value (Get-ReportPropertyValue -InputObject $volume -PropertyName 'CapacityBytes') }
        $free = ConvertTo-ReportText -Value (Get-ReportPropertyValue -InputObject $volume -PropertyName 'Free') -MaxLength 50
        if (-not $free) { $free = ConvertTo-ReportByteSize -Value (Get-ReportPropertyValue -InputObject $volume -PropertyName 'SizeRemaining') }
        if (-not $free) { $free = ConvertTo-ReportByteSize -Value (Get-ReportPropertyValue -InputObject $volume -PropertyName 'FreeSpace') }
        if (-not $free) { $free = ConvertTo-ReportByteSize -Value (Get-ReportPropertyValue -InputObject $volume -PropertyName 'FreeBytes') }
        [void]$summaryBuilder.AppendLine('- ' + $drive + ': capacity ' + $size + '; free ' + $free)
    }
    [void]$summaryBuilder.AppendLine('')
    [void]$summaryBuilder.AppendLine('CRITICAL RECOVERY REQUIREMENTS')
    foreach ($gap in $recoveryGaps) {
        [void]$summaryBuilder.AppendLine('- [' + [string]$gap.Severity + '] ' + [string]$gap.Requirement + ': ' + [string]$gap.Action)
    }
    [void]$summaryBuilder.AppendLine('')
    [void]$summaryBuilder.AppendLine('IMPORTANT')
    [void]$summaryBuilder.AppendLine('This inventory is not a backup. Use the Word or HTML report for documentation, troubleshooting, migration, recovery, replacement, or rebuild assistance, and verify all critical recovery requirements and failed collectors before relying on it for a recovery operation.')

    Write-InventoryLog -Message 'Consolidated report stage: writing summary text report.'
    $summaryEncoding = New-Object System.Text.UTF8Encoding($true)
    [System.IO.File]::WriteAllText($SummaryTextPath, $summaryBuilder.ToString(), $summaryEncoding)
    Write-InventoryLog -Message 'Consolidated report stage: summary text report completed.'

    $reportWriteErrors = New-Object System.Collections.ArrayList

    Write-InventoryLog -Message 'Consolidated report stage: rendering HTML report.'
    try {
        Write-ConsolidatedHtmlReport -Path $HtmlPath -Blocks $script:reportBlocks -DocumentTitle $documentTitle
        Write-InventoryLog -Message 'Consolidated report stage: HTML report completed.'
    }
    catch {
        [void]$reportWriteErrors.Add('HTML report: ' + $_.Exception.Message)
    }

    Write-InventoryLog -Message 'Consolidated report stage: rendering Word report.'
    try {
        Write-ConsolidatedDocxReport -Path $DocxPath -Blocks $script:reportBlocks -DocumentTitle $documentTitle -SubjectText ('Inventory and configuration report for ' + $summary.ComputerName)
        Write-InventoryLog -Message 'Consolidated report stage: Word report completed.'
    }
    catch {
        [void]$reportWriteErrors.Add('Word report: ' + $_.Exception.Message)
    }

    if ($reportWriteErrors.Count -gt 0) {
        throw ('One or more consolidated report formats could not be generated:' + [Environment]::NewLine + ($reportWriteErrors -join [Environment]::NewLine))
    }

    Write-InventoryLog -Message 'Consolidated report stage: all primary report formats were created.'

    return [pscustomobject]@{
        DocxPath        = $DocxPath
        HtmlPath        = $HtmlPath
        SummaryTextPath = $SummaryTextPath
        BlockCount      = $script:reportBlocks.Count
        FailedCollectors = $failedCollectors.Count
    }
}

# Runtime-test the report helper parameter contracts before performing the long collection.
# Parser validation alone cannot detect empty-collection binding behavior in older Windows PowerShell.
try {
    $reportSelfTestGaps = New-Object System.Collections.ArrayList
    Add-RoleRecoveryGap -List $reportSelfTestGaps -Severity 'Test' -Requirement 'Runtime self-test' -Reason 'Runtime self-test' -Action 'Runtime self-test'
    if ($reportSelfTestGaps.Count -ne 1) {
        throw 'The recovery-gap helper did not accept and populate an initially empty collection.'
    }

    $script:reportBlocks = New-Object System.Collections.ArrayList
    Add-ReportTable -Title 'Runtime self-test' -Rows @() -Columns @('Value') -MaxRows 1
    Add-ReportBullets -Items @()
    if ($script:reportBlocks.Count -ne 2) {
        throw 'The report block helpers did not populate their test collection as expected.'
    }

    # Validate that report text conversion cannot recurse indefinitely on cyclic or deeply nested values.
    $reportSelfTestCycle = New-Object System.Collections.ArrayList
    [void]$reportSelfTestCycle.Add('cycle-root')
    [void]$reportSelfTestCycle.Add($reportSelfTestCycle)
    $reportSelfTestCycleText = ConvertTo-ReportText -Value $reportSelfTestCycle -MaxLength 1000
    if ($reportSelfTestCycleText -notmatch 'cycle-root') {
        throw 'The report text formatter did not safely process a cyclic collection.'
    }

    $reportSelfTestDeep = 'deep-leaf'
    for ($reportSelfTestIndex = 0; $reportSelfTestIndex -lt 150; $reportSelfTestIndex++) {
        $reportSelfTestContainer = New-Object System.Collections.ArrayList
        [void]$reportSelfTestContainer.Add($reportSelfTestDeep)
        $reportSelfTestDeep = $reportSelfTestContainer
    }
    $reportSelfTestDeepText = ConvertTo-ReportText -Value $reportSelfTestDeep -MaxLength 1000
    if ([string]::IsNullOrWhiteSpace($reportSelfTestDeepText)) {
        throw 'The report text formatter did not safely process a deeply nested collection.'
    }

    # Exercise the exact table-snapshot path used by the consolidated report.
    # A cyclic value must be converted into a plain string before it is stored
    # in $reportBlocks. This test runs before the long inventory collection.
    $reportSelfTestRow = [pscustomobject]@{ Name = 'cycle-row'; Value = $reportSelfTestCycle }
    $script:reportBlocks = New-Object System.Collections.ArrayList
    Add-ReportTable -Title 'Circular value snapshot test' -Rows @($reportSelfTestRow) -Columns @('Name', 'Value') -MaxRows 10
    if ($script:reportBlocks.Count -ne 1) {
        throw 'The report table snapshot self-test did not create exactly one report block.'
    }
    $reportSelfTestStoredValue = $script:reportBlocks[0].Rows[0].Value
    if (-not ($reportSelfTestStoredValue -is [string])) {
        throw 'The report table snapshot retained a complex value instead of a flat string.'
    }
    if ($reportSelfTestStoredValue -notmatch 'cycle-root') {
        throw 'The report table snapshot did not safely preserve the readable portion of a cyclic value.'
    }

    $script:reportBlocks = $null
}
catch {
    throw ('Consolidated report runtime self-test failed before inventory collection: ' + $_.Exception.Message)
}

try {
    Start-Transcript -Path (Join-Path $paths.Logs 'PowerShell-Transcript.txt') -Force | Out-Null
    $script:transcriptStarted = $true
}
catch {
    Write-InventoryLog -Message ('Transcript could not be started: ' + $_.Exception.Message) -Level 'WARN'
}

Write-InventoryLog -Message ('Inventory package path: ' + $packagePath)

if ($PSCommandPath -and (Test-Path -LiteralPath $PSCommandPath)) {
    Copy-Item -LiteralPath $PSCommandPath -Destination (Join-Path $paths.Summary 'Export-ServerInventory.ps1') -Force
}

$os = Get-CimInstance -ClassName Win32_OperatingSystem
$computerSystem = Get-CimInstance -ClassName Win32_ComputerSystem
$bios = Get-CimInstance -ClassName Win32_BIOS
$processors = @(Get-CimInstance -ClassName Win32_Processor)

$domainRoleNames = @{
    0 = 'Standalone Workstation'
    1 = 'Member Workstation'
    2 = 'Standalone Server'
    3 = 'Member Server'
    4 = 'Backup Domain Controller'
    5 = 'Primary Domain Controller'
}

$logicalProcessorCount = ($processors | Measure-Object -Property NumberOfLogicalProcessors -Sum).Sum
$physicalCoreCount = ($processors | Measure-Object -Property NumberOfCores -Sum).Sum
$domainRoleValue = [int]$computerSystem.DomainRole
$uptime = (Get-Date) - $os.LastBootUpTime

$fqdn = $env:COMPUTERNAME
try {
    $resolvedHostName = [System.Net.Dns]::GetHostEntry($env:COMPUTERNAME).HostName
    if (-not [string]::IsNullOrWhiteSpace($resolvedHostName)) {
        $fqdn = $resolvedHostName
    }
}
catch {
    $fqdn = $env:COMPUTERNAME
}

$osVersionObject = New-Object System.Version([string]$os.Version)
$compatibilityMode = 'Standard compatibility mode'
if (($osVersionObject.Major -eq 6) -and ($osVersionObject.Minor -le 3)) {
    $compatibilityMode = 'Legacy compatibility mode (Windows Server 2012 / 2012 R2 generation)'
}
$powerShellVersionText = $PSVersionTable.PSVersion.ToString()

$summary = [pscustomobject]@{
    CollectedAt              = Get-Date
    ComputerName             = $env:COMPUTERNAME
    FQDN                     = $fqdn
    Domain                   = $computerSystem.Domain
    DomainRole               = $domainRoleNames[$domainRoleValue]
    Manufacturer             = $computerSystem.Manufacturer
    Model                    = $computerSystem.Model
    SerialNumber             = $bios.SerialNumber
    IsVirtualMachine         = (($computerSystem.Model -match 'Virtual|VMware|KVM|HVM') -or ($computerSystem.Manufacturer -match 'Microsoft Corporation|VMware|Xen|QEMU'))
    OperatingSystem          = $os.Caption
    OSArchitecture           = $os.OSArchitecture
    Version                  = $os.Version
    BuildNumber              = $os.BuildNumber
    CompatibilityMode        = $compatibilityMode
    CollectorMinimum         = 'Windows PowerShell 3.0; Windows Server 2012-generation target and newer'
    InstallDate              = $os.InstallDate
    LastBoot                 = $os.LastBootUpTime
    UptimeDays               = [math]::Round($uptime.TotalDays, 2)
    TotalMemoryGB            = [math]::Round($computerSystem.TotalPhysicalMemory / 1GB, 2)
    PhysicalProcessorCount   = $processors.Count
    PhysicalCoreCount        = $physicalCoreCount
    LogicalProcessorCount    = $logicalProcessorCount
    PowerShellVersion        = $PSVersionTable.PSVersion.ToString()
    InventoryScriptVersion   = $scriptVersion
    ExecutableRunId          = $RunId
    OutputPackage            = $packagePath
}

Save-ObjectData -InputObject $summary -BasePath (Join-Path $paths.Summary 'Server-Summary')
$summary | Format-List * | Out-String -Width 4096 |
    Set-Content -Path (Join-Path $paths.Summary 'READ-FIRST-Server-Summary.txt') -Encoding UTF8

$manualItems = @(
    [pscustomobject]@{ Category = 'Business'; Item = 'Server purpose and business function'; Required = 'Yes'; Status = ''; Notes = '' },
    [pscustomobject]@{ Category = 'Business'; Item = 'Business owner and technical owner'; Required = 'Yes'; Status = ''; Notes = '' },
    [pscustomobject]@{ Category = 'Business'; Item = 'Criticality, RTO, and RPO'; Required = 'Yes'; Status = ''; Notes = '' },
    [pscustomobject]@{ Category = 'Dependencies'; Item = 'Upstream and downstream systems'; Required = 'Yes'; Status = ''; Notes = '' },
    [pscustomobject]@{ Category = 'Dependencies'; Item = 'Required DNS names, firewall paths, ports, and certificates'; Required = 'Yes'; Status = ''; Notes = '' },
    [pscustomobject]@{ Category = 'Applications'; Item = 'Installers, exact versions, licenses, and vendor contacts'; Required = 'Yes'; Status = ''; Notes = '' },
    [pscustomobject]@{ Category = 'Applications'; Item = 'Application-native configuration export'; Required = 'Yes'; Status = ''; Notes = '' },
    [pscustomobject]@{ Category = 'Applications'; Item = 'Database backup and documented restore order'; Required = 'When applicable'; Status = ''; Notes = '' },
    [pscustomobject]@{ Category = 'Data'; Item = 'Data backup with NTFS permissions preserved'; Required = 'Yes'; Status = ''; Notes = '' },
    [pscustomobject]@{ Category = 'Credentials'; Item = 'Service account passwords stored in approved password vault'; Required = 'When applicable'; Status = ''; Notes = 'Passwords are not collected by this script.' },
    [pscustomobject]@{ Category = 'Credentials'; Item = 'Scheduled task and application credentials'; Required = 'When applicable'; Status = ''; Notes = 'Task XML does not include reusable passwords.' },
    [pscustomobject]@{ Category = 'Certificates'; Item = 'PFX backup of required certificates and private keys'; Required = 'When applicable'; Status = ''; Notes = 'Private keys are not collected by this script.' },
    [pscustomobject]@{ Category = 'Encryption'; Item = 'BitLocker recovery keys stored in approved escrow location'; Required = 'When applicable'; Status = ''; Notes = 'Recovery keys are not collected by this script.' },
    [pscustomobject]@{ Category = 'Recovery'; Item = 'Current system-state backup'; Required = 'Role dependent'; Status = ''; Notes = '' },
    [pscustomobject]@{ Category = 'Recovery'; Item = 'Current full VM or bare-metal backup'; Required = 'Yes'; Status = ''; Notes = '' },
    [pscustomobject]@{ Category = 'Recovery'; Item = 'Documented recovery, restore, or rebuild sequence'; Required = 'Yes'; Status = ''; Notes = '' },
    [pscustomobject]@{ Category = 'Validation'; Item = 'Post-recovery or post-rebuild validation tests'; Required = 'Yes'; Status = ''; Notes = '' },
    [pscustomobject]@{ Category = 'Validation'; Item = 'Test restore or recovery exercise completed'; Required = 'Yes'; Status = ''; Notes = '' }
)
$manualItems | Export-Csv -Path (Join-Path $paths.Manual 'Recovery-Readiness-Checklist.csv') -NoTypeInformation -Encoding UTF8

$readme = @"
WINDOWS SERVER INVENTORY AND CONFIGURATION PACKAGE

Server: $($summary.ComputerName)
Collected: $($summary.CollectedAt)
Script version: $scriptVersion
Package: $packagePath

This package is an inventory and configuration reference. It is not a complete,
standalone backup and cannot preserve every secret, private key, application data
set, database, password, license entitlement, or vendor-specific configuration.

Not collected by design:
- User passwords, Active Directory password hashes, or service-account passwords
- Scheduled-task reusable passwords
- BitLocker recovery keys
- Certificate private keys or PFX files
- Application databases and business data
- Full virtual machines, operating-system images, or bare-metal recovery media

Use the Recovery-Readiness-Checklist.csv file to record remaining recovery and continuity items.
Keep this package in a secured administrative location because it contains detailed
infrastructure, account, policy, networking, software, and security information.

Selected options:
- IncludeDomainInventory: $IncludeDomainInventory
- IncludeGpoBackup: $IncludeGpoBackup
- IncludeShareAclBackup: $IncludeShareAclBackup
- IncludeEventLogs: $IncludeEventLogs
- EventLogDays: $EventLogDays
- IncludeSensitiveRoleExports: $IncludeSensitiveRoleExports
- ExecutableRunId: $RunId
- CreateZip: $CreateZip
"@
$readme | Set-Content -Path (Join-Path $paths.Summary 'READ-ME.txt') -Encoding UTF8

$recoveryReference = @'
RECOVERY COMMAND REFERENCE

These are references, not an automated restore procedure. Validate server names,
paths, role state, backup age, and restore order before use.

Windows Server Backup feature:
Install-WindowsFeature Windows-Server-Backup

Create a system-state backup to a dedicated backup volume:
wbadmin start systemstatebackup -backupTarget:E: -quiet

Import the exported Windows Firewall policy:
netsh advfirewall import "C:\Path\Windows-Firewall-Policy.wfw"

Apply an exported local security policy to a replacement server only after review:
secedit /configure /db C:\Windows\Security\Database\Rebuild.sdb /cfg "C:\Path\Local-Security-Policy.inf"

Restore a DHCP export after installing and authorizing DHCP Server:
Import-DhcpServer -ComputerName "ReplacementServer" -File "C:\Path\DHCP-Configuration-and-Leases.xml" -BackupPath "C:\Windows\System32\dhcp\backup" -Leases -Force

Restore a PrintBRM printer migration file:
%WINDIR%\System32\spool\tools\PrintBrm.exe -R -F "C:\Path\Print-Server-Backup.printerExport"

GPO, IIS, DNS, NPS, AD CS, SQL Server, WSUS, Hyper-V, clustering, DFS, and
application restores require role-specific validation and an approved recovery
procedure. Do not treat the inventory CSV or CLIXML files as supported backups.
'@
$recoveryReference | Set-Content -Path (Join-Path $paths.Manual 'Recovery-Command-Reference.txt') -Encoding UTF8

Invoke-Collector -Name 'Operating system details' -Action {
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_OperatingSystem) -BasePath (Join-Path $paths.System 'Operating-System')
    Invoke-NativeCapture -Command 'systeminfo.exe' -OutputFile (Join-Path $paths.System 'SystemInfo.txt')
}

Invoke-Collector -Name 'Computer and firmware details' -Action {
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_ComputerSystem) -BasePath (Join-Path $paths.Hardware 'Computer-System')
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_ComputerSystemProduct) -BasePath (Join-Path $paths.Hardware 'Computer-System-Product')
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_BIOS) -BasePath (Join-Path $paths.Hardware 'BIOS')
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_BaseBoard) -BasePath (Join-Path $paths.Hardware 'Baseboard')
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_SystemEnclosure) -BasePath (Join-Path $paths.Hardware 'System-Enclosure')
}

Invoke-Collector -Name 'Processor and memory details' -Action {
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_Processor) -BasePath (Join-Path $paths.Hardware 'Processors')
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_PhysicalMemory) -BasePath (Join-Path $paths.Hardware 'Physical-Memory')
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_PhysicalMemoryArray) -BasePath (Join-Path $paths.Hardware 'Memory-Arrays')
}

Invoke-Collector -Name 'PowerShell and locale details' -Action {
    $psInfo = [pscustomobject]@{
        PSVersion        = $PSVersionTable.PSVersion.ToString()
        PSEdition        = if ($PSVersionTable.ContainsKey('PSEdition')) { $PSVersionTable.PSEdition } else { 'Desktop' }
        CLRVersion       = if ($PSVersionTable.CLRVersion) { $PSVersionTable.CLRVersion.ToString() } else { '' }
        WSManStackVersion = if ($PSVersionTable.WSManStackVersion) { $PSVersionTable.WSManStackVersion.ToString() } else { '' }
        ProcessBitness   = if ([Environment]::Is64BitProcess) { '64-bit' } else { '32-bit' }
        OSBitness        = if ([Environment]::Is64BitOperatingSystem) { '64-bit' } else { '32-bit' }
    }
    Save-ObjectData -InputObject $psInfo -BasePath (Join-Path $paths.System 'PowerShell-Version')

    $localeInfo = [pscustomobject]@{
        TimeZone       = if (Get-Command Get-TimeZone -ErrorAction SilentlyContinue) { (Get-TimeZone).Id } else { [System.TimeZoneInfo]::Local.Id }
        Culture        = (Get-Culture).Name
        UICulture      = (Get-UICulture).Name
        SystemLocale   = if (Get-Command Get-WinSystemLocale -ErrorAction SilentlyContinue) { (Get-WinSystemLocale).Name } else { '' }
        HomeLocation   = if (Get-Command Get-WinHomeLocation -ErrorAction SilentlyContinue) { (Get-WinHomeLocation).GeoId } else { '' }
    }
    Save-ObjectData -InputObject $localeInfo -BasePath (Join-Path $paths.System 'Locale-TimeZone')

    $modules = Get-Module -ListAvailable | Sort-Object Name, Version -Unique |
        Select-Object Name, Version, ModuleType, Path, Description
    Save-ObjectData -InputObject $modules -BasePath (Join-Path $paths.System 'PowerShell-Modules')
}

Invoke-Collector -Name 'Windows activation metadata' -Action {
    $activation = Get-CimInstance -ClassName SoftwareLicensingProduct -Filter "PartialProductKey IS NOT NULL" |
        Where-Object { $_.Name -like 'Windows*' } |
        Select-Object Name, Description, LicenseStatus, GracePeriodRemaining, PartialProductKey
    Save-ObjectData -InputObject $activation -BasePath (Join-Path $paths.System 'Windows-Activation-Metadata')
}

$installedFeatures = @()
Invoke-Collector -Name 'Windows roles and features' -Action {
    if (Get-Command Get-WindowsFeature -ErrorAction SilentlyContinue) {
        $script:installedFeatures = @(Get-WindowsFeature)
        Save-ObjectData -InputObject $script:installedFeatures -BasePath (Join-Path $paths.System 'All-Windows-Roles-and-Features')
        Save-ObjectData -InputObject ($script:installedFeatures | Where-Object Installed) -BasePath (Join-Path $paths.System 'Installed-Windows-Roles-and-Features')
        Save-ObjectData -InputObject ($script:installedFeatures | Where-Object { $_.Installed -and $_.FeatureType -eq 'Role' }) -BasePath (Join-Path $paths.Summary 'Installed-Server-Roles')
    }
    else {
        'Get-WindowsFeature is not available.' | Set-Content -Path (Join-Path $paths.System 'Windows-Roles-and-Features.txt') -Encoding UTF8
    }

    if (Get-Command Get-WindowsOptionalFeature -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-WindowsOptionalFeature -Online) -BasePath (Join-Path $paths.System 'Windows-Optional-Features')
    }

    if (Get-Command Get-WindowsCapability -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-WindowsCapability -Online) -BasePath (Join-Path $paths.System 'Windows-Capabilities')
    }

    if ($script:installedFeatures) {
        $installedFeatureNames = @($script:installedFeatures | Where-Object Installed | Select-Object -ExpandProperty Name | Sort-Object -Unique)
        $installedFeatureNames | Set-Content -Path (Join-Path $paths.Summary 'Installed-Feature-Names.txt') -Encoding UTF8

        $featureEntries = for ($featureIndex = 0; $featureIndex -lt $installedFeatureNames.Count; $featureIndex++) {
            $entry = "    '{0}'" -f ([string]$installedFeatureNames[$featureIndex]).Replace("'", "''")
            if ($featureIndex -lt ($installedFeatureNames.Count - 1)) {
                $entry + ','
            }
            else {
                $entry
            }
        }
        @(
            '#requires -version 3.0'
            ''
            '# Review this list before use on a replacement server.'
            '$features = @('
            $featureEntries
            ')'
            ''
            'Install-WindowsFeature -Name $features -IncludeManagementTools'
        ) | Set-Content -Path (Join-Path $paths.Summary 'Reinstall-Windows-Roles-and-Features.ps1') -Encoding UTF8
    }
}

Invoke-Collector -Name 'Windows updates and packages' -Action {
    Save-ObjectData -InputObject (Get-HotFix | Sort-Object InstalledOn) -BasePath (Join-Path $paths.System 'Installed-Hotfixes')

    if (Get-Command Get-WindowsPackage -ErrorAction SilentlyContinue) {
        $packages = Get-WindowsPackage -Online |
            Select-Object PackageName, PackageState, ReleaseType, InstallTime
        Save-ObjectData -InputObject $packages -BasePath (Join-Path $paths.System 'Windows-Packages')
    }

    $wuPolicyRows = New-Object System.Collections.ArrayList
    foreach ($registryPath in @(
        'HKLM:\\SOFTWARE\\Policies\\Microsoft\\Windows\\WindowsUpdate',
        'HKLM:\\SOFTWARE\\Policies\\Microsoft\\Windows\\WindowsUpdate\\AU',
        'HKLM:\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\WindowsUpdate\\Auto Update'
    )) {
        if (Test-Path -LiteralPath $registryPath) {
            try {
                $registryItem = Get-ItemProperty -LiteralPath $registryPath -ErrorAction Stop
                foreach ($property in $registryItem.PSObject.Properties) {
                    if ($property.Name -like 'PS*') { continue }
                    [void]$wuPolicyRows.Add([pscustomobject]@{ RegistryPath=$registryPath; Setting=$property.Name; Value=(ConvertTo-ReportText -Value $property.Value -MaxLength 2000) })
                }
            } catch { }
        }
    }
    Save-ObjectData -InputObject $wuPolicyRows -BasePath (Join-Path $paths.System 'Windows-Update-Configuration')

    $pendingRebootRows = @(
        [pscustomobject]@{ Indicator='Component Based Servicing'; Pending=(Test-Path -LiteralPath 'HKLM:\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Component Based Servicing\\RebootPending'); Detail='CBS RebootPending key' },
        [pscustomobject]@{ Indicator='Windows Update'; Pending=(Test-Path -LiteralPath 'HKLM:\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\WindowsUpdate\\Auto Update\\RebootRequired'); Detail='Windows Update RebootRequired key' }
    )
    try {
        $sessionManager = Get-ItemProperty -LiteralPath 'HKLM:\\SYSTEM\\CurrentControlSet\\Control\\Session Manager' -Name PendingFileRenameOperations -ErrorAction SilentlyContinue
        $pendingFileRename = ($null -ne $sessionManager -and $null -ne $sessionManager.PendingFileRenameOperations)
        $pendingRebootRows += [pscustomobject]@{ Indicator='Pending file rename operations'; Pending=$pendingFileRename; Detail='Session Manager PendingFileRenameOperations' }
    } catch { }
    Save-ObjectData -InputObject $pendingRebootRows -BasePath (Join-Path $paths.System 'Pending-Reboot-Indicators')

    try {
        $updateSession = New-Object -ComObject 'Microsoft.Update.Session'
        $searcher = $updateSession.CreateUpdateSearcher()
        $historyCount = [math]::Min([int]$searcher.GetTotalHistoryCount(), 500)
        if ($historyCount -gt 0) {
            $history = @($searcher.QueryHistory(0, $historyCount) | ForEach-Object {
                [pscustomobject]@{
                    Date=$_.Date; Title=$_.Title; Description=$_.Description; Operation=$_.Operation; ResultCode=$_.ResultCode; HResult=$_.HResult; ClientApplicationID=$_.ClientApplicationID; SupportUrl=$_.SupportUrl
                }
            })
            Save-ObjectData -InputObject $history -BasePath (Join-Path $paths.System 'Windows-Update-History')
        }
    } catch {
        ('Windows Update history API was unavailable: ' + $_.Exception.Message) | Set-Content -Path (Join-Path $paths.System 'Windows-Update-History-Unavailable.txt') -Encoding UTF8
    }
}

Invoke-Collector -Name 'Boot recovery power and pagefile configuration' -Action {
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_PageFileSetting) -BasePath (Join-Path $paths.System 'Pagefile-Settings')
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_PageFileUsage) -BasePath (Join-Path $paths.System 'Pagefile-Usage')
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_StartupCommand) -BasePath (Join-Path $paths.System 'Startup-Commands')

    Invoke-NativeCapture -Command 'bcdedit.exe' -Arguments @('/enum', 'all') -OutputFile (Join-Path $paths.System 'Boot-Configuration.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'reagentc.exe' -Arguments @('/info') -OutputFile (Join-Path $paths.System 'Windows-Recovery-Configuration.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'powercfg.exe' -Arguments @('/getactivescheme') -OutputFile (Join-Path $paths.System 'Active-Power-Scheme.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'powercfg.exe' -Arguments @('/query') -OutputFile (Join-Path $paths.System 'Power-Configuration.txt') -AllowNonZeroExit | Out-Null
}

Invoke-Collector -Name 'Environment variables' -Action {
    $machineEnvironment = [Environment]::GetEnvironmentVariables('Machine').GetEnumerator() |
        Sort-Object Name | ForEach-Object {
            [pscustomobject]@{ Name = $_.Key; Value = $_.Value; Scope = 'Machine' }
        }
    Save-ObjectData -InputObject $machineEnvironment -BasePath (Join-Path $paths.System 'Machine-Environment-Variables')
}

Invoke-Collector -Name 'Device and driver inventory' -Action {
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_PnPSignedDriver) -BasePath (Join-Path $paths.Hardware 'Signed-Drivers')
    Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_PnPEntity) -BasePath (Join-Path $paths.Hardware 'Plug-and-Play-Devices')
    Invoke-NativeCapture -Command 'driverquery.exe' -Arguments @('/v', '/fo', 'csv') -OutputFile (Join-Path $paths.Hardware 'DriverQuery.csv') -AllowNonZeroExit
}

Invoke-Collector -Name 'Disk partition volume and storage inventory' -Action {
    $cimDisks = @(Get-CimInstance -ClassName Win32_DiskDrive)
    $cimLogicalDisks = @(Get-CimInstance -ClassName Win32_LogicalDisk)
    $cimVolumes = @(Get-CimInstance -ClassName Win32_Volume)
    Save-ObjectData -InputObject $cimDisks -BasePath (Join-Path $paths.Storage 'Disk-Drives-CIM')
    Save-ObjectData -InputObject $cimLogicalDisks -BasePath (Join-Path $paths.Storage 'Logical-Disks-CIM')
    Save-ObjectData -InputObject $cimVolumes -BasePath (Join-Path $paths.Storage 'Volumes-CIM')
    Save-ObjectData -InputObject (New-StorageReadableRows -InputObject $cimDisks -Kind Disk) -BasePath (Join-Path $paths.Storage 'Disk-Drives-Readable')
    Save-ObjectData -InputObject (New-StorageReadableRows -InputObject $cimLogicalDisks -Kind LogicalDisk) -BasePath (Join-Path $paths.Storage 'Logical-Disks-Readable')

    if (Get-Command Get-Disk -ErrorAction SilentlyContinue) {
        $modernDisks = @(Get-Disk)
        Save-ObjectData -InputObject $modernDisks -BasePath (Join-Path $paths.Storage 'Disks')
        Save-ObjectData -InputObject (New-StorageReadableRows -InputObject $modernDisks -Kind Disk) -BasePath (Join-Path $paths.Storage 'Disks-Readable')
    }
    if (Get-Command Get-Partition -ErrorAction SilentlyContinue) {
        $modernPartitions = @(Get-Partition)
        Save-ObjectData -InputObject $modernPartitions -BasePath (Join-Path $paths.Storage 'Partitions')
        Save-ObjectData -InputObject (New-StorageReadableRows -InputObject $modernPartitions -Kind Partition) -BasePath (Join-Path $paths.Storage 'Partitions-Readable')
    }
    if (Get-Command Get-Volume -ErrorAction SilentlyContinue) {
        $modernVolumes = @(Get-Volume)
        Save-ObjectData -InputObject $modernVolumes -BasePath (Join-Path $paths.Storage 'Volumes')
        Save-ObjectData -InputObject (New-StorageReadableRows -InputObject $modernVolumes -Kind Volume) -BasePath (Join-Path $paths.Storage 'Volumes-Readable')
    }
    if (Get-Command Get-PhysicalDisk -ErrorAction SilentlyContinue) {
        $modernPhysicalDisks = @(Get-PhysicalDisk)
        Save-ObjectData -InputObject $modernPhysicalDisks -BasePath (Join-Path $paths.Storage 'Physical-Disks')
        Save-ObjectData -InputObject (New-StorageReadableRows -InputObject $modernPhysicalDisks -Kind PhysicalDisk) -BasePath (Join-Path $paths.Storage 'Physical-Disks-Readable')
    }
    if (Get-Command Get-StoragePool -ErrorAction SilentlyContinue) {
        $modernPools = @(Get-StoragePool)
        Save-ObjectData -InputObject $modernPools -BasePath (Join-Path $paths.Storage 'Storage-Pools')
        Save-ObjectData -InputObject (New-StorageReadableRows -InputObject $modernPools -Kind Pool) -BasePath (Join-Path $paths.Storage 'Storage-Pools-Readable')
    }
    if (Get-Command Get-VirtualDisk -ErrorAction SilentlyContinue) {
        $modernVirtualDisks = @(Get-VirtualDisk)
        Save-ObjectData -InputObject $modernVirtualDisks -BasePath (Join-Path $paths.Storage 'Virtual-Disks')
        Save-ObjectData -InputObject (New-StorageReadableRows -InputObject $modernVirtualDisks -Kind VirtualDisk) -BasePath (Join-Path $paths.Storage 'Virtual-Disks-Readable')
    }

    Invoke-NativeCapture -Command 'mountvol.exe' -OutputFile (Join-Path $paths.Storage 'Mount-Points.txt') -AllowNonZeroExit
    Invoke-NativeCapture -Command 'vssadmin.exe' -Arguments @('list', 'writers') -OutputFile (Join-Path $paths.Storage 'VSS-Writers.txt') -AllowNonZeroExit
    Invoke-NativeCapture -Command 'vssadmin.exe' -Arguments @('list', 'providers') -OutputFile (Join-Path $paths.Storage 'VSS-Providers.txt') -AllowNonZeroExit
}

Invoke-Collector -Name 'Network adapter and IP configuration' -Action {
    Invoke-NativeCapture -Command 'ipconfig.exe' -Arguments @('/all') -OutputFile (Join-Path $paths.Network 'IPConfig-All.txt')
    $hostsFilePath = Join-Path $env:SystemRoot 'System32\drivers\etc\hosts'
    if (Test-Path -LiteralPath $hostsFilePath) {
        try { Copy-Item -LiteralPath $hostsFilePath -Destination (Join-Path $paths.Network 'Hosts-File.txt') -Force -ErrorAction Stop } catch { Write-InventoryLog -Message ('Unable to copy hosts file: ' + $_.Exception.Message) -Level 'WARN' }
    }
    Invoke-NativeCapture -Command 'route.exe' -Arguments @('print') -OutputFile (Join-Path $paths.Network 'Route-Print.txt') -AllowNonZeroExit
    Invoke-NativeCapture -Command 'arp.exe' -Arguments @('-a') -OutputFile (Join-Path $paths.Network 'ARP-Table.txt') -AllowNonZeroExit
    Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('interface', 'ipv4', 'dump') -OutputFile (Join-Path $paths.Network 'IPv4-Netsh-Rebuild-Script.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('interface', 'ipv6', 'dump') -OutputFile (Join-Path $paths.Network 'IPv6-Netsh-Rebuild-Script.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('interface', 'portproxy', 'show', 'all') -OutputFile (Join-Path $paths.Network 'Port-Proxy.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('winhttp', 'show', 'proxy') -OutputFile (Join-Path $paths.Network 'WinHTTP-Proxy.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('interface', 'ipv4', 'show', 'config') -OutputFile (Join-Path $paths.Network 'IPv4-Configuration-Netsh.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('interface', 'ipv4', 'show', 'interfaces') -OutputFile (Join-Path $paths.Network 'IPv4-Interfaces-Netsh.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('interface', 'ipv4', 'show', 'route') -OutputFile (Join-Path $paths.Network 'IPv4-Routes-Netsh.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'netstat.exe' -Arguments @('-ano') -OutputFile (Join-Path $paths.Network 'Netstat-ANO.txt') -AllowNonZeroExit | Out-Null

    # WMI/CIM baselines are intentionally collected even when the newer NetTCPIP
    # cmdlets are present. These classes are available across the Server 2012+
    # generations and provide a stable fallback when a module is absent.
    $legacyNetworkAdapters = @(Get-CimInstance -ClassName Win32_NetworkAdapter)
    $legacyNetworkConfiguration = @(Get-CimInstance -ClassName Win32_NetworkAdapterConfiguration)
    Save-ObjectData -InputObject $legacyNetworkAdapters -BasePath (Join-Path $paths.Network 'Network-Adapters-CIM')
    Save-ObjectData -InputObject $legacyNetworkConfiguration -BasePath (Join-Path $paths.Network 'Network-Adapter-Configuration-CIM')

    if (Get-Command Get-NetAdapter -ErrorAction SilentlyContinue) {
        $adapters = @(Get-NetAdapter -IncludeHidden)
        Save-ObjectData -InputObject $adapters -BasePath (Join-Path $paths.Network 'Network-Adapters')
        Save-ObjectData -InputObject (Get-NetAdapterBinding -Name '*') -BasePath (Join-Path $paths.Network 'Network-Adapter-Bindings')

        $advanced = foreach ($adapter in $adapters) {
            try {
                Get-NetAdapterAdvancedProperty -Name $adapter.Name -AllProperties -ErrorAction Stop
            }
            catch {
                [pscustomobject]@{
                    Name          = $adapter.Name
                    DisplayName   = 'Unable to query advanced properties'
                    DisplayValue  = $_.Exception.Message
                }
            }
        }
        Save-ObjectData -InputObject $advanced -BasePath (Join-Path $paths.Network 'Network-Adapter-Advanced-Properties')
    }
    else {
        $legacyAdapterRows = foreach ($adapter in ($legacyNetworkAdapters | Where-Object { $_.PhysicalAdapter -or $_.NetConnectionID })) {
            [pscustomobject]@{
                Name                 = if ($adapter.NetConnectionID) { $adapter.NetConnectionID } else { $adapter.Name }
                InterfaceDescription = $adapter.Description
                Status               = $adapter.NetConnectionStatus
                MacAddress           = $adapter.MACAddress
                LinkSpeed            = $adapter.Speed
                MediaType            = $adapter.AdapterType
                DriverDescription    = $adapter.Description
                DriverVersion        = ''
                VlanID               = ''
                InterfaceIndex       = $adapter.InterfaceIndex
            }
        }
        Save-ObjectData -InputObject $legacyAdapterRows -BasePath (Join-Path $paths.Network 'Network-Adapters')
    }

    if (Get-Command Get-NetIPConfiguration -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-NetIPConfiguration -Detailed) -BasePath (Join-Path $paths.Network 'IP-Configuration-Detailed')
        Save-ObjectData -InputObject (Get-NetIPAddress) -BasePath (Join-Path $paths.Network 'IP-Addresses')
        Save-ObjectData -InputObject (Get-NetIPInterface) -BasePath (Join-Path $paths.Network 'IP-Interfaces')
        Save-ObjectData -InputObject (Get-NetRoute) -BasePath (Join-Path $paths.Network 'Routes')
    }
    else {
        $legacyIpRows = New-Object System.Collections.ArrayList
        $legacyInterfaceRows = New-Object System.Collections.ArrayList
        foreach ($configuration in ($legacyNetworkConfiguration | Where-Object { $_.IPEnabled })) {
            $alias = [string]$configuration.Description
            $addresses = @($configuration.IPAddress)
            for ($legacyIpIndex = 0; $legacyIpIndex -lt $addresses.Count; $legacyIpIndex++) {
                $address = [string]$addresses[$legacyIpIndex]
                if ([string]::IsNullOrWhiteSpace($address)) { continue }
                [void]$legacyIpRows.Add([pscustomobject]@{
                    InterfaceAlias = $alias
                    AddressFamily  = if ($address -match ':') { 'IPv6' } else { 'IPv4' }
                    IPAddress      = $address
                    PrefixLength   = ''
                    PrefixOrigin   = if ($configuration.DHCPEnabled) { 'Dhcp' } else { 'Manual' }
                    SuffixOrigin   = ''
                    AddressState   = ''
                    SkipAsSource   = ''
                })
            }
            [void]$legacyInterfaceRows.Add([pscustomobject]@{
                InterfaceAlias   = $alias
                AddressFamily    = 'IPv4'
                ConnectionState  = if ($configuration.IPEnabled) { 'Connected or configured' } else { 'Disconnected' }
                Dhcp             = $configuration.DHCPEnabled
                NlMtuBytes       = $configuration.MTU
                InterfaceMetric  = $configuration.IPConnectionMetric
                RouterDiscovery  = ''
                WeakHostSend     = ''
                WeakHostReceive  = ''
            })
        }
        Save-ObjectData -InputObject $legacyIpRows -BasePath (Join-Path $paths.Network 'IP-Addresses')
        Save-ObjectData -InputObject $legacyInterfaceRows -BasePath (Join-Path $paths.Network 'IP-Interfaces')

        $legacyRoutes = foreach ($route in @(Get-CimInstance -ClassName Win32_IP4RouteTable -ErrorAction SilentlyContinue)) {
            [pscustomobject]@{
                DestinationPrefix = (($route.Destination) + ' mask ' + ($route.Mask))
                NextHop           = $route.NextHop
                InterfaceAlias    = $route.InterfaceIndex
                RouteMetric       = $route.Metric1
                Protocol          = 'WMI'
                PolicyStore       = ''
            }
        }
        Save-ObjectData -InputObject $legacyRoutes -BasePath (Join-Path $paths.Network 'Routes')
    }

    if (Get-Command Get-DnsClientServerAddress -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-DnsClientServerAddress) -BasePath (Join-Path $paths.Network 'DNS-Client-Server-Addresses')
    }
    else {
        $legacyDnsRows = foreach ($configuration in ($legacyNetworkConfiguration | Where-Object { $_.IPEnabled })) {
            [pscustomobject]@{
                InterfaceAlias = [string]$configuration.Description
                InterfaceIndex = $configuration.InterfaceIndex
                AddressFamily  = 'IPv4/IPv6'
                ServerAddresses = (@($configuration.DNSServerSearchOrder) -join '; ')
            }
        }
        Save-ObjectData -InputObject $legacyDnsRows -BasePath (Join-Path $paths.Network 'DNS-Client-Server-Addresses')
    }
    if (Get-Command Get-DnsClientGlobalSetting -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-DnsClientGlobalSetting) -BasePath (Join-Path $paths.Network 'DNS-Client-Global-Settings')
    }
    if (Get-Command Get-NetConnectionProfile -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-NetConnectionProfile) -BasePath (Join-Path $paths.Network 'Network-Connection-Profiles')
    }
    if (Get-Command Get-NetNat -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-NetNat) -BasePath (Join-Path $paths.Network 'Windows-NAT')
    }
    if (Get-Command Get-NetNatStaticMapping -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-NetNatStaticMapping -ErrorAction SilentlyContinue) -BasePath (Join-Path $paths.Network 'Windows-NAT-Static-Mappings')
    }
}

Invoke-Collector -Name 'HTTP system bindings and remote management' -Action {
    Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('http', 'show', 'sslcert') -OutputFile (Join-Path $paths.Network 'HTTP-SYS-SSL-Certificate-Bindings.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('http', 'show', 'urlacl') -OutputFile (Join-Path $paths.Network 'HTTP-SYS-URL-Reservations.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('http', 'show', 'iplisten') -OutputFile (Join-Path $paths.Network 'HTTP-SYS-IP-Listen-List.txt') -AllowNonZeroExit | Out-Null

    if (Get-Command winrm.cmd -ErrorAction SilentlyContinue) {
        Invoke-NativeCapture -Command 'winrm.cmd' -Arguments @('enumerate', 'winrm/config/listener') -OutputFile (Join-Path $paths.Network 'WinRM-Listeners.txt') -AllowNonZeroExit
        Invoke-NativeCapture -Command 'winrm.cmd' -Arguments @('get', 'winrm/config/service') -OutputFile (Join-Path $paths.Network 'WinRM-Service-Configuration.txt') -AllowNonZeroExit
    }
    if (Get-Command Get-PSSessionConfiguration -ErrorAction SilentlyContinue) {
        try {
            $psRemotingEndpoints = @(Get-PSSessionConfiguration -ErrorAction Stop)
            Save-ObjectData -InputObject $psRemotingEndpoints -BasePath (Join-Path $paths.Network 'PowerShell-Remoting-Endpoints')
        }
        catch {
            ('PowerShell remoting endpoints could not be queried. This commonly means WinRM is disabled or not configured. Error: ' + $_.Exception.Message) |
                Set-Content -Path (Join-Path $paths.Network 'PowerShell-Remoting-Endpoints-Unavailable.txt') -Encoding UTF8
        }
    }
}

Invoke-Collector -Name 'Listening network endpoints' -Action {
    if (Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue) {
        $tcpListeners = foreach ($connection in (Get-NetTCPConnection -State Listen)) {
            $processName = ''
            try { $processName = (Get-Process -Id $connection.OwningProcess -ErrorAction Stop).ProcessName } catch { }
            [pscustomobject]@{
                Protocol      = 'TCP'
                LocalAddress  = $connection.LocalAddress
                LocalPort     = $connection.LocalPort
                OwningProcess = $connection.OwningProcess
                ProcessName   = $processName
                AppliedSetting = $connection.AppliedSetting
            }
        }
        Save-ObjectData -InputObject $tcpListeners -BasePath (Join-Path $paths.Network 'TCP-Listening-Endpoints')
    }

    if (Get-Command Get-NetUDPEndpoint -ErrorAction SilentlyContinue) {
        $udpListeners = foreach ($endpoint in (Get-NetUDPEndpoint)) {
            $processName = ''
            try { $processName = (Get-Process -Id $endpoint.OwningProcess -ErrorAction Stop).ProcessName } catch { }
            [pscustomobject]@{
                Protocol      = 'UDP'
                LocalAddress  = $endpoint.LocalAddress
                LocalPort     = $endpoint.LocalPort
                OwningProcess = $endpoint.OwningProcess
                ProcessName   = $processName
            }
        }
        Save-ObjectData -InputObject $udpListeners -BasePath (Join-Path $paths.Network 'UDP-Listening-Endpoints')
    }
}

Invoke-Collector -Name 'NIC teaming configuration' -Action {
    if (Get-Command Get-NetLbfoTeam -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-NetLbfoTeam) -BasePath (Join-Path $paths.Network 'LBFO-Teams')
        if (Get-Command Get-NetLbfoTeamMember -ErrorAction SilentlyContinue) {
            Save-ObjectData -InputObject (Get-NetLbfoTeamMember) -BasePath (Join-Path $paths.Network 'LBFO-Team-Members')
        }
        if (Get-Command Get-NetLbfoTeamNic -ErrorAction SilentlyContinue) {
            Save-ObjectData -InputObject (Get-NetLbfoTeamNic) -BasePath (Join-Path $paths.Network 'LBFO-Team-NICs')
        }
    }

    if (Get-Command Get-NetSwitchTeam -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-NetSwitchTeam) -BasePath (Join-Path $paths.Network 'Switch-Embedded-Teams')
    }
}

Invoke-Collector -Name 'Windows Time service configuration' -Action {
    Invoke-NativeCapture -Command 'w32tm.exe' -Arguments @('/query', '/configuration') -OutputFile (Join-Path $paths.System 'Windows-Time-Configuration.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'w32tm.exe' -Arguments @('/query', '/status') -OutputFile (Join-Path $paths.System 'Windows-Time-Status.txt') -AllowNonZeroExit | Out-Null
    Invoke-NativeCapture -Command 'w32tm.exe' -Arguments @('/query', '/peers') -OutputFile (Join-Path $paths.System 'Windows-Time-Peers.txt') -AllowNonZeroExit | Out-Null
}

Invoke-Collector -Name 'iSCSI and MPIO configuration' -Action {
    $iscsiService = Get-Service -Name 'MSiSCSI' -ErrorAction SilentlyContinue
    if ($iscsiService) {
        Save-ObjectData -InputObject ([pscustomobject]@{
            Name      = $iscsiService.Name
            Status    = $iscsiService.Status
            StartType = $iscsiService.StartType
        }) -BasePath (Join-Path $paths.Network 'iSCSI-Initiator-Service')
    }

    $canQueryIscsi = (-not $iscsiService) -or ($iscsiService.Status -eq 'Running')
    if ($canQueryIscsi) {
        foreach ($iscsiQuery in @(
            [pscustomobject]@{ Command = 'Get-IscsiTargetPortal'; File = 'iSCSI-Target-Portals' },
            [pscustomobject]@{ Command = 'Get-IscsiSession'; File = 'iSCSI-Sessions' },
            [pscustomobject]@{ Command = 'Get-IscsiConnection'; File = 'iSCSI-Connections' }
        )) {
            if (Get-Command -Name $iscsiQuery.Command -ErrorAction SilentlyContinue) {
                try {
                    $iscsiData = & $iscsiQuery.Command -ErrorAction Stop
                    Save-ObjectData -InputObject $iscsiData -BasePath (Join-Path $paths.Network $iscsiQuery.File)
                }
                catch {
                    ('The optional iSCSI query was unavailable: ' + $_.Exception.Message) |
                        Set-Content -Path (Join-Path $paths.Network ($iscsiQuery.File + '-Unavailable.txt')) -Encoding UTF8
                }
            }
        }
    }
    else {
        'The Microsoft iSCSI Initiator service is not running. No active iSCSI portals, sessions, or connections were queried, and the service was not started by this inventory tool.' |
            Set-Content -Path (Join-Path $paths.Network 'iSCSI-Not-Active.txt') -Encoding UTF8
    }

    if (Get-Command Get-MSDSMAutomaticClaimSettings -ErrorAction SilentlyContinue) {
        try {
            Save-ObjectData -InputObject (Get-MSDSMAutomaticClaimSettings -ErrorAction Stop) -BasePath (Join-Path $paths.Storage 'MPIO-Automatic-Claim-Settings')
        }
        catch {
            ('MPIO automatic-claim settings were unavailable: ' + $_.Exception.Message) |
                Set-Content -Path (Join-Path $paths.Storage 'MPIO-Automatic-Claim-Settings-Unavailable.txt') -Encoding UTF8
        }
    }
    if (Get-Command mpclaim.exe -ErrorAction SilentlyContinue) {
        Invoke-NativeCapture -Command 'mpclaim.exe' -Arguments @('-s', '-d') -OutputFile (Join-Path $paths.Storage 'MPIO-Claimed-Devices.txt') -AllowNonZeroExit | Out-Null
    }
}

Invoke-Collector -Name 'Installed applications' -Action {
    $applicationLocations = @(
        [pscustomobject]@{ Path = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*'; Architecture = '64-bit or native' },
        [pscustomobject]@{ Path = 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'; Architecture = '32-bit' }
    )

    $applications = foreach ($location in $applicationLocations) {
        Get-ItemProperty -Path $location.Path -ErrorAction SilentlyContinue |
            Where-Object { $_.DisplayName } |
            ForEach-Object {
                [pscustomobject]@{
                    DisplayName          = $_.DisplayName
                    DisplayVersion       = $_.DisplayVersion
                    Publisher            = $_.Publisher
                    InstallDate          = $_.InstallDate
                    InstallLocation      = $_.InstallLocation
                    InstallSource        = $_.InstallSource
                    UninstallString      = $_.UninstallString
                    QuietUninstallString = $_.QuietUninstallString
                    ProductCodeOrKey     = $_.PSChildName
                    Architecture         = $location.Architecture
                }
            }
    }

    $applications = $applications | Sort-Object DisplayName, DisplayVersion, Architecture -Unique
    Save-ObjectData -InputObject $applications -BasePath (Join-Path $paths.Software 'Installed-Applications')

    $directoryRoots = @($env:ProgramFiles, ${env:ProgramFiles(x86)}, $env:ProgramData) |
        Where-Object { $_ -and (Test-Path -LiteralPath $_) } | Sort-Object -Unique
    $topLevelFolders = foreach ($root in $directoryRoots) {
        Get-ChildItem -LiteralPath $root -Directory -Force -ErrorAction SilentlyContinue |
            Select-Object @{Name = 'Root'; Expression = { $root }}, Name, FullName, CreationTime, LastWriteTime, Attributes
    }
    Save-ObjectData -InputObject $topLevelFolders -BasePath (Join-Path $paths.Software 'Application-Folder-Inventory')
}

Invoke-Collector -Name 'Services and dependencies' -Action {
    $serviceCim = Get-CimInstance -ClassName Win32_Service |
        Select-Object Name, DisplayName, Description, State, Status, StartMode, StartName, PathName, ProcessId, ServiceType, ExitCode
    Save-ObjectData -InputObject $serviceCim -BasePath (Join-Path $paths.Software 'Services')

    $serviceDependencies = foreach ($service in (Get-Service)) {
        [pscustomobject]@{
            Name                 = $service.Name
            DisplayName          = $service.DisplayName
            Status               = $service.Status
            ServicesDependedOn   = (($service.ServicesDependedOn | Select-Object -ExpandProperty Name) -join '; ')
            DependentServices    = (($service.DependentServices | Select-Object -ExpandProperty Name) -join '; ')
        }
    }
    Save-ObjectData -InputObject $serviceDependencies -BasePath (Join-Path $paths.Software 'Service-Dependencies')

    $customServicePath = Join-Path $paths.Software 'Custom-Service-Configuration-Details'
    New-Item -Path $customServicePath -ItemType Directory -Force | Out-Null
    $builtInServiceAccounts = @(
        'LocalSystem',
        'NT AUTHORITY\LocalService',
        'NT AUTHORITY\NetworkService',
        'NT AUTHORITY\SYSTEM'
    )

    $customServices = foreach ($service in $serviceCim) {
        $expandedPath = [Environment]::ExpandEnvironmentVariables([string]$service.PathName)
        $pathForComparison = $expandedPath.TrimStart().TrimStart('"')
        $isWindowsBinary = $false
        if ($pathForComparison) {
            $isWindowsBinary = $pathForComparison.StartsWith($env:SystemRoot, [System.StringComparison]::OrdinalIgnoreCase) -or
                $pathForComparison.StartsWith('\SystemRoot\', [System.StringComparison]::OrdinalIgnoreCase)
        }
        $usesBuiltInAccount = $builtInServiceAccounts -contains [string]$service.StartName

        if ((-not $isWindowsBinary) -or (-not $usesBuiltInAccount)) {
            $registryPath = 'HKLM:\SYSTEM\CurrentControlSet\Services\' + $service.Name
            $registryData = $null
            try { $registryData = Get-ItemProperty -LiteralPath $registryPath -ErrorAction Stop } catch { }

            [pscustomobject]@{
                Name             = $service.Name
                DisplayName      = $service.DisplayName
                StartMode        = $service.StartMode
                StartName        = $service.StartName
                State            = $service.State
                PathName         = $service.PathName
                Description      = $service.Description
                DelayedAutoStart = if ($registryData) { $registryData.DelayedAutoStart } else { $null }
                DependOnService  = if ($registryData -and $registryData.DependOnService) { ($registryData.DependOnService -join '; ') } else { '' }
                DependOnGroup    = if ($registryData -and $registryData.DependOnGroup) { ($registryData.DependOnGroup -join '; ') } else { '' }
            }
        }
    }
    Save-ObjectData -InputObject $customServices -BasePath (Join-Path $paths.Software 'Custom-Service-Configuration')

    foreach ($service in $customServices) {
        $safeServiceName = Get-SafeFileName -Name $service.Name
        $serviceOutput = Join-Path $customServicePath ($safeServiceName + '.txt')
        @(
            'SERVICE CONFIGURATION',
            '',
            (& sc.exe qc $service.Name 2>&1),
            '',
            'FAILURE ACTIONS',
            '',
            (& sc.exe qfailure $service.Name 2>&1),
            '',
            'SERVICE SECURITY DESCRIPTOR',
            '',
            (& sc.exe sdshow $service.Name 2>&1)
        ) | Out-File -FilePath $serviceOutput -Encoding UTF8 -Width 4096
    }
}

Invoke-Collector -Name 'Scheduled tasks' -Action {
    if (-not (Get-Command Get-ScheduledTask -ErrorAction SilentlyContinue)) {
        # Legacy fallback for systems where the ScheduledTasks module is absent.
        # SCHTASKS is available throughout the Server 2012+ target range.
        Invoke-NativeCapture -Command 'schtasks.exe' -Arguments @('/query', '/fo', 'CSV', '/v') -OutputFile (Join-Path $paths.Software 'Scheduled-Tasks-SCHTASKS.csv') -AllowNonZeroExit | Out-Null
        Invoke-NativeCapture -Command 'schtasks.exe' -Arguments @('/query', '/fo', 'LIST', '/v') -OutputFile (Join-Path $paths.Software 'Scheduled-Tasks-SCHTASKS.txt') -AllowNonZeroExit | Out-Null
        'The ScheduledTasks PowerShell module is unavailable. Native SCHTASKS exports were collected instead.' |
            Set-Content -Path (Join-Path $paths.Software 'Scheduled-Tasks-PowerShell-Module-Unavailable.txt') -Encoding UTF8
        return
    }

    $taskXmlPath = Join-Path $paths.Software 'Scheduled-Task-XML-Custom'
    New-Item -Path $taskXmlPath -ItemType Directory -Force | Out-Null

    $tasks = @(Get-ScheduledTask)
    $taskRows = foreach ($task in $tasks) {
        $taskInfo = $null
        try { $taskInfo = Get-ScheduledTaskInfo -TaskName $task.TaskName -TaskPath $task.TaskPath -ErrorAction Stop } catch { }

        $actionSummary = @()
        foreach ($action in $task.Actions) {
            $parts = @($action.Execute, $action.Arguments, $action.WorkingDirectory) | Where-Object { $_ }
            if ($parts.Count -gt 0) {
                $actionSummary += ($parts -join ' ')
            }
            else {
                $actionSummary += (($action | Out-String).Trim())
            }
        }

        $triggerSummary = @()
        foreach ($trigger in $task.Triggers) {
            $triggerSummary += (($trigger | Format-List * | Out-String -Width 4096).Trim() -replace '[\r\n]+', '; ')
        }
        $settingsSummary = (($task.Settings | Format-List * | Out-String -Width 4096).Trim() -replace '[\r\n]+', '; ')

        [pscustomobject]@{
            TaskPath        = $task.TaskPath
            TaskName        = $task.TaskName
            State           = $task.State
            Author          = $task.Author
            Description     = $task.Description
            PrincipalUser   = $task.Principal.UserId
            LogonType       = $task.Principal.LogonType
            RunLevel        = $task.Principal.RunLevel
            Actions         = ($actionSummary -join ' | ')
            Triggers        = ($triggerSummary -join ' | ')
            Settings        = $settingsSummary
            LastRunTime     = if ($taskInfo) { $taskInfo.LastRunTime } else { $null }
            NextRunTime     = if ($taskInfo) { $taskInfo.NextRunTime } else { $null }
            LastTaskResult  = if ($taskInfo) { $taskInfo.LastTaskResult } else { $null }
            NumberOfMissedRuns = if ($taskInfo) { $taskInfo.NumberOfMissedRuns } else { $null }
        }
    }
    Save-ObjectData -InputObject $taskRows -BasePath (Join-Path $paths.Software 'Scheduled-Tasks')

    if (Test-ExportCategory -Category @('ScheduledTasks')) {
        foreach ($task in ($tasks | Where-Object { $_.TaskPath -notlike '\Microsoft\*' })) {
            $safeTaskPath = Get-SafeFileName -Name ($task.TaskPath.Trim('\') + '_' + $task.TaskName)
            try {
                Export-ScheduledTask -TaskName $task.TaskName -TaskPath $task.TaskPath -ErrorAction Stop |
                    Set-Content -Path (Join-Path $taskXmlPath ($safeTaskPath + '.xml')) -Encoding Unicode
            }
            catch {
                ('Task export failed: ' + $_.Exception.Message) |
                    Set-Content -Path (Join-Path $taskXmlPath ($safeTaskPath + '.error.txt')) -Encoding UTF8
            }
        }
    }
    else {
        'Scheduled Task definitions were inventoried but task XML export was not selected.' | Set-Content -Path (Join-Path $taskXmlPath 'Export-Not-Selected.txt') -Encoding UTF8
    }
}

Invoke-Collector -Name 'ODBC configuration' -Action {
    if (Get-Command Get-OdbcDsn -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-OdbcDsn) -BasePath (Join-Path $paths.Software 'ODBC-Data-Sources')
    }
    if (Get-Command Get-OdbcDriver -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-OdbcDriver) -BasePath (Join-Path $paths.Software 'ODBC-Drivers')
    }
}

Invoke-Collector -Name 'Local users and groups' -Action {
    if ($computerSystem.DomainRole -ge 4) {
        'Domain controllers do not have a normal local SAM account database. Local user and local group enumeration was skipped. Use the Active Directory exports and a supported system-state backup for domain identities.' |
            Set-Content -Path (Join-Path $paths.Security 'Local-Accounts-Not-Applicable-On-Domain-Controller.txt') -Encoding UTF8
        Invoke-NativeCapture -Command 'net.exe' -Arguments @('accounts', '/domain') -OutputFile (Join-Path $paths.Security 'Domain-Account-Policy-Net.txt') -AllowNonZeroExit
    }
    elseif (Get-Command Get-LocalUser -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-LocalUser) -BasePath (Join-Path $paths.Security 'Local-Users')
        Save-ObjectData -InputObject (Get-LocalGroup) -BasePath (Join-Path $paths.Security 'Local-Groups')

        $memberships = foreach ($group in (Get-LocalGroup)) {
            try {
                foreach ($member in (Get-LocalGroupMember -Group $group.Name -ErrorAction Stop)) {
                    [pscustomobject]@{
                        GroupName       = $group.Name
                        MemberName      = $member.Name
                        ObjectClass     = $member.ObjectClass
                        PrincipalSource = $member.PrincipalSource
                        SID             = $member.SID.Value
                    }
                }
            }
            catch {
                [pscustomobject]@{
                    GroupName       = $group.Name
                    MemberName      = ''
                    ObjectClass     = ''
                    PrincipalSource = ''
                    SID             = ''
                    Error           = $_.Exception.Message
                }
            }
        }
        Save-ObjectData -InputObject $memberships -BasePath (Join-Path $paths.Security 'Local-Group-Memberships')
        Invoke-NativeCapture -Command 'net.exe' -Arguments @('accounts') -OutputFile (Join-Path $paths.Security 'Local-Account-Policy.txt') -AllowNonZeroExit
    }
    else {
        Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_UserAccount | Where-Object LocalAccount) -BasePath (Join-Path $paths.Security 'Local-Users')
        Save-ObjectData -InputObject (Get-CimInstance -ClassName Win32_Group | Where-Object LocalAccount) -BasePath (Join-Path $paths.Security 'Local-Groups')
        Invoke-NativeCapture -Command 'net.exe' -Arguments @('user') -OutputFile (Join-Path $paths.Security 'Local-Users-Net.txt') -AllowNonZeroExit
        Invoke-NativeCapture -Command 'net.exe' -Arguments @('localgroup') -OutputFile (Join-Path $paths.Security 'Local-Groups-Net.txt') -AllowNonZeroExit
        Invoke-NativeCapture -Command 'net.exe' -Arguments @('accounts') -OutputFile (Join-Path $paths.Security 'Local-Account-Policy.txt') -AllowNonZeroExit
    }

    Invoke-NativeCapture -Command 'whoami.exe' -Arguments @('/all') -OutputFile (Join-Path $paths.Security 'Collection-Account-WhoAmI.txt') -AllowNonZeroExit
}

Invoke-Collector -Name 'Effective policy and local policy backup' -Action {
    $gpHtml = Join-Path $paths.Security 'GPResult-Computer.html'
    $gpXml = Join-Path $paths.Security 'GPResult-Computer.xml'
    # GPResult has a legacy 127-character output-path limit. Generate to a short
    # temporary path, then move the finished report into the inventory package.
    $gpTempRoot = Join-Path $env:TEMP ('SRI-GP-' + [Guid]::NewGuid().ToString('N').Substring(0, 8))
    New-Item -Path $gpTempRoot -ItemType Directory -Force | Out-Null
    try {
        $gpTempHtml = Join-Path $gpTempRoot 'computer.html'
        $gpTempXml = Join-Path $gpTempRoot 'computer.xml'
        Invoke-NativeCapture -Command 'gpresult.exe' -Arguments @('/scope', 'computer', '/h', $gpTempHtml, '/f') -OutputFile (Join-Path $paths.Logs 'GPResult-HTML.log') -AllowNonZeroExit | Out-Null
        Invoke-NativeCapture -Command 'gpresult.exe' -Arguments @('/scope', 'computer', '/x', $gpTempXml, '/f') -OutputFile (Join-Path $paths.Logs 'GPResult-XML.log') -AllowNonZeroExit | Out-Null
        if (Test-Path -LiteralPath $gpTempHtml) { Move-Item -LiteralPath $gpTempHtml -Destination $gpHtml -Force }
        if (Test-Path -LiteralPath $gpTempXml) { Move-Item -LiteralPath $gpTempXml -Destination $gpXml -Force }
    }
    finally {
        Remove-Item -LiteralPath $gpTempRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
    Invoke-NativeCapture -Command 'gpresult.exe' -Arguments @('/scope', 'computer', '/z') -OutputFile (Join-Path $paths.Security 'GPResult-Computer-Readable.txt') -AllowNonZeroExit | Out-Null

    $securityPolicy = Join-Path $paths.Security 'Local-Security-Policy.inf'
    & secedit.exe /export /cfg $securityPolicy /quiet 2>&1 |
        Out-File -FilePath (Join-Path $paths.Logs 'Secedit-Export.log') -Encoding UTF8

    $auditBackup = Join-Path $paths.Security 'Advanced-Audit-Policy.csv'
    & auditpol.exe /backup /file:$auditBackup 2>&1 |
        Out-File -FilePath (Join-Path $paths.Logs 'AuditPol-Backup.log') -Encoding UTF8
    Invoke-NativeCapture -Command 'auditpol.exe' -Arguments @('/get', '/category:*', '/r') -OutputFile (Join-Path $paths.Security 'Advanced-Audit-Policy-Readable.csv') -AllowNonZeroExit | Out-Null

    $localGpoDestination = Join-Path $paths.Security 'Local-Group-Policy-Files'
    New-Item -Path $localGpoDestination -ItemType Directory -Force | Out-Null
    foreach ($source in @(
        (Join-Path $env:SystemRoot 'System32\GroupPolicy'),
        (Join-Path $env:SystemRoot 'System32\GroupPolicyUsers')
    )) {
        if (Test-Path -LiteralPath $source) {
            $destination = Join-Path $localGpoDestination (Split-Path -Path $source -Leaf)
            Copy-Item -LiteralPath $source -Destination $destination -Recurse -Force -ErrorAction Stop
        }
    }
}

Invoke-Collector -Name 'Windows Firewall configuration' -Action {
    if (Test-ExportCategory -Category @('Firewall')) {
        $firewallBackup = Join-Path $paths.Security 'Windows-Firewall-Policy.wfw'
        & netsh.exe advfirewall export $firewallBackup 2>&1 |
            Out-File -FilePath (Join-Path $paths.Logs 'Firewall-Export.log') -Encoding UTF8
    }
    else {
        'Firewall inventory collected; native .wfw export was not selected.' | Set-Content -Path (Join-Path $paths.Security 'Firewall-Export-Not-Selected.txt') -Encoding UTF8
    }

    Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('advfirewall', 'show', 'allprofiles') -OutputFile (Join-Path $paths.Security 'Windows-Firewall-Profiles.txt') -AllowNonZeroExit | Out-Null

    if (Get-Command Get-NetFirewallRule -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-NetFirewallProfile) -BasePath (Join-Path $paths.Security 'Firewall-Profiles')
        Save-ObjectData -InputObject (Get-NetFirewallRule) -BasePath (Join-Path $paths.Security 'Firewall-Rules')
        Save-ObjectData -InputObject (Get-NetFirewallPortFilter) -BasePath (Join-Path $paths.Security 'Firewall-Port-Filters')
        Save-ObjectData -InputObject (Get-NetFirewallAddressFilter) -BasePath (Join-Path $paths.Security 'Firewall-Address-Filters')
        Save-ObjectData -InputObject (Get-NetFirewallApplicationFilter) -BasePath (Join-Path $paths.Security 'Firewall-Application-Filters')
        Save-ObjectData -InputObject (Get-NetFirewallServiceFilter) -BasePath (Join-Path $paths.Security 'Firewall-Service-Filters')
        Save-ObjectData -InputObject (Get-NetFirewallInterfaceTypeFilter) -BasePath (Join-Path $paths.Security 'Firewall-Interface-Type-Filters')
    }
    if (Get-Command Get-NetIPsecRule -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-NetIPsecRule -ErrorAction SilentlyContinue) -BasePath (Join-Path $paths.Security 'IPsec-Rules')
    }
    if (Get-Command Get-NetIPsecMainModeRule -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-NetIPsecMainModeRule -ErrorAction SilentlyContinue) -BasePath (Join-Path $paths.Security 'IPsec-Main-Mode-Rules')
    }
    if (Get-Command Get-NetIPsecQuickModeRule -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-NetIPsecQuickModeRule -ErrorAction SilentlyContinue) -BasePath (Join-Path $paths.Security 'IPsec-Quick-Mode-Rules')
    }
}

Invoke-Collector -Name 'Windows Defender and security metadata' -Action {
    if (Get-Command Get-MpComputerStatus -ErrorAction SilentlyContinue) {
        try {
            Save-ObjectData -InputObject (Get-MpComputerStatus -ErrorAction Stop) -BasePath (Join-Path $paths.Security 'Microsoft-Defender-Status')
        }
        catch {
            ('Microsoft Defender status could not be queried. Defender may be disabled, stopped, or replaced by another security product. Error: ' + $_.Exception.Message) |
                Set-Content -Path (Join-Path $paths.Security 'Microsoft-Defender-Status-Unavailable.txt') -Encoding UTF8
        }
    }
    if (Get-Command Get-MpPreference -ErrorAction SilentlyContinue) {
        try {
            Save-ObjectData -InputObject (Get-MpPreference -ErrorAction Stop) -BasePath (Join-Path $paths.Security 'Microsoft-Defender-Preferences')
        }
        catch {
            ('Microsoft Defender preferences could not be queried. Defender may be disabled, stopped, or replaced by another security product. Error: ' + $_.Exception.Message) |
                Set-Content -Path (Join-Path $paths.Security 'Microsoft-Defender-Preferences-Unavailable.txt') -Encoding UTF8
        }
    }
    if (Get-Command Get-TlsCipherSuite -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-TlsCipherSuite) -BasePath (Join-Path $paths.Security 'TLS-Cipher-Suites')
    }
    if (Get-Command Get-TlsEccCurve -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-TlsEccCurve) -BasePath (Join-Path $paths.Security 'TLS-ECC-Curves')
    }

    Export-RegistryKey -RegistryPath 'HKLM\SYSTEM\CurrentControlSet\Control\SecurityProviders\SCHANNEL' -DestinationFile (Join-Path $paths.Security 'SCHANNEL-Registry.reg')
    Export-RegistryKey -RegistryPath 'HKLM\SYSTEM\CurrentControlSet\Control\Terminal Server' -DestinationFile (Join-Path $paths.Security 'Remote-Desktop-Registry.reg')
    Export-RegistryKey -RegistryPath 'HKLM\SOFTWARE\Policies' -DestinationFile (Join-Path $paths.Security 'Machine-Policy-Registry.reg')
}

Invoke-Collector -Name 'BitLocker status without recovery keys' -Action {
    if (Get-Command Get-BitLockerVolume -ErrorAction SilentlyContinue) {
        $bitLocker = Get-BitLockerVolume | Select-Object MountPoint, VolumeType, CapacityGB, VolumeStatus, EncryptionPercentage, EncryptionMethod, ProtectionStatus, LockStatus, AutoUnlockEnabled
        Save-ObjectData -InputObject $bitLocker -BasePath (Join-Path $paths.Security 'BitLocker-Status')
    }
    elseif (Get-Command manage-bde.exe -ErrorAction SilentlyContinue) {
        Invoke-NativeCapture -Command 'manage-bde.exe' -Arguments @('-status') -OutputFile (Join-Path $paths.Security 'BitLocker-Status.txt') -AllowNonZeroExit | Out-Null
    }
    else {
        'BitLocker management cmdlets and manage-bde.exe are not installed or available on this server. No recovery keys were requested or exported.' |
            Set-Content -Path (Join-Path $paths.Security 'BitLocker-Tools-Not-Available.txt') -Encoding UTF8
    }
}

Invoke-Collector -Name 'Certificate inventory without private keys' -Action {
    $certificateObjects = @(Get-ChildItem -Path Cert:\LocalMachine -Recurse -ErrorAction SilentlyContinue | Where-Object { $_ -is [System.Security.Cryptography.X509Certificates.X509Certificate2] })
    $certificates = foreach ($certificate in $certificateObjects) {
        $eku = ''
        try { $eku = (($certificate.EnhancedKeyUsageList | ForEach-Object { $_.FriendlyName }) -join '; ') } catch { }
        $dnsNames = ''
        try {
            if ($certificate.PSObject.Properties['DnsNameList']) {
                $dnsNames = (($certificate.DnsNameList | ForEach-Object { $_.Unicode }) -join '; ')
            }
        }
        catch { }

        [pscustomobject]@{
            StorePath              = ($certificate.PSParentPath -replace '^Microsoft\.PowerShell\.Security\\Certificate::', '')
            Subject                = $certificate.Subject
            Issuer                 = $certificate.Issuer
            Thumbprint             = $certificate.Thumbprint
            SerialNumber           = $certificate.SerialNumber
            NotBefore              = $certificate.NotBefore
            NotAfter               = $certificate.NotAfter
            HasPrivateKey          = $certificate.HasPrivateKey
            SignatureAlgorithm     = $certificate.SignatureAlgorithm.FriendlyName
            PublicKeyAlgorithm     = $certificate.PublicKey.Oid.FriendlyName
            EnhancedKeyUsage       = $eku
            DNSNames               = $dnsNames
            FriendlyName           = $certificate.FriendlyName
        }
    }
    Save-ObjectData -InputObject $certificates -BasePath (Join-Path $paths.Security 'Local-Machine-Certificates')

    if (Test-ExportCategory -Category @('Certificates')) {
        $publicExportPath = Join-Path $paths.Security 'Public-Certificate-Exports'
        New-Item -Path $publicExportPath -ItemType Directory -Force | Out-Null
        foreach ($certificate in $certificateObjects) {
            try {
                $safeSubject = Get-SafeFileName -Name ([string]$certificate.Subject)
                if ($safeSubject.Length -gt 80) { $safeSubject = $safeSubject.Substring(0,80) }
                $fileName = '{0}_{1}.cer' -f $safeSubject, $certificate.Thumbprint
                $bytes = $certificate.Export([System.Security.Cryptography.X509Certificates.X509ContentType]::Cert)
                [System.IO.File]::WriteAllBytes((Join-Path $publicExportPath $fileName), $bytes)
            } catch { }
        }
        'Only public certificate data was exported. Private keys, PFX files, passwords, and other reusable secrets are intentionally not exported by this module.' |
            Set-Content -Path (Join-Path $publicExportPath 'README-NO-PRIVATE-KEYS.txt') -Encoding UTF8
    }
}

Invoke-Collector -Name 'SMB server and share configuration' -Action {
    Invoke-NativeCapture -Command 'net.exe' -Arguments @('share') -OutputFile (Join-Path $paths.Shares 'NET-SHARE.txt') -AllowNonZeroExit | Out-Null
    if (-not (Get-Command Get-SmbShare -ErrorAction SilentlyContinue)) {
        # WMI fallback keeps share names and paths available on older or reduced
        # installations even when the SMBShare module is unavailable.
        $legacyShares = @(Get-CimInstance -ClassName Win32_Share | Where-Object { $_.Type -eq 0 -or $_.Type -eq 2147483648 })
        $shares = foreach ($share in $legacyShares) {
            [pscustomobject]@{
                Name                  = $share.Name
                Path                  = $share.Path
                Description           = $share.Description
                ScopeName             = '*'
                Special               = ($share.Name -match '\$$')
                Temporary             = $false
                FolderEnumerationMode = ''
                CachingMode           = ''
                EncryptData           = ''
                ContinuouslyAvailable = ''
            }
        }
        Save-ObjectData -InputObject $shares -BasePath (Join-Path $paths.Shares 'SMB-Shares')
        'The SMBShare PowerShell module is unavailable. Share names and paths were collected from Win32_Share and NET SHARE; modern SMB server settings and share-access cmdlets are unavailable.' |
            Set-Content -Path (Join-Path $paths.Shares 'SMBShare-Module-Unavailable.txt') -Encoding UTF8
    }
    else {
        $shares = @(Get-SmbShare)
        Save-ObjectData -InputObject $shares -BasePath (Join-Path $paths.Shares 'SMB-Shares')
    }

    if (Get-Command Get-SmbServerConfiguration -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-SmbServerConfiguration) -BasePath (Join-Path $paths.Shares 'SMB-Server-Configuration')
    }
    if (Get-Command Get-SmbClientConfiguration -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-SmbClientConfiguration) -BasePath (Join-Path $paths.Shares 'SMB-Client-Configuration')
    }

    $shareAccess = foreach ($share in $shares) {
        try {
            if (Get-Command Get-SmbShareAccess -ErrorAction SilentlyContinue) {
                Get-SmbShareAccess -Name $share.Name -ErrorAction Stop |
                    Select-Object @{Name = 'ShareName'; Expression = { $share.Name }}, AccountName, AccessControlType, AccessRight
            }
            else {
                [pscustomobject]@{
                    ShareName         = $share.Name
                    AccountName       = ''
                    AccessControlType = ''
                    AccessRight       = ''
                    Error             = 'Get-SmbShareAccess is unavailable on this operating system or installation.'
                }
            }
        }
        catch {
            [pscustomobject]@{
                ShareName        = $share.Name
                AccountName      = ''
                AccessControlType = ''
                AccessRight      = ''
                Error            = $_.Exception.Message
            }
        }
    }
    Save-ObjectData -InputObject $shareAccess -BasePath (Join-Path $paths.Shares 'SMB-Share-Permissions')

    $rootAclRows = foreach ($share in ($shares | Where-Object { $_.Path -and (Test-Path -LiteralPath $_.Path) })) {
        try {
            $acl = Get-Acl -LiteralPath $share.Path -ErrorAction Stop
            if ($acl.Access.Count -eq 0) {
                [pscustomobject]@{
                    ShareName            = $share.Name
                    Path                 = $share.Path
                    Owner                = $acl.Owner
                    IdentityReference    = ''
                    AccessControlType    = ''
                    FileSystemRights     = ''
                    InheritanceFlags     = ''
                    PropagationFlags     = ''
                    IsInherited          = ''
                }
            }
            else {
                foreach ($entry in $acl.Access) {
                    [pscustomobject]@{
                        ShareName         = $share.Name
                        Path              = $share.Path
                        Owner             = $acl.Owner
                        IdentityReference = $entry.IdentityReference.Value
                        AccessControlType = $entry.AccessControlType
                        FileSystemRights  = $entry.FileSystemRights
                        InheritanceFlags  = $entry.InheritanceFlags
                        PropagationFlags  = $entry.PropagationFlags
                        IsInherited       = $entry.IsInherited
                    }
                }
            }
        }
        catch {
            [pscustomobject]@{
                ShareName = $share.Name
                Path      = $share.Path
                Error     = $_.Exception.Message
            }
        }
    }
    Save-ObjectData -InputObject $rootAclRows -BasePath (Join-Path $paths.Shares 'Share-Root-NTFS-Permissions')

    if (Test-ExportCategory -Category @('SharesPermissions')) {
        $shareReferencePath = Join-Path $paths.Shares 'SMB-Share-Recreation-REFERENCE.ps1'
        $shareReferenceLines = New-Object System.Collections.ArrayList
        [void]$shareReferenceLines.Add('# SMB share recreation reference generated by Server Inventory & Configuration Exporter.')
        [void]$shareReferenceLines.Add('# REVIEW BEFORE USE. This file is never executed automatically. Confirm paths, accounts, rights, clustering, continuously available settings, caching, and security on the target server.')
        [void]$shareReferenceLines.Add('')
        foreach ($share in @($shares | Where-Object { (-not $_.Special) -and $_.Name -and $_.Path })) {
            $nameQuoted = ([string]$share.Name).Replace("'", "''")
            $pathQuoted = ([string]$share.Path).Replace("'", "''")
            $descriptionQuoted = ([string]$share.Description).Replace("'", "''")
            [void]$shareReferenceLines.Add(("# Share: {0}" -f $share.Name))
            [void]$shareReferenceLines.Add(("New-SmbShare -Name '{0}' -Path '{1}' -Description '{2}'" -f $nameQuoted, $pathQuoted, $descriptionQuoted))
            foreach ($permission in @($shareAccess | Where-Object { $_.ShareName -eq $share.Name -and $_.AccountName })) {
                $accountQuoted = ([string]$permission.AccountName).Replace("'", "''")
                if ([string]$permission.AccessControlType -eq 'Deny') {
                    [void]$shareReferenceLines.Add(("Block-SmbShareAccess -Name '{0}' -AccountName '{1}' -Force" -f $nameQuoted, $accountQuoted))
                }
                elseif ($permission.AccessRight) {
                    [void]$shareReferenceLines.Add(("Grant-SmbShareAccess -Name '{0}' -AccountName '{1}' -AccessRight {2} -Force" -f $nameQuoted, $accountQuoted, [string]$permission.AccessRight))
                }
            }
            [void]$shareReferenceLines.Add('')
        }
        $shareReferenceLines | Set-Content -Path $shareReferencePath -Encoding UTF8
        @(
            'This reference does not create directories, restore file data, restore ownership, restore NTFS ACLs, supply service-account passwords, or validate target-side identities.',
            'Create/restore data paths first. Review the generated commands. Restore NTFS ACLs only against the correct root paths and validate share/NTFS effective access afterward.'
        ) | Set-Content -Path (Join-Path $paths.Shares 'SMB-Share-Recreation-Guidance.txt') -Encoding UTF8
    }

    # BEGIN APPROVED ACL EXPORT INVOCATION
    if ($IncludeShareAclBackup -or (Test-ExportCategory -Category @('SharesPermissions'))) {
        Invoke-RecursiveAclExports -Shares @($shares) -Destination (Join-Path $paths.Shares 'Recursive-ICACLS-Backups')
    }
    # END APPROVED ACL EXPORT INVOCATION
}

# BEGIN APPROVED ACL EXPORT TERMINATION GUARD
if ($script:aclExportUnsafePending) {
    throw 'An owned ACL export child or output stream could not be confirmed stopped. Collection stopped; no completion marker or ZIP will be created. Partial ACL files are incomplete.'
}
# END APPROVED ACL EXPORT TERMINATION GUARD

Invoke-Collector -Name 'Existing Windows Server Backup metadata' -Action {
    if (Get-Command wbadmin.exe -ErrorAction SilentlyContinue) {
        Invoke-NativeCapture -Command 'wbadmin.exe' -Arguments @('get', 'status') -OutputFile (Join-Path $paths.System 'WBAdmin-Current-Status.txt') -AllowNonZeroExit | Out-Null
        Invoke-NativeCapture -Command 'wbadmin.exe' -Arguments @('get', 'versions') -OutputFile (Join-Path $paths.System 'WBAdmin-Backup-Versions.txt') -AllowNonZeroExit | Out-Null
        Invoke-NativeCapture -Command 'wbadmin.exe' -Arguments @('get', 'disks') -OutputFile (Join-Path $paths.System 'WBAdmin-Disks.txt') -AllowNonZeroExit | Out-Null
    }

    if (Get-Command Get-WBPolicy -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-WBPolicy -ErrorAction SilentlyContinue) -BasePath (Join-Path $paths.System 'Windows-Backup-Policy')
    }
    if (Get-Command Get-WBSchedule -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-WBSchedule -ErrorAction SilentlyContinue) -BasePath (Join-Path $paths.System 'Windows-Backup-Schedule')
    }
    if (Get-Command Get-WBSummary -ErrorAction SilentlyContinue) {
        Save-ObjectData -InputObject (Get-WBSummary -ErrorAction SilentlyContinue) -BasePath (Join-Path $paths.System 'Windows-Backup-Summary')
    }
}

Invoke-Collector -Name 'Advanced storage and file service configuration' -Action {
    $advancedStoragePath = Join-Path $paths.Storage 'Advanced-Storage-and-File-Services'
    New-Item -Path $advancedStoragePath -ItemType Directory -Force | Out-Null
    $collected = $false

    $advancedStorageCommands = @(
        @{ Command = 'Get-DedupVolume'; File = 'Data-Deduplication-Volumes' },
        @{ Command = 'Get-DedupSchedule'; File = 'Data-Deduplication-Schedules' },
        @{ Command = 'Get-DedupStatus'; File = 'Data-Deduplication-Status' },
        @{ Command = 'Get-SRGroup'; File = 'Storage-Replica-Groups' },
        @{ Command = 'Get-SRPartnership'; File = 'Storage-Replica-Partnerships' },
        @{ Command = 'Get-IscsiServerTarget'; File = 'iSCSI-Server-Targets' },
        @{ Command = 'Get-IscsiVirtualDisk'; File = 'iSCSI-Virtual-Disks' },
        @{ Command = 'Get-IscsiVirtualDiskTargetMapping'; File = 'iSCSI-Virtual-Disk-Target-Mappings' },
        @{ Command = 'Get-NfsShare'; File = 'NFS-Shares' },
        @{ Command = 'Get-NfsServerConfiguration'; File = 'NFS-Server-Configuration' },
        @{ Command = 'Get-NfsClientConfiguration'; File = 'NFS-Client-Configuration' }
    )

    foreach ($entry in $advancedStorageCommands) {
        $commandName = [string]$entry.Command
        if (Get-Command -Name $commandName -ErrorAction SilentlyContinue) {
            try {
                $data = & $commandName -ErrorAction SilentlyContinue
                Save-ObjectData -InputObject $data -BasePath (Join-Path $advancedStoragePath $entry.File)
                $collected = $true
            }
            catch {
                ('Command failed: ' + $_.Exception.Message) |
                    Set-Content -Path (Join-Path $advancedStoragePath ($entry.File + '-Error.txt')) -Encoding UTF8
            }
        }
    }

    if (-not $collected) {
        'No advanced storage, data deduplication, Storage Replica, iSCSI Target Server, or NFS cmdlets were detected.' |
            Set-Content -Path (Join-Path $advancedStoragePath 'No-Advanced-Storage-Features-Detected.txt') -Encoding UTF8
    }
}

# Role detection based on installed features and services.
$featureNames = @()
if ($script:installedFeatures) {
    $featureNames = @($script:installedFeatures | Where-Object Installed | Select-Object -ExpandProperty Name)
}
$serviceNames = @(Get-Service | Select-Object -ExpandProperty Name)

if (($computerSystem.DomainRole -ge 4) -or ($featureNames -contains 'AD-Domain-Services')) {
    Add-DetectedRole -Role 'Active Directory Domain Services' -DetectedBy 'Domain role or AD-Domain-Services feature' -Notes 'Requires supported AD recovery and system-state protection.'
}
if (($featureNames -contains 'DNS') -or ($serviceNames -contains 'DNS')) {
    Add-DetectedRole -Role 'DNS Server' -DetectedBy 'DNS feature or DNS service'
}
if (($featureNames -contains 'DHCP') -or ($serviceNames -contains 'DHCPServer')) {
    Add-DetectedRole -Role 'DHCP Server' -DetectedBy 'DHCP feature or DHCPServer service'
}
if (($featureNames -contains 'Web-Server') -or ($serviceNames -contains 'W3SVC')) {
    Add-DetectedRole -Role 'IIS Web Server' -DetectedBy 'Web-Server feature or W3SVC service' -Notes 'Website content, application data, and private certificate keys require separate backup.'
}
if (($featureNames -contains 'Hyper-V') -or ($serviceNames -contains 'vmms')) {
    Add-DetectedRole -Role 'Hyper-V' -DetectedBy 'Hyper-V feature or VMMS service' -Notes 'VM configuration and VHD/VHDX data require a VM-aware backup or export.'
}
if (($featureNames -contains 'Failover-Clustering') -or ($serviceNames -contains 'ClusSvc')) {
    Add-DetectedRole -Role 'Failover Clustering' -DetectedBy 'Failover-Clustering feature or ClusSvc service'
}
if (($featureNames -contains 'NPAS') -or ($serviceNames -contains 'IAS')) {
    Add-DetectedRole -Role 'Network Policy Server' -DetectedBy 'NPAS feature or IAS service' -Notes 'Full export contains unencrypted RADIUS shared secrets.'
}
if (($featureNames -contains 'RemoteAccess') -or ($serviceNames -contains 'RemoteAccess')) {
    Add-DetectedRole -Role 'Routing and Remote Access' -DetectedBy 'RemoteAccess feature or service' -Notes 'Routing, VPN, NAT, and demand-dial configuration should be reviewed before migration or recovery.'
}
if (($featureNames -contains 'Print-Server') -or ($serviceNames -contains 'Spooler')) {
    $printerCount = 0
    if (Get-Command Get-Printer -ErrorAction SilentlyContinue) {
        try { $printerCount = @(Get-Printer).Count } catch { }
    }
    if ($printerCount -gt 0 -or ($featureNames -contains 'Print-Server')) {
        Add-DetectedRole -Role 'Print Server' -DetectedBy 'Print-Server feature or installed printer queues'
    }
}
if (($featureNames -contains 'ADCS-Cert-Authority') -or ($serviceNames -contains 'CertSvc')) {
    Add-DetectedRole -Role 'Active Directory Certificate Services' -DetectedBy 'ADCS feature or CertSvc service' -Notes 'CA database and CA private key require a supported, secured CA backup.'
}
if (($featureNames -contains 'UpdateServices') -or ($serviceNames -contains 'WsusService')) {
    Add-DetectedRole -Role 'WSUS' -DetectedBy 'UpdateServices feature or WsusService service' -Notes 'WSUS database and content require separate backup or rebuild planning.'
}
if ($serviceNames | Where-Object { $_ -like 'MSSQL*' -or $_ -like 'SQLAgent*' }) {
    Add-DetectedRole -Role 'Microsoft SQL Server' -DetectedBy 'SQL Server services' -Notes 'Database and encryption-key backups must be created with SQL-aware procedures.'
}

$detectedRoleRows = @(foreach ($detectedRole in $script:detectedRoles) { $detectedRole })
Save-ObjectData -InputObject $detectedRoleRows -BasePath (Join-Path $paths.Summary 'Detected-Critical-Roles-and-Platforms')
Save-ObjectData -InputObject (Get-SelectedCategoryRows -Mode Inventory) -BasePath (Join-Path $paths.Summary 'Selected-Inventory-Categories')
Save-ObjectData -InputObject (Get-SelectedCategoryRows -Mode Export) -BasePath (Join-Path $paths.Summary 'Selected-Backup-Export-Modules')
$detectedRoleNames = @($detectedRoleRows | Select-Object -ExpandProperty Role)

if ($detectedRoleNames -contains 'DHCP Server') {
    Invoke-Collector -Name 'DHCP Server export' -Action {
        $rolePath = Join-Path $paths.Roles 'DHCP'
        New-Item -Path $rolePath -ItemType Directory -Force | Out-Null

        if (-not (Get-Command Get-DhcpServerv4Scope -ErrorAction SilentlyContinue)) {
            Import-Module DhcpServer -ErrorAction Stop
        }

        if (Test-ExportCategory -Category @('DHCP')) {
            if (Get-Command Export-DhcpServer -ErrorAction SilentlyContinue) {
                Export-DhcpServer -ComputerName $env:COMPUTERNAME -File (Join-Path $rolePath 'DHCP-Configuration-and-Leases.xml') -Leases -Force
            }
            $databaseBackup = Join-Path $rolePath 'DHCP-Database-Backup'
            New-Item -Path $databaseBackup -ItemType Directory -Force | Out-Null
            if (Get-Command Backup-DhcpServer -ErrorAction SilentlyContinue) {
                Backup-DhcpServer -ComputerName $env:COMPUTERNAME -Path $databaseBackup -Confirm:$false
            }
            'DHCP export selected. Restore only during an approved recovery operation. Typical import command: Import-DhcpServer -ComputerName TARGET -File DHCP-Configuration-and-Leases.xml -BackupPath <backup-folder> -Leases -ScopeOverwrite -Force' |
                Set-Content -Path (Join-Path $rolePath 'RESTORE-GUIDANCE.txt') -Encoding UTF8
        }
        else {
            'DHCP inventory was collected. Configuration backup/export was not selected for this run.' | Set-Content -Path (Join-Path $rolePath 'Export-Not-Selected.txt') -Encoding UTF8
        }

        if (Get-Command Get-DhcpServerv4Scope -ErrorAction SilentlyContinue) {
            $scopes = @(Get-DhcpServerv4Scope -ComputerName $env:COMPUTERNAME)
            Save-ObjectData -InputObject $scopes -BasePath (Join-Path $rolePath 'IPv4-Scopes')
            Save-ObjectData -InputObject (Get-DhcpServerv4OptionValue -ComputerName $env:COMPUTERNAME) -BasePath (Join-Path $rolePath 'IPv4-Server-Options')

            $reservations = foreach ($scope in $scopes) {
                Get-DhcpServerv4Reservation -ComputerName $env:COMPUTERNAME -ScopeId $scope.ScopeId -ErrorAction SilentlyContinue
            }
            Save-ObjectData -InputObject $reservations -BasePath (Join-Path $rolePath 'IPv4-Reservations')

            $scopeOptions = foreach ($scope in $scopes) {
                Get-DhcpServerv4OptionValue -ComputerName $env:COMPUTERNAME -ScopeId $scope.ScopeId -ErrorAction SilentlyContinue |
                    Select-Object @{Name = 'ScopeId'; Expression = { $scope.ScopeId }}, OptionId, Name, Type, Value, VendorClass, UserClass, PolicyName
            }
            Save-ObjectData -InputObject $scopeOptions -BasePath (Join-Path $rolePath 'IPv4-Scope-Options')

            $exclusions = foreach ($scope in $scopes) { Get-DhcpServerv4ExclusionRange -ComputerName $env:COMPUTERNAME -ScopeId $scope.ScopeId -ErrorAction SilentlyContinue | Select-Object @{Name='ScopeId';Expression={$scope.ScopeId}}, StartRange, EndRange }
            Save-ObjectData -InputObject $exclusions -BasePath (Join-Path $rolePath 'IPv4-Exclusion-Ranges')
            $leases = foreach ($scope in $scopes) { Get-DhcpServerv4Lease -ComputerName $env:COMPUTERNAME -ScopeId $scope.ScopeId -AllLeases -ErrorAction SilentlyContinue }
            Save-ObjectData -InputObject $leases -BasePath (Join-Path $rolePath 'IPv4-Leases')
            if (Get-Command Get-DhcpServerv4Policy -ErrorAction SilentlyContinue) {
                $policies = foreach ($scope in $scopes) { Get-DhcpServerv4Policy -ComputerName $env:COMPUTERNAME -ScopeId $scope.ScopeId -ErrorAction SilentlyContinue | Select-Object @{Name='ScopeId';Expression={$scope.ScopeId}}, * }
                Save-ObjectData -InputObject $policies -BasePath (Join-Path $rolePath 'IPv4-Policies')
            }
            if (Get-Command Get-DhcpServerv4ScopeStatistics -ErrorAction SilentlyContinue) {
                Save-ObjectData -InputObject (Get-DhcpServerv4ScopeStatistics -ComputerName $env:COMPUTERNAME -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'IPv4-Scope-Statistics')
            }
            if (Get-Command Get-DhcpServerv4Binding -ErrorAction SilentlyContinue) {
                Save-ObjectData -InputObject (Get-DhcpServerv4Binding -ComputerName $env:COMPUTERNAME -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'IPv4-Bindings')
            }
        }

        if (Get-Command Get-DhcpServerv4Failover -ErrorAction SilentlyContinue) {
            Save-ObjectData -InputObject (Get-DhcpServerv4Failover -ComputerName $env:COMPUTERNAME) -BasePath (Join-Path $rolePath 'IPv4-Failover')
        }
        if (Get-Command Get-DhcpServerv4Filter -ErrorAction SilentlyContinue) {
            Save-ObjectData -InputObject (Get-DhcpServerv4Filter -ComputerName $env:COMPUTERNAME) -BasePath (Join-Path $rolePath 'IPv4-Filters')
        }
    }
}

if ($detectedRoleNames -contains 'DNS Server') {
    Invoke-Collector -Name 'DNS Server inventory and zone export' -Action {
        $rolePath = Join-Path $paths.Roles 'DNS'
        New-Item -Path $rolePath -ItemType Directory -Force | Out-Null

        if (-not (Get-Command Get-DnsServerZone -ErrorAction SilentlyContinue)) {
            Import-Module DnsServer -ErrorAction Stop
        }

        $zones = @(Get-DnsServerZone -ComputerName $env:COMPUTERNAME)
        Save-ObjectData -InputObject $zones -BasePath (Join-Path $rolePath 'DNS-Zones')

        foreach ($zone in $zones) {
            $safeZone = Get-SafeFileName -Name $zone.ZoneName
            $zoneFolder = Join-Path $rolePath ('Zone-' + $safeZone)
            New-Item -Path $zoneFolder -ItemType Directory -Force | Out-Null

            try {
                $records = @(Get-DnsServerResourceRecord -ComputerName $env:COMPUTERNAME -ZoneName $zone.ZoneName -ErrorAction Stop)
                Save-ObjectData -InputObject $records -BasePath (Join-Path $zoneFolder 'Resource-Records-Raw')

                $readableRecords = foreach ($record in $records) {
                    [pscustomobject]@{
                        HostName    = $record.HostName
                        RecordType  = $record.RecordType
                        RecordClass = $record.RecordClass
                        TimeToLive  = $record.TimeToLive
                        Timestamp   = $record.Timestamp
                        RecordData  = (($record.RecordData | Format-List * | Out-String -Width 4096).Trim() -replace '[\r\n]+', '; ')
                    }
                }
                Save-ObjectData -InputObject $readableRecords -BasePath (Join-Path $zoneFolder 'Resource-Records-Readable')
            }
            catch {
                ('Record export failed: ' + $_.Exception.Message) |
                    Set-Content -Path (Join-Path $zoneFolder 'Record-Export-Error.txt') -Encoding UTF8
            }

            if ((Test-ExportCategory -Category @('DNS')) -and $zone.IsDsIntegrated -and (Get-Command Export-DnsServerZone -ErrorAction SilentlyContinue)) {
                try {
                    $temporaryFileName = 'InventoryExport_{0}_{1}.txt' -f $safeZone, $timestamp
                    Export-DnsServerZone -ComputerName $env:COMPUTERNAME -Name $zone.ZoneName -FileName $temporaryFileName -Confirm:$false
                    $temporaryFilePath = Join-Path (Join-Path $env:SystemRoot 'System32\dns') $temporaryFileName
                    if (Test-Path -LiteralPath $temporaryFilePath) {
                        Copy-Item -LiteralPath $temporaryFilePath -Destination (Join-Path $zoneFolder 'AD-Integrated-Zone-Export.txt') -Force
                        Remove-Item -LiteralPath $temporaryFilePath -Force -ErrorAction SilentlyContinue
                    }
                }
                catch {
                    ('DNS zone file export failed: ' + $_.Exception.Message) |
                        Set-Content -Path (Join-Path $zoneFolder 'Zone-File-Export-Error.txt') -Encoding UTF8
                }
            }
            elseif ((Test-ExportCategory -Category @('DNS')) -and $zone.ZoneFile) {
                try {
                    $zoneFilePath = Join-Path (Join-Path $env:SystemRoot 'System32\dns') ([string]$zone.ZoneFile)
                    if (Test-Path -LiteralPath $zoneFilePath) {
                        Copy-Item -LiteralPath $zoneFilePath -Destination (Join-Path $zoneFolder ('File-Backed-Zone-' + (Split-Path -Path $zoneFilePath -Leaf))) -Force
                    }
                    else {
                        ('Configured zone file was not found at ' + $zoneFilePath) |
                            Set-Content -Path (Join-Path $zoneFolder 'Zone-File-Not-Found.txt') -Encoding UTF8
                    }
                }
                catch {
                    ('File-backed DNS zone copy failed: ' + $_.Exception.Message) |
                        Set-Content -Path (Join-Path $zoneFolder 'Zone-File-Copy-Error.txt') -Encoding UTF8
                }
            }
        }

        $dnsCommands = @(
            @{ Name = 'Server-Settings'; Command = 'Get-DnsServerSetting'; Arguments = @{ All = $true } },
            @{ Name = 'Forwarders'; Command = 'Get-DnsServerForwarder'; Arguments = @{} },
            @{ Name = 'Root-Hints'; Command = 'Get-DnsServerRootHint'; Arguments = @{} },
            @{ Name = 'Scavenging'; Command = 'Get-DnsServerScavenging'; Arguments = @{} },
            @{ Name = 'Diagnostics'; Command = 'Get-DnsServerDiagnostics'; Arguments = @{} },
            @{ Name = 'Client-Subnets'; Command = 'Get-DnsServerClientSubnet'; Arguments = @{} },
            @{ Name = 'Query-Resolution-Policies'; Command = 'Get-DnsServerQueryResolutionPolicy'; Arguments = @{} },
            @{ Name = 'Response-Rate-Limiting'; Command = 'Get-DnsServerResponseRateLimiting'; Arguments = @{} }
        )

        foreach ($dnsCommand in $dnsCommands) {
            $commandName = [string]$dnsCommand.Command
            $commandInfo = Get-Command -Name $commandName -ErrorAction SilentlyContinue
            if ($commandInfo) {
                try {
                    $arguments = @{} + $dnsCommand.Arguments
                    if ($commandInfo.Parameters.ContainsKey('ComputerName')) {
                        $arguments['ComputerName'] = $env:COMPUTERNAME
                    }
                    $data = & $commandName @arguments
                    Save-ObjectData -InputObject $data -BasePath (Join-Path $rolePath $dnsCommand.Name)
                }
                catch {
                    ('Command failed: ' + $_.Exception.Message) |
                        Set-Content -Path (Join-Path $rolePath ($dnsCommand.Name + '-Error.txt')) -Encoding UTF8
                }
            }
        }

        $dnsRecoveryNotice = @'
DNS RECOVERY NOTICE

The resource-record inventory and zone-file output are intended for validation and
reconstruction assistance. For Active Directory-integrated DNS, use supported Active
Directory system-state or forest recovery procedures. Do not treat an AD-integrated
Export-DnsServerZone text file as a complete standalone DNS backup.
'@
        $dnsRecoveryNotice |
            Set-Content -Path (Join-Path $rolePath 'IMPORTANT-DNS-Recovery-Limitations.txt') -Encoding UTF8
    }
}

if ($detectedRoleNames -contains 'IIS Web Server') {
    Invoke-Collector -Name 'IIS configuration backup and inventory' -Action {
        $rolePath = Join-Path $paths.Roles 'IIS'
        New-Item -Path $rolePath -ItemType Directory -Force | Out-Null
        $appCmd = Join-Path $env:SystemRoot 'System32\inetsrv\appcmd.exe'

        if (Test-Path -LiteralPath $appCmd) {
            $backupName = 'ServerInventory_' + $timestamp
            if (Test-ExportCategory -Category @('IIS')) {
                Invoke-NativeCapture -Command $appCmd -Arguments @('add', 'backup', $backupName) -OutputFile (Join-Path $rolePath 'AppCmd-Backup.log')
                $iisBackupSource = Join-Path (Join-Path $env:SystemRoot 'System32\inetsrv\backup') $backupName
                if (Test-Path -LiteralPath $iisBackupSource) {
                    Copy-Item -LiteralPath $iisBackupSource -Destination (Join-Path $rolePath 'IIS-Configuration-Backup') -Recurse -Force
                }
            }

            Invoke-NativeCapture -Command $appCmd -Arguments @('list', 'site', '/config', '/xml') -OutputFile (Join-Path $rolePath 'AppCmd-Sites.xml') -AllowNonZeroExit
            Invoke-NativeCapture -Command $appCmd -Arguments @('list', 'apppool', '/config', '/xml') -OutputFile (Join-Path $rolePath 'AppCmd-AppPools.xml') -AllowNonZeroExit
            Invoke-NativeCapture -Command $appCmd -Arguments @('list', 'app', '/config', '/xml') -OutputFile (Join-Path $rolePath 'AppCmd-Applications.xml') -AllowNonZeroExit
            Invoke-NativeCapture -Command $appCmd -Arguments @('list', 'vdir', '/config', '/xml') -OutputFile (Join-Path $rolePath 'AppCmd-Virtual-Directories.xml') -AllowNonZeroExit
            if (Test-ExportCategory -Category @('IIS')) { Invoke-NativeCapture -Command $appCmd -Arguments @('delete', 'backup', $backupName) -OutputFile (Join-Path $rolePath 'AppCmd-Temporary-Backup-Cleanup.log') -AllowNonZeroExit }
        }

        if (Get-Command Get-Website -ErrorAction SilentlyContinue) {
            Save-ObjectData -InputObject (Get-Website) -BasePath (Join-Path $rolePath 'Websites')
            Save-ObjectData -InputObject (Get-WebBinding) -BasePath (Join-Path $rolePath 'Bindings')
            Save-ObjectData -InputObject (Get-WebApplication) -BasePath (Join-Path $rolePath 'Applications')
            Save-ObjectData -InputObject (Get-WebVirtualDirectory) -BasePath (Join-Path $rolePath 'Virtual-Directories')
        }

        'The IIS configuration backup does not include website content, application databases, reusable application-pool passwords, or certificate private keys.' |
            Set-Content -Path (Join-Path $rolePath 'IMPORTANT-Recovery-Limitations.txt') -Encoding UTF8
    }
}

if ($detectedRoleNames -contains 'Hyper-V') {
    Invoke-Collector -Name 'Hyper-V inventory' -Action {
        $rolePath = Join-Path $paths.Roles 'Hyper-V'
        New-Item -Path $rolePath -ItemType Directory -Force | Out-Null

        if (-not (Get-Command Get-VM -ErrorAction SilentlyContinue)) {
            Import-Module Hyper-V -ErrorAction Stop
        }

        Save-ObjectData -InputObject (Get-VMHost) -BasePath (Join-Path $rolePath 'Host-Configuration')
        $vms = @(Get-VM)
        Save-ObjectData -InputObject $vms -BasePath (Join-Path $rolePath 'Virtual-Machines')
        Save-ObjectData -InputObject ($vms | Get-VMHardDiskDrive) -BasePath (Join-Path $rolePath 'VM-Hard-Disks')
        Save-ObjectData -InputObject ($vms | Get-VMDvdDrive -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'VM-DVD-Drives')
        Save-ObjectData -InputObject ($vms | Get-VMNetworkAdapter) -BasePath (Join-Path $rolePath 'VM-Network-Adapters')
        Save-ObjectData -InputObject (Get-VMNetworkAdapter -ManagementOS -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'Host-Virtual-Network-Adapters')
        Save-ObjectData -InputObject (Get-VMSwitch) -BasePath (Join-Path $rolePath 'Virtual-Switches')
        Save-ObjectData -InputObject ($vms | Get-VMProcessor -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'VM-Processors')
        Save-ObjectData -InputObject ($vms | Get-VMMemory -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'VM-Memory')
        Save-ObjectData -InputObject ($vms | Get-VMIntegrationService -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'VM-Integration-Services')
        Save-ObjectData -InputObject ($vms | Get-VMSnapshot -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'VM-Checkpoints')

        $firmwareRows = foreach ($vm in $vms) {
            if ($vm.Generation -eq 2 -and (Get-Command Get-VMFirmware -ErrorAction SilentlyContinue)) {
                Get-VMFirmware -VM $vm -ErrorAction SilentlyContinue | Select-Object *, @{Name = 'VMName'; Expression = { $vm.Name }}
            }
            elseif (Get-Command Get-VMBios -ErrorAction SilentlyContinue) {
                Get-VMBios -VM $vm -ErrorAction SilentlyContinue | Select-Object *, @{Name = 'VMName'; Expression = { $vm.Name }}
            }
        }
        Save-ObjectData -InputObject $firmwareRows -BasePath (Join-Path $rolePath 'VM-Firmware-and-BIOS')

        $vlanRows = foreach ($vm in $vms) {
            foreach ($adapter in (Get-VMNetworkAdapter -VM $vm)) {
                try {
                    Get-VMNetworkAdapterVlan -VMNetworkAdapter $adapter -ErrorAction Stop |
                        Select-Object @{Name = 'VMName'; Expression = { $vm.Name }}, VMNetworkAdapterName, OperationMode, AccessVlanId, NativeVlanId, AllowedVlanIdList
                }
                catch { }
            }
        }
        Save-ObjectData -InputObject $vlanRows -BasePath (Join-Path $rolePath 'VM-VLAN-Configuration')

        $vhdPaths = @($vms | Get-VMHardDiskDrive | Where-Object Path | Select-Object -ExpandProperty Path -Unique)
        $vhdDetails = foreach ($vhdPath in $vhdPaths) {
            try { Get-VHD -Path $vhdPath -ErrorAction Stop } catch {
                [pscustomobject]@{ Path = $vhdPath; Error = $_.Exception.Message }
            }
        }
        Save-ObjectData -InputObject $vhdDetails -BasePath (Join-Path $rolePath 'VHD-Details')

        if (Get-Command Get-VMReplication -ErrorAction SilentlyContinue) {
            Save-ObjectData -InputObject (Get-VMReplication -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'VM-Replication')
        }
        if (Get-Command Get-VMReplicationServer -ErrorAction SilentlyContinue) {
            Save-ObjectData -InputObject (Get-VMReplicationServer -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'Hyper-V-Replica-Server-Configuration')
        }
        if (Get-Command Get-VMReplicationAuthorizationEntry -ErrorAction SilentlyContinue) {
            Save-ObjectData -InputObject (Get-VMReplicationAuthorizationEntry -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'Hyper-V-Replica-Authorization-Entries')
        }
        if (Get-Command Get-VMNetworkAdapterVlan -ErrorAction SilentlyContinue) {
            $hostVlan = Get-VMNetworkAdapter -ManagementOS -ErrorAction SilentlyContinue | Get-VMNetworkAdapterVlan -ErrorAction SilentlyContinue
            Save-ObjectData -InputObject $hostVlan -BasePath (Join-Path $rolePath 'Host-VLAN-Configuration')
        }

        'This inventory does not export or back up VM configuration files, checkpoints, VHD/VHDX files, guest data, or application-consistent guest state.' |
            Set-Content -Path (Join-Path $rolePath 'IMPORTANT-Recovery-Limitations.txt') -Encoding UTF8
    }
}

if ($detectedRoleNames -contains 'Failover Clustering') {
    Invoke-Collector -Name 'Failover Cluster inventory' -Action {
        $rolePath = Join-Path $paths.Roles 'Failover-Clustering'
        New-Item -Path $rolePath -ItemType Directory -Force | Out-Null

        if (-not (Get-Command Get-Cluster -ErrorAction SilentlyContinue)) {
            Import-Module FailoverClusters -ErrorAction Stop
        }

        Save-ObjectData -InputObject (Get-Cluster) -BasePath (Join-Path $rolePath 'Cluster')
        Save-ObjectData -InputObject (Get-ClusterNode) -BasePath (Join-Path $rolePath 'Nodes')
        Save-ObjectData -InputObject (Get-ClusterGroup) -BasePath (Join-Path $rolePath 'Groups')
        $clusterResources = @(Get-ClusterResource)
        Save-ObjectData -InputObject $clusterResources -BasePath (Join-Path $rolePath 'Resources')
        Save-ObjectData -InputObject (Get-ClusterNetwork) -BasePath (Join-Path $rolePath 'Networks')
        Save-ObjectData -InputObject (Get-ClusterNetworkInterface) -BasePath (Join-Path $rolePath 'Network-Interfaces')
        Save-ObjectData -InputObject (Get-ClusterQuorum) -BasePath (Join-Path $rolePath 'Quorum')
        Save-ObjectData -InputObject (Get-ClusterSharedVolume -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'Cluster-Shared-Volumes')

        if (Get-Command Get-ClusterParameter -ErrorAction SilentlyContinue) {
            $resourceParameters = foreach ($resource in $clusterResources) {
                try {
                    Get-ClusterParameter -InputObject $resource -ErrorAction Stop |
                        Select-Object @{Name = 'ClusterResource'; Expression = { $resource.Name }}, Name, Value, Type
                }
                catch {
                    [pscustomobject]@{ ClusterResource = $resource.Name; Name = ''; Value = ''; Type = ''; Error = $_.Exception.Message }
                }
            }
            Save-ObjectData -InputObject $resourceParameters -BasePath (Join-Path $rolePath 'Resource-Parameters')
        }
        if (Get-Command Get-ClusterResourceDependency -ErrorAction SilentlyContinue) {
            $resourceDependencies = foreach ($resource in $clusterResources) {
                try {
                    [pscustomobject]@{
                        ClusterResource = $resource.Name
                        DependencyExpression = (Get-ClusterResourceDependency -Resource $resource.Name -ErrorAction Stop).DependencyExpression
                    }
                }
                catch {
                    [pscustomobject]@{ ClusterResource = $resource.Name; DependencyExpression = ''; Error = $_.Exception.Message }
                }
            }
            Save-ObjectData -InputObject $resourceDependencies -BasePath (Join-Path $rolePath 'Resource-Dependencies')
        }
    }
}

if ($detectedRoleNames -contains 'Print Server') {
    Invoke-Collector -Name 'Print Server export' -Action {
        $rolePath = Join-Path $paths.Roles 'Print-Server'
        New-Item -Path $rolePath -ItemType Directory -Force | Out-Null

        if (Get-Command Get-Printer -ErrorAction SilentlyContinue) {
            Save-ObjectData -InputObject (Get-Printer) -BasePath (Join-Path $rolePath 'Printers')
            Save-ObjectData -InputObject (Get-PrinterDriver) -BasePath (Join-Path $rolePath 'Printer-Drivers')
            Save-ObjectData -InputObject (Get-PrinterPort) -BasePath (Join-Path $rolePath 'Printer-Ports')
        }

        $printBrm = Join-Path $env:SystemRoot 'System32\spool\tools\PrintBrm.exe'
        if ((Test-Path -LiteralPath $printBrm) -and (Test-ExportCategory -Category @('PrintServices'))) {
            $printerExport = Join-Path $rolePath ($env:COMPUTERNAME + '.printerExport')
            Invoke-NativeCapture -Command $printBrm -Arguments @('-s', ('\\' + $env:COMPUTERNAME), '-b', '-f', $printerExport) -OutputFile (Join-Path $rolePath 'PrintBRM-Export.log')
            'Restore reference: use PrintBRM/Print Management during an approved recovery operation and validate queues, ports, drivers, defaults, sharing, and test pages after import.' | Set-Content -Path (Join-Path $rolePath 'RESTORE-GUIDANCE.txt') -Encoding UTF8
        }
    }
}

if ($detectedRoleNames -contains 'Routing and Remote Access') {
    Invoke-Collector -Name 'Routing and Remote Access inventory' -Action {
        $rolePath = Join-Path $paths.Roles 'RRAS'
        New-Item -Path $rolePath -ItemType Directory -Force | Out-Null
        Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('routing','dump') -OutputFile (Join-Path $rolePath 'Routing-Dump.txt') -AllowNonZeroExit | Out-Null
        Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('ras','show','registeredserver') -OutputFile (Join-Path $rolePath 'RAS-Registered-Server.txt') -AllowNonZeroExit | Out-Null
        Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('interface','ipv4','show','interfaces') -OutputFile (Join-Path $rolePath 'IPv4-Interfaces.txt') -AllowNonZeroExit | Out-Null
        try { Export-RegistryKey -RegistryPath 'HKLM\\SYSTEM\\CurrentControlSet\\Services\\RemoteAccess' -DestinationFile (Join-Path $rolePath 'RemoteAccess-Registry.reg') } catch { }
        if (Test-ExportCategory -Category @('RRAS')) {
            Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('routing','dump') -OutputFile (Join-Path $rolePath 'RRAS-Restore-Reference.txt') -AllowNonZeroExit | Out-Null
            'RRAS restore is version- and topology-sensitive. Use the captured NETSH/registry information as reconstruction guidance, verify VPN certificates and secrets separately, then validate routes, NAT, VPN listeners, authentication, and client connectivity.' | Set-Content -Path (Join-Path $rolePath 'RESTORE-GUIDANCE.txt') -Encoding UTF8
        }
    }
}

if ($detectedRoleNames -contains 'Network Policy Server') {
    Invoke-Collector -Name 'NPS inventory' -Action {
        $rolePath = Join-Path $paths.Roles 'NPS'
        New-Item -Path $rolePath -ItemType Directory -Force | Out-Null

        if (Get-Command Get-NpsRadiusClient -ErrorAction SilentlyContinue) {
            Save-ObjectData -InputObject (Get-NpsRadiusClient) -BasePath (Join-Path $rolePath 'RADIUS-Clients-Metadata')
        }
        Invoke-NativeCapture -Command 'netsh.exe' -Arguments @('nps', 'show', 'config') -OutputFile (Join-Path $rolePath 'NPS-Configuration-Summary.txt') -AllowNonZeroExit | Out-Null

        if ($IncludeSensitiveRoleExports -and (Test-ExportCategory -Category @('NPS'))) {
            if (-not (Get-Command Export-NpsConfiguration -ErrorAction SilentlyContinue)) {
                Import-Module NPS -ErrorAction Stop
            }
            Export-NpsConfiguration -Path (Join-Path $rolePath 'NPS-FULL-CONFIG-SENSITIVE.xml')
            'WARNING: The NPS full configuration export can contain unencrypted RADIUS shared secrets. Encrypt and tightly restrict this package.' |
                Set-Content -Path (Join-Path $rolePath 'WARNING-SENSITIVE-EXPORT.txt') -Encoding UTF8
        }
        else {
            'Full NPS export was skipped. Re-run with IncludeSensitiveRoleExports only when the package will be encrypted and tightly controlled.' |
                Set-Content -Path (Join-Path $rolePath 'Full-Export-Skipped.txt') -Encoding UTF8
        }
    }
}

if ($detectedRoleNames -contains 'Active Directory Certificate Services') {
    Invoke-Collector -Name 'Certification Authority inventory' -Action {
        $rolePath = Join-Path $paths.Roles 'Active-Directory-Certificate-Services'
        New-Item -Path $rolePath -ItemType Directory -Force | Out-Null

        Invoke-NativeCapture -Command 'certutil.exe' -Arguments @('-getreg') -OutputFile (Join-Path $rolePath 'CA-Registry-Configuration.txt') -AllowNonZeroExit | Out-Null
        Invoke-NativeCapture -Command 'certutil.exe' -Arguments @('-catemplates') -OutputFile (Join-Path $rolePath 'Published-Certificate-Templates.txt') -AllowNonZeroExit | Out-Null
        Export-RegistryKey -RegistryPath 'HKLM\SYSTEM\CurrentControlSet\Services\CertSvc\Configuration' -DestinationFile (Join-Path $rolePath 'CertSvc-Configuration-Registry.reg')

        'This inventory does not back up the CA database or CA private key. Complete a supported CA backup and protect its password and media separately.' |
            Set-Content -Path (Join-Path $rolePath 'CRITICAL-CA-BACKUP-REQUIRED.txt') -Encoding UTF8
    }
}

if ($detectedRoleNames -contains 'Microsoft SQL Server') {
    Invoke-Collector -Name 'SQL Server discovery and database metadata' -Action {
        $rolePath = Join-Path $paths.Roles 'Microsoft-SQL-Server'
        New-Item -Path $rolePath -ItemType Directory -Force | Out-Null

        $instances = @(Get-SqlRegistryInstances)
        # Service discovery fills gaps when the Instance Names registry key is incomplete.
        foreach ($service in @(Get-CimInstance -ClassName Win32_Service -ErrorAction SilentlyContinue | Where-Object { $_.Name -eq 'MSSQLSERVER' -or $_.Name -like 'MSSQL$*' })) {
            $instanceName = if ($service.Name -eq 'MSSQLSERVER') { 'MSSQLSERVER' } else { $service.Name.Substring(6) }
            if (-not ($instances | Where-Object { $_.InstanceName -eq $instanceName })) {
                $instances += [pscustomobject]@{ RegistryPath='Detected from Windows service'; InstanceName=$instanceName; InstanceId='' }
            }
        }
        Save-ObjectData -InputObject $instances -BasePath (Join-Path $rolePath 'SQL-Instances')

        $windowsMetadata = @()
        foreach ($instance in $instances) { $windowsMetadata += Get-SqlInstanceWindowsMetadata -Instance $instance }
        Save-ObjectData -InputObject $windowsMetadata -BasePath (Join-Path $rolePath 'SQL-Instance-Windows-Metadata')

        $sqlServices = @(Get-CimInstance -ClassName Win32_Service -ErrorAction SilentlyContinue | Where-Object { $_.Name -like 'MSSQL*' -or $_.Name -like 'SQLAgent*' -or $_.Name -eq 'SQLBrowser' -or $_.Name -like 'SQLWriter*' })
        Save-ObjectData -InputObject $sqlServices -BasePath (Join-Path $rolePath 'SQL-Windows-Services')

        $installedSqlComponents = @(Get-InstalledApplicationInventory | Where-Object { $_.DisplayName -match '(?i)SQL Server|SQL Native Client|ODBC Driver.*SQL|SQL Server Management|SQL Server Reporting|SQL Server Integration|SQL Server Analysis' })
        Save-ObjectData -InputObject $installedSqlComponents -BasePath (Join-Path $rolePath 'Installed-SQL-Components')

        if (Test-ExportCategory -Category @('SQLServer','Registry')) {
            try { Export-RegistryKey -RegistryPath 'HKLM\\SOFTWARE\\Microsoft\\Microsoft SQL Server' -DestinationFile (Join-Path $rolePath 'SQL-Server-Registry-64bit.reg') } catch { }
            try { Export-RegistryKey -RegistryPath 'HKLM\\SOFTWARE\\WOW6432Node\\Microsoft\\Microsoft SQL Server' -DestinationFile (Join-Path $rolePath 'SQL-Server-Registry-32bit.reg') } catch { }
        }

        $connectionStatus = New-Object System.Collections.ArrayList
        foreach ($instance in $instances) {
            $instanceName = [string]$instance.InstanceName
            $dataSource = if ($instanceName -eq 'MSSQLSERVER') { 'localhost' } else { 'localhost\\' + $instanceName }
            try {
                $serverInfo = @(Invoke-SqlMetadataQuery -DataSource $dataSource -Query @"
SELECT
 CAST(SERVERPROPERTY('ServerName') AS nvarchar(256)) AS ServerName,
 CAST(SERVERPROPERTY('InstanceName') AS nvarchar(256)) AS InstanceName,
 CAST(SERVERPROPERTY('Edition') AS nvarchar(256)) AS Edition,
 CAST(SERVERPROPERTY('ProductVersion') AS nvarchar(128)) AS ProductVersion,
 CAST(SERVERPROPERTY('ProductLevel') AS nvarchar(128)) AS ProductLevel,
 CAST(SERVERPROPERTY('ProductUpdateLevel') AS nvarchar(128)) AS ProductUpdateLevel,
 CAST(SERVERPROPERTY('Collation') AS nvarchar(256)) AS ServerCollation,
 CAST(SERVERPROPERTY('IsClustered') AS int) AS IsClustered,
 CAST(SERVERPROPERTY('IsIntegratedSecurityOnly') AS int) AS IsIntegratedSecurityOnly,
 CAST(SERVERPROPERTY('InstanceDefaultDataPath') AS nvarchar(2048)) AS InstanceDefaultDataPath,
 CAST(SERVERPROPERTY('InstanceDefaultLogPath') AS nvarchar(2048)) AS InstanceDefaultLogPath;
"@)
                foreach ($row in $serverInfo) {
                    Add-Member -InputObject $row -MemberType NoteProperty -Name CollectedInstance -Value $instanceName -Force
                    $authenticationMode = if ([int]$row.IsIntegratedSecurityOnly -eq 1) { 'Windows authentication' } else { 'Mixed mode' }
                    Add-Member -InputObject $row -MemberType NoteProperty -Name AuthenticationMode -Value $authenticationMode -Force
                }
                Save-ObjectData -InputObject $serverInfo -BasePath (Join-Path $rolePath ('Instance-' + (Get-SafeFileName $instanceName) + '-Server-Properties'))

                $sqlDatasetErrors = New-Object System.Collections.ArrayList
                $databases = @()
                try {
                    $databases = @(Invoke-SqlMetadataQuery -DataSource $dataSource -Query @"
SELECT d.name AS DatabaseName, d.state_desc AS State, d.recovery_model_desc AS RecoveryModel,
 d.compatibility_level AS CompatibilityLevel, SUSER_SNAME(d.owner_sid) AS DatabaseOwner,
 d.create_date AS CreationDate, d.collation_name AS Collation,
 SUM(CAST(mf.size AS bigint))*8192 AS TotalSizeBytes
FROM sys.databases d LEFT JOIN sys.master_files mf ON d.database_id=mf.database_id
GROUP BY d.name,d.state_desc,d.recovery_model_desc,d.compatibility_level,d.owner_sid,d.create_date,d.collation_name
ORDER BY d.name;
"@)
                    $dbFriendly = foreach ($db in $databases) {
                        [pscustomobject]@{ DatabaseName=$db.DatabaseName; State=$db.State; RecoveryModel=$db.RecoveryModel; CompatibilityLevel=$db.CompatibilityLevel; DatabaseOwner=$db.DatabaseOwner; CreationDate=$db.CreationDate; Collation=$db.Collation; TotalSize=(Format-ByteSize $db.TotalSizeBytes); TotalSizeBytes=$db.TotalSizeBytes }
                    }
                    Save-ObjectData -InputObject $dbFriendly -BasePath (Join-Path $rolePath ('Instance-' + (Get-SafeFileName $instanceName) + '-Databases'))
                }
                catch {
                    [void]$sqlDatasetErrors.Add('Databases: ' + $_.Exception.Message)
                    ('Database inventory was unavailable: ' + $_.Exception.Message) | Set-Content -Path (Join-Path $rolePath ('Instance-' + (Get-SafeFileName $instanceName) + '-Databases-Unavailable.txt')) -Encoding UTF8
                }

                try {
                    $files = @(Invoke-SqlMetadataQuery -DataSource $dataSource -Query @"
SELECT DB_NAME(database_id) AS DatabaseName, name AS LogicalName, type_desc AS FileType,
 physical_name AS PhysicalPath, CAST(size AS bigint)*8192 AS FileSizeBytes,
 CASE WHEN max_size=-1 THEN 'Unlimited' ELSE CAST(CAST(max_size AS bigint)*8192 AS nvarchar(40)) END AS MaxSizeBytes,
 growth AS GrowthValue, is_percent_growth AS IsPercentGrowth
FROM sys.master_files ORDER BY DB_NAME(database_id), type_desc, file_id;
"@)
                    $fileFriendly = foreach ($f in $files) {
                        [pscustomobject]@{ DatabaseName=$f.DatabaseName; LogicalName=$f.LogicalName; FileType=$f.FileType; PhysicalPath=$f.PhysicalPath; FileSize=(Format-ByteSize $f.FileSizeBytes); FileSizeBytes=$f.FileSizeBytes; MaxSize=if($f.MaxSizeBytes -eq 'Unlimited'){'Unlimited'}else{Format-ByteSize $f.MaxSizeBytes}; MaxSizeBytes=$f.MaxSizeBytes; GrowthValue=$f.GrowthValue; IsPercentGrowth=$f.IsPercentGrowth }
                    }
                    Save-ObjectData -InputObject $fileFriendly -BasePath (Join-Path $rolePath ('Instance-' + (Get-SafeFileName $instanceName) + '-Database-Files'))
                }
                catch {
                    [void]$sqlDatasetErrors.Add('Database files: ' + $_.Exception.Message)
                    ('Database file metadata was unavailable: ' + $_.Exception.Message) | Set-Content -Path (Join-Path $rolePath ('Instance-' + (Get-SafeFileName $instanceName) + '-Database-Files-Unavailable.txt')) -Encoding UTF8
                }

                $spaceRows = New-Object System.Collections.ArrayList
                foreach ($db in $databases | Where-Object { $_.State -eq 'ONLINE' }) {
                    try {
                        $dbName = [string]$db.DatabaseName
                        $space = @(Invoke-SqlMetadataQuery -DataSource $dataSource -Database $dbName -Query @"
SELECT DB_NAME() AS DatabaseName,
 SUM(CASE WHEN type_desc='ROWS' THEN CAST(size AS bigint)*8192 ELSE 0 END) AS DataAllocatedBytes,
 SUM(CASE WHEN type_desc='ROWS' THEN CAST(size-FILEPROPERTY(name,'SpaceUsed') AS bigint)*8192 ELSE 0 END) AS DataFreeBytes,
 SUM(CASE WHEN type_desc='LOG' THEN CAST(size AS bigint)*8192 ELSE 0 END) AS LogAllocatedBytes
FROM sys.database_files;
"@)
                        foreach ($row in $space) {
                            [void]$spaceRows.Add([pscustomobject]@{ DatabaseName=$row.DatabaseName; DataAllocated=(Format-ByteSize $row.DataAllocatedBytes); DataFree=(Format-ByteSize $row.DataFreeBytes); LogAllocated=(Format-ByteSize $row.LogAllocatedBytes); DataAllocatedBytes=$row.DataAllocatedBytes; DataFreeBytes=$row.DataFreeBytes; LogAllocatedBytes=$row.LogAllocatedBytes })
                        }
                    }
                    catch {
                        [void]$sqlDatasetErrors.Add(('Database free space [' + [string]$db.DatabaseName + ']: ' + $_.Exception.Message))
                    }
                }
                Save-ObjectData -InputObject $spaceRows -BasePath (Join-Path $rolePath ('Instance-' + (Get-SafeFileName $instanceName) + '-Database-Space'))

                try {
                    Save-ObjectData -InputObject (Invoke-SqlMetadataQuery -DataSource $dataSource -Query "SELECT name AS ConfigurationName, value AS ConfiguredValue, value_in_use AS ValueInUse, minimum, maximum, is_dynamic AS IsDynamic, is_advanced AS IsAdvanced, description FROM sys.configurations ORDER BY name;") -BasePath (Join-Path $rolePath ('Instance-' + (Get-SafeFileName $instanceName) + '-Server-Configuration'))
                }
                catch { [void]$sqlDatasetErrors.Add('Server configuration: ' + $_.Exception.Message) }

                try {
                    Save-ObjectData -InputObject (Invoke-SqlMetadataQuery -DataSource $dataSource -Query "SELECT name AS LinkedServerName, product, provider, data_source AS DataSource, catalog, is_linked AS IsLinked, is_data_access_enabled AS DataAccessEnabled, is_rpc_out_enabled AS RpcOutEnabled FROM sys.servers WHERE server_id > 0 ORDER BY name;") -BasePath (Join-Path $rolePath ('Instance-' + (Get-SafeFileName $instanceName) + '-Linked-Servers'))
                }
                catch { [void]$sqlDatasetErrors.Add('Linked servers: ' + $_.Exception.Message) }

                try {
                    Save-ObjectData -InputObject (Invoke-SqlMetadataQuery -DataSource $dataSource -Query "SELECT name AS PrincipalName, type_desc AS PrincipalType, default_database_name AS DefaultDatabase, is_disabled AS IsDisabled, create_date AS CreationDate, modify_date AS ModifiedDate FROM sys.server_principals WHERE type IN ('S','U','G') AND name NOT LIKE '##%##' ORDER BY name;") -BasePath (Join-Path $rolePath ('Instance-' + (Get-SafeFileName $instanceName) + '-Server-Principals'))
                }
                catch { [void]$sqlDatasetErrors.Add('Server principals: ' + $_.Exception.Message) }

                try {
                    Save-ObjectData -InputObject (Invoke-SqlMetadataQuery -DataSource $dataSource -Database 'msdb' -Query "SELECT name AS JobName, enabled AS Enabled, description AS Description, date_created AS CreationDate, date_modified AS ModifiedDate FROM dbo.sysjobs ORDER BY name;") -BasePath (Join-Path $rolePath ('Instance-' + (Get-SafeFileName $instanceName) + '-SQL-Agent-Jobs'))
                }
                catch { [void]$sqlDatasetErrors.Add('SQL Agent jobs: ' + $_.Exception.Message) }

                try {
                    $backups = @(Invoke-SqlMetadataQuery -DataSource $dataSource -Database 'msdb' -Query @"
SELECT d.name AS DatabaseName,
 MAX(CASE WHEN b.type='D' THEN b.backup_finish_date END) AS LastFullBackup,
 MAX(CASE WHEN b.type='I' THEN b.backup_finish_date END) AS LastDifferentialBackup,
 MAX(CASE WHEN b.type='L' THEN b.backup_finish_date END) AS LastLogBackup
FROM master.sys.databases d LEFT JOIN dbo.backupset b ON b.database_name=d.name
GROUP BY d.name ORDER BY d.name;
"@)
                    Save-ObjectData -InputObject $backups -BasePath (Join-Path $rolePath ('Instance-' + (Get-SafeFileName $instanceName) + '-Backup-History-Summary'))
                }
                catch { [void]$sqlDatasetErrors.Add('Backup history: ' + $_.Exception.Message) }

                $sqlStatusText = if ($sqlDatasetErrors.Count -gt 0) { 'Available with limitations' } else { 'Available' }
                $sqlStatusError = if ($sqlDatasetErrors.Count -gt 0) { ($sqlDatasetErrors -join ' | ') } else { '' }
                if ($sqlDatasetErrors.Count -gt 0) {
                    @(
                        'SQL database-level connection succeeded, but one or more read-only datasets were unavailable to the current Windows account.',
                        '',
                        ($sqlDatasetErrors | ForEach-Object { '- ' + [string]$_ })
                    ) | Set-Content -Path (Join-Path $rolePath ('Instance-' + (Get-SafeFileName $instanceName) + '-Database-Level-Collection-Limitations.txt')) -Encoding UTF8
                }
                [void]$connectionStatus.Add([pscustomobject]@{ InstanceName=$instanceName; DataSource=$dataSource; DatabaseLevelCollection=$sqlStatusText; Error=$sqlStatusError })
            }
            catch {
                [void]$connectionStatus.Add([pscustomobject]@{ InstanceName=$instanceName; DataSource=$dataSource; DatabaseLevelCollection='Unavailable'; Error=$_.Exception.Message })
                ('Database-level SQL collection could not connect using the current Windows account. Windows services, registry, installed components, and local port information were still collected. Error: ' + $_.Exception.Message) | Set-Content -Path (Join-Path $rolePath ('Instance-' + (Get-SafeFileName $instanceName) + '-Database-Level-Collection-Unavailable.txt')) -Encoding UTF8
            }
        }
        Save-ObjectData -InputObject $connectionStatus -BasePath (Join-Path $rolePath 'SQL-Database-Level-Collection-Status')

        @(
            'SQL collection is read-only. It does not collect database contents, passwords, login password hashes, credentials, private keys, master keys, or certificate private keys.',
            'Recovery requirement: maintain tested SQL-native database backups and separately preserve instance-level security objects and encryption dependencies.',
            'Suggested restore order: install matching SQL major version and required features; patch to an appropriate build; configure service accounts and network settings; restore master/msdb only when the recovery plan specifically requires it; restore user databases; recreate or validate logins, jobs, linked servers, credentials, endpoints, certificates and keys; then validate application connectivity and backup jobs.'
        ) | Set-Content -Path (Join-Path $rolePath 'SQL-Recovery-and-Restore-Guidance.txt') -Encoding UTF8
    }
}

if ($detectedRoleNames -contains 'WSUS') {
    Invoke-Collector -Name 'WSUS metadata' -Action {
        $rolePath = Join-Path $paths.Roles 'WSUS'
        New-Item -Path $rolePath -ItemType Directory -Force | Out-Null
        Export-RegistryKey -RegistryPath 'HKLM\SOFTWARE\Microsoft\Update Services\Server\Setup' -DestinationFile (Join-Path $rolePath 'WSUS-Setup-Registry.reg')

        'This inventory does not back up SUSDB or WSUS content. Document whether WSUS will be restored or rebuilt and preserve approvals and reporting data as required.' |
            Set-Content -Path (Join-Path $rolePath 'CRITICAL-WSUS-BACKUP-REQUIRED.txt') -Encoding UTF8
    }
}

Invoke-Collector -Name 'DFS namespace and replication inventory' -Action {
    $rolePath = Join-Path $paths.Roles 'DFS'
    $hasDfsData = $false

    if (Get-Command Get-DfsnRoot -ErrorAction SilentlyContinue) {
        $roots = @(Get-DfsnRoot -ErrorAction SilentlyContinue)
        if ($roots.Count -gt 0) {
            $hasDfsData = $true
            New-Item -Path $rolePath -ItemType Directory -Force | Out-Null
            Save-ObjectData -InputObject $roots -BasePath (Join-Path $rolePath 'Namespace-Roots')

            $folders = foreach ($root in $roots) {
                Get-DfsnFolder -Path ($root.Path.TrimEnd('\') + '\*') -ErrorAction SilentlyContinue
            }
            Save-ObjectData -InputObject $folders -BasePath (Join-Path $rolePath 'Namespace-Folders')

            $targets = foreach ($folder in $folders) {
                Get-DfsnFolderTarget -Path $folder.Path -ErrorAction SilentlyContinue
            }
            Save-ObjectData -InputObject $targets -BasePath (Join-Path $rolePath 'Namespace-Folder-Targets')
        }
    }

    if (Get-Command Get-DfsReplicationGroup -ErrorAction SilentlyContinue) {
        $groups = @(Get-DfsReplicationGroup -ErrorAction SilentlyContinue)
        if ($groups.Count -gt 0) {
            $hasDfsData = $true
            New-Item -Path $rolePath -ItemType Directory -Force | Out-Null
            Save-ObjectData -InputObject $groups -BasePath (Join-Path $rolePath 'Replication-Groups')

            $replicatedFolders = foreach ($group in $groups) {
                Get-DfsReplicatedFolder -GroupName $group.GroupName -ErrorAction SilentlyContinue
            }
            Save-ObjectData -InputObject $replicatedFolders -BasePath (Join-Path $rolePath 'Replicated-Folders')

            $memberships = foreach ($group in $groups) {
                Get-DfsrMembership -GroupName $group.GroupName -ErrorAction SilentlyContinue
            }
            Save-ObjectData -InputObject $memberships -BasePath (Join-Path $rolePath 'Replication-Memberships')

            $connections = foreach ($group in $groups) {
                Get-DfsrConnection -GroupName $group.GroupName -ErrorAction SilentlyContinue
            }
            Save-ObjectData -InputObject $connections -BasePath (Join-Path $rolePath 'Replication-Connections')
        }
    }

    if (-not $hasDfsData) {
        'No DFS namespace or DFS Replication configuration was detected.' |
            Set-Content -Path (Join-Path $paths.Roles 'DFS-Not-Detected.txt') -Encoding UTF8
    }
}

Invoke-Collector -Name 'File Server Resource Manager inventory' -Action {
    $rolePath = Join-Path $paths.Roles 'FSRM'
    $collected = $false
    $commands = @('Get-FsrmQuota', 'Get-FsrmAutoQuota', 'Get-FsrmFileScreen', 'Get-FsrmFileGroup', 'Get-FsrmStorageReport')

    foreach ($command in $commands) {
        if (Get-Command $command -ErrorAction SilentlyContinue) {
            if (-not (Test-Path -LiteralPath $rolePath)) {
                New-Item -Path $rolePath -ItemType Directory -Force | Out-Null
            }
            $data = & $command -ErrorAction SilentlyContinue
            Save-ObjectData -InputObject $data -BasePath (Join-Path $rolePath ($command -replace '^Get-', ''))
            $collected = $true
        }
    }

    if (-not $collected) {
        'FSRM cmdlets were not available.' | Set-Content -Path (Join-Path $paths.Roles 'FSRM-Not-Detected.txt') -Encoding UTF8
    }
}

if (($computerSystem.DomainRole -ge 4) -or ($featureNames -contains 'AD-Domain-Services') -or $IncludeDomainInventory -or $IncludeGpoBackup -or (Test-InventoryCategory -Category @('ActiveDirectory','GroupPolicy'))) {
    Invoke-Collector -Name 'Active Directory health and topology summary' -Action {
        $rolePath = Join-Path $paths.Roles 'Active-Directory'
        New-Item -Path $rolePath -ItemType Directory -Force | Out-Null

        if (Get-Command dcdiag.exe -ErrorAction SilentlyContinue) {
            Invoke-NativeCapture -Command 'dcdiag.exe' -Arguments @('/v') -OutputFile (Join-Path $rolePath 'DCDiag-Verbose.txt') -AllowNonZeroExit | Out-Null
        }
        if (Get-Command repadmin.exe -ErrorAction SilentlyContinue) {
            Invoke-NativeCapture -Command 'repadmin.exe' -Arguments @('/replsummary') -OutputFile (Join-Path $rolePath 'Replication-Summary.txt') -AllowNonZeroExit | Out-Null
            Invoke-NativeCapture -Command 'repadmin.exe' -Arguments @('/showrepl', '*', '/csv') -OutputFile (Join-Path $rolePath 'Replication-Details.csv') -AllowNonZeroExit | Out-Null
        }
        if (Get-Command netdom.exe -ErrorAction SilentlyContinue) {
            Invoke-NativeCapture -Command 'netdom.exe' -Arguments @('query', 'fsmo') -OutputFile (Join-Path $rolePath 'FSMO-Roles.txt') -AllowNonZeroExit | Out-Null
        }
        if (Get-Command nltest.exe -ErrorAction SilentlyContinue) {
            Invoke-NativeCapture -Command 'nltest.exe' -Arguments @('/domain_trusts', '/all_trusts', '/v') -OutputFile (Join-Path $rolePath 'NLTest-Domain-Trusts.txt') -AllowNonZeroExit | Out-Null
            if ($computerSystem.Domain -and $computerSystem.Domain -notmatch '(?i)WORKGROUP') {
                Invoke-NativeCapture -Command 'nltest.exe' -Arguments @('/dclist:' + [string]$computerSystem.Domain) -OutputFile (Join-Path $rolePath 'NLTest-Domain-Controllers.txt') -AllowNonZeroExit | Out-Null
                Invoke-NativeCapture -Command 'nltest.exe' -Arguments @('/dsgetdc:' + [string]$computerSystem.Domain, '/force') -OutputFile (Join-Path $rolePath 'NLTest-DC-Locator.txt') -AllowNonZeroExit | Out-Null
            }
        }
        if (Get-Command net.exe -ErrorAction SilentlyContinue) {
            Invoke-NativeCapture -Command 'net.exe' -Arguments @('accounts', '/domain') -OutputFile (Join-Path $rolePath 'NET-Accounts-Domain.txt') -AllowNonZeroExit | Out-Null
        }

        if (-not (Get-Command Get-ADDomain -ErrorAction SilentlyContinue)) {
            try { Import-Module ActiveDirectory -ErrorAction Stop }
            catch {
                ('The ActiveDirectory PowerShell module is unavailable. Native DCDIAG, REPADMIN, NETDOM, and other available diagnostics were retained. Install the AD DS/RSAT management tools for full directory-object inventory. Error: ' + $_.Exception.Message) | Set-Content -Path (Join-Path $rolePath 'ActiveDirectory-Module-Unavailable.txt') -Encoding UTF8
                return
            }
        }

        $domain = Get-ADDomain
        $forest = Get-ADForest
        Save-ObjectData -InputObject $domain -BasePath (Join-Path $rolePath 'Domain')
        Save-ObjectData -InputObject $forest -BasePath (Join-Path $rolePath 'Forest')
        Save-ObjectData -InputObject (Get-ADDomainController -Filter *) -BasePath (Join-Path $rolePath 'Domain-Controllers')
        Save-ObjectData -InputObject (Get-ADTrust -Filter * -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'Trusts')
        Save-ObjectData -InputObject (Get-ADDefaultDomainPasswordPolicy) -BasePath (Join-Path $rolePath 'Default-Domain-Password-Policy')
        Save-ObjectData -InputObject (Get-ADFineGrainedPasswordPolicy -Filter * -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'Fine-Grained-Password-Policies')

        if (Get-Command Get-ADReplicationSite -ErrorAction SilentlyContinue) {
            Save-ObjectData -InputObject (Get-ADReplicationSite -Filter *) -BasePath (Join-Path $rolePath 'AD-Sites')
            Save-ObjectData -InputObject (Get-ADReplicationSubnet -Filter *) -BasePath (Join-Path $rolePath 'AD-Subnets')
            Save-ObjectData -InputObject (Get-ADReplicationSiteLink -Filter *) -BasePath (Join-Path $rolePath 'AD-Site-Links')
            Save-ObjectData -InputObject (Get-ADReplicationConnection -Filter *) -BasePath (Join-Path $rolePath 'AD-Replication-Connections')
            Save-ObjectData -InputObject (Get-ADReplicationFailure -Target * -Scope Forest -ErrorAction SilentlyContinue) -BasePath (Join-Path $rolePath 'AD-Replication-Failures')
        }

        'CSV and CLIXML directory exports are documentation only. They are not a supported replacement for Active Directory replication, system-state backup, or forest recovery procedures.' |
            Set-Content -Path (Join-Path $rolePath 'CRITICAL-AD-RECOVERY-NOTICE.txt') -Encoding UTF8
    }
}

if ($IncludeDomainInventory -or (Test-InventoryCategory -Category @('ActiveDirectory'))) {
    Invoke-Collector -Name 'Deep Active Directory object inventory' -Action {
        $rolePath = Join-Path $paths.Roles 'Active-Directory\Deep-Domain-Inventory'
        New-Item -Path $rolePath -ItemType Directory -Force | Out-Null

        if (-not (Get-Command Get-ADUser -ErrorAction SilentlyContinue)) {
            try { Import-Module ActiveDirectory -ErrorAction Stop }
            catch {
                ('Deep Active Directory inventory was unavailable because the ActiveDirectory PowerShell module is not installed. No passwords or hashes are collected by this module. Error: ' + $_.Exception.Message) | Set-Content -Path (Join-Path $rolePath 'ActiveDirectory-Module-Unavailable.txt') -Encoding UTF8
                return
            }
        }

        $users = Get-ADUser -Filter * -Properties DisplayName, UserPrincipalName, Mail, Enabled, Description, Department, Title, Company, Manager, WhenCreated, WhenChanged, LastLogonTimestamp, PasswordLastSet, PasswordNeverExpires, PasswordExpired, CannotChangePassword, SmartcardLogonRequired, ServicePrincipalName, MemberOf |
            ForEach-Object {
                [pscustomobject]@{
                    SamAccountName         = $_.SamAccountName
                    UserPrincipalName      = $_.UserPrincipalName
                    Name                   = $_.Name
                    DisplayName            = $_.DisplayName
                    Enabled                = $_.Enabled
                    Mail                   = $_.Mail
                    Description            = $_.Description
                    Department             = $_.Department
                    Title                  = $_.Title
                    Company                = $_.Company
                    Manager                = $_.Manager
                    DistinguishedName      = $_.DistinguishedName
                    SID                    = $_.SID.Value
                    ObjectGUID             = $_.ObjectGUID
                    WhenCreated            = $_.WhenCreated
                    WhenChanged            = $_.WhenChanged
                    LastLogonDate          = if ($_.LastLogonTimestamp) { [DateTime]::FromFileTime($_.LastLogonTimestamp) } else { $null }
                    PasswordLastSet        = $_.PasswordLastSet
                    PasswordNeverExpires   = $_.PasswordNeverExpires
                    PasswordExpired        = $_.PasswordExpired
                    CannotChangePassword   = $_.CannotChangePassword
                    SmartcardLogonRequired = $_.SmartcardLogonRequired
                    ServicePrincipalNames  = ($_.ServicePrincipalName -join '; ')
                    MemberOf               = ($_.MemberOf -join '; ')
                }
            }
        Save-ObjectData -InputObject $users -BasePath (Join-Path $rolePath 'Users')

        $groupsRaw = @(Get-ADGroup -Filter * -Properties Description, Mail, ManagedBy, Member)
        $groups = foreach ($group in $groupsRaw) {
            [pscustomobject]@{
                SamAccountName    = $group.SamAccountName
                Name              = $group.Name
                GroupScope        = $group.GroupScope
                GroupCategory     = $group.GroupCategory
                Description       = $group.Description
                Mail              = $group.Mail
                ManagedBy         = $group.ManagedBy
                DistinguishedName = $group.DistinguishedName
                SID               = $group.SID.Value
                ObjectGUID        = $group.ObjectGUID
                DirectMemberCount = @($group.Member).Count
            }
        }
        Save-ObjectData -InputObject $groups -BasePath (Join-Path $rolePath 'Groups')

        $groupMemberships = foreach ($group in $groupsRaw) {
            foreach ($member in @($group.Member)) {
                [pscustomobject]@{
                    GroupSamAccountName = $group.SamAccountName
                    GroupName           = $group.Name
                    GroupDistinguishedName = $group.DistinguishedName
                    MemberDistinguishedName = $member
                }
            }
        }
        Save-ObjectData -InputObject $groupMemberships -BasePath (Join-Path $rolePath 'Direct-Group-Memberships')

        $computers = Get-ADComputer -Filter * -Properties DNSHostName, OperatingSystem, OperatingSystemVersion, OperatingSystemServicePack, IPv4Address, Enabled, Description, LastLogonTimestamp, PasswordLastSet, ServicePrincipalName, Location, ManagedBy |
            ForEach-Object {
                [pscustomobject]@{
                    SamAccountName             = $_.SamAccountName
                    Name                       = $_.Name
                    DNSHostName                = $_.DNSHostName
                    OperatingSystem            = $_.OperatingSystem
                    OperatingSystemVersion     = $_.OperatingSystemVersion
                    OperatingSystemServicePack = $_.OperatingSystemServicePack
                    IPv4Address                = $_.IPv4Address
                    Enabled                    = $_.Enabled
                    Description                = $_.Description
                    Location                   = $_.Location
                    ManagedBy                  = $_.ManagedBy
                    LastLogonDate              = if ($_.LastLogonTimestamp) { [DateTime]::FromFileTime($_.LastLogonTimestamp) } else { $null }
                    PasswordLastSet            = $_.PasswordLastSet
                    ServicePrincipalNames      = ($_.ServicePrincipalName -join '; ')
                    DistinguishedName          = $_.DistinguishedName
                    SID                        = $_.SID.Value
                    ObjectGUID                 = $_.ObjectGUID
                }
            }
        Save-ObjectData -InputObject $computers -BasePath (Join-Path $rolePath 'Computers')

        $ous = Get-ADOrganizationalUnit -Filter * -Properties Description, ProtectedFromAccidentalDeletion, gPLink, gPOptions, ManagedBy |
            Select-Object Name, Description, DistinguishedName, ObjectGUID, ProtectedFromAccidentalDeletion, ManagedBy, gPLink, gPOptions
        Save-ObjectData -InputObject $ous -BasePath (Join-Path $rolePath 'Organizational-Units')

        if (Get-Command Get-ADServiceAccount -ErrorAction SilentlyContinue) {
            $managedServiceAccounts = Get-ADServiceAccount -Filter * -Properties DNSHostName, Enabled, Description, ServicePrincipalName, PrincipalsAllowedToRetrieveManagedPassword, ManagedPasswordIntervalInDays, KerberosEncryptionType |
                ForEach-Object {
                    [pscustomobject]@{
                        Name                                      = $_.Name
                        SamAccountName                            = $_.SamAccountName
                        DNSHostName                               = $_.DNSHostName
                        Enabled                                   = $_.Enabled
                        Description                               = $_.Description
                        ServicePrincipalNames                     = ($_.ServicePrincipalName -join '; ')
                        PrincipalsAllowedToRetrieveManagedPassword = ($_.PrincipalsAllowedToRetrieveManagedPassword -join '; ')
                        ManagedPasswordIntervalInDays             = $_.ManagedPasswordIntervalInDays
                        KerberosEncryptionType                    = $_.KerberosEncryptionType
                        DistinguishedName                         = $_.DistinguishedName
                        SID                                       = $_.SID.Value
                        ObjectGUID                                = $_.ObjectGUID
                    }
                }
            Save-ObjectData -InputObject $managedServiceAccounts -BasePath (Join-Path $rolePath 'Managed-Service-Accounts')
        }

        'Passwords and password hashes are never exported. Recreate or recover credentials through approved identity and password-vault procedures.' |
            Set-Content -Path (Join-Path $rolePath 'PASSWORDS-NOT-EXPORTED.txt') -Encoding UTF8
    }
}

if ($IncludeGpoBackup -or (Test-InventoryCategory -Category @('GroupPolicy'))) {
    Invoke-Collector -Name 'Domain Group Policy backup and reports' -Action {
        $rolePath = Join-Path $paths.Roles 'Group-Policy'
        $backupPath = Join-Path $rolePath 'GPO-Backups'
        New-Item -Path $backupPath -ItemType Directory -Force | Out-Null

        if (-not (Get-Command Get-GPO -ErrorAction SilentlyContinue)) {
            try { Import-Module GroupPolicy -ErrorAction Stop }
            catch {
                ('The GroupPolicy PowerShell module is unavailable. Local/effective policy collection remains available elsewhere in the package. Install GPMC/RSAT Group Policy tools for domain GPO inventory and Backup-GPO output. Error: ' + $_.Exception.Message) | Set-Content -Path (Join-Path $rolePath 'GroupPolicy-Module-Unavailable.txt') -Encoding UTF8
                return
            }
        }

        if (Test-ExportCategory -Category @('GroupPolicy')) {
            Backup-GPO -All -Path $backupPath -Comment ('Server inventory configuration export ' + $timestamp) -Confirm:$false |
                Export-Csv -Path (Join-Path $rolePath 'GPO-Backup-Results.csv') -NoTypeInformation -Encoding UTF8
        }
        else {
            'Group Policy inventory was selected, but GPO backup/export was not selected.' | Set-Content -Path (Join-Path $rolePath 'GPO-Backup-Not-Selected.txt') -Encoding UTF8
        }
        Get-GPOReport -All -ReportType Html -Path (Join-Path $rolePath 'All-GPOs.html')
        Get-GPOReport -All -ReportType Xml -Path (Join-Path $rolePath 'All-GPOs.xml')

        $gpos = @(Get-GPO -All)
        Save-ObjectData -InputObject $gpos -BasePath (Join-Path $rolePath 'GPO-Inventory')

        $permissions = foreach ($gpo in $gpos) {
            Get-GPPermission -Guid $gpo.Id -All -ErrorAction SilentlyContinue |
                Select-Object @{Name = 'GpoName'; Expression = { $gpo.DisplayName }}, @{Name = 'GpoId'; Expression = { $gpo.Id }}, Trustee, TrusteeType, Permission, Inherited
        }
        Save-ObjectData -InputObject $permissions -BasePath (Join-Path $rolePath 'GPO-Permissions')

        $targets = @()
        if (-not (Get-Command Get-ADDomain -ErrorAction SilentlyContinue)) { try { Import-Module ActiveDirectory -ErrorAction Stop } catch { } }
        if (Get-Command Get-ADDomain -ErrorAction SilentlyContinue) {
            $domain = Get-ADDomain
            $targets = @($domain.DNSRoot) + @(Get-ADOrganizationalUnit -Filter * | Select-Object -ExpandProperty DistinguishedName)
        } else {
            ('GPO inventory and backup completed, but OU/domain inheritance enumeration was skipped because the ActiveDirectory module is unavailable.') | Set-Content -Path (Join-Path $rolePath 'GPO-Inheritance-ADModule-Unavailable.txt') -Encoding UTF8
        }
        $inheritance = foreach ($target in $targets) {
            try {
                $result = Get-GPInheritance -Target $target -ErrorAction Stop
                [pscustomobject]@{
                    Target             = $target
                    GpoInheritanceBlocked = $result.GpoInheritanceBlocked
                    GpoLinks           = (($result.GpoLinks | ForEach-Object { '{0} [{1}] Enabled={2} Enforced={3}' -f $_.DisplayName, $_.GpoId, $_.Enabled, $_.Enforced }) -join ' | ')
                    InheritedGpoLinks  = (($result.InheritedGpoLinks | ForEach-Object { '{0} [{1}] Enabled={2} Enforced={3}' -f $_.DisplayName, $_.GpoId, $_.Enabled, $_.Enforced }) -join ' | ')
                }
            }
            catch {
                [pscustomobject]@{ Target = $target; Error = $_.Exception.Message }
            }
        }
        Save-ObjectData -InputObject $inheritance -BasePath (Join-Path $rolePath 'GPO-Link-and-Inheritance-Inventory')

        $scriptsSource = Join-Path $env:SystemRoot ('SYSVOL\sysvol\' + $domain.DNSRoot + '\scripts')
        if ((Test-ExportCategory -Category @('GroupPolicy')) -and (Test-Path -LiteralPath $scriptsSource)) {
            Copy-Item -LiteralPath $scriptsSource -Destination (Join-Path $rolePath 'SYSVOL-Scripts') -Recurse -Force
        }

        'GPO backups and SYSVOL scripts can contain highly sensitive settings or embedded credentials. Encrypt and tightly restrict the package.' |
            Set-Content -Path (Join-Path $rolePath 'WARNING-SENSITIVE-GPO-DATA.txt') -Encoding UTF8
    }
}

if ($IncludeEventLogs -or (Test-InventoryCategory -Category @('EventLogs'))) {
    Invoke-Collector -Name 'Recent System and Application event history' -Action {
        $startTime = (Get-Date).AddDays(-1 * $EventLogDays)
        $eventLogs = Get-WinEvent -ListLog * -ErrorAction SilentlyContinue |
            Select-Object LogName, RecordCount, IsEnabled, LogMode, MaximumSizeInBytes, LogFilePath, LastWriteTime
        Save-ObjectData -InputObject $eventLogs -BasePath (Join-Path $paths.Events 'Event-Log-Configuration')

        foreach ($logName in @('System', 'Application')) {
            $events = Get-WinEvent -FilterHashtable @{
                LogName   = $logName
                Level     = 1, 2, 3
                StartTime = $startTime
            } -ErrorAction SilentlyContinue |
                Select-Object TimeCreated, Id, LevelDisplayName, ProviderName, MachineName, UserId, ProcessId, ThreadId, Message
            Save-ObjectData -InputObject $events -BasePath (Join-Path $paths.Events ($logName + '-Critical-Error-Warning-Last-' + $EventLogDays + '-Days'))
        }
    }
}

Invoke-Collector -Name 'Selected inventory registry configuration' -Action {
    $registryPath = Join-Path $paths.System 'Registry-Configuration'
    New-Item -Path $registryPath -ItemType Directory -Force | Out-Null

    $registryExports = @(
        @{ Key = 'HKLM\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters'; File = 'TCPIP-Parameters.reg' },
        @{ Key = 'HKLM\SYSTEM\CurrentControlSet\Services\LanmanServer\Parameters'; File = 'SMB-Server-Parameters.reg' },
        @{ Key = 'HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment'; File = 'Machine-Environment.reg' },
        @{ Key = 'HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion'; File = 'Windows-Version-Registry.reg' }
    )

    foreach ($entry in $registryExports) {
        try {
            Export-RegistryKey -RegistryPath $entry.Key -DestinationFile (Join-Path $registryPath $entry.File)
        }
        catch {
            ('Registry export failed for ' + $entry.Key + ': ' + $_.Exception.Message) |
                Set-Content -Path (Join-Path $registryPath ($entry.File + '.error.txt')) -Encoding UTF8
        }
    }
}

# Capture the final role-specific recovery/migration matrix after optional exports have run,
# so BackupExportStatus reflects the actual outcome of this collection.
Save-ObjectData -InputObject (Get-RoleRecoveryReferenceRows -RoleRows $detectedRoleRows) -BasePath (Join-Path $paths.Summary 'Detected-Role-Recovery-Migration-Reference')

$docxReportPath = Join-Path $packagePath 'OPEN-FIRST - Server Inventory Report.docx'
$htmlReportPath = Join-Path $packagePath 'OPEN-FIRST - Server Inventory Report.html'
$summaryTextPath = Join-Path $packagePath 'OPEN-FIRST - Server Inventory Summary.txt'

Invoke-Collector -Name 'Consolidated inventory report generation' -Action {
    $script:consolidatedReportResult = New-ConsolidatedServerReport -DocxPath $docxReportPath -HtmlPath $htmlReportPath -SummaryTextPath $summaryTextPath
}

# Save collector status before stopping the transcript or evaluating report completion.
$script:collectorResults | Export-Csv -Path (Join-Path $paths.Summary 'Collector-Results.csv') -NoTypeInformation -Encoding UTF8
$detectedRoleRows | Export-Csv -Path (Join-Path $paths.Summary 'Detected-Critical-Roles-and-Platforms.csv') -NoTypeInformation -Encoding UTF8

$reportCollectorRecord = @($script:collectorResults | Where-Object { $_.Collector -eq 'Consolidated inventory report generation' } | Select-Object -Last 1)
$missingReportFiles = @(
    @($docxReportPath, $htmlReportPath, $summaryTextPath) | Where-Object {
        -not (Test-Path -LiteralPath $_ -PathType Leaf) -or ((Get-Item -LiteralPath $_ -ErrorAction SilentlyContinue).Length -le 0)
    }
)
$reportFailed = ($reportCollectorRecord.Count -eq 0) -or ($reportCollectorRecord[0].Status -ne 'Success') -or ($missingReportFiles.Count -gt 0)

if ($script:transcriptStarted) {
    try { Stop-Transcript | Out-Null } catch { }
    $script:transcriptStarted = $false
}

if ($reportFailed) {
    $reportFailureMessage = 'Consolidated report generation failed. No completion marker or successful ZIP package will be created.'
    if (($reportCollectorRecord.Count -gt 0) -and $reportCollectorRecord[0].ErrorMessage) {
        $reportFailureMessage += ' Error: ' + [string]$reportCollectorRecord[0].ErrorMessage
    }
    if ($missingReportFiles.Count -gt 0) {
        $reportFailureMessage += ' Missing or empty report files: ' + ($missingReportFiles -join '; ')
    }
    Write-InventoryLog -Message $reportFailureMessage -Level 'ERROR'
    throw $reportFailureMessage
}

# Generate a manifest after collection completes.
$manifest = Get-ChildItem -LiteralPath $packagePath -File -Recurse |
    Where-Object { $_.Name -ne 'SHA256-Manifest.csv' } |
    ForEach-Object {
        $hash = Get-SHA256FileHash -Path $_.FullName
        [pscustomobject]@{
            RelativePath = $_.FullName.Substring($packagePath.Length).TrimStart('\')
            LengthBytes  = $_.Length
            LastWriteTime = $_.LastWriteTime
            SHA256       = $hash
        }
    }
$manifest | Export-Csv -Path (Join-Path $packagePath 'SHA256-Manifest.csv') -NoTypeInformation -Encoding UTF8

$zipPath = $null
if ($CreateZip) {
    $zipPath = Join-Path $OutputRoot ($packageName + '.zip')
    if (Test-Path -LiteralPath $zipPath) {
        Remove-Item -LiteralPath $zipPath -Force
    }

    if (Get-Command Compress-Archive -ErrorAction SilentlyContinue) {
        Compress-Archive -Path (Join-Path $packagePath '*') -DestinationPath $zipPath -CompressionLevel Optimal -Force
    }
    else {
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        [System.IO.Compression.ZipFile]::CreateFromDirectory($packagePath, $zipPath, [System.IO.Compression.CompressionLevel]::Optimal, $false)
    }

    $zipHash = Get-SHA256FileHash -Path $zipPath
    ('{0} *{1}' -f $zipHash, (Split-Path -Path $zipPath -Leaf)) |
        Set-Content -Path ($zipPath + '.sha256.txt') -Encoding ASCII
}

$collectorCompletion = [pscustomobject]@{
    Status        = 'Completed'
    # BEGIN APPROVED ACL EXPORT COMPLETION METADATA
    IncompleteAclExports = $script:aclExportsIncompleteCount
    AclExportStatus = $(if ($script:aclExportsIncompleteCount -gt 0) { 'Incomplete' } else { 'NoIncompleteExports' })
    # END APPROVED ACL EXPORT COMPLETION METADATA
    CompletedAt   = Get-Date
    ComputerName  = $env:COMPUTERNAME
    ScriptVersion = $scriptVersion
    RunId         = $RunId
    PackagePath   = $packagePath
    ZipPath       = $zipPath
    DocxReportPath = $docxReportPath
    HtmlReportPath = $htmlReportPath
    SummaryTextPath = $summaryTextPath
}
$collectorCompletion | Format-List * | Out-String -Width 4096 |
    Set-Content -Path (Join-Path $paths.Summary 'COLLECTION-COMPLETED.txt') -Encoding UTF8
$collectorCompletion | ConvertTo-Json -Depth 4 |
    Set-Content -Path (Join-Path $paths.Summary 'COLLECTION-COMPLETED.json') -Encoding UTF8

Write-Host ''
Write-Host 'Server inventory and configuration collection completed.'
Write-Host ('Folder: ' + $packagePath)
if ($zipPath) {
    Write-Host ('ZIP:    ' + $zipPath)
}
Write-Host ('Word report: ' + $docxReportPath)
Write-Host ('HTML report: ' + $htmlReportPath)
Write-Host ('Text summary: ' + $summaryTextPath)
Write-Host 'Open the consolidated report first. Supporting files are retained for validation, recovery, migration, and rebuild assistance.'
