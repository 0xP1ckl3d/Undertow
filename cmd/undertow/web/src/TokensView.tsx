import {useEffect, useRef, useState} from 'react';
import {api, type Agent} from './api';
import './tokens.css';

export type TokenContext = {id:string;identity:string;domain:string;user:string;token_type:string;impersonation_level:string;integrity_level:string;session_id:number;elevated:boolean;elevation_type:string;source:string;created_at:string};
type TokenResponse = {contexts:TokenContext[];candidates?:TokenContext[];default_context_id?:string;created?:TokenContext};

export function TokenContextPicker({agentID,value,onChange}:{agentID:string;value:string;onChange:(value:string)=>void}) {
  const [contexts,setContexts]=useState<TokenContext[]>([]),[error,setError]=useState(''),[defaultLabel,setDefaultLabel]=useState('');
  const revision=useRef(0);
  const load=async()=>{
    const current=++revision.current;
    if(!agentID)return;
    try{
      const result=await api<TokenResponse>(`/agents/${encodeURIComponent(agentID)}/tokens`);
      if(current!==revision.current)return;
      setContexts(result.contexts||[]);setError('');
      setDefaultLabel(result.default_context_id?(result.contexts.find(item=>item.id===result.default_context_id)?.identity||'Unavailable context'):'Agent process identity');
    }catch{
      if(current!==revision.current)return;
      setContexts([]);setDefaultLabel('');setError('Live token contexts unavailable');
    }
  };
  useEffect(()=>{setContexts([]);setDefaultLabel('');void load();return()=>{revision.current++}},[agentID]);
  return <label className="token-picker">Authentication context<select aria-label="Authentication context for this operation" value={value} onFocus={()=>void load()} onChange={event=>onChange(event.target.value)}><option value="">Operator session default{defaultLabel?` · ${defaultLabel}`:''}</option><option value="process">Agent process identity</option>{contexts.map(item=><option key={item.id} value={item.id}>{item.identity} · {item.integrity_level} · {item.id}</option>)}{value&&value!=='process'&&!contexts.some(item=>item.id===value)&&<option value={value}>Unavailable context · {value}</option>}</select>{error&&<small>{error}</small>}</label>;
}

export function TokensView({agent}:{agent:Agent}) {
  const [data,setData]=useState<TokenResponse>({contexts:[]}),[candidates,setCandidates]=useState<TokenContext[]>([]),[busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState('');
  const [user,setUser]=useState(''),[domain,setDomain]=useState('.'),[password,setPassword]=useState(''),[logonType,setLogonType]=useState('interactive');
  const supported=!!agent.capabilities?.supported?.includes('tokens'),allowed=!!agent.capabilities?.allowed?.includes('tokens');
  const base=`/agents/${encodeURIComponent(agent.id)}/tokens`;
  const refresh=async()=>{setBusy(true);setError('');try{setData(await api<TokenResponse>(base))}catch(e){setError(String(e))}finally{setBusy(false)}};
  useEffect(()=>{setData({contexts:[]});setCandidates([]);setPassword('');if(allowed)void refresh()},[agent.id,allowed]);
  const action=async(action:string,id?:string)=>{
    setBusy(true);setError('');setNotice('');
    try{
      const logon=action==='create'?{user,domain,password,logon_type:logonType}:undefined;
      if(action==='create')setPassword('');
      const result=await api<TokenResponse>(base,'POST',{action,id,logon});setData(result);
      if(action==='discover'){setCandidates(result.candidates||[]);setNotice('Candidates expire in two minutes or when discovery is repeated.')}
      if(action==='clear')setCandidates([]);
      if(result.created)setNotice(`Created context ${result.created.id} for ${result.created.identity}.`);
      if(action==='revert')await refresh();
    }catch(e){setError(String(e))}finally{setPassword('');setBusy(false)}
  };
  const rows=(items:TokenContext[],discovery=false)=><div className="token-table-scroll"><table><thead><tr><th>Identity / context ID</th><th>Domain / user</th><th>Type / impersonation</th><th>Integrity / elevation</th><th>Session</th><th>Source / created</th><th>Actions</th></tr></thead><tbody>{items.map(item=><tr key={item.id}><td><strong>{item.identity}</strong><code>{item.id}</code>{item.id===data.default_context_id&&<span className="token-default-marker">This session default</span>}</td><td>{item.domain||'—'}<small>{item.user}</small></td><td>{item.token_type}<small>{item.impersonation_level}</small></td><td>{item.integrity_level}<small>{item.elevated?'Elevated':'Not elevated'} · {item.elevation_type}</small></td><td>{item.session_id}</td><td>{item.source}<small>{new Date(item.created_at).toLocaleString()}</small></td><td>{discovery?<button disabled={busy} onClick={()=>void action('import',item.id)}>Import duplicate</button>:<><button disabled={busy} onClick={()=>void action('use',item.id)}>Use for this session</button><button disabled={busy} onClick={()=>void action('remove',item.id)}>Remove</button></>}</td></tr>)}</tbody></table></div>;
  if(!supported||!allowed)return <div className="tokens-workspace"><h3>Authentication Contexts / Tokens</h3><p>{supported?'Token context management is disabled on this agent.':'This agent does not support authentication contexts. A Windows agent with the tokens capability is required.'}</p></div>;
  return <div className="tokens-workspace"><div className="token-heading"><div><h3>Authentication Contexts / Tokens</h3><p>Live Windows identities held by this agent process.</p></div><button disabled={busy} onClick={()=>void refresh()}>Refresh</button></div><div className="token-session"><span>Default for this operator connection</span><strong>{data.default_context_id?(data.contexts.find(item=>item.id===data.default_context_id)?.identity||'Unavailable context'):'Agent process identity'}</strong>{data.default_context_id&&<code>{data.default_context_id}</code>}<button disabled={busy} onClick={()=>void action('revert')}>Revert to process identity</button></div>{error&&<p role="alert" className="token-error">{error}</p>}{notice&&<p role="status">{notice}</p>}<div className="token-heading"><h4>Stored contexts · {data.contexts.length}</h4><div><button disabled={busy} onClick={()=>void action('discover')}>Discover candidates</button><button disabled={busy||!data.contexts.length} onClick={()=>void action('clear')}>Clear store</button></div></div>{data.contexts.length?rows(data.contexts):<p>No stored contexts. Discover and import a candidate, or create a context through Windows logon.</p>}{candidates.length>0&&<section><h4>Token candidates · {candidates.length}</h4>{rows(candidates,true)}</section>}<section className="token-create"><h4>Create a context</h4><form autoComplete="off" onSubmit={event=>{event.preventDefault();void action('create')}}><label>User<input required maxLength={256} value={user} onChange={event=>setUser(event.target.value)}/></label><label>Domain<input maxLength={256} value={domain} onChange={event=>setDomain(event.target.value)}/></label><label>Password<input type="password" autoComplete="new-password" maxLength={512} value={password} onChange={event=>setPassword(event.target.value)}/></label><label>Logon type<select value={logonType} onChange={event=>setLogonType(event.target.value)}><option value="interactive">Interactive</option><option value="network">Network</option><option value="batch">Batch</option><option value="new_credentials">New credentials (outbound network)</option></select></label><button disabled={busy||!user} type="submit">Create context</button></form><p>{logonType==='new_credentials'?'New credentials keeps the local identity and uses supplied credentials for outbound authentication. Creation does not verify the password.':'Windows account logon rights and agent permissions apply.'} Clear Domain when using a UPN. Creation requires a live agent.</p></section><p>Use an operation's Authentication context control or <code>--token-context ID</code> to override the session default. Removing contexts prevents new operations; accepted work retains its identity until it finishes.</p></div>;
}
