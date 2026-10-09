import {useEffect,useId,useState,type ComponentProps} from 'react';
import {Eye,EyeOff} from 'lucide-react';

type Props=Omit<ComponentProps<'input'>,'type'> & {visibilityLabel?:string};

export function PasswordInput({visibilityLabel='password',...props}:Props){
  const [visible,setVisible]=useState(false);
  const generatedID=useId();
  const id=props.id||generatedID;
  useEffect(()=>{setVisible(false)},[visibilityLabel]);
  useEffect(()=>{if(props.value==='')setVisible(false)},[props.value]);
  const label=(visible?'Hide ':'Show ')+visibilityLabel;
  return <span className="password-input">
    <input {...props} id={id} type={visible?'text':'password'} aria-label={props['aria-label']||visibilityLabel.charAt(0).toUpperCase()+visibilityLabel.slice(1)}/>
    <button type="button" className="password-visibility" aria-label={label} title={label} aria-controls={id} aria-pressed={visible}
      disabled={props.disabled} onMouseDown={event=>event.preventDefault()} onClick={()=>setVisible(current=>!current)}>
      {visible?<EyeOff size={16}/>:<Eye size={16}/>}
    </button>
  </span>;
}
