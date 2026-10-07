// Opt-in synthetic lifecycle regression; no real agents or persisted terminal data.
import assert from 'node:assert/strict'
import {spawn} from 'node:child_process'
import {createHmac} from 'node:crypto'
import {mkdir} from 'node:fs/promises'
import path from 'node:path'
import {chromium,webkit} from '../../.tools/browser/node_modules/playwright/index.mjs'
const binary=process.argv.find(x=>x.startsWith('--binary='))?.slice(9)
assert.ok(binary,'pass --binary=fixture-load-server')
function otp(secret){const alphabet='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';let bits='';for(const c of secret.toUpperCase())bits+=alphabet.indexOf(c).toString(2).padStart(5,'0');const bytes=[];for(let i=0;i+8<=bits.length;i+=8)bytes.push(parseInt(bits.slice(i,i+8),2));const ctr=Buffer.alloc(8);ctr.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));const h=createHmac('sha1',Buffer.from(bytes)).update(ctr).digest();return String((h.readUInt32BE(h[19]&15)&0x7fffffff)%1000000).padStart(6,'0')}
async function fixture(){const child=spawn(binary,['--terminal-load','--terminal-frame-delay-ms','500'],{stdio:['ignore','pipe','pipe']});let buffer='';const ready=await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(new Error('fixture startup timeout')),20000);child.once('exit',code=>reject(new Error(`fixture exit ${code}`)));child.stdout.on('data',b=>{buffer+=b.toString();try{const value=JSON.parse(buffer.split('\n')[0]);if(value.url){clearTimeout(timer);resolve(value)}}catch{}})});return {child,ready}}
const results=[]
const output=path.resolve('.state/terminal-load/after');await mkdir(output,{recursive:true,mode:0o700})
for(const [engineName,engine] of [['chromium',chromium],['webkit',webkit]]){
 const {child,ready}=await fixture();const browser=await engine.launch(engineName==='chromium'?{executablePath:process.env.HERDR_CHROME_EXECUTABLE || undefined,args:['--no-sandbox'],headless:true}:{headless:true})
 try{
  const context=await browser.newContext({viewport:{width:1440,height:960}})
  const login=await context.request.post(`${ready.url}/api/v1/auth/login`,{data:{username:ready.username,password:ready.password,otp:otp(ready.totp_secret)},headers:{Origin:ready.url}});assert.equal(login.status(),200)
  const page=await context.newPage();await page.addInitScript(()=>{const Native=window.WebSocket,all=new Set();window.__life={active:0,peak:0,closing:0};const count=()=>{window.__life.active=[...all].filter(x=>x.readyState===Native.OPEN).length;window.__life.closing=[...all].filter(x=>x.readyState===Native.CLOSING).length;window.__life.peak=Math.max(window.__life.peak,window.__life.active)};window.WebSocket=class extends Native{constructor(...args){super(...args);if(!String(args[0]).includes('/stream'))return;all.add(this);this.addEventListener('open',count);this.addEventListener('close',()=>{all.delete(this);count()})}close(...args){const x=super.close(...args);count();return x}}})
  await page.goto(ready.url,{waitUntil:'load'});await page.locator('.space-list').getByRole('button',{name:'Workspace',exact:true}).click()
  const tab=name=>page.locator('.workspace-tabs').getByRole('tab',{name,exact:true})
  const waitFrame=()=>page.waitForFunction(()=>{const p=[...document.querySelectorAll('.terminal-panel')].find(x=>!x.closest('[hidden]')&&x.getClientRects().length);return p?.querySelector('.xterm-screen')?.textContent?.includes('FINAL_PROMPT >')},null,{timeout:10000})
  for(const [index,name] of ['Codex · isolated test','Review','Research','Planning'].entries()){await tab(name).click();await waitFrame();assert.equal(await page.locator('.xterm').count(),Math.min(index+1,3),'LRU capacity')}
  const retained=await page.locator('.terminal-cache-slot .terminal-title strong').allTextContents();assert.equal(retained.includes('Codex · isolated test'),false,'A evicted on fourth visit')
  assert.equal((await page.evaluate(()=>window.__life)).peak,1,'at most one OPEN stream')
  await page.locator('.nav-list').getByRole('button',{name:'Tasks',exact:true}).click();await page.waitForFunction(()=>window.__life.active===0);assert.equal(await page.locator('.xterm').count(),3,'page switch retains visual cache')
  await page.locator('.nav-list').getByRole('button',{name:'Sessions',exact:true}).click();await page.locator('.space-list').getByRole('button',{name:'Workspace',exact:true}).click();await tab('Review').click();await waitFrame()
  await page.locator('.terminal-panel:visible .xterm').evaluate(el=>{el.dataset.focusIdentity='history-revision'})
  const historyCard=()=>page.getByRole('dialog').locator('.session-card').filter({has:page.getByRole('heading',{name:'Review',exact:true})})
  await page.getByRole('button',{name:'Space history'}).click();await historyCard().getByRole('button',{name:'Observe'}).click()
  await page.locator('.terminal-panel:visible .terminal-state',{hasText:'Observing'}).waitFor()
  await page.getByRole('button',{name:'Space history'}).click();await historyCard().getByRole('button',{name:'Control',exact:true}).click()
  await page.locator('.terminal-panel:visible .terminal-state',{hasText:'Control active'}).waitFor()
  assert.equal(await page.locator('.terminal-panel:visible .xterm').getAttribute('data-focus-identity'),'history-revision','history mode change retains xterm')
  assert.equal(await page.evaluate(()=>document.activeElement?.classList.contains('xterm-helper-textarea')),true,'history observe to control focuses terminal after cleanup')
  await page.keyboard.type('FOCUS_OK');await page.waitForFunction(()=>[...document.querySelectorAll('.terminal-panel')].find(el=>!el.closest('[hidden]')&&el.getClientRects().length)?.querySelector('.xterm-screen')?.textContent?.includes('FOCUS_OK'),null,{timeout:10000})
  assert.equal((await page.evaluate(()=>window.__life)).peak,1,'history mode change keeps at most one OPEN stream')
  assert.equal(await page.evaluate(()=>document.activeElement?.classList.contains('xterm-helper-textarea')),true,'tab focuses terminal')
  await page.getByRole('heading',{name:'Workspace',exact:true}).click();assert.equal(await page.evaluate(()=>document.activeElement?.classList.contains('xterm-helper-textarea')),false,'outside click releases focus')
  await page.locator('.terminal-screen:visible').hover();await page.mouse.wheel(0,160);assert.equal(await page.evaluate(()=>document.activeElement?.classList.contains('xterm-helper-textarea')),true,'wheel selects terminal')
  await page.getByRole('button',{name:'Full screen',exact:true}).click();assert.equal(await page.locator('.terminal-panel:visible').evaluate(el=>el.classList.contains('terminal-panel--fullscreen')),true)
  await page.getByRole('button',{name:'Exit full screen',exact:true}).click()
  await page.setViewportSize({width:320,height:800})
  await tab('Research').click();await page.locator('.terminal-panel:visible .terminal-state',{hasText:'Control active'}).waitFor()
  await tab('Review').click();await page.locator('.terminal-panel:visible .terminal-state',{hasText:'Refreshing'}).waitFor()
  await page.locator('.terminal-panel:visible').screenshot({path:path.join(output,`${engineName}-mobile-cached.png`)})
  await page.locator('.terminal-panel:visible .terminal-state',{hasText:'Control active'}).waitFor()
  await page.locator('.terminal-panel:visible').screenshot({path:path.join(output,`${engineName}-mobile-fresh.png`)})
  await page.route('**/api/v1/sessions',route=>route.fulfill({status:401,contentType:'application/json',body:'{"error":"unauthorized"}'}))
  await page.getByRole('button',{name:'Refresh data',exact:true}).click();await page.waitForFunction(()=>document.querySelectorAll('.xterm').length===0,null,{timeout:10000})
  results.push({engine:engineName,lru:3,maxOpen:1,pageRelease:true,focus:true,historyModeFocus:true,fullscreen:true,authCleanup:true})
  await context.close()
 }finally{await browser.close();child.kill('SIGTERM');await new Promise(resolve=>child.once('exit',resolve))}
}
console.log(JSON.stringify(results))
