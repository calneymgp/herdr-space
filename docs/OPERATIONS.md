# Operations

Build on a Linux host with Go 1.27.1, Node.js 24, and a separate HERDR installation. `make deps && make build` produces `bin/herdr-space` with `bin/LICENSE`, `bin/NOTICE.md`, and `bin/THIRD_PARTY_LICENSES.md`. Keep these four files together when redistributing or installing a build. Run `scripts/install-binary.sh` to atomically install the binary and three license files to `HERDR_SPACE_BIN_DIR` (default `~/.local/lib/herdr-space`); the script keeps the previous binary. Set `--herdr-binary` or `--herdr-config-dir` if the HERDR installation is outside its normal path. The service account needs access to HERDR's local socket and `/proc`.

Create the owner in a terminal with the intended persistent data directory before starting the web service:

```sh
~/.local/lib/herdr-space/herdr-space auth setup --data-dir ~/.local/share/herdr-space
~/.local/lib/herdr-space/herdr-space serve --listen 127.0.0.1:9380 --origin https://space.example.com --data-dir ~/.local/share/herdr-space
```

Place an HTTPS reverse proxy in front of the loopback listener. Forward the request host and WebSocket upgrades; keep the external origin identical to `--origin`. Only for local development, use `--origin http://127.0.0.1:9380 --allow-insecure-local` with a loopback listener. The server refuses remote insecure origins.

`deploy/` contains generic systemd user-service examples. Copy the `.service.example` and `.timer.example` files to `~/.config/systemd/user/` without `.example`; set `HERDR_SPACE_ORIGIN` in a mode-0600 copy of `server.env.example` at `~/.config/herdr-space/server.env`, then run `systemctl --user daemon-reload` and enable the service and backup timer. The examples expect the binary at the install script's default directory, `~/.local/lib/herdr-space/herdr-space`; adjust that path if installing elsewhere. Configure TLS and firewall rules according to your own host; the examples do not create public routes.

Optional browser enrollment uses Cloudflare Access in front of the service. Configure all three flags `--bootstrap-issuer`, `--bootstrap-audience`, and `--bootstrap-email` with your own exact values through a private service override or manual invocation. The application verifies the Access JWT and owner identity for both setup steps. Leave these flags unset when using terminal enrollment. Cloudflare Access session duration and policy are managed separately; align them with the app's seven-day absolute session only if that is your intended access policy.

Run `~/.local/lib/herdr-space/herdr-space backup --data-dir ~/.local/share/herdr-space --retain 7` for consistent SQLite snapshots, and protect the key, database, and backups together. Back up before upgrades. Stop the service before restoring a snapshot and its matching key; verify ownership and permissions before starting it. Keep a copy of the previous binary for rollback, then check `/api/v1/auth/status` and sign in through the browser. Never copy live data into test fixtures or public issue reports.
