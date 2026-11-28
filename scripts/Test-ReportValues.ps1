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
foreach($name in @('ConvertTo-SubnetMaskText','Save-ObjectData','ConvertTo-ReportAtomicText','ConvertTo-ReportText','ConvertTo-ReportMultilineText','Get-ReportPropertyValue','Get-ReportCellText','Get-ReportData','Add-ReportTitle','Add-ReportHeading','Add-ReportTable','ConvertTo-HtmlText','ConvertTo-HtmlMultilineText','Write-ConsolidatedHtmlReport','Write-DocxTextRun','Write-DocxParagraph','Write-DocxTable','Write-ConsolidatedDocxReport')){
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

# Executive summary TCP/IP and activation facts: run the collector's own
# approved block against synthetic saved exports.
$collectorText=[IO.File]::ReadAllText((Join-Path $root 'src/app/Export-ServerInventory.ps1'))
$profileStart=$collectorText.IndexOf('    # BEGIN APPROVED PROFILE NETWORK AND ACTIVATION')
$profileEnd=$collectorText.IndexOf('    # END APPROVED PROFILE NETWORK AND ACTIVATION',$profileStart)
if($profileStart -lt 0 -or $profileEnd -lt 0){throw 'Profile network and activation block not found'}
$profileBlock=[scriptblock]::Create($collectorText.Substring($profileStart,$profileEnd-$profileStart))
$paths=@{System=(Join-Path $OutputDirectory 'profile-system')};New-Item -ItemType Directory -Path $paths.System -Force|Out-Null
function Test-Profile($Addresses,$Routes,$Dns,$Interfaces,$Activation){
 Remove-Item -Path (Join-Path $paths.System 'Windows-Activation-Metadata.*') -ErrorAction SilentlyContinue
 if($null -ne $Activation){Save-ObjectData -InputObject $Activation -BasePath (Join-Path $paths.System 'Windows-Activation-Metadata')}
 $script:ipAddresses=@($Addresses);$script:routes=@($Routes);$script:dnsClients=@($Dns);$script:ipInterfaces=@($Interfaces)
 $ipAddresses=$script:ipAddresses;$routes=$script:routes;$dnsClients=$script:dnsClients;$ipInterfaces=$script:ipInterfaces
 . $profileBlock
 [ordered]@{Activation=(& $profileText $profileActivation $activationEmpty);IPv4=(& $profileText $profileIPv4 'None found');IPv6=(& $profileText $profileIPv6 'None found');Assignment=(& $profileText $profileAssignment 'Not collected');Gateway=(& $profileText $profileGateways 'None found');Dns=(& $profileText $profileDns 'None found')}
}
$modern=Test-Profile -Addresses @(
 [pscustomobject]@{IPAddress='192.0.2.10';PrefixLength='24';InterfaceAlias='Ethernet0';AddressFamily='IPv4'},
 [pscustomobject]@{IPAddress='127.0.0.1';PrefixLength='8';InterfaceAlias='Loopback Pseudo-Interface 1';AddressFamily='IPv4'},
 [pscustomobject]@{IPAddress='169.254.10.1';PrefixLength='16';InterfaceAlias='Ethernet1';AddressFamily='IPv4'},
 [pscustomobject]@{IPAddress='fe80::1%5';PrefixLength='64';InterfaceAlias='Ethernet0';AddressFamily='IPv6'},
 [pscustomobject]@{IPAddress='2001:db8::10';PrefixLength='64';InterfaceAlias='Ethernet0';AddressFamily='IPv6'}
) -Routes @(
 [pscustomobject]@{DestinationPrefix='0.0.0.0/0';NextHop='192.0.2.1';InterfaceAlias='Ethernet0'},
 [pscustomobject]@{DestinationPrefix='192.0.2.0/24';NextHop='0.0.0.0';InterfaceAlias='Ethernet0'},
 [pscustomobject]@{DestinationPrefix='::/0';NextHop='::';InterfaceAlias='Ethernet0'}
) -Dns @(
 [pscustomobject]@{InterfaceAlias='Ethernet0';AddressFamily='IPv4';ServerAddresses='192.0.2.53; 192.0.2.54'},
 [pscustomobject]@{InterfaceAlias='Ethernet0';AddressFamily='IPv6';ServerAddresses='2001:db8::53'},
 [pscustomobject]@{InterfaceAlias='Loopback Pseudo-Interface 1';AddressFamily='IPv4';ServerAddresses='127.0.0.1'}
) -Interfaces @(
 [pscustomobject]@{InterfaceAlias='Ethernet0';AddressFamily='IPv4';Dhcp='Disabled'},
 [pscustomobject]@{InterfaceAlias='Loopback Pseudo-Interface 1';AddressFamily='IPv4';Dhcp='Disabled'}
) -Activation @([pscustomobject]@{Name='Windows(R), ServerStandard edition';Description='Windows(R) Operating System, VOLUME_KMSCLIENT channel';LicenseStatus=1;GracePeriodRemaining=259200;PartialProductKey='XXXXX'})
Assert-Equal $modern.IPv4 '192.0.2.10 / 255.255.255.0 (/24) (Ethernet0)' 'Profile IPv4 skips loopback and APIPA'
Assert-Equal $modern.IPv6 '2001:db8::10/64 (Ethernet0)' 'Profile IPv6 skips link-local'
Assert-Equal $modern.Gateway '192.0.2.1 (Ethernet0)' 'Profile default gateway skips on-link and empty IPv6 routes'
Assert-Equal $modern.Dns '192.0.2.53; 192.0.2.54' 'Profile DNS servers for addressed IPv4 interfaces'
Assert-Equal $modern.Assignment 'Static (Ethernet0)' 'Profile IP assignment'
Assert-Equal $modern.Activation 'Licensed (activated) - Windows(R), ServerStandard edition' 'Profile activation licensed'
$legacy=Test-Profile -Addresses @([pscustomobject]@{IPAddress='192.0.2.20';PrefixLength='';InterfaceAlias='Intel(R) PRO/1000';AddressFamily='IPv4'}) -Routes @([pscustomobject]@{DestinationPrefix='0.0.0.0 mask 0.0.0.0';NextHop='192.0.2.1';InterfaceAlias='11'}) -Dns @() -Interfaces @([pscustomobject]@{InterfaceAlias='Intel(R) PRO/1000';AddressFamily='IPv4';Dhcp='True'}) -Activation @([pscustomobject]@{Name='Windows(R), ServerDatacenter edition';LicenseStatus=2;GracePeriodRemaining=43200})
Assert-Equal $legacy.IPv4 '192.0.2.20 (Intel(R) PRO/1000)' 'Profile IPv4 from the legacy WMI collector'
Assert-Equal $legacy.Gateway '192.0.2.1 (11)' 'Profile gateway from the legacy WMI route table'
Assert-Equal $legacy.Assignment 'DHCP (Intel(R) PRO/1000)' 'Profile DHCP from the legacy WMI collector'
Assert-Equal $legacy.Dns 'None found' 'Profile DNS without data'
Assert-Equal $legacy.Activation 'Initial grace period, 30 day(s) left - Windows(R), ServerDatacenter edition' 'Profile activation grace period'
$none=Test-Profile -Addresses @() -Routes @() -Dns @() -Interfaces @() -Activation $null
Assert-Equal $none.Activation 'Not collected' 'Profile activation when the activation collector did not run'
Assert-Equal $none.IPv4 'None found' 'Profile IPv4 without data'
Write-Host 'PASS: executive summary TCP/IP and activation facts from saved exports'
Write-Host 'PASS: array-valued CSV, HTML, Word and raw CLIXML regression'
