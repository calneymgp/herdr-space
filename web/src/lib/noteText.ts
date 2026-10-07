export const NOTE_TITLE_MAX_BYTES=240
export const NOTE_BODY_MAX_BYTES=1_000_000
const byteLength=(value:string)=>new TextEncoder().encode(value).length
export function validateNoteText(title:string,body:string){
 const titleBytes=byteLength(title),bodyBytes=byteLength(body)
 const titleError=!title.trim()?'Enter a title to save the note.':titleBytes>NOTE_TITLE_MAX_BYTES?`The title has ${titleBytes} bytes UTF-8; the limit is ${NOTE_TITLE_MAX_BYTES}. Shorten the title.`:''
 const bodyError=bodyBytes>NOTE_BODY_MAX_BYTES?`The content has ${bodyBytes} bytes UTF-8; the limit is ${NOTE_BODY_MAX_BYTES}. Shorten the text.`:''
 return {valid:!titleError&&!bodyError,titleBytes,bodyBytes,titleError,bodyError}
}
