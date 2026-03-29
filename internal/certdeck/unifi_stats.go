package certdeck

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ActiveWiFiClients returns a count of WiFi (non-wired) clients if UniFi API is configured.
func ActiveWiFiClients(ctx context.Context, cfg AppConfig) (int, error) {
	host := strings.TrimRight(cfg.UniFiHost, "/")
	if host == "" || cfg.UniFiAPIKey == "" {
		return 0, fmt.Errorf("unifi not configured")
	}
	site := cfg.UniFiSite
	if site == "" {
		site = "default"
	}
	url := fmt.Sprintf("%s/proxy/network/api/s/%s/stat/sta", host, site)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-API-KEY", cfg.UniFiAPIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unifi http %s", resp.Status)
	}
	var payload struct {
		Data []struct {
			IsWired bool `json:"is_wired"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, err
	}
	n := 0
	for _, d := range payload.Data {
		if !d.IsWired {
			n++
		}
	}
	return n, nil
}
