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
