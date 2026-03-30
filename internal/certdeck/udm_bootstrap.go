package certdeck

import (
	"fmt"
	"strings"
)

const smashMarker = "# --- Smash Deck overrides (append once) ---"

// BuildUdmBootstrapShell returns a shell script to run on the UniFi gateway as root over SSH.
// It either installs udm-le from GitHub (main branch tarball) or, if already present, only appends
// the Smash Deck env fragment once. It does not embed live secrets — replace token placeholders on-device.
//
// See https://github.com/kchristensen/udm-le#installation
func BuildUdmBootstrapShell(cfg AppConfig) string {
	frag := strings.TrimSpace(BuildUdmLeEnvSnippet(cfg))
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# UniFi Cert Smash Deck — run on your UDM Pro (or other UniFi OS device) as root.\n")
	b.WriteString("# Paste this entire script into an SSH session.\n")
	b.WriteString("# Ref: https://github.com/kchristensen/udm-le#installation\n")
	b.WriteString("set -eu\n")
	b.WriteString("UDM_LE=/data/udm-le\n")
	fmt.Fprintf(&b, "MARKER=%q\n", smashMarker)
	b.WriteString(`
if [ -f "$UDM_LE/udm-le.env" ] && grep -qF "$MARKER" "$UDM_LE/udm-le.env" 2>/dev/null; then
  echo "Smash Deck block already in udm-le.env — edit $UDM_LE/udm-le.env or remove the old block first."
  exit 0
fi

if [ -f "$UDM_LE/udm-le.sh" ]; then
  echo "udm-le already installed — appending Smash Deck overrides to udm-le.env"
else
  echo "Installing udm-le from GitHub (main) into $UDM_LE …"
  TMPDIR=$(mktemp -d)
  trap 'rm -rf "$TMPDIR"' EXIT
  curl -fsSL https://github.com/kchristensen/udm-le/archive/refs/heads/main.tar.gz | tar xz -C "$TMPDIR"
  install -d /data
  if [ -d "$UDM_LE" ]; then
    echo "Removing existing $UDM_LE …"
    rm -rf "$UDM_LE"
  fi
  SRC=$(find "$TMPDIR" -maxdepth 1 -type d -name 'udm-le-*' | head -1)
  if [ -z "$SRC" ] || [ ! -d "$SRC" ]; then
    echo "Could not find udm-le-* directory in tarball (expected udm-le-main)." >&2
    exit 1
  fi
  mv "$SRC" "$UDM_LE"
  trap - EXIT
  rm -rf "$TMPDIR"
  echo "udm-le files installed."
fi

printf '\n%s\n' "$MARKER" >> "$UDM_LE/udm-le.env"
cat >> "$UDM_LE/udm-le.env" <<'SMASH_ENV_FRAG'
`)
	b.WriteString(frag)
	b.WriteString(`
SMASH_ENV_FRAG

echo ""
echo "Next on the UDM:"
echo "  1. Edit $UDM_LE/udm-le.env — set CLOUDFLARE_DNS_API_TOKEN (or your DNS provider vars)."
echo "  2. Run: $UDM_LE/udm-le.sh initial"
echo "  3. Confirm renewal timer: systemctl list-timers | grep udm-le"
`)
	return b.String()
}
