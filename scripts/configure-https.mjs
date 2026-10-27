import {spawnSync} from 'node:child_process';
import {mkdirSync,existsSync} from 'node:fs';
import {resolve} from 'node:path';
const root=resolve(import.meta.dirname,'..'),dir=resolve(root,'.local/tls');
mkdirSync(dir,{recursive:true});
if(existsSync(resolve(dir,'server.crt')))throw new Error('Certificates already exist. Preserve their keys and explicitly move the directory before generating replacements.');
const r=spawnSync('docker',['run','--rm','--user','0','--mount',`type=bind,source=${dir},target=/tls`,'kzure/control-plane:0.1.0','tls-init','/tls','localhost'],{cwd:root,stdio:'inherit',windowsHide:true});
if(r.status!==0)process.exit(r.status||1);
console.log('Certificates saved privately in .local/tls. No trust store was changed. Start with docker compose -f compose.yaml -f compose.https.yaml up -d --wait.');
