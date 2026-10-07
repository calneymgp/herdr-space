# Architecture

The Go binary embeds the compiled React/Vite client and serves `/api/v1` plus static assets. SQLite stores the owner account, session references, tasks, notes, preferences, and audit data. The key file in the data directory protects authenticator material. The backend talks to a locally installed HERDR through its Unix socket and command interface. HERDR owns agent processes; closing a web terminal releases browser control only. GitHub issue access uses the host's configured `gh` CLI authentication when enabled.

The Linux server discovers local processes through `/proc`; browser clients can run on desktop or mobile systems. Build assets with `make build`. No Node process is needed to serve the app.
