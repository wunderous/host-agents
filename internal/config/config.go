package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/opute-io/host-agents/internal/provider"
)

// Config holds host agent runtime configuration from environment variables.
type Config struct {
	HostMCPPort          int
	HostMCPBindHost      string
	IsReverseTunnel      bool
	HostWSURL            string
	MCPURL               string
	MCPHealthURL         string
	RemoteAgentID        string
	RemoteAgentAuthToken string
	MCPAuthToken         string
	BridgeToken          string
	ProviderID           string
	OnboardingToken      string
	OnboardingSessionID  string
	TestMode             bool
}

func Load() Config {
	port, _ := strconv.Atoi(envOr("HOST_MCP_PORT", "3004"))
	providerID := string(provider.NormalizeProviderID(os.Getenv("OPUTE_INFRA_PROVIDER_ID")))
	agentID := strings.TrimSpace(os.Getenv("OPUTE_REMOTE_AGENT_ID"))
	if agentID == "" {
		agentID = "local-bridge-host"
	}
	mcpAuth := strings.TrimSpace(os.Getenv("MCP_AUTH_TOKEN"))
	tunnelAuth := firstNonEmpty(
		os.Getenv("OPUTE_REMOTE_AGENT_AUTH_TOKEN"),
		os.Getenv("OPUTE_CPC_TOKEN"),
	)
	if tunnelAuth == "" && mcpAuth != "" && !strings.HasPrefix(mcpAuth, "opha_") {
		tunnelAuth = mcpAuth
	}
	if tunnelAuth == "" {
		tunnelAuth = firstNonEmpty(os.Getenv("OPUTE_BRIDGE_TOKEN"), os.Getenv("BRIDGE_TOKEN"))
	}
	bindHost := envOr("HOST_MCP_BIND_HOST", "127.0.0.1")
	wsURL := envOr("OPUTE_HOST_WS_URL", "ws://"+bindHost+":9091")
	mcpURL := strings.TrimSpace(os.Getenv("OPUTE_MCP_URL"))
	if mcpURL == "" {
		mcpURL = "http://127.0.0.1:9091/mcp"
	}
	healthURL := strings.TrimSpace(os.Getenv("OPUTE_MCP_HEALTH_URL"))
	if healthURL == "" {
		healthURL = "http://127.0.0.1:" + envOr("AGENT_PORT", "9091") + "/health"
	}
	return Config{
		HostMCPPort:          port,
		HostMCPBindHost:      bindHost,
		IsReverseTunnel:      os.Getenv("OPUTE_REVERSE_TUNNEL") == "true",
		HostWSURL:            wsURL,
		MCPURL:               mcpURL,
		MCPHealthURL:         healthURL,
		RemoteAgentID:        agentID,
		RemoteAgentAuthToken: tunnelAuth,
		MCPAuthToken:         mcpAuth,
		BridgeToken:          firstNonEmpty(os.Getenv("OPUTE_BRIDGE_TOKEN"), os.Getenv("BRIDGE_TOKEN")),
		ProviderID:           providerID,
		OnboardingToken:      strings.TrimSpace(os.Getenv("OPUTE_ONBOARDING_TOKEN")),
		OnboardingSessionID:  strings.TrimSpace(os.Getenv("OPUTE_ONBOARDING_SESSION_ID")),
		TestMode:             os.Getenv("OPUTE_TEST") == "true" || os.Getenv("NODE_ENV") == "test",
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func (c Config) AllowedAuthTokens() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, t := range []string{c.MCPAuthToken, c.BridgeToken, c.RemoteAgentAuthToken} {
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}
