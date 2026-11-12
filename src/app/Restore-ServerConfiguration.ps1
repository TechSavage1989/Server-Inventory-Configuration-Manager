[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidateSet('Scan', 'Restore')][string]$Mode,
    [Parameter(Mandatory = $true)][string]$PackagePath,
    [string]$ResultPath,
    [string]$PlanPath,
    [string]$RollbackPath,
    [string]$CancelPath
)

# Restores configuration from a Server Inventory Configuration Manager package
# onto this server. Scan reports what can be restored and why anything cannot.
# Restore runs only the items the operator confirmed, in a fixed order, saves
# the current configuration of each item to the rollback folder first, uses
# only supported Microsoft tools, and stops at the first failure. Every package
# file is checked against the package's SHA256-Manifest.csv before use.
# Compatible with Windows PowerShell 3.0 (Windows Server 2012) and later.

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }

function Write-Line {
    param([string]$Text)
    [Console]::Out.WriteLine($Text)
    [Console]::Out.Flush()
}

function Write-Log {
    param([string]$Level, [string]$Message)
    Write-Line ('{0} [{1}] {2}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Level, $Message)
}

function Write-RestoreEvent {
    param([hashtable]$Event)
    Write-Line ('@@RESTORE@@' + (ConvertTo-Json -InputObject $Event -Compress -Depth 6))
}

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

function Get-SafeFileName {
    # Mirrors the collector's Get-SafeFileName so zone folders can be found.
    param([string]$Name)
    $safeName = $Name
    foreach ($character in [System.IO.Path]::GetInvalidFileNameChars()) { $safeName = $safeName.Replace([string]$character, '_') }
    $safeName = $safeName.Replace('\', '_').Replace('/', '_').Replace(':', '_')
    if ([string]::IsNullOrWhiteSpace($safeName)) { $safeName = 'Unnamed' }
    return $safeName
}

function Invoke-Native {
    # Runs a native tool, logs its output and returns the exit code.
    param([string]$FilePath, [string[]]$Arguments)
    Write-Log 'INFO' ('Running: ' + (Split-Path -Path $FilePath -Leaf) + ' ' + ($Arguments -join ' '))
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $output = & $FilePath @Arguments 2>&1
        $code = $LASTEXITCODE
    }
    finally { $ErrorActionPreference = $previous }
    $script:LastNativeOutput = (@($output) | ForEach-Object { [string]$_ }) -join "`n"
    foreach ($line in @($output)) {
        $text = ([string]$line).TrimEnd()
        if ($text) { Write-Log 'INFO' ('  ' + $text) }
    }
    if ($null -eq $code) { $code = 0 }
    return [int]$code
}

function Assert-Native {
    param([string]$FilePath, [string[]]$Arguments, [string]$What)
    $code = Invoke-Native -FilePath $FilePath -Arguments $Arguments
    if ($code -ne 0) { throw ('{0} failed with exit code {1}.' -f $What, $code) }
}

# ---------------------------------------------------------------- package ----

$script:Package = $null
$script:Manifest = @{}
$script:ManifestMessage = ''
$script:ManifestPresent = $false
$script:HashCache = @{}

function Initialize-Package {
    param([string]$Path)
    $resolved = [System.IO.Path]::GetFullPath($Path).TrimEnd('\')
    if (-not (Test-Path -LiteralPath $resolved -PathType Container)) { throw ('The package folder was not found: ' + $resolved) }
    if (-not (Test-Path -LiteralPath (Join-Path $resolved '00-Summary') -PathType Container)) {
        throw 'This folder is not an inventory package. Choose the package folder that contains 00-Summary, not the ZIP file or its parent folder.'
    }
    $script:Package = $resolved
    $manifestPath = Join-Path $resolved 'SHA256-Manifest.csv'
    if (Test-Path -LiteralPath $manifestPath -PathType Leaf) {
        $script:ManifestPresent = $true
        foreach ($row in @(Import-Csv -LiteralPath $manifestPath)) {
            if ($row.RelativePath -and $row.SHA256) { $script:Manifest[([string]$row.RelativePath).ToLowerInvariant()] = ([string]$row.SHA256).ToUpperInvariant() }
        }
        $script:ManifestMessage = ('Package files are checked against SHA256-Manifest.csv ({0} files listed) before they are used.' -f $script:Manifest.Count)
    }
    else {
        $script:ManifestMessage = 'This package has no SHA256-Manifest.csv, so its files cannot be verified. Restore is not available from it.'
    }
}

function Get-PackageFile {
    param([string]$RelativePath)
    return (Join-Path $script:Package $RelativePath)
}

function Get-RelativePackagePath {
    param([string]$FullPath)
    $full = [System.IO.Path]::GetFullPath($FullPath)
    if (-not $full.StartsWith($script:Package + '\', [System.StringComparison]::OrdinalIgnoreCase)) { return $null }
    return $full.Substring($script:Package.Length + 1)
}

function Test-PackageFile {
    # Returns '' when the file is inside the package and matches the manifest,
    # otherwise the reason it must not be used.
    param([string]$FullPath)
    $relative = Get-RelativePackagePath -FullPath $FullPath
    if (-not $relative) { return 'The file is outside the package folder.' }
    if (-not (Test-Path -LiteralPath $FullPath -PathType Leaf)) { return 'The file is missing from the package.' }
    if (-not $script:ManifestPresent) { return 'The package has no SHA256-Manifest.csv to verify this file.' }
    $key = $relative.ToLowerInvariant()
    if (-not $script:Manifest.ContainsKey($key)) { return ('{0} is not listed in the package manifest.' -f $relative) }
    if (-not $script:HashCache.ContainsKey($key)) { $script:HashCache[$key] = Get-FileSha256 -Path $FullPath }
    if ($script:HashCache[$key] -ne $script:Manifest[$key]) { return ('{0} does not match its SHA-256 in the package manifest; it was changed or damaged after collection.' -f $relative) }
    return ''
}

function Test-PackageFolder {
    # Every file in the folder must verify, and every manifest entry under the
    # folder must still be present.
    param([string]$FullPath)
    if (-not (Test-Path -LiteralPath $FullPath -PathType Container)) { return 'The folder is missing from the package.' }
    $relative = Get-RelativePackagePath -FullPath $FullPath
    if (-not $relative) { return 'The folder is outside the package folder.' }
    $files = @(Get-ChildItem -LiteralPath $FullPath -Recurse -File -Force -ErrorAction Stop)
    if ($files.Count -eq 0) { return 'The folder is empty.' }
    foreach ($file in $files) {
        $reason = Test-PackageFile -FullPath $file.FullName
        if ($reason) { return $reason }
    }
    $prefix = $relative.ToLowerInvariant() + '\'
    $listed = @($script:Manifest.Keys | Where-Object { $_.StartsWith($prefix) }).Count
    if ($listed -ne $files.Count) { return ('{0} files are listed in the manifest under this folder but {1} are present.' -f $listed, $files.Count) }
    return ''
}

function Get-PackageIdentity {
    $name = Split-Path -Path $script:Package -Leaf
    $identity = @{ packageName = $name; sourceComputer = ''; collectedAt = '' }
    if ($name -match '^(?<computer>.+?)_ServerInventory_(?<date>\d{8})_(?<time>\d{6})') {
        $identity.sourceComputer = $Matches['computer']
        try {
            $identity.collectedAt = ([datetime]::ParseExact($Matches['date'] + $Matches['time'], 'yyyyMMddHHmmss', [System.Globalization.CultureInfo]::InvariantCulture)).ToString('yyyy-MM-dd HH:mm:ss')
        } catch { }
    }
    return $identity
}

function Import-PackageCsv {
    param([string]$RelativePath)
    $path = Get-PackageFile -RelativePath $RelativePath
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { return @() }
    if (Test-PackageFile -FullPath $path) { return @() }
    return @(Import-Csv -LiteralPath $path)
}

# -------------------------------------------------------------- this host ----

$script:ComputerName = [string]$env:COMPUTERNAME
$script:SameComputer = $false
$script:DomainRole = 0
try { $script:DomainRole = [int](Get-WmiObject -Class Win32_ComputerSystem).DomainRole } catch { }
$script:IsDomainController = $script:DomainRole -ge 4
$script:IsDomainMember = $script:DomainRole -eq 1 -or $script:DomainRole -eq 3 -or $script:IsDomainController

function Test-Administrator {
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Test-ServiceInstalled {
    param([string]$Name)
    return [bool](Get-Service -Name $Name -ErrorAction SilentlyContinue)
}

function Test-CommandAvailable {
    param([string]$Name, [string]$Module)
    if (Get-Command -Name $Name -ErrorAction SilentlyContinue) { return $true }
    if ($Module) {
        try { Import-Module -Name $Module -ErrorAction Stop } catch { return $false }
        return [bool](Get-Command -Name $Name -ErrorAction SilentlyContinue)
    }
    return $false
}

# ------------------------------------------------------------ components ----

function New-RestoreItem {
    param([string]$Id, [string]$Title, [string]$Detail, [string]$BlockedReason, [bool]$DefaultSelected = $true, [hashtable]$Data = @{})
    return @{
        id = $Id; title = $Title; detail = $Detail
        available = -not $BlockedReason; blockedReason = [string]$BlockedReason
        defaultSelected = ($DefaultSelected -and -not $BlockedReason)
        data = $Data
    }
}

function New-RestoreComponent {
    param([string]$Id, [string]$Title, [string]$Description, [string]$Impact, [string[]]$Warnings, [object[]]$Items, [string]$BlockedReason)
    $itemList = @($Items | Where-Object { $_ })
    if (-not $BlockedReason -and $itemList.Count -eq 0) { $BlockedReason = 'Nothing to restore for this component in the package.' }
    if (-not $BlockedReason -and @($itemList | Where-Object { $_.available }).Count -eq 0) { $BlockedReason = [string]$itemList[0].blockedReason }
    if ($BlockedReason) { foreach ($item in $itemList) { if ($item.available) { $item.available = $false; $item.blockedReason = $BlockedReason; $item.defaultSelected = $false } } }
    return @{
        id = $Id; title = $Title; description = $Description; impact = $Impact
        available = -not $BlockedReason; blockedReason = [string]$BlockedReason
        warnings = @($Warnings | Where-Object { $_ }); items = $itemList
    }
}

function Get-DhcpComponent {
    $file = Get-PackageFile '08-Role-Specific\DHCP\DHCP-Configuration-and-Leases.xml'
    $blocked = ''
    if (-not (Test-Path -LiteralPath $file)) { $blocked = 'This package has no DHCP configuration export. It is captured when the DHCP export is selected for a collection.' }
    elseif (-not (Test-ServiceInstalled 'DHCPServer') -or -not (Test-CommandAvailable 'Import-DhcpServer' 'DhcpServer')) { $blocked = 'The DHCP Server role is not installed on this server. Install it, and authorize it in Active Directory if needed, first.' }
    else { $blocked = Test-PackageFile -FullPath $file }
    $warnings = @('Scopes, options, reservations, policies and leases from the package replace the matching ones on this server.', 'The DHCP database is also backed up by Import-DhcpServer before the import.')
    if (-not $script:SameComputer) { $warnings += 'Restoring onto a different server: authorize this server for DHCP in Active Directory and review failover relationships and server bindings afterwards.' }
    $item = New-RestoreItem -Id 'dhcp' -Title 'DHCP configuration and leases' -Detail 'DHCP-Configuration-and-Leases.xml' -BlockedReason $blocked -Data @{ file = $file }
    return New-RestoreComponent -Id 'dhcp' -Title 'DHCP Server' -Description 'Scopes, options, reservations, exclusions, policies and leases.' -Impact 'high' -Warnings $warnings -Items @($item)
}

function Get-NpsComponent {
    $file = Get-PackageFile '08-Role-Specific\NPS\NPS-FULL-CONFIG-SENSITIVE.xml'
    $blocked = ''
    if (-not (Test-Path -LiteralPath $file)) { $blocked = 'This package has no full NPS export. It is only captured when sensitive exports were allowed and the NPS export was selected.' }
    elseif (-not (Test-ServiceInstalled 'IAS') -or -not (Test-CommandAvailable 'Import-NpsConfiguration' 'NPS')) { $blocked = 'The Network Policy Server role is not installed on this server.' }
    else { $blocked = Test-PackageFile -FullPath $file }
    $warnings = @('Replaces the whole NPS configuration: RADIUS clients, shared secrets, connection request and network policies.', 'Certificates used by PEAP/EAP policies must already be installed on this server.', 'The rollback copy of the current NPS configuration contains shared secrets; protect the rollback folder.')
    $item = New-RestoreItem -Id 'nps' -Title 'Full NPS configuration' -Detail 'NPS-FULL-CONFIG-SENSITIVE.xml' -BlockedReason $blocked -Data @{ file = $file }
    return New-RestoreComponent -Id 'nps' -Title 'Network Policy Server (NPS)' -Description 'RADIUS clients, policies and server settings.' -Impact 'high' -Warnings $warnings -Items @($item)
}

function Get-PrintersComponent {
    $folder = Get-PackageFile '08-Role-Specific\Print-Server'
    $files = @()
    if (Test-Path -LiteralPath $folder) { $files = @(Get-ChildItem -LiteralPath $folder -Filter '*.printerExport' -File -ErrorAction SilentlyContinue) }
    $printBrm = Join-Path $env:SystemRoot 'System32\spool\tools\PrintBrm.exe'
    $items = @()
    foreach ($file in $files) {
        $blocked = ''
        if (-not (Test-Path -LiteralPath $printBrm)) { $blocked = 'PrintBrm.exe is not available on this server. Install the Print and Document Services role.' }
        elseif (-not (Test-ServiceInstalled 'Spooler')) { $blocked = 'The Print Spooler service is not installed.' }
        else { $blocked = Test-PackageFile -FullPath $file.FullName }
        $items += New-RestoreItem -Id ('printers:' + $file.Name) -Title 'Printers, drivers and ports' -Detail $file.Name -BlockedReason $blocked -Data @{ file = $file.FullName }
    }
    $blockedComponent = ''
    if ($items.Count -eq 0) { $blockedComponent = 'This package has no PrintBRM printer export. It is captured when the Print services export is selected.' }
    $warnings = @('Print queues, printer drivers, ports and print processors from the package are added; queues with the same name are overwritten.', 'The Print Spooler restarts during the restore, interrupting printing briefly.')
    if (-not $script:SameComputer) { $warnings += 'Restoring onto a different server: drivers must suit this server''s Windows version and architecture; review sharing and published names afterwards.' }
    return New-RestoreComponent -Id 'printers' -Title 'Printers and print drivers' -Description 'Print queues, printer drivers, ports and print processors (PrintBRM).' -Impact 'normal' -Warnings $warnings -Items $items -BlockedReason $blockedComponent
}

function Get-IisComponent {
    $folder = Get-PackageFile '08-Role-Specific\IIS\IIS-Configuration-Backup'
    $appCmd = Join-Path $env:SystemRoot 'System32\inetsrv\appcmd.exe'
    $blocked = ''
    if (-not (Test-Path -LiteralPath $folder)) { $blocked = 'This package has no IIS configuration backup. It is captured when the IIS export is selected.' }
    elseif (-not (Test-Path -LiteralPath $appCmd)) { $blocked = 'IIS is not installed on this server.' }
    else { $blocked = Test-PackageFolder -FullPath $folder }
    $warnings = @('Replaces the whole IIS configuration: sites, bindings, application pools, applications and virtual directories.', 'IIS stops during the restore and starts again afterwards.', 'Website content and TLS certificates are not part of this backup.')
    if (-not $script:SameComputer) { $warnings += 'Restoring onto a different server: encrypted settings such as application pool passwords only work with the original server''s keys, and site paths must exist here.' }
    $item = New-RestoreItem -Id 'iis' -Title 'IIS configuration' -Detail 'IIS-Configuration-Backup' -BlockedReason $blocked -Data @{ folder = $folder }
    return New-RestoreComponent -Id 'iis' -Title 'IIS web server' -Description 'Sites, bindings, application pools, applications and virtual directories.' -Impact 'high' -Warnings $warnings -Items @($item)
}

function Get-FirewallComponent {
    $file = Get-PackageFile '06-Accounts-Policy-Security\Windows-Firewall-Policy.wfw'
    $blocked = ''
    if (-not (Test-Path -LiteralPath $file)) { $blocked = 'This package has no firewall policy export. It is captured when the Firewall export is selected.' }
    else { $blocked = Test-PackageFile -FullPath $file }
    $warnings = @('Replaces every Windows Firewall rule and profile setting with the package''s policy.', 'If the restored rules do not allow your connection, remote access such as Remote Desktop can be cut off. Keep console access available.')
    $item = New-RestoreItem -Id 'firewall' -Title 'Windows Firewall policy' -Detail 'Windows-Firewall-Policy.wfw' -BlockedReason $blocked -Data @{ file = $file }
    return New-RestoreComponent -Id 'firewall' -Title 'Windows Firewall' -Description 'Firewall rules, IPsec rules and profile settings.' -Impact 'high' -Warnings $warnings -Items @($item)
}

function Get-RegistryFileKey {
    # reg.exe exports are UTF-16 with one root key followed by its subkeys.
    param([string]$Path)
    $lines = @(Get-Content -LiteralPath $Path -Encoding Unicode -TotalCount 200000)
    $root = $null
    foreach ($line in $lines) {
        if ($line -match '^\[(?<delete>-?)(?<key>HKEY_[^\]]+)\]\s*$') {
            if ($Matches['delete']) { return @{ key = $null; reason = 'The file deletes registry keys, which a restore never does.' } }
            $key = $Matches['key']
            if (-not $root) { $root = $key; continue }
            if (-not ($key -eq $root -or $key.StartsWith($root + '\', [System.StringComparison]::OrdinalIgnoreCase))) { return @{ key = $null; reason = ('The file writes outside its own key ({0}).' -f $key) } }
        }
    }
    if (-not $root) { return @{ key = $null; reason = 'The file does not contain a registry key.' } }
    if (-not $root.StartsWith('HKEY_LOCAL_MACHINE\', [System.StringComparison]::OrdinalIgnoreCase)) { return @{ key = $null; reason = 'Only HKEY_LOCAL_MACHINE exports are restored.' } }
    return @{ key = $root; reason = '' }
}

function Get-RegistryComponent {
    $files = @(Get-ChildItem -LiteralPath $script:Package -Recurse -Filter '*.reg' -File -ErrorAction SilentlyContinue | Sort-Object FullName)
    $items = @()
    foreach ($file in $files) {
        $relative = Get-RelativePackagePath -FullPath $file.FullName
        $blocked = Test-PackageFile -FullPath $file.FullName
        $key = ''
        if (-not $blocked) {
            $parsed = Get-RegistryFileKey -Path $file.FullName
            $blocked = $parsed.reason
            $key = [string]$parsed.key
        }
        if (-not $blocked -and $key -match '\\Microsoft\\Windows NT\\CurrentVersion$') { $blocked = 'Windows version and product information is never restored.' }
        if (-not $blocked -and -not $script:SameComputer -and $key -match '\\Services\\Tcpip\\Parameters$') { $blocked = 'TCP/IP parameters include the computer''s host name and are only restored onto the server they came from.' }
        $items += New-RestoreItem -Id ('registry:' + $relative) -Title $file.Name -Detail $key -BlockedReason $blocked -DefaultSelected $false -Data @{ file = $file.FullName; key = $key }
    }
    $warnings = @('Each selected file writes its values back into the registry; values added since the backup are kept.', 'Nothing is selected by default. Choose only the branches you need, and restart affected services or the server afterwards.')
    return New-RestoreComponent -Id 'registry' -Title 'Registry settings' -Description 'Exported registry branches such as TLS (SCHANNEL), Remote Desktop, SMB and role settings.' -Impact 'high' -Warnings $warnings -Items $items
}

function Get-TasksComponent {
    $folder = Get-PackageFile '05-Software-Services-Tasks\Scheduled-Task-XML-Custom'
    $files = @()
    if (Test-Path -LiteralPath $folder) { $files = @(Get-ChildItem -LiteralPath $folder -Filter '*.xml' -File -ErrorAction SilentlyContinue | Sort-Object Name) }
    $canRegister = Test-CommandAvailable 'Register-ScheduledTask' 'ScheduledTasks'
    $items = @()
    foreach ($file in $files) {
        $blocked = Test-PackageFile -FullPath $file.FullName
        $uri = ''; $user = ''
        if (-not $blocked) {
            try {
                [xml]$xml = Get-Content -LiteralPath $file.FullName -Raw
                $uri = [string]$xml.Task.RegistrationInfo.URI
                $principal = $xml.Task.Principals.Principal
                $user = [string]$principal.UserId
                if ([string]$principal.LogonType -eq 'Password') { $blocked = ('Runs as {0} with a stored password. Re-create it in Task Scheduler and enter the password there.' -f $user) }
                if (-not $uri) { $blocked = 'The task definition has no name (URI).' }
            }
            catch { $blocked = 'The task definition could not be read: ' + $_.Exception.Message }
        }
        if (-not $blocked -and -not $canRegister) { $blocked = 'The ScheduledTasks module is not available on this server.' }
        $detail = $uri
        if ($user) { $detail = $uri + ' (runs as ' + $user + ')' }
        $items += New-RestoreItem -Id ('tasks:' + $file.Name) -Title ($uri.Split('\')[-1]) -Detail $detail -BlockedReason $blocked -Data @{ file = $file.FullName; uri = $uri }
    }
    $warnings = @('Each selected task is registered from its saved definition; an existing task with the same name is replaced.')
    if (-not $script:SameComputer) { $warnings += 'Restoring onto a different server: tasks that run as local accounts need those accounts to exist here, and program paths must exist.' }
    return New-RestoreComponent -Id 'tasks' -Title 'Scheduled tasks' -Description 'Custom scheduled tasks (Microsoft tasks are not included).' -Impact 'normal' -Warnings $warnings -Items $items
}

function Get-CertificatesComponent {
    $folder = Get-PackageFile '06-Accounts-Policy-Security\Public-Certificate-Exports'
    $files = @()
    if (Test-Path -LiteralPath $folder) { $files = @(Get-ChildItem -LiteralPath $folder -Filter '*.cer' -File -ErrorAction SilentlyContinue | Sort-Object Name) }
    $stores = @{}
    foreach ($row in (Import-PackageCsv '06-Accounts-Policy-Security\Local-Machine-Certificates.csv')) {
        if ($row.Thumbprint -and $row.StorePath) { $stores[([string]$row.Thumbprint).ToUpperInvariant()] = [string]$row.StorePath }
    }
    $canImport = Test-CommandAvailable 'Import-Certificate' 'PKI'
    $items = @()
    foreach ($file in $files) {
        $thumbprint = ($file.BaseName -split '_')[-1].ToUpperInvariant()
        $store = [string]$stores[$thumbprint]
        $subject = $file.BaseName.Substring(0, [Math]::Max(0, $file.BaseName.Length - $thumbprint.Length - 1))
        $blocked = Test-PackageFile -FullPath $file.FullName
        if (-not $blocked -and $store -notmatch '^LocalMachine\\[A-Za-z]+$') { $blocked = 'The certificate''s original store is not recorded in the package.' }
        if (-not $blocked -and (Test-Path -LiteralPath ('Cert:\' + $store + '\' + $thumbprint))) { $blocked = 'Already installed in ' + $store + '.' }
        if (-not $blocked -and -not $canImport) { $blocked = 'The PKI module is not available on this server.' }
        $items += New-RestoreItem -Id ('certificates:' + $file.Name) -Title $subject -Detail ($store + '  ' + $thumbprint) -BlockedReason $blocked -Data @{ file = $file.FullName; store = $store; thumbprint = $thumbprint }
    }
    $warnings = @('Public certificates are added to the store they came from, including trusted root and intermediate authorities.', 'Private keys were never exported, so restored certificates in the Personal store cannot be used for TLS or signing; re-import those from a protected PFX.')
    return New-RestoreComponent -Id 'certificates' -Title 'Certificates' -Description 'Public certificates missing from this server''s certificate stores.' -Impact 'high' -Warnings $warnings -Items $items
}

function Get-AclsComponent {
    $folder = Get-PackageFile '07-Shares-Permissions\Recursive-ICACLS-Backups'
    $items = @()
    foreach ($row in (Import-PackageCsv '07-Shares-Permissions\Recursive-ICACLS-Backups\ICACLS-Mapping.csv')) {
        if ([string]$row.Status -ne 'Completed' -or -not $row.AclFile) { continue }
        $file = Join-Path $folder ([string]$row.AclFile)
        $source = [string]$row.SourcePath
        $blocked = Test-PackageFile -FullPath $file
        if (-not $blocked -and -not $source) { $blocked = 'The original folder path is not recorded.' }
        if (-not $blocked -and ([System.IO.Path]::GetPathRoot($source).TrimEnd('\') -eq $source.TrimEnd('\'))) { $blocked = 'Permissions saved for a whole drive are restored manually with ICACLS.' }
        if (-not $blocked -and -not (Test-Path -LiteralPath $source -PathType Container)) { $blocked = ('The folder {0} does not exist on this server.' -f $source) }
        $items += New-RestoreItem -Id ('acls:' + [string]$row.AclFile) -Title ([string]$row.ShareName) -Detail $source -BlockedReason $blocked -Data @{ file = $file; source = $source }
    }
    $warnings = @('NTFS permissions on every file and folder under each selected share are set back to the saved permissions; files added since keep theirs.', 'The current permissions are saved first, which can take a while on large folders.')
    if (-not $script:SameComputer) { $warnings += 'Restoring onto a different server: permissions for local accounts of the original server cannot be resolved here.' }
    return New-RestoreComponent -Id 'acls' -Title 'Share folder permissions (NTFS)' -Description 'Complete recursive permission saves for shared folders.' -Impact 'high' -Warnings $warnings -Items $items
}

function Get-DnsComponent {
    $zones = @(Import-PackageCsv '08-Role-Specific\DNS\DNS-Zones.csv')
    $hasDns = (Test-ServiceInstalled 'DNS') -and (Test-CommandAvailable 'Add-DnsServerPrimaryZone' 'DnsServer')
    $items = @()
    $anyDomain = $false
    foreach ($zone in $zones) {
        $name = [string]$zone.ZoneName
        if (-not $name -or $name -eq 'TrustAnchors' -or $name -match '^(0|127|255)\.in-addr\.arpa$') { continue }
        $folder = Get-PackageFile ('08-Role-Specific\DNS\Zone-' + (Get-SafeFileName -Name $name))
        $file = $null
        if (Test-Path -LiteralPath (Join-Path $folder 'AD-Integrated-Zone-Export.txt')) { $file = Join-Path $folder 'AD-Integrated-Zone-Export.txt' }
        else { $candidate = @(Get-ChildItem -LiteralPath $folder -Filter 'File-Backed-Zone-*' -File -ErrorAction SilentlyContinue | Select-Object -First 1); if ($candidate.Count) { $file = $candidate[0].FullName } }
        if (-not $file) { continue }
        $dsIntegrated = [string]$zone.IsDsIntegrated -eq 'True'
        $blocked = Test-PackageFile -FullPath $file
        if (-not $blocked -and [string]$zone.ZoneType -ne 'Primary') { $blocked = 'Only primary zones are restored; secondary and stub zones are re-created from their primary server.' }
        if (-not $blocked -and -not $hasDns) { $blocked = 'The DNS Server role is not installed on this server.' }
        if (-not $blocked -and (Get-DnsServerZone -Name $name -ErrorAction SilentlyContinue)) { $blocked = 'The zone already exists on this server. Only missing zones are restored.' }
        if ($dsIntegrated) { $anyDomain = $true }
        $detail = 'File-backed primary zone'
        if ($dsIntegrated) { $detail = 'Active Directory-integrated zone (' + [string]$zone.ReplicationScope + ')' }
        $items += New-RestoreItem -Id ('dns:' + $name) -Title $name -Detail $detail -BlockedReason $blocked -Data @{ file = $file; zone = $name; dsIntegrated = $dsIntegrated; scope = [string]$zone.ReplicationScope }
    }
    $impact = 'normal'
    if ($anyDomain) { $impact = 'domain' }
    $warnings = @('Missing zones are re-created with all records from the package.', 'Active Directory-integrated zones are stored in Active Directory again when this server is a domain controller, and then replicate to other DNS servers in the domain.')
    return New-RestoreComponent -Id 'dns' -Title 'DNS zones' -Description 'Primary zones that are missing from this DNS server.' -Impact $impact -Warnings $warnings -Items $items
}

function Get-GpoComponent {
    $folder = Get-PackageFile '08-Role-Specific\Group-Policy\GPO-Backups'
    $items = @()
    $available = (Test-CommandAvailable 'Restore-GPO' 'GroupPolicy') -and $script:IsDomainMember
    if (Test-Path -LiteralPath $folder) {
        foreach ($backup in @(Get-ChildItem -LiteralPath $folder -Directory -ErrorAction SilentlyContinue | Sort-Object Name)) {
            $info = Join-Path $backup.FullName 'bkupInfo.xml'
            if (-not (Test-Path -LiteralPath $info)) { continue }
            $blocked = Test-PackageFolder -FullPath $backup.FullName
            $name = ''; $gpoId = ''
            if (-not $blocked) {
                try {
                    [xml]$xml = Get-Content -LiteralPath $info -Raw
                    $name = [string]$xml.BackupInst.GPODisplayName.'#cdata-section'
                    if (-not $name) { $name = [string]$xml.BackupInst.GPODisplayName.InnerText }
                    $gpoId = [string]$xml.BackupInst.GPOGuid.'#cdata-section'
                    if (-not $gpoId) { $gpoId = [string]$xml.BackupInst.GPOGuid.InnerText }
                }
                catch { $blocked = 'The GPO backup information could not be read.' }
            }
            if (-not $blocked -and -not $available) { $blocked = 'Group Policy restore needs the Group Policy Management tools on a domain-joined server.' }
            $detail = 'Settings are restored into the existing GPO'
            if (-not $blocked) {
                try { Get-GPO -Guid $gpoId.Trim('{}') -ErrorAction Stop | Out-Null }
                catch { $detail = 'The GPO no longer exists; it is re-created with a new ID (links, permissions and WMI filters are not restored)' }
            }
            if (-not $name) { $name = $backup.Name }
            $items += New-RestoreItem -Id ('gpo:' + $backup.Name) -Title $name -Detail $detail -BlockedReason $blocked -DefaultSelected $false -Data @{ folder = $folder; backupId = $backup.Name; gpoId = $gpoId; name = $name }
        }
    }
    $warnings = @('Group Policy changes apply to every computer and user the GPO is linked to across the domain.', 'Nothing is selected by default. Choose only the GPOs you need.', 'Restoring needs rights to edit Group Policy in the domain.')
    return New-RestoreComponent -Id 'gpo' -Title 'Group Policy objects' -Description 'Domain GPO settings from Backup-GPO.' -Impact 'domain' -Warnings $warnings -Items $items
}

function Get-SecpolComponent {
    $file = Get-PackageFile '06-Accounts-Policy-Security\Local-Security-Policy.inf'
    $blocked = ''
    if (-not (Test-Path -LiteralPath $file)) { $blocked = 'This package has no local security policy export.' }
    elseif ($script:IsDomainController) { $blocked = 'On a domain controller, security policy comes from Group Policy. Restore the Default Domain Controllers Policy instead.' }
    else { $blocked = Test-PackageFile -FullPath $file }
    $warnings = @('Replaces local password, lockout and audit policy, security options and user rights assignments.', 'On a domain member, domain Group Policy still overrides these settings.', 'Wrong user rights can stop people from signing in. Keep console access available.')
    if (-not $script:SameComputer) { $warnings += 'Restoring onto a different server: rights granted to the original server''s local accounts do not apply here.' }
    $item = New-RestoreItem -Id 'secpol' -Title 'Local security policy' -Detail 'Local-Security-Policy.inf' -BlockedReason $blocked -DefaultSelected $false -Data @{ file = $file }
    return New-RestoreComponent -Id 'secpol' -Title 'Local security policy' -Description 'Account policies, audit policy, security options and user rights.' -Impact 'high' -Warnings $warnings -Items @($item)
}

# Restore order: base system and security first, then files, services, roles,
# and domain-wide changes last.
$script:ComponentOrder = @('registry', 'secpol', 'certificates', 'firewall', 'acls', 'tasks', 'printers', 'iis', 'dhcp', 'nps', 'dns', 'gpo')

function Get-AllComponents {
    $builders = @{
        registry = { Get-RegistryComponent }; secpol = { Get-SecpolComponent }; certificates = { Get-CertificatesComponent }
        firewall = { Get-FirewallComponent }; acls = { Get-AclsComponent }; tasks = { Get-TasksComponent }
        printers = { Get-PrintersComponent }; iis = { Get-IisComponent }; dhcp = { Get-DhcpComponent }
        nps = { Get-NpsComponent }; dns = { Get-DnsComponent }; gpo = { Get-GpoComponent }
    }
    $display = @('dhcp', 'nps', 'printers', 'iis', 'dns', 'gpo', 'firewall', 'certificates', 'tasks', 'acls', 'registry', 'secpol')
    $components = @()
    foreach ($id in $display) {
        try { $components += (& $builders[$id]) }
        catch { $components += New-RestoreComponent -Id $id -Title $id -Description '' -Impact 'normal' -Warnings @() -Items @() -BlockedReason ('The package could not be checked for this component: ' + $_.Exception.Message) }
    }
    return $components
}

function Remove-ItemData {
    # Internal paths stay out of the scan the interface shows.
    param([object[]]$Components)
    foreach ($component in $Components) { foreach ($item in $component.items) { $item.Remove('data') } }
    return $Components
}

# ------------------------------------------------------------ restore ops ----

function Restore-Dhcp {
    param([hashtable]$Data, [string]$Rollback)
    Import-Module DhcpServer -ErrorAction Stop
    Export-DhcpServer -ComputerName $script:ComputerName -File (Join-Path $Rollback 'Current-DHCP-Configuration-and-Leases.xml') -Leases -Force
    Write-Log 'INFO' 'Saved the current DHCP configuration and leases.'
    Import-DhcpServer -ComputerName $script:ComputerName -File $Data.file -BackupPath (Join-Path $Rollback 'DHCP-Database-Backup') -Leases -ScopeOverwrite -Force
}

function Restore-Nps {
    param([hashtable]$Data, [string]$Rollback)
    Import-Module NPS -ErrorAction Stop
    Export-NpsConfiguration -Path (Join-Path $Rollback 'Current-NPS-CONFIG-SENSITIVE.xml')
    Write-Log 'WARN' 'Saved the current NPS configuration; it contains shared secrets.'
    Import-NpsConfiguration -Path $Data.file
}

function Wait-PrintBrmEngine {
    # PrintBrm.exe can return while PrintBrmEngine.exe is still writing or
    # applying the migration file, so wait for the engine before going on.
    param([string]$What)
    $deadline = (Get-Date).AddMinutes(60)
    while (@(Get-Process -Name 'PrintBrmEngine' -ErrorAction SilentlyContinue).Count -gt 0) {
        if ((Get-Date) -gt $deadline) { throw ($What + ' did not finish within 60 minutes.') }
        Start-Sleep -Seconds 2
    }
}

function Assert-PrintBrmSucceeded {
    # PrintBrm.exe can exit with 0 after reporting an error, so read its output.
    param([string]$What)
    if ($script:LastNativeOutput -match 'error occurred' -or $script:LastNativeOutput -notmatch 'Successfully finished operation') {
        throw ($What + ' did not finish successfully. See the PrintBRM output above and the Microsoft-Windows-PrintBRM/Admin event log.')
    }
}

function Restore-Printers {
    param([hashtable]$Data, [string]$Rollback)
    $printBrm = Join-Path $env:SystemRoot 'System32\spool\tools\PrintBrm.exe'
    Wait-PrintBrmEngine -What 'An earlier printer migration'
    $snapshot = Join-Path $Rollback 'Current-Printers.printerExport'
    Assert-Native -FilePath $printBrm -Arguments @('-B', '-F', $snapshot) -What 'Saving the current printers'
    Assert-PrintBrmSucceeded -What 'Saving the current printers'
    Wait-PrintBrmEngine -What 'Saving the current printers'
    if (-not (Test-Path -LiteralPath $snapshot)) { throw 'PrintBRM did not save the current printers, so the restore was not started.' }
    Assert-Native -FilePath $printBrm -Arguments @('-R', '-F', $Data.file, '-O', 'FORCE') -What 'PrintBRM restore'
    Assert-PrintBrmSucceeded -What 'PrintBRM restore'
    Wait-PrintBrmEngine -What 'PrintBRM restore'
}

function Restore-Iis {
    param([hashtable]$Data, [string]$Rollback)
    $appCmd = Join-Path $env:SystemRoot 'System32\inetsrv\appcmd.exe'
    $backupRoot = Join-Path $env:SystemRoot 'System32\inetsrv\backup'
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $current = 'SICM-PreRestore-' + $stamp
    Assert-Native -FilePath $appCmd -Arguments @('add', 'backup', $current) -What 'Saving the current IIS configuration'
    Copy-Item -LiteralPath (Join-Path $backupRoot $current) -Destination (Join-Path $Rollback 'Current-IIS-Configuration') -Recurse -Force
    $restoreName = 'SICM-Restore-' + $stamp
    Copy-Item -LiteralPath $Data.folder -Destination (Join-Path $backupRoot $restoreName) -Recurse -Force
    try { Assert-Native -FilePath $appCmd -Arguments @('restore', 'backup', $restoreName, '/stop:true') -What 'IIS restore' }
    finally {
        foreach ($service in @('WAS', 'W3SVC')) { try { Start-Service -Name $service -ErrorAction Stop } catch { Write-Log 'WARN' ('Could not start ' + $service + ': ' + $_.Exception.Message) } }
        Invoke-Native -FilePath $appCmd -Arguments @('delete', 'backup', $restoreName) | Out-Null
    }
}

function Restore-Firewall {
    param([hashtable]$Data, [string]$Rollback)
    $netsh = Join-Path $env:SystemRoot 'System32\netsh.exe'
    Assert-Native -FilePath $netsh -Arguments @('advfirewall', 'export', (Join-Path $Rollback 'Current-Windows-Firewall-Policy.wfw')) -What 'Saving the current firewall policy'
    Assert-Native -FilePath $netsh -Arguments @('advfirewall', 'import', $Data.file) -What 'Firewall policy import'
}

function Restore-Registry {
    param([hashtable]$Data, [string]$Rollback)
    $reg = Join-Path $env:SystemRoot 'System32\reg.exe'
    $nativeKey = $Data.key -replace '^HKEY_LOCAL_MACHINE\\', 'HKLM\'
    $snapshot = Join-Path $Rollback ((Get-SafeFileName -Name $nativeKey) + '.current.reg')
    if (Test-Path -LiteralPath ('Registry::' + $Data.key)) {
        Assert-Native -FilePath $reg -Arguments @('export', $nativeKey, $snapshot, '/y') -What 'Saving the current registry key'
    }
    else {
        Write-Utf8File -Path ($snapshot + '.did-not-exist.txt') -Text ($Data.key + " did not exist before the restore. Delete it to undo.`r`n")
    }
    Assert-Native -FilePath $reg -Arguments @('import', $Data.file) -What 'Registry import'
}

function Restore-Task {
    param([hashtable]$Data, [string]$Rollback)
    Import-Module ScheduledTasks -ErrorAction SilentlyContinue
    $uri = $Data.uri
    $index = $uri.LastIndexOf('\')
    $taskPath = $uri.Substring(0, $index + 1)
    $taskName = $uri.Substring($index + 1)
    if (-not $taskPath) { $taskPath = '\' }
    $safeName = Get-SafeFileName -Name $uri.Trim('\')
    $existing = Get-ScheduledTask -TaskName $taskName -TaskPath $taskPath -ErrorAction SilentlyContinue
    if ($existing) {
        Export-ScheduledTask -TaskName $taskName -TaskPath $taskPath | Set-Content -LiteralPath (Join-Path $Rollback ($safeName + '.current.xml')) -Encoding Unicode
    }
    else {
        Write-Utf8File -Path (Join-Path $Rollback ($safeName + '.did-not-exist.txt')) -Text ($uri + " did not exist before the restore. Delete it to undo.`r`n")
    }
    $xml = Get-Content -LiteralPath $Data.file -Raw
    Register-ScheduledTask -Xml $xml -TaskName $taskName -TaskPath $taskPath -Force | Out-Null
}

function Restore-Certificate {
    param([hashtable]$Data, [string]$Rollback)
    Import-Module PKI -ErrorAction SilentlyContinue
    Import-Certificate -FilePath $Data.file -CertStoreLocation ('Cert:\' + $Data.store) | Out-Null
    Add-Content -LiteralPath (Join-Path $Rollback 'Certificates-Added.txt') -Value ('Cert:\' + $Data.store + '\' + $Data.thumbprint) -Encoding UTF8
}

function Restore-Acl {
    param([hashtable]$Data, [string]$Rollback)
    $icacls = Join-Path $env:SystemRoot 'System32\icacls.exe'
    $source = $Data.source.TrimEnd('\')
    $parent = Split-Path -Path $source -Parent
    Assert-Native -FilePath $icacls -Arguments @($source, '/save', (Join-Path $Rollback ((Get-SafeFileName -Name $source) + '.current.acl')), '/t', '/c', '/q') -What 'Saving the current permissions'
    # ICACLS saves names relative to the parent of the saved folder, so the
    # restore runs against that parent.
    Assert-Native -FilePath $icacls -Arguments @($parent, '/restore', $Data.file, '/c', '/q') -What 'Permission restore'
}

function Restore-DnsZone {
    param([hashtable]$Data, [string]$Rollback)
    Import-Module DnsServer -ErrorAction Stop
    $fileName = (Get-SafeFileName -Name $Data.zone) + '.dns'
    $target = Join-Path (Join-Path $env:SystemRoot 'System32\dns') $fileName
    if (Test-Path -LiteralPath $target) { throw ('{0} already exists in the DNS folder; move it away first.' -f $target) }
    Copy-Item -LiteralPath $Data.file -Destination $target
    Add-DnsServerPrimaryZone -Name $Data.zone -ZoneFile $fileName -LoadExisting
    Add-Content -LiteralPath (Join-Path $Rollback 'DNS-Zones-Added.txt') -Value $Data.zone -Encoding UTF8
    if ($Data.dsIntegrated) {
        if ($script:IsDomainController) {
            $scope = $Data.scope
            if ($scope -notin @('Forest', 'Domain', 'Legacy')) { $scope = 'Domain' }
            ConvertTo-DnsServerPrimaryZone -Name $Data.zone -ReplicationScope $scope -Force
            Write-Log 'INFO' ('Stored the zone in Active Directory (' + $scope + ' replication).')
        }
        else { Write-Log 'WARN' 'The zone was Active Directory-integrated, but this server is not a domain controller; it was restored as a file-backed zone.' }
    }
}

function Restore-Gpo {
    param([hashtable]$Data, [string]$Rollback)
    Import-Module GroupPolicy -ErrorAction Stop
    $guid = $Data.gpoId.Trim('{}')
    $existing = $null
    try { $existing = Get-GPO -Guid $guid -ErrorAction Stop } catch { }
    if ($existing) {
        Backup-GPO -Guid $guid -Path $Rollback -Comment 'Before Server Inventory Configuration Manager restore' | Out-Null
        Restore-GPO -BackupId $Data.backupId.Trim('{}') -Path $Data.folder | Out-Null
    }
    else {
        Import-GPO -BackupId $Data.backupId.Trim('{}') -Path $Data.folder -TargetName $Data.name -CreateIfNeeded | Out-Null
        Add-Content -LiteralPath (Join-Path $Rollback 'GPOs-Created.txt') -Value $Data.name -Encoding UTF8
        Write-Log 'WARN' ('Re-created GPO ' + $Data.name + ' with a new ID. Link it and set its permissions and WMI filter again.')
    }
}

function Restore-Secpol {
    param([hashtable]$Data, [string]$Rollback)
    $secedit = Join-Path $env:SystemRoot 'System32\secedit.exe'
    Assert-Native -FilePath $secedit -Arguments @('/export', '/cfg', (Join-Path $Rollback 'Current-Local-Security-Policy.inf'), '/quiet') -What 'Saving the current security policy'
    $code = Invoke-Native -FilePath $secedit -Arguments @('/configure', '/db', (Join-Path $Rollback 'restore.sdb'), '/cfg', $Data.file, '/areas', 'SECURITYPOLICY', 'USER_RIGHTS', '/log', (Join-Path $Rollback 'secedit.log'), '/quiet')
    # Exit code 3 means the policy was applied but some accounts named in user
    # rights could not be resolved on this server; secedit.log lists them.
    if ($code -eq 3) { Write-Log 'WARN' 'Security policy applied with warnings: some accounts in user rights assignments do not exist on this server. See secedit.log in the rollback folder.' }
    elseif ($code -ne 0) { throw ('Security policy restore failed with exit code {0}. See secedit.log in the rollback folder.' -f $code) }
}

$script:Restorers = @{
    dhcp = 'Restore-Dhcp'; nps = 'Restore-Nps'; printers = 'Restore-Printers'; iis = 'Restore-Iis'; firewall = 'Restore-Firewall'
    registry = 'Restore-Registry'; tasks = 'Restore-Task'; certificates = 'Restore-Certificate'; acls = 'Restore-Acl'
    dns = 'Restore-DnsZone'; gpo = 'Restore-Gpo'; secpol = 'Restore-Secpol'
}

$script:UndoGuidance = @{
    dhcp = 'Import Current-DHCP-Configuration-and-Leases.xml with Import-DhcpServer -ScopeOverwrite, or restore DHCP-Database-Backup with Restore-DhcpServer.'
    nps = 'Import Current-NPS-CONFIG-SENSITIVE.xml with Import-NpsConfiguration.'
    printers = 'Restore Current-Printers.printerExport with PrintBrm.exe -R -O FORCE.'
    iis = 'Copy Current-IIS-Configuration into %windir%\System32\inetsrv\backup and run appcmd restore backup.'
    firewall = 'Import Current-Windows-Firewall-Policy.wfw with netsh advfirewall import.'
    registry = 'Import the .current.reg files with reg import; delete keys listed in .did-not-exist.txt files.'
    tasks = 'Register the .current.xml files with Register-ScheduledTask; delete tasks listed in .did-not-exist.txt files.'
    certificates = 'Remove the certificates listed in Certificates-Added.txt.'
    acls = 'Run icacls <parent folder> /restore <name>.current.acl.'
    dns = 'Remove the zones listed in DNS-Zones-Added.txt.'
    gpo = 'Restore the GPO backups in this folder with Restore-GPO; delete GPOs listed in GPOs-Created.txt.'
    secpol = 'Apply Current-Local-Security-Policy.inf with secedit /configure.'
}

function Invoke-RestorePlan {
    if (-not (Test-Administrator)) { throw 'Restoring needs administrator rights. Run the application as administrator.' }
    if (-not $PlanPath -or -not (Test-Path -LiteralPath $PlanPath)) { throw 'The restore plan was not provided.' }
    if (-not $RollbackPath) { throw 'The rollback folder was not provided.' }
    $plan = Get-Content -LiteralPath $PlanPath -Raw | ConvertFrom-Json
    $requested = @{}
    foreach ($component in @($plan.components)) {
        $ids = @{}
        foreach ($id in @($component.items)) { $ids[[string]$id] = $true }
        $requested[[string]$component.id] = $ids
    }
    # Re-check everything now: availability and file hashes can change
    # between the scan the operator reviewed and this run.
    $components = Get-AllComponents
    $queue = @()
    foreach ($componentId in $script:ComponentOrder) {
        if (-not $requested.ContainsKey($componentId)) { continue }
        $component = @($components | Where-Object { $_.id -eq $componentId })[0]
        foreach ($item in $component.items) {
            if (-not $requested[$componentId].ContainsKey($item.id)) { continue }
            $queue += @{ component = $componentId; id = $item.id; title = $item.title; available = $item.available; blockedReason = $item.blockedReason; data = $item.data }
            $requested[$componentId].Remove($item.id)
        }
        foreach ($missing in @($requested[$componentId].Keys)) { $queue += @{ component = $componentId; id = $missing; title = $missing; available = $false; blockedReason = 'This item is no longer in the package.'; data = @{} } }
    }
    if ($queue.Count -eq 0) { throw 'No restore items were selected.' }
    New-Item -Path $RollbackPath -ItemType Directory -Force | Out-Null
    $readme = "Server Inventory Configuration Manager restore`r`nPackage: $($script:Package)`r`nServer: $($script:ComputerName)`r`nStarted: $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')`r`n`r`nEach folder holds the configuration saved just before that component was restored.`r`n"
    Write-Utf8File -Path (Join-Path $RollbackPath 'README.txt') -Text $readme
    Write-RestoreEvent @{ event = 'plan'; items = @($queue | ForEach-Object { @{ component = $_.component; id = $_.id; title = $_.title } }) }
    $failed = $false
    $canceled = $false
    foreach ($entry in $queue) {
        if ($failed -or $canceled) {
            $reason = 'Not run because an earlier item failed.'
            $status = 'skipped'
            if ($canceled) { $reason = 'Not run because the restore was stopped.'; $status = 'canceled' }
            Write-RestoreEvent @{ event = 'item'; component = $entry.component; id = $entry.id; status = $status; message = $reason }
            continue
        }
        if ($CancelPath -and (Test-Path -LiteralPath $CancelPath)) {
            $canceled = $true
            Write-Log 'WARN' 'Restore stopped by the operator.'
            Write-RestoreEvent @{ event = 'item'; component = $entry.component; id = $entry.id; status = 'canceled'; message = 'Not run because the restore was stopped.' }
            continue
        }
        if (-not $entry.available) {
            # Something changed since the operator reviewed the scan, such as a
            # package file. Stop rather than restore a different set than agreed.
            $failed = $true
            Write-Log 'ERROR' ('Failed: Restore ' + $entry.component + ' - ' + $entry.title + ': ' + $entry.blockedReason)
            Write-RestoreEvent @{ event = 'item'; component = $entry.component; id = $entry.id; status = 'failed'; message = ('No longer restorable: ' + $entry.blockedReason) }
            continue
        }
        $rollback = Join-Path $RollbackPath $entry.component
        New-Item -Path $rollback -ItemType Directory -Force | Out-Null
        if (-not (Test-Path -LiteralPath (Join-Path $rollback 'UNDO.txt'))) { Write-Utf8File -Path (Join-Path $rollback 'UNDO.txt') -Text ($script:UndoGuidance[$entry.component] + "`r`n") }
        Write-Log 'INFO' ('Starting: Restore ' + $entry.component + ' - ' + $entry.title)
        Write-RestoreEvent @{ event = 'item'; component = $entry.component; id = $entry.id; status = 'restoring'; message = 'Saving the current configuration, then restoring.' }
        try {
            & $script:Restorers[$entry.component] -Data $entry.data -Rollback $rollback
            Write-Log 'INFO' ('Completed: Restore ' + $entry.component + ' - ' + $entry.title)
            Write-RestoreEvent @{ event = 'item'; component = $entry.component; id = $entry.id; status = 'succeeded'; message = 'Restored.' }
        }
        catch {
            $failed = $true
            $message = $_.Exception.Message
            # CIM-based cmdlets (DHCP, DNS) keep the useful part of the error
            # in the inner exception or error data.
            if ($_.Exception.InnerException -and $_.Exception.InnerException.Message -and $_.Exception.InnerException.Message -ne $message) { $message += ' ' + $_.Exception.InnerException.Message }
            try { if ($_.Exception.ErrorData -and $_.Exception.ErrorData.CimInstanceProperties['error_WindowsErrorMessage']) { $message += ' ' + [string]$_.Exception.ErrorData.CimInstanceProperties['error_WindowsErrorMessage'].Value } } catch { }
            Write-Log 'ERROR' ('Failed: Restore ' + $entry.component + ' - ' + $entry.title + ': ' + $message)
            Write-RestoreEvent @{ event = 'item'; component = $entry.component; id = $entry.id; status = 'failed'; message = $message }
        }
    }
    $final = 'succeeded'
    if ($failed) { $final = 'failed' } elseif ($canceled) { $final = 'canceled' }
    Write-RestoreEvent @{ event = 'done'; status = $final }
    if ($failed) { return 1 }
    if ($canceled) { return 2 }
    return 0
}

# ------------------------------------------------------------------- main ----

try {
    Initialize-Package -Path $PackagePath
    $identity = Get-PackageIdentity
    $script:SameComputer = [bool]$identity.sourceComputer -and ($identity.sourceComputer -eq $script:ComputerName)
    if ($Mode -eq 'Scan') {
        if (-not $ResultPath) { throw 'ResultPath is required for a scan.' }
        $components = Get-AllComponents
        $result = @{
            packagePath = $script:Package; packageName = $identity.packageName
            sourceComputer = $identity.sourceComputer; currentComputer = $script:ComputerName; sameComputer = $script:SameComputer
            collectedAt = $identity.collectedAt; manifestVerified = $script:ManifestPresent; manifestMessage = $script:ManifestMessage
            isAdministrator = (Test-Administrator); components = @(Remove-ItemData -Components $components)
        }
        if (-not $script:ManifestPresent) { foreach ($component in $result.components) { $component.available = $false; $component.blockedReason = $script:ManifestMessage; foreach ($item in $component.items) { $item.available = $false; $item.blockedReason = $script:ManifestMessage; $item.defaultSelected = $false } } }
        Write-Utf8File -Path $ResultPath -Text (ConvertTo-Json -InputObject $result -Depth 8)
        exit 0
    }
    exit (Invoke-RestorePlan)
}
catch {
    Write-Log 'ERROR' $_.Exception.Message
    if ($Mode -eq 'Restore') { Write-RestoreEvent @{ event = 'done'; status = 'failed'; message = $_.Exception.Message } }
    exit 3
}
