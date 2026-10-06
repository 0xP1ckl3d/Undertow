import {useEffect, useState} from 'react';
import {api} from './api';

type HostResult = {agent_id:string;operation:string;session_id:string;at:string;truncated?:boolean;result:{stdout:string;stderr:string;exit_code:number;error?:string}};
const operations: [string,string,string][] = [
  ['whoami','Identity','Current agent process identity'],
  ['privileges','Privileges','Privileges and integrity reported by the host'],
  ['ps','Processes','Process list at the time of capture'],
  ['interfaces','Interfaces','Network interface details'],
  ['dns','DNS','Resolver configuration'],
  ['route-table','Route table','Host route table']
];

export function HostResults({agentID,sessionID,offline=false,revision}:{agentID:string;sessionID?:string;offline?:boolean;revision?:string}) {
  const [selected,setSelected]=useState('whoami');
  const [results,setResults]=useState<HostResult[]>([]);
  const [busy,setBusy]=useState(false);
  const [error,setError]=useState('');
  const load=()=>api<HostResult[]>(`/agents/${encodeURIComponent(agentID)}/host-results`).then(setResults);
  useEffect(()=>{setResults([]);setSelected('whoami');setError('');load().catch(e=>setError(String(e)))},[agentID]);
  useEffect(()=>{if(revision)void load().catch(e=>setError(String(e)))},[revision]);
  const current=results.find(result=>result.operation===selected);
  const operation=operations.find(item=>item[0]===selected)!;
  const run=async()=>{
    setBusy(true);setError('');
    try{
      await api(`/agents/${encodeURIComponent(agentID)}/exec`,'POST',{builtin:selected,args:[]});
      await load();
    }catch(e){setError(String(e))}
    finally{setBusy(false)}
  };
  return <div className="host-layout">
    <div className="host-operation-list" aria-label="Host operations">{operations.map(([id,label,description])=>{
      const result=results.find(item=>item.operation===id);
      return <button key={id} className={'host-operation '+(id===selected?'active':'')} onClick={()=>setSelected(id)}><strong>{label}</strong><small>{result?new Date(result.at).toLocaleString():'Never run'}</small><span>{description}</span></button>;
    })}</div>
    <section className="host-result-panel"><div className="host-result-header"><div><span className="eyebrow">RETAINED HOST RESULT</span><h3>{operation[1]}</h3><p>{operation[2]}</p></div>{!offline&&<button disabled={busy} onClick={run}>{busy?'Running…':current?'Run again':'Run'}</button>}</div>
      {error&&<div className="host-error" role="alert">{error}</div>}{busy&&<div className="host-queued" role="status">Waiting for the agent result. Check-in agents begin at their next callback.</div>}
      {current?<><div className="host-result-meta"><span>Captured {new Date(current.at).toLocaleString()}</span><span>Agent session {current.session_id}</span>{sessionID&&current.session_id!==sessionID&&<span className="stale">Previous session</span>}{current.truncated&&<span className="stale">Output truncated</span>}<span>Exit {current.result.exit_code}</span></div><pre className="host-output">{current.result.error||current.result.stdout||current.result.stderr||'(No output)'}</pre>{current.result.stdout&&current.result.stderr&&<><h4>Standard error</h4><pre className="host-output stderr">{current.result.stderr}</pre></>}</>:<div className="host-never-run">No result recorded. Run this operation when you need a snapshot; opening this tab does not query the agent.</div>}
    </section>
  </div>;
}
