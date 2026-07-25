package ops

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	clusterAgentBinaryPath              = "/usr/local/lib/opute/cluster-agent"
	clusterAgentConfigPath              = "/etc/opute/cluster-agent.json"
	clusterAgentArtifactDownloadSeconds = 180
	clusterAgentInstallTimeout          = 10 * time.Minute
	clusterAgentServiceWait             = 3 * time.Minute
)

type clusterAgentArch string

const (
	clusterAgentArchX64   clusterAgentArch = "x64"
	clusterAgentArchARM64 clusterAgentArch = "arm64"
)

type clusterAgentConfig struct {
	BridgeURL           string `json:"bridgeUrl"`
	BridgeMcpURL        string `json:"bridgeMcpUrl"`
	BridgeToken         string `json:"bridgeToken"`
	ClusterID           string `json:"clusterId"`
	ClusterName         string `json:"clusterName"`
	AgentID             string `json:"agentId"`
	APIEndpoint         string `json:"apiEndpoint,omitempty"`
	ProviderID          string `json:"providerId,omitempty"`
	ResourceID          string `json:"resourceId,omitempty"`
	PollIntervalSeconds int    `json:"pollIntervalSeconds"`
}

func normalizeClusterAgentArch(unameMachine string) clusterAgentArch {
	normalized := strings.ToLower(strings.TrimSpace(unameMachine))
	if normalized == "aarch64" || normalized == "arm64" {
		return clusterAgentArchARM64
	}
	return clusterAgentArchX64
}

func resolveClusterAgentArtifactURL(bridgeURL string, arch clusterAgentArch) string {
	return fmt.Sprintf("%s/artifacts/cluster-agent/%s.gz", strings.TrimRight(bridgeURL, "/"), arch)
}

func renderClusterAgentServiceUnit() string {
	return fmt.Sprintf(`[Unit]
Description=Opute Cluster Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=%s
Restart=always
RestartSec=5
Environment=OPUTE_AGENT_CONFIG=%s

[Install]
WantedBy=multi-user.target
`, clusterAgentBinaryPath, clusterAgentConfigPath)
}

func renderClusterAgentInstallScript(bridgeURL string, arch clusterAgentArch, configJSON []byte) string {
	artifactURL := resolveClusterAgentArtifactURL(bridgeURL, arch)
	encodedConfig := base64.StdEncoding.EncodeToString(configJSON)
	encodedUnit := base64.StdEncoding.EncodeToString([]byte(renderClusterAgentServiceUnit()))
	return strings.Join([]string{
		"set -euo pipefail",
		"mkdir -p /usr/local/lib/opute /etc/opute",
		fmt.Sprintf("systemctl stop %s 2>/dev/null || true", clusterAgentServiceName),
		"if ! command -v curl >/dev/null 2>&1; then apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y curl; fi",
		"if ! command -v gunzip >/dev/null 2>&1; then apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y gzip; fi",
		fmt.Sprintf(
			"curl -sfL --max-time %d %s | gunzip > %s",
			clusterAgentArtifactDownloadSeconds,
			shellEscape(artifactURL),
			clusterAgentBinaryPath,
		),
		fmt.Sprintf("chmod 755 %s", clusterAgentBinaryPath),
		fmt.Sprintf("printf '%%s' %s | base64 -d > %s", shellEscape(encodedConfig), clusterAgentConfigPath),
		fmt.Sprintf(
			"printf '%%s' %s | base64 -d > /etc/systemd/system/%s.service",
			shellEscape(encodedUnit),
			clusterAgentServiceName,
		),
		"systemctl daemon-reload",
		fmt.Sprintf("systemctl enable --now %s", clusterAgentServiceName),
		fmt.Sprintf("systemctl restart %s", clusterAgentServiceName),
	}, "\n")
}

func defaultBridgePort() int {
	for _, key := range []string{"BRIDGE_PORT", "PLATFORM_MCP_PORT"} {
		if v := strings.TrimSpace(envOr(key, "")); v != "" {
			if port, err := strconv.Atoi(v); err == nil && port > 0 {
				return port
			}
		}
	}
	return 9093
}

func buildBridgeEndpointURL(host string, port int) string {
	return fmt.Sprintf("http://%s:%d", host, port)
}

func isLoopbackBridgeURL(bridgeURL string) bool {
	parsed := strings.TrimSpace(bridgeURL)
	if parsed == "" {
		return false
	}
	if !strings.HasPrefix(parsed, "http://") && !strings.HasPrefix(parsed, "https://") {
		parsed = "http://" + parsed
	}
	hostPort := strings.TrimPrefix(strings.TrimPrefix(parsed, "https://"), "http://")
	host := hostPort
	if idx := strings.Index(hostPort, "/"); idx >= 0 {
		host = hostPort[:idx]
	}
	if idx := strings.Index(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (s *HostOperationsService) probeBridgeHealthFromGuest(vmName, baseURL string, onData func(string)) bool {
	healthURL := strings.TrimRight(baseURL, "/") + "/health"
	script := fmt.Sprintf("curl -sf -o /dev/null %s", shellEscape(healthURL))
	res, err := s.runVMExec(vmName, []string{"bash", "-lc", script}, onData, 15*time.Second)
	return err == nil && res.ExitCode == 0
}

func (s *HostOperationsService) readVMDefaultGateway(vmName string, onData func(string)) string {
	res, err := s.runVMExec(vmName, []string{"bash", "-lc", "ip route show default | awk '{print $3; exit}'"}, onData, 15*time.Second)
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	gateway := strings.TrimSpace(res.Stdout)
	if gateway == "" || net.ParseIP(gateway) == nil {
		return ""
	}
	return gateway
}

func (s *HostOperationsService) resolveBridgeEndpointForVM(
	vmName string,
	bridgeURL string,
	bridgePort int,
	onData func(string),
) (string, error) {
	bridgeURL = strings.TrimRight(strings.TrimSpace(bridgeURL), "/")
	if bridgeURL != "" && !isLoopbackBridgeURL(bridgeURL) && s.probeBridgeHealthFromGuest(vmName, bridgeURL, onData) {
		return bridgeURL, nil
	}

	port := bridgePort
	if port <= 0 {
		port = defaultBridgePort()
	}

	seen := map[string]struct{}{}
	candidates := make([]string, 0, 4)
	pushCandidate := func(host string) {
		host = strings.TrimSpace(host)
		if host == "" {
			return
		}
		url := buildBridgeEndpointURL(host, port)
		if _, ok := seen[url]; ok {
			return
		}
		seen[url] = struct{}{}
		candidates = append(candidates, url)
	}

	pushCandidate(s.readVMDefaultGateway(vmName, onData))
	pushCandidate(resolveHyperVDefaultSwitchIPv4())

	for _, host := range []string{
		"host.lan",
		strings.TrimSpace(envOr("OPUTE_BRIDGE_GUEST_HOST", "")),
		strings.TrimSpace(envOr("BRIDGE_GUEST_HOST", "")),
	} {
		if host == "host.lan" {
			res, err := s.runVMExec(vmName, []string{"getent", "hosts", "host.lan"}, onData, 10*time.Second)
			if err == nil && res.ExitCode == 0 {
				fields := strings.Fields(strings.TrimSpace(res.Stdout))
				if len(fields) > 0 {
					pushCandidate(fields[0])
				}
			}
			continue
		}
		pushCandidate(host)
	}

	for _, candidate := range candidates {
		if s.probeBridgeHealthFromGuest(vmName, candidate, onData) {
			return strings.TrimRight(candidate, "/"), nil
		}
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("failed to resolve bridge endpoint for VM %q: no gateway candidates", vmName)
	}
	return "", fmt.Errorf(
		"failed to resolve reachable bridge endpoint for VM %q (probed %d candidate(s) on port %d)",
		vmName,
		len(candidates),
		port,
	)
}

func (s *HostOperationsService) readVMMachineArch(vmName string, onData func(string)) (clusterAgentArch, error) {
	res, err := s.runVMExec(vmName, []string{"uname", "-m"}, onData, 30*time.Second)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("uname -m failed in %s: %s", vmName, firstNonEmpty(res.Stderr, res.Stdout, "unknown error"))
	}
	return normalizeClusterAgentArch(res.Stdout), nil
}

func (s *HostOperationsService) InstallClusterAgent(args InstallClusterAgentArgs, onData func(string)) (map[string]any, error) {
	if strings.TrimSpace(args.ClusterID) == "" {
		return nil, fmt.Errorf("clusterId is required")
	}
	if strings.TrimSpace(args.ClusterName) == "" {
		return nil, fmt.Errorf("clusterName is required")
	}
	if strings.TrimSpace(args.AgentID) == "" {
		return nil, fmt.Errorf("agentId is required")
	}
	if strings.TrimSpace(args.BridgeToken) == "" {
		return nil, fmt.Errorf("bridgeToken is required")
	}

	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" && args.Source != "k3s-host" && args.Source != "external" {
		return nil, fmt.Errorf("vmName is required for VM-based cluster agent install")
	}

	if args.Source == "k3s-host" || vmName == "" {
		return nil, fmt.Errorf("host-native cluster agent install is not implemented in the Go host agent")
	}

	if err := s.waitForVMExecReady(vmName, 5*time.Minute, onData); err != nil {
		return nil, err
	}

	bridgeURL := strings.TrimSpace(args.BridgeURL)
	if bridgeURL == "" {
		bridgeURL = resolveBridgeURLFromEnv()
	}
	resolvedBridgeURL, err := s.resolveBridgeEndpointForVM(vmName, bridgeURL, args.BridgePort, onData)
	if err != nil {
		return nil, err
	}

	arch, err := s.readVMMachineArch(vmName, onData)
	if err != nil {
		return nil, err
	}

	configJSON, err := json.Marshal(clusterAgentConfig{
		BridgeURL:           resolvedBridgeURL,
		BridgeMcpURL:        resolvedBridgeURL + "/mcp",
		BridgeToken:         strings.TrimSpace(args.BridgeToken),
		ClusterID:           strings.TrimSpace(args.ClusterID),
		ClusterName:         strings.TrimSpace(args.ClusterName),
		AgentID:             strings.TrimSpace(args.AgentID),
		APIEndpoint:         strings.TrimSpace(args.APIEndpoint),
		ProviderID:          strings.TrimSpace(args.ProviderID),
		ResourceID:          strings.TrimSpace(args.ResourceID),
		PollIntervalSeconds: 5,
	})
	if err != nil {
		return nil, err
	}

	installScript := renderClusterAgentInstallScript(resolvedBridgeURL, arch, configJSON)
	res, err := s.runVMExec(vmName, []string{"bash", "-lc", installScript}, onData, clusterAgentInstallTimeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "cluster agent install failed in VM"))
	}

	if err := s.waitForVMServiceActive(vmName, clusterAgentServiceName, onData, clusterAgentServiceWait); err != nil {
		return nil, err
	}

	return map[string]any{
		"vmName":      vmName,
		"bridgeUrl":   resolvedBridgeURL,
		"serviceName": clusterAgentServiceName,
		"status":      "active",
		"arch":        string(arch),
	}, nil
}

// RestartClusterAgent restarts the cluster-agent service inside the target VM.
// The service name is intentionally fixed by the generic cluster-agent
// contract; callers select only the VM, never an arbitrary guest command.
func (s *HostOperationsService) RestartClusterAgent(vmName string, onData func(string)) (map[string]any, error) {
	vmName = strings.TrimSpace(vmName)
	if vmName == "" {
		return nil, fmt.Errorf("vmName is required")
	}
	res, err := s.runVMExec(vmName, []string{"systemctl", "restart", clusterAgentServiceName}, onData, clusterAgentServiceWait)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "cluster agent restart failed in VM"))
	}
	if err := s.waitForVMServiceActive(vmName, clusterAgentServiceName, onData, clusterAgentServiceWait); err != nil {
		return nil, err
	}
	return map[string]any{
		"vmName":      vmName,
		"serviceName": clusterAgentServiceName,
		"status":      "active",
	}, nil
}
