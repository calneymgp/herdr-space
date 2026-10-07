import { goCommand } from './go-command.mjs'
import assert from 'node:assert/strict'
import { spawn, spawnSync } from 'node:child_process'
import { createHash, createHmac } from 'node:crypto'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import os from 'node:os'
import { chromium, webkit } from '../../.tools/browser/node_modules/playwright/index.mjs'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const output = path.join(root, '.state/qa')
const layoutMeasurements = []
await mkdir(output, { recursive: true, mode: 0o700 })
const build = spawnSync(goCommand(root), ['build', '-mod=readonly', '-o', path.join(output, 'fixture-server'), './tests/browser/fixture'], { cwd: root, encoding: 'utf8' })
assert.equal(build.status, 0, build.stderr)
const sha256 = data => createHash('sha256').update(data).digest('hex')
const sourceListing = spawnSync('git', ['ls-files', '-z', '--', 'go.mod', 'go.sum', 'cmd', 'internal', 'web/src', 'web/package.json', 'web/package-lock.json', 'web/vite.config.ts', 'web/tsconfig.json', 'web/tsconfig.app.json', 'tests/browser/fixture'], { cwd: root, encoding: 'utf8' })
assert.equal(sourceListing.status, 0, sourceListing.stderr)
const sourceFiles = sourceListing.stdout.split('\0').filter(file => file && !file.endsWith('.gitkeep')).sort()
const sourceReads = await Promise.allSettled(sourceFiles.map(file => readFile(path.join(root, file))))
const sourceDigest = createHash('sha256')
for (const [index, result] of sourceReads.entries()) {
  if (result.status === 'rejected') throw result.reason
  sourceDigest.update(sourceFiles[index]).update('\0').update(createHash('sha256').update(result.value).digest())
}
const evidence = {
  status: 'running', startedAt: new Date().toISOString(),
  sourceHead: spawnSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).stdout.trim(),
  sourceSnapshotSHA256: sourceDigest.digest('hex'), sourceFileCount: sourceFiles.length,
  sourceSnapshotMethod: 'SHA256 of sorted tracked Go/web/fixture filenames + NUL + each raw SHA256 digest; excludes embedded dist, whose entry and compiled fixture have separate hashes',
  harnessSHA256: sha256(await readFile(fileURLToPath(import.meta.url))),
  fixtureSHA256: sha256(await readFile(path.join(output, 'fixture-server'))),
  embeddedEntrySHA256: sha256(await readFile(path.join(root, 'internal/webassets/dist/index.html'))),
  layoutWidths: process.argv.includes('--layout-audit') ? [320, 360, 390, 768, 1024, 1440] : [],
}
await writeFile(path.join(output, 'run-evidence.json'), JSON.stringify(evidence, null, 2), { mode: 0o600 })

function otp(secret, offset = 0) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = ''
  for (const c of secret.toUpperCase()) bits += alphabet.indexOf(c).toString(2).padStart(5, '0')
  const bytes = []
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2))
  const counter = Buffer.alloc(8)
  counter.writeBigUInt64BE(BigInt(Math.floor((Date.now() + offset) / 30000)))
  const hash = createHmac('sha1', Buffer.from(bytes)).update(counter).digest()
  return String((hash.readUInt32BE(hash[19] & 15) & 0x7fffffff) % 1000000).padStart(6, '0')
}

async function fixture(bootstrap = false) {
  const child = spawn(path.join(output, 'fixture-server'), bootstrap ? ['--bootstrap'] : [], { cwd: root, stdio: ['ignore', 'pipe', 'pipe'] })
  let text = '', stderr = ''
  child.stderr.on('data', b => { stderr += b.toString() })
  const ready = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('Fixture startup timeout')), 20000)
    child.once('exit', code => { clearTimeout(timer); reject(new Error(`Fixture failed (${code}): ${stderr.slice(0, 500)}`)) })
    child.stdout.on('data', chunk => {
      text += chunk.toString()
      const line = text.split('\n')[0]
      try { const data = JSON.parse(line); if (data.url) { clearTimeout(timer); resolve(data) } } catch {}
    })
  })
  return { child, ready }
}

async function assertTerminalFits(page, label) {
  const geometry = await page.locator('.terminal-screen').evaluate(host => {
    const style = getComputedStyle(host), box = host.getBoundingClientRect()
    const screen = host.querySelector('.xterm-screen').getBoundingClientRect()
    const panel = host.closest('.terminal-panel')
    const fullscreen = panel.classList.contains('terminal-panel--fullscreen')
    const nav = document.querySelector('.mobile-nav')?.getBoundingClientRect()
    const prompt = [...host.querySelectorAll('.xterm-rows > div')].find(row => row.textContent.includes('FINAL_PROMPT >'))?.getBoundingClientRect()
    return {
      screenBottom: screen.bottom, contentBottom: box.bottom - parseFloat(style.paddingBottom), screenRight: screen.right, contentRight: box.right - parseFloat(style.paddingRight),
      panelBottom: panel.getBoundingClientRect().bottom, viewportBottom: fullscreen || !nav ? innerHeight : Math.min(innerHeight, nav.top), promptBottom: prompt?.bottom,
    }
  })
  assert.ok(geometry.screenBottom <= geometry.contentBottom + 1, `${label}: the last terminal row must fit inside its host: ${JSON.stringify(geometry)}`)
  assert.ok(geometry.screenRight <= geometry.contentRight + 1, `${label}: terminal columns must fit inside its host`)
  assert.ok(geometry.panelBottom <= geometry.viewportBottom + 1, `${label}: terminal must fit above the mobile navigation and inside the viewport: ${JSON.stringify(geometry)}`)
  assert.ok(geometry.promptBottom && geometry.promptBottom <= Math.min(geometry.contentBottom, geometry.viewportBottom) + 1, `${label}: the final prompt must be rendered and fully visible: ${JSON.stringify(geometry)}`)
}

async function auditLayout(page, name, view) {
  if (!process.argv.includes('--layout-audit')) return
  const previous = page.viewportSize()
  for (const width of [320, 360, 390, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: width < 760 ? 844 : 960 })
    await page.waitForTimeout(180)
    const measurement = await page.evaluate(() => {
      const visible = element => element.getBoundingClientRect().width > 0 && getComputedStyle(element).visibility !== 'hidden' && !element.closest('[inert]')
      const rect = selector => {
        const element = document.querySelector(selector)
        if (!element || !visible(element)) return null
        const box = element.getBoundingClientRect(), css = getComputedStyle(element)
        return { x: box.x, y: box.y, width: box.width, height: box.height, bottom: box.bottom, padding: [css.paddingTop, css.paddingRight, css.paddingBottom, css.paddingLeft] }
      }
      const smallTargets = [...document.querySelectorAll('button,a[href],select,input,textarea,summary,[role="button"],[role="tab"]')].filter(visible).map(element => {
        const label = element.matches('input[type="checkbox"],input[type="radio"]') ? element.closest('label') || (element.id && document.querySelector(`label[for="${CSS.escape(element.id)}"]`)) : null
        // xterm's helper textarea receives keyboard input; the terminal host is its pointer target.
        const terminalHost = element.matches('.xterm-helper-textarea') ? element.closest('.terminal-screen') : null
        const box = (label || terminalHost || element).getBoundingClientRect()
        return { label: element.getAttribute('aria-label') || element.getAttribute('title') || element.textContent.trim().slice(0, 70), width: box.width, height: box.height }
      }).filter(target => target.width < 24 || target.height < 24)
      const mobileNav = rect('.mobile-nav')
      const topbar = rect('.topbar')
      const topbarControlOverflow = [...document.querySelectorAll('.topbar button')].filter(visible).some(element => {
        const box = element.getBoundingClientRect()
        return box.top < topbar.y - 1 || box.bottom > topbar.bottom + 1
      })
      return { viewport: { width: innerWidth, height: innerHeight }, documentWidth: document.documentElement.scrollWidth, horizontalOverflow: document.documentElement.scrollWidth > innerWidth + 1, content: rect('.content'), topbar, topbarControlOverflow, mobileNav, mobileColumns: mobileNav ? getComputedStyle(document.querySelector('.mobile-nav')).gridTemplateColumns.split(' ').length : null, toast: rect('.toast'), terminal: rect('.terminal-panel'), terminalInputPointerTarget: rect('.terminal-screen'), dialog: rect('[role="dialog"]'), editorTitle: rect('.editor-head .field'), smallTargets }
    })
    layoutMeasurements.push({ browser: name, view, ...measurement })
    await page.screenshot({ path: path.join(output, `${name}-audit-${view}-${width}.png`), fullPage: true })
    assert.equal(measurement.horizontalOverflow, false, `${name}/${view}/${width}: the page must not overflow horizontally`)
    assert.equal(measurement.topbarControlOverflow, false, `${name}/${view}/${width}: header controls must fit vertically`)
  }
  await page.setViewportSize(previous)
  await page.waitForTimeout(180)
}

async function run(name, engine) {
  const { child, ready } = await fixture()
  const browser = await engine.launch(name === 'chromium' ? { executablePath: process.env.HERDR_CHROME_EXECUTABLE || undefined, args: ['--no-sandbox'], headless: true } : { headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 960 }, timezoneId: 'America/Sao_Paulo' })
  const page = await context.newPage()
  const errors = []
    let controlInputObserved = false
    const terminalScrolls = []
    let terminalInputCount = 0
  let terminalSocketCount = 0
  page.on('websocket', socket => {
    if (socket.url().includes('/stream')) terminalSocketCount++
    socket.on('framesent', frame => {
    if (typeof frame.payload !== 'string') return
    try {
      const message = JSON.parse(frame.payload)
      if (message.type === 'input' && message.data === 'x') controlInputObserved = true
      if (message.type === 'input') terminalInputCount++
      if (message.type === 'scroll') terminalScrolls.push(message.delta)
    } catch {}
    })
  })
  page.on('pageerror', e => errors.push(e.name + ': ' + e.message.slice(0, 200)))
  const url = ready.url
  try {
    let r = await context.request.get(`${url}/api/v1/projects`)
    assert.equal(r.status(), 401, 'private API must reject anonymous requests')
    r = await context.request.get(`${url}/api/v1/github/status`)
    assert.equal(r.status(), 401, 'GitHub integration must retain app authentication')
    r = await context.request.post(`${url}/api/v1/auth/login`, { data: { username: ready.username, password: ready.password, otp: '' }, headers: { Origin: url } })
    assert.notEqual(r.status(), 200, 'password alone must not authenticate')
    const before = await (await context.request.get(`${url}/api/v1/auth/status`)).json()
    assert.equal(before.authenticated, false)
    await page.goto(url)
    const manifest = await (await context.request.get(`${url}/manifest.webmanifest`)).json()
    assert.equal(manifest.display, 'standalone')
    for (const size of [192, 512]) {
      const icon = manifest.icons.find(item => item.sizes === `${size}x${size}` && item.type === 'image/png')
      assert.ok(icon, `install manifest must include a ${size}px PNG icon`)
      const image = await (await context.request.get(new URL(icon.src, url).href)).body()
      assert.equal(image.readUInt32BE(16), size)
      assert.equal(image.readUInt32BE(20), size)
    }
    assert.ok(await page.locator('link[rel="apple-touch-icon"]').getAttribute('href'))
    await page.getByLabel('Username', { exact: true }).fill(ready.username)
    await page.getByLabel('Password', { exact: true }).fill(ready.password)
    await page.getByLabel('Authentication or recovery code', { exact: true }).fill(otp(ready.totp_secret))
    await page.getByRole('button', { name: /Sign in/ }).click()
    await page.getByRole('heading', { name: 'Sessions', exact: true }).waitFor()
    const status = await (await context.request.get(`${url}/api/v1/auth/status`)).json()
    assert.equal(status.authenticated, true)
    assert.ok(status.csrf_token)
    const headers = { 'X-CSRF-Token': status.csrf_token, Origin: url }
    r = await context.request.post(`${url}/api/v1/projects`, { data: { name: 'Project without CSRF', path: ready.project_path }, headers: { Origin: url } })
    assert.equal(r.status(), 403, 'mutations require CSRF')
    r = await context.request.post(`${url}/api/v1/projects`, { data: { name: 'Test project', path: ready.project_path }, headers })
    assert.equal(r.status(), 201)
    const project = (await r.json()).item
    r = await context.request.post(`${url}/api/v1/tasks`, { data: { title: 'Validate workspace', description: 'Isolated integration test', project_id: project.id, status: 'todo', due_date: '2026-10-05' }, headers })
    assert.equal(r.status(), 201)
    const task = (await r.json()).item
    r = await context.request.patch(`${url}/api/v1/tasks/${task.id}`, { data: { ...task, status: 'doing' }, headers })
    assert.equal(r.status(), 200)
    r = await context.request.post(`${url}/api/v1/notes`, { data: { title: 'Test note', body: '# Integration\n\n- [ ] Preserve agents\n\n<script>window.__unsafe = true</script>', project_id: project.id }, headers })
    assert.equal(r.status(), 201)
    const note = (await r.json()).item
    r = await context.request.patch(`${url}/api/v1/notes/${note.id}`, { data: { ...note, body: '# New version\n\n<script>window.__unsafe = true</script>', version: note.version }, headers })
    assert.equal(r.status(), 200)
    r = await context.request.patch(`${url}/api/v1/notes/${note.id}`, { data: { ...note, body: 'Conflito', version: note.version }, headers })
    assert.equal(r.status(), 409, 'stale note versions must not overwrite')
    r = await context.request.patch(`${url}/api/v1/preferences`, { data: { theme: 'dark' }, headers })
    assert.equal(r.status(), 200)
    await page.reload()
    await page.getByRole('heading', { name: 'Sessions', exact: true }).waitFor()
    const workspaceInventory = await (await context.request.get(`${url}/api/v1/sessions`)).json()
    assert.ok(workspaceInventory.spaces.length >= 2, 'the real API must expose occupied and empty HERDR spaces')
    const occupiedSpace = workspaceInventory.spaces.find(space => workspaceInventory.items.some(item => item.server_id === space.server_id && item.workspace_id === space.workspace_id))
    const emptySpace = workspaceInventory.spaces.find(space => !workspaceInventory.items.some(item => item.server_id === space.server_id && item.workspace_id === space.workspace_id))
    assert.ok(occupiedSpace && emptySpace)
    const featureBox = await page.locator('.nav-list').boundingBox()
    assert.equal(await page.locator('.nav-list').getByRole('button', { name: 'Projects', exact: true }).count(), 0, 'Spaces replace the Projects management page')
    assert.equal(await page.locator('.space-list').getByRole('button', { name: 'Test project', exact: true }).count(), 0, 'registered project metadata must not create synthetic HERDR Spaces')
    const spacesBox = await page.locator('.spaces-section').boundingBox()
    assert.ok(featureBox.y + featureBox.height <= spacesBox.y + 1, 'features must appear above Spaces in the left sidebar')
    await page.locator('.space-list').getByRole('button', { name: emptySpace.name, exact: true }).click()
    assert.equal(await page.locator('.workspace-tabs').getByRole('tab').count(), 0, 'empty HERDR spaces must remain selectable without borrowed sessions')
    await page.locator('.workspace-empty').waitFor()
    await page.locator('.space-list').getByRole('button', { name: occupiedSpace.name, exact: true }).click()
    await page.getByRole('heading', { name: occupiedSpace.name, exact: true }).waitFor()
    assert.equal(await page.locator('.session-filters,.session-grid').count(), 0, 'a Space workspace must not show inventory or category filters')
    const allScopedSessions = workspaceInventory.items.filter(item => item.server_id === occupiedSpace.server_id && item.workspace_id === occupiedSpace.workspace_id)
    const scopedSessions = allScopedSessions.filter(item => item.alive)
    assert.equal(await page.locator('.workspace-tabs').getByRole('tab').count(), scopedSessions.length)
    for (const item of scopedSessions) {
      const tab = page.locator('.workspace-tabs').getByRole('tab', { name: item.name, exact: true })
      assert.equal(await tab.count(), 1, 'each active tab must preserve its exact session name')
      const state = ['working', 'active'].includes(item.activity) ? 'running' : ['idle', 'done'].includes(item.activity) ? 'waiting' : 'attention'
      const indicator = tab.locator('.workspace-tab-status')
      assert.equal(await indicator.getAttribute('data-state'), state, 'every active tab must display its current HERDR activity')
      assert.ok(await indicator.getAttribute('title'), 'the activity indicator needs a textual tooltip')
      assert.equal(await indicator.getAttribute('aria-hidden'), 'true', 'indicators must preserve tab accessible names')
    }
    for (const item of allScopedSessions.filter(item => !item.alive)) assert.equal(await page.locator('.workspace-tabs').getByRole('tab', { name: item.name, exact: true }).count(), 0, 'ended sessions must not open workspace tabs')
    const spinner = page.locator('.workspace-tab-status[data-state="running"]').first()
    assert.notEqual(await spinner.evaluate(element => getComputedStyle(element).animationName), 'none', 'working activity must spin')
    await page.emulateMedia({ reducedMotion: 'reduce' })
    assert.equal(await spinner.evaluate(element => getComputedStyle(element).animationName), 'none', 'reduced motion must preserve a static activity indicator')
    await page.emulateMedia({ reducedMotion: 'no-preference' })
    await auditLayout(page, name, 'spaces')
    assert.equal(await page.locator('.xterm').count(), 0, 'listing Spaces and tabs must not automatically attach a terminal')
    await page.locator('.space-list').getByRole('button', { name: 'All spaces', exact: true }).click()
    await page.screenshot({ path: path.join(output, `${name}-desktop.png`), fullPage: true })
    await context.request.patch(`${url}/api/v1/preferences`, { data: { theme: 'light' }, headers })
    await page.reload()
    await page.getByRole('heading', { name: 'Sessions', exact: true }).waitFor()
    await page.screenshot({ path: path.join(output, `${name}-desktop-light.png`), fullPage: true })
    await context.request.patch(`${url}/api/v1/preferences`, { data: { theme: 'dark' }, headers })
    await page.reload()
    await page.getByRole('heading', { name: 'Sessions', exact: true }).waitFor()
    await page.locator('.space-list').getByRole('button', { name: occupiedSpace.name, exact: true }).click()
    await page.locator('.workspace-tabs').getByRole('tab', { name: scopedSessions[0].name, exact: true }).click()
    await page.locator('.xterm').waitFor()
    await page.waitForTimeout(600)
    await page.getByText('Control active', { exact: true }).waitFor({ timeout: 3000 })
    assert.equal(await page.evaluate(() => document.activeElement?.classList.contains('xterm-helper-textarea')), true, 'the tab must open an immediately focused, usable control terminal')
    assert.equal(await page.locator('.session-filters,.session-grid').count(), 0, 'terminal workspaces must not show inventory or category filters')
    await assertTerminalFits(page, 'desktop initial control')
    await page.screenshot({ path: path.join(output, `${name}-workspace.png`), fullPage: true })
    await auditLayout(page, name, 'terminal')
    await page.setViewportSize({ width: 1100, height: 800 })
    await page.waitForTimeout(350)
    await assertTerminalFits(page, 'desktop resize')
    const showTerminalHelpers = async () => {
      const toggle = page.getByRole('button', { name: 'Extra keys', exact: true })
      if (await toggle.getAttribute('aria-pressed') !== 'true') await toggle.click()
      assert.equal(await toggle.getAttribute('aria-pressed'), 'true', 'terminal helpers must be visible')
    }
    await showTerminalHelpers()
    await page.waitForTimeout(250)
    await assertTerminalFits(page, 'desktop keyboard helpers')
    await page.getByRole('button', { name: 'Full screen', exact: true }).click()
    await page.waitForTimeout(250)
    await assertTerminalFits(page, 'desktop fullscreen')
    await page.getByRole('button', { name: 'Exit full screen', exact: true }).click()
    await page.waitForTimeout(250)
    await assertTerminalFits(page, 'desktop leaving fullscreen')
    await page.getByRole('button', { name: 'Disconnect terminal', exact: true }).click()
    await page.getByRole('button', { name: 'Observe', exact: true }).click()
    await page.getByText('Observing', { exact: true }).waitFor({ timeout: 3000 })
    assert.equal(await page.getByRole('button', { name: 'Extra keys', exact: true }).getAttribute('aria-pressed'), 'true', 'reopening the retained terminal must preserve visible helpers')
    await showTerminalHelpers()
    await page.getByRole('button', { name: 'Scroll up', exact: true }).click()
    const observeScrollBefore = terminalScrolls.length
    const observeInputBefore = terminalInputCount
    const observeBox = await page.locator('.terminal-screen').boundingBox()
    await page.mouse.move(observeBox.x + observeBox.width / 2, observeBox.y + observeBox.height / 2)
    await page.mouse.wheel(0, -160)
    await page.waitForTimeout(200)
    assert.equal(await page.getByText('Observing', { exact: true }).count(), 1, 'read-only resize and scrolling must preserve the observer connection')
    assert.equal(terminalScrolls.length, observeScrollBefore, 'observing scroll must stay local instead of sending a control mutation')
    assert.equal(terminalInputCount, observeInputBefore, 'observing wheel must never send input')
    await page.getByRole('button', { name: 'Disconnect terminal', exact: true }).click()
    const inv = await (await context.request.get(`${url}/api/v1/sessions`)).json()
    assert.equal(inv.items[0].alive, true, 'disconnect must preserve the agent')
    const wideStream = await page.evaluate(({ id, csrf }) => new Promise(resolve => {
      const streamURL = new URL(`/api/v1/terminals/${encodeURIComponent(id)}/stream`, location.href)
      streamURL.protocol = 'ws:'
      streamURL.search = '?mode=observe&cols=500&rows=30'
      const socket = new WebSocket(streamURL)
      const timer = setTimeout(() => { socket.close(); resolve(false) }, 3000)
      socket.onopen = () => socket.send(JSON.stringify({ type: 'hello', csrf_token: csrf }))
      socket.onmessage = event => { clearTimeout(timer); socket.close(); resolve(typeof event.data !== 'string') }
      socket.onerror = () => { clearTimeout(timer); socket.close(); resolve(false) }
    }), { id: inv.items[0].id, csrf: status.csrf_token })
    assert.equal(wideStream, true, 'protected terminal must accept the agreed 500-column maximum')
    await page.getByRole('button', { name: 'Control', exact: true }).first().click()
    await page.getByText('Control active', { exact: true }).waitFor()
    assert.equal(await page.evaluate(() => document.activeElement?.classList.contains('xterm-helper-textarea')), true, 'control must focus the terminal without a second click')
    assert.equal(await page.locator('.workspace-tabs').getByRole('tab', { name: scopedSessions[0].name, exact: true }).count(), 1, 'switching terminal mode must keep one named tab per session')
    const connectedSocketCount = terminalSocketCount
    await page.locator('.workspace-tabs').getByRole('tab', { name: scopedSessions[0].name, exact: true }).click()
    await page.waitForTimeout(200)
    assert.equal(await page.getByText('Control active', { exact: true }).count(), 1, 'clicking the active tab must preserve terminal control')
    assert.equal(terminalSocketCount, connectedSocketCount, 'clicking the active tab must not reconnect')
    await page.keyboard.type('x')
    await page.waitForTimeout(200)
    assert.equal(controlInputObserved, true, 'control input must reach the protected WebSocket')
    await page.getByRole('heading', { name: occupiedSpace.name, exact: true }).click()
    assert.equal(await page.evaluate(() => document.activeElement?.classList.contains('xterm-helper-textarea')), false, 'clicking outside must release terminal keyboard focus')
    const inputBeforeOutsideTyping = terminalInputCount
    await page.keyboard.type('y')
    await page.waitForTimeout(100)
    assert.equal(terminalInputCount, inputBeforeOutsideTyping, 'typing outside must not reach the agent')
    const pageScrollBefore = await page.evaluate(() => ({ window: scrollY, content: document.querySelector('.content').scrollTop }))
    const screenBox = await page.locator('.terminal-screen').boundingBox()
    await page.mouse.move(screenBox.x + screenBox.width / 2, screenBox.y + screenBox.height / 2)
    const scrollBefore = terminalScrolls.length
    await page.mouse.wheel(0, -160)
    await page.waitForTimeout(250)
    await page.mouse.wheel(0, 160)
    await page.waitForTimeout(250)
    assert.ok(terminalScrolls.slice(scrollBefore).some(delta => delta < 0) && terminalScrolls.slice(scrollBefore).some(delta => delta > 0), 'native wheel must scroll the HERDR terminal in both directions')
    assert.equal(await page.evaluate(() => document.activeElement?.classList.contains('xterm-helper-textarea')), true, 'wheel interaction must select and focus the terminal')
    assert.deepEqual(await page.evaluate(() => ({ window: scrollY, content: document.querySelector('.content').scrollTop })), pageScrollBefore, 'terminal wheel must not scroll the web page')
    assert.equal(terminalInputCount, inputBeforeOutsideTyping, 'wheel must not leak duplicate xterm mouse input')
    assert.equal(terminalSocketCount, connectedSocketCount, 'selecting and deselecting the terminal must preserve its connection')
    await page.locator('.terminal-screen').click({ position: { x: 60, y: 40 } })
    assert.equal(await page.evaluate(() => document.activeElement?.classList.contains('xterm-helper-textarea')), true, 'clicking inside must return keyboard focus')
    await page.getByRole('button', { name: 'Disconnect terminal', exact: true }).click()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.waitForTimeout(350)
    assert.equal(await page.locator('.sidebar').evaluate(element => element.inert), true, 'closed mobile navigation must be inert')
    await page.locator('.workspace-tabs').getByRole('tab', { name: scopedSessions[0].name, exact: true }).click()
    await page.getByText('Control active', { exact: true }).waitFor()
    await page.waitForTimeout(250)
    await assertTerminalFits(page, 'mobile initial control')
    const touchScrollBefore = terminalScrolls.length
    await page.locator('.terminal-screen').evaluate(element => {
      const dispatch = (type, y) => {
        const event = new Event(type, { bubbles: true, cancelable: true })
        Object.defineProperty(event, 'touches', { value: type === 'touchend' ? [] : [{ clientX: 100, clientY: y }] })
        element.dispatchEvent(event)
      }
      dispatch('touchstart', 150); dispatch('touchmove', 102); dispatch('touchend', 102)
      dispatch('touchstart', 102); dispatch('touchmove', 150); dispatch('touchend', 150)
    })
    await page.waitForTimeout(150)
    const touchScrolls = terminalScrolls.slice(touchScrollBefore)
    assert.ok(touchScrolls[0] > 0 && touchScrolls[1] < 0, 'swiping upward must advance the viewport and downward must return through HERDR scroll')
    await showTerminalHelpers()
    await page.waitForTimeout(250)
    await assertTerminalFits(page, 'mobile keyboard helpers')
    assert.equal(await page.locator('.terminal-state').isVisible(), true, 'mobile terminal connection status must remain visible')
    await page.screenshot({ path: path.join(output, `${name}-mobile.png`), fullPage: true })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true, 'mobile page must not overflow horizontally')
    await page.locator('.mobile-nav').getByRole('button', { name: 'Notes', exact: true }).click()
    await page.getByText('Test note', { exact: true }).first().waitFor()
    await page.getByRole('tab', { name: /Test note/ }).first().click()
    await page.getByRole('tab', { name: 'Preview', exact: true }).click()
    assert.equal(await page.evaluate(() => Boolean(window.__unsafe)), false, 'Markdown must never execute HTML')
    await page.setViewportSize({ width: 1440, height: 960 })
    await page.waitForTimeout(350)
    await page.getByRole('tab', { name: 'Write', exact: true }).click()
    const draft = 'Draft preserved after network failure and navigation'
    await page.route('**/api/v1/notes/*', route => route.request().method() === 'PATCH' ? route.abort() : route.continue())
    await page.getByLabel('Note content in Markdown', { exact: true }).fill(draft)
    await page.getByText('Save error', { exact: true }).waitFor()
    await page.getByRole('button', { name: 'Tasks', exact: true }).click()
    await page.getByRole('button', { name: 'Notes', exact: true }).click()
    await page.getByRole('tab', { name: /Test note/ }).first().click()
    await page.getByRole('tab', { name: 'Write', exact: true }).click()
    assert.equal(await page.getByLabel('Note content in Markdown', { exact: true }).inputValue(), draft, 'failed drafts must survive page navigation')
    await page.unroute('**/api/v1/notes/*')
    await page.getByRole('button', { name: 'Save now', exact: true }).click()
    await page.getByText('Saved', { exact: true }).waitFor()
    const saved = await (await context.request.get(`${url}/api/v1/notes`)).json()
    assert.equal(saved.items.find(item => item.id === note.id).body, draft)
    await page.getByRole('button', { name: 'Tasks', exact: true }).click()
    await page.getByText('Validate workspace', { exact: true }).first().waitFor()
    assert.equal(await page.locator('.task-card').filter({ hasText: 'Validate workspace' }).locator('time').innerText(), '10/05/2026', 'date-only deadlines must preserve the selected day in a time zone behind UTC')
    const contrast = await page.locator('.kanban-column').first().evaluate(column => {
      const channel = value => { value /= 255; return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4 }
      const luminance = color => { const rgb = color.match(/[\d.]+/g).slice(0, 3).map(Number).map(channel); return rgb[0] * 0.2126 + rgb[1] * 0.7152 + rgb[2] * 0.0722 }
      const foreground = luminance(getComputedStyle(column.querySelector('h2')).color)
      const background = luminance(getComputedStyle(column).backgroundColor)
      return (Math.max(foreground, background) + 0.05) / (Math.min(foreground, background) + 0.05)
    })
    assert.ok(contrast >= 4.5, 'dark kanban heading must meet 4.5:1 text contrast')
    await Promise.all([
      page.waitForResponse(response => response.url().endsWith('/api/v1/tasks') && response.status() === 200),
      page.getByLabel('Move Validate workspace', { exact: true }).selectOption('done'),
    ])
    const moved = await (await context.request.get(`${url}/api/v1/tasks`)).json()
    assert.equal(moved.items.find(item => item.id === task.id).status, 'done', 'kanban control must persist the task status')
    await page.getByRole('button', { name: 'Notes', exact: true }).click()
    await page.getByRole('button', { name: 'New task', exact: true }).click()
    await page.getByRole('dialog').getByLabel('Title', { exact: true }).fill('Task created in the interface')
    await page.getByRole('button', { name: 'Save task', exact: true }).click()
    await page.getByRole('button', { name: 'Tasks', exact: true }).click()
    await page.getByText('Task created in the interface', { exact: true }).waitFor()
    await page.screenshot({ path: path.join(output, `${name}-tasks.png`), fullPage: true })
    await auditLayout(page, name, 'tasks')
    // Exercise the real protected API with an in-memory GitHub provider. No
    // request in this scenario can create or edit a real GitHub issue.
    r = await context.request.get(`${url}/api/v1/github/repositories`)
    assert.equal(r.status(), 200)
    const githubRepositories = (await r.json()).items
    assert.equal(githubRepositories.length, 1)
    assert.equal(githubRepositories[0].repository, 'herdr-fixture/workspace')
    r = await context.request.post(`${url}/api/v1/github/issues`, { data: { space_id: occupiedSpace.id, title: 'Without CSRF', body: '' }, headers: { Origin: url } })
    assert.equal(r.status(), 403, 'GitHub mutations require the same app CSRF protection')
    await page.getByRole('button', { name: 'GitHub Issues', exact: true }).click()
    await page.getByRole('button', { name: 'Review GitHub integration', exact: true }).waitFor()
    await page.getByRole('button', { name: 'Review GitHub integration', exact: true }).click()
    await page.getByRole('dialog').getByLabel('Title', { exact: true }).fill('Review edited integration')
    await page.getByRole('dialog').getByLabel('Description', { exact: true }).fill('Description preserved across state change')
    const [editResponse] = await Promise.all([
      page.waitForResponse(response => response.url().endsWith('/api/v1/github/issues/1') && response.request().method() === 'PATCH'),
      page.getByRole('dialog').getByRole('button', { name: 'Save changes', exact: true }).click(),
    ])
    assert.equal(editResponse.status(), 200)
    assert.equal(Object.hasOwn(editResponse.request().postDataJSON(), 'state'), false, 'editing content must not overwrite a concurrently changed state')
    await page.getByRole('button', { name: 'Review edited integration', exact: true }).waitFor()
    const [closeResponse] = await Promise.all([
      page.waitForResponse(response => response.url().endsWith('/api/v1/github/issues/1') && response.request().method() === 'PATCH'),
      page.getByRole('button', { name: 'Close issue #1', exact: true }).click(),
    ])
    assert.equal(closeResponse.status(), 200)
    assert.deepEqual(Object.keys(closeResponse.request().postDataJSON()).sort(), ['space_id', 'state'], 'closing an issue must preserve its title and body')
    await page.getByRole('button', { name: 'Closed', exact: true }).click()
    await page.getByRole('button', { name: 'Review edited integration', exact: true }).waitFor()
    await Promise.all([
      page.waitForResponse(response => response.url().endsWith('/api/v1/github/issues/1') && response.status() === 200),
      page.getByRole('button', { name: 'Reopen issue #1', exact: true }).click(),
    ])
    await page.getByRole('button', { name: 'Open', exact: true }).click()
    await page.getByRole('button', { name: 'Review edited integration', exact: true }).waitFor()
    await page.getByRole('button', { name: 'Create issue', exact: true }).click()
    await page.getByRole('dialog').getByLabel('Title', { exact: true }).fill('Issue created by the workspace')
    await page.getByRole('dialog').getByLabel('Description', { exact: true }).fill('Synthetic data without external writes')
    await page.route('**/api/v1/github/issues', route => route.request().method() === 'POST' ? route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"Simulated GitHub failure"}' }) : route.continue())
    await page.getByRole('dialog').getByRole('button', { name: 'Create issue', exact: true }).click()
    await page.getByRole('dialog').getByRole('alert').waitFor()
    assert.equal(await page.getByRole('dialog').getByLabel('Title', { exact: true }).inputValue(), 'Issue created by the workspace', 'failed writes must keep the issue draft')
    assert.equal((await (await context.request.get(`${url}/api/v1/auth/status`)).json()).authenticated, true, 'GitHub unavailability must not log out the app')
    await page.unroute('**/api/v1/github/issues')
    await page.getByRole('dialog').getByRole('button', { name: 'Refresh before retrying', exact: true }).click()
    await page.getByRole('dialog').getByRole('region', { name: 'Updated issues', exact: true }).waitFor()
    assert.equal(await page.getByRole('dialog').getByRole('region', { name: 'Updated issues', exact: true }).getByText('Issue created by the workspace', { exact: true }).count(), 0, 'the refreshed list must be reviewed before a deliberate retry')
    await Promise.all([
      page.waitForResponse(response => response.url().endsWith('/api/v1/github/issues') && response.request().method() === 'POST' && response.status() === 201),
      page.getByRole('dialog').getByRole('button', { name: 'Create anyway', exact: true }).click(),
    ])
    await page.getByRole('button', { name: 'Issue created by the workspace', exact: true }).waitFor()
    await page.screenshot({ path: path.join(output, `${name}-github-issues.png`), fullPage: true })
    await auditLayout(page, name, 'github')
    await page.setViewportSize({ width: 390, height: 844 })
    await page.waitForTimeout(150)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true, 'GitHub issues must fit the mobile viewport')
    await page.screenshot({ path: path.join(output, `${name}-github-mobile.png`), fullPage: true })
    await page.setViewportSize({ width: 1440, height: 960 })
    await page.getByRole('button', { name: 'Local tasks', exact: true }).click()
    const notesBeforeQuickCreate = await (await context.request.get(`${url}/api/v1/notes`)).json()
    await Promise.all([
      page.waitForResponse(response => response.url().endsWith('/api/v1/notes') && response.request().method() === 'POST' && response.status() === 201),
      page.getByRole('button', { name: 'New note', exact: true }).click(),
    ])
    await page.getByLabel('Title', { exact: true }).waitFor()
    assert.equal(await page.getByRole('tab', { name: 'Write', exact: true }).getAttribute('aria-selected'), 'true', 'one-click note creation must open the writing editor')
    const notesAfterQuickCreate = await (await context.request.get(`${url}/api/v1/notes`)).json()
    assert.equal(notesAfterQuickCreate.items.length, notesBeforeQuickCreate.items.length + 1, 'one create request must create exactly one note')
    await page.getByLabel('Title', { exact: true }).fill('Quick test note')
    await page.getByLabel('Note content in Markdown', { exact: true }).fill('An idea written from the global shortcut.')
    await page.getByText('Saved', { exact: true }).waitFor()
    const quickNote = (await (await context.request.get(`${url}/api/v1/notes`)).json()).items.find(item => item.title === 'Quick test note')
    assert.equal(quickNote.body, 'An idea written from the global shortcut.')
    await page.screenshot({ path: path.join(output, `${name}-notes.png`), fullPage: true })
    await auditLayout(page, name, 'notes')
    await page.getByRole('button', { name: 'New agent', exact: true }).click()
    await page.getByRole('dialog').waitFor()
    await page.getByRole('dialog').getByLabel('Agent', { exact: true }).waitFor()
    await page.screenshot({ path: path.join(output, `${name}-new-agent-dialog.png`), fullPage: true })
    await auditLayout(page, name, 'new-agent-dialog')
    await page.getByRole('dialog').getByLabel('Space', { exact: true }).selectOption(occupiedSpace.id)
    const [spaceProjectResponse, startResponse] = await Promise.all([
      page.waitForResponse(response => response.url().endsWith('/project') && response.url().includes('/api/v1/spaces/') && response.status() === 200),
      page.waitForResponse(response => response.url().endsWith('/api/v1/sessions/start') && response.request().method() === 'POST'),
      page.getByRole('dialog').getByRole('button', { name: 'Start agent', exact: true }).click(),
    ])
    assert.equal((await spaceProjectResponse.json()).item.id, project.id, 'Space association must reuse existing metadata')
    assert.equal(startResponse.status(), 201, 'new agent must launch from a native Space without Projects management')
    assert.equal((await startResponse.json()).item.cwd, ready.project_path, 'launch must use the selected Space directory')
    await page.getByRole('dialog').waitFor({ state: 'hidden' })
    await page.route('**/api/v1/sessions', route => route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"unavailable"}' }))
    await page.route('**/api/v1/events', route => route.abort())
    await page.reload()
    await page.getByText('Sessions unavailable. The inventory may be out of date.', { exact: true }).waitFor()
    await page.getByRole('button', { name: 'Tasks', exact: true }).click()
    await page.getByText('Validate workspace', { exact: true }).first().waitFor()
    await page.getByText('Task created in the interface', { exact: true }).waitFor()
    await page.getByRole('button', { name: 'Notes', exact: true }).click()
    await page.getByRole('tab', { name: /Test note/ }).first().click()
    await page.getByRole('tab', { name: 'Write', exact: true }).click()
    assert.equal(await page.getByLabel('Note content in Markdown', { exact: true }).inputValue(), draft, 'stored notes remain accessible during session discovery failure')
    await page.unroute('**/api/v1/sessions')
    await page.unroute('**/api/v1/events')
    await page.getByRole('button', { name: 'Refresh data', exact: true }).click()
    await page.locator('.nav-list').getByRole('button', { name: /^Sessions/ }).click()
    await page.getByRole('button', { name: /Observe/ }).first().waitFor()
    assert.equal(errors.length, 0, `unexpected browser errors: ${errors.join('; ')}`)
    await page.route('**/api/v1/auth/logout', route => route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"unavailable"}' }))
    await Promise.all([
      page.waitForResponse(response => response.url().endsWith('/api/v1/auth/logout') && response.status() === 503),
      page.getByRole('button', { name: /Sign out of Space/ }).click(),
    ])
    assert.equal(await page.getByRole('heading', { name: 'Sessions', exact: true }).isVisible(), true, 'failed revocation must not falsely show successful logout')
    await page.setViewportSize({ width: 390, height: 844 })
    await page.locator('.toast').waitFor()
    const toastBox = await page.locator('.toast').boundingBox()
    const mobileNavBox = await page.locator('.mobile-nav').boundingBox()
    assert.ok(toastBox.y + toastBox.height <= mobileNavBox.y, 'global errors must remain above mobile navigation')
    const navHit = await page.locator('.mobile-nav button').first().evaluate(element => {
      const box = element.getBoundingClientRect()
      return element.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2))
    })
    assert.equal(navHit, true, 'the visible toast must not intercept mobile navigation')
    for (const width of [390, 1440]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 960 })
      await page.getByRole('button', { name: 'New task', exact: true }).click()
      await page.getByRole('dialog').waitFor()
      const modalHits = await page.evaluate(() => {
        const toast = document.querySelector('.toast')?.getBoundingClientRect()
        const close = document.querySelector('[role="dialog"] .dialog-close')
        const closeBox = close.getBoundingClientRect()
        const toastPoint = toast && document.elementFromPoint(toast.x + toast.width / 2, toast.y + toast.height / 2)
        return {
          toastUnderModal: !!toastPoint?.closest('.dialog-overlay,[role="dialog"]'),
          closeReachable: close.contains(document.elementFromPoint(closeBox.x + closeBox.width / 2, closeBox.y + closeBox.height / 2)),
        }
      })
      assert.equal(modalHits.toastUnderModal, true, `${width}: a modal must receive pointer events above the toast`)
      assert.equal(modalHits.closeReachable, true, `${width}: the modal close button must remain reachable`)
      await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click()
    }
    await page.setViewportSize({ width: 390, height: 844 })
    assert.equal(await page.locator('.mobile-nav').evaluate(element => getComputedStyle(element).gridTemplateColumns.split(' ').length), 4, 'mobile navigation must distribute the four visible tools evenly')
    await auditLayout(page, name, 'failure')
    await page.setViewportSize({ width: 1440, height: 960 })
    await page.unroute('**/api/v1/auth/logout')
    await page.getByRole('button', { name: /Sign out of Space/ }).click()
    await page.getByRole('button', { name: /Sign in/ }).waitFor()
    r = await context.request.get(`${url}/api/v1/sessions`)
    assert.equal(r.status(), 401)
    return { engine: name, passed: true, scenarios: ['install-manifest-icons', 'mandatory-two-factors', 'csrf', 'project-reference-compatibility', 'herdr-spaces-and-session-name-tabs', 'empty-space-filter', 'one-click-session-tab', 'one-click-global-creation', 'tasks-kanban', 'task-form', 'date-only-deadline', 'dark-kanban-contrast', 'independent-collection-recovery', 'note-conflict', 'note-draft-recovery', 'terminal-observe-resize-scroll', 'terminal-control-input', 'terminal-outside-focus-release', 'terminal-native-wheel', 'terminal-touch-directions', 'native-spaces-no-projects-page', 'space-based-agent-start', 'github-issue-crud', 'github-failure-draft', 'github-mobile-layout', 'terminal-500-columns', 'terminal-disconnect', 'mobile-layout', 'mobile-bottom-navigation', 'mobile-terminal-status', 'inert-mobile-navigation', 'markdown-xss', 'logout-failure', 'logout'] }
  } catch (error) {
    await page.screenshot({ path: path.join(output, `${name}-failed.png`), fullPage: true }).catch(() => {})
    await writeFile(path.join(output, `${name}-failed-body.txt`), await page.locator('body').innerText().catch(() => ''), { mode: 0o600 })
    const geometry = await page.evaluate(() => ({
      viewport: innerWidth, documentWidth: document.documentElement.scrollWidth,
      outside: [...document.querySelectorAll('body *')].map(element => {
        const box = element.getBoundingClientRect()
        return { tag: element.tagName, class: element.className?.toString(), x: box.x, right: box.right, width: box.width }
      }).filter(box => box.width > 0 && (box.x < -1 || box.right > innerWidth + 1)),
    })).catch(() => null)
    if (geometry) await writeFile(path.join(output, `${name}-failed-geometry.json`), JSON.stringify(geometry, null, 2), { mode: 0o600 })
    if (errors.length) console.log(`${name} page errors: ${errors.join('; ')}`)
    throw error
  } finally {
    await context.close(); await browser.close(); child.kill('SIGTERM')
    await new Promise(resolve => { if (child.exitCode !== null) return resolve(); child.once('exit', resolve); setTimeout(() => { child.kill('SIGKILL'); resolve() }, 6000).unref() })
  }
}

const results = []
try {
if (!process.argv.includes('--persistence-only')) {
for (const [name, engine] of [['chromium', chromium], ['webkit', webkit]]) {
  results.push(await run(name, engine))
  console.log(`${name}: browser integration scenarios passed`)
}
for (const [name, engine] of [['chromium', chromium], ['webkit', webkit]]) {
  results.push(await runBootstrap(name, engine))
  console.log(`${name}: protected first-run enrollment passed`)
}
}
for (const [name, engine] of [['chromium', chromium], ['webkit', webkit]]) {
  results.push(await runPersistence(name, engine))
  console.log(`${name}: seven-day login survived browser restart`)
}
await writeFile(path.join(output, 'results.json'), JSON.stringify(results, null, 2), { mode: 0o600 })
if (layoutMeasurements.length) await writeFile(path.join(output, 'layout-measurements.json'), JSON.stringify(layoutMeasurements, null, 2), { mode: 0o600 })
await writeFile(path.join(output, 'run-evidence.json'), JSON.stringify({ ...evidence, status: 'passed', finishedAt: new Date().toISOString(), scenarioCount: results.length, layoutCount: layoutMeasurements.length }, null, 2), { mode: 0o600 })
console.log('Browser evidence: .state/qa/results.json and screenshots')
} catch (error) {
  await writeFile(path.join(output, 'run-evidence.json'), JSON.stringify({ ...evidence, status: 'failed', finishedAt: new Date().toISOString() }, null, 2), { mode: 0o600 })
  throw error
}

async function runPersistence(name, engine) {
  const { child, ready } = await fixture()
  const profile = await mkdtemp(path.join(os.tmpdir(), 'herdr-session-browser-'))
  const options = { viewport: { width: 1100, height: 800 }, headless: true, ...(name === 'chromium' ? { executablePath: process.env.HERDR_CHROME_EXECUTABLE || undefined, args: ['--no-sandbox'] } : {}) }
  let context
  try {
    context = await engine.launchPersistentContext(profile, options)
    let page = context.pages()[0] || await context.newPage()
    await page.goto(ready.url)
    await page.getByLabel('Username', { exact: true }).fill(ready.username)
    await page.getByLabel('Password', { exact: true }).fill(ready.password)
    await page.getByLabel('Authentication or recovery code', { exact: true }).fill(otp(ready.totp_secret))
    await page.getByRole('button', { name: /Sign in/ }).click()
    await page.getByRole('heading', { name: 'Sessions', exact: true }).waitFor()
    const cookie = (await context.cookies()).find(item => item.name === 'herdr_session')
    assert.ok(cookie?.httpOnly && cookie.sameSite === 'Strict', 'persistent login must keep HttpOnly and SameSite protections')
    const remaining = cookie.expires - Date.now() / 1000
    assert.ok(remaining > 7 * 24 * 60 * 60 - 30 && remaining <= 7 * 24 * 60 * 60 + 2, 'browser cookie must persist for seven days')
    await context.close()
    context = await engine.launchPersistentContext(profile, options)
    page = context.pages()[0] || await context.newPage()
    await page.goto(ready.url)
    await page.getByRole('heading', { name: 'Sessions', exact: true }).waitFor()
    const status = await (await context.request.get(`${ready.url}/api/v1/auth/status`)).json()
    assert.equal(status.authenticated, true, 'reopening the browser must not require another password or OTP')
    const refreshed = (await context.cookies()).find(item => item.name === 'herdr_session')
    assert.ok(refreshed.expires <= cookie.expires + 1, 'status checks must preserve the absolute expiry instead of sliding it forward')
    return { engine: name, persistence: true, passed: true, scenarios: ['seven-day-httpOnly-cookie', 'real-browser-profile-restart', 'absolute-expiry-retained'] }
  } finally {
    await context?.close().catch(() => {})
    child.kill('SIGTERM')
    await new Promise(resolve => { if (child.exitCode !== null) return resolve(); child.once('exit', resolve); setTimeout(() => { child.kill('SIGKILL'); resolve() }, 6000).unref() })
    await rm(profile, { recursive: true, force: true })
  }
}

async function runBootstrap(name, engine) {
  const { child, ready } = await fixture(true)
  const browser = await engine.launch(name === 'chromium' ? { executablePath: process.env.HERDR_CHROME_EXECUTABLE || undefined, args: ['--no-sandbox'], headless: true } : { headless: true })
  const context = await browser.newContext({ viewport: { width: 390, height: 844 } })
  const page = await context.newPage()
  let step = 'Access identity gate'
  try {
    const denied = await context.request.post(`${ready.url}/api/v1/auth/setup/begin`, { data: { username: ready.username, password: ready.password }, headers: { Origin: ready.url } })
    assert.equal(denied.status(), 403)
    await page.goto(ready.url)
    await page.getByRole('heading', { name: 'Authorized access required', exact: true }).waitFor()
    await page.evaluate(() => { window.setupPageMarker = true })
    let releaseStatus
    const statusGate = new Promise(resolve => { releaseStatus = resolve })
    await page.route('**/api/v1/auth/status', route => statusGate.then(() => route.continue()))
    const [stillDenied] = await Promise.all([
      page.waitForResponse(response => response.url().endsWith('/auth/status')),
      (async () => {
        await page.getByRole('button', { name: 'Check access' }).click()
        const checking = page.getByRole('button', { name: 'Verifying…' })
        await checking.waitFor()
        assert.equal(await checking.isDisabled(), true)
        releaseStatus()
      })(),
    ])
    assert.equal(stillDenied.status(), 200)
    await page.unroute('**/api/v1/auth/status')
    await page.getByRole('alert').filter({ hasText: 'Access has not been confirmed.' }).waitFor()
    assert.equal(await page.evaluate(() => window.setupPageMarker), true, 'a denied recheck must not reload the page')
    await context.setExtraHTTPHeaders({ 'Cf-Access-Jwt-Assertion': ready.access_jwt })
    await page.getByRole('button', { name: 'Check access' }).click()
    await page.getByRole('heading', { name: 'Set up your Space', exact: true }).waitFor()
    assert.equal(await page.evaluate(() => window.setupPageMarker), true, 'authorized recheck must reveal signup without reload')
    await page.screenshot({ path: path.join(output, `${name}-first-run.png`), fullPage: true })
    step = 'Choose account credentials'
    await page.getByLabel('Username', { exact: true }).fill(ready.username)
    await page.getByLabel('Password', { exact: true }).fill(ready.password)
    await page.getByLabel('Confirm password', { exact: true }).fill(ready.password)
    const [begin] = await Promise.all([
      page.waitForResponse(response => response.url().endsWith('/auth/setup/begin')),
      page.getByRole('button', { name: 'Continue', exact: true }).click(),
    ])
    assert.equal(begin.status(), 200)
    const pending = await begin.json()
    const headers = { Origin: ready.url, 'Cf-Access-Jwt-Assertion': ready.access_jwt }
    let state = await (await context.request.get(`${ready.url}/api/v1/auth/status`, { headers })).json()
    assert.equal(state.configured, false, 'credentials alone must not install the account')
    assert.equal((await context.cookies()).some(cookie => cookie.name === 'herdr_session'), false)
    step = 'Verify TOTP with bounded retry'
    await page.getByRole('heading', { name: 'Set up authentication', exact: true }).waitFor()
    const validCodes = new Set([-30000, 0, 30000].map(offset => otp(pending.secret, offset)))
    let invalidCode = '000000'
    while (validCodes.has(invalidCode)) invalidCode = String(Number(invalidCode) + 1).padStart(6, '0')
    await page.getByLabel('Six-digit code', { exact: true }).fill(invalidCode)
    const [invalid] = await Promise.all([
      page.waitForResponse(response => response.url().endsWith('/auth/setup/complete')),
      page.getByRole('button', { name: 'Verify code', exact: true }).click(),
    ])
    assert.equal(invalid.status(), 400)
    await page.getByRole('alert').waitFor()
    assert.ok(await page.locator('.setup-secret code').textContent() === pending.secret, 'invalid OTP must preserve the same challenge')
    const enrollmentCode = otp(pending.secret)
    await page.getByLabel('Six-digit code', { exact: true }).fill(enrollmentCode)
    const [complete] = await Promise.all([
      page.waitForResponse(response => response.url().endsWith('/auth/setup/complete')),
      page.getByRole('button', { name: 'Verify code', exact: true }).click(),
    ])
    assert.equal(complete.status(), 200)
    await page.getByRole('heading', { name: 'Save your recovery codes', exact: true }).waitFor()
    const recoveryCodes = await page.locator('.recovery-codes code').allTextContents()
    assert.ok(recoveryCodes.length > 0)
    state = await (await context.request.get(`${ready.url}/api/v1/auth/status`, { headers })).json()
    assert.equal(state.configured, true)
    assert.equal(state.authenticated, false)
    const closed = await context.request.post(`${ready.url}/api/v1/auth/setup/begin`, { data: { username: ready.username, password: ready.password }, headers })
    assert.equal(closed.status(), 404, 'existing account must permanently disable first-run signup')
    const replay = await context.request.post(`${ready.url}/api/v1/auth/login`, { data: { username: ready.username, password: ready.password, otp: enrollmentCode }, headers })
    assert.equal(replay.status(), 401, 'enrollment TOTP must not be replayed for login')
    step = 'Recovery acknowledgement and real authenticated login'
    await page.getByLabel('I saved the recovery codes', { exact: true }).check()
    await page.getByRole('button', { name: 'Go to sign in', exact: true }).click()
    await page.getByLabel('Username', { exact: true }).fill(ready.username)
    await page.getByLabel('Password', { exact: true }).fill(ready.password)
    await page.getByLabel('Authentication or recovery code', { exact: true }).fill(recoveryCodes[0])
    await page.getByRole('button', { name: /Sign in/ }).click()
    await page.getByRole('heading', { name: 'Sessions', exact: true }).waitFor()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true)
    return { engine: name, bootstrap: true, passed: true, scenarios: ['signed-access-identity', 'credentials-without-account', 'totp-retry', 'totp-replay-denied', 'recovery-acknowledgement', 'signup-disabled', 'authenticated-login'] }
  } catch {
    // Do not capture screenshots, response payloads or DOM containing fixture
    // TOTP secrets/recovery codes, even if a bootstrap assertion fails.
    throw new Error(`${name}: bootstrap verification failed at ${step}`)
  } finally {
    await context.close(); await browser.close(); child.kill('SIGTERM')
    await new Promise(resolve => { if (child.exitCode !== null) return resolve(); child.once('exit', resolve); setTimeout(() => { child.kill('SIGKILL'); resolve() }, 6000).unref() })
  }
}
