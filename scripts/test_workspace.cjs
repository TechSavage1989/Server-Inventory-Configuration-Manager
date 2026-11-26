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
async function fixture(browser,old=false){
 let saved=null,started=[],saveCalls=0,rejectStart=false,skipRequests=[],opens=[],restoreReject=null,migrateReject={},vmwareReject={};const receiverPair={allowed:false};
 const logs=[],dialogs=[],errors=[],restoreCalls={browse:0,scan:[],start:[],cancel:0,tokens:[]},migrateCalls=[],vmwareCalls=[],convertCalls=[],replCalls=[],planCalls=[];const plans={plans:[],inventory:{open:true,server:'vc01.example.com',serverType:'vCenter Server',live:true}};let convertReject={},convertSize=0;const pickCalls=[];let pickReject='';
 const state={status:'idle',phase:'Ready',running:false,computerName:'SRV-DEMO-01',isAdministrator:true,powerShellVersion:'5.1',defaultOutputRoot:'C:\\Reports',update:{status:'current',title:'Up to date',message:'Installed v3.2.10. Latest v3.2.10.',canCheck:true},
  restore:{status:'idle',packages:{lastCollection:'',loadedReport:''},items:[],logs:[]},migrate:migrateIdle(),vmware:vmwareIdle(),convert:convertIdle()};
 const vm={inventory:null,plan:null,sizing:null,guest:null,compare:null};
 const failover={failovers:[],copies:[{name:'APP01-disk0.vhdx',size:107374182400,complete:true,verified:false,round:'a1@2026-11-25T14:19:23Z',job:'a1',recoveryPoint:'2026-11-25T14:19:23Z',updated:'2026-11-25T14:20:00Z',busy:false,generation:2,machine:{name:'APP01',cpus:6,memoryMB:12288,firmware:'uefi',adapters:2}},{name:'APP01-disk1.vhdx',size:536870912000,complete:true,verified:false,round:'a1@2026-11-25T14:19:23Z',job:'a1',recoveryPoint:'2026-11-25T14:19:23Z',updated:'2026-11-25T14:20:00Z',busy:false,generation:1},{name:'SQL01-disk0.vhdx',size:107374182400,complete:false,verified:false,updated:'2026-11-25T14:20:00Z',busy:true,generation:1}]};
 const targetTest={running:false,log:[],error:'',tests:[]};
 const repl={ok:true,pushing:false,pushLog:[],pushResults:null,serial:0,isAdministrator:true,defaultPort:44390,agents:[],summary:{agents:0,unreachable:0,jobs:0,healthy:0,behind:0,failing:0,paused:0,cutOver:0,alerts:0}};
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
  if(url.pathname.startsWith('/api/hvbuild/')){
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{},action='hvbuild/'+url.pathname.slice(13);vmwareCalls.push({action,method:req.method,body:data,token:req.headers['x-app-token']});
   if(req.headers['x-app-token']!=='workspace-test'){send(403,{});return;}
   if(vmwareReject[action]){send(400,{error:vmwareReject[action]});return;}
   const v=state.vmware,hb=v.hvBuild=v.hvBuild||{plan:null,revision:'',steps:[],results:{},saved:[],serial:0,here:'ADMIN01'};
   if(action==='hvbuild/suggest'){send(200,{ok:true,plan:JSON.parse(JSON.stringify(hvBuildFixture.plan))});return;}
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
   if(action==='guest-auto'){v.guestAuto={user:data.user,hasPassword:true};send(200,{ok:true,user:data.user});return;}
   if(action==='guest-auto/forget'){v.guestAuto={user:'',hasPassword:false};send(200,{ok:true});return;}
   if(action==='guest-auto/run'){(v.fixups||[]).forEach(x=>{if(x.vm===data.vm){x.state='waiting';x.message='Waiting for Windows in '+x.vm+' to start.';x.steps=[];}});send(202,{ok:true});return;}
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
   if(action==='failover'&&req.method==='GET'){send(200,Object.assign({ok:true,hyperV:true,folder:'D:\\Migration\\VMware-Copies\\Received',receiving:true},failover,url.searchParams.get('switches')==='1'?{switches:[{name:'Isolated test',type:'Private'},{name:'LAN',type:'External'}]}:{}));return;}
   if(action==='failover'&&data.action==='start'){failover.failovers=[{id:'f1',mode:data.request.mode,name:data.request.name,vmId:'6f1d4c3a-0000-4000-8000-000000000001',copies:data.request.copies,recoveryPoint:'2026-11-25T14:19:23Z',started:'2026-11-25T14:30:00Z'}];failover.copies.forEach(c=>{if(data.request.copies.includes(c.name))c.held='test failover '+data.request.name;});send(200,{ok:true});return;}
   if(action==='targettest'&&req.method==='GET'){const t=(state.vmware&&state.vmware.target)||{};send(200,Object.assign({ok:true,connected:!!t.connected,platform:t.platform||'',server:t.server||'',user:t.user||'',node:t.node||''},targetTest));return;}
   if(action==='targettest'&&data.action==='start'){Object.assign(targetTest,{running:true,log:['14:30:00  Pausing APP01 when its current round finishes, so the copy is one whole recovery point.']});send(200,{ok:true});return;}
   if(action==='targettest'&&data.action==='end'){targetTest.tests=[];targetTest.log=['14:40:00  The test APP01-TEST ended: its VM and disks were deleted. The replica was not touched.'];send(200,{ok:true});return;}
   if(action==='failover'&&data.action==='end'){failover.failovers=[];failover.copies.forEach(c=>{delete c.held;});send(200,{ok:true});return;}
   if(action==='push'){Object.assign(repl,{pushing:true,pushLog:['14:02:10  Installing the agent on 2 servers.','14:02:11  app01.example.com: copying the agent.']});send(202,{ok:true});return;}
   if(action==='agent'&&data.action==='disks'){send(200,{ok:true,disks:[{id:'0',size:107374182400,system:true,volumes:[{name:'v1',mounts:[],fileSystem:'FAT32',length:104857600},{name:'v2',mounts:['C:\\'],fileSystem:'NTFS',length:106300000000}]},{id:'1',size:536870912000,system:false,volumes:[{name:'v3',mounts:['D:\\','E:\\'],fileSystem:'NTFS',length:536700000000,kind:'spanned'}]}]});return;}
   if(action==='agent'&&data.action==='receiverdisks'){send(200,{ok:true,folder:false,disks:[{name:'disk-0',label:'Disk 0, QEMU HARDDISK',size:107374182400},{name:'disk-1',label:'Disk 1, QEMU HARDDISK',size:536870912000}]});return;}
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
  if(url.pathname==='/api/plans'){
   let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):{};planCalls.push({method:req.method,body:data,token:req.headers['x-app-token']});
   if(req.method==='GET'){send(200,Object.assign({ok:true,sweeping:false,blocked:'',hyperVHere:true,testing:false,waves:{},switches:[{name:'Isolated',type:'Private'}]},plans));return;}
   if(data.action==='create'){plans.plans.push({id:'p1',name:data.name,server:'vc01.example.com',paused:false,settings:data.settings,throttle:'no limit',summary:{total:data.vmIds.length,waiting:data.vmIds.length,copying:0,ready:0,testing:0,scheduled:0,done:0,failed:0},vms:data.vmIds.map(id=>({id,name:id.replace('vm-','').toUpperCase(),wave:0,state:'waiting',seeded:false,failures:0}))});send(200,{ok:true,id:'p1'});return;}
   send(200,{ok:true});return;
  }
  if(url.pathname==='/api/pick'){let body='';for await(const chunk of req)body+=chunk;const data=JSON.parse(body);pickCalls.push(data);if(pickReject){send(400,{ok:false,error:pickReject});return;}send(200,{ok:true,selected:true,path:data.kind==='remote'?'C:\\ClusterStorage\\Volume1\\Picked':data.kind==='file'?'E:\\Media\\receiver.iso':'E:\\Picked'});return;}
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
 return {page,context,state,pickCalls,rejectPick(message){pickReject=message},vm,repl,plans,planCalls,failover,receiverPair,targetTest,replCalls,vmwareCalls,convertCalls,rejectConvert(action,message){convertReject[action]=message},setConvertSize(size){convertSize=size},rejectVmware(action,message){vmwareReject[action]=message},logs,dialogs,errors,started,skipRequests,opens,restoreCalls,migrateCalls,rejectMigrate(action,message){migrateReject[action]=message},rejectRestore(message){restoreReject=message},get saved(){return saved},get saveCalls(){return saveCalls},reject(){rejectStart=true},async close(){assert.deepEqual(errors,[]);await context.close();await new Promise(r=>server.close(r));}};
}
// Tools live in two tabs: Discovery (Alt+1) and Migration and replication (Alt+5).
const discoveryTools=['server','environment'];
async function goTool(f,id){await f.page.keyboard.press(discoveryTools.includes(id)?'Alt+1':'Alt+5');await f.page.locator('[data-tool="'+id+'"]').click();}
// The VMware and Hyper-V inventory: open an item from the tree, and read the tree as 'level label' lines.
// HTML5 drag and drop with real mouse steps (locator.dragTo misses the drop on scrolled pages).
async function dragTo(f,from,to){const a=await f.page.locator(from).boundingBox(),b=await f.page.locator(to).boundingBox();await f.page.mouse.move(a.x+5,a.y+5);await f.page.mouse.down();await f.page.mouse.move(a.x+30,a.y+30,{steps:5});await f.page.mouse.move(b.x+5,b.y+5,{steps:10});await f.page.mouse.up();}
async function openVm(f,id){await f.page.locator('#vmInvTree li[data-inv-id="vm:'+id+'"] > .vm-inv-row').click();}
async function openTree(f,label){await f.page.locator('#vmInvTree li.vm-inv-item > .vm-inv-row').filter({has:f.page.locator('.vm-inv-label',{hasText:new RegExp('^'+label.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')+'$')})}).first().click();}
async function treeLines(f){return f.page.locator('#vmInvTree li.vm-inv-item').evaluateAll(list=>list.map(li=>li.getAttribute('aria-level')+' '+li.querySelector('.vm-inv-label').textContent));}
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
 await check('VMware tab: Read Hyper-V host reads this computer or a remote host, sends the optional account once, opens a VM disk in the Disk converter, and copies a running Hyper-V VM through checkpoints without a vSphere session',async()=>{const f=await fixture(browser);try{
  await goTool(f,'environment');
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
  await f.page.keyboard.press('Alt+1');assert(await f.page.locator('#startButton').isDisabled(),'collection locks while VMware reads');await goTool(f,'environment');
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
  await f.page.locator('#vmReset').click();await f.page.waitForFunction(()=>document.querySelector('#vmEnvResult').classList.contains('hidden'));assert.equal(await f.page.locator('#vmServer').inputValue(),'');
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
  await f.page.locator('#vmGaUser').fill('EXAMPLE\\admin');await f.page.locator('#vmGaPassword').fill('Synthetic-Guest-1');await f.page.locator('#vmGaUse').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmGaPanel')&&document.querySelector('#vmGaPanel').textContent.includes('sign in to the new VMs as EXAMPLE\\admin'));
  assert.deepEqual(vmwareCalls(f,'guest-auto').map(c=>c.body),[{user:'EXAMPLE\\admin',password:'Synthetic-Guest-1'}]);
  assert.equal(await f.page.locator('#vmGaPassword').count(),0,'the password field is gone once the account is held');
  await f.page.locator('#vmMoveFolder').fill('E:\\Copies\\APP01');
  await f.page.locator('#vmMoveCopy').click();assert(await f.page.locator('#migrateDialog').isVisible());
  const copyText=await f.page.locator('#migrateDialogText').textContent();assert(copyText.includes('changed block tracking')&&copyText.includes('E:\\Copies\\APP01'),copyText);
  assert(await f.page.locator('#migrateConfirmButton').isDisabled());await f.page.locator('#migrateConfirmInput').fill('APP01');await f.page.locator('#migrateConfirmInput').press('Enter');
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert(await f.page.locator('#vmStop').isVisible(),'a copy can be stopped');
  assert.deepEqual(vmwareCalls(f,'move').map(c=>c.body),[{vmId:'vm-app01',action:'copy',copyFolder:'E:\\Copies\\APP01',vmFolder:'',quiesce:true,keepMac:false,start:true,cluster:false,disconnectSource:true,fixUp:true,confirm:'APP01'}]);
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',move:{vm:'APP01',cutover:false,round:{disks:[{label:'Hard disk 1',mode:'seed',bytesWritten:21474836480,verified:false}]},disks:['E:\\Copies\\APP01\\APP01-disk0.vhdx']},moveSerial:1});
  await f.page.waitForFunction(()=>document.querySelector('#vmMove')&&document.querySelector('#vmMove').textContent.includes('first copy of the used areas'));
  assert.equal(await f.page.locator('#vmMoveCopy').textContent(),'Copy changes\u2026');
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
  await f.page.locator('[data-hvr="host"]').fill('hvclu01.example.com');await f.page.locator('[data-hvr="user"]').fill('EXAMPLE\\admin');await f.page.locator('[data-hvr="password"]').fill('Synthetic-Pass-1');
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
  // The guided flow accepts the remote connection instead of running on the host.
  await goTool(f,'guide');
  const guide=await f.page.locator('#tabGuide').textContent();assert(!guide.includes('is not a Hyper-V host. Run this application on the Hyper-V host'),'the guide still blocks a remote move');
  await goTool(f,'movevms');await openVm(f,'vm-app01');await f.page.locator('#vmHvrForget').click();
  await f.page.waitForFunction(()=>!!document.querySelector('#vmHvrConnect'));
  assert.deepEqual(f.dialogs,[]);
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
 await check('Guided migration: each flow lists every step with the computer it runs on, and a step on the wrong computer or short of space is blocked',async()=>{const f=await fixture(browser);try{
  await goTool(f,'guide');assert(await f.page.locator('#gdPlan').isHidden(),'nothing is shown until source and target are chosen');
  await f.page.locator('[data-gd-source="vmware"]').click();assert(await f.page.locator('#gdTargetPick').isVisible());
  assert.deepEqual(await f.page.locator('[data-gd-target]').evaluateAll(n=>n.map(x=>x.getAttribute('data-gd-target'))),['hyperv','scale','nutanix','proxmox','physical']);
  await f.page.locator('[data-gd-target="hyperv"]').click();
  assert.equal(await f.page.locator('#gdPlanTitle').textContent(),'Moving a VMware VM to Hyper-V');
  const titles=await f.page.locator('#gdSteps .gd-head strong').allTextContents();
  for(const want of ['Read vCenter or ESXi','Choose where the VM\'s files go','Copy the disks while the VM runs','Cut over: this builds the Hyper-V VM','Make it highly available (failover cluster only)','Fix up the new machine'])assert(titles.includes(want),titles.join(' | '));
  const storage=f.page.locator('#gdSteps li.gd-step').filter({hasText:'Choose where the VM\'s files go'});
  assert(await storage.evaluate(e=>e.classList.contains('blocked')),'blocked on a computer that is not a Hyper-V host');assert(await storage.locator('[data-gd-open]').isDisabled());
  assert((await storage.locator('[data-gd-check]').textContent()).includes('is not a Hyper-V host'));
  await vmwareConnected(f);await goTool(f,'guide');
  Object.assign(f.state.vmware,{hyperVHere:true,inventorySerial:2,hostStorage:{known:true,vhdPath:'D:\\Hyper-V\\Disks',clustered:false,volumes:[{path:'D:\\',freeBytes:21474836480,totalBytes:107374182400,csv:false}]}});
  await f.page.waitForFunction(()=>[...document.querySelectorAll('#gdSteps li.gd-step')].some(li=>li.textContent.includes('Not enough space')));
  Object.assign(f.state.vmware,{inventorySerial:3,hostStorage:{known:true,vhdPath:'D:\\Hyper-V\\Disks',clustered:true,volumes:[{path:'C:\\ClusterStorage\\Volume1',freeBytes:1099511627776,totalBytes:2199023255552,csv:true}]}});
  await f.page.waitForFunction(()=>[...document.querySelectorAll('#gdSteps li.gd-step')].some(li=>li.textContent.includes('choose a Cluster Shared Volume')&&!li.classList.contains('blocked')));
  await f.page.locator('[data-gd-source="server"]').click();await f.page.locator('[data-gd-target="hyperv"]').click();
  const serverTitles=await f.page.locator('#gdSteps .gd-head strong').allTextContents();
  assert(serverTitles.includes('Choose where the copies go')&&serverTitles.includes('Install the agent on the server being moved')&&serverTitles.includes('Add the agent to your endpoint protection exclusions')&&serverTitles.includes('Wait for the initial copy to complete'),serverTitles.join(' | '));
  assert((await f.page.locator('#gdComputers').textContent()).includes('The new Hyper-V host'));
  await f.page.locator('[data-gd-source="shares"]').click();assert(await f.page.locator('#gdTargetPick').isHidden());assert.equal(await f.page.locator('#gdPlanTitle').textContent(),'Moving file shares to a new file server');
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Works with: every migration tool says which systems it works for and how, and the guide lists only the ways that fit the chosen source and target',async()=>{const f=await fixture(browser);try{
  await goTool(f,'guide');
  const card=async id=>(await f.page.locator(`[data-tool="${id}"]`).textContent());
  {const t=await card('movevms');assert(t.includes('Works with VMware (vCenter or ESXi) or Hyper-V VMs to Hyper-V, Scale HyperCore, Nutanix AHV, Proxmox VE or a physical server')&&t.includes('Agentless')&&t.includes('Live, source keeps running'),t);}
  {const t=await card('replicate');assert(t.includes('Any running Windows Server or Linux server, physical or a VM on any hypervisor')&&t.includes('Agent on the source'),t);}
  {const t=await card('convert');assert(t.includes('Offline, source shut down')&&t.includes('VMware ESXi or vCenter'),t);}
  assert((await card('shares')).includes('to a new Windows file server'));assert((await card('environment')).includes('Read only'));
  await goTool(f,'convert');assert((await f.page.locator('#toolWorks-tabConvert').textContent()).includes('Offline, source shut down'));
  await goTool(f,'movevms');assert((await f.page.locator('#toolWorks-tabVmware').textContent()).includes('Agentless'));
  await goTool(f,'failover');assert((await f.page.locator('#toolWorks-tabVmware').textContent()).includes('Copies received on a Hyper-V host'));
  await goTool(f,'guide');await f.page.locator('[data-gd-source="vmware"]').click();await f.page.locator('[data-gd-target="hyperv"]').click();
  const modes=async()=>f.page.locator('[data-gd-mode]').evaluateAll(n=>n.map(x=>x.getAttribute('data-gd-mode')+(x.classList.contains('current')?'*':'')));
  assert.deepEqual(await modes(),['movevms*','plans','onevmhv','replicate','sendbyhand','convert']);
  {const t=await f.page.locator('[data-gd-mode="onevmhv"]').textContent();assert(t.includes('remote Hyper-V host or failover cluster (Windows Server 2012 R2 to 2025')&&t.includes('vCenter Server or ESXi 6.5 to 8.0')&&t.includes('Agentless'),t);}
  await f.page.locator('[data-gd-target="scale"]').click();assert.deepEqual(await modes(),['movevms*','replicate','sendbyhand','convert']);
  await f.page.locator('[data-gd-target="physical"]').click();assert.deepEqual(await modes(),['movevms*','replicate','sendbyhand'],'disk images are not written to a physical server');
  await f.page.locator('[data-gd-mode-open="replicate"]').click();
  assert.equal(await f.page.locator('[data-gd-source="server"]').getAttribute('aria-pressed'),'true');assert.deepEqual(await modes(),['replicate*','sendbyhand','convert'].filter(m=>m!=='convert'));
  await f.page.locator('[data-gd-source="image"]').click();await f.page.locator('[data-gd-target="vsphere"]').click();assert.deepEqual(await modes(),['convert*']);
  assert((await f.page.locator('#gdModes').textContent()).startsWith('How it is done:'));
  await f.page.locator('[data-gd-source="shares"]').click();assert.deepEqual(await modes(),['shares*']);
  await f.page.locator('[data-gd-source="vmware"]').click();await f.page.locator('[data-gd-target="hyperv"]').click();
  await f.page.locator('[data-gd-mode-open="sendbyhand"]').click();assert(await f.page.locator('#vmViewDisks').isVisible(),'a mode without its own steps opens its tool');
  await f.page.setViewportSize({width:1440,height:1000});await goTool(f,'guide');await f.page.locator('#gdModes').scrollIntoViewIfNeeded();await f.page.screenshot({path:shots+'/guide-modes-1440x1000.png'});
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 await check('Guided migration shows one step at a time with Next, folds optional steps, asks for the vCenter password inside a blocked step, and Prepare and fix up reaches the new VM with nothing installed in it',async()=>{const f=await fixture(browser);try{
  await goTool(f,'guide');await f.page.locator('[data-gd-source="vmware"]').click();await f.page.locator('[data-gd-target="hyperv"]').click();
  assert.equal(await f.page.locator('#gdSteps li.gd-step.current').count(),1,'exactly one step is open');
  assert.equal(await f.page.locator('#gdSteps li.gd-step.current .gd-head strong').textContent(),'Read vCenter or ESXi');
  assert(!await f.page.locator('#gdSteps li.gd-step').nth(1).locator('.gd-text').isVisible(),'other steps are folded');
  const optional=await f.page.locator('#gdSteps li.gd-step.optional .gd-head strong').allTextContents();
  for(const want of ['Plan waves and write a runbook','Save BitLocker keys (only with BitLocker or a virtual TPM)','Compare before and after'])assert(optional.includes(want),optional.join(' | '));
  const all=(await f.page.locator('#tabGuide').textContent());assert(!/Copy this application into the VM/.test(all),'no step asks to copy the application into the VM');
  assert((await f.page.locator('#gdModes').textContent()).includes('Pick this when'),'each way of moving says when to pick it');
  await f.page.locator('#gdSteps li.gd-step.current [data-gd-next]').click();
  assert.equal(await f.page.locator('#gdSteps li.gd-step.current .gd-head strong').textContent(),'Check readiness');
  assert((await f.page.locator('#gdProgress').textContent()).startsWith('Step 2 of '));
  await f.page.locator('#gdSteps li.gd-step').filter({hasText:'Fix up the new machine'}).locator('.gd-head').click();
  const fixup=f.page.locator('#gdSteps li.gd-step.current');assert.equal(await fixup.locator('.gd-head strong').textContent(),'Fix up the new machine');
  assert((await fixup.locator('.gd-text').textContent()).includes('PowerShell Direct'));
  await vmwareConnected(f);Object.assign(f.state.vmware,{hyperVHere:true,canPrepare:false,inventorySerial:2,hostStorage:{known:true,vhdPath:'D:\\Hyper-V\\Disks',clustered:false,volumes:[{path:'D:\\',freeBytes:1099511627776,totalBytes:2199023255552,csv:false}]}});
  await goTool(f,'guide');await f.page.locator('#gdSteps li.gd-step').filter({hasText:'Copy the disks while the VM runs'}).locator('.gd-head').click();
  const copy=f.page.locator('#gdSteps li.gd-step.current');await f.page.waitForFunction(()=>!!document.querySelector('#gdSteps li.gd-step.current [data-gd-vcpass]'));
  assert((await copy.locator('[data-gd-check]').textContent()).includes('does not hold the vCenter password'));
  assert((await copy.locator('[data-gd-fix]').textContent()).includes('readonly@vsphere.local on vc01.example.com'));
  await copy.locator('[data-gd-vcpass]').fill('Synthetic-Pa55');await copy.locator('[data-gd-vcconnect]').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Reading');
  assert.deepEqual(vmwareCalls(f,'connect').at(-1).body,{server:'vc01.example.com',user:'readonly@vsphere.local',password:'Synthetic-Pa55'},'the inline fix reconnects with the held server and account');
  Object.assign(f.state.vmware,{running:false,action:'',status:'connected',canPrepare:true,move:{vm:'APP01',cutover:true,hyperVId:'4f6c1d6e-2b1a-4c55-9d3e-0f1e2d3c4b5a'},moveSerial:3});
  await goTool(f,'guide');await f.page.locator('#gdSteps li.gd-step').filter({hasText:'Fix up the new machine'}).locator('.gd-head').click();
  await f.page.waitForFunction(()=>document.querySelector('#gdSteps li.gd-step.current [data-gd-check]').textContent.includes('The new VM APP01 exists'));
  await f.page.locator('#gdSteps li.gd-step.current [data-gd-open]').click();
  assert(await f.page.locator('#vmViewGuest').isVisible());
  assert(await f.page.locator('input[name="vmGuestMode"][value="direct"]').isChecked(),'after a cutover on the host the guide picks PowerShell Direct');
  assert.equal(await f.page.locator('#vmGuestVmName').inputValue(),'APP01','the new VM is filled in');
  assert(await f.page.locator('input[name="vmGuestMode"][value="hostdirect"]').isDisabled(),'the connected-host option needs a connection');
  await f.page.locator('#vmGuestUser').fill('.\\Administrator');await f.page.locator('#vmGuestPassword').fill('Synthetic-Guest-1');await f.page.locator('#vmGuestUse').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmGuestPassword').value==='');
  assert.deepEqual(vmwareCalls(f,'guest-target').at(-1).body,{mode:'direct',vm:'APP01',computer:'',host:'',user:'.\\Administrator',password:'Synthetic-Guest-1',keepPassword:true});
  f.state.vmware.guestTarget={mode:'direct',modeName:'A VM on this Hyper-V host (PowerShell Direct)',label:'APP01',vm:'APP01',computer:'',host:'',user:'.\\Administrator',hasPassword:true,kept:'C:\\x',keptSource:'vCenter',keptAt:'2026-11-26T03:00:00Z'};
  await f.page.waitForFunction(()=>document.querySelector('#vmGuestTargetNote').textContent.includes('Checks run on APP01'));
  assert((await f.page.locator('#vmGuestTargetNote').textContent()).includes('Recording kept here: from vCenter'));
  await f.page.waitForFunction(()=>true);await f.page.waitForTimeout(400);
  assert(vmwareCalls(f,'guest/status').length>=1,'Use this machine reads the VM straight away');
  await f.page.locator('input[name="vmGuestMode"][value="winrm"]').check();assert(await f.page.locator('#vmGuestComputerName').isVisible()&&!await f.page.locator('#vmGuestVmName').isVisible());
  await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('#vmGuestWhere').scrollIntoViewIfNeeded();await f.page.screenshot({path:shots+'/guest-which-machine-1440x1000.png'});
  await goTool(f,'guide');await f.page.screenshot({path:shots+'/guide-one-step-1440x1000.png'});
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
  await f.page.locator('#vmWaveBulk [data-bulk="clear"]').click();assert.equal(await f.page.locator('#vmWaveBulk .vm-bulk-count').textContent(),'Tick VMs to change many at once');
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
  assert.deepEqual(vmwareCalls(f,'receiver').map(c=>c.body),[{action:'start',folder:'',port:44390,firewall:true,discoverable:true}]);
  const where=await f.page.locator('#vmReceiveAddress').textContent();assert(where.includes('192.0.2.40:44390')&&where.includes('allowed in Windows Firewall'),where);
  assert(await f.page.locator('#vmReceiveStart').isHidden());
  await f.page.locator('#vmSendFind').click();await f.page.waitForFunction(()=>document.querySelectorAll('[data-send-disk]').length===2);
  assert((await f.page.locator('#vmSendDisks').textContent()).includes('Disk 0, 100 GB (Windows): no drive letter FAT32'));
  await f.page.locator('#vmSendAddress').fill('hv01.example.com');await f.page.locator('#vmSendThumbprint').fill('3F:9A:6C:0E 1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e');await f.page.locator('#vmSendCode').fill('K7QMZ-2RT4X');
  assert(await f.page.locator('#vmSendStart').isDisabled(),'this computer is receiving');
  await f.page.locator('#vmReceiveStop').click();await f.page.waitForFunction(()=>!document.querySelector('#vmReceiveIdle').classList.contains('hidden'));assert.deepEqual(vmwareCalls(f,'receiver').at(-1).body,{action:'stop'});
  await f.page.locator('[data-send-disk="1"]').uncheck();
  await f.page.locator('#vmSendStart').click();assert((await f.page.locator('#vmSendStatus').textContent()).includes('20-character pairing code'));assert.equal(vmwareCalls(f,'agent/send').length,0);
  await f.page.locator('#vmSendCode').fill('K7QMZ-2RT4X-9WBNP-H3VDA');
  await f.page.locator('#vmSendCheck').click();await f.page.waitForFunction(()=>document.querySelectorAll('[data-send-target]').length===1);
  assert.equal(await f.page.locator('[data-send-target="0"]').inputValue(),'disk-0','a raw receiver maps disks in order');
  assert((await f.page.locator('#vmSendReceiverNote').textContent()).includes('Writes 2 disks of the new VM or server'));
  await f.page.locator('[data-send-target="0"]').selectOption('disk-1');await f.page.locator('#vmSendLimit').fill('200');
  await f.page.locator('#vmSendStart').click();
  await f.page.waitForFunction(()=>document.querySelector('#vmwareTabBadge').textContent==='Working');
  assert.deepEqual(vmwareCalls(f,'agent/send').map(c=>c.body),[{disks:[0],address:'hv01.example.com',thumbprint:'3F9A6C0E1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e',code:'K7QMZ-2RT4X-9WBNP-H3VDA',verify:true,allowLive:false,targets:{'0':'disk-1'},compress:true,limitMbps:200}]);
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
  await goTool(f,'environment');await goTool(f,'diskcopy');
  assert(await f.page.locator('#vmFailover').isHidden(),'Start VMs from copies is its own tool');assert(await f.page.locator('#vmMediaCard').isVisible(),'receiver media is built in Receive and send disks');await goTool(f,'failover');
  await f.page.locator('#fvRefresh').click();await f.page.waitForFunction(()=>document.querySelectorAll('[data-fv-copy]').length===3);
  assert(await f.page.locator('[data-fv-copy="SQL01-disk0.vhdx"]').isDisabled(),'a copy being received cannot be chosen');
  assert((await f.page.locator('#fvCopies').textContent()).includes('being received now. It unlocks by itself when this round finishes'));
  // Finished copies of the newest round are ticked without a click, and the button says why it is still disabled.
  assert(await f.page.locator('[data-fv-copy="APP01-disk0.vhdx"]').isChecked());assert(await f.page.locator('[data-fv-copy="APP01-disk1.vhdx"]').isChecked());
  // A test VM is named NAME-TEST and confirmed for you; the button says why when something is missing.
  assert.equal(await f.page.locator('#fvConfirm').inputValue(),'APP01-TEST');assert.equal(await f.page.locator('#fvWhy').textContent(),'');
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
  const end=f.page.locator('[data-fv-end="f1"]');assert(await end.isDisabled());await f.page.locator('[data-fv-endname="f1"]').fill('APP01-TEST');assert(await end.isEnabled());
  await end.click();await f.page.waitForFunction(()=>document.querySelector('#fvRunning').classList.contains('hidden'));
  assert((await f.page.locator('#fvStatus').textContent()).includes('catches up'));
  assert((await f.page.locator('#fvNext').textContent()).includes('Next: cut over'));await f.page.locator('[data-fv-next="replicate"]').click();await f.page.waitForFunction(()=>!document.querySelector('#tabReplication').classList.contains('hidden'));
  assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});

 await check('Replication tab: Linux servers install over SSH, a new host key is shown and trusted only after the password is typed again',async()=>{const f=await fixture(browser);try{
  await goTool(f,'replicate');
  assert(await f.page.locator('#rpSshPortField').isHidden());await f.page.locator('#rpPlatform').selectOption('linux');
  assert(await f.page.locator('#rpSshPortField').isVisible());assert(await f.page.locator('#rpPushHelpLinux').isVisible());assert(await f.page.locator('#rpPushHelp').isHidden());
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
  await f.page.locator('#rpHosts').fill('app01.example.com, sql01.example.com');await f.page.locator('#rpUser').fill('EXAMPLE\\admin');
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
  await f.page.locator('#rpInterval').selectOption('5');await f.page.locator('#rpLimit').fill('100');await f.page.locator('#rpBusinessLimit').fill('20');await f.page.locator('#rpBusinessStart').fill('8');await f.page.locator('#rpBusinessEnd').fill('18');
  await f.page.locator('#rpServices').fill('MSSQLSERVER, W3SVC');await f.page.locator('#rpJobSave').click();
  await f.page.waitForFunction(()=>!document.querySelector('#rpDetails').classList.contains('hidden'));
  assert.deepEqual(f.replCalls.filter(c=>c.action==='job'&&c.body.action==='save').map(c=>c.body),[{agent:'ag1',action:'save',spec:{id:'',name:'APP01',disks:[{source:'0',target:'disk-0'},{source:'1',target:'disk-1'}],receiver:{address:'192.0.2.50',thumbprint:'3f9a6c0e1b7d4a25c8e0f13b6a9d2c4e7f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e',code:'K7QMZ-2RT4X-9WBNP-H3VDA'},intervalMinutes:5,rpoMinutes:15,compress:true,throttle:{limitMbps:100,businessMbps:20,businessStart:8,businessEnd:18},stopServices:['MSSQLSERVER','W3SVC'],shutdownAfterCutover:true,fullReadAtCutover:false,rescanFirst:false,allowLive:false}}]);
  assert(await f.page.locator('#rpEditor').isHidden());assert.equal(await f.page.locator('#rpRecvCode').inputValue(),'','the pairing code is cleared');
  await f.page.waitForFunction(()=>document.querySelector('#rpDetailsFacts').textContent.includes('Every 5 minutes'));
  assert((await f.page.locator('#rpAgents').textContent()).includes('Up to date'));
  assert(await f.page.locator('#rpCutover').isDisabled(),'cutover needs the typed name');
  await f.page.locator('#rpCutoverTyped').fill('app01');assert(await f.page.locator('#rpCutover').isEnabled());
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
 await check('New migration: one sequence of screens picks VMs, fills the target, maps networks, recommends the way, sets waves and timed cutovers, checks, and starts a migration plan with one typed name',async()=>{const f=await fixture(browser);try{
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();f.vm.inventory.vms[1].snapshots=[{name:'before patch',created:'2026-10-06T00:00:00Z'}];
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
  await f.page.locator('[data-nw-method="plans"]').click();await f.page.screenshot({path:out+'/new-migration-how-1440x1000.png'});await f.page.locator('#nwAdvanced summary').click();await f.page.locator('#nwSync').selectOption('30');
  await f.page.locator('#nwNext').click();
  const at=new Date(Date.now()+48*3600000),pad=n=>String(n).padStart(2,'0'),local=at.getFullYear()+'-'+pad(at.getMonth()+1)+'-'+pad(at.getDate())+'T22:00';
  const waves=await f.page.locator('[data-nw-cutmode]').evaluateAll(n=>n.map(x=>x.getAttribute('data-nw-cutmode')));assert(waves.length>=1);
  await f.page.locator('[data-nw-cutmode="'+waves[0]+'"]').selectOption('at');assert((await f.page.locator('#nwWhy').textContent()).startsWith('Choose the cutover time'));
  await f.page.locator('[data-nw-cutat="'+waves[0]+'"]').fill(local);
  await f.page.locator('[data-nw-wave="vm-sql01"]').selectOption(String(Math.max(...waves.map(Number))+1));await f.page.screenshot({path:out+'/new-migration-when-1440x1000.png'});
  await f.page.locator('#nwNext').click();
  assert((await f.page.locator('.nw-checks').textContent()).includes('VMware snapshots'),'snapshots are caught before Start');assert(await f.page.locator('#nwNext').isDisabled());
  await f.page.locator('#nwSnapshots').click();assert(await f.page.locator('#migrateDialog').isVisible());assert((await f.page.locator('#migrateConfirmLabel').textContent()).endsWith('1 VM'));await f.page.locator('#migrateDialogCancel').click();
  f.vm.inventory.vms[1].snapshots=[];f.state.vmware.inventorySerial=2;
  await f.page.waitForFunction(()=>!document.querySelector('#nwSnapshots'),null,{timeout:15000});
  {const t=await f.page.locator('.nw-summary').textContent();assert(t.includes('VM-Servers to Isolated VLAN 20'),t);}
  const name=await f.page.locator('#nwName').inputValue();assert(name.startsWith('Migration '));
  assert.equal(await f.page.locator('#nwWhy').textContent(),'Type the name again, '+name+', to start.');
  await f.page.locator('#nwConfirm').fill(name);await f.page.screenshot({path:out+'/new-migration-review-1440x1000.png'});await f.page.locator('#nwNext').click();
  await f.page.waitForFunction(()=>document.querySelector('.nw-stepper-item.current')&&document.querySelector('.nw-stepper-item.current').textContent==='6Status',null,{timeout:15000});
  const saved=f.vmwareCalls.filter(c=>c.action==='plan'&&c.method==='POST').pop();const net=saved.body.plan.networks.find(n=>n.source==='VM-Servers');assert.equal(net.targetName,'Isolated');assert.equal(net.targetVlan,'20');
  assert(saved.body.plan.vms.find(v=>v.id==='vm-sql01').manual,'a wave changed here is kept by later suggestions');
  const created=f.planCalls.find(c=>c.body.action==='create');assert.deepEqual(created.body.vmIds.sort(),['vm-app01','vm-sql01']);assert.equal(created.body.settings.copyRoot,'D:\\Hyper-V\\Disks');assert.equal(created.body.settings.syncMinutes,30);assert.equal(created.body.settings.disconnectSource,true);
  const cut=f.planCalls.find(c=>c.body.action==='cutover');assert(cut,'the timed cutover is scheduled at Start');assert.equal(cut.body.confirm,name);assert.equal(cut.body.vmIds.length,1);assert.equal(new Date(cut.body.at).getHours(),22);
  await f.page.waitForSelector('[data-nw-filter="total"]');assert((await f.page.locator('#nwStatusHead').textContent()).includes(name));
  assert(await f.page.locator('#nwCutNow').isDisabled());
  f.plans.plans[0].vms[0]=Object.assign(f.plans.plans[0].vms[0],{state:'ready',seeded:true,wave:1});f.plans.plans[0].vms[1]=Object.assign(f.plans.plans[0].vms[1],{state:'copying',wave:2,live:{percent:40,bytesRead:42949672960,bytesPerSecond:104857600,secondsLeft:600,elapsed:400}});f.plans.plans[0].summary=Object.assign(f.plans.plans[0].summary,{waiting:0,copying:1,ready:1});
  await f.page.waitForFunction(()=>document.querySelector('#nwStatusBody').textContent.includes('40%'),null,{timeout:15000});
  await f.page.locator('[data-nw-filter="copying"]').click();assert.equal(await f.page.locator('#nwStatusBody [data-nw-row]').count(),1,'tiles filter the list');await f.page.locator('[data-nw-filter="copying"]').click();
  await f.page.screenshot({path:out+'/new-migration-status-1440x1000.png'});
  await f.page.locator('[data-nw-row="'+f.plans.plans[0].vms[0].id+'"]').check();assert((await f.page.locator('#nwStatusMsg').textContent()).startsWith('Type the name'));
  await f.page.locator('#nwCutConfirm').fill(name);await f.page.locator('#nwCutNow').click();
  for(let i=0;i<100&&f.planCalls.filter(c=>c.body.action==='cutover').length<2;i++){await new Promise(r=>setTimeout(r,50));}const now=f.planCalls.filter(c=>c.body.action==='cutover').pop();assert.equal(now.body.at,'now');assert.equal(now.body.confirm,name);
  await f.page.setViewportSize({width:1008,height:690});await f.page.locator('[data-nw-step]').first().waitFor();
  assert(await f.page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth+1),'no horizontal scroll at 1024x768');
 }finally{await f.close();}});
 await check('New migration: a target this host cannot run hands off to its tool with the VMs selected',async()=>{const f=await fixture(browser);try{
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',summary:vmwareSummaryFixture(),inventorySerial:1,canPrepare:true,hyperVHere:false});
  await goTool(f,'wizard');await f.page.locator('[data-nw-source="vms"]').click();await f.page.waitForSelector('#nwVmList [data-nw-pick]');await f.page.locator('[data-nw-pick="vm-app01"]').check();await f.page.locator('#nwNext').click();
  assert(await f.page.locator('[data-nw-target="here"]').isDisabled(),'this host is not a Hyper-V host');assert((await f.page.locator('[data-nw-target="here"]').textContent()).includes('is not a Hyper-V host'));
  await f.page.locator('[data-nw-target="proxmox"]').click();await f.page.locator('#nwNext').click();
  assert.equal(await f.page.locator('.nw-stepper-item').count(),3,'a hand-off ends at How');
  assert.equal(await f.page.locator('#nwNext').textContent(),'Continue in Move VMs');await f.page.locator('#nwNext').click();
  assert.equal(await f.page.locator('[data-tool="movevms"]').getAttribute('aria-pressed'),'true');assert.equal(await f.page.locator('#vmTargetSelect').inputValue(),'proxmox','the target comes along');
  assert(await f.page.locator('#vmDetail').isVisible(),'the VM opens in Move VMs');
 }finally{await f.close();}});
 await check('New migration: with no inventory the sign-in is on the first screen',async()=>{const f=await fixture(browser);try{
  await goTool(f,'wizard');await f.page.locator('[data-nw-source="vms"]').click();
  await f.page.locator('#nwServer').fill('vc01.example.com');await f.page.locator('#nwUser').fill('administrator@vsphere.local');await f.page.locator('#nwConnect').click();assert.equal(await f.page.locator('#nwConnectStatus').textContent(),'Type the password for administrator@vsphere.local.');
  await f.page.locator('#nwPassword').fill('secret');await f.page.locator('#nwPassword').press('Enter');
  for(let i=0;i<100&&!f.vmwareCalls.some(c=>c.action==='connect');i++){await new Promise(r=>setTimeout(r,50));}const call=f.vmwareCalls.find(c=>c.action==='connect');assert(call&&call.body.server==='vc01.example.com');
  assert.equal(await f.page.evaluate(()=>{const p=document.querySelector('#nwPassword');return p?p.value:'';}),'','the password is cleared from the page');
 }finally{await f.close();}});
 await check('Migration plans: VMs are picked from the inventory, tiles filter the list, live progress shows speed and time left, and cutover needs selected in-sync VMs and the typed plan name',async()=>{const f=await fixture(browser);try{
  f.vm.inventory=vmwareInventoryFixture();f.vm.plan=vmwarePlanFixture();f.state.vmware=Object.assign(vmwareIdle(),{status:'connected',server:'vc01.example.com',summary:vmwareSummaryFixture(),inventorySerial:1});
  await goTool(f,'plans');await f.page.waitForSelector('#mpNewButton');
  assert((await f.page.locator('[data-tool="plans"] .tool-works').textContent()).includes('this Hyper-V host or its failover cluster'));
  assert((await f.page.locator('.mp-when').textContent()).includes('Replicate servers'),'the pane says when to use it and when not');
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
   {id:'vm-app01',name:'APP01',wave:1,state:'ready',seeded:true,lastSync:now,lastMode:'changed',lastRead:1073741824,lastWritten:268435456,lastSeconds:95,nextSync:now,failures:0},
   {id:'vm-sql01',name:'SQL01',wave:1,state:'copying',seeded:false,live:{percent:40,bytesRead:42949672960,bytesPerSecond:104857600,secondsLeft:600,elapsed:400},failures:0},
   {id:'vm-web01',name:'WEB01',wave:2,state:'failed',seeded:true,error:'<b>datastore unreachable</b>',failures:2}]});
  await f.page.waitForFunction(()=>document.querySelectorAll('#mpRows tr').length===3,null,{timeout:15000});
  assert((await f.page.locator('#mpRows').textContent()).includes('Seeding 40%'));assert((await f.page.locator('#mpRows .mp-progress-text').textContent()).includes('100 MB/s · 10 min left'));
  assert.equal(await f.page.locator('#mpRows .mp-err').textContent(),'<b>datastore unreachable</b>','errors are text, not markup');
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
  if(!f.errors.length&&!f.dialogs.length){await f.page.setViewportSize({width:1440,height:1000});await f.page.locator('.mp-body').evaluate(e=>e.scrollTop=0);await f.page.screenshot({path:out+'/migration-plans.png'});}
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
  await goTool(f,'guide');assert.equal(await f.page.locator('#gdHere [data-gd-fact]').count(),5);await f.page.locator('#gdHere [data-gd-fact="diskcopy"]').click();assert.equal(await f.page.locator('[data-tool="diskcopy"]').getAttribute('aria-pressed'),'true');
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
   await goTool(f,'environment');await f.page.locator('#vmwareBody').evaluate(e=>e.scrollTop=0);await inView('#vmwareBody',withUpdate?110:150);await inView('#vmServer',24);
   for(const view of ['env','plan','guest','disks','failover','compare']){await goTool(f,{env:'movevms',disks:'diskcopy'}[view]||view);await inView('[data-tool="'+({env:'movevms',disks:'diskcopy'}[view]||view)+'"]',24);
    for(const target of {env:['#vmInvTree .vm-inv-row','#vmDrawerTitle','.vm-obj-tab'],plan:['#vmSuggest','#vmWriteRunbook'],guest:['#vmGuestCapture','#vmGuestApply','#vmGuestTools'],disks:['#vmReceiveStart','#vmSendFind','#vmSendStart','#vmMediaBuild'],failover:['#fvRefresh'],compare:['#vmCompareRun','#vmCompareBody tr']}[view]){await f.page.locator(target).first().scrollIntoViewIfNeeded();await inView(target+':visible >> nth=0',20);}
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
   await goTool(f,'hvbuild');await f.page.locator('.hb-body').evaluate(e=>e.scrollTop=0);await inView('#hbBlank',24);
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
