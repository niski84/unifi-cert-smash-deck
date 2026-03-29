package web

import "embed"

// FS holds static assets under web/certdeck/ (CSS, favicon, etc.).
//
//go:embed certdeck
var FS embed.FS
