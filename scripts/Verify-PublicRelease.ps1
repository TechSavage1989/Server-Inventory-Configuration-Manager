[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)]
    [string]$ExpectedVersion,

    [int]$Attempts = 12,
    [int]$DelaySeconds = 10
)

$ErrorActionPreference = 'Stop'

try {
    [Net.ServicePointManager]::SecurityProtocol =
        [Net.SecurityProtocolType]::Tls12
}
catch { }

$feed = 'https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest/download/update-manifest.json'
$temp = Join-Path $env:TEMP (
    'sicm-release-' + [guid]::NewGuid().ToString('N')
)

New-Item -Path $temp -ItemType Directory -Force | Out-Null

try {
    $manifest = $null

    for ($attempt=1; $attempt -le $Attempts; $attempt++) {
        try {
            $manifestPath = Join-Path $temp 'update-manifest.json'

            Invoke-WebRequest -Uri $feed -OutFile $manifestPath -UseBasicParsing -TimeoutSec 20

            $manifest =
                Get-Content -LiteralPath $manifestPath -Raw |
                ConvertFrom-Json

            if ([string]$manifest.version -eq $ExpectedVersion) {
                break
            }

            $manifest = $null
        }
        catch {
            if ($attempt -eq $Attempts) {
                throw
            }
        }

        Start-Sleep -Seconds $DelaySeconds
    }

    if ($null -eq $manifest) {
        throw "Latest public release did not become version $ExpectedVersion."
    }

    if ([int]$manifest.schema_version -ne 1) {
        throw 'Unexpected manifest schema.'
    }

    if (
        [string]$manifest.app_id -ne
        'Server-Inventory-Configuration-Manager'
    ) {
        throw 'Wrong manifest application ID.'
    }

    if (
        [string]$manifest.asset.name -ne
        'Server-Inventory-Configuration-Manager.ps1'
    ) {
        throw 'Wrong release asset name.'
    }

    $assetPath =
        Join-Path $temp 'Server-Inventory-Configuration-Manager.ps1'

    Invoke-WebRequest -Uri ([string]$manifest.asset.url) -OutFile $assetPath -UseBasicParsing -TimeoutSec 30

    if (
        (Get-Item -LiteralPath $assetPath).Length -ne
        [int64]$manifest.asset.size
    ) {
        throw 'Downloaded release size does not match the manifest.'
    }

    $sha = [Security.Cryptography.SHA256]::Create()
    $stream = $null

    try {
        $stream = [IO.File]::OpenRead($assetPath)
        $hash = (
            (
                $sha.ComputeHash($stream) |
                ForEach-Object {
                    $_.ToString('x2')
                }
            ) -join ''
        )
    }
    finally {
        if ($stream) { $stream.Dispose() }
        $sha.Dispose()
    }

    if (
        $hash -ne
        ([string]$manifest.asset.sha256).ToLowerInvariant()
    ) {
        throw 'Downloaded release SHA-256 does not match the manifest.'
    }

    $content = [IO.File]::ReadAllText($assetPath)

    if (
        $content -notmatch
        '\$script:ApplicationId\s*=\s*["'']Server-Inventory-Configuration-Manager["'']'
    ) {
        throw 'Downloaded release has the wrong application identity.'
    }

    if (
        $content -notmatch (
            '\$script:AppVersion\s*=\s*["'']' +
            [regex]::Escape($ExpectedVersion) +
            '["'']'
        )
    ) {
        throw 'Downloaded release has the wrong application version.'
    }

    Write-Host 'Public release verification passed.'
    Write-Host "Version: $ExpectedVersion"
    Write-Host "SHA-256: $hash"
}
finally {
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
}
