[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][string]$ExecutablePath,
    [Parameter(Mandatory=$true)][string]$ExpectedVersion
)
$ErrorActionPreference='Stop'
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

    $p=Start-Process -FilePath $staged -ArgumentList @('--update-helper','--parent-pid','0','--target',$target,'--expected-version',$ExpectedVersion,'--expected-sha256',$hash,'--old-version','3.0.10','--health-marker',$health,'--health-token',$token,'--rollback',$rollback,'--rollback-meta',$rollbackMeta,'--log',$log,'--allow-headless-health','true') -PassThru -Wait
    if($p.ExitCode -ne 0){throw "Update helper integration test failed with exit code $($p.ExitCode). Log: $log"}
    if(-not(Test-Path $target)){throw 'Updated target was not present.'}
    if(-not(Test-Path $rollback)){throw 'Rollback executable was not retained.'}
    $marker=Get-Content $health -Raw|ConvertFrom-Json
    if($marker.version -ne $ExpectedVersion -or $marker.health_token -ne $token){throw 'Healthy-start marker did not match the update test.'}

    # Negative control: append a byte so the helper starts but its trusted hash check fails.
    $tampered=Join-Path $temp 'Tampered.exe'
    Copy-Item $ExecutablePath $tampered
    $stream=[IO.File]::Open($tampered,[IO.FileMode]::Append,[IO.FileAccess]::Write,[IO.FileShare]::Read)
    try{$stream.WriteByte(0x42)}finally{$stream.Dispose()}
    $bad=Start-Process -FilePath $tampered -ArgumentList @('--update-helper','--parent-pid','0','--target',(Join-Path $temp 'BadTarget.exe'),'--expected-version',$ExpectedVersion,'--expected-sha256',$hash,'--old-version',$ExpectedVersion,'--health-marker',(Join-Path $temp 'bad-health.json'),'--health-token','bad','--rollback',(Join-Path $temp 'bad-prev.exe'),'--rollback-meta',(Join-Path $temp 'bad-prev.json'),'--log',(Join-Path $temp 'bad.log'),'--allow-headless-health','true') -PassThru -Wait
    if($bad.ExitCode -eq 0){throw 'Negative control failed: tampered update was accepted.'}

    Write-Host 'Update install integration test passed, including tampered-update negative control.'
}
finally{
    $env:LOCALAPPDATA=$oldLocalAppData
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
}