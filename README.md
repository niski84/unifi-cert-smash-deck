# UniFi Cert Smash Deck

[![Release](https://img.shields.io/github/v/release/niski84/unifi-cert-smash-deck)](https://github.com/niski84/unifi-cert-smash-deck/releases)
[![Docker](https://img.shields.io/badge/docker-ghcr.io-blue)](https://github.com/niski84/unifi-cert-smash-deck/pkgs/container/unifi-cert-smash-deck)
[![Issues](https://img.shields.io/github/issues/niski84/unifi-cert-smash-deck)](https://github.com/niski84/unifi-cert-smash-deck/issues)

**A local web dashboard that installs and manages Let's Encrypt certificates on your UniFi Dream Machine.**

Certificate issuance and automatic 90-day renewal run on the UDM itself via **[kchristensen/udm-le](https://github.com/kchristensen/udm-le)**. This app handles setup — SSH key deployment, config generation, and one-click install — plus ongoing cert health monitoring so you always know how many days are left.

> **Bugs, questions, or feature requests?** [Open an issue](https://github.com/niski84/unifi-cert-smash-deck/issues) — all feedback welcome.

![Demo walkthrough](docs/screenshots/demo.gif)

---

## What it does

- **Guided wizard** — 5-step setup: SSH connect → domain config → preflight → install → verify. Run once; certs renew automatically.
- **Cert health dashboard** — status ring shows days remaining, last check time, common name, and error state. Auto-refreshes every 12 s.
- **One-click install** — SSHes into the UDM, downloads udm-le, appends your config, and deploys an SSH key for future cert reads. No terminal copy/paste needed.
- **Health API** — `/api/health` returns cert status as JSON, including `cert_days_left`, `cert_expires`, `cert_healthy`, and `cert_common_name`.
- **Generated `udm-le.env` snippet** — builds the correct environment fragment from your settings (email, hostnames, DNS provider).
- **Optional Cloudflare token verify** — one-time DNS permission check; token is never saved to disk.

---

## Prerequisites

| Requirement | Notes |
|-------------|-------|
| **Go 1.23+** | `go build` |
| **Node.js + npm** | Build the embedded Tailwind CSS |
| **Domain name** | DNS managed by Cloudflare, Route53, DigitalOcean, DuckDNS, Azure, GCloud, or Linode |
| **DNS API token** | "Edit zone DNS" permission — written to the UDM, never stored here |
| **SSH access to UDM** | Settings → System → Advanced → SSH → Enable. User is `root`; password is the local SSH password shown there — not your Ubiquiti cloud login. |

---

## Quick start

```bash
cd goprojects/unifi-cert-smash-deck
npm install
./scripts/compile.sh
cp .env.example .env   # edit PORT if needed (default 8105)
./scripts/reload.sh
```

Open **`http://127.0.0.1:8105/`** — you'll land on the setup wizard if this is your first run.

### Windows

Download the latest `*-windows-amd64-setup.exe` from the [Releases page](https://github.com/niski84/unifi-cert-smash-deck/releases), run it, and follow the installer. It installs the binary to `Program Files`, creates Start Menu shortcuts, and optionally registers a Windows service that starts automatically with Windows.

### Docker

```bash
docker run -d \
  --name unifi-cert-smash-deck \
  --restart unless-stopped \
  -p 8105:8105 \
  -v "$HOME/.ssh:/home/nonroot/.ssh:ro" \
  -v unificert-data:/data \
  ghcr.io/niski84/unifi-cert-smash-deck:latest
```

Settings are stored in the `/data` volume. Pass `.env` variables with `-e` flags or a `--env-file`. SSH keys mounted at `/home/nonroot/.ssh` are accessible as `/home/nonroot/.ssh/id_ed25519` — set `UNIFICERT_SSH_KEY` accordingly.

---

## Checking cert health

Once the wizard completes, the dashboard shows a live cert status ring. You can also query the health API directly:

```bash
curl http://127.0.0.1:8105/api/health | jq .
```

```json
{
  "cert_common_name": "unifi.example.com",
  "cert_days_left": 89,
  "cert_expires": "2026-06-27T23:21:09Z",
  "cert_healthy": true,
  "cert_known": true,
  "cert_hosts_configured": true,
  "ssh_host_configured": true,
  "last_check": "2026-03-30T14:10:57Z",
  "last_error": "",
  "service": "unifi-cert-smash-deck"
}
```

The status ring and health API update on every scheduler cycle (default every 12 hours). Click **Check cert now** on the dashboard to force an immediate SSH read.

---

## Setup flow

1. **Enable SSH** on the UDM: UniFi OS → Settings → System → Advanced → SSH → Enable. Note the local SSH password.
2. Open the app — the wizard starts automatically.
3. **Step 1 – Connect**: enter the UDM's LAN IP and SSH password. The app generates an Ed25519 key and deploys it so future operations are passwordless.
4. **Step 2 – Domain**: enter your Let's Encrypt email and hostname(s) (`unifi.example.com`), select your DNS provider.
5. **Step 3 – Preflight**: optionally verify your DNS API token has the right permissions before touching the UDM.
6. **Step 4 – Install**: one click — the app SSHes in, downloads udm-le, appends the config, and writes your DNS token directly on the UDM. Token never leaves the gateway.
7. **Step 5 – Verify**: triggers the first certificate issuance (`udm-le.sh initial`) and confirms the cert is readable.

Full step-by-step walkthrough with SSH troubleshooting: **[docs/SETUP-UDM-LE.md](docs/SETUP-UDM-LE.md)**

---

## SSH setup (manual)

If you prefer to handle SSH key deployment yourself rather than using the wizard:

```bash
# Copy your key to the UDM
ssh-copy-id -o PreferredAuthentications=keyboard-interactive,password -i ~/.ssh/id_ed25519.pub root@UDM_IP

# Create a dedicated known_hosts file (avoids conflicts with your main one)
ssh-keyscan UDM_IP | grep -v '^#' > ~/.ssh/known_hosts_unifi
```

Then set `UNIFICERT_SSH_KEY` and `UNIFICERT_SSH_KNOWN_HOSTS` in `.env` (or in Settings).

---

## `.env` reference

```bash
PORT=8105
UNIFICERT_SSH_HOST=192.168.1.1          # gateway LAN IP — no https://
UNIFICERT_SSH_USER=root
UNIFICERT_SSH_KEY=/home/you/.ssh/id_ed25519
UNIFICERT_SSH_KNOWN_HOSTS=/home/you/.ssh/known_hosts_unifi
UNIFICERT_SSH_PASSWORD="your-ssh-password"  # temporary; prefer key auth
UNIFICERT_CERT_EMAIL=you@example.com
UNIFICERT_CERT_HOSTS=unifi.example.com
UNIFICERT_DNS_PROVIDER=cloudflare
```

All settings can also be saved via the web UI (stored in `data/unificert-settings.json`). The binary also loads `../unifi-smash-deck/.env` before this project's `.env`, so `UNIFI_HOST`, `UNIFI_API_KEY`, and `UNIFI_SITE` can be shared with [UniFi Smash Deck](https://github.com/niski84/unifi-smash-deck).

---

## Stack

- **Go** — HTTP server (Echo), SSH/SFTP client, WebSocket log stream
- **Templ** — typed HTML templates
- **HTMX** — partial swaps, form posts
- **Alpine.js** — WebSocket log panel, ephemeral UI state
- **Tailwind CSS 4** — dark-first, `web/styles/input.css` → embedded static

See [docs/HTMX_ALPINE.md](docs/HTMX_ALPINE.md) for UI conventions.

---

## Security notes

- **DNS token never stored here.** The token is written directly to the UDM via SSH (`/data/udm-le/udm-le.env`). The optional Verify field is POST-only and cleared from memory immediately.
- `data/` holds settings and runtime state — keep it out of untrusted backups.
- UniFi OS upgrades can reset udm-le state. Check the [udm-le README](https://github.com/kchristensen/udm-le/blob/master/README.md) before firmware updates.
- Prefer a **dedicated `known_hosts` file** via `ssh-keyscan` to avoid `knownhosts: key mismatch` errors.

---

## Changing the poll interval

**Cert check interval (hours)** in Settings controls how often the app reads the cert over SSH. Restart after changing it (`./scripts/reload.sh`).

---

## Issues & contributing

Found a bug? Something not working with your UDM model, DNS provider, or SSH setup? **[Open an issue](https://github.com/niski84/unifi-cert-smash-deck/issues)** — include your UDM model, DNS provider, and any error output from the log panel. Pull requests are welcome.
