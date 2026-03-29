package certdeck

import (
	"context"
	"sync"
	"time"
)

// Service coordinates scheduling, persistence, and certificate lifecycle.
type Service struct {
	mu           sync.RWMutex
	runMu        sync.Mutex
	cfg          AppConfig
	settingsPath string
	state        *StateStore
	log          *DeckLogger
	sched        *Scheduler
}

func NewService(settingsPath string) *Service {
	cfg := LoadAppConfig(settingsPath)
	s := &Service{
		cfg:          cfg,
		settingsPath: settingsPath,
		state:        NewStateStore(),
		log:          NewDeckLogger(),
	}
	s.log.Info("UniFi Cert Smash Deck started (see Settings to configure).")
	return s
}

func (s *Service) SnapshotConfig() AppConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Service) ReplaceConfig(c AppConfig) {
	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
}

func (s *Service) Logger() *DeckLogger { return s.log }

func (s *Service) State() *StateStore { return s.state }

func (s *Service) SettingsPath() string { return s.settingsPath }

// RunCycle checks the remote certificate and renews when needed or when forceRenew is true.
func (s *Service) RunCycle(ctx context.Context, forceRenew bool) {
	s.runMu.Lock()
	defer s.runMu.Unlock()

	cfg := s.SnapshotConfig()
	now := time.Now()
	_ = s.state.Update(func(st *RuntimeState) { st.LastCheckAt = now })

	if cfg.Domain == "" || cfg.SSHHost == "" {
		s.log.Info("Cycle skipped: set domain and SSH host in Settings.")
		_ = s.state.Update(func(st *RuntimeState) { st.LastError = "not configured" })
		return
	}

	th := cfg.RenewWithinDays
	if th <= 0 {
		th = 30
	}
	threshold := time.Duration(th) * 24 * time.Hour

	sshC := NewSSHUnifi(cfg)
	s.log.Info("Checking remote certificate (%s)…", cfg.RemoteCertPath)
	cn, notAfter, errRead := sshC.RemoteCertInfo(ctx)
	if errRead != nil {
		s.log.Warn("Remote cert read failed: %v", errRead)
		_ = s.state.Update(func(st *RuntimeState) { st.LastError = errRead.Error() })
		if !forceRenew {
			return
		}
		s.log.Info("Forced sync: proceeding to ACME despite read error.")
	} else {
		dleft := int(time.Until(notAfter).Hours() / 24)
		s.log.Info("Certificate CN=%q — %d day(s) until expiry.", cn, dleft)
		_ = s.state.Update(func(st *RuntimeState) {
			st.CommonName = cn
			st.NotAfter = notAfter
			st.LastError = ""
		})
		if !forceRenew && time.Until(notAfter) > threshold {
			s.log.Info("Above %d-day renewal threshold — no action.", th)
			if cfg.UniFiActiveClientsPoll {
				if n, e := ActiveWiFiClients(ctx, cfg); e == nil {
					s.log.Info("Network health: %d Wi‑Fi client(s) active.", n)
					_ = s.state.Update(func(st *RuntimeState) { st.LastActiveClients = n })
				}
			}
			return
		}
		if forceRenew {
			s.log.Info("Manual sync requested — renewing now.")
		} else {
			s.log.Info("Within renewal window — renewing.")
		}
	}

	_ = s.state.Update(func(st *RuntimeState) { st.Renewing = true })
	s.log.Info("ACME DNS-01 for %q (staging=%v)…", cfg.Domain, cfg.ACMEUseStaging)
	certPEM, keyPEM, err := ObtainCertificate(ctx, cfg)
	if err != nil {
		s.log.Warn("ACME failed: %v", err)
		_ = s.state.Update(func(st *RuntimeState) {
			st.Renewing = false
			st.LastError = err.Error()
		})
		return
	}
	s.log.Info("Installing certificate and restarting unifi-core…")
	if err := sshC.InstallCertificate(ctx, certPEM, keyPEM); err != nil {
		s.log.Warn("Install failed: %v", err)
		_ = s.state.Update(func(st *RuntimeState) {
			st.Renewing = false
			st.LastError = err.Error()
		})
		return
	}
	syncAt := time.Now()
	cn2, na2, err3 := sshC.RemoteCertInfo(ctx)
	_ = s.state.Update(func(st *RuntimeState) {
		st.Renewing = false
		st.LastSyncAt = syncAt
		st.LastError = ""
		if err3 == nil {
			st.CommonName = cn2
			st.NotAfter = na2
		}
	})
	s.log.Info("Renewal complete.")
}

func (s *Service) StartScheduler() {
	interval := time.Duration(s.SnapshotConfig().CheckIntervalHours) * time.Hour
	if interval < time.Hour {
		interval = 24 * time.Hour
	}
	s.sched = NewScheduler(interval, func(ctx context.Context, force bool) {
		s.RunCycle(ctx, force)
	})
	s.sched.Start()
}

func (s *Service) StopScheduler() {
	if s.sched != nil {
		s.sched.Stop()
	}
}
