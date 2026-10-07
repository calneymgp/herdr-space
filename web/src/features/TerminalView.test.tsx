import {act,cleanup,fireEvent,render,screen} from '@testing-library/react'
import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest'
import {TerminalView} from './TerminalView'
import type {Session} from '../lib/state'
import {api} from '../lib/api'
const fake=vi.hoisted(()=>({resize:undefined as undefined|((size:{cols:number;rows:number})=>void),data:undefined as undefined|((data:string)=>void),opens:0,disposes:0,writes:[] as Uint8Array[],autoRender:true,renderers:new Set<()=>void>(),onScroll:undefined as undefined|((line:number)=>void),scroll:vi.fn(),scrollBottom:vi.fn(),focus:vi.fn(),blur:vi.fn(),clearSelection:vi.fn(),fit:vi.fn(),viewportY:0,baseY:0,pending:[] as Array<()=>void>}))
vi.mock('@xterm/xterm',()=>({Terminal:class {
 cols=80;rows=24;options={disableStdin:true,cursorBlink:false}
 buffer={active:{get viewportY(){return fake.viewportY},get baseY(){return fake.baseY}}}
 loadAddon(){}open(){fake.opens++}write(data:Uint8Array,callback?:()=>void){fake.writes.push(data);fake.pending.push(()=>{fake.baseY+=20;callback?.()})}focus(){fake.focus()}blur(){fake.blur()}clearSelection(){fake.clearSelection()}dispose(){fake.disposes++}
 onData(handler:(data:string)=>void){fake.data=handler;return {dispose(){}}}
 onResize(handler:(size:{cols:number;rows:number})=>void){fake.resize=handler;return {dispose(){}}}
 onScroll(handler:(line:number)=>void){fake.onScroll=handler;return {dispose(){}}}
 scrollLines(delta:number){fake.scroll(delta)}
 scrollToBottom(){fake.viewportY=fake.baseY;fake.scrollBottom();fake.onScroll?.(fake.viewportY)}
 onRender(handler:()=>void){fake.renderers.add(handler);return {dispose(){fake.renderers.delete(handler)}}}
 refresh(){if(fake.autoRender)for(const callback of [...fake.renderers])callback()}
}}))
vi.mock('@xterm/addon-fit',()=>({FitAddon:class{fit(){fake.fit()}}}))
class FakeSocket {
 static instances:FakeSocket[]=[];static OPEN=1
 readyState=1;binaryType='';closed=false;onopen:(()=>void)|null=null;onclose:(()=>void)|null=null;onerror:(()=>void)|null=null;onmessage:((event:MessageEvent)=>void)|null=null
 sent:string[]=[]
 constructor(public url:string){FakeSocket.instances.push(this)}
 send(data:string){this.sent.push(data);const type=JSON.parse(data).type;if(this.url.includes('mode=observe')&&(type==='resize'||type==='scroll'))this.onclose?.()}
 close(){this.closed=true;this.readyState=3}
}
const session:Session={id:'s1',name:'Agent',source:'herdr',agent:'codex',launcher:'codex',project_id:'',cwd:'',server_id:'',terminal_id:'',workspace_id:'',pane_id:'',pid:1,start_time:'',membership:'managed',activity:'active',alive:true,updated_at:'',capabilities:['observe']}
afterEach(()=>{cleanup();vi.unstubAllGlobals();FakeSocket.instances=[];fake.resize=undefined;fake.onScroll=undefined;fake.pending=[];fake.writes=[];fake.opens=0;fake.disposes=0;fake.data=undefined;fake.autoRender=true;fake.renderers.clear();fake.baseY=0;fake.viewportY=0;fake.scroll.mockClear();fake.scrollBottom.mockClear();fake.focus.mockClear();fake.blur.mockClear();fake.clearSelection.mockClear();fake.fit.mockClear()})
const wheel=(host:HTMLElement,deltaY:number,options:WheelEventInit={})=>{
 const event=new WheelEvent('wheel',{deltaY,bubbles:true,cancelable:true,...options})
 let dispatched=true
 act(()=>{dispatched=host.dispatchEvent(event)})
 return {event,dispatched}
}
const touch=(host:HTMLElement,type:string,points:Array<{clientX:number;clientY:number}>)=>{
 const event=new Event(type,{bubbles:true,cancelable:true})
 Object.defineProperty(event,'touches',{value:points})
 let dispatched=true
 act(()=>{dispatched=host.dispatchEvent(event)})
 return {event,dispatched}
}
const fresh=(socket:FakeSocket)=>act(()=>{socket.onmessage?.({data:new Uint8Array([65]).buffer} as MessageEvent);fake.pending.shift()?.()})
beforeEach(()=>vi.stubGlobal('requestAnimationFrame',(callback:FrameRequestCallback)=>{callback(0);return 1}))
describe('observe terminal',()=>{
 it('keeps the public stream error visible when the socket closes',()=>{
  vi.stubGlobal('WebSocket',FakeSocket)
  vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
  render(<TerminalView session={session} mode="observe" takeover={false} onClose={()=>{}}/>)
  const socket=FakeSocket.instances[0]
  act(()=>socket.onopen?.())
  act(()=>socket.onmessage?.({data:JSON.stringify({type:'error',code:'terminal_slow_consumer',message:'The terminal is receiving data too quickly.'})} as MessageEvent))
  act(()=>{socket.onerror?.();socket.onclose?.()})
  expect(screen.getByRole('status').textContent).toBe('The terminal is receiving data too quickly.')
 })
 it('stays connected after viewport resize and scrolls locally',()=>{
  vi.stubGlobal('WebSocket',FakeSocket)
  vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
  render(<TerminalView session={session} mode="observe" takeover={false} onClose={()=>{}}/>)
  const socket=FakeSocket.instances[0]
  act(()=>socket.onopen?.())
  act(()=>fake.resize?.({cols:100,rows:30}))
  act(()=>{socket.onmessage?.({data:new Uint8Array([65]).buffer} as MessageEvent);fake.pending.shift()?.()})
  fireEvent.click(screen.getByRole('button',{name:'Scroll up'}))
  expect(screen.getByRole('status').textContent).toBe('Observing')
  expect(socket.sent.map(x=>JSON.parse(x).type)).toEqual(['hello'])
  expect(fake.scroll).toHaveBeenCalledWith(-8)
})
it('selects on wheel, consumes page scrolling, and scrolls the observe buffer locally',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 render(<TerminalView session={session} mode="observe" takeover={false} onClose={()=>{}}/>)
 const socket=FakeSocket.instances[0]
 act(()=>socket.onopen?.())
 fake.focus.mockClear()
 const host=document.querySelector('.terminal-screen') as HTMLElement
 const {event,dispatched}=wheel(host,32)
 expect(dispatched).toBe(false)
 expect(event.defaultPrevented).toBe(true)
 expect(fake.scroll).toHaveBeenCalledWith(2)
 expect(fake.focus).toHaveBeenCalledTimes(1)
 expect(screen.getByLabelText('Terminal for Agent').classList.contains('terminal-panel--selected')).toBe(true)
 expect(socket.sent.map(data=>JSON.parse(data).type)).toEqual(['hello'])
 fake.scroll.mockClear()
 wheel(host,8)
 expect(fake.scroll).not.toHaveBeenCalled()
 wheel(host,8)
 expect(fake.scroll).toHaveBeenCalledWith(1)
})
it('routes control wheel through HERDR scroll without sending input or reconnecting',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 render(<TerminalView session={session} mode="control" takeover={false} onClose={()=>{}}/>)
 const socket=FakeSocket.instances[0]
 act(()=>socket.onopen?.())
 fresh(socket)
 fake.focus.mockClear()
 const host=document.querySelector('.terminal-screen') as HTMLElement
 const {event,dispatched}=wheel(host,64)
 expect(dispatched).toBe(false)
 expect(event.defaultPrevented).toBe(true)
 expect(fake.focus).toHaveBeenCalledTimes(1)
 const messages=socket.sent.map(data=>JSON.parse(data))
 expect(messages[0].type).toBe('hello')
 expect(messages.slice(1)).toEqual([
  {type:'resize',cols:80,rows:24},
  {type:'scroll',delta:4},
 ])
 wheel(host,32000)
 expect(JSON.parse(socket.sent.at(-1)!).delta).toBe(1000)
 expect(socket.sent.map(data=>JSON.parse(data).type)).not.toContain('input')
 expect(FakeSocket.instances).toHaveLength(1)
})
it('keeps key helper buttons inside the terminal focus boundary',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 render(<TerminalView session={session} mode="control" takeover={false} onClose={()=>{}}/>)
 const socket=FakeSocket.instances[0]
 act(()=>socket.onopen?.())
 fresh(socket)
 fake.focus.mockClear()
 fireEvent.pointerDown(document.body)
 fireEvent.click(screen.getByRole('button',{name:'Extra keys'}))
 fireEvent.click(screen.getByRole('button',{name:'Esc'}))
 expect(socket.sent.map(data=>JSON.parse(data).type)).toEqual(['hello','resize','input'])
 expect(JSON.parse(socket.sent[2]).data).toBe('\u001b')
 expect(fake.focus).toHaveBeenCalledTimes(1)
 expect(screen.getByLabelText('Terminal for Agent').classList.contains('terminal-panel--selected')).toBe(true)
})
it('routes vertical touch scrolling to observe history and leaves text-selection gestures native',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 render(<TerminalView session={session} mode="observe" takeover={false} onClose={()=>{}}/>)
 const socket=FakeSocket.instances[0],host=document.querySelector('.terminal-screen') as HTMLElement
 act(()=>socket.onopen?.())
 touch(host,'touchstart',[{clientX:100,clientY:100}])
 const firstMove=touch(host,'touchmove',[{clientX:100,clientY:80}])
 touch(host,'touchmove',[{clientX:100,clientY:64}])
 expect(firstMove.dispatched).toBe(false)
 expect(firstMove.event.defaultPrevented).toBe(true)
 expect(fake.scroll.mock.calls).toEqual([[1],[1]])
 touch(host,'touchstart',[{clientX:100,clientY:64}])
 touch(host,'touchmove',[{clientX:100,clientY:96}])
 expect(fake.scroll.mock.calls.at(-1)?.[0]).toBeLessThan(0)
 expect(screen.getByLabelText('Terminal for Agent').classList.contains('terminal-panel--selected')).toBe(true)
 expect(socket.sent.map(data=>JSON.parse(data).type)).toEqual(['hello'])

 fake.scroll.mockClear()
 touch(host,'touchstart',[{clientX:100,clientY:100}])
 const horizontal=touch(host,'touchmove',[{clientX:140,clientY:95}])
 expect(horizontal.event.defaultPrevented).toBe(false)
 expect(fake.scroll).not.toHaveBeenCalled()

 const now=vi.spyOn(Date,'now')
 now.mockReturnValueOnce(1000).mockReturnValueOnce(1500)
 touch(host,'touchstart',[{clientX:100,clientY:100}])
 const longPressDrag=touch(host,'touchmove',[{clientX:100,clientY:60}])
 expect(longPressDrag.event.defaultPrevented).toBe(false)
 expect(fake.scroll).not.toHaveBeenCalled()
 now.mockRestore()
})
it('routes vertical control touch scroll through HERDR without turning it into terminal input',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 render(<TerminalView session={session} mode="control" takeover={false} onClose={()=>{}}/>)
 const socket=FakeSocket.instances[0],host=document.querySelector('.terminal-screen') as HTMLElement
 act(()=>socket.onopen?.())
 fresh(socket)
 socket.sent.length=0
 touch(host,'touchstart',[{clientX:100,clientY:100}])
 touch(host,'touchmove',[{clientX:100,clientY:60}])
 touch(host,'touchstart',[{clientX:100,clientY:60}])
 touch(host,'touchmove',[{clientX:100,clientY:100}])
 expect(socket.sent.map(data=>JSON.parse(data))).toEqual([{type:'scroll',delta:2},{type:'scroll',delta:-2}])
 expect(socket.closed).toBe(false)
})
it('leaves browser zoom on modified wheel and clears terminal focus outside without disconnecting',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 render(<TerminalView session={session} mode="control" takeover={false} onClose={()=>{}}/>)
 const socket=FakeSocket.instances[0]
 const panel=screen.getByLabelText('Terminal for Agent')
 act(()=>socket.onopen?.())
 const host=document.querySelector('.terminal-screen') as HTMLElement
 const modified=wheel(host,60,{ctrlKey:true})
 expect(modified.event.defaultPrevented).toBe(false)
 expect(fake.scroll).not.toHaveBeenCalled()
 fireEvent.pointerDown(document.body)
 expect(fake.blur).toHaveBeenCalledTimes(1)
 expect(fake.clearSelection).toHaveBeenCalledTimes(1)
 expect(panel.classList.contains('terminal-panel--selected')).toBe(false)
 expect(socket.closed).toBe(false)
})
it('does not reclaim control focus on socket open after an outside click',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 render(<TerminalView session={session} mode="control" takeover={false} onClose={()=>{}}/>)
 const socket=FakeSocket.instances[0]
 fake.focus.mockClear()
 fireEvent.pointerDown(document.body)
 act(()=>socket.onopen?.())
 expect(fake.focus).not.toHaveBeenCalled()
 expect(socket.closed).toBe(false)
})
it('keeps the last prompt visible after async frames without interrupting history reading',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 render(<TerminalView session={session} mode="observe" takeover={false} onClose={()=>{}}/>)
 const socket=FakeSocket.instances[0]
 act(()=>socket.onopen?.())
 expect(screen.getByRole('status').textContent).not.toBe('Observing')
 act(()=>{socket.onmessage?.({data:new Uint8Array([65]).buffer} as MessageEvent);fake.pending.shift()?.()})
 expect(screen.getByRole('status').textContent).toBe('Observing')
 expect(fake.scrollBottom).toHaveBeenCalledTimes(1)
 fake.viewportY=5;act(()=>fake.onScroll?.(5))
 act(()=>{socket.onmessage?.({data:new Uint8Array([66]).buffer} as MessageEvent);fake.pending.shift()?.()})
 expect(fake.scrollBottom).toHaveBeenCalledTimes(1)
 fireEvent.click(screen.getByRole('button',{name:'Full screen'}))
 expect(fake.scrollBottom).toHaveBeenCalledTimes(1)
})
it('refits when keyboard and fullscreen change layout and preserves server error on close',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 render(<TerminalView session={session} mode="control" takeover={false} onClose={()=>{}}/>)
 const socket=FakeSocket.instances[0]
 act(()=>socket.onopen?.())
 expect(fake.focus).toHaveBeenCalled()
 const fitBefore=fake.fit.mock.calls.length
 fireEvent.click(screen.getByRole('button',{name:'Extra keys'}))
 fireEvent.click(screen.getByRole('button',{name:'Full screen'}))
 expect(fake.fit.mock.calls.length).toBeGreaterThan(fitBefore)
 act(()=>{socket.onmessage?.({data:JSON.stringify({type:'error',error:'Terminal ocupado'})} as MessageEvent);socket.onclose?.()})
 expect(screen.getByRole('status').textContent).toBe('Terminal ocupado')
})
it('restores control focus on tab request without reopening the socket',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 const {rerender}=render(<TerminalView session={session} mode="control" takeover={false} focusRequest={0} onClose={()=>{}}/>)
 const socket=FakeSocket.instances[0]
 act(()=>socket.onopen?.())
 fireEvent.pointerDown(document.body)
 expect(screen.getByLabelText('Terminal for Agent').classList.contains('terminal-panel--selected')).toBe(false)
 fake.focus.mockClear()
 rerender(<TerminalView session={session} mode="control" takeover={false} focusRequest={1} onClose={()=>{}}/>)
 expect(fake.focus).toHaveBeenCalledTimes(1)
 expect(screen.getByLabelText('Terminal for Agent').classList.contains('terminal-panel--selected')).toBe(true)
 expect(FakeSocket.instances).toHaveLength(1)
})
})

it('retains one xterm across deactivation, rejects stale frames, and waits for a fresh write before input',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 const view=(active:boolean,mode:'observe'|'control'='control')=><TerminalView session={{...session,capabilities:['observe','control']}} mode={mode} takeover={false} active={active} onClose={()=>{}}/>
 const {rerender,unmount}=render(view(true))
 const old=FakeSocket.instances[0]
 act(()=>old.onopen?.())
 const oldMessage=old.onmessage
 act(()=>{oldMessage?.({data:new Uint8Array([27,91,50,74,27,91,72,65]).buffer} as MessageEvent);fake.pending.shift()?.()})
 expect(screen.getByRole('status').textContent).toBe('Control active')
 rerender(view(false))
 expect(old.closed).toBe(true)
 expect(old.sent.map(data=>JSON.parse(data).type)).toContain('release')
 expect(fake.disposes).toBe(0)
 expect(fake.opens).toBe(1)
 const writes=fake.writes.length
 act(()=>oldMessage?.({data:new Uint8Array([66]).buffer} as MessageEvent))
 expect(fake.writes).toHaveLength(writes)
 act(()=>fake.data?.('x'))
 expect(old.sent.map(data=>JSON.parse(data).type)).not.toContain('input')
 rerender(view(true))
 const current=FakeSocket.instances[1]
 expect(fake.opens).toBe(1)
 expect(screen.getByRole('status').textContent).toBe('Refreshing…')
 act(()=>current.onopen?.())
 expect(screen.getByRole('status').textContent).not.toBe('Control active')
 act(()=>fake.data?.('x'))
 expect(current.sent.map(data=>JSON.parse(data).type)).not.toContain('input')
 act(()=>{current.onmessage?.({data:new Uint8Array([27,91,50,74,27,91,72,66]).buffer} as MessageEvent);fake.pending.shift()?.()})
 expect(screen.getByRole('status').textContent).toBe('Control active')
 act(()=>fake.data?.('x'))
 expect(current.sent.map(data=>JSON.parse(data).type)).toContain('input')
 unmount()
 expect(fake.disposes).toBe(1)
})
it('expires auth only from the current stream generation',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 api.setAuth('synthetic','tester')
 const unauthorized=vi.fn(),unsubscribe=api.onUnauthorized(unauthorized)
 const props={session,mode:'observe' as const,takeover:false,onClose:()=>{}}
 const {rerender}=render(<TerminalView {...props} active/>)
 const old=FakeSocket.instances[0],oldMessage=old.onmessage
 rerender(<TerminalView {...props} active={false}/>)
 rerender(<TerminalView {...props} active/>)
 act(()=>oldMessage?.({data:JSON.stringify({type:'error',code:'auth_expired_or_revoked'})} as MessageEvent))
 expect(api.authenticated).toBe(true)
 const current=FakeSocket.instances[1]
 act(()=>current.onmessage?.({data:JSON.stringify({type:'error',code:'auth_expired_or_revoked'})} as MessageEvent))
 expect(api.authenticated).toBe(false)
 expect(unauthorized).toHaveBeenCalledTimes(1)
 unsubscribe();api.clearAuth()
})
it('keeps control locked until render confirmation and never unlocks an errored frame',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 fake.autoRender=false
 const {rerender}=render(<TerminalView session={session} mode="control" takeover={false} onClose={()=>{}}/>)
 const socket=FakeSocket.instances[0]
 act(()=>socket.onopen?.())
 act(()=>{socket.onmessage?.({data:new Uint8Array([27,91,50,74,27,91,72,65]).buffer} as MessageEvent);fake.pending.shift()?.()})
 expect(screen.getByRole('status').textContent).toBe('Connecting…')
 act(()=>fake.data?.('x'))
 expect(socket.sent.map(data=>JSON.parse(data).type)).toEqual(['hello'])
 act(()=>{for(const callback of [...fake.renderers])callback()})
 expect(screen.getByRole('status').textContent).toBe('Control active')
 act(()=>fake.data?.('x'))
 expect(socket.sent.map(data=>JSON.parse(data).type)).toContain('input')
 rerender(<TerminalView session={session} mode="control" takeover onClose={()=>{}}/>)
 const next=FakeSocket.instances[1]
 act(()=>{next.onopen?.();next.onmessage?.({data:new Uint8Array([27,91,50,74,27,91,72,66]).buffer} as MessageEvent);fake.pending.shift()?.();next.onmessage?.({data:JSON.stringify({type:'error',message:'Terminal ocupado'})} as MessageEvent)})
 act(()=>{for(const callback of [...fake.renderers])callback()})
 expect(screen.getByRole('status').textContent).toBe('Terminal ocupado')
 act(()=>fake.data?.('x'))
 expect(next.sent.map(data=>JSON.parse(data).type)).not.toContain('input')
})
it('emits only numeric phase timings and distinguishes cached render from fresh render',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 const details:unknown[]=[]
 const listener=(event:Event)=>details.push((event as CustomEvent).detail)
 window.addEventListener('herdr:terminal-timing',listener)
 const visit={startedAt:performance.now(),cache:'miss' as const}
 const props={session,mode:'observe' as const,takeover:false,onClose:()=>{}}
 const {rerender}=render(<TerminalView {...props} active visit={visit}/>)
 const socket=FakeSocket.instances[0]
 act(()=>{socket.onopen?.();socket.onmessage?.({data:new Uint8Array([27,91,50,74,27,91,72,65]).buffer} as MessageEvent);fake.pending.shift()?.()})
 expect(details).toEqual(expect.arrayContaining(['socket-open','first-frame','first-write','first-render'].map(phase=>expect.objectContaining({phase,cache:'miss',elapsedMs:expect.any(Number)}))))
 rerender(<TerminalView {...props} active={false} visit={visit}/>)
 rerender(<TerminalView {...props} active visit={{startedAt:performance.now(),cache:'hit'}}/>)
 expect(details).toEqual(expect.arrayContaining([expect.objectContaining({phase:'cached-render',cache:'hit',elapsedMs:expect.any(Number)})]))
 for(const detail of details)expect(Object.keys(detail as object).sort()).toEqual(['cache','elapsedMs','phase'])
 window.removeEventListener('herdr:terminal-timing',listener)
})
it('reconnects for mode and takeover changes without remounting the buffer or accepting old frames',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 const {rerender}=render(<TerminalView session={session} mode="observe" takeover={false} onClose={()=>{}}/>)
 const observe=FakeSocket.instances[0]
 act(()=>{observe.onopen?.();observe.onmessage?.({data:new Uint8Array([27,91,50,74,27,91,72,65]).buffer} as MessageEvent);fake.pending.shift()?.()})
 const oldMessage=observe.onmessage
 rerender(<TerminalView session={session} mode="control" takeover={false} onClose={()=>{}}/>)
 expect(observe.closed).toBe(true)
 expect(fake.opens).toBe(1)
 const control=FakeSocket.instances[1]
 expect(control.url).toContain('takeover=false')
 const before=fake.writes.length
 act(()=>oldMessage?.({data:new Uint8Array([66]).buffer} as MessageEvent))
 expect(fake.writes).toHaveLength(before)
 rerender(<TerminalView session={session} mode="control" takeover onClose={()=>{}}/>)
 expect(control.closed).toBe(true)
 expect(FakeSocket.instances[2].url).toContain('takeover=true')
 expect(fake.opens).toBe(1)
})
it.each([
 ['observe to control',false],
 ['control takeover',true],
] as const)('keeps keyboard focus after %s reconnect cleanup',(_,takeover)=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 const view=(mode:'observe'|'control',isTakeover=false,focusRequest=0)=><TerminalView session={session} mode={mode} takeover={isTakeover} focusRequest={focusRequest} onClose={()=>{}}/>
 const {rerender}=render(view(takeover?'control':'observe'))
 const previous=FakeSocket.instances[0]
 act(()=>{previous.onopen?.();fresh(previous)})
 fake.focus.mockClear();fake.blur.mockClear()
 rerender(view('control',takeover,1))
 const current=FakeSocket.instances[1]
 expect(previous.closed).toBe(true)
 expect(fake.opens).toBe(1)
 expect(fake.blur).toHaveBeenCalledTimes(1)
 expect(fake.focus).toHaveBeenCalledTimes(1)
 expect(fake.focus.mock.invocationCallOrder[0]).toBeGreaterThan(fake.blur.mock.invocationCallOrder[0])
 act(()=>fake.data?.('x'))
 expect(current.sent.map(data=>JSON.parse(data).type)).not.toContain('input')
 act(()=>{current.onopen?.();fresh(current)})
 act(()=>fake.data?.('x'))
 expect(current.sent.map(data=>JSON.parse(data).type)).toContain('input')
 expect(fake.opens).toBe(1)
})
it('removes pending render callbacks when a connection is parked',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 fake.autoRender=false
 const {rerender}=render(<TerminalView session={session} mode="observe" takeover={false} onClose={()=>{}}/>)
 const socket=FakeSocket.instances[0]
 act(()=>{socket.onopen?.();socket.onmessage?.({data:new Uint8Array([65]).buffer} as MessageEvent);fake.pending.shift()?.()})
 expect(fake.renderers.size).toBe(1)
 rerender(<TerminalView session={session} mode="observe" takeover={false} active={false} onClose={()=>{}}/>)
 expect(fake.renderers.size).toBe(0)
})
it('omits cached-render if a fresh frame wins before onRender or before its animation frame',()=>{
 vi.stubGlobal('WebSocket',FakeSocket)
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}})
 const phases:string[]=[]
 const listener=(event:Event)=>phases.push((event as CustomEvent<{phase:string}>).detail.phase)
 window.addEventListener('herdr:terminal-timing',listener)
 const visit=(cache:'hit'|'miss')=>({startedAt:performance.now(),cache})
 const props={session,mode:'observe' as const,takeover:false,onClose:()=>{}}
 const {rerender}=render(<TerminalView {...props} active visit={visit('miss')}/>)
 const old=FakeSocket.instances[0]
 act(()=>{old.onopen?.();old.onmessage?.({data:new Uint8Array([65]).buffer} as MessageEvent);fake.pending.shift()?.()})
 rerender(<TerminalView {...props} active={false} visit={visit('miss')}/>)
 fake.autoRender=false
 rerender(<TerminalView {...props} active visit={visit('hit')}/>)
 const firstReturn=FakeSocket.instances[1]
 act(()=>{firstReturn.onmessage?.({data:new Uint8Array([66]).buffer} as MessageEvent);for(const callback of [...fake.renderers])callback();fake.pending.shift()?.();for(const callback of [...fake.renderers])callback()})
 expect(phases).not.toContain('cached-render')
 rerender(<TerminalView {...props} active={false} visit={visit('hit')}/>)
 fake.autoRender=true
 const frames:FrameRequestCallback[]=[]
 vi.stubGlobal('requestAnimationFrame',(callback:FrameRequestCallback)=>{frames.push(callback);return frames.length})
 rerender(<TerminalView {...props} active visit={visit('hit')}/>)
 const secondReturn=FakeSocket.instances[2]
 act(()=>secondReturn.onmessage?.({data:new Uint8Array([67]).buffer} as MessageEvent))
 act(()=>{for(const callback of frames.splice(0))callback(0)})
 expect(phases).not.toContain('cached-render')
 window.removeEventListener('herdr:terminal-timing',listener)
})
