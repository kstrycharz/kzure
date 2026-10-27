import{useEffect,useRef,useState}from'react';
import{Monitor,RefreshCw,X}from'lucide-react';
// Apache's pinned browser library is bundled locally from its official image.
// Its dynamic protocol classes have no upstream TypeScript declarations.
declare global{interface Window{Guacamole:any}}
let library:Promise<void>|null=null;
function loadLibrary(){return library??=new Promise<void>((resolve,reject)=>{if(window.Guacamole){resolve();return}const script=document.createElement('script');script.src='/vendor/guacamole-common.js';script.onload=()=>resolve();script.onerror=()=>{library=null;reject(new Error('Desktop browser library could not be loaded.'))};document.head.appendChild(script)})}
function encode(parts:unknown[]){return parts.map(p=>{const value=String(p);return Array.from(value).length+'.'+value}).join(',')+';'}

export function BrowserDesktop({resourceId,bastionId,name,onClose}:{resourceId:string;bastionId:string;name:string;onClose:()=>void}){
 const surface=useRef<HTMLDivElement>(null),[status,setStatus]=useState('Connecting'),[error,setError]=useState(''),[attempt,setAttempt]=useState(0);
 useEffect(()=>{
  let stopped=false,ws:WebSocket|null=null,client:any=null,keyboard:any=null,sid='',observer:ResizeObserver|null=null;
  setStatus('Connecting');setError('');
  async function connect(){try{
   await loadLibrary();if(stopped)return;const G=window.Guacamole;
   const response=await fetch('/api/resources/'+resourceId+'/desktop-sessions',{method:'POST',headers:{'Content-Type':'application/json','X-Kzure-Request':'portal'},body:JSON.stringify({bastionId})});const grant=await response.json();if(!response.ok)throw new Error(grant.error||'Desktop admission failed');sid=grant.id;if(stopped){void fetch('/api/desktop-sessions/'+sid,{method:'DELETE',headers:{'X-Kzure-Request':'portal'}});return}
   const tunnel=new G.Tunnel();const parser=new G.Parser();
   tunnel.connect=()=>{
    tunnel.setState(G.Tunnel.State.CONNECTING);ws=new WebSocket(`${location.protocol==='https:'?'wss:':'ws:'}//${location.host}/api/desktop-sessions/${sid}/stream`,['guacamole']);
    ws.onopen=()=>ws?.send(JSON.stringify({type:'auth',ticket:grant.ticket,width:1280,height:800}));
    parser.oninstruction=(opcode:string,args:string[])=>{if(opcode===''){tunnel.setUUID(args[0]);tunnel.setState(G.Tunnel.State.OPEN)}else tunnel.oninstruction?.(opcode,args)};
    ws.onmessage=event=>{if(stopped)return;const raw=String(event.data);if(raw.startsWith('{')){try{const message=JSON.parse(raw);setError(message.data||message.error||'Desktop connection failed')}catch{setError('Invalid desktop response')}return}parser.receive(raw)};
    ws.onerror=()=>{if(!stopped)setError('The desktop connection failed. Check the desktop and bastion state.')};
    ws.onclose=()=>{tunnel.setState(G.Tunnel.State.CLOSED);if(!stopped)setStatus('Disconnected')};
   };
   tunnel.sendMessage=(...elements:unknown[])=>{if(ws?.readyState===WebSocket.OPEN)ws.send(encode(elements))};
   tunnel.disconnect=()=>{ws?.close();tunnel.setState(G.Tunnel.State.CLOSED)};
   client=new G.Client(tunnel);client.onerror=(message:{message:string})=>setError(message.message||'The desktop server rejected the connection.');client.onstatechange=(state:number)=>{if(!stopped){if(state===3)setStatus('Connected');if(state===5)setStatus('Disconnected')}};
   const display=client.getDisplay();const element=display.getElement();surface.current?.appendChild(element);
   const mouse=new G.Mouse(element);mouse.onmousedown=mouse.onmouseup=mouse.onmousemove=(state:unknown)=>client.sendMouseState(state);
   keyboard=new G.Keyboard(surface.current);keyboard.onkeydown=(keysym:number)=>{client.sendKeyEvent(1,keysym);return false};keyboard.onkeyup=(keysym:number)=>client.sendKeyEvent(0,keysym);
   const fit=()=>{if(surface.current&&display.getWidth()>0)display.scale(Math.min(1,surface.current.clientWidth/display.getWidth()))};display.onresize=fit;observer=new ResizeObserver(fit);if(surface.current)observer.observe(surface.current);
   client.connect('');surface.current?.focus();
  }catch(e){if(!stopped){setError((e as Error).message);setStatus('Disconnected')}}}
  void connect();
  return()=>{stopped=true;keyboard?.reset();client?.disconnect();ws?.close();observer?.disconnect();surface.current?.replaceChildren();if(sid)void fetch('/api/desktop-sessions/'+sid,{method:'DELETE',headers:{'X-Kzure-Request':'portal'},keepalive:true})};
 },[resourceId,bastionId,attempt]);
 return <div className="desktop-overlay" role="dialog" aria-modal="true" aria-label={'Desktop '+name}><div className="desktop-toolbar"><div><Monitor size={19}/><b>{name}</b><span>{status}</span></div><div><button className="button secondary small-button" onClick={()=>setAttempt(n=>n+1)}><RefreshCw size={14}/>Reconnect</button><button className="icon-button" aria-label="Close desktop" onClick={onClose}><X size={20}/></button></div></div>{error&&<div className="form-error" role="alert">{error}</div>}<div className="desktop-surface" ref={surface} tabIndex={0} aria-label="Remote desktop display" onMouseDown={()=>surface.current?.focus()} onBlur={()=>{}}/><p>Click the desktop to use keyboard and mouse. Clipboard, local drives, audio and file redirection are disabled. Closing disconnects; your desktop files stay in its persistent home.</p></div>
}
