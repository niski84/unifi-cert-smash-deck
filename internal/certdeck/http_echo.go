package certdeck

import (
	"bytes"
	"context"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/gorilla/websocket"
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
			"service":                     "unifi-cert-smash-deck",
			"data_dir":                    DataDir(),
			"cloudflare_dns_token_loaded": strings.TrimSpace(cfg.CloudflareAPIToken) != "",
			"domain_configured":           strings.TrimSpace(cfg.Domain) != "",
			"ssh_host_configured":         strings.TrimSpace(cfg.SSHHost) != "",
			"unifi_api_key_loaded":        strings.TrimSpace(cfg.UniFiAPIKey) != "",
		})
	})

	e.POST("/api/settings", svc.handleSettingsForm)
	e.POST("/api/sync", svc.handleSyncNow)

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
	if v := strings.TrimSpace(f.Get("domain")); v != "" {
		cur.Domain = v
	}
	if v := strings.TrimSpace(f.Get("acme_email")); v != "" {
		cur.ACMEEmail = v
	}
	cur.ACMEUseStaging = f.Get("acme_use_staging") == "true"
	if v := strings.TrimSpace(f.Get("cloudflare_api_token")); v != "" && !isMaskedSecret(v) {
		cur.CloudflareAPIToken = v
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
	if v := strings.TrimSpace(f.Get("ssh_key_path")); v != "" {
		cur.SSHKeyPath = v
	}
	cur.SSHKnownHosts = strings.TrimSpace(f.Get("ssh_known_hosts"))
	if v := strings.TrimSpace(f.Get("remote_cert_path")); v != "" {
		cur.RemoteCertPath = v
	}
	if v := strings.TrimSpace(f.Get("remote_key_path")); v != "" {
		cur.RemoteKeyPath = v
	}
	if v := strings.TrimSpace(f.Get("renew_within_days")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cur.RenewWithinDays = n
		}
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

	if err := SaveAppConfig(svc.SettingsPath(), cur); err != nil {
		return renderHTML(c, http.StatusInternalServerError, views.SyncFeedback(false, "Save failed: "+err.Error()))
	}
	svc.ReplaceConfig(cur)
	return renderHTML(c, http.StatusOK, views.SyncFeedback(true, "Settings saved."))
}

func (svc *Service) handleSyncNow(c echo.Context) error {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		svc.RunCycle(ctx, true)
	}()
	return renderHTML(c, http.StatusOK, views.SyncFeedback(true, "Sync started — watch the log stream."))
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
	healthy := !st.Renewing && st.LastError == ""
	if !st.NotAfter.IsZero() && daysLeft < 7 {
		healthy = false
	}
	domainOK := cfg.Domain != "" && cfg.SSHHost != ""
	return views.StatusVM{
		CommonName:       nonEmpty(st.CommonName, cfg.Domain),
		DaysLeft:         daysLeft,
		Renewing:         st.Renewing,
		Healthy:          healthy,
		LastSyncRel:      relTime(st.LastSyncAt),
		LastCheckRel:     relTime(st.LastCheckAt),
		LastError:        st.LastError,
		DomainConfigured: domainOK,
	}
}

func (svc *Service) settingsViewModel() views.SettingsVM {
	c := svc.SnapshotConfig()
	port := c.SSHPort
	if port == 0 {
		port = 22
	}
	rw := c.RenewWithinDays
	if rw == 0 {
		rw = 30
	}
	ch := c.CheckIntervalHours
	if ch == 0 {
		ch = 24
	}
	return views.SettingsVM{
		Port:                   c.Port,
		Domain:                 c.Domain,
		ACMEEmail:              c.ACMEEmail,
		ACMEUseStaging:         c.ACMEUseStaging,
		CloudflareAPIToken:     maskSecret(c.CloudflareAPIToken),
		CloudflareTokenLoaded:  strings.TrimSpace(c.CloudflareAPIToken) != "",
		CloudflareEnvVarSet:    strings.TrimSpace(os.Getenv("CLOUDFLARE_DNS_API_TOKEN")) != "",
		SSHHost:                c.SSHHost,
		SSHPort:                port,
		SSHUser:                nonEmpty(c.SSHUser, "root"),
		SSHKeyPath:             c.SSHKeyPath,
		SSHKnownHosts:          c.SSHKnownHosts,
		RemoteCertPath:         c.RemoteCertPath,
		RemoteKeyPath:          c.RemoteKeyPath,
		RenewWithinDays:        rw,
		CheckIntervalHours:     ch,
		UniFiActiveClientsPoll: c.UniFiActiveClientsPoll,
		UniFiHost:              c.UniFiHost,
		UniFiSite:              nonEmpty(c.UniFiSite, "default"),
		UniFiAPIKey:            maskSecret(c.UniFiAPIKey),
		UniFiAPIKeyLoaded:      strings.TrimSpace(c.UniFiAPIKey) != "",
		UniFiAPIEnvVarSet:      strings.TrimSpace(os.Getenv("UNIFI_API_KEY")) != "",
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
