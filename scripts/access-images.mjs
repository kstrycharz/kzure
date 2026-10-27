import {spawnSync} from 'node:child_process';
import {readFileSync,writeFileSync} from 'node:fs';
import {resolve} from 'node:path';
const root=resolve(import.meta.dirname,'..');
const bases=JSON.parse(readFileSync(resolve(root,'images/bases.lock.json'),'utf8'));
function docker(args,capture=false){const r=spawnSync('docker',args,{cwd:root,encoding:'utf8',stdio:capture?'pipe':'inherit',windowsHide:true});if(r.status!==0)throw new Error('Docker '+args[0]+' failed'+(capture?': '+r.stderr:''));return r.stdout?.trim()}
const lock=JSON.parse(readFileSync(resolve(root,'.local/images.lock.json'),'utf8'));
const requested=process.argv.slice(2);
for(const [distro,base]of Object.entries(bases)){
 if(requested.length&&!requested.includes(distro))continue;
 const ref=`kzure/workspace-ssh-${distro}:0.1.0`;
 docker(['build','--build-arg',`DISTRO=${base}`,'-t',ref,'images/workspace-ssh']);
 if(lock[ref])lock[`history/${ref}/${lock[ref]}`]=lock[ref];
 lock[ref]=docker(['image','inspect',ref,'--format','{{.Id}}'],true);
 writeFileSync(resolve(root,'.local/images.lock.json'),JSON.stringify(lock,null,2)+'\n');
 writeFileSync(resolve(root,'images.lock.json'),JSON.stringify({generatedAt:new Date().toISOString(),images:lock},null,2)+'\n');
}
console.log('SSH workspace images built and locked.');
