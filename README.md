# HERDR Space

HERDR Space is a self-hosted workspace for [HERDR](https://github.com/herdrdev/herdr) sessions, terminals, tasks, GitHub issues, and Markdown notes. It has one owner account with password and TOTP, a Go API backed by SQLite, and an embedded React web client. It is free software under the [MIT license](LICENSE). Redistributions of the binary or web assets must include [the dependency license bundle](third_party/THIRD_PARTY_LICENSES.md).

HERDR itself is a separate Apache-2.0 project and is not included. This release was tested with HERDR 0.9.3. The server needs Linux `/proc` to discover local processes; the browser client works on desktop and mobile operating systems.

## Build and run locally

Install Node.js 24, Go 1.27.1, and an existing HERDR installation on a Linux host. A [verified project-local Go installer](scripts/install-toolchain.py) is available with `make toolchain`; otherwise a compatible `go` on `PATH` works. From a fresh clone:

```sh
git clone https://github.com/calneymgp/herdr-space.git
cd herdr-space
make deps
make build
./bin/herdr-space auth setup --data-dir .state/dev
make dev
```

`auth setup` requires an interactive terminal. Choose a username and a password of at least 16 characters, enter the current six-digit TOTP code, and store the one-time recovery codes securely. The secret and codes appear only on that terminal. Open `http://127.0.0.1:9380` and sign in with the next TOTP code. `make dev` binds to loopback and explicitly enables local HTTP; never use that flag on a public listener.

The account's data and encryption key live in the chosen data directory. Keep the directory private and back it up. Run `./bin/herdr-space auth change` to rotate credentials or `./bin/herdr-space auth recovery` to regenerate codes, with the same `--data-dir` as the server. Use `make verify` for Go race tests, frontend tests, vet, and an embedded build.

## Host it

Serve behind an HTTPS reverse proxy and pass the exact external origin to `--origin`, such as `https://space.example.com`. The server refuses an absent origin and remote HTTP. Bind the app to loopback or another private interface; configure the proxy's TLS and access controls for your environment. The [operations guide](docs/OPERATIONS.md) includes example user services, backups, and optional Cloudflare Access browser enrollment. Initial owner creation through `auth setup` is the portable default.

[Architecture](docs/ARCHITECTURE.md) · [API contract](docs/CONTRACT.md) · [Security](docs/SECURITY.md) · [Browser tests](tests/browser/README.md) · [Contributing](CONTRIBUTING.md)
