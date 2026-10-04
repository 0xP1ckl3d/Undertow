import {useEffect, useState} from 'react';
import {Download, Radio} from 'lucide-react';
import {api, type Agent} from './api';

type Host = {id:string;artifact_id:string;agent_id:string;bind:string;public_host:string;retrieval:string;started:string};

function downloadScript(text:string,filename:string){
  const href=URL.createObjectURL(new Blob([text],{type:'text/plain;charset=utf-8'}));
  const link=document.createElement('a');link.href=href;link.download=filename;document.body.appendChild(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(href),2000);
}

export function AgentPayloadHosting({artifactID,agents,relays,relayTarget,revision}:{artifactID:string;agents:Agent[];relays:{agent_id:string;bind:string}[];relayTarget?:{agentID:string;bind:string}|null;revision?:string}){
  const [hosts,setHosts]=useState<Host[]>([]),[agentID,setAgentID]=useState(''),[bind,setBind]=useState(''),[publicHost,setPublicHost]=useState('');
  const [format,setFormat]=useState<'powershell'|'shell'>('powershell'),[script,setScript]=useState<{text:string;filename:string;hostID:string}|null>(null);
  const [busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState(''),[stopping,setStopping]=useState('');
  const refresh=()=>api<Host[]|null>(`/artifacts/${encodeURIComponent(artifactID)}/agent-hosts`).then(value=>setHosts(value||[]));
  useEffect(()=>{setHosts([]);setScript(null);refresh().catch(e=>setError(String(e)))},[artifactID,revision]);
  useEffect(()=>{if(relayTarget){setAgentID(relayTarget.agentID);setBind(/:\d+$/.test(relayTarget.bind)?relayTarget.bind:'');setPublicHost('')}},[relayTarget]);
  const start=async()=>{
    setBusy(true);setError('');setNotice('');
    try{
      const host=await api<Host>(`/artifacts/${encodeURIComponent(artifactID)}/agent-hosts`,'POST',{agent_id:agentID,bind:bind.trim(),public_host:publicHost.trim()});
      await refresh();setNotice(`Payload downloads enabled on relay ${host.bind}.`);
    }catch(e){setError(String(e))}finally{setBusy(false)}
  };
  const stop=async(id:string)=>{
    setBusy(true);setError('');
    try{await api(`/agent-hosts/${encodeURIComponent(id)}`,'DELETE');await refresh();setStopping('');if(script?.hostID===id)setScript(null);setNotice('Payload download URL disabled; the relay is still active.')}catch(e){setError(String(e))}finally{setBusy(false)}
  };
  const preview=async(id:string)=>{
    setBusy(true);setError('');
    try{const result=await api<{script:string;filename:string}>(`/agent-hosts/${encodeURIComponent(id)}/deploy-script?format=${format}`);setScript({text:result.script,filename:result.filename,hostID:id})}catch(e){setError(String(e))}finally{setBusy(false)}
  };
  return <div className="agent-payload-hosting">
    <div className="payload-section-title"><h3><Radio size={16}/> Host through an agent</h3><button onClick={()=>refresh().catch(e=>setError(String(e)))}>Refresh</button></div>
    <p>Enable HTTPS downloads on an existing relay listener. The parent agent requests each download from the Undertow server through its current session. Child agent sessions continue on the same address and port.</p>
    <div className="payload-fields">
      <label>Parent agent<select value={agentID} onChange={e=>{setAgentID(e.target.value);setBind('')}}><option value="">Select parent with active TCP relay</option>{agents.filter(a=>a.online!==false&&relays.some(r=>r.agent_id===a.id&&/:\d+$/.test(r.bind))).map(a=><option key={a.id} value={a.id}>{a.hostname||a.id} · {a.id.slice(0,12)}</option>)}</select></label>
      <label>Existing TCP relay listener<select value={bind} onChange={e=>setBind(e.target.value)}><option value="">Select listener</option>{relays.filter(r=>r.agent_id===agentID&&/:\d+$/.test(r.bind)).map(r=><option key={r.bind} value={r.bind}>{r.bind}</option>)}</select><small>This is the same address and port used by child agent sessions. SMB named pipes do not have an HTTPS download URL.</small></label>
      <label className="wide">Host or IP reachable by child<input value={publicHost} onChange={e=>setPublicHost(e.target.value)} placeholder="Parent agent IP or DNS name; no port"/><small>The listener port is added to the download URL automatically. This address must lead to the chosen parent agent.</small></label>
    </div>
    <button className="primary" disabled={busy||!agentID||!bind.trim()||!publicHost.trim()} onClick={start}>{busy?'Working…':'Enable payload downloads'}</button>
    {error&&<div className="payload-alert error" role="alert">{error}</div>}{notice&&<div className="payload-alert">{notice}</div>}
    <div className="agent-host-list"><strong>Active relay download URLs</strong>{hosts.length===0?<p>No relay download is enabled for this artifact.</p>:hosts.map(host=><div className="agent-host-card" key={host.id}>
      <div><b>{agents.find(a=>a.id===host.agent_id)?.hostname||host.agent_id}</b><span>{host.bind} · started {new Date(host.started).toLocaleString()}</span></div>
      <label>Download URL<input readOnly value={host.retrieval}/></label>
      <div className="payload-actions"><select value={format} onChange={e=>{setFormat(e.target.value as 'powershell'|'shell');setScript(null)}}><option value="powershell">PowerShell helper</option><option value="shell">POSIX shell helper</option></select><button disabled={busy} onClick={()=>preview(host.id)}>Preview deploy script</button>{script?.hostID===host.id&&<button onClick={()=>downloadScript(script.text,script.filename)}><Download size={13}/> Download script</button>}<button className="danger" disabled={busy} onClick={()=>setStopping(host.id)}>Disable download</button></div>
      {script?.hostID===host.id&&<pre>{script.text}</pre>}
      {stopping===host.id&&<div className="payload-confirm">Disable this download URL? Child agent sessions on this relay continue. <button disabled={busy} onClick={()=>stop(host.id)}>Disable download</button><button onClick={()=>setStopping('')}>Cancel</button></div>}
    </div>)}</div>
  </div>;
}
