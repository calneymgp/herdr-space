// Synthetic terminal comparison. Never logs or writes fixture credentials or frame bytes.
import assert from 'node:assert/strict'
import {spawn} from 'node:child_process'
import {createHmac,createHash} from 'node:crypto'
import {readFile,mkdir,writeFile} from 'node:fs/promises'
import path from 'node:path'
import {fileURLToPath} from 'node:url'
import {chromium,webkit} from '../../.tools/browser/node_modules/playwright/index.mjs'

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..')
const args=Object.fromEntries(process.argv.slice(2).map(a=>a.replace(/^--/,'').split('=',2)))
const side=args.side, binary=args.binary, samples=Number(args.samples??12), warmups=Number(args.warmups??2)
assert.ok(['before','after'].includes(side));assert.ok(binary);assert.ok(samples>=1&&warmups>=0)
const output=path.join(root,'.state/terminal-load',side,'browser-load.json')
const sha=async p=>createHash('sha256').update(await readFile(p)).digest('hex')
function otp(secret){const alphabet='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';let bits='';for(const c of secret.toUpperCase())bits+=alphabet.indexOf(c).toString(2).padStart(5,'0');const bytes=[];for(let i=0;i+8<=bits.length;i+=8)bytes.push(parseInt(bits.slice(i,i+8),2));const ctr=Buffer.alloc(8);ctr.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));const h=createHmac('sha1',Buffer.from(bytes)).update(ctr).digest();return String((h.readUInt32BE(h[19]&15)&0x7fffffff)%1000000).padStart(6,'0')}
async function fixture(delay){const child=spawn(binary,['--terminal-load','--terminal-frame-delay-ms',String(delay)],{cwd:root,stdio:['ignore','pipe','pipe']});let buf='',err='';child.stderr.on('data',b=>{err+=b.toString()});const ready=await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(new Error('fixture startup timeout')),20000);child.once('exit',code=>{clearTimeout(timer);reject(new Error(`fixture exit ${code}: ${err.slice(0,80)}`))});child.stdout.on('data',b=>{buf+=b.toString();try{const value=JSON.parse(buf.split('\n')[0]);if(value.url){clearTimeout(timer);resolve(value)}}catch{}})});return {child,ready}}
async function resource(pid){const status=await readFile(`/proc/${pid}/status`,'utf8');const stat=await readFile(`/proc/${pid}/stat`,'utf8');const fields=stat.slice(stat.lastIndexOf(') ')+2).trim().split(/\s+/);return {rssKiB:Number(status.match(/^VmRSS:\s+(\d+) kB/m)?.[1]??0),userTicks:Number(fields[11]),systemTicks:Number(fields[12])}}
function quantile(values,q){if(!values.length)return null;const s=[...values].sort((a,b)=>a-b);return s[Math.max(0,Math.ceil(q*s.length)-1)]}
function summarize(rows){const out={};for(const field of ['cachedRenderMs','socketOpenMs','firstFrameMs','firstWriteMs','firstRenderMs','domNewMs']){const xs=rows.map(r=>r[field]).filter(Number.isFinite);out[field]={n:xs.length,min:xs.length?Math.min(...xs):null,max:xs.length?Math.max(...xs):null,p50:quantile(xs,.5),p95:quantile(xs,.95),p99:quantile(xs,.99)}}return out}
const probe=()=>{
 const p={visits:[],active:0,closing:0,peak:0,peakClosing:0,created:0,receivedBytes:0,sentBytes:0,frames:0,messages:0,current:null,old:null};window.__terminalProbe=p
 const Native=window.WebSocket,sockets=new Set(),encoder=new TextEncoder();const count=()=>{p.active=[...sockets].filter(ws=>ws.readyState===Native.OPEN).length;p.closing=[...sockets].filter(ws=>ws.readyState===Native.CLOSING).length;p.peak=Math.max(p.peak,p.active);p.peakClosing=Math.max(p.peakClosing,p.closing)}
 window.WebSocket=class extends Native{constructor(url,protocols){super(url,protocols);if(!String(url).includes('/stream'))return;const visit=p.current;sockets.add(this);p.created++;this.addEventListener('open',()=>{count();if(visit&&visit.socketOpenMs==null)visit.socketOpenMs=performance.now()-visit.start});this.addEventListener('close',()=>{sockets.delete(this);count()});this.addEventListener('message',event=>{p.messages++;const data=event.data,bytes=data instanceof ArrayBuffer?data.byteLength:data instanceof Blob?data.size:typeof data==='string'?encoder.encode(data).byteLength:0;p.receivedBytes+=bytes;if(data instanceof ArrayBuffer||data instanceof Blob){p.frames++;if(visit&&visit.firstFrameMs==null)visit.firstFrameMs=performance.now()-visit.start}})}send(data){if(String(this.url).includes('/stream'))p.sentBytes+=typeof data==='string'?encoder.encode(data).byteLength:data?.byteLength??data?.size??0;return super.send(data)}close(...args){const result=super.close(...args);count();return result}}
 window.addEventListener('herdr:terminal-timing',event=>{const d=event.detail;if(!p.current||!Number.isFinite(d.elapsedMs))return;if(!['cached-render','socket-open','first-frame','first-write','first-render'].includes(d.phase))return;const key={'cached-render':'cachedRenderMs','socket-open':'hookSocketMs','first-frame':'hookFrameMs','first-write':'firstWriteMs','first-render':'firstRenderMs'}[d.phase];p.current[key]=d.elapsedMs;p.current.cache=d.cache},{capture:true})
 document.addEventListener('pointerdown',event=>{if(!event.target.closest('.workspace-tabs [role="tab"]'))return;p.current={start:performance.now(),socketOpenMs:null,firstFrameMs:null,domNewMs:null,domOldMs:null,oldPresentBeforeNew:false};p.visits.push(p.current)},{capture:true})
 const loop=()=>{const v=p.current;if(v){const panel=[...document.querySelectorAll('.terminal-panel')].find(el=>!el.closest('[hidden]')&&el.getClientRects().length);const txt=panel?.querySelector('.xterm-screen')?.textContent??'';const mark=txt.match(/FRAME_\d{6}/)?.[0];if(mark){if(mark===p.old&&v.domOldMs==null){v.domOldMs=performance.now()-v.start;v.oldPresentBeforeNew=v.firstFrameMs==null;v.oldStatus=panel?.querySelector('.terminal-state')?.textContent??''}if(mark!==p.old&&v.firstFrameMs!=null&&v.domNewMs==null)v.domNewMs=performance.now()-v.start}v.status=panel?.querySelector('.terminal-state')?.textContent??'';v.xterms=document.querySelectorAll('.xterm').length}requestAnimationFrame(loop)};requestAnimationFrame(loop)
}
const rows=[],resources=[],captures=[]
for(const delay of [0,150]){
 const {child,ready}=await fixture(delay);const begin=await resource(child.pid)
 let authState=null
 try{
  for(const [engineName,engine] of [['chromium',chromium],['webkit',webkit]]){
   const browser=await engine.launch(engineName==='chromium'?{executablePath:process.env.HERDR_CHROME_EXECUTABLE || undefined,args:['--no-sandbox'],headless:true}:{headless:true})
   try{
    const context=await browser.newContext({viewport:{width:1440,height:960},timezoneId:'America/Sao_Paulo',...(authState?{storageState:authState}:{})})
    if(!authState){const login=await context.request.post(`${ready.url}/api/v1/auth/login`,{data:{username:ready.username,password:ready.password,otp:otp(ready.totp_secret)},headers:{Origin:ready.url}});assert.equal(login.status(),200);authState=await context.storageState()}
    for(let i=-warmups;i<samples;i++){
     const page=await context.newPage();await page.addInitScript(probe)
     try{
      const response=await page.goto(ready.url,{waitUntil:'load'});assert.equal(response.status(),200)
      await page.locator('.space-list').getByRole('button',{name:'Workspace',exact:true}).click()
      const tabs=page.locator('.workspace-tabs');const tab=name=>tabs.getByRole('tab',{name,exact:true});
      await tab('Codex · isolated test').click();await page.waitForFunction(()=>window.__terminalProbe.current?.domNewMs!=null,null,{timeout:10000})
      const cold=await page.evaluate(()=>window.__terminalProbe.visits.at(-1));
      await tab('Review').click();await page.waitForFunction(()=>window.__terminalProbe.current?.domNewMs!=null,null,{timeout:10000})
      const oldMark=await page.evaluate(()=>{const slot=[...document.querySelectorAll('.terminal-cache-slot')].find(el=>el.querySelector('.terminal-title strong')?.textContent==='Codex · isolated test');return slot?.querySelector('.xterm-screen')?.textContent?.match(/FRAME_\d{6}/)?.[0]??null})
      await page.evaluate(value=>{window.__terminalProbe.old=value},oldMark)
      await tab('Codex · isolated test').click();
      let beforeFresh=null
      if(i===0&&delay===150){beforeFresh=await page.evaluate(()=>{const p=window.__terminalProbe;const panel=[...document.querySelectorAll('.terminal-panel')].find(el=>!el.closest('[hidden]')&&el.getClientRects().length);const text=panel?.querySelector('.xterm-screen')?.textContent??'';return {oldVisible:!!p.old&&text.includes(p.old),statusUpdating:(panel?.querySelector('.terminal-state')?.textContent??'').includes('Refreshing'),newFrameSeen:p.current.firstFrameMs!=null}});const target=path.join(root,'.state/terminal-load',side,`${engineName}-${delay}-cached.png`);await page.locator('.terminal-panel:visible').screenshot({path:target});captures.push(path.basename(target))}
      await page.waitForFunction(()=>window.__terminalProbe.current?.domNewMs!=null,null,{timeout:10000})
      const warm=await page.evaluate(()=>window.__terminalProbe.visits.at(-1));
      const state=await page.evaluate(()=>({active:window.__terminalProbe.active,closing:window.__terminalProbe.closing,peak:window.__terminalProbe.peak,peakClosing:window.__terminalProbe.peakClosing,created:window.__terminalProbe.created,receivedBytes:window.__terminalProbe.receivedBytes,sentBytes:window.__terminalProbe.sentBytes,frames:window.__terminalProbe.frames,messages:window.__terminalProbe.messages,xterms:document.querySelectorAll('.xterm').length,visibleXterms:[...document.querySelectorAll('.terminal-panel .xterm')].filter(el=>!el.closest('[hidden]')&&el.getClientRects().length).length,heapBytes:performance.memory?.usedJSHeapSize??null}))
      if(i===0){for(const width of [1440,320]){await page.setViewportSize({width,height:960});const target=path.join(root,'.state/terminal-load',side,`${engineName}-${delay}-${width}-fresh.png`);await page.locator('.terminal-panel:visible').screenshot({path:target});captures.push(path.basename(target))}}
      if(i>=0)rows.push({engine:engineName,delayMs:delay,sample:i,kind:'cold',...cold,resource:state},{engine:engineName,delayMs:delay,sample:i,kind:'warm',oldMarkAvailable:!!oldMark,beforeFresh,...warm,resource:state})
     }finally{await page.close()}
    }
    await context.close()
   }finally{await browser.close()}
  }
 }finally{const end=await resource(child.pid);resources.push({delayMs:delay,before:begin,after:end});child.kill('SIGTERM');await new Promise(resolve=>child.once('exit',resolve))}
}
const groups={};for(const r of rows){const key=`${r.engine}/${r.delayMs}/${r.kind}`;(groups[key]??=[]).push(r)}
const summaries=Object.fromEntries(Object.entries(groups).map(([k,v])=>[k,summarize(v)]))
const evidence={side,date:new Date().toISOString(),harnessSHA256:await sha(fileURLToPath(import.meta.url)),fixtureSHA256:await sha(binary),samples,warmups,viewport:[1440,960],delaysMs:[0,150],definition:'cold=first terminal visit in new page; warm=return to previously visited terminal after visiting another; HTTP cache shared within browser context; first DOM marker on animation frame is a render proxy, not compositor paint',resources,captures}
await mkdir(path.dirname(output),{recursive:true,mode:0o700});await writeFile(output,JSON.stringify({evidence,summaries,rows},null,2),{mode:0o600})
console.log(JSON.stringify({side,rows:rows.length,summaries},null,2))
