# Synthetic browser checks

These checks run only against an isolated fixture server and temporary data. Install the pinned test-only Playwright package, Chromium, and WebKit:

```sh
make browser-deps
.tools/browser/node_modules/.bin/playwright install chromium webkit
make build
node tests/browser/smoke.mjs
node tests/browser/sw-migration.mjs
```

On a clean Ubuntu runner, install browser system dependencies with `.tools/browser/node_modules/.bin/playwright install --with-deps chromium webkit`. Set `HERDR_CHROME_EXECUTABLE` to use a separately installed Chrome; the default uses Playwright Chromium. Browser fixture builds use project-local Go 1.27.1 if present, then `go` on PATH; `HERDR_GO_BINARY` can select another compatible Go executable.

The smoke suite covers enrollment and login, persistent seven-day cookies, tasks, notes, sessions, terminal control, GitHub issue behavior, mobile layout, and cache migration in both engines. `node tests/browser/smoke.mjs --layout-audit` runs the wider layout matrix. Evidence and synthetic screenshots are kept under ignored `.state/qa/`; do not upload that directory as a public CI artifact. Enrollment secrets, QR codes, recovery codes, and credentials must never be present in screenshots or logs. The fixture uses no production account, session, or HERDR agent.

## Public README screenshots

After installing the browser tooling above, generate the four documentation previews:

```sh
make frontend
node tests/browser/screenshots.mjs
```

The capture uses a fresh fixture database and browser context on an ephemeral loopback address. It supplies fictional spaces, session names, tasks, notes, and terminal output in memory and blocks browser traffic outside that fixture. Screenshots are captured only after login, with no credential forms, enrollment screens, dialogs, or error messages visible.

The PNGs and a private DOM/network manifest are written to ignored `.state/public-screenshots/`. Review every image before copying only the four approved PNGs into `docs/screenshots/`. Keep the manifest and the rest of `.state/` private. The captures display actual application UI with mock content; they do not connect to production or run real agent sessions.
