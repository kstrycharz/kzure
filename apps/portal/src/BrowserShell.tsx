import {useEffect,useRef,useState} from 'react';
import {Terminal as Xterm} from '@xterm/xterm';
import {FitAddon} from '@xterm/addon-fit';
import {Maximize2,Minimize2,RefreshCw,Square,Terminal} from 'lucide-react';
import '@xterm/xterm/css/xterm.css';

type Session={id:string;user:string;state:string;createdAt:string;reason:string};
type Member={id:string;name:string;email:string;role:string;granted:boolean};
async function request<T>(path:string,method='GET'):Promise<T>{
 const response=await fetch('/api'+path,{method,headers:{'X-Kzure-Request':'portal'}});
 const data=await response.json();if(!response.ok)throw new Error(data.error||'Request failed');return data;
}

export function BrowserShell({resourceId,name,running}:{resourceId:string;name:string;running:boolean}){
 const mount=useRef<HTMLDivElement>(null),terminal=useRef<Xterm|null>(null),socket=useRef<WebSocket|null>(null),sessionId=useRef('');
 const [attempt,setAttempt]=useState(0),[status,setStatus]=useState('Disconnected'),[error,setError]=useState(''),[full,setFull]=useState(false),[access,setAccess]=useState<{canConnect:boolean;canManage:boolean}|null>(null),[sessions,setSessions]=useState<Session[]>([]),[member,setMember]=useState(''),[members,setMembers]=useState<Member[]>([]),[accessNote,setAccessNote]=useState('');
 const reloadSessions=()=>request<Session[]>('/resources/'+resourceId+'/terminal-sessions').then(setSessions).catch(()=>{});
 useEffect(()=>{setAccess(null);request<{canConnect:boolean;canManage:boolean}>('/resources/'+resourceId+'/shell-access').then(setAccess).catch(e=>setError(e.message));reloadSessions()},[resourceId]);
 const reloadMembers=()=>request<Member[]>('/resources/'+resourceId+'/shell-members').then(setMembers).catch(e=>setAccessNote(e.message));
 useEffect(()=>{if(access?.canManage)reloadMembers()},[resourceId,access?.canManage]);
 useEffect(()=>{
  if(!mount.current)return;
  const term=new Xterm({cursorBlink:true,fontFamily:'Consolas, "Liberation Mono", monospace',fontSize:13,scrollback:3000,theme:{background:'#101b2d',foreground:'#dde7f5',cursor:'#7dd3fc'},allowProposedApi:false,allowTransparency:false});
  const fit=new FitAddon();term.loadAddon(fit);term.open(mount.current);terminal.current=term;
  // Terminal output cannot trigger clipboard writes or open links automatically.
  term.parser.registerOscHandler(52,()=>true);
  let stopped=false,resizeTimer:number|undefined;
  function resize(){try{fit.fit();const ws=socket.current;if(ws?.readyState===WebSocket.OPEN)ws.send(JSON.stringify({type:'resize',cols:term.cols,rows:term.rows}))}catch{}}
  const observer=new ResizeObserver(()=>{window.clearTimeout(resizeTimer);resizeTimer=window.setTimeout(resize,100)});observer.observe(mount.current);resize();
  const input=term.onData(data=>{const ws=socket.current;if(ws?.readyState===WebSocket.OPEN){if(ws.bufferedAmount>65536){setError('Connection is busy; wait before entering more input.');return}ws.send(JSON.stringify({type:'input',data}))}});
  const stop=()=>{stopped=true;socket.current?.close();socket.current=null;const sid=sessionId.current;sessionId.current='';if(sid)void fetch('/api/terminal-sessions/'+sid,{method:'DELETE',headers:{'X-Kzure-Request':'portal'},keepalive:true});input.dispose();observer.disconnect();window.clearTimeout(resizeTimer);term.dispose();terminal.current=null};
  if(!access?.canConnect||!running){term.writeln(running?'Shell access is required. Ask a tenant owner for access.':'Start this workspace to open a shell.');return stop}
  setStatus('Connecting');setError('');term.writeln('Connecting to '+name+'…');
  request<{id:string;ticket:string}>('/resources/'+resourceId+'/terminal-sessions','POST').then(grant=>{
   if(stopped){void request('/terminal-sessions/'+grant.id,'DELETE');return}sessionId.current=grant.id;
   const ws=new WebSocket(`${location.protocol==='https:'?'wss:':'ws:'}//${location.host}/api/terminal-sessions/${grant.id}/stream`);socket.current=ws;
   ws.onopen=()=>{ws.send(JSON.stringify({type:'auth',ticket:grant.ticket}));resize()};
   ws.onmessage=event=>{if(stopped)return;try{const frame=JSON.parse(event.data);if(frame.type==='output'){const bytes=Uint8Array.from(atob(frame.data),(c:string)=>c.charCodeAt(0));term.write(bytes,()=>{if(ws.readyState===WebSocket.OPEN)ws.send(JSON.stringify({type:"ack"}))})}else if(frame.type==='ready'){setStatus('Connected');term.clear();resize();term.focus();reloadSessions()}else if(frame.type==='error'){setError(frame.data);setStatus('Disconnected')}else if(frame.type==='exit'){setStatus('Disconnected');term.writeln('\r\nSession ended (exit '+frame.code+').');reloadSessions()}}catch{setError('The terminal received an invalid response.')}};
   ws.onerror=()=>{if(!stopped)setError('The terminal connection could not be established.')};
   ws.onclose=()=>{if(!stopped){setStatus('Disconnected');term.writeln('\r\nDisconnected. Choose Reconnect to start a new shell.');reloadSessions()}};
  }).catch(e=>{if(!stopped){setError(e.message);setStatus('Disconnected')}});
  return stop;
 },[resourceId,access?.canConnect,running,attempt]);
 function disconnect(){socket.current?.close();setStatus('Disconnected');const sid=sessionId.current;if(sid)void request('/terminal-sessions/'+sid,'DELETE').then(reloadSessions)}
 async function changeAccess(revoke=false){setAccessNote('');try{await request('/resources/'+resourceId+'/shell-access/'+encodeURIComponent(member.trim()),revoke?'DELETE':'PUT');setAccessNote(revoke?'Access revoked. Active sessions will close within 5 seconds.':'Shell access granted.');reloadSessions();reloadMembers()}catch(e){setAccessNote((e as Error).message)}}
 return <section className={`browser-shell ${full?'shell-fullscreen':''}`} aria-label={'Browser shell for '+name}>
  <div className="shell-toolbar"><div><Terminal size={16}/><b>{name}</b><span className={status==='Connected'?'shell-connected':''}>{status}</span></div><div><button className="icon-button" aria-label="Reconnect shell" title="Reconnect" disabled={!running||!access?.canConnect||status==='Connecting'} onClick={()=>setAttempt(n=>n+1)}><RefreshCw size={16}/></button><button className="icon-button" aria-label="Disconnect shell" title="Disconnect" disabled={status==='Disconnected'} onClick={disconnect}><Square size={15}/></button><button className="icon-button" aria-label={full?'Exit fullscreen shell':'Fullscreen shell'} onClick={()=>setFull(v=>!v)}>{full?<Minimize2 size={16}/>:<Maximize2 size={16}/>}</button></div></div>
  <p className="shell-info">User 1000 · /data persists · 15-minute idle limit · 2-hour session limit</p>
  {error&&<div className="form-error" role="alert">{error}</div>}
  <div ref={mount} className="shell-terminal"/>
  <p className="field-note">Input runs inside this workspace. Ctrl+C interrupts a command. Reconnecting starts a new shell. Connection activity is audited; terminal input is not recorded.</p>
  <div className="section-line"><h3>Recent sessions</h3><button className="icon-button" aria-label="Refresh shell sessions" onClick={reloadSessions}><RefreshCw size={15}/></button></div>
  <div className="shell-sessions">{sessions.length===0?<p className="muted">No sessions yet.</p>:sessions.map(s=><div key={s.id}><span><b>{s.user}</b><small>{new Date(s.createdAt).toLocaleString()} · {s.reason||s.state}</small></span>{['Pending','Active'].includes(s.state)&&<button className="button secondary small-button" onClick={()=>request('/terminal-sessions/'+s.id,'DELETE').then(()=>{reloadSessions();if(s.id===sessionId.current)disconnect()}).catch(e=>setError(e.message))}>End session</button>}</div>)}</div>
  {access?.canManage&&<details className="shell-access"><summary>Manage member shell access</summary><p className="field-note">Owners have access. Contributors require a workspace grant. Readers cannot connect.</p><label>Contributor<select value={member} onChange={e=>setMember(e.target.value)}><option value="">Select a member</option>{members.filter(m=>m.role==='contributor').map(m=><option key={m.id} value={m.id}>{m.name} ({m.email}){m.granted?' · access granted':''}</option>)}</select></label><div className="shell-access-actions"><button className="button secondary small-button" disabled={!member} onClick={()=>changeAccess()}>Grant access</button><button className="button secondary small-button" disabled={!member} onClick={()=>changeAccess(true)}>Revoke access</button></div>{accessNote&&<p role="status">{accessNote}</p>}</details>}
 </section>
}
