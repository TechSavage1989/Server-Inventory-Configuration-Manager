[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$PackagePath,
    [Parameter(Mandatory = $true)][string]$ResultPath,
    # Copy the files of IIS sites, applications and virtual directories, with
    # their NTFS permissions, into the bundle.
    [switch]$IncludeWebContent,
    # Website files larger than this in total are left out and listed.
    [ValidateRange(1, 2048)][int]$WebContentLimitGB = 10,
    [string]$CancelPath,
    # A live move between servers (Invoke-ServerMoveRemote.ps1) runs this
    # engine inside the old server as a script block: -Embedded returns the
    # exit code instead of ending the session, lines go to -LogPath (read by
    # a second session), and -Passphrase replaces standard input, which a
    # remote script block does not have. The passphrase travels inside the
    # encrypted remoting session only, never on a command line.
    [string]$LogPath,
    [switch]$Embedded,
    [string]$Passphrase
)

# Writes the migration bundle of an inventory package: what the restore needs
# to make the package's roles work on a different server. Runs on the server
# that was just collected, after the protected collector finished, and writes
# only inside <package>\09-Migration-Bundle with its own Bundle-Manifest.csv.
#
# Bundle.json holds structured settings (roles and features, local accounts,
# file shares, IIS sites, bindings and add-on modules, DNS forwarders and
# non-primary zones, DHCP authorization, scheduled task accounts, certificate
# list). Secrets.simmsec holds what must stay secret (certificates with their
# private keys as PFX, and the passwords IIS keeps encrypted, read through
# IIS's own API), encrypted with the passphrase the
# operator typed (PBKDF2, AES-256-CBC, HMAC-SHA256). The passphrase arrives as
# one JSON line {"passphrase":".."} on standard input and is never written
# anywhere. Nothing on this server is changed: only reads, exports of keys
# Windows marks exportable, and copies of website files.
# Compatible with Windows PowerShell 3.0 (Windows Server 2012) and later.

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }

function Write-Line {
    param([string]$Text)
    if ($LogPath) { [System.IO.File]::AppendAllText($LogPath, $Text + "`r`n", (New-Object System.Text.UTF8Encoding($false))); return }
    [Console]::Out.WriteLine($Text); [Console]::Out.Flush()
}
function Write-Log { param([string]$Level, [string]$Message) Write-Line ('{0} [{1}] {2}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Level, $Message) }

function Write-Utf8File {
    param([string]$Path, [string]$Text)
    $parent = Split-Path -Path $Path -Parent
    if ($parent -and -not (Test-Path -LiteralPath $parent)) { New-Item -Path $parent -ItemType Directory -Force | Out-Null }
    [System.IO.File]::WriteAllText($Path, $Text, (New-Object System.Text.UTF8Encoding($false)))
}

function Get-FileSha256 {
    param([string]$Path)
    $stream = [System.IO.File]::OpenRead($Path)
    try {
        $sha = [System.Security.Cryptography.SHA256]::Create()
        try { return ([System.BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '') }
        finally { $sha.Dispose() }
    }
    finally { $stream.Dispose() }
}

function Test-Canceled { return ($CancelPath -and (Test-Path -LiteralPath $CancelPath)) }

function Invoke-NativeTool {
    # Native tools write errors to standard error; with ErrorActionPreference
    # Stop that becomes a terminating error, so run them with Continue.
    param([string]$FilePath, [string[]]$Arguments)
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { $output = & $FilePath @Arguments 2>&1; $code = $LASTEXITCODE }
    finally { $ErrorActionPreference = $previous }
    if ($null -eq $code) { $code = 0 }
    return @{ code = [int]$code; output = ((@($output) | ForEach-Object { [string]$_ }) -join "`n") }
}

# BEGIN SIMM SECRET ENVELOPE
# The same functions are in Restore-ServerConfiguration.ps1; a Go test keeps
# the two copies identical. Format: 'SIMMSEC1', PBKDF2 iterations (4 bytes,
# big-endian), salt (16), IV (16), AES-256-CBC ciphertext, HMAC-SHA256 (32)
# over everything before it. PBKDF2-HMAC-SHA1 gives 64 bytes: the AES key,
# then the HMAC key.
$script:SecretMagic = [System.Text.Encoding]::ASCII.GetBytes('SIMMSEC1')
$script:SecretIterations = 200000

function Get-SecretKeys {
    param([string]$Passphrase, [byte[]]$Salt, [int]$Iterations)
    $derive = New-Object System.Security.Cryptography.Rfc2898DeriveBytes($Passphrase, $Salt, $Iterations)
    try { $bytes = $derive.GetBytes(64) } finally { $derive.Dispose() }
    $encryption = New-Object byte[] 32; $mac = New-Object byte[] 32
    [Array]::Copy($bytes, 0, $encryption, 0, 32); [Array]::Copy($bytes, 32, $mac, 0, 32)
    return @{ encryption = $encryption; mac = $mac }
}

function Protect-SecretText {
    param([string]$Text, [string]$Passphrase)
    $random = New-Object System.Security.Cryptography.RNGCryptoServiceProvider
    $salt = New-Object byte[] 16; $iv = New-Object byte[] 16
    $random.GetBytes($salt); $random.GetBytes($iv); $random.Dispose()
    $keys = Get-SecretKeys -Passphrase $Passphrase -Salt $salt -Iterations $script:SecretIterations
    $aes = New-Object System.Security.Cryptography.AesManaged
    $aes.KeySize = 256; $aes.Mode = [System.Security.Cryptography.CipherMode]::CBC; $aes.Padding = [System.Security.Cryptography.PaddingMode]::PKCS7
    $aes.Key = $keys.encryption; $aes.IV = $iv
    $plain = [System.Text.Encoding]::UTF8.GetBytes($Text)
    $encryptor = $aes.CreateEncryptor()
    try { $cipher = $encryptor.TransformFinalBlock($plain, 0, $plain.Length) } finally { $encryptor.Dispose(); $aes.Dispose(); [Array]::Clear($plain, 0, $plain.Length) }
    $iterations = [BitConverter]::GetBytes([int]$script:SecretIterations)
    if ([BitConverter]::IsLittleEndian) { [Array]::Reverse($iterations) }
    $stream = New-Object System.IO.MemoryStream
    foreach ($part in @($script:SecretMagic, $iterations, $salt, $iv, $cipher)) { $stream.Write($part, 0, $part.Length) }
    $body = $stream.ToArray(); $stream.Dispose()
    $hmac = New-Object System.Security.Cryptography.HMACSHA256 -ArgumentList @(, $keys.mac)
    try { $tag = $hmac.ComputeHash($body) } finally { $hmac.Dispose() }
    $result = New-Object byte[] ($body.Length + $tag.Length)
    [Array]::Copy($body, 0, $result, 0, $body.Length); [Array]::Copy($tag, 0, $result, $body.Length, $tag.Length)
    return , $result
}

function Unprotect-SecretBytes {
    # Returns the text, or throws 'passphrase' when the passphrase is wrong or
    # the file was changed (the HMAC does not match).
    param([byte[]]$Data, [string]$Passphrase)
    if ($Data.Length -lt (8 + 4 + 16 + 16 + 16 + 32)) { throw 'The secrets file is too short.' }
    for ($i = 0; $i -lt 8; $i++) { if ($Data[$i] -ne $script:SecretMagic[$i]) { throw 'The secrets file is not a Server Inventory and Migration Manager secrets file.' } }
    $iterationBytes = New-Object byte[] 4; [Array]::Copy($Data, 8, $iterationBytes, 0, 4)
    if ([BitConverter]::IsLittleEndian) { [Array]::Reverse($iterationBytes) }
    $iterations = [BitConverter]::ToInt32($iterationBytes, 0)
    if ($iterations -lt 10000 -or $iterations -gt 10000000) { throw 'The secrets file has an invalid key setting.' }
    $salt = New-Object byte[] 16; [Array]::Copy($Data, 12, $salt, 0, 16)
    $iv = New-Object byte[] 16; [Array]::Copy($Data, 28, $iv, 0, 16)
    $keys = Get-SecretKeys -Passphrase $Passphrase -Salt $salt -Iterations $iterations
    $bodyLength = $Data.Length - 32
    $hmac = New-Object System.Security.Cryptography.HMACSHA256 -ArgumentList @(, $keys.mac)
    try { $tag = $hmac.ComputeHash($Data, 0, $bodyLength) } finally { $hmac.Dispose() }
    $difference = 0
    for ($i = 0; $i -lt 32; $i++) { $difference = $difference -bor ($tag[$i] -bxor $Data[$bodyLength + $i]) }
    if ($difference -ne 0) { throw 'passphrase' }
    $aes = New-Object System.Security.Cryptography.AesManaged
    $aes.KeySize = 256; $aes.Mode = [System.Security.Cryptography.CipherMode]::CBC; $aes.Padding = [System.Security.Cryptography.PaddingMode]::PKCS7
    $aes.Key = $keys.encryption; $aes.IV = $iv
    $decryptor = $aes.CreateDecryptor()
    try { $plain = $decryptor.TransformFinalBlock($Data, 44, $bodyLength - 44) } finally { $decryptor.Dispose(); $aes.Dispose() }
    try { return [System.Text.Encoding]::UTF8.GetString($plain) } finally { [Array]::Clear($plain, 0, $plain.Length) }
}
# END SIMM SECRET ENVELOPE

function New-RandomPassword {
    param([int]$Length = 32)
    $alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!#%+=?@'
    $bytes = New-Object byte[] $Length
    $random = New-Object System.Security.Cryptography.RNGCryptoServiceProvider
    $random.GetBytes($bytes); $random.Dispose()
    $chars = for ($i = 0; $i -lt $Length; $i++) { $alphabet[$bytes[$i] % $alphabet.Length] }
    return (-join $chars)
}

# --------------------------------------------------------------- readers ----

$script:Warnings = New-Object System.Collections.ArrayList
function Add-Warning { param([string]$Text) [void]$script:Warnings.Add($Text); Write-Log 'WARN' $Text }

$script:Computer = [string]$env:COMPUTERNAME
$script:IsDomainController = $false
try { $script:IsDomainController = [int](Get-WmiObject -Class Win32_ComputerSystem).DomainRole -ge 4 } catch { }

function Get-MachineFacts {
    $facts = [ordered]@{ computer = $script:Computer; dnsName = ''; domain = ''; osVersion = ''; osCaption = ''; addresses = @(); drives = @() }
    try { $system = Get-WmiObject -Class Win32_ComputerSystem; $facts.domain = [string]$system.Domain; if ($system.PartOfDomain) { $facts.dnsName = $script:Computer + '.' + $system.Domain } else { $facts.dnsName = $script:Computer } } catch { }
    try { $os = Get-WmiObject -Class Win32_OperatingSystem; $facts.osVersion = [string]$os.Version; $facts.osCaption = [string]$os.Caption } catch { }
    try {
        $facts.addresses = @(Get-WmiObject -Class Win32_NetworkAdapterConfiguration -Filter 'IPEnabled = TRUE' | ForEach-Object { @($_.IPAddress) } | Where-Object { $_ -and $_ -notmatch '^(fe80:|169\.254\.)' } | ForEach-Object { [string]$_ })
    } catch { }
    try { $facts.drives = @(Get-WmiObject -Class Win32_LogicalDisk -Filter 'DriveType = 3' | ForEach-Object { [string]$_.DeviceID }) } catch { }
    return $facts
}

function Get-InstalledFeatureNames {
    if (-not (Get-Command Get-WindowsFeature -ErrorAction SilentlyContinue)) {
        try { Import-Module ServerManager -ErrorAction Stop } catch { return @() }
    }
    return @(Get-WindowsFeature | Where-Object { $_.Installed } | ForEach-Object { [string]$_.Name })
}

function Get-LocalAccounts {
    # ADSI works on every Windows Server version this engine supports, where
    # Get-LocalUser needs Windows Server 2016.
    $result = @{ users = @(); groups = @(); machineSid = '' }
    if ($script:IsDomainController) { return $result }
    $computer = [ADSI]('WinNT://' + $script:Computer + ',computer')
    foreach ($child in @($computer.Children)) {
        $class = [string]$child.SchemaClassName
        if ($class -ne 'User' -and $class -ne 'Group') { continue }
        $sid = ''
        try { $sid = (New-Object System.Security.Principal.SecurityIdentifier($child.objectSid.Value, 0)).Value } catch { }
        if ($class -eq 'User') {
            $flags = [int]$child.UserFlags.Value
            if (-not $result.machineSid -and $sid -match '^(S-1-5-21-\d+-\d+-\d+)-\d+$') { $result.machineSid = $Matches[1] }
            $result.users += [ordered]@{
                name = [string]$child.Name.Value; fullName = [string]$child.FullName.Value; description = [string]$child.Description.Value; sid = $sid
                disabled = [bool]($flags -band 0x2); passwordNeverExpires = [bool]($flags -band 0x10000); cannotChangePassword = [bool]($flags -band 0x40)
                builtIn = ($sid -match '-(500|501|503|504)$')
            }
        }
        else {
            $members = @()
            try {
                foreach ($member in @($child.Invoke('Members'))) {
                    $path = [string]$member.GetType().InvokeMember('ADsPath', 'GetProperty', $null, $member, $null)
                    $memberSid = ''
                    try { $memberSid = (New-Object System.Security.Principal.SecurityIdentifier($member.GetType().InvokeMember('objectSid', 'GetProperty', $null, $member, $null), 0)).Value } catch { }
                    # Local members read as WinNT://WORKGROUP/COMPUTER/name (or
                    # DOMAIN/COMPUTER/name): keep the last two parts, COMPUTER\name.
                    $parts = @(($path -replace '^WinNT://', '').Split('/') | Where-Object { $_ })
                    if ($parts.Count -gt 2) { $parts = $parts[($parts.Count - 2)..($parts.Count - 1)] }
                    $name = $parts -join '\'
                    $members += [ordered]@{ name = $name; sid = $memberSid }
                }
            } catch { Add-Warning ('The members of local group ' + $child.Name.Value + ' could not be read: ' + $_.Exception.Message) }
            $result.groups += [ordered]@{ name = [string]$child.Name.Value; description = [string]$child.Description.Value; sid = $sid; builtIn = ($sid -match '^S-1-5-32-'); members = $members }
        }
    }
    return $result
}

function Get-ShareDefinitions {
    $shares = @()
    if (-not (Get-Command Get-SmbShare -ErrorAction SilentlyContinue)) { Add-Warning 'Get-SmbShare is not available, so file shares are not in the bundle.'; return $shares }
    foreach ($share in @(Get-SmbShare -ErrorAction Stop)) {
        $name = [string]$share.Name
        $special = [bool]$share.Special -or $name -match '^(ADMIN\$|IPC\$|[A-Za-z]\$|print\$|SYSVOL|NETLOGON|CertEnroll|REMINST|WsusContent|UpdateServicesPackages|WSUSTemp)$'
        $permissions = @()
        try { $permissions = @(Get-SmbShareAccess -Name $name -ErrorAction Stop | ForEach-Object { [ordered]@{ account = [string]$_.AccountName; type = [string]$_.AccessControlType; right = [string]$_.AccessRight } }) }
        catch { Add-Warning ('The permissions of share ' + $name + ' could not be read: ' + $_.Exception.Message) }
        $scoped = $false; try { $scoped = [string]$share.ScopeName -and [string]$share.ScopeName -ne '*' } catch { }
        $shares += [ordered]@{
            name = $name; path = [string]$share.Path; description = [string]$share.Description; special = $special
            scopeName = [string]$share.ScopeName; clustered = ($scoped -or [string]$share.ShareType -ne 'FileSystemDirectory')
            folderEnumerationMode = [string]$share.FolderEnumerationMode; cachingMode = [string]$share.CachingMode
            concurrentUserLimit = [int]$share.ConcurrentUserLimit; encryptData = [bool]$share.EncryptData; permissions = $permissions
        }
    }
    return $shares
}

function Get-TaskAccounts {
    $tasks = @()
    if (-not (Get-Command Get-ScheduledTask -ErrorAction SilentlyContinue)) { return $tasks }
    foreach ($task in @(Get-ScheduledTask -ErrorAction SilentlyContinue | Where-Object { $_.TaskPath -notlike '\Microsoft\*' })) {
        $tasks += [ordered]@{ uri = ([string]$task.TaskPath + [string]$task.TaskName); user = [string]$task.Principal.UserId; logonType = [string]$task.Principal.LogonType }
    }
    return $tasks
}

function Get-DnsSettings {
    $dns = [ordered]@{ installed = $false; forwarders = @(); useRootHint = $true; zones = @() }
    if (-not (Get-Service -Name DNS -ErrorAction SilentlyContinue)) { return $dns }
    try { Import-Module DnsServer -ErrorAction Stop } catch { Add-Warning 'The DnsServer module is not available, so DNS server settings are not in the bundle.'; return $dns }
    $dns.installed = $true
    try { $forwarder = Get-DnsServerForwarder -ErrorAction Stop; $dns.forwarders = @($forwarder.IPAddress | ForEach-Object { [string]$_ }); $dns.useRootHint = [bool]$forwarder.UseRootHint } catch { Add-Warning ('DNS forwarders could not be read: ' + $_.Exception.Message) }
    foreach ($zone in @(Get-DnsServerZone -ErrorAction SilentlyContinue)) {
        $type = [string]$zone.ZoneType
        if ($type -ne 'Forwarder' -and $type -ne 'Secondary' -and $type -ne 'Stub') { continue }
        $masters = @()
        try { $masters = @($zone.MasterServers | ForEach-Object { [string]$_ }) } catch { }
        $dns.zones += [ordered]@{ name = [string]$zone.ZoneName; type = $type; masters = $masters; dsIntegrated = [bool]$zone.IsDsIntegrated; replicationScope = [string]$zone.ReplicationScope; zoneFile = [string]$zone.ZoneFile }
    }
    return $dns
}

function Get-DhcpSettings {
    $dhcp = [ordered]@{ installed = $false; authorized = $false; dnsName = ''; addresses = @() }
    if (-not (Get-Service -Name DHCPServer -ErrorAction SilentlyContinue)) { return $dhcp }
    $dhcp.installed = $true
    try {
        Import-Module DhcpServer -ErrorAction Stop
        $names = @($script:Computer, (Get-MachineFacts).dnsName) | Where-Object { $_ }
        foreach ($entry in @(Get-DhcpServerInDC -ErrorAction Stop)) {
            foreach ($name in $names) { if ([string]$entry.DnsName -like ($name + '*')) { $dhcp.authorized = $true; $dhcp.dnsName = [string]$entry.DnsName; $dhcp.addresses += [string]$entry.IPAddress } }
        }
    } catch { Add-Warning ('DHCP authorization could not be read: ' + $_.Exception.Message) }
    return $dhcp
}

function Expand-IisPath {
    param([string]$Path)
    if (-not $Path) { return '' }
    return [Environment]::ExpandEnvironmentVariables($Path)
}

function Get-IisSettings {
    $iis = [ordered]@{ installed = $false; configFolder = ''; encryptedValues = 0; sites = @(); appPools = @(); globalModules = @(); contentRoots = @() }
    $config = Join-Path $env:SystemRoot 'System32\inetsrv\config\applicationHost.config'
    if (-not (Test-Path -LiteralPath $config)) { return $iis }
    $iis.installed = $true
    [xml]$xml = Get-Content -LiteralPath $config -Raw
    $raw = Get-Content -LiteralPath $config -Raw
    $iis.encryptedValues = ([regex]::Matches($raw, '\[enc:')).Count
    $paths = New-Object System.Collections.ArrayList
    foreach ($site in @($xml.SelectNodes('/configuration/system.applicationHost/sites/site'))) {
        $bindings = @()
        foreach ($binding in @($site.SelectNodes('bindings/binding'))) {
            $bindings += [ordered]@{
                protocol = [string]$binding.protocol; bindingInformation = [string]$binding.bindingInformation
                sslFlags = [string]$binding.sslFlags; certificateHash = ([string]$binding.certificateHash).ToUpperInvariant(); certificateStoreName = [string]$binding.certificateStoreName
            }
        }
        foreach ($vdir in @($site.SelectNodes('application/virtualDirectory'))) {
            $physical = [string]$vdir.physicalPath
            if ($physical -and $physical -notmatch '^\\\\') { [void]$paths.Add((Expand-IisPath $physical)) }
        }
        $iis.sites += [ordered]@{ name = [string]$site.name; id = [string]$site.id; bindings = $bindings }
    }
    foreach ($pool in @($xml.SelectNodes('/configuration/system.applicationHost/applicationPools/add'))) {
        $model = $pool.SelectSingleNode('processModel')
        $identity = 'ApplicationPoolIdentity'; $user = ''
        if ($model) { if ([string]$model.identityType) { $identity = [string]$model.identityType }; $user = [string]$model.userName }
        $iis.appPools += [ordered]@{ name = [string]$pool.name; identityType = $identity; userName = $user }
    }
    foreach ($module in @($xml.SelectNodes('/configuration/system.webServer/globalModules/add'))) {
        $image = [string]$module.image
        $iis.globalModules += [ordered]@{ name = [string]$module.name; image = $image; expanded = (Expand-IisPath $image) }
    }
    # Keep only top folders: a folder inside another listed folder is copied
    # with it.
    $unique = @($paths | Where-Object { $_ } | ForEach-Object { $_.TrimEnd('\') } | Sort-Object -Unique)
    $roots = @()
    foreach ($candidate in $unique) {
        $inside = $false
        foreach ($other in $unique) { if ($other -ne $candidate -and $candidate.StartsWith($other + '\', [System.StringComparison]::OrdinalIgnoreCase)) { $inside = $true; break } }
        if (-not $inside) { $roots += $candidate }
    }
    $iis.contentRoots = $roots
    return $iis
}

function Get-FolderSize {
    param([string]$Path)
    $total = [long]0
    try { foreach ($file in [System.IO.Directory]::EnumerateFiles($Path, '*', [System.IO.SearchOption]::AllDirectories)) { $total += (New-Object System.IO.FileInfo($file)).Length } }
    catch { return -1 }
    return $total
}

function Test-SystemFolder {
    # Never copy Windows, Program Files or profile folders as website files.
    param([string]$Path)
    $full = $Path.TrimEnd('\')
    if ([System.IO.Path]::GetPathRoot($full).TrimEnd('\') -eq $full) { return 'a whole drive' }
    foreach ($name in @('SystemRoot', 'ProgramFiles', 'ProgramFiles(x86)', 'ProgramData', 'USERPROFILE')) {
        $value = [Environment]::GetEnvironmentVariable($name)
        if ($value -and ($full -eq $value.TrimEnd('\') -or $full.StartsWith($value.TrimEnd('\') + '\', [System.StringComparison]::OrdinalIgnoreCase))) { return ('inside ' + $value) }
    }
    $users = Join-Path $env:SystemDrive 'Users'
    if ($full.StartsWith($users + '\', [System.StringComparison]::OrdinalIgnoreCase)) { return ('inside ' + $users) }
    return ''
}

function Copy-WebContent {
    param([string[]]$Roots, [string]$Destination)
    $entries = @()
    $limit = [long]$WebContentLimitGB * 1GB
    $robocopy = Join-Path $env:SystemRoot 'System32\robocopy.exe'
    $icacls = Join-Path $env:SystemRoot 'System32\icacls.exe'
    $index = 0
    $planned = @()
    $total = [long]0
    foreach ($root in $Roots) {
        $entry = [ordered]@{ index = 0; path = $root; folder = ''; aclFile = ''; sizeBytes = [long]0; included = $false; reason = '' }
        $system = Test-SystemFolder -Path $root
        if ($system) { $entry.reason = ('Not copied: the folder is ' + $system + '.') }
        elseif (-not (Test-Path -LiteralPath $root -PathType Container)) { $entry.reason = 'Not copied: the folder does not exist.' }
        else {
            $entry.sizeBytes = Get-FolderSize -Path $root
            if ($entry.sizeBytes -lt 0) { $entry.reason = 'Not copied: the folder could not be read.'; $entry.sizeBytes = 0 }
        }
        $planned += $entry
        if (-not $entry.reason) { $total += $entry.sizeBytes }
    }
    if ($total -gt $limit) {
        foreach ($entry in $planned) { if (-not $entry.reason) { $entry.reason = ('Not copied: the website files total {0:N1} GB, more than the {1} GB limit chosen for the bundle. Copy them with Move file shares or Robocopy.' -f ($total / 1GB), $WebContentLimitGB) } }
    }
    foreach ($entry in $planned) {
        if (Test-Canceled) { throw 'Stopped by the operator.' }
        if ($entry.reason) { Add-Warning ($entry.path + ': ' + $entry.reason); $entries += $entry; continue }
        $index++
        $entry.index = $index
        $entry.folder = ('Content\{0}' -f $index)
        $target = Join-Path $Destination $entry.folder
        Write-Log 'INFO' ('Copying website files from ' + $entry.path)
        # /B reads with backup rights, so files the administrator cannot open
        # normally are still copied; permissions are saved separately below.
        $copy = Invoke-NativeTool -FilePath $robocopy -Arguments @($entry.path, $target, '/E', '/B', '/COPY:DAT', '/DCOPY:T', '/XJ', '/R:1', '/W:1', '/NP', '/NFL', '/NDL', '/NJH')
        if ($copy.code -ge 8) { throw ('Copying the website files from {0} failed (Robocopy exit code {1}).' -f $entry.path, $copy.code) }
        $aclName = ('Content\{0}.acl' -f $index)
        $save = Invoke-NativeTool -FilePath $icacls -Arguments @($entry.path, '/save', (Join-Path $Destination $aclName), '/t', '/c', '/q')
        if ($save.code -ne 0) { Add-Warning ('The permissions of ' + $entry.path + ' were only partly saved (ICACLS exit code ' + $save.code + ').') }
        if (Test-Path -LiteralPath (Join-Path $Destination $aclName)) { $entry.aclFile = $aclName }
        $entry.included = $true
        $entries += $entry
    }
    return $entries
}

function Get-CertificateFacts {
    # Every certificate with a private key in the computer's Personal and Web
    # Hosting stores, and whether it can be carried in the bundle.
    param([hashtable]$UsedBy)
    $list = @()
    foreach ($storeName in @('My', 'WebHosting')) {
        $path = 'Cert:\LocalMachine\' + $storeName
        if (-not (Test-Path -LiteralPath $path)) { continue }
        foreach ($certificate in @(Get-ChildItem -LiteralPath $path -ErrorAction SilentlyContinue)) {
            if (-not $certificate.HasPrivateKey) { continue }
            $thumb = ([string]$certificate.Thumbprint).ToUpperInvariant()
            # Only a key known to forbid export is left out here; anything else is
            # tried, and Export-PfxCertificate decides. CNG keys (what
            # New-SelfSignedCertificate and most current templates make) answer
            # PrivateKey with null, so null never means "not exportable".
            $exportable = $true
            try {
                $rsa = [System.Security.Cryptography.X509Certificates.RSACertificateExtensions]::GetRSAPrivateKey($certificate)
                if ($rsa -is [System.Security.Cryptography.RSACng]) {
                    $allow = [System.Security.Cryptography.CngExportPolicies]::AllowExport -bor [System.Security.Cryptography.CngExportPolicies]::AllowPlaintextExport
                    $exportable = (([int]$rsa.Key.ExportPolicy) -band ([int]$allow)) -ne 0
                }
                elseif ($rsa -is [System.Security.Cryptography.RSACryptoServiceProvider]) { $exportable = [bool]$rsa.CspKeyContainerInfo.Exportable }
            }
            catch {
                # .NET 4.0 has no GetRSAPrivateKey; CSP keys still answer PrivateKey.
                try {
                    $key = $certificate.PrivateKey
                    if ($key -and $key.CspKeyContainerInfo) { $exportable = [bool]$key.CspKeyContainerInfo.Exportable }
                }
                catch { }
            }
            $list += [ordered]@{
                thumbprint = $thumb; store = ('LocalMachine\' + $storeName); subject = [string]$certificate.Subject
                friendlyName = [string]$certificate.FriendlyName; notAfter = $certificate.NotAfter.ToString('yyyy-MM-dd')
                exportable = $exportable; inBundle = $false; reason = ''; usedBy = @($UsedBy[$thumb])
            }
        }
    }
    return $list
}

function Export-CertificatePfx {
    param($Certificate, [string]$WorkFolder)
    $password = New-RandomPassword
    $file = Join-Path $WorkFolder ($Certificate.thumbprint + '.pfx')
    try {
        if (Get-Command Export-PfxCertificate -ErrorAction SilentlyContinue) {
            $secure = ConvertTo-SecureString -String $password -AsPlainText -Force
            Export-PfxCertificate -Cert ('Cert:\' + $Certificate.store + '\' + $Certificate.thumbprint) -FilePath $file -Password $secure -ChainOption BuildChain -Force | Out-Null
            $bytes = [System.IO.File]::ReadAllBytes($file)
        }
        else {
            $item = Get-Item -LiteralPath ('Cert:\' + $Certificate.store + '\' + $Certificate.thumbprint)
            $bytes = $item.Export([System.Security.Cryptography.X509Certificates.X509ContentType]::Pkcs12, $password)
        }
        return @{ pfx = [Convert]::ToBase64String($bytes); password = $password }
    }
    finally { if (Test-Path -LiteralPath $file) { Remove-Item -LiteralPath $file -Force -ErrorAction SilentlyContinue } }
}

function Get-IisManager {
    $dll = Join-Path $env:SystemRoot 'System32\inetsrv\Microsoft.Web.Administration.dll'
    if (-not (Test-Path -LiteralPath $dll)) { return $null }
    [void][System.Reflection.Assembly]::LoadFrom($dll)
    return (New-Object Microsoft.Web.Administration.ServerManager)
}

function Export-IisPasswords {
    # Reads the passwords IIS keeps encrypted in applicationHost.config through
    # IIS's own API (Microsoft.Web.Administration), which decrypts them for an
    # administrator with whichever keys this IIS version uses (RSA or CNG).
    # The restore sets them again through the same API on the new server, so
    # they are encrypted with that server's keys. No key leaves this server.
    param([xml]$Config)
    $values = @()
    $manager = Get-IisManager
    if (-not $manager) { Add-Warning 'Microsoft.Web.Administration is not available, so IIS passwords are not in the bundle.'; return @{ values = $values; covered = 0 } }
    try {
        foreach ($pool in @($manager.ApplicationPools)) {
            $password = [string]$pool.ProcessModel.Password
            if ($password) { $values += @{ kind = 'apppool'; name = [string]$pool.Name; value = $password } }
        }
        $password = [string]$manager.ApplicationPoolDefaults.ProcessModel.Password
        if ($password) { $values += @{ kind = 'apppoolDefaults'; value = $password } }
        $password = [string]$manager.VirtualDirectoryDefaults.Password
        if ($password) { $values += @{ kind = 'vdirDefaults'; value = $password } }
        foreach ($site in @($manager.Sites)) {
            foreach ($application in @($site.Applications)) {
                foreach ($directory in @($application.VirtualDirectories)) {
                    $password = [string]$directory.Password
                    if ($password) { $values += @{ kind = 'vdir'; site = [string]$site.Name; application = [string]$application.Path; path = [string]$directory.Path; value = $password } }
                }
            }
        }
        $configuration = $manager.GetApplicationHostConfiguration()
        $locations = @('') + @($Config.SelectNodes('/configuration/location') | ForEach-Object { [string]$_.path } | Where-Object { $_ })
        foreach ($location in ($locations | Select-Object -Unique)) {
            try {
                if ($location) { $section = $configuration.GetSection('system.webServer/security/authentication/anonymousAuthentication', $location) }
                else { $section = $configuration.GetSection('system.webServer/security/authentication/anonymousAuthentication') }
                $password = [string]$section.GetAttributeValue('password')
                if ($password -and -not $section.GetAttribute('password').IsInheritedFromDefaultValue) { $values += @{ kind = 'anonymous'; location = $location; value = $password } }
            } catch { }
        }
    }
    finally { $manager.Dispose() }
    return @{ values = $values; covered = $values.Count }
}

# ------------------------------------------------------------------ main ----

$exitCode = 0
$bundleFolder = $null
$workFolder = $null
try {
    $package = [System.IO.Path]::GetFullPath($PackagePath).TrimEnd('\')
    if (-not (Test-Path -LiteralPath (Join-Path $package '00-Summary') -PathType Container)) { throw 'The package folder was not found.' }
    $bundleFolder = Join-Path $package '09-Migration-Bundle'
    if (Test-Path -LiteralPath $bundleFolder) { throw 'The package already has a migration bundle.' }
    $passphrase = ''
    if ($Embedded) { $passphrase = [string]$Passphrase }
    else {
        $line = [Console]::In.ReadLine()
        if ($line) { try { $passphrase = [string](ConvertFrom-Json -InputObject $line).passphrase } catch { } }
        $line = $null
    }
    if ($passphrase.Length -lt 12) { throw 'The bundle passphrase must be at least 12 characters.' }
    New-Item -Path $bundleFolder -ItemType Directory -Force | Out-Null
    # Plain PFX files and keys exist only here, in a folder only SYSTEM and
    # Administrators can open, and only until they are encrypted.
    $workFolder = Join-Path $env:TEMP ('SIMM-Bundle-' + [guid]::NewGuid().ToString('N'))
    New-Item -Path $workFolder -ItemType Directory -Force | Out-Null
    $acl = New-Object System.Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($sid in @('S-1-5-18', 'S-1-5-32-544')) { $acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule((New-Object System.Security.Principal.SecurityIdentifier($sid)), 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))) }
    Set-Acl -LiteralPath $workFolder -AclObject $acl

    Write-Log 'INFO' 'Reading roles and features.'
    $facts = Get-MachineFacts
    # @(): a function returns a one-item list as the item itself, and foreach
    # over a single [ordered] dictionary walks its keys instead.
    $features = @(Get-InstalledFeatureNames)
    Write-Log 'INFO' 'Reading local users and groups.'
    $accounts = Get-LocalAccounts
    Write-Log 'INFO' 'Reading file shares.'
    $shares = @(Get-ShareDefinitions)
    $tasks = @(Get-TaskAccounts)
    Write-Log 'INFO' 'Reading DNS and DHCP settings.'
    $dns = Get-DnsSettings
    $dhcp = Get-DhcpSettings
    Write-Log 'INFO' 'Reading IIS.'
    $iis = Get-IisSettings
    if (Test-Canceled) { throw 'Stopped by the operator.' }

    $usedBy = @{}
    foreach ($site in $iis.sites) { foreach ($binding in $site.bindings) { if ($binding.certificateHash) { if (-not $usedBy.ContainsKey($binding.certificateHash)) { $usedBy[$binding.certificateHash] = @() }; $usedBy[$binding.certificateHash] += ('IIS site ' + $site.name + ' (' + $binding.bindingInformation + ')') } } }
    $certificates = @(Get-CertificateFacts -UsedBy $usedBy)
    $secrets = @{ schema = 1; pfx = @{}; iisPasswords = @() }
    foreach ($certificate in $certificates) {
        if (Test-Canceled) { throw 'Stopped by the operator.' }
        if (-not $certificate.exportable) { $certificate.reason = 'Its private key is marked not exportable. Export it from where it was issued, or issue a new certificate for the new server.' }
        else { try {
            $secrets.pfx[$certificate.thumbprint] = Export-CertificatePfx -Certificate $certificate -WorkFolder $workFolder
            $certificate.inBundle = $true
        }
        catch { $certificate.reason = 'Its private key could not be exported: ' + $_.Exception.Message } }
        if (-not $certificate.inBundle) { Add-Warning ('Certificate ' + $certificate.subject + ' (' + $certificate.thumbprint + ') is not in the bundle. ' + $certificate.reason) }
    }
    $iis.passwordsInBundle = 0
    if ($iis.installed -and $iis.encryptedValues -gt 0) {
        Write-Log 'INFO' ('applicationHost.config holds {0} encrypted value(s); reading the IIS passwords through IIS.' -f $iis.encryptedValues)
        [xml]$configXml = Get-Content -LiteralPath (Join-Path $env:SystemRoot 'System32\inetsrv\config\applicationHost.config') -Raw
        $read = Export-IisPasswords -Config $configXml
        $secrets.iisPasswords = @($read.values)
        $iis.passwordsInBundle = $read.covered
        if ($read.covered -lt $iis.encryptedValues) { Add-Warning ('{0} of the {1} encrypted IIS settings are not application pool, virtual directory or anonymous authentication passwords; type those again in IIS Manager on the new server.' -f ($iis.encryptedValues - $read.covered), $iis.encryptedValues) }
    }

    $content = @()
    if ($iis.installed -and $IncludeWebContent -and $iis.contentRoots.Count) { $content = @(Copy-WebContent -Roots $iis.contentRoots -Destination $bundleFolder) }
    elseif ($iis.installed -and $iis.contentRoots.Count) {
        foreach ($root in $iis.contentRoots) { $content += [ordered]@{ index = 0; path = $root; folder = ''; aclFile = ''; sizeBytes = [long]0; included = $false; reason = 'Website files were not chosen for the bundle.' } }
    }
    $iis.content = $content

    $secretText = ConvertTo-Json -InputObject $secrets -Depth 6 -Compress
    $secrets = $null
    $sealed = Protect-SecretText -Text $secretText -Passphrase $passphrase
    $secretText = $null
    [System.IO.File]::WriteAllBytes((Join-Path $bundleFolder 'Secrets.simmsec'), $sealed)
    # A short known value lets a scan tell a wrong passphrase from a damaged
    # secrets file without decrypting the secrets.
    [System.IO.File]::WriteAllBytes((Join-Path $bundleFolder 'Passphrase-Check.simmsec'), (Protect-SecretText -Text 'SIMM-PASSPHRASE-OK' -Passphrase $passphrase))
    $passphrase = $null

    $bundle = [ordered]@{
        schema = 1; createdAt = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss'); source = $facts
        features = $features; localAccounts = $accounts; shares = $shares; tasks = $tasks; dns = $dns; dhcp = $dhcp; iis = $iis
        certificates = $certificates; warnings = @($script:Warnings)
    }
    Write-Utf8File -Path (Join-Path $bundleFolder 'Bundle.json') -Text (ConvertTo-Json -InputObject $bundle -Depth 10)
    Write-Utf8File -Path (Join-Path $bundleFolder 'README.txt') -Text ("Migration bundle for restoring this package's roles on a different server.`r`nOpen the package in the Restore tab of Server Inventory and Migration Manager on the new server and type the bundle passphrase.`r`nSecrets.simmsec holds certificates with their private keys and the IIS passwords, encrypted with that passphrase. Protect this package like a backup that contains private keys.`r`n")

    $rows = foreach ($file in @(Get-ChildItem -LiteralPath $bundleFolder -Recurse -File -Force)) {
        [pscustomobject]@{ RelativePath = $file.FullName.Substring($package.Length + 1); LengthBytes = $file.Length; SHA256 = (Get-FileSha256 -Path $file.FullName) }
    }
    @($rows) | Export-Csv -LiteralPath (Join-Path $bundleFolder 'Bundle-Manifest.csv') -NoTypeInformation -Encoding UTF8

    $summary = [ordered]@{
        status = 'succeeded'; bundlePath = $bundleFolder; features = $features.Count; localUsers = @($accounts.users).Count; localGroups = @($accounts.groups).Count
        shares = @($shares | Where-Object { -not $_.special }).Count; iisSites = @($iis.sites).Count; siteNames = @($iis.sites | ForEach-Object { [string]$_.name })
        certificatesInBundle = @($certificates | Where-Object { $_.inBundle }).Count; certificatesLeftOut = @($certificates | Where-Object { -not $_.inBundle }).Count
        iisPasswords = [int]$iis.passwordsInBundle; webContentFolders = @($content | Where-Object { $_.included }).Count; warnings = @($script:Warnings)
    }
    Write-Utf8File -Path $ResultPath -Text (ConvertTo-Json -InputObject $summary -Depth 4)
    Write-Log 'INFO' ('Migration bundle written: {0} certificate(s) with private keys, {1} IIS password(s), {2} website folder(s).' -f $summary.certificatesInBundle, $summary.iisPasswords, $summary.webContentFolders)
}
catch {
    $exitCode = 1
    $message = $_.Exception.Message
    Write-Log 'ERROR' $message
    # A half-written bundle must never be mistaken for a complete one.
    if ($bundleFolder -and (Test-Path -LiteralPath $bundleFolder)) { Remove-Item -LiteralPath $bundleFolder -Recurse -Force -ErrorAction SilentlyContinue }
    try { Write-Utf8File -Path $ResultPath -Text (ConvertTo-Json -InputObject @{ status = 'failed'; message = $message } -Compress) } catch { }
}
finally {
    if ($workFolder -and (Test-Path -LiteralPath $workFolder)) { Remove-Item -LiteralPath $workFolder -Recurse -Force -ErrorAction SilentlyContinue }
}
if ($Embedded) { return $exitCode }
exit $exitCode
