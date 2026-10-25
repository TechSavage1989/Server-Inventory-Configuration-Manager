[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][string]$ExecutablePath,
    [Parameter(Mandatory=$true)][string]$ExpectedVersion
)
$ErrorActionPreference='Stop'

function Invoke-NativeAndWait {
    param(
        [Parameter(Mandatory=$true)][string]$FilePath,
        [Parameter(Mandatory=$true)][string[]]$Arguments
    )

    # Start-Process -ArgumentList flattens arrays into one command line and does
    # not reliably preserve arguments containing spaces. Build one explicitly
    # quoted command line so the test exercises the same argv values as Go's
    # exec.Command used by the production updater.
    $quoted=@(
        $Arguments | ForEach-Object {
            '"' + ([string]$_).Replace('"','\"') + '"'
        }
    )

    $info=New-Object System.Diagnostics.ProcessStartInfo
    $info.FileName=$FilePath
    $info.Arguments=($quoted -join ' ')
    $info.UseShellExecute=$false
    $info.CreateNoWindow=$true

    $process=[System.Diagnostics.Process]::Start($info)
    if($null-eq$process){throw "Could not start native process: $FilePath"}
    $process.WaitForExit()

    return $process.ExitCode
}
$ExecutablePath=[IO.Path]::GetFullPath($ExecutablePath)
$temp=Join-Path $env:TEMP ('SICM-UpdateTest-'+[guid]::NewGuid().ToString('N'))
New-Item -Path $temp -ItemType Directory -Force|Out-Null
$oldLocalAppData=$env:LOCALAPPDATA
try{
    $env:LOCALAPPDATA=Join-Path $temp 'LocalAppData'
    New-Item -Path $env:LOCALAPPDATA -ItemType Directory -Force|Out-Null
    $target=Join-Path $temp 'Target.exe'
    $staged=Join-Path $temp 'Ready.exe'
    Copy-Item $ExecutablePath $target
    Copy-Item $ExecutablePath $staged
    $hash=(Get-FileHash $staged -Algorithm SHA256).Hash.ToLowerInvariant()
    $health=Join-Path $env:LOCALAPPDATA 'Server Inventory Configuration Manager\Health\current.json'
    $rollback=Join-Path $env:LOCALAPPDATA 'Server Inventory Configuration Manager\Rollback\previous.exe'
    $rollbackMeta=Join-Path $env:LOCALAPPDATA 'Server Inventory Configuration Manager\Rollback\previous.json'
    $log=Join-Path $env:LOCALAPPDATA 'Server Inventory Configuration Manager\Logs\Updates.log'
    $token=[guid]::NewGuid().ToString('N')

    $exitCode=Invoke-NativeAndWait -FilePath $staged -Arguments @('--update-helper','--parent-pid','0','--target',$target,'--expected-version',$ExpectedVersion,'--expected-sha256',$hash,'--old-version','3.0.10','--health-marker',$health,'--health-token',$token,'--rollback',$rollback,'--rollback-meta',$rollbackMeta,'--log',$log)
    if($exitCode -ne 0){
        Write-Host "Update helper exit code: $($exitCode)"
        if(Test-Path -LiteralPath $log){
            Write-Host '----- Updates.log -----'
            Get-Content -LiteralPath $log | ForEach-Object { Write-Host $_ }
            Write-Host '----- End Updates.log -----'
        }else{
            Write-Host "Update log was not created: $log"
        }
        if(Test-Path -LiteralPath $health){
            Write-Host '----- Health marker -----'
            Get-Content -LiteralPath $health | ForEach-Object { Write-Host $_ }
            Write-Host '----- End health marker -----'
        }else{
            Write-Host "Health marker was not created: $health"
        }
        throw "Update helper integration test failed with exit code $($exitCode)."
    }
    if(-not(Test-Path $target)){throw 'Updated target was not present.'}
    if(-not(Test-Path $rollback)){throw 'Rollback executable was not retained.'}
    $marker=Get-Content $health -Raw|ConvertFrom-Json
    if($marker.version -ne $ExpectedVersion -or $marker.health_token -ne $token -or -not $marker.visible_window_confirmed -or -not $marker.ui_ready){throw 'Healthy-start marker did not match the update test.'}

    & taskkill.exe /PID $marker.pid /T /F | Out-Null

    # Negative control: append a byte so the helper starts but its trusted hash check fails.
    $tampered=Join-Path $temp 'Tampered.exe'
    Copy-Item $ExecutablePath $tampered
    $stream=[IO.File]::Open($tampered,[IO.FileMode]::Append,[IO.FileAccess]::Write,[IO.FileShare]::Read)
    try{$stream.WriteByte(0x42)}finally{$stream.Dispose()}
    $badExitCode=Invoke-NativeAndWait -FilePath $tampered -Arguments @('--update-helper','--parent-pid','0','--target',(Join-Path $temp 'BadTarget.exe'),'--expected-version',$ExpectedVersion,'--expected-sha256',$hash,'--old-version',$ExpectedVersion,'--health-marker',(Join-Path $temp 'bad-health.json'),'--health-token','bad','--rollback',(Join-Path $temp 'bad-prev.exe'),'--rollback-meta',(Join-Path $temp 'bad-prev.json'),'--log',(Join-Path $temp 'bad.log'))
    if($badExitCode -eq 0){throw 'Negative control failed: tampered update was accepted.'}

    Write-Host 'Native GUI update helper test passed with rendered UI, owned visible window and tampered-update negative control.'
}
finally{
    $env:LOCALAPPDATA=$oldLocalAppData
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
}