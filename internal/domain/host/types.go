package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/contract/vminfo"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

// defaultSystemdRunPath is host-only: no other domain launches transient
// units, so S9.2 rule 2 keeps it here rather than in hostruntime.
const defaultSystemdRunPath = "/usr/bin/systemd-run"

// HostInfoResult mirrors the TypeScript describeHost payload.
type HostInfoResult struct {
	URI            string                      `json:"uri"`
	HostName       string                      `json:"hostName"`
	ProviderID     string                      `json:"providerId"`
	LXCBinaryPath  string                      `json:"lxcBinaryPath"`
	SystemctlPath  string                      `json:"systemctlPath"`
	SupportedTools []string                    `json:"supportedTools"`
	Capacity       *vminfo.VMInventoryCapacity `json:"capacity,omitempty"`
	System         map[string]any              `json:"system,omitempty"`
}

// BridgeDiagnosticResult is returned by DiagnoseBridge.
type BridgeDiagnosticResult struct {
	BridgeProcess struct {
		Status    string `json:"status"`
		Command   string `json:"command,omitempty"`
		Restarted bool   `json:"restarted,omitempty"`
	} `json:"bridgeProcess"`
	BridgePort struct {
		Port   int    `json:"port"`
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	} `json:"bridgePort"`
	Database struct {
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	} `json:"database"`
	LastHeartbeat struct {
		At *string `json:"at"`
	} `json:"lastHeartbeat"`
	BridgeStatus string `json:"bridgeStatus"`
	CheckedAt    string `json:"checkedAt"`
}

type RestartHostServiceArgs struct {
	ServiceName string `json:"serviceName"`
}

type SetHostServiceStateArgs struct {
	ServiceName string `json:"serviceName"`
	State       string `json:"state"`
	Scope       string `json:"scope,omitempty"`
}

// EnsureHostServiceSupervisorArgs describes the lifecycle contract required by
// a caller-owned host service. It is deliberately independent of any product,
// service name, URL, or runtime: user-scoped services need a persistent
// systemd user manager, while system-scoped services only need the system
// manager to be reachable.
type EnsureHostServiceSupervisorArgs struct {
	Scope string `json:"scope,omitempty"`
}

func restartServiceCommand(serviceName string) []string {
	// The production host agent itself is a systemd *user* unit.  Invoking
	// plain systemctl from the unprivileged agent asks polkit for interactive
	// elevation and fails over MCP with “Interactive authentication required”.
	// Keep system services on the existing system scope, but route Opute-owned
	// user units through the user manager they actually belong to.
	if strings.HasPrefix(serviceName, "opute-") {
		// --no-block is essential when the target is this very host-agent
		// service: waiting for systemd to stop this process would close the MCP
		// operation before the caller can receive its result.
		return []string{hostruntime.DefaultSystemctlPath, "--user", "--no-block", "restart", serviceName}
	}
	return []string{hostruntime.DefaultSystemctlPath, "restart", serviceName}
}

func serviceStatusCommand(serviceName string) []string {
	if strings.HasPrefix(serviceName, "opute-") {
		return []string{hostruntime.DefaultSystemctlPath, "--user", "is-active", serviceName}
	}
	return []string{hostruntime.DefaultSystemctlPath, "is-active", serviceName}
}

func serviceStateUnit(serviceName string) string {
	return "host-service-state-" + strings.NewReplacer("/", "-", ":", "-", "@", "-", ".", "-").Replace(serviceName)
}

func serviceStateCommand(serviceName, state, scope string) []string {
	if state == "restart" && scope == "user" && strings.HasPrefix(serviceName, "opute-host-agent") {
		// A user-systemd transient job is outside the target service's cgroup.
		// This matters when the target is the MCP process serving this request:
		// systemctl can enqueue the restart and return a truthful scheduled
		// result before the current agent is stopped.
		return []string{
			defaultSystemdRunPath,
			"--user",
			"--unit=" + serviceStateUnit(serviceName),
			"--collect",
			"--no-block",
			hostruntime.DefaultSystemctlPath,
			"--user",
			"--no-block",
			"restart",
			serviceName,
		}
	}
	command := []string{hostruntime.DefaultSystemctlPath}
	if scope == "user" {
		command = append(command, "--user")
	} else {
		command = append([]string{"sudo", "-n"}, command...)
	}
	return append(command, state, serviceName)
}

func probeBridgeHealth(ctx context.Context) (BridgeDiagnosticResult, error) {
	port := 9093
	if p := strings.TrimSpace(os.Getenv("PLATFORM_MCP_PORT")); p != "" {
		_, _ = fmt.Sscanf(p, "%d", &port)
	}
	bridgeURL := hostruntime.EnvOr("BRIDGE_URL", fmt.Sprintf("http://127.0.0.1:%d", port))
	serviceName := hostruntime.EnvOr("BRIDGE_SERVICE_NAME", "opute-bridge")
	checkedAt := time.Now().UTC().Format(time.RFC3339)

	portOpen, portErr := probeTCPPort(ctx, "127.0.0.1", port)
	result := BridgeDiagnosticResult{CheckedAt: checkedAt}
	result.BridgeProcess.Command = serviceName
	if portOpen {
		result.BridgeProcess.Status = "running"
		result.BridgePort.Port = port
		result.BridgePort.Status = "open"
		result.BridgeStatus = "online"
	} else {
		result.BridgeProcess.Status = "stopped"
		result.BridgePort.Port = port
		result.BridgePort.Status = "closed"
		if portErr != nil {
			result.BridgePort.Error = portErr.Error()
		}
		result.BridgeStatus = "offline"
	}

	dbStatus := "unhealthy"
	dbErr := "Bridge health check failed"
	var lastHeartbeat *string
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(bridgeURL, "/")+"/health", nil)
	if err == nil {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var body struct {
					Database struct {
						Status string `json:"status"`
						Error  string `json:"error"`
					} `json:"database"`
					LastHeartbeatAt *string `json:"lastHeartbeatAt"`
				}
				if json.NewDecoder(resp.Body).Decode(&body) == nil {
					lastHeartbeat = body.LastHeartbeatAt
					if body.Database.Status == "healthy" {
						dbStatus = "healthy"
						dbErr = ""
					} else if body.Database.Error != "" {
						dbErr = body.Database.Error
					}
				}
			} else {
				dbErr = fmt.Sprintf("Bridge health check failed with HTTP %d", resp.StatusCode)
			}
		} else {
			dbErr = err.Error()
		}
	}
	result.Database.Status = dbStatus
	if dbErr != "" {
		result.Database.Error = dbErr
	}
	result.LastHeartbeat.At = lastHeartbeat
	return result, nil
}

var safeSystemdUnitName = regexp.MustCompile(`^[A-Za-z0-9_.@:-]+$`)
