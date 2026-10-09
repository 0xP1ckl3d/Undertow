import {useEffect, useRef, type ReactNode} from 'react';
import {AlertTriangle, X} from 'lucide-react';

export function ConfirmDialog({title, children, confirmLabel, busy, onConfirm, onClose}: {
  title:string; children:ReactNode; confirmLabel:string; busy:boolean;
  onConfirm:()=>void; onClose:()=>void;
}) {
  const dialog=useRef<HTMLDialogElement>(null);
  const cancel=useRef<HTMLButtonElement>(null);
  useEffect(()=>{
    const element=dialog.current;
    const previousFocus=document.activeElement instanceof HTMLElement?document.activeElement:null;
    element?.showModal();
    cancel.current?.focus();
    return()=>{element?.close();if(previousFocus?.isConnected)previousFocus.focus()};
  },[]);
  return <dialog ref={dialog} className="confirm-dialog" aria-labelledby="confirm-title" aria-describedby="confirm-description"
    onCancel={event=>{event.preventDefault();if(!busy)onClose()}}>
    <div className="confirm-dialog-heading"><AlertTriangle size={20}/><h2 id="confirm-title">{title}</h2>
      <button type="button" className="icon-button" aria-label="Close confirmation" disabled={busy} onClick={onClose}><X size={16}/></button>
    </div>
    <div id="confirm-description" className="confirm-dialog-body">{children}</div>
    <div className="confirm-dialog-actions"><button ref={cancel} type="button" disabled={busy} onClick={onClose}>Cancel</button>
      <button type="button" className="danger" disabled={busy} onClick={onConfirm}>{busy?'Applying…':confirmLabel}</button>
    </div>
  </dialog>;
}
