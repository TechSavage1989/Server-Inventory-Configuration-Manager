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
async function fixture(browser,old=false){
 let saved=null,started=[],saveCalls=0,rejectStart=false,skipRequests=[],opens=[],restoreReject=null;
 const logs=[],dialogs=[],errors=[],restoreCalls={browse:0,scan:[],start:[],cancel:0,tokens:[]};
 const state={status:'idle',phase:'Ready',running:false,computerName:'SRV-DEMO-01',isAdministrator:true,powerShellVersion:'5.1',defaultOutputRoot:'C:\\Reports',update:{status:'current',title:'Up to date',message:'Installed v3.2.10. Latest v3.2.10.',canCheck:true},
  restore:{status:'idle',packages:{lastCollection:'',loadedReport:''},items:[],logs:[]}};
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
 return {page,context,state,logs,dialogs,errors,started,skipRequests,opens,restoreCalls,rejectRestore(message){restoreReject=message},get saved(){return saved},get saveCalls(){return saveCalls},reject(){rejectStart=true},async close(){assert.deepEqual(errors,[]);await context.close();await new Promise(r=>server.close(r));}};
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
   await f.page.locator('#tabButtonSetup').focus();await f.page.keyboard.press('ArrowRight');assert.equal(await f.page.locator('#tabButtonCollection').getAttribute('aria-selected'),'true');await f.page.keyboard.press('End');assert.equal(await f.page.locator('#tabButtonRestore').getAttribute('aria-selected'),'true');await f.page.keyboard.press('ArrowLeft');assert.equal(await f.page.locator('#tabButtonReport').getAttribute('aria-selected'),'true');await f.page.keyboard.press('Home');assert.equal(await f.page.locator('#tabButtonSetup').getAttribute('aria-selected'),'true');
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
  assert.equal(await f.page.title(),'Server Inventory Configuration Manager');
  f.state.running=true;f.state.status='running';f.state.progress=42;await f.page.waitForTimeout(1100);assert.equal(await f.page.title(),'42% Collecting \u2014 Server Inventory Configuration Manager');
  f.state.running=false;f.state.status='success';f.state.packagePath='C:\\Reports\\SRV';f.state.zipPath='C:\\Reports\\SRV.zip';await f.page.waitForTimeout(1100);
  const title=(await f.page.title()).toLowerCase();assert(title.includes('server inventory')&&title.includes('configuration manager')&&title.startsWith('collection complete'));
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
  assert.equal(await f.page.evaluate(()=>document.activeElement.id),'tabButtonRestore');await f.page.keyboard.press('ArrowRight');assert.equal(await f.page.locator('#tabButtonSetup').getAttribute('aria-selected'),'true');await f.page.keyboard.press('ArrowLeft');assert.equal(await f.page.locator('#tabButtonRestore').getAttribute('aria-selected'),'true');
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
 await check('Low-resolution windows (800x600 and 1024x768 screens) keep every workspace reachable and legible',async()=>{const f=await fixture(browser);try{
  for(let i=0;i<40;i++)f.logs.push('[INFO] Message '+i);
  f.state.restore={status:'ready',packages:{lastCollection:'C:\\Reports\\SRV-DEMO-01',loadedReport:''},packagePath:'C:\\Reports\\SRV-DEMO-01',scan:restoreScanFixture(),items:[],logs:[]};
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
   const small=await f.page.evaluate(()=>Array.from(document.querySelectorAll('body *')).filter(e=>e.offsetParent&&e.childNodes.length&&Array.from(e.childNodes).some(n=>n.nodeType===3&&n.nodeValue.trim())&&parseFloat(getComputedStyle(e).fontSize)<10.5).map(e=>e.className||e.id||e.tagName));assert.deepEqual(small,[],label+' text below 10.5px');
   if(size.width===784&&!withUpdate){await f.page.locator('#tabButtonCollection').click();await f.page.screenshot({path:out+'/collection-800x600.png'});await f.page.locator('#tabButtonSetup').click();await f.page.screenshot({path:out+'/setup-800x600.png'});}
  }}
 }finally{await f.close();}});
 report.status='passed';
 }finally{await browser.close();}})().catch(e=>{report.status='failed';report.error=e.stack;console.error(e);process.exitCode=1;}).finally(()=>fs.writeFileSync(out+'/results.json',JSON.stringify(report,null,2)));
