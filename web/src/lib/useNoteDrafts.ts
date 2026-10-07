import {useCallback,useEffect,useRef,useState} from 'react'
import {api,ApiError} from './api'
import type {Draft,Note} from './state'
import {discardDraft,putDraft,resolveFailure,resolveSave,type DraftStore} from './noteDrafts'
import {apiErrorMessage} from './apiErrorMessage'
import {validateNoteText} from './noteText'
export function useNoteDrafts(reload:()=>Promise<void>,_onError:(message:string)=>void,enabled:boolean){
 const [drafts,setDrafts]=useState<DraftStore>({})
 const [settled,setSettled]=useState(0)
 const enabledRef=useRef(enabled),draftsRef=useRef(drafts),generation=useRef(0),noteGeneration=useRef(new Map<string,number>()),alive=useRef(true)
 const inFlight=useRef(new Set<string>()),blocked=useRef(new Set<string>()),timers=useRef(new Map<string,{revision:number;epoch:number;version:number;timer:number}>())
 if(enabledRef.current!==enabled){generation.current++;enabledRef.current=enabled;blocked.current.clear();inFlight.current.clear()}
 draftsRef.current=drafts
 enabledRef.current=enabled
 const setDraft=useCallback((draft:Draft)=>{if(enabledRef.current&&alive.current)setDrafts(current=>putDraft(current,draft))},[])
 const discard=useCallback((id:string)=>{noteGeneration.current.set(id,(noteGeneration.current.get(id)||0)+1);blocked.current.delete(id);const pending=timers.current.get(id);if(pending){window.clearTimeout(pending.timer);timers.current.delete(id)}setDrafts(current=>discardDraft(current,id))},[])
 const save=useCallback(async(snapshot:Draft)=>{
  if(!enabledRef.current||!alive.current||snapshot.status==='conflict'||snapshot.status==='saved')return
  if(inFlight.current.has(snapshot.id)){blocked.current.add(snapshot.id);return}
  const active=draftsRef.current[snapshot.id]
  if(!active||active.epoch!==snapshot.epoch||active.revision!==snapshot.revision||active.version!==snapshot.version)return
  const validation=validateNoteText(snapshot.title,snapshot.body)
  if(!validation.valid){setDrafts(current=>resolveFailure(current,snapshot,{},validation.titleError||validation.bodyError));return}
  const requestGeneration=generation.current,requestNoteGeneration=noteGeneration.current.get(snapshot.id)||0
  const currentRequest=()=>alive.current&&enabledRef.current&&generation.current===requestGeneration&&(noteGeneration.current.get(snapshot.id)||0)===requestNoteGeneration
  inFlight.current.add(snapshot.id)
  setDrafts(current=>current[snapshot.id]?.epoch===snapshot.epoch?putDraft(current,{...current[snapshot.id],status:'saving'}):current)
  try{
   let saved:Note
   try{saved=(await api.update<Note>('notes',snapshot.id,{title:snapshot.title,body:snapshot.body,project_id:snapshot.project_id,version:snapshot.version})).item}
   catch(error){if(currentRequest()){blocked.current.delete(snapshot.id);setDrafts(current=>resolveFailure(current,snapshot,error instanceof ApiError?error:{},apiErrorMessage(error,'Could not save the note. Please try again.')))}return}
   if(!currentRequest())return
   setDrafts(current=>resolveSave(current,snapshot,saved))
   try{await reload()}catch{
    if(currentRequest())setDrafts(current=>{const draft=current[snapshot.id];return draft?.version===saved.version?putDraft(current,{...draft,errorMessage:'The note was saved, but the list could not be refreshed. Refresh the data to check.'}):current})
   }
  }finally{if(alive.current&&enabledRef.current&&generation.current===requestGeneration){inFlight.current.delete(snapshot.id);if(blocked.current.has(snapshot.id))setSettled(value=>value+1)}}
 },[reload])
 useEffect(()=>{
  if(!enabled)return
  for(const [id,pending] of timers.current){const current=drafts[id];if(!current||current.status!=='dirty'||current.revision!==pending.revision||current.epoch!==pending.epoch||current.version!==pending.version){window.clearTimeout(pending.timer);timers.current.delete(id)}}
  for(const draft of Object.values(drafts)){if(draft.status==='dirty'&&!timers.current.has(draft.id)){const delay=blocked.current.has(draft.id)&&!inFlight.current.has(draft.id)?0:1000;if(delay===0)blocked.current.delete(draft.id);const timer=window.setTimeout(()=>{timers.current.delete(draft.id);void save(draft)},delay);timers.current.set(draft.id,{revision:draft.revision,epoch:draft.epoch,version:draft.version,timer})}}
 },[drafts,enabled,save,settled])
 useEffect(()=>{if(!enabled){for(const pending of timers.current.values())window.clearTimeout(pending.timer);timers.current.clear();blocked.current.clear();setDrafts({})}},[enabled])
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;generation.current++;for(const pending of timers.current.values())window.clearTimeout(pending.timer);timers.current.clear();blocked.current.clear();inFlight.current.clear()}},[])
 return {drafts,setDraft,discard,save}
}
