import {Component, useEffect, useRef, useState, type ReactNode} from 'react';
import {createRoot} from 'react-dom/client';
import {Terminal} from '@xterm/xterm';
import {Activity, Boxes, Cable, Clock3, Computer, Download, ListTodo, Network, Radio, RefreshCw, Route as RouteIcon, SquareTerminal, X} from 'lucide-react';
import {api, connect, csrfToken, type Agent, type Job, type Status, type Topology} from './api';
import {TopologyGraph} from './TopologyGraph';
import {HostResults} from './HostResults';
import {RoutesView} from './RoutesView';
import {PayloadView} from './PayloadView';
import {ModulesView} from './ModulesView';
import {JobsView} from './JobsView';
import {ScreenshotsView} from './ScreenshotsView';
import {FilesView} from './FilesView';
import '@xyflow/react/dist/style.css';
import '@xterm/xterm/css/xterm.css';
import './style.css';
import './workflows.css';
import './screenshots.css';
import './files.css';

type View = 'topology'|'agents'|'jobs'|'routes'|'relays'|'modules'|'payloads'|'history'|'status';
type Audit = {id:string;at:string;action:string;target:string;client_id?:string;client_session_id?:number;operator_id?:string;display_name?:string;source:string;identity_trust:string;status:number};
const nav: {id:View; label:string; icon:typeof Network}[] = [
  {id:'topology',label:'Topology',icon:Network},{id:'agents',label:'Agents',icon:Computer},{id:'jobs',label:'Jobs',icon:ListTodo},
  {id:'routes',label:'Routes',icon:RouteIcon},{id:'relays',label:'Relays',icon:Cable},{id:'modules',label:'Modules',icon:Boxes},{id:'payloads',label:'Payloads',icon:Boxes},{id:'history',label:'History',icon:Clock3},{id:'status',label:'Status',icon:Activity}
];
function formatTime(value?:string){return value?new Date(value).toLocaleString():'—'}
function short(value:string){return value.length>18?value.slice(0,18)+'…':value}

function App(){
  const [ready,setReady]=useState(false), [authError,setAuthError]=useState(''), [error,setError]=useState('');
  const [view,setView]=useState<View>('topology'), [status,setStatus]=useState<Status|null>(null), [topology,setTopology]=useState<Topology|null>(null);
  const [selected,setSelected]=useState<string|null>(null), [tab,setTab]=useState('Console'), [jobs,setJobs]=useState<Job[]>([]), [relays,setRelays]=useState<{agent_id:string;bind:string}[]>([]), [history,setHistory]=useState<Audit[]>([]);
  const [client,setClient]=useState<{session_id:number;transport:string;operator_only:boolean;vpn:boolean;internal:boolean}|null>(null), [busy,setBusy]=useState(false);
  const refresh=async()=>{try{const [s,t,j,r,c,h]=await Promise.all([api<Status>('/status'),api<Topology>('/topology'),api<Job[]>('/jobs'),api<{agent_id:string;bind:string}[]>('/relays'),api<{session_id:number;transport:string;operator_only:boolean;vpn:boolean;internal:boolean}>('/client'),api<Audit[]>('/history').catch(()=>[])]);setStatus(s);setTopology(t);setJobs(j);setRelays(r);setClient(c);setHistory(h);setError('')}catch(e){setError(String(e))}};
  useEffect(()=>{connect().then(()=>{setReady(true);refresh()}).catch(e=>setAuthError(String(e)))},[]);
  useEffect(()=>{if(!ready)return;const source=new EventSource('/api/events');let timer:number|undefined;source.addEventListener('change',()=>{if(timer)window.clearTimeout(timer);timer=window.setTimeout(()=>refresh(),150)});return()=>{source.close();if(timer)window.clearTimeout(timer)}},[ready]);
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
        {view==='topology'&&<><PageHeader title="Network topology" description="Observed sessions, relay paths, routes, and operator clients." count={topology?.nodes.length} /><div className="topology-layout"><div className="topology-frame"><TopologyGraph topology={topology} localClient={client} onAgent={id=>{setSelected(id);setTab('Console');setView('agents')}}/></div><div className="side-stack"><section className="panel"><h2>Connected peers</h2><Metric label="Agents" value={status?.agents.length||0}/><Metric label="Operator clients" value={status?.clients.length||0}/><Metric label="Active relays" value={relays.length}/></section><section className="panel"><h2>Carrier listeners</h2>{status?.server.listeners?.length?status.server.listeners.map(l=><div className="listener-row" key={l.transport}><span className="state-indicator"/><div><strong>{l.transport.toUpperCase()}</strong><small>{l.listen}</small></div><b>{l.sessions}</b></div>):<Empty text="No listener data"/>}</section><section className="panel compact"><h2>Route legend</h2><Legend color="var(--amber)" label="Carrier / relay path"/><Legend color="var(--green)" label="Client accepted path"/><Legend color="#cd85dd" label="Active forward"/></section></div></div></>}
        {view==='agents'&&<><PageHeader title="Agents" description="Select an agent to open its workspace." count={status?.agents.length}/><div className="agent-layout"><div className="agent-list panel"><div className="panel-title">Connected agents</div>{status?.agents.length?status.agents.map(a=><button key={a.id} className={'agent-row '+(selected===a.id?'active':'')} onClick={()=>{setSelected(a.id);setTab('Console')}}><span className="agent-icon"><Computer size={17}/></span><span className="agent-name"><strong>{a.hostname||short(a.id)}</strong><small>{a.os||'Unknown OS'} · {a.transport||'Unknown carrier'}</small></span><span className="state-indicator"/></button>):<Empty text="No agents connected"/>}</div><div className="workspace panel">{activeAgent?<AgentWorkspace agent={activeAgent} jobs={jobs.filter(j=>j.agent_id===activeAgent.id)} tab={tab} setTab={setTab} act={act} busy={busy} onRefresh={refresh} revision={topology?.at}/>:<div className="workspace-empty"><Computer size={35}/><h2>Select an agent</h2><p>Agent details and operations will appear here.</p></div>}</div></div></>}
        {view==='jobs'&&<><PageHeader title="Jobs" description="Background execution and retained server output." count={jobs.length}/><JobsView jobs={jobs} onRefresh={refresh} onAgent={id=>{setSelected(id);setTab('Jobs');setView('agents')}}/></>}
        {view==='routes'&&<><PageHeader title="Routes" description="Server routes and each client’s accepted paths." count={status?.routes.length}/><RoutesView status={status} client={client}/></>}
        {view==='relays'&&<><PageHeader title="Relays" description="Listeners hosted by agents and child paths." count={relays.length}/><RelaysView agents={status?.agents||[]} relays={relays} act={act} busy={busy}/></>}
        {view==='modules'&&<><PageHeader title="Module bank" description="Compiled modules loaded on this client. Select an agent before running one."/><ModulesView agents={status?.agents||[]} onJobs={()=>setView('jobs')}/></>}
        {view==='payloads'&&<><PageHeader title="Payloads" description="Existing profiles and built artifacts on the server."/><PayloadView revision={topology?.at}/></>}
        {view==='history'&&<><PageHeader title="Operational history" description="Server recorded operator actions and their results." count={history.length}/><div className="panel table-panel"><HistoryTable records={history}/></div></>}
        {view==='status'&&<><PageHeader title="System status" description="Client, server, and carrier information."/><StatusView status={status} client={client}/><PreferencesView/></>}
      </main>
    </div>
  </div>
}
function PageHeader({title,description,count}:{title:string;description:string;count?:number}){return <div className="page-header"><div><h1>{title}</h1><p>{description}</p></div>{count!==undefined&&<div className="count-card"><strong>{count}</strong><span>TOTAL</span></div>}</div>}
function Metric({label,value}:{label:string;value:number}){return <div className="metric"><span>{label}</span><strong>{value}</strong></div>}
function Legend({color,label}:{color:string;label:string}){return <div className="legend"><span style={{background:color}}/>{label}</div>}
function Empty({text}:{text:string}){return <div className="empty">{text}</div>}
function HistoryTable({records}:{records:Audit[]}){return records.length?<table><thead><tr><th>Time</th><th>Action</th><th>Target</th><th>Operator claim</th><th>Client</th><th>Result</th></tr></thead><tbody>{records.map(r=><tr key={r.id}><td>{formatTime(r.at)}</td><td className="mono">{r.action}</td><td className="mono">{r.target}</td><td>{r.display_name||r.operator_id||'Unclaimed'}<small className="history-trust">{r.identity_trust.replaceAll('_',' ')}</small></td><td className="mono">{r.client_id?short(r.client_id):r.source}</td><td><span className={'badge '+(r.status===0?'warn':r.status<400?'good':'muted')}>{r.status||'Pending'}</span></td></tr>)}</tbody></table>:<Empty text="No operator actions recorded"/>}

function AgentWorkspace({agent,jobs,tab,setTab,act,busy,onRefresh,revision}:{agent:Agent;jobs:Job[];tab:string;setTab:(s:string)=>void;act:(fn:()=>Promise<unknown>)=>void;busy:boolean;onRefresh:()=>Promise<void>;revision?:string}){
  const tabs=['Console','Overview','Files','Jobs','Screenshots','Modules','Host','Live shell'];
  return <><div className="workspace-header"><div className="workspace-avatar"><Computer size={25}/></div><div><div className="eyebrow">AGENT WORKSPACE</div><h2>{agent.hostname||agent.id}</h2><p>{agent.id}</p></div><span className="badge good">Connected</span></div><div className="tabs">{tabs.map(t=><button key={t} className={tab===t?'active':''} onClick={()=>setTab(t)}>{t}</button>)}</div><div className="workspace-body">
    {tab==='Overview'&&<div className="overview-grid"><Info label="Operating system" value={`${agent.os||'Unknown'} / ${agent.arch||'Unknown'}`}/><Info label="Carrier" value={agent.transport||'Unknown'}/><Info label="Remote address" value={agent.remote||'Unknown'}/><Info label="Relay parent" value={agent.via||'Direct'}/><Info label="Last seen" value={formatTime(agent.last_seen)}/><Info label="Active jobs" value={String(agent.active_jobs||0)}/><div className="wide"><h3>Advertised routes</h3>{agent.advertised_routes?.length?<div className="chips">{agent.advertised_routes.map(p=><span key={p}>{p}</span>)}</div>:<Empty text="No routes advertised"/>}</div><div className="wide"><h3>Allowed capabilities</h3><div className="chips">{agent.capabilities?.allowed?.map(c=><span key={c}>{c}</span>)||<span>Not reported</span>}</div></div></div>}
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
type ConsoleLine={kind:'command'|'output'|'error';text:string};
const agentConsoleLog=new Map<string,ConsoleLine[]>();
const agentConsoleCommands=new Map<string,string[]>();
function AgentCommandConsole({agent,openShell}:{agent:Agent;openShell:()=>void}){
  const [lines,setLines]=useState<ConsoleLine[]>(()=>agentConsoleLog.get(agent.id)||[{kind:'output',text:`Attached to ${agent.hostname||agent.id}. Type help for Undertow agent commands.\n`}]);
  const [command,setCommand]=useState(''),[busy,setBusy]=useState(false),[historyIndex,setHistoryIndex]=useState(-1);
  const outputRef=useRef<HTMLDivElement>(null);
  const activeCommand=useRef<AbortController|null>(null);
  useEffect(()=>()=>activeCommand.current?.abort(),[agent.id]);
  useEffect(()=>{agentConsoleLog.set(agent.id,lines);outputRef.current?.scrollTo({top:outputRef.current.scrollHeight})},[agent.id,lines]);
  const append=(kind:ConsoleLine['kind'],text:string)=>setLines(v=>[...v,{kind,text}].slice(-300));
  const submit=async()=>{
    const line=command.trim();if(!line||busy)return;
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
    }catch(e){append((e as Error).name==='AbortError'?'output':'error',(e as Error).name==='AbortError'?'Command stream stopped.\n':String(e))}finally{activeCommand.current=null;setBusy(false)}
  };
  return <div className="agent-command-console"><div className="command-console-bar"><SquareTerminal size={15}/><strong>UNDERTOW AGENT CONSOLE</strong><span>{agent.hostname||short(agent.id)}</span></div><div className="command-console-output" ref={outputRef} role="log" aria-live="polite">{lines.map((entry,i)=><div key={i} className={'command-entry '+entry.kind}>{entry.kind==='command'&&<span className="prompt-marker">undertow[{agent.hostname||short(agent.id)}]&gt; </span>}{entry.text}</div>)}{busy&&<div className="command-entry output">Working…</div>}</div><div className="command-console-input"><span className="prompt-marker">›</span><input aria-label="Undertow agent command" placeholder="Type an Undertow command, or help" value={command} onChange={e=>setCommand(e.target.value)} onKeyDown={e=>{if(e.key==='Enter'){e.preventDefault();submit()}else if(e.key==='ArrowUp'){e.preventDefault();const h=agentConsoleCommands.get(agent.id)||[];const next=Math.min(historyIndex+1,h.length-1);if(next>=0){setHistoryIndex(next);setCommand(h[next])}}else if(e.key==='ArrowDown'){e.preventDefault();const h=agentConsoleCommands.get(agent.id)||[];const next=historyIndex-1;setHistoryIndex(next);setCommand(next<0?'':h[next])}}}/>{busy?<button className="command-stop" onClick={()=>activeCommand.current?.abort()}>Stop</button>:<button disabled={!command.trim()} onClick={submit}>Run</button>}</div></div>
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
function RelaysView({agents,relays,act,busy}:{agents:Agent[];relays:{agent_id:string;bind:string}[];act:(fn:()=>Promise<unknown>)=>void;busy:boolean}){const [agent,setAgent]=useState(''),[bind,setBind]=useState('');return <div className="panel"><h2>Agent relay listeners</h2><div className="form-line"><select value={agent} onChange={e=>setAgent(e.target.value)}><option value="">Select agent</option>{agents.map(a=><option key={a.id} value={a.id}>{a.hostname||short(a.id)}</option>)}</select><input placeholder="Bind address (optional)" value={bind} onChange={e=>setBind(e.target.value)}/><button disabled={busy||!agent} onClick={()=>act(()=>api(`/agents/${encodeURIComponent(agent)}/relays`,'POST',{bind}))}>Start relay</button></div>{relays.length?relays.map(r=><div className="data-row" key={r.agent_id+r.bind}><Cable size={15}/><strong>{r.bind}</strong><span>on {short(r.agent_id)}</span><button className="text-button" disabled={busy} onClick={()=>act(()=>api(`/agents/${encodeURIComponent(r.agent_id)}/relays?bind=${encodeURIComponent(r.bind)}`,'DELETE'))}>Stop</button></div>):<Empty text="No active relays"/>}</div>}
function StatusView({status,client}:{status:Status|null;client:{session_id:number;transport:string;operator_only:boolean}|null}){return <div className="two-column"><section className="panel"><h2>Local client</h2><Info label="Session ID" value={String(client?.session_id||'Disconnected')}/><Info label="Carrier" value={client?.transport||'Unknown'}/><Info label="Mode" value={client?.operator_only?'Operator only':'VPN client'}/></section><section className="panel"><h2>Undertow server</h2><Info label="Fingerprint" value={status?.server.fingerprint||'Unknown'}/><Info label="Listeners" value={String(status?.server.listeners?.length||0)}/><Info label="Connected agents" value={String(status?.agents.length||0)}/></section></div>}
function PreferencesView(){const [operatorID,setOperatorID]=useState(''),[displayName,setDisplayName]=useState(''),[message,setMessage]=useState('');useEffect(()=>{api<{operator_id:string;display_name:string}>('/preferences').then(p=>{setOperatorID(p.operator_id);setDisplayName(p.display_name)}).catch(e=>setMessage(String(e)))},[]);return <section className="panel preferences-panel"><h2>Operator attribution</h2><p>These are unverified claims recorded with your server bound client session. Authentication and permissions will be added later.</p><div className="form-line"><input aria-label="Operator ID" placeholder="Operator ID" maxLength={128} value={operatorID} onChange={e=>setOperatorID(e.target.value)}/><input aria-label="Display name" placeholder="Display name" maxLength={128} value={displayName} onChange={e=>setDisplayName(e.target.value)}/><button onClick={async()=>{try{await api('/preferences','PUT',{operator_id:operatorID,display_name:displayName});setMessage('Saved on this client')}catch(e){setMessage(String(e))}}}>Save identity</button></div>{message&&<small>{message}</small>}</section>}

class GUIErrorBoundary extends Component<{children:ReactNode},{error:string}> {
  state={error:''};
  static getDerivedStateFromError(error:unknown){return {error:error instanceof Error?error.message:String(error)}}
  render(){return this.state.error?<main className="gui-crash"><h1>View could not be displayed</h1><p>{this.state.error}</p><button onClick={()=>location.reload()}>Reload GUI</button></main>:this.props.children}
}
createRoot(document.getElementById('root')!).render(<GUIErrorBoundary><App/></GUIErrorBoundary>);
