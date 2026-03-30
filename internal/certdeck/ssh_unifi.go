package certdeck

import (
	"bufio"
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

	"github.com/niski84/unifi-cert-smash-deck/internal/sshkey"
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

// RunCommand opens a new SSH session, runs cmd, returns combined stdout+stderr.
func (s *SSHUnifi) RunCommand(ctx context.Context, cmd string) (string, error) {
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

	out, err := session.CombinedOutput(cmd)
	return string(out), err
}

// WriteRemoteFile writes data to remotePath on the UDM via SFTP, creating parent directories as needed.
func (s *SSHUnifi) WriteRemoteFile(ctx context.Context, remotePath string, data []byte, mode os.FileMode) error {
	client, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	sc, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("sftp client: %w", err)
	}
	defer sc.Close()

	dir := filepath.Dir(remotePath)
	if dir != "" && dir != "." {
		if mkErr := sc.MkdirAll(dir); mkErr != nil {
			return fmt.Errorf("mkdir %s: %w", dir, mkErr)
		}
	}

	f, err := sc.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("open remote file %s: %w", remotePath, err)
	}
	defer f.Close()

	if err := f.Chmod(mode); err != nil {
		// Non-fatal on some platforms.
		_ = err
	}

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write remote file %s: %w", remotePath, err)
	}
	return nil
}

// ReadRemoteFile reads a file from the UDM via SFTP and returns its contents.
func (s *SSHUnifi) ReadRemoteFile(ctx context.Context, remotePath string) ([]byte, error) {
	client, err := s.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	sc, err := sftp.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("sftp client: %w", err)
	}
	defer sc.Close()

	f, err := sc.Open(remotePath)
	if err != nil {
		return nil, fmt.Errorf("open remote file %s: %w", remotePath, err)
	}
	defer f.Close()

	return io.ReadAll(f)
}

// ExecStream opens an SSH session for cmd, streams each output line to logCh, and waits for exit.
func (s *SSHUnifi) ExecStream(ctx context.Context, cmd string, logCh chan<- string) error {
	client, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	pr, pw := io.Pipe()
	session.Stdout = pw
	session.Stderr = pw

	if err := session.Start(cmd); err != nil {
		_ = pw.Close()
		return fmt.Errorf("start command: %w", err)
	}

	// Scan lines in a goroutine; close the pipe when the session exits.
	doneCh := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			select {
			case logCh <- line:
			case <-ctx.Done():
				return
			}
		}
		close(doneCh)
	}()

	waitErr := session.Wait()
	_ = pw.Close()
	<-doneCh

	return waitErr
}

// CheckInternet runs two connectivity probes from the UDM:
//   - cfOK: whether Cloudflare (1.1.1.1) is reachable
//   - leOK: whether Let's Encrypt ACME endpoint is reachable
func (s *SSHUnifi) CheckInternet(ctx context.Context) (cfOK bool, leOK bool, err error) {
	cfOut, cfErr := s.RunCommand(ctx, `curl -sf --connect-timeout 5 https://1.1.1.1 -o /dev/null && echo OK || echo FAIL`)
	if cfErr != nil {
		return false, false, fmt.Errorf("internet check (CF): %w", cfErr)
	}
	cfOK = strings.Contains(cfOut, "OK")

	leOut, leErr := s.RunCommand(ctx, `curl -sf --connect-timeout 5 https://acme-v02.api.letsencrypt.org/directory -o /dev/null && echo OK || echo FAIL`)
	if leErr != nil {
		// Treat as reachability failure, not a fatal error.
		leOK = false
	} else {
		leOK = strings.Contains(leOut, "OK")
	}

	return cfOK, leOK, nil
}

// DeployPublicKey appends pubKey to /root/.ssh/authorized_keys on the UDM via SFTP.
// It creates /root/.ssh if it does not exist, and skips writing if the key is already present.
func (s *SSHUnifi) DeployPublicKey(ctx context.Context, pubKey string) error {
	client, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	sc, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("sftp client: %w", err)
	}
	defer sc.Close()

	// Ensure /root/.ssh exists.
	sshDir := "/root/.ssh"
	if mkErr := sc.MkdirAll(sshDir); mkErr != nil {
		return fmt.Errorf("mkdir %s: %w", sshDir, mkErr)
	}
	_ = sc.Chmod(sshDir, 0o700)

	akPath := sshDir + "/authorized_keys"
	var existing []byte
	f, openErr := sc.Open(akPath)
	if openErr == nil {
		existing, _ = io.ReadAll(f)
		f.Close()
	}

	pk := strings.TrimSpace(pubKey)
	if strings.Contains(string(existing), pk) {
		// Key already present.
		return nil
	}

	var newContent bytes.Buffer
	newContent.Write(existing)
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		newContent.WriteByte('\n')
	}
	newContent.WriteString(pk)
	newContent.WriteByte('\n')

	wf, err := sc.OpenFile(akPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("open authorized_keys for writing: %w", err)
	}
	defer wf.Close()
	_ = wf.Chmod(0o600)

	if _, err := wf.Write(newContent.Bytes()); err != nil {
		return fmt.Errorf("write authorized_keys: %w", err)
	}
	return nil
}

// ScanAndSaveKnownHosts is a package-level function (does not require an established SSH connection).
// It scans the host keys of host:port and writes them to savePath.
func ScanAndSaveKnownHosts(host string, port int, savePath string) error {
	keys, err := sshkey.ScanHostKeys(host, port)
	if err != nil {
		return fmt.Errorf("scan host keys: %w", err)
	}
	return sshkey.WriteKnownHostsFile(host, port, keys, savePath)
}
