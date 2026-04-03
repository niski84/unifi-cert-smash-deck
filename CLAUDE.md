# Smash Deck Web Stack

Use this pattern for `*-smash-deck` Go web apps.

## Core Stack

| Layer | Choice |
|-------|--------|
| Server | Go — `net/http` or **Echo** (`github.com/labstack/echo/v4`) |
| Templates | **Templ** (`github.com/a-h/templ`) — typed components, `templ generate` → `*_templ.go` |
| Interactivity | **HTMX** — partial swaps, forms, `hx-*` attributes; keep behavior server-driven |
| Client behavior | **Alpine.js** — only where HTMX is awkward (WebSocket client, ephemeral UI state) |
| CSS | **Tailwind CSS 4** — `@import "tailwindcss"`, `@source` globs over `**/*.templ` and `**/*.go` |

## Repo Layout

```
cmd/<app>/main.go
internal/<pkg>/            # domain logic, handlers, schedulers
internal/<pkg>/views/*.templ  # UI — exported components use PascalCase
web/<app>/static/          # built assets
web/embed.go               # //go:embed
scripts/compile.sh         # templ generate + npm run build:css + go build
scripts/reload.sh          # rebuild, restart, probe /api/health
```

## HTMX + Alpine Rules
- One HTML response per action; avoid ad hoc `fetch` + manual DOM updates
- Self-contained HTMX fragments that poll must carry `hx-*` on their **root** node (outerHTML swap must not drop triggers)
- Document swap conventions in `docs/HTMX_ALPINE.md`

## Theming
- Dark-first with toggle: class-based `dark` on `<html>`, Tailwind `@custom-variant dark`, localStorage persistence, inline head script to prevent flash. See global CLAUDE.md dark theme rules.
