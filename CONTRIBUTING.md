# Contributing

Open an issue or pull request at https://github.com/calneymgp/herdr-space. Keep code, UI text, fixtures, and documentation in English. Use a focused change, include a test when behavior changes, and run `make verify` before requesting review. Browser changes can also use the synthetic checks in [tests/browser/README.md](tests/browser/README.md).

Never include credentials, production data, terminal transcripts, personal server details, screenshots of enrollment or recovery material, or generated databases in a contribution. The project uses the MIT license for its own code; preserve third-party notices. After changing `go.mod` or `web/package-lock.json`, run `make deps`, `make license-bundle`, and `make license-check`; review the generated bundle before committing.
