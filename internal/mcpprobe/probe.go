// Package mcpprobe contains the small, transport-level probe shared by the
// Host Agent server and host-owned public exposure reconciliation.
package mcpprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const ProtocolVersion = "2026-07-28"

// ValidateEndpoint validates and normalizes an exposed MCP resource URL. A
// public binding must end at the MCP resource itself so a caller cannot turn
// the tunnel bootstrap into a generic arbitrary-URL proxy.
func ValidateEndpoint(endpoint string) (string, error) {
	resourceURL, _, err := probeURLs(endpoint)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(resourceURL)
	if err != nil {
		return "", err
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("MCP endpoint must not contain a query or fragment")
	}
	return resourceURL, nil
}

// ProbeAuthenticatedMCPEndpoint proves that an exposed endpoint supports the
// user-facing contract: OAuth client credentials (or an explicitly supplied
// bearer) authorize a Streamable HTTP tools/list request. Secrets are used
// only in memory and are never returned in observations or errors.
func ProbeAuthenticatedMCPEndpoint(ctx context.Context, endpoint, bearerToken string) (map[string]any, error) {
	resourceURL, tokenURL, err := probeURLs(endpoint)
	if err != nil {
		return nil, err
	}

	token := strings.TrimSpace(bearerToken)
	authorizationMode := "provided-bearer"
	if token == "" {
		token, err = mintProbeToken(ctx, tokenURL, resourceURL)
		if err != nil {
			return nil, err
		}
		authorizationMode = "client-credentials"
	}

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "host-agent-mcp-probe",
		"method":  "tools/list",
		"params": map[string]any{
			"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion": ProtocolVersion,
				"io.modelcontextprotocol/clientInfo": map[string]any{
					"name":    "opute-host-agent-mcp-probe",
					"version": "1.0.0",
				},
				"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode MCP tools/list probe: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, resourceURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("create MCP tools/list probe: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	request.Header.Set("Mcp-Method", "tools/list")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("MCP tools/list probe failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("MCP tools/list probe returned HTTP %d", response.StatusCode)
	}

	var rpc struct {
		Result *struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&rpc); err != nil {
		return nil, fmt.Errorf("decode MCP tools/list probe: %w", err)
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("MCP tools/list probe returned JSON-RPC error %d", rpc.Error.Code)
	}
	if rpc.Result == nil {
		return nil, fmt.Errorf("MCP tools/list probe returned no result")
	}

	return map[string]any{
		"servingContract":   "mcp-exposure.v1",
		"endpoint":          resourceURL,
		"ready":             true,
		"authenticated":     true,
		"authorizationMode": authorizationMode,
		"protocolVersion":   ProtocolVersion,
		"statusCode":        response.StatusCode,
		"toolCount":         len(rpc.Result.Tools),
	}, nil
}

func probeURLs(endpoint string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", "", fmt.Errorf("MCP endpoint must be an absolute HTTP(S) URL without userinfo")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(parsed.Path, "/mcp") {
		return "", "", fmt.Errorf("MCP endpoint must end with /mcp")
	}
	parsed.Fragment = ""
	resourceURL := parsed.String()
	tokenURL := *parsed
	tokenURL.Path = strings.TrimSuffix(parsed.Path, "/mcp") + "/oauth/token"
	tokenURL.RawQuery = ""
	return resourceURL, tokenURL.String(), nil
}

func mintProbeToken(ctx context.Context, tokenURL, resourceURL string) (string, error) {
	form := url.Values{
		"grant_type": {"client_credentials"},
		"client_id":  {"opute-mcp-host"},
		"resource":   {resourceURL},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create MCP OAuth token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return "", fmt.Errorf("MCP OAuth token request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("MCP OAuth token request returned HTTP %d", response.StatusCode)
	}
	var tokenBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&tokenBody); err != nil {
		return "", fmt.Errorf("decode MCP OAuth token response: %w", err)
	}
	if strings.TrimSpace(tokenBody.AccessToken) == "" {
		return "", fmt.Errorf("MCP OAuth token response did not contain access_token")
	}
	return strings.TrimSpace(tokenBody.AccessToken), nil
}
