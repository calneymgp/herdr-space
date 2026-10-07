import {cleanup,render,screen,within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {afterEach,expect,it,vi} from 'vitest'
import {App} from './App'
const terminalMounts=vi.hoisted(()=>({count:0,disposes:0}))
vi.mock('./features/TerminalView',async()=>{const React=await import('react');return {TerminalView:({mode,takeover,focusRequest,active}:{mode:string;takeover:boolean;focusRequest?:number;active?:boolean})=>{React.useEffect(()=>{terminalMounts.count++;return()=>{terminalMounts.disposes++}},[]);return <div data-testid="terminal-connection" data-mode={mode} data-takeover={String(takeover)} data-focus-request={focusRequest} data-active={String(active)}><input aria-label="Terminal draft"/></div>}}})
afterEach(()=>{cleanup();vi.unstubAllGlobals();terminalMounts.count=0;terminalMounts.disposes=0})
it('does not offer first-run signup when the server is already configured',async()=>{
 vi.stubGlobal('fetch',vi.fn().mockResolvedValue({ok:true,status:200,headers:new Headers(),text:async()=>JSON.stringify({authenticated:false,configured:true,setup_available:true})}))
 render(<App/>)
 await screen.findByRole('heading',{name:'Welcome back.'})
 expect(screen.queryByRole('heading',{name:'Set up your Space'})).toBeNull()
 expect(screen.getByRole('button',{name:/Sign in to Space/})).toBeTruthy()
})

it('shows native Spaces and Other sessions without a Projects navigation or synthetic project entries',async()=>{
 signedIn()
 const project={id:'legacy',name:'Old project',path:'/work/old',created_at:'2026-01-01T00:00:00Z'}
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession],spaces:[space]}:name==='projects'?{items:[project]}:{items:[]})
 render(<App/>)
 expect(await screen.findByRole('button',{name:'demo'})).toBeTruthy()
 expect(screen.getByRole('button',{name:'Other sessions'})).toBeTruthy()
 expect(screen.queryByRole('button',{name:'Projects'})).toBeNull()
 expect(screen.queryByRole('button',{name:'Old project'})).toBeNull()
})

it('offers a native Space to start an agent when there are no legacy projects',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:/New agent/}))
 expect(await screen.findByRole('heading',{name:'Start an agent'})).toBeTruthy()
 expect(screen.getByRole('combobox',{name:'Space'})).toBeTruthy()
 expect(screen.queryByRole('combobox',{name:'Project'})).toBeNull()
})

it('disables agent launch when a native Space has no available directory',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[{...space,path:''}]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:/New agent/}))
 expect(await screen.findByText('The available Spaces do not have working directories yet.')).toBeTruthy()
 expect((screen.getByRole('option',{name:'demo · directory unavailable'}) as HTMLOptionElement).disabled).toBe(true)
 expect((screen.getByRole('button',{name:'Start agent'}) as HTMLButtonElement).disabled).toBe(true)
})

it('resolves the selected Space to legacy project metadata before launching an agent',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:{items:[]})
 const resolveSpace=vi.spyOn(api,'spaceProject').mockResolvedValue({item:{id:'internal-space-project',name:'demo',path:'/work/demo',created_at:'2026-01-01T00:00:00Z'}})
 const start=vi.spyOn(api,'start').mockResolvedValue({item:controlledSession})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:/New agent/}))
 fireEvent.click(screen.getByRole('button',{name:'Start agent'}))
 await waitFor(()=>expect(start).toHaveBeenCalledWith('codex','codex','internal-space-project'))
 expect(resolveSpace).toHaveBeenCalledWith('space-1')
})

it('keeps local tasks separate and resolves their native Space association on save',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:{items:[]})
 const spaceProject=vi.spyOn(api,'spaceProject').mockResolvedValue({item:{id:'internal-space-project',name:'demo',path:'/work/demo',created_at:'2026-01-01T00:00:00Z'}})
 const create=vi.spyOn(api,'create').mockResolvedValue({item:task})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'New task'}))
 expect(screen.getByRole('combobox',{name:'Associated Space'})).toBeTruthy()
 expect(screen.queryByRole('combobox',{name:'Project'})).toBeNull()
 fireEvent.change(screen.getByRole('textbox',{name:'Title'}),{target:{value:'Plan delivery'}})
 fireEvent.change(screen.getByRole('combobox',{name:'Associated Space'}),{target:{value:'space:space-1'}})
 fireEvent.click(screen.getByRole('button',{name:'Save task'}))
 await waitFor(()=>expect(create).toHaveBeenCalledWith('tasks',expect.objectContaining({project_id:'internal-space-project',title:'Plan delivery'})))
 expect(spaceProject).toHaveBeenCalledWith('space-1')
})

it('offers GitHub Issues as a separate Tasks source',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:{items:[]})
 vi.spyOn(api,'githubStatus').mockResolvedValue({available:true,authenticated:true})
 vi.spyOn(api,'githubRepositories').mockResolvedValue({items:[{space_id:'space-1',space_name:'demo',repository:'example/demo'}]})
 vi.spyOn(api,'githubIssues').mockResolvedValue({items:[{number:12,title:'Fix flow',body:'',state:'open',url:'https://github.com/example/demo/issues/12',updated_at:'2026-10-06T10:00:00Z'}],page:1,has_more:false})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'Tasks'}))
 fireEvent.click(screen.getByRole('button',{name:'GitHub Issues'}))
 expect(await screen.findByRole('button',{name:'Fix flow'})).toBeTruthy()
})
import {act,fireEvent,waitFor} from '@testing-library/react'
import {api,ApiError} from './lib/api'
import type {Note,Session,Space} from './lib/state'
const task={id:'task-1',title:'Review plan',description:'',project_id:'',session_id:'',status:'todo' as const,due_date:'2026-10-05',created_at:'2026-01-01T00:00:00Z',updated_at:'2026-01-01T00:00:00Z'}
function signedIn(onSessions?:(handler:(event:MessageEvent)=>void)=>void){
 vi.stubGlobal('EventSource',class{addEventListener(type:string,handler:(event:MessageEvent)=>void){if(type==='sessions')onSessions?.(handler)}close(){}})
 vi.spyOn(api,'status').mockResolvedValue({authenticated:true,configured:true,username:'owner',csrf_token:'token'})
 vi.spyOn(api,'preferences').mockResolvedValue({item:{theme:'system'}})
}
const space:Space={id:'space-1',name:'demo',path:'/work/demo',server_id:'server-1',workspace_id:'workspace-1'}
const controlledSession:Session={id:'session-1',name:'Implementation A',source:'herdr',agent:'codex',launcher:'codex',project_id:'',cwd:'/work/demo',server_id:'server-1',terminal_id:'terminal-1',workspace_id:'workspace-1',pane_id:'pane-1',pid:123,start_time:'2026-10-05T10:00:00Z',membership:'managed',activity:'active',alive:true,updated_at:'2026-10-05T10:00:00Z',capabilities:['observe','control']}
it('opens selected Space history without replacing the terminal or its draft, then restores trigger focus',async()=>{
 signedIn()
 const ended:Session={...controlledSession,id:'ended-1',name:'Previous conversation',source:'external',membership:'external',alive:false,activity:'done',capabilities:[]}
 const elsewhere:Session={...ended,id:'elsewhere',name:'Another Space',workspace_id:'workspace-2'}
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession,ended,elsewhere],spaces:[space]}:{items:[]})
 const resume=vi.spyOn(api,'resume').mockResolvedValue({item:ended})
 const stop=vi.spyOn(api,'stop').mockResolvedValue(undefined)
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'demo'}))
 fireEvent.click(screen.getByRole('tab',{name:'Implementation A'}))
 const terminal=await screen.findByTestId('terminal-connection')
 fireEvent.change(within(terminal).getByRole('textbox',{name:'Terminal draft'}),{target:{value:'preserved draft'}})
 const mountCount=terminalMounts.count
 const history=screen.getByRole('button',{name:'Space history'})
 history.focus()
 fireEvent.click(history)
 const dialog=await screen.findByRole('dialog',{name:'History of demo'})
 expect(within(dialog).getByRole('heading',{name:'Previous conversation'})).toBeTruthy()
 expect(within(dialog).getByRole('textbox',{name:'Search sessions'})).toBe(document.activeElement)
 expect(within(dialog).queryByText('Another Space')).toBeNull()
 expect(screen.getByTestId('terminal-connection')).toBe(terminal)
 expect(terminalMounts.count).toBe(mountCount)
 expect(terminal.getAttribute('data-mode')).toBe('control')
 expect(resume).not.toHaveBeenCalled()
 expect(stop).not.toHaveBeenCalled()
 fireEvent.keyDown(dialog,{key:'Escape'})
 await waitFor(()=>expect(screen.queryByRole('dialog',{name:'History of demo'})).toBeNull())
 await waitFor(()=>expect(document.activeElement).toBe(history))
 expect(screen.getByRole('heading',{name:'demo'})).toBeTruthy()
 expect(screen.getByTestId('terminal-connection')).toBe(terminal)
 expect((within(terminal).getByRole('textbox',{name:'Terminal draft'}) as HTMLInputElement).value).toBe('preserved draft')
 expect(terminalMounts.count).toBe(mountCount)
})

it('offers only a supported resume action for an ended session in selected Space history',async()=>{
 signedIn()
 const ended:Session={...controlledSession,id:'ended-1',name:'Previous conversation',alive:false,activity:'done',capabilities:['resume','control','stop']}
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession,ended],spaces:[space]}:{items:[]})
 const resume=vi.spyOn(api,'resume').mockResolvedValue({item:ended})
 const stop=vi.spyOn(api,'stop').mockResolvedValue(undefined)
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'demo'}))
 fireEvent.click(screen.getByRole('button',{name:'Space history'}))
 const dialog=await screen.findByRole('dialog',{name:'History of demo'})
 const card=within(dialog).getByRole('heading',{name:'Previous conversation'}).closest('.session-card') as HTMLElement
 expect(within(card).getByRole('button',{name:'Resume'})).toBeTruthy()
 expect(within(card).queryByRole('button',{name:'Control'})).toBeNull()
 expect(within(card).queryByRole('button',{name:/Parar/})).toBeNull()
 fireEvent.click(within(card).getByRole('button',{name:'Resume'}))
 fireEvent.click(await screen.findByRole('button',{name:'Confirm'}))
 await waitFor(()=>expect(resume).toHaveBeenCalledWith('ended-1',''))
 expect(stop).not.toHaveBeenCalled()
 expect(screen.queryByTestId('terminal-connection')).toBeNull()
})

it('returns focus to the history action after cancelling its nested confirmation',async()=>{
 signedIn()
 const stoppable={...controlledSession,capabilities:['observe','control','stop']}
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[stoppable],spaces:[space]}:{items:[]})
 const stop=vi.spyOn(api,'stop').mockResolvedValue(undefined)
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'demo'}))
 fireEvent.click(screen.getByRole('tab',{name:'Implementation A'}))
 const terminal=await screen.findByTestId('terminal-connection')
 fireEvent.click(screen.getByRole('button',{name:'Space history'}))
 const history=await screen.findByRole('dialog',{name:'History of demo'})
 const trigger=within(history).getByRole('button',{name:'Stop Implementation A'})
 for(const dismissal of ['Escape','Cancel']){
  fireEvent.click(trigger)
  const confirmation=await screen.findByRole('dialog',{name:'Stop this agent?'})
  if(dismissal==='Escape')fireEvent.keyDown(confirmation,{key:'Escape'})
  else fireEvent.click(within(confirmation).getByRole('button',{name:'Cancel'}))
  await waitFor(()=>expect(screen.queryByRole('dialog',{name:'Stop this agent?'})).toBeNull())
  expect(screen.getByRole('dialog',{name:'History of demo'})).toBe(history)
  await waitFor(()=>expect(document.activeElement).toBe(trigger))
  expect(screen.getByTestId('terminal-connection')).toBe(terminal)
  expect(terminal.getAttribute('data-mode')).toBe('control')
  expect(stop).not.toHaveBeenCalled()
 }
})

it('returns nested confirmation focus to history search when its action disappears',async()=>{
 let emit:((event:MessageEvent)=>void)|undefined
 signedIn(handler=>{emit=handler})
 const stoppable:Session={...controlledSession,id:'session-2',name:'Another session',capabilities:['stop']}
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession,stoppable],spaces:[space]}:{items:[]})
 const stop=vi.spyOn(api,'stop').mockResolvedValue(undefined)
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'demo'}))
 fireEvent.click(screen.getByRole('tab',{name:'Implementation A'}))
 const terminal=await screen.findByTestId('terminal-connection')
 fireEvent.click(screen.getByRole('button',{name:'Space history'}))
 const history=await screen.findByRole('dialog',{name:'History of demo'})
 fireEvent.click(within(history).getByRole('button',{name:'Stop Another session'}))
 const confirmation=await screen.findByRole('dialog',{name:'Stop this agent?'})
 act(()=>emit?.({data:JSON.stringify({items:[controlledSession,{...stoppable,capabilities:[]}],spaces:[space]})} as MessageEvent))
 expect(within(history).queryByRole('button',{name:'Stop Another session'})).toBeNull()
 fireEvent.keyDown(confirmation,{key:'Escape'})
 await waitFor(()=>expect(screen.queryByRole('dialog',{name:'Stop this agent?'})).toBeNull())
 await waitFor(()=>expect(document.activeElement).toBe(within(history).getByRole('textbox',{name:'Search sessions'})))
 expect(screen.getByTestId('terminal-connection')).toBe(terminal)
 expect(stop).not.toHaveBeenCalled()
})

it('keeps focus in history when a confirmed stop later removes its action',async()=>{
 signedIn()
 const stoppable:Session={...controlledSession,id:'session-2',name:'Another session',capabilities:['stop']}
 let afterStop:Promise<void>|undefined,releaseReload:(()=>void)|undefined
 vi.spyOn(api,'list').mockImplementation(async name=>{
  if(name!=='sessions')return {items:[]}
  if(afterStop)await afterStop
  return {items:afterStop?[controlledSession]:[controlledSession,stoppable],spaces:[space]}
 })
 const stop=vi.spyOn(api,'stop').mockImplementation(async()=>{afterStop=new Promise(resolve=>{releaseReload=resolve})})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'demo'}))
 fireEvent.click(screen.getByRole('tab',{name:'Implementation A'}))
 const terminal=await screen.findByTestId('terminal-connection')
 fireEvent.click(screen.getByRole('button',{name:'Space history'}))
 const history=await screen.findByRole('dialog',{name:'History of demo'})
 fireEvent.click(within(history).getByRole('button',{name:'Stop Another session'}))
 fireEvent.click(within(await screen.findByRole('dialog',{name:'Stop this agent?'})).getByRole('button',{name:'Confirm'}))
 await waitFor(()=>expect(stop).toHaveBeenCalledTimes(1))
 await waitFor(()=>expect(screen.queryByRole('dialog',{name:'Stop this agent?'})).toBeNull())
 act(()=>releaseReload?.())
 await waitFor(()=>expect(within(history).queryByRole('button',{name:'Stop Another session'})).toBeNull())
 await waitFor(()=>expect(document.activeElement).toBe(within(history).getByRole('textbox',{name:'Search sessions'})))
 expect(screen.getByTestId('terminal-connection')).toBe(terminal)
})

it('shows English icon hints on hover and keyboard focus without changing button names or touch actions',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:{items:[]})
 render(<App/>)
 const refresh=await screen.findByRole('button',{name:'Refresh data'})
 fireEvent.pointerMove(refresh.parentElement as HTMLElement,{pointerType:'mouse'})
 expect(screen.getByRole('tooltip').textContent).toBe('Refresh data')
 expect(screen.getByRole('button',{name:'Refresh data'})).toBe(refresh)
 fireEvent.pointerLeave(refresh.parentElement as HTMLElement,{pointerType:'mouse'})
 expect(screen.queryByRole('tooltip')).toBeNull()
 fireEvent.focus(refresh)
 expect(screen.getByRole('tooltip').textContent).toBe('Refresh data')
 fireEvent.keyDown(refresh,{key:'Escape'})
 expect(screen.queryByRole('tooltip')).toBeNull()
 fireEvent.click(await screen.findByRole('button',{name:'demo'}))
 const history=screen.getByRole('button',{name:'Space history'})
 fireEvent.focus(history)
 expect(screen.getByRole('tooltip').textContent).toBe('Space history')
 fireEvent.click(history)
 expect(await screen.findByRole('dialog',{name:'History of demo'})).toBeTruthy()
})
it('opens a controllable session in control on tab click without automatic takeover or reconnection',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession],spaces:[space]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'demo'}))
 const tab=await screen.findByRole('tab',{name:'Implementation A'})
 expect(screen.queryByTestId('terminal-connection')).toBeNull()
 fireEvent.click(tab)
 const terminal=await screen.findByTestId('terminal-connection')
 expect(terminal.getAttribute('data-mode')).toBe('control')
 expect(terminal.getAttribute('data-takeover')).toBe('false')
 const previousFocusRequest=Number(terminal.getAttribute('data-focus-request'))
 fireEvent.click(tab)
 expect(screen.getByTestId('terminal-connection')).toBe(terminal)
 expect(Number(terminal.getAttribute('data-focus-request'))).toBeGreaterThan(previousFocusRequest)
})
it('uses read-only observation when a session lacks control',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[{...controlledSession,capabilities:['observe']}],spaces:[space]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'demo'}))
 fireEvent.click(await screen.findByRole('tab',{name:'Implementation A'}))
 expect((await screen.findByTestId('terminal-connection')).getAttribute('data-mode')).toBe('observe')
})
it('keeps inventory and filters out of the selected space workspace',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession],spaces:[space]}:{items:[]})
 render(<App/>)
 expect(await screen.findByRole('button',{name:'demo'})).toBeTruthy()
 fireEvent.click(screen.getByRole('button',{name:'demo'}))
 expect(screen.getByRole('heading',{name:'demo'})).toBeTruthy()
 expect(screen.queryByRole('button',{name:'All'})).toBeNull()
 expect(screen.queryByRole('article')).toBeNull()
 expect(screen.getByRole('tab',{name:'Implementation A'})).toBeTruthy()
})
it('keeps one page title and one selected Space activity count',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession],spaces:[space]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'demo'}))
 expect(screen.getAllByRole('heading',{level:1})).toHaveLength(1)
 expect(screen.getByRole('heading',{level:1,name:'demo'})).toBeTruthy()
 expect(screen.getAllByText('1 active session')).toHaveLength(1)
 fireEvent.click(screen.getByRole('button',{name:'Tasks'}))
 expect(screen.getAllByRole('heading',{level:1})).toHaveLength(1)
 expect(screen.getAllByText('Tasks')).toHaveLength(2) // navigation and page heading
 fireEvent.click(screen.getByRole('button',{name:'Notes'}))
 expect(await screen.findByRole('heading',{level:2,name:'Notes index'})).toBeTruthy()
 expect(screen.getAllByRole('heading',{level:1})).toHaveLength(1)
 expect(screen.getAllByText('Notes')).toHaveLength(2)
})
it('keeps card metadata searchable and accessible behind details while actions stay visible',async()=>{
 signedIn()
 const session={...controlledSession,name:'Long implementation session',cwd:'/work/very-long-path/unique-leaf',project_id:'project-1'}
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[session],spaces:[space]}:name==='projects'?{items:[{id:'project-1',name:'Unique project',path:session.cwd,created_at:'2026-01-01T00:00:00Z'}]}:{items:[]})
 render(<App/>)
 const heading=await screen.findByRole('heading',{name:session.name})
 const card=heading.closest<HTMLElement>('.session-card')!
 const details=within(card).getByText('Session details').closest<HTMLElement>('details')!
 expect(details.hasAttribute('open')).toBe(false)
 expect(within(card).getByRole('button',{name:'Control'})).toBeTruthy()
 fireEvent.change(screen.getByRole('textbox',{name:'Search sessions'}),{target:{value:'unique-leaf'}})
 expect(screen.getByRole('heading',{name:session.name})).toBeTruthy()
 fireEvent.click(within(card).getByText('Session details'))
 expect(details.hasAttribute('open')).toBe(true)
 expect(within(details).getByText(session.name)).toBeTruthy()
 expect(within(details).getByText(session.cwd)).toBeTruthy()
 expect(within(details).getByText('Unique project')).toBeTruthy()
})
it('loads tasks when session inventory is unavailable',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?Promise.reject(new ApiError('unavailable',503)):{items:name==='tasks'?[task]:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'Tasks'}))
 expect(await screen.findByText('Review plan')).toBeTruthy()
 expect(screen.getByText(/Sessions unavailable/)).toBeTruthy()
})
it('clears previously loaded tasks on logout before another login finishes loading',async()=>{
 signedIn()
 let load=0
 vi.spyOn(api,'list').mockImplementation(async name=>{if(name==='tasks'){load++;if(load>1)return new Promise(()=>{});return {items:[task]}}return {items:[]}})
 vi.spyOn(api,'logout').mockResolvedValue(undefined)
 vi.spyOn(api,'login').mockResolvedValue({username:'owner',csrf_token:'token'})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'Tasks'}))
 await screen.findByText('Review plan')
 fireEvent.click(screen.getByTitle('Sign out'))
 await screen.findByRole('heading',{name:'Welcome back.'})
 fireEvent.change(screen.getByRole('textbox',{name:'Username'}),{target:{value:'owner'}})
 fireEvent.change(screen.getByLabelText('Password'),{target:{value:'password'}})
 fireEvent.change(screen.getByRole('textbox',{name:'Authentication or recovery code'}),{target:{value:'000000'}})
 fireEvent.click(screen.getByRole('button',{name:/Sign in to Space/}))
 await waitFor(()=>expect(screen.getByRole('heading',{name:'Tasks'})).toBeTruthy())
 expect(screen.queryByText('Review plan')).toBeNull()
})
it('ignores a refresh started before logout when its response arrives after re-login',async()=>{
 signedIn()
 let taskLoads=0
 let completeOldRefresh!:(value:{items:typeof task[]})=>void
 vi.spyOn(api,'list').mockImplementation(async name=>{
  if(name!=='tasks')return {items:[]}
  taskLoads++
  if(taskLoads===1)return {items:[task]}
  if(taskLoads===2)return new Promise(resolve=>{completeOldRefresh=resolve})
  return new Promise(()=>{})
 })
 vi.spyOn(api,'logout').mockResolvedValue(undefined)
 vi.spyOn(api,'login').mockResolvedValue({username:'owner',csrf_token:'token'})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'Tasks'}))
 await screen.findByText('Review plan')
 fireEvent.click(screen.getByRole('button',{name:'Refresh data'}))
 fireEvent.click(screen.getByTitle('Sign out'))
 await screen.findByRole('heading',{name:'Welcome back.'})
 fireEvent.change(screen.getByRole('textbox',{name:'Username'}),{target:{value:'owner'}})
 fireEvent.change(screen.getByLabelText('Password'),{target:{value:'password'}})
 fireEvent.change(screen.getByRole('textbox',{name:'Authentication or recovery code'}),{target:{value:'000000'}})
 fireEvent.click(screen.getByRole('button',{name:/Sign in to Space/}))
 await waitFor(()=>expect(screen.getByRole('heading',{name:'Tasks'})).toBeTruthy())
 await act(async()=>{completeOldRefresh({items:[task]});await Promise.resolve()})
 expect(screen.queryByText('Review plan')).toBeNull()
})
it('shows stored tasks while session inventory is still pending',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?new Promise(()=>{}):{items:name==='tasks'?[task]:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'Tasks'}))
 expect(await screen.findByText('Review plan')).toBeTruthy()
})
it('clears the workspace when any protected request returns 401',async()=>{
 signedIn()
 let taskLoads=0
 vi.spyOn(api,'list').mockImplementation(async name=>{if(name==='tasks'){taskLoads++;return taskLoads===1?{items:[task]}:new Promise(()=>{})}return {items:[]}})
 vi.spyOn(api,'login').mockResolvedValue({username:'owner',csrf_token:'token'})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'Tasks'}))
 await screen.findByText('Review plan')
 vi.stubGlobal('fetch',vi.fn().mockResolvedValue({ok:false,status:401,headers:new Headers(),text:async()=>JSON.stringify({error:'unauthorized'})}))
 await act(async()=>{await expect(api.request('/preferences')).rejects.toThrow()})
 await screen.findByRole('heading',{name:'Welcome back.'})
 fireEvent.change(screen.getByRole('textbox',{name:'Username'}),{target:{value:'owner'}})
 fireEvent.change(screen.getByLabelText('Password'),{target:{value:'password'}})
 fireEvent.change(screen.getByRole('textbox',{name:'Authentication or recovery code'}),{target:{value:'000000'}})
 fireEvent.click(screen.getByRole('button',{name:/Sign in to Space/}))
 await waitFor(()=>expect(screen.getByRole('heading',{name:'Tasks'})).toBeTruthy())
 expect(screen.queryByText('Review plan')).toBeNull()
})


it('keeps a manually selected Space and task draft through a live inventory refresh',async()=>{
 let onSessions:((event:MessageEvent)=>void)|undefined
 signedIn(handler=>{onSessions=handler})
 const secondSpace:Space={...space,id:'space-2',name:'Another Space',path:'/work/other',workspace_id:'workspace-2'}
 const internalProject={id:'project-1',name:'demo',path:space.path,created_at:'2026-01-01T00:00:00Z'}
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space,secondSpace]}:name==='projects'?{items:[internalProject]}:name==='tasks'?{items:[{...task,project_id:internalProject.id}]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'Tasks'}))
 fireEvent.click(await screen.findByRole('button',{name:'Review plan'}))
 fireEvent.change(screen.getByRole('textbox',{name:'Title'}),{target:{value:'Preserved draft'}})
 fireEvent.change(screen.getByRole('combobox',{name:'Associated Space'}),{target:{value:'space:space-2'}})
 await waitFor(()=>expect(onSessions).toBeTruthy())
 await act(async()=>{onSessions?.({data:JSON.stringify({items:[],spaces:[{...space,name:'demo atualizado'},secondSpace],stale:false})} as MessageEvent)})
 expect((screen.getByRole('textbox',{name:'Title'}) as HTMLInputElement).value).toBe('Preserved draft')
 expect((screen.getByRole('combobox',{name:'Associated Space'}) as HTMLSelectElement).value).toBe('space:space-2')
})

it('keeps the chosen agent Space through a live inventory refresh before launch',async()=>{
 let onSessions:((event:MessageEvent)=>void)|undefined
 signedIn(handler=>{onSessions=handler})
 const secondSpace:Space={...space,id:'space-2',name:'Another Space',path:'/work/other',workspace_id:'workspace-2'}
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space,secondSpace]}:{items:[]})
 vi.spyOn(api,'spaceProject').mockImplementation(async id=>({item:{id:'project-'+id,name:'Space',path:id==='space-2'?'/work/other':'/work/demo',created_at:'2026-01-01T00:00:00Z'}}))
 const start=vi.spyOn(api,'start').mockResolvedValue({item:controlledSession})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:/New agent/}))
 fireEvent.change(screen.getByRole('combobox',{name:'Space'}),{target:{value:'space-2'}})
 await waitFor(()=>expect(onSessions).toBeTruthy())
 await act(async()=>{onSessions?.({data:JSON.stringify({items:[],spaces:[{...space,name:'demo atualizado'},secondSpace],stale:false})} as MessageEvent)})
 expect((screen.getByRole('combobox',{name:'Space'}) as HTMLSelectElement).value).toBe('space-2')
 fireEvent.click(screen.getByRole('button',{name:'Start agent'}))
 await waitFor(()=>expect(start).toHaveBeenCalledWith('codex','codex','project-space-2'))
})

it('defaults a reopened agent dialog to the Space selected in the sidebar',async()=>{
 signedIn()
 const secondSpace:Space={...space,id:'space-2',name:'Another Space',path:'/work/other',workspace_id:'workspace-2'}
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space,secondSpace]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:/New agent/}))
 expect((screen.getByRole('combobox',{name:'Space'}) as HTMLSelectElement).value).toBe('space-1')
 fireEvent.click(screen.getByRole('button',{name:'Cancel'}))
 fireEvent.click(screen.getByRole('button',{name:'Another Space'}))
 fireEvent.click(screen.getByRole('button',{name:/New agent/}))
 expect((screen.getByRole('combobox',{name:'Space'}) as HTMLSelectElement).value).toBe('space-2')
})

it('keeps the latest task, note, and inventory after overlapping reloads resolve out of order',async()=>{
 signedIn()
 const oldNote:Note={id:'note-old',title:'Initial note',body:'',project_id:'',version:1,created_at:'2026-01-01T00:00:00Z',updated_at:'2026-01-01T00:00:00Z'}
 const newestNote:Note={...oldNote,id:'note-b',title:'Reload note B'}
 const newestTask={...task,id:'task-b',title:'Reload task B'}
 const staleTask={...task,id:'task-a',title:'Old response A'}
 const staleNote:Note={...oldNote,id:'note-a',title:'Old note A'}
 const newestSession:Session={...controlledSession,id:'session-b',name:'Reload session B'}
 const staleSession:Session={...controlledSession,id:'session-a',name:'Old session A'}
 const initial:Record<string,{items:unknown[];spaces?:Space[]}>= {projects:{items:[]},tasks:{items:[task]},notes:{items:[oldNote]},sessions:{items:[controlledSession],spaces:[space]}}
 const latest:Record<string,{items:unknown[];spaces?:Space[]}>= {projects:{items:[]},tasks:{items:[newestTask]},notes:{items:[newestNote]},sessions:{items:[newestSession],spaces:[space]}}
 const stale:Record<string,{items:unknown[];spaces?:Space[]}>= {projects:{items:[]},tasks:{items:[staleTask]},notes:{items:[staleNote]},sessions:{items:[staleSession],spaces:[space]}}
 const calls:Record<string,number>={},resolveOld:Record<string,(result:{items:unknown[];spaces?:Space[]})=>void>={}
 vi.spyOn(api,'list').mockImplementation(async name=>{
  const request=(calls[name]||0)+1;calls[name]=request
  if(request===1)return initial[name] as never
  if(request===2)return new Promise(resolve=>{resolveOld[name]=resolve}) as never
  return latest[name] as never
 })
 const create=vi.spyOn(api,'create').mockResolvedValue({item:newestTask})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'Tasks'}))
 await screen.findByRole('button',{name:'Review plan'})
 fireEvent.click(screen.getByRole('button',{name:'Refresh data'}))
 await waitFor(()=>expect(Object.keys(resolveOld)).toHaveLength(4))
 fireEvent.click(screen.getByRole('button',{name:'New task'}))
 fireEvent.change(screen.getByRole('textbox',{name:'Title'}),{target:{value:'Create new version'}})
 fireEvent.click(screen.getByRole('button',{name:'Save task'}))
 await waitFor(()=>expect(create).toHaveBeenCalled())
 expect(await screen.findByRole('button',{name:'Reload task B'})).toBeTruthy()
 await waitFor(()=>expect(Object.values(calls).every(count=>count===3)).toBe(true))
 await act(async()=>{for(const [name,resolve] of Object.entries(resolveOld))resolve(stale[name]);await Promise.resolve()})
 expect(screen.getByRole('button',{name:'Reload task B'})).toBeTruthy()
 expect(screen.queryByRole('button',{name:'Old response A'})).toBeNull()
 fireEvent.click(screen.getByRole('button',{name:'Sessions'}))
 expect(await screen.findByRole('heading',{name:'Reload session B'})).toBeTruthy()
 expect(screen.queryByRole('heading',{name:'Old session A'})).toBeNull()
 fireEvent.click(screen.getByRole('button',{name:'Notes'}))
 expect(await screen.findByText('Reload note B')).toBeTruthy()
 expect(screen.queryByText('Old note A')).toBeNull()
})

it('keeps newer SSE sessions when an older inventory request finishes later',async()=>{
 let onSessions:((event:MessageEvent)=>void)|undefined,sessionRequests=0,resolveOld!:(result:{items:Session[];spaces:Space[]})=>void
 signedIn(handler=>{onSessions=handler})
 vi.spyOn(api,'list').mockImplementation(async name=>{
  if(name==='sessions'){
   sessionRequests++
   if(sessionRequests===1)return {items:[controlledSession],spaces:[space]}
   return new Promise(resolve=>{resolveOld=resolve})
  }
  return {items:[]}
 })
 const liveSession:Session={...controlledSession,id:'session-live',name:'Session received via SSE'}
 const staleSession:Session={...controlledSession,id:'session-stale',name:'Old HTTP session'}
 render(<App/> )
 expect(await screen.findByRole('heading',{name:'Implementation A'})).toBeTruthy()
 fireEvent.click(screen.getByRole('button',{name:'Refresh data'}))
 await waitFor(()=>expect(resolveOld).toBeTruthy())
 await act(async()=>{onSessions?.({data:JSON.stringify({items:[liveSession],spaces:[space],stale:false})} as MessageEvent)})
 expect(await screen.findByRole('heading',{name:'Session received via SSE'})).toBeTruthy()
 await act(async()=>{resolveOld({items:[staleSession],spaces:[space]});await Promise.resolve()})
 expect(screen.getByRole('heading',{name:'Session received via SSE'})).toBeTruthy()
 expect(screen.queryByRole('heading',{name:'Old HTTP session'})).toBeNull()
})

it('keeps the task dialog open through escape and outside close while a save is pending',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:{items:[]})
 let resolveProject!:(value:{item:{id:string;name:string;path:string;created_at:string}})=>void
 const projectRequest=new Promise<{item:{id:string;name:string;path:string;created_at:string}}>(resolve=>{resolveProject=resolve})
 vi.spyOn(api,'spaceProject').mockReturnValue(projectRequest)
 const create=vi.spyOn(api,'create').mockResolvedValue({item:task})
 render(<App/> )
 fireEvent.click(await screen.findByRole('button',{name:'New task'}))
 fireEvent.change(screen.getByRole('textbox',{name:'Title'}),{target:{value:'Draft during save'}})
 fireEvent.change(screen.getByRole('combobox',{name:'Associated Space'}),{target:{value:'space:space-1'}})
 fireEvent.click(screen.getByRole('button',{name:'Save task'}))
 await waitFor(()=>expect(api.spaceProject).toHaveBeenCalledWith('space-1'))
 expect((screen.getByRole('textbox',{name:'Title'}) as HTMLInputElement).disabled).toBe(true)
 fireEvent.keyDown(document,{key:'Escape',code:'Escape'})
 fireEvent.pointerDown(document.querySelector('.dialog-overlay')!,{button:0})
 fireEvent.click(screen.getByRole('button',{name:'Close'}))
 expect(screen.getByRole('heading',{name:'New task'})).toBeTruthy()
 expect((screen.getByRole('textbox',{name:'Title'}) as HTMLInputElement).value).toBe('Draft during save')
 await act(async()=>{resolveProject({item:{id:'project-space',name:'demo',path:'/work/demo',created_at:'2026-01-01T00:00:00Z'}})})
 await waitFor(()=>expect(create).toHaveBeenCalled())
 await waitFor(()=>expect(screen.queryByRole('heading',{name:'New task'})).toBeNull())
})

it('keeps stop confirmation locked until mutation succeeds, then closes before a slow refresh',async()=>{
 signedIn()
 const stoppable={...controlledSession,capabilities:['stop']}
 let rejectRefresh!:(error:Error)=>void
 let sessionLoads=0
 vi.spyOn(api,'list').mockImplementation(async name=>{
  if(name!=='sessions')return {items:[]}
  sessionLoads++
  if(sessionLoads===1)return {items:[stoppable],spaces:[space]}
  return new Promise((_resolve,reject)=>{rejectRefresh=reject})
 })
 let resolveStop!:()=>void
 const stop=vi.spyOn(api,'stop').mockReturnValue(new Promise(resolve=>{resolveStop=()=>resolve(undefined)}))
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'Stop Implementation A'}))
 const confirmButton=screen.getByRole('button',{name:'Confirm'})
 fireEvent.click(confirmButton)
 fireEvent.click(confirmButton)
 expect(stop).toHaveBeenCalledTimes(1)
 expect((screen.getByRole('button',{name:'Cancel'}) as HTMLButtonElement).disabled).toBe(true)
 fireEvent.keyDown(document,{key:'Escape',code:'Escape'})
 expect(screen.getByRole('heading',{name:'Stop this agent?'})).toBeTruthy()
 await act(async()=>resolveStop())
 await waitFor(()=>expect(screen.queryByRole('heading',{name:'Stop this agent?'})).toBeNull())
 await act(async()=>rejectRefresh(new ApiError('unavailable',503)))
 expect(screen.queryByRole('heading',{name:'Stop this agent?'})).toBeNull()
 expect(stop).toHaveBeenCalledTimes(1)
})

it('keeps a failed task draft in its dialog and succeeds on explicit retry',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:{items:[]})
 const create=vi.spyOn(api,'create').mockRejectedValueOnce(new ApiError('invalid request',400)).mockResolvedValueOnce({item:task})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'New task'}))
 fireEvent.change(screen.getByRole('textbox',{name:'Title'}),{target:{value:'My draft'}})
 fireEvent.click(screen.getByRole('button',{name:'Save task'}))
 expect((await screen.findByRole('alert')).textContent).toContain('Invalid data')
 expect((screen.getByRole('textbox',{name:'Title'}) as HTMLInputElement).value).toBe('My draft')
 expect(screen.queryByText('invalid request')).toBeNull()
 fireEvent.click(screen.getByRole('button',{name:'Save task'}))
 await waitFor(()=>expect(create).toHaveBeenCalledTimes(2))
 await waitFor(()=>expect(screen.queryByRole('heading',{name:'New task'})).toBeNull())
})

it('shows a failed logout while preserving the workspace',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:{items:[]})
 vi.spyOn(api,'logout').mockRejectedValue(new ApiError('unavailable',503))
 render(<App/>)
 await screen.findByRole('button',{name:'New task'})
 fireEvent.click(screen.getByTitle('Sign out'))
 expect((await screen.findByRole('alert')).textContent).toContain('unavailable')
 expect(screen.getByRole('button',{name:'New task'})).toBeTruthy()
})

it('keeps agent choices in the dialog after a network failure and retries explicitly',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:{items:[]})
 vi.spyOn(api,'spaceProject').mockResolvedValue({item:{id:'project-space',name:'demo',path:'/work/demo',created_at:'2026-01-01T00:00:00Z'}})
 const start=vi.spyOn(api,'start').mockRejectedValueOnce(new ApiError('No connection to the server. Your local changes were preserved.',0)).mockResolvedValueOnce({item:controlledSession})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:/New agent/}))
 fireEvent.change(screen.getByRole('combobox',{name:'Agent'}),{target:{value:'pi'}})
 fireEvent.click(screen.getByRole('button',{name:'Start agent'}))
 expect((await screen.findByRole('alert')).textContent).toContain('No connection')
 expect((screen.getByRole('combobox',{name:'Agent'}) as HTMLSelectElement).value).toBe('pi')
 expect(screen.getByRole('heading',{name:'Start an agent'})).toBeTruthy()
 fireEvent.click(screen.getByRole('button',{name:'Start agent'}))
 await waitFor(()=>expect(start).toHaveBeenCalledTimes(2))
 await waitFor(()=>expect(screen.queryByRole('heading',{name:'Start an agent'})).toBeNull())
})

it('returns to login without a stale form alert on a protected malformed 401',async()=>{
 vi.restoreAllMocks()
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:{items:[]})
 vi.stubGlobal('fetch',vi.fn().mockResolvedValue({ok:false,status:401,text:async()=>'<html>unauthorized</html>'}))
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'New task'}))
 fireEvent.change(screen.getByRole('textbox',{name:'Title'}),{target:{value:'Protected draft'}})
 fireEvent.click(screen.getByRole('button',{name:'Save task'}))
 await screen.findByRole('heading',{name:'Welcome back.'})
 expect(screen.queryByRole('alert')).toBeNull()
})

it('shows only living sessions in a Space and keeps waiting and attention statuses distinct',async()=>{
 signedIn()
 const sessions:Session[]=[
  {...controlledSession,id:'working',name:'Working tab',activity:'working'},
  {...controlledSession,id:'idle',name:'Idle tab',activity:'idle'},
  {...controlledSession,id:'done',name:'Done tab',activity:'done'},
  {...controlledSession,id:'unknown',name:'Unknown tab',activity:'unknown'},
  {...controlledSession,id:'error',name:'Error tab',activity:'error'},
  {...controlledSession,id:'ended',name:'Ended tab',activity:'done',alive:false},
  {...controlledSession,id:'other-space',name:'Another Space',workspace_id:'workspace-2'}
 ]
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:sessions,spaces:[space]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'demo'}))
 const tabs=screen.getAllByRole('tab')
 expect(tabs.map(tab=>tab.getAttribute('aria-label')||tab.textContent?.trim())).toEqual([
  'Working tab','Idle tab','Done tab','Unknown tab','Error tab'
 ])
 expect(screen.queryByRole('tab',{name:'Ended tab'})).toBeNull()
 expect(screen.queryByRole('tab',{name:'Another Space'})).toBeNull()
 expect(screen.getByText('5 active sessions')).toBeTruthy()
 expect(screen.getByRole('button',{name:'demo'}).querySelector('.space-count')).toBeNull()
 expect(screen.getByRole('button',{name:'Other sessions'}).querySelector('.space-count')?.textContent).toBe('1')
 expect(screen.getByRole('tab',{name:'Working tab'}).querySelector('.workspace-tab-status')?.getAttribute('data-state')).toBe('running')
 expect(screen.getByRole('tab',{name:'Idle tab'}).querySelector('.workspace-tab-status')?.getAttribute('data-state')).toBe('waiting')
 expect(screen.getByRole('tab',{name:'Done tab'}).querySelector('.workspace-tab-status')?.getAttribute('data-state')).toBe('waiting')
 expect(screen.getByRole('tab',{name:'Unknown tab'}).querySelector('.workspace-tab-status')?.getAttribute('data-state')).toBe('attention')
 expect(screen.getByRole('tab',{name:'Error tab'}).querySelector('.workspace-tab-status')?.getAttribute('data-state')).toBe('attention')
 expect(screen.getByRole('tab',{name:'Working tab'}).getAttribute('aria-description')).toBe('Running')
 expect(screen.getByRole('tab',{name:'Idle tab'}).getAttribute('aria-description')).toBe('Idle')
 expect(screen.getByRole('tab',{name:'Done tab'}).getAttribute('aria-description')).toBe('Completed')
 expect(screen.getByRole('tab',{name:'Unknown tab'}).getAttribute('aria-description')).toBe('Unknown status')
 expect(screen.getByRole('tab',{name:'Error tab'}).getAttribute('aria-description')).toBe('Failed')
})

it('shows inventory cards with the same activity meaning as tabs and keeps ended history actions valid',async()=>{
 signedIn()
 const sessions:Session[]=[
  {...controlledSession,id:'card-working',name:'Card working',activity:'working',capabilities:['observe','control','resume']},
  {...controlledSession,id:'card-active',name:'Card active',activity:'active'},
  {...controlledSession,id:'card-idle',name:'Card idle',activity:'idle'},
  {...controlledSession,id:'card-done',name:'Card done',activity:'done'},
  {...controlledSession,id:'card-unknown',name:'Card unknown',activity:'unknown'},
  {...controlledSession,id:'card-error',name:'Card error',activity:'error'},
  {...controlledSession,id:'card-other',name:'Card other',activity:'unexpected'},
  {...controlledSession,id:'card-ended',name:'Card ended',activity:'done',alive:false,capabilities:['observe','control','resume','stop']}
 ]
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:sessions,spaces:[space]}:{items:[]})
 render(<App/>)
 const expected=[
  ['Card working','running','Running'],
  ['Card active','running','Running'],
  ['Card idle','waiting','Idle'],
  ['Card done','waiting','Completed'],
  ['Card unknown','attention','Unknown status'],
  ['Card error','attention','Failed'],
  ['Card other','attention','Unknown status'],
  ['Card ended','ended','Ended']
 ] as const
 await screen.findByRole('heading',{name:'Card working'})
 for(const [name,state,label] of expected){
  const card=screen.getByRole('heading',{name}).closest('.session-card')
  const badge=card?.querySelector('.status-badge')
  expect(badge?.getAttribute('data-state'),name).toBe(state)
  expect(badge?.textContent,name).toContain(label)
 }
 const working=within(screen.getByRole('heading',{name:'Card working'}).closest('.session-card')!)
 expect(working.getByRole('button',{name:'Observe'})).toBeTruthy()
 expect(working.getByRole('button',{name:'Control'})).toBeTruthy()
 expect(working.getByRole('button',{name:'Resume'})).toBeTruthy()
 const ended=within(screen.getByRole('heading',{name:'Card ended'}).closest('.session-card')!)
 expect(ended.getByRole('button',{name:'Resume'})).toBeTruthy()
 expect(ended.queryByRole('button',{name:'Observe'})).toBeNull()
 expect(ended.queryByRole('button',{name:'Control'})).toBeNull()
 expect(ended.queryByRole('button',{name:/Parar/})).toBeNull()
 expect(ended.queryByRole('button',{name:'Take existing control'})).toBeNull()
})

it('marks inventory cards stale through the current SSE connection',async()=>{
 signedIn()
 let onSessions:((event:MessageEvent)=>void)|undefined
 const sources:Array<{closed:boolean}> = []
 vi.stubGlobal('EventSource',class{
  closed=false
  constructor(){sources.push(this)}
  addEventListener(type:string,handler:(event:MessageEvent)=>void){if(type==='sessions')onSessions=handler}
  close(){this.closed=true}
 })
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession],spaces:[space]}:{items:[]})
 render(<App/>)
 await screen.findByRole('heading',{name:'Implementation A'})
 const card=screen.getByRole('heading',{name:'Implementation A'}).closest('.session-card')!
 expect(card.querySelector('.status-badge')?.getAttribute('data-state')).toBe('running')
 expect(sources).toHaveLength(1)
 await act(async()=>{onSessions?.({data:JSON.stringify({items:[controlledSession],spaces:[space],stale:true})} as MessageEvent)})
 expect(screen.getByRole('heading',{name:'Implementation A'}).closest('.session-card')?.querySelector('.status-badge')?.getAttribute('data-state')).toBe('attention')
 expect(screen.getByText('Outdated status')).toBeTruthy()
 expect(sources).toHaveLength(1)
 expect(sources[0].closed).toBe(false)
})

it('updates the tab status from SSE without remounting its terminal and marks stale status for attention',async()=>{
 let onSessions:((event:MessageEvent)=>void)|undefined
 const sources:Array<{closed:boolean}> = []
 signedIn(handler=>{onSessions=handler})
 vi.stubGlobal('EventSource',class{
  closed=false
  constructor(){sources.push(this)}
  addEventListener(type:string,handler:(event:MessageEvent)=>void){if(type==='sessions')onSessions=handler}
  close(){this.closed=true}
 })
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession],spaces:[space]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'demo'}))
 const tab=screen.getByRole('tab',{name:'Implementation A'})
 fireEvent.click(tab)
 const terminal=await screen.findByTestId('terminal-connection')
 tab.focus()
 expect(document.activeElement).toBe(tab)
 expect(sources).toHaveLength(1)
 expect(tab.querySelector('.workspace-tab-status')?.getAttribute('data-state')).toBe('running')
 await act(async()=>{onSessions?.({data:JSON.stringify({items:[{...controlledSession,activity:'idle'}],spaces:[space],stale:false})} as MessageEvent)})
 expect(screen.getByRole('tab',{name:'Implementation A'}).querySelector('.workspace-tab-status')?.getAttribute('data-state')).toBe('waiting')
 expect(screen.getByTestId('terminal-connection')).toBe(terminal)
 expect(document.activeElement).toBe(tab)
 expect(sources).toHaveLength(1)
 expect(sources[0].closed).toBe(false)
 await act(async()=>{onSessions?.({data:JSON.stringify({items:[{...controlledSession,activity:'working'}],spaces:[space],stale:true})} as MessageEvent)})
 expect(screen.getByRole('tab',{name:'Implementation A'}).querySelector('.workspace-tab-status')?.getAttribute('data-state')).toBe('attention')
})

it('removes an ended selected session from workspace tabs and clears its selection',async()=>{
 let onSessions:((event:MessageEvent)=>void)|undefined
 signedIn(handler=>{onSessions=handler})
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession],spaces:[space]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'demo'}))
 fireEvent.click(screen.getByRole('tab',{name:'Implementation A'}))
 expect(await screen.findByTestId('terminal-connection')).toBeTruthy()
 await act(async()=>{onSessions?.({data:JSON.stringify({items:[{...controlledSession,alive:false,activity:'done'}],spaces:[space],stale:false})} as MessageEvent)})
 expect(screen.queryByRole('tab',{name:'Implementation A'})).toBeNull()
 expect(screen.queryByTestId('terminal-connection')).toBeNull()
 expect(screen.getByText('No sessions in this Space.')).toBeTruthy()
 fireEvent.click(screen.getByRole('button',{name:'All spaces'}))
 expect(screen.getByRole('heading',{name:'Implementation A'})).toBeTruthy()
 fireEvent.click(screen.getByRole('button',{name:'demo'}))
 expect(screen.queryByRole('tab',{name:'Implementation A'})).toBeNull()
})

it('moves focus through workspace tabs without opening a terminal until Enter',async()=>{
 signedIn()
 const secondSession={...controlledSession,id:'session-2',name:'Implementation B'}
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession,secondSession],spaces:[space]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'demo'}))
 const user=userEvent.setup()
 const first=screen.getByRole('tab',{name:'Implementation A'})
 const second=screen.getByRole('tab',{name:'Implementation B'})
 expect(first.tabIndex).toBe(0)
 expect(second.tabIndex).toBe(-1)
 first.focus()
 await user.keyboard('{ArrowRight}')
 expect(document.activeElement).toBe(second)
 expect(second.tabIndex).toBe(0)
 expect(screen.queryByTestId('terminal-connection')).toBeNull()
 await user.keyboard('{Enter}')
 expect(await screen.findByTestId('terminal-connection')).toBeTruthy()
 const panel=screen.getByRole('tabpanel')
 expect(second.getAttribute('aria-controls')).toBe(panel.id)
 expect(panel.getAttribute('aria-labelledby')).toBe(second.id)
})

it('keeps terminal actions hidden on ended history while preserving its resume action',async()=>{
 signedIn()
 const endedSession:Session={...controlledSession,id:'ended-history',name:'Ended session',activity:'done',alive:false,capabilities:['observe','control','resume']}
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[endedSession],spaces:[space]}:{items:[]})
 render(<App/>)
 expect(await screen.findByRole('heading',{name:'Ended session'})).toBeTruthy()
 expect(screen.queryByRole('button',{name:'Observe'})).toBeNull()
 expect(screen.queryByRole('button',{name:'Control'})).toBeNull()
 expect(screen.queryByRole('button',{name:'Take existing control'})).toBeNull()
 expect(screen.getByRole('button',{name:'Resume'})).toBeTruthy()
 expect(screen.queryByTestId('terminal-connection')).toBeNull()
})

it('keeps the agent dialog open through escape and outside close while starting',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:{items:[]})
 vi.spyOn(api,'spaceProject').mockResolvedValue({item:{id:'project-space',name:'demo',path:'/work/demo',created_at:'2026-01-01T00:00:00Z'}})
 let resolveStart!:(value:{item:Session})=>void
 const pendingStart=new Promise<{item:Session}>(resolve=>{resolveStart=resolve})
 const start=vi.spyOn(api,'start').mockReturnValue(pendingStart)
 render(<App/> )
 fireEvent.click(await screen.findByRole('button',{name:/New agent/}))
 fireEvent.click(screen.getByRole('button',{name:'Start agent'}))
 await waitFor(()=>expect(start).toHaveBeenCalled())
 expect((screen.getByRole('combobox',{name:'Space'}) as HTMLSelectElement).disabled).toBe(true)
 fireEvent.keyDown(document,{key:'Escape',code:'Escape'})
 fireEvent.pointerDown(document.querySelector('.dialog-overlay')!,{button:0})
 fireEvent.click(screen.getByRole('button',{name:'Close'}))
 expect(screen.getByRole('heading',{name:'Start an agent'})).toBeTruthy()
 await act(async()=>{resolveStart({item:controlledSession})})
 await waitFor(()=>expect(screen.queryByRole('heading',{name:'Start an agent'})).toBeNull())
})

it('uses List on first mobile entry and Board on desktop, then preserves an explicit choice across resize and navigation',async()=>{
 const resize=installViewport(390)
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:{items:[]})
 render(<App/>)
 const mobileNav=await screen.findByRole('navigation',{name:'Mobile navigation'})
 fireEvent.click(within(mobileNav).getByRole('button',{name:'Tasks'}))
 expect(screen.getByRole('button',{name:'List'}).classList.contains('active')).toBe(true)
 expect(screen.getByRole('button',{name:'Board'}).classList.contains('active')).toBe(false)

 fireEvent.click(screen.getByRole('button',{name:'Board'}))
 resize(1440)
 expect(screen.getByRole('button',{name:'Board'}).classList.contains('active')).toBe(true)
 fireEvent.click(within(screen.getByRole('navigation',{name:'Main navigation'})).getByRole('button',{name:'Sessions'}))
 fireEvent.click(within(screen.getByRole('navigation',{name:'Main navigation'})).getByRole('button',{name:'Tasks'}))
 expect(screen.getByRole('button',{name:'Board'}).classList.contains('active')).toBe(true)
 resize(390)
 expect(screen.getByRole('button',{name:'Board'}).classList.contains('active')).toBe(true)
})

it('uses the responsive default until a manual Tasks view is selected',async()=>{
 const resize=installViewport(1440)
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:{items:[]})
 render(<App/>)
 fireEvent.click(within(await screen.findByRole('navigation',{name:'Main navigation'})).getByRole('button',{name:'Tasks'}))
 expect(screen.getByRole('button',{name:'Board'}).classList.contains('active')).toBe(true)
 resize(390)
 expect(screen.getByRole('button',{name:'List'}).classList.contains('active')).toBe(true)
})

it('shows a workspace cue only while tabs can continue scrolling and leaves the position alone on SSE updates',async()=>{
 let onSessions:((event:MessageEvent)=>void)|undefined
 signedIn(handler=>{onSessions=handler})
 const sessions=Array.from({length:5},(_,index)=>({...controlledSession,id:`session-${index+1}`,terminal_id:`terminal-${index+1}`,name:`Session ${index+1}`}))
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:sessions,spaces:[space]}:{items:[]})
 render(<App/>)
 const tab=await screen.findByRole('tab',{name:'Session 1'})
 fireEvent.click(tab)
 const tablist=screen.getByRole('tablist',{name:'Space sessions'})
 const cue=tablist.parentElement!
 expect(cue.getAttribute('data-scroll-cue')).toBeNull()
 Object.defineProperties(tablist,{clientWidth:{configurable:true,value:120},scrollWidth:{configurable:true,value:500},scrollLeft:{configurable:true,writable:true,value:0}})
 fireEvent.scroll(tablist)
 expect(cue.getAttribute('data-scroll-cue')).toBe('right')
 tablist.scrollLeft=380
 fireEvent.scroll(tablist)
 expect(cue.getAttribute('data-scroll-cue')).toBe('left')
 expect(tab.getAttribute('aria-selected')).toBe('true')
 const before=tablist.scrollLeft
 await act(async()=>{onSessions?.({data:JSON.stringify({items:[...sessions,{...controlledSession,id:'session-6',terminal_id:'terminal-6',name:'Session 6'}],spaces:[space],stale:false})} as MessageEvent)})
 expect(tablist.scrollLeft).toBe(before)
 expect(tab.getAttribute('aria-selected')).toBe('true')
})

it('shows a Kanban cue only when its columns overflow the board viewport',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[],spaces:[space]}:name==='tasks'?{items:[task]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('button',{name:'Tasks'}))
 const todoHeading=await screen.findByRole('heading',{name:'To do'})
 const board=todoHeading.closest('.kanban') as HTMLElement
 const cue=board.parentElement!
 expect(cue.getAttribute('data-scroll-cue')).toBeNull()
 Object.defineProperties(board,{clientWidth:{configurable:true,value:300},scrollWidth:{configurable:true,value:695},scrollLeft:{configurable:true,writable:true,value:0}})
 fireEvent.scroll(board)
 expect(cue.getAttribute('data-scroll-cue')).toBe('right')
 board.scrollLeft=395
 fireEvent.scroll(board)
 expect(cue.getAttribute('data-scroll-cue')).toBe('left')
})

function installViewport(initialWidth:number){
 let width=initialWidth
 const queries=new Map<string,{matches:boolean;listeners:Set<(event:MediaQueryListEvent)=>void>}>()
 const matches=(query:string)=>query.includes('760px')?width<=760:query.includes('1100px')?width<=1100:false
 const matchMedia=(query:string)=>{
  let record=queries.get(query)
  if(!record){record={matches:matches(query),listeners:new Set()};queries.set(query,record)}
  return {media:query,get matches(){return matches(query)},addEventListener:(_type:string,listener:(event:MediaQueryListEvent)=>void)=>{record!.listeners.add(listener)},removeEventListener:(_type:string,listener:(event:MediaQueryListEvent)=>void)=>{record!.listeners.delete(listener)}} as unknown as MediaQueryList
 }
 vi.stubGlobal('matchMedia',matchMedia)
 return (nextWidth:number)=>{
  const before=new Map([...queries.keys()].map(query=>[query,matches(query)]))
  act(()=>{width=nextWidth;for(const [query,record] of queries){const next=matches(query);if(before.get(query)!==next){record.matches=next;for(const listener of record.listeners)listener({matches:next,media:query} as MediaQueryListEvent)}}})
 }
}

it('keeps a previously visited terminal mounted through another session and a page change',async()=>{
 signedIn()
 const second={...controlledSession,id:'session-2',name:'Implementation B',terminal_id:'terminal-2'}
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession,second],spaces:[space]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('tab',{name:'Implementation A'}))
 const first=await screen.findByTestId('terminal-connection')
 fireEvent.click(screen.getByRole('tab',{name:'Implementation B'}))
 await waitFor(()=>expect(terminalMounts.count).toBe(2))
 fireEvent.click(screen.getByRole('tab',{name:'Implementation A'}))
 expect(terminalMounts.count).toBe(2)
 expect(first.isConnected).toBe(true)
 expect(first.getAttribute('data-active')).toBe('true')
 fireEvent.click(screen.getByRole('button',{name:'Tasks'}))
 expect(first.isConnected).toBe(true)
 expect(first.getAttribute('data-active')).toBe('false')
})

it('evicts the least recent terminal after a fourth visit across Spaces',async()=>{
 signedIn()
 const items=['A','B','C','D'].map((label,index)=>({...controlledSession,id:`session-${label}`,name:label,terminal_id:`terminal-${label}`,workspace_id:index%2?'workspace-2':'workspace-1'}))
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items,spaces:[space,{...space,id:'space-2',name:'another',workspace_id:'workspace-2'}]}:{items:[]})
 render(<App/>)
 await screen.findByRole('tab',{name:'A'})
 for(const [index,label] of ['A','B','C','D'].entries()){fireEvent.click(screen.getByRole('tab',{name:label}));await waitFor(()=>expect(terminalMounts.count).toBe(index+1))}
 await waitFor(()=>expect(terminalMounts.count).toBe(4))
 expect(terminalMounts.disposes).toBe(1)
 expect(screen.getAllByTestId('terminal-connection')).toHaveLength(3)
})

it('clears visited terminals at logout start even when logout fails',async()=>{
 signedIn()
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession],spaces:[space]}:{items:[]})
 vi.spyOn(api,'logout').mockRejectedValue(new ApiError('unavailable',503))
 render(<App/>)
 fireEvent.click(await screen.findByRole('tab',{name:'Implementation A'}))
 expect(await screen.findByTestId('terminal-connection')).toBeTruthy()
 fireEvent.click(screen.getByTitle('Sign out'))
 await waitFor(()=>expect(terminalMounts.disposes).toBe(1))
 expect(screen.queryByTestId('terminal-connection')).toBeNull()
 expect(screen.getByRole('tab',{name:'Implementation A'})).toBeTruthy()
 expect(screen.getByRole('alert').textContent).toContain('unavailable')
})
it('disposes a retained terminal when fresh inventory changes its process identity or ends it',async()=>{
 let publish:(event:MessageEvent)=>void=()=>{}
 signedIn(handler=>{publish=handler})
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession],spaces:[space]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('tab',{name:'Implementation A'}))
 await screen.findByTestId('terminal-connection')
 act(()=>publish({data:JSON.stringify({items:[{...controlledSession,pid:124}],spaces:[space],stale:false})} as MessageEvent))
 await waitFor(()=>expect(terminalMounts.disposes).toBe(1))
 expect(screen.queryByTestId('terminal-connection')).toBeNull()
 fireEvent.click(screen.getByRole('tab',{name:'Implementation A'}))
 await waitFor(()=>expect(terminalMounts.count).toBe(2))
 act(()=>publish({data:JSON.stringify({items:[{...controlledSession,pid:124,alive:false,capabilities:[]}],spaces:[space],stale:false})} as MessageEvent))
 await waitFor(()=>expect(terminalMounts.disposes).toBe(2))
})
it('retains the global cache while switching Spaces and activates only the selected panel',async()=>{
 signedIn()
 const other={...controlledSession,id:'session-other',name:'Another agent',workspace_id:'workspace-2',terminal_id:'terminal-2'}
 const otherSpace={...space,id:'space-2',name:'another',workspace_id:'workspace-2'}
 vi.spyOn(api,'list').mockImplementation(async name=>name==='sessions'?{items:[controlledSession,other],spaces:[space,otherSpace]}:{items:[]})
 render(<App/>)
 fireEvent.click(await screen.findByRole('tab',{name:'Implementation A'}))
 const first=await screen.findByTestId('terminal-connection')
 fireEvent.click(screen.getByRole('button',{name:'another'}))
 expect(first.getAttribute('data-active')).toBe('false')
 fireEvent.click(screen.getByRole('tab',{name:'Another agent'}))
 await waitFor(()=>expect(terminalMounts.count).toBe(2))
 expect(first.isConnected).toBe(true)
 fireEvent.click(screen.getByRole('button',{name:'demo'}))
 fireEvent.click(screen.getByRole('tab',{name:'Implementation A'}))
 expect(terminalMounts.count).toBe(2)
 expect(first.getAttribute('data-active')).toBe('true')
})
