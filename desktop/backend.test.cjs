const { test } = require('node:test');
const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const { createInterface } = require('node:readline');
const { mkdtempSync, rmSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { join } = require('node:path');
const { parseReady } = require('./policy.cjs');
const binary = join(__dirname, 'resources', process.platform === 'win32' ? 'bgmcat.exe' : 'bgmcat');
async function launch(data) {
 const child = spawn(binary, ['--desktop','--listen','127.0.0.1:0','--data',data],{stdio:['pipe','pipe','pipe'],windowsHide:true});
 child.stderr.resume();
 const done = new Promise(resolve => child.once('exit', (code,signal)=>resolve({code,signal})));
 try {
  const ready = await new Promise((resolve,reject)=>{
   const timer=setTimeout(()=>reject(new Error('backend handshake timeout')),10000);
   child.once('error',error=>{clearTimeout(timer);reject(error)});
   child.once('exit',()=>{clearTimeout(timer);reject(new Error('backend exited before handshake'))});
   createInterface({input:child.stdout}).on('line',line=>{
    const ready=parseReady(line);
    if(ready){clearTimeout(timer);resolve(ready)}
   });
  });
  return {...ready, child, done};
 } catch(error) {child.kill();await done;throw error;}
}
async function stop(process) {
 process.child.stdin.end();
 const timer=setTimeout(()=>process.child.kill(),9000);
 try {const result=await process.done;assert.equal(result.code,0);}finally{clearTimeout(timer)}
 await assert.rejects(fetch(process.origin+'/api/health',{signal:AbortSignal.timeout(1000)}));
}
test('desktop startup authenticates, preserves token and exits on parent pipe closure', {timeout:30000}, async()=>{
 const data=mkdtempSync(join(tmpdir(),'bgmcat-desktop-'));
 let first,second;
 try {
  first=await launch(data);
  const unauthorized=await fetch(first.origin+'/api/settings');
  assert.equal(unauthorized.status,401);
  const authorized=await fetch(first.origin+'/api/settings',{headers:{Authorization:'Bearer '+first.token}});
  assert.equal(authorized.status,200);
  assert.equal(first.child.spawnargs.some(arg=>arg.includes(first.token)),false);
  await stop(first);
  second=await launch(data);
  assert.equal(second.token,first.token);
  await stop(second);
 } finally {
  for(const p of [first,second])if(p&&p.child.exitCode===null&&p.child.signalCode===null){p.child.kill();await p.done}
  rmSync(data,{recursive:true,force:true});
 }
});
