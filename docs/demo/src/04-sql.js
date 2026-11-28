/* Move SQL databases: two fictional SQL Servers, a running plan whose log
   shipping advances every few seconds, tests, cutover and rollback. */
(function(){
'use strict';
var X=window.__DEMO_INTERNAL__,on=X.on,ok=X.ok,bad=X.bad,clone=X.clone,now=X.now,iso=X.iso,hhmmss=X.hhmmss,tickers=X.tickers;
var MIN=60000,ZERO='0001-01-01T00:00:00Z';
function T(t){return t?iso(t):ZERO;}
function inst(server,dbs){return {server:server+'.corp.example.com',machine:server,instanceName:'',serverName:server,version:'15.0.4385.2',major:15,edition:'Standard Edition (64-bit)',hadr:false,dataPath:'D:\\SQLData',logPath:'L:\\SQLLogs',backupPath:'E:\\SQLBackup',serviceAccount:'CORP\\svc-sql',dnsDomain:'corp.example.com',sysadmin:true,databases:dbs,readAt:iso(now())};}
var srcDbs=[{name:'Finance',state:'ONLINE',recovery:'FULL',dataMB:48200,logMB:4100},{name:'HR',state:'ONLINE',recovery:'FULL',dataMB:6100,logMB:900},{name:'Reports',state:'ONLINE',recovery:'SIMPLE',dataMB:2300,logMB:120},{name:'Orders',state:'ONLINE',recovery:'FULL',dataMB:9800,logMB:1500,ag:'AG-SALES'},{name:'master',state:'ONLINE',recovery:'SIMPLE',dataMB:6,logMB:2},{name:'msdb',state:'ONLINE',recovery:'SIMPLE',dataMB:40,logMB:8}];
function readView(src,dst){
 return {source:inst(src,clone(srcDbs)),target:inst(dst,[]),
  choices:{Finance:{method:'logship',why:'Full recovery model and 52 GB: log shipping keeps the copy minutes behind, so the cutover takes about a minute.',available:['backup','logship'],unavailable:{dag:'It is not in an availability group.'}},
   HR:{method:'logship',why:'Full recovery model: log shipping keeps the copy minutes behind.',available:['backup','logship'],unavailable:{dag:'It is not in an availability group.'}},
   Reports:{method:'backup',why:'Simple recovery model and 2 GB: backed up and restored at cutover in a few minutes.',available:['backup'],unavailable:{logship:'The simple recovery model keeps no log backups. Switch it to full on the old server to ship its log.',dag:'It is not in an availability group.'}},
   Orders:{method:'dag',why:'It is in the availability group AG-SALES: a distributed availability group keeps it in sync and fails back if needed.',available:['backup','logship','dag'],unavailable:{}}},
  reasons:{master:{off:'System database: its logins, jobs and linked servers move as instance objects.'},msdb:{off:'System database: its logins, jobs and linked servers move as instance objects.'}},
  objects:[{kind:'login',name:'CORP\\app-finance',action:'create'},{kind:'login',name:'report_reader',action:'create',detail:'With its SID and password hash'},{kind:'login',name:'sa',action:'skip',detail:'Built in'},{kind:'job',name:'Finance nightly close',action:'create',detail:'Created turned off'},{kind:'linked',name:'ERP01',action:'manual',detail:'Its remote password cannot be read; set it on the new server.'}],
  config:[{name:'max degree of parallelism',old:'4',new:'0',default:true},{name:'cost threshold for parallelism',old:'50',new:'5',default:true},{name:'max server memory (MB)',old:'57344',new:'2147483647',default:false}],
  counts:{logins:14,jobs:6,linked:1,operators:2},findings:[],
  suggest:{backupPath:'\\\\'+src+'\\E$\\SQLBackup\\SIMM',backupWhy:'The old server\'s own backup folder through its administrative share. Both SQL Server service accounts need access to it; Check backup folder tests that.',dataPath:'D:\\SQLData',logPath:'L:\\SQLLogs',pathWhy:'The new server\'s default data and log folders.',syncMinutes:15,syncWhy:'Every 15 minutes keeps the copy close with little load.',concurrency:2,aliasZone:'corp.example.com',aliasWhy:'The new server\'s DNS domain.',objectsWhy:'Logins keep their SIDs and passwords, so database users and applications sign in unchanged. Jobs are created turned off and turned on at cutover.',disableJobs:true,disableJobWhy:'Turning the old server\'s jobs off at cutover keeps a job from running against the old copy too. Roll back turns them on again.'},errors:{}};
}
function db(name,method,state,o){var t=now();var d={name:name,method:method,wave:0,state:state,facts:{name:name},nextTry:ZERO,seeded:ZERO,lastRound:ZERO,cutoverAt:ZERO,cutAt:ZERO,testedAt:ZERO,rolledBackAt:ZERO};if(state==='ready'||state==='done'){d.seeded=T(t-5*3600000);d.lastRound=T(t-4*MIN);}if(state==='done'){d.cutAt=T(t-40*MIN);}for(var k in o||{}){d[k]=o[k];}return d;}
var SQ={plans:[],read:null,reading:false,message:'',failed:false,logs:[]},seq=2;
function log(p,text){SQ.logs.push(hhmmss(now())+'  '+p.name+': '+text);if(SQ.logs.length>200){SQ.logs.shift();}}
function plan(id,name,src,dst,dbs){return {id:id,name:name,created:T(now()-6*3600000),source:{server:src,auth:'current'},target:{server:dst,auth:'current'},backupPath:'\\\\'+src+'\\E$\\SQLBackup\\SIMM',dataPath:'D:\\SQLData',logPath:'L:\\SQLLogs',syncMinutes:15,concurrency:2,startAt:ZERO,paused:false,objects:{logins:true,jobs:true,disableSourceJobs:true},alias:{name:'sql-finance',zone:'corp.example.com',dnsServer:'DC01',spn:true,atCutover:true},databases:dbs,objectsAt:T(now()-6*3600000),objectsNote:'Created 14 logins with their SIDs and passwords, 6 jobs (turned off until cutover) and 2 operators. 1 linked server needs its password set by hand.',objectItems:[{kind:'login',name:'CORP\\app-finance',action:'created'},{kind:'job',name:'Finance nightly close',action:'created',detail:'turned off'},{kind:'linked',name:'ERP01',action:'manual',detail:'set its remote password'}]};}
SQ.plans.push(plan('q1','Finance SQL to SQL22','SQL12','SQL22',[db('Finance','logship','ready',{wave:1}),db('HR','logship','ready',{wave:1,testedAt:T(now()-90*MIN)}),db('Reports','backup','waiting',{wave:2,step:'Copied at cutover'})]));
log(SQ.plans[0],'shipped the log of Finance (38 MB).');
function view(){return {plans:clone(SQ.plans),running:{},read:SQ.read,reading:SQ.reading,message:SQ.message,failed:SQ.failed,held:[],dnsUser:'',logs:SQ.logs.slice(-60),ready:{},methods:{backup:'Backup and restore',logship:'Log shipping',dag:'Distributed availability group'},signedInAccount:'CORP\\admin',limits:{minSync:5,maxSync:1440}};}
function find(id){return SQ.plans.filter(function(p){return p.id===id;})[0];}
on('/api/sql/state',function(){return ok({sql:view()});});
on('/api/sql/read',function(q){var s=(q.data.source||{}).server,d=(q.data.target||{}).server;if(!s||!d){return bad('Type both SQL Servers.');}SQ.reading=true;SQ.message='Reading '+s+' and '+d+' (read only).';setTimeout(function(){SQ.read=readView(s.split('.')[0].toUpperCase(),d.split('.')[0].toUpperCase());SQ.reading=false;SQ.message='Read '+s+' and '+d+'.';},1500);return X.J(202,{ok:true});});
on('/api/sql/check',function(q){var f=[];(q.data.databases||[]).forEach(function(d){if(d.method==='backup'){f.push({db:d.name,severity:'info',code:'backup-outage',message:'Backed up and restored at cutover: users wait while it copies (a few minutes for its size).'});}});f.push({severity:'warning',code:'version',message:'The new server runs a later version: the databases are upgraded when they come online and cannot be moved back to the old version. Roll back uses the old databases, which stay as they were.'});return ok({findings:f});});
on('/api/sql/checkfolder',function(q){return ok({message:'Both SQL Server services can use '+q.data.backupPath+'.'});});
on('/api/sql/signin',function(){return ok();});
on('/api/sql/forget',function(){return ok();});
on('/api/sql/create',function(q){var d=q.data;if(!d.name||d.confirm!==d.name){return bad('Confirm the plan\'s name to start it.');}if(!(d.databases||[]).length){return bad('Choose at least one database.');}
 var p=plan('q'+(seq++),d.name,d.source.server,d.target.server,d.databases.map(function(x){return db(x.name,x.method,'waiting',{wave:x.wave||0,targetName:x.targetName||''});}));p.alias=d.alias||p.alias;p.syncMinutes=d.syncMinutes||15;p.objectsNote='Copying logins, jobs and linked servers.';
 SQ.plans.push(p);log(p,'started.');return ok({plan:p.id,findings:[]});});
on('/api/sql/action',function(q){var d=q.data,p=find(d.plan);if(!p){return bad('That SQL plan no longer exists.');}var names=d.names||[],pick=p.databases.filter(function(x){return names.indexOf(x.name)>=0;});
 if((d.action==='cutover'||d.action==='schedule'||d.action==='rollback')&&d.confirm!==String(names.length)){return bad('Confirm '+names.length+' to continue.');}
 switch(d.action){
  case 'sync':pick.forEach(function(x){if(x.method==='logship'&&x.state==='ready'){x.state='syncing';x._t=0;}});break;
  case 'test':pick.forEach(function(x){if(!x.test&&x.state!=='done'){x.test={name:x.name+'_TEST',id:40+seq,created:iso(now())};x.testedAt=iso(now());log(p,'brought the test copy '+x.name+'_TEST online on '+p.target.server+'.');}});break;
  case 'endtest':pick.forEach(function(x){if(x.test){log(p,'removed the test copy '+x.test.name+'.');delete x.test;}});break;
  case 'cutover':pick.forEach(function(x){x.state='cutting';x.step='Disconnecting users and backing up the tail of the log';x._t=0;});break;
  case 'schedule':pick.forEach(function(x){x.cutoverAt=d.at;});break;
  case 'unschedule':pick.forEach(function(x){x.cutoverAt=ZERO;});break;
  case 'rollback':pick.forEach(function(x){if(x.state==='done'){x.state='rolled-back';x.rolledBackAt=iso(now());x.step='';x.note='The new copy is offline (not deleted); the old database is back online.';log(p,'rolled '+x.name+' back to '+p.source.server+'.');}});break;
  case 'reseed':pick.forEach(function(x){x.state='seeding';x.seeded=ZERO;x._t=0;x.error='';});break;
  case 'pause':p.paused=true;break;
  case 'resume':p.paused=false;break;
  case 'alias':p.aliasState={changedAt:iso(now()),target:p.target.server};log(p,'pointed '+p.alias.name+' at '+p.target.server+'.');break;
  case 'unalias':if(p.aliasState){p.aliasState.restored=iso(now());}log(p,'pointed '+p.alias.name+' back at '+p.source.server+'.');break;
  case 'delete':if(d.confirm!==p.name){return bad('Confirm the plan\'s name.');}SQ.plans.splice(SQ.plans.indexOf(p),1);break;
 }
 return ok();});
tickers.push(function(){
 SQ.plans.forEach(function(p){if(p.paused){return;}p.databases.forEach(function(x){
  if(x.state==='waiting'&&x.method!=='backup'){x.state='seeding';x._t=0;x.step='Backing up '+x.name+' on '+p.source.server;}
  else if(x.state==='seeding'){x._t=(x._t||0)+1;if(x._t===4){x.step='Restoring on '+p.target.server+' (left restoring)';}if(x._t>=8){x.state='ready';x.step='';x.seeded=iso(now());x.lastRound=iso(now());log(p,'first copy of '+x.name+' is restored and receiving the log.');}}
  else if(x.state==='syncing'){x._t=(x._t||0)+1;if(x._t>=3){x.state='ready';x.lastRound=iso(now());log(p,'shipped the log of '+x.name+' ('+(2+Math.floor(Math.random()*40))+' MB).');}}
  else if(x.state==='ready'&&x.method==='logship'&&Date.now()-Date.parse(x.lastRound)>p.syncMinutes*MIN){x.state='syncing';x._t=0;}
  else if(x.state==='cutting'){x._t=(x._t||0)+1;if(x._t===3){x.step='Restoring the last log backup on '+p.target.server;}if(x._t===5){x.step='Bringing it online and turning its jobs on';}if(x._t>=7){x.state='done';x.step='';x.cutAt=iso(now());x.seeded=x.seeded===ZERO?iso(now()):x.seeded;log(p,x.name+' is online on '+p.target.server+'; the old database is left restoring for Roll back.');
   if(p.alias&&p.alias.atCutover&&p.databases.every(function(y){return y.state==='done';})&&!p.aliasState){p.aliasState={changedAt:iso(now()),target:p.target.server};log(p,'pointed '+p.alias.name+' at '+p.target.server+'.');}}}
 });});
});
})();
