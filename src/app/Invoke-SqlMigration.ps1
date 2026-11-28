param(
    [Parameter(Mandatory = $true)][ValidateSet('Batches', 'Dns')][string]$Mode,
    [Parameter(Mandatory = $true)][string]$ResultPath
)

# SQL Server database migration engine (sqlmig_windows.go). It runs T-SQL
# batches against the old and the new SQL Server, and (Dns mode) moves a DNS
# alias, from the computer running Server Inventory and Migration Manager.
#
# Everything arrives as one JSON line on standard input (never a command
# line or a file): the batches, which can hold login password hashes and
# certificate passwords, and the accounts. Batch text is never logged; only
# batch names are.
#
#   current  the Windows account running this application connects directly
#   sql      a SQL Server login connects directly
#   windows  another Windows account: Invoke-Command to the SQL Server's
#            computer with that account, which then connects locally with
#            its own Windows sign-in (one hop, so no credential delegation)
#
# Dns mode reaches the DNS server with Invoke-Command and uses only the
# DnsServer module's Get/Set/Add/Remove-DnsServerResourceRecord on the one
# alias record (CNAME) and setspn for that alias's names.
#
# Nothing is copied to or installed on any server, and no service, task,
# WMI process start, encoded command or CredSSP is used. Windows PowerShell
# 3.0 or later.

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }

function Write-Line { param([string]$Text) [Console]::Out.WriteLine($Text); [Console]::Out.Flush() }
function Write-Log { param([string]$Message) Write-Line ('{0} [INFO] {1}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Message) }
function Write-Result {
    param($Value)
    $json = ConvertTo-Json -InputObject $Value -Depth 12 -Compress
    [IO.File]::WriteAllText($ResultPath, $json, (New-Object System.Text.UTF8Encoding($false)))
}

function New-Credential {
    param([string]$User, [string]$Password)
    $secure = New-Object System.Security.SecureString
    foreach ($c in $Password.ToCharArray()) { $secure.AppendChar($c) }
    $secure.MakeReadOnly()
    return New-Object System.Management.Automation.PSCredential($User, $secure)
}

# Runs a list of batches on one SQL Server and returns the results as JSON
# text. It runs here or, for another Windows account, inside the SQL
# Server's computer.
$batchRunner = {
    param([string]$Server, [string]$SqlUser, [string]$SqlPassword, [string]$BatchesJson)
    $ErrorActionPreference = 'Stop'
    function ConvertTo-Cell {
        param($Value)
        if ($Value -is [DBNull] -or $null -eq $Value) { return $null }
        if ($Value -is [byte[]]) { return '0x' + [BitConverter]::ToString($Value).Replace('-', '') }
        if ($Value -is [DateTime]) { return $Value.ToString('yyyy-MM-ddTHH:mm:ss.fff') }
        if ($Value -is [DateTimeOffset]) { return $Value.ToString('o') }
        if ($Value -is [decimal]) { return [double]$Value }
        if ($Value -is [Guid]) { return $Value.ToString() }
        if ($Value -is [bool] -or $Value -is [string] -or $Value -is [int] -or $Value -is [long] -or $Value -is [double] -or $Value -is [int16] -or $Value -is [byte] -or $Value -is [single]) { return $Value }
        return [string]$Value
    }
    $builder = New-Object System.Data.SqlClient.SqlConnectionStringBuilder
    $builder['Data Source'] = $Server
    $builder['Initial Catalog'] = 'master'
    $builder['Application Name'] = 'Server Inventory and Migration Manager'
    $builder['Connect Timeout'] = 30
    if ($SqlUser) {
        $builder['User ID'] = $SqlUser
        $builder['Password'] = $SqlPassword
        $builder['Integrated Security'] = $false
    }
    else {
        $builder['Integrated Security'] = $true
    }
    $batches = @(ConvertFrom-Json -InputObject $BatchesJson)
    $results = New-Object System.Collections.ArrayList
    $connection = New-Object System.Data.SqlClient.SqlConnection($builder.ConnectionString)
    $builder = $null
    $messages = New-Object System.Collections.ArrayList
    $handler = [System.Data.SqlClient.SqlInfoMessageEventHandler] { param($sender, $e) foreach ($m in $e.Errors) { [void]$messages.Add([string]$m.Message) } }
    $connection.add_InfoMessage($handler)
    $connection.FireInfoMessageEventOnUserErrors = $false
    try {
        $connection.Open()
    }
    catch {
        $text = $_.Exception.Message
        if ($_.Exception.InnerException) { $text = $_.Exception.InnerException.Message }
        foreach ($b in $batches) {
            [void]$results.Add(@{ name = [string]$b.name; ok = $false; error = ('Could not connect to ' + $Server + ': ' + $text); sets = @(); messages = @() })
            break
        }
        return (ConvertTo-Json -InputObject @($results) -Depth 10 -Compress)
    }
    try {
        foreach ($b in $batches) {
            $messages.Clear()
            $entry = @{ name = [string]$b.name; ok = $true; error = ''; sets = @(); messages = @() }
            $sets = New-Object System.Collections.ArrayList
            $command = $connection.CreateCommand()
            $command.CommandText = [string]$b.sql
            $command.CommandTimeout = [int]$b.timeout
            $reader = $null
            try {
                $reader = $command.ExecuteReader()
                do {
                    if ($reader.FieldCount -gt 0) {
                        $rows = New-Object System.Collections.ArrayList
                        while ($reader.Read()) {
                            $row = @{}
                            for ($i = 0; $i -lt $reader.FieldCount; $i++) {
                                $name = $reader.GetName($i)
                                if (-not $name) { $name = 'c' + $i }
                                $row[$name] = ConvertTo-Cell $reader.GetValue($i)
                            }
                            [void]$rows.Add($row)
                        }
                        [void]$sets.Add(@($rows))
                    }
                } while ($reader.NextResult())
            }
            catch {
                $ex = $_.Exception
                while ($ex.InnerException -and -not ($ex -is [System.Data.SqlClient.SqlException])) { $ex = $ex.InnerException }
                $text = $ex.Message
                if ($ex -is [System.Data.SqlClient.SqlException]) {
                    $parts = @()
                    foreach ($err in $ex.Errors) { if ($err.Class -gt 10) { $parts += [string]$err.Message } }
                    if ($parts.Count -gt 0) { $text = ($parts -join ' ') }
                }
                $entry.ok = $false
                $entry.error = $text
            }
            finally {
                if ($reader) { $reader.Close() }
                $command.Dispose()
            }
            $entry.sets = @($sets)
            $entry.messages = @($messages | Select-Object -Last 50)
            [void]$results.Add($entry)
            if (-not $entry.ok -and -not $b.continue) { break }
        }
    }
    finally {
        $connection.Close()
        $connection.Dispose()
    }
    return (ConvertTo-Json -InputObject @($results) -Depth 10 -Compress)
}

# Moves the alias on the DNS server. Runs inside the DNS server.
$dnsRunner = {
    param([string]$Zone, [string]$Name, [string]$Action, [string]$Target, [string]$Expect, [string]$Account, [string[]]$Spns)
    $ErrorActionPreference = 'Stop'
    function Invoke-Native {
        param([string]$File, [string[]]$Arguments)
        $previous = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        try { return @(& $File @Arguments 2>&1 | ForEach-Object { [string]$_ }) }
        finally { $ErrorActionPreference = $previous }
    }
    function Get-Alias {
        $records = @(Get-DnsServerResourceRecord -ZoneName $Zone -Name $Name -ErrorAction SilentlyContinue | Where-Object { $_.RecordType -ne 'SOA' -and $_.RecordType -ne 'NS' })
        return $records
    }
    $out = @{ type = ''; value = ''; spnOwner = ''; notes = @() }
    if ($Action -eq 'read' -or $Action -eq 'set' -or $Action -eq 'remove') {
        Import-Module DnsServer -ErrorAction Stop
        $records = Get-Alias
        if ($records.Count -gt 1) { throw ('{0} has {1} DNS records; only a single alias (CNAME) is changed.' -f $Name, $records.Count) }
        if ($records.Count -eq 1) {
            $out.type = [string]$records[0].RecordType
            if ($out.type -eq 'CNAME') { $out.value = [string]$records[0].RecordData.HostNameAlias }
            elseif ($out.type -eq 'A') { $out.value = [string]$records[0].RecordData.IPv4Address }
            elseif ($out.type -eq 'AAAA') { $out.value = [string]$records[0].RecordData.IPv6Address }
        }
        if ($Action -eq 'set') {
            if ($records.Count -eq 0) {
                Add-DnsServerResourceRecordCName -ZoneName $Zone -Name $Name -HostNameAlias ($Target + '.')
            }
            elseif ($out.type -eq 'CNAME') {
                $old = $records[0]
                $new = $old.Clone()
                $new.RecordData.HostNameAlias = $Target + '.'
                Set-DnsServerResourceRecord -ZoneName $Zone -OldInputObject $old -NewInputObject $new
            }
            else { throw ('{0} is a {1} record, not a CNAME, so it was left alone.' -f $Name, $out.type) }
        }
        if ($Action -eq 'remove') {
            if ($records.Count -eq 1 -and $out.type -eq 'CNAME') {
                if ($out.value.TrimEnd('.') -ne $Expect.TrimEnd('.')) { throw ('{0} now points at {1}, not {2}; it was changed by someone else and was left alone.' -f $Name, $out.value, $Expect) }
                Remove-DnsServerResourceRecord -ZoneName $Zone -InputObject $records[0] -Force
            }
        }
    }
    if ($Action -eq 'spn' -or $Action -eq 'unspn') {
        foreach ($spn in $Spns) {
            if ($Action -eq 'spn') {
                $query = Invoke-Native 'setspn.exe' @('-Q', $spn)
                $owner = ''
                for ($i = 0; $i -lt $query.Count; $i++) {
                    if ($query[$i] -match '^CN=([^,]+),') { $owner = $Matches[1]; break }
                }
                if ($owner) {
                    # The name belongs to another account (the old server's): move it.
                    if (-not $out.spnOwner) { $out.spnOwner = $owner }
                    $null = Invoke-Native 'setspn.exe' @('-D', $spn, $owner)
                    $out.notes += ('Removed {0} from {1}.' -f $spn, $owner)
                }
                $add = Invoke-Native 'setspn.exe' @('-S', $spn, $Account)
                if (($add -join ' ') -match 'Duplicate SPN found|FindDomainForAccount|failed') { throw ('setspn could not register {0} on {1}: {2}' -f $spn, $Account, ($add -join ' ')) }
                $out.notes += ('Registered {0} on {1}.' -f $spn, $Account)
            }
            else {
                $null = Invoke-Native 'setspn.exe' @('-D', $spn, $Account)
                if ($Target) { $null = Invoke-Native 'setspn.exe' @('-S', $spn, $Target) }
                $out.notes += ('Put back {0}.' -f $spn)
            }
        }
    }
    return (ConvertTo-Json -InputObject $out -Depth 4 -Compress)
}

try {
    $line = [Console]::In.ReadLine()
    if (-not $line) { throw 'No request arrived on standard input.' }
    $request = ConvertFrom-Json -InputObject $line
    $line = $null

    if ($Mode -eq 'Batches') {
        $all = @($request.batches)
        # Each server's results are kept as the JSON text it returned and
        # joined as text, so nothing is lost converting it back and forth.
        $parts = New-Object System.Collections.ArrayList
        $index = 0
        while ($index -lt $all.Count) {
            $on = [string]$all[$index].on
            $group = New-Object System.Collections.ArrayList
            while ($index -lt $all.Count -and [string]$all[$index].on -eq $on) { [void]$group.Add($all[$index]); $index++ }
            $side = $request.source
            if ($on -eq 'target') { $side = $request.target }
            if (-not $side) { throw ('No connection for ' + $on + '.') }
            $names = @($group | ForEach-Object { [string]$_.name }) -join ', '
            Write-Log ('{0} server ({1}): {2}' -f $on, [string]$side.server, $names)
            $json = ConvertTo-Json -InputObject @($group) -Depth 4 -Compress
            $text = $null
            if ([string]$side.auth -eq 'windows') {
                $credential = New-Credential ([string]$side.user) ([string]$side.password)
                $text = Invoke-Command -ComputerName ([string]$side.computer) -Credential $credential -ScriptBlock $batchRunner -ArgumentList @([string]$side.server, '', '', $json)
                $credential = $null
            }
            elseif ([string]$side.auth -eq 'sql') {
                $text = & $batchRunner ([string]$side.server) ([string]$side.user) ([string]$side.password) $json
            }
            else {
                $text = & $batchRunner ([string]$side.server) '' '' $json
            }
            $json = $null
            $text = [string]$text
            [void]$parts.Add($text)
            $part = @(ConvertFrom-Json -InputObject $text)
            $stop = $false
            foreach ($r in $part) {
                if (-not $r.ok) {
                    Write-Log ('{0} failed: {1}' -f $r.name, $r.error)
                    $continue = $false
                    foreach ($g in $group) { if ([string]$g.name -eq [string]$r.name -and $g.continue) { $continue = $true } }
                    if (-not $continue) { $stop = $true }
                }
            }
            if ($stop) { break }
        }
        $json = '{"ok":true,"parts":[' + ($parts -join ',') + ']}'
        [IO.File]::WriteAllText($ResultPath, $json, (New-Object System.Text.UTF8Encoding($false)))
    }
    else {
        $d = $request.dns
        $dnsArgs = @([string]$d.zone, [string]$d.name, [string]$d.mode, [string]$d.target, [string]$d.expect, [string]$d.spnAccount, [string[]]@($d.spns))
        $text = $null
        if ($request.account -and [string]$request.account.user) {
            $credential = New-Credential ([string]$request.account.user) ([string]$request.account.password)
            $text = Invoke-Command -ComputerName ([string]$d.server) -Credential $credential -ScriptBlock $dnsRunner -ArgumentList $dnsArgs
            $credential = $null
        }
        else {
            $text = Invoke-Command -ComputerName ([string]$d.server) -ScriptBlock $dnsRunner -ArgumentList $dnsArgs
        }
        Write-Log ('DNS {0} {1} in {2} on {3}' -f [string]$d.mode, [string]$d.name, [string]$d.zone, [string]$d.server)
        Write-Result @{ ok = $true; dns = (ConvertFrom-Json -InputObject ([string]$text)) }
    }
    $request = $null
    exit 0
}
catch {
    $message = $_.Exception.Message
    Write-Log ('Failed: ' + $message)
    Write-Result @{ ok = $false; error = $message }
    exit 1
}
