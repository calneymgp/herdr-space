# Security policy

Please report a vulnerability privately through GitHub's security advisory flow at https://github.com/calneymgp/herdr-space/security/advisories/new. Include the affected version, reproduction steps, and impact. Do not publish credentials, recovery codes, or user data in an issue.

HERDR Space is intended for a trusted owner on a self-managed host. Keep the service behind HTTPS and an exact configured origin, restrict the data directory to its owner, and protect backups as credentials. Public deployments should use a reverse proxy and its normal network restrictions. Browser enrollment is optional and requires a verified Cloudflare Access owner identity; terminal enrollment is the default. The application also requires password plus TOTP, checks origin and CSRF on mutations, and issues absolute seven-day sessions. See [docs/SECURITY.md](docs/SECURITY.md) for the implementation boundaries.
