# UniFi Cert Smash Deck

**This app does not issue certificates.** It is a local web dashboard that helps you install and configure **[kchristensen/udm-le](https://github.com/kchristensen/udm-le)** on your UniFi gateway (UDM Pro, Dream Machine, etc.). Let's Encrypt issuance and renewal run on the UDM itself via udm-le. This tool:

- Builds a correct **`udm-le.env`** config snippet from your settings (email, `CERT_HOSTS`, DNS provider).
- Pushes the install script to the UDM over **SSH** with one button click — no terminal copy/paste needed.
- Optionally reads **installed cert expiry** from the gateway over SSH and shows it in a status ring.
- Offers an optional one-time **Cloudflare token check** (not saved) to verify permissions before running udm-le.

## Prerequisites

| Tool | Why |
|------|-----|
| **Go 1.23+** | Build the binary (`go build`) |
| **Node.js + npm** | Build the embedded Tailwind CSS |
| **Internet access on the UDM** | udm-le downloads Let's Encrypt certs at issuance time |

The build script runs `templ generate` via `go run` — no separate templ installation needed.

## Quick start

```bash
cd goprojects/unifi-cert-smash-deck
npm install
./scripts/compile.sh
cp .env.example .env        # edit at minimum: PORT (default 8105)
./scripts/reload.sh
```

Open `http://127.0.0.1:8105/` (or your `PORT`).

## Setup flow (the short version)

1. **Enable SSH** on the UDM in UniFi OS settings. SSH user is `root`; password is the console SSH password (not your Ubiquiti cloud login).
2. In **Settings**, fill in CERT_EMAIL, CERT_HOSTS, DNS provider, SSH host (gateway LAN IP), SSH user/key/password. **Save**.
3. Click **Test SSH** — it should read the cert and show success.
4. Click **Install Now** in the Automation panel — the app SSHes in and runs the install script. No terminal needed.
5. On the UDM: `nano /data/udm-le/udm-le.env` — replace the DNS token placeholder with your real credential.
6. On the UDM: `/data/udm-le/udm-le.sh initial` — runs the first issuance (takes a few minutes).
7. Open your CERT_HOSTS hostname over HTTPS and verify the cert is valid.

Full step-by-step walkthrough including SSH troubleshooting: **[docs/SETUP-UDM-LE.md](docs/SETUP-UDM-LE.md)**

## SSH setup

SSH credentials are used for both **installing udm-le** (Install Now button) and **reading cert expiry** (status ring). The minimum you need is host + user + password. For key-based auth:

```bash
# 1. Copy your key to the UDM (use keyboard-interactive if prompted)
ssh-copy-id -o PreferredAuthentications=keyboard-interactive,password -i ~/.ssh/id_ed25519.pub root@UDM_IP

# 2. Create a small known_hosts file for this app (avoids conflicts with your main known_hosts)
ssh-keyscan UDM_IP | grep -v '^#' > ~/.ssh/known_hosts_unifi

# 3. Set SSH known_hosts in Settings (or UNIFICERT_SSH_KNOWN_HOSTS in .env) to that path
```

Set `UNIFICERT_SSH_PASSWORD` in `.env` (with quotes if the password contains `&`) while setting up; remove it once key auth works.

## `.env` reference

```bash
PORT=8105
UNIFICERT_SSH_HOST=192.168.1.1       # gateway LAN IP — no https://
UNIFICERT_SSH_USER=root
UNIFICERT_SSH_KEY=/home/you/.ssh/id_ed25519
UNIFICERT_SSH_KNOWN_HOSTS=/home/you/.ssh/known_hosts_unifi
UNIFICERT_SSH_PASSWORD="your-ssh-password"   # temporary; prefer key auth
UNIFICERT_CERT_EMAIL=you@example.com
UNIFICERT_CERT_HOSTS=unifi.example.com
UNIFICERT_DNS_PROVIDER=cloudflare
```

All settings can also be saved via the web UI (stored in `data/unificert-settings.json`). `.env` overrides take effect after `./scripts/reload.sh`.

The binary also loads **`../unifi-smash-deck/.env`** (and `$GOPROJECTS/unifi-smash-deck/.env`) before this project's `.env`, so `UNIFI_HOST`, `UNIFI_API_KEY`, and `UNIFI_SITE` can be shared with UniFi Smash Deck.

## Stack

- **Go** — HTTP server, SSH/SFTP client, WebSocket log stream
- **Templ** — HTML templates
- **HTMX** — form posts and fragment swaps
- **Alpine.js** — log panel
- **Tailwind CSS 4** — `web/styles/input.css` → embedded static

See [docs/HTMX_ALPINE.md](docs/HTMX_ALPINE.md) for UI conventions.

## Security notes

- **Do not** put your DNS API token into this app's settings. The token belongs **on the UDM** in `/data/udm-le/udm-le.env`. The Cloudflare Verify field is ephemeral — POST only, never saved.
- Prefer a **dedicated `known_hosts` file** (from `ssh-keyscan`) via `UNIFICERT_SSH_KNOWN_HOSTS` — avoids `knownhosts: key mismatch` with a large or hashed default `known_hosts`.
- `data/` holds settings and state — keep it out of backups you do not trust.
- UniFi OS upgrades can reset or change udm-le state — check the [udm-le README](https://github.com/kchristensen/udm-le/blob/master/README.md) before firmware updates.

## Changing the poll interval

**Cert check interval (hours)** in Settings controls how often the app reads the gateway cert over SSH. Restart after changing it (`./scripts/reload.sh`).
