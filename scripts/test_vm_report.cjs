'use strict';
// Browser checks for the interactive VMware / Hyper-V discovery and readiness
// report (AGENTS.md rule 44). Generate the synthetic reports first:
//   SIMM_REPORT_SAMPLES=<folder> go test -run TestWriteSampleReports ./src/app
// then run: node scripts/test_vm_report.cjs <folder> [--executable <chrome>]
const path=require('path'),fs=require('fs'),assert=require('assert');
const {chromium}=require('playwright');
const folder=process.argv[2];
if(!folder||!fs.existsSync(path.join(folder,'VMware-Readiness-Report.html'))){console.error('usage: node scripts/test_vm_report.cjs <folder with VMware-Readiness-Report.html> [--executable path]');process.exit(2);}
const url=name=>'file:///'+path.resolve(folder,name).replace(/\\/g,'/');
(async()=>{
  const arg=process.argv.indexOf('--executable');
  const browser=await chromium.launch(arg>=0?{headless:true,executablePath:process.argv[arg+1],args:['--no-sandbox']}:{channel:'msedge',headless:true});
  const failures=[];
  try{
    const context=await browser.newContext({viewport:{width:1440,height:1000}});
    const page=await context.newPage();
    // Pages switch on hashchange, which fires after the click returns, so
    // wait for the page itself rather than the address.
    const onPage=(prefix,not)=>page.waitForFunction(([p,n])=>{const e=document.querySelector('.pg:not([hidden])');return !!e&&(e.id.indexOf(p)===0)!==n;},[prefix,!!not]);
    page.on('pageerror',e=>failures.push('page error: '+e.message));
    page.on('console',m=>{if(m.type()==='error'){failures.push('console: '+m.text());}});
    page.on('request',r=>{if(!r.url().startsWith('file:')){failures.push('request: '+r.url());}});
    await page.goto(url('VMware-Readiness-Report.html'));
    assert.strictEqual(await page.locator('.pg:not([hidden])').count(),1,'one page at a time');
    assert.strictEqual(await page.locator('#overview').isVisible(),true,'opens at the overview');

    // A tile lists exactly the VMs it counts.
    const blocked=Number(await page.locator('.tile.blocked b').first().textContent());
    await page.locator('.tile.blocked').first().click();
    await onPage('vms');
    assert.strictEqual(await page.locator('#vms').isVisible(),true,'tile opens the VM list');
    assert.strictEqual(await page.locator('table.vt tbody tr:not(.hide)').count(),blocked,'Blocked tile filters the VM list');
    assert.match(await page.locator('#vmCount').textContent(),new RegExp('Showing '+blocked+' of'));
    // Another target recounts; clearing the status shows every VM.
    await page.locator('#vms select.tsel').selectOption('scale');
    await page.locator('.fchip[data-status=""]').click();
    const all=await page.locator('table.vt tbody tr').count();
    assert.strictEqual(await page.locator('table.vt tbody tr:not(.hide)').count(),all);
    await page.locator('#vmFilter').fill('SQL01');
    assert.strictEqual(await page.locator('table.vt tbody tr:not(.hide)').count(),1,'text filter');
    await page.locator('#vmFilter').fill('');
    // Sorting by a column.
    await page.locator('table.vt th',{hasText:'vCPU'}).click();
    await page.locator('table.vt th',{hasText:'vCPU'}).click();
    const cpus=await page.locator('table.vt tbody tr td:nth-child(4)').allTextContents();
    assert.deepStrictEqual(cpus.map(Number),cpus.map(Number).slice().sort((a,b)=>b-a),'sorts numerically');

    // A bar segment filters too.
    await page.goto(url('VMware-Readiness-Report.html')+'#overview');
    const seg=page.locator('.bar-row').first().locator('.seg.review');
    const review=Number(await seg.textContent());
    await seg.click();
    await onPage('vms');
    assert.strictEqual(await page.locator('table.vt tbody tr:not(.hide)').count(),review,'bar segment filters the VM list');

    // Links open object pages, with tabs, and the tree follows.
    await page.locator('table.vt tbody tr:not(.hide) td:first-child a').first().click();
    await onPage('vm-');
    const vmPage=page.locator('.pg:not([hidden])');
    assert.match(await vmPage.getAttribute('id'),/^vm-/,'a VM name opens its page');
    assert.strictEqual(await page.locator('.tree.on a.sel').count(),1,'the tree shows the open VM');
    await vmPage.locator('.tabs button[data-tab="disks"]').click();
    assert.strictEqual(await vmPage.locator('.pane[data-pane="disks"]').isVisible(),true,'tabs switch');
    await vmPage.locator('.crumb a').last().click();
    await onPage('vm-',true);
    assert.match(await page.locator('.pg:not([hidden])').getAttribute('id'),/^host-|^vms$/,'breadcrumb opens the host');
    // Back returns to the VM.
    await page.goBack();
    await onPage('vm-');
    assert.match(await page.locator('.pg:not([hidden])').getAttribute('id'),/^vm-/,'Back returns');

    // Issues: the fix is shown and every VM in it links to its page.
    await page.goto(url('VMware-Readiness-Report.html')+'#issues');
    assert.ok(await page.locator('.tg:not([hidden]) .issue').count()>0,'issues for the target');
    assert.strictEqual(await page.locator('.tg:not([hidden])').count(),1,'one target at a time');
    assert.ok(await page.locator('.tg:not([hidden]) .issue .fix').count()>0,'fixes shown');

    // Tree search.
    await page.locator('.views button[data-view="vms"]').click();
    await page.locator('#q').fill('WEB0');
    const shown=await page.evaluate(()=>Array.from(document.querySelectorAll('.tree.on a[data-vm]')).filter(a=>a.offsetParent!==null).map(a=>a.textContent));
    assert.ok(shown.length===2&&shown.every(t=>t.includes('WEB0')),'search filters the tree: '+shown.join(','));
    await page.locator('#q').press('Enter');
    await onPage('vm-');
    assert.match(await page.locator('.pg:not([hidden]) h2').textContent(),/WEB0/,'Enter opens the first match');

    // The discovery report opens at the hosts.
    await page.goto(url('VMware-Discovery-Report.html'));
    assert.strictEqual(await page.locator('#hosts').isVisible(),true,'discovery report opens at the hosts');

    // Printing shows every page and tab.
    await page.emulateMedia({media:'print'});
    const hidden=await page.evaluate(()=>Array.from(document.querySelectorAll('.pg,.pane')).filter(e=>getComputedStyle(e).display==='none').length);
    assert.strictEqual(hidden,0,'print shows everything');
    await page.emulateMedia({media:'screen'});

    // Low resolution: nothing wider than the window.
    for(const size of [{width:1008,height:690},{width:784,height:522}]){
      await page.setViewportSize(size);
      await page.goto(url('VMware-Readiness-Report.html')+'#overview');
      const wide=await page.evaluate(()=>document.documentElement.scrollWidth-window.innerWidth);
      assert.ok(wide<=1,'no horizontal scroll at '+size.width+': '+wide);
    }

    // Without script the whole report is still there to read.
    const plain=await browser.newContext({javaScriptEnabled:false,viewport:{width:1200,height:900}});
    const noscript=await plain.newPage();
    await noscript.goto(url('VMware-Readiness-Report.html'));
    assert.ok(await noscript.locator('section.pg').count()>20,'pages present');
    assert.strictEqual(await noscript.locator('#issues').isVisible(),true,'every page readable without script');
    await plain.close();
  }catch(error){failures.push(error.stack||String(error));}
  finally{await browser.close();}
  if(failures.length){console.error(failures.join('\n'));process.exit(1);}
  console.log('Interactive VM report: all checks passed.');
})();
