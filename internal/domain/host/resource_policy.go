package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resourceid"
)

// These values are the Linux/WSL projection of
// opute-host-resource-policy.v1. The policy service remains neutral; this file
// is the host-owned backend that knows how to repair the systemd projection.
const (
	hostAgentProtectedSlice = "opute-host-agent-protected.slice"
	hostWorkloadSlice       = "opute-workload.slice"
	hostAgentCPUWeight      = "1000"
	hostAgentTasksMax       = "1024"
)

var hostAgentInstancePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// resourcePolicyTarget resolves only the exact host-service unit vocabulary
// owned by this agent. It intentionally does not turn an arbitrary URI into a
// systemd unit name.
func resourcePolicyTarget(target resourceid.URI) (scope, unit string, err error) {
	if target.ResourceType != resourceid.TypeHostService {
		return "", "", fmt.Errorf("host resource target must be a host-service URI")
	}
	parts := strings.SplitN(strings.TrimSpace(target.ResourceID), "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("host resource target must use <scope>/<exact-host-agent-unit>")
	}
	scope = strings.ToLower(strings.TrimSpace(parts[0]))
	if scope != "user" && scope != "system" {
		return "", "", fmt.Errorf("host resource target scope must be user or system")
	}
	name := strings.TrimSpace(parts[1])
	if strings.HasSuffix(name, ".service") {
		name = strings.TrimSuffix(name, ".service")
	}
	switch {
	case name == "opute-host-agent":
		return scope, name + ".service", nil
	case strings.HasPrefix(name, "opute-host-agent@"):
		instanceID := strings.TrimPrefix(name, "opute-host-agent@")
		if !hostAgentInstancePattern.MatchString(instanceID) {
			return "", "", fmt.Errorf("host resource target has invalid Host Agent instance id")
		}
		return scope, name + ".service", nil
	default:
		return "", "", fmt.Errorf("host resource target must name the exact opute-host-agent service")
	}
}

func renderHostResourceSliceUnits() map[string]string {
	return map[string]string{
		hostAgentProtectedSlice: "[Unit]\nDescription=Protected Opute Host Agent control slice\n\n[Slice]\nCPUWeight=1000\nTasksMax=1024\n",
		hostWorkloadSlice:       "[Unit]\nDescription=Bounded Opute Host Agent workload slice\n\n[Slice]\nMemoryHigh=5G\nMemoryMax=6G\nMemorySwapMax=1G\nCPUQuota=600%\nCPUWeight=100\nTasksMax=4096\n",
	}
}

func (s *Service) ensureUserResourceSliceUnits() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve host resource policy home: %w", err)
	}
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o700); err != nil {
		return fmt.Errorf("create user systemd unit directory: %w", err)
	}
	for name, contents := range renderHostResourceSliceUnits() {
		path := filepath.Join(unitDir, name)
		if existing, readErr := os.ReadFile(path); readErr == nil {
			// An operator may have deliberately chosen a stricter local
			// boundary. Never replace it with this profile's less restrictive
			// value during reconciliation. A missing or looser property is
			// drift and is repaired by the approved policy operation.
			if preservesStricterResourceUnit(name, string(existing)) {
				continue
			}
		} else if !os.IsNotExist(readErr) {
			return fmt.Errorf("inspect managed systemd unit %q: %w", name, readErr)
		}
		if err := writeManagedUnit(path, contents); err != nil {
			return err
		}
	}
	return nil
}

func writeManagedUnit(path, contents string) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".opute-host-resource-*.tmp")
	if err != nil {
		return fmt.Errorf("create managed systemd unit %q: %w", filepath.Base(path), err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect managed systemd unit %q: %w", filepath.Base(path), err)
	}
	if _, err := temporary.WriteString(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write managed systemd unit %q: %w", filepath.Base(path), err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close managed systemd unit %q: %w", filepath.Base(path), err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("install managed systemd unit %q: %w", filepath.Base(path), err)
	}
	return nil
}

// ReconcileHostResourcePolicy is the concrete Linux/WSL projection backend
// mounted into resource.Coordinator. It changes only the exact approved Host
// Agent unit and the two managed user slice definitions; it never accepts raw
// systemd arguments or arbitrary cgroup paths and never restarts a service.
func (s *Service) ReconcileHostResourcePolicy(ctx context.Context, target resourceid.URI) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("host resource policy reconciliation is unsupported on %s", runtime.GOOS)
	}
	scope, unit, err := resourcePolicyTarget(target)
	if err != nil {
		return err
	}
	if scope == "user" {
		if err := s.ensureUserResourceSliceUnits(); err != nil {
			return err
		}
	}
	base := []string{hostruntime.DefaultSystemctlPath}
	if scope == "user" {
		base = append(base, "--user")
	} else {
		base = append([]string{"sudo", "-n"}, base...)
	}
	if result, err := s.shared.HostCommandRunnerContext(ctx, append(append([]string(nil), base...), "daemon-reload"), nil, 15*time.Second); err != nil || result.ExitCode != 0 {
		return fmt.Errorf("reload %s systemd resource policy: %s", scope, firstHostResourceError(result.Stderr, result.Stdout, err))
	}
	if scope == "user" {
		// A slice with no member processes may be garbage-collected by systemd,
		// which removes its ControlGroup path even though the declarative limits
		// remain installed. Materialize only the managed workload slice so the
		// enforcement probe can verify the actual cgroup controllers before it
		// admits work; this does not start a workload or the Host Agent service.
		startArgs := append(append([]string(nil), base...), "start", hostWorkloadSlice)
		if result, err := s.shared.HostCommandRunnerContext(ctx, startArgs, nil, 15*time.Second); err != nil || result.ExitCode != 0 {
			return fmt.Errorf("materialize %s systemd workload slice: %s", scope, firstHostResourceError(result.Stderr, result.Stdout, err))
		}
	}
	setArgs := append(append([]string(nil), base...), "set-property", unit,
		"CPUWeight="+hostAgentCPUWeight,
		"TasksMax="+hostAgentTasksMax,
	)
	if result, err := s.shared.HostCommandRunnerContext(ctx, setArgs, nil, 15*time.Second); err != nil || result.ExitCode != 0 {
		return fmt.Errorf("apply protected Host Agent resource policy to %s: %s", unit, firstHostResourceError(result.Stderr, result.Stdout, err))
	}
	showArgs := append(append([]string(nil), base...), "show", unit, "--property=Slice,CPUWeight,TasksMax,KillMode")
	result, err := s.shared.HostCommandRunnerContext(ctx, showArgs, nil, 15*time.Second)
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("verify protected Host Agent resource policy on %s: %s", unit, firstHostResourceError(result.Stderr, result.Stdout, err))
	}
	properties := parseSystemdProperties(result.Stdout)
	for key, expected := range map[string]string{
		"Slice":     hostAgentProtectedSlice,
		"CPUWeight": hostAgentCPUWeight,
		"TasksMax":  hostAgentTasksMax,
		"KillMode":  "control-group",
	} {
		if properties[key] != expected {
			return fmt.Errorf("host resource policy verification failed for %s: %s=%q, want %q", unit, key, properties[key], expected)
		}
	}
	return nil
}

// ObserveHostResourceEnforcement verifies the concrete killable workload
// boundary before resource admission allows a workload to start. The Host
// Agent itself remains in the protected slice, so checking only its own
// cgroup would be insufficient: a protected process can be alive while the
// delegated workload slice is missing or unlimited.
func (s *Service) ObserveHostResourceEnforcement() string {
	if runtime.GOOS != "linux" || s == nil || s.shared == nil {
		return "unsupported"
	}
	for _, scope := range hostResourceSystemdScopes() {
		command := []string{hostruntime.DefaultSystemctlPath}
		if scope == "user" {
			command = append(command, "--user")
		}
		command = append(command, "show", hostWorkloadSlice,
			"--property=ControlGroup,MemoryHigh,MemoryMax,MemorySwapMax,CPUQuotaPerSecUSec,CPUWeight,TasksMax")
		result, err := s.shared.HostCommandRunner(command, nil, 5*time.Second)
		if err != nil || result.ExitCode != 0 {
			continue
		}
		properties := parseSystemdProperties(result.Stdout)
		if workloadSystemdPropertiesEnforced(properties) && cgroupControlsAvailable(properties["ControlGroup"]) {
			return "enforced"
		}
	}
	return "unknown"
}

func hostResourceSystemdScopes() []string {
	contents, err := os.ReadFile("/proc/self/cgroup")
	if err == nil {
		value := string(contents)
		if strings.Contains(value, "/user.slice/") {
			return []string{"user"}
		}
		if strings.Contains(value, "/system.slice/") {
			return []string{"system"}
		}
	}
	return []string{"user", "system"}
}

func workloadSystemdPropertiesEnforced(properties map[string]string) bool {
	limits := map[string]int64{
		"MemoryHigh":         5 << 30,
		"MemoryMax":          6 << 30,
		"MemorySwapMax":      1 << 30,
		"CPUQuotaPerSecUSec": 6_000_000,
		"CPUWeight":          100,
		"TasksMax":           4096,
	}
	for property, maximum := range limits {
		value, ok := parseSystemdLimit(properties[property])
		if property == "CPUQuotaPerSecUSec" {
			value, ok = parseSystemdMicroseconds(properties[property])
		}
		if !ok || value <= 0 || value > maximum {
			return false
		}
	}
	return true
}

func parseSystemdLimit(value string) (int64, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "max" || value == "infinity" || value == "inf" {
		return 0, false
	}
	multiplier := int64(1)
	for suffix, factor := range map[string]int64{"k": 1 << 10, "m": 1 << 20, "g": 1 << 30, "t": 1 << 40} {
		if strings.HasSuffix(value, suffix) {
			value = strings.TrimSpace(strings.TrimSuffix(value, suffix))
			multiplier = factor
			break
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 || parsed > int64(^uint64(0)>>1)/multiplier {
		return 0, false
	}
	return parsed * multiplier, true
}

// systemd renders CPUQuotaPerSecUSec using duration suffixes on some
// versions (for example, "6s") and as a raw microsecond count on others.
func parseSystemdMicroseconds(value string) (int64, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "max" || value == "infinity" || value == "inf" {
		return 0, false
	}
	for _, unit := range []struct {
		suffix     string
		multiplier float64
	}{
		{suffix: "us", multiplier: 1},
		{suffix: "ms", multiplier: 1_000},
		{suffix: "s", multiplier: 1_000_000},
	} {
		if !strings.HasSuffix(value, unit.suffix) {
			continue
		}
		number := strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
		parsed, err := strconv.ParseFloat(number, 64)
		if err != nil || parsed <= 0 || parsed > float64(int64(^uint64(0)>>1))/unit.multiplier {
			return 0, false
		}
		return int64(parsed * unit.multiplier), true
	}
	return parseSystemdLimit(value)
}

func cgroupControlsAvailable(controlGroup string) bool {
	controlGroup = strings.TrimSpace(controlGroup)
	if controlGroup == "" || !strings.HasPrefix(controlGroup, "/") {
		return false
	}
	root := filepath.Clean("/sys/fs/cgroup")
	relativeGroup := strings.TrimPrefix(controlGroup, "/")
	path := filepath.Clean(filepath.Join(root, filepath.FromSlash(relativeGroup)))
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	for _, name := range []string{"memory.max", "cpu.max", "pids.max"} {
		if !hostResourceFileExists(filepath.Join(path, name)) {
			return false
		}
	}
	return true
}

func hostResourceFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func parseSystemdProperties(output string) map[string]string {
	properties := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			properties[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return properties
}

func preservesStricterResourceUnit(name, contents string) bool {
	properties := parseSystemdProperties(contents)
	if name == hostAgentProtectedSlice {
		// The protected control slice must never acquire a hard memory kill
		// boundary as a side effect of reconciliation.
		for _, property := range []string{"MemoryHigh", "MemoryMax", "MemorySwapMax"} {
			if _, present := properties[property]; present {
				return false
			}
		}
		cpuWeight, cpuOK := parseSystemdLimit(properties["CPUWeight"])
		tasksMax, tasksOK := parseSystemdLimit(properties["TasksMax"])
		return cpuOK && cpuWeight >= 1000 && tasksOK && tasksMax > 0 && tasksMax <= 1024
	}
	if name != hostWorkloadSlice {
		return false
	}
	for property, maximum := range map[string]int64{
		"MemoryHigh":    5 << 30,
		"MemoryMax":     6 << 30,
		"MemorySwapMax": 1 << 30,
		"CPUQuota":      6_000_000,
		"CPUWeight":     100,
		"TasksMax":      4096,
	} {
		value, ok := parseSystemdUnitProperty(property, properties[property])
		if !ok || value <= 0 || value > maximum {
			return false
		}
	}
	return true
}

func parseSystemdUnitProperty(property, value string) (int64, bool) {
	if property == "CPUQuota" {
		value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "%"))
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed <= 0 || parsed > float64(int64(^uint64(0)>>1))/10_000 {
			return 0, false
		}
		return int64(parsed * 10_000), true
	}
	return parseSystemdLimit(value)
}

func firstHostResourceError(stderr, stdout string, err error) string {
	if value := strings.TrimSpace(stderr); value != "" {
		return value
	}
	if value := strings.TrimSpace(stdout); value != "" {
		return value
	}
	if err != nil {
		return err.Error()
	}
	return "systemd command failed"
}
