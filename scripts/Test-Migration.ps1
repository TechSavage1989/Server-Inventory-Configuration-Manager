# Real file share migration round trip for src/app/Migrate-FileShares.ps1 on a
# disposable Windows runner. The runner is both the old and the new server: a
# synthetic share is discovered, checked, copied, changed, copied again, cut
# over to a new share name and folder, and the old share is made read-only and
# restored. It creates shares and folders, so it refuses to run outside CI
# unless -AllowSystemChanges is given.
[CmdletBinding()]
param(
    [string]$OutputDirectory = (Join-Path $PSScriptRoot '..\build\migration-validation'),
    [switch]$AllowSystemChanges
)
$ErrorActionPreference = 'Stop'
if (-not $env:GITHUB_ACTIONS -and -not $AllowSystemChanges) { throw 'Test-Migration.ps1 creates shares and folders. Run it only on a disposable machine with -AllowSystemChanges.' }
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Test-Migration.ps1 must run elevated.' }

$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
if (Test-Path -LiteralPath $OutputDirectory) { Remove-Item -LiteralPath $OutputDirectory -Recurse -Force }
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$engine = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\src\app\Migrate-FileShares.ps1'))
$powerShell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
$work = Join-Path $OutputDirectory 'work'
$checks = New-Object Collections.ArrayList
function Assert([bool]$Condition, [string]$Description) { if (-not $Condition) { throw ('FAIL ' + $Description) }; [void]$checks.Add($Description); Write-Host ('PASS ' + $Description) }
function Invoke-Engine([string]$Mode, [hashtable]$Arguments, [string]$Name, [string]$InputLine) {
    $log = Join-Path $OutputDirectory ($Name + '.log')
    $result = Join-Path $OutputDirectory ($Name + '.json')
    $list = @('-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', $engine, '-Mode', $Mode, '-ResultPath', $result, '-WorkPath', $work)
    foreach ($key in $Arguments.Keys) { $list += @(('-' + $key), $Arguments[$key]) }
    # Engine errors on stderr must be recorded, not end this script.
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        # The Connect as account reaches the engine on standard input only.
        if ($InputLine) { $InputLine | & $powerShell @($list + '-CredentialFromInput') *> $log } else { & $powerShell @list *> $log }
        $code = $LASTEXITCODE
    } finally { $ErrorActionPreference = $previous }
    Get-Content -LiteralPath $log | Where-Object { $_ -notmatch '"event":"progress"' } | ForEach-Object { Write-Host ('  ' + $_) }
    $json = $null
    if (Test-Path -LiteralPath $result) { $json = Get-Content -LiteralPath $result -Raw | ConvertFrom-Json }
    return @{ code = $code; log = (Get-Content -LiteralPath $log -Raw); result = $json }
}

$tokens = $null; $errors = $null
[Management.Automation.Language.Parser]::ParseFile($engine, [ref]$tokens, [ref]$errors) | Out-Null
Assert ($errors.Count -eq 0) 'Migration engine parses without errors'

# --- A synthetic "old" share --------------------------------------------------
$root = 'C:\SICM-Migration'
if (Test-Path -LiteralPath $root) { Remove-Item -LiteralPath $root -Recurse -Force }
$old = Join-Path $root 'Old\Finance'
New-Item -ItemType Directory -Path (Join-Path $old 'Reports\2026') -Force | Out-Null
New-Item -ItemType Directory -Path (Join-Path $old 'Empty Folder') -Force | Out-Null
for ($i = 1; $i -le 40; $i++) { Set-Content -LiteralPath (Join-Path $old ('Reports\2026\Report-' + $i + '.txt')) -Value ('synthetic report ' + $i) }
Set-Content -LiteralPath (Join-Path $old 'Budget.txt') -Value 'version 1'
Set-Content -LiteralPath (Join-Path $old 'Remove-Me.txt') -Value 'deleted on the old server later'
$hidden = Join-Path $old 'hidden.txt'; Set-Content -LiteralPath $hidden -Value 'hidden'; (Get-Item -LiteralPath $hidden).Attributes = 'Hidden'
& icacls.exe (Join-Path $old 'Reports') /grant 'Guests:(OI)(CI)R' | Out-Null
# Top-folder permissions: one set on the share folder itself (Backup
# Operators) and one inherited from its parent (Remote Desktop Users).
& icacls.exe $old /grant '*S-1-5-32-551:(OI)(CI)RX' | Out-Null
& icacls.exe (Split-Path -Path $old -Parent) /grant '*S-1-5-32-555:(OI)(CI)R' | Out-Null
$oldName = 'SICMFinance'; $newName = 'SICMFinanceNew'
foreach ($name in @($oldName, $newName)) { if (Get-SmbShare -Name $name -ErrorAction SilentlyContinue) { Remove-SmbShare -Name $name -Force } }
New-SmbShare -Name $oldName -Path $old -FullAccess 'Administrators' -ChangeAccess 'Users' -ReadAccess 'Everyone' -FolderEnumerationMode AccessBased -CachingMode None -Description 'Synthetic finance share' | Out-Null
Block-SmbShareAccess -Name $oldName -AccountName 'Guests' -Force | Out-Null
$newFolder = Join-Path $root 'New\Finance'

# --- Discover -------------------------------------------------------------------
$discover = Invoke-Engine 'Discover' @{ Source = $env:COMPUTERNAME } 'discover'
Assert ($discover.code -eq 0) 'Discover completes'
$share = @($discover.result.shares | Where-Object { $_.name -eq $oldName })[0]
Assert ([bool]$share -and $share.path -eq $old -and -not $share.system) 'Discover finds the share and its folder'
Assert (@($discover.result.shares | Where-Object { $_.name -match '\$$' -and $_.name -ne 'print$' }).Count -eq 0) 'Admin shares (C$, ADMIN$, IPC$) are not offered'
Assert ($share.folderEnumerationMode -eq 'AccessBased' -and $share.cachingMode -eq 'None' -and $share.description -eq 'Synthetic finance share') 'Discover reads the share settings'
Assert (@($share.access).Count -eq 4 -and @($share.access | Where-Object { $_.type -eq 'Deny' -and $_.account -match 'Guests' }).Count -eq 1) 'Discover reads the share permissions, including a deny'
Assert ($discover.result.sameServer -and $discover.result.isAdministrator) 'Discover reports this server and administrator rights'
# The same server through its network address takes the remote path: a
# WinRM or packet-privacy DCOM session and admin-share folder paths.
$address = @(Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -notmatch '^(127\.|169\.254\.)' } | Select-Object -First 1)[0].IPAddress
$remote = Invoke-Engine 'Discover' @{ Source = $address } 'discover-remote'
$remoteShare = @($remote.result.shares | Where-Object { $_.name -eq $oldName })[0]
Assert ($remote.code -eq 0 -and -not $remote.result.sameServer -and $remote.log -match 'Connected to .* over (WinRM|DCOM)') ('Discover through the network address uses a remote management session: ' + (($remote.log -split "`n" | Where-Object { $_ -match 'Connected to|ERROR' }) -join ' '))
Assert ([bool]$remoteShare -and $remoteShare.remotePath -eq ('\\' + $address + '\C$\SICM-Migration\Old\Finance') -and @($remoteShare.access).Count -eq 4) 'Remote discovery reads the share, its admin-share path and its permissions'
$missing = Invoke-Engine 'Discover' @{ Source = 'sicm-no-such-server.invalid' } 'discover-missing'
Assert ($missing.code -ne 0 -and $missing.log -match 'could not be found') 'An unknown server name is reported plainly'

function Write-Plan([string]$Path, [string]$Destination, [bool]$Synced, [bool]$Created, [bool]$AllowOverwrite = $false) {
    $item = @{}
    foreach ($property in $share.PSObject.Properties) { $item[$property.Name] = $property.Value }
    $item['destName'] = $newName; $item['destPath'] = $Destination; $item['previouslySynced'] = $Synced; $item['createdShare'] = $Created; $item['allowOverwrite'] = $AllowOverwrite; $item['expectedFiles'] = 43
    ConvertTo-Json -InputObject @{ source = $discover.result.source; sourceFqdn = $discover.result.sourceFqdn; shares = @($item) } -Depth 6 | Set-Content -LiteralPath $Path -Encoding UTF8
}
$plan = Join-Path $OutputDirectory 'plan.json'

# --- Negative controls: a folder with unrelated files is never mirrored over ---
$occupied = Join-Path $root 'Occupied'
New-Item -ItemType Directory -Path $occupied -Force | Out-Null
Set-Content -LiteralPath (Join-Path $occupied 'keep.txt') -Value 'unrelated'
Write-Plan $plan $occupied $false $false
$check = Invoke-Engine 'Precheck' @{ PlanPath = $plan } 'precheck-occupied'
Assert ($check.code -eq 0 -and -not $check.result.shares[0].ok -and ($check.result.shares[0].problems -join ' ') -match 'already contains files') 'Check refuses a new folder that already holds other files'
$sync = Invoke-Engine 'Sync' @{ PlanPath = $plan } 'sync-occupied'
Assert ($sync.code -ne 0 -and (Test-Path -LiteralPath (Join-Path $occupied 'keep.txt'))) 'Copy refuses a non-empty folder and deletes nothing in it'
Write-Plan $plan $occupied $false $false $true
$allowed = Invoke-Engine 'Precheck' @{ PlanPath = $plan } 'precheck-occupied-overwrite'
Assert ($allowed.code -eq 0 -and $allowed.result.shares[0].ok -and (($allowed.result.shares[0].warnings -join ' ') -match 'Overwrite selected')) 'Explicit override allows an occupied folder after a warning'
$sync = Invoke-Engine 'Sync' @{ PlanPath = $plan } 'sync-occupied-overwrite'
Assert ($sync.code -eq 0 -and $sync.result.shares[0].status -eq 'succeeded') 'Mirror copy to explicitly approved occupied destination succeeds'
Assert (-not (Test-Path -LiteralPath (Join-Path $occupied 'keep.txt')) -and (Test-Path -LiteralPath (Join-Path $occupied 'Budget.txt'))) 'Approved mirror removes destination-only files and copies source files'

# --- Check, copy, change, copy again -----------------------------------------
Write-Plan $plan $newFolder $false $false
$check = Invoke-Engine 'Precheck' @{ PlanPath = $plan } 'precheck'
Assert ($check.code -eq 0 -and $check.result.shares[0].ok) 'Check passes for an empty new folder'
Assert ([int]$check.result.shares[0].files -eq 43 -and [int64]$check.result.shares[0].bytes -gt 0) 'Check measures the share (files and size)'
Assert (@($check.result.space).Count -eq 1 -and $check.result.space[0].ok) 'Check confirms free space'

$sync = Invoke-Engine 'Sync' @{ PlanPath = $plan } 'sync-1'
Assert ($sync.code -eq 0 -and $sync.result.shares[0].status -eq 'succeeded') 'First copy succeeds'
Assert (@(Get-ChildItem -LiteralPath $newFolder -Recurse -File -Force).Count -eq 43) 'Every file is copied, including hidden ones'
Assert (Test-Path -LiteralPath (Join-Path $newFolder 'Empty Folder')) 'Empty folders are copied'
Assert (((& icacls.exe (Join-Path $newFolder 'Reports\2026\Report-1.txt')) -join ' ') -match 'Guests') 'NTFS permissions are copied'
Assert (((Get-Item -LiteralPath (Join-Path $newFolder 'hidden.txt') -Force).Attributes -band [IO.FileAttributes]::Hidden) -ne 0) 'File attributes are copied'
$rootAcl = Get-Acl -LiteralPath $newFolder
$rootSids = @($rootAcl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]) | ForEach-Object { $_.IdentityReference.Value })
$parentSids = @((Get-Acl -LiteralPath (Split-Path -Path $newFolder -Parent)).GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]) | ForEach-Object { $_.IdentityReference.Value })
Assert ($rootAcl.AreAccessRulesProtected -and $rootSids -contains 'S-1-5-32-551' -and $rootSids -contains 'S-1-5-32-555' -and $parentSids -notcontains 'S-1-5-32-555') ('The new top folder gets the old top folder''s own and inherited permissions: ' + ($rootSids -join ','))
Assert ($sync.log -match 'Gave .* the permissions of the old top folder' -and $sync.log -match 'Created the folder') 'The first copy creates the chosen folder and sets its permissions'
$subSids = @((Get-Acl -LiteralPath (Join-Path $newFolder 'Reports\2026')).GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]) | ForEach-Object { $_.IdentityReference.Value })
Assert ($subSids -contains 'S-1-5-32-551' -and $subSids -contains 'S-1-5-32-555') ('Subfolders carry the top folder''s inheritable permissions: ' + ($subSids -join ','))

Set-Content -LiteralPath (Join-Path $old 'Budget.txt') -Value 'version 2'
Set-Content -LiteralPath (Join-Path $old 'New-Plan.txt') -Value 'added after the first copy'
Remove-Item -LiteralPath (Join-Path $old 'Remove-Me.txt')
Write-Plan $plan $newFolder $true $false
$sync = Invoke-Engine 'Sync' @{ PlanPath = $plan } 'sync-2'
Assert ($sync.code -eq 0) 'Second copy succeeds'
Assert ((Get-Content -LiteralPath (Join-Path $newFolder 'Budget.txt') -Raw) -match 'version 2' -and (Test-Path -LiteralPath (Join-Path $newFolder 'New-Plan.txt'))) 'Second copy brings over changed and new files'
Assert (-not (Test-Path -LiteralPath (Join-Path $newFolder 'Remove-Me.txt'))) 'Second copy removes files deleted on the old server'
Assert ([int]$sync.result.shares[0].files[1] -eq 2) 'Second copy copies only what changed'
$rootAfter = Get-Acl -LiteralPath $newFolder
$rootAfterSids = @($rootAfter.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]) | ForEach-Object { $_.IdentityReference.Value })
Assert ($rootAfter.AreAccessRulesProtected -and $rootAfterSids -contains 'S-1-5-32-555') 'After a later copy the top folder still has its own protected permissions'

$cancel = Join-Path $OutputDirectory 'stop'
Set-Content -LiteralPath $cancel -Value 'stop'
$sync = Invoke-Engine 'Sync' @{ PlanPath = $plan; CancelPath = $cancel } 'sync-canceled'
Assert ($sync.code -eq 2 -and $sync.log -match '"status":"canceled"') 'A stop request is honoured'
Remove-Item -LiteralPath $cancel

# --- Cutover -------------------------------------------------------------------
$cut = Invoke-Engine 'Cutover' @{ PlanPath = $plan } 'cutover'
Assert ($cut.code -eq 0 -and -not $cut.result.failed) 'Cutover completes'
$created = Get-SmbShare -Name $newName -ErrorAction SilentlyContinue
Assert ([bool]$created -and $created.Path -eq $newFolder) 'Cutover creates the new share for the new folder'
Assert ($created.FolderEnumerationMode -eq 'AccessBased' -and $created.CachingMode -eq 'None' -and $created.Description -eq 'Synthetic finance share') 'The new share keeps the old share settings'
$access = @(Get-SmbShareAccess -Name $newName | ForEach-Object { ([string]$_.AccountName).Split('\')[-1] + '|' + $_.AccessControlType + '|' + $_.AccessRight } | Sort-Object)
$expected = @(Get-SmbShareAccess -Name $oldName | ForEach-Object { ([string]$_.AccountName).Split('\')[-1] + '|' + $_.AccessControlType + '|' + $_.AccessRight } | Sort-Object)
Assert ((($access -join ';') -eq ($expected -join ';'))) ('The new share has the old share permissions: ' + ($access -join ';'))
Assert ($cut.result.verification[0].verified -and [int]$cut.result.verification[0].files -eq 43) 'Cutover verifies that old and new folders match'
Assert ($cut.result.verification[0].reachable -and $cut.result.verification[0].sharePath -eq ('\\' + $env:COMPUTERNAME + '\' + $newName) -and (Test-Path -LiteralPath ('\\' + $env:COMPUTERNAME + '\' + $newName + '\Budget.txt'))) 'Cutover checks that the new share opens through its network path'

# --- Existing share name: default refusal and intentional replacement ----------
$replacementFolder = Join-Path $root 'New\Replacement'
Write-Plan $plan $replacementFolder $false $false
$blocked = Invoke-Engine 'Precheck' @{ PlanPath = $plan } 'precheck-existing-name'
Assert ($blocked.code -eq 0 -and -not $blocked.result.shares[0].ok -and (($blocked.result.shares[0].problems -join ' ') -match 'already exists')) 'Existing share name is blocked by default'
$blockedSync = Invoke-Engine 'Sync' @{ PlanPath = $plan } 'sync-existing-name'
Assert ($blockedSync.code -ne 0 -and (Get-SmbShare -Name $newName).Path -eq $newFolder) 'Direct copy without override cannot silently repoint an existing share'
Write-Plan $plan $replacementFolder $false $false $true
$approved = Invoke-Engine 'Precheck' @{ PlanPath = $plan } 'precheck-existing-name-approved'
Assert ($approved.code -eq 0 -and $approved.result.shares[0].ok -and (($approved.result.shares[0].warnings -join ' ') -match 'disconnect users')) 'Explicit override allows an existing share name with a replacement warning'
$copyReplacement = Invoke-Engine 'Sync' @{ PlanPath = $plan } 'sync-existing-name-approved'
Assert ($copyReplacement.code -eq 0 -and $copyReplacement.result.shares[0].status -eq 'succeeded') 'Approved new destination folder copies successfully'
Write-Plan $plan $replacementFolder $true $false $true
$replace = Invoke-Engine 'Cutover' @{ PlanPath = $plan } 'cutover-replace-existing-name'
Assert ($replace.code -eq 0 -and (Get-SmbShare -Name $newName).Path -eq $replacementFolder) 'Approved cutover replaces the old share mapping with the chosen folder'
$backupFile = @(Get-ChildItem -LiteralPath $work -Filter 'ExistingShare-SICMFinanceNew-*.json' | Sort-Object LastWriteTime -Descending | Select-Object -First 1)
Assert ($backupFile.Count -eq 1) 'Existing share settings were backed up before replacement'
$backup = Get-Content -LiteralPath $backupFile[0].FullName -Raw | ConvertFrom-Json
Assert ($backup.destPath -eq $newFolder -and $backup.destName -eq $newName -and @($backup.access).Count -gt 0 -and (Test-Path -LiteralPath $newFolder)) 'Existing share backup contains its original path and access permissions; old files are retained'

# --- Old server: read-only and undo ----------------------------------------------
$readOnly = Invoke-Engine 'SourceReadOnly' @{ PlanPath = $plan } 'source-readonly'
Assert ($readOnly.code -eq 0) 'Old shares can be made read-only'
$rights = @(Get-SmbShareAccess -Name $oldName | Where-Object { $_.AccessControlType -eq 'Allow' } | ForEach-Object { [string]$_.AccessRight } | Sort-Object -Unique)
Assert (($rights -join ',') -eq 'Read') ('Every allowed account on the old share now has read access only: ' + ($rights -join ','))
$again = Invoke-Engine 'SourceReadOnly' @{ PlanPath = $plan } 'source-readonly-again'
Assert ($again.code -ne 0) 'Making the old shares read-only twice is refused (the saved permissions are kept)'
$restore = Invoke-Engine 'SourceRestore' @{} 'source-restore'
Assert ($restore.code -eq 0) 'Old share permissions can be put back'
$back = @(Get-SmbShareAccess -Name $oldName | ForEach-Object { ([string]$_.AccountName).Split('\')[-1] + '|' + $_.AccessControlType + '|' + $_.AccessRight } | Sort-Object)
Assert ((($back -join ';') -eq ($expected -join ';'))) 'The old share has its original permissions again'

# --- DFS and alias checks -------------------------------------------------------------
$dfs = Invoke-Engine 'DfsReport' @{ PlanPath = $plan } 'dfs'
Assert ($dfs.code -eq 0 -and -not $dfs.result.available -and $dfs.result.message -match 'not in a domain') 'DFS check explains that a workgroup server has no domain namespaces'
$alias = Invoke-Engine 'AliasCheck' @{ PlanPath = $plan } 'alias-check'
Assert ($alias.code -eq 0 -and -not $alias.result.possible -and @($alias.result.reasons).Count -ge 1) 'Taking over the old name is refused while it is unsafe'
$aliasRun = Invoke-Engine 'Alias' @{ PlanPath = $plan } 'alias'
Assert ($aliasRun.code -ne 0) 'The alias step itself also refuses when the check fails'

# --- Connect as another account ----------------------------------------------------------
# A synthetic local administrator stands in for a domain administrator of the
# old server. Remote UAC would strip a local account's administrator rights over
# the network, so this disposable runner allows them; WinRM with an explicit
# account to an IP address needs the address in TrustedHosts.
$accountName = 'sicm-migrate'
$account = $env:COMPUTERNAME + '\' + $accountName
$secret = 'Sicm-' + [guid]::NewGuid().ToString('N').Substring(0, 20) + '-a1!'
$wrongSecret = 'Wrong-' + [guid]::NewGuid().ToString('N').Substring(0, 12)
if (Get-LocalUser -Name $accountName -ErrorAction SilentlyContinue) { Remove-LocalUser -Name $accountName }
New-LocalUser -Name $accountName -Password (ConvertTo-SecureString -String $secret -AsPlainText -Force) -PasswordNeverExpires -AccountNeverExpires | Out-Null
Add-LocalGroupMember -Group 'Administrators' -Member $accountName
$policy = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System'
Set-ItemProperty -LiteralPath $policy -Name 'LocalAccountTokenFilterPolicy' -Value 1 -Type DWord
Enable-PSRemoting -Force -SkipNetworkProfileCheck | Out-Null
Set-Item -Path 'WSMan:\localhost\Client\TrustedHosts' -Value $address -Force
$credentialLine = ConvertTo-Json -InputObject @{ user = $account; password = $secret } -Compress
$wrongLine = ConvertTo-Json -InputObject @{ user = $account; password = $wrongSecret } -Compress
$asAccount = Invoke-Engine 'Discover' @{ Source = $address } 'discover-account' $credentialLine
$accountShare = @($asAccount.result.shares | Where-Object { $_.name -eq $oldName })[0]
Assert ($asAccount.code -eq 0 -and $asAccount.log -match ('Connected to ' + [regex]::Escape($address) + ' over (WinRM|DCOM) as ' + [regex]::Escape($account))) ('Discover connects with the Connect as account: ' + (($asAccount.log -split "`n" | Where-Object { $_ -match 'Connected to|ERROR' }) -join ' '))
Assert ([bool]$accountShare -and @($accountShare.access).Count -eq 4) 'The Connect as account reads the share and its permissions'
$accountPlan = Join-Path $OutputDirectory 'plan-account.json'
$accountFolder = Join-Path $root 'New\Account'
$item = @{}
foreach ($property in $accountShare.PSObject.Properties) { $item[$property.Name] = $property.Value }
$item['destName'] = 'SICMAccount'; $item['destPath'] = $accountFolder; $item['previouslySynced'] = $false; $item['createdShare'] = $false; $item['expectedFiles'] = 0
ConvertTo-Json -InputObject @{ source = $address; sourceFqdn = $address; shares = @($item) } -Depth 6 | Set-Content -LiteralPath $accountPlan -Encoding UTF8
$accountCheck = Invoke-Engine 'Precheck' @{ PlanPath = $accountPlan } 'precheck-account' $credentialLine
Assert ($accountCheck.code -eq 0 -and $accountCheck.log -match ('Opened a file connection to ' + [regex]::Escape($address) + ' as ' + [regex]::Escape($account))) ('Check opens a file connection with the Connect as account: ' + (($accountCheck.log -split "`n" | Where-Object { $_ -match 'file connection|ERROR|WARN' }) -join ' '))
Assert ($accountCheck.result.shares[0].ok -and [int]$accountCheck.result.shares[0].files -eq 43) 'Robocopy measures the old share through the admin share with that account'
Assert (-not (((& net.exe use) -join ' ') -match [regex]::Escape('\\' + $address))) 'The file connection is removed when the step ends'
$denied = Invoke-Engine 'Discover' @{ Source = $address } 'discover-wrong-password' $wrongLine
Assert ($denied.code -ne 0 -and $denied.log -match ('refused access on ' + [regex]::Escape($address) + ' for ' + [regex]::Escape($account)) -and $denied.log -match '"event":"accessDenied"') ('A wrong password is reported as refused access and asks for the account: ' + (($denied.log -split "`n" | Where-Object { $_ -match 'ERROR' }) -join ' '))
$deniedFiles = Invoke-Engine 'Precheck' @{ PlanPath = $accountPlan } 'precheck-wrong-password' $wrongLine
Assert ($deniedFiles.code -ne 0 -and $deniedFiles.log -match ('did not accept ' + [regex]::Escape($account) + ' for file access') -and $deniedFiles.log -match '"event":"accessDenied"') ('A wrong password for file access is reported plainly: ' + (($deniedFiles.log -split "`n" | Where-Object { $_ -match 'ERROR' }) -join ' '))

# --- From an admin computer: the new server pulls from the old one -------------
# Invoke-ShareMigrationRemote.ps1 runs the engine inside the "new server"
# through PowerShell remoting (here localhost), and the new server reaches the
# "old server" (this runner's network address) with the old server's account.
$remoteRunner = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\src\app\Invoke-ShareMigrationRemote.ps1'))
$tokens = $null; $errors = $null
[Management.Automation.Language.Parser]::ParseFile($remoteRunner, [ref]$tokens, [ref]$errors) | Out-Null
Assert ($errors.Count -eq 0) 'The remote share migration runner parses without errors'
$remoteWork = 'SICM-REMOTE-TEST'
function Invoke-Remote([string]$Mode, [hashtable]$Arguments, [string]$Name, [string]$InputLine) {
    $log = Join-Path $OutputDirectory ($Name + '.log')
    $result = Join-Path $OutputDirectory ($Name + '.json')
    if (Test-Path -LiteralPath $result) { Remove-Item -LiteralPath $result -Force }
    $list = @('-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'RemoteSigned', '-File', $remoteRunner, '-Target', 'localhost', '-Mode', $Mode, '-EnginePath', $engine, '-ResultPath', $result, '-WorkName', $remoteWork, '-CredentialFromInput')
    foreach ($key in $Arguments.Keys) { $list += @(('-' + $key), $Arguments[$key]) }
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { $InputLine | & $powerShell @list *> $log; $code = $LASTEXITCODE } finally { $ErrorActionPreference = $previous }
    Get-Content -LiteralPath $log | Where-Object { $_ -notmatch '"event":"progress"' } | ForEach-Object { Write-Host ('  ' + $_) }
    $json = $null
    if (Test-Path -LiteralPath $result) { $json = Get-Content -LiteralPath $result -Raw | ConvertFrom-Json }
    return @{ code = $code; log = (Get-Content -LiteralPath $log -Raw); result = $json }
}
$remoteLine = ConvertTo-Json -InputObject @{ targetUser = ''; targetPassword = ''; sourceUser = $account; sourcePassword = $secret } -Compress
$remoteDiscover = Invoke-Remote 'Discover' @{ Source = $address } 'remote-discover' $remoteLine
$remoteShareFound = @($remoteDiscover.result.shares | Where-Object { $_.name -eq $oldName })[0]
Assert ($remoteDiscover.code -eq 0 -and [bool]$remoteShareFound -and $remoteDiscover.log -match 'Connecting to the new server localhost through PowerShell remoting') ('From an admin computer, the new server reads the old server''s shares: ' + (($remoteDiscover.log -split "`n" | Where-Object { $_ -match 'Connect|ERROR' }) -join ' '))
Assert ($remoteDiscover.log -match ('Connected to ' + [regex]::Escape($address) + ' over (WinRM|DCOM) as ' + [regex]::Escape($account))) 'Inside the new server the engine reaches the old server with the old server''s account'
$remotePlan = Join-Path $OutputDirectory 'plan-remote.json'
$remoteFolder = Join-Path $root 'New\Remote'
$item = @{}
foreach ($property in $remoteShareFound.PSObject.Properties) { $item[$property.Name] = $property.Value }
$item['destName'] = 'SICMRemote'; $item['destPath'] = $remoteFolder; $item['previouslySynced'] = $false; $item['createdShare'] = $false; $item['allowOverwrite'] = $false; $item['expectedFiles'] = 43
ConvertTo-Json -InputObject @{ source = $address; sourceFqdn = $address; shares = @($item) } -Depth 6 | Set-Content -LiteralPath $remotePlan -Encoding UTF8
$remoteCheck = Invoke-Remote 'Precheck' @{ PlanPath = $remotePlan } 'remote-precheck' $remoteLine
Assert ($remoteCheck.code -eq 0 -and $remoteCheck.result.shares[0].ok -and [int]$remoteCheck.result.shares[0].files -eq 43) 'The remote check measures the old share from the new server'
Assert ($remoteCheck.log -match '"event":"share"') 'The engine''s progress events come back live from the new server'
$remoteSync = Invoke-Remote 'Sync' @{ PlanPath = $remotePlan } 'remote-sync' $remoteLine
Assert ($remoteSync.code -eq 0 -and $remoteSync.result.shares[0].status -eq 'succeeded' -and @(Get-ChildItem -LiteralPath $remoteFolder -Recurse -File -Force).Count -eq 43) 'The new server copies every file straight from the old one'
$item['previouslySynced'] = $true
ConvertTo-Json -InputObject @{ source = $address; sourceFqdn = $address; shares = @($item) } -Depth 6 | Set-Content -LiteralPath $remotePlan -Encoding UTF8
$remoteCut = Invoke-Remote 'Cutover' @{ PlanPath = $remotePlan } 'remote-cutover' $remoteLine
$remoteCreated = Get-SmbShare -Name 'SICMRemote' -ErrorAction SilentlyContinue
Assert ($remoteCut.code -eq 0 -and [bool]$remoteCreated -and $remoteCreated.Path -eq $remoteFolder -and $remoteCut.result.verification[0].verified) 'The remote cutover creates the share on the new server and verifies it'
$remoteBase = Join-Path $env:ProgramData ('Server Inventory and Migration Manager\File-Share-Migration\' + $remoteWork)
Assert ((Test-Path -LiteralPath $remoteBase) -and @(Get-ChildItem -LiteralPath (Join-Path $remoteBase 'Runs') -Force -ErrorAction SilentlyContinue).Count -eq 0) 'The new server keeps only data in its migration folder and every run folder is removed'
$baseAcl = Get-Acl -LiteralPath (Split-Path -Path $remoteBase -Parent)
$baseSids = @($baseAcl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]) | ForEach-Object { $_.IdentityReference.Value } | Sort-Object -Unique)
Assert ($baseAcl.AreAccessRulesProtected -and (($baseSids -join ',') -eq 'S-1-5-18,S-1-5-32-544')) ('The migration folder on the new server is protected to SYSTEM and Administrators: ' + ($baseSids -join ','))
Assert (-not (((& net.exe use) -join ' ') -match [regex]::Escape('\\' + $address))) 'No file connection to the old server is left behind'
Remove-SmbShare -Name 'SICMRemote' -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $remoteBase -Recurse -Force -ErrorAction SilentlyContinue
$leaked = @(Get-ChildItem -LiteralPath $OutputDirectory -Recurse -File | Where-Object { $_.Name -notmatch '^plan' } | Where-Object { $text = [IO.File]::ReadAllText($_.FullName); $text.Contains($secret) -or $text.Contains($wrongSecret) })
Assert ($leaked.Count -eq 0) ('No log, result or work file contains the password: ' + (($leaked | ForEach-Object { $_.Name }) -join ', '))
Remove-LocalUser -Name $accountName -ErrorAction SilentlyContinue
Remove-ItemProperty -LiteralPath $policy -Name 'LocalAccountTokenFilterPolicy' -ErrorAction SilentlyContinue
Set-Item -Path 'WSMan:\localhost\Client\TrustedHosts' -Value '' -Force

# --- Clean up ----------------------------------------------------------------------------------
foreach ($name in @($oldName, $newName)) { Remove-SmbShare -Name $name -Force -ErrorAction SilentlyContinue }
Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
Write-Host ('Migration validation passed: {0} checks.' -f $checks.Count)
$checks | Set-Content -LiteralPath (Join-Path $OutputDirectory 'checks.txt') -Encoding UTF8
# The last engine run was an expected refusal; report success explicitly.
exit 0
