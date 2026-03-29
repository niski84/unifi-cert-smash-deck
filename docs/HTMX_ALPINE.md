# HTMX + Alpine conventions (UniFi Cert Smash Deck)

This app keeps **most behavior in Go** (routes, lifecycle, persistence) and uses a thin, declarative UI layer.

## Dark / light theme

- **Mechanism:** Tailwind v4 **class** dark mode via `@custom-variant dark (&:where(.dark, .dark *));` in `web/styles/input.css`. The `<html>` element gets the `dark` class when dark mode is active.
- **FOUC:** A short inline script in `Layout` runs before CSS and sets `dark` from `localStorage` key `unificert-theme` (`dark` | `light`). If unset, default is **dark**.
- **Toggle:** Header button uses vanilla `onclick` (no Alpine dependency for theme) to flip `document.documentElement.classList` and persist the key.
- **Styling:** Prefer paired utilities, e.g. `bg-zinc-50 dark:bg-zinc-950`, `text-zinc-900 dark:text-white`, so both themes stay readable. `<meta name="color-scheme" content="dark light">` plus `color-scheme` on `html` helps native controls and scrollbars.

## HTMX

- **Partial swaps:** `GET /fragment/status` returns a self-contained `StatusFragment` that includes its own `hx-get`, `hx-trigger`, and `hx-swap="outerHTML"` on the root node. After each swap, polling continues without extra JavaScript.
- **Commands:** `POST /api/sync` and `POST /api/settings` return small HTML fragments (`SyncFeedback` or settings toast) via Templ. Targets use predictable `id`s (`#sync-feedback`, `#settings-toast`).
- **Principle:** Prefer one HTMX request that returns HTML over hand-written `fetch` + DOM updates unless you need a persistent client-side connection (see WebSocket).

## Alpine.js

- **Scope:** Used lightly for the **WebSocket log viewer** (`x-data`, `x-init`, `x-text`) where HTMX does not maintain a long-lived connection.
- **Coexistence:** Alpine runs `defer`; HTMX runs without `defer`. Avoid putting `x-data` on elements that HTMX replaces wholesale unless the fragment intentionally re-instantiates Alpine (not needed for the status panel).

## Templ

- **Exported components** use PascalCase (`DashboardPage`, `StatusFragment`) so `internal/certdeck` can render them.
- After editing `*.templ`, run `go generate` or `./scripts/compile.sh` so `*_templ.go` stays in sync.

## Tailwind 4

- Source CSS: `web/styles/input.css` with `@source` globs over `internal/certdeck/**/*.templ` and `**/*.go`.
- Built asset: `web/certdeck/static/app.css` (run `npm run build:css`). The binary embeds `web/certdeck/static/` via `web/embed.go`.
