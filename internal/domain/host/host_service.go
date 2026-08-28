package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	osuser "os/user"
	"strings"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/heartbeat"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/internal/textutil"
)

func (s *Service) DescribeHost() HostInfoResult {
	pid := string(s.shared.Runtime.ReadProviderID())
	host, _ := os.Hostname()
	result := HostInfoResult{
		HostName:       host,
		ProviderID:     pid,
		LXCBinaryPath:  s.shared.Runtime.ProviderBinary(),
		SystemctlPath:  hostruntime.DefaultSystemctlPath,
		SupportedTools: s.deps.SupportedTools(pid),
	}
	if uri, err := resourceid.HostURI(s.shared.TenantID, textutil.FirstNonEmpty(s.shared.AgentID, host)); err == nil {
		result.URI = uri.String()
		if s.shared.ResourceRegistry != nil {
			_ = s.shared.RegisterResource(result.URI, map[string]any{"agentId": s.shared.AgentID, "hostName": host})
		}
	}
	if capacity, err := s.deps.VMInventoryCapacity(); err == nil {
		result.Capacity = &capacity
	}
	result.System = heartbeat.ReadHostSystemMetadata()
	if s.shared.ResourceSnapshot != nil {
		if result.System == nil {
			result.System = map[string]any{}
		}
		result.System["resourceAdmission"] = s.shared.ResourceSnapshot()
	}
	return result
}

func (s *Service) RunAgentShell(command string, onData func(string)) (hostexec.Result, error) {
	return s.RunAgentShellWithTimeout(command, 0, onData)
}

// RunAgentShellWithTimeout runs a caller-declared host command with an
// explicit bounded execution budget. A zero timeout preserves the command
// runner's no-deadline behavior for internal lifecycle calls; externally
// dispatched commands should provide a positive timeout so the caller's
// lifecycle has a finite, observable boundary.
func (s *Service) RunAgentShellWithTimeout(command string, timeout time.Duration, onData func(string)) (hostexec.Result, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return hostexec.Result{}, errors.New("command is required")
	}
	return s.shared.Runtime.RunHost([]string{"bash", "-lc", command}, onData, timeout)
}

func (s *Service) RestartHostService(args RestartHostServiceArgs, onData func(string)) (map[string]string, error) {
	if err := s.shared.RequireSharedHostOwner("restart_host_service"); err != nil {
		return nil, err
	}
	serviceName := strings.TrimSpace(args.ServiceName)
	if serviceName == "" {
		return nil, errors.New("serviceName is required")
	}
	if !safeSystemdUnitName.MatchString(serviceName) {
		return nil, errors.New("serviceName contains invalid characters")
	}
	restart, err := s.shared.HostCommandRunner(restartServiceCommand(serviceName), onData, 0)
	if err != nil || restart.ExitCode != 0 {
		return nil, fmt.Errorf("%s", textutil.FirstNonEmpty(restart.Stderr, restart.Stdout, "failed to restart service"))
	}
	if strings.HasPrefix(serviceName, "opute-") {
		return map[string]string{"serviceName": serviceName, "status": "scheduled"}, nil
	}
	verify, err := s.shared.HostCommandRunner(serviceStatusCommand(serviceName), onData, 0)
	if err != nil || verify.ExitCode != 0 || strings.TrimSpace(verify.Stdout) != "active" {
		return nil, fmt.Errorf("service '%s' is not active after restart", serviceName)
	}
	return map[string]string{"serviceName": serviceName, "status": "active"}, nil
}

// SetHostServiceState provides the generic, approval-gated service lifecycle
// primitive used by recovery workflows. User scope is the safe default; system
// scope is explicit and uses non-interactive sudo so MCP cannot hang on a
// password prompt.
func (s *Service) SetHostServiceState(args SetHostServiceStateArgs, onData func(string)) (map[string]any, error) {
	if err := s.shared.RequireSharedHostOwner("set_host_service_state"); err != nil {
		return nil, err
	}
	serviceName := strings.TrimSpace(args.ServiceName)
	state := strings.ToLower(strings.TrimSpace(args.State))
	scope := strings.ToLower(strings.TrimSpace(args.Scope))
	if serviceName == "" || !safeSystemdUnitName.MatchString(serviceName) {
		return nil, errors.New("serviceName is required and must be a valid systemd unit name")
	}
	if scope == "" {
		scope = "user"
	}
	if scope != "user" && scope != "system" {
		return nil, errors.New("scope must be user or system")
	}
	if state != "start" && state != "stop" && state != "restart" && state != "enable" && state != "disable" {
		return nil, errors.New("state must be start, stop, restart, enable, or disable")
	}
	// Recipes commonly reconcile a unit file immediately before changing its
	// state. Reload the matching manager so systemd cannot act on a stale unit
	// definition (notably after an ExecStart or environment change).
	reloadCommand := []string{hostruntime.DefaultSystemctlPath}
	if scope == "user" {
		reloadCommand = append(reloadCommand, "--user", "daemon-reload")
	} else {
		reloadCommand = append([]string{"sudo", "-n"}, reloadCommand...)
		reloadCommand = append(reloadCommand, "daemon-reload")
	}
	if reload, reloadErr := s.shared.HostCommandRunner(reloadCommand, onData, 0); reloadErr != nil || reload.ExitCode != 0 {
		return nil, fmt.Errorf("service manager reload failed: %s", textutil.FirstNonEmpty(reload.Stderr, reload.Stdout, "command failed"))
	}
	command := serviceStateCommand(serviceName, state, scope)
	result, err := s.shared.HostCommandRunner(command, onData, 0)
	if err != nil || result.ExitCode != 0 {
		return nil, fmt.Errorf("service state change failed: %s", textutil.FirstNonEmpty(result.Stderr, result.Stdout, "command failed"))
	}
	status := "applied"
	if state == "restart" {
		status = "scheduled"
	}
	return map[string]any{"serviceName": serviceName, "state": state, "scope": scope, "status": status}, nil
}

// EnsureHostServiceSupervisor makes the host service lifecycle explicit. WSL
// and other session-based Linux environments otherwise terminate a user
// manager as soon as the last non-interactive session exits, taking every
// caller-owned service and its listeners with it. The operation is idempotent
// and reports observed supervisor state rather than claiming service health.
func (s *Service) EnsureHostServiceSupervisor(args EnsureHostServiceSupervisorArgs, onData func(string)) (map[string]any, error) {
	if err := s.shared.RequireSharedHostOwner("ensure_host_service_supervisor"); err != nil {
		return nil, err
	}
	scope := strings.ToLower(strings.TrimSpace(args.Scope))
	if scope == "" {
		scope = "user"
	}
	if scope != "user" && scope != "system" {
		return nil, errors.New("scope must be user or system")
	}
	if scope == "system" {
		result, err := s.shared.HostCommandRunner([]string{hostruntime.DefaultSystemctlPath, "is-system-running"}, onData, 10*time.Second)
		if err != nil || (result.ExitCode != 0 && strings.TrimSpace(result.Stdout) == "") {
			return nil, fmt.Errorf("system service supervisor is unavailable: %s", textutil.FirstNonEmpty(result.Stderr, result.Stdout, "systemctl failed"))
		}
		return map[string]any{"scope": scope, "status": "ready", "persistent": true, "state": strings.TrimSpace(result.Stdout)}, nil
	}
	user := strings.TrimSpace(os.Getenv("USER"))
	if user == "" {
		identity, err := osuser.Current()
		if err != nil {
			return nil, fmt.Errorf("resolve host service user: %w", err)
		}
		user = identity.Username
	}
	if user == "" || strings.ContainsAny(user, "\r\n") {
		return nil, errors.New("resolve host service user: invalid username")
	}
	command := []string{"loginctl", "enable-linger", user}
	result, err := s.shared.HostCommandRunner(command, onData, 15*time.Second)
	if err != nil || result.ExitCode != 0 {
		// A non-root host agent may have a narrowly scoped sudo policy prepared
		// by the bootstrap installer. Never fall back to an interactive prompt.
		result, err = s.shared.HostCommandRunner([]string{"sudo", "-n", "loginctl", "enable-linger", user}, onData, 15*time.Second)
	}
	if err != nil || result.ExitCode != 0 {
		return nil, fmt.Errorf("enable persistent user service supervisor: %s", textutil.FirstNonEmpty(result.Stderr, result.Stdout, "loginctl failed"))
	}
	observed, err := s.shared.HostCommandRunner([]string{"loginctl", "show-user", user, "-p", "Linger"}, onData, 15*time.Second)
	if err != nil || observed.ExitCode != 0 || !strings.Contains(observed.Stdout, "Linger=yes") {
		return nil, fmt.Errorf("verify persistent user service supervisor: %s", textutil.FirstNonEmpty(observed.Stderr, observed.Stdout, "Linger=yes was not observed"))
	}
	bus, err := s.shared.HostCommandRunner([]string{hostruntime.DefaultSystemctlPath, "--user", "show-environment"}, onData, 15*time.Second)
	if err != nil || bus.ExitCode != 0 {
		return nil, fmt.Errorf("user service supervisor bus is unavailable: %s", textutil.FirstNonEmpty(bus.Stderr, bus.Stdout, "systemctl --user failed"))
	}
	return map[string]any{"scope": scope, "status": "ready", "persistent": true, "user": user, "linger": true, "userBus": true}, nil
}

func (s *Service) EnsureDocker(onData func(string)) (map[string]any, error) {
	return nil, errors.New("ensure_docker is not supported on Incus Linux host agents")
}

func (s *Service) EnsureK3d(onData func(string)) (map[string]any, error) {
	return nil, errors.New("ensure_k3d is not supported on Incus Linux host agents")
}

func (s *Service) WaitForSystemdActive(service string, onData func(string), timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		res, err := s.shared.HostCommandRunner([]string{hostruntime.DefaultSystemctlPath, "is-active", service}, onData, 0)
		if err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == "active" {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("systemd service '%s' did not become active within %s", service, timeout)
}

func (s *Service) DiagnoseBridge(ctx context.Context) (BridgeDiagnosticResult, error) {
	return probeBridgeHealth(ctx)
}

func (s *Service) RecoverBridge(ctx context.Context, onData func(string)) (BridgeDiagnosticResult, error) {
	serviceName := hostruntime.EnvOr("BRIDGE_SERVICE_NAME", "opute-bridge")
	if _, err := s.RestartHostService(RestartHostServiceArgs{ServiceName: serviceName}, onData); err != nil {
		return BridgeDiagnosticResult{}, err
	}
	result, err := probeBridgeHealth(ctx)
	if err != nil {
		return result, err
	}
	result.BridgeProcess.Restarted = true
	return result, nil
}
