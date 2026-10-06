import {useEffect, useMemo, useRef, useState} from 'react';
import {ArrowUp, Download, File as FileIcon, Folder, FolderOpen, RefreshCw, Upload} from 'lucide-react';
import {api, csrfToken} from './api';

type FileEntry = {name:string;is_dir:boolean;is_link:boolean;size:number;mode:string;modified:string;error?:string};
type FileListing = {path:string;parent:string;entries:FileEntry[];offset:number;has_more:boolean;at:string};
const lastListing = new Map<string,FileListing>();
function size(value:number){if(value<1024)return value+' B';const units=['KiB','MiB','GiB','TiB'];let unit='B';for(const next of units){value/=1024;unit=next;if(value<1024)break}return value.toFixed(value<10?1:0)+' '+unit}
function date(value:string){return value?new Date(value).toLocaleString():'—'}
function joinPath(parent:string,name:string){return parent.replace(/[\\/]$/,'')+(parent.includes('\\')?'\\':'/')+name}

export function FilesView({agentID}:{agentID:string;revision?:string}){
  const [listing,setListing]=useState<FileListing|null>(()=>lastListing.get(agentID)||null);
  const [path,setPath]=useState(lastListing.get(agentID)?.path||'');
  const [selected,setSelected]=useState('');
  const [loading,setLoading]=useState(false);
  const [error,setError]=useState('');
  const [transfer,setTransfer]=useState<{kind:string;percent:number;bytes:number;total:number;message:string}|null>(null);
  const [upload,setUpload]=useState<File|null>(null),[uploadPath,setUploadPath]=useState(''),[folderName,setFolderName]=useState(''),[creating,setCreating]=useState(false);
  const sequence=useRef(0);
  useEffect(()=>{sequence.current++;const cached=lastListing.get(agentID)||null;setListing(cached);setPath(cached?.path||'');setSelected('');setError('');setLoading(false)},[agentID]);
  const entries=useMemo(()=>[...(listing?.entries||[])].sort((a,b)=>Number(b.is_dir)-Number(a.is_dir)||a.name.localeCompare(b.name)),[listing]);
  const chosen=listing?.entries.find(entry=>entry.name===selected);
  const browse=async(directory:string,offset=0)=>{
    const current=++sequence.current;setLoading(true);setError('');
    try{
      const result=await api<{files?:FileListing;error?:string}>(`/agents/${encodeURIComponent(agentID)}/exec`,'POST',{builtin:'file-list',args:[directory,...(offset?[String(offset)]:[])]});
      if(sequence.current!==current)return;
      if(result.error)throw new Error(result.error);
      if(!result.files)throw new Error('Agent returned no structured file list.');
      setListing(result.files);setPath(result.files.path);setSelected('');lastListing.set(agentID,result.files);
    }catch(reason){if(sequence.current===current)setError(String(reason))}
    finally{if(sequence.current===current)setLoading(false)}
  };
  const transferFile=async(operation:'upload'|'download',remotePath:string,file?:File)=>{
    setError('');setTransfer({kind:operation,percent:0,bytes:0,total:file?.size||0,message:operation==='upload'?'Sending to agent…':'Receiving from agent…'});
    try{
      const endpoint=operation==='upload'?`/api/agents/${encodeURIComponent(agentID)}/files/upload?path=${encodeURIComponent(remotePath)}`:`/api/agents/${encodeURIComponent(agentID)}/files/download`;
      const response=await fetch(endpoint,{method:'POST',headers:{'X-Undertow-CSRF':csrfToken(),'Content-Type':operation==='upload'?'application/octet-stream':'application/json'},body:operation==='upload'?file:JSON.stringify({path:remotePath})});
      if(!response.ok)throw new Error(await response.text());if(!response.body)throw new Error('Transfer stream unavailable');
      const reader=response.body.getReader(),decoder=new TextDecoder();let pending='',completed=false;
      const receive=(row:string)=>{if(!row)return;const event=JSON.parse(row) as {kind:string;data:{bytes?:number;total?:number;percent?:number;size?:number;sha256?:string;download?:string}|string};
        if(event.kind==='error')throw new Error(String(event.data));
        if(event.kind==='started'){const p=event.data as {state?:string};if(p.state==='pending')setTransfer({kind:operation,bytes:0,total:file?.size||0,percent:0,message:'Waiting for agent check-in…'})}
        if(event.kind==='progress'){const p=event.data as {bytes:number;total:number;percent:number};setTransfer({kind:operation,bytes:p.bytes,total:p.total,percent:p.percent,message:operation==='upload'?'Sending to agent…':'Receiving from agent…'})}
        if(event.kind==='complete'){const item=event.data as {size:number;sha256:string;download?:string};completed=true;setTransfer({kind:operation,bytes:item.size,total:item.size,percent:100,message:`Verified ${item.size.toLocaleString()} bytes · SHA-256 ${item.sha256}`});if(item.download){const link=document.createElement('a');link.href=item.download;link.download='';document.body.appendChild(link);link.click();link.remove()}else{setUpload(null);setUploadPath('');browse(listing?.path||path)}}};
      for(;;){const {done,value}=await reader.read();if(done)break;pending+=decoder.decode(value,{stream:true});let index;while((index=pending.indexOf('\n'))>=0){receive(pending.slice(0,index));pending=pending.slice(index+1)}}
      if(pending.trim())receive(pending);if(!completed)throw new Error('Transfer ended before verification');
    }catch(reason){setTransfer(null);setError(String(reason))}
  };
  const mkdir=async()=>{if(!listing||!folderName.trim())return;setCreating(true);setError('');try{const result=await api<{error?:string}>(`/agents/${encodeURIComponent(agentID)}/exec`,'POST',{builtin:'mkdir',args:[joinPath(listing.path,folderName.trim())]});if(result.error)throw new Error(result.error);setFolderName('');await browse(listing.path)}catch(e){setError(String(e))}finally{setCreating(false)}};
  return <div className="files-view">
    <div className="files-toolbar"><div><h3>Agent file system</h3><p>Browse directories on this agent. Selecting a file reads metadata only; opening a directory is an explicit action.</p></div><div className="files-path"><input aria-label="Directory on agent" value={path} onChange={event=>setPath(event.target.value)} onKeyDown={event=>{if(event.key==='Enter')browse(path)}} placeholder="Agent working directory"/><button disabled={loading} onClick={()=>browse(path)}>{loading?'Loading…':listing?'Go':'Browse'}</button></div></div>
    {error&&<div className="files-error" role="alert">{error}</div>}
    {(loading||creating)&&<div className="files-transfer" role="status">{loading?'Waiting for the directory listing':'Waiting to create the folder'}{` · the agent starts at its next check-in when sleeping.`}</div>}
    {listing&&<div className="files-operations"><div><label className="files-upload-pick"><Upload size={14}/> Choose local file<input aria-label="Choose local file" type="file" onChange={e=>{const file=e.target.files?.[0]||null;setUpload(file);setUploadPath(file?joinPath(listing.path,file.name):'')}}/></label>{upload&&<><span title={upload.name}>{upload.name} · {size(upload.size)}</span><input aria-label="Upload destination on agent" value={uploadPath} onChange={e=>setUploadPath(e.target.value)}/><button disabled={!!transfer&&transfer.percent<100||!uploadPath.trim()} onClick={()=>transferFile('upload',uploadPath,upload)}>Upload</button></>}</div><div><input aria-label="New folder name" value={folderName} onChange={e=>setFolderName(e.target.value)} placeholder="New folder name"/><button disabled={creating||!folderName.trim()} onClick={mkdir}>Create folder</button></div></div>}
    {transfer&&<div className="files-transfer" role="status"><strong>{transfer.kind==='upload'?'Upload':'Download'}</strong><span>{transfer.message}</span>{transfer.percent<100&&<><progress max="100" value={transfer.percent}/><small>{size(transfer.bytes)} / {transfer.total?size(transfer.total):'unknown'}</small></>}</div>}
    {listing?<><div className="files-location"><button disabled={loading||listing.parent===listing.path} onClick={()=>browse(listing.parent)} title="Parent directory"><ArrowUp size={14}/> Parent</button><code title={listing.path}>{listing.path}</code><button disabled={loading} onClick={()=>browse(listing.path,listing.offset)} title="Refresh directory"><RefreshCw size={14}/></button><span>Listed {date(listing.at)}</span></div><div className="files-layout"><div className="files-list"><div className="files-columns"><span>Name</span><span>Size</span><span>Modified</span></div>{entries.length?entries.map(entry=><button key={entry.name} className={'files-entry '+(selected===entry.name?'active':'')} onClick={()=>setSelected(entry.name)} onDoubleClick={()=>entry.is_dir&&!entry.is_link&&browse(joinPath(listing.path,entry.name))} onKeyDown={event=>{if(event.key==='Enter'&&entry.is_dir&&!entry.is_link)browse(joinPath(listing.path,entry.name))}}><span>{entry.is_dir?<Folder size={16}/>:<FileIcon size={15}/>}<strong>{entry.name}{entry.is_link?' ↗':''}</strong></span><span>{entry.is_dir?'Folder':size(entry.size)}</span><span>{date(entry.modified)}</span></button>):<div className="files-empty">This directory is empty.</div>}<div className="files-pagination"><span>{listing.entries.length?listing.offset+1:0}–{listing.offset+listing.entries.length}{listing.has_more?' · more available':''}</span><div><button disabled={loading||listing.offset===0} onClick={()=>browse(listing.path,Math.max(0,listing.offset-200))}>Previous</button><button disabled={loading||!listing.has_more} onClick={()=>browse(listing.path,listing.offset+200)}>Next</button></div></div></div><aside className="files-detail">{chosen?<><div className="files-detail-icon">{chosen.is_dir?<FolderOpen size={26}/>:<FileIcon size={25}/>}</div><h3>{chosen.name}</h3><dl><div><dt>Type</dt><dd>{chosen.is_link?'Symbolic link':chosen.is_dir?'Directory':'File'}</dd></div><div><dt>Size</dt><dd>{chosen.is_dir?'—':size(chosen.size)}</dd></div><div><dt>Modified</dt><dd>{date(chosen.modified)}</dd></div><div><dt>Mode</dt><dd>{chosen.mode||'—'}</dd></div></dl>{chosen.error&&<p className="files-error">{chosen.error}</p>}{chosen.is_dir&&!chosen.is_link&&<button className="files-open" disabled={loading} onClick={()=>browse(joinPath(listing.path,chosen.name))}>Open folder</button>}{!chosen.is_dir&&!chosen.is_link&&<button className="files-open" disabled={!!transfer&&transfer.percent<100} onClick={()=>transferFile('download',joinPath(listing.path,chosen.name))}><Download size={14}/> Download verified file</button>}</>:<div className="files-detail-empty"><FolderOpen size={27}/><h3>Select an entry</h3><p>Inspect a file or open a folder. The directory was listed at {date(listing.at)}.</p></div>}</aside></div></>:<div className="files-first"><FolderOpen size={35}/><h3>Browse the agent file system</h3><p>Enter a path or use the agent’s working directory. The agent is contacted only when you click Browse.</p><button disabled={loading} onClick={()=>browse(path)}>{loading?'Loading…':'Browse working directory'}</button></div>}
  </div>
}
