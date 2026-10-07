import { goCommand } from './go-command.mjs'
import assert from 'node:assert/strict'
import { spawn, spawnSync } from 'node:child_process'
import { mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { chromium, webkit } from '../../.tools/browser/node_modules/playwright/index.mjs'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const output = path.join(root, '.state/qa')
await mkdir(output, { recursive: true, mode: 0o700 })
const binary = path.join(output, 'sw-migration-fixture')
const built = spawnSync(goCommand(root), ['build', '-mod=readonly', '-o', binary, './tests/browser/fixture'], { cwd: root, encoding: 'utf8' })
assert.equal(built.status, 0, built.stderr)

async function fixture() {
  const child = spawn(binary, ['--legacy-sw'], { cwd: root, stdio: ['ignore', 'pipe', 'pipe'] })
  let stdout = '', stderr = ''
  child.stderr.on('data', chunk => { stderr += chunk.toString() })
  const ready = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('Fixture startup timeout')), 20000)
    child.once('exit', code => {
      clearTimeout(timer)
      reject(new Error(`Fixture failed (${code}): ${stderr.slice(0, 500)}`))
    })
    child.stdout.on('data', chunk => {
      stdout += chunk.toString()
      try {
        const data = JSON.parse(stdout.split('\n')[0])
        if (data.url) { clearTimeout(timer); resolve({ url: data.url }) }
      } catch {}
    })
  })
  return { child, ...ready }
}

for (const [name, engine] of [['chromium', chromium], ['webkit', webkit]]) {
  const { child, url } = await fixture()
  const browser = await engine.launch(name === 'chromium'
    ? { executablePath: process.env.HERDR_CHROME_EXECUTABLE || undefined, args: ['--no-sandbox'], headless: true }
    : { headless: true })
  const context = await browser.newContext({ serviceWorkers: 'allow' })
  const page = await context.newPage()
  const other = await context.newPage()
  const startup = await context.newPage()
  const network = await context.newPage()
  let fresh

  try {
    await page.goto(url)
    await page.getByRole('heading', { name: 'Force Agent' }).waitFor()
    await page.evaluate(async () => {
      await navigator.serviceWorker.register('/sw.js')
      await navigator.serviceWorker.ready
    })
    await page.reload()
    await page.waitForFunction(() => navigator.serviceWorker.controller !== null)
    assert.equal(await page.getByRole('heading', { name: 'Force Agent' }).count(), 1)

    await other.goto(url)
    await other.waitForFunction(() => navigator.serviceWorker.controller !== null)
    await other.getByRole('textbox', { name: 'Draft' }).fill('draft open in the second tab')

    await startup.goto(url)
    await startup.waitForFunction(() => navigator.serviceWorker.controller !== null)
    // Force Agent's startup helper reloads only the page that observes activation.
    await startup.evaluate(async () => {
      const registration = await navigator.serviceWorker.getRegistration()
      registration.addEventListener('updatefound', () => {
        const worker = registration.installing
        if (!worker) return
        worker.addEventListener('statechange', () => {
          if (worker.state === 'activated') location.reload()
        })
      })
    })

    await page.getByRole('textbox', { name: 'Draft' }).fill('text not yet submitted')
    await page.evaluate(async () => {
      localStorage.setItem('draft-for-migration', 'persistent draft')
      const db = await new Promise((resolve, reject) => {
        const request = indexedDB.open('migration-drafts', 1)
        request.onupgradeneeded = () => request.result.createObjectStore('drafts')
        request.onsuccess = () => resolve(request.result)
        request.onerror = () => reject(request.error)
      })
      await new Promise((resolve, reject) => {
        const tx = db.transaction('drafts', 'readwrite')
        tx.objectStore('drafts').put('saved content', 'note')
        tx.oncomplete = resolve
        tx.onerror = () => reject(tx.error)
      })
      db.close()
      const assets = await caches.open('opencode-assets')
      await assets.put('/old.js', new Response('legacy'))
      const unrelated = await caches.open('user-unrelated-cache')
      await unrelated.put('/saved', new Response('keep'))
    })
    const before = await page.evaluate(() => caches.keys())
    assert.ok(before.some(key => key.startsWith('workbox-precache-v2-')))
    assert.ok(before.includes('opencode-assets'))

    const migrated = await context.request.post(url + '/__fixture/migrate')
    assert.equal(migrated.status(), 204)
    const current = await context.request.get(url + '/sw.js')
    assert.equal(current.status(), 200, 'HERDR must serve a replacement worker at the same URL')
    assert.match(current.headers()['content-type'] || '', /javascript/)
    assert.match(current.headers()['cache-control'] || '', /no-store/)
    assert.equal(current.headers()['service-worker-allowed'], '/')

    await page.evaluate(() => { window.legacyController = navigator.serviceWorker.controller })
    await network.addInitScript(() => {
      const original = ServiceWorkerRegistration.prototype.update
      ServiceWorkerRegistration.prototype.update = function (...args) {
        window.herdrUpdateCalls = (window.herdrUpdateCalls || 0) + 1
        return original.apply(this, args)
      }
    })
    await network.goto(url + '/__fixture/network')
    await network.getByRole('heading', { name: 'Welcome back.' }).waitFor()
    await network.waitForFunction(() => window.herdrUpdateCalls > 0, null, { timeout: 5000 })
    await page.waitForFunction(() => navigator.serviceWorker.controller !== window.legacyController && navigator.serviceWorker.controller?.state === 'activated', null, { timeout: 15000 })

    assert.equal(await page.getByRole('heading', { name: 'Force Agent' }).count(), 1, 'open tab must not be forcibly navigated')
    assert.equal(await page.getByRole('textbox', { name: 'Draft' }).inputValue(), 'text not yet submitted')
    assert.equal(await other.getByRole('heading', { name: 'Force Agent' }).count(), 1, 'second open tab must not be forcibly navigated')
    assert.equal(await other.getByRole('textbox', { name: 'Draft' }).inputValue(), 'draft open in the second tab')
    await startup.getByRole('heading', { name: 'Welcome back.' }).waitFor()

    const state = await page.evaluate(async () => {
      const keys = await caches.keys()
      const db = await new Promise((resolve, reject) => {
        const request = indexedDB.open('migration-drafts')
        request.onsuccess = () => resolve(request.result)
        request.onerror = () => reject(request.error)
      })
      const note = await new Promise((resolve, reject) => {
        const request = db.transaction('drafts').objectStore('drafts').get('note')
        request.onsuccess = () => resolve(request.result)
        request.onerror = () => reject(request.error)
      })
      db.close()
      return { keys, local: localStorage.getItem('draft-for-migration'), note }
    })
    assert.equal(state.local, 'persistent draft')
    assert.equal(state.note, 'saved content')
    assert.ok(state.keys.includes('user-unrelated-cache'))
    assert.ok(!state.keys.includes('opencode-assets'), `legacy asset cache remains: ${JSON.stringify(state.keys)}`)
    assert.ok(!state.keys.some(key => key.startsWith('workbox-') && key.endsWith(url + '/')))

    await page.reload()
    await page.getByRole('heading', { name: 'Welcome back.' }).waitFor()
    assert.equal(await page.evaluate(() => localStorage.getItem('draft-for-migration')), 'persistent draft')
    const htmlCache = await page.evaluate(async () => {
      const keys = await caches.keys()
      const paths = await Promise.all(keys.map(async key =>
        (await (await caches.open(key)).keys()).map(request => new URL(request.url).pathname)))
      return paths.flat().filter(path => path === '/' || path === '/index.html')
    })
    assert.deepEqual(htmlCache, [], 'replacement worker must not cache private HTML')

    fresh = await browser.newContext({ serviceWorkers: 'allow' })
    const firstVisit = await fresh.newPage()
    await firstVisit.goto(url)
    await firstVisit.getByRole('heading', { name: 'Welcome back.' }).waitFor()
    assert.equal(await firstVisit.evaluate(async () => (await navigator.serviceWorker.getRegistrations()).length), 0, 'a new HERDR visitor must not install a worker')
    console.log(`${name}: legacy service worker migration passed`)
  } finally {
    if (fresh) await fresh.close()
    await context.close()
    await browser.close()
    child.kill('SIGTERM')
  }
}
