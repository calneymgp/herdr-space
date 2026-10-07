import {saveConflict,saveFailed,saveSucceeded,type Draft,type Note} from './state'
export type DraftStore=Record<string,Draft>
export const putDraft=(drafts:DraftStore,draft:Draft):DraftStore=>({...drafts,[draft.id]:draft})
export const discardDraft=(drafts:DraftStore,id:string):DraftStore=>{const next={...drafts};delete next[id];return next}
export const rebaseDraft=(draft:Draft,note:Note):Draft=>({id:note.id,title:note.title,body:note.body,project_id:note.project_id,version:note.version,revision:0,epoch:draft.epoch+1,status:'saved'})
const matching=(drafts:DraftStore,snapshot:Draft)=>{const current=drafts[snapshot.id];return current&&current.epoch===snapshot.epoch&&current.version===snapshot.version?current:null}
export const resolveSave=(drafts:DraftStore,snapshot:Draft,saved:Note):DraftStore=>{
 const current=matching(drafts,snapshot)
 return current?putDraft(drafts,saveSucceeded(current,saved,snapshot.revision)):drafts
}
export const resolveFailure=(drafts:DraftStore,snapshot:Draft,error:Error|{current?:Note},message?:string):DraftStore=>{
 const current=matching(drafts,snapshot)
 return current?putDraft(drafts,'current' in error&&error.current?saveConflict(current,error.current):saveFailed(current,message)):drafts
}
