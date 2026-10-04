import {useEffect, useMemo, useState} from 'react';
import {Download, ListTodo, RefreshCw, Square, Trash2} from 'lucide-react';
import {api, type Job} from './api';

function short(value:string){return value.length>18?value.slice(0,18)+'…':value}
function when(value?:string){return value?new Date(value).toLocaleString():'—'}
function taskName(job:Job){return job.argv?.length?job.argv.join(' '):job.language?`${job.kind} · ${job.language}`:job.kind||'Agent task'}

export function JobsView({jobs,agentID,onRefresh,onAgent,readOnly=false,offlineAgentIDs=[]}:{jobs:Job[];agentID?:string;onRefresh:()=>Promise<void>;onAgent?:(id:string)=>void;readOnly?:boolean;offlineAgentIDs?:string[]}) {
  const [selectedID,setSelectedID]=useState('');
  const [detail,setDetail]=useState<Job|null>(null);
  const [query,setQuery]=useState('');
  const [state,setState]=useState('all');
  const [confirm,setConfirm]=useState<'cancel'|'delete'|null>(null);
  const [error,setError]=useState('');
  const [busy,setBusy]=useState(false);
  const sorted=useMemo(()=>[...jobs].sort((a,b)=>Date.parse(b.started)-Date.parse(a.started)),[jobs]);
  const filtered=useMemo(()=>sorted.filter(job=>(!agentID||job.agent_id===agentID)&&(state==='all'||job.state===state)&&(!query||`${job.id} ${job.agent_id} ${taskName(job)}`.toLowerCase().includes(query.toLowerCase()))),[sorted,agentID,state,query]);
  useEffect(()=>{if(!selectedID||!jobs.some(job=>job.id===selectedID))setSelectedID(sorted.find(job=>!agentID||job.agent_id===agentID)?.id||'')},[jobs,agentID,selectedID,sorted]);
  const selected=jobs.find(job=>job.id===selectedID);
  useEffect(()=>{
    if(!selectedID){setDetail(null);return}
    let active=true;
    api<Job>(`/jobs/${encodeURIComponent(selectedID)}/output`).then(job=>{if(active){setDetail(job);setError('')}}).catch(reason=>{if(active){setError(String(reason));setDetail(null)}});
    return()=>{active=false};
  },[selectedID,selected?.output_bytes,selected?.state]);
  const change=async(action:'cancel'|'delete')=>{
    if(!selectedID)return;setBusy(true);setError('');
    try{
      await api(`/jobs/${encodeURIComponent(selectedID)}${action==='cancel'?'/cancel':''}`,action==='cancel'?'POST':'DELETE',action==='cancel'?{}:undefined);
      setConfirm(null);if(action==='delete'){setSelectedID('');setDetail(null)}
      await onRefresh();
    }catch(reason){setError(String(reason))}finally{setBusy(false)}
  };
  return <div className="jobs-layout"><section className="panel jobs-list"><div className="jobs-list-top"><h2>{agentID?'Agent jobs':'All jobs'}</h2><button title="Refresh jobs" onClick={()=>onRefresh().catch(reason=>setError(String(reason)))}><RefreshCw size={14}/></button></div><div className="jobs-filters"><input aria-label="Search jobs" value={query} onChange={event=>setQuery(event.target.value)} placeholder="Search job, command, agent"/><select aria-label="Filter job state" value={state} onChange={event=>setState(event.target.value)}><option value="all">All states</option>{['running','completed','failed','cancelled','interrupted'].map(value=><option key={value} value={value}>{value}</option>)}</select></div><div className="jobs-list-count">{filtered.length} retained job{filtered.length===1?'':'s'}</div><div className="jobs-rows">{filtered.length?filtered.map(job=><button key={job.id} className={'jobs-row '+(selectedID===job.id?'active':'')} onClick={()=>{setSelectedID(job.id);setDetail(null);setConfirm(null)}}><span className={'jobs-state '+job.state}/><span><strong>{taskName(job)}</strong><small>{short(job.id)} · {when(job.started)}</small></span><em>{job.state}</em></button>):<div className="jobs-empty">No jobs match this view.</div>}</div></section>
    <section className="panel jobs-detail">{error&&<div className="jobs-alert" role="alert">{error}</div>}{selected&&detail?<><div className="jobs-detail-heading"><div className="jobs-detail-icon"><ListTodo size={22}/></div><div><span className="eyebrow">SERVER RETAINED JOB</span><h2>{taskName(detail)}</h2><p>{detail.id}</p></div><span className={'badge '+(detail.state==='running'?'warn':detail.state==='completed'?'good':'muted')}>{detail.state}</span></div><div className="jobs-facts"><div><small>Agent</small>{onAgent?<button onClick={()=>onAgent(detail.agent_id)}>{short(detail.agent_id)}</button>:<strong>{short(detail.agent_id)}</strong>}</div><div><small>Started</small><strong>{when(detail.started)}</strong></div><div><small>Finished</small><strong>{when(detail.ended)}</strong></div><div><small>Exit</small><strong>{detail.exit_code===undefined?'—':detail.exit_code}</strong></div><div><small>Kind</small><strong>{detail.kind}{detail.language?` · ${detail.language}`:''}</strong></div><div><small>Retained output</small><strong>{detail.output_bytes.toLocaleString()} bytes</strong></div></div><div className="jobs-output-heading"><div><strong>Output</strong><span>{detail.output_truncated?'Latest 256 KiB preview · full output remains on server':'Server retained preview'}</span></div>{detail.output_bytes>0&&<a href={`/api/jobs/${encodeURIComponent(detail.id)}/download`} download><Download size={13}/> Download full output</a>}</div>{detail.output_error&&<div className="jobs-alert">{detail.output_error}</div>}<pre className="jobs-output">{detail.output||'No output retained for this job.'}</pre>{!(readOnly||offlineAgentIDs.includes(detail.agent_id))&&<div className="jobs-actions">{detail.state==='running'&&<button onClick={()=>setConfirm('cancel')}><Square size={12}/>Cancel job</button>}{detail.state!=='running'&&<button onClick={()=>setConfirm('delete')}><Trash2 size={12}/>Delete job and output</button>}</div>}{!(readOnly||offlineAgentIDs.includes(detail.agent_id))&&confirm&&<div className="jobs-confirm">{confirm==='cancel'?'Stop this running job on the agent?':'Delete this job record and its retained server output?'}<div><button disabled={busy} onClick={()=>change(confirm)}>{busy?'Working…':confirm==='cancel'?'Confirm cancel':'Confirm delete'}</button><button disabled={busy} onClick={()=>setConfirm(null)}>Keep job</button></div></div>}</>:<div className="jobs-selection"><ListTodo size={31}/><h2>{jobs.length?'Select a job':'No jobs yet'}</h2><p>Background jobs and their output are retained by the Undertow server.</p></div>}</section></div>;
}
