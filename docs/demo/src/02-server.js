/* Collection, restore, file shares and the disk converter. */
(function(){
'use strict';
var X=window.__DEMO_INTERNAL__,S=X.S,on=X.on,ok=X.ok,bad=X.bad,J=X.J,clone=X.clone,now=X.now,stamp=X.stamp,hhmmss=X.hhmmss,rnd=X.rnd,GB=X.GB,log=X.log;
var tickers=X.tickers=X.tickers||[];

/* ---------- collection of this server ---------- */
var stepNames={SystemInfo:'System identity and baseline',OperatingSystem:'Operating system',Hardware:'Hardware and firmware',Storage:'Disks and volumes',Network:'Network configuration',Roles:'Roles and features',Updates:'Installed updates',Services:'Services',ScheduledTasks:'Scheduled tasks',Applications:'Installed applications',Security:'Local accounts and security policy',Firewall:'Windows Firewall',Shares:'SMB shares and permissions',Printers:'Printers',Certificates:'Certificates',EventLogs:'Event logs',HyperV:'Hyper-V',Cluster:'Failover clustering',DHCP:'DHCP Server',DNS:'DNS Server',AD:'Active Directory',IIS:'IIS',NPS:'Network Policy Server',WSUS:'WSUS',GPO:'Group Policy'};
var col=null;
on('/api/start',function(q){
 if(S.running){return bad('A collection is already running.');}
 var o=q.data||{},inv=(o.inventoryCategories||[]).slice(),exp=(o.exportCategories||[]).slice();
 X.setPrefs({schema:1,options:Object.assign({},o,{includeSensitive:false})});
 var steps=['Preflight validation'].concat(inv.map(function(c){return 'Inventory: '+(stepNames[c]||c.replace(/([a-z])([A-Z])/g,'$1 $2'));})).concat(exp.map(function(c){return 'Export: '+(stepNames[c]||c.replace(/([a-z])([A-Z])/g,'$1 $2'));})).concat(['Writing Word report','Writing HTML report','Writing summary and manifest','Validating package']);
 var stampDir=(function(){var d=new Date(),p=function(n){return (n<10?'0':'')+n;};return d.getFullYear()+'-'+p(d.getMonth()+1)+'-'+p(d.getDate())+'_'+p(d.getHours())+p(d.getMinutes());})();
 var root=(o.outputRoot||S.defaultOutputRoot||'D:\\Reports').replace(/\\+$/,'');
 col={steps:steps,i:0,root:root,dir:root+'\\HV01_'+stampDir,cancel:false};
 Object.assign(S,{running:true,status:'running',phase:'Preparing collection',currentStep:'Preflight validation',progress:2,completedSteps:0,totalSteps:steps.length,warningCount:0,errorCount:0,startedAtUnixMs:now(),endedAtUnixMs:0,runId:X.uuid(),outputRoot:root,message:'Collecting the inventory of HV01. Nothing is changed on this server.',packagePath:col.dir});
 log('[INFO] Server Inventory and Migration Manager 3.3.0 collector started on HV01 as '+X.ACCOUNT+'.');
 log('[INFO] Output folder: '+col.dir);
 return ok();
});
on('/api/cancel',function(){if(col){col.cancel=true;}return J(202,{ok:true});});
tickers.push(function(){
 if(!col||!S.running){return;}
 if(col.cancel){S.running=false;S.status='canceled';S.phase='Collection canceled';S.message='The collection was canceled. Partial output and diagnostic files may remain.';S.currentStep='Review the execution log for details.';S.endedAtUnixMs=now();log('[WARN] Collection canceled by the operator.');col=null;return;}
 var step=col.steps[col.i];
 if(step){
  S.currentStep=step;S.phase=/^Inventory/.test(step)?'Collecting inventory':/^Export/.test(step)?'Exporting configuration':/^Writing|Validating/.test(step)?'Writing reports':'Preparing collection';
  log('[INFO] Started: '+step);
  if(/Printers/.test(step)){log('[WARN] Printers: the Print Spooler service is disabled on this server; the collector is marked unavailable.');S.warningCount++;}
  else if(/Event logs/.test(step)){log('[INFO] Read 214 critical, error and warning events from the last 30 days.');}
  else if(/Hyper-V/.test(step)){log('[INFO] Found 6 virtual machines and 1 virtual switch.');}
  else if(/Failover clustering/.test(step)){log('[INFO] Node of cluster HVCLU01 (3 nodes, 2 Cluster Shared Volumes).');}
  log('[INFO] Completed: '+step);
  col.i++;S.completedSteps=col.i;S.progress=Math.min(99,Math.round(2+97*col.i/col.steps.length));
  return;
 }
 S.running=false;S.status='success';S.phase='Inventory complete';S.message='The consolidated reports and supporting package passed validation.';
 S.currentStep='Open the engineering report or summary to review the server inventory and configuration information.';S.progress=100;S.endedAtUnixMs=now();
 S.htmlReportPath=col.dir+'\\HV01-Server-Inventory.html';S.docxReportPath=col.dir+'\\HV01-Server-Inventory.docx';S.summaryTextPath=col.dir+'\\HV01-Summary.txt';S.zipPath=col.dir+'.zip';S.launcherLogPath=col.dir+'\\Logs\\launcher.log';S.diagnosticPath=col.dir+'\\Logs';
 S.loadedReportAvailable=true;S.loadedReportPath=S.htmlReportPath;S.loadedReportPackage=col.dir;S.loadedReportDocxPath=S.docxReportPath;S.loadedReportSummaryPath=S.summaryTextPath;S.loadedReportName=col.dir.split('\\').pop();S.loadedReportSerial++;
 S.restore.packages.lastCollection=col.dir;S.restore.packages.loadedReport=col.dir;
 log('[OK] SUCCESS: package validated (SHA-256 manifest written for 186 files). Completed: collection of HV01.');
 col=null;
});

/* ---------- restore ---------- */
function restoreScan(path){return {packagePath:path,sourceComputer:'FS-OLD-07',currentComputer:X.COMPUTER,collectedAt:'2026-11-21 09:14',sameComputer:false,isAdministrator:true,manifestVerified:true,manifestMessage:'All 214 export files match the package manifest (SHA-256).',components:[
 {id:'DHCP',title:'DHCP Server',description:'Scopes, reservations, options and failover settings.',impact:'high',available:true,blockedReason:'',warnings:['Existing scopes with the same ID are replaced.'],items:[{id:'DHCP-config',title:'DHCP configuration (3 scopes, 41 reservations)',detail:'',available:true,blockedReason:'',defaultSelected:true}]},
 {id:'Shares',title:'File shares and share permissions',description:'Share definitions and their share-level permissions. Files are not part of a configuration package.',impact:'normal',available:true,blockedReason:'',warnings:[],items:[{id:'share-finance',title:'Finance',detail:'E:\\Data\\Finance',available:true,blockedReason:'',defaultSelected:true},{id:'share-projects',title:'Projects',detail:'E:\\Data\\Projects',available:true,blockedReason:'',defaultSelected:true},{id:'share-scans',title:'Scans',detail:'E:\\Data\\Scans',available:false,blockedReason:'E:\\Data\\Scans does not exist on this server.',defaultSelected:true}]},
 {id:'Printers',title:'Printers and drivers',description:'Print queues, ports and printer settings.',impact:'normal',available:true,blockedReason:'',warnings:[],items:[{id:'Printers-all',title:'4 print queues and their ports',detail:'',available:true,blockedReason:'',defaultSelected:true}]},
 {id:'NPS',title:'Network Policy Server',description:'RADIUS clients and network policies.',impact:'high',available:false,blockedReason:'The Network Policy Server role is not installed on this server.',warnings:[],items:[{id:'NPS-config',title:'NPS configuration',detail:'',available:false,blockedReason:'The Network Policy Server role is not installed on this server.',defaultSelected:true}]},
 {id:'GPO',title:'Group Policy objects',description:'Backed-up Group Policy objects.',impact:'domain',available:true,blockedReason:'',warnings:['Changes replicate to every domain controller.'],items:[{id:'GPO-all',title:'12 backed-up GPOs',detail:'',available:true,blockedReason:'',defaultSelected:false}]},
 {id:'Registry',title:'Registry keys',description:'Exported registry keys for selected services.',impact:'normal',available:true,blockedReason:'',warnings:[],items:[
  {id:'reg-lanman',title:'LanmanServer parameters',detail:'HKLM\\SYSTEM\\CurrentControlSet\\Services\\LanmanServer\\Parameters',available:true,blockedReason:'',defaultSelected:false},
  {id:'reg-spooler',title:'Spooler settings',detail:'HKLM\\SYSTEM\\CurrentControlSet\\Services\\Spooler',available:true,blockedReason:'',defaultSelected:false}]}]};}
var rs=null;
on(/^\/api\/restore\/(browse|scan|start|cancel)$/,function(q){
 var a=q.match[1],r=S.restore;
 if(a==='browse'){return ok({selected:true,path:'D:\\Restore\\FS-OLD-07_2026-11-21_0914'});}
 if(a==='scan'){r.status='scanning';r.message='Checking the package against its manifest.';r.packagePath=q.data.packagePath;r.scan=null;X.later(1800,function(){r.status='ready';r.message='The package is verified. Choose what to restore.';r.scan=restoreScan(q.data.packagePath);});return ok();}
 if(a==='start'){var sc=r.scan,items=[];(q.data.selections||q.data.components||[]).forEach(function(sel){var c=(sc.components||[]).filter(function(x){return x.id===sel.id;})[0];if(!c){return;}(sel.items||[]).forEach(function(id){var it=c.items.filter(function(x){return x.id===id;})[0];if(it){items.push({component:c.id,id:id,title:c.title+': '+it.title,status:'waiting',message:''});}});});
  if(!items.length){return bad('Choose at least one item to restore.');}
  Object.assign(r,{status:'running',message:'Restoring '+items.length+' item(s). Each item\'s current configuration is saved first.',items:items,logs:[],startedAtUnixMs:now(),endedAtUnixMs:0,cancelRequested:false,rollbackPath:'C:\\Users\\admin\\AppData\\Local\\Server Inventory Configuration Manager\\Restore-Rollback\\'+stamp().replace(/[: ]/g,'-')});
  rs={i:0,phase:0};r.logs.push({text:stamp()+' [INFO] Re-checked every file of the package against SHA256-Manifest.csv.'});return J(202,{ok:true});}
 if(a==='cancel'){r.cancelRequested=true;return J(202,{ok:true});}
});
tickers.push(function(){
 var r=S.restore;if(!rs||r.status!=='running'){return;}
 var it=r.items[rs.i];
 if(!it){r.status=r.cancelRequested?'canceled':'success';r.message=r.cancelRequested?'Stopped between items. Items already restored stay restored.':'Restore complete. Every item was saved to the rollback folder before it changed.';r.endedAtUnixMs=now();r.logs.push({text:stamp()+' [OK] Restore finished.'});rs=null;return;}
 if(r.cancelRequested&&it.status==='waiting'){r.items.slice(rs.i).forEach(function(x){x.status='skipped';x.message='Stopped before this item.';});rs.i=r.items.length;return;}
 if(rs.phase===0){it.status='running';it.message='Saving the current configuration.';r.logs.push({text:stamp()+' [INFO] '+it.title+': saved the current configuration to the rollback folder.'});rs.phase=1;return;}
 it.status='success';it.message='Restored and checked.';r.logs.push({text:stamp()+' [OK] '+it.title+': restored.'});rs.i++;rs.phase=0;
});

/* ---------- move a server live (Restore tab) ---------- */
S.serverMove={status:'idle',logs:[]};
on(/^\/api\/servermove\/(read|sites|forget)$/,function(q){
 var a=q.match[1],m=S.serverMove,d=q.data||{};
 if(a==='read'){
  if(!d.old){return bad('Type the old server\'s name.');}
  var target=d.newIsHere?X.COMPUTER:(d['new']||'');if(!target){return bad('Type the new server\'s name.');}
  var short=String(d.old).split('.')[0].toUpperCase(),pkg=(d.newIsHere?'C:\\Users\\admin\\AppData\\Local\\Server Inventory Configuration Manager\\Server-Move\\':'C:\\ProgramData\\Server Inventory and Migration Manager\\Server-Move\\')+short+'_TO_'+String(target).split('.')[0].toUpperCase()+'\\Package\\'+short+'_ServerInventory_20261128_101500';
  Object.assign(m,{status:'reading',step:'read',message:'Reading '+d.old+'.',old:d.old,'new':target,newIsHere:!!d.newIsHere,oldUser:d.oldUser||'',newUser:d.newUser||'',includeFiles:!!d.includeFiles,limitGB:d.limitGB||10,read:null,sites:[],stoppedSites:m.stoppedSites||[],logs:[{index:1,text:stamp()+' [INFO] Connecting to '+d.old+' through PowerShell remoting.'}]});
  X.later(1500,function(){m.status='copying';m.message='Copying the package to '+target+'.';m.logs.push({index:2,text:stamp()+' [INFO] Saving the IIS configuration.'},{index:3,text:stamp()+' [INFO] Copying website files from D:\\Sites\\shop'});});
  X.later(3200,function(){m.status='ready';m.step='check';m.message='Copied. Checking the package on '+target+'.';m.packagePath=pkg;m.readAt='2026-11-28 10:15:00';
   m.read={features:14,localUsers:2,localGroups:1,shares:1,iisSites:2,siteNames:['Default Web Site','Shop'],certificatesInBundle:1,certificatesLeftOut:0,iisPasswords:2,webContentFolders:2,warnings:[],sizeBytes:162529280};m.sites=m.read.siteNames.slice();
   m.logs.push({index:4,text:stamp()+' [INFO] The package is on '+target+'. Checking what can be restored there.'});
   var r=S.restore;r.live=true;r.target=target;r.status='scanning';r.packagePath=pkg;r.scan=null;
   X.later(1500,function(){var sc=restoreScan(pkg);sc.sourceComputer=short;sc.currentComputer=String(target).split('.')[0].toUpperCase();r.status='ready';r.message='Choose what to restore.';r.scan=sc;});});
  return J(202,{ok:true});
 }
 if(a==='sites'){if(d.action==='stop'){m.stoppedSites=(m.sites||[]).slice();m.message='Stopped '+m.stoppedSites.length+' site(s) on '+m.old+'.';}else{m.message='Started '+(m.stoppedSites||[]).length+' site(s) on '+m.old+' again.';m.stoppedSites=[];}return J(202,{ok:true});}
 if(a==='forget'){if((m.stoppedSites||[]).length){return J(409,{ok:false,error:'Sites this move stopped on '+m.old+' are still stopped. Start them again first.'});}S.serverMove={status:'idle',logs:[]};var r2=S.restore;if(r2.live){r2.live=false;r2.target='';r2.status='idle';r2.scan=null;r2.packagePath='';}return ok();}
});

/* ---------- move file shares ---------- */
function migrateIdle(){return {running:false,action:'',status:'idle',message:'',source:'',discovery:null,plan:[],space:[],prechecked:false,canCopy:false,canCutover:false,dfs:null,alias:null,sourceReadOnly:false,aliasAdded:'',logs:[],workPath:'',cancelRequested:false,startedAtUnixMs:0,endedAtUnixMs:0,account:'',accessDenied:false,target:'',targetName:'',targetAccount:'',autoSyncMinutes:0,nextAutoSyncUnixMs:0,autoSyncWaiting:'',throttle:{limitMbps:0},throttleNowMbps:0};}
S.migrate=migrateIdle();
function discovery(src){var up=src.split('.')[0].toUpperCase();return {source:up,sourceFqdn:up.toLowerCase()+'.example.com',currentComputer:X.COMPUTER,sameServer:false,isAdministrator:true,
 shares:[
  {name:'Finance',path:'E:\\Data\\Finance',description:'Finance team',system:false,remotePath:'\\\\'+up+'\\E$\\Data\\Finance',access:[{account:'EXAMPLE\\Finance',type:'Allow',right:'Change'},{account:'EXAMPLE\\Domain Admins',type:'Allow',right:'Full'}],warnings:[],bytes:GB(182),files:41210},
  {name:'Projects',path:'E:\\Data\\Projects',description:'Project files',system:false,remotePath:'\\\\'+up+'\\E$\\Data\\Projects',access:[{account:'EXAMPLE\\Staff',type:'Allow',right:'Change'}],warnings:[],bytes:GB(415),files:120332},
  {name:'Scans',path:'E:\\Data\\Scans',description:'Scanner drop folder',system:false,remotePath:'\\\\'+up+'\\E$\\Data\\Scans',access:[{account:up+'\\Scanner',type:'Allow',right:'Change'}],warnings:['Permissions name local accounts of the old server, which do not exist here: '+up+'\\Scanner.'],bytes:GB(12),files:3821},
  {name:'Users$',path:'F:\\Users',description:'Home folders',system:false,remotePath:'\\\\'+up+'\\F$\\Users',access:[{account:'EXAMPLE\\Domain Users',type:'Allow',right:'Change'}],warnings:[],bytes:GB(640),files:388120},
  {name:'NETLOGON',path:'C:\\Windows\\SYSVOL\\sysvol\\example.com\\SCRIPTS',description:'Logon server share',system:true,remotePath:'',access:[],warnings:['System share. It is normally re-created by its role.']}],
 localShares:['ADMIN$','C$','IPC$'],volumes:[{drive:'C:',freeBytes:GB(402),sizeBytes:GB(479)},{drive:'E:',freeBytes:GB(3100),sizeBytes:GB(4000)},{drive:'F:',freeBytes:GB(1500),sizeBytes:GB(2000)}],warnings:[]};}
var mg=null;
function mlog(t){S.migrate.logs.push({text:stamp()+' '+t});}
on(/^\/api\/migrate\/([a-z-]+)$/,function(q){
 var a=q.match[1],m=S.migrate,d=q.data||{};
 if(m.running&&['cancel','throttle','autosync'].indexOf(a)<0){return bad('A step is running. Wait for it or stop it.');}
 if(a==='discover'){var src=String(d.source||'FS-OLD-07').trim()||'FS-OLD-07';Object.assign(m,{running:true,action:'discover',status:'discovering',message:'Connecting to '+src+'.',source:src.split('.')[0].toUpperCase(),account:d.user||'',target:d.target||'',targetName:d.target?String(d.target).split('.')[0].toUpperCase():'',targetAccount:d.targetUser||'',startedAtUnixMs:now(),logs:[],workPath:'C:\\Users\\admin\\AppData\\Local\\Server Inventory Configuration Manager\\Migrations\\'+src.split('.')[0].toUpperCase()});
  mlog('[INFO] Reading shares, permissions and volumes of '+src+' (read only).');mg={kind:'discover',t:0};return J(202,{ok:true});}
 if(a==='plan'){m.plan=(d.shares||[]).map(function(s){return {name:s.name,destName:s.destName||s.name,destPath:s.destPath,status:'planned',message:'',percent:0,processed:0,expected:0,precheck:null,previouslySynced:false,lastSyncUnixMs:0,createdShare:false,allowOverwrite:!!s.allowOverwrite,verified:false};});
  if(!m.plan.length){return bad('Select at least one share to move.');}m.prechecked=false;m.canCopy=false;m.canCutover=false;m.status='planned';m.message='Plan saved. Run the check next.';return ok();}
 if(a==='precheck'){Object.assign(m,{running:true,action:'precheck',status:'checking',message:'Checking each share, the destination folders and free space.',startedAtUnixMs:now()});m.plan.forEach(function(s){s.status='checking';});mg={kind:'precheck',t:0};return J(202,{ok:true});}
 if(a==='sync'){if(!m.canCopy){return bad('Run the check, and fix any problem it finds, before copying.');}Object.assign(m,{running:true,action:'sync',status:'copying',message:'Copying. Users can keep working on the old server; run Copy again later to bring over what changed.',startedAtUnixMs:now()});m.plan.forEach(function(s){s.status='copying';s.percent=0;s.processed=0;s.expected=s._bytes;});mg={kind:'sync',t:0,i:0};mlog('[INFO] Robocopy /MIR /COPY:DATSO /DCOPY:DAT /B /R:1 /W:1 /MT:16 for each share.');return J(202,{ok:true});}
 if(a==='cutover'){Object.assign(m,{running:true,action:'cutover',status:'cuttingover',message:'Cutting over: last copy, then the shares are created here with their permissions.',startedAtUnixMs:now()});mg={kind:'cutover',t:0,i:0};return J(202,{ok:true});}
 if(a==='source-readonly'){Object.assign(m,{running:true,action:'readonly',status:'readonly-running',message:'Making the old shares read-only.'});mg={kind:'readonly',t:0};return J(202,{ok:true});}
 if(a==='source-restore'){Object.assign(m,{running:true,action:'restoresource',status:'restoring',message:'Putting the old permissions back.'});mg={kind:'restoresource',t:0};return J(202,{ok:true});}
 if(a==='dfs'){m.dfs={available:true,message:'1 DFS namespace folder points at the old server.',targets:[{namespacePath:'\\\\example.com\\Files\\Finance',target:'\\\\'+m.source+'\\Finance',suggested:'\\\\'+(m.targetName||X.COMPUTER)+'\\Finance'}]};return ok();}
 if(a==='alias-check'){m.alias={possible:false,reasons:[m.source+' still answers on the network. Add the old name only after the old server is turned off.'],oldName:m.source,oldFqdn:m.source.toLowerCase()+'.example.com',thisFqdn:'hv01.example.com',added:'',restartRequired:false};return ok();}
 if(a==='alias'){return bad(m.source+' still answers on the network.');}
 if(a==='cancel'){m.cancelRequested=true;return J(202,{ok:true});}
 if(a==='reset'){S.migrate=migrateIdle();mg=null;return ok();}
 if(a==='autosync'){m.autoSyncMinutes=d.minutes||0;m.nextAutoSyncUnixMs=d.minutes?now()+d.minutes*60000:0;return ok();}
 if(a==='throttle'){m.throttle=d.throttle||{limitMbps:d.limitMbps||0};return ok();}
 if(a==='browse'){return ok({selected:true,path:d.parent?'F:\\Shares':'F:\\Shares\\'+(d.share||'Share')});}
 return ok();
});
tickers.push(function(){
 var m=S.migrate;if(!mg||!m.running){return;}mg.t++;
 function finish(status,msg){m.running=false;m.status=status;m.message=msg;m.endedAtUnixMs=now();m.cancelRequested=false;mg=null;}
 if(m.cancelRequested){m.plan.forEach(function(s){if(/copying|checking|sharing/.test(s.status)){s.status='canceled';}});mlog('[WARN] Stopped by the operator.');finish('canceled','Stopped. Run it again to continue where it left off.');return;}
 if(mg.kind==='discover'){if(mg.t<3){return;}var disc=discovery(m.source);m.discovery=disc;disc.shares.forEach(function(s){s._b=s.bytes;});mlog('[INFO] Found '+disc.shares.length+' shares ('+(disc.shares.length-1)+' to move).');finish('discovered','Found '+disc.shares.length+' shares on '+m.source+'.');return;}
 if(mg.kind==='precheck'){if(mg.t<3){return;}var by={};(m.discovery.shares||[]).forEach(function(s){by[s.name]=s;});var need={};
  m.plan.forEach(function(s){var src=by[s.name]||{};s._bytes=src.bytes||GB(50);s.status='ready';s.message='Ready to copy.';s.precheck={ok:true,problems:[],warnings:(src.warnings||[]),files:src.files||1000,bytes:s._bytes};var dr=(s.destPath||'F:').slice(0,2).toUpperCase();need[dr]=(need[dr]||0)+s._bytes;});
  m.space=Object.keys(need).map(function(dr){var free=dr==='E:'?GB(3100):dr==='C:'?GB(402):GB(1500);return {drive:dr,neededBytes:need[dr],freeBytes:free,ok:free>need[dr]};});
  m.prechecked=true;m.canCopy=m.space.every(function(x){return x.ok;});mlog('[INFO] Check finished: '+m.plan.length+' share(s) ready.');finish('checked',m.canCopy?'Everything is ready to copy.':'Fix the problems shown before copying.');return;}
 if(mg.kind==='sync'||mg.kind==='cutover'){
  var s=m.plan[mg.i];
  if(!s){
   if(mg.kind==='sync'){m.canCutover=true;m.plan.forEach(function(x){x.previouslySynced=true;x.lastSyncUnixMs=now();});m.nextAutoSyncUnixMs=m.autoSyncMinutes?now()+m.autoSyncMinutes*60000:0;finish('copied','Copy finished. Run Copy again any time to bring over changes, then cut over.');}
   else{finish('cutover','Cutover complete. The shares are live on '+(m.targetName||X.COMPUTER)+' and match the old ones.');}
   return;}
  if(mg.kind==='cutover'){
   if(s.status!=='sharing'&&s.status!=='verifying'){s.status='copying';s.percent=100;s.message='Final copy of changes.';mlog('[INFO] '+s.name+': final copy, 12 changed files.');s.status='sharing';return;}
   if(s.status==='sharing'){s.createdShare=true;s.status='verifying';s.message='Share created with its permissions; comparing files.';mlog('[INFO] '+s.name+': created \\\\'+(m.targetName||X.COMPUTER)+'\\'+s.destName+' with the old share permissions.');return;}
   s.verified=true;s.status='done';s.message='Live here. Files and permissions match.';mlog('[OK] '+s.name+': verified.');mg.i++;return;}
  var step=Math.max(GB(18),s._bytes/6);s.processed=Math.min(s._bytes,(s.processed||0)+step);s.expected=s._bytes;s.percent=Math.floor(100*s.processed/s._bytes);s.message='Copying with Robocopy.';
  if(s.processed>=s._bytes){s.status='succeeded';s.message='Copied. Run Copy again to bring over changes.';mlog('[OK] '+s.name+': copied '+Math.round(s._bytes/1073741824)+' GB.');mg.i++;}
  return;}
 if(mg.kind==='readonly'){if(mg.t<2){return;}m.sourceReadOnly=true;m.plan.forEach(function(x){x.status='readonly';});finish('readonly','The old shares are read-only. Their previous permissions are saved and can be put back.');return;}
 if(mg.kind==='restoresource'){if(mg.t<2){return;}m.sourceReadOnly=false;m.plan.forEach(function(x){x.status='restored';});finish('restoredsource','The old shares\' permissions are back.');}
});

/* ---------- disk converter ---------- */
S.convert={running:false,status:'idle',message:'',source:'',target:'',format:'',info:null,progress:{pass:0,chunks:0,done:0,bytesRead:0,bytesWritten:0,changed:0,zero:0},result:null,cancelRequested:false,serial:0,startedAtUnixMs:0,endedAtUnixMs:0,
 formats:[{id:'vhdx',name:'VHDX (dynamic)',extension:'.vhdx',use:'Hyper-V, Scale HyperCore, Nutanix AHV',limit:70368744177664},{id:'qcow2',name:'qcow2',extension:'.qcow2',use:'Proxmox VE, Nutanix AHV, Scale HyperCore, KVM',limit:4611686018427387904},{id:'vmdk',name:'VMDK (monolithic sparse)',extension:'.vmdk',use:'VMware Workstation, vmkfstools -i on ESXi, Nutanix AHV',limit:2190433320960},{id:'vmdk-flat',name:'VMDK (ESXi flat)',extension:'.vmdk',use:'ESXi datastores (descriptor plus -flat.vmdk)',limit:4611686018427387904},{id:'vhd',name:'VHD (dynamic)',extension:'.vhd',use:'Older Hyper-V, Scale HyperCore, Azure (fixed)',limit:2190433320960},{id:'vhd-fixed',name:'VHD (fixed)',extension:'.vhd',use:'Azure, older Hyper-V',limit:2190433320960},{id:'raw',name:'Raw image',extension:'.img',use:'Any hypervisor, dd, physical disks',limit:4611686018427387904}],
 repairTargets:[{id:'hyperv',label:'Hyper-V',driver:'hyperv',virtio:false},{id:'scale',label:'Scale Computing HyperCore',driver:'kvm',virtio:true},{id:'nutanix',label:'Nutanix AHV',driver:'kvm',virtio:true},{id:'proxmox',label:'Proxmox VE (KVM)',driver:'kvm',virtio:true},{id:'vsphere',label:'VMware ESXi',driver:'vmware',virtio:false},{id:'physical',label:'Physical server',driver:'physical',virtio:false}],task:'convert',repairTarget:'',repair:null,log:[],
 send:{platforms:[{id:'vsphere',name:'VMware ESXi or vCenter',detail:'Imported as a new VM through vSphere (stream-optimized VMDK).'},{id:'hyperv',name:'Hyper-V on this computer',detail:'A VHDX in the folder you choose, attached to a new VM.'},{id:'proxmox',name:'Proxmox VE',detail:'Uploaded to a storage with the Import content type, then imported into a new VM.'},{id:'nutanix',name:'Nutanix AHV',detail:'Uploaded as a disk image in Prism Central, then cloned into a new VM.'},{id:'scale',name:'Scale HyperCore',detail:'Uploaded as a virtual disk (HyperCore 9.2 or later), then attached to a new VM.'}],platform:'',server:'',user:'',connected:false,inventory:null}};
var cv=null;
function cinfo(path){var phys=/^\\\\\.\\PhysicalDrive(\d+)$/.exec(path);
 if(phys){return {info:{path:path,format:'raw',formatName:'Disk '+phys[1]+' of this computer',variant:X.COMPUTER,virtualSize:GB(480),fileSize:GB(480),dataBytes:GB(77),notes:['C: (NTFS, 479 GB): read from a new shadow copy.']},boot:{partitionStyle:'GPT',firmware:'uefi',systemPartition:true,partitions:4,note:''}};}
 var fmt=/\.vhdx$/i.test(path)?['vhdx','VHDX','dynamic']:/\.vhd$/i.test(path)?['vhd','VHD','dynamic']:/\.qcow2$/i.test(path)?['qcow2','qcow2','v3']:/\.(img|raw)$/i.test(path)?['raw','Raw','']:['vmdk','VMDK','monolithicSparse'];
 return {info:{path:path,format:fmt[0],formatName:fmt[1],variant:fmt[2],virtualSize:GB(120),fileSize:GB(41),dataBytes:GB(38),chain:[path],notes:[]},boot:{partitionStyle:'GPT',firmware:'uefi',systemPartition:true,partitions:3,note:''}};}
on(/^\/api\/convert\/([a-z]+)$/,function(q){
 var a=q.match[1],c=S.convert,d=q.data||{};
 if(a==='browse'){if(d.kind==='drivers'){return ok({selected:true,path:'D:\\Drivers\\virtio-win-0.1.262'});}if(d.kind==='fixup'){return ok({selected:true,path:'E:\\Converted\\PRINT01.vhdx'});}return ok(d.kind==='folder'?{selected:true,path:'E:\\Converted'}:{selected:true,path:'D:\\Exports\\PRINT01\\PRINT01.vmdk'});}
 if(a==='localdisks'){return ok({computer:X.COMPUTER,disks:[{number:0,size:GB(480),system:true,volumes:[{name:'\\\\?\\Volume{1}\\',mounts:['C:\\'],fileSystem:'NTFS',length:GB(479)}]},{number:1,size:GB(8192),system:false,volumes:[{name:'\\\\?\\Volume{2}\\',mounts:['C:\\ClusterStorage\\Volume1\\'],fileSystem:'CSVFS',length:GB(8192)}]}]});}
 if(a==='inspect'){var ci=cinfo(d.path||'');var base=(d.path||'disk').split('\\').pop().replace(/\.[^.]+$/,'');var ext={vhdx:'.vhdx',qcow2:'.qcow2',raw:'.img',vhd:'.vhd','vhd-fixed':'.vhd',vmdk:'.vmdk','vmdk-flat':'.vmdk'}[d.format]||'.vhdx';
  return ok({info:ci.info,boot:ci.boot,suggested:'E:\\Converted\\'+(/PhysicalDrive/.test(d.path)?X.COMPUTER+'-PhysicalDrive0':base)+ext});}
 if(a==='start'){if(c.running){return bad('A conversion is running.');}var ci2=cinfo(d.source||'');var chunks=Math.ceil(ci2.info.virtualSize/4194304);
  Object.assign(c,{running:true,status:'running',task:'convert',source:d.source,target:d.target,format:d.format,message:'Converting '+String(d.source).split('\\').pop()+'.',startedAtUnixMs:now(),endedAtUnixMs:0,result:null,cancelRequested:false,info:ci2.info,progress:{pass:1,chunks:chunks,done:0,bytesRead:0,bytesWritten:0,changed:0,zero:0},serial:c.serial+1});
  cv={kind:'convert',data:ci2.info.dataBytes};return ok();}
 if(a==='cancel'){c.cancelRequested=true;return ok();}
 if(a==='connect'){c.send=Object.assign({},c.send,{platform:d.platform,server:d.server,user:d.user,node:d.node||'pve1',connected:true,certificate:null,version:{proxmox:'Proxmox VE 8.3',nutanix:'Prism Central pc.2024.2',scale:'HyperCore 9.4.27',vsphere:'VMware vCenter Server 8.0.2'}[d.platform]||'',inventory:{platform:d.platform,version:'',nodes:[{id:'pve1',name:'pve1'},{id:'pve2',name:'pve2'}],networks:[{id:'vmbr0',name:'vmbr0'},{id:'vmbr1',name:'vmbr1'}],storages:[{id:'local-lvm',name:'local-lvm',detail:'lvmthin, 1.2 TB free'},{id:'ceph-vm',name:'ceph-vm',detail:'rbd, 9.0 TB free'}]}});return ok({send:c.send});}
 if(a==='forget'){c.send=Object.assign({},c.send,{platform:'',server:'',user:'',connected:false,inventory:null});return ok();}
 if(a==='send'){if(c.running){return bad('A conversion is running.');}Object.assign(c,{running:true,status:'running',task:'send',message:'Sending '+String(d.source||'the disk').split('\\').pop()+' to '+(c.send.server||'the host')+' as '+((d.spec&&d.spec.name)||'a new VM')+'.',serial:c.serial+1,startedAtUnixMs:now(),endedAtUnixMs:0,result:null});c.send=Object.assign({},c.send,{stage:'Uploading',done:0,total:100});cv={kind:'send',name:(d.spec&&d.spec.name)||'the VM'};return ok();}
 if(a==='repair'){Object.assign(c,{running:true,status:'running',task:'repair',repairTarget:d.target,message:(d.mode==='check'?'Checking ':'Fixing up ')+String(d.path).split('\\').pop()+'.',serial:c.serial+1,log:['Opening '+d.path+(d.mode==='check'?' read only.':' for changes.')],repair:null});cv={kind:'repair',mode:d.mode||'apply',path:d.path,t:0};return ok();}
 return ok();
});
tickers.push(function(){
 var c=S.convert;if(!cv||!c.running){return;}
 function end(status,msg){c.running=false;c.status=status;c.message=msg;c.endedAtUnixMs=now();c.cancelRequested=false;c.serial++;cv=null;}
 if(c.cancelRequested){end('canceled','Stopped. Run it again to continue where it stopped.');return;}
 if(cv.kind==='convert'){var p=c.progress,per=Math.ceil(p.chunks/14);p.done=Math.min(p.chunks,p.done+per);p.bytesRead=Math.round(cv.data*p.done/p.chunks);p.bytesWritten=p.bytesRead;p.changed=p.done;p.zero=Math.round(p.done*0.62);
  if(p.done>=p.chunks){c.result={target:c.target,format:c.format,bytesRead:p.bytesRead,bytesWritten:p.bytesWritten,chunks:p.chunks,changed:p.changed,zero:p.zero,verified:true,seconds:Math.round((now()-c.startedAtUnixMs)/1000)};end('success','Converted and verified. Every 4 MB chunk matches its SHA-256.');}return;}
 if(cv.kind==='send'){var s=c.send;s.done=Math.min(100,(s.done||0)+9);s.stage=s.done<70?'Uploading':s.done<90?'Importing':'Creating the VM';if(s.done>=100){end('success',cv.name+' was created on '+(s.server||'the host')+'. It was not started.');}return;}
 if(cv.kind==='repair'){cv.t++;var lines=['Copied the SYSTEM hive and recorded every value it changes.','Set storport, vmbus, storvsc and netvsc to start at boot.','Added 0 driver packages with DISM.','Set the VMware Tools services to disabled (Start 4).','Detached the image and unloaded the hives.'];
  if(cv.mode==='check'){lines=['Read a copy of the SYSTEM hive.','Hyper-V storage and network drivers are present and will start at boot.','VMware Tools services are set to start automatically.'];}
  if(cv.t<=lines.length){c.log.push(lines[cv.t-1]);return;}
  c.repair={mode:cv.mode,ok:true,changes:cv.mode==='check'?0:6};end('success',cv.mode==='check'?'Check finished. Nothing was changed.':'Fixed up for '+(c.repairTarget||'the new platform')+'. Undo puts every value back.');}
});
})();
