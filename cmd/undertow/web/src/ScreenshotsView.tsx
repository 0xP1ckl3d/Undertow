import {useEffect, useState} from 'react';
import {Camera, Download, Monitor, RefreshCw} from 'lucide-react';
import {api, type Agent} from './api';

type Screen = {number:number;name:string;width:number;height:number;foreground?:string};
type Screenshot = {id:string;agent_id:string;screen:number;at:string;size:number;sha256:string;width:number;height:number;client_id?:string;operator_id?:string;display_name?:string};
function date(value:string){return new Date(value).toLocaleString()}

export function ScreenshotsView({agent,revision}:{agent:Agent;revision?:string}){
  const [history,setHistory]=useState<Screenshot[]>([]);
  const [screens,setScreens]=useState<Screen[]|null>(null);
  const [selectedID,setSelectedID]=useState('');
  const [loading,setLoading]=useState(false);
  const [capturing,setCapturing]=useState<number|null>(null);
  const [error,setError]=useState('');
  const [imageError,setImageError]=useState(false);
  const canCapture=!agent.capabilities?.allowed||agent.capabilities.allowed.includes('hostops')&&agent.capabilities.allowed.includes('download');
  const refreshHistory=async()=>{const items=await api<Screenshot[]>(`/screenshots?agent_id=${encodeURIComponent(agent.id)}`);setHistory(items||[]);setSelectedID(current=>current&&items.some(item=>item.id===current)?current:items[0]?.id||'')};
  useEffect(()=>{let active=true;api<Screenshot[]>(`/screenshots?agent_id=${encodeURIComponent(agent.id)}`).then(items=>{if(active){setHistory(items||[]);setSelectedID(current=>current&&items.some(item=>item.id===current)?current:items[0]?.id||'');setError('')}}).catch(reason=>{if(active)setError(String(reason))});return()=>{active=false}},[agent.id,revision]);
  useEffect(()=>{setScreens(null);setSelectedID('');setImageError(false)},[agent.id]);
  const listScreens=async()=>{setLoading(true);setError('');try{setScreens(await api<Screen[]>(`/agents/${encodeURIComponent(agent.id)}/screens`))}catch(reason){setError(String(reason))}finally{setLoading(false)}};
  const capture=async(number:number)=>{setCapturing(number);setError('');try{const item=await api<Screenshot>(`/agents/${encodeURIComponent(agent.id)}/screenshots`,'POST',{screen:number});await refreshHistory();setSelectedID(item.id);setImageError(false)}catch(reason){setError(String(reason))}finally{setCapturing(null)}};
  const selected=history.find(item=>item.id===selectedID);
  return <div className="screenshots-view">
    <div className="screenshots-head"><div><h3>Screenshots</h3><p>Captures are stored on the Undertow server and visible to other operator clients. Viewing history does not contact the agent.</p></div><button disabled={loading||!canCapture} onClick={listScreens}><Monitor size={14}/>{loading?'Checking screens…':screens?'Refresh screens':'List screens'}</button></div>
    {!canCapture&&<div className="screenshots-notice">This agent does not allow the host operations and download capabilities required for screenshot capture.</div>}
    {error&&<div className="screenshots-error" role="alert">{error}</div>}
    {screens!==null&&<section className="screenshots-screens"><div className="screenshots-section-title"><strong>Available screens</strong><span>Capture only when you click a screen button.</span></div>{screens.length?screens.map(screen=><div className="screen-row" key={screen.number}><div className="screen-glyph"><Monitor size={19}/><small>{screen.number}</small></div><div><strong>{screen.name||`Screen ${screen.number}`}</strong><span>{screen.width} × {screen.height}{screen.foreground?` · ${screen.foreground}`:''}</span></div><button disabled={capturing!==null} onClick={()=>capture(screen.number)}><Camera size={14}/>{capturing===screen.number?'Capturing…':'Capture screen'}</button></div>):<div className="screenshots-empty">No desktop screens are available in this agent session.</div>}</section>}
    <div className="screenshots-history-head"><div><strong>Server history</strong><span>{history.length} retained capture{history.length===1?'':'s'}</span></div><button title="Refresh screenshot history" onClick={()=>refreshHistory().catch(reason=>setError(String(reason)))}><RefreshCw size={14}/></button></div>
    <div className="screenshots-layout"><div className="screenshots-list">{history.length?history.map(item=><button key={item.id} className={'screenshot-row '+(selectedID===item.id?'active':'')} onClick={()=>{setSelectedID(item.id);setImageError(false)}}><span className="screenshot-thumb"><Camera size={18}/></span><span><strong>Screen {item.screen} · {item.width} × {item.height}</strong><small>{date(item.at)}</small><small>{item.display_name||item.operator_id||'Operator not claimed'}</small></span></button>):<div className="screenshots-empty"><Camera size={24}/><p>No screenshots have been captured for this agent.</p></div>}</div><div className="screenshots-preview">{selected?<><div className="screenshot-preview-top"><div><strong>Screen {selected.screen}</strong><span>{date(selected.at)} · {(selected.size/1024).toFixed(0)} KiB</span></div><a href={`/api/screenshots/${encodeURIComponent(selected.id)}/image?download=1`} download><Download size={14}/> Download PNG</a></div>{imageError?<div className="screenshots-empty">The image could not be retrieved. The server record remains available.</div>:<a className="screenshot-image" href={`/api/screenshots/${encodeURIComponent(selected.id)}/image`} target="_blank" rel="noopener noreferrer" title="Open full-size image"><img src={`/api/screenshots/${encodeURIComponent(selected.id)}/image`} alt={`Screen ${selected.screen} captured ${date(selected.at)}`} onError={()=>setImageError(true)}/></a>}<div className="screenshot-meta"><span>SHA-256</span><code>{selected.sha256}</code></div></>:<div className="screenshots-empty"><Camera size={31}/><p>Select a retained screenshot to view it.</p></div>}</div></div>
  </div>;
}
