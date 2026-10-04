import {useEffect, useMemo, useRef, useState} from 'react';
import {ArrowUp, File as FileIcon, Folder, FolderOpen, RefreshCw} from 'lucide-react';
import {api} from './api';

type FileEntry = {name:string;is_dir:boolean;is_link:boolean;size:number;mode:string;modified:string;error?:string};
type FileListing = {path:string;parent:string;entries:FileEntry[];offset:number;has_more:boolean;at:string};
const lastListing = new Map<string,FileListing>();
function size(value:number){if(value<1024)return value+' B';const units=['KiB','MiB','GiB','TiB'];let unit='B';for(const next of units){value/=1024;unit=next;if(value<1024)break}return value.toFixed(value<10?1:0)+' '+unit}
function date(value:string){return value?new Date(value).toLocaleString():'—'}
function joinPath(parent:string,name:string){return parent.replace(/[\\/]$/,'')+(parent.includes('\\')?'\\':'/')+name}

export function FilesView({agentID}:{agentID:string}){
  const [listing,setListing]=useState<FileListing|null>(()=>lastListing.get(agentID)||null);
  const [path,setPath]=useState(lastListing.get(agentID)?.path||'');
  const [selected,setSelected]=useState('');
  const [loading,setLoading]=useState(false);
  const [error,setError]=useState('');
  const sequence=useRef(0);
  useEffect(()=>{sequence.current++;const cached=lastListing.get(agentID)||null;setListing(cached);setPath(cached?.path||'');setSelected('');setError('');setLoading(false);return()=>{sequence.current++}},[agentID]);
  const entries=useMemo(()=>[...(listing?.entries||[])].sort((a,b)=>Number(b.is_dir)-Number(a.is_dir)||a.name.localeCompare(b.name)),[listing]);
  const chosen=listing?.entries.find(entry=>entry.name===selected);
  const browse=async(directory:string,offset=0)=>{
    const current=++sequence.current;setLoading(true);setError('');
    try{
      const query=new URLSearchParams();if(directory)query.set('path',directory);if(offset)query.set('offset',String(offset));
      const result=await api<FileListing>(`/agents/${encodeURIComponent(agentID)}/files?${query}`);
      if(sequence.current!==current)return;
      setListing(result);setPath(result.path);setSelected('');lastListing.set(agentID,result);
    }catch(reason){if(sequence.current===current)setError(String(reason))}
    finally{if(sequence.current===current)setLoading(false)}
  };
  return <div className="files-view">
    <div className="files-toolbar"><div><h3>Agent file system</h3><p>Browse directories on this agent. Selecting a file reads metadata only; opening a directory is an explicit action.</p></div><div className="files-path"><input aria-label="Directory on agent" value={path} onChange={event=>setPath(event.target.value)} onKeyDown={event=>{if(event.key==='Enter')browse(path)}} placeholder="Agent working directory"/><button disabled={loading} onClick={()=>browse(path)}>{loading?'Loading…':listing?'Go':'Browse'}</button></div></div>
    {error&&<div className="files-error" role="alert">{error}</div>}
    {listing?<><div className="files-location"><button disabled={loading||listing.parent===listing.path} onClick={()=>browse(listing.parent)} title="Parent directory"><ArrowUp size={14}/> Parent</button><code title={listing.path}>{listing.path}</code><button disabled={loading} onClick={()=>browse(listing.path,listing.offset)} title="Refresh directory"><RefreshCw size={14}/></button><span>Listed {date(listing.at)}</span></div><div className="files-layout"><div className="files-list"><div className="files-columns"><span>Name</span><span>Size</span><span>Modified</span></div>{entries.length?entries.map(entry=><button key={entry.name} className={'files-entry '+(selected===entry.name?'active':'')} onClick={()=>setSelected(entry.name)} onDoubleClick={()=>entry.is_dir&&!entry.is_link&&browse(joinPath(listing.path,entry.name))} onKeyDown={event=>{if(event.key==='Enter'&&entry.is_dir&&!entry.is_link)browse(joinPath(listing.path,entry.name))}}><span>{entry.is_dir?<Folder size={16}/>:<FileIcon size={15}/>}<strong>{entry.name}{entry.is_link?' ↗':''}</strong></span><span>{entry.is_dir?'Folder':size(entry.size)}</span><span>{date(entry.modified)}</span></button>):<div className="files-empty">This directory is empty.</div>}<div className="files-pagination"><span>{listing.entries.length?listing.offset+1:0}–{listing.offset+listing.entries.length}{listing.has_more?' · more available':''}</span><div><button disabled={loading||listing.offset===0} onClick={()=>browse(listing.path,Math.max(0,listing.offset-200))}>Previous</button><button disabled={loading||!listing.has_more} onClick={()=>browse(listing.path,listing.offset+200)}>Next</button></div></div></div><aside className="files-detail">{chosen?<><div className="files-detail-icon">{chosen.is_dir?<FolderOpen size={26}/>:<FileIcon size={25}/>}</div><h3>{chosen.name}</h3><dl><div><dt>Type</dt><dd>{chosen.is_link?'Symbolic link':chosen.is_dir?'Directory':'File'}</dd></div><div><dt>Size</dt><dd>{chosen.is_dir?'—':size(chosen.size)}</dd></div><div><dt>Modified</dt><dd>{date(chosen.modified)}</dd></div><div><dt>Mode</dt><dd>{chosen.mode||'—'}</dd></div></dl>{chosen.error&&<p className="files-error">{chosen.error}</p>}{chosen.is_dir&&!chosen.is_link&&<button className="files-open" disabled={loading} onClick={()=>browse(joinPath(listing.path,chosen.name))}>Open folder</button>}</>:<div className="files-detail-empty"><FolderOpen size={27}/><h3>Select an entry</h3><p>Inspect a file or open a folder. The directory was listed at {date(listing.at)}.</p></div>}</aside></div></>:<div className="files-first"><FolderOpen size={35}/><h3>Browse the agent file system</h3><p>Enter a path or use the agent’s working directory. The agent is contacted only when you click Browse.</p><button disabled={loading} onClick={()=>browse(path)}>{loading?'Loading…':'Browse working directory'}</button></div>}
  </div>
}
