/* Actual application HTML and production CSP, synthetic state only. */
const fs=require('fs'),http=require('http'),assert=require('assert'),crypto=require('crypto');
assert.equal(crypto.createHash('sha256').update(fs.readFileSync('tests/fixtures/index-v3.2.9.html')).digest('hex'),'b7145c6b9d0a83949606d10d3671815ba7e43e5f894bca24df6430c8c1ced609','Published negative-control fixture changed');
const {chromium}=require('playwright');
const out='build/workspace-validation';fs.mkdirSync(out,{recursive:true});
const report={status:'running',checks:[]};
const policy=fs.readFileSync('src/app/main.go','utf8').match(/applicationPageCSP\s*=\s*"([^"]+)"/)[1];
const args=process.argv.slice(2),executable=args.includes('--executable')?args[args.indexOf('--executable')+1]:null;
async function fixture(browser,old=false){
 let saved=null,started=[],saveCalls=0,rejectStart=false,skipRequests=[];
 const logs=[],dialogs=[],errors=[];
 const state={status:'idle',phase:'Ready',running:false,computerName:'SRV-DEMO-01',isAdministrator:true,powerShellVersion:'5.1',defaultOutputRoot:'C:\\Reports',update:{status:'current',title:'Up to date',message:'Installed v3.2.10. Latest v3.2.10.',canCheck:true}};
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
  if(url.pathname==='/api/skip-acl-export'){let body='';for await(const chunk of req)body+=chunk;skipRequests.push(JSON.parse(body));state.aclExport.skipRequested=true;send(202,{ok:true});return;}
  const icon={'/app-icon.svg':'assets/icons/server-inventory.svg','/favicon.ico':'favicon.ico','/app-icon.png':'app-icon.png'}[url.pathname];
  if(icon){res.setHeader('Content-Type',icon.endsWith('.svg')?'image/svg+xml':'image/png');res.end(fs.readFileSync('src/app/'+icon));return;}
  send(200,{ok:true});
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const context=await browser.newContext({viewport:{width:1440,height:1000}}),page=await context.newPage();
 page.on('dialog',async d=>{dialogs.push(d.message());await d.dismiss();});page.on('pageerror',e=>errors.push(e.message));
 const url='http://127.0.0.1:'+server.address().port+'/';await page.goto(url);await page.waitForFunction(()=>document.querySelector('#serverName').textContent==='SRV-DEMO-01');
 return {page,context,state,logs,dialogs,errors,started,skipRequests,get saved(){return saved},get saveCalls(){return saveCalls},reject(){rejectStart=true},async close(){assert.deepEqual(errors,[]);await context.close();await new Promise(r=>server.close(r));}};
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
   await f.page.locator('#tabButtonSetup').focus();await f.page.keyboard.press('ArrowRight');assert.equal(await f.page.locator('#tabButtonCollection').getAttribute('aria-selected'),'true');await f.page.keyboard.press('End');assert.equal(await f.page.locator('#tabButtonReport').getAttribute('aria-selected'),'true');await f.page.keyboard.press('Home');assert.equal(await f.page.locator('#tabButtonSetup').getAttribute('aria-selected'),'true');
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
 await check('Accepted collection submits the selected scope once and keeps start/save locked',async()=>{const f=await fixture(browser);try{
  await f.page.locator('#presetCore').click();await f.page.locator('#startButton').click();await f.page.waitForTimeout(1100);assert.equal(f.started.length,1);assert.equal(f.started[0].inventoryCategories.length,12);assert.equal(f.started[0].exportCategories.length,0);assert(await f.page.locator('#tabCollection').isVisible());assert(await f.page.locator('#startButton').isDisabled());assert(await f.page.locator('#saveChoicesButton').isDisabled());assert.deepEqual(f.dialogs,[]);
 }finally{await f.close();}});
 report.status='passed';
 }finally{await browser.close();}})().catch(e=>{report.status='failed';report.error=e.stack;console.error(e);process.exitCode=1;}).finally(()=>fs.writeFileSync(out+'/results.json',JSON.stringify(report,null,2)));
