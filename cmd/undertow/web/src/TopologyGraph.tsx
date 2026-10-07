import {useEffect, useMemo, useRef, useState} from 'react';
import {Background, Controls, MarkerType, Position, ReactFlow, useNodesState, type Edge, type Node, type ReactFlowInstance} from '@xyflow/react';
import {Cable, Globe2, Network, Radio} from 'lucide-react';
import {SiApple, SiDebian, SiLinux, SiUbuntu} from 'react-icons/si';
import {FaWindows} from 'react-icons/fa6';
import {api, type Topology, type TopologyEdge, type TopologyNode} from './api';

type LocalClient = {session_id:string;vpn?:boolean;internal?:boolean};
type PositionRecord = {id:string;x:number;y:number};

function DeviceIcon({node}:{node:TopologyNode}) {
  if(node.kind==='server')return <span className="undertow-symbol">U</span>;
  if(node.kind==='client')return <span className="undertow-symbol small">U</span>;
  if(node.kind==='relay')return <Cable size={29}/>;
  if(node.kind==='network')return <Globe2 size={29}/>;
  const os=(node.os||'').toLowerCase();
	if(os.includes('windows'))return <FaWindows size={29}/>;
  if(os.includes('ubuntu'))return <SiUbuntu size={29}/>;
  if(os.includes('debian'))return <SiDebian size={29}/>;
  if(os.includes('linux'))return <SiLinux size={29}/>;
  if(os.includes('darwin')||os.includes('mac'))return <SiApple size={29}/>;
  return <Network size={28}/>;
}

function sessionUptime(value?:string) {
  if(!value)return 'Unknown';
  const seconds=Math.max(0,Math.floor((Date.now()-Date.parse(value))/1000));
  if(!Number.isFinite(seconds))return 'Unknown';
  if(seconds<60)return `${seconds}s`;
  if(seconds<3600)return `${Math.floor(seconds/60)}m`;
  if(seconds<86400)return `${Math.floor(seconds/3600)}h ${Math.floor(seconds%3600/60)}m`;
  return `${Math.floor(seconds/86400)}d ${Math.floor(seconds%86400/3600)}h`;
}

function GraphDetails({node,edge,localClient,topology,onMouseEnter,onMouseLeave}:{node?:TopologyNode;edge?:TopologyEdge;localClient?:LocalClient|null;topology:Topology;onMouseEnter:()=>void;onMouseLeave:()=>void}) {
  if(!node&&!edge)return null;
  const rows: [string,string][]=[];
  if(node){
    if(node.kind==='server'){
      rows.push(['Role','Undertow server']);
      rows.push(['Public host',node.public_host||'Not configured on server']);
      for(const carrier of node.carriers||[])rows.push([carrier.transport.toUpperCase(),carrier.active?`${carrier.listen} · ${carrier.sessions} sessions`:'Inactive']);
      if(!node.carriers?.length)rows.push(['Listeners','None reported']);
    }else{
      rows.push(['Type',node.kind==='client'?'Operator client':node.kind==='agent'?'Agent':node.kind==='relay'?'Relay listener':'Accepted network']);
      if(node.hostname&&node.hostname!==node.label)rows.push(['Hostname',node.hostname]);
      if(node.os)rows.push(['Platform',`${node.os}${node.arch?' / '+node.arch:''}`]);
      if(node.kind==='agent')rows.push(['Privilege',node.privilege==='high'?'Elevated (observed)':node.privilege==='low'?'Standard (observed)':'Unknown · run Privileges to classify']);
      if(node.kind==='agent'){
        if(node.first_seen&&!node.first_seen.startsWith('0001-'))rows.push(['First seen',`${node.first_seen_estimated?'At least since ':''}${new Date(node.first_seen).toLocaleString()}`]);
        rows.push(['State',node.active?'Connected':node.connection_state==='sleeping'?'Sleeping by policy':`${node.archived?'Archived · ':''}Disconnected${node.disconnected_at?' · '+new Date(node.disconnected_at).toLocaleString():''}`]);
        rows.push(['Mode',node.connection_mode==='checkin'?'Check-in':'Continuous']);
        if(node.sleep?.interval_seconds)rows.push(['Sleep cadence',`${node.sleep.interval_seconds}s ±${node.sleep.jitter_percent}%`]);
        if(node.connection_reason)rows.push(['Why',node.connection_reason]);
        if(node.connection_state==='sleeping'&&node.expected_checkin&&!node.expected_checkin.startsWith('0001-'))rows.push(['Expected check-in',new Date(node.expected_checkin).toLocaleString()]);
        if(node.connection_state==='sleeping'&&node.sleep_lost_after&&!node.sleep_lost_after.startsWith('0001-'))rows.push(['Mark lost after',new Date(node.sleep_lost_after).toLocaleString()]);
      }
	  if(node.kind==='agent'&&node.via)rows.push(['Via agent',node.via]);
      if(node.kind==='relay')rows.push(['State',node.active?'Listening on parent agent':'Inactive · last known listener']);
      if(node.kind==='client')rows.push(['Internal path',node.internal?'Enabled':'Disabled']);
      if(node.kind==='client')rows.push(['Internet VPN',node.vpn?'Enabled':'Disabled']);
      if(node.kind==='client')rows.push(['Accepted routes',String(node.accepted_count||0)]);
      if(node.connected&&node.active)rows.push(['Session uptime',sessionUptime(node.connected)]);
      if(node.carrier)rows.push(['Carrier',node.carrier]);
      if(node.kind==='agent'&&node.relay_bind)rows.push(['Relay listener',node.relay_bind]);
      if(node.remote)rows.push(['Remote',node.remote]);
      if(node.kind==='agent')rows.push(['Observed public IP',node.public_ip||'Not observed from this connection']);
      if(node.session_id)rows.push([node.kind==='agent'&&!node.active?'Last session':'Session',String(node.session_id)]);
      if(node.rtt_ns)rows.push(['RTT',`${Math.round(node.rtt_ns/1e6)} ms`]);
      if(node.last_seen)rows.push(['Last seen',new Date(node.last_seen).toLocaleString()]);
    }
  }else if(edge){
    rows.push(['Connection',edge.kind.replaceAll('_',' ')]);
    if(edge.deployment_id)rows.push(['Deployment',edge.deployment_id]);
    if(edge.label)rows.push(['Carrier / path',edge.label]);
    if(edge.kind==='carrier'&&edge.client_id){
      if(localClient?.session_id&&String(localClient.session_id)===String(edge.session_id))rows.push(['Operator client','This client']);
      rows.push(['Internal path',edge.internal?'Enabled':'Disabled']);
      rows.push(['Internet VPN',edge.vpn?'Enabled':'Disabled']);
      if(edge.accepted_routes?.length){
        for(const route of edge.accepted_routes)rows.push(['Accepted route',`${route.prefix} via ${route.agent_id.slice(0,12)}${route.manual?' · manual':''}`]);
      }else rows.push(['Accepted routes','None']);
    }
    if(edge.accepted_by?.length)rows.push(['Accepted by',edge.accepted_by.map(id=>topology.nodes.find(node=>node.id===id)?.label||id).join(', ')]);
    if(edge.session_id)rows.push(['Session',String(edge.session_id)]);
    if(edge.rtt_ns)rows.push(['RTT',`${Math.round(edge.rtt_ns/1e6)} ms`]);
    if(edge.last_seen)rows.push(['Last seen',new Date(edge.last_seen).toLocaleString()]);
  }
  return <div className="graph-inspector" onMouseEnter={onMouseEnter} onMouseLeave={onMouseLeave}><strong>{node?.label||'Connection details'}</strong><dl>{rows.map(([label,value],i)=><div key={label+i}><dt>{label}</dt><dd>{value}</dd></div>)}</dl></div>;
}

function AgentCheckInClock({node,serverAt,receivedAt}:{node:TopologyNode;serverAt:string;receivedAt:number}) {
  const [now,setNow]=useState(()=>performance.now());
  const show=node.kind==='agent'&&node.connection_mode==='checkin'&&node.connection_state!=='disconnected'&&!!node.last_seen;
  useEffect(()=>{
    if(!show)return;
    setNow(performance.now());
    const timer=window.setInterval(()=>setNow(performance.now()),1000);
    return()=>window.clearInterval(timer);
  },[show,node.last_seen,receivedAt]);
  if(!show)return null;
  const seconds=Math.max(0,Math.floor((Date.parse(serverAt)-Date.parse(node.last_seen!)+Math.max(0,now-receivedAt))/1000));
  if(!Number.isFinite(seconds))return null;
  const label=seconds<60?`${seconds}s`:seconds<3600?`${Math.floor(seconds/60)}m`:seconds<86400?`${Math.floor(seconds/3600)}h ${Math.floor(seconds%3600/60)}m`:`${Math.floor(seconds/86400)}d`;
  return <span className="graph-device-checkin" title={`Last seen ${new Date(node.last_seen!).toLocaleString()} · ${label} ago`}>{label}</span>;
}

function lostAt(node:TopologyNode){
  if(node.kind!=='agent'||node.connection_state!=='disconnected'||!node.disconnected_at||node.disconnected_at.startsWith('0001-'))return '';
  const value=new Date(node.disconnected_at);
  return Number.isFinite(value.getTime())?value.toLocaleString(undefined,{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'}):'';
}

type GraphActions = {
  renameAgent:(id:string)=>void;
  archiveAgent:(id:string,archived:boolean)=>void;
  killSession:(id:string)=>void;
  shutdownAgent:(id:string)=>void;
  openRoutes:()=>void;
  removeRoute:(prefix:string,agentID:string)=>void;
  openRelays:()=>void;
  stopRelay:(agentID:string,bind:string)=>void;
  openSettings:()=>void;
  openDeployment:(id:string)=>void;
};
type GraphMenu = {x:number;y:number;nodeID?:string;edgeID?:string};

export function TopologyGraph({topology,onAgent,localClient,actions}:{topology:Topology|null;onAgent:(id:string)=>void;localClient?:LocalClient|null;actions:GraphActions}) {
  const [nodes,setNodes,onNodesChange]=useNodesState<Node>([]);
  const [flow,setFlow]=useState<ReactFlowInstance<Node,Edge>|null>(null);
  const [layout,setLayout]=useState<Record<string,{x:number;y:number}>>({});
  const [hoverNode,setHoverNode]=useState<string|null>(null);
  const [hoverEdge,setHoverEdge]=useState<string|null>(null);
  const [pinnedNode,setPinnedNode]=useState<string|null>(null);
  const [pinnedEdge,setPinnedEdge]=useState<string|null>(null);
  const [menu,setMenu]=useState<GraphMenu|null>(null);
  const [confirm,setConfirm]=useState<{title:string;detail:string;run:()=>void}|null>(null);
  const hoverTimer=useRef<number|undefined>(undefined);
  const clearHoverSoon=()=>{window.clearTimeout(hoverTimer.current);hoverTimer.current=window.setTimeout(()=>{setHoverNode(null);setHoverEdge(null)},850)};
  const holdInspector=()=>window.clearTimeout(hoverTimer.current);
  useEffect(()=>()=>window.clearTimeout(hoverTimer.current),[]);
  useEffect(()=>{const dismiss=(event:KeyboardEvent)=>{if(event.key==='Escape'){setMenu(null);setConfirm(null);setPinnedNode(null);setPinnedEdge(null)}};window.addEventListener('keydown',dismiss);return()=>window.removeEventListener('keydown',dismiss)},[]);
  useEffect(()=>{api<PositionRecord[]>('/layout').then(items=>setLayout(Object.fromEntries(items.map(p=>[p.id,{x:p.x,y:p.y}])))).catch(()=>{})},[]);
  useEffect(()=>{
    if(!topology)return;
    const receivedAt=performance.now();
    const count:Record<number,number>={};
    setNodes(topology.nodes.map(n=>{
      const column=n.kind==='client'?0:n.kind==='server'?1:n.kind==='agent'?2+2*(n.depth||0):n.kind==='relay'?3+2*(n.depth||0):6;
      const row=count[column]||0;count[column]=row+1;
      return {id:n.id,position:layout[n.id]||{x:column*190,y:row*130+80},data:{label:<div className={`graph-device ${n.kind} ${n.kind==='agent'?(n.privilege||'unknown'):''} ${n.kind==='agent'&&n.connection_state==='sleeping'?'sleeping':n.active?'':'offline'} ${n.archived?'archived':''}`} title={n.label}><div className="graph-device-square"><DeviceIcon node={n}/><AgentCheckInClock node={n} serverAt={topology.at} receivedAt={receivedAt}/><span className={'device-state '+(n.kind==='agent'&&n.connection_state==='sleeping'?'sleeping':n.active?'online':'')}/></div><span className="graph-device-name">{n.label}</span><span className="graph-device-subtitle">{n.kind==='agent'&&n.archived?'Archived':n.kind==='agent'&&!n.active&&n.connection_state!=='sleeping'?'Disconnected':n.kind==='agent'?n.os||'Agent':n.kind==='client'?`Operator${n.accepted_count?` · ${n.accepted_count} routes`:''}`:n.kind==='server'?'Server':n.kind==='relay'?(n.active?'Relay · listening':'Relay · inactive'):'Network'}</span>{lostAt(n)&&<span className="graph-device-lost-at">Lost {lostAt(n)}</span>}</div>},style:{padding:0,border:0,background:'transparent',width:110},sourcePosition:Position.Right,targetPosition:Position.Left,draggable:true};
    }));
  },[topology,layout,setNodes]);
  const edges=useMemo<Edge[]>(()=>topology?.edges.map(e=>{const accepted=!!e.accepted_by?.length;const color=e.kind==='deployment'?'#92bdcf':e.kind==='forward'?'#cd85dd':accepted?'#4ecb94':e.kind==='relay_path'?'#e7aa4d':'#bc8c40';return {id:e.id,source:e.source,target:e.target,label:e.kind==='accepted_route'?undefined:e.label,type:'smoothstep',animated:e.kind==='carrier'&&e.active,style:{stroke:color,strokeWidth:accepted?2.8:e.kind==='carrier'?2.2:1.8,opacity:e.active?1:.45,strokeDasharray:e.kind==='deployment'||e.kind==='forward'||!e.active?'5 4':undefined},labelStyle:{fill:'#c9d0c8',fontSize:10,fontWeight:600},labelBgStyle:{fill:'#111714',fillOpacity:.96,stroke:'#485248',strokeWidth:1},labelBgPadding:[8,5],markerEnd:{type:MarkerType.ArrowClosed,color}}})||[],[topology]);
  const selectedNode=topology?.nodes.find(n=>n.id===(pinnedNode||hoverNode));
  const selectedEdge=topology?.edges.find(e=>e.id===(pinnedEdge||hoverEdge));
  const menuNode=topology?.nodes.find(n=>n.id===menu?.nodeID);
  const menuEdge=topology?.edges.find(e=>e.id===menu?.edgeID);
  const menuItems:{label:string;run:()=>void;confirm?:string}[]=[];
  if(menuNode?.kind==='agent'&&menuNode.agent_id){
    const id=menuNode.agent_id;
    const available=menuNode.active||menuNode.connection_state==='sleeping';
    menuItems.push({label:available?'Open workspace':'View retained workspace',run:()=>onAgent(id)});
    menuItems.push({label:'Rename agent',run:()=>actions.renameAgent(id)});
    if(!available)menuItems.push({label:menuNode.archived?'Restore to agent list':'Archive agent',run:()=>actions.archiveAgent(id,!menuNode.archived)});
    if(available){
      menuItems.push({label:'Kill current session',run:()=>actions.killSession(id),confirm:'The current session will disconnect. If the agent is sleeping, this runs at its next check-in. The agent may reconnect.'});
      menuItems.push({label:'Shut down agent',run:()=>actions.shutdownAgent(id),confirm:'This sends the agent shutdown operation. If the agent is sleeping, it runs at its next check-in.'});
    }
  }else if(menuNode?.kind==='network'){
    menuItems.push({label:'View routes',run:actions.openRoutes});
    const local=topology?.edges.find(e=>e.kind==='accepted_route'&&e.target===menuNode.id&&localClient?.session_id&&String(e.session_id)===String(localClient.session_id));
    if(local)menuItems.push({label:'Remove from this client',run:()=>actions.removeRoute(menuNode.label,local.source.replace(/^agent:/,'')),confirm:`Remove ${menuNode.label} from this client's saved and installed routes?`});
  }else if(menuNode?.kind==='relay'){
    menuItems.push({label:'View relays',run:actions.openRelays});
    if(menuNode.active&&menuNode.agent_id)menuItems.push({label:'Stop listener',run:()=>actions.stopRelay(menuNode.agent_id!,menuNode.label),confirm:`Stop ${menuNode.label}? Child sessions using it may disconnect.`});
  }else if(menuNode?.kind==='client'){
    menuItems.push({label:'View routes',run:actions.openRoutes});
  }else if(menuNode?.kind==='server'){
    menuItems.push({label:'Open settings',run:actions.openSettings});
  }else if(menuEdge){
    if(menuEdge.kind==='deployment'&&menuEdge.deployment_id)menuItems.push({label:'Open deployment',run:()=>actions.openDeployment(menuEdge.deployment_id!)});
    else if(menuEdge.kind==='accepted_route'){
      menuItems.push({label:'View routes',run:actions.openRoutes});
      if(localClient?.session_id&&String(menuEdge.session_id)===String(localClient.session_id)){
        const prefix=topology?.nodes.find(n=>n.id===menuEdge.target)?.label;
        if(prefix)menuItems.push({label:'Remove from this client',run:()=>actions.removeRoute(prefix,menuEdge.source.replace(/^agent:/,'')),confirm:`Remove ${prefix} from this client's saved and installed routes?`});
      }
    }else if(menuEdge.kind==='relay_path'){
      const child=topology?.nodes.find(n=>n.id===menuEdge.target);
      if(child?.agent_id)menuItems.push({label:'Open child workspace',run:()=>onAgent(child.agent_id!)});
    }else if(menuEdge.kind==='relay_listener')menuItems.push({label:'View relays',run:actions.openRelays});
    else if(menuEdge.kind==='carrier')menuItems.push({label:'View routes',run:actions.openRoutes});
  }
  const showMenu=(event:React.MouseEvent,selection:{nodeID?:string;edgeID?:string})=>{
    event.preventDefault();
    const frame=(event.currentTarget as HTMLElement).closest('.topology-frame');
    if(!frame)return;
    const rect=frame.getBoundingClientRect();
    setMenu({x:Math.max(8,Math.min(event.clientX-rect.left,rect.width-235)),y:Math.max(32,Math.min(event.clientY-rect.top,rect.height-235)),...selection});
  };
  if(!topology)return <div className="graph-loading">Loading topology…</div>;
  return <><ReactFlow nodes={nodes} edges={edges} onInit={setFlow} onNodesChange={onNodesChange} onNodeDragStop={(_,node)=>{setLayout(current=>({...current,[node.id]:node.position}));api('/layout','PUT',{id:node.id,x:node.position.x,y:node.position.y}).catch(()=>{})}} onNodeClick={(_,node)=>{setPinnedNode(node.id);setPinnedEdge(null);setMenu(null)}} onEdgeClick={(_,edge)=>{setPinnedEdge(edge.id);setPinnedNode(null);setMenu(null)}} onNodeDoubleClick={(_,node)=>{const agent=topology.nodes.find(n=>n.id===node.id);if(agent?.kind==='agent'&&agent.agent_id)onAgent(agent.agent_id)}} onNodeContextMenu={(event,node)=>showMenu(event,{nodeID:node.id})} onEdgeContextMenu={(event,edge)=>showMenu(event,{edgeID:edge.id})} onPaneClick={()=>{setMenu(null);setHoverNode(null);setHoverEdge(null);setPinnedNode(null);setPinnedEdge(null)}} onNodeMouseEnter={(_,node)=>{holdInspector();setHoverNode(node.id);setHoverEdge(null)}} onNodeMouseLeave={clearHoverSoon} onEdgeMouseEnter={(_,edge)=>{holdInspector();setHoverEdge(edge.id);setHoverNode(null)}} onEdgeMouseLeave={clearHoverSoon} fitView fitViewOptions={{padding:.2}} nodesConnectable={false} edgesFocusable={false} minZoom={.3} maxZoom={2.2} proOptions={{hideAttribution:true}}><Background color="#252a30" gap={22} size={1}/><Controls showInteractive={false}/></ReactFlow><button className="graph-fit" onClick={()=>void flow?.fitView({padding:.22,duration:350})}>Fit all</button><GraphDetails node={selectedNode} edge={selectedEdge} localClient={localClient} topology={topology} onMouseEnter={holdInspector} onMouseLeave={clearHoverSoon}/>{menu&&menuItems.length>0&&<div className="graph-context-menu" role="menu" style={{left:menu.x,top:menu.y}}>{menuItems.map(item=><button role="menuitem" key={item.label} onClick={()=>{setMenu(null);if(item.confirm)setConfirm({title:item.label,detail:item.confirm,run:item.run});else item.run()}}>{item.label}</button>)}</div>}{confirm&&<div className="graph-confirm" role="alertdialog" aria-label={confirm.title}><strong>{confirm.title}?</strong><p>{confirm.detail}</p><div><button onClick={()=>{confirm.run();setConfirm(null)}}>Confirm</button><button onClick={()=>setConfirm(null)}>Cancel</button></div></div>}<div className="graph-help"><Radio size={12}/> Drag nodes · Click to pin details · Double click agent · Right click for actions</div></>;
}
