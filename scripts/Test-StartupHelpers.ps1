# Report helper regressions only; never invoke the collector's top-level code.
[CmdletBinding()]
param()
$ErrorActionPreference='Stop'
$root=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$tokens=$null; $parseErrors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile((Join-Path $root 'src/app/Export-ServerInventory.ps1'),[ref]$tokens,[ref]$parseErrors)
if($parseErrors.Count){throw 'Collector failed syntax validation.'}
$names=@('ConvertTo-ReportAtomicText','ConvertTo-ReportText','Get-ReportPropertyValue','Get-ReportCellText','Add-ReportBullets','Add-ReportTable','Add-RoleRecoveryGap')
foreach($name in $names){
 $found=@($ast.EndBlock.Statements|Where-Object{$_ -is [Management.Automation.Language.FunctionDefinitionAst] -and $_.Name -eq $name})
 if($found.Count -ne 1){throw ('Missing report helper '+$name)}
 . ([scriptblock]::Create($found[0].Extent.Text))
}
$legacyPath=Join-Path $root 'tests/fixtures/startup/Get-ReportPropertyValue-v3.2.10.ps1'
if((Get-FileHash -LiteralPath $legacyPath -Algorithm SHA256).Hash -ne '12D12F3A34D751D9175F84F3EA01B14069805773A969E71781EC1DBCF6A50BFF'){throw 'Published negative-control getter changed.'}
$legacy=[IO.File]::ReadAllText($legacyPath).Replace('function Get-ReportPropertyValue {','function Get-LegacyReportPropertyValue {')
. ([scriptblock]::Create($legacy))
$checks=New-Object Collections.ArrayList
function Assert-Check([bool]$Condition,[string]$Name){if(-not $Condition){throw $Name};[void]$checks.Add($Name);Write-Host ('PASS '+$Name)}
# A finite negative control demonstrates the pipeline-enumeration defect safely.
# Do not run an unguarded cyclic legacy getter in this test runner process.
$list=New-Object Collections.ArrayList;[void]$list.Add('first');[void]$list.Add('second')
$row=[pscustomobject]@{Value=$list}
$legacyValue=Get-LegacyReportPropertyValue -InputObject $row -PropertyName Value
Assert-Check (-not [object]::ReferenceEquals($legacyValue,$list)) 'Published getter negative control loses collection identity'
$value=Get-ReportPropertyValue -InputObject $row -PropertyName Value
Assert-Check ([object]::ReferenceEquals($value,$list)) 'Getter preserves collection identity'
$empty=New-Object Collections.ArrayList
$value=Get-ReportPropertyValue -InputObject ([pscustomobject]@{Value=$empty}) -PropertyName Value
Assert-Check ([object]::ReferenceEquals($value,$empty)) 'Getter preserves an empty collection'
$singleton=New-Object Collections.ArrayList;[void]$singleton.Add('only')
$value=Get-ReportPropertyValue -InputObject ([pscustomobject]@{Value=$singleton}) -PropertyName Value
Assert-Check ([object]::ReferenceEquals($value,$singleton)) 'Getter preserves a singleton collection without scalar collapse'
Assert-Check ((Get-ReportPropertyValue -InputObject ([pscustomobject]@{Value='plain'}) -PropertyName Value) -ceq 'plain') 'Scalar string remains unchanged'
Assert-Check ((Get-ReportPropertyValue -InputObject ([pscustomobject]@{Value=42}) -PropertyName Value) -eq 42) 'Numeric property remains unchanged'
Assert-Check ($null -eq (Get-ReportPropertyValue -InputObject ([pscustomobject]@{Value=$null}) -PropertyName Value)) 'Null property remains null'
Assert-Check ($null -eq (Get-ReportPropertyValue -InputObject ([pscustomobject]@{}) -PropertyName Missing)) 'Absent property remains null'
$cycle=New-Object Collections.ArrayList;[void]$cycle.Add('cycle-root');[void]$cycle.Add($cycle)
$cycleRow=[pscustomobject]@{Name='cycle-row';Value=$cycle}
$value=Get-ReportPropertyValue -InputObject $cycleRow -PropertyName Value
Assert-Check ([object]::ReferenceEquals($value,$cycle)) 'Cyclic property reaches formatter without pipeline enumeration'
$text=Get-ReportCellText -InputObject $cycleRow -PropertyName Value
Assert-Check ($text -ceq 'cycle-root; [self-reference omitted]') 'Cell formatter retains readable cyclic root and detects original self-reference'
$script:reportBlocks=New-Object Collections.ArrayList
Add-ReportTable -Title 'Cyclic snapshot' -Rows @($cycleRow) -Columns @('Name','Value')
Assert-Check (($script:reportBlocks.Count -eq 1) -and ($script:reportBlocks[0].Rows[0].Value -is [string]) -and ($script:reportBlocks[0].Rows[0].Value -ceq $text)) 'Table stores a flat string for the cyclic property'
$deep='leaf'
for($i=0;$i -lt 150;$i++){$next=New-Object Collections.ArrayList;[void]$next.Add($deep);$deep=$next}
$value=Get-ReportPropertyValue -InputObject ([pscustomobject]@{Value=$deep}) -PropertyName Value
Assert-Check ([object]::ReferenceEquals($value,$deep)) 'Deep property retains identity without flattening'
$text=Get-ReportCellText -InputObject ([pscustomobject]@{Value=$deep}) -PropertyName Value
Assert-Check (($text -is [string]) -and ($text -match 'nested values retained')) 'Deep property is bounded to a nested-value marker'
# Execute the actual startup self-test try block after loading definitions only.
$startup=@($ast.EndBlock.Statements|Where-Object{$_ -is [Management.Automation.Language.TryStatementAst] -and $_.Extent.Text.Contains('$reportSelfTestGaps = New-Object')})
if($startup.Count -ne 1){throw 'Unable to find exact startup self-test block.'}
. ([scriptblock]::Create($startup[0].Extent.Text))
Assert-Check ($null -eq $script:reportBlocks) 'Actual complete startup report self-tests finish'
Write-Host ('Passed '+$checks.Count+' synthetic report-helper checks; no inventory commands executed.')
