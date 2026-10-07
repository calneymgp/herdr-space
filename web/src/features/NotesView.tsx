import {useEffect,useId,useRef,useState,type KeyboardEvent} from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {Download,FilePlus2,Search,Save,Trash2} from 'lucide-react'
import {Button} from '../components/ui/button'
import {ScrollCue} from '../components/ScrollCue'
import {Input,Textarea} from '../components/ui/input'
import {api} from '../lib/api'
import {apiErrorMessage,isUnauthorized} from '../lib/apiErrorMessage'
import {NOTE_TITLE_MAX_BYTES,validateNoteText} from '../lib/noteText'
import type {Note,Project,Space} from '../lib/state'
import {beginDraft,editDraft} from '../lib/state'
import {rebaseDraft} from '../lib/noteDrafts'
import {spaceSelectionForProject} from '../lib/spaceSelection'
import type {useNoteDrafts} from '../lib/useNoteDrafts'
const safeUrl=(url:string)=>/^(https?:|mailto:|\/|#)/i.test(url)?url:''
const filename=(title:string)=>((title||'note').normalize('NFKD').replace(/[^a-zA-Z0-9-_ ]/g,'').trim().replace(/\s+/g,'-')||'note')+'.md'
export function NotesView({items,projects,spaces,reload,draftStore,createRequested=false,onCreateHandled,showCreateAction=true}: {items:Note[];projects:Project[];spaces:Space[];reload:()=>Promise<void>;onError:(message:string)=>void;draftStore:ReturnType<typeof useNoteDrafts>;createRequested?:boolean;onCreateHandled?:()=>void;showCreateAction?:boolean}){
 const [selected,setSelected]=useState(''),[focusedTab,setFocusedTab]=useState(''),[focusedModeTab,setFocusedModeTab]=useState(''),[preview,setPreview]=useState(false),[busy,setBusy]=useState(false),[spaceBusy,setSpaceBusy]=useState(false),[spaceError,setSpaceError]=useState(''),[actionError,setActionError]=useState(''),[query,setQuery]=useState(''),[focusNew,setFocusNew]=useState(false),[compactIndex,setCompactIndex]=useState(()=>typeof window!=='undefined'&&typeof window.matchMedia==='function'&&window.matchMedia('(max-width: 1100px)').matches)
 const noteTabPrefix=useId(),noteTabs=useRef(new Map<string,HTMLButtonElement>()),modeTabs=useRef(new Map<string,HTMLButtonElement>())
 const createConsumed=useRef(false),pendingCreatedId=useRef(''),titleInput=useRef<HTMLInputElement>(null),spaceRequest=useRef(0),actionRequest=useRef(0),selectionGeneration=useRef(0),busyRef=useRef(false),mounted=useRef(true)
 const latestDrafts=useRef(draftStore.drafts),selectedNote=useRef(selected)
 latestDrafts.current=draftStore.drafts
 selectedNote.current=selected
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false}},[])
 useEffect(()=>{
  if(typeof window.matchMedia!=='function')return
  const query=window.matchMedia('(max-width: 1100px)'),update=()=>setCompactIndex(query.matches)
  update()
  query.addEventListener('change',update)
  return ()=>query.removeEventListener('change',update)
 },[])
 const note=items.find(n=>n.id===selected)
 const stored=draftStore.drafts[selected]
 const draft=stored||(note?beginDraft(note):null)
 const noteText=draft?validateNoteText(draft.title,draft.body):null
 useEffect(()=>{if(items.some(item=>item.id===pendingCreatedId.current))pendingCreatedId.current='';if(!items.some(item=>item.id===selected)&&(!selected||selected!==pendingCreatedId.current))setSelected(items[0]?.id||'')},[items,selected])
 useEffect(()=>{if(note&&stored?.status==='saved'&&note.version>stored.version)draftStore.setDraft(beginDraft(note))},[note,stored,draftStore])
 const set=draftStore.setDraft
 const save=draftStore.save
 const changeSpace=async(value:string)=>{
  if(!draft||spaceBusy)return
  const noteId=draft.id,request=++spaceRequest.current
  setSpaceError('');setSpaceBusy(true)
  try{
   const projectId=value.startsWith('space:')?(await api.spaceProject(value.slice(6))).item.id:value.startsWith('legacy:')?value.slice(7):''
   if(!mounted.current||request!==spaceRequest.current||selectedNote.current!==noteId)return
   const current=latestDrafts.current[noteId]||draft
   set(editDraft(current,{project_id:projectId}))
  }catch(error){if(mounted.current&&request===spaceRequest.current&&selectedNote.current===noteId&&!isUnauthorized(error))setSpaceError(apiErrorMessage(error,'Could not link this note to the Space.'))}
  finally{if(mounted.current&&request===spaceRequest.current)setSpaceBusy(false)}
 }
 const create=async()=>{if(busyRef.current)return;busyRef.current=true;setBusy(true);setActionError('');const request=++actionRequest.current,selection=selectionGeneration.current;try{const {item}=await api.create<Note>('notes',{title:'Untitled',body:'',project_id:''});if(!mounted.current||request!==actionRequest.current)return;if(selection===selectionGeneration.current){pendingCreatedId.current=item.id;set(beginDraft(item));setSelected(item.id);setPreview(false);setFocusNew(true)}try{await reload()}catch{if(mounted.current&&request===actionRequest.current&&selection===selectionGeneration.current)setActionError('The note was created, but the list could not be refreshed. Refresh the data to check.')}}catch(e){if(mounted.current&&request===actionRequest.current&&selection===selectionGeneration.current&&!isUnauthorized(e))setActionError(apiErrorMessage(e,'Could not create the note. Please try again.'))}finally{if(mounted.current&&request===actionRequest.current){busyRef.current=false;setBusy(false)}}}
 useEffect(()=>{if(!createRequested){createConsumed.current=false;return}if(createConsumed.current)return;createConsumed.current=true;onCreateHandled?.();void create()},[createRequested,onCreateHandled])
 useEffect(()=>{if(focusNew&&draft){titleInput.current?.focus();titleInput.current?.select();setFocusNew(false)}},[focusNew,draft])
 const visibleItems=items.filter(item=>{const text=((draftStore.drafts[item.id]?.title??item.title)+' '+(draftStore.drafts[item.id]?.body??item.body)).toLocaleLowerCase('en-US');return text.includes(query.trim().toLocaleLowerCase('en-US'))})
 const noteTabId=(id:string)=>`${noteTabPrefix}-tab-${encodeURIComponent(id)}`
 const notePanelId=`${noteTabPrefix}-panel`
 const modeTabId=(mode:'writing'|'preview')=>`${noteTabPrefix}-mode-${mode}`
 const modePanelId=`${noteTabPrefix}-mode-panel`
 const hasVisibleTabs=visibleItems.length>0
 const visibleSelected=visibleItems.some(item=>item.id===selected)
 const rovingTabId=visibleItems.some(item=>item.id===focusedTab)?focusedTab:visibleSelected?selected:visibleItems[0]?.id||''
 const activeModeTab=preview?'preview':'writing'
 const rovingModeTab=focusedModeTab==='writing'||focusedModeTab==='preview'?focusedModeTab:activeModeTab
 const focusNoteTab=(id:string)=>{setFocusedTab(id);noteTabs.current.get(id)?.focus()}
 const activateMode=(mode:'writing'|'preview')=>{setFocusedModeTab(mode);setPreview(mode==='preview')}
 const focusModeTab=(mode:'writing'|'preview')=>{setFocusedModeTab(mode);modeTabs.current.get(mode)?.focus()}
 const handleNoteTabKeyDown=(event:KeyboardEvent<HTMLButtonElement>,id:string)=>{
  if(event.key==='Enter'||event.key===' '){event.preventDefault();activateNote(id);return}
  const index=visibleItems.findIndex(item=>item.id===id)
  if(index<0||visibleItems.length===0)return
  let nextIndex:number
  switch(event.key){
   case 'ArrowDown':case 'ArrowRight':nextIndex=(index+1)%visibleItems.length;break
   case 'ArrowUp':case 'ArrowLeft':nextIndex=(index-1+visibleItems.length)%visibleItems.length;break
   case 'Home':nextIndex=0;break
   case 'End':nextIndex=visibleItems.length-1;break
   default:return
  }
  event.preventDefault()
  focusNoteTab(visibleItems[nextIndex].id)
 }
 const handleModeTabKeyDown=(event:KeyboardEvent<HTMLButtonElement>,mode:'writing'|'preview')=>{
  if(event.key==='Enter'||event.key===' '){event.preventDefault();activateMode(mode);return}
  let next:'writing'|'preview'
  switch(event.key){
   case 'ArrowDown':case 'ArrowRight':next=mode==='writing'?'preview':'writing';break
   case 'ArrowUp':case 'ArrowLeft':next=mode==='writing'?'preview':'writing';break
   case 'Home':next='writing';break
   case 'End':next='preview';break
   default:return
  }
  event.preventDefault()
  focusModeTab(next)
 }
 const activateNote=(id:string)=>{selectionGeneration.current++;spaceRequest.current++;setSpaceBusy(false);setSelected(id);setFocusedTab(id);setPreview(false);setFocusedModeTab('');setSpaceError('');setActionError('')}
 const panelLabel=draft?.title.trim()||'Untitled'
 const remove=async()=>{if(busyRef.current||!draft||!window.confirm('Delete this note? This action cannot be undone.'))return;busyRef.current=true;setBusy(true);setActionError('');const request=++actionRequest.current,selection=selectionGeneration.current,noteId=draft.id;try{await api.remove('notes',noteId);if(!mounted.current||request!==actionRequest.current)return;draftStore.discard(noteId);if(selection===selectionGeneration.current&&selectedNote.current===noteId)setSelected('');try{await reload()}catch{if(mounted.current&&request===actionRequest.current&&selection===selectionGeneration.current)setActionError('The note was deleted, but the list could not be refreshed. Refresh the data to check.')}}catch(e){if(mounted.current&&request===actionRequest.current&&selection===selectionGeneration.current&&!isUnauthorized(e))setActionError(apiErrorMessage(e,'Could not delete the note. Please try again.'))}finally{if(mounted.current&&request===actionRequest.current){busyRef.current=false;setBusy(false)}}}
 const exportNote=()=>{if(!draft)return;const blob=new Blob([`# ${draft.title||'Untitled'}\n\n${draft.body}\n`],{type:'text/markdown;charset=utf-8'});const url=URL.createObjectURL(blob);const a=document.createElement('a');a.href=url;a.download=filename(draft.title);a.click();URL.revokeObjectURL(url)}
 return <div className="notes-layout"><aside className="notes-index"><div className="section-bar"><h2>Notes index</h2>{showCreateAction&&<Button size="icon" aria-label="New note" onClick={create} disabled={busy}><FilePlus2 size={18}/></Button>}</div><label className="notes-search"><Search size={16} aria-hidden="true"/><input type="search" aria-label="Search notes" placeholder="Search notes" value={query} onChange={e=>setQuery(e.target.value)}/></label><ScrollCue className="note-list" role={hasVisibleTabs?'tablist':undefined} aria-label={hasVisibleTabs?'Notes index':undefined} aria-orientation={hasVisibleTabs?(compactIndex?'horizontal':'vertical'):undefined}>{items.length===0&&<p className="empty-small">No notes yet.</p>}{items.length>0&&visibleItems.length===0&&<p className="empty-small">No notes found.</p>}{visibleItems.map(item=><button type="button" role="tab" id={noteTabId(item.id)} aria-controls={notePanelId} aria-selected={selected===item.id} tabIndex={rovingTabId===item.id?0:-1} key={item.id} ref={element=>{if(element)noteTabs.current.set(item.id,element);else noteTabs.current.delete(item.id)}} className={'note-link '+(selected===item.id?'active':'')} onFocus={()=>setFocusedTab(item.id)} onKeyDown={event=>handleNoteTabKeyDown(event,item.id)} onClick={()=>activateNote(item.id)}><strong>{(draftStore.drafts[item.id]?.title??item.title)||'Untitled'}</strong><span>{(draftStore.drafts[item.id]?.body??item.body).slice(0,65)||'Empty note'}</span>{draftStore.drafts[item.id]&&draftStore.drafts[item.id].status!=='saved'&&<em className={'draft-indicator draft-indicator--'+draftStore.drafts[item.id].status}>{draftStore.drafts[item.id].status==='conflict'?'Conflict':draftStore.drafts[item.id].status==='error'?'Not saved':draftStore.drafts[item.id].status==='saving'?'Saving':'Pending'}</em>}<small>{new Date(item.updated_at).toLocaleDateString('en-US')}</small></button>)}</ScrollCue></aside>
  <section className="note-editor" id={draft||items.length>0?notePanelId:undefined} role={draft||items.length>0?'tabpanel':undefined} aria-labelledby={visibleSelected?noteTabId(selected):undefined} aria-label={visibleSelected?undefined:panelLabel}>{draft?<><div className="editor-head"><label className="field"><span>Title</span><Input ref={titleInput} className="note-title-input" aria-label="Title" value={draft.title} onChange={e=>set(editDraft(draft,{title:e.target.value}))} maxLength={200} aria-invalid={!!noteText?.titleError} aria-describedby="note-title-hint" placeholder="Untitled"/><small id="note-title-hint" className={noteText?.titleError?"form-error":"hint"}>{noteText?.titleError||`${noteText?.titleBytes} / ${NOTE_TITLE_MAX_BYTES} bytes UTF-8`}</small></label><div className="editor-actions"><span className={'save-state save-state--'+draft.status} role="status">{draft.status==='saved'?'Saved':draft.status==='dirty'?'Unsaved changes':draft.status==='saving'?'Saving…':draft.status==='conflict'?'Version conflict':'Save error'}</span><Button variant="outline" size="sm" onClick={exportNote} aria-label="Export Markdown note"><Download size={15}/> Export</Button><Button variant="ghost" size="icon" onClick={remove} disabled={busy} aria-label="Delete note"><Trash2 size={17}/></Button></div></div>
   {actionError&&<p className="form-error" role="alert">{actionError}</p>}{draft.errorMessage&&<p className="form-error" role="alert">{draft.errorMessage}</p>}
   {draft.status==='conflict'&&draft.current&&<div className="conflict-panel" role="alert"><strong>This note changed elsewhere.</strong><p>Your draft is preserved. Choose how to reconcile it with version {draft.current.version}.</p><div className="inline-actions"><Button size="sm" onClick={()=>{const current=draft.current!;set({...draft,body:`${current.body}\n\n---\n\n${draft.body}`,version:current.version,revision:draft.revision+1,epoch:draft.epoch+1,current:undefined,status:'dirty'})}}>Merge text</Button><Button size="sm" variant="outline" onClick={()=>set({...draft,version:draft.current!.version,revision:draft.revision+1,epoch:draft.epoch+1,current:undefined,status:'dirty'})}>Use my draft</Button><Button size="sm" variant="ghost" onClick={()=>set(rebaseDraft(draft,draft.current!))}>Load current version</Button></div></div>}
   <div className="note-editor-toolbar"><label className="field"><span>Space</span><select className="ui-input" aria-label="Note Space" value={spaceSelectionForProject(draft.project_id,projects,spaces)} onChange={event=>void changeSpace(event.target.value)} disabled={spaceBusy}><option value="">No Space</option>{spaceSelectionForProject(draft.project_id,projects,spaces).startsWith('legacy:')&&<option value={spaceSelectionForProject(draft.project_id,projects,spaces)}>Previous association{projects.find(project=>project.id===draft.project_id)?` · ${projects.find(project=>project.id===draft.project_id)?.name}`:''}</option>}{spaces.map(space=><option key={space.id} value={'space:'+space.id}>{space.name}</option>)}</select></label>{spaceError&&<p className="form-error" role="alert">{spaceError}</p>}<div className="note-editor-controls"><div className="editor-tabs" role="tablist" aria-label="Note view" aria-orientation="horizontal"><button type="button" role="tab" id={modeTabId('writing')} aria-controls={modePanelId} aria-selected={!preview} tabIndex={rovingModeTab==='writing'?0:-1} ref={element=>{if(element)modeTabs.current.set('writing',element);else modeTabs.current.delete('writing')}} onFocus={()=>setFocusedModeTab('writing')} onKeyDown={event=>handleModeTabKeyDown(event,'writing')} onClick={()=>activateMode('writing')}>Write</button><button type="button" role="tab" id={modeTabId('preview')} aria-controls={modePanelId} aria-selected={preview} tabIndex={rovingModeTab==='preview'?0:-1} ref={element=>{if(element)modeTabs.current.set('preview',element);else modeTabs.current.delete('preview')}} onFocus={()=>setFocusedModeTab('preview')} onKeyDown={event=>handleModeTabKeyDown(event,'preview')} onClick={()=>activateMode('preview')}>Preview</button></div><Button variant="ghost" size="sm" onClick={()=>save(draft)} disabled={draft.status==='saved'||draft.status==='saving'||draft.status==='conflict'}><Save size={15}/> Save now</Button></div></div>
   <div className="note-mode-panel" id={modePanelId} role="tabpanel" aria-labelledby={modeTabId(activeModeTab)} tabIndex={preview?0:undefined}>{preview?<div className="markdown-body"><ReactMarkdown remarkPlugins={[remarkGfm]} urlTransform={safeUrl} components={{a:({href,children})=><a href={href} target="_blank" rel="noopener noreferrer">{children}</a>}}>{draft.body||'*No content yet.*'}</ReactMarkdown></div>:<Textarea className="note-textarea" aria-label="Note content in Markdown" aria-invalid={!!noteText?.bodyError} aria-describedby={noteText?.bodyError?"note-body-hint":undefined} value={draft.body} onChange={e=>set(editDraft(draft,{body:e.target.value}))} placeholder="Write your note in Markdown…"/>}{noteText?.bodyError&&<p id="note-body-hint" className="form-error" role="alert">{noteText.bodyError}</p>}</div>
  </>:<div className="empty-state">{actionError&&<p className="form-error" role="alert">{actionError}</p>}<FilePlus2 size={32}/><h2>No notes yet</h2><Button onClick={create} disabled={busy}>Create note</Button></div>}</section></div>
}
