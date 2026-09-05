package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const (
	platformTunnelName       = "opute-platform-opute-io"
	cloudflareAPIDefaultBase = "https://api.cloudflare.com/client/v4"
)

var platformHostnames = map[string]struct{}{
	"platform.opute.io": {},
	"mcp.opute.io":      {},
}

type dedicatedTunnelResult struct {
	TunnelID    string
	DNSRecordID string
	RunToken    string
	Hostname    string
	TunnelName  string
}

type cloudflareAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cloudflareEnvelope struct {
	Success bool                 `json:"success"`
	Errors  []cloudflareAPIError `json:"errors"`
	Result  json.RawMessage      `json:"result"`
}

type cloudflareTunnel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cloudflareDNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

type cloudflareIngressRule struct {
	Hostname      string         `json:"hostname,omitempty"`
	Service       string         `json:"service"`
	OriginRequest map[string]any `json:"originRequest,omitempty"`
}

// dedicatedTunnelName deliberately has no fallback. A dedicated tunnel owns
// its whole ingress: ensureDedicatedTunnel replaces the rule list with the one
// hostname it was asked for. A default name therefore does not produce a
// harmless extra tunnel - it silently redirects the request at whichever
// tunnel carries that name and evicts the hostname already published there.
// That is how harness.opute.io lost its route to a host-exposure connector
// call that omitted tunnelName. An absent name is a caller defect, and
// refusePlatformTunnelName reports it as one.
func dedicatedTunnelName(args map[string]any) string {
	return strings.TrimSpace(stringInput(args, "tunnelName", ""))
}

func refusePlatformTunnelName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("tunnelName is required")
	}
	if strings.EqualFold(trimmed, platformTunnelName) {
		return fmt.Errorf("refusing to mutate platform Cloudflare tunnel %q", trimmed)
	}
	return nil
}

func refusePlatformHostname(hostname string) error {
	trimmed := strings.ToLower(strings.TrimSpace(hostname))
	if trimmed == "" {
		return fmt.Errorf("hostname is required")
	}
	if _, blocked := platformHostnames[trimmed]; blocked {
		return fmt.Errorf("refusing to attach platform hostname %q to a dedicated host tunnel", trimmed)
	}
	return nil
}

func refusePlatformIngress(rules []cloudflareIngressRule) error {
	for _, rule := range rules {
		hostname := strings.ToLower(strings.TrimSpace(rule.Hostname))
		if _, blocked := platformHostnames[hostname]; blocked {
			return fmt.Errorf("refusing to mutate a tunnel whose ingress includes %q", hostname)
		}
	}
	return nil
}

func parseTunnelIDFromCNAME(content string) string {
	trimmed := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(content)), ".")
	const suffix = ".cfargotunnel.com"
	if !strings.HasSuffix(trimmed, suffix) {
		return ""
	}
	id := strings.TrimSuffix(trimmed, suffix)
	if !cloudflareIdentifierPattern.MatchString(id) {
		return ""
	}
	return id
}

func cnameTarget(tunnelID string) string {
	return tunnelID + ".cfargotunnel.com"
}

func cloudflareAPIBase() string {
	return strings.TrimRight(firstNonEmpty(os.Getenv("CLOUDFLARE_API_BASE"), cloudflareAPIDefaultBase), "/")
}

func cloudflareCreds() (accountID, zoneID, apiToken string, err error) {
	accountID = strings.TrimSpace(os.Getenv("CLOUDFLARE_ACCOUNT_ID"))
	zoneID = strings.TrimSpace(os.Getenv("CLOUDFLARE_ZONE_ID"))
	apiToken = strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN"))
	if accountID == "" || zoneID == "" || apiToken == "" {
		return "", "", "", fmt.Errorf("dedicated Cloudflare tunnel requires CLOUDFLARE_ACCOUNT_ID, CLOUDFLARE_ZONE_ID, and CLOUDFLARE_API_TOKEN")
	}
	return accountID, zoneID, apiToken, nil
}

func ensureDedicatedTunnel(ctx context.Context, args map[string]any) (*dedicatedTunnelResult, error) {
	hostname := strings.ToLower(strings.TrimSpace(stringInput(args, "hostname", "")))
	if err := refusePlatformHostname(hostname); err != nil {
		return nil, err
	}
	tunnelName := dedicatedTunnelName(args)
	if err := refusePlatformTunnelName(tunnelName); err != nil {
		return nil, err
	}
	localTarget := strings.TrimSpace(stringInput(args, "localTarget", ""))
	if localTarget == "" {
		return nil, fmt.Errorf("localTarget is required")
	}
	accountID, zoneID, apiToken, err := cloudflareCreds()
	if err != nil {
		return nil, err
	}
	tunnel, err := ensureCloudflareTunnelByName(ctx, apiToken, accountID, tunnelName)
	if err != nil {
		return nil, err
	}
	rules, err := cloudflareTunnelIngress(ctx, apiToken, accountID, tunnel.ID)
	if err != nil {
		return nil, err
	}
	if err := refusePlatformIngress(rules); err != nil {
		return nil, err
	}
	// Dedicated hostnames own the whole connector. Merging into the in-cluster
	// platform/mcp ingress would share one tunnel UUID and make unroll unsafe.
	if err := putCloudflareTunnelIngress(ctx, apiToken, accountID, tunnel.ID, []cloudflareIngressRule{
		{Hostname: hostname, Service: localTarget},
		{Service: "http_status:404"},
	}); err != nil {
		return nil, err
	}
	record, err := upsertDedicatedCNAME(ctx, apiToken, accountID, zoneID, hostname, tunnel.ID)
	if err != nil {
		return nil, err
	}
	token, err := cloudflareTunnelToken(ctx, apiToken, accountID, tunnel.ID)
	if err != nil {
		return nil, err
	}
	return &dedicatedTunnelResult{
		TunnelID:    tunnel.ID,
		DNSRecordID: record.ID,
		RunToken:    token,
		Hostname:    hostname,
		TunnelName:  tunnelName,
	}, nil
}

func unpublishDedicatedTunnel(ctx context.Context, args map[string]any) error {
	hostname := strings.ToLower(strings.TrimSpace(stringInput(args, "hostname", "")))
	if err := refusePlatformHostname(hostname); err != nil {
		return err
	}
	tunnelName := dedicatedTunnelName(args)
	if err := refusePlatformTunnelName(tunnelName); err != nil {
		return err
	}
	accountID, zoneID, apiToken, err := cloudflareCreds()
	if err != nil {
		return err
	}
	tunnel, err := findCloudflareTunnelByName(ctx, apiToken, accountID, tunnelName)
	if err != nil {
		return err
	}
	if tunnel != nil {
		rules, ingressErr := cloudflareTunnelIngress(ctx, apiToken, accountID, tunnel.ID)
		if ingressErr != nil {
			return ingressErr
		}
		if err := refusePlatformIngress(rules); err != nil {
			return err
		}
	}
	record, err := findHostnameCNAME(ctx, apiToken, zoneID, hostname)
	if err != nil {
		return err
	}
	if record != nil {
		targetID := parseTunnelIDFromCNAME(record.Content)
		if targetID != "" {
			target, lookupErr := getCloudflareTunnel(ctx, apiToken, accountID, targetID)
			if lookupErr != nil {
				return lookupErr
			}
			if target != nil {
				if err := refusePlatformTunnelName(target.Name); err != nil {
					return err
				}
				if err := refusePlatformIngressMustLoad(ctx, apiToken, accountID, target.ID); err != nil {
					return err
				}
			}
		}
		if err := cloudflareDelete(ctx, apiToken, fmt.Sprintf("%s/zones/%s/dns_records/%s", cloudflareAPIBase(), zoneID, record.ID)); err != nil {
			return fmt.Errorf("delete dedicated CNAME: %w", err)
		}
	}
	if tunnel != nil {
		if err := cloudflareDelete(ctx, apiToken, fmt.Sprintf("%s/accounts/%s/cfd_tunnel/%s?force=true", cloudflareAPIBase(), accountID, tunnel.ID)); err != nil {
			return fmt.Errorf("delete dedicated tunnel: %w", err)
		}
	}
	return nil
}

// probeTunnelRouting reports the hostnames the named tunnel actually serves.
// Nothing else in this capability observes the route itself: a token file on
// disk and a healthy local listener both stay perfectly green while the
// tunnel's ingress has been rewritten to somebody else's hostname, which is
// exactly the drift that took harness.opute.io down to the catch-all 404
// while every other signal still read as ready. Confirmation is required -
// a configuration this call cannot read reports routed=false rather than
// assuming the route survived.
func probeTunnelRouting(ctx context.Context, args map[string]any, hostnames []string) (routedHostnames []string, routed bool, tunnelID string) {
	tunnelName := dedicatedTunnelName(args)
	if tunnelName == "" || len(hostnames) == 0 {
		return nil, false, ""
	}
	accountID, _, apiToken, err := cloudflareCreds()
	if err != nil {
		return nil, false, ""
	}
	tunnel, err := findCloudflareTunnelByName(ctx, apiToken, accountID, tunnelName)
	if err != nil || tunnel == nil {
		return nil, false, ""
	}
	rules, err := cloudflareTunnelIngress(ctx, apiToken, accountID, tunnel.ID)
	if err != nil {
		return nil, false, tunnel.ID
	}
	served := map[string]struct{}{}
	for _, rule := range rules {
		name := strings.ToLower(strings.TrimSpace(rule.Hostname))
		if name == "" {
			continue
		}
		served[name] = struct{}{}
		routedHostnames = append(routedHostnames, name)
	}
	routed = true
	for _, hostname := range hostnames {
		if _, ok := served[strings.ToLower(strings.TrimSpace(hostname))]; !ok {
			routed = false
		}
	}
	return routedHostnames, routed, tunnel.ID
}

func refusePlatformIngressMustLoad(ctx context.Context, apiToken, accountID, tunnelID string) error {
	rules, err := cloudflareTunnelIngress(ctx, apiToken, accountID, tunnelID)
	if err != nil {
		return err
	}
	return refusePlatformIngress(rules)
}

func ensureCloudflareTunnelByName(ctx context.Context, apiToken, accountID, name string) (*cloudflareTunnel, error) {
	existing, err := findCloudflareTunnelByName(ctx, apiToken, accountID, name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	body, err := json.Marshal(map[string]any{"name": name, "config_src": "cloudflare"})
	if err != nil {
		return nil, err
	}
	var created cloudflareTunnel
	if err := cloudflareJSON(ctx, apiToken, http.MethodPost, fmt.Sprintf("%s/accounts/%s/cfd_tunnel", cloudflareAPIBase(), accountID), body, &created); err != nil {
		return nil, fmt.Errorf("create Cloudflare tunnel: %w", err)
	}
	if created.ID == "" {
		return nil, fmt.Errorf("create Cloudflare tunnel: missing id")
	}
	return &created, nil
}

func findCloudflareTunnelByName(ctx context.Context, apiToken, accountID, name string) (*cloudflareTunnel, error) {
	endpoint := fmt.Sprintf("%s/accounts/%s/cfd_tunnel?is_deleted=false&name=%s", cloudflareAPIBase(), accountID, url.QueryEscape(name))
	var tunnels []cloudflareTunnel
	if err := cloudflareJSON(ctx, apiToken, http.MethodGet, endpoint, nil, &tunnels); err != nil {
		return nil, fmt.Errorf("list Cloudflare tunnels: %w", err)
	}
	for _, tunnel := range tunnels {
		if tunnel.Name == name && tunnel.ID != "" {
			found := tunnel
			return &found, nil
		}
	}
	return nil, nil
}

func getCloudflareTunnel(ctx context.Context, apiToken, accountID, tunnelID string) (*cloudflareTunnel, error) {
	if !cloudflareIdentifierPattern.MatchString(tunnelID) {
		return nil, fmt.Errorf("invalid Cloudflare tunnel id")
	}
	var tunnel cloudflareTunnel
	err := cloudflareJSON(ctx, apiToken, http.MethodGet, fmt.Sprintf("%s/accounts/%s/cfd_tunnel/%s", cloudflareAPIBase(), accountID, tunnelID), nil, &tunnel)
	if err != nil {
		if isCloudflareNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if tunnel.ID == "" {
		return nil, nil
	}
	return &tunnel, nil
}

func cloudflareTunnelIngress(ctx context.Context, apiToken, accountID, tunnelID string) ([]cloudflareIngressRule, error) {
	var payload struct {
		Config struct {
			Ingress []cloudflareIngressRule `json:"ingress"`
		} `json:"config"`
	}
	err := cloudflareJSON(ctx, apiToken, http.MethodGet, fmt.Sprintf("%s/accounts/%s/cfd_tunnel/%s/configurations", cloudflareAPIBase(), accountID, tunnelID), nil, &payload)
	if err != nil {
		if isCloudflareNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read Cloudflare tunnel ingress: %w", err)
	}
	return payload.Config.Ingress, nil
}

func putCloudflareTunnelIngress(ctx context.Context, apiToken, accountID, tunnelID string, rules []cloudflareIngressRule) error {
	body, err := json.Marshal(map[string]any{"config": map[string]any{"ingress": rules}})
	if err != nil {
		return err
	}
	if err := cloudflareJSON(ctx, apiToken, http.MethodPut, fmt.Sprintf("%s/accounts/%s/cfd_tunnel/%s/configurations", cloudflareAPIBase(), accountID, tunnelID), body, nil); err != nil {
		return fmt.Errorf("put Cloudflare tunnel ingress: %w", err)
	}
	return nil
}

func cloudflareTunnelToken(ctx context.Context, apiToken, accountID, tunnelID string) (string, error) {
	var token string
	if err := cloudflareJSON(ctx, apiToken, http.MethodGet, fmt.Sprintf("%s/accounts/%s/cfd_tunnel/%s/token", cloudflareAPIBase(), accountID, tunnelID), nil, &token); err != nil {
		return "", fmt.Errorf("mint Cloudflare tunnel token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("mint Cloudflare tunnel token: empty result")
	}
	return token, nil
}

func findHostnameCNAME(ctx context.Context, apiToken, zoneID, hostname string) (*cloudflareDNSRecord, error) {
	endpoint := fmt.Sprintf("%s/zones/%s/dns_records?type=CNAME&name=%s", cloudflareAPIBase(), zoneID, url.QueryEscape(hostname))
	var records []cloudflareDNSRecord
	if err := cloudflareJSON(ctx, apiToken, http.MethodGet, endpoint, nil, &records); err != nil {
		return nil, fmt.Errorf("list Cloudflare DNS records: %w", err)
	}
	for _, record := range records {
		if strings.EqualFold(record.Name, hostname) && strings.EqualFold(record.Type, "CNAME") {
			found := record
			return &found, nil
		}
	}
	return nil, nil
}

func upsertDedicatedCNAME(ctx context.Context, apiToken, accountID, zoneID, hostname, tunnelID string) (*cloudflareDNSRecord, error) {
	desired := cnameTarget(tunnelID)
	existing, err := findHostnameCNAME(ctx, apiToken, zoneID, hostname)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		targetID := parseTunnelIDFromCNAME(existing.Content)
		if targetID != "" && !strings.EqualFold(targetID, tunnelID) {
			target, lookupErr := getCloudflareTunnel(ctx, apiToken, accountID, targetID)
			if lookupErr != nil {
				return nil, lookupErr
			}
			if target != nil {
				if err := refusePlatformTunnelName(target.Name); err != nil {
					return nil, err
				}
			}
		}
		if strings.EqualFold(strings.TrimSuffix(existing.Content, "."), desired) {
			return existing, nil
		}
		body, err := json.Marshal(map[string]any{
			"type":    "CNAME",
			"name":    hostname,
			"content": desired,
			"proxied": true,
			"ttl":     1,
		})
		if err != nil {
			return nil, err
		}
		var updated cloudflareDNSRecord
		if err := cloudflareJSON(ctx, apiToken, http.MethodPut, fmt.Sprintf("%s/zones/%s/dns_records/%s", cloudflareAPIBase(), zoneID, existing.ID), body, &updated); err != nil {
			return nil, fmt.Errorf("update dedicated CNAME: %w", err)
		}
		return &updated, nil
	}
	body, err := json.Marshal(map[string]any{
		"type":    "CNAME",
		"name":    hostname,
		"content": desired,
		"proxied": true,
		"ttl":     1,
	})
	if err != nil {
		return nil, err
	}
	var created cloudflareDNSRecord
	if err := cloudflareJSON(ctx, apiToken, http.MethodPost, fmt.Sprintf("%s/zones/%s/dns_records", cloudflareAPIBase(), zoneID), body, &created); err != nil {
		return nil, fmt.Errorf("create dedicated CNAME: %w", err)
	}
	if created.ID == "" {
		return nil, fmt.Errorf("create dedicated CNAME: missing id")
	}
	return &created, nil
}

func cloudflareJSON(ctx context.Context, apiToken, method, endpoint string, body []byte, dest any) error {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+apiToken)
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 256*1024))
	if err != nil {
		return fmt.Errorf("read Cloudflare response: %w", err)
	}
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("cloudflare not found: %s", endpoint)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	var envelope cloudflareEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("decode Cloudflare response: %w", err)
	}
	if !envelope.Success {
		if len(envelope.Errors) > 0 {
			return fmt.Errorf("Cloudflare API error %d: %s", envelope.Errors[0].Code, envelope.Errors[0].Message)
		}
		return fmt.Errorf("Cloudflare API reported failure")
	}
	if dest == nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, dest); err != nil {
		return fmt.Errorf("decode Cloudflare result: %w", err)
	}
	return nil
}

func isCloudflareNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "cloudflare not found")
}
