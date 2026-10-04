export type Agent = { id:string; hostname?:string; os?:string; arch?:string; privilege?:'high'|'low'; online?:boolean; disconnected_at?:string; session_id?:string; connected?:string; transport?:string; via?:string; depth?:number; remote?:string; public_ip?:string; last_seen?:string; rtt_ns?:number; active_jobs?:number; advertised_routes?:string[]; capabilities?:{allowed:string[];supported:string[]} };
export type Client = { id:string; hostname?:string; session_id:string; transport?:string; internal:boolean; vpn:boolean; accepted_routes?:{prefix:string;agent_id:string}[] };
export type Route = {prefix:string;agent_id:string;active:boolean};
export type Status = {server:{listeners?:{transport:string;listen:string;sessions:number;agents:number;clients:number}[];fingerprint?:string;public_host?:string};agents:Agent[];clients:Client[];routes:Route[]};
export type TopologyNode = {id:string;kind:string;label:string;agent_id?:string;client_id?:string;session_id?:string;carrier?:string;remote?:string;public_ip?:string;last_seen?:string;rtt_ns?:number;depth?:number;os?:string;arch?:string;connected?:string;disconnected_at?:string;privilege?:'high'|'low';internal?:boolean;vpn?:boolean;listeners?:{transport:string;listen:string;sessions:number}[];carriers?:{transport:string;active:boolean;listen?:string;sessions?:number}[];public_host?:string;active:boolean};
export type TopologyEdge = {id:string;kind:string;source:string;target:string;label?:string;active:boolean;client_id?:string;session_id?:string;internal?:boolean;vpn?:boolean;accepted_routes?:{prefix:string;agent_id:string;manual?:boolean}[];rtt_ns?:number;last_seen?:string};
export type Topology = {version:number;at:string;nodes:TopologyNode[];edges:TopologyEdge[]};
export type Job = {id:string;agent_id:string;kind:string;language?:string;argv?:string[];state:string;started:string;ended?:string;output_bytes:number;exit_code?:number;output?:string;output_truncated?:boolean;output_error?:string};
let csrf = '';
export async function connect() {
  const existing = await fetch('/api/client');
  if (existing.ok) { const body = await existing.json(); csrf = body.csrf || ''; return body; }
  const secret = location.hash.slice(1);
  if (!secret) throw new Error("Open the URL shown by 'undertow client gui'.");
  const result = await fetch('/api/auth', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({secret})});
  if (!result.ok) throw new Error('The GUI launch link has expired. Run undertow client gui again.');
  csrf = (await result.json()).csrf;
  history.replaceState(null, '', location.pathname);
  return await (await fetch('/api/client')).json();
}
export async function api<T>(path:string, method='GET', body?:unknown, signal?:AbortSignal):Promise<T> {
  const response = await fetch('/api'+path,{method,signal,headers:{...(body!==undefined?{'Content-Type':'application/json'}:{}),...(method!=='GET'?{'X-Undertow-CSRF':csrf}:{})},body:body===undefined?undefined:JSON.stringify(body)});
  if (!response.ok) { const text = await response.text(); try { throw new Error(JSON.parse(text).error || text) } catch(e) { if(e instanceof SyntaxError) throw new Error(text || response.statusText); throw e } }
  if (response.status===204) return undefined as T;
  return response.json();
}
export function csrfToken(){return csrf}
