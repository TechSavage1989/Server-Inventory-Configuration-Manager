[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$CollectorPath,

    [Parameter(Mandatory = $true)]
    [string]$OutputRoot,

    [Parameter(Mandatory = $true)]
    [string]$PackageName,

    [Parameter(Mandatory = $true)]
    [string]$RunId,

    [Parameter(Mandatory = $true)]
    [string]$ResultPath,

    [Parameter()]
    [string]$InventoryCategoryList = 'All',

    [Parameter()]
    [string]$ExportCategoryList = '',

    [Parameter()]
    [switch]$IncludeDomainInventory,

    [Parameter()]
    [switch]$IncludeGpoBackup,

    [Parameter()]
    [switch]$IncludeShareAclBackup,

    [Parameter()]
    [switch]$IncludeEventLogs,

    [Parameter()]
    [ValidateRange(1, 365)]
    [int]$EventLogDays = 30,

    [Parameter()]
    [switch]$IncludeSensitive
)

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }
$expectedPackagePath = Join-Path $OutputRoot $PackageName
$expectedZipPath = Join-Path $OutputRoot ($PackageName + '.zip')
$expectedDocxReportPath = Join-Path $expectedPackagePath 'OPEN-FIRST - Server Inventory Report.docx'
$expectedHtmlReportPath = Join-Path $expectedPackagePath 'OPEN-FIRST - Server Inventory Report.html'
$expectedSummaryTextPath = Join-Path $expectedPackagePath 'OPEN-FIRST - Server Inventory Summary.txt'
$powerShellVersionText = ''

function Write-RunResult {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Status,

        [Parameter(Mandatory = $true)]
        [string]$Message
    )

    try {
        $resultParent = Split-Path -Path $ResultPath -Parent
        if ($resultParent -and -not (Test-Path -LiteralPath $resultParent)) {
            New-Item -Path $resultParent -ItemType Directory -Force | Out-Null
        }

        if ($PSVersionTable.PSVersion.Major -ge 3) {
            [pscustomobject]@{
                Status            = $Status
                Message           = $Message
                RunId             = $RunId
                PowerShellVersion = $powerShellVersionText
                OutputRoot        = $OutputRoot
                PackageName       = $PackageName
                PackagePath       = $expectedPackagePath
                ZipPath           = $expectedZipPath
                DocxReportPath    = $expectedDocxReportPath
                HtmlReportPath    = $expectedHtmlReportPath
                SummaryTextPath   = $expectedSummaryTextPath
                CompletedAt       = (Get-Date).ToString('o')
            } | ConvertTo-Json -Depth 4 | Set-Content -Path $ResultPath -Encoding UTF8
        }
        else {
            @(
                'Status=' + $Status
                'Message=' + $Message
                'RunId=' + $RunId
            ) | Set-Content -Path $ResultPath -Encoding UTF8
        }
    }
    catch {
    }
}

try {
    if (-not $PSVersionTable -or -not $PSVersionTable.PSVersion) {
        throw 'Unable to determine the Windows PowerShell version.'
    }

    $powerShellVersionText = $PSVersionTable.PSVersion.ToString()
    Write-Host ('Windows PowerShell version: ' + $powerShellVersionText)
    Write-Host ('Run ID: ' + $RunId)
    Write-Host ('Output root: ' + $OutputRoot)
    Write-Host ('Expected package: ' + $expectedPackagePath)
    Write-Host ''

    if ($PSVersionTable.PSVersion.Major -lt 3) {
        throw ('Windows PowerShell 3.0 or later is required. Detected: ' + $powerShellVersionText)
    }

    if ([Environment]::Is64BitOperatingSystem -and -not [Environment]::Is64BitProcess) {
        throw 'The collector was started in 32-bit Windows PowerShell on a 64-bit server.'
    }

    if (-not (Test-Path -LiteralPath $CollectorPath -PathType Leaf)) {
        throw ('The embedded collector was not found: ' + $CollectorPath)
    }

    if (Test-Path -LiteralPath $expectedPackagePath) {
        throw ('The expected package directory already exists: ' + $expectedPackagePath)
    }

    if (Test-Path -LiteralPath $expectedZipPath) {
        throw ('The expected ZIP file already exists: ' + $expectedZipPath)
    }

    Write-Host 'Validating the embedded collector with the Windows PowerShell parser...'
    $tokens = $null
    $parseErrors = $null
    [System.Management.Automation.Language.Parser]::ParseFile(
        $CollectorPath,
        [ref]$tokens,
        [ref]$parseErrors
    ) | Out-Null

    if ($parseErrors -and $parseErrors.Count -gt 0) {
        $parseDetails = @($parseErrors | ForEach-Object {
            'Line {0}, column {1}: {2}' -f $_.Extent.StartLineNumber, $_.Extent.StartColumnNumber, $_.Message
        }) -join [Environment]::NewLine
        throw ('The embedded collector failed PowerShell syntax validation.' + [Environment]::NewLine + $parseDetails)
    }

    Write-Host 'Collector syntax validation passed.'
    Write-Host 'Starting inventory collection...'
    Write-Host ''

    $parameters = @{
        OutputRoot = $OutputRoot
        PackageName = $PackageName
        RunId = $RunId
        CreateZip = $true
        InventoryCategoryList = $InventoryCategoryList
        ExportCategoryList = $ExportCategoryList
        EventLogDays = $EventLogDays
    }

    if ($IncludeDomainInventory) {
        $parameters.IncludeDomainInventory = $true
    }
    if ($IncludeGpoBackup) {
        $parameters.IncludeGpoBackup = $true
    }
    if ($IncludeShareAclBackup) {
        $parameters.IncludeShareAclBackup = $true
    }
    if ($IncludeEventLogs) {
        $parameters.IncludeEventLogs = $true
        $parameters.EventLogDays = $EventLogDays
    }
    if ($IncludeSensitive) {
        $parameters.IncludeSensitiveRoleExports = $true
    }

    & $CollectorPath @parameters
    if (-not $?) {
        throw 'PowerShell reported that the inventory collector invocation failed.'
    }

    Write-Host ''
    Write-Host 'Collector returned. Validating required output...'

    $requiredFiles = @(
        (Join-Path $expectedPackagePath '00-Summary\READ-FIRST-Server-Summary.txt'),
        (Join-Path $expectedPackagePath '00-Summary\Collector-Results.csv'),
        (Join-Path $expectedPackagePath '00-Summary\COLLECTION-COMPLETED.json'),
        (Join-Path $expectedPackagePath 'Logs\Collection.log'),
        (Join-Path $expectedPackagePath 'SHA256-Manifest.csv'),
        $expectedDocxReportPath,
        $expectedHtmlReportPath,
        $expectedSummaryTextPath,
        $expectedZipPath,
        ($expectedZipPath + '.sha256.txt')
    )

    $missingFiles = @($requiredFiles | Where-Object {
        -not (Test-Path -LiteralPath $_ -PathType Leaf)
    })
    if ($missingFiles.Count -gt 0) {
        throw ('Required completion files were not created:' + [Environment]::NewLine + ($missingFiles -join [Environment]::NewLine))
    }

    $emptyFiles = @($requiredFiles | Where-Object {
        (Get-Item -LiteralPath $_).Length -le 0
    })
    if ($emptyFiles.Count -gt 0) {
        throw ('Required completion files were empty:' + [Environment]::NewLine + ($emptyFiles -join [Environment]::NewLine))
    }

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $docxArchive = $null
    try {
        $docxArchive = [System.IO.Compression.ZipFile]::OpenRead($expectedDocxReportPath)

        # Normalize ZIP entry names because older .NET Framework versions can expose
        # archive separators as backslashes even when the package is otherwise readable.
        $docxEntriesByName = @{}
        foreach ($entry in @($docxArchive.Entries)) {
            $normalizedName = ([string]$entry.FullName).Replace('\', '/').TrimStart([char[]]@('/'))
            if (-not $docxEntriesByName.ContainsKey($normalizedName)) {
                $docxEntriesByName[$normalizedName] = $entry
            }
        }
        $docxEntryNames = @($docxEntriesByName.Keys)
        $requiredDocxParts = @(
            '[Content_Types].xml',
            'word/document.xml',
            'word/styles.xml',
            'word/settings.xml',
            'word/_rels/document.xml.rels'
        )
        $missingDocxParts = @($requiredDocxParts | Where-Object { $docxEntryNames -notcontains $_ })
        if ($missingDocxParts.Count -gt 0) {
            throw ('The Word report did not contain the required Open XML document parts:' + [Environment]::NewLine + ($missingDocxParts -join [Environment]::NewLine))
        }

        $documentEntry = $docxEntriesByName['word/document.xml']
        $documentReader = $null
        try {
            $documentReader = New-Object System.IO.StreamReader($documentEntry.Open())
            $documentXml = $documentReader.ReadToEnd()
        }
        finally {
            if ($documentReader) {
                $documentReader.Dispose()
            }
        }
        if ($documentXml -notmatch 'Server Inventory and Configuration Report') {
            throw 'The Word report document body did not contain the expected report title.'
        }

        $relationshipsEntry = $docxEntriesByName['word/_rels/document.xml.rels']
        $relationshipsReader = $null
        try {
            $relationshipsReader = New-Object System.IO.StreamReader($relationshipsEntry.Open())
            $relationshipsXml = $relationshipsReader.ReadToEnd()
        }
        finally {
            if ($relationshipsReader) {
                $relationshipsReader.Dispose()
            }
        }
        if (($relationshipsXml -notmatch '/relationships/styles') -or ($relationshipsXml -notmatch '/relationships/settings')) {
            throw 'The Word report was missing required document relationships for styles or settings.'
        }
    }
    finally {
        if ($docxArchive) {
            $docxArchive.Dispose()
        }
    }

    $htmlHeader = Get-Content -LiteralPath $expectedHtmlReportPath -TotalCount 5 | Out-String
    if ($htmlHeader -notmatch '(?i)<!DOCTYPE html>|<html') {
        throw 'The HTML consolidated report did not contain a valid HTML header.'
    }

    $summaryHeader = Get-Content -LiteralPath $expectedSummaryTextPath -TotalCount 3 | Out-String
    if ($summaryHeader -notmatch 'SERVER INVENTORY SUMMARY') {
        throw 'The consolidated text summary did not contain the expected heading.'
    }

    $completionMarkerPath = Join-Path $expectedPackagePath '00-Summary\COLLECTION-COMPLETED.json'
    $completionMarker = Get-Content -LiteralPath $completionMarkerPath -Raw | ConvertFrom-Json
    if ([string]$completionMarker.Status -ne 'Completed') {
        throw ('The completion marker had an unexpected status: ' + [string]$completionMarker.Status)
    }
    if ([string]$completionMarker.RunId -ne $RunId) {
        throw ('The completion marker Run ID did not match this execution. Expected ' + $RunId + ', found ' + [string]$completionMarker.RunId)
    }
    if ([string]$completionMarker.DocxReportPath -ne $expectedDocxReportPath) {
        throw 'The completion marker Word report path did not match the validated report.'
    }
    if ([string]$completionMarker.HtmlReportPath -ne $expectedHtmlReportPath) {
        throw 'The completion marker HTML report path did not match the validated report.'
    }
    if ([string]$completionMarker.SummaryTextPath -ne $expectedSummaryTextPath) {
        throw 'The completion marker text summary path did not match the validated summary.'
    }

    [pscustomobject]@{
        Status            = 'ValidatedSuccess'
        RunId             = $RunId
        ValidatedAt       = Get-Date
        PowerShellVersion = $powerShellVersionText
        PackagePath       = $expectedPackagePath
        ZipPath           = $expectedZipPath
        DocxReportPath    = $expectedDocxReportPath
        HtmlReportPath    = $expectedHtmlReportPath
        SummaryTextPath   = $expectedSummaryTextPath
    } | Format-List * | Out-String -Width 4096 |
        Set-Content -Path (Join-Path $expectedPackagePath '00-Summary\EXECUTABLE-VALIDATION-SUCCEEDED.txt') -Encoding UTF8

    Write-RunResult -Status 'Success' -Message 'The collector completed and the consolidated reports and supporting output were validated.'
    Write-Host 'The consolidated Word report, HTML report, text summary, and supporting output were found and validated.'
    exit 0
}
catch {
    $errorMessage = $_.Exception.Message
    Write-RunResult -Status 'Failed' -Message $errorMessage

    Write-Host ''
    Write-Host 'SERVER INVENTORY COLLECTION FAILED' -ForegroundColor Red
    Write-Host $errorMessage -ForegroundColor Red
    if ($_.InvocationInfo -and $_.InvocationInfo.PositionMessage) {
        Write-Host $_.InvocationInfo.PositionMessage -ForegroundColor DarkYellow
    }
    if ($_.ScriptStackTrace) {
        Write-Host ('PowerShell stack trace: ' + $_.ScriptStackTrace) -ForegroundColor DarkGray
    }
    exit 1
}
