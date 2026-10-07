import {useEffect,useLayoutEffect,useRef,useState} from 'react'
import {Terminal} from '@xterm/xterm'
import {FitAddon} from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import {Button} from '../components/ui/button'
import {api} from '../lib/api'
import type {Session} from '../lib/state'
import {sendTerminalInput,sendTerminalResize,sendTerminalScroll,sendTerminalRelease,terminalUrl,type TerminalMode} from '../lib/terminal'
import {Maximize2,Minimize2,Radio,ShieldCheck,ArrowDown,ArrowUp,Keyboard,Unplug} from 'lucide-react'

type Visit={startedAt:number;cache:'hit'|'miss'}
type Props={session:Session;mode:TerminalMode;takeover:boolean;active?:boolean;focusRequest?:number;visit?:Visit;onClose:()=>void}
type Phase='cached-render'|'socket-open'|'first-frame'|'first-write'|'first-render'
const timing=(phase:Phase,visit?:Visit)=>{if(!visit)return;window.dispatchEvent(new CustomEvent('herdr:terminal-timing',{detail:{phase,elapsedMs:Math.max(0,performance.now()-visit.startedAt),cache:visit.cache}}))}

export function TerminalView({session,mode,takeover,active=true,focusRequest=0,visit,onClose}:Props){
 const host=useRef<HTMLDivElement>(null),socket=useRef<WebSocket|null>(null),term=useRef<Terminal|null>(null),fitCurrent=useRef<(()=>void)|null>(null)
 const generation=useRef(0),ready=useRef(false),hasFrame=useRef(false),activeRef=useRef(active),modeRef=useRef(mode),takeoverRef=useRef(takeover),connectionPolicy=useRef({mode,takeover}),pinned=useRef(true),selectedRef=useRef(false),wheelRemainder=useRef(0),visitRef=useRef(visit)
 const [state,setState]=useState('Connecting…'),[fullscreen,setFullscreen]=useState(false),[mobileKeys,setMobileKeys]=useState(false),[selected,setSelected]=useState(false)
 activeRef.current=active;modeRef.current=mode;takeoverRef.current=takeover;visitRef.current=visit
 const selectTerminal=()=>{if(!activeRef.current)return;selectedRef.current=true;setSelected(true);term.current?.focus()}
 const deselectTerminal=()=>{selectedRef.current=false;setSelected(false);term.current?.blur();term.current?.clearSelection()}
 const scrollTerminal=(delta:number)=>{if(!activeRef.current)return;const lines=Math.max(-1000,Math.min(1000,Math.round(delta)));if(!lines)return;if(modeRef.current==='observe')term.current?.scrollLines(lines);else if(ready.current&&connectionPolicy.current.mode==='control'&&connectionPolicy.current.takeover===takeoverRef.current&&socket.current)sendTerminalScroll(socket.current,modeRef.current,lines)}
 useLayoutEffect(()=>{if(active)fitCurrent.current?.()},[active,fullscreen,mobileKeys])
 useEffect(()=>{
  if(!host.current)return
  const t=new Terminal({cursorBlink:false,disableStdin:true,convertEol:true,fontFamily:'"SFMono-Regular", Consolas, "Liberation Mono", monospace',fontSize:13,theme:{background:'#18181b',foreground:'#e4e4e7',cursor:'#fafafa',selectionBackground:'#52525b'}})
  const fit=new FitAddon();t.loadAddon(fit);t.open(host.current);term.current=t
  const fitNow=()=>{if(!activeRef.current||host.current?.closest('[hidden]'))return;try{fit.fit();if(pinned.current&&t.buffer.active.baseY>0)t.scrollToBottom()}catch{/* layout not ready */}}
  fitCurrent.current=fitNow
  if(activeRef.current&&modeRef.current==='control')selectTerminal()
  const frame=requestAnimationFrame(fitNow)
  const input=t.onData(data=>{if(activeRef.current&&ready.current&&modeRef.current==='control'&&connectionPolicy.current.mode==='control'&&connectionPolicy.current.takeover===takeoverRef.current&&socket.current)sendTerminalInput(socket.current,'control',data)})
  const resize=t.onResize(({cols,rows})=>{if(activeRef.current&&ready.current&&modeRef.current==='control'&&connectionPolicy.current.mode==='control'&&connectionPolicy.current.takeover===takeoverRef.current&&socket.current)sendTerminalResize(socket.current,'control',cols,rows)})
  const scrolling=t.onScroll(()=>{pinned.current=t.buffer.active.viewportY>=t.buffer.active.baseY})
  const observer=new ResizeObserver(fitNow);observer.observe(host.current)
  const terminalHost=host.current,panel=terminalHost.closest('.terminal-panel')
  let touchGesture:{startX:number;startY:number;lastY:number;startedAt:number;claimed:boolean;rejected:boolean}|null=null
  const accumulateScroll=(amount:number)=>{const total=wheelRemainder.current+amount;if(!Number.isFinite(total)){wheelRemainder.current=0;return}const whole=Math.trunc(total),lines=Math.max(-1000,Math.min(1000,whole));wheelRemainder.current=Math.abs(whole)>1000?0:total-whole;if(lines)scrollTerminal(lines)}
  const onWheel=(event:WheelEvent)=>{if(!activeRef.current)return;if(event.ctrlKey||event.metaKey){event.stopPropagation();return}event.preventDefault();event.stopPropagation();selectTerminal();const lineHeight=16;accumulateScroll(event.deltaMode===1?event.deltaY:event.deltaMode===2?event.deltaY*Math.max(1,terminalHost.clientHeight/lineHeight):event.deltaY/lineHeight)}
  const onTouchStart=(event:TouchEvent)=>{if(!activeRef.current||event.touches.length!==1){touchGesture=null;return}const point=event.touches[0];touchGesture={startX:point.clientX,startY:point.clientY,lastY:point.clientY,startedAt:Date.now(),claimed:false,rejected:false}}
  const onTouchMove=(event:TouchEvent)=>{const gesture=touchGesture;if(!activeRef.current||!gesture||gesture.rejected)return;if(event.touches.length!==1){touchGesture=null;return}const point=event.touches[0],dx=point.clientX-gesture.startX,dy=point.clientY-gesture.startY;if(!gesture.claimed){if(Math.max(Math.abs(dx),Math.abs(dy))<8)return;if(Math.abs(dx)>Math.abs(dy)*1.2||Date.now()-gesture.startedAt>=500){gesture.rejected=true;return}gesture.claimed=true}const movement=gesture.lastY-point.clientY;gesture.lastY=point.clientY;if(!movement)return;event.preventDefault();event.stopPropagation();selectTerminal();accumulateScroll(movement/16)}
  const onTouchEnd=()=>{touchGesture=null}
  const onOutsidePointerDown=(event:PointerEvent)=>{if(selectedRef.current&&panel&&!panel.contains(event.target as Node))deselectTerminal()}
  terminalHost.addEventListener('wheel',onWheel,{capture:true,passive:false});terminalHost.addEventListener('touchstart',onTouchStart,{capture:true,passive:true});terminalHost.addEventListener('touchmove',onTouchMove,{capture:true,passive:false});terminalHost.addEventListener('touchend',onTouchEnd,{capture:true,passive:true});terminalHost.addEventListener('touchcancel',onTouchEnd,{capture:true,passive:true});document.addEventListener('pointerdown',onOutsidePointerDown,true)
  return()=>{generation.current++;ready.current=false;cancelAnimationFrame(frame);fitCurrent.current=null;observer.disconnect();terminalHost.removeEventListener('wheel',onWheel,true);terminalHost.removeEventListener('touchstart',onTouchStart,true);terminalHost.removeEventListener('touchmove',onTouchMove,true);terminalHost.removeEventListener('touchend',onTouchEnd,true);terminalHost.removeEventListener('touchcancel',onTouchEnd,true);document.removeEventListener('pointerdown',onOutsidePointerDown,true);input.dispose();resize.dispose();scrolling.dispose();t.dispose();term.current=null}
 },[])
 useEffect(()=>{
  const t=term.current;if(!active||!t){if(t){t.options.disableStdin=true;t.options.cursorBlink=false}deselectTerminal();return}
  const current=++generation.current,epoch=api.authSnapshot();let failed=false,closed=false,first=true;let cachedListener:{dispose():void}|undefined;const renderListeners:Array<{dispose():void}>=[];connectionPolicy.current={mode,takeover}
  ready.current=false;t.options.disableStdin=true;t.options.cursorBlink=false;setState(hasFrame.current?'Refreshing…':'Connecting…')
  // The retained xterm keeps its FIFO parser queue. HERDR's first frame is a full ANSI 2J/H redraw.
  const ws=new WebSocket(terminalUrl(session.id,mode,takeover,t.cols,t.rows));socket.current=ws;ws.binaryType='arraybuffer'
  const valid=()=>!closed&&generation.current===current&&activeRef.current&&modeRef.current===mode&&takeoverRef.current===takeover&&socket.current===ws&&api.authSnapshot()===epoch
  ws.onopen=()=>{if(!valid())return;ws.send(JSON.stringify({type:'hello',csrf_token:api.csrf}));timing('socket-open',visitRef.current)}
  ws.onmessage=event=>{if(!valid())return;if(typeof event.data==='string'){try{const message=JSON.parse(event.data) as {type?:string;code?:string;error?:string;message?:string};if(message.type==='error'){if(message.code==='auth_expired_or_revoked'){ready.current=false;t.options.disableStdin=true;t.options.cursorBlink=false;api.expireStream(epoch);return}failed=true;ready.current=false;t.options.disableStdin=true;t.options.cursorBlink=false;setState(message.message||message.error||'Terminal error')}}catch{failed=true;ready.current=false;setState('Invalid terminal message')}return}
   if(!(event.data instanceof ArrayBuffer)||failed)return
   const firstWrite=first;if(first){first=false;cachedListener?.dispose();timing('first-frame',visitRef.current)}
   // Generation is checked before enqueue; writes already accepted finish in xterm FIFO order.
   t.write(new Uint8Array(event.data),()=>{if(!valid()||failed||closed)return;if(firstWrite){hasFrame.current=true;timing('first-write',visitRef.current);const disposable=t.onRender(()=>{disposable.dispose();requestAnimationFrame(()=>{if(!valid()||failed||closed)return;timing('first-render',visitRef.current);ready.current=true;t.options.disableStdin=mode!=='control';t.options.cursorBlink=mode==='control';if(mode==='control')sendTerminalResize(ws,mode,t.cols,t.rows);setState(mode==='control'?'Control active':'Observing')})});renderListeners.push(disposable);t.refresh(0,t.rows-1)}if(pinned.current)t.scrollToBottom()})
  }
  ws.onerror=()=>{if(!valid()||failed)return;failed=true;ready.current=false;t.options.disableStdin=true;t.options.cursorBlink=false;setState('Terminal connection interrupted')}
  ws.onclose=()=>{if(valid()&&!failed){ready.current=false;t.options.disableStdin=true;t.options.cursorBlink=false;setState('Terminal disconnected. The agent is still running.')}closed=true}
  if(hasFrame.current&&typeof t.onRender==='function'){const listener=t.onRender(()=>{listener.dispose();if(!first)return;requestAnimationFrame(()=>{if(valid()&&first)timing('cached-render',visitRef.current)})});cachedListener=listener;renderListeners.push(listener);t.refresh(0,t.rows-1)}
  return()=>{generation.current++;for(const listener of renderListeners)listener.dispose();ready.current=false;t.options.disableStdin=true;t.options.cursorBlink=false;socket.current=null;ws.onopen=null;ws.onmessage=null;ws.onerror=null;ws.onclose=null;if(ws.readyState===WebSocket.OPEN)sendTerminalRelease(ws);ws.close();deselectTerminal()}
 },[active,session.id,mode,takeover])
 useEffect(()=>{if(active&&mode==='control'&&focusRequest)selectTerminal()},[active,mode,takeover,focusRequest])
 const key=(data:string)=>{if(active&&ready.current&&mode==='control'&&connectionPolicy.current.mode==='control'&&connectionPolicy.current.takeover===takeover&&socket.current)sendTerminalInput(socket.current,mode,data);selectTerminal()}
 const scroll=(delta:number)=>{selectTerminal();scrollTerminal(delta)}
 return <section className={'terminal-panel '+(fullscreen?'terminal-panel--fullscreen ':'')+(selected?'terminal-panel--selected':'')} aria-label={'Terminal for '+session.name}>
  <div className="terminal-toolbar"><div className="terminal-title"><span className="terminal-led" aria-hidden="true"/><strong>{session.name||session.agent}</strong><span className="terminal-mode">{mode==='control'?<ShieldCheck size={14}/>:<Radio size={14}/>} {mode==='control'?'Control':'Observe'}</span></div><div className="terminal-actions"><span className="terminal-state" role="status">{state}</span><Button size="icon" variant="ghost" disabled={!active} onClick={()=>setMobileKeys(x=>!x)} aria-label="Extra keys" aria-pressed={mobileKeys}><Keyboard size={17}/></Button><Button size="icon" variant="ghost" disabled={!active} onClick={()=>setFullscreen(x=>!x)} aria-label={fullscreen?'Exit full screen':'Full screen'} aria-pressed={fullscreen}>{fullscreen?<Minimize2 size={17}/>:<Maximize2 size={17}/>}</Button><Button size="icon" variant="ghost" disabled={!active} onClick={onClose} aria-label="Disconnect terminal"><Unplug size={17}/></Button></div></div>
  <div ref={host} className="terminal-screen" onClick={selectTerminal}/>
  <div className={'terminal-keyboard '+(mobileKeys?'terminal-keyboard--show':'')} aria-label="Terminal keys"><Button variant="secondary" size="sm" disabled={!active||mode==='observe'||!ready.current} onClick={()=>key('\x1b')}>Esc</Button><Button variant="secondary" size="sm" disabled={!active||mode==='observe'||!ready.current} onClick={()=>key('\t')}>Tab</Button><Button variant="secondary" size="sm" disabled={!active||mode==='observe'||!ready.current} onClick={()=>key('\x03')}>Ctrl+C</Button><Button variant="secondary" size="sm" disabled={!active||mode==='observe'||!ready.current} onClick={()=>key('\x04')}>Ctrl+D</Button><Button variant="secondary" size="sm" disabled={!active} onClick={()=>scroll(-8)} aria-label="Scroll up"><ArrowUp size={16}/></Button><Button variant="secondary" size="sm" disabled={!active} onClick={()=>scroll(8)} aria-label="Scroll down"><ArrowDown size={16}/></Button></div>
 </section>
}
