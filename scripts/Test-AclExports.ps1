# Synthetic-only bounded process and ACL export contract tests. Never invoke inventory or ICACLS.
[CmdletBinding()]
param([string]$OutputDirectory=(Join-Path $PSScriptRoot '..\build\acl-validation'))
$ErrorActionPreference='Stop'
$OutputDirectory=[IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$tokens=$null;$errors=$null
$source=Join-Path $PSScriptRoot '../src/app/Export-ServerInventory.ps1'
$ast=[Management.Automation.Language.Parser]::ParseFile($source,[ref]$tokens,[ref]$errors)
if($errors.Count){throw ('Collector parse errors: '+($errors -join '; '))}
$names=@('Initialize-AclExportProcessType','Test-AclExportSkipRequested','Write-AclExportStatus','Invoke-BoundedAclProcess','Invoke-RecursiveAclExports','Get-SafeFileName')
foreach($name in $names){
 $definition=@($ast.EndBlock.Statements|Where-Object{$_ -is [Management.Automation.Language.FunctionDefinitionAst] -and $_.Name -eq $name})
 if($definition.Count -ne 1){throw "Missing helper $name"}
 . ([scriptblock]::Create($definition[0].Extent.Text))
}
function Write-InventoryLog { param([string]$Message,[string]$Level='INFO'); Write-Host ('TEST '+$Level+': '+$Message) }
$RunId='synthetic-acl-test'
$AclControlPath=Join-Path $OutputDirectory 'control.json'
$AclExportTimeoutSeconds=30
$script:aclSkipRemaining=$false
$script:aclExportUnsafePending=$false
$checks=New-Object Collections.ArrayList
$engine=Join-Path $PSHOME 'powershell.exe'
if(-not (Test-Path $engine)){$engine=Join-Path $PSHOME 'pwsh'}
function Assert([bool]$Condition,[string]$Description){if(-not $Condition){throw $Description};[void]$checks.Add($Description);Write-Host ('PASS '+$Description)}
function Test-Native([string]$Name,[string]$Code,[int]$Timeout=10){
 $path=Join-Path $OutputDirectory ($Name+'.ps1');[IO.File]::WriteAllText($path,$Code)
 Invoke-BoundedAclProcess -Command $engine -Arguments @('-NoLogo','-NoProfile','-NonInteractive','-File',$path) -OutputFile (Join-Path $OutputDirectory ($Name+'.out')) -TimeoutSeconds $Timeout -ShareName 'SYNTHETIC'
}
# Process exit and both stream drains are checked explicitly, never inferred from ambient LASTEXITCODE.
$r=Test-Native 'success' "[Console]::Out.WriteLine('synthetic-output');[Console]::Error.WriteLine('synthetic-error');exit 0"
Assert ($r.Status -eq 'Completed' -and $r.ExitCode -eq 0) 'Normal native child returns explicit success'
Assert (([IO.File]::ReadAllText((Join-Path $OutputDirectory 'success.out'))) -match 'synthetic-output') 'stdout retained'
Assert (([IO.File]::ReadAllText((Join-Path $OutputDirectory 'success.out.stderr.txt'))) -match 'synthetic-error') 'stderr retained separately'
$r=Test-Native 'nonzero' 'exit 7'
Assert ($r.Status -eq 'Failed' -and $r.ExitCode -eq 7) 'Nonzero native exit is incomplete, not success'
$r=Test-Native 'flood' '$s="x"*200;for($i=0;$i -lt 6000;$i++){[Console]::Out.WriteLine($s);[Console]::Error.WriteLine($s)}' 20
Assert ($r.Status -eq 'Completed') 'Simultaneous stdout/stderr flood does not deadlock'
Assert (((Get-Item (Join-Path $OutputDirectory 'flood.out')).Length -gt 1000000) -and ((Get-Item (Join-Path $OutputDirectory 'flood.out.stderr.txt')).Length -gt 1000000)) 'Large dual-stream output is retained without a whole-output buffer'
# An unrelated process remains alive after timeout; all stopping uses the created Process handle.
$otherInfo=New-Object Diagnostics.ProcessStartInfo
$otherInfo.FileName=$engine;$otherInfo.Arguments='-NoLogo -NoProfile -NonInteractive -Command "Start-Sleep -Seconds 60"';$otherInfo.UseShellExecute=$false
$other=[Diagnostics.Process]::Start($otherInfo)
try{
 $watch=[Diagnostics.Stopwatch]::StartNew()
 $r=Test-Native 'silent' 'Start-Sleep -Seconds 60' 1
 Assert ($r.Status -eq 'TimedOut' -and $watch.Elapsed.TotalSeconds -lt 12) 'Silent child times out within bounded grace'
 Assert (-not $other.HasExited) 'Timeout preserves unrelated process'
}finally{if(-not $other.HasExited){$other.Kill();[void]$other.WaitForExit(2000)};$other.Dispose()}
# Skip signal is run-scoped. A stale run cannot stop this operation.
[IO.File]::WriteAllText($AclControlPath,'{"RunId":"another-run","SkipRemaining":true}')
Assert (-not (Test-AclExportSkipRequested)) 'Stale-run skip is ignored'
[IO.File]::WriteAllText($AclControlPath,'{"RunId":"synthetic-acl-test","SkipRemaining":true}')
$r=Test-Native 'skip-before' 'throw "Must not execute"'
Assert ($r.Status -eq 'Skipped' -and -not (Test-Path (Join-Path $OutputDirectory 'skip-before.out'))) 'Skip before launch creates no native process/output'
$script:aclSkipRemaining=$false
Remove-Item $AclControlPath
# A synthetic background helper supplies a skip during a silent owned child.
$signal=Join-Path $OutputDirectory 'signal.ps1'
$escaped=$AclControlPath.Replace("'","''")
[IO.File]::WriteAllText($signal,"Start-Sleep -Milliseconds 1000;[IO.File]::WriteAllText('$escaped','{`"RunId`":`"synthetic-acl-test`",`"SkipRemaining`":true}')")
$signalInfo=New-Object Diagnostics.ProcessStartInfo
$signalInfo.FileName=$engine;$signalInfo.Arguments='-NoLogo -NoProfile -NonInteractive -File "'+$signal+'"';$signalInfo.UseShellExecute=$false
$signaler=[Diagnostics.Process]::Start($signalInfo)
try{
 $watch=[Diagnostics.Stopwatch]::StartNew();$r=Test-Native 'skip-running' 'Start-Sleep -Seconds 60' 20
 Assert ($r.Status -eq 'Skipped' -and $watch.Elapsed.TotalSeconds -lt 12) 'Skip interrupts a running silent child promptly'
}finally{if(-not $signaler.HasExited){$signaler.Kill();[void]$signaler.WaitForExit(2000)};$signaler.Dispose()}
# Test artifact truth with deterministic native substitutes; this never calls ICACLS or real share paths.
$realBounded=${function:Invoke-BoundedAclProcess}
if ([string]::IsNullOrEmpty($env:SystemRoot)) { $env:SystemRoot=$OutputDirectory } # fixture only; never invokes ICACLS
function Invoke-BoundedAclProcess {
 param($Command,$Arguments,$OutputFile,$TimeoutSeconds,$ShareIndex,$ShareCount,$ShareName)
 if ($ShareName -ne 'Missing') { [IO.File]::WriteAllText($Arguments[2],$(if($ShareName -eq 'Empty'){''}else{'synthetic ACL fixture'})) }
 $status=if($ShareName -eq 'Partial'){'TimedOut'}else{'Completed'}
 [pscustomobject]@{Status=$status;ExitCode=$(if($status -eq 'Completed'){0}else{-1});ElapsedSeconds=1;Reason='synthetic fixture'}
}
$script:aclSkipRemaining=$false;Remove-Item $AclControlPath -ErrorAction SilentlyContinue
$destination=Join-Path $OutputDirectory 'mapping'
Invoke-RecursiveAclExports -Shares @([pscustomobject]@{Name='Complete';Path='X:\synthetic-only';Special=$false},[pscustomobject]@{Name='Partial';Path='Y:\synthetic-only';Special=$false}) -Destination $destination
$map=@(Import-Csv (Join-Path $destination 'ICACLS-Mapping.csv'))
Assert ($map.Count -eq 2 -and $map[0].Status -eq 'Completed' -and $map[0].AclFile.EndsWith('.acl')) 'Successful ACL file is explicitly promoted and mapped complete'
Assert ($map[1].Status -eq 'TimedOut' -and $map[1].AclFile.EndsWith('.incomplete') -and $script:aclExportsIncompleteCount -eq 1) 'Timed-out ACL remains marked incomplete and counted'
Assert (-not (Test-Path (Join-Path $destination '0002-Partial.acl'))) 'No complete-looking ACL file is produced for a partial export'
# Zero exit alone never proves a complete export. Test missing, empty, and promotion failures.
function Move-Item {
 param([string]$LiteralPath,[string]$Destination,$ErrorAction)
 if($LiteralPath -match 'PromotionFail'){throw 'Synthetic promotion failure'}
 Microsoft.PowerShell.Management\Move-Item @PSBoundParameters
}
$invalid=Join-Path $OutputDirectory 'invalid-artifacts'
Invoke-RecursiveAclExports -Shares @([pscustomobject]@{Name='Missing';Path='X:\synthetic-only';Special=$false},[pscustomobject]@{Name='Empty';Path='X:\synthetic-only';Special=$false},[pscustomobject]@{Name='PromotionFail';Path='X:\synthetic-only';Special=$false}) -Destination $invalid
$invalidMap=@(Import-Csv (Join-Path $invalid 'ICACLS-Mapping.csv'))
Assert (@($invalidMap|Where-Object{$_.Status -eq 'Failed'}).Count -eq 3) 'Missing, empty, and unpromotable ACL files are all failed despite native exit zero'
Assert (@($invalidMap|Where-Object{-not $_.AclFile.EndsWith('.incomplete')}).Count -eq 0) 'Invalid exports retain incomplete filenames'
Assert ($script:aclExportsIncompleteCount -eq 3) 'All invalid artifacts contribute to report incomplete count'
Remove-Item Function:\Move-Item
${function:Invoke-BoundedAclProcess}=$realBounded
$script:aclExportUnsafePending=$true
$guard=@($ast.EndBlock.Statements|Where-Object{$_ -is [Management.Automation.Language.IfStatementAst] -and $_.Extent.Text.StartsWith('if ($script:aclExportUnsafePending)')})
$guardFailed=$false
try{. ([scriptblock]::Create($guard[0].Extent.Text))}catch{$guardFailed=$true}
Assert $guardFailed 'Unconfirmed child/output termination blocks later report/manifest/package generation'
[pscustomobject]@{Checks=@($checks.ToArray());Count=$checks.Count;PowerShell=$PSVersionTable.PSVersion.ToString();SyntheticOnly=$true} | ConvertTo-Json -Depth 4 | Set-Content (Join-Path $OutputDirectory 'acl-validation.json') -Encoding UTF8
Write-Host ('PASS all '+$checks.Count+' synthetic ACL checks')
