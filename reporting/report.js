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
                if (tag === 'SCRIPT' || tag === 'STYLE' || tag === 'BUTTON' || tag === 'MARK') { return NodeFilter.FILTER_REJECT; }
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
