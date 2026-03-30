#!/bin/bash
set -euo pipefail

_reload_ok_double_beep() {
	local _n
	for _n in 1 2; do
		printf '\a' 2>/dev/null || true
		sleep 0.12
	done
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
BINARY="$PROJECT_DIR/unifi-cert-smash-deck"

echo "=== UniFi Cert Smash Deck reload ==="
echo "→ Stopping existing process..."
pkill -f "$BINARY" 2>/dev/null && sleep 1 || echo "  (none running)"

# ── vault: pull fresh secrets from Infisical before sourcing .env ─────────────
_VAULT_SYNC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd ../../infrastructure && pwd)/sync-secrets.sh"
if [[ -f "$_VAULT_SYNC" ]] && [[ -n "${INFISICAL_CLIENT_ID:-}" ]]; then
  echo "→ Pulling secrets from vault (unifi-cert-smash-deck)..."
  "$_VAULT_SYNC" --pull unifi-cert-smash-deck 2>/dev/null || echo "  ⚠  Vault pull skipped (using cached .env)"
fi
# ─────────────────────────────────────────────────────────────────────────────

if [ -f "$PROJECT_DIR/.env" ]; then
	set -a
	# shellcheck disable=SC1090
	source "$PROJECT_DIR/.env"
	set +a
fi

echo "→ Building..."
cd "$PROJECT_DIR"
go run github.com/a-h/templ/cmd/templ@latest generate -path ./internal/certdeck/views
if [ -f package.json ] && [ -d node_modules ]; then
	npm run build:css
fi
go build ./...
go build -o "$BINARY" ./cmd/unificert
echo "  Build OK: $BINARY"

PORT="${PORT:-8105}"
export PORT
GOPROJECTS="${GOPROJECTS:-$(dirname "$PROJECT_DIR")}"
export GOPROJECTS
echo "→ Starting on :${PORT}  GOPROJECTS=${GOPROJECTS}..."
nohup env PORT="$PORT" GOPROJECTS="$GOPROJECTS" "$BINARY" >"$PROJECT_DIR/unifi-cert-smash-deck.log" 2>&1 &
echo $! >"$PROJECT_DIR/unifi-cert-smash-deck.pid"

for i in $(seq 1 30); do
	sleep 0.2
	if curl -fsS "http://127.0.0.1:${PORT}/api/health" >/dev/null 2>&1; then
		echo "✓ UniFi Cert Smash Deck at http://127.0.0.1:${PORT}/"
		_reload_ok_double_beep
		exit 0
	fi
done
echo "✗ Server did not respond — check unifi-cert-smash-deck.log"
exit 1
