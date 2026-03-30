package certdeck

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/niski84/unifi-cert-smash-deck/internal/wizard"
)

// wizardSession holds in-memory secrets that are never persisted to disk.
type wizardSession struct {
	mu       sync.Mutex
	dnsToken string // cleared after writing to UDM
	sshPass  string // cleared after key deployment
}

func (ws *wizardSession) setDNSToken(t string) {
	ws.mu.Lock()
	ws.dnsToken = t
	ws.mu.Unlock()
}

func (ws *wizardSession) getDNSToken() string {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.dnsToken
}

func (ws *wizardSession) setSSHPass(p string) {
	ws.mu.Lock()
	ws.sshPass = p
	ws.mu.Unlock()
}

func (ws *wizardSession) getSSHPass() string {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.sshPass
}

func (ws *wizardSession) clearSSHPass() {
	ws.mu.Lock()
	ws.sshPass = ""
	ws.mu.Unlock()
}

// Service coordinates scheduling, persistence, and the udm-le helper UI.
type Service struct {
	mu           sync.RWMutex
	runMu        sync.Mutex
	cfg          AppConfig
	settingsPath string
	state        *StateStore
	log          *DeckLogger
	sched        *Scheduler

	// Wizard
	Wizard         *wizard.Store
	wizSession     *wizardSession
	installRunning atomic.Bool
	installErrMu   sync.Mutex
	installErr     string
}

func NewService(settingsPath string) *Service {
	cfg := LoadAppConfig(settingsPath)
	s := &Service{
		cfg:          cfg,
		settingsPath: settingsPath,
		state:        NewStateStore(),
		log:          NewDeckLogger(),
		Wizard:       wizard.NewStore(DataDir()),
		wizSession:   &wizardSession{},
	}
	s.log.Info("UniFi Cert Smash Deck started — setup wizard active (see /wizard).")
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

func (s *Service) IsInstallRunning() bool { return s.installRunning.Load() }

func (s *Service) InstallError() string {
	s.installErrMu.Lock()
	defer s.installErrMu.Unlock()
	return s.installErr
}

func (s *Service) setInstallError(err string) {
	s.installErrMu.Lock()
	s.installErr = err
	s.installErrMu.Unlock()
}

// StartInstall begins the async install goroutine for wizard step 4.
func (s *Service) StartInstall(cfg AppConfig, token string, action wizard.InstallAction, staging bool) {
	if s.installRunning.Swap(true) {
		return // already running
	}
	s.setInstallError("")

	go func() {
		defer s.installRunning.Store(false)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		// Bridge log channel to DeckLogger (broadcasts to WebSocket).
		logCh := make(chan string, 256)
		go func() {
			for line := range logCh {
				s.log.Info("[udm-le] %s", line)
			}
		}()

		err := WizInstall(ctx, cfg, token, action, staging, logCh)
		close(logCh)

		if err != nil {
			s.log.Warn("Install failed: %v", err)
			s.setInstallError(err.Error())
			s.Wizard.Update(func(st *wizard.State) {
				st.Results[wizard.StepInstall] = &wizard.StepResult{
					StepNum: wizard.StepInstall,
					Status:  "failed",
				}
			})
			return
		}

		s.log.Info("Install succeeded — running verify checks.")
		s.Wizard.Update(func(st *wizard.State) {
			st.Results[wizard.StepInstall] = &wizard.StepResult{
				StepNum:    wizard.StepInstall,
				Status:     "passed",
				FinishedAt: time.Now(),
			}
			if st.CurrentStep <= wizard.StepInstall {
				st.CurrentStep = wizard.StepVerify
			}
		})
	}()
}

// SyncConfigFromWizard writes the wizard SSH/cert settings into AppConfig and saves.
// Called when the wizard completes step 5 successfully.
func (s *Service) SyncConfigFromWizard() {
	st := s.Wizard.Snapshot()
	s.mu.Lock()
	s.cfg.SSHHost = st.UDMHost
	s.cfg.SSHPort = st.UDMPort
	s.cfg.SSHUser = st.SSHUser
	s.cfg.SSHKeyPath = st.SSHKeyPath
	s.cfg.SSHKnownHosts = st.SSHKnownHosts
	s.cfg.CertEmail = st.CertEmail
	s.cfg.CertHosts = st.CertHosts
	s.cfg.DNSProvider = st.DNSProvider
	if s.cfg.RemoteCertPath == "" {
		s.cfg.RemoteCertPath = "/data/unifi-core/config/unifi-core.crt"
	}
	cfg := s.cfg
	s.mu.Unlock()
	_ = SaveAppConfig(s.settingsPath, cfg)
}

// RunCheckCycle reads the gateway certificate over SSH (if configured) and optionally polls UniFi API.
func (s *Service) RunCheckCycle(ctx context.Context) {
	s.runMu.Lock()
	defer s.runMu.Unlock()

	cfg := s.SnapshotConfig()
	now := time.Now()
	_ = s.state.Update(func(st *RuntimeState) { st.LastCheckAt = now })

	if cfg.SSHHost == "" {
		s.log.Info("SSH not configured — skipping remote cert read.")
		_ = s.state.Update(func(st *RuntimeState) { st.LastError = "" })
		return
	}

	sshC := NewSSHUnifi(cfg)
	s.log.Info("Reading remote certificate (%s)…", cfg.RemoteCertPath)
	cn, notAfter, errRead := sshC.RemoteCertInfo(ctx)
	if errRead != nil {
		s.log.Warn("Remote cert read failed: %v", errRead)
		_ = s.state.Update(func(st *RuntimeState) { st.LastError = errRead.Error() })
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
