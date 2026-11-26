package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"html/template"
	"strconv"
	"strings"
)

// The interactive HTML discovery and readiness report (rule 44): one
// self-contained page, laid out like the application's Environment view: a
// tree of hosts, VMs, storage and networks, a page for every object, summary
// tiles that open the VMs they count, a filterable VM list for any target
// and every issue with its fix. The page is written in full by the server,
// so it reads, searches and prints without its script; the script (allowed
// by its exact hash, never 'unsafe-inline') only adds navigation, filters,
// tabs and sorting. It makes no request of any kind.

const vmReportStyle = `:root{--ink:#172033;--muted:#52647c;--line:#dbe3ed;--soft:#f4f6fa;--brand:#2563eb;--brand-ink:#0f2a5c;--ready:#1d6b43;--ready-bg:#e6f6ee;--ready-bar:#2f9e63;--review:#7a5410;--review-bg:#fff4dc;--review-bar:#e0a526;--blocked:#8c2626;--blocked-bg:#fde8e8;--blocked-bar:#d64545;--skip:#52647c;--skip-bg:#edf1f7;--skip-bar:#a3b1c2}
*{box-sizing:border-box}html{scroll-padding-top:70px}body{margin:0;background:var(--soft);color:var(--ink);font:14px/1.45 "Segoe UI",Arial,sans-serif}
a{color:#1d4ed8;text-decoration:none}a:hover{text-decoration:underline}
.top{background:linear-gradient(90deg,#14274a,#1f3f7a);color:#fff;padding:12px 20px;display:flex;align-items:center;gap:14px;flex-wrap:wrap}
.top .mark{width:34px;height:34px;border-radius:9px;background:rgba(255,255,255,.14);display:flex;align-items:center;justify-content:center;flex:none}
.top h1{font-size:19px;margin:0;font-weight:650}.top .sub{font-size:12px;color:#c9d6ee;margin:2px 0 0}
.top .tools{margin-left:auto;display:none;gap:8px;align-items:center}.js .top .tools{display:flex}
.top input{width:260px;max-width:52vw;padding:7px 10px;border-radius:8px;border:1px solid rgba(255,255,255,.35);background:rgba(255,255,255,.12);color:#fff;font:inherit}.top input::placeholder{color:#c9d6ee}
.btn{border:1px solid var(--line);background:#fff;color:var(--ink);border-radius:8px;padding:6px 12px;font:inherit;font-size:13px;cursor:pointer}.btn:hover{border-color:var(--brand)}.top .btn{background:rgba(255,255,255,.12);border-color:rgba(255,255,255,.35);color:#fff}
.shell{display:block}.js .shell{display:grid;grid-template-columns:minmax(220px,290px) minmax(0,1fr)}
.side{background:#fff;border-right:1px solid var(--line);padding:10px 0}.js .side{position:sticky;top:0;height:100vh;overflow:auto}
.side h2{font-size:11px;letter-spacing:.04em;text-transform:uppercase;color:#596b80;margin:10px 14px 4px;border:0;padding:0}
.pages{list-style:none;margin:0;padding:0 6px}.pages a{display:flex;justify-content:space-between;padding:5px 10px;border-radius:6px;color:#23334b;font-size:13px}.pages a:hover{background:#eef3fb;text-decoration:none}.pages a.sel{background:#d6e4fb;color:var(--brand-ink);font-weight:650}.pages em{font-style:normal;color:#6b7c91;font-size:12px}
.views{display:none;gap:2px;padding:0 8px;margin-top:8px;border-bottom:1px solid #e0e6ee}.js .views{display:flex}
.views button{flex:1 1 0;border:0;border-bottom:2px solid transparent;background:none;font:inherit;font-size:11.5px;color:#52647c;padding:6px 2px;cursor:pointer;border-radius:6px 6px 0 0}.views button.on{color:var(--brand-ink);border-bottom-color:var(--brand);font-weight:650;background:#eef3fb}
.tree{display:none;list-style:none;margin:6px 0;padding:0}.js .tree.on{display:block}.tree ul{list-style:none;margin:0;padding:0 0 0 14px}
.tree li:not(.open)>ul{display:none}.tree li.hide{display:none}
.tr{display:flex;align-items:center;min-height:25px;padding-left:6px}
.tw{flex:none;width:18px;height:18px;border:0;background:none;color:#596b80;cursor:pointer;padding:0;font-size:10px;line-height:18px;border-radius:4px;transition:transform .12s}.tw:hover{background:#eef3fb}.open>.tr>.tw{transform:rotate(90deg)}.tw.leaf{visibility:hidden}
.tr a{display:flex;align-items:center;gap:6px;min-width:0;flex:1;padding:2px 8px 2px 2px;border-radius:5px;color:#23334b;font-size:12.5px;white-space:nowrap}.tr a:hover{background:#eef3fb;text-decoration:none}.tr a.sel{background:#d6e4fb;color:var(--brand-ink);font-weight:650;box-shadow:inset 3px 0 var(--brand)}
.tr a span{overflow:hidden;text-overflow:ellipsis}.tr a em{margin-left:auto;font-style:normal;color:#6b7c91;font-size:11px;padding-left:6px}
.ic{flex:none;width:15px;height:15px;color:#4b6587}.ic svg{width:15px;height:15px;display:block;fill:none;stroke:currentColor;stroke-width:1.6;stroke-linecap:round;stroke-linejoin:round}
.dot{flex:none;width:8px;height:8px;border-radius:50%;background:var(--skip-bar)}.dot.ready{background:var(--ready-bar)}.dot.review{background:var(--review-bar)}.dot.blocked{background:var(--blocked-bar)}
main{padding:18px 22px 40px;max-width:1500px;min-width:0}
.pg{margin-bottom:34px}.js .pg{margin-bottom:0}
.crumb{font-size:12px;color:var(--muted);margin:0 0 4px}.crumb a{color:var(--muted)}
.pg>h2,.pg>header h2{font-size:22px;margin:0 0 4px;font-weight:650;display:flex;align-items:center;gap:10px;flex-wrap:wrap}
h3{font-size:15.5px;margin:22px 0 8px}h4{font-size:13.5px;margin:14px 0 6px}
.lead{color:var(--muted);margin:0 0 14px}
.note{background:#fff;border:1px solid var(--line);border-left:4px solid var(--brand);border-radius:8px;padding:9px 13px;margin:10px 0 14px;font-size:13px}
.tiles{display:grid;grid-template-columns:repeat(auto-fill,minmax(148px,1fr));gap:10px;margin:12px 0 6px}
.tile{display:block;background:#fff;border:1px solid var(--line);border-radius:12px;padding:12px 14px;color:var(--ink);position:relative;overflow:hidden;transition:box-shadow .12s,border-color .12s}
.tile:hover{text-decoration:none;border-color:var(--brand);box-shadow:0 4px 14px rgba(37,99,235,.14)}
.tile b{display:block;font-size:26px;line-height:1.15;font-weight:700}.tile span{display:block;font-weight:600;font-size:13px}.tile small{display:block;color:var(--muted);font-size:12px;margin-top:2px}
.tile:before{content:"";position:absolute;left:0;top:0;bottom:0;width:4px;background:#c7d3e3}
.tile.ready:before{background:var(--ready-bar)}.tile.ready b{color:var(--ready)}.tile.review:before{background:var(--review-bar)}.tile.review b{color:var(--review)}.tile.blocked:before{background:var(--blocked-bar)}.tile.blocked b{color:var(--blocked)}.tile.skip:before{background:var(--skip-bar)}.tile.info:before{background:var(--brand)}
.card{background:#fff;border:1px solid var(--line);border-radius:12px;padding:14px 16px;margin:12px 0}
.card>h3:first-child{margin-top:0}
.grid2{display:grid;grid-template-columns:repeat(auto-fit,minmax(360px,1fr));gap:12px}
.bars{display:grid;gap:8px}
.bar-row{display:grid;grid-template-columns:170px minmax(0,1fr) 250px;gap:10px;align-items:center;font-size:13px}
.bar-row .lbl{font-weight:600;color:var(--ink)}.bar-row .nums{color:var(--muted);font-size:12px;white-space:nowrap}
.bar{display:flex;height:20px;border-radius:6px;overflow:hidden;background:#edf1f7}
.seg{display:block;height:100%;min-width:2px;color:#fff;font-size:11px;font-weight:700;line-height:20px;text-align:center;overflow:hidden}.seg:hover{filter:brightness(.92);text-decoration:none}
.seg.ready{background:var(--ready-bar)}.seg.review{background:var(--review-bar)}.seg.blocked{background:var(--blocked-bar)}.seg.skip{background:var(--skip-bar)}
.legend{display:flex;gap:14px;flex-wrap:wrap;font-size:12px;color:var(--muted);margin:8px 0 0}.legend i{display:inline-block;width:10px;height:10px;border-radius:3px;margin-right:5px;vertical-align:-1px}
.meter{height:8px;border-radius:5px;background:#edf1f7;overflow:hidden;margin-top:4px;min-width:80px}.meter i{display:block;height:100%;background:var(--brand);border-radius:5px}.meter i.warn{background:var(--review-bar)}.meter i.high{background:var(--blocked-bar)}
.kv{display:grid;grid-template-columns:repeat(auto-fill,minmax(220px,1fr));gap:8px 18px;margin:6px 0 4px}.kv div{font-size:13px;min-width:0;overflow-wrap:anywhere}.kv b{display:block;color:var(--muted);font-size:11.5px;font-weight:600;text-transform:uppercase;letter-spacing:.02em}
table{width:100%;border-collapse:collapse;background:#fff;font-size:12.5px}
.tw-wrap{border:1px solid var(--line);border-radius:10px;overflow:auto;margin:6px 0 12px;background:#fff}
th,td{text-align:left;vertical-align:top;padding:6px 9px;border-top:1px solid #edf1f6;overflow-wrap:anywhere}
th{background:#f0f4f9;color:#334156;border-top:0;font-weight:600;white-space:nowrap;position:sticky;top:0}
.js th.sort{cursor:pointer}.js th.sort:after{content:" \2195";color:#9aa8ba;font-size:10px}.js th.asc:after{content:" \2191";color:var(--brand)}.js th.desc:after{content:" \2193";color:var(--brand)}
tbody tr:hover td{background:#f7faff}tr.hide{display:none}
td.num,th.num{text-align:right;white-space:nowrap}
.muted{color:var(--muted)}.small{font-size:12px}
.chip{display:inline-block;padding:1px 9px;border-radius:999px;font-size:11px;font-weight:700;white-space:nowrap;line-height:18px}
.ready{}.chip.ready{background:var(--ready-bg);color:var(--ready)}.chip.review,.chip.warning{background:var(--review-bg);color:var(--review)}.chip.blocked,.chip.blocker{background:var(--blocked-bg);color:var(--blocked)}.chip.skip,.chip.none{background:var(--skip-bg);color:var(--skip)}.chip.info{background:#e8f0fe;color:#245eae}.chip.on{background:var(--ready-bg);color:var(--ready)}.chip.off{background:var(--skip-bg);color:var(--skip)}
.bar-tools{display:flex;flex-wrap:wrap;gap:8px;align-items:center;margin:10px 0}
.bar-tools label{font-size:12.5px;color:var(--muted);display:flex;gap:6px;align-items:center}
select,.tf{font:inherit;font-size:13px;padding:5px 9px;border:1px solid #c7d0dd;border-radius:7px;background:#fff;color:var(--ink)}
.tf{min-width:220px}
.fchips{display:none;gap:6px;flex-wrap:wrap}.js .fchips{display:flex}
.fchip{border:1px solid #c7d0dd;background:#fff;border-radius:999px;padding:3px 11px;font:inherit;font-size:12px;cursor:pointer;color:var(--ink)}.fchip b{font-weight:700;margin-left:4px}
.fchip.on{background:var(--brand);border-color:var(--brand);color:#fff}.fchip.ready.on{background:var(--ready-bar);border-color:var(--ready-bar)}.fchip.review.on{background:var(--review-bar);border-color:var(--review-bar)}.fchip.blocked.on{background:var(--blocked-bar);border-color:var(--blocked-bar)}.fchip.skip.on{background:var(--skip-bar);border-color:var(--skip-bar)}
.count{font-size:12px;color:var(--muted)}
.tgt{text-align:center}.vt td{overflow-wrap:normal}.vt td:first-child,.vt td:nth-child(7){white-space:nowrap}.vt td:nth-child(3){min-width:170px}.js .vt th.tgt.cur,.js .vt td.tgt.cur{background:#eef4ff}
.issue{background:#fff;border:1px solid var(--line);border-left:4px solid var(--review-bar);border-radius:10px;padding:11px 14px;margin:10px 0}.issue.blocker{border-left-color:var(--blocked-bar)}
.issue h4{margin:0 0 4px;font-size:14px;display:flex;gap:8px;align-items:baseline}.issue .fix{background:#f3f8ff;border-radius:7px;padding:7px 10px;margin:8px 0 6px;font-size:13px}.issue .fix b{color:#1d4ed8}
.idt{width:auto;min-width:50%;margin-top:4px;font-size:12.5px}.idt td{border-top:1px solid #edf1f6;padding:4px 10px 4px 0}.idt td:first-child{white-space:nowrap;padding-right:16px}
.vmlist{display:flex;flex-wrap:wrap;gap:4px 6px;font-size:12.5px}.vmlist a{background:#f0f4f9;border-radius:6px;padding:1px 8px}
.targets{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:10px}
.tcard{background:#fff;border:1px solid var(--line);border-radius:10px;padding:10px 13px}.tcard h4{margin:0 0 6px;display:flex;justify-content:space-between;align-items:center}
.tcard ul,.plain{margin:4px 0 0;padding-left:0;list-style:none}.tcard li,.plain li{margin:5px 0;font-size:12.5px}.tcard li .act{display:block;color:var(--muted);margin:2px 0 0 4px}
.tabs{display:none;gap:2px;border-bottom:1px solid var(--line);margin:14px 0 10px;flex-wrap:wrap}.js .tabs{display:flex}
.tabs button{border:0;background:none;font:inherit;font-size:13px;padding:7px 14px;color:var(--muted);cursor:pointer;border-bottom:2px solid transparent}.tabs button.on{color:var(--brand-ink);border-bottom-color:var(--brand);font-weight:650}
.pane>h3.ph{margin-top:18px}.js .pane>h3.ph{display:none}
.js .pane:not(.on){display:none}
.flash{animation:flash 1.6s ease-out}@keyframes flash{0%{box-shadow:0 0 0 3px rgba(37,99,235,.55)}100%{box-shadow:0 0 0 3px rgba(37,99,235,0)}}
details.more summary{cursor:pointer;color:#1d4ed8;font-size:12.5px;margin-top:4px}
.empty{padding:14px;color:var(--muted);text-align:center}
.foot{color:var(--muted);font-size:12px;margin-top:30px;border-top:1px solid var(--line);padding-top:10px}
@media (max-width:860px){.js .shell{grid-template-columns:1fr}.js .side{position:static;height:auto;max-height:340px;border-right:0;border-bottom:1px solid var(--line)}.bar-row{grid-template-columns:110px minmax(0,1fr)}.bar-row .nums{grid-column:2}main{padding:14px}}
@media print{body{background:#fff;font-size:11.5px}.top{background:#fff;color:var(--ink);border-bottom:2px solid var(--ink)}.top .sub{color:var(--muted)}.top .tools,.side,.tabs,.bar-tools,.fchips{display:none!important}.js .shell{display:block}.pg,.pg[hidden]{display:block!important;break-before:page}.pg:first-of-type{break-before:auto}.pane,.js .pane:not(.on){display:block!important}.js .pane>h3.ph{display:block}tr.hide,.tree li.hide{display:table-row!important}.card,.tile,.issue,.tcard,.tw-wrap{break-inside:avoid;box-shadow:none}th{position:static}.tile:hover{box-shadow:none}a{color:var(--ink)}main{max-width:none;padding:0}}`

// vmReportScript adds navigation, filters, tabs and sorting. It reads only
// the page and keeps nothing outside it.
const vmReportScript = `(function(){
var d=document,root=d.documentElement;root.className+=' js';
function all(s,c){return Array.prototype.slice.call((c||d).querySelectorAll(s));}
function one(s,c){return (c||d).querySelector(s);}
function has(el,c){return el&&el.classList&&el.classList.contains(c);}
var state={target:root.getAttribute('data-focus')||'hyperv',status:'',flag:'',view:'hosts'};
var pages=all('.pg'),side=one('.side'),title=d.title;
function pageOf(el){while(el&&!has(el,'pg')){el=el.parentNode;}return el;}
function paneOf(el){while(el&&!has(el,'pg')){if(has(el,'pane')){return el;}el=el.parentNode;}return null;}
function showPane(pg,name){var tabs=all('.tabs button',pg);if(!tabs.length){return;}if(!name){name=tabs[0].getAttribute('data-tab');}tabs.forEach(function(b){var on=b.getAttribute('data-tab')===name;b.classList.toggle('on',on);b.setAttribute('aria-selected',on?'true':'false');});all('.pane',pg).forEach(function(p){p.classList.toggle('on',p.getAttribute('data-pane')===name);});}
all('.pg').forEach(function(pg){showPane(pg,'');});
all('.tabs button').forEach(function(b){b.addEventListener('click',function(){showPane(pageOf(b),b.getAttribute('data-tab'));});});
function treeItems(){return all('.tree a');}
function reveal(a){var li=a.parentNode.parentNode;while(li&&li.tagName==='LI'){li.classList.add('open');li=li.parentNode.parentNode;}}
function setView(v){state.view=v;all('.views button').forEach(function(b){b.classList.toggle('on',b.getAttribute('data-view')===v);});all('.tree').forEach(function(t){t.classList.toggle('on',t.getAttribute('data-view')===v);});}
function select(id){all('.sel',side).forEach(function(a){a.classList.remove('sel');});var href='#'+id,found=null;
 all('.pages a').forEach(function(a){if(a.getAttribute('href')===href){a.classList.add('sel');}});
 var cur=one('.tree[data-view="'+state.view+'"]');all('a',cur).forEach(function(a){if(!found&&a.getAttribute('href')===href){found=a;}});
 if(!found){all('.tree').some(function(t){var hit=null;all('a',t).forEach(function(a){if(!hit&&a.getAttribute('href')===href){hit=a;}});if(hit){found=hit;setView(t.getAttribute('data-view'));return true;}return false;});}
 if(found){found.classList.add('sel');reveal(found);var r=found.getBoundingClientRect(),s=side.getBoundingClientRect();if(r.top<s.top){side.scrollTop-=s.top-r.top+40;}else if(r.bottom>s.bottom){side.scrollTop+=r.bottom-s.bottom+40;}}}
function go(){var id=decodeURIComponent(location.hash.slice(1))||root.getAttribute('data-start')||'overview';var el=d.getElementById(id);if(!el||!pageOf(el)){id='overview';el=d.getElementById(id);}
 var pg=pageOf(el);pages.forEach(function(p){p.hidden=p!==pg;});var pane=paneOf(el);if(pane){showPane(pg,pane.getAttribute('data-pane'));}
 select(pg.id);if(el!==pg){select(el.id);}var h=one('h2',pg);d.title=(h?h.textContent.replace(/\s+/g,' ').trim()+' - ':'')+title;
 if(el!==pg){el.scrollIntoView();el.classList.remove('flash');void el.offsetWidth;el.classList.add('flash');}else{window.scrollTo(0,0);}}
window.addEventListener('hashchange',go);window.addEventListener('load',function(){setTimeout(go,0);});
all('.views button').forEach(function(b){b.addEventListener('click',function(){setView(b.getAttribute('data-view'));filterTree();});});
all('.tree li').forEach(function(li){if(!one('ul',li)){var t=one('.tw',li);if(t){t.classList.add('leaf');}}});
all('.tree>li').forEach(function(li){li.classList.add('open');});
all('.tree .tw').forEach(function(b){b.addEventListener('click',function(){var li=b.parentNode.parentNode;li.classList.toggle('open');b.setAttribute('aria-expanded',li.classList.contains('open')?'true':'false');});});
var q=one('#q');
function filterTree(){if(!q){return;}var text=q.value.trim().toLowerCase(),tree=one('.tree[data-view="'+state.view+'"]');if(!tree){return;}
 function walk(li){var a=one('a',li),own=!text||(a&&a.textContent.toLowerCase().indexOf(text)>=0),kids=false;all(':scope>ul>li',li).forEach(function(c){if(walk(c)){kids=true;}});var show=own||kids;li.classList.toggle('hide',!show);if(text&&kids){li.classList.add('open');}return show;}
 all(':scope>li',tree).forEach(walk);}
if(q){q.addEventListener('input',filterTree);q.addEventListener('keydown',function(e){if(e.key==='Enter'){var tree=one('.tree[data-view="'+state.view+'"]'),hit=null;all('li',tree).forEach(function(li){var a=one('a',li);if(!hit&&!li.classList.contains('hide')&&q.value&&a.textContent.toLowerCase().indexOf(q.value.trim().toLowerCase())>=0){hit=a;}});if(hit){location.hash=hit.getAttribute('href');}}else if(e.key==='Escape'){q.value='';filterTree();}});}
var vt=one('table.vt'),rows=vt?all('tbody tr',vt):[];rows.forEach(function(r){r._t=r.textContent.toLowerCase();});
var vf=one('#vmFilter');
var labels={ready:'Ready',review:'Review',blocked:'Blocked',skip:'Not moved'};
function applyVMs(){if(!vt){return;}var text=vf?vf.value.trim().toLowerCase():'',shown=0,counts={'':0,ready:0,review:0,blocked:0,skip:0},flags={};
 rows.forEach(function(r){var st=r.getAttribute('data-'+state.target)||'',fl=' '+(r.getAttribute('data-flags')||'')+' ',textOK=!text||r._t.indexOf(text)>=0,flagOK=!state.flag||fl.indexOf(' '+state.flag+' ')>=0;
  if(textOK&&flagOK){counts['']++;counts[st]=(counts[st]||0)+1;}
  if(textOK&&(!state.status||st===state.status)){fl.trim().split(' ').forEach(function(f){flags[f]=(flags[f]||0)+1;});}
  var show=textOK&&flagOK&&(!state.status||st===state.status);r.classList.toggle('hide',!show);if(show){shown++;}});
 all('.fchip[data-status]').forEach(function(c){var s=c.getAttribute('data-status');c.classList.toggle('on',s===state.status);var b=one('b',c);if(b){b.textContent=counts[s]||0;}});
 all('.fchip[data-flag]').forEach(function(c){var f=c.getAttribute('data-flag');c.classList.toggle('on',f===state.flag);var b=one('b',c);if(b){b.textContent=flags[f]||0;}});
 var n=one('#vmCount');if(n){n.textContent='Showing '+shown+' of '+rows.length+' VMs';}
 all('th.tgt,td.tgt',vt).forEach(function(c){c.classList.toggle('cur',c.getAttribute('data-target')===state.target);});
 var e=one('#vmEmpty');if(e){e.hidden=shown>0;}}
function setTarget(t){state.target=t;all('select.tsel').forEach(function(s){s.value=t;});all('.tg').forEach(function(g){g.hidden=g.getAttribute('data-target')!==t;});
 var map={};rows.forEach(function(r){map[r.getAttribute('data-vm')]=r.getAttribute('data-'+t)||'';});
 all('.tree a[data-vm]').forEach(function(a){var dot=one('.dot',a);if(dot){dot.className='dot '+(map[a.getAttribute('data-vm')]||'');dot.title=labels[map[a.getAttribute('data-vm')]]||'';}});applyVMs();}
all('select.tsel').forEach(function(s){s.addEventListener('change',function(){setTarget(s.value);});});
all('.fchip[data-status]').forEach(function(c){c.addEventListener('click',function(){var s=c.getAttribute('data-status');state.status=state.status===s?'':s;applyVMs();});});
all('.fchip[data-flag]').forEach(function(c){c.addEventListener('click',function(){var f=c.getAttribute('data-flag');state.flag=state.flag===f?'':f;applyVMs();});});
if(vf){vf.addEventListener('input',applyVMs);}
all('[data-status],[data-flag],[data-target]').forEach(function(a){if(a.tagName!=='A'){return;}a.addEventListener('click',function(){var t=a.getAttribute('data-target');if(t){setTarget(t);}if(a.hasAttribute('data-status')||a.hasAttribute('data-flag')){state.status=a.getAttribute('data-status')||'';state.flag=a.getAttribute('data-flag')||'';if(vf){vf.value='';}applyVMs();}});});
function cellValue(td){var v=td.getAttribute('data-v');return v!==null?v:td.textContent.trim();}
all('table.dt').forEach(function(t){var head=all('thead th',t),body=one('tbody',t);if(!body){return;}
 head.forEach(function(th,i){th.classList.add('sort');th.addEventListener('click',function(){var dir=th.classList.contains('asc')?-1:1;head.forEach(function(h){h.classList.remove('asc','desc');});th.classList.add(dir>0?'asc':'desc');
  var list=all(':scope>tr',body),num=list.every(function(r){var c=r.children[i];return !c||cellValue(c)===''||!isNaN(parseFloat(cellValue(c)));});
  list.sort(function(a,b){var x=a.children[i]?cellValue(a.children[i]):'',y=b.children[i]?cellValue(b.children[i]):'';if(num){return ((parseFloat(x)||0)-(parseFloat(y)||0))*dir;}return x.localeCompare(y,undefined,{numeric:true,sensitivity:'base'})*dir;});
  list.forEach(function(r){body.appendChild(r);});});});
 if(t.classList.contains('vt')||all(':scope>tr',body).length<9){return;}
 var box=d.createElement('div');box.className='bar-tools';var input=d.createElement('input');input.type='search';input.className='tf';input.placeholder='Filter this table';input.setAttribute('aria-label','Filter this table');var count=d.createElement('span');count.className='count';box.appendChild(input);box.appendChild(count);
 var wrap=t.parentNode;wrap.parentNode.insertBefore(box,wrap);var list=all(':scope>tr',body);list.forEach(function(r){r._t=r.textContent.toLowerCase();});
 input.addEventListener('input',function(){var text=input.value.trim().toLowerCase(),shown=0;list.forEach(function(r){var show=!text||r._t.indexOf(text)>=0;r.classList.toggle('hide',!show);if(show){shown++;}});count.textContent=text?shown+' of '+list.length+' rows':'';});});
var p=one('#printAll');if(p){p.addEventListener('click',function(){window.print();});}
d.addEventListener('keydown',function(e){if(e.key==='/'&&q&&d.activeElement!==q&&!/INPUT|SELECT|TEXTAREA/.test((d.activeElement||{}).tagName||'')){e.preventDefault();q.focus();}});
setView('hosts');setTarget(state.target);go();
})();`

// vmReportIcons are line icons drawn for this report (no vendor artwork).
const vmReportIcons = `<svg width="0" height="0" style="position:absolute" aria-hidden="true"><defs>
<symbol id="i-dc" viewBox="0 0 16 16"><path d="M2 14V5l6-3 6 3v9M5 14V8h6v6M2 14h12"/></symbol>
<symbol id="i-cluster" viewBox="0 0 16 16"><rect x="1.5" y="2" width="6" height="5" rx="1"/><rect x="8.5" y="2" width="6" height="5" rx="1"/><rect x="5" y="9" width="6" height="5" rx="1"/></symbol>
<symbol id="i-host" viewBox="0 0 16 16"><rect x="2" y="2.5" width="12" height="4.5" rx="1"/><rect x="2" y="9" width="12" height="4.5" rx="1"/><path d="M4.5 4.7h.01M4.5 11.2h.01"/></symbol>
<symbol id="i-vm" viewBox="0 0 16 16"><rect x="2" y="2.5" width="12" height="8.5" rx="1.2"/><path d="M6 14h4M8 11v3"/></symbol>
<symbol id="i-ds" viewBox="0 0 16 16"><ellipse cx="8" cy="3.8" rx="5.5" ry="2"/><path d="M2.5 3.8v8.4c0 1.1 2.5 2 5.5 2s5.5-.9 5.5-2V3.8M2.5 8c0 1.1 2.5 2 5.5 2s5.5-.9 5.5-2"/></symbol>
<symbol id="i-net" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6"/><path d="M2 8h12M8 2c2 2 2 10 0 12M8 2c-2 2-2 10 0 12"/></symbol>
<symbol id="i-root" viewBox="0 0 16 16"><rect x="2" y="2" width="12" height="12" rx="2"/><path d="M5 6h6M5 8.5h6M5 11h4"/></symbol>
</defs></svg>`

var vmReportScriptHash = func() string {
	sum := sha256.Sum256([]byte(vmReportScript))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}()

var vmReportHTMLFunctions = template.FuncMap{
	"bytes":       vmFormatBytes,
	"mb":          func(mb int64) string { return vmFormatBytes(mb << 20) },
	"join":        strings.Join,
	"date":        vmReportDate,
	"statusLabel": vmReportStatusLabel,
	"sev":         vmReportSeverityLabel,
	"power":       vmReportPower,
	"guest":       vmReportGuest,
	"firmware":    vmReportFirmware,
	"tools":       vmReportTools,
	"pct":         vmReportPercent,
	"used":        func(capacity, free int64) int64 { return capacity - free },
	"f1":          func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) },
	"i64":         func(v int) int64 { return int64(v) },
	"mib":         func(v int64) int64 { return v << 20 },
	"flags":       func(flags []string) string { return strings.Join(flags, " ") },
	"plus":        func(a, b int) int { return a + b },
	"yesno": func(value bool) string {
		if value {
			return "Yes"
		}
		return "No"
	},
	"running": func(value bool) string {
		if value {
			return "Running"
		}
		return "Stopped"
	},
	"limit": func(value int64, unit string) string {
		if value < 0 {
			return "Unlimited"
		}
		return strconv.FormatInt(value, 10) + " " + unit
	},
	"speed": func(mb int) string {
		switch {
		case mb <= 0:
			return "Down"
		case mb%1000 == 0:
			return strconv.Itoa(mb/1000) + " Gb/s"
		default:
			return strconv.Itoa(mb) + " Mb/s"
		}
	},
	"meter": func(percent int) string {
		switch {
		case percent >= 90:
			return "high"
		case percent >= 75:
			return "warn"
		}
		return ""
	},
	"ratio": func(vcpus, cores int) string {
		if cores <= 0 {
			return ""
		}
		return strconv.FormatFloat(float64(vcpus)/float64(cores), 'f', 1, 64) + " : 1"
	},
	"first": func(n int, list []vmReportIssueVM) []vmReportIssueVM {
		if len(list) > n {
			return list[:n]
		}
		return list
	},
	"rest": func(n int, list []vmReportIssueVM) []vmReportIssueVM {
		if len(list) > n {
			return list[n:]
		}
		return nil
	},
	"targets":      func() []vmTargetProfile { return vmTargetProfiles },
	"datacenters":  vmReportDatacenters,
	"hostsIn":      vmReportHostsIn,
	"standalone":   vmReportStandalone,
	"datastoresIn": vmReportDatastoresIn,
	"networksIn":   vmReportNetworksIn,
	"vmsIn":        vmReportVMsIn,
	"datastoreVMs": vmReportDatastoreVMs,
	"networkVMs":   vmReportNetworkVMs,
	"focusStatus":  func(vm *vmRelVM) string { return vmReportStatusOf(vm.VM, vmReportFocusTarget) },
	"clusterID":    vmReportClusterID,
}

// vmReportStatusOf returns a VM's readiness status for a target.
func vmReportStatusOf(vm vmMachine, target string) string {
	if readiness := vm.readinessFor(target); readiness != nil {
		return readiness.Status
	}
	return ""
}

// vmReportClusterID is a cluster's page anchor: letters, digits and dashes
// only, so it works as a link in every browser, Word and PDF reader.
func vmReportClusterID(datacenter, cluster string) string {
	clean := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, datacenter+"-"+cluster)
	sum := sha256.Sum256([]byte(datacenter + "\x00" + cluster))
	return "cluster-" + clean + "-" + hex.EncodeToString(sum[:3])
}

// vmReportDatacenter is one datacenter's branch of the tree.
type vmReportDatacenter struct {
	Name     string
	Clusters []vmRelCluster
}

func vmReportDatacenters(model *vmReportModel) []vmReportDatacenter {
	order, byName := []string{}, map[string]*vmReportDatacenter{}
	add := func(name string) *vmReportDatacenter {
		if item := byName[name]; item != nil {
			return item
		}
		order = append(order, name)
		byName[name] = &vmReportDatacenter{Name: name}
		return byName[name]
	}
	for _, name := range model.Inventory.Datacenters {
		add(name)
	}
	for _, cluster := range model.Relations.Clusters {
		item := add(cluster.Datacenter)
		item.Clusters = append(item.Clusters, cluster)
	}
	for _, host := range model.Inventory.Hosts {
		add(host.Datacenter)
	}
	for _, datastore := range model.Inventory.Datastores {
		add(datastore.Datacenter)
	}
	for _, network := range model.Inventory.Networks {
		add(network.Datacenter)
	}
	result := []vmReportDatacenter{}
	for _, name := range order {
		result = append(result, *byName[name])
	}
	return result
}

func vmReportHostsIn(model *vmReportModel, datacenter, cluster string) []*vmRelHost {
	result := []*vmRelHost{}
	for _, host := range model.Relations.Hosts {
		if host.Host.Datacenter == datacenter && host.Host.Cluster == cluster {
			result = append(result, host)
		}
	}
	return result
}

// vmReportStandalone reports whether a cluster entry is the relations'
// group of hosts outside any cluster.
func vmReportStandalone(cluster vmRelCluster) bool {
	for _, host := range cluster.Hosts {
		return host.Host.Cluster == ""
	}
	return false
}

func vmReportDatastoresIn(model *vmReportModel, datacenter string) []*vmRelDatastore {
	result := []*vmRelDatastore{}
	for _, item := range model.Relations.Datastores {
		if item.Datastore.Datacenter == datacenter {
			result = append(result, item)
		}
	}
	return result
}

func vmReportNetworksIn(model *vmReportModel, datacenter string) []*vmRelNetwork {
	result := []*vmRelNetwork{}
	for _, item := range model.Relations.Networks {
		if item.Network.Datacenter == datacenter {
			result = append(result, item)
		}
	}
	return result
}

// vmReportVMsIn lists the VMs of a datacenter, for the VMs view of the tree.
func vmReportVMsIn(model *vmReportModel, datacenter string, known bool) []*vmRelVM {
	names := map[string]bool{}
	for _, name := range model.Inventory.Datacenters {
		names[name] = true
	}
	result := []*vmRelVM{}
	for _, item := range model.Relations.VMs {
		if known && item.VM.Datacenter == datacenter || !known && !names[item.VM.Datacenter] && datacenter == "" {
			result = append(result, item)
		}
	}
	return result
}

// vmReportDatastoreVMs lists each VM with a disk on the datastore once.
func vmReportDatastoreVMs(item *vmRelDatastore) []vmRelLink {
	seen, result := map[string]bool{}, []vmRelLink{}
	for _, disk := range item.Disks {
		if !seen[disk.VMAnchor] {
			seen[disk.VMAnchor] = true
			result = append(result, vmRelLink{Name: disk.VM, Anchor: disk.VMAnchor})
		}
	}
	return result
}

// vmReportNetworkVMs lists each VM with an adapter on the network once.
func vmReportNetworkVMs(item *vmRelNetwork) []vmRelLink {
	seen, result := map[string]bool{}, []vmRelLink{}
	for _, nic := range item.Adapters {
		if !seen[nic.VMAnchor] {
			seen[nic.VMAnchor] = true
			result = append(result, vmRelLink{Name: nic.VM, Anchor: nic.VMAnchor})
		}
	}
	return result
}

const vmReportTemplate = `<!DOCTYPE html>
<html lang="en" data-start="{{.Start}}" data-focus="{{.Model.Focus.ID}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="color-scheme" content="light"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src '{{.ScriptHash}}'"><title>{{.Model.Title}} - {{.Model.Inventory.Server}}</title><style>{{.Style}}</style></head>
<body>{{.Icons}}{{$m := .Model}}{{$w := $m.Words}}{{$inv := $m.Inventory}}
<header class="top"><div class="mark"><svg width="20" height="20" viewBox="0 0 16 16" style="fill:none;stroke:#fff;stroke-width:1.5;stroke-linecap:round"><rect x="2" y="2.5" width="12" height="4.5" rx="1"/><rect x="2" y="9" width="12" height="4.5" rx="1"/><path d="M4.5 4.7h.01M4.5 11.2h.01"/></svg></div>
<div><h1>{{$m.Title}}</h1><p class="sub">{{$inv.ServerType}} {{$inv.Server}}{{if $inv.Product}} · {{$inv.Product}}{{end}} · read {{date $inv.CollectedAt}}{{if $inv.CollectedBy}} by {{$inv.CollectedBy}}{{end}}</p></div>
<div class="tools"><input type="search" id="q" placeholder="Find a host, VM, datastore or network  ( / )" aria-label="Find in the inventory" autocomplete="off" spellcheck="false"><button class="btn" type="button" id="printAll">Print or save as PDF</button></div></header>
<div class="shell">
<nav class="side" aria-label="Report navigation">
<h2>Report</h2>
<ul class="pages">
<li><a href="#overview">Overview</a></li>
<li><a href="#vms">VMs and readiness <em>{{len $m.VMs}}</em></a></li>
<li><a href="#issues">Issues and fixes</a></li>
<li><a href="#hosts">{{$w.Hosts}} <em>{{len $inv.Hosts}}</em></a></li>
<li><a href="#datastores">{{$w.Storage}} <em>{{len $inv.Datastores}}</em></a></li>
<li><a href="#networks">{{$w.Networks}} <em>{{len $inv.Networks}}</em></a></li>
<li><a href="#environment">{{if eq $w.Platform "VMware"}}vCenter, licenses and more{{else}}Host details{{end}}</a></li>
</ul>
<h2>Inventory</h2>
<div class="views" role="tablist"><button type="button" data-view="hosts">Hosts</button><button type="button" data-view="vms">VMs</button><button type="button" data-view="storage">Storage</button><button type="button" data-view="network">Networks</button></div>
<ul class="tree" data-view="hosts" role="tree" aria-label="Hosts and clusters"><li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#overview"><i class="ic"><svg><use href="#i-root"/></svg></i><span>{{$inv.Server}}</span></a></div><ul>
{{range datacenters $m}}{{$dc := .Name}}<li class="open"><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#hosts"><i class="ic"><svg><use href="#i-dc"/></svg></i><span>{{if $dc}}{{$dc}}{{else}}{{$w.Hosts}}{{end}}</span></a></div><ul>
{{range $i, $c := .Clusters}}{{if standalone $c}}{{range $c.Hosts}}<li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#{{.Anchor}}"><i class="ic"><svg><use href="#i-host"/></svg></i><span>{{.Host.Name}}</span><em>{{len .VMs}}</em></a></div><ul>{{range .VMs}}<li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#{{.Anchor}}" data-vm="{{.Anchor}}"><i class="ic"><svg><use href="#i-vm"/></svg></i><span>{{.VM.Name}}</span><i class="dot {{focusStatus .}}"></i></a></div></li>{{end}}</ul></li>{{end}}{{else}}<li class="open"><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#{{clusterID $dc $c.Name}}"><i class="ic"><svg><use href="#i-cluster"/></svg></i><span>{{$c.Name}}</span><em>{{len $c.Hosts}}</em></a></div><ul>{{range $c.Hosts}}<li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#{{.Anchor}}"><i class="ic"><svg><use href="#i-host"/></svg></i><span>{{.Host.Name}}</span><em>{{len .VMs}}</em></a></div><ul>{{range .VMs}}<li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#{{.Anchor}}" data-vm="{{.Anchor}}"><i class="ic"><svg><use href="#i-vm"/></svg></i><span>{{.VM.Name}}</span><i class="dot {{focusStatus .}}"></i></a></div></li>{{end}}</ul></li>{{end}}</ul></li>{{end}}{{end}}
</ul></li>{{end}}
{{if $m.Relations.Unplaced}}<li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#vms"><i class="ic"><svg><use href="#i-vm"/></svg></i><span>VMs on hosts not read</span><em>{{len $m.Relations.Unplaced}}</em></a></div><ul>{{range $m.Relations.Unplaced}}<li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#{{.Anchor}}" data-vm="{{.Anchor}}"><i class="ic"><svg><use href="#i-vm"/></svg></i><span>{{.VM.Name}}</span><i class="dot {{focusStatus .}}"></i></a></div></li>{{end}}</ul></li>{{end}}
</ul></li></ul>
<ul class="tree" data-view="vms" role="tree" aria-label="Virtual machines"><li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#vms"><i class="ic"><svg><use href="#i-root"/></svg></i><span>{{$inv.Server}}</span><em>{{len $m.Relations.VMs}}</em></a></div><ul>
{{range datacenters $m}}{{$dc := .Name}}{{$list := vmsIn $m $dc true}}{{if $list}}<li class="open"><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#vms"><i class="ic"><svg><use href="#i-dc"/></svg></i><span>{{if $dc}}{{$dc}}{{else}}Virtual machines{{end}}</span><em>{{len $list}}</em></a></div><ul>{{range $list}}<li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#{{.Anchor}}" data-vm="{{.Anchor}}"><i class="ic"><svg><use href="#i-vm"/></svg></i><span>{{.VM.Name}}</span><i class="dot {{focusStatus .}}"></i></a></div></li>{{end}}</ul></li>{{end}}{{end}}
{{$other := vmsIn $m "" false}}{{range $other}}<li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#{{.Anchor}}" data-vm="{{.Anchor}}"><i class="ic"><svg><use href="#i-vm"/></svg></i><span>{{.VM.Name}}</span><i class="dot {{focusStatus .}}"></i></a></div></li>{{end}}
</ul></li></ul>
<ul class="tree" data-view="storage" role="tree" aria-label="Storage"><li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#datastores"><i class="ic"><svg><use href="#i-root"/></svg></i><span>{{$inv.Server}}</span><em>{{len $inv.Datastores}}</em></a></div><ul>
{{range datacenters $m}}{{$list := datastoresIn $m .Name}}{{if $list}}<li class="open"><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#datastores"><i class="ic"><svg><use href="#i-dc"/></svg></i><span>{{if .Name}}{{.Name}}{{else}}{{$w.Storage}}{{end}}</span><em>{{len $list}}</em></a></div><ul>{{range $list}}<li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#{{.Anchor}}"><i class="ic"><svg><use href="#i-ds"/></svg></i><span>{{.Datastore.Name}}</span><em>{{pct (used .Datastore.CapacityBytes .Datastore.FreeBytes) .Datastore.CapacityBytes}}%</em></a></div><ul>{{range datastoreVMs .}}<li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#{{.Anchor}}" data-vm="{{.Anchor}}"><i class="ic"><svg><use href="#i-vm"/></svg></i><span>{{.Name}}</span><i class="dot"></i></a></div></li>{{end}}</ul></li>{{end}}</ul></li>{{end}}{{end}}
</ul></li></ul>
<ul class="tree" data-view="network" role="tree" aria-label="Networks"><li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#networks"><i class="ic"><svg><use href="#i-root"/></svg></i><span>{{$inv.Server}}</span><em>{{len $inv.Networks}}</em></a></div><ul>
{{range datacenters $m}}{{$list := networksIn $m .Name}}{{if $list}}<li class="open"><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#networks"><i class="ic"><svg><use href="#i-dc"/></svg></i><span>{{if .Name}}{{.Name}}{{else}}{{$w.Networks}}{{end}}</span><em>{{len $list}}</em></a></div><ul>{{range $list}}<li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#{{.Anchor}}"><i class="ic"><svg><use href="#i-net"/></svg></i><span>{{.Network.Name}}</span><em>{{.VMs}}</em></a></div><ul>{{range networkVMs .}}<li><div class="tr"><button type="button" class="tw" aria-label="Expand">&#9654;</button><a href="#{{.Anchor}}" data-vm="{{.Anchor}}"><i class="ic"><svg><use href="#i-vm"/></svg></i><span>{{.Name}}</span><i class="dot"></i></a></div></li>{{end}}</ul></li>{{end}}</ul></li>{{end}}{{end}}
</ul></li></ul>
</nav>
<main>

<section class="pg" id="overview">
<p class="crumb">{{$inv.Server}}</p>
<h2>Overview</h2>
<p class="lead">{{len $m.VMs}} virtual machines on {{len $inv.Hosts}} {{if eq (len $inv.Hosts) 1}}host{{else}}hosts{{end}}, with their readiness to move to each target. Select any figure, bar or name to open it.</p>
<div class="note">Read-only: nothing in {{$w.Platform}} was changed to write this report.{{if eq $w.Platform "VMware"}} License keys show only their first and last group.{{end}} Readiness is a guide based on the configuration {{$w.Platform}} reports; confirm against your target's supported guest list and test a migration before moving production VMs.</div>
<h3>Readiness for {{$m.Focus.Label}}</h3>
<div class="tiles">{{range $m.Tiles}}<a class="tile {{.Tone}}" href="{{.Href}}"{{if .Status}} data-status="{{.Status}}"{{end}}{{if .Flag}} data-flag="{{.Flag}}"{{end}}{{if .Target}} data-target="{{.Target}}"{{end}}{{if and (not .Status) (not .Flag)}} data-status=""{{end}}><b>{{.Value}}</b><span>{{.Label}}</span>{{if .Detail}}<small>{{.Detail}}</small>{{end}}</a>{{end}}</div>
<div class="card"><h3>Readiness by target</h3><p class="muted small">Each bar is every VM. Select a part of a bar to list those VMs.</p>
<div class="bars">{{range $m.Targets}}{{$t := .}}<div class="bar-row"><a class="lbl" href="#vms" data-target="{{.ID}}" data-status="">{{.Label}}</a><div class="bar" role="img" aria-label="{{.Label}}: {{.Ready}} ready, {{.Review}} review, {{.Blocked}} blocked, {{.Skip}} not moved">{{if .Ready}}<a class="seg ready" href="#vms" data-target="{{.ID}}" data-status="ready" style="width:{{f1 ($t.Percent "ready")}}%" title="{{.Ready}} ready">{{.Ready}}</a>{{end}}{{if .Review}}<a class="seg review" href="#vms" data-target="{{.ID}}" data-status="review" style="width:{{f1 ($t.Percent "review")}}%" title="{{.Review}} to review">{{.Review}}</a>{{end}}{{if .Blocked}}<a class="seg blocked" href="#vms" data-target="{{.ID}}" data-status="blocked" style="width:{{f1 ($t.Percent "blocked")}}%" title="{{.Blocked}} blocked">{{.Blocked}}</a>{{end}}{{if .Skip}}<a class="seg skip" href="#vms" data-target="{{.ID}}" data-status="skip" style="width:{{f1 ($t.Percent "skip")}}%" title="{{.Skip}} not moved">{{.Skip}}</a>{{end}}</div><span class="nums">{{.Ready}} ready · {{.Review}} review · {{.Blocked}} blocked · {{.Skip}} not moved</span></div>{{end}}</div>
<div class="legend"><span><i style="background:#2f9e63"></i>Ready</span><span><i style="background:#e0a526"></i>Review first</span><span><i style="background:#d64545"></i>Blocked</span><span><i style="background:#a3b1c2"></i>Not moved ({{$w.Platform}} system VMs)</span></div></div>
<h3>Environment</h3>
<div class="tiles">{{range $m.EnvTiles}}<a class="tile {{.Tone}}" href="{{.Href}}"><b>{{.Value}}</b><span>{{.Label}}</span>{{if .Detail}}<small>{{.Detail}}</small>{{end}}</a>{{end}}</div>
<div class="grid2">
<div class="card"><h3>Top issues for {{$m.Focus.Label}}</h3>{{$issues := $m.IssuesFor $m.Focus.ID}}{{if $issues}}<ul class="plain">{{range $i, $is := $issues}}{{if lt $i 8}}<li><span class="chip {{$is.Severity}}">{{sev $is.Severity}}</span> <a href="#{{$is.Anchor}}" data-target="{{$is.Target}}">{{$is.Message}}</a> <span class="muted">({{len $is.VMs}} VM{{if ne (len $is.VMs) 1}}s{{end}})</span></li>{{end}}{{end}}</ul><p class="small"><a href="#issues" data-target="{{$m.Focus.ID}}">All issues and fixes</a></p>{{else}}<p class="muted">No warnings or blockers for {{$m.Focus.Label}}.</p>{{end}}</div>
<div class="card" id="attention"><h3>Needs attention in the environment</h3>{{if $m.Watch}}<ul class="plain">{{range $m.Watch}}<li><span class="chip warning">Check</span> {{.}}</li>{{end}}</ul>{{else}}<p class="muted">Nothing found: no expired or over-allocated licenses, host alarms, disconnected hosts or hosts in maintenance mode.</p>{{end}}</div>
</div>
<div class="card"><h3>Storage</h3>{{if $inv.Datastores}}<table class="dt"><thead><tr><th>{{$w.Datastore}}</th><th>Type</th><th class="num">Capacity</th><th class="num">Used</th><th style="width:30%">Used</th></tr></thead><tbody>{{range $m.Relations.Datastores}}{{$p := pct (used .Datastore.CapacityBytes .Datastore.FreeBytes) .Datastore.CapacityBytes}}<tr><td><a href="#{{.Anchor}}">{{.Datastore.Name}}</a></td><td>{{.Datastore.Type}}</td><td class="num" data-v="{{.Datastore.CapacityBytes}}">{{bytes .Datastore.CapacityBytes}}</td><td class="num" data-v="{{$p}}">{{$p}}%</td><td data-v="{{$p}}"><div class="meter"><i class="{{meter $p}}" style="width:{{$p}}%"></i></div></td></tr>{{end}}</tbody></table>{{else}}<p class="muted">No storage was read.</p>{{end}}</div>
</section>

<section class="pg" id="vms">
<p class="crumb"><a href="#overview">{{$inv.Server}}</a></p>
<h2>VMs and readiness</h2>
<p class="lead">Every VM with its readiness for each target. Choose a target, then select a status to list only those VMs.</p>
<div class="bar-tools"><label>Target <select class="tsel" aria-label="Migration target">{{range $m.Targets}}<option value="{{.ID}}"{{if eq .ID $m.Focus.ID}} selected{{end}}>{{.Label}}</option>{{end}}</select></label>
<div class="fchips" role="group" aria-label="Readiness"><button type="button" class="fchip" data-status="">All<b></b></button><button type="button" class="fchip ready" data-status="ready">Ready<b></b></button><button type="button" class="fchip review" data-status="review">Review<b></b></button><button type="button" class="fchip blocked" data-status="blocked">Blocked<b></b></button><button type="button" class="fchip skip" data-status="skip">Not moved<b></b></button></div>
<div class="fchips" role="group" aria-label="Show only"><button type="button" class="fchip" data-flag="off">Powered off<b></b></button><button type="button" class="fchip" data-flag="snap">Snapshots<b></b></button><button type="button" class="fchip" data-flag="static">Static IP<b></b></button></div>
<input type="search" class="tf" id="vmFilter" placeholder="Filter by name, guest OS, host or IP" aria-label="Filter VMs"><span class="count" id="vmCount"></span></div>
<div class="tw-wrap"><table class="dt vt"><thead><tr><th>VM</th><th>Power</th><th>Guest OS</th><th class="num">vCPU</th><th class="num">Memory</th><th class="num">Used</th><th>Host</th>{{range targets}}<th class="tgt" data-target="{{.ID}}" title="{{.Label}}">{{.Short}}</th>{{end}}</tr></thead><tbody>
{{range $m.VMs}}<tr data-vm="{{.Anchor}}" data-flags="{{flags .Flags}}"{{range .Statuses}} data-{{.Target}}="{{.Status}}"{{end}}><td><a href="#{{.Anchor}}"><b>{{.VM.Name}}</b></a>{{if .VM.IPAddress}}<div class="muted small">{{.VM.IPAddress}}</div>{{end}}</td><td><span class="chip {{if eq .VM.PowerState "poweredOn"}}on{{else}}off{{end}}">{{power .VM.PowerState}}</span></td><td>{{guest .VM}}</td><td class="num">{{.VM.CPUs}}</td><td class="num" data-v="{{.VM.MemoryMB}}">{{mb .VM.MemoryMB}}</td><td class="num" data-v="{{.VM.UsedBytes}}">{{bytes .VM.UsedBytes}}</td><td>{{if .HostAnchor}}<a href="#{{.HostAnchor}}">{{.VM.Host}}</a>{{else}}{{.VM.Host}}{{end}}{{if .VM.Cluster}}<div class="muted small">{{.VM.Cluster}}</div>{{end}}</td>{{range .Statuses}}<td class="tgt" data-target="{{.Target}}" data-v="{{.Status}}"><span class="chip {{.Status}}" title="{{.Label}}: {{statusLabel .Status}}">{{statusLabel .Status}}</span></td>{{end}}</tr>
{{end}}</tbody></table><p class="empty" id="vmEmpty" hidden>No VMs match. Clear the filter or choose another status.</p></div>
{{if $m.Templates}}<h3>Templates</h3><div class="tw-wrap"><table class="dt"><thead><tr><th>Template</th><th>Guest OS</th><th class="num">Provisioned</th><th>Host</th></tr></thead><tbody>{{range $m.Templates}}<tr><td><a href="#{{.Anchor}}">{{.VM.Name}}</a></td><td>{{guest .VM}}</td><td class="num" data-v="{{.VM.ProvisionedBytes}}">{{bytes .VM.ProvisionedBytes}}</td><td>{{.VM.Host}}</td></tr>{{end}}</tbody></table></div>{{end}}
</section>

<section class="pg" id="issues">
<p class="crumb"><a href="#overview">{{$inv.Server}}</a></p>
<h2>Issues and fixes</h2>
<p class="lead">Every warning and blocker, grouped by issue, with the fix and the VMs it applies to. Information-only notes are on each VM's page.</p>
<div class="bar-tools"><label>Target <select class="tsel" aria-label="Migration target">{{range $m.Targets}}<option value="{{.ID}}"{{if eq .ID $m.Focus.ID}} selected{{end}}>{{.Label}}</option>{{end}}</select></label></div>
{{range $m.Issues}}<div class="tg" data-target="{{.Target.ID}}"><h3>{{.Target.Label}} <span class="chip ready">{{.Target.Ready}} ready</span> <span class="chip review">{{.Target.Review}} review</span> <span class="chip blocked">{{.Target.Blocked}} blocked</span></h3>
{{range .Issues}}<div class="issue {{.Severity}}" id="{{.Anchor}}"><h4><span class="chip {{.Severity}}">{{sev .Severity}}</span> {{.Message}}</h4>{{if .Action}}<div class="fix"><b>Fix:</b> {{.Action}}</div>{{end}}
<div class="muted small">{{len .VMs}} VM{{if ne (len .VMs) 1}}s{{end}}:</div>{{if .Varies}}<table class="idt">{{range first 40 .VMs}}<tr><td><a href="#{{.Anchor}}">{{.Name}}</a></td><td>{{.Message}}</td></tr>{{end}}</table>{{with rest 40 .VMs}}<details class="more"><summary>{{len .}} more</summary><table class="idt">{{range .}}<tr><td><a href="#{{.Anchor}}">{{.Name}}</a></td><td>{{.Message}}</td></tr>{{end}}</table></details>{{end}}{{else}}<div class="vmlist">{{range first 40 .VMs}}<a href="#{{.Anchor}}">{{.Name}}</a>{{end}}</div>{{with rest 40 .VMs}}<details class="more"><summary>{{len .}} more</summary><div class="vmlist">{{range .}}<a href="#{{.Anchor}}">{{.Name}}</a>{{end}}</div></details>{{end}}{{end}}</div>
{{else}}<p class="muted">No warnings or blockers for {{.Target.Label}}.</p>{{end}}</div>{{end}}
</section>

<section class="pg" id="hosts">
<p class="crumb"><a href="#overview">{{$inv.Server}}</a></p>
<h2>{{$w.Hosts}} and clusters</h2>
<p class="lead">{{len $inv.Hosts}} {{if eq (len $inv.Hosts) 1}}host{{else}}hosts{{end}} with {{$m.Cores}} physical cores and {{bytes $m.MemoryBytes}} memory. Select a host for its hardware, network, storage, services and VMs.</p>
{{if $inv.Clusters}}<h3>Clusters</h3><div class="tw-wrap"><table class="dt"><thead><tr><th>Cluster</th><th>Datacenter</th><th class="num">Hosts</th><th class="num">Cores</th><th class="num">Memory</th><th>HA</th><th>DRS</th><th>EVC mode</th><th>vSAN</th></tr></thead><tbody>{{range $inv.Clusters}}<tr><td><a href="#{{clusterID .Datacenter .Name}}">{{.Name}}</a></td><td>{{.Datacenter}}</td><td class="num">{{.Hosts}}</td><td class="num">{{.CPUCores}}</td><td class="num" data-v="{{.MemoryBytes}}">{{bytes .MemoryBytes}}</td><td>{{yesno .HA}}{{if .HAAdmission}}, admission control{{end}}</td><td>{{yesno .DRS}}{{if .DRSBehavior}} ({{.DRSBehavior}}){{end}}</td><td>{{if .EVCMode}}{{.EVCMode}}{{else}}Off{{end}}</td><td>{{yesno .VSAN}}</td></tr>{{end}}</tbody></table></div>{{end}}
{{range $m.Relations.Clusters}}<div class="card" id="{{clusterID .Datacenter .Name}}"><h3><i class="ic"><svg><use href="#i-cluster"/></svg></i> {{.Name}} <span class="muted small">{{if .Datacenter}}{{.Datacenter}} · {{end}}{{len .Hosts}} host(s) · {{.VMs}} VM(s)</span></h3>
<div class="tw-wrap"><table class="dt"><thead><tr><th>Host</th><th>Model</th><th>CPU</th><th class="num">Memory</th><th>{{$w.Version}}</th><th>State</th><th class="num">VMs</th><th>vCPU : core</th><th>Memory allocated</th></tr></thead><tbody>{{range .Hosts}}{{$mp := pct (mib .MemoryMB) .Host.MemoryBytes}}<tr><td><a href="#{{.Anchor}}">{{.Host.Name}}</a></td><td>{{.Host.Vendor}} {{.Host.Model}}</td><td>{{if .Host.Sockets}}{{.Host.Sockets}} × {{end}}{{.Host.CPUModel}}<div class="muted small">{{.Host.Cores}} cores</div></td><td class="num" data-v="{{.Host.MemoryBytes}}">{{bytes .Host.MemoryBytes}}</td><td>{{.Host.Version}}{{if .Host.Build}} ({{.Host.Build}}){{end}}</td><td><span class="chip {{if eq .Host.ConnectionState "connected"}}ready{{else}}review{{end}}">{{.Host.ConnectionState}}</span>{{if .Host.Maintenance}} <span class="chip review">Maintenance</span>{{end}}</td><td class="num">{{len .VMs}}</td><td data-v="{{.VCPUs}}">{{ratio .VCPUs .Host.Cores}}</td><td data-v="{{$mp}}">{{$mp}}%<div class="meter"><i class="{{meter $mp}}" style="width:{{if gt $mp 100}}100{{else}}{{$mp}}{{end}}%"></i></div></td></tr>{{else}}<tr><td colspan="9" class="empty">No hosts were read in this cluster.</td></tr>{{end}}</tbody></table></div></div>{{end}}
</section>

{{range $m.Relations.Hosts}}{{$h := .Host}}<section class="pg" id="{{.Anchor}}">
<p class="crumb"><a href="#hosts">{{$w.Hosts}}</a>{{if $h.Cluster}} / <a href="#{{clusterID $h.Datacenter $h.Cluster}}">{{$h.Cluster}}</a>{{end}}</p>
<h2><i class="ic"><svg><use href="#i-host"/></svg></i> {{$h.Name}} <span class="chip {{if eq $h.ConnectionState "connected"}}ready{{else}}review{{end}}">{{$h.ConnectionState}}</span>{{if $h.Maintenance}} <span class="chip review">Maintenance mode</span>{{end}}</h2>
<p class="lead">{{$w.Host}} · {{$h.Vendor}} {{$h.Model}} · {{$w.Version}} {{$h.Version}} · {{len .VMs}} VM(s), {{.PoweredOn}} powered on</p>
<div class="tabs" role="tablist"><button type="button" data-tab="summary">Summary</button><button type="button" data-tab="vms">VMs ({{len .VMs}})</button>{{with $h.Details}}<button type="button" data-tab="network">Networking</button><button type="button" data-tab="storage">Storage</button><button type="button" data-tab="services">Services</button><button type="button" data-tab="software">Software</button>{{end}}</div>
<div class="pane" data-pane="summary"><h3 class="ph">Summary</h3>
{{$mp := pct (mib .MemoryMB) $h.MemoryBytes}}<div class="tiles"><a class="tile" href="#{{.Anchor}}-vms"><b>{{len .VMs}}</b><span>VMs</span><small>{{.PoweredOn}} powered on</small></a><div class="tile"><b>{{.VCPUs}}</b><span>vCPUs allocated</span><small>{{ratio .VCPUs $h.Cores}} on {{$h.Cores}} cores</small></div><div class="tile {{if ge $mp 90}}blocked{{else if ge $mp 75}}review{{end}}"><b>{{$mp}}%</b><span>Memory allocated</span><small>{{mb .MemoryMB}} of {{bytes $h.MemoryBytes}}</small></div>{{with $h.Details}}<div class="tile"><b>{{.CPUUsagePercent}}%</b><span>CPU in use</span><small>memory {{.MemUsagePercent}}% in use</small></div>{{end}}</div>
<div class="card"><div class="kv"><div><b>Datacenter / cluster</b>{{$h.Datacenter}}{{if $h.Cluster}} / {{$h.Cluster}}{{end}}</div><div><b>Hardware</b>{{$h.Vendor}} {{$h.Model}}</div><div><b>CPU</b>{{$h.Sockets}} × {{$h.CPUModel}} ({{$h.Cores}} cores, {{$h.Threads}} threads, {{$h.CPUMhz}} MHz)</div><div><b>Memory</b>{{bytes $h.MemoryBytes}}</div><div><b>{{$w.Version}}</b>{{$h.Version}}{{if $h.Build}} build {{$h.Build}}{{end}}</div><div><b>State</b>{{$h.ConnectionState}}{{if $h.PowerState}}, {{$h.PowerState}}{{end}}{{if $h.Maintenance}}, maintenance mode{{end}}</div>{{if eq $w.Platform "VMware"}}<div><b>License</b>{{with $h.License}}{{.Name}} ({{.Key}}){{else}}Not reported{{end}}</div>{{end}}
{{with $h.Details}}<div><b>Serial number</b>{{.SerialNumber}}</div><div><b>Asset tag</b>{{.AssetTag}}</div><div><b>System UUID</b>{{.UUID}}</div><div><b>BIOS</b>{{.BIOSVendor}} {{.BIOSVersion}}{{if .BIOSDate}} ({{.BIOSDate}}){{end}}{{if .Firmware}}, {{.Firmware}}{{end}}</div><div><b>Hyperthreading</b>{{.Hyperthreading}}</div><div><b>Booted</b>{{.BootTime}}</div><div><b>Health</b>{{.OverallStatus}}</div><div><b>Lockdown mode</b>{{.LockdownMode}}</div><div><b>Management IP</b>{{.ManagementIP}}</div><div><b>DNS name</b>{{.HostName}}{{if .Domain}}.{{.Domain}}{{end}}</div><div><b>DNS servers</b>{{join .DNSServers ", "}}</div><div><b>Search domains</b>{{join .SearchDomains ", "}}</div><div><b>Default gateway</b>{{.DefaultGateway}}</div><div><b>Time zone</b>{{.TimeZone}}</div><div><b>NTP</b>{{if .NTPServers}}{{join .NTPServers ", "}}{{else}}None{{end}} ({{running .NTPRunning}})</div><div><b>Certificate</b>{{if .CertificateExpiry}}expires {{.CertificateExpiry}}{{if .CertificateIssuer}}, issued by {{.CertificateIssuer}}{{end}}{{end}}</div><div><b>Software acceptance level</b>{{.AcceptanceLevel}}</div>{{end}}
<div><b>{{$w.Storage}} mounted</b>{{range $i, $d := .Datastores}}{{if $i}}, {{end}}<a href="#{{$d.Anchor}}">{{$d.Name}}</a>{{else}}Not reported{{end}}</div></div></div>
{{with $h.Details}}{{if or .Alarms .ConfigIssues}}<div class="card"><h3>Alarms and configuration issues</h3><ul class="plain">{{range .Alarms}}<li><span class="chip warning">Alarm</span> {{.}}</li>{{end}}{{range .ConfigIssues}}<li><span class="chip warning">Issue</span> {{.}}</li>{{end}}</ul></div>{{end}}{{end}}
</div>
<div class="pane" data-pane="vms" id="{{.Anchor}}-vms"><h3 class="ph">Virtual machines on this host ({{len .VMs}})</h3>
<div class="tw-wrap"><table class="dt"><thead><tr><th>VM</th><th>Power</th><th>Guest OS</th><th class="num">vCPU</th><th class="num">Memory</th><th>Disks and {{$w.Storage}}</th><th>{{$w.Networks}}</th><th>{{$m.Focus.Short}}</th></tr></thead><tbody>{{range .VMs}}<tr><td><a href="#{{.Anchor}}">{{.VM.Name}}</a>{{if .VM.Template}} <span class="chip skip">Template</span>{{end}}</td><td><span class="chip {{if eq .VM.PowerState "poweredOn"}}on{{else}}off{{end}}">{{power .VM.PowerState}}</span></td><td>{{guest .VM}}</td><td class="num">{{.VM.CPUs}}</td><td class="num" data-v="{{.VM.MemoryMB}}">{{mb .VM.MemoryMB}}</td><td>{{range .Disks}}<div>{{.Disk.Label}} · {{bytes .Disk.CapacityBytes}} on {{if .DatastoreAnchor}}<a href="#{{.DatastoreAnchor}}">{{.Disk.Datastore}}</a>{{else if .Disk.RDM}}RDM ({{.Disk.RDM}}){{else}}{{.Disk.Datastore}}{{end}}</div>{{end}}</td><td>{{range .NICs}}<div>{{if .NetworkAnchor}}<a href="#{{.NetworkAnchor}}">{{.NIC.Network}}</a>{{else}}{{.NIC.Network}}{{end}}</div>{{end}}</td><td>{{$s := focusStatus .}}<span class="chip {{$s}}">{{statusLabel $s}}</span></td></tr>{{else}}<tr><td colspan="8" class="empty">No VMs on this host.</td></tr>{{end}}</tbody></table></div>
</div>
{{with $h.Details}}<div class="pane" data-pane="network"><h3 class="ph">Networking</h3>
<h4>Physical adapters ({{len .PhysicalNICs}})</h4><div class="tw-wrap"><table class="dt"><thead><tr><th>Physical NIC</th><th>Speed</th><th>MAC</th><th>Driver</th><th>Firmware</th><th>PCI</th><th>Switch</th></tr></thead><tbody>{{range .PhysicalNICs}}<tr><td>{{.Device}}</td><td>{{if .SpeedMb}}{{speed .SpeedMb}}{{if not .Duplex}}, half duplex{{end}}{{else}}<span class="chip blocked">Down</span>{{end}}</td><td>{{.MAC}}</td><td>{{.Driver}}</td><td>{{.Firmware}}</td><td>{{.PCI}}</td><td>{{.Switch}}</td></tr>{{end}}</tbody></table></div>
<h4>Virtual switches ({{len .Switches}})</h4><div class="tw-wrap"><table class="dt"><thead><tr><th>Virtual switch</th><th>Kind</th><th class="num">MTU</th><th class="num">Ports</th><th>Uplinks</th><th>Port groups</th></tr></thead><tbody>{{range .Switches}}<tr><td>{{.Name}}</td><td>{{.Kind}}</td><td class="num">{{.MTU}}</td><td class="num">{{.Ports}}</td><td>{{join .Uplinks ", "}}</td><td>{{join .Portgroups ", "}}</td></tr>{{end}}</tbody></table></div>
<h4>VMkernel ports ({{len .VMkernel}})</h4><div class="tw-wrap"><table class="dt"><thead><tr><th>VMkernel port</th><th>Port group</th><th>IP address</th><th>Mask</th><th class="num">MTU</th><th>TCP/IP stack</th><th>Services</th></tr></thead><tbody>{{range .VMkernel}}<tr><td>{{.Device}}</td><td>{{.Portgroup}}</td><td>{{.IP}}{{if .DHCP}} (DHCP){{end}}</td><td>{{.Mask}}</td><td class="num">{{.MTU}}</td><td>{{.Stack}}</td><td>{{join .Services ", "}}</td></tr>{{end}}</tbody></table></div></div>
<div class="pane" data-pane="storage"><h3 class="ph">Storage</h3>
<h4>Storage adapters ({{len .StorageAdapters}})</h4><div class="tw-wrap"><table class="dt"><thead><tr><th>Storage adapter</th><th>Kind</th><th>Model</th><th>Driver</th><th>Identifier</th><th>Status</th></tr></thead><tbody>{{range .StorageAdapters}}<tr><td>{{.Device}}</td><td>{{.Kind}}</td><td>{{.Model}}</td><td>{{.Driver}}</td><td>{{.Identifier}}</td><td>{{.Status}}</td></tr>{{end}}</tbody></table></div>
<h4>Storage devices ({{len .LUNs}})</h4><div class="tw-wrap"><table class="dt"><thead><tr><th>Storage device</th><th>Type</th><th>Vendor / model</th><th class="num">Capacity</th><th>SSD</th><th>Local</th><th>State</th></tr></thead><tbody>{{range .LUNs}}<tr><td>{{.DisplayName}}<div class="muted small">{{.CanonicalName}}</div></td><td>{{.Type}}</td><td>{{.Vendor}} {{.Model}}</td><td class="num" data-v="{{.CapacityBytes}}">{{if .CapacityBytes}}{{bytes .CapacityBytes}}{{end}}</td><td>{{yesno .SSD}}</td><td>{{yesno .Local}}</td><td>{{.State}}</td></tr>{{end}}</tbody></table></div></div>
<div class="pane" data-pane="services"><h3 class="ph">Services ({{len .Services}})</h3><div class="tw-wrap"><table class="dt"><thead><tr><th>Service</th><th>Key</th><th>State</th><th>Start policy</th></tr></thead><tbody>{{range .Services}}<tr><td>{{.Label}}</td><td>{{.Key}}</td><td><span class="chip {{if .Running}}ready{{else}}skip{{end}}">{{running .Running}}</span></td><td>{{.Policy}}</td></tr>{{end}}</tbody></table></div></div>
<div class="pane" data-pane="software"><h3 class="ph">Installed software packages (VIBs) ({{len .Packages}})</h3><div class="tw-wrap"><table class="dt"><thead><tr><th>Package</th><th>Version</th><th>Vendor</th><th>Acceptance</th><th>Created</th></tr></thead><tbody>{{range .Packages}}<tr><td>{{.Name}}</td><td>{{.Version}}</td><td>{{.Vendor}}</td><td>{{.Acceptance}}</td><td>{{.Created}}</td></tr>{{else}}<tr><td colspan="5" class="empty">Not listed by this host.</td></tr>{{end}}</tbody></table></div></div>{{end}}
</section>
{{end}}

<section class="pg" id="datastores">
<p class="crumb"><a href="#overview">{{$inv.Server}}</a></p>
<h2>{{$w.Storage}} and their disks</h2>
<p class="lead">{{bytes (used $m.StorageCapacity $m.StorageFree)}} used of {{bytes $m.StorageCapacity}} over {{len $inv.Datastores}} {{if eq (len $inv.Datastores) 1}}location{{else}}locations{{end}}.</p>
<div class="tw-wrap"><table class="dt"><thead><tr><th>{{$w.Datastore}}</th><th>Type</th><th class="num">Capacity</th><th class="num">Free</th><th class="num">Provisioned</th><th>Used</th><th>Shared</th><th class="num">Hosts</th><th class="num">VMs</th><th class="num">VM disks</th></tr></thead><tbody>{{range $m.Relations.Datastores}}{{$p := pct (used .Datastore.CapacityBytes .Datastore.FreeBytes) .Datastore.CapacityBytes}}<tr><td><a href="#{{.Anchor}}">{{.Datastore.Name}}</a>{{if not .Datastore.Accessible}} <span class="chip blocked">Not accessible</span>{{end}}</td><td>{{.Datastore.Type}}</td><td class="num" data-v="{{.Datastore.CapacityBytes}}">{{bytes .Datastore.CapacityBytes}}</td><td class="num" data-v="{{.Datastore.FreeBytes}}">{{bytes .Datastore.FreeBytes}}</td><td class="num" data-v="{{.Datastore.ProvisionedBytes}}">{{bytes .Datastore.ProvisionedBytes}}</td><td data-v="{{$p}}">{{$p}}%<div class="meter"><i class="{{meter $p}}" style="width:{{$p}}%"></i></div></td><td>{{yesno .Datastore.Shared}}</td><td class="num">{{len .Hosts}}</td><td class="num">{{.VMs}}</td><td class="num">{{len .Disks}}</td></tr>{{end}}</tbody></table></div>
{{if $m.Relations.OtherDisks}}<h3>Disks not on a datastore in this inventory</h3><p class="muted">Raw device mappings, and disks on datastores this account could not read.</p><div class="tw-wrap"><table class="dt"><thead><tr><th>VM</th><th>Host</th><th>Disk</th><th class="num">Size</th><th>Datastore or device</th><th>File</th></tr></thead><tbody>{{range $m.Relations.OtherDisks}}<tr><td><a href="#{{.VMAnchor}}">{{.VM}}</a></td><td>{{.Host}}</td><td>{{.Disk.Label}}</td><td class="num" data-v="{{.Disk.CapacityBytes}}">{{bytes .Disk.CapacityBytes}}</td><td>{{.Disk.Datastore}}{{if .Disk.RDM}}{{if .Disk.Datastore}}, {{end}}RDM ({{.Disk.RDM}}){{end}}</td><td>{{.Disk.File}}</td></tr>{{end}}</tbody></table></div>{{end}}
</section>

{{range $m.Relations.Datastores}}{{$ds := .Datastore}}{{$p := pct (used $ds.CapacityBytes $ds.FreeBytes) $ds.CapacityBytes}}<section class="pg" id="{{.Anchor}}">
<p class="crumb"><a href="#datastores">{{$w.Storage}}</a>{{if $ds.Datacenter}} / {{$ds.Datacenter}}{{end}}</p>
<h2><i class="ic"><svg><use href="#i-ds"/></svg></i> {{$ds.Name}}{{if not $ds.Accessible}} <span class="chip blocked">Not accessible</span>{{end}}</h2>
<div class="tiles"><div class="tile {{if ge $p 90}}blocked{{else if ge $p 75}}review{{end}}"><b>{{$p}}%</b><span>Used</span><small>{{bytes $ds.FreeBytes}} free of {{bytes $ds.CapacityBytes}}</small><div class="meter"><i class="{{meter $p}}" style="width:{{$p}}%"></i></div></div><div class="tile"><b>{{bytes $ds.ProvisionedBytes}}</b><span>Provisioned</span><small>{{pct $ds.ProvisionedBytes $ds.CapacityBytes}}% of capacity</small></div><a class="tile" href="#{{.Anchor}}-disks"><b>{{.VMs}}</b><span>VMs</span><small>{{len .Disks}} disk(s), {{bytes .DiskBytes}}</small></a><div class="tile"><b>{{len .Hosts}}</b><span>Hosts mount it</span><small>{{if $ds.Shared}}Shared{{else}}Not shared{{end}}</small></div></div>
<div class="card"><div class="kv"><div><b>Type</b>{{$ds.Type}}</div><div><b>Datacenter</b>{{$ds.Datacenter}}</div>{{if $ds.URL}}<div><b>URL</b>{{$ds.URL}}</div>{{end}}<div><b>Mounted on</b>{{range $i, $h := .Hosts}}{{if $i}}, {{end}}{{if $h.Anchor}}<a href="#{{$h.Anchor}}">{{$h.Name}}</a>{{else}}{{$h.Name}}{{end}}{{else}}Not reported{{end}}</div></div></div>
<h3 id="{{.Anchor}}-disks">VM disks on this datastore ({{len .Disks}} disks of {{.VMs}} VM(s), {{bytes .DiskBytes}})</h3>
<div class="tw-wrap"><table class="dt"><thead><tr><th>VM</th><th>Host</th><th>Disk</th><th class="num">Size</th><th>Provisioning</th><th>Mode</th><th>File</th></tr></thead><tbody>{{range .Disks}}<tr><td><a href="#{{.VMAnchor}}">{{.VM}}</a></td><td>{{.Host}}</td><td>{{.Disk.Label}}{{if .Disk.RDM}} (RDM {{.Disk.RDM}}){{end}}</td><td class="num" data-v="{{.Disk.CapacityBytes}}">{{bytes .Disk.CapacityBytes}}</td><td>{{.Disk.Provisioning}}</td><td>{{.Disk.Mode}}{{if .Disk.Sharing}}{{if ne .Disk.Sharing "sharingNone"}}, {{.Disk.Sharing}}{{end}}{{end}}</td><td class="small">{{.Disk.File}}</td></tr>{{else}}<tr><td colspan="7" class="empty">No VM disks on this datastore.</td></tr>{{end}}</tbody></table></div>
</section>
{{end}}

<section class="pg" id="networks">
<p class="crumb"><a href="#overview">{{$inv.Server}}</a></p>
<h2>{{$w.Networks}} and their VMs</h2>
<div class="tw-wrap"><table class="dt"><thead><tr><th>{{$w.Network}}</th><th>Kind</th><th>Switch</th><th>VLAN</th><th class="num">VMs</th><th class="num">Adapters</th></tr></thead><tbody>{{range $m.Relations.Networks}}<tr><td><a href="#{{.Anchor}}">{{.Network.Name}}</a></td><td>{{.Network.Kind}}</td><td>{{.Network.Switch}}</td><td>{{.Network.VLAN}}</td><td class="num">{{.VMs}}</td><td class="num">{{len .Adapters}}</td></tr>{{end}}</tbody></table></div>
</section>

{{range $m.Relations.Networks}}{{$n := .Network}}<section class="pg" id="{{.Anchor}}">
<p class="crumb"><a href="#networks">{{$w.Networks}}</a>{{if $n.Datacenter}} / {{$n.Datacenter}}{{end}}</p>
<h2><i class="ic"><svg><use href="#i-net"/></svg></i> {{$n.Name}}</h2>
<div class="card"><div class="kv"><div><b>Kind</b>{{$n.Kind}}</div><div><b>Switch</b>{{$n.Switch}}</div><div><b>VLAN</b>{{$n.VLAN}}</div><div><b>VMs</b>{{.VMs}}</div><div><b>Adapters</b>{{len .Adapters}}</div></div></div>
<h3>Adapters on this network</h3>
<div class="tw-wrap"><table class="dt"><thead><tr><th>VM</th><th>Host</th><th>Adapter</th><th>Type</th><th>MAC</th><th>Connected</th><th>Guest addresses</th></tr></thead><tbody>{{range .Adapters}}<tr><td><a href="#{{.VMAnchor}}">{{.VM}}</a></td><td>{{.Host}}</td><td>{{.NIC.Label}}</td><td>{{.NIC.AdapterType}}</td><td>{{.NIC.MAC}}</td><td>{{yesno .NIC.Connected}}</td><td>{{join .NIC.IPAddresses ", "}}{{if .NIC.Static}} <span class="chip warning">Static</span>{{end}}</td></tr>{{else}}<tr><td colspan="7" class="empty">No VM adapters on this network.</td></tr>{{end}}</tbody></table></div>
</section>
{{end}}

{{range $m.VMs}}{{template "vm" (vmCtx $m .)}}{{end}}
{{range $m.Templates}}{{template "vm" (vmCtx $m .)}}{{end}}

<section class="pg" id="environment">
<p class="crumb"><a href="#overview">{{$inv.Server}}</a></p>
<h2>{{if eq $w.Platform "VMware"}}vCenter, licenses and more{{else}}Host details{{end}}</h2>
<div class="card"><h3>{{$inv.ServerType}}</h3><div class="kv"><div><b>Server</b>{{$inv.Server}}</div><div><b>Product</b>{{$inv.Product}}</div>{{if $inv.Build}}<div><b>Build</b>{{$inv.Build}}</div>{{end}}{{if $inv.APIVersion}}<div><b>API version</b>{{$inv.APIVersion}}</div>{{end}}{{if $inv.InstanceUUID}}<div><b>Instance UUID</b>{{$inv.InstanceUUID}}</div>{{end}}{{if $inv.OSType}}<div><b>Platform</b>{{$inv.OSType}}</div>{{end}}{{if $inv.Datacenters}}<div><b>Datacenters</b>{{join $inv.Datacenters ", "}}</div>{{end}}<div><b>Read</b>{{date $inv.CollectedAt}}{{if $inv.CollectedBy}} by {{$inv.CollectedBy}}{{end}}</div></div></div>
{{if eq $w.Platform "VMware"}}<h3 id="licenses">Licenses</h3>
<div class="tw-wrap"><table class="dt"><thead><tr><th>License</th><th>Edition</th><th>Key</th><th>Product</th><th>Used / total</th><th>Expires</th><th>Assigned to</th></tr></thead><tbody>{{range $inv.Licenses}}<tr><td>{{.Name}}</td><td>{{.Edition}}</td><td>{{.Key}}</td><td>{{.Product}} {{.Version}}</td><td>{{.Used}} / {{if .Total}}{{.Total}}{{else}}unlimited{{end}} {{.Unit}}{{if .Total}}<div class="meter"><i class="{{meter (pct (i64 .Used) (i64 .Total))}}" style="width:{{if gt .Used .Total}}100{{else}}{{pct (i64 .Used) (i64 .Total)}}{{end}}%"></i></div>{{end}}</td><td>{{if .Expires}}{{.Expires}}{{else}}Never{{end}}</td><td>{{join .AssignedTo ", "}}</td></tr>{{else}}<tr><td colspan="7" class="empty">Licenses were not read (inventories saved by older versions do not have them).</td></tr>{{end}}</tbody></table></div>
<h3 id="switches">Distributed switches</h3>
<div class="tw-wrap"><table class="dt"><thead><tr><th>Switch</th><th>Datacenter</th><th>Version</th><th class="num">MTU</th><th class="num">Uplinks</th><th>Discovery protocol</th><th>Hosts</th></tr></thead><tbody>{{range $inv.Switches}}<tr><td>{{.Name}}</td><td>{{.Datacenter}}</td><td>{{.Version}}</td><td class="num">{{.MTU}}</td><td class="num">{{.Uplinks}}</td><td>{{.Discovery}}</td><td>{{join .Hosts ", "}}</td></tr>{{else}}<tr><td colspan="7" class="empty">None.</td></tr>{{end}}</tbody></table></div>
<h3 id="pools">Resource pools and vApps</h3>
<div class="tw-wrap"><table class="dt"><thead><tr><th>Pool</th><th>Kind</th><th>Cluster or host</th><th>Parent pool</th><th>CPU reservation / limit</th><th>Memory reservation / limit</th><th class="num">VMs</th></tr></thead><tbody>{{range $inv.ResourcePools}}<tr><td>{{.Name}}</td><td>{{if .VApp}}vApp{{else}}Resource pool{{end}}</td><td>{{.Owner}}</td><td>{{.Parent}}</td><td>{{.CPUReservationMHz}} MHz / {{limit .CPULimitMHz "MHz"}}</td><td>{{.MemReservationMB}} MB / {{limit .MemLimitMB "MB"}}</td><td class="num">{{.VMCount}}</td></tr>{{else}}<tr><td colspan="7" class="empty">None.</td></tr>{{end}}</tbody></table></div>{{end}}
<h3 id="warnings">Collection warnings</h3>{{if $inv.Warnings}}<ul class="plain">{{range $inv.Warnings}}<li><span class="chip warning">Warning</span> {{.}}</li>{{end}}</ul>{{else}}<p class="muted">Everything was read without warnings.</p>{{end}}
<p class="foot">{{$m.Generator}} {{$inv.AppVersion}} · written {{date $m.Generated}}. The same report is in {{.Docx}} (Word) and {{.PDF}} (PDF) in this folder, with every table as CSV.</p>
</section>
</main></div>
<script>{{.Script}}</script>
</body></html>
{{define "vm"}}{{$m := .Model}}{{$w := $m.Words}}{{with .VM}}{{$vm := .VM}}<section class="pg" id="{{.Anchor}}">
<p class="crumb"><a href="#vms">VMs</a>{{if $vm.Cluster}} / {{$vm.Cluster}}{{end}}{{if .HostAnchor}} / <a href="#{{.HostAnchor}}">{{$vm.Host}}</a>{{else if $vm.Host}} / {{$vm.Host}}{{end}}</p>
<h2><i class="ic"><svg><use href="#i-vm"/></svg></i> {{$vm.Name}} <span class="chip {{if eq $vm.PowerState "poweredOn"}}on{{else}}off{{end}}">{{power $vm.PowerState}}</span>{{if $vm.Template}} <span class="chip skip">Template</span>{{end}}</h2>
<p class="lead">{{guest $vm}} · {{$vm.CPUs}} vCPU · {{mb $vm.MemoryMB}} · {{bytes $vm.UsedBytes}} used of {{bytes $vm.ProvisionedBytes}}</p>
<div class="tabs" role="tablist"><button type="button" data-tab="readiness">Readiness</button><button type="button" data-tab="summary">Summary</button><button type="button" data-tab="disks">Disks ({{len $vm.Disks}})</button><button type="button" data-tab="nics">Network ({{len $vm.NICs}})</button>{{if $vm.Snapshots}}<button type="button" data-tab="snapshots">Snapshots ({{len $vm.Snapshots}})</button>{{end}}</div>
<div class="pane" data-pane="readiness"><h3 class="ph">Readiness</h3>
<div class="targets">{{range .Statuses}}<div class="tcard"><h4>{{.Label}} <span class="chip {{.Status}}">{{statusLabel .Status}}{{if .Generation}} · Gen {{.Generation}}{{end}}</span></h4><ul>{{range .Findings}}<li><span class="chip {{.Severity}}">{{sev .Severity}}</span> {{.Message}}{{if .Action}}<span class="act">Fix: {{.Action}}</span>{{end}}</li>{{else}}<li class="muted">Nothing to fix first.</li>{{end}}</ul></div>{{end}}</div>
</div>
<div class="pane" data-pane="summary"><h3 class="ph">Summary</h3><div class="card"><div class="kv">
<div><b>Guest OS</b>{{guest $vm}}</div><div><b>Host</b>{{if .HostAnchor}}<a href="#{{.HostAnchor}}">{{$vm.Host}}</a>{{else}}{{$vm.Host}}{{end}}</div><div><b>Cluster</b>{{$vm.Cluster}}</div><div><b>Datacenter</b>{{$vm.Datacenter}}</div>{{if $vm.Folder}}<div><b>Folder</b>{{$vm.Folder}}</div>{{end}}
<div><b>CPU</b>{{$vm.CPUs}} vCPU{{if $vm.CoresPerSocket}} ({{$vm.CoresPerSocket}} per socket){{end}}</div><div><b>Memory</b>{{mb $vm.MemoryMB}}{{if $vm.DynamicMemory}}, dynamic{{end}}</div><div><b>Storage</b>{{bytes $vm.UsedBytes}} used of {{bytes $vm.ProvisionedBytes}}</div><div><b>Firmware</b>{{firmware $vm}}{{if $vm.Generation}} (generation {{$vm.Generation}}){{end}}</div>
<div><b>{{if eq $vm.Source "hyperv"}}Configuration version{{else}}Hardware version{{end}}</b>{{if $vm.ConfigVersion}}{{$vm.ConfigVersion}}{{else}}{{$vm.HardwareVersion}}{{end}}</div><div><b>{{$w.Tools}}</b>{{tools $vm}}</div>{{if $vm.GuestHostName}}<div><b>Guest name</b>{{$vm.GuestHostName}}</div>{{end}}{{if $vm.IPAddress}}<div><b>IP address</b>{{$vm.IPAddress}}</div>{{end}}
<div><b>Encrypted</b>{{yesno $vm.Encrypted}}</div><div><b>Change tracking</b>{{yesno $vm.ChangeTracking}}</div>{{if $vm.ConnectedMedia}}<div><b>Connected media</b>{{join $vm.ConnectedMedia ", "}}</div>{{end}}{{if $vm.Passthrough}}<div><b>Passthrough devices</b>{{join $vm.Passthrough ", "}}</div>{{end}}{{if $vm.USBDevices}}<div><b>USB devices</b>{{$vm.USBDevices}}</div>{{end}}
</div></div>
{{if $vm.GuestDisks}}<h4>Volumes inside the guest</h4><div class="tw-wrap"><table class="dt"><thead><tr><th>Volume</th><th class="num">Capacity</th><th class="num">Free</th><th>Used</th></tr></thead><tbody>{{range $vm.GuestDisks}}{{$p := pct (used .CapacityBytes .FreeBytes) .CapacityBytes}}<tr><td>{{.Path}}</td><td class="num" data-v="{{.CapacityBytes}}">{{bytes .CapacityBytes}}</td><td class="num" data-v="{{.FreeBytes}}">{{bytes .FreeBytes}}</td><td data-v="{{$p}}">{{$p}}%<div class="meter"><i class="{{meter $p}}" style="width:{{$p}}%"></i></div></td></tr>{{end}}</tbody></table></div>{{end}}
</div>
<div class="pane" data-pane="disks"><h3 class="ph">Disks</h3><div class="tw-wrap"><table class="dt"><thead><tr><th>Disk</th><th class="num">Size</th><th>Type</th><th>Controller</th><th>{{$w.Datastore}}</th><th>Mode</th></tr></thead><tbody>{{range .Disks}}<tr><td>{{.Disk.Label}}{{if .Disk.BootCandidate}} <span class="chip info">Boot</span>{{end}}{{if .Disk.Encrypted}} <span class="chip blocked">Encrypted</span>{{end}}<div class="muted small">{{.Disk.File}}</div></td><td class="num" data-v="{{.Disk.CapacityBytes}}">{{bytes .Disk.CapacityBytes}}</td><td>{{.Disk.Provisioning}}{{if .Disk.RDM}} ({{.Disk.RDM}} RDM){{end}}</td><td>{{.Disk.Controller}} {{.Disk.Bus}}:{{.Disk.Unit}}{{if and .Disk.BusSharing (ne .Disk.BusSharing "noSharing")}} ({{.Disk.BusSharing}}){{end}}</td><td>{{if .DatastoreAnchor}}<a href="#{{.DatastoreAnchor}}">{{.Disk.Datastore}}</a>{{else}}{{.Disk.Datastore}}{{end}}</td><td>{{.Disk.Mode}}{{if and .Disk.Sharing (ne .Disk.Sharing "sharingNone")}}, {{.Disk.Sharing}}{{end}}</td></tr>{{else}}<tr><td colspan="6" class="empty">No disks.</td></tr>{{end}}</tbody></table></div></div>
<div class="pane" data-pane="nics"><h3 class="ph">Network adapters</h3><div class="tw-wrap"><table class="dt"><thead><tr><th>Adapter</th><th>Type</th><th>{{$w.Network}}</th><th>MAC</th><th>Guest addresses</th></tr></thead><tbody>{{range .NICs}}<tr><td>{{.NIC.Label}}{{if not .NIC.Connected}} <span class="chip skip">Disconnected</span>{{end}}</td><td>{{.NIC.AdapterType}}</td><td>{{if .NetworkAnchor}}<a href="#{{.NetworkAnchor}}">{{.NIC.Network}}</a>{{else}}{{.NIC.Network}}{{end}}</td><td>{{.NIC.MAC}}{{if .NIC.ManualMAC}} (manual){{end}}</td><td>{{join .NIC.IPAddresses ", "}}{{if .NIC.Static}} <span class="chip warning">Static</span>{{end}}{{if .NIC.Gateways}}<div class="muted small">Gateway {{join .NIC.Gateways ", "}}{{if .NIC.DNSServers}} · DNS {{join .NIC.DNSServers ", "}}{{end}}</div>{{end}}</td></tr>{{else}}<tr><td colspan="5" class="empty">No network adapters.</td></tr>{{end}}</tbody></table></div></div>
{{if $vm.Snapshots}}<div class="pane" data-pane="snapshots"><h3 class="ph">Snapshots</h3><div class="tw-wrap"><table class="dt"><thead><tr><th>Snapshot</th><th>Created</th><th>Quiesced</th></tr></thead><tbody>{{range $vm.Snapshots}}<tr><td>{{.Name}}</td><td>{{date .Created}}</td><td>{{yesno .Quiesced}}</td></tr>{{end}}</tbody></table></div></div>{{end}}
</section>{{end}}{{end}}`

// vmReportVMContext passes the model and one VM to the "vm" template.
type vmReportVMContext struct {
	Model *vmReportModel
	VM    vmReportVM
}

var vmReportHTMLTmpl = func() *template.Template {
	functions := template.FuncMap{"vmCtx": func(model *vmReportModel, vm vmReportVM) vmReportVMContext {
		return vmReportVMContext{Model: model, VM: vm}
	}}
	for name, function := range vmReportHTMLFunctions {
		functions[name] = function
	}
	return template.Must(template.New("vmreport").Funcs(functions).Parse(vmReportTemplate))
}()

// vmReportHTML renders the interactive report, opening at start.
func vmReportHTML(model *vmReportModel, start string) ([]byte, error) {
	var page bytes.Buffer
	err := vmReportHTMLTmpl.Execute(&page, map[string]any{
		"Model": model, "Start": start, "Style": template.CSS(vmReportStyle), "Script": template.JS(vmReportScript),
		"ScriptHash": vmReportScriptHash, "Icons": template.HTML(vmReportIcons), "Docx": vmReportDocx, "PDF": vmReportPDF,
	})
	return page.Bytes(), err
}
