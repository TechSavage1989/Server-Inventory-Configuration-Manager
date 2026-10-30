# Runs only the hash-pinned synthetic probe in a bounded child process.
[CmdletBinding()]
param()
$ErrorActionPreference='Stop'
$expected='BFFFA7189BAB9DC4FB99F1DB3F375E5616F708CF2C6B54D885A5865218F81502'
$payload=Join-Path $PSScriptRoot 'Startup-Probe.ps1'
if((Get-FileHash -LiteralPath $payload -Algorithm SHA256).Hash -ne $expected){
 Write-Host 'STOP: diagnostic payload checksum mismatch. Nothing was executed.'
 exit 2
}
$stamp=(Get-Date -Format 'yyyyMMdd-HHmmss')+'-'+[Guid]::NewGuid().ToString('N').Substring(0,8)
$log=Join-Path $PSScriptRoot ('Startup-Diagnostic-'+$stamp+'.txt')
$exe=Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
if([Environment]::Is64BitOperatingSystem -and -not [Environment]::Is64BitProcess){$exe=Join-Path $env:SystemRoot 'Sysnative\WindowsPowerShell\v1.0\powershell.exe'}
$info=New-Object Diagnostics.ProcessStartInfo
$info.FileName=$exe
$info.Arguments='-NoLogo -NoProfile -NonInteractive -File "'+$payload+'"'
$info.UseShellExecute=$false
$info.RedirectStandardOutput=$true
$info.RedirectStandardError=$true
$child=New-Object Diagnostics.Process
$child.StartInfo=$info
Write-Host 'Running synthetic helper tests only (maximum 60 seconds).'
[void]$child.Start()
$outTask=$child.StandardOutput.ReadToEndAsync()
$errTask=$child.StandardError.ReadToEndAsync()
$completed=$child.WaitForExit(60000)
if(-not $completed){$child.Kill()}
$child.WaitForExit()
$code=$child.ExitCode
$text='Payload SHA256: '+$expected+[Environment]::NewLine
$text+='Finished within timeout: '+$completed+[Environment]::NewLine
$text+='Child exit code: '+$code+[Environment]::NewLine
$text+='--- Standard output ---'+[Environment]::NewLine+$outTask.Result
$text+='--- Error output ---'+[Environment]::NewLine+$errTask.Result
[IO.File]::WriteAllText($log,$text,(New-Object Text.UTF8Encoding($true)))
Write-Host ('Saved '+[IO.Path]::GetFileName($log))
if(-not $completed){Write-Host 'Diagnostic child stopped at the 60-second limit.';exit 3}
if($code -ne 0){Write-Host 'Probe did not complete successfully. Keep the diagnostic text file.';exit 1}
Write-Host 'Synthetic helper checks passed. This is not a full inventory or Server 2019 compatibility certification.'
exit 0
