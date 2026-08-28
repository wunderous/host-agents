package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

// Config holds host agent runtime configuration from environment variables.
type Config struct {
	AgentMode                  string
	TenantID                   string
	InstanceID                 string
	InstanceRoot               string
	RelayConfigDir             string
	OwnershipMode              string
	SharedHostOwnerInstance    string
	StandaloneStateDir         string
	SQLiteDatabaseRoot         string
	StandaloneAllowMutations   bool
	StandaloneInstanceID       string
	HostMCPPort                int
	HostMCPBindHost            string
	RemoteAgentID              string
	MCPAuthToken               string
	OputeClientSecret          string
	ProviderID                 string
	OnboardingToken            string
	OnboardingSessionID        string
	EnvFile                    string
	TestMode                   bool
	HostResourceLockDir        string
	HostResourceMaxNormal      int
	HostResourceMaxHeavy       int
	HostResourceMaxQueued      int
	HostResourceMinMemoryBytes int64
	HostResourceMinDiskBytes   int64
	HostResourceDiskPaths      []string
	// AllowLegacyHandshake enables backwards-compatible support for standard MCP clients
	// (e.g. Codex, Cursor IDE) that send initialize/notifications/initialized handshakes.
	AllowLegacyHandshake bool
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
	providerID := string(hostruntime.NormalizeProviderID(envValue("OPUTE_INFRA_PROVIDER_ID")))
	tenantID := strings.TrimSpace(envValue("OPUTE_TENANT_ID"))
	if tenantID == "" {
		tenantID = "local"
	}
	agentID := strings.TrimSpace(envValue("OPUTE_REMOTE_AGENT_ID"))
	mcpAuth := strings.TrimSpace(envValue("MCP_AUTH_TOKEN"))
	// Standalone stays loopback-only. Platform mode is intentionally reachable
	// on the host bridge because Platform/MCP pods cannot address host loopback;
	// the Host Agent authz layer still gates the authenticated MCP resource.
	defaultBindHost := "127.0.0.1"
	if mode == "platform" {
		defaultBindHost = "0.0.0.0"
	}
	bindHost := envOr("HOST_MCP_BIND_HOST", defaultBindHost)
	return Config{
		AgentMode:                  mode,
		TenantID:                   tenantID,
		InstanceID:                 instanceID,
		InstanceRoot:               instanceRoot,
		RelayConfigDir:             relayDir,
		OwnershipMode:              normalizeOwnershipMode(envValue("OPUTE_INCUS_OWNERSHIP_MODE")),
		SharedHostOwnerInstance:    strings.TrimSpace(envValue("OPUTE_SHARED_HOST_OWNER_INSTANCE")),
		StandaloneStateDir:         stateDir,
		SQLiteDatabaseRoot:         sqliteDatabaseRoot,
		StandaloneAllowMutations:   os.Getenv("OPUTE_STANDALONE_ALLOW_MUTATIONS") == "true",
		StandaloneInstanceID:       strings.TrimSpace(envValue("OPUTE_LOCAL_HOST_AGENT_INSTANCE_ID")),
		HostMCPPort:                port,
		HostMCPBindHost:            bindHost,
		RemoteAgentID:              agentID,
		MCPAuthToken:               mcpAuth,
		OputeClientSecret:          strings.TrimSpace(envValue("OPUTE_HOST_OAUTH_CLIENT_SECRET")),
		ProviderID:                 providerID,
		OnboardingToken:            strings.TrimSpace(envValue("OPUTE_ONBOARDING_TOKEN")),
		OnboardingSessionID:        strings.TrimSpace(envValue("OPUTE_ONBOARDING_SESSION_ID")),
		EnvFile:                    strings.TrimSpace(envValue("OPUTE_HOST_AGENT_ENV_FILE")),
		TestMode:                   os.Getenv("OPUTE_TEST") == "true" || os.Getenv("NODE_ENV") == "test",
		HostResourceLockDir:        envOr("OPUTE_HOST_RESOURCE_LOCK_DIR", filepath.Join(userConfigDir(), "host-resource-coordinator")),
		HostResourceMaxNormal:      envIntOr("OPUTE_HOST_MAX_NORMAL_OPERATIONS", 2),
		HostResourceMaxHeavy:       envIntOr("OPUTE_HOST_MAX_HEAVY_OPERATIONS", 1),
		HostResourceMaxQueued:      envIntOr("OPUTE_HOST_MAX_QUEUED_OPERATIONS", 16),
		HostResourceMinMemoryBytes: envInt64Or("OPUTE_HOST_MIN_AVAILABLE_MEMORY_BYTES", 0),
		HostResourceMinDiskBytes:   envInt64Or("OPUTE_HOST_MIN_AVAILABLE_DISK_BYTES", 0),
		HostResourceDiskPaths:      envPathsOr("OPUTE_HOST_RESOURCE_DISK_PATHS", []string{"/"}),
		AllowLegacyHandshake:       os.Getenv("OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE") == "true",
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
	if c.HostMCPPort <= 0 {
		return fmt.Errorf("HOST_MCP_PORT must be positive")
	}
	bindHost := strings.TrimSpace(c.HostMCPBindHost)
	if bindHost == "" {
		bindHost = "127.0.0.1"
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
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OPUTE_REVERSE_TUNNEL")), "true") {
		return fmt.Errorf("OPUTE_REVERSE_TUNNEL is retired; the kernel serves mode-scoped Streamable HTTP POST /mcp")
	}
	for _, key := range []string{
		"OPUTE_HOST_WS_URL", "OPUTE_CPC_TOKEN", "OPUTE_REMOTE_AGENT_AUTH_TOKEN",
		"OPUTE_ONBOARDING_TOKEN", "OPUTE_ONBOARDING_SESSION_ID",
	} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return fmt.Errorf("%s is retired; enroll a host resource URL instead of phone-home credentials", key)
		}
	}
	if mode == "standalone" {
		for _, key := range []string{"OPUTE_MCP_URL", "OPUTE_MCP_HEALTH_URL"} {
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
// literal surrounding quotes; keeping them makes bind and token checks fail
// closed before the agent starts a listener.
func envValue(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if len(value) >= 2 {
		if (value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"') {
			return strings.TrimSpace(value[1 : len(value)-1])
		}
	}
	return value
}

func (c Config) BootstrapToken() string {
	return strings.TrimSpace(c.MCPAuthToken)
}
