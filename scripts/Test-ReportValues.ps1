# Synthetic export-to-report regression. Does not execute inventory collectors.
[CmdletBinding()]
param([string]$OutputDirectory=(Join-Path $PSScriptRoot '../build/report-values'))
$ErrorActionPreference='Stop'
Set-StrictMode -Version 2
$root=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$OutputDirectory=[IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile((Join-Path $root 'src/app/Export-ServerInventory.ps1'),[ref]$tokens,[ref]$errors)
if($errors.Count){throw ($errors -join '; ')}
$definitions=@($ast.FindAll({param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst]},$true))
foreach($name in @('Save-ObjectData','ConvertTo-ReportAtomicText','ConvertTo-ReportText','ConvertTo-ReportMultilineText','Get-ReportPropertyValue','Get-ReportCellText','Get-ReportData','Add-ReportTitle','Add-ReportHeading','Add-ReportTable','ConvertTo-HtmlText','ConvertTo-HtmlMultilineText','Write-ConsolidatedHtmlReport','Write-DocxTextRun','Write-DocxParagraph','Write-DocxTable','Write-ConsolidatedDocxReport')){
 $f=$definitions|Where-Object{$_.Name -eq $name}|Select-Object -First 1
 if(-not $f){throw "Missing function $name"};Invoke-Expression $f.Extent.Text
}
function Assert-Equal($Actual,$Expected,$Message){if($Actual -cne $Expected){throw "$Message`: expected '$Expected', got '$Actual'"}}
$list=New-Object 'System.Collections.Generic.List[string]';$list.Add('2001:db8::1');$list.Add('雪.example.test')
$nested=New-Object Collections.ArrayList;[void]$nested.Add([string[]]@('nested-a','nested-b'))
$cycle=New-Object Collections.ArrayList;[void]$cycle.Add($cycle)
$row=[pscustomobject][ordered]@{
 ComputerName='SYNTHETIC-DNS.example.test';ListeningIPAddress=[string[]]@('192.0.2.10','2001:db8::10');AllIPAddress=[string[]]@('192.0.2.10','198.51.100.5');RoundRobin=$true
 Empty=[string[]]@();Null=$null;Unicode=$list;Nested=$nested;Cycle=$cycle;LongList=[string[]](1..130|ForEach-Object{"member-$_"});Literal='System.String[]';Multiline="first`nsecond";Count=0;LongScalar=('x'*6000);ErrorText='[unavailable: synthetic error]'
}
$base=Join-Path $OutputDirectory 'dns'
# Negative control: actual old Export-Csv behavior, not a hardcoded fixture.
$row|Export-Csv -LiteralPath ($base+'-old.csv') -NoTypeInformation -Encoding UTF8
$old=@(Import-Csv -LiteralPath ($base+'-old.csv'))[0]
Assert-Equal $old.ListeningIPAddress 'System.String[]' 'Negative control must reproduce array type-name loss'
Save-ObjectData -InputObject @($null,$row,$null) -BasePath $base
$csv=@(Import-Csv -LiteralPath ($base+'.csv'))[0]
Assert-Equal $csv.ListeningIPAddress '192.0.2.10; 2001:db8::10' 'DNS listening addresses'
Assert-Equal $csv.AllIPAddress '192.0.2.10; 198.51.100.5' 'DNS all addresses'
Assert-Equal $csv.Unicode '2001:db8::1; 雪.example.test' 'Generic list and Unicode'
Assert-Equal $csv.Empty '' 'Empty collection';Assert-Equal $csv.Null '' 'Null';Assert-Equal $csv.Literal 'System.String[]' 'Legitimate scalar type-name text must not be guessed'
foreach($name in @('ComputerName','RoundRobin','Multiline','Count','ErrorText','Literal','LongScalar')){Assert-Equal $csv.$name $old.$name "Scalar CSV parity $name"}
Assert-Equal ($csv.PSObject.Properties.Name -join '|') ($old.PSObject.Properties.Name -join '|') 'CSV schema/order'
if($csv.Nested -notmatch 'nested values retained in supporting export'){throw 'Nested collection must be explicitly bounded'}
if($csv.Cycle -notmatch 'self-reference omitted'){throw 'Cycle must be explicitly bounded'}
if($csv.LongList -notmatch 'additional values omitted'){throw 'Long list omission must be explicit'}
$typed=@(Import-Clixml -LiteralPath ($base+'.clixml') | Where-Object {$null -ne $_})[0]
Assert-Equal (@($typed.ListeningIPAddress) -join '|') '192.0.2.10|2001:db8::10' 'Raw typed addresses retained'
Assert-Equal @($typed.LongList).Count 130 'Full raw long list retained'
Assert-Equal (@($row.ListeningIPAddress) -join '|') '192.0.2.10|2001:db8::10' 'Input not mutated'
$flat=@(Get-ReportData -BasePath $base)
Assert-Equal $flat[0].ListeningIPAddress $csv.ListeningIPAddress 'CSV values reach report'
$script:reportBlocks=New-Object Collections.ArrayList
$scriptVersion='3.2.11'
Add-ReportTitle -Text 'Synthetic value regression' -Subtitle 'No customer data'
Add-ReportHeading -Text '1. DNS'
Add-ReportTable -Title 'DNS server settings' -Rows $flat -Columns @('ComputerName','ListeningIPAddress','AllIPAddress','RoundRobin','Unicode','Empty','ErrorText')
$htmlPath=Join-Path $OutputDirectory 'values.html';$docxPath=Join-Path $OutputDirectory 'values.docx'
Write-ConsolidatedHtmlReport -Path $htmlPath -Blocks $script:reportBlocks -DocumentTitle 'Synthetic values'
Write-ConsolidatedDocxReport -Path $docxPath -Blocks $script:reportBlocks -DocumentTitle 'Synthetic values' -SubjectText 'Synthetic array values'
$html=[IO.File]::ReadAllText($htmlPath)
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip=[IO.Compression.ZipFile]::OpenRead($docxPath)
try{$reader=New-Object IO.StreamReader($zip.GetEntry('word/document.xml').Open());try{$xml=$reader.ReadToEnd()}finally{$reader.Dispose()}}finally{$zip.Dispose()}
foreach($value in @('192.0.2.10; 2001:db8::10','192.0.2.10; 198.51.100.5','2001:db8::1; 雪.example.test')){
 if(-not $html.Contains([Net.WebUtility]::HtmlEncode($value))){throw "HTML missing $value"}
 if(-not $xml.Contains($value)){throw "DOCX missing $value"}
}
[ordered]@{status='passed';data='Synthetic only';checks=@('Real CSV negative control','DNS arrays and generic lists','Scalar formatting and schema parity','Explicit nesting/cycle/long-list limits','Unmodified full CLIXML and input','CSV-to-report values','HTML and DOCX actual values');limitations=@('Run on current PowerShell runtime; Windows PowerShell CI still required.')}|ConvertTo-Json -Depth 5|Set-Content -LiteralPath (Join-Path $OutputDirectory 'results.json') -Encoding UTF8
Write-Host 'PASS: array-valued CSV, HTML, Word and raw CLIXML regression'
