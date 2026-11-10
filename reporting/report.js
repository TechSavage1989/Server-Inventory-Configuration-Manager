(function () {
    'use strict';
    var root = document.documentElement;
    var content = document.querySelector('.report-content');
    var nav = document.getElementById('report-nav');
    var sections = Array.prototype.slice.call(document.querySelectorAll('.report-section'));
    var buttons = Array.prototype.slice.call(document.querySelectorAll('.section-main-button'));
    var details = Array.prototype.slice.call(document.querySelectorAll('details.dataset'));
    var summaryId = root.getAttribute('data-default-section');
    var currentIndex = 0, matches = [], matchIndex = -1, printState = null, lastQuery = '', absentCount = 0;
    function byId(id) { return document.getElementById(id); }
    function toArray(list) { return Array.prototype.slice.call(list); }
    function formatNumber(value) { return String(value).replace(/\B(?=(\d{3})+(?!\d))/g, ','); }
    function plural(count, one, many) { return formatNumber(count) + ' ' + (count === 1 ? one : many); }
    function cleanTitle(text) { return String(text || '').replace(/^\s+|\s+$/g, '').replace(/^\d+(\.\d+)*\.?\s+/, ''); }
    function parentSection(element) {
        while (element && element !== document) {
            if (element.classList && element.classList.contains('report-section')) { return element; }
            element = element.parentNode;
        }
        return null;
    }
    function openParents(element) {
        while (element && element !== document) {
            if (element.tagName === 'DETAILS') { element.open = true; }
            element = element.parentNode;
        }
    }
    function indexById(id) {
        for (var i=0; i<buttons.length; i++) { if (buttons[i].getAttribute('data-section') === id) { return i; } }
        return -1;
    }
    function keepNavButtonVisible(button) {
        if (!nav || !button || nav.offsetParent === null || nav.clientHeight === 0) { return; }
        var top = button.offsetTop, bottom = top + button.offsetHeight;
        if (top < nav.scrollTop + 8) { nav.scrollTop = Math.max(0, top - 24); }
        else if (bottom > nav.scrollTop + nav.clientHeight - 8) { nav.scrollTop = bottom - nav.clientHeight + 24; }
    }
    function markSubsectionLink(anchor) {
        var links = document.querySelectorAll('.subsection-nav-button');
        for (var k=0; k<links.length; k++) { links[k].classList.toggle('active', !!anchor && links[k].getAttribute('data-anchor') === anchor); }
    }
    function activate(index, updateHash, anchor) {
        if (!buttons.length) { return; }
        currentIndex = Math.max(0, Math.min(index, buttons.length-1));
        var selectedId = buttons[currentIndex].getAttribute('data-section');
        for (var i=0; i<sections.length; i++) {
            var selected = sections[i].id === selectedId;
            sections[i].classList.toggle('active', selected);
            sections[i].setAttribute('aria-hidden', selected ? 'false' : 'true');
        }
        for (var j=0; j<buttons.length; j++) {
            var active = j === currentIndex;
            buttons[j].classList.toggle('active', active);
            buttons[j].setAttribute('aria-current', active ? 'page' : 'false');
            buttons[j].parentNode.classList.toggle('active', active);
        }
        markSubsectionLink(anchor);
        byId('section-label').textContent = buttons[currentIndex].getAttribute('data-title') + ' / ' + (currentIndex+1) + ' of ' + buttons.length;
        byId('previous-section').disabled = currentIndex === 0;
        byId('next-section').disabled = currentIndex === buttons.length-1;
        if (updateHash) {
            try { window.history.replaceState(null, '', '#' + (anchor || selectedId)); }
            catch (error) { window.location.hash = anchor || selectedId; }
        }
        if (anchor) {
            var target = byId(anchor);
            if (target) { openParents(target); target.scrollIntoView(true); }
        } else if (content) { content.scrollTop = 0; }
        root.classList.remove('nav-open');
        byId('toggle-report-nav').setAttribute('aria-expanded', 'false');
        keepNavButtonVisible(buttons[currentIndex]);
    }
    function revealElement(element) {
        var section = parentSection(element);
        if (!section) { return; }
        var panel=element.closest&&element.closest('details.dataset');
        if(panel&&panel.clearTableFilter){panel.clearTableFilter();}
        var index = indexById(section.id);
        if (index >= 0 && index !== currentIndex) { activate(index, false, ''); }
        openParents(element);
        try { element.scrollIntoView({block:'center',inline:'nearest'}); }
        catch (error) { element.scrollIntoView(true); }
    }
    function revealId(id) {
        var element = byId(id);
        if (!element) { return; }
        var section = parentSection(element);
        if (section) { activate(indexById(section.id), true, element === section ? '' : id); }
    }

    // Search -------------------------------------------------------------
    function clearSearch() {
        var marks = document.querySelectorAll('mark.report-match');
        for (var i=0; i<marks.length; i++) {
            var parent = marks[i].parentNode;
            parent.replaceChild(document.createTextNode(marks[i].textContent), marks[i]);
            parent.normalize();
        }
        matches = []; matchIndex = -1; lastQuery = '';
        root.classList.remove('search-active');
        byId('report-search-prev').disabled = true;
        byId('report-search-next').disabled = true;
        byId('report-search-message').textContent = 'Search every section, including collapsed details.';
    }
    function showMatch(index) {
        if (!matches.length) { return; }
        if (matchIndex >= 0) { matches[matchIndex].classList.remove('report-current'); }
        matchIndex = (index + matches.length) % matches.length;
        matches[matchIndex].classList.add('report-current');
        revealElement(matches[matchIndex]);
        var section = parentSection(matches[matchIndex]), button = section ? buttons[indexById(section.id)] : null;
        byId('report-search-message').textContent = 'Match ' + (matchIndex+1) + ' of ' + matches.length + ' across the full report' + (button ? ' \u2014 ' + button.getAttribute('data-title') : '') + '.';
    }
    function search() {
        clearSearch();
        var query = byId('report-search-input').value.replace(/^\s+|\s+$/g, '');
        if (!query) { return; }
        root.classList.add('search-active'); lastQuery = query;
        var lowerQuery = query.toLowerCase(), nodes = [];
        for (var i=0; i<sections.length; i++) {
            var walker = document.createTreeWalker(sections[i], NodeFilter.SHOW_TEXT, {acceptNode:function(node) {
                var tag = node.parentNode && node.parentNode.nodeName;
                if (node.parentElement.closest('.dataset-tools,.section-actions,.section-toolbar,.section-overview,.copy-feedback') || tag === 'SCRIPT' || tag === 'STYLE' || (tag === 'BUTTON' && !node.parentElement.classList.contains('table-sort')) || tag === 'MARK') { return NodeFilter.FILTER_REJECT; }
                return node.nodeValue.toLowerCase().indexOf(lowerQuery) >= 0 ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT;
            }}, false);
            var node; while ((node = walker.nextNode())) { nodes.push(node); }
        }
        for (var n=0; n<nodes.length; n++) {
            var text = nodes[n].nodeValue, lower = text.toLowerCase(), offset=0, at=lower.indexOf(lowerQuery), fragment=document.createDocumentFragment();
            while (at >= 0) {
                if (at > offset) { fragment.appendChild(document.createTextNode(text.substring(offset,at))); }
                var mark = document.createElement('mark'); mark.className='report-match';
                mark.appendChild(document.createTextNode(text.substr(at,query.length))); fragment.appendChild(mark);
                matches.push(mark); offset=at+query.length; at=lower.indexOf(lowerQuery,offset);
            }
            if (offset < text.length) { fragment.appendChild(document.createTextNode(text.substring(offset))); }
            nodes[n].parentNode.replaceChild(fragment,nodes[n]);
        }
        if (!matches.length) { byId('report-search-message').textContent='No matches for "' + query + '".'; return; }
        byId('report-search-prev').disabled=false; byId('report-search-next').disabled=false; showMatch(0);
    }

    // Print: every collapsible panel (datasets and "No data" groups) opens.
    function preparePrint() {
        if (printState) { return; }
        printState = toArray(document.querySelectorAll('details')).map(function(detail) { return [detail, detail.open]; });
        for (var i=0; i<printState.length; i++) { printState[i][0].open=true; }
    }
    function restorePrint() {
        if (!printState) { return; }
        for (var i=0; i<printState.length; i++) { printState[i][0].open=printState[i][1]; }
        printState=null;
        root.classList.remove('print-section');
    }

    // Right-align columns whose populated cells are all numeric (counts, sizes,
    // percentages). Presentation only: cell text is never changed.
    var numericCell = /^[-+]?(\d{1,3}(,\d{3})+|\d+)(\.\d+)?\s?(%|[KMGT]i?B|bytes|ms|s|sec)?$/i;
    function alignNumericColumns(table) {
        var head = table.tHead && table.tHead.rows[0], body = table.tBodies[0];
        if (!head || !body || !body.rows.length) { return; }
        for (var c=0; c<head.cells.length; c++) {
            if (/version|build|\bid\b|year|phone|port/i.test(head.cells[c].getAttribute('data-original-label') || head.cells[c].textContent)) { continue; }
            var seen = 0, numeric = true;
            for (var r=0; r<body.rows.length && numeric; r++) {
                var cell = body.rows[r].cells[c]; if (!cell) { continue; }
                var value = cell.textContent.replace(/^\s+|\s+$/g, '');
                if (!value) { continue; }
                seen++; numeric = numericCell.test(value);
            }
            if (seen && numeric) {
                head.cells[c].classList.add('num');
                for (var n=0; n<body.rows.length; n++) { if (body.rows[n].cells[c]) { body.rows[n].cells[c].classList.add('num'); } }
            }
        }
    }
    function issueCounts(container) {
        return {errors:container.querySelectorAll('td .status-error').length, warnings:container.querySelectorAll('td .status-warning').length};
    }
    function issueDescription(counts) {
        return (counts.errors ? counts.errors + (counts.errors === 1 ? ' failure or high-severity item' : ' failures or high-severity items') : '') + (counts.errors && counts.warnings ? ' and ' : '') + (counts.warnings ? counts.warnings + (counts.warnings === 1 ? ' warning' : ' warnings') : '');
    }
    // Count items that need attention in each section so the navigation shows
    // where to look first. Badges live in the navigation, outside searchable
    // and copyable section content.
    function addReviewBadges() {
        for (var i=0; i<buttons.length; i++) {
            var section = byId(buttons[i].getAttribute('data-section'));
            if (!section) { continue; }
            var counts = issueCounts(section), total = counts.errors + counts.warnings;
            if (!total) { continue; }
            var badge = document.createElement('span');
            badge.className = 'nav-badge' + (counts.errors ? ' nav-badge-error' : '');
            badge.textContent = String(total);
            badge.title = issueDescription(counts); badge.setAttribute('aria-label', issueDescription(counts));
            buttons[i].appendChild(badge);
        }
    }

    // Copy ---------------------------------------------------------------
    // Read DOM text, not innerText: collapsed panels, horizontal scrolling and
    // search marks must never omit or change the copied report values.
    function tableText(table) {
        return Array.prototype.map.call(table.rows,function(row) {
            return Array.prototype.map.call(row.cells,function(cell) {
                var value=cell.textContent;
                return /[\t\r\n"]/.test(value) ? '"'+value.replace(/"/g,'""')+'"' : value;
            }).join('\t');
        }).join('\r\n');
    }
    function partsText(item) {
        return Array.prototype.filter.call(item.children,function(part){return part.tagName!=='BUTTON';}).map(function(part){return part.textContent;}).join(': ');
    }
    function reportText(container) {
        var chunks=[];
        function visit(node) {
            if (node.nodeType !== 1 || node.matches('button,.dataset-tools,.section-toolbar,.section-overview,.copy-feedback,.review-links,.role-list,.health-legend,.health-bar')) { return; }
            if (node.tagName === 'TABLE') { chunks.push(tableText(node)); return; }
            if (node.tagName === 'SUMMARY') {
                var title=node.querySelector('.dataset-title'),count=node.querySelector('.dataset-count');
                chunks.push((title?title.textContent:node.textContent)+(count?' ('+count.textContent+')':''));return;
            }
            if (node.matches('.facts,.metrics')) { chunks.push(Array.prototype.map.call(node.children,partsText).join('\r\n'));return; }
            if (node.matches('.health-figure')) {
                var percent=node.querySelector('.health-percent'),caption=node.querySelector('.health-caption');
                chunks.push('Collection health: '+(percent?percent.textContent:'')+(caption?' '+caption.textContent:''));return;
            }
            if (node.matches('h1,h2,h3,h4,p,li,pre')) { chunks.push(node.textContent); return; }
            for (var i=0;i<node.children.length;i++) { visit(node.children[i]); }
        }
        visit(container); return chunks.join('\r\n\r\n');
    }
    var coverActions=document.querySelector('.cover-actions')||document.querySelector('.report-brand');
    var copyFeedback=document.createElement('div');
    copyFeedback.className='copy-feedback';copyFeedback.setAttribute('role','status');copyFeedback.setAttribute('aria-live','polite');
    var copyPanel=document.createElement('div');copyPanel.className='copy-panel';copyPanel.hidden=true;
    copyPanel.setAttribute('role','region');copyPanel.setAttribute('aria-label','Manual report copy');
    var copyHelp=document.createElement('p');copyHelp.textContent='Automatic copy is unavailable. The complete text is selected below. Press Ctrl+C (Command+C on Mac), then Close.';
    var copyArea=document.createElement('textarea');copyArea.readOnly=true;copyArea.setAttribute('aria-label','Complete report text to copy');
    var copyClose=document.createElement('button');copyClose.type='button';copyClose.className='quiet-button';copyClose.textContent='Close';
    copyPanel.appendChild(copyHelp);copyPanel.appendChild(copyArea);copyPanel.appendChild(copyClose);document.body.appendChild(copyPanel);
    var copyOrigin=null,copyRequest=0;
    function closeCopy() { copyPanel.hidden=true;copyArea.value='';if(copyOrigin){copyOrigin.focus();} }
    copyClose.addEventListener('click',closeCopy);
    copyPanel.addEventListener('keydown',function(event){if(event.key==='Escape'){event.preventDefault();closeCopy();}});
    function copyText(text,button) {
        // Keep the selected text available if either the Clipboard API or the
        // older local-file fallback is blocked by browser/iframe policy.
        copyOrigin=button;copyFeedback.textContent='';var request=++copyRequest;copyPanel.hidden=true;copyArea.value='';
        function fallback() {
            if(request!==copyRequest){return;}
            copyArea.value=text;copyPanel.hidden=false;copyArea.focus();copyArea.select();
            var copied=false;try { copied=document.execCommand('copy'); } catch(error) { /* manual copy remains available */ }
            if(copied){closeCopy();copyFeedback.textContent='Copied complete text.';}
            else{copyFeedback.textContent='Use the selected text in the manual copy panel.';}
        }
        if(navigator.clipboard && navigator.clipboard.writeText) {
            try { navigator.clipboard.writeText(text).then(function(){if(request===copyRequest){copyFeedback.textContent='Copied complete text.';}},fallback); }
            catch(error){fallback();}
        } else { fallback(); }
    }
    function copyButton(label,container,getText) {
        var button=document.createElement('button');button.type='button';button.className='quiet-button copy-action';button.textContent=label;
        button.addEventListener('click',function(){copyText(getText(),button);});container.appendChild(button);return button;
    }
    // This control remains available in the embedded viewer as well as saved HTML.
    copyButton('Copy full report',coverActions,function(){
        return document.querySelector('h1').textContent+'\r\n'+document.querySelector('.subtitle').textContent+'\r\n\r\n'+sections.map(reportText).join('\r\n\r\n');
    });
    coverActions.appendChild(copyFeedback);
    for(var cs=0;cs<sections.length;cs++) {
        (function(section){
            var actions=section.querySelector('.section-actions');
            copyButton('Copy section',actions,function(){return reportText(section);});
            var print=document.createElement('button');print.type='button';print.className='quiet-button print-action';print.textContent='Print section';
            print.addEventListener('click',function(){var index=indexById(section.id);if(index>=0){activate(index,false,'');}preparePrint();root.classList.add('print-section');window.print();});
            actions.appendChild(print);
        }(sections[cs]));
    }

    // Tables -------------------------------------------------------------
    function enhanceTable(panel,table,toolbar) {
        var rows=Array.prototype.slice.call(table.tBodies[0].rows),searchRows=null;
        var filterLabel=document.createElement('label');filterLabel.className='table-filter';filterLabel.textContent='Filter rows ';
        var filter=document.createElement('input');filter.type='search';filter.placeholder='Find in this table';filter.setAttribute('aria-label','Filter rows in '+panel.getAttribute('data-title'));
        filterLabel.appendChild(filter);toolbar.appendChild(filterLabel);
        var count=document.createElement('span');count.className='table-filter-count';count.setAttribute('role','status');count.setAttribute('aria-live','polite');toolbar.appendChild(count);
        function applyFilter() {
            var query=filter.value.trim().toLowerCase(),shown=0;
            if(query&&!searchRows){searchRows=rows.map(function(row){return row.textContent.toLowerCase();});}
            for(var i=0;i<rows.length;i++){rows[i].hidden=!!query&&searchRows[i].indexOf(query)<0;if(!rows[i].hidden){shown++;}}
            count.textContent=query?shown+' of '+rows.length+' rows shown. Copy table includes all rows.':'';
        }
        filter.addEventListener('input',applyFilter);
        panel.clearTableFilter=function(){if(filter.value){filter.value='';applyFilter();}};
        var headers=Array.prototype.slice.call(table.tHead.rows[0].cells),sortColumn=-1,sortDirection=0;
        var collator=typeof Intl!=='undefined'&&Intl.Collator?new Intl.Collator(undefined,{numeric:true,sensitivity:'base'}):null;
        headers.forEach(function(header,index){
            var label=header.textContent,button=document.createElement('button');button.type='button';button.className='table-sort';button.textContent=label;
            button.setAttribute('aria-label','Sort by '+label);header.textContent='';header.appendChild(button);header.setAttribute('aria-sort','none');
            button.addEventListener('click',function(){
                sortDirection=sortColumn===index?(sortDirection+1)%3:1;sortColumn=index;
                headers.forEach(function(h,i){h.setAttribute('aria-sort',i===index&&sortDirection?(sortDirection===1?'ascending':'descending'):'none');});
                var ordered=rows.map(function(row,original){return {row:row,original:original,value:row.cells[index].textContent};});
                if(sortDirection){ordered.sort(function(a,b){var result=collator?collator.compare(a.value,b.value):a.value.localeCompare(b.value);return (sortDirection===1?result:-result)||a.original-b.original;});}
                var fragment=document.createDocumentFragment();ordered.forEach(function(item){fragment.appendChild(item.row);});table.tBodies[0].appendChild(fragment);
            });
        });
    }
    function enhanceDataset(panel) {
        var body=panel.querySelector('.dataset-body'),table=panel.querySelector('table'),pre=panel.querySelector('pre');
        var toolbar=document.createElement('div');toolbar.className='dataset-tools';body.insertBefore(toolbar,body.firstChild);
        copyButton(table?'Copy table': 'Copy text',toolbar,function(){return table?tableText(table):pre?pre.textContent:reportText(body);});
        if(!table&&!pre&&panel.querySelector('.empty-data')){panel.classList.add('dataset-empty');}
        if(!table){return;}
        var rows=toArray(table.tBodies[0].rows),labels=toArray(table.tHead.rows[0].cells).map(function(cell){return cell.getAttribute('data-original-label')||cell.textContent;});
        // Two-column name/value lists are styled as property sheets (still compact
        // rows with optional wrapping); a single wide record is transposed visually.
        // Table rows, cells and order are unchanged.
        if(labels.length===2&&/^value$/i.test(labels[1])){table.classList.add('kv-table');panel.classList.add('dataset-properties');}
        else if(rows.length===1&&labels.length>=3){table.classList.add('record-table');panel.classList.add('dataset-properties');table.style.gridTemplateRows='repeat('+labels.length+', auto)';}
        alignNumericColumns(table);
        var counts=issueCounts(panel);
        // Header flags are buttons: each click opens the dataset and jumps to the
        // next failed/high-severity row (red) or warning row (amber). Their labels
        // are CSS-generated so search and copy keep returning only source data.
        var summaryCount=panel.querySelector('summary .dataset-count');
        [['error',counts.errors,' error',' errors',{errors:counts.errors,warnings:0}],['warning',counts.warnings,' warning',' warnings',{errors:0,warnings:counts.warnings}]].forEach(function(kind){
            if(!kind[1]){return;}
            var flag=document.createElement('button'),cursor=-1;flag.type='button';flag.className='dataset-flag dataset-flag-'+kind[0];
            var about=issueDescription(kind[4]);
            flag.setAttribute('data-label',kind[1]+(kind[1]===1?kind[2]:kind[3]));flag.title=about+'. Click to jump to '+(kind[1]===1?'it':'the next one')+'.';flag.setAttribute('aria-label',about+'. Jump to '+(kind[1]===1?'it':'the next one')+'.');
            flag.addEventListener('click',function(event){
                // Inside <summary>: do not let the click toggle the dataset closed.
                event.preventDefault();event.stopPropagation();
                var cells=panel.querySelectorAll('td .status-'+kind[0]);if(!cells.length){return;}
                if(panel.clearTableFilter){panel.clearTableFilter();}
                openParents(panel);
                var marked=panel.querySelectorAll('tr.issue-focus');for(var m=0;m<marked.length;m++){marked[m].classList.remove('issue-focus');}
                cursor=(cursor+1)%cells.length;
                var row=cells[cursor].closest('tr');row.classList.add('issue-focus');
                try { row.scrollIntoView({block:'center',inline:'nearest'}); } catch (error) { row.scrollIntoView(true); }
            });
            summaryCount.parentNode.insertBefore(flag,summaryCount);
        });
        if(table.classList.contains('record-table')){return;}
        var wrap=document.createElement('button');wrap.type='button';wrap.className='quiet-button';wrap.textContent='Wrap values';wrap.setAttribute('aria-pressed','false');
        wrap.addEventListener('click',function(){var on=panel.classList.toggle('wrap-values');wrap.setAttribute('aria-pressed',String(on));wrap.textContent=on?'Compact rows':'Wrap values';});toolbar.appendChild(wrap);
        var hint=document.createElement('span');hint.className='table-hint';hint.textContent='Select text to copy a value. Copy table includes all rows.';toolbar.appendChild(hint);
        if(rows.length>1){enhanceTable(panel,table,toolbar);}
    }
    for(var cd=0;cd<details.length;cd++) { enhanceDataset(details[cd]); }
    for (var d=0; d<details.length; d++) { details[d].open=details[d].getAttribute('data-default-open') === 'true'; }

    // Hierarchy ----------------------------------------------------------
    // Put actionable findings in the summary's attention panel without
    // changing the underlying blocks or their original section/anchor identities.
    var summarySection=byId(summaryId);
    if(summarySection){
        var attentionItems=summarySection.querySelector('.attention-items');
        var priorityPanels=summarySection.querySelectorAll('.dataset-priority');
        for(var p=0;attentionItems&&p<priorityPanels.length;p++){ attentionItems.appendChild(priorityPanels[p]); }
    }
    // Datasets that returned nothing move into one quiet group per section or
    // subsection. They stay in the DOM for search, copy and print.
    var defaultEmptyMessage='No data was collected or the component is not present.';
    function groupEmptyDatasets(container) {
        var empties=toArray(container.children).filter(function(child){return child.tagName==='DETAILS'&&child.classList.contains('dataset-empty');});
        if(!empties.length){return;}
        absentCount++;
        var group=document.createElement('details');group.className='absent-group';group.id='no-data-'+absentCount;
        var summary=document.createElement('summary');
        var title=document.createElement('span');title.className='dataset-title';title.textContent='No data collected';
        var count=document.createElement('span');count.className='dataset-count';count.textContent=plural(empties.length,'dataset','datasets');
        summary.appendChild(title);summary.appendChild(count);group.appendChild(summary);
        var body=document.createElement('div');body.className='absent-body';
        var note=document.createElement('p');note.className='absent-note';
        note.textContent='These datasets were checked but returned no rows. The component may be absent or unconfigured, or its collector may have failed; see the collection results.';
        body.appendChild(note);
        var list=document.createElement('div');list.className='absent-list';
        // Only explanations that differ from the standard "no data" text start open.
        empties.forEach(function(panel){var message=panel.querySelector('.empty-data');panel.open=!!message&&message.textContent!==defaultEmptyMessage;list.appendChild(panel);});
        body.appendChild(list);group.appendChild(body);container.appendChild(group);
    }
    function addOverviewItem(list,panel) {
        var item=document.createElement('li'),button=document.createElement('button'),counts=issueCounts(panel);
        button.type='button';button.className='overview-item'+(counts.errors?' has-error':(counts.warnings?' has-warning':''));
        button.setAttribute('data-anchor',panel.id);button.textContent=panel.getAttribute('data-title');
        var countText=panel.querySelector('summary .dataset-count').textContent.replace(/\d{4,}/g,formatNumber);
        button.setAttribute('data-count',countText);button.setAttribute('aria-label',panel.getAttribute('data-title')+', '+countText+(counts.errors||counts.warnings?', '+issueDescription(counts):''));
        item.appendChild(button);list.appendChild(item);
    }
    function buildOverview(section) {
        var panels=toArray(section.querySelectorAll('details.dataset'));
        var withData=panels.filter(function(panel){return !panel.classList.contains('dataset-empty');});
        var rows=0;withData.forEach(function(panel){var table=panel.querySelector('table');if(table){rows+=table.tBodies[0].rows.length;}});
        var stats=section.querySelector('.section-stats');
        if(stats&&panels.length){
            var parts=[plural(panels.length,'dataset','datasets')];
            if(withData.length!==panels.length){parts.push(formatNumber(withData.length)+' with data');}
            if(rows){parts.push(plural(rows,'table row','table rows'));}
            var counts=issueCounts(section);if(counts.errors+counts.warnings){parts.push(formatNumber(counts.errors+counts.warnings)+' to review');}
            stats.setAttribute('data-stats',parts.join(' \u00b7 '));
        }
        if(section.id===summaryId||panels.length<4){return;}
        var overview=document.createElement('nav');overview.className='section-overview';overview.setAttribute('aria-label','In this section');
        var containers=[section].concat(toArray(section.querySelectorAll('.subsection-group')));
        containers.forEach(function(container){
            var heading=container===section?null:container.querySelector('.subsection-anchor');
            var owned=panels.filter(function(panel){var group=panel.closest('.subsection-group');return container===section?!group:group===container;});
            var filled=owned.filter(function(panel){return !panel.classList.contains('dataset-empty');});
            if(!owned.length&&!heading){return;}
            var block=document.createElement('div');block.className='overview-group';
            if(heading){var title=document.createElement('button');title.type='button';title.className='overview-group-title';title.setAttribute('data-anchor',heading.id);title.textContent=cleanTitle(heading.textContent);block.appendChild(title);}
            var list=document.createElement('ul');list.className='overview-list';
            filled.forEach(function(panel){addOverviewItem(list,panel);});
            var absent=null;for(var c=0;c<container.children.length;c++){if(container.children[c].classList.contains('absent-group')){absent=container.children[c];}}
            if(absent&&owned.length>filled.length){
                var item=document.createElement('li'),button=document.createElement('button');button.type='button';button.className='overview-item overview-absent';
                button.setAttribute('data-anchor',absent.id);button.textContent=plural(owned.length-filled.length,'dataset','datasets')+' with no data';
                item.appendChild(button);list.appendChild(item);
            }
            if(list.children.length){block.appendChild(list);}
            if(block.children.length){overview.appendChild(block);}
        });
        if(!overview.children.length){return;}
        if(overview.children.length===1){overview.classList.add('section-overview-single');}
        var toolbar=section.querySelector('.section-toolbar');
        toolbar.parentNode.insertBefore(overview,toolbar.nextSibling);
    }
    for(var gs=0;gs<sections.length;gs++){
        var section=sections[gs];
        groupEmptyDatasets(section);
        var groups=section.querySelectorAll('.subsection-group');
        for(var gg=0;gg<groups.length;gg++){groupEmptyDatasets(groups[gg]);}
        buildOverview(section);
    }

    // Navigation and interaction -----------------------------------------
    for (var b=0; b<buttons.length; b++) {
        (function(index) { buttons[index].addEventListener('click',function() { activate(index,true,''); }); }(b));
    }
    document.addEventListener('click',function(event) {
        var target=event.target&&event.target.closest?event.target.closest('[data-anchor]'):null;
        if(!target||target.tagName!=='BUTTON'){return;}
        event.preventDefault();revealId(target.getAttribute('data-anchor'));
    });
    var expandButtons = document.querySelectorAll('[data-details-action]');
    for (var e=0; e<expandButtons.length; e++) {
        (function(button) { button.addEventListener('click',function() {
            var section=parentSection(button), panels=section.querySelectorAll('details');
            for (var i=0; i<panels.length; i++) { panels[i].open=button.getAttribute('data-details-action') === 'expand'; }
        }); }(expandButtons[e]));
    }
    byId('previous-section').addEventListener('click',function() { activate(currentIndex-1,true,''); });
    byId('next-section').addEventListener('click',function() { activate(currentIndex+1,true,''); });
    byId('toggle-report-nav').addEventListener('click',function() {
        var open=root.classList.toggle('nav-open'); this.setAttribute('aria-expanded',open ? 'true' : 'false');
    });
    byId('report-search-form').addEventListener('submit',function(event) {
        event.preventDefault();
        var query = byId('report-search-input').value.replace(/^\s+|\s+$/g, '');
        if (query && query === lastQuery && matches.length) { showMatch(matchIndex+1); } else { search(); }
    });
    byId('report-search-input').addEventListener('keydown',function(event) {
        if (event.key === 'Enter' && event.shiftKey && matches.length) { event.preventDefault(); showMatch(matchIndex-1); }
        else if (event.key === 'Escape' && (this.value || matches.length)) { event.preventDefault(); event.stopPropagation(); clearSearch(); this.value=''; }
    });
    function isTyping(target) { var tag=(target&&target.nodeName||'').toLowerCase(); return tag==='input'||tag==='textarea'||tag==='select'||!!(target&&target.isContentEditable); }
    document.addEventListener('keydown',function(event) {
        if (event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey || isTyping(event.target) || !copyPanel.hidden) { return; }
        var input = byId('report-search-input');
        if (event.key === '/' && input.offsetParent !== null) { event.preventDefault(); input.focus(); input.select(); }
        else if (event.key === ']') { event.preventDefault(); activate(currentIndex+1,true,''); }
        else if (event.key === '[') { event.preventDefault(); activate(currentIndex-1,true,''); }
    });
    // Highlight the subsection currently at the top of the reading pane.
    var spyPending=false;
    function updateSpy() {
        spyPending=false;
        if(!content||!buttons.length){return;}
        var links=buttons[currentIndex].parentNode.querySelectorAll('.subsection-nav-button'),limit=content.getBoundingClientRect().top+96,best=null,bestTop=-Infinity;
        if(content.scrollTop<8){markSubsectionLink('');return;}
        for(var i=0;i<links.length;i++){
            var target=byId(links[i].getAttribute('data-anchor'));
            if(!target||target.offsetParent===null){continue;}
            var top=target.getBoundingClientRect().top;
            if(top<=limit&&top>bestTop){best=links[i];bestTop=top;}
        }
        markSubsectionLink(best?best.getAttribute('data-anchor'):'');
    }
    // One floating control copies the single value under the pointer. It lives
    // outside the tables, so cell text, search and Copy table are unchanged.
    var copyLayout=document.querySelector('.report-layout');
    if (copyLayout && content) {
        var cellCopy=document.createElement('button'),cellTarget=null;
        cellCopy.type='button';cellCopy.className='cell-copy';cellCopy.textContent='Copy';cellCopy.hidden=true;cellCopy.setAttribute('aria-label','Copy this value');
        copyLayout.appendChild(cellCopy);
        var hideCellCopy=function(){cellCopy.hidden=true;cellTarget=null;};
        copyLayout.addEventListener('mouseover',function(event){
            if(event.target===cellCopy){return;}
            var cell=event.target.closest?event.target.closest('tbody td,.fact-value'):null;
            if(!cell||!cell.textContent.replace(/\s+/g,'')){hideCellCopy();return;}
            var box=cell.getBoundingClientRect(),frame=copyLayout.getBoundingClientRect();
            cellTarget=cell;cellCopy.style.top=(box.top-frame.top+1)+'px';
            cellCopy.style.left=Math.max(0,Math.min(box.right,frame.right-18)-frame.left-48)+'px';cellCopy.hidden=false;
        });
        copyLayout.addEventListener('mouseleave',hideCellCopy);
        content.addEventListener('scroll',hideCellCopy,{passive:true});
        cellCopy.addEventListener('click',function(){if(cellTarget){copyText(cellTarget.textContent,cellCopy);}});
    }
    if (content) {
        var toTop=document.createElement('button');toTop.type='button';toTop.className='back-to-top';toTop.textContent='Back to top';toTop.hidden=true;
        toTop.addEventListener('click',function(){content.scrollTop=0;var heading=document.querySelector('.report-section.active .section-title');if(heading){heading.setAttribute('tabindex','-1');heading.focus({preventScroll:true});}});
        document.querySelector('.report-layout').appendChild(toTop);
        content.addEventListener('scroll',function(){toTop.hidden=content.scrollTop<400;if(!spyPending){spyPending=true;window.requestAnimationFrame(updateSpy);}},{passive:true});
    }
    byId('report-search-prev').addEventListener('click',function() { showMatch(matchIndex-1); });
    byId('report-search-next').addEventListener('click',function() { showMatch(matchIndex+1); });
    byId('report-search-clear').addEventListener('click',function() { clearSearch(); byId('report-search-input').value=''; });
    byId('report-print').addEventListener('click',function() { window.print(); });
    window.addEventListener('beforeprint',preparePrint); window.addEventListener('afterprint',restorePrint);
    window.addEventListener('hashchange',function() { revealId(window.location.hash.substring(1)); });
    // The native viewer uses this to expose search hits inside collapsed panels.
    window.SICMReport = {revealElement:revealElement,preparePrint:preparePrint,restorePrint:restorePrint};
    addReviewBadges();
    root.classList.add('report-js');
    var requested = window.location.hash.substring(1);
    if (requested && byId(requested)) { revealId(requested); }
    else { activate(indexById(summaryId),false,''); }
}());
