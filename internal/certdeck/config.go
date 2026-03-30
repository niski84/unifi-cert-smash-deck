package certdeck

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AppConfig holds persisted settings for the udm-le helper dashboard.
type AppConfig struct {
	Port string `json:"port"`

	// udm-le.env generation (saved locally; secrets belong on the UDM)
	CertEmail             string `json:"cert_email"`
	CertHosts             string `json:"cert_hosts"` // comma-separated FQDNs, same as CERT_HOSTS
	CertDaysBeforeRenewal int    `json:"cert_days_before_renewal"`
	DNSProvider           string `json:"dns_provider"` // cloudflare, route53, digitalocean, duckdns, azure, gcloud, linode, other

	// Optional: poll installed cert on the gateway (read-only SFTP)
	SSHHost        string `json:"ssh_host"`
	SSHPort        int    `json:"ssh_port"`
	SSHUser        string `json:"ssh_user"`
	SSHPassword    string `json:"-"`
	SSHKeyPath     string `json:"ssh_key_path"`
	SSHKnownHosts  string `json:"ssh_known_hosts"`
	RemoteCertPath string `json:"remote_cert_path"`

	CheckIntervalHours int `json:"check_interval_hours"` // SSH cert poll

	UniFiActiveClientsPoll bool   `json:"unifi_active_clients_poll"`
	UniFiHost              string `json:"unifi_host"`
	UniFiSite              string `json:"unifi_site"`
	UniFiAPIKey            string `json:"unifi_api_key"`
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
		Port:                   getenv("PORT", "8105"),
		CertEmail:              getenv("UNIFICERT_CERT_EMAIL", ""),
		CertHosts:              getenv("UNIFICERT_CERT_HOSTS", ""),
		SSHHost:                getenv("UNIFICERT_SSH_HOST", ""),
		SSHUser:                getenv("UNIFICERT_SSH_USER", "root"),
		SSHPassword:            getenv("UNIFICERT_SSH_PASSWORD", ""),
		SSHKeyPath:             getenv("UNIFICERT_SSH_KEY", filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")),
		SSHPort:                22,
		RemoteCertPath:         "/data/unifi-core/config/unifi-core.crt",
		CertDaysBeforeRenewal:  30,
		CheckIntervalHours:     12,
		DNSProvider:            "cloudflare",
		UniFiSite:              "default",
	}
	if p := getenv("UNIFICERT_SSH_PORT", ""); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			cfg.SSHPort = n
		}
	}
	if v := getenv("UNIFICERT_CERT_DAYS", ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.CertDaysBeforeRenewal = n
		}
	}
	if v := getenv("UNIFICERT_DNS_PROVIDER", ""); v != "" {
		cfg.DNSProvider = v
	}
	cfg.UniFiHost = getenv("UNIFI_HOST", "")
	cfg.UniFiAPIKey = getenv("UNIFI_API_KEY", "")

	log.Printf("[unificert] loading config from %s", path)
	raw, err := os.ReadFile(path)
	if err == nil {
		var stored AppConfig
		if json.Unmarshal(raw, &stored) == nil {
			log.Printf("[unificert]   merged settings from json file")
			mergeAppConfig(&cfg, stored)
		}
	}

	// Environment variable overrides (higher priority than file)
	if v := strings.TrimSpace(os.Getenv("PORT")); v != "" {
		cfg.Port = v
	}
	if v := strings.TrimSpace(os.Getenv("UNIFICERT_CERT_EMAIL")); v != "" {
		cfg.CertEmail = v
	}
	if v := strings.TrimSpace(os.Getenv("UNIFICERT_CERT_HOSTS")); v != "" {
		cfg.CertHosts = v
	}
	if v := strings.TrimSpace(os.Getenv("UNIFICERT_SSH_HOST")); v != "" {
		cfg.SSHHost = v
		log.Printf("[unificert]   env override: SSHHost=%s", v)
	}
	if v := strings.TrimSpace(os.Getenv("UNIFICERT_SSH_USER")); v != "" {
		cfg.SSHUser = v
	}
	if v := strings.TrimSpace(os.Getenv("UNIFICERT_SSH_PASSWORD")); v != "" {
		cfg.SSHPassword = v
	}
	if v := strings.TrimSpace(os.Getenv("UNIFICERT_SSH_KEY")); v != "" {
		cfg.SSHKeyPath = v
	}
	if v := strings.TrimSpace(os.Getenv("UNIFICERT_SSH_KNOWN_HOSTS")); v != "" {
		cfg.SSHKnownHosts = v
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
	if src.CertEmail != "" {
		dst.CertEmail = src.CertEmail
	}
	if src.CertHosts != "" {
		dst.CertHosts = src.CertHosts
	}
	if src.CertDaysBeforeRenewal > 0 {
		dst.CertDaysBeforeRenewal = src.CertDaysBeforeRenewal
	}
	if strings.TrimSpace(src.DNSProvider) != "" {
		dst.DNSProvider = src.DNSProvider
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
	// SSHPassword is not persisted to JSON (json:"-"); kept in-memory from .env only.
	if src.SSHKeyPath != "" {
		dst.SSHKeyPath = src.SSHKeyPath
	}
	if src.SSHKnownHosts != "" {
		dst.SSHKnownHosts = src.SSHKnownHosts
	}
	if src.RemoteCertPath != "" {
		dst.RemoteCertPath = src.RemoteCertPath
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
