/* Actual application HTML and production CSP, synthetic state only. */
const fs=require('fs'),http=require('http'),assert=require('assert'),crypto=require('crypto');
assert.equal(crypto.createHash('sha256').update(fs.readFileSync('tests/fixtures/index-v3.2.9.html')).digest('hex'),'b7145c6b9d0a83949606d10d3671815ba7e43e5f894bca24df6430c8c1ced609','Published negative-control fixture changed');
const {chromium}=require('playwright');
const out='build/workspace-validation';fs.mkdirSync(out,{recursive:true});
const report={status:'running',checks:[]};
const policy=fs.readFileSync('src/app/main.go','utf8').match(/applicationPageCSP\s*=\s*"([^"]+)"/)[1];
const args=process.argv.slice(2),executable=args.includes('--executable')?args[args.indexOf('--executable')+1]:null;
// Synthetic restore scan: a different source server, one unavailable role, high-impact and domain-wide components and a multi-item registry export.
function restoreScanFixture(){return {sourceComputer:'FS-OLD-07',currentComputer:'SRV-DEMO-01',collectedAt:'2026-11-01 09:14',sameComputer:false,isAdministrator:true,manifestVerified:true,manifestMessage:'All 14 export files match the package manifest.',components:[
 {id:'DHCP',title:'DHCP Server',description:'Scopes, reservations, options and failover settings.',impact:'high',available:true,blockedReason:'',warnings:['Existing scopes with the same ID are replaced.'],items:[{id:'DHCP-config',title:'DHCP configuration',detail:'',available:true,blockedReason:'',defaultSelected:true}]},
 {id:'NPS',title:'Network Policy Server',description:'RADIUS clients and network policies.',impact:'high',available:false,blockedReason:'The Network Policy Server role is not installed on this server.',warnings:[],items:[{id:'NPS-config',title:'NPS configuration',detail:'',available:false,blockedReason:'The Network Policy Server role is not installed on this server.',defaultSelected:true}]},
 {id:'GPO',title:'Group Policy objects',description:'Backed-up Group Policy objects.',impact:'domain',available:true,blockedReason:'',warnings:['Changes replicate to every domain controller. <b>not markup</b>'],items:[{id:'GPO-all',title:'All backed-up GPOs',detail:'',available:true,blockedReason:'',defaultSelected:true}]},
 {id:'Registry',title:'Registry keys',description:'Exported registry hives for selected services.',impact:'normal',available:true,blockedReason:'',warnings:[],items:[
  {id:'reg-lanman',title:'LanmanServer parameters',detail:'HKLM\\SYSTEM\\CurrentControlSet\\Services\\LanmanServer',available:true,blockedReason:'',defaultSelected:true},
  {id:'reg-spooler',title:'Spooler settings',detail:'HKLM\\SYSTEM\\CurrentControlSet\\Services\\Spooler',available:true,blockedReason:'',defaultSelected:false},
  {id:'reg-wsus',title:'Windows Update policy',detail:'HKLM\\SOFTWARE\\Policies\\Microsoft\\Windows\\WindowsUpdate',available:false,blockedReason:'The export file is missing from the package.',defaultSelected:true}]},
 {id:'Printers',title:'Printers and drivers',description:'Print queues, ports and printer settings.',impact:'normal',available:true,blockedReason:'',warnings:[],items:[{id:'Printers-all',title:'Print queues and ports',detail:'',available:true,blockedReason:'',defaultSelected:true}]}]};}
// Synthetic file migration discovery: fictional old server, one system share, one share on a non-local path.
function migrateDiscoveryFixture(){return {source:'FS-OLD-07',sourceFqdn:'fs-old-07.example.com',currentComputer:'SRV-DEMO-01',sameServer:false,isAdministrator:true,
 shares:[
  {name:'Finance',path:'E:\\Data\\Finance',description:'Finance team',system:false,remotePath:'\\\\FS-OLD-07\\E$\\Data\\Finance',access:[{account:'EXAMPLE\\Finance',type:'Allow',right:'Change'}],warnings:['Permissions name local accounts of the old server, which do not exist here: FS-OLD-07\\Scanner. <b>not markup</b>']},
  {name:'Projects',path:'E:\\Data\\Projects',description:'',system:false,remotePath:'\\\\FS-OLD-07\\E$\\Data\\Projects',access:[],warnings:[]},
  {name:'NETLOGON',path:'C:\\Windows\\SYSVOL\\sysvol\\example.com\\SCRIPTS',description:'Logon server share',system:true,remotePath:'\\\\FS-OLD-07\\C$\\Windows\\SYSVOL\\sysvol\\example.com\\SCRIPTS',access:[],warnings:['System share. It is normally re-created by its role.']}],
 localShares:['ADMIN$','C$'],volumes:[{drive:'C:',freeBytes:900e9,sizeBytes:1000e9},{drive:'D:',freeBytes:400e9,sizeBytes:500e9},{drive:'F:',freeBytes:100e9,sizeBytes:200e9}],warnings:[]};}
function migrateIdle(){return {running:false,action:'',status:'idle',message:'',source:'',discovery:null,plan:[],space:[],prechecked:false,canCopy:false,canCutover:false,dfs:null,alias:null,sourceReadOnly:false,aliasAdded:'',logs:[],workPath:'',cancelRequested:false,startedAtUnixMs:0,endedAtUnixMs:0,account:'',accessDenied:false};}
// Synthetic VMware environment: fictional vCenter, hosts and VMs only.
function vmwareIdle(){return {running:false,action:'',status:'idle',message:'',server:'',account:'',certificate:null,summary:null,inventorySerial:0,outputFolder:'',reportPath:'',runbookPath:'',planCsvPath:'',guestSerial:0,guestMessage:'',guestError:false,guestWorkPath:'C:\\Users\\admin\\AppData\\Local\\Server Inventory Configuration Manager\\Guest-Migration',compareSerial:0,compareError:'',sendSerial:0,send:null,sendError:'',receiver:{running:false,port:44380,sessions:0,firewall:false,defaultFolder:'D:\\Migration\\VMware-Copies\\Received'},cancelRequested:false,logs:[],startedAtUnixMs:0,endedAtUnixMs:0};}
// Synthetic disk converter state; formats mirror vmDiskFormatNames in vmdisk_image.go.
function convertIdle(){return {running:false,status:'idle',message:'',source:'',target:'',format:'',info:null,progress:{pass:0,chunks:0,done:0,bytesRead:0,bytesWritten:0,changed:0,zero:0},result:null,cancelRequested:false,serial:0,startedAtUnixMs:0,endedAtUnixMs:0,
 formats:[{id:'vhdx',name:'VHDX (dynamic)',extension:'.vhdx',use:'Hyper-V, Scale HyperCore, Nutanix AHV',limit:70368744177664},{id:'qcow2',name:'qcow2',extension:'.qcow2',use:'Proxmox VE, Nutanix AHV, Scale HyperCore, KVM',limit:4611686018427387904},{id:'vmdk',name:'VMDK (monolithic sparse)',extension:'.vmdk',use:'VMware Workstation, vmkfstools -i on ESXi, Nutanix AHV',limit:2190433320960},{id:'vmdk-flat',name:'VMDK (ESXi flat)',extension:'.vmdk',use:'ESXi datastores (descriptor plus -flat.vmdk)',limit:4611686018427387904},{id:'vhd',name:'VHD (dynamic)',extension:'.vhd',use:'Older Hyper-V, Scale HyperCore, Virtual PC',limit:2190433320960},{id:'vhd-fixed',name:'VHD (fixed)',extension:'.vhd',use:'Azure uploads and tools that need a fixed VHD',limit:2190433320960},{id:'raw',name:'Raw image',extension:'.img',use:'dd, physical disks (V2P), Proxmox, any hypervisor',limit:4611686018427387904}],
 repairTargets:[{id:'hyperv',label:'Hyper-V',driver:'hyperv',virtio:false},{id:'scale',label:'Scale Computing HyperCore',driver:'kvm',virtio:true},{id:'nutanix',label:'Nutanix AHV',driver:'kvm',virtio:true},{id:'proxmox',label:'Proxmox VE (KVM)',driver:'kvm',virtio:true},{id:'vsphere',label:'VMware ESXi',driver:'vmware',virtio:false},{id:'physical',label:'Physical server',driver:'physical',virtio:false}],task:'convert',repairTarget:'',repair:null,log:[],
 send:{platforms:[{id:'vsphere',name:'VMware ESXi or vCenter',detail:'Imported as a new VM through vSphere (stream-optimized VMDK).'},{id:'hyperv',name:'Hyper-V on this computer',detail:'A VHDX in the folder you choose, attached to a new VM.'},{id:'proxmox',name:'Proxmox VE',detail:'Uploaded to a storage with the Import content type, then imported into a new VM.'},{id:'nutanix',name:'Nutanix AHV',detail:'Uploaded as a disk image in Prism Central, then cloned into a new VM.'},{id:'scale',name:'Scale HyperCore',detail:'Uploaded as a virtual disk (HyperCore 9.2 or later), then attached to a new VM.'}],platform:'',server:'',user:'',node:'',connected:false,version:'',hyperVHere:false,stage:'',done:0,total:0}};}
function convertInfoFixture(path,size){return {path,format:'vmdk',formatName:'VMDK',variant:'monolithicSparse',virtualSize:size||107374182400,fileSize:21474836480,dataBytes:21474836480,chain:[path,'D:\\VMs\\APP01\\APP01-base.vmdk'],notes:['Differencing disk: 2 disks in the chain are merged into one','<b>not markup</b>']};}
function vmFixtureVM(name,o){const r=(target,status,findings,generation)=>({target,status,generation,findings});return Object.assign({name,id:'vm-'+name.toLowerCase(),datacenter:'DC-East',cluster:'Prod-Cluster',host:'esx01.example.com',folder:'Servers',template:false,powerState:'poweredOn',guestId:'windows2019srv_64Guest',guestOs:'Microsoft Windows Server 2019 (64-bit)',guestOsRunning:'Microsoft Windows Server 2019 Standard',ipAddress:'192.0.2.10',cpus:4,coresPerSocket:2,memoryMb:16384,hardwareVersion:'vmx-15',firmware:'efi',secureBoot:true,toolsStatus:'toolsOk',toolsRunning:true,provisionedBytes:128849018880,usedBytes:64424509440,
 disks:[{label:'Hard disk 1',controller:'SCSI 0',bus:0,unit:0,capacityBytes:107374182400,datastore:'DS-SSD-01',provisioning:'thin',mode:'persistent',rdm:''}],nics:[{label:'Network adapter 1',adapterType:'VMXNET3',network:'VM-Servers',mac:'00:50:56:00:00:01',connected:true,ipAddresses:['192.0.2.10'],static:true,guestKnown:true}],snapshots:[],
 readiness:[r('hyperv',o.hv||'ready',o.hvFindings||[{severity:'info',code:'tools-remove',message:'Remove VMware Tools after cutover.',action:''}],2),r('scale',o.sc||'review',o.scFindings||[{severity:'warning',code:'virtio-drivers',message:'Install the VirtIO drivers before cutover.',action:'Install the Scale Computing guest tools.'}]),r('nutanix',o.nx||'review',o.nxFindings||[{severity:'warning',code:'virtio-drivers',message:'Install the Nutanix VirtIO drivers before cutover.',action:'Let Nutanix Move install them.'}])]},o.vm||{});}
function vmwareInventoryFixture(){return {schema:1,server:'vc01.example.com',serverType:'vCenter Server',product:'VMware vCenter Server 8.0.2',collectedAt:'2026-11-20T09:30:00Z',datacenters:['DC-East'],clusters:[{name:'Prod-Cluster',datacenter:'DC-East',hosts:3,ha:true,drs:true,cpuCores:96,memoryBytes:1649267441664}],hosts:['esx01','esx02','esx03'].map((n,i)=>({name:n+'.example.com',datacenter:'DC-East',cluster:'Prod-Cluster',vendor:'Example',model:'R760',cpuModel:'Xeon Gold',sockets:2,memoryBytes:549755813888,version:'8.0.2',build:'22380479',connectionState:'connected',license:{name:'vSphere 8 Enterprise Plus',key:'AB12C-*****-*****-*****-MN90P'},details:{serialNumber:'SVC100'+i,biosVersion:'2.19.1',managementIp:'192.0.2.2'+i,overallStatus:'green'}})),licenses:[{name:'vSphere 8 Enterprise Plus',key:'AB12C-*****-*****-*****-MN90P',product:'VMware ESX Server',version:'8.*',used:6,total:8,unit:'cpuPackage',expires:'',assignedTo:['esx01.example.com','esx02.example.com','esx03.example.com']}],datastores:[{name:'DS-SSD-01',datacenter:'DC-East',type:'VMFS',capacityBytes:4398046511104,freeBytes:1649267441664,provisionedBytes:3298534883328,accessible:true,shared:true,vmCount:4,hosts:['esx01.example.com','esx02.example.com','esx03.example.com']},{name:'DS-NL-02',datacenter:'DC-East',type:'NFS',capacityBytes:8796093022208,freeBytes:659706976665,provisionedBytes:8246337208320,accessible:true,shared:true,vmCount:1,hosts:['esx02.example.com','esx03.example.com']}],networks:[{name:'VM-Servers',datacenter:'DC-East',kind:'Distributed',switch:'dvs-prod',vlan:'20',vmCount:3},{name:'VM-DMZ',datacenter:'DC-East',kind:'Distributed',switch:'dvs-prod',vlan:'30',vmCount:1}],warnings:[],vms:[
 vmFixtureVM('APP01',{}),
 vmFixtureVM('SQL01',{hv:'review',hvFindings:[{severity:'warning',code:'snapshots',message:'1 snapshot, the oldest 45 days old.',action:'Delete or consolidate snapshots before cutover.'}],vm:{host:'esx02.example.com',disks:[{label:'Hard disk 1',controller:'SCSI 0',bus:0,unit:0,capacityBytes:107374182400,datastore:'DS-SSD-01',provisioning:'thick',mode:'persistent',rdm:'',file:'[DS-SSD-01] SQL01/SQL01.vmdk'},{label:'Hard disk 2',controller:'SCSI 1',bus:1,unit:0,capacityBytes:1099511627776,datastore:'DS-NL-02',provisioning:'thick',mode:'persistent',rdm:'',file:'[DS-NL-02] SQL01/SQL01_1.vmdk'}],memoryMb:65536,cpus:8,snapshots:[{name:'Before patching <b>not markup</b>',created:'2026-10-06T10:00:00Z',quiesced:false,depth:1}]}}),
 vmFixtureVM('LEGACY03',{hv:'blocked',sc:'blocked',hvFindings:[{severity:'blocker',code:'os-obsolete',message:'Windows Server 2003 is not supported on Hyper-V.',action:'Upgrade or rebuild the server first.'}],scFindings:[{severity:'blocker',code:'os-obsolete',message:'Windows Server 2003 is not supported on HyperCore.',action:'Upgrade or rebuild the server first.'}],vm:{guestOsRunning:'Microsoft Windows Server 2003 Standard (32-bit)',firmware:'bios',secureBoot:false,cpus:2,memoryMb:4096}}),
 vmFixtureVM('WEB02',{sc:'ready',scFindings:[],vm:{host:'esx03.example.com',guestOsRunning:'Ubuntu Linux (64-bit)',ipAddress:'192.0.2.22',powerState:'poweredOff',nics:[{label:'Network adapter 1',adapterType:'VMXNET3',network:'VM-DMZ',mac:'00:50:56:00:00:22',connected:true,ipAddresses:['192.0.2.22'],static:false,guestKnown:true}]}})]};}
function vmwareSummaryFixture(){return {server:'vc01.example.com',serverType:'vCenter Server',product:'VMware vCenter Server 8.0.2',collectedAt:'2026-11-20 09:30',hosts:3,clusters:1,datastores:2,networks:3,vms:4,templates:0,poweredOn:3,vcpus:18,memoryBytes:103079215104,provisionedBytes:515396075520,usedBytes:257698037760,snapshots:1,staticIpVms:4,
 readiness:[{target:'hyperv',ready:2,review:1,blocked:1},{target:'scale',ready:1,review:2,blocked:1},{target:'nutanix',ready:0,review:4,blocked:0}]};}
function vmwarePlanFixture(){return {schema:1,server:'vc01.example.com',target:'hyperv',networks:[{source:'VM-Servers',sourceVlan:'20',targetName:'',targetVlan:''},{source:'VM-DMZ',sourceVlan:'30',targetName:'',targetVlan:''}],storage:[{source:'DS-SSD-01',target:''}],
 capacity:{nodes:3,coresPerNode:0,memoryGbPerNode:0,usableStorageTb:0,reserveNodes:1,maxVcpuPerCore:4,reservedMemoryGbPerNode:8},vms:vmwareInventoryFixture().vms.map(v=>({id:v.id,name:v.name,wave:0,hold:false,exclude:false,notes:''})),updatedAt:'2026-11-20T09:31:00Z'};}
function vmwareSizingFixture(configured){return configured?{vms:3,vcpus:14,memoryBytes:90194313216,provisionedBytes:386547056640,usedBytes:193273528320,usableNodes:2,cores:64,vcpuPerCore:0.2,memoryPercent:18,storagePercent:9,provisionedPercent:18,status:'ok',notes:['The VMs fit with one node kept free.'],configured:true}:
 {vms:3,vcpus:14,memoryBytes:90194313216,provisionedBytes:386547056640,usedBytes:193273528320,usableNodes:0,cores:0,vcpuPerCore:0,memoryPercent:0,storagePercent:0,provisionedPercent:0,status:'unknown',notes:['Describe the new cluster to check that the VMs fit.'],configured:false};}
function vmwareGuestFixture(platform){const adapter=(name,mac,index,addresses,isStatic)=>({Name:name,Description:'Microsoft Hyper-V Network Adapter',MacAddress:mac,InterfaceIndex:index,Status:'Up',Dhcp:!isStatic,Static:isStatic,Addresses:addresses.map(a=>({Address:a,PrefixLength:24,Family:'IPv4',Origin:isStatic?'Manual':'Dhcp'})),Gateways:isStatic?[{NextHop:'192.0.2.1',Metric:0,Family:'IPv4'}]:[],DnsServers:['192.0.2.53']});
 const capture={Schema:1,CapturedAt:'2026-11-20T08:00:00.000Z',Findings:[{Severity:'info',Message:'Recorded static IP settings for 1 adapter(s). <b>not markup</b>'},{Severity:'good',Message:'Hyper-V storage and network drivers are built into this Windows version.'}],
  State:{ComputerName:'SRV-DEMO-01',Adapters:[Object.assign(adapter('Ethernet0','00-50-56-00-00-01',4,['192.0.2.10'],true),{Description:'vmxnet3 Ethernet Adapter'})],RunningAutoServices:['Spooler','VMTools','W3SVC']}};
 return {Schema:1,Capture:capture,RollbackAvailable:false,MissingAutoServices:['W3SVC'],MissingAddresses:['192.0.2.10'],
  TargetChecks:[{Target:'hyperv',Severity:'good',Message:'Hyper-V drivers are built in.'},{Target:'nutanix',Severity:'warning',Message:'VirtIO drivers are not installed.'}],
  Ghosts:[{InstanceId:'PCI\\VEN_15AD&DEV_07B0&SUBSYS_07B015AD\\FF565000B8E7AA0000',Description:'vmxnet3 Ethernet Adapter',Vmware:true},{InstanceId:'USB\\VID_0BDA&PID_8153\\000001',Description:'USB Ethernet',Vmware:false}],
  State:{ComputerName:'SRV-DEMO-01',Domain:'example.com',OperatingSystem:'Microsoft Windows Server 2019 Standard',Firmware:'UEFI',SecureBoot:true,Platform:{Name:platform||'Hyper-V'},VmwareTools:{Installed:true,Version:'12.3.0',ProductCode:'{00000000-0000-0000-0000-000000000000}'},
   Adapters:[adapter('Ethernet','00-15-5D-00-00-01',7,['169.254.0.0'].slice(1),false)],Disks:[{Number:0,FriendlyName:'Virtual Disk',SizeBytes:107374182400,IsOffline:false,IsReadOnly:false,IsBoot:true,IsSystem:true},{Number:1,FriendlyName:'Virtual Disk',SizeBytes:214748364800,IsOffline:true,IsReadOnly:false,OfflineReason:'Policy',IsBoot:false,IsSystem:false}]}};}
function vmwareCompareFixture(){const change=(dataset,label,kind,item,expected,fields)=>({dataset,label,kind,item,fields:fields||[],expected,important:!expected&&dataset==='services'});
 const result={before:{path:'D:\\Packages\\Before',computer:'APP01',collectedAt:'2026-11-19 22:00'},after:{path:'D:\\Packages\\After',computer:'APP01',collectedAt:'2026-11-21 07:30'},sameComputer:true,expectedCount:2,changeCount:4,attentionCount:2,reportPath:'C:\\Reports\\Comparison_APP01\\Before-After-Comparison.html',
  attention:[change('services','Services','changed','W3SVC (World Wide Web Publishing Service)',false,[{name:'State',before:'Running',after:'Stopped'}]),change('addresses','IP addresses','removed','192.0.2.11 <b>not markup</b>',false)]};
 const datasets=[{key:'services',label:'Services',available:true,note:'',beforeCount:180,afterCount:176,added:0,removed:3,changed:1,expected:3,changes:[result.attention[0],change('services','Services','removed','VMTools (VMware Tools)',true)],more:0},
  {key:'addresses',label:'IP addresses',available:true,note:'',beforeCount:2,afterCount:1,added:0,removed:1,changed:0,expected:0,changes:[result.attention[1]],more:0},
  {key:'printers',label:'Printers',available:false,note:'Not collected in both packages.',beforeCount:0,afterCount:0,added:0,removed:0,changed:0,expected:0,changes:[],more:0}];
 return {result,datasets};}
async function fixture(browser,old=false){
 let saved=null,started=[],saveCalls=0,rejectStart=false,skipRequests=[],opens=[],restoreReject=null,migrateReject={},vmwareReject={};
 const logs=[],dialogs=[],errors=[],restoreCalls={browse:0,scan:[],start:[],cancel:0,tokens:[]},migrateCalls=[],vmwareCalls=[],convertCalls=[];let convertReject={},convertSize=0;
 const state={status:'idle',phase:'Ready',running:false,computerName:'SRV-DEMO-01',isAdministrator:true,powerShellVersion:'5.1',defaultOutputRoot:'C:\\Reports',update:{status:'current',title:'Up to date',message:'Installed v3.2.10. Latest v3.2.10.',canCheck:true},
  restore:{status:'idle',packages:{lastCollection:'',loadedReport:''},items:[],logs:[]},migrate:migrateIdle(),vmware:vmwareIdle(),convert:convertIdle()};
 const vm={inventory:null,plan:null,sizing:null,guest:null,compare:null};
 const html=fs.readFileSync(old?'tests/fixtures/index-v3.2.9.html':'src/app/index.html','utf8').replaceAll('__TOKEN__','workspace-test').replaceAll('__APP_VERSION__','3.2.10').replaceAll('__COLLECTOR_VERSION__','3.2.10');
 const server=http.createServer(async(req,res)=>{
  const url=new URL(req.url,'http://localhost');
  if(url.pathname==='/'){res.writeHead(200,{'Content-Type':'text/html','Content-Security-Policy':policy});res.end(html);return;}
  const send=(code,data)=>{res.writeHead(code,{'Content-Type':'application/json'});res.end(JSON.stringify(data));};
  if(url.pathname==='/api/state'){const since=Number(url.searchParams.get('since')||0);send(200,{...state,collectionPreferences:saved,logs:logs.slice(since).map((text,i)=>({index:since+i,text})),nextLogIndex:logs.length});return;}
  if(url.pathname==='/api/preferences'||url.pathname==='/api/start'){
   let body='';for await(const chunk of req)body+=chunk;
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   const options=JSON.parse(body);
   if(url.pathname==='/api/start'){started.push(options);if(rejectStart){send(400,{error:'The output folder is not writable.'});return;}state.running=true;state.status='running';state.phase='Collecting';}
   else saveCalls++;
   saved={schema:1,options:{...options,includeSensitive:false,exportCategories:options.exportCategories.filter(n=>n!=='NPS')}};
   send(200,{ok:true,preferencesSaved:true});return;
  }
  if(url.pathname.startsWith('/api/restore/')){
   let body='';for await(const chunk of req)body+=chunk;restoreCalls.tokens.push(req.headers['x-app-token']);const data=body?JSON.parse(body):{},action=url.pathname.slice(13);
   if(action==='browse'){restoreCalls.browse++;send(200,{selected:true,path:'D:\\Restore\\FS-OLD-07'});return;}
   if(action==='scan'){restoreCalls.scan.push(data);state.restore={...state.restore,status:'ready',packagePath:data.packagePath,scan:{...restoreScanFixture(),packagePath:data.packagePath}};send(200,{ok:true});return;}
   if(action==='start'){restoreCalls.start.push(data);if(restoreReject){send(400,{error:restoreReject});return;}state.restore={...state.restore,status:'running',message:'Restoring.',startedAtUnixMs:Date.now(),rollbackPath:'',items:[],logs:[]};send(202,{ok:true});return;}
   if(action==='cancel'){restoreCalls.cancel++;state.restore.cancelRequested=true;send(202,{ok:true});return;}
  }
  if(url.pathname.startsWith('/api/migrate/')){
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{},action=url.pathname.slice(13);migrateCalls.push({action,body:data,token:req.headers['x-app-token']});
   if(migrateReject[action]){send(400,{error:migrateReject[action]});return;}
   const m=state.migrate;
   if(action==='discover'){Object.assign(m,{running:true,action:'discover',status:'discovering',message:'Connecting to '+data.source+'.',source:data.source,account:data.user||''});send(202,{ok:true});return;}
   if(action==='plan'){m.plan=data.shares.map(s=>({...s,status:'planned',message:'',percent:0,processed:0,expected:0,precheck:null,previouslySynced:false,lastSyncUnixMs:0,createdShare:false,verified:false}));m.prechecked=false;m.canCopy=false;m.canCutover=false;send(200,{ok:true});return;}
   if(action==='precheck'){Object.assign(m,{running:true,action:'precheck',status:'checking'});send(202,{ok:true});return;}
   if(action==='sync'){Object.assign(m,{running:true,action:'sync',status:'copying'});send(202,{ok:true});return;}
   if(action==='cutover'){Object.assign(m,{running:true,action:'cutover',status:'cuttingover'});send(202,{ok:true});return;}
   if(action==='cancel'){m.cancelRequested=true;send(202,{ok:true});return;}
   if(action==='reset'){state.migrate=migrateIdle();send(200,{ok:true});return;}
   if(action==='browse'){send(200,data.parent?{ok:true,selected:true,path:'E:\\'}:{ok:true,selected:true,path:'F:\\Data\\'+data.share+'-New'});return;}
   send(202,{ok:true});return;
  }
  if(url.pathname.startsWith('/api/vmware/')){
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{},action=url.pathname.slice(12);vmwareCalls.push({action,method:req.method,body:data,token:req.headers['x-app-token']});
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   if(vmwareReject[action]){send(400,{error:vmwareReject[action]});return;}
   const v=state.vmware;
   if(action==='inventory'){send(200,vm.inventory?{ok:true,serial:v.inventorySerial,inventory:vm.inventory,plan:vm.plan,sizing:vm.sizing,attention:vm.attention||[]}:{ok:true,inventory:null});return;}
   if(action==='hyperv'){Object.assign(v,{running:true,action:'hyperv',status:'connecting'});send(202,{ok:true});return;}
   if(action==='connect'){Object.assign(v,{running:true,action:'connect',status:'connecting',server:data.server,account:data.user,certificate:null});send(202,{ok:true});return;}
   if(action==='plan'){vm.plan=JSON.parse(JSON.stringify(data.plan));if(data.suggest){vm.plan.vms.forEach((p,i)=>{p.hold=i===2;p.wave=i===2?0:(i===3?1:2);});}vm.sizing=['azure','aws'].includes(vm.plan.target)?Object.assign(vmwareSizingFixture(false),{status:'quota',notes:['Check the subscription quota covers 14 vCPUs.']}):vmwareSizingFixture(!!vm.plan.capacity.coresPerNode);send(200,{ok:true,plan:vm.plan,sizing:vm.sizing,saved:true});return;}
   if(action==='export-plan'){v.runbookPath='C:\\Reports\\VMware_vc01\\Migration-Runbook.html';v.planCsvPath='C:\\Reports\\VMware_vc01\\Migration-Plan.csv';v.runbookDocxPath='C:\\Reports\\VMware_vc01\\Migration-Runbook.docx';send(200,{ok:true});return;}
   if(action==='reset'){state.vmware=Object.assign(vmwareIdle(),{inventorySerial:v.inventorySerial+1,logs:v.logs});vm.inventory=null;vm.plan=null;send(200,{ok:true});return;}
   if(action==='prepare'){Object.assign(v,{running:true,action:'prepare',status:'preparing'});send(202,{ok:true});return;}
   if(action==='move'){Object.assign(v,{running:true,action:'move',status:'moving'});send(202,{ok:true});return;}
   if(action==='target/connect'){v.target=Object.assign(v.target||{},{platform:data.platform,server:data.server,user:data.user,node:data.node||'pve1',connected:true,inventory:{platform:data.platform,version:'Proxmox VE 8.3.0',nodes:[{id:'pve1',name:'pve1',detail:'online'}],storages:[{id:'local-lvm',name:'local-lvm',detail:'lvmthin, images, 1.0 TB free'},{id:'ceph-vm',name:'ceph-vm',detail:'rbd, images, 9.0 TB free'}],networks:[{id:'vmbr0',name:'vmbr0',detail:'bridge'},{id:'vmbr1',name:'vmbr1',detail:'bridge'}]},serial:(v.target&&v.target.serial||0)+1});send(200,{ok:true,inventory:v.target.inventory});return;}
   if(action==='target/media'){Object.assign(v,{running:true,action:'media',status:'media'});send(202,{ok:true});return;}
   if(action==='target/upload'){Object.assign(v,{running:true,action:'upload',status:'uploading'});send(202,{ok:true});return;}
   if(action==='target/create'){Object.assign(v,{running:true,action:'target-create',status:'creating'});send(202,{ok:true});return;}
   if(action==='target/receiver'){v.target.receivers={[data.vmId]:{link:{address:data.address,thumbprint:data.thumbprint},disks:[{name:'disk-0',label:'Disk 0, QEMU HARDDISK',size:107374182400}],mapping:['disk-0']}};v.target.serial++;send(200,{ok:true});return;}
   if(action==='target/move'){Object.assign(v,{running:true,action:'move',status:'moving'});send(202,{ok:true});return;}
   if(action==='receiver'){if(data.action==='start'){v.receiver={running:true,port:data.port||44380,code:'K7QMZ-2RT4X-9WBNP-H3VDA',thumbprint:'3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e',folder:data.folder||'D:\\Migration\\VMware-Copies\\Received',addresses:['192.0.2.40'],sessions:0,firewall:!!data.firewall,defaultFolder:'D:\\Migration\\VMware-Copies\\Received'};}else{v.receiver={running:false,port:44380,sessions:0,firewall:false,defaultFolder:'D:\\Migration\\VMware-Copies\\Received',message:'Stopped.'};}send(200,{ok:true});return;}
   if(action==='agent/disks'){send(200,{ok:true,computer:'APP01',disks:[{number:0,size:107374182400,system:true,volumes:[{name:'v1',mounts:[],fileSystem:'FAT32',length:104857600},{name:'v2',mounts:['C:\\'],fileSystem:'NTFS',length:106300000000}]},{number:1,size:536870912000,system:false,volumes:[{name:'v3',mounts:['D:\\'],fileSystem:'NTFS',length:536700000000}]}]});return;}
   if(action==='agent/send'){Object.assign(v,{running:true,action:'send',status:'sending'});send(202,{ok:true});return;}
   if(action==='guest'){send(200,{ok:true,serial:v.guestSerial,result:vm.guest});return;}
   if(action.startsWith('guest/')){Object.assign(v,{running:true,action:'guest-'+action.slice(6)});send(202,{ok:true});return;}
   if(action==='compare/browse'){send(200,{ok:true,selected:true,path:'D:\\Packages\\'+(data.side==='before'?'Before':'After'),computer:'APP01',collectedAt:data.side==='before'?'2026-11-19 22:00':'2026-11-21 07:30'});return;}
   if(action==='compare'&&req.method==='GET'){send(200,Object.assign({ok:true,serial:v.compareSerial},vm.compare||{result:null}));return;}
   if(action==='compare'){Object.assign(v,{running:true,action:'compare'});send(202,{ok:true});return;}
   if(action==='cancel'){v.cancelRequested=true;send(202,{ok:true});return;}
   send(200,{ok:true,selected:false});return;
  }
  if(url.pathname.startsWith('/api/convert/')){
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{},action=url.pathname.slice(13);convertCalls.push({action,body:data,token:req.headers['x-app-token']});
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   if(convertReject[action]){send(400,{error:convertReject[action]});return;}
   const c=state.convert;
   if(action==='browse'&&data.kind==='drivers'){send(200,{ok:true,selected:true,path:'F:\\'});return;}
   if(action==='browse'&&data.kind==='fixup'){send(200,{ok:true,selected:true,path:'E:\\Hyper-V\\Disks\\APP01.vhdx'});return;}
   if(action==='browse'){send(200,data.kind==='folder'?{ok:true,selected:true,path:'E:\\Hyper-V\\Disks'}:{ok:true,selected:true,path:'D:\\VMs\\APP01\\APP01-000001.vmdk'});return;}
   if(action==='localdisks'){send(200,{ok:true,computer:'SRV-DEMO-01',disks:[{number:0,size:137438953472,system:true,volumes:[{name:'\\\\?\\Volume{1}\\',mounts:['C:\\'],fileSystem:'NTFS',length:136365211648}]},{number:1,size:536870912000,system:false,volumes:[{name:'\\\\?\\Volume{2}\\',mounts:['D:\\'],fileSystem:'NTFS',length:536870912000}]}]});return;}
   if(action==='inspect'&&/^\\\\\.\\PhysicalDrive\d+$/.test(data.path)){send(200,{ok:true,info:{path:data.path,format:'raw',formatName:'Disk 0 of this computer',variant:'SRV-DEMO-01',virtualSize:137438953472,fileSize:137438953472,dataBytes:137438953472,notes:['C: (NTFS, 127 GB): read from a new shadow copy.']},boot:{partitionStyle:'GPT',firmware:'uefi',systemPartition:true,partitions:4,note:''},suggested:'SRV-DEMO-01-PhysicalDrive0.vhdx'});return;}
   if(action==='inspect'){const info=convertInfoFixture(data.path,convertSize);send(200,{ok:true,info,boot:{partitionStyle:'GPT',firmware:'uefi',systemPartition:true,partitions:3,note:''},suggested:'D:\\VMs\\APP01\\APP01-000001'+({vhdx:'.vhdx',qcow2:'.qcow2',raw:'.img',vhd:'.vhd','vhd-fixed':'.vhd',vmdk:'.vmdk','vmdk-flat':'.vmdk'}[data.format]||'.vhdx')});return;}
   if(action==='start'){Object.assign(c,{running:true,status:'running',source:data.source,target:data.target,format:data.format,message:'Converting APP01-000001.vmdk.',startedAtUnixMs:Date.now(),endedAtUnixMs:0,progress:{pass:1,chunks:200,done:50,bytesRead:209715200,bytesWritten:104857600,changed:0,zero:10}});send(200,{ok:true});return;}
   if(action==='cancel'){c.cancelRequested=true;send(200,{ok:true});return;}
   if(action==='connect'&&data.server==='untrusted.example.com'&&!data.thumbprint){c.send=Object.assign({},c.send,{platform:data.platform,certificate:{thumbprint:'AA:BB:CC',subject:'CN=untrusted'}});send(409,{error:'The certificate of untrusted.example.com is not trusted.',certificate:{thumbprint:'AA:BB:CC'}});return;}
   if(action==='connect'){c.send=Object.assign({},c.send,{platform:data.platform,server:data.server,user:data.user,node:data.node||'pve1',connected:true,certificate:null,version:'Proxmox VE 8.4',inventory:{platform:data.platform,version:'8.4',nodes:[{id:'pve1',name:'pve1'}],networks:[{id:'vmbr0',name:'vmbr0'},{id:'vmbr1',name:'vmbr1',detail:'<b>lab</b>'}],storages:[{id:'local',name:'local',detail:'dir'},{id:'local-lvm',name:'local-lvm',detail:'lvmthin'}]}});send(200,{ok:true,send:c.send});return;}
   if(action==='forget'){c.send=Object.assign({},c.send,{platform:'',server:'',user:'',connected:false,inventory:null});send(200,{ok:true});return;}
   if(action==='send'){Object.assign(c,{running:true,status:'running',task:'send',message:'Sending APP01.vhdx to Proxmox VE as APP01.',serial:c.serial+1});c.send=Object.assign({},c.send,{stage:'Uploading',done:25,total:100});send(200,{ok:true});return;}
   if(action==='repair'){Object.assign(c,{running:true,status:'running',task:'repair',repairTarget:data.target,message:'Fixing up '+data.path.split('\\').pop()+'.',serial:c.serial+1,log:['Opening '+data.path+' for changes.']});send(200,{ok:true});return;}
   send(200,{ok:true});return;
  }
  if(url.pathname==='/api/open'){let body='';for await(const chunk of req)body+=chunk;opens.push(JSON.parse(body).kind);send(200,{ok:true});return;}
  if(url.pathname==='/report/current'){res.writeHead(200,{'Content-Type':'text/html'});res.end('<!DOCTYPE html><html><head><title>Synthetic</title></head><body><div class="report-content"><section class="report-section active" id="s1"><p>alpha one</p><p>beta</p><p>alpha two</p><p>alpha three</p></section></div></body></html>');return;}
  if(url.pathname==='/api/skip-acl-export'){let body='';for await(const chunk of req)body+=chunk;skipRequests.push(JSON.parse(body));state.aclExport.skipRequested=true;send(202,{ok:true});return;}
  const icon={'/app-icon.svg':'assets/icons/server-inventory.svg','/favicon.ico':'favicon.ico','/app-icon.png':'app-icon.png'}[url.pathname];
  if(icon){res.setHeader('Content-Type',icon.endsWith('.svg')?'image/svg+xml':'image/png');res.end(fs.readFileSync('src/app/'+icon));return;}
  send(200,{ok:true});
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const context=await browser.newContext({viewport:{width:1440,height:1000}}),page=await context.newPage();
 page.on('dialog',async d=>{dialogs.push(d.message());await d.dismiss();});page.on('pageerror',e=>errors.push(e.message));
 const url='http://127.0.0.1:'+server.address().port+'/';await page.goto(url);await page.waitForFunction(()=>document.querySelector('#serverName').textContent==='SRV-DEMO-01');
 return {page,context,state,vm,vmwareCalls,convertCalls,rejectConvert(action,message){convertReject[action]=message},setConvertSize(size){convertSize=size},rejectVmware(action,message){vmwareReject[action]=message},logs,dialogs,errors,started,skipRequests,opens,restoreCalls,migrateCalls,rejectMigrate(action,message){migrateReject[action]=message},rejectRestore(message){restoreReject=message},get saved(){return saved},get saveCalls(){return saveCalls},reject(){rejectStart=true},async close(){assert.deepEqual(errors,[]);await context.close();await new Promise(r=>server.close(r));}};
}
async function check(name,fn){await fn();report.checks.push({name,result:'passed'});console.log('PASS: '+name);}
(async()=>{const browser=await chromium.launch(executable?{executablePath:executable,headless:true,args:['--no-sandbox']}:{channel:'msedge',headless:true});try{
 await check('Published 3.2.9 reproduces unstyled cards and an inventory checkbox that cannot be deselected',async()=>{const f=await fixture(browser,true);try{
  assert.equal(await f.page.locator('.category-check').first().evaluate(e=>getComputedStyle(e).borderTopStyle),'none');
  const dhcp=f.page.locator('input[data-inventory][value=DHCP]');await dhcp.uncheck().catch(()=>{});assert(await dhcp.isChecked());
 }finally{await f.close();}});
 await check('Published 3.2.9 reproduces log scroll jumps and an unlocked event-history field during collection',async()=>{const f=await fixture(browser,true);try{
  await f.page.locator('#tabButtonCollection').click();for(let i=0;i<100;i++)f.logs.push('[INFO] Old message '+i);await f.page.waitForTimeout(1100);
  await f.page.locator('#logView').evaluate(e=>e.scrollTop=0);f.logs.push('[INFO] New message');await f.page.waitForTimeout(1100);assert(await f.page.locator('#logView').evaluate(e=>e.scrollTop>0));
  f.state.running=true;f.state.status='running';await f.page.waitForTimeout(1100);assert(await f.page.locator('#eventDays').isEnabled());
 }finally{await f.close();}});
 const f=await fixture(browser);try{
  await check('Selected cards have real borders, profile state and scope summary',async()=>{
   assert.equal(await f.page.locator('.category-check').first().evaluate(e=>getComputedStyle(e).borderTopStyle),'solid');
   assert.equal(await f.page.locator('#presetMaximum').getAttribute('aria-pressed'),'true');assert((await f.page.locator('#selectionSummary').textContent()).includes('34 inventory categories / 12'));
   await f.page.screenshot({path:out+'/setup-desktop.png'});
  });
  await check('Deselecting inventory clears its dependent export, selecting export restores inventory',async()=>{
   await f.page.locator('input[data-inventory][value=DHCP]').uncheck();assert(!await f.page.locator('input[data-export][value=DHCP]').isChecked());
   await f.page.locator('#exportPanel').evaluate(e=>e.open=true);await f.page.locator('input[data-export][value=DHCP]').check();assert(await f.page.locator('input[data-inventory][value=DHCP]').isChecked());
  });
  await check('Category search uses descriptions, opens exports and preserves every selection',async()=>{
   const before=await f.page.locator('input[data-inventory]:checked').count();await f.page.locator('#scopeSearch').fill('RADIUS');
   assert.equal(await f.page.locator('.category-check:visible').count(),2);assert(await f.page.locator('#exportPanel').evaluate(e=>e.open));
   assert.equal(await f.page.locator('input[data-inventory]:checked').count(),before);await f.page.locator('#scopeSearch').fill('nothing-matches-this');assert(await f.page.locator('#scopeEmpty').isVisible());await f.page.locator('#scopeSearchClear').click();
  });
  await check('Presets and saved choices survive page reopen without persisting sensitive consent',async()=>{
   await f.page.locator('#presetCore').click();await f.page.locator('#saveChoicesButton').click();await f.page.waitForFunction(()=>document.querySelector('#preferencesStatus').textContent.includes('saved for'));
   assert.equal(f.saved.options.inventoryCategories.length,12);assert.equal(f.saved.options.exportCategories.length,0);
   await f.page.reload();await f.page.waitForFunction(()=>document.querySelector('#preferencesStatus').textContent==='Saved choices restored');assert.equal(await f.page.locator('#profileName').textContent(),'Core server');
   await f.page.locator('#exportPanel').evaluate(e=>e.open=true);await f.page.locator('input[data-export][value=NPS]').check();await f.page.locator('#includeSensitive').check();await f.page.locator('#saveChoicesButton').click();await f.page.waitForFunction(()=>document.querySelector('#preferencesStatus').textContent.includes('saved for'));
   await f.page.reload();await f.page.waitForFunction(()=>document.querySelector('#preferencesStatus').textContent==='Saved choices restored');assert(!await f.page.locator('#includeSensitive').isChecked());assert(!await f.page.locator('input[data-export][value=NPS]').isChecked());
  });
  await check('Inline path, empty-scope, fractional-days and NPS validation sends no start request or modal',async()=>{
   await f.page.locator('#outputPath').fill('relative');await f.page.locator('#startButton').click();assert((await f.page.locator('#appNotice').textContent()).includes('absolute'));
   await f.page.locator('#outputPath').fill('C:\\Reports');await f.page.locator('#presetClear').click();await f.page.locator('#startButton').click();assert((await f.page.locator('#appNotice').textContent()).includes('at least one'));
   await f.page.locator('#presetInventory').click();await f.page.locator('#eventDays').fill('1.5');await f.page.locator('#startButton').click();assert((await f.page.locator('#appNotice').textContent()).includes('whole number'));
   await f.page.locator('#eventDays').fill('30');await f.page.locator('#exportPanel').evaluate(e=>e.open=true);await f.page.locator('input[data-export][value=NPS]').check();await f.page.locator('#startButton').click();assert((await f.page.locator('#appNotice').textContent()).includes('NPS'));assert.equal(f.started.length,0);assert.deepEqual(f.dialogs,[]);
  });
  await check('Rejected collection keeps log and choices and reports the error inline',async()=>{
   await f.page.locator('#presetCore').click();f.logs.push('[INFO] Existing diagnostic detail');await f.page.waitForTimeout(1100);f.reject();await f.page.locator('#startButton').click();await f.page.waitForFunction(()=>document.querySelector('#appNotice').textContent.includes('not writable'));assert((await f.page.locator('#logView').textContent()).includes('Existing diagnostic'));assert.equal(await f.page.locator('#profileName').textContent(),'Core server');assert.deepEqual(f.dialogs,[]);
  });
  await check('Application tabs support arrow keys, Home and End with correct selection semantics',async()=>{
   await f.page.locator('#tabButtonSetup').focus();await f.page.keyboard.press('ArrowRight');assert.equal(await f.page.locator('#tabButtonCollection').getAttribute('aria-selected'),'true');await f.page.keyboard.press('End');assert.equal(await f.page.locator('#tabButtonConvert').getAttribute('aria-selected'),'true');await f.page.keyboard.press('ArrowLeft');assert.equal(await f.page.locator('#tabButtonVmware').getAttribute('aria-selected'),'true');await f.page.keyboard.press('ArrowLeft');assert.equal(await f.page.locator('#tabButtonMigrate').getAttribute('aria-selected'),'true');await f.page.keyboard.press('Home');assert.equal(await f.page.locator('#tabButtonSetup').getAttribute('aria-selected'),'true');
  });
  await check('Log search and severity filters retain all received text and safely render markup',async()=>{
   await f.page.locator('#tabButtonCollection').click();f.logs.push('[WARN] DNS slow','[ERROR] Access denied','[INFO] <img src=x onerror=alert(1)>');for(let i=0;i<100;i++)f.logs.push('[INFO] Row '+i);await f.page.waitForTimeout(1100);
   await f.page.locator('#logLevel').selectOption('issues');assert.equal(await f.page.locator('[data-log-line]:visible').count(),2);await f.page.locator('#logSearch').fill('denied');assert.equal(await f.page.locator('[data-log-line]:visible').count(),1);assert.equal(await f.page.locator('#logView img').count(),0);await f.page.locator('#logSearch').fill('');await f.page.locator('#logLevel').selectOption('all');assert.equal(await f.page.locator('[data-log-line]').count(),f.logs.length);
  });
  await check('Inspecting older log messages pauses live follow; resuming follows new output',async()=>{
   await f.page.locator('#followLog').check();await f.page.locator('#logView').evaluate(e=>e.scrollTop=0);await f.page.waitForTimeout(120);assert(!await f.page.locator('#followLog').isChecked());f.logs.push('[ERROR] Latest diagnostic');await f.page.waitForTimeout(1100);assert.equal(await f.page.locator('#logView').evaluate(e=>e.scrollTop),0);await f.page.locator('#followLog').check();assert(await f.page.locator('#logView').evaluate(e=>e.scrollHeight-e.scrollTop-e.clientHeight<5));
   await f.page.screenshot({path:out+'/collection-desktop.png'});
   await f.page.locator('#clearLogButton').click();assert.equal(await f.page.locator('[data-log-line]').count(),0);assert.equal(f.logs.length,105);
  });
  await check('Small windows retain visible Start and Save actions without horizontal page overflow',async()=>{
   await f.page.locator('#tabButtonSetup').click();await f.page.locator('#dismissNotice').click();
   for(const size of [{width:800,height:600},{width:390,height:844}]){await f.page.setViewportSize(size);await f.page.waitForTimeout(100);const box=await f.page.locator('#startButton').boundingBox();assert(box&&box.y>=0&&box.y+box.height<=size.height);assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await f.page.screenshot({path:out+'/setup-'+size.width+'.png'});}
  });
  await check('ACL timeout validates and persists without allowing unbounded exports',async()=>{
   await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('#exportPanel').evaluate(e=>e.open=true);await f.page.locator('#aclExportTimeout').fill('0');await f.page.locator('#saveChoicesButton').click();assert((await f.page.locator('#appNotice').textContent()).includes('30 to 3600'));
   await f.page.locator('#aclExportTimeout').fill('240');await f.page.locator('#saveChoicesButton').click();await f.page.waitForFunction(()=>document.querySelector('#preferencesStatus').textContent.includes('saved for'));assert.equal(f.saved.options.aclExportTimeoutSeconds,240);
   await f.page.reload();await f.page.waitForFunction(()=>document.querySelector('#preferencesStatus').textContent==='Saved choices restored');assert.equal(await f.page.locator('#aclExportTimeout').inputValue(),'240');
  });
  await check('Running state locks all collection settings, including event history',async()=>{
   await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('#presetInventory').click();f.state.running=true;f.state.status='running';await f.page.waitForTimeout(1100);assert(await f.page.locator('#eventDays').isDisabled());assert(await f.page.locator('#aclExportTimeout').isDisabled());assert(await f.page.locator('#saveChoicesButton').isDisabled());assert(await f.page.locator('#startButton').isDisabled());assert.deepEqual(f.dialogs,[]);
  });
  await check('ACL heartbeat shows escaped share, elapsed limit and run-scoped skip without blocking Cancel',async()=>{
   f.state.runId='synthetic-run';f.state.aclExport={runId:'synthetic-run',state:'Running',shareIndex:1,shareCount:3,shareName:'<img src=x onerror=alert(1)>',elapsedSeconds:15,timeoutSeconds:120};await f.page.waitForTimeout(1100);
   assert(await f.page.locator('#aclExportPanel').isVisible());assert((await f.page.locator('#aclExportDetails').textContent()).includes('15s / 120s'));assert.equal(await f.page.locator('#aclExportPanel img').count(),0);assert(await f.page.locator('#cancelButton').isEnabled());await f.page.screenshot({path:out+'/acl-progress.png'});
   await f.page.locator('#skipACLButton').click();await f.page.waitForTimeout(100);assert.deepEqual(f.skipRequests,[{expectedRunId:'synthetic-run'}]);assert(await f.page.locator('#skipACLButton').isDisabled());assert(await f.page.locator('#cancelButton').isEnabled());
   f.state.aclExport.state='Skipped';f.state.aclExport.incomplete=true;await f.page.waitForTimeout(1100);assert((await f.page.locator('#aclExportTitle').textContent()).includes('incomplete'));assert(!await f.page.locator('#skipACLButton').isVisible());
   f.state.aclExport.state='Idle';await f.page.waitForTimeout(1100);assert((await f.page.locator('#aclExportTitle').textContent()).includes('incomplete'));
   f.state.running=false;f.state.status='success';await f.page.waitForTimeout(1100);await f.page.locator('#tabButtonCollection').click();assert((await f.page.locator('#aclExportTitle').textContent()).includes('incomplete'));
   f.state.runId='new-run';f.state.running=true;f.state.status='running';await f.page.waitForTimeout(1100);assert(!await f.page.locator('#aclExportPanel').isVisible());assert.deepEqual(f.dialogs,[]);
  });
 }finally{await f.close();}
 await check('Grouped categories show per-group counts, toggle whole groups and hide empty groups while searching',async()=>{const f=await fixture(browser);try{
  assert.equal(await f.page.locator('.category-group').count(),5);assert.equal(await f.page.locator('input[data-inventory]').count(),34);
  const platform=f.page.locator('.category-group[data-group=platform]');assert.equal(await platform.locator('[data-group-count]').textContent(),'8 of 8 selected');
  await platform.locator('[data-group-toggle]').click();assert.equal(await platform.locator('input:checked').count(),0);assert.equal(await platform.locator('[data-group-toggle]').textContent(),'Select all');assert.equal(await f.page.locator('input[data-inventory]:checked').count(),26);
  const identity=f.page.locator('.category-group[data-group=identity]');await identity.locator('[data-group-toggle]').click();assert(!await f.page.locator('input[data-export][value=DHCP]').isChecked(),'clearing a group clears dependent exports');
  await identity.locator('[data-group-toggle]').click();assert.equal(await identity.locator('input:checked').count(),7);assert.equal(await f.page.locator('#profileName').textContent(),'Custom scope');
  assert(await f.page.locator('input[data-inventory][value=IIS]').evaluate(e=>e.closest('.category-check').classList.contains('has-export')),'untouched IIS export still marked');
  assert(!await f.page.locator('input[data-inventory][value=DHCP]').evaluate(e=>e.closest('.category-check').classList.contains('has-export')),'cleared DHCP export is unmarked');
  await f.page.locator('#scopeSearch').fill('RADIUS');assert.equal(await f.page.locator('#inventoryCategories .category-group:visible').count(),1);
  await f.page.locator('#scopeSearch').press('Escape');assert.equal(await f.page.locator('#scopeSearch').inputValue(),'');assert.equal(await f.page.locator('#inventoryCategories .category-group:visible').count(),5);
  f.state.running=true;f.state.status='running';await f.page.waitForTimeout(1100);assert(await platform.locator('[data-group-toggle]').isDisabled());
 }finally{await f.close();}});
 await check('Revert restores the last saved choices and only appears with unsaved changes',async()=>{const f=await fixture(browser);try{
  assert(!await f.page.locator('#revertChoicesButton').isVisible());
  await f.page.locator('#presetCore').click();assert(!await f.page.locator('#revertChoicesButton').isVisible(),'nothing saved yet');
  await f.page.keyboard.press('Control+s');await f.page.waitForFunction(()=>document.querySelector('#preferencesStatus').textContent.includes('saved for'));assert.equal(f.saveCalls,1);await f.page.waitForTimeout(1100);
  await f.page.locator('#presetMaximum').click();await f.page.locator('#outputPath').fill('D:\\Elsewhere');assert(await f.page.locator('#revertChoicesButton').isVisible());
  await f.page.locator('#revertChoicesButton').click();assert.equal(await f.page.locator('#profileName').textContent(),'Core server');assert.equal(await f.page.locator('#outputPath').inputValue(),'C:\\Reports');
  assert.equal(await f.page.locator('#preferencesStatus').textContent(),'Saved choices restored');assert(!await f.page.locator('#revertChoicesButton').isVisible());assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Log issue counts, next issue, clickable metrics and copy work on the received log',async()=>{const f=await fixture(browser);try{
  await f.context.grantPermissions(['clipboard-read','clipboard-write']);await f.page.locator('#tabButtonCollection').click();
  assert(await f.page.locator('#nextIssueButton').isDisabled());assert(await f.page.locator('#copyLogButton').isDisabled());
  f.logs.push('[INFO] Start','[WARN] DNS slow','[INFO] Middle','[ERROR] Access denied','[INFO] End');await f.page.waitForTimeout(1100);
  assert.equal(await f.page.locator('#logLevel option[value=issues]').textContent(),'Warnings and errors (2)');assert.equal(await f.page.locator('#logLevel option[value=error]').textContent(),'Errors only (1)');
  await f.page.locator('#nextIssueButton').click();assert.equal(await f.page.locator('#logView .log-focus').textContent(),'[WARN] DNS slow');assert(!await f.page.locator('#followLog').isChecked());
  await f.page.locator('#logView').focus();await f.page.keyboard.press('n');assert.equal(await f.page.locator('#logView .log-focus').textContent(),'[ERROR] Access denied');
  await f.page.locator('#nextIssueButton').click();assert.equal(await f.page.locator('#logView .log-focus').textContent(),'[WARN] DNS slow','wraps around');
  await f.page.locator('.metric-action[data-log-filter=error]').click();assert.equal(await f.page.locator('#logLevel').inputValue(),'error');assert.equal(await f.page.locator('[data-log-line]:visible').count(),1);
  await f.page.locator('#copyLogButton').click();await f.page.waitForFunction(()=>document.querySelector('#copyLogButton').textContent.startsWith('Copied'));assert.equal(await f.page.evaluate(()=>navigator.clipboard.readText()),'[ERROR] Access denied');
  await f.page.locator('#logLevel').selectOption('all');await f.page.locator('#copyLogButton').click();await f.page.waitForTimeout(100);assert.equal((await f.page.evaluate(()=>navigator.clipboard.readText())).split('\r\n').length,5);
 }finally{await f.close();}});
 await check('Window title reports progress and completion while keeping the native window-match name',async()=>{const f=await fixture(browser);try{
  assert.equal(await f.page.title(),'Server Inventory and Migration Manager');
  f.state.running=true;f.state.status='running';f.state.progress=42;await f.page.waitForTimeout(1100);assert.equal(await f.page.title(),'42% Collecting \u2014 Server Inventory and Migration Manager');
  f.state.running=false;f.state.status='success';f.state.packagePath='C:\\Reports\\SRV';f.state.zipPath='C:\\Reports\\SRV.zip';await f.page.waitForTimeout(1100);
  const title=(await f.page.title()).toLowerCase();assert(title.includes('server inventory and migration manager')&&title.startsWith('collection complete'));
  await f.page.locator('#tabButtonCollection').click();await f.page.locator('#openPackageButton').click();await f.page.locator('#showZipButton').click();await f.page.waitForTimeout(100);assert.deepEqual(f.opens,['folder','zip']);
 }finally{await f.close();}});
 await check('Keyboard shortcuts switch tabs, focus search, show help and start collection',async()=>{const f=await fixture(browser);try{
  await f.page.keyboard.press('Alt+2');assert.equal(await f.page.locator('#tabButtonCollection').getAttribute('aria-selected'),'true');
  await f.page.keyboard.press('/');assert.equal(await f.page.evaluate(()=>document.activeElement.id),'logSearch');
  await f.page.keyboard.press('Alt+1');await f.page.keyboard.press('Control+k');assert.equal(await f.page.evaluate(()=>document.activeElement.id),'scopeSearch');
  await f.page.locator('#serverName').click();await f.page.keyboard.press('Shift+?');assert(await f.page.locator('#shortcutsDialog').isVisible());await f.page.keyboard.press('Escape');assert(!await f.page.locator('#shortcutsDialog').isVisible());
  await f.page.locator('#shortcutsButton').click();assert(await f.page.locator('#shortcutsDialog').isVisible());await f.page.locator('#shortcutsClose').click();assert(!await f.page.locator('#shortcutsDialog').isVisible());
  await f.page.locator('#presetCore').click();await f.page.keyboard.press('Control+Enter');await f.page.waitForTimeout(1100);assert.equal(f.started.length,1);assert.equal(f.started[0].inventoryCategories.length,12);
  await f.page.keyboard.press('Alt+1');await f.page.keyboard.press('Control+Enter');await f.page.keyboard.press('Control+s');await f.page.waitForTimeout(300);assert.equal(f.started.length,1,'locked while running');assert.equal(f.saveCalls,0);assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Report search: Enter steps to next match, Shift+Enter and F3 navigate, Escape clears',async()=>{const f=await fixture(browser);try{
  Object.assign(f.state,{loadedReportAvailable:true,loadedReportSerial:1,loadedReportName:'SRV-DEMO-01',loadedReportPath:'Synthetic'});await f.page.waitForTimeout(1100);
  await f.page.locator('#tabButtonReport').click();await f.page.frameLocator('#reportFrame').locator('section').waitFor();
  const input=f.page.locator('#viewerSearchInput');await input.fill('alpha');await input.press('Enter');await f.page.waitForFunction(()=>document.querySelector('#viewerSearchStatus').textContent==='Match 1 of 3.');
  await input.press('Enter');assert.equal(await f.page.locator('#viewerSearchStatus').textContent(),'Match 2 of 3.');
  await input.press('Shift+Enter');assert.equal(await f.page.locator('#viewerSearchStatus').textContent(),'Match 1 of 3.');
  await f.page.keyboard.press('F3');assert.equal(await f.page.locator('#viewerSearchStatus').textContent(),'Match 2 of 3.');
  await input.fill('beta');await input.press('Enter');assert.equal(await f.page.locator('#viewerSearchStatus').textContent(),'Match 1 of 1.');
  await input.press('Escape');assert.equal(await input.inputValue(),'');assert.equal(await f.page.frameLocator('#reportFrame').locator('mark').count(),0);
 }finally{await f.close();}});
 await check('Accepted collection submits the selected scope once and keeps start/save locked',async()=>{const f=await fixture(browser);try{
  await f.page.locator('#presetCore').click();await f.page.locator('#startButton').click();await f.page.waitForTimeout(1100);assert.equal(f.started.length,1);assert.equal(f.started[0].inventoryCategories.length,12);assert.equal(f.started[0].exportCategories.length,0);assert(await f.page.locator('#tabCollection').isVisible());assert(await f.page.locator('#startButton').isDisabled());assert(await f.page.locator('#saveChoicesButton').isDisabled());assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Update bar stays hidden unless an update exists; routine status lives in Help > About and updates',async()=>{const f=await fixture(browser);try{
  assert(!await f.page.locator('#updateBanner').isVisible());assert(!await f.page.locator('#updateCheckButton').isVisible());
  for(const update of [{status:'idle',title:'Update status not yet verified',message:'Waiting.',canCheck:true},{status:'checking',title:'Checking for updates',message:'Installed',checking:true},{status:'offline',title:'Offline or connection unavailable',message:'No network.',canCheck:true},{status:'cooldown',title:'Update checks paused',message:'Retry later.',canCheck:true}]){f.state.update=update;await f.page.waitForTimeout(1100);assert(!await f.page.locator('#updateBanner').isVisible(),update.status);}
  await f.page.locator('#helpMenuButton').click();assert(await f.page.locator('#helpMenuList').isVisible());await f.page.keyboard.press('Escape');assert(!await f.page.locator('#helpMenuList').isVisible());
  await f.page.locator('#helpMenuButton').focus();await f.page.keyboard.press('ArrowDown');assert.equal(await f.page.evaluate(()=>document.activeElement.id),'helpCheckUpdates');await f.page.keyboard.press('ArrowDown');await f.page.keyboard.press('Enter');assert(await f.page.locator('#shortcutsDialog').isVisible());await f.page.locator('#shortcutsClose').click();
  await f.page.locator('#helpMenuButton').click();await f.page.locator('#helpAbout').click();assert((await f.page.locator('#aboutUpdateTitle').textContent()).includes('Update checks paused'));assert(await f.page.locator('#updateCheckButton').isVisible());assert(!await f.page.locator('#aboutShowUpdate').isVisible());
  f.state.update={status:'available',title:'Update available',message:'Installed 3.2.10. Latest 99.0.0.',latestVersion:'99.0.0',available:true,canCheck:true,canDownload:true,notes:['First note']};await f.page.waitForTimeout(1100);
  assert(await f.page.locator('#aboutShowUpdate').isVisible());await f.page.locator('#aboutShowUpdate').click();assert(!await f.page.locator('#aboutDialog').isVisible());assert(await f.page.locator('#updateBanner').isVisible());assert.equal(await f.page.evaluate(()=>document.activeElement.id),'updateDownloadButton');
  f.state.update={...f.state.update,status:'error',title:'Update download failed',message:'Hash mismatch.'};await f.page.waitForTimeout(1100);assert(await f.page.locator('#updateBanner').isVisible());assert((await f.page.locator('#updateBanner').getAttribute('class')).includes('error'));
  f.state.update={status:'current',title:'Up to date',message:'Installed.',canCheck:true};await f.page.waitForTimeout(1100);assert(!await f.page.locator('#updateBanner').isVisible());assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('A loaded report offers Copy path for its file location',async()=>{const f=await fixture(browser);try{
  assert(!await f.page.locator('#copyReportPathButton').isVisible());await f.context.grantPermissions(['clipboard-read','clipboard-write']);
  f.state.loadedReportAvailable=true;f.state.loadedReportSerial=1;f.state.loadedReportName='SRV-DEMO-01_20261103';f.state.loadedReportPath='C:\\Reports\\SRV-DEMO-01_20261103\\OPEN-FIRST - Server Inventory Report.html';
  await f.page.locator('#tabButtonReport').click();await f.page.locator('#copyReportPathButton').waitFor({state:'visible'});await f.page.locator('#copyReportPathButton').click();
  await f.page.waitForFunction(()=>document.querySelector('#copyReportPathButton').textContent==='Copied');assert.equal(await f.page.evaluate(()=>navigator.clipboard.readText()),f.state.loadedReportPath);assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 const latest='C:\\Reports\\SRV-DEMO-01_20261101_0914';
 const loadRestore=async f=>{f.state.restore.packages={lastCollection:latest,loadedReport:''};await f.page.keyboard.press('Alt+4');await f.page.waitForFunction(()=>!document.querySelector('#restoreUseLatest').disabled);await f.page.locator('#restoreUseLatest').click();await f.page.waitForFunction(()=>document.querySelectorAll('.restore-component').length===5);};
 const pick=async(f,id)=>{await f.page.locator('input[data-restore-component="'+id+'"]').check();};
 await check('Restore tab: Alt+4, arrow keys and the shortcuts list reach it; package buttons follow available packages',async()=>{const f=await fixture(browser);try{
  assert.equal(await f.page.locator('#restoreTabBadge').textContent(),'Ready');
  await f.page.keyboard.press('Alt+4');assert.equal(await f.page.locator('#tabButtonRestore').getAttribute('aria-selected'),'true');assert(await f.page.locator('#tabRestore').isVisible());
  assert.equal(await f.page.evaluate(()=>document.activeElement.id),'tabButtonRestore');await f.page.keyboard.press('ArrowRight');assert.equal(await f.page.locator('#tabButtonMigrate').getAttribute('aria-selected'),'true');await f.page.keyboard.press('ArrowLeft');assert.equal(await f.page.locator('#tabButtonRestore').getAttribute('aria-selected'),'true');
  assert(/Restore/.test(await f.page.locator('#shortcutsDialog').textContent()));
  assert(await f.page.locator('#restoreUseLatest').isDisabled());assert(await f.page.locator('#restoreUseLoaded').isDisabled());assert(await f.page.locator('#restoreReviewButton').isDisabled());assert(await f.page.locator('#restoreBrowse').isEnabled());
  f.state.restore.packages={lastCollection:'',loadedReport:'C:\\Reports\\Loaded'};await f.page.waitForTimeout(1100);assert(await f.page.locator('#restoreUseLatest').isDisabled());assert(await f.page.locator('#restoreUseLoaded').isEnabled());
  await f.page.locator('#restoreUseLoaded').click();await f.page.waitForTimeout(1100);assert.deepEqual(f.restoreCalls.scan,[{packagePath:'C:\\Reports\\Loaded'}]);assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Restore package choice posts a scan and shows source, target, integrity, blocked roles and escaped warnings',async()=>{const f=await fixture(browser);try{
  await loadRestore(f);assert.deepEqual(f.restoreCalls.scan,[{packagePath:latest}]);
  assert.equal(await f.page.locator('#restorePackagePath').textContent(),latest);assert.equal(await f.page.locator('#restoreSource').textContent(),'FS-OLD-07');assert.equal(await f.page.locator('#restoreTarget').textContent(),'SRV-DEMO-01');assert.equal(await f.page.locator('#restoreIntegrity').textContent(),'Verified');
  assert(/This package came from FS-OLD-07; you are restoring onto SRV-DEMO-01/.test(await f.page.locator('#restoreMismatch').textContent()));assert(await f.page.locator('#restoreMismatch').isVisible());assert(!await f.page.locator('#restoreAdminNotice').isVisible());
  const nps=f.page.locator('.restore-component[data-component="NPS"]');assert(await nps.locator('input[data-restore-component]').isDisabled());assert(/role is not installed/.test(await nps.locator('.restore-blocked').textContent()));
  assert.equal(await f.page.locator('.restore-component[data-component="DHCP"] .impact-chip').textContent(),'High impact');assert.equal(await f.page.locator('.restore-component[data-component="GPO"] .impact-chip').textContent(),'Affects the whole domain');assert.equal(await f.page.locator('.restore-component[data-component="Printers"] .impact-chip').count(),0);
  assert(/<b>not markup<\/b>/.test(await f.page.locator('.restore-component[data-component="GPO"] .restore-warnings').textContent()));assert.equal(await f.page.locator('.restore-warnings b').count(),0);
  assert.equal(await f.page.locator('.restore-component[data-component="DHCP"] details').count(),0,'single-item components have no item list');
  await f.page.setViewportSize({width:1440,height:900});await f.page.locator('input[data-restore-component="DHCP"]').check();await f.page.locator('input[data-restore-component="Registry"]').check();await f.page.locator('.restore-component[data-component="Registry"] summary').click();await f.page.screenshot({path:out+'/restore-components-1440x900.png'});await f.page.locator('input[data-restore-component="DHCP"]').uncheck();await f.page.locator('input[data-restore-component="Registry"]').uncheck();
  await f.page.locator('#restoreBrowse').click();await f.page.waitForTimeout(1100);assert.equal(f.restoreCalls.browse,1);assert.deepEqual(f.restoreCalls.scan[1],{packagePath:'D:\\Restore\\FS-OLD-07'});assert.equal(await f.page.locator('#restorePackagePath').textContent(),'D:\\Restore\\FS-OLD-07');
  assert(f.restoreCalls.tokens.every(t=>t==='workspace-test'));assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Restore item and component checkboxes stay in sync and survive state polls',async()=>{const f=await fixture(browser);try{
  await loadRestore(f);const card=f.page.locator('.restore-component[data-component="Registry"]'),parent=card.locator('input[data-restore-component]'),item=id=>card.locator('input[data-restore-item="'+id+'"]');
  await parent.check();await card.locator('summary').click();assert(await item('reg-lanman').isChecked());assert(!await item('reg-spooler').isChecked());assert(await item('reg-wsus').isDisabled());assert(/export file is missing/.test(await card.textContent()));
  assert.equal(await card.locator('summary').textContent(),'1 of 3 items selected');assert(await parent.evaluate(e=>e.indeterminate));
  await item('reg-spooler').check();assert.equal(await card.locator('summary').textContent(),'2 of 3 items selected');assert(!await parent.evaluate(e=>e.indeterminate));
  await f.page.waitForTimeout(1100);assert(await item('reg-spooler').isChecked(),'selection survives polls');assert(await card.locator('details').evaluate(e=>e.open),'item list stays open across polls');
  await item('reg-lanman').uncheck();await item('reg-spooler').uncheck();assert(!await parent.isChecked(),'unchecking every item unchecks the component');assert.equal(await f.page.locator('#restoreSelectionSummary').textContent(),'No items selected.');
  await item('reg-spooler').check();assert(await parent.isChecked());await parent.uncheck();assert(!await item('reg-spooler').isChecked());assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Review restore requires administrator rights, a selection and no running collection',async()=>{const f=await fixture(browser);try{
  await loadRestore(f);const review=f.page.locator('#restoreReviewButton');assert(await review.isDisabled());await pick(f,'Printers');assert(await review.isEnabled());
  assert.equal(await f.page.locator('#restoreSelectionSummary').textContent(),'1 item in 1 component selected.');
  f.state.restore.scan.isAdministrator=false;await f.page.waitForTimeout(1100);assert(await f.page.locator('#restoreAdminNotice').isVisible());assert(/Run the application as administrator to restore/.test(await f.page.locator('#restoreAdminNotice').textContent()));
  await pick(f,'Printers');assert(await review.isDisabled(),'not elevated');
  f.state.restore.scan.isAdministrator=true;await f.page.waitForTimeout(1100);await pick(f,'Printers');assert(await review.isEnabled());
  f.state.running=true;await f.page.waitForTimeout(1100);assert(await review.isDisabled(),'collection running');assert(await f.page.locator('#restoreBrowse').isDisabled());assert(await f.page.locator('input[data-restore-component="DHCP"]').isDisabled(),'inputs locked during collection');
  f.state.running=false;await f.page.waitForTimeout(1100);assert(await review.isEnabled());assert(await f.page.locator('input[data-restore-component="DHCP"]').isEnabled());assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Typed server name gates the restore, errors stay in the dialog and the exact start body is sent',async()=>{const f=await fixture(browser);try{
  await loadRestore(f);await pick(f,'DHCP');await pick(f,'GPO');await pick(f,'Registry');const card=f.page.locator('.restore-component[data-component="Registry"]');await card.locator('summary').click();await card.locator('input[data-restore-item="reg-spooler"]').check();
  await f.page.locator('#restoreReviewButton').click();const dialog=f.page.locator('#restoreDialog');assert(await dialog.isVisible());assert.equal(await f.page.evaluate(()=>document.activeElement.id),'restoreConfirmInput');
  const text=await dialog.textContent();assert(text.includes('Before each item, the current configuration is saved to a rollback folder so you can undo it.'));assert(text.includes('This package came from FS-OLD-07; you are restoring onto SRV-DEMO-01.'));assert(text.includes('Registry keys: LanmanServer parameters, Spooler settings'));
  assert.equal(await f.page.locator('#restoreDialogWarnings li').count(),2);assert.equal(await f.page.locator('#restoreConfirmLabel').textContent(),"Type this server's name to confirm: SRV-DEMO-01");
  await f.page.setViewportSize({width:1440,height:900});await f.page.screenshot({path:out+'/restore-review-1440x900.png'});
  const confirm=f.page.locator('#restoreConfirmButton');assert(await confirm.isDisabled());await f.page.locator('#restoreConfirmInput').fill('srv-demo');assert(await confirm.isDisabled());await f.page.keyboard.press('Enter');assert.equal(f.restoreCalls.start.length,0);
  await f.page.locator('#restoreConfirmInput').fill('FS-OLD-07');assert(await confirm.isDisabled(),'the source name is not accepted');
  await f.page.locator('#restoreConfirmInput').fill('srv-demo-01');assert(await confirm.isEnabled(),'case-insensitive match');
  f.rejectRestore('A collection is running. Wait for it to finish.');await confirm.click();await f.page.waitForFunction(()=>!document.querySelector('#restoreDialogError').classList.contains('hidden'));assert(await dialog.isVisible());assert.equal(await f.page.locator('#restoreDialogError').textContent(),'A collection is running. Wait for it to finish.');
  f.rejectRestore(null);await confirm.click();await f.page.waitForFunction(()=>!document.querySelector('#restoreDialog').open);
  assert.deepEqual(f.restoreCalls.start[1],{packagePath:latest,components:[{id:'DHCP',items:['DHCP-config']},{id:'GPO',items:['GPO-all']},{id:'Registry',items:['reg-lanman','reg-spooler']}],confirmComputerName:'srv-demo-01'});
  assert(await f.page.locator('#restoreProgress').isVisible());assert(await f.page.locator('#restoreCancelButton').isVisible());await f.page.waitForTimeout(1100);assert.equal(await f.page.locator('#restoreTabBadge').textContent(),'Running');
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isDisabled(),'Start collection is locked during a restore');assert(await f.page.locator('#presetCore').isDisabled());
  await f.page.keyboard.press('Control+Enter');await f.page.waitForTimeout(300);assert.equal(f.started.length,0);assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Restore progress shows per-item status and log, stops after the current item, and opens the rollback folder',async()=>{const f=await fixture(browser);try{
  await loadRestore(f);await pick(f,'DHCP');await f.page.locator('#restoreReviewButton').click();await f.page.locator('#restoreConfirmInput').fill('SRV-DEMO-01');await f.page.locator('#restoreConfirmButton').click();await f.page.waitForFunction(()=>!document.querySelector('#restoreDialog').open);
  const r=f.state.restore;r.items=[{component:'DHCP',id:'DHCP-config',title:'DHCP configuration',status:'succeeded',message:'12 scopes restored.'},{component:'GPO',id:'GPO-all',title:'All backed-up GPOs',status:'restoring',message:'Importing <script>alert(1)</script>'},{component:'Registry',id:'reg-lanman',title:'LanmanServer parameters',status:'pending',message:''}];
  r.logs=Array.from({length:80},(_,i)=>({index:i,text:(i===79?'[ERROR] ':'[INFO] ')+'Restore message '+i}));await f.page.waitForTimeout(1100);
  assert.deepEqual(await f.page.locator('.run-chip').allTextContents(),['Restored','Restoring','Waiting']);assert(/<script>alert\(1\)<\/script>/.test(await f.page.locator('#restoreRunItems').textContent()));
  assert(await f.page.locator('#restoreLog').evaluate(e=>e.scrollHeight-e.scrollTop-e.clientHeight<5),'log follows newest output');assert.equal(await f.page.locator('#restoreLog .log-line-error').count(),1);
  await f.page.setViewportSize({width:1440,height:900});await f.page.screenshot({path:out+'/restore-progress-1440x900.png'});
  await f.page.locator('#restoreCancelButton').click();await f.page.waitForTimeout(300);assert.equal(f.restoreCalls.cancel,1);assert(await f.page.locator('#restoreCancelButton').isDisabled());assert.equal(await f.page.locator('#restoreCancelButton').textContent(),'Stopping after current item\u2026');
  r.status='failed';r.message='1 item failed. The rollback folder holds the configuration saved before each item.';r.rollbackPath='C:\\Reports\\Rollback\\20261112';r.endedAtUnixMs=Date.now();r.items[1].status='failed';r.items[1].message='Access denied.';r.items[2].status='canceled';await f.page.waitForTimeout(1100);
  assert.equal(await f.page.locator('#restoreTabBadge').textContent(),'Failed');assert.deepEqual(await f.page.locator('.run-chip').allTextContents(),['Restored','Failed','Stopped']);assert(!await f.page.locator('#restoreCancelButton').isVisible());
  await f.page.locator('#restoreOpenRollback').click();await f.page.waitForTimeout(300);assert.deepEqual(f.opens,['restorerollback']);
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isEnabled(),'collection unlocks after the restore');await f.page.keyboard.press('Alt+4');
  await f.page.locator('#restoreAgainButton').click();assert(await f.page.locator('#restoreChooser').isVisible());assert(!await f.page.locator('#restoreProgress').isVisible());await f.page.waitForTimeout(1100);assert(await f.page.locator('#restoreChooser').isVisible(),'stays on the component list across polls');
  assert.equal(await f.page.locator('.restore-component').count(),5);assert(await f.page.locator('#restoreReviewButton').isVisible());assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 const shots=out;try{fs.mkdirSync(shots,{recursive:true});}catch(e){}
 const migrateConnect=async f=>{
  await f.page.keyboard.press('Alt+5');await f.page.locator('#migrateSource').fill('FS-OLD-07');await f.page.locator('#migrateConnect').click();
  await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Connecting');
  Object.assign(f.state.migrate,{running:false,action:'',status:'discovered',message:'Found 3 share(s) on FS-OLD-07.',source:'FS-OLD-07',discovery:migrateDiscoveryFixture(),workPath:'C:\\Users\\admin\\AppData\\Local\\Server Inventory Configuration Manager\\Migrations\\FS-OLD-07'});
  await f.page.waitForFunction(()=>document.querySelectorAll('.migrate-share').length===3);
 };
 const migrateChecked=f=>{const m=f.state.migrate;Object.assign(m,{running:false,action:'',status:'checked',message:'Everything is ready to copy.',prechecked:true,canCopy:true,space:[{drive:'D:',neededBytes:268435456000,freeBytes:400e9,ok:true}]});
  m.plan.forEach((p,i)=>{p.status='ready';p.precheck={ok:true,problems:[],warnings:i===0?['Permissions name local accounts of the old server, which do not exist here: FS-OLD-07\\Scanner.']:[],files:i===0?120345:5400,bytes:i===0?214748364800:53687091200};});};
 const migrateCalls=(f,action)=>f.migrateCalls.filter(c=>c.action===action);
 await check('Migrate tab: Alt+5 reaches it; Connect posts the server name; shares get editable defaults and system shares start unticked',async()=>{const f=await fixture(browser);try{
  assert.equal(await f.page.locator('#migrateTabBadge').textContent(),'Ready');
  await f.page.keyboard.press('Alt+5');assert.equal(await f.page.locator('#tabButtonMigrate').getAttribute('aria-selected'),'true');assert(await f.page.locator('#tabMigrate').isVisible());assert.equal(await f.page.evaluate(()=>document.activeElement.id),'tabButtonMigrate');
  assert((await f.page.locator('#tabButtonMigrate').textContent()).startsWith('File shares'));assert(/Restore, File shares, VMware or Disk converter/.test(await f.page.locator('#shortcutsDialog').textContent()));
  assert(await f.page.locator('#migrateConnect').isDisabled(),'Connect needs a server name');assert(await f.page.locator('#migrateCheck').isDisabled());assert(await f.page.locator('#migrateCopy').isDisabled());assert(await f.page.locator('#migrateCutover').isDisabled());
  assert.equal(await f.page.locator('#migrateSteps li.current').getAttribute('data-step'),'1');
  await migrateConnect(f);assert.deepEqual(migrateCalls(f,'discover').map(c=>c.body),[{source:'FS-OLD-07'}]);
  assert.equal(await f.page.locator('#migrateTabBadge').textContent(),'Connected');assert.equal(await f.page.locator('#migrateFactSource').textContent(),'FS-OLD-07 (fs-old-07.example.com)');assert.equal(await f.page.locator('#migrateFactShares').textContent(),'3');
  assert(await f.page.locator('input[data-migrate-share="Finance"]').isChecked());assert(await f.page.locator('input[data-migrate-share="Projects"]').isChecked());assert(!await f.page.locator('input[data-migrate-share="NETLOGON"]').isChecked(),'system share unticked by default');
  assert.equal(await f.page.locator('[data-migrate-row="NETLOGON"] .impact-chip').textContent(),'System share');assert(!await f.page.locator('[data-migrate-row="NETLOGON"] [data-migrate-dest-block]').isVisible(),'unticked rows hide destination fields');
  assert.equal(await f.page.locator('input[data-migrate-dest="Finance"]').inputValue(),'D:\\Shares\\Finance','largest non-system volume');assert.equal(await f.page.locator('input[data-migrate-destname="Finance"]').inputValue(),'Finance');
  assert(/<b>not markup<\/b>/.test(await f.page.locator('[data-migrate-row="Finance"] .migrate-notes').textContent()));assert.equal(await f.page.locator('.migrate-notes b').count(),0);
  await f.page.locator('input[data-migrate-dest="Projects"]').fill('F:\\Team\\Projects');await f.page.locator('input[data-migrate-destname="Projects"]').fill('TeamProjects');await f.page.locator('input[data-migrate-share="NETLOGON"]').check();
  await f.page.waitForTimeout(1100);assert.equal(await f.page.locator('input[data-migrate-dest="Projects"]').inputValue(),'F:\\Team\\Projects','edits survive polls');assert(await f.page.locator('input[data-migrate-share="NETLOGON"]').isChecked());
  assert.equal(await f.page.locator('#migrateSelectionSummary').textContent(),'3 shares selected.');assert.equal(await f.page.locator('#migrateSteps li.current').getAttribute('data-step'),'2');
  f.state.isAdministrator=false;await f.page.waitForTimeout(1100);assert(await f.page.locator('#migrateAdminNotice').isVisible());assert(await f.page.locator('#migrateCheck').isDisabled(),'not elevated');
  f.state.isAdministrator=true;await f.page.waitForTimeout(1100);assert(await f.page.locator('#migrateCheck').isEnabled());assert(f.migrateCalls.every(c=>c.token==='workspace-test'));assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Check plan posts the exact plan then the check; Copy waits for a passing check and an unchanged plan',async()=>{const f=await fixture(browser);try{
  await migrateConnect(f);await f.page.locator('input[data-migrate-dest="Projects"]').fill('D:\\Shares\\Projects2');
  f.rejectMigrate('plan','The new folder for Projects overlaps another share\'s new folder.');await f.page.locator('#migrateCheck').click();
  await f.page.waitForFunction(()=>document.querySelector('#migrateCheckStatus').classList.contains('error'));assert.equal(await f.page.locator('#migrateCheckStatus').textContent(),'The new folder for Projects overlaps another share\'s new folder.');assert.equal(migrateCalls(f,'precheck').length,0,'no check after a rejected plan');
  f.rejectMigrate('plan',null);await f.page.locator('#migrateCheck').click();await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Checking');
  assert.deepEqual(migrateCalls(f,'plan')[1].body,{shares:[{name:'Finance',destName:'Finance',destPath:'D:\\Shares\\Finance',allowOverwrite:false},{name:'Projects',destName:'Projects',destPath:'D:\\Shares\\Projects2',allowOverwrite:false}]});assert.deepEqual(migrateCalls(f,'precheck').map(c=>c.body),[{}]);
  assert(await f.page.locator('#migrateCheck').isDisabled(),'inputs lock while checking');assert(await f.page.locator('input[data-migrate-dest="Finance"]').isDisabled());assert(await f.page.locator('input[data-migrate-overwrite="Finance"]').isDisabled());assert(await f.page.locator('#migrateStop').isVisible());
  assert(await f.page.locator('#migrateCopy').isDisabled());migrateChecked(f);await f.page.waitForTimeout(1100);
  assert(await f.page.locator('#migrateCopy').isEnabled());assert.equal(await f.page.locator('#migrateCopy').textContent(),'Copy now');assert(await f.page.locator('#migrateCutover').isDisabled(),'cut over waits for a copy');
  assert.deepEqual(await f.page.locator('[data-migrate-check] .run-chip').allTextContents(),['Ready','Ready']);assert((await f.page.locator('[data-migrate-check="Finance"]').textContent()).includes('120,345 files, 200 GB'));
  {const t=await f.page.locator('#migrateSpace').textContent();assert(t.includes('Drive D: needs about 250 GB, 373 GB free.'),t);}assert.equal(await f.page.locator('#migrateSteps li.current').getAttribute('data-step'),'3');
  await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('#migrateStepChoose').scrollIntoViewIfNeeded();await f.page.screenshot({path:shots+'/migrate-step2-1440x1000.png'});
  await f.page.locator('input[data-migrate-destname="Finance"]').fill('Finance2');assert(await f.page.locator('#migrateCopy').isDisabled(),'changing the plan needs a new check');assert.equal(await f.page.locator('#migrateCheck').textContent(),'Check plan');
  await f.page.locator('input[data-migrate-destname="Finance"]').fill('Finance');assert(await f.page.locator('#migrateCopy').isEnabled());assert.equal(await f.page.locator('#migrateCheck').textContent(),'Check plan again');
  f.state.migrate.plan[1].precheck={ok:false,problems:['The new folder D:\\Shares\\Projects2 already contains files.'],warnings:[],files:0,bytes:0};f.state.migrate.canCopy=false;await f.page.waitForTimeout(1100);
  assert(await f.page.locator('#migrateCopy').isDisabled());assert.equal(await f.page.locator('[data-migrate-check="Projects"] .run-chip').textContent(),'Needs attention');assert(/already contains files/.test(await f.page.locator('[data-migrate-row="Projects"] .problem').textContent()));assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Migrate explicitly opts into existing-folder and share overwrite, and the mirror requires typed confirmation',async()=>{const f=await fixture(browser);try{
  await migrateConnect(f);
  assert(!await f.page.locator('input[data-migrate-overwrite="Finance"]').isChecked(),'overwrite must be off by default');
  await f.page.locator('input[data-migrate-overwrite="Finance"]').check();
  assert(await f.page.locator('[data-migrate-overwrite-note="Finance"]').isVisible());
  assert(/permanently deleted|deleted to match/.test(await f.page.locator('[data-migrate-overwrite-note="Finance"]').textContent()));
  await f.page.locator('input[data-migrate-share="NETLOGON"]').check();
  await f.page.locator('input[data-migrate-overwrite="NETLOGON"]').check();
  assert(/Active Directory/.test(await f.page.locator('[data-migrate-overwrite-note="NETLOGON"]').textContent()),'system shares show a distinct warning');
  await f.page.locator('input[data-migrate-share="NETLOGON"]').uncheck();
  await f.page.locator('#migrateCheck').click();
  await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Checking');
  assert.deepEqual(migrateCalls(f,'plan')[0].body.shares.map(p=>[p.name,p.allowOverwrite]),[['Finance',true],['Projects',false]]);
  migrateChecked(f);await f.page.waitForTimeout(1100);
  assert(await f.page.locator('#migrateCopy').isEnabled());assert.equal(await f.page.locator('#migrateCheck').textContent(),'Check plan again');
  await f.page.locator('#migrateCopy').click();
  assert(await f.page.locator('#migrateDialog').isVisible(),'overwrite copy opens explicit confirmation');
  assert.equal(migrateCalls(f,'sync').length,0,'copy did not start before confirmation');
  assert(/permanently deleted/.test(await f.page.locator('#migrateDialogAlert').textContent()));
  assert((await f.page.locator('#migrateDialogText').textContent()).includes('Finance - D:\\Shares\\Finance'));
  await f.page.locator('#migrateConfirmInput').fill('FS-OLD-07');assert(await f.page.locator('#migrateConfirmButton').isDisabled());
  await f.page.locator('#migrateConfirmInput').fill('srv-demo-01');await f.page.locator('#migrateConfirmButton').click();
  await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Copying');
  assert.deepEqual(migrateCalls(f,'sync')[0].body,{confirmComputerName:'srv-demo-01'});
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Copy shows per-share progress, Stop sends cancel, other work locks, and cut over needs this server\'s typed name',async()=>{const f=await fixture(browser);try{
  await migrateConnect(f);await f.page.locator('#migrateCheck').click();await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Checking');migrateChecked(f);await f.page.waitForTimeout(1100);
  await f.page.locator('#migrateCopy').click();await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Copying');assert.equal(migrateCalls(f,'sync').length,1);assert.deepEqual(migrateCalls(f,'sync')[0].body,{});
  const m=f.state.migrate;m.plan[0].status='copying';m.plan[0].percent=42;m.plan[0].processed=50544;m.plan[0].expected=120345;m.plan[0].message='Copying changes.';m.plan[1].status='planned';
  m.logs=Array.from({length:60},(_,i)=>({index:i,text:(i===59?'2026-11-12 10:00:00 [ERROR] ':'2026-11-12 10:00:00 [INFO] ')+'Migration message '+i}));await f.page.waitForTimeout(1100);
  assert.equal(await f.page.locator('[data-migrate-run="Finance"] .migrate-bar').getAttribute('aria-valuenow'),'42');assert((await f.page.locator('[data-migrate-run="Finance"]').textContent()).includes('50,544 of about 120,345 files processed'));
  assert.deepEqual(await f.page.locator('#migrateRun .run-chip').allTextContents(),['Copying','Not copied']);
  await f.page.locator('#migrateLogBlock summary').click();assert.equal(await f.page.locator('#migrateLog .log-line-error').count(),1);assert(await f.page.locator('#migrateLog').evaluate(e=>e.scrollHeight-e.scrollTop-e.clientHeight<5),'log follows newest output');
  await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('#migrateStepCopy').scrollIntoViewIfNeeded();await f.page.screenshot({path:shots+'/migrate-step3-1440x1000.png'});
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isDisabled(),'Start collection is locked during a migration step');await f.page.keyboard.press('Control+Enter');await f.page.waitForTimeout(300);assert.equal(f.started.length,0);
  await f.page.keyboard.press('Alt+5');await f.page.locator('#migrateStop').click();await f.page.waitForTimeout(300);assert.equal(migrateCalls(f,'cancel').length,1);assert(await f.page.locator('#migrateStop').isDisabled());assert.equal(await f.page.locator('#migrateStop').textContent(),'Stopping\u2026');
  Object.assign(m,{running:false,action:'',status:'copied',message:'Copy finished.',cancelRequested:false,canCutover:true});m.plan.forEach(p=>{p.status='succeeded';p.previouslySynced=true;p.lastSyncUnixMs=Date.now();p.message='1,204 file(s) copied, 3 removed that no longer exist on the old server.';});await f.page.waitForTimeout(1100);
  assert(!await f.page.locator('#migrateStop').isVisible());assert.equal(await f.page.locator('#migrateCopy').textContent(),'Copy again');assert(await f.page.locator('#migrateCutover').isEnabled());assert.equal(await f.page.locator('#migrateSteps li.current').getAttribute('data-step'),'4');
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isEnabled(),'collection unlocks after the step');await f.page.keyboard.press('Alt+5');
  await f.page.locator('#migrateCutover').click();const dialog=f.page.locator('#migrateDialog');assert(await dialog.isVisible());assert.equal(await f.page.evaluate(()=>document.activeElement.id),'migrateConfirmInput');
  const text=await dialog.textContent();assert(text.includes('Ask users to stop using the old shares now.'));assert(text.includes('Finance \u2192 \\\\SRV-DEMO-01\\Finance (D:\\Shares\\Finance)'));assert.equal(await f.page.locator('#migrateConfirmLabel').textContent(),"Type this server's name to confirm: SRV-DEMO-01");
  const confirm=f.page.locator('#migrateConfirmButton');assert(await confirm.isDisabled());await f.page.locator('#migrateConfirmInput').fill('FS-OLD-07');assert(await confirm.isDisabled(),'the old server name is not accepted');
  await f.page.locator('#migrateConfirmInput').fill('srv-demo-01');assert(await confirm.isEnabled(),'case-insensitive match');
  f.rejectMigrate('cutover','A collection is running. Wait for it to finish first.');await confirm.click();await f.page.waitForFunction(()=>!document.querySelector('#migrateDialogError').classList.contains('hidden'));assert(await dialog.isVisible());assert.equal(await f.page.locator('#migrateDialogError').textContent(),'A collection is running. Wait for it to finish first.');
  f.rejectMigrate('cutover',null);await confirm.click();await f.page.waitForFunction(()=>!document.querySelector('#migrateDialog').open);assert.deepEqual(migrateCalls(f,'cutover').map(c=>c.body),[{confirmComputerName:'srv-demo-01'},{confirmComputerName:'srv-demo-01'}]);
  await f.page.waitForTimeout(1100);assert.equal(await f.page.locator('#migrateTabBadge').textContent(),'Cutting over');assert(await f.page.locator('#migrateCutover').isDisabled());assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('After cutover: new share paths, read-only with typed old name and Undo, DFS links, the name check and the add-name dialog',async()=>{const f=await fixture(browser);try{
  await migrateConnect(f);await f.page.locator('#migrateCheck').click();await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Checking');migrateChecked(f);
  const m=f.state.migrate;assert(!await f.page.locator('#migrateAfter').isVisible(),'hidden before cutover');
  Object.assign(m,{status:'cutover',message:'Cutover complete.',canCutover:true});m.plan[0]=Object.assign(m.plan[0],{status:'done',previouslySynced:true,createdShare:true,verified:true,message:'120,345 file(s) match.'});m.plan[1]=Object.assign(m.plan[1],{status:'attention',previouslySynced:true,createdShare:true,verified:false,message:'2 file(s) differ.'});
  await f.page.waitForTimeout(1100);assert(await f.page.locator('#migrateAfter').isVisible());assert.equal(await f.page.locator('#migrateTabBadge').textContent(),'Done');
  assert.deepEqual(await f.page.locator('#migrateCutoverRun .run-chip').allTextContents(),['Live and verified','Check again']);assert.equal(await f.page.locator('[data-migrate-cut="Finance"] .migrate-run-name').textContent(),'\\\\SRV-DEMO-01\\Finance');
  await f.page.locator('#migrateReadOnly').click();assert(await f.page.locator('#migrateDialog').isVisible());assert.equal(await f.page.locator('#migrateConfirmLabel').textContent(),"Type the old server's name to confirm: FS-OLD-07");
  await f.page.locator('#migrateConfirmInput').fill('SRV-DEMO-01');assert(await f.page.locator('#migrateConfirmButton').isDisabled());await f.page.locator('#migrateConfirmInput').fill('fs-old-07');await f.page.locator('#migrateConfirmButton').click();
  await f.page.waitForFunction(()=>!document.querySelector('#migrateDialog').open);assert.deepEqual(migrateCalls(f,'source-readonly').map(c=>c.body),[{confirmSourceName:'fs-old-07'}]);
  Object.assign(m,{running:false,action:'',status:'readonly',sourceReadOnly:true});await f.page.waitForTimeout(1100);assert(!await f.page.locator('#migrateReadOnly').isVisible());assert(await f.page.locator('#migrateReadOnlyUndo').isVisible());
  await f.page.locator('#migrateReadOnlyUndo').click();await f.page.waitForTimeout(300);assert.deepEqual(migrateCalls(f,'source-restore').map(c=>c.body),[{}]);
  Object.assign(m,{running:false,action:'',status:'restoredsource',sourceReadOnly:false});await f.page.waitForTimeout(1100);
  await f.page.locator('#migrateDfs').click();await f.page.waitForTimeout(300);assert.equal(migrateCalls(f,'dfs').length,1);
  Object.assign(m,{running:false,action:'',status:'dfschecked',dfs:{available:true,message:'1 DFS link(s) point at the old server.',targets:[{namespacePath:'\\\\example.com\\Files\\Finance',target:'\\\\FS-OLD-07\\Finance',suggested:'\\\\SRV-DEMO-01\\Finance'}]}});await f.page.waitForTimeout(1100);
  assert.deepEqual(await f.page.locator('#migrateDfsResult td').allTextContents(),['\\\\example.com\\Files\\Finance','\\\\FS-OLD-07\\Finance','\\\\SRV-DEMO-01\\Finance']);
  await f.page.locator('#migrateAliasCheck').click();await f.page.waitForTimeout(300);assert.equal(migrateCalls(f,'alias-check').length,1);
  Object.assign(m,{running:false,action:'',status:'aliaschecked',alias:{possible:false,reasons:['The old server FS-OLD-07 still answers on the network.'],oldName:'FS-OLD-07',oldFqdn:'fs-old-07.example.com',thisFqdn:'srv-demo-01.example.com'}});await f.page.waitForTimeout(1100);
  assert(/still answers/.test(await f.page.locator('#migrateAliasResult').textContent()));assert(!await f.page.locator('#migrateAlias').isVisible(),'Add name waits for a passing check');
  m.alias.possible=true;m.alias.reasons=[];await f.page.waitForTimeout(1100);assert(await f.page.locator('#migrateAlias').isVisible());
  await f.page.locator('#migrateAlias').click();const text=await f.page.locator('#migrateDialog').textContent();assert(text.includes('This changes DNS and Active Directory. It needs Domain Admin rights'));assert(text.includes('restarted'));
  await f.page.locator('#migrateConfirmInput').fill('FS-OLD-07');await f.page.locator('#migrateConfirmButton').click();await f.page.waitForFunction(()=>!document.querySelector('#migrateDialog').open);assert.deepEqual(migrateCalls(f,'alias').map(c=>c.body),[{confirmOldName:'FS-OLD-07'}]);
  Object.assign(m,{running:false,action:'',status:'aliasadded',aliasAdded:'fs-old-07.example.com'});await f.page.waitForTimeout(1100);assert(/Restart this server/.test(await f.page.locator('#migrateAliasResult').textContent()));assert(!await f.page.locator('#migrateAlias').isVisible());
  await f.page.locator('#migrateOpenLogs').click();await f.page.waitForTimeout(300);assert.deepEqual(f.opens,['migrationlogs']);
  await f.page.locator('#migrateReset').click();await f.page.waitForTimeout(1100);assert.equal(migrateCalls(f,'reset').length,1);assert.equal(await f.page.locator('.migrate-share').count(),0);assert.equal(await f.page.locator('#migrateSource').inputValue(),'');
  assert(f.migrateCalls.every(c=>c.token==='workspace-test'));assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Migrate Browse and Choose a folder for all pick the new folders on this server',async()=>{const f=await fixture(browser);try{
  await migrateConnect(f);
  assert(await f.page.locator('#migrateParent').isVisible()&&!await f.page.locator('#migrateParent').isDisabled());
  await f.page.locator('button[data-migrate-browse="Finance"]').click();await f.page.waitForFunction(()=>document.querySelector('input[data-migrate-dest="Finance"]').value==='F:\\Data\\Finance-New');
  assert.deepEqual(migrateCalls(f,'browse').map(c=>c.body),[{share:'Finance'}]);
  await f.page.locator('input[data-migrate-destname="Projects"]').fill('Team Projects.');const netlogon=await f.page.locator('input[data-migrate-dest="NETLOGON"]').inputValue();
  await f.page.locator('#migrateParent').click();await f.page.waitForFunction(()=>document.querySelector('input[data-migrate-dest="Finance"]').value==='E:\\Finance');
  assert.equal(await f.page.locator('input[data-migrate-dest="Projects"]').inputValue(),'E:\\Team Projects','folder named after the new share name, trailing dot removed');
  assert.equal(await f.page.locator('input[data-migrate-dest="NETLOGON"]').inputValue(),netlogon,'unticked shares keep their folder');
  assert.equal(await f.page.locator('#migrateCheckStatus').textContent(),'2 shares will go in E:\\. Check the plan again.');
  await f.page.locator('#migrateCheck').click();await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Checking');
  assert.deepEqual(migrateCalls(f,'plan')[0].body.shares.map(s=>s.destPath),['E:\\Finance','E:\\Team Projects']);
  assert(await f.page.locator('button[data-migrate-browse="Finance"]').isDisabled()&&await f.page.locator('#migrateParent').isDisabled(),'folder pickers lock while a step runs');
  f.rejectMigrate('browse','That folder is inside the Windows folder. Choose a folder for shared data, such as D:\\Shares.');
  Object.assign(f.state.migrate,{running:false,action:'',status:'checked'});await f.page.waitForTimeout(1100);
  await f.page.locator('button[data-migrate-browse="Finance"]').click();await f.page.waitForFunction(()=>/inside the Windows folder/.test(document.querySelector('#migrateCheckStatus').textContent));
  assert.equal(await f.page.locator('input[data-migrate-dest="Finance"]').inputValue(),'E:\\Finance','a refused folder changes nothing');assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Migrate Connect as offers another account, sends it only to Connect and asks for it after access is denied',async()=>{const f=await fixture(browser);try{
  await f.page.keyboard.press('Alt+5');await f.page.locator('#migrateSource').fill('FS-OLD-07');
  assert(await f.page.locator('#migrateAccountCurrent').isChecked());assert(!await f.page.locator('#migrateAccountFields').isVisible(),'account fields start hidden');
  await f.page.locator('#migrateConnect').click();await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Connecting');
  assert.deepEqual(migrateCalls(f,'discover').map(c=>c.body),[{source:'FS-OLD-07'}],'this Windows account sends no account');
  Object.assign(f.state.migrate,{running:false,action:'',status:'discoverfailed',message:'Windows refused access on FS-OLD-07 for this Windows account.',accessDenied:true,endedAtUnixMs:Date.now()});
  await f.page.waitForFunction(()=>document.querySelector('#migrateAccountOther').checked&&!document.querySelector('#migrateAccountFields').classList.contains('hidden'));
  assert.equal(await f.page.evaluate(()=>document.activeElement.id),'migrateUser','access denied focuses the user name');
  assert.equal(await f.page.locator('#migratePassword').getAttribute('type'),'password');assert.equal(await f.page.locator('#migratePassword').getAttribute('autocomplete'),'new-password');
  await f.page.locator('#migrateConnect').click();assert(/Type the user name of an administrator on FS-OLD-07/.test(await f.page.locator('#migrateConnectStatus').textContent()));
  await f.page.locator('#migrateUser').fill('EXAMPLE\\admin');await f.page.locator('#migrateConnect').click();assert.equal(await f.page.locator('#migrateConnectStatus').textContent(),'Type the password for EXAMPLE\\admin.');
  assert.equal(await f.page.evaluate(()=>document.activeElement.id),'migratePassword');assert.equal(migrateCalls(f,'discover').length,1,'nothing sent without a password');
  await f.page.locator('#migratePassword').fill('Synthetic-Pa55');await f.page.locator('#migratePassword').press('Enter');await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Connecting');
  assert.deepEqual(migrateCalls(f,'discover')[1].body,{source:'FS-OLD-07',user:'EXAMPLE\\admin',password:'Synthetic-Pa55'});assert.equal(await f.page.locator('#migratePassword').inputValue(),'','the password leaves the page after Connect');
  Object.assign(f.state.migrate,{running:false,action:'',status:'discovered',message:'Found 3 share(s) on FS-OLD-07.',accessDenied:false,discovery:migrateDiscoveryFixture()});
  await f.page.waitForFunction(()=>document.querySelectorAll('.migrate-share').length===3);
  assert.equal(await f.page.locator('#migratePassword').getAttribute('placeholder'),'Password (kept for this session)');
  await f.page.locator('#migrateConnect').click();await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Connecting');
  assert.deepEqual(migrateCalls(f,'discover')[2].body,{source:'FS-OLD-07',user:'EXAMPLE\\admin',password:''},'reconnecting reuses the held password');
  assert(await f.page.locator('#migrateUser').isDisabled()&&await f.page.locator('#migrateAccountCurrent').isDisabled(),'account inputs lock while busy');
  Object.assign(f.state.migrate,{running:false,action:'',status:'discovered'});await f.page.waitForTimeout(1100);
  await f.page.locator('#migrateReset').click();await f.page.waitForTimeout(1100);
  assert(await f.page.locator('#migrateAccountCurrent').isChecked());assert.equal(await f.page.locator('#migrateUser').inputValue(),'');assert(!await f.page.locator('#migrateAccountFields').isVisible());
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 const vmwareConnected=async f=>{
  await f.page.keyboard.press('Alt+6');await f.page.locator('#vmServer').fill('vc01.example.com');await f.page.locator('#vmUser').fill('readonly@vsphere.local');await f.page.locator('#vmPassword').fill('Synthetic-Pa55');await f.page.locator('#vmConnect').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Reading');
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();f.vm.sizing=vmwareSizingFixture(false);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',message:'Read 4 VM(s) on 3 host(s) from vc01.example.com.',summary:vmwareSummaryFixture(),inventorySerial:1,outputFolder:'C:\\Reports\\VMware_vc01',reportPath:'C:\\Reports\\VMware_vc01\\VMware-Readiness-Report.html'});
  await f.page.waitForFunction(()=>document.querySelectorAll('#vmTableBody tr.vm-row').length===4);
 };
 const vmwareCalls=(f,action)=>f.vmwareCalls.filter(c=>c.action===action&&c.method==='POST');
 await check('VMware tab: Read Hyper-V host reads this computer or a remote host, sends the optional account once, and opens a VM disk in the Disk converter',async()=>{const f=await fixture(browser);try{
  await f.page.keyboard.press('Alt+6');
  await f.page.locator('#vmHvUser').fill('EXAMPLE\\admin');await f.page.locator('#vmHvRead').click();
  assert.equal(await f.page.locator('#vmHvStatus').textContent(),'Type the password for EXAMPLE\\admin, or clear the account to use your own.');assert.equal(vmwareCalls(f,'hyperv').length,0);
  await f.page.locator('#vmHvUser').fill('');await f.page.locator('#vmHvRead').click();await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Reading');
  assert.deepEqual(vmwareCalls(f,'hyperv').map(c=>c.body),[{host:'',user:'',password:''}],'an empty host reads this computer as the current user');
  assert(await f.page.locator('#vmStop').isVisible());assert(await f.page.locator('#vmHvHost').isDisabled());
  Object.assign(f.state.vmware,{running:false,action:'',status:'idle'});await f.page.waitForFunction(()=>!document.querySelector('#vmHvHost').disabled);
  await f.page.locator('#vmHvHost').fill('hv01.example.com');await f.page.locator('#vmHvUser').fill('EXAMPLE\\admin');await f.page.locator('#vmHvPassword').fill('Synthetic-Pa55');await f.page.locator('#vmHvPassword').press('Enter');
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Reading');
  assert.deepEqual(vmwareCalls(f,'hyperv').at(-1).body,{host:'hv01.example.com',user:'EXAMPLE\\admin',password:'Synthetic-Pa55'});assert.equal(await f.page.locator('#vmHvPassword').inputValue(),'','the password leaves the page');
  const inventory=vmwareInventoryFixture();Object.assign(inventory,{server:'hv01.example.com',serverType:'Hyper-V host',product:'Microsoft Windows Server 2022 Datacenter'});
  const app=inventory.vms.find(v=>v.id==='vm-app01');Object.assign(app,{source:'hyperv',generation:2,integrationServices:'ok',integrationVersion:'10.0.20348',dynamicMemory:true,toolsStatus:'',toolsVersion:'',toolsRunning:false});
  app.disks=[{label:'Hard drive on SCSI controller 0 location 0',controller:'SCSI',capacityBytes:136365211648,datastore:'D:',file:'D:\\VMs\\APP01\\APP01.vhdx',localPath:'\\\\hv01.example.com\\D$\\VMs\\APP01\\APP01.vhdx',provisioning:'dynamic',mode:'persistent',sharing:'sharingNone'}];
  f.vm.inventory=inventory;f.vm.plan=Object.assign(vmwarePlanFixture(),{server:'hv01.example.com'});f.vm.sizing=vmwareSizingFixture(false);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',server:'hv01.example.com',account:'EXAMPLE\\admin',message:'Read 4 VM(s) from Hyper-V host hv01.example.com.',summary:Object.assign(vmwareSummaryFixture(),{server:'hv01.example.com',serverType:'Hyper-V host'}),inventorySerial:1});
  await f.page.waitForFunction(()=>document.querySelectorAll('#vmTableBody tr.vm-row').length===4);
  assert.equal(await f.page.locator('#vmFactServer').textContent(),'hv01.example.com (Hyper-V host)');
  assert.equal(await f.page.locator('#vmServer').inputValue(),'','a Hyper-V host is not offered as a vCenter server');assert.equal(await f.page.locator('#vmUser').inputValue(),'');
  await f.page.locator('tr[data-vm-id="vm-app01"]').click();const detail=await f.page.locator('#vmDetail').textContent();
  assert(detail.includes('Generation 2')&&detail.includes('responding, version 10.0.20348'),detail);
  await f.page.locator('#vmDetail button.vm-link',{hasText:'Convert'}).click();
  assert.equal(await f.page.locator('#tabButtonConvert').getAttribute('aria-selected'),'true');assert.equal(await f.page.locator('#cvSource').inputValue(),'\\\\hv01.example.com\\D$\\VMs\\APP01\\APP01.vhdx');
  await f.page.waitForFunction(()=>!document.querySelector('#cvInfo').classList.contains('hidden'));assert.equal(f.convertCalls.filter(c=>c.action==='inspect').at(-1).body.path,'\\\\hv01.example.com\\D$\\VMs\\APP01\\APP01.vhdx');
  assert(f.vmwareCalls.every(c=>c.token==='workspace-test'));assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware tab: Alt+6 reaches it; Read inventory sends the password once; an untrusted certificate needs an explicit trust; readiness filters and details',async()=>{const f=await fixture(browser);try{
  assert.equal(await f.page.locator('#vmwareTabBadge').textContent(),'Ready');
  await f.page.keyboard.press('Alt+6');assert.equal(await f.page.locator('#tabButtonVmware').getAttribute('aria-selected'),'true');assert(await f.page.locator('#tabVmware').isVisible());
  assert(await f.page.locator('#vmConnect').isDisabled(),'needs a server and user');assert(!await f.page.locator('#vmEnvResult').isVisible());
  await f.page.locator('#vmServer').fill('vc01.example.com');await f.page.locator('#vmUser').fill('readonly@vsphere.local');await f.page.locator('#vmConnect').click();
  assert.equal(await f.page.locator('#vmConnectStatus').textContent(),'Type the password for readonly@vsphere.local.');assert.equal(vmwareCalls(f,'connect').length,0);
  await f.page.locator('#vmPassword').fill('Synthetic-Pa55');await f.page.locator('#vmPassword').press('Enter');await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Reading');
  assert.deepEqual(vmwareCalls(f,'connect').map(c=>c.body),[{server:'vc01.example.com',user:'readonly@vsphere.local',password:'Synthetic-Pa55'}]);assert.equal(await f.page.locator('#vmPassword').inputValue(),'','the password leaves the page');
  assert(await f.page.locator('#vmStop').isVisible());assert(await f.page.locator('#vmServer').isDisabled());
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isDisabled(),'collection locks while VMware reads');await f.page.keyboard.press('Alt+6');
  Object.assign(f.state.vmware,{running:false,action:'',status:'connectfailed',message:'vc01.example.com presented a certificate Windows does not trust.',certificate:{thumbprint:'AA:BB:CC:<b>',subject:'CN=vc01.example.com',issuer:'CN=CA <b>not markup</b>',notAfter:'2027-01-01',reason:'x509: certificate signed by unknown authority',mismatch:false}});
  await f.page.waitForFunction(()=>!document.querySelector('#vmCert').classList.contains('hidden'));assert.equal(await f.page.locator('#vmwareTabBadge').textContent(),'Failed');
  assert.equal(await f.page.locator('#vmCertIssuer').textContent(),'CN=CA <b>not markup</b>');assert.equal(await f.page.locator('#vmCert b').count(),0);
  await f.page.locator('#vmCertTrust').click();await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Reading');
  assert.deepEqual(vmwareCalls(f,'connect')[1].body,{server:'vc01.example.com',user:'readonly@vsphere.local',password:'',thumbprint:'AA:BB:CC:<b>'},'trust resends only the thumbprint; the held password is reused');
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();f.vm.sizing=vmwareSizingFixture(false);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',message:'Read 4 VM(s) on 3 host(s) from vc01.example.com.',certificate:null,summary:vmwareSummaryFixture(),inventorySerial:1,outputFolder:'C:\\Reports\\VMware_vc01',reportPath:'C:\\Reports\\VMware_vc01\\VMware-Readiness-Report.html'});
  await f.page.waitForFunction(()=>document.querySelectorAll('#vmTableBody tr.vm-row').length===4);
  assert.equal(await f.page.locator('#vmwareTabBadge').textContent(),'Read');assert(!await f.page.locator('#vmCert').isVisible());
  assert.equal(await f.page.locator('#vmFactServer').textContent(),'vc01.example.com (vCenter Server)');assert.equal(await f.page.locator('#vmCountBlocked').textContent(),'1');assert.equal(await f.page.locator('#vmCountsTitle').textContent(),'Readiness for Hyper-V');
  await f.page.locator('[data-vmfilter="blocked"]').click();assert.deepEqual(await f.page.locator('#vmTableBody tr.vm-row strong').allTextContents(),['LEGACY03']);
  await f.page.locator('[data-vmfilter="all"]').click();await f.page.locator('#vmSearch').fill('ubuntu');assert.deepEqual(await f.page.locator('#vmTableBody tr.vm-row strong').allTextContents(),['WEB02']);await f.page.locator('#vmSearch').fill('');
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmEnvResult').evaluate(e=>e.scrollIntoView({block:'start'}));await f.page.screenshot({path:shots+'/vmware-readiness-1440x1000.png'});
  await f.page.locator('tr[data-vm-id="vm-sql01"]').click();assert(await f.page.locator('#vmDetail').isVisible());const detail=await f.page.locator('#vmDetail').textContent();
  assert(detail.includes('Delete or consolidate snapshots before cutover.')&&detail.includes('Before patching <b>not markup</b>'),detail);assert.equal(await f.page.locator('#vmDetail b').count(),0);
  assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Virtual machine');assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'SQL01');assert(await f.page.locator('tr[data-vm-id="vm-sql01"]').evaluate(e=>e.classList.contains('selected')));
  assert.deepEqual(await f.page.locator('#vmDetail tbody td:nth-child(3) .vm-link').allTextContents(),['DS-SSD-01','DS-NL-02'],'each disk links to its datastore');
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.screenshot({path:shots+'/vmware-vm-details-1440x1000.png'});
  await f.page.locator('#vmDetail .vm-kv .vm-link',{hasText:'esx02.example.com'}).click();assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Host');assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'esx02.example.com');
  assert.equal(await f.page.locator('#vmDrawerBack').textContent(),'\u2039 SQL01');assert.deepEqual(await f.page.locator('#vmDrawerBody tbody td:first-child .vm-link').allTextContents(),['SQL01'],'the host lists its VMs');
  await f.page.locator('#vmDrawerBody .vm-kv .vm-link',{hasText:'Prod-Cluster'}).click();assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Cluster');assert((await f.page.locator('#vmDrawerBody').textContent()).includes('vSphere 8 Enterprise Plus'),'the cluster shows its licensing');
  await f.page.locator('#vmDrawerBack').click();assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'esx02.example.com');await f.page.locator('#vmDrawerBack').click();assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'SQL01');
  await f.page.keyboard.press('Escape');assert(!await f.page.locator('#vmDrawer').isVisible());assert.equal(await f.page.locator('#vmTableBody tr.selected').count(),0);
  await f.page.locator('tr[data-vm-id="vm-app01"]').focus();await f.page.keyboard.press('Space');assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'APP01');await f.page.keyboard.press('ArrowDown');assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'SQL01','arrow keys move through the VMs');
  assert.equal(await f.page.evaluate(()=>document.activeElement.getAttribute('data-vm-id')),'vm-sql01');await f.page.locator('#vmDrawerClose').click();assert(!await f.page.locator('#vmDrawer').isVisible());
  assert.equal(await f.page.locator('#vmTargetSelect option').count(),7,'every target is offered');
  await f.page.locator('#vmTargetSelect').selectOption('scale');await f.page.waitForFunction(()=>document.querySelector('#vmCountsTitle').textContent==='Readiness for Scale HyperCore');
  assert.equal(await f.page.locator('tr[data-vm-id="vm-app01"] .run-chip').textContent(),'Review first');assert.equal(vmwareCalls(f,'plan').at(-1).body.plan.target,'scale','the target is saved with the plan');
  await f.page.locator('#vmTargetSelect').selectOption('nutanix');await f.page.waitForFunction(()=>document.querySelector('#vmCountsTitle').textContent==='Readiness for Nutanix AHV');assert.equal(await f.page.locator('#vmCountReview').textContent(),'4');assert.equal(await f.page.locator('#vmCountBlocked').textContent(),'0','the cards follow the VM list');assert.equal(vmwareCalls(f,'plan').at(-1).body.plan.target,'nutanix');
  await f.page.locator('#vmOpenReport').click();await f.page.waitForTimeout(200);assert.deepEqual(f.opens,['vmwarereport']);
  await f.page.locator('#vmReset').click();await f.page.waitForFunction(()=>document.querySelector('#vmEnvResult').classList.contains('hidden'));assert.equal(await f.page.locator('#vmServer').inputValue(),'');
  assert(f.vmwareCalls.every(c=>c.token==='workspace-test'));assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware prepare source: needs a live session, preselects the needed steps, and posts them only after the VM name is typed',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);
  await f.page.locator('tr[data-vm-id="vm-legacy03"]').click();assert((await f.page.locator('#vmDetail').textContent()).includes('read the inventory from vCenter or ESXi in this session'));assert.equal(await f.page.locator('#vmPrepareButton').count(),0);
  Object.assign(f.vm.inventory.vms[2],{vtpm:true,encrypted:true});f.state.vmware.canPrepare=true;f.state.vmware.inventorySerial=2;
  await f.page.waitForFunction(()=>!!document.querySelector('#vmPrepareButton'));
  assert(!await f.page.locator('input[data-vm-prepare="snapshots"]').isChecked());for(const key of ['shutdown','removeTpm','decrypt']){assert(await f.page.locator('input[data-vm-prepare="'+key+'"]').isChecked(),key);}
  await f.page.locator('#vmPrepareButton').click();assert(await f.page.locator('#migrateDialog').isVisible());assert.equal(await f.page.locator('#migrateConfirmLabel').textContent(),"Type the VM's name to confirm: LEGACY03");
  assert((await f.page.locator('#migrateDialogAlert').textContent()).includes('BitLocker'));assert(await f.page.locator('#migrateConfirmButton').isDisabled());
  await f.page.locator('#migrateConfirmInput').fill('LEGACY03');await f.page.locator('#migrateConfirmInput').press('Enter');
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert.deepEqual(vmwareCalls(f,'prepare').map(c=>c.body),[{vmId:'vm-legacy03',steps:{snapshots:false,shutdown:true,removeTpm:true,decrypt:true},confirm:'LEGACY03'}]);
  Object.assign(f.vm.inventory.vms[2],{vtpm:false,encrypted:false,powerState:'poweredOff'});Object.assign(f.state.vmware,{running:false,action:'',status:'connected',prepare:{vm:'LEGACY03',done:['Shut down the guest.','Removed the virtual TPM.','Decrypted the VM and its disks.'],skipped:[]},prepareSerial:1,inventorySerial:3});
  await f.page.waitForFunction(()=>document.querySelector('#vmDetail').textContent.includes('Decrypted the VM and its disks.'));
  assert(!await f.page.locator('input[data-vm-prepare="decrypt"]').isChecked(),'nothing left to decrypt');assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware move to Hyper-V: copy and cut over need a live session and the typed VM name, post the folders and options, and show each disk\'s result',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);
  await f.page.locator('tr[data-vm-id="vm-app01"]').click();assert((await f.page.locator('#vmMove').textContent()).includes('run this on the Hyper-V host'));assert.equal(await f.page.locator('#vmMoveCopy').count(),0);
  f.state.vmware.canPrepare=true;f.state.vmware.hyperVHere=false;f.state.vmware.moveCopyRoot='D:\\Migration\\VMware-Copies';f.state.vmware.inventorySerial=2;
  await f.page.waitForFunction(()=>!!document.querySelector('#vmMoveCopy'));
  assert((await f.page.locator('#vmMove .vm-runs-on').textContent()).includes('This computer is not a Hyper-V host'));assert(await f.page.locator('#vmMoveCopy').isDisabled()&&await f.page.locator('#vmMoveCutover').isDisabled(),'a move runs only on the Hyper-V host');
  const rolesOff=await f.page.locator('#vmRolesSummary').textContent();assert(rolesOff.includes('Run this application on the Hyper-V host')&&rolesOff.includes('SRV-DEMO-01) is not a Hyper-V host'),rolesOff);
  f.state.vmware.hyperVHere=true;f.state.vmware.inventorySerial=3;
  await f.page.waitForFunction(()=>document.querySelector('#vmMove .vm-runs-on').textContent.includes('Runs on the Hyper-V host (this computer)')&&!document.querySelector('#vmMoveCopy').disabled);
  await f.page.locator('#vmRoles > summary').click();assert.equal(await f.page.locator('#vmRolesGrid .vm-role').count(),3);
  assert.equal(await f.page.locator('#vmRolesGrid .vm-role.here h4').textContent(),'The Hyper-V host that will run the VM');assert((await f.page.locator('#vmRolesFlow').textContent()).includes('this application on the Hyper-V host'));
  await f.page.locator('#vmDrawerClose').click();await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('#vmwareBody').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:shots+'/vmware-roles-1440x1000.png'});
  await f.page.locator('#vmRoles > summary').click();await f.page.locator('tr[data-vm-id="vm-app01"]').click();await f.page.locator('#vmMoveCopy').waitFor();
  assert.equal(await f.page.locator('#vmMoveFolder').getAttribute('placeholder'),'D:\\Migration\\VMware-Copies\\APP01');
  assert((await f.page.locator('#vmMove').textContent()).includes('No Hyper-V switch is named for VM-Servers'));
  assert(await f.page.locator('input[data-vm-move="quiesce"]').isChecked());assert(!await f.page.locator('input[data-vm-move="start"]').isChecked());
  await f.page.locator('#vmMoveFolder').fill('E:\\Copies\\APP01');
  await f.page.locator('#vmMoveCopy').click();assert(await f.page.locator('#migrateDialog').isVisible());
  const copyText=await f.page.locator('#migrateDialogText').textContent();assert(copyText.includes('changed block tracking')&&copyText.includes('E:\\Copies\\APP01'),copyText);
  assert(await f.page.locator('#migrateConfirmButton').isDisabled());await f.page.locator('#migrateConfirmInput').fill('APP01');await f.page.locator('#migrateConfirmInput').press('Enter');
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert(await f.page.locator('#vmStop').isVisible(),'a copy can be stopped');
  assert.deepEqual(vmwareCalls(f,'move').map(c=>c.body),[{vmId:'vm-app01',action:'copy',copyFolder:'E:\\Copies\\APP01',vmFolder:'',quiesce:true,keepMac:false,start:false,confirm:'APP01'}]);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',move:{vm:'APP01',cutover:false,round:{disks:[{label:'Hard disk 1',mode:'seed',bytesWritten:21474836480,verified:false}]},disks:['E:\\Copies\\APP01\\APP01-disk0.vhdx']},moveSerial:1});
  await f.page.waitForFunction(()=>document.querySelector('#vmMove')&&document.querySelector('#vmMove').textContent.includes('first copy of the used areas'));
  assert.equal(await f.page.locator('#vmMoveCopy').textContent(),'Copy changes\u2026');
  await f.page.locator('input[data-vm-move="start"]').check();
  await f.page.locator('#vmMoveCutover').click();assert((await f.page.locator('#migrateDialogAlert').textContent()).includes('shut down through VMware Tools'));
  assert((await f.page.locator('#migrateDialogText').textContent()).includes('and start it'));
  await f.page.locator('#migrateConfirmInput').fill('app01');await f.page.locator('#migrateConfirmButton').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert.equal(vmwareCalls(f,'move').at(-1).body.action,'cutover');assert.equal(vmwareCalls(f,'move').at(-1).body.start,true);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',message:'APP01 was created on Hyper-V and started.',move:{vm:'APP01',cutover:true,shutDown:true,round:{disks:[{label:'Hard disk 1',mode:'changed',bytesWritten:83886080,verified:true}]},hyperV:{generation:2},hyperVId:'4f6c1d6e-2b1a-4c55-9d3e-0f1e2d3c4b5a'},moveSerial:2});
  await f.page.waitForFunction(()=>document.querySelector('#vmMove').textContent.includes('Created on Hyper-V as APP01 (generation 2)'));
  assert((await f.page.locator('#vmMove').textContent()).includes('changed blocks only, 80.0 MB written, verified'));
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmMove').evaluate(e=>e.scrollIntoView({block:'center'}));await f.page.screenshot({path:shots+'/vmware-move-1440x1000.png'});
  await f.page.locator('#vmTargetSelect').selectOption('scale');await f.page.waitForFunction(()=>!document.querySelector('#vmMove'));
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware move to Proxmox VE and physical servers: connect, receiver media, create, receiver and copy each post what was chosen, with the typed VM name where vSphere or the platform changes',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);f.state.vmware.canPrepare=true;f.state.vmware.inventorySerial=2;
  await f.page.locator('#vmTargetSelect').selectOption('proxmox');await f.page.waitForFunction(()=>document.querySelector('#vmCountsTitle').textContent==='Readiness for Proxmox VE');
  await f.page.locator('tr[data-vm-id="vm-app01"]').click();await f.page.locator('#vmMoveTarget').waitFor();assert.equal(await f.page.locator('#vmMove').count(),0,'the Hyper-V panel is only for Hyper-V');
  assert((await f.page.locator('#vmMoveTarget .vm-runs-on').textContent()).includes('Runs on this computer, your admin computer or jump box'));
  assert.deepEqual(await f.page.locator('#vmMoveTarget .vm-step-where').allTextContents(),['From this computer','On this computer','On Proxmox VE',"On the new VM's console",'From this computer: vCenter to the receiver']);
  assert((await f.page.locator('#vmRolesSummary').textContent()).includes('the new VM on Proxmox VE boots the receiver media'));
  assert.deepEqual(await f.page.locator('#vmRolesGrid .vm-role').evaluateAll(n=>n.map(x=>x.getAttribute('data-role'))),['console','source','receiver','after']);
  assert(await f.page.locator('#vmTargetUpload').isDisabled()&&await f.page.locator('#vmTargetCreate').isDisabled()&&await f.page.locator('#vmTargetCopy').isDisabled(),'later steps wait for the earlier ones');
  await f.page.locator('[data-vm-target="server"]').fill('pve1.example.com');await f.page.locator('[data-vm-target="user"]').fill('migrate@pve!move');await f.page.locator('[data-vm-target="password"]').fill('Synthetic-Token-Secret');
  await f.page.locator('#vmTargetConnect').click();await f.page.waitForFunction(()=>document.querySelector('#vmMoveTarget').textContent.includes('Connected to pve1.example.com: Proxmox VE 8.3.0.'));
  assert.deepEqual(vmwareCalls(f,'target/connect').map(c=>c.body),[{platform:'proxmox',server:'pve1.example.com',user:'migrate@pve!move',password:'Synthetic-Token-Secret',node:''}]);
  assert.equal(await f.page.locator('[data-vm-target="password"]').inputValue(),'','the secret is not kept in the page');
  await f.page.locator('[data-vm-target="drivers"]').fill('D:\\virtio-win\\amd64\\2k22; D:\\virtio-win\\NetKVM');await f.page.locator('#vmTargetMedia').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');assert(await f.page.locator('#vmStop').isVisible(),'building media can be stopped');
  assert.deepEqual(vmwareCalls(f,'target/media').map(c=>c.body),[{target:'proxmox',drivers:['D:\\virtio-win\\amd64\\2k22','D:\\virtio-win\\NetKVM']}]);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected'});Object.assign(f.state.vmware.target,{mediaPath:'C:\\Reports\\Receiver-Media_20261125\\receiver-proxmox.iso',message:'Receiver media written.',serial:5});
  await f.page.waitForFunction(()=>document.querySelector('#vmMoveTarget').textContent.includes('Built: C:\\Reports'));
  assert.equal(await f.page.locator('[data-vm-target="isoName"]').inputValue(),'local/receiver-app01.iso');
  await f.page.locator('#vmTargetUpload').click();await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert.deepEqual(vmwareCalls(f,'target/upload').map(c=>c.body),[{path:'C:\\Reports\\Receiver-Media_20261125\\receiver-proxmox.iso',name:'local/receiver-app01.iso'}]);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected'});Object.assign(f.state.vmware.target,{iso:'local:iso/receiver-app01.iso',serial:6});
  await f.page.waitForFunction(()=>!document.querySelector('#vmTargetCreate').disabled);
  await f.page.locator('[data-vm-target="storage"]').selectOption('ceph-vm');await f.page.locator('[data-vm-target="net0"]').selectOption('vmbr1');await f.page.locator('[data-vm-target="vlan0"]').fill('20');await f.page.locator('[data-vm-target="bus"]').selectOption('scsi');
  await f.page.locator('#vmTargetCreate').click();assert(await f.page.locator('#migrateDialog').isVisible());assert((await f.page.locator('#migrateDialogText').textContent()).includes('local:iso/receiver-app01.iso'));
  await f.page.locator('#migrateConfirmInput').fill('APP01');await f.page.locator('#migrateConfirmInput').press('Enter');await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert.deepEqual(vmwareCalls(f,'target/create').map(c=>c.body),[{vmId:'vm-app01',name:'APP01',storage:'ceph-vm',bus:'scsi',nicModel:'virtio',keepMac:false,networks:[{network:'vmbr1',vlan:20}],confirm:'APP01'}]);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected'});Object.assign(f.state.vmware.target,{created:{'vm-app01':{platform:'proxmox',vm:{id:'101',name:'APP01',state:'running'}}},message:'APP01 was created on Proxmox VE and started from the receiver media.',serial:7});
  await f.page.waitForFunction(()=>document.querySelector('#vmMoveTarget').textContent.includes('Created on Proxmox VE as APP01 (101).'));
  await f.page.locator('[data-vm-target="address"]').fill('192.0.2.50:8443');await f.page.locator('[data-vm-target="code"]').fill('ABCDE-FGHIJ-KLMNO-PQRST');await f.page.locator('[data-vm-target="thumbprint"]').fill('AA:BB');
  await f.page.locator('#vmTargetReceiver').click();await f.page.waitForFunction(()=>document.querySelector('#vmMoveTarget').textContent.includes('goes to disk-0, Disk 0, QEMU HARDDISK'));
  assert.deepEqual(vmwareCalls(f,'target/receiver').map(c=>c.body),[{vmId:'vm-app01',address:'192.0.2.50:8443',code:'ABCDE-FGHIJ-KLMNO-PQRST',thumbprint:'AA:BB'}]);
  assert.equal(await f.page.locator('[data-vm-target="code"]').inputValue(),'','the pairing code is not kept in the page');
  await f.page.locator('#vmTargetCopy').click();const copyText=await f.page.locator('#migrateDialogText').textContent();assert(copyText.includes('192.0.2.50:8443')&&copyText.includes('changed block tracking'),copyText);
  await f.page.locator('#migrateConfirmInput').fill('APP01');await f.page.locator('#migrateConfirmInput').press('Enter');await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert.deepEqual(vmwareCalls(f,'target/move').map(c=>c.body),[{vmId:'vm-app01',action:'copy',quiesce:true,confirm:'APP01'}]);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',move:{vm:'APP01',cutover:false,round:{disks:[{label:'Hard disk 1',mode:'seed',target:'disk-0 on 192.0.2.50:8443',bytesWritten:21474836480,verified:false}]}},moveSerial:1});
  await f.page.waitForFunction(()=>document.querySelector('#vmMoveTarget').textContent.includes('first copy of the used areas to disk-0 on 192.0.2.50:8443'));
  await f.page.locator('#vmTargetCutover').click();assert((await f.page.locator('#migrateDialogText').textContent()).includes('switch it to boot from its first disk'));await f.page.locator('#migrateDialogCancel').click();
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmMoveTarget').evaluate(e=>e.scrollIntoView({block:'start'}));await f.page.screenshot({path:shots+'/vmware-move-target-1440x1000.png'});
  await f.page.locator('#vmTargetSelect').selectOption('physical');await f.page.waitForFunction(()=>document.querySelector('#vmMoveTarget')&&document.querySelector('#vmMoveTarget').textContent.includes('Move this VM to a physical server'));
  assert.equal(await f.page.locator('#vmTargetConnect').count(),0,'a physical server has no platform to connect to');assert.equal(await f.page.locator('#vmTargetCreate').count(),0);
  await f.page.locator('#vmTargetSelect').selectOption('azure');await f.page.waitForFunction(()=>!document.querySelector('#vmMoveTarget'));
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware Hosts card: every cluster and host with hardware, licensing and items to check, all linked',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);
  assert.equal(await f.page.locator('#vmCountHosts').textContent(),'3');assert.equal(await f.page.locator('#vmCountHostsSub').textContent(),'1 cluster, 1 license');
  f.vm.inventory.hosts=[{name:'esx01.example.com',datacenter:'DC-East',cluster:'Prod-Cluster',vendor:'Example',model:'R760',cpuModel:'Xeon Gold',sockets:2,cores:32,threads:64,memoryBytes:549755813888,version:'8.0.2',build:'22380479',connectionState:'connected',license:{name:'vSphere 8 Enterprise Plus',key:'AB12C-*****-*****-*****-MN90P'},details:{serialNumber:'SVC1234',biosVendor:'Example Inc.',biosVersion:'2.19.1',managementIp:'192.0.2.21',overallStatus:'yellow',ntpServers:['192.0.2.123'],ntpRunning:true,physicalNics:[{device:'vmnic0',driver:'ixgben',speedMb:10000,duplex:true,mac:'00:00:5e:00:53:01',switch:'vSwitch0'}],switches:[{name:'vSwitch0',kind:'Standard',mtu:1500,uplinks:['vmnic0'],portgroups:['VM-Servers']}],services:[{key:'ntpd',label:'NTP Daemon',running:true,policy:'on'},{key:'TSM-SSH',label:'SSH',running:false,policy:'off'}],packages:[{name:'esx-base',version:'8.0.2-0.0.22380479',vendor:'VMware',acceptance:'VMwareCertified'}],alarms:['Host hardware sensor <b>warning</b>']}},{name:'esx02 <b>x</b>',datacenter:'DC-East',cluster:'Prod-Cluster',connectionState:'connected',maintenance:true}];
  f.vm.inventory.licenses=[{name:'vSphere 8 Enterprise Plus',key:'AB12C-*****-*****-*****-MN90P',product:'VMware ESX Server',version:'8.*',used:4,total:8,unit:'cpuPackage',expires:'2026-12-15',assignedTo:['esx01.example.com']},{name:'vCenter Server 8 Standard',key:'CD34E-*****-*****-*****-QR12S',product:'VMware VirtualCenter Server',version:'8.*',used:1,total:1,unit:'server',expires:'',assignedTo:['vc01.example.com']}];
  f.vm.attention=['License vSphere 8 Enterprise Plus (AB12C-*****-*****-*****-MN90P) expires on 2026-12-15.','Host esx02 <b>x</b> is in maintenance mode.'];
  Object.assign(f.state.vmware,{inventorySerial:2,discoveryPath:'C:\\Reports\\VMware_vc01\\VMware-Discovery-Report.html'});
  await f.page.waitForFunction(()=>document.querySelector('#vmCountHostsSub').textContent==='1 cluster, 2 licenses');assert.equal(await f.page.locator('#vmCountHosts').textContent(),'2');
  await f.page.locator('#vmExploreBy').selectOption('network');await f.page.locator('#vmExplorePanel > summary').click();assert(!await f.page.locator('#vmExplorePanel').evaluate(e=>e.open));
  await f.page.locator('#vmHostsCard').click();assert(await f.page.locator('#vmExplorePanel').evaluate(e=>e.open),'the Hosts card opens Browse');assert.equal(await f.page.locator('#vmExploreBy').inputValue(),'host');
  assert.equal(await f.page.evaluate(()=>document.activeElement.parentElement.className),'vm-host-node','focus lands on the first host');
  assert.equal(await f.page.locator('#vmHostsWatch li').count(),2);assert.equal(await f.page.locator('#vmExplorePanel b').count(),0,'names and notes are text');
  const cluster=f.page.locator('#vmExploreTree > details.vm-cluster-node');assert(await cluster.evaluate(e=>e.open));
  const facts=await cluster.locator('.vm-tree-body > dl.vm-kv').first().textContent();assert(facts.includes('High availabilityOn')&&facts.includes('DRSOn'),facts);
  const esx01=cluster.locator('details.vm-host-node').first();const kv=await esx01.locator('dl.vm-kv').first().textContent();
  assert(kv.includes('SVC1234')&&kv.includes('8.0.2 build 22380479')&&kv.includes('192.0.2.21')&&kv.includes('vSphere 8 Enterprise Plus')&&kv.includes('health yellow'),kv);
  const fold=re=>esx01.locator('details > summary').filter({hasText:re}).first().locator('..');
  assert((await fold(/^Alarms and configuration issues/).textContent()).includes('Host hardware sensor <b>warning</b>'));
  const hw=fold(/^Hardware and firmware/);await hw.locator('> summary').click();await hw.locator('dl').waitFor();assert((await hw.textContent()).includes('Example Inc. 2.19.1'));
  const net=fold(/^Network/);await net.locator('> summary').click();await net.locator('tbody').first().waitFor();assert((await net.textContent()).includes('10 Gb full'));assert.deepEqual(await net.locator('.vm-link').allTextContents(),['VM-Servers'],'port groups link to their network');
  const svc=fold(/^Services/);assert((await svc.locator('> summary').textContent()).includes('1 of 2 running'));
  assert((await cluster.locator('details.vm-host-node').nth(1).locator('> summary').textContent()).includes('maintenance mode'));
  const lic=cluster.locator('> .vm-tree-body > details.vm-section-node',{hasText:'Licensing'});assert((await lic.locator('> summary').textContent()).includes('1 license'));await lic.locator('> summary').click();await lic.locator('tbody').waitFor();
  const licRow=await lic.locator('tbody tr').textContent();assert(licRow.includes('AB12C-*****-*****-*****-MN90P')&&licRow.includes('4 / 8 cpuPackage')&&licRow.includes('2026-12-15'),licRow);
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmExplorePanel').evaluate(e=>e.scrollIntoView({block:'start'}));await f.page.screenshot({path:shots+'/vmware-hosts-1440x1000.png'});
  await lic.locator('tbody .vm-link',{hasText:'vSphere 8 Enterprise Plus'}).click();assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'License');
  await f.page.locator('#vmDrawerBody tbody .vm-link',{hasText:'esx01.example.com'}).click();assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Host');assert((await f.page.locator('#vmDrawerBody').textContent()).includes('SVC1234'));
  await f.page.locator('#vmDrawerBody button',{hasText:'Show its VMs in the list'}).click();assert.equal(await f.page.locator('#vmSearch').inputValue(),'esx01.example.com');
  const all=f.page.locator('#vmExploreTree > details',{hasText:'All licenses'});assert((await all.locator('> summary').textContent()).includes('2 licenses'));
  await f.page.locator('#vmOpenDiscovery').click();await f.page.waitForTimeout(200);assert.deepEqual(f.opens,['vmwarediscovery']);
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware counts: All VMs matches the list, VMware system VMs are counted as Not moved and can be listed',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);assert(!await f.page.locator('#vmCountSkipCard').isVisible(),'no system VMs, no card');assert.equal(await f.page.locator('#vmCountsNote').textContent(),'');
  const skip=vmFixtureVM('vCLS-0001',{hv:'skip',sc:'skip',nx:'skip',hvFindings:[{severity:'info',code:'vmware-system',message:'vSphere Cluster Services VM. It is not moved.',action:''}],scFindings:[],nxFindings:[],vm:{guestOsRunning:'Other 3.x Linux (64-bit)',cpus:1,memoryMb:128}});
  f.vm.inventory.vms.push(skip);f.state.vmware.inventorySerial=2;await f.page.waitForFunction(()=>document.querySelectorAll('#vmTableBody tr.vm-row').length===5);
  assert.equal(await f.page.locator('#vmCountAll').textContent(),'5');assert.equal(await f.page.locator('#vmTableNote').textContent(),'5 VMs');assert.equal(await f.page.locator('#vmCountSkip').textContent(),'1');
  const sum=['#vmCountBlocked','#vmCountReview','#vmCountReady','#vmCountSkip'];let total=0;for(const id of sum){total+=Number(await f.page.locator(id).textContent());}assert.equal(total,5,'the cards add up to All VMs');
  assert.equal(await f.page.locator('#vmCountsNote').textContent(),'4 VMs to move; 1 VMware system VM stays behind.');
  await f.page.locator('[data-vmfilter="skip"]').click();assert.deepEqual(await f.page.locator('#vmTableBody tr.vm-row strong').allTextContents(),['vCLS-0001']);assert.equal(await f.page.locator('#vmTableBody tr.vm-row td').nth(6).textContent(),'VMware system VM, not moved.');
  await f.page.locator('tr[data-vm-id="vm-vcls-0001"]').click();assert.equal(await f.page.locator('#vmPrepareButton').count(),0,'system VMs cannot be prepared');assert.equal(await f.page.locator('#vmDetail .vm-actions button',{hasText:'Prepare on VMware'}).count(),0);
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware browse: each host with its VMs, each datastore with its disks and each network with its VMs, searchable, linking to the VM',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);
  assert(await f.page.locator('#vmExplorePanel').evaluate(e=>e.open),'browse is open after a read');assert.equal(await f.page.locator('#vmExploreSummary').textContent(),'(3 hosts, 2 datastores, 2 networks, 1 license)');
  const cluster=f.page.locator('#vmExploreTree > details.vm-cluster-node');assert.equal(await cluster.count(),1);assert(await cluster.evaluate(e=>e.open),'a single cluster opens');
  assert((await cluster.locator('> summary').textContent()).includes('Prod-Cluster')&&(await cluster.locator('> summary').textContent()).includes('3 hosts, 4 VMs'));
  const hosts=cluster.locator('details.vm-host-node');assert.equal(await hosts.count(),3);const esx01=hosts.nth(0);assert((await esx01.locator('summary').textContent()).includes('2 VMs (2 on)'));
  await esx01.locator('> summary').click();await esx01.locator('tbody .vm-link').first().waitFor();assert.deepEqual(await esx01.locator('tbody td:first-child .vm-link').allTextContents(),['APP01','LEGACY03']);
  assert((await esx01.locator('dl.vm-kv').first().textContent()).includes('DatastoresDS-SSD-01'));assert((await esx01.textContent()).includes('Hard disk 1 100 GB on DS-SSD-01'));
  await hosts.nth(1).locator('> summary').click();await hosts.nth(1).locator('tbody').waitFor();assert((await hosts.nth(1).textContent()).includes('Hard disk 2 1.0 TB on DS-NL-02'));
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmExplorePanel').evaluate(e=>e.scrollIntoView({block:'start'}));await f.page.screenshot({path:shots+'/vmware-browse-1440x1000.png'});
  await f.page.locator('#vmExploreBy').selectOption('datastore');const stores=f.page.locator('#vmExploreTree > details');assert.equal(await stores.count(),2);
  assert((await stores.nth(0).locator('summary').textContent()).includes('4 VMs, 4 disks'));assert.equal(await stores.nth(0).locator('.vm-meter').count(),1);
  await stores.nth(1).locator('summary').click();await stores.nth(1).locator('tbody').waitFor();const nl=await stores.nth(1).textContent();assert(nl.includes('SQL01')&&nl.includes('[DS-NL-02] SQL01/SQL01_1.vmdk')&&nl.includes('Mounted on: esx02.example.com, esx03.example.com'),nl);
  await f.page.locator('#vmExploreBy').selectOption('network');const nets=f.page.locator('#vmExploreTree > details');assert.equal(await nets.count(),2);assert((await nets.nth(1).locator('summary').textContent()).includes('VLAN 30'));
  await nets.nth(1).locator('summary').click();await nets.nth(1).locator('tbody .vm-link').first().waitFor();assert.deepEqual(await nets.nth(1).locator('tbody td:first-child .vm-link').allTextContents(),['WEB02']);
  await f.page.locator('#vmExploreBy').selectOption('host');await f.page.locator('#vmExploreSearch').fill('sql');
  const found=f.page.locator('#vmExploreTree details.vm-host-node[open]');assert.equal(await found.count(),1,'only the host with a match');assert.deepEqual(await found.locator('tbody td:first-child .vm-link').allTextContents(),['SQL01']);
  await found.locator('tbody td:first-child .vm-link').click();assert.equal(await f.page.locator('#vmSearch').inputValue(),'','opening a VM keeps the list as it was');assert(await f.page.locator('#vmDetail').isVisible());assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'SQL01');
  assert.equal(await f.page.evaluate(()=>document.activeElement.id),'vmDrawerTitle');await f.page.locator('#vmDrawerClose').click();
  await found.locator('.vm-tree-body > dl.vm-kv .vm-link',{hasText:'DS-NL-02'}).click();assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Datastore');assert((await f.page.locator('#vmDrawerBody').textContent()).includes('[DS-NL-02] SQL01/SQL01_1.vmdk'));await f.page.keyboard.press('Escape');
  await f.page.locator('#vmExploreSearch').fill('nothing-like-this');assert.equal(await f.page.locator('#vmExploreTree').textContent(),'Nothing matches.');
  f.vm.inventory.vms[0].name='APP <b>01</b>';f.vm.inventory.vms[0].id='vm-x';f.state.vmware.inventorySerial=2;await f.page.locator('#vmExploreSearch').fill('app');
  await f.page.waitForFunction(()=>document.querySelector('#vmExploreTree').textContent.includes('APP <b>01</b>'));assert.equal(await f.page.locator('#vmExploreTree b').count(),0,'names are text');
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware plan: capacity, network and wave edits survive polls; Suggest waves and Save post the plan; the runbook is written after saving',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);await f.page.locator('[data-vmview="plan"]').click();assert(await f.page.locator('#vmPlanContent').isVisible());
  assert((await f.page.locator('#vmSizing').textContent()).includes('Not described'));
  await f.page.locator('#vmCapCores').fill('32');await f.page.locator('#vmCapMemory').fill('512');await f.page.locator('#vmCapStorage').fill('20');await f.page.locator('[aria-label="New network for VM-Servers"]').fill('Servers');await f.page.locator('[aria-label="New VLAN for VM-Servers"]').fill('20');
  await f.page.locator('input[data-vm-wave="vm-app01"]').fill('3');await f.page.locator('[aria-label="Notes for APP01"]').fill('Weekend window');
  assert.equal(await f.page.locator('#vmPlanDirtyNote').textContent(),'Unsaved changes.');await f.page.waitForTimeout(1100);
  assert.equal(await f.page.locator('input[data-vm-wave="vm-app01"]').inputValue(),'3','edits survive polls');assert.equal(await f.page.locator('#vmCapCores').inputValue(),'32');
  await f.page.locator('#vmSavePlan').click();await f.page.waitForFunction(()=>document.querySelector('#vmPlanStatus').textContent==='Plan saved.');
  const saved=vmwareCalls(f,'plan')[0].body.plan;assert.equal(saved.capacity.coresPerNode,32);assert.equal(saved.capacity.memoryGbPerNode,512);assert.deepEqual(saved.networks[0],{source:'VM-Servers',sourceVlan:'20',targetName:'Servers',targetVlan:'20'});
  assert.deepEqual(saved.vms[0],{id:'vm-app01',name:'APP01',wave:3,hold:false,exclude:false,notes:'Weekend window'});assert((await f.page.locator('#vmSizing').textContent()).includes('Fits'));assert.equal(await f.page.locator('#vmPlanDirtyNote').textContent(),'');
  await f.page.locator('#vmWaveSize').fill('5');await f.page.locator('#vmSuggest').click();await f.page.waitForFunction(()=>/Suggested waves/.test(document.querySelector('#vmPlanStatus').textContent));
  assert.equal(vmwareCalls(f,'plan')[1].body.suggest,true);assert.equal(vmwareCalls(f,'plan')[1].body.waveSize,5);
  assert.equal(await f.page.locator('#vmWaveSummary').textContent(),'wave 1: 1, wave 2: 2, 1 on hold');assert(await f.page.locator('[aria-label="Hold LEGACY03"]').isChecked());
  await f.page.locator('#vmWriteRunbook').click();await f.page.waitForFunction(()=>document.querySelector('#vmPlanStatus').textContent==='Wrote the runbook and plan CSV.');
  assert.equal(vmwareCalls(f,'plan').length,3,'the runbook saves the plan first');assert.equal(vmwareCalls(f,'export-plan').length,1);
  await f.page.waitForFunction(()=>!document.querySelector('#vmOpenRunbook').classList.contains('hidden'));await f.page.locator('#vmOpenRunbook').click();await f.page.locator('#vmOpenRunbookDocx').click();await f.page.locator('#vmOpenPlanCsv').click();await f.page.waitForTimeout(200);assert.deepEqual(f.opens,['vmwarerunbook','vmwarerunbookdocx','vmwareplancsv']);
  await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('#vmPlanContent').evaluate(e=>e.scrollIntoView({block:'start'}));await f.page.screenshot({path:shots+'/vmware-plan-1440x1000.png'});
  await f.page.locator('#vmTargetSelect').selectOption('azure');await f.page.waitForFunction(()=>document.querySelector('#vmCapacityTitle').textContent==='Cloud quota');await f.page.waitForFunction(()=>/Check quota/.test(document.querySelector('#vmSizing').textContent));assert(!await f.page.locator('#vmCapCores').isVisible(),'no cluster fields for a cloud target');
  await f.page.locator('#vmTargetSelect').selectOption('physical');await f.page.waitForFunction(()=>document.querySelector('#vmCapacityTitle').textContent==='Servers needed');
  await f.page.locator('#vmTargetSelect').selectOption('hyperv');await f.page.waitForFunction(()=>document.querySelector('#vmCapacityTitle').textContent==='New cluster size');assert(await f.page.locator('#vmCapCores').isVisible());
  f.rejectVmware('plan','Wave numbers go from 0 to 99.');await f.page.locator('#vmSavePlan').click();await f.page.waitForFunction(()=>document.querySelector('#vmPlanStatus').classList.contains('error'));assert.equal(await f.page.locator('#vmPlanStatus').textContent(),'Wave numbers go from 0 to 99.');
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware disk copy: the receiver shows its address, code and thumbprint; sending checks them and sends only the chosen disks',async()=>{const f=await fixture(browser);try{
  await f.page.keyboard.press('Alt+6');await f.page.locator('[data-vmview="disks"]').click();
  assert(await f.page.locator('#vmReceiveTitle').isVisible()&&await f.page.locator('#vmSendTitle').isVisible(),'both sides show until a role is chosen');
  await f.page.locator('[data-diskrole="send"]').click();assert(await f.page.locator('#vmReceiveTitle').isHidden());assert(await f.page.locator('#vmSendTitle').isVisible());assert.equal(await f.page.locator('#vmFlowSend.here').count(),1);
  await f.page.locator('[data-diskrole="receive"]').click();assert(await f.page.locator('#vmSendTitle').isHidden());assert(await f.page.locator('#vmReceiveTitle').isVisible());assert.equal(await f.page.locator('[data-diskrole="receive"]').getAttribute('aria-pressed'),'true');
  await f.page.locator('[data-diskrole="receive"]').click();assert(await f.page.locator('#vmSendTitle').isVisible(),'choosing the role again shows both sides');
  assert.equal(await f.page.locator('#vmReceiveFolder').getAttribute('placeholder'),'D:\\Migration\\VMware-Copies\\Received');
  await f.page.locator('#vmReceivePort').fill('44a');await f.page.locator('#vmReceiveStart').click();assert((await f.page.locator('#vmReceiveStatus').textContent()).includes('as a number'));assert.equal(vmwareCalls(f,'receiver').length,0);
  await f.page.locator('#vmReceivePort').fill('44390');await f.page.locator('#vmReceiveStart').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmReceiveCode').textContent==='K7QMZ-2RT4X-9WBNP-H3VDA');
  assert.deepEqual(vmwareCalls(f,'receiver').map(c=>c.body),[{action:'start',folder:'',port:44390,firewall:true}]);
  const where=await f.page.locator('#vmReceiveAddress').textContent();assert(where.includes('192.0.2.40:44390')&&where.includes('allowed in Windows Firewall'),where);
  assert(await f.page.locator('#vmReceiveStart').isHidden());
  await f.page.locator('#vmSendFind').click();await f.page.waitForFunction(()=>document.querySelectorAll('[data-send-disk]').length===2);
  assert((await f.page.locator('#vmSendDisks').textContent()).includes('Disk 0, 100 GB (Windows): no drive letter FAT32'));
  await f.page.locator('#vmSendAddress').fill('hv01.example.com');await f.page.locator('#vmSendThumbprint').fill('3F:9A:6C:0E 1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e');await f.page.locator('#vmSendCode').fill('K7QMZ-2RT4X');
  assert(await f.page.locator('#vmSendStart').isDisabled(),'this computer is receiving');
  await f.page.locator('#vmReceiveStop').click();await f.page.waitForFunction(()=>!document.querySelector('#vmReceiveIdle').classList.contains('hidden'));assert.deepEqual(vmwareCalls(f,'receiver').at(-1).body,{action:'stop'});
  await f.page.locator('[data-send-disk="1"]').uncheck();
  await f.page.locator('#vmSendStart').click();assert((await f.page.locator('#vmSendStatus').textContent()).includes('20-character pairing code'));assert.equal(vmwareCalls(f,'agent/send').length,0);
  await f.page.locator('#vmSendCode').fill('K7QMZ-2RT4X-9WBNP-H3VDA');await f.page.locator('#vmSendStart').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert.deepEqual(vmwareCalls(f,'agent/send').map(c=>c.body),[{disks:[0],address:'hv01.example.com',thumbprint:'3F9A6C0E1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e',code:'K7QMZ-2RT4X-9WBNP-H3VDA',verify:true,allowLive:false}]);
  assert(await f.page.locator('#vmStop').isVisible(),'a send can be stopped');assert(await f.page.locator('#vmSendCode').isDisabled());
  Object.assign(f.state.vmware,{running:false,action:'',status:'idle',message:'Sent 1 disk to hv01.example.com.',send:[{disk:0,name:'APP01-disk0.vhdx',bytesRead:42949672960,bytesWritten:1073741824,resumed:true,verified:true,passes:2,shadowed:1}],sendSerial:1});
  await f.page.waitForFunction(()=>document.querySelector('#vmSendResults').textContent.includes('APP01-disk0.vhdx'));
  const sent=await f.page.locator('#vmSendResults').textContent();assert(sent.includes('1.0 GB sent (pass 2, only what changed), 1 volume from a shadow copy, verified'),sent);
  f.state.vmware.receiver={running:true,port:44380,code:'K7QMZ-2RT4X-9WBNP-H3VDA',thumbprint:'3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e',folder:'D:\\Migration\\VMware-Copies\\Received',addresses:['192.0.2.40'],sessions:1,firewall:true,defaultFolder:'D:\\Migration\\VMware-Copies\\Received'};
  await f.page.waitForFunction(()=>document.querySelector('#vmReceiveSessions').textContent==='1 disk');
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmwareBody').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:shots+'/vmware-disk-copy-1440x1000.png'});
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware Inside the VM: recording and checks need no confirmation; fix-ups need the typed computer name and send only the chosen items',async()=>{const f=await fixture(browser);try{
  await f.page.keyboard.press('Alt+6');await f.page.locator('[data-vmview="guest"]').click();assert(await f.page.locator('#vmGuestEmpty').isVisible());
  assert((await f.page.locator('#vmViewGuest .vm-runs-on').textContent()).includes('Runs inside the machine being moved'));assert.equal(await f.page.locator('[data-vmview="guest"]').textContent(),'Inside the VM');
  await f.page.locator('#vmGuestCapture').click();await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');assert.deepEqual(vmwareCalls(f,'guest/capture').map(c=>c.body),[{}]);
  assert(await f.page.locator('#vmGuestStatus').isDisabled(),'one step at a time');assert.equal(await f.page.locator('#vmGuestMessage').textContent(),'Recording this VM\'s settings.');
  f.vm.guest=vmwareGuestFixture('Hyper-V');Object.assign(f.state.vmware,{running:false,action:'',guestSerial:1,guestMessage:'Recorded this VM\'s settings.'});
  await f.page.waitForFunction(()=>!document.querySelector('#vmGuestCards').classList.contains('hidden'));
  assert.equal(await f.page.locator('#vmGuestPlatform').textContent(),'Hyper-V');assert((await f.page.locator('#vmGuestFindings').textContent()).includes('<b>not markup</b>'));assert.equal(await f.page.locator('#vmGuestFindings b').count(),0);
  assert((await f.page.locator('#vmGuestAfterNotes').textContent()).includes('W3SVC'));
  {const text=await f.page.locator('#vmGuestFindings').textContent();assert(text.includes('For Hyper-V: Hyper-V drivers are built in.')&&!text.includes('VirtIO'),text);}
  await f.page.locator('#vmTargetSelect').selectOption('nutanix');await f.page.waitForFunction(()=>document.querySelector('#vmGuestFindings').textContent.includes('For Nutanix AHV: VirtIO drivers are not installed.'));await f.page.locator('#vmTargetSelect').selectOption('hyperv');
  assert(await f.page.locator('input[data-vm-ghost^="PCI"]').isChecked(),'VMware adapters start ticked');assert(!await f.page.locator('input[data-vm-ghost^="USB"]').isChecked());assert(await f.page.locator('input[data-vm-disk="1"]').isChecked());assert.equal(await f.page.locator('input[data-vm-disk="0"]').count(),0,'never the boot disk');
  assert.equal(await f.page.locator('select[data-vm-ip="00-50-56-00-00-01"]').inputValue(),'7');
  await f.page.locator('#vmGuestApply').click();assert(await f.page.locator('#migrateDialog').isVisible());assert.equal(await f.page.locator('#migrateConfirmLabel').textContent(),"Type this computer's name to confirm: SRV-DEMO-01");
  assert(await f.page.locator('#migrateConfirmButton').isDisabled());await f.page.locator('#migrateConfirmInput').fill('srv-demo-01');await f.page.locator('#migrateConfirmButton').click();
  await f.page.waitForFunction(()=>!document.querySelector('#migrateDialog').open);
  assert.deepEqual(vmwareCalls(f,'guest/apply').map(c=>c.body),[{items:[{capture:'00-50-56-00-00-01',interfaceIndex:7,rename:true}],confirm:'srv-demo-01'}]);
  Object.assign(f.state.vmware,{running:false,action:''});await f.page.waitForTimeout(1100);
  await f.page.locator('input[data-vm-ghost^="USB"]').check();await f.page.locator('#vmGuestGhosts').click();await f.page.locator('#migrateConfirmInput').fill('SRV-DEMO-01');await f.page.locator('#migrateConfirmInput').press('Enter');
  await f.page.waitForFunction(()=>!document.querySelector('#migrateDialog').open);assert.equal(vmwareCalls(f,'guest/remove-ghosts')[0].body.items.length,2);
  Object.assign(f.state.vmware,{running:false,action:''});await f.page.waitForTimeout(1100);
  await f.page.locator('#vmGuestBitLocker').click();assert((await f.page.locator('#migrateDialog').textContent()).includes('recovery'));await f.page.locator('#migrateConfirmInput').fill('SRV-DEMO-01');await f.page.locator('#migrateConfirmInput').press('Enter');
  await f.page.waitForFunction(()=>!document.querySelector('#migrateDialog').open);assert.deepEqual(vmwareCalls(f,'guest/bitlocker').map(c=>c.body),[{items:[],confirm:'SRV-DEMO-01'}]);
  Object.assign(f.state.vmware,{running:false,action:''});await f.page.waitForTimeout(1100);
  f.vm.guest=vmwareGuestFixture('VMware');f.state.vmware.guestSerial=2;await f.page.waitForTimeout(1100);assert(await f.page.locator('#vmGuestTools').isDisabled(),'VMware Tools stays while the VM is on VMware');
  assert(await f.page.locator('#vmGuestDisks').isEnabled());await f.page.locator('input[data-vm-disk="1"]').uncheck();assert(await f.page.locator('#vmGuestDisks').isDisabled());
  f.state.isAdministrator=false;await f.page.waitForTimeout(1100);assert(await f.page.locator('#vmGuestAdmin').isVisible());assert(await f.page.locator('#vmGuestCapture').isDisabled());
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware Before and after: both packages are chosen, compared, and changes to check are separated from expected ones',async()=>{const f=await fixture(browser);try{
  f.state.restore.packages.lastCollection='C:\\Reports\\APP01_20261121';
  await f.page.keyboard.press('Alt+6');await f.page.locator('[data-vmview="compare"]').click();assert(await f.page.locator('#vmCompareRun').isDisabled());
  await f.page.locator('#vmCompareBefore').click();await f.page.waitForFunction(()=>/Before/.test(document.querySelector('#vmCompareBeforeText').textContent));
  await f.page.locator('#vmCompareUseLast').click();assert.equal(await f.page.locator('#vmCompareAfterText').textContent(),'The last collection - C:\\Reports\\APP01_20261121');
  await f.page.locator('#vmCompareRun').click();await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert.deepEqual(vmwareCalls(f,'compare').map(c=>c.body),[{before:'D:\\Packages\\Before',after:'C:\\Reports\\APP01_20261121'}]);assert(await f.page.locator('#vmStop').isVisible());
  f.vm.compare=vmwareCompareFixture();Object.assign(f.state.vmware,{running:false,action:'',compareSerial:1});
  await f.page.waitForFunction(()=>!document.querySelector('#vmCompareResult').classList.contains('hidden'));
  assert.equal(await f.page.locator('#vmCompareFactAttention').textContent(),'2 changes');assert.equal(await f.page.locator('#vmCompareAttention li').count(),2);assert.equal(await f.page.locator('#vmCompareAttention b').count(),0);
  assert((await f.page.locator('#vmCompareAttention').textContent()).includes('State: Running → Stopped'));
  await f.page.locator('tr[data-compare="services"]').click();assert((await f.page.locator('.vm-changes').textContent()).includes('Gone: VMTools (VMware Tools) (expected)'));
  await f.page.setViewportSize({width:1440,height:1000});await f.page.screenshot({path:shots+'/vmware-compare-1440x1000.png'});
  await f.page.locator('#vmCompareReport').click();await f.page.waitForTimeout(200);assert.deepEqual(f.opens,['comparereport']);
  f.state.vmware.compareError='The package from before the move has no 00-Summary folder.';await f.page.waitForTimeout(1100);assert(await f.page.locator('#vmCompareStatus').evaluate(e=>e.classList.contains('error')));
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Disk converter tab: Alt+7 reaches it; Browse checks the disk; the format sets the file name; Convert posts the choice; Stop; everything locks while it runs',async()=>{const f=await fixture(browser);try{
  await f.page.keyboard.press('Alt+7');assert.equal(await f.page.locator('#tabButtonConvert').getAttribute('aria-selected'),'true');assert(await f.page.locator('#tabConvert').isVisible());
  assert(await f.page.locator('#cvStart').isDisabled(),'nothing to convert yet');assert.equal(await f.page.locator('input[name=cvFormat]').count(),7);assert(await f.page.locator('input[name=cvFormat][value=vhdx]').isChecked());
  await f.page.locator('#cvBrowse').click();await f.page.waitForFunction(()=>document.querySelector('#cvInfoFormat').textContent.includes('VMDK'));
  assert.equal(await f.page.locator('#cvSource').inputValue(),'D:\\VMs\\APP01\\APP01-000001.vmdk');assert.equal(await f.page.locator('#cvTarget').inputValue(),'D:\\VMs\\APP01\\APP01-000001.vhdx');
  assert.equal(await f.page.locator('#cvInfoFiles').textContent(),'2 disks in the chain');assert.equal(await f.page.locator('#cvInfoNotes b').count(),0,'notes are text, not markup');
  await f.page.locator('.cv-format[data-format=qcow2]').click();assert.equal(await f.page.locator('#cvTarget').inputValue(),'D:\\VMs\\APP01\\APP01-000001.qcow2');
  await f.page.locator('#cvFolder').click();await f.page.waitForFunction(()=>document.querySelector('#cvTarget').value.startsWith('E:'));assert.equal(await f.page.locator('#cvTarget').inputValue(),'E:\\Hyper-V\\Disks\\APP01-000001.qcow2');
  await f.page.locator('.cv-format[data-format=raw]').click();assert.equal(await f.page.locator('#cvTarget').inputValue(),'E:\\Hyper-V\\Disks\\APP01-000001.img');
  await f.page.locator('#cvVerify').uncheck();
  f.rejectConvert('start','E:\\Hyper-V\\Disks\\APP01-000001.img already exists and was not created by this conversion');await f.page.locator('#cvStart').click();await f.page.waitForFunction(()=>document.querySelector('#appNotice').textContent.includes('already exists'));assert.deepEqual(f.dialogs,[]);f.rejectConvert('start','');
  await f.page.locator('#cvStart').click();await f.page.waitForTimeout(1300);
  const start=f.convertCalls.filter(c=>c.action==='start').pop();assert.deepEqual(start.body,{source:'D:\\VMs\\APP01\\APP01-000001.vmdk',target:'E:\\Hyper-V\\Disks\\APP01-000001.img',format:'raw',verify:false});
  assert.equal(await f.page.locator('#convertTabBadge').textContent(),'Running');assert(await f.page.locator('#cvProgress').isVisible());assert.equal(await f.page.locator('#cvRunTitle').textContent(),'Converting 25%');
  for(const id of ['#cvSource','#cvTarget','#cvBrowse','#cvFolder','#cvStart','input[name=cvFormat] >> nth=0']){assert(await f.page.locator(id).isDisabled(),id+' locked while converting');}
  await f.page.screenshot({path:out+'/convert-running.png'});
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isDisabled(),'collection locks while converting');
  await f.page.keyboard.press('Alt+7');await f.page.locator('#cvStop').click();await f.page.waitForTimeout(1300);assert.equal(f.convertCalls.filter(c=>c.action==='cancel').length,1);assert.equal(await f.page.locator('#cvStop').textContent(),'Stopping\u2026');
  Object.assign(f.state.convert,{running:false,cancelRequested:false,status:'canceled',message:'Stopped. Convert again with the same output file to continue where it stopped.',endedAtUnixMs:Date.now()});await f.page.waitForTimeout(1300);
  assert.equal(await f.page.locator('#cvStart').textContent(),'Continue');assert.equal(await f.page.locator('#convertTabBadge').textContent(),'Stopped');
  Object.assign(f.state.convert,{status:'success',message:'Converted. Every chunk was read back and matches the source.',progress:{pass:1,chunks:200,done:200,bytesRead:21474836480,bytesWritten:21474836480,changed:0,zero:120}});await f.page.waitForTimeout(1300);
  assert.equal(await f.page.locator('#cvRunTitle').textContent(),'Conversion complete');await f.page.locator('#cvOpenFolder').click();await f.page.waitForTimeout(200);assert.deepEqual(f.opens,['convertfolder']);
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isEnabled(),'collection unlocks after converting');
  // A disk too large for the chosen format cannot be started.
  await f.page.keyboard.press('Alt+7');f.setConvertSize(3*1099511627776);await f.page.locator('#cvInspect').click();await f.page.waitForFunction(()=>document.querySelector('#cvInfoSize').textContent.includes('TB'));
  await f.page.locator('.cv-format[data-format=vhd]').click();assert(await f.page.locator('#cvStart').isDisabled());assert((await f.page.locator('#cvSummary').textContent()).includes('holds at most'));
  await f.page.locator('.cv-format[data-format=vhdx]').click();assert(await f.page.locator('#cvStart').isEnabled());
  assert.deepEqual(f.dialogs,[]);assert(f.convertCalls.every(c=>c.token==='workspace-test'));
 }finally{await f.close();}});
 await check('Disk converter tab: fix-ups take the converted VHDX, pick VirtIO drivers for KVM platforms, post the choice, lock while running and show what Windows has',async()=>{const f=await fixture(browser);try{
  await f.page.keyboard.press('Alt+7');await f.page.locator('#cvBrowse').click();await f.page.waitForFunction(()=>document.querySelector('#cvInfoBoot').textContent==='UEFI (GPT)');
  await f.page.locator('#cvFix > summary').click();assert(await f.page.locator('#cvFixApply').isDisabled(),'nothing to fix up yet');
  assert.deepEqual(await f.page.locator('#cvFixTarget option').evaluateAll(o=>o.map(x=>x.value)),['hyperv','scale','nutanix','proxmox','vsphere','physical']);
  Object.assign(f.state.convert,{status:'success',result:{target:'E:\\Hyper-V\\Disks\\APP01.vhdx',format:'vhdx',passes:1}});await f.page.waitForTimeout(1300);
  assert.equal(await f.page.locator('#cvFixPath').inputValue(),'E:\\Hyper-V\\Disks\\APP01.vhdx','the converted VHDX is offered');
  await f.page.locator('#cvFixTarget').selectOption('proxmox');assert.equal(await f.page.locator('#cvFixDriversLabel').textContent(),'VirtIO drivers folder');
  await f.page.locator('#cvFixDriversBrowse').click();await f.page.waitForFunction(()=>document.querySelector('#cvFixDrivers').value==='F:\\');
  await f.page.locator('#cvFixTools').check();await f.page.locator('#cvFixApply').click();await f.page.waitForTimeout(1300);
  const call=f.convertCalls.filter(c=>c.action==='repair').pop();assert.deepEqual(call.body,{mode:'apply',path:'E:\\Hyper-V\\Disks\\APP01.vhdx',target:'proxmox',drivers:['F:\\'],disableVMwareTools:true});
  for(const id of ['#cvFixPath','#cvFixTarget','#cvFixApply','#cvFixUndo','#cvStart','#cvSource']){assert(await f.page.locator(id).isDisabled(),id+' locked while fixing up');}
  assert(!(await f.page.locator('#cvProgress').isVisible()),'the conversion progress is not reused for a fix-up');
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isDisabled(),'collection locks while fixing up');await f.page.keyboard.press('Alt+7');
  Object.assign(f.state.convert,{running:false,status:'success',message:'Fixed up: 3 driver packages added and 4 settings changed. Undo puts them back.',repair:{ok:true,mode:'Apply',boot:{partitionStyle:'GPT',firmware:'uefi',systemPartition:true},
   windows:[{partition:3,productName:'Windows Server 2019 Datacenter',build:17763,architecture:'amd64',drivers:[{name:'viostor',present:true,start:0,file:true},{name:'storahci',present:true,start:0,file:true}],vmwareTools:true,vmwareServices:['VMTools']}],
   findings:[{severity:'good',message:'A VirtIO storage driver is in this Windows installation.'},{severity:'info',message:'<b>not markup</b>'}],applied:{record:'C:\\r.json',drivers:['oem3.inf'],changes:[{key:'ControlSet001\\Services\\storahci',name:'Start',old:3,existed:true,new:0}],overrides:[]}},log:['Opening E:\\Hyper-V\\Disks\\APP01.vhdx for changes.','Set ControlSet001\\Services\\storahci Start to 0 (was 3).']});
  await f.page.waitForTimeout(1300);
  assert.equal(await f.page.locator('#cvFixStatus').textContent(),'Fixed up: 3 driver packages added and 4 settings changed. Undo puts them back.');
  assert.equal(await f.page.locator('#cvFixWindows').textContent(),'Windows Server 2019 Datacenter (build 17763)');assert.equal(await f.page.locator('#cvFixStorage').textContent(),'VirtIO block (starts at boot), SATA (AHCI) (starts at boot)');
  assert.equal(await f.page.locator('#cvFixFindings b').count(),0,'findings are text');assert((await f.page.locator('#cvFixFindings').textContent()).includes('Changed storahci Start to 0.'));
  assert((await f.page.locator('#cvLog').textContent()).includes('storahci Start to 0'));assert(await f.page.locator('#cvFixUndo').isEnabled());
  await f.page.screenshot({path:out+'/convert-fixups.png'});
  f.rejectConvert('repair','No fix-up of this disk was recorded on this computer, so there is nothing to undo.');await f.page.locator('#cvFixUndo').click();await f.page.waitForFunction(()=>document.querySelector('#cvFixStatus').textContent.includes('nothing to undo'));
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Disk converter tab: Send to a host connects (trusting a certificate only on request), sends the password once, needs the typed VM name, posts the VM settings and locks while sending',async()=>{const f=await fixture(browser);try{
  await f.page.keyboard.press('Alt+7');await f.page.locator('#cvBrowse').click();await f.page.waitForFunction(()=>document.querySelector('#cvInfoBoot').textContent==='UEFI (GPT)');
  await f.page.locator('#cvSend > summary').click();
  assert.deepEqual(await f.page.locator('#cvSendPlatform option').evaluateAll(o=>o.map(x=>x.value)),['vsphere','hyperv','proxmox','nutanix','scale']);
  assert.equal(await f.page.locator('#cvSendPath').inputValue(),'D:\\VMs\\APP01\\APP01-000001.vmdk','the checked disk is offered');
  assert(!(await f.page.locator('#cvSendVM').isVisible()),'VM settings wait for a connection');
  await f.page.locator('#cvSendPlatform').selectOption('hyperv');assert(!(await f.page.locator('#cvSendServer').isVisible()),'Hyper-V needs no server');
  assert((await f.page.locator('#cvSendStatus').textContent()).includes('not a Hyper-V host'));
  await f.page.locator('#cvSendPlatform').selectOption('proxmox');assert.equal(await f.page.locator('#cvSendUserLabel').textContent(),'API token ID (user@realm!token)');
  await f.page.locator('#cvSendServer').fill('untrusted.example.com');await f.page.locator('#cvSendUser').fill('migrate@pve!move');await f.page.locator('#cvSendPassword').fill('token-secret');
  await f.page.locator('#cvSendConnect').click();await f.page.waitForFunction(()=>!document.querySelector('#cvSendCert').classList.contains('hidden'));
  assert((await f.page.locator('#cvSendCertText').textContent()).includes('AA:BB:CC'));assert.equal(await f.page.locator('#cvSendPassword').inputValue(),'','the password is cleared after it is sent');
  await f.page.locator('#cvSendServer').fill('pve.example.com');await f.page.locator('#cvSendConnect').click();await f.page.waitForFunction(()=>!document.querySelector('#cvSendVM').classList.contains('hidden'));
  const connects=f.convertCalls.filter(c=>c.action==='connect');assert.equal(connects[0].body.password,'token-secret');assert.equal(connects[1].body.password,'','the password is sent once');assert.equal(connects[0].body.thumbprint,'');
  assert.deepEqual(await f.page.locator('#cvSendImport option').evaluateAll(o=>o.map(x=>x.value)),['local','local-lvm']);
  assert.equal(await f.page.locator('#cvSendNetwork option b').count(),0,'choices are text');assert.equal(await f.page.locator('#cvSendFirmware').inputValue(),'uefi','firmware follows the disk');
  await f.page.locator('#cvSendName').fill('APP01');await f.page.locator('#cvSendStorage').selectOption('local-lvm');await f.page.locator('#cvSendImport').selectOption('local');await f.page.locator('#cvSendNetwork').selectOption('vmbr0');
  await f.page.locator('#cvSendCPUs').fill('4');await f.page.locator('#cvSendMemory').fill('8');await f.page.locator('#cvSendVLAN').fill('20');await f.page.locator('#cvSendSecureBoot').check();await f.page.locator('#cvSendFolderBrowse').click();await f.page.waitForFunction(()=>document.querySelector('#cvSendFolder').value==='E:\\Hyper-V\\Disks');
  assert(await f.page.locator('#cvSendGo').isDisabled(),'sending needs the typed VM name');await f.page.locator('#cvSendConfirm').fill('APP02');assert(await f.page.locator('#cvSendGo').isDisabled());
  await f.page.locator('#cvSendConfirm').fill('APP01');assert(await f.page.locator('#cvSendGo').isEnabled());await f.page.locator('#cvSendGo').click();await f.page.waitForTimeout(1300);
  const sent=f.convertCalls.filter(c=>c.action==='send').pop();
  assert.deepEqual(sent.body,{source:'D:\\VMs\\APP01\\APP01-000001.vmdk',confirm:'APP01',spec:{platform:'proxmox',name:'APP01',cpus:4,memoryMiB:8192,firmware:'uefi',secureBoot:true,tpm:false,windows:true,guestOs:'Microsoft Windows Server 2022',storage:'local-lvm',importStorage:'local',network:'vmbr0',vlan:20,bus:'virtio',nic:'virtio',controller:'',datacenter:'',compute:'',folder:'E:\\Hyper-V\\Disks',thick:false,start:false}});
  assert(!JSON.stringify(sent.body).includes('token-secret'),'the send never carries the secret');
  assert.equal(await f.page.locator('#cvSendTitle').textContent(),'Uploading 25%');
  for(const id of ['#cvSendGo','#cvSendPlatform','#cvSendName','#cvStart','#cvFixApply']){assert(await f.page.locator(id).isDisabled(),id+' locked while sending');}
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isDisabled(),'collection locks while sending');await f.page.keyboard.press('Alt+7');
  await f.page.screenshot({path:out+'/convert-send.png'});
  Object.assign(f.state.convert,{running:false,status:'success',message:'Created APP01 on Proxmox VE. It is off; check its settings, then start it.'});f.state.convert.send=Object.assign({},f.state.convert.send,{result:{platform:'proxmox',vmId:'105',vmName:'APP01'}});await f.page.waitForTimeout(1300);
  assert.equal(await f.page.locator('#cvSendTitle').textContent(),'VM created');assert(await f.page.locator('#cvSendGo').isDisabled(),'the confirmation is cleared after a send');
  await f.page.locator('#cvSendForget').click();await f.page.waitForFunction(()=>document.querySelector('#cvSendVM').classList.contains('hidden'));
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Disk converter tab: This computer lists the local disks for P2V, checks the chosen disk and converts it to a file on another disk',async()=>{const f=await fixture(browser);try{
  await f.page.keyboard.press('Alt+7');await f.page.locator('#cvLocal').click();await f.page.waitForFunction(()=>document.querySelectorAll('.cv-local-disk').length===2);
  assert.equal(await f.page.locator('.cv-local-disk').first().textContent(),'Disk 0 · 128 GB · C: · Windows');
  await f.page.locator('.cv-local-disk').first().click();await f.page.waitForFunction(()=>document.querySelector('#cvInfoFormat').textContent.startsWith('Disk 0'));
  assert.equal(await f.page.locator('#cvSource').inputValue(),'\\\\.\\PhysicalDrive0');assert.equal(await f.page.locator('#cvInfoBoot').textContent(),'UEFI (GPT)');assert(!(await f.page.locator('#cvLocalBox').isVisible()));
  await f.page.locator('#cvFolder').click();await f.page.waitForFunction(()=>document.querySelector('#cvTarget').value.startsWith('E:'));assert.equal(await f.page.locator('#cvTarget').inputValue(),'E:\\Hyper-V\\Disks\\SRV-DEMO-01-PhysicalDrive0.vhdx');
  await f.page.locator('#cvStart').click();await f.page.waitForTimeout(1300);
  const start=f.convertCalls.filter(c=>c.action==='start').pop();assert.equal(start.body.source,'\\\\.\\PhysicalDrive0');assert.equal(start.body.format,'vhdx');
  assert(await f.page.locator('#cvLocal').isDisabled(),'the disk list locks while converting');assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Low-resolution windows (800x600 and 1024x768 screens) keep every workspace reachable and legible',async()=>{const f=await fixture(browser);try{
  for(let i=0;i<40;i++)f.logs.push('[INFO] Message '+i);
  f.state.restore={status:'ready',packages:{lastCollection:'C:\\Reports\\SRV-DEMO-01',loadedReport:''},packagePath:'C:\\Reports\\SRV-DEMO-01',scan:restoreScanFixture(),items:[],logs:[]};
  f.state.migrate=Object.assign(migrateIdle(),{status:'cutover',message:'Cutover complete. The shares are live on this server and match the old ones.',source:'FS-OLD-07',discovery:migrateDiscoveryFixture(),prechecked:true,canCopy:true,canCutover:true,workPath:'C:\\Migrations\\FS-OLD-07',
   space:[{drive:'D:',neededBytes:268435456000,freeBytes:400e9,ok:true}],logs:[{index:0,text:'2026-11-12 10:00:00 [INFO] Final copy before creating the shares.'}],
   plan:[{name:'Finance',destName:'Finance',destPath:'D:\\Shares\\Finance',status:'done',message:'120,345 file(s) match.',percent:0,processed:0,expected:0,precheck:{ok:true,problems:[],warnings:['Permissions name local accounts of the old server.'],files:120345,bytes:214748364800},previouslySynced:true,lastSyncUnixMs:Date.now(),createdShare:true,verified:true},
         {name:'Projects',destName:'Projects',destPath:'D:\\Shares\\Projects',status:'copying',message:'Copying changes.',percent:40,processed:2000,expected:5000,precheck:{ok:true,problems:[],warnings:[],files:5000,bytes:53687091200},previouslySynced:true,lastSyncUnixMs:Date.now(),createdShare:false,verified:false}]});
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();f.vm.sizing=vmwareSizingFixture(false);f.vm.guest=vmwareGuestFixture('Hyper-V');f.vm.compare=vmwareCompareFixture();
  Object.assign(f.state.vmware,{status:'connected',server:'vc01.example.com',account:'readonly@vsphere.local',message:'Read 4 VM(s) on 3 host(s) from vc01.example.com.',summary:vmwareSummaryFixture(),inventorySerial:1,guestSerial:1,compareSerial:1,outputFolder:'C:\\Reports\\VMware_vc01',reportPath:'C:\\Reports\\VMware_vc01\\VMware-Readiness-Report.html'});
  // Viewports left by 800x600, 1024x768 and 1366x768 screens after the taskbar and window frame.
  for(const size of [{width:784,height:522},{width:1008,height:690},{width:1350,height:690}]){for(const withUpdate of [false,true]){
   f.state.update=withUpdate?{status:'available',title:'Update available',message:'Installed 3.2.10. Latest 99.0.0.',available:true,canCheck:true,canDownload:true,notes:['One','Two','Three']}:{status:'current',title:'Up to date',message:'Installed.',canCheck:true};
   await f.page.setViewportSize(size);await f.page.waitForTimeout(1100);const label=JSON.stringify(size)+' update='+withUpdate;
   const inView=async(selector,minHeight)=>{const box=await f.page.locator(selector).boundingBox();assert(box&&box.x>=0&&box.y>=0&&box.x+box.width<=size.width+1&&box.y+box.height<=size.height+1&&box.height>=minHeight,label+' '+selector+' '+JSON.stringify(box));};
   assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),label);await inView('#helpMenuButton',20);
   await f.page.locator('#tabButtonSetup').click();await inView('#startButton',30);await inView('#saveChoicesButton',30);await inView('#configBody',withUpdate?150:200);
   await f.page.locator('#tabButtonCollection').click();await inView('#logView',90);await inView('#logSearch',24);await inView('#progressValue',10);
   await f.page.locator('#tabButtonReport').click();await inView('#emptyLoadPreviousButton',24);await inView('#collapseReportHeaderButton',24);
   const smallText=()=>f.page.evaluate(()=>Array.from(document.querySelectorAll('body *')).filter(e=>e.offsetParent&&e.childNodes.length&&Array.from(e.childNodes).some(n=>n.nodeType===3&&n.nodeValue.trim())&&parseFloat(getComputedStyle(e).fontSize)<10.5).map(e=>e.className||e.id||e.tagName));
   assert.deepEqual(await smallText(),[],label+' report text below 10.5px');
   await f.page.locator('#tabButtonRestore').click();await f.page.locator('#restoreBody').evaluate(e=>e.scrollTop=0);await inView('#restoreReviewButton',28);await inView('#restoreBody',withUpdate?110:150);await inView('#restoreBrowse',24);
   const registry=f.page.locator('.restore-component[data-component="Registry"]');await registry.scrollIntoViewIfNeeded();await registry.locator('input[data-restore-component]').check();if(!await registry.locator('details').evaluate(e=>e.open))await registry.locator('summary').click();
   assert.deepEqual(await smallText(),[],label+' restore text below 10.5px');assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),label+' restore overflow');
   if(size.width===784&&!withUpdate){await f.page.locator('#restoreBody').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:out+'/restore-800x600.png'});}
   await f.page.locator('#restoreReviewButton').click();await inView('#restoreConfirmInput',24);await inView('#restoreConfirmButton',28);assert.deepEqual(await smallText(),[],label+' restore dialog text below 10.5px');
   if(size.width===784&&!withUpdate){await f.page.screenshot({path:out+'/restore-review-800x600.png'});}
   await f.page.locator('#restoreDialogCancel').click();assert(!await f.page.locator('#restoreDialog').isVisible());await registry.locator('input[data-restore-component]').uncheck();
   await f.page.locator('#tabButtonMigrate').click();await f.page.locator('#migrateBody').evaluate(e=>e.scrollTop=0);await inView('#migrateBody',withUpdate?110:150);await inView('#migrateOpenLogs',24);await inView('#migrateSource',24);
   await f.page.locator('#migrateLogBlock summary').evaluate(e=>{if(!e.parentNode.open)e.click();});
   for(const target of ['#migrateCheck','#migrateCopy','#migrateCutover','#migrateAliasCheck','#migrateLog']){await f.page.locator(target).scrollIntoViewIfNeeded();await inView(target,target==='#migrateLog'?60:24);}
   assert.deepEqual(await smallText(),[],label+' migrate text below 10.5px');assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),label+' migrate overflow');
   assert(await f.page.locator('#migrateBody').evaluate(e=>e.scrollWidth<=e.clientWidth+1),label+' migrate panel overflow');
   if(size.width===784&&!withUpdate){await f.page.locator('#migrateBody').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:out+'/migrate-800x600.png'});}
   await f.page.locator('#migrateCutover').scrollIntoViewIfNeeded();await f.page.locator('#migrateCutover').click();await inView('#migrateConfirmInput',24);await inView('#migrateConfirmButton',28);assert.deepEqual(await smallText(),[],label+' migrate dialog text below 10.5px');
   await f.page.locator('#migrateDialogCancel').click();assert(!await f.page.locator('#migrateDialog').isVisible());
   await f.page.locator('#tabButtonVmware').click();await f.page.locator('[data-vmview="env"]').click();await f.page.locator('#vmwareBody').evaluate(e=>e.scrollTop=0);await inView('#vmwareBody',withUpdate?110:150);await inView('#vmServer',24);
   for(const view of ['env','plan','guest','disks','compare']){await f.page.locator('[data-vmview="'+view+'"]').click();await inView('[data-vmview="'+view+'"]',24);
    for(const target of {env:['#vmConnect','#vmTableBody tr.vm-row'],plan:['#vmSuggest','#vmWriteRunbook'],guest:['#vmGuestCapture','#vmGuestApply','#vmGuestTools'],disks:['#vmReceiveStart','#vmSendFind','#vmSendStart'],compare:['#vmCompareRun','#vmCompareBody tr']}[view]){await f.page.locator(target).first().scrollIntoViewIfNeeded();await inView(target+':visible >> nth=0',20);}
    assert.deepEqual(await smallText(),[],label+' vmware '+view+' text below 10.5px');assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),label+' vmware overflow');assert(await f.page.locator('#vmwareBody').evaluate(e=>e.scrollWidth<=e.clientWidth+1),label+' vmware panel overflow '+view);}
   if(size.width===784&&!withUpdate){await f.page.locator('[data-vmview="env"]').click();await f.page.locator('#vmwareBody').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:out+'/vmware-800x600.png'});}
   await f.page.locator('#tabButtonConvert').click();await inView('#cvStart',28);await inView('#cvSource',24);await inView('#cvBrowse',24);
   for(const target of ['.cv-format[data-format=raw]','#cvTarget','#cvVerify']){await f.page.locator(target).scrollIntoViewIfNeeded();await inView(target,12);}
   assert.deepEqual(await smallText(),[],label+' converter text below 10.5px');assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),label+' converter overflow');
   if(size.width===784&&!withUpdate){await f.page.locator('.cv-body').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:out+'/convert-800x600.png'});}
   const small=await f.page.evaluate(()=>Array.from(document.querySelectorAll('body *')).filter(e=>e.offsetParent&&e.childNodes.length&&Array.from(e.childNodes).some(n=>n.nodeType===3&&n.nodeValue.trim())&&parseFloat(getComputedStyle(e).fontSize)<10.5).map(e=>e.className||e.id||e.tagName));assert.deepEqual(small,[],label+' text below 10.5px');
   if(size.width===784&&!withUpdate){await f.page.locator('#tabButtonCollection').click();await f.page.screenshot({path:out+'/collection-800x600.png'});await f.page.locator('#tabButtonSetup').click();await f.page.screenshot({path:out+'/setup-800x600.png'});}
  }}
 }finally{await f.close();}});
 report.status='passed';
 }finally{await browser.close();}})().catch(e=>{report.status='failed';report.error=e.stack;console.error(e);process.exitCode=1;}).finally(()=>fs.writeFileSync(out+'/results.json',JSON.stringify(report,null,2)));
