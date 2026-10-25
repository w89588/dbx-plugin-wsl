// Exercise the packaged sidecar over stdio using an isolated Windows profile.
// Python 3 is used only to inspect/extract the ZIP package.
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createInterface } from 'node:readline';
const root = fileURLToPath(new URL('../', import.meta.url));
const manifest = JSON.parse(readFileSync(join(root,'manifest.json'),'utf8'));
const tempRoot=join(root,'tests/tmp'); mkdirSync(tempRoot,{recursive:true});
const profile=mkdtempSync(join(tempRoot,'rpc-profile-'));
const pkg=join(root,`dist/com.dbx.wsl-${manifest.version}-windows-x64.dbxp`);
const binary='bin/windows-x64/dbx-plugin-wsl-manager.exe';
const unpack=spawnSync(process.env.PYTHON || 'python',['-c',
  'import zipfile,json,sys; z=zipfile.ZipFile(sys.argv[1]); m=json.loads(z.read("manifest.json")); assert m["version"]==sys.argv[3]; assert "signature.json" not in z.namelist(); assert z.testzip() is None; z.extract("bin/windows-x64/dbx-plugin-wsl-manager.exe",sys.argv[2])',
  pkg,profile,manifest.version],{encoding:'utf8',windowsHide:true});
assert.equal(unpack.status,0,unpack.stderr);
const child=spawn(join(profile,binary),[],{windowsHide:true,env:{...process.env,USERPROFILE:profile,LOCALAPPDATA:join(profile,'AppData/Local')},stdio:['pipe','pipe','pipe']});
let seq=0; const pending=new Map(); let stderr='';
child.stderr.on('data',chunk=>stderr+=chunk);
const reader=createInterface({input:child.stdout});
reader.on('line',line=>{const msg=JSON.parse(line);const p=pending.get(msg.id);if(p){clearTimeout(p.timer);pending.delete(msg.id);p.resolve(msg);}});
function rpc(method,params={}) {return new Promise((resolve,reject)=>{const id=++seq;const timer=setTimeout(()=>{pending.delete(id);reject(new Error('RPC timeout: '+method+' '+stderr));},10000);pending.set(id,{resolve,reject,timer});child.stdin.write(JSON.stringify({jsonrpc:'2.0',id,method,params})+'\n');});}
async function result(method,params) {const response=await rpc(method,params);assert(!response.error,JSON.stringify(response.error));return response.result;}
try {
  const init=await result('plugin/initialize',{host:{protocolVersions:[1]}});
  assert.equal(init.plugin.version,manifest.version);assert.equal(init.plugin.id,manifest.id);
  let policy=await result('system/wsl/idle/read');
  assert.equal(policy.mode,'default');
  const enabled=await result('system/wsl/idle/write',{mode:'keepAlive',expectedRevision:policy.revision});
  assert.equal(enabled.policy.instanceIdleTimeoutMs,-1); assert.equal(enabled.backupPath,null);
  const path=join(profile,'.wslconfig');
  assert.match(readFileSync(path,'utf8'),/\[general\]\r?\ninstanceIdleTimeout=-1/);
  assert.equal((await rpc('system/wsl/idle/write',{mode:'default',expectedRevision:policy.revision})).error.data.code,'CONFLICT');
  policy=enabled.policy;
  const custom=await result('system/wsl/idle/write',{mode:'custom',expectedRevision:policy.revision,instanceSeconds:30,vmSeconds:120});
  assert.equal(custom.policy.instanceIdleTimeoutMs,30000);assert.equal(custom.policy.vmIdleTimeoutMs,120000);
  assert.match(readFileSync(custom.backupPath,'utf8'),/instanceIdleTimeout=-1/);
  assert.equal((await rpc('system/wsl/idle/write',{mode:'custom',expectedRevision:custom.policy.revision,instanceSeconds:0,vmSeconds:60})).error.data.code,'INVALID_PARAMS');
  assert.equal((await rpc('system/project/open',{target:'https://example.com'})).error.data.code,'INVALID_PARAMS');
  writeFileSync(path,readFileSync(path,'utf8')+'# external edit\n');
  assert.equal((await rpc('system/wsl/idle/write',{mode:'default',expectedRevision:custom.policy.revision})).error.data.code,'CONFLICT');
  policy=await result('system/wsl/idle/read');
  const restored=await result('system/wsl/idle/write',{mode:'default',expectedRevision:policy.revision});
  assert.equal(restored.policy.mode,'default');assert.match(readFileSync(path,'utf8'),/# external edit/);
  console.log(`PASS: package identity ${manifest.version}, ZIP integrity, stdio initialization, keep-alive/custom/default, backups, stale writes, validation and link allowlist (isolated profile).`);
} finally {
  for(const p of pending.values())clearTimeout(p.timer);
  child.stdin.end(); reader.close(); child.kill();
}
