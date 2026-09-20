package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	tailscaleAPIDefaultBase = "https://api.tailscale.com"
	defaultTailnet          = "-"
)

// TailscaleDevice is a subset of the Tailscale devices API response.
type TailscaleDevice struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Hostname  string   `json:"hostname"`
	Addresses []string `json:"addresses"`
	NodeKey   string   `json:"nodeKey,omitempty"`
	User      string   `json:"user,omitempty"`
	OS        string   `json:"os,omitempty"`
}

type TailscaleAuthKey struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

type tailscaleAPIClient struct {
	apiKey  string
	tailnet string
	baseURL string
	http    *http.Client
}

func newTailscaleAPIClient(apiKey, tailnet, baseURL string) *tailscaleAPIClient {
	apiKey = strings.TrimSpace(apiKey)
	tailnet = strings.TrimSpace(tailnet)
	if tailnet == "" {
		tailnet = defaultTailnet
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = tailscaleAPIDefaultBase
	}
	return &tailscaleAPIClient{
		apiKey:  apiKey,
		tailnet: tailnet,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *tailscaleAPIClient) ListDevices(ctx context.Context) ([]TailscaleDevice, error) {
	if c == nil || c.apiKey == "" {
		return nil, fmt.Errorf("tailscale API key is required")
	}
	endpoint := fmt.Sprintf("%s/api/v2/tailnet/%s/devices", c.baseURL, url.PathEscape(c.tailnet))
	var payload struct {
		Devices []TailscaleDevice `json:"devices"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &payload); err != nil {
		return nil, fmt.Errorf("list Tailscale devices: %w", err)
	}
	return payload.Devices, nil
}

// CreateAuthKey creates a reusable, preauthorized auth key.
func (c *tailscaleAPIClient) CreateAuthKey(ctx context.Context, expirySeconds int) (TailscaleAuthKey, error) {
	if c == nil || c.apiKey == "" {
		return TailscaleAuthKey{}, fmt.Errorf("tailscale API key is required")
	}
	if expirySeconds <= 0 {
		expirySeconds = 24 * 60 * 60
	}
	endpoint := fmt.Sprintf("%s/api/v2/tailnet/%s/keys", c.baseURL, url.PathEscape(c.tailnet))
	body, err := json.Marshal(map[string]any{
		"capabilities": map[string]any{
			"devices": map[string]any{
				"create": map[string]any{
					"reusable":      true,
					"ephemeral":     false,
					"preauthorized": true,
				},
			},
		},
		"expirySeconds": expirySeconds,
	})
	if err != nil {
		return TailscaleAuthKey{}, err
	}
	var key TailscaleAuthKey
	if err := c.doJSON(ctx, http.MethodPost, endpoint, body, &key); err != nil {
		return TailscaleAuthKey{}, fmt.Errorf("create Tailscale auth key: %w", err)
	}
	if strings.TrimSpace(key.Key) == "" {
		return TailscaleAuthKey{}, fmt.Errorf("Tailscale auth key response did not include a key")
	}
	return key, nil
}

func (c *tailscaleAPIClient) DeleteDevice(ctx context.Context, deviceID string) error {
	if c == nil || c.apiKey == "" {
		return fmt.Errorf("tailscale API key is required")
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return fmt.Errorf("device id is required")
	}
	endpoint := fmt.Sprintf("%s/api/v2/device/%s", c.baseURL, url.PathEscape(deviceID))
	if err := c.doJSON(ctx, http.MethodDelete, endpoint, nil, nil); err != nil {
		return fmt.Errorf("delete Tailscale device: %w", err)
	}
	return nil
}

func (c *tailscaleAPIClient) doJSON(ctx context.Context, method, endpoint string, body []byte, dest any) error {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read Tailscale API response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound && method == http.MethodDelete {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	if dest == nil || len(payload) == 0 || string(payload) == "null" {
		return nil
	}
	if err := json.Unmarshal(payload, dest); err != nil {
		return fmt.Errorf("decode Tailscale API response: %w", err)
	}
	return nil
}

func findDeviceByHostname(devices []TailscaleDevice, hostname string) (TailscaleDevice, bool) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" {
		return TailscaleDevice{}, false
	}
	for _, device := range devices {
		candidates := []string{device.Hostname, device.Name}
		for _, candidate := range candidates {
			candidate = strings.ToLower(strings.TrimSpace(candidate))
			if candidate == "" {
				continue
			}
			if candidate == hostname || strings.HasPrefix(candidate, hostname+".") || strings.Split(candidate, ".")[0] == hostname {
				return device, true
			}
		}
	}
	return TailscaleDevice{}, false
}

func deviceIPv4(device TailscaleDevice) string {
	for _, addr := range device.Addresses {
		ip := strings.TrimSpace(strings.SplitN(addr, "/", 2)[0])
		if parsed := parseIPv4(ip); parsed != "" {
			return parsed
		}
	}
	return ""
}

func parseIPv4(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, ":") {
		return ""
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 4 {
		return ""
	}
	return raw
}
