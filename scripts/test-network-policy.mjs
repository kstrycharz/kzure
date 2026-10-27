import{readFileSync,writeFileSync}from'node:fs';
import{resolve}from'node:path';
import{spawnSync}from'node:child_process';
import{randomBytes,randomUUID,randomInt}from'node:crypto';
import assert from'node:assert/strict';
const root=resolve(import.meta.dirname,'..');const config=Object.fromEntries(readFileSync(resolve(root,'.env'),'utf8').split(/\r?\n/).filter(l=>l.includes('=')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1)]}));
const base=`http://localhost:${config.KZURE_PORT||8088}/api`;let cookie='';const results=[],created=[];const suffix=randomBytes(4).toString('hex');let tenant,subnet,vnet,group,sg,connection,hostProbe,otherSubnet,otherVNet;
const pause=ms=>new Promise(r=>setTimeout(r,ms));
async function api(path,{method='GET',body,status=200,session=cookie}={}){const response=await fetch(base+path,{method,headers:{Cookie:session,'Content-Type':'application/json','X-Kzure-Request':'portal','Idempotency-Key':randomUUID()},body:body===undefined?undefined:JSON.stringify(body)});const data=await response.json();assert.equal(response.status,status,`${method} ${path}: ${JSON.stringify(data)}`);return data}
function docker(args,expect=true){const r=spawnSync('docker',args,{encoding:'utf8',windowsHide:true});if(expect&&r.status!==0)throw new Error('Docker test failed: '+(r.stderr||r.stdout));return{ok:r.status===0,text:r.stdout?.trim()||''}}
function pass(name){results.push(name);console.log('PASS '+name)}
async function wait(id,state='Running'){for(let i=0;i<120;i++){const r=await api('/resources/'+id);if(r.state===state)return r;if(r.state==='Failed')throw new Error(r.error);await pause(400)}throw new Error('Resource timeout '+id)}
async function create(type,name,t=tenant,g=group,s=subnet){const c=(await api('/catalog')).find(c=>c.id===type);const out=await api('/resources',{method:'POST',status:202,body:{tenantId:t.id,groupId:g.id,name,type,spec:{cpu:c.cpu,ram:c.ram,storage:c.storage,subnetId:s.id}}});created.push(out.resource.id);return wait(out.resource.id)}
function request(source,url){return docker(['exec',source.containerId,'wget','-q','-T','2','-O','-',url],false).ok}
const allow=(priority,direction,port=8080)=>({name:'rule-'+priority,priority,direction,action:'allow',protocol:'tcp',source:'*',destination:'*',port});
async function update(rules){await api('/network-security-groups/'+sg.id,{method:'PUT',body:{tenantId:tenant.id,name:'policy-'+suffix,rules}})}
const inspect=()=>docker(['run','--rm','--network','host','--cap-drop','ALL','--cap-add','NET_ADMIN','kzure/network-policy:0.1.0','inspect']).text;
let baseline;
try{
 const response=await fetch(base+'/login',{method:'POST',headers:{'Content-Type':'application/json','X-Kzure-Request':'portal'},body:JSON.stringify({email:config.KZURE_ADMIN_EMAIL,password:config.KZURE_ADMIN_PASSWORD})});assert.equal(response.status,200);cookie=response.headers.get('set-cookie').split(';')[0];
 baseline=inspect();
 tenant=await api('/tenants',{method:'POST',status:201,body:{name:'Disposable network qualification '+suffix}});group=(await api('/groups?tenant='+tenant.id))[0];const octet=randomInt(120,200);
 vnet=await api('/virtual-networks',{method:'POST',status:201,body:{tenantId:tenant.id,subscriptionId:group.subscriptionId,name:'policy-net-'+suffix,cidr:`10.${octet}.0.0/16`}});
 subnet=await api('/subnets',{method:'POST',status:201,body:{tenantId:tenant.id,vnetId:vnet.id,name:'policy-subnet-'+suffix,cidr:`10.${octet}.1.0/24`}});
 const target=await create('static-web','policy-server-'+suffix);let source=await create('workspace-alpine','policy-client-'+suffix);
 let overview=await api('/networking?tenant='+tenant.id);const targetIP=overview.interfaces.find(i=>i.resourceId===target.id).address;const sourceIP=overview.interfaces.find(i=>i.resourceId===source.id).address;
 assert(request(source,'http://'+targetIP+':8080'));pass('Baseline confirms real same-subnet HTTP reachability before enforcement');
 const port=randomInt(18000,19000);const probeConfig=resolve(root,'.local/network-host-'+suffix+'.conf');writeFileSync(probeConfig,`worker_processes 1; error_log stderr; pid /tmp/probe.pid; events { worker_connections 64; } http { access_log off; client_body_temp_path /tmp/body; proxy_temp_path /tmp/proxy; fastcgi_temp_path /tmp/fastcgi; uwsgi_temp_path /tmp/uwsgi; scgi_temp_path /tmp/scgi; server { listen ${port}; location / { return 200 'host-probe'; } } }`);
 hostProbe=docker(['run','-d','--network','host','--cap-drop','ALL','--memory','32m','--cpus','.1','--label','io.kzure.installation='+config.KZURE_INSTALL_ID,'--name','kzure-policy-host-'+suffix,'--mount',`type=bind,source=${probeConfig},target=/etc/nginx/nginx.conf,readonly`,'--entrypoint','nginx','kzure/static-web:0.1.0','-g','daemon off;']).text;
 const hostURL=`http://10.${octet}.1.1:${port}`;let hostReady=false;for(let i=0;i<10;i++){if(request(source,hostURL)){hostReady=true;break}await pause(100)}assert.equal(docker(['inspect',hostProbe,'--format','{{.State.Running}}']).text,'true');assert(hostReady);pass('Baseline confirms a controlled TCP listener on the Docker host is reachable');
 sg=await api('/network-security-groups',{method:'POST',body:{tenantId:tenant.id,name:'policy-'+suffix,rules:[]}});
 await api('/subnets/'+subnet.id+'/security-group',{method:'PUT',body:{securityGroupId:sg.id}});
 assert(!request(source,'http://'+targetIP+':8080'));assert(!request(source,hostURL));pass('Default-deny policy blocks same-subnet traffic and new host connections');
 const prefix='KZ'+config.KZURE_INSTALL_ID.slice(0,8);const original=baseline.split('\n').filter(l=>!l.includes('-j '+prefix+'F'));const after=inspect().split('\n').filter(l=>!l.includes('-j '+prefix+'F'));assert.deepEqual(after,original);pass('Existing Docker/operator firewall rules remain intact');
 await update([allow(100,'outbound'),{...allow(200,'outbound'),action:'deny'},allow(300,'inbound')]);assert(request(source,'http://'+targetIP+':8080'));pass('Priority-100 allow wins over a matching lower-priority deny');
 await update([{...allow(100,'outbound'),action:'deny'},allow(200,'outbound'),allow(300,'inbound')]);assert(!request(source,'http://'+targetIP+':8080'));pass('Priority-100 deny wins over a matching lower-priority allow');
 await update([allow(100,'outbound',0),allow(200,'inbound',0)]);assert(request(source,'http://'+targetIP+':8080'));assert(!request(source,hostURL));pass('An allow-all user policy cannot override the platform host boundary');
 docker(['run','--rm','--network','container:'+source.containerId,'--cap-drop','ALL','--cap-add','NET_ADMIN','--entrypoint','ip','kzure/network-policy:0.1.0','route','add','default','via','10.'+octet+'.1.1']);
 const managementIP=JSON.parse(docker(['inspect','kzure-agent-1']).text)[0].NetworkSettings.Networks.kzure_management.IPAddress;
 assert(!docker(['exec',source.containerId,'nc','-w','2',managementIP,'8091'],false).ok);
 let applied=await api('/networking?tenant='+tenant.id);let sourceChain=prefix+'G'+applied.policyGeneration+'O'+source.id.slice(0,6);
 let counters=docker(['run','--rm','--network','host','--cap-drop','ALL','--cap-add','NET_ADMIN','--entrypoint','iptables','kzure/network-policy:0.1.0','-vnxL',sourceChain]).text;
 assert(counters.split('\n').some(line=>/\sDROP\s/.test(line)&&Number(line.trim().split(/\s+/)[0])>0));pass('Management-network probes hit a platform deny rule despite an allow-all user policy');
 const otherTenant=await api('/tenants',{method:'POST',status:201,body:{name:'Disposable peer tenant '+suffix}});const otherGroup=(await api('/groups?tenant='+otherTenant.id))[0];
 otherVNet=await api('/virtual-networks',{method:'POST',status:201,body:{tenantId:otherTenant.id,subscriptionId:otherGroup.subscriptionId,name:'peer-net-'+suffix,cidr:`10.${octet+1}.0.0/16`}});
 otherSubnet=await api('/subnets',{method:'POST',status:201,body:{tenantId:otherTenant.id,vnetId:otherVNet.id,name:'peer-subnet-'+suffix,cidr:`10.${octet+1}.1.0/24`}});
 const peer=await create('static-web','peer-server-'+suffix,otherTenant,otherGroup,otherSubnet);const peerGroup=await api('/network-security-groups',{method:'POST',body:{tenantId:otherTenant.id,name:'peer-policy-'+suffix,rules:[allow(100,'outbound',0),allow(200,'inbound',0)]}});
 await api('/subnets/'+otherSubnet.id+'/security-group',{method:'PUT',body:{securityGroupId:peerGroup.id}});const peerIP=(await api('/networking?tenant='+otherTenant.id)).interfaces.find(i=>i.resourceId===peer.id).address;
 assert(!request(source,'http://'+peerIP+':8080'));applied=await api('/networking?tenant='+tenant.id);sourceChain=prefix+'G'+applied.policyGeneration+'O'+source.id.slice(0,6);counters=docker(['run','--rm','--network','host','--cap-drop','ALL','--cap-add','NET_ADMIN','--entrypoint','iptables','kzure/network-policy:0.1.0','-vnxL',sourceChain]).text;assert(counters.split('\n').some(line=>line.includes(peerIP)&&line.includes('DROP')));pass('Cross-tenant traffic is blocked and a platform deny is installed even when both tenants allow all TCP');
 await api('/service-connections',{method:'POST',status:404,body:{sourceId:source.id,targetId:peer.id}});await api('/subnets/'+subnet.id+'/security-group',{method:'PUT',status:404,body:{securityGroupId:peerGroup.id}});pass('Cross-tenant connector and policy references are rejected by the API');
 const beforeInvalid=await api('/networking?tenant='+tenant.id);await api('/network-security-groups/'+sg.id,{method:'PUT',status:400,body:{tenantId:tenant.id,name:'policy-'+suffix,rules:[{...allow(100,'inbound'),source:'0.0.0.0/0 -j ACCEPT'}]}});const afterInvalid=await api('/networking?tenant='+tenant.id);assert.equal(afterInvalid.policyGeneration,beforeInvalid.policyGeneration);assert.deepEqual(afterInvalid.securityGroups.find(g=>g.id===sg.id).rules,beforeInvalid.securityGroups.find(g=>g.id===sg.id).rules);pass('Malformed rules are rejected without changing the applied generation');
 await update([]);connection=await api('/service-connections',{method:'POST',status:201,body:{sourceId:source.id,targetId:target.id}});assert(request(source,'http://'+connection.hostname+':8080'));assert(!request(source,'http://'+targetIP+':8080'));pass('An explicit connector works while direct origin access remains denied');
 await api('/service-connections/'+connection.id,{method:'DELETE'});connection=null;assert(!request(source,'http://'+targetIP+':8080'));pass('Removing the connector revokes its route');
 await api('/resources/'+source.id+'/actions',{method:'POST',status:202,body:{action:'stop'}});await wait(source.id,'Stopped');await api('/resources/'+source.id+'/actions',{method:'POST',status:202,body:{action:'start'}});source=await wait(source.id);assert(!request(source,'http://'+targetIP+':8080'));pass('Stop/start retains the protected address and deny policy');
 docker(['compose','restart','agent']);await pause(1500);await update([allow(100,'outbound'),allow(200,'inbound')]);assert(request(source,'http://'+targetIP+':8080'));pass('Agent restart reconciles the same owned policy hooks and restores authorized traffic');
 const ipv6=docker(['exec',source.containerId,'sh','-c','ip -6 route show default; ip -6 addr show dev eth0']).text;assert(!ipv6.includes('inet6')&&!ipv6.includes('default'));pass('The private subnet exposes no IPv6 interface address or default route');
 await api('/subnets/'+otherSubnet.id+'/security-group',{method:'DELETE'});await api('/subnets/'+subnet.id+'/security-group',{method:'DELETE'});assert(request(source,'http://'+targetIP+':8080'));pass('Removing the last association applies a valid empty generation');
 const final=await api('/networking?tenant='+tenant.id);assert.equal(final.nsgEnforced,false);writeFileSync(resolve(root,'.local/network-policy-report.json'),JSON.stringify({at:new Date().toISOString(),passed:results.length,tests:results,sourceIP,targetIP,scope:'Docker Desktop Linux VM; disposable IPv4 subnet. Physical LAN and dedicated-host qualification remain open.'},null,2));console.log(results.length+' network policy groups passed.');
}catch(e){console.error(e.message);process.exitCode=1}
finally{
 if(connection)try{await api('/service-connections/'+connection.id,{method:'DELETE'})}catch{}
 if(subnet)try{await api('/subnets/'+subnet.id+'/security-group',{method:'DELETE'})}catch{}
 if(otherSubnet)try{await api('/subnets/'+otherSubnet.id+'/security-group',{method:'DELETE'})}catch{}
 for(const id of created.reverse())try{await api('/resources/'+id+'/actions',{method:'POST',status:202,body:{action:'purge'}});await wait(id,'Deleted')}catch(e){console.error('Fixture cleanup pending: '+e.message)}
 if(subnet)try{await api('/subnets/'+subnet.id,{method:'DELETE'})}catch{}
 if(vnet)try{await api('/virtual-networks/'+vnet.id,{method:'DELETE'})}catch{}
 if(otherSubnet)try{await api('/subnets/'+otherSubnet.id,{method:'DELETE'})}catch{}
 if(otherVNet)try{await api('/virtual-networks/'+otherVNet.id,{method:'DELETE'})}catch{}
 if(hostProbe)docker(['rm','-f',hostProbe],false);
}
