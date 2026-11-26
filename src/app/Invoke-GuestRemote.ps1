param(
    [Parameter(Mandatory = $true)][ValidateSet('direct', 'hostdirect', 'winrm')][string]$Transport,
    [Parameter(Mandatory = $true)][string]$Mode,
    [Parameter(Mandatory = $true)][string]$EnginePath,
    [Parameter(Mandatory = $true)][string]$ResultPath,
    [string]$VMName = '',
    [string]$ComputerName = '',
    [string]$HostName = '',
    [string]$PlanPath = '',
    [string]$CapturePath = '',
    [string]$ExportPath = '',
    [switch]$CredentialFromInput
)

# Runs the guest engine (Migrate-VmGuest.ps1) inside another Windows computer
# so nothing is copied into, installed in or left running in it, the way
# StarWind V2V needs nothing on the old server:
#   direct      a VM on this Hyper-V host, through PowerShell Direct
#               (Invoke-Command -VMName over the VM bus: no network needed,
#               so it works right after a cutover, before any IP is set)
#   hostdirect  a VM on another Hyper-V host: PowerShell remoting to that
#               host, which then uses PowerShell Direct to the VM
#   winrm       a computer over the network (Invoke-Command -ComputerName),
#               for example the VMware VM before the cutover
# Only Invoke-Command is used: no service, scheduled task, WMI process start,
# credential delegation, file copy of a program, or encoded command. The engine's text is
# sent as a script block. Inside the guest it keeps its recording and undo
# data in ProgramData\Server Inventory and Migration Manager\Guest-Migration
# (data only, which also travels with the disks to the new VM), and a
# per-run folder in TEMP that is removed when the run ends.
# The accounts arrive as one JSON line on standard input, never on a command
# line, and are used only for this run. BitLocker recovery keys come back to
# -ExportPath on this computer and are removed from the guest. The result is
# written to -ResultPath as the engine's own {"ok":..,"error":..,"result":..}.
# Compatible with Windows PowerShell 3.0 and later (PowerShell Direct needs
# Windows 10 or Windows Server 2016 and later on both the host and the VM).

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }

function Write-Line { param([string]$Text) [Console]::Out.WriteLine($Text); [Console]::Out.Flush() }
function Write-Log { param([string]$Message) Write-Line ('{0} [INFO] {1}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Message) }
function Write-Failure {
    param([string]$ErrorText)
    $json = ConvertTo-Json -InputObject @{ ok = $false; mode = $Mode; error = $ErrorText } -Compress
    [IO.File]::WriteAllText($ResultPath, $json, (New-Object System.Text.UTF8Encoding($false)))
}
function New-Credential {
    param([string]$User, [string]$Password)
    if (-not $User) { return $null }
    $secure = New-Object System.Security.SecureString
    foreach ($character in $Password.ToCharArray()) { $secure.AppendChar($character) }
    $secure.MakeReadOnly()
    return New-Object System.Management.Automation.PSCredential($User, $secure)
}

# Runs inside the guest. Arguments: the engine's text, the mode, the plan
# file's text, the console's capture text (or empty).
$insideGuest = {
    param([string]$EngineText, [string]$EngineMode, [string]$PlanText, [string]$CaptureText)
    $ErrorActionPreference = 'Stop'
    $encoding = New-Object System.Text.UTF8Encoding($false)
    $work = Join-Path $env:ProgramData 'Server Inventory and Migration Manager\Guest-Migration'
    if (-not (Test-Path -LiteralPath $work)) { New-Item -ItemType Directory -Path $work -Force | Out-Null }
    $run = Join-Path ([IO.Path]::GetTempPath()) ('SIMM-Guest-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $run -Force | Out-Null
    $notes = New-Object System.Collections.ArrayList
    try {
        # The console's recording is used only when this computer has none, or
        # when it is a full recording newer than the one here. A recording
        # made inside Windows holds more than one made from vCenter.
        if ($CaptureText) {
            $capturePath = Join-Path $work 'Guest-Capture.json'
            $offered = $null
            try { $offered = $CaptureText | ConvertFrom-Json } catch { }
            $use = $false
            if ($offered) {
                if (-not (Test-Path -LiteralPath $capturePath)) { $use = $true }
                else {
                    $existing = $null
                    try { $existing = (Get-Content -LiteralPath $capturePath -Raw) | ConvertFrom-Json } catch { }
                    if (-not $existing) { $use = $true }
                    elseif ([string]$offered.Source -ne 'vCenter' -or [string]$existing.Source -eq 'vCenter') {
                        try { $use = ([datetime]::Parse([string]$offered.CapturedAt).ToUniversalTime() -gt [datetime]::Parse([string]$existing.CapturedAt).ToUniversalTime()) } catch { $use = $false }
                    }
                }
            }
            if ($use) {
                [IO.File]::WriteAllText($capturePath, $CaptureText, $encoding)
                [void]$notes.Add('Used the recording kept by the console (' + [string]$offered.Source + ' ' + [string]$offered.CapturedAt + ').')
            }
        }
        $planPath = ''
        if ($PlanText) { $planPath = Join-Path $run 'Plan.json'; [IO.File]::WriteAllText($planPath, $PlanText, $encoding) }
        $resultPath = Join-Path $run 'Result.json'
        $logPath = Join-Path $work 'Guest-Migration.log'
        $logStart = 0
        if (Test-Path -LiteralPath $logPath) { $logStart = @(Get-Content -LiteralPath $logPath).Count }
        $engine = [scriptblock]::Create($EngineText)
        & $engine -Mode $EngineMode -WorkPath $work -ResultPath $resultPath -PlanPath $planPath -ExportPath $run -Embedded | Out-Null
        $result = ''
        if (Test-Path -LiteralPath $resultPath) { $result = [IO.File]::ReadAllText($resultPath) }
        $log = @()
        if (Test-Path -LiteralPath $logPath) { $log = @(Get-Content -LiteralPath $logPath | Select-Object -Skip $logStart) }
        $keys = @(Get-ChildItem -LiteralPath $run -Filter 'BitLocker-Recovery-Keys-*.txt' -ErrorAction SilentlyContinue | ForEach-Object { @{ name = $_.Name; text = [IO.File]::ReadAllText($_.FullName) } })
        $capture = ''
        if ($EngineMode -eq 'Capture' -and (Test-Path -LiteralPath (Join-Path $work 'Guest-Capture.json'))) { $capture = [IO.File]::ReadAllText((Join-Path $work 'Guest-Capture.json')) }
        return @{ result = $result; log = $log; notes = @($notes); keys = $keys; capture = $capture; computer = [string]$env:COMPUTERNAME }
    }
    finally {
        Remove-Item -LiteralPath $run -Recurse -Force -ErrorAction SilentlyContinue
    }
}

try {
    $accounts = @{ user = ''; password = ''; hostUser = ''; hostPassword = '' }
    if ($CredentialFromInput) {
        $line = [Console]::In.ReadLine()
        if ($line) {
            $parsed = $line | ConvertFrom-Json
            foreach ($name in @('user', 'password', 'hostUser', 'hostPassword')) { if ($parsed.$name) { $accounts[$name] = [string]$parsed.$name } }
        }
    }
    $guestCredential = New-Credential $accounts.user $accounts.password
    $hostCredential = New-Credential $accounts.hostUser $accounts.hostPassword
    $accounts = $null
    $engineText = [IO.File]::ReadAllText($EnginePath)
    $planText = ''
    if ($PlanPath -and (Test-Path -LiteralPath $PlanPath)) { $planText = [IO.File]::ReadAllText($PlanPath) }
    $captureText = ''
    if ($CapturePath -and (Test-Path -LiteralPath $CapturePath)) { $captureText = [IO.File]::ReadAllText($CapturePath) }
    $arguments = @($engineText, $Mode, $planText, $captureText)
    $output = $null
    switch ($Transport) {
        'direct' {
            if (-not (Get-Command Invoke-Command).Parameters.ContainsKey('VMName')) { throw 'PowerShell Direct needs Windows 10 or Windows Server 2016 or later on this Hyper-V host.' }
            Write-Log ('Connecting to VM ' + $VMName + ' through PowerShell Direct.')
            $output = Invoke-Command -VMName $VMName -Credential $guestCredential -ScriptBlock $insideGuest -ArgumentList $arguments -ErrorAction Stop
        }
        'hostdirect' {
            Write-Log ('Connecting to Hyper-V host ' + $HostName + ', then to VM ' + $VMName + ' through PowerShell Direct.')
            $relay = {
                param([string]$Name, [System.Management.Automation.PSCredential]$Credential, [string]$Inner, [object[]]$InnerArguments)
                if (-not (Get-Command Invoke-Command).Parameters.ContainsKey('VMName')) { throw ('PowerShell Direct needs Windows Server 2016 or later on ' + $env:COMPUTERNAME + '.') }
                Invoke-Command -VMName $Name -Credential $Credential -ScriptBlock ([scriptblock]::Create($Inner)) -ArgumentList $InnerArguments -ErrorAction Stop
            }
            $hostArguments = @($VMName, $guestCredential, $insideGuest.ToString(), $arguments)
            if ($hostCredential) { $output = Invoke-Command -ComputerName $HostName -Credential $hostCredential -ScriptBlock $relay -ArgumentList $hostArguments -ErrorAction Stop }
            else { $output = Invoke-Command -ComputerName $HostName -ScriptBlock $relay -ArgumentList $hostArguments -ErrorAction Stop }
        }
        'winrm' {
            Write-Log ('Connecting to ' + $ComputerName + ' through PowerShell remoting.')
            if ($guestCredential) { $output = Invoke-Command -ComputerName $ComputerName -Credential $guestCredential -ScriptBlock $insideGuest -ArgumentList $arguments -ErrorAction Stop }
            else { $output = Invoke-Command -ComputerName $ComputerName -ScriptBlock $insideGuest -ArgumentList $arguments -ErrorAction Stop }
        }
    }
    $guestCredential = $null
    $hostCredential = $null
    $reply = @($output | Where-Object { $_ -and $_.result -ne $null } | Select-Object -Last 1)
    if ($reply.Count -ne 1) { throw 'The computer did not return a result.' }
    $reply = $reply[0]
    foreach ($note in @($reply.notes)) { Write-Log ([string]$note) }
    foreach ($entry in @($reply.log)) { Write-Line ('[' + [string]$reply.computer + '] ' + [string]$entry) }
    if ($ExportPath -and @($reply.keys).Count -gt 0) {
        if (-not (Test-Path -LiteralPath $ExportPath)) { New-Item -ItemType Directory -Path $ExportPath -Force | Out-Null }
        foreach ($key in @($reply.keys)) {
            $name = [IO.Path]::GetFileName([string]$key.name)
            if ($name -notmatch '^BitLocker-Recovery-Keys-[A-Za-z0-9_.\-]+\.txt$') { continue }
            [IO.File]::WriteAllText((Join-Path $ExportPath $name), [string]$key.text, (New-Object System.Text.UTF8Encoding($false)))
            Write-Log ('Saved the BitLocker recovery passwords to ' + (Join-Path $ExportPath $name) + ' on this computer; the copy inside the guest was removed.')
        }
    }
    if ($ExportPath -and [string]$reply.capture) {
        if (-not (Test-Path -LiteralPath $ExportPath)) { New-Item -ItemType Directory -Path $ExportPath -Force | Out-Null }
        [IO.File]::WriteAllText((Join-Path $ExportPath 'Guest-Capture.json'), [string]$reply.capture, (New-Object System.Text.UTF8Encoding($false)))
    }
    if (-not [string]$reply.result) { throw ('The guest engine wrote no result on ' + [string]$reply.computer + '.') }
    [IO.File]::WriteAllText($ResultPath, [string]$reply.result, (New-Object System.Text.UTF8Encoding($false)))
}
catch {
    $text = $_.Exception.Message
    if ($text -match 'TrustedHosts|Kerberos|WinRM cannot process the request|cannot find the computer|WinRM client cannot') { $text += ' PowerShell remoting needs WinRM on the computer (on by default on Windows Server; run Enable-PSRemoting on Windows 10 and 11), and by IP address or outside the domain the computer in this computer''s WinRM TrustedHosts list. Or use its DNS name in the domain.' }
    if ($text -match 'credential is invalid|user name or password|logon failure') { $text = 'The VM refused the account: check the user name (for example .\Administrator or DOMAIN\admin) and password.' }
    Write-Line ((Get-Date -Format 'yyyy-MM-dd HH:mm:ss') + ' [ERROR] ' + $text)
    try { Write-Failure $text } catch { }
    exit 1
}
exit 0
