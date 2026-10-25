function Write-ConsolidatedHtmlReport {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][System.Collections.IEnumerable]$Blocks,
        [Parameter(Mandatory = $true)][string]$DocumentTitle
    )

    # Presentation only. Keep every original block, row, column and warning.
    # The standalone collector embeds the stylesheet and script at build time.
    $identity = @{}
    $metrics = @{}
    $anchors = @{}
    $entries = New-Object System.Collections.ArrayList
    $currentEntry = [pscustomobject]@{ Id='section-overview'; Title='About this report'; Links=(New-Object System.Collections.ArrayList) }
    [void]$entries.Add($currentEntry)
    $sectionNumber = 0
    $subsectionNumber = 0
    $summaryId = 'section-overview'
    $subtitle = ''
    $blockList = @($Blocks)
    for ($index = 0; $index -lt $blockList.Count; $index++) {
        $block = $blockList[$index]
        if ([string]$block.Type -eq 'Title') { $subtitle = [string]$block.Subtitle }
        if (([string]$block.Type -eq 'Heading') -and ([int]$block.Level -eq 1)) {
            $sectionNumber++
            $subsectionNumber = 0
            $currentEntry = [pscustomobject]@{ Id=('section-' + $sectionNumber); Title=([string]$block.Text); Links=(New-Object System.Collections.ArrayList) }
            [void]$entries.Add($currentEntry)
            if ([string]$block.Text -match 'Executive Summary') { $summaryId = $currentEntry.Id }
        }
        elseif ((([string]$block.Type -eq 'Heading') -and ([int]$block.Level -gt 1)) -or ([string]$block.Type -eq 'Table') -or ([string]$block.Type -eq 'Preformatted')) {
            $subsectionNumber++
            $anchor = 'section-' + $sectionNumber + '-sub-' + $subsectionNumber
            $anchors[$index] = $anchor
            $label = ''
            if ([string]$block.Type -eq 'Heading') { $label = [string]$block.Text }
            else { $label = [string]$block.Title }
            [void]$currentEntry.Links.Add([pscustomobject]@{ Id=$anchor; Title=$label })
        }
        if ([string]$block.Type -eq 'Table') {
            if ([string]$block.Title -eq 'Server identity and baseline') {
                foreach ($row in @($block.Rows)) { $identity[[string]$row.Field] = [string]$row.Value }
            }
            if ([string]$block.Title -eq 'Inventory at a glance') {
                foreach ($row in @($block.Rows)) { $metrics[[string]$row.Area] = [string]$row.Count }
            }
        }
    }
    $serverName = [string]$identity['Computer name']
    if ([string]::IsNullOrWhiteSpace($serverName)) { $serverName = $DocumentTitle }
    $builder = New-Object System.Text.StringBuilder

    function Get-CleanReportTitle {
        param([string]$Text)
        return ($Text -replace '^\d+(\.\d+)*\.?\s+', '')
    }
    function Write-CleanReportSection {
        param([string]$Id, [string]$Title)
        $intro = 'Captured settings and detailed inventory.'
        if ($Id -eq $summaryId) { $intro = 'A quick view of this server and the items that need review.' }
        elseif ($Title -match 'Network') { $intro = 'IP addressing, routing, adapters, and listening services.' }
        elseif ($Title -match 'Storage') { $intro = 'Disks, volumes, paths, and storage configuration.' }
        elseif ($Title -match 'Recovery|Rebuild|Migration') { $intro = 'Review the requirements before planning migration or recovery.' }
        elseif ($Title -match 'Collection Appendix') { $intro = 'Collection results and supporting export references.' }
        [void]$builder.AppendLine('<section id="' + $Id + '" class="report-section">')
        [void]$builder.AppendLine('<h2 class="section-title">' + (ConvertTo-HtmlText (Get-CleanReportTitle $Title)) + '</h2>')
        [void]$builder.AppendLine('<p class="section-intro">' + $intro + '</p>')
        if ($Id -eq $summaryId) {
            [void]$builder.AppendLine('<div class="facts">')
            foreach ($name in @('Operating system','Domain or workgroup','Physical or virtual','Collected')) {
                $value = [string]$identity[$name]
                if ([string]::IsNullOrWhiteSpace($value)) { $value = 'Not collected' }
                [void]$builder.AppendLine('<div><span class="fact-label">' + (ConvertTo-HtmlText $name) + '</span><span class="fact-value">' + (ConvertTo-HtmlText $value) + '</span></div>')
            }
            [void]$builder.AppendLine('</div><div class="metrics">')
            foreach ($metric in @(
                @{Key='Collectors completed successfully'; Label='Collectors completed'},
                @{Key='Collectors failed or unavailable'; Label='Collectors requiring review'},
                @{Key='Detected critical roles and platforms'; Label='Critical roles and platforms'},
                @{Key='Installed applications'; Label='Installed applications'}
            )) {
                $value = [string]$metrics[$metric.Key]
                if ($value -notmatch '^\d+$') { $value = 'Not collected' }
                $class = 'metric'
                if (($metric.Key -eq 'Collectors failed or unavailable') -and ($value -match '^[1-9]')) { $class += ' metric-review' }
                [void]$builder.AppendLine('<div class="' + $class + '"><span class="metric-value">' + (ConvertTo-HtmlText $value) + '</span><span class="metric-label">' + $metric.Label + '</span></div>')
            }
            [void]$builder.AppendLine('</div><div class="review-links">')
            for ($reviewIndex=0; $reviewIndex -lt $blockList.Count; $reviewIndex++) {
                $review = $blockList[$reviewIndex]
                if (([string]$review.Type -eq 'Table') -and ([int]$review.DisplayRows -gt 0) -and ([string]$review.Title -match '^(Collection failures requiring review|Recovery prerequisites and gaps)$')) {
                    $label = 'collection issues'
                    if ([string]$review.Title -eq 'Recovery prerequisites and gaps') { $label = 'recovery prerequisites or gaps' }
                    if ([int]$review.DisplayRows -eq 1) {
                        $label = 'collection issue'
                        if ([string]$review.Title -eq 'Recovery prerequisites and gaps') { $label = 'recovery prerequisite or gap' }
                    }
                    [void]$builder.AppendLine('<button type="button" class="review-link" data-anchor="' + $anchors[$reviewIndex] + '">' + $review.DisplayRows + ' ' + $label + '</button>')
                }
            }
            [void]$builder.AppendLine('</div>')
        }
        [void]$builder.AppendLine('<div class="section-actions"><button type="button" class="quiet-button" data-details-action="expand">Expand section</button><button type="button" class="quiet-button" data-details-action="collapse">Collapse section</button></div>')
    }

    [void]$builder.AppendLine('<!DOCTYPE html><html lang="en" data-report-layout="clean-v1" data-default-section="' + $summaryId + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">')
    [void]$builder.AppendLine('<title>' + (ConvertTo-HtmlText $DocumentTitle) + '</title>')
    $styleBlock = @'
<style>
__REPORT_CSS__
</style>
'@
    [void]$builder.AppendLine($styleBlock)
    [void]$builder.AppendLine('</head><body><div class="report-app"><header class="cover"><div class="report-brand"><div class="eyebrow">Server inventory / configuration snapshot</div>')
    [void]$builder.AppendLine('<h1>' + (ConvertTo-HtmlText $serverName) + '</h1><p class="subtitle">' + (ConvertTo-HtmlText $subtitle) + '</p></div>')
    [void]$builder.AppendLine('<button class="nav-toggle" id="toggle-report-nav" aria-controls="report-nav" aria-expanded="false" type="button">Sections</button>')
    [void]$builder.AppendLine('<div class="standalone-tools"><form id="report-search-form" class="report-search" role="search"><input id="report-search-input" type="search" aria-label="Search the full report" placeholder="Search the full report"><button class="quiet-button" type="submit">Find</button><button id="report-print" class="quiet-button" type="button">Print all</button></form>')
    [void]$builder.AppendLine('<div class="search-controls"><button id="report-search-prev" class="quiet-button" type="button" disabled>Previous match</button><button id="report-search-next" class="quiet-button" type="button" disabled>Next match</button><button id="report-search-clear" class="quiet-button" type="button">Clear</button></div><div id="report-search-message" class="search-message" role="status" aria-live="polite">Search every section, including collapsed details.</div></div></header>')
    [void]$builder.AppendLine('<div class="report-layout"><nav class="report-nav" id="report-nav" aria-label="Report sections" tabindex="0"><div class="nav-heading">Inventory sections</div>')
    $orderedEntries = @($entries | Where-Object { $_.Id -eq $summaryId }) + @($entries | Where-Object { ($_.Id -ne $summaryId) -and ($_.Id -ne 'section-overview') -and ($_.Title -ne 'Report Contents') }) + @($entries | Where-Object { ($_.Id -ne $summaryId) -and (($_.Id -eq 'section-overview') -or ($_.Title -eq 'Report Contents')) })
    $referenceStarted = $false
    foreach ($entry in $orderedEntries) {
        if ((-not $referenceStarted) -and (($entry.Id -eq 'section-overview') -or ($entry.Title -eq 'Report Contents')) -and ($entry.Id -ne $summaryId)) {
            [void]$builder.AppendLine('<div class="nav-reference"><div class="nav-heading">Reference</div>')
            $referenceStarted = $true
        }
        $title = Get-CleanReportTitle $entry.Title
        $number = ''
        if ($entry.Title -match '^(\d+)\.') { $number = ([int]$matches[1]).ToString('00') }
        [void]$builder.AppendLine('<div class="nav-group"><button type="button" class="section-nav-button section-main-button" data-section="' + $entry.Id + '" data-title="' + (ConvertTo-HtmlText $title) + '"><span class="nav-number" aria-hidden="true">' + $number + '</span><span>' + (ConvertTo-HtmlText $title) + '</span></button>')
        if ($entry.Links.Count -gt 0) {
            [void]$builder.AppendLine('<div class="subsection-list">')
            foreach ($link in $entry.Links) {
                [void]$builder.AppendLine('<button type="button" class="subsection-nav-button" data-anchor="' + $link.Id + '">' + (ConvertTo-HtmlText (Get-CleanReportTitle $link.Title)) + '</button>')
            }
            [void]$builder.AppendLine('</div>')
        }
        [void]$builder.AppendLine('</div>')
    }
    if ($referenceStarted) { [void]$builder.AppendLine('</div>') }
    [void]$builder.AppendLine('</nav><main class="report-content" tabindex="0" aria-label="Report content"><div class="reading-width"><div class="section-pager"><div id="section-label" class="pager-label"></div><button id="previous-section" class="pager-button" type="button">Previous section</button><button id="next-section" class="pager-button" type="button">Next section</button></div>')
    Write-CleanReportSection -Id 'section-overview' -Title 'About this report'
    $sectionNumber = 0
    $datasetInSection = 0
    $currentId = 'section-overview'
    for ($index = 0; $index -lt $blockList.Count; $index++) {
        $block = $blockList[$index]
        $type = [string]$block.Type
        if ($type -eq 'Title') { continue }
        if (($type -eq 'Heading') -and ([int]$block.Level -eq 1)) {
            [void]$builder.AppendLine('</section>')
            $sectionNumber++
            $datasetInSection = 0
            $currentId = 'section-' + $sectionNumber
            Write-CleanReportSection -Id $currentId -Title ([string]$block.Text)
            continue
        }
        switch ($type) {
            'Heading' {
                $tag = 'h3'
                if ([int]$block.Level -gt 2) { $tag = 'h4' }
                [void]$builder.AppendLine('<' + $tag + ' id="' + $anchors[$index] + '" class="subsection-anchor">' + (ConvertTo-HtmlText $block.Text) + '</' + $tag + '>')
            }
            'Paragraph' {
                $class = ''
                if ($block.Warning) { $class = 'notice' }
                elseif ($block.Bold) { $class = 'info' }
                $text = ConvertTo-HtmlText $block.Text
                if ($block.Bold -and -not $block.Warning) { $text = '<strong>' + $text + '</strong>' }
                [void]$builder.AppendLine('<p class="' + $class + '">' + $text + '</p>')
            }
            'Bullets' {
                [void]$builder.AppendLine('<ul>')
                foreach ($item in @($block.Items)) { [void]$builder.AppendLine('<li>' + (ConvertTo-HtmlText $item) + '</li>') }
                [void]$builder.AppendLine('</ul>')
            }
            { ($_ -eq 'Table') -or ($_ -eq 'Preformatted') } {
                $datasetInSection++
                $priority = ([string]$block.Title -match '^(Collection failures requiring review|Recovery prerequisites and gaps)$') -and ($type -eq 'Table') -and ([int]$block.DisplayRows -gt 0)
                $openByDefault = $priority
                $class = 'dataset'
                $count = 'Text output'
                if ($priority) { $class += ' dataset-priority' }
                if ($type -eq 'Table') {
                    $count = [string]$block.DisplayRows + ' rows'
                    if ([int]$block.DisplayRows -eq 1) { $count = '1 row' }
                    if ([int]$block.TotalRows -gt [int]$block.DisplayRows) { $count = [string]$block.DisplayRows + ' of ' + [string]$block.TotalRows + ' rows' }
                    if ([int]$block.DisplayRows -gt 15) { $class += ' dataset-long' }
                    if (($currentId -ne $summaryId) -and ($datasetInSection -eq 1) -and ([int]$block.DisplayRows -gt 0) -and ([int]$block.DisplayRows -le 8) -and (@($block.Columns).Count -le 5)) { $openByDefault = $true }
                }
                $openText = 'false'
                if ($openByDefault) { $openText = 'true' }
                # Render open in the source for no-script and print fallbacks;
                # the script only collapses the lower-priority screen details.
                [void]$builder.AppendLine('<details open id="' + $anchors[$index] + '" class="' + $class + '" data-default-open="' + $openText + '" data-title="' + (ConvertTo-HtmlText $block.Title) + '"><summary><span class="dataset-title">' + (ConvertTo-HtmlText $block.Title) + '</span><span class="dataset-count">' + $count + '</span></summary><div class="dataset-body">')
                if ($type -eq 'Preformatted') {
                    if ([string]::IsNullOrWhiteSpace([string]$block.Text)) { [void]$builder.AppendLine('<p class="empty-data">No data was collected or the component is not present.</p>') }
                    else { [void]$builder.AppendLine('<pre>' + (ConvertTo-HtmlMultilineText $block.Text) + '</pre>') }
                }
                elseif ([int]$block.DisplayRows -le 0) { [void]$builder.AppendLine('<p class="empty-data">' + (ConvertTo-HtmlText $block.EmptyMessage) + '</p>') }
                else {
                    $tableClass = ''
                    if ([string]$block.Title -eq 'Server identity and baseline') { $tableClass = 'identity-table' }
                    [void]$builder.AppendLine('<div class="table-wrap" tabindex="0" aria-label="' + (ConvertTo-HtmlText $block.Title) + '"><table class="' + $tableClass + '"><thead><tr>')
                    foreach ($column in @($block.Columns)) {
                        $originalLabel = $column
                        $label = ([string]$column -replace '([a-z0-9])([A-Z])', '$1 $2') -replace '([A-Z]+)([A-Z][a-z])', '$1 $2'
                        if ($block.Labels -and $block.Labels.ContainsKey($column)) { $label = $block.Labels[$column]; $originalLabel = $label }
                        [void]$builder.AppendLine('<th scope="col" data-original-label="' + (ConvertTo-HtmlText $originalLabel) + '">' + (ConvertTo-HtmlText $label) + '</th>')
                    }
                    [void]$builder.AppendLine('</tr></thead><tbody>')
                    foreach ($row in @($block.Rows)) {
                        [void]$builder.AppendLine('<tr>')
                        foreach ($column in @($block.Columns)) {
                            $value = Get-ReportCellText -InputObject $row -PropertyName $column -MaxLength 2500
                            $text = ConvertTo-HtmlText $value
                            if ($column -match '^(Status|Severity)$') {
                                $status = ''
                                if ($value -match '^(Success|Complete|Completed)$') { $status = 'success' }
                                elseif ($value -match '^(Failed|Error|Critical|High)$') { $status = 'error' }
                                elseif ($value -match '^(Warning|Unavailable|Skipped|Medium)$') { $status = 'warning' }
                                if ($status) { $text = '<span class="status status-' + $status + '">' + $text + '</span>' }
                            }
                            [void]$builder.AppendLine('<td>' + $text + '</td>')
                        }
                        [void]$builder.AppendLine('</tr>')
                    }
                    [void]$builder.AppendLine('</tbody></table></div>')
                    if ([int]$block.TotalRows -gt [int]$block.DisplayRows) { [void]$builder.AppendLine('<p class="table-note">Showing ' + $block.DisplayRows + ' of ' + $block.TotalRows + ' rows. The complete dataset remains in the supporting exports.</p>') }
                }
                [void]$builder.AppendLine('</div></details>')
            }
        }
    }
    [void]$builder.AppendLine('</section></div><footer class="footer-note">Sensitive infrastructure documentation. This inventory is a configuration snapshot, not a backup. Supporting exports retain raw data and reusable artifacts.</footer></main></div></div>')
    $scriptBlock = @'
<script>
__REPORT_JS__
</script>
'@
    [void]$builder.AppendLine($scriptBlock)
    [void]$builder.AppendLine('</body></html>')
    $encoding = New-Object System.Text.UTF8Encoding($true)
    [System.IO.File]::WriteAllText($Path, $builder.ToString(), $encoding)
}

