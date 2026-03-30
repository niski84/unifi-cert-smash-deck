package certdeck

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niski84/unifi-cert-smash-deck/internal/sshkey"
	"github.com/niski84/unifi-cert-smash-deck/internal/wizard"
)

// ---------------------------------------------------------------------------
// Check builder helpers
// ---------------------------------------------------------------------------

func pendingCheck(id, label string, required bool) wizard.Check {
	return wizard.Check{
		ID:       id,
		Label:    label,
		Status:   wizard.StatusPending,
		Required: required,
	}
}

func passCheck(checks []wizard.Check, id, detail string) []wizard.Check {
	for i, c := range checks {
		if c.ID == id {
			checks[i].Status = wizard.StatusPassed
			checks[i].Detail = detail
			return checks
		}
	}
	return checks
}

func failCheck(checks []wizard.Check, id, detail string) []wizard.Check {
	for i, c := range checks {
		if c.ID == id {
			checks[i].Status = wizard.StatusFailed
			checks[i].Detail = detail
			return checks
		}
	}
	return checks
}

func warnCheck(checks []wizard.Check, id, detail string) []wizard.Check {
	for i, c := range checks {
		if c.ID == id {
			checks[i].Status = wizard.StatusWarning
			checks[i].Detail = detail
			return checks
		}
	}
	return checks
}

// skipFrom marks all pending checks from the first occurrence of fromID onward as skipped.
func skipFrom(checks []wizard.Check, fromID string) []wizard.Check {
	skipping := false
	for i, c := range checks {
		if c.ID == fromID {
			skipping = true
		}
		if skipping && c.Status == wizard.StatusPending {
			checks[i].Status = wizard.StatusSkipped
		}
	}
	return checks
}

// allRequired returns true when every Required check is passed or warned.
func allRequired(checks []wizard.Check) bool {
	for _, c := range checks {
		if !c.Required {
			continue
		}
		if c.Status != wizard.StatusPassed && c.Status != wizard.StatusWarning {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Step 0: Discover
// ---------------------------------------------------------------------------

// WizDiscover validates connectivity to the UDM host.
func WizDiscover(ctx context.Context, host string, port int) []wizard.Check {
	checks := []wizard.Check{
		pendingCheck("host_valid", "Host name is valid", true),
		pendingCheck("tcp_open", "TCP port is reachable", true),
		pendingCheck("web_present", "HTTPS management UI present (port 443)", false),
	}

	h := strings.TrimSpace(host)
	if h == "" || strings.ContainsAny(h, " /\\") {
		checks = failCheck(checks, "host_valid", "host must be a non-empty hostname or IP address with no spaces or slashes")
		checks = skipFrom(checks, "tcp_open")
		return checks
	}
	checks = passCheck(checks, "host_valid", h)

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addr := net.JoinHostPort(h, fmt.Sprintf("%d", port))
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		errStr := err.Error()
		detail := fmt.Sprintf("cannot connect to %s: %s", addr, errStr)
		if strings.Contains(errStr, "refused") {
			detail = fmt.Sprintf("port %d is actively refused on %s — check SSH is enabled", port, h)
		} else if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline") {
			detail = fmt.Sprintf("timed out connecting to %s — check host is reachable on the network", addr)
		}
		checks = failCheck(checks, "tcp_open", detail)
		checks = skipFrom(checks, "web_present")
		return checks
	}
	conn.Close()
	checks = passCheck(checks, "tcp_open", fmt.Sprintf("%s is reachable", addr))

	tlsClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	httpsURL := fmt.Sprintf("https://%s", h)
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, httpsURL, nil)
	if reqErr == nil {
		resp, httpErr := tlsClient.Do(req)
		if httpErr == nil {
			resp.Body.Close()
			checks = passCheck(checks, "web_present", fmt.Sprintf("HTTPS responded with %d", resp.StatusCode))
		} else {
			checks = warnCheck(checks, "web_present", fmt.Sprintf("HTTPS not reachable: %s", httpErr))
		}
	} else {
		checks = warnCheck(checks, "web_present", fmt.Sprintf("could not build HTTPS request: %s", reqErr))
	}

	return checks
}

// ---------------------------------------------------------------------------
// Step 1: SSH + key setup
// ---------------------------------------------------------------------------

// SSHConnResult holds results gathered during the SSH step.
type SSHConnResult struct {
	SSHKeyPath     string
	KnownHostsPath string
	UDMOSVersion   string
	UDMLeState     wizard.UDMLeState
	CertCN         string
	CertDays       int
	CertSelfSigned bool
}

// WizSSH performs SSH authentication, key deployment, host key scanning, and UDM state detection.
func WizSSH(ctx context.Context, host string, port int, user, password, existingKeyPath, dataDir string) ([]wizard.Check, SSHConnResult) {
	checks := []wizard.Check{
		pendingCheck("ssh_auth", "SSH authentication succeeds", true),
		pendingCheck("key_ready", "SSH key pair is ready", true),
		pendingCheck("key_deployed", "Public key deployed to UDM", true),
		pendingCheck("known_hosts", "Host key saved to known_hosts", true),
		pendingCheck("udm_internet", "UDM can reach the internet", true),
		pendingCheck("udm_le_status", "udm-le installation status detected", false),
		pendingCheck("cert_status", "Current certificate read", false),
	}

	var result SSHConnResult

	// Step 1: password-based auth to verify connectivity.
	pwCfg := AppConfig{
		SSHHost:     host,
		SSHPort:     port,
		SSHUser:     user,
		SSHPassword: password,
	}
	pwSSH := NewSSHUnifi(pwCfg)
	testClient, dialErr := pwSSH.dial(ctx)
	if dialErr != nil {
		checks = failCheck(checks, "ssh_auth", fmt.Sprintf("SSH auth failed: %s", dialErr))
		checks = skipFrom(checks, "key_ready")
		return checks, result
	}
	testClient.Close()
	checks = passCheck(checks, "ssh_auth", fmt.Sprintf("authenticated as %s@%s:%d", user, host, port))

	// Step 2: ensure key pair exists.
	keyPath, generated, err := sshkey.Ensure(existingKeyPath)
	if err != nil {
		checks = failCheck(checks, "key_ready", fmt.Sprintf("key generation failed: %s", err))
		checks = skipFrom(checks, "key_deployed")
		return checks, result
	}
	result.SSHKeyPath = keyPath
	if generated {
		checks = passCheck(checks, "key_ready", fmt.Sprintf("generated new key at %s", keyPath))
	} else {
		checks = passCheck(checks, "key_ready", fmt.Sprintf("using existing key at %s", keyPath))
	}

	// Step 3: deploy public key via password-based connection.
	pubLine, err := sshkey.PublicKeyLine(keyPath)
	if err != nil {
		checks = failCheck(checks, "key_deployed", fmt.Sprintf("cannot read public key: %s", err))
		checks = skipFrom(checks, "known_hosts")
		return checks, result
	}
	if err := pwSSH.DeployPublicKey(ctx, pubLine); err != nil {
		checks = failCheck(checks, "key_deployed", fmt.Sprintf("SFTP write of authorized_keys failed: %s", err))
		checks = skipFrom(checks, "known_hosts")
		return checks, result
	}
	checks = passCheck(checks, "key_deployed", "public key appended to /root/.ssh/authorized_keys")

	// Step 4: scan and save host keys.
	knownHostsPath := filepath.Join(dataDir, "known_hosts_unifi")
	if err := ScanAndSaveKnownHosts(host, port, knownHostsPath); err != nil {
		checks = failCheck(checks, "known_hosts", fmt.Sprintf("host key scan failed: %s", err))
		checks = skipFrom(checks, "udm_internet")
		return checks, result
	}
	result.KnownHostsPath = knownHostsPath
	checks = passCheck(checks, "known_hosts", fmt.Sprintf("host keys written to %s", knownHostsPath))

	// Build key-auth config for subsequent operations.
	keyCfg := AppConfig{
		SSHHost:       host,
		SSHPort:       port,
		SSHUser:       user,
		SSHKeyPath:    keyPath,
		SSHKnownHosts: knownHostsPath,
	}
	keySSH := NewSSHUnifi(keyCfg)

	// Verify key auth works.
	verifyClient, verifyErr := keySSH.dial(ctx)
	if verifyErr != nil {
		checks = failCheck(checks, "known_hosts", fmt.Sprintf("key auth verification failed: %s", verifyErr))
		checks = skipFrom(checks, "udm_internet")
		return checks, result
	}
	verifyClient.Close()

	// Step 5: check internet.
	cfOK, leOK, err := keySSH.CheckInternet(ctx)
	if err != nil || (!cfOK && !leOK) {
		detail := "no internet access from UDM"
		if err != nil {
			detail = fmt.Sprintf("internet check error: %s", err)
		}
		checks = failCheck(checks, "udm_internet", detail)
	} else if cfOK && leOK {
		checks = passCheck(checks, "udm_internet", "Cloudflare and Let's Encrypt reachable")
	} else if cfOK {
		checks = warnCheck(checks, "udm_internet", "Cloudflare reachable but Let's Encrypt unreachable")
	} else {
		checks = warnCheck(checks, "udm_internet", "Let's Encrypt reachable but Cloudflare unreachable")
	}

	// Step 6: detect udm-le state.
	installedOut, runErr := keySSH.RunCommand(ctx, `[ -f /data/udm-le/udm-le.sh ] && echo INSTALLED || echo MISSING`)
	timerOut, _ := keySSH.RunCommand(ctx, `systemctl is-active udm-le.timer 2>/dev/null || echo inactive`)
	if runErr != nil {
		checks = warnCheck(checks, "udm_le_status", fmt.Sprintf("could not detect udm-le: %s", runErr))
		result.UDMLeState = wizard.UDMLeUnknown
	} else {
		installed := strings.Contains(installedOut, "INSTALLED")
		timerActive := strings.Contains(timerOut, "active") || strings.Contains(timerOut, "waiting")
		switch {
		case !installed:
			result.UDMLeState = wizard.UDMLeFresh
			checks = passCheck(checks, "udm_le_status", "udm-le not installed (fresh)")
		case installed && timerActive:
			result.UDMLeState = wizard.UDMLeHealthy
			checks = passCheck(checks, "udm_le_status", "udm-le installed, renewal timer active")
		case installed && !timerActive:
			result.UDMLeState = wizard.UDMLeDegraded
			checks = warnCheck(checks, "udm_le_status", "udm-le installed but renewal timer is not active")
		default:
			result.UDMLeState = wizard.UDMLeUnknown
			checks = warnCheck(checks, "udm_le_status", "udm-le state is unclear")
		}
	}

	// Step 7: read current remote cert.
	cn, notAfter, certErr := keySSH.RemoteCertInfo(ctx)
	if certErr != nil {
		checks = warnCheck(checks, "cert_status", fmt.Sprintf("cannot read remote cert: %s", certErr))
	} else {
		days := int(time.Until(notAfter).Hours() / 24)
		result.CertCN = cn
		result.CertDays = days

		selfSigned := false
		pemOut, pemErr := keySSH.RunCommand(ctx, `cat /data/unifi-core/config/unifi-core.crt 2>/dev/null`)
		if pemErr == nil {
			selfSigned = isSelfSignedPEM([]byte(pemOut))
		}
		result.CertSelfSigned = selfSigned

		detail := fmt.Sprintf("CN=%q, expires in %d days", cn, days)
		if selfSigned {
			detail += " (self-signed)"
		}
		checks = passCheck(checks, "cert_status", detail)
	}

	return checks, result
}

// ---------------------------------------------------------------------------
// Step 2: Domain & DNS
// ---------------------------------------------------------------------------

// DomainResult holds results gathered during the domain step.
type DomainResult struct {
	DNSZone string
}

// WizDomain validates domain/email/token configuration and optionally resolves DNS.
func WizDomain(ctx context.Context, hosts, email, provider, token string) ([]wizard.Check, DomainResult) {
	checks := []wizard.Check{
		pendingCheck("hosts_valid", "Certificate hosts are valid FQDNs", true),
		pendingCheck("email_valid", "Contact email is valid", true),
		pendingCheck("token_provided", "DNS provider token is provided", true),
		pendingCheck("token_valid", "DNS provider token is accepted", true),
		pendingCheck("dns_zone", "DNS zone detected", false),
		pendingCheck("host_resolves", "First host resolves in DNS", false),
	}

	var result DomainResult

	h := strings.TrimSpace(hosts)
	if h == "" {
		checks = failCheck(checks, "hosts_valid", "at least one hostname is required")
		checks = skipFrom(checks, "email_valid")
		return checks, result
	}
	hostList := strings.Split(h, ",")
	for i, hn := range hostList {
		hostList[i] = strings.TrimSpace(hn)
	}
	for _, hn := range hostList {
		if hn == "" || strings.ContainsAny(hn, " \\@!#$%^&*()+") {
			checks = failCheck(checks, "hosts_valid", fmt.Sprintf("invalid hostname: %q", hn))
			checks = skipFrom(checks, "email_valid")
			return checks, result
		}
	}
	checks = passCheck(checks, "hosts_valid", fmt.Sprintf("%d host(s): %s", len(hostList), strings.Join(hostList, ", ")))

	em := strings.TrimSpace(email)
	if em == "" || !strings.Contains(em, "@") {
		checks = failCheck(checks, "email_valid", "must be a valid email address (contains @)")
		checks = skipFrom(checks, "token_provided")
		return checks, result
	}
	checks = passCheck(checks, "email_valid", em)

	tk := strings.TrimSpace(token)
	if tk == "" {
		checks = failCheck(checks, "token_provided", "DNS provider token is required")
		checks = skipFrom(checks, "token_valid")
		return checks, result
	}
	checks = passCheck(checks, "token_provided", "token provided (not stored)")

	prov := strings.ToLower(strings.TrimSpace(provider))
	switch prov {
	case "cloudflare":
		msg, cfErr := TestCloudflareToken(ctx, tk)
		if cfErr != nil {
			checks = failCheck(checks, "token_valid", fmt.Sprintf("Cloudflare API rejected token: %s", cfErr))
		} else {
			checks = passCheck(checks, "token_valid", msg)
			// Try to extract zone from the message.
			if idx := strings.Index(msg, "zones visible: "); idx != -1 {
				zonesPart := msg[idx+len("zones visible: "):]
				// strip trailing paren or period if present
				zonesPart = strings.TrimRight(zonesPart, ").")
				firstZone := strings.TrimSpace(strings.SplitN(zonesPart, ",", 2)[0])
				result.DNSZone = firstZone
				if result.DNSZone != "" {
					checks = passCheck(checks, "dns_zone", result.DNSZone)
				}
			}
		}
	default:
		checks = passCheck(checks, "token_valid", fmt.Sprintf("token accepted (provider %q not validated via API)", prov))
	}

	if result.DNSZone == "" {
		checks = warnCheck(checks, "dns_zone", "zone auto-detection only supported for Cloudflare")
	}

	addrs, dnsErr := net.DefaultResolver.LookupHost(ctx, hostList[0])
	if dnsErr != nil {
		checks = warnCheck(checks, "host_resolves", fmt.Sprintf("%s does not resolve yet: %s", hostList[0], dnsErr))
	} else {
		checks = passCheck(checks, "host_resolves", fmt.Sprintf("%s → %s", hostList[0], strings.Join(addrs, ", ")))
	}

	return checks, result
}

// ---------------------------------------------------------------------------
// Step 3: Preflight
// ---------------------------------------------------------------------------

// PreflightResult describes the planned install action.
type PreflightResult struct {
	Action      wizard.InstallAction
	ActionLabel string
}

// WizPreflight confirms internet access from the UDM and determines the install action.
func WizPreflight(ctx context.Context, cfg AppConfig, udmLeState wizard.UDMLeState, certHosts string) ([]wizard.Check, PreflightResult) {
	checks := []wizard.Check{
		pendingCheck("udm_cf_internet", "UDM can reach Cloudflare (1.1.1.1)", true),
		pendingCheck("udm_le_internet", "UDM can reach Let's Encrypt (acme-v02.api.letsencrypt.org)", true),
		pendingCheck("state_detected", "UDM certificate/install state assessed", false),
		pendingCheck("action_ready", "Install action determined", false),
	}

	var result PreflightResult

	s := NewSSHUnifi(cfg)
	cfOK, leOK, err := s.CheckInternet(ctx)
	if err != nil {
		checks = failCheck(checks, "udm_cf_internet", fmt.Sprintf("internet check error: %s", err))
		checks = failCheck(checks, "udm_le_internet", "skipped due to error")
		checks = skipFrom(checks, "state_detected")
		return checks, result
	}

	if cfOK {
		checks = passCheck(checks, "udm_cf_internet", "1.1.1.1 reachable")
	} else {
		checks = failCheck(checks, "udm_cf_internet", "cannot reach 1.1.1.1 from UDM")
	}
	if leOK {
		checks = passCheck(checks, "udm_le_internet", "acme-v02.api.letsencrypt.org reachable")
	} else {
		checks = failCheck(checks, "udm_le_internet", "cannot reach acme-v02.api.letsencrypt.org from UDM")
	}

	checks = passCheck(checks, "state_detected", fmt.Sprintf("udm-le state: %s", udmLeState))

	switch udmLeState {
	case wizard.UDMLeFresh:
		result.Action = wizard.ActionInstall
		result.ActionLabel = "Install udm-le and issue certificate"
	case wizard.UDMLeHealthy:
		result.Action = wizard.ActionRenew
		result.ActionLabel = "Force renew existing certificate"
	case wizard.UDMLeDegraded:
		result.Action = wizard.ActionRepair
		result.ActionLabel = "Repair: restart renewal timer"
	case wizard.UDMLeBroken:
		result.Action = wizard.ActionRenew
		result.ActionLabel = "Re-issue certificate (previous cert expired/missing)"
	case wizard.UDMLeMissing:
		result.Action = wizard.ActionReinstall
		result.ActionLabel = "Reinstall udm-le (firmware may have wiped /data)"
	default:
		result.Action = wizard.ActionInstall
		result.ActionLabel = "Install udm-le and issue certificate"
	}

	checks = passCheck(checks, "action_ready", result.ActionLabel)
	return checks, result
}

// ---------------------------------------------------------------------------
// Step 4: Install
// ---------------------------------------------------------------------------

// WizInstall runs the udm-le installation/repair on the remote UDM.
// The caller is responsible for running this in a goroutine if async behaviour is needed.
func WizInstall(ctx context.Context, cfg AppConfig, token string, action wizard.InstallAction, staging bool, logCh chan<- string) error {
	sendLog := func(line string) {
		select {
		case logCh <- line:
		default:
		}
	}

	sendLog(fmt.Sprintf("Starting install: %s", action))

	s := NewSSHUnifi(cfg)

	// Backup existing udm-le.env (except for pure repair).
	if action != wizard.ActionRepair {
		var backupDir string
		if cfg.SSHKnownHosts != "" {
			backupDir = filepath.Join(filepath.Dir(cfg.SSHKnownHosts), "backups")
		} else {
			backupDir = "data/backups"
		}
		if mkErr := os.MkdirAll(backupDir, 0o755); mkErr != nil {
			sendLog(fmt.Sprintf("WARNING: could not create backup dir: %s", mkErr))
		} else {
			existing, readErr := s.ReadRemoteFile(ctx, "/data/udm-le/udm-le.env")
			if readErr == nil && len(existing) > 0 {
				ts := time.Now().Format("20060102_150405")
				backupPath := filepath.Join(backupDir, fmt.Sprintf("udm-le.env.%s.bak", ts))
				if writeErr := os.WriteFile(backupPath, existing, 0o600); writeErr != nil {
					sendLog(fmt.Sprintf("WARNING: backup write failed: %s", writeErr))
				} else {
					sendLog(fmt.Sprintf("Backed up /data/udm-le/udm-le.env to %s", backupPath))
				}
			}
		}
	}

	// Download udm-le for install / reinstall.
	if action == wizard.ActionInstall || action == wizard.ActionReinstall {
		sendLog("Downloading udm-le from GitHub...")
		downloadScript := `mkdir -p /data/udm-le && cd /data/udm-le && ` +
			`if command -v curl >/dev/null 2>&1; then ` +
			`curl -fsSL https://raw.githubusercontent.com/kchristensen/udm-le/master/udm-le.sh -o udm-le.sh && chmod +x udm-le.sh; ` +
			`elif command -v wget >/dev/null 2>&1; then ` +
			`wget -qO udm-le.sh https://raw.githubusercontent.com/kchristensen/udm-le/master/udm-le.sh && chmod +x udm-le.sh; ` +
			`else echo "ERROR: neither curl nor wget found"; exit 1; fi`
		if err := s.ExecStream(ctx, downloadScript, logCh); err != nil {
			return fmt.Errorf("download udm-le: %w", err)
		}
	}

	// Write udm-le.env with the real token.
	envContent := BuildUdmLeEnvWithToken(cfg, token)
	if staging {
		envContent += "\n# Staging mode\nACME_SERVER=\"https://acme-staging-v02.api.letsencrypt.org/directory\"\n"
	}
	sendLog("Writing /data/udm-le/udm-le.env...")
	if err := s.WriteRemoteFile(ctx, "/data/udm-le/udm-le.env", []byte(envContent), 0o644); err != nil {
		return fmt.Errorf("write udm-le.env: %w", err)
	}
	sendLog("udm-le.env written successfully")

	// Run install or fix timer.
	switch action {
	case wizard.ActionRepair:
		sendLog("Restarting udm-le timer...")
		if err := s.ExecStream(ctx, "systemctl restart udm-le.timer 2>&1", logCh); err != nil {
			return fmt.Errorf("restart timer: %w", err)
		}
	default:
		sendLog("Running udm-le.sh initial...")
		if err := s.ExecStream(ctx, "/data/udm-le/udm-le.sh initial 2>&1", logCh); err != nil {
			return fmt.Errorf("run udm-le.sh initial: %w", err)
		}
	}

	timerOut, timerErr := s.RunCommand(ctx, "systemctl is-active udm-le.timer 2>/dev/null || echo inactive")
	if timerErr != nil {
		sendLog(fmt.Sprintf("WARNING: could not check timer status: %s", timerErr))
	} else {
		sendLog(fmt.Sprintf("Timer status: %s", strings.TrimSpace(timerOut)))
	}

	sendLog("Install complete.")
	return nil
}

// ---------------------------------------------------------------------------
// Step 5: Verify
// ---------------------------------------------------------------------------

// WizVerify confirms the issued certificate meets all requirements.
func WizVerify(ctx context.Context, cfg AppConfig, certHosts string) []wizard.Check {
	checks := []wizard.Check{
		pendingCheck("cert_read", "Remote certificate is readable", true),
		pendingCheck("cn_match", "Certificate CN matches requested host", true),
		pendingCheck("le_issuer", "Certificate issued by Let's Encrypt", true),
		pendingCheck("days_remaining", "Certificate valid for > 60 days", true),
		pendingCheck("timer_active", "Renewal timer is active", true),
	}

	s := NewSSHUnifi(cfg)

	cn, notAfter, err := s.RemoteCertInfo(ctx)
	if err != nil {
		checks = failCheck(checks, "cert_read", fmt.Sprintf("cannot read remote cert: %s", err))
		checks = skipFrom(checks, "cn_match")
		return checks
	}
	checks = passCheck(checks, "cert_read", fmt.Sprintf("CN=%q, expires %s", cn, notAfter.UTC().Format(time.RFC3339)))

	firstHost := strings.TrimSpace(strings.SplitN(certHosts, ",", 2)[0])
	if strings.EqualFold(cn, firstHost) {
		checks = passCheck(checks, "cn_match", fmt.Sprintf("CN=%q matches %s", cn, firstHost))
	} else {
		checks = failCheck(checks, "cn_match", fmt.Sprintf("CN=%q does not match expected %q", cn, firstHost))
	}

	pemOut, pemErr := s.RunCommand(ctx, "cat /data/unifi-core/config/unifi-core.crt 2>/dev/null")
	if pemErr != nil {
		checks = warnCheck(checks, "le_issuer", fmt.Sprintf("could not read cert PEM: %s", pemErr))
	} else {
		if isLEIssued([]byte(pemOut)) {
			checks = passCheck(checks, "le_issuer", "issuer contains \"Let's Encrypt\"")
		} else {
			checks = failCheck(checks, "le_issuer", "certificate issuer does not contain \"Let's Encrypt\"")
		}
	}

	days := int(time.Until(notAfter).Hours() / 24)
	if days > 60 {
		checks = passCheck(checks, "days_remaining", fmt.Sprintf("%d days remaining", days))
	} else {
		checks = failCheck(checks, "days_remaining", fmt.Sprintf("only %d days remaining (need > 60)", days))
	}

	timerOut, timerErr := s.RunCommand(ctx, "systemctl is-active udm-le.timer 2>/dev/null || echo inactive")
	if timerErr != nil {
		checks = failCheck(checks, "timer_active", fmt.Sprintf("cannot check timer: %s", timerErr))
	} else {
		ts := strings.TrimSpace(timerOut)
		if ts == "active" || ts == "waiting" {
			checks = passCheck(checks, "timer_active", fmt.Sprintf("timer is %s", ts))
		} else {
			checks = failCheck(checks, "timer_active", fmt.Sprintf("timer status is %q (expected active or waiting)", ts))
		}
	}

	return checks
}

// ---------------------------------------------------------------------------
// Internal cert helpers
// ---------------------------------------------------------------------------

// isSelfSignedPEM returns true when the PEM-encoded certificate's issuer equals its subject.
func isSelfSignedPEM(pemBytes []byte) bool {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	return cert.Issuer.String() == cert.Subject.String()
}

// isLEIssued returns true when any certificate in pemBytes was issued by Let's Encrypt.
func isLEIssued(pemBytes []byte) bool {
	for len(pemBytes) > 0 {
		var block *pem.Block
		block, pemBytes = pem.Decode(pemBytes)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		issuerOrg := strings.Join(cert.Issuer.Organization, " ")
		if strings.Contains(issuerOrg, "Let's Encrypt") ||
			strings.Contains(cert.Issuer.CommonName, "Let's Encrypt") ||
			cert.Issuer.CommonName == "R3" ||
			cert.Issuer.CommonName == "R10" ||
			cert.Issuer.CommonName == "E1" ||
			cert.Issuer.CommonName == "E6" {
			return true
		}
	}
	return false
}

// ensure allRequired is used (silence unused warning in case callers use it externally).
var _ = allRequired
