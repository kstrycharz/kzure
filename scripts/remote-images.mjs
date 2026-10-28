import{spawnSync}from'node:child_process';
import{readFileSync,writeFileSync}from'node:fs';
import{resolve}from'node:path';
const root=resolve(import.meta.dirname,'..');
function run(command,args,capture=false){const r=spawnSync(command,args,{cwd:root,encoding:'utf8',stdio:capture?'pipe':'inherit',windowsHide:true});if(r.status!==0)throw new Error('Image command failed: '+command+' '+args[0]);return r.stdout?.trim()}
if(!process.argv.includes('--skip-workspaces'))run(process.execPath,['scripts/access-images.mjs']);
run('docker',['build','-f','images/bastion/Dockerfile','-t','kzure/bastion:0.1.0','.']);
run('docker',['build','-t','kzure/desktop-ubuntu:0.1.0','images/desktop']);
run('docker',['build','-t','kzure/network-policy:0.1.0','images/network-policy']);
run('docker',['build','-f','images/ingress/Dockerfile','-t','kzure/ingress:0.1.0','.']);
run('docker',['build','-f','images/backup/Dockerfile','-t','kzure/backup:0.1.0','.']);
const guacd='guacamole/guacd@sha256:8974eaa9ba32f713daf311e7cc8cd7e4cdfba1edea39eed75524e78ef4b08f4f';
run('docker',['pull',guacd]);
const lock=JSON.parse(readFileSync(resolve(root,'.local/images.lock.json'),'utf8'));
for(const ref of ['kzure/bastion:0.1.0','kzure/desktop-ubuntu:0.1.0','kzure/network-policy:0.1.0','kzure/ingress:0.1.0','kzure/backup:0.1.0'])lock[ref]=run('docker',['image','inspect',ref,'--format','{{.Id}}'],true);
lock['guacamole/guacd:1.6.0']=guacd;
writeFileSync(resolve(root,'.local/images.lock.json'),JSON.stringify(lock,null,2)+'\n');
writeFileSync(resolve(root,'images.lock.json'),JSON.stringify({generatedAt:new Date().toISOString(),images:lock},null,2)+'\n');
console.log('Remote access images built and locked.');
