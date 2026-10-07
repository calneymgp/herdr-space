import type {GitHubIssue,GitHubIssuePage,GitHubRepository,GitHubStatus,Note,Project,Session,Space,Task,Theme} from './state'
export class ApiError extends Error { constructor(message:string, public status:number, public current?:Note){super(message)} }
export class ApiClient {
  csrf = ''; username = ''; authenticated = false
  private authEpoch = 0
  private unauthorizedListeners = new Set<()=>void>()
  setAuth(token:string,username:string){this.authEpoch++;this.csrf=token;this.username=username;this.authenticated=true}
  clearAuth(){this.authEpoch++;this.csrf='';this.username='';this.authenticated=false}
  authSnapshot(){return this.authEpoch}
  expireStream(epoch:number){if(epoch!==this.authEpoch||!this.authenticated)return false;this.clearAuth();this.unauthorizedListeners.forEach(listener=>listener());return true}
  onUnauthorized(listener:()=>void){this.unauthorizedListeners.add(listener);return()=>{this.unauthorizedListeners.delete(listener)}}
  async request<T>(path:string,options:RequestInit={}):Promise<T>{
    const authEpoch=this.authEpoch
    const headers:Record<string,string> = {'Accept':'application/json',...(options.headers as Record<string,string>||{})}
    if(options.body) headers['Content-Type']='application/json'
    if(options.method && options.method!=='GET') headers['X-CSRF-Token']=headers['X-CSRF-Token']||this.csrf
    let response:Response
    try { response=await fetch('/api/v1'+path,{...options,credentials:'same-origin',headers,cache:'no-store'}) }
    catch { throw new ApiError('No connection to the server. Your local changes were preserved.',0) }
    if(response.status===401&&authEpoch===this.authEpoch&&this.authenticated){
      this.clearAuth()
      this.unauthorizedListeners.forEach(listener=>listener())
    }
    const raw=await response.text()
    let data:Record<string,unknown>={}
    try {const parsed:unknown=raw?JSON.parse(raw):{};data=parsed&&typeof parsed==='object'&&!Array.isArray(parsed)?parsed as Record<string,unknown>:{} } catch {throw new ApiError('Unexpected server response.',response.status)}
    if(!response.ok){
      throw new ApiError(typeof data.error==='string'?data.error:`Error ${response.status}`,response.status,data.current as Note|undefined)
    }
    return data as T
  }
  status(){return this.request<{authenticated:boolean;configured:boolean;setup_available?:boolean;setup_browser_enabled?:boolean;username?:string;csrf_token?:string}>('/auth/status')}
  setupBegin(username:string,password:string){return this.request<{secret:string;otpauth_url:string;csrf_token:string}>('/auth/setup/begin',{method:'POST',body:JSON.stringify({username,password})})}
  setupComplete(otp:string,csrf:string){return this.request<{username:string;recovery_codes:string[];configured:true}>('/auth/setup/complete',{method:'POST',headers:{'X-CSRF-Token':csrf},body:JSON.stringify({otp,csrf})})}
  async login(username:string,password:string,otp:string){const result=await this.request<{username:string;csrf_token:string}>('/auth/login',{method:'POST',body:JSON.stringify({username,password,otp})});this.setAuth(result.csrf_token,result.username);return result}
  async logout(){await this.request('/auth/logout',{method:'POST'});this.clearAuth()}
  list<T>(name:'projects'|'tasks'|'notes'|'sessions'){return this.request<{items:T[];spaces?:Space[];stale?:boolean;warning?:string}>('/'+name)}
  spaceProject(spaceId:string){return this.request<{item:Project}>('/spaces/'+encodeURIComponent(spaceId)+'/project',{method:'POST',body:JSON.stringify({})})}
  create<T>(name:'projects'|'tasks'|'notes',value:object){return this.request<{item:T}>('/'+name,{method:'POST',body:JSON.stringify(value)})}
  update<T>(name:'projects'|'tasks'|'notes',id:string,value:object){return this.request<{item:T}>('/'+name+'/'+encodeURIComponent(id),{method:'PATCH',body:JSON.stringify(value)})}
  remove(name:'projects'|'tasks'|'notes',id:string){return this.request('/'+name+'/'+encodeURIComponent(id),{method:'DELETE'})}
  preferences(){return this.request<{item:{theme:Theme}}>('/preferences')}
  githubStatus(){return this.request<GitHubStatus>('/github/status')}
  githubRepositories(){return this.request<{items:GitHubRepository[]}>('/github/repositories')}
  githubIssues(spaceId:string,state:'open'|'closed'|'all',page:number){return this.request<GitHubIssuePage>('/github/issues?space_id='+encodeURIComponent(spaceId)+'&state='+state+'&page='+page)}
  createGithubIssue(spaceId:string,title:string,body:string){return this.request<{item:GitHubIssue}>('/github/issues',{method:'POST',body:JSON.stringify({space_id:spaceId,title,body})})}
  updateGithubIssue(number:number,value:{space_id:string;title?:string;body?:string;state?:'open'|'closed'}){return this.request<{item:GitHubIssue}>('/github/issues/'+number,{method:'PATCH',body:JSON.stringify(value)})}
  setTheme(theme:Theme){return this.request<{item:{theme:Theme}}>('/preferences',{method:'PATCH',body:JSON.stringify({theme})})}
  start(agent:string,launcher:string,project_id:string){return this.request<{item:Session}>('/sessions/start',{method:'POST',body:JSON.stringify({agent,launcher,project_id})})}
  stop(id:string,force=false){return this.request('/sessions/'+encodeURIComponent(id)+'/stop',{method:'POST',body:JSON.stringify({confirm:true,force})})}
  resume(id:string,project_id:string){return this.request<{item:Session}>('/sessions/'+encodeURIComponent(id)+'/resume',{method:'POST',body:JSON.stringify({confirm:true,project_id})})}
}
export const api = new ApiClient()
export type {GitHubIssue,GitHubIssuePage,GitHubRepository,GitHubStatus,Note,Project,Session,Task}
