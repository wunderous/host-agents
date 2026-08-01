package ops

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ConfigurePlatformAgentArgs switches a standalone (or already-platform) host
// agent onto a CPC MCP URL without requiring an out-of-band shell installer.
// Secrets are written to a mode-0600 EnvironmentFile; values are never logged.
type ConfigurePlatformAgentArgs struct {
	McpURL                  string `json:"mcpUrl"`
	HostWsURL               string `json:"hostWsUrl,omitempty"`
	RemoteAgentAuthToken    string `json:"remoteAgentAuthToken"`
	HostAuthToken           string `json:"hostAuthToken"`
	RemoteAgentID           string `json:"remoteAgentId,omitempty"`
	EnvFile                 string `json:"envFile,omitempty"`
	ServiceName             string `json:"serviceName,omitempty"`
	Restart                 *bool  `json:"restart,omitempty"`
	McpHealthURL            string `json:"mcpHealthUrl,omitempty"`
	McpRouteHost            string `json:"mcpRouteHost,omitempty"`
	InstanceID              string `json:"instanceId,omitempty"`
	InstanceRoot            string `json:"instanceRoot,omitempty"`
	RelayConfigDir          string `json:"relayConfigDir,omitempty"`
	OwnershipMode           string `json:"ownershipMode,omitempty"`
	SharedHostOwnerInstance string `json:"sharedHostOwnerInstance,omitempty"`
}

// ConfigurePlatformAgent writes platform-mode settings into the host-agent env
// file and optionally schedules a user-systemd restart so the agent reconnects
// to the CPC over the reverse tunnel.
func (s *HostOperationsService) ConfigurePlatformAgent(args ConfigurePlatformAgentArgs, onData func(string)) (map[string]any, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("configure_platform_agent is unsupported on %s host agents", runtime.GOOS)
	}
	mcpURL := strings.TrimSpace(args.McpURL)
	remoteAuth := strings.TrimSpace(args.RemoteAgentAuthToken)
	hostAuth := strings.TrimSpace(args.HostAuthToken)
	if mcpURL == "" {
		return nil, errors.New("mcpUrl is required")
	}
	if remoteAuth == "" {
		return nil, errors.New("remoteAgentAuthToken is required")
	}
	if hostAuth == "" {
		return nil, errors.New("hostAuthToken is required")
	}
	parsed, err := url.Parse(mcpURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("mcpUrl must be an absolute http(s) URL")
	}

	hostWsURL := strings.TrimSpace(args.HostWsURL)
	if hostWsURL == "" {
		hostWsURL = deriveHostWsURL(mcpURL)
	}
	mcpHealthURL := strings.TrimSpace(args.McpHealthURL)
	if mcpHealthURL == "" {
		base := strings.TrimRight(mcpURL, "/")
		base = strings.TrimSuffix(base, "/mcp")
		mcpHealthURL = base + "/health"
	}

	instanceID := strings.TrimSpace(args.InstanceID)
	if instanceID == "" {
		instanceID = strings.TrimSpace(s.instanceID)
	}
	if instanceID == "" {
		instanceID = strings.TrimSpace(args.RemoteAgentID)
	}
	if instanceID == "" {
		instanceID = "platform"
	}
	if err := validateHostAgentInstanceID(instanceID); err != nil {
		return nil, err
	}
	instanceRoot := strings.TrimSpace(args.InstanceRoot)
	if instanceRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home directory: %w", err)
		}
		instanceRoot = filepath.Join(home, ".config", "opute", "instances", instanceID)
	}
	envFile := strings.TrimSpace(args.EnvFile)
	if envFile == "" {
		envFile = filepath.Join(instanceRoot, "host-agent.env")
	} else if strings.TrimSpace(args.InstanceRoot) == "" {
		instanceRoot = filepath.Dir(envFile)
	}
	if err := os.MkdirAll(filepath.Dir(envFile), 0o700); err != nil {
		return nil, fmt.Errorf("create env directory: %w", err)
	}

	mcpRouteHost := strings.TrimSpace(args.McpRouteHost)
	if mcpRouteHost == "" {
		if parsedMcp, err := url.Parse(mcpURL); err == nil {
			if host := parsedMcp.Hostname(); host != "" {
				mcpRouteHost = host
			}
		}
	}

	assignments := map[string]string{
		"OPUTE_AGENT_MODE":                 "platform",
		"OPUTE_HOST_AGENT_INSTANCE":        instanceID,
		"OPUTE_HOST_AGENT_INSTANCE_ROOT":   instanceRoot,
		"OPUTE_HOST_AGENT_RELAY_DIR":       filepath.Join(instanceRoot, "local-llm-relays"),
		"OPUTE_HOST_AGENT_SERVICE_NAME":    "opute-host-agent@" + instanceID + ".service",
		"OPUTE_INCUS_OWNERSHIP_MODE":       firstNonEmpty(args.OwnershipMode, "audit"),
		"OPUTE_SHARED_HOST_OWNER_INSTANCE": firstNonEmpty(args.SharedHostOwnerInstance, instanceID),
		"OPUTE_REVERSE_TUNNEL":             "true",
		"HOST_MCP_PORT":                    "0",
		"OPUTE_MCP_URL":                    mcpURL,
		"OPUTE_HOST_WS_URL":                hostWsURL,
		"OPUTE_REMOTE_AGENT_AUTH_TOKEN":    remoteAuth,
		"OPUTE_MCP_HEALTH_URL":             mcpHealthURL,
		"MCP_AUTH_TOKEN":                   hostAuth,
	}
	if mcpRouteHost != "" {
		assignments["OPUTE_MCP_ROUTE_HOST"] = mcpRouteHost
	}
	if remoteID := strings.TrimSpace(args.RemoteAgentID); remoteID != "" {
		assignments["OPUTE_REMOTE_AGENT_ID"] = remoteID
	}

	if onData != nil {
		onData(fmt.Sprintf("Writing platform agent env to %s (secrets redacted)", envFile))
	}
	if err := upsertEnvFile(envFile, assignments); err != nil {
		return nil, err
	}

	restart := true
	if args.Restart != nil {
		restart = *args.Restart
	}
	serviceName := strings.TrimSpace(args.ServiceName)
	if serviceName == "" {
		serviceName = "opute-host-agent@" + instanceID + ".service"
	}
	result := map[string]any{
		"envFile":      envFile,
		"mcpUrl":       mcpURL,
		"hostWsUrl":    hostWsURL,
		"mcpHealthUrl": mcpHealthURL,
		"serviceName":  serviceName,
		"restart":      restart,
		"mode":         "platform",
	}
	if !restart {
		result["status"] = "env_written"
		return result, nil
	}
	if onData != nil {
		onData(fmt.Sprintf("Scheduling restart of %s", serviceName))
	}
	restarted, err := s.SetHostServiceState(SetHostServiceStateArgs{
		ServiceName: serviceName,
		State:       "restart",
		Scope:       "user",
	}, onData)
	if err != nil {
		return nil, fmt.Errorf("env written but restart failed: %w", err)
	}
	result["status"] = "restart_scheduled"
	result["service"] = restarted
	return result, nil
}

func deriveHostWsURL(mcpURL string) string {
	trimmed := strings.TrimSpace(mcpURL)
	switch {
	case strings.HasPrefix(trimmed, "https://"):
		return "wss://" + strings.TrimPrefix(trimmed, "https://")
	case strings.HasPrefix(trimmed, "http://"):
		return "ws://" + strings.TrimPrefix(trimmed, "http://")
	default:
		return trimmed
	}
}

func upsertEnvFile(path string, assignments map[string]string) error {
	values := map[string]string{}
	var comments []string
	keyOrder := []string{}

	if raw, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				if trimmed != "" {
					comments = append(comments, line)
				}
				continue
			}
			trimmed = strings.TrimPrefix(trimmed, "export ")
			key, value, ok := strings.Cut(trimmed, "=")
			key = strings.TrimSpace(key)
			if !ok || key == "" {
				continue
			}
			if _, exists := values[key]; !exists {
				keyOrder = append(keyOrder, key)
			}
			values[key] = value
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read env file: %w", err)
	}

	preferredOrder := []string{
		"OPUTE_AGENT_MODE",
		"OPUTE_HOST_AGENT_INSTANCE",
		"OPUTE_HOST_AGENT_INSTANCE_ROOT",
		"OPUTE_HOST_AGENT_RELAY_DIR",
		"OPUTE_HOST_AGENT_SERVICE_NAME",
		"OPUTE_INCUS_OWNERSHIP_MODE",
		"OPUTE_SHARED_HOST_OWNER_INSTANCE",
		"OPUTE_REVERSE_TUNNEL",
		"HOST_MCP_PORT",
		"OPUTE_MCP_URL",
		"OPUTE_HOST_WS_URL",
		"OPUTE_MCP_HEALTH_URL",
		"OPUTE_MCP_ROUTE_HOST",
		"OPUTE_REMOTE_AGENT_AUTH_TOKEN",
		"OPUTE_REMOTE_AGENT_ID",
		"MCP_AUTH_TOKEN",
	}
	for _, key := range preferredOrder {
		if value, ok := assignments[key]; ok {
			if _, exists := values[key]; !exists {
				keyOrder = append(keyOrder, key)
			}
			values[key] = value
		}
	}
	for key, value := range assignments {
		if _, exists := values[key]; !exists {
			keyOrder = append(keyOrder, key)
		}
		values[key] = value
	}
	delete(values, "OPUTE_STANDALONE_ALLOW_MUTATIONS")
	delete(values, "OPUTE_TRANSPORT")

	var b strings.Builder
	for _, comment := range comments {
		b.WriteString(comment)
		b.WriteByte('\n')
	}
	seen := map[string]bool{}
	for _, key := range keyOrder {
		value, ok := values[key]
		if !ok || seen[key] {
			continue
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(value)
		b.WriteByte('\n')
		seen[key] = true
	}
	for key, value := range values {
		if seen[key] {
			continue
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(value)
		b.WriteByte('\n')
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".host-agent.env.*")
	if err != nil {
		return fmt.Errorf("create temp env file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod env file: %w", err)
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write env file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close env file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace env file: %w", err)
	}
	return nil
}
