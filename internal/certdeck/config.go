package certdeck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AppConfig holds persisted settings for the UniFi certificate controller.
type AppConfig struct {
	Port   string `json:"port"`
	Domain string `json:"domain"`

	ACMEEmail      string `json:"acme_email"`
	ACMEUseStaging bool   `json:"acme_use_staging"`

	CloudflareAPIToken string `json:"cloudflare_api_token"`

	SSHHost       string `json:"ssh_host"`
	SSHPort       int    `json:"ssh_port"`
	SSHUser       string `json:"ssh_user"`
	SSHKeyPath    string `json:"ssh_key_path"`
	SSHKnownHosts string `json:"ssh_known_hosts"` // optional path; empty + no fingerprint uses insecure callback (LAN only)

	RemoteCertPath string `json:"remote_cert_path"`
	RemoteKeyPath  string `json:"remote_key_path"`

	RenewWithinDays      int `json:"renew_within_days"`
	CheckIntervalHours   int `json:"check_interval_hours"`
	UniFiActiveClientsPoll bool `json:"unifi_active_clients_poll"` // optional: hit UniFi API for dashboard line
	UniFiHost            string `json:"unifi_host"`
	UniFiSite            string `json:"unifi_site"`
	UniFiAPIKey          string `json:"unifi_api_key"`
}

// DataDir returns persistent data directory (default ./data).
func DataDir() string {
	if d := strings.TrimSpace(os.Getenv("DATA_DIR")); d != "" {
		return d
	}
	return "data"
}

func DefaultSettingsPath() string {
	return filepath.Join(DataDir(), "unificert-settings.json")
}

func LoadAppConfig(path string) AppConfig {
	cfg := AppConfig{
		Port:             getenv("PORT", "8105"),
		Domain:           getenv("UNIFICERT_DOMAIN", ""),
		ACMEEmail:        getenv("UNIFICERT_ACME_EMAIL", ""),
		SSHHost:          getenv("UNIFICERT_SSH_HOST", ""),
		SSHUser:          getenv("UNIFICERT_SSH_USER", "root"),
		SSHKeyPath:       getenv("UNIFICERT_SSH_KEY", filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")),
		SSHPort:          22,
		RemoteCertPath:   "/data/unifi-core/config/unifi-core.crt",
		RemoteKeyPath:    "/data/unifi-core/config/unifi-core.key",
		RenewWithinDays:  30,
		CheckIntervalHours: 24,
		UniFiSite:        "default",
	}
	if p := getenv("UNIFICERT_SSH_PORT", ""); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			cfg.SSHPort = n
		}
	}
	cfg.CloudflareAPIToken = getenv("CLOUDFLARE_DNS_API_TOKEN", "")
	cfg.UniFiHost = getenv("UNIFI_HOST", "")
	cfg.UniFiAPIKey = getenv("UNIFI_API_KEY", "")

	raw, err := os.ReadFile(path)
	if err == nil {
		var stored AppConfig
		if json.Unmarshal(raw, &stored) == nil {
			mergeAppConfig(&cfg, stored)
		}
	}

	if v := strings.TrimSpace(os.Getenv("PORT")); v != "" {
		cfg.Port = v
	}
	if v := strings.TrimSpace(os.Getenv("UNIFICERT_DOMAIN")); v != "" {
		cfg.Domain = v
	}
	if v := strings.TrimSpace(os.Getenv("UNIFICERT_ACME_EMAIL")); v != "" {
		cfg.ACMEEmail = v
	}
	if v := strings.TrimSpace(os.Getenv("UNIFICERT_SSH_HOST")); v != "" {
		cfg.SSHHost = v
	}
	if v := strings.TrimSpace(os.Getenv("UNIFICERT_SSH_USER")); v != "" {
		cfg.SSHUser = v
	}
	if v := strings.TrimSpace(os.Getenv("UNIFICERT_SSH_KEY")); v != "" {
		cfg.SSHKeyPath = v
	}
	if v := strings.TrimSpace(os.Getenv("CLOUDFLARE_DNS_API_TOKEN")); v != "" && v != "your-cloudflare-token" {
		cfg.CloudflareAPIToken = v
	}
	if v := strings.TrimSpace(os.Getenv("UNIFI_HOST")); v != "" && v != "https://192.168.1.1" {
		cfg.UniFiHost = v
	}
	if v := strings.TrimSpace(os.Getenv("UNIFI_API_KEY")); v != "" && v != "your-api-key-here" {
		cfg.UniFiAPIKey = v
	}
	return cfg
}

func mergeAppConfig(dst *AppConfig, src AppConfig) {
	if src.Port != "" {
		dst.Port = src.Port
	}
	if src.Domain != "" {
		dst.Domain = src.Domain
	}
	if src.ACMEEmail != "" {
		dst.ACMEEmail = src.ACMEEmail
	}
	dst.ACMEUseStaging = src.ACMEUseStaging
	if src.CloudflareAPIToken != "" {
		dst.CloudflareAPIToken = src.CloudflareAPIToken
	}
	if src.SSHHost != "" {
		dst.SSHHost = src.SSHHost
	}
	if src.SSHPort != 0 {
		dst.SSHPort = src.SSHPort
	}
	if src.SSHUser != "" {
		dst.SSHUser = src.SSHUser
	}
	if src.SSHKeyPath != "" {
		dst.SSHKeyPath = src.SSHKeyPath
	}
	if src.SSHKnownHosts != "" {
		dst.SSHKnownHosts = src.SSHKnownHosts
	}
	if src.RemoteCertPath != "" {
		dst.RemoteCertPath = src.RemoteCertPath
	}
	if src.RemoteKeyPath != "" {
		dst.RemoteKeyPath = src.RemoteKeyPath
	}
	if src.RenewWithinDays > 0 {
		dst.RenewWithinDays = src.RenewWithinDays
	}
	if src.CheckIntervalHours > 0 {
		dst.CheckIntervalHours = src.CheckIntervalHours
	}
	dst.UniFiActiveClientsPoll = src.UniFiActiveClientsPoll
	if src.UniFiHost != "" {
		dst.UniFiHost = src.UniFiHost
	}
	if src.UniFiSite != "" {
		dst.UniFiSite = src.UniFiSite
	}
	if src.UniFiAPIKey != "" {
		dst.UniFiAPIKey = src.UniFiAPIKey
	}
}

func SaveAppConfig(path string, cfg AppConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
