import {act,cleanup,fireEvent,render,screen,within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {afterEach,describe,expect,it,vi} from 'vitest'
import {StrictMode,useState} from 'react'
import {NotesView} from './NotesView'
import {useNoteDrafts} from '../lib/useNoteDrafts'
import {api} from '../lib/api'
import type {Note,Project,Space} from '../lib/state'
const notes:Note[]=[
 {id:'A',title:'Note A',body:'Server A',project_id:'',version:1,created_at:'2026-01-01T00:00:00Z',updated_at:'2026-01-01T00:00:00Z'},
 {id:'B',title:'Note B',body:'Server B',project_id:'',version:7,created_at:'2026-01-01T00:00:00Z',updated_at:'2026-01-01T00:00:00Z'},
]
function Harness(){const [visible,setVisible]=useState(true);const draftStore=useNoteDrafts(async()=>{},()=>{},true);return <><button onClick={()=>setVisible(v=>!v)}>Switch page</button>{visible&&<NotesView items={notes} projects={[]} spaces={[]} reload={async()=>{}} onError={()=>{}} draftStore={draftStore}/>}</>}
afterEach(()=>{cleanup();vi.restoreAllMocks();vi.useRealTimers()})
describe('note navigation',()=>{
 it('keeps the selected note when a pending create finishes after navigation',async()=>{
  let resolveCreate!:(value:{item:Note})=>void
  vi.spyOn(api,'create').mockReturnValue(new Promise(resolve=>{resolveCreate=resolve}))
  render(<Harness/>)
  fireEvent.click(screen.getByRole('button',{name:'New note'}))
  fireEvent.click(screen.getByRole('tab',{name:/Note B/}))
  await act(async()=>resolveCreate({item:{...notes[0],id:'C',title:'Untitled'}}))
  expect((screen.getByRole('textbox',{name:'Title'}) as HTMLInputElement).value).toBe('Note B')
  expect(screen.getByRole('tab',{name:/Note B/}).getAttribute('aria-selected')).toBe('true')
 })
 it('does not show an old create failure in another note and distinguishes refresh failure after creation',async()=>{
  let rejectCreate!:(error:Error)=>void
  vi.spyOn(api,'create').mockReturnValueOnce(new Promise((_resolve,reject)=>{rejectCreate=reject})).mockResolvedValueOnce({item:{...notes[0],id:'C',title:'Untitled'}})
  function FailingRefresh(){const store=useNoteDrafts(async()=>{},()=>{},true);return <NotesView items={notes} projects={[]} spaces={[]} reload={async()=>{throw Error('refresh failed')}} onError={()=>{}} draftStore={store}/>}
  render(<FailingRefresh/>)
  fireEvent.click(screen.getByRole('button',{name:'New note'}))
  fireEvent.click(screen.getByRole('tab',{name:/Note B/}))
  await act(async()=>rejectCreate(Error('old failure')))
  expect(screen.queryByRole('alert')).toBeNull()
  fireEvent.click(screen.getByRole('button',{name:'New note'}))
  expect((await screen.findByRole('alert')).textContent).toContain('The note was created, but the list could not be refreshed')
  expect(screen.queryByText('Could not create the note')).toBeNull()
 })
 it('does not reload or change drafts when create resolves in the same turn as unmount',async()=>{
  let resolveCreate!:(value:{item:Note})=>void
  vi.spyOn(api,'create').mockReturnValue(new Promise(resolve=>{resolveCreate=resolve}))
  const reload=vi.fn(async()=>{}),setDraft=vi.fn()
  function Leaving(){const [visible,setVisible]=useState(true);const store=useNoteDrafts(async()=>{},()=>{},true);return <><button onClick={()=>setVisible(false)}>Leave Notes</button>{visible&&<NotesView items={notes} projects={[]} spaces={[]} reload={reload} onError={()=>{}} draftStore={{...store,setDraft}}/>}</>}
  render(<Leaving/>)
  fireEvent.click(screen.getByRole('button',{name:'New note'}))
  await act(async()=>{resolveCreate({item:{...notes[0],id:'C'}});fireEvent.click(screen.getByRole('button',{name:'Leave Notes'}));await Promise.resolve()})
  expect(reload).not.toHaveBeenCalled()
  expect(setDraft).not.toHaveBeenCalled()
 })
 it('does not edit a note when Space resolution finishes after unmount',async()=>{
  let resolveSpace!:(value:{item:Project})=>void
  vi.spyOn(api,'spaceProject').mockReturnValue(new Promise(resolve=>{resolveSpace=resolve}))
  const setDraft=vi.fn()
  const space:Space={id:'space-1',name:'Work',path:'/work/project',server_id:'server',workspace_id:'workspace'}
  function Leaving(){const [visible,setVisible]=useState(true);const store=useNoteDrafts(async()=>{},()=>{},true);return <><button onClick={()=>setVisible(false)}>Leave Notes</button>{visible&&<NotesView items={notes} projects={[]} spaces={[space]} reload={async()=>{}} onError={()=>{}} draftStore={{...store,setDraft}}/>}</>}
  render(<Leaving/>)
  fireEvent.change(screen.getByRole('combobox',{name:'Note Space'}),{target:{value:'space:space-1'}})
  await act(async()=>{resolveSpace({item:{id:'project-1',name:'Work',path:'/work/project',created_at:'2026-01-01T00:00:00Z'}});fireEvent.click(screen.getByRole('button',{name:'Leave Notes'}));await Promise.resolve()})
  expect(setDraft).not.toHaveBeenCalled()
 })
 it('keeps the new editor after an old note deletion completes',async()=>{
  let resolveDelete!:()=>void
  vi.spyOn(api,'remove').mockReturnValue(new Promise(resolve=>{resolveDelete=()=>resolve(undefined)}))
  vi.spyOn(window,'confirm').mockReturnValue(true)
  render(<Harness/>)
  fireEvent.click(screen.getByRole('button',{name:'Delete note'}))
  fireEvent.click(screen.getByRole('tab',{name:/Note B/}))
  await act(async()=>resolveDelete())
  expect((screen.getByRole('textbox',{name:'Title'}) as HTMLInputElement).value).toBe('Note B')
  expect(screen.queryByRole('alert')).toBeNull()
 })
 it('moves roving focus between notes without activating or saving a draft',async()=>{
  const user=userEvent.setup()
  const update=vi.spyOn(api,'update')
  render(<Harness/>)
  const tabA=screen.getByRole('tab',{name:/Note A/})
  const tabB=screen.getByRole('tab',{name:/Note B/})
  fireEvent.click(tabA)
  fireEvent.change(screen.getByRole('textbox',{name:'Note content in Markdown'}),{target:{value:'Draft A'}})
  tabA.focus()
  await user.keyboard('{ArrowDown}')
  expect(document.activeElement).toBe(tabB)
  expect(tabA.getAttribute('aria-selected')).toBe('true')
  expect(tabB.getAttribute('aria-selected')).toBe('false')
  expect(tabA.tabIndex).toBe(-1)
  expect(tabB.tabIndex).toBe(0)
  expect((screen.getByRole('textbox',{name:'Note content in Markdown'}) as HTMLTextAreaElement).value).toBe('Draft A')
  expect(update).not.toHaveBeenCalled()
  await user.keyboard('{Home}')
  expect(document.activeElement).toBe(tabA)
  await user.keyboard('{End}')
  expect(document.activeElement).toBe(tabB)
  expect(tabA.getAttribute('aria-selected')).toBe('true')
 })

 it('activates note tabs with Enter and Space while preserving edits and preview on focus movement',async()=>{
  const user=userEvent.setup()
  const update=vi.spyOn(api,'update')
  render(<Harness/>)
  const tabA=screen.getByRole('tab',{name:/Note A/})
  const tabB=screen.getByRole('tab',{name:/Note B/})
  fireEvent.click(tabA)
  fireEvent.change(screen.getByRole('textbox',{name:'Note content in Markdown'}),{target:{value:'Draft A preservado'}})
  fireEvent.click(screen.getByRole('tab',{name:'Preview'}))
  expect(within(screen.getByRole('tabpanel',{name:'Preview'})).getByText('Draft A preservado')).toBeTruthy()
  tabA.focus()
  await user.keyboard('{ArrowRight}')
  expect(document.activeElement).toBe(tabB)
  expect(tabA.getAttribute('aria-selected')).toBe('true')
  expect(within(screen.getByRole('tabpanel',{name:'Preview'})).getByText('Draft A preservado')).toBeTruthy()
  const spaceClick=vi.fn()
  tabB.addEventListener('click',spaceClick)
  await user.keyboard(' ')
  expect(spaceClick).not.toHaveBeenCalled()
  expect(tabB.getAttribute('aria-selected')).toBe('true')
  expect(document.activeElement).toBe(tabB)
  expect((screen.getByRole('textbox',{name:'Note content in Markdown'}) as HTMLTextAreaElement).value).toBe('Server B')
  const enterClick=vi.fn()
  tabA.addEventListener('click',enterClick)
  tabA.focus()
  await user.keyboard('{Enter}')
  expect(enterClick).not.toHaveBeenCalled()
  expect(tabA.getAttribute('aria-selected')).toBe('true')
  expect((screen.getByRole('textbox',{name:'Note content in Markdown'}) as HTMLTextAreaElement).value).toBe('Draft A preservado')
  expect(update).not.toHaveBeenCalled()
 })

 it('moves focus across writing and preview tabs without changing mode and links each mode to its panel',async()=>{
  const user=userEvent.setup()
  render(<Harness/>)
  const modeTabs=within(screen.getByRole('tablist',{name:'Note view'}))
  const writing=modeTabs.getByRole('tab',{name:'Write'})
  const preview=modeTabs.getByRole('tab',{name:'Preview'})
  expect(modeTabs.queryByRole('button',{name:'Save now'})).toBeNull()
  expect(screen.getByRole('button',{name:'Save now'})).toBeTruthy()
  fireEvent.change(screen.getByRole('textbox',{name:'Note content in Markdown'}),{target:{value:'Draft preserved between modes'}})
  fireEvent.click(preview)
  const previewPanel=screen.getByRole('tabpanel',{name:'Preview'})
  expect(preview.getAttribute('aria-controls')).toBe(previewPanel.id)
  expect(previewPanel.getAttribute('aria-labelledby')).toBe(preview.id)
  preview.focus()
  await user.keyboard('{ArrowLeft}')
  expect(document.activeElement).toBe(writing)
  expect(writing.getAttribute('aria-selected')).toBe('false')
  expect(preview.getAttribute('aria-selected')).toBe('true')
  expect(within(previewPanel).getByText('Draft preserved between modes')).toBeTruthy()
  const keyboardClick=vi.fn()
  writing.addEventListener('click',keyboardClick)
  await user.keyboard(' ')
  expect(keyboardClick).not.toHaveBeenCalled()
  expect(writing.getAttribute('aria-selected')).toBe('true')
  expect(document.activeElement).toBe(writing)
  expect((screen.getByRole('textbox',{name:'Note content in Markdown'}) as HTMLTextAreaElement).value).toBe('Draft preserved between modes')
  const writingPanel=screen.getByRole('tabpanel',{name:'Write'})
  expect(writingPanel.getAttribute('aria-labelledby')).toBe(writing.id)
  preview.focus()
  await user.keyboard('{Enter}')
  expect(preview.getAttribute('aria-selected')).toBe('true')
  expect(screen.getByRole('tabpanel',{name:'Preview'}).getAttribute('aria-labelledby')).toBe(preview.id)
  expect(within(screen.getByRole('tabpanel',{name:'Preview'})).getByText('Draft preserved between modes')).toBeTruthy()
 })

 it('keeps the editor accessibly named when search hides its selected tab without stealing search focus',async()=>{
  const user=userEvent.setup()
  render(<Harness/>)
  const search=screen.getByRole('searchbox',{name:'Search notes'})
  await user.type(search,'Note B')
  const tabB=screen.getByRole('tab',{name:/Note B/})
  const panel=screen.getByRole('tabpanel',{name:'Note A'})
  expect(document.activeElement).toBe(search)
  expect(screen.queryByRole('tab',{name:/Note A/})).toBeNull()
  expect(tabB.tabIndex).toBe(0)
  expect(panel.getAttribute('aria-labelledby')).toBeNull()
  expect(tabB.getAttribute('aria-controls')).toBe(panel.id)
  expect(panel.getAttribute('id')).not.toBe('')
 })

 it('keeps a named panel when the filter has no results and omits tabs when there are no notes',async()=>{
  const user=userEvent.setup()
  render(<Harness/>)
  const search=screen.getByRole('searchbox',{name:'Search notes'})
  await user.type(search,'no result')
  expect(screen.getByText('No notes found.')).toBeTruthy()
  expect(screen.queryByRole('tablist',{name:'Notes index'})).toBeNull()
  const panel=screen.getByRole('tabpanel',{name:'Note A'})
  expect(panel.getAttribute('aria-label')).toBe('Note A')
  expect(panel.getAttribute('aria-labelledby')).toBeNull()
  expect(document.activeElement).toBe(search)

  function EmptyNotes(){const store=useNoteDrafts(async()=>{},()=>{},true);return <NotesView items={[]} projects={[]} spaces={[]} reload={async()=>{}} onError={()=>{}} draftStore={store}/>}
  cleanup()
  render(<EmptyNotes/>)
  expect(screen.getByText('No notes yet.')).toBeTruthy()
  expect(screen.queryByRole('tablist',{name:'Notes index'})).toBeNull()
  expect(screen.queryByRole('tabpanel')).toBeNull()
 })

 it('opens existing notes in writing mode and filters the index',()=>{
  render(<Harness/>)
  expect(screen.getByRole('textbox',{name:'Note content in Markdown'})).toBeTruthy()
  const noteTabs=within(screen.getByRole('tablist',{name:'Notes index'}))
  const tabA=noteTabs.getByRole('tab',{name:/Note A/})
  const panel=screen.getByRole('tabpanel',{name:/Note A/})
  expect(tabA.getAttribute('aria-controls')).toBe(panel.id)
  expect(panel.getAttribute('aria-labelledby')).toBe(tabA.id)
  expect(tabA.tabIndex).toBe(0)
  fireEvent.change(screen.getByRole('searchbox',{name:'Search notes'}),{target:{value:'Note B'}})
  expect(noteTabs.queryByRole('tab',{name:/Note A/})).toBeNull()
  expect(noteTabs.getByRole('tab',{name:/Note B/})).toBeTruthy()
 })
 it('handles a global create request once in StrictMode and opens the editor',async()=>{
  const item:Note={...notes[0],id:'C',title:'Untitled',body:''}
  const create=vi.spyOn(api,'create').mockResolvedValue({item})
  const handled=vi.fn()
  function Requested(){const store=useNoteDrafts(async()=>{},()=>{},true);const [items,setItems]=useState(notes);return <NotesView items={items} projects={[]} spaces={[]} reload={async()=>setItems([...notes,item])} onError={()=>{}} draftStore={store} createRequested onCreateHandled={handled} showCreateAction={false}/>}
  render(<StrictMode><Requested/></StrictMode>)
  await screen.findByRole('textbox',{name:'Note content in Markdown'})
  await act(async()=>{await Promise.resolve()})
  expect(create).toHaveBeenCalledTimes(1)
  expect(handled).toHaveBeenCalledTimes(1)
  expect(screen.queryByRole('button',{name:'New note'})).toBeNull()
  expect(screen.getByRole('tab',{name:'Write'}).getAttribute('aria-selected')).toBe('true')
  expect((screen.getByRole('textbox',{name:'Title'}) as HTMLInputElement).value).toBe('Untitled')
  expect(document.activeElement).toBe(screen.getByRole('textbox',{name:'Title'}))
 })

 it('resolves a native Space only when a note association is selected and autosaves the internal reference',async()=>{
  vi.useFakeTimers()
  const space:Space={id:'space-1',name:'Work',path:'/work/project',server_id:'server',workspace_id:'workspace'}
  const project:Project={id:'project-1',name:'Work',path:'/work/project',created_at:'2026-01-01T00:00:00Z'}
  vi.spyOn(api,'spaceProject').mockResolvedValue({item:project})
  const update=vi.spyOn(api,'update').mockResolvedValue({item:{...notes[0],project_id:project.id,version:2}})
  function NativeAssociation(){const store=useNoteDrafts(async()=>{},()=>{},true);return <NotesView items={[notes[0]]} projects={[project]} spaces={[space]} reload={async()=>{}} onError={()=>{}} draftStore={store}/>}
  render(<NativeAssociation/>)
  fireEvent.change(screen.getByRole('combobox',{name:'Note Space'}),{target:{value:'space:space-1'}})
  await act(async()=>{await Promise.resolve();await Promise.resolve()})
  expect(api.spaceProject).toHaveBeenCalledWith('space-1')
  expect(screen.getByText('Unsaved changes')).toBeTruthy()
  await act(async()=>{vi.advanceTimersByTime(1000);await Promise.resolve()})
  expect(update).toHaveBeenCalledWith('notes','A',expect.objectContaining({project_id:'project-1'}))
 })

 it('preserves title and body edits made while Space resolution is pending',async()=>{
  const space:Space={id:'space-1',name:'Work',path:'/work/project',server_id:'server',workspace_id:'workspace'}
  const project:Project={id:'project-1',name:'Work',path:'/work/project',created_at:'2026-01-01T00:00:00Z'}
  let resolveProject!:(value:{item:Project})=>void
  vi.spyOn(api,'spaceProject').mockReturnValue(new Promise(resolve=>{resolveProject=resolve}))
  function PendingAssociation(){const store=useNoteDrafts(async()=>{},()=>{},true);return <NotesView items={[notes[0]]} projects={[project]} spaces={[space]} reload={async()=>{}} onError={()=>{}} draftStore={store}/>}
  render(<PendingAssociation/>)
  fireEvent.change(screen.getByRole('combobox',{name:'Note Space'}),{target:{value:'space:space-1'}})
  fireEvent.change(screen.getByRole('textbox',{name:'Title'}),{target:{value:'Title updated while waiting'}})
  fireEvent.change(screen.getByRole('textbox',{name:'Note content in Markdown'}),{target:{value:'Body updated while waiting'}})
  await act(async()=>{resolveProject({item:project});await Promise.resolve()})
  expect((screen.getByRole('textbox',{name:'Title'}) as HTMLInputElement).value).toBe('Title updated while waiting')
  expect((screen.getByRole('textbox',{name:'Note content in Markdown'}) as HTMLTextAreaElement).value).toBe('Body updated while waiting')
  expect((screen.getByRole('combobox',{name:'Note Space'}) as HTMLSelectElement).value).toBe('space:space-1')
 })

 it('does not apply a delayed Space selection after switching to another note',async()=>{
  const space:Space={id:'space-1',name:'Work',path:'/work/project',server_id:'server',workspace_id:'workspace'}
  const project:Project={id:'project-1',name:'Work',path:'/work/project',created_at:'2026-01-01T00:00:00Z'}
  let resolveProject!:(value:{item:Project})=>void
  vi.spyOn(api,'spaceProject').mockReturnValue(new Promise(resolve=>{resolveProject=resolve}))
  function PendingAssociation(){const store=useNoteDrafts(async()=>{},()=>{},true);return <NotesView items={notes} projects={[project]} spaces={[space]} reload={async()=>{}} onError={()=>{}} draftStore={store}/>}
  render(<PendingAssociation/>)
  fireEvent.change(screen.getByRole('combobox',{name:'Note Space'}),{target:{value:'space:space-1'}})
  fireEvent.click(screen.getByRole('tab',{name:/Note B/}))
  await act(async()=>{resolveProject({item:project});await Promise.resolve()})
  fireEvent.click(screen.getByRole('tab',{name:/Note A/}))
  expect((screen.getByRole('combobox',{name:'Note Space'}) as HTMLSelectElement).value).toBe('')
 })

 it('keeps an unmatched historical repository association as an explicit choice',()=>{
  const oldProject:Project={id:'old-project',name:'Old project',path:'/old/path',created_at:'2026-01-01T00:00:00Z'}
  const space:Space={id:'space-1',name:'New Space',path:'/new/path',server_id:'server',workspace_id:'workspace'}
  function HistoricalAssociation(){const store=useNoteDrafts(async()=>{},()=>{},true);return <NotesView items={[{...notes[0],project_id:oldProject.id}]} projects={[oldProject]} spaces={[space]} reload={async()=>{}} onError={()=>{}} draftStore={store}/>}
  render(<HistoricalAssociation/>)
  const choice=screen.getByRole('option',{name:'Previous association · Old project'}) as HTMLOptionElement
  expect(choice.value).toBe('legacy:old-project')
  expect((screen.getByRole('combobox',{name:'Note Space'}) as HTMLSelectElement).value).toBe('legacy:old-project')
 })
 it('keeps a failed draft after switching notes and unmounting the editor',async()=>{
  vi.spyOn(api,'update').mockRejectedValue(new Error('offline'))
  render(<Harness/>)
  fireEvent.click(screen.getByRole('tab',{name:'Write'}))
  fireEvent.change(screen.getByRole('textbox',{name:'Note content in Markdown'}),{target:{value:'Draft A'}})
  await act(async()=>{await new Promise(resolve=>setTimeout(resolve,1100))})
  expect(screen.getByText('Save error')).toBeTruthy()
  fireEvent.click(screen.getByRole('tab',{name:/Note B/}))
  fireEvent.click(screen.getByRole('button',{name:'Switch page'}))
  fireEvent.click(screen.getByRole('button',{name:'Switch page'}))
  fireEvent.click(screen.getByRole('tab',{name:'Write'}))
  expect((screen.getByRole('textbox',{name:'Note content in Markdown'}) as HTMLTextAreaElement).value).toBe('Draft A')
 })
 it('does not apply a delayed save for A to edited B',async()=>{
  let finish!:(value:{item:Note})=>void
  vi.spyOn(api,'update').mockImplementation(()=>new Promise(resolve=>{finish=resolve}) as ReturnType<typeof api.update>)
  render(<Harness/>)
  fireEvent.click(screen.getByRole('tab',{name:'Write'}))
  fireEvent.change(screen.getByRole('textbox',{name:'Note content in Markdown'}),{target:{value:'Draft A'}})
  await act(async()=>{await new Promise(resolve=>setTimeout(resolve,1100))})
  fireEvent.click(screen.getByRole('tab',{name:/Note B/}))
  fireEvent.change(screen.getByRole('textbox',{name:'Note content in Markdown'}),{target:{value:'Draft B'}})
  await act(async()=>{finish({item:{...notes[0],body:'Draft A',version:2}});await Promise.resolve()})
  expect((screen.getByRole('textbox',{name:'Note content in Markdown'}) as HTMLTextAreaElement).value).toBe('Draft B')
  expect(screen.getByText('Unsaved changes')).toBeTruthy()
 })

 it('shows a note-index cue only while more notes remain and leaves the selected editor alone',()=>{
  render(<Harness/>)
  const list=screen.getByRole('tablist',{name:'Notes index'})
  const selected=screen.getByRole('tab',{name:/Note A/})
  fireEvent.click(selected)
  const cue=list.parentElement!
  expect(cue.getAttribute('data-scroll-cue')).toBeNull()
  Object.defineProperties(list,{clientWidth:{configurable:true,value:120},scrollWidth:{configurable:true,value:360},scrollLeft:{configurable:true,writable:true,value:0}})
  fireEvent.scroll(list)
  expect(cue.getAttribute('data-scroll-cue')).toBe('right')
  list.scrollLeft=240
  fireEvent.scroll(list)
  expect(cue.getAttribute('data-scroll-cue')).toBe('left')
  expect(selected.getAttribute('aria-selected')).toBe('true')
  expect((screen.getByRole('textbox',{name:'Note content in Markdown'}) as HTMLTextAreaElement).value).toBe('Server A')
 })
 it('shows vertical note-index cues down then up and hides them when the list fits without moving the editor',()=>{
  render(<Harness/>)
  const list=screen.getByRole('tablist',{name:'Notes index'})
  const cue=list.parentElement!
  const selected=screen.getByRole('tab',{name:/Note A/})
  fireEvent.click(selected)
  const editor=screen.getByRole('textbox',{name:'Note content in Markdown'}) as HTMLTextAreaElement
  fireEvent.change(editor,{target:{value:'Draft vertical preservado'}})
  editor.focus()
  const selectedId=selected.id
  const preserveEditor=()=>{
   expect(document.getElementById(selectedId)?.getAttribute('aria-selected')).toBe('true')
   expect(editor.value).toBe('Draft vertical preservado')
   expect(document.activeElement).toBe(editor)
  }
  Object.defineProperties(list,{clientHeight:{configurable:true,value:120},scrollHeight:{configurable:true,value:360},scrollTop:{configurable:true,writable:true,value:0}})
  fireEvent.scroll(list)
  expect(cue.getAttribute('data-scroll-cue')).toBe('down')
  preserveEditor()
  list.scrollTop=240
  fireEvent.scroll(list)
  expect(cue.getAttribute('data-scroll-cue')).toBe('up')
  preserveEditor()
  Object.defineProperty(list,'scrollHeight',{configurable:true,value:120})
  list.scrollTop=0
  fireEvent.scroll(list)
  expect(cue.getAttribute('data-scroll-cue')).toBeNull()
  preserveEditor()
 })
})
