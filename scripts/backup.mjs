import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { createHash, randomBytes } from 'node:crypto';
import { resolve } from 'node:path';
const root=resolve(import.meta.dirname,'..');
const config=Object.fromEntries(readFileSync(resolve(root,'.env'),'utf8').trim().split(/\r?\n/).map(line=>{const i=line.indexOf('=');return [line.slice(0,i),line.slice(i+1)]}));
function docker(args,input){const r=spawnSync('docker',args,{cwd:root,input,maxBuffer:128*1024*1024});if(r.status!==0)throw new Error(`Docker ${args[0]} failed: ${r.stderr?.toString()}`);return r.stdout}
const stamp=new Date().toISOString().replace(/[:.]/g,'-');
const directory=resolve(root,'.local/backups',stamp);mkdirSync(directory,{recursive:true});
const dump=docker(['compose','exec','-T','database','pg_dump','-U','kzure','-d','kzure','-Fc','--no-owner','--no-acl']);
writeFileSync(resolve(directory,'platform.dump'),dump,{mode:0o600});
const metadata={createdAt:new Date().toISOString(),version:'0.1.0-dev',installationId:config.KZURE_INSTALL_ID,sha256:createHash('sha256').update(dump).digest('hex'),bytes:dump.length,includes:'Platform PostgreSQL metadata, users, encrypted credentials, operations and allocations',excludes:'Workload volumes, container images, installation encryption key and other .env settings',keyFingerprint:createHash('sha256').update(config.KZURE_ENCRYPTION_KEY).digest('hex'),restoreCheck:'Not performed'};
if(process.argv.includes('--verify')){
 const scratch='kzure_restore_verify_'+randomBytes(6).toString('hex');
 docker(['compose','exec','-T','database','createdb','-U','kzure',scratch]);
 try{
  docker(['compose','exec','-T','database','pg_restore','-U','kzure','-d',scratch,'--exit-on-error','--no-owner','--no-acl'],dump);
  const count=db=>docker(['compose','exec','-T','database','psql','-U','kzure','-d',db,'-Atc','SELECT count(*) FROM resources']).toString().trim();
  const original=count('kzure'),restored=count(scratch);if(original!==restored)throw new Error('Restored resource inventory does not match the source');
  metadata.restoreCheck=`Passed: scratch database restored with ${restored} resource records`;
 }finally{docker(['compose','exec','-T','database','dropdb','-U','kzure',scratch])}
}
writeFileSync(resolve(directory,'manifest.json'),JSON.stringify(metadata,null,2)+'\n',{mode:0o600});
console.log(`Platform backup saved: ${directory}`);console.log(metadata.restoreCheck);console.log('Workload data and .env recovery keys must be backed up separately.');
