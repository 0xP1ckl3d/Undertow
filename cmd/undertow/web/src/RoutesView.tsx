import {useEffect, useMemo, useState} from 'react';
import {Computer, Route as RouteIcon} from 'lucide-react';
import {api, type Agent, type Status} from './api';

type LocalRoute = {prefix:string;agent_id:string;manual:boolean;disabled:boolean;installed:boolean};
type LocalClient = {session_id:string;operator_only:boolean;vpn:boolean;internal:boolean}|null;

function RoutePicker({agents,agentID,setAgentID,prefix,setPrefix,custom,setCustom}:{agents:Agent[];agentID:string;setAgentID:(value:string)=>void;prefix:string;setPrefix:(value:string)=>void;custom:boolean;setCustom:(value:boolean)=>void}) {
  const agent=agents.find(a=>a.id===agentID);
  return <div className="route-picker"><label>Agent<select value={agentID} onChange={event=>{setAgentID(event.target.value);setPrefix('');setCustom(false)}}><option value="">Select an agent</option>{agents.map(a=><option key={a.id} value={a.id}>{a.nickname||a.hostname||a.id.slice(0,16)} · {a.id.slice(0,8)}</option>)}</select></label>
    <div className="route-source"><button type="button" className={!custom?'active':''} onClick={()=>{setCustom(false);setPrefix('')}}>Advertised</button><button type="button" className={custom?'active':''} onClick={()=>{setCustom(true);setPrefix('')}}>Custom CIDR</button></div>
    <label>Network prefix{custom?<input aria-label="Custom CIDR" placeholder="10.40.0.0/16" value={prefix} onChange={event=>setPrefix(event.target.value)}/>:<select aria-label="Advertised CIDR" value={prefix} disabled={!agentID} onChange={event=>setPrefix(event.target.value)}><option value="">Select an advertised prefix</option>{agent?.advertised_routes?.map(value=><option key={value} value={value}>{value}</option>)}</select>}</label>
    {custom&&<small>Manual routes can reach a network the agent has not advertised. Confirm the target network before adding one.</small>}
    {!custom&&agentID&&!agent?.advertised_routes?.length&&<small>This agent currently advertises no routes.</small>}
  </div>;
}

export function RoutesView({status,client}:{status:Status|null;client:LocalClient}) {
  const agents=status?.agents||[];
  const selectableAgents=agents.filter(agent=>agent.online!==false||agent.connection_state==='sleeping');
  const [localRoutes,setLocalRoutes]=useState<LocalRoute[]>([]);
  const [agentID,setAgentID]=useState('');
  const [prefix,setPrefix]=useState('');
  const [custom,setCustom]=useState(false);
  const [serverAgentID,setServerAgentID]=useState('');
  const [serverPrefix,setServerPrefix]=useState('');
  const [serverCustom,setServerCustom]=useState(false);
  const [busy,setBusy]=useState(false);
  const [error,setError]=useState('');
  const [notice,setNotice]=useState('');
  useEffect(()=>{api<LocalRoute[]>('/client/routes').then(setLocalRoutes).catch(e=>setError(String(e)))},[status]);
  const otherClients=useMemo(()=>status?.clients.flatMap(c=>(c.accepted_routes||[]).map(r=>({client:c,route:r})))||[],[status]);
  const run=async(action:()=>Promise<unknown>,message:string)=>{setBusy(true);setError('');setNotice('');try{await action();setLocalRoutes(await api<LocalRoute[]>('/client/routes'));setNotice(message)}catch(e){setError(String(e))}finally{setBusy(false)}};
  const canInstall=!client?.operator_only&&!!client?.session_id;
  return <div className="routes-layout">
    <section className="panel routes-primary"><h2>Routes accepted by this client</h2><p className="route-explanation">Accepting a route binds it to this client session and installs it locally. It appears in the topology only after acceptance.</p>
      {!canInstall&&<div className="route-mode-note">This client is in operator-only mode. Route installation needs a connected VPN client; current routes remain visible below.</div>}
      <RoutePicker agents={selectableAgents} agentID={agentID} setAgentID={setAgentID} prefix={prefix} setPrefix={setPrefix} custom={custom} setCustom={setCustom}/>
      <button className="route-primary-button" disabled={busy||!canInstall||!selectableAgents.some(a=>a.id===agentID)||!prefix} onClick={()=>run(()=>api('/client/routes','POST',{prefix,agent_id:agentID,manual:custom}),`Accepted ${prefix} on this client`)}>Accept and install route</button>
      <div className="route-section-title">Saved local routes <span>{localRoutes.length}</span></div>
      {localRoutes.length?localRoutes.map(route=><div className="route-item" key={route.prefix}><div><strong>{route.prefix}</strong><small>via {agents.find(a=>a.id===route.agent_id)?.hostname||route.agent_id.slice(0,16)} · {route.manual?'custom':'advertised'}</small></div><span className={'badge '+(route.installed?'good':'muted')}>{route.disabled?'Off':route.installed?'Installed':'Pending'}</span><button disabled={busy||route.disabled&& !canInstall} onClick={()=>run(()=>api('/client/routes','PUT',{prefix:route.prefix,agent_id:route.agent_id,enabled:route.disabled}),`${route.disabled?'Enabled':'Disabled'} ${route.prefix}`)}>{route.disabled?'Enable':'Disable'}</button><button disabled={busy} onClick={()=>run(()=>api(`/client/routes?prefix=${encodeURIComponent(route.prefix)}&agent_id=${encodeURIComponent(route.agent_id)}`,'DELETE'),`Removed ${route.prefix}`)}>Remove</button></div>):<div className="routes-empty">No routes accepted by this client.</div>}
    </section>
    <div className="routes-side"><section className="panel"><h2>Server configured routes</h2><p className="route-explanation">Server routes are separate from each client’s accepted paths. They appear in the topology only when a client accepts them.</p><RoutePicker agents={selectableAgents} agentID={serverAgentID} setAgentID={setServerAgentID} prefix={serverPrefix} setPrefix={setServerPrefix} custom={serverCustom} setCustom={setServerCustom}/><button className="route-primary-button" disabled={busy||!serverPrefix||!selectableAgents.some(a=>a.id===serverAgentID)} onClick={()=>run(()=>api('/routes','POST',{prefix:serverPrefix,agent_id:serverAgentID}),`Added server route ${serverPrefix}`)}>Add server route</button>
        {status?.routes.length?status.routes.map(route=><div className="route-item" key={route.prefix}><RouteIcon size={15}/><div><strong>{route.prefix}</strong><small>via {agents.find(a=>a.id===route.agent_id)?.hostname||route.agent_id.slice(0,16)}</small></div><span className={'badge '+(route.active?'good':'muted')}>{route.active?'Active':'Inactive'}</span><button disabled={busy} onClick={()=>run(()=>api(`/routes?prefix=${encodeURIComponent(route.prefix)}`,'DELETE'),`Removed server route ${route.prefix}`)}>Remove</button></div>):<div className="routes-empty">No server routes configured.</div>}
      </section><section className="panel"><h2>Accepted by connected clients</h2>{otherClients.length?otherClients.map(({client:c,route})=><div className="route-item" key={`${c.session_id}:${route.prefix}`}><Computer size={15}/><div><strong>{route.prefix}</strong><small>{c.hostname||c.id.slice(0,16)} via {agents.find(a=>a.id===route.agent_id)?.hostname||route.agent_id.slice(0,12)}</small></div></div>):<div className="routes-empty">No clients have accepted a route.</div>}</section></div>
    {(error||notice)&&<div className={'route-message '+(error?'error':'')}>{error||notice}</div>}
  </div>;
}
