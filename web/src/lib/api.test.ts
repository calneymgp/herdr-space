import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiClient } from './api'
afterEach(() => vi.unstubAllGlobals())
describe('logout', () => {
  it('invalidates the current session on malformed 401 but not a newer login', async () => {
    let resolveOld!:(response:unknown)=>void
    const old=new Promise(resolve=>{resolveOld=resolve})
    vi.stubGlobal('fetch',vi.fn().mockReturnValueOnce(old).mockResolvedValueOnce({ok:false,status:401,text:async()=>'<html>'}))
    const api=new ApiClient()
    api.setAuth('old','owner')
    const pending=api.list('notes')
    api.setAuth('new','owner')
    resolveOld({ok:false,status:401,text:async()=>'<html>'})
    await expect(pending).rejects.toMatchObject({status:401})
    expect(api.authenticated).toBe(true)
    const unauthorized=vi.fn()
    api.onUnauthorized(unauthorized)
    await expect(api.list('notes')).rejects.toMatchObject({status:401})
    expect(unauthorized).toHaveBeenCalledTimes(1)
    expect(api.authenticated).toBe(false)
  })
  it('sends csrf and clears local authentication only after server confirmation', async () => {
    const fetcher = vi.fn().mockResolvedValue({ok:true,status:204,headers:new Headers(),text:async()=>''})
    vi.stubGlobal('fetch', fetcher)
    const api = new ApiClient()
    api.setAuth('token', 'owner')
    await api.logout()
    expect(fetcher).toHaveBeenCalledWith('/api/v1/auth/logout', expect.objectContaining({method:'POST',headers:expect.objectContaining({'X-CSRF-Token':'token'})}))
    expect(api.authenticated).toBe(false)
  })
  it('retains authentication when logout cannot reach server', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('offline')))
    const api = new ApiClient()
    api.setAuth('token', 'owner')
    await expect(api.logout()).rejects.toThrow()
    expect(api.authenticated).toBe(true)
  })
})
describe('first-run setup API', () => {
  it('sends pending CSRF in the completion body', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce({ok:true,status:200,headers:new Headers(),text:async()=>JSON.stringify({secret:'S',otpauth_url:'otpauth://example',csrf_token:'pending'})})
      .mockResolvedValueOnce({ok:true,status:200,headers:new Headers(),text:async()=>JSON.stringify({username:'owner',recovery_codes:['r1'],configured:true})})
    vi.stubGlobal('fetch', fetcher)
    const api = new ApiClient()
    await api.setupBegin('owner','a long password')
    await api.setupComplete('123456','pending')
    expect(JSON.parse(fetcher.mock.calls[1][1].body)).toEqual({otp:'123456',csrf:'pending'})
    expect(fetcher.mock.calls[1][0]).toBe('/api/v1/auth/setup/complete')
  })
})

describe('GitHub issues API', () => {
  it('routes repository and issue reads through the authenticated API client', async () => {
    const fetcher = vi.fn().mockResolvedValue({ok:true,status:200,headers:new Headers(),text:async()=>JSON.stringify({available:true,authenticated:true,items:[],page:1,has_more:false})})
    vi.stubGlobal('fetch', fetcher)
    const api = new ApiClient()
    api.setAuth('csrf-token','owner')
    await api.githubStatus()
    await api.githubRepositories()
    await api.githubIssues('space / one','closed',2)
    expect(fetcher.mock.calls.map(call=>call[0])).toEqual([
      '/api/v1/github/status',
      '/api/v1/github/repositories',
      '/api/v1/github/issues?space_id=space%20%2F%20one&state=closed&page=2',
    ])
    expect(fetcher.mock.calls[2][1]).toEqual(expect.objectContaining({credentials:'same-origin',cache:'no-store'}))
  })

  it('sends CSRF for create and edit and preserves authentication on GitHub auth failures', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce({ok:true,status:200,headers:new Headers(),text:async()=>JSON.stringify({item:{number:7}})})
      .mockResolvedValueOnce({ok:true,status:200,headers:new Headers(),text:async()=>JSON.stringify({item:{number:7}})})
      .mockResolvedValueOnce({ok:false,status:503,headers:new Headers(),text:async()=>JSON.stringify({error:'GitHub unavailable'})})
    vi.stubGlobal('fetch', fetcher)
    const api = new ApiClient()
    api.setAuth('csrf-token','owner')
    await api.createGithubIssue('space-1','Failed','Detalhes')
    await api.updateGithubIssue(7,{space_id:'space-1',title:'Corrigida',body:'Detalhes novos',state:'closed'})
    await expect(api.githubStatus()).rejects.toMatchObject({status:503})
    expect(fetcher.mock.calls[0][0]).toBe('/api/v1/github/issues')
    expect(fetcher.mock.calls[0][1]).toEqual(expect.objectContaining({method:'POST',headers:expect.objectContaining({'X-CSRF-Token':'csrf-token'})}))
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({space_id:'space-1',title:'Failed',body:'Detalhes'})
    expect(fetcher.mock.calls[1][0]).toBe('/api/v1/github/issues/7')
    expect(fetcher.mock.calls[1][1]).toEqual(expect.objectContaining({method:'PATCH',headers:expect.objectContaining({'X-CSRF-Token':'csrf-token'})}))
    expect(JSON.parse(fetcher.mock.calls[1][1].body)).toEqual({space_id:'space-1',title:'Corrigida',body:'Detalhes novos',state:'closed'})
    expect(api.authenticated).toBe(true)
  })
})

describe('native Space project compatibility API',()=>{
  it('resolves internal project metadata lazily with CSRF protection',async()=>{
    const fetcher=vi.fn().mockResolvedValue({ok:true,status:200,headers:new Headers(),text:async()=>JSON.stringify({item:{id:'internal',name:'Space',path:'/work/space',created_at:'2026-01-01T00:00:00Z'}})})
    vi.stubGlobal('fetch',fetcher)
    const api=new ApiClient()
    api.setAuth('csrf-token','owner')
    await api.spaceProject('space/id')
    expect(fetcher).toHaveBeenCalledWith('/api/v1/spaces/space%2Fid/project',expect.objectContaining({method:'POST',headers:expect.objectContaining({'X-CSRF-Token':'csrf-token'})}))
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({})
  })
})
it('ignores a revoked WebSocket from an earlier login and expires the current one once',()=>{
 const api=new ApiClient(),unauthorized=vi.fn()
 api.onUnauthorized(unauthorized)
 api.setAuth('old','owner')
 const old=api.authSnapshot()
 api.setAuth('new','owner')
 expect(api.expireStream(old)).toBe(false)
 expect(api.authenticated).toBe(true)
 expect(api.expireStream(api.authSnapshot())).toBe(true)
 expect(api.authenticated).toBe(false)
 expect(unauthorized).toHaveBeenCalledTimes(1)
 expect(api.expireStream(old)).toBe(false)
})
