const{execFile}=require('node:child_process'),crypto=require('node:crypto'),net=require('node:net');
async function database(){
 if(!process.env.DATABASE_URL)return{configured:false};
 const env={...process.env,PGHOST:process.env.DATABASE_HOST,PGPORT:process.env.DATABASE_PORT,PGDATABASE:process.env.DATABASE_DATABASE,PGUSER:process.env.DATABASE_USER,PGPASSWORD:process.env.DATABASE_PASSWORD,PGCONNECT_TIMEOUT:'3'};
 const sql="CREATE TABLE IF NOT EXISTS kzure_binding_demo(id bigserial PRIMARY KEY,created_at timestamptz NOT NULL DEFAULT now()); INSERT INTO kzure_binding_demo DEFAULT VALUES; SELECT count(*) FROM kzure_binding_demo;";
 const rows=await new Promise((resolve,reject)=>execFile('psql',['-X','-q','-v','ON_ERROR_STOP=1','-Atc',sql],{env,timeout:5000,maxBuffer:65536},(error,stdout)=>error?reject(Error('Database binding unavailable')):resolve(Number(stdout.trim().split('\n').at(-1)))));
 return{configured:true,connected:true,rows};
}
function sha(value){return crypto.createHash('sha256').update(value).digest('hex')}
function hmac(key,value){return crypto.createHmac('sha256',key).update(value).digest()}
async function signed(method,path,body=''){
 const target=new URL(path,process.env.STORAGE_ENDPOINT),date=new Date().toISOString().replace(/[:-]|\.\d{3}/g,''),day=date.slice(0,8),region=process.env.STORAGE_REGION||'us-east-1',hash=sha(body),scope=day+'/'+region+'/s3/aws4_request';
 const headers='host:'+target.host+'\nx-amz-content-sha256:'+hash+'\nx-amz-date:'+date+'\n',names='host;x-amz-content-sha256;x-amz-date';
 const canonical=[method,target.pathname,'',headers,names,hash].join('\n'),key=hmac(hmac(hmac(hmac('AWS4'+process.env.STORAGE_SECRET_ACCESS_KEY,day),region),'s3'),'aws4_request');
 const signature=crypto.createHmac('sha256',key).update('AWS4-HMAC-SHA256\n'+date+'\n'+scope+'\n'+sha(canonical)).digest('hex');
 return fetch(target,{method,headers:{'x-amz-content-sha256':hash,'x-amz-date':date,Authorization:'AWS4-HMAC-SHA256 Credential='+process.env.STORAGE_ACCESS_KEY_ID+'/'+scope+', SignedHeaders='+names+', Signature='+signature},body:method==='GET'?undefined:body,signal:AbortSignal.timeout(5000)});
}
async function storage(){
 if(!process.env.STORAGE_ENDPOINT)return{configured:false};
 const bucket='/kzure-binding-demo',value='Local service binding works';
 const created=await signed('PUT',bucket);if(!created.ok&&created.status!==409)throw Error('Storage binding unavailable');await created.arrayBuffer();
 const written=await signed('PUT',bucket+'/proof.txt',value);if(!written.ok)throw Error('Storage write failed');await written.arrayBuffer();
 const read=await signed('GET',bucket+'/proof.txt');if(!read.ok||await read.text()!==value)throw Error('Storage read failed');
 return{configured:true,connected:true,objectRoundTrip:true};
}
function resp(args){return'*'+args.length+'\r\n'+args.map(s=>'$'+Buffer.byteLength(s)+'\r\n'+s+'\r\n').join('')}
async function cache(){
 if(!process.env.CACHE_URL)return{configured:false};
 const counter=await new Promise((resolve,reject)=>{
  const socket=net.createConnection({host:process.env.CACHE_HOST,port:Number(process.env.CACHE_PORT)});let text='',offset=0;const replies=[];
  function fail(){socket.destroy();reject(Error('Cache binding unavailable'))}
  socket.setTimeout(5000,fail);socket.on('error',fail);socket.on('connect',()=>socket.write(resp(['AUTH',process.env.CACHE_PASSWORD])+resp(['INCR','kzure-binding-demo'])+resp(['GET','kzure-binding-demo'])));
  socket.on('data',chunk=>{text+=chunk.toString();if(text.length>65536)return fail();while(replies.length<3){const end=text.indexOf('\r\n',offset);if(end<0)return;const kind=text[offset],header=text.slice(offset+1,end);if(kind==='-')return fail();if(kind==='$'){const size=Number(header);if(!Number.isInteger(size)||size<0||size>65536)return fail();if(text.length<end+2+size+2)return;replies.push(text.slice(end+2,end+2+size));offset=end+2+size+2}else if(kind==='+'||kind===':'){replies.push(header);offset=end+2}else return fail()}socket.end();if(replies[0]!=='OK'||Number(replies[1])!==Number(replies[2]))return fail();resolve(Number(replies[1]))});
 });
 return{configured:true,connected:true,counter};
}
module.exports=async()=>{
 const result={};let healthy=true;
 await Promise.all(Object.entries({database,storage,cache}).map(async([name,probe])=>{try{result[name]=await probe()}catch{healthy=false;result[name]={configured:true,connected:false};console.error(name+' binding probe failed')}}));
 return{healthy,...result};
};
