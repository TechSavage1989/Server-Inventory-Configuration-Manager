# Synthetic startup helper diagnostic, derived from published collector 3.2.10.
# Source SHA256: B072C3CF15F1CEF14DF397718A311A753CFAE0A7B12EBF4380FBED4A2D01C091
# Only seven report helper definitions and synthetic self-tests are included.
# No inventory, remote access, exports, registry changes, installation or admin requirement.
$ErrorActionPreference='Stop'
Write-Host 'Diagnostic format: ServerInventory-StartupProbe-1'
Write-Host ('PowerShell: '+$PSVersionTable.PSVersion.ToString())
Write-Host ('OS version: '+[Environment]::OSVersion.VersionString)
Write-Host ('64-bit process: '+[Environment]::Is64BitProcess)
$clr=[Diagnostics.Process]::GetCurrentProcess().Modules | Where-Object {$_.ModuleName -eq 'clr.dll'} | Select-Object -First 1
if($clr){Write-Host ('CLR file version: '+$clr.FileVersionInfo.FileVersion)}
function ConvertTo-ReportAtomicText {
    param(
        [Parameter()]
        [AllowNull()]
        [object]$Value
    )

    if ($null -eq $Value) {
        return ''
    }

    if ($Value -is [string]) {
        return [string]$Value
    }
    if ($Value -is [DateTime]) {
        return $Value.ToString('yyyy-MM-dd HH:mm:ss')
    }
    if ($Value -is [DateTimeOffset]) {
        return $Value.ToString('yyyy-MM-dd HH:mm:ss zzz')
    }
    if ($Value -is [TimeSpan]) {
        return $Value.ToString()
    }
    if ($Value -is [Guid]) {
        return $Value.ToString()
    }
    if ($Value -is [Uri]) {
        return $Value.ToString()
    }
    if ($Value -is [Version]) {
        return $Value.ToString()
    }
    if ($Value -is [System.Security.Principal.SecurityIdentifier]) {
        return $Value.Value
    }
    if ($Value -is [System.Net.IPAddress]) {
        return $Value.ToString()
    }
    if ($Value -is [System.Net.NetworkInformation.PhysicalAddress]) {
        return $Value.ToString()
    }
    if ($Value -is [System.IO.FileSystemInfo]) {
        return $Value.FullName
    }
    if ($Value -is [System.Security.Cryptography.Oid]) {
        if (-not [string]::IsNullOrWhiteSpace([string]$Value.FriendlyName)) {
            return ([string]$Value.FriendlyName + ' (' + [string]$Value.Value + ')')
        }
        return [string]$Value.Value
    }
    if ($Value -is [System.Security.SecureString]) {
        return '[secure value omitted]'
    }
    if ($Value -is [System.Collections.DictionaryEntry]) {
        $entryKey = ''
        $entryValue = ''
        try { $entryKey = [string]$Value.Key } catch { $entryKey = '[unavailable key]' }
        try { $entryValue = [string]$Value.Value } catch { $entryValue = '[unavailable value]' }
        return ($entryKey + '=' + $entryValue)
    }

    $valueType = $Value.GetType()
    if ($valueType.IsPrimitive -or $valueType.IsEnum -or ($Value -is [decimal])) {
        try {
            return [System.Convert]::ToString($Value, [System.Globalization.CultureInfo]::InvariantCulture)
        }
        catch {
            return [string]$Value
        }
    }

    # Complex values are deliberately converted without recursively walking their
    # members. Some CIM and deserialized objects contain circular references, which
    # can exceed the call-depth limit in Windows PowerShell 3.0 and 4.0.
    foreach ($preferredPropertyName in @('DisplayName', 'FriendlyName', 'Name', 'Value', 'Path', 'Id', 'IPAddress', 'Address', 'DistinguishedName')) {
        try {
            $preferredProperty = $Value.PSObject.Properties[$preferredPropertyName]
            if ($preferredProperty) {
                $preferredValue = $preferredProperty.Value
                if (($null -ne $preferredValue) -and (-not [object]::ReferenceEquals($preferredValue, $Value)) -and (-not (($preferredValue -is [System.Collections.IEnumerable]) -and -not ($preferredValue -is [string])))) {
                    $preferredText = [string]$preferredValue
                    if (-not [string]::IsNullOrWhiteSpace($preferredText)) {
                        return $preferredText
                    }
                }
            }
        }
        catch { }
    }

    # Do not call an unknown object's custom ToString implementation here. A
    # vendor type can traverse parent or child references internally and recreate
    # the same stack-overflow condition this function is intended to prevent.
    try {
        return ('[' + $valueType.FullName + '; full value retained in supporting export]')
    }
    catch {
        return '[complex value retained in supporting export]'
    }
}

function ConvertTo-ReportText {
    param(
        [Parameter()]
        [AllowNull()]
        [object]$Value,

        [Parameter()]
        [int]$MaxLength = 2000
    )

    if ($null -eq $Value) {
        return ''
    }

    $text = ''
    if (($Value -is [System.Collections.IEnumerable]) -and -not ($Value -is [string])) {
        $parts = New-Object System.Collections.ArrayList
        $itemCount = 0
        $maximumCollectionItems = 128
        $estimatedLength = 0

        try {
            foreach ($item in $Value) {
                if ($itemCount -ge $maximumCollectionItems) {
                    [void]$parts.Add('[additional values omitted from consolidated report]')
                    break
                }

                $itemText = ''
                if ($null -ne $item) {
                    if ([object]::ReferenceEquals($item, $Value)) {
                        $itemText = '[self-reference omitted]'
                    }
                    elseif (($item -is [System.Collections.IEnumerable]) -and -not ($item -is [string])) {
                        # Do not recursively enumerate nested collections. Recursive
                        # collection conversion caused the report-generation failure
                        # on Windows PowerShell 4.0.
                        $itemTypeName = 'collection'
                        try { $itemTypeName = $item.GetType().FullName } catch { }
                        $itemText = '[' + $itemTypeName + '; nested values retained in supporting export]'
                    }
                    else {
                        $itemText = ConvertTo-ReportAtomicText -Value $item
                    }
                }

                if (-not [string]::IsNullOrWhiteSpace($itemText)) {
                    [void]$parts.Add($itemText)
                    $estimatedLength += $itemText.Length + 2
                }

                $itemCount++
                if (($MaxLength -gt 0) -and ($estimatedLength -gt ($MaxLength + 500))) {
                    [void]$parts.Add('[remaining values omitted from consolidated report]')
                    break
                }
            }
        }
        catch {
            [void]$parts.Add('[collection could not be fully rendered: ' + $_.Exception.Message + ']')
        }

        $text = $parts -join '; '
    }
    else {
        $text = ConvertTo-ReportAtomicText -Value $Value
    }

    if ($null -eq $text) {
        $text = ''
    }
    else {
        $text = [string]$text
    }

    $text = $text -replace '[\x00-\x08\x0B\x0C\x0E-\x1F]', ''
    $text = $text -replace '\s*[\r\n]+\s*', ' | '
    $text = $text.Trim()

    if (($MaxLength -gt 0) -and ($text.Length -gt $MaxLength)) {
        $suffix = ' [truncated in consolidated report; full value is retained in supporting exports]'
        $usableLength = [Math]::Max(1, $MaxLength - $suffix.Length)
        $text = $text.Substring(0, $usableLength) + $suffix
    }

    return $text
}

function Get-ReportPropertyValue {
    param(
        [Parameter()]
        [AllowNull()]
        [object]$InputObject,

        [Parameter(Mandatory = $true)]
        [string]$PropertyName
    )

    if ($null -eq $InputObject) {
        return $null
    }

    $property = $InputObject.PSObject.Properties[$PropertyName]
    if ($property) {
        return $property.Value
    }

    return $null
}

function Get-ReportCellText {
    param(
        [Parameter()]
        [AllowNull()]
        [object]$InputObject,

        [Parameter(Mandatory = $true)]
        [string]$PropertyName,

        [Parameter()]
        [int]$MaxLength = 2500
    )

    try {
        $cellValue = Get-ReportPropertyValue -InputObject $InputObject -PropertyName $PropertyName
        return ConvertTo-ReportText -Value $cellValue -MaxLength $MaxLength
    }
    catch {
        $message = $_.Exception.Message
        if ([string]::IsNullOrWhiteSpace($message)) {
            $message = 'unknown conversion error'
        }
        return ('[value could not be rendered in consolidated report: ' + $message + ']')
    }
}

function Add-ReportBullets {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [string[]]$Items
    )

    [void]$script:reportBlocks.Add([pscustomobject]@{
        Type  = 'Bullets'
        Items = @($Items)
    })
}

function Add-ReportTable {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Title,

        [Parameter()]
        [AllowEmptyCollection()]
        [object[]]$Rows = @(),

        [Parameter(Mandatory = $true)]
        [string[]]$Columns,

        [Parameter()]
        [hashtable]$Labels = @{},

        [Parameter()]
        [int]$MaxRows = 5000,

        [Parameter()]
        [string]$EmptyMessage = 'No data was collected or the component is not present.'
    )

    # Snapshot table data into flat strings at the time the block is created.
    # This prevents CIM, WMI, deserialized, or vendor objects from being retained
    # inside $reportBlocks and later traversed by HTML/DOCX rendering.
    $safeRows = New-Object System.Collections.ArrayList
    $totalRows = 0

    foreach ($row in $Rows) {
        if ($null -eq $row) {
            continue
        }

        $totalRows++
        if (($MaxRows -gt 0) -and ($safeRows.Count -ge $MaxRows)) {
            continue
        }

        $flatRow = [ordered]@{}
        foreach ($column in $Columns) {
            try {
                $flatRow[[string]$column] = Get-ReportCellText -InputObject $row -PropertyName ([string]$column) -MaxLength 2500
            }
            catch {
                $flatRow[[string]$column] = '[value could not be rendered; see supporting export]'
            }
        }

        [void]$safeRows.Add([pscustomobject]$flatRow)
    }

    [void]$script:reportBlocks.Add([pscustomobject]@{
        Type         = 'Table'
        Title        = $Title
        Rows         = @($safeRows)
        Columns      = @($Columns)
        Labels       = $Labels
        TotalRows    = $totalRows
        DisplayRows  = $safeRows.Count
        EmptyMessage = $EmptyMessage
    })
}

function Add-RoleRecoveryGap {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [System.Collections.ArrayList]$List,

        [Parameter(Mandatory = $true)]
        [string]$Severity,

        [Parameter(Mandatory = $true)]
        [string]$Requirement,

        [Parameter(Mandatory = $true)]
        [string]$Reason,

        [Parameter(Mandatory = $true)]
        [string]$Action
    )

    [void]$List.Add([pscustomobject]@{
        Severity    = $Severity
        Requirement = $Requirement
        Reason      = $Reason
        Action      = $Action
    })
}

Write-Host 'BEGIN individual startup statements'
Write-Host 'BEGIN source line 3487'
$reportSelfTestGaps = New-Object System.Collections.ArrayList
Write-Host 'PASS source line 3487'
Write-Host 'BEGIN source line 3488'
Add-RoleRecoveryGap -List $reportSelfTestGaps -Severity 'Test' -Requirement 'Runtime self-test' -Reason 'Runtime self-test' -Action 'Runtime self-test'
Write-Host 'PASS source line 3488'
Write-Host 'BEGIN source line 3489'
if ($reportSelfTestGaps.Count -ne 1) {
        throw 'The recovery-gap helper did not accept and populate an initially empty collection.'
    }
Write-Host 'PASS source line 3489'
Write-Host 'BEGIN source line 3493'
$script:reportBlocks = New-Object System.Collections.ArrayList
Write-Host 'PASS source line 3493'
Write-Host 'BEGIN source line 3494'
Add-ReportTable -Title 'Runtime self-test' -Rows @() -Columns @('Value') -MaxRows 1
Write-Host 'PASS source line 3494'
Write-Host 'BEGIN source line 3495'
Add-ReportBullets -Items @()
Write-Host 'PASS source line 3495'
Write-Host 'BEGIN source line 3496'
if ($script:reportBlocks.Count -ne 2) {
        throw 'The report block helpers did not populate their test collection as expected.'
    }
Write-Host 'PASS source line 3496'
Write-Host 'BEGIN source line 3501'
$reportSelfTestCycle = New-Object System.Collections.ArrayList
Write-Host 'PASS source line 3501'
Write-Host 'BEGIN source line 3502'
[void]$reportSelfTestCycle.Add('cycle-root')
Write-Host 'PASS source line 3502'
Write-Host 'BEGIN source line 3503'
[void]$reportSelfTestCycle.Add($reportSelfTestCycle)
Write-Host 'PASS source line 3503'
Write-Host 'BEGIN source line 3504'
$reportSelfTestCycleText = ConvertTo-ReportText -Value $reportSelfTestCycle -MaxLength 1000
Write-Host 'PASS source line 3504'
Write-Host 'BEGIN source line 3505'
if ($reportSelfTestCycleText -notmatch 'cycle-root') {
        throw 'The report text formatter did not safely process a cyclic collection.'
    }
Write-Host 'PASS source line 3505'
Write-Host 'BEGIN source line 3509'
$reportSelfTestDeep = 'deep-leaf'
Write-Host 'PASS source line 3509'
Write-Host 'BEGIN source line 3510'
for ($reportSelfTestIndex = 0; $reportSelfTestIndex -lt 150; $reportSelfTestIndex++) {
        $reportSelfTestContainer = New-Object System.Collections.ArrayList
        [void]$reportSelfTestContainer.Add($reportSelfTestDeep)
        $reportSelfTestDeep = $reportSelfTestContainer
    }
Write-Host 'PASS source line 3510'
Write-Host 'BEGIN source line 3515'
$reportSelfTestDeepText = ConvertTo-ReportText -Value $reportSelfTestDeep -MaxLength 1000
Write-Host 'PASS source line 3515'
Write-Host 'BEGIN source line 3516'
if ([string]::IsNullOrWhiteSpace($reportSelfTestDeepText)) {
        throw 'The report text formatter did not safely process a deeply nested collection.'
    }
Write-Host 'PASS source line 3516'
Write-Host 'BEGIN source line 3523'
$reportSelfTestRow = [pscustomobject]@{ Name = 'cycle-row'; Value = $reportSelfTestCycle }
Write-Host 'PASS source line 3523'
Write-Host 'BEGIN source line 3524'
$script:reportBlocks = New-Object System.Collections.ArrayList
Write-Host 'PASS source line 3524'
Write-Host 'BEGIN source line 3525'
Add-ReportTable -Title 'Circular value snapshot test' -Rows @($reportSelfTestRow) -Columns @('Name', 'Value') -MaxRows 10
Write-Host 'PASS source line 3525'
Write-Host 'BEGIN source line 3526'
if ($script:reportBlocks.Count -ne 1) {
        throw 'The report table snapshot self-test did not create exactly one report block.'
    }
Write-Host 'PASS source line 3526'
Write-Host 'BEGIN source line 3529'
$reportSelfTestStoredValue = $script:reportBlocks[0].Rows[0].Value
Write-Host 'PASS source line 3529'
Write-Host 'BEGIN source line 3530'
if (-not ($reportSelfTestStoredValue -is [string])) {
        throw 'The report table snapshot retained a complex value instead of a flat string.'
    }
Write-Host 'PASS source line 3530'
Write-Host 'BEGIN source line 3533'
if ($reportSelfTestStoredValue -notmatch 'cycle-root') {
        throw 'The report table snapshot did not safely preserve the readable portion of a cyclic value.'
    }
Write-Host 'PASS source line 3533'
Write-Host 'BEGIN source line 3537'
$script:reportBlocks = $null
Write-Host 'PASS source line 3537'
Write-Host 'BEGIN unchanged complete startup self-test block'
try {
    $reportSelfTestGaps = New-Object System.Collections.ArrayList
    Add-RoleRecoveryGap -List $reportSelfTestGaps -Severity 'Test' -Requirement 'Runtime self-test' -Reason 'Runtime self-test' -Action 'Runtime self-test'
    if ($reportSelfTestGaps.Count -ne 1) {
        throw 'The recovery-gap helper did not accept and populate an initially empty collection.'
    }

    $script:reportBlocks = New-Object System.Collections.ArrayList
    Add-ReportTable -Title 'Runtime self-test' -Rows @() -Columns @('Value') -MaxRows 1
    Add-ReportBullets -Items @()
    if ($script:reportBlocks.Count -ne 2) {
        throw 'The report block helpers did not populate their test collection as expected.'
    }

    # Validate that report text conversion cannot recurse indefinitely on cyclic or deeply nested values.
    $reportSelfTestCycle = New-Object System.Collections.ArrayList
    [void]$reportSelfTestCycle.Add('cycle-root')
    [void]$reportSelfTestCycle.Add($reportSelfTestCycle)
    $reportSelfTestCycleText = ConvertTo-ReportText -Value $reportSelfTestCycle -MaxLength 1000
    if ($reportSelfTestCycleText -notmatch 'cycle-root') {
        throw 'The report text formatter did not safely process a cyclic collection.'
    }

    $reportSelfTestDeep = 'deep-leaf'
    for ($reportSelfTestIndex = 0; $reportSelfTestIndex -lt 150; $reportSelfTestIndex++) {
        $reportSelfTestContainer = New-Object System.Collections.ArrayList
        [void]$reportSelfTestContainer.Add($reportSelfTestDeep)
        $reportSelfTestDeep = $reportSelfTestContainer
    }
    $reportSelfTestDeepText = ConvertTo-ReportText -Value $reportSelfTestDeep -MaxLength 1000
    if ([string]::IsNullOrWhiteSpace($reportSelfTestDeepText)) {
        throw 'The report text formatter did not safely process a deeply nested collection.'
    }

    # Exercise the exact table-snapshot path used by the consolidated report.
    # A cyclic value must be converted into a plain string before it is stored
    # in $reportBlocks. This test runs before the long inventory collection.
    $reportSelfTestRow = [pscustomobject]@{ Name = 'cycle-row'; Value = $reportSelfTestCycle }
    $script:reportBlocks = New-Object System.Collections.ArrayList
    Add-ReportTable -Title 'Circular value snapshot test' -Rows @($reportSelfTestRow) -Columns @('Name', 'Value') -MaxRows 10
    if ($script:reportBlocks.Count -ne 1) {
        throw 'The report table snapshot self-test did not create exactly one report block.'
    }
    $reportSelfTestStoredValue = $script:reportBlocks[0].Rows[0].Value
    if (-not ($reportSelfTestStoredValue -is [string])) {
        throw 'The report table snapshot retained a complex value instead of a flat string.'
    }
    if ($reportSelfTestStoredValue -notmatch 'cycle-root') {
        throw 'The report table snapshot did not safely preserve the readable portion of a cyclic value.'
    }

    $script:reportBlocks = $null
}
catch {
    throw ('Consolidated report runtime self-test failed before inventory collection: ' + $_.Exception.Message)
}
Write-Host 'PASS all synthetic startup tests'
