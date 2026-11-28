/* Actual application HTML and production CSP, synthetic state only. */
const fs=require('fs'),http=require('http'),assert=require('assert'),crypto=require('crypto');
assert.equal(crypto.createHash('sha256').update(fs.readFileSync('tests/fixtures/index-v3.2.9.html')).digest('hex'),'b7145c6b9d0a83949606d10d3671815ba7e43e5f894bca24df6430c8c1ced609','Published negative-control fixture changed');
const {chromium}=require('playwright');
const out='build/workspace-validation';fs.mkdirSync(out,{recursive:true});
const report={status:'running',checks:[]};
const policy=fs.readFileSync('src/app/main.go','utf8').match(/applicationPageCSP\s*=\s*"([^"]+)"/)[1];
const args=process.argv.slice(2),executable=args.includes('--executable')?args[args.indexOf('--executable')+1]:null;
// A tiny solid-colour PNG for the fake console stream.
function solidPNG(w,h,rgb){const zlib=require('zlib'),crcTable=[];for(let n=0;n<256;n++){let c=n;for(let k=0;k<8;k++)c=c&1?0xedb88320^(c>>>1):c>>>1;crcTable[n]=c>>>0;}
 const crc=b=>{let c=0xffffffff;for(const x of b)c=crcTable[(c^x)&255]^(c>>>8);return (c^0xffffffff)>>>0;};
 const chunk=(type,data)=>{const len=Buffer.alloc(4);len.writeUInt32BE(data.length);const td=Buffer.concat([Buffer.from(type),data]);const c=Buffer.alloc(4);c.writeUInt32BE(crc(td));return Buffer.concat([len,td,c]);};
 const ihdr=Buffer.alloc(13);ihdr.writeUInt32BE(w,0);ihdr.writeUInt32BE(h,4);ihdr[8]=8;ihdr[9]=2;
 const rows=[];for(let y=0;y<h;y++){rows.push(0);for(let x=0;x<w;x++)rows.push(...rgb);}
 return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),chunk('IHDR',ihdr),chunk('IDAT',zlib.deflateSync(Buffer.from(rows))),chunk('IEND',Buffer.alloc(0))]).toString('base64');}
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
function vmwareIdle(){return {running:false,action:'',status:'idle',message:'',server:'',account:'',certificate:null,summary:null,inventorySerial:0,outputFolder:'',reportPath:'',runbookPath:'',planCsvPath:'',guestSerial:0,guestMessage:'',guestError:false,guestWorkPath:'C:\\Users\\admin\\AppData\\Local\\Server Inventory Configuration Manager\\Guest-Migration',compareSerial:0,compareError:'',sendSerial:0,send:null,sendError:'',hvBuild:{plan:null,revision:'',steps:[],results:{},saved:[],serial:0,here:'ADMIN01'},receiver:{running:false,port:44380,sessions:0,firewall:false,defaultFolder:'D:\\Migration\\VMware-Copies\\Received'},cancelRequested:false,logs:[],startedAtUnixMs:0,endedAtUnixMs:0};}
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
// Synthetic rolling-conversion report, shaped like analyzeRolling in vmscenario.go.
function vmRollingFixture(plan){const st=(plan.rolling&&plan.rolling.stages)||[],hosts=['esx01.example.com','esx02.example.com','esx03.example.com'],side=(h,status,notes)=>({status,hosts:h,vms:2,memoryBytes:34359738368,capacityBytes:500e9,vcpus:8,cores:64,notes});
 const ds=(plan.storage||[]).find(x=>x.source==='DS-SSD-01')||{};
 return {source:'VMware',status:st.length?'tight':'over',hosts:hosts.map((n,i)=>({name:n,cluster:'Prod-Cluster',cores:32,memoryBytes:549755813888,vms:2,vmMemoryBytes:34359738368,nics:4,managementIp:'192.0.2.2'+(i+1)})),
  storage:[{datastore:'DS-SSD-01',type:'VMFS',capacityBytes:4398046511104,usedBytes:2748779069440,kind:'san',localTo:'',reusable:true,reuse:ds.reuse!==false,advice:'Shared LUN: it can become target storage once it is empty.'}],
  stages:st.map((s,i)=>({number:i+1,host:s.host,wave:s.wave,moveFirst:!!s.moveFirst||!s.host,waveVms:s.wave===1?['APP01']:[],source:side(hosts.filter(h=>h!==s.host),'ok',['Fits: 32 GB of 1 TB memory in use with a host kept free (N+1).']),target:side(s.host?[s.host]:[],i===0?'tight':'ok',[i===0?'Only 1 target host: no host is free for a failure (N+1) at this point.':'Fits.']),localVms:[],steps:['Put the host in maintenance mode so DRS moves its running VMs.','Install Windows Server on '+(s.host||'')+'.'],status:i===0?'tight':'ok',notes:s.notes?[s.notes]:[]})),
  notes:st.length?['Reused LUNs are formatted for the target, so the first target node needs storage that is already empty.']:['No stages yet. Suggest stages drafts one per host; change it as you like.'],unstaged:[]};}
function vmwareSizingFixture(configured){return configured?{vms:3,vcpus:14,memoryBytes:90194313216,provisionedBytes:386547056640,usedBytes:193273528320,usableNodes:2,cores:64,vcpuPerCore:0.2,memoryPercent:18,storagePercent:9,provisionedPercent:18,status:'ok',notes:['The VMs fit with one node kept free.'],configured:true}:
 {vms:3,vcpus:14,memoryBytes:90194313216,provisionedBytes:386547056640,usedBytes:193273528320,usableNodes:0,cores:0,vcpuPerCore:0,memoryPercent:0,storagePercent:0,provisionedPercent:0,status:'unknown',notes:['Describe the new cluster to check that the VMs fit.'],configured:false};}
function vmwareGuestFixture(platform){const adapter=(name,mac,index,addresses,isStatic)=>({Name:name,Description:'Microsoft Hyper-V Network Adapter',MacAddress:mac,InterfaceIndex:index,Status:'Up',Dhcp:!isStatic,Static:isStatic,Addresses:addresses.map(a=>({Address:a,PrefixLength:24,Family:'IPv4',Origin:isStatic?'Manual':'Dhcp'})),Gateways:isStatic?[{NextHop:'192.0.2.1',Metric:0,Family:'IPv4'}]:[],DnsServers:['192.0.2.53']});
 const capture={Schema:1,CapturedAt:'2026-11-20T08:00:00.000Z',Findings:[{Severity:'info',Message:'Recorded static IP settings for 1 adapter(s). <b>not markup</b>'},{Severity:'good',Message:'Hyper-V storage and network drivers are built into this Windows version.'}],
  State:{ComputerName:'SRV-DEMO-01',Adapters:[Object.assign(adapter('Ethernet0','00-50-56-00-00-01',4,['192.0.2.10'],true),{Description:'vmxnet3 Ethernet Adapter'})],RunningAutoServices:['Spooler','VMTools','W3SVC']}};
 return {Schema:1,Capture:capture,RollbackAvailable:false,MissingAutoServices:['W3SVC'],MissingAddresses:['192.0.2.10'],
  TargetChecks:[{Target:'hyperv',Severity:'good',Message:'Hyper-V drivers are built in.'},{Target:'nutanix',Severity:'warning',Message:'VirtIO drivers are not installed.'}],
  Activation:{Known:true,Channel:'MAK',Before:'MAK',Licensed:false,Status:'Notification',Severity:'warning',Message:'Windows uses a MAK key. Activate now activates it again over the internet, which uses one activation from the MAK\'s count. Do not activate a test VM. On a Hyper-V host running Windows Server Datacenter, this VM can use AVMA instead: no count is used and no internet is needed.',CanActivate:true,AvmaKey:'WWVGQ-PNHV9-B89P4-8GGM9-9HPQ4',AvmaEdition:'2025 Standard'},
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
const hvBuildFixture={"plan":{"schema":1,"name":"Prod","source":"vcenter.example.com","sourceCluster":"Prod","nodes":[{"name":"HV1.example.com","esxi":"esx01.example.com","switches":{"SET-vSwitch0":["SLOT 1 Port 1","SLOT 1 Port 2"],"SET-vSwitch1":["SLOT 2 Port 1","SLOT 2 Port 2"]},"hostIps":{"Cluster":"10.0.31.21/24","LiveMigration":"10.0.30.21/24"},"storage":[{"adapter":"SLOT 3 Port 1","ip":"10.0.40.21/24"},{"adapter":"SLOT 3 Port 2","ip":"10.0.41.21/24"}]},{"name":"HV2.example.com","esxi":"esx02.example.com","switches":{"SET-vSwitch0":["SLOT 1 Port 1","SLOT 1 Port 2"],"SET-vSwitch1":["SLOT 2 Port 1","SLOT 2 Port 2"]},"hostIps":{"Cluster":"10.0.31.22/24","LiveMigration":"10.0.30.22/24"},"storage":[{"adapter":"SLOT 3 Port 1","ip":"10.0.40.22/24"},{"adapter":"SLOT 3 Port 2","ip":"10.0.41.22/24"}]}],"switches":[{"name":"SET-vSwitch0","from":["vSwitch0"],"managementOs":true,"managementVlan":"","loadBalancing":"HyperVPort","mtu":1500,"uplinks":2},{"name":"SET-vSwitch1","from":["vSwitch1"],"managementOs":false,"managementVlan":"","loadBalancing":"HyperVPort","mtu":9000,"uplinks":2}],"hostNics":[{"name":"LiveMigration","switch":"SET-vSwitch1","vlan":"30","role":"livemigration","from":"vMotion"},{"name":"Cluster","switch":"SET-vSwitch0","vlan":"","role":"cluster","from":""}],"networks":[{"portgroup":"VM Network","sourceVlan":"0","switch":"SET-vSwitch0","vlan":"","vms":4},{"portgroup":"Servers-20","sourceVlan":"20","switch":"SET-vSwitch0","vlan":"20","vms":9}],"storage":{"kind":"iscsi","mpio":true,"portals":["10.0.40.5:3260","10.0.41.5:3260"],"targetFilter":"","jumbo":true,"arrays":null,"disks":[{"uniqueId":"60002AC0000000000000000000012345","label":"CSV-SAN-01","fileSystem":"ReFS","use":"csv","sizeBytes":0,"datastore":"SAN-01"},{"uniqueId":"60002AC0000000000000000000012346","label":"Witness","fileSystem":"NTFS","use":"witness","sizeBytes":0,"datastore":""}]},"cluster":{"enabled":true,"name":"HVCLU01","addresses":["192.0.2.30"],"witness":"disk","witnessShare":"","cloudAccount":"","storageTests":true},"settings":{"migrationAuth":"Kerberos","maxMigrations":2,"maxStorageMigrations":2,"performance":"Compression","vmPath":"","vhdPath":"C:\\ClusterStorage\\Volume1\\Hyper-V"},"sizing":[{"datastore":"NFS-ISO","type":"NFS","capacityBytes":1099511627776,"usedBytes":0,"suggestedBytes":274877906944,"label":"CSV-NFS-ISO","vms":0,"note":"NFS: Hyper-V keeps VMs on a Cluster Shared Volume or an SMB 3 share instead."},{"datastore":"SAN-01","type":"VMFS","capacityBytes":4398046511104,"usedBytes":3298534883328,"suggestedBytes":4123168604160,"label":"CSV-SAN-01","vms":12,"note":""}],"notes":["Give each node's iSCSI initiator name (shown after Read nodes) access to the LUNs on the array, the same way the ESXi hosts have it.","The VMware cluster uses EVC mode intel-broadwell. Use nodes with the same processor generation, or turn on processor compatibility on VMs that must live migrate between different ones.","DRS balances the VMware cluster; Hyper-V clusters balance VMs with VM load balancing, which Windows Server 2016 and later turn on by default.","ESXi uses DNS servers 192.0.2.10. The Windows nodes must be joined to the domain and use its DNS servers before the cluster is created.","ESXi keeps time from time.example.com. Domain-joined nodes take time from the domain.","The 2 ESXi hosts have 64 cores and 1.0 TB of memory in all. Plan and runbook sizes the new cluster against the VMs."],"updatedAt":"2026-11-26T03:36:09.82061879Z"},"revision":"5ca425eef755be1e0433659c77b14c888c300e62912a2d7c5ec7e3df0bbf7651","steps":[{"id":"localtools","title":"Cluster tools on this computer","detail":"The Failover Cluster Module for Windows PowerShell, which the cluster steps use from here.","where":"here","missing":""},{"id":"roles","title":"Install roles","detail":"Hyper-V and its PowerShell module, Failover Clustering, Multipath I/O.","where":"each","missing":""},{"id":"restart","title":"Restart nodes that need it","detail":"Only nodes that wait for a restart after installing roles; one at a time.","where":"restart","missing":""},{"id":"network","title":"Virtual switches and host adapters","detail":"Switches SET-vSwitch0, SET-vSwitch1; host adapters LiveMigration, Cluster.","where":"each","missing":""},{"id":"storage","title":"Connect storage","detail":"iSCSI: the initiator service, the portals 10.0.40.5:3260, 10.0.41.5:3260 and persistent sessions with multipath.","where":"each","missing":""},{"id":"disks","title":"Prepare disks","detail":"Initialize and format 2 empty disk(s) on the first node. Disks that hold partitions are never changed.","where":"first","missing":""},{"id":"validate","title":"Validate the cluster","detail":"Runs Microsoft's cluster validation and saves its report. Changes nothing.","where":"here","missing":""},{"id":"cluster","title":"Create the cluster","detail":"Creates HVCLU01 without storage, or adds missing nodes to it.","where":"here","missing":""},{"id":"csv","title":"Cluster Shared Volumes","detail":"Adds the prepared disks to the cluster and makes them Cluster Shared Volumes.","where":"here","missing":""},{"id":"quorum","title":"Quorum witness","detail":"A disk witness on the shared storage.","where":"here","missing":""},{"id":"clusternetworks","title":"Cluster networks","detail":"Names the cluster networks after the host adapters, sets their roles and keeps live migration on its own network.","where":"here","missing":""},{"id":"hostsettings","title":"Live migration and default folders","detail":"Kerberos authentication, 2 at a time, Compression.","where":"each","missing":""}]};
const hvBuildStepCount=hvBuildFixture.steps.length;
// An existing one-node cluster read through HV-HOST01 and the plan drafted
// to join it: esx02 is rebuilt with Windows, esx03 still waits on ESXi.
const hvBuildExistingRead={kind:'join',names:['HVCLU01'],readFrom:'HV-HOST01.example.com',at:'2026-11-27T10:00:00Z',template:{computer:'HV-HOST01',caption:'Windows Server 2022 Datacenter',build:20348,domain:'example.com',partOfDomain:true,hyperV:true,switches:[{name:'SETswitch',type:'External',embedded:true,adapters:['NIC1','NIC2'],speedsMb:[25000,25000]}],hostNics:[{name:'LM',switch:'SETswitch',vlan:30,ips:['10.0.30.11/24']}],storage:{portals:['10.0.40.5:3260'],mpio:true},warnings:[]},cluster:{name:'HVCLU01',nodes:[{name:'HV-HOST01',state:'Up'}],addresses:['192.0.2.30'],witnessType:'Disk',csvs:[{name:'Cluster Disk 1',freeBytes:1099511627776}],vms:5}};
const hvBuildJoinPlan={schema:1,name:'Join HVCLU01',source:'vcenter.example.com',sourceCluster:'Prod',nodes:[{name:'HV2.example.com',esxi:'esx02.example.com',switches:{},hostIps:{},storage:[]},{name:'',esxi:'esx03.example.com',later:true,switches:{},hostIps:{},storage:[]}],switches:[{name:'SETswitch',from:['vSwitch0'],existing:true,managementOs:true,managementVlan:'',loadBalancing:'HyperVPort',mtu:1500,uplinkSpeedsMb:[25000,25000]}],hostNics:[{name:'LM',switch:'SETswitch',vlan:'30',role:'livemigration'}],networks:[],storage:{kind:'iscsi',mpio:true,portals:['10.0.40.5:3260'],targetFilter:'',jumbo:false,arrays:null,disks:[]},cluster:{enabled:true,join:true,name:'HVCLU01',addresses:[],witness:'disk',witnessShare:'',cloudAccount:'',storageTests:false},settings:{migrationAuth:'Kerberos',maxMigrations:2,maxStorageMigrations:2,performance:'Compression',vmPath:'',vhdPath:''},sizing:[],notes:['HV-HOST01 is already in HVCLU01 and is not changed.'],existing:{kind:'join',name:'HVCLU01',readFrom:'HV-HOST01.example.com',members:[{name:'HV-HOST01',state:'Up'}],witness:'Disk witness',hostIps:{LM:'10.0.30.11/24'},readAt:'2026-11-27T10:00:00Z'}};
const hvBuildNodeFacts={ok:true,facts:{computer:'HV2',caption:'Windows Server 2022 Datacenter',build:20348,productType:3,partOfDomain:true,domain:'example.com',cores:32,memoryBytes:274877906944,virtualization:true,features:{'Hyper-V':'Installed','Failover-Clustering':'Available'},adapters:[{name:'Embedded 1',status:'Up',mac:'00-15-5D-00-00-01',speedBits:1000000000,ips:['192.0.2.52/24'],gateway:true,vlanId:0},{name:'SLOT 1 Port 1',status:'Up',mac:'00-15-5D-00-01-01',speedBits:25000000000,ips:[]},{name:'SLOT 1 Port 2',status:'Up',mac:'00-15-5D-00-01-02',speedBits:25000000000,ips:[]},{name:'SLOT 3 Port 1',status:'Up',mac:'00-15-5D-00-03-01',speedBits:10000000000,ips:['10.0.40.22/24']}],switches:[],hostNics:[],disks:[],initiator:'iqn.1991-05.com.microsoft:hv2.example.com',warnings:[]}};
async function fixture(browser,old=false){
 let saved=null,started=[],saveCalls=0,rejectStart=false,skipRequests=[],opens=[],restoreReject=null,migrateReject={},vmwareReject={};const receiverPair={allowed:false};
 const logs=[],dialogs=[],errors=[],restoreCalls={browse:0,scan:[],start:[],cancel:0,tokens:[]},migrateCalls=[],vmwareCalls=[],convertCalls=[],replCalls=[],planCalls=[],serverCalls=[];const servers={rows:[],summary:{total:0,states:{},scheduled:0,methods:{}},bulk:{running:false,action:'',total:0,results:[]},hyperVHere:true,isAdministrator:true,local:{hyperV:true,computer:'HV01',running:false,folder:'',defaultFolder:'C:\\ClusterStorage\\Volume1\\Hyper-V\\Received',port:44380,addresses:['192.0.2.40']}};const plans={plans:[],inventory:{open:true,server:'vc01.example.com',serverType:'vCenter Server',live:true}};let convertReject={},convertSize=0;const pickCalls=[],outputRootCalls=[],profileCalls=[],profiles=[];let pickReject='';
 const consoleCalls=[],consoleStreams=[],consoleOpt={needSignIn:'',caps:null};const uiCalls=[],vddkCalls=[],channelCalls=[],productionCalls=[];let vddk={folder:'',ready:false,loaded:false,message:'Off. Copies of running VMware VMs read whole disks through vSphere\'s export when the datastore refuses them.'};const state={ui:{schema:1,confirmStyle:'typed'},status:'idle',phase:'Ready',running:false,computerName:'SRV-DEMO-01',signedInAccount:'EXAMPLE\\operator',isAdministrator:true,powerShellVersion:'5.1',defaultOutputRoot:'C:\\Reports',update:{status:'current',title:'Up to date',message:'Installed v3.2.10. Latest v3.2.10.',canCheck:true},
  restore:{status:'idle',packages:{lastCollection:'',loadedReport:''},items:[],logs:[]},migrate:migrateIdle(),vmware:vmwareIdle(),convert:convertIdle()};
 const vm={inventory:null,plan:null,sizing:null,guest:null,compare:null};
 const rollbackCalls=[];
 const sqlCalls=[];
 const sqlFix=sqlFixture();
 const failover={failovers:[],copies:[{name:'APP01-disk0.vhdx',size:107374182400,complete:true,verified:false,round:'a1@2026-11-25T14:19:23Z',job:'a1',recoveryPoint:'2026-11-25T14:19:23Z',updated:'2026-11-25T14:20:00Z',busy:false,generation:2,machine:{name:'APP01',cpus:6,memoryMB:12288,firmware:'uefi',adapters:2}},{name:'APP01-disk1.vhdx',size:536870912000,complete:true,verified:false,round:'a1@2026-11-25T14:19:23Z',job:'a1',recoveryPoint:'2026-11-25T14:19:23Z',updated:'2026-11-25T14:20:00Z',busy:false,generation:1},{name:'SQL01-disk0.vhdx',size:107374182400,complete:false,verified:false,updated:'2026-11-25T14:20:00Z',busy:true,generation:1}]};
 const targetTest={running:false,log:[],error:'',tests:[]};
 const repl={ok:true,pushing:false,pushLog:[],pushResults:null,serial:0,isAdministrator:true,defaultPort:44390,agents:[],summary:{agents:0,unreachable:0,jobs:0,healthy:0,behind:0,failing:0,paused:0,cutOver:0,alerts:0}};
 const html=fs.readFileSync(old?'tests/fixtures/index-v3.2.9.html':'src/app/index.html','utf8').replaceAll('__TOKEN__','workspace-test').replaceAll('__APP_VERSION__','3.2.10').replaceAll('__COLLECTOR_VERSION__','3.2.10');
 const server=http.createServer(async(req,res)=>{
  const url=new URL(req.url,'http://localhost');
  if(url.pathname==='/'){res.writeHead(200,{'Content-Type':'text/html','Content-Security-Policy':policy});res.end(html);return;}
  const send=(code,data)=>{res.writeHead(code,{'Content-Type':'application/json'});res.end(JSON.stringify(data));};
  if(url.pathname==='/api/state'){const since=Number(url.searchParams.get('since')||0);send(200,{...state,collectionPreferences:saved,logs:logs.slice(since).map((text,i)=>({index:since+i,text})),nextLogIndex:logs.length});return;}
  if(url.pathname==='/api/update/channel'){
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   let body='';for await(const chunk of req)body+=chunk;const data=JSON.parse(body);channelCalls.push(data);
   const test=data.channel==='test';
   state.update={...state.update,channel:data.channel,stableVersion:'3.2.10',testVersion:test?'3.3.0-test.2':'',available:test,canDownload:test,latestVersion:test?'3.3.0-test.2':'3.2.10',status:test?'available':'current',title:test?'Test build available':'Up to date',message:test?'Installed 3.2.10. Test build 3.3.0-test.2 is available (you chose the Test channel).':'Installed 3.2.10.'};
   send(200,{ok:true,update:state.update});return;
  }
  if(url.pathname==='/api/vddk'){
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   if(req.method==='GET'){send(200,{ok:true,vddk});return;}
   let body='';for await(const chunk of req)body+=chunk;const data=JSON.parse(body);vddkCalls.push(data);
   if(data.folder==='C:\\Bad'){send(400,{ok:false,error:'Not saved: zlib1.dll: its signature is not valid.'});return;}
   vddk=data.folder?{folder:data.folder,ready:true,version:'8.0.3 build 23950268',signer:'Broadcom Inc',loaded:false,message:'VDDK 8.0.3 build 23950268 (signed by Broadcom Inc) is ready. Copies of running VMware VMs read only changed blocks through it.'}:{folder:'',ready:false,loaded:false,message:'Off.'};
   send(200,{ok:true,vddk});return;
  }
  if(url.pathname==='/api/production'){
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   let body='';for await(const chunk of req)body+=chunk;const data=JSON.parse(body);productionCalls.push(data);
   if(data.stopPercent>data.reservePercent){send(400,{ok:false,error:'The stop level must not be above the free space kept for production.'});return;}
   state.production=Object.assign({},state.production||{},{settings:data,limit:'set'});send(200,{ok:true,production:state.production});return;
  }
  if(url.pathname==='/api/ui-settings'){
   let body='';for await(const chunk of req)body+=chunk;
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   const data=JSON.parse(body);uiCalls.push(data);
   if(!['none','tick','prompt','typed'].includes(data.confirmStyle)){send(400,{ok:false,error:'Choose how changes are confirmed: none, tick, prompt or typed.'});return;}
   state.ui={schema:1,confirmStyle:data.confirmStyle};send(200,{ok:true,ui:state.ui});return;
  }
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
   if(action==='discover'){Object.assign(m,{running:true,action:'discover',status:'discovering',message:'Connecting to '+data.source+'.',source:data.source,account:data.user||'',target:data.target||'',targetName:data.target?String(data.target).split('.')[0].toUpperCase():'',targetAccount:data.targetUser||''});send(202,{ok:true});return;}
   if(action==='autosync'){m.autoSyncMinutes=data.minutes;m.nextAutoSyncUnixMs=data.minutes?Date.now()+data.minutes*60000:0;send(200,{ok:true});return;}
   if(action==='plan'){m.plan=data.shares.map(s=>({...s,status:'planned',message:'',percent:0,processed:0,expected:0,precheck:null,previouslySynced:false,lastSyncUnixMs:0,createdShare:false,verified:false}));m.prechecked=false;m.canCopy=false;m.canCutover=false;send(200,{ok:true});return;}
   if(action==='precheck'){Object.assign(m,{running:true,action:'precheck',status:'checking'});send(202,{ok:true});return;}
   if(action==='sync'){Object.assign(m,{running:true,action:'sync',status:'copying'});send(202,{ok:true});return;}
   if(action==='cutover'){Object.assign(m,{running:true,action:'cutover',status:'cuttingover'});send(202,{ok:true});return;}
   if(action==='cancel'){m.cancelRequested=true;send(202,{ok:true});return;}
   if(action==='reset'){state.migrate=migrateIdle();send(200,{ok:true});return;}
   if(action==='browse'){send(200,data.parent?{ok:true,selected:true,path:'E:\\'}:{ok:true,selected:true,path:'F:\\Data\\'+data.share+'-New'});return;}
   send(202,{ok:true});return;
  }
  if(url.pathname.startsWith('/api/hvbuild/')){
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{},action='hvbuild/'+url.pathname.slice(13);vmwareCalls.push({action,method:req.method,body:data,token:req.headers['x-app-token']});
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   if(vmwareReject[action]){send(400,{error:vmwareReject[action]});return;}
   const v=state.vmware,hb=v.hvBuild=v.hvBuild||{plan:null,revision:'',steps:[],results:{},saved:[],serial:0,here:'ADMIN01'};
   if(action==='hvbuild/suggest'){send(200,{ok:true,plan:JSON.parse(JSON.stringify(data.existing?hvBuildJoinPlan:hvBuildFixture.plan))});return;}
   if(action==='hvbuild/readexisting'){Object.assign(v,{running:true,action:'hvbuild',status:'hvbuilding'});send(202,{ok:true});return;}
   if(action==='hvbuild/save'){Object.assign(hb,{plan:data.plan,revision:hvBuildFixture.revision,steps:JSON.parse(JSON.stringify(hvBuildFixture.steps)),saved:[data.plan.name],serial:hb.serial+1});send(200,{ok:true,plan:data.plan});return;}
   if(action==='hvbuild/read'){Object.assign(v,{running:true,action:'hvbuild',status:'hvbuilding'});send(202,{ok:true});return;}
   if(action==='hvbuild/preview'){Object.assign(v,{running:true,action:'hvbuild',status:'hvbuilding'});send(202,{ok:true});return;}
   if(action==='hvbuild/apply'){if(data.confirm!==hb.plan.name){send(400,{error:'Type the plan\'s name to approve.'});return;}Object.assign(v,{running:true,action:'hvbuild',status:'hvbuilding'});send(202,{ok:true});return;}
   if(action==='hvbuild/networkmap'){send(200,{ok:true,changed:2});return;}
   if(action==='hvbuild/account'){hb.user=data.forget?'':data.user;hb.serial++;send(200,{ok:true});return;}
   send(404,{});return;
  }
  if(url.pathname.startsWith('/api/hvremote/')){
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{},action='hvremote/'+url.pathname.slice(14);vmwareCalls.push({action,method:req.method,body:data,token:req.headers['x-app-token']});
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   if(vmwareReject[action]){send(400,{error:vmwareReject[action]});return;}
   const v=state.vmware;
   if(action==='hvremote/connect'){Object.assign(v,{running:true,action:'hvconnect',status:'hvconnecting'});send(202,{ok:true});return;}
   if(action==='hvremote/forget'){v.hvRemote={connected:false,serial:((v.hvRemote&&v.hvRemote.serial)||0)+1};send(200,{ok:true});return;}
   if(action==='hvremote/move'){Object.assign(v,{running:true,action:'hvmove',status:'moving'});send(202,{ok:true,node:data.node});return;}
  }
  if(url.pathname.startsWith('/api/receivers/')){
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{},action='receivers/'+url.pathname.slice(15);vmwareCalls.push({action,method:req.method,body:data,token:req.headers['x-app-token']});
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   if(action==='receivers/find'){send(200,{ok:true,receivers:data.host==='hv09.example.com'?[]:[{name:'HV01',port:44380,thumbprint:'3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e',folder:true,disks:0,freeBytes:4398046511104,version:'3.2.34',pairing:true,fingerprint:'3F9A 6C0E 1B7D 4A25',seen:['192.0.2.40:44380']},{name:'MINWINPC',port:44380,thumbprint:'ab'.repeat(32),folder:false,disks:2,diskBytes:322122547200,pairing:false,fingerprint:'ABAB ABAB ABAB ABAB',seen:['192.0.2.51:44380']}]});return;}
   if(action==='receivers/reach'){send(200,{ok:true,reachable:(data.addresses||[]).filter(a=>a.startsWith('192.0.2.'))});return;}
   if(action==='receivers/pair'){if(!data.id){send(200,{ok:true,id:'0f'.repeat(16),check:'123 456',state:'pending'});return;}send(200,receiverPair.allowed?{ok:true,state:'allowed',code:'K7QMZ-2RT4X-9WBNP-H3VDA'}:{ok:true,state:'pending'});return;}
   send(404,{});return;
  }
  if(url.pathname.startsWith('/api/vmware/')){
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{},action=url.pathname.slice(12);vmwareCalls.push({action,method:req.method,body:data,token:req.headers['x-app-token']});
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   if(vmwareReject[action]){send(400,{error:vmwareReject[action]});return;}
   const v=state.vmware;
   if(action==='inventory'){send(200,vm.inventory?{ok:true,serial:v.inventorySerial,inventory:vm.inventory,plan:vm.plan,sizing:vm.sizing,attention:vm.attention||[]}:{ok:true,inventory:null});return;}
   if(action==='hyperv'){Object.assign(v,{running:true,action:'hyperv',status:'connecting'});send(202,{ok:true});return;}
   if(action==='guest-auto'){const rem=!!(data.remember||(v.guestAuto&&v.guestAuto.rememberOn));v.guestAuto={user:data.user,hasPassword:true,remembered:rem,rememberOn:rem};send(200,{ok:true,user:data.user});return;}
   if(action==='guest-auto/forget'){v.guestAuto={user:'',hasPassword:false,rememberOn:!!(v.guestAuto&&v.guestAuto.rememberOn)};send(200,{ok:true});return;}
   if(action==='preflight/run'){const p=v.preflight||{};v.preflight=Object.assign(p,{running:true,user:data.user||p.user||'',hasPassword:!!data.user||!!p.hasPassword,items:(data.items||[]).map(i=>({id:i.id,state:'waiting',summary:''})),message:'Checking.'});send(202,{ok:true});return;}
   if(action==='preflight/forget'){v.preflight=Object.assign(v.preflight||{},{user:'',hasPassword:false});send(200,{ok:true});return;}
   if(action==='guest-auto/run'){(v.fixups||[]).forEach(x=>{if(x.vm===data.vm){x.state='waiting';x.message='Waiting for Windows in '+x.vm+' to start.';x.steps=[];}});send(202,{ok:true});return;}
   if(action==='remember'){const r=vm.remember||(vm.remember={enabled:false,entries:[]});if(req.method==='POST'){if(typeof data.enabled==='boolean'){r.enabled=data.enabled;if(!data.enabled){r.entries=[];}}if(data.forget){r.entries=r.entries.filter(e=>e.server!==data.forget);}}send(200,{ok:true,remember:r});return;}
   if(action==='connect'){Object.assign(v,{running:true,action:'connect',status:'connecting',server:data.server,account:data.user,certificate:null});send(202,{ok:true});return;}
   if(action==='plan'){vm.plan=JSON.parse(JSON.stringify(data.plan));if(data.suggest){vm.plan.vms.forEach((p,i)=>{if(p.manual&&!data.resetManual){return;}p.manual=false;p.hold=i===2;p.wave=i===2?0:(i===3?1:2);p.why=i===2?'Blocked for Hyper-V.':(i===3?'Powered off, so nobody is using it.':'A running server with no special role in its name.');});vm.plan.waves=[{wave:1,title:'Pilot',reason:'Powered-off, test and development VMs go first.'},{wave:2,title:'General servers',reason:'Running servers with no special role in their names.'}];vm.plan.waveSize=data.waveSize||2;}if(vm.plan.scenario==='rolling'){vm.plan.rolling=vm.plan.rolling||{autoCapacity:true,stages:[],sourceCluster:''};if(!vm.plan.rolling.sourceCluster){vm.plan.rolling.sourceCluster='Prod-Cluster';}if(data.suggestStages){vm.plan.rolling.stages=['esx03.example.com','esx01.example.com','esx02.example.com'].map((h,i)=>({host:h,wave:i+1,moveFirst:i===2,notes:''}));}if(vm.plan.rolling.autoCapacity){Object.assign(vm.plan.capacity,{nodes:3,coresPerNode:32,memoryGbPerNode:512,usableStorageTb:4,reserveNodes:1});}}vm.sizing=['azure','aws'].includes(vm.plan.target)?Object.assign(vmwareSizingFixture(false),{status:'quota',notes:['Check the subscription quota covers 14 vCPUs.']}):vmwareSizingFixture(!!vm.plan.capacity.coresPerNode);send(200,{ok:true,plan:vm.plan,sizing:vm.sizing,rolling:vm.plan.scenario==='rolling'?vmRollingFixture(vm.plan):null,saved:true});return;}
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
   if(action==='receiver/pair'){(v.receiver.pairRequests||[]).splice(0);send(200,{ok:true});return;}
   if(action==='receiver'&&data.action==='close-other'){if(data.pid!==4321){send(409,{ok:false,error:'wrong process'});return;}v.receiverHeld=false;send(200,{ok:true});return;}
   if(action==='receiver'&&data.action==='start'&&v.receiverHeld){send(400,{ok:false,error:'Port 44390 is held by another copy of this application (process 4321, started 03:12) that is still running, perhaps without a window. Close it, then start the receiver.',otherInstance:{pid:4321,port:44390,started:'2026-11-26T08:12:00Z'}});return;}
   if(action==='receiver'&&/^service-/.test(data.action)){if(v.receiver){v.receiver.serviceInstalled=data.action==='service-on';}send(200,{ok:true});return;}
   if(action==='receiver'){if(data.action==='start'){v.receiver={running:true,port:data.port||44380,code:'K7QMZ-2RT4X-9WBNP-H3VDA',thumbprint:'3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e',folder:data.folder||'D:\\Migration\\VMware-Copies\\Received',addresses:['192.0.2.40'],sessions:0,firewall:!!data.firewall,fingerprint:'3F9A 6C0E 1B7D 4A25',discoverable:data.discoverable!==false,pairRequests:[],defaultFolder:'D:\\Migration\\VMware-Copies\\Received'};}else{v.receiver={running:false,port:44380,sessions:0,firewall:false,defaultFolder:'D:\\Migration\\VMware-Copies\\Received',message:'Stopped.'};}send(200,{ok:true});return;}
   if(action==='agent/disks'){send(200,{ok:true,computer:'APP01',disks:[{number:0,size:107374182400,system:true,volumes:[{name:'v1',mounts:[],fileSystem:'FAT32',length:104857600},{name:'v2',mounts:['C:\\'],fileSystem:'NTFS',length:106300000000}]},{number:1,size:536870912000,system:false,volumes:[{name:'v3',mounts:['D:\\'],fileSystem:'NTFS',length:536700000000}]}]});return;}
   if(action==='agent/receiverdisks'){send(200,{ok:true,folder:false,disks:[{name:'disk-0',label:'Disk 0, QEMU HARDDISK',size:107374182400,sectorSize:512},{name:'disk-1',label:'Disk 1, QEMU HARDDISK',size:214748364800,sectorSize:512}]});return;}
   if(action==='agent/send'){Object.assign(v,{running:true,action:'send',status:'sending'});send(202,{ok:true});return;}
   if(action==='guest'){send(200,{ok:true,serial:v.guestSerial,result:vm.guest});return;}
   if(action.startsWith('guest/')){Object.assign(v,{running:true,action:'guest-'+action.slice(6)});send(202,{ok:true});return;}
   if(action==='compare/browse'){send(200,{ok:true,selected:true,path:'D:\\Packages\\'+(data.side==='before'?'Before':'After'),computer:'APP01',collectedAt:data.side==='before'?'2026-11-19 22:00':'2026-11-21 07:30'});return;}
   if(action==='compare'&&req.method==='GET'){send(200,Object.assign({ok:true,serial:v.compareSerial},vm.compare||{result:null}));return;}
   if(action==='compare'){Object.assign(v,{running:true,action:'compare'});send(202,{ok:true});return;}
   if(action==='cancel'){v.cancelRequested=true;send(202,{ok:true});return;}
   send(200,{ok:true,selected:false});return;
  }
  if(url.pathname.startsWith('/api/repl/')){
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{},action=url.pathname.slice(10);replCalls.push({action,method:req.method,body:data,token:req.headers['x-app-token']});
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   if(action==='state'){send(200,repl);return;}
   if(action==='local'){send(200,{ok:true,selected:true,path:'C:\\ClusterStorage\\Volume2\\Replicas'});return;}
   if(action==='failover'&&req.method==='GET'){send(200,Object.assign({ok:true,hyperV:true,folder:'D:\\Migration\\VMware-Copies\\Received',receiving:true},failover,url.searchParams.get('switches')==='1'?{switches:[{name:'Isolated test',type:'Private'},{name:'LAN',type:'External'}]}:{}));return;}
   if(action==='failover'&&data.action==='start'){failover.failovers=[{id:'f1',mode:data.request.mode,name:data.request.name,vmId:'6f1d4c3a-0000-4000-8000-000000000001',copies:data.request.copies,recoveryPoint:'2026-11-25T14:19:23Z',started:'2026-11-25T14:30:00Z'}];failover.copies.forEach(c=>{if(data.request.copies.includes(c.name))c.held='test failover '+data.request.name;});send(200,{ok:true});return;}
   if(action==='targettest'&&req.method==='GET'){const t=(state.vmware&&state.vmware.target)||{};send(200,Object.assign({ok:true,connected:!!t.connected,platform:t.platform||'',server:t.server||'',user:t.user||'',node:t.node||''},targetTest));return;}
   if(action==='targettest'&&data.action==='start'){Object.assign(targetTest,{running:true,log:['14:30:00  Pausing APP01 when its current round finishes, so the copy is one whole recovery point.']});send(200,{ok:true});return;}
   if(action==='targettest'&&data.action==='end'){targetTest.tests=[];targetTest.log=['14:40:00  The test APP01-TEST ended: its VM and disks were deleted. The replica was not touched.'];send(200,{ok:true});return;}
   if(action==='failover'&&data.action==='end'){failover.failovers=[];failover.copies.forEach(c=>{delete c.held;});send(200,{ok:true});return;}
   if(action==='push'){Object.assign(repl,{pushing:true,pushLog:['14:02:10  Installing the agent on 2 servers.','14:02:11  app01.example.com: copying the agent.']});send(202,{ok:true});return;}
   if(action==='agent'&&data.action==='disks'){send(200,{ok:true,disks:[{id:'0',size:107374182400,system:true,volumes:[{name:'v1',mounts:[],fileSystem:'FAT32',length:104857600},{name:'v2',mounts:['C:\\'],fileSystem:'NTFS',length:106300000000}]},{id:'1',size:536870912000,system:false,volumes:[{name:'v3',mounts:['D:\\','E:\\'],fileSystem:'NTFS',length:536700000000,kind:'spanned'}]}]});return;}
   if(action==='agent'&&data.action==='receiverdisks'){const alt=(data.alternates||[]).find(a=>a.startsWith('198.51.100.'));send(200,{ok:true,from:data.id?'agent':'console',address:alt||data.address,folder:false,disks:[{name:'disk-0',label:'Disk 0, QEMU HARDDISK',size:107374182400},{name:'disk-1',label:'Disk 1, QEMU HARDDISK',size:536870912000}]});return;}
   if(action==='job'&&data.action==='save'){const now=new Date();const job={spec:Object.assign({},data.spec,{id:'a1b2c3d4e5f60718',receiver:{address:data.spec.receiver.address+':44380',thumbprint:data.spec.receiver.thumbprint}}),status:{state:'idle',message:'Up to date as of 14:20:05 UTC. Next round at 14:21 UTC.',rounds:2,recoveryPoint:new Date(now-95000).toISOString(),lastStart:now.toISOString(),nextRun:new Date(+now+60000).toISOString(),lastDurationSeconds:42,lastSent:268435456,totalSent:161061273600,disks:data.spec.disks.map((d,i)=>({source:d.source,target:d.target,size:i?536870912000:107374182400,passes:2,chunks:100,done:100,sent:134217728,verified:false}))},rpoSeconds:95,hasCode:true};repl.agents[0].live.jobs.push(job);repl.summary.jobs++;repl.summary.healthy++;send(200,{ok:true,job});return;}
   if(action==='job'&&data.action==='log'){send(200,{ok:true,lines:['2026-11-25 14:18:00  Round 1 finished in 41 minutes; recovery point 2026-11-25 13:37:00.','2026-11-25 14:20:05  Round 2 finished in 42 seconds; recovery point 2026-11-25 14:19:23.']});return;}
   send(200,{ok:true});return;
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
  if(url.pathname.startsWith('/api/console/')){
   const action=url.pathname.slice(13);
   if(action==='stream'){
    if(url.searchParams.get('token')!=='workspace-test'){send(403,{});return;}
    res.writeHead(200,{'Content-Type':'text/event-stream','Cache-Control':'no-store'});
    const ev=(name,data)=>res.write('event: '+name+'\ndata: '+JSON.stringify(data)+'\n\n');
    ev('size',{w:640,h:480});ev('tiles',[{x:0,y:0,png:solidPNG(64,64,[30,200,90])}]);ev('state',consoleStreams.view);
    const entry={id:url.searchParams.get('id'),ev};consoleStreams.push(entry);req.on('close',()=>{const i=consoleStreams.indexOf(entry);if(i>=0)consoleStreams.splice(i,1);});return;
   }
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{};consoleCalls.push({action,body:data,token:req.headers['x-app-token']});
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   if(action==='open'){
    if(consoleOpt.needSignIn){send(400,{ok:false,needSignIn:consoleOpt.needSignIn,error:'Sign in to vCenter or ESXi in Discovery first; the console uses that session.'});return;}
    const caps=consoleOpt.caps||{display:'live',keyboard:true,mouse:true,typeText:true,clipToGuest:true,clipFrom:true,clipFromWhy:'Text the VM copies arrives here when VMware Tools passes it on.',filesTo:true,filesFrom:true,filesHow:'Through VMware Tools, with an account in the VM.',native:['vmrc'],notes:['Nothing is installed in the VM.']};
    consoleStreams.view={id:'cn1',target:data,platform:'VMware',where:'esx01.example.com (WebMKS)',caps,status:'connected',message:'',typing:false};
    send(200,{ok:true,session:consoleStreams.view});return;
   }
   if(action==='file'){send(202,{ok:true});const view=Object.assign({},consoleStreams.view,{file:{direction:data.direction,status:'done',bytes:2048,message:'Copied 2 KB.'}});consoleStreams.view=view;consoleStreams.forEach(e=>e.ev('state',view));return;}
   if(action==='ocr'){const png=Buffer.from(data.png||'','base64');const ok=png.length>24&&png.readUInt32BE(0)===0x89504e47;send(200,ok?{ok:true,text:'Setup log: '+png.readUInt32BE(16)+'x'+png.readUInt32BE(20),language:'en-US'}:{ok:false,error:'The picture of the screen is not a PNG.'});return;}
   if(action==='text'){send(200,data.guest?{ok:true,text:'C:\\> ipconfig\nIPv4 Address. . . : 192.0.2.25'}:{ok:false,error:'Type the path of the text file in the VM.'});return;}
   if(action==='close'){const view=Object.assign({},consoleStreams.view,{status:'closed',message:'The console was closed.'});consoleStreams.forEach(e=>e.ev('state',view));send(200,{ok:true});return;}
   send(200,{ok:true});return;
  }
  if(url.pathname==='/api/servers'){
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{};serverCalls.push({method:req.method,body:data,token:req.headers['x-app-token']});
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   if(req.method==='GET'){send(200,Object.assign({ok:true},servers));return;}
   if(data.action==='cutover'&&data.confirm!==String(data.keys.length)){send(400,{ok:false,error:'Type '+data.keys.length+' to confirm.'});return;}
   servers.bulk={running:false,action:data.action,total:data.keys.length,results:data.keys.map(k=>({key:k,name:(servers.rows.find(r=>r.key===k)||{}).name,ok:true,text:'Done.'}))};send(202,{ok:true,count:data.keys.length});return;
  }
  if(url.pathname==='/api/profiles'){
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{};profileCalls.push({method:req.method,body:data});
   if(req.method==='GET'){send(200,{ok:true,profiles});return;}
   if(data.action==='save'){if(!data.profile.name){send(400,{ok:false,error:'Name the defaults.'});return;}const i=profiles.findIndex(p=>p.name.toLowerCase()===data.profile.name.toLowerCase());if(i>=0){profiles.splice(i,1);}profiles.push(data.profile);profiles.sort((x,y)=>x.name.localeCompare(y.name));send(200,{ok:true,profiles});return;}
   if(data.action==='delete'){const i=profiles.findIndex(p=>p.name===data.name);if(i<0){send(404,{ok:false,error:'Those defaults no longer exist.'});return;}profiles.splice(i,1);send(200,{ok:true,profiles});return;}
   send(400,{ok:false,error:'Unknown action.'});return;
  }
  if(url.pathname==='/api/plans'){
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{};planCalls.push({method:req.method,body:data,token:req.headers['x-app-token']});
   if(req.method==='GET'){send(200,Object.assign({ok:true,sweeping:false,blocked:'',hyperVHere:true,testing:false,waves:{},switches:[{name:'Isolated',type:'Private'}]},plans));return;}
   if(data.action==='sync'){send(200,{ok:true,sync:plans.syncAnswer||{queued:(data.vmIds&&data.vmIds.length)||1}});return;}
   if(data.action==='create'){plans.plans.push({id:'p1',name:data.name,server:'vc01.example.com',paused:false,settings:data.settings,throttle:'no limit',summary:{total:data.vmIds.length,waiting:data.vmIds.length,copying:0,ready:0,testing:0,scheduled:0,done:0,failed:0},vms:data.vmIds.map(id=>({id,name:id.replace('vm-','').toUpperCase(),wave:0,state:'waiting',seeded:false,failures:0}))});send(200,{ok:true,id:'p1'});return;}
   send(200,{ok:true});return;
  }
  if(url.pathname==='/api/output-root'){let body='';for await(const chunk of req)body+=chunk;const data=JSON.parse(body);outputRootCalls.push(data);if(data.outputRoot==='Q:\\NoSuchDrive'){send(400,{ok:false,error:'Reports cannot be saved there: synthetic failure.'});return;}state.defaultOutputRoot=data.outputRoot;send(200,{ok:true,outputRoot:data.outputRoot,saved:true});return;}
  if(url.pathname==='/api/pick'){let body='';for await(const chunk of req)body+=chunk;const data=JSON.parse(body);pickCalls.push(data);if(pickReject){send(400,{ok:false,error:pickReject});return;}send(200,{ok:true,selected:true,path:data.kind==='remote'?'C:\\ClusterStorage\\Volume1\\Picked':data.kind==='file'?'E:\\Media\\receiver.iso':'E:\\Picked'});return;}
  if(url.pathname.startsWith('/api/sql/')){
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   const action=url.pathname.slice(9);let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{};sqlCalls.push({action,body:data});
   if(action==='state'){send(200,{ok:true,sql:sqlFix});return;}
   if(action==='read'){sqlFix.read=sqlReadView();sqlFix.message='Read OLDSQL and NEWSQL.';send(202,{ok:true});return;}
   if(action==='check'){send(200,{ok:true,findings:[{db:'Sales',severity:'warning',code:'compat',message:'Compatibility level 110 stays as it is on the new server.'}]});return;}
   if(action==='checkfolder'){send(200,{ok:true,message:'Both SQL Server services can use '+data.backupPath+'.'});return;}
   if(action==='create'){if(data.confirm!==data.name){send(400,{ok:false,error:'Confirm the plan\'s name to start it.'});return;}sqlFix.plans.push(sqlPlanFixture('q2',data.name,data.databases.map(d=>Object.assign(sqlDbFixture(d.name,d.method,'waiting'),{wave:d.wave}))));send(200,{ok:true,plan:'q2',findings:[]});return;}
   if(action==='action'){const p=sqlFix.plans.find(x=>x.id===data.plan);if(!p){send(404,{ok:false,error:'That SQL plan no longer exists.'});return;}
    if(['cutover','schedule','rollback'].includes(data.action)&&data.confirm!==String(data.names.length)){send(400,{ok:false,error:'Confirm '+data.names.length+'.'});return;}
    if(data.action==='cutover'){p.databases.filter(d=>data.names.includes(d.name)).forEach(d=>{d.state='cutting';d.step='Backing up the tail of the log';});}
    if(data.action==='test'){p.databases.filter(d=>data.names.includes(d.name)).forEach(d=>{d.test={name:d.name+'_TEST',id:7,created:'2026-11-28T10:00:00Z'};});}
    if(data.action==='delete'){if(data.confirm!==p.name){send(400,{ok:false,error:'Confirm the plan name.'});return;}sqlFix.plans=sqlFix.plans.filter(x=>x!==p);}
    send(200,{ok:true});return;}
   send(200,{ok:true});return;
  }
  if(url.pathname.startsWith('/api/rollback/')){
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   const action=url.pathname.slice('/api/rollback/'.length);
   if(action==='guides'){send(200,{ok:true,guides:[{id:'movevms',title:'Move VMs, Migration plans and New migration (VMware or Hyper-V to Hyper-V)',automatic:'yes',covers:'VMware and Hyper-V VMs moved to Hyper-V.',steps:['Use Roll back beside the VM below.'],warnings:['Anything written on the new machine since the cutover is not on the old one.']},{id:'shares',title:'Move file shares',automatic:'partly',covers:'Shares moved to a new file server.',steps:['In Move file shares, use Undo read-only on the old server.','Point users back to the old server.'],warnings:[]}]});return;}
   let body='';for await(const chunk of req)body+=chunk;const data=JSON.parse(body||'{}');rollbackCalls.push({action,body:data});
   if(action==='run'){const rb=state.vmware.rollback,r=rb&&rb.records.find(x=>x.id===data.id);if(r&&r.dc&&!data.dcConfirmed){send(400,{ok:false,error:'Tick the box to confirm you understand the domain controller warning.'});return;}if(r){r.state='running';rb.running=r.id;rb.serial++;}send(202,{ok:true});return;}
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
 const url='http://127.0.0.1:'+server.address().port+'/';
 // A fixture that fails to load closes its own server and context; otherwise Node never exits and the job hangs until its timeout.
 try{await page.goto(url);await page.waitForFunction(()=>document.querySelector('#serverName').textContent==='SRV-DEMO-01');}catch(e){await context.close().catch(()=>{});server.close();throw new Error('Fixture page did not load ('+errors.concat(dialogs).join('; ')+'): '+e.message);}
 return {sqlCalls,sqlFix,consoleCalls,consoleStreams,consoleOpt,page,context,state,profiles,profileCalls,rollbackCalls,uiCalls,vddkCalls,channelCalls,productionCalls,pickCalls,outputRootCalls,rejectPick(message){pickReject=message},vm,repl,plans,planCalls,servers,serverCalls,failover,receiverPair,targetTest,replCalls,vmwareCalls,convertCalls,rejectConvert(action,message){convertReject[action]=message},setConvertSize(size){convertSize=size},rejectVmware(action,message){vmwareReject[action]=message},logs,dialogs,errors,started,skipRequests,opens,restoreCalls,migrateCalls,rejectMigrate(action,message){migrateReject[action]=message},rejectRestore(message){restoreReject=message},get saved(){return saved},get saveCalls(){return saveCalls},reject(){rejectStart=true},async close(){assert.deepEqual(errors,[]);await context.close();await new Promise(r=>server.close(r));}};
}
// Move SQL databases: a synthetic read of two servers and one plan.
function sqlDbFixture(name,method,state){return {name,method,wave:0,state,facts:{name},nextTry:'0001-01-01T00:00:00Z',seeded:state==='ready'||state==='done'?'2026-11-28T08:00:00Z':'0001-01-01T00:00:00Z',lastRound:state==='ready'?'2026-11-28T09:45:00Z':'0001-01-01T00:00:00Z',cutoverAt:'0001-01-01T00:00:00Z',cutAt:state==='done'?'2026-11-28T09:00:00Z':'0001-01-01T00:00:00Z',testedAt:'0001-01-01T00:00:00Z',rolledBackAt:'0001-01-01T00:00:00Z'};}
function sqlPlanFixture(id,name,dbs){return {id,name,created:'2026-11-28T07:00:00Z',source:{server:'OLDSQL',auth:'current'},target:{server:'NEWSQL',auth:'current'},backupPath:'\\\\OLDSQL\\E$\\Backup\\SIMM',dataPath:'D:\\Data',logPath:'L:\\Logs',syncMinutes:15,concurrency:2,startAt:'0001-01-01T00:00:00Z',paused:false,objects:{logins:true,jobs:true},alias:{name:'sql-app',zone:'corp.example.com',dnsServer:'DC1',spn:true,atCutover:true},databases:dbs,objectsAt:'2026-11-28T07:01:00Z',objectsNote:'Created 12 logins and 3 jobs (turned off until cutover).',objectItems:[{kind:'login',name:'CORP\\app-svc',action:'created'}]};}
function sqlInstanceFixture(server,dbs){return {server,machine:server,instanceName:'',serverName:server,version:'15.0.4385.2',major:15,edition:'Standard Edition (64-bit)',hadr:false,dataPath:'D:\\Data',logPath:'L:\\Logs',backupPath:'E:\\Backup',serviceAccount:'CORP\\sql-svc',dnsDomain:'corp.example.com',sysadmin:true,databases:dbs,readAt:'2026-11-28T09:30:00Z'};}
function sqlReadView(){
 const dbs=[{name:'Sales',state:'ONLINE',recovery:'FULL',dataMB:20480,logMB:2048},{name:'Archive',state:'ONLINE',recovery:'SIMPLE',dataMB:512,logMB:64},{name:'Orders',state:'ONLINE',recovery:'FULL',dataMB:4096,logMB:512,ag:'AG1'},{name:'master',state:'ONLINE',recovery:'SIMPLE',dataMB:6,logMB:2}];
 return {source:sqlInstanceFixture('OLDSQL',dbs),target:sqlInstanceFixture('NEWSQL',[]),
  choices:{Sales:{method:'logship',why:'Full recovery model: log shipping keeps the copy minutes behind, so the cutover is short.',available:['backup','logship'],unavailable:{dag:'It is not in an availability group.'}},Archive:{method:'backup',why:'Simple recovery model: backed up and restored at cutover.',available:['backup'],unavailable:{logship:'The simple recovery model keeps no log backups.',dag:'It is not in an availability group.'}},Orders:{method:'dag',why:'It is in the availability group AG1.',available:['backup','logship','dag'],unavailable:{}}},
  reasons:{master:{off:'System database: its logins, jobs and linked servers move as instance objects.'}},
  objects:[{kind:'login',name:'CORP\\app-svc',action:'create'},{kind:'login',name:'sa',action:'skip',detail:'Built in'},{kind:'linked',name:'ERP',action:'manual',detail:'Its password cannot be read.'}],
  config:[{name:'max degree of parallelism',old:'4',new:'0',default:true}],counts:{logins:12,jobs:3,linked:1,operators:1},findings:[],
  suggest:{backupPath:'\\\\OLDSQL\\E$\\Backup\\SIMM',backupWhy:'The old server\'s own backup folder through its administrative share.',dataPath:'D:\\Data',logPath:'L:\\Logs',pathWhy:'The new server\'s default data and log folders.',syncMinutes:15,syncWhy:'Every 15 minutes.',concurrency:2,aliasZone:'corp.example.com',objectsWhy:'Logins keep their SIDs and passwords.',disableJobs:true,disableJobWhy:'The old jobs are turned off at cutover.'},errors:{}};
}
function sqlFixture(){return {plans:[sqlPlanFixture('q1','Finance SQL',[sqlDbFixture('Payroll','logship','ready'),Object.assign(sqlDbFixture('Ledger','backup','failed'),{error:'The backup of Ledger failed: Operating system error 5 (Access is denied.) on \\\\OLDSQL\\E$\\Backup\\SIMM. Give the SQL Server service account write access to the folder and try again; nothing else was changed on either server.'})])],running:{},read:sqlReadView(),reading:false,message:'',failed:false,held:[],dnsUser:'',logs:['10:00:00  Finance SQL: shipped the log of Payroll (2 MB).'],ready:{},methods:{backup:'Backup and restore',logship:'Log shipping',dag:'Distributed availability group'},signedInAccount:'CORP\\admin',limits:{minSync:5,maxSync:1440}};}
// Tools live in two tabs: Discovery (Alt+1) and Migration and replication (Alt+5).
const discoveryTools=['server','environment'];
// apartControls finds controls pulled apart on wide windows: a tick box or
// radio stretched wider than itself, or one standing far from its own text.
function apartControls(page){return page.evaluate(()=>{
 const bad=[],root=document.querySelector('.tab-panel.active');if(!root){return ['no active tab'];}
 const name=e=>e.tagName.toLowerCase()+(e.id?'#'+e.id:'')+(e.getAttribute('data-cfm-tick')!==null?'[tick '+e.getAttribute('data-cfm-tick')+']':'')+(e.parentElement&&typeof e.parentElement.className==='string'&&e.parentElement.className?' in .'+e.parentElement.className.trim().split(/\s+/)[0]:'');
 for(const i of root.querySelectorAll('input[type=checkbox],input[type=radio]')){
  if(!i.getClientRects().length||getComputedStyle(i).visibility==='hidden'||getComputedStyle(i).opacity==='0'||i.closest('.toggle')){continue;}
  const r=i.getBoundingClientRect();if(r.width>24){bad.push(name(i)+' is '+Math.round(r.width)+' px wide');}
  const lab=i.closest('label');if(!lab){continue;}
  const w=document.createTreeWalker(lab,NodeFilter.SHOW_TEXT);let t,gap=null;
  while((t=w.nextNode())){if(!t.textContent.trim()){continue;}if(!(i.compareDocumentPosition(t)&Node.DOCUMENT_POSITION_FOLLOWING)){continue;}const rg=document.createRange();rg.selectNodeContents(t);const rr=rg.getClientRects()[0];if(!rr){continue;}if(Math.abs((rr.top+rr.bottom)/2-(r.top+r.bottom)/2)<rr.height){gap=rr.left-r.right;}break;}
  if(gap!==null&&gap>32){bad.push(name(i)+' stands '+Math.round(gap)+' px from its label');}
 }
 // Controls that touch: side by side on one line with less than 4 px
 // between them (an input joined to its Browse button is one control).
 const vis=e=>{if(!e.getClientRects().length){return false;}const d=e.closest('details:not([open])');if(d&&!e.closest('summary')){return false;}const cs=getComputedStyle(e);return cs.visibility!=='hidden'&&cs.display!=='none'&&cs.opacity!=='0';};
 const ctl='button,select,input:not([type=checkbox]):not([type=radio]):not([type=hidden]),textarea,label.cfm-tick,label.vm-check,label.vm-field';
 const items=[...root.querySelectorAll(ctl)].filter(e=>vis(e)&&!e.closest('.toggle,.path-pick,.path-row,.tool-nav,.nw-stepper,[role=tablist],.vm-inv-views,.seg,.btn-group,.mp-summary,.nw-tiles,.vm-tiles,.status-track,.gd-choices,.nw-choices')&&!(e.matches('label.vm-field')&&e.querySelector(ctl))).filter(e=>!(e.matches('input,select,textarea')&&e.closest('label.vm-field,label.vm-check,label.cfm-tick')));
 const shown=e=>{let r=e.getBoundingClientRect(),L=r.left,T=r.top,R=r.right,B=r.bottom;for(let p=e.parentElement;p&&p!==document.documentElement;p=p.parentElement){const cs=getComputedStyle(p);if(cs.overflowX!=='visible'||cs.overflowY!=='visible'){const q=p.getBoundingClientRect();L=Math.max(L,q.left);T=Math.max(T,q.top);R=Math.min(R,q.right);B=Math.min(B,q.bottom);}}return {left:L,top:T,right:R,bottom:B,width:R-L,height:B-T};};
 const boxes=items.map(e=>({e,r:shown(e)})).filter(b=>b.r.width>4&&b.r.height>4);
 for(let a=0;a<boxes.length;a++){for(let b=0;b<boxes.length;b++){if(a===b){continue;}const A=boxes[a].r,B=boxes[b].r;
  const overlapY=Math.min(A.bottom,B.bottom)-Math.max(A.top,B.top);if(overlapY<Math.min(A.height,B.height)*0.5){continue;}
  const gap=B.left-A.right;if(gap>=-1&&gap<4&&!boxes[a].e.contains(boxes[b].e)&&!boxes[b].e.contains(boxes[a].e)){bad.push(name(boxes[a].e)+' touches '+name(boxes[b].e)+' ('+Math.round(gap)+' px)');}
  const overlapX=Math.min(A.right,B.right)-Math.max(A.left,B.left);if(overlapX>2&&overlapY>2&&a<b&&!boxes[a].e.contains(boxes[b].e)&&!boxes[b].e.contains(boxes[a].e)){bad.push(name(boxes[a].e)+' overlaps '+name(boxes[b].e));}
 }}
 // Bordered boxes stacked with no space between them.
 const boxed=e=>{const cs=getComputedStyle(e);return parseFloat(cs.borderTopWidth)>0&&cs.borderTopStyle!=='none'&&parseFloat(cs.borderBottomWidth)>0&&cs.borderBottomStyle!=='none'&&parseFloat(cs.borderLeftWidth)>0;};
 for(const e of root.querySelectorAll('section,div,details,fieldset')){
  if(!vis(e)||!boxed(e)){continue;}let n=e.nextElementSibling;while(n&&!vis(n)){n=n.nextElementSibling;}
  if(!n||!boxed(n)){continue;}const A=shown(e),B=shown(n);if(A.height<4||B.height<4){continue;}
  const gap=B.top-A.bottom,ox=Math.min(A.right,B.right)-Math.max(A.left,B.left);
  if(ox>20&&gap>=-1&&gap<4&&getComputedStyle(e.parentElement).display!=='grid'||ox>20&&gap>=-1&&gap<4&&getComputedStyle(e.parentElement).rowGap==='normal'&&getComputedStyle(e.parentElement).display==='grid'){bad.push(name(e)+' touches '+name(n)+' below it ('+Math.round(gap)+' px)');}
 }
 // Buttons side by side in one row are the same height.
 for(const row of new Set(boxes.filter(b=>b.e.tagName==='BUTTON').map(b=>b.e.parentElement))){
  const bs=[...row.children].filter(c=>c.tagName==='BUTTON'&&vis(c)&&!c.classList.contains('link-button')).map(c=>c.getBoundingClientRect());
  for(let k=1;k<bs.length;k++){if(Math.abs(bs[k].top-bs[k-1].top)<bs[k].height/2&&Math.abs(bs[k].height-bs[k-1].height)>4){bad.push(name(row)+' has buttons of '+Math.round(bs[k-1].height)+' and '+Math.round(bs[k].height)+' px');break;}}
 }
 // Text cut off at the side.
 for(const e of root.querySelectorAll('p,div,span,label,h3,h4,li')){
  if(!vis(e)){continue;}const cs=getComputedStyle(e);if(cs.whiteSpace==='nowrap'||cs.textOverflow==='ellipsis'){continue;}
  if(/hidden|clip/.test(cs.overflowX)&&e.scrollWidth-e.clientWidth>2){bad.push(name(e)+' hides '+(e.scrollWidth-e.clientWidth)+' px at the side');}
 }
 return [...new Set(bad)];
});}
async function openReads(f){const t=f.page.locator('#vmReadBarToggle');if(await t.isVisible()&&await f.page.locator('#vmReadPanels').isHidden()){await t.click();}}
async function goTool(f,id){await f.page.keyboard.press(discoveryTools.includes(id)?'Alt+1':'Alt+5');await f.page.locator('[data-tool="'+id+'"]').click();}
// The VMware and Hyper-V inventory: open an item from the tree, and read the tree as 'level label' lines.
// HTML5 drag and drop with real mouse steps (locator.dragTo misses the drop on scrolled pages).
async function dragTo(f,from,to){const a=await f.page.locator(from).boundingBox(),b=await f.page.locator(to).boundingBox();await f.page.mouse.move(a.x+5,a.y+5);await f.page.mouse.down();await f.page.mouse.move(a.x+30,a.y+30,{steps:5});await f.page.mouse.move(b.x+5,b.y+5,{steps:10});await f.page.mouse.up();}
async function openVm(f,id){await f.page.locator('#vmInvTree li[data-inv-id="vm:'+id+'"] > .vm-inv-row').click();}
async function openTree(f,label){await f.page.locator('#vmInvTree li.vm-inv-item > .vm-inv-row').filter({has:f.page.locator('.vm-inv-label',{hasText:new RegExp('^'+label.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')+'$')})}).first().click();}
async function treeLines(f){return f.page.locator('#vmInvTree li.vm-inv-item').evaluateAll(list=>list.map(li=>li.getAttribute('aria-level')+' '+li.querySelector('.vm-inv-label').textContent));}
const readmeShot=async(f,name)=>{if(!process.env.SIMM_README_SHOTS){return;}const v=f.page.viewportSize();await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement&&document.activeElement.blur());await f.page.waitForTimeout(200);await f.page.screenshot({path:process.env.SIMM_README_SHOTS+'/'+name});await f.page.setViewportSize(v);};
async function check(name,fn){if(process.env.SIMM_CHECK&&!name.includes(process.env.SIMM_CHECK)){return;}await fn();report.checks.push({name,result:'passed'});console.log('PASS: '+name);}
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
   await f.page.locator('#tabButtonDiscovery').focus();await f.page.keyboard.press('ArrowRight');assert.equal(await f.page.locator('#tabButtonCollection').getAttribute('aria-selected'),'true');await f.page.keyboard.press('End');assert.equal(await f.page.locator('#tabButtonMigration').getAttribute('aria-selected'),'true');await f.page.keyboard.press('ArrowLeft');assert.equal(await f.page.locator('#tabButtonRestore').getAttribute('aria-selected'),'true');await f.page.keyboard.press('ArrowLeft');assert.equal(await f.page.locator('#tabButtonReport').getAttribute('aria-selected'),'true');await f.page.keyboard.press('Home');assert.equal(await f.page.locator('#tabButtonDiscovery').getAttribute('aria-selected'),'true');assert.equal(await f.page.locator('.tab-button').count(),5,'five top tabs');
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
   await goTool(f,'server');await f.page.locator('#dismissNotice').click();
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
  // A failed install names itself, gives the reason and the manual fallback, and offers Try again instead of looping silently.
  f.state.update={status:'ready',title:'Update ready',message:'Installed 3.2.35. Version 3.2.36 is downloaded and verified.',available:true,canInstall:true,canDownload:true,installFailed:true,notice:'Version 3.2.36 was not installed. The installed executable could not be replaced because another program held it open or access was denied. To install it by hand, close this application and copy C:\\Ready\\x.exe over C:\\Tools\\x.exe.'};await f.page.waitForTimeout(1100);
  assert(await f.page.locator('#updateBanner').isVisible());assert((await f.page.locator('#updateBanner').getAttribute('class')).includes('warning'));assert.equal(await f.page.locator('#updateTitle').textContent(),'Update not installed');assert(/could not be replaced.*To install it by hand/.test(await f.page.locator('#updateStatus').textContent()));assert.equal(await f.page.locator('#updateInstallButton').textContent(),'Try again');
  f.state.update={...f.state.update,installFailed:false,notice:''};await f.page.waitForTimeout(1100);assert.equal(await f.page.locator('#updateInstallButton').textContent(),'Install and restart');assert.equal(await f.page.locator('#updateTitle').textContent(),'Update ready');
  f.state.update={status:'current',title:'Up to date',message:'Installed.',canCheck:true};await f.page.waitForTimeout(1100);assert(!await f.page.locator('#updateBanner').isVisible());assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Fluid density: text and spacing are full size on a 1440p screen, smaller at 1080p and smaller windows, and never below 10.5 px',async()=>{const f=await fixture(browser);try{
  const read=()=>f.page.evaluate(()=>{const cs=s=>getComputedStyle(document.querySelector(s));return {body:parseFloat(cs('body').fontSize),pad:parseFloat(cs('.card-body').paddingTop),tab:document.querySelector('.tab-button').getBoundingClientRect().height,small:Array.from(document.querySelectorAll('body *')).filter(e=>e.offsetParent&&Array.from(e.childNodes).some(x=>x.nodeType===3&&x.nodeValue.trim())&&parseFloat(getComputedStyle(e).fontSize)<10.5).length};});
  await f.page.setViewportSize({width:2560,height:1369});await f.page.waitForTimeout(100);const big=await read();
  assert(Math.abs(big.body-14)<0.05,'body text is 14 px on a 1440p screen, got '+big.body);
  await f.page.setViewportSize({width:1904,height:1000});await f.page.waitForTimeout(100);const hd=await read();
  assert(hd.body<12&&hd.body>=11,'body text shrinks at 1080p, got '+hd.body);assert(hd.pad<big.pad*0.8,'padding shrinks at 1080p: '+hd.pad+' vs '+big.pad);assert(hd.tab<big.tab,'tabs get shorter at 1080p');
  await f.page.setViewportSize({width:2384,height:1000});await f.page.waitForTimeout(100);const wideShort=await read();assert(wideShort.body<=hd.body+0.01,'a short window scales by its height');
  await f.page.setViewportSize({width:2240,height:1300});await f.page.waitForTimeout(100);const mid=await read();assert(mid.body>hd.body&&mid.body<big.body,'the scale is fluid between 1080p and 1440p, got '+mid.body);
  for(const size of [{width:1904,height:1000},{width:1350,height:690},{width:1008,height:690},{width:784,height:522}]){await f.page.setViewportSize(size);await f.page.waitForTimeout(100);assert.equal((await read()).small,0,'text below 10.5 px at '+size.width+'x'+size.height);}
 }finally{await f.close();}});
 await check('Nothing is cut off: every tool and every New migration screen at 1024x768, 1366x768 and 1920x1080 scrolls instead of clipping, and Back and Next stay in view',async()=>{const f=await fixture(browser);try{
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();
  f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',account:'administrator@vsphere.local',summary:vmwareSummaryFixture(),inventorySerial:1,canPrepare:true,hyperVHere:true,hostStorage:{known:true,vhdPath:'D:\\Hyper-V\\Disks',vmPath:'D:\\Hyper-V',clustered:true,volumes:[{path:'C:\\ClusterStorage\\Volume1',freeBytes:1099511627776,totalBytes:2199023255552,csv:true}]}});
  // A box that hides overflow while its content is taller than it is cuts
  // content off; so does a panel that runs past the bottom of the window
  // when the page itself cannot scroll. One-line ellipsis and line clamps
  // are deliberate and left alone.
  const clipped=()=>f.page.evaluate(()=>{
   const bad=[],H=innerHeight,doc=document.scrollingElement||document.documentElement,pageScrolls=doc.scrollHeight>H+1&&getComputedStyle(doc).overflowY!=='hidden'&&getComputedStyle(document.body).overflowY!=='hidden';
   const name=e=>e.tagName.toLowerCase()+(e.id?'#'+e.id:'')+(typeof e.className==='string'&&e.className?'.'+e.className.trim().split(/\s+/).slice(0,2).join('.'):'');
   const root=document.querySelector('.tab-panel.active');if(!root){return ['no active tab'];}
   for(const e of [root,...root.querySelectorAll('*')]){
    const cs=getComputedStyle(e);if(cs.display==='none'||cs.visibility==='hidden'||!e.getClientRects().length){continue;}
    const r=e.getBoundingClientRect();if(r.width<2||r.height<24){continue;}
    if(cs.whiteSpace==='nowrap'||cs.textOverflow==='ellipsis'||(cs.webkitLineClamp&&cs.webkitLineClamp!=='none')){continue;}
    if(/hidden|clip/.test(cs.overflowY)&&e.scrollHeight-e.clientHeight>2){bad.push(name(e)+' hides '+(e.scrollHeight-e.clientHeight)+' px');}
    if(!pageScrolls&&/auto|scroll/.test(cs.overflowY)&&r.bottom>H+1){let inner=false;for(let p=e.parentElement;p&&p!==document.body;p=p.parentElement){if(/auto|scroll/.test(getComputedStyle(p).overflowY)&&p.getBoundingClientRect().bottom<=H+1){inner=true;break;}}if(!inner){bad.push(name(e)+' runs '+Math.round(r.bottom-H)+' px past the window');}}
   }
   return [...new Set(bad)];
  });
  const inView=sel=>f.page.locator(sel).evaluate(e=>{const r=e.getBoundingClientRect();return r.width>0&&r.top>=0&&r.bottom<=innerHeight&&r.left>=0&&r.right<=innerWidth;});
  const sizes=[{width:1008,height:690},{width:1350,height:690},{width:1904,height:1000}];
  const migration=await f.page.locator('#migrationNav [data-tool]').evaluateAll(n=>n.map(x=>x.getAttribute('data-tool')));
  const all=['server','environment',...migration];
  for(const size of sizes){await f.page.setViewportSize(size);
   for(const id of all){await goTool(f,id);await f.page.waitForTimeout(150);const bad=await clipped();assert.deepEqual(bad,[],id+' at '+size.width+'x'+size.height+': '+bad.join('; '));const apart=await apartControls(f.page);if(process.env.SIMM_SWEEP){if(size.width>1900){await f.page.screenshot({path:process.env.SIMM_SWEEP+'/'+id+'.png'});}if(apart.length){console.log('APART',id,size.width,apart.join('; '));}}else{assert.deepEqual(apart,[],'controls apart in '+id+' at '+size.width+'x'+size.height+': '+apart.join('; '));}}
   for(const tab of ['Collection','Report','Restore']){const b=f.page.locator('#tabButton'+tab);if(await b.count()){await b.click();await f.page.waitForTimeout(100);const bad=await clipped();assert.deepEqual(bad,[],tab+' at '+size.width+'x'+size.height+': '+bad.join('; '));}}
  }
  // Every New migration screen, including the tallest: Where with a cluster
  // and the steps of a way that continues in its own tool.
  const screens=async label=>{for(const size of sizes){await f.page.setViewportSize(size);await f.page.waitForTimeout(150);const bad=await clipped();assert.deepEqual(bad,[],'New migration '+label+' at '+size.width+'x'+size.height+': '+bad.join('; '));const apart=await apartControls(f.page);if(process.env.SIMM_SWEEP){if(size.width>1900){await f.page.screenshot({path:process.env.SIMM_SWEEP+'/nw-'+label.replace(/\W+/g,'-')+'.png'});}if(apart.length){console.log('APART nw',label,size.width,apart.join('; '));}}else{assert.deepEqual(apart,[],'controls apart on New migration '+label+' at '+size.width+'x'+size.height+': '+apart.join('; '));}assert(await inView('#nwNext'),'Next is in view on '+label+' at '+size.width+'x'+size.height);if(await f.page.locator('#nwBack').isVisible()){assert(await inView('#nwBack'),'Back is in view on '+label);}}};
  await goTool(f,'wizard');await f.page.locator('[data-nw-source="vms"]').click();await f.page.waitForSelector('#nwVmList [data-nw-pick]');
  await f.page.locator('[data-nw-pick="vm-app01"]').check();await f.page.locator('[data-nw-pick="vm-sql01"]').check();await screens('What');
  await f.page.locator('#nwNext').click();await f.page.locator('[data-nw-target="here"]').click();await f.page.locator('[data-nw-switch="VM-Servers"]').selectOption('Isolated');await screens('Where');
  const step=f.page.locator('#nwStep');await f.page.setViewportSize(sizes[0]);
  assert(await step.evaluate(e=>e.scrollHeight>e.clientHeight),'at 1024x768 the Where screen is taller than its space');
  await step.evaluate(e=>e.scrollTop=e.scrollHeight);assert(await f.page.locator('#nwCluster').evaluate(e=>{const r=e.getBoundingClientRect(),s=document.querySelector('#nwStep').getBoundingClientRect();return r.top>=s.top&&r.bottom<=s.bottom;}),'scrolling the step reaches the last option');
  await f.page.locator('#nwNext').click();await screens('How (migration plan)');
  await f.page.locator('[data-nw-method="movevms"]').click();await f.page.waitForSelector('#gdSteps li.gd-step');await screens('How (steps for Move VMs)');
  await f.page.locator('[data-nw-method="plans"]').click();await f.page.locator('#nwNext').click();await screens('When');
  await f.page.locator('#nwNext').click();await screens('Review');
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Native feel: no browser right-click menu or selection on interface text; fields and values stay selectable',async()=>{const f=await fixture(browser);try{
  await f.page.evaluate(()=>{window.menuLog=[];window.addEventListener('contextmenu',e=>{window.menuLog.push((e.target.id||e.target.className||e.target.nodeName)+':'+(e.defaultPrevented?'blocked':'shown'));});});
  await f.page.locator('#tabButtonReport').click({button:'right'});await f.page.locator('.brand-title').click({button:'right'});
  await f.page.keyboard.press('Alt+5');await goTool(f,'shares');await f.page.locator('#migrateSource').click({button:'right'});
  assert.deepEqual(await f.page.evaluate(()=>window.menuLog),['tabButtonReport:blocked','brand-title:blocked','migrateSource:shown']);
  await f.page.locator('.brand-title').click({clickCount:3});assert.equal(await f.page.evaluate(()=>String(window.getSelection()).trim()),'');
  const looks=await f.page.evaluate(()=>['#tabButtonReport','.brand-title','.tool-name','#migrateSource','#logView','#vmReceiveThumbprint'].map(q=>{const el=document.querySelector(q);return q+'='+getComputedStyle(el).userSelect;}));
  assert.deepEqual(looks,['#tabButtonReport=none','.brand-title=none','.tool-name=none','#migrateSource=text','#logView=text','#vmReceiveThumbprint=text']);
  await f.page.evaluate(()=>{window.menuLog=[];const dd=document.createElement('dd');dd.id='menuValue';dd.textContent='192.0.2.10';document.body.appendChild(dd);const r=document.createRange();r.selectNodeContents(dd);const sel=window.getSelection();sel.removeAllRanges();sel.addRange(r);});
  await f.page.locator('#menuValue').click({button:'right'});assert.deepEqual(await f.page.evaluate(()=>window.menuLog),['menuValue:shown']);
  assert.deepEqual(f.dialogs,[]);
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
  assert.equal(await f.page.evaluate(()=>document.activeElement.id),'tabButtonRestore');await f.page.keyboard.press('ArrowRight');assert.equal(await f.page.locator('#tabButtonMigration').getAttribute('aria-selected'),'true');await f.page.keyboard.press('ArrowLeft');assert.equal(await f.page.locator('#tabButtonRestore').getAttribute('aria-selected'),'true');
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
  await goTool(f,'shares');await f.page.locator('#migrateSource').fill('FS-OLD-07');await f.page.locator('#migrateConnect').click();
  await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Connecting');
  Object.assign(f.state.migrate,{running:false,action:'',status:'discovered',message:'Found 3 share(s) on FS-OLD-07.',source:'FS-OLD-07',discovery:migrateDiscoveryFixture(),workPath:'C:\\Users\\admin\\AppData\\Local\\Server Inventory Configuration Manager\\Migrations\\FS-OLD-07'});
  await f.page.waitForFunction(()=>document.querySelectorAll('.migrate-share').length===3);
 };
 const migrateChecked=f=>{const m=f.state.migrate;Object.assign(m,{running:false,action:'',status:'checked',message:'Everything is ready to copy.',prechecked:true,canCopy:true,space:[{drive:'D:',neededBytes:268435456000,freeBytes:400e9,ok:true}]});
  m.plan.forEach((p,i)=>{p.status='ready';p.precheck={ok:true,problems:[],warnings:i===0?['Permissions name local accounts of the old server, which do not exist here: FS-OLD-07\\Scanner.']:[],files:i===0?120345:5400,bytes:i===0?214748364800:53687091200};});};
 const migrateCalls=(f,action)=>f.migrateCalls.filter(c=>c.action===action);
 await check('Migrate tab: Alt+5 reaches it; Connect posts the server name; shares get editable defaults and system shares start unticked',async()=>{const f=await fixture(browser);try{
  assert.equal(await f.page.locator('#migrateTabBadge').textContent(),'Ready');
  await goTool(f,'shares');assert.equal(await f.page.locator('#tabButtonMigration').getAttribute('aria-selected'),'true');assert(await f.page.locator('#tabMigrate').isVisible());assert.equal(await f.page.locator('[data-tool="shares"]').getAttribute('aria-pressed'),'true');
  assert((await f.page.locator('[data-tool="shares"] .tool-name').textContent()).startsWith('Move file shares'));assert((await f.page.locator('[data-tool="shares"] .tool-runs').textContent()).includes('the new file server'));assert(/Discovery, Collection, Report Viewer, Restore or Migration and replication/.test(await f.page.locator('#shortcutsDialog').textContent()));
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
  await goTool(f,'shares');await f.page.locator('#migrateStop').click();await f.page.waitForTimeout(300);assert.equal(migrateCalls(f,'cancel').length,1);assert(await f.page.locator('#migrateStop').isDisabled());assert.equal(await f.page.locator('#migrateStop').textContent(),'Stopping\u2026');
  Object.assign(m,{running:false,action:'',status:'copied',message:'Copy finished.',cancelRequested:false,canCutover:true});m.plan.forEach(p=>{p.status='succeeded';p.previouslySynced=true;p.lastSyncUnixMs=Date.now();p.message='1,204 file(s) copied, 3 removed that no longer exist on the old server.';});await f.page.waitForTimeout(1100);
  assert(!await f.page.locator('#migrateStop').isVisible());assert.equal(await f.page.locator('#migrateCopy').textContent(),'Copy again');assert(await f.page.locator('#migrateCutover').isEnabled());assert.equal(await f.page.locator('#migrateSteps li.current').getAttribute('data-step'),'4');
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isEnabled(),'collection unlocks after the step');await goTool(f,'shares');
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
 await check('Migrate from an admin computer: Another server needs no administrator rights here, sends both accounts only to Connect, browses the new server and keeps copying until cutover',async()=>{const f=await fixture(browser);try{
  f.state.isAdministrator=false;await goTool(f,'shares');await f.page.waitForTimeout(1100);
  assert(await f.page.locator('#migrateAdminNotice').isVisible(),'moving to this computer needs administrator rights');assert(/choose Another server/.test(await f.page.locator('#migrateAdminNotice').textContent()));
  assert(!await f.page.locator('#migrateTargetFields').isVisible(),'the new server fields start hidden');
  await f.page.locator('#migrateTargetOther').check();await f.page.waitForTimeout(100);
  assert(!await f.page.locator('#migrateAdminNotice').isVisible(),'another server needs no administrator rights here');
  assert(await f.page.locator('#migrateTargetFields').isVisible());assert(/never pass through this computer/.test(await f.page.locator('#migrateTargetHow').textContent()));
  assert(await f.page.locator('#migrateAccountOther').isChecked()&&await f.page.locator('#migrateAccountCurrent').isDisabled(),'the old server account must be typed from an admin computer');
  assert(/cannot pass your sign-in on/.test(await f.page.locator('#migrateAccountDetail').textContent()));
  await f.page.locator('#migrateSource').fill('FS-OLD-07');assert(await f.page.locator('#migrateConnect').isDisabled(),'Connect needs the new server too');
  await f.page.locator('#migrateTarget').fill('fs-new-02.example.com');await f.page.locator('#migrateUser').fill('EXAMPLE\\admin');await f.page.locator('#migratePassword').fill('Synthetic-Pa55');
  await f.page.locator('#migrateConnect').click();await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Connecting');
  assert.deepEqual(migrateCalls(f,'discover')[0].body,{source:'FS-OLD-07',target:'fs-new-02.example.com',user:'EXAMPLE\\admin',password:'Synthetic-Pa55',targetUser:'EXAMPLE\\admin',targetPassword:'Synthetic-Pa55'},'the same account reaches both servers by default');
  assert.equal(await f.page.locator('#migratePassword').inputValue(),'','the password leaves the page after Connect');
  assert(await f.page.locator('#migrateTarget').isDisabled()&&await f.page.locator('#migrateTargetHere').isDisabled(),'the new server is fixed once connected');
  Object.assign(f.state.migrate,{running:false,action:'',status:'discovered',message:'Found 3 share(s) on FS-OLD-07.',discovery:migrateDiscoveryFixture()});
  await f.page.waitForFunction(()=>document.querySelectorAll('.migrate-share').length===3);
  assert(await f.page.locator('#migrateCheck').isEnabled(),'Check plan works without administrator rights here');
  f.pickCalls.length=0;await f.page.locator('[data-migrate-row="Finance"] [data-migrate-browse]').click();await f.page.waitForTimeout(200);
  assert.equal(f.pickCalls.length,1);assert.equal(f.pickCalls[0].kind,'remote');assert.equal(f.pickCalls[0].host,'fs-new-02.example.com');assert(/on FS-NEW-02/.test(f.pickCalls[0].title));
  assert.equal(migrateCalls(f,'browse').length,0,'never this computer\'s own folder picker');
  await f.page.locator('#migrateCheck').click();await f.page.waitForFunction(()=>document.querySelector('#migrateTabBadge').textContent==='Checking');
  migrateChecked(f);await f.page.waitForTimeout(1100);
  assert(await f.page.locator('#migrateAutoRow').isVisible());assert(!await f.page.locator('#migrateAuto').isChecked());
  await f.page.locator('#migrateAutoMinutes').selectOption('30');await f.page.locator('#migrateAuto').check();await f.page.waitForTimeout(200);
  assert.deepEqual(migrateCalls(f,'autosync').map(c=>c.body),[{minutes:30}]);await f.page.waitForTimeout(1100);
  assert(/Runs while this application stays open/.test(await f.page.locator('#migrateAutoNote').textContent()));
  await f.page.locator('#migrateAuto').uncheck();await f.page.waitForTimeout(200);assert.deepEqual(migrateCalls(f,'autosync')[1].body,{minutes:0});
  await f.page.locator('#migrateCopy').click();await f.page.waitForTimeout(200);
  if(await f.page.locator('#migrateDialog').isVisible()){assert(/FS-NEW-02/.test(await f.page.locator('#migrateDialog').textContent()),'the confirmation names the new server');}
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Migrate Connect as offers another account, sends it only to Connect and asks for it after access is denied',async()=>{const f=await fixture(browser);try{
  await goTool(f,'shares');await f.page.locator('#migrateSource').fill('FS-OLD-07');
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
  await goTool(f,'environment');await f.page.locator('#vmServer').fill('vc01.example.com');await f.page.locator('#vmUser').fill('readonly@vsphere.local');await f.page.locator('#vmPassword').fill('Synthetic-Pa55');await f.page.locator('#vmConnect').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Reading');
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();f.vm.sizing=vmwareSizingFixture(false);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',message:'Read 4 VM(s) on 3 host(s) from vc01.example.com.',summary:vmwareSummaryFixture(),inventorySerial:1,outputFolder:'C:\\Reports\\VMware_vc01',reportPath:'C:\\Reports\\VMware_vc01\\VMware-Readiness-Report.html'});
  await f.page.waitForFunction(()=>document.querySelectorAll('#vmTableBody tr.vm-row').length===4);
 };
 const vmwareCalls=(f,action)=>f.vmwareCalls.filter(c=>c.action===action&&c.method==='POST');
 await check('VM console: opens from a VM page in the app\'s own window, sends keys, special keys, sticky modifiers, paste as typing and files, shows the VM clipboard, and asks to sign in when the session is missing',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);await openVm(f,'vm-app01');
  f.consoleOpt.needSignIn='vmware';await f.page.locator('#vmConsoleButton').click();
  await f.page.waitForFunction(()=>/could not open/.test(document.querySelector('#cnOverlayBox').textContent));
  assert((await f.page.locator('#cnOverlayBox').textContent()).includes('Sign in to vCenter or ESXi'));
  f.consoleOpt.needSignIn='';await f.page.locator('#cnOverlayBox button',{hasText:'Try again'}).click();
  await f.page.waitForFunction(()=>document.querySelector('#cnOverlay').classList.contains('hidden'));
  const open=f.consoleCalls.filter(c=>c.action==='open').at(-1);assert.deepEqual({p:open.body.platform,id:open.body.id,name:open.body.name},{p:'vmware',id:'vm-app01',name:'APP01'});
  await f.page.waitForFunction(()=>{const c=document.querySelector('#cnScreen canvas');if(!c||c.width!==640)return false;const d=c.getContext('2d').getImageData(10,10,1,1).data;return d[1]===200&&d[0]===30;});
  assert((await f.page.locator('#cnStatus').textContent()).includes('APP01 on VMware'));
  await f.page.locator('#cnScreen canvas').click({position:{x:20,y:20}});await f.page.keyboard.type('ab');await f.page.keyboard.press('Alt+2');
  await f.page.waitForFunction(()=>true);await new Promise(r=>setTimeout(r,300));
  const events=f.consoleCalls.filter(c=>c.action==='input').flatMap(c=>c.body.events);
  assert(events.some(e=>e.t==='k'&&e.s===0x61&&e.d),'a is sent as a keysym');assert(events.some(e=>e.t==='k'&&e.s===0xffe9&&e.d),'Alt goes to the VM');assert(events.some(e=>e.t==='p'),'the click goes to the VM');
  assert(await f.page.locator('#consoleDialog').evaluate(d=>d.open),'Alt+2 does not switch tabs while the console is open');
  await f.page.locator('#cnCad').click();await f.page.locator('#cnKeysMenu summary').click();await f.page.locator('#cnKeysList button',{hasText:'Task Manager'}).click();
  await f.page.waitForFunction(()=>true);await new Promise(r=>setTimeout(r,200));
  assert.deepEqual(f.consoleCalls.filter(c=>c.action==='keys').map(c=>c.body.combo),['ctrlaltdel','ctrlshiftesc']);
  await f.page.locator('#cnStickyCtrl').click();assert.equal(await f.page.locator('#cnStickyCtrl').getAttribute('aria-pressed'),'true');
  await f.page.locator('#cnScreen canvas').focus();await f.page.keyboard.press('c');await new Promise(r=>setTimeout(r,300));
  const after=f.consoleCalls.filter(c=>c.action==='input').flatMap(c=>c.body.events);const ci=after.findIndex(e=>e.s===0x63&&e.d);
  assert(ci>0&&after.slice(0,ci).some(e=>e.s===0xffe3&&e.d),'sticky Ctrl is held for the next key');assert.equal(await f.page.locator('#cnStickyCtrl').getAttribute('aria-pressed'),'false','sticky Ctrl lets go after one key');
  await f.page.locator('#cnPasteButton').click();await f.page.locator('#cnPasteText').fill('P@ssw0rd!');await f.page.locator('#cnPasteSpeed').selectOption('60');await f.page.locator('#cnPasteEnter').check();await f.page.locator('#cnPasteGo').click();
  await f.page.waitForFunction(()=>document.querySelector('#cnPasteText')&&document.querySelector('#cnPasteText').value==='');
  assert.deepEqual(f.consoleCalls.filter(c=>c.action==='type').at(-1).body,{id:'cn1',text:'P@ssw0rd!\n',delayMs:60});
  // Ctrl+V on the screen types this computer's clipboard into the VM (no
  // panel), with Ctrl released in the VM first and no V sent; Ctrl+Shift+V
  // sends a plain Ctrl+V to the VM instead.
  await f.context.grantPermissions(['clipboard-read','clipboard-write']);await f.page.evaluate(()=>navigator.clipboard.writeText('admin\r\nS3cret!'));
  const typedBefore=f.consoleCalls.filter(c=>c.action==='type').length,inputBefore=f.consoleCalls.filter(c=>c.action==='input').flatMap(c=>c.body.events).length;
  await f.page.locator('#cnScreen canvas').focus();await f.page.keyboard.press('Control+V');
  for(let i=0;i<40&&f.consoleCalls.filter(c=>c.action==='type').length===typedBefore;i++){await new Promise(r=>setTimeout(r,50));}
  assert.deepEqual(f.consoleCalls.filter(c=>c.action==='type').at(-1).body,{id:'cn1',text:'admin\r\nS3cret!',delayMs:60},'Ctrl+V types the clipboard into the VM');
  await new Promise(r=>setTimeout(r,200));
  const pasteEvents=f.consoleCalls.filter(c=>c.action==='input').flatMap(c=>c.body.events).slice(inputBefore);
  assert(!pasteEvents.some(e=>e.t==='k'&&e.s===0x76),'Ctrl+V sends no V key to the VM');
  const ctrlDown=pasteEvents.findIndex(e=>e.s===0xffe3&&e.d);assert(ctrlDown>=0&&pasteEvents.slice(ctrlDown+1).some(e=>e.s===0xffe3&&!e.d),'Ctrl is let go in the VM before the text is typed');
  {const ti=f.consoleCalls.map(c=>c.action).lastIndexOf('type');const before=f.consoleCalls.slice(0,ti).filter(c=>c.action==='input').flatMap(c=>c.body.events).slice(inputBefore);
    assert(before.some(e=>e.s===0xffe3&&!e.d),'the Ctrl release reaches the server before the typing request (the VM lost the first letter to Ctrl+H)');}
  assert((await f.page.locator('#cnHint').textContent()).includes('Ctrl+V types your clipboard'));
  const typedAfter=f.consoleCalls.filter(c=>c.action==='type').length;await f.page.keyboard.press('Control+Shift+V');await new Promise(r=>setTimeout(r,500));
  assert.equal(f.consoleCalls.filter(c=>c.action==='type').length,typedAfter,'Ctrl+Shift+V does not type the clipboard');
  const rawEvents=f.consoleCalls.filter(c=>c.action==='input').flatMap(c=>c.body.events);const vi=rawEvents.findIndex(e=>e.s===0x76&&e.d);
  assert(vi>0&&rawEvents.slice(0,vi).some(e=>e.s===0xffe3&&e.d),'Ctrl+Shift+V sends Ctrl+V to the VM');
  f.consoleStreams.forEach(e=>e.ev('clip',{text:'copied in the VM'}));await f.page.waitForFunction(()=>/new/.test(document.querySelector('#cnClipButton').textContent));
  await f.page.locator('#cnClipButton').click();assert.equal(await f.page.locator('#cnSide .cn-clip').textContent(),'copied in the VM');
  // Copy out: text from the screen (a dragged area and the whole screen,
  // sent as PNG crops and read by Windows on this computer) and from a file
  // in the VM, with the save command typed into the VM.
  const inputsBeforeSel=f.consoleCalls.filter(c=>c.action==='input').length;
  await f.page.locator('#cnOcrArea').click();const cbox=await f.page.locator('#cnScreen canvas').boundingBox();
  await f.page.mouse.move(cbox.x+cbox.width*0.1,cbox.y+cbox.height*0.1);await f.page.mouse.down();await f.page.mouse.move(cbox.x+cbox.width*0.35,cbox.y+cbox.height*0.3,{steps:4});
  assert(await f.page.locator('#cnSelBox').isVisible(),'the chosen area is outlined');await f.page.mouse.up();
  await f.page.waitForFunction(()=>/Setup log/.test(document.querySelector('#cnOutText').value));
  const ocr=f.consoleCalls.filter(c=>c.action==='ocr').at(-1).body,png=Buffer.from(ocr.png,'base64'),ow=png.readUInt32BE(16),oh=png.readUInt32BE(20);
  assert.equal(ocr.id,'cn1');assert(ow>=0.25*640*0.9&&oh>=0.2*480*0.9&&ow<640*3,'the crop covers the dragged area: '+ow+'x'+oh);
  assert.equal(f.consoleCalls.filter(c=>c.action==='input').length,inputsBeforeSel,'dragging an area sends no mouse events to the VM');
  assert(await f.page.locator('#cnSelBox').isHidden());assert((await f.page.locator('#cnOutMsg').textContent()).includes('Check it'));
  await f.page.locator('#cnOcrWhole').click();await f.page.waitForFunction(()=>/Setup log: (1280|1920|640)x/.test(document.querySelector('#cnOutText').value));
  await f.page.locator('#cnTextGuest').fill("C:\\Users\\Public\\it's.txt");await f.page.locator('#cnTextSave').click();
  await f.page.waitForFunction(()=>/Press Enter/.test(document.querySelector('#cnOutMsg').textContent));
  assert.equal(f.consoleCalls.filter(c=>c.action==='type').at(-1).body.text,"Get-Clipboard | Set-Content -Encoding UTF8 -LiteralPath 'C:\\Users\\Public\\it''s.txt'",'the save command quotes the path');
  await f.page.locator('#cnTextUser').fill('Administrator');await f.page.locator('#cnTextPass').fill('Synthetic-Pa55');await f.page.locator('#cnTextRead').click();
  await f.page.waitForFunction(()=>/192\.0\.2\.25/.test(document.querySelector('#cnOutText').value));
  assert.deepEqual(f.consoleCalls.filter(c=>c.action==='text').at(-1).body,{id:'cn1',guest:"C:\\Users\\Public\\it's.txt",user:'Administrator',password:'Synthetic-Pa55'});
  assert.equal(await f.page.locator('#cnTextPass').inputValue(),'','the guest password leaves the page');
  await f.page.locator('#cnOutCopy').click();await f.page.waitForFunction(()=>/Copied/.test(document.querySelector('#cnOutMsg').textContent));
  assert.equal((await f.page.evaluate(()=>navigator.clipboard.readText())).replace(/\r\n/g,'\n'),'C:\\> ipconfig\nIPv4 Address. . . : 192.0.2.25','Copy puts the text on this computer\'s clipboard');
  await f.page.locator('#cnFilesButton').click();await f.page.locator('#cnFileLocal').fill('C:\\Transfer\\setup.log');await f.page.locator('#cnFileGuest').fill('C:\\Temp\\setup.log');await f.page.locator('#cnFileUser').fill('Administrator');await f.page.locator('#cnFilePass').fill('Synthetic-Pa55');await f.page.locator('#cnFileGo').click();
  await f.page.waitForFunction(()=>/Copied 2 KB/.test(document.querySelector('#cnFileStatus').textContent));assert.equal(await f.page.locator('#cnFilePass').inputValue(),'','the guest password leaves the page');
  assert.deepEqual(f.consoleCalls.filter(c=>c.action==='file').at(-1).body,{id:'cn1',direction:'to',local:'C:\\Transfer\\setup.log',guest:'C:\\Temp\\setup.log',user:'Administrator',password:'Synthetic-Pa55'});
  await f.page.locator('#cnInfoButton').click();assert((await f.page.locator('#cnSide').textContent()).includes('Nothing is installed in the VM'));
  await f.page.locator('#cnNative button',{hasText:'VMware Remote Console'}).click();await f.page.waitForFunction(()=>/Opened in its own window/.test(document.querySelector('#cnStatus').textContent));
  await f.page.setViewportSize({width:1008,height:690});assert(await f.page.locator('#cnCad').isVisible());assert(await f.page.locator('#cnInfoButton').isVisible(),'the toolbar fits at 1024x768');
  await f.page.locator('#cnHide').click();assert(await f.page.locator('#cnReopen').isVisible());assert.equal(await f.page.locator('#cnReopen').textContent(),'Consoles (1)');
  await f.page.locator('#cnReopen').click();await f.page.locator('#cnTabs .cn-tab button[aria-label^="Close"]').click();
  await f.page.waitForFunction(()=>!document.querySelector('#consoleDialog').open);assert.equal(f.consoleCalls.filter(c=>c.action==='close').length,1);
  assert(f.consoleCalls.every(c=>c.token==='workspace-test'));assert.deepEqual(f.dialogs,[]);assert.deepEqual(f.errors,[]);
 }finally{await f.close();}});
 await check('SQL and DC checks: optional, read only, run on chosen VMs with this or another account and shown in readiness',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);await f.page.locator('[data-vmfilter="all"]').click();
  await f.page.locator('#vmListBulk [data-bulk="preflight"]').click();
  await f.page.waitForSelector('#pfBulkPanel');assert.deepEqual(await f.page.locator('#pfBulkPanel [data-pf-vm]').evaluateAll(l=>l.map(e=>e.getAttribute('data-pf-vm'))),['vm-sql01'],'the VMs named like SQL Servers are selected');
  const panel=await f.page.locator('#pfBulkPanel').textContent();assert(panel.includes('nothing is installed, copied or changed')&&panel.includes('Named like a SQL Server'),panel);
  assert.equal(await f.page.locator('[data-pf-computer="vm-sql01"]').inputValue(),'192.0.2.10','the guest address is filled in');
  assert(await f.page.locator('#pfBulkRun').isEnabled());assert.equal(await f.page.locator('#pfBulkRun').textContent(),'Run the checks on 1 server');
  assert(await f.page.locator('#pfBulkSignInCurrent').isChecked(),'checks start on the current sign-in');assert(!await f.page.locator('#pfBulkUser').isVisible(),'the account fields are hidden while the current sign-in is chosen');
  await f.page.locator('#pfBulkSignInOther').check();await f.page.locator('#pfBulkUser').fill('EXAMPLE\\admin');assert(await f.page.locator('#pfBulkRun').isDisabled());assert.equal(await f.page.locator('#pfBulkStatus').textContent(),'Type the password, or choose your current sign-in.');
  await f.page.locator('#pfBulkPassword').fill('Synthetic-Pa55');await f.page.locator('[data-pf-computer="vm-sql01"]').fill('sql01.example.com');
  await f.page.locator('#pfBulkDc').uncheck();await f.page.locator('#pfBulkSql').uncheck();assert(await f.page.locator('#pfBulkRun').isDisabled());await f.page.locator('#pfBulkSql').check();
  await f.page.locator('#pfBulkRun').click();await f.page.waitForFunction(()=>document.querySelector('#pfBulkRun').textContent==='Checking…');
  assert.deepEqual(vmwareCalls(f,'preflight/run').map(c=>c.body),[{items:[{id:'vm-sql01',computer:'sql01.example.com',via:'winrm'}],checks:['sql'],user:'EXAMPLE\\admin',password:'Synthetic-Pa55'}]);
  assert(!(await f.page.locator('#pfBulkPanel').textContent()).includes('Synthetic-Pa55'));
  f.state.vmware.preflight={running:true,user:'EXAMPLE\\admin',hasPassword:true,checks:['sql'],items:[{id:'vm-sql01',name:'SQL01',computer:'sql01.example.com',via:'winrm',state:'running',summary:''}],message:'Checking SQL01 (sql01.example.com), read only.',summary:{}};
  await f.page.waitForFunction(()=>document.querySelector('[data-pf-last="vm-sql01"]').textContent==='Checking now…');assert(await f.page.locator('#pfBulkStop').isVisible());assert(await f.page.locator('[data-pf-computer="vm-sql01"]').isDisabled(),'fields lock while checking');
  assert((await f.page.locator('#pfBulkPanel').textContent()).includes('Checks sign in as EXAMPLE\\admin'));
  const sql=f.vm.inventory.vms.find(v=>v.id==='vm-sql01');sql.preflight={checkedAt:'2026-11-26T10:00:00Z',computer:'sql01.example.com',via:'winrm',checks:['sql'],facts:{sql:{checked:true,instances:[{name:'MSSQLSERVER'}]}},summary:[['Checked','2026-11-26 10:00 through PowerShell remoting to sql01.example.com'],['SQL Server MSSQLSERVER','Standard Edition (64-bit) 16.0.4135.4 RTM; running']]};
  sql.readiness.forEach(r=>r.findings.push({severity:'warning',code:'sql-backup',message:'SQL Server instance MSSQLSERVER: no full backup in the last 7 days for Sales.',action:'Take a full backup of each database before the cutover.'}));
  f.state.vmware.preflight=Object.assign(f.state.vmware.preflight,{running:false,items:[{id:'vm-sql01',name:'SQL01',state:'done',summary:'1 SQL Server instance: 1 item to review.'}],message:'Checked 1 server. The results are in each VM\'s readiness and the reports.'});f.state.vmware.inventorySerial++;
  await f.page.waitForFunction(()=>/^Checked 2026-11-26 10:00/.test(document.querySelector('[data-pf-last="vm-sql01"]').textContent));
  assert.equal(await f.page.locator('[data-pf-last="vm-sql01"]').textContent(),'Checked 2026-11-26 10:00:00 UTC: 1 SQL Server instance; 1 item in readiness.');
  sql.readiness.forEach(r=>r.findings.push({severity:'warning',code:'preflight-failed',message:'SQL and DC checks: couldn\'t reach sql01.example.com over WinRM (TCP 5985). (2026-11-25 10:00)',action:'Check the computer name.',detail:'Connecting to remote server sql01.example.com failed with the following error message : The client cannot connect to the destination specified in the request.'}));f.state.vmware.inventorySerial++;
  await f.page.waitForFunction(()=>/2 items in readiness/.test(document.querySelector('[data-pf-last="vm-sql01"]').textContent));
  await openVm(f,'vm-sql01');await f.page.locator('#vmObjTab-readiness').click();
  const section=await f.page.locator('#pfVmSection').textContent();assert(section.includes('SQL Server MSSQLSERVER')&&section.includes('Standard Edition (64-bit)'),section);
  assert((await f.page.locator('#vmDetail').textContent()).includes('no full backup in the last 7 days for Sales'),'the finding is in readiness');
  const unreached=f.page.locator('#vmDetail li').filter({hasText:'couldn\'t reach sql01.example.com over WinRM'}).first();assert(!await unreached.locator('pre').isVisible(),'a remote error is folded under Details');
  await unreached.locator('summary').click();assert((await unreached.locator('pre').textContent()).includes('The client cannot connect to the destination'),'Details shows the full error');
  assert(await f.page.locator('#pfVmSignInOther').isChecked(),'a held account shows as a different account');await f.page.locator('#pfVmSignInCurrent').check();await f.page.waitForFunction(()=>!!document.querySelector('#pfVmUser'));assert.equal(vmwareCalls(f,'preflight/forget').length,1);
  await f.page.setViewportSize({width:1008,height:690});assert(await f.page.locator('#pfVmRun').isVisible());assert.equal(await f.page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),true,'no horizontal scroll at 1024x768');
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware tab: Read Hyper-V host reads this computer or a remote host, sends the optional account once, opens a VM disk in the Disk converter, and copies a running Hyper-V VM through checkpoints without a vSphere session',async()=>{const f=await fixture(browser);try{
  await goTool(f,'environment');
  assert(await f.page.locator('#vmHvSignInCurrent').isChecked());assert(!await f.page.locator('#vmHvUser').isVisible());await f.page.locator('#vmHvSignInOther').check();
  await f.page.locator('#vmHvUser').fill('EXAMPLE\\admin');await f.page.locator('#vmHvRead').click();
  assert.equal(await f.page.locator('#vmHvStatus').textContent(),'Type the password for EXAMPLE\\admin, or choose your current sign-in.');assert.equal(vmwareCalls(f,'hyperv').length,0);
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
  assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'hv01.example.com');assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Hyper-V host');
  assert.deepEqual(await treeLines(f),['1 hv01.example.com','2 Prod-Cluster','3 esx01.example.com','4 APP01','4 LEGACY03','3 esx02.example.com','4 SQL01','3 esx03.example.com','4 WEB02'],'a Hyper-V read has no datacenter level');
  await f.page.locator('[data-inv-view="storage"]').click();assert.equal(await f.page.locator('#vmInvViewName').textContent(),'Storage: volumes, CSVs and shares');await f.page.locator('[data-inv-view="hosts"]').click();
  assert.equal(await f.page.locator('#vmServer').inputValue(),'','a Hyper-V host is not offered as a vCenter server');assert.equal(await f.page.locator('#vmUser').inputValue(),'');
  await openVm(f,'vm-app01');const detail=await f.page.locator('#vmDetail').textContent();
  assert(detail.includes('Generation 2')&&detail.includes('responding, version 10.0.20348')&&detail.includes('Integration services'),detail);assert(!(await f.page.locator('#vmDetail dt').allTextContents()).includes('VMware Tools'),'no VMware wording for a Hyper-V VM');
  assert.deepEqual(await f.page.locator('#vmObjTabs .vm-obj-tab').evaluateAll(t=>t.map(x=>x.firstChild.textContent)),['Summary','Readiness','Configure','Storage','Networks','Checkpoints']);
  await f.page.locator('#vmObjTab-datastores').click();await f.page.locator('#vmDetail button.vm-link',{hasText:'Convert'}).click();
  assert.equal(await f.page.locator('[data-tool="convert"]').getAttribute('aria-pressed'),'true');assert(await f.page.locator('#tabConvert').isVisible());assert.equal(await f.page.locator('#cvSource').inputValue(),'\\\\hv01.example.com\\D$\\VMs\\APP01\\APP01.vhdx');
  await f.page.waitForFunction(()=>!document.querySelector('#cvInfo').classList.contains('hidden'));assert.equal(f.convertCalls.filter(c=>c.action==='inspect').at(-1).body.path,'\\\\hv01.example.com\\D$\\VMs\\APP01\\APP01.vhdx');
  // A Hyper-V VM is copied through checkpoints with this computer's account: no vSphere session, no Prepare, no quiesce.
  f.state.vmware.hyperVHere=true;f.state.vmware.inventorySerial=2;await goTool(f,'environment');
  await goTool(f,'movevms');await openVm(f,'vm-app01');await f.page.locator('#vmMoveCopy').waitFor();
  assert(!await f.page.locator('#vmMoveCopy').isDisabled()&&!await f.page.locator('#vmMoveCutover').isDisabled(),'a Hyper-V source needs no vSphere session');
  assert.equal(await f.page.locator('#vmDetail button',{hasText:'Prepare on VMware'}).count(),0);assert(!(await f.page.locator('#vmDetail').textContent()).includes('Prepare this VM on VMware'));
  assert.equal(await f.page.locator('input[data-vm-move="quiesce"]').count(),0);assert((await f.page.locator('#vmMove').textContent()).includes('takes a checkpoint on the source host'));
  await f.page.locator('#vmMoveCopy').click();const hvCopy=await f.page.locator('#migrateDialogText').textContent();
  assert(hvCopy.includes('checkpoint named "Server Inventory and Migration Manager copy"')&&!hvCopy.includes('changed block tracking'),hvCopy);
  await f.page.locator('#migrateConfirmInput').fill('APP01');await f.page.locator('#migrateConfirmButton').click();await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert.equal(await f.page.locator('input[data-vm-after="fixUp"]').count(),0,'a Hyper-V source keeps its adapters: no fix-up offered');assert.deepEqual(vmwareCalls(f,'move').at(-1).body,{vmId:'vm-app01',action:'copy',copyFolder:'',vmFolder:'',quiesce:false,keepMac:false,start:true,cluster:false,disconnectSource:false,fixUp:false,confirm:'APP01'});
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',move:{vm:'APP01',cutover:false,round:{disks:[{label:'Disk 1 (APP01.vhdx)',mode:'delta',bytesWritten:8388608,verified:false}]}},moveSerial:1});
  await f.page.waitForFunction(()=>document.querySelector('#vmMove').textContent.includes('changed chunks only, 8.0 MB written'));
  await f.page.locator('#vmMoveCutover').click();assert((await f.page.locator('#migrateDialogAlert').textContent()).includes('Hyper-V shutdown integration service'));await f.page.locator('#migrateDialogCancel').click();
  assert(f.vmwareCalls.every(c=>c.token==='workspace-test'));assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware tab: Alt+6 reaches it; Read inventory sends the password once; an untrusted certificate needs an explicit trust; readiness filters and details',async()=>{const f=await fixture(browser);try{
  assert.equal(await f.page.locator('#vmwareTabBadge').textContent(),'Ready');
  await goTool(f,'environment');assert.equal(await f.page.locator('#tabButtonDiscovery').getAttribute('aria-selected'),'true');assert.equal(await f.page.locator('[data-tool="environment"]').getAttribute('aria-pressed'),'true');assert(await f.page.locator('#tabVmware').isVisible());
  assert(await f.page.locator('#vmConnect').isDisabled(),'needs a server and user');assert(!await f.page.locator('#vmEnvResult').isVisible());
  await f.page.locator('#vmServer').fill('vc01.example.com');await f.page.locator('#vmUser').fill('readonly@vsphere.local');await f.page.locator('#vmConnect').click();
  assert.equal(await f.page.locator('#vmConnectStatus').textContent(),'Type the password for readonly@vsphere.local.');assert.equal(vmwareCalls(f,'connect').length,0);
  await f.page.locator('#vmPassword').fill('Synthetic-Pa55');await f.page.locator('#vmPassword').press('Enter');await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Reading');
  assert.deepEqual(vmwareCalls(f,'connect').map(c=>c.body),[{server:'vc01.example.com',user:'readonly@vsphere.local',password:'Synthetic-Pa55'}]);assert.equal(await f.page.locator('#vmPassword').inputValue(),'','the password leaves the page');
  assert(await f.page.locator('#vmStop').isVisible());assert(await f.page.locator('#vmServer').isDisabled());
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isEnabled(),'a Discovery read only reads, so collection stays available (rule 65)');await goTool(f,'environment');
  Object.assign(f.state.vmware,{running:false,action:'',status:'connectfailed',message:'vc01.example.com presented a certificate Windows does not trust.',certificate:{thumbprint:'AA:BB:CC:<b>',subject:'CN=vc01.example.com',issuer:'CN=CA <b>not markup</b>',notAfter:'2027-01-01',reason:'x509: certificate signed by unknown authority',mismatch:false}});
  await f.page.waitForFunction(()=>!document.querySelector('#vmCert').classList.contains('hidden'));assert.equal(await f.page.locator('#vmwareTabBadge').textContent(),'Failed');
  assert.equal(await f.page.locator('#vmCertIssuer').textContent(),'CN=CA <b>not markup</b>');assert.equal(await f.page.locator('#vmCert b').count(),0);
  await f.page.locator('#vmCertTrust').click();await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Reading');
  assert.deepEqual(vmwareCalls(f,'connect')[1].body,{server:'vc01.example.com',user:'readonly@vsphere.local',password:'',thumbprint:'AA:BB:CC:<b>'},'trust resends only the thumbprint; the held password is reused');
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();f.vm.sizing=vmwareSizingFixture(false);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',message:'Read 4 VM(s) on 3 host(s) from vc01.example.com.',certificate:null,summary:vmwareSummaryFixture(),inventorySerial:1,outputFolder:'C:\\Reports\\VMware_vc01',reportPath:'C:\\Reports\\VMware_vc01\\VMware-Readiness-Report.html'});
  await f.page.waitForFunction(()=>document.querySelectorAll('#vmTableBody tr.vm-row').length===4);
  assert.equal(await f.page.locator('#vmwareTabBadge').textContent(),'Read');assert(!await f.page.locator('#vmCert').isVisible());
  assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'vc01.example.com');assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'vCenter Server');assert((await f.page.locator('#vmDrawerBody').textContent()).includes('1 snapshot, 4 VMs with static IPs'));assert.equal(await f.page.locator('#vmCountBlocked').textContent(),'1');assert.equal(await f.page.locator('#vmCountsTitle').textContent(),'Readiness for Hyper-V');
  await f.page.locator('[data-vmfilter="blocked"]').click();assert.deepEqual(await f.page.locator('#vmTableBody tr.vm-row strong').allTextContents(),['LEGACY03']);
  await f.page.locator('[data-vmfilter="all"]').click();await f.page.locator('#vmSearch').fill('ubuntu');assert.deepEqual(await f.page.locator('#vmTableBody tr.vm-row strong').allTextContents(),['WEB02']);await f.page.locator('#vmSearch').fill('');
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmEnvResult').evaluate(e=>e.scrollIntoView({block:'start'}));await f.page.screenshot({path:shots+'/vmware-readiness-1440x1000.png'});
  await openVm(f,'vm-sql01');assert(await f.page.locator('#vmDetail').isVisible());const detail=await f.page.locator('#vmDetail').textContent();
  assert(detail.includes('Delete or consolidate snapshots before cutover.')&&detail.includes('1 snapshot'),detail);
  await f.page.locator('#vmObjTab-snapshots').click();assert((await f.page.locator('#vmDetail').textContent()).includes('Before patching <b>not markup</b>'));assert.equal(await f.page.locator('#vmDetail b').count(),0);
  await f.page.locator('#vmObjTab-readiness').click();assert((await f.page.locator('#vmDetail').textContent()).includes('Scale HyperCore: Review first'),'other platforms are listed');
  assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Virtual machine');assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'SQL01');assert(await f.page.locator('tr[data-vm-id="vm-sql01"]').evaluate(e=>e.classList.contains('selected')));assert.equal(await f.page.locator('#vmInvTree li[aria-selected="true"] > .vm-inv-row .vm-inv-label').textContent(),'SQL01');
  await f.page.locator('#vmObjTab-datastores').click();assert.deepEqual(await f.page.locator('#vmDetail tbody').first().locator('td:nth-child(3) .vm-link').allTextContents(),['DS-SSD-01','DS-NL-02'],'each disk links to its datastore');
  await f.page.locator('#vmObjTab-networks').click();assert.deepEqual(await f.page.locator('#vmDetail tbody').first().locator('.vm-link').allTextContents(),['VM-Servers']);
  await f.page.locator('#vmObjTab-summary').click();await f.page.locator('#vmObjTab-summary').press('ArrowRight');assert.equal(await f.page.evaluate(()=>document.activeElement.id),'vmObjTab-readiness','arrow keys move between tabs');await f.page.locator('#vmObjTab-summary').click();
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmInv').evaluate(e=>e.scrollIntoView({block:'start'}));await f.page.screenshot({path:shots+'/vmware-vm-details-1440x1000.png'});
  await f.page.locator('#vmDetail .vm-kv .vm-link',{hasText:'esx02.example.com'}).click();assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Host');assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'esx02.example.com');
  assert.equal(await f.page.locator('#vmDrawerBack').textContent(),'\u2039 SQL01');assert.equal(await f.page.locator('#vmInvTree li[aria-selected="true"] > .vm-inv-row .vm-inv-label').textContent(),'esx02.example.com','the tree follows the link');
  await f.page.locator('#vmObjTab-vms').click();assert.deepEqual(await f.page.locator('#vmDrawerBody tbody td:first-child .vm-link').allTextContents(),['SQL01'],'the host lists its VMs');
  await f.page.locator('#vmObjTab-summary').click();await f.page.locator('#vmDrawerBody .vm-kv .vm-link',{hasText:'Prod-Cluster'}).click();assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Cluster');assert((await f.page.locator('#vmDrawerBody').textContent()).includes('vSphere 8 Enterprise Plus'),'the cluster shows its licensing');
  await f.page.locator('#vmDrawerBack').click();assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'esx02.example.com');await f.page.locator('#vmDrawerBack').click();assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'SQL01');
  await f.page.keyboard.press('Escape');assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'vc01.example.com','Escape goes back');assert.equal(await f.page.locator('#vmTableBody tr.selected').count(),0);
  await f.page.locator('#vmInvTree li[data-inv-id="vm:vm-app01"]').focus();await f.page.keyboard.press('Space');assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'APP01');await f.page.keyboard.press('ArrowDown');assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'LEGACY03','arrow keys move through the tree');
  assert.equal(await f.page.evaluate(()=>document.activeElement.getAttribute('data-inv-id')),'vm:vm-legacy03');await f.page.keyboard.press('ArrowLeft');assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'esx01.example.com','left goes to the parent');
  await f.page.keyboard.press('ArrowLeft');assert.equal(await f.page.locator('#vmInvTree li[aria-selected="true"]').getAttribute('aria-expanded'),'false','left again collapses it');await f.page.keyboard.press('ArrowRight');assert.equal(await f.page.locator('#vmInvTree li[aria-selected="true"]').getAttribute('aria-expanded'),'true');
  assert.equal(await f.page.locator('#vmTargetSelect option').count(),7,'every target is offered');
  await f.page.locator('#vmTargetSelect').selectOption('scale');await f.page.waitForFunction(()=>document.querySelector('#vmCountsTitle').textContent==='Readiness for Scale HyperCore');
  assert.equal(await f.page.locator('tr[data-vm-id="vm-app01"] .run-chip').textContent(),'Review first');assert.equal(vmwareCalls(f,'plan').at(-1).body.plan.target,'scale','the target is saved with the plan');
  await f.page.locator('#vmTargetSelect').selectOption('nutanix');await f.page.waitForFunction(()=>document.querySelector('#vmCountsTitle').textContent==='Readiness for Nutanix AHV');assert.equal(await f.page.locator('#vmCountReview').textContent(),'4');assert.equal(await f.page.locator('#vmCountBlocked').textContent(),'0','the cards follow the VM list');assert.equal(vmwareCalls(f,'plan').at(-1).body.plan.target,'nutanix');
  // Acknowledge: a reviewed VM counts as ready, saved with the plan, per target; Open again undoes it.
  await f.page.locator('[data-vmfilter="all"]').click();
  await f.page.locator('tr[data-vm-id="vm-app01"] [data-vm-ack]').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmCountReview').textContent==='3');
  const acked=vmwareCalls(f,'plan').at(-1).body.plan.vms.find(p=>p.id==='vm-app01').acknowledged;assert(acked.length>0&&acked.every(a=>a.startsWith('nutanix:')),JSON.stringify(acked));
  assert.equal(await f.page.locator('tr[data-vm-id="vm-app01"] .run-chip').textContent(),'Ready');assert((await f.page.locator('tr[data-vm-id="vm-app01"]').textContent()).includes('Acknowledged'));
  assert.equal(await f.page.locator('#vmAckAll').textContent(),'Acknowledge all 3 to review');await f.page.locator('#vmAckAll').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmCountReview').textContent==='0');assert(await f.page.locator('#vmAckAll').isHidden(),'nothing left to acknowledge');assert.equal(await f.page.locator('#vmCountBlocked').textContent(),'0');
  await f.page.locator('tr[data-vm-id="vm-app01"] [data-vm-ack]').click();await f.page.waitForFunction(()=>document.querySelector('#vmCountReview').textContent==='1');
  await f.page.locator('#vmTargetSelect').selectOption('scale');await f.page.waitForFunction(()=>document.querySelector('#vmCountsTitle').textContent==='Readiness for Scale HyperCore');assert.equal(await f.page.locator('tr[data-vm-id="vm-app01"] .run-chip').textContent(),'Review first','acknowledgements are per target');
  await f.page.locator('[data-vmfilter="all"]').click();await f.page.locator('#vmOpenReport').click();await f.page.waitForTimeout(200);assert.deepEqual(f.opens,['vmwarereport']);
  // Once read, the read sections fold into one line above the inventory.
  assert(await f.page.locator('#vmReadPanels').isHidden(),'the read sections fold once an inventory is open');
  assert((await f.page.locator('#vmReadBarText').textContent()).includes('vc01.example.com'));
  await f.page.locator('#vmReadBarToggle').click();assert(await f.page.locator('#vmServer').isVisible());assert.equal(await f.page.locator('#vmReadBarToggle').textContent(),'Hide');
  await f.page.locator('#vmReset').click();await f.page.waitForFunction(()=>document.querySelector('#vmEnvResult').classList.contains('hidden'));assert.equal(await f.page.locator('#vmServer').inputValue(),'');assert(await f.page.locator('#vmReadBar').isHidden(),'no line without an inventory');
  assert(f.vmwareCalls.every(c=>c.token==='workspace-test'));assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware prepare source: needs a live session, preselects the needed steps, and posts them only after the VM name is typed',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);
  await openVm(f,'vm-legacy03');await f.page.locator('#vmMoveLinkButton').waitFor();assert.equal(await f.page.locator('#vmPrepareButton').count(),0,'Discovery only reads: no Prepare there');await f.page.locator('#vmMoveLinkButton').click();assert.equal(await f.page.locator('#tabButtonMigration').getAttribute('aria-selected'),'true');assert.equal(await f.page.locator('[data-tool="movevms"]').getAttribute('aria-pressed'),'true');assert(await f.page.locator('#vmDetail').isVisible(),'the VM stays open in Move VMs');assert(await f.page.locator('#vmServer').isHidden(),'Move VMs does not ask for the connection again');await openVm(f,'vm-legacy03');assert((await f.page.locator('#vmDetail').textContent()).includes('read the inventory from vCenter or ESXi in this session'));assert.equal(await f.page.locator('#vmPrepareButton').count(),0);
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
  await goTool(f,'movevms');await openVm(f,'vm-app01');assert((await f.page.locator('#vmMove').textContent()).includes('run this on the Hyper-V host'));assert.equal(await f.page.locator('#vmMoveCopy').count(),0);
  f.state.vmware.canPrepare=true;f.state.vmware.hyperVHere=false;f.state.vmware.moveCopyRoot='D:\\Migration\\VMware-Copies';f.state.vmware.inventorySerial=2;
  // Off a Hyper-V host the move offers the remote host first; this check covers the local move.
  await f.page.waitForFunction(()=>!!document.querySelector('#vmHvrConnect'));await f.page.locator('[data-vm-hvwhere="here"]').click();
  await f.page.waitForFunction(()=>!!document.querySelector('#vmMoveCopy'));
  assert((await f.page.locator('#vmMove .vm-runs-on').textContent()).includes('This computer is not a Hyper-V host'));assert(await f.page.locator('#vmMoveCopy').isDisabled()&&await f.page.locator('#vmMoveCutover').isDisabled(),'a move runs only on the Hyper-V host');
  const rolesOff=await f.page.locator('#vmRolesSummary').textContent();assert(rolesOff.includes('Run this application on the Hyper-V host')&&rolesOff.includes('SRV-DEMO-01) is not a Hyper-V host'),rolesOff);
  f.state.vmware.hyperVHere=true;f.state.vmware.inventorySerial=3;
  await f.page.waitForFunction(()=>document.querySelector('#vmMove .vm-runs-on').textContent.includes('Runs on the Hyper-V host (this computer)')&&!document.querySelector('#vmMoveCopy').disabled);
  await f.page.locator('#vmRoles > summary').click();assert.equal(await f.page.locator('#vmRolesGrid .vm-role').count(),3);
  assert.equal(await f.page.locator('#vmRolesGrid .vm-role.here h4').textContent(),'The Hyper-V host that will run the VM');assert((await f.page.locator('#vmRolesFlow').textContent()).includes('this application on the Hyper-V host'));
  await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('#vmwareBody').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:shots+'/vmware-roles-1440x1000.png'});
  await goTool(f,'movevms');await f.page.locator('#vmRoles > summary').click();await openVm(f,'vm-app01');await f.page.locator('#vmMoveCopy').waitFor();
  assert.equal(await f.page.locator('#vmMoveFolder').getAttribute('placeholder'),'D:\\Migration\\VMware-Copies\\APP01');
  assert((await f.page.locator('#vmMove').textContent()).includes('No Hyper-V switch is named for VM-Servers'));
  assert(await f.page.locator('input[data-vm-move="quiesce"]').isChecked());
  // No-touch cutover: start, disconnect the old VM and fix up are on by default.
  for(const k of ['start','disconnect','fixUp']){assert(await f.page.locator('input[data-vm-after="'+k+'"]').isChecked(),k+' is ticked by default');}
  assert((await f.page.locator('#vmAfterNote').textContent()).includes('Enter the guest account'),'the fix-up asks for the guest account');
  // The signed-in account fills the name by itself; the password is still asked for, inline, without a request.
  assert.equal(await f.page.locator('#vmGaUser').inputValue(),'EXAMPLE\\operator');
  assert((await f.page.locator('#vmGaPanel').textContent()).includes('Windows cannot pass your sign-in into a VM'));
  {const pw=await f.page.locator('#vmGaPassword').boundingBox(),use=await f.page.locator('#vmGaUse').boundingBox();assert(use.y>=pw.y+pw.height+4,'Use this account sits below the password box');}
  await f.page.locator('#vmGaUse').click();assert.equal(await f.page.locator('#vmGaStatus').textContent(),'Type the password of EXAMPLE\\operator.');assert.equal(vmwareCalls(f,'guest-auto').length,0);
  // Values the browser fills in without an input event are still read.
  await f.page.locator('#vmGaUser').evaluate(e=>{e.value='EXAMPLE\\admin';});await f.page.locator('#vmGaPassword').evaluate(e=>{e.value='Synthetic-Guest-1';});await f.page.locator('#vmGaUse').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmGaPanel')&&document.querySelector('#vmGaPanel').textContent.includes('sign in to the new VMs as EXAMPLE\\admin'));
  assert.deepEqual(vmwareCalls(f,'guest-auto').map(c=>c.body),[{user:'EXAMPLE\\admin',password:'Synthetic-Guest-1'}]);
  assert.equal(await f.page.locator('#vmGaPassword').count(),0,'the password field is gone once the account is held');
  // Forget, then type it once with Remember ticked: one Enter keeps it, remembered, with no second try.
  await f.page.locator('#vmGaForget').click();await f.page.locator('#vmGaPassword').waitFor();
  await f.page.locator('#vmGaRemember').check();await f.page.locator('#vmGaPassword').fill('Synthetic-Guest-2');await f.page.locator('#vmGaPassword').press('Enter');
  await f.page.waitForFunction(()=>document.querySelector('#vmGaPanel').textContent.includes('kept encrypted for your Windows account'));
  assert.deepEqual(vmwareCalls(f,'guest-auto').at(-1).body,{user:'EXAMPLE\\admin',password:'Synthetic-Guest-2',remember:true});
  await f.page.locator('#vmMoveFolder').fill('E:\\Copies\\APP01');
  await f.page.locator('#vmMoveCopy').click();assert(await f.page.locator('#migrateDialog').isVisible());
  const copyText=await f.page.locator('#migrateDialogText').textContent();assert(copyText.includes('changed block tracking')&&copyText.includes('E:\\Copies\\APP01'),copyText);
  assert(await f.page.locator('#migrateConfirmButton').isDisabled());await f.page.locator('#migrateConfirmInput').fill('APP01');await f.page.locator('#migrateConfirmInput').press('Enter');
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  // The live progress panel: steps in order, the copy's percent, speed and time left.
  {const now=new Date().toISOString();
   f.state.vmware.moveStatus={vmId:'vm-app01',vm:'APP01',action:'copy',stages:['connect','snapshot','copy','cleanup'],stage:'copy',detail:'Hard disk 1 (1 of 2)',state:'running',started:now,updated:now};
   f.state.vmware.transfers=[{id:'move-vm-app01|Hard disk 1',kind:'move',label:'APP01: Hard disk 1',source:'APP01 (Hard disk 1)',target:'E:\\Copies\\APP01',size:107374182400,pass:1,chunks:25600,done:12800,bytesRead:53687091200,started:now,updated:now,state:'running',percent:50,bytesPerSecond:115343360,etaSeconds:465}];
   await f.page.waitForFunction(()=>{const p=document.querySelector('#vmMoveProgress');return p&&!p.classList.contains('hidden')&&p.textContent.includes('25% of the copy');});
   const progress=await f.page.locator('#vmMoveProgress').textContent();
   for(const want of ['Copying APP01','Running','Take the replication snapshot','Copy the disks: Hard disk 1 (1 of 2)','50.0 GB read','110 MB/s','about 8 min left on this disk'])assert(progress.includes(want),want+' in '+progress);
   assert.deepEqual(await f.page.locator('#vmMoveProgress .vm-move-steps li').evaluateAll(l=>l.map(x=>x.className)),['done','done','current','']);
   assert.equal(await f.page.locator('#vmMoveConsolidate').count(),0);}
  assert(await f.page.locator('#vmStop').isVisible(),'a copy can be stopped');
  assert.deepEqual(vmwareCalls(f,'move').map(c=>c.body),[{vmId:'vm-app01',action:'copy',copyFolder:'E:\\Copies\\APP01',vmFolder:'',quiesce:true,keepMac:false,start:true,cluster:false,disconnectSource:true,fixUp:true,confirm:'APP01'}]);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',move:{vm:'APP01',cutover:false,round:{disks:[{label:'Hard disk 1',mode:'seed',bytesWritten:21474836480,verified:false}]},disks:['E:\\Copies\\APP01\\APP01-disk0.vhdx']},moveSerial:1});
  await f.page.waitForFunction(()=>document.querySelector('#vmMove')&&document.querySelector('#vmMove').textContent.includes('first copy of the used areas'));
  assert.equal(await f.page.locator('#vmMoveCopy').textContent(),'Copy changes\u2026');
  // A disk that runs on a delta vSphere lists no snapshot for: the result says so and offers Consolidate disks.
  {const now=new Date().toISOString();f.state.vmware.canPrepare=true;f.state.vmware.transfers=[];
   f.state.vmware.moveStatus={vmId:'vm-app01',vm:'APP01',action:'copy',stages:['connect','snapshot','copy','cleanup'],stage:'connect',state:'failed',result:'Hard disk 1 still has a snapshot chain: the VM runs on [ds1] APP01/APP01-000001.vmdk on top of [ds1] APP01/APP01.vmdk. Consolidate the disks with Prepare source (Delete snapshots and consolidate), or Snapshots > Consolidate in vSphere, then copy again.',started:now,updated:now,finished:now};
   await f.page.locator('#vmMoveConsolidate').waitFor();assert((await f.page.locator('#vmMoveProgress').textContent()).includes('Failed'));
   assert.equal(await f.page.locator('#vmMoveProgress .vm-move-steps li.failed').textContent(),'Connect to the source');
   await f.page.locator('#vmMoveConsolidate').click();const text=await f.page.locator('#migrateDialogText').textContent();
   assert(text.includes('consolidate disks vSphere flags as needing it')&&text.includes('Server Inventory and Migration Manager consolidation'),text);await f.page.locator('#migrateDialogCancel').click();
   f.state.vmware.moveStatus=null;await f.page.waitForFunction(()=>document.querySelector('#vmMoveProgress').classList.contains('hidden'));}
  await f.page.locator('#vmMoveCutover').click();assert((await f.page.locator('#migrateDialogAlert').textContent()).includes('shut down through VMware Tools'));
  const cutList=await f.page.locator('#migrateDialogText').textContent();
  assert(cutList.includes('and start it')&&cutList.includes('set the old VM\'s network adapters to not connect')&&cutList.includes('put the IP settings back'),cutList);
  await f.page.locator('#migrateConfirmInput').fill('app01');await f.page.locator('#migrateConfirmButton').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert.equal(vmwareCalls(f,'move').at(-1).body.action,'cutover');assert.equal(vmwareCalls(f,'move').at(-1).body.start,true);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',message:'APP01 was created on Hyper-V and started.',move:{vm:'APP01',cutover:true,shutDown:true,round:{disks:[{label:'Hard disk 1',mode:'changed',bytesWritten:83886080,verified:true}]},hyperV:{generation:2},hyperVId:'4f6c1d6e-2b1a-4c55-9d3e-0f1e2d3c4b5a'},moveSerial:2});
  await f.page.waitForFunction(()=>document.querySelector('#vmMove').textContent.includes('Created on Hyper-V as APP01 (generation 2)'));
  assert((await f.page.locator('#vmMove').textContent()).includes('changed blocks only, 80.0 MB written, verified'));
  assert.equal(vmwareCalls(f,'move').at(-1).body.fixUp,true);
  // The fix-up's live status and result, with the manual fallback.
  f.state.vmware.fixups=[{vm:'APP01',hyperVId:'4f6c1d6e-2b1a-4c55-9d3e-0f1e2d3c4b5a',host:'',via:'PowerShell Direct on this host',state:'waiting',message:'Waiting for Windows in APP01 to start.',started:'2026-11-26T06:00:00Z'}];
  await f.page.waitForFunction(()=>{const x=document.querySelector('[data-vm-fixup="APP01"]');return x&&x.textContent.includes('Waiting for Windows to start');});
  f.state.vmware.fixups=[{vm:'APP01',hyperVId:'4f6c1d6e-2b1a-4c55-9d3e-0f1e2d3c4b5a',host:'',via:'PowerShell Direct on this host',state:'failed',message:'APP01 was not fully fixed up: No new adapter could be matched to Network adapter 1 (00-50-56-AA-BB-01). Choose it in Prepare and fix up. Use Prepare and fix up for this VM.',started:'2026-11-26T06:00:00Z',finished:'2026-11-26T06:04:00Z',
    steps:[{step:'Hidden adapters',status:'done',message:'Removed 1 hidden VMware adapter(s): vmxnet3 Ethernet Adapter.'},{step:'IP settings',status:'failed',message:'No new adapter could be matched to Network adapter 1 (00-50-56-AA-BB-01). Choose it in Prepare and fix up.'},{step:'VMware Tools',status:'done',message:'Removed VMware Tools.'}]}];
  await f.page.waitForFunction(()=>{const x=document.querySelector('[data-vm-fixup="APP01"]');return x&&x.textContent.includes('Not fixed up');});
  const fix=await f.page.locator('[data-vm-fixup="APP01"]').textContent();assert(fix.includes('IP settings: No new adapter could be matched')&&fix.includes('Open Prepare and fix up'),fix);
  await f.page.locator('[data-vm-fixup-run="APP01"]').click();
  assert.deepEqual(vmwareCalls(f,'guest-auto/run').map(c=>c.body),[{vm:'APP01',hyperVId:'4f6c1d6e-2b1a-4c55-9d3e-0f1e2d3c4b5a',host:''}]);
  f.state.vmware.fixups=[{vm:'APP01',hyperVId:'4f6c1d6e-2b1a-4c55-9d3e-0f1e2d3c4b5a',host:'',via:'PowerShell Direct on this host',state:'done',message:'APP01 is fixed up: IP settings, adapters, disks, VMware Tools and activation are done.',started:'2026-11-26T06:00:00Z',finished:'2026-11-26T06:09:00Z',
    steps:[{step:'IP settings',status:'done',message:'Put back 192.0.2.10/24 with the recorded gateway and DNS servers. The gateway 192.0.2.1 answers.'},{step:'Activation',status:'skipped',message:'Windows is activated (KMS).'}]}];
  await f.page.waitForFunction(()=>{const x=document.querySelector('[data-vm-fixup="APP01"]');return x&&x.textContent.includes('Fixed up')&&x.textContent.includes('192.0.2.10/24');});
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmMove').evaluate(e=>e.scrollIntoView({block:'center'}));await f.page.screenshot({path:shots+'/vmware-move-1440x1000.png'});
  await f.page.locator('#vmTargetSelect').selectOption('scale');await f.page.waitForFunction(()=>!document.querySelector('#vmMove'));
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Move VMs from one console: connect to a remote Hyper-V cluster, pick a node and Cluster Shared Volume, copy over its admin share or a receiver, cut over with high availability, and label the supported systems',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);
  Object.assign(f.state.vmware,{canPrepare:true,hyperVHere:false,inventorySerial:2});
  await goTool(f,'movevms');await openVm(f,'vm-app01');await f.page.locator('#vmHvrConnect').waitFor();
  assert.equal(await f.page.locator('[data-vm-hvwhere="remote"]').getAttribute('aria-pressed'),'true','off a Hyper-V host the remote host is the default');
  const works=await f.page.locator('#vmHvrWorks').textContent();
  assert(works.includes('ESXi 6.5, 6.7, 7.0 or 8.0')&&works.includes('Windows Server 2012 R2, 2016, 2019, 2022 or 2025')&&works.includes('failover cluster with Cluster Shared Volumes')&&works.includes('free ESXi'),works);
  assert((await f.page.locator('#vmMove .vm-runs-on').textContent()).includes('Runs on this computer, your admin computer'));
  await f.page.locator('[data-hvr="host"]').fill('hvclu01.example.com');await f.page.locator('#vmHvrSignInOther').check();await f.page.locator('[data-hvr="user"]').fill('EXAMPLE\\admin');await f.page.locator('[data-hvr="password"]').fill('Synthetic-Pass-1');
  await f.page.locator('#vmHvrConnect').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert.deepEqual(vmwareCalls(f,'hvremote/connect').map(c=>c.body),[{host:'hvclu01.example.com',user:'EXAMPLE\\admin',password:'Synthetic-Pass-1'}]);
  const info={host:'hvclu01.example.com',computer:'HV01',caption:'Microsoft Windows Server 2022 Datacenter',build:20348,systemDrive:'C:',hyperV:true,hyperVModule:true,clusterModule:true,cluster:'HVCLU01',vhdPath:'C:\\ClusterStorage\\Volume1\\Hyper-V',vmPath:'C:\\ClusterStorage\\Volume1\\Hyper-V',
   nodes:[{name:'HV01',state:'Up'},{name:'HV02',state:'Up'},{name:'HV03',state:'Paused'}],volumes:[{path:'C:\\',label:'',freeBytes:42949672960,totalBytes:137438953472,csv:false},{path:'C:\\ClusterStorage\\Volume1',label:'CSV1',freeBytes:536870912000,totalBytes:2199023255552,csv:true},{path:'C:\\ClusterStorage\\Volume2',label:'CSV2',freeBytes:1073741824,totalBytes:107374182400,csv:true}],
   switches:[{name:'LAN',type:'External'}],vms:['OTHER01'],warnings:[],supported:true,support:'Windows Server 2022 Datacenter is supported; failover cluster HVCLU01 can make the VM highly available.'};
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',hvRemote:{connected:true,host:'hvclu01.example.com',user:'EXAMPLE\\admin',info,receiver:'',message:'Connected to HV01.',failed:false,serial:1}});
  await f.page.waitForFunction(()=>!!document.querySelector('#vmHvrCopy'));
  assert.equal(await f.page.locator('[data-hvr="password"]').count(),0,'the password field is gone once connected');
  assert((await f.page.locator('#vmHvrFacts').textContent()).includes('Connected to HV01 in failover cluster HVCLU01 as EXAMPLE\\admin'));
  assert(await f.page.locator('#vmHvrNode option[value="HV03"]').evaluate(o=>o.disabled),'a paused node cannot be chosen');
  await f.page.locator('#vmHvrNode').selectOption('HV02');
  await f.page.locator('[data-hvr-storage]').nth(2).click();
  assert((await f.page.locator('#vmHvrSpace').textContent()).includes('Not enough'));assert(await f.page.locator('#vmHvrCopy').isDisabled(),'a volume that is too small refuses the copy');
  await f.page.locator('[data-hvr-storage]').nth(1).click();
  assert.equal(await f.page.locator('[data-hvr="folder"]').inputValue(),'C:\\ClusterStorage\\Volume1\\VMs\\APP01');assert.equal(await f.page.locator('[data-hvr="vmFolder"]').inputValue(),'C:\\ClusterStorage\\Volume1\\VMs');
  assert(await f.page.locator('input[data-hvr-opt="cluster"]').isChecked(),'a CSV ticks high availability');assert(!await f.page.locator('#vmHvrCopy').isDisabled());
  assert((await f.page.locator('#vmHvrBrowseNote').textContent()).includes('cannot browse the node'));
  await f.page.locator('[data-hvr="folder"] + .path-pick-button').click();await f.page.waitForFunction(()=>document.querySelector('[data-hvr="folder"]').value==='C:\\ClusterStorage\\Volume1\\Picked');
  const hvrPick=f.pickCalls[f.pickCalls.length-1];assert.deepEqual({kind:hvrPick.kind,host:hvrPick.host,current:hvrPick.current},{kind:'remote',host:'HV02',current:'C:\\ClusterStorage\\Volume1\\VMs\\APP01'},'Browse host opens the chosen node');
  await f.page.locator('[data-hvr-storage]').nth(1).click();
  await f.page.locator('#vmHvrCopy').click();const copyText=await f.page.locator('#migrateDialogText').textContent();assert(copyText.includes('C:\\ClusterStorage\\Volume1\\VMs\\APP01 on HV02'),copyText);
  await f.page.locator('#migrateConfirmInput').fill('APP01');await f.page.locator('#migrateConfirmInput').press('Enter');
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');assert(await f.page.locator('#vmStop').isVisible(),'a remote copy can be stopped');
  const sent=vmwareCalls(f,'hvremote/move').at(-1).body;
  assert.deepEqual(sent,{vmId:'vm-app01',action:'copy',node:'HV02',copyFolder:'C:\\ClusterStorage\\Volume1\\VMs\\APP01',vmFolder:'C:\\ClusterStorage\\Volume1\\VMs',path:'share',address:'',thumbprint:'',code:'',quiesce:true,keepMac:false,start:true,cluster:true,disconnectSource:true,fixUp:true,confirm:'APP01'});
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',move:{vm:'APP01',cutover:false,round:{disks:[{label:'Hard disk 1',mode:'seed',bytesWritten:21474836480,verified:false}]},disks:['C:\\ClusterStorage\\Volume1\\VMs\\APP01\\APP01-disk0.vhdx']},moveSerial:1});
  await f.page.waitForFunction(()=>document.querySelector('#vmHvrCopy')&&document.querySelector('#vmHvrCopy').textContent==='Copy changes\u2026');
  // A receiver on the node: its pairing code is sent once and cleared.
  await f.page.locator('input[data-hvr-path="receiver"]').check();await f.page.locator('[data-hvr="address"]').waitFor();
  await f.page.locator('[data-hvr="address"]').fill('hv02.example.com:44380');await f.page.locator('[data-hvr="code"]').fill('K7QMZ-2RT4X-9WBNP-H3VDA');await f.page.locator('[data-hvr="thumbprint"]').fill('3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e');
  await f.page.locator('#vmHvrCutover').click();const cutText=await f.page.locator('#migrateDialogText').textContent();
  assert(cutText.includes('read the Hyper-V host again')&&cutText.includes('make it highly available in HVCLU01'),cutText);
  await f.page.locator('#migrateConfirmInput').fill('app01');await f.page.locator('#migrateConfirmButton').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  const cut=vmwareCalls(f,'hvremote/move').at(-1).body;assert.equal(cut.action,'cutover');assert.equal(cut.path,'receiver');assert.equal(cut.code,'K7QMZ-2RT4X-9WBNP-H3VDA');assert.equal(cut.cluster,true);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',move:{vm:'APP01',cutover:true,shutDown:true,round:{disks:[{label:'Hard disk 1',mode:'changed',bytesWritten:83886080,verified:true}]},hyperV:{generation:2,clusterResult:'CLUSTER-ROLE-ADDED'},hyperVId:'4f6c1d6e-2b1a-4c55-9d3e-0f1e2d3c4b5a'},moveSerial:2});
  await f.page.waitForFunction(()=>document.querySelector('#vmMove').textContent.includes('Made highly available in the failover cluster.'));
  assert.equal(await f.page.locator('[data-hvr="code"]').inputValue(),'','the pairing code is cleared after use');
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmMove').evaluate(e=>e.scrollIntoView({block:'start'}));await f.page.screenshot({path:shots+'/vmware-move-remote-1440x1000.png'});
  await goTool(f,'movevms');await openVm(f,'vm-app01');await f.page.locator('#vmHvrForget').click();
  await f.page.waitForFunction(()=>!!document.querySelector('#vmHvrConnect'));
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Migration UX: compact tool list with About this tool, empty screens that start New migration, a sortable VM table with step summaries, and Migrate from Discovery',async()=>{const f=await fixture(browser);try{
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();
  f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',account:'administrator@vsphere.local',summary:vmwareSummaryFixture(),inventorySerial:1,canPrepare:true,hyperVHere:true,hostStorage:{known:true,vhdPath:'D:\\Hyper-V\\Disks',vmPath:'D:\\Hyper-V',clustered:false,volumes:[{path:'D:\\',freeBytes:1099511627776,totalBytes:2199023255552,csv:false}]}});
  await f.page.keyboard.press('Alt+1');await readmeShot(f,'hero.png');
  await goTool(f,'plans');
  // The list shows each tool's name and one line; Works with and its tags are folded into the tool's About this tool.
  assert(!await f.page.locator('[data-tool="plans"] .tool-works').isVisible(),'Works with is not on the card');assert(!await f.page.locator('[data-tool="replicate"] .tool-runs').isVisible(),'only the selected card says where it runs');assert(await f.page.locator('[data-tool="plans"] .tool-runs').isVisible());
  assert(!await f.page.locator('#toolWorks-tabPlans').isVisible(),'About this tool starts folded');await f.page.locator('#toolAbout-tabPlans summary').click();
  const about=await f.page.locator('#toolWorks-tabPlans').textContent();assert(about.includes('Works with')&&about.includes('Pick this when')&&about.includes('Runs on the Hyper-V host'),about);
  assert.equal(await f.page.locator('.hero').count(),0,'no banner above the tabs on any screen');
  for(const size of [{width:1440,height:1000},{width:1008,height:690},{width:784,height:522}]){await f.page.setViewportSize(size);await f.page.keyboard.press('Alt+3');
   const bar=f.page.locator('.status-bar');assert(await bar.isVisible(),'the status bar shows at '+size.width);assert((await bar.textContent()).includes('Interface v'),'with the version');
   const box=await bar.boundingBox();assert(box.y+box.height<=size.height&&box.height<=30,'a slim bar at the bottom: '+JSON.stringify(box));}
  assert.equal(await f.page.locator('.topbar').count(),0,'no header bar');assert(!(await f.page.locator('body').textContent()).includes('Portable EXE'));
  assert((await f.page.locator('#statusConnection').getAttribute('title')).includes('connected to the application'));assert(await f.page.locator('#helpMenuButton').isVisible(),'Help sits beside the tabs');
  await f.page.setViewportSize({width:1440,height:1000});
  // Empty screens offer New migration.
  await goTool(f,'plans');await f.page.locator('#mpEmptyWizard').click();assert.equal(await f.page.locator('[data-tool="wizard"]').getAttribute('aria-pressed'),'true');assert.equal(await f.page.locator('[data-nw-source="vms"]').getAttribute('aria-pressed'),'true','it opens with VMs chosen');
  await goTool(f,'replicate');await f.page.waitForSelector('#rpEmptyWizard');assert(await f.page.locator('#rpSummary').isHidden(),'no row of zero tiles');
  await f.page.locator('#rpEmptyWizard').click();assert.equal(await f.page.locator('[data-nw-source="servers"]').getAttribute('aria-pressed'),'true','Replicate servers starts New migration with servers chosen');
  // The VM table sorts, ticks every VM shown and folds the VMs that cannot move.
  await f.page.locator('[data-nw-source="vms"]').click();await f.page.waitForSelector('#nwVmList [data-nw-pick]');
  const order=()=>f.page.locator('#nwVmList table').first().locator('tbody strong').allTextContents();
  assert.deepEqual(await order(),['APP01','SQL01','WEB02']);await f.page.locator('[data-nw-sort="ready"]').first().click();{const o=await order();assert.deepEqual(o,['APP01','WEB02','SQL01'],'Readiness sorts ready first: '+o);}
  await f.page.locator('[data-nw-sort="ready"]').first().click();assert.equal(await f.page.locator('#nwVmList table').first().locator('th[aria-sort="descending"]').count(),1);
  assert(!await f.page.locator('[data-nw-pick="vm-legacy03"]').isVisible(),'a VM that cannot move is folded away');await f.page.locator('.nw-vm-off summary').click();assert(await f.page.locator('[data-nw-pick="vm-legacy03"]').isVisible());
  // A redraw from the live refresh keeps it open (owner, 2026-11-27: it folded by itself after a few seconds).
  await f.page.evaluate(()=>{window.__nwBefore=document.querySelector('#nwVmOff');});f.state.vmware.canPrepare=!f.state.vmware.canPrepare;
  await f.page.waitForFunction(()=>document.querySelector('#nwVmOff')!==window.__nwBefore,null,{timeout:10000});assert(await f.page.locator('#nwVmOff').evaluate(d=>d.open),'Show why stays open through a redraw');
  assert((await f.page.locator('[data-nw-pick="vm-sql01"]').locator('xpath=ancestor::tr').textContent()).includes('1 snapshot'),'a Review first VM says why');
  await f.page.locator('#nwPickShown').check();assert((await f.page.locator('[data-nw="count"]').textContent()).startsWith('3 VMs ticked'));await readmeShot(f,'new-migration.png');
  await f.page.locator('#nwNext').click();const done=await f.page.locator('.nw-stepper-item.done').first().textContent();assert(done.includes('3 VMs'),'the step bar sums up a finished step: '+done);
  await f.page.locator('[data-nw-target="here"]').click();await f.page.locator('[data-nw-step="0"]').click();assert.equal(await f.page.locator('.nw-stepper-item.current').textContent(),'1What','a finished step can be opened again');
  // Discovery starts a migration with the VMs it shows.
  await goTool(f,'environment');await f.page.locator('#vmNwScopeButton').waitFor();assert.equal(await f.page.locator('#vmNwScopeButton').textContent(),'Migrate 3 VMs…','the blocked VM is not counted');
  await f.page.locator('#vmNwScopeButton').click();assert.equal(await f.page.locator('[data-tool="wizard"]').getAttribute('aria-pressed'),'true');assert((await f.page.locator('[data-nw="count"]').textContent()).startsWith('3 VMs ticked'));
  // Move VMs points a Hyper-V move to New migration first.
  await goTool(f,'movevms');await openVm(f,'vm-app01');await f.page.locator('#vmMovePlanButton').click();assert.equal(await f.page.locator('[data-tool="wizard"]').getAttribute('aria-pressed'),'true');assert((await f.page.locator('[data-nw="count"]').textContent()).startsWith('1 VM ticked'));
  await goTool(f,'environment');await openVm(f,'vm-web02');await f.page.locator('#vmNwVmButton').click();assert((await f.page.locator('[data-nw="count"]').textContent()).startsWith('1 VM ticked'));assert(await f.page.locator('[data-nw-pick="vm-web02"]').isChecked());
  assert.deepEqual(f.errors,[]);assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Migration UX: a status track with the next step, plain-language errors with a fix button, and an activity strip on every screen',async()=>{const f=await fixture(browser);try{
  const row=(o)=>Object.assign({percent:-1,lagSeconds:-1,wave:0,actions:[],detail:'',reason:''},o);
  f.servers.rows=[
   row({key:'plan:p1|vm-a',kind:'plan',method:'Migration plan',name:'APP01',plan:'p1',vm:'vm-a',state:'insync',stateText:'In sync',lagSeconds:300,actions:['sync','test','cutover']}),
   row({key:'plan:p1|vm-b',kind:'plan',method:'Migration plan',name:'WEB01',plan:'p1',vm:'vm-b',state:'seeding',stateText:'Initial copy',percent:30,actions:['sync']}),
   row({key:'job:a1|j1',kind:'job',method:'Agent replication',name:'SQL01',agent:'a1',job:'j1',state:'failed',stateText:'Failed',reason:'The last round failed: The receiver did not answer (timeout).',actions:['sync']})];
  f.servers.summary={total:3,states:{insync:1,seeding:1,failed:1},scheduled:0,methods:{}};
  await f.page.waitForSelector('#activityStrip:not(.hidden)',{timeout:15000});
  const strip=await f.page.locator('#activityStrip').textContent();assert(strip.includes('1 server needs attention')&&strip.includes('1 copying')&&strip.includes('1 in sync'),strip);
  await f.page.locator('#activityStrip [data-act-filter="failed"]').click();
  assert.equal(await f.page.locator('[data-tool="servers"]').getAttribute('aria-pressed'),'true','the strip opens All jobs and servers');
  await f.page.waitForFunction(()=>document.querySelectorAll('#asRows tr').length===1);
  assert(await f.page.locator('#activityStrip').isHidden(),'the strip is not shown over the list it summarises');
  assert((await f.page.locator('#asRows .err-help').textContent()).length>10,'a failure is explained in plain words');
  // A plan VM shows where it is on the way and what comes next; a known error gets its fix.
  f.plans.plans=[{id:'p1',name:'Wave 1',server:'vc01.example.com',paused:false,settings:{syncMinutes:60,concurrency:2},throttle:'no limit',summary:{total:2,waiting:0,copying:0,ready:1,testing:0,scheduled:0,done:0,failed:1},vms:[
   {id:'vm-a',name:'APP01',wave:1,state:'ready',seeded:true,failures:0,testedAt:'0001-01-01T00:00:00Z'},
   {id:'vm-b',name:'WEB01',wave:1,state:'failed',seeded:false,failures:2,error:'[DS1] WEB01/WEB01-flat.vmdk: the server answered 500 Internal Server Error'},
   {id:'vm-c',name:'FILE01',wave:2,state:'ready',seeded:true,failures:0,lastSync:new Date().toISOString(),testedAt:new Date(Date.now()-3600000).toISOString()}]}];
  await goTool(f,'plans');await f.page.waitForSelector('#mpRows .vm-track');
  const track=await f.page.locator('#mpRows tr').filter({hasText:'APP01'}).locator('.vm-track').textContent();assert(track.includes('Copy')&&track.includes('Cut over'),track);
  assert((await f.page.locator('#mpRows tr').filter({hasText:'APP01'}).textContent()).includes('Next: test it'),'the next step is named');
  await readmeShot(f,'migration-plans.png');
  // Go/no-go per wave: wave 1 is not ready (a failed first copy), wave 2 is.
  const w1=f.page.locator('[data-mp-go="1"]'),w2=f.page.locator('[data-mp-go="2"]');
  assert.equal(await w1.locator('.mp-chip').textContent(),'Not yet');assert((await w1.textContent()).includes('WEB01 failed')||(await w1.textContent()).includes('1 VM failed'),await w1.textContent());
  assert((await w1.textContent()).includes('not tested yet: APP01'),'an untested VM is named');
  // Go's zero time is not a test: the track agrees with the wave check (3.3.0-test.3 showed Tested done beside "not tested yet").
  assert(!(await f.page.locator('#mpRows tr').filter({hasText:'APP01'}).locator('.vm-track .done').allTextContents()).some(t=>t.includes('Tested')),'an untested VM is not shown as tested');
  await w1.locator('[data-mp-marktested]').click();for(let i=0;i<50&&!f.planCalls.some(c=>c.body.action==='markTested');i++){await f.page.waitForTimeout(100);}
  assert.deepEqual(f.planCalls.find(c=>c.body.action==='markTested').body.vmIds,['vm-a','vm-b'],'a test run another way can be recorded for the wave');assert(await w1.locator('[data-mp-gowave]').isDisabled());
  assert.equal(await w2.locator('.mp-chip').textContent(),'Go',await w2.textContent());
  await w2.locator('[data-mp-gowave]').click();assert.equal(await f.page.locator('#mpSelCount').textContent(),'1 VM(s) selected.');assert(await f.page.locator('[data-mp-row="vm-c"]').isChecked(),'the wave is selected for the cutover');
  const help=f.page.locator('#mpRows tr').filter({hasText:'WEB01'}).locator('.err-help');assert((await help.textContent()).includes('ESXi'),await help.textContent());
  await help.locator('[data-err-fix]').click();for(let i=0;i<50&&!f.planCalls.some(c=>c.body.action==='sync');i++){await f.page.waitForTimeout(100);}
  const sync=f.planCalls.filter(c=>c.body.action==='sync').at(-1);assert(sync&&sync.body.vmIds[0]==='vm-b','the fix copies that VM again');
  // A SQL and DC check that could not reach its server is one line per server, with the remote error under Details.
  const raw='Connecting to remote server sql01.example.com failed with the following error message : The client cannot connect to the destination specified in the request. Verify that the service on the destination is running and is accepting requests.\nFor more information, see the about_Remote_Troubleshooting Help topic.';
  await vmwareConnected(f);const sqlVm=f.vm.inventory.vms.find(v=>v.id==='vm-sql01');
  sqlVm.readiness.forEach(r=>r.findings.push({severity:'warning',code:'preflight-failed',message:'SQL and DC checks: couldn\'t reach sql01.example.com over WinRM (TCP 5985). (2026-11-27 06:33)',action:'Check the computer name.',detail:raw}));
  f.state.vmware.inventorySerial++;await f.page.waitForTimeout(1500);
  f.plans.plans[0].vms.push({id:'vm-sql01',name:'SQL01',wave:3,state:'ready',seeded:true,failures:0,lastSync:new Date().toISOString(),testedAt:new Date().toISOString()});
  await goTool(f,'plans');const w3=f.page.locator('[data-mp-go="3"]');await w3.waitFor();
  const item=w3.locator('.mp-go-items li');assert.equal(await item.count(),1);
  assert.equal(await item.evaluate(li=>li.firstChild.textContent),'SQL01: SQL and DC checks: couldn\'t reach sql01.example.com over WinRM (TCP 5985). (2026-11-27 06:33)');
  assert(!await item.locator('pre').isVisible(),'the raw error is folded');await item.locator('summary').click();assert.equal(await item.locator('pre').textContent(),raw,'Details shows the full error');
  assert.equal(await w3.locator('.mp-chip').textContent(),'Check first');
  assert.deepEqual(f.errors,[]);assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Migration UX: saved defaults per customer fill New migration and Migration plans, without any password, and a rolling conversion runs stage by stage',async()=>{const f=await fixture(browser);try{
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();
  f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',account:'administrator@vsphere.local',summary:vmwareSummaryFixture(),inventorySerial:1,canPrepare:true,hyperVHere:true,hostStorage:{known:true,vhdPath:'D:\\Hyper-V\\Disks',vmPath:'D:\\Hyper-V',clustered:false,volumes:[{path:'D:\\',freeBytes:1099511627776,totalBytes:2199023255552,csv:false}]}});
  f.profiles.push({name:'Contoso',copyRoot:'E:\\Contoso\\Copies',vmFolder:'E:\\Contoso\\VMs',testSwitch:'',syncMinutes:30,concurrency:3,throttle:{businessMbps:200,limitMbps:0,businessStart:8,businessEnd:18},options:{keepMac:true,startVms:true,cluster:false,quiesce:false,ejectMedia:true,disconnectSource:true,fixUp:false},networkMap:[]});
  await goTool(f,'wizard');await f.page.locator('[data-nw-source="vms"]').click();await f.page.waitForSelector('#nwVmList [data-nw-pick]');
  await f.page.locator('#nwPickShown').check();await f.page.locator('#nwNext').click();await f.page.locator('[data-nw-target="here"]').click();
  await f.page.waitForSelector('#nwProfilePick option[value="Contoso"]',{state:'attached'});
  assert(await f.page.locator('#nwProfileUse').isDisabled(),'nothing to use until a customer is chosen');
  await f.page.locator('#nwProfilePick').selectOption('Contoso');assert((await f.page.locator('#nwProfileNote').textContent()).includes('sync every 30'),await f.page.locator('#nwProfileNote').textContent());
  await f.page.locator('#nwProfileUse').click();assert.equal(await f.page.locator('#nwCopyRoot').inputValue(),'E:\\Contoso\\Copies');assert.equal(await f.page.locator('#nwVMFolder').inputValue(),'E:\\Contoso\\VMs');
  assert((await f.page.locator('#nwProfileStatus').textContent()).startsWith('Filled in from Contoso'));
  await f.page.locator('#nwProfileName').fill('Fabrikam');await f.page.locator('#nwProfileSave').click();await f.page.waitForFunction(()=>document.querySelector('#nwProfileStatus').textContent.startsWith('Saved as Fabrikam'));
  const saved=f.profileCalls.filter(c=>c.body.action==='save').at(-1).body.profile;assert.equal(saved.copyRoot,'E:\\Contoso\\Copies');assert.equal(saved.syncMinutes,30);assert(!JSON.stringify(saved).toLowerCase().includes('password'),'no password is saved');
  // Rolling conversion inside New migration: the stages come from the plan, one ESXi host at a time.
  for(const sel of await f.page.locator('[data-nw-switch]').all()){await sel.selectOption('Isolated');}
  await f.page.locator('#nwNext').click();await f.page.locator('#nwNext').click();
  await f.page.locator('input[data-nw-wavemode="rolling"]').check();await f.page.waitForFunction(()=>document.querySelectorAll('[data-nw-stage]').length===3);
  const plan=vmwareCalls(f,'plan').at(-1).body;assert.equal(plan.plan.scenario,'rolling');assert.equal(plan.suggestStages,true);
  const st=await f.page.locator('[data-nw-stage="1"]').textContent();assert(st.includes('esx03.example.com')&&st.includes('Build Hyper-V hosts'),st);
  assert((await f.page.locator('[data-nw-stage="3"]').textContent()).includes('onto the Hyper-V nodes built so far'),'the last host moves its wave first');
  await f.page.locator('[data-nw-tool="hvbuild"]').click();assert.equal(await f.page.locator('[data-tool="hvbuild"]').getAttribute('aria-pressed'),'true');
  // Migration plans' new plan form fills from the same defaults.
  await goTool(f,'plans');await f.page.locator('#mpNewButton').click();await f.page.waitForSelector('#mpProfilePick option[value="Fabrikam"]',{state:'attached'});
  await f.page.locator('#mpProfilePick').selectOption('Contoso');await f.page.locator('#mpProfileUse').click();
  assert.equal(await f.page.locator('#mpSync').inputValue(),'30');assert.equal(await f.page.locator('#mpConcurrency').inputValue(),'3');assert.equal(await f.page.locator('#mpCopyRoot').inputValue(),'E:\\Contoso\\Copies');assert.equal(await f.page.locator('#mpBizMbps').inputValue(),'200');assert(!await f.page.locator('#mpFixUp').isChecked());
  assert.deepEqual(f.errors,[]);assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Build Hyper-V hosts: draft a plan from the VMware inventory, preview a step, and apply only after typing the plan name',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);await goTool(f,'hvbuild');
  const card=await f.page.locator('[data-tool="hvbuild"]').textContent();assert(card.includes('Runs on'),'the tool says where it runs');
  assert((await f.page.locator('#tabHvBuild').textContent()).includes('Pick this when'),'the tool says when to pick it');
  await f.page.locator('#hbSourceCluster').waitFor();assert(await f.page.locator('#hbSourceCluster option').count()>1,'the VMware clusters are offered');
  await f.page.locator('#hbSuggest').click();
  await f.page.waitForFunction(n=>document.querySelectorAll('#hbSteps li[data-hb-step]').length===n,hvBuildStepCount);
  assert.equal(vmwareCalls(f,'hvbuild/suggest').length,1);assert.equal(vmwareCalls(f,'hvbuild/save').length,1,'a drafted plan is saved');
  assert.equal(await f.page.locator('#hbName').inputValue(),'Prod');
  assert(await f.page.locator('[data-hb-apply="roles"]').isDisabled(),'nothing can be applied before a preview');
  await f.page.locator('[data-hb-preview="roles"]').click();assert.equal(vmwareCalls(f,'hvbuild/preview').at(-1).body.step,'roles');
  const hb=f.state.vmware.hvBuild;
  Object.assign(f.state.vmware,{running:false,action:'',status:'idle'});
  hb.results={roles:{step:'roles',revision:hb.revision,mode:'preview',at:new Date().toISOString(),changes:2,approvable:true,runs:[
   {computer:'HV1.example.com',ok:true,output:{computer:'HV1',changes:['Install Hyper-V, Failover-Clustering, Multipath-IO (a restart follows)'],notes:['Windows Server 2022 Datacenter'],blocks:[],hash:'a'.repeat(64)}},
   {computer:'HV2.example.com',ok:true,output:{computer:'HV2',changes:['Install Hyper-V, Failover-Clustering, Multipath-IO (a restart follows)'],notes:[],blocks:[],hash:'b'.repeat(64)}}]}};
  hb.serial++;
  await f.page.waitForFunction(()=>!document.querySelector('[data-hb-apply="roles"]').disabled);
  assert((await f.page.locator('[data-hb-step="roles"]').textContent()).includes('2 changes to review'));
  await f.page.locator('[data-hb-apply="roles"]').click();
  const text=await f.page.locator('#migrateDialog').textContent();assert(text.includes('HV1.example.com: Install Hyper-V')&&text.includes('HV2.example.com: Install Hyper-V'),'the dialog lists every change');
  await f.page.locator('#migrateConfirmInput').fill('Pro');assert(await f.page.locator('#migrateConfirmButton').isDisabled(),'a wrong name does not approve');
  await f.page.locator('#migrateConfirmInput').fill('Prod');await f.page.locator('#migrateConfirmButton').click();
  await f.page.waitForFunction(()=>document.querySelector('#hvbuildBadge').textContent==='Working');
  const body=vmwareCalls(f,'hvbuild/apply').at(-1).body;assert.equal(body.step,'roles');assert.equal(body.confirm,'Prod');
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Build Hyper-V hosts: join an existing cluster, read a node, fill in its choices and keep what the operator changed',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);await goTool(f,'hvbuild');
  await f.page.locator('#hbModeJoin').check();await f.page.locator('#hbExistingName').fill('HVCLU01');
  assert(await f.page.locator('#hbSuggest').isDisabled(),'nothing is drafted before the cluster is read');
  await f.page.locator('#hbReadExisting').click();assert.deepEqual(vmwareCalls(f,'hvbuild/readexisting').at(-1).body,{name:'HVCLU01',kind:'join'});
  const hb=f.state.vmware.hvBuild;Object.assign(f.state.vmware,{running:false,action:'',status:'idle'});hb.existing=JSON.parse(JSON.stringify(hvBuildExistingRead));hb.serial++;
  await f.page.waitForFunction(()=>document.querySelector('#hbExisting')&&document.querySelector('#hbExisting').textContent.includes('Cluster HVCLU01'));
  assert((await f.page.locator('#hbExisting').textContent()).includes('Switch SETswitch'),'the cluster\'s switches are listed');
  assert.equal(await f.page.locator('#hbSuggest').textContent(),'Draft plan to join HVCLU01');
  await f.page.locator('#hbSuggest').click();await f.page.waitForFunction(()=>!!document.querySelector('#hbNext'));
  const draft=vmwareCalls(f,'hvbuild/suggest').at(-1).body;assert.equal(draft.existing,true);assert.equal(draft.reuse,true);
  assert((await f.page.locator('#hbNext').textContent()).includes('Next host to convert: esx03.example.com'),'the host still on ESXi is the next one');
  assert(await f.page.locator('#hbNextReady').isDisabled(),'it cannot be read before it has a Windows name');
  assert.equal(await f.page.locator('#hbClusterName').inputValue(),'HVCLU01');assert.equal(await f.page.locator('#hbClusterAddress').count(),0,'joining never asks for a new cluster address or quorum');
  const ports='[data-hb-ports="HV2.example.com|SETswitch"]';assert.equal(await f.page.locator(ports+' [data-hb-readnodes]').count(),1,'an unread node offers Read nodes, not a text field');
  await f.page.locator('#hbRead').click();assert(vmwareCalls(f,'hvbuild/read').length>=1);
  Object.assign(f.state.vmware,{running:false,action:'',status:'idle'});hb.facts={nodes:{'HV2.example.com':JSON.parse(JSON.stringify(hvBuildNodeFacts))}};hb.serial++;
  await f.page.waitForSelector(ports+' [data-hb-port]');
  const ticked=async name=>f.page.locator(ports+' [data-hb-port="'+name+'"]').isChecked();
  assert(await ticked('SLOT 1 Port 1')&&await ticked('SLOT 1 Port 2'),'the two 25 Gbps ports are ticked, like the cluster\'s switch');assert(!await ticked('Embedded 1'),'the 1 Gbps port is left out');
  assert.equal(await f.page.locator(ports+' .hb-tag').textContent(),'Suggested');assert((await f.page.locator(ports+' .hb-why').textContent()).includes('25 Gbps'),'the reason is shown');
  const lm=f.page.locator('label.hb-field',{hasText:'Address on HV2.example.com'});assert.equal(await lm.locator('input').inputValue(),'10.0.30.12/24','the next live migration address after the existing node\'s');
  assert.equal(await lm.locator('.hb-tag').textContent(),'Suggested: check it is free');
  assert((await f.page.locator('.hb-card',{hasText:'Add iSCSI adapter'}).locator('select').first().inputValue())==='SLOT 3 Port 1','the port already in the iSCSI subnet is the iSCSI adapter');
  assert((await f.page.locator('#hbSaveStatus').textContent()).includes('Filled in'),'the page says it filled values in');
  await f.page.locator(ports+' [data-hb-port="SLOT 1 Port 2"]').uncheck();assert.equal(await f.page.locator(ports+' .hb-tag').textContent(),'Set by you');
  await lm.locator('input').fill('10.0.30.40/24');assert.equal(await lm.locator('.hb-tag').textContent(),'Set by you');
  await f.page.locator('#hbSave').click();await f.page.waitForFunction(()=>document.querySelector('#hbSave').textContent==='Save plan');
  const saved=vmwareCalls(f,'hvbuild/save').at(-1).body.plan.nodes[0];assert(saved.suggested.includes('!switch:SETswitch')&&saved.suggested.includes('!ip:LM'),'the operator\'s values are recorded as theirs');
  hb.facts={nodes:{'HV2.example.com':JSON.parse(JSON.stringify(hvBuildNodeFacts))}};hb.serial++;await f.page.waitForTimeout(400);
  assert(!await ticked('SLOT 1 Port 2'),'a new read keeps the port the operator unticked');assert.equal(await lm.locator('input').inputValue(),'10.0.30.40/24','and the address they typed');
  await f.page.locator('#hbNextName').fill('HV3.example.com');await f.page.locator('#hbNextReady').waitFor();assert(!await f.page.locator('#hbNextReady').isDisabled());
  const reads=vmwareCalls(f,'hvbuild/read').length;await f.page.locator('#hbNextReady').click();
  await f.page.waitForTimeout(400);
  const after=vmwareCalls(f,'hvbuild/save').at(-1).body.plan.nodes[1];assert.equal(after.name,'HV3.example.com');assert(!after.later,'the converted host is ready');assert(after.suggested.includes('!later'));
  assert.equal(vmwareCalls(f,'hvbuild/read').length,reads+1,'and it is read');
  assert.deepEqual(f.errors,[]);assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Hyper-V host storage: the move offers Cluster Shared Volumes with free space, refuses one too small, and can make the VM highly available at the cutover',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);
  Object.assign(f.state.vmware,{canPrepare:true,hyperVHere:true,moveCopyRoot:'C:\\ProgramData\\Microsoft\\Windows\\Hyper-V\\Virtual Hard Disks',inventorySerial:2,hostStorage:{known:true,vhdPath:'C:\\ProgramData\\Microsoft\\Windows\\Hyper-V\\Virtual Hard Disks',vmPath:'C:\\ProgramData\\Microsoft\\Windows\\Hyper-V',clustered:true,volumes:[
   {path:'C:\\ClusterStorage\\Volume1',label:'Volume1',freeBytes:536870912000,totalBytes:2199023255552,csv:true},{path:'C:\\ClusterStorage\\Volume2',label:'Volume2',freeBytes:21474836480,totalBytes:107374182400,csv:true},{path:'C:\\',label:'',freeBytes:85899345920,totalBytes:274877906944,csv:false}]}});
  await goTool(f,'movevms');await openVm(f,'vm-app01');await f.page.locator('#vmMoveCopy').waitFor();
  assert.equal(await f.page.locator('[data-vm-storage]').count(),3);
  assert(await f.page.locator('[data-vm-storage]').nth(1).evaluate(e=>e.classList.contains('short')),'a volume smaller than the VM is marked');
  assert.equal(await f.page.locator('#vmMoveFolder').getAttribute('placeholder'),'C:\\ProgramData\\Microsoft\\Windows\\Hyper-V\\Virtual Hard Disks\\APP01');
  assert(!await f.page.locator('input[data-vm-move="cluster"]').isChecked(),'highly available is opt-in');
  await f.page.locator('[data-vm-storage]').nth(1).click();
  assert((await f.page.locator('#vmMoveSpace').textContent()).includes('Not enough'));assert(await f.page.locator('#vmMoveCopy').isDisabled(),'a copy that does not fit is refused');
  await f.page.locator('[data-vm-storage]').nth(0).click();
  assert.equal(await f.page.locator('#vmMoveFolder').inputValue(),'C:\\ClusterStorage\\Volume1\\APP01');assert.equal(await f.page.locator('#vmMoveVmFolder').inputValue(),'C:\\ClusterStorage\\Volume1');
  assert(await f.page.locator('input[data-vm-move="cluster"]').isChecked(),'choosing a CSV ticks highly available');assert(!await f.page.locator('#vmMoveCopy').isDisabled());
  assert((await f.page.locator('#vmMoveSpace').textContent()).includes('500 GB is free on C:\\ClusterStorage\\Volume1'));
  f.state.vmware.inventorySerial=3;await f.page.waitForTimeout(700);assert.equal(await f.page.locator('#vmMoveFolder').inputValue(),'C:\\ClusterStorage\\Volume1\\APP01','the choice survives a refresh');
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmMove').evaluate(e=>e.scrollIntoView({block:'start'}));await f.page.screenshot({path:shots+'/vmware-move-cluster-1440x1000.png'});
  await f.page.locator('#vmMoveCutover').click();assert((await f.page.locator('#migrateDialogText').textContent()).includes('make it highly available in the cluster'));
  await f.page.locator('#migrateConfirmInput').fill('APP01');await f.page.locator('#migrateConfirmButton').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  const body=vmwareCalls(f,'move').at(-1).body;assert.equal(body.cluster,true);assert.equal(body.copyFolder,'C:\\ClusterStorage\\Volume1\\APP01');assert.equal(body.vmFolder,'C:\\ClusterStorage\\Volume1');
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',move:{vm:'APP01',cutover:true,shutDown:true,round:{disks:[]},hyperV:{generation:2,clusterRole:true,clusterResult:'CLUSTER-ROLE-FAILED The cluster service is not running.'},hyperVId:'4f6c1d6e-2b1a-4c55-9d3e-0f1e2d3c4b5a'},moveSerial:2});
  await f.page.waitForFunction(()=>document.querySelector('#vmMove').textContent.includes('not made highly available: The cluster service is not running.'));
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('New migration holds the former Guided migration: every way that continues in its own tool lists its steps in order with the computer each runs on, live checks that block the wrong computer or too little space, an inline vCenter sign-in, and Prepare and fix up reaching the new VM',async()=>{const f=await fixture(browser);try{
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();
  f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',account:'administrator@vsphere.local',summary:vmwareSummaryFixture(),inventorySerial:1,canPrepare:true,hyperVHere:true,hostStorage:{known:true,vhdPath:'D:\\Hyper-V\\Disks',vmPath:'D:\\Hyper-V',clustered:false,volumes:[{path:'D:\\',freeBytes:1099511627776,totalBytes:2199023255552,csv:false}]}});
  await f.page.keyboard.press('Alt+5');assert.equal(await f.page.locator('[data-tool="guide"]').count(),0,'there is no separate Guided migration tool');assert.equal(await f.page.locator('#tabGuide').count(),0);
  {const card=async id=>(await f.page.locator(`[data-tool="${id}"]`).textContent());
   const t=await card('movevms');assert(t.includes('Works with VMware (vCenter or ESXi) or Hyper-V VMs to Hyper-V, Scale HyperCore, Nutanix AHV, Proxmox VE or a physical server')&&t.includes('Agentless')&&t.includes('Live, source keeps running'),t);
   assert((await card('replicate')).includes('Agent on the source'));assert((await card('convert')).includes('Offline, source shut down'));assert((await card('shares')).includes('to a new Windows file server'));
   assert((await card('wizard')).includes('every other source and target gets its steps in order'));}
  await goTool(f,'wizard');assert.equal(await f.page.locator('#nwHere [data-gd-fact]').count(),5,'the first screen says what this computer is');
  await f.page.locator('[data-nw-source="vms"]').click();await f.page.waitForSelector('#nwVmList [data-nw-pick]');await f.page.locator('[data-nw-pick="vm-app01"]').check();await f.page.locator('#nwNext').click();
  await f.page.locator('[data-nw-target="here"]').click();await f.page.locator('[data-nw-switch="VM-Servers"]').selectOption('Isolated');await f.page.locator('#nwNext').click();
  const methods=()=>f.page.locator('[data-nw-method]').evaluateAll(n=>n.map(x=>x.getAttribute('data-nw-method')));
  assert.deepEqual(await methods(),['plans','movevms','replicate']);assert.equal(await f.page.locator('#gdSteps').count(),0,'a way that runs here from start to finish needs no step list');
  await f.page.locator('[data-nw-method="movevms"]').click();await f.page.waitForSelector('#gdSteps li.gd-step');
  assert.equal(await f.page.locator('#gdPlanTitle').textContent(),'Moving a VMware VM to Hyper-V: the steps, in order');assert.equal(await f.page.locator('#nwNext').textContent(),'Continue in Move VMs');
  const titles=await f.page.locator('#gdSteps .gd-head strong').allTextContents();
  for(const want of ['Read vCenter or ESXi','Check readiness','Choose where the VM\'s files go','Copy the disks while the VM runs','Cut over: this builds the Hyper-V VM','Make it highly available (failover cluster only)','Fix up the new machine'])assert(titles.includes(want),titles.join(' | '));
  assert.equal(await f.page.locator('#gdSteps li.gd-step.current').count(),1,'exactly one step is open');
  await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('#nwStep').evaluate(e=>{const g=document.querySelector('#gdPlan');e.scrollTop=g.offsetTop-e.offsetTop-150;});await f.page.screenshot({path:out+'/new-migration-steps-1440x1000.png'});
  const optional=await f.page.locator('#gdSteps li.gd-step.optional .gd-head strong').allTextContents();
  for(const want of ['Plan waves and write a runbook','Save BitLocker keys (only with BitLocker or a virtual TPM)','Compare before and after'])assert(optional.includes(want),optional.join(' | '));
  assert(!/Copy this application into the VM/.test(await f.page.locator('#nwStep').textContent()),'no step asks to copy the application into the VM');
  const stepNo=async()=>Number((await f.page.locator('#gdProgress').textContent()).match(/^Step (\d+) of /)[1]);
  const first=await stepNo();await f.page.locator('#gdSteps li.gd-step.current [data-gd-next]').click();assert.equal(await stepNo(),first+1,'Next step opens the next one');
  const storage=f.page.locator('#gdSteps li.gd-step').filter({hasText:'Choose where the VM\'s files go'});
  f.state.vmware.hostStorage={known:true,vhdPath:'D:\\Hyper-V\\Disks',clustered:false,volumes:[{path:'D:\\',freeBytes:21474836480,totalBytes:107374182400,csv:false}]};
  await f.page.waitForFunction(()=>[...document.querySelectorAll('#gdSteps li.gd-step')].some(li=>li.textContent.includes('Not enough space')&&li.classList.contains('blocked')));
  assert(await storage.locator('[data-gd-open]').isDisabled(),'a blocked step cannot be opened');
  f.state.vmware.hostStorage={known:true,vhdPath:'D:\\Hyper-V\\Disks',clustered:true,volumes:[{path:'C:\\ClusterStorage\\Volume1',freeBytes:1099511627776,totalBytes:2199023255552,csv:true}]};
  await f.page.waitForFunction(()=>[...document.querySelectorAll('#gdSteps li.gd-step')].some(li=>li.textContent.includes('choose a Cluster Shared Volume')&&!li.classList.contains('blocked')));
  Object.assign(f.state.vmware,{canPrepare:false,inventorySerial:2});
  await f.page.waitForFunction(()=>!!document.querySelector('#gdSteps li.gd-step'));
  await f.page.locator('#gdSteps li.gd-step').filter({hasText:'Copy the disks while the VM runs'}).locator('.gd-head').click();
  const copy=f.page.locator('#gdSteps li.gd-step.current');await f.page.waitForFunction(()=>!!document.querySelector('#gdSteps li.gd-step.current [data-gd-vcpass]'));
  assert((await copy.locator('[data-gd-check]').textContent()).includes('does not hold the vCenter password'));
  assert((await copy.locator('[data-gd-fix]').textContent()).includes('administrator@vsphere.local on vc01.example.com'));
  await copy.locator('[data-gd-vcpass]').fill('Synthetic-Pa55');await copy.locator('[data-gd-vcconnect]').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Reading');
  assert.deepEqual(vmwareCalls(f,'connect').at(-1).body,{server:'vc01.example.com',user:'administrator@vsphere.local',password:'Synthetic-Pa55'},'the inline fix reconnects with the held server and account');
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',canPrepare:true,move:{vm:'APP01',cutover:true,hyperVId:'4f6c1d6e-2b1a-4c55-9d3e-0f1e2d3c4b5a'},moveSerial:3});
  await f.page.waitForFunction(()=>!!document.querySelector('#gdSteps li.gd-step')&&!document.querySelector('[data-gd-vcpass]'));
  await f.page.locator('#gdSteps li.gd-step').filter({hasText:'Fix up the new machine'}).locator('.gd-head').click();
  assert((await f.page.locator('#gdSteps li.gd-step.current .gd-text').textContent()).includes('PowerShell Direct'));
  await f.page.waitForFunction(()=>document.querySelector('#gdSteps li.gd-step.current [data-gd-check]').textContent.includes('The new VM APP01 exists'));
  await f.page.locator('#gdSteps li.gd-step.current [data-gd-open]').click();
  assert(await f.page.locator('#vmViewGuest').isVisible());
  assert(await f.page.locator('input[name="vmGuestMode"][value="direct"]').isChecked(),'after a cutover on the host the steps pick PowerShell Direct');
  assert.equal(await f.page.locator('#vmGuestVmName').inputValue(),'APP01','the new VM is filled in');
  assert(await f.page.locator('input[name="vmGuestMode"][value="hostdirect"]').isDisabled(),'the connected-host option needs a connection');
  assert(await f.page.locator('#vmGuestSignInCurrent').isDisabled()&&(await f.page.locator('#vmGuestSignInWhy').textContent()).includes('PowerShell Direct'),'PowerShell Direct says why the current sign-in cannot be used');
  await f.page.locator('#vmGuestUser').fill('.\\Administrator');await f.page.locator('#vmGuestPassword').fill('Synthetic-Guest-1');await f.page.locator('#vmGuestUse').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmGuestPassword').value==='');
  assert.deepEqual(vmwareCalls(f,'guest-target').at(-1).body,{mode:'direct',vm:'APP01',computer:'',host:'',user:'.\\Administrator',password:'Synthetic-Guest-1',keepPassword:true});
  f.state.vmware.guestTarget={mode:'direct',modeName:'A VM on this Hyper-V host (PowerShell Direct)',label:'APP01',vm:'APP01',computer:'',host:'',user:'.\\Administrator',hasPassword:true,kept:'C:\\x',keptSource:'vCenter',keptAt:'2026-11-26T03:00:00Z'};
  await f.page.waitForFunction(()=>document.querySelector('#vmGuestTargetNote').textContent.includes('Checks run on APP01'));
  assert((await f.page.locator('#vmGuestTargetNote').textContent()).includes('Recording kept here: from vCenter'));
  await f.page.waitForTimeout(400);assert(vmwareCalls(f,'guest/status').length>=1,'Use this machine reads the VM straight away');
  await f.page.locator('input[name="vmGuestMode"][value="winrm"]').check();assert(await f.page.locator('#vmGuestComputerName').isVisible()&&!await f.page.locator('#vmGuestVmName').isVisible());
  await f.page.locator('#vmGuestWhere').scrollIntoViewIfNeeded();await f.page.screenshot({path:out+'/guest-which-machine-1440x1000.png'});
  // Replicate servers follows the running-server steps.
  await goTool(f,'wizard');await f.page.locator('[data-nw-method="replicate"]').click();await f.page.waitForSelector('#gdSteps li.gd-step');
  const serverTitles=await f.page.locator('#gdSteps .gd-head strong').allTextContents();
  assert(serverTitles.includes('Choose where the copies go')&&serverTitles.includes('Install the agent on the server being moved')&&serverTitles.includes('Add the agent to your endpoint protection exclusions')&&serverTitles.includes('Wait for the initial copy to complete'),serverTitles.join(' | '));
  assert((await f.page.locator('#gdComputers').textContent()).includes('The new Hyper-V host'));
  // Only ways that can write to the chosen target are offered.
  await f.page.locator('[data-nw-step="0"]').click();await f.page.locator('#nwNext').click();await f.page.locator('[data-nw-target="physical"]').click();await f.page.locator('#nwNext').click();
  assert.deepEqual(await methods(),['movevms','replicate'],'disk images are not written to a physical server');
  // File shares and disk images get their own steps.
  await f.page.locator('[data-nw-step="0"]').click();await f.page.locator('[data-nw-source="shares"]').click();await f.page.locator('#nwNext').click();await f.page.locator('[data-nw-target="fileserver"]').click();await f.page.locator('#nwNext').click();
  assert.deepEqual(await methods(),['shares']);assert.equal(await f.page.locator('#gdPlanTitle').textContent(),'Moving file shares to a new file server: the steps, in order');
  assert((await f.page.locator('#gdSteps').textContent()).includes('Connect to the old server and choose shares'));
  // Between two other servers, from an admin computer: its own steps, and the hand-off presets Another server.
  await f.page.locator('[data-nw-step="1"]').click();await f.page.locator('[data-nw-target="fileremote"]').click();await f.page.locator('#nwNext').click();
  assert.deepEqual(await methods(),['shares']);assert.equal(await f.page.locator('#gdPlanTitle').textContent(),'Moving file shares between two other servers: the steps, in order');
  {const t=await f.page.locator('#gdSteps').textContent();assert(t.includes('Choose the old and new servers')&&t.includes('Check, copy and keep copying'),t);}
  await f.page.locator('#nwNext').click();await f.page.waitForTimeout(200);
  assert(await f.page.locator('#tabMigrate').isVisible(),'Continue in opens Move file shares');assert(await f.page.locator('#migrateTargetOther').isChecked(),'Another server is preset');
  assert(await f.page.locator('#migrateTargetFields').isVisible(),'the new server fields show');
  // SQL Server databases: one way, its steps, and the hand-off opens Move SQL databases.
  await goTool(f,'wizard');await f.page.locator('[data-nw-step="0"]').click();await f.page.locator('[data-nw-source="sqldbs"]').click();await f.page.locator('#nwNext').click();await f.page.locator('[data-nw-target="sqlserver"]').click();await f.page.locator('#nwNext').click();
  assert.deepEqual(await methods(),['sqlmove']);assert.equal(await f.page.locator('#gdPlanTitle').textContent(),'Moving SQL Server databases to a new SQL Server: the steps, in order');
  assert((await f.page.locator('#gdSteps').textContent()).includes('Choose databases and how each moves'));
  await f.page.locator('#nwNext').click();await f.page.waitForTimeout(200);assert(await f.page.locator('#tabSql').isVisible(),'Continue in opens Move SQL databases');
  await goTool(f,'wizard');
  f.state.vmware.hyperVHere=false;
  await f.page.locator('[data-nw-step="0"]').click();await f.page.locator('[data-nw-source="images"]').click();await f.page.locator('#nwNext').click();await f.page.locator('[data-nw-target="here"]').click();await f.page.locator('#nwNext').click();
  assert.deepEqual(await methods(),['convert']);assert((await f.page.locator('#gdSteps').textContent()).includes('Check the disk'));
  await f.page.waitForFunction(()=>[...document.querySelectorAll('#gdSteps li.gd-step')].some(li=>li.textContent.includes('Send it to Hyper-V and create the VM')&&li.classList.contains('blocked')&&li.textContent.includes('is not a Hyper-V host')),null,{timeout:5000}).catch(async()=>{throw new Error('a Hyper-V step is not blocked on a computer that is not a Hyper-V host: '+await f.page.locator('#gdSteps li.gd-step').filter({hasText:'Send it to Hyper-V'}).evaluate(e=>e.className+' | '+e.querySelector('[data-gd-check]').textContent));});
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Receiver connection details: Copy buttons copy each detail and all of them, and pasting the block into any sending field fills all three',async()=>{const f=await fixture(browser);try{
  await f.context.grantPermissions(['clipboard-read','clipboard-write']);
  await goTool(f,'environment');await goTool(f,'diskcopy');
  f.state.vmware.receiver={running:true,port:44380,code:'K7QMZ-2RT4X-9WBNP-H3VDA',thumbprint:'3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e',folder:'D:\\Hyper-V\\Disks',freeBytes:1099511627776,addresses:['192.0.2.40'],sessions:0,firewall:true,defaultFolder:'D:\\Hyper-V\\Disks'};
  await f.page.waitForFunction(()=>document.querySelector('#vmReceiveCode').textContent==='K7QMZ-2RT4X-9WBNP-H3VDA');
  assert((await f.page.locator('#vmReceiveSaving').textContent()).includes('1.0 TB free'));
  await f.page.locator('#vmCopyCode').click();await f.page.waitForFunction(()=>document.querySelector('#vmCopyCode').textContent==='Copied');assert.equal(await f.page.evaluate(()=>navigator.clipboard.readText()),'K7QMZ-2RT4X-9WBNP-H3VDA');
  await f.page.locator('#vmCopyAll').click();await f.page.waitForFunction(()=>document.querySelector('#vmCopyAll').textContent==='Copied');
  const all=await f.page.evaluate(()=>navigator.clipboard.readText());assert(all.includes('192.0.2.40')&&all.includes('3f9a6c0e1b7d4a25')&&all.includes('Pairing code: K7QMZ-2RT4X-9WBNP-H3VDA'),all);
  f.state.vmware.receiver={running:false,port:44380,sessions:0,firewall:false,defaultFolder:'D:\\Hyper-V\\Disks'};
  await f.page.locator('[data-diskrole="send"]').click();
  await f.page.locator('#vmSendAddress').evaluate((e,text)=>{const d=new DataTransfer();d.setData('text/plain',text);e.dispatchEvent(new ClipboardEvent('paste',{clipboardData:d,bubbles:true,cancelable:true}));},all);
  assert.equal(await f.page.locator('#vmSendAddress').inputValue(),'192.0.2.40:44380');assert.equal(await f.page.locator('#vmSendThumbprint').inputValue(),'3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e');assert.equal(await f.page.locator('#vmSendCode').inputValue(),'K7QMZ-2RT4X-9WBNP-H3VDA');
  // A receiver with storage and cluster networks lists every address: the
  // default-route one first, the rest under Other addresses. A paste keeps
  // the one that answers, or the one already found.
  f.state.vmware.receiver={running:true,port:44380,code:'K7QMZ-2RT4X-9WBNP-H3VDA',thumbprint:'3F:9A:6C:0E:1B:7D:4A:25:C8:E0:F1:3B:6A:9D:2C:4E:7F:0A:1B:3C:5D:7E:9F:2A:4C:6E:8B:0D:1F:3A:5C:7E',folder:'D:\\Hyper-V\\Disks',addresses:['10.10.100.11','192.0.2.40','10.10.200.11'],sessions:0,firewall:true,defaultFolder:'D:\\Hyper-V\\Disks'};
  await f.page.locator('[data-diskrole="receive"]').click();
  await f.page.waitForFunction(()=>/^10\.10\.100\.11 \(default route; also 192\.0\.2\.40, 10\.10\.200\.11\)/.test(document.querySelector('#vmReceiveAddress').textContent));
  await f.page.locator('#vmCopyAll').click();await f.page.waitForFunction(()=>document.querySelector('#vmCopyAll').textContent==='Copied');
  const many=await f.page.evaluate(()=>navigator.clipboard.readText());assert(many.includes('Receiver: 10.10.100.11:44380')&&many.includes('Other addresses: 192.0.2.40:44380, 10.10.200.11:44380'),many);
  f.state.vmware.receiver={running:false,port:44380,sessions:0,firewall:false,defaultFolder:'D:\\Hyper-V\\Disks'};
  await f.page.locator('[data-diskrole="send"]').click();
  const paste=async text=>f.page.locator('#vmSendThumbprint').evaluate((e,t)=>{const d=new DataTransfer();d.setData('text/plain',t);e.dispatchEvent(new ClipboardEvent('paste',{clipboardData:d,bubbles:true,cancelable:true}));},text);
  await f.page.locator('#vmSendAddress').fill('');await paste(many);
  await f.page.waitForFunction(()=>document.querySelector('#vmSendAddress').value==='192.0.2.40:44380');
  assert.deepEqual(vmwareCalls(f,'receivers/reach').at(-1).body,{addresses:['10.10.100.11:44380','192.0.2.40:44380','10.10.200.11:44380']});
  const reaches=vmwareCalls(f,'receivers/reach').length;
  await f.page.locator('#vmSendAddress').fill('10.10.200.11:44380');await paste(many);
  assert.equal(await f.page.locator('#vmSendAddress').inputValue(),'10.10.200.11:44380','an address already filled that the receiver lists is kept');
  assert.equal(vmwareCalls(f,'receivers/reach').length,reaches);
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware move to Proxmox VE and physical servers: connect, receiver media, create, receiver and copy each post what was chosen, with the typed VM name where vSphere or the platform changes',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);f.state.vmware.canPrepare=true;f.state.vmware.inventorySerial=2;
  await f.page.locator('#vmTargetSelect').selectOption('proxmox');await f.page.waitForFunction(()=>document.querySelector('#vmCountsTitle').textContent==='Readiness for Proxmox VE');
  await goTool(f,'movevms');await openVm(f,'vm-app01');await f.page.locator('#vmMoveTarget').waitFor();assert.equal(await f.page.locator('#vmMove').count(),0,'the Hyper-V panel is only for Hyper-V');
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
  assert.deepEqual(vmwareCalls(f,'target/receiver').map(c=>c.body),[{vmId:'vm-app01',address:'192.0.2.50:8443',code:'ABCDE-FGHIJ-KLMNO-PQRST',thumbprint:'AABB'}]);
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
 await check('VMware Hosts card and pages: the inventory, cluster, host and license pages with hardware, licensing, monitoring and configuration, all linked',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);
  assert.equal(await f.page.locator('#vmCountHosts').textContent(),'3');assert.equal(await f.page.locator('#vmCountHostsSub').textContent(),'1 cluster, 1 license');
  f.vm.inventory.hosts=[{name:'esx01.example.com',datacenter:'DC-East',cluster:'Prod-Cluster',vendor:'Example',model:'R760',cpuModel:'Xeon Gold',sockets:2,cores:32,threads:64,memoryBytes:549755813888,version:'8.0.2',build:'22380479',connectionState:'connected',license:{name:'vSphere 8 Enterprise Plus',key:'AB12C-*****-*****-*****-MN90P'},details:{serialNumber:'SVC1234',biosVendor:'Example Inc.',biosVersion:'2.19.1',managementIp:'192.0.2.21',overallStatus:'yellow',ntpServers:['192.0.2.123'],ntpRunning:true,physicalNics:[{device:'vmnic0',driver:'ixgben',speedMb:10000,duplex:true,mac:'00:00:5e:00:53:01',switch:'vSwitch0'}],switches:[{name:'vSwitch0',kind:'Standard',mtu:1500,uplinks:['vmnic0'],portgroups:['VM-Servers']}],services:[{key:'ntpd',label:'NTP Daemon',running:true,policy:'on'},{key:'TSM-SSH',label:'SSH',running:false,policy:'off'}],packages:[{name:'esx-base',version:'8.0.2-0.0.22380479',vendor:'VMware',acceptance:'VMwareCertified'}],alarms:['Host hardware sensor <b>warning</b>']}},{name:'esx02 <b>x</b>',datacenter:'DC-East',cluster:'Prod-Cluster',connectionState:'connected',maintenance:true}];
  f.vm.inventory.licenses=[{name:'vSphere 8 Enterprise Plus',key:'AB12C-*****-*****-*****-MN90P',product:'VMware ESX Server',version:'8.*',used:4,total:8,unit:'cpuPackage',expires:'2026-12-15',assignedTo:['esx01.example.com']},{name:'vCenter Server 8 Standard',key:'CD34E-*****-*****-*****-QR12S',product:'VMware VirtualCenter Server',version:'8.*',used:1,total:1,unit:'server',expires:'',assignedTo:['vc01.example.com']}];
  f.vm.attention=['License vSphere 8 Enterprise Plus (AB12C-*****-*****-*****-MN90P) expires on 2026-12-15.','Host esx02 <b>x</b> is in maintenance mode.'];
  Object.assign(f.state.vmware,{inventorySerial:2,discoveryPath:'C:\\Reports\\VMware_vc01\\VMware-Discovery-Report.html'});
  await f.page.waitForFunction(()=>document.querySelector('#vmCountHostsSub').textContent==='1 cluster, 2 licenses');assert.equal(await f.page.locator('#vmCountHosts').textContent(),'2');
  await f.page.locator('#vmHostsCard').click();assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'vc01.example.com');assert.equal(await f.page.locator('#vmObjTab-hosts').getAttribute('aria-selected'),'true','the Hosts card opens the inventory\'s hosts');
  assert.equal(await f.page.evaluate(()=>document.activeElement.id),'vmObjTab-hosts');assert.equal(await f.page.locator('#vmDrawerBody tbody tr').count(),2);assert.equal(await f.page.locator('#vmDrawerBody b').count(),0,'names are text');
  assert((await f.page.locator('#vmDrawerBody tbody tr').nth(1).textContent()).includes('maintenance mode'));
  await f.page.locator('#vmObjTab-summary').click();assert.equal(await f.page.locator('#vmHostsWatch li').count(),2);assert.equal(await f.page.locator('#vmHostsWatch b').count(),0,'notes are text');
  await f.page.locator('#vmObjTab-licenses').click();assert.equal(await f.page.locator('#vmDrawerBody tbody tr').count(),2,'every license');
  await f.page.locator('#vmOpenDiscovery').click();await f.page.waitForTimeout(200);assert.deepEqual(f.opens,['vmwarediscovery']);
  assert.equal(await f.page.locator('#vmInvTree li.vm-inv-item.off .vm-inv-label',{hasText:'esx02'}).textContent(),'esx02 <b>x</b>','a host in maintenance mode is dimmed');assert.equal(await f.page.locator('#vmInvTree b').count(),0);
  await openTree(f,'Prod-Cluster');assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Cluster');
  const facts=await f.page.locator('#vmDrawerBody').textContent();assert(facts.includes('High availabilityOn')&&facts.includes('DRSOn')&&facts.includes('vSphere 8 Enterprise Plus'),facts);
  await f.page.locator('#vmObjTab-configure').click();const licRow=await f.page.locator('#vmDrawerBody .vm-drawer-section',{hasText:'Licensing'}).locator('tbody tr').textContent();assert(licRow.includes('AB12C-*****-*****-*****-MN90P')&&licRow.includes('4 / 8 cpuPackage')&&licRow.includes('2026-12-15'),licRow);
  await openTree(f,'esx01.example.com');const kv=await f.page.locator('#vmDrawerBody').textContent();
  assert(kv.includes('SVC1234')&&kv.includes('8.0.2 build 22380479')&&kv.includes('192.0.2.21')&&kv.includes('vSphere 8 Enterprise Plus')&&kv.includes('health yellow')&&kv.includes('1 item reported by the host.'),kv);
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmInv').evaluate(e=>e.scrollIntoView({block:'start'}));await f.page.screenshot({path:shots+'/vmware-hosts-1440x1000.png'});
  await f.page.locator('#vmObjTab-monitor').click();const mon=await f.page.locator('#vmDrawerBody').textContent();assert(mon.includes('Host hardware sensor <b>warning</b>')&&mon.includes('1 of 2 running'),mon);
  await f.page.locator('#vmObjTab-configure').click();const fold=re=>f.page.locator('#vmDrawerBody details > summary').filter({hasText:re}).first().locator('..');
  const hw=fold(/^Hardware and firmware/);await hw.locator('> summary').click();await hw.locator('dl').waitFor();assert((await hw.textContent()).includes('Example Inc. 2.19.1'));
  const net=fold(/^Network/);await net.locator('> summary').click();await net.locator('tbody').first().waitFor();assert((await net.textContent()).includes('10 Gb full'));assert.deepEqual(await net.locator('.vm-link').allTextContents(),['VM-Servers'],'port groups link to their network');
  await f.page.locator('#vmObjTab-summary').click();await f.page.locator('#vmDrawerBody .vm-link',{hasText:'vSphere 8 Enterprise Plus'}).click();assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'License');
  await f.page.locator('#vmObjTab-hosts').click();await f.page.locator('#vmDrawerBody tbody .vm-link',{hasText:'esx01.example.com'}).click();assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Host');assert((await f.page.locator('#vmDrawerBody').textContent()).includes('SVC1234'));
  await f.page.locator('#vmDrawerBody .vm-stat',{hasText:'VMs'}).click();assert.equal(await f.page.locator('#vmObjTab-vms').getAttribute('aria-selected'),'true','a count opens its tab');assert.deepEqual(await f.page.locator('#vmDrawerBody tbody td:first-child .vm-link').allTextContents(),['APP01','LEGACY03']);
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware counts: All VMs matches the list, VMware system VMs are counted as Not moved and can be listed',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);assert(!await f.page.locator('#vmCountSkipCard').isVisible(),'no system VMs, no card');assert.equal(await f.page.locator('#vmCountsNote').textContent(),'');
  const skip=vmFixtureVM('vCLS-0001',{hv:'skip',sc:'skip',nx:'skip',hvFindings:[{severity:'info',code:'vmware-system',message:'vSphere Cluster Services VM. It is not moved.',action:''}],scFindings:[],nxFindings:[],vm:{guestOsRunning:'Other 3.x Linux (64-bit)',cpus:1,memoryMb:128}});
  f.vm.inventory.vms.push(skip);f.state.vmware.inventorySerial=2;await f.page.waitForFunction(()=>document.querySelectorAll('#vmTableBody tr.vm-row').length===5);
  assert.equal(await f.page.locator('#vmCountAll').textContent(),'5');assert.equal(await f.page.locator('#vmTableNote').textContent(),'5 VMs');assert.equal(await f.page.locator('#vmCountSkip').textContent(),'1');
  const sum=['#vmCountBlocked','#vmCountReview','#vmCountReady','#vmCountSkip'];let total=0;for(const id of sum){total+=Number(await f.page.locator(id).textContent());}assert.equal(total,5,'the cards add up to All VMs');
  assert.equal(await f.page.locator('#vmCountsNote').textContent(),'4 VMs to move; 1 VMware system VM stays behind.');
  await f.page.locator('[data-vmfilter="skip"]').click();assert.deepEqual(await f.page.locator('#vmTableBody tr.vm-row strong').allTextContents(),['vCLS-0001']);assert.equal(await f.page.locator('#vmTableBody tr.vm-row td').nth(7).textContent(),'VMware system VM, not moved.');
  await goTool(f,'movevms');await openVm(f,'vm-vcls-0001');assert.equal(await f.page.locator('#vmPrepareButton').count(),0,'system VMs cannot be prepared');assert.equal(await f.page.locator('#vmObjActions button',{hasText:'Prepare on VMware'}).count(),0);
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware inventory tree: hosts and clusters, VMs and templates, storage and networking views, readiness dots, keyboard, filter, and links that follow the tree',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);
  assert.deepEqual(await treeLines(f),['1 vc01.example.com','2 DC-East','3 Prod-Cluster','4 esx01.example.com','5 APP01','5 LEGACY03','4 esx02.example.com','5 SQL01','4 esx03.example.com','5 WEB02'],'hosts and clusters open to the VMs');
  assert.deepEqual(await f.page.locator('#vmInvTree li[data-inv-id^="vm:"] .vm-inv-dot').evaluateAll(d=>d.map(x=>x.className)),['vm-inv-dot ready','vm-inv-dot blocked','vm-inv-dot review','vm-inv-dot ready'],'each VM shows its readiness');
  assert(await f.page.locator('#vmInvTree li[data-inv-id="vm:vm-web02"]').evaluate(e=>e.classList.contains('off')),'a powered-off VM is dimmed');
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmInv').evaluate(e=>e.scrollIntoView({block:'start'}));await f.page.screenshot({path:shots+'/vmware-browse-1440x1000.png'});
  await f.page.locator('#vmInvTree li.vm-inv-item',{hasText:'esx01.example.com'}).last().locator('> .vm-inv-row .vm-inv-twisty').click();assert(!(await treeLines(f)).includes('5 APP01'),'the arrow collapses a branch');
  await f.page.locator('[data-inv-view="storage"]').click();assert.deepEqual(await treeLines(f),['1 vc01.example.com','2 DC-East','3 DS-SSD-01','3 DS-NL-02']);
  await openTree(f,'DS-NL-02');assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Datastore');const ds=await f.page.locator('#vmDrawerBody').textContent();assert(ds.includes('esx02.example.com, esx03.example.com')&&ds.includes('8.0 TB'),ds);
  await f.page.locator('#vmObjTab-vms').click();const nl=await f.page.locator('#vmDrawerBody').textContent();assert(nl.includes('SQL01')&&nl.includes('[DS-NL-02] SQL01/SQL01_1.vmdk'),nl);
  await f.page.locator('[data-inv-view="network"]').click();assert.deepEqual(await treeLines(f),['1 vc01.example.com','2 DC-East','3 dvs-prod','4 VM-Servers','4 VM-DMZ']);
  await openTree(f,'VM-DMZ');assert((await f.page.locator('#vmDrawerBody').textContent()).includes('VLAN30'));await f.page.locator('#vmObjTab-vms').click();assert.deepEqual(await f.page.locator('#vmDrawerBody tbody td:first-child .vm-link').allTextContents(),['WEB02']);
  await openTree(f,'dvs-prod');assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Switch');assert.equal(await f.page.locator('#vmObjTab-networks').textContent(),'Port groups2');
  await f.page.locator('[data-inv-view="vms"]').click();assert.deepEqual(await treeLines(f),['1 vc01.example.com','2 DC-East','3 Servers','4 APP01','4 LEGACY03','4 SQL01','4 WEB02']);
  await openTree(f,'Servers');assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Folder');
  await f.page.locator('[data-inv-view="hosts"]').click();await f.page.locator('#vmInvSearch').fill('sql');assert.deepEqual(await treeLines(f),['1 vc01.example.com','2 DC-East','3 Prod-Cluster','4 esx02.example.com','5 SQL01'],'the filter keeps only matches and their parents');
  await openVm(f,'vm-sql01');assert.equal(await f.page.locator('#vmSearch').inputValue(),'','opening a VM keeps the list as it was');assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'SQL01');
  await f.page.locator('#vmDrawerBody .vm-kv .vm-link',{hasText:'DS-NL-02'}).click();assert.equal(await f.page.locator('#vmDrawerKind').textContent(),'Datastore');
  assert.equal(await f.page.locator('[data-inv-view="storage"]').getAttribute('aria-selected'),'true','a datastore link switches to the storage view');assert.equal(await f.page.locator('#vmInvTree li[aria-selected="true"] > .vm-inv-row .vm-inv-label').textContent(),'DS-NL-02');
  await f.page.keyboard.press('Escape');assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'SQL01');
  await f.page.locator('#vmInvSearch').fill('nothing-like-this');assert.equal(await f.page.locator('#vmInvTree').textContent(),'Nothing matches.');
  f.vm.inventory.vms[0].name='APP <b>01</b>';f.vm.inventory.vms[0].id='vm-x';f.state.vmware.inventorySerial=2;await f.page.locator('#vmInvSearch').fill('app');
  await f.page.waitForFunction(()=>document.querySelector('#vmInvTree').textContent.includes('APP <b>01</b>'));assert.equal(await f.page.locator('#vmInvTree b').count(),0,'names are text');
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware plan: capacity, network and wave edits survive polls; Suggest waves and Save post the plan; the runbook is written after saving',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);await goTool(f,'plan');assert(await f.page.locator('#vmPlanContent').isVisible());
  assert((await f.page.locator('#vmSizing').textContent()).includes('Not described'));
  await f.page.locator('#vmCapCores').fill('32');await f.page.locator('#vmCapMemory').fill('512');await f.page.locator('#vmCapStorage').fill('20');await f.page.locator('[aria-label="New network for VM-Servers"]').fill('Servers');await f.page.locator('[aria-label="New VLAN for VM-Servers"]').fill('20');
  await f.page.locator('.vm-wave-table > summary').click();  await f.page.locator('input[data-vm-wave="vm-app01"]').fill('3');await f.page.locator('[aria-label="Notes for APP01"]').fill('Weekend window');
  assert.equal(await f.page.locator('#vmPlanDirtyNote').textContent(),'Unsaved changes.');await f.page.waitForTimeout(1100);
  assert.equal(await f.page.locator('input[data-vm-wave="vm-app01"]').inputValue(),'3','edits survive polls');assert.equal(await f.page.locator('#vmCapCores').inputValue(),'32');
  await f.page.locator('#vmSavePlan').click();await f.page.waitForFunction(()=>document.querySelector('#vmPlanStatus').textContent==='Plan saved.');
  const saved=vmwareCalls(f,'plan')[0].body.plan;assert.equal(saved.capacity.coresPerNode,32);assert.equal(saved.capacity.memoryGbPerNode,512);assert.deepEqual(saved.networks[0],{source:'VM-Servers',sourceVlan:'20',targetName:'Servers',targetVlan:'20'});
  assert.deepEqual(saved.vms[0],{id:'vm-app01',name:'APP01',wave:3,hold:false,exclude:false,notes:'Weekend window',manual:true,why:''});assert((await f.page.locator('#vmSizing').textContent()).includes('Fits'));assert.equal(await f.page.locator('#vmPlanDirtyNote').textContent(),'');
  await f.page.locator('#vmWaveSize').fill('5');await f.page.locator('#vmSuggest').click();await f.page.waitForFunction(()=>/Suggested/.test(document.querySelector('#vmPlanStatus').textContent));
  assert.equal(vmwareCalls(f,'plan')[1].body.suggest,true);assert.equal(vmwareCalls(f,'plan')[1].body.waveSize,5);
  assert.equal(await f.page.locator('#vmWaveSummary').textContent(),'wave 1: 1, wave 2: 1, wave 3: 1, 1 on hold','APP01, placed in wave 3 by hand, keeps its wave');assert(await f.page.locator('[aria-label="Hold LEGACY03"]').isChecked());
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
 await check('Rolling conversion: choosing Reuse the existing hosts fills the cluster, suggests stages and every stage, wave and value stays editable',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);await goTool(f,'plan');assert.equal(await f.page.locator('#vmScenarios input[type=radio]').count(),2);
  assert(await f.page.locator('#vmScenarios input[data-scenario="new"]').isChecked(),'new hardware is the default');assert(!await f.page.locator('#vmStagesSection').isVisible());
  await f.page.locator('#vmScenarios input[data-scenario="rolling"]').check();await f.page.waitForFunction(()=>document.querySelectorAll('#vmStages .vm-stage').length===3);
  let call=vmwareCalls(f,'plan').at(-1).body;assert.equal(call.plan.scenario,'rolling');assert.equal(call.suggestStages,true);assert.equal(call.keepWaves,true);
  assert.equal(await f.page.locator('#vmCapNodes').inputValue(),'3','the cluster size comes from the hosts');assert.equal(await f.page.locator('#vmCapMemory').inputValue(),'512');assert(await f.page.locator('#vmCapAuto').isChecked());
  assert.equal(await f.page.locator('#vmRollingHosts tr').count(),3);assert((await f.page.locator('#vmRollingStorage').textContent()).includes('Shared LUN'));
  assert((await f.page.locator('#vmStages .vm-stage').first().textContent()).includes('no host is free for a failure (N+1)'));assert.equal(await f.page.locator('select[data-stage-host="0"]').inputValue(),'esx03.example.com');
  // Reorder, change a wave and a host, add a move-only stage, remove one, untick a LUN and type a size.
  await f.page.locator('[aria-label="Move stage 2 up"]').click();assert.equal(await f.page.locator('select[data-stage-host="0"]').inputValue(),'esx01.example.com');
  await f.page.locator('input[data-stage-wave="2"]').fill('7');await f.page.locator('#vmAddStage').click();assert.equal(await f.page.locator('#vmStages .vm-stage').count(),4);
  assert.equal(await f.page.locator('select[data-stage-host="3"]').inputValue(),'');assert(await f.page.locator('input[data-stage-first="3"]').isDisabled(),'a move-only stage always moves first');
  await f.page.locator('[aria-label="Remove stage 4"]').click();await f.page.locator('[aria-label="Reuse DS-SSD-01"]').uncheck();
  await f.page.locator('#vmCapMemory').fill('384');assert(!await f.page.locator('#vmCapAuto').isChecked(),'typing a size stops the automatic fill');
  assert.equal(await f.page.locator('#vmStagesSummary').textContent(),'Save the plan to check the stages again.');
  await f.page.locator('#vmSavePlan').click();await f.page.waitForFunction(()=>document.querySelector('#vmPlanStatus').textContent==='Plan saved.');
  call=vmwareCalls(f,'plan').at(-1).body.plan;assert.deepEqual(call.rolling.stages.map(x=>[x.host,x.wave]),[['esx01.example.com',2],['esx03.example.com',1],['esx02.example.com',7]]);
  assert.equal(call.rolling.autoCapacity,false);assert.equal(call.capacity.memoryGbPerNode,384);assert.equal(call.storage.find(x=>x.source==='DS-SSD-01').reuse,false);
  await f.page.waitForFunction(()=>document.querySelectorAll('#vmStages .vm-stage-head .run-chip').length===3);
  await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('#vmStagesSection').evaluate(e=>e.scrollIntoView({block:'start'}));await f.page.screenshot({path:shots+'/vmware-rolling-1440x1000.png'});
  // Build Hyper-V hosts is drafted from the same stages.
  await f.page.locator('#vmRollingBuild').click();await f.page.waitForFunction(()=>document.querySelector('[data-tool="hvbuild"]').getAttribute('aria-pressed')==='true');
  await f.page.waitForTimeout(300);const draft=vmwareCalls(f,'hvbuild/suggest').at(-1).body;assert.equal(draft.reuse,true);assert.equal(draft.cluster,'Prod-Cluster');
  // Back to new hardware: the stages hide and the plan says so.
  await goTool(f,'plan');await f.page.locator('#vmScenarios input[data-scenario="new"]').check();await f.page.waitForTimeout(300);
  assert.equal(vmwareCalls(f,'plan').at(-1).body.plan.scenario,'new');assert(!await f.page.locator('#vmStagesSection').isVisible());
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Move VMs selection: select-all takes only the VMs shown, hidden selected VMs are counted and named, and VMs already moved are flagged and left out',async()=>{const f=await fixture(browser);try{
  f.plans.plans=[{id:'p0',name:'Wave 1',server:'vc01.example.com',paused:false,settings:{},throttle:'no limit',summary:{total:1,waiting:0,copying:0,ready:0,testing:0,scheduled:0,done:1,failed:0},vms:[{id:'vm-web02',name:'WEB02',wave:1,state:'done',seeded:true,failures:0}]}];
  await vmwareConnected(f);
  f.vm.inventory.vms[0].name='APP01 - MIGRATED! Do not power on';f.vm.inventory.vms[0].readiness[0]=Object.assign({},f.vm.inventory.vms[0].readiness[0],{status:'review',findings:[{severity:'warning',code:'already-moved',message:"The VM's name says it was already migrated or retired.",action:'Exclude it.'}]});f.state.vmware.inventorySerial=2;
  await goTool(f,'movevms');await f.page.locator('#vmObjTab-vms').click();await f.page.waitForFunction(()=>/MIGRATED/.test(document.querySelector('#vmTableBody').textContent));
  await f.page.waitForFunction(()=>document.querySelectorAll('#vmTableBody input[data-vm-pick]').length===4);
  await f.page.waitForFunction(()=>/Already cut over by the migration plan Wave 1/.test(document.querySelector('#vmTableBody').textContent));
  assert((await f.page.locator('#vmTableBody tr[data-vm-id="vm-app01"] .vm-moved').textContent()).includes('already migrated'),'a name that says migrated is flagged');
  await f.page.locator('#vmPickShown').check();
  assert.equal(await f.page.locator('#vmListBulk .vm-bulk-count').textContent(),'2 VMs selected','select-all leaves out VMs already moved');
  assert(!(await f.page.locator('#vmTableBody input[data-vm-pick="vm-web02"]').isChecked())&&!(await f.page.locator('#vmTableBody input[data-vm-pick="vm-app01"]').isChecked()));
  await f.page.locator('#vmTableBody input[data-vm-pick="vm-web02"]').check();
  assert((await f.page.locator('#vmBulkMoved').textContent()).includes('WEB02 (Already cut over by the migration plan Wave 1)'),'a VM ticked by hand that was moved is named');
  await f.page.locator('#vmSearch').fill('sql');await f.page.waitForFunction(()=>document.querySelectorAll('#vmTableBody input[data-vm-pick]').length===1);
  assert.equal(await f.page.locator('#vmListBulk .vm-bulk-count').textContent(),'3 VMs selected (2 hidden by the filter)','hidden selected VMs are counted');
  assert((await f.page.locator('#vmBulkHidden').textContent()).includes('LEGACY03, WEB02'),'and named');
  await f.page.locator('#vmPickShown').uncheck();assert.equal(await f.page.locator('#vmListBulk .vm-bulk-count').textContent(),'2 VMs selected (2 hidden by the filter)','unticking the header changes only the VMs shown');
  await f.page.locator('#vmListBulk [data-bulk="showhidden"]').click();await f.page.waitForFunction(()=>document.querySelectorAll('#vmTableBody input[data-vm-pick]').length===4);
  assert.equal(await f.page.locator('#vmSearch').inputValue(),'');assert.equal(await f.page.locator('#vmListBulk .vm-bulk-count').textContent(),'2 VMs selected');
  await f.page.locator('#vmSearch').fill('legacy');await f.page.waitForFunction(()=>document.querySelectorAll('#vmTableBody input[data-vm-pick]').length===1);
  await f.page.locator('#vmListBulk [data-bulk="drophidden"]').click();assert.equal(await f.page.locator('#vmListBulk .vm-bulk-count').textContent(),'1 VM selected');
  assert(await f.page.locator('#vmTableBody input[data-vm-pick="vm-legacy03"]').isChecked());
  await f.page.setViewportSize({width:1008,height:690});await f.page.locator('#vmSearch').fill('');await f.page.locator('#vmTableBody input[data-vm-pick="vm-sql01"]').check();await f.page.locator('#vmSearch').fill('legacy');
  await f.page.locator('#vmBulkHidden').scrollIntoViewIfNeeded();assert(await f.page.locator('#vmListBulk [data-bulk="drophidden"]').isVisible(),'reachable at 1024x768');
  await f.page.screenshot({path:shots+'/vmware-selection-hidden-1008x690.png'});
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware waves: explained, editable by name, planned window, drag, move, split and merge; bulk wave, prepare and plan actions on ticked VMs',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);await goTool(f,'plan');
  assert((await f.page.locator('#vmWaveWhat').textContent()).includes('A group of VMs you move together'),'waves are explained');
  assert.equal(await f.page.locator('#vmWaveSize').inputValue(),'','the size starts on Auto');
  await f.page.locator('#vmSuggest').click();await f.page.waitForFunction(()=>/Suggested 2 waves of up to 2 VMs \(Auto\)/.test(document.querySelector('#vmPlanStatus').textContent));
  assert.equal(vmwareCalls(f,'plan')[0].body.waveSize,0);assert.equal(vmwareCalls(f,'plan')[0].body.resetManual,false);
  assert.deepEqual(await f.page.locator('#vmWaveBoard .vm-wave-card').evaluateAll(c=>c.map(x=>x.getAttribute('data-wave-lane'))),['1','2','hold'],'one card per wave, then held VMs');
  assert((await f.page.locator('[data-wave-lane="1"] .vm-wave-reason').textContent()).includes('Powered-off'),'each wave says why');
  assert((await f.page.locator('[data-wave-lane="1"] .vm-wave-vm').textContent()).includes('Powered off, so nobody is using it.'),'each VM says why');
  await f.page.locator('[aria-label="Name of wave 1"]').fill('Friday pilot');await f.page.locator('[aria-label="Planned window for wave 1"]').fill('2026-12-04T22:00');await f.page.locator('[aria-label="Notes for wave 1"]').fill('CHG-1001');
  await dragTo(f,'[data-wave-lane="2"] .vm-wave-vm[data-vm-id="vm-app01"] strong','[data-wave-lane="1"] .vm-wave-stats');
  await f.page.waitForFunction(()=>!!document.querySelector('[data-wave-lane="1"] .vm-wave-vm[data-vm-id="vm-app01"]'));
  assert((await f.page.locator('[data-wave-lane="1"] .vm-wave-vm[data-vm-id="vm-app01"]').textContent()).includes('Placed by you.'));
  assert.equal(await f.page.locator('[aria-label="Name of wave 1"]').inputValue(),'Friday pilot','a drag keeps the typed name');
  await f.page.locator('[data-wave-lane="1"] input[data-vm-pick="vm-app01"]').check();assert.equal(await f.page.locator('#vmWaveBulk .vm-bulk-count').textContent(),'1 VM selected');
  await f.page.locator('[data-wave-act="split"][data-wave="1"]').click();await f.page.waitForFunction(()=>!!document.querySelector('[data-wave-lane="2"] .vm-wave-vm[data-vm-id="vm-app01"]'));
  assert.equal(await f.page.locator('[aria-label="Name of wave 2"]').inputValue(),'Friday pilot (part 2)','split makes a new wave right after');assert.equal(await f.page.locator('[aria-label="Name of wave 3"]').inputValue(),'General servers','later waves move down one');
  await f.page.locator('[data-wave-act="up"][data-wave="3"]').click();assert.equal(await f.page.locator('[aria-label="Name of wave 2"]').inputValue(),'General servers','Move earlier swaps two waves');
  await f.page.locator('[data-wave-act="merge"][data-wave="3"]').click();assert.equal(await f.page.locator('#vmWaveBoard [data-wave-lane]').count(),3,'merge removes the wave');
  assert.equal(await f.page.locator('[data-wave-lane="2"] .vm-wave-vm[data-vm-id="vm-app01"]').count(),1,'merged VMs join the wave before');
  await f.page.locator('#vmWaveAdd').click();assert.equal(await f.page.locator('[data-wave-lane="3"]').count(),1,'New wave adds an empty wave');
  await f.page.locator('#vmWaveBulk [data-bulk="wave"]').selectOption('3');await f.page.locator('#vmWaveBulk [data-bulk="put"]').click();
  assert.equal(await f.page.locator('[data-wave-lane="3"] .vm-wave-vm[data-vm-id="vm-app01"]').count(),1,'Put in wave moves the ticked VMs');
  await f.page.locator('#vmWaveBulk [data-vm-pickby]').selectOption({label:'On hold'});assert.equal(await f.page.locator('#vmWaveBulk .vm-bulk-count').textContent(),'2 VMs selected','select by wave or lane');
  await f.page.locator('#vmWaveBulk [data-bulk="clear"]').click();assert.equal(await f.page.locator('#vmWaveBulk .vm-bulk-count').textContent(),'No VMs selected');
  assert.equal(await f.page.locator('#vmPlanDirtyNote').textContent(),'Unsaved changes.');
  await f.page.locator('#vmSavePlan').click();await f.page.waitForFunction(()=>document.querySelector('#vmPlanStatus').textContent==='Plan saved.');
  const saved=vmwareCalls(f,'plan')[1].body.plan;
  assert.deepEqual(saved.waves.map(w=>[w.wave,w.title,w.window||'',w.notes||'',!!w.edited]),[[1,'Friday pilot','2026-12-04T22:00','CHG-1001',true],[2,'General servers','','',true],[3,'Wave 3','','',true]]);
  const app=saved.vms.find(v=>v.id==='vm-app01');assert.deepEqual([app.wave,app.manual,app.why],[3,true,'']);
  await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('#vmWaveTitle').evaluate(e=>e.scrollIntoView({block:'start'}));await f.page.screenshot({path:shots+'/vmware-waves-1440x1000.png'});
  await f.page.locator('#vmWaveReset').check();await f.page.locator('#vmSuggest').click();await f.page.waitForFunction(()=>/Suggested/.test(document.querySelector('#vmPlanStatus').textContent));
  assert.equal(vmwareCalls(f,'plan')[2].body.resetManual,true,'Suggest can replace hand-placed VMs too');
  // Bulk in the VM list (Move VMs): select by readiness, put in a wave, prepare one after another, add to a migration plan.
  f.state.vmware.canPrepare=true;f.vm.inventory.vms[1].snapshots=[{name:'before patch',created:'2026-10-06T00:00:00Z'}];f.state.vmware.inventorySerial=2;
  await goTool(f,'movevms');await f.page.locator('[data-vmfilter="all"]').first().click().catch(()=>{});
  await f.page.waitForFunction(()=>document.querySelectorAll('#vmTableBody input[data-vm-pick]').length===4);
  await f.page.locator('#vmListBulk [data-vm-pickby]').selectOption({label:'Running'});assert.equal(await f.page.locator('#vmListBulk .vm-bulk-count').textContent(),'3 VMs selected');
  assert(await f.page.locator('#vmTableBody input[data-vm-pick="vm-sql01"]').isChecked(),'the list shows the selection');
  await f.page.locator('#vmTableBody input[data-vm-pick="vm-legacy03"]').uncheck();assert.equal(await f.page.locator('#vmListBulk .vm-bulk-count').textContent(),'2 VMs selected');
  assert.equal(await f.page.locator('#vmDrawerTitle').textContent(),'vc01.example.com','ticking a VM does not open it');
  await f.page.waitForTimeout(1200);await f.page.locator('#vmListBulk [data-bulk="prepare"]').click();assert(await f.page.locator('#vmBulkPrepare').isVisible());
  const preview=await f.page.locator('#vmBulkPrepare tbody tr').allTextContents();assert.deepEqual(preview,['APP01Nothing to do with the steps ticked.','SQL01delete all snapshots'],'only VMs that need a step get it');
  await f.page.locator('#vmBulkPrepare input[data-bulk-step="shutdown"]').check();
  await f.page.locator('#vmBulkPrepareButton').click();assert.equal(await f.page.locator('#migrateConfirmLabel').textContent(),'Type the number of VMs to confirm: 2 VMs');
  assert((await f.page.locator('#migrateDialogAlert').textContent()).includes('2 running VMs will be shut down'));
  await f.page.locator('#migrateConfirmInput').fill('2 vms');await f.page.locator('#migrateConfirmButton').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert.deepEqual(vmwareCalls(f,'prepare').map(c=>c.body),[{items:[{vmId:'vm-app01',steps:{snapshots:false,shutdown:true,removeTpm:false,decrypt:false}},{vmId:'vm-sql01',steps:{snapshots:true,shutdown:true,removeTpm:false,decrypt:false}}],confirm:'2 vms'}]);
  Object.assign(f.state.vmware,{prepareBatch:[{id:'vm-app01',vm:'APP01',state:'done',done:['Shut down the guest.'],skipped:[]},{id:'vm-sql01',vm:'SQL01',state:'failed',done:['Deleted 1 snapshot.'],skipped:[],error:'The guest did not shut down within 10 minutes.'}]});
  await f.page.waitForFunction(()=>/1 of 2 done, 1 failed/.test(document.querySelector('#vmListBatch').textContent));
  assert((await f.page.locator('#vmListBatch [data-batch-vm="vm-sql01"]').textContent()).includes('did not shut down'),'each VM shows its own result');
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected'});await f.page.waitForFunction(()=>!document.querySelector('#vmListBulk [data-bulk="put"]').disabled);
  await f.page.locator('#vmListBulk [data-bulk="networks"]').click();assert.deepEqual(await f.page.locator('#vmBulkNetworks tbody td:first-child').allTextContents(),['VM-Servers (VLAN 20)']);
  await f.page.locator('#vmListBulk [data-bulk="wave"]').selectOption('2');await f.page.locator('#vmListBulk [data-bulk="put"]').click();
  await f.page.waitForFunction(()=>/2 VMs put in wave 2/.test(document.querySelector('#vmListBulk').textContent));const listSave=vmwareCalls(f,'plan').pop().body.plan;
  assert.deepEqual(listSave.vms.filter(v=>v.wave===2).map(v=>v.id).sort(),['vm-app01','vm-sql01'],'list actions save the plan at once');
  await f.page.locator('#vmListBulk [data-bulk="plans"]').click();assert.equal(await f.page.locator('[data-tool="plans"]').getAttribute('aria-pressed'),'true');
  await f.page.waitForFunction(()=>document.querySelectorAll('#mpPicker input[data-mp-pick]:checked').length>0);
  assert.deepEqual(await f.page.locator('#mpPicker input[data-mp-pick]:checked').evaluateAll(n=>n.map(x=>x.getAttribute('data-mp-pick'))),['vm-app01','vm-sql01'],'the selection comes into a new migration plan');
  assert.equal(await f.page.locator('#mpName').inputValue(),'Wave 2: General servers');
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware and Hyper-V discovery saves its reports in a chosen folder, shared with This server, with Browse',async()=>{const f=await fixture(browser);try{
  await goTool(f,'environment');
  const field=f.page.locator('#vmOutputRoot');
  assert(await field.isVisible(),'the save folder shows beside the reads');
  await f.page.waitForFunction(()=>document.querySelector('#vmOutputRoot').value==='C:\\Reports');
  await f.page.locator('#vmOutputRoot + .path-pick-button').click();await f.page.waitForFunction(()=>document.querySelector('#vmOutputStatus').textContent.includes('E:\\Picked'));
  assert.deepEqual({kind:f.pickCalls[0].kind,current:f.pickCalls[0].current},{kind:'folder',current:'C:\\Reports'},'Browse opens at the current folder');
  assert.deepEqual(f.outputRootCalls,[{outputRoot:'E:\\Picked'}],'a browsed folder is saved at once');
  await field.fill('Q:\\NoSuchDrive');await field.press('Enter');await f.page.waitForFunction(()=>document.querySelector('#vmOutputStatus').textContent.includes('synthetic failure'));
  assert.equal(await field.getAttribute('aria-invalid'),'true');assert.equal(f.dialogs.length,0,'a refused folder stays inline');
  await field.press('Escape');assert.equal(await field.inputValue(),'E:\\Picked','Escape puts the saved folder back');
  await field.fill('D:\\Migration\\Reports');await field.press('Tab');await f.page.waitForFunction(()=>document.querySelector('#vmOutputStatus').textContent==='Saved. New reports go to D:\\Migration\\Reports.');
  assert.equal(f.state.defaultOutputRoot,'D:\\Migration\\Reports');
  await goTool(f,'server');
  assert.equal(await f.page.locator('#outputPath').inputValue(),'D:\\Migration\\Reports','This server shows the same folder');
  await goTool(f,'movevms');assert(await field.isHidden(),'Move VMs does not show the discovery save folder');
  for(const size of [{width:1008,height:690},{width:784,height:522}]){await f.page.setViewportSize(size);await goTool(f,'environment');await field.scrollIntoViewIfNeeded();
   const box=await f.page.locator('#vmOutputRoot + .path-pick-button').boundingBox();assert(box&&box.x+box.width<=size.width,'Browse stays inside '+size.width+' px');
   assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),'no horizontal scroll at '+size.width+' px');}
 }finally{await f.close();}});
 await check('A migration plan copying keeps Discovery and This server usable, and says why anything waits',async()=>{const f=await fixture(browser);try{
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();
  f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',account:'administrator@vsphere.local',summary:vmwareSummaryFixture(),inventorySerial:1,canPrepare:true,hyperVHere:true,
   planCopy:{plans:['Wave 1'],servers:['vc01.example.com'],vms:4,cutover:false}});
  await goTool(f,'environment');await f.page.waitForFunction(()=>!document.querySelector('#vmReadNotice').classList.contains('hidden'));
  const notice=await f.page.locator('#vmReadNotice').textContent();
  assert(notice.includes('Migration plan “Wave 1” is copying 4 VMs from vc01.example.com')&&notice.includes('read vc01.example.com again'),notice);
  assert.equal(await f.page.locator('#vmwareTabBadge').textContent(),'Plan copying');
  await openReads(f);await f.page.locator('#vmServer').fill('vc01.example.com');await f.page.locator('#vmUser').fill('readonly@vsphere.local');
  assert(await f.page.locator('#vmConnect').isEnabled(),'reading the same vCenter again stays available');
  await f.page.locator('#vmServer').fill('vc02.example.com');assert(await f.page.locator('#vmConnect').isDisabled(),'reading another vCenter waits for the copy');
  assert(await f.page.locator('#vmHvRead').isDisabled()&&await f.page.locator('#vmOpenInventory').isDisabled()&&await f.page.locator('#vmReset').isDisabled(),'reads that replace the session wait');
  assert(await f.page.locator('#vmOutputRoot').isEnabled(),'the save folder can still be changed');
  await f.page.locator('#vmReadNoticeOpen').click();await f.page.waitForFunction(()=>!document.querySelector('#tabPlans').classList.contains('hidden'));
  await goTool(f,'server');assert(await f.page.locator('#startButton').isEnabled(),'This server can collect while a plan copies');assert(await f.page.locator('#configLockNotice').isHidden());
  await goTool(f,'movevms');await f.page.waitForFunction(()=>!document.querySelector('#vmBusyNotice').classList.contains('hidden'));
  assert((await f.page.locator('#vmBusyNotice').textContent()).includes('Wave 1'),'Move VMs says why its steps wait');assert(await f.page.locator('#vmReadNotice').isHidden());
  // Saving the plan (network map, waves) only writes the plan file, so it runs beside the copy.
  await goTool(f,'plan');await f.page.waitForFunction(()=>!document.querySelector('#vmSavePlan').disabled);assert(await f.page.locator('#vmBusyNotice').isHidden(),'Plan and runbook needs no notice for a plan copy');
  assert(await f.page.locator('#vmWriteRunbook').isEnabled(),'the runbook can be written while a plan copies');
  const plansBefore=vmwareCalls(f,'plan').length;await f.page.locator('#vmSavePlan').click();await f.page.waitForFunction(()=>document.querySelector('#vmPlanStatus').textContent==='Plan saved.');
  assert.equal(vmwareCalls(f,'plan').length,plansBefore+1,'the plan is saved while another plan copies');
  // Another VMware step blocks collection, and This server says what and links to it.
  Object.assign(f.state.vmware,{planCopy:null,running:true,action:'prepare',status:'preparing'});
  await goTool(f,'server');await f.page.waitForFunction(()=>!document.querySelector('#configLockNotice').classList.contains('hidden'));
  assert((await f.page.locator('#configLockText').textContent()).includes('Prepare on VMware'));assert(await f.page.locator('#startButton').isDisabled());
  // A Discovery read never blocks collection.
  Object.assign(f.state.vmware,{action:'connect',status:'connecting'});await f.page.waitForFunction(()=>!document.querySelector('#startButton').disabled);
  // A collection never blocks a Discovery read.
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected'});f.state.running=true;f.state.status='running';
  await f.page.waitForFunction(()=>/Collection/.test((document.querySelector('.tab-button.active')||{}).textContent||''));await goTool(f,'environment');await f.page.waitForFunction(()=>!document.querySelector('#vmServer').disabled);await f.page.locator('#vmServer').fill('vc02.example.com');await f.page.locator('#vmUser').fill('readonly@vsphere.local');
  await f.page.waitForFunction(()=>!document.querySelector('#vmConnect').disabled&&!document.querySelector('#vmHvRead').disabled);
  assert(await f.page.locator('#vmOutputRoot').isDisabled(),'the shared folder waits for the collection that writes there');
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware disk copy: the receiver shows its address, code and thumbprint; sending checks them and sends only the chosen disks',async()=>{const f=await fixture(browser);try{
  await goTool(f,'environment');await goTool(f,'diskcopy');
  assert(await f.page.locator('#vmReceiveTitle').isVisible()&&await f.page.locator('#vmSendTitle').isVisible(),'both sides show until a role is chosen');
  await f.page.locator('[data-diskrole="send"]').click();assert(await f.page.locator('#vmReceiveTitle').isHidden());assert(await f.page.locator('#vmSendTitle').isVisible());assert.equal(await f.page.locator('#vmFlowSend.here').count(),1);
  await f.page.locator('[data-diskrole="receive"]').click();assert(await f.page.locator('#vmSendTitle').isHidden());assert(await f.page.locator('#vmReceiveTitle').isVisible());assert.equal(await f.page.locator('[data-diskrole="receive"]').getAttribute('aria-pressed'),'true');
  await f.page.locator('[data-diskrole="receive"]').click();assert(await f.page.locator('#vmSendTitle').isVisible(),'choosing the role again shows both sides');
  assert.equal(await f.page.locator('#vmReceiveFolder').getAttribute('placeholder'),'D:\\Migration\\VMware-Copies\\Received');
  await f.page.locator('#vmReceiveFolder + .path-pick-button').click();await f.page.waitForFunction(()=>document.querySelector('#vmReceiveFolder').value==='E:\\Picked');
  assert.deepEqual({kind:f.pickCalls[0].kind,current:f.pickCalls[0].current},{kind:'folder',current:'D:\\Migration\\VMware-Copies\\Received'},'Browse opens the picker at the default folder');
  await f.page.locator('#vmMediaDrivers + .path-pick-button').click();await f.page.waitForFunction(()=>document.querySelector('#vmMediaDrivers').value==='E:\\Picked');
  await f.page.locator('#vmMediaDrivers').fill('D:\\virtio-win\\amd64\\2k22');await f.page.locator('#vmMediaDrivers + .path-pick-button').click();await f.page.waitForFunction(()=>document.querySelector('#vmMediaDrivers').value==='D:\\virtio-win\\amd64\\2k22;E:\\Picked');
  f.rejectPick('Synthetic picker failure.');await f.page.locator('#vmReceiveFolder + .path-pick-button').click();await f.page.waitForFunction(()=>(document.querySelector('#vmReceiveFolder').parentNode.nextSibling||{}).textContent==='Synthetic picker failure.');assert.equal(f.dialogs.length,0,'a picker failure stays inline');f.rejectPick('');
  await f.page.locator('#vmReceiveFolder').fill('');
  await f.page.locator('#vmReceivePort').fill('44a');await f.page.locator('#vmReceiveStart').click();assert((await f.page.locator('#vmReceiveStatus').textContent()).includes('as a number'));assert.equal(vmwareCalls(f,'receiver').length,0);
  await f.page.locator('#vmReceivePort').fill('44390');await f.page.locator('#vmReceiveStart').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmReceiveCode').textContent==='K7QMZ-2RT4X-9WBNP-H3VDA');
  assert.deepEqual(vmwareCalls(f,'receiver').map(c=>c.body),[{action:'start',folder:'',port:44390,firewall:true,discoverable:true,newIdentity:false}]);
  const where=await f.page.locator('#vmReceiveAddress').textContent();assert(where.includes('192.0.2.40:44390')&&where.includes('allowed in Windows Firewall'),where);
  // Keep receiving after a reboot installs the receiver service on this computer; unticking removes it.
  assert(await f.page.locator('#vmReceiveService').isEnabled()&&!await f.page.locator('#vmReceiveService').isChecked());
  await f.page.locator('#vmReceiveService').check();await f.page.waitForFunction(()=>/receiver service is set up/.test(document.querySelector('#vmReceiveStatus').textContent));
  assert.deepEqual(vmwareCalls(f,'receiver').at(-1).body,{action:'service-on'});
  await f.page.waitForFunction(()=>document.querySelector('#vmReceiveService').checked);
  await f.page.locator('#vmReceiveService').uncheck();await f.page.waitForFunction(()=>/receiver service was removed/.test(document.querySelector('#vmReceiveStatus').textContent));
  assert.deepEqual(vmwareCalls(f,'receiver').at(-1).body,{action:'service-off'});
  assert(await f.page.locator('#vmReceiveStart').isHidden());
  await f.page.locator('#vmSendFind').click();await f.page.waitForFunction(()=>document.querySelectorAll('[data-send-disk]').length===2);
  assert((await f.page.locator('#vmSendDisks').textContent()).includes('Disk 0, 100 GB (Windows): no drive letter FAT32'));
  await f.page.locator('#vmSendAddress').fill('hv01.example.com');await f.page.locator('#vmSendThumbprint').fill('3F:9A:6C:0E 1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e');await f.page.locator('#vmSendCode').fill('K7QMZ-2RT4X');
  assert(await f.page.locator('#vmSendStart').isDisabled(),'this computer is receiving');
  await f.page.locator('#vmReceiveStop').click();await f.page.waitForFunction(()=>!document.querySelector('#vmReceiveIdle').classList.contains('hidden'));assert.deepEqual(vmwareCalls(f,'receiver').at(-1).body,{action:'stop'});
  // A copy of the application left running holds the port: the start error names it and one button closes exactly that process and starts again.
  f.state.vmware.receiverHeld=true;await f.page.locator('#vmReceiveStart').click();await f.page.waitForFunction(()=>!document.querySelector('#vmReceiveOtherRow').classList.contains('hidden'));
  assert((await f.page.locator('#vmReceiveStatus').textContent()).includes('another copy of this application (process 4321'));
  await f.page.locator('#vmReceiveCloseOther').click();await f.page.waitForFunction(()=>document.querySelector('#vmReceiveIdle').classList.contains('hidden'),null,{timeout:15000});
  assert.deepEqual(vmwareCalls(f,'receiver').slice(-3).map(c=>c.body.action+(c.body.pid?':'+c.body.pid:'')),['start','close-other:4321','start']);assert(await f.page.locator('#vmReceiveOtherRow').isHidden());
  await f.page.locator('#vmReceiveStop').click();await f.page.waitForFunction(()=>!document.querySelector('#vmReceiveIdle').classList.contains('hidden'));
  await f.page.locator('[data-send-disk="1"]').uncheck();
  await f.page.locator('#vmSendStart').click();assert((await f.page.locator('#vmSendStatus').textContent()).includes('20-character pairing code'));assert.equal(vmwareCalls(f,'agent/send').length,0);
  await f.page.locator('#vmSendCode').fill('K7QMZ-2RT4X-9WBNP-H3VDA');
  await f.page.locator('#vmSendCheck').click();await f.page.waitForFunction(()=>document.querySelectorAll('[data-send-target]').length===1);
  assert.equal(await f.page.locator('[data-send-target="0"]').inputValue(),'disk-0','a raw receiver maps disks in order');
  assert((await f.page.locator('#vmSendReceiverNote').textContent()).includes('Writes 2 disks of the new VM or server'));
  const checks=vmwareCalls(f,'agent/receiverdisks').length;
  await f.page.locator('#vmSendThumbprint').fill('3f:9a:6c:0e:1b:7d:4a:25:c8:e0:f1:3b:6a:9d:2c:4e:7f:0a:1b:3c:5d:7e:9f:2a:4c:6e:8b:0d:1f:3a:5c:7e');
  await f.page.waitForFunction(()=>document.querySelector('#vmSendReceiverNote').textContent.includes('Writes 2 disks of the new VM or server')&&document.querySelectorAll('[data-send-target]').length===1);
  assert(vmwareCalls(f,'agent/receiverdisks').length>checks,'a changed thumbprint is checked again by itself');
  assert.equal(vmwareCalls(f,'agent/receiverdisks').at(-1).body.thumbprint,'3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e','colons are dropped');
  await f.page.locator('[data-send-target="0"]').selectOption('disk-1');await f.page.locator('#vmSendLimit').fill('200');
  await f.page.locator('#vmSendStart').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert.deepEqual(vmwareCalls(f,'agent/send').map(c=>c.body),[{disks:[0],address:'hv01.example.com',thumbprint:'3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e',code:'K7QMZ-2RT4X-9WBNP-H3VDA',verify:true,allowLive:false,targets:{'0':'disk-1'},compress:true,limitMbps:200}]);
  assert(await f.page.locator('[data-send-target="0"]').isDisabled(),'targets are locked while sending');
  assert(await f.page.locator('#vmStop').isVisible(),'a send can be stopped');assert(await f.page.locator('#vmSendCode').isDisabled());
  Object.assign(f.state.vmware,{running:false,action:'',status:'idle',message:'Sent 1 disk to hv01.example.com.',send:[{disk:0,name:'APP01-disk0.vhdx',bytesRead:42949672960,bytesWritten:1073741824,resumed:true,verified:true,passes:2,shadowed:1,bytesSent:536870912}],sendSerial:1});
  await f.page.waitForFunction(()=>document.querySelector('#vmSendResults').textContent.includes('APP01-disk0.vhdx'));
  const sent=await f.page.locator('#vmSendResults').textContent();assert(sent.includes('1.0 GB written (512 MB on the network) (pass 2, only what changed), 1 volume from a shadow copy, verified'),sent);
  f.state.vmware.receiver={running:true,port:44380,code:'K7QMZ-2RT4X-9WBNP-H3VDA',thumbprint:'3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e',folder:'D:\\Migration\\VMware-Copies\\Received',addresses:['192.0.2.40'],sessions:1,firewall:true,defaultFolder:'D:\\Migration\\VMware-Copies\\Received'};
  await f.page.waitForFunction(()=>document.querySelector('#vmReceiveSessions').textContent==='1 disk open');
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmwareBody').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:shots+'/vmware-disk-copy-1440x1000.png'});
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});

 await check('Receiver discovery: Find receivers lists what answers, fills the address and thumbprint, pairs by approval with a check number, and the receiver shows Allow and Deny',async()=>{const f=await fixture(browser);try{
  await goTool(f,'environment');await goTool(f,'diskcopy');
  await f.page.locator('#vmReceiveDiscover').uncheck();await f.page.locator('#vmReceiveStart').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmReceiveCode').textContent!=='');
  assert.equal(vmwareCalls(f,'receiver').at(-1).body.discoverable,false);
  assert.equal(await f.page.locator('#vmReceiveFingerprint').textContent(),'3F9A 6C0E 1B7D 4A25');assert((await f.page.locator('#vmReceiveDiscoverNote').textContent()).includes('Not announced'));
  f.state.vmware.receiver.pairRequests=[{id:'0f'.repeat(16),computer:'APP01',address:'192.0.2.60',check:'123 456',state:'pending'}];
  await f.page.waitForSelector('[data-pair-allow]');
  const ask=await f.page.locator('#vmReceivePairs').textContent();assert(ask.includes('APP01 (192.0.2.60) asks to send disks here')&&ask.includes('123 456'),ask);
  await f.page.locator('[data-pair-allow]').click();await f.page.waitForFunction(()=>!document.querySelector('[data-pair-allow]'));
  assert.deepEqual(vmwareCalls(f,'receiver/pair').map(c=>c.body),[{id:'0f'.repeat(16),allow:true}]);
  await f.page.locator('#vmReceiveStop').click();await f.page.waitForFunction(()=>!document.querySelector('#vmReceiveIdle').classList.contains('hidden'));
  // The sender searches, picks one and asks to pair.
  await f.page.locator('#vmSendFinder [data-finder-host]').fill('hv09.example.com');await f.page.locator('#vmSendFinder [data-finder-find]').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmSendFinder').textContent.includes('No receiver answered on hv09.example.com'));
  await f.page.locator('#vmSendFinder [data-finder-host]').fill('');await f.page.locator('#vmSendFinder [data-finder-find]').click();
  await f.page.waitForFunction(()=>document.querySelectorAll('#vmSendFinder [data-finder-use]').length===2);
  assert.deepEqual(vmwareCalls(f,'receivers/find').map(c=>c.body),[{host:'hv09.example.com'},{host:''}]);
  const found=await f.page.locator('#vmSendFinder .vm-found').textContent();assert(found.includes('HV01')&&found.includes('VHDX folder, 4.0 TB free, version 3.2.34')&&found.includes('Fingerprint 3F9A 6C0E 1B7D 4A25')&&found.includes('2 disks (300 GB)'),found);
  await f.page.locator('#vmSendFinder [data-finder-use="192.0.2.51:44380"]').click();
  assert.equal(await f.page.locator('#vmSendAddress').inputValue(),'192.0.2.51:44380');assert.equal(await f.page.locator('#vmSendThumbprint').inputValue(),'ab'.repeat(32));
  assert((await f.page.locator('#vmSendFinder .vm-pair-box').textContent()).includes('Type the pairing code shown on its console'));assert.equal(await f.page.locator('#vmSendFinder [data-finder-pair]').count(),0);
  await f.page.locator('#vmSendFinder [data-finder-use="192.0.2.40:44380"]').click();
  assert.equal(await f.page.locator('#vmSendAddress').inputValue(),'192.0.2.40:44380');assert.equal(await f.page.locator('#vmSendCode').inputValue(),'');
  await f.page.locator('#vmSendFinder [data-finder-pair]').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmSendFinder .vm-pair-check')&&document.querySelector('#vmSendFinder .vm-pair-check').textContent==='123 456');
  assert.deepEqual(vmwareCalls(f,'receivers/pair')[0].body,{address:'192.0.2.40:44380',thumbprint:'3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e'});
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('#vmSendFinder').scrollIntoViewIfNeeded();await f.page.screenshot({path:shots+'/receiver-discovery-1440x1000.png'});
  f.receiverPair.allowed=true;
  await f.page.waitForFunction(()=>document.querySelector('#vmSendCode').value==='K7QMZ-2RT4X-9WBNP-H3VDA',null,{timeout:8000});
  assert((await f.page.locator('#vmSendFinder').textContent()).includes('Paired with HV01'));
  assert.equal(vmwareCalls(f,'receivers/pair').at(-1).body.id,'0f'.repeat(16));
  assert(!(await f.page.locator('#vmSendFinder').textContent()).includes('K7QMZ'),'the code is never shown in the finder');
  // The replication job editor has the same finder.
  assert.equal(await f.page.locator('#rpRecvFinder [data-finder-find]').count(),1);
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware Inside the VM: recording and checks need no confirmation; fix-ups need the typed computer name and send only the chosen items',async()=>{const f=await fixture(browser);try{
  await goTool(f,'environment');await goTool(f,'guest');assert(await f.page.locator('#vmGuestEmpty').isVisible());
  assert((await f.page.locator('#vmViewGuest .vm-runs-on').textContent()).includes('Nothing has to be installed in the machine being moved'));assert.equal(await f.page.locator('[data-tool="guest"] .tool-name').textContent(),'Prepare and fix up');assert((await f.page.locator('#vmToolRuns').textContent()).includes('nothing installed in it'));
  assert(await f.page.locator('input[name="vmGuestMode"][value="local"]').isChecked(),'with nothing chosen, checks run on this computer as before');
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
  // Windows activation: the channel's fix, a MAK warning, and the AVMA key on a Hyper-V host.
  assert((await f.page.locator('#vmGuestActivationText').textContent()).includes('Windows uses a MAK key'));assert(await f.page.locator('#vmGuestAvma').isVisible());
  await f.page.locator('#vmGuestKey').fill('ABCDE-12345');assert(await f.page.locator('#vmGuestActivate').isDisabled(),'a malformed key is refused');
  await f.page.locator('#vmGuestAvma').click();assert.equal(await f.page.locator('#vmGuestKey').inputValue(),'WWVGQ-PNHV9-B89P4-8GGM9-9HPQ4');
  await f.page.locator('#vmGuestActivate').click();assert((await f.page.locator('#migrateDialog').textContent()).includes('AVMA works only on a Hyper-V host running an activated Windows Server Datacenter'));
  await f.page.locator('#migrateConfirmInput').fill('SRV-DEMO-01');await f.page.locator('#migrateConfirmInput').press('Enter');await f.page.waitForFunction(()=>!document.querySelector('#migrateDialog').open);
  assert.deepEqual(vmwareCalls(f,'guest/activate').map(c=>c.body),[{items:[{productKey:'WWVGQ-PNHV9-B89P4-8GGM9-9HPQ4'}],confirm:'SRV-DEMO-01'}]);assert.equal(await f.page.locator('#vmGuestKey').inputValue(),'','the key is cleared from the page after use');
  Object.assign(f.state.vmware,{running:false,action:''});await f.page.waitForTimeout(1100);
  f.vm.guest=vmwareGuestFixture('VMware');f.state.vmware.guestSerial=2;await f.page.waitForTimeout(1100);assert(await f.page.locator('#vmGuestTools').isDisabled(),'VMware Tools stays while the VM is on VMware');
  assert(await f.page.locator('#vmGuestDisks').isEnabled());await f.page.locator('input[data-vm-disk="1"]').uncheck();assert(await f.page.locator('#vmGuestDisks').isDisabled());
  f.state.isAdministrator=false;await f.page.waitForTimeout(1100);assert(await f.page.locator('#vmGuestAdmin').isVisible());assert(await f.page.locator('#vmGuestCapture').isDisabled());
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('VMware Before and after: both packages are chosen, compared, and changes to check are separated from expected ones',async()=>{const f=await fixture(browser);try{
  f.state.restore.packages.lastCollection='C:\\Reports\\APP01_20261121';
  await goTool(f,'environment');await goTool(f,'compare');assert(await f.page.locator('#vmCompareRun').isDisabled());
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
  await goTool(f,'convert');assert.equal(await f.page.locator('[data-tool="convert"]').getAttribute('aria-pressed'),'true');assert(await f.page.locator('#tabConvert').isVisible());
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
  await goTool(f,'convert');await f.page.locator('#cvStop').click();await f.page.waitForTimeout(1300);assert.equal(f.convertCalls.filter(c=>c.action==='cancel').length,1);assert.equal(await f.page.locator('#cvStop').textContent(),'Stopping\u2026');
  Object.assign(f.state.convert,{running:false,cancelRequested:false,status:'canceled',message:'Stopped. Convert again with the same output file to continue where it stopped.',endedAtUnixMs:Date.now()});await f.page.waitForTimeout(1300);
  assert.equal(await f.page.locator('#cvStart').textContent(),'Continue');assert.equal(await f.page.locator('#convertTabBadge').textContent(),'Stopped');
  Object.assign(f.state.convert,{status:'success',message:'Converted. Every chunk was read back and matches the source.',progress:{pass:1,chunks:200,done:200,bytesRead:21474836480,bytesWritten:21474836480,changed:0,zero:120}});await f.page.waitForTimeout(1300);
  assert.equal(await f.page.locator('#cvRunTitle').textContent(),'Conversion complete');await f.page.locator('#cvOpenFolder').click();await f.page.waitForTimeout(200);assert.deepEqual(f.opens,['convertfolder']);
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isEnabled(),'collection unlocks after converting');
  // A disk too large for the chosen format cannot be started.
  await goTool(f,'convert');f.setConvertSize(3*1099511627776);await f.page.locator('#cvInspect').click();await f.page.waitForFunction(()=>document.querySelector('#cvInfoSize').textContent.includes('TB'));
  await f.page.locator('.cv-format[data-format=vhd]').click();assert(await f.page.locator('#cvStart').isDisabled());assert((await f.page.locator('#cvSummary').textContent()).includes('holds at most'));
  await f.page.locator('.cv-format[data-format=vhdx]').click();assert(await f.page.locator('#cvStart').isEnabled());
  assert.deepEqual(f.dialogs,[]);assert(f.convertCalls.every(c=>c.token==='workspace-test'));
 }finally{await f.close();}});
 await check('Disk converter tab: fix-ups take the converted VHDX, pick VirtIO drivers for KVM platforms, post the choice, lock while running and show what Windows has',async()=>{const f=await fixture(browser);try{
  await goTool(f,'convert');await f.page.locator('#cvBrowse').click();await f.page.waitForFunction(()=>document.querySelector('#cvInfoBoot').textContent==='UEFI (GPT)');
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
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isDisabled(),'collection locks while fixing up');await goTool(f,'convert');
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
  await goTool(f,'convert');await f.page.locator('#cvBrowse').click();await f.page.waitForFunction(()=>document.querySelector('#cvInfoBoot').textContent==='UEFI (GPT)');
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
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isDisabled(),'collection locks while sending');await goTool(f,'convert');
  await f.page.screenshot({path:out+'/convert-send.png'});
  Object.assign(f.state.convert,{running:false,status:'success',message:'Created APP01 on Proxmox VE. It is off; check its settings, then start it.'});f.state.convert.send=Object.assign({},f.state.convert.send,{result:{platform:'proxmox',vmId:'105',vmName:'APP01'}});await f.page.waitForTimeout(1300);
  assert.equal(await f.page.locator('#cvSendTitle').textContent(),'VM created');assert(await f.page.locator('#cvSendGo').isDisabled(),'the confirmation is cleared after a send');
  await f.page.locator('#cvSendForget').click();await f.page.waitForFunction(()=>document.querySelector('#cvSendVM').classList.contains('hidden'));
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Disk converter tab: This computer lists the local disks for P2V, checks the chosen disk and converts it to a file on another disk',async()=>{const f=await fixture(browser);try{
  await goTool(f,'convert');await f.page.locator('#cvLocal').click();await f.page.waitForFunction(()=>document.querySelectorAll('.cv-local-disk').length===2);
  assert.equal(await f.page.locator('.cv-local-disk').first().textContent(),'Disk 0 · 128 GB · C: · Windows');
  await f.page.locator('.cv-local-disk').first().click();await f.page.waitForFunction(()=>document.querySelector('#cvInfoFormat').textContent.startsWith('Disk 0'));
  assert.equal(await f.page.locator('#cvSource').inputValue(),'\\\\.\\PhysicalDrive0');assert.equal(await f.page.locator('#cvInfoBoot').textContent(),'UEFI (GPT)');assert(!(await f.page.locator('#cvLocalBox').isVisible()));
  await f.page.locator('#cvFolder').click();await f.page.waitForFunction(()=>document.querySelector('#cvTarget').value.startsWith('E:'));assert.equal(await f.page.locator('#cvTarget').inputValue(),'E:\\Hyper-V\\Disks\\SRV-DEMO-01-PhysicalDrive0.vhdx');
  await f.page.locator('#cvStart').click();await f.page.waitForTimeout(1300);
  const start=f.convertCalls.filter(c=>c.action==='start').pop();assert.equal(start.body.source,'\\\\.\\PhysicalDrive0');assert.equal(start.body.format,'vhdx');
  assert(await f.page.locator('#cvLocal').isDisabled(),'the disk list locks while converting');assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});

 await check('Test failover: copies from one round start a disconnected test VM after the typed name; the copies show as held and End test needs the name again',async()=>{const f=await fixture(browser);try{
  await vmwareConnected(f);await goTool(f,'diskcopy');
  assert(await f.page.locator('#vmFailover').isHidden(),'Start VMs from copies is its own tool');assert(await f.page.locator('#vmMediaCard').isVisible(),'receiver media is built in Receive and send disks');await goTool(f,'failover');
  await f.page.locator('#fvRefresh').click();await f.page.waitForFunction(()=>document.querySelectorAll('[data-fv-copy]').length===3);
  assert(await f.page.locator('[data-fv-copy="SQL01-disk0.vhdx"]').isDisabled(),'a copy being received cannot be chosen');
  assert((await f.page.locator('#fvCopies').textContent()).includes('being received now. It unlocks by itself when this round finishes'));
  // Finished copies of the newest round are ticked without a click, and the button says why it is still disabled.
  assert(await f.page.locator('[data-fv-copy="APP01-disk0.vhdx"]').isChecked());assert(await f.page.locator('[data-fv-copy="APP01-disk1.vhdx"]').isChecked());
  // A test VM is named NAME-TEST and confirmed for you; the button says why when something is missing.
  assert.equal(await f.page.locator('#fvConfirm').inputValue(),'APP01-TEST');assert.equal(await f.page.locator('#fvWhy').textContent(),'');
  // Production fills the source's network: the VMware port group's VLAN and the host's only external switch.
  assert((await f.page.locator('#fvNetSource').textContent()).includes('test VM starts with its network not connected'));
  await f.page.locator('#fvModeProd').check();
  assert.equal(await f.page.locator('#fvSwitch').inputValue(),'LAN');assert.equal(await f.page.locator('#fvVlan').inputValue(),'20');
  assert((await f.page.locator('#fvNetSource').textContent()).includes('VMware port group VM-Servers of APP01'));
  await f.page.locator('#fvModeTest').check();
  assert.equal(await f.page.locator('#fvSwitch').inputValue(),'','back to a test: not connected again');assert.equal(await f.page.locator('#fvVlan').inputValue(),'');
  // A test VM on an external switch would meet the running source: warned, and it needs a tick.
  await f.page.locator('#fvSwitch').selectOption('LAN');await f.page.locator('#fvVlan').fill('120');
  assert(await f.page.locator('#fvNetWarn').isVisible());assert((await f.page.locator('#fvNetWarnText').textContent()).includes('LAN is an external switch (VLAN 120)'));
  assert(await f.page.locator('#fvStartButton').isDisabled());assert((await f.page.locator('#fvWhy').textContent()).includes('would join external switch LAN'));
  await f.page.locator('#fvJoinNetwork').check();assert(await f.page.locator('#fvStartButton').isEnabled());
  await f.page.locator('#fvSwitch').selectOption('Isolated test');assert(await f.page.locator('#fvNetWarn').isHidden(),'a private switch is fine');assert(!(await f.page.locator('#fvJoinNetwork').isChecked()));
  await f.page.locator('#fvSwitch').selectOption('');await f.page.locator('#fvVlan').fill('');
  await f.page.locator('#fvConfirm').fill('APP01');assert((await f.page.locator('#fvWhy').textContent()).includes('does not match the VM name: type APP01-TEST exactly'));assert(await f.page.locator('#fvStartButton').isDisabled());await f.page.locator('#fvConfirm').fill('');
  assert.equal(await f.page.locator('#fvWhy').textContent(),'Type APP01-TEST in "Type the VM name to confirm".');
  // Sized from the source the agent recorded, with a warning when lowered.
  assert((await f.page.locator('#fvSource').textContent()).includes('From the source (the replication agent on APP01): 6 processors, 12.0 GB memory, UEFI, 2 network adapters'));
  assert.equal(await f.page.locator('#fvCpus').inputValue(),'6');assert.equal(await f.page.locator('#fvMemory').inputValue(),'12');assert.equal(await f.page.locator('#fvAdapters').inputValue(),'2');
  await f.page.locator('#fvCpus').fill('4');assert((await f.page.locator('#fvSizeWarn').textContent()).includes('Lower than the source: 4 processors (the source has 6)'));await f.page.locator('#fvCpus').fill('6');assert(await f.page.locator('#fvSizeWarn').isHidden());
  // The firmware follows the boot disk: a mismatch is explained and Secure boot is hidden for generation 1.
  await f.page.locator('#fvGen').selectOption('1');assert((await f.page.locator('#fvGenWarn').textContent()).includes('uses UEFI (GPT partitions), so Generation 1 will not start it'));assert(await f.page.locator('#fvUefiRow').isHidden());
  await f.page.locator('#fvGen').selectOption('2');assert(await f.page.locator('#fvGenWarn').isHidden());assert(await f.page.locator('#fvUefiRow').isVisible());
  // The list follows the receiver by itself: a copy that finishes unlocks with no click.
  f.failover.copies[2]=Object.assign({},f.failover.copies[2],{busy:true,generation:0});
  await f.page.waitForFunction(()=>document.querySelector('#fvCopies').textContent.includes('SQL01-disk0.vhdx (100 GB, firmware known once finished'),null,{timeout:15000});
  f.failover.copies[2]=Object.assign({},f.failover.copies[2],{busy:false,complete:true,generation:1,round:'s1@2026-11-25T14:10:00Z',job:'s1',recoveryPoint:'2026-11-25T14:10:00Z'});
  await f.page.waitForFunction(()=>!document.querySelector('[data-fv-copy="SQL01-disk0.vhdx"]').disabled,null,{timeout:15000});
  assert(!(await f.page.locator('[data-fv-copy="SQL01-disk0.vhdx"]').isChecked()),'a copy that finishes later is not added to the disks already chosen');
  f.failover.copies[2]=Object.assign({},f.failover.copies[2],{busy:true,complete:false,generation:0});
  await f.page.waitForFunction(()=>document.querySelector('[data-fv-copy="SQL01-disk0.vhdx"]').disabled,null,{timeout:15000});
  await f.page.locator('[data-fv-copy="APP01-disk0.vhdx"]').check();await f.page.locator('[data-fv-copy="APP01-disk1.vhdx"]').check();
  assert.equal(await f.page.locator('#fvName').inputValue(),'APP01-TEST');assert.equal(await f.page.locator('#fvGen').inputValue(),'2','a GPT boot disk starts as generation 2');
  assert.equal(await f.page.locator('#fvSwitch').inputValue(),'','a test VM starts disconnected');assert.equal(await f.page.locator('#fvSwitch option').count(),3);
  assert(await f.page.locator('#fvStartButton').isDisabled());await f.page.locator('#fvConfirm').fill('app01-test');assert(await f.page.locator('#fvStartButton').isEnabled());
  await f.page.locator('#fvStartButton').click();await f.page.waitForFunction(()=>document.querySelector('#fvList').textContent.includes('APP01-TEST'));
  const start=f.replCalls.filter(c=>c.action==='failover'&&c.method==='POST')[0].body;
  assert.deepEqual(start,{action:'start',confirm:'app01-test',request:{mode:'test',name:'APP01-TEST',copies:['APP01-disk0.vhdx','APP01-disk1.vhdx'],generation:2,cpus:6,memoryMB:12288,secureBoot:'windows',tpm:false,switch:'',vlan:0,start:true,adapters:2}});
  assert((await f.page.locator('#fvList').textContent()).includes('Do not activate the test VM'));assert((await f.page.locator('#fvNext').textContent()).includes('Test running: APP01-TEST'));
  assert((await f.page.locator('#fvCopies').textContent()).includes('in use: test failover APP01-TEST'));
  const end=f.page.locator('[data-fv-end="f1"]');assert(await end.isDisabled());
  // The list refreshes every 4 seconds: an unchanged answer leaves the page alone (no jump while scrolled), and a changed one keeps what is typed and where.
  await f.page.locator('[data-fv-endname="f1"]').fill('APP01');await f.page.evaluate(()=>{document.querySelector('#fvList li').simmMark=1;});await f.page.waitForTimeout(5500);
  assert(await f.page.evaluate(()=>document.querySelector('#fvList li').simmMark===1),'an unchanged refresh rebuilt the list');
  f.failover.copies[0].updated='2026-11-25T14:30:00Z';await f.page.waitForFunction(()=>document.querySelector('#fvList li').simmMark!==1,null,{timeout:10000});
  assert.equal(await f.page.locator('[data-fv-endname="f1"]').inputValue(),'APP01');assert(await f.page.evaluate(()=>document.activeElement&&document.activeElement.getAttribute('data-fv-endname')==='f1'));
  await f.page.locator('[data-fv-endname="f1"]').fill('APP01-TEST');assert(await end.isEnabled());
  await end.click();await f.page.waitForFunction(()=>document.querySelector('#fvRunning').classList.contains('hidden'));
  assert((await f.page.locator('#fvStatus').textContent()).includes('catches up'));
  assert((await f.page.locator('#fvNext').textContent()).includes('Next: cut over'));await f.page.locator('[data-fv-next="replicate"]').click();await f.page.waitForFunction(()=>!document.querySelector('#tabReplication').classList.contains('hidden'));
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});

 await check('Replication tab: Linux servers install over SSH, a new host key is shown and trusted only after the password is typed again',async()=>{const f=await fixture(browser);try{
  await goTool(f,'replicate');
  assert(await f.page.locator('#rpSshPortField').isHidden());await f.page.locator('#rpPlatform').selectOption('linux');
  assert(await f.page.locator('#rpSshPortField').isVisible());assert(await f.page.locator('#rpPushHelpLinux').isVisible());
  assert(await f.page.locator('#rpPushSignInCurrent').isDisabled()&&await f.page.locator('#rpPushSignInOther').isChecked(),'Linux installs need a typed account');assert((await f.page.locator('#rpPushSignInWhy').textContent()).includes('SSH'));assert(await f.page.locator('#rpPushHelp').isHidden());
  await f.page.locator('#rpHosts').fill('web01.example.com');await f.page.locator('#rpPassword').fill('Fict1onal!');await f.page.locator('#rpPush').click();
  assert((await f.page.locator('#rpPushStatus').textContent()).includes('Linux account'));assert.equal(f.replCalls.filter(c=>c.action==='push').length,0);
  await f.page.locator('#rpUser').fill('root');await f.page.locator('#rpSshPort').fill('2222');await f.page.locator('#rpPush').click();
  await f.page.waitForFunction(()=>!document.querySelector('#rpPushStop').classList.contains('hidden'));
  assert.deepEqual(f.replCalls.filter(c=>c.action==='push').map(c=>c.body),[{hosts:'web01.example.com',user:'root',password:'Fict1onal!',port:44390,platform:'linux',sshPort:2222,hostKeys:{}}]);
  const key='SHA256:Fict1onalHostKeyFingerprint0123456789abcdefABC';
  Object.assign(f.repl,{pushing:false,serial:1,pushResults:[{host:'web01.example.com',ok:false,error:'web01.example.com offered SSH host key '+key+', which is not trusted yet',hostKey:key}]});
  await f.page.waitForFunction(()=>document.querySelector('[data-rp-trust]'));
  await f.page.locator('[data-rp-trust]').click();assert((await f.page.locator('#rpPushStatus').textContent()).includes('password again'));assert.equal(f.replCalls.filter(c=>c.action==='push').length,1);
  await f.page.locator('#rpPassword').fill('Fict1onal!');await f.page.locator('[data-rp-trust]').click();
  await f.page.waitForFunction(()=>!document.querySelector('#rpPushStop').classList.contains('hidden'));
  const second=f.replCalls.filter(c=>c.action==='push')[1].body;
  assert.deepEqual(second,{hosts:'web01.example.com',user:'root',password:'Fict1onal!',port:44390,platform:'linux',sshPort:2222,hostKeys:{'web01.example.com':key}});
 }finally{await f.close();}});

 await check('Replication tab: Alt+8 reaches it; Install agents posts the servers and account once; a job is built from the agent disks and the receiver disks; cutover needs the typed job name',async()=>{const f=await fixture(browser);try{
  await goTool(f,'replicate');assert.equal(await f.page.locator('[data-tool="replicate"]').getAttribute('aria-pressed'),'true');assert((await f.page.locator('#rpPushTitle').textContent()).includes('servers you are copying'));
  await f.page.waitForFunction(()=>document.querySelector('#rpAgents').textContent.includes('No servers yet'));
  assert(await f.page.locator('#rpPush').isDisabled(),'nothing to install yet');
  await f.page.locator('#rpHosts').fill('app01.example.com, sql01.example.com');await f.page.locator('#rpPushSignInOther').check();await f.page.locator('#rpUser').fill('EXAMPLE\\admin');
  await f.page.locator('#rpPush').click();assert((await f.page.locator('#rpPushStatus').textContent()).includes('password'));assert.equal(f.replCalls.filter(c=>c.action==='push').length,0);
  await f.page.locator('#rpPassword').fill('Fict1onal!');await f.page.locator('#rpPush').click();
  await f.page.waitForFunction(()=>!document.querySelector('#rpPushStop').classList.contains('hidden'));
  assert.deepEqual(f.replCalls.filter(c=>c.action==='push').map(c=>c.body),[{hosts:'app01.example.com, sql01.example.com',user:'EXAMPLE\\admin',password:'Fict1onal!',port:44390}]);
  assert.equal(await f.page.locator('#rpPassword').inputValue(),'','the password is cleared once sent');assert(await f.page.locator('#rpHosts').isDisabled());
  Object.assign(f.repl,{pushing:false,serial:1,pushResults:[{host:'app01.example.com',ok:true},{host:'sql01.example.com',ok:false,error:'the account or password was refused by sql01.example.com'}],
   agents:[{id:'ag1',name:'APP01',address:'app01.example.com:44390',thumbprint:'3F:9A',os:'Windows',pushed:true,live:{reachable:true,version:'3.3.0',computer:'APP01',platform:'Windows',jobs:[],alerts:[{id:1,time:'2026-11-25T13:10:00Z',job:'x',jobName:'FILE01',level:'warning',kind:'rpo',text:'FILE01 is behind: its recovery point is 32 minutes old, more than the 15 minutes allowed.',cleared:false}]}}],
   summary:{agents:1,unreachable:0,jobs:0,healthy:0,behind:0,failing:0,paused:0,cutOver:0,alerts:1}});
  await f.page.waitForFunction(()=>document.querySelector('#rpPushResults').textContent.includes('refused by sql01'));
  assert((await f.page.locator('#rpAlerts').textContent()).includes('FILE01 is behind'));assert.equal(await f.page.locator('#replicationTabBadge').textContent(),'1 alert');
  await f.page.locator('[data-rp-new="ag1"]').click();await f.page.waitForFunction(()=>document.querySelectorAll('[data-rp-disk]').length===2);
  assert.equal(await f.page.locator('[data-rp-target="0"]').inputValue(),'APP01-disk0.vhdx');assert((await f.page.locator('#rpJobDisks').textContent()).includes('(spanned)'));
  await f.page.locator('#rpRecvAddress').fill('192.0.2.50');await f.page.locator('#rpRecvThumbprint').fill('3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e');await f.page.locator('#rpRecvCode').fill('K7QMZ-2RT4X-9WBNP-H3VDA');
  await f.page.locator('#rpRecvCheck').click();await f.page.waitForFunction(()=>document.querySelector('[data-rp-target="1"]').tagName==='SELECT');
  assert.equal(await f.page.locator('[data-rp-target="1"]').inputValue(),'disk-1');
  assert.deepEqual(f.replCalls.filter(c=>c.action==='agent'&&c.body.action==='receiverdisks').at(-1).body,{action:'receiverdisks',id:'ag1',address:'192.0.2.50',alternates:[],thumbprint:'3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e',code:'K7QMZ-2RT4X-9WBNP-H3VDA'},'the server checks the receiver');
  assert((await f.page.locator('#rpRecvNote').textContent()).includes('Checked from the server'));
  // Copy all from a receiver with several networks: the console cannot
  // reach any of them (it is not the sender), so the server checks each and
  // the job keeps the one the server reached, with the rest as alternates.
  await f.page.locator('#rpRecvAddress').evaluate((e,text)=>{const d=new DataTransfer();d.setData('text/plain',text);e.dispatchEvent(new ClipboardEvent('paste',{clipboardData:d,bubbles:true,cancelable:true}));},'Receiver: 10.10.100.11:44380\r\nOther addresses: 198.51.100.9:44380\r\nThumbprint: 3F:9A:6C:0E:1B:7D:4A:25:C8:E0:F1:3B:6A:9D:2C:4E:7F:0A:1B:3C:5D:7E:9F:2A:4C:6E:8B:0D:1F:3A:5C:7E\r\nPairing code: K7QMZ-2RT4X-9WBNP-H3VDA');
  await f.page.waitForFunction(()=>document.querySelector('#rpRecvAddress').value==='198.51.100.9:44380',null,{timeout:8000});
  await f.page.waitForFunction(()=>document.querySelector('#rpRecvNote').textContent.includes('could not reach 10.10.100.11:44380'));
  await f.page.waitForFunction(()=>document.querySelector('[data-rp-target="1"]').tagName==='SELECT');
  await f.page.locator('#rpInterval').selectOption('5');await f.page.locator('#rpLimit').fill('100');await f.page.locator('#rpBusinessLimit').fill('20');await f.page.locator('#rpBusinessStart').fill('8');await f.page.locator('#rpBusinessEnd').fill('18');
  await f.page.locator('#rpServices').fill('MSSQLSERVER, W3SVC');await f.page.locator('#rpJobSave').click();
  await f.page.waitForFunction(()=>!document.querySelector('#rpDetails').classList.contains('hidden'));
  assert.deepEqual(f.replCalls.filter(c=>c.action==='job'&&c.body.action==='save').map(c=>c.body),[{agent:'ag1',action:'save',spec:{id:'',name:'APP01',disks:[{source:'0',target:'disk-0'},{source:'1',target:'disk-1'}],receiver:{address:'198.51.100.9:44380',alternates:['10.10.100.11:44380'],thumbprint:'3F9A6C0E1B7D4A25C8E0F13B6A9D2C4E7F0A1B3C5D7E9F2A4C6E8B0D1F3A5C7E',code:'K7QMZ-2RT4X-9WBNP-H3VDA'},intervalMinutes:5,rpoMinutes:15,compress:true,throttle:{limitMbps:100,businessMbps:20,businessStart:8,businessEnd:18},stopServices:['MSSQLSERVER','W3SVC'],shutdownAfterCutover:true,fullReadAtCutover:false,rescanFirst:false,allowLive:false,space:{reservePercent:10,reserveGB:20}}}]);
  assert(await f.page.locator('#rpEditor').isHidden());assert.equal(await f.page.locator('#rpRecvCode').inputValue(),'','the pairing code is cleared');
  await f.page.waitForFunction(()=>document.querySelector('#rpDetailsFacts').textContent.includes('Every 5 minutes'));
  assert((await f.page.locator('#rpAgents').textContent()).includes('Up to date'));
  assert(await f.page.locator('#rpCutover').isDisabled(),'cutover needs the typed name');
  assert(await f.page.locator('#rpFinishByHand').isDisabled(),'finishing by hand needs the typed name');
  await f.page.locator('#rpCutoverTyped').fill('app01');assert(await f.page.locator('#rpCutover').isEnabled());assert(await f.page.locator('#rpFinishByHand').isEnabled());
  await f.page.locator('#rpLogButton').click();await f.page.waitForFunction(()=>document.querySelector('#rpLog').textContent.includes('Round 2 finished'));
  // Receiver-media jobs offer the platform test failover.
  await f.page.waitForFunction(()=>!document.querySelector('#ttSection').classList.contains('hidden'));
  assert.equal(await f.page.locator('#ttSource').inputValue(),'APP01');assert.equal(await f.page.locator('#ttName').inputValue(),'APP01-TEST');
  assert(await f.page.locator('#ttStart').isDisabled(),'start needs a connection and the typed name');
  await f.page.locator('#ttPlatform').selectOption('proxmox');assert((await f.page.locator('#ttUserLabel').textContent()).includes('API token ID'));assert(await f.page.locator('#ttNodeField').isVisible());
  await f.page.locator('#ttServer').fill('pve1.example.com');await f.page.locator('#ttUser').fill('migrate@pve!move');await f.page.locator('#ttPassword').fill('Synthetic-Token-Secret');
  await f.page.locator('#ttConnectButton').click();await f.page.waitForFunction(()=>document.querySelector('#ttConnected').textContent.includes('Connected to Proxmox VE at pve1.example.com'));
  assert.equal(await f.page.locator('#ttPassword').inputValue(),'','the token secret is cleared once sent');assert(await f.page.locator('#ttConnect').isHidden());
  await f.page.locator('#ttTyped').fill('app01-test');await f.page.locator('#ttStart').click();
  await f.page.waitForFunction(()=>document.querySelector('#ttLog').textContent.includes('Pausing APP01'));
  assert.deepEqual(f.replCalls.filter(c=>c.action==='targettest'&&c.method==='POST').map(c=>c.body),[{action:'start',agent:'ag1',job:'a1b2c3d4e5f60718',sourceVM:'APP01',name:'APP01-TEST',confirm:'app01-test'}]);
  assert(await f.page.locator('#ttCancel').isVisible());
  Object.assign(f.targetTest,{running:false,log:f.targetTest.log.concat(['14:31:10  APP01-TEST is running from the recovery point of 2026-11-25 14:19:23 UTC with its network disconnected.']),tests:[{id:'t1',platform:'proxmox',server:'pve1.example.com',agent:'ag1',job:'a1b2c3d4e5f60718',jobName:'APP01',sourceVM:'APP01',sourceID:'150',name:'APP01-TEST',vmID:'205',recoveryPoint:'2026-11-25T14:19:23Z',started:'2026-11-25T14:31:10Z'}]});
  await f.page.waitForFunction(()=>document.querySelector('#ttTests').textContent.includes('APP01-TEST runs on pve1.example.com'),null,{timeout:15000});
  assert(await f.page.locator('[data-tt-end="t1"]').isDisabled(),'End test needs the name');
  await f.page.locator('[data-tt-typed="t1"]').fill('APP01-TEST');await f.page.locator('[data-tt-end="t1"]').click();
  await f.page.waitForFunction(()=>document.querySelector('#ttLog').textContent.includes('replica was not touched'));
  assert.deepEqual(f.replCalls.filter(c=>c.action==='targettest'&&c.body.action==='end').map(c=>c.body),[{action:'end',id:'t1',confirm:'APP01-TEST'}]);
  await f.page.waitForFunction(()=>!document.querySelector('#rpRunbook').disabled);await f.page.locator('#rpRunbook').click();await f.page.waitForFunction(()=>!document.querySelector('#rpRunbookDocx').classList.contains('hidden'));
  assert.equal(f.replCalls.filter(c=>c.action==='runbook').length,1);assert(f.opens.includes('replrunbook'));await f.page.locator('#rpRunbookDocx').click();await f.page.waitForFunction(()=>true);
  await f.page.setViewportSize({width:1440,height:1000});await f.page.evaluate(()=>document.activeElement.blur());await f.page.locator('.rp-body').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:out+'/replication-1440x1000.png'});await f.page.locator('.rp-body').evaluate(e=>{const h=document.querySelector('.rp-heading');e.scrollTop=h.offsetTop-e.offsetTop-8;});await f.page.screenshot({path:out+'/replication-jobs-1440x1000.png'});
  await f.page.locator('#rpCutover').click();await f.page.waitForFunction(()=>document.querySelector('#rpDetailsStatus').textContent.includes('Cutting over'));
  assert.deepEqual(f.replCalls.filter(c=>c.action==='job'&&c.body.action==='cutover').map(c=>c.body),[{agent:'ag1',id:'a1b2c3d4e5f60718',action:'cutover',typed:'app01'}]);
  assert(f.replCalls.every(c=>c.token==='workspace-test'));assert.deepEqual(f.errors,[]);assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('New migration: one sequence of screens picks VMs, fills the target, maps networks, recommends the way, sets waves and timed cutovers, checks, and starts a migration plan with one tick box (the default confirmation)',async()=>{const f=await fixture(browser);try{f.state.ui={schema:1,confirmStyle:'tick'};
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();f.vm.inventory.vms[1].snapshots=[{name:'before patch',created:'2026-10-06T00:00:00Z'}];
  f.vm.inventory.vms.find(v=>v.id==='vm-app01').mediaImages=['[DS-SSD-01] ISO Files/en_windows_server_2022.iso'];
  f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',account:'administrator@vsphere.local',summary:vmwareSummaryFixture(),inventorySerial:1,canPrepare:true,hyperVHere:true,hostStorage:{known:true,vhdPath:'D:\\Hyper-V\\Disks',vmPath:'D:\\Hyper-V',clustered:false,volumes:[{path:'D:\\',freeBytes:1099511627776,totalBytes:2199023255552,csv:false}]}});
  await f.page.keyboard.press('Alt+5');assert.equal(await f.page.locator('[data-tool="wizard"]').getAttribute('aria-pressed'),'true','New migration is the first thing in Migration and replication');
  assert(await f.page.locator('#toolsAdvancedTitle').isVisible(),'the separate tools are listed under Tools (advanced)');
  assert.equal(await f.page.locator('#nwWhy').textContent(),'Choose what you are moving.');assert(await f.page.locator('#nwNext').isDisabled());
  await f.page.locator('[data-nw-source="vms"]').click();await f.page.waitForSelector('#nwVmList [data-nw-pick]');
  assert(await f.page.locator('[data-nw-pick="vm-legacy03"]').isDisabled(),'a blocked VM cannot be ticked');assert((await f.page.locator('#nwVmList').textContent()).includes('Windows Server 2003 is not supported'),'and the list says why');
  assert.equal(await f.page.locator('#nwWhy').textContent(),'Tick the VMs to move.');
  await f.page.locator('[data-nw-pick="vm-app01"]').check();await f.page.locator('[data-nw-pick="vm-sql01"]').check();
  assert((await f.page.locator('[data-nw="count"]').textContent()).startsWith('2 VMs ticked'));await f.page.screenshot({path:out+'/new-migration-what-1440x1000.png'});
  await f.page.locator('#nwFilter').fill('sql');assert.equal(await f.page.locator('#nwVmList [data-nw-pick]').count(),1,'search narrows the list');assert.equal(await f.page.evaluate(()=>document.activeElement.id),'nwFilter','typing keeps focus');await f.page.locator('#nwFilter').fill('');
  await f.page.locator('#nwNext').click();
  assert.equal(await f.page.locator('.nw-stepper-item.current').textContent(),'2Where');
  await f.page.locator('[data-nw-target="here"]').click();
  assert.equal(await f.page.locator('#nwCopyRoot').inputValue(),'D:\\Hyper-V\\Disks','the copy folder comes from the host');
  assert.equal(await f.page.locator('[data-nw-switch]').count(),1,'only the networks the chosen VMs use');
  assert.equal(await f.page.locator('[data-nw-vlan="VM-Servers"]').inputValue(),'20','the VLAN comes from the source');
  assert.equal(await f.page.locator('#nwWhy').textContent(),'Choose a switch for VM-Servers.');
  await f.page.locator('[data-nw-switch="VM-Servers"]').selectOption('Isolated');assert.equal(await f.page.locator('#nwWhy').textContent(),'');await f.page.screenshot({path:out+'/new-migration-where-1440x1000.png'});
  await f.page.locator('#nwNext').click();
  assert.equal(await f.page.locator('[data-nw-method="plans"]').getAttribute('aria-pressed'),'true','a migration plan is recommended for this host');
  assert((await f.page.locator('[data-nw-method="plans"]').textContent()).includes('Recommended'));assert((await f.page.locator('[data-nw-method="plans"]').textContent()).includes('Pick this when'));
  assert(await f.page.locator('#nwAdvanced').isVisible());assert(await f.page.locator('#nwSync').isHidden(),'advanced options stay folded');
  await f.page.locator('[data-nw-method="movevms"]').click();assert.equal((await f.page.locator('#nwNext').textContent()),'Continue in Move VMs','another way hands off, and says so');assert(!(await f.page.locator('#nwStep').textContent()).includes('Options for a migration plan'),'only the chosen way\'s options are shown');
  await f.page.locator('[data-nw-method="plans"]').click();await f.page.screenshot({path:out+'/new-migration-how-1440x1000.png'});
  {assert(await f.page.locator('#nwEject').isChecked(),'ejecting CD/DVD images is offered, ticked, for a VM whose drive names an ISO');const t=await f.page.locator('label:has(#nwEject)').textContent();assert(t.includes('APP01: en_windows_server_2022.iso')&&t.includes('no longer exists'),t);}await f.page.locator('#nwAdvanced summary').click();await f.page.locator('#nwSync').selectOption('30');
  await f.page.locator('#nwNext').click();
  const at=new Date(Date.now()+48*3600000),pad=n=>String(n).padStart(2,'0'),local=at.getFullYear()+'-'+pad(at.getMonth()+1)+'-'+pad(at.getDate())+'T22:00';
  const waves=await f.page.locator('[data-nw-cutmode]').evaluateAll(n=>n.map(x=>x.getAttribute('data-nw-cutmode')));assert(waves.length>=1);
  await f.page.locator('[data-nw-cutmode="'+waves[0]+'"]').selectOption('at');assert((await f.page.locator('#nwWhy').textContent()).startsWith('Choose the cutover time'));
  await f.page.locator('[data-nw-cutat="'+waves[0]+'"]').fill(local);
  await f.page.locator('[data-nw-wave="vm-sql01"]').selectOption(String(Math.max(...waves.map(Number))+1));await f.page.screenshot({path:out+'/new-migration-when-1440x1000.png'});
  await f.page.locator('#nwNext').click();
  assert((await f.page.locator('.nw-checks').textContent()).includes('VMware snapshots'),'snapshots are caught before Start');assert(await f.page.locator('#nwNext').isDisabled());
  await f.page.locator('#nwSnapshots').click();assert(await f.page.locator('#migrateDialog').isVisible());assert(!await f.page.locator('#migrateConfirmInput').isVisible());assert((await f.page.locator('#migrateConfirmTickLabel').textContent()).includes('every snapshot of 1 VM'));assert(await f.page.locator('#migrateConfirmButton').isDisabled());await f.page.locator('#migrateDialogCancel').click();
  f.vm.inventory.vms[1].snapshots=[];f.state.vmware.inventorySerial=2;
  await f.page.waitForFunction(()=>!document.querySelector('#nwSnapshots'),null,{timeout:15000});
  {const t=await f.page.locator('.nw-summary').textContent();assert(t.includes('VM-Servers to Isolated VLAN 20'),t);}
  assert(await f.page.locator('#nwPreflight').evaluate(d=>d.open),'the optional SQL and DC checks open for a VM named like a SQL Server');assert.equal(await f.page.locator('#nwPreflight [data-pf-vm]').first().getAttribute('data-pf-vm'),'vm-sql01','likely ones first');assert(await f.page.locator('#pfNwRun').isVisible());
  const name=await f.page.locator('#nwName').inputValue();assert(name.startsWith('Migration '));
  assert.equal(await f.page.locator('#nwWhy').textContent(),'Tick "I understand" to start '+name+'.');
  assert(!await f.page.locator('#nwConfirm').isVisible(),'no name to type');assert((await f.page.locator('label:has(#nwConfirmTick)').textContent()).includes('I understand Start creates the migration '+name));
  await f.page.locator('#nwConfirmTick').check();assert(!await f.page.locator('#nwNext').isDisabled());await f.page.screenshot({path:out+'/new-migration-review-1440x1000.png'});await f.page.locator('#nwNext').click();
  await f.page.waitForFunction(()=>document.querySelector('.nw-stepper-item.current')&&document.querySelector('.nw-stepper-item.current').textContent==='6Status',null,{timeout:15000});
  const saved=f.vmwareCalls.filter(c=>c.action==='plan'&&c.method==='POST').pop();const net=saved.body.plan.networks.find(n=>n.source==='VM-Servers');assert.equal(net.targetName,'Isolated');assert.equal(net.targetVlan,'20');
  assert(saved.body.plan.vms.find(v=>v.id==='vm-sql01').manual,'a wave changed here is kept by later suggestions');
  const created=f.planCalls.find(c=>c.body.action==='create');assert.deepEqual(created.body.vmIds.sort(),['vm-app01','vm-sql01']);assert.equal(created.body.settings.copyRoot,'D:\\Hyper-V\\Disks');assert.equal(created.body.settings.syncMinutes,30);assert.equal(created.body.settings.disconnectSource,true);assert.equal(created.body.settings.ejectMedia,true,'the Eject tick reaches the plan');
  const cut=f.planCalls.find(c=>c.body.action==='cutover');assert(cut,'the timed cutover is scheduled at Start');assert.equal(cut.body.confirm,name);assert.equal(cut.body.vmIds.length,1);assert.equal(new Date(cut.body.at).getHours(),22);
  await f.page.waitForSelector('[data-nw-filter="total"]');assert((await f.page.locator('#nwStatusHead').textContent()).includes(name));
  assert(await f.page.locator('#nwCutNow').isDisabled());
  f.plans.plans[0].vms[0]=Object.assign(f.plans.plans[0].vms[0],{state:'ready',seeded:true,wave:1});f.plans.plans[0].vms[1]=Object.assign(f.plans.plans[0].vms[1],{state:'copying',wave:2,live:{percent:40,bytesRead:42949672960,bytesPerSecond:104857600,secondsLeft:600,elapsed:400}});f.plans.plans[0].summary=Object.assign(f.plans.plans[0].summary,{waiting:0,copying:1,ready:1});
  await f.page.waitForFunction(()=>document.querySelector('#nwStatusBody').textContent.includes('40%'),null,{timeout:15000});
  await f.page.locator('[data-nw-filter="copying"]').click();assert.equal(await f.page.locator('#nwStatusBody [data-nw-row]').count(),1,'tiles filter the list');await f.page.locator('[data-nw-filter="copying"]').click();
  await f.page.screenshot({path:out+'/new-migration-status-1440x1000.png'});
  await f.page.locator('[data-nw-row="'+f.plans.plans[0].vms[0].id+'"]').check();assert((await f.page.locator('#nwStatusMsg').textContent()).startsWith('Tick "I understand"'));
  for(const width of [1904,2560]){await f.page.setViewportSize({width,height:1000});await f.page.waitForTimeout(100);await f.page.locator('#nwCutNow').scrollIntoViewIfNeeded();await f.page.screenshot({path:out+'/new-migration-status-'+width+'.png'});const apart=await apartControls(f.page);assert.deepEqual(apart,[],'Status at '+width+': '+apart.join('; '));
   const g=await f.page.evaluate(()=>{const t=document.querySelector('#nwCutConfirmTick').closest('label').getBoundingClientRect(),b=document.querySelector('#nwCutNow').getBoundingClientRect(),m=document.querySelector('#nwStatusMsg').getBoundingClientRect();return {tickToButton:b.left-t.right,msgLeft:m.left,buttonLeft:b.left,msgTop:m.top,buttonBottom:b.bottom,buttonRight:b.right};});
   assert(g.tickToButton>=0&&g.tickToButton<=32,'the cutover button sits beside its tick box at '+width+': '+JSON.stringify(g));assert(Math.abs(g.msgLeft-g.buttonLeft)<=2||(g.msgLeft>=g.buttonRight&&g.msgLeft-g.buttonRight<=32),'the reason sits under or beside the button at '+width+': '+JSON.stringify(g));}
  await f.page.setViewportSize({width:1440,height:1000});
  await f.page.locator('#nwCutConfirmTick').check();await f.page.locator('#nwCutNow').click();
  for(let i=0;i<100&&f.planCalls.filter(c=>c.body.action==='cutover').length<2;i++){await new Promise(r=>setTimeout(r,50));}const now=f.planCalls.filter(c=>c.body.action==='cutover').pop();assert.equal(now.body.at,'now');assert.equal(now.body.confirm,name);
  await f.page.setViewportSize({width:1008,height:690});await f.page.locator('[data-nw-step]').first().waitFor();
  assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth+1),'no horizontal scroll at 1024x768');
 }finally{await f.close();}});
 await check('New migration: a VM with encrypted disks is flagged in What, blocks Start in Review and is decrypted there with a tick box and live progress',async()=>{const f=await fixture(browser);try{
  f.state.ui={schema:1,confirmStyle:'tick'};f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();
  const encDisk={label:'Hard disk 1',controller:'SCSI 0',bus:0,unit:0,capacityBytes:107374182400,datastore:'DS-SSD-01',provisioning:'thin',mode:'persistent',rdm:'',encrypted:true};
  f.vm.inventory.vms.push(vmFixtureVM('DESK21',{hv:'blocked',hvFindings:[{severity:'blocker',code:'encrypted',message:'1 of the VM\'s 1 disks are encrypted with vSphere VM Encryption, so no other platform can read them.',action:'Decrypt it.'}],vm:{encrypted:true,encryptionRead:true,vtpm:true,disks:[encDisk]}}));
  f.vm.inventory.vms.push(vmFixtureVM('DESK22',{vm:{encrypted:true,encryptionRead:true,vtpm:true,powerState:'poweredOff'}}));
  f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',account:'administrator@vsphere.local',summary:vmwareSummaryFixture(),inventorySerial:1,canPrepare:true,hyperVHere:true,hostStorage:{known:true,vhdPath:'D:\\Hyper-V\\Disks',vmPath:'D:\\Hyper-V',clustered:false,volumes:[{path:'D:\\',freeBytes:1099511627776,totalBytes:2199023255552,csv:false}]}});
  await goTool(f,'wizard');await f.page.locator('[data-nw-source="vms"]').click();await f.page.waitForSelector('#nwVmList [data-nw-pick]');
  assert(!(await f.page.locator('[data-nw-pick="vm-desk21"]').isDisabled()),'a VM blocked only by encrypted disks can be ticked, because it is decrypted here');
  assert(await f.page.locator('[data-nw-pick="vm-legacy03"]').isDisabled(),'other blockers still stop a VM');
  const list=await f.page.locator('#nwVmList').textContent();assert(list.includes('Encrypted disks: tick it and decrypt it in Review'),list);assert(list.includes('the disks copy as they are'),'a VM encrypted only for its virtual TPM needs nothing');
  for(const w of [1440,1008]){await f.page.setViewportSize({width:w,height:w===1440?1000:690});
   const tiles=await f.page.evaluate(()=>['vm-desk21','vm-desk22'].map(id=>{const row=document.querySelector('[data-nw-pick="'+id+'"]').closest('.nw-vm'),cb=row.querySelector('input').getBoundingClientRect(),name=row.querySelector('strong').getBoundingClientRect(),chip=row.querySelector('.run-chip').getBoundingClientRect(),note=row.querySelector('.nw-enc').getBoundingClientRect(),list=document.querySelector('#nwVmList').getBoundingClientRect();return{id,tag:row.tagName,nameW:name.width,cbMid:cb.top+cb.height/2,nameMid:name.top+name.height/2,chipMid:chip.top+chip.height/2,noteLeft:note.left,chipRight:chip.right,noteRight:note.right,listRight:list.right};}));
   for(const t of tiles){assert.equal(t.tag,'TR',w+': '+t.id+' is a table row');assert(t.nameW>20,w+': '+t.id+' shows its name');assert(Math.abs(t.cbMid-t.nameMid)<12&&Math.abs(t.chipMid-t.nameMid)<12,w+': '+t.id+' tick box, name and readiness share the row ('+JSON.stringify(t)+')');assert(t.noteLeft>=t.chipRight-1,w+': '+t.id+' encryption note is in the Notes column');}}
  await f.page.setViewportSize({width:1440,height:1000});
  await f.page.locator('[data-nw-pick="vm-desk21"]').check();await f.page.locator('[data-nw-pick="vm-desk22"]').check();
  assert((await f.page.locator('[data-nw="count"]').textContent()).includes('1 VM has encrypted disks: Review decrypts it'),'encryption is flagged on the first screen');
  await f.page.locator('#nwNext').click();await f.page.locator('[data-nw-target="here"]').click();await f.page.locator('[data-nw-switch="VM-Servers"]').selectOption('Isolated');
  await f.page.locator('#nwNext').click();await f.page.locator('#nwNext').click();await f.page.locator('#nwNext').click();
  const checks=await f.page.locator('.nw-checks').textContent();assert(checks.includes('disks encrypted with vSphere VM Encryption'),checks);assert(checks.includes('DESK22: only the VM\'s own files are encrypted'),checks);
  await f.page.locator('#nwConfirmTick').check();
  assert(await f.page.locator('#nwNext').isDisabled(),'Start stays disabled while a VM is still encrypted');assert((await f.page.locator('#nwWhy').textContent()).includes('encrypted'));
  assert.equal(await f.page.locator('#nwDecrypt').textContent(),'Decrypt 1 VM…');
  await f.page.locator('#nwDecrypt').click();assert(await f.page.locator('#migrateDialog').isVisible());
  const text=await f.page.locator('#migrateDialogText').textContent();assert(text.includes('shut down through VMware Tools')&&text.includes('about as long as copying them'),text);assert(text.includes('DESK21: shut down, remove the virtual TPM, decrypt'),text);
  assert((await f.page.locator('#migrateDialogAlert').textContent()).includes('BitLocker'),'removing the virtual TPM warns about BitLocker');
  assert(await f.page.locator('#migrateConfirmInput').isHidden(),'nothing to type');assert(await f.page.locator('#migrateConfirmButton').isDisabled());
  await f.page.locator('#migrateConfirmTick').check();assert(!(await f.page.locator('#migrateConfirmButton').isDisabled()));await f.page.locator('#migrateConfirmButton').click();
  await f.page.waitForFunction(()=>!document.querySelector('#migrateDialog').open);
  assert.deepEqual(vmwareCalls(f,'prepare').map(c=>c.body),[{items:[{vmId:'vm-desk21',steps:{snapshots:false,shutdown:true,removeTpm:true,decrypt:true}}],confirm:'1 VM'}]);
  const started=new Date(Date.now()-125000).toISOString();
  f.state.vmware.prepareBatch=[{id:'vm-desk21',vm:'DESK21',steps:{decrypt:true},state:'running',step:'Decrypting DESK21. vSphere rewrites every encrypted disk, so this takes about as long as copying them.',percent:42,startedAt:started,done:[],skipped:[]}];
  await f.page.waitForFunction(()=>document.querySelector('#nwBatch')&&document.querySelector('#nwBatch').textContent.includes('42% decrypted'),null,{timeout:15000});
  assert((await f.page.locator('#nwBatch').textContent()).includes('Preparing on VMware: 0 of 1 done'));assert(await f.page.locator('#nwBatchStop').isVisible());assert(await f.page.locator('#nwDecrypt').isDisabled(),'no second batch while one runs');
  await f.page.screenshot({path:out+'/new-migration-decrypt-1440x1000.png'});
  const desk=f.vm.inventory.vms.find(v=>v.id==='vm-desk21');Object.assign(desk,{encrypted:false,vtpm:false,powerState:'poweredOff',disks:[Object.assign({},encDisk,{encrypted:false})]});desk.readiness[0]={target:'hyperv',status:'ready',generation:2,findings:[]};
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',inventorySerial:2,prepareBatch:[{id:'vm-desk21',vm:'DESK21',steps:{decrypt:true},state:'done',percent:-1,startedAt:started,endedAt:new Date().toISOString(),done:['Shut down the guest.','Removed the virtual TPM.','Decrypted the VM and its disks.'],skipped:[]}]});
  await f.page.waitForFunction(()=>!document.querySelector('#nwDecrypt'),null,{timeout:15000});
  assert((await f.page.locator('#nwBatch').textContent()).includes('Decrypted the VM and its disks.'));assert.equal(await f.page.locator('#nwWhy').textContent(),'','Start is ready once the VM is decrypted');
  await f.page.setViewportSize({width:1008,height:690});assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth+1),'no horizontal scroll at 1024x768');
  assert.deepEqual(f.errors,[]);f.state.ui={schema:1,confirmStyle:'typed'};
 }finally{await f.close();}});
 await check('New migration: a running VM with a virtual TPM is copied through VDDK or at its cutover while shut down, in the same migration as the others',async()=>{const f=await fixture(browser);try{
  f.state.ui={schema:1,confirmStyle:'tick'};f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();
  f.vm.inventory.vms.push(vmFixtureVM('DESK23',{vm:{encrypted:true,encryptionRead:true,vtpm:true,firmware:'efi'}}));
  f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',account:'administrator@vsphere.local',summary:vmwareSummaryFixture(),inventorySerial:1,canPrepare:true,hyperVHere:true,hostStorage:{known:true,vhdPath:'D:\\Hyper-V\\Disks',vmPath:'D:\\Hyper-V',clustered:false,volumes:[{path:'D:\\',freeBytes:1099511627776,totalBytes:2199023255552,csv:false}]}});
  await goTool(f,'wizard');await f.page.locator('[data-nw-source="vms"]').click();await f.page.waitForSelector('#nwVmList [data-nw-pick]');
  assert((await f.page.locator('#nwVmList').textContent()).includes('Virtual TPM: copied while it runs only with VDDK'),'What says how a running vTPM VM can be copied');
  await f.page.locator('[data-nw-pick="vm-app01"]').check();await f.page.locator('[data-nw-pick="vm-desk23"]').check();
  await f.page.locator('#nwNext').click();await f.page.locator('[data-nw-target="here"]').click();await f.page.locator('[data-nw-switch="VM-Servers"]').selectOption('Isolated');
  await f.page.locator('#nwNext').click();await f.page.locator('#nwNext').click();await f.page.locator('#nwNext').click();
  await f.page.waitForSelector('#nwVddkFolder');
  let checks=await f.page.locator('.nw-checks').textContent();assert(checks.includes('DESK23 runs with a virtual TPM')&&checks.includes('without VDDK')&&checks.includes('The other VMs are not affected'),checks);
  await f.page.locator('#nwConfirmTick').check();assert(await f.page.locator('#nwNext').isDisabled(),'Start waits for a way to copy DESK23');
  await f.page.screenshot({path:out+'/new-migration-vtpm-review-1440x1000.png'});
  // Copy it at its cutover, while shut down: the rest of the migration is unchanged.
  await f.page.locator('#nwVtpmOffline').click();
  await f.page.waitForFunction(()=>document.querySelector('.nw-checks').textContent.includes('DESK23: copied at its cutover, while shut down'),null,{timeout:15000});
  assert(!(await f.page.locator('.nw-checks').textContent()).includes('without VDDK'));assert((await f.page.locator('.nw-summary').textContent()).includes('1 VM copied at cutover while shut down (DESK23)'));
  await f.page.screenshot({path:out+'/new-migration-vtpm-offline-1440x1000.png'});
  // Back to live, then set up VDDK right here.
  await f.page.locator('#nwVtpmLive').click();await f.page.waitForSelector('#nwVddkFolder');
  await f.page.locator('#nwVddkFolder').fill('C:\\Bad');await f.page.locator('#nwVddkSave').click();
  await f.page.waitForFunction(()=>document.querySelector('.nw-checks').textContent.includes('signature is not valid'),null,{timeout:15000});
  await f.page.locator('#nwVddkFolder').fill('C:\\VDDK\\VMware-vix-disklib-8.0.3');await f.page.locator('#nwVddkSave').click();
  await f.page.waitForFunction(()=>document.querySelector('.nw-checks').textContent.includes('VDDK 8.0.3 build 23950268 reads it instead'),null,{timeout:15000});
  assert.deepEqual(f.vddkCalls.map(c=>c.folder),['C:\\Bad','C:\\VDDK\\VMware-vix-disklib-8.0.3']);
  checks=await f.page.locator('.nw-checks').textContent();assert(checks.includes('Cryptographic operations > Direct Access'),checks);
  await f.page.locator('#nwVtpmOffline').waitFor({state:'detached'});
  // Start with DESK23 copied at its cutover: the plan carries each VM's method.
  await f.page.locator('#nwConfirmTick').check();await f.page.locator('#nwNext').click();
  await f.page.waitForFunction(()=>document.querySelector('.nw-stepper-item.current')&&document.querySelector('.nw-stepper-item.current').textContent==='6Status',null,{timeout:15000});
  let created=f.planCalls.find(c=>c.body.action==='create');assert.deepEqual(created.body.methods,{},'with VDDK every VM is copied while it runs');
  // A copy that fails for this reason offers both fixes on its row.
  const desk=f.plans.plans[0].vms.find(v=>v.id==='vm-desk23');Object.assign(desk,{name:'DESK23',state:'failed',failures:1,error:'Hard disk 1: vSphere will not let this VM be read while it runs. Its own files are encrypted (every VM with a virtual TPM has them encrypted; the disks themselves are plain), vSphere refuses to export an encrypted VM, and the ESXi host locks the running disk. Choose the VDDK folder in Help > Settings to copy it while it runs, or copy it at its cutover while it is shut down.'});
  await f.page.waitForSelector('#nwStatusBody [data-mp-offline="vm-desk23"]',{timeout:15000});assert(await f.page.locator('#nwStatusBody [data-mp-vddk="vm-desk23"]').isVisible());
  // A long error is one line in the state column, with the whole text behind Details, kept open across refreshes.
  const errBox=f.page.locator('#nwStatusBody details.mp-err');assert.equal(await errBox.count(),1,'a long error is folded');
  assert(!(await errBox.locator('.err-more').isVisible()),'the full error starts hidden');
  const head=await errBox.locator('summary').textContent();assert(head.length<140&&head.endsWith('Details'),head);
  await errBox.locator('summary').click();await new Promise(r=>setTimeout(r,4500));
  assert(await f.page.locator('#nwStatusBody details.mp-err .err-more').isVisible(),'Details stays open after a refresh');
  assert((await f.page.locator('#nwStatusBody details.mp-err .err-more').textContent()).startsWith('Hard disk 1: vSphere will not let'));
  // With VDDK set up, the same failure offers a retry and VDDK's log, never setup again.
  desk.error='Hard disk 1: vSphere will not let this VM be read while it runs. VDDK could not read it either (could not open [ds] desk23.vmdk: no permission). If the vCenter account is not an administrator, give its role Cryptographic operations > Direct Access. Fix that and sync again to copy it through VDDK while it runs (VDDK\'s own log says more), or copy it at its cutover while it is shut down.';
  await f.page.waitForSelector('#nwStatusBody [data-mp-retry="vm-desk23"]',{timeout:15000});
  assert.equal(await f.page.locator('#nwStatusBody [data-mp-vddk="vm-desk23"]').count(),0,'no Set up VDDK once VDDK is set up');
  assert(await f.page.locator('#nwStatusBody [data-mp-vddklog="vm-desk23"]').isVisible());
  await f.page.locator('#nwStatusBody [data-mp-retry="vm-desk23"]').click();
  for(let i=0;i<100&&!f.planCalls.some(c=>c.body.action==='sync');i++){await new Promise(r=>setTimeout(r,50));}
  assert.deepEqual(f.planCalls.find(c=>c.body.action==='sync').body.vmIds,['vm-desk23'],'Retry with VDDK syncs that VM');
  await f.page.locator('#nwStatusBody [data-mp-offline="vm-desk23"]').click();
  for(let i=0;i<100&&!f.planCalls.some(c=>c.body.action==='method');i++){await new Promise(r=>setTimeout(r,50));}
  const m=f.planCalls.find(c=>c.body.action==='method');assert.deepEqual({vmIds:m.body.vmIds,method:m.body.method},{vmIds:['vm-desk23'],method:'offline'});
  Object.assign(desk,{state:'waiting',failures:0,error:'',method:'offline'});
  await f.page.waitForFunction(()=>document.querySelector('#nwStatusBody').textContent.includes('Copied at its cutover, while shut down'),null,{timeout:15000});
  assert((await f.page.locator('#nwStatusBody').textContent()).includes('Copied at cutover'),'the cutover column says when it is copied');
  await f.page.locator('[data-nw-row="vm-desk23"]').check();await f.page.locator('#nwCutConfirmTick').check();
  assert(!(await f.page.locator('#nwCutNow').isDisabled()),'a VM copied at cutover can be cut over without an earlier copy');
  await f.page.screenshot({path:out+'/new-migration-vtpm-status-1440x1000.png'});
  await f.page.locator('#nwStatusBody [data-mp-vddk="vm-desk23"]').waitFor({state:'detached'});
  await f.page.locator('#nwStatusBody [data-mp-live="vm-desk23"]').click();
  for(let i=0;i<100&&f.planCalls.filter(c=>c.body.action==='method').length<2;i++){await new Promise(r=>setTimeout(r,50));}
  assert.equal(f.planCalls.filter(c=>c.body.action==='method').pop().body.method,'','switching back to live');
  assert.deepEqual(f.errors,[]);f.state.ui={schema:1,confirmStyle:'typed'};
 }finally{await f.close();}});
 await check('New migration: without VDDK, running vTPM VMs can be set to copy at cutover and the plan carries the method',async()=>{const f=await fixture(browser);try{
  f.state.ui={schema:1,confirmStyle:'tick'};f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();
  f.vm.inventory.vms.push(vmFixtureVM('DESK23',{vm:{encrypted:true,encryptionRead:true,vtpm:true}}));
  f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',account:'administrator@vsphere.local',summary:vmwareSummaryFixture(),inventorySerial:1,canPrepare:true,hyperVHere:true,hostStorage:{known:true,vhdPath:'D:\\Hyper-V\\Disks',vmPath:'D:\\Hyper-V',clustered:false,volumes:[{path:'D:\\',freeBytes:1099511627776,totalBytes:2199023255552,csv:false}]}});
  await goTool(f,'wizard');await f.page.locator('[data-nw-source="vms"]').click();await f.page.waitForSelector('#nwVmList [data-nw-pick]');
  await f.page.locator('[data-nw-pick="vm-app01"]').check();await f.page.locator('[data-nw-pick="vm-desk23"]').check();
  await f.page.locator('#nwNext').click();await f.page.locator('[data-nw-target="here"]').click();await f.page.locator('[data-nw-switch="VM-Servers"]').selectOption('Isolated');
  await f.page.locator('#nwNext').click();await f.page.locator('#nwNext').click();await f.page.locator('#nwNext').click();
  await f.page.locator('#nwVtpmOffline').click();await f.page.locator('#nwVtpmLive').waitFor();
  await f.page.locator('#nwConfirmTick').check();await f.page.locator('#nwNext').click();
  await f.page.waitForFunction(()=>document.querySelector('.nw-stepper-item.current')&&document.querySelector('.nw-stepper-item.current').textContent==='6Status',null,{timeout:15000});
  const created=f.planCalls.find(c=>c.body.action==='create');assert.deepEqual(created.body.methods,{'vm-desk23':'offline'});assert.deepEqual(created.body.vmIds.sort(),['vm-app01','vm-desk23'],'both VMs are in one migration');
  assert.deepEqual(f.errors,[]);f.state.ui={schema:1,confirmStyle:'typed'};
 }finally{await f.close();}});
 await check('New migration: a Proxmox target connects inside Where, lists each VM with where it stands, and hands off to Move VMs with the VMs selected',async()=>{const f=await fixture(browser);try{
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',summary:vmwareSummaryFixture(),inventorySerial:1,canPrepare:true,hyperVHere:false});
  await goTool(f,'wizard');await f.page.locator('[data-nw-source="vms"]').click();await f.page.waitForSelector('#nwVmList [data-nw-pick]');await f.page.locator('[data-nw-pick="vm-app01"]').check();await f.page.locator('#nwNext').click();
  assert(await f.page.locator('[data-nw-target="here"]').isDisabled(),'this host is not a Hyper-V host');assert((await f.page.locator('[data-nw-target="here"]').textContent()).includes('is not a Hyper-V host'));
  await f.page.locator('[data-nw-target="proxmox"]').click();await f.page.locator('#nwPfConnect').waitFor();
  assert((await f.page.locator('#nwPfPanel .vm-runs-on').textContent()).includes('Runs on this computer, your admin computer'));
  assert.equal(await f.page.locator('#nwWhy').textContent(),'Connect to Proxmox VE (above).','the platform connection is part of Where');
  await f.page.locator('#nwPfServer').fill('pve1.example.com');await f.page.locator('#nwPfUser').fill('migrate@pve!move');await f.page.locator('#nwPfConnect').click();
  assert.equal(await f.page.locator('#nwPfStatus').textContent(),'Type the token secret.');
  await f.page.locator('#nwPfSecret').fill('Synthetic-Token-Secret');await f.page.locator('#nwPfSecret').press('Enter');
  await f.page.waitForFunction(()=>document.querySelector('#nwPfForget'),null,{timeout:15000});
  assert.deepEqual(f.vmwareCalls.filter(c=>c.action==='target/connect').map(c=>c.body),[{platform:'proxmox',server:'pve1.example.com',user:'migrate@pve!move',password:'Synthetic-Token-Secret',node:''}]);
  assert(!(await f.page.content()).includes('Synthetic-Token-Secret'),'the token secret is not kept in the page');
  assert.equal(await f.page.locator('#nwWhy').textContent(),'');
  await f.page.locator('#nwNext').click();
  assert.equal(await f.page.locator('.nw-stepper-item').count(),3,'a hand-off ends at How');
  assert.equal(await f.page.locator('.nw-queue tbody tr').count(),1,'each chosen VM is listed with where it stands');assert((await f.page.locator('.nw-queue').textContent()).includes('Not created yet'));
  assert.equal(await f.page.locator('.nw-steps li').count(),5,'the steps of this way are listed');
  assert.equal(await f.page.locator('#nwNext').textContent(),'Continue in Move VMs');await f.page.locator('#nwNext').click();
  assert.equal(await f.page.locator('[data-tool="movevms"]').getAttribute('aria-pressed'),'true');assert.equal(await f.page.locator('#vmTargetSelect').inputValue(),'proxmox','the target comes along');
  assert(await f.page.locator('#vmDetail').isVisible(),'the VM opens in Move VMs');
 }finally{await f.close();}});
 await check('New migration: from an admin computer, connect to a remote Hyper-V cluster inside Where, pick a node and Cluster Shared Volume, and start a migration plan that runs against it',async()=>{const f=await fixture(browser);try{
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();f.vm.plan.networks[0].targetName='VM-Servers';f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',account:'administrator@vsphere.local',summary:vmwareSummaryFixture(),inventorySerial:1,canPrepare:true,hyperVHere:false});
  await goTool(f,'wizard');await f.page.locator('[data-nw-source="vms"]').click();await f.page.waitForSelector('#nwVmList [data-nw-pick]');await f.page.locator('[data-nw-pick="vm-app01"]').check();await f.page.locator('#nwNext').click();
  assert((await f.page.locator('[data-nw-target="here"]').textContent()).includes('Another Hyper-V host or cluster'),'the disabled local target points to the remote one');
  await f.page.locator('[data-nw-target="hvremote"]').click();await f.page.locator('#nwHvConnect').waitFor();
  assert((await f.page.locator('#nwHvPanel .vm-runs-on').textContent()).includes('Runs on this computer, your admin computer'));
  assert.equal(await f.page.locator('#nwWhy').textContent(),'Connect to the Hyper-V host or cluster (above).');assert(await f.page.locator('#nwNext').isDisabled());
  await f.page.locator('#nwHvHost').fill('hvclu01.example.com');await f.page.locator('#nwHvSignInOther').check();await f.page.locator('#nwHvUser').fill('EXAMPLE\\admin');await f.page.locator('#nwHvConnect').click();
  assert.equal(await f.page.locator('#nwHvStatus').textContent(),'Type the password for EXAMPLE\\admin, or choose your current sign-in.');
  await f.page.locator('#nwHvPassword').fill('Synthetic-Pass-1');await f.page.locator('#nwHvPassword').press('Enter');
  for(let i=0;i<100&&!f.vmwareCalls.some(c=>c.action==='hvremote/connect');i++){await new Promise(r=>setTimeout(r,50));}
  assert.deepEqual(f.vmwareCalls.filter(c=>c.action==='hvremote/connect').map(c=>c.body),[{host:'hvclu01.example.com',user:'EXAMPLE\\admin',password:'Synthetic-Pass-1'}]);
  assert.equal(await f.page.evaluate(()=>{const p=document.querySelector('#nwHvPassword');return p?p.value:'';}),'','the password is cleared from the page');
  const info={host:'hvclu01.example.com',computer:'HV01',caption:'Microsoft Windows Server 2022 Datacenter',build:20348,systemDrive:'C:',hyperV:true,hyperVModule:true,clusterModule:true,cluster:'HVCLU01',vhdPath:'C:\\ClusterStorage\\Volume1\\Hyper-V',vmPath:'C:\\ClusterStorage\\Volume1\\Hyper-V',
   nodes:[{name:'HV01',state:'Up'},{name:'HV02',state:'Up'},{name:'HV03',state:'Paused'}],volumes:[{path:'C:\\',label:'',freeBytes:42949672960,totalBytes:137438953472,csv:false},{path:'C:\\ClusterStorage\\Volume1',label:'CSV1',freeBytes:536870912000,totalBytes:2199023255552,csv:true},{path:'C:\\ClusterStorage\\Volume2',label:'CSV2',freeBytes:1073741824,totalBytes:107374182400,csv:true}],
   switches:[{name:'LAN',type:'External'}],vms:['OTHER01'],warnings:[],supported:true,support:'Windows Server 2022 Datacenter is supported; failover cluster HVCLU01 can make the VM highly available.'};
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',hvRemote:{connected:true,host:'hvclu01.example.com',user:'EXAMPLE\\admin',info,receiver:'',message:'Connected to HV01.',failed:false,serial:1}});
  await f.page.waitForSelector('#nwNode',{timeout:15000});
  assert.equal(await f.page.locator('#nwNode').inputValue(),'HV01','the node that answered is chosen');assert(await f.page.evaluate(()=>document.querySelector('#nwNode option[value="HV03"]').disabled),'a paused node cannot be chosen');
  assert.equal(await f.page.locator('#nwCopyRoot').inputValue(),'C:\\ClusterStorage\\Volume1\\Migrations','the Cluster Shared Volume with the most room is filled in');
  assert.equal(await f.page.locator('#nwVMFolder').inputValue(),'C:\\ClusterStorage\\Volume1\\VMs');assert(await f.page.locator('#nwCluster').isChecked(),'high availability is on for a cluster');
  assert.equal(await f.page.locator('[data-nw-switch="VM-Servers"]').inputValue(),'LAN','the host\'s only external switch is filled in, not the plan\'s port group name the cluster lacks');
  assert.equal(await f.page.locator('[data-nw-vlan="VM-Servers"]').inputValue(),'20','the VLAN comes from the port group');
  // A switch the cluster does not have blocks Where and Review, and Review never also says every network is fine.
  f.state.vmware.hvRemote=Object.assign({},f.state.vmware.hvRemote,{serial:2,info:Object.assign({},info,{switches:[{name:'Other',type:'External'},{name:'Second',type:'External'}]})});
  await f.page.waitForFunction(()=>document.querySelector('#nwWhy').textContent==='Switch LAN is not on cluster HVCLU01. Choose a switch for VM-Servers.',null,{timeout:15000});
  assert((await f.page.locator('[data-nw-switch="VM-Servers"] option:checked').textContent()).includes('not on every node'));
  f.state.vmware.hvRemote=Object.assign({},f.state.vmware.hvRemote,{serial:3,info});
  await f.page.waitForFunction(()=>document.querySelector('#nwWhy').textContent!=='Switch LAN is not on cluster HVCLU01. Choose a switch for VM-Servers.',null,{timeout:15000});
  await f.page.locator('#nwNode').selectOption('HV02');await f.page.locator('[data-nw-volume$="Volume2"]').click();assert.equal(await f.page.locator('#nwCopyRoot').inputValue(),'C:\\ClusterStorage\\Volume2\\Migrations');
  await f.page.locator('[data-nw-volume$="Volume1"]').click();
  assert.equal(await f.page.locator('#nwWhy').textContent(),'');await f.page.locator('#nwNext').click();
  assert.equal(await f.page.locator('[data-nw-method="plans"]').getAttribute('aria-pressed'),'true','a migration plan is recommended for a remote host too');assert((await f.page.locator('[data-nw-method="plans"]').textContent()).includes('Runs from here, start to finish'));
  await f.page.locator('#nwNext').click();await f.page.locator('#nwNext').click();
  {const t=await f.page.locator('.nw-checks').textContent();assert(t.includes('runs the migration and is connected to cluster HVCLU01'),t);assert(t.includes('created on HV02'),t);}
  {const t=await f.page.locator('.nw-checks').textContent();assert(t.includes('Every network has a switch cluster HVCLU01 has'),t);}
  assert((await f.page.locator('.nw-summary').textContent()).includes('Hyper-V host HV02 in cluster HVCLU01, run from this computer'));
  const name=await f.page.locator('#nwName').inputValue();await f.page.locator('#nwConfirm').fill(name);await f.page.locator('#nwNext').click();
  for(let i=0;i<100&&!f.planCalls.some(c=>c.body.action==='create');i++){await new Promise(r=>setTimeout(r,50));}
  const created=f.planCalls.find(c=>c.body.action==='create');assert(created,'the plan is created');
  assert.equal(created.body.settings.remoteHost,'hvclu01.example.com');assert.equal(created.body.settings.node,'HV02');assert.equal(created.body.settings.copyRoot,'C:\\ClusterStorage\\Volume1\\Migrations');assert.equal(created.body.settings.cluster,true);
  assert(!JSON.stringify(f.planCalls).includes('Synthetic-Pass-1'),'the Hyper-V password never reaches the plan');
 }finally{await f.close();}});
 await check('New migration: Windows and Linux servers replicate to this Hyper-V host from the flow: pick agents, install one inline, only the folder in Where, start jobs after a tick box, and cut over from Status',async()=>{const f=await fixture(browser);try{f.state.ui={schema:1,confirmStyle:'tick'};
  f.servers.rows=[
   {key:'agent:a1',kind:'agent',method:'Agent replication',name:'APP01',state:'agent',stateText:'Agent installed',reason:'The agent answers. Create a replication job to start copying.',percent:-1,lagSeconds:-1,wave:0,host:'app01.example.com',os:'Windows Server 2019',agent:'a1',actions:['createjob']},
   {key:'agent:a2',kind:'agent',method:'Agent replication',name:'DB01',state:'unreachable',stateText:'Not answering',reason:'The agent does not answer: check the server is on.',percent:-1,lagSeconds:-1,wave:0,host:'db01.example.com',agent:'a2',actions:[]},
   {key:'job:a3|j3',kind:'job',method:'Agent replication',name:'WEB01',state:'insync',stateText:'In sync',reason:'A whole copy exists.',percent:-1,lagSeconds:120,wave:0,agent:'a3',job:'j3',actions:['sync','pause','test','cutover','wave']},
   {key:'vm:vm-9',kind:'vm',method:'Not configured',name:'FILE01',state:'notconfigured',stateText:'Not configured',percent:-1,lagSeconds:-1,wave:0,actions:['install']}];
  await goTool(f,'wizard');await f.page.locator('[data-nw-source="servers"]').click();await f.page.waitForSelector('#nwSrvList [data-nw-srv]');
  assert.deepEqual(await f.page.locator('#nwSrvList [data-nw-srv]').evaluateAll(l=>l.map(x=>x.getAttribute('data-nw-srv')+(x.disabled?' off':''))),['agent:a1','agent:a2 off','job:a3|j3 off'],'agents and jobs are listed; only an answering agent without a job can be ticked');
  assert((await f.page.locator('#nwSrvList').textContent()).includes('Already replicating (In sync)'));
  assert.equal(await f.page.locator('#nwWhy').textContent(),'Tick the servers to replicate, or add servers below.');assert(await f.page.locator('#nwNext').isDisabled());
  await f.page.locator('#nwSrvHosts').fill('app02.example.com');await f.page.locator('#nwSrvSignInOther').check();await f.page.locator('#nwSrvUser').fill('EXAMPLE\\admin');await f.page.locator('#nwSrvPassword').fill('Synthetic-Pass-2');await f.page.locator('#nwSrvInstall').click();
  for(let i=0;i<100&&!f.replCalls.some(c=>c.action==='push');i++){await new Promise(r=>setTimeout(r,50));}
  assert.deepEqual(f.replCalls.filter(c=>c.action==='push').map(c=>c.body),[{hosts:'app02.example.com',user:'EXAMPLE\\admin',password:'Synthetic-Pass-2',platform:'windows'}]);
  await f.page.waitForSelector('.nw-push-log');assert.equal(await f.page.locator('#nwSrvPassword').inputValue(),'','the password is cleared after the install starts');
  await f.page.locator('[data-nw-srv="agent:a1"]').check();assert.equal(await f.page.locator('#nwWhy').textContent(),'');await f.page.locator('#nwNext').click();
  assert.deepEqual(await f.page.locator('[data-nw-target]').evaluateAll(l=>l.map(x=>x.getAttribute('data-nw-target'))),['here','scale','nutanix','proxmox','physical']);
  await f.page.locator('[data-nw-target="here"]').click();await f.page.locator('#nwSrvFolder').waitFor();
  assert.equal(await f.page.locator('#nwSrvFolder').getAttribute('placeholder'),'C:\\ClusterStorage\\Volume1\\Hyper-V\\Received');
  {const t=await f.page.locator('#nwStep').textContent();assert(t.includes('no pairing code, thumbprint or Receive panel'),t);assert(!(await f.page.locator('#nwStep [data-nw-switch]').count()),'no network map for agent jobs');}
  await f.page.locator('#nwSrvFolder').fill('relative');assert.equal(await f.page.locator('#nwWhy').textContent(),'Type a full path for the copies, or leave it empty for the default.');
  await f.page.locator('#nwSrvFolder').fill('C:\\ClusterStorage\\Volume1\\Replicas');await f.page.locator('#nwNext').click();
  assert.equal(await f.page.locator('[data-nw-method="replicate"]').getAttribute('aria-pressed'),'true');assert((await f.page.locator('[data-nw-method="replicate"]').textContent()).includes('Runs from here, start to finish'));
  assert.equal(await f.page.locator('#nwNext').textContent(),'Next','replication jobs run from the flow instead of handing off');
  await f.page.locator('#nwSrvInterval').selectOption('5');await f.page.locator('#nwSrvRpo').fill('x');assert.equal(await f.page.locator('#nwWhy').textContent(),'Type the RPO alert as a number of minutes from 1 to 10080.');
  await f.page.locator('#nwSrvRpo').fill('20');await f.page.locator('#nwSrvServices').fill('MSSQLSERVER, W3SVC');await f.page.locator('#nwNext').click();
  assert((await f.page.locator('#nwStep').textContent()).includes('every 5 minutes'));await f.page.locator('#nwNext').click();
  {const t=await f.page.locator('.nw-checks').textContent();assert(t.includes('is a Hyper-V host'),t);assert(t.includes('C:\\ClusterStorage\\Volume1\\Replicas'),t);}
  assert.equal(await f.page.locator('#nwNext').textContent(),'Start');assert.equal(await f.page.locator('#nwWhy').textContent(),'Tick "I understand" to start replicating 1 server.');
  await f.page.locator('#nwSrvConfirmTick').check();await f.page.locator('#nwNext').click();
  for(let i=0;i<100&&!f.serverCalls.some(c=>c.body.action==='createjob');i++){await new Promise(r=>setTimeout(r,50));}
  const created=f.serverCalls.find(c=>c.body.action==='createjob');assert.deepEqual(created.body.keys,['agent:a1']);
  assert.deepEqual(created.body.template,{local:true,folder:'C:\\ClusterStorage\\Volume1\\Replicas',receiver:{address:'',thumbprint:'',code:''},intervalMinutes:5,rpoMinutes:20,compress:true,throttle:{limitMbps:0,businessMbps:0,businessStart:0,businessEnd:0},stopServices:['MSSQLSERVER','W3SVC'],shutdownAfterCutover:true,fullReadAtCutover:false,allowLive:false});
  assert(!JSON.stringify(f.serverCalls).includes('Synthetic-Pass-2'));
  f.servers.rows[0]={key:'job:a1|j1',kind:'job',method:'Agent replication',name:'APP01',detail:'To 192.0.2.40:44380',state:'insync',stateText:'In sync',reason:'A whole copy exists and stays current. Test it, then cut over.',percent:-1,lagSeconds:300,wave:0,agent:'a1',job:'j1',actions:['sync','pause','test','cutover','wave']};
  await f.page.waitForSelector('[data-nw-srvrow="job:a1|j1"]',{timeout:15000});
  assert.equal(await f.page.locator('.nw-status-table tbody tr').count(),1,'only the servers this flow started are listed');
  assert((await f.page.locator('#nwStatusHead').textContent()).includes('Replicating 1 server'));
  assert.equal(await f.page.evaluate(()=>window.localStorage.getItem('simm.wizard.servers')),'["a1"]');
  await f.page.locator('[data-nw-srvrow="job:a1|j1"]').check();await f.page.locator('#nwSrvAction').selectOption('cutover');
  assert(await f.page.locator('#nwSrvRun').isDisabled());assert.equal(await f.page.locator('#nwStatusMsg').textContent(),'Tick "I understand" to confirm.');
  await f.page.locator('#nwSrvActConfirmTick').check();await f.page.locator('#nwSrvRun').click();
  for(let i=0;i<100&&!f.serverCalls.some(c=>c.body.action==='cutover');i++){await new Promise(r=>setTimeout(r,50));}
  assert.deepEqual(f.serverCalls.filter(c=>c.body.action==='cutover').map(c=>c.body),[{action:'cutover',keys:['job:a1|j1'],confirm:'1'}]);
 }finally{await f.close();}});
 await check('New migration: with no inventory the sign-in is on the first screen',async()=>{const f=await fixture(browser);try{
  await goTool(f,'wizard');await f.page.locator('[data-nw-source="vms"]').click();
  await f.page.locator('#nwServer').fill('vc01.example.com');await f.page.locator('#nwUser').fill('administrator@vsphere.local');await f.page.locator('#nwConnect').click();assert.equal(await f.page.locator('#nwConnectStatus').textContent(),'Type the password for administrator@vsphere.local.');
  await f.page.locator('#nwPassword').fill('secret');await f.page.locator('#nwPassword').press('Enter');
  for(let i=0;i<100&&!f.vmwareCalls.some(c=>c.action==='connect');i++){await new Promise(r=>setTimeout(r,50));}const call=f.vmwareCalls.find(c=>c.action==='connect');assert(call&&call.body.server==='vc01.example.com');
  assert.equal(await f.page.evaluate(()=>{const p=document.querySelector('#nwPassword');return p?p.value:'';}),'','the password is cleared from the page');
 }finally{await f.close();}});
 await check('Migration plans: Sync now always says what it did, and a plan waiting for its vCenter password signs in right there',async()=>{const f=await fixture(browser);try{
  const now=new Date().toISOString(),blocked='Sign in to vc01.example.com in Discovery again: the password is kept only while the application runs.';
  f.state.vmware=Object.assign(vmwareIdle(),{server:'vc01.example.com',account:'admin@vsphere.local'});
  f.plans.plans.push({id:'p1',name:'Wave 1',server:'vc01.example.com',paused:false,blocked,settings:{syncMinutes:60,concurrency:2,copyRoot:'D:\\Copies',vmFolder:'D:\\VMs',startAt:now},throttle:'no limit',summary:{total:2,waiting:0,copying:0,ready:2,testing:0,scheduled:0,done:0,failed:0},vms:[{id:'vm-app01',name:'APP01',wave:1,state:'ready',seeded:true,lastSync:now,failures:0},{id:'vm-sql01',name:'SQL01',wave:1,state:'ready',seeded:true,lastSync:now,failures:0}]});
  f.plans.syncAnswer={queued:2,waiting:blocked};
  await goTool(f,'plans');await f.page.waitForSelector('#mpSignIn:not(.hidden)');
  await f.page.waitForFunction(()=>document.querySelector('#mpSignInUser').value==='admin@vsphere.local',null,{timeout:15000});
  assert((await f.page.locator('#mpSignIn').textContent()).includes('Sign in to vc01.example.com so this plan can copy'));
  // Sync all now answers at once, even though the plan cannot copy yet.
  await f.page.locator('#mpSyncAll').click();
  await f.page.waitForFunction(()=>/2 VMs asked to sync, but the plan cannot copy yet/.test(document.querySelector('#mpStatus').textContent),null,{timeout:15000});
  assert((await f.page.locator('#mpStatus').textContent()).includes('Sign in above and it syncs.'));
  assert.equal(await f.page.evaluate(()=>document.activeElement&&document.activeElement.id),'mpSignInPassword','the password box takes the focus');
  // Signing in reads vCenter again and asks the plan to sync; the password never stays in the page.
  await f.page.locator('#mpSignInButton').click();assert.equal(await f.page.locator('#mpSignInStatus').textContent(),'Type the password.');
  await f.page.locator('#mpSignInPassword').fill('S3cret!');await f.page.locator('#mpSignInButton').click();
  await f.page.waitForFunction(()=>/Signed in\. Reading vc01\.example\.com again/.test(document.querySelector('#mpSignInStatus').textContent),null,{timeout:15000});
  const connect=f.vmwareCalls.filter(c=>c.action==='connect'&&c.method==='POST');assert.equal(connect.length,1);assert.deepEqual(connect[0].body,{server:'vc01.example.com',user:'admin@vsphere.local',password:'S3cret!'});
  assert.equal(await f.page.locator('#mpSignInPassword').inputValue(),'','the password is cleared at once');
  await f.page.waitForTimeout(300);
  assert.equal(f.planCalls.filter(c=>c.body.action==='sync').length,2,'signing in asks the plan to sync');
  // Once the plan can copy, the prompt goes and Sync says when copying starts.
  f.plans.plans[0].blocked='';f.plans.syncAnswer={queued:1,skipped:['SQL01 is copying now']};
  await f.page.waitForSelector('#mpSignIn.hidden',{state:'attached',timeout:15000});
  await f.page.locator('#mpSyncAll').click();
  await f.page.waitForFunction(()=>/1 VM asked to sync\. Copying starts within 20 seconds/.test(document.querySelector('#mpStatus').textContent),null,{timeout:15000});
  assert((await f.page.locator('#mpStatus').textContent()).includes('Left alone: SQL01 is copying now.'));
  f.plans.syncAnswer={queued:0,skipped:['APP01 is already cut over']};await f.page.locator('#mpSyncAll').click();
  await f.page.waitForFunction(()=>document.querySelector('#mpStatus').textContent==='Nothing to sync. Left alone: APP01 is already cut over.',null,{timeout:15000});
  assert(!(await f.page.content()).includes('S3cret!'));
 }finally{await f.close();}});
 await check('Remembered vCenter sign-ins are opt-in, listed without passwords and forgettable, and the plan sign-in can turn them on',async()=>{const f=await fixture(browser);try{
  const now=new Date().toISOString(),blocked='Sign in to vc01.example.com in Discovery again: the password is kept only while the application runs.';
  f.state.vmware=Object.assign(vmwareIdle(),{server:'vc01.example.com',account:'svc-migrate@vsphere.local'});
  f.plans.plans.push({id:'p1',name:'Wave 1',server:'vc01.example.com',paused:false,blocked,settings:{syncMinutes:60,concurrency:2,copyRoot:'D:\\Copies',vmFolder:'D:\\VMs',startAt:now},throttle:'no limit',summary:{total:1,waiting:0,copying:0,ready:1,testing:0,scheduled:0,done:0,failed:0},vms:[{id:'vm-app01',name:'APP01',wave:1,state:'ready',seeded:true,lastSync:now,failures:0}]});
  await goTool(f,'plans');await f.page.waitForSelector('#mpSignIn:not(.hidden)');
  assert.equal(await f.page.locator('#mpSignInRemember').isChecked(),false,'remembering is off by default');
  assert((await f.page.locator('#mpSignIn').textContent()).includes('use a dedicated vCenter account'));
  await f.page.waitForFunction(()=>document.querySelector('#mpSignInUser').value==='svc-migrate@vsphere.local',null,{timeout:15000});
  await f.page.locator('#mpSignInRemember').check();await f.page.locator('#mpSignInPassword').fill('Pa55-word!');await f.page.locator('#mpSignInButton').click();
  await f.page.waitForFunction(()=>/Signed in\./.test(document.querySelector('#mpSignInStatus').textContent),null,{timeout:15000});
  const connect=f.vmwareCalls.filter(c=>c.action==='connect'&&c.method==='POST');assert.equal(connect.length,1);assert.equal(connect[0].body.remember,true,'the tick asks to remember it');
  // Help > Settings lists it by server and user only, and Forget and the tick remove it.
  f.vm.remember={enabled:true,entries:[{server:'vc01.example.com',user:'svc-migrate@vsphere.local',saved:now}]};
  await f.page.locator('#helpMenuButton').click();await f.page.locator('#helpSettings').click();await f.page.waitForSelector('#vcRememberList [data-vc-forget="vc01.example.com"]');
  assert(await f.page.locator('#vcRememberOn').isChecked());
  assert((await f.page.locator('#vcRememberList').textContent()).includes('svc-migrate@vsphere.local on vc01.example.com'));
  await f.page.locator('#vcRememberList [data-vc-forget="vc01.example.com"]').click();
  await f.page.waitForFunction(()=>!document.querySelector('#vcRememberList [data-vc-forget]'),null,{timeout:15000});
  assert.deepEqual(f.vmwareCalls.filter(c=>c.action==='remember'&&c.method==='POST').map(c=>c.body),[{forget:'vc01.example.com'}]);
  await f.page.locator('#vcRememberOn').uncheck();
  await f.page.waitForFunction(()=>/kept in memory only/.test(document.querySelector('#vcRememberStatus').textContent),null,{timeout:15000});
  assert.deepEqual(f.vmwareCalls.filter(c=>c.action==='remember'&&c.method==='POST').map(c=>c.body).pop(),{enabled:false});
  assert(!(await f.page.content()).includes('Pa55-word!'),'the password never stays in the page');
 }finally{await f.close();}});
 await check('All jobs and servers: a VM whose sync is running shows Syncing with its progress, overdue or not',async()=>{const f=await fixture(browser);try{
  const now=Date.now(),iso=t=>new Date(t).toISOString();
  const base={kind:'plan',method:'Migration plan',detail:'Plan Wave 1',plan:'p1',percent:-1,lagSeconds:600,wave:1,limitMinutes:60,actions:['sync','pause','cutover','wave']};
  f.servers.rows=[Object.assign({},base,{key:'plan:p1|a',vm:'a',name:'EPIC01',state:'syncing',stateText:'Syncing',reason:'A sync is copying the changes since the last one. A whole copy exists, so it can be cut over.',percent:1,bytesPerSecond:9800000,secondsLeft:4260,lastSync:iso(now-110*60000)}),
   Object.assign({},base,{key:'plan:p1|b',vm:'b',name:'DC02',state:'syncing',stateText:'Syncing',overdue:true,reason:'A sync is copying the changes since the last one. A whole copy exists, so it can be cut over. The last sync is older than twice the plan\'s interval; this one brings it up to date.',percent:3,bytesPerSecond:20200000,secondsLeft:2460,lastSync:iso(now-132*60000),lagSeconds:7920}),
   Object.assign({},base,{key:'plan:p1|c',vm:'c',name:'APP03',state:'insync',stateText:'In sync, ready to cut over',reason:'Seeded and kept in sync. Test it, then cut over.',lastSync:iso(now-600000)})];
  f.servers.summary={total:3,states:{syncing:2,insync:1},scheduled:0,methods:{plan:3}};
  await goTool(f,'servers');await f.page.waitForFunction(()=>document.querySelectorAll('#asRows tr').length>=3,null,{timeout:15000});
  const chips=await f.page.locator('#asRows .as-chip').allTextContents();assert.deepEqual(chips.slice(0,3).sort(),['In sync, ready to cut over','Syncing','Syncing']);
  assert(!(await f.page.locator('#tabServers').textContent()).includes('Tick servers in the list'),'the obvious hint is gone');
  const tile=f.page.locator('#tabServers button[data-as-filter="syncing"]');assert((await tile.textContent()).includes('2'));
  await tile.click();await f.page.waitForTimeout(200);
  const shown=await f.page.locator('#asRows tr .as-name').allTextContents();assert.deepEqual(shown.sort(),['DC02','EPIC01']);
  assert((await f.page.locator('#asRows').textContent()).includes('this one brings it up to date'),'overdue is a detail of the Syncing row');
 }finally{await f.close();}});
 await check('Migration plans: VMs are picked from the inventory, tiles filter the list, live progress shows speed and time left, and cutover needs selected in-sync VMs and the typed plan name',async()=>{const f=await fixture(browser);try{
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',summary:vmwareSummaryFixture(),inventorySerial:1});
  await goTool(f,'plans');await f.page.waitForSelector('#mpNewButton');
  assert((await f.page.locator('[data-tool="plans"] .tool-works').textContent()).includes('this Hyper-V host or its failover cluster'));
  assert((await f.page.locator('#toolWorks-tabPlans').textContent()).includes('Pick this when'),'the pane says when to use it');
  await f.page.locator('#mpNewButton').click();await f.page.waitForFunction(()=>document.querySelectorAll('#mpPicker [data-mp-pick]').length>0,null,{timeout:15000});
  await f.page.locator('#mpCreate').click();assert.equal(await f.page.locator('#mpNewStatus').textContent(),'Name the plan.');
  await f.page.locator('#mpName').fill('Wave 1');await f.page.locator('#mpCreate').click();assert.equal(await f.page.locator('#mpNewStatus').textContent(),'Choose the VMs for the plan.');
  const enabled=await f.page.locator('#mpPicker [data-mp-pick]:not([disabled])').evaluateAll(n=>n.map(i=>i.getAttribute('data-mp-pick')));assert(enabled.length>0);
  await f.page.locator('#mpPicker [data-mp-pick="'+enabled[0]+'"]').check();
  await f.page.locator('#mpStartMode').selectOption('at');await f.page.locator('#mpStartAt').fill('2026-12-01T22:00');await f.page.locator('#mpBizMbps').fill('200');await f.page.locator('#mpTestSwitch').selectOption('Isolated');
  await f.page.locator('#mpCreate').click();await f.page.waitForFunction(()=>!document.querySelector('#mpDetail').classList.contains('hidden'),null,{timeout:15000});
  const created=f.planCalls.find(c=>c.body.action==='create');assert.deepEqual(created.body.vmIds,[enabled[0]]);assert.equal(created.body.settings.throttle.businessMbps,200);assert.equal(created.body.settings.throttle.businessStart,8);assert.equal(created.body.settings.testSwitch,'Isolated');assert.equal(created.body.settings.disconnectSource,true,'disconnecting the old VM is ticked by default');assert(created.body.settings.startAt.startsWith('2026-12-0'));assert.equal(created.token,'workspace-test');
  const now=new Date().toISOString();
  f.plans.plans[0]=Object.assign(f.plans.plans[0],{summary:{total:3,waiting:0,copying:1,ready:1,testing:0,scheduled:0,done:0,failed:1},vms:[
   {id:'vm-app01',name:'APP01',wave:1,state:'ready',seeded:true,lastSync:now,lastMode:'changed',lastVia:'vddk',lastTracked:1073741824,lastRead:1073741824,lastWritten:268435456,lastSeconds:95,nextSync:now,failures:0},
   {id:'vm-sql01',name:'SQL01',wave:1,state:'copying',seeded:false,live:{percent:40,bytesRead:42949672960,bytesPerSecond:104857600,secondsLeft:600,elapsed:400},failures:0},
   {id:'vm-web01',name:'WEB01',wave:2,state:'failed',seeded:true,error:'<b>datastore unreachable</b>',failures:2,lastMode:'full',lastVia:'export',lastRead:96636764160,lastWritten:4294967296,lastSeconds:2040,lastWhy:'Hard disk 1: changed block tracking could not answer (FileFault); this usually means vSphere reset tracking (a storage move, a restore, or tracking turned off and on), so the disk is read in full once'}]});
  await f.page.waitForFunction(()=>document.querySelectorAll('#mpRows tr').length===3,null,{timeout:15000});
  assert((await f.page.locator('#mpRows').textContent()).includes('Seeding 40%'));assert((await f.page.locator('#mpRows .mp-progress-text').textContent()).includes('100 MB/s · 10 min left'));
  assert.equal(await f.page.locator('#mpRows .mp-err').textContent(),'<b>datastore unreachable</b>','errors are text, not markup');
  // Each row says how its last round read the disks, and why a full read happened.
  const reads=await f.page.locator('#mpRows .mp-read').allTextContents();
  assert(reads[0].startsWith('Changed blocks through VDDK')&&reads[0].includes('1.0 GB changed, 1.0 GB read, 256 MB written in 2 min'),reads[0]);
  assert.equal(await f.page.locator('#mpRows .mp-read.good').count(),1);assert.equal(await f.page.locator('#mpRows .mp-read.warning').count(),1);
  assert(reads[1].startsWith('Full read')&&!reads[1].includes('changed,'),reads[1]);
  assert((await f.page.locator('#mpRows .mp-why').textContent()).includes('reset tracking'),'a full read says why');
  assert((await f.page.locator('#mpAdvice').textContent()).startsWith('1 VM(s) failed'));
  await f.page.locator('#mpSummary [data-mp-filter="failed"]').click();await f.page.waitForFunction(()=>document.querySelectorAll('#mpRows tr').length===1);assert.equal(await f.page.locator('#mpSummary [data-mp-filter="failed"]').getAttribute('aria-pressed'),'true');
  await f.page.locator('#mpFilterBar [data-mp-filter]').click();await f.page.waitForFunction(()=>document.querySelectorAll('#mpRows tr').length===3);
  assert.equal(await f.page.locator('#mpCutWhy').textContent(),'Select the VMs to cut over in the list.');assert(await f.page.locator('#mpCutNow').isDisabled());
  await f.page.locator('[data-mp-row="vm-sql01"]').check();assert.equal(await f.page.locator('#mpCutWhy').textContent(),'SQL01 has not finished its first copy yet.');
  await f.page.locator('[data-mp-row="vm-sql01"]').uncheck();await f.page.locator('[data-mp-row="vm-app01"]').check();assert((await f.page.locator('#mpCutWhy').textContent()).startsWith('Type the plan name'));
  await f.page.locator('#mpConfirm').fill('wave 1');assert(!await f.page.locator('#mpCutNow').isDisabled());assert(await f.page.locator('#mpCutSchedule').isDisabled(),'schedule needs a time');
  await f.page.locator('#mpCutAt').fill('2026-12-05T23:30');await f.page.locator('#mpCutSchedule').click();
  await f.page.waitForFunction(()=>document.querySelector('#mpConfirm').value==='',null,{timeout:15000});
  const cut=f.planCalls.find(c=>c.body.action==='cutover');assert.deepEqual(cut.body.vmIds,['vm-app01']);assert.equal(cut.body.confirm,'wave 1');assert(cut.body.at.startsWith('2026-12-0'));
  await f.page.waitForSelector('[data-mp-test="vm-app01"]');await f.page.locator('[data-mp-test="vm-app01"]').click();
  for(let i=0;i<50&&!f.planCalls.some(c=>c.body.action==='test');i++){await new Promise(r=>setTimeout(r,100));}
  assert.deepEqual(f.planCalls.find(c=>c.body.action==='test').body.vmIds,['vm-app01']);
  for(const size of [{width:784,height:522},{width:1008,height:690}]){await f.page.setViewportSize(size);
   const small=await f.page.evaluate(()=>Array.from(document.querySelectorAll('#tabPlans *')).filter(e=>e.offsetParent&&e.childNodes.length&&Array.from(e.childNodes).some(n=>n.nodeType===3&&n.nodeValue.trim())&&parseFloat(getComputedStyle(e).fontSize)<10.5).map(e=>e.className+':'+e.textContent.slice(0,30)));assert.deepEqual(small,[],'plans text below 10.5px at '+size.width);
   assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'plans overflow at '+size.width);
   await f.page.locator('#mpCutNow').scrollIntoViewIfNeeded();assert(await f.page.locator('#mpCutNow').isVisible());}
  f.plans.plans[0].vms[2].error='Datastore DS-NL-02 could not be reached; the next round retries in 10 minutes.';await f.page.waitForFunction(()=>document.querySelector('#mpRows .mp-err').textContent.startsWith('Datastore'),null,{timeout:15000});
  assert.equal(await f.page.locator('[data-mp-eject]').count(),0,'no eject button for an unrelated failure');
  // A missing ISO stopped vSphere's export: one button turns Eject on for the plan and copies the VM again.
  f.plans.plans[0].settings=Object.assign({},f.plans.plans[0].settings,{ejectMedia:false});
  f.plans.plans[0].vms[2].error='Hard disk 1: the server answered 500 Internal Server Error; reading the snapshot through vSphere\'s export also failed: File [DS-NL-02] ISO Files/old.iso was not found. The VM\'s CD/DVD drive 1 ([DS-NL-02] ISO Files/old.iso) names an image file, and vSphere\'s export fails when that file no longer exists. Tick Eject CD/DVD and floppy images (New migration\'s Review, the migration plan\'s settings or Move VMs), then copy again.';
  await f.page.waitForSelector('[data-mp-eject="vm-web01"]',{timeout:15000});const before=f.planCalls.length;await f.page.locator('[data-mp-eject="vm-web01"]').click();
  for(let i=0;i<100&&f.planCalls.slice(before).filter(c=>c.body.action==='update'||c.body.action==='sync').length<2;i++){await new Promise(r=>setTimeout(r,50));}
  {const calls=f.planCalls.slice(before);const up=calls.find(c=>c.body.action==='update'),sy=calls.find(c=>c.body.action==='sync');assert(up&&up.body.settings.ejectMedia===true,'the plan turns Eject on');assert(sy&&sy.body.vmIds.join()==='vm-web01','and copies the VM again');}
  f.plans.plans[0].vms[2].error='Datastore DS-NL-02 could not be reached; the next round retries in 10 minutes.';
  if(!f.errors.length&&!f.dialogs.length){await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('.mp-body').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:out+'/migration-plans.png'});}
  assert.deepEqual(f.errors,[]);assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Help > Settings chooses the VDDK folder: Browse, a refused folder stays unsaved with the reason, a signed one is saved, and Turn off clears it',async()=>{const f=await fixture(browser);try{
  await f.page.locator('#helpMenuButton').click();await f.page.locator('#helpSettings').click();
  await f.page.waitForFunction(()=>/^Off\./.test(document.querySelector('#vddkStatus').textContent));
  assert(await f.page.locator('#vddkSettings button.path-pick-button').isVisible(),'the folder has a Browse button');
  assert(/license does not let this application include VDDK/.test(await f.page.locator('#vddkSettings').textContent()));
  await f.page.locator('#vddkFolder').fill('C:\\Bad');await f.page.locator('#vddkSave').click();
  await f.page.waitForFunction(()=>/signature is not valid/.test(document.querySelector('#vddkStatus').textContent));
  assert(await f.page.locator('#vddkStatus.error').isVisible());
  await f.page.locator('#vddkFolder').fill('C:\\VDDK\\vix-disklib-8.0.3');await f.page.locator('#vddkSave').click();
  await f.page.waitForFunction(()=>/^Saved\. VDDK 8\.0\.3/.test(document.querySelector('#vddkStatus').textContent));
  await f.page.locator('#vddkOff').click();await f.page.waitForFunction(()=>document.querySelector('#vddkFolder').value==='');
  assert.deepEqual(f.vddkCalls,[{folder:'C:\\Bad'},{folder:'C:\\VDDK\\vix-disklib-8.0.3'},{folder:''}]);
  await f.page.setViewportSize({width:1008,height:690});
  assert(await f.page.evaluate(()=>{const d=document.querySelector('#settingsDialog');const r=d.getBoundingClientRect();return r.bottom<=innerHeight&&d.scrollHeight<=d.clientHeight+2||getComputedStyle(d).overflowY!=='visible';}),'Settings scrolls instead of clipping at 1024x768');
  await f.page.locator('#settingsClose').click();assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Help > Settings protects production: free space kept, the stop level, copy caps and this computer\'s bandwidth; a copy that waits says why in a banner and in its plan row',async()=>{const f=await fixture(browser);try{
  const defaults={schema:1,reservePercent:10,reserveGB:50,stopPercent:5,stopGB:20,growthPercent:10,growthMinGB:4,maxCopies:4,maxPerDatastore:2,maxPerSourceHost:2,maxPerTarget:2,bandwidth:{limitMbps:0,businessMbps:0,businessStart:0,businessEnd:0}};
  f.state.production={settings:defaults,defaults:defaults,holds:[],active:[],running:0,limit:'no limit'};
  await f.page.locator('#helpMenuButton').click();await f.page.locator('#helpSettings').click();
  await f.page.waitForFunction(()=>document.querySelector('#pgReserveGB').value==='50');
  assert.equal(await f.page.locator('#pgMaxCopies').inputValue(),'4');assert.equal(await f.page.locator('#pgPerDs').inputValue(),'2');
  assert(/Nothing is waiting/.test(await f.page.locator('#pgHolds').textContent()));
  // A stop level above the reserve is refused inline and nothing changes.
  await f.page.locator('#pgStopPct').fill('20');await f.page.locator('#pgSave').click();
  await f.page.waitForFunction(()=>/must not be above/.test(document.querySelector('#pgStatus').textContent));assert(await f.page.locator('#pgStatus.error').isVisible());
  await f.page.locator('#pgDefaultsButton').click();assert.equal(await f.page.locator('#pgStopPct').inputValue(),'5');
  await f.page.locator('#pgReserveGB').fill('100');await f.page.locator('#pgPerHost').selectOption('1');await f.page.locator('#pgBizMbps').fill('200');await f.page.locator('#pgOffMbps').fill('800');await f.page.locator('#pgBizStart').selectOption('8');await f.page.locator('#pgBizEnd').selectOption('18');
  await f.page.locator('#pgSave').click();await f.page.waitForFunction(()=>/^Saved\./.test(document.querySelector('#pgStatus').textContent));
  const saved=f.productionCalls[f.productionCalls.length-1];
  assert.equal(saved.reserveGB,100);assert.equal(saved.maxPerSourceHost,1);assert.deepEqual(saved.bandwidth,{businessMbps:200,limitMbps:800,businessStart:8,businessEnd:18});
  await f.page.locator('#pgOffMbps').fill('fast');await f.page.locator('#pgSave').click();assert(/whole numbers/.test(await f.page.locator('#pgStatus').textContent()));
  await f.page.locator('#settingsClose').click();
  // A copy waits for space: the banner over the tools says why and opens the settings.
  f.state.production=Object.assign({},f.state.production,{holds:[{owner:'SQL01',kind:'space',reason:'Waiting for space on datastore DS-SQL: 80 GB of 1 TB is free, and 102 GB is kept free for production (Help > Settings). It tries again when space allows.',since:new Date().toISOString()}]});
  await goTool(f,'plans');await f.page.waitForFunction(()=>!document.querySelector('#pgBanner').classList.contains('hidden'),null,{timeout:15000});
  const banner=await f.page.locator('#pgBannerText').textContent();assert(/1 copy waits for free space/.test(banner)&&/DS-SQL/.test(banner),banner);
  await f.page.locator('#pgBannerOpen').click();assert(await f.page.locator('#settingsDialog').isVisible());
  await f.page.waitForFunction(()=>/Waiting for space: SQL01/.test(document.querySelector('#pgHolds').textContent));
  await f.page.locator('#settingsClose').click();
  // A plan VM held for space shows the reason, not an error.
  f.plans.plans=[{id:'p1',name:'Wave 1',server:'vc01.example.com',created:new Date().toISOString(),paused:false,settings:{syncMinutes:60,concurrency:2,throttle:{},waves:[{wave:1,concurrency:1,throttle:{limitMbps:100,businessMbps:0,businessStart:8,businessEnd:18}}]},throttle:'no limit',summary:{total:1,waiting:0,copying:0,ready:1,testing:0,scheduled:0,done:0,failed:0},vms:[{id:'vm-sql01',name:'SQL01',wave:1,state:'ready',seeded:true,failures:0,held:'Waiting for space on datastore DS-SQL.'}]}];
  await f.page.waitForFunction(()=>/Waiting for space on datastore DS-SQL/.test((document.querySelector('#mpRows')||{}).textContent||''),null,{timeout:15000});
  assert(!/mp-err/.test(await f.page.locator('#mpRows').innerHTML()),'a held VM is not shown as an error');
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Help > Settings opts in to the Test update channel: Stable by default, ticking offers the test build, unticking goes back to Stable',async()=>{const f=await fixture(browser);try{
  await f.page.locator('#helpMenuButton').click();await f.page.locator('#helpSettings').click();
  assert(!(await f.page.locator('#updateTestChannel').isChecked()),'Stable is the default');
  assert(/stable release is offered in its place/.test(await f.page.locator('#updateChannelSettings').textContent()),'the setting explains returning to stable');
  await f.page.locator('#updateTestChannel').check();
  await f.page.waitForFunction(()=>/^Saved\. Test channel/.test(document.querySelector('#updateChannelStatus').textContent));
  assert(/3\.3\.0-test\.2/.test(await f.page.locator('#updateChannelStatus').textContent()));
  assert(await f.page.locator('#updateBanner').isVisible(),'the update bar offers the test build');
  assert(/Test build available/.test(await f.page.locator('#updateTitle').textContent()));
  await f.page.locator('#updateTestChannel').uncheck();
  await f.page.waitForFunction(()=>/^Saved\. Stable channel/.test(document.querySelector('#updateChannelStatus').textContent));
  assert(!(await f.page.locator('#updateBanner').isVisible()),'Stable again hides the test build');
  assert.deepEqual(f.channelCalls,[{channel:'test'},{channel:'stable'}]);
  await f.page.locator('#settingsClose').click();
  await f.page.locator('#helpMenuButton').click();await f.page.locator('#helpAbout').click();
  assert(/Update channel: Stable/.test(await f.page.locator('#aboutChannel').textContent()));
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Confirmations follow Help > Settings: a tick box by default, a dialog, the typed name, or none, and every request still carries the name it confirms',async()=>{const f=await fixture(browser);try{
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',summary:vmwareSummaryFixture(),inventorySerial:1});
  // Settings: saved through /api/ui-settings, the radio buttons follow the state.
  await f.page.locator('#helpMenuButton').click();await f.page.locator('#helpSettings').click();assert(await f.page.locator('#settingsDialog').isVisible());
  assert(await f.page.locator('#cfmStyleTyped').isChecked(),'the saved style is shown');
  await f.page.locator('#cfmStyleTick').check();await f.page.waitForFunction(()=>/Saved\. Confirmations now: Tick a box/.test(document.querySelector('#settingsStatus').textContent));
  assert.deepEqual(f.uiCalls,[{confirmStyle:'tick'}]);assert.equal(f.state.ui.confirmStyle,'tick');
  await f.page.screenshot({path:out+'/confirm-settings-1440x1000.png'});await f.page.locator('#settingsClose').click();
  // A plan with one VM ready to cut over.
  await goTool(f,'plans');await f.page.waitForSelector('#mpNewButton');await f.page.locator('#mpNewButton').click();await f.page.waitForFunction(()=>document.querySelectorAll('#mpPicker [data-mp-pick]:not([disabled])').length>0,null,{timeout:15000});
  const id=await f.page.locator('#mpPicker [data-mp-pick]:not([disabled])').first().getAttribute('data-mp-pick');await f.page.locator('#mpPicker [data-mp-pick="'+id+'"]').check();
  await f.page.locator('#mpName').fill('Wave 1');await f.page.locator('#mpCreate').click();await f.page.waitForFunction(()=>!document.querySelector('#mpDetail').classList.contains('hidden'),null,{timeout:15000});
  f.plans.plans[0]=Object.assign(f.plans.plans[0],{summary:{total:2,waiting:0,copying:0,ready:2,testing:0,scheduled:0,done:0,failed:0},vms:[{id:'vm-app01',name:'APP01',wave:1,state:'ready',seeded:true,failures:0},{id:'vm-web01',name:'WEB01',wave:1,state:'ready',seeded:true,failures:0}]});
  await f.page.waitForFunction(()=>document.querySelectorAll('#mpRows tr').length===2,null,{timeout:15000});
  // Tick box: no name to type, the box says what happens and is cleared when the selection changes.
  assert(!await f.page.locator('#mpConfirm').isVisible(),'no plan name to type');assert(await f.page.locator('#mpConfirmTick').isVisible());
  assert((await f.page.locator('label:has(#mpConfirmTick)').textContent()).includes('shut down and cut over'));
  for(const size of [{width:1008,height:690},{width:1350,height:690},{width:1904,height:1000}]){await f.page.setViewportSize(size);await f.page.waitForTimeout(100);await f.page.locator('#mpCutNow').scrollIntoViewIfNeeded();await f.page.screenshot({path:out+'/plans-cutover-'+size.width+'.png'});const apart=await apartControls(f.page);assert.deepEqual(apart,[],'Migration plans cutover at '+size.width+': '+apart.join('; '));}
  await f.page.setViewportSize({width:1440,height:1000});
  await f.page.locator('[data-mp-row="vm-app01"]').check();assert.equal(await f.page.locator('#mpCutWhy').textContent(),'Tick "I understand" to cut over the selected VMs.');assert(await f.page.locator('#mpCutNow').isDisabled());
  await f.page.locator('#mpConfirmTick').check();assert(!await f.page.locator('#mpCutNow').isDisabled());
  await f.page.locator('#mpCutNow').scrollIntoViewIfNeeded();await f.page.screenshot({path:out+'/confirm-tick-1440x1000.png'});
  await f.page.locator('[data-mp-row="vm-web01"]').check();assert(!await f.page.locator('#mpConfirmTick').isChecked(),'a tick given for other VMs is cleared');assert(await f.page.locator('#mpCutNow').isDisabled());
  await f.page.locator('[data-mp-row="vm-web01"]').uncheck();await f.page.locator('#mpConfirmTick').check();await f.page.locator('#mpCutNow').click();
  for(let i=0;i<100&&!f.planCalls.some(c=>c.body.action==='cutover');i++){await new Promise(r=>setTimeout(r,50));}
  {const cut=f.planCalls.find(c=>c.body.action==='cutover');assert.deepEqual(cut.body.vmIds,['vm-app01']);assert.equal(cut.body.confirm,'Wave 1','the request still names the plan');}
  await f.page.waitForFunction(()=>!document.querySelector('#mpConfirmTick').checked,null,{timeout:15000});
  // Delete opens its dialog with a tick box instead of a name.
  await f.page.locator('#mpDelete').click();assert(await f.page.locator('#migrateDialog').isVisible());assert(!await f.page.locator('#migrateConfirmInput').isVisible());
  assert.equal(await f.page.locator('#migrateConfirmTickLabel').textContent(),'I understand the plan Wave 1 is deleted and its schedules stop');assert(await f.page.locator('#migrateConfirmButton').isDisabled());
  await f.page.locator('#migrateConfirmTick').check();assert(!await f.page.locator('#migrateConfirmButton').isDisabled());await f.page.locator('#migrateDialogCancel').click();
  assert(!f.planCalls.some(c=>c.body.action==='delete'),'Cancel changes nothing');
  // Dialog: the button is on at once and asks in a dialog first; Cancel sends nothing.
  f.state.ui={schema:1,confirmStyle:'prompt'};await f.page.waitForFunction(()=>!document.querySelector('#mpConfirmTick').offsetParent,null,{timeout:15000});
  await f.page.locator('[data-mp-row="vm-app01"]').check();await f.page.waitForFunction(()=>!document.querySelector('#mpCutNow').disabled,null,{timeout:15000});
  await f.page.locator('#mpCutNow').click();assert(await f.page.locator('#cfmDialog').isVisible());assert.equal(await f.page.locator('#cfmDialogTitle').textContent(),'Cut over 1 VM');assert(/shut down now/.test(await f.page.locator('#cfmDialogText').textContent()));
  await f.page.locator('#cfmDialogCancel').click();await new Promise(r=>setTimeout(r,300));assert.equal(f.planCalls.filter(c=>c.body.action==='cutover').length,1,'Cancel sends nothing');
  await f.page.locator('#mpCutNow').click();await f.page.locator('#cfmDialogGo').click();
  for(let i=0;i<100&&f.planCalls.filter(c=>c.body.action==='cutover').length<2;i++){await new Promise(r=>setTimeout(r,50));}
  assert.equal(f.planCalls.filter(c=>c.body.action==='cutover').pop().body.confirm,'Wave 1');
  await f.page.locator('#mpDelete').click();assert(!await f.page.locator('#migrateConfirmTickRow').isVisible());assert(!await f.page.locator('#migrateConfirmButton').isDisabled(),'the dialog itself is the confirmation');await f.page.locator('#migrateDialogCancel').click();
  // Typed: the old behaviour.
  f.state.ui={schema:1,confirmStyle:'typed'};await f.page.waitForFunction(()=>!!document.querySelector('#mpConfirm').offsetParent,null,{timeout:15000});
  await f.page.locator('[data-mp-row="vm-app01"]').check();assert((await f.page.locator('#mpCutWhy').textContent()).startsWith('Type the plan name'));
  // Don't ask: Delete runs at once with the plan name.
  f.state.ui={schema:1,confirmStyle:'none'};await f.page.waitForFunction(()=>!document.querySelector('#mpConfirm').offsetParent&&!document.querySelector('#mpConfirmTick').offsetParent,null,{timeout:15000});
  await f.page.locator('#mpDelete').click();
  for(let i=0;i<100&&!f.planCalls.some(c=>c.body.action==='delete');i++){await new Promise(r=>setTimeout(r,50));}
  assert(!await f.page.locator('#migrateDialog').isVisible());assert.equal(f.planCalls.find(c=>c.body.action==='delete').body.confirm,'Wave 1');
  for(const size of [{width:1008,height:690}]){await f.page.setViewportSize(size);await f.page.locator('#helpMenuButton').click();await f.page.locator('#helpSettings').click();
   assert(await f.page.evaluate(()=>{const d=document.querySelector('#settingsDialog').getBoundingClientRect();return d.bottom<=innerHeight&&d.right<=innerWidth;}),'Settings fits at 1024x768');await f.page.locator('#settingsClose').click();}
  assert.deepEqual(f.errors,[]);assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Move SQL databases: both servers are read, each database shows its suggested way with the reason, a changed way is marked Set by you, Start sends the plan confirmed, and a plan lists every database with its track and actions',async()=>{const f=await fixture(browser);try{
  await goTool(f,'sqlmove');await f.page.waitForSelector('#sqDbRows [data-sq-db="Sales"]');
  assert.equal(await f.page.locator('[data-sq-method="Sales"]').inputValue(),'logship');
  assert.equal(await f.page.locator('[data-sq-method="Archive"]').inputValue(),'backup');
  assert.equal(await f.page.locator('[data-sq-method="Orders"]').inputValue(),'dag');
  assert(await f.page.locator('[data-sq-method="Archive"] option[value="logship"]').isDisabled(),'log shipping is off for a simple recovery database');
  assert.equal(await f.page.locator('#sqDbRows [data-sq-db="master"]').count(),0,'system databases cannot be ticked');
  assert.match(await f.page.locator('#sqOffTitle').textContent(),/1 database cannot move/);
  assert.equal(await f.page.locator('#sqBackup').inputValue(),'\\\\OLDSQL\\E$\\Backup\\SIMM');
  assert(await f.page.locator('#sqStart').isDisabled(),'Start waits for databases');
  await f.page.locator('[data-sq-pick="Sales"]').check();await f.page.locator('[data-sq-pick="Orders"]').check();
  await f.page.locator('[data-sq-method="Orders"]').selectOption('logship');
  assert.match(await f.page.locator('#sqDbRows [data-sq-db="Orders"] .sq-tag').textContent(),/Set by you/);
  assert.match(await f.page.locator('#sqDbRows [data-sq-db="Sales"] .sq-tag').textContent(),/Suggested/);
  assert.match(await f.page.locator('#sqPickCount').textContent(),/2 databases chosen/);
  await f.page.locator('#sqCheck').click();await f.page.waitForSelector('.sq-finding');
  assert.match(await f.page.locator('#sqFindings').textContent(),/Compatibility level 110/);
  await f.page.locator('#sqName').fill('Sales to NEWSQL');
  await f.page.locator('#sqConfirm').fill('Sales to NEWSQL');
  await f.page.locator('#sqStart').click();
  await f.page.waitForFunction(()=>document.querySelectorAll('[data-sq-plan]').length===2);
  const create=f.sqlCalls.find(c=>c.action==='create');
  assert.equal(create.body.confirm,'Sales to NEWSQL');
  assert.deepEqual(create.body.databases.map(d=>d.name+':'+d.method),['Sales:logship','Orders:logship']);
  assert.equal(create.body.objects.logins,true);assert.equal(create.body.objects.disableSourceJobs,true);
  assert.equal(create.body.backupPath,'\\\\OLDSQL\\E$\\Backup\\SIMM');
  for(const c of f.sqlCalls){assert(!JSON.stringify(c.body).includes('"password"'),'no password field is sent');}
  // The first plan: tracks, a long error folded, and cutover confirmed by the tick.
  const plan=f.page.locator('[data-sq-plan="q1"]');
  assert.equal(await plan.locator('.vm-track').count(),2);
  assert.match(await plan.textContent(),/In sync/);
  assert(await plan.locator('[data-sq-act="cutover"]').isDisabled(),'Cut over waits for ticked databases');
  await plan.locator('[data-sq-row="q1"][data-sq-dbname="Payroll"]').check();
  await plan.locator('#sqCut_q1').fill('1');
  await plan.locator('[data-sq-act="cutover"]').click();
  await f.page.waitForFunction(()=>/Cutting over/.test(document.querySelector('[data-sq-plan="q1"]').textContent));
  const cut=f.sqlCalls.find(c=>c.action==='action'&&c.body.action==='cutover');
  assert.deepEqual(cut.body.names,['Payroll']);assert.equal(cut.body.confirm,'1');
  assert.deepEqual(f.errors,[]);
 }finally{await f.close();}});
 await check('Roll back: every cutover is listed with its window, the dialog turns the new VM off first, warns about lost data, asks for a vCenter sign-in when needed, a domain controller needs its own tick, and every other way of moving has its steps',async()=>{const f=await fixture(browser);try{
  const planned=[{key:'stop',title:'Shut the new VM down'},{key:'check',title:'Check the new VM is off'},{key:'network',title:'Put the adapters back'},{key:'start',title:'Start the old VM'}];
  const base={method:'move',cutoverAt:'2026-11-27T08:00:00Z',state:'ready',planned,window:'Roll back works as long as the old VM stays in vcenter.example.com with its disks.',dataWarning:'Anything written on the new VM since the cutover 2026-11-27 08:00 UTC (3 hours ago) is not on the old one.'};
  f.state.vmware.rollback={running:'',serial:1,records:[
   Object.assign({},base,{id:'r1',name:'APP01',source:{kind:'vmware',server:'vcenter.example.com',vmId:'vm-42',name:'APP01',adapterRecord:'C:\\rec\\APP01-adapters.json'},new:{kind:'hyperv',id:'6f1c7a3e-1b2c-4d5e-8f90-0a1b2c3d4e5f',name:'APP01'}}),
   Object.assign({},base,{id:'r2',name:'DC01',dc:true,dcWarning:'This is a domain controller. Starting the old copy can cause USN rollback.',source:{kind:'vmware',server:'vcenter.example.com',vmId:'vm-7',name:'DC01'},new:{kind:'hyperv',id:'7f1c7a3e-1b2c-4d5e-8f90-0a1b2c3d4e5f',name:'DC01',host:'HV02'}}),
   Object.assign({},base,{id:'r3',name:'FILE01',method:'copies',state:'done',finished:'2026-11-27T09:00:00Z',message:'FILE01 is rolled back.',steps:[{key:'stop',title:'Shut the new VM down',state:'ok'},{key:'agent',title:'Ask the agent',state:'ok'}],logPath:'C:\\x\\r3.log',source:{kind:'agent',name:'FILE01',job:'j1'},new:{kind:'hyperv',id:'8f1c7a3e-1b2c-4d5e-8f90-0a1b2c3d4e5f',name:'FILE01'}})]};
  await goTool(f,'rollback');
  await f.page.waitForFunction(()=>document.querySelectorAll('#rbList .rb-item').length===3);
  assert.equal(await f.page.locator('#rollbackBadge').textContent(),'2');
  assert((await f.page.locator('[data-rb-item="r1"]').textContent()).includes('stays in vcenter.example.com'));
  assert.equal(await f.page.locator('[data-rb-item="r3"] [data-rb-open]').count(),0,'a rolled back VM has no Roll back button');
  await f.page.locator('[data-rb-item="r3"] [data-rb-log]').click();await f.page.waitForFunction(()=>true);
  await f.page.waitForTimeout(300);assert.deepEqual(f.opens.slice(-1),['rollbacklog:r3']);
  // Every other way of moving has its steps.
  await f.page.waitForFunction(()=>document.querySelectorAll('#rbMethod option').length===2);
  await f.page.selectOption('#rbMethod','shares');assert((await f.page.locator('#rbGuide').textContent()).includes('Undo read-only'));
  // The dialog: not signed in to vCenter, so the sign-in shows and Roll back waits.
  await f.page.locator('[data-rb-item="r1"] [data-rb-open]').click();
  await f.page.waitForFunction(()=>document.querySelector('#rbDialog').open);
  assert((await f.page.locator('#rbDialogData').textContent()).includes('is not on the old one'));
  const steps=await f.page.locator('#rbDialogSteps li').allTextContents();assert(steps[0].startsWith('Shut the new VM APP01 down')&&steps[1].startsWith('Read the new VM again'),steps.join('|'));
  assert(await f.page.locator('#rbDialogDC').isHidden());
  assert(await f.page.locator('#rbSignIn').isVisible());assert(await f.page.locator('#rbDialogGo').isDisabled());
  Object.assign(f.state.vmware,{server:'vcenter.example.com',account:'EXAMPLE\\operator',canPrepare:true});
  await f.page.waitForFunction(()=>document.querySelector('#rbSignIn').classList.contains('hidden'),null,{timeout:10000});
  await f.page.locator('#rbConfirmInput').fill('APP0');assert(await f.page.locator('#rbDialogGo').isDisabled());
  await f.page.locator('#rbConfirmInput').fill('APP01');await f.page.locator('#rbTurnOff').uncheck();
  assert(!(await f.page.locator('#rbDialogSteps li').first().textContent()).includes('turning it off'));
  await f.page.locator('#rbDialogGo').click();
  await f.page.waitForFunction(()=>!document.querySelector('#rbDialog').open);
  assert.deepEqual(f.rollbackCalls.filter(c=>c.action==='run').map(c=>c.body),[{turnOff:false,reconnect:true,start:true,dcConfirmed:false,id:'r1',confirm:'APP01'}]);
  await f.page.waitForFunction(()=>(document.querySelector('[data-rb-item="r1"]')||{}).className==='rb-item running',null,{timeout:10000});
  // A domain controller needs its own tick, whatever the confirmation style.
  await f.page.locator('[data-rb-item="r2"] [data-rb-open]').click();await f.page.waitForFunction(()=>document.querySelector('#rbDialog').open);
  assert(await f.page.locator('#rbDialogDC').isVisible());assert((await f.page.locator('#rbDialogDCText').textContent()).includes('USN rollback'));
  await f.page.locator('#rbConfirmInput').fill('DC01');assert(await f.page.locator('#rbDialogGo').isDisabled(),'the domain controller tick is required');
  await f.page.locator('#rbDCTick').check();assert(await f.page.locator('#rbDialogGo').isEnabled());
  assert((await f.page.locator('#rbDialogSteps li').first().textContent()).includes('on HV02'));
  await f.page.locator('#rbDialogCancel').click();assert(!(await f.page.evaluate(()=>document.querySelector('#rbDialog').open)));
  assert.equal(f.rollbackCalls.filter(c=>c.action==='run').length,1);
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('All jobs and servers: one list of every method with clickable tiles, live progress, Select by, bulk cutover in waves with the typed count, jobs to this Hyper-V host without a receiver to set up, agent installs, and task rows that open their tool',async()=>{const f=await fixture(browser);try{
  const now=new Date(),iso=d=>new Date(d).toISOString(),ago=m=>iso(now.getTime()-m*60000);
  const row=(o)=>Object.assign({percent:-1,lagSeconds:-1,wave:0,actions:[],detail:'',reason:''},o);
  f.servers.rows=[
   row({key:'plan:p1|vm-10',kind:'plan',method:'Migration plan',name:'DC01',detail:'Plan Wave 1',plan:'p1',vm:'vm-10',state:'insync',stateText:'In sync, ready to cut over',reason:'Seeded and kept in sync. Test it, then cut over.',lastSync:ago(20),lagSeconds:1200,limitMinutes:60,wave:1,actions:['sync','pause','test','cutover','wave']}),
   row({key:'plan:p1|vm-11',kind:'plan',method:'Migration plan',name:'PRINT01',detail:'Plan Wave 1',plan:'p1',vm:'vm-11',state:'seeding',stateText:'Initial copy',reason:'The first full copy has not finished yet.',percent:40,bytesPerSecond:104857600,secondsLeft:600,wave:2,actions:['sync','pause','wave']}),
   row({key:'job:a1|j1',kind:'job',method:'Agent replication',name:'APP01',detail:'Job APP01 on app01.example.com:44390',agent:'a1',job:'j1',host:'app01.example.com',state:'insync',stateText:'In sync, ready to cut over',reason:'A whole copy exists and stays current.',lastSync:ago(2),lagSeconds:120,limitMinutes:15,wave:2,actions:['sync','pause','test','cutover','wave']}),
   row({key:'job:a2|j2',kind:'job',method:'Agent replication',name:'SQL01',detail:'Job SQL01 on 192.0.2.20:44390',agent:'a2',job:'j2',state:'failed',stateText:'Failed',reason:'The last round failed: the receiver went away & stopped answering.',actions:['sync','pause','wave']}),
   row({key:'agent:a4',kind:'agent',method:'Agent replication',name:'WEB01',detail:'web01:44390',agent:'a4',host:'web01',state:'agent',stateText:'Agent installed, no job',reason:'The agent answers. Create a replication job to start copying.',actions:['createjob']}),
   row({key:'vm:vm-31',kind:'vm',method:'Not configured',name:'NEW01',detail:'vc01.example.com',vm:'vm-31',host:'new01.example.com',state:'notconfigured',stateText:'Not configured',reason:'Nothing moves this VM yet.',actions:['install']}),
   row({key:'task:convert',kind:'task',method:'Disk conversion',name:'APP02.vmdk',detail:'To D:\\VMs\\APP02.vhdx',state:'running',stateText:'Running',percent:25,bytesRead:2147483648,actions:[]})];
  f.servers.summary={total:7,states:{insync:2,seeding:1,failed:1,agent:1,notconfigured:1,running:1},scheduled:0,methods:{}};
  await goTool(f,'servers');await f.page.waitForFunction(()=>document.querySelectorAll('#asRows tr').length===7,null,{timeout:15000});
  assert((await f.page.locator('[data-tool="servers"] .tool-works').textContent()).includes('Migration plans'));
  assert.equal(await f.page.locator('#asSummary [data-as-filter="failed"]').evaluate(e=>e.tagName),'BUTTON');
  assert.equal(await f.page.locator('#asSummary [data-as-filter="behind"]').count(),0,'empty tiles are left out');
  await f.page.locator('#asSummary [data-as-filter="failed"]').click();await f.page.waitForFunction(()=>document.querySelectorAll('#asRows tr').length===1);
  assert.equal(await f.page.locator('#asRows .as-reason').textContent(),'The last round failed: the receiver went away & stopped answering.','reasons are text');
  await f.page.locator('#asFilterBar [data-as-showall]').click();await f.page.waitForFunction(()=>document.querySelectorAll('#asRows tr').length===7);
  assert((await f.page.locator('#asRows tr[data-as-key="plan:p1|vm-11"] .as-progress-text').textContent()).includes('100 MB/s'));
  assert.equal(await f.page.locator('#asRows tr[data-as-key="plan:p1|vm-11"] [role="progressbar"]').getAttribute('aria-valuenow'),'40');
  assert(await f.page.locator('[data-as-act="cutover"]').isDisabled(),'nothing selected');
  assert.equal(await f.page.locator('#asWhy').textContent(),'','no hint to tick servers');assert(await f.page.locator('#asWhy').isVisible(),'the reason line keeps its space');
  await f.page.locator('#asSelectBy').selectOption('insync');await f.page.waitForFunction(()=>document.querySelector('#asSelCount').textContent==='2 servers selected');
  assert.equal(await f.page.locator('[data-as-act="cutover"]').textContent(),'Cut over (2)');
  await f.page.evaluate(()=>document.activeElement.blur());if(!f.errors.length){await f.page.screenshot({path:out+'/all-jobs-and-servers.png'});}assert.equal(await f.page.locator('[data-as-act="createjob"]').textContent(),'Create replication jobs (0)');
  await f.page.locator('[data-as-act="cutover"]').click();await f.page.waitForSelector('#asFormCutover:not(.hidden)');
  assert.deepEqual(await f.page.locator('#asCutPreview li').allTextContents(),['Wave 1: 1 server now',(await f.page.locator('#asCutPreview li').nth(1).textContent())]);
  assert((await f.page.locator('#asCutPreview li').nth(1).textContent()).startsWith('Wave 2: 1 server at '));
  assert(await f.page.locator('#asCutRun').isDisabled(),'the count must be typed');await f.page.locator('#asCutConfirm').fill('2');assert(!await f.page.locator('#asCutRun').isDisabled());
  await f.page.locator('#asCutRun').click();await f.page.waitForFunction(()=>document.querySelector('#asResultsTitle').textContent.includes('2 of 2 done'),null,{timeout:15000});
  const cut=f.serverCalls.find(c=>c.body.action==='cutover');assert.deepEqual(cut.body.keys.sort(),['job:a1|j1','plan:p1|vm-10']);assert.equal(cut.body.staggerMinutes,60);assert.equal(cut.body.confirm,'2');assert(!cut.body.at,'now has no time');
  // Create jobs: this Hyper-V host is the default target; no receiver details to type.
  await f.page.locator('#asSelectBy').selectOption('none');await f.page.locator('[data-as-row="agent:a4"]').check();
  await f.page.locator('[data-as-act="createjob"]').click();await f.page.waitForSelector('#asFormJobs:not(.hidden)');
  assert(await f.page.locator('#asJobTargetLocal').isChecked());assert(await f.page.locator('#asJobRemoteBox').isHidden());
  assert((await f.page.locator('#asJobLocalText').textContent()).startsWith('This Hyper-V host (HV01)'));
  assert.equal(await f.page.locator('#asJobFolder').inputValue(),'C:\\ClusterStorage\\Volume1\\Hyper-V\\Received');
  await f.page.locator('#asJobBrowse').click();await f.page.waitForFunction(()=>document.querySelector('#asJobFolder').value.includes('Volume2'));
  await f.page.locator('#asJobRun').click();await f.page.waitForFunction(()=>document.querySelector('#asFormJobs').classList.contains('hidden'),null,{timeout:15000});
  const made=f.serverCalls.find(c=>c.body.action==='createjob');assert.deepEqual(made.body.keys,['agent:a4']);assert.equal(made.body.template.local,true);assert.equal(made.body.template.folder,'C:\\ClusterStorage\\Volume2\\Replicas');assert.equal(made.body.template.receiver.code,'');
  // Install agents on a VM nothing moves yet: the guest name is filled in.
  await f.page.locator('[data-as-row="agent:a4"]').uncheck();await f.page.locator('[data-as-row="vm:vm-31"]').check();
  await f.page.locator('[data-as-act="install"]').click();await f.page.waitForSelector('#asFormInstall:not(.hidden)');
  assert.equal(await f.page.locator('#asInstHosts').inputValue(),'new01.example.com');assert(await f.page.locator('#asInstallSignInCurrent').isChecked()&&await f.page.locator('#asInstPassword').isHidden(),'installs start on the current sign-in');
  await f.page.locator('#asInstRun').click();await f.page.waitForFunction(()=>document.querySelector('#asFormInstall').classList.contains('hidden')&&/Installing the agent/.test(document.querySelector('#asStatus').textContent),null,{timeout:15000});
  const push=f.replCalls.find(c=>c.action==='push');assert.equal(push.body.hosts,'new01.example.com');assert.equal(push.body.user,'');assert.equal(push.body.password,'','the current sign-in sends no account');assert.equal(push.body.platform,'windows');
  for(const size of [{width:784,height:522},{width:1008,height:690}]){await f.page.setViewportSize(size);
   const small=await f.page.evaluate(()=>Array.from(document.querySelectorAll('#tabServers *')).filter(e=>e.offsetParent&&e.childNodes.length&&Array.from(e.childNodes).some(n=>n.nodeType===3&&n.nodeValue.trim())&&parseFloat(getComputedStyle(e).fontSize)<10.5).map(e=>e.className+':'+e.textContent.slice(0,30)));assert.deepEqual(small,[],'text below 10.5px at '+size.width);
   assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'overflow at '+size.width);
   await f.page.locator('[data-as-act="cutover"]').scrollIntoViewIfNeeded();assert(await f.page.locator('[data-as-act="cutover"]').isVisible());}
  await f.page.setViewportSize({width:1440,height:1000});
  // A task row opens its own tool.
  await f.page.locator('[data-as-open="task:convert"]').click();await f.page.waitForFunction(()=>document.querySelector('[data-tool="convert"]').getAttribute('aria-pressed')==='true');
  assert(f.serverCalls.every(c=>c.token==='workspace-test'));assert.deepEqual(f.errors,[]);assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Replication jobs can go to this Hyper-V host: the receiver here is set up for you and only the destination folder is asked',async()=>{const f=await fixture(browser);try{
  f.repl.local={hyperV:true,computer:'HV01',running:true,folder:'C:\\ClusterStorage\\Volume1\\Replicas',defaultFolder:'C:\\Hyper-V\\Received',port:44380,addresses:['192.0.2.40']};
  f.repl.agents=[{id:'ag1',name:'APP01',address:'app01.example.com:44390',live:{reachable:true,platform:'Windows',jobs:[],alerts:[]}}];f.repl.summary={agents:1,unreachable:0,jobs:0,healthy:0,behind:0,failing:0,paused:0,cutOver:0,alerts:0};
  await goTool(f,'replicate');await f.page.waitForSelector('[data-rp-new="ag1"]',{timeout:15000});await f.page.locator('[data-rp-new="ag1"]').click();
  await f.page.waitForFunction(()=>document.querySelectorAll('[data-rp-disk]').length===2);
  assert(await f.page.locator('#rpTargetLocal').isChecked(),'this Hyper-V host is the default');assert(await f.page.locator('#rpRemoteBox').isHidden());
  assert.equal(await f.page.locator('#rpLocalFolder').inputValue(),'C:\\ClusterStorage\\Volume1\\Replicas');assert(await f.page.locator('#rpLocalBrowse').isDisabled(),'a running receiver keeps its folder');
  assert.equal(await f.page.locator('[data-rp-target="0"]').inputValue(),'APP01-disk0.vhdx');
  await f.page.locator('#rpJobSave').click();await f.page.waitForFunction(()=>document.querySelector('#rpEditor').classList.contains('hidden'),null,{timeout:15000});
  const saved=f.replCalls.find(c=>c.action==='job'&&c.body.action==='save');assert.equal(saved.body.local,true);assert.equal(saved.body.folder,'C:\\ClusterStorage\\Volume1\\Replicas');assert.equal(saved.body.spec.receiver.code,'');assert.equal(saved.body.spec.disks[0].target,'APP01-disk0.vhdx');
  await f.page.locator('[data-rp-new="ag1"]').click();await f.page.locator('#rpTargetRemote').check();assert(await f.page.locator('#rpRecvAddress').isVisible());assert(await f.page.locator('#rpLocalBox').isHidden());
  assert.deepEqual(f.errors,[]);assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Replication summary tiles are buttons: each filters the servers and jobs it counts, shows the reason and next step, and the same tile or Show all clears it',async()=>{const f=await fixture(browser);try{
  const job=(id,name,state,rp,rpo,extra)=>({spec:{id,name,rpoMinutes:15,disks:[{source:'0',target:name+'-disk0.vhdx'}]},rpoSeconds:rpo,status:Object.assign({state,recoveryPoint:rp,rounds:3,disks:[]},extra||{})});
  Object.assign(f.repl,{agents:[
   {id:'ag1',name:'APP01',address:'app01.example.com:44390',live:{reachable:true,platform:'Windows',jobs:[job('j1','APP01','syncing','2026-11-25T14:00:00Z',120,{disks:[{source:'0',target:'APP01-disk0.vhdx',size:107374182400,chunks:25600,done:6400,read:26843545600,sent:4294967296}]}),job('j4','PRINT01','syncing','',0,{rounds:0,disks:[{source:'0',target:'PRINT01-disk0.vhdx',size:53687091200,chunks:12800,done:3200,read:13421772800,sent:6442450944}]}),job('j2','FILE01','idle','2026-11-25T13:00:00Z',1920),job('j3','SQL01','error','2026-11-25T13:30:00Z',600,{lastError:'the receiver at 192.0.2.50 did not answer'})],alerts:[]}},
   {id:'ag2',name:'WEB01',address:'web01.example.com:44390',live:{reachable:false,error:'no answer from web01.example.com:44390',jobs:[],alerts:[]}}],
   summary:{agents:2,unreachable:1,jobs:3,healthy:1,behind:1,failing:1,paused:0,cutOver:0,alerts:0}});
  await goTool(f,'replicate');await f.page.waitForFunction(()=>document.querySelectorAll('#rpAgents .rp-agent').length===2,null,{timeout:15000});
  const bars=await f.page.locator('#rpAgents .rp-bar').evaluateAll(n=>n.map(b=>b.getAttribute('aria-valuenow')+' '+b.className));assert.deepEqual(bars.slice(0,2),['25 rp-bar active','25 rp-bar active'],bars.join());
  const rowText=await f.page.locator('#rpAgents .rp-progress-text').allTextContents();assert(rowText[0].startsWith('Round 4: 25% compared, 25.0 GB of 100 GB checked for changes, 4.0 GB of changes sent'),rowText[0]);assert(rowText[1].startsWith('Initial copy: 25%, 12.5 GB of 50.0 GB copied'),rowText[1]);
  // The initial copy milestone: a whole copy says so, with Test failover and Cut over; one still copying does not.
  const milestones=await f.page.locator('#rpAgents .rp-milestone').allTextContents();assert.equal(milestones.length,3,milestones.join('|'));assert(milestones.every(t=>t.startsWith('Initial copy complete')&&t.endsWith('ready to cut over.')),milestones.join('|'));
  assert.deepEqual(await f.page.locator('#rpAgents [data-rp-goto-cutover]').evaluateAll(n=>n.map(b=>b.getAttribute('data-rp-goto-cutover'))),['ag1|j1','ag1|j2','ag1|j3']);assert.equal(await f.page.locator('[data-rp-test="ag1|j4"]').count(),0);
  assert((await f.page.locator('#rpSummary [data-rp-filter="healthy"]').getAttribute('title')).includes('initial copy is complete'));
  f.repl.agents[0].live.jobs[1].status=Object.assign({},f.repl.agents[0].live.jobs[1].status,{state:'idle',rounds:1,recoveryPoint:'2026-11-25T14:05:00Z',initialCopyAt:'2026-11-25T14:06:00Z',disks:[]});
  await f.page.waitForFunction(()=>document.querySelector('#appNoticeText').textContent.includes('Initial copy complete: PRINT01 on APP01'),null,{timeout:15000});assert((await f.page.locator('#appNoticeText').textContent()).includes('ready for a test failover or cutover'));
  await f.page.locator('[data-rp-goto-cutover="ag1|j4"]').click();await f.page.waitForFunction(()=>document.activeElement&&document.activeElement.id==='rpCutoverTyped');
  assert((await f.page.locator('#rpDetailsFacts').textContent()).includes('Initial copy complete'));
  f.repl.agents[0].live.jobs[1].status=Object.assign({},f.repl.agents[0].live.jobs[1].status,{state:'syncing',rounds:0,recoveryPoint:'',initialCopyAt:'',disks:[{source:'0',target:'PRINT01-disk0.vhdx',size:53687091200,chunks:12800,done:3200,read:13421772800,sent:6442450944}]});
  await f.page.waitForFunction(()=>!document.querySelector('[data-rp-test="ag1|j4"]'),null,{timeout:15000});await f.page.locator('#rpDetailsClose').click();
  assert((await f.page.locator('#rpAgents').textContent()).includes('Failed: the receiver at 192.0.2.50 did not answer'));
  // A long error (URL and certificate thumbprint) wraps: the row's Open button stays in view and a click anywhere on the row opens the job.
  const longError='disk 0: Post "https://192.0.2.50:44380/v1/hello": the receiver\'s certificate thumbprint is 9E:AB:8E:25:DF:2F:79:7C:BD:6A:7A:19:AD:34:DA:CC:7E:4D:19:48:6D:F6:F1:22:DE:ED:56:45:09:81:58:C0, not the one entered for this job';
  f.repl.agents[0].live.jobs[3].status=Object.assign({},f.repl.agents[0].live.jobs[3].status,{lastError:longError});
  for(const size of [{width:1008,height:690},{width:1440,height:1000}]){
   await f.page.setViewportSize(size);await f.page.waitForFunction(()=>/9E:AB:8E:25/.test(document.querySelector('#rpAgents').textContent),null,{timeout:15000});
   const fit=await f.page.evaluate(()=>{const t=document.querySelector('#rpAgents .rp-jobs'),box=t.parentElement.getBoundingClientRect(),open=document.querySelector('[data-rp-open="ag1|j3"]').getBoundingClientRect();return {table:Math.round(t.getBoundingClientRect().right),box:Math.round(box.right),open:Math.round(open.right),body:document.querySelector('#rpAgents').scrollWidth-document.querySelector('#rpAgents').clientWidth};});
   assert(fit.table<=fit.box+1&&fit.open<=fit.box+1&&fit.body<=1,JSON.stringify(size)+' '+JSON.stringify(fit));
  }
  await f.page.locator('#rpAgents tr[data-rp-row="ag1|j3"]:not(.rp-advice) td:nth-child(4)').click();await f.page.waitForFunction(()=>!document.querySelector('#rpDetails').classList.contains('hidden'));
  assert((await f.page.locator('#rpDetails').textContent()).includes('SQL01'));await f.page.locator('#rpDetailsClose').click();
  await f.page.locator('[data-rp-advice="j3"]').click();await f.page.waitForFunction(()=>!document.querySelector('#rpDetails').classList.contains('hidden'));await f.page.locator('#rpDetailsClose').click();
  f.repl.agents[0].live.jobs[3].status=Object.assign({},f.repl.agents[0].live.jobs[3].status,{lastError:'the receiver at 192.0.2.50 did not answer'});await f.page.waitForFunction(()=>!/9E:AB:8E:25/.test(document.querySelector('#rpAgents').textContent),null,{timeout:15000});
  assert.equal(await f.page.locator('#rpSummary [data-rp-filter="behind"]').evaluate(e=>e.tagName),'BUTTON');assert.equal(await f.page.locator('#rpSummary [data-rp-filter="behind"]').evaluate(e=>getComputedStyle(e).cursor),'pointer');
  await f.page.locator('#rpSummary [data-rp-filter="behind"]').click();
  assert.equal(await f.page.locator('#rpSummary [data-rp-filter="behind"]').getAttribute('aria-pressed'),'true');
  assert.deepEqual(await f.page.locator('#rpAgents .rp-jobs tbody tr:not(.rp-advice) td:first-child').allTextContents(),['PRINT01','FILE01']);assert((await f.page.locator('[data-rp-advice="j4"]').textContent()).includes('initial copy is still running'));
  assert((await f.page.locator('[data-rp-advice="j2"]').textContent()).includes('over the 15-minute RPO'));assert((await f.page.locator('.rp-filter-bar').textContent()).includes('Jobs behind their RPO: 2 jobs'));
  await f.page.locator('#rpSummary [data-rp-filter="failing"]').click();
  assert.deepEqual(await f.page.locator('#rpAgents .rp-jobs tbody tr:not(.rp-advice) td:first-child').allTextContents(),['SQL01']);assert((await f.page.locator('[data-rp-advice="j3"]').textContent()).includes('the receiver at 192.0.2.50 did not answer'));
  await f.page.locator('#rpSummary [data-rp-filter="unreachable"]').click();
  assert.deepEqual(await f.page.locator('#rpAgents .rp-agent-title strong').allTextContents(),['WEB01']);
  await f.page.locator('.rp-filter-bar [data-rp-filter]').click();assert.equal(await f.page.locator('.rp-filter-bar').count(),0);assert.equal(await f.page.locator('#rpAgents .rp-agent').count(),2);
  await f.page.locator('#rpSummary [data-rp-filter="healthy"]').click();await f.page.locator('#rpSummary [data-rp-filter="healthy"]').click();assert.equal(await f.page.locator('.rp-filter-bar').count(),0,'the same tile clears the filter');
  await goTool(f,'wizard');assert.equal(await f.page.locator('#nwHere [data-gd-fact]').count(),5);await f.page.locator('#nwHere [data-gd-fact="diskcopy"]').click();assert.equal(await f.page.locator('[data-tool="diskcopy"]').getAttribute('aria-pressed'),'true');
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('A cutover that stops moving says so and can be cancelled: no progress, verifying on the receiver, Cancel cutover and both logs',async()=>{const f=await fixture(browser);try{
  const status={state:'cutting-over',message:'Cutting over: sending the last changes and verifying every disk.',recoveryPoint:'2026-11-26T08:18:09Z',rounds:2,phase:'copying',lastProgress:'2026-11-26T08:30:00Z',stallAfterSeconds:300,cutoverAttempt:1,logFolder:'C:\\ProgramData\\Server Inventory and Migration Manager Agent\\Logs',disks:[{source:'0',target:'PRINT01-disk0.vhdx',size:96636764160,chunks:23040,done:11750,read:16535624704,sent:595591168}]};
  Object.assign(f.repl,{agents:[{id:'ag1',name:'PRINT01',address:'print01.example.com:44390',live:{reachable:true,platform:'Windows',jobs:[{spec:{id:'j1',name:'PRINT01',rpoMinutes:15,receiver:{address:'hv01.example.com:44380'},stopServices:['Spooler'],disks:[{source:'0',target:'PRINT01-disk0.vhdx'}]},rpoSeconds:660,idleSeconds:400,status}],alerts:[]}}],summary:{agents:1,unreachable:0,jobs:1,healthy:0,behind:1,failing:0,paused:0,cutOver:0,alerts:0}});
  await goTool(f,'replicate');await f.page.waitForFunction(()=>document.querySelector('#rpAgents .rp-stall'));
  const stall=await f.page.locator('#rpAgents .rp-stall').textContent();assert(/^No progress for 7 minutes\. /.test(stall)&&stall.includes('stopping this round to try again'),stall);
  await f.page.locator('[data-rp-open="ag1|j1"]').click();await f.page.waitForFunction(()=>!document.querySelector('#rpDetails').classList.contains('hidden'));
  const facts=await f.page.locator('#rpDetailsFacts').textContent();assert(facts.includes('No progress for 7 minutes'),facts);assert(facts.includes('C:\\ProgramData\\Server Inventory and Migration Manager Agent\\Logs on PRINT01'),facts);
  assert.equal(await f.page.locator('#rpUnschedule').textContent(),'Cancel cutover');assert(await f.page.locator('#rpUnschedule').isEnabled());assert(await f.page.locator('#rpDelete').isDisabled());
  assert((await f.page.locator('#rpButtonsReason').textContent()).includes('Cancel cutover stops the final round'));
  // The receiver verifying the copy is not a stall.
  f.repl.agents[0].live.jobs[0].status=Object.assign({},status,{phase:'finishing',cutoverAttempt:2});
  await f.page.waitForFunction(()=>(document.querySelector('#rpAgents .rp-stall')||{}).textContent&&document.querySelector('#rpAgents .rp-stall').textContent.startsWith('The receiver is verifying the copy (attempt 2)'),null,{timeout:15000});
  await f.page.locator('#rpUnschedule').click();await f.page.waitForFunction(()=>document.querySelector('#rpDetailsStatus').textContent.includes('Cancelling'));
  assert.deepEqual(f.replCalls.filter(c=>c.action==='job'&&c.body.action==='cancel-cutover').map(c=>c.body),[{agent:'ag1',id:'j1',action:'cancel-cutover'}]);
  await f.page.locator('#rpReceiverLog').click();await f.page.waitForFunction(()=>document.querySelector('#rpDetailsStatus').textContent.includes('receiver log opened'));assert.deepEqual(f.opens.slice(-1),['receiverlog']);
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Live transfers: the receiver lists connected senders with each copy\'s progress, speed and time left; the sender and a VM move show theirs',async()=>{const f=await fixture(browser);try{
  await goTool(f,'environment');await goTool(f,'diskcopy');
  const now=new Date().toISOString();
  f.state.vmware.receiver={running:true,port:44380,code:'K7QMZ-2RT4X-9WBNP-H3VDA',thumbprint:'3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e',folder:'D:\\Hyper-V\\Disks',addresses:['192.0.2.40'],sessions:1,firewall:true,defaultFolder:'D:\\Hyper-V\\Disks',transfers:[
   {id:'r1',kind:'receive',label:'APP01-disk0.vhdx',source:'APP01, disk 0',target:'APP01-disk0.vhdx',peer:'192.0.2.21',size:107374182400,pass:1,chunks:25600,done:6400,bytesRead:26843545600,bytesWritten:21474836480,bytesSent:12884901888,started:now,updated:now,state:'running',percent:25,bytesPerSecond:115343360,etaSeconds:1395},
   {id:'r0',kind:'receive',label:'APP01-disk1.vhdx',source:'APP01, disk 1',target:'APP01-disk1.vhdx',peer:'192.0.2.21',size:53687091200,pass:2,chunks:12800,done:12800,bytesRead:1073741824,bytesWritten:536870912,started:now,updated:now,finished:now,state:'done',verified:true,percent:100}]};
  f.state.vmware.transfers=[{id:'send-0',kind:'send',label:'Disk 0',source:'SRV-DEMO-01, disk 0',target:'SRV-DEMO-01-disk0.vhdx',peer:'192.0.2.40:44380',size:68719476736,pass:1,chunks:16384,done:16384,bytesRead:34359738368,bytesWritten:34359738368,started:now,updated:now,finished:now,state:'failed',error:'the receiver at 192.0.2.40:44380 stopped answering',percent:62}];
  await f.page.waitForFunction(()=>document.querySelectorAll('#vmReceiveTransfers [data-xfer]').length===2);
  const recv=await f.page.locator('#vmReceiveTransfers').textContent();
  for(const want of ['1 disk copying, 25% overall, 110 MB/s','192.0.2.21','25.0 GB of 100 GB read','about 23 min','Verified','(pass 2, only changes)'])assert(recv.includes(want),want+' in '+recv);
  assert((await f.page.locator('#vmReceiveSessions').textContent()).startsWith('192.0.2.21: 1 disk open'));
  assert(!recv.includes('K7QMZ'),'the pairing code is not in the transfer list');
  await f.page.locator('[data-diskrole="send"]').click();
  const sent=await f.page.locator('#vmSendTransfers').textContent();assert(sent.includes('Failed')&&sent.includes('stopped answering')&&sent.includes('192.0.2.40:44380'),sent);
  await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('[data-diskrole="receive"]').click();await f.page.locator('#vmReceiveTransfers').evaluate(e=>e.scrollIntoView({block:'center'}));await f.page.screenshot({path:shots+'/vmware-receive-transfers-1440x1000.png'});
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Selecting rows keeps the layout still: nothing below the list moves when a row is ticked or unticked, and no hint says to tick rows',async()=>{const f=await fixture(browser);try{
  await f.page.setViewportSize({width:1350,height:690});
  // Tops of every visible element with an id in the shown pane; ids survive a rebuild, so a moved control is found even when it was redrawn.
  const tops=()=>f.page.evaluate(()=>{const out={},pane=Array.prototype.find.call(document.querySelectorAll('.tool-pane'),p=>p.offsetParent!==null)||document.body;
    Array.prototype.forEach.call(pane.querySelectorAll('[id]'),e=>{if(e.closest('tbody'))return;const r=e.getBoundingClientRect();if(!r.width||!r.height)return;let y=r.top;for(let q=e;q;q=q.parentElement){if(/sticky|fixed/.test(getComputedStyle(q).position))return;if(q!==e)y+=q.scrollTop;}out[e.id]=Math.round(y+window.scrollY);});return out;});
  const still=async(label,act)=>{const a=await tops();await act();await f.page.waitForTimeout(250);const b=await tops();
    const moved=Object.keys(a).filter(k=>k in b&&Math.abs(a[k]-b[k])>1).map(k=>k+' '+a[k]+'->'+b[k]);assert.deepEqual(moved,[],label+': moved '+moved.join(', '));};
  await vmwareConnected(f);f.vm.plan=vmwarePlanFixture();f.state.vmware.inventorySerial=2;
  await goTool(f,'movevms');await f.page.locator('#vmObjTab-vms').click();await f.page.waitForFunction(()=>document.querySelectorAll('#vmTableBody input[data-vm-pick]').length>1);
  assert(!/tick VMs to change many/i.test(await f.page.locator('#vmListPanel').textContent()),'no hint to tick VMs');
  const pick=f.page.locator('#vmTableBody input[data-vm-pick]').first();
  await still('Move VMs tick',()=>pick.check());assert(await f.page.locator('#vmListBulk [data-bulk="put"]').isEnabled());
  await still('Move VMs untick',()=>pick.uncheck());assert(await f.page.locator('#vmListBulk [data-bulk="put"]').isDisabled(),'the wave actions stay in place, turned off');
  const row=(o)=>Object.assign({percent:-1,lagSeconds:-1,wave:0,actions:[],detail:'',reason:''},o);
  f.servers.rows=[row({key:'job:a1|j1',kind:'job',method:'Agent replication',name:'APP01',agent:'a1',job:'j1',state:'insync',stateText:'In sync',actions:['sync','pause','test','cutover','wave']}),
    row({key:'agent:a4',kind:'agent',method:'Agent installed',name:'FS01',agent:'a4',state:'agent',stateText:'Agent installed, no job',actions:[]})];
  await goTool(f,'servers');await f.page.waitForSelector('[data-as-row="job:a1|j1"]');
  assert(!/Tick servers in the list/.test(await f.page.locator('#tabServers').textContent()),'no hint to tick servers');
  await still('All servers tick',()=>f.page.locator('[data-as-row="job:a1|j1"]').check());
  await still('All servers tick one that cannot act',()=>f.page.locator('[data-as-row="agent:a4"]').check());
  await still('All servers untick',async()=>{await f.page.locator('[data-as-row="agent:a4"]').uncheck();await f.page.locator('[data-as-row="job:a1|j1"]').uncheck();});
  f.plans.plans=[{id:'p0',name:'Wave 1',server:'vc01.example.com',paused:false,settings:{},throttle:'no limit',summary:{total:2,waiting:0,copying:0,ready:2,testing:0,scheduled:0,done:0,failed:0},vms:[{id:'vm-web02',name:'WEB02',wave:1,state:'ready',seeded:true,failures:0},{id:'vm-app01',name:'APP01',wave:1,state:'ready',seeded:true,failures:0}]}];
  await goTool(f,'plans');await f.page.waitForSelector('#mpRows input[data-mp-row]');
  await still('Migration plans tick',()=>f.page.locator('#mpRows input[data-mp-row]').first().check());
  await still('Migration plans untick',()=>f.page.locator('#mpRows input[data-mp-row]').first().uncheck());
  f.vm.inventory=vmwareInventoryFixture();f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',summary:vmwareSummaryFixture(),inventorySerial:3,canPrepare:true,hyperVHere:true});
  await goTool(f,'wizard');await f.page.locator('[data-nw-source="vms"]').click();await f.page.waitForSelector('#nwVmList [data-nw-pick]:not([disabled])');
  const nwPick=f.page.locator('#nwVmList [data-nw-pick]:not([disabled])').first();
  await f.page.waitForTimeout(2500);
  await still('New migration tick',()=>nwPick.check());await still('New migration untick',()=>nwPick.uncheck());
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
   await goTool(f,'server');await inView('#startButton',30);await inView('#saveChoicesButton',30);await inView('#configBody',withUpdate?150:200);
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
   await goTool(f,'shares');await f.page.locator('#migrateBody').evaluate(e=>e.scrollTop=0);await inView('#migrateBody',withUpdate?110:150);await inView('#migrateOpenLogs',24);await inView('#migrateSource',24);
   await f.page.locator('#migrateLogBlock summary').evaluate(e=>{if(!e.parentNode.open)e.click();});
   for(const target of ['#migrateCheck','#migrateCopy','#migrateCutover','#migrateAliasCheck','#migrateLog']){await f.page.locator(target).scrollIntoViewIfNeeded();await inView(target,target==='#migrateLog'?60:24);}
   assert.deepEqual(await smallText(),[],label+' migrate text below 10.5px');assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),label+' migrate overflow');
   assert(await f.page.locator('#migrateBody').evaluate(e=>e.scrollWidth<=e.clientWidth+1),label+' migrate panel overflow');
   if(size.width===784&&!withUpdate){await f.page.locator('#migrateBody').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:out+'/migrate-800x600.png'});}
   await f.page.locator('#migrateCutover').scrollIntoViewIfNeeded();await f.page.locator('#migrateCutover').click();await inView('#migrateConfirmInput',24);await inView('#migrateConfirmButton',28);assert.deepEqual(await smallText(),[],label+' migrate dialog text below 10.5px');
   await f.page.locator('#migrateDialogCancel').click();assert(!await f.page.locator('#migrateDialog').isVisible());
   await goTool(f,'environment');await f.page.locator('#vmwareBody').evaluate(e=>e.scrollTop=0);await inView('#vmwareBody',withUpdate?110:150);await inView('#vmReadBarToggle',24);
   for(const view of ['env','plan','guest','disks','failover','compare']){await goTool(f,{env:'movevms',disks:'diskcopy'}[view]||view);await inView('[data-tool="'+({env:'movevms',disks:'diskcopy'}[view]||view)+'"]',24);
    for(const target of {env:['#vmInvTree .vm-inv-row','#vmDrawerTitle','.vm-obj-tab'],plan:['#vmSuggest','#vmWriteRunbook'],guest:['#vmGuestCapture','#vmGuestApply','#vmGuestTools'],disks:['#vmReceiveStart','#vmSendFind','#vmSendStart','#vmMediaBuild'],failover:['#fvRefresh'],compare:['#vmCompareRun','#vmCompareBody tr']}[view]){for(let attempt=0;;attempt++){await f.page.locator(target).first().scrollIntoViewIfNeeded();try{await inView(target+':visible >> nth=0',20);break;}catch(e){if(attempt>=2){throw e;}await f.page.waitForTimeout(300);}}}
    assert.deepEqual(await smallText(),[],label+' vmware '+view+' text below 10.5px');assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),label+' vmware overflow');assert(await f.page.locator('#vmwareBody').evaluate(e=>e.scrollWidth<=e.clientWidth+1),label+' vmware panel overflow '+view);}
   if(size.width===784&&!withUpdate){await goTool(f,'environment');await f.page.locator('#vmwareBody').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:out+'/vmware-800x600.png'});}
   await goTool(f,'convert');await inView('#cvStart',28);await inView('#cvSource',24);await inView('#cvBrowse',24);
   for(const target of ['.cv-format[data-format=raw]','#cvTarget','#cvVerify']){await f.page.locator(target).scrollIntoViewIfNeeded();await inView(target,12);}
   assert.deepEqual(await smallText(),[],label+' converter text below 10.5px');assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),label+' converter overflow');
   if(size.width===784&&!withUpdate){await f.page.locator('.cv-body').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:out+'/convert-800x600.png'});}
   await goTool(f,'replicate');await inView('.rp-body',withUpdate?110:150);
   for(const target of ['#rpPush','#rpHosts','#rpAlerts','#rpAgents']){await f.page.locator(target).scrollIntoViewIfNeeded();await inView(target,target==='#rpPush'?28:12);}
   assert.deepEqual(await smallText(),[],label+' replication text below 10.5px');assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),label+' replication overflow');
   assert(await f.page.locator('.rp-body').evaluate(e=>e.scrollWidth<=e.clientWidth+1),label+' replication panel overflow');
   await goTool(f,'hvbuild');await f.page.waitForSelector('#hbBlank');await f.page.locator('.hb-body').evaluate(e=>e.scrollTop=0);await inView('#hbBlank',24);
   if(!await f.page.locator('#hbName').count()){await f.page.locator('#hbBlank').click();}
   for(const target of ['#hbName','#hbAddNode','#hbAddSwitch','#hbStorageKind','#hbSave','#hbSteps']){await f.page.locator(target).scrollIntoViewIfNeeded();await inView(target,12);}
   assert.deepEqual(await smallText(),[],label+' build Hyper-V hosts text below 10.5px');assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),label+' build Hyper-V hosts overflow');
   assert(await f.page.locator('.hb-body').evaluate(e=>e.scrollWidth<=e.clientWidth+1),label+' build Hyper-V hosts panel overflow');
   if(size.width===784&&!withUpdate){await f.page.locator('.hb-body').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:out+'/hvbuild-800x600.png'});}
   const small=await f.page.evaluate(()=>Array.from(document.querySelectorAll('body *')).filter(e=>e.offsetParent&&e.childNodes.length&&Array.from(e.childNodes).some(n=>n.nodeType===3&&n.nodeValue.trim())&&parseFloat(getComputedStyle(e).fontSize)<10.5).map(e=>e.className||e.id||e.tagName));assert.deepEqual(small,[],label+' text below 10.5px');
   if(size.width===784&&!withUpdate){await f.page.locator('#tabButtonCollection').click();await f.page.screenshot({path:out+'/collection-800x600.png'});await goTool(f,'server');await f.page.screenshot({path:out+'/setup-800x600.png'});}
  }}
 }finally{await f.close();}});
 report.status='passed';
 }finally{await browser.close();}})().catch(e=>{report.status='failed';report.error=e.stack;console.error(e);process.exitCode=1;}).finally(()=>fs.writeFileSync(out+'/results.json',JSON.stringify(report,null,2)));
