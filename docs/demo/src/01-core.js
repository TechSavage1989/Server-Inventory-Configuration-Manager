/* Demo backend for Server Inventory and Migration Manager.
   Runs inside the real application page: answers every /api/ request in the
   browser with a simulated, fictional estate. Nothing leaves the page. */
(function(){
'use strict';
var D=window.__DEMO__||{};
function clone(x){return x==null?x:JSON.parse(JSON.stringify(x));}
function now(){return Date.now();}
function iso(t){return new Date(t==null?Date.now():t).toISOString();}
var ZERO='0001-01-01T00:00:00Z';
function hhmmss(t){var d=new Date(t==null?Date.now():t);function p(n){return (n<10?'0':'')+n;}return p(d.getHours())+':'+p(d.getMinutes())+':'+p(d.getSeconds());}
function stamp(t){var d=new Date(t==null?Date.now():t);function p(n){return (n<10?'0':'')+n;}return d.getFullYear()+'-'+p(d.getMonth()+1)+'-'+p(d.getDate())+' '+hhmmss(t);}
function rnd(a,b){return a+Math.random()*(b-a);}
function pick(a){return a[Math.floor(Math.random()*a.length)];}
function uuid(){var h='0123456789abcdef',s='';for(var i=0;i<32;i++){s+=h[Math.floor(Math.random()*16)];}return s.slice(0,8)+'-'+s.slice(8,12)+'-4'+s.slice(13,16)+'-8'+s.slice(17,20)+'-'+s.slice(20);}
function GB(n){return Math.round(n*1073741824);}
var COMPUTER='HV01',DOMAIN='EXAMPLE',ACCOUNT='EXAMPLE\\admin';
var timers=[];function later(ms,fn){timers.push({at:now()+ms,fn:fn});}

/* ---------- main application state ---------- */
var logs=[];
function log(text){logs.push('['+stamp()+'] '+text);}
var productionDefaults={schema:1,reservePercent:10,reserveGB:50,stopPercent:5,stopGB:20,growthPercent:10,growthMinGB:4,maxCopies:4,maxPerDatastore:2,maxPerSourceHost:2,maxPerTarget:2,bandwidth:{limitMbps:0,businessMbps:0,businessStart:0,businessEnd:0}};
var S={
 aclExport:{running:false,skipRequested:false},ui:{schema:1,confirmStyle:'tick'},
 production:{settings:clone(productionDefaults),defaults:clone(productionDefaults),holds:[],active:[],running:0,limit:'no limit'},
 appVersion:'3.3.0',closing:false,collectorVersion:'3.3.0',
 update:{status:'current',title:'Up to date',message:'Installed v3.3.0. Latest v3.3.0. (Demo: no updates are downloaded.)',canCheck:true,channel:'stable',stableVersion:'3.3.0',latestVersion:'3.3.0',available:false,canDownload:false},
 buildIdentity:'demo',computerName:COMPUTER,signedInAccount:ACCOUNT,isAdministrator:true,powerShellPath:'C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe',powerShellVersion:'5.1.26100.2161',
 defaultOutputRoot:'D:\\Reports',status:'idle',phase:'Ready',message:'Choose what to collect, then start.',currentStep:'',progress:0,completedSteps:0,totalSteps:0,warningCount:0,errorCount:0,startedAtUnixMs:0,endedAtUnixMs:0,running:false,runId:'',outputRoot:'D:\\Reports',
 packagePath:'',zipPath:'',docxReportPath:'',htmlReportPath:'',summaryTextPath:'',launcherLogPath:'',diagnosticPath:'',
 loadedReportAvailable:true,loadedReportPath:'D:\\Reports\\HV01_2026-11-27_1810\\HV01-Server-Inventory.html',loadedReportPackage:'D:\\Reports\\HV01_2026-11-27_1810',loadedReportDocxPath:'D:\\Reports\\HV01_2026-11-27_1810\\HV01-Server-Inventory.docx',loadedReportSummaryPath:'D:\\Reports\\HV01_2026-11-27_1810\\HV01-Summary.txt',loadedReportName:'HV01_2026-11-27_1810',loadedReportSerial:1,
 restore:{status:'idle',packages:{lastCollection:'D:\\Reports\\HV01_2026-11-27_1810',loadedReport:'D:\\Reports\\HV01_2026-11-27_1810'},items:[],logs:[]},
 migrate:null,vmware:null,convert:null
};
var savedPrefs=null;

/* ---------- request plumbing ---------- */
var routes=[];
function on(test,fn){routes.push({test:test,fn:fn});}
function J(status,data){return {status:status,body:JSON.stringify(data)};}
function ok(extra){var o={ok:true};if(extra){for(var k in extra){o[k]=extra[k];}}return J(200,o);}
function bad(msg,extra){var o={ok:false,error:msg};if(extra){for(var k in extra){o[k]=extra[k];}}return J(400,o);}
function dispatch(method,url,body){
 var u=new URL(url,location.href),path=u.pathname,i=path.indexOf('/api/');
 if(i<0){return J(404,{});}
 path=path.slice(i);
 var data={};try{data=body?JSON.parse(body):{};}catch(e){data={};}
 for(var r=0;r<routes.length;r++){var t=routes[r].test,m=null;
  if(typeof t==='string'){if(t===path){m=[path];}}else if(t.test(path)){m=path.match(t);}
  if(m){try{var res=routes[r].fn({method:method,path:path,query:u.searchParams,data:data,match:m});if(res){return res;}}catch(err){console.error('[demo]',path,err);return J(500,{ok:false,error:'Demo backend error: '+err.message});}}
 }
 return ok({selected:false});
}
function FakeXHR(){this.readyState=0;this.status=0;this.responseText='';this.onreadystatechange=null;this._h={};}
FakeXHR.prototype.open=function(m,u){this._m=m;this._u=u;this.readyState=1;};
FakeXHR.prototype.setRequestHeader=function(k,v){this._h[k.toLowerCase()]=v;};
FakeXHR.prototype.abort=function(){this._aborted=true;};
FakeXHR.prototype.getResponseHeader=function(){return 'application/json';};
FakeXHR.prototype.send=function(body){var x=this;setTimeout(function(){if(x._aborted){return;}var r=dispatch(x._m,x._u,body);x.status=r.status;x.responseText=r.body;x.readyState=4;if(x.onreadystatechange){x.onreadystatechange();}if(x.onload){x.onload();}},25+Math.random()*60);};
window.XMLHttpRequest=FakeXHR;

/* ---------- small endpoints ---------- */
on('/api/ui/ready',function(){return ok();});
on('/api/state',function(q){
 var since=Number(q.query.get('since')||0),out={};
 for(var k in S){out[k]=S[k];}
 out.collectionPreferences=savedPrefs;
 out.logs=logs.slice(since).map(function(t,i){return {index:since+i,text:t};});out.nextLogIndex=logs.length;
 return J(200,out);
});
on('/api/preferences',function(q){savedPrefs={schema:1,options:q.data};return ok({preferencesSaved:true});});
on('/api/ui-settings',function(q){if(['none','tick','prompt','typed'].indexOf(q.data.confirmStyle)<0){return bad('Choose how changes are confirmed.');}S.ui={schema:1,confirmStyle:q.data.confirmStyle};return ok({ui:S.ui});});
on('/api/output-root',function(q){S.defaultOutputRoot=q.data.outputRoot;S.outputRoot=q.data.outputRoot;return ok({outputRoot:q.data.outputRoot,saved:true});});
var vddk={folder:'C:\\VDDK\\vmware-vix-disklib-distrib',ready:true,version:'8.0.3 build 24022515',signer:'Broadcom Inc.',loaded:true,message:'VDDK 8.0.3 build 24022515 (signed by Broadcom Inc.) is ready. Copies of running VMware VMs read only changed blocks through it.'};
on('/api/vddk',function(q){if(q.method==='GET'){return ok({vddk:vddk});}
 vddk=q.data.folder?{folder:q.data.folder,ready:true,version:'8.0.3 build 24022515',signer:'Broadcom Inc.',loaded:false,message:'VDDK 8.0.3 build 24022515 (signed by Broadcom Inc.) is ready. Copies of running VMware VMs read only changed blocks through it.'}:{folder:'',ready:false,loaded:false,message:'Off.'};
 return ok({vddk:vddk});});
on('/api/production',function(q){var d=q.data;if(d.stopPercent>d.reservePercent){return bad('The stop level must not be above the free space kept for production.');}
 S.production.settings=d;var bw=d.bandwidth||{};S.production.limit=bw.limitMbps?(bw.limitMbps+' Mbps'+(bw.businessMbps?', '+bw.businessMbps+' Mbps in business hours':'')):'no limit';return ok({production:S.production});});
on('/api/update/check',function(){S.update.message='Installed v3.3.0. Latest v3.3.0. Checked at '+hhmmss()+'. (Demo)';S.update.status='current';return ok({update:S.update});});
on('/api/update/channel',function(q){var test=q.data.channel==='test';S.update.channel=q.data.channel;
 S.update.message=test?'Installed 3.3.0. You chose the Test channel; no newer test build is published in this demo.':'Installed 3.3.0.';return ok({update:S.update});});
on('/api/update/download',function(){return bad('Updates are not downloaded in the demo.');});
on('/api/update/install',function(){return bad('Updates are not installed in the demo.');});
on('/api/reset',function(){if(S.running){return bad('A collection is running.');}S.status='idle';S.phase='Ready';S.message='Choose what to collect, then start.';S.progress=0;S.currentStep='';return ok();});
on('/api/skip-acl-export',function(){S.aclExport.skipRequested=true;return J(202,{ok:true});});

/* ---------- opening files ---------- */
var openMap={
 html:'report.html',loadedhtml:'report.html',summary:'files/HV01-Summary.txt',loadedsummary:'files/HV01-Summary.txt',
 vmwarereport:'files/VMware-Readiness-Report.html',vmwarediscovery:'files/VMware-Discovery-Report.html',vmwarereportdocx:'files/Discovery-and-Readiness-Report.docx',vmwarereportpdf:'files/Discovery-and-Readiness-Report.pdf',
 vmwareplancsv:'files/Migration-Plan.csv',updaterelease:'https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest'
};
function demoOpen(href,what){
 var a=document.createElement('a');a.href=href;a.target='_blank';a.rel='noopener';if(/\.(docx|csv)$/i.test(href)){a.download=href.split('/').pop();}
 document.body.appendChild(a);a.click();a.remove();
 demoToast((what||'Opened')+' (demo copy).');
}
function demoToast(text){var t=document.getElementById('demoToast');if(!t){t=document.createElement('div');t.id='demoToast';t.setAttribute('role','status');t.style.cssText='position:fixed;left:50%;bottom:46px;transform:translateX(-50%);background:#1f2937;color:#fff;padding:8px 14px;border-radius:8px;font:13px/1.4 system-ui,Segoe UI,sans-serif;z-index:99999;box-shadow:0 4px 16px rgba(0,0,0,.25);max-width:80vw;transition:opacity .3s';document.body.appendChild(t);}
 t.textContent=text;t.style.opacity='1';clearTimeout(t._h);t._h=setTimeout(function(){t.style.opacity='0';},3200);}
window.__demoToast=demoToast;
on('/api/open',function(q){
 var k=String(q.data.kind||''),href=openMap[k];
 if(href){demoOpen(href,'Opened '+href.split('/').pop());}
 else{var what={vmwarereportdocx:'the discovery and readiness report in Word',docx:'the Word copy of the inventory report',loadeddocx:'the Word copy of the inventory report',vmwarerunbook:'the migration runbook (HTML, with tick boxes per step)',vmwarerunbookdocx:'the migration runbook in Word',replrunbook:'the replication runbook (HTML)',replrunbookdocx:'the replication runbook in Word',folder:'the report folder in Explorer',loadedfolder:'the report folder in Explorer',zip:'Explorer with the ZIP selected',diagnostics:'the diagnostics folder',vmwarefolder:'the discovery folder',receivefolder:'the receiver folder',guestfolder:'the guest settings folder',convertfolder:'the output folder',migrationlogs:'the migration log folder',restorerollback:'the restore rollback folder',vddklog:'the VDDK log',receiverlog:'the receiver log',hvbuildreport:'the build report',hvbuildfolder:'the build folder',comparereport:'the comparison report',comparefolder:'the comparison folder'}[k]||'that file or folder';
  demoToast('In the real application this opens '+what+' on this computer.');}
 return ok();
});
on('/api/vmlog',function(q){
 var vm=q.method==='GET'?(q.query.get('vm')||''):String(q.data.vm||'');
 if(q.method==='POST'){demoToast('In the real application Save as asks where to save '+(vm||'the VMware log')+' and writes it there.');return ok({cancelled:true});}
 var name=vm||'VMware',base='C:\\Users\\admin\\AppData\\Local\\Server Inventory Configuration Manager\\Logs\\';
 var lines=vm?['2026-11-27 21:10:04 Plan: '+vm+': Recorded '+vm+'\'s network settings from vCenter (1 adapter(s)).','2026-11-27 21:10:05 Plan: '+vm+': Taking the replication snapshot.','2026-11-27 21:10:06 Plan: '+vm+': Hard disk 1: reading through VDDK (nbdssl), only the blocks this round needs.','2026-11-27 21:10:31 Plan: '+vm+': Hard disk 1: changed copy finished, 1.2 GB read, 1.2 GB written.','2026-11-27 21:10:32 Plan: '+vm+': changed blocks through VDDK, 0.1 GB reported by change tracking, 1.2 GB read, 1.2 GB written in 26s.']:['2026-11-27 21:10:00 Plan: 3 VM(s) are due for a sync.','2026-11-27 21:10:32 Plan: APP01: changed blocks through VDDK, 1.2 GB read in 26s.'];
 return ok({vm:vm,text:lines.join('\n')+'\n',path:vm?base+'VMs\\'+name+'.log':base+'VMware.log',exists:true});
});
on('/api/pick',function(q){var k=q.data.kind;return ok({selected:true,path:k==='remote'?'C:\\ClusterStorage\\Volume1\\Hyper-V':k==='file'?'D:\\Media\\SIMM-Receiver.iso':'D:\\Migration'});});
on('/api/browse',function(){return ok({selected:true,path:'D:\\Reports'});});
on('/api/browse-report',function(){S.loadedReportSerial++;S.loadedReportAvailable=true;S.loadedReportName='HV01_2026-11-27_1810';return ok({selected:true,path:S.loadedReportPath});});
var profiles=[{name:'Example Co (head office)',description:'Copies on CSV1, 5-minute syncs, 2 at a time',copyRoot:'C:\\ClusterStorage\\Volume1\\Hyper-V\\Copies',vmFolder:'C:\\ClusterStorage\\Volume1\\Hyper-V',syncMinutes:5,concurrency:2,keepMac:false,startVms:true,cluster:true,disconnectSource:true,fixUp:true,ejectMedia:true,networks:[]}];
on('/api/profiles',function(q){var d=q.data;if(q.method==='GET'){return ok({profiles:profiles});}
 if(d.action==='save'){if(!d.profile||!d.profile.name){return bad('Name the defaults.');}profiles=profiles.filter(function(p){return p.name.toLowerCase()!==d.profile.name.toLowerCase();});profiles.push(d.profile);profiles.sort(function(a,b){return a.name.localeCompare(b.name);});return ok({profiles:profiles});}
 if(d.action==='delete'){profiles=profiles.filter(function(p){return p.name!==d.name;});return ok({profiles:profiles});}
 return bad('Unknown action.');});

window.__DEMO_INTERNAL__={D:D,S:S,on:on,ok:ok,bad:bad,J:J,clone:clone,now:now,iso:iso,ZERO:ZERO,hhmmss:hhmmss,stamp:stamp,rnd:rnd,pick:pick,uuid:uuid,GB:GB,log:log,later:later,timers:timers,COMPUTER:COMPUTER,ACCOUNT:ACCOUNT,demoToast:demoToast,demoOpen:demoOpen,logs:logs,setPrefs:function(p){savedPrefs=p;},getPrefs:function(){return savedPrefs;}};
})();
