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

    $exitCode=Invoke-NativeAndWait -FilePath $staged -Arguments @('--update-helper','--parent-pid','0','--target',$target,'--expected-version',$ExpectedVersion,'--expected-sha256',$hash,'--old-version',$ExpectedVersion,'--health-marker',$health,'--health-token',$token,'--rollback',$rollback,'--rollback-meta',$rollbackMeta,'--log',$log)
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

    # A child (browser or WebView2) can exit while taskkill walks the tree; that
    # partial failure is harmless, but its exit code would become the step's.
    & taskkill.exe /PID $marker.pid /T /F | Out-Null
    $global:LASTEXITCODE=0
    $started=Get-Date
    while((Get-Process -Id $marker.pid -ErrorAction SilentlyContinue) -and ((Get-Date)-$started).TotalSeconds -lt 15){Start-Sleep -Milliseconds 250}
    if(Get-Process -Id $marker.pid -ErrorAction SilentlyContinue){throw "The updated application (PID $($marker.pid)) did not close."}

    # Negative control: append a byte so the helper starts but its trusted hash check fails.
    $tampered=Join-Path $temp 'Tampered.exe'
    Copy-Item $ExecutablePath $tampered
    $stream=[IO.File]::Open($tampered,[IO.FileMode]::Append,[IO.FileAccess]::Write,[IO.FileShare]::Read)
    try{$stream.WriteByte(0x42)}finally{$stream.Dispose()}
    $badExitCode=Invoke-NativeAndWait -FilePath $tampered -Arguments @('--update-helper','--parent-pid','0','--target',(Join-Path $temp 'BadTarget.exe'),'--expected-version',$ExpectedVersion,'--expected-sha256',$hash,'--old-version',$ExpectedVersion,'--health-marker',(Join-Path $temp 'bad-health.json'),'--health-token','bad','--rollback',(Join-Path $temp 'bad-prev.exe'),'--rollback-meta',(Join-Path $temp 'bad-prev.json'),'--log',(Join-Path $temp 'bad.log'))
    if($badExitCode -eq 0){throw 'Negative control failed: tampered update was accepted.'}

    # Regression control: the published v3.2.1 helper left a target-volume
    # temporary file when the existing executable could not be read for rollback.
    # Test that real published binary, then the new helper, with the same lock.
    $prior=Join-Path $temp 'Published-3.2.1.exe'
    Invoke-WebRequest -UseBasicParsing -Uri 'https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/download/v3.2.1/Server-Inventory-Configuration-Manager.exe' -OutFile $prior
    $priorHash=(Get-FileHash $prior -Algorithm SHA256).Hash.ToLowerInvariant()
    if($priorHash -ne '6ba0d0d231c313fd87874dbd737753c42255d5635c6a67082109f7a5890d0dc5'){throw 'Published negative-control executable checksum mismatch'}
    foreach($case in @(@{Name='previous-failure';Exe=$prior;Version='3.2.1';Hash=$priorHash;ExpectOrphan=$true},@{Name='fixed';Exe=$staged;Version=$ExpectedVersion;Hash=$hash;ExpectOrphan=$false})){
        $caseDir=Join-Path $temp $case.Name
        New-Item -ItemType Directory $caseDir -Force|Out-Null
        $lockedTarget=Join-Path $caseDir 'LockedTarget.exe'
        Copy-Item $ExecutablePath $lockedTarget
        $originalTargetHash=(Get-FileHash $lockedTarget -Algorithm SHA256).Hash
        $lock=[IO.File]::Open($lockedTarget,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::None)
        try{
            $code=Invoke-NativeAndWait -FilePath $case.Exe -Arguments @('--update-helper','--parent-pid','0','--target',$lockedTarget,'--expected-version',$case.Version,'--expected-sha256',$case.Hash,'--old-version',$ExpectedVersion,'--health-marker',(Join-Path $caseDir 'Health\current.json'),'--health-token','locked-target-test','--rollback',(Join-Path $caseDir 'Rollback\previous.exe'),'--rollback-meta',(Join-Path $caseDir 'Rollback\previous.json'),'--log',(Join-Path $caseDir 'Updates.log'))
        }finally{$lock.Dispose()}
        if($code -ne 10){throw "Expected locked-target refusal 10, got $code ($($case.Name))"}
        $orphans=@(Get-ChildItem -LiteralPath $caseDir -Force|Where-Object{$_.Name -like '*.update-*.tmp.exe'})
        if($case.ExpectOrphan -and $orphans.Count -eq 0){throw 'Negative control did not reproduce the published v3.2.1 cleanup failure'}
        if(-not $case.ExpectOrphan -and $orphans.Count -ne 0){throw 'A failed installation left update clutter beside the executable'}
        if((Get-FileHash $lockedTarget -Algorithm SHA256).Hash -ne $originalTargetHash){throw 'Locked-target refusal modified the installed application'}
        if(-not $case.ExpectOrphan){
            $record=Get-Content -Raw -LiteralPath (Join-Path $caseDir 'Health\last-update.json')|ConvertFrom-Json
            if($record.status -ne 'failed' -or $record.code -ne 10 -or $record.target -ne $case.Version -or $record.message -notmatch 'locked or unreadable'){throw "Locked-target refusal did not record its reason: $($record|ConvertTo-Json -Compress)"}
        }
        Write-Host "Locked-target control passed: $($case.Name), temporary files: $($orphans.Count)"
    }

    # Failure path: the installed executable stays readable but cannot be
    # replaced (endpoint protection or another program holds it open). The
    # helper must retry, then give up with a recorded reason and leave the
    # installed file unchanged, never a silent loop.
    $heldDir=Join-Path $temp 'held'
    New-Item -ItemType Directory $heldDir -Force|Out-Null
    $heldTarget=Join-Path $heldDir 'HeldTarget.exe'
    Copy-Item $ExecutablePath $heldTarget
    $heldHash=(Get-FileHash $heldTarget -Algorithm SHA256).Hash
    $hold=[IO.File]::Open($heldTarget,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
    try{
        $heldCode=Invoke-NativeAndWait -FilePath $staged -Arguments @('--update-helper','--parent-pid','0','--target',$heldTarget,'--expected-version',$ExpectedVersion,'--expected-sha256',$hash,'--old-version','3.2.0','--health-marker',(Join-Path $heldDir 'Health\current.json'),'--health-token','held-target-test','--rollback',(Join-Path $heldDir 'Rollback\previous.exe'),'--rollback-meta',(Join-Path $heldDir 'Rollback\previous.json'),'--log',(Join-Path $heldDir 'Updates.log'))
    }finally{$hold.Dispose()}
    if($heldCode -ne 11){throw "Expected held-target refusal 11, got $heldCode"}
    if((Get-FileHash $heldTarget -Algorithm SHA256).Hash -ne $heldHash){throw 'Held-target refusal modified the installed application'}
    if(@(Get-ChildItem -LiteralPath $heldDir -Force|Where-Object{$_.Name -like '*.update-*.tmp.exe'}).Count -ne 0){throw 'Held-target refusal left update clutter beside the executable'}
    $heldLog=Get-Content -Raw -LiteralPath (Join-Path $heldDir 'Updates.log')
    if($heldLog -notmatch 'after (\d+) tries' -or [int]$Matches[1] -lt 3){throw "Held-target replace was not retried: $heldLog"}
    $heldRecord=Get-Content -Raw -LiteralPath (Join-Path $heldDir 'Health\last-update.json')|ConvertFrom-Json
    if($heldRecord.status -ne 'failed' -or $heldRecord.code -ne 11 -or $heldRecord.target -ne $ExpectedVersion -or $heldRecord.version -ne '3.2.0' -or $heldRecord.message -notmatch 'could not be replaced' -or $heldRecord.path -ne $heldTarget){throw "Held-target refusal did not record its reason: $($heldRecord|ConvertTo-Json -Compress)"}
    Write-Host "Held-target control passed after $($Matches[1]) tries: $($heldRecord.message)"
    Write-Host 'Native GUI update helper test passed with rendered UI, owned visible window and tampered-update negative control.'
}
finally{
    $env:LOCALAPPDATA=$oldLocalAppData
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
}