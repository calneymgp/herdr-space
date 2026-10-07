import {describe,expect,it} from 'vitest'
import {beginDraft,editDraft,type Note} from './state'
import {putDraft,resolveSave,resolveFailure,discardDraft,rebaseDraft} from './noteDrafts'
const make=(id:string,body:string,version=1):Note=>({id,title:id,body,project_id:'',version,created_at:'2026-01-01T00:00:00Z',updated_at:'2026-01-01T00:00:00Z'})
describe('per-note drafts',()=>{
 it('retains a failed draft while switching to another note and back',()=>{
  const a=make('A','server A'),b=make('B','server B')
  const edited=editDraft(beginDraft(a),{body:'local A'})
  let drafts=putDraft({},edited)
  drafts=resolveFailure(drafts,edited,new Error('offline'))
  drafts=putDraft(drafts,beginDraft(b))
  expect(drafts.A.body).toBe('local A')
  expect(drafts.A.status).toBe('error')
  expect(drafts.B.body).toBe('server B')
 })
 it('applies delayed save success only to the captured note while another is edited',()=>{
  const a=editDraft(beginDraft(make('A','server A')),{body:'new A'})
  const b=editDraft(beginDraft(make('B','server B',7)),{body:'new B'})
  const drafts=resolveSave(putDraft(putDraft({},a),b),a,make('A','new A',2))
  expect(drafts.A.version).toBe(2)
  expect(drafts.A.status).toBe('saved')
  expect(drafts.B).toEqual(b)
 })
 it('applies delayed conflict only to A and preserves edits to B',()=>{
  const a=editDraft(beginDraft(make('A','server A')),{body:'new A'})
  const b=editDraft(beginDraft(make('B','server B',7)),{body:'new B'})
  const drafts=resolveFailure(putDraft(putDraft({},a),b),a,{current:make('A','other A',2)})
  expect(drafts.A.body).toBe('new A')
  expect(drafts.A.current?.body).toBe('other A')
  expect(drafts.B).toEqual(b)
 })
 it('keeps edits made during a save and ignores its result after loading current',()=>{
  const a=editDraft(beginDraft(make('A','old')),{body:'first'})
  const changed=editDraft(a,{body:'second'})
  const afterSave=resolveSave(putDraft({},changed),a,make('A','first',2))
  expect(afterSave.A.body).toBe('second')
  expect(afterSave.A.status).toBe('dirty')
  const loaded=rebaseDraft(afterSave.A,make('A','server current',3))
  const ignored=resolveSave(putDraft({},loaded),a,make('A','first',2))
  expect(ignored.A).toEqual(loaded)
 })
 it('does not resurrect a deleted draft when an old request completes',()=>{
  const a=editDraft(beginDraft(make('A','old')),{body:'local'})
  expect(resolveSave(discardDraft(putDraft({},a),'A'),a,make('A','local',2))).toEqual({})
 })
})
