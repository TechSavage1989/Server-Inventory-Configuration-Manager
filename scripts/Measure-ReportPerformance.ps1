# Synthetic-only timing; loads function definitions, never inventory commands.
[CmdletBinding()]
param(
    [string]$SourcePath=(Join-Path $PSScriptRoot '../src/app/Export-ServerInventory.ps1'),
    [string]$OutputDirectory=(Join-Path $PSScriptRoot '../build/report-performance'),
    [int]$Rows=5000,
    [int]$Tables=4,
    [int]$Iterations=3
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version 2
$OutputDirectory=[IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Force $OutputDirectory | Out-Null
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile([IO.Path]::GetFullPath($SourcePath),[ref]$tokens,[ref]$errors)
if($errors.Count){throw ($errors -join '; ')}
$names=@('ConvertTo-ReportAtomicText','ConvertTo-ReportText','ConvertTo-ReportMultilineText','Get-ReportPropertyValue','Get-ReportCellText','Add-ReportTitle','Add-ReportHeading','Add-ReportParagraph','Add-ReportBullets','Add-ReportPreformatted','Add-ReportTable','ConvertTo-HtmlText','ConvertTo-HtmlMultilineText','Write-ConsolidatedHtmlReport','Write-DocxTextRun','Write-DocxParagraph','Write-DocxTable','Write-ConsolidatedDocxReport')
$functions=$ast.FindAll({param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst]},$true)
foreach($name in $names){$fn=$functions|Where-Object Name -eq $name|Select-Object -First 1;if(-not $fn){throw "Missing $name"};Invoke-Expression $fn.Extent.Text}
$scriptVersion='3.2.11'
$data=@(for($i=1;$i -le $Rows;$i++){[pscustomobject]@{Name="SYNTHETIC-$i";Path=('C:\Synthetic\'+('Long path segment\'*8)+"$i.txt");Command='program.exe --argument "<escaped> & Unicode Ω 中文"';Status='Success';Values=@('alpha','beta','gamma');Description="Line one`nLine two with representative settings";Empty='';Number=$i}})
$results=@()
for($iteration=0;$iteration -le $Iterations;$iteration++){
 $script:reportBlocks=New-Object Collections.ArrayList
 $timer=[Diagnostics.Stopwatch]::StartNew()
 Add-ReportTitle -Text 'Synthetic performance report' -Subtitle 'No customer data'
 Add-ReportHeading -Text '1. Executive Summary'
 for($table=1;$table -le $Tables;$table++){Add-ReportTable -Title "Synthetic table $table" -Rows $data -Columns @('Name','Path','Command','Status','Values','Description','Empty','Number') -MaxRows 0}
 $snapshotMs=$timer.Elapsed.TotalMilliseconds;$timer.Restart()
 $html=Join-Path $OutputDirectory 'benchmark.html'
 Write-ConsolidatedHtmlReport -Path $html -Blocks $script:reportBlocks -DocumentTitle 'Synthetic performance report'
 $htmlMs=$timer.Elapsed.TotalMilliseconds;$timer.Restart()
 $docx=Join-Path $OutputDirectory 'benchmark.docx'
 Write-ConsolidatedDocxReport -Path $docx -Blocks $script:reportBlocks -DocumentTitle 'Synthetic performance report' -SubjectText 'Synthetic only'
 $docxMs=$timer.Elapsed.TotalMilliseconds;$timer.Stop()
 $result=[pscustomobject]@{Iteration=$iteration;Warmup=($iteration -eq 0);RowsPerTable=$Rows;Tables=$Tables;Cells=($Rows*$Tables*8);SnapshotMs=[math]::Round($snapshotMs,2);HtmlMs=[math]::Round($htmlMs,2);DocxMs=[math]::Round($docxMs,2);HtmlBytes=(Get-Item $html).Length;DocxBytes=(Get-Item $docx).Length}
 $results+=$result;$result|ConvertTo-Json -Compress|Write-Host
}
[pscustomobject]@{Runtime=$PSVersionTable.PSVersion.ToString();Platform=[Environment]::OSVersion.ToString();SourceSHA256=(Get-FileHash $SourcePath -Algorithm SHA256).Hash;Results=$results}|ConvertTo-Json -Depth 5|Set-Content -Encoding UTF8 (Join-Path $OutputDirectory 'timings.json')
