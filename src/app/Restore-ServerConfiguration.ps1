[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidateSet('Scan', 'Restore')][string]$Mode,
    [Parameter(Mandatory = $true)][string]$PackagePath,
    [string]$ResultPath,
    [string]$PlanPath,
    [string]$RollbackPath,
    [string]$CancelPath,
    # Reads one JSON line from standard input: {"passphrase":"..","passwords":{..}}.
    # The migration bundle's passphrase and the passwords typed under Changes
    # for this server never reach a command line, a file or the log.
    [switch]$SecretsFromInput
)

# Restores configuration from a Server Inventory and Migration Manager package
# onto this server. Scan reports what can be restored and why anything cannot.
# Restore runs only the items the operator confirmed, in a fixed order, saves
# the current configuration of each item to the rollback folder first, uses
# only supported Microsoft tools, and stops at the first failure. Every package
# file is checked against the package's SHA256-Manifest.csv before use.
# Files of the migration bundle (09-Migration-Bundle, written by
# Export-MigrationBundle.ps1 after the collector) are checked against the
# bundle's own Bundle-Manifest.csv. With a bundle, a restore onto a different
# server also installs missing roles and features, re-creates local accounts
# and file shares, imports certificates with their private keys and the IIS
# keys, and maps the old server's IP addresses, drives and account names to
# this server's (Changes for this server, chosen by the operator).
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
$script:BundlePrefix = '09-migration-bundle\'
$script:BundleManifest = @{}
$script:BundleManifestPresent = $false
$script:Bundle = $null
$script:BundleMessage = ''
$script:Secrets = $null
$script:PassphraseState = 'none'
$script:Passphrase = ''
$script:Passwords = @{}
$script:Mappings = @{ ip = @{}; path = @{} }

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
    $key = $relative.ToLowerInvariant()
    $manifest = $script:Manifest
    $manifestName = 'package manifest'
    if ($key.StartsWith($script:BundlePrefix)) {
        if (-not $script:BundleManifestPresent) { return 'The migration bundle has no Bundle-Manifest.csv to verify this file.' }
        $manifest = $script:BundleManifest
        $manifestName = 'migration bundle manifest'
    }
    elseif (-not $script:ManifestPresent) { return 'The package has no SHA256-Manifest.csv to verify this file.' }
    if (-not $manifest.ContainsKey($key)) { return ('{0} is not listed in the {1}.' -f $relative, $manifestName) }
    if (-not $script:HashCache.ContainsKey($key)) { $script:HashCache[$key] = Get-FileSha256 -Path $FullPath }
    if ($script:HashCache[$key] -ne $manifest[$key]) { return ('{0} does not match its SHA-256 in the {1}; it was changed or damaged after collection.' -f $relative, $manifestName) }
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
    $manifest = $script:Manifest
    if ($prefix.StartsWith($script:BundlePrefix)) { $manifest = $script:BundleManifest }
    $listed = @($manifest.Keys | Where-Object { $_.StartsWith($prefix) }).Count
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

# ---------------------------------------------------------- migration bundle ----

function Initialize-Bundle {
    # Loads 09-Migration-Bundle when the package has one. Its files verify
    # against Bundle-Manifest.csv, which the bundle engine wrote over them.
    $folder = Join-Path $script:Package '09-Migration-Bundle'
    if (-not (Test-Path -LiteralPath $folder -PathType Container)) { return }
    $manifestPath = Join-Path $folder 'Bundle-Manifest.csv'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) { $script:BundleMessage = 'The migration bundle has no Bundle-Manifest.csv, so it cannot be verified or used.'; return }
    $script:BundleManifestPresent = $true
    foreach ($row in @(Import-Csv -LiteralPath $manifestPath)) {
        if ($row.RelativePath -and $row.SHA256) { $script:BundleManifest[([string]$row.RelativePath).ToLowerInvariant()] = ([string]$row.SHA256).ToUpperInvariant() }
    }
    $settings = Join-Path $folder 'Bundle.json'
    $reason = Test-PackageFile -FullPath $settings
    if ($reason) { $script:BundleMessage = 'The migration bundle cannot be used: ' + $reason; return }
    try { $script:Bundle = Get-Content -LiteralPath $settings -Raw -Encoding UTF8 | ConvertFrom-Json }
    catch { $script:BundleMessage = 'The migration bundle settings could not be read: ' + $_.Exception.Message; return }
    $script:BundleMessage = ('The package has a migration bundle from {0}, made {1}.' -f [string]$script:Bundle.source.computer, [string]$script:Bundle.createdAt)
}

function Read-SecretInput {
    if (-not $SecretsFromInput) { return }
    $line = [Console]::In.ReadLine()
    if (-not $line) { return }
    $value = $null
    try { $value = ConvertFrom-Json -InputObject $line } catch { throw 'The secrets passed to the restore engine could not be read.' }
    $line = $null
    if ($value.passphrase) { $script:Passphrase = [string]$value.passphrase }
    if ($value.passwords) { foreach ($property in $value.passwords.PSObject.Properties) { $script:Passwords[[string]$property.Name] = [string]$property.Value } }
}

function Open-BundleSecrets {
    # Sets PassphraseState: none (no bundle secrets), missing, wrong, damaged, ok.
    if (-not $script:Bundle) { return }
    $file = Join-Path $script:Package '09-Migration-Bundle\Secrets.simmsec'
    $check = Join-Path $script:Package '09-Migration-Bundle\Passphrase-Check.simmsec'
    if (-not (Test-Path -LiteralPath $file)) { return }
    if (-not $script:Passphrase) { $script:PassphraseState = 'missing'; return }
    foreach ($path in @($check, $file)) {
        $reason = Test-PackageFile -FullPath $path
        if ($reason) { $script:PassphraseState = 'damaged'; $script:BundleMessage = $reason; return }
    }
    try {
        $known = Unprotect-SecretBytes -Data ([System.IO.File]::ReadAllBytes($check)) -Passphrase $script:Passphrase
        if ($known -ne 'SIMM-PASSPHRASE-OK') { $script:PassphraseState = 'damaged'; return }
    }
    catch { if ($_.Exception.Message -eq 'passphrase') { $script:PassphraseState = 'wrong' } else { $script:PassphraseState = 'damaged' }; return }
    try { $script:Secrets = Unprotect-SecretBytes -Data ([System.IO.File]::ReadAllBytes($file)) -Passphrase $script:Passphrase | ConvertFrom-Json }
    catch { $script:PassphraseState = 'damaged'; return }
    $script:PassphraseState = 'ok'
}

function Get-SecretsBlockedReason {
    switch ($script:PassphraseState) {
        'missing' { return 'Type the migration bundle''s passphrase and check the package again.' }
        'wrong' { return 'The passphrase does not open the migration bundle. Type the passphrase chosen when the package was collected.' }
        'damaged' { return 'The migration bundle''s secrets file was changed or damaged after collection.' }
        'ok' { return '' }
    }
    return 'The package has no migration bundle secrets.'
}

function Get-BundleList {
    # ConvertFrom-Json gives $null, one object or an array; always return an array.
    param($Value)
    if ($null -eq $Value) { return @() }
    return @($Value)
}

function Get-SourceComputer {
    if ($script:Bundle -and $script:Bundle.source.computer) { return [string]$script:Bundle.source.computer }
    return [string](Get-PackageIdentity).sourceComputer
}

# ------------------------------------------------- changes for this server ----

function Get-ThisServerAddresses {
    $list = @()
    try {
        $list = @(Get-WmiObject -Class Win32_NetworkAdapterConfiguration -Filter 'IPEnabled = TRUE' | ForEach-Object { @($_.IPAddress) } | Where-Object { $_ -and $_ -notmatch '^(fe80:|169\.254\.)' } | ForEach-Object { [string]$_ })
    } catch { }
    return @($list | Select-Object -Unique)
}

function Get-ThisServerDrives {
    $drives = @()
    try { $drives = @(Get-WmiObject -Class Win32_LogicalDisk -Filter 'DriveType = 3' | Sort-Object -Property FreeSpace -Descending | ForEach-Object { @{ name = ([string]$_.DeviceID).ToUpperInvariant(); free = [long]$_.FreeSpace } }) } catch { }
    return $drives
}

function Get-DriveOf {
    param([string]$Path)
    if ($Path -match '^([A-Za-z]:)') { return $Matches[1].ToUpperInvariant() }
    return ''
}

function Get-MappedPath {
    param([string]$Path)
    if (-not $Path) { return $Path }
    $drive = Get-DriveOf -Path $Path
    if ($drive -and $script:Mappings.path.ContainsKey($drive)) { return ($script:Mappings.path[$drive] + $Path.Substring(2)) }
    return $Path
}

function Get-MappedAddress {
    param([string]$Address)
    if ($Address -and $script:Mappings.ip.ContainsKey($Address)) { return [string]$script:Mappings.ip[$Address] }
    return $Address
}

function Convert-AccountName {
    # OLD\name (an account of the old server) becomes THIS\name.
    param([string]$Name)
    $source = Get-SourceComputer
    if (-not $Name -or -not $source -or $script:SameComputer) { return $Name }
    if ($Name.StartsWith($source + '\', [System.StringComparison]::OrdinalIgnoreCase)) { return ($script:ComputerName + $Name.Substring($source.Length)) }
    return $Name
}

function Get-IisBindingAddresses {
    $list = @()
    if (-not $script:Bundle) { return $list }
    foreach ($site in (Get-BundleList $script:Bundle.iis.sites)) {
        foreach ($binding in (Get-BundleList $site.bindings)) {
            if ([string]$binding.protocol -notmatch '^https?$') { continue }
            $information = [string]$binding.bindingInformation
            $index = $information.LastIndexOf(':', $information.LastIndexOf(':') - 1)
            if ($index -le 0) { continue }
            $address = $information.Substring(0, $index).Trim('[', ']')
            if ($address -and $address -ne '*') { $list += $address }
        }
    }
    return @($list | Select-Object -Unique)
}

function Get-UsedDrives {
    # Drives named by anything this restore would write to.
    $used = @{}
    $add = { param($path, $why) $drive = Get-DriveOf -Path $path; if ($drive) { if (-not $used.ContainsKey($drive)) { $used[$drive] = @() }; if ($used[$drive] -notcontains $why) { $used[$drive] += $why } } }
    if ($script:Bundle) {
        foreach ($share in (Get-BundleList $script:Bundle.shares)) { if (-not $share.special) { & $add ([string]$share.path) 'File shares' } }
        foreach ($root in (Get-BundleList $script:Bundle.iis.contentRoots)) { & $add ([string]$root) 'IIS sites' }
    }
    foreach ($row in (Import-PackageCsv '07-Shares-Permissions\Recursive-ICACLS-Backups\ICACLS-Mapping.csv')) { if ([string]$row.Status -eq 'Completed') { & $add ([string]$row.SourcePath) 'Folder permissions' } }
    return $used
}

function Get-SuggestedAddress {
    param([string]$Address, [string[]]$Here)
    $v4 = @($Here | Where-Object { $_ -match '^\d+\.\d+\.\d+\.\d+$' })
    if ($Address -match '^(\d+\.\d+\.\d+)\.\d+$') {
        $prefix = $Matches[1] + '.'
        $same = @($v4 | Where-Object { $_.StartsWith($prefix) })
        if ($same.Count) { return @{ to = $same[0]; why = 'This server''s address in the same /24 network.' } }
    }
    if ($v4.Count -eq 1) { return @{ to = $v4[0]; why = 'This server''s only IPv4 address.' } }
    if ($v4.Count) { return @{ to = $v4[0]; why = 'This server''s first IPv4 address.' } }
    return @{ to = '*'; why = 'All unassigned addresses.' }
}

function Get-Mappings {
    $result = @()
    if ($script:SameComputer) { return $result }
    $here = @(Get-ThisServerAddresses)
    foreach ($address in (Get-IisBindingAddresses)) {
        if ($here -contains $address) { continue }
        $suggested = Get-SuggestedAddress -Address $address -Here $here
        $result += @{ kind = 'ip'; from = $address; to = $suggested.to; why = $suggested.why; options = @(@('*') + $here); usedBy = @('IIS site bindings') }
    }
    $drives = @(Get-ThisServerDrives)
    $names = @($drives | ForEach-Object { $_.name })
    $used = Get-UsedDrives
    foreach ($drive in @($used.Keys | Sort-Object)) {
        if ($names -contains $drive) {
            $result += @{ kind = 'path'; from = $drive; to = $drive; why = 'This server has the same drive.'; options = $names; usedBy = @($used[$drive]) }
            continue
        }
        $system = ([string]$env:SystemDrive).ToUpperInvariant()
        $pick = @($drives | Where-Object { $_.name -ne $system } | Select-Object -First 1)
        if ($pick.Count) { $to = $pick[0].name; $why = ('{0} is not on this server; {1} is the data drive with the most free space.' -f $drive, $to) }
        else { $to = $system; $why = ('{0} is not on this server; it has only the system drive.' -f $drive) }
        $result += @{ kind = 'path'; from = $drive; to = $to; why = $why; options = $names; usedBy = @($used[$drive]) }
    }
    return $result
}

function Set-PlanMappings {
    # Takes the operator's choices from the plan. Each value must be one this
    # server offers; anything else stops the restore before it starts.
    param($Plan)
    $offered = @{}
    foreach ($mapping in (Get-Mappings)) { $offered[$mapping.kind + '|' + $mapping.from] = $mapping }
    foreach ($kind in @('ip', 'path')) {
        $choices = $null
        if ($Plan.mappings) { $choices = $Plan.mappings.$kind }
        foreach ($key in @($offered.Keys | Where-Object { $_.StartsWith($kind + '|') })) {
            $mapping = $offered[$key]
            $value = [string]$mapping.to
            if ($choices -and $choices.PSObject.Properties[$mapping.from]) { $value = [string]$choices.($mapping.from) }
            if (@($mapping.options) -notcontains $value) { throw ('{0} cannot be changed to {1} on this server.' -f $mapping.from, $value) }
            $script:Mappings[$kind][[string]$mapping.from] = $value
        }
    }
    foreach ($kind in @('ip', 'path')) { foreach ($from in @($script:Mappings[$kind].Keys)) { if ($from -ne $script:Mappings[$kind][$from]) { Write-Log 'INFO' ('Change for this server: ' + $from + ' becomes ' + $script:Mappings[$kind][$from] + '.') } } }
}

function Get-SidMap {
    # Old local account SIDs to this server's SIDs for accounts of the same
    # name. Only accounts that exist here now are mapped.
    $map = @{}
    if (-not $script:Bundle -or $script:SameComputer) { return $map }
    $accounts = $script:Bundle.localAccounts
    $machine = [string]$accounts.machineSid
    if (-not $machine) { return $map }
    foreach ($entry in @((Get-BundleList $accounts.users) + (Get-BundleList $accounts.groups))) {
        $old = [string]$entry.sid
        if (-not $old -or -not $old.StartsWith($machine + '-')) { continue }
        try {
            $new = (New-Object System.Security.Principal.NTAccount($script:ComputerName, [string]$entry.name)).Translate([System.Security.Principal.SecurityIdentifier]).Value
            if ($new -and $new -ne $old) { $map[$old] = $new }
        } catch { }
    }
    return $map
}

function Convert-SidText {
    # Replaces whole old SIDs in text (SDDL, INF, task XML) with new ones.
    param([string]$Text, [hashtable]$Map)
    if (-not $Map -or $Map.Count -eq 0) { return $Text }
    return [regex]::Replace($Text, 'S-1-5-21-\d+-\d+-\d+-\d+', { param($match) if ($Map.ContainsKey($match.Value)) { return $Map[$match.Value] } return $match.Value })
}

function Get-NotMovableRoles {
    # Roles a configuration package cannot move, with what to do instead.
    $names = @()
    if ($script:Bundle) { $names = @(Get-BundleList $script:Bundle.features) }
    else { $names = @(Import-PackageCsv '00-Summary\Installed-Server-Roles.csv' | ForEach-Object { [string]$_.Name }) }
    $guide = [ordered]@{
        'AD-Domain-Services' = 'Active Directory Domain Services: promote this server as an additional domain controller and let it replicate, then demote the old one. A package never carries the directory.'
        'ADCS-Cert-Authority' = 'Certification Authority: move it with the CA''s own backup (certutil -backup, which includes the CA key) and restore that on this server.'
        'Hyper-V' = 'Hyper-V: move the virtual machines with Move VMs or Migration plans; the role itself is installed by Roles and features.'
        'Failover-Clustering' = 'Failover clustering: build or join the cluster on this server (Build Hyper-V hosts can do it); cluster configuration is not in a package.'
        'UpdateServices' = 'WSUS: run the WSUS post-install step on this server, then copy approvals with WSUS''s own export; its database is not in a package.'
        'RDS-Connection-Broker' = 'Remote Desktop Services: add this server to the deployment with Server Manager; deployment settings are not in a package.'
    }
    $notes = @()
    foreach ($name in $guide.Keys) { if ($names -contains $name) { $notes += @{ role = $name; guidance = $guide[$name] } } }
    if (Test-Path -LiteralPath (Get-PackageFile '08-Role-Specific\Microsoft-SQL-Server') -PathType Container) {
        $notes += @{ role = 'SQL Server'; guidance = 'SQL Server: install SQL Server on this server, then restore the databases from SQL Server backups (or use Replicate servers to move the whole server). Databases are never in a package.' }
    }
    return $notes
}

# ------------------------------------------------------------ components ----

function New-RestoreItem {
    # NeedsSecret names a password typed under Changes for this server that
    # the item needs (required) or can use (optional, SecretOptional).
    param([string]$Id, [string]$Title, [string]$Detail, [string]$BlockedReason, [bool]$DefaultSelected = $true, [hashtable]$Data = @{}, [string]$NeedsSecret = '', [string]$SecretLabel = '', [bool]$SecretOptional = $false)
    return @{
        id = $Id; title = $Title; detail = $Detail
        available = -not $BlockedReason; blockedReason = [string]$BlockedReason
        defaultSelected = ($DefaultSelected -and -not $BlockedReason)
        needsSecret = $NeedsSecret; secretLabel = $SecretLabel; secretOptional = $SecretOptional
        data = $Data
    }
}

$script:FeatureState = $null
function Get-FeatureState {
    # Installed and installable feature names on this server, read once.
    if ($null -ne $script:FeatureState) { return $script:FeatureState }
    $script:FeatureState = @{ available = $false; installed = @{}; known = @{} }
    if (-not (Test-CommandAvailable 'Get-WindowsFeature' 'ServerManager')) { return $script:FeatureState }
    try {
        foreach ($feature in @(Get-WindowsFeature -ErrorAction Stop)) {
            $script:FeatureState.known[[string]$feature.Name] = $true
            if ($feature.Installed) { $script:FeatureState.installed[[string]$feature.Name] = $true }
        }
        $script:FeatureState.available = [bool](Get-Command Install-WindowsFeature -ErrorAction SilentlyContinue)
    } catch { }
    return $script:FeatureState
}

function Test-FeatureComing {
    # True when Roles and features in this restore installs the feature: the
    # old server had it, this server does not, and Windows can install it.
    param([string]$Name)
    if (-not $script:Bundle) { return $false }
    $state = Get-FeatureState
    if (-not $state.available -or $state.installed.ContainsKey($Name) -or -not $state.known.ContainsKey($Name)) { return $false }
    return (@(Get-BundleList $script:Bundle.features) -contains $Name)
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
    elseif ((-not (Test-ServiceInstalled 'DHCPServer') -or -not (Test-CommandAvailable 'Import-DhcpServer' 'DhcpServer')) -and -not (Test-FeatureComing 'DHCP')) { $blocked = 'The DHCP Server role is not installed on this server. Install it, and authorize it in Active Directory if needed, first.' }
    else { $blocked = Test-PackageFile -FullPath $file }
    $warnings = @('Scopes, options, reservations, policies and leases from the package replace the matching ones on this server.', 'The DHCP database is also backed up by Import-DhcpServer before the import.')
    if ([Environment]::OSVersion.Version.Major -ge 10) { $warnings += 'On Windows Server 2016 and later, scopes and server settings, classes, option definitions and options are restored; NAP settings are left out, and server-level policies, filters, failover and DHCPv6 are listed for you to re-create.' }
    if (-not $script:SameComputer) { $warnings += 'Restoring onto a different server: authorize this server for DHCP in Active Directory and review failover relationships and server bindings afterwards.' }
    $items = @(New-RestoreItem -Id 'dhcp' -Title 'DHCP configuration and leases' -Detail 'DHCP-Configuration-and-Leases.xml' -BlockedReason $blocked -Data @{ kind = 'config'; file = $file })
    if ($script:Bundle -and $script:Bundle.dhcp.authorized -and -not $script:SameComputer) {
        $authorize = ''
        if (-not $script:IsDomainMember) { $authorize = 'This server is not in a domain, so it is not authorized in Active Directory.' }
        elseif ((-not (Test-ServiceInstalled 'DHCPServer') -or -not (Test-CommandAvailable 'Add-DhcpServerInDC' 'DhcpServer')) -and -not (Test-FeatureComing 'DHCP')) { $authorize = 'The DHCP Server role is not installed on this server.' }
        else {
            try { if (@(Get-DhcpServerInDC -ErrorAction Stop | Where-Object { [string]$_.DnsName -like ($script:ComputerName + '*') }).Count) { $authorize = 'This server is already authorized in Active Directory.' } } catch { }
        }
        $items += New-RestoreItem -Id 'dhcp:authorize' -Title 'Authorize this server in Active Directory' -Detail ('The old server was authorized as ' + [string]$script:Bundle.dhcp.dnsName) -BlockedReason $authorize -Data @{ kind = 'authorize' }
        $warnings += 'Authorizing needs rights to change DHCP servers in Active Directory (Enterprise Admins, or delegated). Once both servers are authorized and running, clients can get leases from either: stop DHCP on the old server at cutover.'
    }
    return New-RestoreComponent -Id 'dhcp' -Title 'DHCP Server' -Description 'Scopes, options, reservations, exclusions, policies and leases.' -Impact 'high' -Warnings $warnings -Items $items
}

function Get-NpsComponent {
    $file = Get-PackageFile '08-Role-Specific\NPS\NPS-FULL-CONFIG-SENSITIVE.xml'
    $blocked = ''
    if (-not (Test-Path -LiteralPath $file)) { $blocked = 'This package has no full NPS export. It is only captured when sensitive exports were allowed and the NPS export was selected.' }
    elseif ((-not (Test-ServiceInstalled 'IAS') -or -not (Test-CommandAvailable 'Import-NpsConfiguration' 'NPS')) -and -not (Test-FeatureComing 'NPAS')) { $blocked = 'The Network Policy Server role is not installed on this server.' }
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
        if (-not (Test-Path -LiteralPath $printBrm) -and -not (Test-FeatureComing 'Print-Services')) { $blocked = 'PrintBrm.exe is not available on this server. Install the Print and Document Services role.' }
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

$script:IisAddOns = @{
    'RewriteModule' = 'URL Rewrite 2.1'
    'ApplicationRequestRouting' = 'Application Request Routing 3.0'
    'AspNetCoreModule' = 'the ASP.NET Core Hosting Bundle (ASP.NET Core Module)'
    'AspNetCoreModuleV2' = 'the ASP.NET Core Hosting Bundle (ASP.NET Core Module V2)'
    'iisnode' = 'iisnode'
    'PHP_via_FastCGI' = 'PHP'
}

function Get-IisModuleCheck {
    # Splits the old server's global modules into those missing here that
    # Windows features provide, and add-ons that must be installed first.
    $result = @{ windows = @(); addOns = @() }
    if (-not $script:Bundle) { return $result }
    $windows = ([string]$env:SystemRoot).TrimEnd('\') + '\'
    foreach ($module in (Get-BundleList $script:Bundle.iis.globalModules)) {
        $name = [string]$module.name
        $image = [Environment]::ExpandEnvironmentVariables([string]$module.image)
        if (-not $image -or (Test-Path -LiteralPath $image)) { continue }
        if ($script:IisAddOns.ContainsKey($name)) { $result.addOns += ('{0} ({1})' -f $script:IisAddOns[$name], $name); continue }
        if ($image.StartsWith($windows, [System.StringComparison]::OrdinalIgnoreCase)) { $result.windows += $name }
        else { $result.addOns += ('{0} ({1})' -f $name, [string]$module.image) }
    }
    $result.addOns = @($result.addOns | Select-Object -Unique)
    return $result
}

function Get-IisComponent {
    $folder = Get-PackageFile '08-Role-Specific\IIS\IIS-Configuration-Backup'
    $appCmd = Join-Path $env:SystemRoot 'System32\inetsrv\appcmd.exe'
    $items = @()
    $warnings = @('Replaces the whole IIS configuration: sites, bindings, application pools, applications and virtual directories.', 'IIS stops during the restore and starts again afterwards.')
    $iisHere = Test-Path -LiteralPath $appCmd
    # Website files from the migration bundle come first, so the sites find
    # their folders when the configuration is restored.
    if ($script:Bundle) {
        foreach ($entry in (Get-BundleList $script:Bundle.iis.content)) {
            if (-not $entry.included) { continue }
            $source = Join-Path $script:Package ('09-Migration-Bundle\' + [string]$entry.folder)
            $blocked = Test-PackageFolder -FullPath $source
            if (-not $blocked -and $entry.aclFile) { $blocked = Test-PackageFile -FullPath (Join-Path $script:Package ('09-Migration-Bundle\' + [string]$entry.aclFile)) }
            $target = Get-MappedPath -Path ([string]$entry.path)
            $items += New-RestoreItem -Id ('iis:files:' + [string]$entry.index) -Title ('Website files: ' + [string]$entry.path) -Detail ('{0:N1} MB, copied with their permissions' -f ([double]$entry.sizeBytes / 1MB)) -BlockedReason $blocked -Data @{ kind = 'files'; source = $source; original = [string]$entry.path; acl = [string]$entry.aclFile }
        }
        foreach ($entry in (Get-BundleList $script:Bundle.iis.content)) { if (-not $entry.included) { $warnings += ('Not in the bundle: website files of ' + [string]$entry.path + '. ' + [string]$entry.reason) } }
    }
    $blocked = ''
    if (-not (Test-Path -LiteralPath $folder)) { $blocked = 'This package has no IIS configuration backup. It is captured when the IIS export is selected.' }
    elseif (-not $iisHere -and -not (Test-FeatureComing 'Web-Server')) { $blocked = 'IIS is not installed on this server. Install the Web Server (IIS) role first, then check the package again.' }
    else { $blocked = Test-PackageFolder -FullPath $folder }
    $detail = 'IIS-Configuration-Backup'
    if (-not $blocked -and $script:Bundle) {
        $modules = Get-IisModuleCheck
        if ($modules.addOns.Count) { $blocked = 'Install these IIS add-ons on this server first, then check the package again: ' + ($modules.addOns -join '; ') + '.' }
        elseif ($modules.windows.Count) { $warnings += ('Modules provided by Windows features that are not installed here yet: ' + ($modules.windows -join ', ') + '. Restore Roles and features with IIS; the restore checks again before it changes IIS.') }
        $encrypted = [int]$script:Bundle.iis.encryptedValues
        if ($encrypted -gt 0) {
            $carried = [int]$script:Bundle.iis.passwordsInBundle
            if ($carried -gt 0) {
                $reason = Get-SecretsBlockedReason
                if ($reason) { $blocked = $reason }
                else { $detail = ('IIS-Configuration-Backup, with {0} password(s) set again for this server' -f $carried) }
            }
            if ($carried -lt $encrypted) { $warnings += ('{0} encrypted IIS setting(s) cannot be carried (they were encrypted with the old server''s keys); type them again in IIS Manager after the restore.' -f ($encrypted - $carried)) }
        }
        $https = 0
        foreach ($site in (Get-BundleList $script:Bundle.iis.sites)) { foreach ($binding in (Get-BundleList $site.bindings)) { if ([string]$binding.protocol -eq 'https') { $https++ } } }
        if ($https) { $warnings += ('{0} HTTPS binding(s) are bound to their certificates here after the restore; restore their certificates (Certificates) in the same run.' -f $https) }
    }
    elseif (-not $blocked -and -not $script:SameComputer) {
        $warnings += 'This package has no migration bundle: encrypted settings such as application pool passwords only work with the original server''s keys, HTTPS bindings need their certificates bound again, and website files are not included. Collect with Prepare this package for a new server to carry them.'
    }
    $items += New-RestoreItem -Id 'iis' -Title 'IIS configuration' -Detail $detail -BlockedReason $blocked -Data @{ kind = 'config'; folder = $folder }
    return New-RestoreComponent -Id 'iis' -Title 'IIS web server' -Description 'Website files, sites, bindings, application pools, applications and virtual directories.' -Impact 'high' -Warnings $warnings -Items $items
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

function Get-AccountSecretKey {
    param([string]$Account)
    return ('account:' + $Account.ToLowerInvariant())
}

function Get-TasksComponent {
    $folder = Get-PackageFile '05-Software-Services-Tasks\Scheduled-Task-XML-Custom'
    $files = @()
    if (Test-Path -LiteralPath $folder) { $files = @(Get-ChildItem -LiteralPath $folder -Filter '*.xml' -File -ErrorAction SilentlyContinue | Sort-Object Name) }
    $canRegister = Test-CommandAvailable 'Register-ScheduledTask' 'ScheduledTasks'
    $items = @()
    foreach ($file in $files) {
        $blocked = Test-PackageFile -FullPath $file.FullName
        $uri = ''; $user = ''; $secret = ''
        if (-not $blocked) {
            try {
                [xml]$xml = Get-Content -LiteralPath $file.FullName -Raw
                $uri = [string]$xml.Task.RegistrationInfo.URI
                $principal = $xml.Task.Principals.Principal
                $user = Convert-AccountName -Name ([string]$principal.UserId)
                # A stored password cannot be read back from Windows, so it is
                # typed under Changes for this server.
                if ([string]$principal.LogonType -eq 'Password') { $secret = Get-AccountSecretKey -Account $user }
                if (-not $uri) { $blocked = 'The task definition has no name (URI).' }
            }
            catch { $blocked = 'The task definition could not be read: ' + $_.Exception.Message }
        }
        if (-not $blocked -and -not $canRegister) { $blocked = 'The ScheduledTasks module is not available on this server.' }
        $detail = $uri
        if ($user) { $detail = $uri + ' (runs as ' + $user + ')' }
        $label = ''
        if ($secret) { $label = 'Password for ' + $user }
        $items += New-RestoreItem -Id ('tasks:' + $file.Name) -Title ($uri.Split('\')[-1]) -Detail $detail -BlockedReason $blocked -Data @{ file = $file.FullName; uri = $uri; user = $user; secret = $secret } -NeedsSecret $secret -SecretLabel $label
    }
    $warnings = @('Each selected task is registered from its saved definition; an existing task with the same name is replaced.', 'Tasks that run with a stored password need that account''s password typed under Changes for this server.')
    if (-not $script:SameComputer) { $warnings += 'Restoring onto a different server: tasks that run as the old server''s local accounts run as the accounts of the same name here, and program paths must exist.' }
    return New-RestoreComponent -Id 'tasks' -Title 'Scheduled tasks' -Description 'Custom scheduled tasks (Microsoft tasks are not included).' -Impact 'normal' -Warnings $warnings -Items $items
}

function Get-FeaturesComponent {
    $items = @()
    $warnings = @('Each missing role and feature is installed with Install-WindowsFeature. Installing roles adds their programs only; each role''s own configuration is restored by its own section below.', 'Some roles need a restart before they work. The restore says so at the end; restart before relying on them.')
    $blocked = ''
    if (-not $script:Bundle) { $blocked = 'This package has no migration bundle. Collect with Prepare this package for a new server, or use 00-Summary\Reinstall-Windows-Roles-and-Features.ps1 from the package.' }
    $state = Get-FeatureState
    if (-not $blocked -and -not $state.available) { $blocked = 'Install-WindowsFeature is not available on this computer (it needs Windows Server).' }
    if (-not $blocked) {
        $unknown = @()
        foreach ($name in (Get-BundleList $script:Bundle.features)) {
            $name = [string]$name
            if ($state.installed.ContainsKey($name)) { continue }
            if (-not $state.known.ContainsKey($name)) { $unknown += $name; continue }
            $items += New-RestoreItem -Id ('features:' + $name) -Title $name -Detail 'Installed on the old server, not here' -Data @{ name = $name }
        }
        if ($unknown.Count) { $warnings += ('Not offered by this version of Windows, so not installed: ' + ($unknown -join ', ') + '.') }
        if ($items.Count -eq 0) { $blocked = 'Every role and feature of the old server is already installed here.' }
    }
    return New-RestoreComponent -Id 'features' -Title 'Roles and features' -Description 'Windows roles and features the old server had and this server does not.' -Impact 'normal' -Warnings $warnings -Items $items -BlockedReason $blocked
}

function Get-AccountsComponent {
    $items = @()
    $blocked = ''
    $warnings = @('Windows never reveals account passwords, so they are not in any package. Type a password for each user under Changes for this server; a user left without one is created disabled with a random password.', 'Members are added to groups that already exist; nobody is removed from a group.')
    if (-not $script:Bundle) { $blocked = 'This package has no migration bundle. Collect with Prepare this package for a new server to re-create local users and groups.' }
    elseif ($script:IsDomainController) { $blocked = 'Domain controllers have no local users and groups.' }
    elseif (-not (Get-BundleList $script:Bundle.localAccounts.users).Count -and -not (Get-BundleList $script:Bundle.localAccounts.groups).Count) { $blocked = 'The old server had no local accounts to re-create (it was a domain controller).' }
    if (-not $blocked) {
        $computer = [ADSI]('WinNT://' + $script:ComputerName + ',computer')
        $exists = { param($name, $class) try { $entry = $computer.Children.Find($name, $class); return [bool]$entry } catch { return $false } }
        foreach ($user in (Get-BundleList $script:Bundle.localAccounts.users)) {
            $name = [string]$user.name
            if ($user.builtIn -or $name -in @('DefaultAccount', 'WDAGUtilityAccount', 'Guest')) { continue }
            $reason = ''
            if (& $exists $name 'User') { $reason = 'A user with this name already exists here.' }
            $account = $script:ComputerName + '\' + $name
            $detail = 'Local user'
            if ($user.fullName) { $detail += ', ' + [string]$user.fullName }
            if ($user.disabled) { $detail += ', disabled on the old server' }
            $items += New-RestoreItem -Id ('accounts:user:' + $name) -Title $name -Detail $detail -BlockedReason $reason -Data @{ kind = 'user'; name = $name } -NeedsSecret (Get-AccountSecretKey -Account $account) -SecretLabel ('Password for ' + $account) -SecretOptional $true
        }
        foreach ($group in (Get-BundleList $script:Bundle.localAccounts.groups)) {
            $name = [string]$group.name
            $members = @(Get-BundleList $group.members)
            $groupExists = & $exists $name 'Group'
            if ($group.builtIn -and $members.Count -eq 0) { continue }
            if (-not $group.builtIn -and $groupExists -and $members.Count -eq 0) { continue }
            $detail = ''
            if ($groupExists) { $detail = 'Add members: ' } else { $detail = 'Create, with members: ' }
            $names = @($members | ForEach-Object { Convert-AccountName -Name ([string]$_.name) })
            if ($names.Count) { $detail += ($names -join ', ') } else { $detail += 'none' }
            $items += New-RestoreItem -Id ('accounts:group:' + $name) -Title $name -Detail $detail -Data @{ kind = 'group'; name = $name }
        }
        if ($items.Count -eq 0) { $blocked = 'The old server''s local users and groups already exist here.' }
    }
    return New-RestoreComponent -Id 'accounts' -Title 'Local users and groups' -Description 'Local users, local groups and group members of the old server.' -Impact 'high' -Warnings $warnings -Items $items -BlockedReason $blocked
}

function Get-SharesComponent {
    $items = @()
    $blocked = ''
    $warnings = @('Each share is created with the old share''s settings and permissions. A missing folder is created empty: copy the files with Move file shares (it keeps copying until cutover) or your backup.', 'Permissions given to the old server''s local accounts are given to the accounts of the same name here.')
    if (-not $script:Bundle) { $blocked = 'This package has no migration bundle. Move shares with Move file shares, or collect with Prepare this package for a new server.' }
    elseif (-not (Test-CommandAvailable 'New-SmbShare' 'SmbShare')) { $blocked = 'New-SmbShare is not available on this server.' }
    if (-not $blocked) {
        $existing = @{}
        try { foreach ($share in @(Get-SmbShare -ErrorAction Stop)) { $existing[([string]$share.Name).ToLowerInvariant()] = [string]$share.Path } } catch { }
        foreach ($share in (Get-BundleList $script:Bundle.shares)) {
            if ($share.special) { continue }
            $name = [string]$share.name
            $path = Get-MappedPath -Path ([string]$share.path)
            $reason = ''
            if ($share.clustered) { $reason = 'A clustered or scoped share is re-created in Failover Cluster Manager.' }
            elseif ($existing.ContainsKey($name.ToLowerInvariant())) { $reason = ('A share with this name already exists here ({0}). Move file shares can replace it.' -f $existing[$name.ToLowerInvariant()]) }
            elseif (-not (Get-DriveOf -Path $path)) { $reason = 'The share''s folder is not on a local drive.' }
            elseif (-not (Test-Path -LiteralPath ((Get-DriveOf -Path $path) + '\'))) { $reason = ('Drive {0} does not exist here. Change it under Changes for this server.' -f (Get-DriveOf -Path $path)) }
            $detail = $path
            if (-not (Test-Path -LiteralPath $path -PathType Container)) { $detail += ' (folder created empty)' }
            $rights = @(Get-BundleList $share.permissions | ForEach-Object { (Convert-AccountName -Name ([string]$_.account)) + ' ' + [string]$_.right })
            if ($rights.Count) { $detail += '  ' + ($rights -join ', ') }
            $items += New-RestoreItem -Id ('shares:' + $name) -Title $name -Detail $detail -BlockedReason $reason -Data @{ name = $name }
        }
        if ($items.Count -eq 0) { $blocked = 'The old server had no file shares to re-create.' }
    }
    return New-RestoreComponent -Id 'shares' -Title 'File shares' -Description 'Share names, folders, settings and share permissions.' -Impact 'normal' -Warnings $warnings -Items $items -BlockedReason $blocked
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
    $withKey = @{}
    # Certificates with their private keys, from the migration bundle.
    if ($script:Bundle) {
        $secretsReason = Get-SecretsBlockedReason
        foreach ($certificate in (Get-BundleList $script:Bundle.certificates)) {
            if (-not $certificate.inBundle) { continue }
            $thumbprint = ([string]$certificate.thumbprint).ToUpperInvariant()
            $store = [string]$certificate.store
            $withKey[$thumbprint] = $true
            $blocked = $secretsReason
            if (-not $blocked -and $store -notmatch '^LocalMachine\\(My|WebHosting)$') { $blocked = 'The certificate''s store is not one the restore writes to.' }
            if (-not $blocked) {
                $here = Get-Item -LiteralPath ('Cert:\' + $store + '\' + $thumbprint) -ErrorAction SilentlyContinue
                if ($here -and $here.HasPrivateKey) { $blocked = 'Already installed with its private key in ' + $store + '.' }
            }
            $used = @(Get-BundleList $certificate.usedBy)
            $detail = $store + '  ' + $thumbprint + '  expires ' + [string]$certificate.notAfter
            if ($used.Count) { $detail += '  used by ' + ($used -join '; ') }
            $items += New-RestoreItem -Id ('certificates:pfx:' + $thumbprint) -Title ([string]$certificate.subject + ' (with private key)') -Detail $detail -BlockedReason $blocked -Data @{ kind = 'pfx'; store = $store; thumbprint = $thumbprint }
        }
    }
    foreach ($file in $files) {
        $thumbprint = ($file.BaseName -split '_')[-1].ToUpperInvariant()
        if ($withKey.ContainsKey($thumbprint)) { continue }
        $store = [string]$stores[$thumbprint]
        $subject = $file.BaseName.Substring(0, [Math]::Max(0, $file.BaseName.Length - $thumbprint.Length - 1))
        $blocked = Test-PackageFile -FullPath $file.FullName
        if (-not $blocked -and $store -notmatch '^LocalMachine\\[A-Za-z]+$') { $blocked = 'The certificate''s original store is not recorded in the package.' }
        if (-not $blocked -and (Test-Path -LiteralPath ('Cert:\' + $store + '\' + $thumbprint))) { $blocked = 'Already installed in ' + $store + '.' }
        if (-not $blocked -and -not $canImport) { $blocked = 'The PKI module is not available on this server.' }
        $items += New-RestoreItem -Id ('certificates:' + $file.Name) -Title $subject -Detail ($store + '  ' + $thumbprint) -BlockedReason $blocked -Data @{ kind = 'cer'; file = $file.FullName; store = $store; thumbprint = $thumbprint }
    }
    $warnings = @('Public certificates are added to the store they came from, including trusted root and intermediate authorities.')
    if ($withKey.Count) { $warnings += 'Certificates with private keys are imported from the migration bundle with their keys marked exportable, as they were on the old server.' }
    else { $warnings += 'Private keys were not exported, so restored certificates in the Personal store cannot be used for TLS or signing; re-import those from a protected PFX, or collect with Prepare this package for a new server.' }
    if ($script:Bundle) {
        foreach ($certificate in (Get-BundleList $script:Bundle.certificates)) { if (-not $certificate.inBundle) { $warnings += ('Not in the bundle: ' + [string]$certificate.subject + ' (' + [string]$certificate.thumbprint + '). ' + [string]$certificate.reason) } }
    }
    return New-RestoreComponent -Id 'certificates' -Title 'Certificates' -Description 'Certificates with their private keys, and public certificates missing from this server''s stores.' -Impact 'high' -Warnings $warnings -Items $items
}

function Get-AclsComponent {
    $folder = Get-PackageFile '07-Shares-Permissions\Recursive-ICACLS-Backups'
    $items = @()
    $shareFolders = @{}
    if ($script:Bundle) { foreach ($share in (Get-BundleList $script:Bundle.shares)) { if (-not $share.special) { $shareFolders[(Get-MappedPath -Path ([string]$share.path)).TrimEnd('\').ToLowerInvariant()] = $true } } }
    foreach ($row in (Import-PackageCsv '07-Shares-Permissions\Recursive-ICACLS-Backups\ICACLS-Mapping.csv')) {
        if ([string]$row.Status -ne 'Completed' -or -not $row.AclFile) { continue }
        $file = Join-Path $folder ([string]$row.AclFile)
        $source = [string]$row.SourcePath
        $target = Get-MappedPath -Path $source
        $blocked = Test-PackageFile -FullPath $file
        if (-not $blocked -and -not $source) { $blocked = 'The original folder path is not recorded.' }
        if (-not $blocked -and ([System.IO.Path]::GetPathRoot($source).TrimEnd('\') -eq $source.TrimEnd('\'))) { $blocked = 'Permissions saved for a whole drive are restored manually with ICACLS.' }
        # File shares in the same run create missing share folders first.
        if (-not $blocked -and -not (Test-Path -LiteralPath $target -PathType Container) -and -not $shareFolders.ContainsKey($target.TrimEnd('\').ToLowerInvariant())) { $blocked = ('The folder {0} does not exist on this server. Copy the files first (Move file shares), or change its drive under Changes for this server.' -f $target) }
        $detail = $target
        if ($target -ne $source) { $detail = $source + ' -> ' + $target }
        $items += New-RestoreItem -Id ('acls:' + [string]$row.AclFile) -Title ([string]$row.ShareName) -Detail $detail -BlockedReason $blocked -Data @{ file = $file; source = $source }
    }
    $warnings = @('NTFS permissions on every file and folder under each selected share are set back to the saved permissions; files added since keep theirs.', 'The current permissions are saved first, which can take a while on large folders.')
    if (-not $script:SameComputer) {
        if ($script:Bundle) { $warnings += 'Permissions of the old server''s local accounts are given to the accounts of the same name here (Local users and groups re-creates them first).' }
        else { $warnings += 'Restoring onto a different server: permissions for local accounts of the original server cannot be resolved here.' }
    }
    return New-RestoreComponent -Id 'acls' -Title 'Share folder permissions (NTFS)' -Description 'Complete recursive permission saves for shared folders.' -Impact 'high' -Warnings $warnings -Items $items
}

function Get-DnsComponent {
    $zones = @(Import-PackageCsv '08-Role-Specific\DNS\DNS-Zones.csv')
    $hasDns = (Test-ServiceInstalled 'DNS') -and (Test-CommandAvailable 'Add-DnsServerPrimaryZone' 'DnsServer')
    $coming = (-not $hasDns) -and (Test-FeatureComing 'DNS')
    $existing = @{}
    if ($hasDns) { try { foreach ($zone in @(Get-DnsServerZone -ErrorAction Stop)) { $existing[([string]$zone.ZoneName).ToLowerInvariant()] = $true } } catch { } }
    $noDns = ''
    if (-not $hasDns -and -not $coming) { $noDns = 'The DNS Server role is not installed on this server.' }
    $items = @()
    $anyDomain = $false
    foreach ($zone in $zones) {
        $name = [string]$zone.ZoneName
        if (-not $name -or $name -eq 'TrustAnchors' -or $name -match '^(0|127|255)\.in-addr\.arpa$') { continue }
        if ([string]$zone.ZoneType -ne 'Primary') { continue }
        $folder = Get-PackageFile ('08-Role-Specific\DNS\Zone-' + (Get-SafeFileName -Name $name))
        $file = $null
        if (Test-Path -LiteralPath (Join-Path $folder 'AD-Integrated-Zone-Export.txt')) { $file = Join-Path $folder 'AD-Integrated-Zone-Export.txt' }
        else { $candidate = @(Get-ChildItem -LiteralPath $folder -Filter 'File-Backed-Zone-*' -File -ErrorAction SilentlyContinue | Select-Object -First 1); if ($candidate.Count) { $file = $candidate[0].FullName } }
        if (-not $file) { continue }
        $dsIntegrated = [string]$zone.IsDsIntegrated -eq 'True'
        $blocked = Test-PackageFile -FullPath $file
        if (-not $blocked) { $blocked = $noDns }
        if (-not $blocked -and $existing.ContainsKey($name.ToLowerInvariant())) { $blocked = 'The zone already exists on this server. Only missing zones are restored.' }
        if ($dsIntegrated) { $anyDomain = $true }
        $detail = 'File-backed primary zone'
        if ($dsIntegrated) { $detail = 'Active Directory-integrated zone (' + [string]$zone.ReplicationScope + ')' }
        $items += New-RestoreItem -Id ('dns:' + $name) -Title $name -Detail $detail -BlockedReason $blocked -Data @{ kind = 'primary'; file = $file; zone = $name; dsIntegrated = $dsIntegrated; scope = [string]$zone.ReplicationScope }
    }
    if ($script:Bundle -and $script:Bundle.dns.installed) {
        $forwarders = @(Get-BundleList $script:Bundle.dns.forwarders)
        if ($forwarders.Count) {
            $items += New-RestoreItem -Id 'dns:forwarders' -Title 'Forwarders' -Detail (($forwarders -join ', ') + $(if ($script:Bundle.dns.useRootHint) { ', then root hints' } else { ', no root hints' })) -BlockedReason $noDns -Data @{ kind = 'forwarders'; addresses = $forwarders; useRootHint = [bool]$script:Bundle.dns.useRootHint }
        }
        foreach ($zone in (Get-BundleList $script:Bundle.dns.zones)) {
            $name = [string]$zone.name
            $type = [string]$zone.type
            $masters = @(Get-BundleList $zone.masters)
            $blocked = $noDns
            if (-not $blocked -and $existing.ContainsKey($name.ToLowerInvariant())) { $blocked = 'The zone already exists on this server.' }
            if (-not $blocked -and $masters.Count -eq 0) { $blocked = 'The zone has no master servers recorded.' }
            $label = @{ Forwarder = 'Conditional forwarder'; Secondary = 'Secondary zone'; Stub = 'Stub zone' }[$type]
            if ($zone.dsIntegrated) { $anyDomain = $true; $label += ' stored in Active Directory' }
            $items += New-RestoreItem -Id ('dns:' + $type.ToLowerInvariant() + ':' + $name) -Title $name -Detail ($label + ', masters ' + ($masters -join ', ')) -BlockedReason $blocked -Data @{ kind = 'other'; zone = $name; type = $type; masters = $masters; dsIntegrated = [bool]$zone.dsIntegrated; scope = [string]$zone.replicationScope }
        }
    }
    $impact = 'normal'
    if ($anyDomain) { $impact = 'domain' }
    $warnings = @('Missing zones are re-created with all records from the package.', 'Active Directory-integrated zones are stored in Active Directory again when this server is a domain controller, and then replicate to other DNS servers in the domain.')
    if ($script:Bundle -and $script:Bundle.dns.installed) { $warnings += 'Forwarders replace this server''s forwarders. Secondary and stub zones transfer from their master servers, which must allow zone transfers to this server.' }
    return New-RestoreComponent -Id 'dns' -Title 'DNS server' -Description 'Missing zones, conditional forwarders and forwarders.' -Impact $impact -Warnings $warnings -Items $items
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
# Roles and features and local accounts come first, because the rest needs
# them; file shares before folder permissions, certificates before IIS.
$script:ComponentOrder = @('features', 'accounts', 'registry', 'secpol', 'certificates', 'firewall', 'shares', 'acls', 'tasks', 'printers', 'iis', 'dhcp', 'nps', 'dns', 'gpo')

function Get-AllComponents {
    $builders = @{
        registry = { Get-RegistryComponent }; secpol = { Get-SecpolComponent }; certificates = { Get-CertificatesComponent }
        firewall = { Get-FirewallComponent }; acls = { Get-AclsComponent }; tasks = { Get-TasksComponent }
        printers = { Get-PrintersComponent }; iis = { Get-IisComponent }; dhcp = { Get-DhcpComponent }
        nps = { Get-NpsComponent }; dns = { Get-DnsComponent }; gpo = { Get-GpoComponent }
        features = { Get-FeaturesComponent }; accounts = { Get-AccountsComponent }; shares = { Get-SharesComponent }
    }
    $display = @('features', 'accounts', 'shares', 'iis', 'certificates', 'dhcp', 'nps', 'printers', 'dns', 'gpo', 'firewall', 'tasks', 'acls', 'registry', 'secpol')
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

function Get-XmlChildText {
    param($Node, [string]$Name)
    $child = @($Node.ChildNodes | Where-Object { $_.LocalName -eq $Name })
    if ($child.Count -eq 0) { return '' }
    return [string]$child[0].InnerText
}

function Get-XmlChildren {
    param($Node, [string]$Name)
    if (-not $Node) { return @() }
    return @($Node.ChildNodes | Where-Object { $_.LocalName -eq $Name })
}

function Restore-DhcpServerLevel {
    # Applies the IPv4 server-level part of a DHCP export without the
    # Network Access Protection settings that Windows Server 2016 and later
    # refuse. Scopes are imported separately.
    param($IPv4)
    $settings = @{}
    $conflict = Get-XmlChildText $IPv4 'ConflictDetectionAttempts'
    if ($conflict -ne '') { $settings['ConflictDetectionAttempts'] = [uint32]$conflict }
    $activate = Get-XmlChildText $IPv4 'ActivatePolicies'
    if ($activate -ne '') { $settings['ActivatePolicies'] = ($activate -eq 'true') }
    if ($settings.Count -gt 0) { Set-DhcpServerSetting -ComputerName $script:ComputerName @settings; Write-Log 'INFO' ('Applied DHCP server settings: ' + (($settings.Keys | Sort-Object) -join ', ')) }

    $existingClasses = @{}
    foreach ($class in @(Get-DhcpServerv4Class -ComputerName $script:ComputerName)) { $existingClasses[$class.Type + '|' + $class.Name] = $true }
    foreach ($class in (Get-XmlChildren (@(Get-XmlChildren $IPv4 'Classes'))[0] 'Class')) {
        $name = Get-XmlChildText $class 'Name'; $type = Get-XmlChildText $class 'Type'
        if (-not $name -or $existingClasses.ContainsKey($type + '|' + $name)) { continue }
        $data = (Get-XmlChildText $class 'Data') -replace '^0x', ''
        Add-DhcpServerv4Class -ComputerName $script:ComputerName -Name $name -Type $type -Data ([Text.Encoding]::ASCII.GetString([byte[]]@(for ($i = 0; $i -lt $data.Length; $i += 2) { [Convert]::ToByte($data.Substring($i, 2), 16) }))) -Description (Get-XmlChildText $class 'Description')
        Write-Log 'INFO' ('Added DHCP class ' + $name)
    }

    $existingDefinitions = @{}
    foreach ($definition in @(Get-DhcpServerv4OptionDefinition -ComputerName $script:ComputerName -All)) { $existingDefinitions[[string]$definition.VendorClass + '|' + $definition.OptionId] = $true }
    foreach ($definition in (Get-XmlChildren (@(Get-XmlChildren $IPv4 'OptionDefinitions'))[0] 'OptionDefinition')) {
        $id = Get-XmlChildText $definition 'OptionId'; $vendor = Get-XmlChildText $definition 'VendorClass'
        if (-not $id -or $existingDefinitions.ContainsKey($vendor + '|' + $id)) { continue }
        $arguments = @{ ComputerName = $script:ComputerName; Name = (Get-XmlChildText $definition 'Name'); OptionId = [uint32]$id; Type = (Get-XmlChildText $definition 'Type'); MultiValued = ((Get-XmlChildText $definition 'MultiValued') -eq 'true') }
        if ($vendor) { $arguments['VendorClass'] = $vendor }
        $description = Get-XmlChildText $definition 'Description'; if ($description) { $arguments['Description'] = $description }
        $defaults = @(Get-XmlChildren $definition 'DefaultValue' | ForEach-Object { [string]$_.InnerText } | Where-Object { $_ -ne '' })
        if ($defaults.Count) { $arguments['DefaultValue'] = $defaults }
        Add-DhcpServerv4OptionDefinition @arguments
        Write-Log 'INFO' ('Added DHCP option definition ' + $id + ' ' + $arguments['Name'])
    }

    foreach ($option in (Get-XmlChildren (@(Get-XmlChildren $IPv4 'OptionValues'))[0] 'OptionValue')) {
        $id = Get-XmlChildText $option 'OptionId'
        $values = @(Get-XmlChildren $option 'Value' | ForEach-Object { [string]$_.InnerText })
        if (-not $id -or $values.Count -eq 0) { continue }
        $arguments = @{ ComputerName = $script:ComputerName; OptionId = [uint32]$id; Value = $values; Force = $true }
        $vendor = Get-XmlChildText $option 'VendorClass'; if ($vendor) { $arguments['VendorClass'] = $vendor }
        $user = Get-XmlChildText $option 'UserClass'; if ($user) { $arguments['UserClass'] = $user }
        Set-DhcpServerv4OptionValue @arguments
        Write-Log 'INFO' ('Set DHCP server option ' + $id)
    }

    $notApplied = @()
    # Count real entries, not the empty containers every export carries.
    $sections = [ordered]@{ Policies = 'Policy'; Filters = 'Filter'; FailoverRelationships = 'Failover'; SuperScopes = 'SuperScope'; MulticastScopes = 'MulticastScope' }
    foreach ($name in $sections.Keys) {
        $section = @(Get-XmlChildren $IPv4 $name)
        if ($section.Count -and $section[0].SelectNodes(('.//*[local-name()="{0}"]' -f $sections[$name])).Count) { $notApplied += $name }
    }
    if ($notApplied.Count) { Write-Log 'WARN' ('Not restored on this Windows version, review and re-create them: server-level DHCP ' + ($notApplied -join ', ') + '.') }
}

function Restore-Dhcp {
    param([hashtable]$Data, [string]$Rollback)
    if ($Data.kind -eq 'authorize') { Restore-DhcpAuthorization -Rollback $Rollback; return }
    Restore-DhcpConfig -Data $Data -Rollback $Rollback
}

function Restore-DhcpAuthorization {
    param([string]$Rollback)
    Import-Module DhcpServer -ErrorAction Stop
    $fqdn = $script:ComputerName
    try { $system = Get-WmiObject -Class Win32_ComputerSystem; if ($system.PartOfDomain) { $fqdn = $script:ComputerName + '.' + $system.Domain } } catch { }
    $addresses = @(Get-ThisServerAddresses | Where-Object { $_ -match '^\d+\.\d+\.\d+\.\d+$' })
    if (-not $addresses.Count) { throw 'This server has no IPv4 address to authorize.' }
    Add-DhcpServerInDC -DnsName $fqdn -IPAddress $addresses[0]
    Add-Content -LiteralPath (Join-Path $Rollback 'DHCP-Authorized.txt') -Value ($fqdn + ' ' + $addresses[0]) -Encoding UTF8
    Write-Log 'INFO' ('Authorized ' + $fqdn + ' (' + $addresses[0] + ') as a DHCP server in Active Directory.')
}

function Restore-DhcpConfig {
    param([hashtable]$Data, [string]$Rollback)
    Import-Module DhcpServer -ErrorAction Stop
    Export-DhcpServer -ComputerName $script:ComputerName -File (Join-Path $Rollback 'Current-DHCP-Configuration-and-Leases.xml') -Leases -Force
    Write-Log 'INFO' 'Saved the current DHCP configuration and leases.'
    $backupPath = Join-Path $Rollback 'DHCP-Database-Backup'
    if ([Environment]::OSVersion.Version.Major -lt 10) {
        Import-DhcpServer -ComputerName $script:ComputerName -File $Data.file -BackupPath $backupPath -Leases -ScopeOverwrite -Force
        return
    }
    # Windows Server 2016 and later: Import-DhcpServer always re-applies the
    # removed Network Access Protection settings and the server refuses them,
    # even for an export taken from this server. Apply the server level here
    # without NAP, then import the scopes on their own.
    [xml]$xml = Get-Content -LiteralPath $Data.file -Raw
    $ipv4 = @(Get-XmlChildren $xml.DocumentElement 'IPv4')
    if ($ipv4.Count -eq 0) { throw 'The DHCP export has no IPv4 configuration.' }
    Restore-DhcpServerLevel -IPv4 $ipv4[0]
    $scopeIds = @(Get-XmlChildren (@(Get-XmlChildren $ipv4[0] 'Scopes'))[0] 'Scope' | ForEach-Object { Get-XmlChildText $_ 'ScopeId' } | Where-Object { $_ })
    if ($scopeIds.Count -gt 0) {
        Import-DhcpServer -ComputerName $script:ComputerName -File $Data.file -BackupPath $backupPath -Leases -ScopeOverwrite -ScopeId $scopeIds -Force
        Write-Log 'INFO' ('Imported {0} DHCP scope(s) with their options, reservations, exclusions, policies and leases.' -f $scopeIds.Count)
    }
    $ipv6 = @(Get-XmlChildren $xml.DocumentElement 'IPv6')
    if ($ipv6.Count -and ($ipv6[0].SelectNodes('.//*[local-name()="Scope"]').Count -or $ipv6[0].SelectNodes('.//*[local-name()="OptionValue"]').Count)) { Write-Log 'WARN' 'DHCPv6 configuration in the export is not restored on this Windows version; review it in the DHCP console.' }
}

function Restore-Nps {
    param([hashtable]$Data, [string]$Rollback)
    Import-Module NPS -ErrorAction Stop
    Export-NpsConfiguration -Path (Join-Path $Rollback 'Current-NPS-CONFIG-SENSITIVE.xml')
    Write-Log 'WARN' 'Saved the current NPS configuration; it contains shared secrets.'
    Import-NpsConfiguration -Path $Data.file
}

function Assert-PrintBrmSucceeded {
    # PrintBrm.exe can exit with 0 after reporting an error, so read its output.
    param([string]$What)
    # PrintBRM text is localized; its error code (0x8xxxxxxx) is not.
    if ($script:LastNativeOutput -match '0x8[0-9A-Fa-f]{7}') {
        throw ($What + ' reported error ' + $Matches[0] + '. See the PrintBRM output above and the Microsoft-Windows-PrintBRM/Admin event log.')
    }
}

function Restore-Printers {
    param([hashtable]$Data, [string]$Rollback)
    $printBrm = Join-Path $env:SystemRoot 'System32\spool\tools\PrintBrm.exe'
    $snapshot = Join-Path $Rollback 'Current-Printers.printerExport'
    Assert-Native -FilePath $printBrm -Arguments @('-B', '-F', $snapshot) -What 'Saving the current printers'
    Assert-PrintBrmSucceeded -What 'Saving the current printers'
    if (-not (Test-Path -LiteralPath $snapshot)) { throw 'PrintBRM did not save the current printers, so the restore was not started.' }
    Assert-Native -FilePath $printBrm -Arguments @('-R', '-F', $Data.file, '-O', 'FORCE') -What 'PrintBRM restore'
    Assert-PrintBrmSucceeded -What 'PrintBRM restore'
}

function Restore-Iis {
    param([hashtable]$Data, [string]$Rollback)
    if ($Data.kind -eq 'files') { Restore-IisFiles -Data $Data -Rollback $Rollback; return }
    $appCmd = Join-Path $env:SystemRoot 'System32\inetsrv\appcmd.exe'
    if (-not (Test-Path -LiteralPath $appCmd)) { throw 'IIS is not installed on this server.' }
    if ($script:Bundle) {
        $modules = Get-IisModuleCheck
        $missing = @($modules.addOns) + @($modules.windows)
        if ($missing.Count) { throw ('These IIS modules the sites use are not installed here: ' + ($missing -join '; ') + '. Install them (Roles and features installs the Windows ones), then restore IIS again.') }
    }
    $backupRoot = Join-Path $env:SystemRoot 'System32\inetsrv\backup'
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $current = 'SICM-PreRestore-' + $stamp
    Assert-Native -FilePath $appCmd -Arguments @('add', 'backup', $current) -What 'Saving the current IIS configuration'
    Copy-Item -LiteralPath (Join-Path $backupRoot $current) -Destination (Join-Path $Rollback 'Current-IIS-Configuration') -Recurse -Force
    $restoreName = 'SICM-Restore-' + $stamp
    Copy-Item -LiteralPath $Data.folder -Destination (Join-Path $backupRoot $restoreName) -Recurse -Force
    try {
        if ($script:Bundle -and -not $script:SameComputer) { Convert-IisConfigurationForThisServer -Folder (Join-Path $backupRoot $restoreName) }
        Assert-Native -FilePath $appCmd -Arguments @('restore', 'backup', $restoreName, '/stop:true') -What 'IIS restore'
    }
    finally {
        foreach ($service in @('WAS', 'W3SVC')) { try { Start-Service -Name $service -ErrorAction Stop } catch { Write-Log 'WARN' ('Could not start ' + $service + ': ' + $_.Exception.Message) } }
        Invoke-Native -FilePath $appCmd -Arguments @('delete', 'backup', $restoreName) | Out-Null
    }
    if ($script:Bundle -and -not $script:SameComputer -and [int]$script:Bundle.iis.passwordsInBundle -gt 0) { Set-IisPasswords }
    if ($script:Bundle) { Set-IisHttpsBindings -Rollback $Rollback }
    $sites = Invoke-Native -FilePath $appCmd -Arguments @('list', 'site')
    if ($sites -eq 0 -and $script:LastNativeOutput -match 'state:Stopped') { Write-Log 'WARN' 'Some sites are stopped after the restore. Check their bindings and folders in IIS Manager.' }
}

function Restore-IisFiles {
    param([hashtable]$Data, [string]$Rollback)
    $robocopy = Join-Path $env:SystemRoot 'System32\robocopy.exe'
    $target = Get-MappedPath -Path $Data.original
    if (-not (Test-Path -LiteralPath ((Get-DriveOf -Path $target) + '\'))) { throw ('Drive {0} does not exist on this server.' -f (Get-DriveOf -Path $target)) }
    if ((Test-Path -LiteralPath $target -PathType Container) -and @(Get-ChildItem -LiteralPath $target -Force -ErrorAction SilentlyContinue | Select-Object -First 1).Count) {
        # Files with the same names are replaced, so keep what was there.
        $before = Join-Path $Rollback ('Website-Files-Before\' + (Get-SafeFileName -Name $target))
        Write-Log 'INFO' ('Saving the files already in ' + $target + ' to the rollback folder.')
        $code = Invoke-Native -FilePath $robocopy -Arguments @($target, $before, '/E', '/B', '/COPY:DATSO', '/DCOPY:T', '/XJ', '/R:1', '/W:1', '/NP', '/NFL', '/NDL', '/NJH')
        if ($code -ge 8) { throw ('Saving the files already in {0} failed (Robocopy exit code {1}).' -f $target, $code) }
    }
    Write-Log 'INFO' ('Copying website files to ' + $target)
    $code = Invoke-Native -FilePath $robocopy -Arguments @($Data.source, $target, '/E', '/B', '/COPY:DAT', '/DCOPY:T', '/XJ', '/R:1', '/W:1', '/NP', '/NFL', '/NDL', '/NJH')
    if ($code -ge 8) { throw ('Copying the website files to {0} failed (Robocopy exit code {1}).' -f $target, $code) }
    if ($Data.acl) {
        $acl = Join-Path $script:Package ('09-Migration-Bundle\' + $Data.acl)
        $reason = Test-PackageFile -FullPath $acl
        if ($reason) { throw $reason }
        Restore-AclFile -File $acl -Target $target -Rollback $Rollback
    }
}

function Convert-IisConfigurationForThisServer {
    # Edits the copy of applicationHost.config that is about to be restored:
    # the old server's IP addresses in bindings, its drives in paths and its
    # computer name in account names become this server's, as chosen under
    # Changes for this server. The package's own copy is never changed.
    param([string]$Folder)
    $path = Join-Path $Folder 'applicationHost.config'
    if (-not (Test-Path -LiteralPath $path)) { throw 'The IIS backup has no applicationHost.config.' }
    $document = New-Object System.Xml.XmlDocument
    $document.PreserveWhitespace = $true
    $document.Load($path)
    $source = Get-SourceComputer
    $changes = 0
    $removed = @()
    foreach ($node in @($document.SelectNodes('//*[@*]'))) {
        foreach ($attribute in @($node.Attributes)) {
            $value = [string]$attribute.Value
            $new = $value
            if ($node.LocalName -eq 'binding' -and $attribute.LocalName -eq 'bindingInformation') {
                $parts = $value.Split(':')
                if ($parts.Count -ge 3) {
                    $address = ($parts[0..($parts.Count - 3)] -join ':').Trim('[', ']')
                    $mapped = Get-MappedAddress -Address $address
                    if ($mapped -ne $address) {
                        if ($mapped -match ':') { $mapped = '[' + $mapped + ']' }
                        $new = $mapped + ':' + $parts[$parts.Count - 2] + ':' + $parts[$parts.Count - 1]
                    }
                }
            }
            elseif ($value.StartsWith('[enc:', [System.StringComparison]::OrdinalIgnoreCase)) {
                # Encrypted with the old server's keys, which never leave it:
                # removed here and set again through IIS after the restore.
                $removed += $attribute
                continue
            }
            elseif ($value -match '^[A-Za-z]:\\') { $new = Get-MappedPath -Path $value }
            elseif ($source -and $value.StartsWith($source + '\', [System.StringComparison]::OrdinalIgnoreCase)) { $new = Convert-AccountName -Name $value }
            if ($new -ne $value) { $attribute.Value = $new; $changes++ }
        }
    }
    foreach ($attribute in $removed) { [void]$attribute.OwnerElement.Attributes.Remove($attribute) }
    if ($removed.Count) { Write-Log 'INFO' ('Left out {0} value(s) encrypted with the old server''s keys.' -f $removed.Count) }
    $changes += $removed.Count
    if ($changes) {
        $settings = New-Object System.Xml.XmlWriterSettings
        $settings.Encoding = New-Object System.Text.UTF8Encoding($false)
        $writer = [System.Xml.XmlWriter]::Create($path, $settings)
        try { $document.Save($writer) } finally { $writer.Close() }
        Write-Log 'INFO' ('Changed {0} IIS setting(s) for this server (addresses, drives and account names).' -f $changes)
    }
}

function Set-IisPasswords {
    # Sets the application pool, virtual directory and anonymous user
    # passwords the bundle carries through IIS's own API, which encrypts them
    # with this server's keys. Only values the restored configuration names
    # are set; a pool, site or location that is not there is reported.
    if ($script:PassphraseState -ne 'ok') { throw (Get-SecretsBlockedReason) }
    $dll = Join-Path $env:SystemRoot 'System32\inetsrv\Microsoft.Web.Administration.dll'
    if (-not (Test-Path -LiteralPath $dll)) { Write-Log 'WARN' 'Microsoft.Web.Administration is not available, so the IIS passwords were not set. Type them in IIS Manager.'; return }
    [void][System.Reflection.Assembly]::LoadFrom($dll)
    $manager = New-Object Microsoft.Web.Administration.ServerManager
    $set = 0
    try {
        foreach ($entry in (Get-BundleList $script:Secrets.iisPasswords)) {
            $value = [string]$entry.value
            if (-not $value) { continue }
            $found = $false
            switch ([string]$entry.kind) {
                'apppool' {
                    $pool = $manager.ApplicationPools[[string]$entry.name]
                    if ($pool) { $pool.ProcessModel.Password = $value; $found = $true }
                    $where = 'application pool ' + $entry.name
                }
                'apppoolDefaults' { $manager.ApplicationPoolDefaults.ProcessModel.Password = $value; $found = $true; $where = 'application pool defaults' }
                'vdirDefaults' { $manager.VirtualDirectoryDefaults.Password = $value; $found = $true; $where = 'virtual directory defaults' }
                'vdir' {
                    $where = 'virtual directory ' + $entry.site + $entry.application + ' ' + $entry.path
                    $site = $manager.Sites[[string]$entry.site]
                    if ($site) {
                        $application = $site.Applications[[string]$entry.application]
                        if ($application) {
                            $directory = $application.VirtualDirectories[[string]$entry.path]
                            if ($directory) { $directory.Password = $value; $found = $true }
                        }
                    }
                }
                'anonymous' {
                    $where = 'anonymous authentication ' + $entry.location
                    $configuration = $manager.GetApplicationHostConfiguration()
                    try {
                        if ($entry.location) { $section = $configuration.GetSection('system.webServer/security/authentication/anonymousAuthentication', [string]$entry.location) }
                        else { $section = $configuration.GetSection('system.webServer/security/authentication/anonymousAuthentication') }
                        $section.SetAttributeValue('password', $value)
                        $found = $true
                    } catch { }
                }
                default { $where = [string]$entry.kind }
            }
            if ($found) { $set++ } else { Write-Log 'WARN' ('The IIS password for ' + $where + ' was not set because it is not in the restored configuration.') }
        }
        if ($set) { $manager.CommitChanges() }
    }
    finally { $manager.Dispose() }
    Write-Log 'INFO' ('Set {0} IIS password(s) again, encrypted for this server.' -f $set)
}

function Set-IisHttpsBindings {
    # Binds each HTTPS binding's certificate in HTTP.sys, the way IIS Manager
    # does, because a configuration restore does not.
    param([string]$Rollback)
    $netsh = Join-Path $env:SystemRoot 'System32\netsh.exe'
    $appId = '{4dc3e181-e14b-4a21-b022-59fc669b0914}'
    $saved = $false
    foreach ($site in (Get-BundleList $script:Bundle.iis.sites)) {
        foreach ($binding in (Get-BundleList $site.bindings)) {
            if ([string]$binding.protocol -ne 'https') { continue }
            $hash = ([string]$binding.certificateHash).ToUpperInvariant()
            $flags = 0; [void][int]::TryParse([string]$binding.sslFlags, [ref]$flags)
            $parts = ([string]$binding.bindingInformation).Split(':')
            if ($parts.Count -lt 3) { continue }
            $port = $parts[$parts.Count - 2]; $hostName = $parts[$parts.Count - 1]
            $address = Get-MappedAddress -Address (($parts[0..($parts.Count - 3)] -join ':').Trim('[', ']'))
            if ($flags -band 2) { Write-Log 'WARN' ('Site ' + $site.name + ' uses the IIS Central Certificate Store for ' + $binding.bindingInformation + '; configure the store on this server.'); continue }
            if (-not $hash) { Write-Log 'WARN' ('Site ' + $site.name + ' has an HTTPS binding (' + $binding.bindingInformation + ') with no certificate recorded; choose its certificate in IIS Manager.'); continue }
            $store = [string]$binding.certificateStoreName
            if (-not $store) { $store = 'My' }
            if (-not (Test-Path -LiteralPath ('Cert:\LocalMachine\' + $store + '\' + $hash))) { Write-Log 'WARN' ('The certificate ' + $hash + ' for site ' + $site.name + ' (' + $binding.bindingInformation + ') is not installed here. Restore it under Certificates, then choose it in IIS Manager.'); continue }
            if (-not $saved) {
                Invoke-Native -FilePath $netsh -Arguments @('http', 'show', 'sslcert') | Out-Null
                Write-Utf8File -Path (Join-Path $Rollback 'Current-HTTPS-Bindings.txt') -Text $script:LastNativeOutput
                $saved = $true
            }
            if (($flags -band 1) -and $hostName) { $key = 'hostnameport=' + $hostName + ':' + $port }
            else {
                if (-not $address -or $address -eq '*') { $address = '0.0.0.0' }
                if ($address -match ':') { $address = '[' + $address + ']' }
                $key = 'ipport=' + $address + ':' + $port
            }
            Invoke-Native -FilePath $netsh -Arguments @('http', 'delete', 'sslcert', $key) | Out-Null
            $arguments = @('http', 'add', 'sslcert', $key, ('certhash=' + $hash), ('appid=' + $appId), ('certstorename=' + $store))
            Assert-Native -FilePath $netsh -Arguments $arguments -What ('Binding the certificate for ' + $site.name + ' (' + $key + ')')
            Add-Content -LiteralPath (Join-Path $Rollback 'HTTPS-Bindings-Changed.txt') -Value ($key + ' ' + $hash) -Encoding UTF8
        }
    }
}

function Restore-Feature {
    param([hashtable]$Data, [string]$Rollback)
    # The first feature item installs every selected feature in one
    # Install-WindowsFeature call (much faster); later items read its result.
    if ($null -eq $script:FeatureResults) {
        $script:FeatureResults = @{}
        $names = @($script:SelectedFeatures)
        Write-Log 'INFO' ('Installing {0} role(s) and feature(s): {1}' -f $names.Count, ($names -join ', '))
        $result = $null
        try { $result = Install-WindowsFeature -Name $names -ErrorAction Stop } catch { Write-Log 'WARN' ('Installing them together failed: ' + $_.Exception.Message + ' Trying one at a time.') }
        if ($result -and $result.Success) {
            foreach ($name in $names) { $script:FeatureResults[$name] = '' }
            if ([string]$result.RestartNeeded -eq 'Yes') { $script:RestartNeeded = $true }
        }
        else {
            foreach ($name in $names) {
                try {
                    $one = Install-WindowsFeature -Name $name -ErrorAction Stop
                    if ($one.Success) { $script:FeatureResults[$name] = ''; if ([string]$one.RestartNeeded -eq 'Yes') { $script:RestartNeeded = $true } }
                    else { $script:FeatureResults[$name] = 'Windows did not install it (exit code ' + [string]$one.ExitCode + ').' }
                }
                catch { $script:FeatureResults[$name] = $_.Exception.Message }
            }
        }
        foreach ($name in $names) { if ($script:FeatureResults[$name] -eq '') { Add-Content -LiteralPath (Join-Path $Rollback 'Features-Installed.txt') -Value $name -Encoding UTF8 } }
        $script:FeatureState = $null
        if ($script:RestartNeeded) { Write-Log 'WARN' 'Windows needs a restart to finish installing roles and features. Restart this server when the restore ends.' }
    }
    $message = [string]$script:FeatureResults[$Data.name]
    if ($message) { throw ('{0} was not installed: {1} If it needs installation files (such as .NET Framework 3.5), install it with -Source, then run the restore again.' -f $Data.name, $message) }
}

function New-StrongPassword {
    $alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!#%+=?@'
    $bytes = New-Object byte[] 28
    $random = New-Object System.Security.Cryptography.RNGCryptoServiceProvider
    $random.GetBytes($bytes); $random.Dispose()
    $chars = for ($i = 0; $i -lt $bytes.Length; $i++) { $alphabet[$bytes[$i] % $alphabet.Length] }
    # One of each class so any complexity policy accepts it.
    return ('Aa7!' + (-join $chars))
}

function Restore-Account {
    param([hashtable]$Data, [string]$Rollback)
    $computer = [ADSI]('WinNT://' + $script:ComputerName + ',computer')
    if ($Data.kind -eq 'user') {
        $user = @(Get-BundleList $script:Bundle.localAccounts.users | Where-Object { [string]$_.name -eq $Data.name })[0]
        $secret = Get-AccountSecretKey -Account ($script:ComputerName + '\' + $Data.name)
        $password = [string]$script:Passwords[$secret]
        $disable = [bool]$user.disabled
        if (-not $password) { $password = New-StrongPassword; $disable = $true }
        $entry = $computer.Create('User', $Data.name)
        $entry.SetPassword($password)
        $entry.SetInfo()
        $password = $null
        Add-Content -LiteralPath (Join-Path $Rollback 'Accounts-Created.txt') -Value ('user ' + $Data.name) -Encoding UTF8
        if ($user.fullName) { $entry.Put('FullName', [string]$user.fullName) }
        if ($user.description) { $entry.Put('Description', [string]$user.description) }
        $flags = [int]$entry.UserFlags.Value
        if ($disable) { $flags = $flags -bor 0x2 } else { $flags = $flags -band (-bnot 0x2) }
        if ($user.passwordNeverExpires) { $flags = $flags -bor 0x10000 }
        if ($user.cannotChangePassword) { $flags = $flags -bor 0x40 }
        $entry.Put('UserFlags', $flags)
        $entry.SetInfo()
        if ($disable -and -not $user.disabled) { Write-Log 'WARN' ('Created ' + $Data.name + ' disabled with a random password because no password was typed. Set its password and enable it in Computer Management before anything uses it.') }
        else { Write-Log 'INFO' ('Created local user ' + $Data.name + '.') }
        return
    }
    $group = @(Get-BundleList $script:Bundle.localAccounts.groups | Where-Object { [string]$_.name -eq $Data.name })[0]
    $target = $null
    try { $target = $computer.Children.Find($Data.name, 'Group') } catch { }
    if (-not $target) {
        $target = $computer.Create('Group', $Data.name)
        $target.SetInfo()
        if ($group.description) { $target.Put('Description', [string]$group.description); $target.SetInfo() }
        Add-Content -LiteralPath (Join-Path $Rollback 'Accounts-Created.txt') -Value ('group ' + $Data.name) -Encoding UTF8
        Write-Log 'INFO' ('Created local group ' + $Data.name + '.')
    }
    $source = Get-SourceComputer
    $machine = [string]$script:Bundle.localAccounts.machineSid
    $skipped = @()
    foreach ($member in (Get-BundleList $group.members)) {
        $name = [string]$member.name
        $sid = [string]$member.sid
        $local = ($machine -and $sid.StartsWith($machine + '-')) -or ($source -and ($name.StartsWith($source + '\', [System.StringComparison]::OrdinalIgnoreCase) -or $name.IndexOf('\' + $source + '\', [System.StringComparison]::OrdinalIgnoreCase) -ge 0))
        if ($local) { $path = 'WinNT://' + $script:ComputerName + '/' + $name.Substring($name.LastIndexOf('\') + 1) }
        elseif ($sid) { $path = 'WinNT://' + $sid }
        else { $path = 'WinNT://' + $name.Replace('\', '/') }
        try { $target.Add($path); Add-Content -LiteralPath (Join-Path $Rollback 'Members-Added.txt') -Value ($Data.name + ': ' + $name) -Encoding UTF8 }
        catch {
            $text = $_.Exception.Message
            if ($_.Exception.InnerException) { $text = $_.Exception.InnerException.Message }
            # 0x80070562: already a member.
            if ($text -match '0x80070562|already a member') { continue }
            $skipped += ($name + ' (' + $text.Trim() + ')')
        }
    }
    if ($skipped.Count) { Write-Log 'WARN' ('Not added to ' + $Data.name + ' because they could not be found from this server: ' + ($skipped -join '; ')) }
}

function Restore-Share {
    param([hashtable]$Data, [string]$Rollback)
    $share = @(Get-BundleList $script:Bundle.shares | Where-Object { [string]$_.name -eq $Data.name })[0]
    $path = Get-MappedPath -Path ([string]$share.path)
    if (Get-SmbShare -Name $Data.name -ErrorAction SilentlyContinue) { throw ('A share named {0} exists now; it was not replaced.' -f $Data.name) }
    $created = $false
    if (-not (Test-Path -LiteralPath $path -PathType Container)) { New-Item -Path $path -ItemType Directory -Force | Out-Null; $created = $true }
    $arguments = @{ Name = $Data.name; Path = $path; ErrorAction = 'Stop' }
    if ($share.description) { $arguments.Description = [string]$share.description }
    if ($share.folderEnumerationMode) { $arguments.FolderEnumerationMode = [string]$share.folderEnumerationMode }
    if ($share.cachingMode) { $arguments.CachingMode = [string]$share.cachingMode }
    if ([int]$share.concurrentUserLimit -gt 0) { $arguments.ConcurrentUserLimit = [int]$share.concurrentUserLimit }
    if ($share.encryptData) { $arguments.EncryptData = $true }
    # Created with no one allowed, then given exactly the old permissions.
    $arguments.NoAccess = @('Everyone')
    New-SmbShare @arguments | Out-Null
    $note = ''
    if ($created) { $note = ' (folder created empty)' }
    Add-Content -LiteralPath (Join-Path $Rollback 'Shares-Created.txt') -Value ($Data.name + ' ' + $path + $note) -Encoding UTF8
    $granted = 0; $skipped = @()
    foreach ($permission in (Get-BundleList $share.permissions)) {
        $account = Convert-AccountName -Name ([string]$permission.account)
        try {
            if ([string]$permission.type -eq 'Deny') { Block-SmbShareAccess -Name $Data.name -AccountName $account -Force -ErrorAction Stop | Out-Null }
            else { Grant-SmbShareAccess -Name $Data.name -AccountName $account -AccessRight ([string]$permission.right) -Force -ErrorAction Stop | Out-Null }
            $granted++
        }
        catch { $skipped += ($account + ' (' + $_.Exception.Message.Trim() + ')') }
    }
    # Remove the deny used while creating the share, unless the old share
    # denied Everyone too.
    try { Unblock-SmbShareAccess -Name $Data.name -AccountName 'Everyone' -Force -ErrorAction Stop | Out-Null } catch { }
    foreach ($permission in (Get-BundleList $share.permissions)) { if ([string]$permission.type -eq 'Deny' -and [string]$permission.account -match '(^|\\)Everyone$') { Block-SmbShareAccess -Name $Data.name -AccountName 'Everyone' -Force -ErrorAction Stop | Out-Null } }
    if ($skipped.Count) { Write-Log 'WARN' ('Share ' + $Data.name + ': these permissions could not be given because the accounts were not found from this server: ' + ($skipped -join '; ')) }
    if ($granted -eq 0) { throw ('No permission of share ' + $Data.name + ' could be given, so nobody can open it. Check the accounts, then set its permissions.') }
    Write-Log 'INFO' ('Created share ' + $Data.name + ' on ' + $path + $note + '.')
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
    if (-not $script:SameComputer) {
        # The task runs as the account of the same name here.
        [xml]$document = $xml
        $principal = $document.Task.Principals.Principal
        if ($principal -and $principal.UserId) {
            $user = Convert-AccountName -Name ([string]$principal.UserId)
            $user = Convert-SidText -Text $user -Map (Get-SidMap)
            if ($user -ne [string]$principal.UserId) { $principal.UserId = $user; $xml = $document.OuterXml; Write-Log 'INFO' ('The task runs as ' + $user + ' on this server.') }
        }
    }
    if ($Data.secret) {
        $password = [string]$script:Passwords[$Data.secret]
        if (-not $password) { throw ('Type the password for ' + $Data.user + ' under Changes for this server.') }
        Register-ScheduledTask -Xml $xml -TaskName $taskName -TaskPath $taskPath -User $Data.user -Password $password -Force | Out-Null
        return
    }
    Register-ScheduledTask -Xml $xml -TaskName $taskName -TaskPath $taskPath -Force | Out-Null
}

function Restore-Certificate {
    param([hashtable]$Data, [string]$Rollback)
    Import-Module PKI -ErrorAction SilentlyContinue
    if ($Data.kind -eq 'pfx') { Import-BundleCertificate -Data $Data -Rollback $Rollback; return }
    Import-Certificate -FilePath $Data.file -CertStoreLocation ('Cert:\' + $Data.store) | Out-Null
    Add-Content -LiteralPath (Join-Path $Rollback 'Certificates-Added.txt') -Value ('Cert:\' + $Data.store + '\' + $Data.thumbprint) -Encoding UTF8
}

function Get-PrivateFolder {
    # A folder only SYSTEM and Administrators can open, for a PFX file that
    # exists for a moment before it is imported.
    param([string]$Parent)
    $folder = Join-Path $Parent ('Private-' + [guid]::NewGuid().ToString('N'))
    New-Item -Path $folder -ItemType Directory -Force | Out-Null
    $acl = New-Object System.Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($sid in @('S-1-5-18', 'S-1-5-32-544')) { $acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule((New-Object System.Security.Principal.SecurityIdentifier($sid)), 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))) }
    Set-Acl -LiteralPath $folder -AclObject $acl
    return $folder
}

function Import-BundleCertificate {
    param([hashtable]$Data, [string]$Rollback)
    if ($script:PassphraseState -ne 'ok') { throw (Get-SecretsBlockedReason) }
    $entry = $script:Secrets.pfx.($Data.thumbprint)
    if (-not $entry) { throw 'The certificate is not in the migration bundle''s secrets.' }
    $bytes = [Convert]::FromBase64String([string]$entry.pfx)
    $folder = Get-PrivateFolder -Parent $env:TEMP
    $file = Join-Path $folder 'certificate.pfx'
    try {
        [System.IO.File]::WriteAllBytes($file, $bytes)
        $secure = ConvertTo-SecureString -String ([string]$entry.password) -AsPlainText -Force
        if (Get-Command Import-PfxCertificate -ErrorAction SilentlyContinue) {
            Import-PfxCertificate -FilePath $file -CertStoreLocation ('Cert:\' + $Data.store) -Password $secure -Exportable | Out-Null
        }
        else {
            $flags = [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]'MachineKeySet, PersistKeySet, Exportable'
            $certificate = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($file, [string]$entry.password, $flags)
            $store = New-Object System.Security.Cryptography.X509Certificates.X509Store(($Data.store -split '\\')[1], 'LocalMachine')
            $store.Open('ReadWrite'); try { $store.Add($certificate) } finally { $store.Close() }
        }
    }
    finally {
        if (Test-Path -LiteralPath $folder) { Remove-Item -LiteralPath $folder -Recurse -Force -ErrorAction SilentlyContinue }
        [Array]::Clear($bytes, 0, $bytes.Length)
    }
    $here = Get-Item -LiteralPath ('Cert:\' + $Data.store + '\' + $Data.thumbprint) -ErrorAction SilentlyContinue
    if (-not $here -or -not $here.HasPrivateKey) { throw 'The certificate was imported but Windows does not show its private key.' }
    Add-Content -LiteralPath (Join-Path $Rollback 'Certificates-Added.txt') -Value ('Cert:\' + $Data.store + '\' + $Data.thumbprint) -Encoding UTF8
    Write-Log 'INFO' ('Imported ' + $here.Subject + ' with its private key into ' + $Data.store + '.')
}

function Restore-Acl {
    param([hashtable]$Data, [string]$Rollback)
    Restore-AclFile -File $Data.file -Target (Get-MappedPath -Path $Data.source) -Rollback $Rollback
}

function Restore-AclFile {
    # Restores an ICACLS /save file onto Target (the same folder name, maybe on
    # another drive). The old server's local account SIDs are changed to the
    # SIDs of the accounts of the same name here first.
    param([string]$File, [string]$Target, [string]$Rollback)
    $icacls = Join-Path $env:SystemRoot 'System32\icacls.exe'
    $target = $Target.TrimEnd('\')
    if (-not (Test-Path -LiteralPath $target -PathType Container)) { throw ('The folder {0} does not exist on this server.' -f $target) }
    $parent = Split-Path -Path $target -Parent
    Assert-Native -FilePath $icacls -Arguments @($target, '/save', (Join-Path $Rollback ((Get-SafeFileName -Name $target) + '.current.acl')), '/t', '/c', '/q') -What 'Saving the current permissions'
    $file = $File
    $map = Get-SidMap
    if ($map.Count) {
        $text = [System.IO.File]::ReadAllText($File, [System.Text.Encoding]::Unicode)
        $converted = Convert-SidText -Text $text -Map $map
        if ($converted -ne $text) {
            $file = Join-Path $Rollback ((Get-SafeFileName -Name $target) + '.this-server.acl')
            [System.IO.File]::WriteAllText($file, $converted, [System.Text.Encoding]::Unicode)
            Write-Log 'INFO' ('Gave the old server''s local account permissions to the accounts of the same name here ({0} account(s)).' -f $map.Count)
        }
    }
    # ICACLS saves names relative to the parent of the saved folder, so the
    # restore runs against that parent.
    Assert-Native -FilePath $icacls -Arguments @($parent, '/restore', $file, '/c', '/q') -What 'Permission restore'
}

function Restore-DnsZone {
    param([hashtable]$Data, [string]$Rollback)
    Import-Module DnsServer -ErrorAction Stop
    if ($Data.kind -eq 'forwarders') {
        $current = Get-DnsServerForwarder
        Write-Utf8File -Path (Join-Path $Rollback 'Current-DNS-Forwarders.txt') -Text (('Forwarders: ' + (@($current.IPAddress | ForEach-Object { [string]$_ }) -join ', ')) + "`r`nUseRootHint: " + [string]$current.UseRootHint + "`r`n")
        Set-DnsServerForwarder -IPAddress @($Data.addresses) -UseRootHint ([bool]$Data.useRootHint)
        Write-Log 'INFO' ('Set DNS forwarders to ' + (@($Data.addresses) -join ', ') + '.')
        return
    }
    if ($Data.kind -eq 'other') {
        $masters = @($Data.masters)
        $useDirectory = $Data.dsIntegrated -and $script:IsDomainController
        $scope = $Data.scope
        if ($scope -notin @('Forest', 'Domain', 'Legacy')) { $scope = 'Domain' }
        switch ($Data.type) {
            'Forwarder' {
                if ($useDirectory) { Add-DnsServerConditionalForwarderZone -Name $Data.zone -MasterServers $masters -ReplicationScope $scope }
                else { Add-DnsServerConditionalForwarderZone -Name $Data.zone -MasterServers $masters }
            }
            'Stub' {
                if ($useDirectory) { Add-DnsServerStubZone -Name $Data.zone -MasterServers $masters -ReplicationScope $scope }
                else { Add-DnsServerStubZone -Name $Data.zone -MasterServers $masters -ZoneFile ((Get-SafeFileName -Name $Data.zone) + '.dns') }
            }
            'Secondary' { Add-DnsServerSecondaryZone -Name $Data.zone -MasterServers $masters -ZoneFile ((Get-SafeFileName -Name $Data.zone) + '.dns') }
            default { throw ('Unknown zone type ' + $Data.type + '.') }
        }
        Add-Content -LiteralPath (Join-Path $Rollback 'DNS-Zones-Added.txt') -Value $Data.zone -Encoding UTF8
        if ($Data.dsIntegrated -and -not $script:IsDomainController) { Write-Log 'WARN' ('The zone ' + $Data.zone + ' was stored in Active Directory; this server is not a domain controller, so it was created as a file-backed zone.') }
        return
    }
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
        Backup-GPO -Guid $guid -Path $Rollback -Comment 'Before Server Inventory and Migration Manager restore' | Out-Null
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
    $policy = $Data.file
    $map = Get-SidMap
    if ($map.Count) {
        # secedit exports are UTF-16; rights given to the old server's local
        # accounts go to the accounts of the same name here.
        $text = [System.IO.File]::ReadAllText($Data.file, [System.Text.Encoding]::Unicode)
        $converted = Convert-SidText -Text $text -Map $map
        if ($converted -ne $text) {
            $policy = Join-Path $Rollback 'Local-Security-Policy.this-server.inf'
            [System.IO.File]::WriteAllText($policy, $converted, [System.Text.Encoding]::Unicode)
            Write-Log 'INFO' 'User rights given to the old server''s local accounts go to the accounts of the same name here.'
        }
    }
    $code = Invoke-Native -FilePath $secedit -Arguments @('/configure', '/db', (Join-Path $Rollback 'restore.sdb'), '/cfg', $policy, '/areas', 'SECURITYPOLICY', 'USER_RIGHTS', '/log', (Join-Path $Rollback 'secedit.log'), '/quiet')
    # Exit code 3 means the policy was applied but some accounts named in user
    # rights could not be resolved on this server; secedit.log lists them.
    if ($code -eq 3) { Write-Log 'WARN' 'Security policy applied with warnings: some accounts in user rights assignments do not exist on this server. See secedit.log in the rollback folder.' }
    elseif ($code -ne 0) { throw ('Security policy restore failed with exit code {0}. See secedit.log in the rollback folder.' -f $code) }
}

$script:Restorers = @{
    dhcp = 'Restore-Dhcp'; nps = 'Restore-Nps'; printers = 'Restore-Printers'; iis = 'Restore-Iis'; firewall = 'Restore-Firewall'
    registry = 'Restore-Registry'; tasks = 'Restore-Task'; certificates = 'Restore-Certificate'; acls = 'Restore-Acl'
    dns = 'Restore-DnsZone'; gpo = 'Restore-Gpo'; secpol = 'Restore-Secpol'
    features = 'Restore-Feature'; accounts = 'Restore-Account'; shares = 'Restore-Share'
}

$script:UndoGuidance = @{
    dhcp = 'Import Current-DHCP-Configuration-and-Leases.xml with Import-DhcpServer -ScopeOverwrite, or restore DHCP-Database-Backup with Restore-DhcpServer. If DHCP-Authorized.txt exists, remove that authorization with Remove-DhcpServerInDC.'
    nps = 'Import Current-NPS-CONFIG-SENSITIVE.xml with Import-NpsConfiguration.'
    printers = 'Restore Current-Printers.printerExport with PrintBrm.exe -R -O FORCE.'
    iis = 'Copy Current-IIS-Configuration into %windir%\System32\inetsrv\backup and run appcmd restore backup. Website folders that held files are copied in Website-Files-Before. HTTPS bindings changed are listed in HTTPS-Bindings-Changed.txt with the previous ones in Current-HTTPS-Bindings.txt.'
    features = 'Remove the roles and features listed in Features-Installed.txt with Uninstall-WindowsFeature.'
    accounts = 'Delete the users and groups listed in Accounts-Created.txt, and remove the members listed in Members-Added.txt.'
    shares = 'Remove the shares listed in Shares-Created.txt with Remove-SmbShare; folders listed there as created were empty when created.'
    firewall = 'Import Current-Windows-Firewall-Policy.wfw with netsh advfirewall import.'
    registry = 'Import the .current.reg files with reg import; delete keys listed in .did-not-exist.txt files.'
    tasks = 'Register the .current.xml files with Register-ScheduledTask; delete tasks listed in .did-not-exist.txt files.'
    certificates = 'Remove the certificates listed in Certificates-Added.txt (with their private keys).'
    acls = 'Run icacls <parent folder> /restore <name>.current.acl.'
    dns = 'Remove the zones listed in DNS-Zones-Added.txt. Current-DNS-Forwarders.txt holds the forwarders before the restore.'
    gpo = 'Restore the GPO backups in this folder with Restore-GPO; delete GPOs listed in GPOs-Created.txt.'
    secpol = 'Apply Current-Local-Security-Policy.inf with secedit /configure.'
}

function Invoke-RestorePlan {
    if (-not (Test-Administrator)) { throw 'Restoring needs administrator rights. Run the application as administrator.' }
    if (-not $PlanPath -or -not (Test-Path -LiteralPath $PlanPath)) { throw 'The restore plan was not provided.' }
    if (-not $RollbackPath) { throw 'The rollback folder was not provided.' }
    $plan = Get-Content -LiteralPath $PlanPath -Raw | ConvertFrom-Json
    Set-PlanMappings -Plan $plan
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
    $script:SelectedFeatures = @($queue | Where-Object { $_.component -eq 'features' -and $_.available } | ForEach-Object { [string]$_.data.name })
    $script:FeatureResults = $null
    $script:RestartNeeded = $false
    New-Item -Path $RollbackPath -ItemType Directory -Force | Out-Null
    $readme = "Server Inventory and Migration Manager restore`r`nPackage: $($script:Package)`r`nServer: $($script:ComputerName)`r`nStarted: $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')`r`n`r`nEach folder holds the configuration saved just before that component was restored.`r`n"
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
    if ($script:RestartNeeded) { Write-RestoreEvent @{ event = 'restart' } }
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
    Read-SecretInput
    Initialize-Bundle
    Open-BundleSecrets
    if ($Mode -eq 'Scan') {
        if (-not $ResultPath) { throw 'ResultPath is required for a scan.' }
        # A scan can carry the operator's Changes for this server, so paths it
        # checks are the ones the restore will use; otherwise the suggestions.
        $scanPlan = New-Object PSObject
        if ($PlanPath -and (Test-Path -LiteralPath $PlanPath)) { $scanPlan = Get-Content -LiteralPath $PlanPath -Raw | ConvertFrom-Json }
        Set-PlanMappings -Plan $scanPlan
        # Not $mappings: PowerShell names ignore case, so at script level it
        # would replace $script:Mappings that the components read.
        $scanMappings = @(Get-Mappings | ForEach-Object { $_.suggested = $_.to; $_.to = $script:Mappings[$_.kind][$_.from]; $_ })
        $components = Get-AllComponents
        $result = @{
            packagePath = $script:Package; packageName = $identity.packageName
            sourceComputer = $identity.sourceComputer; currentComputer = $script:ComputerName; sameComputer = $script:SameComputer
            collectedAt = $identity.collectedAt; manifestVerified = $script:ManifestPresent; manifestMessage = $script:ManifestMessage
            isAdministrator = (Test-Administrator); components = @(Remove-ItemData -Components $components)
            bundle = @{
                present = [bool]$script:Bundle -or [bool]$script:BundleMessage; usable = [bool]$script:Bundle; message = $script:BundleMessage
                passphrase = $script:PassphraseState; createdAt = $(if ($script:Bundle) { [string]$script:Bundle.createdAt } else { '' })
                warnings = @($(if ($script:Bundle) { Get-BundleList $script:Bundle.warnings } else { @() }))
            }
            mappings = $scanMappings; notMovable = @(Get-NotMovableRoles)
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
