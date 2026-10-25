/* Hosted Windows test: published v3.2.4, real UI actions, public feed, native helper,
   visible new app health, settings, shortcut, timer, and failed-health rollback.
   Playwright drives the real application page while the native Edge app is visible.
   This does not simulate UAC consent or a user's physical desktop interaction. */
const fs=require('fs'),fsp=fs.promises,path=require('path'),os=require('os'),crypto=require('crypto');
const {spawn,execFileSync}=require('child_process');
const {chromium}=require('playwright');
const expected=fs.readFileSync('main.go','utf8').match(/appVersion\s*=\s*"([\d.]+)"/)[1];
const repo='https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager';
const oldVersion='3.2.4',exeName='Server-Inventory-Configuration-Manager.exe';
const report={expected,previous:oldVersion,started:new Date().toISOString(),checks:[],limitations:['UAC consent transitions were not exercised.','Playwright drives the application page alongside its visible native Edge window; physical desktop interaction was not performed.','Offline, HTTP failure, rate limiting, ETag and malicious-file cases use deterministic native Go tests.','Legacy Windows Server versions and every server-role collector were not exercised.']};
const suiteRoot=path.resolve('build','published-upgrade-work');
const tempRoot=path.join(os.tmpdir(),'SICM-Published-'+crypto.randomUUID());
let browser;let page;let installingLegacy=false;let confirmingInstall=false;const unexpectedDialogs=[];const legacyShutdownDialogs=[];
function check(condition,message){if(!condition)throw Error(message);report.checks.push({name:message,result:'passed',time:new Date().toISOString()});console.log('PASS: '+message);}
async function until(fn,timeout=60000){const deadline=Date.now()+timeout;let last;while(Date.now()<deadline){try{const v=await fn();if(v)return v;}catch(e){last=e;}await new Promise(r=>setTimeout(r,250));}throw Error('Timed out: '+(last?.message||fn.toString().slice(0,140)));}
async function get(url){const r=await fetch(url);if(!r.ok)throw Error('HTTP '+r.status+' '+url);return Buffer.from(await r.arrayBuffer());}
const sha=b=>crypto.createHash('sha256').update(b).digest('hex');
function envFor(name){const data=path.join(tempRoot,name);fs.mkdirSync(path.join(data,'Temp'),{recursive:true});return {...process.env,LOCALAPPDATA:path.join(data,'LocalAppData'),TEMP:path.join(data,'Temp'),TMP:path.join(data,'Temp')};}
const appRoot=e=>path.join(e.LOCALAPPDATA,'Server Inventory Configuration Manager');
function powershell(text){return execFileSync('powershell.exe',['-NoLogo','-NoProfile','-NonInteractive','-Command',text],{encoding:'utf8'}).trim();}
const psQuote=s=>"'"+s.replaceAll("'","''")+"'";
function nativeBrowserProfile(env,url){return path.join(appRoot(env),'Runtime','Browser-'+new URL(url).searchParams.get('token'));}
function nativeBrowserProcesses(profile){
 const raw=powershell("ConvertTo-Json -Compress -InputObject @(Get-CimInstance Win32_Process -Filter \"Name='msedge.exe' OR Name='chrome.exe'\" | Select-Object ProcessId,CommandLine)");
 const escaped=profile.replace(/[.*+?^${}()|[\]\\]/g,'\\$&');
 const match=new RegExp('(?:^|\\s)"?--user-data-dir=(?:"'+escaped+'"|'+escaped+'(?:"|(?=\\s|$)))','i');
 return JSON.parse(raw||'[]').filter(p=>p.CommandLine&&match.test(p.CommandLine));
}

function nativeWindowGeometry(profile,move=false){
 const ids=nativeBrowserProcesses(profile).map(p=>p.ProcessId);if(!ids.length)return null;
 const source=`using System;using System.Runtime.InteropServices;
 public static class SicmGeometryProbe {
 [StructLayout(LayoutKind.Sequential)] public struct Point {public int X,Y;}
 [StructLayout(LayoutKind.Sequential)] public struct Rect {public int Left,Top,Right,Bottom;}
 [StructLayout(LayoutKind.Sequential)] public struct Placement {public uint Length,Flags,ShowCmd;public Point Min,Max;public Rect Bounds;}
 [DllImport("user32.dll")] public static extern bool GetWindowPlacement(IntPtr h,ref Placement p);
 [DllImport("user32.dll")] public static extern bool SetWindowPlacement(IntPtr h,ref Placement p);
 }`;
 return JSON.parse(powershell('Add-Type -TypeDefinition '+psQuote(source)+';$p=Get-Process -Id '+ids.join(',')+' -ErrorAction SilentlyContinue | Where-Object {$_.MainWindowHandle -ne 0 -and $_.MainWindowTitle -like "*Server Inventory Configuration Manager*"} | Select-Object -First 1;if(-not $p){"null";exit};$w=New-Object SicmGeometryProbe+Placement;$w.Length=44;if(-not [SicmGeometryProbe]::GetWindowPlacement($p.MainWindowHandle,[ref]$w)){throw "Window probe failed"};'+(move?'$w.Flags=4;$w.ShowCmd=1;$w.Bounds.Left=87;$w.Bounds.Top=63;$w.Bounds.Right=987;$w.Bounds.Bottom=663;if(-not [SicmGeometryProbe]::SetWindowPlacement($p.MainWindowHandle,[ref]$w)){throw "Window move failed"};':'')+'ConvertTo-Json -Compress -InputObject @{Left=$w.Bounds.Left;Top=$w.Bounds.Top;Right=$w.Bounds.Right;Bottom=$w.Bounds.Bottom;ShowCmd=$w.ShowCmd}'));
}
async function urlFor(env,old=false,prior=''){
 const p=old?path.join(env.TEMP,'Server-Inventory-Configuration-Exporter-URL.txt'):path.join(appRoot(env),'Runtime','application-url.txt');
 return until(async()=>{const u=(await fsp.readFile(p,'utf8')).trim();if(u===prior)return false;const r=await fetch(new URL('/api/state'+new URL(u).search,u));return r.ok?u:false;});
}
async function state(url){return (await fetch(new URL('/api/state'+new URL(url).search,url))).json();}
async function stopApp(env){try{const h=JSON.parse(await fsp.readFile(path.join(appRoot(env),'Health','current.json'),'utf8'));execFileSync('taskkill.exe',['/PID',String(h.pid),'/T','/F'],{stdio:'ignore'});}catch{}}
async function seedSettings(env){const p=path.join(appRoot(env),'Settings','settings.json');await fsp.mkdir(path.dirname(p),{recursive:true});const output=path.join(suiteRoot,'User Reports');await fsp.mkdir(output,{recursive:true});const b=Buffer.from(JSON.stringify({output_root:output},null,2));await fsp.writeFile(p,b);return {path:p,bytes:b,output};}
async function nativeProcess(exe,args,env){const child=spawn(exe,args,{env,windowsHide:true,stdio:'ignore'});return new Promise((resolve,reject)=>{child.on('error',reject);child.on('exit',resolve);});}
(async()=>{
 await fsp.mkdir(suiteRoot,{recursive:true});await fsp.mkdir(tempRoot,{recursive:true});
 const manifest=JSON.parse((await get(repo+'/releases/latest/download/update-manifest.json')).toString('utf8').replace(/^\uFEFF/,''));
 check(manifest.version===expected,'Public updater feed selects the expected new release');
 const oldBytes=await get(repo+'/releases/download/v'+oldVersion+'/'+exeName);
 check(sha(oldBytes)==='6319f6ad5c2b15882110b4a34ba63f99812be879166de3a754b70e794c1c4ab2','Previous executable matches the published v3.2.4 checksum');
 const newBytes=await get(manifest.asset.url);check(sha(newBytes)===manifest.asset.sha256,'Published new executable matches its manifest checksum');
 const env=envFor('upgrade');const saved=await seedSettings(env);const target=path.join(suiteRoot,exeName);await fsp.writeFile(target,oldBytes);
 const shortcut=path.join(suiteRoot,'Server Inventory Configuration Manager.lnk');
 powershell('$s=(New-Object -ComObject WScript.Shell).CreateShortcut('+psQuote(shortcut)+');$s.TargetPath='+psQuote(target)+';$s.WorkingDirectory='+psQuote(suiteRoot)+';$s.IconLocation='+psQuote(target+',0')+';$s.Save()');
 spawn('powershell.exe',['-NoProfile','-NonInteractive','-Command','Start-Process -FilePath '+psQuote(shortcut)],{env,stdio:'ignore'});
 const oldURL=await urlFor(env,oldVersion==='3.2.0');check((await state(oldURL)).appVersion===oldVersion,'Previous executable starts through the desktop shortcut');
 browser=await chromium.launch({channel:'msedge',headless:true});page=await browser.newPage({viewport:{width:1440,height:1000}});
 page.on('dialog',async d=>{
  if(confirmingInstall&&d.type()==='confirm'&&d.message().startsWith('Install ')){confirmingInstall=false;await d.accept();return}
  // The unmodified published legacy HTML can alert during shutdown. Record this
  // known regression explicitly; no dialog from the fixed page is exempted.
  if(installingLegacy&&d.type()==='alert'&&d.message()==='Request failed with status 0.'){legacyShutdownDialogs.push(d.message());await d.dismiss();return}
  unexpectedDialogs.push({type:d.type(),message:d.message()});await d.dismiss();
 });
 await page.goto(oldURL);await page.locator('#updateDownloadButton').waitFor({state:'visible',timeout:60000});
 await until(async()=>{const s=await state(oldURL);return s.update.available&&s.update.canDownload});
 check((await state(oldURL)).update.latestVersion===expected,'Previous application automatically detects the newly published release');
 const ready=path.join(appRoot(env),'Updates','Ready',exeName);
 check(!fs.existsSync(ready),'No automatic download occurs before Download is clicked');
 await page.locator('#updateDownloadButton').click();await page.locator('#updateInstallButton').waitFor({state:'visible',timeout:120000});
 check(sha(await fsp.readFile(ready))===manifest.asset.sha256,'Download through the application completes and stages verified release bytes');
 await page.screenshot({path:'build/published-upgrade-before.png'});
 const oldProfile=nativeBrowserProfile(env,oldURL);
 check(nativeBrowserProcesses(oldProfile).length>0,'Previous native application browser session is observed before installation');
 installingLegacy=true;confirmingInstall=true;await page.locator('#updateInstallButton').click();
 const newURL=await urlFor(env,false,oldURL);await page.goto('about:blank');installingLegacy=false;await page.goto(newURL);
 await until(async()=>nativeBrowserProcesses(oldProfile).length===0);
 check(true,'All browser processes for the old application profile close during the real upgrade');
 report.legacyShutdownDialogs=legacyShutdownDialogs;
 check(unexpectedDialogs.length===0,'No unexpected dialog is silently accepted during the upgrade');
 const newNative=await until(async()=>{const p=nativeBrowserProcesses(nativeBrowserProfile(env,newURL));return p.length?p:false});
 const guest=newNative.find(p=>/(?:^|\s)--guest(?:\s|$)/.test(p.CommandLine));
 check(guest&&['--disable-extensions','--disable-sync','--no-first-run'].every(flag=>guest.CommandLine.includes(flag)),'Actual new native browser uses guest mode with extensions and sync disabled');
 const healthy=await until(async()=>{const h=JSON.parse(await fsp.readFile(path.join(appRoot(env),'Health','current.json'),'utf8'));return h.version===expected&&h.ui_ready&&h.visible_window_confirmed?h:false},90000);
 check((await state(newURL)).appVersion===expected,'Native helper installs and restarts the new version');
 check(healthy.ui_ready&&healthy.visible_window_confirmed,'New application confirms a rendered interface and a visible owned native window');
 await until(async()=>{const log=await fsp.readFile(path.join(appRoot(env),'Logs','Updates.log'),'utf8');return log.includes('Update installed successfully.')});
 check((await fsp.readFile(saved.path)).equals(saved.bytes),'User settings remain byte-identical after the real upgrade');
 const rollback=path.join(appRoot(env),'Rollback','previous.exe');check(sha(await fsp.readFile(rollback))===sha(oldBytes),'Exactly the previous published executable is retained for rollback');
 const rollbackFiles=(await fsp.readdir(path.dirname(rollback))).filter(p=>p.endsWith('.exe'));check(rollbackFiles.length===1,'Only one rollback executable generation is retained');
 check(powershell('$s=(New-Object -ComObject WScript.Shell).CreateShortcut('+psQuote(shortcut)+');$s.TargetPath')===target,'Desktop shortcut target remains the same stable executable path');
 const roots={target:path.parse(target).root,staging:path.parse(ready).root};report.volumes=roots;
 check(sha(await fsp.readFile(target))===manifest.asset.sha256,'Installed target bytes match the new public release across staging volumes');
 await until(async()=>{const s=await state(newURL);return s.update.status==='current'});
 let last=(await state(newURL)).update.lastCheckUnixMs;
 await page.locator('#updateCheckButton').click();await until(async()=>{const s=await state(newURL);return s.update.lastCheckUnixMs>last});
 check(true,'Manual update check succeeds on the new executable');last=(await state(newURL)).update.lastCheckUnixMs;
 await page.locator('#updateCheckButton').click();await new Promise(r=>setTimeout(r,600));check((await state(newURL)).update.lastCheckUnixMs===last,'Short manual-check cooldown prevents another request');
 console.log('Waiting two minutes for the real focus-return threshold...');
 await until(async()=>Date.now()-last>=122000,130000);
 await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
 await until(async()=>{const s=await state(newURL);return s.update.lastCheckUnixMs>last});check(true,'Application focus binding checks again after two minutes');
 last=(await state(newURL)).update.lastCheckUnixMs;
 console.log('Waiting for the real five-minute native scheduler...');
 await until(async()=>{const s=await state(newURL);return s.update.lastCheckUnixMs>last},330000);
 check(true,'Native automatic checks continue after five active minutes');
 await until(async()=>!(await state(newURL)).update.checking);
 await page.screenshot({path:'build/published-upgrade-after.png'});
 await until(async()=>!fs.existsSync(ready));
 const stagedFiles=(await fsp.readdir(path.join(appRoot(env),'Updates','Downloads')));check(stagedFiles.length===0,'Owned download staging is cleaned after the update');
 // Close the automation page before stopping the native host, then launch the
 // same shortcut again to prove its actual post-upgrade launch behavior.
 const geometryPath=path.join(appRoot(env),'Settings','window.json');
 nativeWindowGeometry(nativeBrowserProfile(env,newURL),true);
 const expectedBounds={Left:87,Top:63,Right:987,Bottom:663};
 await until(async()=>{const g=JSON.parse(await fsp.readFile(geometryPath,'utf8'));return !g.maximized&&JSON.stringify(g.normal_bounds)===JSON.stringify(expectedBounds)});
 check(true,'Updated native window automatically saves its moved position and resized bounds');
 await page.goto('about:blank');
 await fetch(new URL('/api/shutdown'+new URL(newURL).search,newURL),{method:'POST'});
 await until(async()=>!fs.existsSync(path.join(appRoot(env),'Runtime','application-url.txt')));
 spawn('powershell.exe',['-NoProfile','-NonInteractive','-Command','Start-Process -FilePath '+psQuote(shortcut)],{env,stdio:'ignore'});
 const shortcutURL=await urlFor(env);await page.goto(shortcutURL);check((await state(shortcutURL)).appVersion===expected,'Existing desktop shortcut launches the updated executable');
 await until(async()=>{const g=nativeWindowGeometry(nativeBrowserProfile(env,shortcutURL));return g&&g.ShowCmd===1&&Object.keys(expectedBounds).every(k=>g[k]===expectedBounds[k])});
 check(true,'Real native window reopens from the stable shortcut at its saved size and position');
 check((await fsp.readFile(saved.path)).equals(saved.bytes),'Window geometry persistence leaves inventory settings byte-identical');
 await page.goto('about:blank');await stopApp(env);
 // A different owned health filename deliberately withholds the expected
 // healthy-start confirmation. No shipped headless-health bypass is used.
 const badEnv=envFor('rollback');const badSaved=await seedSettings(badEnv);const badDir=path.join(suiteRoot,'rollback-case');await fsp.mkdir(badDir,{recursive:true});
 const badTarget=path.join(badDir,'Server-Inventory-Configuration-Exporter-GUI-v3.0.10.exe');const stage=path.join(badDir,'new-stage.exe');await fsp.writeFile(badTarget,oldBytes);await fsp.writeFile(stage,newBytes);
 const root=appRoot(badEnv);const health=path.join(root,'Health','intentionally-unconfirmed.json');await fsp.mkdir(path.dirname(health),{recursive:true});
 const code=await nativeProcess(stage,['--update-helper','--parent-pid','0','--target',badTarget,'--expected-version',expected,'--expected-sha256',manifest.asset.sha256,'--old-version',oldVersion,'--health-marker',health,'--health-token',crypto.randomUUID(),'--rollback',path.join(root,'Rollback','previous.exe'),'--rollback-meta',path.join(root,'Rollback','previous.json'),'--log',path.join(root,'Logs','Updates.log')],badEnv);
 check(code===17,'Missing healthy-start confirmation triggers the actual native rollback path');
 check(sha(await fsp.readFile(badTarget))===sha(oldBytes),'Rollback restores the previous executable bytes at a legacy versioned filename');
 const restoredURL=await urlFor(badEnv,oldVersion==='3.2.0');check((await state(restoredURL)).appVersion===oldVersion,'Rollback relaunches the previous application');
 check((await fsp.readFile(badSaved.path)).equals(badSaved.bytes),'Rollback preserves user settings');await stopApp(badEnv);
 check(unexpectedDialogs.length===0,'Fixed application produces no unexpected modal dialogs throughout the test');
 report.status='passed';
})().catch(e=>{report.status='failed';report.error=e.stack;console.error(e);process.exitCode=1}).finally(async()=>{
 if(browser)await browser.close();report.unexpectedDialogs=unexpectedDialogs;report.completed=new Date().toISOString();await fsp.mkdir('build',{recursive:true});await fsp.writeFile('build/published-upgrade-report.json',JSON.stringify(report,null,2));
 // Keep only non-customer test logs as CI evidence. Never upload collected server inventory.
 for(const name of ['upgrade','rollback']){try{const log=await fsp.readFile(path.join(appRoot(envFor(name)),'Logs','Updates.log'));await fsp.writeFile('build/published-upgrade-'+name+'.log',log)}catch{}}
});
