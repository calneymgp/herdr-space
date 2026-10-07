import { describe, expect, it } from 'vitest'
import { beginDraft, editDraft, saveFailed, saveConflict, saveSucceeded, type Note } from './state'
const note: Note = { id:'n1', title:'Original', body:'A', project_id:'', version:2, created_at:'2026-01-01T00:00:00Z', updated_at:'2026-01-01T00:00:00Z' }
describe('note draft', () => {
  it('keeps local edits and the server version on conflict', () => {
    const draft = editDraft(beginDraft(note), { body:'My unsaved text' })
    const conflicted = saveConflict(draft, { ...note, body:'Other edit', version:3 })
    expect(conflicted.body).toBe('My unsaved text')
    expect(conflicted.version).toBe(2)
    expect(conflicted.current?.version).toBe(3)
    expect(conflicted.status).toBe('conflict')
  })
  it('keeps unsaved text after network failure', () => {
    const failed = saveFailed(editDraft(beginDraft(note), { body:'Offline edit' }))
    expect(failed.body).toBe('Offline edit')
    expect(failed.status).toBe('error')
  })
  it('does not erase edits typed while an earlier save was in flight', () => {
    const savedSnapshot = editDraft(beginDraft(note), { body:'First' })
    const latest = editDraft(savedSnapshot, { body:'Second' })
    const result = saveSucceeded(latest, { ...note, body:'First', version:3 }, savedSnapshot.revision)
    expect(result.body).toBe('Second')
    expect(result.version).toBe(3)
    expect(result.status).toBe('dirty')
  })
})
