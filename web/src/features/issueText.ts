export const GITHUB_ISSUE_TITLE_MAX_BYTES=256
export const GITHUB_ISSUE_BODY_MAX_BYTES=65536

const utf8Encoder=new TextEncoder()
const utf8Bytes=(value:string)=>utf8Encoder.encode(value).length

export function validateGithubIssueText(title:string,body:string){
 const payloadTitle=title.trim()
 const titleBytes=utf8Bytes(payloadTitle)
 const bodyBytes=utf8Bytes(body)
 const titleOverLimit=titleBytes>GITHUB_ISSUE_TITLE_MAX_BYTES
 const bodyOverLimit=bodyBytes>GITHUB_ISSUE_BODY_MAX_BYTES

 return {
  payloadTitle,
  titleBytes,
  bodyBytes,
  titleOverLimit,
  bodyOverLimit,
  valid:payloadTitle.length>0&&!titleOverLimit&&!bodyOverLimit,
 }
}
