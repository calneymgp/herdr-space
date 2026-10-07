import type {Session} from './state'

// JSON preserves field boundaries and distinguishes absent values from strings.
export const terminalIdentity=(session:Session)=>JSON.stringify([
 session.id,session.source,session.server_id,session.terminal_id,session.pane_id,session.pid,session.start_time,
].map(value=>value===undefined?{absent:true}:value))

export class TerminalCache {
 private entries:Session[]=[]
 items(){return this.entries.slice()}
 visit(session:Session){
  const identity=terminalIdentity(session)
  const hit=this.entries.some(item=>terminalIdentity(item)===identity)
  const evicted=this.entries.filter(item=>item.id===session.id&&terminalIdentity(item)!==identity)
  this.entries=this.entries.filter(item=>item.id!==session.id)
  this.entries.push(session)
  if(this.entries.length>3)evicted.push(this.entries.shift()!)
  return {evicted,hit}
 }
 evict(predicate:(session:Session)=>boolean){const removed=this.entries.filter(predicate);this.entries=this.entries.filter(item=>!predicate(item));return removed}
 clear(){const removed=this.entries;this.entries=[];return removed}
}
