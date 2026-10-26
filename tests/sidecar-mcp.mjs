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
function rpc(method,params={}) {return new Promise((resolve,reject)=>{const id=++seq;const timer=setTimeout(()=>{pending.delete(id);reject(new Error('RPC timeout: '+method+' '+stderr));},90000);pending.set(id,{resolve,reject,timer});child.stdin.write(JSON.stringify({jsonrpc:'2.0',id,method,params})+'\n');});}
async function result(method,params) {const response=await rpc(method,params);assert(!response.error,JSON.stringify(response.error));return response.result;}
async function invoke(tool, args={}) {
 const envelope=await result('mcp/call',{tool,arguments:args});
 assert(Array.isArray(envelope.content));
 return {envelope,payload:JSON.parse(envelope.content[0].text)};
}
try {
 const init=await result('plugin/initialize',{host:{protocolVersions:[1]}});
 assert.equal(init.plugin.version,manifest.version);assert(init.capabilities.includes('mcp'));
 const listing=await result('mcp/tools');assert.equal(listing.tools.length,4);
 const mcp=manifest.contributions.find(c=>c.type==='mcp');assert.equal(mcp.external_tools,true);
 const described=await invoke('wsl_describe');assert.equal(described.payload.methods.length,(readFileSync(join(root,'backend/internal/handler/mcp_catalog.go'),'utf8').match(/\{"[^"]+", (?:true|false),/g)||[]).length);
 const names=new Set(described.payload.methods.map(m=>m.method));
 for(const method of ['wsl/distros/exec','wslc/containers/exec','wslc/registry/login','jobs/get'])assert(names.has(method));
 const policy=(await invoke('wsl_read',{method:'system/wsl/idle/read'})).payload;
 const saved=await invoke('wsl_write',{method:'system/wsl/idle/write',paramsJson:JSON.stringify({mode:'keepAlive',expectedRevision:policy.revision})});
 assert.equal(saved.envelope.isError,false);assert.equal(saved.payload.policy.instanceIdleTimeoutMs,-1);
 assert.match(readFileSync(join(profile,'.wslconfig'),'utf8'),/instanceIdleTimeout=-1/);
 const blocked=await invoke('wsl_read',{method:'system/wsl/idle/write',paramsJson:JSON.stringify({mode:'default',expectedRevision:saved.payload.policy.revision})});
 assert.equal(blocked.envelope.isError,true);assert.match(readFileSync(join(profile,'.wslconfig'),'utf8'),/instanceIdleTimeout=-1/);
 const conflict=await invoke('wsl_write',{method:'system/wsl/idle/write',paramsJson:JSON.stringify({mode:'default',expectedRevision:policy.revision})});
 assert.equal(conflict.payload.error.code,'CONFLICT');
 const malformed=await invoke('wsl_write',{method:'wslc/registry/login',paramsJson:'{"password":"private-secret","server":[]}'});
 assert.equal(malformed.envelope.isError,true);assert(!JSON.stringify(malformed).includes('private-secret'));assert(!stderr.includes('private-secret'));
 const recursion=await invoke('wsl_write',{method:'mcp/call'});assert.equal(recursion.envelope.isError,true);
 if(process.env.WSL_MCP_LIVE==='1') {
  const connections=await invoke('wsl_connections');
  assert.equal(connections.envelope.isError,false,JSON.stringify(connections.payload));
  assert(Array.isArray(connections.payload.distros));
  const containers=await invoke('wsl_read',{method:'wslc/containers/list',paramsJson:'{"all":true}'});
  assert.equal(containers.envelope.isError,false,JSON.stringify(containers.payload));
  assert(Array.isArray(containers.payload.containers));
  console.log(`LIVE READ: ${connections.payload.distros.length} WSL distributions, ${containers.payload.containers.length} WSLC containers.`);
 }
 const jobs=await invoke('wsl_read',{method:'jobs/list'});assert.equal(jobs.envelope.isError,false);
 console.log(`PASS: packaged ${manifest.version} MCP discovery, ${described.payload.methods.length} methods, isolated write/read, read-only enforcement, conflict, redaction and recursion rejection.`);
} finally {
 for(const p of pending.values())clearTimeout(p.timer);
 child.stdin.end();reader.close();child.kill();
}
