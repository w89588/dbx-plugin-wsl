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
  capabilities:{storage:true,events:true}, storage:{get:async()=>({settings:{interval:0}}),set:async()=>{}}, onEvent:()=>()=>{},
  invoke:async(method,params={})=>{
    window.__calls.push({method,params});
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
    if(method==='jobs/list') return {jobs:[]};
    if(method==='system/project/open') return {opened:true};
    return {};
  }
};`;
const source = readFileSync(join(root, 'ui/index.html'), 'utf8');
const html = source.replace('<script>', '<script>' + mock + '</script><script>');
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
  const wait=async(expression)=>{for(let i=0;i<100;i++){if(await evaluate(expression))return;await delay(50);}throw new Error('Timed out: '+expression);};
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
  assert.equal(errors.length,0,JSON.stringify(errors));
  console.log('PASS: keep-alive save, restart disclosure, cancel shutdown, input validation, draft protection, source/Issue link dispatch, navigation, Chinese/English, light/dark/narrow layout.');
  await call('Browser.close').catch(()=>{});
} finally {
  ws?.close(); child.kill(); await new Promise(r=>server.close(r));
}
