// Capture command: make frontend && node tests/browser/screenshots.mjs
// Outputs four private, fictional previews and a loopback/DOM audit under .state/public-screenshots/.
import assert from 'node:assert/strict'
import {createHmac} from 'node:crypto'
import {spawn, spawnSync} from 'node:child_process'
import {chmod, mkdir, mkdtemp, rm, writeFile} from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import {fileURLToPath} from 'node:url'
import {chromium} from '../../.tools/browser/node_modules/playwright/index.mjs'
import {goCommand} from './go-command.mjs'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const outputDir = path.join(root, '.state/public-screenshots')
const manifestPath = path.join(outputDir, 'manifest.json')
const displayName = 'Alex Morgan'
const forbiddenDisplayText = /browser-test|isolated test|test terminal|test line|herdr-fixture|\/tmp\/herdr-space-browser|recovery code|authenticator setup|sign in to space/i
const previewFiles = [
  'workspace-desktop.png',
  'tasks-desktop.png',
  'notes-desktop.png',
  'workspace-mobile.png',
]

const projects = {
  items: [{
    id: 'project-demo',
    name: 'demo-workspace',
    path: '/workspace/demo',
    created_at: '2026-10-01T09:00:00Z',
  }],
}

const tasks = {
  items: [
    {id: 'task-journey', title: 'Map the new user journey', description: 'Turn first-run feedback into a clear three-step path.', project_id: 'project-demo', session_id: '', status: 'todo', due_date: '2026-10-09', created_at: '2026-10-06T10:00:00Z', updated_at: '2026-10-06T10:00:00Z'},
    {id: 'task-empty-states', title: 'Polish empty states', description: 'Give new Spaces a helpful, quiet starting point.', project_id: 'project-demo', session_id: '', status: 'todo', due_date: '2026-10-12', created_at: '2026-10-06T11:00:00Z', updated_at: '2026-10-06T11:00:00Z'},
    {id: 'task-access-model', title: 'Sketch the access model', description: 'Keep team access clear as Spaces grow.', project_id: 'project-demo', session_id: '', status: 'todo', due_date: '2026-10-14', created_at: '2026-10-06T14:00:00Z', updated_at: '2026-10-06T14:00:00Z'},
    {id: 'task-activity', title: 'Add live activity markers', description: 'Show each agent’s current focus in the workspace.', project_id: 'project-demo', session_id: '', status: 'doing', due_date: '2026-10-08', created_at: '2026-10-05T09:00:00Z', updated_at: '2026-10-07T08:15:00Z'},
    {id: 'task-reconnect', title: 'Refine reconnect behavior', description: 'Keep terminal context available after a brief network drop.', project_id: 'project-demo', session_id: '', status: 'doing', due_date: '2026-10-10', created_at: '2026-10-05T13:00:00Z', updated_at: '2026-10-07T08:30:00Z'},
    {id: 'task-restore-flow', title: 'Tune terminal restore flow', description: 'Return to a useful prompt after reconnecting.', project_id: 'project-demo', session_id: '', status: 'doing', due_date: '2026-10-11', created_at: '2026-10-06T08:00:00Z', updated_at: '2026-10-07T08:40:00Z'},
    {id: 'task-vocabulary', title: 'Define workspace language', description: 'Use one consistent vocabulary across the product.', project_id: 'project-demo', session_id: '', status: 'done', due_date: '2026-10-06', created_at: '2026-10-02T09:00:00Z', updated_at: '2026-10-06T16:00:00Z'},
    {id: 'task-setup-guide', title: 'Document first session setup', description: 'Help a new team reach a useful first run.', project_id: 'project-demo', session_id: '', status: 'done', due_date: '2026-10-05', created_at: '2026-10-02T11:00:00Z', updated_at: '2026-10-05T15:20:00Z'},
    {id: 'task-agent-names', title: 'Agree on agent naming', description: 'Keep session labels short and recognizable.', project_id: 'project-demo', session_id: '', status: 'done', due_date: '2026-10-04', created_at: '2026-10-01T13:00:00Z', updated_at: '2026-10-04T16:00:00Z'},
  ],
}

const notes = {
  items: [
    {
      id: 'note-direction',
      title: 'Product direction · October',
      body: '# A calmer home for focused work\n\nHERDR Space keeps agent work, terminal access, and decisions in one place.\n\n## North star\n\nAnswer three questions at a glance: what is moving, who owns it, and what happens next?\n\n## This week\n\n- [x] Keep active work visible in the Space\n- [x] Make session status easy to scan\n\n## Product decisions\n\n| Area | Direction |\n| --- | --- |\n| Workspace | One focused view per Space |\n| Notes | Lightweight Markdown in context |\n| Activity | Clear status without extra noise |\n',
      project_id: 'project-demo',
      version: 1,
      created_at: '2026-10-01T09:00:00Z',
      updated_at: '2026-10-07T09:10:00Z',
    },
    {
      id: 'note-launch',
      title: 'Launch checklist',
      body: '# Launch checklist\n\n- Confirm the first-run path\n- Review keyboard access\n- Publish the short release note\n',
      project_id: 'project-demo',
      version: 1,
      created_at: '2026-10-03T10:00:00Z',
      updated_at: '2026-10-06T14:20:00Z',
    },
    {
      id: 'note-handoffs',
      title: 'Agent roles and handoffs',
      body: '# Agent roles and handoffs\n\n## Working agreements\n\n- Keep each task focused on one useful outcome.\n- Leave a short note when context changes.\n- Return a clear next step to the team.\n',
      project_id: 'project-demo',
      version: 1,
      created_at: '2026-10-04T11:00:00Z',
      updated_at: '2026-10-05T16:00:00Z',
    },
  ],
}

const sessionPresentation = [
  {name: 'Atlas · API design', agent: 'codex', activity: 'working'},
  {name: 'Nova · Release plan', agent: 'claude', activity: 'idle'},
  {name: 'Sage · Onboarding', agent: 'opencode', activity: 'working'},
  {name: 'Milo · QA strategy', agent: 'pi', activity: 'idle'},
  {name: 'Iris · Research notes', agent: 'claude', activity: 'done'},
  {name: 'Echo · Archived review', agent: 'codex', activity: 'ended'},
]

const terminalFrame = Buffer.from(
  '\x1b[2J\x1b[H' +
  '\x1b[1;36mHERDR SPACE\x1b[0m  /  \x1b[1mdemo-workspace\x1b[0m\r\n' +
  'Sprint 14 · product workspace\r\n' +
  '\r\n' +
  '$ pwd\r\n' +
  '\x1b[36m/workspace/demo\x1b[0m\r\n' +
  '$ git status --short\r\n' +
  ' M src/roadmap.ts\r\n' +
  ' M notes/launch.md\r\n' +
  '\r\n' +
  '$ git diff --stat\r\n' +
  ' src/roadmap.ts  | +12\r\n' +
  ' notes/launch.md | +8 -4\r\n' +
  '\r\n' +
  '$ pnpm test --filter workspace\r\n' +
  '\x1b[32m✓ 28 checks passing · 1.4s\x1b[0m\r\n' +
  '\r\n' +
  '$ cat notes/tasks.md\r\n' +
  '3 to do · 3 in progress · 3 done\r\n' +
  '\r\n' +
  '$ cat notes/next-step.md\r\n' +
  'Pair with Nova on reconnect copy.\r\n' +
  '\r\n' +
  '$ cat notes/agents.md\r\n' +
  'Atlas · API design      working\r\n' +
  'Nova  · Release plan     waiting\r\n' +
  'Sage  · Onboarding       working\r\n' +
  '\r\n' +
  'Next: polish the release notes\r\n' +
  '\r\n' +
  '\x1b[36matlas@demo-workspace\x1b[0m:\x1b[34m~/product\x1b[0m$ ',
)

function otp(secret) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = ''
  for (const character of secret.toUpperCase()) bits += alphabet.indexOf(character).toString(2).padStart(5, '0')
  const key = []
  for (let index = 0; index + 8 <= bits.length; index += 8) key.push(Number.parseInt(bits.slice(index, index + 8), 2))
  const counter = Buffer.alloc(8)
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)))
  const digest = createHmac('sha1', Buffer.from(key)).update(counter).digest()
  return String((digest.readUInt32BE(digest[19] & 15) & 0x7fffffff) % 1_000_000).padStart(6, '0')
}

async function startFixture(binaryPath, tempDir) {
  const child = spawn(binaryPath, [], {
    cwd: root,
    stdio: ['ignore', 'pipe', 'pipe'],
    env: {
      PATH: process.env.PATH || '/usr/bin:/bin',
      TMPDIR: tempDir,
      TEMP: tempDir,
      TMP: tempDir,
    },
  })
  child.stderr.on('data', () => {})
  let pending = ''
  let settled = false
  let timer
  const ready = await new Promise((resolve, reject) => {
    const finish = (error, value) => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      if (error) reject(error)
      else resolve(value)
    }
    timer = setTimeout(() => finish(new Error('Synthetic fixture startup timed out.')), 20_000)
    child.once('error', () => finish(new Error('Synthetic fixture could not start.')))
    child.once('exit', () => finish(new Error('Synthetic fixture exited before readiness.')))
    child.stdout.on('data', chunk => {
      pending += chunk.toString('utf8')
      const newline = pending.indexOf('\n')
      if (newline < 0) return
      const line = pending.slice(0, newline)
      pending = ''
      try {
        const data = JSON.parse(line)
        const url = new URL(data.url)
        assert.equal(url.protocol, 'http:')
        assert.equal(url.hostname, '127.0.0.1')
        assert.ok(Number(url.port) >= 1024)
        assert.equal(data.username, 'browser-test')
        assert.ok(data.password.length >= 32)
        assert.ok(data.totp_secret.length >= 16)
        finish(null, data)
      } catch {
        finish(new Error('Synthetic fixture readiness data was invalid.'))
      }
    })
  })
  return {child, ready}
}

async function stopChild(child) {
  if (!child || child.exitCode !== null) return
  const exited = new Promise(resolve => child.once('exit', resolve))
  child.kill('SIGTERM')
  await Promise.race([exited, new Promise(resolve => setTimeout(resolve, 6_000))])
  if (child.exitCode === null) child.kill('SIGKILL')
}

function jsonResponse(route, value) {
  return route.fulfill({status: 200, contentType: 'application/json; charset=utf-8', body: JSON.stringify(value)})
}

async function transformSessions(route) {
  const response = await route.fetch()
  assert.equal(response.status(), 200, 'the synthetic fixture must provide its session inventory')
  const inventory = await response.json()
  assert.equal(inventory.items?.length, sessionPresentation.length)
  assert.equal(inventory.spaces?.length, 2)
  const items = inventory.items.map((session, index) => ({
    ...session,
    ...sessionPresentation[index],
    alive: index < 5,
    capabilities: index < 5 ? ['observe', 'control', 'stop'] : [],
    cwd: '/workspace/demo',
    project_id: 'project-demo',
    updated_at: '2026-10-07T09:00:00Z',
  }))
  const spaces = inventory.spaces.map((space, index) => ({
    ...space,
    name: index === 0 ? 'demo-workspace' : 'briefing-room',
    path: index === 0 ? '/workspace/demo' : '',
  }))
  return route.fulfill({response, body: JSON.stringify({...inventory, items, spaces, stale: false, warning: ''})})
}

async function installRoutes(context, fixtureOrigin, websocketOrigin, credentials, audit) {
  await context.addInitScript(presentation => {
    const addEventListener = EventSource.prototype.addEventListener
    EventSource.prototype.addEventListener = function (type, listener, options) {
      if (type !== 'sessions') return addEventListener.call(this, type, listener, options)
      const wrapped = event => {
        const inventory = JSON.parse(event.data)
        inventory.items = inventory.items.map((session, index) => ({
          ...session,
          ...presentation[index],
          alive: index < 5,
          capabilities: index < 5 ? ['observe', 'control', 'stop'] : [],
          cwd: '/workspace/demo',
          project_id: 'project-demo',
          updated_at: '2026-10-07T09:00:00Z',
        }))
        inventory.spaces = inventory.spaces.map((space, index) => ({
          ...space,
          name: index === 0 ? 'demo-workspace' : 'briefing-room',
          path: index === 0 ? '/workspace/demo' : '',
        }))
        inventory.stale = false
        inventory.warning = ''
        const rewritten = new MessageEvent('sessions', {data: JSON.stringify(inventory), origin: event.origin, lastEventId: event.lastEventId})
        if (typeof listener === 'function') listener.call(this, rewritten)
        else listener.handleEvent(rewritten)
      }
      return addEventListener.call(this, type, wrapped, options)
    }
  }, sessionPresentation)

  await context.route('**/*', async route => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.protocol === 'http:' || url.protocol === 'https:') {
      audit.httpOrigins.add(url.origin)
      audit.requests.add(`${request.method()} ${url.pathname}`)
      if (url.origin !== fixtureOrigin) {
        audit.blockedOrigins.add(url.origin)
        await route.abort()
        return
      }
    }

    if (url.pathname === '/api/v1/auth/login' && request.method() === 'POST') {
      let body
      try { body = request.postDataJSON() } catch { throw new Error('The synthetic login request was malformed.') }
      assert.equal(body.username, displayName)
      const response = await route.fetch({postData: JSON.stringify({...body, username: credentials.username})})
      assert.equal(response.status(), 200, 'the synthetic account must authenticate only against the fixture')
      const result = await response.json()
      assert.equal(result.username, credentials.username)
      return route.fulfill({response, body: JSON.stringify({...result, username: displayName})})
    }
    if (url.pathname === '/api/v1/projects' && request.method() === 'GET') return jsonResponse(route, projects)
    if (url.pathname === '/api/v1/tasks' && request.method() === 'GET') return jsonResponse(route, tasks)
    if (url.pathname === '/api/v1/notes' && request.method() === 'GET') return jsonResponse(route, notes)
    if (url.pathname === '/api/v1/preferences' && request.method() === 'GET') return jsonResponse(route, {item: {theme: 'light'}})
    if (url.pathname === '/api/v1/sessions' && request.method() === 'GET') return transformSessions(route)
    return route.continue()
  })

  await context.routeWebSocket(() => true, async route => {
    const url = new URL(route.url())
    audit.websocketOrigins.add(url.origin)
    if (url.origin !== websocketOrigin) {
      audit.blockedOrigins.add(url.origin)
      await route.close({code: 1008, reason: 'loopback fixture only'})
      return
    }
    assert.match(url.pathname, /^\/api\/v1\/terminals\/[^/]+\/stream$/)
    const server = route.connectToServer()
    server.onMessage(message => route.send(Buffer.isBuffer(message) ? terminalFrame : message))
  })
}

async function assertCleanScene(page, label, expectedText) {
  const pageText = await page.locator('body').innerText()
  assert.match(pageText, expectedText, `${label}: expected fictional content must be visible`)
  assert.doesNotMatch(pageText, forbiddenDisplayText, `${label}: fixture and credential text must never reach a capture`)
  assert.equal(await page.locator('input[type="password"]').count(), 0, `${label}: password screens cannot be captured`)
  assert.equal(await page.getByRole('heading', {name: /Welcome back|Set up your Space|Set up authentication|Save your recovery codes/}).count(), 0, `${label}: auth and enrollment screens cannot be captured`)
  assert.equal(await page.locator('[role="dialog"]:visible').count(), 0, `${label}: dialogs must be closed`)
  assert.equal(await page.locator('.toast:visible, .warning-banner:visible, [role="alert"]:visible').count(), 0, `${label}: warnings and errors must be absent`)
  const geometry = await page.evaluate(() => ({
    viewportWidth: innerWidth,
    documentWidth: document.documentElement.scrollWidth,
    bodyWidth: document.body.scrollWidth,
  }))
  assert.ok(geometry.documentWidth <= geometry.viewportWidth + 1 && geometry.bodyWidth <= geometry.viewportWidth + 1, `${label}: content must not overflow horizontally (${JSON.stringify(geometry)})`)
  return pageText.replace(/\s+/g, ' ').trim().slice(0, 1_200)
}

async function capture(page, outputPath, label, expectedText, viewport) {
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.evaluate(() => document.fonts.ready)
  const textAudit = await assertCleanScene(page, label, expectedText)
  const image = await page.screenshot({
    path: outputPath,
    fullPage: false,
    animations: 'disabled',
    caret: 'hide',
    scale: 'css',
    type: 'png',
  })
  await chmod(outputPath, 0o600)
  return {file: path.basename(outputPath), viewport, bytes: image.length, visibleText: textAudit}
}

const tempDir = await mkdtemp(path.join(os.tmpdir(), 'herdr-public-screenshots-'))
let fixtureChild
let browser
try {
  await mkdir(outputDir, {recursive: true, mode: 0o700})
  await chmod(outputDir, 0o700)
  for (const file of [...previewFiles, 'manifest.json']) await rm(path.join(outputDir, file), {force: true})

  const fixtureBinary = path.join(tempDir, 'fixture-server')
  const build = spawnSync(goCommand(root), ['build', '-mod=readonly', '-o', fixtureBinary, './tests/browser/fixture'], {
    cwd: root,
    encoding: 'utf8',
    stdio: ['ignore', 'ignore', 'ignore'],
  })
  assert.equal(build.status, 0, 'the synthetic browser fixture must build successfully')

  const started = await startFixture(fixtureBinary, tempDir)
  fixtureChild = started.child
  const credentials = started.ready
  const fixtureOrigin = new URL(credentials.url).origin
  const websocketOrigin = `ws://${new URL(credentials.url).host}`
  const audit = {httpOrigins: new Set(), websocketOrigins: new Set(), blockedOrigins: new Set(), requests: new Set()}

  browser = await chromium.launch({
    executablePath: process.env.HERDR_CHROME_EXECUTABLE || undefined,
    args: ['--no-sandbox'],
    headless: true,
  })
  const context = await browser.newContext({
    viewport: {width: 1440, height: 960},
    deviceScaleFactor: 1,
    colorScheme: 'light',
    timezoneId: 'UTC',
    serviceWorkers: 'block',
  })
  await installRoutes(context, fixtureOrigin, websocketOrigin, credentials, audit)
  const page = await context.newPage()
  const pageErrors = []
  page.on('pageerror', error => pageErrors.push(error.name))
  page.on('websocket', socket => {
    const url = new URL(socket.url())
    audit.websocketOrigins.add(url.origin)
  })

  await page.goto(credentials.url, {waitUntil: 'networkidle'})
  if (await page.getByLabel('Username', {exact: true}).count() !== 1) {
    const initialText = (await page.locator('body').innerText()).replace(/\s+/g, ' ').trim().slice(0, 400)
    throw new Error(`Unexpected initial fixture UI state: ${initialText || '(empty page)'}`)
  }
  await page.getByLabel('Username', {exact: true}).fill(displayName)
  await page.getByLabel('Password', {exact: true}).fill(credentials.password)
  await page.getByLabel('Authentication or recovery code', {exact: true}).fill(otp(credentials.totp_secret))
  await page.getByRole('button', {name: /Sign in to Space/}).click()
  await page.getByRole('heading', {name: 'Sessions', exact: true}).waitFor()
  credentials.password = ''
  credentials.totp_secret = ''
  credentials.access_jwt = ''
  credentials.project_path = ''
  await page.getByRole('button', {name: 'demo-workspace', exact: true}).click()
  await page.getByRole('heading', {name: 'demo-workspace', exact: true}).waitFor()
  await page.getByRole('tab', {name: 'Atlas · API design', exact: true}).click()
  await page.getByText('Control active', {exact: true}).waitFor({timeout: 8_000})
  const terminalText = await page.locator('.xterm-rows').evaluate(element => element.textContent || '')
  assert.match(terminalText, /\/workspace\/demo/)
  assert.match(terminalText, /28 checks passing/)
  assert.match(terminalText, /atlas@demo-workspace/)
  assert.doesNotMatch(terminalText, /test terminal|test line|fixture/i)

  const screenshots = []
  screenshots.push(await capture(page, path.join(outputDir, 'workspace-desktop.png'), 'workspace desktop', /demo-workspace|Atlas · API design|28 checks passing/i, {width: 1440, height: 960}))

  await page.getByRole('button', {name: 'Tasks', exact: true}).click()
  await page.getByRole('heading', {name: 'Tasks', exact: true}).waitFor()
  for (const title of ['Map the new user journey', 'Add live activity markers', 'Document first session setup']) {
    await page.getByRole('button', {name: new RegExp(title)}).waitFor()
  }
  screenshots.push(await capture(page, path.join(outputDir, 'tasks-desktop.png'), 'tasks desktop', /Map the new user journey|Add live activity markers|Document first session setup/i, {width: 1440, height: 960}))

  await page.getByRole('button', {name: 'Notes', exact: true}).click()
  await page.getByRole('heading', {name: 'Notes', exact: true}).waitFor()
  await page.getByRole('tab', {name: /Product direction · October/}).click()
  await page.getByRole('tab', {name: 'Preview', exact: true}).click()
  await page.getByRole('heading', {name: 'A calmer home for focused work'}).waitFor()
  screenshots.push(await capture(page, path.join(outputDir, 'notes-desktop.png'), 'notes desktop', /Product direction · October|A calmer home for focused work|North star/i, {width: 1440, height: 960}))

  await page.setViewportSize({width: 390, height: 844})
  await page.locator('.mobile-nav').getByRole('button', {name: 'Sessions', exact: true}).click()
  await page.getByRole('heading', {name: 'demo-workspace', exact: true}).waitFor()
  await page.getByRole('tab', {name: 'Atlas · API design', exact: true}).click()
  await page.getByText('Control active', {exact: true}).waitFor({timeout: 8_000})
  screenshots.push(await capture(page, path.join(outputDir, 'workspace-mobile.png'), 'workspace mobile', /demo-workspace|Atlas · API design|28 checks passing/i, {width: 390, height: 844}))

  assert.deepEqual([...audit.blockedOrigins], [], 'browser requests outside the synthetic fixture must be blocked')
  assert.deepEqual([...audit.httpOrigins], [fixtureOrigin], 'all browser HTTP requests must stay on the ephemeral loopback fixture')
  assert.deepEqual([...audit.websocketOrigins], [websocketOrigin], 'all browser WebSockets must stay on the ephemeral loopback fixture')
  assert.deepEqual(pageErrors, [], 'the captured UI must be free of browser errors')

  const manifest = {
    generatedAt: new Date().toISOString(),
    fixture: 'tests/browser/fixture built in a temporary directory; fresh SQLite DB and auth key are created by that fixture',
    allowedHTTPOrigin: fixtureOrigin,
    allowedWebSocketOrigin: websocketOrigin,
    observedHTTPOrigins: [...audit.httpOrigins],
    observedWebSocketOrigins: [...audit.websocketOrigins],
    blockedExternalOrigins: [...audit.blockedOrigins],
    mockedDataSource: 'Fictional project, tasks, notes, display name, and session labels are literals in tests/browser/screenshots.mjs. The session IDs and authenticated API come from the isolated fixture; the route renames its generated test sessions in memory. The fixture terminal stream is replaced in memory with deterministic demo output and /workspace/demo paths.',
    networkRequestCount: audit.requests.size,
    screenshots,
  }
  await writeFile(manifestPath, JSON.stringify(manifest, null, 2), {mode: 0o600})
  await chmod(manifestPath, 0o600)
  for (const error of pageErrors) assert.fail(error)
  process.stdout.write(`Captured ${screenshots.length} private mock screenshots in ${path.relative(root, outputDir)}\n`)
} finally {
  if (browser) await browser.close()
  await stopChild(fixtureChild)
  await rm(tempDir, {recursive: true, force: true})
}
