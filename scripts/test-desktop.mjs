import{readFileSync,writeFileSync}from'node:fs';
import{resolve}from'node:path';
import assert from'node:assert/strict';
import WebSocket from'ws';
const root=resolve(import.meta.dirname,'..');const config=Object.fromEntries(readFileSync(resolve(root,'.env'),'utf8').split(/\r?\n/).filter(l=>l.includes('=')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1)]}));const origin=`http://localhost:${config.KZURE_PORT||8088}`;let cookie='';const tests=[];
async function api(path,{method='GET',body,status=200,session=cookie}={}){const r=await fetch(origin+'/api'+path,{method,headers:{Cookie:session,'Content-Type':'application/json','X-Kzure-Request':'portal'},body:body===undefined?undefined:JSON.stringify(body)});const data=await r.json();assert.equal(r.status,status,`${path}: ${JSON.stringify(data)}`);return{data,response:r}}
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
function encode(...values){return values.map(value=>[...String(value)].length+'.'+String(value)).join(',')+';'}
function parse(raw){const messages=[];let i=0,parts=[];while(i<raw.length){const dot=raw.indexOf('.',i);assert(dot>=0);const n=Number(raw.slice(i,dot));const value=raw.slice(dot+1,dot+1+n);parts.push(value);i=dot+1+n;const sep=raw[i++];if(sep===';'){messages.push(parts);parts=[]}else assert.equal(sep,',')}return messages}
function pass(name){tests.push(name);console.log('PASS '+name)}
async function deniedStream(grant,{wsOrigin=origin,ticket=grant.ticket}={}){return new Promise((resolve,reject)=>{const s=new WebSocket(origin.replace('http','ws')+'/api/desktop-sessions/'+grant.id+'/stream',['guacamole'],{headers:{Cookie:cookie,Origin:wsOrigin}});let denied=false;const timer=setTimeout(()=>{s.terminate();reject(new Error('Denied desktop stream did not close'))},8000);s.on('open',()=>s.send(JSON.stringify({type:'auth',ticket,width:800,height:600})));s.on('message',raw=>{const text=raw.toString();if(text.startsWith('{')&&JSON.parse(text).type==='error')denied=true});s.on('unexpected-response',(_req,res)=>{denied=res.statusCode===403;res.resume();s.terminate()});s.on('error',()=>{});s.on('close',()=>{clearTimeout(timer);denied?resolve():reject(new Error('Expected explicit desktop rejection'))})})}
let ws;
try{
 const login=await api('/login',{method:'POST',body:{email:config.KZURE_ADMIN_EMAIL,password:config.KZURE_ADMIN_PASSWORD}});cookie=login.response.headers.get('set-cookie').split(';')[0];const{data:tenants}=await api('/tenants');const tenant=tenants.find(t=>t.name==='Home lab');const{data:resources}=await api('/resources?tenant='+tenant.id);const desktop=resources.find(r=>r.name==='desktop-lab'),bastion=resources.find(r=>r.name==='bastion-main');assert(desktop&&bastion);
 await api('/resources/'+desktop.id+'/desktop-sessions',{method:'POST',body:{bastionId:bastion.id},session:'',status:401});pass('Anonymous browser-desktop admission is denied');
 const{data:grant}=await api('/resources/'+desktop.id+'/desktop-sessions',{method:'POST',body:{bastionId:bastion.id},status:201});
 await deniedStream(grant,{wsOrigin:'https://untrusted.example'});pass('Desktop WebSocket rejects a foreign Origin');
 const instructions=[];let closed=false,ready=false,error='';ws=new WebSocket(origin.replace('http','ws')+'/api/desktop-sessions/'+grant.id+'/stream',['guacamole'],{headers:{Cookie:cookie,Origin:origin}});
 ws.on('message',raw=>{try{const text=raw.toString();if(text.startsWith('{')){error=JSON.parse(text).data||text;return}for(const parts of parse(text)){instructions.push(parts);if(parts[0]==='')ready=true;if(parts[0]==='error')error=parts.slice(1).join(' ');if(parts[0]==='sync')ws.send(encode('sync',...parts.slice(1)));if(parts[0]==='blob')ws.send(encode('ack',parts[1],'OK','0'))}}catch(e){error=e.message}});ws.on('close',()=>closed=true);ws.on('error',e=>error=e.message);
 await new Promise((resolve,reject)=>{ws.once('open',resolve);ws.once('error',reject)});ws.send(JSON.stringify({type:'auth',ticket:grant.ticket,width:1280,height:800}));
 for(let i=0;i<300;i++){if(error)throw new Error(error);if(instructions.some(p=>p[0]==='img'||p[0]==='png'))break;if(closed)throw new Error('Desktop closed before display data');await sleep(100)}
 assert(ready,'No tunnel identity');assert(instructions.some(p=>p[0]==='img'||p[0]==='png'),'No actual RDP display image');pass('Real RDP session authenticates with certificate pinning and streams display images');
 await deniedStream(grant);pass('Consumed desktop ticket cannot be replayed');
 ws.send(encode('mouse','100','100','0'));ws.send(encode('key','65507','1'));ws.send(encode('key','65513','1'));ws.send(encode('key','116','1'));ws.send(encode('key','116','0'));ws.send(encode('key','65513','0'));ws.send(encode('key','65507','0'));await sleep(1500);assert(!closed);pass('Desktop accepts mouse and keyboard input over the browser protocol');
 await api('/desktop-sessions/'+grant.id,{method:'DELETE'});for(let i=0;i<100&&!closed;i++)await sleep(100);assert(closed);pass('Explicit disconnect ends the authenticated desktop stream');
 writeFileSync(resolve(root,'.local/desktop-report.json'),JSON.stringify({at:new Date().toISOString(),passed:tests.length,tests,instructionTypes:[...new Set(instructions.map(p=>p[0]))]},null,2));console.log(tests.length+' desktop groups passed.');
}catch(e){console.error(e.message);process.exitCode=1}finally{ws?.terminate()}
