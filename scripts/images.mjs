import { spawnSync } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';
const root=resolve(import.meta.dirname,'..');
function docker(args, capture=false){const r=spawnSync('docker',args,{cwd:root,stdio:capture?'pipe':'inherit',encoding:'utf8'});if(r.status!==0)throw new Error(`Docker ${args[0]} failed${capture?': '+r.stderr:''}`);return (r.stdout||'').trim()}
docker(['build','-t','kzure/static-web:0.1.0','images/static-web']);
docker(['build','-t','kzure/storage-seaweed:0.1.0','images/storage']);
docker(['build','-t','kzure/demo-app:0.1.0','images/application-demo']);
const distros=JSON.parse(readFileSync(resolve(root,'images/bases.lock.json'),'utf8'));
for(const [name,base] of Object.entries(distros))docker(['build','--build-arg',`DISTRO=${base}`,'-t',`kzure/workspace-${name}:0.1.0`,'images/workspace']);
const upstream={
 'nginxinc/nginx-unprivileged:1.29-alpine':'nginxinc/nginx-unprivileged@sha256:0c79d56aee561a1d81c63f00eee5fb5fe29279560cdc55e91425133104c7fbe6',
 'postgres:18-alpine':'postgres@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5',
 'valkey/valkey:8-alpine':'valkey/valkey@sha256:081c2f5cb575efc901aa80ff9cdbd1ec6a301682fd35e1ebb4b0990a4a4a8507'
};
const lockPath=resolve(root,'.local/images.lock.json');
const locks=existsSync(lockPath)?JSON.parse(readFileSync(lockPath,'utf8')):{};
for(const [ref,digest] of Object.entries(upstream)){docker(['pull',digest]);locks[ref]=digest}
for(const ref of ['kzure/static-web:0.1.0','kzure/storage-seaweed:0.1.0','kzure/demo-app:0.1.0',...Object.keys(distros).map(n=>`kzure/workspace-${n}:0.1.0`)]){if(locks[ref]&&ref.startsWith('kzure/workspace-'))locks[`history/${ref}/${locks[ref]}`]=locks[ref];locks[ref]=docker(['image','inspect',ref,'--format','{{.Id}}'],true)}
mkdirSync(resolve(root,'.local'),{recursive:true});
writeFileSync(resolve(root,'.local/images.lock.json'),JSON.stringify(locks,null,2)+'\n');
writeFileSync(resolve(root,'images.lock.json'),JSON.stringify({generatedAt:new Date().toISOString(),images:locks},null,2)+'\n');
console.log('Catalog images built and locked.');
const remote=spawnSync(process.execPath,['scripts/remote-images.mjs'],{cwd:root,stdio:'inherit',windowsHide:true});
if(remote.status!==0)throw new Error('Remote access image build failed');
