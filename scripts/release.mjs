import {spawnSync} from 'node:child_process';
import {mkdirSync,readFileSync,writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {resolve} from 'node:path';
const root=resolve(import.meta.dirname,'..');
const catalog=JSON.parse(readFileSync(resolve(root,'images.lock.json'),'utf8'));
const refs=['kzure/control-plane:0.1.0','kzure/portal:0.1.0',...Object.keys(catalog.images).filter(ref=>!ref.startsWith('history/'))];
const images=refs.map(ref=>{const r=spawnSync('docker',['image','inspect',catalog.images[ref]||ref,'--format','{{json .}}'],{encoding:'utf8'});if(r.status!==0)throw new Error(`Image missing: ${ref}`);const i=JSON.parse(r.stdout);return{reference:ref,id:i.Id,architecture:i.Architecture,os:i.Os,repoDigests:i.RepoDigests||[]}});
const dir=resolve(root,'.local/releases/0.1.0-dev');mkdirSync(dir,{recursive:true});
const out={version:'0.1.0-dev',createdAt:new Date().toISOString(),status:'Development preview; see docs/implementation-status.md for incomplete release gates',images};
const text=JSON.stringify(out,null,2)+'\n';writeFileSync(resolve(dir,'manifest.json'),text);writeFileSync(resolve(dir,'manifest.sha256'),createHash('sha256').update(text).digest('hex')+'  manifest.json\n');
if(process.argv.includes('--images')){const path=resolve(dir,'images.tar');const r=spawnSync('docker',['save','-o',path,...new Set(images.map(i=>i.id))],{stdio:'inherit'});if(r.status!==0)throw new Error('Image archive failed');console.log('Saved offline image archive: '+path)}
console.log(`Recorded ${images.length} local image identities in ${dir}`);
