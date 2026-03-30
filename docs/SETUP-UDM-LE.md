# Step-by-step: Let’s Encrypt on your UDM Pro with udm-le

This guide assumes you run the **UniFi Cert Smash Deck** helper on your Mac or PC, and **udm-le** on the **UDM Pro**. Nothing in this flow uses the UniFi “API” to install certificates — Ubiquiti does not expose a supported API for writing `/data/udm-le` or `udm-le.env`. You use **SSH to the gateway** (one paste of an auto-generated script).

---

## What you need before you start

| Item | Why |
|------|-----|
| **Domain** you control (e.g. `home.example.com`) | The name on the TLS certificate. |
| **DNS hosted somewhere udm-le supports** (e.g. Cloudflare) | DNS-01 challenge creates a temporary `_acme-challenge` TXT record. |
| **Cloudflare API token** (if using Cloudflare) | Permissions: **Zone → DNS → Edit** and **Zone → Zone → Read** on that zone. |
| **Email address** | Let’s Encrypt registration / notices. |
| **UDM Pro LAN IP** and **SSH enabled** | UniFi OS: enable SSH; shell user is **`root`** with the **console SSH password** (see below). |

You do **not** need to give this helper app your Cloudflare token for normal setup. The token belongs **only** in `udm-le.env` **on the UDM**. The helper can **verify** a token once (optional); it does not save it.

---

## SSH to the UDM — what actually works (lessons learned)

These are the issues people hit most often; fixing them is required before **Test SSH** or `ssh-copy-id` can succeed.

| Symptom | What it means | What to do |
|--------|----------------|------------|
| **`Connection refused`** on port 22 | SSH daemon not listening yet | In UniFi: enable **SSH** on the **console / gateway** (not only a switch AP). Apply settings, wait 1–2 minutes. Use the gateway **IP** (e.g. `192.168.1.201`), not a `https://` URL, in SSH and in **SSH host** here. |
| **`Permission denied`** after a password prompt | Wrong **Linux user** or wrong **password** | On **UniFi OS (UDM / UDM Pro)**, the SSH user is almost always **`root`**, with the password you set in the **same place you enabled SSH** — **not** your Ubiquiti account email / cloud username. Official note: [UniFi — Debug tools & SSH](https://help.ui.com/hc/en-us/articles/204909374-UniFi-Connect-with-Debug-Tools-SSH). |
| **`knownhosts: key mismatch`** (this app) or odd SSH host-key errors | Stale or conflicting lines in `~/.ssh/known_hosts` (including **hashed** entries) | Run `ssh-keygen -R YOUR_UDM_IP`, then `ssh-keyscan YOUR_UDM_IP` and put **only** those lines in a **small file** (e.g. `~/.ssh/known_hosts_unifi`) and set **SSH known_hosts** in Settings (or `UNIFICERT_SSH_KNOWN_HOSTS` in `.env`) to that path. |
| **UniFi only offers `keyboard-interactive`** | Normal on UniFi OS | This app supports **`UNIFICERT_SSH_PASSWORD`** in `.env` (use **double quotes** around the value when it contains `&` or spaces). After **`ssh-copy-id`**, you can rely on your **Ed25519 key** and remove the password from `.env` if you prefer. |

**Recommended path:** enable SSH → `ssh-copy-id -i ~/.ssh/id_ed25519.pub root@UDM_IP` (use **keyboard-interactive** if prompted: `-o PreferredAuthentications=keyboard-interactive,password`) → set **SSH user** = `root`, **key path**, **known_hosts** in the helper → **Test SSH**.

---

## Part A — On your computer (Cert Smash Deck)

1. Start the app (`./scripts/reload.sh` or your usual command) and open the dashboard (e.g. `http://127.0.0.1:8105/`).

2. Under **Settings**, fill in:
   - **CERT_EMAIL** — your email.
   - **CERT_HOSTS** — comma-separated hostnames, e.g. `unifi.home.example.com` (must match DNS and what you type in the browser).
   - **DNS provider** — e.g. Cloudflare.
   - Click **Save settings**.

3. (Optional) Paste your Cloudflare token into **Verify Cloudflare token** and submit. Fix permissions if it fails.

4. In the yellow **Install script** box, click **Copy script**.  
   This script is generated from your saved settings. It does **not** contain your real token — you will paste the token on the UDM in the next part.

5. (Optional but useful) Fill **SSH host** = UDM **IP**, **SSH user** = **`root`**, **SSH private key path**, **known_hosts** (dedicated file is best — see table above). Optional: **`UNIFICERT_SSH_PASSWORD`** in `.env` until key auth works. **Save**, then **Test SSH** and **Check cert now** to confirm the ring shows expiry.

---

## Part B — On the UDM Pro (SSH)

1. From a terminal on your computer:

   ```bash
   ssh root@YOUR_UDM_IP
   ```

   Accept the host key if prompted (or use `known_hosts` properly).

2. **Paste the entire install script** you copied (right-click / middle-click in the SSH window), then press **Enter**.

   What it does:
   - If **udm-le is not installed**: downloads [kchristensen/udm-le](https://github.com/kchristensen/udm-le) (`main` branch tarball) into `/data/udm-le`.
   - If **udm-le is already installed**: skips download and only **appends** a marked block to `udm-le.env` (it will not append twice with the same marker).
   - **Warning:** On first-time install the script removes an existing `/data/udm-le` directory if it is not a valid udm-le install — do not use this on a folder you customized without backup.

3. Edit the config on the UDM and set your **real** DNS token:

   ```bash
   nano /data/udm-le/udm-le.env
   ```

   Find `CLOUDFLARE_DNS_API_TOKEN` (or your provider’s variables) and replace the placeholder with your token.  
   Save: `Ctrl+O`, Enter, `Ctrl+X`.

4. Run the initial issuance and service setup (this can take a few minutes):

   ```bash
   /data/udm-le/udm-le.sh initial
   ```

5. Confirm a renewal timer exists (wording may vary slightly by OS version):

   ```bash
   systemctl list-timers | grep -i udm-le
   ```

6. In the browser, open `https://YOUR_HOSTNAME` (the one in `CERT_HOSTS`) and check the certificate is valid.

---

## If something fails

- **DNS / Cloudflare:** Token must include DNS edit on the **zone** that contains the hostname. Hostname must match `CERT_HOSTS`.
- **SSH:** [Ubiquiti SSH article](https://help.ui.com/hc/en-us/articles/115005159588-UniFi-Using-SSH-to-connect-to-your-device).
- **udm-le errors:** Read the script output and the [udm-le README](https://github.com/kchristensen/udm-le/blob/master/README.md). After UniFi OS upgrades, check for udm-le updates too.

---

## What the helper automates vs what you still do

| Automated by helper | You still do |
|---------------------|--------------|
| Builds `udm-le.env` fragment from your form | Enable SSH on UDM; run `ssh root@…` |
| Generates one-shot **install shell script** (download udm-le + append settings) | Paste script on UDM; edit token in `udm-le.env` |
| Optional: verify Cloudflare token (not saved) | Run `udm-le.sh initial` on the UDM |
| Optional: read cert expiry over SSH from your PC | Keep DNS API token **only** on the UDM |

There is **no** supported UniFi API replacement for the SSH + `udm-le` steps above for installing Let’s Encrypt this way.

---

## Where you are on the journey — what to do next

Use this as a single checklist. Skip steps you already finished.

| Step | Done? | Action |
|------|--------|--------|
| **1** | ☐ | **Helper app runs** (`./scripts/reload.sh`), dashboard opens. |
| **2** | ☐ | **SSH from PC to UDM works** as `root@IP` (enable SSH in UniFi; fix user/password/known_hosts per table above). |
| **3** | ☐ | **Optional:** `ssh-copy-id` so the helper can use **key-only** auth; optional **`UNIFICERT_SSH_PASSWORD`** in `.env` until then. |
| **4** | ☐ | In the helper: **CERT_EMAIL**, **CERT_HOSTS**, DNS provider → **Save**; **Test SSH** shows cert read OK. |
| **5** | ☐ | **Copy install script** (yellow box) → paste in **`ssh root@IP`** session on UDM → Enter. |
| **6** | ☐ | On UDM: **`nano /data/udm-le/udm-le.env`** → set real **Cloudflare** (or provider) secrets. |
| **7** | ☐ | On UDM: **`/data/udm-le/udm-le.sh initial`** → wait until it completes. |
| **8** | ☐ | **`systemctl list-timers \| grep udm-le`** — confirm renewal timer. |
| **9** | ☐ | Browser: open **`https://`** your hostname from **CERT_HOSTS** — certificate should be valid. |
| **10** | ☐ | **Optional:** remove **`UNIFICERT_SSH_PASSWORD`** from `.env` if key-only SSH is enough; rotate SSH password if it was ever shared. |

**You are “done” with the painful part** when step **4** passes (helper reads the gateway cert) **and** step **9** shows a valid Let’s Encrypt (or staging) cert in the browser. Everything before that is connectivity and udm-le install; everything after is maintenance (udm-le renews on a timer).
