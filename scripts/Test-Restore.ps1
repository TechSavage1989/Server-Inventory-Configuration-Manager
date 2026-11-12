# Real restore round trips for the embedded restore engine on a disposable
# Windows runner. Each component's configuration is created, exported the same
# way the collector exports it into a synthetic package with a SHA-256
# manifest, changed or removed, and then restored by
# src/app/Restore-ServerConfiguration.ps1. The script changes this computer's
# configuration (roles, firewall, security policy, registry, certificates), so
# it refuses to run outside CI unless -AllowSystemChanges is given.
[CmdletBinding()]
param(
    [string]$OutputDirectory = (Join-Path $PSScriptRoot '..\build\restore-validation'),
    [switch]$AllowSystemChanges,
    [switch]$SkipRoles
)
$ErrorActionPreference = 'Stop'
if (-not $env:GITHUB_ACTIONS -and -not $AllowSystemChanges) { throw 'Test-Restore.ps1 changes system configuration. Run it only on a disposable machine with -AllowSystemChanges.' }
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Test-Restore.ps1 must run elevated.' }

$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
if (Test-Path -LiteralPath $OutputDirectory) { Remove-Item -LiteralPath $OutputDirectory -Recurse -Force }
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$engine = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\src\app\Restore-ServerConfiguration.ps1'))
$powerShell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
$checks = New-Object Collections.ArrayList
function Assert([bool]$Condition, [string]$Description) { if (-not $Condition) { throw ('FAIL ' + $Description) }; [void]$checks.Add($Description); Write-Host ('PASS ' + $Description) }
function Invoke-Checked([string]$FilePath, [string[]]$Arguments) {
    $output = & $FilePath @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) { throw ('{0} {1} failed ({2}): {3}' -f $FilePath, ($Arguments -join ' '), $LASTEXITCODE, ($output | Out-String)) }
}

$tokens = $null; $errors = $null
[Management.Automation.Language.Parser]::ParseFile($engine, [ref]$tokens, [ref]$errors) | Out-Null
Assert ($errors.Count -eq 0) 'Restore engine parses without errors'

if (-not $SkipRoles) {
    $features = @('DHCP', 'DNS', 'Web-Server', 'NPAS', 'Print-Server')
    Write-Host ('Installing test roles: ' + ($features -join ', '))
    $install = Install-WindowsFeature -Name $features -IncludeManagementTools
    Assert ($install.Success) 'DHCP, DNS, IIS, NPS and Print Server roles installed on the runner'
    foreach ($service in @('DHCPServer', 'DNS', 'W3SVC', 'IAS', 'Spooler')) { Set-Service -Name $service -StartupType Automatic; Start-Service -Name $service }
}

$computer = $env:COMPUTERNAME
$package = Join-Path $OutputDirectory ($computer + '_ServerInventory_20261112_101010_restore1')
function Get-PackagePath([string]$Relative) { $path = Join-Path $package $Relative; New-Item -ItemType Directory -Path (Split-Path $path -Parent) -Force | Out-Null; return $path }
New-Item -ItemType Directory -Path (Join-Path $package '00-Summary') -Force | Out-Null
Set-Content -LiteralPath (Join-Path $package '00-Summary\Summary.txt') -Value 'Synthetic restore validation package' -Encoding UTF8
$work = Join-Path $OutputDirectory 'work'
New-Item -ItemType Directory -Path $work -Force | Out-Null

# --- Original configuration, exported as the collector exports it ---------

# Registry branch (reg.exe export, like Export-RegistryKey).
$regKey = 'HKLM\SOFTWARE\SICM-RestoreTest'
Invoke-Checked reg.exe @('add', $regKey, '/v', 'Setting', '/t', 'REG_SZ', '/d', 'Original', '/f')
Invoke-Checked reg.exe @('export', $regKey, (Get-PackagePath '01-System\Registry-Configuration\SICM-RestoreTest.reg'), '/y')

# Local security policy (secedit /export).
Invoke-Checked net.exe @('accounts', '/minpwlen:9')
Invoke-Checked secedit.exe @('/export', '/cfg', (Get-PackagePath '06-Accounts-Policy-Security\Local-Security-Policy.inf'), '/quiet')

# Certificate: public .cer plus its recorded store in the certificate CSV.
$certificate = New-SelfSignedCertificate -Subject 'CN=SICM Restore Test' -CertStoreLocation 'Cert:\LocalMachine\My' -NotAfter (Get-Date).AddDays(2)
$thumbprint = $certificate.Thumbprint
$certFile = Get-PackagePath ('06-Accounts-Policy-Security\Public-Certificate-Exports\CN=SICM Restore Test_' + $thumbprint + '.cer')
[IO.File]::WriteAllBytes($certFile, $certificate.Export([Security.Cryptography.X509Certificates.X509ContentType]::Cert))
Remove-Item -LiteralPath ('Cert:\LocalMachine\My\' + $thumbprint) -DeleteKey
# New-SelfSignedCertificate also places a copy in the intermediate CA store.
Get-ChildItem -LiteralPath 'Cert:\LocalMachine\CA' | Where-Object { $_.Thumbprint -eq $thumbprint } | Remove-Item
Assert (-not (Test-Path -LiteralPath ('Cert:\LocalMachine\CA\' + $thumbprint))) 'Test certificate starts absent from LocalMachine\CA'
@([pscustomobject]@{ StorePath = 'LocalMachine\CA'; Subject = 'CN=SICM Restore Test'; Thumbprint = $thumbprint }) |
    Export-Csv -LiteralPath (Get-PackagePath '06-Accounts-Policy-Security\Local-Machine-Certificates.csv') -NoTypeInformation -Encoding UTF8

# Firewall policy (netsh advfirewall export).
New-NetFirewallRule -DisplayName 'SICM Restore Test' -Direction Inbound -Protocol TCP -LocalPort 49999 -Action Allow | Out-Null
Invoke-Checked netsh.exe @('advfirewall', 'export', (Get-PackagePath '06-Accounts-Policy-Security\Windows-Firewall-Policy.wfw'))

# Recursive NTFS permissions (icacls /save /t, like Invoke-RecursiveAclExports).
$aclRoot = Join-Path $work 'Shares'
$share = Join-Path $aclRoot 'Finance'
New-Item -ItemType Directory -Path (Join-Path $share 'Reports') -Force | Out-Null
Set-Content -LiteralPath (Join-Path $share 'Reports\Q1.txt') -Value 'synthetic'
Invoke-Checked icacls.exe @($share, '/grant', 'Guests:(OI)(CI)R', '/t', '/c', '/q')
$aclFolder = Get-PackagePath '07-Shares-Permissions\Recursive-ICACLS-Backups\placeholder'
$aclFolder = Split-Path $aclFolder -Parent
Invoke-Checked icacls.exe @($share, '/save', (Join-Path $aclFolder '0001-Finance.acl'), '/t', '/c', '/q')
@([pscustomobject]@{ ShareName = 'Finance'; SourcePath = $share; AclFile = '0001-Finance.acl'; Status = 'Completed'; ExitCode = 0; ElapsedSeconds = 1; TimeoutSeconds = 120; Note = 'synthetic' }) |
    Export-Csv -LiteralPath (Join-Path $aclFolder 'ICACLS-Mapping.csv') -NoTypeInformation -Encoding UTF8

# Scheduled task (Export-ScheduledTask XML, Unicode).
$action = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument '/c exit 0'
$trigger = New-ScheduledTaskTrigger -Daily -At '03:00'
Register-ScheduledTask -TaskName 'Nightly-Check' -TaskPath '\SICM-Restore\' -Action $action -Trigger $trigger -User 'SYSTEM' | Out-Null
Export-ScheduledTask -TaskName 'Nightly-Check' -TaskPath '\SICM-Restore\' | Set-Content -LiteralPath (Get-PackagePath '05-Software-Services-Tasks\Scheduled-Task-XML-Custom\SICM-Restore_Nightly-Check.xml') -Encoding Unicode

if (-not $SkipRoles) {
    # Printers (PrintBRM backup of the local print server).
    Add-PrinterDriver -Name 'Generic / Text Only'
    Add-PrinterPort -Name 'SICM_TEST_PORT' -PrinterHostAddress '192.0.2.9'
    Add-Printer -Name 'SICM Restore Printer' -DriverName 'Generic / Text Only' -PortName 'SICM_TEST_PORT'
    $printBrm = Join-Path $env:SystemRoot 'System32\spool\tools\PrintBrm.exe'
    $printerExport = Get-PackagePath ('08-Role-Specific\Print-Server\' + $computer + '.printerExport')
    # Negative control: the remote form the collector used before 3.2.19 exits 0
    # but writes nothing when no printer is shared.
    $remoteExport = Join-Path $work 'remote-form.printerExport'
    $remoteOutput = (& $printBrm -s ('\\' + $computer) -b -f $remoteExport 2>&1 | Out-String)
    Assert (($LASTEXITCODE -eq 0) -and -not (Test-Path -LiteralPath $remoteExport) -and ($remoteOutput -match '0x80043333')) 'Old PrintBRM -s form exits 0 without an export when no printer is shared'
    # The real collector, limited to print services, must now write the export.
    $collectorOut = Join-Path $work 'collector'
    New-Item -ItemType Directory -Path $collectorOut -Force | Out-Null
    $collector = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\src\app\Export-ServerInventory.ps1'))
    & $powerShell -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "& '$collector' -OutputRoot '$collectorOut' -InventoryCategoryList 'PrintServices' -ExportCategoryList 'PrintServices' -CreateZip `$false" *> (Join-Path $OutputDirectory 'collector.log')
    $collected = @(Get-ChildItem -LiteralPath $collectorOut -Recurse -Filter ($computer + '.printerExport') -File)
    $collectedLog = @(Get-ChildItem -LiteralPath $collectorOut -Recurse -Filter 'PrintBRM-Export.log' -File)
    if ($collectedLog.Count) { Get-Content -LiteralPath $collectedLog[0].FullName | ForEach-Object { Write-Host ('  collector PrintBRM: ' + $_) } }
    Assert ($collected.Count -eq 1 -and $collected[0].Length -gt 0) 'Collector writes the PrintBRM printer export on a server with no shared printer'
    Assert (-not ((Get-Content -LiteralPath $collectedLog[0].FullName -Raw) -match '0x8[0-9A-Fa-f]{7}')) 'Collector PrintBRM export reports no error code'
    # Restore printers from the file the real collector produced.
    Copy-Item -LiteralPath $collected[0].FullName -Destination $printerExport

    # IIS (appcmd add backup, copied out of inetsrv\backup).
    $appCmd = Join-Path $env:SystemRoot 'System32\inetsrv\appcmd.exe'
    Invoke-Checked $appCmd @('add', 'apppool', '/name:SICM-Restore-Pool')
    Invoke-Checked $appCmd @('add', 'backup', 'SICM_RestoreValidation')
    Copy-Item -LiteralPath (Join-Path $env:SystemRoot 'System32\inetsrv\backup\SICM_RestoreValidation') -Destination (Get-PackagePath '08-Role-Specific\IIS\IIS-Configuration-Backup') -Recurse
    Invoke-Checked $appCmd @('delete', 'backup', 'SICM_RestoreValidation')

    # DHCP (Export-DhcpServer with leases).
    # Finish DHCP post-install configuration as Server Manager does on a real
    # server: security groups, then mark configuration complete.
    Add-DhcpServerSecurityGroup -ComputerName $computer
    Set-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\ServerManager\Roles\12' -Name 'ConfigurationState' -Value 2 -ErrorAction SilentlyContinue
    Restart-Service DHCPServer
    Add-DhcpServerv4Scope -Name 'SICM Restore Scope' -StartRange 10.250.0.10 -EndRange 10.250.0.50 -SubnetMask 255.255.255.0
    Set-DhcpServerv4OptionValue -ComputerName $computer -OptionId 6 -Value '192.0.2.53' -Force
    Add-DhcpServerv4Class -ComputerName $computer -Name 'SICM Restore Class' -Type User -Data 'SICMTEST' -Description 'Synthetic restore class'
    Export-DhcpServer -ComputerName $computer -File (Get-PackagePath '08-Role-Specific\DHCP\DHCP-Configuration-and-Leases.xml') -Leases -Force

    # NPS (full configuration export; contains a synthetic shared secret).
    $secret = [Guid]::NewGuid().ToString('N')
    New-NpsRadiusClient -Name 'SICM-Restore-Client' -Address '192.0.2.5' -SharedSecret $secret | Out-Null
    Export-NpsConfiguration -Path (Get-PackagePath '08-Role-Specific\NPS\NPS-FULL-CONFIG-SENSITIVE.xml')

    # DNS (file-backed primary zone copied from %windir%\System32\dns).
    # A freshly installed DNS Server can refuse zone creation until it has
    # finished starting, and the runner image may boot DNS from a boot file.
    # Switch to registry boot, then try a few ordinary zone names.
    New-Item -ItemType Directory -Path (Join-Path $env:SystemRoot 'System32\dns') -Force | Out-Null
    & dnscmd.exe /config /bootmethod 3 2>&1 | ForEach-Object { Write-Host ('  dnscmd: ' + $_) }
    Restart-Service DNS
    Write-Host ('OS: ' + (Get-CimInstance Win32_OperatingSystem).Caption + ' ' + [Environment]::OSVersion.Version)
    & dnscmd.exe /info 2>&1 | Select-Object -First 40 | ForEach-Object { Write-Host ('  dnscmd /info: ' + $_) }
    $zone = $null
    foreach ($candidate in @('restoretest.example.com', 'sicmrestore.test', 'sicm-restore.example')) {
        for ($attempt = 1; $attempt -le 4 -and -not $zone; $attempt++) {
            try { Add-DnsServerPrimaryZone -Name $candidate -ZoneFile ($candidate + '.dns') -ErrorAction Stop; $zone = $candidate }
            catch {
                $detail = $_.Exception.Message
                if ($_.Exception.InnerException) { $detail += ' | ' + $_.Exception.InnerException.Message }
                if ($_.Exception.ErrorData) { $detail += ' | ' + ($_.Exception.ErrorData | Out-String).Trim() }
                Write-Host ('DNS zone {0} attempt {1} failed: {2}' -f $candidate, $attempt, $detail)
                Start-Sleep -Seconds 5
            }
        }
        if ($zone) { break }
    }
    if (-not $zone) {
        & dnscmd.exe /zoneadd restoretest.example.com /primary 2>&1 | ForEach-Object { Write-Host ('  dnscmd /zoneadd: ' + $_) }
        Get-WinEvent -LogName 'DNS Server' -MaxEvents 20 -ErrorAction SilentlyContinue | ForEach-Object { Write-Host ('  DNS event ' + $_.Id + ': ' + $_.Message) }
        throw 'The DNS Server on this runner refused every test zone.'
    }
    Write-Host ('Created test zone ' + $zone)
    Add-DnsServerResourceRecordA -ZoneName $zone -Name 'app01' -IPv4Address '192.0.2.20'
    Invoke-Checked dnscmd.exe @('/zonewriteback', $zone)
    $zoneFolder = Get-PackagePath ('08-Role-Specific\DNS\Zone-' + $zone + '\placeholder')
    $zoneFolder = Split-Path $zoneFolder -Parent
    Copy-Item -LiteralPath (Join-Path $env:SystemRoot ('System32\dns\' + $zone + '.dns')) -Destination (Join-Path $zoneFolder ('File-Backed-Zone-' + $zone + '.dns'))
    Get-DnsServerZone | Select-Object ZoneName, ZoneType, IsDsIntegrated, ReplicationScope, ZoneFile, IsReverseLookupZone |
        Export-Csv -LiteralPath (Get-PackagePath '08-Role-Specific\DNS\DNS-Zones.csv') -NoTypeInformation -Encoding UTF8
}

# SHA256-Manifest.csv like the collector's.
$rows = foreach ($file in @(Get-ChildItem -LiteralPath $package -Recurse -File)) {
    $sha = [Security.Cryptography.SHA256]::Create()
    $stream = [IO.File]::OpenRead($file.FullName)
    try { $hash = ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '') } finally { $stream.Dispose(); $sha.Dispose() }
    [pscustomobject]@{ RelativePath = $file.FullName.Substring($package.Length + 1); LengthBytes = $file.Length; LastWriteTime = $file.LastWriteTime.ToString('yyyy-MM-dd HH:mm:ss'); SHA256 = $hash }
}
$rows | Export-Csv -LiteralPath (Join-Path $package 'SHA256-Manifest.csv') -NoTypeInformation -Encoding UTF8

# --- Change or remove everything -------------------------------------------

Invoke-Checked reg.exe @('add', $regKey, '/v', 'Setting', '/t', 'REG_SZ', '/d', 'Changed', '/f')
Invoke-Checked net.exe @('accounts', '/minpwlen:3')
Remove-NetFirewallRule -DisplayName 'SICM Restore Test'
Invoke-Checked icacls.exe @($share, '/remove:g', 'Guests', '/t', '/c', '/q')
Unregister-ScheduledTask -TaskName 'Nightly-Check' -TaskPath '\SICM-Restore\' -Confirm:$false
if (-not $SkipRoles) {
    Remove-Printer -Name 'SICM Restore Printer'
    Invoke-Checked $appCmd @('delete', 'apppool', 'SICM-Restore-Pool')
    Remove-DhcpServerv4Scope -ScopeId 10.250.0.0 -Force
    Remove-DhcpServerv4OptionValue -ComputerName $computer -OptionId 6
    Remove-DhcpServerv4Class -ComputerName $computer -Name 'SICM Restore Class' -Type User
    Remove-NpsRadiusClient -Name 'SICM-Restore-Client'
    Remove-DnsServerZone -Name $zone -Force
    Remove-Item -LiteralPath (Join-Path $env:SystemRoot ('System32\dns\' + $zone + '.dns')) -Force -ErrorAction SilentlyContinue
}
Assert ((Get-ItemProperty -Path 'HKLM:\SOFTWARE\SICM-RestoreTest').Setting -eq 'Changed') 'Configuration changed before restore'

# --- Scan ------------------------------------------------------------------

function Invoke-Engine([string[]]$Arguments, [string]$Name) {
    $log = Join-Path $OutputDirectory ($Name + '.log')
    & $powerShell -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $engine @Arguments *> $log
    $code = $LASTEXITCODE
    Get-Content -LiteralPath $log | ForEach-Object { Write-Host ('  ' + $_) }
    return @{ code = $code; log = (Get-Content -LiteralPath $log -Raw) }
}
$scanPath = Join-Path $OutputDirectory 'scan.json'
$result = Invoke-Engine @('-Mode', 'Scan', '-PackagePath', $package, '-ResultPath', $scanPath) 'scan'
Assert ($result.code -eq 0) 'Scan completes'
$scan = Get-Content -LiteralPath $scanPath -Raw | ConvertFrom-Json
Assert ($scan.manifestVerified -and $scan.sameComputer -and $scan.isAdministrator) 'Scan identifies the package, this server and administrator rights'
$expected = @('registry', 'secpol', 'certificates', 'firewall', 'acls', 'tasks')
if (-not $SkipRoles) { $expected += @('printers', 'iis', 'dhcp', 'nps', 'dns') }
$plan = @()
foreach ($id in $expected) {
    $component = @($scan.components | Where-Object { $_.id -eq $id })[0]
    $items = @($component.items | Where-Object { $_.available })
    Assert ($component.available -and $items.Count -ge 1) ('Scan offers ' + $id + ': ' + $component.blockedReason)
    if ($id -eq 'registry') { $items = @($items | Where-Object { $_.id -like '*SICM-RestoreTest.reg' }) }
    if ($id -eq 'dns') { $items = @($items | Where-Object { $_.title -eq $zone }) }
    $plan += @{ id = $id; items = @($items | ForEach-Object { $_.id }) }
}
$gpo = @($scan.components | Where-Object { $_.id -eq 'gpo' })[0]
Assert (-not $gpo.available -and $gpo.blockedReason) 'Group Policy is not offered without backups'

# --- Negative controls: stop request and changed package file ---------------

$planPath = Join-Path $OutputDirectory 'plan.json'
ConvertTo-Json -InputObject @{ components = $plan } -Depth 5 | Set-Content -LiteralPath $planPath -Encoding UTF8
$cancelPath = Join-Path $OutputDirectory 'stop'
Set-Content -LiteralPath $cancelPath -Value 'stop'
$result = Invoke-Engine @('-Mode', 'Restore', '-PackagePath', $package, '-PlanPath', $planPath, '-RollbackPath', (Join-Path $OutputDirectory 'rollback-canceled'), '-CancelPath', $cancelPath) 'restore-canceled'
Assert ($result.code -eq 2 -and $result.log -match '"status":"canceled"' -and $result.log -notmatch '"status":"succeeded"') 'A stop request before the first item restores nothing'
Assert ((Get-ItemProperty -Path 'HKLM:\SOFTWARE\SICM-RestoreTest').Setting -eq 'Changed') 'Stopped restore changed nothing'
Remove-Item -LiteralPath $cancelPath

$regFile = Join-Path $package '01-System\Registry-Configuration\SICM-RestoreTest.reg'
$original = [IO.File]::ReadAllBytes($regFile)
$tampered = [IO.File]::ReadAllText($regFile, [Text.Encoding]::Unicode).Replace('Original', 'Tampered!')
[IO.File]::WriteAllText($regFile, $tampered, [Text.Encoding]::Unicode)
$result = Invoke-Engine @('-Mode', 'Restore', '-PackagePath', $package, '-PlanPath', $planPath, '-RollbackPath', (Join-Path $OutputDirectory 'rollback-tampered'), '-CancelPath', $cancelPath) 'restore-tampered'
Assert ($result.code -eq 1 -and $result.log -match 'SHA-256' -and $result.log -match '"status":"skipped"') 'A package file changed after the scan stops the restore before anything is applied'
Assert ((Get-ItemProperty -Path 'HKLM:\SOFTWARE\SICM-RestoreTest').Setting -eq 'Changed') 'Tampered registry file was not imported'
[IO.File]::WriteAllBytes($regFile, $original)

# --- Restore ---------------------------------------------------------------

$rollback = Join-Path $OutputDirectory 'rollback'
$result = Invoke-Engine @('-Mode', 'Restore', '-PackagePath', $package, '-PlanPath', $planPath, '-RollbackPath', $rollback, '-CancelPath', $cancelPath) 'restore'
Assert ($result.code -eq 0 -and $result.log -match '"event":"done","status":"succeeded"' -and $result.log -notmatch '"status":"failed"') 'Restore of every selected component succeeds'

Assert ((Get-ItemProperty -Path 'HKLM:\SOFTWARE\SICM-RestoreTest').Setting -eq 'Original') 'Registry value restored'
Assert ((Get-Content -LiteralPath (Join-Path $rollback 'registry\HKLM_SOFTWARE_SICM-RestoreTest.current.reg') -Raw -Encoding Unicode) -match 'Changed') 'Registry rollback holds the replaced value'
$minimum = ((net.exe accounts) | Where-Object { $_ -match '^Minimum password length' }) -replace '\D', ''
Assert ($minimum -eq '9') 'Local security policy restored (minimum password length)'
Assert ((Get-Content -LiteralPath (Join-Path $rollback 'secpol\Current-Local-Security-Policy.inf') -Raw) -match 'MinimumPasswordLength = 3') 'Security policy rollback holds the replaced policy'
Assert (Test-Path -LiteralPath ('Cert:\LocalMachine\CA\' + $thumbprint)) 'Certificate restored to its original store'
Assert ([bool](Get-NetFirewallRule -DisplayName 'SICM Restore Test' -ErrorAction SilentlyContinue)) 'Firewall rule restored'
Assert (Test-Path -LiteralPath (Join-Path $rollback 'firewall\Current-Windows-Firewall-Policy.wfw')) 'Firewall rollback policy saved'
Assert (((icacls.exe (Join-Path $share 'Reports\Q1.txt')) -join ' ') -match 'Guests') 'Recursive NTFS permission restored on a nested file'
Assert (@(Get-ChildItem -LiteralPath (Join-Path $rollback 'acls') -Filter '*.current.acl').Count -eq 1) 'Permission rollback saved'
$task = Get-ScheduledTask -TaskName 'Nightly-Check' -TaskPath '\SICM-Restore\' -ErrorAction SilentlyContinue
Assert ([bool]$task -and $task.Actions[0].Execute -eq 'cmd.exe') 'Scheduled task restored'
Assert (Test-Path -LiteralPath (Join-Path $rollback 'tasks\SICM-Restore_Nightly-Check.did-not-exist.txt')) 'Task rollback records that it was re-created'
if (-not $SkipRoles) {
    Assert ([bool](Get-Printer -Name 'SICM Restore Printer' -ErrorAction SilentlyContinue)) 'Printer restored by PrintBRM'
    Assert (Test-Path -LiteralPath (Join-Path $rollback 'printers\Current-Printers.printerExport')) 'Printer rollback export saved'
    Assert (((& $appCmd list apppool /name:SICM-Restore-Pool) -join ' ') -match 'SICM-Restore-Pool') 'IIS application pool restored'
    Assert ((Get-Service W3SVC).Status -eq 'Running') 'IIS running after restore'
    Assert (Test-Path -LiteralPath (Join-Path $rollback 'iis\Current-IIS-Configuration\applicationHost.config')) 'IIS rollback configuration saved'
    Assert ([bool](Get-DhcpServerv4Scope -ScopeId 10.250.0.0 -ErrorAction SilentlyContinue)) 'DHCP scope restored'
    Assert ([string]((Get-DhcpServerv4OptionValue -ComputerName $computer -OptionId 6 -ErrorAction SilentlyContinue).Value) -eq '192.0.2.53') 'DHCP server-level option restored'
    Assert ([bool](Get-DhcpServerv4Class -ComputerName $computer -Name 'SICM Restore Class' -ErrorAction SilentlyContinue)) 'DHCP user class restored'
    Assert (Test-Path -LiteralPath (Join-Path $rollback 'dhcp\Current-DHCP-Configuration-and-Leases.xml')) 'DHCP rollback export saved'
    Assert ([bool](Get-NpsRadiusClient | Where-Object { $_.Name -eq 'SICM-Restore-Client' })) 'NPS RADIUS client restored'
    Assert (Test-Path -LiteralPath (Join-Path $rollback 'nps\Current-NPS-CONFIG-SENSITIVE.xml')) 'NPS rollback export saved'
    Assert ([bool](Get-DnsServerResourceRecord -ZoneName $zone -Name 'app01' -RRType A -ErrorAction SilentlyContinue)) 'DNS zone and record restored'
}
Assert (Test-Path -LiteralPath (Join-Path $rollback 'README.txt')) 'Rollback folder explains its contents'

# Restoring again finds already-present certificates and zones and does not
# offer them; the rest restores again idempotently.
$result = Invoke-Engine @('-Mode', 'Scan', '-PackagePath', $package, '-ResultPath', $scanPath) 'rescan'
$scan = Get-Content -LiteralPath $scanPath -Raw | ConvertFrom-Json
$certificates = @($scan.components | Where-Object { $_.id -eq 'certificates' })[0]
Assert (-not $certificates.available -and $certificates.items[0].blockedReason -match 'Already installed') 'Installed certificates are not offered again'

# Clean up the runner state this script created.
Remove-Item -LiteralPath ('Cert:\LocalMachine\CA\' + $thumbprint) -ErrorAction SilentlyContinue
Remove-NetFirewallRule -DisplayName 'SICM Restore Test' -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName 'Nightly-Check' -TaskPath '\SICM-Restore\' -Confirm:$false -ErrorAction SilentlyContinue
& reg.exe delete $regKey /f | Out-Null
Write-Host ('Restore validation passed: {0} checks.' -f $checks.Count)
$checks | Set-Content -LiteralPath (Join-Path $OutputDirectory 'checks.txt') -Encoding UTF8
