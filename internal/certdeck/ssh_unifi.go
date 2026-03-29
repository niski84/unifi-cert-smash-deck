package certdeck

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SSHUnifi performs remote certificate inspection and installation.
type SSHUnifi struct {
	cfg AppConfig
}

func NewSSHUnifi(cfg AppConfig) *SSHUnifi {
	return &SSHUnifi{cfg: cfg}
}

func (s *SSHUnifi) clientConfig() (*ssh.ClientConfig, error) {
	keyPath := s.cfg.SSHKeyPath
	if keyPath == "" {
		return nil, fmt.Errorf("ssh_key_path is empty")
	}
	raw, err := os.ReadFile(filepath.Clean(keyPath))
	if err != nil {
		return nil, fmt.Errorf("read ssh key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("parse ssh key: %w", err)
	}

	var hostKey ssh.HostKeyCallback
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
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
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

	f, err := sftpC.Open(s.cfg.RemoteCertPath)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("open remote cert: %w", err)
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

// InstallCertificate writes cert and key over SFTP and restarts unifi-core.
func (s *SSHUnifi) InstallCertificate(ctx context.Context, certPEM, keyPEM []byte) error {
	client, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	sftpC, err := sftp.NewClient(client)
	if err != nil {
		return err
	}
	defer sftpC.Close()

	if err := writeRemoteFile(sftpC, s.cfg.RemoteCertPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("write cert: %w", err)
	}
	if err := writeRemoteFile(sftpC, s.cfg.RemoteKeyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}

	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	out, err := sess.CombinedOutput("systemctl restart unifi-core")
	if err != nil {
		return fmt.Errorf("restart unifi-core: %w: %s", err, string(out))
	}
	return nil
}

func writeRemoteFile(c *sftp.Client, path string, data []byte, mode uint32) error {
	f, err := c.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return c.Chmod(path, fs.FileMode(mode))
}
