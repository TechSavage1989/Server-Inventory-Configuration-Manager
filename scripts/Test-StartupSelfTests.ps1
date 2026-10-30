# Synthetic diagnostic only: never execute the inventory collector's top-level code.
[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$CollectorPath, [switch]$WrapperProbe, [string]$WrapperResultPath)
$ErrorActionPreference = 'Stop'
$expectedHash = 'B072C3CF15F1CEF14DF397718A311A753CFAE0A7B12EBF4380FBED4A2D01C091'
$actualHash = (Get-FileHash -LiteralPath $CollectorPath -Algorithm SHA256).Hash
if ($actualHash -ne $expectedHash) { throw 'This diagnostic requires the exact published 3.2.10 collector.' }
Write-Host ('PowerShell: ' + $PSVersionTable.PSVersion.ToString())
Write-Host ('OS: ' + [Environment]::OSVersion.VersionString)
Write-Host ('64-bit process: ' + [Environment]::Is64BitProcess)
Write-Host ('Collector SHA256: ' + $actualHash)
$clrModule=[Diagnostics.Process]::GetCurrentProcess().Modules | Where-Object { $_.ModuleName -eq 'clr.dll' } | Select-Object -First 1
if ($clrModule) { Write-Host ('Loaded CLR file version: ' + $clrModule.FileVersionInfo.FileVersion) }
Write-Host ('PowerShell assembly version: ' + [Diagnostics.FileVersionInfo]::GetVersionInfo([Management.Automation.PSObject].Assembly.Location).FileVersion)
if ($WrapperProbe) {
    $wrapper=Join-Path (Split-Path -Parent $CollectorPath) 'Run-Inventory.ps1'
    if ((Get-FileHash -LiteralPath $wrapper -Algorithm SHA256).Hash -ne 'DFB7922B697BF6110EACD02FFF1053EC5B0A3724DD5E4C1D98D6C4DBE45BAEC7') { throw 'Unexpected wrapper bytes.' }
    if ([string]::IsNullOrWhiteSpace($WrapperResultPath)) { throw 'Wrapper result path is required.' }
    function global:Get-CimInstance {
        [CmdletBinding()]
        param([string]$ClassName)
        throw 'SYNTHETIC_INVENTORY_BARRIER'
    }
    if ((Get-Command Get-CimInstance).CommandType -ne 'Function') { throw 'Inventory barrier not installed.' }
    $syntheticRoot=Join-Path ([IO.Path]::GetTempPath()) ('inventory-wrapper-'+[Guid]::NewGuid().ToString('N'))
    Write-Host 'BEGIN byte-identical wrapper and collector with first-query barrier; expected controlled failure'
    & $wrapper -CollectorPath $CollectorPath -OutputRoot $syntheticRoot -PackageName 'synthetic-wrapper' -RunId 'SYNTHETIC' -ResultPath $WrapperResultPath -InventoryCategoryList 'Hardware;OperatingSystem;Storage;Networking;DNS;DHCP;ActiveDirectory;GroupPolicy;FileServices;SharesPermissions;PrintServices;IIS;SQLServer;HyperV;FailoverClustering;WindowsServerBackup;Certificates;ScheduledTasks;Services;InstalledSoftware;WindowsFeaturesRoles;Firewall;RRAS;NPS;BitLocker;SMB;DFS;WindowsUpdate;EventLogs;LocalAccounts;Power;TimeNTP;Security;RemoteManagement' -ExportCategoryList 'DHCP;DNS;GroupPolicy;IIS;Firewall;Certificates;ScheduledTasks;SharesPermissions;PrintServices;RRAS;SQLServer;Registry' -EventLogDays 30
    throw 'Wrapper unexpectedly returned instead of terminating with controlled barrier failure.'
}

$tokens=$null; $parseErrors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile($CollectorPath,[ref]$tokens,[ref]$parseErrors)
if ($parseErrors.Count) { throw 'Collector parsing failed.' }
$functions=@($ast.EndBlock.Statements | Where-Object { $_ -is [Management.Automation.Language.FunctionDefinitionAst] })
foreach ($function in $functions) { . ([scriptblock]::Create($function.Extent.Text)) }
Write-Host ('Loaded function definitions only: ' + $functions.Count)
$tests=@($ast.EndBlock.Statements | Where-Object {
 $_ -is [Management.Automation.Language.TryStatementAst] -and $_.Extent.Text.Contains('$reportSelfTestGaps = New-Object')
})
if ($tests.Count -ne 1) { throw 'Expected exactly one published startup report self-test block.' }
foreach ($statement in $tests[0].Body.Statements) {
 $line=$statement.Extent.StartLineNumber
 Write-Host ('BEGIN startup self-test source line ' + $line)
 . ([scriptblock]::Create($statement.Extent.Text))
 Write-Host ('PASS startup self-test source line ' + $line)
}
Write-Host 'PASS: exact published startup report self-tests completed. No inventory commands executed.'
Write-Host 'BEGIN unmodified whole startup self-test block'
. ([scriptblock]::Create($tests[0].Extent.Text))
Write-Host 'PASS unmodified whole startup self-test block'
# Execute the original startup prefix, ending strictly BEFORE the first inventory command.
$firstInventory=@($ast.EndBlock.Statements | Where-Object { $_.Extent.Text -match '^\$os = Get-CimInstance -ClassName Win32_OperatingSystem' })
if ($firstInventory.Count -ne 1) { throw 'Unable to establish safe startup boundary.' }
$source=[IO.File]::ReadAllText($CollectorPath)
$prefix=$source.Substring(0,$firstInventory[0].Extent.StartOffset)
$syntheticRoot=Join-Path ([IO.Path]::GetTempPath()) ('inventory-startup-'+[Guid]::NewGuid().ToString('N'))
Write-Host 'BEGIN original startup prefix (terminates before the first CIM inventory query)'
& ([scriptblock]::Create($prefix)) -OutputRoot $syntheticRoot -PackageName 'synthetic-startup' -RunId 'SYNTHETIC' -InventoryCategoryList 'All' -ExportCategoryList 'DHCP;DNS;GroupPolicy;IIS;Firewall;Certificates;ScheduledTasks;SharesPermissions;PrintServices;RRAS;SQLServer;Registry'
if ($script:transcriptStarted) { Stop-Transcript | Out-Null }
Write-Host 'PASS original startup prefix; no inventory queries were executed.'
# Preserve the exact complete script compilation context while preventing inventory.
# The first inventory operation is an unqualified Get-CimInstance call. A function
# takes precedence over that cmdlet and throws before any CIM query can occur.
function global:Get-CimInstance {
    [CmdletBinding()]
    param([string]$ClassName)
    throw 'SYNTHETIC_INVENTORY_BARRIER'
}
if ((Get-Command Get-CimInstance).CommandType -ne 'Function') { throw 'Inventory barrier not installed.' }
Write-Host 'BEGIN byte-identical complete collector with first-query barrier'
$barrierReached=$false
try {
    & $CollectorPath -OutputRoot $syntheticRoot -PackageName 'synthetic-complete-script' -RunId 'SYNTHETIC' -InventoryCategoryList 'All' -ExportCategoryList 'DHCP;DNS;GroupPolicy;IIS;Firewall;Certificates;ScheduledTasks;SharesPermissions;PrintServices;RRAS;SQLServer;Registry'
}
catch {
    if ($_.Exception.Message -ne 'SYNTHETIC_INVENTORY_BARRIER') { throw }
    $barrierReached=$true
}
finally {
    if ($script:transcriptStarted) { Stop-Transcript | Out-Null }
    Remove-Item Function:\Get-CimInstance -ErrorAction SilentlyContinue
}
if (-not $barrierReached) { throw 'Expected first-inventory barrier was not reached.' }
Write-Host 'PASS byte-identical complete collector reached the barrier; zero inventory queries executed.'
