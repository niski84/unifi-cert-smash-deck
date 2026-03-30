package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// DefaultKeyPath returns the default path for the generated ed25519 key.
func DefaultKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".ssh", "id_ed25519_unificert")
}

// Ensure returns an existing key at keyPath or generates a new ed25519 key pair.
// If keyPath is empty, DefaultKeyPath() is used.
// Returns the actual path used, whether a new key was generated, and any error.
func Ensure(keyPath string) (path string, generated bool, err error) {
	if keyPath == "" {
		keyPath = DefaultKeyPath()
	}

	// If private key file already exists, return it.
	if _, statErr := os.Stat(keyPath); statErr == nil {
		return keyPath, false, nil
	}

	// Generate a new ed25519 key pair.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", false, fmt.Errorf("generate ed25519 key: %w", err)
	}

	// Marshal private key to PEM using openssh format.
	privPEM, err := ssh.MarshalPrivateKey(priv, "unifi-cert-smash-deck")
	if err != nil {
		return "", false, fmt.Errorf("marshal private key: %w", err)
	}

	// Write private key (0600).
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return "", false, fmt.Errorf("create key directory: %w", err)
	}
	privBytes := pem.EncodeToMemory(privPEM)
	if err := os.WriteFile(keyPath, privBytes, 0o600); err != nil {
		return "", false, fmt.Errorf("write private key: %w", err)
	}

	// Build and write public key (0644).
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", false, fmt.Errorf("build ssh public key: %w", err)
	}
	pubLine := string(ssh.MarshalAuthorizedKey(sshPub))
	// Replace trailing newline so we can append the comment properly.
	pubLine = strings.TrimRight(pubLine, "\n") + " unifi-cert-smash-deck\n"
	pubPath := keyPath + ".pub"
	if err := os.WriteFile(pubPath, []byte(pubLine), 0o644); err != nil {
		// Non-fatal – private key is written; attempt cleanup is best-effort.
		return keyPath, true, fmt.Errorf("write public key: %w", err)
	}

	return keyPath, true, nil
}

// PublicKeyLine returns the OpenSSH authorized_keys line for the key at keyPath.
// It reads keyPath+".pub" first; if that fails it falls back to parsing the private key.
func PublicKeyLine(keyPath string) (string, error) {
	pubPath := keyPath + ".pub"
	if raw, err := os.ReadFile(pubPath); err == nil {
		return strings.TrimSpace(string(raw)), nil
	}

	// Fallback: parse private key and re-derive public key.
	privRaw, err := os.ReadFile(keyPath)
	if err != nil {
		return "", fmt.Errorf("read private key %s: %w", keyPath, err)
	}
	signer, err := ssh.ParsePrivateKey(privRaw)
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}
	line := strings.TrimRight(string(ssh.MarshalAuthorizedKey(signer.PublicKey())), "\n")
	return line, nil
}

// ScanHostKeys dials host:port and collects the server's host keys.
// Authentication will intentionally fail; that is expected and not treated as an error.
// An error is returned only when no host keys could be collected.
func ScanHostKeys(host string, port int) ([]ssh.PublicKey, error) {
	var collected []ssh.PublicKey

	cfg := &ssh.ClientConfig{
		User: "scan",
		Auth: []ssh.AuthMethod{
			ssh.Password("scan"),
		},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			// Copy the key (the callback may be called multiple times).
			collected = append(collected, key)
			return nil
		},
		Timeout: 10 * time.Second,
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	// We expect the dial to return an error because authentication will fail.
	// We only care that HostKeyCallback was invoked.
	_, _ = ssh.Dial("tcp", addr, cfg)

	if len(collected) == 0 {
		return nil, fmt.Errorf("no host keys received from %s", addr)
	}
	return collected, nil
}

// WriteKnownHostsFile formats the provided host keys and writes them to filePath (mode 0600).
// Port 22 uses bare hostname format; other ports use [host]:port format.
func WriteKnownHostsFile(host string, port int, keys []ssh.PublicKey, filePath string) error {
	var hostEntry string
	if port == 22 {
		hostEntry = host
	} else {
		hostEntry = fmt.Sprintf("[%s]:%d", host, port)
	}

	var lines []string
	for _, key := range keys {
		keyType := key.Type()
		keyB64 := base64.StdEncoding.EncodeToString(key.Marshal())
		lines = append(lines, fmt.Sprintf("%s %s %s", hostEntry, keyType, keyB64))
	}
	content := strings.Join(lines, "\n") + "\n"

	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		return fmt.Errorf("create known_hosts directory: %w", err)
	}
	return os.WriteFile(filePath, []byte(content), 0o600)
}
