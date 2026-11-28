/* Discovery (vCenter, ESXi, Hyper-V), plan and runbook, Move VMs, Prepare,
   receivers, guest fix-up, compare, SQL/DC checks and target platforms. */
(function(){
'use strict';
var X=window.__DEMO_INTERNAL__,D=X.D,S=X.S,on=X.on,ok=X.ok,bad=X.bad,J=X.J,clone=X.clone,now=X.now,iso=X.iso,stamp=X.stamp,hhmmss=X.hhmmss,rnd=X.rnd,GB=X.GB,uuid=X.uuid;
var tickers=X.tickers;
var VM={inventory:clone(D.inventory),plan:clone(D.plan),sizing:clone(D.sizing),rolling:null,guest:null,compare:null,attention:clone(D.attention)||[],remember:{enabled:false,entries:[]}};
X.VM=VM;
var suggested={};(D.plan.vms||[]).forEach(function(p){suggested[p.id]={wave:p.wave,hold:p.hold,exclude:p.exclude,why:p.why};});
// The plan starts unsuggested: everything in no wave until Suggest is pressed.
VM.plan.vms.forEach(function(p){if(!p.exclude){p.wave=0;p.hold=false;p.why='';}});VM.plan.waves=[];
var COPYROOT='C:\\ClusterStorage\\Volume1\\Hyper-V\\Copies',VMROOT='C:\\ClusterStorage\\Volume1\\Hyper-V';
var outFolder='D:\\Reports\\VMware_vcenter.example.com';
S.vmware={running:false,action:'',status:'connected',message:'Read 16 VM(s) on 4 host(s) from vcenter.example.com.',server:'vcenter.example.com',account:'EXAMPLE\\svc-migrate',certificate:null,summary:clone(D.summary),inventorySerial:1,
 outputFolder:outFolder,reportPath:outFolder+'\\VMware-Readiness-Report.html',runbookPath:'',runbookDocxPath:'',discoveryPath:outFolder+'\\VMware-Discovery-Report.html',reportDocxPath:outFolder+'\\Discovery-and-Readiness-Report.docx',reportPdfPath:outFolder+'\\Discovery-and-Readiness-Report.pdf',planCsvPath:'',
 guestSerial:0,guestMessage:'',guestError:false,guestWorkPath:'C:\\Users\\admin\\AppData\\Local\\Server Inventory Configuration Manager\\Guest-Migration',guestTarget:null,guestAuto:{user:'',hasPassword:false,rememberOn:false},fixups:[],
 preflight:{running:false,user:'',hasPassword:false,items:[],message:''},rollback:{running:'',serial:0,records:[]},compareSerial:0,compareError:'',prepare:null,prepareSerial:0,prepareError:'',prepareBatch:null,canPrepare:true,hyperVHere:true,
 move:null,moveSerial:0,moveError:'',moveStatus:null,moveCopyRoot:COPYROOT,hostStorage:{known:true,vhdPath:COPYROOT,vmPath:VMROOT,clustered:true,volumes:[{path:'C:\\ClusterStorage\\Volume1',freeBytes:GB(5017),totalBytes:GB(8192),csv:true},{path:'C:\\ClusterStorage\\Volume2',freeBytes:GB(6246),totalBytes:GB(8192),csv:true}]},
 hvRemote:{connected:false,serial:0},hvBuild:{plan:null,revision:'',steps:[],results:{},saved:[],serial:0,here:X.COMPUTER},transfers:[],target:{platform:'',server:'',user:'',node:'',connected:false,inventory:null,certificate:null,mediaPath:'',iso:'',isoName:'',receivers:{},created:{},message:'',failed:false,serial:0},
 send:null,sendSerial:0,sendError:'',receiver:{running:false,port:44380,sessions:0,firewall:false,defaultFolder:'D:\\Migration\\Received'},cancelRequested:false,planCopy:null,logs:[],startedAtUnixMs:0,endedAtUnixMs:0};
var V=S.vmware;
function vlog(t){V.logs.push({text:stamp()+' '+t});if(V.logs.length>400){V.logs.splice(0,V.logs.length-400);}}
X.vlog=vlog;
vlog('[INFO] Read 16 VMs, 4 hosts, 4 datastores and 4 networks from vcenter.example.com (vCenter Server 8.0.2).');

/* generic running step */
var act=null;
function begin(action,status,message,steps,finish){V.running=true;V.action=action;V.status=status;V.message=message;V.startedAtUnixMs=now();V.endedAtUnixMs=0;V.cancelRequested=false;act={steps:steps||[],i:0,finish:finish,t:0};vlog('[INFO] '+message);}
function end(status,message){V.running=false;V.action='';V.status=status;V.message=message;V.endedAtUnixMs=now();act=null;vlog((/failed/.test(status)?'[ERROR] ':'[OK] ')+message);}
X.vmBusy=function(){return V.running;};
tickers.push(function(){
 if(!act){return;}
 if(V.cancelRequested&&act.cancel!==false){var f=act.onCancel;act=null;V.running=false;V.action='';V.status='connected';V.message='Stopped.';V.cancelRequested=false;if(f){f();}vlog('[WARN] Stopped by the operator.');return;}
 act.t++;var s=act.steps[act.i];
 if(s&&act.t>=(s.at||0)){act.i++;if(s.log){vlog('[INFO] '+s.log);}if(s.msg){V.message=s.msg;}if(s.fn){s.fn();}return;}
 if(!s&&act.t>=1){var fin=act.finish;act=null;if(fin){fin();}}
});

function findVM(id){return VM.inventory.vms.filter(function(v){return v.id===id;})[0];}
function findVMByName(n){return VM.inventory.vms.filter(function(v){return v.name.toLowerCase()===String(n).toLowerCase();})[0];}
X.findVM=findVM;X.findVMByName=findVMByName;
function summarize(inv){var s={server:inv.server,serverType:inv.serverType,product:inv.product,collectedAt:inv.collectedAt.replace('T',' ').slice(0,16),hosts:inv.hosts.length,clusters:inv.clusters.length,datastores:inv.datastores.length,networks:inv.networks.length,vms:0,templates:0,poweredOn:0,vcpus:0,memoryBytes:0,provisionedBytes:0,usedBytes:0,snapshots:0,staticIpVms:0,readiness:[]};
 var by={};inv.vms.forEach(function(v){if(v.template){s.templates++;return;}s.vms++;if(v.powerState==='poweredOn'){s.poweredOn++;}s.vcpus+=v.cpus||0;s.memoryBytes+=(v.memoryMb||0)*1048576;s.provisionedBytes+=v.provisionedBytes||0;s.usedBytes+=v.usedBytes||0;s.snapshots+=(v.snapshots||[]).length;if((v.nics||[]).some(function(n){return n.static;})){s.staticIpVms++;}
  (v.readiness||[]).forEach(function(r){var b=by[r.target]=by[r.target]||{target:r.target,ready:0,review:0,blocked:0,skip:0};b[r.status]=(b[r.status]||0)+1;});});
 s.readiness=Object.keys(by).map(function(k){return by[k];});return s;}
X.summarize=summarize;
function invResponse(){var rolling=VM.plan.scenario==='rolling';return {ok:true,serial:V.inventorySerial,inventory:VM.inventory,plan:VM.plan,sizing:rolling?clone(D.rollingSizing):VM.sizing,rolling:rolling?clone(D.rollingReport):null,scenarios:clone(D.scenarios),attention:VM.attention};}

/* ---------- Discovery ---------- */
on('/api/vmware/inventory',function(){return J(200,VM.inventory?invResponse():{ok:true,inventory:null});});
on('/api/vmware/connect',function(q){
 if(V.running&&V.action!=='connect'){return bad('Wait for '+V.action+' to finish.');}
 var server=String(q.data.server||'vcenter.example.com').trim()||'vcenter.example.com',user=q.data.user||'EXAMPLE\\svc-migrate';
 if(!q.data.password&&!q.data.remembered){return bad('Type the password.');}
 V.server=server;V.account=user;V.certificate=null;
 if(VM.remember.enabled||q.data.remember){VM.remember.enabled=true;VM.remember.entries=VM.remember.entries.filter(function(e){return e.server!==server;}).concat([{server:server,user:user}]);}
 begin('connect','connecting','Connecting to '+server+' as '+user+'.',[
  {at:1,log:'Signed in to '+server+' (VMware vCenter Server 8.0.2). The certificate is trusted by Windows.',msg:'Reading hosts, clusters and licences.'},
  {at:2,log:'Read 4 hosts in 2 clusters, with their hardware, BIOS, network, storage and licences.',msg:'Reading datastores and networks.'},
  {at:1,log:'Read 4 datastores and 4 networks (2 distributed port groups).',msg:'Reading virtual machines.'},
  {at:2,log:'Read 16 virtual machines with their disks, adapters, snapshots and guest details from VMware Tools.',msg:'Checking readiness for every target.'},
  {at:1,log:'Wrote the readiness and discovery reports (HTML, Word, PDF) and the CSV files to '+outFolder+'.'}
 ],function(){var inv=VM.inventory;inv.server=server;inv.collectedAt=iso().slice(0,19)+'Z';V.summary=summarize(inv);V.inventorySerial++;V.outputFolder=outFolder;V.reportPath=outFolder+'\\VMware-Readiness-Report.html';V.discoveryPath=outFolder+'\\VMware-Discovery-Report.html';
  VM.plan.server=server;end('connected','Read '+V.summary.vms+' VM(s) on '+V.summary.hosts+' host(s) from '+server+'.');});
 return J(202,{ok:true});
});
function hypervInventory(host){
 var src=D.inventory,names=['WEB01','WEB02','MON01','PRINT01','BR-APP01'];
 var vms=src.vms.filter(function(v){return names.indexOf(v.name)>=0;}).map(function(v,i){var m=clone(v);m.id='hv-'+(100+i);m.source='hyperv';m.datacenter=host;m.cluster='HVCLU01';m.host=host;m.folder='';m.toolsStatus='';m.toolsVersion='';m.hardwareVersion='9.0';m.firmware='efi';m.vtpm=false;m.encrypted=false;m.changeTracking=true;
  m.disks=(m.disks||[]).map(function(d,j){return Object.assign(d,{controller:'SCSI',datastore:'CSV1',file:'C:\\ClusterStorage\\Volume1\\Hyper-V\\'+m.name+'\\'+m.name+(j?'_'+j:'')+'.vhdx',localPath:'\\\\'+host+'\\C$\\ClusterStorage\\Volume1\\Hyper-V\\'+m.name+'\\'+m.name+(j?'_'+j:'')+'.vhdx'});});
  m.nics=(m.nics||[]).map(function(n){return Object.assign(n,{adapterType:'Synthetic',network:'SET-vSwitch0',mac:'00:15:5d:0a:2'+i+':01'});});m.snapshots=[];
  var gen={severity:'info',code:'generation',message:'Generation 2 VM: created as Generation 2.',action:''};
  m.readiness=[{target:'hyperv',status:'ready',generation:2,findings:[gen,{severity:'info',code:'already-hyperv',message:'Already on Hyper-V: it moves to another host as it is, with its integration services.',action:''}]},
   {target:'scale',status:'review',findings:[{severity:'warning',code:'virtio-drivers',message:'Windows needs VirtIO drivers to boot from Scale HyperCore\'s disk controller.',action:'Install the Scale Computing guest tools before cutover.'}]},
   {target:'nutanix',status:'review',findings:[{severity:'warning',code:'virtio-drivers',message:'Windows needs VirtIO drivers to boot on Nutanix AHV.',action:'Install the Nutanix VirtIO drivers before cutover.'}]},
   {target:'proxmox',status:'review',findings:[{severity:'warning',code:'virtio-drivers',message:'Windows needs VirtIO drivers to boot on Proxmox VE.',action:'Install the virtio-win drivers before cutover.'}]}];
  if(/Linux|Red Hat|Ubuntu/.test(m.guestOs)){m.readiness.forEach(function(r){if(r.target!=='hyperv'){r.status='ready';r.findings=[{severity:'info',code:'linux-virtio',message:'Linux includes the VirtIO drivers.',action:''}];}});}
  return m;});
 return {schema:1,server:host,serverType:'Hyper-V host',product:'Microsoft Windows Server 2022 Datacenter (Hyper-V)',collectedAt:iso().slice(0,19)+'Z',datacenters:[host],
  clusters:[{name:'HVCLU01',datacenter:host,hosts:1,ha:true,drs:false,cpuCores:48,memoryBytes:GB(384)}],
  hosts:[{name:host,datacenter:host,cluster:'HVCLU01',vendor:'Example Systems',model:'R750',cpuModel:'Example Xeon Silver 4316',sockets:2,memoryBytes:GB(384),version:'10.0.20348',build:'20348',connectionState:'connected'}],
  licenses:[],datastores:[{name:'CSV1',datacenter:host,type:'CSV',capacityBytes:GB(8192),freeBytes:GB(5017),provisionedBytes:GB(2600),accessible:true,shared:true,vmCount:5,hosts:[host]}],
  networks:[{name:'SET-vSwitch0',datacenter:host,kind:'External',switch:'SET-vSwitch0',vlan:'',vmCount:5}],warnings:[],vms:vms};
}
on('/api/vmware/hyperv',function(q){
 if(V.running){return bad('Wait for '+V.action+' to finish.');}
 var host=String(q.data.host||'hv02.example.com').trim()||'hv02.example.com';
 begin('hyperv','connecting','Reading Hyper-V host '+host+'.',[
  {at:1,log:'Connected to '+host+' through CIM over WinRM (root\\virtualization\\v2). Nothing is changed on the host.'},
  {at:2,log:'Read 5 virtual machines, their virtual hard disks, switches and VLANs.'}],
  function(){VM.inventory=hypervInventory(host);V.server=host;V.account=q.data.user||X.ACCOUNT;V.summary=summarize(VM.inventory);V.inventorySerial++;
   VM.plan={schema:1,server:host,target:'hyperv',networks:[{source:'SET-vSwitch0',sourceVlan:'',targetName:'SET-vSwitch0',targetVlan:''}],storage:[{source:'CSV1',target:''}],capacity:clone(D.plan.capacity),vms:VM.inventory.vms.map(function(v){return {id:v.id,name:v.name,wave:0,hold:false,exclude:false,notes:''};}),waves:[]};
   end('connected','Read '+VM.inventory.vms.length+' VM(s) from Hyper-V host '+host+'.');});
 return J(202,{ok:true});
});
on('/api/vmware/open-inventory',function(){VM.inventory=clone(D.inventory);V.summary=summarize(VM.inventory);V.server=VM.inventory.server;V.inventorySerial++;V.status='connected';V.message='Opened the saved inventory of vcenter.example.com (read 2026-11-20 12:00).';return ok({selected:true});});
on('/api/vmware/reset',function(){if(V.running){return bad('Wait for the running step.');}VM.inventory=null;V.summary=null;V.server='';V.account='';V.status='idle';V.message='';V.inventorySerial++;return ok();});
on('/api/vmware/cancel',function(){V.cancelRequested=true;return J(202,{ok:true});});
on('/api/vmware/remember',function(q){var r=VM.remember;if(q.method==='POST'){if(typeof q.data.enabled==='boolean'){r.enabled=q.data.enabled;if(!r.enabled){r.entries=[];}}if(q.data.forget){r.entries=r.entries.filter(function(e){return e.server!==q.data.forget;});}}return ok({remember:r});});

/* ---------- Plan and runbook ---------- */
on('/api/vmware/plan',function(q){
 var d=q.data;if(!d.plan){return bad('The plan is missing.');}
 VM.plan=clone(d.plan);
 if(d.suggest){
  var waveOf={};VM.plan.vms.forEach(function(p){var sg=suggested[p.id];if(!sg){return;}if(p.manual&&!d.resetManual){return;}p.manual=false;p.wave=sg.wave;p.hold=sg.hold;p.why=sg.why;});
  VM.plan.waves=(VM.plan.waves||[]).filter(function(w){return w.edited&&!d.resetManual;});
  (D.plan.waves||[]).forEach(function(w){if(!VM.plan.waves.some(function(x){return x.wave===w.wave;})){VM.plan.waves.push(clone(w));}});
  VM.plan.waves.sort(function(a,b){return a.wave-b.wave;});VM.plan.waveSize=d.waveSize||0;
 }
 if(VM.plan.scenario==='rolling'){var rp=D.rollingPlan;VM.plan.rolling=VM.plan.rolling||{autoCapacity:true,stages:[],sourceCluster:''};
  if(!VM.plan.rolling.sourceCluster){VM.plan.rolling.sourceCluster='Production';}
  if(d.suggestStages||!VM.plan.rolling.stages.length&&d.suggestStages!==false){VM.plan.rolling.stages=clone(rp.rolling.stages);if(!d.keepWaves){VM.plan.vms.forEach(function(p){var rv=(rp.vms||[]).filter(function(x){return x.id===p.id;})[0];if(rv){p.wave=rv.wave;p.hold=rv.hold;p.why=rv.why;}});VM.plan.waves=clone(rp.waves||VM.plan.waves);}}
  if(VM.plan.rolling.autoCapacity){VM.plan.capacity=clone(rp.capacity);}}
 VM.plan.updatedAt=iso();
 var rolling=VM.plan.scenario==='rolling';
 return ok({plan:VM.plan,sizing:rolling?clone(D.rollingSizing):VM.sizing,rolling:rolling?clone(D.rollingReport):null,saved:true});
});
on('/api/vmware/export-plan',function(){V.runbookPath=outFolder+'\\Migration-Runbook.html';V.planCsvPath=outFolder+'\\Migration-Plan.csv';V.runbookDocxPath=outFolder+'\\Migration-Runbook.docx';vlog('[OK] Wrote the runbook (HTML and Word) and the plan CSV to '+outFolder+'.');return ok();});

/* ---------- Prepare source (snapshots, shutdown, vTPM, decrypt) ---------- */
function prepareSteps(vm,steps){var out=[];
 if(steps.snapshots&&(vm.snapshots||[]).length){out.push(['Removing '+vm.snapshots.length+' snapshot(s)','Deleted '+vm.snapshots.length+' snapshot(s) and consolidated the disks.',function(){vm.snapshots=[];vm.consolidationNeeded=false;}]);}
 if(steps.shutdown&&vm.powerState==='poweredOn'){out.push(['Shutting the guest down through VMware Tools','Shut down the guest.',function(){vm.powerState='poweredOff';}]);}
 if(steps.vtpm&&vm.vtpm){out.push(['Removing the virtual TPM','Removed the virtual TPM.',function(){vm.vtpm=false;}]);}
 if(steps.decrypt&&vm.encrypted){out.push(['Decrypting the VM and its disks','Decrypted the VM and its disks.',function(){vm.encrypted=false;(vm.disks||[]).forEach(function(d){d.encrypted=false;});}]);}
 if(!out.length){out.push(['Checking','Nothing needed changing.',function(){}]);}
 return out;}
function clearFindings(vm){(vm.readiness||[]).forEach(function(r){r.findings=(r.findings||[]).filter(function(f){return !/snapshot|encrypt|consolid/.test(f.code);});if(r.status==='blocked'&&!r.findings.some(function(f){return f.severity==='blocker';})){r.status=r.findings.some(function(f){return f.severity==='warning';})?'review':'ready';}});}
on('/api/vmware/prepare',function(q){
 if(V.running){return bad('Wait for '+V.action+' to finish.');}
 var d=q.data,items=d.items||[{vmId:d.vmId,steps:d.steps||{}}];
 var list=items.map(function(it){var vm=findVM(it.vmId);return {vm:vm,steps:prepareSteps(vm,it.steps||{})};}).filter(function(x){return x.vm;});
 if(!list.length){return bad('Choose a VM.');}
 V.prepareBatch=d.items?list.map(function(x){return {id:x.vm.id,vm:x.vm.name,state:'waiting',done:[],skipped:[],step:'',percent:0};}):null;
 var seq=[];list.forEach(function(x,i){x.steps.forEach(function(st,j){seq.push({at:j===0&&i===0?0:1,msg:x.vm.name+': '+st[0]+'.',log:x.vm.name+': '+st[0]+'.',fn:function(){var b=V.prepareBatch&&V.prepareBatch[i];if(b){b.state='running';b.step=st[0];b.startedAt=b.startedAt||iso();}}});
   seq.push({at:/Decrypt/.test(st[0])?4:2,fn:function(){st[2]();var b=V.prepareBatch&&V.prepareBatch[i];if(b){b.done.push(st[1]);b.percent=100;if(j===x.steps.length-1){b.state='done';b.step='';b.endedAt=iso();}}}});});
  seq.push({at:0,fn:function(){clearFindings(x.vm);}});});
 begin('prepare','preparing',list.length>1?'Preparing '+list.length+' VMs one after another.':'Preparing '+list[0].vm.name+'.',seq,function(){
  if(!d.items){V.prepare={vm:list[0].vm.name,done:list[0].steps.map(function(s){return s[1];}),skipped:[]};V.prepareSerial++;}
  V.summary=summarize(VM.inventory);V.inventorySerial++;end('connected',list.length>1?'Prepared '+list.length+' VMs.':'Prepared '+list[0].vm.name+'.');});
 return J(202,{ok:true});
});

/* ---------- Move VMs (one VM, this host, a remote host or a platform) ---------- */
X.transfer=function(id,label,source,target,size){var t={id:id,kind:'move',label:label,source:source,target:target,size:size,pass:1,chunks:Math.ceil(size/4194304),done:0,bytesRead:0,started:iso(),updated:iso(),state:'running',percent:0,bytesPerSecond:0,etaSeconds:0};V.transfers=V.transfers.filter(function(x){return x.id!==id;});V.transfers.push(t);return t;};
function moveRun(vm,action,where,doneFn){
 var cut=action==='cutover',disks=vm.disks||[],size=vm.usedBytes||GB(40),seeded=vm._seeded;
 var amount=seeded?GB(rnd(1,4)):size;var stages=cut?['connect','shutdown','copy','verify','create','cleanup']:['connect','snapshot','copy','cleanup'];
 V.moveStatus={vmId:vm.id,vm:vm.name,action:cut?'cutover':'copy',stages:stages,stage:'connect',detail:'',state:'running',started:iso(),updated:iso()};
 var tr=X.transfer('move-'+vm.id+'|'+(disks[0]?disks[0].label:'Hard disk 1'),vm.name+': '+(disks[0]?disks[0].label:'Hard disk 1'),vm.name+' ('+(disks[0]?disks[0].label:'Hard disk 1')+')',where,amount);
 var seq=[{at:0,log:'Connected to vcenter.example.com and '+(where.indexOf('\\\\')===0?where.split('\\')[2]:X.COMPUTER)+'.'}];
 if(cut){seq.push({at:1,msg:'Shutting '+vm.name+' down through VMware Tools.',log:'Asked '+vm.name+' to shut down through VMware Tools (never a hard power-off).',fn:function(){V.moveStatus.stage='shutdown';}});seq.push({at:2,log:vm.name+' is off.',fn:function(){vm.powerState='poweredOff';}});}
 else{seq.push({at:1,msg:'Taking this application\'s own snapshot of '+vm.name+'.',log:'Took snapshot SIMM-replica of '+vm.name+'.',fn:function(){V.moveStatus.stage='snapshot';}});}
 var n=6;for(var i=1;i<=n;i++){(function(i){seq.push({at:1,msg:'Copying '+vm.name+' ('+Math.round(100*i/n)+'%).',fn:function(){V.moveStatus.stage='copy';V.moveStatus.detail=(disks[0]?disks[0].label:'Hard disk 1')+' (1 of '+Math.max(1,disks.length)+')';tr.percent=Math.round(100*i/n);tr.done=Math.round(tr.chunks*i/n);tr.bytesRead=Math.round(amount*i/n);tr.bytesPerSecond=Math.round(amount/n);tr.etaSeconds=n-i;tr.updated=iso();}});})(i);}
 if(cut){seq.push({at:1,msg:'Verifying the copy.',log:'Verified every 4 MB chunk against its SHA-256.',fn:function(){V.moveStatus.stage='verify';}});seq.push({at:1,msg:'Creating the Hyper-V VM.',log:'Created '+vm.name+' (Generation 2) and started it.',fn:function(){V.moveStatus.stage='create';}});}
 seq.push({at:1,log:cut?'Disconnected the old VM\'s adapters in vSphere (they can be put back by Roll back).':'Removed snapshot SIMM-replica.',fn:function(){V.moveStatus.stage='cleanup';}});
 begin('move','moving',(cut?'Cutting over ':'Copying ')+vm.name+'.',seq,function(){
  tr.state='done';tr.percent=100;V.moveStatus.state='done';vm._seeded=true;
  var id=cut?uuid():'';V.move={vm:vm.name,vmId:vm.id,cutover:cut,shutDown:cut,round:{disks:disks.map(function(dk,i){return {label:dk.label,mode:seeded?'changed':'seed',via:vddkReady()?'vddk':'export',bytesWritten:i===0?amount:GB(0.2),verified:cut};})},disks:disks.map(function(dk,i){return where+'\\'+vm.name+'-disk'+i+'.vhdx';}),hyperV:cut?{generation:2}:null,hyperVId:id};V.moveSerial++;
  if(cut){X.afterCutover(vm,{hyperVId:id,host:where.indexOf('\\\\')===0?where.split('\\')[2]:X.COMPUTER,method:'move'});}
  if(doneFn){doneFn(id);}
  end('connected',cut?vm.name+' was created on Hyper-V and started.':'Copied '+vm.name+'. Copy again for the changes, then cut over.');});
}
function vddkReady(){var r=false;try{r=!!(window.__demoVddk&&window.__demoVddk());}catch(e){}return r;}
on('/api/vmware/move',function(q){if(V.running){return bad('Wait for '+V.action+' to finish.');}var vm=findVM(q.data.vmId);if(!vm){return bad('Choose a VM.');}moveRun(vm,q.data.action,(q.data.copyRoot||COPYROOT)+'\\'+vm.name);return J(202,{ok:true});});
on('/api/vmware/target/move',function(q){if(V.running){return bad('Wait for '+V.action+' to finish.');}var vm=findVM(q.data.vmId);if(!vm){return bad('Choose a VM.');}var t=V.target;moveRun(vm,q.data.action,'disk-0 on the receiver VM ('+(t.platform||'target')+')',function(){if(q.data.action==='cutover'){var c=t.created[vm.id];if(c){c.vm.state='running';c.cutOver=true;}t.message=vm.name+' boots from its own disks on '+(t.server||'the platform')+'.';t.serial++;}});return J(202,{ok:true});});

/* remote Hyper-V host or cluster */
on(/^\/api\/hvremote\/(connect|forget|move)$/,function(q){var a=q.match[1];
 if(a==='connect'){if(V.running){return bad('Wait for '+V.action+' to finish.');}var host=String(q.data.host||'hvclu01.example.com');
  begin('hvconnect','hvconnecting','Connecting to '+host+' with PowerShell remoting.',[{at:2,log:'Connected to '+host+' (failover cluster HVCLU01, 3 nodes, Windows Server 2025).'}],function(){
   V.hvRemote={connected:true,host:host,user:q.data.user||X.ACCOUNT,receiver:'',message:'Connected to '+host+'.',failed:false,serial:(V.hvRemote.serial||0)+1,info:{computer:'HV02',cluster:'HVCLU01',version:'10.0.26100',os:'Windows Server 2025 Datacenter',nodes:[{name:'HV01',state:'Up'},{name:'HV02',state:'Up'},{name:'HV03',state:'Up'}],switches:[{name:'SET-vSwitch0',type:'External'}],csvs:[{name:'Cluster Disk 1',path:'C:\\ClusterStorage\\Volume1',freeBytes:GB(5017),totalBytes:GB(8192)},{name:'Cluster Disk 2',path:'C:\\ClusterStorage\\Volume2',freeBytes:GB(6246),totalBytes:GB(8192)}],vhdPath:COPYROOT,vmPath:VMROOT,vms:['WEB01','WEB02','MON01']}};
   end('connected','Connected to '+host+'.');});return J(202,{ok:true});}
 if(a==='forget'){V.hvRemote={connected:false,serial:(V.hvRemote.serial||0)+1};return ok();}
 if(a==='move'){if(V.running){return bad('Wait for '+V.action+' to finish.');}var vm=findVM(q.data.vmId);if(!vm){return bad('Choose a VM.');}var node=q.data.node||'HV02';moveRun(vm,q.data.action,'\\\\'+node+'\\C$\\ClusterStorage\\Volume1\\Hyper-V\\Copies\\'+vm.name);return J(202,{ok:true,node:node});}
});

/* ---------- target platforms (HyperCore, Nutanix, Proxmox, physical) ---------- */
var platformNames={scale:'Scale Computing HyperCore',nutanix:'Nutanix AHV',proxmox:'Proxmox VE',physical:'Physical server'};
on(/^\/api\/vmware\/target\/(connect|forget|media|upload|create|receiver)$/,function(q){var a=q.match[1],t=V.target,d=q.data;
 if(a==='connect'){var p=d.platform||'proxmox';Object.assign(t,{platform:p,server:d.server,user:d.user,node:d.node||'',connected:true,certificate:null,failed:false,message:'Connected to '+d.server+'.',serial:t.serial+1,
   inventory:{platform:p,version:{proxmox:'Proxmox VE 8.3.0',nutanix:'Prism Central pc.2024.2',scale:'HyperCore 9.4.27'}[p]||'',nodes:p==='proxmox'?[{id:'pve1',name:'pve1',detail:'online'},{id:'pve2',name:'pve2',detail:'online'}]:p==='nutanix'?[{id:'c1',name:'NTNX-CLU01',detail:'AHV 10.0'}]:[{id:'n1',name:'hc-node1',detail:'online'},{id:'n2',name:'hc-node2',detail:'online'},{id:'n3',name:'hc-node3',detail:'online'}],
    storages:p==='proxmox'?[{id:'local-lvm',name:'local-lvm',detail:'lvmthin, images, 1.2 TB free'},{id:'ceph-vm',name:'ceph-vm',detail:'rbd, images, 9.0 TB free'}]:[{id:'default',name:'default',detail:'18 TB free'}],
    networks:p==='proxmox'?[{id:'vmbr0',name:'vmbr0',detail:'bridge'},{id:'vmbr1',name:'vmbr1',detail:'bridge, VLAN aware'}]:[{id:'servers',name:'Servers-120',detail:'VLAN 120'},{id:'dmz',name:'DMZ-200',detail:'VLAN 200'}]}});
  return ok({inventory:t.inventory});}
 if(a==='forget'){Object.assign(t,{platform:'',server:'',user:'',connected:false,inventory:null,serial:t.serial+1});return ok();}
 if(a==='media'){begin('media','media','Building receiver media with the Windows ADK on this computer.',[{at:2,log:'Mounted the Windows PE image and added the receiver.'},{at:2,log:'Added 2 driver folders.'}],function(){t.mediaPath='D:\\Reports\\Receiver-Media\\receiver-'+(t.platform||'media')+'.iso';t.message='Receiver media written.';t.serial++;end('connected','Receiver media written to '+t.mediaPath+'.');});return J(202,{ok:true});}
 if(a==='upload'){begin('upload','uploading','Uploading the receiver ISO to '+(t.server||'the platform')+'.',[{at:3,log:'Uploaded receiver-'+(t.platform||'media')+'.iso.'}],function(){t.iso=(t.platform==='proxmox'?'local:iso/':'')+'SIMM-Receiver.iso';t.isoName='SIMM-Receiver.iso';t.serial++;end('connected','Uploaded the receiver ISO.');});return J(202,{ok:true});}
 if(a==='create'){var vm=findVM(d.vmId)||{name:(d.spec&&d.spec.name)||'VM',id:d.vmId};begin('target-create','creating','Creating '+vm.name+' on '+(platformNames[t.platform]||'the platform')+'.',[{at:2,log:'Created '+vm.name+' with its disks blank, booting the receiver ISO.'},{at:1,log:'Started '+vm.name+'.'}],function(){t.created[vm.id]={platform:t.platform,vm:{id:String(100+Object.keys(t.created).length+1),name:vm.name,state:'running'}};t.message=vm.name+' was created on '+(platformNames[t.platform]||'the platform')+' and started from the receiver media. Its console shows the pairing code.';t.serial++;end('connected',t.message);});return J(202,{ok:true});}
 if(a==='receiver'){t.receivers[d.vmId]={link:{address:d.address,thumbprint:d.thumbprint},disks:[{name:'disk-0',label:'Disk 0, QEMU HARDDISK',size:GB(128)},{name:'disk-1',label:'Disk 1, QEMU HARDDISK',size:GB(500)}],mapping:['disk-0','disk-1']};t.serial++;return ok();}
});

/* ---------- receiver, send by hand, finder ---------- */
var THUMB='3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e';
on('/api/vmware/receiver',function(q){var d=q.data;
 if(/^service-/.test(d.action||'')){V.receiver.serviceInstalled=d.action==='service-on';return ok();}
 if(d.action==='start'){V.receiver={running:true,port:d.port||44380,code:'K7QMZ-2RT4X-9WBNP-H3VDA',thumbprint:THUMB,folder:d.folder||'D:\\Migration\\Received',addresses:['192.0.2.40','10.0.30.21'],sessions:0,firewall:!!d.firewall,fingerprint:'3F9A 6C0E 1B7D 4A25',discoverable:d.discoverable!==false,pairRequests:[],defaultFolder:'D:\\Migration\\Received',transfers:[]};vlog('[INFO] Receiving on port '+V.receiver.port+' into '+V.receiver.folder+'.');
  X.later(9000,function(){if(V.receiver.running){V.receiver.pairRequests=[{id:'0f'.repeat(16),computer:'APP02',address:'192.0.2.51',check:'482 913',started:iso()}];}});return ok();}
 V.receiver={running:false,port:44380,sessions:0,firewall:false,defaultFolder:'D:\\Migration\\Received',message:'Stopped.'};return ok();});
on('/api/vmware/receiver/pair',function(){V.receiver.pairRequests=[];return ok();});
on('/api/receivers/find',function(q){return ok({receivers:[{name:'HV01',port:44380,thumbprint:THUMB,folder:true,disks:0,freeBytes:GB(5017),version:'3.3.0',pairing:true,fingerprint:'3F9A 6C0E 1B7D 4A25',seen:['192.0.2.40:44380'],addresses:['192.0.2.40','10.0.30.21']},{name:'MINWINPC',port:44380,thumbprint:'ab'.repeat(32),folder:false,disks:2,diskBytes:GB(628),pairing:false,fingerprint:'ABAB ABAB ABAB ABAB',seen:['192.0.2.77:44380']}]});});
on('/api/receivers/reach',function(q){return ok({reachable:(q.data.addresses||[]).filter(function(a){return /^(192\.0\.2\.|10\.)/.test(a);})});});
var pairPolls=0;on('/api/receivers/pair',function(q){if(!q.data.id){pairPolls=0;return ok({id:'0f'.repeat(16),check:'482 913',state:'pending'});}pairPolls++;return ok(pairPolls>2?{state:'allowed',code:'K7QMZ-2RT4X-9WBNP-H3VDA'}:{state:'pending'});});
on('/api/vmware/agent/disks',function(){return ok({computer:X.COMPUTER,disks:[{number:0,size:GB(480),system:true,volumes:[{name:'v1',mounts:[],fileSystem:'FAT32',length:104857600},{name:'v2',mounts:['C:\\'],fileSystem:'NTFS',length:GB(479)}]}]});});
on('/api/vmware/agent/receiverdisks',function(){return ok({folder:false,disks:[{name:'disk-0',label:'Disk 0, QEMU HARDDISK',size:GB(500),sectorSize:512}]});});
on('/api/vmware/agent/send',function(){if(V.running){return bad('Wait for '+V.action+' to finish.');}var tr=X.transfer('send-0','Disk 0',X.COMPUTER+', disk 0','HV01-disk0.vhdx',GB(77));tr.kind='send';
 var seq=[{at:0,log:'Took a Volume Shadow Copy of C: (non-persistent, auto-release).'}];for(var i=1;i<=5;i++){(function(i){seq.push({at:1,msg:'Sending disk 0 ('+i*20+'%).',fn:function(){tr.percent=i*20;tr.bytesRead=Math.round(GB(77)*i/5);tr.done=Math.round(tr.chunks*i/5);tr.bytesPerSecond=GB(15);tr.etaSeconds=5-i;}});})(i);}
 begin('send','sending','Sending disk 0 of '+X.COMPUTER+'.',seq,function(){tr.state='done';V.send=[{disk:0,name:'HV01-disk0.vhdx',bytesRead:GB(77),bytesWritten:GB(77),resumed:false,verified:true,passes:1,shadowed:1,bytesSent:GB(31)}];V.sendSerial++;V.status='idle';end('connected','Sent 1 disk.');});return J(202,{ok:true});});

/* ---------- Prepare and fix up (guest engine) ---------- */
function guestResult(name){var adapter=function(n,mac,idx,addrs,st){return {Name:n,Description:'Microsoft Hyper-V Network Adapter',MacAddress:mac,InterfaceIndex:idx,Status:'Up',Dhcp:!st,Static:st,Addresses:addrs.map(function(a){return {Address:a,PrefixLength:24,Family:'IPv4',Origin:st?'Manual':'Dhcp'};}),Gateways:st?[{NextHop:'192.0.2.1',Metric:0,Family:'IPv4'}]:[],DnsServers:['192.0.2.21','192.0.2.22']};};
 return {Schema:1,Capture:{Schema:1,CapturedAt:iso(),Source:'vCenter',Findings:[{Severity:'info',Message:'Recorded static IP settings for 1 adapter.'},{Severity:'good',Message:'Hyper-V storage and network drivers are built into this Windows version.'}],State:{ComputerName:name,Adapters:[Object.assign(adapter('Ethernet0','00-50-56-9A-00-05',4,['192.0.2.50'],true),{Description:'vmxnet3 Ethernet Adapter'})],RunningAutoServices:['Spooler','VMTools','W3SVC']}},
  RollbackAvailable:false,MissingAutoServices:[],MissingAddresses:[],TargetChecks:[{Target:'hyperv',Severity:'good',Message:'Hyper-V drivers are built in.'},{Target:'scale',Severity:'warning',Message:'VirtIO drivers are not installed.'},{Target:'nutanix',Severity:'warning',Message:'VirtIO drivers are not installed.'},{Target:'proxmox',Severity:'warning',Message:'VirtIO drivers are not installed.'}],
  Activation:{Known:true,Channel:'KMS',Before:'KMS',Licensed:true,Status:'Licensed',Severity:'good',Message:'Windows activates against the KMS host kms.example.com. Nothing to do after the move.',CanActivate:true},
  Ghosts:[],State:{ComputerName:name,Domain:'example.com',OperatingSystem:'Microsoft Windows Server 2022 Standard',Firmware:'UEFI',SecureBoot:true,Platform:{Name:'VMware'},VmwareTools:{Installed:true,Version:'12.4.0',ProductCode:'{A0B1C2D3-0000-4000-8000-000000000000}'},Adapters:[Object.assign(adapter('Ethernet0','00-50-56-9A-00-05',4,['192.0.2.50'],true),{Description:'vmxnet3 Ethernet Adapter'})],Disks:[{Number:0,FriendlyName:'VMware Virtual disk',SizeBytes:GB(120),IsOffline:false,IsReadOnly:false,IsBoot:true,IsSystem:true}]}};}
on('/api/vmware/guest',function(){return ok({serial:V.guestSerial,result:VM.guest});});
on('/api/vmware/guest-load',function(){VM.guest=guestResult('APP01');V.guestSerial++;V.guestMessage='Opened the recording of APP01.';return ok({selected:true});});
on('/api/vmware/guest-target',function(q){var d=q.data;V.guestTarget={mode:d.mode||'local',modeName:{direct:'A VM on this Hyper-V host (PowerShell Direct)',hostdirect:'A VM on the connected Hyper-V host',winrm:'Another Windows computer (PowerShell remoting)',local:'This computer'}[d.mode||'local'],label:d.vm||d.computer||X.COMPUTER,vm:d.vm||'',computer:d.computer||'',host:d.host||'',user:d.user||'',hasPassword:!!d.password,kept:'',keptSource:'',keptAt:''};return ok();});
on('/api/vmware/guest-target/forget',function(){V.guestTarget=null;return ok();});
on('/api/vmware/guest-target/vcenter',function(q){var vm=findVM(q.data.vmId)||findVMByName(q.data.vm||'APP01');VM.guest=guestResult(vm?vm.name:'APP01');V.guestSerial++;V.guestMessage='Recorded '+(vm?vm.name:'the VM')+'\'s IP settings from what VMware Tools reports in vCenter.';return ok();});
on(/^\/api\/vmware\/guest\/([a-z-]+)$/,function(q){var mode=q.match[1];var who=(V.guestTarget&&V.guestTarget.label)||X.COMPUTER;
 var texts={capture:'Recorded '+who+'\'s settings.',status:'Read '+who+'\'s status on the new platform.',apply:'Put the recorded IP settings back on '+who+'. Undo is available.',undo:'Put '+who+'\'s adapters back as they were.',ghosts:'Removed 1 hidden VMware adapter.',activate:'Windows on '+who+' is activated.',tools:'Removed VMware Tools from '+who+'. Windows restarts in 60 seconds.',bitlocker:'Saved the BitLocker recovery keys to the output folder and suspended protection.',disks:'Brought 1 offline data disk online.',autofixup:'Fixed up '+who+'.'};
 begin('guest-'+mode,'guest','Running '+mode+' on '+who+'.',[{at:2}],function(){VM.guest=guestResult(who);if(mode!=='capture'){VM.guest.State.Platform={Name:'Hyper-V'};VM.guest.State.Adapters=[{Name:'Ethernet',Description:'Microsoft Hyper-V Network Adapter',MacAddress:'00-15-5D-0A-31-05',InterfaceIndex:7,Status:'Up',Dhcp:false,Static:true,Addresses:[{Address:'192.0.2.50',PrefixLength:24,Family:'IPv4',Origin:'Manual'}],Gateways:[{NextHop:'192.0.2.1',Metric:0,Family:'IPv4'}],DnsServers:['192.0.2.21','192.0.2.22']}];}
  V.guestSerial++;V.guestMessage=texts[mode]||'Done.';V.guestError=false;end('connected',V.guestMessage);});return J(202,{ok:true});});
on('/api/vmware/guest-auto',function(q){var rem=!!(q.data.remember||V.guestAuto.rememberOn);V.guestAuto={user:q.data.user||'.\\Administrator',hasPassword:true,remembered:rem,rememberOn:rem};return ok({user:V.guestAuto.user});});
on('/api/vmware/guest-auto/forget',function(){V.guestAuto={user:'',hasPassword:false,rememberOn:V.guestAuto.rememberOn};return ok();});
on('/api/vmware/guest-auto/run',function(q){var f=V.fixups.filter(function(x){return x.vm===q.data.vm;})[0];if(f){X.runFixup(f);}return J(202,{ok:true});});

/* ---------- compare before and after ---------- */
on('/api/vmware/compare/browse',function(q){return ok({selected:true,path:'D:\\Reports\\APP01_'+(q.data.side==='before'?'2026-11-19_2200':'2026-11-21_0730'),computer:'APP01',collectedAt:q.data.side==='before'?'2026-11-19 22:00':'2026-11-21 07:30'});});
on('/api/vmware/compare',function(q){if(q.method==='GET'){return J(200,Object.assign({ok:true,serial:V.compareSerial},VM.compare||{result:null}));}
 begin('compare','comparing','Comparing the two inventories.',[{at:2}],function(){var ch=function(ds,label,kind,item,expected,fields){return {dataset:ds,label:label,kind:kind,item:item,fields:fields||[],expected:expected,important:!expected&&ds==='services'};};
  var att=[ch('services','Services','changed','W3SVC (World Wide Web Publishing Service)',false,[{name:'StartType',before:'Automatic',after:'Manual'}])];
  VM.compare={result:{before:{path:q.data.before,computer:'APP01',collectedAt:'2026-11-19 22:00'},after:{path:q.data.after,computer:'APP01',collectedAt:'2026-11-21 07:30'},sameComputer:true,expectedCount:5,changeCount:6,attentionCount:1,reportPath:'D:\\Reports\\Comparison_APP01\\Before-After-Comparison.html',attention:att},
   datasets:[{key:'services',label:'Services',available:true,note:'',beforeCount:182,afterCount:178,added:0,removed:4,changed:1,expected:4,changes:[att[0],ch('services','Services','removed','VMTools (VMware Tools)',true),ch('services','Services','removed','VGAuthService (VMware Alias Manager and Ticket Service)',true)],more:2},
    {key:'adapters',label:'Network adapters',available:true,note:'',beforeCount:1,afterCount:1,added:1,removed:1,changed:0,expected:2,changes:[ch('adapters','Network adapters','removed','vmxnet3 Ethernet Adapter',true),ch('adapters','Network adapters','added','Microsoft Hyper-V Network Adapter',true)],more:0},
    {key:'addresses',label:'IP addresses',available:true,note:'',beforeCount:1,afterCount:1,added:0,removed:0,changed:0,expected:0,changes:[],more:0},
    {key:'shares',label:'Shares',available:true,note:'',beforeCount:4,afterCount:4,added:0,removed:0,changed:0,expected:0,changes:[],more:0}]};
  V.compareSerial++;end('connected','Compared: 6 changes, 5 expected after a move, 1 to review.');});return J(202,{ok:true});});

/* ---------- SQL Server and domain controller checks ---------- */
var pf=null;
on('/api/vmware/preflight/run',function(q){var p=V.preflight;var items=(q.data.items||[]).map(function(i){return {id:i.id,name:(findVM(i.id)||{}).name||i.id,state:'waiting',summary:''};});
 Object.assign(p,{running:true,user:q.data.user||p.user||'',hasPassword:!!q.data.user||!!p.hasPassword,items:items,message:'Checking '+items.length+' server(s) one after another (read only).'});pf={i:0,t:0};return J(202,{ok:true});});
on('/api/vmware/preflight/stop',function(){if(pf){pf.stop=true;}return ok();});
on('/api/vmware/preflight/forget',function(){V.preflight.user='';V.preflight.hasPassword=false;return ok();});
tickers.push(function(){var p=V.preflight;if(!pf||!p.running){return;}pf.t++;var it=p.items[pf.i];
 if(!it||pf.stop){p.items.forEach(function(x){if(x.state==='waiting'){x.state='skipped';}});p.running=false;p.message=pf.stop?'Stopped.':'Checked '+p.items.length+' server(s).';pf=null;V.inventorySerial++;return;}
 if(it.state==='waiting'){it.state='running';it.summary='Reading over WinRM.';return;}
 var vm=findVM(it.id)||{name:it.name};var dc=/^DC/i.test(vm.name),sql=/SQL/i.test(vm.name);
 it.state='done';
 if(dc){it.summary='Domain controller: replication healthy, SYSVOL on DFS-R, time from the PDC emulator. 1 review item.';vm.preflight={checkedAt:iso(),dc:{isDC:true,fsmo:vm.name==='DC01'?['PDC','RID','Infrastructure','Schema','Domain naming']:[],sysvol:'DFSR',replication:'healthy',timeSource:vm.name==='DC01'?'time.example.com':'DC01.example.com'},sql:null,errorSummary:''};addFinding(vm,{severity:'warning',code:'dc-fsmo',message:vm.name==='DC01'?'Holds all five FSMO roles: move it in its own wave and check replication right after.':'Domain controller: move it on its own, after DC01 is proven.',action:''});}
 else if(sql){it.summary='SQL Server 2019 (MSSQLSERVER): 6 databases, last full backup 9 hours ago, SQL VSS Writer running.';vm.preflight={checkedAt:iso(),sql:{instances:[{name:'MSSQLSERVER',version:'15.0.4385.2',databases:6,lastFullBackup:'9 hours ago',tde:false,ag:''}],vssWriter:'Running'},dc:null,errorSummary:''};addFinding(vm,{severity:'info',code:'sql-backup',message:'SQL Server 2019: last full backup 9 hours ago. Take a fresh backup before cutover.',action:''});}
 else{it.summary='Not a domain controller or SQL Server.';}
 pf.i++;});
function addFinding(vm,f){var r=(vm.readiness||[]).filter(function(x){return x.target==='hyperv';})[0];if(!r){return;}r.findings=(r.findings||[]).filter(function(x){return x.code!==f.code;}).concat([f]);if(f.severity==='warning'&&r.status==='ready'){r.status='review';}}

/* ---------- Build Hyper-V hosts ---------- */
var hbAct=null;
on(/^\/api\/hvbuild\/([a-z]+)$/,function(q){var a=q.match[1],hb=V.hvBuild,d=q.data;
 if(a==='suggest'){return ok({plan:clone(D.hvBuildPlan)});}
 if(a==='save'){hb.plan=d.plan;hb.revision=D.hvBuildRevision;hb.steps=clone(D.hvBuildSteps);if(hb.saved.indexOf(d.plan.name)<0){hb.saved.push(d.plan.name);}hb.serial++;return ok({plan:d.plan});}
 if(a==='open'){hb.plan=clone(D.hvBuildPlan);hb.revision=D.hvBuildRevision;hb.steps=clone(D.hvBuildSteps);hb.serial++;return ok({plan:hb.plan});}
 if(a==='account'){hb.user=d.forget?'':d.user;hb.serial++;return ok();}
 if(a==='networkmap'){return ok({changed:3});}
 if(a==='read'||a==='preview'||a==='apply'){if(V.running){return bad('Wait for '+V.action+' to finish.');}
  if(a==='apply'&&!hb.previewedAt){return bad('Preview the plan first.');}
  var steps=(hb.steps||[]).filter(function(s){return !d.steps||d.steps.indexOf(s.id)>=0;});var nodes=(hb.plan&&hb.plan.nodes||[]).map(function(n){return n.name;});
  var seq=[];steps.forEach(function(s){seq.push({at:1,msg:(a==='apply'?'Applying: ':a==='preview'?'Previewing: ':'Reading: ')+s.title+'.',log:s.title+(s.where==='each'?' on '+nodes.join(', '):s.where==='here'?' on this computer':'')+'.',fn:function(){
    var per={};(s.where==='each'||s.where==='restart'?nodes:[s.where==='here'?X.COMPUTER:'cluster']).forEach(function(n){per[n]=a==='read'?{state:'ok',notes:['Read.'],changes:[]}:a==='preview'?{state:'ok',changes:demoChanges(s,n),notes:[],blocks:[]}:{state:'ok',changes:demoChanges(s,n),applied:true,notes:['Done.']};});
    hb.results[s.id]={mode:a,at:iso(),computers:per};hb.serial++;}});});
  begin('hvbuild','hvbuilding',(a==='apply'?'Applying':a==='preview'?'Previewing':'Reading')+' the build plan '+(hb.plan&&hb.plan.name||'')+'.',seq,function(){if(a==='preview'){hb.previewedAt=iso();}hb.serial++;end('connected',a==='apply'?'Applied every step. Nothing was removed or replaced.':a==='preview'?'Preview finished: '+steps.length+' steps, nothing changed.':'Read the nodes.');});return J(202,{ok:true});}
 return J(404,{});
});
function demoChanges(s,n){var m={roles:['Install Hyper-V, Failover Clustering and Multipath I/O on '+n],networking:['Create SET switch SET-vSwitch0 on '+n,'Add host adapter LiveMigration with its address'],storage:['Add iSCSI portals 10.0.40.10 and 10.0.41.10','Connect 3 iSCSI targets with MPIO'],cluster:['Create cluster HVCLU02 with 3 nodes'],csv:['Add 2 disks as Cluster Shared Volumes'],quorum:['Set a cloud witness'],restart:['Restart '+n+' (waits for it to come back)']};return m[s.id]||[s.title+' on '+n];}

X.platformNames=platformNames;
})();
