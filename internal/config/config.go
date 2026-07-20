package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wunderous/host-agents/internal/provider"
)

// Config holds host agent runtime configuration from environment variables.
type Config struct {
	AgentMode                        string
	TransportMode                    string
	StandaloneStateDir               string
	StandaloneAllowMutations         bool
	StandaloneAllowInsecureDownloads bool
	HostMCPPort                      int
	HostMCPBindHost                  string
	IsReverseTunnel                  bool
	HostWSURL                        string
	MCPURL                           string
	MCPHealthURL                     string
	RemoteAgentID                    string
	RemoteAgentAuthToken             string
	MCPAuthToken                     string
	BridgeToken                      string
	ProviderID                       string
	OnboardingToken                  string
	OnboardingSessionID              string
	EnvFile                          string
	TestMode                         bool
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
		AgentMode:                        normalizeMode(os.Getenv("OPUTE_AGENT_MODE")),
		TransportMode:                    normalizeTransport(os.Getenv("OPUTE_TRANSPORT")),
		StandaloneStateDir:               envOr("OPUTE_STANDALONE_STATE_DIR", filepath.Join(userHomeDir(), ".opute", "standalone")),
		StandaloneAllowMutations:         os.Getenv("OPUTE_STANDALONE_ALLOW_MUTATIONS") == "true",
		StandaloneAllowInsecureDownloads: os.Getenv("OPUTE_STANDALONE_ALLOW_INSECURE_DOWNLOADS") == "true",
		HostMCPPort:                      port,
		HostMCPBindHost:                  bindHost,
		IsReverseTunnel:                  os.Getenv("OPUTE_REVERSE_TUNNEL") == "true",
		HostWSURL:                        wsURL,
		MCPURL:                           mcpURL,
		MCPHealthURL:                     healthURL,
		RemoteAgentID:                    agentID,
		RemoteAgentAuthToken:             tunnelAuth,
		MCPAuthToken:                     mcpAuth,
		BridgeToken:                      firstNonEmpty(os.Getenv("OPUTE_BRIDGE_TOKEN"), os.Getenv("BRIDGE_TOKEN")),
		ProviderID:                       providerID,
		OnboardingToken:                  strings.TrimSpace(os.Getenv("OPUTE_ONBOARDING_TOKEN")),
		OnboardingSessionID:              strings.TrimSpace(os.Getenv("OPUTE_ONBOARDING_SESSION_ID")),
		EnvFile:                          strings.TrimSpace(os.Getenv("OPUTE_HOST_AGENT_ENV_FILE")),
		TestMode:                         os.Getenv("OPUTE_TEST") == "true" || os.Getenv("NODE_ENV") == "test",
	}
}

// Validate rejects ambiguous profile combinations before the agent starts a
// listener, emits MCP protocol output, or contacts the Opute control plane.
func (c Config) Validate() error {
	rawMode := strings.TrimSpace(os.Getenv("OPUTE_AGENT_MODE"))
	if rawMode != "" && !strings.EqualFold(rawMode, "platform") && !strings.EqualFold(rawMode, "standalone") {
		return fmt.Errorf("invalid OPUTE_AGENT_MODE %q: expected platform or standalone", rawMode)
	}
	mode := strings.ToLower(strings.TrimSpace(c.AgentMode))
	if mode == "" {
		mode = normalizeMode(rawMode)
	}
	if mode != "platform" && mode != "standalone" {
		return fmt.Errorf("invalid agent mode %q: expected platform or standalone", c.AgentMode)
	}
	rawTransport := strings.TrimSpace(os.Getenv("OPUTE_TRANSPORT"))
	if rawTransport != "" && !strings.EqualFold(rawTransport, "http") && !strings.EqualFold(rawTransport, "stdio") {
		return fmt.Errorf("invalid OPUTE_TRANSPORT %q: expected http or stdio", rawTransport)
	}
	transport := strings.ToLower(strings.TrimSpace(c.TransportMode))
	if transport == "" {
		transport = normalizeTransport(rawTransport)
	}
	if transport != "http" && transport != "stdio" {
		return fmt.Errorf("invalid transport %q: expected http or stdio", c.TransportMode)
	}
	rawProvider := strings.TrimSpace(os.Getenv("OPUTE_INFRA_PROVIDER_ID"))
	providerValue := strings.TrimSpace(c.ProviderID)
	if rawProvider != "" {
		providerValue = rawProvider
	}
	if providerValue != "" && !strings.EqualFold(providerValue, "incus") {
		return fmt.Errorf("unsupported provider %q: only incus is supported", providerValue)
	}
	if mode == "standalone" {
		if transport != "stdio" {
			return fmt.Errorf("standalone mode requires stdio transport")
		}
		if c.IsReverseTunnel {
			return fmt.Errorf("standalone mode cannot use OPUTE_REVERSE_TUNNEL=true")
		}
		for _, key := range []string{
			"OPUTE_MCP_URL", "OPUTE_MCP_HEALTH_URL", "OPUTE_HOST_WS_URL",
			"OPUTE_ONBOARDING_TOKEN", "OPUTE_ONBOARDING_SESSION_ID",
			"OPUTE_REMOTE_AGENT_AUTH_TOKEN", "OPUTE_CPC_TOKEN", "OPUTE_BRIDGE_TOKEN", "BRIDGE_TOKEN",
			"MCP_AUTH_TOKEN",
		} {
			if strings.TrimSpace(os.Getenv(key)) != "" {
				return fmt.Errorf("standalone mode cannot use platform setting %s", key)
			}
		}
	}
	if mode == "platform" && transport == "stdio" {
		return fmt.Errorf("platform mode does not support stdio transport")
	}
	return nil
}

func normalizeMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "standalone") {
		return "standalone"
	}
	return "platform"
}

func normalizeTransport(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "stdio") {
		return "stdio"
	}
	return "http"
}

func userHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return home
	}
	return "."
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
