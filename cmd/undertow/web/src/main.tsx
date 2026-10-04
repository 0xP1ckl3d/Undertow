import {Component, useEffect, useRef, useState, type ReactNode} from 'react';
import {createRoot} from 'react-dom/client';
import {Terminal} from '@xterm/xterm';
import {Boxes, Cable, Clock3, Computer, Download, ListTodo, Network, Radio, RefreshCw, Route as RouteIcon, Settings, SquareTerminal, X} from 'lucide-react';
import {api, connect, csrfToken, type Agent, type Job, type Status, type Topology} from './api';
import {TopologyGraph} from './TopologyGraph';
import {HostResults} from './HostResults';
import {RoutesView} from './RoutesView';
import {PayloadView} from './PayloadView';
import {ModulesView} from './ModulesView';
import {JobsView} from './JobsView';
import {ScreenshotsView} from './ScreenshotsView';
import {FilesView} from './FilesView';
import {AgentLifecycle, ForwardsView, RoutingModeView, TransportsView} from './OperationalControls';
import {WorkerLogsView} from './WorkerLogsView';
import {TransfersView} from './TransfersView';
import '@xyflow/react/dist/style.css';
import '@xterm/xterm/css/xterm.css';
import './style.css';
import './workflows.css';
import './screenshots.css';
import './files.css';
import './controls.css';

type View = 'topology'|'agents'|'jobs'|'transfers'|'routes'|'relays'|'forwards'|'modules'|'payloads'|'history'|'settings';
type Audit = {id:string;at:string;action:string;target:string;client_id?:string;client_session_id?:string;operator_id?:string;display_name?:string;source:string;identity_trust:string;status:number};
const nav: {id:View; label:string; icon:typeof Network}[] = [
  {id:'topology',label:'Topology',icon:Network},{id:'agents',label:'Agents',icon:Computer},{id:'jobs',label:'Jobs',icon:ListTodo},{id:'transfers',label:'Transfers',icon:Download},
  {id:'routes',label:'Routes',icon:RouteIcon},{id:'relays',label:'Relays',icon:Cable},{id:'forwards',label:'Forwards',icon:Cable},{id:'modules',label:'Modules',icon:Boxes},{id:'payloads',label:'Payloads',icon:Boxes},{id:'history',label:'History',icon:Clock3},{id:'settings',label:'Settings',icon:Settings}
];
function formatTime(value?:string){return value?new Date(value).toLocaleString():'—'}
function short(value:string){return value.length>18?value.slice(0,18)+'…':value}

function App(){
  const [ready,setReady]=useState(false), [authError,setAuthError]=useState(''), [error,setError]=useState('');
  const refreshSequence=useRef(0);
  const [view,setView]=useState<View>('topology'), [status,setStatus]=useState<Status|null>(null), [topology,setTopology]=useState<Topology|null>(null);
  const [relayTarget,setRelayTarget]=useState<{agentID:string;bind:string}|null>(null);
  const [selected,setSelected]=useState<string|null>(null), [tab,setTab]=useState('Console'), [jobs,setJobs]=useState<Job[]>([]), [relays,setRelays]=useState<{agent_id:string;bind:string}[]>([]), [history,setHistory]=useState<Audit[]>([]);
  const [client,setClient]=useState<{session_id:string;transport:string;server_address:string;operator_only:boolean;vpn:boolean;internal:boolean}|null>(null), [busy,setBusy]=useState(false);
  const snapshot=<T,>(path:string):Promise<T>=>{const controller=new AbortController();const timeout=window.setTimeout(()=>controller.abort(),8000);return api<T>(path,'GET',undefined,controller.signal).finally(()=>window.clearTimeout(timeout))};
  const refresh=async()=>{
    const sequence=++refreshSequence.current;
    const [s,t,j,r,c,h]=await Promise.allSettled([snapshot<Status>('/status'),snapshot<Topology>('/topology'),snapshot<Job[]>('/jobs'),snapshot<{agent_id:string;bind:string}[]>('/relays'),snapshot<{session_id:string;transport:string;server_address:string;operator_only:boolean;vpn:boolean;internal:boolean}>('/client'),snapshot<Audit[]>('/history')]);
    if(sequence!==refreshSequence.current)return;
    if(s.status==='fulfilled')setStatus(s.value);
    if(t.status==='fulfilled')setTopology(t.value);
    if(j.status==='fulfilled')setJobs(j.value);
    if(r.status==='fulfilled')setRelays(r.value);
    if(c.status==='fulfilled')setClient(c.value);
    if(h.status==='fulfilled')setHistory(h.value);
    setError(s.status==='rejected'?String(s.reason):'');
  };
  useEffect(()=>{connect().then(()=>{setReady(true);refresh()}).catch(e=>setAuthError(String(e)))},[]);
  useEffect(()=>{if(!ready)return;const source=new EventSource('/api/events');let timer:number|undefined;source.onopen=()=>{void refresh()};source.onerror=()=>{void refresh()};source.addEventListener('change',event=>{try{if(JSON.parse((event as MessageEvent).data).kind==='worker_log')return}catch{}if(timer)window.clearTimeout(timer);timer=window.setTimeout(()=>refresh(),150)});return()=>{source.close();if(timer)window.clearTimeout(timer)}},[ready]);
  const activeAgent=status?.agents.find(a=>a.id===selected);
  const act=async(fn:()=>Promise<unknown>)=>{setBusy(true);try{await fn();await refresh()}catch(e){setError(String(e))}finally{setBusy(false)}};
  if(authError)return <div className="auth-page"><div className="brand-mark">U</div><h1>Undertow</h1><p>{authError}</p><code>undertow client gui</code></div>;
  if(!ready)return <div className="auth-page">Connecting to local client…</div>;
  return <div className="app-shell">
    <aside className="nav-rail"><div className="brand"><div className="brand-mark">U</div><div><strong>UNDERTOW</strong><small>OPERATOR CLIENT</small></div></div>
      <div className="nav-heading">WORKSPACE</div><nav>{nav.map(item=><button key={item.id} aria-label={item.label} title={item.label} className={'nav-item '+(view===item.id?'selected':'')} onClick={()=>setView(item.id)}><item.icon size={17}/><span>{item.label}</span>{item.id==='agents'&&status?<em>{status.agents.length}</em>:null}</button>)}</nav>
      <div className="nav-footer"><div className={'connection-dot '+(client?.session_id?'online':'offline')}/><div><strong>{client?.session_id?'Connected':'Disconnected'}</strong><small>{client?.session_id?`Client session ${client.session_id}`:'Waiting for server'}</small></div></div>
    </aside>
    <div className="main-column"><header className="topbar"><div className="breadcrumb"><span>OPERATIONS</span><span className="slash">/</span><strong>{nav.find(n=>n.id===view)?.label}</strong>{activeAgent?<><span className="slash">/</span><span>{activeAgent.hostname||short(activeAgent.id)}</span></>:null}</div><div className="top-actions"><span className="carrier-pill"><Radio size={14}/>{client?.transport||'No carrier'}</span><button className="icon-button" title="Refresh" onClick={refresh}><RefreshCw size={17}/></button></div></header>
      {error&&<div className="error-banner"><span>{error}</span><button onClick={()=>setError('')} aria-label="Dismiss"><X size={15}/></button></div>}
      <main className="content">
        {view==='topology'&&<><PageHeader title="Network topology" description="Observed sessions, relay paths, routes, and operator clients." count={topology?.nodes.length} /><div className="topology-layout"><div className="topology-frame"><TopologyGraph topology={topology} localClient={client} onAgent={id=>{setSelected(id);setTab('Console');setView('agents')}}/></div><div className="side-stack"><section className="panel"><h2>Connected peers</h2><Metric label="Agents" value={status?.agents.filter(a=>a.online!==false).length||0}/><Metric label="Operator clients" value={status?.clients.length||0}/><Metric label="Active relays" value={relays.length}/></section><section className="panel"><h2>Carrier listeners</h2>{(topology?.nodes.find(n=>n.kind==='server')?.carriers||['dns','quic','websocket'].map(transport=>({transport,active:!!status?.server.listeners?.some(l=>l.transport===transport),listen:status?.server.listeners?.find(l=>l.transport===transport)?.listen,sessions:status?.server.listeners?.find(l=>l.transport===transport)?.sessions}))).map(l=><div className={'listener-row '+(l.active?'':'inactive')} key={l.transport}><span className="state-indicator"/><div><strong>{l.transport.toUpperCase()}</strong><small>{l.active?l.listen:'Inactive'}</small></div><b>{l.active?l.sessions:'—'}</b></div>)}</section><section className="panel compact"><h2>Route legend</h2><Legend color="var(--amber)" label="Carrier / relay path"/><Legend color="var(--green)" label="Client accepted path"/><Legend color="#cd85dd" label="Active forward"/></section></div></div></>}
        {view==='agents'&&<><PageHeader title="Agents" description="Select an agent to open its workspace." count={status?.agents.filter(a=>a.online!==false).length}/><div className="agent-layout"><div className="agent-list panel"><div className="panel-title">Agent records · {status?.agents.filter(a=>a.online!==false).length||0} connected</div>{status?.agents.length?status.agents.map(a=><button key={a.id} className={'agent-row '+(selected===a.id?'active':'')+(a.online===false?' disconnected':'')} onClick={()=>{setSelected(a.id);setTab('Console')}}><span className="agent-icon"><Computer size={17}/></span><span className="agent-name"><strong>{a.hostname||short(a.id)}</strong><small>{a.online===false?'Disconnected · ':''}{a.os||'Unknown OS'} · {a.transport||'Unknown carrier'}</small></span><span className={"state-indicator "+(a.online===false?"offline":"")}/></button>):<Empty text="No agent records yet"/>}</div><div className="workspace panel">{activeAgent?<AgentWorkspace agent={activeAgent} jobs={jobs.filter(j=>j.agent_id===activeAgent.id)} tab={tab} setTab={setTab} act={act} busy={busy} onRefresh={refresh} revision={topology?.at} client={client} openRoutes={()=>setView('routes')}/>:<div className="workspace-empty"><Computer size={35}/><h2>Select an agent</h2><p>Agent details and operations will appear here.</p></div>}</div></div></>}
        {view==='jobs'&&<><PageHeader title="Jobs" description="Background execution and retained server output." count={jobs.length}/><JobsView jobs={jobs} onRefresh={refresh} offlineAgentIDs={status?.agents.filter(a=>a.online===false).map(a=>a.id)} onAgent={id=>{setSelected(id);setTab('Jobs');setView('agents')}}/></>}
        {view==='transfers'&&<><PageHeader title="Transfers" description="Server retained file transfer state and verification."/><TransfersView revision={topology?.at}/></>}
        {view==='routes'&&<><PageHeader title="Routes" description="Server routes and each client’s accepted paths." count={status?.routes.length}/><RoutingModeView client={client} onChange={refresh}/><RoutesView status={status} client={client}/></>}
        {view==='relays'&&<><PageHeader title="Relays" description="Listeners hosted by agents and child paths." count={relays.length}/><RelaysView agents={status?.agents.filter(a=>a.online!==false)||[]} relays={relays} act={act} busy={busy} onPayload={target=>{setRelayTarget(target);setView('payloads')}}/></>}
        {view==='forwards'&&<><PageHeader title="Forwards" description="Agent TCP listeners that forward connections to this client."/><ForwardsView agents={status?.agents.filter(a=>a.online!==false)||[]} revision={topology?.at}/></>}
        {view==='modules'&&<><PageHeader title="Module bank" description="Compiled modules loaded on this client. Select an agent before running one."/><ModulesView agents={status?.agents.filter(a=>a.online!==false)||[]} onJobs={()=>setView('jobs')}/></>}
        {view==='payloads'&&<><PageHeader title="Payloads" description="Existing profiles and built artifacts on the server."/><PayloadView revision={topology?.at} serverAddress={client?.server_address} publicHost={status?.server.public_host} clientTransport={client?.transport} relayTarget={relayTarget}/></>}
        {view==='history'&&<><PageHeader title="Operational history" description="Server recorded operator actions and their results." count={history.length}/><div className="panel table-panel"><HistoryTable records={history}/></div></>}
        {view==='settings'&&<SettingsView status={status} client={client} revision={topology?.at} onRefresh={refresh}/>}
      </main>
    </div>
  </div>
}
function PageHeader({title,description,count}:{title:string;description:string;count?:number}){return <div className="page-header"><div><h1>{title}</h1><p>{description}</p></div>{count!==undefined&&<div className="count-card"><strong>{count}</strong><span>TOTAL</span></div>}</div>}
function Metric({label,value}:{label:string;value:number}){return <div className="metric"><span>{label}</span><strong>{value}</strong></div>}
function Legend({color,label}:{color:string;label:string}){return <div className="legend"><span style={{background:color}}/>{label}</div>}
function Empty({text}:{text:string}){return <div className="empty">{text}</div>}
function HistoryTable({records}:{records:Audit[]}){return records.length?<table><thead><tr><th>Time</th><th>Action</th><th>Target</th><th>Operator claim</th><th>Client</th><th>Result</th></tr></thead><tbody>{records.map(r=><tr key={r.id}><td>{formatTime(r.at)}</td><td className="mono">{r.action}</td><td className="mono">{r.target}</td><td>{r.display_name||r.operator_id||'Unclaimed'}<small className="history-trust">{r.identity_trust.replaceAll('_',' ')}</small></td><td className="mono">{r.client_id?short(r.client_id):r.source}</td><td><span className={'badge '+(r.status===0?'warn':r.status<400?'good':'muted')}>{r.status||'Pending'}</span></td></tr>)}</tbody></table>:<Empty text="No operator actions recorded"/>}

function AgentWorkspace({agent,jobs,tab,setTab,act,busy,onRefresh,revision,client,openRoutes}:{agent:Agent;jobs:Job[];tab:string;setTab:(s:string)=>void;act:(fn:()=>Promise<unknown>)=>void;busy:boolean;onRefresh:()=>Promise<void>;revision?:string;client:{operator_only:boolean;session_id:string}|null;openRoutes:()=>void}){
  if(agent.online===false){
    const offlineTabs=['Overview','Console','Jobs','Screenshots','Host'];
    const current=offlineTabs.includes(tab)?tab:'Overview';
    return <><div className="workspace-header"><div className="workspace-avatar"><Computer size={25}/></div><div><div className="eyebrow">RETAINED AGENT RECORD</div><h2>{agent.hostname||agent.id}</h2><p>{agent.id}</p></div><span className="badge muted">Disconnected</span></div><div className="offline-notice">Disconnected {formatTime(agent.disconnected_at)}. Last known details and retained history remain available. Agent actions are unavailable.</div><div className="tabs">{offlineTabs.map(name=><button key={name} className={current===name?'active':''} onClick={()=>setTab(name)}>{name}</button>)}</div><div className="workspace-body offline-workspace">
      {current==='Overview'&&<div className="overview-grid"><Info label="Operating system" value={`${agent.os||'Unknown'} / ${agent.arch||'Unknown'}`}/><Info label="Last carrier" value={agent.transport||'Unknown'}/><Info label="Last remote address" value={agent.remote||'Unknown'}/><Info label="Observed public IP" value={agent.public_ip||'Not observed'}/><Info label="Relay parent" value={agent.via||'Direct'}/><Info label="Last seen" value={formatTime(agent.last_seen)}/><div className="wide"><h3>Last advertised routes</h3><div className="chips">{agent.advertised_routes?.map(prefix=><span key={prefix}>{prefix}</span>)||<span>None recorded</span>}</div></div><div className="wide"><h3>Last reported capabilities</h3><div className="chips">{agent.capabilities?.allowed?.map(capability=><span key={capability}>{capability}</span>)||<span>Not reported</span>}</div></div></div>}
      {current==='Console'&&<AgentCommandConsole agent={agent} openShell={()=>{}} readOnly/>}
      {current==='Jobs'&&<JobsView jobs={jobs} agentID={agent.id} onRefresh={onRefresh} readOnly/>}
      {current==='Screenshots'&&<ScreenshotsView agent={agent} revision={revision}/>}
      {current==='Host'&&<HostResults agentID={agent.id} sessionID={agent.session_id} offline/>}
    </div></>
  }
  const tabs=['Console','Overview','Files','Jobs','Screenshots','Modules','Host','Live shell'];
  return <><div className="workspace-header"><div className="workspace-avatar"><Computer size={25}/></div><div><div className="eyebrow">AGENT WORKSPACE</div><h2>{agent.hostname||agent.id}</h2><p>{agent.id}</p></div><span className="badge good">Connected</span></div><div className="tabs">{tabs.map(t=><button key={t} className={tab===t?'active':''} onClick={()=>setTab(t)}>{t}</button>)}</div><div className="workspace-body">
    {tab==='Overview'&&<div className="overview-grid"><Info label="Operating system" value={`${agent.os||'Unknown'} / ${agent.arch||'Unknown'}`}/><Info label="Carrier" value={agent.transport||'Unknown'}/><Info label="Remote address" value={agent.remote||'Unknown'}/><Info label="Observed public IP" value={agent.public_ip||'Not observed from this connection'}/><Info label="Relay parent" value={agent.via||'Direct'}/><Info label="Last seen" value={formatTime(agent.last_seen)}/><Info label="Active jobs" value={String(agent.active_jobs||0)}/><div className="wide"><AgentOverviewRoutes agent={agent} client={client} openRoutes={openRoutes} onRefresh={onRefresh}/></div><div className="wide"><h3>Allowed capabilities</h3><div className="chips">{agent.capabilities?.allowed?.map(c=><span key={c}>{c}</span>)||<span>Not reported</span>}</div></div><AgentLifecycle agent={agent} onRefresh={onRefresh}/></div>}
    {tab==='Console'&&<AgentCommandConsole agent={agent} openShell={()=>setTab('Live shell')}/>}
    {tab==='Files'&&<FilesView agentID={agent.id}/>}
    {tab==='Jobs'&&<><StartJob agentID={agent.id} act={act} busy={busy}/><JobsView jobs={jobs} agentID={agent.id} onRefresh={onRefresh}/></>}
    {tab==='Screenshots'&&<ScreenshotsView agent={agent} revision={revision}/>}
    {tab==='Modules'&&<ModulesView agent={agent} onJobs={()=>setTab('Jobs')}/>}
    {tab==='Host'&&<HostResults agentID={agent.id} sessionID={agent.session_id}/>}
    {tab==='Live shell'&&<LiveShellPanel key={agent.id} agentID={agent.id}/>}
  </div></>
}
function Info({label,value}:{label:string;value:string}){return <div className="info"><small>{label}</small><strong>{value}</strong></div>}
function AgentOverviewRoutes({agent,client,openRoutes,onRefresh}:{agent:Agent;client:{operator_only:boolean;session_id:string}|null;openRoutes:()=>void;onRefresh:()=>Promise<void>}){
  type SavedRoute={prefix:string;agent_id:string;manual:boolean;disabled:boolean;installed:boolean};
  const [routes,setRoutes]=useState<SavedRoute[]>([]),[busy,setBusy]=useState(false),[error,setError]=useState('');
  const load=()=>api<SavedRoute[]>('/client/routes').then(setRoutes).catch(e=>setError(String(e)));
  useEffect(()=>{load()},[agent.id]);
  const act=async(prefix:string,enabled:boolean,create=false)=>{setBusy(true);setError('');try{await api('/client/routes',create?'POST':'PUT',create?{prefix,agent_id:agent.id,manual:false}:{prefix,agent_id:agent.id,enabled});await load();await onRefresh()}catch(e){setError(String(e))}finally{setBusy(false)}};
  const own=routes.filter(route=>route.agent_id===agent.id),canInstall=!!client?.session_id&&!client.operator_only;
  return <div className="agent-overview-routes"><div className="agent-overview-heading"><h3>Client routes through this agent</h3><button onClick={openRoutes}>Open Routes to add a custom CIDR</button></div>
    {agent.advertised_routes?.map(prefix=>{const saved=own.find(route=>route.prefix===prefix);return <div className="route-item" key={prefix}><div><strong>{prefix}</strong><small>Advertised by this agent</small></div><span className={'badge '+(saved?.installed?'good':'muted')}>{saved?.disabled?'Off':saved?.installed?'Installed':saved?'Pending':'Available'}</span><button disabled={busy||(!saved?.installed&&!canInstall)} onClick={()=>act(prefix,!!saved?.disabled,!saved)}>{!saved?'Accept':saved.disabled?'Enable':'Disable'}</button></div>})}
    {own.filter(route=>route.manual&&!agent.advertised_routes?.includes(route.prefix)).map(route=><div className="route-item" key={route.prefix}><div><strong>{route.prefix}</strong><small>Saved custom route</small></div><span className={'badge '+(route.installed?'good':'muted')}>{route.disabled?'Off':route.installed?'Installed':'Pending'}</span><button disabled={busy||route.disabled&&!canInstall} onClick={()=>act(route.prefix,route.disabled)}>{route.disabled?'Enable':'Disable'}</button></div>)}
    {!agent.advertised_routes?.length&&!own.length&&<Empty text="No routes advertised or saved for this agent"/>}
    {!canInstall&&<small>Route changes require a VPN client with a TUN device.</small>}{error&&<p className="control-error" role="alert">{error}</p>}
  </div>
}
type ConsoleLine={kind:'command'|'output'|'error';text:string;source?:string};
type ConsoleEntry={id:number;at:string;source:string;kind:'command'|'output'|'error'|'stderr'|'exit';text:string};
const agentConsoleLog=new Map<string,ConsoleLine[]>();
const agentConsoleCommands=new Map<string,string[]>();
function ConsoleText({entry}:{entry:ConsoleLine}){
  if(entry.kind!=='output'||!entry.text.trimStart().startsWith('UNDERTOW  GUI CLIENT / AGENT COMMANDS'))return <>{entry.text}</>;
  return <div className="console-help">{entry.text.split('\n').map((line,index)=>{
    if(line.startsWith('UNDERTOW  '))return <div className="console-help-title" key={index}>{line}</div>;
    if(line.startsWith('──'))return <div className="console-help-rule" key={index}>{line}</div>;
    if(line.startsWith('◆ '))return <div className="console-help-heading" key={index}>{line}</div>;
    const command=/^  (.+?)\s{2,}(.+)$/.exec(line);
    if(command)return <div className="console-help-row" key={index}><span>{command[1]}</span><span>{command[2]}</span></div>;
    return <div className="console-help-note" key={index}>{line||'\u00a0'}</div>;
  })}</div>
}
function AgentCommandConsole({agent,openShell,readOnly=false}:{agent:Agent;openShell:()=>void;readOnly?:boolean}){
  const [lines,setLines]=useState<ConsoleLine[]>(()=>agentConsoleLog.get(agent.id)||[{kind:'output',text:`Attached to ${agent.hostname||agent.id}. Type help for Undertow agent commands.\n`}]);
  const [history,setHistory]=useState<ConsoleEntry[]>([]);
  const [command,setCommand]=useState(''),[busy,setBusy]=useState(false),[historyIndex,setHistoryIndex]=useState(-1);
  const outputRef=useRef<HTMLDivElement>(null);
  const activeCommand=useRef<AbortController|null>(null);
  useEffect(()=>()=>activeCommand.current?.abort(),[agent.id]);
  useEffect(()=>{let mounted=true;const load=()=>api<ConsoleEntry[]>(`/agents/${encodeURIComponent(agent.id)}/console-history`).then(entries=>{if(!mounted)return;setHistory(entries);setLines(entries.length?entries.map(entry=>({kind:entry.kind==='command'?'command':entry.kind==='error'?'error':'output',source:entry.source,text:entry.kind==='exit'?`[exit ${entry.text}]\n`:entry.kind==='stderr'?`[stderr] ${entry.text}`:entry.text})): [{kind:'output',text:`Attached to ${agent.hostname||agent.id}. Type help for Undertow agent commands.\n`}]);agentConsoleCommands.set(agent.id,entries.filter(entry=>entry.kind==='command'&&entry.source==='console').map(entry=>entry.text).reverse().slice(0,100))}).catch(()=>{});load();const listener=(event:Event)=>{if((event as CustomEvent<{agentID:string}>).detail?.agentID===agent.id)load()};window.addEventListener('undertow-console-updated',listener);return()=>{mounted=false;window.removeEventListener('undertow-console-updated',listener)}},[agent.id]);
  useEffect(()=>{agentConsoleLog.set(agent.id,lines);outputRef.current?.scrollTo({top:outputRef.current.scrollHeight})},[agent.id,lines]);
  const append=(kind:ConsoleLine['kind'],text:string)=>setLines(v=>[...v,{kind,text}].slice(-300));
  const submit=async()=>{
    const line=command.trim();if(!line||busy||readOnly)return;
    setCommand('');setHistoryIndex(-1);
    if(line==='clear'||line==='cls'){setLines([]);return}
    const previous=agentConsoleCommands.get(agent.id)||[];
    agentConsoleCommands.set(agent.id,[line,...previous].slice(0,100));
    append('command',line);setBusy(true);
    const controller=new AbortController();activeCommand.current=controller;
    try{
      const response=await fetch(`/api/agents/${encodeURIComponent(agent.id)}/command-stream`,{method:'POST',headers:{'Content-Type':'application/json','X-Undertow-CSRF':csrfToken()},body:JSON.stringify({line}),signal:controller.signal});
      if(!response.ok)throw new Error((await response.text())||response.statusText);
      if(!response.body)throw new Error('Command output stream unavailable');
      const reader=response.body.getReader(),decoder=new TextDecoder();let pending='';
      for(;;){const {done,value}=await reader.read();if(done)break;pending+=decoder.decode(value,{stream:true});let newline;
        while((newline=pending.indexOf('\n'))>=0){const row=pending.slice(0,newline);pending=pending.slice(newline+1);if(!row)continue;const event=JSON.parse(row) as {kind:string;data:string};if(event.kind==='shell')openShell();else append(event.kind==='error'?'error':'output',event.kind==='exit'?`[exit ${event.data}]\n`:event.kind==='stderr'?`[stderr] ${event.data}`:event.data)}
      }
    }catch(e){append((e as Error).name==='AbortError'?'output':'error',(e as Error).name==='AbortError'?'Command stream stopped.\n':String(e))}finally{activeCommand.current=null;setBusy(false);window.dispatchEvent(new CustomEvent('undertow-console-updated',{detail:{agentID:agent.id}}))}
  };
  return <div className="agent-command-console"><div className="command-console-bar"><SquareTerminal size={15}/><strong>UNDERTOW AGENT CONSOLE</strong><span>{agent.hostname||short(agent.id)}</span></div><div className="console-main"><div className="command-console-output" ref={outputRef} role="log" aria-live="polite">{lines.map((entry,i)=><div key={i} className={'command-entry '+entry.kind}>{entry.source==='modules'&&entry.kind==='command'&&<span className="console-source">MODULES TAB </span>}{entry.kind==='command'&&<span className="prompt-marker">undertow[{agent.hostname||short(agent.id)}]&gt; </span>}<ConsoleText entry={entry}/></div>)}{busy&&<div className="command-entry output">Working…</div>}</div><aside className="console-history"><strong>HISTORY</strong>{history.filter(entry=>entry.kind==='command').slice(-100).reverse().map(entry=><div className="console-history-row" key={entry.id} title={`${entry.at} · ${entry.source}`}><small>{entry.source==='modules'?'MODULE':'CONSOLE'} · {entry.at}</small>{entry.source==='console'?<button disabled={readOnly} onClick={()=>setCommand(entry.text)}>{entry.text}</button>:<span>{entry.text}</span>}</div>)}{!history.some(entry=>entry.kind==='command')&&<p>No commands yet.</p>}</aside></div><div className="command-console-input"><span className="prompt-marker">›</span><input disabled={readOnly} aria-label="Undertow agent command" placeholder="Type an Undertow command, or help" value={command} onChange={e=>setCommand(e.target.value)} onKeyDown={e=>{if(e.key==='Enter'){e.preventDefault();submit()}else if(e.key==='ArrowUp'){e.preventDefault();const h=agentConsoleCommands.get(agent.id)||[];const next=Math.min(historyIndex+1,h.length-1);if(next>=0){setHistoryIndex(next);setCommand(h[next])}}else if(e.key==='ArrowDown'){e.preventDefault();const h=agentConsoleCommands.get(agent.id)||[];const next=historyIndex-1;setHistoryIndex(next);setCommand(next<0?'':h[next])}}}/>{busy?<button className="command-stop" onClick={()=>activeCommand.current?.abort()}>Stop</button>:<button disabled={readOnly||!command.trim()} onClick={submit}>Run</button>}</div></div>
}
function LiveShellPanel({agentID}:{agentID:string}){const [started,setStarted]=useState(false);return <div className="live-shell-panel">{started?<><div className="live-shell-actions"><span>Live shell session on {short(agentID)}</span><button onClick={()=>setStarted(false)}>Close session</button></div><TerminalPanel agentID={agentID}/></>:<div className="shell-start"><SquareTerminal size={28}/><h3>Live OS shell</h3><p>Starts a separate interactive agent session only when requested. The Undertow agent command console is in the Console tab.</p><button onClick={()=>setStarted(true)}>Start live shell</button></div>}</div>}
function TerminalPanel({agentID}:{agentID:string}){
  const mount=useRef<HTMLDivElement>(null);
  useEffect(()=>{if(!mount.current)return;const term=new Terminal({cursorBlink:true,convertEol:true,fontFamily:'Consolas, monospace',fontSize:13,theme:{background:'#0a0d0f',foreground:'#d8ddd9',cursor:'#f2ad42'}});term.open(mount.current);term.writeln('Connecting to agent…');const scheme=location.protocol==='https:'?'wss':'ws';const socket=new WebSocket(`${scheme}://${location.host}/api/agents/${encodeURIComponent(agentID)}/terminal?csrf=${encodeURIComponent(csrfToken())}`);socket.onopen=()=>term.clear();socket.onmessage=e=>{try{const msg=JSON.parse(e.data);if(msg.type==='output'||msg.type==='stderr')term.write(msg.data);else if(msg.type==='error')term.writeln(`\r\n${msg.data}`);else if(msg.type==='exit')term.writeln(`\r\nSession ended ${msg.data}`)}catch{}};socket.onclose=()=>term.writeln('\r\nSession disconnected.');const disposable=term.onData(data=>{if(socket.readyState===WebSocket.OPEN)socket.send(JSON.stringify({type:'input',data}))});return()=>{disposable.dispose();socket.close();term.dispose()}},[agentID]);
  return <div className="terminal-shell"><div className="terminal-title"><SquareTerminal size={15}/> Live OS shell <span>{short(agentID)}</span></div><div ref={mount} className="terminal-mount"/></div>
}
function StartJob({agentID,act,busy}:{agentID:string;act:(fn:()=>Promise<unknown>)=>void;busy:boolean}){
  const [program,setProgram]=useState(''),[args,setArgs]=useState<string[]>([]);
  return <section className="job-start"><div><strong>Start background job</strong><span>Runs one program directly on this agent. Arguments are passed exactly as entered; no shell is opened.</span></div><div className="job-start-fields"><label>Program<input aria-label="Job program" value={program} onChange={event=>setProgram(event.target.value)} placeholder="Executable path or name"/></label>{args.map((arg,index)=><label key={index}>Argument {index+1}<span><input aria-label={`Job argument ${index+1}`} value={arg} onChange={event=>setArgs(current=>current.map((value,i)=>i===index?event.target.value:value))}/><button type="button" aria-label={`Remove argument ${index+1}`} onClick={()=>setArgs(current=>current.filter((_,i)=>i!==index))}>×</button></span></label>)}</div><div className="job-start-actions"><button type="button" onClick={()=>setArgs(current=>[...current,''])}>+ Argument</button><button type="button" disabled={busy||!program.trim()} onClick={()=>act(()=>api(`/agents/${encodeURIComponent(agentID)}/jobs`,'POST',{argv:[program.trim(),...args]}))}>Start job</button></div></section>
}
function RelaysView({agents,relays,act,busy,onPayload}:{agents:Agent[];relays:{agent_id:string;bind:string}[];act:(fn:()=>Promise<unknown>)=>void;busy:boolean;onPayload:(target:{agentID:string;bind:string})=>void}){const [agent,setAgent]=useState(''),[bind,setBind]=useState(''),[stop,setStop]=useState<{agent_id:string;bind:string}|null>(null);return <div className="panel"><h2>Agent relay listeners</h2><p>A relay carries child agent sessions. Starting a listener does not build or run a child payload.</p><div className="form-line"><select aria-label="Relay parent agent" value={agent} onChange={e=>setAgent(e.target.value)}><option value="">Select agent</option>{agents.map(a=><option key={a.id} value={a.id}>{a.hostname||short(a.id)}</option>)}</select><input aria-label="Relay bind address" placeholder="127.0.0.1:8443 (default)" value={bind} onChange={e=>setBind(e.target.value)}/><button disabled={busy||!agents.some(a=>a.id===agent)} onClick={()=>act(()=>api(`/agents/${encodeURIComponent(agent)}/relays`,'POST',{bind}))}>Start relay</button></div><p className="control-note">A loopback bind accepts children on the parent host only. Use a specific reachable interface address for a child on another host.</p>{relays.length?relays.map(r=><div className="data-row" key={r.agent_id+r.bind}><Cable size={15}/><strong>{r.bind}</strong><span>on {agents.find(a=>a.id===r.agent_id)?.hostname||short(r.agent_id)}</span><button disabled={busy} onClick={()=>onPayload({agentID:r.agent_id,bind:r.bind})}>Create child payload</button><button className="text-button" disabled={busy} onClick={()=>setStop(r)}>Stop</button></div>):<Empty text="No active relays"/>}{stop&&<div className="control-confirm" role="alertdialog" aria-label="Confirm relay stop"><div><strong>Stop relay {stop.bind}?</strong><p>Child sessions using this listener will disconnect and may reconnect when the relay returns.</p></div><button disabled={busy} onClick={()=>{act(()=>api(`/agents/${encodeURIComponent(stop.agent_id)}/relays?bind=${encodeURIComponent(stop.bind)}`,'DELETE'));setStop(null)}}>Confirm stop</button><button onClick={()=>setStop(null)}>Cancel</button></div>}</div>}
function StatusView({status,client}:{status:Status|null;client:{session_id:string;transport:string;operator_only:boolean}|null}){return <div className="two-column"><section className="panel"><h2>Local client</h2><Info label="Session ID" value={client?.session_id||'Disconnected'}/><Info label="Carrier" value={client?.transport||'Unknown'}/><Info label="Mode" value={client?.operator_only?'Operator only':'VPN client'}/></section><section className="panel"><h2>Undertow server</h2><Info label="Fingerprint" value={status?.server.fingerprint||'Unknown'}/><Info label="Listeners" value={String(status?.server.listeners?.length||0)}/><Info label="Connected agents" value={String(status?.agents.filter(a=>a.online!==false).length||0)}/></section></div>}
function SettingsView({status,client,revision,onRefresh}:{status:Status|null;client:{session_id:string;transport:string;operator_only:boolean;vpn:boolean;internal:boolean}|null;revision?:string;onRefresh:()=>Promise<void>}){
  const [section,setSection]=useState<'Client'|'Carriers'|'Identity'|'Status'|'Logs'>('Client');
  return <><PageHeader title="Settings" description="Client routing, shared server listeners, operator identity, and diagnostics."/><div className="tabs settings-tabs">{(['Client','Carriers','Identity','Status','Logs'] as const).map(name=><button key={name} className={section===name?'active':''} onClick={()=>setSection(name)}>{name}</button>)}</div>
    {section==='Client'&&<RoutingModeView client={client} onChange={onRefresh}/>}
    {section==='Carriers'&&<><ServerPublicHost current={status?.server.public_host||''} onSaved={onRefresh}/><TransportsView revision={revision} currentCarrier={client?.session_id?client.transport:undefined}/></>}
    {section==='Identity'&&<PreferencesView/>}
    {section==='Status'&&<StatusView status={status} client={client}/>}
    {section==='Logs'&&<WorkerLogsView/>}
  </>
}
function ServerPublicHost({current,onSaved}:{current:string;onSaved:()=>Promise<void>}){
  const [host,setHost]=useState(current),[message,setMessage]=useState(''),[busy,setBusy]=useState(false);
  useEffect(()=>setHost(current),[current]);
  return <section className="panel preferences-panel"><h2>Server public address</h2><p>Set the IP or hostname operators should use for new payload profiles. This is shared server metadata; Undertow does not probe an external IP service.</p><div className="form-line"><input aria-label="Server public IP or hostname" placeholder="Public IP or DNS hostname" value={host} onChange={e=>setHost(e.target.value)} maxLength={253}/><button disabled={busy} onClick={async()=>{setBusy(true);setMessage('');try{await api('/server-public-host','PUT',{host:host.trim()});await onSaved();setMessage('Saved on server')}catch(e){setMessage(String(e))}finally{setBusy(false)}}}>Save address</button></div>{message&&<small role="status">{message}</small>}</section>
}
function PreferencesView(){const [operatorID,setOperatorID]=useState(''),[displayName,setDisplayName]=useState(''),[message,setMessage]=useState('');useEffect(()=>{api<{operator_id:string;display_name:string}>('/preferences').then(p=>{setOperatorID(p.operator_id);setDisplayName(p.display_name)}).catch(e=>setMessage(String(e)))},[]);return <section className="panel preferences-panel"><h2>Operator attribution</h2><p>These are unverified claims recorded with your server bound client session. Authentication and permissions will be added later.</p><div className="form-line"><input aria-label="Operator ID" placeholder="Operator ID" maxLength={128} value={operatorID} onChange={e=>setOperatorID(e.target.value)}/><input aria-label="Display name" placeholder="Display name" maxLength={128} value={displayName} onChange={e=>setDisplayName(e.target.value)}/><button onClick={async()=>{try{await api('/preferences','PUT',{operator_id:operatorID,display_name:displayName});setMessage('Saved on this client')}catch(e){setMessage(String(e))}}}>Save identity</button></div>{message&&<small>{message}</small>}</section>}

class GUIErrorBoundary extends Component<{children:ReactNode},{error:string}> {
  state={error:''};
  static getDerivedStateFromError(error:unknown){return {error:error instanceof Error?error.message:String(error)}}
  render(){return this.state.error?<main className="gui-crash"><h1>View could not be displayed</h1><p>{this.state.error}</p><button onClick={()=>location.reload()}>Reload GUI</button></main>:this.props.children}
}
createRoot(document.getElementById('root')!).render(<GUIErrorBoundary><App/></GUIErrorBoundary>);
