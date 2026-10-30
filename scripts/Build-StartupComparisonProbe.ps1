# Build a synthetic-only bounded-child diagnostic; no collector top-level code runs.
[CmdletBinding()]
param([string]$OutputDirectory=(Join-Path $PSScriptRoot '../build/startup-comparison'))
$ErrorActionPreference='Stop'
$root=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$OutputDirectory=[IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$t=$null;$e=$null
$ast=[Management.Automation.Language.Parser]::ParseFile((Join-Path $root 'src/app/Export-ServerInventory.ps1'),[ref]$t,[ref]$e)
if($e.Count){throw 'Collector parsing failed.'}
$names=@('ConvertTo-ReportAtomicText','ConvertTo-ReportText','Get-ReportPropertyValue','Get-ReportCellText','Add-ReportBullets','Add-ReportTable','Add-RoleRecoveryGap')
$header=@'
# Synthetic report-helper probe only. No inventory, exports, admin, or network access.
$ErrorActionPreference='Stop'
Write-Host ('PowerShell: '+$PSVersionTable.PSVersion.ToString())
Write-Host ('OS version: '+[Environment]::OSVersion.VersionString)
Write-Host ('64-bit process: '+[Environment]::Is64BitProcess)
$clr=[Diagnostics.Process]::GetCurrentProcess().Modules | Where-Object {$_.ModuleName -eq 'clr.dll'} | Select-Object -First 1
if($clr){Write-Host ('CLR file version: '+$clr.FileVersionInfo.FileVersion)}
'@
$tests=@'
Write-Host ('Probe variant: '+$variant)
$list=New-Object Collections.ArrayList;[void]$list.Add('first');[void]$list.Add('second')
Write-Host 'BEGIN safe finite published negative control'
$oldValue=Get-LegacyReportPropertyValue -InputObject ([pscustomobject]@{Value=$list}) -PropertyName Value
if([object]::ReferenceEquals($oldValue,$list)){throw 'Published negative control unexpectedly preserved identity.'}
Write-Host 'PASS finite published negative control; known-fatal published cyclic path deliberately not rerun'
Write-Host 'BEGIN finite property identity'
$value=Get-ReportPropertyValue -InputObject ([pscustomobject]@{Value=$list}) -PropertyName Value
$preserved=[object]::ReferenceEquals($value,$list)
Write-Host ('PASS finite property identity; original reference preserved: '+$preserved)
if($variant -eq 'candidate' -and -not $preserved){throw 'Candidate lost collection identity.'}
$cycle=New-Object Collections.ArrayList;[void]$cycle.Add('cycle-root');[void]$cycle.Add($cycle)
$row=[pscustomobject]@{Name='cycle-row';Value=$cycle}
Write-Host 'BEGIN direct cyclic formatter'
$text=ConvertTo-ReportText -Value $cycle -MaxLength 1000
if($text -notmatch 'cycle-root'){throw 'Missing readable cyclic root.'}
Write-Host 'PASS direct cyclic formatter'
Write-Host 'BEGIN cyclic property getter'
$value=Get-ReportPropertyValue -InputObject $row -PropertyName Value
$preserved=[object]::ReferenceEquals($value,$cycle)
Write-Host ('PASS cyclic property getter; original reference preserved: '+$preserved)
if($variant -eq 'candidate' -and -not $preserved){throw 'Candidate cyclic getter lost collection identity.'}
Write-Host 'BEGIN scalar cell formatter'
$text=Get-ReportCellText -InputObject $row -PropertyName Name
if($text -cne 'cycle-row'){throw 'Scalar cell changed.'}
Write-Host 'PASS scalar cell formatter'
Write-Host 'BEGIN cyclic cell formatter'
$text=Get-ReportCellText -InputObject $row -PropertyName Value
if($text -notmatch 'cycle-root'){throw 'Cyclic cell lost readable root.'}
if($variant -eq 'candidate' -and $text -notmatch '\[self-reference omitted\]'){throw 'Original-reference guard was not reached.'}
Write-Host 'PASS cyclic cell formatter'
$script:reportBlocks=New-Object Collections.ArrayList
Write-Host 'BEGIN original cyclic table snapshot'
Add-ReportTable -Title 'Circular value snapshot test' -Rows @($row) -Columns @('Name','Value') -MaxRows 10
if($script:reportBlocks.Count -ne 1 -or -not ($script:reportBlocks[0].Rows[0].Value -is [string])){throw 'Table snapshot is not a flat string.'}
Write-Host 'PASS original cyclic table snapshot'
Write-Host 'PASS targeted candidate boundary checks'
'@
$legacyPath=Join-Path $root 'tests/fixtures/startup/Get-ReportPropertyValue-v3.2.10.ps1'
if((Get-FileHash $legacyPath -Algorithm SHA256).Hash -ne '12D12F3A34D751D9175F84F3EA01B14069805773A969E71781EC1DBCF6A50BFF'){throw 'Published getter fixture changed.'}
foreach($variant in @('candidate')){
 $getter=@($ast.EndBlock.Statements|Where-Object{$_ -is [Management.Automation.Language.FunctionDefinitionAst] -and $_.Name -eq 'Get-ReportPropertyValue'})[0].Extent.Text
 $sha=[Security.Cryptography.SHA256]::Create()
 try{$getterHash=([BitConverter]::ToString($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($getter)))).Replace('-','')}finally{$sha.Dispose()}
 $text=$header+"`n`$variant='$variant'`nWrite-Host 'Original collector SHA256: B072C3CF15F1CEF14DF397718A311A753CFAE0A7B12EBF4380FBED4A2D01C091'`nWrite-Host 'Candidate getter SHA256: $getterHash'`n"
 foreach($name in $names){
  $function=@($ast.EndBlock.Statements|Where-Object{$_ -is [Management.Automation.Language.FunctionDefinitionAst] -and $_.Name -eq $name})
  if($function.Count -ne 1){throw ('Missing helper '+$name)}
  $body=$function[0].Extent.Text
  if($variant -eq 'published' -and $name -eq 'Get-ReportPropertyValue'){$body=[IO.File]::ReadAllText($legacyPath)}
  $text+="`n"+$body+"`n"
 }
 $legacy=[IO.File]::ReadAllText($legacyPath).Replace('function Get-ReportPropertyValue {','function Get-LegacyReportPropertyValue {')
 $text+="`n"+$legacy+"`n"+$tests+"`n"
 $startup=@($ast.EndBlock.Statements|Where-Object{$_ -is [Management.Automation.Language.TryStatementAst] -and $_.Extent.Text.Contains('$reportSelfTestGaps = New-Object')})
 if($startup.Count -ne 1){throw 'Missing original startup self-test block.'}
 $text+="Write-Host 'BEGIN complete original startup self-test block'`n"+$startup[0].Extent.Text+"`nWrite-Host 'PASS ALL CANDIDATE STARTUP TESTS'`n"
 [IO.File]::WriteAllText((Join-Path $OutputDirectory ($variant+'.ps1')),$text,(New-Object Text.UTF8Encoding($true)))
}
Get-FileHash (Join-Path $OutputDirectory 'candidate.ps1') -Algorithm SHA256 | Format-Table -AutoSize
$candidateHash=(Get-FileHash (Join-Path $OutputDirectory 'candidate.ps1') -Algorithm SHA256).Hash
$runner=@'
# Runs a hash-pinned synthetic helper, never the inventory collector.
$ErrorActionPreference='Stop'
$expected=@{candidate='__CANDIDATE_HASH__'}
foreach($variant in @('candidate')){
 $file=Join-Path $PSScriptRoot ($variant+'.ps1')
 if((Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash -ne $expected[$variant]){Write-Host 'STOP: comparison payload checksum mismatch. Nothing was executed.';exit 2}
}
$exe=Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
if([Environment]::Is64BitOperatingSystem -and -not [Environment]::Is64BitProcess){$exe=Join-Path $env:SystemRoot 'Sysnative\WindowsPowerShell\v1.0\powershell.exe'}
$log=Join-Path $PSScriptRoot ('Startup-Comparison-'+(Get-Date -Format 'yyyyMMdd-HHmmss')+'-'+[Guid]::NewGuid().ToString('N').Substring(0,8)+'.txt')
[IO.File]::WriteAllText($log,"Synthetic report-helper comparison only. No inventory commands executed.`r`n",(New-Object Text.UTF8Encoding($true)))
foreach($variant in @('candidate')){
 Write-Host ('Running '+$variant+' helper test (maximum 20 seconds).')
 $info=New-Object Diagnostics.ProcessStartInfo
 $info.FileName=$exe
 $info.Arguments='-NoLogo -NoProfile -NonInteractive -File "'+(Join-Path $PSScriptRoot ($variant+'.ps1'))+'"'
 $info.UseShellExecute=$false;$info.RedirectStandardOutput=$true;$info.RedirectStandardError=$true
 $child=New-Object Diagnostics.Process;$child.StartInfo=$info
 [void]$child.Start()
 $stdout=$child.StandardOutput.ReadToEndAsync();$stderr=$child.StandardError.ReadToEndAsync()
 $finished=$child.WaitForExit(20000)
 if(-not $finished){$child.Kill(); if(-not $child.WaitForExit(2000)){throw "Owned diagnostic child did not exit after termination."}}
 if(-not $stdout.Wait(2000) -or -not $stderr.Wait(2000)){throw "Diagnostic output drain exceeded its deadline."}
 $result="`r`n=== "+$variant+" ===`r`nPayload SHA256: "+$expected[$variant]+"`r`nFinished within timeout: "+$finished+"`r`nChild exit code: "+$child.ExitCode+"`r`n--- Standard output ---`r`n"+$stdout.Result+"--- Error output ---`r`n"+$stderr.Result
 [IO.File]::AppendAllText($log,$result,(New-Object Text.UTF8Encoding($false)))
 $child.Dispose()
}
Write-Host ('Saved '+[IO.Path]::GetFileName($log))
Write-Host 'This is a diagnostic comparison, not an installed application fix.'
'@
$runner=$runner.Replace('__CANDIDATE_HASH__',$candidateHash)
[IO.File]::WriteAllText((Join-Path $OutputDirectory 'Run-Comparison.ps1'),$runner,(New-Object Text.UTF8Encoding($true)))
$launcher="@echo off`r`nsetlocal`r`n`"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe`" -NoLogo -NoProfile -File `"%~dp0Run-Comparison.ps1`"`r`necho.`r`necho If script execution was blocked, stop and report the message. Do not change security settings.`r`npause`r`n"
[IO.File]::WriteAllText((Join-Path $OutputDirectory 'Run-Comparison.cmd'),$launcher,[Text.Encoding]::ASCII)
$readme=@'
SERVER INVENTORY - SYNTHETIC STARTUP COMPARISON

This checks the suspected startup fix; it does not install or update the app.
It includes only seven report-helper functions and made-up values. No server
inventory, role/config/account queries, network calls, exports or admin rights.

1. Extract into a NEW writable folder on the affected Server 2019 computer.
2. Double-click Run-Comparison.cmd normally (not Run as administrator).
3. Wait up to 26 seconds. Only the diagnostic's own child is stopped on timeout.
4. Return Startup-Comparison-*.txt. No automatic upload occurs.

The published negative control uses only a finite collection. The already-known
fatal published cyclic path is deliberately NOT rerun. The candidate runs the
cyclic boundary checks and complete original startup self-tests.
If Windows/PowerShell blocks execution, STOP and report that message. Do not
change execution policy, disable protection, or install another PowerShell.

Logs contain runtime versions, synthetic checkpoint names, child exit codes,
and errors. Errors can include the local diagnostic folder path: redact that
path if desired. No customer inventory or credentials are collected. The only
output is the comparison text file beside the scripts. Delete this extracted
folder when finished.

Published source: collector 3.2.10, SHA256
B072C3CF15F1CEF14DF397718A311A753CFAE0A7B12EBF4380FBED4A2D01C091
The candidate changes only Get-ReportPropertyValue to preserve the property's
object identity at the PowerShell function-output boundary. The supervisor
checks the payload hash before executing it.
'@
[IO.File]::WriteAllText((Join-Path $OutputDirectory 'README.txt'),$readme,(New-Object Text.UTF8Encoding($false)))
