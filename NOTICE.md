# Third-party notices

HERDR Space's own source is [MIT-licensed](LICENSE). The separate [HERDR](https://github.com/herdrdev/herdr) application is Apache-2.0 and is not bundled.

The [third-party license bundle](third_party/THIRD_PARTY_LICENSES.md) contains the original license and notice texts for the Go standard library, the modules compiled into the server, and the production npm packages used by the embedded web assets. Package names and exact versions are listed with each text; module and package versions remain pinned in `go.mod`, `go.sum`, and `web/package-lock.json`. The bundled `modernc` texts include its embedded SQLite and other component notices.

`make build` copies this notice, the project MIT license, and the third-party bundle alongside `bin/herdr-space`. `scripts/install-binary.sh` installs all four files together. Keep these files with a redistributed binary or compiled asset bundle. `make license-check` detects stale or missing notices after a dependency update; `make license-bundle` regenerates them from installed pinned dependencies and fails if a required license text is unavailable.
