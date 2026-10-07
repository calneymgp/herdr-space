import {expect,it} from 'vitest'
import {formatDueDate} from './date'
it('renders a date-only task deadline without shifting it in a time zone behind UTC',()=>{
 expect(formatDueDate('2026-10-05')).toBe('10/05/2026')
})
