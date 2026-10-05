import {useEffect, useMemo, useState} from 'react';
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

function GraphDetails({node,edge,localClient}:{node?:TopologyNode;edge?:TopologyEdge;localClient?:LocalClient|null}) {
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
      if(node.os)rows.push(['Platform',`${node.os}${node.arch?' / '+node.arch:''}`]);
      if(node.kind==='agent')rows.push(['Privilege',node.privilege==='high'?'Elevated (observed)':node.privilege==='low'?'Standard (observed)':'Unknown · run Privileges to classify']);
      if(node.kind==='agent')rows.push(['State',node.active?'Connected':`Disconnected${node.disconnected_at?' · '+new Date(node.disconnected_at).toLocaleString():''}`]);
      if(node.kind==='client')rows.push(['Internal path',node.internal?'Enabled':'Disabled']);
      if(node.kind==='client')rows.push(['Internet VPN',node.vpn?'Enabled':'Disabled']);
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
    if(edge.label)rows.push(['Carrier / path',edge.label]);
    if(edge.kind==='carrier'&&edge.client_id){
      if(localClient?.session_id===edge.session_id)rows.push(['Operator client','This client']);
      rows.push(['Internal path',edge.internal?'Enabled':'Disabled']);
      rows.push(['Internet VPN',edge.vpn?'Enabled':'Disabled']);
      if(edge.accepted_routes?.length){
        for(const route of edge.accepted_routes)rows.push(['Accepted route',`${route.prefix} via ${route.agent_id.slice(0,12)}${route.manual?' · manual':''}`]);
      }else rows.push(['Accepted routes','None']);
    }
    if(edge.kind==='accepted_route'&&edge.client_id)rows.push(['Accepted by',edge.client_id]);
    if(edge.session_id)rows.push(['Session',String(edge.session_id)]);
    if(edge.rtt_ns)rows.push(['RTT',`${Math.round(edge.rtt_ns/1e6)} ms`]);
    if(edge.last_seen)rows.push(['Last seen',new Date(edge.last_seen).toLocaleString()]);
  }
  return <div className="graph-inspector"><strong>{node?.label||'Connection details'}</strong><dl>{rows.map(([label,value],i)=><div key={label+i}><dt>{label}</dt><dd>{value}</dd></div>)}</dl></div>;
}

export function TopologyGraph({topology,onAgent,localClient}:{topology:Topology|null;onAgent:(id:string)=>void;localClient?:LocalClient|null}) {
  const [nodes,setNodes,onNodesChange]=useNodesState<Node>([]);
  const [flow,setFlow]=useState<ReactFlowInstance<Node,Edge>|null>(null);
  const [layout,setLayout]=useState<Record<string,{x:number;y:number}>>({});
  const [hoverNode,setHoverNode]=useState<string|null>(null);
  const [hoverEdge,setHoverEdge]=useState<string|null>(null);
  useEffect(()=>{api<PositionRecord[]>('/layout').then(items=>setLayout(Object.fromEntries(items.map(p=>[p.id,{x:p.x,y:p.y}])))).catch(()=>{})},[]);
  useEffect(()=>{
    if(!topology)return;
    const count:Record<number,number>={};
    setNodes(topology.nodes.map(n=>{
      const column=n.kind==='client'?0:n.kind==='server'?1:n.kind==='agent'?2+2*(n.depth||0):n.kind==='relay'?3+2*(n.depth||0):6;
      const row=count[column]||0;count[column]=row+1;
      return {id:n.id,position:layout[n.id]||{x:column*190,y:row*130+80},data:{label:<div className={`graph-device ${n.kind} ${n.kind==='agent'?(n.privilege||'unknown'):''} ${n.active?'':'offline'}`} title={n.label}><div className="graph-device-square"><DeviceIcon node={n}/><span className={'device-state '+(n.active?'online':'')}/></div><span className="graph-device-name">{n.label}</span><span className="graph-device-subtitle">{n.kind==='agent'&&!n.active?'Disconnected':n.kind==='agent'?n.os||'Agent':n.kind==='client'?'Operator':n.kind==='server'?'Server':n.kind==='relay'?'Relay':'Network'}</span></div>},style:{padding:0,border:0,background:'transparent',width:110},sourcePosition:Position.Right,targetPosition:Position.Left,draggable:true};
    }));
  },[topology,layout,setNodes]);
  const edges=useMemo<Edge[]>(()=>topology?.edges.map(e=>({id:e.id,source:e.source,target:e.target,label:e.kind==='accepted_route'?undefined:e.label,type:'smoothstep',animated:e.kind==='carrier'&&e.active,style:{stroke:e.kind==='accepted_route'?'#4ecb94':e.kind==='forward'?'#cd85dd':e.kind==='relay_path'?'#e7aa4d':'#bc8c40',strokeWidth:e.kind==='carrier'?2.2:1.8,opacity:e.active?1:.45,strokeDasharray:e.kind==='forward'||!e.active?'5 4':undefined},labelStyle:{fill:'#c9d0c8',fontSize:10,fontWeight:600},labelBgStyle:{fill:'#111714',fillOpacity:.96,stroke:'#485248',strokeWidth:1},labelBgPadding:[8,5],markerEnd:{type:MarkerType.ArrowClosed,color:'#8c968b'}}))||[],[topology]);
  const selectedNode=topology?.nodes.find(n=>n.id===hoverNode);
  const selectedEdge=topology?.edges.find(e=>e.id===hoverEdge);
  if(!topology)return <div className="graph-loading">Loading topology…</div>;
  return <><ReactFlow nodes={nodes} edges={edges} onInit={setFlow} onNodesChange={onNodesChange} onNodeDragStop={(_,node)=>{setLayout(current=>({...current,[node.id]:node.position}));api('/layout','PUT',{id:node.id,x:node.position.x,y:node.position.y}).catch(()=>{})}} onNodeDoubleClick={(_,node)=>{const agent=topology.nodes.find(n=>n.id===node.id);if(agent?.kind==='agent'&&agent.agent_id)onAgent(agent.agent_id)}} onNodeMouseEnter={(_,node)=>{setHoverNode(node.id);setHoverEdge(null)}} onNodeMouseLeave={()=>setHoverNode(null)} onEdgeMouseEnter={(_,edge)=>{setHoverEdge(edge.id);setHoverNode(null)}} onEdgeMouseLeave={()=>setHoverEdge(null)} fitView fitViewOptions={{padding:.2}} nodesConnectable={false} edgesFocusable={false} minZoom={.3} maxZoom={2.2} proOptions={{hideAttribution:true}}><Background color="#252a30" gap={22} size={1}/><Controls showInteractive={false}/></ReactFlow><button className="graph-fit" onClick={()=>void flow?.fitView({padding:.22,duration:350})}>Fit all</button><GraphDetails node={selectedNode} edge={selectedEdge} localClient={localClient}/><div className="graph-help"><Radio size={12}/> Drag nodes to arrange · Double click agent to open</div></>;
}
