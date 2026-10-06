import {useEffect,useLayoutEffect,useMemo,useRef,useState} from 'react';
import {ArrowDown, Check, Circle, ClipboardList, MessageSquare, Plus, RefreshCw, Send, Users, X} from 'lucide-react';
import {api} from './api';
import type {OperatorAccount} from './OperatorSettings';
import './team.css';

type TeamMessage={id:number;sent_at:string;sender_id:string;sender_name:string;recipient_id?:string;kind:'text'|'task_created'|'task_updated';body:string;task_id?:string};
type TeamTask={id:string;title:string;description:string;creator_id:string;creator_name:string;assignee_id:string;assignee_name:string;status:'open'|'in_progress'|'done'|'cancelled';created_at:string;updated_at:string};
const pageSize=100;
const taskStatus:Record<TeamTask['status'],string>={open:'Open',in_progress:'In progress',done:'Done',cancelled:'Cancelled'};
const timeOf=(value:string)=>new Date(value).toLocaleTimeString(undefined,{hour:'2-digit',minute:'2-digit'});
const dateOf=(value:string)=>new Date(value).toLocaleDateString(undefined,{year:'numeric',month:'short',day:'numeric'});
const mergeMessages=(previous:TeamMessage[],incoming:TeamMessage[])=>{
  const byID=new Map(previous.map(item=>[item.id,item]));
  for(const item of incoming)byID.set(item.id,item);
  return [...byID.values()].sort((a,b)=>a.id-b.id);
};

export function TeamView({self,connected}:{self:OperatorAccount|null;connected:boolean}) {
  const [roster,setRoster]=useState<OperatorAccount[]>([]),[peer,setPeer]=useState(''),[messages,setMessages]=useState<TeamMessage[]>([]),[tasks,setTasks]=useState<TeamTask[]>([]);
  const [draft,setDraft]=useState(''),[error,setError]=useState(''),[loading,setLoading]=useState(true),[sending,setSending]=useState(false),[hasOlder,setHasOlder]=useState(false),[olderBusy,setOlderBusy]=useState(false);
  const [taskForm,setTaskForm]=useState(false),[assignee,setAssignee]=useState(''),[title,setTitle]=useState(''),[description,setDescription]=useState(''),[taskBusy,setTaskBusy]=useState(false),[taskFilter,setTaskFilter]=useState<'active'|'all'>('active');
  const scroll=useRef<HTMLDivElement>(null),keepPosition=useRef<number|null>(null),stickBottom=useRef(true),latest=useRef(0),conversation=useRef(peer);
  const selected=roster.find(operator=>operator.id===peer);
  const taskByID=useMemo(()=>new Map(tasks.map(task=>[task.id,task])),[tasks]);
  const visibleTasks=tasks.filter(task=>taskFilter==='all'||task.status==='open'||task.status==='in_progress');
  const activeCount=tasks.filter(task=>task.status==='open'||task.status==='in_progress').length;
  const loadRoster=()=>api<OperatorAccount[]>('/team/operators').then(setRoster);
  const loadTasks=()=>api<TeamTask[]>('/team/tasks').then(setTasks);
  useEffect(()=>{if(!connected)return;void Promise.all([loadRoster(),loadTasks()]).catch(e=>setError(String(e)))},[connected]);
  useEffect(()=>{
    conversation.current=peer;latest.current=0;setMessages([]);setHasOlder(false);setLoading(true);setError('');stickBottom.current=true;
    if(!connected){setLoading(false);return}
    let live=true;
    const path='/team/messages'+(peer?`?peer=${encodeURIComponent(peer)}`:'');
    void api<TeamMessage[]>(path).then(items=>{if(!live)return;setMessages(previous=>mergeMessages(previous,items));latest.current=Math.max(latest.current,items.at(-1)?.id||0);setHasOlder(items.length===pageSize)}).catch(e=>{if(live)setError(String(e))}).finally(()=>{if(live)setLoading(false)});
    return()=>{live=false};
  },[peer,connected]);
  useEffect(()=>{
    if(!connected)return;
    const changed=()=>{
      const current=conversation.current;
      const catchUp=async()=>{
        for(let batch=0;batch<100;batch++){
          const cursor=latest.current,query=new URLSearchParams();
          if(current)query.set('peer',current);if(cursor)query.set('after',String(cursor));
          const items=await api<TeamMessage[]>('/team/messages'+(query.size?`?${query}`:''));
          if(conversation.current!==current)return;
          if(!cursor)setHasOlder(items.length===pageSize);
          if(items.length){setMessages(previous=>mergeMessages(previous,items));latest.current=Math.max(latest.current,items.at(-1)!.id)}
          if(items.length<pageSize)return;
        }
      };
      void catchUp().catch(e=>setError(String(e)));
      void loadTasks().catch(e=>setError(String(e)));
    };
    window.addEventListener('undertow-team-changed',changed);
    return()=>window.removeEventListener('undertow-team-changed',changed);
  },[connected]);
  useLayoutEffect(()=>{
    const element=scroll.current;if(!element)return;
    if(keepPosition.current!==null){element.scrollTop=element.scrollHeight-keepPosition.current;keepPosition.current=null}
    else if(stickBottom.current)element.scrollTop=element.scrollHeight;
  },[messages]);
  const older=async()=>{
    if(!messages.length||olderBusy)return;
    setOlderBusy(true);setError('');
    try{
      const query=new URLSearchParams({before:String(messages[0].id)});if(peer)query.set('peer',peer);
      const items=await api<TeamMessage[]>(`/team/messages?${query}`);
      if(scroll.current)keepPosition.current=scroll.current.scrollHeight-scroll.current.scrollTop;
      setMessages(previous=>mergeMessages(previous,items));setHasOlder(items.length===pageSize);
    }catch(e){setError(String(e))}finally{setOlderBusy(false)}
  };
  const send=async()=>{
    const body=draft.trim();if(!body||sending||!connected)return;
    setSending(true);setError('');
    try{
      const message=await api<TeamMessage>('/team/messages','POST',{recipient_id:peer,body});
      setDraft('');stickBottom.current=true;
      setMessages(previous=>previous.some(item=>item.id===message.id)?previous:[...previous,message]);latest.current=Math.max(latest.current,message.id);
    }catch(e){setError(String(e))}finally{setSending(false)}
  };
  const createTask=async()=>{
    if(!assignee||!title.trim()||taskBusy)return;
    setTaskBusy(true);setError('');
    try{await api('/team/tasks','POST',{assignee_id:assignee,title:title.trim(),description:description.trim()});await loadTasks();setTaskForm(false);setAssignee('');setTitle('');setDescription('')}
    catch(e){setError(String(e))}finally{setTaskBusy(false)}
  };
  const updateTask=async(task:TeamTask,status:TeamTask['status'])=>{
    setTaskBusy(true);setError('');
    try{const updated=await api<TeamTask>(`/team/tasks/${task.id}`,'PUT',{status});setTasks(previous=>previous.map(item=>item.id===task.id?updated:item))}
    catch(e){setError(String(e))}finally{setTaskBusy(false)}
  };
  const canUpdate=(task:TeamTask)=>self?.id===task.assignee_id||self?.id===task.creator_id||self?.role==='team_leader';
  return <div className="team-layout">
    <aside className="team-conversations"><div className="team-side-title"><Users size={16}/><strong>Conversations</strong><button title="Refresh team data" aria-label="Refresh team data" onClick={()=>void Promise.all([loadRoster(),loadTasks()]).catch(e=>setError(String(e)))}><RefreshCw size={14}/></button></div>
      <button className={'team-conversation '+(!peer?'selected':'')} onClick={()=>setPeer('')}><span className="team-conversation-icon"><Users size={17}/></span><span><strong>Team</strong><small>Shared conversation</small></span></button>
      <div className="team-side-label">DIRECT MESSAGES</div>
      {roster.filter(operator=>operator.id!==self?.id).map(operator=><button key={operator.id} className={'team-conversation '+(peer===operator.id?'selected':'')} onClick={()=>setPeer(operator.id)}><span className="team-avatar">{operator.display_name.slice(0,1).toUpperCase()}</span><span><strong>{operator.display_name}</strong><small>{operator.id}{operator.role==='team_leader'?' · Team Leader':''}</small></span></button>)}
      {!roster.some(operator=>operator.id!==self?.id)&&<p className="team-roster-empty">Other operators appear here when accounts are created.</p>}
      <div className="team-side-foot">Messages are retained by the Undertow server.</div>
    </aside>
    <section className="team-chat" aria-label="Team conversation"><header className="team-chat-head"><div className="team-chat-mark">{peer?<MessageSquare size={18}/>:<Users size={18}/>}</div><div><h2>{selected?.display_name||'Team'}</h2><p>{peer?`Direct message · ${selected?.id||peer}`:'Visible to authenticated operators'}</p></div><span className="team-chat-count">{messages.length} shown</span></header>
      {!connected&&<div className="team-connection-note">Waiting for the Undertow server. Messages and assignments will load when the client reconnects.</div>}
      {error&&<div className="team-error" role="alert"><span>{error}</span><button onClick={()=>setError('')} aria-label="Dismiss error"><X size={14}/></button></div>}
      <div className="team-timeline" ref={scroll} onScroll={event=>{const node=event.currentTarget;stickBottom.current=node.scrollHeight-node.scrollTop-node.clientHeight<90}}>
        {hasOlder&&<button className="team-older" disabled={olderBusy} onClick={()=>void older()}>{olderBusy?'Loading…':'Load older messages'}</button>}
        {loading?<div className="team-empty">Loading conversation…</div>:messages.length===0?<div className="team-empty"><MessageSquare size={28}/><strong>No messages yet</strong><span>{peer?'Start a direct conversation with this operator.':'Post a message for the team or create an assignment.'}</span></div>:messages.map((message,index)=>{
          const previous=messages[index-1],newDay=!previous||dateOf(previous.sent_at)!==dateOf(message.sent_at),task=message.task_id?taskByID.get(message.task_id):undefined;
          return <div key={message.id}>{newDay&&<div className="team-day"><span>{dateOf(message.sent_at)}</span></div>}{message.kind==='text'?<article className={'team-message '+(message.sender_id===self?.id?'mine':'')}><div className="team-message-meta"><strong>{message.sender_name}</strong><time dateTime={message.sent_at}>{timeOf(message.sent_at)}</time></div><p>{message.body}</p></article>:<article className="team-task-event"><ClipboardList size={15}/><div><strong>{message.kind==='task_created'?'Task assigned':'Task updated'}</strong><p>{task?.title||message.body}</p><small>{message.sender_name} · {task?.assignee_name||'Assigned operator'}{task?` · ${taskStatus[task.status]}`:''} · {timeOf(message.sent_at)}</small></div></article>}</div>
        })}
      </div>
      <div className="team-composer"><label htmlFor="team-message-draft">Message {selected?.display_name||'Team'}</label><textarea id="team-message-draft" value={draft} maxLength={4096} disabled={!connected} onChange={event=>setDraft(event.target.value)} onKeyDown={event=>{if(event.key==='Enter'&&!event.shiftKey&&!event.nativeEvent.isComposing){event.preventDefault();void send()}}} placeholder={connected?'Write a message…':'Waiting for server'} rows={3}/><div><small>Enter to send · Shift+Enter for a new line</small><button disabled={!connected||sending||!draft.trim()} onClick={()=>void send()}><Send size={14}/>{sending?'Sending…':'Send'}</button></div></div>
    </section>
    <aside className="team-assignments"><div className="team-assignments-head"><div><span>ASSIGNMENTS</span><h2>Team tasks <em>{activeCount}</em></h2></div><button title="New assignment" aria-label="New assignment" disabled={!connected} onClick={()=>setTaskForm(value=>!value)}>{taskForm?<X size={17}/>:<Plus size={17}/>}</button></div>
      {taskForm&&<div className="team-task-form"><label>Assign to<select value={assignee} onChange={event=>setAssignee(event.target.value)}><option value="">Select operator</option>{roster.map(operator=><option key={operator.id} value={operator.id}>{operator.display_name} · {operator.id}</option>)}</select></label><label>Task title<input value={title} maxLength={160} onChange={event=>setTitle(event.target.value)} placeholder="What needs to be done?"/></label><label>Details <span>optional</span><textarea value={description} maxLength={4000} onChange={event=>setDescription(event.target.value)} rows={4} placeholder="Scope, context, or handoff notes"/></label><button disabled={taskBusy||!assignee||!title.trim()} onClick={()=>void createTask()}>{taskBusy?'Assigning…':'Assign task'}</button></div>}
      <div className="team-task-filters"><button className={taskFilter==='active'?'active':''} onClick={()=>setTaskFilter('active')}>Active</button><button className={taskFilter==='all'?'active':''} onClick={()=>setTaskFilter('all')}>All</button></div>
      <div className="team-task-list">{visibleTasks.length?visibleTasks.map(task=><article className="team-task-card" key={task.id}><div className="team-task-card-top"><span className={'team-task-status '+task.status}>{task.status==='done'?<Check size={11}/>:<Circle size={9}/>} {taskStatus[task.status]}</span><time dateTime={task.updated_at}>{dateOf(task.updated_at)}</time></div><h3>{task.title}</h3>{task.description&&<p>{task.description}</p>}<div className="team-task-owner"><span className="team-avatar">{task.assignee_name.slice(0,1).toUpperCase()}</span><div><strong>{task.assignee_name}</strong><small>Assigned by {task.creator_name}</small></div></div>{canUpdate(task)&&<div className="team-task-actions">{task.status==='open'&&<button disabled={taskBusy} onClick={()=>void updateTask(task,'in_progress')}>Start</button>}{task.status==='in_progress'&&<button disabled={taskBusy} onClick={()=>void updateTask(task,'done')}>Mark done</button>}{(task.status==='done'||task.status==='cancelled')&&<button disabled={taskBusy} onClick={()=>void updateTask(task,'open')}>Reopen</button>}{task.status!=='cancelled'&&task.status!=='done'&&<button disabled={taskBusy} onClick={()=>void updateTask(task,'cancelled')}>Cancel</button>}</div>}</article>):<div className="team-task-empty"><ArrowDown size={17}/>{taskFilter==='active'?'No active assignments':'No assignments yet'}</div>}</div>
    </aside>
  </div>;
}
