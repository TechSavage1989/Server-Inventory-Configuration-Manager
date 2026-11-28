param(
    [Parameter(Mandatory = $true)][string]$Target,
    [Parameter(Mandatory = $true)][ValidateSet('Discover', 'Read', 'Prepare', 'Scan', 'Restore', 'StopSites', 'StartSites', 'Clean')][string]$Mode,
    [Parameter(Mandatory = $true)][string]$ResultPath,
    [Parameter(Mandatory = $true)][string]$WorkName,
    [string]$EnginePath = '',
    [string]$PackageName = '',
    [string]$PlanPath = '',
    [string]$CancelPath = '',
    [switch]$IncludeWebContent,
    [ValidateRange(1, 2048)][int]$WebContentLimitGB = 10,
    [switch]$InputFromStdin
)

# Moves a server's roles (IIS sites with their files, certificates, local
# accounts, file shares and the other items a migration bundle carries) from
# an old server to a new one, live, run from an admin computer (2026-11-28,
# owner request; AGENTS rule 75).
# Discover lists what the old server has (the bundle engine with -ListOnly:
# nothing is copied or exported) so the operator picks what to move first;
# Read then carries only the picked items (rule 76).
# Read runs the migration bundle engine (Export-MigrationBundle.ps1) inside
# the OLD server and builds a package there with the IIS configuration
# backup; the application then copies that package to the new server with
# Robocopy; Scan and Restore run the restore engine
# (Restore-ServerConfiguration.ps1) inside the NEW server. StopSites and
# StartSites stop or start named IIS sites on the old server (cutover and
# rollback); Clean removes this move's package folder on one server.
# Only Invoke-Command over WinRM is used: no program is copied to either
# server, no service, scheduled task or WMI process start is created, and
# neither credential delegation nor an encoded command is used. The engines
# travel as script block text, so no script file is written there either.
# Each server keeps data only under ProgramData\Server Inventory and
# Migration Manager\Server-Move (protected to SYSTEM and Administrators):
# the package (secrets inside it stay encrypted with a passphrase that exists
# only in the application's memory), restore rollback folders, and a per-run
# folder removed when the run ends.
# The account and secrets arrive as one JSON line on standard input
# {"user","password","passphrase","passwords"}, never on a command line. An
# empty account uses this Windows account.
# Lines the engines write are followed from a second session and repeated
# here, so progress shows live; a stop request (-CancelPath on this computer)
# is passed on. Compatible with Windows PowerShell 3.0 and later.

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }

function Write-Line { param([string]$Text) [Console]::Out.WriteLine($Text); [Console]::Out.Flush() }
function Write-Log { param([string]$Level, [string]$Message) Write-Line ('{0} [{1}] {2}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Level, $Message) }
function New-Credential {
    param([string]$User, [string]$Password)
    if (-not $User) { return $null }
    $secure = New-Object System.Security.SecureString
    foreach ($character in $Password.ToCharArray()) { $secure.AppendChar($character) }
    $secure.MakeReadOnly()
    return New-Object System.Management.Automation.PSCredential($User, $secure)
}

if ($Target -notmatch '^[A-Za-z0-9][A-Za-z0-9.\-]{0,252}$') { Write-Log 'ERROR' 'The server name is not valid.'; exit 3 }
if ($WorkName -notmatch '^[A-Za-z0-9._\-]{1,120}$') { Write-Log 'ERROR' 'The move folder name is not valid.'; exit 3 }
if ($PackageName -and $PackageName -notmatch '^[A-Za-z0-9.\-]{1,63}_ServerInventory_\d{8}_\d{6}$') { Write-Log 'ERROR' 'The package name is not valid.'; exit 3 }

# Runs on the server: makes the move's folder (protected to SYSTEM and
# Administrators) and this run's folder.
$prepare = {
    param([string]$Name, [string]$RunId)
    $ErrorActionPreference = 'Stop'
    $base = Join-Path $env:ProgramData 'Server Inventory and Migration Manager\Server-Move'
    if (-not (Test-Path -LiteralPath $base)) {
        $security = New-Object System.Security.AccessControl.DirectorySecurity
        $security.SetAccessRuleProtection($true, $false)
        foreach ($sid in @('S-1-5-18', 'S-1-5-32-544')) {
            $identity = New-Object System.Security.Principal.SecurityIdentifier($sid)
            $security.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($identity, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')))
        }
        $parent = Split-Path -Path $base -Parent
        if (-not (Test-Path -LiteralPath $parent)) { New-Item -ItemType Directory -Path $parent -Force | Out-Null }
        [void][System.IO.Directory]::CreateDirectory($base, $security)
    }
    $work = Join-Path $base $Name
    $run = Join-Path $work ('Runs\' + $RunId)
    New-Item -ItemType Directory -Path $run -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $work 'Package') -Force | Out-Null
    $iis = Test-Path -LiteralPath (Join-Path $env:SystemRoot 'System32\inetsrv\appcmd.exe')
    return @{ work = $work; run = $run; package = (Join-Path $work 'Package'); computer = [string]$env:COMPUTERNAME; iis = $iis; admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator) }
}

# Runs inside the old server: a package folder named like a collection
# (OLD_ServerInventory_<date>_<time>) with the IIS configuration backup and
# a manifest, then the migration bundle engine as a script block. The IIS
# backup is made by appcmd under this run's own name and deleted again, the
# way the collector does it; nothing else on the server changes.
$read = {
    param([string]$EngineText, [string]$Work, [string]$Run, [string]$Name, [string]$Passphrase, [bool]$Include, [int]$Limit, [string]$SelectionText)
    $ErrorActionPreference = 'Stop'
    $log = Join-Path $Run 'Engine.log'
    $encoding = New-Object System.Text.UTF8Encoding($false)
    $say = { param([string]$Level, [string]$Text) [IO.File]::AppendAllText($log, ('{0} [{1}] {2}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Level, $Text) + "`r`n", $encoding) }
    $root = Join-Path $Work 'Package'
    $package = Join-Path $root $Name
    # Only this move's own earlier package is replaced; the next copy to the
    # new server sends only what changed (Robocopy compares sizes and times).
    foreach ($old in @(Get-ChildItem -LiteralPath $root -Directory -ErrorAction SilentlyContinue)) {
        if ($old.Name -match '_ServerInventory_\d{8}_\d{6}$') { Remove-Item -LiteralPath $old.FullName -Recurse -Force }
    }
    New-Item -ItemType Directory -Path (Join-Path $package '00-Summary') -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $package '00-Summary\Live-Move.txt'), ('Package read live from {0} at {1} by Server Inventory and Migration Manager for a move to another server.' -f $env:COMPUTERNAME, (Get-Date -Format 'yyyy-MM-dd HH:mm:ss')), $encoding)
    $appCmd = Join-Path $env:SystemRoot 'System32\inetsrv\appcmd.exe'
    $wantIis = $true
    if ($SelectionText) { $wantIis = [bool](ConvertFrom-Json -InputObject $SelectionText).iis }
    if (-not $wantIis) { & $say 'INFO' 'IIS was not picked, so its configuration is not read.' }
    elseif (Test-Path -LiteralPath $appCmd) {
        & $say 'INFO' 'Saving the IIS configuration.'
        $backupName = 'SIMM_ServerMove_' + $Run.Substring($Run.Length - 12)
        $previous = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        try {
            $null = & $appCmd add backup $backupName 2>&1
            $code = $LASTEXITCODE
            $source = Join-Path (Join-Path $env:SystemRoot 'System32\inetsrv\backup') $backupName
            if ($code -eq 0 -and (Test-Path -LiteralPath $source)) {
                New-Item -ItemType Directory -Path (Join-Path $package '08-Role-Specific\IIS') -Force | Out-Null
                Copy-Item -LiteralPath $source -Destination (Join-Path $package '08-Role-Specific\IIS\IIS-Configuration-Backup') -Recurse -Force
            }
            else { & $say 'WARN' ('appcmd could not save the IIS configuration (exit code ' + $code + ').') }
            if (Test-Path -LiteralPath $source) { $null = & $appCmd delete backup $backupName 2>&1 }
        }
        finally { $ErrorActionPreference = $previous }
    }
    else { & $say 'INFO' 'IIS is not installed on this server; the other roles are read.' }
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        $rows = foreach ($file in @(Get-ChildItem -LiteralPath $package -Recurse -File -Force)) {
            $stream = [IO.File]::OpenRead($file.FullName)
            try { $hash = ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '') } finally { $stream.Dispose() }
            [pscustomobject]@{ RelativePath = $file.FullName.Substring($package.Length + 1); LengthBytes = $file.Length; SHA256 = $hash }
        }
    }
    finally { $sha.Dispose() }
    @($rows) | Export-Csv -LiteralPath (Join-Path $package 'SHA256-Manifest.csv') -NoTypeInformation -Encoding UTF8
    $resultFile = Join-Path $Run 'Result.json'
    $engine = [scriptblock]::Create($EngineText)
    $arguments = @{ PackagePath = $package; ResultPath = $resultFile; CancelPath = (Join-Path $Run 'Stop-Requested'); LogPath = $log; Embedded = $true; Passphrase = $Passphrase; WebContentLimitGB = $Limit }
    if ($Include) { $arguments['IncludeWebContent'] = $true }
    if ($SelectionText) { $arguments['SelectionJson'] = $SelectionText }
    $Passphrase = $null
    $output = @(& $engine @arguments)
    $arguments = $null
    $code = 3
    if ($output.Count -and $output[$output.Count - 1] -is [int]) { $code = [int]$output[$output.Count - 1] }
    $result = ''
    if (Test-Path -LiteralPath $resultFile) { $result = [IO.File]::ReadAllText($resultFile) }
    $size = [long]0
    foreach ($file in @(Get-ChildItem -LiteralPath $package -Recurse -File -Force -ErrorAction SilentlyContinue)) { $size += $file.Length }
    return @{ exitCode = $code; result = $result; package = $package; sizeBytes = $size }
}

# Runs inside the old server: the bundle engine lists what the server has.
# Nothing is copied, exported or changed.
$discover = {
    param([string]$EngineText, [string]$Run)
    $ErrorActionPreference = 'Stop'
    $resultFile = Join-Path $Run 'Result.json'
    $engine = [scriptblock]::Create($EngineText)
    $output = @(& $engine -ListOnly -ResultPath $resultFile -LogPath (Join-Path $Run 'Engine.log') -Embedded)
    $code = 3
    if ($output.Count -and $output[$output.Count - 1] -is [int]) { $code = [int]$output[$output.Count - 1] }
    $result = ''
    if (Test-Path -LiteralPath $resultFile) { $result = [IO.File]::ReadAllText($resultFile) }
    return @{ exitCode = $code; result = $result }
}

# Runs inside the new server: the restore engine as a script block.
$restore = {
    param([string]$EngineText, [string]$EngineMode, [string]$Work, [string]$Run, [string]$Name, [string]$SecretsJson, [string]$PlanText)
    $ErrorActionPreference = 'Stop'
    $encoding = New-Object System.Text.UTF8Encoding($false)
    $package = Join-Path (Join-Path $Work 'Package') $Name
    if (-not (Test-Path -LiteralPath $package -PathType Container)) { return @{ exitCode = 3; result = ''; error = ('The package was not found on ' + $env:COMPUTERNAME + '. Read the old server again.') } }
    $planFile = Join-Path $Run 'Plan.json'
    [IO.File]::WriteAllText($planFile, $PlanText, $encoding)
    $resultFile = Join-Path $Run 'Result.json'
    $rollback = ''
    $arguments = @{ Mode = $EngineMode; PackagePath = $package; PlanPath = $planFile; LogPath = (Join-Path $Run 'Engine.log'); Embedded = $true; SecretsJson = $SecretsJson }
    if ($EngineMode -eq 'Scan') { $arguments['ResultPath'] = $resultFile }
    else {
        $rollback = Join-Path $Work ('Rollback\' + (Get-Date -Format 'yyyyMMdd_HHmmss'))
        New-Item -ItemType Directory -Path $rollback -Force | Out-Null
        $arguments['RollbackPath'] = $rollback
        $arguments['CancelPath'] = (Join-Path $Run 'Stop-Requested')
    }
    $SecretsJson = $null
    $engine = [scriptblock]::Create($EngineText)
    $output = @(& $engine @arguments)
    $arguments = $null
    Remove-Item -LiteralPath $planFile -Force -ErrorAction SilentlyContinue
    $code = 3
    if ($output.Count -and $output[$output.Count - 1] -is [int]) { $code = [int]$output[$output.Count - 1] }
    $result = ''
    if (Test-Path -LiteralPath $resultFile) { $result = [IO.File]::ReadAllText($resultFile) }
    return @{ exitCode = $code; result = $result; rollback = $rollback }
}

# Runs inside the old server: stops (or starts) the named IIS sites and
# returns the names it changed, so starting them again restores exactly the
# sites that were running.
$sites = {
    param([string[]]$Names, [string]$Action)
    $ErrorActionPreference = 'Stop'
    $appCmd = Join-Path $env:SystemRoot 'System32\inetsrv\appcmd.exe'
    if (-not (Test-Path -LiteralPath $appCmd)) { throw 'IIS is not installed on this server.' }
    $changed = @()
    $failed = @()
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        foreach ($name in $Names) {
            $state = [string](& $appCmd list site ('/site.name:' + $name) '/text:state' 2>&1)
            if ($LASTEXITCODE -ne 0) { $failed += ($name + ': not found'); continue }
            $wanted = 'Stopped'
            if ($Action -eq 'start') { $wanted = 'Started' }
            if ($state.Trim() -eq $wanted) { continue }
            $out = [string](& $appCmd $Action site ('/site.name:' + $name) 2>&1)
            if ($LASTEXITCODE -eq 0) { $changed += $name } else { $failed += ($name + ': ' + $out.Trim()) }
        }
    }
    finally { $ErrorActionPreference = $previous }
    return @{ changed = $changed; failed = $failed }
}

$clean = {
    param([string]$Work)
    $package = Join-Path $Work 'Package'
    if (Test-Path -LiteralPath $package) { Remove-Item -LiteralPath $package -Recurse -Force }
    return @{ removed = $true }
}

# Runs on the server: returns the engine's new complete lines from Offset,
# and passes on a stop request.
$poll = {
    param([string]$Run, [long]$Offset, [bool]$Stop)
    if ($Stop) { [IO.File]::WriteAllText((Join-Path $Run 'Stop-Requested'), 'stop') }
    $log = Join-Path $Run 'Engine.log'
    $text = ''
    $next = $Offset
    if (Test-Path -LiteralPath $log) {
        $stream = New-Object System.IO.FileStream($log, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
        try {
            if ($stream.Length -gt $Offset) {
                [void]$stream.Seek($Offset, [System.IO.SeekOrigin]::Begin)
                $count = [int][Math]::Min([long]4194304, $stream.Length - $Offset)
                $buffer = New-Object byte[] $count
                $got = $stream.Read($buffer, 0, $count)
                $end = -1
                for ($i = $got - 1; $i -ge 0; $i--) { if ($buffer[$i] -eq 10) { $end = $i; break } }
                if ($end -ge 0) {
                    $text = (New-Object System.Text.UTF8Encoding($false)).GetString($buffer, 0, $end + 1)
                    $next = $Offset + $end + 1
                }
            }
        }
        finally { $stream.Dispose() }
    }
    return @{ offset = $next; text = $text }
}

$cleanup = {
    param([string]$Run)
    Remove-Item -LiteralPath $Run -Recurse -Force -ErrorAction SilentlyContinue
}

function Write-Result { param($Value) [IO.File]::WriteAllText($ResultPath, (ConvertTo-Json -InputObject $Value -Depth 6 -Compress), (New-Object System.Text.UTF8Encoding($false))) }

$sessions = @()
$watch = $null
$info = $null
try {
    $inputs = @{ user = ''; password = ''; passphrase = ''; passwords = $null }
    if ($InputFromStdin) {
        $line = [Console]::In.ReadLine()
        if ($line) {
            $parsed = $line | ConvertFrom-Json
            foreach ($name in @('user', 'password', 'passphrase')) { if ($parsed.$name) { $inputs[$name] = [string]$parsed.$name } }
            if ($parsed.passwords) { $inputs.passwords = $parsed.passwords }
            $parsed = $null
        }
        $line = $null
    }
    $credential = New-Credential $inputs.user $inputs.password
    $label = 'this Windows account'
    if ($credential) { $label = $inputs.user }
    $passphrase = $inputs.passphrase
    $secrets = ''
    if ($Mode -eq 'Scan' -or $Mode -eq 'Restore') {
        $passwords = @{}
        if ($inputs.passwords) { foreach ($property in $inputs.passwords.PSObject.Properties) { $passwords[[string]$property.Name] = [string]$property.Value } }
        $secrets = ConvertTo-Json -InputObject @{ passphrase = $passphrase; passwords = $passwords } -Compress
        $passwords = $null
    }
    $inputs = $null
    $engineText = ''
    if ($EnginePath) { $engineText = [IO.File]::ReadAllText($EnginePath) }
    $planText = '{}'
    if ($PlanPath -and (Test-Path -LiteralPath $PlanPath)) { $planText = [IO.File]::ReadAllText($PlanPath) }

    Write-Log 'INFO' ('Connecting to ' + $Target + ' through PowerShell remoting as ' + $label + '.')
    $sessionArguments = @{ ComputerName = $Target; ErrorAction = 'Stop' }
    if ($credential) { $sessionArguments['Credential'] = $credential }
    $watch = New-PSSession @sessionArguments
    $sessions += $watch
    $runId = [guid]::NewGuid().ToString('N')
    $info = Invoke-Command -Session $watch -ScriptBlock $prepare -ArgumentList @($WorkName, $runId) -ErrorAction Stop
    if (-not $info.admin) { throw ($label + ' is not an administrator on ' + $Target + '. Moving a server needs an administrator on both servers.') }
    Write-Log 'INFO' ('Connected to ' + [string]$info.computer + '.')

    if ($Mode -eq 'Prepare') {
        Write-Result @{ work = [string]$info.work; package = [string]$info.package; computer = [string]$info.computer; iis = [bool]$info.iis }
    }
    elseif ($Mode -eq 'StopSites' -or $Mode -eq 'StartSites') {
        $names = @()
        if ($PlanText -ne '{}') { $names = @((ConvertFrom-Json -InputObject $planText).sites | ForEach-Object { [string]$_ }) }
        $action = 'stop'
        if ($Mode -eq 'StartSites') { $action = 'start' }
        $reply = Invoke-Command -Session $watch -ScriptBlock $sites -ArgumentList $names, $action -ErrorAction Stop
        foreach ($name in @($reply.changed)) { if ($name) { Write-Log 'INFO' ('IIS site ' + $name + ' ' + $(if ($action -eq 'stop') { 'stopped' } else { 'started' }) + ' on ' + $Target + '.') } }
        foreach ($text in @($reply.failed)) { if ($text) { Write-Log 'WARN' ('IIS site ' + $text) } }
        Write-Result @{ changed = @($reply.changed | Where-Object { $_ }); failed = @($reply.failed | Where-Object { $_ }) }
        if (@($reply.failed | Where-Object { $_ }).Count) { throw ('Some IIS sites on ' + $Target + ' could not be changed.') }
    }
    elseif ($Mode -eq 'Clean') {
        $null = Invoke-Command -Session $watch -ScriptBlock $clean -ArgumentList @([string]$info.work) -ErrorAction Stop
        Write-Log 'INFO' ('Removed the move''s package from ' + $Target + '.')
        Write-Result @{ removed = $true }
    }
    else {
        if (-not $engineText) { throw 'The engine was not given.' }
        if (-not $PackageName -and $Mode -ne 'Discover') { throw 'The package name was not given.' }
        $main = New-PSSession @sessionArguments
        $sessions += $main
        if ($Mode -eq 'Discover') { $job = Invoke-Command -Session $main -ScriptBlock $discover -ArgumentList @($engineText, [string]$info.run) -AsJob }
        elseif ($Mode -eq 'Read') {
            $selectionText = ''
            if ($planText -ne '{}') { $plan = ConvertFrom-Json -InputObject $planText; if ($plan.selection) { $selectionText = ConvertTo-Json -InputObject $plan.selection -Depth 4 -Compress } }
            $job = Invoke-Command -Session $main -ScriptBlock $read -ArgumentList @($engineText, [string]$info.work, [string]$info.run, $PackageName, $passphrase, [bool]$IncludeWebContent, $WebContentLimitGB, $selectionText) -AsJob
        }
        else { $job = Invoke-Command -Session $main -ScriptBlock $restore -ArgumentList @($engineText, $Mode, [string]$info.work, [string]$info.run, $PackageName, $secrets, $planText) -AsJob }
        $passphrase = $null
        $secrets = $null
        $stopSent = $false
        $script:offsetValue = [long]0
        $emit = {
            param($Chunk)
            if (-not $Chunk) { return }
            foreach ($text in ([string]$Chunk.text -split "`r?`n")) { if ($text) { Write-Line $text } }
            $script:offsetValue = [long]$Chunk.offset
        }
        while ($job.State -eq 'Running' -or $job.State -eq 'NotStarted') {
            Start-Sleep -Seconds 2
            $stop = [bool]($CancelPath -and -not $stopSent -and (Test-Path -LiteralPath $CancelPath))
            try {
                & $emit (Invoke-Command -Session $watch -ScriptBlock $poll -ArgumentList @([string]$info.run, $script:offsetValue, $stop) -ErrorAction Stop)
                if ($stop) { $stopSent = $true; Write-Log 'INFO' ('Asked ' + $Target + ' to stop after the current item.') }
            }
            catch { Write-Log 'WARN' ('Could not read the progress from ' + $Target + ': ' + $_.Exception.Message) }
        }
        $reply = @(Receive-Job -Job $job -ErrorAction Stop | Where-Object { $_ -and $_.exitCode -ne $null }) | Select-Object -Last 1
        $jobError = ''
        if ($job.ChildJobs.Count -and $job.ChildJobs[0].JobStateInfo.Reason) { $jobError = $job.ChildJobs[0].JobStateInfo.Reason.Message }
        Remove-Job -Job $job -Force -ErrorAction SilentlyContinue
        try { & $emit (Invoke-Command -Session $watch -ScriptBlock $poll -ArgumentList @([string]$info.run, $script:offsetValue, $false) -ErrorAction Stop) } catch { }
        if (-not $reply) {
            if (-not $jobError) { $jobError = 'the connection ended before the engine finished' }
            throw ('The step on ' + $Target + ' did not finish: ' + $jobError + '.')
        }
        if ([string]$reply.error) { throw [string]$reply.error }
        Write-Result @{ exitCode = [int]$reply.exitCode; result = [string]$reply.result; package = [string]$reply.package; sizeBytes = [long]$reply.sizeBytes; rollback = [string]$reply.rollback; work = [string]$info.work; computer = [string]$info.computer }
        $code = [int]$reply.exitCode
        try { Invoke-Command -Session $watch -ScriptBlock $cleanup -ArgumentList @([string]$info.run) -ErrorAction Stop } catch { }
        foreach ($session in $sessions) { Remove-PSSession -Session $session -ErrorAction SilentlyContinue }
        exit $code
    }
    try { Invoke-Command -Session $watch -ScriptBlock $cleanup -ArgumentList @([string]$info.run) -ErrorAction Stop } catch { }
    foreach ($session in $sessions) { Remove-PSSession -Session $session -ErrorAction SilentlyContinue }
    exit 0
}
catch {
    $text = $_.Exception.Message
    if ($text -match 'Access is denied|user name or password|logon failure|credential is invalid|not an administrator') {
        $text += ' Check the account for ' + $Target + ': it must be an administrator there.'
    }
    elseif ($text -match 'TrustedHosts|Kerberos|WinRM cannot|WinRM client cannot|cannot find the computer|cannot complete the operation') {
        $text += ' PowerShell remoting must be allowed on ' + $Target + ' (it is on by default on Windows Server 2012 and later; otherwise run Enable-PSRemoting there). Use its DNS name in the domain; by IP address or outside the domain, add it to this computer''s WinRM TrustedHosts list.'
    }
    Write-Log 'ERROR' $text
    if ($Mode -eq 'Restore') { Write-Line ('@@RESTORE@@' + (ConvertTo-Json -InputObject @{ event = 'done'; status = 'failed'; message = $text } -Compress)) }
    if ($watch -and $info) { try { Invoke-Command -Session $watch -ScriptBlock $cleanup -ArgumentList @([string]$info.run) -ErrorAction Stop } catch { } }
    foreach ($session in $sessions) { Remove-PSSession -Session $session -ErrorAction SilentlyContinue }
    exit 3
}
