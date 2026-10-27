import {spawnSync} from 'node:child_process';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {resolve,dirname} from 'node:path';
const root=resolve(import.meta.dirname,'..');
function git(args){const r=spawnSync('git',args,{cwd:root,encoding:null,maxBuffer:32*1024*1024});if(r.status!==0)throw new Error('Git audit read failed');return r.stdout}
const files=git(['ls-files','-z']).toString().split('\0').filter(Boolean);
if(!files.length)throw new Error('Stage the publishable source before auditing.');
const config=readFileSync(resolve(root,'.env'),'utf8').split(/\r?\n/).map(line=>{const i=line.indexOf('=');return [line.slice(0,i),line.slice(i+1)]});
const privateValues=config.filter(([key,value])=>value.length>=8&&/PASSWORD|TOKEN|ENCRYPTION|SECRET|INSTALL_ID/.test(key)).flatMap(([key,value])=>key==='KZURE_INSTALL_ID'?[value,value.slice(0,8)]:[value]);
const findings=[];const destination=resolve(root,'.local/publish-audit',Date.now().toString());mkdirSync(destination,{recursive:true});
for(const file of files){
 const bytes=git(['show',':'+file]),content=bytes.toString();
 if(/(^|\/)(\.env($|\.)|\.local\/|node_modules\/|backups\/)|\.(pem|key|pfx|p12|dump|backup|db|sqlite3?)$/i.test(file))findings.push({file,reason:'Private or generated artifact staged'});
 if(privateValues.some(value=>content.includes(value)))findings.push({file,reason:'Installation-private value found'});
 if(new RegExp('-----BEGIN '+'(?:RSA |EC |OPENSSH )?PRIVATE KEY-----').test(content))findings.push({file,reason:'Private key material found'});
 if(/[A-Z]:[\\/]Users[\\/][^\s"'<>]+/i.test(content))findings.push({file,reason:'Personal machine path found'});
 const out=resolve(destination,file);mkdirSync(dirname(out),{recursive:true});writeFileSync(out,bytes);
}
writeFileSync(resolve(root,'.local/repository-audit.json'),JSON.stringify({at:new Date().toISOString(),files:files.length,findings,stagedExport:destination},null,2)+'\n');
if(findings.length){console.error(JSON.stringify(findings,null,2));process.exit(1)}
console.log(`PASS: ${files.length} indexed files contain no known installation secrets, private key blocks, private artifacts, or personal machine paths.`);
console.log('Staged export: '+destination);
