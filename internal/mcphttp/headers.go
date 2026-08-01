package mcphttp

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/wunderous/host-agents/schemas"
)

const tasksExtensionID = "io.modelcontextprotocol/tasks"

type wireClientFixture struct {
	Accept                string `json:"accept"`
	ProtocolVersionHeader string `json:"protocolVersionHeader"`
	ProtocolVersion       string `json:"protocolVersion"`
	MethodHeader          string `json:"methodHeader"`
	NameHeader            string `json:"nameHeader"`
}

var (
	fixtureOnce sync.Once
	fixture     wireClientFixture
	fixtureErr  error
)

func wireFixture() (wireClientFixture, error) {
	fixtureOnce.Do(func() {
		raw, err := schemas.FS.ReadFile("streamable-http-client.json")
		if err != nil {
			fixtureErr = err
			return
		}
		fixtureErr = json.Unmarshal(raw, &fixture)
	})
	return fixture, fixtureErr
}

// ModernRequestEnvelope builds params._meta required for MCP 2026-07-28 tools/call.
func ModernRequestEnvelope(clientVersion string) (map[string]any, error) {
	cfg, err := wireFixture()
	if err != nil {
		return nil, err
	}
	version := clientVersion
	if version == "" {
		version = "1.0.0"
	}
	return map[string]any{
		"io.modelcontextprotocol/protocolVersion": cfg.ProtocolVersion,
		"io.modelcontextprotocol/clientInfo": map[string]any{
			"name":    "opute-host-agent",
			"version": version,
		},
		"io.modelcontextprotocol/clientCapabilities": map[string]any{
			"extensions": map[string]any{
				tasksExtensionID: map[string]any{},
			},
		},
	}, nil
}

// ApplyToolsCallRequestHeaders sets Streamable HTTP headers for tools/call.
func ApplyToolsCallRequestHeaders(req *http.Request, toolName string) error {
	cfg, err := wireFixture()
	if err != nil {
		return err
	}
	if cfg.Accept != "" {
		req.Header.Set("Accept", cfg.Accept)
	}
	if cfg.ProtocolVersionHeader != "" && cfg.ProtocolVersion != "" {
		req.Header.Set(cfg.ProtocolVersionHeader, cfg.ProtocolVersion)
	}
	if cfg.MethodHeader != "" {
		req.Header.Set(cfg.MethodHeader, "tools/call")
	}
	if cfg.NameHeader != "" && toolName != "" {
		req.Header.Set(cfg.NameHeader, toolName)
	}
	return nil
}

// RouteHostFromEnv returns the Traefik Host header for hairpin MCP calls when
// OPUTE_MCP_URL dials a cluster IP directly (OPUTE_MCP_ROUTE_HOST or MCP hostname).
func RouteHostFromEnv() string {
	if routeHost := strings.TrimSpace(os.Getenv("OPUTE_MCP_ROUTE_HOST")); routeHost != "" {
		return routeHost
	}
	mcpURL := strings.TrimSpace(os.Getenv("OPUTE_MCP_URL"))
	if mcpURL == "" {
		return ""
	}
	parsed, err := url.Parse(mcpURL)
	if err != nil {
		return ""
	}
	host := parsed.Hostname()
	if host == "" || host == "127.0.0.1" || host == "localhost" {
		return ""
	}
	return host
}

// ApplyMcpRouteHost sets req.Host for Traefik ingress when dialing a backend IP.
func ApplyMcpRouteHost(req *http.Request) {
	if routeHost := RouteHostFromEnv(); routeHost != "" {
		req.Host = routeHost
		req.Header.Set("Host", routeHost)
	}
}

// ApplyStreamableHTTPRequestHeaders sets Accept + MCP-Protocol-Version on MCP HTTP calls.
func ApplyStreamableHTTPRequestHeaders(req *http.Request) error {
	cfg, err := wireFixture()
	if err != nil {
		return err
	}
	if cfg.Accept != "" {
		req.Header.Set("Accept", cfg.Accept)
	}
	if cfg.ProtocolVersionHeader != "" && cfg.ProtocolVersion != "" {
		req.Header.Set(cfg.ProtocolVersionHeader, cfg.ProtocolVersion)
	}
	return nil
}
