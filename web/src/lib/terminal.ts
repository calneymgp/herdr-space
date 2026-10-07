export type TerminalMode='observe'|'control'
type Sendable={readyState:number;send(data:string):void}
const emit=(ws:Sendable,payload:object)=>{if(ws.readyState===1) ws.send(JSON.stringify(payload))}
export const sendTerminalInput=(ws:Sendable,mode:TerminalMode,data:string)=>{if(mode==='control'&&data)emit(ws,{type:'input',data})}
export const sendTerminalResize=(ws:Sendable,mode:TerminalMode,cols:number,rows:number)=>{if(mode==='control')emit(ws,{type:'resize',cols:Math.max(20,Math.min(500,Math.round(cols))),rows:Math.max(5,Math.min(200,Math.round(rows)))})}
export const sendTerminalScroll=(ws:Sendable,mode:TerminalMode,delta:number)=>{if(mode==='control')emit(ws,{type:'scroll',delta:Math.max(-1000,Math.min(1000,Math.round(delta)))})}
export const sendTerminalRelease=(ws:Sendable)=>emit(ws,{type:'release'})
export const terminalUrl=(id:string,mode:TerminalMode,takeover:boolean,cols:number,rows:number)=>{
 const u=new URL('/api/v1/terminals/'+encodeURIComponent(id)+'/stream',location.href)
 u.protocol=location.protocol==='https:'?'wss:':'ws:'
 u.searchParams.set('mode',mode);u.searchParams.set('takeover',String(takeover));u.searchParams.set('cols',String(Math.max(20,Math.min(500,cols))));u.searchParams.set('rows',String(Math.max(5,Math.min(200,rows))))
 return u.toString()
}
