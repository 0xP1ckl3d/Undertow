import {PasswordInput} from './PasswordInput';
import {useEffect,useState} from 'react';
import {KeyRound, LockKeyhole, RefreshCw, ShieldCheck, UserRound, Users} from 'lucide-react';
import {api} from './api';
import {ConfirmDialog} from './ConfirmDialog';

export type OperatorAccount={id:string;display_name:string;role:'operator'|'team_leader';disabled:boolean;revoked:boolean};
type AccountAction={account:OperatorAccount;kind:'demote'|'disable'|'revoke'|'reset'};
const passwordValid=(value:string)=>{const bytes=new TextEncoder().encode(value).length;return bytes>=12&&bytes<=72};
const errorMessage=(error:unknown)=>error instanceof Error?error.message:String(error);

export function OperatorSettings({me}:{me:OperatorAccount|null}){
  const [accounts,setAccounts]=useState<OperatorAccount[]>([]),[id,setID]=useState(''),[displayName,setDisplayName]=useState(''),[role,setRole]=useState<'operator'|'team_leader'>('operator'),[password,setPassword]=useState(''),[resetID,setResetID]=useState(''),[resetPassword,setResetPassword]=useState(''),[error,setError]=useState(''),[message,setMessage]=useState(''),[busy,setBusy]=useState(false),[loading,setLoading]=useState(true),[pending,setPending]=useState<AccountAction|null>(null);
  const refresh=async()=>{setLoading(true);try{setAccounts(await api<OperatorAccount[]>('/operators'))}finally{setLoading(false)}};
  useEffect(()=>{if(me?.role==='team_leader')void refresh().catch(e=>setError(errorMessage(e)))},[me?.id,me?.role]);
  const act=async(fn:()=>Promise<unknown>,success:string)=>{
    setBusy(true);setError('');setMessage('');
    try{await fn();setPending(null);setMessage(success);await refresh()}
    catch(e){setError(errorMessage(e))}finally{setBusy(false)}
  };
  const confirm=()=>{
    if(!pending)return;
    const {account,kind}=pending;
    if(account.id===me?.id&&kind!=='reset'){setError('Ask another Team Leader to change your role or account access.');setPending(null);return}
    void act(async()=>{
      const path='/operators/'+encodeURIComponent(account.id);
      if(kind==='revoke')await api(path,'DELETE');
      else await api(path,'PUT',kind==='demote'?{role:'operator'}:kind==='disable'?{disabled:true}:{password:resetPassword});
      if(kind==='reset'){setResetID('');setResetPassword('')}
    },kind==='reset'?'Password updated for '+account.display_name+'.':kind==='revoke'?account.display_name+' has been revoked.':kind==='disable'?account.display_name+' has been disabled.':account.display_name+' is now an Operator.');
  };
  if(!me)return <section className="panel preferences-panel identity-empty"><UserRound size={28}/><h2>Operator identity unavailable</h2><p>Connect to the server to see your authenticated account.</p></section>;
  const activeLeaders=accounts.filter(a=>a.role==='team_leader'&&!a.disabled&&!a.revoked).length;
  return <div className="identity-settings">
    <section className="panel identity-summary"><div className="identity-avatar"><ShieldCheck size={24}/></div><div><span className="identity-label">Signed in as</span><h2>{me.display_name}</h2><code>{me.id}</code></div><span className="badge">{me.role==='team_leader'?'Team Leader':'Operator'}</span></section>
    <div className="identity-safety"><LockKeyhole size={16}/><p>{me.role==='team_leader'?'Your account is protected from self-demotion, disabling, and revocation. Ask another Team Leader to make those changes.':'A Team Leader manages account roles, passwords, and access.'}</p></div>
    {error&&<p className="settings-feedback error" role="alert">{error}</p>}
    {message&&<p className="settings-feedback" role="status">{message}</p>}
    {me.role==='team_leader'&&<>
      <section className="panel operator-accounts"><div className="settings-section-heading"><div><h2><Users size={17}/>Operator accounts <span>{accounts.length}</span></h2><p>Account changes close that operator’s connected sessions. Enabled accounts can reconnect with their current password.</p></div><button type="button" disabled={busy||loading} onClick={()=>{setError('');void refresh().catch(e=>setError(errorMessage(e)))}}><RefreshCw size={14} className={loading?'is-loading':''}/>{loading?'Loading…':'Refresh'}</button></div>
      <div className="account-list" aria-busy={loading}>
        {!accounts.length&&<p className="settings-empty">{loading?'Loading operator accounts…':error?'Accounts could not be loaded. Use Refresh to try again.':'No operator accounts available.'}</p>}
        {accounts.map(account=>{
          const self=account.id===me.id;
          const protectedReason=self?'Ask another Team Leader to change your own access.':account.revoked?'Revoked accounts cannot be changed.':undefined;
          const lastLeader=account.role==='team_leader'&&!account.disabled&&!account.revoked&&activeLeaders===1;
          const accessReason=protectedReason||(lastLeader?'The final active Team Leader must retain access.':undefined);
          return <article className={'account-card'+(self?' is-self':'')} key={account.id}>
            <div className="account-details"><div className="account-name"><strong>{account.display_name}</strong>{self&&<span className="account-you">You</span>}</div><code>{account.id}</code><span className="account-role">{account.role==='team_leader'?'Team Leader':'Operator'}</span></div>
            <span className={'badge '+(account.revoked?'muted':account.disabled?'warn':'good')}>{account.revoked?'Revoked':account.disabled?'Disabled':'Active'}</span>
            <div className="account-actions">
              <button type="button" title={account.role==='team_leader'?accessReason:protectedReason} disabled={busy||account.revoked||(account.role==='team_leader'&&!!accessReason)} onClick={()=>account.role==='team_leader'?setPending({account,kind:'demote'}):void act(()=>api('/operators/'+encodeURIComponent(account.id),'PUT',{role:'team_leader'}),account.display_name+' is now a Team Leader.')}>{account.role==='team_leader'?'Demote':'Promote'}</button>
              <button type="button" title={accessReason} disabled={busy||!!accessReason} onClick={()=>account.disabled?void act(()=>api('/operators/'+encodeURIComponent(account.id),'PUT',{disabled:false}),account.display_name+' has been enabled.'):setPending({account,kind:'disable'})}>{account.disabled?'Enable':'Disable'}</button>
              <button type="button" disabled={busy||account.revoked} onClick={()=>{setResetID(account.id);setResetPassword('')}}><KeyRound size={13}/>Reset password</button>
              <button type="button" className="danger" title={accessReason} disabled={busy||!!accessReason} onClick={()=>setPending({account,kind:'revoke'})}>Revoke</button>
            </div>
            {resetID===account.id&&<form className="account-password-form" onSubmit={event=>{event.preventDefault();if(passwordValid(resetPassword)&&!busy)setPending({account,kind:'reset'})}}><label>New password for {account.display_name}<PasswordInput aria-label={'New password for '+account.display_name} autoComplete="new-password" autoFocus value={resetPassword} onChange={e=>setResetPassword(e.target.value)} aria-describedby="reset-password-help" required disabled={busy}/></label><small id="reset-password-help">Use 12–72 UTF-8 bytes. Saving closes this account’s sessions{self?', including your current session':''}.</small><div><button type="button" disabled={busy} onClick={()=>{setResetID('');setResetPassword('')}}>Cancel</button><button type="submit" className="primary" disabled={busy||!passwordValid(resetPassword)}>Save new password</button></div></form>}
          </article>;
        })}
      </div></section>
      <section className="panel account-create"><div className="settings-section-heading"><div><h2><UserRound size={17}/>Create account</h2><p>Each operator needs a unique account ID. Revoked IDs cannot be reused.</p></div></div>
        <form onSubmit={event=>{event.preventDefault();if(!busy&&id.trim()&&displayName.trim()&&passwordValid(password))void act(async()=>{await api('/operators','POST',{id:id.trim(),display_name:displayName.trim(),role,password});setID('');setDisplayName('');setPassword('');setRole('operator')},'Operator account created.')}}>
          <div className="account-create-fields"><label>Account ID<input autoComplete="off" required value={id} onChange={e=>setID(e.target.value)} disabled={busy}/></label><label>Display name<input autoComplete="off" required value={displayName} onChange={e=>setDisplayName(e.target.value)} disabled={busy}/></label><label>Role<select value={role} onChange={e=>setRole(e.target.value as 'operator'|'team_leader')} disabled={busy}><option value="operator">Operator</option><option value="team_leader">Team Leader</option></select></label><label>Password<PasswordInput   autoComplete="new-password" required value={password} onChange={e=>setPassword(e.target.value)} disabled={busy} aria-describedby="create-password-help"/></label></div>
          <div className="account-create-footer"><small id="create-password-help">Passwords must be 12–72 UTF-8 bytes. Team Leaders can manage accounts.</small><button type="submit" className="primary" disabled={busy||!id.trim()||!displayName.trim()||!passwordValid(password)}>Create account</button></div>
        </form>
      </section>
    </>}
    {pending&&<ConfirmDialog title={pending.kind==='revoke'?'Revoke account permanently?':pending.kind==='disable'?'Disable account?':pending.kind==='demote'?'Change role to Operator?':'Reset account password?'} confirmLabel={pending.kind==='revoke'?'Revoke account':pending.kind==='disable'?'Disable account':pending.kind==='demote'?'Demote account':'Save new password'} busy={busy} onClose={()=>setPending(null)} onConfirm={confirm}>
      <p><strong>{pending.account.display_name}</strong> <code>{pending.account.id}</code></p><p>{pending.kind==='revoke'?'This account will lose access permanently. Its ID cannot be reused; its audit history will be retained.':pending.kind==='disable'?'This account will lose access until a Team Leader enables it again.':pending.kind==='demote'?'This account will keep operator access and lose account-management permissions.':'This account must reconnect using the new password.'}</p><p>Connected sessions will close{pending.account.id===me.id?', including your current session':''}.</p>{error&&<p className="settings-feedback error" role="alert">{error}</p>}
    </ConfirmDialog>}
  </div>;
}
