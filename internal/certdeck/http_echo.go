package certdeck

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/niski84/unifi-cert-smash-deck/internal/certdeck/views"
	certweb "github.com/niski84/unifi-cert-smash-deck/web"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// NewEcho wires routes for the dashboard, API, static assets, and WebSocket log stream.
func NewEcho(svc *Service) (*echo.Echo, error) {
	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	e.Use(middleware.Logger())

	staticRoot, err := fs.Sub(certweb.FS, "certdeck/static")
	if err != nil {
		return nil, err
	}
	fsHandler := http.FileServer(http.FS(staticRoot))
	e.GET("/static/*", echo.WrapHandler(http.StripPrefix("/static/", fsHandler)))

	e.GET("/", func(c echo.Context) error {
		return renderHTML(c, http.StatusOK, views.DashboardPage(svc.statusViewModel(), svc.settingsViewModel()))
	})
	e.GET("/fragment/status", func(c echo.Context) error {
		return renderHTML(c, http.StatusOK, views.StatusFragment(svc.statusViewModel()))
	})

	e.GET("/api/health", func(c echo.Context) error {
		cfg := svc.SnapshotConfig()
		return c.JSON(http.StatusOK, map[string]any{
			"service":              "unifi-cert-smash-deck",
			"mode":                 "udm-le-helper",
			"data_dir":             DataDir(),
			"cert_hosts_configured": strings.TrimSpace(cfg.CertHosts) != "",
			"ssh_host_configured":   strings.TrimSpace(cfg.SSHHost) != "",
			"unifi_api_key_loaded":  strings.TrimSpace(cfg.UniFiAPIKey) != "",
		})
	})

	e.POST("/api/settings", svc.handleSettingsForm)
	e.POST("/api/check-cert", svc.handleCheckCertNow)
	e.POST("/api/test/cloudflare", svc.handleTestCloudflare)
	e.POST("/api/test/ssh", svc.handleTestSSH)
	e.POST("/api/install-udm-le", svc.handleInstallUdmLe)

	e.GET("/ws/log", svc.handleWSLog)

	return e, nil
}

func renderHTML(c echo.Context, code int, comp templ.Component) error {
	var buf bytes.Buffer
	if err := comp.Render(c.Request().Context(), &buf); err != nil {
		return err
	}
	return c.HTMLBlob(code, buf.Bytes())
}

func (svc *Service) handleSettingsForm(c echo.Context) error {
	if err := c.Request().ParseForm(); err != nil {
		return renderHTML(c, http.StatusBadRequest, views.SyncFeedback(false, "Invalid form."))
	}
	f := c.Request().Form
	cur := svc.SnapshotConfig()

	if v := strings.TrimSpace(f.Get("port")); v != "" {
		cur.Port = v
	}
	if v := strings.TrimSpace(f.Get("cert_email")); v != "" {
		cur.CertEmail = v
	}
	if v := strings.TrimSpace(f.Get("cert_hosts")); v != "" {
		cur.CertHosts = v
	}
	if v := strings.TrimSpace(f.Get("cert_days_before_renewal")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cur.CertDaysBeforeRenewal = n
		}
	}
	if v := strings.TrimSpace(f.Get("dns_provider")); v != "" {
		cur.DNSProvider = v
	}
	if v := strings.TrimSpace(f.Get("ssh_host")); v != "" {
		cur.SSHHost = v
	}
	if v := strings.TrimSpace(f.Get("ssh_port")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cur.SSHPort = n
		}
	}
	if v := strings.TrimSpace(f.Get("ssh_user")); v != "" {
		cur.SSHUser = v
	}
	if v := strings.TrimSpace(f.Get("ssh_password")); v != "" && !isMaskedSecret(v) {
		cur.SSHPassword = v
	}
	if v := strings.TrimSpace(f.Get("ssh_key_path")); v != "" {
		cur.SSHKeyPath = v
	}
	if v := strings.TrimSpace(f.Get("ssh_known_hosts")); v != "" {
		cur.SSHKnownHosts = v
	}
	if v := strings.TrimSpace(f.Get("remote_cert_path")); v != "" {
		cur.RemoteCertPath = v
	}
	if v := strings.TrimSpace(f.Get("check_interval_hours")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cur.CheckIntervalHours = n
		}
	}
	cur.UniFiActiveClientsPoll = f.Get("unifi_active_clients_poll") == "true"
	if v := strings.TrimSpace(f.Get("unifi_host")); v != "" {
		cur.UniFiHost = strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(f.Get("unifi_site")); v != "" {
		cur.UniFiSite = v
	}
	if v := strings.TrimSpace(f.Get("unifi_api_key")); v != "" && !isMaskedSecret(v) {
		cur.UniFiAPIKey = v
	}

	// Overload from .env files again
	if gp := os.Getenv("GOPROJECTS"); gp != "" {
		_ = godotenv.Overload(filepath.Join(gp, "unifi-cert-smash-deck", ".env"))
	}
	if cwd, err := os.Getwd(); err == nil {
		_ = godotenv.Overload(filepath.Join(cwd, ".env"))
	}

	if err := SaveAppConfig(svc.SettingsPath(), cur); err != nil {
		return renderHTML(c, http.StatusInternalServerError, views.SyncFeedback(false, "Save failed: "+err.Error()))
	}
	svc.ReplaceConfig(cur)
	return renderHTML(c, http.StatusOK, views.SyncFeedback(true, "Settings saved."))
}

func (svc *Service) handleCheckCertNow(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 60*time.Second)
	defer cancel()

	cfg := svc.SnapshotConfig()
	if cfg.SSHHost == "" {
		return renderHTML(c, http.StatusOK, views.SyncFeedback(false, "SSH host not configured in Settings or .env"))
	}

	// Run the check synchronously so we can return the result
	sshC := NewSSHUnifi(cfg)
	cn, notAfter, err := sshC.RemoteCertInfo(ctx)
	if err != nil {
		svc.log.Warn("Manual check failed: %v", err)
		_ = svc.state.Update(func(st *RuntimeState) { st.LastError = err.Error() })
		return renderHTML(c, http.StatusOK, views.SyncFeedback(false, fmt.Sprintf("Failed: %v", err)))
	}

	dleft := int(time.Until(notAfter).Hours() / 24)
	svc.log.Info("Manual check OK: CN=%q, %d days left", cn, dleft)
	_ = svc.state.Update(func(st *RuntimeState) {
		st.CommonName = cn
		st.NotAfter = notAfter
		st.LastError = ""
		st.LastCheckAt = time.Now()
	})

	return renderHTML(c, http.StatusOK, views.SyncFeedback(true, fmt.Sprintf("Success! Read cert for %s", cn)))
}

func (svc *Service) handleTestCloudflare(c echo.Context) error {
	_ = c.Request().ParseForm()
	token := strings.TrimSpace(c.Request().FormValue("cloudflare_verify_token"))
	ctx, cancel := context.WithTimeout(c.Request().Context(), 45*time.Second)
	defer cancel()
	msg, err := TestCloudflareToken(ctx, token)
	if err != nil {
		svc.log.Warn("Test Cloudflare failed: %v", err)
		return renderHTML(c, http.StatusOK, views.InlineAlert(false, err.Error()))
	}
	svc.log.Info("Test Cloudflare OK: %s", msg)
	return renderHTML(c, http.StatusOK, views.InlineAlert(true, msg))
}

func (svc *Service) handleTestSSH(c echo.Context) error {
	// Re-load all relevant .env files in order
	if gp := os.Getenv("GOPROJECTS"); gp != "" {
		_ = godotenv.Overload(filepath.Join(gp, "unifi-cert-smash-deck", ".env"))
	}
	if cwd, err := os.Getwd(); err == nil {
		_ = godotenv.Overload(filepath.Join(cwd, ".env"))
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 60*time.Second)
	defer cancel()

	_ = c.Request().ParseForm()
	f := c.Request().Form
	cfg := svc.SnapshotConfig()

	// Allow testing with form values before saving
	if v := strings.TrimSpace(f.Get("ssh_host")); v != "" {
		cfg.SSHHost = v
	}
	if v := strings.TrimSpace(f.Get("ssh_port")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.SSHPort = n
		}
	}
	if v := strings.TrimSpace(f.Get("ssh_user")); v != "" {
		cfg.SSHUser = v
	}
	if v := strings.TrimSpace(f.Get("ssh_key_path")); v != "" {
		cfg.SSHKeyPath = v
	}
	if v := f.Get("ssh_known_hosts"); v != "" {
		cfg.SSHKnownHosts = strings.TrimSpace(v)
	}
	if v := strings.TrimSpace(f.Get("remote_cert_path")); v != "" {
		cfg.RemoteCertPath = v
	}

	if cfg.SSHHost == "" {
		return renderHTML(c, http.StatusOK, views.InlineAlert(false, "No SSH host provided (check Settings or .env)"))
	}

	svc.log.Info("Testing SSH to %s:%d (user: %s)...", cfg.SSHHost, cfg.SSHPort, cfg.SSHUser)
	msg, err := TestSSHUniFi(ctx, cfg)
	if err != nil {
		svc.log.Warn("Test SSH failed: %v", err)
		return renderHTML(c, http.StatusOK, views.InlineAlert(false, fmt.Sprintf("Test failed: %v", err)))
	}
	svc.log.Info("Test SSH OK: %s", msg)
	return renderHTML(c, http.StatusOK, views.InlineAlert(true, msg))
}

func (svc *Service) handleInstallUdmLe(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 120*time.Second)
	defer cancel()

	cfg := svc.SnapshotConfig()
	if cfg.SSHHost == "" {
		return renderHTML(c, http.StatusOK, views.InlineAlert(false, "SSH host not configured."))
	}

	sshC := NewSSHUnifi(cfg)
	script := BuildUdmBootstrapShell(cfg)

	// Inject the Cloudflare token if we have it in .env
	cfToken := strings.TrimSpace(os.Getenv("CLOUDFLARE_DNS_API_TOKEN"))
	if cfToken != "" {
		script = strings.ReplaceAll(script, "YOUR_CLOUDFLARE_API_TOKEN", cfToken)
	}

	svc.log.Info("Installing udm-le on remote host %s...", cfg.SSHHost)
	out, err := sshC.RunBootstrap(ctx, script)
	if err != nil {
		svc.log.Warn("Installation failed: %v", err)
		return renderHTML(c, http.StatusOK, views.InlineAlert(false, fmt.Sprintf("Installation failed: %v", err)))
	}

	svc.log.Info("Installation output:\n%s", out)
	svc.log.Info("Running initial issuance...")

	initialOut, err := sshC.RunBootstrap(ctx, "cd /data/udm-le && ./udm-le.sh initial")
	if err != nil {
		svc.log.Warn("Initial issuance failed: %v", err)
		return renderHTML(c, http.StatusOK, views.InlineAlert(false, fmt.Sprintf("Initial issuance failed: %v", err)))
	}

	svc.log.Info("Initial issuance output:\n%s", initialOut)
	svc.log.Info("udm-le installation and initial issuance completed successfully.")

	// Refresh the status
	svc.RunCheckCycle(ctx)

	return renderHTML(c, http.StatusOK, views.InlineAlert(true, "udm-le installed and certificate issued successfully!"))
}

func (svc *Service) handleWSLog(c echo.Context) error {
	ws, err := wsUpgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	defer ws.Close()

	ch := svc.Logger().Subscribe(64)
	defer svc.Logger().Unsubscribe(ch)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			return nil
		case line, ok := <-ch:
			if !ok {
				return nil
			}
			if err := ws.WriteMessage(websocket.TextMessage, []byte(line)); err != nil {
				return err
			}
		}
	}
}

func (svc *Service) statusViewModel() views.StatusVM {
	cfg := svc.SnapshotConfig()
	st := svc.State().Snapshot()
	daysLeft := 0
	if !st.NotAfter.IsZero() {
		d := int(time.Until(st.NotAfter).Hours() / 24)
		if d < 0 {
			d = 0
		}
		daysLeft = d
	}
	sshOK := strings.TrimSpace(cfg.SSHHost) != ""
	remoteKnown := !st.NotAfter.IsZero()
	healthy := st.LastError == "" && remoteKnown && daysLeft >= 7
	cn := st.CommonName
	if cn == "" {
		parts := strings.SplitN(strings.TrimSpace(cfg.CertHosts), ",", 2)
		if len(parts) > 0 {
			cn = strings.TrimSpace(parts[0])
		}
	}
	if cn == "" {
		cn = "—"
	}

	vm := views.StatusVM{
		CommonName:      cn,
		DaysLeft:        daysLeft,
		Healthy:         healthy,
		LastCheckRel:    relTime(st.LastCheckAt),
		LastError:       st.LastError,
		SSHConfigured:   sshOK,
		RemoteCertKnown: remoteKnown,
	}

	if sshOK {
		sshC := NewSSHUnifi(cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		inst, active, _ := sshC.CheckUdmLeStatus(ctx)
		vm.UdmLeInstalled = inst
		vm.UdmLeActive = active
	}

	return vm
}

func (svc *Service) settingsViewModel() views.SettingsVM {
	c := svc.SnapshotConfig()
	port := c.SSHPort
	if port == 0 {
		port = 22
	}
	days := c.CertDaysBeforeRenewal
	if days == 0 {
		days = 30
	}
	ch := c.CheckIntervalHours
	if ch == 0 {
		ch = 12
	}
	dns := strings.TrimSpace(c.DNSProvider)
	if dns == "" {
		dns = "cloudflare"
	}
	return views.SettingsVM{
		Port:                   c.Port,
		CertEmail:              c.CertEmail,
		CertHosts:              c.CertHosts,
		CertDaysBeforeRenewal:  days,
		DNSProvider:            dns,
		UdmLeEnvSnippet:        BuildUdmLeEnvSnippet(c),
		UdmBootstrapScript:     BuildUdmBootstrapShell(c),
		SSHHost:                c.SSHHost,
		SSHPort:                port,
		SSHUser:                nonEmpty(c.SSHUser, "root"),
		SSHPassword:            maskSecret(c.SSHPassword),
		SSHKeyPath:             c.SSHKeyPath,
		SSHKnownHosts:          c.SSHKnownHosts,
		RemoteCertPath:         c.RemoteCertPath,
		CheckIntervalHours:     ch,
		UniFiActiveClientsPoll: c.UniFiActiveClientsPoll,
		UniFiHost:              c.UniFiHost,
		UniFiSite:              nonEmpty(c.UniFiSite, "default"),
		UniFiAPIKey:            maskSecret(c.UniFiAPIKey),
		UniFiAPIKeyLoaded:      strings.TrimSpace(c.UniFiAPIKey) != "",
		UniFiAPIEnvVarSet:      strings.TrimSpace(os.Getenv("UNIFI_API_KEY")) != "",
		SshPasswordLoaded:      strings.TrimSpace(c.SSHPassword) != "",
		SshHostEnvSet:          strings.TrimSpace(os.Getenv("UNIFICERT_SSH_HOST")) != "",
		SshKeyEnvSet:           strings.TrimSpace(os.Getenv("UNIFICERT_SSH_KEY")) != "",
	}
}

func nonEmpty(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	}
	if d < 24*time.Hour {
		return strconv.Itoa(int(d.Hours())) + "h ago"
	}
	return strconv.Itoa(int(d.Hours()/24)) + "d ago"
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) < 8 {
		return "••••••••"
	}
	return s[:2] + strings.Repeat("•", len(s)-4) + s[len(s)-2:]
}

func isMaskedSecret(s string) bool {
	return strings.Contains(s, "•") || strings.HasPrefix(s, "••••")
}
