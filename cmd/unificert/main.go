package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/niski84/unifi-cert-smash-deck/internal/certdeck"
)

func main() {
	loadDotEnv()

	settings := certdeck.DefaultSettingsPath()
	svc := certdeck.NewService(settings)
	cfg := svc.SnapshotConfig()

	log.Printf("[unificert] data directory : %s", certdeck.DataDir())
	log.Printf("[unificert] settings file  : %s", settings)
	log.Printf("[unificert] mode            : udm-le helper (no remote ACME)")
	if cfg.CertHosts != "" {
		log.Printf("[unificert] cert hosts      : %s", cfg.CertHosts)
	}
	if cfg.SSHHost != "" {
		log.Printf("[unificert] ssh target      : %s:%d", cfg.SSHHost, effectivePort(cfg.SSHPort))
	}
	if cfg.UniFiAPIKey != "" {
		log.Printf("[unificert] unifi api key   : loaded (optional client-count log)")
	}

	e, err := certdeck.NewEcho(svc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "echo setup: %v\n", err)
		os.Exit(1)
	}

	svc.StartScheduler()
	defer svc.StopScheduler()

	addr := net.JoinHostPort("", cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      e,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("[unificert] listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("[unificert] shutdown complete")
}

func effectivePort(p int) int {
	if p == 0 {
		return 22
	}
	return p
}

// loadDotEnv loads env files in order; later files override earlier ones.
func loadDotEnv() {
	ordered := getEnvPaths()
	for _, p := range ordered {
		_ = godotenv.Load(p)
	}
}

func getEnvPaths() []string {
	seen := make(map[string]bool)
	var ordered []string
	add := func(p string) {
		p = filepath.Clean(p)
		if seen[p] {
			return
		}
		if _, err := os.Stat(p); err == nil {
			seen[p] = true
			ordered = append(ordered, p)
		}
	}

	// 1. GOPROJECTS sibling
	if gp := os.Getenv("GOPROJECTS"); gp != "" {
		add(filepath.Join(gp, "unifi-smash-deck", ".env"))
	}
	// 2. Relative sibling
	if cwd, err := os.Getwd(); err == nil {
		add(filepath.Join(cwd, "..", "unifi-smash-deck", ".env"))
		add(filepath.Join(cwd, ".env"))
	}
	// 3. Executable relative
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		add(filepath.Join(exeDir, "..", "unifi-smash-deck", ".env"))
		add(filepath.Join(exeDir, ".env"))
	}

	return ordered
}
