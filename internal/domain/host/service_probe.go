package host

import (
	"fmt"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resourceid"
)

type InspectHostServiceArgs struct {
	ServiceName string
	Scope       string
}

// InspectHostService returns read-only systemd evidence for a caller-owned
// service. It never starts, stops, enables, or reloads the unit.
func (s *Service) InspectHostService(args InspectHostServiceArgs, onData func(string)) (map[string]any, error) {
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
	commandPrefix := []string{hostruntime.DefaultSystemctlPath}
	if scope == "user" {
		commandPrefix = append(commandPrefix, "--user")
	}
	command := append(append([]string{}, commandPrefix...), "is-active", serviceName)
	result, err := s.shared.HostCommandRunner(command, onData, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("inspect host service: %w", err)
	}
	status := strings.TrimSpace(result.Stdout)
	if status == "" {
		status = strings.TrimSpace(result.Stderr)
	}
	enabledResult, enabledErr := s.shared.HostCommandRunner(append(append([]string{}, commandPrefix...), "is-enabled", serviceName), onData, 15*time.Second)
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

// ListHostServices returns a list of systemd services and registers their canonical URIs.
func (s *Service) ListHostServices(scope string) (map[string]any, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = "user"
	}
	if scope != "user" && scope != "system" {
		return nil, fmt.Errorf("scope must be user or system")
	}
	commandPrefix := []string{hostruntime.DefaultSystemctlPath}
	if scope == "user" {
		commandPrefix = append(commandPrefix, "--user")
	}
	command := append(append([]string{}, commandPrefix...), "list-units", "--type=service", "--all", "--no-pager", "--plain", "--no-legend")
	result, err := s.shared.HostCommandRunner(command, nil, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("list host services: %w", err)
	}
	lines := strings.Split(result.Stdout, "\n")
	services := make([]map[string]any, 0, len(lines))
	tenantID := s.shared.EffectiveTenantID()
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		unitName := fields[0]
		if !strings.HasSuffix(unitName, ".service") {
			continue
		}
		serviceName := strings.TrimSuffix(unitName, ".service")
		activeState := fields[2]
		subState := fields[3]
		status := activeState
		if subState != "" && subState != activeState {
			status = activeState + "/" + subState
		}
		active := activeState == "active"
		uri, uriErr := resourceid.HostServiceURI(tenantID, scope+"/"+serviceName)
		if uriErr != nil {
			continue
		}
		if s.shared.ResourceRegistry != nil {
			_ = s.shared.RegisterResource(uri.String(), map[string]any{
				"serviceName": serviceName,
				"scope":       scope,
			})
		}
		services = append(services, map[string]any{
			"uri":         uri.String(),
			"serviceName": serviceName,
			"scope":       scope,
			"status":      status,
			"active":      active,
			"enabled":     true,
		})
	}
	return map[string]any{
		"services": services,
		"total":    len(services),
	}, nil
}
