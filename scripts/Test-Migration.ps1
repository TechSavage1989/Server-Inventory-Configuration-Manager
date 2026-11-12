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
function Invoke-Engine([string]$Mode, [hashtable]$Arguments, [string]$Name) {
    $log = Join-Path $OutputDirectory ($Name + '.log')
    $result = Join-Path $OutputDirectory ($Name + '.json')
    $list = @('-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', $engine, '-Mode', $Mode, '-ResultPath', $result, '-WorkPath', $work)
    foreach ($key in $Arguments.Keys) { $list += @(('-' + $key), $Arguments[$key]) }
    # Engine errors on stderr must be recorded, not end this script.
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { & $powerShell @list *> $log; $code = $LASTEXITCODE } finally { $ErrorActionPreference = $previous }
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
$missing = Invoke-Engine 'Discover' @{ Source = 'sicm-no-such-server.invalid' } 'discover-missing'
Assert ($missing.code -ne 0 -and $missing.log -match 'could not be found') 'An unknown server name is reported plainly'

function Write-Plan([string]$Path, [string]$Destination, [bool]$Synced, [bool]$Created) {
    $item = @{}
    foreach ($property in $share.PSObject.Properties) { $item[$property.Name] = $property.Value }
    $item['destName'] = $newName; $item['destPath'] = $Destination; $item['previouslySynced'] = $Synced; $item['createdShare'] = $Created; $item['expectedFiles'] = 43
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

Set-Content -LiteralPath (Join-Path $old 'Budget.txt') -Value 'version 2'
Set-Content -LiteralPath (Join-Path $old 'New-Plan.txt') -Value 'added after the first copy'
Remove-Item -LiteralPath (Join-Path $old 'Remove-Me.txt')
Write-Plan $plan $newFolder $true $false
$sync = Invoke-Engine 'Sync' @{ PlanPath = $plan } 'sync-2'
Assert ($sync.code -eq 0) 'Second copy succeeds'
Assert ((Get-Content -LiteralPath (Join-Path $newFolder 'Budget.txt') -Raw) -match 'version 2' -and (Test-Path -LiteralPath (Join-Path $newFolder 'New-Plan.txt'))) 'Second copy brings over changed and new files'
Assert (-not (Test-Path -LiteralPath (Join-Path $newFolder 'Remove-Me.txt'))) 'Second copy removes files deleted on the old server'
Assert ([int]$sync.result.shares[0].files[1] -eq 2) 'Second copy copies only what changed'

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

# --- Clean up ----------------------------------------------------------------------------------
foreach ($name in @($oldName, $newName)) { Remove-SmbShare -Name $name -Force -ErrorAction SilentlyContinue }
Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
Write-Host ('Migration validation passed: {0} checks.' -f $checks.Count)
$checks | Set-Content -LiteralPath (Join-Path $OutputDirectory 'checks.txt') -Encoding UTF8
