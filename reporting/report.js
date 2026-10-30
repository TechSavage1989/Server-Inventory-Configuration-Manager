(function () {
    'use strict';
    var root = document.documentElement;
    var content = document.querySelector('.report-content');
    var sections = Array.prototype.slice.call(document.querySelectorAll('.report-section'));
    var buttons = Array.prototype.slice.call(document.querySelectorAll('.section-main-button'));
    var details = Array.prototype.slice.call(document.querySelectorAll('details.dataset'));
    var currentIndex = 0, matches = [], matchIndex = -1, printState = null;
    function byId(id) { return document.getElementById(id); }
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
        var links = document.querySelectorAll('.subsection-nav-button');
        for (var k=0; k<links.length; k++) { links[k].classList.toggle('active', links[k].getAttribute('data-anchor') === anchor); }
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
    }
    function revealElement(element) {
        var section = parentSection(element);
        if (!section) { return; }
        var panel=element.closest&&element.closest('details.dataset');
        if(panel&&panel.clearTableFilter){panel.clearTableFilter();}
        var index = indexById(section.id);
        if (index >= 0) { activate(index, false, ''); }
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
    function clearSearch() {
        var marks = document.querySelectorAll('mark.report-match');
        for (var i=0; i<marks.length; i++) {
            var parent = marks[i].parentNode;
            parent.replaceChild(document.createTextNode(marks[i].textContent), marks[i]);
            parent.normalize();
        }
        matches = []; matchIndex = -1;
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
        byId('report-search-message').textContent = 'Match ' + (matchIndex+1) + ' of ' + matches.length + ' across the full report.';
    }
    function search() {
        clearSearch();
        var query = byId('report-search-input').value.replace(/^\s+|\s+$/g, '');
        if (!query) { return; }
        root.classList.add('search-active');
        var lowerQuery = query.toLowerCase(), nodes = [];
        for (var i=0; i<sections.length; i++) {
            var walker = document.createTreeWalker(sections[i], NodeFilter.SHOW_TEXT, {acceptNode:function(node) {
                var tag = node.parentNode && node.parentNode.nodeName;
                if (node.parentElement.closest('.dataset-tools,.section-actions,.copy-feedback') || tag === 'SCRIPT' || tag === 'STYLE' || (tag === 'BUTTON' && !node.parentElement.classList.contains('table-sort')) || tag === 'MARK') { return NodeFilter.FILTER_REJECT; }
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
    function preparePrint() {
        if (printState) { return; }
        printState = details.map(function(detail) { return detail.open; });
        for (var i=0; i<details.length; i++) { details[i].open=true; }
    }
    function restorePrint() {
        if (!printState) { return; }
        for (var i=0; i<details.length; i++) { details[i].open=printState[i]; }
        printState=null;
    }
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
    function reportText(container) {
        var chunks=[];
        function visit(node) {
            if (node.nodeType !== 1 || node.matches('button,.dataset-tools,.section-actions,.copy-feedback,.review-links')) { return; }
            if (node.tagName === 'TABLE') { chunks.push(tableText(node)); return; }
            if (node.tagName === 'SUMMARY') { chunks.push(node.querySelector('.dataset-title').textContent+' ('+node.querySelector('.dataset-count').textContent+')'); return; }
            if (node.matches('.facts,.metrics')) {
                chunks.push(Array.prototype.map.call(node.children,function(item){return Array.prototype.map.call(item.children,function(part){return part.textContent;}).join(': ');}).join('\r\n'));return;
            }
            if (node.matches('h1,h2,h3,h4,p,li,pre')) { chunks.push(node.textContent); return; }
            for (var i=0;i<node.children.length;i++) { visit(node.children[i]); }
        }
        visit(container); return chunks.join('\r\n\r\n');
    }
    var copyFeedback=document.createElement('div');
    copyFeedback.className='copy-feedback';copyFeedback.setAttribute('role','status');copyFeedback.setAttribute('aria-live','polite');
    document.querySelector('.report-brand').appendChild(copyFeedback);
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
    for(var cs=0;cs<sections.length;cs++) {
        (function(section){copyButton('Copy section',section.querySelector('.section-actions'),function(){return reportText(section);});}(sections[cs]));
    }
    // This control remains available in the embedded viewer as well as saved HTML.
    copyButton('Copy full report',document.querySelector('.report-brand'),function(){
        return document.querySelector('h1').textContent+'\r\n'+document.querySelector('.subtitle').textContent+'\r\n\r\n'+sections.map(reportText).join('\r\n\r\n');
    });
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
    for(var cd=0;cd<details.length;cd++) {
        (function(panel){
            var body=panel.querySelector('.dataset-body'),table=panel.querySelector('table'),pre=panel.querySelector('pre');
            var toolbar=document.createElement('div');toolbar.className='dataset-tools';body.insertBefore(toolbar,body.firstChild);
            copyButton(table?'Copy table': 'Copy text',toolbar,function(){return table?tableText(table):pre?pre.textContent:reportText(body);});
            if(table){
                var wrap=document.createElement('button');wrap.type='button';wrap.className='quiet-button';wrap.textContent='Wrap values';wrap.setAttribute('aria-pressed','false');
                wrap.addEventListener('click',function(){var on=panel.classList.toggle('wrap-values');wrap.setAttribute('aria-pressed',String(on));wrap.textContent=on?'Compact rows':'Wrap values';});toolbar.appendChild(wrap);
                var hint=document.createElement('span');hint.className='table-hint';hint.textContent='Select text to copy a value. Scroll sideways for wide tables. Copy table includes all rows.';toolbar.appendChild(hint);
                enhanceTable(panel,table,toolbar);
            }
        }(details[cd]));
    }
    for (var d=0; d<details.length; d++) { details[d].open=details[d].getAttribute('data-default-open') === 'true'; }
    // Put actionable findings ahead of routine datasets without changing the
    // underlying blocks or discarding their original section/anchor identities.
    var summarySection=byId(root.getAttribute('data-default-section'));
    if(summarySection){
        var cursor=summarySection.querySelector('.section-actions');
        var priorityPanels=summarySection.querySelectorAll('.dataset-priority');
        for(var p=0;cursor&&p<priorityPanels.length;p++){
            cursor.parentNode.insertBefore(priorityPanels[p],cursor.nextSibling);cursor=priorityPanels[p];
        }
    }
    for (var b=0; b<buttons.length; b++) {
        (function(index) { buttons[index].addEventListener('click',function() { activate(index,true,''); }); }(b));
    }
    var jumpButtons = document.querySelectorAll('[data-anchor]');
    for (var j=0; j<jumpButtons.length; j++) {
        (function(button) { button.addEventListener('click',function() { revealId(button.getAttribute('data-anchor')); }); }(jumpButtons[j]));
    }
    var expandButtons = document.querySelectorAll('[data-details-action]');
    for (var e=0; e<expandButtons.length; e++) {
        (function(button) { button.addEventListener('click',function() {
            var section=parentSection(button), panels=section.querySelectorAll('details.dataset');
            for (var i=0; i<panels.length; i++) { panels[i].open=button.getAttribute('data-details-action') === 'expand'; }
        }); }(expandButtons[e]));
    }
    byId('previous-section').addEventListener('click',function() { activate(currentIndex-1,true,''); });
    byId('next-section').addEventListener('click',function() { activate(currentIndex+1,true,''); });
    byId('toggle-report-nav').addEventListener('click',function() {
        var open=root.classList.toggle('nav-open'); this.setAttribute('aria-expanded',open ? 'true' : 'false');
    });
    byId('report-search-form').addEventListener('submit',function(event) { event.preventDefault(); search(); });
    byId('report-search-prev').addEventListener('click',function() { showMatch(matchIndex-1); });
    byId('report-search-next').addEventListener('click',function() { showMatch(matchIndex+1); });
    byId('report-search-clear').addEventListener('click',function() { clearSearch(); byId('report-search-input').value=''; });
    byId('report-print').addEventListener('click',function() { window.print(); });
    window.addEventListener('beforeprint',preparePrint); window.addEventListener('afterprint',restorePrint);
    window.addEventListener('hashchange',function() { revealId(window.location.hash.substring(1)); });
    // The native viewer uses this to expose search hits inside collapsed panels.
    window.SICMReport = {revealElement:revealElement,preparePrint:preparePrint,restorePrint:restorePrint};
    root.classList.add('report-js');
    var requested = window.location.hash.substring(1);
    if (requested && byId(requested)) { revealId(requested); }
    else { activate(indexById(root.getAttribute('data-default-section')),false,''); }
}());
