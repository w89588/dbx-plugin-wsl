// Isolated-profile native tests; never writes the real user's WSL configuration.
import assert from 'node:assert/strict';import {spawn} from 'node:child_process';import {createInterface} from 'node:readline';import {mkdtempSync,readFileSync,writeFileSync,mkdirSync} from 'node:fs';import {resolve,join} from 'node:path';
mkdirSync('tests/tmp',{recursive:true});const dir=mkdtempSync(resolve('tests/tmp/workflows-'));const child=spawn(resolve('tests/tmp/workflow.exe'),[],{windowsHide:true,env:{...process.env,USERPROFILE:dir,LOCALAPPDATA:dir},stdio:['pipe','pipe','pipe']});
const pending=new Map();let seq=0;const reader=createInterface({input:child.stdout});reader.on('line',line=>{const m=JSON.parse(line);const p=pending.get(m.id);if(p){clearTimeout(p.timer);pending.delete(m.id);p.resolve(m)}});
function rpc(method,params={}){return new Promise((resolve,reject)=>{const id=++seq;const timer=setTimeout(()=>reject(new Error('timeout '+method)),65000);pending.set(id,{resolve,reject,timer});child.stdin.write(JSON.stringify({jsonrpc:'2.0',id,method,params})+'\n')})}
async function result(method,params){const r=await rpc(method,params);assert(!r.error,JSON.stringify(r.error));return r.result}
try{
const path=join(dir,'.wslconfig');writeFileSync(path,'# retained\r\n[wsl2]\r\nmemory=4GB\r\nnetworkingMode=mirrored\r\n');
const state=await result('system/resources/read');const p={expectedRevision:state.revision,memoryMB:8192,swapMB:0,processors:1};
const preview=await result('system/resources/preview',p);assert.match(preview.after,/memory=8192MB/);assert.match(preview.after,/networkingMode=mirrored/);assert.match(readFileSync(path,'utf8'),/memory=4GB/);
writeFileSync(path,'# external edit\n');const conflict=await rpc('system/resources/apply',p);assert.equal(conflict.error.data.code,'CONFLICT');assert.equal(readFileSync(path,'utf8'),'# external edit\n');
writeFileSync(path,'[wsl2]\nmemory=4GB\n');p.expectedRevision=(await result('system/resources/read')).revision;const saved=await result('system/resources/apply',p);assert.equal(saved.appliedToRunningInstances,false);assert.match(readFileSync(path,'utf8'),/8192MB/);
const report=await result('diagnostics/run',{ports:[]});assert.equal(report.redacted,true);assert(report.checks.every(c=>c.checkedAt&&c.evidence));assert(!JSON.stringify(report).includes(dir));
console.log('PASS: native resource preview, no-write preview, external-conflict rejection, isolated apply and redacted live tool/disk diagnostics.');
}finally{for(const p of pending.values())clearTimeout(p.timer);child.stdin.end();reader.close();child.kill()}
