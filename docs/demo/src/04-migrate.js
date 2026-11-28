/* Migration plans, All jobs and servers, Replicate servers, Start VMs from
   copies, fix-ups after cutover, Roll back and the VM console. */
(function(){
'use strict';
var X=window.__DEMO_INTERNAL__,D=X.D,S=X.S,V=S.vmware,VM=X.VM,on=X.on,ok=X.ok,bad=X.bad,J=X.J,clone=X.clone,now=X.now,iso=X.iso,stamp=X.stamp,hhmmss=X.hhmmss,rnd=X.rnd,GB=X.GB,uuid=X.uuid,vlog=X.vlog;
var tickers=X.tickers;
var MIN=60000;
function T(t){return t?iso(t):undefined;}
function vddkOn(){return true;}
window.__demoVddk=function(){return vddkOn();};

/* ---------- migration plans ---------- */
var PLANS=[];X.PLANS=PLANS;var planSeq=1;
var defaultSettings={startAt:iso(now()-3*3600000),syncMinutes:15,concurrency:2,throttle:{limitMbps:0,businessMbps:400,businessStart:8,businessEnd:18},copyRoot:'C:\\ClusterStorage\\Volume1\\Hyper-V\\Copies',vmFolder:'C:\\ClusterStorage\\Volume1\\Hyper-V',keepMac:false,startVms:true,cluster:true,quiesce:true,testSwitch:'',disconnectSource:true,fixUp:true,ejectMedia:true};
function invVM(id){return X.findVM(id)||{};}
function newVM(id,o){var src=invVM(id);return Object.assign({id:id,name:src.name||id,wave:0,state:'waiting',seeded:false,rounds:0,failures:0,copyFolder:defaultSettings.copyRoot+'\\'+(src.name||id),disks:[]},o||{});}
function throttleText(t){if(!t||(!t.limitMbps&&!t.businessMbps)){return 'no limit';}return (t.limitMbps?t.limitMbps+' Mbps':'no limit')+(t.businessMbps?', '+t.businessMbps+' Mbps '+String(t.businessStart).padStart(2,'0')+':00 to '+String(t.businessEnd).padStart(2,'0')+':00 on weekdays':'');}
function seedPlans(){
 var t=now();
 var p={id:'p'+(planSeq++),name:'Head Office',server:'vcenter.example.com',created:iso(t-26*3600000),paused:false,settings:clone(defaultSettings),vms:[
  newVM('vm-1009',{wave:1,state:'done',seeded:true,rounds:9,lastSync:iso(t-20*3600000),lastEnded:iso(t-20*3600000),lastMode:'changed',lastVia:'vddk',lastRead:GB(1.2),lastWritten:GB(1.2),lastTracked:GB(1.2),lastSeconds:41,testedAt:iso(t-22*3600000),hyperVId:'6f1c7a3e-1b2c-4d5e-8f90-0a1b2c3d4e51',cutOver:iso(t-20*3600000),disks:['PRINT01-disk0.vhdx']}),
  newVM('vm-1001',{wave:2,state:'ready',seeded:true,rounds:31,lastSync:iso(t-6*MIN),lastEnded:iso(t-6*MIN),lastMode:'changed',lastVia:'vddk',lastRead:GB(0.6),lastWritten:GB(0.6),lastTracked:GB(0.6),lastSeconds:22,testedAt:iso(t-2*3600000)}),
  newVM('vm-1003',{wave:3,state:'ready',seeded:true,rounds:28,lastSync:iso(t-11*MIN),lastEnded:iso(t-11*MIN),lastMode:'changed',lastVia:'vddk',lastRead:GB(3.4),lastWritten:GB(3.4),lastTracked:GB(3.4),lastSeconds:57}),
  newVM('vm-1012',{wave:3,state:'waiting',seeded:false}),
  newVM('vm-1011',{wave:4,state:'waiting',seeded:false})]};
 p.vms.forEach(function(v){var s=invVM(v.id);v.name=s.name||v.name;});
 PLANS.push(p);
 // Earlier cutover: a rollback record and a finished fix-up for PRINT01.
 X.recordCutover({name:'PRINT01',vmId:'vm-1009',hyperVId:'6f1c7a3e-1b2c-4d5e-8f90-0a1b2c3d4e51',host:'HV01',at:t-20*3600000,plan:p.id,method:'plan'});
 V.fixups.push({vm:'PRINT01',hyperVId:'6f1c7a3e-1b2c-4d5e-8f90-0a1b2c3d4e51',host:'',via:'PowerShell Direct on this host',state:'done',message:'Fixed up: IP settings carried over, VMware Tools removed, Windows activated.',started:iso(t-20*3600000+120000),finished:iso(t-20*3600000+600000),steps:fixSteps('PRINT01',true)});
}
function fixSteps(name,done){return [{step:'Wait for Windows',status:'done',message:'Windows in '+name+' answered after 1 minute 50 seconds.'},{step:'Hidden VMware adapters',status:'done',message:'Removed 1 hidden vmxnet3 adapter.'},{step:'Network',status:'done',message:'Put 192.0.2.x/24, gateway 192.0.2.1 and DNS 192.0.2.21, 192.0.2.22 on "Ethernet" (matched by MAC). No duplicate address.'},{step:'Data disks',status:'done',message:'All disks online.'},{step:'VMware Tools',status:'done',message:'Removed VMware Tools and its leftovers; Windows restarts in 60 seconds.'},{step:'Activation',status:done?'done':'warning',message:'Windows is activated (KMS).'}];}
var live={};
function planView(p){
 var sum={total:p.vms.length,waiting:0,copying:0,ready:0,testing:0,scheduled:0,done:0,failed:0},t=now();
 var vms=p.vms.map(function(v){var o={};for(var k in v){if(k[0]!=='_'){o[k]=v[k];}}
  if(v.state==='waiting'){sum.waiting++;}else if(v.state==='copying'||v.state==='cutting'){sum.copying++;}else if(v.state==='ready'){sum.ready++;}else if(v.state==='testing'){sum.testing++;}else if(v.state==='done'){sum.done++;}else if(v.state==='failed'){sum.failed++;}
  if(v.cutoverAt&&v.state!=='done'){sum.scheduled++;}
  if(!p.paused&&v.state!=='done'&&v.state!=='cutting'&&v.method!=='offline'){var ns=v.lastEnded?Date.parse(v.lastEnded)+p.settings.syncMinutes*MIN:t;o.nextSync=iso(Math.max(ns,t));}
  var L=live[p.id+'|'+v.id];if(L){var el=(t-L.started)/1000;o.live={cutover:L.cutover,percent:Math.min(100,Math.floor(100*L.done/L.total)),bytesRead:Math.round(L.done),bytesPerSecond:el>0?Math.round(L.done/el):0,secondsLeft:L.rate?Math.round((L.total-L.done)/L.rate):0,elapsed:Math.round(el)};}
  if(!o.testedAt){o.testedAt=X.ZERO;}
  return o;});
 return {id:p.id,name:p.name,server:p.server,created:p.created,paused:p.paused,settings:p.settings,throttle:throttleText(p.settings.throttle),summary:sum,blocked:'',vms:vms};
}
function planById(id){return PLANS.filter(function(p){return p.id===id;})[0];}
on('/api/plans',function(q){var d=q.data;
 if(q.method==='GET'){var waves={};(VM.plan&&VM.plan.vms||[]).forEach(function(p){waves[p.id]=p.wave;});
  var out={ok:true,plans:PLANS.map(planView),sweeping:Object.keys(live).length>0,blocked:'',hyperVHere:true,inventory:{open:!!VM.inventory,server:VM.inventory&&VM.inventory.server,serverType:VM.inventory&&VM.inventory.serverType,live:true},waves:waves,testing:false};
  if(q.query.get('switches')==='1'){out.switches=[{name:'SET-vSwitch0',type:'External'},{name:'Isolated test',type:'Private'}];}
  return J(200,out);}
 if(d.action==='create'){if(!d.vmIds||!d.vmIds.length){return bad('Choose at least one VM.');}var waves={};(VM.plan&&VM.plan.vms||[]).forEach(function(p){waves[p.id]=p.wave;});
  var bad1=d.vmIds.filter(function(id){var m=invVM(id);return m.encrypted&&(m.disks||[]).some(function(x){return x.encrypted;});});
  var p={id:'p'+(planSeq++),name:d.name||'Plan',server:VM.inventory?VM.inventory.server:'vcenter.example.com',created:iso(),paused:false,settings:Object.assign(clone(defaultSettings),d.settings||{}),vms:d.vmIds.map(function(id){return newVM(id,{wave:waves[id]||0,method:(d.methods||{})[id]||''});})};
  if(!p.settings.startAt||p.settings.startAt===X.ZERO){p.settings.startAt=iso();}
  PLANS.push(p);vlog('[INFO] Created the migration plan '+p.name+' with '+p.vms.length+' VM(s).');return ok({id:p.id});}
 var p=planById(d.id);if(!p){return bad('That plan no longer exists.');}
 var pick=function(){return p.vms.filter(function(v){return !d.vmIds||!d.vmIds.length||d.vmIds.indexOf(v.id)>=0;});};
 if(d.action==='update'){Object.assign(p.settings,d.settings||{});return ok();}
 if(d.action==='pause'||d.action==='resume'){p.paused=d.action==='pause';if(p.paused){Object.keys(live).forEach(function(k){if(k.indexOf(p.id+'|')===0&&!live[k].cutover){var v=live[k].vm;v.state=v.seeded?'ready':'waiting';delete live[k];}});}return ok();}
 if(d.action==='sync'){var ans={queued:0,skipped:[]};pick().forEach(function(v){if(v.method==='offline'){ans.skipped.push(v.name+' copies at its cutover');return;}if(['waiting','ready','failed'].indexOf(v.state)<0){ans.skipped.push(v.name+' is '+(v.state==='copying'?'copying now':v.state==='testing'?'being tested':v.state==='done'?'cut over':'busy'));return;}v.syncNow=true;ans.queued++;});if(p.paused){ans.paused=true;}return ok({sync:ans});}
 if(d.action==='cutover'){if(String(d.confirm||'').toLowerCase()!==p.name.toLowerCase()){return bad('Confirm with the plan\'s name to cut over: each chosen VM\'s guest is shut down, its last changes are copied and the Hyper-V VM is created.');}
  var at=d.at&&d.at!=='now'?Date.parse(d.at):now();pick().forEach(function(v){if(v.state!=='done'){v.cutoverAt=iso(at);}});return ok();}
 if(d.action==='cancelCutover'){pick().forEach(function(v){delete v.cutoverAt;});return ok();}
 if(d.action==='method'){pick().forEach(function(v){v.method=d.method||'';if(v.method==='offline'&&v.state==='failed'){v.state='waiting';v.error='';}});return ok();}
 if(d.action==='markTested'){pick().forEach(function(v){v.testedAt=iso();});return ok();}
 if(d.action==='test'){var v=pick()[0];if(!v||v.state!=='ready'){return bad('Only a VM that is in sync can be tested.');}v.state='testing';v.test={name:v.name+'-TEST',vmId:uuid(),folder:p.settings.copyRoot+'\\Test cutovers\\'+v.name,copies:[v.name+'-disk0.vhdx'],children:[v.name+'-disk0-test.avhdx'],started:iso()};vlog('[INFO] Started '+v.test.name+' on differencing disks, its adapters not connected.');return ok();}
 if(d.action==='endTest'){var v2=pick()[0];if(!v2||!v2.test){return bad('That VM is not being tested.');}vlog('[INFO] Ended '+v2.test.name+': removed it and its differencing disks by ID. The copies were not touched.');delete v2.test;v2.state='ready';v2.testedAt=iso();return ok();}
 if(d.action==='remove'){p.vms=p.vms.filter(function(v){return d.vmIds.indexOf(v.id)<0;});return ok();}
 if(d.action==='delete'){if(String(d.confirm||'').toLowerCase()!==p.name.toLowerCase()){return bad('Confirm with the plan\'s name to delete it.');}PLANS.splice(PLANS.indexOf(p),1);return ok();}
 return bad('Unknown action.');
});
function startRound(p,v,cutover){var src=invVM(v.id),used=src.usedBytes||GB(40);
 var total=cutover?(v.method==='offline'&&!v.seeded?used:GB(rnd(0.3,1.2))):(v.seeded?GB(rnd(0.4,4)):used);
 var rate=v.seeded||cutover?GB(rnd(0.25,0.45)):GB(rnd(0.9,1.4));
 live[p.id+'|'+v.id]={vm:v,plan:p,started:now(),total:total,done:0,rate:rate,cutover:cutover,phase:cutover?'shutdown':'copy',t:0};
 v.state=cutover?'cutting':'copying';v.lastAttempt=iso();v.syncNow=false;
 if(!cutover){addTransfer(p,v,total);}
}
function addTransfer(p,v,total){var tr=X.transfer('plan-'+v.id,v.name+': Hard disk 1',v.name+' (Hard disk 1)',p.settings.copyRoot+'\\'+v.name,total);tr.kind='plan';return tr;}
tickers.push(function(){
 var t=now();
 Object.keys(live).forEach(function(k){var L=live[k],v=L.vm,p=L.plan;L.t++;
  if(L.cutover&&L.phase==='shutdown'){if(L.t===1){vlog('[INFO] '+v.name+': asked the guest to shut down through VMware Tools.');var s=invVM(v.id);}if(L.t>=3){var s2=invVM(v.id);s2.powerState='poweredOff';L.phase='copy';vlog('[INFO] '+v.name+' is off. Copying the last changes.');}return;}
  if(L.phase==='copy'){L.done=Math.min(L.total,L.done+L.rate);var tr=V.transfers.filter(function(x){return x.id==='plan-'+v.id;})[0];if(tr){tr.percent=Math.floor(100*L.done/L.total);tr.bytesRead=L.done;tr.done=Math.round(tr.chunks*L.done/L.total);tr.bytesPerSecond=L.rate;tr.etaSeconds=Math.round((L.total-L.done)/L.rate);tr.updated=iso();}
   if(L.done<L.total){return;}
   if(tr){tr.state='done';tr.percent=100;tr.finished=iso();}
   v.rounds=(v.rounds||0)+1;v.lastMode=v.seeded?'changed':'seed';v.lastVia='vddk';v.lastRead=L.total;v.lastWritten=L.total;v.lastTracked=v.seeded?L.total:0;v.lastSeconds=Math.max(1,Math.round((t-L.started)/1000));v.lastSync=iso();v.lastEnded=iso();
   if(!v.seeded){v.lastWhy='First copy: read only the areas vSphere reports allocated.';}else{delete v.lastWhy;}
   v.seeded=true;v.disks=[v.name+'-disk0.vhdx'];
   if(!L.cutover){v.state='ready';delete live[k];return;}
   L.phase='create';L.t=0;vlog('[INFO] '+v.name+': final copy verified. Creating the Hyper-V VM.');return;}
  if(L.phase==='create'&&L.t>=2){delete live[k];v.state='done';v.hyperVId=uuid();v.cutOver=iso();delete v.cutoverAt;var src=invVM(v.id);
   vlog('[OK] '+v.name+' was created on '+(p.settings.cluster?'HVCLU01 (highly available)':'HV01')+' and started.');
   X.afterCutover(src.name?src:{name:v.name,id:v.id},{hyperVId:v.hyperVId,host:'HV01',plan:p.id,method:'plan',fixUp:p.settings.fixUp});}
 });
 PLANS.forEach(function(p){if(p.paused){return;}var running=p.vms.filter(function(v){return live[p.id+'|'+v.id];}).length;
  p.vms.forEach(function(v){if(v.cutoverAt&&Date.parse(v.cutoverAt)<=t&&(v.state==='ready'||(v.state==='waiting'&&v.method==='offline'))&&!live[p.id+'|'+v.id]){startRound(p,v,true);running++;}});
  if(Date.parse(p.settings.startAt||0)>t){return;}
  p.vms.forEach(function(v){if(running>=(p.settings.concurrency||2)||live[p.id+'|'+v.id]||v.method==='offline'){return;}
   var due=v.syncNow||v.state==='waiting'||((v.state==='ready'||v.state==='failed')&&v.lastEnded&&Date.parse(v.lastEnded)+p.settings.syncMinutes*MIN<=t);
   if(due&&['waiting','ready','failed'].indexOf(v.state)>=0){startRound(p,v,false);running++;}});});
 V.planCopy=Object.keys(live).length?{plans:1,vms:Object.keys(live).length}:null;
});

/* ---------- after a cutover: adapters, fix-up, rollback record ---------- */
var RB=V.rollback;
X.recordCutover=function(o){var id='r'+(RB.records.length+1);var dc=/^DC/i.test(o.name);
 RB.records.unshift({id:id,method:o.method==='copies'?'copies':o.method==='platform'?'platform':'move',name:o.name,plan:o.plan?((planById(o.plan)||{}).name||o.plan):'',cutoverAt:iso(o.at||now()),state:'ready',dc:dc,dcWarning:dc?'This is a domain controller. Starting the old copy after the new one has run on the network can cause USN rollback: the other domain controllers ignore its changes and replication between them breaks for good. Only roll back if the new domain controller never replicated with the others, or if you will repair the domain right away.':'',
  source:{kind:o.method==='copies'?'agent':'vmware',server:'vcenter.example.com',vmId:o.vmId||'',name:o.name,adapterRecord:'C:\\Users\\admin\\AppData\\Local\\Server Inventory Configuration Manager\\VMware\\vcenter.example.com\\Move\\'+o.name+'-adapters.json'},new:{kind:'hyperv',id:o.hyperVId,name:o.name,host:o.host||'HV01'},
  planned:[{key:'stop',title:'Shut the new VM down'},{key:'check',title:'Check the new VM is off before anything else happens, so its addresses are never used twice'},{key:'network',title:'Put the old VM\'s network adapters back'},{key:'start',title:'Start the old VM'}],
  window:'Roll back works as long as the old VM stays in vcenter.example.com with its disks. Nothing deletes it.',dataWarning:'Anything written on the new VM since the cutover '+stamp(o.at||now()).slice(0,16)+' is not on the old one.'});
 RB.serial++;return id;};
X.afterCutover=function(vm,o){
 vlog('[INFO] Set every adapter of the old VM '+vm.name+' to not connect (saved first, so Roll back can put them back).');
 (vm.nics||[]).forEach(function(n){n.connected=false;});
 X.recordCutover({name:vm.name,vmId:vm.id,hyperVId:o.hyperVId,host:o.host,plan:o.plan,method:o.method});
 if(o.fixUp===false){return;}
 var f={vm:vm.name,hyperVId:o.hyperVId,host:'',via:'PowerShell Direct on this host',state:'waiting',message:'Waiting for Windows in '+vm.name+' to start.',started:iso(),steps:[]};
 V.fixups=V.fixups.filter(function(x){return x.vm!==vm.name;});V.fixups.push(f);X.runFixup(f);
};
X.runFixup=function(f){f.state='waiting';f.message='Waiting for Windows in '+f.vm+' to start.';f.steps=[];f._t=0;f._run=true;delete f.finished;};
tickers.push(function(){V.fixups.forEach(function(f){if(!f._run){return;}f._t++;var all=fixSteps(f.vm,true);
 if(f._t<4){return;}if(f._t===4){f.state='running';f.message='Fixing up '+f.vm+' through PowerShell Direct.';}
 var n=Math.min(all.length,Math.floor((f._t-4)/2)+1);f.steps=all.slice(0,n);
 if(n>=all.length&&(f._t-4)>=all.length*2){f.state='done';f.message='Fixed up: IP settings carried over, VMware Tools removed, Windows activated.';f.finished=iso();f._run=false;}});});

/* rollback */
var rbRun=null;
on('/api/rollback/guides',function(){return ok({guides:clone(D.rbGuides)});});
on(/^\/api\/rollback\/(run|stop)$/,function(q){var a=q.match[1];
 if(a==='stop'){if(rbRun){rbRun.stop=true;}return ok();}
 var r=RB.records.filter(function(x){return x.id===q.data.id;})[0];if(!r){return bad('That record no longer exists.');}
 if(r.dc&&!q.data.dcConfirmed){return bad('Tick the box to confirm you understand the domain controller warning.');}
 if(RB.running){return bad('Another rollback is running.');}
 r.state='running';r.started=iso();r.steps=r.planned.map(function(s){return {key:s.key,title:s.title,state:''};});RB.running=r.id;RB.serial++;rbRun={r:r,i:0,t:0};return J(202,{ok:true});
});
tickers.push(function(){if(!rbRun){return;}var r=rbRun.r;rbRun.t++;if(rbRun.t%2){return;}var s=r.steps[rbRun.i];
 if(!s){r.state='done';r.finished=iso();r.message=r.name+' is rolled back: the old VM runs again and the new one is off (not deleted).';r.logPath='C:\\Users\\admin\\AppData\\Local\\Server Inventory Configuration Manager\\Rollback\\'+r.id+'.log';RB.running='';RB.serial++;
  var vm=X.findVMByName(r.name);if(vm){vm.powerState='poweredOn';(vm.nics||[]).forEach(function(n){n.connected=true;});}
  PLANS.forEach(function(p){p.vms.forEach(function(v){if(v.name===r.name&&v.state==='done'){v.state='ready';delete v.hyperVId;delete v.cutOver;v.notes=['Rolled back '+stamp().slice(0,16)+': the old VM runs again.'];}});});rbRun=null;return;}
 s.state='ok';s.at=iso();s.message={stop:'Shut down through the integration services.',check:'The new VM is off.',network:'Put 1 adapter back as recorded.',start:'Started the old VM in vSphere.'}[s.key]||'Done.';RB.serial++;rbRun.i++;});

/* ---------- Replicate servers (agents and jobs) ---------- */
var REPL={ok:true,pushing:false,pushLog:[],pushResults:null,serial:1,isAdministrator:true,defaultPort:44390,agents:[]};X.REPL=REPL;
function job(id,name,disks,o){var t=now();var j={spec:{id:id,name:name,disks:disks.map(function(d,i){return {source:String(i),target:name+'-disk'+i+'.vhdx'};}),receiver:{address:'192.0.2.40:44380',thumbprint:'3F9A6C0E1B7D4A25C8E0F13B6A9D2C4E7F0A1B3C5D7E9F2A4C6E8B0D1F3A5C7E'},intervalMinutes:0,throttle:{limitMbps:0},compress:true,allowLive:false,rpoMinutes:15,stopServices:[],shutdownAfterCutover:true,fullReadAtCutover:false},
 status:{state:'idle',message:'',rounds:0,recoveryPoint:X.ZERO,initialCopyAt:X.ZERO,lastStart:X.ZERO,lastEnd:X.ZERO,nextRun:iso(t+60000),lastDurationSeconds:0,lastRead:0,lastSent:0,totalSent:0,failures:0,disks:disks.map(function(sz,i){return {source:String(i),target:name+'-disk'+i+'.vhdx',size:sz,passes:0,verified:false,chunks:Math.ceil(sz/4194304),done:0,read:0,sent:0,changed:0};}),rescanNext:false,consistent:false},rpoSeconds:0,hasCode:true,_used:disks.map(function(sz){return sz*0.45;})};
 return Object.assign(j,o||{});}
function seedRepl(){var t=now();
 var a1={id:'ag1',name:'SQL01',address:'sql01.example.com:44390',thumbprint:'51AC...E9',os:'windows',pushed:true,live:{reachable:true,checked:iso(),version:'3.3.0',computer:'SQL01',platform:'Windows Server 2016 Standard',jobs:[],alerts:[]}};
 var j1=job('j-sql01','SQL01 to HV01',[GB(80),GB(400)]);j1.spec.stopServices=['MSSQLSERVER','SQLSERVERAGENT'];j1.status.state='idle';j1.status.rounds=212;j1.status.recoveryPoint=iso(t-4*MIN);j1.status.initialCopyAt=iso(t-2*86400000);j1.status.lastStart=iso(t-5*MIN);j1.status.lastEnd=iso(t-4*MIN);j1.status.lastDurationSeconds=48;j1.status.lastSent=GB(0.7);j1.status.totalSent=GB(390);j1.status.nextRun=iso(t+40000);j1.status.consistent=true;j1.status.disks.forEach(function(d){d.passes=212;d.done=d.chunks;});j1.rpoSeconds=240;j1.status.message='Up to date as of '+hhmmss(t-4*MIN)+'.';
 a1.live.jobs.push(j1);
 var a2={id:'ag2',name:'LEGACY-ERP',address:'192.0.2.90:44390',thumbprint:'77B2...1C',os:'windows',pushed:true,live:{reachable:true,checked:iso(),version:'3.3.0',computer:'LEGACY-ERP',platform:'Windows Server 2003 R2 (Enterprise)',jobs:[],alerts:[]}};
 a2.live.platform='Windows Server 2008 R2 Enterprise';
 var j2=job('j-erp','LEGACY-ERP to HV01',[GB(60),GB(250)]);j2.status.state='syncing';j2.status.rounds=0;j2.status.lastStart=iso(t-3*MIN);j2.status.message='First full copy.';j2._seedLeft=true;a2.live.jobs.push(j2);
 var a3={id:'ag3',name:'web03.example.com',address:'web03.example.com:44390',thumbprint:'90DE...44',os:'linux',pushed:true,live:{reachable:true,checked:iso(),version:'3.3.0',computer:'web03',platform:'Ubuntu 22.04.4 LTS',jobs:[],alerts:[]}};
 REPL.agents.push(a1,a2,a3);
}
function replSummary(){var s={agents:REPL.agents.length,unreachable:0,jobs:0,healthy:0,behind:0,failing:0,paused:0,cutOver:0,alerts:0};REPL.agents.forEach(function(a){if(!a.live.reachable){s.unreachable++;}a.live.jobs.forEach(function(j){s.jobs++;var st=j.status.state;if(st==='paused'){s.paused++;}else if(st==='error'){s.failing++;}else if(st==='cut-over'){s.cutOver++;}else if(j.rpoSeconds>j.spec.rpoMinutes*60){s.behind++;}else{s.healthy++;}});s.alerts+=a.live.alerts.filter(function(x){return !x.cleared;}).length;});return s;}
function stripJob(j){var o={};for(var k in j){if(k[0]!=='_'){o[k]=j[k];}}return o;}
function replView(){var v={};for(var k in REPL){v[k]=REPL[k];}v.agents=REPL.agents.map(function(a){var o=clone({id:a.id,name:a.name,address:a.address,thumbprint:a.thumbprint,os:a.os,pushed:a.pushed});o.live=Object.assign({},a.live,{jobs:a.live.jobs.map(stripJob)});return o;});v.summary=replSummary();return v;}
function agentById(id){return REPL.agents.filter(function(a){return a.id===id;})[0];}
on('/api/repl/state',function(){return J(200,replView());});
on('/api/repl/local',function(){return ok({selected:true,path:'C:\\ClusterStorage\\Volume2\\Replicas'});});
on('/api/repl/runbook',function(){return ok({path:'D:\\Reports\\Replication-Runbook.html',docx:'D:\\Reports\\Replication-Runbook.docx'});});
on('/api/repl/push',function(q){var hosts=(q.data.hosts||String(q.data.host||'').split(/[\s,;]+/)).filter(Boolean);if(!hosts.length){return bad('Type at least one server.');}
 REPL.pushing=true;REPL.pushLog=[hhmmss()+'  Installing the agent on '+hosts.length+' server(s) with the account you typed (not saved).'];REPL.pushResults=null;var lines=[];
 hosts.forEach(function(h){lines.push(h+': connected to \\\\'+h+'\\IPC$.',h+': copied the signed agent through the administrative share.',h+': created the service SIMMReplicationAgent and its firewall rule.',h+': the agent answers; its certificate is pinned.');});
 var i=0;var iv=setInterval(function(){if(i<lines.length){REPL.pushLog.push(hhmmss()+'  '+lines[i]);i++;return;}clearInterval(iv);REPL.pushing=false;REPL.pushResults=hosts.map(function(h){return {host:h,ok:true,text:'Installed.'};});
  hosts.forEach(function(h){var short=h.split('.')[0].toUpperCase();REPL.agents.push({id:'ag'+(REPL.agents.length+1),name:short,address:h+':44390',thumbprint:'',os:'windows',pushed:true,live:{reachable:true,checked:iso(),version:'3.3.0',computer:short,platform:'Windows Server 2022 Standard',jobs:[],alerts:[]}});});REPL.serial++;},700);
 return J(202,{ok:true});});
on('/api/repl/agent',function(q){var d=q.data,a=agentById(d.id||d.agent);
 if(d.action==='disks'){return ok({disks:[{id:'0',size:GB(80),system:true,volumes:[{name:'v1',mounts:[],fileSystem:'FAT32',length:104857600},{name:'v2',mounts:['C:\\'],fileSystem:'NTFS',length:GB(79.9)}]},{id:'1',size:GB(400),system:false,volumes:[{name:'v3',mounts:['D:\\'],fileSystem:'NTFS',length:GB(399.9)}]}]});}
 if(d.action==='receiverdisks'){return ok({from:'agent',address:d.address,folder:true,disks:[]});}
 if(d.action==='forget'||d.action==='uninstall'){REPL.agents=REPL.agents.filter(function(x){return x!==a;});REPL.serial++;return ok();}
 if(d.action==='add'){REPL.agents.push({id:'ag'+(REPL.agents.length+1),name:d.name||d.address,address:d.address,thumbprint:d.thumbprint||'',os:'windows',pushed:false,live:{reachable:true,checked:iso(),version:'3.3.0',computer:d.name||'',platform:'Windows Server 2022',jobs:[],alerts:[]}});REPL.serial++;return ok();}
 return ok();});
on('/api/repl/job',function(q){var d=q.data,a=agentById(d.agent);if(!a){return bad('That agent is not in the console.');}
 var j=a.live.jobs.filter(function(x){return x.spec.id===d.id;})[0];
 if(d.action==='save'){var spec=d.spec||{};if(j){Object.assign(j.spec,spec);return ok({job:stripJob(j)});}
  var sizes=(spec.disks||[{source:'0'}]).map(function(x,i){return i?GB(400):GB(80);});var nj=job('j-'+Math.random().toString(16).slice(2,10),spec.name||(a.name+' to HV01'),sizes,{});Object.assign(nj.spec,spec,{id:nj.spec.id});
  if(d.local){nj.spec.receiver={address:'192.0.2.40:44380',thumbprint:'3F9A6C0E1B7D4A25C8E0F13B6A9D2C4E7F0A1B3C5D7E9F2A4C6E8B0D1F3A5C7E'};}
  nj.status.state='syncing';nj.status.lastStart=iso();nj._seedLeft=true;a.live.jobs.push(nj);REPL.serial++;return ok({job:stripJob(nj)});}
 if(!j){return bad('That job no longer exists.');}
 if(d.action==='log'){return ok({lines:[stamp(now()-2*86400000)+'  Round 1 (first full copy) finished in 41 minutes.',stamp(Date.parse(j.status.lastEnd)||now())+'  Round '+j.status.rounds+' finished in '+j.status.lastDurationSeconds+' seconds; recovery point '+stamp(Date.parse(j.status.recoveryPoint)||now())+'. Change tracking read 1.9 GB of NTFS changes.']});}
 if(['cutover','delete','finish-by-hand'].indexOf(d.action)>=0&&String(d.typed||'').toLowerCase()!==j.spec.name.toLowerCase()){return bad('Confirm with the job\'s name, '+j.spec.name+'.');}
 if(d.action==='schedule'){j.spec.cutoverAt=d.at||undefined;return ok();}
 if(d.action==='pause'){j.status.state='paused';j.status.message='Paused by an operator.';return ok();}
 if(d.action==='resume'){j.status.state='idle';j.status.nextRun=iso(now()+5000);return ok();}
 if(d.action==='sync'||d.action==='rescan'){if(j.status.state==='idle'||j.status.state==='error'){j.status.nextRun=iso(now()+1000);}return ok();}
 if(d.action==='clear'){a.live.alerts.forEach(function(x){x.cleared=true;});return ok();}
 if(d.action==='cutover'){j.status.state='cutting-over';j.status.phase='stopping services';j._cut={t:0};j.status.message='Stopping '+(j.spec.stopServices.length?j.spec.stopServices.join(', '):'nothing')+', then the final round.';return ok();}
 if(d.action==='cancel-cutover'){j.status.state='idle';delete j._cut;j.status.message='Cutover canceled; the services were started again.';return ok();}
 if(d.action==='finish-by-hand'){j.status.state='cut-over';j.status.cutoverDone=true;return ok();}
 if(d.action==='delete'){a.live.jobs.splice(a.live.jobs.indexOf(j),1);return ok();}
 return ok();});
tickers.push(function(){var t=now();REPL.agents.forEach(function(a){a.live.checked=iso();a.live.jobs.forEach(function(j){var st=j.status;
 if(st.recoveryPoint&&st.recoveryPoint!==X.ZERO){j.rpoSeconds=Math.round((t-Date.parse(st.recoveryPoint))/1000);}
 if(j._cut){j._cut.t++;if(j._cut.t===3){st.phase='final round';}if(j._cut.t>=9){st.state='cut-over';st.cutoverDone=true;st.phase='';st.recoveryPoint=iso();st.message='The final round finished. '+(j.spec.shutdownAfterCutover?'The server was shut down.':'');delete j._cut;X.recordCutover({name:a.name,hyperVId:'',host:'HV01',method:'copies'});}return;}
 if(st.state==='paused'||st.state==='cut-over'||st.state==='held'){return;}
 if(st.state==='idle'&&Date.parse(st.nextRun)<=t){st.state='syncing';st.lastStart=iso();st.disks.forEach(function(d){d.done=0;d.read=0;d.sent=0;});j._round={need:j._seedLeft?null:GB(rnd(0.3,2))};}
 if(st.state==='syncing'){var seed=!!j._seedLeft,allDone=true;st.disks.forEach(function(d,i){var used=j._used[i];var step=seed?Math.max(GB(3),used/30):d.size/6;d.done=Math.min(d.chunks,d.done+Math.ceil(step/4194304));d.read=Math.round(Math.min(seed?used:d.size,d.done*4194304));d.sent=seed?Math.round(d.read*0.55):Math.round((j._round&&j._round.need||GB(1))*d.done/d.chunks/st.disks.length);if(d.done<d.chunks){allDone=false;}});
  if(allDone){st.rounds++;st.lastEnd=iso();st.lastDurationSeconds=Math.round((t-Date.parse(st.lastStart))/1000);st.recoveryPoint=iso(Date.parse(st.lastStart));st.lastSent=st.disks.reduce(function(s,d){return s+d.sent;},0);st.totalSent+=st.lastSent;st.state='idle';st.nextRun=iso(t+45000);st.consistent=true;st.disks.forEach(function(d){d.passes++;});
   if(seed){st.initialCopyAt=iso();delete j._seedLeft;a.live.alerts.push({id:a.live.alerts.length+1,time:iso(),job:j.spec.id,jobName:j.spec.name,level:'info',kind:'initial',text:'The initial copy of '+j.spec.name+' is complete. It can be tested and cut over.',cleared:true});}
   st.message='Up to date as of '+hhmmss()+'. Next round at '+hhmmss(t+45000)+'.';}}
 });});});

/* ---------- Start VMs from copies (agent replicas on this host) ---------- */
var FAIL={failovers:[]};
function copies(){var list=[];REPL.agents.forEach(function(a){a.live.jobs.forEach(function(j){j.status.disks.forEach(function(d,i){var seeded=j.status.initialCopyAt&&j.status.initialCopyAt!==X.ZERO;list.push({name:d.target,size:d.size,complete:!!seeded&&j.status.state!=='syncing',verified:false,round:j.spec.id+'@'+j.status.recoveryPoint,job:j.spec.id,recoveryPoint:j.status.recoveryPoint,updated:iso(),busy:j.status.state==='syncing',generation:i===0?2:1,held:j._held||undefined,machine:i===0?{name:a.name,cpus:8,memoryMB:65536,firmware:'uefi',adapters:1,addresses:['192.0.2.40']}:undefined});});});});return list;}
on('/api/repl/failover',function(q){var d=q.data;
 if(q.method==='GET'){var o={ok:true,hyperV:true,folder:'D:\\Migration\\Received',receiving:true,failovers:FAIL.failovers,copies:copies()};if(q.query.get('switches')==='1'){o.switches=[{name:'SET-vSwitch0',type:'External'},{name:'Isolated test',type:'Private'}];}return J(200,o);}
 if(d.action==='start'){var r=d.request||{};var f={id:'f'+(FAIL.failovers.length+1),mode:r.mode||'test',name:r.name,vmId:uuid(),copies:r.copies||[],recoveryPoint:iso(),started:iso()};FAIL.failovers.push(f);REPL.agents.forEach(function(a){a.live.jobs.forEach(function(j){if(j.status.disks.some(function(x){return (r.copies||[]).indexOf(x.target)>=0;})){j._held='test failover '+r.name;if(f.mode==='test'){j.status.state='held';}}});});
  if(f.mode==='production'){X.recordCutover({name:String(r.name||'VM'),hyperVId:f.vmId,host:'HV01',method:'copies'});}return ok();}
 if(d.action==='end'){FAIL.failovers=FAIL.failovers.filter(function(x){return x.id!==d.id;});REPL.agents.forEach(function(a){a.live.jobs.forEach(function(j){if(j._held){delete j._held;if(j.status.state==='held'){j.status.state='idle';}}});});return ok();}
 return ok();});
var TT={running:false,log:[],error:'',tests:[]};
on('/api/repl/targettest',function(q){var t=V.target;if(q.method==='GET'){return J(200,Object.assign({ok:true,connected:!!t.connected,platform:t.platform||'',server:t.server||'',user:t.user||'',node:t.node||''},TT));}
 if(q.data.action==='start'){TT.running=true;TT.log=[hhmmss()+'  Pausing the job when its current round finishes, so the copy is one whole recovery point.'];setTimeout(function(){TT.log.push(hhmmss()+'  Cloned the receiver VM as '+(q.data.name||'TEST')+' and disconnected its adapters.');TT.log.push(hhmmss()+'  Started the clone from its disks. Replication resumed.');TT.running=false;TT.tests=[{id:'t1',name:q.data.name||'TEST',vmId:'201',started:iso()}];},4000);return ok();}
 if(q.data.action==='end'){TT.tests=[];TT.log.push(hhmmss()+'  The test ended: its clone was deleted. The replica was not touched.');return ok();}return ok();});

/* ---------- All jobs and servers ---------- */
var BULK={running:false,action:'',total:0,results:[]};
function lag(t){return t&&t!==X.ZERO?Math.round((now()-Date.parse(t))/1000):-1;}
function consolesFor(row,v){var c=[];var src=v&&X.findVM(v.id||row.vm);if(src&&row.state!=='cutover'){c.push({label:'Source VM on VMware',target:{platform:'vmware',id:src.id,name:src.name,host:src.host||''}});}
 if(v&&v.hyperVId){c.push({label:'New VM on Hyper-V',target:{platform:'hyperv',id:v.hyperVId,name:v.name,host:'HV01'}});}
 if(v&&v.test){c.push({label:'Test VM',target:{platform:'hyperv',id:v.test.vmId,name:v.test.name,host:'HV01'}});}return c;}
function planRow(p,pv,v){var row={key:'plan:'+p.id+'|'+v.id,kind:'plan',method:'Migration plan',name:v.name,detail:'Plan '+p.name,plan:p.id,vm:v.id,tested:!!(v.testedAt&&v.testedAt!==X.ZERO),percent:-1,lagSeconds:-1,wave:v.wave,cutoverAt:v.cutoverAt,nextSync:v.nextSync,lastSync:v.lastSync,limitMinutes:p.settings.syncMinutes};
 if(v.lastMode){row.lastRound={lastMode:v.lastMode,lastVia:v.lastVia,lastTracked:v.lastTracked,lastRead:v.lastRead,lastWritten:v.lastWritten,lastSeconds:v.lastSeconds,lastWhy:v.lastWhy};}
 if(v.seeded&&v.lastSync&&v.state!=='done'){row.lagSeconds=lag(v.lastSync);}
 if(v.live){row.percent=v.live.percent;row.bytesRead=v.live.bytesRead;row.bytesPerSecond=v.live.bytesPerSecond;row.secondsLeft=v.live.secondsLeft;}
 var behind=p.settings.syncMinutes>0&&row.lagSeconds>p.settings.syncMinutes*120+600;
 if(v.state==='done'){row.state='cutover';row.reason='Cut over: the Hyper-V VM was created from the final copy.';}
 else if(v.state==='cutting'){row.state='cuttingover';row.reason='The guest is shut down and the final copy is running.';}
 else if(v.state==='testing'||v.test){row.state='testing';row.reason='A test VM runs on differencing disks; syncs wait until the test ends.';}
 else if(p.paused){row.state='paused';row.reason='The plan '+p.name+' is paused. Resume it to carry on.';}
 else if(v.state==='failed'){row.state='failed';row.reason='The last round failed'+(v.error?': '+v.error:'')+'. It is retried on its own.';}
 else if(!v.seeded&&v.method==='offline'){row.state='atcutover';row.reason='Copied at its cutover, while shut down. Schedule or start the cutover when downtime suits.';}
 else if(!v.seeded){row.state='seeding';row.reason='The first full copy has not finished yet.';}
 else if(v.state==='copying'){row.state='syncing';row.reason='A sync is copying the changes since the last one. A whole copy exists, so it can be cut over.';}
 else if(behind){row.state='behind';row.overdue=true;row.reason='The last sync is older than twice the plan\'s interval.';}
 else{row.state='insync';row.reason='Seeded and kept in sync. Test it, then cut over.';}
 var acts=[];if(row.state!=='cutover'&&row.state!=='cuttingover'){if(row.state!=='testing'&&row.state!=='atcutover'){acts.push('sync');}acts.push(p.paused?'resume':'pause');if(!v.test&&v.state==='ready'){acts.push('test');}if(v.test){acts.push('endtest');}if(v.seeded||v.method==='offline'){acts.push('cutover');}acts.push('wave');}
 if(v.cutoverAt&&v.state!=='done'){acts.push('cancelcutover');}row.actions=acts;row.consoles=consolesFor(row,v);return row;}
function jobRow(a,j){var st=j.status;var row={key:'job:'+a.id+'|'+j.spec.id,kind:'job',method:'Agent replication',name:a.name,detail:'Job '+j.spec.name+' on '+a.address,agent:a.id,job:j.spec.id,host:a.address.split(':')[0],os:a.live.platform,percent:-1,lagSeconds:-1,limitMinutes:j.spec.rpoMinutes,wave:j._wave||0,nextSync:st.nextRun,lastSync:st.recoveryPoint===X.ZERO?undefined:st.recoveryPoint};
 var seeded=st.initialCopyAt&&st.initialCopyAt!==X.ZERO;if(st.recoveryPoint&&st.recoveryPoint!==X.ZERO){row.lagSeconds=j.rpoSeconds;}
 if(st.state==='syncing'||st.state==='cutting-over'){var c=0,dn=0,rd=0;st.disks.forEach(function(d){c+=d.chunks;dn+=d.done;rd+=d.read;});row.percent=c?Math.floor(dn*100/c):0;row.bytesRead=rd;}
 if(j.spec.cutoverAt){row.cutoverAt=j.spec.cutoverAt;}
 if(st.state==='cut-over'){row.state='cutover';row.reason='The final round finished. The new machine can take over; nothing more runs for this job.';}
 else if(st.state==='cutting-over'){row.state='cuttingover';row.reason='Services are stopped and the final round is running.';}
 else if(st.state==='held'){row.state='testing';row.reason='A test VM runs from the copies, so rounds are held. End the test to carry on replicating.';}
 else if(st.state==='paused'){row.state='paused';row.reason='Paused by an operator. Resume to carry on.';}
 else if(!seeded){row.state='seeding';row.reason='The first full copy is running. It cannot be tested or cut over until it finishes.';}
 else if(st.state==='syncing'){row.state='syncing';row.reason='A round is copying the changes since the last recovery point. A whole copy exists, so it can be tested or cut over.';}
 else{row.state='insync';row.reason='A whole copy exists and stays current. Test it, then cut over.';}
 var acts=[];if(row.state!=='cutover'&&row.state!=='cuttingover'){if(row.state!=='testing'){acts.push('sync');}acts.push(st.state==='paused'?'resume':'pause');if(seeded&&row.state!=='testing'){acts.push('test','cutover');}acts.push('wave');}row.actions=acts;row.consoles=[];return row;}
var stateText={notconfigured:'Not configured',agent:'Agent installed, no job',unreachable:'Agent not answering',seeding:'Initial copy',atcutover:'Copies at cutover',insync:'In sync, ready to cut over',syncing:'Syncing',behind:'Behind',paused:'Paused',testing:'Testing',cuttingover:'Cutting over',cutover:'Cut over',failed:'Failed',running:'Running',finished:'Finished'};
function rows(){var out=[],known={};
 REPL.agents.forEach(function(a){known[a.name.toLowerCase()]=1;known[a.name.split('.')[0].toLowerCase()]=1;if(!a.live.jobs.length){out.push({key:'agent:'+a.id,kind:'agent',method:'Agent replication',name:a.name,detail:a.address,agent:a.id,host:a.address.split(':')[0],os:a.live.platform,percent:-1,lagSeconds:-1,state:'agent',reason:'The agent answers. Create a replication job to start copying.',actions:['createjob'],wave:0});return;}a.live.jobs.forEach(function(j){out.push(jobRow(a,j));});});
 var inPlan={};PLANS.forEach(function(p){var pv=planView(p);pv.vms.forEach(function(v){inPlan[v.id]=1;known[v.name.toLowerCase()]=1;out.push(planRow(p,pv,v));});});
 V.transfers.filter(function(t){return t.kind==='move';}).forEach(function(t){var id=t.id.replace(/^move-/,'').split('|')[0];if(inPlan[id]){return;}var name=t.source.replace(/ \(.*$/,'');known[name.toLowerCase()]=1;inPlan[id]=1;
  out.push({key:'move:'+id,kind:'move',method:'Move VMs',name:name,detail:'To '+t.target,vm:id,percent:t.state==='running'?t.percent:-1,lagSeconds:-1,state:t.state==='running'?'seeding':'insync',stateText:t.state==='running'?'Copying':'Copied',reason:t.state==='running'?'Move VMs is copying this VM.':'The last Move VMs copy finished. Copy again for a delta, or cut over in Move VMs.',actions:[],wave:0});});
 if(VM.inventory){VM.inventory.vms.forEach(function(v){if(inPlan[v.id]||v.template||(v.readiness||[]).some(function(r){return r.status==='skip';})){return;}if(known[v.name.toLowerCase()]){return;}var host=v.guestHostName||v.ipAddress||'';var w=(VM.plan.vms.filter(function(p){return p.id===v.id;})[0]||{}).wave||0;
  out.push({key:'vm:'+v.id,kind:'vm',method:'Not configured',name:v.name,detail:VM.inventory.server,vm:v.id,host:host,os:v.guestOs,state:'notconfigured',reason:'Nothing moves this VM yet. Add it to a Migration plan (nothing installed in it), or install the agent to replicate it.',percent:-1,lagSeconds:-1,wave:w,actions:host&&v.powerState==='poweredOn'?['install']:[],consoles:[{label:'VM on VMware',target:{platform:'vmware',id:v.id,name:v.name,host:v.host}}]});});}
 var c=S.convert;if(c.running||c.endedAtUnixMs>now()-12*3600000&&c.status!=='idle'){out.push({key:'task:convert',kind:'task',method:'Convert disk images',name:String(c.source||'').split('\\').pop()||'Conversion',detail:'To '+(c.target||''),state:c.running?'running':c.status==='failed'?'failed':'finished',reason:c.message,percent:c.running&&c.progress.chunks?Math.floor(100*c.progress.done/c.progress.chunks):-1,lagSeconds:-1,wave:0,actions:[]});}
 var m=S.migrate;if(m.running||(m.endedAtUnixMs&&m.endedAtUnixMs>now()-12*3600000)){out.push({key:'task:shares',kind:'task',method:'Move file shares',name:m.source||'File shares',detail:m.plan.length+' share(s) to '+(m.targetName||X.COMPUTER),state:m.running?'running':m.status==='failed'?'failed':'finished',reason:m.message,percent:-1,lagSeconds:-1,wave:0,actions:[]});}
 var r=S.restore;if(r.status==='running'||(r.endedAtUnixMs&&r.endedAtUnixMs>now()-12*3600000)){out.push({key:'task:restore',kind:'task',method:'Restore',name:(r.scan&&r.scan.sourceComputer)||'Restore',detail:'To '+X.COMPUTER,state:r.status==='running'?'running':r.status==='failed'?'failed':'finished',reason:r.message,percent:-1,lagSeconds:-1,wave:0,actions:[]});}
 out.forEach(function(x){if(!x.stateText){x.stateText=stateText[x.state]||x.state;}if(!x.actions){x.actions=[];}});
 out.sort(function(a,b){if(a.wave!==b.wave){if(!a.wave||!b.wave){return b.wave?1:-1;}return a.wave-b.wave;}return a.name.toLowerCase()<b.name.toLowerCase()?-1:1;});
 var sum={total:out.length,states:{},scheduled:0,methods:{}};Object.keys(stateText).forEach(function(k){sum.states[k]=0;});out.forEach(function(x){sum.states[x.state]=(sum.states[x.state]||0)+1;sum.methods[x.kind]=(sum.methods[x.kind]||0)+1;if(x.cutoverAt&&x.state!=='cutover'){sum.scheduled++;}});return {rows:out,summary:sum};}
X.rows=rows;
on('/api/servers',function(q){var d=q.data;
 if(q.method==='GET'){var r=rows();return J(200,{ok:true,local:{hyperV:true,computer:X.COMPUTER,running:V.receiver.running,folder:V.receiver.folder||'',defaultFolder:'C:\\ClusterStorage\\Volume2\\Replicas',port:44380,addresses:['192.0.2.40']},rows:r.rows,summary:r.summary,bulk:BULK,hyperVHere:true,isAdministrator:true,inventory:VM.inventory?VM.inventory.server:'',console:true,now:iso()});}
 if(d.action==='stop'){BULK.stop=true;return ok();}
 var keys=d.keys||[];if(['cutover','test','endtest'].indexOf(d.action)>=0&&String(d.confirm)!==String(keys.length)){return bad('Confirm with the number of servers, '+keys.length+'.');}
 BULK={running:true,action:d.action,total:keys.length,results:[],started:iso()};var all=rows().rows;var i=0;
 var iv=setInterval(function(){if(i>=keys.length||BULK.stop){clearInterval(iv);BULK.running=false;BULK.ended=iso();delete BULK.stop;return;}var k=keys[i++],row=all.filter(function(r){return r.key===k;})[0]||{name:k,actions:[]};var res=bulkOne(d,row);BULK.results.push({key:k,name:row.name,ok:res.ok,text:res.text});},600);
 return J(202,{ok:true,count:keys.length});});
function bulkOne(d,row){var a=d.action;if(a!=='wave'&&a!=='createjob'&&row.actions.indexOf(a)<0){return {ok:false,text:'Skipped: '+(row.stateText||'')+' rows cannot '+a+'.'};}
 if(row.kind==='plan'){var p=planById(row.plan),v=p&&p.vms.filter(function(x){return x.id===row.vm;})[0];if(!v){return {ok:false,text:'Not found.'};}
  if(a==='sync'){v.syncNow=true;return {ok:true,text:'Sync asked.'};}if(a==='pause'||a==='resume'){p.paused=a==='pause';return {ok:true,text:a==='pause'?'Plan paused.':'Plan resumed.'};}
  if(a==='test'){v.state='testing';v.test={name:v.name+'-TEST',vmId:uuid(),folder:'',copies:[],children:[],started:iso()};return {ok:true,text:'Test VM '+v.name+'-TEST started, not connected.'};}
  if(a==='endtest'){delete v.test;v.state='ready';v.testedAt=iso();return {ok:true,text:'Test ended.'};}
  if(a==='cutover'){v.cutoverAt=d.at&&d.at!==X.ZERO?d.at:iso();return {ok:true,text:d.at&&d.at!==X.ZERO?'Cutover scheduled.':'Cutting over now.'};}
  if(a==='cancelcutover'){delete v.cutoverAt;return {ok:true,text:'Cutover canceled.'};}
  if(a==='wave'){v.wave=d.wave;var pv=VM.plan.vms.filter(function(x){return x.id===v.id;})[0];if(pv){pv.wave=d.wave;pv.manual=true;}return {ok:true,text:'Wave '+d.wave+'.'};}}
 if(row.kind==='job'){var ag=agentById(row.agent),j=ag&&ag.live.jobs.filter(function(x){return x.spec.id===row.job;})[0];if(!j){return {ok:false,text:'Not found.'};}
  if(a==='sync'){j.status.nextRun=iso();return {ok:true,text:'Round started.'};}if(a==='pause'){j.status.state='paused';return {ok:true,text:'Paused.'};}if(a==='resume'){j.status.state='idle';return {ok:true,text:'Resumed.'};}
  if(a==='cutover'){j.status.state='cutting-over';j._cut={t:0};return {ok:true,text:'Cutting over.'};}if(a==='wave'){j._wave=d.wave;return {ok:true,text:'Wave '+d.wave+'.'};}
  if(a==='test'){j.status.state='held';j._held='test';return {ok:true,text:'Test VM started from the copies.'};}if(a==='endtest'){j.status.state='idle';delete j._held;return {ok:true,text:'Test ended.'};}}
 if(row.kind==='agent'&&a==='createjob'){var ag2=agentById(row.agent);var nj=job('j-'+Math.random().toString(16).slice(2,10),ag2.name+' to HV01',[GB(80),GB(200)]);nj.status.state='syncing';nj.status.lastStart=iso();nj._seedLeft=true;ag2.live.jobs.push(nj);return {ok:true,text:'Job created; the first copy started.'};}
 if(row.kind==='vm'&&a==='wave'){var pv2=VM.plan.vms.filter(function(x){return x.id===row.vm;})[0];if(pv2){pv2.wave=d.wave;pv2.manual=true;}return {ok:true,text:'Wave '+d.wave+'.'};}
 return {ok:false,text:'Skipped.'};}

/* ---------- VM console ---------- */
var CN={};var cnSeq=1;
function screen(kind,o){var c=document.createElement('canvas');c.width=1024;c.height=768;var g=c.getContext('2d');
 var grad=g.createLinearGradient(0,0,1024,768);grad.addColorStop(0,'#0b3a6e');grad.addColorStop(1,'#06223f');g.fillStyle=grad;g.fillRect(0,0,1024,768);
 g.fillStyle='#fff';g.font='300 96px Segoe UI, sans-serif';var d=new Date();var hh=d.getHours(),mm=d.getMinutes();
 if(kind==='lock'){g.fillText((hh<10?'0':'')+hh+':'+(mm<10?'0':'')+mm,64,560);g.font='300 32px Segoe UI, sans-serif';g.fillText(d.toLocaleDateString('en-US',{weekday:'long',month:'long',day:'numeric'}),70,610);g.font='20px Segoe UI, sans-serif';g.fillText('Press Ctrl+Alt+Delete to sign in.',70,690);}
 else if(kind==='signin'){g.fillStyle='rgba(255,255,255,.12)';g.beginPath();g.arc(512,250,70,0,7);g.fill();g.fillStyle='#fff';g.font='36px Segoe UI, sans-serif';g.textAlign='center';g.fillText('EXAMPLE\\admin',512,370);g.fillStyle='#fff';g.fillRect(362,400,300,36);g.fillStyle='#222';g.font='22px Segoe UI, sans-serif';g.textAlign='left';g.fillText(o&&o.typed?'\u25CF'.repeat(Math.min(18,o.typed)):'Password',372,426);g.fillStyle='#ccc';g.textAlign='center';g.font='16px Segoe UI, sans-serif';g.fillText('Type with the keyboard, or use Paste as typing.',512,470);g.textAlign='left';}
 else{g.fillStyle='#1e1e1e';g.fillRect(0,0,1024,768);g.fillStyle='#f3f3f3';g.fillRect(80,60,860,560);g.fillStyle='#2b579a';g.fillRect(80,60,860,40);g.fillStyle='#fff';g.font='18px Segoe UI, sans-serif';g.fillText('Server Manager',96,86);g.fillStyle='#222';g.font='26px Segoe UI, sans-serif';g.fillText('Welcome to Server Manager',110,150);g.font='16px Segoe UI, sans-serif';
  (function(n){var v=X.findVMByName(n)||{};var ip=v.ipAddress||'192.0.2.50';var r=/^DC/i.test(n)?'Active Directory Domain Services, DNS Server':/^SQL/i.test(n)?'SQL Server 2019 (MSSQLSERVER)':/^FS|FS0/i.test(n)?'File and Storage Services':/^WEB/i.test(n)?'Web Server (IIS)':/^PRINT/i.test(n)?'Print and Document Services':'Application Server';return ['Local Server: '+n,'Roles: '+r,'Network: Ethernet  '+ip+' (static)','Activation: Windows is activated'];})(o&&o.name||'APP01').forEach(function(t,i){g.fillText(t,110,200+i*32);});g.fillStyle='#202020';g.fillRect(0,728,1024,40);g.fillStyle='#fff';g.font='14px Segoe UI, sans-serif';g.fillText(hhmmss().slice(0,5),960,752);}
 g.fillStyle='rgba(255,255,255,.55)';g.font='13px Segoe UI, sans-serif';g.textAlign='right';g.fillText('Demo console: '+(o&&o.name||'VM'),1012,20);g.textAlign='left';
 return c.toDataURL('image/png').split(',')[1];}
function push(s){var png=screen(s.screen,{name:s.view.target.name,typed:s.typed});s.listeners.forEach(function(f){f('tiles',[{x:0,y:0,png:png}]);});}
function pushState(s){s.listeners.forEach(function(f){f('state',s.view);});}
on(/^\/api\/console\/([a-z]+)$/,function(q){var a=q.match[1],d=q.data,s=CN[d.id];
 if(a==='open'){var tg=d.target||d;var plat=tg.platform||'vmware';var caps=plat==='hyperv'?{display:'live',keyboard:true,mouse:true,typeText:true,clipToGuest:true,clipFrom:false,clipFromWhy:'Hyper-V\'s console passes no clipboard out of the VM. Use Read text from the screen, or Text from a file in the VM.',filesTo:true,filesFrom:true,filesHow:'PowerShell Direct, with an account in the VM.',native:['vmconnect','rdp'],notes:['Works before the VM has a network: it uses Hyper-V\'s own keyboard, mouse and screen.','Nothing is installed in the VM.']}
   :{display:'live',keyboard:true,mouse:true,typeText:true,clipToGuest:true,clipFrom:true,clipFromWhy:'Text the VM copies arrives here when VMware Tools passes it on.',filesTo:true,filesFrom:true,filesHow:'Through VMware Tools\' file transfer, with an account in the VM.',native:['vmrc'],notes:['Uses the vSphere session from Discovery (a WebMKS ticket); no password is asked for.','Nothing is installed in the VM.']};
  var id='cn'+(cnSeq++);CN[id]={screen:'lock',typed:0,listeners:[],view:{id:id,target:tg,platformName:plat==='hyperv'?'Hyper-V':'VMware',where:plat==='hyperv'?(tg.host||'HV01')+' (Hyper-V console)':(tg.host||'esx01.example.com')+' (WebMKS)',caps:caps,status:'connected',message:'',typing:false,started:iso()}};
  return ok({session:CN[id].view});}
 if(!s){return ok();}
 if(a==='keys'){if(d.combo==='ctrlaltdel'&&s.screen==='lock'){s.screen='signin';s.typed=0;push(s);}return ok();}
 if(a==='input'){(d.events||[]).forEach(function(e){if(e.t!=='k'||!e.d){return;}if(s.screen==='lock'&&(e.s===0xffff||e.s===0xff9f)){return;}if(s.screen==='signin'){if(e.s===0xff0d){s.screen='desktop';push(s);}else if(e.s===0xff08){s.typed=Math.max(0,s.typed-1);push(s);}else if(e.s>=32&&e.s<127){s.typed++;push(s);}}});return ok();}
 if(a==='type'){if(d.stop){s.view.typing=false;pushState(s);return ok();}var txt=String(d.text||'');s.view.typing=true;pushState(s);var n=0;var iv=setInterval(function(){if(!s.view.typing||n>=txt.length){clearInterval(iv);s.view.typing=false;pushState(s);if(s.screen==='signin'&&/\n$/.test(txt)){s.screen='desktop';push(s);}return;}if(s.screen==='signin'&&txt[n]!=='\n'){s.typed++;push(s);}n++;},Math.max(20,d.delayMs||40));return ok();}
 if(a==='file'){s.view.file={direction:d.direction,status:'done',bytes:24576,message:'Copied 24 KB.'};pushState(s);return J(202,{ok:true});}
 if(a==='ocr'){return ok({text:s.screen==='desktop'?'Welcome to Server Manager\nLocal Server: '+s.view.target.name+'\nActivation: Windows is activated':s.screen==='signin'?'EXAMPLE\\admin\nPassword':'Press Ctrl+Alt+Delete to sign in.',language:'en-US'});}
 if(a==='text'){return ok(d.guest?{text:'Windows IP Configuration\n\nEthernet adapter Ethernet:\n   IPv4 Address. . . . . . . . . . . : 192.0.2.50\n   Subnet Mask . . . . . . . . . . . : 255.255.255.0\n   Default Gateway . . . . . . . . . : 192.0.2.1'}:{ok:false,error:'Type the path of the text file in the VM.'});}
 if(a==='native'){X.demoToast('In the real application this opens '+(d.kind==='vmrc'?'VMware Remote Console':d.kind==='rdp'?'an Enhanced session (.rdp)':'Hyper-V Virtual Machine Connection')+'.');return ok();}
 if(a==='close'){s.view.status='closed';s.view.message='The console was closed.';pushState(s);delete CN[d.id];return ok();}
 return ok();});
function FakeES(url){var es=this,u=new URL(url,location.href),id=u.searchParams.get('id');this.readyState=1;this._l={};this.url=url;
 setTimeout(function(){var s=CN[id];if(!s){return;}var f=function(name,data){var h=es._l[name]||[];h.forEach(function(fn){fn({data:JSON.stringify(data)});});};s.listeners.push(f);es._f=f;es._s=s;f('size',{w:1024,h:768});push(s);f('state',s.view);},60);}
FakeES.prototype.addEventListener=function(n,fn){(this._l[n]=this._l[n]||[]).push(fn);};
FakeES.prototype.close=function(){this.readyState=2;if(this._s){var i=this._s.listeners.indexOf(this._f);if(i>=0){this._s.listeners.splice(i,1);}}};
window.EventSource=FakeES;

seedRepl();seedPlans();
})();
