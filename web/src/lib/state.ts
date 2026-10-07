export type Project = {id:string; name:string; path:string; created_at:string}
export type Space = {id:string; name:string; path:string; server_id:string; workspace_id:string}
export type Task = {id:string; title:string; description:string; project_id:string; session_id:string; status:'todo'|'doing'|'done'; due_date:string; created_at:string; updated_at:string}
export type Note = {id:string; title:string; body:string; project_id:string; version:number; created_at:string; updated_at:string}
export type Session = {id:string; name:string; source:string; agent:string; launcher:string; project_id:string; cwd:string; server_id:string; terminal_id:string; workspace_id:string; pane_id:string; pid:number; start_time:string; agent_session_id?:string; membership:string; activity:string; alive:boolean; updated_at:string; capabilities:string[]}
export type Theme = 'system'|'dark'|'light'
export type GitHubStatus = {available:boolean;authenticated:boolean;message?:string}
export type GitHubRepository = {space_id:string;space_name:string;repository:string}
export type GitHubIssue = {number:number;title:string;body:string;state:'open'|'closed';url:string;updated_at:string}
export type GitHubIssuePage = {items:GitHubIssue[];page:number;has_more:boolean}
export type Draft = Pick<Note,'title'|'body'|'project_id'|'version'> & {id:string; revision:number; epoch:number; status:'saved'|'dirty'|'saving'|'error'|'conflict'; current?:Note; errorMessage?:string}
export const beginDraft = (note:Note):Draft => ({id:note.id,title:note.title,body:note.body,project_id:note.project_id,version:note.version,revision:0,epoch:0,status:'saved'})
export const editDraft = (draft:Draft, patch:Partial<Pick<Draft,'title'|'body'|'project_id'>>):Draft => ({...draft,...patch,errorMessage:undefined,revision:draft.revision+1,status:draft.status==='conflict'?'conflict':'dirty'})
export const saveFailed = (draft:Draft,errorMessage?:string):Draft => ({...draft,status:'error',errorMessage})
export const saveConflict = (draft:Draft,current:Note):Draft => ({...draft,current,status:'conflict'})
export const saveSucceeded = (draft:Draft,saved:Note,revision:number):Draft => ({...draft,version:saved.version,current:undefined,errorMessage:undefined,status:draft.revision===revision?'saved':'dirty'})
