import {useEffect, useState} from 'react';
import {Cable, Power, Radio, RefreshCw, ShieldAlert} from 'lucide-react';
import {api, type Agent} from './api';

type Forward={agent_id:string;bind:string;target:string;active?:boolean};
type Listener={transport:string;listen:string;network:string;tls_mode?:string;sessions:number;agents:number;clients:number};
type ClientMode={session_id:string;operator_only:boolean;vpn:boolean;internal:boolean;transport:string};
const label=(agent:Agent)=>agent.nickname||agent.hostname||agent.id.slice(0,16);

export function AgentLifecycle({agent,onRefresh}:{agent:Agent;onRefresh:()=>Promise<void>}){
  const [pending,setPending]=useState<'kill'|'shutdown'|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState('');
  useEffect(()=>{setPending(null);setError('')},[agent.id]);
  const perform=async()=>{if(!pending)return;setBusy(true);setError('');try{
    if(pending==='shutdown')await api(`/agents/${encodeURIComponent(agent.id)}/shutdown`,'POST',{});
    else await api(`/agents/${encodeURIComponent(agent.id)}/session/kill`,'POST',{});
    setPending(null);await onRefresh();
  }catch(e){setError(String(e))}finally{setBusy(false)}};
  return <section className="control-card wide"><div className="control-heading"><Power size={17}/><div><h3>Agent lifecycle</h3><p>These actions affect this connected agent only.</p></div></div>
    <div className="control-actions"><button disabled={!agent.session_id||busy} onClick={()=>setPending('kill')}>Kill current session</button><button className="danger" disabled={busy} onClick={()=>setPending('shutdown')}>Shut down agent</button></div>
    {pending&&<div className="control-confirm" role="alertdialog" aria-label="Confirm agent lifecycle action"><ShieldAlert size={18}/><div><strong>{pending==='kill'?`Close session ${agent.session_id}?`:`Shut down ${label(agent)}?`}</strong><p>{pending==='kill'?'The connection will close. The agent may reconnect using its existing configuration.':'The packaged agent will exit and cannot reconnect until started again.'}</p></div><button className="danger" disabled={busy} onClick={perform}>{busy?'Working…':'Confirm'}</button><button disabled={busy} onClick={()=>setPending(null)}>Cancel</button></div>}
    {error&&<p className="control-error" role="alert">{error}</p>}
  </section>
}

export function ForwardsView({agents,revision}:{agents:Agent[];revision?:string}){
  const [items,setItems]=useState<Forward[]>([]),[agent,setAgent]=useState(''),[bind,setBind]=useState(''),[target,setTarget]=useState(''),[busy,setBusy]=useState(false),[error,setError]=useState(''),[remove,setRemove]=useState<Forward|null>(null);
  const refresh=()=>api<Forward[]|null>('/forwards').then(result=>setItems(result||[])).catch(e=>setError(String(e)));
  useEffect(()=>{refresh()},[revision]);
  const action=async(fn:()=>Promise<unknown>)=>{setBusy(true);setError('');try{await fn();await refresh()}catch(e){setError(String(e))}finally{setBusy(false)}};
  return <div className="operations-grid"><section className="panel control-panel"><div className="control-heading"><Cable size={18}/><div><h2>Agent TCP forwards</h2><p>Open a listener on the selected agent and forward connections to this client’s target address.</p></div></div>
    <div className="control-fields"><label>Agent<select value={agent} onChange={e=>setAgent(e.target.value)}><option value="">Select connected agent</option>{agents.map(a=><option key={a.id} value={a.id}>{label(a)} · {a.id.slice(0,8)}</option>)}</select></label><label>Agent bind address<input value={bind} onChange={e=>setBind(e.target.value)} placeholder="127.0.0.1:8080"/></label><label>Client target address<input value={target} onChange={e=>setTarget(e.target.value)} placeholder="127.0.0.1:8000"/></label><button disabled={busy||!agents.some(a=>a.id===agent)||!bind.trim()||!target.trim()} onClick={()=>action(async()=>{await api('/forwards','POST',{agent_id:agent,bind:bind.trim(),target:target.trim()});setBind('');setTarget('')})}>Start forward</button></div>
    <p className="control-note">Bind and target are interpreted by Undertow’s existing forward service. Review both addresses before starting a listener.</p></section>
    <section className="panel control-panel"><div className="control-heading"><Radio size={18}/><div><h2>Active forwards</h2><p>Scoped to this client session.</p></div><button className="control-refresh" title="Refresh forwards" onClick={refresh}><RefreshCw size={15}/></button></div>
      {items.length?items.map(item=><div className="control-list-row" key={item.agent_id+item.bind}><div><strong>{item.bind}</strong><small>{agents.find(a=>a.id===item.agent_id)?.hostname||item.agent_id.slice(0,14)} → {item.target}</small></div><button disabled={busy} onClick={()=>setRemove(item)}>Stop</button></div>):<p className="control-empty">No active forwards for this client.</p>}
      {remove&&<div className="control-confirm" role="alertdialog" aria-label="Confirm forward removal"><ShieldAlert size={18}/><div><strong>Stop {remove.bind}?</strong><p>New connections through this forward will no longer be accepted.</p></div><button className="danger" disabled={busy} onClick={()=>action(async()=>{await api(`/forwards?agent_id=${encodeURIComponent(remove.agent_id)}&bind=${encodeURIComponent(remove.bind)}`,'DELETE');setRemove(null)})}>Stop forward</button><button onClick={()=>setRemove(null)}>Cancel</button></div>}
      {error&&<p className="control-error" role="alert">{error}</p>}
    </section></div>
}

export function RoutingModeView({client,onChange}:{client:ClientMode|null;onChange:()=>Promise<void>}){
  const [busy,setBusy]=useState(''),[error,setError]=useState('');
  const change=async(name:'vpn'|'internal',enabled:boolean)=>{setBusy(name);setError('');try{await api(`/client/mode/${name}`,'PUT',{enabled});await onChange()}catch(e){setError(String(e))}finally{setBusy('')}};
  return <section className="panel control-panel"><div className="control-heading"><Radio size={18}/><div><h2>Client routing modes</h2><p>Modes apply to this local client. Existing accepted routes are managed below.</p></div></div>
    {client?.operator_only&&<p className="control-note">This is an operator-only client with no TUN device. Start a VPN client to install routes.</p>}
    {(['vpn','internal'] as const).map(name=><div className="mode-row" key={name}><div><strong>{name==='vpn'?'Internet egress (VPN)':'Internal agent routing'}</strong><small>{name==='vpn'?'Installs or removes the Undertow IPv4 Internet routes.':'Changes server routing for new flows and synchronizes local internal routes.'}</small></div><span className={'badge '+(client?.[name]?'good':'muted')}>{client?.[name]?'On':'Off'}</span><button disabled={!client?.session_id||client.operator_only||!!busy||!!client?.[name]} onClick={()=>change(name,true)}>On</button><button disabled={!client?.session_id||client.operator_only||!!busy||!client?.[name]} onClick={()=>change(name,false)}>Off</button></div>)}
    {error&&<p className="control-error" role="alert">{error}</p>}
  </section>
}

export function TransportsView({revision,currentCarrier}:{revision?:string;currentCarrier?:string}){
  const [items,setItems]=useState<Listener[]>([]),[name,setName]=useState(''),[listen,setListen]=useState(''),[tlsMode,setTLSMode]=useState('self-signed'),[tlsCert,setTLSCert]=useState(''),[tlsKey,setTLSKey]=useState(''),[busy,setBusy]=useState(false),[error,setError]=useState(''),[stop,setStop]=useState<{item:Listener;force:boolean}|null>(null);
  const refresh=()=>api<Listener[]|null>('/transports').then(result=>setItems(result||[])).catch(e=>setError(String(e)));
  useEffect(()=>{refresh()},[revision]);
  const action=async(fn:()=>Promise<unknown>)=>{setBusy(true);setError('');try{await fn();await refresh()}catch(e){setError(String(e))}finally{setBusy(false)}};
  return <div className="operations-grid"><section className="panel control-panel"><div className="control-heading"><Radio size={18}/><div><h2>Server carrier listeners</h2><p>Listeners are owned by the Undertow server and shared by all operators.</p></div><button className="control-refresh" title="Refresh listeners" onClick={refresh}><RefreshCw size={15}/></button></div>
    {items.length?items.map(item=><div className="control-list-row" key={item.transport}><div><strong>{item.transport.toUpperCase()} <span>{item.listen}</span></strong><small>{item.sessions} sessions · {item.agents} agents · {item.clients} clients · {item.tls_mode||'TLS mode unspecified'}</small></div><button disabled={busy||item.transport===currentCarrier} title={item.transport===currentCarrier?'This client is connected through this carrier':''} onClick={()=>setStop({item,force:false})}>{item.transport===currentCarrier?'Current carrier':'Stop'}</button></div>):<p className="control-empty">No transport listeners are running.</p>}
    {stop&&<div className="control-confirm" role="alertdialog" aria-label="Confirm transport stop"><ShieldAlert size={18}/><div><strong>{stop.force?'Force stop':'Stop'} {stop.item.transport.toUpperCase()}?</strong><p>{stop.force?`This disconnects ${stop.item.sessions} current session(s). Agents and clients may reconnect through another listener.`:`A normal stop requires zero active sessions. Current sessions: ${stop.item.sessions}.`}</p>{!stop.force&&stop.item.sessions>0&&<button onClick={()=>setStop({...stop,force:true})}>Review force stop</button>}</div><button className="danger" disabled={busy||!stop.force&&stop.item.sessions>0} onClick={()=>action(async()=>{await api(`/transports/${encodeURIComponent(stop.item.transport)}${stop.force?'?force=true':''}`,'DELETE');setStop(null)})}>Confirm {stop.force?'force stop':'stop'}</button><button onClick={()=>setStop(null)}>Cancel</button></div>}
    {error&&<p className="control-error" role="alert">{error}</p>}
  </section><section className="panel control-panel"><div className="control-heading"><Power size={18}/><div><h2>Start listener</h2><p>Starts an existing Undertow carrier. Enter server-side TLS paths when using certificate files.</p></div></div>
    <div className="control-fields"><label>Carrier<select value={name} onChange={e=>setName(e.target.value)}><option value="">Select carrier</option>{['quic','websocket','dns'].filter(value=>!items.some(item=>item.transport===value)).map(value=><option key={value} value={value}>{value.toUpperCase()}</option>)}</select></label><label>Listen address<input value={listen} onChange={e=>setListen(e.target.value)} placeholder="Optional · server default"/></label>{name!=='dns'&&<label>TLS mode<select value={tlsMode} onChange={e=>setTLSMode(e.target.value)}><option value="self-signed">Self-signed</option><option value="files">Certificate files</option></select></label>}{name!=='dns'&&tlsMode==='files'&&<><label>Server certificate path<input value={tlsCert} onChange={e=>setTLSCert(e.target.value)}/></label><label>Server key path<input value={tlsKey} onChange={e=>setTLSKey(e.target.value)}/></label></>}
      <button disabled={busy||!name||tlsMode==='files'&&name!=='dns'&&(!tlsCert.trim()||!tlsKey.trim())} onClick={()=>action(async()=>{await api(`/transports/${name}`,'POST',{listen:listen.trim(),...(name==='dns'?{}:tlsMode==='files'?{tls_mode:'certificate',tls_cert:tlsCert.trim(),tls_key:tlsKey.trim()}:{tls_mode:'self-signed'})});setName('');setListen('')})}>Start listener</button></div>
  </section></div>
}
