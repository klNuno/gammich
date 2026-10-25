# Security

## Reporting a vulnerability

Use [GitHub's private vulnerability reporting](https://github.com/klNuno/gammich/security/advisories/new),
not a public issue. Include the version or commit, your setup (Docker, binary,
reverse proxy), the steps to reproduce and what an attacker gains.

Only the latest release and `main` receive fixes.

## Scope

In scope: the login and session cookie, the proxy secret and ingest token
checks, path handling when serving files from the recordings folder, and
anything that writes outside the cache and database paths.

Out of scope: a deployment without `GAMMICH_PASSWORD` or `GAMMICH_PROXY_SECRET`
exposed to an untrusted network (reads are open by design there), and
`GAMMICH_TRUST_LOOPBACK` enabled behind a reverse proxy on the same host.
