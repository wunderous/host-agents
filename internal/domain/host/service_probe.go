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
	// Where the unit lives and what it launches. Running/enabled says whether a
	// service is up; it does not say WHOSE it is, and a caller holding no
	// install-time record of a unit has no other way to establish that. Both are
	// read-only systemd properties, which is all this probe is allowed to be.
	fragmentPath, execStart := "", ""
	showResult, showErr := s.shared.HostCommandRunner(
		append(append([]string{}, commandPrefix...), "show", serviceName, "-p", "FragmentPath", "-p", "ExecStart", "--no-pager"),
		onData, 15*time.Second)
	if showErr == nil && showResult.ExitCode == 0 {
		fragmentPath, execStart = parseUnitLaunchProperties(showResult.Stdout)
	}
	return map[string]any{
		"serviceName":   serviceName,
		"scope":         scope,
		"status":        status,
		"active":        result.ExitCode == 0 && status == "active",
		"enabled":       enabledErr == nil && enabledResult.ExitCode == 0 && unitFileState == "enabled",
		"unitFileState": unitFileState,
		"exitCode":      result.ExitCode,
		"fragmentPath":  fragmentPath,
		"execStart":     execStart,
	}, nil
}

// parseUnitLaunchProperties reads FragmentPath and the ExecStart executable out
// of `systemctl show`. ExecStart is a structured value rather than a plain
// string -- `{ path=/x ; argv[]=/x serve ; ... }` -- and only the executable is
// wanted, because it is the part that says which installation the unit belongs
// to.
func parseUnitLaunchProperties(output string) (fragmentPath, execStart string) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "FragmentPath="):
			fragmentPath = strings.TrimSpace(strings.TrimPrefix(line, "FragmentPath="))
		case strings.HasPrefix(line, "ExecStart=") && execStart == "":
			if index := strings.Index(line, "path="); index >= 0 {
				value := line[index+len("path="):]
				if end := strings.Index(value, " ;"); end >= 0 {
					value = value[:end]
				}
				execStart = strings.TrimSpace(value)
			}
		}
	}
	return fragmentPath, execStart
}

// unitEnablement maps unit name to its systemd enablement state.
//
// Best-effort by design: this detail is an addition to a listing that is useful
// without it, so a failure here reports every unit as "unknown" rather than
// failing the whole call. It never reports a unit as enabled on a guess.
func (s *Service) unitEnablement(commandPrefix []string) map[string]string {
	command := append(append([]string{}, commandPrefix...), "list-unit-files", "--type=service", "--no-pager", "--plain", "--no-legend")
	result, err := s.shared.HostCommandRunner(command, nil, 15*time.Second)
	if err != nil {
		return nil
	}
	states := make(map[string]string)
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		states[fields[0]] = fields[1]
	}
	return states
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
	enablement := s.unitEnablement(commandPrefix)
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
		// `enabled` used to be the literal `true` for every row. `list-units`
		// does not report enablement at all -- it reports load/active/sub state
		// -- so the field was answering a question nothing had asked systemd,
		// and a disabled, static or masked unit was indistinguishable from one
		// set to start at boot. `list-unit-files` is the command that knows.
		state, known := enablement[unitName]
		if !known {
			state = "unknown"
		}
		services = append(services, map[string]any{
			"uri":         uri.String(),
			"serviceName": serviceName,
			"scope":       scope,
			"status":      status,
			"active":      active,
			"enabled":     state == "enabled" || state == "enabled-runtime",
			// The raw systemd word, because the boolean above flattens six
			// distinct states into two. A `static` unit is not disabled, it has
			// no install section to enable; a `masked` one cannot be started at
			// all. A caller deciding whether to act needs the difference.
			"enablementState": state,
		})
	}
	return map[string]any{
		"services": services,
		"total":    len(services),
	}, nil
}
