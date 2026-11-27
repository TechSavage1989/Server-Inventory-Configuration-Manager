[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidateSet('Discover', 'Precheck', 'Sync', 'Cutover', 'SourceReadOnly', 'SourceRestore', 'DfsReport', 'AliasCheck', 'Alias')][string]$Mode,
    [string]$Source,
    [string]$PlanPath,
    [string]$ResultPath,
    [string]$WorkPath,
    [string]$CancelPath,
    # Reads one JSON line {"user":..,"password":..} from standard input: the
    # account used for the old server. Never passed on the command line.
    [switch]$CredentialFromInput,
    # The new server when it is not the computer running this engine. Only
    # DfsReport, AliasCheck and Alias use it: they run on the admin computer
    # and name the new server in their suggestions and checks.
    [string]$TargetName,
    # Embedded: the engine runs inside the new server through PowerShell
    # remoting (Invoke-ShareMigrationRemote.ps1). It never calls exit then,
    # writes its lines to -LogPath for the console to read, and gets the old
    # server's account as a credential object instead of standard input.
    [switch]$Embedded,
    [string]$LogPath,
    [System.Management.Automation.PSCredential]$SourceCredentialObject
)

# Moves file shares from an old server to this server. Runs on the new server.
# Discover lists the old server's shares. Precheck checks space, paths and
# accounts. Sync copies with Robocopy and can be repeated; each pass copies only
# what changed. Cutover runs a final sync, creates the shares here with the old
# share settings and permissions, and verifies the copy. The old server is
# changed only by the separate SourceReadOnly step, which saves the previous
# share permissions first so SourceRestore can put them back.
# With -CredentialFromInput, the old server is reached with that account for
# remote management (CIM) and for files (an SMB connection made for this run
# and removed when it ends); otherwise with the account running this engine.
# From an admin computer, Invoke-ShareMigrationRemote.ps1 runs this engine
# inside the new server (-Embedded), so the new server copies straight from
# the old one: files cross the network once, and nothing is installed on
# either server.
# Compatible with Windows PowerShell 3.0 (Windows Server 2012) and later.

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }
$script:ExitCode = 0
$script:LineWriter = $null
$script:EngineLock = $null
if ($LogPath) {
    $logParent = Split-Path -Path $LogPath -Parent
    if ($logParent -and -not (Test-Path -LiteralPath $logParent)) { New-Item -Path $logParent -ItemType Directory -Force | Out-Null }
    # Shared for reading, so the console can follow the log while it grows.
    $script:LineWriter = New-Object System.IO.StreamWriter((New-Object System.IO.FileStream($LogPath, [System.IO.FileMode]::Append, [System.IO.FileAccess]::Write, [System.IO.FileShare]::ReadWrite)), (New-Object System.Text.UTF8Encoding($false)))
    $script:LineWriter.AutoFlush = $true
}

function Write-Line {
    param([string]$Text)
    if ($script:LineWriter) { $script:LineWriter.WriteLine($Text); return }
    [Console]::Out.WriteLine($Text); [Console]::Out.Flush()
}
function Write-Log { param([string]$Level, [string]$Message) Write-Line ('{0} [{1}] {2}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Level, $Message) }
function Write-MigrateEvent { param([hashtable]$Event) Write-Line ('@@MIGRATE@@' + (ConvertTo-Json -InputObject $Event -Compress -Depth 6)) }
function Write-Utf8File {
    param([string]$Path, [string]$Text)
    $parent = Split-Path -Path $Path -Parent
    if ($parent -and -not (Test-Path -LiteralPath $parent)) { New-Item -Path $parent -ItemType Directory -Force | Out-Null }
    [System.IO.File]::WriteAllText($Path, $Text, (New-Object System.Text.UTF8Encoding($false)))
}
function Write-Result { param($Value) if ($ResultPath) { Write-Utf8File -Path $ResultPath -Text (ConvertTo-Json -InputObject $Value -Depth 8) } }
function Test-Administrator {
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}
function Test-CancelRequested { return [bool]($CancelPath -and (Test-Path -LiteralPath $CancelPath)) }

$script:ComputerName = [string]$env:COMPUTERNAME
if ($TargetName) { $script:ComputerName = ([string]$TargetName).Split('.')[0].ToUpperInvariant() }
$script:Robocopy = Join-Path $env:SystemRoot 'System32\robocopy.exe'
$script:SystemShares = @('NETLOGON', 'SYSVOL', 'PRINT$', 'FAX$', 'CERTENROLL', 'REMINST', 'WSUSCONTENT', 'WSUSTEMP', 'UPDATESERVICESPACKAGES')
$script:ExcludedFolders = @('System Volume Information', '$RECYCLE.BIN', 'DfsrPrivate')

function Get-SourceName {
    param([string]$Name)
    $value = ([string]$Name).Trim().TrimStart('\').Split('\')[0]
    if (-not $value) { throw 'Type the old server''s name.' }
    if ($value -notmatch '^[A-Za-z0-9][A-Za-z0-9.\-]{0,252}$') { throw 'The server name can contain only letters, digits, dots and hyphens.' }
    return $value
}

function Test-IsThisServer {
    param([string]$Name)
    $short = $Name.Split('.')[0]
    return $short -eq $script:ComputerName -or $Name -eq 'localhost' -or $Name -eq '127.0.0.1'
}

$script:SourceCredential = $null
$script:SourceAccount = ''
$script:SourceConnection = $null

function Read-SourceCredential {
    if ($SourceCredentialObject) {
        $script:SourceAccount = [string]$SourceCredentialObject.UserName
        $script:SourceCredential = $SourceCredentialObject
        return
    }
    if (-not $CredentialFromInput) { return }
    $line = [Console]::In.ReadLine()
    $data = $null
    if ($line) { $data = ConvertFrom-Json -InputObject $line }
    $line = $null
    if (-not $data -or -not [string]$data.user -or -not [string]$data.password) { throw 'The account for the old server was not provided.' }
    $script:SourceAccount = [string]$data.user
    $script:SourceCredential = New-Object System.Management.Automation.PSCredential($script:SourceAccount, (ConvertTo-SecureString -String ([string]$data.password) -AsPlainText -Force))
    $data = $null
}

function Get-AccountLabel {
    if ($script:SourceAccount) { return $script:SourceAccount }
    return 'this Windows account'
}

function Write-AccessDenied {
    # Tells the interface to offer the Connect as account fields.
    Write-MigrateEvent @{ event = 'accessDenied'; message = '' }
}

function Get-AccessDeniedAdvice {
    param([string]$Name)
    if ($script:SourceAccount) {
        return ('Check the user name and password, and that ' + $script:SourceAccount + ' is an administrator on ' + $Name + '.')
    }
    return ('Under Connect as, choose Another account and enter an account that is an administrator on ' + $Name + ', for example DOMAIN\admin, then connect again. A local account with the same name and password is not enough: Windows removes administrator rights from local accounts over the network.')
}

function Connect-SourceFiles {
    # Opens an SMB connection to the old server with the Connect as account, so
    # Test-Path, Get-Acl and Robocopy reach its administrative shares with it.
    # The connection belongs to this run and is removed by Disconnect-SourceFiles.
    param([string]$Name)
    if (-not $script:SourceCredential -or (Test-IsThisServer $Name)) { return }
    if (-not ('SicmSourceConnection' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class SicmSourceConnection {
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    public class NetResource {
        public int Scope; public int Type; public int DisplayType; public int Usage;
        public string LocalName; public string RemoteName; public string Comment; public string Provider;
    }
    [DllImport("mpr.dll", CharSet = CharSet.Unicode)]
    private static extern int WNetAddConnection2(NetResource resource, string password, string user, int flags);
    [DllImport("mpr.dll", CharSet = CharSet.Unicode)]
    private static extern int WNetCancelConnection2(string name, int flags, bool force);
    public static int Connect(string remote, string user, string password) {
        NetResource resource = new NetResource();
        resource.RemoteName = remote;
        return WNetAddConnection2(resource, password, user, 4); // CONNECT_TEMPORARY
    }
    public static int Disconnect(string remote) { return WNetCancelConnection2(remote, 0, true); }
}
'@
    }
    $remote = '\\' + $Name + '\IPC$'
    $code = [SicmSourceConnection]::Connect($remote, $script:SourceAccount, $script:SourceCredential.GetNetworkCredential().Password)
    if ($code -eq 0) {
        $script:SourceConnection = $remote
        Write-Log 'INFO' ('Opened a file connection to ' + $Name + ' as ' + $script:SourceAccount + '.')
        return
    }
    $reason = ([string](New-Object System.ComponentModel.Win32Exception($code)).Message).Trim().TrimEnd('.') + '.'
    if ($code -eq 1219) {
        Write-Log 'WARN' ('Windows already has a file connection to ' + $Name + ' under another account, so it is used instead of ' + $script:SourceAccount + '. To use ' + $script:SourceAccount + ', close File Explorer windows and mapped drives that use ' + $Name + ' (net use lists them) and run this step again.')
        return
    }
    if (@(5, 86, 1326, 1327, 1328, 1329, 1330, 1331, 1907, 1909) -contains $code) {
        Write-AccessDenied
        throw ($Name + ' did not accept ' + $script:SourceAccount + ' for file access: ' + $reason + ' ' + (Get-AccessDeniedAdvice $Name))
    }
    throw ('Could not open a file connection to ' + $Name + ' as ' + $script:SourceAccount + ': ' + $reason + ' Check that ' + $Name + ' is on and that File and Printer Sharing is allowed in its firewall.')
}

function Disconnect-SourceFiles {
    if ($script:SourceConnection) {
        [void][SicmSourceConnection]::Disconnect($script:SourceConnection)
        $script:SourceConnection = $null
    }
}

function New-SourceCimSession {
    # Tries WinRM first (enabled by default on Windows Server 2012 and later),
    # then DCOM with packet privacy, which Windows' DCOM hardening updates
    # require. Each session is checked with a real query before it is used.
    param([string]$Name)
    if (Test-IsThisServer $Name) { return $null }
    $failures = @()
    foreach ($protocol in @('WinRM', 'DCOM')) {
        $session = $null
        try {
            $arguments = @{ ComputerName = $Name; ErrorAction = 'Stop' }
            if ($script:SourceCredential) { $arguments['Credential'] = $script:SourceCredential }
            # The DCOM parameter set: -PacketPrivacy cannot be combined with -Protocol.
            if ($protocol -eq 'DCOM') { $arguments['SessionOption'] = New-CimSessionOption -PacketPrivacy -Impersonation Impersonate }
            $session = New-CimSession @arguments
            [void](Get-CimInstance -CimSession $session -ClassName Win32_OperatingSystem -ErrorAction Stop)
            Write-Log 'INFO' ('Connected to ' + $Name + ' over ' + $protocol + ' as ' + (Get-AccountLabel) + '.')
            return $session
        }
        catch {
            $failures += ($protocol + ': ' + $_.Exception.Message.Trim())
            if ($session) { Remove-CimSession -CimSession $session -ErrorAction SilentlyContinue }
        }
    }
    $details = $failures -join ' | '
    if ($details -match 'Access is denied|0x80070005|E_ACCESSDENIED|0x8009030e|0x8009030c|0x80090311|logon session does not exist|user name or password') {
        Write-AccessDenied
        throw ('Windows refused access on ' + $Name + ' for ' + (Get-AccountLabel) + '. ' + (Get-AccessDeniedAdvice $Name) + ' (' + $details + ')')
    }
    throw ('Could not connect to ' + $Name + '. Check that it is on and reachable, and that remote management is allowed: WinRM (run winrm quickconfig on ' + $Name + ') or the Windows Management Instrumentation rules in Windows Firewall. (' + $details + ')')
}

function Get-ShareAccessEntries {
    param([string]$ShareName, $Session)
    $arguments = @{ Name = $ShareName; ErrorAction = 'Stop' }
    if ($Session) { $arguments['CimSession'] = $Session }
    $entries = @()
    foreach ($entry in @(Get-SmbShareAccess @arguments)) {
        $entries += @{ account = [string]$entry.AccountName; type = [string]$entry.AccessControlType; right = [string]$entry.AccessRight }
    }
    return $entries
}

function Get-RemotePath {
    # Admin-share path to a folder on the old server, e.g. \\OLD\D$\Shares\Finance.
    param([string]$Server, [string]$LocalPath)
    if ($LocalPath -notmatch '^[A-Za-z]:\\?') { return '' }
    if (Test-IsThisServer $Server) {
        # A drive root stays 'D:\.'; a bare 'D:' would mean the current folder.
        $trimmed = $LocalPath.TrimEnd('\')
        if ($trimmed.Length -eq 2) { return $trimmed + '\.' }
        return $trimmed
    }
    return ('\\{0}\{1}${2}' -f $Server, $LocalPath.Substring(0, 1), $LocalPath.Substring(2).TrimEnd('\'))
}

# ------------------------------------------------------------------ discover --

function Invoke-Discover {
    $name = Get-SourceName $Source
    $result = @{ source = $name; sourceFqdn = $name; currentComputer = $script:ComputerName; sameServer = (Test-IsThisServer $name); isAdministrator = (Test-Administrator); shares = @(); localShares = @(); volumes = @(); warnings = @() }
    try { $result.sourceFqdn = [System.Net.Dns]::GetHostEntry($name).HostName } catch { throw ('The server name ' + $name + ' could not be found in DNS.') }
    $session = $null
    $session = New-SourceCimSession $name
    try {
        $wmi = @{ ClassName = 'Win32_Share'; ErrorAction = 'Stop' }
        if ($session) { $wmi['CimSession'] = $session }
        $raw = @(Get-CimInstance @wmi)
        $details = @{}
        $smbArgs = @{ ErrorAction = 'SilentlyContinue' }
        if ($session) { $smbArgs['CimSession'] = $session }
        foreach ($share in @(Get-SmbShare @smbArgs)) { $details[[string]$share.Name] = $share }
        foreach ($share in $raw) {
            $type = [uint32]$share.Type
            $isAdmin = ($type -band [uint32]2147483648) -ne 0
            $baseType = $type -band 0xFF
            if ($isAdmin -or $baseType -ne 0) { continue }
            $shareName = [string]$share.Name
            $item = @{
                name = $shareName; path = [string]$share.Path; description = [string]$share.Description
                system = ($script:SystemShares -contains $shareName.ToUpperInvariant())
                remotePath = (Get-RemotePath -Server $name -LocalPath ([string]$share.Path))
                folderEnumerationMode = ''; cachingMode = ''; encryptData = $false; concurrentUserLimit = 0
                access = @(); warnings = @()
            }
            $detail = $details[$shareName]
            if ($detail) {
                $item.folderEnumerationMode = [string]$detail.FolderEnumerationMode
                $item.cachingMode = [string]$detail.CachingMode
                $item.encryptData = [bool]$detail.EncryptData
                $item.concurrentUserLimit = [uint32]$detail.ConcurrentUserLimit
                if ($detail.ScopeName -and [string]$detail.ScopeName -ne '*') { $item.warnings += ('This share belongs to the clustered or scoped name ' + $detail.ScopeName + '.') }
            }
            if (-not $detail) { $item.warnings += 'Share settings and permissions could not be read from the old server (it may be older than Windows Server 2012). The new share will allow only Administrators until you set its permissions.' }
            try { $item.access = @(Get-ShareAccessEntries -ShareName $shareName -Session $session) }
            catch { $item.warnings += ('Share permissions could not be read: ' + $_.Exception.Message) }
            if ($item.system) { $item.warnings += 'System share. It is normally re-created by its role (for example Active Directory or Print Services). Move it only if you know you need its files.' }
            if (-not $item.remotePath) { $item.warnings += 'The share''s folder is not on a local drive of the old server, so it cannot be copied from here.' }
            $result.shares += $item
        }
    }
    finally { if ($session) { Remove-CimSession -CimSession $session -ErrorAction SilentlyContinue } }
    foreach ($share in @(Get-SmbShare -ErrorAction SilentlyContinue)) { $result.localShares += [string]$share.Name }
    foreach ($volume in @(Get-WmiObject -Class Win32_LogicalDisk -Filter 'DriveType=3')) {
        $result.volumes += @{ drive = [string]$volume.DeviceID; freeBytes = [uint64]$volume.FreeSpace; sizeBytes = [uint64]$volume.Size }
    }
    if ($result.sameServer) { $result.warnings += 'The old server is this server. That is only useful for testing.' }
    Write-Result $result
}

# ------------------------------------------------------------------ helpers ---

function Read-Plan {
    if (-not $PlanPath -or -not (Test-Path -LiteralPath $PlanPath)) { throw 'The migration plan was not provided.' }
    $plan = Get-Content -LiteralPath $PlanPath -Raw | ConvertFrom-Json
    $plan.source = Get-SourceName ([string]$plan.source)
    foreach ($share in @($plan.shares)) {
        if ([string]$share.destPath -notmatch '^[A-Za-z]:\\[^<>"|?*]+$') { throw ('The new folder for ' + $share.name + ' must be a full local path such as D:\Shares\' + $share.name + '.') }
        if ([string]$share.destName -notmatch '^[^\\/:*?"<>|\[\]+=;,]{1,80}$') { throw ('The share name ' + $share.destName + ' is not valid.') }
        if ([string]$share.destName -match '^(?i:ADMIN\$|IPC\$|[A-Z]\$)$') { throw ('The share name ' + $share.destName + ' is reserved for Windows administration and cannot be replaced.') }
    }
    return $plan
}

function Get-RobocopySummary {
    # Robocopy's summary labels are localized, but the four rows (directories,
    # files, bytes, times) and their columns (total, copied, skipped,
    # mismatch, failed, extras) are always in this order.
    param([string]$LogPath)
    $summary = @{ dirs = @(); files = @(); bytes = @() }
    if (-not (Test-Path -LiteralPath $LogPath)) { return $summary }
    $rows = @()
    foreach ($line in @(Get-Content -LiteralPath $LogPath -Tail 40)) {
        if ($line -match ':\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s*$') { $rows += , @([uint64]$Matches[1], [uint64]$Matches[2], [uint64]$Matches[3], [uint64]$Matches[4], [uint64]$Matches[5], [uint64]$Matches[6]) }
    }
    if ($rows.Count -ge 3) { $summary.dirs = $rows[$rows.Count - 3]; $summary.files = $rows[$rows.Count - 2]; $summary.bytes = $rows[$rows.Count - 1] }
    elseif ($rows.Count -eq 2) { $summary.dirs = $rows[0]; $summary.files = $rows[1] }
    return $summary
}

function Get-CommonRobocopyArguments {
    $arguments = @('/R:2', '/W:2', '/XJ', '/NP', '/BYTES', '/XD')
    $arguments += $script:ExcludedFolders
    return $arguments
}

function Invoke-RobocopyRun {
    # Runs Robocopy as a child process, reports progress from its log, and
    # stops it when the operator asks. Robocopy can always be run again.
    param([string]$From, [string]$To, [string[]]$Options, [string]$LogPath, [string]$ShareName, [uint64]$ExpectedFiles)
    # Start-Process joins arguments with spaces, so quote any that contain one
    # (for example the excluded folder "System Volume Information").
    $quoted = @()
    foreach ($option in $Options) { if ($option -match '\s' -and $option -notmatch '^".*"$') { $quoted += ('"' + $option + '"') } else { $quoted += $option } }
    $arguments = @('"' + $From + '"', '"' + $To + '"') + $quoted + @('/UNILOG:"' + $LogPath + '"')
    Write-Log 'INFO' ('Running: robocopy ' + ($arguments -join ' '))
    $process = Start-Process -FilePath $script:Robocopy -ArgumentList $arguments -PassThru -WindowStyle Hidden
    $position = 0L; $lines = 0L; $lastReport = Get-Date
    while (-not $process.HasExited) {
        Start-Sleep -Milliseconds 1000
        if (Test-CancelRequested) {
            try { $process.Kill() } catch { }
            $process.WaitForExit()
            return -1
        }
        if (((Get-Date) - $lastReport).TotalSeconds -ge 3 -and (Test-Path -LiteralPath $LogPath)) {
            try {
                $stream = New-Object System.IO.FileStream($LogPath, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
                try {
                    [void]$stream.Seek($position, [System.IO.SeekOrigin]::Begin)
                    $buffer = New-Object byte[] 1048576
                    while (($read = $stream.Read($buffer, 0, $buffer.Length)) -gt 0) {
                        for ($i = 0; $i -lt $read - 1; $i += 2) { if ($buffer[$i] -eq 10 -and $buffer[$i + 1] -eq 0) { $lines++ } }
                        $position += $read - ($read % 2)
                        if ($read % 2) { [void]$stream.Seek($position, [System.IO.SeekOrigin]::Begin) }
                    }
                }
                finally { $stream.Dispose() }
            } catch { }
            $percent = 0
            if ($ExpectedFiles -gt 0) { $percent = [Math]::Min(99, [int](100 * $lines / [double]$ExpectedFiles)) }
            Write-MigrateEvent @{ event = 'progress'; share = $ShareName; processed = $lines; expected = $ExpectedFiles; percent = $percent }
            $lastReport = Get-Date
        }
    }
    $process.WaitForExit()
    return [int]$process.ExitCode
}

function Get-ShareSource {
    param($Plan, $Share)
    $path = Get-RemotePath -Server $Plan.source -LocalPath ([string]$Share.path)
    if (-not $path) { throw ('The folder of ' + $Share.name + ' is not on a local drive of the old server.') }
    return $path
}

# ------------------------------------------------------------------ precheck --

function Get-AccountWarnings {
    param($Plan, $Share, [string]$From)
    $warnings = @()
    $sourceShort = ([string]$Plan.source).Split('.')[0]
    $accounts = @()
    foreach ($entry in @($Share.access)) { $accounts += [string]$entry.account }
    try { foreach ($rule in @((Get-Acl -LiteralPath $From).Access)) { $accounts += [string]$rule.IdentityReference } } catch { $warnings += ('The folder''s NTFS permissions could not be read: ' + $_.Exception.Message) }
    $local = @($accounts | Where-Object { $_ -like ($sourceShort + '\*') } | Sort-Object -Unique)
    if ($local.Count -and -not (Test-IsThisServer $Plan.source)) { $warnings += ('Permissions name local accounts of the old server, which do not exist here: ' + ($local -join ', ') + '. Re-grant them to domain or local groups on this server afterwards.') }
    $unresolved = @($accounts | Where-Object { $_ -match '^S-1-5-21-' } | Sort-Object -Unique)
    if ($unresolved.Count) { $warnings += ('Permissions include accounts that no longer exist (shown as SIDs): ' + ($unresolved -join ', ') + '.') }
    return $warnings
}

function Invoke-Precheck {
    if (-not (Test-Administrator)) { throw 'Moving file shares needs administrator rights. Run the application as administrator.' }
    $plan = Read-Plan
    $results = @(); $needed = @{}
    $localShares = @{}
    foreach ($share in @(Get-SmbShare -ErrorAction SilentlyContinue)) { $localShares[[string]$share.Name.ToUpperInvariant()] = [string]$share.Path }
    foreach ($share in @($plan.shares)) {
        if (Test-CancelRequested) { break }
        Write-MigrateEvent @{ event = 'share'; share = [string]$share.name; status = 'checking'; message = 'Checking.' }
        $item = @{ name = [string]$share.name; ok = $true; problems = @(); warnings = @(); files = 0; bytes = 0 }
        $from = ''
        try { $from = Get-ShareSource -Plan $plan -Share $share } catch { $item.problems += $_.Exception.Message }
        if ($from -and -not (Test-Path -LiteralPath $from)) { $item.problems += ('The old folder ' + $from + ' cannot be reached with ' + (Get-AccountLabel) + '. Check that the account is an administrator on the old server (choose it under Connect as and connect again) and that the old server''s administrative shares are enabled.') }
        $dest = [string]$share.destPath
        $drive = $dest.Substring(0, 2).ToUpperInvariant()
        if (-not (Test-Path -LiteralPath ($drive + '\'))) { $item.problems += ('Drive ' + $drive + ' does not exist on this server.') }
        $known = [bool]$share.previouslySynced
        $overwrite = [bool]$share.allowOverwrite
        if ((Test-Path -LiteralPath $dest) -and -not $known) {
            if (@(Get-ChildItem -LiteralPath $dest -Force -ErrorAction SilentlyContinue | Select-Object -First 1).Count) {
                if (-not $overwrite) { $item.problems += ('The new folder ' + $dest + ' already contains files. Choose an empty folder or explicitly allow overwrite for this share.') }
                else { $item.warnings += ('Overwrite selected: files in ' + $dest + ' that are absent from the old share will be deleted by each mirror copy.') }
            }
        }
        if ($from -and ($dest.TrimEnd('\') -eq $from.TrimEnd('\'))) { $item.problems += 'The new folder is the old folder.' }
        $existing = $localShares[([string]$share.destName).ToUpperInvariant()]
        if ($existing -and $existing.TrimEnd('\') -ne $dest.TrimEnd('\')) {
            if (-not $overwrite) { $item.problems += ('A share named ' + $share.destName + ' already exists on this server for ' + $existing + '. Select Allow overwrite to replace its mapping at cutover.') }
            else { $item.warnings += ('Overwrite selected: existing share ' + $share.destName + ' points to ' + $existing + '. Cutover will disconnect users, save its current settings and permissions, and recreate it for ' + $dest + '.') }
        }
        elseif ($existing -and -not [bool]$share.createdShare) {
            if (-not $overwrite) { $item.problems += ('The share ' + $share.destName + ' already exists for ' + $existing + '. Select Allow overwrite to change its settings and permissions.') }
            else { $item.warnings += ('Overwrite selected: existing share ' + $share.destName + ' will have its settings and permissions updated at cutover; a backup will be saved.') }
        }
        if ($overwrite -and ($script:SystemShares -contains ([string]$share.destName).ToUpperInvariant())) {
            $item.warnings += ('CAUTION: ' + $share.destName + ' is a system or role-managed share. Replacing it may interrupt domain services, Group Policy or other Windows roles. Only proceed with a verified recovery plan.')
        }
        if ($from -and -not $item.problems.Count) {
            $log = Join-Path $WorkPath ('Estimate-' + ($share.name -replace '[^A-Za-z0-9_-]', '_') + '.log')
            $code = Invoke-RobocopyRun -From $from -To $dest -Options (@('/L', '/MIR', '/NFL', '/NDL', '/NJH') + (Get-CommonRobocopyArguments)) -LogPath $log -ShareName ([string]$share.name) -ExpectedFiles 0
            if ($code -lt 0) { break }
            $summary = Get-RobocopySummary $log
            if ($summary.files.Count) { $item.files = $summary.files[0]; $item.bytes = $summary.bytes[0]; $item.toCopyBytes = $summary.bytes[1] } else { $item.warnings += 'The size of the share could not be measured.' }
            if ($code -ge 8) { $item.warnings += ('Robocopy could not read part of the share while measuring it (exit code ' + $code + '). See the estimate log.') }
            if (-not $needed.ContainsKey($drive)) { $needed[$drive] = [uint64]0 }
            # Delta copies need space for files that Robocopy would copy, not
            # the entire size already present on the destination.
            if ($summary.bytes.Count -ge 2) { $needed[$drive] += [uint64]$summary.bytes[1] }
            else { $needed[$drive] += [uint64]$item.bytes }
            $item.warnings += @(Get-AccountWarnings -Plan $plan -Share $share -From $from)
        }
        if ($item.problems.Count) { $item.ok = $false }
        Write-MigrateEvent @{ event = 'share'; share = [string]$share.name; status = $(if ($item.ok) { 'ready' } else { 'blocked' }); message = (@($item.problems) + @($item.warnings) | Select-Object -First 1) }
        $results += $item
    }
    $space = @()
    foreach ($drive in $needed.Keys) {
        $disk = Get-WmiObject -Class Win32_LogicalDisk -Filter ("DeviceID='" + $drive + "'")
        $free = [uint64]$disk.FreeSpace
        $ok = $free -gt [uint64]([double]$needed[$drive] * 1.05)
        $space += @{ drive = $drive; neededBytes = $needed[$drive]; freeBytes = $free; ok = $ok }
        if (-not $ok) { foreach ($item in $results) { foreach ($share in @($plan.shares)) { if ($share.name -eq $item.name -and ([string]$share.destPath).Substring(0, 2).ToUpperInvariant() -eq $drive) { $item.ok = $false; $item.problems += ('Drive ' + $drive + ' does not have enough free space for all shares planned on it.') } } } }
    }
    Write-Result @{ shares = $results; space = $space; canceled = (Test-CancelRequested) }
}

# ------------------------------------------------------------------ sync -------

function Get-RuleKey {
    param($Rule)
    return ('{0}|{1}|{2}|{3}|{4}' -f $Rule.IdentityReference.Value, [int]$Rule.FileSystemRights, [int]$Rule.InheritanceFlags, [int]$Rule.PropagationFlags, [int]$Rule.AccessControlType)
}

function Set-RootPermissions {
    # Robocopy copies the top folder's own list, but its inherited entries
    # then follow the new parent (for example D:\Shares) and are lost the next
    # time that parent's permissions are applied. Give the new top folder the
    # old top folder's effective permissions, inherited ones included, as its
    # own protected list, so the share grants the same access as before. Runs
    # after each successful copy (Robocopy rewrites the list when the top
    # folder changed) and only when the list differs. The files below already
    # carry the same entries, so nothing below the top folder is rewritten.
    param([string]$From, [string]$To)
    $source = Get-Acl -LiteralPath $From
    $security = New-Object System.Security.AccessControl.DirectorySecurity
    $security.SetAccessRuleProtection($true, $false)
    $skipped = @()
    $count = 0
    foreach ($rule in @($source.GetAccessRules($true, $true, [System.Security.Principal.SecurityIdentifier]))) {
        # Generic rights (for example CREATOR OWNER's GENERIC_ALL) become the
        # matching file rights, which a file system rule can hold.
        $raw = [int64]([int]$rule.FileSystemRights)
        if ($raw -lt 0) { $raw += 4294967296 }
        if ($raw -band 0x10000000) { $raw = $raw -bor 0x1F01FF }
        if ($raw -band 0x80000000) { $raw = $raw -bor 0x120089 }
        if ($raw -band 0x40000000) { $raw = $raw -bor 0x120116 }
        if ($raw -band 0x20000000) { $raw = $raw -bor 0x1200A0 }
        $rights = [System.Security.AccessControl.FileSystemRights][int]($raw -band 0x0FFFFFFF)
        try {
            $security.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($rule.IdentityReference, $rights, $rule.InheritanceFlags, $rule.PropagationFlags, $rule.AccessControlType)))
            $count++
        }
        catch { $skipped += ([string]$rule.IdentityReference.Value) }
    }
    if (-not $count) { throw ('The permissions of ' + $From + ' could not be read, so ' + $To + ' was left unchanged.') }
    $current = Get-Acl -LiteralPath $To
    $have = @($current.GetAccessRules($true, $false, [System.Security.Principal.SecurityIdentifier]) | ForEach-Object { Get-RuleKey $_ } | Sort-Object -Unique)
    $want = @($security.GetAccessRules($true, $false, [System.Security.Principal.SecurityIdentifier]) | ForEach-Object { Get-RuleKey $_ } | Sort-Object -Unique)
    if ($current.AreAccessRulesProtected -and (($have -join ';') -eq ($want -join ';'))) { return $skipped }
    # SetFileSecurity writes only this folder's list. The .NET and PowerShell
    # setters re-apply inheritance through every file below it, which is slow
    # on a large share and makes Robocopy copy every file again.
    if (-not ('SicmFolderSecurity' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class SicmFolderSecurity {
    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool SetFileSecurity(string fileName, int securityInformation, byte[] securityDescriptor);
    public static int SetDacl(string path, byte[] descriptor) {
        return SetFileSecurity(path, 4, descriptor) ? 0 : Marshal.GetLastWin32Error(); // DACL_SECURITY_INFORMATION
    }
}
'@
    }
    $code = [SicmFolderSecurity]::SetDacl($To, $security.GetSecurityDescriptorBinaryForm())
    if ($code -ne 0) { throw ('The permissions of ' + $To + ' could not be set: ' + (New-Object System.ComponentModel.Win32Exception($code)).Message) }
    Write-Log 'INFO' ('Gave ' + $To + ' the permissions of the old top folder (' + $want.Count + ' entries).')
    return $skipped
}

function Invoke-SyncShares {
    param($Plan, [string]$Label)
    $results = @(); $failed = $false
    foreach ($share in @($Plan.shares)) {
        $name = [string]$share.name
        if (Test-CancelRequested) { Write-MigrateEvent @{ event = 'share'; share = $name; status = 'canceled'; message = 'Not run because the copy was stopped.' }; continue }
        if ($failed) { Write-MigrateEvent @{ event = 'share'; share = $name; status = 'skipped'; message = 'Not run because an earlier share failed.' }; continue }
        $item = @{ name = $name; status = 'failed'; exitCode = -1; files = @(); bytes = @(); dirs = @(); log = ''; message = '' }
        try {
            $from = Get-ShareSource -Plan $Plan -Share $share
            $dest = [string]$share.destPath
            # Mirror mode deletes files that are absent from the source. The
            # operator must explicitly allow this for pre-existing folders.
            if ((Test-Path -LiteralPath $dest) -and -not [bool]$share.previouslySynced -and -not [bool]$share.allowOverwrite -and @(Get-ChildItem -LiteralPath $dest -Force -ErrorAction SilentlyContinue | Select-Object -First 1).Count) {
                throw ('The new folder ' + $dest + ' already contains files that did not come from this migration. Choose an empty folder or explicitly allow overwrite.')
            }
            # Recheck the live share at execution time. A plan that was safe
            # during Precheck must not silently replace another share later.
            $existingShare = Get-SmbShare -Name ([string]$share.destName) -ErrorAction SilentlyContinue
            if ($existingShare -and -not [bool]$share.allowOverwrite) {
                if (([string]$existingShare.Path).TrimEnd('\') -ne $dest.TrimEnd('\') -or -not [bool]$share.createdShare) {
                    throw ('The share ' + $share.destName + ' already exists on this server. Check the plan again and explicitly allow overwrite to replace it.')
                }
            }
            if (-not (Test-Path -LiteralPath $dest)) { New-Item -Path $dest -ItemType Directory -Force | Out-Null; Write-Log 'INFO' ('Created the folder ' + $dest + '.') }
            $log = Join-Path $WorkPath ((Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + $Label + '-' + ($name -replace '[^A-Za-z0-9_-]', '_') + '.log')
            $item.log = $log
            Write-MigrateEvent @{ event = 'share'; share = $name; status = 'copying'; message = 'Copying changes.' }
            # /MIR makes the new folder an exact copy, including deletions;
            # /COPYALL keeps data, attributes, timestamps, NTFS permissions,
            # owner and auditing; /B uses backup rights to read every file.
            $options = @('/MIR', '/COPYALL', '/DCOPY:DAT', '/B', '/MT:16', '/NDL') + (Get-CommonRobocopyArguments)
            $code = Invoke-RobocopyRun -From $from -To $dest -Options $options -LogPath $log -ShareName $name -ExpectedFiles ([uint64]$share.expectedFiles)
            $item.exitCode = $code
            $summary = Get-RobocopySummary $log
            $item.files = $summary.files; $item.bytes = $summary.bytes; $item.dirs = $summary.dirs
            if ($code -lt 0) { $item.status = 'canceled'; $item.message = 'Stopped. Run the copy again to continue where it left off.' }
            elseif ($code -ge 8) {
                $failedFiles = 0; if ($summary.files.Count) { $failedFiles = $summary.files[4] }
                $item.message = ('Robocopy could not copy {0} file(s) (exit code {1}). Files that are open or locked are usually copied by the next pass; see the log for each file.' -f $failedFiles, $code)
                $failed = $true
            }
            else {
                foreach ($account in @(Set-RootPermissions -From $from -To $dest)) { Write-Log 'WARN' ($name + ': the top folder permission for ' + $account + ' could not be copied.') }
                $item.status = 'succeeded'
                $copied = 0; $removed = 0; if ($summary.files.Count) { $copied = $summary.files[1]; $removed = $summary.files[5] }
                $item.message = ('{0} file(s) copied, {1} removed that no longer exist on the old server.' -f $copied, $removed)
            }
        }
        catch { $item.message = $_.Exception.Message; $failed = $true }
        Write-Log $(if ($item.status -eq 'succeeded') { 'INFO' } else { 'ERROR' }) ($name + ': ' + $item.message)
        Write-MigrateEvent @{ event = 'share'; share = $name; status = $item.status; message = $item.message; files = $item.files; bytes = $item.bytes }
        $results += $item
    }
    return @{ shares = $results; failed = $failed; canceled = (Test-CancelRequested) }
}

function Invoke-Sync {
    if (-not (Test-Administrator)) { throw 'Moving file shares needs administrator rights. Run the application as administrator.' }
    $plan = Read-Plan
    $result = Invoke-SyncShares -Plan $plan -Label 'Copy'
    Write-Result $result
    if ($result.failed) { $script:ExitCode = 1 }
    elseif ($result.canceled) { $script:ExitCode = 2 }
}

# ------------------------------------------------------------------ cutover ----

# The existing share definition is saved before any intentional replacement.
# This is a local recovery reference, not a backup of its files.
function Get-ShareSettings {
    param($Share)
    $settings = @{}
    if ($Share.description) { $settings['Description'] = [string]$Share.description }
    if ($Share.folderEnumerationMode) { $settings['FolderEnumerationMode'] = [string]$Share.folderEnumerationMode }
    if ($Share.cachingMode) { $settings['CachingMode'] = [string]$Share.cachingMode }
    if ([uint32]$Share.concurrentUserLimit -gt 0) { $settings['ConcurrentUserLimit'] = [uint32]$Share.concurrentUserLimit }
    if ([bool]$Share.encryptData) { $settings['EncryptData'] = $true }
    return $settings
}

function Get-ExistingShareSnapshot {
    param($Existing)
    $rights = @()
    foreach ($entry in @(Get-SmbShareAccess -Name ([string]$Existing.Name) -ErrorAction Stop)) {
        $rights += @{ account = [string]$entry.AccountName; type = [string]$entry.AccessControlType; right = [string]$entry.AccessRight }
    }
    return @{
        destName = [string]$Existing.Name; destPath = [string]$Existing.Path
        description = [string]$Existing.Description
        folderEnumerationMode = [string]$Existing.FolderEnumerationMode
        cachingMode = [string]$Existing.CachingMode
        concurrentUserLimit = [uint32]$Existing.ConcurrentUserLimit
        encryptData = [bool]$Existing.EncryptData
        scopeName = [string]$Existing.ScopeName
        access = $rights
    }
}

function Set-ShareAccessEntries {
    param([string]$Name, $Wanted)
    $notApplied = @()
    foreach ($entry in @(Get-SmbShareAccess -Name $Name -ErrorAction Stop)) {
        if ([string]$entry.AccessControlType -eq 'Deny') { Unblock-SmbShareAccess -Name $Name -AccountName $entry.AccountName -Force -ErrorAction Stop | Out-Null }
        else { Revoke-SmbShareAccess -Name $Name -AccountName $entry.AccountName -Force -ErrorAction Stop | Out-Null }
    }
    foreach ($entry in @($Wanted)) {
        try {
            if ([string]$entry.type -eq 'Deny') { Block-SmbShareAccess -Name $Name -AccountName ([string]$entry.account) -Force -ErrorAction Stop | Out-Null }
            else { Grant-SmbShareAccess -Name $Name -AccountName ([string]$entry.account) -AccessRight ([string]$entry.right) -Force -ErrorAction Stop | Out-Null }
        }
        catch { $notApplied += ([string]$entry.account + ' (' + $entry.right + ')') }
    }
    if (-not @(Get-SmbShareAccess -Name $Name -ErrorAction Stop).Count) {
        Grant-SmbShareAccess -Name $Name -AccountName 'Administrators' -AccessRight Full -Force -ErrorAction Stop | Out-Null
        $notApplied += 'No share permission from the old server applied here, so only Administrators have access.'
    }
    return $notApplied
}

function Set-ShareFromSource {
    param($Share)
    $name = [string]$Share.destName; $path = [string]$Share.destPath
    $overwrite = [bool]$Share.allowOverwrite
    $existing = Get-SmbShare -Name $name -ErrorAction SilentlyContinue
    $differentPath = $existing -and ([string]$existing.Path).TrimEnd('\') -ne $path.TrimEnd('\')
    if ($existing -and -not $overwrite -and ($differentPath -or -not [bool]$Share.createdShare)) {
        throw ('A share named ' + $name + ' already exists for ' + $existing.Path + '. Check the plan again and explicitly allow overwrite.')
    }
    $settings = Get-ShareSettings $Share
    $original = $null; $backupPath = ''
    if ($existing -and $overwrite) {
        if ($existing.ScopeName -and [string]$existing.ScopeName -ne '*') {
            throw ('The existing share ' + $name + ' belongs to a scoped or clustered server name. Replace it manually with the appropriate cluster tools.')
        }
        $original = Get-ExistingShareSnapshot $existing
        $backupPath = Join-Path $WorkPath ('ExistingShare-' + ($name -replace '[^A-Za-z0-9_-]', '_') + '-' + (Get-Date -Format 'yyyyMMdd-HHmmss-fff') + '.json')
        Write-Utf8File -Path $backupPath -Text (ConvertTo-Json -InputObject $original -Depth 8)
        Write-Log 'WARN' ('Saved existing share ' + $name + ' settings and permissions to ' + $backupPath + '. This does not back up its files.')
    }
    if ($differentPath) {
        # No share name can be repointed through Set-SmbShare. A replacement
        # disconnects clients. The original definition is saved first, and is
        # restored if creating the replacement fails.
        $removed = $false
        try {
            Remove-SmbShare -Name $name -Force -ErrorAction Stop
            $removed = $true
            New-SmbShare -Name $name -Path $path -FullAccess 'Administrators' @settings -ErrorAction Stop | Out-Null
            Write-Log 'WARN' ('Replaced existing share ' + $name + ': ' + $original.destPath + ' is no longer shared at this name; new folder is ' + $path + '.')
        }
        catch {
            $reason = $_.Exception.Message
            $restoreMessage = 'The original share was not removed.'
            if ($removed -and $original) {
                try {
                    $oldSettings = Get-ShareSettings $original
                    New-SmbShare -Name $name -Path ([string]$original.destPath) -FullAccess 'Administrators' @oldSettings -ErrorAction Stop | Out-Null
                    $missed = @(Set-ShareAccessEntries -Name $name -Wanted $original.access)
                    if ($missed.Count) { throw ('Permissions not restored: ' + ($missed -join ', ')) }
                    $restoreMessage = 'The original share was restored.'
                }
                catch { $restoreMessage = ('Automatic restore failed: ' + $_.Exception.Message + '. Use the saved configuration at ' + $backupPath + '.') }
            }
            throw ('Could not replace existing share ' + $name + ': ' + $reason + '. ' + $restoreMessage)
        }
    }
    elseif ($existing) {
        if ($settings.Count) { Set-SmbShare -Name $name -Force @settings -ErrorAction Stop }
    }
    else {
        New-SmbShare -Name $name -Path $path -FullAccess 'Administrators' @settings -ErrorAction Stop | Out-Null
        Write-Log 'INFO' ('Created share ' + $name + ' for ' + $path)
    }
    return @(Set-ShareAccessEntries -Name $name -Wanted $Share.access)
}

function Invoke-Cutover {
    if (-not (Test-Administrator)) { throw 'Moving file shares needs administrator rights. Run the application as administrator.' }
    $plan = Read-Plan
    Write-Log 'INFO' 'Final copy before creating the shares.'
    $sync = Invoke-SyncShares -Plan $plan -Label 'Final'
    $result = @{ shares = $sync.shares; failed = $sync.failed; canceled = $sync.canceled; created = @(); verification = @() }
    if ($sync.failed -or $sync.canceled) { Write-Result $result; if ($sync.failed) { $script:ExitCode = 1 } else { $script:ExitCode = 2 }; return }
    foreach ($share in @($plan.shares)) {
        $name = [string]$share.name
        Write-MigrateEvent @{ event = 'share'; share = $name; status = 'sharing'; message = 'Creating the share and its permissions.' }
        try {
            $notApplied = @(Set-ShareFromSource -Share $share)
            $result.created += @{ name = $name; destName = [string]$share.destName; destPath = [string]$share.destPath; notApplied = $notApplied }
            foreach ($text in $notApplied) { Write-Log 'WARN' ($share.destName + ': share permission not applied: ' + $text) }
        }
        catch {
            $result.failed = $true
            Write-Log 'ERROR' ($name + ': ' + $_.Exception.Message)
            Write-MigrateEvent @{ event = 'share'; share = $name; status = 'failed'; message = $_.Exception.Message }
            Write-Result $result
            $script:ExitCode = 1
            return
        }
        # Verify: a listing pass must find nothing left to copy or remove.
        Write-MigrateEvent @{ event = 'share'; share = $name; status = 'verifying'; message = 'Comparing the old and new folders.' }
        $log = Join-Path $WorkPath ((Get-Date -Format 'yyyyMMdd-HHmmss') + '-Verify-' + ($name -replace '[^A-Za-z0-9_-]', '_') + '.log')
        $from = Get-ShareSource -Plan $plan -Share $share
        $code = Invoke-RobocopyRun -From $from -To ([string]$share.destPath) -Options (@('/L', '/MIR', '/NJH') + (Get-CommonRobocopyArguments)) -LogPath $log -ShareName $name -ExpectedFiles 0
        $summary = Get-RobocopySummary $log
        $different = 0; $extra = 0; $total = 0
        if ($summary.files.Count) { $total = $summary.files[0]; $different = $summary.files[1] + $summary.files[3] + $summary.files[4]; $extra = $summary.files[5] }
        $verified = ($code -lt 8 -and $code -ge 0 -and $different -eq 0 -and $extra -eq 0)
        # The share must answer through its new network path.
        $sharePath = '\\' + $script:ComputerName + '\' + [string]$share.destName
        $reachable = $false
        try { $reachable = Test-Path -LiteralPath $sharePath } catch { }
        $message = ('{0} file(s) match. {1} is ready.' -f $total, $sharePath)
        if (-not $verified) { $message = ('{0} file(s) differ and {1} extra file(s) exist, usually because users changed files during cutover. Run the final copy again.' -f $different, $extra) }
        if (-not $reachable) { $verified = $false; $message = ($sharePath + ' did not open from this server. Check that the Server service is running and File and Printer Sharing is allowed. ' + $message) }
        else { Write-Log 'INFO' ($sharePath + ' opens.') }
        $result.verification += @{ name = $name; verified = $verified; files = $total; different = $different; extra = $extra; log = $log; sharePath = $sharePath; reachable = $reachable }
        Write-MigrateEvent @{ event = 'share'; share = $name; status = $(if ($verified) { 'done' } else { 'attention' }); message = $message }
    }
    Write-Result $result
}

# ------------------------------------------------------------ old server ------

function Invoke-SourceReadOnly {
    if (-not (Test-Administrator)) { throw 'This needs administrator rights.' }
    $plan = Read-Plan
    $session = New-SourceCimSession $plan.source
    $saved = @{ source = $plan.source; shares = @() }
    try {
        foreach ($share in @($plan.shares)) {
            $entries = @(Get-ShareAccessEntries -ShareName ([string]$share.name) -Session $session)
            $saved.shares += @{ name = [string]$share.name; access = $entries }
        }
        $savedPath = Join-Path $WorkPath 'Old-Server-Share-Permissions.json'
        if (Test-Path -LiteralPath $savedPath) { throw 'The old shares are already read-only. Restore their permissions first.' }
        Write-Utf8File -Path $savedPath -Text (ConvertTo-Json -InputObject $saved -Depth 6)
        foreach ($share in $saved.shares) {
            foreach ($entry in $share.access) {
                if ($entry.type -eq 'Allow' -and ($entry.right -eq 'Full' -or $entry.right -eq 'Change')) {
                    $arguments = @{ Name = $share.name; AccountName = $entry.account; AccessRight = 'Read'; Force = $true }
                    if ($session) { $arguments['CimSession'] = $session }
                    Grant-SmbShareAccess @arguments | Out-Null
                }
            }
            Write-Log 'INFO' ('Made ' + $share.name + ' on ' + $plan.source + ' read-only.')
            Write-MigrateEvent @{ event = 'share'; share = $share.name; status = 'readonly'; message = 'The old share is now read-only.' }
        }
    }
    finally { if ($session) { Remove-CimSession -CimSession $session -ErrorAction SilentlyContinue } }
    Write-Result @{ savedPath = $savedPath }
}

function Invoke-SourceRestore {
    if (-not (Test-Administrator)) { throw 'This needs administrator rights.' }
    $savedPath = Join-Path $WorkPath 'Old-Server-Share-Permissions.json'
    if (-not (Test-Path -LiteralPath $savedPath)) { throw 'No saved permissions were found for the old shares.' }
    $saved = Get-Content -LiteralPath $savedPath -Raw | ConvertFrom-Json
    $session = New-SourceCimSession (Get-SourceName ([string]$saved.source))
    try {
        foreach ($share in @($saved.shares)) {
            foreach ($entry in @($share.access)) {
                if ($entry.type -eq 'Allow') {
                    $arguments = @{ Name = $share.name; AccountName = $entry.account; AccessRight = $entry.right; Force = $true }
                    if ($session) { $arguments['CimSession'] = $session }
                    Grant-SmbShareAccess @arguments | Out-Null
                }
            }
            Write-MigrateEvent @{ event = 'share'; share = $share.name; status = 'restored'; message = 'The old share''s permissions are back.' }
        }
    }
    finally { if ($session) { Remove-CimSession -CimSession $session -ErrorAction SilentlyContinue } }
    Move-Item -LiteralPath $savedPath -Destination ($savedPath + '.restored-' + (Get-Date -Format 'yyyyMMdd-HHmmss')) -Force
    Write-Result @{ restored = $true }
}

# ------------------------------------------------------------ DFS and alias ---

function Invoke-DfsReport {
    $plan = Read-Plan
    $result = @{ available = $false; message = ''; targets = @() }
    $domain = ''
    try { $domain = [System.DirectoryServices.ActiveDirectory.Domain]::GetComputerDomain().Name } catch { }
    if (-not $domain) { $result.message = 'This server is not in a domain, so there are no domain DFS namespaces to check.'; Write-Result $result; return }
    if (-not (Get-Command Get-DfsnRoot -ErrorAction SilentlyContinue)) {
        try { Import-Module DFSN -ErrorAction Stop } catch { $result.message = 'The DFS Management tools are not installed. Install them with: Install-WindowsFeature RSAT-DFS-Mgmt-Con'; Write-Result $result; return }
    }
    $result.available = $true
    $names = @(([string]$plan.source).ToLowerInvariant(), ([string]$plan.sourceFqdn).ToLowerInvariant()) | Where-Object { $_ } | Sort-Object -Unique
    $map = @{}
    foreach ($share in @($plan.shares)) { $map[([string]$share.name).ToLowerInvariant()] = ('\\' + $script:ComputerName + '\' + $share.destName) }
    foreach ($root in @(Get-DfsnRoot -Domain $domain -ErrorAction SilentlyContinue)) {
        $paths = @($root.Path)
        try { $paths += @(Get-DfsnFolder -Path ($root.Path + '\*') -ErrorAction Stop | ForEach-Object { $_.Path }) } catch { }
        foreach ($path in $paths) {
            $targets = @()
            try { $targets = @(Get-DfsnFolderTarget -Path $path -ErrorAction Stop) } catch { try { $targets = @(Get-DfsnRootTarget -Path $path -ErrorAction Stop) } catch { } }
            foreach ($target in $targets) {
                $targetPath = [string]$target.TargetPath
                if ($targetPath -match '^\\\\([^\\]+)\\([^\\]+)') {
                    if ($names -contains $Matches[1].ToLowerInvariant()) {
                        $result.targets += @{ namespacePath = [string]$path; target = $targetPath; suggested = [string]$map[$Matches[2].ToLowerInvariant()] }
                    }
                }
            }
        }
    }
    $result.message = ('{0} DFS link(s) point at the old server.' -f $result.targets.Count)
    Write-Result $result
}

function Get-AliasStatus {
    param($Plan)
    $status = @{ possible = $false; reasons = @(); oldName = ''; oldFqdn = [string]$Plan.sourceFqdn; thisFqdn = '' }
    $status.oldName = ([string]$Plan.source).Split('.')[0]
    try { $status.thisFqdn = [System.Net.Dns]::GetHostEntry($script:ComputerName).HostName } catch { $status.thisFqdn = $script:ComputerName }
    if (Test-IsThisServer $Plan.source) { $status.reasons += 'The old server is this server.' }
    $domainRole = -1
    if ($TargetName) {
        # Run from the admin computer: ask the new server (read only).
        try { $domainRole = [int](Get-CimInstance -ClassName Win32_ComputerSystem -ComputerName $TargetName -ErrorAction Stop).DomainRole }
        catch { $status.reasons += ('Could not read whether ' + $script:ComputerName + ' is in a domain with this Windows account: ' + $_.Exception.Message) }
    }
    else { $domainRole = [int](Get-WmiObject -Class Win32_ComputerSystem).DomainRole }
    if ($domainRole -eq 0 -or $domainRole -eq 2) { $status.reasons += ('The new server ' + $script:ComputerName + ' is not in a domain. The alias needs Active Directory and DNS.') }
    if (-not (Get-Command netdom.exe -ErrorAction SilentlyContinue)) { $status.reasons += ('netdom.exe is not installed on ' + [string]$env:COMPUTERNAME + '. Install it with: Install-WindowsFeature RSAT-AD-Tools') }
    $answers = $false
    try { $answers = Test-Connection -ComputerName $status.oldName -Count 1 -Quiet -ErrorAction SilentlyContinue } catch { }
    if (-not $answers) { try { $answers = Test-Path -LiteralPath ('\\' + $status.oldName + '\IPC$') } catch { } }
    if ($answers) { $status.reasons += ('The old server ' + $status.oldName + ' still answers on the network. Shut it down or rename it, and remove it from DNS, before giving its name to this server.') }
    $status.possible = ($status.reasons.Count -eq 0)
    return $status
}

function Invoke-AliasCheck { Write-Result (Get-AliasStatus (Read-Plan)) }

function Invoke-Alias {
    if (-not (Test-Administrator)) { throw 'This needs administrator rights.' }
    $plan = Read-Plan
    $status = Get-AliasStatus $plan
    if (-not $status.possible) { throw ($status.reasons -join ' ') }
    $fqdn = $status.oldFqdn
    if (-not $fqdn -or $fqdn -notmatch '\.') {
        $domain = [System.DirectoryServices.ActiveDirectory.Domain]::GetComputerDomain().Name
        $fqdn = $status.oldName + '.' + $domain
    }
    # netdom adds the old name as an alternate computer name: it registers the
    # name in DNS and its service principal names in Active Directory, and SMB
    # answers to it. Undo with: netdom computername <this server> /remove:<old name>
    $output = & netdom.exe computername $status.thisFqdn ('/add:' + $fqdn) 2>&1
    foreach ($line in @($output)) { if ([string]$line) { Write-Log 'INFO' ('  ' + [string]$line) } }
    if ($LASTEXITCODE -ne 0) { throw ('netdom could not add the alternate name (exit code ' + $LASTEXITCODE + '). Adding a computer name needs Domain Admin rights.') }
    Write-Utf8File -Path (Join-Path $WorkPath 'Alias-UNDO.txt') -Text ("netdom computername " + $status.thisFqdn + " /remove:" + $fqdn + "`r`n")
    Write-Result @{ added = $fqdn; restartRequired = $true }
}

# ------------------------------------------------------------------ main -------

function Enter-EngineLock {
    # One copy at a time per migration: a copy left running (for example
    # when the console lost its connection to the new server) must finish
    # or be stopped before another starts on the same folders.
    if (-not $WorkPath -or @('Precheck', 'Sync', 'Cutover') -notcontains $Mode) { return }
    try { $script:EngineLock = New-Object System.IO.FileStream((Join-Path $WorkPath 'Engine.lock'), [System.IO.FileMode]::OpenOrCreate, [System.IO.FileAccess]::ReadWrite, [System.IO.FileShare]::None) }
    catch { throw ('A previous copy for this migration is still running on ' + $script:ComputerName + '. Wait for it to finish (it stops by itself when it is done), then run this step again.') }
}

try {
    if ($WorkPath -and -not (Test-Path -LiteralPath $WorkPath)) { New-Item -Path $WorkPath -ItemType Directory -Force | Out-Null }
    Enter-EngineLock
    Read-SourceCredential
    switch ($Mode) {
        'Discover' { Invoke-Discover }
        'Precheck' { Connect-SourceFiles (Read-Plan).source; Invoke-Precheck }
        'Sync' { Connect-SourceFiles (Read-Plan).source; Invoke-Sync }
        'Cutover' { Connect-SourceFiles (Read-Plan).source; Invoke-Cutover }
        'SourceReadOnly' { Invoke-SourceReadOnly }
        'SourceRestore' { Invoke-SourceRestore }
        'DfsReport' { Invoke-DfsReport }
        'AliasCheck' { Invoke-AliasCheck }
        'Alias' { Invoke-Alias }
    }
}
catch {
    Write-Log 'ERROR' $_.Exception.Message
    Write-MigrateEvent @{ event = 'error'; message = $_.Exception.Message }
    $script:ExitCode = 3
}
finally {
    Disconnect-SourceFiles
    if ($script:EngineLock) { $script:EngineLock.Dispose(); $script:EngineLock = $null }
    if ($script:LineWriter) { $script:LineWriter.Dispose(); $script:LineWriter = $null }
}
if ($Embedded) { return $script:ExitCode }
exit $script:ExitCode
