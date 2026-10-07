import {expect,it} from 'vitest'
import {TerminalCache,terminalIdentity} from './terminalCache'
import type {Session} from './state'
const session=(id:string):Session=>({id,name:id,source:'herdr',agent:'codex',launcher:'codex',project_id:'',cwd:'',server_id:'server',terminal_id:'terminal',workspace_id:'',pane_id:'pane',pid:1,start_time:'1',membership:'managed',activity:'active',alive:true,updated_at:'',capabilities:['observe']})
it('retains only three visited identities and evicts the least recently visited',()=>{
 const cache=new TerminalCache()
 const a=session('a'),b=session('b'),c=session('c'),d=session('d')
 expect(cache.visit(a).evicted).toEqual([])
 cache.visit(b);cache.visit(c);expect(cache.visit(a).hit).toBe(true)
 expect(cache.visit(d).evicted.map(item=>item.id)).toEqual(['b'])
 expect(cache.items().map(item=>item.id)).toEqual(['c','a','d'])
 expect(cache.visit({...a,pid:2}).evicted.map(item=>item.id)).toEqual(['a'])
 expect(cache.items().map(item=>item.id)).toEqual(['c','d','a'])
 expect(terminalIdentity({...a,name:'renamed',updated_at:'later'})).toBe(terminalIdentity(a))
})
