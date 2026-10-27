const http=require('node:http'),fs=require('node:fs'),crypto=require('node:crypto');
http.createServer(async(req,res)=>{
 if(req.url==='/healthz'){res.writeHead(process.env.FAIL_HEALTH==='1'?503:200);return res.end('health')}
 if(req.url==='/bindings'){const result=await require('./bindings.cjs')();res.writeHead(result.healthy?200:503,{'Content-Type':'application/json'});return res.end(JSON.stringify(result))}
 let visits=0;try{visits=Number(fs.readFileSync('/data/visits','utf8'))}catch{};fs.writeFileSync('/data/visits',String(++visits));
 res.setHeader('Content-Type','application/json');res.end(JSON.stringify({application:'Kzure example API',version:process.env.DEMO_VERSION,greeting:process.env.GREETING||'Hello from your own image',visits,secretConfigured:!!process.env.APP_TOKEN,tokenHash:process.env.APP_TOKEN?crypto.createHash('sha256').update(process.env.APP_TOKEN).digest('hex'):null}));
}).listen(3000,'0.0.0.0');
