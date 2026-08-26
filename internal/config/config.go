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
	TenantID                         string
	InstanceID                       string
	InstanceRoot                     string
	RelayConfigDir                   string
	OwnershipMode                    string
	SharedHostOwnerInstance          string
	StandaloneStateDir               string
	SQLiteDatabaseRoot               string
	StandaloneAllowMutations         bool
	StandaloneAllowInsecureDownloads bool
	StandaloneInstanceID             string
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
	HostResourceLockDir              string
	HostResourceMaxNormal            int
	HostResourceMaxHeavy             int
	HostResourceMaxQueued            int
	HostResourceMinMemoryBytes       int64
	HostResourceMinDiskBytes         int64
	HostResourceDiskPaths            []string
}

func Load() Config {
	mode := normalizeMode(os.Getenv("OPUTE_AGENT_MODE"))
	instanceID := strings.TrimSpace(envValue("OPUTE_HOST_AGENT_INSTANCE"))
	if instanceID == "" {
		if mode == "standalone" {
			instanceID = "standalone"
		} else {
			instanceID = "platform"
		}
	}
	instanceRoot := strings.TrimSpace(envValue("OPUTE_HOST_AGENT_INSTANCE_ROOT"))
	if instanceRoot == "" {
		instanceRoot = filepath.Join(userConfigDir(), "instances", instanceID)
	}
	stateDir := strings.TrimSpace(envValue("OPUTE_STANDALONE_STATE_DIR"))
	if stateDir == "" {
		stateDir = filepath.Join(instanceRoot, "state")
	}
	sqliteDatabaseRoot := strings.TrimSpace(envValue("OPUTE_HOST_AGENT_SQLITE_ROOT"))
	if sqliteDatabaseRoot == "" {
		sqliteDatabaseRoot = filepath.Join(instanceRoot, "databases")
	}
	relayDir := strings.TrimSpace(envValue("OPUTE_HOST_AGENT_RELAY_DIR"))
	if relayDir == "" {
		relayDir = filepath.Join(instanceRoot, "local-llm-relays")
	}
	defaultPort := "3004"
	if mode == "standalone" {
		// Avoid colliding with platform dogfood host on 3004.
		defaultPort = "3014"
	}
	port, _ := strconv.Atoi(envOr("HOST_MCP_PORT", defaultPort))
	providerID := string(provider.NormalizeProviderID(envValue("OPUTE_INFRA_PROVIDER_ID")))
	tenantID := strings.TrimSpace(envValue("OPUTE_TENANT_ID"))
	if tenantID == "" {
		tenantID = "local"
	}
	agentID := strings.TrimSpace(envValue("OPUTE_REMOTE_AGENT_ID"))
	mcpAuth := strings.TrimSpace(envValue("MCP_AUTH_TOKEN"))
	tunnelAuth := firstNonEmpty(
		envValue("OPUTE_REMOTE_AGENT_AUTH_TOKEN"),
		envValue("OPUTE_CPC_TOKEN"),
		mcpAuth,
	)
	bindHost := envOr("HOST_MCP_BIND_HOST", "127.0.0.1")
	wsURL := envOr("OPUTE_HOST_WS_URL", "ws://"+bindHost+":9091")
	mcpURL := strings.TrimSpace(envValue("OPUTE_MCP_URL"))
	if mcpURL == "" {
		mcpURL = "http://127.0.0.1:9091/mcp"
	}
	healthURL := strings.TrimSpace(envValue("OPUTE_MCP_HEALTH_URL"))
	if healthURL == "" {
		healthURL = "http://127.0.0.1:" + envOr("AGENT_PORT", "9091") + "/health"
	}
	return Config{
		AgentMode:                        mode,
		TenantID:                         tenantID,
		InstanceID:                       instanceID,
		InstanceRoot:                     instanceRoot,
		RelayConfigDir:                   relayDir,
		OwnershipMode:                    normalizeOwnershipMode(envValue("OPUTE_INCUS_OWNERSHIP_MODE")),
		SharedHostOwnerInstance:          strings.TrimSpace(envValue("OPUTE_SHARED_HOST_OWNER_INSTANCE")),
		StandaloneStateDir:               stateDir,
		SQLiteDatabaseRoot:               sqliteDatabaseRoot,
		StandaloneAllowMutations:         os.Getenv("OPUTE_STANDALONE_ALLOW_MUTATIONS") == "true",
		StandaloneAllowInsecureDownloads: os.Getenv("OPUTE_STANDALONE_ALLOW_INSECURE_DOWNLOADS") == "true",
		StandaloneInstanceID:             strings.TrimSpace(envValue("OPUTE_LOCAL_HOST_AGENT_INSTANCE_ID")),
		HostMCPPort:                      port,
		HostMCPBindHost:                  bindHost,
		IsReverseTunnel:                  os.Getenv("OPUTE_REVERSE_TUNNEL") == "true",
		HostWSURL:                        wsURL,
		MCPURL:                           mcpURL,
		MCPHealthURL:                     healthURL,
		RemoteAgentID:                    agentID,
		RemoteAgentAuthToken:             tunnelAuth,
		MCPAuthToken:                     mcpAuth,
		BridgeToken:                      mcpAuth,
		ProviderID:                       providerID,
		OnboardingToken:                  strings.TrimSpace(envValue("OPUTE_ONBOARDING_TOKEN")),
		OnboardingSessionID:              strings.TrimSpace(envValue("OPUTE_ONBOARDING_SESSION_ID")),
		EnvFile:                          strings.TrimSpace(envValue("OPUTE_HOST_AGENT_ENV_FILE")),
		TestMode:                         os.Getenv("OPUTE_TEST") == "true" || os.Getenv("NODE_ENV") == "test",
		HostResourceLockDir:              envOr("OPUTE_HOST_RESOURCE_LOCK_DIR", filepath.Join(userConfigDir(), "host-resource-coordinator")),
		HostResourceMaxNormal:            envIntOr("OPUTE_HOST_MAX_NORMAL_OPERATIONS", 2),
		HostResourceMaxHeavy:             envIntOr("OPUTE_HOST_MAX_HEAVY_OPERATIONS", 1),
		HostResourceMaxQueued:            envIntOr("OPUTE_HOST_MAX_QUEUED_OPERATIONS", 16),
		HostResourceMinMemoryBytes:       envInt64Or("OPUTE_HOST_MIN_AVAILABLE_MEMORY_BYTES", 0),
		HostResourceMinDiskBytes:         envInt64Or("OPUTE_HOST_MIN_AVAILABLE_DISK_BYTES", 0),
		HostResourceDiskPaths:            envPathsOr("OPUTE_HOST_RESOURCE_DISK_PATHS", []string{"/"}),
	}
}

// Validate rejects ambiguous profile combinations before the agent starts a
// listener, emits MCP protocol output, or contacts the Opute control plane.
func (c Config) Validate() error {
	if c.TenantID != "" {
		if err := validateTenantID(c.TenantID); err != nil {
			return err
		}
	}
	instanceID := strings.TrimSpace(c.InstanceID)
	if instanceID == "" {
		if strings.EqualFold(strings.TrimSpace(c.AgentMode), "standalone") {
			instanceID = "standalone"
		} else {
			instanceID = "platform"
		}
	}
	if err := validateInstanceID(instanceID); err != nil {
		return err
	}
	if c.OwnershipMode != "" && c.OwnershipMode != "audit" && c.OwnershipMode != "enforce" {
		return fmt.Errorf("invalid OPUTE_INCUS_OWNERSHIP_MODE %q: expected audit or enforce", c.OwnershipMode)
	}
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
	if strings.TrimSpace(c.RemoteAgentID) == "" {
		return fmt.Errorf("OPUTE_REMOTE_AGENT_ID is required; the host agent must be onboarded with one canonical id")
	}
	if err := validateAgentID(c.RemoteAgentID); err != nil {
		return err
	}
	if c.HostMCPPort < 0 {
		return fmt.Errorf("HOST_MCP_PORT must be non-negative")
	}
	if !c.IsReverseTunnel && c.HostMCPPort == 0 && strings.TrimSpace(os.Getenv("HOST_MCP_PORT")) == "0" {
		return fmt.Errorf("HOST_MCP_PORT must be positive for direct HTTP mode")
	}
	rawTransport := strings.TrimSpace(os.Getenv("OPUTE_TRANSPORT"))
	if rawTransport != "" && !strings.EqualFold(rawTransport, "http") {
		return fmt.Errorf("invalid OPUTE_TRANSPORT %q: only Streamable HTTP (http) is supported", rawTransport)
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
		if c.IsReverseTunnel {
			return fmt.Errorf("standalone mode cannot use OPUTE_REVERSE_TUNNEL=true")
		}
		for _, key := range []string{
			"OPUTE_MCP_URL", "OPUTE_MCP_HEALTH_URL", "OPUTE_HOST_WS_URL",
			"OPUTE_ONBOARDING_TOKEN", "OPUTE_ONBOARDING_SESSION_ID",
			"OPUTE_REMOTE_AGENT_AUTH_TOKEN", "OPUTE_CPC_TOKEN",
			"MCP_AUTH_TOKEN",
		} {
			if strings.TrimSpace(os.Getenv(key)) != "" {
				return fmt.Errorf("standalone mode cannot use platform setting %s", key)
			}
		}
	}
	return nil
}

func validateTenantID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("OPUTE_TENANT_ID is required")
	}
	if len(value) > 32 {
		return fmt.Errorf("OPUTE_TENANT_ID must be at most 32 characters")
	}
	for i, ch := range value {
		valid := ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-'
		if !valid || (i == 0 && ch == '-') {
			return fmt.Errorf("OPUTE_TENANT_ID %q is invalid: use [a-z][a-z0-9-]{0,31}", value)
		}
	}
	return nil
}

func normalizeMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "standalone") {
		return "standalone"
	}
	return "platform"
}

func normalizeOwnershipMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "enforce") {
		return "enforce"
	}
	return "audit"
}

func validateInstanceID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("OPUTE_HOST_AGENT_INSTANCE is required")
	}
	if len(value) > 63 {
		return fmt.Errorf("OPUTE_HOST_AGENT_INSTANCE must be at most 63 characters")
	}
	for i, ch := range value {
		valid := ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-'
		if !valid || (i == 0 && ch == '-') || (i == len(value)-1 && ch == '-') {
			return fmt.Errorf("OPUTE_HOST_AGENT_INSTANCE %q is invalid: use [a-z0-9][a-z0-9-]{0,62}", value)
		}
	}
	return nil
}

func validateAgentID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("OPUTE_REMOTE_AGENT_ID is required")
	}
	if len(value) > 255 {
		return fmt.Errorf("OPUTE_REMOTE_AGENT_ID must be at most 255 characters")
	}
	for _, ch := range value {
		if ch < 0x21 || ch > 0x7e {
			return fmt.Errorf("OPUTE_REMOTE_AGENT_ID %q is invalid: use printable non-whitespace characters", value)
		}
	}
	return nil
}

func userHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return home
	}
	return "."
}

func userConfigDir() string {
	if configured := strings.TrimSpace(envValue("XDG_CONFIG_HOME")); configured != "" {
		return configured
	}
	return filepath.Join(userHomeDir(), ".config", "opute")
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(envValue(key)); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	value := strings.TrimSpace(envValue(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envInt64Or(key string, fallback int64) int64 {
	value := strings.TrimSpace(envValue(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func envPathsOr(key string, fallback []string) []string {
	value := strings.TrimSpace(envValue(key))
	if value == "" {
		return append([]string(nil), fallback...)
	}
	paths := make([]string, 0)
	for _, path := range strings.Split(value, ",") {
		if path = strings.TrimSpace(path); path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return append([]string(nil), fallback...)
	}
	return paths
}

// envValue accepts both ordinary systemd EnvironmentFile values and values
// emitted by shell-quoting helpers. Some existing installations contain
// literal surrounding quotes; keeping them makes URL health probes and
// reverse-tunnel dialing fail before the agent can reconnect.
func envValue(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if len(value) >= 2 {
		if (value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"') {
			return strings.TrimSpace(value[1 : len(value)-1])
		}
	}
	return value
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
