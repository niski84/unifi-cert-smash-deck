package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/niski84/unifi-cert-smash-deck/internal/certdeck"
)

func main() {
	settings := certdeck.DefaultSettingsPath()
	svc := certdeck.NewService(settings)
	cfg := svc.SnapshotConfig()

	log.Printf("[unificert] data directory : %s", certdeck.DataDir())
	log.Printf("[unificert] settings file  : %s", settings)
	if cfg.Domain != "" {
		log.Printf("[unificert] domain         : %s", cfg.Domain)
	} else {
		log.Printf("[unificert] domain         : (not configured — use Settings in the UI)")
	}
	if cfg.SSHHost != "" {
		log.Printf("[unificert] ssh target     : %s:%d", cfg.SSHHost, effectivePort(cfg.SSHPort))
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
		WriteTimeout: 0, // WebSocket / long renew
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
