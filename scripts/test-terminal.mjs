import assert from 'node:assert/strict';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {resolve} from 'node:path';
import {randomBytes,randomUUID} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import WebSocket from 'ws';

const root=resolve(import.meta.dirname,'..');
const config=Object.fromEntries(readFileSync(resolve(root,'.env'),'utf8').split(/\r?\n/).filter(l=>l.includes('=')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1)]}));
const origin=`http://localhost:${config.KZURE_PORT||8088}`,base=origin+'/api';
let cookie='';const results=[],created=[],openSockets=[];const suffix=randomBytes(4).toString('hex');
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function api(path,{method='GET',body,session=cookie,status=200}={}){const response=await fetch(base+path,{method,headers:{Cookie:session,'X-Kzure-Request':'portal','Content-Type':'application/json','Idempotency-Key':randomUUID()},body:body===undefined?undefined:JSON.stringify(body)});const data=await response.json();assert.equal(response.status,status,`${method} ${path}: ${JSON.stringify(data)}`);return{data,response}}
function pass(name){results.push(name);console.log('PASS '+name)}
async function waitResource(id,state='Running'){for(let i=0;i<100;i++){const{data}=await api('/resources/'+id);if(data.state===state)return data;if(data.state==='Failed')throw new Error(data.error);await delay(400)}throw new Error('Resource timeout '+id)}
async function grant(id,session=cookie){return(await api('/resources/'+id+'/terminal-sessions',{method:'POST',session,status:201})).data}
async function connect(g,{session=cookie,wsOrigin=origin,ticket=g.ticket,ack=true}={}){
 const ws=new WebSocket(origin.replace('http','ws')+'/api/terminal-sessions/'+g.id+'/stream',{headers:{Cookie:session,Origin:wsOrigin}});openSockets.push(ws);
 const state={ws,text:'',frames:[],closed:false};
 ws.on('message',raw=>{const f=JSON.parse(raw);state.frames.push(f);if(f.type==='output'){state.text+=Buffer.from(f.data,'base64').toString('utf8');if(ack)ws.send(JSON.stringify({type:'ack'}))}});ws.on('close',()=>state.closed=true);ws.on('error',()=>{});
 await new Promise((resolve,reject)=>{ws.once('open',()=>{ws.send(JSON.stringify({type:'auth',ticket}));resolve()});ws.once('error',reject)});
 return state;
}
async function until(fn,label,timeout=15000){const end=Date.now()+timeout;while(Date.now()<end){if(fn())return;await delay(50)}throw new Error('Timeout: '+label)}
async function ready(s){await until(()=>s.frames.some(f=>f.type==='ready')||s.closed,'terminal ready');assert(s.frames.some(f=>f.type==='ready'),'No ready frame: '+JSON.stringify(s.frames));return s}
function input(s,data){s.ws.send(JSON.stringify({type:'input',data}))}
async function command(s,text,expected){
 s.text='';const marker='finished'+randomBytes(6).toString('hex');
 // Encode the completion marker so the PTY's input echo cannot satisfy the wait.
 const escaped=[...Buffer.from(marker)].map(byte=>'\\'+byte.toString(8).padStart(3,'0')).join('');
 input(s,text+`; printf '${escaped}\\n'\n`);
 await until(()=>s.text.includes(marker),'command completion '+text);
 assert(s.text.includes(expected),'Missing command output '+expected);return s.text;
}
async function disconnect(s,g){s.ws.close();await until(()=>s.closed,'WebSocket close');await api('/terminal-sessions/'+g.id,{method:'DELETE'});}
const docker=args=>execFileSync('docker',args,{encoding:'utf8',windowsHide:true}).trim();

try{
 const login=await api('/login',{method:'POST',body:{email:config.KZURE_ADMIN_EMAIL,password:config.KZURE_ADMIN_PASSWORD}});cookie=login.response.headers.get('set-cookie').split(';')[0];
 const{data:tenants}=await api('/tenants');const tenant=tenants.find(t=>t.name==='Home lab');const{data:groups}=await api('/groups?tenant='+tenant.id);const group=groups[0];
 const{data:resources}=await api('/resources?tenant='+tenant.id);const workspace=resources.find(r=>r.name==='linux-lab');assert(workspace?.state==='Running');
 await api('/resources/'+workspace.id+'/terminal-sessions',{method:'POST',session:'',status:401});pass('Anonymous terminal admission is denied');
 const g=await grant(workspace.id);const s=await ready(await connect(g));
 await command(s,'printf "USER=%s DIR=%s\\n" "$(id -u)" "$PWD"','USER=1000 DIR=/data');
 await command(s,"printf '\\360\\237\\214\\215 terminal-unicode\\n'",'🌍 terminal-unicode');
 s.ws.send(JSON.stringify({type:'resize',cols:103,rows:37}));await delay(700);await command(s,'stty size','37 103');
 s.text='';input(s,'sleep 30\n');await delay(300);input(s,'\x03');await delay(100);await command(s,"printf 'INTERRUPT_OK\\n'",'INTERRUPT_OK');
 await command(s,`echo ${suffix} > /data/terminal-proof-${suffix}.txt; printf 'WRITE_OK\\n'`,'WRITE_OK');
 pass('Real PTY supports non-root shell, UTF-8, resize, Ctrl-C and persistent writes');
 await command(s,'i=0; while [ "$i" -lt 3000 ]; do printf "large-output-line-%s-abcdefghijklmnopqrstuvwxyz\\n" "$i"; i=$((i+1)); done','large-output-line-2999-');assert(s.text.length>100000);pass('Large output streams through bounded acknowledged frames');
 const stalledGrant=await grant(workspace.id);const stalled=await ready(await connect(stalledGrant,{ack:false}));await until(()=>stalled.closed,'stalled output disconnect',15000);pass('A client that stops consuming output is disconnected');
 const expired=await grant(workspace.id);docker(['compose','exec','-T','database','psql','-U','kzure','-d','kzure','-c',`UPDATE terminal_sessions SET expires_at=now()-interval '1 second' WHERE id='${expired.id}'`]);const expiredSocket=await connect(expired);await until(()=>expiredSocket.frames.some(f=>f.type==='error')||expiredSocket.closed,'expired ticket rejection');assert(expiredSocket.frames.some(f=>f.type==='error'));await api('/terminal-sessions/'+expired.id,{method:'DELETE'});pass('Expired tickets are rejected without starting a shell');
 const replay=await connect(g);await until(()=>replay.frames.some(f=>f.type==='error')||replay.closed,'replay rejection');assert(replay.frames.some(f=>f.type==='error'));replay.ws.close();pass('Consumed connection tickets cannot be replayed');
 const pending=await grant(workspace.id);await api('/resources/'+workspace.id+'/terminal-sessions',{method:'POST',status:429});await api('/terminal-sessions/'+pending.id,{method:'DELETE'});pass('Concurrent pending and active sessions obey the resource limit');
 const denied=new WebSocket(origin.replace('http','ws')+'/api/terminal-sessions/'+g.id+'/stream',{headers:{Cookie:cookie,Origin:'https://untrusted.example'}});openSockets.push(denied);const rejected=await new Promise(resolve=>{denied.on('unexpected-response',(_req,res)=>{res.resume();resolve(res.statusCode);denied.terminate()});denied.on('error',()=>{})});assert.equal(rejected,403);pass('WebSocket connections require an approved Origin');
 // Closing one of two sessions must not interrupt the other.
 const secondGrant=await grant(workspace.id);const second=await ready(await connect(secondGrant));
 await command(s,'sleep 120 & echo BACKGROUND_PID=$!','BACKGROUND_PID=');
 const bg=Number(s.text.match(/BACKGROUND_PID=(\d+)/)?.[1]);assert(bg>1);
 await disconnect(s,g);await delay(1000);
 assert.equal(docker(['exec',workspace.containerId,'sh','-c',`test ! -e /proc/${bg}/stat || { read p comm state rest < /proc/${bg}/stat; test "$state" = Z; }; echo $?`]),'0');
 await command(second,"printf 'SECOND_ALIVE\\n'",'SECOND_ALIVE');await disconnect(second,secondGrant);pass('Disconnect cleans up the session jobs without stopping another shell');
 const reconnectedGrant=await grant(workspace.id);const reconnected=await ready(await connect(reconnectedGrant));await command(reconnected,`cat /data/terminal-proof-${suffix}.txt`,suffix);await disconnect(reconnected,reconnectedGrant);pass('Reconnect preserves workspace data');
 const exitGrant=await grant(workspace.id);const exitShell=await ready(await connect(exitGrant));input(exitShell,'exit 7\n');await until(()=>exitShell.frames.some(f=>f.type==='exit'),'shell exit status');assert.equal(exitShell.frames.find(f=>f.type==='exit').code,7);await until(()=>exitShell.closed,'natural shell exit');assert.equal(docker(['exec',workspace.containerId,'sh','-c',`test ! -e /tmp/kzure-terminal-1000/${exitGrant.id}.sock; echo $?`]),'0');pass('Natural shell exit returns the actual exit code and removes its control socket');
 const password=randomBytes(18).toString('hex');
 const{data:member}=await api('/users',{method:'POST',status:201,body:{tenantId:tenant.id,name:'Terminal contributor '+suffix,email:'terminal-'+suffix+'@kzure.local',password,role:'contributor'}});
 const{data:members}=await api('/resources/'+workspace.id+'/shell-members');assert(members.some(m=>m.id===member.id&&m.role==='contributor'));assert(members.every(m=>!('password'in m)));
 const memberLogin=await api('/login',{method:'POST',body:{email:'terminal-'+suffix+'@kzure.local',password}});const memberCookie=memberLogin.response.headers.get('set-cookie').split(';')[0];
 await api('/resources/'+workspace.id+'/terminal-sessions',{method:'POST',session:memberCookie,status:403});
 await api('/resources/'+workspace.id+'/shell-members',{session:memberCookie,status:403});
 await api('/resources/'+workspace.id+'/shell-access/'+member.id,{method:'PUT'});const memberGrant=await grant(workspace.id,memberCookie);const memberShell=await ready(await connect(memberGrant,{session:memberCookie}));
 await command(memberShell,"printf 'GRANTED_OK\\n'",'GRANTED_OK');await api('/resources/'+workspace.id+'/shell-access/'+member.id,{method:'DELETE'});await until(()=>memberShell.closed,'grant revocation',10000);pass('Contributors need explicit grants and revocation closes active shells');
 const{data:t2}=await api('/tenants',{method:'POST',status:201,body:{name:'Terminal isolation '+suffix}});
 const readerPassword=randomBytes(18).toString('hex');const{data:readerUser}=await api('/users',{method:'POST',status:201,body:{tenantId:t2.id,name:'Terminal reader',email:'terminal-reader-'+suffix+'@kzure.local',password:readerPassword,role:'reader'}});
 const readerLogin=await api('/login',{method:'POST',body:{email:'terminal-reader-'+suffix+'@kzure.local',password:readerPassword}});const readerCookie=readerLogin.response.headers.get('set-cookie').split(';')[0];
 for(const path of ['/resources/'+workspace.id+'/shell-access','/resources/'+workspace.id+'/terminal-sessions'])await api(path,{session:readerCookie,status:404});await api('/resources/'+workspace.id+'/terminal-sessions',{method:'POST',session:readerCookie,status:404});pass('Cross-tenant users cannot inspect or open workspace sessions');
 const sameReaderPassword=randomBytes(18).toString('hex');const{data:sameReader}=await api('/users',{method:'POST',status:201,body:{tenantId:tenant.id,name:'Local reader',email:'same-reader-'+suffix+'@kzure.local',password:sameReaderPassword,role:'reader'}});const sameReaderLogin=await api('/login',{method:'POST',body:{email:'same-reader-'+suffix+'@kzure.local',password:sameReaderPassword}});const sameReaderCookie=sameReaderLogin.response.headers.get('set-cookie').split(';')[0];await api('/resources/'+workspace.id+'/terminal-sessions',{method:'POST',session:sameReaderCookie,status:403});await api('/resources/'+workspace.id+'/shell-access/'+sameReader.id,{method:'PUT',status:400});pass('Readers cannot run shells or receive interactive grants');
 // Exercise each actual distro with disposable resources; no existing workspace is stopped.
 const{data:catalog}=await api('/catalog');
 for(const item of catalog.filter(c=>c.id.startsWith('workspace-'))){
  const{data:createdResource}=await api('/resources',{method:'POST',status:202,body:{tenantId:tenant.id,groupId:group.id,name:'terminal-'+item.id.slice(10)+'-'+suffix,type:item.id,spec:{cpu:item.cpu,ram:item.ram,storage:item.storage}}});created.push(createdResource.resource.id);const resource=await waitResource(createdResource.resource.id);
  const testGrant=await grant(resource.id);const shell=await ready(await connect(testGrant));await command(shell,"printf 'DISTRO_OK=%s\\n' \"$(id -u)\"",'DISTRO_OK=1000');
  await api('/resources/'+resource.id+'/actions',{method:'POST',status:202,body:{action:'stop'}});await until(()=>shell.closed,'workspace stop ends shell',10000);await waitResource(resource.id,'Stopped');await api('/resources/'+resource.id+'/terminal-sessions',{method:'POST',status:409});
  await api('/resources/'+resource.id+'/actions',{method:'POST',status:202,body:{action:'purge'}});await waitResource(resource.id,'Deleted');created.splice(created.indexOf(resource.id),1);
  pass(item.name+': real shell, stop disconnect, stopped admission denial');
 }
 // A separate authenticated session lets logout testing leave the operator login intact.
 const login2=await api('/login',{method:'POST',body:{email:config.KZURE_ADMIN_EMAIL,password:config.KZURE_ADMIN_PASSWORD}});const cookie2=login2.response.headers.get('set-cookie').split(';')[0];const logoutGrant=await grant(workspace.id,cookie2);const logoutShell=await ready(await connect(logoutGrant,{session:cookie2}));await api('/logout',{method:'POST',session:cookie2});await until(()=>logoutShell.closed,'logout disconnect',10000);pass('Signing out closes shells bound to that login');
 const crashGrant=await grant(workspace.id);const crashShell=await ready(await connect(crashGrant));await command(crashShell,'sleep 120 & echo CRASH_PID=$!','CRASH_PID=');const crashPid=Number(crashShell.text.match(/CRASH_PID=(\d+)/)?.[1]);assert(crashPid>1);docker(['compose','restart','agent']);await until(()=>crashShell.closed,'agent restart disconnect',15000);await delay(30000);assert.equal(docker(['exec',workspace.containerId,'sh','-c',`test ! -e /proc/${crashPid}/stat || { read p comm state rest < /proc/${crashPid}/stat; test "$state" = Z; }; echo $?`]),'0');const afterCrash=await grant(workspace.id);const afterCrashShell=await ready(await connect(afterCrash));await command(afterCrashShell,"printf 'RECOVERY_OK\\n'",'RECOVERY_OK');await disconnect(afterCrashShell,afterCrash);pass('Agent restart closes the stream, local lease cleans orphan jobs, and new sessions recover');
 docker(['exec',workspace.containerId,'rm','-f','/data/terminal-proof-'+suffix+'.txt']);
 mkdirSync(resolve(root,'.local'),{recursive:true});writeFileSync(resolve(root,'.local/terminal-report.json'),JSON.stringify({at:new Date().toISOString(),passed:results.length,tests:results},null,2));console.log(results.length+' terminal integration groups passed.');
}catch(error){console.error(error.message);process.exitCode=1}
finally{for(const ws of openSockets)ws.terminate();for(const id of created){try{await api('/resources/'+id+'/actions',{method:'POST',status:202,body:{action:'purge'}})}catch{}}}
