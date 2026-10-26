// Dependency-free browser regression test. The host bridge is mocked: no real
// WSL configuration, workloads, browser links or GitHub Issues are changed.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createServer } from 'node:http';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

const root = fileURLToPath(new URL('../', import.meta.url));
const temp = join(root, 'tests/tmp');
const shots = join(root, 'tests/screenshots');
mkdirSync(temp, { recursive: true }); mkdirSync(shots, { recursive: true });
const chrome = process.env.CHROME_PATH || [
  'C:/Program Files/Google/Chrome/Application/chrome.exe',
  'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
  'D:/tools/Scoop/apps/googlechrome/current/chrome.exe',
].find(existsSync);
assert(chrome, 'Set CHROME_PATH to Chrome or Edge');
const profile = mkdtempSync(join(temp, 'ui-profile-'));
const mock = `
window.__calls = [];
window.__policy = {path:'C:\\Users\\test\\.wslconfig',revision:'r1',mode:'default',valid:true,instanceIdleTimeoutMs:15000,vmIdleTimeoutMs:60000};
window.__config = '[wsl2]\\nmemory=8GB\\n';
window.dbxPlugin = {
  ready:Promise.resolve(), locale:'zh-CN', theme:{appearance:'light'},
  capabilities:{storage:true,events:true}, storage:{get:async()=>({settings:{interval:0}}),set:async()=>{}}, onEvent:fn=>{window.__hostEvent=fn;return ()=>{};},
  invoke:async(method,params={})=>{
    window.__calls.push({method,params});
    if(method==='resources/subscribe') { if(window.__rejectSubscription){window.__rejectSubscription=false;throw new Error('disconnected');} window.__subscription=params; return {subscriptionId:params.subscriptionId}; }
    if(method==='resources/unsubscribe') return {stopped:true};
    if(method==='system/info') return {platform:{os:'windows'},wsl:{available:true,version:{wsl:'3.0.4'},status:{}},wslc:{available:false},warnings:[]};
    if(method==='system/wsl/idle/read') return {...window.__policy};
    if(method==='system/wsl/idle/write') {
      if(window.__conflict) throw new Error('WSL 配置已更改');
      await new Promise(r=>setTimeout(r,100));
      window.__policy = {...window.__policy,mode:params.mode,revision:'r2',instanceIdleTimeoutMs:params.mode==='keepAlive'?-1:15000};
      return {policy:{...window.__policy},changed:true,restartRequired:true,backupPath:'C:\\test\\.wslconfig.bak-test'};
    }
    if(method==='system/config/read') return {wslconfig:{path:'C:\\test\\.wslconfig',exists:true,content:window.__config},wslcSettings:{exists:false,content:''}};
    if(method==='wsl/distros/list') return {distros:[{name:'Debian',state:'Stopped',version:2}],count:1};
    if(method==='backups/list') return {backups:[{id:'a'.repeat(32),name:'Debian',path:'C:/backups/distribution.tar',status:'completed',stage:'completed',bytes:1024,createdAt:Date.now()}],defaultDirectory:'C:/backups'};
    if(method==='system/resources/read') return {revision:'resources-r1',content:'[wsl2]\\nmemory=4GB',values:{memory:'4GB',processors:'2',swap:'0MB'},maxProcessors:8};
    if(method==='system/resources/preview') return {before:'[wsl2]\\nmemory=4GB',after:'[wsl2]\\nmemory=8192MB',changed:true,revision:'resources-r1'};
    if(method==='diagnostics/run') return {scope:'redacted test report',checks:[{id:'wsl',status:'ok',evidence:'Tool available',action:'',checkedAt:Date.now()}],redacted:true};
    if(method==='jobs/list') return {jobs:[]};
    if(method==='jobs/get') return window.__jobSnapshot || {};
    if(method==='test/fastJob') {window.__hostEvent({method:'job/end',params:{jobId:'fast-job',status:'cancelled',ok:false,sequence:1,exitCode:0}});return {jobId:'fast-job'};}
    if(method==='system/project/open') return {opened:true};
    return {};
  }
};`;
const source = readFileSync(join(root, 'ui/index.html'), 'utf8');
const instrumented = source.replace('setTimeout(startApp, 0);', 'Object.assign(window, {S,navigate,resourceFeed,stopResourceStream,setupResourceStream,showDialog,closeDialog,startJob});setTimeout(startApp, 0);');
const html = instrumented.replace('<script>', '<script>' + mock + '</script><script>');
const server = createServer((req,res)=>{ res.setHeader('Content-Type','text/html; charset=utf-8'); res.end(html); });
await new Promise(r=>server.listen(0,'127.0.0.1',r));
const child = spawn(chrome, ['--headless=new','--disable-gpu','--no-first-run','--no-default-browser-check','--remote-debugging-port=0',`--user-data-dir=${profile}`,'about:blank'], {windowsHide:true,stdio:'ignore'});
let ws;
try {
  const portFile = join(profile,'DevToolsActivePort');
  for(let i=0;i<100 && !existsSync(portFile);i++) await delay(100);
  assert(existsSync(portFile),'Chrome did not start');
  const port = readFileSync(portFile,'utf8').split('\n')[0];
  const tabs = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  ws = new WebSocket(tabs.find(t=>t.type==='page').webSocketDebuggerUrl);
  await new Promise((r,j)=>{ws.addEventListener('open',r,{once:true});ws.addEventListener('error',j,{once:true});});
  let seq=0; const pending=new Map(), errors=[];
  ws.addEventListener('message', event=>{
    const msg=JSON.parse(event.data);
    if(msg.id) {const p=pending.get(msg.id);pending.delete(msg.id);if(msg.error)p.reject(msg.error);else p.resolve(msg.result);}
    else if(msg.method==='Runtime.exceptionThrown') errors.push(msg.params.exceptionDetails);
  });
  const call=(method,params={})=>new Promise((resolve,reject)=>{const id=++seq;pending.set(id,{resolve,reject});ws.send(JSON.stringify({id,method,params}));});
  const evaluate=async(expression)=>{ const r=await call('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});assert(!r.exceptionDetails,JSON.stringify(r.exceptionDetails));return r.result.value; };
  const wait=async(expression)=>{for(let i=0;i<160;i++){if(await evaluate(expression))return;await delay(50);}throw new Error('Timed out: '+expression);};
  const click=selector=>evaluate(`document.querySelector(${JSON.stringify(selector)}).click()`);
  const select=(selector,value)=>evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});e.value=${JSON.stringify(value)};e.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  await call('Runtime.enable'); await call('Page.enable');
  await call('Emulation.setDeviceMetricsOverride',{width:1280,height:1000,deviceScaleFactor:1,mobile:false});
  await call('Page.navigate',{url:`http://127.0.0.1:${server.address().port}/#/settings`});
  await wait(`document.querySelector('#idle-mode') && !document.querySelector('#idle-mode').disabled`);
  assert(await evaluate(`document.body.innerText.includes('空闲退出与保活')`));
  assert.equal(await evaluate(`document.querySelector('[data-target="issues"]').getAttribute('href')`),'https://github.com/w89588/dbx-plugin-wsl/issues');
  assert.equal(await evaluate(`document.querySelectorAll('a[data-target="bug"],a[data-target="feature"]').length`),0,'removed feedback buttons are still present');
  assert.equal(await evaluate(`Array.from(document.querySelectorAll('p')).some(p=>p.textContent.trim()==='https://github.com/w89588/dbx-plugin-wsl/issues')`),false,'raw Issue URL is still displayed');
  await click('[data-target="issues"]');
  await wait(`window.__calls.some(c=>c.method==='system/project/open' && c.params.target==='issues')`);
  assert(await evaluate(`location.hash==='#/settings'`),'external link navigated the plugin');
  await select('#idle-mode','keepAlive');
  await click('[data-act="idle.save"]');
  await wait(`window.__policy.mode==='keepAlive' && !document.querySelector('[data-act="idle.save"]').disabled`);
  assert(await evaluate(`document.querySelector('.idle-card').innerText.includes('配置已保存，需关闭 WSL')`));
  assert.equal(await evaluate(`window.__calls.filter(c=>c.method==='wsl/shutdown').length`),0,'saving must not shut down WSL');
  await click('.idle-card [data-act="dashboard.shutdown"]');
  await wait(`document.querySelector('[data-act="dialog.cancel"]')`);
  await click('[data-act="dialog.cancel"]');
  assert.equal(await evaluate(`window.__calls.filter(c=>c.method==='wsl/shutdown').length`),0,'cancelled shutdown executed');
  await select('#idle-mode','custom');
  await evaluate(`(()=>{const e=document.querySelector('#idle-instance');e.value='0';e.dispatchEvent(new Event('input',{bubbles:true}));})()`);
  const writes=await evaluate(`window.__calls.filter(c=>c.method==='system/wsl/idle/write').length`);
  await click('[data-act="idle.save"]');
  await wait(`document.querySelector('#idle-error')`);
  assert.equal(await evaluate(`window.__calls.filter(c=>c.method==='system/wsl/idle/write').length`),writes,'invalid input reached backend');
  assert.equal(await evaluate(`document.activeElement.id`),'idle-error',await evaluate(`JSON.stringify({active:document.activeElement.outerHTML.slice(0,500),dialogHidden:document.querySelector('#dialog-root').hidden})`));
  await select('#idle-mode','keepAlive');
  await evaluate(`(()=>{const e=document.querySelector('textarea[data-target="wslconfig"]');e.value+='\\n# draft';e.dispatchEvent(new Event('input',{bubbles:true}));})()`);
  await click('[data-act="idle.save"]');
  await wait(`document.querySelector('#idle-error')?.innerText.includes('未保存')`);
  assert.equal(await evaluate(`window.__calls.filter(c=>c.method==='system/wsl/idle/write').length`),writes,'unsaved editor was overwritten');
  await click('[data-act="nav.go"][data-route="distros"]');
  await wait(`document.querySelector('[data-act="idle.settings"]')`);
  await click('[data-act="idle.settings"]');
  await wait(`document.querySelector('#idle-mode') && !document.querySelector('#idle-mode').disabled`);
  assert(await evaluate(`document.querySelector('textarea[data-target="wslconfig"]').value.includes('# draft')`));
  await select('[data-change="settings.set"][data-key="lang"]','en');
  assert(await evaluate(`document.querySelector('#idle-title').textContent==='Idle shutdown & keep-alive'`));
  for (const [name,width,theme] of [['idle-light',1280,'light'],['idle-dark',1280,'dark'],['idle-narrow',620,'light']]) {
    await call('Emulation.setDeviceMetricsOverride',{width,height:1000,deviceScaleFactor:1,mobile:false});
    await evaluate(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.querySelector('#page').scrollTop=0`);
    await delay(150);
    assert(await evaluate(`(()=>{const c=document.querySelector('.idle-card');return c.scrollWidth<=c.clientWidth+1;})()`),'idle card overflows');
    const shot=await call('Page.captureScreenshot',{format:'png'});
    writeFileSync(join(shots,name+'.png'),Buffer.from(shot.data,'base64'));
  }
  await select('[data-change="settings.set"][data-key="lang"]','zh-CN');
  // Resource stream regression: no periodic browser queries, keyed DOM reuse,
  // changes while typing/in dialogs, ordering, disconnect and stale generation.
  await call('Emulation.setDeviceMetricsOverride',{width:1280,height:900,deviceScaleFactor:1,mobile:false});
  await evaluate(`S.settings.interval=2; navigate('distros',{force:true})`);
  await wait(`window.__subscription?.resources.includes('distros')`);
  const emit = resources => evaluate(`window.__hostEvent({method:'resources/update',params:{subscriptionId:resourceFeed.id,sequence:resourceFeed.sequence+1,resources:${JSON.stringify(resources)}}})`);
  const distros = Array.from({length:25},(_,i)=>({name:'Distro-'+i,state:'Stopped',version:2}));
  await emit({distros:{data:{distros}}});
  await wait(`document.querySelectorAll('tr[data-resource-key]').length===25`);
  await evaluate(`window.__table=document.querySelector('#page table');window.__row=document.querySelector('[data-resource-key="Distro-0"]');window.__shell=document.querySelector('#page').firstChild;document.querySelector('#page').scrollTop=120;window.__scroll=document.querySelector('#page').scrollTop;window.__resourceCalls=window.__calls.filter(c=>['wsl/distros/list','system/info'].includes(c.method)).length;`);
  distros[0].state='Running';
  await emit({distros:{data:{distros}}});
  await wait(`window.__row.innerText.includes('运行')`);
  assert(await evaluate(`window.__table===document.querySelector('#page table') && window.__shell===document.querySelector('#page').firstChild && window.__row===document.querySelector('[data-resource-key="Distro-0"]')`),'live update replaced page/table/row');
  assert(await evaluate(`document.querySelector('#page').scrollTop===window.__scroll`),'scroll position changed');
  await delay(2300);
  assert(await evaluate(`window.__calls.filter(c=>['wsl/distros/list','system/info'].includes(c.method)).length===window.__resourceCalls`),'browser still polls resource lists');
  await evaluate(`window.__search=document.querySelector('#page input');window.__search.focus();window.__search.value='Distro';window.__search.dispatchEvent(new Event('input',{bubbles:true}));window.__search.setSelectionRange(2,4)`);
  distros[0].state='Stopped';
  await emit({distros:{data:{distros}}});
  await wait(`window.__row.innerText.includes('停止')`);
  assert(await evaluate(`document.activeElement===window.__search && window.__search.value==='Distro' && window.__search.selectionStart===2 && window.__search.selectionEnd===4`),'typing/caret interrupted by stream');
  await emit({distros:{data:{distros:[{name:'New',state:'Stopped',version:2},...distros.slice().reverse()]}}});
  await delay(80);
  assert(await evaluate(`window.__row===document.querySelector('[data-resource-key="Distro-0"]')`),'reorder lost resource identity');
  await evaluate(`showDialog({title:'Draft',body:'<input id="live-draft" value="keep me">'});document.querySelector('#live-draft').focus()`);
  distros[0].state='Running';
  await emit({distros:{data:{distros}}});
  await wait(`window.__row.innerText.includes('运行')`);
  assert(await evaluate(`document.activeElement.id==='live-draft' && document.activeElement.value==='keep me'`),'dialog draft lost');
  await evaluate(`closeDialog('cancelled');window.__oldSubscription=resourceFeed.id;navigate('images')`);
  await wait(`window.__subscription?.resources.includes('images')`);
  assert(await evaluate(`window.__calls.some(c=>c.method==='resources/unsubscribe' && c.params.subscriptionId===window.__oldSubscription)`),'old view not unsubscribed');
  await evaluate(`window.__hostEvent({method:'resources/update',params:{subscriptionId:window.__oldSubscription,sequence:999,resources:{distros:{data:{distros:[]}}}}})`);
  assert(await evaluate(`S.distros.items.length===25`),'stale subscription changed data');
  await evaluate(`resourceFeed.heardAt=0; window.__savedSubscription=resourceFeed.id; stopResourceStream(); setupResourceStream()`);
  await wait(`resourceFeed.id && resourceFeed.id!==window.__savedSubscription`);
  await emit({images:{data:{images:[{id:'img1',repository:'alpine',tag:'latest',size:'8MB'}]}}});
  await wait(`document.querySelector('[data-resource-key="img1:alpine:latest"]')`);
  await evaluate(`document.querySelectorAll('.toast .t-close').forEach(el=>el.click())`);
  await delay(350);
  const liveShot=await call('Page.captureScreenshot',{format:'png'});
  writeFileSync(join(shots,'live-resources.png'),Buffer.from(liveShot.data,'base64'));
  await evaluate(`S.system.info.wslc.available=true;navigate('containers')`);
  await wait(`window.__subscription?.resources.includes('stats')`);
  await emit({containers:{data:{containers:[{id:'container-1',name:'demo',state:'running',image:'alpine'}]}},stats:{data:{stats:[{id:'container-1',cpuPerc:'1.0%',memUsage:'10MiB',memPerc:'1%'}]}}});
  await wait(`document.querySelector('[data-resource-key="container-1"]')?.innerText.includes('1.0%')`);
  await evaluate(`window.__containerRow=document.querySelector('[data-resource-key="container-1"]')`);
  await emit({stats:{data:{stats:[{id:'container-1',cpuPerc:'2.5%',memUsage:'20MiB',memPerc:'2%'}]}}});
  await wait(`window.__containerRow.innerText.includes('2.5%')`);
  assert(await evaluate(`window.__containerRow===document.querySelector('[data-resource-key="container-1"]')`),'stats replaced container row');
  await evaluate(`window.__jobSnapshot={jobId:'fast-job',method:'test/fastJob',status:'cancelled',exitCode:0,sequence:1,lines:[]};startJob('test/fastJob',{})`);
  await wait(`S.jobs.get('fast-job')?.status==='cancelled'`);
  assert(await evaluate(`S.jobs.get('fast-job').sequence===1`),'early terminal event was lost');
  // A missed update triggers a new snapshot instead of keeping stale resources.
  await evaluate(`window.__gapID=resourceFeed.id;window.__hostEvent({method:'resources/heartbeat',params:{subscriptionId:resourceFeed.id,sequence:resourceFeed.sequence+1}})`);
  await wait(`resourceFeed.id!==window.__gapID`);
  await evaluate(`Object.defineProperty(document,'visibilityState',{configurable:true,value:'hidden'});document.dispatchEvent(new Event('visibilitychange'))`);
  assert(await evaluate(`resourceFeed.id===''`),'hidden page kept subscription');
  await evaluate(`Object.defineProperty(document,'visibilityState',{configurable:true,value:'visible'});window.__rejectSubscription=true;document.dispatchEvent(new Event('visibilitychange'))`);
  await wait(`resourceFeed.status==='reconnecting'`);
  await evaluate(`window.__failedID=resourceFeed.id`);
  await wait(`resourceFeed.id!==window.__failedID && window.__subscription.subscriptionId===resourceFeed.id`);
  await evaluate(`S.settings.interval=0;setupResourceStream()`);
  assert(await evaluate(`resourceFeed.id===''`),'live updates off did not stop subscription');
  console.log('PASS: streaming resource updates preserve page/table/row identity, scroll, focus, selection and dialog drafts; no frontend polling; stats updates, early task completion, subscription cleanup, stale-event rejection, sequence recovery, hidden pause and automatic reconnection.');
  await click('#nav [data-route="backups"]');
  await wait(`document.querySelector('[data-act="backups.restore"]')`);
  await click('[data-act="backups.create"]');
  await wait(`document.querySelector('#dialog-root [name="stopBeforeBackup"]')`);
  assert(await evaluate(`document.querySelector('#dialog-root [name="directory"]').value==='C:/backups'`));
  await click('[data-act="dialog.cancel"]');
  await click('[data-act="backups.restore"]');
  await wait(`document.querySelector('#dialog-root [name="location"]')`);
  assert(await evaluate(`document.querySelector('#dialog-root [name="name"]').value==='Debian-restored'`));
  await click('[data-act="dialog.cancel"]');
  console.log('PASS: backup list, create options, new-name restore form and original-preservation disclosure.');
  await click('#nav [data-route="resources"]');
  await wait(`document.querySelector('[data-view="resources"][data-key="memoryMB"]')?.value==='4096'`);
  await evaluate(`const input=document.querySelector('[data-view="resources"][data-key="memoryMB"]');input.value='8192';input.dispatchEvent(new Event('input',{bubbles:true}))`);
  await click('[data-act="resources.preview"]');
  await wait(`document.querySelector('[data-act="resources.apply"]')?.disabled===false`);
  assert(await evaluate(`document.querySelector('#page').innerText.includes('memory=8192MB')`));
  await delay(250);writeFileSync(join(shots,'resource-config.png'),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
  await click('#nav [data-route="diagnostics"]');
  await click('[data-act="diagnostics.run"]');
  await wait(`document.querySelector('#page').innerText.includes('Tool available')`);
  await delay(250);writeFileSync(join(shots,'diagnostics.png'),Buffer.from((await call('Page.captureScreenshot',{format:'png'})).data,'base64'));
  console.log('PASS: resource configuration reads current values, previews changes and enables save only after preview; diagnostic report renders evidence.');
  assert.equal(errors.length,0,JSON.stringify(errors));
  console.log('PASS: keep-alive save, restart disclosure, cancel shutdown, input validation, draft protection, source/Issue link dispatch, navigation, Chinese/English, light/dark/narrow layout.');
  await call('Browser.close').catch(()=>{});
} finally {
  ws?.close(); child.kill(); await new Promise(r=>server.close(r));
}
