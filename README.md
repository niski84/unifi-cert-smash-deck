# UniFi Cert Smash Deck

Go service that acts as a **certificate lifecycle controller** for UniFi OS consoles (Dream Machine, Cloud Key, etc.): read the installed TLS certificate over SSH, renew with **Let’s Encrypt** via **DNS-01** (Cloudflare) and **lego**, push `unifi-core.crt` / `unifi-core.key` over SFTP, then run `systemctl restart unifi-core`.

## Stack

- **Go** — scheduler, ACME, SSH/SFTP, API
- **Templ** — HTML components
- **HTMX** — partial updates and forms
- **Alpine.js** — WebSocket log panel
- **Tailwind CSS 4** — styling (CLI build into embedded static)

See [docs/HTMX_ALPINE.md](docs/HTMX_ALPINE.md) for UI conventions.

## Layout (Smash Deck series)

- `cmd/unificert/` — entrypoint
- `internal/certdeck/` — domain logic + HTTP
- `internal/certdeck/views/` — Templ templates
- `web/certdeck/static/` — embedded CSS
- `scripts/compile.sh` — templ + Tailwind + `go build`
- `scripts/reload.sh` — rebuild and restart locally

## Quick start

```bash
cd goprojects/unifi-cert-smash-deck
npm install
./scripts/compile.sh
cp .env.example .env   # optional PORT / env overrides
./scripts/reload.sh
```

Open `http://127.0.0.1:8105/` (or your `PORT`). Configure domain, ACME email, Cloudflare DNS token, SSH target, and paths in **Settings**.

## Security notes

- Prefer **`ssh_known_hosts`** in settings instead of the default insecure host-key callback.
- `data/` holds settings, ACME account key, state, and logs — keep it off backups you do not trust.

## Requirements

- SSH user with permission to write the remote cert paths and restart `unifi-core` (often `root`).
- Cloudflare API token with DNS edit rights for the zone containing `_acme-challenge` records.

Changing **check interval (hours)** in Settings applies after you restart the process (e.g. run `./scripts/reload.sh` again).
