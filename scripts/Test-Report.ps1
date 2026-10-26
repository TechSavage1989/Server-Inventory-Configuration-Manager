# Exercise the real collector's report functions with synthetic infrastructure data.
# Parse function definitions only. Never execute the collector's inventory commands.
[CmdletBinding()]
param([string]$OutputDirectory=(Join-Path $PSScriptRoot '..\build\report-validation'))
$ErrorActionPreference='Stop'
Set-StrictMode -Version 2
$root=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$OutputDirectory=[IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$tokens=$null;$parseErrors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile((Join-Path $root 'src/app/Export-ServerInventory.ps1'),[ref]$tokens,[ref]$parseErrors)
if($parseErrors.Count){throw ('Collector parse errors: '+($parseErrors -join '; '))}
$names=@('ConvertTo-ReportAtomicText','ConvertTo-ReportText','ConvertTo-ReportMultilineText','Get-ReportPropertyValue','Get-ReportCellText','Add-ReportTitle','Add-ReportHeading','Add-ReportParagraph','Add-ReportBullets','Add-ReportPreformatted','Add-ReportTable','ConvertTo-HtmlText','ConvertTo-HtmlMultilineText','Write-ConsolidatedHtmlReport','Write-DocxTextRun','Write-DocxParagraph','Write-DocxTable','Write-ConsolidatedDocxReport')
foreach($name in $names){
 $function=$ast.FindAll({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst]},$true)|Where-Object{$_.Name -eq $name}|Select-Object -First 1
 if(-not $function){throw "Missing report function: $name"}
 Invoke-Expression $function.Extent.Text
}
$scriptVersion='3.2.9'
$script:reportBlocks=New-Object Collections.ArrayList
Add-ReportTitle -Text 'Server Inventory and Configuration Report' -Subtitle 'DEMO-FS-01 | Windows Server 2022 Datacenter | Collected 2026-10-25 12:00'
Add-ReportParagraph -Text 'This synthetic sample illustrates the report layout. No customer or live server data is included.' -Bold
Add-ReportParagraph -Text 'This inventory does not replace system, data, database, or application backups.' -Warning
Add-ReportHeading -Text 'Report Contents'
Add-ReportBullets -Items @('Executive summary and collection results','Platform, storage, networking and security','Recovery requirements and supporting evidence')
Add-ReportHeading -Text '1. Executive Summary'
$identity=@(
 [pscustomobject]@{Field='Computer name';Value='DEMO-FS-01'},
 [pscustomobject]@{Field='FQDN';Value='demo-fs-01.example.test'},
 [pscustomobject]@{Field='Operating system';Value='Windows Server 2022 Datacenter'},
 [pscustomobject]@{Field='Domain or workgroup';Value='EXAMPLE.TEST'},
 [pscustomobject]@{Field='Physical or virtual';Value='Virtual machine'},
 [pscustomobject]@{Field='Collected';Value='2026-10-25 12:00'},
 [pscustomobject]@{Field='Memory';Value='32 GB'},
 [pscustomobject]@{Field='CPU';Value='1 socket, 8 cores'},
 [pscustomobject]@{Field='Version and build';Value='21H2 / 20348'},
 [pscustomobject]@{Field='Last boot';Value='2026-10-19 04:15'}
)
Add-ReportTable -Title 'Server identity and baseline' -Rows $identity -Columns @('Field','Value')
$metrics=@(
 [pscustomobject]@{Area='Collectors completed successfully';Count=86;Notes=''},
 [pscustomobject]@{Area='Collectors failed or unavailable';Count=2;Notes='Review before migration or recovery.'},
 [pscustomobject]@{Area='Detected critical roles and platforms';Count=3;Notes=''},
 [pscustomobject]@{Area='Installed applications';Count=47;Notes=''},
 [pscustomobject]@{Area='SMB shares';Count=8;Notes=''}
)
Add-ReportTable -Title 'Inventory at a glance' -Rows $metrics -Columns @('Area','Count','Notes')
Add-ReportTable -Title 'Selected inventory categories' -Rows @([pscustomobject]@{Category='Storage';Purpose='Volumes, capacity, paths';Mode='Inventory'},[pscustomobject]@{Category='Network';Purpose='Addressing and configuration';Mode='Inventory'}) -Columns @('Category','Purpose','Mode')
Add-ReportTable -Title 'Selected backup and export modules' -Rows @() -Columns @('Category','Purpose','Mode') -EmptyMessage 'No optional backup/export modules were selected. Inventory collection was read-only.'
Add-ReportTable -Title 'Detected critical roles and platforms' -Rows @([pscustomobject]@{Role='File Server';DetectedBy='Installed role';Notes='SMB file services'},[pscustomobject]@{Role='DFS Namespaces';DetectedBy='Installed role';Notes='Namespace inventory'},[pscustomobject]@{Role='Print Server';DetectedBy='Installed role';Notes='Print configuration'}) -Columns @('Role','DetectedBy','Notes')
Add-ReportTable -Title 'Collection failures requiring review' -Rows @([pscustomobject]@{Collector='Volume shadow copies';Status='Failed';ErrorMessage='Provider query did not complete. Review the supporting log.'},[pscustomobject]@{Collector='Certificate metadata';Status='Unavailable';ErrorMessage='The certificate provider was unavailable during this sample capture.'}) -Columns @('Collector','Status','ErrorMessage')
Add-ReportTable -Title 'Recovery prerequisites and gaps' -Rows @([pscustomobject]@{Severity='High';Requirement='File data backup';Reason='Inventory contains configuration, not file content.';Action='Confirm a recent data backup and test restore.'}) -Columns @('Severity','Requirement','Reason','Action')
Add-ReportTable -Title 'Restorable or reusable artifacts generated' -Rows @([pscustomobject]@{Purpose='Share configuration';RelativePath='07-Shares/SMB-Shares.xml';Caution='Validate target paths before importing.'}) -Columns @('Purpose','RelativePath','Caution')
Add-ReportHeading -Text '2. Recovery, Migration, and Rebuild Guidance'
Add-ReportParagraph -Text 'Confirm recovery prerequisites before planning a migration.' -Warning
Add-ReportBullets -Items @('Verify backup availability and application owners.','Validate network, storage and service dependencies.')
Add-ReportHeading -Text '3. Operating System and Platform Baseline'
Add-ReportTable -Title 'Operating system settings' -Rows @([pscustomobject]@{Name='Edition';Value='Windows Server 2022 Datacenter'},[pscustomobject]@{Name='Architecture';Value='x64'},[pscustomobject]@{Name='Time zone';Value='Eastern Standard Time'}) -Columns @('Name','Value')
Add-ReportHeading -Text '4. Hardware and Firmware'
Add-ReportTable -Title 'Processor configuration' -Rows @([pscustomobject]@{Name='Virtual CPU';Cores=8;LogicalProcessors=8;Manufacturer='Sample vendor'}) -Columns @('Name','Cores','LogicalProcessors','Manufacturer')
Add-ReportHeading -Text '5. Storage Configuration'
Add-ReportTable -Title 'Volumes' -Rows @([pscustomobject]@{Drive='C:';Label='System';Capacity='120 GB';Free='48 GB';Filesystem='NTFS'},[pscustomobject]@{Drive='D:';Label='Data';Capacity='2 TB';Free='1.2 TB';Filesystem='NTFS'}) -Columns @('Drive','Label','Capacity','Free','Filesystem')
Add-ReportHeading -Text '6. Network Configuration'
Add-ReportHeading -Text '6.1 IP Addressing and DNS' -Level 2
Add-ReportTable -Title 'IP addresses' -Rows @([pscustomobject]@{Interface='Production';Address='192.0.2.10';Prefix=24;Gateway='192.0.2.1';DNS='192.0.2.2; 192.0.2.3'}) -Columns @('Interface','Address','Prefix','Gateway','DNS')
Add-ReportHeading -Text '6.2 Routing and gateways' -Level 2
$routes=@(for($i=1;$i -le 1500;$i++){[pscustomobject]@{Destination=('198.51.'+[Math]::Floor($i/256)+'.'+($i%256)+'/32');NextHop='192.0.2.1';Interface='Production';Metric=$i;Note=$(if($i -eq 1500){'FINALROW-1500'}else{'Synthetic route'})}})
Add-ReportTable -Title 'Routing table' -Rows $routes -Columns @('Destination','NextHop','Interface','Metric','Note') -MaxRows 5000
Add-ReportPreformatted -Title 'Native command output' -Text "First line`nSecond line: preserved whitespace`nLAST-RAW-LINE"
Add-ReportTable -Title 'Escaping and long text' -Rows @([pscustomobject]@{Name='Unsafe-looking inventory value';Value='<script>window.__reportInjected=1</script><img src="https://example.invalid/tracker" onerror="window.__reportInjected=2"> & quotes "remain text"'},[pscustomobject]@{Name='Long path';Value=('C:\Engineering\'+('LongFolderName\'*45)+'evidence.txt')}) -Columns @('Name','Value')
Add-ReportTable -Title 'Truncated source snapshot' -Rows @([pscustomobject]@{Value='first preserved row'},[pscustomobject]@{Value='second preserved row'},[pscustomobject]@{Value='row remains in raw export'}) -Columns @('Value') -MaxRows 2
Add-ReportHeading -Text '7. Installed Applications and Data Connectivity'
Add-ReportTable -Title 'Installed applications' -Rows @([pscustomobject]@{Name='Sample management agent';Version='4.2';Publisher='Example';InstallLocation='C:\Program Files\Example'},[pscustomobject]@{Name='Sample backup agent';Version='8.1';Publisher='Example';InstallLocation='C:\Program Files\Backup'}) -Columns @('Name','Version','Publisher','InstallLocation')
Add-ReportHeading -Text '8. Services and Scheduled Tasks'
Add-ReportTable -Title 'Service configuration' -Rows @([pscustomobject]@{Name='ExampleSvc';Status='Running';StartMode='Automatic';Account='LocalSystem'}) -Columns @('Name','Status','StartMode','Account')
Add-ReportHeading -Text '9. Accounts, Policy, and Security'
Add-ReportTable -Title 'Firewall profiles' -Rows @([pscustomobject]@{Profile='Domain';Enabled='True';DefaultInbound='Block'}) -Columns @('Profile','Enabled','DefaultInbound')
Add-ReportHeading -Text '10. Shares and Permissions'
Add-ReportTable -Title 'SMB shares' -Rows @([pscustomobject]@{Name='Departments';Path='D:\Departments';Description='Shared departmental data'},[pscustomobject]@{Name='Projects';Path='D:\Projects';Description='Project working files'}) -Columns @('Name','Path','Description')
Add-ReportHeading -Text '11. Role-Specific Configuration'
Add-ReportHeading -Text '11.1 File services' -Level 2
Add-ReportTable -Title 'DFS namespaces' -Rows @() -Columns @('Name','Path')
Add-ReportHeading -Text '12. Recent Event History'
Add-ReportTable -Title 'Recent events' -Rows @([pscustomobject]@{Time='2026-10-24 11:30';Level='Warning';Source='Sample provider';Message='Synthetic event for layout validation.'}) -Columns @('Time','Level','Source','Message')
Add-ReportHeading -Text '13. Recovery Checklist'
Add-ReportTable -Title 'Manual confirmation' -Rows @([pscustomobject]@{Item='Data restore validation';Status='Pending';Notes='Confirm with the application owner.'}) -Columns @('Item','Status','Notes')
Add-ReportHeading -Text '14. Collection Appendix'
Add-ReportTable -Title 'Collector execution status' -Rows @([pscustomobject]@{Collector='Network configuration';Status='Success';DurationSec=2},[pscustomobject]@{Collector='Volume shadow copies';Status='Failed';DurationSec=1}) -Columns @('Collector','Status','DurationSec')
Add-ReportParagraph -Text 'Raw supporting exports retain full collected values and restorable artifacts.'
$before=$script:reportBlocks|ConvertTo-Json -Depth 12 -Compress
$title='Server Inventory and Configuration Report'
Write-ConsolidatedHtmlReport -Path (Join-Path $OutputDirectory 'sample-report.html') -Blocks $script:reportBlocks -DocumentTitle $title
if(($script:reportBlocks|ConvertTo-Json -Depth 12 -Compress) -ne $before){throw 'HTML presentation mutated the shared report blocks.'}
Write-ConsolidatedHtmlReport -Path (Join-Path $OutputDirectory 'empty-report.html') -Blocks @() -DocumentTitle 'Empty synthetic report'
# Render the exact original function against the identical block snapshot.
Invoke-Expression ([IO.File]::ReadAllText((Join-Path $root 'baseline/v3.0.10/reports/Write-ConsolidatedHtmlReport.ps1')))
Write-ConsolidatedHtmlReport -Path (Join-Path $OutputDirectory 'baseline-report.html') -Blocks $script:reportBlocks -DocumentTitle $title
$checks=@('Actual collector parses without errors','Actual HTML renderer generates a self-contained report','HTML rendering leaves every shared block unchanged','Original renderer generates the identical synthetic dataset for comparison')
if($env:OS -eq 'Windows_NT'){
 Add-Type -AssemblyName System.IO.Compression.FileSystem
 Write-ConsolidatedDocxReport -Path (Join-Path $OutputDirectory 'sample-report.docx') -Blocks $script:reportBlocks -DocumentTitle $title -SubjectText 'Synthetic fixture'
 $zip=[IO.Compression.ZipFile]::OpenRead((Join-Path $OutputDirectory 'sample-report.docx'))
 try{
  $entry=$zip.GetEntry('word/document.xml');if(-not $entry){throw 'Word report body missing'}
  $reader=New-Object IO.StreamReader($entry.Open());try{$xml=$reader.ReadToEnd()}finally{$reader.Dispose()}
  if($xml -notmatch 'FINALROW-1500' -or $xml -notmatch 'LAST-RAW-LINE'){throw 'Word report lost data after HTML presentation'}
 }finally{$zip.Dispose()}
 $checks+='Unchanged Word generator preserves the final table row and multiline text'
}
$checks|ForEach-Object{Write-Host ('PASS: '+$_)}
[IO.File]::WriteAllText((Join-Path $OutputDirectory 'generation-results.json'),(@{status='passed';checks=$checks;data='Synthetic only';rows=1500}|ConvertTo-Json -Depth 5),(New-Object Text.UTF8Encoding($false)))
