import {PasswordInput} from './PasswordInput';
import {useEffect, useState} from 'react';
import {api} from './api';
import type {OperatorAccount} from './OperatorSettings';
import './credentials.css';

export type Credential = {id:string;label:string;domain:string;username:string;kind:'password'|'nt_hash';owner_id:string;shared:boolean;created_at:string;updated_at:string};

export function CredentialPicker({value,onChange,allowHash=true}:{value:string;onChange:(value:string,item?:Credential)=>void;allowHash?:boolean}) {
  const [items,setItems]=useState<Credential[]>([]);
  const load=()=>void api<Credential[]>('/credentials').then(setItems).catch(()=>setItems([]));
  useEffect(load,[]);
  const visible=items.filter(item=>allowHash||item.kind==='password');
  return <label>Stored credential<select value={value} onFocus={load} onChange={event=>onChange(event.target.value,visible.find(item=>item.id===event.target.value))}><option value="">Select a credential</option>{visible.map(item=><option key={item.id} value={item.id}>{item.label} · {item.domain?`${item.domain}\\`:''}{item.username} · {item.kind==='password'?'password':'NT hash'}</option>)}</select></label>;
}

export function CredentialsView({operator}:{operator:OperatorAccount|null}) {
  const [items,setItems]=useState<Credential[]>([]),[selected,setSelected]=useState(''),[label,setLabel]=useState(''),[domain,setDomain]=useState(''),[username,setUsername]=useState(''),[kind,setKind]=useState<'password'|'nt_hash'>('password'),[secret,setSecret]=useState(''),[shared,setShared]=useState(false),[confirm,setConfirm]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState('');
  const load=async()=>setItems(await api<Credential[]>('/credentials'));
  useEffect(()=>{void load().catch(e=>setError(String(e)))},[]);
  const reset=()=>{setSelected('');setLabel('');setDomain('');setUsername('');setKind('password');setSecret('');setShared(false);setConfirm(false)};
  const select=(item:Credential)=>{setSelected(item.id);setLabel(item.label);setDomain(item.domain);setUsername(item.username);setKind(item.kind);setSecret('');setShared(item.shared);setConfirm(false)};
  const save=async()=>{
    setBusy(true);setError('');setNotice('');
    const input={label:label.trim(),domain:domain.trim(),username:username.trim(),kind,secret,shared};setSecret('');
    try{await api(selected?`/credentials/${encodeURIComponent(selected)}`:'/credentials',selected?'PUT':'POST',input);await load();reset();setNotice(selected?'Credential replaced.':'Credential saved.')}catch(e){setError(String(e))}finally{input.secret='';setBusy(false)}
  };
  const remove=async()=>{
    if(!selected)return;
    setBusy(true);setError('');
    try{await api(`/credentials/${encodeURIComponent(selected)}`,'DELETE');await load();reset();setNotice('Credential removed.')}catch(e){setError(String(e))}finally{setBusy(false)}
  };
  return <div className="credentials-layout"><section className="panel credential-form"><h2>{selected?'Replace credential':'Store a credential'}</h2><p>Reusable Windows authentication material for this engagement. Secrets stay hidden after saving.</p><div className="credential-fields"><label>Label<input value={label} maxLength={128} onChange={e=>setLabel(e.target.value)} placeholder="Account purpose"/></label><label>Domain<input value={domain} maxLength={256} onChange={e=>setDomain(e.target.value)} placeholder="Domain or target host"/></label><label>Username<input value={username} maxLength={256} onChange={e=>setUsername(e.target.value)} autoComplete="off"/></label><label>Type<select value={kind} onChange={e=>{setKind(e.target.value as 'password'|'nt_hash');setSecret('')}}><option value="password">Username and password</option><option value="nt_hash">Username and NT hash</option></select></label><label>{kind==='password'?'Password':'NT hash'}<PasswordInput visibilityLabel={kind==='password'?'password':'NT hash'}   value={secret} maxLength={kind==='password'?512:32} autoComplete="new-password" onChange={e=>setSecret(e.target.value)} placeholder={kind==='nt_hash'?'32 hexadecimal characters':''}/></label><label className="credential-share"><input type="checkbox" checked={shared} onChange={e=>setShared(e.target.checked)}/>Available to other operators</label></div><div className="credential-actions"><button disabled={busy||!label.trim()||!username.trim()||!secret} onClick={()=>void save()}>{selected?'Replace credential':'Save credential'}</button>{selected&&<><button disabled={busy} onClick={reset}>Cancel edit</button>{confirm?<><button disabled={busy} onClick={()=>void remove()}>Confirm removal</button><button onClick={()=>setConfirm(false)}>Keep</button></>:<button disabled={busy} onClick={()=>setConfirm(true)}>Remove</button>}</>}</div><small>Replacement requires a new secret. NT hashes support only compatible Jump methods; Windows logon token creation requires a password.</small></section><section className="panel credential-list"><div className="credential-heading"><div><h2>Stored credentials · {items.length}</h2><p>Your entries and entries shared with you. Team Leaders can manage all entries.</p></div><button onClick={()=>void load().catch(e=>setError(String(e)))}>Refresh</button></div>{items.length?<div className="credential-table-scroll"><table><thead><tr><th>Label</th><th>Account</th><th>Type</th><th>Owner / access</th><th>Updated</th><th></th></tr></thead><tbody>{items.map(item=><tr key={item.id}><td><strong>{item.label}</strong><code>{item.id}</code></td><td>{item.domain?`${item.domain}\\`:''}{item.username}</td><td>{item.kind==='password'?'Password':'NT hash'}</td><td>{item.owner_id} · {item.shared?'Shared':'Private'}</td><td>{new Date(item.updated_at).toLocaleString()}</td><td>{(operator?.role==='team_leader'||operator?.id===item.owner_id)&&<button onClick={()=>select(item)}>Manage</button>}</td></tr>)}</tbody></table></div>:<p>No credentials are available to this operator.</p>}</section>{error&&<p role="alert" className="credential-error">{error}</p>}{notice&&<p role="status">{notice}</p>}</div>;
}
