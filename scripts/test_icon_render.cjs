/* Use the real page, SVG and production CSP. Catch image-policy failures before publication. */
const fs=require('fs'),http=require('http'),assert=require('assert');
const {chromium}=require('playwright');
const report={checks:[],status:'running'};
const args=process.argv.slice(2),executable=args.includes('--executable')?args[args.indexOf('--executable')+1]:null;
const policy=fs.readFileSync('src/app/main.go','utf8').match(/applicationPageCSP\s*=\s*"([^"]+)"/)[1];
const html=fs.readFileSync('src/app/index.html','utf8').replaceAll('__TOKEN__','icon-test').replaceAll('__APP_VERSION__','3.2.7').replaceAll('__COLLECTOR_VERSION__','3.2.7');
async function fixture(browser,csp){
 const server=http.createServer((req,res)=>{
  const route=req.url.split('?')[0];
  if(route==='/'){res.setHeader('Content-Type','text/html');res.setHeader('Content-Security-Policy',csp);res.end(html);return}
  if(route==='/app-icon.svg'){res.setHeader('Content-Type','image/svg+xml');res.end(fs.readFileSync('src/app/assets/icons/server-inventory.svg'));return}
  if(route==='/favicon.ico'||route==='/app-icon.png'){res.setHeader('Content-Type',route.endsWith('.ico')?'image/x-icon':'image/png');res.end(fs.readFileSync('src/app/'+route.slice(1)));return}
  if(route==='/api/state'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify({appVersion:'3.2.7',logs:[],nextLogIndex:0,status:'idle',running:false,update:{status:'current',title:'Up to date',installedVersion:'3.2.7',latestVersion:'3.2.7',canCheck:true,notes:[]}}));return}
  res.setHeader('Content-Type','application/json');res.end('{}');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const context=await browser.newContext(),page=await context.newPage();
 const violations=[];page.on('console',m=>{if(/img-src|Content Security Policy/i.test(m.text()))violations.push(m.text())});
 await page.goto('http://127.0.0.1:'+server.address().port+'/');
 return {page,violations,context,async close(){await context.close();await new Promise(r=>server.close(r))}};
}
async function check(name,fn){await fn();report.checks.push({name,result:'passed'});console.log('PASS: '+name)}
(async()=>{
 const browser=await chromium.launch(executable?{headless:true,executablePath:executable,args:process.platform==='linux'&&process.getuid?.()===0?['--no-sandbox']:[]}:{channel:'msedge',headless:true});
 try{
  await check('Published 3.2.6 image policy reproduces a blocked header SVG',async()=>{
   const f=await fixture(browser,policy.replace("img-src 'self' data:;","img-src data:;"));try{
    await f.page.waitForTimeout(300);const image=await f.page.locator('.brand-mark img').evaluate(i=>({complete:i.complete,width:i.naturalWidth}));
    assert(image.complete&&image.width===0);assert(f.violations.length>0);report.negativeControlBlocked=true;
   }finally{await f.close()}
  });
  const f=await fixture(browser,policy);try{
   await check('Production policy renders the actual local SVG header image',async()=>{
    await f.page.locator('.brand-mark img').evaluate(i=>i.decode());
    const image=await f.page.locator('.brand-mark img').evaluate(i=>({complete:i.complete,width:i.naturalWidth,height:i.naturalHeight,path:new URL(i.currentSrc).pathname}));
    assert(image.complete&&image.width===512&&image.height===512&&image.path==='/app-icon.svg');report.renderedImage=image;
    await f.page.screenshot({path:'build/icon-header-validation.png',clip:{x:0,y:0,width:800,height:80}});
   });
   await check('Remote images remain blocked by the production policy',async()=>{
    let requests=0;await f.context.route('https://example.invalid/**',route=>{requests++;return route.fulfill({status:200,body:''})});
    const blocked=await f.page.evaluate(()=>new Promise(resolve=>{const img=new Image();img.onload=()=>resolve(false);img.onerror=()=>resolve(true);img.src='https://example.invalid/remote-icon-probe.png';document.body.appendChild(img)}));
    assert(blocked&&requests===0);report.remoteImagesBlocked=true;
   });
  }finally{await f.close()}
  report.status='passed';
 }finally{await browser.close()}
})().catch(e=>{report.status='failed';report.error=e.stack;console.error(e);process.exitCode=1}).finally(()=>{fs.mkdirSync('build',{recursive:true});fs.writeFileSync('build/icon-render-regression.json',JSON.stringify(report,null,2))});
