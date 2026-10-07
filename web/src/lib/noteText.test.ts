import {expect,it} from 'vitest'
import {validateNoteText} from './noteText'

it('validates exact raw UTF-8 payload and permits an empty body',()=>{
 expect(validateNoteText('á'.repeat(120),'').valid).toBe(true)
 expect(validateNoteText('á'.repeat(121),'').titleBytes).toBe(242)
 expect(validateNoteText('á'.repeat(121),'').valid).toBe(false)
 expect(validateNoteText('  ','').titleError).toMatch(/Enter a title/)
 expect(validateNoteText('a'.repeat(239)+' ','').valid).toBe(true)
 expect(validateNoteText('a'.repeat(240)+' ','').valid).toBe(false)
 expect(validateNoteText('Title','😀'.repeat(250_000)).valid).toBe(true)
 expect(validateNoteText('Title','😀'.repeat(250_000)+'a').bodyError).toMatch(/1000001 bytes/)
})
