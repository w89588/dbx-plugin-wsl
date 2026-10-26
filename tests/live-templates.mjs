// Opt-in: creates unique disposable WSLC containers/volumes; never changes existing ones.
import assert from 'node:assert/strict';
import {spawn,spawnSync} from 'node:child_process';
import {createInterface} from 'node:readline';
import {mkdtempSync,mkdirSync,readFileSync} from 'node:fs';
import {resolve,join} from 'node:path';
import {randomBytes} from 'node:crypto';
import {connect} from 'node:net';
if(process.env.WSL_TEMPLATE_LIVE!=='1')throw Error('Set WSL_TEMPLATE_LIVE=1 to create disposable resources');
mkdirSync('tests/tmp',{recursive:true});const dir=mkdtempSync(resolve('tests/tmp/templates-'));
const child=spawn(resolve('tests/tmp/workflow.exe'),[],{windowsHide:true,env:{...process.env,LOCALAPPDATA:dir},stdio:['pipe','pipe','pipe']});
const pending=new Map();let seq=0;const reader=createInterface({input:child.stdout});reader.on('line',line=>{const m=JSON.parse(line);if(m.method==='job/output')console.log(m.params?.text||'progress');const p=pending.get(m.id);if(p){clearTimeout(p.timer);pending.delete(m.id);p.resolve(m)}});
function rpc(method,params={}){return new Promise((resolve,reject)=>{const id=++seq;const timer=setTimeout(()=>reject(Error('timeout '+method)),65000);pending.set(id,{resolve,reject,timer});child.stdin.write(JSON.stringify({jsonrpc:'2.0',id,method,params})+'\n')})}
async function result(method,params){const r=await rpc(method,params);assert(!r.error,JSON.stringify(r.error));return r.result}
const cli='C:/Program Files/WSL/wslc.exe';const made=[];
function command(args){const r=spawnSync(cli,args,{windowsHide:true,encoding:'utf8',timeout:90000});assert.equal(r.status,0,'WSLC failed: '+args.slice(0,2).join(' '));return r.stdout}
try{
for(const [kind,port]of [['redis',26379],['postgres',25432]]){
const name='qa-'+kind+'-'+Date.now();const p={kind,name,port,password:randomBytes(24).toString('hex')};made.push(name);
const plan=await result('templates/plan',{kind,name,port});assert.equal(plan.host,'127.0.0.1');
const task=await result('templates/create',p);assert(task.jobId);let job;
const deadline=Date.now()+15*60e3;
do{await new Promise(r=>setTimeout(r,2000));job=await result('jobs/get',{jobId:task.jobId});if(Date.now()>deadline)throw Error('template job timed out')}while(job.status==='running');
assert.equal(job.status,'completed',JSON.stringify(job));assert.equal(job.result.ready,true);assert(!JSON.stringify(job).includes(p.password));
const c='dbx-'+name;
// Verify the advertised Windows loopback endpoint, not only in-container health.
await new Promise((resolve,reject)=>{const socket=connect({host:'127.0.0.1',port});const timer=setTimeout(()=>{socket.destroy();reject(Error('loopback endpoint timeout'))},10000);let data=Buffer.alloc(0);socket.on('error',e=>{clearTimeout(timer);reject(e)});socket.on('connect',()=>{if(kind==='redis'){socket.write('AUTH '+p.password+'\r\nPING\r\n')}else{const request=Buffer.alloc(8);request.writeInt32BE(8,0);request.writeInt32BE(80877103,4);socket.write(request)}});socket.on('data',chunk=>{data=Buffer.concat([data,chunk]);if(kind==='redis'&&!data.toString().includes('+PONG'))return;try{if(kind==='postgres')assert(['S','N'].includes(data.toString()[0]));clearTimeout(timer);socket.destroy();resolve()}catch(e){clearTimeout(timer);socket.destroy();reject(e)}})});

if(kind==='postgres'){assert.match(command(['exec',c,'psql','-U','app','-d','app','-tAc','SELECT 42']),/42/)}
else {assert.match(command(['exec',c,'sh','-c','REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli ping']),/PONG/);command(['exec',c,'sh','-c','REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli set dbx-qa-marker retained']);command(['restart',c]);assert.match(command(['exec',c,'sh','-c','REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli get dbx-qa-marker']),/retained/)}
const status=await result('templates/status',{name});assert.equal(status.container,c);
// Wait for health to recover after restart, then retry must reuse existing data.
for(let i=0;i<30;i++){if((await result('templates/status',{name})).ready)break;await new Promise(r=>setTimeout(r,2000))}
const again=await result('templates/create',p);assert.equal(again.reused,true);
const conflict=await rpc('templates/create',{...p,port:port+1});assert.equal(conflict.error.data.code,'CONFLICT');
console.log('PASS: live '+kind+' creation, readiness, Windows loopback endpoint, query, same-spec reuse and conflicting-spec rejection.');
}
}finally{
for(const name of made){
try{const record=JSON.parse(readFileSync(join(dir,'dbx-wsl-manager/environments',name+'.json'),'utf8'));for(const type of ['container','volume']){const n='dbx-'+name+(type==='volume'?'-data':'');const r=spawnSync(cli,[type,'inspect','--format','json',n],{windowsHide:true,encoding:'utf8'});if(r.status===0){const v=JSON.parse(r.stdout)[0];assert.equal(v.Config?.Labels?.['com.dbx.wsl.owner']||v.Labels?.['com.dbx.wsl.owner'],record.token,'refuse foreign cleanup');command(type==='container'?['container','rm','--force',n]:['volume','rm',n]);console.log('Cleaned test '+type+': '+n)}}}catch(e){console.error('Cleanup requires inspection: '+name+' '+e.message)}
}
for(const p of pending.values())clearTimeout(p.timer);reader.close();child.kill();
}
