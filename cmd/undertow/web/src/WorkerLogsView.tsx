import {useEffect, useRef, useState} from 'react';
import {Pause, Play, RefreshCw} from 'lucide-react';
import {api} from './api';

type Entry={seq:number;at:string;text:string};
type Snapshot={entries:Entry[];next:number;gap:boolean};

export function WorkerLogsView(){
  const [entries,setEntries]=useState<Entry[]>([]),[follow,setFollow]=useState(false),[error,setError]=useState(''),[gap,setGap]=useState(false);
  const cursor=useRef(0),bottom=useRef<HTMLDivElement>(null),loading=useRef(false);
  const load=async(reset=false)=>{if(loading.current)return;loading.current=true;try{const result=await api<Snapshot>(`/worker-logs${reset||!cursor.current?'':`?after=${cursor.current}`}`);if(reset){setEntries(result.entries);setGap(false)}else{setEntries(current=>[...current,...result.entries].slice(-500));if(result.gap)setGap(true)}cursor.current=result.next;setError('')}catch(e){setError(String(e))}finally{loading.current=false}};
  useEffect(()=>{load(true)},[]);
  useEffect(()=>{if(!follow)return;const source=new EventSource('/api/events');source.addEventListener('change',event=>{try{if(JSON.parse((event as MessageEvent).data).kind==='worker_log')load()}catch{}});source.onopen=()=>load();source.onerror=()=>setError('Log stream disconnected. Reconnecting…');return()=>source.close()},[follow]);
  useEffect(()=>{if(follow)bottom.current?.scrollIntoView({block:'end'})},[entries,follow]);
  return <section className="panel worker-logs"><div className="worker-logs-head"><div><h2>Server worker logs</h2><p>Recent process diagnostics. The view retains 500 lines locally; the server keeps a bounded live buffer.</p></div><div><button onClick={()=>load(true)} title="Reload recent logs"><RefreshCw size={14}/> Recent</button><button className={follow?'active':''} onClick={()=>setFollow(!follow)}>{follow?<><Pause size={14}/> Pause</>:<><Play size={14}/> Follow</>}</button></div></div>{gap&&<p className="control-note">Older log entries have expired from the server buffer. Showing available entries.</p>}{error&&<p className="control-error" role="alert">{error}</p>}<div className="worker-log-lines" role="log" aria-live={follow?'polite':'off'}>{entries.length?entries.map(item=><div key={item.seq}><time>{new Date(item.at).toLocaleTimeString()}</time><span>{item.text}</span></div>):<p>No worker log entries yet.</p>}<div ref={bottom}/></div></section>
}
