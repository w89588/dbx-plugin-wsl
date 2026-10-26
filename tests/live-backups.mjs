// Explicit opt-in: creates only uniquely named disposable WSL distributions.
import assert from 'node:assert/strict';
import {spawn,spawnSync} from 'node:child_process';
import {createInterface} from 'node:readline';
import {mkdtempSync,mkdirSync,readFileSync,writeFileSync} from 'node:fs';
import {join,resolve} from 'node:path';
import {createHash} from 'node:crypto';
assert.equal(process.env.WSL_BACKUP_LIVE,'1','Set WSL_BACKUP_LIVE=1 to run disposable distro acceptance');
const root=resolve('tests/tmp');mkdirSync(root,{recursive:true});const dir=mkdtempSync(join(root,'backup-live-'));
const name='DBX-QA-'+Date.now(),clone=name+'-clone';let sourceCreated=false,cloneCreated=false,child;
function run(cmd,args){const r=spawnSync(cmd,args,{windowsHide:true,encoding:'utf8',timeout:180000});assert.equal(r.status,0,`${cmd} ${args.join(' ')}: ${r.stderr||r.stdout}`);return r.stdout;}
const url='https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/x86_64/alpine-minirootfs-3.22.2-x86_64.tar.gz';
run('curl.exe',['-fsSL','--max-time','120',url,'-o',join(dir,'rootfs.tar.gz')]);run('curl.exe',['-fsSL','--max-time','30',url+'.sha256','-o',join(dir,'rootfs.sha256')]);
assert.equal(createHash('sha256').update(readFileSync(join(dir,'rootfs.tar.gz'))).digest('hex'),readFileSync(join(dir,'rootfs.sha256'),'utf8').split(/\s+/)[0]);
try{
 run('wsl.exe',['--import',name,join(dir,'source'),join(dir,'rootfs.tar.gz'),'--version','2']);sourceCreated=true;
 run('wsl.exe',['-d',name,'--exec','sh','-c','echo dbx-acceptance > /dbx-marker']);run('wsl.exe',['--terminate',name]);
 child=spawn(resolve('tests/tmp/workflow.exe'),[],{windowsHide:true,env:{...process.env,LOCALAPPDATA:join(dir,'profile')},stdio:['pipe','pipe','pipe']});
 const pending=new Map();let seq=0;const reader=createInterface({input:child.stdout});reader.on('line',line=>{const m=JSON.parse(line);const p=pending.get(m.id);if(p){pending.delete(m.id);m.error?p.reject(new Error(JSON.stringify(m.error))):p.resolve(m.result)}});
 function rpc(method,params={}){return new Promise((resolve,reject)=>{const id=++seq;pending.set(id,{resolve,reject});child.stdin.write(JSON.stringify({jsonrpc:'2.0',id,method,params})+'\n')})}
 async function job(result){const deadline=Date.now()+180000;while(Date.now()<deadline){const j=await rpc('jobs/get',{jobId:result.jobId});if(j.status!=='running'){assert.equal(j.status,'completed',JSON.stringify(j));return j.result}await new Promise(r=>setTimeout(r,200))}throw new Error('Job timeout')}
 const backup=await job(await rpc('backups/create',{name,directory:join(dir,'archives')}));assert.equal(backup.status,'completed');
 assert.equal((await rpc('backups/list')).backups.length,1);
 assert.equal((await job(await rpc('backups/verify',{id:backup.id}))).verified,true);
 const restored=await job(await rpc('backups/restore',{id:backup.id,name:clone,location:join(dir,'clone')}));cloneCreated=true;assert.equal(restored.started,true);
 assert.match(run('wsl.exe',['-d',clone,'--exec','cat','/dbx-marker']),/dbx-acceptance/);
 assert.match(run('wsl.exe',['-d',name,'--exec','cat','/dbx-marker']),/dbx-acceptance/);
 console.log('PASS: real disposable WSL backup, persistent record, SHA-256 verification, new-name restore, startup and marker integrity; source preserved.');
 writeFileSync(join(dir,'acceptance.json'),JSON.stringify({version:JSON.parse(readFileSync('manifest.json')).version,backupID:backup.id,restored:clone,passed:true},null,2));
}finally{
 child?.kill();
 // Only the exact names created by this test may be unregistered.
 if(cloneCreated)run('wsl.exe',['--unregister',clone]);
 if(sourceCreated)run('wsl.exe',['--unregister',name]);
}
