export type Agent = { id:string; hostname?:string; nickname?:string; os?:string; arch?:string; artifact_id?:string; profile_id?:string; interfaces?:string[]; privilege?:'high'|'low'; archived?:boolean; online?:boolean; disconnected_at?:string; session_id?:string; connected?:string; transport?:string; via?:string; depth?:number; remote?:string; public_ip?:string; last_seen?:string; rtt_ns?:number; active_jobs?:number; sleep_supported?:boolean; sleep?:{interval_seconds:number;jitter_percent:number}; sleep_protocol_version?:number; idle_grace_seconds?:number; connection_mode?:'continuous'|'checkin'; connection_state?:'connected'|'sleeping'|'disconnected'; connection_reason?:string; sleep_started_at?:string; expected_checkin?:string; sleep_lost_after?:string; advertised_routes?:string[]; capabilities?:{allowed:string[];supported:string[]} };
export type Deployment = {id:string;source_agent_id:string;target:string;artifact_id:string;profile_id:string;profile:string;artifact_sha256:string;method:string;context:string;account?:string;operator_id?:string;operator_name?:string;requested_from:string;created_at:string;updated_at:string;prepared_at?:string;waiting_at?:string;completed_at?:string;state:'created'|'prepared'|'dispatching'|'waiting'|'completed'|'failed';progress?:string;error?:string;result_agent_id?:string;result_relationship?:string;job_id?:string;transfer_id?:string;delivery_type?:string;delivery_id?:string;install_path?:string;service_name?:string;task_name?:string};
export type Client = { id:string; hostname?:string; session_id:string; transport?:string; internal:boolean; vpn:boolean; accepted_routes?:{prefix:string;agent_id:string}[] };
export type Route = {prefix:string;agent_id:string;active:boolean};
export type Status = {server:{listeners?:{transport:string;listen:string;tls_mode?:string;sessions:number;agents:number;clients:number}[];fingerprint?:string;public_host?:string;websocket_path?:string};agents:Agent[];clients:Client[];routes:Route[]};
export type TopologyNode = {id:string;kind:string;label:string;hostname?:string;agent_id?:string;via?:string;client_id?:string;session_id?:string;carrier?:string;relay_bind?:string;remote?:string;public_ip?:string;last_seen?:string;rtt_ns?:number;depth?:number;os?:string;arch?:string;connected?:string;disconnected_at?:string;privilege?:'high'|'low';archived?:boolean;connection_mode?:'continuous'|'checkin';connection_state?:'connected'|'sleeping'|'disconnected';connection_reason?:string;sleep?:{interval_seconds:number;jitter_percent:number};sleep_protocol_version?:number;idle_grace_seconds?:number;expected_checkin?:string;sleep_lost_after?:string;internal?:boolean;vpn?:boolean;accepted_count?:number;listeners?:{transport:string;listen:string;sessions:number}[];carriers?:{transport:string;active:boolean;listen?:string;sessions?:number}[];public_host?:string;active:boolean};
export type TopologyEdge = {id:string;kind:string;source:string;target:string;label?:string;deployment_id?:string;active:boolean;client_id?:string;session_id?:string|number;internal?:boolean;vpn?:boolean;accepted_routes?:{prefix:string;agent_id:string;manual?:boolean}[];accepted_by?:string[];rtt_ns?:number;last_seen?:string};
export type Topology = {version:number;at:string;nodes:TopologyNode[];edges:TopologyEdge[]};
export type Job = {id:string;agent_id:string;deployment_id?:string;kind:string;language?:string;builtin?:string;args?:string[];argv?:string[];state:string;queued_at?:string;started:string;ended?:string;output_bytes:number;exit_code?:number;output?:string;output_truncated?:boolean;output_error?:string;screenshot_id?:string;exec_result?:{stdout?:string;stderr?:string;error?:string;files?:unknown;screens?:unknown};files?:{id:number;name:string;size:number}[]};
let csrf = '';
export async function connect() {
  const existing = await fetch('/api/client');
  if (existing.ok) { const body = await existing.json(); csrf = body.csrf || ''; sessionStorage.setItem('undertow_gui_opened','1'); return body; }
  const secret = location.hash.slice(1);
  if (!secret) throw new Error(sessionStorage.getItem('undertow_gui_opened')==='1'?'session_expired':'launch_link_required');
  const result = await fetch('/api/auth', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({secret})});
  if (!result.ok) throw new Error('The GUI launch link has expired. Run undertow client gui again.');
  csrf = (await result.json()).csrf;
  sessionStorage.setItem('undertow_gui_opened','1');
  history.replaceState(null, '', location.pathname);
  return await (await fetch('/api/client')).json();
}
export async function api<T>(path:string, method='GET', body?:unknown, signal?:AbortSignal):Promise<T> {
  const response = await fetch('/api'+path,{method,signal,headers:{...(body!==undefined?{'Content-Type':'application/json'}:{}),...(method!=='GET'?{'X-Undertow-CSRF':csrf}:{})},body:body===undefined?undefined:JSON.stringify(body)});
  if(response.status===401){window.dispatchEvent(new Event('undertow-auth-expired'));throw new Error('The local GUI session expired. Open a fresh link from undertow client gui.')}
  if (!response.ok) { const text = await response.text(); try { throw new Error(JSON.parse(text).error || text) } catch(e) { if(e instanceof SyntaxError) throw new Error(text || response.statusText); throw e } }
  if (response.status===204) return undefined as T;
  return response.json();
}
export function csrfToken(){return csrf}
