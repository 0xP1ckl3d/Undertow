import {useEffect, useState} from 'react';
import {Download, Radio} from 'lucide-react';
import {api, type Agent} from './api';

type Host = {id:string;artifact_id:string;agent_id:string;bind:string;public_host:string;retrieval:string;pipe_path?:string;started:string};

function downloadScript(text:string,filename:string){
  const href=URL.createObjectURL(new Blob([text],{type:'text/plain;charset=utf-8'}));
  const link=document.createElement('a');link.href=href;link.download=filename;document.body.appendChild(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(href),2000);
}

export function AgentPayloadHosting({artifactID,agents,relays,relayTarget,revision}:{artifactID:string;agents:Agent[];relays:{agent_id:string;bind:string}[];relayTarget?:{agentID:string;bind:string}|null;revision?:string}){
  const [hosts,setHosts]=useState<Host[]>([]),[agentID,setAgentID]=useState(''),[bind,setBind]=useState(''),[publicHost,setPublicHost]=useState('');
  const [format,setFormat]=useState<'powershell'|'shell'>('powershell'),[script,setScript]=useState<{text:string;filename:string;hostID:string;kind:'probe'|'deploy'}|null>(null);
  const [busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState(''),[stopping,setStopping]=useState('');
  const refresh=()=>api<Host[]|null>(`/artifacts/${encodeURIComponent(artifactID)}/agent-hosts`).then(value=>setHosts(value||[]));
  useEffect(()=>{setHosts([]);setScript(null)},[artifactID]);
  useEffect(()=>{refresh().catch(e=>setError(String(e)))},[artifactID,revision]);
  useEffect(()=>{if(relayTarget){setAgentID(relayTarget.agentID);setBind(relayTarget.bind);setPublicHost('')}},[relayTarget]);
  const pipe=bind.startsWith('\\\\.\\pipe\\');
  const parent=agents.find(agent=>agent.id===agentID);
  const start=async()=>{
    setBusy(true);setError('');setNotice('');
    try{
      const host=await api<Host>(`/artifacts/${encodeURIComponent(artifactID)}/agent-hosts`,'POST',{agent_id:agentID,bind:bind.trim(),public_host:publicHost.trim()});
      await refresh();setNotice(`Payload delivery configured on ${host.bind}. The parent listener is active. Its reachability from another workstation depends on the host firewall or SMB authentication. The deploy helper is available now.`);
    }catch(e){setError(String(e))}finally{setBusy(false)}
  };
  const stop=async(id:string)=>{
    setBusy(true);setError('');
    try{await api(`/agent-hosts/${encodeURIComponent(id)}`,'DELETE');await refresh();setStopping('');if(script?.hostID===id)setScript(null);setNotice('Payload delivery disabled; the relay is still active.')}catch(e){setError(String(e))}finally{setBusy(false)}
  };
  const preview=async(id:string,kind:'probe'|'deploy')=>{
    setBusy(true);setError('');
    try{const selected=hosts.find(host=>host.id===id);const helper=selected?.pipe_path?'powershell':format;const result=await api<{script:string;filename:string}>(`/agent-hosts/${encodeURIComponent(id)}/${kind==='probe'?'probe-script':'deploy-script'}?format=${helper}`);setScript({text:result.script,filename:result.filename,hostID:id,kind})}catch(e){setError(String(e))}finally{setBusy(false)}
  };
  return <div className="agent-payload-hosting">
    <div className="payload-section-title"><h3><Radio size={16}/> Host through an agent</h3><button onClick={()=>refresh().catch(e=>setError(String(e)))}>Refresh</button></div>
    <p>Enable payload delivery on an existing relay. TCP listeners accept pinned HTTPS downloads; Windows named pipes accept a pinned PowerShell helper over the same pipe. The parent fetches each artifact from the server through its current session. Child agent sessions continue on that listener.</p>
    <div className="payload-fields">
      <label>Parent agent<select value={agentID} onChange={e=>{setAgentID(e.target.value);setBind('')}}><option value="">Select parent with active relay</option>{agents.filter(a=>a.online!==false&&relays.some(r=>r.agent_id===a.id)).map(a=><option key={a.id} value={a.id}>{a.hostname||a.id} · {a.id.slice(0,12)}</option>)}</select></label>
      <label>Existing relay listener<select value={bind} onChange={e=>setBind(e.target.value)}><option value="">Select listener</option>{relays.filter(r=>r.agent_id===agentID).map(r=><option key={r.bind} value={r.bind}>{r.bind.startsWith('\\\\.\\pipe\\')?'SMB pipe · ':'TCP · '}{r.bind}</option>)}</select><small>The selected listener continues carrying child sessions.</small></label>
      <label className="wide">Parent host or IP reachable by child<input value={publicHost} onChange={e=>setPublicHost(e.target.value)} placeholder={pipe?'Windows SMB host name or IP; no pipe path':'Parent agent IP or DNS name; no port'}/><small>{pipe?'The helper connects to this host over SMB using the selected pipe name. Windows must permit access to that pipe.':'The listener port is added to the HTTPS URL automatically.'} {parent?`This address must resolve to ${parent.hostname||parent.id} from the child workstation, not just from this operator client.`:'This address must resolve to the selected parent from the child workstation.'}</small>{parent?.public_ip&&<small>Observed public IP: {parent.public_ip}. This may be a shared NAT address; use the address reachable from the child.</small>}</label>
    </div>
    {!relays.length&&<p>No active relay is available. Start one in Relays, then return to this artifact.</p>}
    <button className="primary" disabled={busy||!agentID||!bind.trim()||!publicHost.trim()} onClick={start}>{busy?'Working…':pipe?'Enable pipe delivery':'Enable HTTPS downloads'}</button>
    {error&&<div className="payload-alert error" role="alert">{error}</div>}{notice&&<div className="payload-alert">{notice}</div>}
    <div className="agent-host-list"><strong>Relay payload delivery</strong>{hosts.length===0?<p>No relay delivery is active for this artifact.</p>:hosts.map(host=><div className="agent-host-card" key={host.id}>
      <div><b>{agents.find(a=>a.id===host.agent_id)?.hostname||host.agent_id}</b><span>{host.bind} · started {new Date(host.started).toLocaleString()}</span></div>
      <label>{host.pipe_path?'SMB pipe endpoint (helper only)':'HTTPS download URL'}<input readOnly value={host.retrieval}/></label>
      <small>The parent accepted the listener. A cross-host probe is optional and requires access to that host; it downloads and starts nothing. TCP may need an inbound firewall rule. SMB needs Windows authentication to the parent.</small>
      <div className="payload-actions">{host.pipe_path?<span>Windows PowerShell</span>:<select value={format} onChange={e=>{setFormat(e.target.value as 'powershell'|'shell');setScript(null)}}><option value="powershell">PowerShell</option><option value="shell">POSIX shell</option></select>}<button disabled={busy} onClick={()=>preview(host.id,'deploy')}>Preview deploy script</button><button disabled={busy} onClick={()=>preview(host.id,'probe')}>Preview diagnostic script</button>{script?.hostID===host.id&&<button onClick={()=>downloadScript(script.text,script.filename)}><Download size={13}/> Download {script.kind==='probe'?'diagnostic':'deploy'} script</button>}<button className="danger" disabled={busy} onClick={()=>setStopping(host.id)}>Disable delivery</button></div>
      {script?.hostID===host.id&&<pre>{script.text}</pre>}
      {stopping===host.id&&<div className="payload-confirm">Disable this payload delivery? Child agent sessions on this relay continue. <button disabled={busy} onClick={()=>stop(host.id)}>Disable delivery</button><button onClick={()=>setStopping('')}>Cancel</button></div>}
    </div>)}</div>
  </div>;
}
