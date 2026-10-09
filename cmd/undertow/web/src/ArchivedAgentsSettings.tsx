import {useEffect, useMemo, useState} from 'react';
import {ArchiveRestore, Search, Trash2} from 'lucide-react';
import {api, type Agent} from './api';
import './archived-agents.css';

type Props = {
  agents: Agent[];
  canDelete: boolean;
  onRefresh: () => Promise<void>;
  showArchived: boolean;
  onArchivedVisibility: (show: boolean) => Promise<void>;
};

function when(value?: string) {
  if (!value || value.startsWith('0001-')) return 'Unknown';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? 'Unknown' : date.toLocaleString();
}

export function ArchivedAgentsSettings({agents, canDelete, onRefresh, showArchived, onArchivedVisibility}: Props) {
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [confirmation, setConfirmation] = useState<string[] | null>(null);
  const [confirmText, setConfirmText] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const archived = useMemo(() => agents.filter(agent => agent.archived), [agents]);
  const visible = useMemo(() => archived.filter(agent => `${agent.nickname || ''} ${agent.hostname || ''} ${agent.id} ${agent.transport || ''}`.toLowerCase().includes(query.toLowerCase().trim())), [archived, query]);
  const selectedIDs = archived.filter(agent => selected.has(agent.id)).map(agent => agent.id);
  const allVisibleSelected = visible.length > 0 && visible.every(agent => selected.has(agent.id));

  useEffect(() => {
    if (!confirmation) return;
    const escape = (event: KeyboardEvent) => {if (event.key === 'Escape' && !busy) {setConfirmation(null); setConfirmText('')}};
    window.addEventListener('keydown', escape);
    return () => window.removeEventListener('keydown', escape);
  }, [confirmation, busy]);

  const run = async (action: 'restore' | 'delete', ids: string[]) => {
    if (busy || ids.length === 0) return;
    setBusy(true);
    setError('');
    setMessage('');
    try {
      await api('/agents/archive/bulk', 'POST', {action, ids});
      await onRefresh();
      setSelected(previous => {
        const next = new Set(previous);
        ids.forEach(id => next.delete(id));
        return next;
      });
      setConfirmation(null);
      setConfirmText('');
      setMessage(`${ids.length} agent ${ids.length === 1 ? 'record' : 'records'} ${action === 'restore' ? 'restored' : 'permanently deleted'}.`);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      await onRefresh().catch(() => {});
    } finally {
      setBusy(false);
    }
  };

  const toggle = (id: string) => setSelected(previous => {
    const next = new Set(previous);
    if (next.has(id)) next.delete(id); else next.add(id);
    return next;
  });

  return <section className="panel preferences-panel archived-management">
    <div className="archived-heading"><div><h2>Archived agents</h2><p>Archived agents stay out of Agents and Topology until restored or reconnected. Archiving removes routes owned by that agent.</p></div><strong>{archived.length} records</strong></div>
    <label className="archive-visibility"><input type="checkbox" checked={showArchived} onChange={event => void onArchivedVisibility(event.target.checked)}/><span>Show archived agents in Agents and Topology</span></label>
    <div className="archived-toolbar">
      <label className="archived-search"><Search size={15}/><input aria-label="Search archived agents" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search name, carrier, or ID"/></label>
      <div className="archived-bulk-actions"><span>{selectedIDs.length} selected</span><button disabled={busy || selectedIDs.length === 0} onClick={() => void run('restore', selectedIDs)}><ArchiveRestore size={14}/> Restore selected</button><button className="danger" disabled={busy || !canDelete || selectedIDs.length === 0} title={canDelete ? undefined : 'Team Leader required'} onClick={() => {setConfirmation(selectedIDs); setConfirmText('')}}><Trash2 size={14}/> Delete selected</button></div>
    </div>
    {visible.length > 0 ? <div className="archived-list" role="group" aria-label="Archived agent records">
      <div className="archived-list-header"><label><input type="checkbox" aria-label="Select all visible archived agents" checked={allVisibleSelected} onChange={() => setSelected(previous => {const next = new Set(previous); visible.forEach(agent => allVisibleSelected ? next.delete(agent.id) : next.add(agent.id)); return next})}/><span>Select visible</span></label><span>Agent</span><span>Last seen</span><span>Actions</span></div>
      {visible.map(agent => <div className="archived-list-row" key={agent.id}>
        <input type="checkbox" aria-label={`Select ${agent.nickname || agent.hostname || agent.id}`} checked={selected.has(agent.id)} onChange={() => toggle(agent.id)}/>
        <div className="archived-identity"><strong>{agent.nickname || agent.hostname || agent.id}</strong><span>{agent.nickname && agent.hostname ? `${agent.hostname} · ` : ''}{agent.os || 'Unknown OS'} · {agent.transport || 'Unknown carrier'}</span><code>{agent.id}</code></div>
        <time>{when(agent.last_seen && !agent.last_seen.startsWith('0001-') ? agent.last_seen : agent.disconnected_at)}</time>
        <div className="archived-row-actions"><button disabled={busy} onClick={() => void run('restore', [agent.id])}>Restore</button><button className="danger" disabled={busy || !canDelete} title={canDelete ? undefined : 'Team Leader required'} onClick={() => {setConfirmation([agent.id]); setConfirmText('')}}>Delete</button></div>
      </div>)}
    </div> : <p className="archived-empty">{archived.length ? 'No archived agents match this search.' : 'No archived agents.'}</p>}
    {!canDelete && archived.length > 0 && <p className="archived-note">Permanent deletion requires a Team Leader account.</p>}
    {message && <p className="archived-success" role="status">{message}</p>}
    {error && !confirmation && <p className="archived-error" role="alert">{error}</p>}
    {confirmation && <div className="archived-confirm-backdrop" onMouseDown={() => {if (!busy) {setConfirmation(null); setConfirmText('')}}}><div className="archived-confirm" role="alertdialog" aria-modal="true" aria-labelledby="archived-delete-title" onMouseDown={event => event.stopPropagation()}>
      <h3 id="archived-delete-title">Permanently delete {confirmation.length} archived {confirmation.length === 1 ? 'agent' : 'agents'}?</h3>
      <p>This removes their server snapshots, jobs and output, screenshots, transfers, Jump records, and history, plus console entries stored on this client. It cannot be undone. A running payload may reconnect and create a new record.</p>
      {error && <p className="archived-error" role="alert">{error}</p>}
      <label>Type DELETE to confirm<input autoFocus value={confirmText} onChange={event => setConfirmText(event.target.value)} aria-label="Type DELETE to confirm"/></label>
      <div><button className="danger" disabled={busy || confirmText !== 'DELETE'} onClick={() => void run('delete', confirmation)}>Delete permanently</button><button disabled={busy} onClick={() => {setConfirmation(null); setConfirmText('')}}>Cancel</button></div>
    </div></div>}
  </section>;
}
