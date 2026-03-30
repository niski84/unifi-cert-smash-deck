// printbootstrap prints BuildUdmBootstrapShell to stdout (same config as the server).
// Usage from repo root: go run ./cmd/printbootstrap | ssh root@UDM 'sh -s'
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/niski84/unifi-cert-smash-deck/internal/certdeck"
)

func main() {
	loadDotEnv()
	cfg := certdeck.LoadAppConfig(certdeck.DefaultSettingsPath())
	fmt.Print(certdeck.BuildUdmBootstrapShell(cfg))
}

func loadDotEnv() {
	try := func(path string) {
		path = filepath.Clean(path)
		if _, err := os.Stat(path); err != nil {
			return
		}
		_ = godotenv.Load(path)
	}
	if gp := os.Getenv("GOPROJECTS"); gp != "" {
		try(filepath.Join(gp, "unifi-smash-deck", ".env"))
	}
	if d, err := os.Getwd(); err == nil {
		try(filepath.Join(d, ".env"))
		try(filepath.Join(d, "..", "unifi-smash-deck", ".env"))
	}
}
