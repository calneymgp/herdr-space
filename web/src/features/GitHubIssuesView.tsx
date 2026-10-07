import {useEffect,useRef,useState,type FormEvent} from 'react'
import {ChevronLeft,ChevronRight,ExternalLink,Plus,RefreshCw,RotateCcw,X} from 'lucide-react'
import {Button} from '../components/ui/button'
import {Input,Textarea} from '../components/ui/input'
import {Dialog,DialogContent,DialogDescription,DialogTitle} from '../components/ui/dialog'
import {api,ApiError} from '../lib/api'
import {apiErrorMessage,isUnauthorized} from '../lib/apiErrorMessage'
import type {GitHubIssue,GitHubRepository} from '../lib/state'
import {GITHUB_ISSUE_BODY_MAX_BYTES,GITHUB_ISSUE_TITLE_MAX_BYTES,validateGithubIssueText} from './issueText'
import './GitHubIssuesView.css'

type IssueState='open'|'closed'|'all'
type AccessState='loading'|'ready'|'unavailable'|'unauthenticated'|'error'
const issueUrl=(url:string)=>/^https:\/\/github\.com\/[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+\/issues\/\d+$/.test(url)?url:''
const dateText=(value:string)=>{try{return new Date(value).toLocaleString('en-US',{dateStyle:'short',timeStyle:'short'})}catch{return value}}
const errorText=(error:unknown)=>apiErrorMessage(error,'Could not access GitHub issues.')

export function GitHubIssuesView(){
 const [access,setAccess]=useState<AccessState>('loading'),[accessMessage,setAccessMessage]=useState(''),[repositories,setRepositories]=useState<GitHubRepository[]>([]),[spaceId,setSpaceId]=useState(''),[statusAttempt,setStatusAttempt]=useState(0)
 const [state,setState]=useState<IssueState>('open'),[page,setPage]=useState(1),[issues,setIssues]=useState<GitHubIssue[]>([]),[hasMore,setHasMore]=useState(false),[loading,setLoading]=useState(false),[error,setError]=useState(''),[refreshKey,setRefreshKey]=useState(0)
 const [dialog,setDialog]=useState<'create'|'edit'|null>(null),[editing,setEditing]=useState<GitHubIssue|null>(null),[title,setTitle]=useState(''),[body,setBody]=useState(''),[saveError,setSaveError]=useState(''),[saving,setSaving]=useState(false),[busyIssue,setBusyIssue]=useState<number|null>(null),[createNeedsRefresh,setCreateNeedsRefresh]=useState(false),[createReview,setCreateReview]=useState<GitHubIssue[]|null>(null),[reviewHasMore,setReviewHasMore]=useState(false)
 const savingRef=useRef(false),issueRequest=useRef(0),actionRef=useRef(false),actionGeneration=useRef(0),saveGeneration=useRef(0),currentSpace=useRef(spaceId)
 currentSpace.current=spaceId
 useEffect(()=>()=>{actionGeneration.current++;saveGeneration.current++},[])

 useEffect(()=>{
  let active=true
  setAccess('loading')
  void api.githubStatus().then(async status=>{
   if(!active)return
   if(!status.available){setAccess('unavailable');setAccessMessage(status.message||'GitHub integration is unavailable in this environment.');return}
   if(!status.authenticated){setAccess('unauthenticated');setAccessMessage('GitHub CLI is not authenticated in this environment. Authenticate gh in the agent terminal and try again.');return}
   try{
    const result=await api.githubRepositories()
    if(!active)return
    const items=result.items||[]
    setRepositories(items)
    setSpaceId(current=>items.some(item=>item.space_id===current)?current:items[0]?.space_id||'')
    setAccess('ready')
   }catch(error){if(active){setAccess('error');setAccessMessage(errorText(error))}}
  }).catch(error=>{if(active){setAccess('error');setAccessMessage(errorText(error))}})
  return()=>{active=false}
 },[statusAttempt])

 useEffect(()=>{
  if(access!=='ready'||!spaceId)return
  const request=++issueRequest.current
  setLoading(true);setError('')
  void api.githubIssues(spaceId,state,page).then(result=>{
   if(request!==issueRequest.current)return
   setIssues(result.items||[]);setHasMore(!!result.has_more)
  }).catch(error=>{if(request===issueRequest.current){setIssues([]);setHasMore(false);setError(errorText(error))}}).finally(()=>{if(request===issueRequest.current)setLoading(false)})
  return()=>{if(request===issueRequest.current)issueRequest.current++}
 },[access,spaceId,state,page,refreshKey])

 const selectedRepository=repositories.find(item=>item.space_id===spaceId)
 const issueText=validateGithubIssueText(title,body)
 const refreshGithub=()=>{setPage(1);setRefreshKey(value=>value+1);setStatusAttempt(value=>value+1)}
 const openCreate=()=>{saveGeneration.current++;setEditing(null);setTitle('');setBody('');setSaveError('');setCreateNeedsRefresh(false);setCreateReview(null);setDialog('create')}
 const openEdit=(issue:GitHubIssue)=>{saveGeneration.current++;setEditing(issue);setTitle(issue.title);setBody(issue.body);setSaveError('');setCreateNeedsRefresh(false);setCreateReview(null);setDialog('edit')}
 const closeDialog=()=>{if(savingRef.current)return;saveGeneration.current++;setDialog(null);setEditing(null);setSaveError('')}
 const loadCreateReview=async()=>{
  if(savingRef.current||dialog!=='create'||!spaceId)return
  savingRef.current=true;setSaving(true);const request=++saveGeneration.current,repository=spaceId
  try{const result=await api.githubIssues(repository,'all',1);if(request===saveGeneration.current&&currentSpace.current===repository){setCreateReview(result.items||[]);setReviewHasMore(!!result.has_more);setSaveError('Review the updated issues before deciding whether to create another.')}}
  catch(error){if(request===saveGeneration.current&&currentSpace.current===repository&&!isUnauthorized(error))setSaveError(`${apiErrorMessage(error,'Could not refresh the issues.')} The previous creation may have completed; try refreshing again.`)}
  finally{if(request===saveGeneration.current){savingRef.current=false;setSaving(false)}}
 }
 const submit=async(event:FormEvent)=>{
  event.preventDefault()
  const text=validateGithubIssueText(title,body)
  if(savingRef.current||!spaceId||!text.valid)return
  if(dialog==='create'&&createNeedsRefresh&&createReview===null){await loadCreateReview();return}
  savingRef.current=true;setSaving(true);setSaveError('');const request=++saveGeneration.current,repository=spaceId,kind=dialog
  try{
   if(kind==='create')await api.createGithubIssue(repository,text.payloadTitle,body)
   else if(kind==='edit'&&editing)await api.updateGithubIssue(editing.number,{space_id:repository,title:text.payloadTitle,body})
   else return
   if(request===saveGeneration.current&&currentSpace.current===repository){setDialog(null);setEditing(null);setPage(1);setRefreshKey(value=>value+1)}
  }catch(error){if(request===saveGeneration.current&&currentSpace.current===repository&&!isUnauthorized(error)){const uncertain=kind==='create'&&(!(error instanceof ApiError)||error.status===0||error.status>=500);setCreateNeedsRefresh(uncertain);setCreateReview(null);setSaveError(uncertain?`${errorText(error)} The issue may have been created; refresh the list before trying again.`:apiErrorMessage(error,'Could not save the issue. Please try again.'))}}
  finally{if(request===saveGeneration.current){savingRef.current=false;setSaving(false)}}
 }
 const toggleIssue=async(issue:GitHubIssue)=>{
  if(actionRef.current)return
  actionRef.current=true;setBusyIssue(issue.number);setError('');const request=++actionGeneration.current,repository=spaceId
  try{await api.updateGithubIssue(issue.number,{space_id:repository,state:issue.state==='open'?'closed':'open'});if(request===actionGeneration.current&&currentSpace.current===repository)setRefreshKey(value=>value+1)}
  catch(error){if(request===actionGeneration.current&&currentSpace.current===repository&&!isUnauthorized(error))setError(errorText(error))}
  finally{if(request===actionGeneration.current){actionRef.current=false;if(currentSpace.current===repository)setBusyIssue(null)}}
 }
 const changeState=(next:IssueState)=>{setState(next);setPage(1)}

 if(access==='loading')return <section className="github-issues" aria-label="GitHub issues"><div className="github-empty" role="status"><RefreshCw size={19} className="spin"/> Checking GitHub integration…</div></section>
 if(access!=='ready')return <section className="github-issues" aria-label="GitHub issues"><div className="github-empty" role={access==='error'?'alert':'status'}><X size={20}/><h2>{access==='unauthenticated'?'GitHub not authenticated':access==='unavailable'?'GitHub unavailable':'Could not check GitHub'}</h2><p>{accessMessage}</p><Button size="sm" variant="outline" onClick={()=>setStatusAttempt(value=>value+1)}>Check again</Button></div></section>
 if(repositories.length===0)return <section className="github-issues" aria-label="GitHub issues"><div className="github-empty"><h2>No linked repository</h2><p>Link a GitHub repository to a Space to view its issues.</p><Button size="sm" variant="outline" onClick={()=>setStatusAttempt(value=>value+1)}>Refresh repositories</Button></div></section>

 return <section className="github-issues" aria-label="GitHub issues">
  <div className="github-issues-heading"><div><h2>GitHub issues</h2><p>{selectedRepository?`${selectedRepository.repository} · ${selectedRepository.space_name}`:'Select a repository linked to a Space.'}</p></div><div className="github-issues-heading-actions"><Button variant="outline" onClick={refreshGithub} disabled={loading||saving||busyIssue!==null}><RefreshCw size={15}/> Refresh</Button><Button onClick={openCreate} disabled={!spaceId||saving||busyIssue!==null}><Plus size={16}/> Create issue</Button></div></div>
  <div className="github-issues-toolbar"><label className="field"><span>Repository</span><select className="ui-input" aria-label="Repository" value={spaceId} disabled={saving} onChange={event=>{currentSpace.current=event.target.value;actionGeneration.current++;actionRef.current=false;setBusyIssue(null);setError('');setSpaceId(event.target.value);setPage(1)}}>{repositories.map(item=><option key={item.space_id} value={item.space_id}>{item.repository} · {item.space_name}</option>)}</select></label><div className="github-state-filter" role="group" aria-label="Issue state">{(['open','closed','all'] as const).map(value=><button key={value} className={state===value?'active':''} aria-pressed={state===value} onClick={()=>changeState(value)}>{value==='open'?'Open':value==='closed'?'Closed':'All'}</button>)}</div></div>
  {error&&<div className="github-issues-error" role="alert"><span>{error}</span><Button size="sm" variant="ghost" onClick={()=>setRefreshKey(value=>value+1)}>Try again</Button></div>}
  {loading?<div className="github-empty" role="status"><RefreshCw size={18} className="spin"/> Loading issues…</div>:issues.length===0&&!error?<div className="github-empty"><h3>{page>1||hasMore?'No issues on this page':`No issues ${state==='all'?'found':state==='open'?'open':'closed'}`}</h3><p>{hasMore?'More items are available. Go to the next page.':state==='open'?'Create an issue to start tracking work here.':'No issues match this filter in this repository.'}</p></div>:<div className="github-issue-list">{issues.map(issue=><article className="github-issue-row" key={issue.number}>
   <span className={'github-issue-state '+issue.state} aria-label={issue.state==='open'?'Open':'Closed'}>{issue.state==='open'?'Open':'Closed'}</span>
   <div className="github-issue-main"><button className="github-issue-title" onClick={()=>openEdit(issue)}>{issue.title}</button><span className="github-issue-meta">#{issue.number} · Updated {dateText(issue.updated_at)}</span></div>
   <div className="github-issue-actions">{issueUrl(issue.url)&&<a className="github-issue-link" href={issueUrl(issue.url)} target="_blank" rel="noopener noreferrer" aria-label={'Open issue #'+issue.number+' on GitHub'}><ExternalLink size={16}/></a>}<Button size="sm" variant="outline" onClick={()=>void toggleIssue(issue)} disabled={busyIssue!==null} aria-label={issue.state==='open'?'Close issue #'+issue.number:'Reopen issue #'+issue.number}>{busyIssue===issue.number?'Saving…':issue.state==='open'?<><X size={15}/> Close</>:<><RotateCcw size={15}/> Reopen</>}</Button></div>
  </article>)}</div>}
  <div className="github-pagination"><span>Page {page}</span><div><Button size="sm" variant="outline" onClick={()=>setPage(value=>Math.max(1,value-1))} disabled={page===1||loading}><ChevronLeft size={16}/> Previous</Button><Button size="sm" variant="outline" onClick={()=>setPage(value=>value+1)} disabled={!hasMore||loading}>Next <ChevronRight size={16}/></Button></div></div>
  <Dialog open={dialog!==null} onOpenChange={open=>!open&&closeDialog()}><DialogContent className="github-issue-dialog"><span className="eyebrow">GITHUB ISSUE</span><DialogTitle>{dialog==='edit'&&editing?`Edit issue #${editing.number}`:'New issue'}</DialogTitle><DialogDescription>{selectedRepository?`It will be saved directly in ${selectedRepository.repository}.`:'The issue will be saved in the repository linked to this Space.'}</DialogDescription><form className="dialog-form" onSubmit={submit}><label className="field"><span>Title</span><Input aria-label="Title" aria-describedby="github-issue-title-count" aria-invalid={issueText.titleOverLimit||undefined} value={title} onChange={event=>setTitle(event.target.value)} required autoFocus disabled={saving}/><span id="github-issue-title-count" className={'github-issue-text-count'+(issueText.titleOverLimit?' invalid':'')}>{issueText.titleBytes} / {GITHUB_ISSUE_TITLE_MAX_BYTES} bytes UTF-8{issueText.titleOverLimit?' — over the limit':''}</span></label><label className="field"><span>Description</span><Textarea aria-label="Description" aria-describedby="github-issue-body-count" aria-invalid={issueText.bodyOverLimit||undefined} value={body} onChange={event=>setBody(event.target.value)} rows={6} disabled={saving}/><span id="github-issue-body-count" className={'github-issue-text-count'+(issueText.bodyOverLimit?' invalid':'')}>{issueText.bodyBytes} / {GITHUB_ISSUE_BODY_MAX_BYTES} bytes UTF-8{issueText.bodyOverLimit?' — over the limit':''}</span></label>{saveError&&<div className="github-issues-error" role="alert"><span>{saveError}</span>{dialog==='create'&&createNeedsRefresh&&<Button type="button" size="sm" variant="ghost" onClick={()=>void loadCreateReview()} disabled={saving}>Refresh list</Button>}</div>}{dialog==='create'&&createReview!==null&&<div className="github-create-review" role="region" aria-label="Updated issues"><strong>Current issues · first page</strong>{createReview.length?<ul>{createReview.map(issue=><li key={issue.number}>#{issue.number} · {issue.title}{issueUrl(issue.url)&&<a href={issueUrl(issue.url)} target="_blank" rel="noopener noreferrer" aria-label={'Open issue #'+issue.number}>Open</a>}</li>)}</ul>:<p>No issues appeared on the first page.</p>}{reviewHasMore&&<p>There are more pages in the list. Check them if needed.</p>}<p>Check the previous result. Creating anyway will submit another issue.</p><Button type="button" size="sm" variant="outline" onClick={()=>{closeDialog();setState('all');setPage(1);setRefreshKey(value=>value+1)}}>Return to list without creating</Button></div>}<div className="dialog-footer"><Button type="button" variant="ghost" onClick={closeDialog} disabled={saving}>Cancel</Button><Button disabled={saving||!spaceId||!issueText.valid}>{saving?'Please wait…':dialog==='edit'?'Save changes':createNeedsRefresh?createReview===null?'Refresh before retrying':'Create anyway':'Create issue'}</Button></div></form></DialogContent></Dialog>
 </section>
}
