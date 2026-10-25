function Write-ConsolidatedHtmlReport {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [System.Collections.IEnumerable]$Blocks,

        [Parameter(Mandatory = $true)]
        [string]$DocumentTitle
    )

    $builder = New-Object System.Text.StringBuilder
    [void]$builder.AppendLine('<!DOCTYPE html>')
    [void]$builder.AppendLine('<html><head><meta charset="utf-8">')
    [void]$builder.AppendLine('<meta name="viewport" content="width=device-width, initial-scale=1">')
    [void]$builder.AppendLine('<title>' + (ConvertTo-HtmlText -Value $DocumentTitle) + '</title>')
    $styleBlock = @'
<style>
@page { size: landscape; margin: 0.5in; }
* { box-sizing: border-box; }
html { height: 100%; scroll-behavior: smooth; overflow: hidden; }
body { height: 100%; overflow: hidden; font-family: "Segoe UI", Arial, sans-serif; margin: 0; color: #1f2933; background: #eef2f7; line-height: 1.4; }
.report-app { width: 100%; max-width: none; height: 100%; min-height: 0; margin: 0 auto; padding: 18px; display: flex; flex-direction: column; overflow: hidden; }
.cover { flex: 0 0 auto; border: 1px solid #9fb3c8; border-radius: 12px; padding: 18px 22px; margin-bottom: 12px; background: #ffffff; box-shadow: 0 6px 22px rgba(25, 45, 70, 0.08); }
h1 { font-size: 25px; margin: 0 0 8px 0; color: #102a43; }
.subtitle { font-size: 14px; color: #486581; margin-bottom: 12px; }
.meta { color: #627d98; font-size: 9.5pt; }
.report-layout { width: 100%; max-width: none; flex: 1 1 auto; min-height: 0; height: 100%; display: grid; grid-template-columns: 285px minmax(0, 1fr); grid-template-rows: minmax(0, 1fr); gap: 14px; align-items: stretch; overflow: hidden; }
.report-nav { position: static; height: 100%; min-height: 0; max-height: 100%; overflow-y: scroll; overflow-x: hidden; overscroll-behavior: contain; scrollbar-gutter: stable; border: 1px solid #bcccdc; border-radius: 12px; background: #ffffff; padding: 12px; box-shadow: 0 4px 16px rgba(25, 45, 70, 0.06); }
.nav-heading { color: #102a43; font-size: 11pt; font-weight: 700; margin: 2px 4px 9px; }
.nav-help { color: #627d98; font-size: 8.5pt; margin: 0 4px 11px; }
.section-nav-button { width: 100%; border: 1px solid transparent; border-radius: 8px; background: transparent; color: #334e68; padding: 8px 9px; margin: 2px 0; text-align: left; font: inherit; font-size: 9.5pt; cursor: pointer; }
.section-nav-button:hover { background: #f0f4f8; border-color: #d9e2ec; }
.section-nav-button.active { background: #e8f0fe; border-color: #9fbaf0; color: #174ea6; font-weight: 700; }
.nav-group { margin: 2px 0 7px; }
.subsection-list { margin: 1px 0 4px 12px; padding-left: 8px; border-left: 1px solid #d9e2ec; }
.subsection-nav-button { width: 100%; border: 0; border-radius: 6px; background: transparent; color: #627d98; padding: 5px 7px; text-align: left; font: inherit; font-size: 8.5pt; line-height: 1.25; cursor: pointer; }
.subsection-nav-button:hover { background: #f0f4f8; color: #243b53; }
.subsection-nav-button.active { background: #f0f5ff; color: #174ea6; font-weight: 600; }
.subsection-anchor { scroll-margin-top: 16px; }
.report-content { width: 100%; max-width: none; min-width: 0; min-height: 0; height: 100%; max-height: 100%; overflow-y: scroll; overflow-x: hidden; overscroll-behavior: contain; padding-right: 3px; scrollbar-gutter: stable; }
.section-pager { display: flex; align-items: center; justify-content: space-between; gap: 12px; border: 1px solid #bcccdc; border-radius: 10px; background: #ffffff; padding: 10px 12px; margin-bottom: 12px; }
.pager-button { min-width: 120px; border: 1px solid #9fb3c8; border-radius: 7px; background: #ffffff; color: #243b53; padding: 7px 10px; cursor: pointer; font: inherit; }
.pager-button:hover:not(:disabled) { background: #f0f4f8; }
.pager-button:disabled { opacity: 0.42; cursor: default; }
.pager-label { min-width: 0; color: #486581; font-size: 9.5pt; font-weight: 600; text-align: center; }
.report-section { border: 1px solid #bcccdc; border-radius: 12px; background: #ffffff; padding: 22px; box-shadow: 0 4px 16px rgba(25, 45, 70, 0.05); overflow-x: auto; }
.report-js .report-section { display: none; }
.report-js .report-section.active { display: block; }
h2 { font-size: 20px; margin: 0 0 14px 0; padding-bottom: 7px; border-bottom: 2px solid #bcccdc; color: #102a43; }
h3 { font-size: 16px; margin: 22px 0 8px 0; color: #243b53; }
h4 { font-size: 13px; margin: 16px 0 6px 0; color: #334e68; }
p { margin: 7px 0; }
.notice { border-left: 5px solid #d64545; background: #fff5f5; padding: 10px 12px; margin: 12px 0; font-weight: 600; }
.info { border-left: 5px solid #627d98; background: #f0f4f8; padding: 10px 12px; margin: 12px 0; }
ul { margin-top: 6px; padding-left: 24px; }
table { border-collapse: collapse; width: 100%; margin: 8px 0 18px 0; font-size: 9pt; page-break-inside: auto; }
thead { display: table-header-group; }
tr { page-break-inside: avoid; page-break-after: auto; }
th { background: #d9e2ec; color: #102a43; font-weight: 700; text-align: left; border: 1px solid #829ab1; padding: 5px; vertical-align: top; }
td { border: 1px solid #bcccdc; padding: 5px; vertical-align: top; overflow-wrap: anywhere; }
tr:nth-child(even) td { background: #f8fafc; }
.table-note { color: #52667a; font-size: 9pt; font-style: italic; margin-top: -12px; margin-bottom: 16px; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; border: 1px solid #bcccdc; background: #f8fafc; padding: 10px; font-family: Consolas, "Courier New", monospace; font-size: 8pt; }
.footer-note { margin-top: 18px; border: 1px solid #bcccdc; border-radius: 10px; background: #ffffff; padding: 10px 12px; color: #52667a; font-size: 9pt; }
@media (max-width: 980px) {
    .report-app { padding: 10px; }
    .report-layout { grid-template-columns: 235px minmax(0, 1fr); gap: 10px; }
    .report-nav { display: block; }
    .nav-heading, .nav-help { display: block; }
    .nav-group { display: block; width: auto; padding-right: 0; }
    .section-nav-button { margin: 0; }
}
@media (max-width: 560px) {
    .report-layout { grid-template-columns: 185px minmax(0, 1fr); gap: 8px; }
    .nav-group { width: 100%; padding-right: 0; }
    .section-pager { flex-wrap: wrap; }
    .pager-label { order: -1; width: 100%; }
    .pager-button { flex: 1 1 120px; min-width: 92px; }
    .report-section { padding: 14px; }
}
@media print {
    html, body { height: auto; overflow: visible; }
    body { background: #ffffff; }
    .report-app { display: block; height: auto; overflow: visible; max-width: none; padding: 0; }
    .cover { border: 0; box-shadow: none; padding: 0 0 16px 0; }
    .report-layout { display: block; overflow: visible; }
    .report-content { height: auto; overflow: visible; }
    .report-nav, .section-pager { display: none !important; }
    .report-js .report-section, .report-section { display: block !important; border: 0; box-shadow: none; padding: 0; margin: 0 0 22px 0; }
    .report-section + .report-section { page-break-before: always; }
    a { color: inherit; text-decoration: none; }
}
</style>
'@
    [void]$builder.AppendLine($styleBlock)
    [void]$builder.AppendLine('</head><body>')
    [void]$builder.AppendLine('<div class="report-app">')

    # Render the cover separately so navigation remains available for every report section.
    foreach ($block in $Blocks) {
        if ([string]$block.Type -eq 'Title') {
            [void]$builder.AppendLine('<div class="cover">')
            [void]$builder.AppendLine('<h1>' + (ConvertTo-HtmlText -Value $block.Text) + '</h1>')
            if (-not [string]::IsNullOrWhiteSpace([string]$block.Subtitle)) {
                [void]$builder.AppendLine('<div class="subtitle">' + (ConvertTo-HtmlText -Value $block.Subtitle) + '</div>')
            }
            [void]$builder.AppendLine('<div class="meta">Generated automatically. Treat as sensitive infrastructure documentation. This inventory can assist with recovery or rebuild work, but it is not a backup.</div>')
            [void]$builder.AppendLine('</div>')
        }
    }

    # Build hierarchical navigation. Every main section is directly accessible, and
    # every heading, table, and preformatted dataset inside a section is exposed as
    # a subsection link. The same deterministic IDs are regenerated while rendering.
    [void]$builder.AppendLine('<div class="report-layout">')
    [void]$builder.AppendLine('<nav class="report-nav" aria-label="Report sections and subsections" tabindex="0">')
    [void]$builder.AppendLine('<div class="nav-heading">Report Sections</div>')
    [void]$builder.AppendLine('<div class="nav-help">Select a main section or jump directly to a subsection. Printing still includes the complete report.</div>')
    [void]$builder.AppendLine('<div class="nav-group">')
    [void]$builder.AppendLine('<button type="button" class="section-nav-button section-main-button active" data-section="section-overview">Overview</button>')
    [void]$builder.AppendLine('</div>')

    $navSectionNumber = 0
    $navSubsectionNumber = 0
    $navGroupOpen = $false
    foreach ($block in $Blocks) {
        $navBlockType = [string]$block.Type
        if (($navBlockType -eq 'Heading') -and ([int]$block.Level -eq 1)) {
            if ($navGroupOpen) {
                [void]$builder.AppendLine('</div></div>')
            }
            $navSectionNumber++
            $navSubsectionNumber = 0
            [void]$builder.AppendLine('<div class="nav-group">')
            [void]$builder.AppendLine('<button type="button" class="section-nav-button section-main-button" data-section="section-' + $navSectionNumber + '">' + (ConvertTo-HtmlText -Value $block.Text) + '</button>')
            [void]$builder.AppendLine('<div class="subsection-list">')
            $navGroupOpen = $true
            continue
        }
        if ($navSectionNumber -le 0) { continue }

        $navTitle = ''
        if (($navBlockType -eq 'Heading') -and ([int]$block.Level -ge 2)) {
            $navTitle = [string]$block.Text
        }
        elseif (($navBlockType -eq 'Table') -or ($navBlockType -eq 'Preformatted')) {
            $navTitle = [string]$block.Title
        }
        if (-not [string]::IsNullOrWhiteSpace($navTitle)) {
            $navSubsectionNumber++
            $navAnchorId = 'section-' + $navSectionNumber + '-sub-' + $navSubsectionNumber
            [void]$builder.AppendLine('<button type="button" class="subsection-nav-button" data-section="section-' + $navSectionNumber + '" data-anchor="' + $navAnchorId + '">' + (ConvertTo-HtmlText -Value $navTitle) + '</button>')
        }
    }
    if ($navGroupOpen) {
        [void]$builder.AppendLine('</div></div>')
    }
    [void]$builder.AppendLine('</nav>')
    [void]$builder.AppendLine('<main class="report-content" tabindex="0">')
    [void]$builder.AppendLine('<div class="section-pager">')
    [void]$builder.AppendLine('<button id="previous-section" class="pager-button" type="button">Previous section</button>')
    [void]$builder.AppendLine('<div id="section-label" class="pager-label">Overview</div>')
    [void]$builder.AppendLine('<button id="next-section" class="pager-button" type="button">Next section</button>')
    [void]$builder.AppendLine('</div>')

    $openedSection = $true
    $currentSectionNumber = 0
    $currentSubsectionNumber = 0
    [void]$builder.AppendLine('<section id="section-overview" class="report-section active">')
    [void]$builder.AppendLine('<h2>Overview</h2>')

    foreach ($block in $Blocks) {
        $blockType = [string]$block.Type
        if ($blockType -eq 'Title') {
            continue
        }

        if (($blockType -eq 'Heading') -and ([int]$block.Level -eq 1)) {
            if ($openedSection) {
                [void]$builder.AppendLine('</section>')
            }
            $currentSectionNumber++
            $currentSubsectionNumber = 0
            [void]$builder.AppendLine('<section id="section-' + $currentSectionNumber + '" class="report-section">')
            $openedSection = $true
            [void]$builder.AppendLine('<h2>' + (ConvertTo-HtmlText -Value $block.Text) + '</h2>')
            continue
        }

        switch ($blockType) {
            'Heading' {
                $level = [int]$block.Level
                $currentSubsectionNumber++
                $subsectionId = 'section-' + $currentSectionNumber + '-sub-' + $currentSubsectionNumber
                if ($level -eq 2) {
                    [void]$builder.AppendLine('<h3 id="' + $subsectionId + '" class="subsection-anchor">' + (ConvertTo-HtmlText -Value $block.Text) + '</h3>')
                }
                else {
                    [void]$builder.AppendLine('<h4 id="' + $subsectionId + '" class="subsection-anchor">' + (ConvertTo-HtmlText -Value $block.Text) + '</h4>')
                }
            }
            'Paragraph' {
                $className = ''
                if ($block.Warning) {
                    $className = ' class="notice"'
                }
                elseif ($block.Bold) {
                    $className = ' class="info"'
                }
                $content = ConvertTo-HtmlText -Value $block.Text
                if ($block.Bold -and -not $block.Warning) {
                    $content = '<strong>' + $content + '</strong>'
                }
                [void]$builder.AppendLine('<p' + $className + '>' + $content + '</p>')
            }
            'Bullets' {
                [void]$builder.AppendLine('<ul>')
                foreach ($item in @($block.Items)) {
                    [void]$builder.AppendLine('<li>' + (ConvertTo-HtmlText -Value $item) + '</li>')
                }
                [void]$builder.AppendLine('</ul>')
            }
            'Preformatted' {
                $currentSubsectionNumber++
                $subsectionId = 'section-' + $currentSectionNumber + '-sub-' + $currentSubsectionNumber
                [void]$builder.AppendLine('<h4 id="' + $subsectionId + '" class="subsection-anchor">' + (ConvertTo-HtmlText -Value $block.Title) + '</h4>')
                if ([string]::IsNullOrWhiteSpace([string]$block.Text)) {
                    [void]$builder.AppendLine('<p>No data was collected or the component is not present.</p>')
                }
                else {
                    [void]$builder.AppendLine('<pre>' + (ConvertTo-HtmlMultilineText -Value $block.Text) + '</pre>')
                }
            }
            'Table' {
                $currentSubsectionNumber++
                $subsectionId = 'section-' + $currentSectionNumber + '-sub-' + $currentSubsectionNumber
                [void]$builder.AppendLine('<h4 id="' + $subsectionId + '" class="subsection-anchor">' + (ConvertTo-HtmlText -Value $block.Title) + '</h4>')
                if ($block.DisplayRows -le 0) {
                    [void]$builder.AppendLine('<p>' + (ConvertTo-HtmlText -Value $block.EmptyMessage) + '</p>')
                }
                else {
                    [void]$builder.AppendLine('<div style="overflow-x:auto">')
                    [void]$builder.AppendLine('<table><thead><tr>')
                    foreach ($column in @($block.Columns)) {
                        $header = $column
                        if ($block.Labels -and $block.Labels.ContainsKey($column)) {
                            $header = $block.Labels[$column]
                        }
                        [void]$builder.AppendLine('<th>' + (ConvertTo-HtmlText -Value $header) + '</th>')
                    }
                    [void]$builder.AppendLine('</tr></thead><tbody>')
                    foreach ($row in @($block.Rows)) {
                        [void]$builder.AppendLine('<tr>')
                        foreach ($column in @($block.Columns)) {
                            $cellText = Get-ReportCellText -InputObject $row -PropertyName $column -MaxLength 2500
                            [void]$builder.AppendLine('<td>' + (ConvertTo-HtmlText -Value $cellText) + '</td>')
                        }
                        [void]$builder.AppendLine('</tr>')
                    }
                    [void]$builder.AppendLine('</tbody></table>')
                    [void]$builder.AppendLine('</div>')
                    if ($block.TotalRows -gt $block.DisplayRows) {
                        [void]$builder.AppendLine('<div class="table-note">Showing ' + $block.DisplayRows + ' of ' + $block.TotalRows + ' rows. The complete dataset remains in the supporting exports.</div>')
                    }
                }
            }
        }
    }

    if ($openedSection) {
        [void]$builder.AppendLine('</section>')
    }

    [void]$builder.AppendLine('<div class="footer-note">The consolidated report is the primary engineering reference for inventory review. Supporting exports are retained for validation, migration, recovery, restoration, rebuild assistance, and details too large for a practical report.</div>')
    [void]$builder.AppendLine('</main></div></div>')

    $scriptBlock = @'
<script>
(function () {
    var root = document.documentElement;
    root.className = (root.className ? root.className + ' ' : '') + 'report-js';
    var sections = Array.prototype.slice.call(document.querySelectorAll('.report-section'));
    var buttons = Array.prototype.slice.call(document.querySelectorAll('.section-main-button'));
    var subsectionButtons = Array.prototype.slice.call(document.querySelectorAll('.subsection-nav-button'));
    var previousButton = document.getElementById('previous-section');
    var nextButton = document.getElementById('next-section');
    var sectionLabel = document.getElementById('section-label');
    var currentIndex = 0;

    function sectionIndexById(id) {
        for (var i = 0; i < sections.length; i++) { if (sections[i].id === id) { return i; } }
        return -1;
    }
    function clearSubsectionSelection() {
        for (var i = 0; i < subsectionButtons.length; i++) { subsectionButtons[i].className = 'subsection-nav-button'; }
    }
    function activate(index, updateHash, anchorId) {
        if (!sections.length) { return; }
        if (index < 0) { index = 0; }
        if (index >= sections.length) { index = sections.length - 1; }
        currentIndex = index;
        for (var i = 0; i < sections.length; i++) {
            if (i === index) {
                sections[i].className = 'report-section active';
                buttons[i].className = 'section-nav-button section-main-button active';
            } else {
                sections[i].className = 'report-section';
                buttons[i].className = 'section-nav-button section-main-button';
            }
        }
        sectionLabel.textContent = buttons[index].textContent + '  |  ' + (index + 1) + ' of ' + sections.length;
        previousButton.disabled = index === 0;
        nextButton.disabled = index === sections.length - 1;
        if (buttons[index] && buttons[index].scrollIntoView) { buttons[index].scrollIntoView(false); }
        if (!anchorId) { clearSubsectionSelection(); }
        var hashTarget = anchorId || sections[index].id;
        if (updateHash && window.history && window.history.replaceState) {
            window.history.replaceState(null, '', '#' + hashTarget);
        } else if (updateHash) {
            window.location.hash = hashTarget;
        }
        var scrollTarget = anchorId ? document.getElementById(anchorId) : sections[index];
        if (scrollTarget && scrollTarget.scrollIntoView) { scrollTarget.scrollIntoView(true); }
    }

    for (var i = 0; i < buttons.length; i++) {
        (function (index) {
            buttons[index].addEventListener('click', function () { activate(index, true, ''); });
        }(i));
    }
    for (var j = 0; j < subsectionButtons.length; j++) {
        (function (button) {
            button.addEventListener('click', function () {
                var sectionId = button.getAttribute('data-section');
                var anchorId = button.getAttribute('data-anchor');
                var index = sectionIndexById(sectionId);
                if (index < 0) { return; }
                clearSubsectionSelection();
                button.className = 'subsection-nav-button active';
                activate(index, true, anchorId);
                button.className = 'subsection-nav-button active';
            });
        }(subsectionButtons[j]));
    }
    previousButton.addEventListener('click', function () { activate(currentIndex - 1, true, ''); });
    nextButton.addEventListener('click', function () { activate(currentIndex + 1, true, ''); });

    var requestedId = window.location.hash ? window.location.hash.substring(1) : '';
    if (requestedId) {
        var requestedElement = document.getElementById(requestedId);
        if (requestedElement) {
            var parentSection = requestedElement;
            while (parentSection && (!parentSection.className || String(parentSection.className).indexOf('report-section') < 0)) { parentSection = parentSection.parentNode; }
            if (parentSection) {
                var requestedIndex = sectionIndexById(parentSection.id);
                if (requestedIndex >= 0) {
                    activate(requestedIndex, false, requestedId === parentSection.id ? '' : requestedId);
                    for (var k = 0; k < subsectionButtons.length; k++) {
                        if (subsectionButtons[k].getAttribute('data-anchor') === requestedId) { subsectionButtons[k].className = 'subsection-nav-button active'; break; }
                    }
                    return;
                }
            }
        }
    }
    activate(0, false, '');
}());
</script>
'@
    [void]$builder.AppendLine($scriptBlock)
    [void]$builder.AppendLine('</body></html>')

    $encoding = New-Object System.Text.UTF8Encoding($true)
    [System.IO.File]::WriteAllText($Path, $builder.ToString(), $encoding)
}

