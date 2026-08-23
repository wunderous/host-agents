package ops

import (
	"fmt"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/provider"
)

type InspectHostServiceArgs struct {
	ServiceName string
	Scope       string
}

// InspectHostService returns read-only systemd evidence for a caller-owned
// service. It never starts, stops, enables, or reloads the unit.
func (s *HostOperationsService) InspectHostService(args InspectHostServiceArgs, onData func(string)) (map[string]any, error) {
	serviceName := strings.TrimSpace(args.ServiceName)
	if serviceName == "" || !safeSystemdUnitName.MatchString(serviceName) {
		return nil, fmt.Errorf("serviceName is required and must be a valid systemd unit name")
	}
	scope := strings.ToLower(strings.TrimSpace(args.Scope))
	if scope == "" {
		scope = "user"
	}
	if scope != "user" && scope != "system" {
		return nil, fmt.Errorf("scope must be user or system")
	}
	commandPrefix := []string{provider.DefaultSystemctlPath}
	if scope == "user" {
		commandPrefix = append(commandPrefix, "--user")
	}
	command := append(append([]string{}, commandPrefix...), "is-active", serviceName)
	result, err := s.hostCommandRunner(command, onData, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("inspect host service: %w", err)
	}
	status := strings.TrimSpace(result.Stdout)
	if status == "" {
		status = strings.TrimSpace(result.Stderr)
	}
	enabledResult, enabledErr := s.hostCommandRunner(append(append([]string{}, commandPrefix...), "is-enabled", serviceName), onData, 15*time.Second)
	unitFileState := strings.TrimSpace(enabledResult.Stdout)
	if unitFileState == "" {
		unitFileState = strings.TrimSpace(enabledResult.Stderr)
	}
	return map[string]any{
		"serviceName":   serviceName,
		"scope":         scope,
		"status":        status,
		"active":        result.ExitCode == 0 && status == "active",
		"enabled":       enabledErr == nil && enabledResult.ExitCode == 0 && unitFileState == "enabled",
		"unitFileState": unitFileState,
		"exitCode":      result.ExitCode,
	}, nil
}
