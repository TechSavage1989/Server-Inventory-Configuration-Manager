/* Regression test against the actual v3.2.3 HTML and the fixed HTML.
   Exercises failed XHRs, focus storms, in-flight requests and install rejection.
   Unexpected dialogs are recorded as failures, never silently accepted. */
const fs=require('fs'),path=require('path'),http=require('http'),assert=require('assert');
const {chromium}=require('playwright');
const report={checks:[],status:'running'};
const args=process.argv.slice(2);const executable=args.includes('--executable')?args[args.indexOf('--executable')+1]:null;
async function fixture(browser,html,mode){
 let accepted=false,after=0,checks=0,states=0;
 const update={status:'ready',title:'Update ready',message:'A verified update is ready.',installedVersion:'3.2.4',latestVersion:'99.0.0',available:true,canCheck:true,canDownload:true,canInstall:true,downloaded:true,notes:[]};
 const server=http.createServer((req,res)=>{
  if(accepted&&req.url!=='/api/update/install'){after++;req.socket.destroy();return}
  const send=(code,body)=>{res.writeHead(code,{'Content-Type':'application/json'});res.end(JSON.stringify(body))};
  if(req.url.startsWith('/api/state')){states++;send(200,{appVersion:'3.2.4',collectorVersion:'3.2.4',computerName:'UI regression',isAdministrator:true,defaultOutputRoot:'C:\\Reports',phase:'Ready',status:'idle',running:false,logs:[],nextLogIndex:0,update});return}
  if(req.url==='/api/ui/ready'){send(200,{ok:true});return}
  if(req.url==='/api/update/check'){checks++;if(mode==='manual-error'){send(503,{error:'The update action is unavailable.'})}else{setTimeout(()=>{if(!res.destroyed){send(202,{ok:true})}},1200)}return}
  if(req.url==='/api/update/install'){if(mode==='reject'){send(409,{error:'A critical operation is running.'})}else{send(202,{ok:true});accepted=true}return}
  if(req.url==='/'){res.writeHead(200,{'Content-Type':'text/html'});res.end(html.replaceAll('__TOKEN__','test-token').replaceAll('__APP_VERSION__','3.2.4').replaceAll('__COLLECTOR_VERSION__','3.2.4'));return}
  res.writeHead(204);res.end();
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const context=await browser.newContext();const page=await context.newPage();const alerts=[];
 page.on('dialog',async dialog=>{if(dialog.type()==='confirm'&&dialog.message().startsWith('Install ')){await dialog.accept();return}alerts.push({type:dialog.type(),message:dialog.message()});await dialog.dismiss()});
 await page.goto('http://127.0.0.1:'+server.address().port+'/');await page.locator('#updateInstallButton').waitFor({state:'visible'});
 return {page,alerts,get after(){return after},get checks(){return checks},get states(){return states},get accepted(){return accepted},async close(){await context.close();await new Promise(r=>server.close(r))}};
}
async function check(name,fn){await fn();report.checks.push({name,result:'passed'});process.stdout.write('PASS: '+name+'\n')}
(async()=>{
 const browser=await chromium.launch(executable?{headless:true,executablePath:executable,args:process.platform==='linux'&&process.getuid?.()===0?['--no-sandbox']:[]}:{channel:'msedge',headless:true});
 try{
  const previous=fs.readFileSync('tests/fixtures/index-v3.2.3.html','utf8'),current=fs.readFileSync('src/app/index.html','utf8');
  await check('Published v3.2.3 negative control reproduces the status-0 popup after shutdown',async()=>{const f=await fixture(browser,previous,'accept');try{
   await f.page.locator('#updateInstallButton').click();await f.page.waitForTimeout(150);
   for(let i=0;i<4;i++){await f.page.evaluate(()=>window.dispatchEvent(new Event('focus')));await f.page.waitForTimeout(80)}
   await f.page.waitForTimeout(1100);assert(f.alerts.some(d=>d.message.includes('Request failed with status 0')),JSON.stringify(f.alerts));assert(f.after>0);report.negativeControlDialogs=f.alerts;
  }finally{await f.close()}});
  await check('Fixed page stops polling, aborts in-flight XHRs, and ignores focus/visibility after install acceptance',async()=>{const f=await fixture(browser,current,'accept');try{
   await f.page.evaluate(()=>window.dispatchEvent(new Event('focus')));await f.page.locator('#updateInstallButton').click();await f.page.waitForTimeout(150);
   for(let i=0;i<12;i++){await f.page.evaluate(()=>{window.dispatchEvent(new Event('focus'));document.dispatchEvent(new Event('visibilitychange'))});await f.page.waitForTimeout(35)}
   await f.page.waitForTimeout(1600);assert.equal(f.alerts.length,0,JSON.stringify(f.alerts));assert.equal(f.after,0,'network activity continued after accepted install');assert(f.accepted);
  }finally{await f.close()}});
  await check('Failed manual update action is shown inline with zero modal alerts',async()=>{const f=await fixture(browser,current,'manual-error');try{
   await f.page.locator('#updateCheckButton').click();await f.page.waitForTimeout(1400);assert.equal(f.alerts.length,0);assert((await f.page.locator('#updateStatus').textContent()).includes('unavailable'));assert((await f.page.locator('#updateBanner').getAttribute('class')).includes('error'));
  }finally{await f.close()}});
  await check('Rejected installation restores the controls and active state polling without modal errors',async()=>{const f=await fixture(browser,current,'reject');try{
   const before=f.states;await f.page.locator('#updateInstallButton').click();await f.page.waitForTimeout(1500);assert.equal(f.alerts.length,0);assert(f.states>before);assert(await f.page.locator('#startButton').isEnabled());assert((await f.page.locator('#updateStatus').textContent()).includes('critical operation'));
  }finally{await f.close()}});
  report.status='passed';
 }finally{await browser.close()}
})().catch(e=>{report.status='failed';report.error=e.stack;process.stderr.write(e.stack+'\n');process.exitCode=1}).finally(()=>{fs.mkdirSync('build',{recursive:true});fs.writeFileSync('build/ui-shutdown-regression.json',JSON.stringify(report,null,2))});
