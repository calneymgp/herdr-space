import assert from 'node:assert/strict'
import { spawn, spawnSync } from 'node:child_process'
import { createHash, createHmac } from 'node:crypto'
import { readFile, mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium, webkit } from '../../.tools/browser/node_modules/playwright/index.mjs'

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..')
const out=path.join(root,'.state/task16/results')
await mkdir(out,{recursive:true,mode:0o700})
const sha=async p=>createHash('sha256').update(await readFile(p)).digest('hex')
const baseline=path.join(root,'.state/qa/diagnostic-baseline/fixture-server')
const current=path.join(root,'.state/task16/current-fixture')
const head=spawnSync('git',['rev-parse','HEAD'],{cwd:root,encoding:'utf8'}).stdout.trim()
function otp(secret){const alphabet='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';let bits='';for(const c of secret.toUpperCase())bits+=alphabet.indexOf(c).toString(2).padStart(5,'0');const bytes=[];for(let i=0;i+8<=bits.length;i+=8)bytes.push(parseInt(bits.slice(i,i+8),2));const ctr=Buffer.alloc(8);ctr.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));const h=createHmac('sha1',Buffer.from(bytes)).update(ctr).digest();return String((h.readUInt32BE(h[19]&15)&0x7fffffff)%1000000).padStart(6,'0')}
async function fixture(binary){const child=spawn(binary,[],{cwd:root,stdio:['ignore','pipe','pipe']});let buf='',err='';child.stderr.on('data',b=>{err+=b.toString()});const ready=await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(new Error('fixture startup timeout')),20000);child.once('exit',code=>{clearTimeout(timer);reject(new Error(`fixture exited ${code} ${err.slice(0,100)}`))});child.stdout.on('data',b=>{buf+=b.toString();try{const value=JSON.parse(buf.split('\n')[0]);if(value.url){clearTimeout(timer);resolve(value)}}catch{}})});return{child,ready}}
const rows=[]
const coldRows=[]
const serverResources={}
async function procSnapshot(pid){const status=await readFile(`/proc/${pid}/status`,'utf8');const stat=await readFile(`/proc/${pid}/stat`,'utf8');const fields=stat.slice(stat.lastIndexOf(') ')+2).trim().split(/\s+/);return {rss_kib:Number(status.match(/^VmRSS:\s+(\d+) kB/m)?.[1]??0),cpu_ticks_user:Number(fields[11]),cpu_ticks_system:Number(fields[12])}}
for(const [version,binary] of [['before',baseline],['after',current]]){
 const {child,ready}=await fixture(binary)
 serverResources[version]={before:await procSnapshot(child.pid)}
 let authState=null
 try{
 for(const [engineName,engine] of [['chromium',chromium],['webkit',webkit]]){
  const browser=await engine.launch(engineName==='chromium'?{executablePath:process.env.HERDR_CHROME_EXECUTABLE || undefined,args:['--no-sandbox'],headless:true}:{headless:true})
  try{
   const context=await browser.newContext({viewport:{width:1440,height:960},timezoneId:'America/Sao_Paulo',...(authState?{storageState:authState}:{})})
   if(!authState){
    const login=await context.request.post(`${ready.url}/api/v1/auth/login`,{data:{username:ready.username,password:ready.password,otp:otp(ready.totp_secret)},headers:{Origin:ready.url}})
    assert.equal(login.status(),200,'synthetic fixture authentication')
    authState=await context.storageState()
   }
   for(let sample=-2;sample<12;sample++){
    const page=await context.newPage();await page.addInitScript(()=>{window.__perfLCP=[];new PerformanceObserver(list=>{for(const e of list.getEntries())window.__perfLCP.push(e.startTime)}).observe({type:'largest-contentful-paint',buffered:true});document.addEventListener('pointerdown',e=>{if(e.target.closest('button')?.textContent?.includes('GitHub Issues'))window.__perfClickStart=performance.now()},{capture:true})})
    let lcp=null,click=null,unsupported=null
    try{
     const response=await page.goto(ready.url,{waitUntil:'load'});assert.equal(response.status(),200)
     await page.getByRole('heading',{name:'Sessions',exact:true}).waitFor();await page.waitForTimeout(100)
     await page.waitForFunction(()=>window.__perfLCP.length>0,null,{timeout:1000}).catch(()=>{})
     const lcpSupported=await page.evaluate(()=>PerformanceObserver.supportedEntryTypes.includes('largest-contentful-paint'))
     lcp=await page.evaluate(()=>window.__perfLCP.at(-1)??null)
     if(!lcpSupported)unsupported='unsupported-entry-type'
     else if(lcp===null)unsupported='no-lcp-entry'
     await page.getByRole('button',{name:'Tasks',exact:true}).click()
     await page.getByRole('button',{name:'GitHub Issues',exact:true}).click()
     await page.getByRole('button',{name:'Review GitHub integration',exact:true}).waitFor()
     click=await page.evaluate(()=>window.__perfClickStart==null?null:performance.now()-window.__perfClickStart)
     if(click===null)throw new Error('click marker missing')
     const result={version,engine:engineName,sample,lcp_ms:lcp,github_click_ready_ms:click,lcp_supported:lcpSupported,lcp_status:unsupported}
     if(sample===-2)coldRows.push(result)
     if(sample>=0)rows.push(result)
    }finally{await page.close()}
   }
   await context.close()
  }finally{await browser.close()}
 }
 }finally{serverResources[version].after=await procSnapshot(child.pid);child.kill('SIGTERM');await new Promise(resolve=>child.once('exit',resolve))}
}
const evidence={serverResources,status:'complete',date:new Date().toISOString(),sourceHead:head,sourceSnapshotNote:'Current source is a working tree at this HEAD; inspect git status for scope.',harnessSHA256:await sha(fileURLToPath(import.meta.url)),baselineFixtureSHA256:await sha(baseline),currentFixtureSHA256:await sha(current),viewport:[1440,960],samplesPerVersionEngine:12,warmups:2,navigationConditions:'one login per fixture; in-memory auth cookie reused across engines; one context per engine with two excluded navigation warmups, then a new page for each sample',httpCacheMeasured:false,metric:'LCP browser PerformanceObserver; click from pointerdown to GitHub fixture issue visible'}
await writeFile(path.join(out,'browser.json'),JSON.stringify({evidence,coldRows,rows},null,2),{mode:0o600})
console.log('browser performance samples:',rows.length)
