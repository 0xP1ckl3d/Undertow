import {useEffect, useState} from 'react';
import {ArrowDownToLine, ArrowUpFromLine, RefreshCw} from 'lucide-react';
import {api} from './api';

type Transfer = {id:string;agent_id:string;client_id:string;client_session_id:string;operator_id?:string;display_name?:string;operation:string;remote_path:string;state:string;bytes:number;total:number;sha256?:string;error?:string;started:string;ended?:string};
const size=(value:number)=>value>=1048576?`${(value/1048576).toFixed(1)} MiB`:value>=1024?`${(value/1024).toFixed(1)} KiB`:`${value} B`;

export function TransfersView({revision}:{revision?:string}){
  const [records,setRecords]=useState<Transfer[]>([]),[error,setError]=useState(''),[query,setQuery]=useState('');
  const refresh=()=>api<Transfer[]|null>('/transfers').then(items=>{setRecords(items||[]);setError('')}).catch(reason=>setError(String(reason)));
  useEffect(()=>{refresh()},[revision]);
  const shown=records.filter(item=>`${item.remote_path} ${item.agent_id} ${item.client_id} ${item.display_name||''}`.toLowerCase().includes(query.toLowerCase()));
  return <section className="panel transfers-panel"><div className="transfers-toolbar"><div><h2>Shared transfer history</h2><p>File bytes travel through Undertow’s transfer stream. The server retains state, progress, and checksums.</p></div><input aria-label="Search transfers" placeholder="Search agent, path, or operator" value={query} onChange={event=>setQuery(event.target.value)}/><button title="Refresh transfers" onClick={refresh}><RefreshCw size={15}/></button></div>
    {error&&<p className="control-error" role="alert">{error}</p>}
    {shown.length?<div className="transfers-list">{shown.map(item=><div className="transfer-record" key={item.id}><span className="transfer-icon">{item.operation==='upload'?<ArrowUpFromLine size={17}/>:<ArrowDownToLine size={17}/>}</span><div className="transfer-main"><div><strong>{item.remote_path}</strong><span className={'badge '+(item.state==='completed'?'good':item.state==='running'?'warn':'muted')}>{item.state}</span></div><small>{item.operation} · agent {item.agent_id.slice(0,16)} · {item.display_name||item.operator_id||item.client_id||'Unclaimed client'} · {new Date(item.started).toLocaleString()}</small>{item.state==='running'&&<progress max={item.total||1} value={item.bytes}/>}<small>{size(item.bytes)}{item.total?` / ${size(item.total)}`:''}{item.sha256?` · SHA-256 ${item.sha256}`:''}</small>{item.error&&<span className="control-error">{item.error}</span>}</div></div>)}</div>:<div className="empty">No transfers match this view.</div>}
  </section>;
}
