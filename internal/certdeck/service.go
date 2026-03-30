package certdeck

import (
	"context"
	"sync"
	"time"
)

// Service coordinates scheduling, persistence, and the udm-le helper UI.
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
	s.log.Info("UniFi Cert Smash Deck started — udm-le helper (see Settings).")
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

// RunCheckCycle reads the gateway certificate over SSH (if configured) and optionally polls UniFi API.
func (s *Service) RunCheckCycle(ctx context.Context) {
	s.runMu.Lock()
	defer s.runMu.Unlock()

	cfg := s.SnapshotConfig()
	now := time.Now()
	_ = s.state.Update(func(st *RuntimeState) { st.LastCheckAt = now })

	if cfg.SSHHost == "" {
		s.log.Info("SSH not configured — skipping remote cert read (configure udm-le on the UDM).")
		_ = s.state.Update(func(st *RuntimeState) { st.LastError = "" })
		return
	}

	sshC := NewSSHUnifi(cfg)
	s.log.Info("Reading remote certificate (%s)…", cfg.RemoteCertPath)
	cn, notAfter, errRead := sshC.RemoteCertInfo(ctx)
	if errRead != nil {
		s.log.Warn("Remote cert read failed: %v", errRead)
		_ = s.state.Update(func(st *RuntimeState) {
			st.LastError = errRead.Error()
		})
		return
	}
	dleft := int(time.Until(notAfter).Hours() / 24)
	s.log.Info("Certificate CN=%q — %d day(s) until expiry.", cn, dleft)
	_ = s.state.Update(func(st *RuntimeState) {
		st.CommonName = cn
		st.NotAfter = notAfter
		st.LastError = ""
	})

	if cfg.UniFiActiveClientsPoll {
		if n, e := ActiveWiFiClients(ctx, cfg); e == nil {
			s.log.Info("Network health: %d Wi‑Fi client(s) active.", n)
			_ = s.state.Update(func(st *RuntimeState) { st.LastActiveClients = n })
		}
	}
}

func (s *Service) StartScheduler() {
	interval := time.Duration(s.SnapshotConfig().CheckIntervalHours) * time.Hour
	if interval < time.Hour {
		interval = 12 * time.Hour
	}
	s.sched = NewScheduler(interval, func(ctx context.Context) {
		s.RunCheckCycle(ctx)
	})
	s.sched.Start()
}

func (s *Service) StopScheduler() {
	if s.sched != nil {
		s.sched.Stop()
	}
}
