package certdeck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type cfErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// TestCloudflareToken checks the token against Cloudflare (verify endpoint, then zone list fallback).
func TestCloudflareToken(ctx context.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("paste a token in the verify field (not stored by this app)")
	}

	client := &http.Client{Timeout: 20 * time.Second}

	try := func(method, url string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		return client.Do(req)
	}

	const verifyURL = "https://api.cloudflare.com/client/v4/user/tokens/verify"
	resp, err := try(http.MethodGet, verifyURL)
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	_ = resp.Body.Close()

	var v struct {
		Success bool    `json:"success"`
		Errors  []cfErr `json:"errors"`
		Result  struct {
			Status string `json:"status"`
			ID     string `json:"id"`
		} `json:"result"`
	}
	_ = json.Unmarshal(body, &v)

	if resp.StatusCode == http.StatusOK && v.Success {
		st := v.Result.Status
		if st == "" {
			st = "ok"
		}
		return fmt.Sprintf("Cloudflare accepted the token (verify status: %s). DNS-01 can use this credential.", st), nil
	}

	// Account-owned tokens may not pass /user/tokens/verify — try zone list (needs Zone read at minimum).
	zonesURL := "https://api.cloudflare.com/client/v4/zones?per_page=5"
	resp2, err := try(http.MethodGet, zonesURL)
	if err != nil {
		return "", fmt.Errorf("verify failed (%s); zone list request error: %w", summarizeCFMessages(v.Errors), err)
	}
	defer resp2.Body.Close()
	zbody, _ := io.ReadAll(io.LimitReader(resp2.Body, 512*1024))

	var z struct {
		Success bool `json:"success"`
		Result  []struct {
			Name string `json:"name"`
		} `json:"result"`
		Errors []cfErr `json:"errors"`
	}
	_ = json.Unmarshal(zbody, &z)

	if resp2.StatusCode == http.StatusOK && z.Success && len(z.Result) > 0 {
		names := make([]string, 0, len(z.Result))
		for _, r := range z.Result {
			if r.Name != "" {
				names = append(names, r.Name)
			}
		}
		return fmt.Sprintf("Token can call the Cloudflare API (zones visible: %s). OK for DNS-01.", strings.Join(names, ", ")), nil
	}
	if resp2.StatusCode == http.StatusOK && z.Success {
		return "", fmt.Errorf("token works but no zones returned — check token zone scope")
	}

	return "", fmt.Errorf("cloudflare API rejected this token (verify + zone list). %s", summarizeCFMessages(z.Errors))
}

func summarizeCFMessages(errs []cfErr) string {
	if len(errs) == 0 {
		return "see Cloudflare dashboard / token permissions"
	}
	var b strings.Builder
	for i, e := range errs {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(e.Message)
	}
	return b.String()
}

// TestSSHUniFi connects over SSH and reads the remote certificate path (same as a renew pre-check).
func TestSSHUniFi(ctx context.Context, cfg AppConfig) (string, error) {
	if strings.TrimSpace(cfg.SSHHost) == "" {
		return "", fmt.Errorf("SSH host is not set")
	}
	s := NewSSHUnifi(cfg)
	cn, notAfter, err := s.RemoteCertInfo(ctx)
	if err != nil {
		return "", fmt.Errorf("SSH/SFTP to UniFi failed: %w", err)
	}
	if cn == "" {
		cn = "(no CN)"
	}
	return fmt.Sprintf("SSH OK — read %s, CN=%q, expires %s", cfg.RemoteCertPath, cn, notAfter.UTC().Format(time.RFC3339)), nil
}
