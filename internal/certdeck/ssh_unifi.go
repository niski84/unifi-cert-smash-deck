package certdeck

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SSHUnifi performs read-only remote certificate inspection (udm-le installs on the UDM).
type SSHUnifi struct {
	cfg AppConfig
}

func NewSSHUnifi(cfg AppConfig) *SSHUnifi {
	return &SSHUnifi{cfg: cfg}
}

func sshKeyboardInteractivePassword(password string) ssh.AuthMethod {
	return ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
		if len(questions) == 0 {
			return nil, nil
		}
		answers := make([]string, len(questions))
		for i := range questions {
			answers[i] = password
		}
		return answers, nil
	})
}

func (s *SSHUnifi) clientConfig() (*ssh.ClientConfig, error) {
	pw := strings.TrimSpace(s.cfg.SSHPassword)
	if pw == "" {
		pw = strings.TrimSpace(os.Getenv("UNIFICERT_SSH_PASSWORD"))
	}

	var auth []ssh.AuthMethod
	if keyPath := strings.TrimSpace(s.cfg.SSHKeyPath); keyPath != "" {
		raw, err := os.ReadFile(filepath.Clean(keyPath))
		if err == nil {
			signer, err := ssh.ParsePrivateKey(raw)
			if err == nil {
				auth = append(auth, ssh.PublicKeys(signer))
			} else if strings.Contains(err.Error(), "passphrase protected") {
				// Key is protected, we'll rely on password if available
			}
		}
	}
	if pw != "" {
		// UniFi OS often uses keyboard-interactive instead of plain "password" auth.
		auth = append(auth, sshKeyboardInteractivePassword(pw))
		auth = append(auth, ssh.Password(pw))
	}
	if len(auth) == 0 {
		return nil, fmt.Errorf("ssh: set UNIFICERT_SSH_KEY (readable private key) and/or UNIFICERT_SSH_PASSWORD in environment (.env)")
	}

	var hostKey ssh.HostKeyCallback
	var err error
	if kh := strings.TrimSpace(s.cfg.SSHKnownHosts); kh != "" {
		hostKey, err = knownhosts.New(filepath.Clean(kh))
		if err != nil {
			return nil, fmt.Errorf("known_hosts: %w", err)
		}
	} else {
		hostKey = ssh.InsecureIgnoreHostKey() // LAN UniFi; prefer ssh_known_hosts in settings
	}

	user := s.cfg.SSHUser
	if user == "" {
		user = "root"
	}
	return &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: hostKey,
		Timeout:         15 * time.Second,
	}, nil
}

func (s *SSHUnifi) dial(ctx context.Context) (*ssh.Client, error) {
	cc, err := s.clientConfig()
	if err != nil {
		return nil, err
	}
	host := s.cfg.SSHHost
	if host == "" {
		return nil, fmt.Errorf("ssh_host is empty")
	}
	port := s.cfg.SSHPort
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cc)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// RemoteCertInfo fetches the remote certificate PEM and parses NotAfter / CN.
func (s *SSHUnifi) RemoteCertInfo(ctx context.Context) (cn string, notAfter time.Time, err error) {
	client, err := s.dial(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	defer client.Close()

	sftpC, err := sftp.NewClient(client)
	if err != nil {
		return "", time.Time{}, err
	}
	defer sftpC.Close()

	path := s.cfg.RemoteCertPath
	if path == "" {
		path = "/data/unifi-core/config/unifi-core.crt"
	}

	f, err := sftpC.Open(path)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("open remote cert at %s: %w", path, err)
	}
	defer f.Close()
	pemBytes, err := io.ReadAll(f)
	if err != nil {
		return "", time.Time{}, err
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return "", time.Time{}, fmt.Errorf("remote cert is not valid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", time.Time{}, err
	}
	return cert.Subject.CommonName, cert.NotAfter, nil
}

// RunBootstrap runs the installation/setup script on the remote UDM.
func (s *SSHUnifi) RunBootstrap(ctx context.Context, script string) (string, error) {
	client, err := s.dial(ctx)
	if err != nil {
		return "", err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	var b bytes.Buffer
	session.Stdout = &b
	session.Stderr = &b

	if err := session.Run(script); err != nil {
		return b.String(), fmt.Errorf("bootstrap failed: %w", err)
	}

	return b.String(), nil
}

// CheckUdmLeStatus checks if udm-le is installed and its service status.
func (s *SSHUnifi) CheckUdmLeStatus(ctx context.Context) (installed bool, timerActive bool, err error) {
	client, err := s.dial(ctx)
	if err != nil {
		return false, false, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return false, false, err
	}
	defer session.Close()

	cmd := `[ -f /data/udm-le/udm-le.sh ] && echo "INSTALLED"; systemctl is-active udm-le.timer 2>/dev/null || echo "inactive"`
	out, _ := session.CombinedOutput(cmd)
	outs := string(out)

	installed = strings.Contains(outs, "INSTALLED")
	timerActive = strings.Contains(outs, "active") || strings.Contains(outs, "waiting")

	return installed, timerActive, nil
}
