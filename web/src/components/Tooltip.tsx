import {useId,useRef,useState,type ReactNode} from 'react'
import {createPortal} from 'react-dom'

export function Tooltip({label,children}:{label:string;children:ReactNode}){
 const id=useId(),anchor=useRef<HTMLSpanElement>(null),[visible,setVisible]=useState(false),[position,setPosition]=useState({top:0,left:0})
 const show=()=>{const rect=anchor.current?.getBoundingClientRect();if(!rect)return;const halfWidth=Math.min(240,label.length*7+16,window.innerWidth-16)/2;setPosition({top:Math.min(window.innerHeight-8,rect.bottom+7),left:Math.max(halfWidth+8,Math.min(window.innerWidth-halfWidth-8,rect.left+rect.width/2))});setVisible(true)}
 return <span ref={anchor} className="tooltip-anchor" onPointerMove={event=>{if(event.pointerType==='mouse'&&!visible)show()}} onPointerLeave={()=>setVisible(false)} onFocusCapture={show} onBlurCapture={event=>{if(!event.currentTarget.contains(event.relatedTarget as Node))setVisible(false)}} onClickCapture={()=>setVisible(false)} onKeyDown={event=>{if(event.key==='Escape'){setVisible(false);event.stopPropagation()}}}>
  {children}
  {visible&&createPortal(<span id={id} role="tooltip" className="ui-tooltip" style={{top:position.top,left:position.left}}>{label}</span>,document.body)}
 </span>
}
