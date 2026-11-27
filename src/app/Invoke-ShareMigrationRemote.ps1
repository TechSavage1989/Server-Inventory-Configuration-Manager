param(
    [Parameter(Mandatory = $true)][string]$Target,
    [Parameter(Mandatory = $true)][ValidateSet('Discover', 'Precheck', 'Sync', 'Cutover', 'SourceReadOnly', 'SourceRestore')][string]$Mode,
    [Parameter(Mandatory = $true)][string]$EnginePath,
    [Parameter(Mandatory = $true)][string]$ResultPath,
    [Parameter(Mandatory = $true)][string]$WorkName,
    [string]$Source = '',
    [string]$PlanPath = '',
    [string]$CancelPath = '',
    [switch]$CredentialFromInput
)

# Moves file shares between two other servers, run from an admin computer
# (2026-11-27, owner request). The file share engine (Migrate-FileShares.ps1)
# runs inside the NEW server through PowerShell remoting, so the new server
# reads the old one and Robocopy writes to its own disks: files cross the
# network once, old server to new server, never through this computer.
# Only Invoke-Command over WinRM is used: no program is copied to either
# server, no service, scheduled task or WMI process start is created, and
# neither credential delegation nor an encoded command is used. Inside the new server the
# engine keeps its Robocopy logs, the saved old share permissions and a
# per-run folder under ProgramData\Server Inventory and Migration Manager\
# File-Share-Migration (data only, protected to SYSTEM and Administrators);
# the per-run folder is removed when the run ends.
# Windows cannot pass this computer's sign-in from the new server on to the
# old one (no delegation), so the new server reaches the old one with the
# old server's account, which it uses for that run only.
# The accounts arrive as one JSON line on standard input
# {"targetUser","targetPassword","sourceUser","sourcePassword"}, never on a
# command line. An empty target account uses this Windows account.
# Lines the engine writes inside the new server are followed and repeated
# here, so progress shows live. A stop request (-CancelPath on this computer)
# is passed on to the engine, which stops Robocopy; the next copy carries on
# where it stopped. The engine's result is written to -ResultPath.
# Compatible with Windows PowerShell 3.0 and later on both computers.

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }

function Write-Line { param([string]$Text) [Console]::Out.WriteLine($Text); [Console]::Out.Flush() }
function Write-Log { param([string]$Level, [string]$Message) Write-Line ('{0} [{1}] {2}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Level, $Message) }
function Write-MigrateEvent { param([hashtable]$Event) Write-Line ('@@MIGRATE@@' + (ConvertTo-Json -InputObject $Event -Compress -Depth 4)) }
function New-Credential {
    param([string]$User, [string]$Password)
    if (-not $User) { return $null }
    $secure = New-Object System.Security.SecureString
    foreach ($character in $Password.ToCharArray()) { $secure.AppendChar($character) }
    $secure.MakeReadOnly()
    return New-Object System.Management.Automation.PSCredential($User, $secure)
}

if ($Target -notmatch '^[A-Za-z0-9][A-Za-z0-9.\-]{0,252}$') { Write-Log 'ERROR' 'The new server name is not valid.'; exit 3 }
if ($WorkName -notmatch '^[A-Za-z0-9._\-]{1,120}$') { Write-Log 'ERROR' 'The migration folder name is not valid.'; exit 3 }

# Runs inside the new server: makes the migration's folder (protected to
# SYSTEM and Administrators) and this run's folder.
$prepare = {
    param([string]$Name, [string]$RunId)
    $ErrorActionPreference = 'Stop'
    $base = Join-Path $env:ProgramData 'Server Inventory and Migration Manager\File-Share-Migration'
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
    return @{ work = $work; run = $run; computer = [string]$env:COMPUTERNAME; admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator) }
}

# Runs inside the new server: the engine, as a script block (no file of it
# is written there). Returns its exit code and result text.
$runEngine = {
    param([string]$EngineText, [string]$EngineMode, [string]$SourceName, [string]$PlanText, [string]$Work, [string]$Run, [System.Management.Automation.PSCredential]$Credential)
    $ErrorActionPreference = 'Stop'
    $encoding = New-Object System.Text.UTF8Encoding($false)
    $planFile = ''
    if ($PlanText) { $planFile = Join-Path $Run 'Plan.json'; [IO.File]::WriteAllText($planFile, $PlanText, $encoding) }
    $resultFile = Join-Path $Run 'Result.json'
    $engine = [scriptblock]::Create($EngineText)
    $arguments = @{ Mode = $EngineMode; WorkPath = $Work; ResultPath = $resultFile; CancelPath = (Join-Path $Run 'Stop-Requested'); LogPath = (Join-Path $Run 'Engine.log'); Embedded = $true }
    if ($SourceName) { $arguments['Source'] = $SourceName }
    if ($planFile) { $arguments['PlanPath'] = $planFile }
    if ($Credential) { $arguments['SourceCredentialObject'] = $Credential }
    $output = @(& $engine @arguments)
    $code = 3
    if ($output.Count -and $output[$output.Count - 1] -is [int]) { $code = [int]$output[$output.Count - 1] }
    $result = ''
    if (Test-Path -LiteralPath $resultFile) { $result = [IO.File]::ReadAllText($resultFile) }
    if ($planFile) { Remove-Item -LiteralPath $planFile -Force -ErrorAction SilentlyContinue }
    return @{ exitCode = $code; result = $result }
}

# Runs inside the new server: returns the engine's new complete lines from
# Offset, and passes on a stop request.
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
                $read = $stream.Read($buffer, 0, $count)
                $end = -1
                for ($i = $read - 1; $i -ge 0; $i--) { if ($buffer[$i] -eq 10) { $end = $i; break } }
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

$sessions = @()
$watch = $null
$info = $null
try {
    $accounts = @{ targetUser = ''; targetPassword = ''; sourceUser = ''; sourcePassword = '' }
    if ($CredentialFromInput) {
        $line = [Console]::In.ReadLine()
        if ($line) {
            $parsed = $line | ConvertFrom-Json
            foreach ($name in @('targetUser', 'targetPassword', 'sourceUser', 'sourcePassword')) { if ($parsed.$name) { $accounts[$name] = [string]$parsed.$name } }
            $parsed = $null
        }
        $line = $null
    }
    $targetCredential = New-Credential $accounts.targetUser $accounts.targetPassword
    $sourceCredential = New-Credential $accounts.sourceUser $accounts.sourcePassword
    $targetLabel = 'this Windows account'
    if ($targetCredential) { $targetLabel = $accounts.targetUser }
    $accounts = $null
    $engineText = [IO.File]::ReadAllText($EnginePath)
    $planText = ''
    if ($PlanPath -and (Test-Path -LiteralPath $PlanPath)) { $planText = [IO.File]::ReadAllText($PlanPath) }

    Write-Log 'INFO' ('Connecting to the new server ' + $Target + ' through PowerShell remoting as ' + $targetLabel + '.')
    $sessionArguments = @{ ComputerName = $Target; ErrorAction = 'Stop' }
    if ($targetCredential) { $sessionArguments['Credential'] = $targetCredential }
    # One session runs the engine; the other follows its log and passes on
    # a stop request while it runs.
    $main = New-PSSession @sessionArguments
    $sessions += $main
    $watch = New-PSSession @sessionArguments
    $sessions += $watch
    $runId = [guid]::NewGuid().ToString('N')
    $info = Invoke-Command -Session $watch -ScriptBlock $prepare -ArgumentList @($WorkName, $runId) -ErrorAction Stop
    if (-not $info.admin) { throw ($targetLabel + ' is not an administrator on ' + $Target + '. Moving file shares needs an administrator on the new server.') }
    Write-Log 'INFO' ('Connected to ' + [string]$info.computer + '. Its copy logs are kept in ' + [string]$info.work + ' on ' + [string]$info.computer + '.')
    $job = Invoke-Command -Session $main -ScriptBlock $runEngine -ArgumentList @($engineText, $Mode, $Source, $planText, [string]$info.work, [string]$info.run, $sourceCredential) -AsJob
    $offset = [long]0
    $stopSent = $false
    $emit = {
        param($Chunk)
        if (-not $Chunk) { return }
        foreach ($text in ([string]$Chunk.text -split "`r?`n")) { if ($text) { Write-Line $text } }
        $script:offsetValue = [long]$Chunk.offset
    }
    $script:offsetValue = $offset
    while ($job.State -eq 'Running' -or $job.State -eq 'NotStarted') {
        Start-Sleep -Seconds 2
        $stop = [bool]($CancelPath -and -not $stopSent -and (Test-Path -LiteralPath $CancelPath))
        try {
            $chunk = Invoke-Command -Session $watch -ScriptBlock $poll -ArgumentList @([string]$info.run, $script:offsetValue, $stop) -ErrorAction Stop
            & $emit $chunk
            if ($stop) { $stopSent = $true; Write-Log 'INFO' ('Asked ' + $Target + ' to stop the copy.') }
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
        throw ('The step on ' + $Target + ' did not finish: ' + $jobError + '. A copy that was cut off carries on where it stopped the next time.')
    }
    if ([string]$reply.result) { [IO.File]::WriteAllText($ResultPath, [string]$reply.result, (New-Object System.Text.UTF8Encoding($false))) }
    $code = [int]$reply.exitCode
    try { Invoke-Command -Session $watch -ScriptBlock $cleanup -ArgumentList @([string]$info.run) -ErrorAction Stop } catch { }
    foreach ($session in $sessions) { Remove-PSSession -Session $session -ErrorAction SilentlyContinue }
    exit $code
}
catch {
    $text = $_.Exception.Message
    if ($text -match 'Access is denied|user name or password|logon failure|credential is invalid|not an administrator') {
        Write-MigrateEvent @{ event = 'accessDenied'; message = '' }
        $text += ' Check the account for the new server: it must be an administrator on ' + $Target + '.'
    }
    elseif ($text -match 'TrustedHosts|Kerberos|WinRM cannot|WinRM client cannot|cannot find the computer|cannot complete the operation') {
        $text += ' PowerShell remoting must be allowed on ' + $Target + ' (it is on by default on Windows Server 2012 and later; otherwise run Enable-PSRemoting there). Use its DNS name in the domain; by IP address or outside the domain, add it to this computer''s WinRM TrustedHosts list.'
    }
    Write-Log 'ERROR' $text
    Write-MigrateEvent @{ event = 'error'; message = $text }
    if ($watch -and $info) { try { Invoke-Command -Session $watch -ScriptBlock $cleanup -ArgumentList @([string]$info.run) -ErrorAction Stop } catch { } }
    foreach ($session in $sessions) { Remove-PSSession -Session $session -ErrorAction SilentlyContinue }
    exit 3
}
