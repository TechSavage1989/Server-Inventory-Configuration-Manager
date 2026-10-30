# Prove fast paths retain conversion and cell output, including fallback inputs.
# Functions only: no inventory commands are executed.
[CmdletBinding()]
param()
$ErrorActionPreference='Stop'
Set-StrictMode -Version 2
$root=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$names=@('ConvertTo-ReportAtomicText','ConvertTo-ReportText','ConvertTo-ReportMultilineText','Get-ReportPropertyValue','Get-ReportCellText','ConvertTo-HtmlText','ConvertTo-HtmlMultilineText','Write-ConsolidatedHtmlReport','Write-DocxTextRun','Write-DocxParagraph','Write-DocxTable','Write-ConsolidatedDocxReport')
function Read-Functions([string]$Path){
 $tokens=$null;$errors=$null
 $ast=[Management.Automation.Language.Parser]::ParseFile($Path,[ref]$tokens,[ref]$errors)
 if($errors.Count){throw ($errors -join '; ')}
 $definitions=@{}
 foreach($fn in $ast.FindAll({param($n)$n -is [Management.Automation.Language.FunctionDefinitionAst]},$true)){if($names -contains $fn.Name){$definitions[$fn.Name]=$fn.Extent.Text}}
 return $definitions
}
$current=Read-Functions (Join-Path $root 'src/app/Export-ServerInventory.ps1')
$original=Read-Functions (Join-Path $root 'baseline/v3.2.10/collectors/Export-ServerInventory.ps1')
# Preserve the separately approved 3.2.11 cycle-identity fix in the reference.
$original['Get-ReportPropertyValue']=$current['Get-ReportPropertyValue']
$before=New-Module -ScriptBlock {param($defs)foreach($name in $defs.Keys){Invoke-Expression $defs[$name]}} -ArgumentList $original
$after=New-Module -ScriptBlock {param($defs)foreach($name in $defs.Keys){Invoke-Expression $defs[$name]}} -ArgumentList $current
$cycle=New-Object Collections.ArrayList;[void]$cycle.Add($cycle)
$cases=New-Object Collections.ArrayList
foreach($value in @($null,'',' normal ','plain','Ω 中文 <&> "quotes"',"a`r`n b",("x"*2600),"a`tb",("a"+[char]1+"b"),42,[datetime]'2026-10-30',@('a','b'),$cycle)) {[void]$cases.Add($value)}
foreach($value in $cases){foreach($maximum in @(0,1,100,2500)){
 $expected=& $before {param($v,$m)ConvertTo-ReportText -Value $v -MaxLength $m} $value $maximum
 $actual=& $after {param($v,$m)ConvertTo-ReportText -Value $v -MaxLength $m} $value $maximum
 if($expected -cne $actual){throw "Conversion differs for maximum $maximum"}
}}
$rows=New-Object Collections.ArrayList
foreach($value in $cases){[void]$rows.Add([pscustomobject]@{Value=$value})}
$scriptProperty=New-Object PSObject
$scriptProperty|Add-Member ScriptProperty Value {throw "synthetic property error`nsecond line"}
[void]$rows.Add($scriptProperty)
[void]$rows.Add($null)
[void]$rows.Add(@{Value='Hashtable actual value'})
[void]$rows.Add([pscustomobject]@{Different='Missing column'})
$blocks=@([pscustomobject]@{Type='Table';Title='Synthetic fast-path parity';Rows=@($rows);Columns=@('Value');Labels=@{};TotalRows=$rows.Count;DisplayRows=$rows.Count;EmptyMessage='Empty'})
$output=Join-Path $root 'build/report-performance/parity';New-Item -ItemType Directory -Force $output|Out-Null
foreach($entry in @(@{Module=$before;Name='reference'},@{Module=$after;Name='candidate'})){
 & $entry.Module {param($p,$b)$scriptVersion='3.2.11';Write-ConsolidatedHtmlReport -Path $p -Blocks $b -DocumentTitle 'Synthetic'} (Join-Path $output ($entry.Name+'.html')) $blocks
}
$beforeCells=[regex]::Matches([IO.File]::ReadAllText((Join-Path $output 'reference.html')),'<td>(.*?)</td>',[Text.RegularExpressions.RegexOptions]::Singleline)
$afterCells=[regex]::Matches([IO.File]::ReadAllText((Join-Path $output 'candidate.html')),'<td>(.*?)</td>',[Text.RegularExpressions.RegexOptions]::Singleline)
if($beforeCells.Count -ne $rows.Count -or $afterCells.Count -ne $rows.Count){throw 'Unexpected rendered cell count'}
for($i=0;$i -lt $rows.Count;$i++){if($beforeCells[$i].Value -cne $afterCells[$i].Value){throw "HTML cell $i differs"}}
Write-Host "PASS: $($cases.Count*4) conversion comparisons; $($rows.Count) HTML cells byte-identical including raw/long/error/array/cyclic fallback."

foreach($entry in @(@{Module=$before;Name='reference'},@{Module=$after;Name='candidate'})){
 & $entry.Module {param($p,$b)Write-ConsolidatedDocxReport -Path $p -Blocks $b -DocumentTitle 'Synthetic' -SubjectText 'Synthetic only'} (Join-Path $output ($entry.Name+'.docx')) $blocks
}
Add-Type -AssemblyName System.IO.Compression.FileSystem
$documents=@()
foreach($name in @('reference','candidate')){
 $zip=[IO.Compression.ZipFile]::OpenRead((Join-Path $output ($name+'.docx')))
 try{$reader=New-Object IO.StreamReader($zip.GetEntry('word/document.xml').Open());try{$documents+= $reader.ReadToEnd()}finally{$reader.Dispose()}}finally{$zip.Dispose()}
}
if($documents[0] -cne $documents[1]){throw 'DOCX document XML differs after fixed-format table fast path'}
Write-Host 'PASS: DOCX document XML byte-identical for every fallback case, including multiline property errors.'
