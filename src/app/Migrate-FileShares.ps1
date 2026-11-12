[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidateSet('Discover', 'Precheck', 'Sync', 'Cutover', 'SourceReadOnly', 'SourceRestore', 'DfsReport', 'AliasCheck', 'Alias')][string]$Mode,
    [string]$Source,
    [string]$PlanPath,
    [string]$ResultPath,
    [string]$WorkPath,
    [string]$CancelPath
)

# Moves file shares from an old server to this server. Runs on the new server.
# Discover lists the old server's shares. Precheck checks space, paths and
# accounts. Sync copies with Robocopy and can be repeated; each pass copies only
# what changed. Cutover runs a final sync, creates the shares here with the old
# share settings and permissions, and verifies the copy. The old server is
# changed only by the separate SourceReadOnly step, which saves the previous
# share permissions first so SourceRestore can put them back.
# Compatible with Windows PowerShell 3.0 (Windows Server 2012) and later.

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }

function Write-Line { param([string]$Text) [Console]::Out.WriteLine($Text); [Console]::Out.Flush() }
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

function New-SourceCimSession {
    param([string]$Name)
    if (Test-IsThisServer $Name) { return $null }
    $option = New-CimSessionOption -Protocol Dcom
    return New-CimSession -ComputerName $Name -SessionOption $option -ErrorAction Stop
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
    try { $session = New-SourceCimSession $name } catch { throw ('Could not connect to ' + $name + ' for share details: ' + $_.Exception.Message + ' Check that the server is on, that you are an administrator there, and that Windows Firewall allows remote management (WMI).') }
    try {
        $wmi = @{ Class = 'Win32_Share'; ErrorAction = 'Stop' }
        if (-not $result.sameServer) { $wmi['ComputerName'] = $name }
        $raw = @(Get-WmiObject @wmi)
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
        if ($from -and -not (Test-Path -LiteralPath $from)) { $item.problems += ('The old folder ' + $from + ' cannot be reached. Check that you are an administrator on the old server and its administrative shares are enabled.') }
        $dest = [string]$share.destPath
        $drive = $dest.Substring(0, 2).ToUpperInvariant()
        if (-not (Test-Path -LiteralPath ($drive + '\'))) { $item.problems += ('Drive ' + $drive + ' does not exist on this server.') }
        $known = [bool]$share.previouslySynced
        if ((Test-Path -LiteralPath $dest) -and -not $known) {
            if (@(Get-ChildItem -LiteralPath $dest -Force -ErrorAction SilentlyContinue | Select-Object -First 1).Count) { $item.problems += ('The new folder ' + $dest + ' already contains files. Choose an empty or new folder; the copy makes it an exact mirror of the old share.') }
        }
        if ($from -and ($dest.TrimEnd('\') -eq $from.TrimEnd('\'))) { $item.problems += 'The new folder is the old folder.' }
        $existing = $localShares[([string]$share.destName).ToUpperInvariant()]
        if ($existing -and $existing.TrimEnd('\') -ne $dest.TrimEnd('\')) { $item.problems += ('A share named ' + $share.destName + ' already exists on this server for ' + $existing + '.') }
        elseif ($existing -and -not [bool]$share.createdShare) { $item.warnings += ('A share named ' + $share.destName + ' already exists for this folder; its permissions will be replaced at cutover.') }
        if ($from -and -not $item.problems.Count) {
            $log = Join-Path $WorkPath ('Estimate-' + ($share.name -replace '[^A-Za-z0-9_-]', '_') + '.log')
            $code = Invoke-RobocopyRun -From $from -To $dest -Options (@('/L', '/MIR', '/NFL', '/NDL', '/NJH') + (Get-CommonRobocopyArguments)) -LogPath $log -ShareName ([string]$share.name) -ExpectedFiles 0
            if ($code -lt 0) { break }
            $summary = Get-RobocopySummary $log
            if ($summary.files.Count) { $item.files = $summary.files[0]; $item.bytes = $summary.bytes[0]; $item.toCopyBytes = $summary.bytes[1] } else { $item.warnings += 'The size of the share could not be measured.' }
            if ($code -ge 8) { $item.warnings += ('Robocopy could not read part of the share while measuring it (exit code ' + $code + '). See the estimate log.') }
            if (-not $needed.ContainsKey($drive)) { $needed[$drive] = [uint64]0 }
            $needed[$drive] += [uint64]$item.bytes
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
            # The copy mirrors the old share, deleting anything else in the
            # new folder, so it never runs into a folder it did not create.
            if ((Test-Path -LiteralPath $dest) -and -not [bool]$share.previouslySynced -and @(Get-ChildItem -LiteralPath $dest -Force -ErrorAction SilentlyContinue | Select-Object -First 1).Count) {
                throw ('The new folder ' + $dest + ' already contains files that did not come from this migration. Choose an empty or new folder.')
            }
            if (-not (Test-Path -LiteralPath $dest)) { New-Item -Path $dest -ItemType Directory -Force | Out-Null }
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
    if ($result.failed) { exit 1 }
    if ($result.canceled) { exit 2 }
}

# ------------------------------------------------------------------ cutover ----

function Set-ShareFromSource {
    param($Share)
    $name = [string]$Share.destName; $path = [string]$Share.destPath
    $existing = Get-SmbShare -Name $name -ErrorAction SilentlyContinue
    if ($existing -and ([string]$existing.Path).TrimEnd('\') -ne $path.TrimEnd('\')) { throw ('A share named ' + $name + ' already exists for ' + $existing.Path + '.') }
    $settings = @{}
    if ($Share.description) { $settings['Description'] = [string]$Share.description }
    if ($Share.folderEnumerationMode) { $settings['FolderEnumerationMode'] = [string]$Share.folderEnumerationMode }
    if ($Share.cachingMode) { $settings['CachingMode'] = [string]$Share.cachingMode }
    if ([uint32]$Share.concurrentUserLimit -gt 0) { $settings['ConcurrentUserLimit'] = [uint32]$Share.concurrentUserLimit }
    if ([bool]$Share.encryptData) { $settings['EncryptData'] = $true }
    if ($existing) {
        if ($settings.Count) { Set-SmbShare -Name $name -Force @settings }
    }
    else {
        New-SmbShare -Name $name -Path $path -ReadAccess 'Everyone' @settings | Out-Null
        Write-Log 'INFO' ('Created share ' + $name + ' for ' + $path)
    }
    # Replace share permissions with the old share's.
    $wanted = @($Share.access)
    $notApplied = @()
    foreach ($entry in @(Get-SmbShareAccess -Name $name)) {
        if ([string]$entry.AccessControlType -eq 'Deny') { Unblock-SmbShareAccess -Name $name -AccountName $entry.AccountName -Force | Out-Null }
        else { Revoke-SmbShareAccess -Name $name -AccountName $entry.AccountName -Force | Out-Null }
    }
    foreach ($entry in $wanted) {
        try {
            if ([string]$entry.type -eq 'Deny') { Block-SmbShareAccess -Name $name -AccountName ([string]$entry.account) -Force | Out-Null }
            else { Grant-SmbShareAccess -Name $name -AccountName ([string]$entry.account) -AccessRight ([string]$entry.right) -Force | Out-Null }
        }
        catch { $notApplied += ([string]$entry.account + ' (' + $entry.right + ')') }
    }
    if (-not @(Get-SmbShareAccess -Name $name).Count) {
        Grant-SmbShareAccess -Name $name -AccountName 'Administrators' -AccessRight Full -Force | Out-Null
        $notApplied += 'No share permission from the old server applied here, so only Administrators have access.'
    }
    return $notApplied
}

function Invoke-Cutover {
    if (-not (Test-Administrator)) { throw 'Moving file shares needs administrator rights. Run the application as administrator.' }
    $plan = Read-Plan
    Write-Log 'INFO' 'Final copy before creating the shares.'
    $sync = Invoke-SyncShares -Plan $plan -Label 'Final'
    $result = @{ shares = $sync.shares; failed = $sync.failed; canceled = $sync.canceled; created = @(); verification = @() }
    if ($sync.failed -or $sync.canceled) { Write-Result $result; if ($sync.failed) { exit 1 } else { exit 2 } }
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
            exit 1
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
        $message = ('{0} file(s) match.' -f $total)
        if (-not $verified) { $message = ('{0} file(s) differ and {1} extra file(s) exist, usually because users changed files during cutover. Run the final copy again.' -f $different, $extra) }
        $result.verification += @{ name = $name; verified = $verified; files = $total; different = $different; extra = $extra; log = $log }
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
    $domainRole = [int](Get-WmiObject -Class Win32_ComputerSystem).DomainRole
    if ($domainRole -eq 0 -or $domainRole -eq 2) { $status.reasons += 'This server is not in a domain. The alias needs Active Directory and DNS.' }
    if (-not (Get-Command netdom.exe -ErrorAction SilentlyContinue)) { $status.reasons += 'netdom.exe is not installed. Install it with: Install-WindowsFeature RSAT-AD-Tools' }
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

try {
    if ($WorkPath -and -not (Test-Path -LiteralPath $WorkPath)) { New-Item -Path $WorkPath -ItemType Directory -Force | Out-Null }
    switch ($Mode) {
        'Discover' { Invoke-Discover }
        'Precheck' { Invoke-Precheck }
        'Sync' { Invoke-Sync }
        'Cutover' { Invoke-Cutover }
        'SourceReadOnly' { Invoke-SourceReadOnly }
        'SourceRestore' { Invoke-SourceRestore }
        'DfsReport' { Invoke-DfsReport }
        'AliasCheck' { Invoke-AliasCheck }
        'Alias' { Invoke-Alias }
    }
    exit 0
}
catch {
    Write-Log 'ERROR' $_.Exception.Message
    Write-MigrateEvent @{ event = 'error'; message = $_.Exception.Message }
    exit 3
}
