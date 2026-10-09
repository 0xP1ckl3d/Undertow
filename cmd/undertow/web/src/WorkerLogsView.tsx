import {useEffect, useRef, useState} from 'react';
import {CircleAlert, Pause, Play, RefreshCw, ScrollText} from 'lucide-react';
import {api} from './api';

type Entry={seq:number;at:string;text:string};
type Snapshot={entries:Entry[];next:number;gap:boolean};

export function WorkerLogsView(){
  const [entries,setEntries]=useState<Entry[]>([]),[follow,setFollow]=useState(false),[error,setError]=useState(''),[gap,setGap]=useState(false),[busy,setBusy]=useState(false),[loaded,setLoaded]=useState(false),[updatedAt,setUpdatedAt]=useState<Date|null>(null);
  const cursor=useRef(0),pane=useRef<HTMLDivElement>(null),loading=useRef(false);
  const load=async(reset=false)=>{
    if(loading.current)return;
    loading.current=true;setBusy(true);
    try{
      const result=await api<Snapshot>('/worker-logs'+(reset||!cursor.current?'':'?after='+cursor.current));
      if(reset)setEntries(result.entries.slice(-500));
      else setEntries(current=>[...current,...result.entries].slice(-500));
      setGap(current=>reset?result.gap:current||result.gap);
      cursor.current=result.next;setError('');setLoaded(true);setUpdatedAt(new Date());
    }catch(e){setError(e instanceof Error?e.message:String(e))}
    finally{loading.current=false;setBusy(false)}
  };
  useEffect(()=>{void load(true)},[]);
  useEffect(()=>{
    if(!follow)return;
    const source=new EventSource('/api/events');
    source.addEventListener('change',event=>{try{if(JSON.parse((event as MessageEvent).data).kind==='worker_log')void load()}catch{}});
    source.onopen=()=>void load();
    source.onerror=()=>setError('Log stream disconnected. Reconnecting…');
    return()=>source.close();
  },[follow]);
  useEffect(()=>{if(follow&&pane.current)pane.current.scrollTop=pane.current.scrollHeight},[entries,follow]);
  return <section className="panel worker-logs">
    <div className="worker-logs-head"><div><h2><ScrollText size={18}/>Server worker logs</h2><p>Process diagnostics from the server’s live buffer. This view retains the latest 500 entries.</p></div>
      <div><button type="button" disabled={busy} onClick={()=>void load(true)} title="Reload the latest entries from the server"><RefreshCw size={14} className={busy?'is-loading':''}/>{busy?'Loading…':'Reload recent'}</button><button type="button" className={follow?'active':''} aria-pressed={follow} onClick={()=>setFollow(!follow)}>{follow?<><Pause size={14}/>Pause</>:<><Play size={14}/>Follow</>}</button></div>
    </div>
    <div className="worker-logs-status"><span className={follow?'log-follow-state following':'log-follow-state'}><i/>{follow?'Following new entries':'Snapshot · follow off'}</span><span>{entries.length} / 500 entries</span><span>{updatedAt?'Updated '+updatedAt.toLocaleTimeString():busy?'Loading logs…':'Not loaded'}</span></div>
    {gap&&<p className="log-notice"><CircleAlert size={15}/>Some entries expired from the server buffer. The available entries are shown below.</p>}
    {error&&<p className="settings-feedback error" role="alert">{error}</p>}
    <div className="worker-log-columns" aria-hidden="true"><span>Time · local</span><span>Message</span></div>
    <div ref={pane} className="worker-log-lines" role="log" aria-label="Server worker log entries" aria-live={follow?'polite':'off'} aria-relevant="additions" tabIndex={0}>
      {entries.length?entries.map(item=><div className="worker-log-entry" key={item.seq}><time dateTime={item.at} title={new Date(item.at).toLocaleString()}><span>{new Date(item.at).toLocaleTimeString()}</span><small>{new Date(item.at).toLocaleDateString(undefined,{month:'short',day:'numeric'})}</small></time><span>{item.text}</span></div>):
        <div className="log-empty"><ScrollText size={26}/><strong>{busy?'Loading server logs…':error?'Logs unavailable':loaded?'No worker log entries yet':'No logs loaded'}</strong><p>{error?'Use Reload recent to try again.':loaded?'Diagnostics will appear here when the server records them. Enable Follow for live updates.':'Waiting for the server’s diagnostics buffer.'}</p></div>}
    </div>
  </section>;
}
