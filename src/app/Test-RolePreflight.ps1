param(
    [Parameter(Mandatory = $true)][ValidateSet('winrm', 'direct')][string]$Transport,
    [Parameter(Mandatory = $true)][string]$ResultPath,
    [string]$ComputerName = '',
    [string]$VMName = '',
    [string]$Checks = 'dc,sql',
    [switch]$CredentialFromInput
)

# Optional domain controller and SQL Server checks for readiness
# (preflight.go). Everything here only reads: nothing is installed, copied,
# started as a service or task, or changed on the server being checked.
#   winrm   the server over the network (Invoke-Command -ComputerName)
#   direct  a VM on this Hyper-V host through PowerShell Direct
#           (Invoke-Command -VMName)
# Only Invoke-Command is used: no WMI process start, scheduled task, service,
# credential delegation (CredSSP), encoded command, vssadmin or shadow copy.
# Inside the server it reads the registry, WMI classes and event logs, runs
# the read-only built-in tools dcdiag, repadmin /showrepl and w32tm /query,
# and runs SELECT queries against each local SQL Server instance with the
# signed-in account. The account arrives as one JSON line on standard input
# (never a command line) and is used only for this run; with no account the
# Windows account running this application is used.
# The result is written to -ResultPath as JSON. Windows PowerShell 3.0 or
# later.

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }

function Write-Line { param([string]$Text) [Console]::Out.WriteLine($Text); [Console]::Out.Flush() }
function Write-Log { param([string]$Message) Write-Line ('{0} [INFO] {1}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Message) }
function Write-Result {
    param($Value)
    $json = ConvertTo-Json -InputObject $Value -Depth 8 -Compress
    [IO.File]::WriteAllText($ResultPath, $json, (New-Object System.Text.UTF8Encoding($false)))
}

# Runs inside the server being checked. Returns the facts as JSON text.
$insideServer = {
    param([bool]$WantDC, [bool]$WantSQL)
    $ErrorActionPreference = 'Stop'
    function Get-RegValue {
        param([string]$Path, [string]$Name)
        try {
            $item = Get-ItemProperty -LiteralPath $Path -Name $Name -ErrorAction Stop
            return $item.$Name
        }
        catch { return $null }
    }
    function Invoke-Native {
        # Native tools write warnings to standard error; with Stop they would
        # end the run, so they run with Continue and only their text is kept.
        param([string]$File, [string[]]$Arguments)
        $previous = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        try { return @(& $File @Arguments 2>&1 | ForEach-Object { [string]$_ }) }
        finally { $ErrorActionPreference = $previous }
    }
    $out = @{ computer = [string]$env:COMPUTERNAME }
    $computerSystem = Get-WmiObject -Class Win32_ComputerSystem
    $operatingSystem = Get-WmiObject -Class Win32_OperatingSystem
    $out.os = [string]$operatingSystem.Caption
    $out.osVersion = [string]$operatingSystem.Version
    $out.domain = [string]$computerSystem.Domain

    if ($WantDC) {
        $dc = @{ checked = $true; isDC = ([int]$computerSystem.DomainRole -ge 4); errors = @() }
        $errors = New-Object System.Collections.ArrayList
        if ($dc.isDC) {
            $dc.site = [string](Get-RegValue 'HKLM:\SYSTEM\CurrentControlSet\Services\Netlogon\Parameters' 'DynamicSiteName')
            $dc.sysvolReady = ([int](Get-RegValue 'HKLM:\SYSTEM\CurrentControlSet\Services\Netlogon\Parameters' 'SysvolReady') -eq 1)
            $dc.dsaNotWritable = [int](Get-RegValue 'HKLM:\SYSTEM\CurrentControlSet\Services\NTDS\Parameters' 'Dsa Not Writable')
            try {
                $since = (Get-Date).AddDays(-30)
                $rollback = @(Get-WinEvent -FilterHashtable @{ LogName = 'Directory Service'; Id = 2095; StartTime = $since } -MaxEvents 1 -ErrorAction SilentlyContinue)
                $dc.usnRollbackEvent = ($rollback.Count -gt 0)
            }
            catch { $dc.usnRollbackEvent = $false }
            # Roles and global catalog from this DC's own directory (LDAP to
            # itself), so no other domain controller has to be reached.
            try {
                $root = [ADSI]'LDAP://localhost/RootDSE'
                $self = [string]$root.dsServiceName
                $domainNC = [string]$root.defaultNamingContext
                $configNC = [string]$root.configurationNamingContext
                $dc.forest = (([string]$root.rootDomainNamingContext) -replace '^DC=', '' -replace ',DC=', '.')
                $dc.domainName = ($domainNC -replace '^DC=', '' -replace ',DC=', '.')
                $dc.globalCatalog = ([string]$root.isGlobalCatalogReady -eq 'TRUE')
                $roles = New-Object System.Collections.ArrayList
                $owners = @(
                    @('PDC emulator', $domainNC),
                    @('RID master', ('CN=RID Manager$,CN=System,' + $domainNC)),
                    @('Infrastructure master', ('CN=Infrastructure,' + $domainNC)),
                    @('Schema master', [string]$root.schemaNamingContext),
                    @('Domain naming master', ('CN=Partitions,' + $configNC)))
                foreach ($owner in $owners) {
                    try {
                        $entry = [ADSI]('LDAP://localhost/' + ($owner[1] -replace '/', '\/'))
                        if ([string]$entry.fsmoRoleOwner -eq $self) { [void]$roles.Add($owner[0]) }
                    }
                    catch { [void]$errors.Add('The ' + $owner[0] + ' owner could not be read: ' + $_.Exception.Message) }
                }
                $dc.roles = @($roles)
            }
            catch { [void]$errors.Add('The directory could not be read over LDAP: ' + $_.Exception.Message) }
            # SYSVOL replication: DFSR, or the older FRS.
            try {
                $folder = @(Get-WmiObject -Namespace 'root\MicrosoftDFS' -Class DfsrReplicatedFolderInfo -Filter "ReplicatedFolderName='SYSVOL Share'" -ErrorAction Stop)
                if ($folder.Count -gt 0) { $dc.sysvolReplication = 'DFSR'; $dc.dfsrState = [int]$folder[0].State }
            }
            catch { }
            if (-not $dc.sysvolReplication) {
                $frs = Get-WmiObject -Class Win32_Service -Filter "Name='NtFrs'"
                if ($frs -and $frs.State -eq 'Running') { $dc.sysvolReplication = 'FRS' } else { $dc.sysvolReplication = 'unknown' }
            }
            # Inbound replication of this DC (repadmin /showrepl reads only
            # this DC's own status; columns by position because the headers
            # are localized).
            try {
                $lines = Invoke-Native 'repadmin.exe' @('/showrepl', 'localhost', '/csv')
                $rows = @($lines | Where-Object { $_ -match '^showrepl_' -and $_ -notmatch '^showrepl_COLUMNS' } | ConvertFrom-Csv -Header (0..10 | ForEach-Object { 'c' + $_ }))
                $partners = @{}
                $failing = New-Object System.Collections.ArrayList
                $largest = 0
                foreach ($row in $rows) {
                    $partners[[string]$row.c5] = $true
                    $failures = 0
                    [void][int]::TryParse([string]$row.c7, [ref]$failures)
                    if ($failures -gt 0 -or ([string]$row.c10 -ne '' -and [string]$row.c10 -ne '0')) {
                        if ($failing.Count -lt 20) { [void]$failing.Add(([string]$row.c5 + ' (' + [string]$row.c3 + '): ' + $failures + ' failure(s), last status ' + [string]$row.c10)) }
                    }
                    try {
                        $hours = [int]((Get-Date) - [datetime]::Parse([string]$row.c9)).TotalHours
                        if ($hours -gt $largest) { $largest = $hours }
                    }
                    catch { }
                }
                $dc.replLinks = $rows.Count
                $dc.replPartners = $partners.Count
                $dc.replFailing = @($failing)
                $dc.replLargestHours = $largest
                if ($rows.Count -eq 0) { [void]$errors.Add('repadmin /showrepl returned no replication links: ' + (($lines | Select-Object -First 3) -join ' ')) }
            }
            catch { [void]$errors.Add('repadmin could not be run: ' + $_.Exception.Message) }
            try {
                $source = @(Invoke-Native 'w32tm.exe' @('/query', '/source') | Where-Object { $_.Trim() -ne '' })
                if ($source.Count -gt 0) { $dc.timeSource = $source[0].Trim() }
            }
            catch { [void]$errors.Add('w32tm could not be run: ' + $_.Exception.Message) }
            # dcdiag tests that read only this DC. Its text is English on
            # English Windows; elsewhere the unread lines are kept.
            try {
                $tests = @('Advertising', 'Services', 'NetLogons', 'SysVolCheck', 'Replications', 'MachineAccount', 'KccEvent', 'DFSREvent')
                $arguments = @('/s:' + $env:COMPUTERNAME) + @($tests | ForEach-Object { '/test:' + $_ })
                $lines = Invoke-Native 'dcdiag.exe' $arguments
                $passed = New-Object System.Collections.ArrayList
                $failed = New-Object System.Collections.ArrayList
                foreach ($line in $lines) {
                    if ($line -match '\s(passed|failed) test (\S+)') {
                        if ($Matches[1] -eq 'passed') { [void]$passed.Add($Matches[2]) } else { [void]$failed.Add($Matches[2]) }
                    }
                }
                $dc.dcdiagPassed = @($passed)
                $dc.dcdiagFailed = @($failed)
                if ($passed.Count -eq 0 -and $failed.Count -eq 0) { $dc.dcdiagText = @($lines | Where-Object { $_.Trim() -ne '' } | Select-Object -Last 30) }
            }
            catch { [void]$errors.Add('dcdiag could not be run: ' + $_.Exception.Message) }
        }
        $dc.errors = @($errors)
        $out.dc = $dc
    }

    if ($WantSQL) {
        $sql = @{ checked = $true; instances = @(); errors = @() }
        $errors = New-Object System.Collections.ArrayList
        $services = @{}
        foreach ($service in @(Get-WmiObject -Class Win32_Service | Where-Object { $_.Name -eq 'MSSQLSERVER' -or $_.Name -like 'MSSQL$*' -or $_.Name -eq 'SQLSERVERAGENT' -or $_.Name -like 'SQLAgent$*' -or $_.Name -eq 'SQLWriter' })) {
            $services[[string]$service.Name] = $service
        }
        if ($services['SQLWriter']) { $sql.writerState = [string]$services['SQLWriter'].State; $sql.writerStart = [string]$services['SQLWriter'].StartMode }
        $found = @{}
        foreach ($key in @('HKLM:\SOFTWARE\Microsoft\Microsoft SQL Server\Instance Names\SQL', 'HKLM:\SOFTWARE\Wow6432Node\Microsoft\Microsoft SQL Server\Instance Names\SQL')) {
            if (-not (Test-Path -LiteralPath $key)) { continue }
            $item = Get-ItemProperty -LiteralPath $key
            foreach ($property in $item.PSObject.Properties) {
                if ($property.Name -like 'PS*') { continue }
                if (-not $found.ContainsKey($property.Name)) { $found[$property.Name] = [string]$property.Value }
            }
        }
        $instances = New-Object System.Collections.ArrayList
        foreach ($name in @($found.Keys | Sort-Object)) {
            $id = $found[$name]
            $instance = @{ name = $name; errors = @() }
            $serviceName = 'MSSQL$' + $name
            $agentName = 'SQLAgent$' + $name
            if ($name -eq 'MSSQLSERVER') { $serviceName = 'MSSQLSERVER'; $agentName = 'SQLSERVERAGENT' }
            $service = $services[$serviceName]
            if ($service) { $instance.serviceState = [string]$service.State; $instance.serviceAccount = [string]$service.StartName }
            if ($services[$agentName]) { $instance.agentState = [string]$services[$agentName].State; $instance.agentAccount = [string]$services[$agentName].StartName }
            $clusterName = [string](Get-RegValue ('HKLM:\SOFTWARE\Microsoft\Microsoft SQL Server\' + $id + '\Cluster') 'ClusterName')
            $instance.failoverCluster = ($clusterName -ne '')
            $server = '.'
            if ($clusterName) { $server = $clusterName }
            if ($name -ne 'MSSQLSERVER') { $server = $server + '\' + $name }
            if (-not $service -or $service.State -ne 'Running') {
                if (-not $instance.failoverCluster) { $instances.Add($instance) | Out-Null; continue }
            }
            $connectionText = 'Data Source=' + $server + ';Integrated Security=SSPI;Connect Timeout=15;Application Name=Server Inventory and Migration Manager preflight'
            $connection = New-Object System.Data.SqlClient.SqlConnection $connectionText
            try {
                $connection.Open()
                $query = {
                    param([string]$Text)
                    $command = $connection.CreateCommand()
                    $command.CommandText = $Text
                    $command.CommandTimeout = 60
                    $table = New-Object System.Data.DataTable
                    $reader = $command.ExecuteReader()
                    try { $table.Load($reader) } finally { $reader.Close() }
                    return , $table
                }
                $facts = (& $query "SELECT CAST(SERVERPROPERTY('ProductVersion') AS nvarchar(128)) AS v, CAST(SERVERPROPERTY('Edition') AS nvarchar(128)) AS e, CAST(SERVERPROPERTY('ProductLevel') AS nvarchar(128)) AS l, CAST(ISNULL(SERVERPROPERTY('IsClustered'), 0) AS int) AS c, CAST(ISNULL(SERVERPROPERTY('IsHadrEnabled'), 0) AS int) AS h").Rows[0]
                $instance.version = [string]$facts.v
                $instance.edition = [string]$facts.e
                $instance.level = [string]$facts.l
                if ([int]$facts.c -eq 1) { $instance.failoverCluster = $true }
                $instance.hadr = ([int]$facts.h -eq 1)
                $ages = "(SELECT DATEDIFF(hour, MAX(b.backup_finish_date), GETDATE()) FROM msdb.dbo.backupset b WHERE b.database_name = d.name AND b.type = 'D') AS fullAge, (SELECT DATEDIFF(hour, MAX(b.backup_finish_date), GETDATE()) FROM msdb.dbo.backupset b WHERE b.database_name = d.name AND b.type = 'L') AS logAge"
                try { $rows = & $query ("SELECT d.name, d.recovery_model_desc AS recovery, d.state_desc AS state, CAST(d.is_encrypted AS int) AS enc, " + $ages + " FROM sys.databases d WHERE d.name <> 'tempdb' ORDER BY d.name") }
                catch { $rows = & $query ("SELECT d.name, d.recovery_model_desc AS recovery, d.state_desc AS state, 0 AS enc, " + $ages + " FROM sys.databases d WHERE d.name <> 'tempdb' ORDER BY d.name") }
                $databases = New-Object System.Collections.ArrayList
                foreach ($row in $rows.Rows) {
                    if ($databases.Count -ge 300) { $instance.moreDatabases = $true; break }
                    $full = -1; $log = -1
                    if ($row.fullAge -isnot [DBNull]) { $full = [int]$row.fullAge }
                    if ($row.logAge -isnot [DBNull]) { $log = [int]$row.logAge }
                    [void]$databases.Add(@{ name = [string]$row.name; recovery = [string]$row.recovery; state = [string]$row.state; encrypted = ([int]$row.enc -eq 1); fullAgeHours = $full; logAgeHours = $log })
                }
                $instance.databases = @($databases)
                if ($instance.hadr) {
                    try {
                        $groups = & $query 'SELECT ag.name, rs.role_desc AS role FROM sys.availability_groups ag JOIN sys.dm_hadr_availability_replica_states rs ON rs.group_id = ag.group_id AND rs.is_local = 1'
                        $instance.availabilityGroups = @($groups.Rows | ForEach-Object { [string]$_.name + ' (' + [string]$_.role + ')' })
                    }
                    catch { $instance.errors = @('The availability groups could not be read: ' + $_.Exception.Message) }
                }
            }
            catch { $instance.errors = @($instance.errors) + @('The instance could not be queried with this account: ' + $_.Exception.Message) }
            finally { $connection.Close() }
            [void]$instances.Add($instance)
        }
        $sql.instances = @($instances)
        $sql.errors = @($errors)
        $out.sql = $sql
    }
    return (ConvertTo-Json -InputObject $out -Depth 8 -Compress)
}

$credential = $null
if ($CredentialFromInput) {
    $line = [Console]::In.ReadLine()
    if ($line) {
        $account = $line | ConvertFrom-Json
        if ($account.user) {
            $secure = New-Object System.Security.SecureString
            foreach ($character in ([string]$account.password).ToCharArray()) { $secure.AppendChar($character) }
            $secure.MakeReadOnly()
            $credential = New-Object System.Management.Automation.PSCredential([string]$account.user, $secure)
        }
        $account = $null
    }
    $line = $null
}

$wanted = @($Checks.Split(',') | ForEach-Object { $_.Trim().ToLowerInvariant() })
$wantDC = $wanted -contains 'dc'
$wantSQL = $wanted -contains 'sql'
try {
    $parameters = @{ ScriptBlock = $insideServer; ArgumentList = @($wantDC, $wantSQL); ErrorAction = 'Stop' }
    if ($credential) { $parameters.Credential = $credential }
    if ($Transport -eq 'direct') {
        if (-not $VMName) { throw 'No VM name was given.' }
        # PowerShell Direct looks the VM up with an unqualified Get-VM; on a
        # host with VMware PowerCLI that can be PowerCLI's. Use Hyper-V's.
        Import-Module Hyper-V -ErrorAction Stop
        $found = Get-Command Get-VM -ErrorAction SilentlyContinue
        if (-not $found -or [string]$found.ModuleName -ne 'Hyper-V') { Set-Item -Path function:global:Get-VM -Value { Hyper-V\Get-VM @args } }
        $parameters.VMName = $VMName
        $label = $VMName
    }
    else {
        if (-not $ComputerName) { throw 'No computer name was given.' }
        $parameters.ComputerName = $ComputerName
        $label = $ComputerName
    }
    Write-Log ('Checking ' + $label + ' (read only).')
    $text = Invoke-Command @parameters
    $facts = ([string]$text) | ConvertFrom-Json
    Write-Result @{ ok = $true; facts = $facts }
    Write-Log ('Checked ' + $label + '.')
}
catch {
    $message = $_.Exception.Message
    if ($message -match 'WinRM|WS-Management') { $message = $message + ' Turn on PowerShell remoting on that server (Enable-PSRemoting) or check its firewall allows WinRM (TCP 5985) from this computer.' }
    Write-Result @{ ok = $false; error = $message }
    Write-Log ('The checks could not run: ' + $message)
}
