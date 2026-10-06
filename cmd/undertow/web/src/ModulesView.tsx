import {useEffect, useMemo, useRef, useState} from 'react';
import {Box, Download, FileCode2, FilePlus2, Play, Search, Square, X} from 'lucide-react';
import {api, csrfToken, type Agent} from './api';

type ArgumentSpec={name:string;type:string;required?:boolean};
type ModuleInfo={name:string;kind:'bof'|'module'|'wasm'|'assembly';description?:string;usage:string;help?:string;source:'packaged'|'imported';path:string;os?:string;arch?:string;format?:string;schema_known?:boolean;arguments?:ArgumentSpec[]};
type Catalog={modules:ModuleInfo[];preload_errors:string[]};
type RunEvent={kind:'output'|'stderr'|'error'|'exit'|'file';data:string};
type ModuleDownload={name:string;size:number;download:string};
function parseDownload(data:string):ModuleDownload|null {try{const value=JSON.parse(data) as ModuleDownload;return typeof value.name==='string'&&typeof value.size==='number'&&/^\/api\/transfers\/[a-f0-9]{64}\/download$/.test(value.download)?value:null}catch{return null}}
type JobResult={job:{id:string;agent_id:string;state:string}};
const kindName={bof:'BOF',module:'Native',wasm:'WASM',assembly:'.NET'};

function argumentSpecs(module:ModuleInfo):ArgumentSpec[] {
  if(module.arguments?.length)return module.arguments;
  if(!module.schema_known||!module.format)return [];
  const names:Record<string,string>={i:'Integer',s:'Short integer',z:'ANSI string',Z:'Wide string',b:'Binary file path'};
  return [...module.format].map((code,index)=>({name:`Argument ${index+1}`,type:names[code]||code,required:true}));
}
function compatibility(module:ModuleInfo,agent?:Agent):string {
  if(!agent)return 'Select an agent to run a module';
  if(module.os&&module.os.toLowerCase()!==agent.os?.toLowerCase()||module.arch&&module.arch.toLowerCase()!==agent.arch?.toLowerCase())return `Requires ${module.os}/${module.arch}; agent is ${agent.os||'unknown'}/${agent.arch||'unknown'}`;
  const needed=module.kind==='wasm'?'wasm':'native';
  if(agent.capabilities&&Array.isArray(agent.capabilities.allowed)&&!agent.capabilities.allowed.includes(needed))return `${needed} capability is disabled on this agent`;
  return '';
}
async function responseError(response:Response){const text=await response.text();try{const parsed=JSON.parse(text);return parsed.error||text}catch{return text||response.statusText}}
function fileBase64(file:File):Promise<string>{return file.arrayBuffer().then(buffer=>{const bytes=new Uint8Array(buffer);let binary='';for(const byte of bytes)binary+=String.fromCharCode(byte);return btoa(binary)})}

export function ModulesView({agent,agents,onJobs}:{agent?:Agent;agents?:Agent[];onJobs?:()=>void}) {
  const [catalog,setCatalog]=useState<Catalog>({modules:[],preload_errors:[]});
  const [selected,setSelected]=useState('');
  const [query,setQuery]=useState('');
  const [kind,setKind]=useState<'all'|'wasm'|'module'|'bof'|'assembly'>('all');
  const [targetID,setTargetID]=useState(agent?.id||'');
  const [args,setArgs]=useState('');
  const [values,setValues]=useState<string[]>([]);
  const [extra,setExtra]=useState<File|null>(null);
  const [output,setOutput]=useState('');
  const [files,setFiles]=useState<ModuleDownload[]>([]);
  const [jobID,setJobID]=useState('');
  const [busy,setBusy]=useState(false);
  const [error,setError]=useState('');
  const [notice,setNotice]=useState('');
  const [importOpen,setImportOpen]=useState(false);
  const [importKind,setImportKind]=useState<'bof'|'module'|'wasm'|'assembly'>('wasm');
  const [importFile,setImportFile]=useState<File|null>(null);
  const [manifest,setManifest]=useState<File|null>(null);
  const [importName,setImportName]=useState('');
  const [importFormat,setImportFormat]=useState('');
  const controller=useRef<AbortController|null>(null);
  const load=()=>api<Catalog>('/modules').then(data=>setCatalog({modules:data.modules||[],preload_errors:data.preload_errors||[]}));
  useEffect(()=>{load().catch(e=>setError(String(e)));return()=>controller.current?.abort()},[]);
  useEffect(()=>{if(agent)setTargetID(agent.id)},[agent?.id]);
  const effectiveAgent=agent||agents?.find(item=>item.id===targetID);
  const module=catalog.modules.find(item=>item.name===selected);
  const specs=module?argumentSpecs(module):[];
  const available=module?compatibility(module,effectiveAgent):'';
  const filtered=useMemo(()=>catalog.modules.filter(item=>(kind==='all'||item.kind===kind)&&(!query||`${item.name} ${item.description||''} ${item.kind}`.toLowerCase().includes(query.toLowerCase()))),[catalog.modules,kind,query]);
  const choose=(name:string)=>{setSelected(name);setArgs('');setValues([]);setExtra(null);setOutput('');setFiles([]);setJobID('');setError('');setNotice('')};
  const importModule=async()=>{
    if(!importFile)return;
    setBusy(true);setError('');setNotice('');
    try{
      const body=new FormData();body.set('kind',importKind);body.set('file',importFile);if(importName.trim())body.set('name',importName.trim());if(manifest)body.set('manifest',manifest);if(importFormat.trim())body.set('format',importFormat.trim());
      const response=await fetch('/api/modules',{method:'POST',headers:{'X-Undertow-CSRF':csrfToken()},body});
      if(!response.ok)throw new Error(await responseError(response));
      const imported=await response.json() as ModuleInfo;
      await load();choose(imported.name);setImportOpen(false);setImportFile(null);setManifest(null);setImportName('');setImportFormat('');setNotice(`${imported.name} loaded on this client. No agent operation was started.`);
    }catch(e){setError(String(e))}finally{setBusy(false)}
  };
  const unload=async()=>{if(!module)return;setBusy(true);setError('');try{await api(`/modules/${encodeURIComponent(module.name)}`,'DELETE');await load();choose('');setNotice(`${module.name} unloaded from this client.`)}catch(e){setError(String(e))}finally{setBusy(false)}};
  const run=async(background:boolean)=>{
    if(!module||!effectiveAgent||available)return;
    setBusy(true);setError('');setNotice('');setOutput('');setFiles([]);setJobID('');
    const signal=new AbortController();controller.current=signal;
    try{
      if(extra&&extra.size>64*1024)throw new Error('Input file exceeds 64 KiB');
      const input=extra?await fileBase64(extra):undefined;
      const typed=module.kind==='bof'&&specs.length>0;
      let chosenValues=values;
      if(typed){
        const required=specs.reduce((count,spec,index)=>spec.required===false?count:index+1,0);
        const last=values.reduce((index,value,current)=>value!==''?current+1:index,0);
        chosenValues=Array.from({length:Math.max(required,last)},(_,index)=>values[index]||'');
      }
      const body={background,input,...(typed?{values:chosenValues}:{args})};
      const response=await fetch(`/api/agents/${encodeURIComponent(effectiveAgent.id)}/modules/${encodeURIComponent(module.name)}/run`,{method:'POST',headers:{'Content-Type':'application/json','X-Undertow-CSRF':csrfToken()},body:JSON.stringify(body),signal:signal.signal});
      if(!response.ok)throw new Error(await responseError(response));
      if(response.headers.get('Content-Type')?.includes('application/json')){const result=await response.json() as JobResult;setJobID(result.job.id);setNotice(result.job.state==='queued'?`${module.name} queued as job ${result.job.id}. It will run at the next check-in; output is retained in Jobs.`:`${module.name} started as job ${result.job.id}. Output is retained on the server.`);return}
      if(!response.body)throw new Error('Live module output is unavailable');
      const reader=response.body.getReader(),decoder=new TextDecoder();let pending='';
      for(;;){const {done,value}=await reader.read();if(done)break;pending+=decoder.decode(value,{stream:true});let newline;while((newline=pending.indexOf('\n'))>=0){const line=pending.slice(0,newline);pending=pending.slice(newline+1);if(!line)continue;const event=JSON.parse(line) as RunEvent;if(event.kind==='file'){const file=parseDownload(event.data);if(file)setFiles(previous=>[...previous,file]);else setError('Invalid file download event');continue}const part=event.kind==='exit'?`\n[exit ${event.data}]\n`:event.kind==='error'?`\n[error] ${event.data}\n`:event.kind==='stderr'?`[stderr] ${event.data}`:event.data;setOutput(previous=>(previous+part).slice(-500000))}}
    }catch(e){if((e as Error).name==='AbortError')setNotice('Live run stopped.');else setError(String(e))}finally{controller.current=null;setBusy(false);window.dispatchEvent(new CustomEvent('undertow-console-updated',{detail:{agentID:effectiveAgent.id}}))}
  };
  return <div className="modules-layout"><section className="module-catalog"><div className="module-catalog-header"><h3>Client module bank</h3><button onClick={()=>setImportOpen(value=>!value)}><FilePlus2 size={14}/>Load file</button></div><div className="module-search"><Search size={14}/><input aria-label="Search modules" value={query} onChange={event=>setQuery(event.target.value)} placeholder="Search loaded commands"/></div><div className="module-filters">{(['all','wasm','module','assembly','bof'] as const).map(value=><button key={value} className={kind===value?'active':''} onClick={()=>setKind(value)}>{value==='all'?'All':kindName[value]}</button>)}</div><div className="module-count">{filtered.length} loaded command{filtered.length===1?'':'s'}</div><div className="module-list">{filtered.map(item=><button key={item.name} className={'module-row '+(selected===item.name?'active':'')} onClick={()=>choose(item.name)}><span className={'module-kind '+item.kind}>{item.kind==='bof'?<Box size={15}/>:<FileCode2 size={15}/>}</span><span><strong>{item.name}</strong><small>{item.description||`${kindName[item.kind]} module`}</small></span></button>)}</div></section>
    <div className="module-main">{error&&<div className="module-alert error" role="alert">{error}</div>}{notice&&<div className="module-alert">{notice}</div>}{catalog.preload_errors.length>0&&<details className="module-preload"><summary>{catalog.preload_errors.length} module preload issue{catalog.preload_errors.length===1?'':'s'}</summary>{catalog.preload_errors.map((item,index)=><p key={index}>{item}</p>)}</details>}
      {importOpen&&<section className="module-import"><div className="module-heading"><FilePlus2 size={21}/><div><h3>Load a local module</h3><p>The browser uploads the file to this Undertow client. Loading does not contact an agent.</p></div><button title="Close import" onClick={()=>setImportOpen(false)}><X size={16}/></button></div><div className="module-fields"><label>Runtime<select value={importKind} onChange={event=>setImportKind(event.target.value as 'bof'|'module'|'wasm'|'assembly')}><option value="wasm">WASM (.wasm)</option><option value="module">Native (.module)</option><option value="assembly">.NET Framework (.exe/.dll)</option><option value="bof">BOF (.o)</option></select></label><label>Command name (optional)<input value={importName} onChange={event=>setImportName(event.target.value)} placeholder="Derived from filename"/></label><label className="wide">Module file<input type="file" accept={importKind==='wasm'?'.wasm':importKind==='module'?'.module':importKind==='assembly'?'.exe,.dll':'.o'} onChange={event=>setImportFile(event.target.files?.[0]||null)}/></label><label>Help/argument sidecar (optional)<input type="file" accept=".json,application/json" onChange={event=>setManifest(event.target.files?.[0]||null)}/></label>{importKind==='bof'&&<label>BOF format (optional)<input value={importFormat} onChange={event=>setImportFormat(event.target.value)} placeholder="e.g. zi"/></label>}</div><button className="module-primary" disabled={busy||!importFile} onClick={importModule}>{busy?'Loading…':'Load into client bank'}</button></section>}
      {module?<section className="module-detail"><div className="module-heading"><span className={'module-kind large '+module.kind}><Box size={25}/></span><div><span className="eyebrow">{kindName[module.kind].toUpperCase()} · {module.source.toUpperCase()}</span><h3>{module.name}</h3><p>{module.description||'No description supplied with this module.'}</p></div></div><div className="module-facts"><div><span>Usage</span><code>{module.usage}</code></div><div><span>Source on client</span><code>{module.path}</code></div><div><span>Target</span><strong>{module.os?`${module.os}/${module.arch}`:'Portable WASM'}</strong></div>{module.kind==='bof'&&<div><span>Argument schema</span><strong>{module.schema_known?module.format||'No arguments':'Unspecified · supplied values use ANSI strings'}</strong></div>}</div>{module.help&&<div className="module-help"><strong>Module help</strong><p>{module.help}</p></div>}
        {!agent&&<label className="module-target">Target agent<select value={targetID} onChange={event=>setTargetID(event.target.value)}><option value="">Select an agent</option>{agents?.map(item=><option key={item.id} value={item.id}>{item.nickname||item.hostname||item.id.slice(0,16)} · {item.id.slice(0,8)} · {item.os||'unknown'}/{item.arch||'unknown'}</option>)}</select></label>}
        <div className="module-run"><div className="module-run-title"><Play size={16}/><strong>Run on {effectiveAgent?.hostname||'selected agent'}</strong></div>{available&&<div className="module-incompatible">{available}</div>}{specs.length>0&&module.kind==='bof'?<div className="module-arguments">{specs.map((spec,index)=><label key={index}>{spec.name} <small>{spec.type}{spec.required===false?' · optional':''}</small><input value={values[index]||''} onChange={event=>setValues(current=>{const next=[...current];next[index]=event.target.value;return next})}/></label>)}</div>:<label className="module-argline">Arguments<input value={args} onChange={event=>setArgs(event.target.value)} placeholder={module.kind==='bof'?'Space-separated values; quotes preserve spaces':'Optional arguments; quotes preserve spaces'}/></label>}{module.kind!=='bof'&&module.kind!=='assembly'&&<label className="module-input">{module.kind==='wasm'?'Optional stdin file':'Optional data file'}<input type="file" onChange={event=>setExtra(event.target.files?.[0]||null)}/><small>Read by the local client when Run is clicked. Maximum 64 KiB.</small></label>}
          <div className="module-run-actions"><button className="module-primary" disabled={busy||!!available} onClick={()=>run(true)}><Play size={13}/>Run background</button><button disabled={busy||!!available} onClick={()=>run(false)}><Play size={13}/>Stream foreground</button>{busy&&controller.current&&<button className="module-stop" onClick={()=>controller.current?.abort()}><Square size={12}/>Stop live run</button>}</div><p className="module-run-note">Output is retained in server Jobs. A check-in agent queues either choice and runs it at its next callback; a continuous agent can stream foreground output here.</p>
          {jobID&&<div className="module-job"><strong>Job {jobID}</strong>{onJobs&&<button onClick={onJobs}>Open Jobs</button>}</div>}{output&&<pre className="module-output">{output}</pre>}{files.length>0&&<div className="module-files"><strong>Received files</strong>{files.map((file,index)=><a key={`${file.download}-${index}`} href={file.download} download><Download size={13}/>{file.name} · {file.size.toLocaleString()} bytes</a>)}</div>}</div>
        <div className="module-footer"><button disabled={busy} onClick={unload}>Unload from this client</button><span>{module.source==='packaged'?'Packaged modules load again when this client restarts.':'Imported modules are removed from the client cache when unloaded.'}</span></div></section>:<section className="module-selection"><Box size={33}/><h3>Select a module</h3><p>Choose a loaded command to inspect its help and run it against a selected agent. No module runs on selection.</p></section>}
    </div></div>;
}
