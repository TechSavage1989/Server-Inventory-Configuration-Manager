param(
    [Parameter(Mandatory = $true)][ValidateSet('ReadOld', 'ReadNew', 'Step', 'Rollback')][string]$Mode,
    [Parameter(Mandatory = $true)][string]$ResultPath
)

# Domain controller migration engine (dcmig_windows.go). It builds a new
# domain controller beside an old one, moves the FSMO roles, repoints DNS
# and DHCP, demotes the old DC and moves its IP address, from the computer
# running Server Inventory and Migration Manager.
#
# Everything arrives as one JSON line on standard input (never a command
# line or a file): the server names, the settings and the accounts (the
# Domain Admin account, the Directory Services Restore Mode password, the
# old server's new local Administrator password and, for joining a
# workgroup server, its local account). Passwords are never logged.
#
# Every server is reached with Invoke-Command over WinRM and runs only the
# in-box AD DS deployment, ActiveDirectory, DnsServer, DhcpServer and
# networking cmdlets, dcdiag, repadmin and w32tm. Promotion, role moves and
# demotion pass the typed account to the cmdlet's own -Credential, so no
# credential delegation (CredSSP) is ever needed. Restarts use
# Restart-Computer -Wait from this computer. Nothing is copied to or
# installed on any server, and no service, task, WMI process start or
# encoded command is used. A domain controller is never copied or cloned.
# Windows PowerShell 3.0 or later.

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }

function Write-Line { param([string]$Text) [Console]::Out.WriteLine($Text); [Console]::Out.Flush() }
function Write-Log { param([string]$Message) Write-Line ('{0} [INFO] {1}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Message) }
function Write-Result {
    param($Value)
    $json = ConvertTo-Json -InputObject $Value -Depth 10 -Compress
    [IO.File]::WriteAllText($ResultPath, $json, (New-Object System.Text.UTF8Encoding($false)))
}

function New-Credential {
    param([string]$User, [string]$Password)
    $secure = New-Object System.Security.SecureString
    foreach ($c in $Password.ToCharArray()) { $secure.AppendChar($c) }
    $secure.MakeReadOnly()
    return New-Object System.Management.Automation.PSCredential($User, $secure)
}

function New-Secure {
    param([string]$Text)
    $secure = New-Object System.Security.SecureString
    foreach ($c in $Text.ToCharArray()) { $secure.AppendChar($c) }
    $secure.MakeReadOnly()
    return $secure
}

# Shared by the remote blocks: the computer's IPv4 adapters, read through
# WMI so Windows Server 2008 R2 answers too.
$addressReader = @'
function Get-SimmAddresses {
    $out = New-Object System.Collections.ArrayList
    foreach ($a in @(Get-WmiObject -Class Win32_NetworkAdapterConfiguration -Filter 'IPEnabled=TRUE')) {
        $ips = @($a.IPAddress); $masks = @($a.IPSubnet)
        for ($i = 0; $i -lt $ips.Count; $i++) {
            if ([string]$ips[$i] -notmatch '^\d+\.\d+\.\d+\.\d+$') { continue }
            $prefix = 0
            try {
                foreach ($octet in ([string]$masks[$i]).Split('.')) { $prefix += ([Convert]::ToString([int]$octet, 2) -replace '0', '').Length }
            } catch { }
            $gateway = ''
            foreach ($g in @($a.DefaultIPGateway)) { if ([string]$g -match '^\d+\.\d+\.\d+\.\d+$') { $gateway = [string]$g; break } }
            [void]$out.Add(@{ interface = [string]$a.Description; index = [int]$a.InterfaceIndex; address = [string]$ips[$i]; prefix = $prefix; gateway = $gateway; dns = @($a.DNSServerSearchOrder | ForEach-Object { [string]$_ }); dhcp = [bool]$a.DHCPEnabled })
        }
    }
    return @($out)
}
function Get-SimmReg {
    param([string]$Path, [string]$Name)
    try { $item = Get-ItemProperty -LiteralPath $Path -Name $Name -ErrorAction Stop; return $item.$Name } catch { return $null }
}
function Invoke-SimmNative {
    param([string]$File, [string[]]$Arguments)
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { return @(& $File @Arguments 2>&1 | ForEach-Object { [string]$_ }) }
    finally { $ErrorActionPreference = $previous }
}
'@

# Reads a domain controller and the domain through it. Read only.
$readOldBlock = {
    param([string]$Helpers)
    $ErrorActionPreference = 'Stop'
    . ([ScriptBlock]::Create($Helpers))
    $cs = Get-WmiObject -Class Win32_ComputerSystem
    $os = Get-WmiObject -Class Win32_OperatingSystem
    $out = @{ name = [string]$env:COMPUTERNAME; os = [string]$os.Caption; osVersion = [string]$os.Version; addresses = @(Get-SimmAddresses) }
    if ([int]$cs.DomainRole -lt 4) { return (ConvertTo-Json -InputObject $out -Depth 6 -Compress) }
    Import-Module ActiveDirectory -ErrorAction Stop
    $dc = Get-ADDomainController -Identity $env:COMPUTERNAME -Server localhost
    $domain = Get-ADDomain -Server localhost
    $forest = Get-ADForest -Server localhost
    $out.hostName = [string]$dc.HostName
    $out.domain = [string]$domain.DNSRoot
    $out.netbios = [string]$domain.NetBIOSName
    $out.forest = [string]$forest.Name
    $out.domainMode = [string]$domain.DomainMode
    $out.forestMode = [string]$forest.ForestMode
    $out.site = [string]$dc.Site
    $out.sites = @($forest.Sites | ForEach-Object { [string]$_ })
    $out.globalCatalog = [bool]$dc.IsGlobalCatalog
    $out.readOnly = [bool]$dc.IsReadOnly
    $out.roles = @($dc.OperationMasterRoles | ForEach-Object { [string]$_ })
    $out.roleOwners = @{
        SchemaMaster = [string]$forest.SchemaMaster; DomainNamingMaster = [string]$forest.DomainNamingMaster
        PDCEmulator = [string]$domain.PDCEmulator; RIDMaster = [string]$domain.RIDMaster; InfrastructureMaster = [string]$domain.InfrastructureMaster
    }
    $out.dcs = @(Get-ADDomainController -Filter * -Server localhost | ForEach-Object {
            @{ name = [string]$_.Name; hostName = [string]$_.HostName; site = [string]$_.Site; address = [string]$_.IPv4Address; globalCatalog = [bool]$_.IsGlobalCatalog; readOnly = [bool]$_.IsReadOnly; os = [string]$_.OperatingSystem }
        })
    $features = @{}
    try { foreach ($f in @(Get-WindowsFeature)) { if ($f.Installed) { $features[[string]$f.Name] = $f } } } catch { }
    $out.dnsServer = [bool]$features['DNS']
    $out.dhcpServer = [bool]$features['DHCP']
    $skip = @('AD-Domain-Services', 'DNS', 'FileAndStorage-Services', 'File-Services')
    $out.otherRoles = @($features.Values | Where-Object { $_.FeatureType -eq 'Role' -and $skip -notcontains [string]$_.Name } | ForEach-Object { [string]$_.DisplayName })
    if ($out.dnsServer) {
        try { $out.forwarders = @((Get-DnsServerForwarder).IPAddress | ForEach-Object { $_.IPAddressToString }) } catch { $out.forwarders = @() }
        try { $out.zones = @(Get-DnsServerZone | Where-Object { $_.IsDsIntegrated -and -not $_.IsAutoCreated } | ForEach-Object { [string]$_.ZoneName }) } catch { $out.zones = @() }
    }
    $out.ntpServer = [string](Get-SimmReg 'HKLM:\SYSTEM\CurrentControlSet\Services\W32Time\Parameters' 'NtpServer')
    $out.timeType = [string](Get-SimmReg 'HKLM:\SYSTEM\CurrentControlSet\Services\W32Time\Parameters' 'Type')
    $out.databasePath = [string](Get-SimmReg 'HKLM:\SYSTEM\CurrentControlSet\Services\NTDS\Parameters' 'DSA Working Directory')
    $out.logPath = [string](Get-SimmReg 'HKLM:\SYSTEM\CurrentControlSet\Services\NTDS\Parameters' 'Database log files path')
    $sysvol = [string](Get-SimmReg 'HKLM:\SYSTEM\CurrentControlSet\Services\Netlogon\Parameters' 'SysVol')
    if ($sysvol) { $out.sysvolPath = Split-Path -Parent $sysvol }
    # SYSVOL replication: the DFSR "Domain System Volume" group exists and
    # the FRS-to-DFSR migration (if any) has ended.
    $out.sysvol = 'FRS'
    try {
        $group = @(Get-ADObject -Server localhost -SearchBase ('CN=DFSR-GlobalSettings,CN=System,' + $domain.DistinguishedName) -Filter "objectClass -eq 'msDFSR-ReplicationGroup' -and name -eq 'Domain System Volume'")
        if ($group.Count -gt 0) {
            $settings = Get-ADObject -Server localhost -Identity ('CN=DFSR-GlobalSettings,CN=System,' + $domain.DistinguishedName) -Properties 'msDFSR-Flags'
            $flags = [int]$settings.'msDFSR-Flags'
            if ($flags -eq 0 -or $flags -eq 48) { $out.sysvol = 'DFSR' }
        }
    }
    catch { }
    try {
        $config = (Get-ADRootDSE -Server localhost).configurationNamingContext
        $out.dhcpServers = @(Get-ADObject -Server localhost -SearchBase ('CN=NetServices,CN=Services,' + $config) -Filter "objectClass -eq 'dHCPClass'" | Where-Object { $_.Name -ne 'DhcpRoot' } | ForEach-Object { [string]$_.Name })
    }
    catch { $out.dhcpServers = @() }
    return (ConvertTo-Json -InputObject $out -Depth 6 -Compress)
}

# Reads the new server. Read only.
$readNewBlock = {
    param([string]$Helpers)
    $ErrorActionPreference = 'Stop'
    . ([ScriptBlock]::Create($Helpers))
    $cs = Get-WmiObject -Class Win32_ComputerSystem
    $os = Get-WmiObject -Class Win32_OperatingSystem
    $out = @{ name = [string]$env:COMPUTERNAME; os = [string]$os.Caption; osVersion = [string]$os.Version; domainJoined = [bool]$cs.PartOfDomain; isDC = ([int]$cs.DomainRole -ge 4); addresses = @(Get-SimmAddresses) }
    if ($cs.PartOfDomain) { $out.domain = [string]$cs.Domain; $out.hostName = ([string]$env:COMPUTERNAME + '.' + [string]$cs.Domain) }
    try {
        $out.addsInstalled = [bool](Get-WindowsFeature -Name 'AD-Domain-Services').Installed
        $out.dnsInstalled = [bool](Get-WindowsFeature -Name 'DNS').Installed
    }
    catch { }
    $pending = (Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending') -or (Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired')
    $active = [string](Get-SimmReg 'HKLM:\SYSTEM\CurrentControlSet\Control\ComputerName\ActiveComputerName' 'ComputerName')
    $wanted = [string](Get-SimmReg 'HKLM:\SYSTEM\CurrentControlSet\Control\ComputerName\ComputerName' 'ComputerName')
    if ($active -and $wanted -and $active -ne $wanted) { $pending = $true }
    $out.pendingReboot = [bool]$pending
    $drive = Get-WmiObject -Class Win32_LogicalDisk -Filter ("DeviceID='" + $env:SystemDrive + "'")
    if ($drive) { $out.freeGB = [math]::Round([double]$drive.FreeSpace / 1GB, 1) }
    return (ConvertTo-Json -InputObject $out -Depth 6 -Compress)
}

# Is the new DC working yet? Read only.
$verifyBlock = {
    param([string]$Helpers)
    $ErrorActionPreference = 'Stop'
    . ([ScriptBlock]::Create($Helpers))
    $waiting = New-Object System.Collections.ArrayList
    $cs = Get-WmiObject -Class Win32_ComputerSystem
    if ([int]$cs.DomainRole -lt 4) { [void]$waiting.Add('Windows does not report this server as a domain controller yet') }
    if ([int](Get-SimmReg 'HKLM:\SYSTEM\CurrentControlSet\Services\Netlogon\Parameters' 'SysvolReady') -ne 1) { [void]$waiting.Add('SYSVOL is not ready (the first replication has not finished)') }
    $shares = @(Get-WmiObject -Class Win32_Share | ForEach-Object { [string]$_.Name })
    foreach ($name in @('SYSVOL', 'NETLOGON')) { if ($shares -notcontains $name) { [void]$waiting.Add('the ' + $name + ' share does not exist yet') } }
    $lines = Invoke-SimmNative 'dcdiag.exe' @('/s:' + $env:COMPUTERNAME, '/test:Advertising')
    if (-not ($lines | Where-Object { $_ -match 'passed test Advertising' })) {
        if ($lines | Where-Object { $_ -match 'failed test Advertising' }) { [void]$waiting.Add('dcdiag: the DC is not advertising yet') }
    }
    $rows = @(Invoke-SimmNative 'repadmin.exe' @('/showrepl', 'localhost', '/csv') | Where-Object { $_ -match '^showrepl_' -and $_ -notmatch '^showrepl_COLUMNS' } | ConvertFrom-Csv -Header (0..10 | ForEach-Object { 'c' + $_ }))
    $ok = 0
    foreach ($row in $rows) {
        $failures = 0
        [void][int]::TryParse([string]$row.c7, [ref]$failures)
        if ($failures -gt 0) { [void]$waiting.Add('replication from ' + [string]$row.c5 + ' of ' + [string]$row.c3 + ' is failing (' + [string]$row.c10 + ')') }
        elseif ([string]$row.c9 -and [string]$row.c9 -ne '0') { $ok++ }
    }
    if ($rows.Count -eq 0) { [void]$waiting.Add('no replication links yet') }
    return (ConvertTo-Json -InputObject @{ ready = ($waiting.Count -eq 0); waiting = @($waiting); links = $rows.Count; working = $ok } -Compress)
}

function Read-Request {
    $line = [Console]::In.ReadLine()
    if (-not $line) { throw 'No request was given on standard input.' }
    return (ConvertFrom-Json -InputObject $line)
}

# Runs a block on a computer with the domain account (or this computer's
# sign-in when none was typed).
function Invoke-On {
    param([string]$Computer, [ScriptBlock]$Block, [object[]]$Arguments, $Credential)
    if ($Credential) { return Invoke-Command -ComputerName $Computer -Credential $Credential -ScriptBlock $Block -ArgumentList $Arguments }
    return Invoke-Command -ComputerName $Computer -ScriptBlock $Block -ArgumentList $Arguments
}

function Restart-AndWait {
    param([string]$Computer, $Credential, [int]$Seconds)
    Write-Log ('Restarting ' + $Computer + ' and waiting for it to answer again.')
    if ($Credential) { Restart-Computer -ComputerName $Computer -Credential $Credential -Wait -For WinRM -Timeout $Seconds -Delay 15 -Force }
    else { Restart-Computer -ComputerName $Computer -Wait -For WinRM -Timeout $Seconds -Delay 15 -Force }
    Write-Log ($Computer + ' is back.')
}

$roleNames = @{ SchemaMaster = 'Schema master'; DomainNamingMaster = 'Domain naming master'; PDCEmulator = 'PDC emulator'; RIDMaster = 'RID master'; InfrastructureMaster = 'Infrastructure master' }

try {
    $request = Read-Request
    $credential = $null
    if ([string]$request.user) { $credential = New-Credential ([string]$request.user) ([string]$request.password) }
    $source = [string]$request.source
    $target = [string]$request.target
    $result = @{ ok = $true; notes = @(); changes = @(); rolesMoved = @(); restored = @() }
    $notes = New-Object System.Collections.ArrayList
    $changes = New-Object System.Collections.ArrayList

    switch ($Mode) {
        'ReadOld' {
            Write-Log ('Reading ' + $source + ' (read only).')
            $text = Invoke-On $source $readOldBlock @($addressReader) $credential
            $result.old = ConvertFrom-Json -InputObject ([string]$text)
        }
        'ReadNew' {
            Write-Log ('Reading ' + $target + ' (read only).')
            $text = $null
            try { $text = Invoke-On $target $readNewBlock @($addressReader) $credential }
            catch {
                if (-not [string]$request.joinUser) { throw }
                # A workgroup server answers only its own local account.
                $local = New-Credential ([string]$request.joinUser) ([string]$request.joinPassword)
                $text = Invoke-Command -ComputerName $target -Credential $local -ScriptBlock $readNewBlock -ArgumentList @($addressReader)
            }
            $result.new = ConvertFrom-Json -InputObject ([string]$text)
        }
        'Step' {
            $s = $request.settings
            $step = [string]$request.step
            switch ($step) {
                'join' {
                    $local = $credential
                    if ([string]$request.joinUser) { $local = New-Credential ([string]$request.joinUser) ([string]$request.joinPassword) }
                    Write-Log ('Joining ' + $target + ' to ' + [string]$request.domain + '.')
                    Invoke-Command -ComputerName $target -Credential $local -ArgumentList @([string]$request.domain, [string]$request.user, [string]$request.password) -ScriptBlock {
                        param([string]$Domain, [string]$User, [string]$Password)
                        $ErrorActionPreference = 'Stop'
                        $cs = Get-WmiObject -Class Win32_ComputerSystem
                        if ($cs.PartOfDomain -and [string]$cs.Domain -eq $Domain) { return }
                        if ($cs.PartOfDomain) { throw ('This server is joined to ' + [string]$cs.Domain + '.') }
                        $secure = New-Object System.Security.SecureString
                        foreach ($c in $Password.ToCharArray()) { $secure.AppendChar($c) }
                        Add-Computer -DomainName $Domain -Credential (New-Object System.Management.Automation.PSCredential($User, $secure)) -ErrorAction Stop
                    } | Out-Null
                    Restart-AndWait $target $local 1800
                    [void]$notes.Add($target + ' joined ' + [string]$request.domain + ' and restarted.')
                }
                'install' {
                    $names = @('AD-Domain-Services')
                    if ($s.installDns) { $names += 'DNS' }
                    Write-Log ('Adding ' + ($names -join ' and ') + ' on ' + $target + '.')
                    $restart = Invoke-On $target {
                        param([string[]]$Names)
                        $ErrorActionPreference = 'Stop'
                        $r = Install-WindowsFeature -Name $Names -IncludeManagementTools
                        if (-not $r.Success) { throw ('Install-WindowsFeature did not succeed: ' + [string]$r.ExitCode) }
                        return [string]$r.RestartNeeded
                    } @(, $names) $credential
                    if ([string]$restart -eq 'Yes') { Restart-AndWait $target $credential 1800 }
                    [void]$notes.Add(($names -join ' and ') + ' added on ' + $target + '.')
                }
                'promote' {
                    if (-not $credential) { throw 'Promotion needs a Domain Admin account typed in this tool.' }
                    if (-not [string]$request.dsrm) { throw 'Type the Directory Services Restore Mode password for the new DC.' }
                    Write-Log ('Checking that ' + $target + ' can be promoted (Test-ADDSDomainControllerInstallation).')
                    $args2 = @([string]$request.domain, [string]$request.user, [string]$request.password, [string]$request.dsrm, [string]$s.site, [bool]$s.installDns, [bool]$s.globalCatalog, [string]$s.replicateFrom, [string]$s.databasePath, [string]$s.logPath, [string]$s.sysvolPath)
                    Invoke-On $target {
                        param([string]$Domain, [string]$User, [string]$Password, [string]$Dsrm, [string]$Site, [bool]$Dns, [bool]$GC, [string]$From, [string]$Db, [string]$Log, [string]$Sysvol)
                        $ErrorActionPreference = 'Stop'
                        function ToSecure([string]$t) { $x = New-Object System.Security.SecureString; foreach ($c in $t.ToCharArray()) { $x.AppendChar($c) }; $x.MakeReadOnly(); return $x }
                        Import-Module ADDSDeployment -ErrorAction Stop
                        $p = @{
                            DomainName = $Domain; Credential = (New-Object System.Management.Automation.PSCredential($User, (ToSecure $Password)))
                            SafeModeAdministratorPassword = (ToSecure $Dsrm); SiteName = $Site; InstallDns = $Dns; NoGlobalCatalog = (-not $GC)
                            DatabasePath = $Db; LogPath = $Log; SysvolPath = $Sysvol; NoRebootOnCompletion = $true; Force = $true
                        }
                        if ($From) { $p.ReplicationSourceDC = $From }
                        $test = Test-ADDSDomainControllerInstallation @p -WarningAction SilentlyContinue
                        if ([string]$test.Status -ne 'Success') { throw ('The prerequisites check failed: ' + [string]$test.Message) }
                        $r = Install-ADDSDomainController @p -WarningAction SilentlyContinue -Confirm:$false
                        if ($r -and [string]$r.Status -ne 'Success') { throw ('Promotion did not succeed: ' + [string]$r.Message) }
                    } $args2 $credential | Out-Null
                    Restart-AndWait $target $credential 3600
                    [void]$notes.Add($target + ' was promoted to a domain controller of ' + [string]$request.domain + ' in site ' + [string]$s.site + ' and restarted.')
                    if (@($s.forwarders).Count -gt 0 -and $s.installDns) {
                        try {
                            Invoke-On $target { param([string[]]$F) Set-DnsServerForwarder -IPAddress $F -ErrorAction Stop } @(, [string[]]@($s.forwarders)) $credential | Out-Null
                            [void]$notes.Add('DNS forwarders set: ' + (@($s.forwarders) -join ', ') + '.')
                        }
                        catch { [void]$notes.Add('The DNS forwarders could not be set: ' + $_.Exception.Message) }
                    }
                }
                'verify' {
                    $text = Invoke-On $target $verifyBlock @($addressReader) $credential
                    $v = ConvertFrom-Json -InputObject ([string]$text)
                    $result.ready = [bool]$v.ready
                    $result.waiting = (@($v.waiting) -join '; ')
                    if ($v.ready) { [void]$notes.Add($target + ' advertises as a DC, SYSVOL and NETLOGON are shared and ' + [string]$v.working + ' replication link(s) work.') }
                }
                'roles' {
                    if (-not $credential) { throw 'Moving the roles needs a Domain Admin account typed in this tool.' }
                    $roles = [string[]]@($request.roles)
                    $seize = [bool]$s.oldDead
                    $verb = 'Moving'; if ($seize) { $verb = 'Seizing' }
                    Write-Log ($verb + ' ' + (($roles | ForEach-Object { $roleNames[$_] }) -join ', ') + ' to ' + $target + '.')
                    $owners = Invoke-On $target {
                        param([string[]]$Roles, [bool]$Seize, [string]$User, [string]$Password)
                        $ErrorActionPreference = 'Stop'
                        Import-Module ActiveDirectory -ErrorAction Stop
                        $x = New-Object System.Security.SecureString; foreach ($c in $Password.ToCharArray()) { $x.AppendChar($c) }
                        $cred = New-Object System.Management.Automation.PSCredential($User, $x)
                        $p = @{ Identity = $env:COMPUTERNAME; OperationMasterRole = $Roles; Credential = $cred; Server = 'localhost'; Confirm = $false }
                        if ($Seize) { $p.Force = $true }
                        Move-ADDirectoryServerOperationMasterRole @p
                        $d = Get-ADDomain -Server localhost; $f = Get-ADForest -Server localhost
                        return (ConvertTo-Json -Compress -InputObject @{ SchemaMaster = [string]$f.SchemaMaster; DomainNamingMaster = [string]$f.DomainNamingMaster; PDCEmulator = [string]$d.PDCEmulator; RIDMaster = [string]$d.RIDMaster; InfrastructureMaster = [string]$d.InfrastructureMaster })
                    } @(, $roles + @($seize, [string]$request.user, [string]$request.password)) $credential
                    $ownerMap = ConvertFrom-Json -InputObject ([string]$owners)
                    $result.roleOwners = $ownerMap
                    $moved = New-Object System.Collections.ArrayList
                    foreach ($role in $roles) {
                        $owner = [string]$ownerMap.$role
                        if ($owner -and ($owner.Split('.')[0] -eq $target.Split('.')[0])) { [void]$moved.Add($role) }
                    }
                    $result.rolesMoved = @($moved)
                    if ($moved.Count -ne $roles.Count) { throw ('Only ' + $moved.Count + ' of ' + $roles.Count + ' roles are on ' + $target + ' now.') }
                    [void]$notes.Add((($roles | ForEach-Object { $roleNames[$_] }) -join ', ') + ' now on ' + $target + '.')
                    if ($roles -contains 'PDCEmulator') {
                        # Time: the new PDC takes time from outside, the old DC
                        # from the domain. The old values are recorded first.
                        $ntp = [string]$s.ntpServer
                        $timeArgs = @($ntp)
                        $before = Invoke-On $target {
                            param([string]$Ntp)
                            $ErrorActionPreference = 'Stop'
                            $k = 'HKLM:\SYSTEM\CurrentControlSet\Services\W32Time\Parameters'
                            $old = @([string](Get-ItemProperty $k).NtpServer, [string](Get-ItemProperty $k).Type)
                            if ($Ntp) {
                                & w32tm.exe /config ('/manualpeerlist:' + $Ntp) /syncfromflags:manual /reliable:yes /update | Out-Null
                                & w32tm.exe /resync /nowait | Out-Null
                            }
                            return ($old -join '|')
                        } $timeArgs $credential
                        if ($ntp) { [void]$changes.Add(@{ kind = 'time'; server = $target; key = 'pdc'; before = @(([string]$before).Split('|')); after = @($ntp, 'NTP') }) }
                        if (-not $seize) {
                            try {
                                $beforeOld = Invoke-On $source {
                                    $ErrorActionPreference = 'Stop'
                                    $k = 'HKLM:\SYSTEM\CurrentControlSet\Services\W32Time\Parameters'
                                    $old = @([string](Get-ItemProperty $k).NtpServer, [string](Get-ItemProperty $k).Type)
                                    & w32tm.exe /config /syncfromflags:domhier /reliable:no /update | Out-Null
                                    return ($old -join '|')
                                } @() $credential
                                [void]$changes.Add(@{ kind = 'time'; server = $source; key = 'old'; before = @(([string]$beforeOld).Split('|')); after = @('', 'NT5DS') })
                            }
                            catch { [void]$notes.Add('The old DC still takes time from outside: ' + $_.Exception.Message) }
                        }
                        [void]$notes.Add('Time: ' + $target + ' takes time from ' + $ntp + '.')
                    }
                }
                'repoint' {
                    $from = [string]$request.oldAddress
                    $to = [string]$request.newAddress
                    if (-not $from -or -not $to) { throw 'Both DCs addresses are needed to repoint.' }
                    foreach ($server in @($request.dhcpServers)) {
                        $server = [string]$server
                        try {
                            $text = Invoke-On $server {
                                param([string]$From, [string]$To)
                                $ErrorActionPreference = 'Stop'
                                $out = New-Object System.Collections.ArrayList
                                function Swap([string[]]$List) { $r = @(); $seen = @{}; foreach ($v in $List) { if ($v -eq $From) { $v = $To }; if ($v -and -not $seen[$v]) { $seen[$v] = $true; $r += $v } }; return , $r }
                                $o = Get-DhcpServerv4OptionValue -OptionId 6 -ErrorAction SilentlyContinue
                                if ($o -and @($o.Value) -contains $From) {
                                    $new = Swap @($o.Value)
                                    Set-DhcpServerv4OptionValue -OptionId 6 -Value $new -Force
                                    [void]$out.Add(@{ kind = 'dhcp-server'; key = ''; before = @($o.Value); after = $new })
                                }
                                foreach ($scope in @(Get-DhcpServerv4Scope)) {
                                    $o = Get-DhcpServerv4OptionValue -ScopeId $scope.ScopeId -OptionId 6 -ErrorAction SilentlyContinue
                                    if ($o -and @($o.Value) -contains $From) {
                                        $new = Swap @($o.Value)
                                        Set-DhcpServerv4OptionValue -ScopeId $scope.ScopeId -OptionId 6 -Value $new -Force
                                        [void]$out.Add(@{ kind = 'dhcp-scope'; key = [string]$scope.ScopeId; before = @($o.Value); after = $new })
                                    }
                                }
                                return (ConvertTo-Json -InputObject @($out) -Depth 4 -Compress)
                            } @($from, $to) $credential
                            foreach ($c in @(ConvertFrom-Json -InputObject ([string]$text) | ForEach-Object { $_ })) {
                                if (-not $c) { continue }
                                [void]$changes.Add(@{ kind = [string]$c.kind; server = $server; key = [string]$c.key; before = @($c.before); after = @($c.after) })
                            }
                            [void]$notes.Add('DHCP server ' + $server + ' checked.')
                        }
                        catch { [void]$notes.Add('DHCP server ' + $server + ' was not changed: ' + $_.Exception.Message) }
                    }
                    foreach ($server in @($request.dnsClients)) {
                        $server = [string]$server
                        try {
                            $text = Invoke-On $server {
                                param([string]$From, [string]$To)
                                $ErrorActionPreference = 'Stop'
                                $out = New-Object System.Collections.ArrayList
                                foreach ($a in @(Get-WmiObject -Class Win32_NetworkAdapterConfiguration -Filter 'IPEnabled=TRUE')) {
                                    $list = @($a.DNSServerSearchOrder | ForEach-Object { [string]$_ })
                                    if ($list -notcontains $From) { continue }
                                    $new = @(); $seen = @{}
                                    foreach ($v in $list) { if ($v -eq $From) { $v = $To }; if ($v -and -not $seen[$v]) { $seen[$v] = $true; $new += $v } }
                                    $r = $a.SetDNSServerSearchOrder($new)
                                    if ([int]$r.ReturnValue -ne 0) { throw ('SetDNSServerSearchOrder returned ' + [int]$r.ReturnValue) }
                                    [void]$out.Add(@{ key = [string]$a.InterfaceIndex; before = $list; after = $new })
                                }
                                return (ConvertTo-Json -InputObject @($out) -Depth 4 -Compress)
                            } @($from, $to) $credential
                            foreach ($c in @(ConvertFrom-Json -InputObject ([string]$text) | ForEach-Object { $_ })) {
                                if (-not $c) { continue }
                                [void]$changes.Add(@{ kind = 'dns-client'; server = $server; key = [string]$c.key; before = @($c.before); after = @($c.after) })
                            }
                        }
                        catch { [void]$notes.Add($server + ' DNS settings were not changed: ' + $_.Exception.Message) }
                    }
                    [void]$notes.Add([string]$changes.Count + ' setting(s) now point at ' + $to + ' instead of ' + $from + '.')
                }
                'demote' {
                    if (-not $credential) { throw 'Demotion needs a Domain Admin account typed in this tool.' }
                    if ($s.oldDead) {
                        # The old DC no longer works: remove what is left of it
                        # from the directory, on the new DC.
                        Write-Log ('Removing ' + $source + ' from the directory (metadata cleanup) on ' + $target + '.')
                        Invoke-On $target {
                            param([string]$Old, [string]$User, [string]$Password)
                            $ErrorActionPreference = 'Stop'
                            Import-Module ActiveDirectory -ErrorAction Stop
                            $short = $Old.Split('.')[0]
                            $dc = $null
                            try { $dc = Get-ADDomainController -Identity $short -Server localhost } catch { }
                            if ($dc) {
                                $x = New-Object System.Security.SecureString; foreach ($c in $Password.ToCharArray()) { $x.AppendChar($c) }
                                $cred = New-Object System.Management.Automation.PSCredential($User, $x)
                                $server = Get-ADObject -Server localhost -Identity $dc.ServerObjectDN
                                $ntds = Get-ADObject -Server localhost -Identity $dc.NTDSSettingsObjectDN
                                Remove-ADObject -Server localhost -Identity $ntds -Recursive -Confirm:$false -Credential $cred
                                Remove-ADObject -Server localhost -Identity $server -Recursive -Confirm:$false -Credential $cred
                            }
                        } @($source, [string]$request.user, [string]$request.password) $credential | Out-Null
                        [void]$notes.Add($source + ' was removed from the directory. Delete its computer account and its DNS records once you are sure it never comes back.')
                    }
                    else {
                        if (-not [string]$request.localAdmin) { throw 'Type the new local Administrator password for the old server; it becomes a member server.' }
                        Write-Log ('Demoting ' + $source + ' (Uninstall-ADDSDomainController).')
                        Invoke-On $source {
                            param([string]$User, [string]$Password, [string]$Local)
                            $ErrorActionPreference = 'Stop'
                            function ToSecure([string]$t) { $x = New-Object System.Security.SecureString; foreach ($c in $t.ToCharArray()) { $x.AppendChar($c) }; $x.MakeReadOnly(); return $x }
                            Import-Module ADDSDeployment -ErrorAction Stop
                            $p = @{ Credential = (New-Object System.Management.Automation.PSCredential($User, (ToSecure $Password))); LocalAdministratorPassword = (ToSecure $Local); DemoteOperationMasterRole = $false; NoRebootOnCompletion = $true; Force = $true }
                            $test = Test-ADDSDomainControllerUninstallation @p -WarningAction SilentlyContinue
                            if ([string]$test.Status -ne 'Success') { throw ('The demotion check failed: ' + [string]$test.Message) }
                            $r = Uninstall-ADDSDomainController @p -WarningAction SilentlyContinue -Confirm:$false
                            if ($r -and [string]$r.Status -ne 'Success') { throw ('Demotion did not succeed: ' + [string]$r.Message) }
                        } @([string]$request.user, [string]$request.password, [string]$request.localAdmin) $credential | Out-Null
                        Restart-AndWait $source $credential 3600
                        [void]$notes.Add($source + ' is now a member server of ' + [string]$request.domain + '. Its AD DS role is still installed; remove it in Server Manager when you no longer need it.')
                    }
                }
                'ipswap' {
                    $oldIP = [string]$request.oldAddress
                    $newIP = [string]$request.newAddress
                    $spare = [string]$s.oldNewAddress
                    $prefix = [int]$s.oldNewPrefix
                    $gateway = [string]$s.oldNewGateway
                    # 1. The old server moves to its new address. The session
                    # may drop when the old address goes; the change still
                    # completes on the server.
                    Write-Log ('Moving ' + $source + ' from ' + $oldIP + ' to ' + $spare + '.')
                    $move = {
                        param([string]$From, [string]$To, [int]$Prefix, [string]$Gateway, [string[]]$Dns)
                        $ErrorActionPreference = 'Stop'
                        $a = Get-NetIPAddress -AddressFamily IPv4 -IPAddress $From -ErrorAction Stop
                        $index = $a.InterfaceIndex
                        $p = @{ InterfaceIndex = $index; IPAddress = $To; PrefixLength = $Prefix }
                        New-NetIPAddress @p -SkipAsSource $false | Out-Null
                        if ($Gateway) {
                            $routes = @(Get-NetRoute -InterfaceIndex $index -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue)
                            if (-not ($routes | Where-Object { $_.NextHop -eq $Gateway })) { New-NetRoute -InterfaceIndex $index -DestinationPrefix '0.0.0.0/0' -NextHop $Gateway | Out-Null }
                        }
                        if ($Dns.Count -gt 0) { Set-DnsClientServerAddress -InterfaceIndex $index -ServerAddresses $Dns }
                        Start-Sleep -Seconds 2
                        Remove-NetIPAddress -InterfaceIndex $index -IPAddress $From -Confirm:$false
                        & ipconfig.exe /registerdns | Out-Null
                    }
                    try { Invoke-On $source $move @($oldIP, $spare, $prefix, $gateway, [string[]]@($newIP)) $credential | Out-Null }
                    catch { Write-Log ('The connection to ' + $source + ' ended while its address changed (expected): ' + $_.Exception.Message) }
                    $deadline = (Get-Date).AddMinutes(5)
                    $freed = $false
                    while ((Get-Date) -lt $deadline) {
                        if (-not (Test-Connection -ComputerName $oldIP -Count 1 -Quiet)) { $freed = $true; break }
                        Start-Sleep -Seconds 10
                    }
                    if (-not $freed) { throw ($oldIP + ' still answers, so the new DC did not take it. Check ' + $source + ' (now meant to be at ' + $spare + ').') }
                    [void]$changes.Add(@{ kind = 'address'; server = $source; key = 'old'; before = @($oldIP); after = @($spare) })
                    # 2. The new DC takes the old address, then drops its own.
                    Write-Log ($target + ' takes ' + $oldIP + ' and leaves ' + $newIP + '.')
                    $old = $request.oldAdapter
                    try { Invoke-On $target $move @($newIP, $oldIP, [int]$old.prefix, [string]$old.gateway, [string[]]@()) $credential | Out-Null }
                    catch { Write-Log ('The connection to ' + $target + ' ended while its address changed (expected): ' + $_.Exception.Message) }
                    Start-Sleep -Seconds 20
                    try { Clear-DnsClientCache } catch { }
                    $back = $false
                    $deadline = (Get-Date).AddMinutes(10)
                    while ((Get-Date) -lt $deadline) {
                        try {
                            Invoke-On $target {
                                $ErrorActionPreference = 'Stop'
                                & ipconfig.exe /registerdns | Out-Null
                                Restart-Service -Name Netlogon
                            } @() $credential | Out-Null
                            $back = $true
                            break
                        }
                        catch { Start-Sleep -Seconds 15; try { Clear-DnsClientCache } catch { } }
                    }
                    if (-not $back) { throw ($target + ' did not answer at its new address ' + $oldIP + ' within 10 minutes. Check its network settings at the console.') }
                    [void]$changes.Add(@{ kind = 'address'; server = $target; key = 'new'; before = @($newIP); after = @($oldIP) })
                    [void]$notes.Add($target + ' now answers at ' + $oldIP + ' and registered its DNS and DC records again; ' + $source + ' is at ' + $spare + '.')
                }
                default { throw ('Unknown step ' + $step + '.') }
            }
        }
        'Rollback' {
            if (-not $credential) { throw 'Roll back needs the Domain Admin account typed in this tool.' }
            $rb = $request.rollback
            $restored = New-Object System.Collections.ArrayList
            $i = -1
            foreach ($c in @($rb.changes)) {
                $i++
                if (-not $c) { continue }
                try {
                    switch ([string]$c.kind) {
                        'dhcp-server' {
                            Invoke-On ([string]$c.server) { param([string[]]$V) Set-DhcpServerv4OptionValue -OptionId 6 -Value $V -Force } @(, [string[]]@($c.before)) $credential | Out-Null
                        }
                        'dhcp-scope' {
                            Invoke-On ([string]$c.server) { param([string]$S, [string[]]$V) Set-DhcpServerv4OptionValue -ScopeId $S -OptionId 6 -Value $V -Force } @([string]$c.key, [string[]]@($c.before)) $credential | Out-Null
                        }
                        'dns-client' {
                            Invoke-On ([string]$c.server) {
                                param([int]$Index, [string[]]$V)
                                $a = Get-WmiObject -Class Win32_NetworkAdapterConfiguration -Filter ('InterfaceIndex=' + $Index)
                                $r = $a.SetDNSServerSearchOrder($V)
                                if ([int]$r.ReturnValue -ne 0) { throw ('SetDNSServerSearchOrder returned ' + [int]$r.ReturnValue) }
                            } @([int]$c.key, [string[]]@($c.before)) $credential | Out-Null
                        }
                        'time' {
                            Invoke-On ([string]$c.server) {
                                param([string]$Ntp, [string]$Type)
                                if ($Type -eq 'NTP' -and $Ntp) { & w32tm.exe /config ('/manualpeerlist:' + $Ntp) /syncfromflags:manual /reliable:yes /update | Out-Null }
                                else { & w32tm.exe /config /syncfromflags:domhier /reliable:no /update | Out-Null }
                            } @([string]@($c.before)[0], [string]@($c.before)[1]) $credential | Out-Null
                        }
                        default { throw ('Roll back does not undo ' + [string]$c.kind + ' changes.') }
                    }
                    [void]$restored.Add($i)
                    Write-Log ('Put back ' + [string]$c.kind + ' on ' + [string]$c.server + '.')
                }
                catch { [void]$notes.Add('Not put back on ' + [string]$c.server + ': ' + $_.Exception.Message) }
            }
            $result.restored = @($restored)
            $roles = [string[]]@($rb.roles)
            if ($roles.Count -gt 0) {
                Write-Log ('Moving ' + (($roles | ForEach-Object { $roleNames[$_] }) -join ', ') + ' back to ' + $source + '.')
                Invoke-On $source {
                    param([string[]]$Roles, [string]$User, [string]$Password)
                    $ErrorActionPreference = 'Stop'
                    Import-Module ActiveDirectory -ErrorAction Stop
                    $x = New-Object System.Security.SecureString; foreach ($c in $Password.ToCharArray()) { $x.AppendChar($c) }
                    Move-ADDirectoryServerOperationMasterRole -Identity $env:COMPUTERNAME -OperationMasterRole $Roles -Credential (New-Object System.Management.Automation.PSCredential($User, $x)) -Server localhost -Confirm:$false
                } @(, $roles + @([string]$request.user, [string]$request.password)) $credential | Out-Null
                $result.rolesMoved = $roles
                [void]$notes.Add('The roles are back on ' + $source + '.')
            }
            if ($rb.removeNew) {
                if (-not [string]$request.localAdmin) { throw 'Type a local Administrator password for the new server, which becomes a member server again.' }
                Write-Log ('Demoting the new DC ' + $target + '.')
                Invoke-On $target {
                    param([string]$User, [string]$Password, [string]$Local)
                    $ErrorActionPreference = 'Stop'
                    function ToSecure([string]$t) { $x = New-Object System.Security.SecureString; foreach ($c in $t.ToCharArray()) { $x.AppendChar($c) }; $x.MakeReadOnly(); return $x }
                    Import-Module ADDSDeployment -ErrorAction Stop
                    $r = Uninstall-ADDSDomainController -Credential (New-Object System.Management.Automation.PSCredential($User, (ToSecure $Password))) -LocalAdministratorPassword (ToSecure $Local) -DemoteOperationMasterRole:$false -NoRebootOnCompletion -Force -WarningAction SilentlyContinue -Confirm:$false
                    if ($r -and [string]$r.Status -ne 'Success') { throw ('Demotion did not succeed: ' + [string]$r.Message) }
                } @([string]$request.user, [string]$request.password, [string]$request.localAdmin) $credential | Out-Null
                Restart-AndWait $target $credential 3600
                [void]$notes.Add($target + ' is a member server again.')
            }
        }
    }
    $result.notes = @($notes)
    $result.changes = @($changes)
    $credential = $null
    $request = $null
    Write-Result $result
    exit 0
}
catch {
    $message = $_.Exception.Message
    Write-Log ('Failed: ' + $message)
    $partial = @{ ok = $false; error = $message }
    if ($notes) { $partial.notes = @($notes) }
    if ($changes) { $partial.changes = @($changes) }
    if ($result -and $result.rolesMoved) { $partial.rolesMoved = @($result.rolesMoved) }
    if ($result -and $result.restored) { $partial.restored = @($result.restored) }
    Write-Result $partial
    exit 1
}
