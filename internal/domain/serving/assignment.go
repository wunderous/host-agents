package serving

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ServingAssignmentArgs is intentionally product-neutral. The host agent
// validates and executes a generic target contract without knowing the
// service's product, URLs, command, or domain vocabulary.
type ServingAssignmentArgs struct {
	ContractVersion string         `json:"contractVersion"`
	AssignmentID    string         `json:"assignmentId"`
	Generation      int            `json:"generation"`
	IdempotencyKey  string         `json:"idempotencyKey"`
	Service         string         `json:"service"`
	Mode            string         `json:"mode"`
	Runtime         string         `json:"runtime"`
	Target          map[string]any `json:"target"`
	Artifact        map[string]any `json:"artifact"`
	Endpoints       []any          `json:"endpoints"`
	Readiness       []any          `json:"readiness"`
	Exposure        map[string]any `json:"exposure"`
	ServiceUnit     string         `json:"serviceUnit,omitempty"`
	DesiredState    string         `json:"desiredState,omitempty"`
	RestartPolicy   string         `json:"restartPolicy,omitempty"`
}

var servingIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,127}$`)
var servingSystemdUnit = regexp.MustCompile(`^[A-Za-z0-9_.@:-]+$`)

// servingLaunches makes process assignments idempotent across the readiness
// polls issued by a caller.  The assignment generation is the lifecycle
// boundary: a new generation is allowed to launch once, while retries for the
// same generation only observe readiness.  The map is deliberately generic;
// the caller owns the service command and any recovery semantics.
var servingLaunches = struct {
	sync.Mutex
	started map[string]bool
}{started: make(map[string]bool)}

func servingLaunchKey(args ServingAssignmentArgs) string {
	// The caller may keep a stable generation while changing the declared
	// artifact or idempotency key (for example, a source revision). Treat that
	// contract identity as a new lifecycle boundary; otherwise a stale process
	// can remain in service forever while retries continue to report accepted.
	return fmt.Sprintf("%s:%d:%s", args.AssignmentID, args.Generation, args.IdempotencyKey)
}

func claimServingLaunch(args ServingAssignmentArgs) bool {
	key := servingLaunchKey(args)
	servingLaunches.Lock()
	defer servingLaunches.Unlock()
	if servingLaunches.started[key] {
		// A readiness poll can outlive the process it launched.  The
		// assignment is still the same desired generation, but the observed
		// process is no longer serving it.  Reclaim the in-memory launch
		// marker so reconciliation can restore the declared state.  This is
		// intentionally based on the generic assignment pid file rather than
		// any product-specific process knowledge.
		if servingProcessAlive(servingPidFile(args.AssignmentID)) {
			return false
		}
		delete(servingLaunches.started, key)
	}
	servingLaunches.started[key] = true
	return true
}

func releaseServingLaunch(args ServingAssignmentArgs) {
	servingLaunches.Lock()
	delete(servingLaunches.started, servingLaunchKey(args))
	servingLaunches.Unlock()
}

func servingString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

// MCP JSON numbers arrive as float64 after decoding into map[string]any,
// while direct contract tests and internal callers may use an integer type.
// Normalize the wire representation once so readiness and launch decisions
// observe the same endpoint contract.
func servingPort(value any) int {
	switch port := value.(type) {
	case int:
		return port
	case int8:
		return int(port)
	case int16:
		return int(port)
	case int32:
		return int(port)
	case int64:
		return int(port)
	case uint:
		return int(port)
	case uint8:
		return int(port)
	case uint16:
		return int(port)
	case uint32:
		return int(port)
	case uint64:
		return int(port)
	case float64:
		if port >= 1 && port <= 65535 && port == math.Trunc(port) {
			return int(port)
		}
	}
	return 0
}

func validateServingTarget(target map[string]any) error {
	if target == nil {
		return errors.New("target is required")
	}
	if servingString(target["hostId"]) == "" || servingString(target["resourceId"]) == "" {
		return errors.New("target.hostId and target.resourceId are required")
	}
	kind := servingString(target["kind"])
	instanceType := servingString(target["instanceType"])
	switch kind {
	case "host":
		if instanceType != "host" {
			return errors.New("host target requires instanceType=host")
		}
	case "system-container":
		if instanceType != "container" && instanceType != "system-container" {
			return errors.New("system-container target requires a container instanceType")
		}
	case "kubernetes-cluster":
		if instanceType != "kubernetes" {
			return errors.New("kubernetes-cluster target requires instanceType=kubernetes")
		}
	default:
		return fmt.Errorf("unsupported or ambiguous target kind %q", kind)
	}
	if instanceType == "vm" || strings.Contains(strings.ToLower(kind), "vm") {
		return errors.New("VM serving targets are not supported")
	}
	return nil
}

func validateServingAssignment(args ServingAssignmentArgs) error {
	if args.ContractVersion != "serving-assignment.v1" {
		return fmt.Errorf("unsupported serving assignment contract %q", args.ContractVersion)
	}
	for name, value := range map[string]string{
		"assignmentId": args.AssignmentID, "idempotencyKey": args.IdempotencyKey, "service": args.Service,
	} {
		if value == "" || !servingIdentifier.MatchString(value) {
			return fmt.Errorf("%s is required and must be a bounded identifier", name)
		}
	}
	if args.Generation < 1 {
		return errors.New("generation must be positive")
	}
	if args.Mode != "dev-process" && args.Mode != "oci-release" {
		return fmt.Errorf("unsupported serving mode %q", args.Mode)
	}
	if args.Runtime != "process" && args.Runtime != "podman" && args.Runtime != "kubernetes" {
		return fmt.Errorf("unsupported serving runtime %q", args.Runtime)
	}
	if err := validateServingTarget(args.Target); err != nil {
		return err
	}
	if args.Mode == "dev-process" && args.Runtime != "process" {
		return errors.New("dev-process requires process runtime")
	}
	if args.Mode == "oci-release" {
		if servingString(args.Artifact["kind"]) != "oci" || servingString(args.Artifact["digest"]) == "" {
			return errors.New("oci-release requires an OCI artifact digest")
		}
	}
	if args.Runtime == "process" {
		artifactKind := servingString(args.Artifact["kind"])
		if artifactKind != "source" {
			return errors.New("process runtime requires a source artifact")
		}
		command, ok := args.Artifact["command"].([]any)
		if !ok || len(command) == 0 {
			return errors.New("source artifact command is required")
		}
	}
	if len(args.Endpoints) == 0 || len(args.Readiness) == 0 {
		return errors.New("at least one endpoint and readiness check are required")
	}
	if args.Runtime == "process" {
		if args.RestartPolicy != "" && args.RestartPolicy != "no" && args.RestartPolicy != "on-failure" && args.RestartPolicy != "always" {
			return fmt.Errorf("unsupported restartPolicy %q", args.RestartPolicy)
		}
		if args.ServiceUnit != "" {
			if !servingSystemdUnit.MatchString(args.ServiceUnit) {
				return errors.New("serviceUnit must be a valid systemd unit name")
			}
			if args.DesiredState == "" {
				args.DesiredState = "restart"
			}
			if args.DesiredState != "start" && args.DesiredState != "restart" {
				return errors.New("desiredState must be start or restart when serviceUnit is provided")
			}
		}
	}
	return nil
}

func normalizedServingRestartPolicy(args ServingAssignmentArgs) string {
	if args.RestartPolicy == "" {
		return "no"
	}
	return args.RestartPolicy
}

func probeServingEndpoint(protocol string, port int, path string) (bool, string) {
	if port < 1 || port > 65535 {
		return false, "invalid port"
	}
	if protocol == "tcp" {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
		if err != nil {
			return false, err.Error()
		}
		_ = conn.Close()
		return true, "tcp connection accepted"
	}
	if protocol != "http" && protocol != "https" {
		return true, "readiness delegated to runtime"
	}
	if path == "" {
		path = "/"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(fmt.Sprintf("%s://127.0.0.1:%d%s", protocol, port, path))
	if err != nil {
		return false, err.Error()
	}
	defer response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 500, fmt.Sprintf("HTTP %d", response.StatusCode)
}

func shellQuoteServing(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func servingPidFile(assignmentID string) string {
	return "/tmp/serving-assignment-" + servingFileToken(assignmentID) + ".pid"
}

func servingAssignmentStateFile(assignmentID string) string {
	return "/tmp/serving-assignment-" + servingFileToken(assignmentID) + ".state.json"
}

type servingAssignmentState struct {
	ContractVersion string `json:"contractVersion"`
	AssignmentID    string `json:"assignmentId"`
	Generation      int    `json:"generation"`
	IdempotencyKey  string `json:"idempotencyKey"`
}

func servingAssignmentStateMatches(args ServingAssignmentArgs) bool {
	data, err := os.ReadFile(servingAssignmentStateFile(args.AssignmentID))
	if err != nil {
		return false
	}
	var state servingAssignmentState
	if err := json.Unmarshal(data, &state); err != nil {
		return false
	}
	return state.ContractVersion == args.ContractVersion &&
		state.AssignmentID == args.AssignmentID &&
		state.Generation == args.Generation &&
		state.IdempotencyKey == args.IdempotencyKey
}

func recordServingAssignmentState(args ServingAssignmentArgs) error {
	data, err := json.Marshal(servingAssignmentState{
		ContractVersion: args.ContractVersion,
		AssignmentID:    args.AssignmentID,
		Generation:      args.Generation,
		IdempotencyKey:  args.IdempotencyKey,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(servingAssignmentStateFile(args.AssignmentID), data, 0600)
}

func servingTransientUnit(assignmentID string) string {
	// Assignment IDs are already bounded by validateServingAssignment. Keep the
	// derived unit name independently safe because it is passed to systemd.
	return "host-serving-" + servingFileToken(assignmentID)
}

func servingFileToken(value string) string {
	return strings.NewReplacer("/", "-", ":", "-", "@", "-", ".", "-").Replace(value)
}

func servingSystemdUnitActive(unit string) bool {
	return exec.Command("systemctl", "--user", "is-active", "--quiet", unit).Run() == nil
}

func servingLaunchCommand(pidFile, assignmentID, command, restartPolicy string) string {
	unit := servingTransientUnit(assignmentID)
	logToken := servingFileToken(assignmentID)
	resourceProperties := " --property=Slice=opute-workload.slice --property=MemoryHigh=5G --property=MemoryMax=6G --property=MemorySwapMax=1G --property=CPUQuota=600% --property=CPUWeight=100 --property=TasksMax=4096 --property=StartLimitIntervalSec=60s --property=StartLimitBurst=5"
	restartProperties := ""
	if restartPolicy == "on-failure" || restartPolicy == "always" {
		restartProperties = " --property=Restart=" + restartPolicy + " --property=RestartSec=2s"
	}
	return fmt.Sprintf(
		"pidfile=%s; unit=%s; systemctl --user stop \"$unit\" >/dev/null 2>&1 || true; systemctl --user reset-failed \"$unit\" >/dev/null 2>&1 || true; rm -f \"$pidfile\"; systemd-run --user --unit=\"$unit\" --collect --no-block --property=KillMode=control-group%s%s /bin/bash -lic %s >/tmp/serving-assignment-%s-supervisor.log 2>&1; for _ in $(seq 1 50); do pid=$(systemctl --user show \"$unit\" -p MainPID --value 2>/dev/null || true); if [[ \"$pid\" =~ ^[1-9][0-9]*$ ]] && kill -0 \"$pid\" 2>/dev/null; then printf '%%s\\n' \"$pid\" >\"$pidfile\"; exit 0; fi; sleep 0.1; done; echo \"serving supervisor did not expose a live main PID for $unit\" >&2; systemctl --user status \"$unit\" --no-pager 2>&1 || true; exit 1",
		shellQuoteServing(pidFile),
		shellQuoteServing(unit),
		resourceProperties,
		restartProperties,
		shellQuoteServing(command),
		logToken,
	)
}

func servingProcessAlive(pidFile string) bool {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid < 1 {
		return false
	}
	return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil
}

func servingArtifactCommand(artifact map[string]any) (string, string, error) {
	sourceDir := servingString(artifact["sourceDir"])
	if sourceDir == "" {
		return "", "", errors.New("source artifact sourceDir is required")
	}
	workingDir := servingString(artifact["workingDir"])
	if workingDir == "" {
		workingDir = sourceDir
	}
	rawCommand, ok := artifact["command"].([]any)
	if !ok || len(rawCommand) == 0 {
		return "", "", errors.New("source artifact command is required")
	}
	command := make([]string, 0, len(rawCommand))
	for _, raw := range rawCommand {
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return "", "", errors.New("source artifact command entries must be non-empty strings")
		}
		command = append(command, shellQuoteServing(value))
	}
	environment := ""
	if rawEnvironment, ok := artifact["environment"].(map[string]any); ok {
		for key, rawValue := range rawEnvironment {
			if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(key) {
				return "", "", fmt.Errorf("source artifact environment key %q is invalid", key)
			}
			value, ok := rawValue.(string)
			if !ok {
				return "", "", fmt.Errorf("source artifact environment value %q must be a string", key)
			}
			environment += " export " + key + "=" + shellQuoteServing(value) + ";"
		}
	}
	return fmt.Sprintf("%scd %s && exec %s", environment, shellQuoteServing(workingDir), strings.Join(command, " ")), workingDir, nil
}

// ReconcileServingAssignment validates an assignment and, for host process
// services, applies the requested generic systemd lifecycle transition. OCI
// and Kubernetes deployment mutations remain composed from the generic
// build/push/apply primitives; this call still provides the common admission
// and readiness contract for those runtimes.
func (s *Service) ReconcileServingAssignment(args ServingAssignmentArgs, onData func(string)) (map[string]any, error) {
	if err := validateServingAssignment(args); err != nil {
		return nil, err
	}
	result := map[string]any{
		"contractVersion": args.ContractVersion,
		"assignmentId":    args.AssignmentID,
		"generation":      args.Generation,
		"idempotencyKey":  args.IdempotencyKey,
		"service":         args.Service,
		"runtime":         args.Runtime,
		"status":          "accepted",
	}
	if args.Runtime == "process" {
		restartPolicy := normalizedServingRestartPolicy(args)
		result["restartPolicy"] = restartPolicy
		command, _, err := servingArtifactCommand(args.Artifact)
		if err != nil {
			return nil, err
		}
		allReady := true
		for _, raw := range args.Readiness {
			check, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("readiness entries must be objects")
			}
			endpointName := servingString(check["endpointName"])
			path := servingString(check["path"])
			for _, endpointRaw := range args.Endpoints {
				endpoint, endpointOK := endpointRaw.(map[string]any)
				if endpointOK && servingString(endpoint["name"]) == endpointName {
					port := servingPort(endpoint["port"])
					protocol := servingString(endpoint["protocol"])
					ready, _ := probeServingEndpoint(protocol, port, path)
					allReady = allReady && ready
				}
			}
		}
		pidFile := servingPidFile(args.AssignmentID)
		preStart := servingString(args.Artifact["preStartCommand"])
		managedUnitActive := servingSystemdUnitActive(servingTransientUnit(args.AssignmentID))
		adoptUnmanagedReadyService := allReady && !managedUnitActive
		if adoptUnmanagedReadyService && !servingProcessAlive(pidFile) && preStart == "" {
			return nil, errors.New("declared serving endpoint is ready but is not owned by its host-systemd supervisor; preStartCommand is required for explicit adoption")
		}
		// A healthy declared endpoint is already the desired state only when its
		// assignment-scoped supervisor is active. An endpoint left behind by a
		// prior launcher must be explicitly adopted before the host agent can
		// claim the serving contract; otherwise an agent restart can kill the
		// process while the route still appears ready.
		assignmentChanged := !servingAssignmentStateMatches(args)
		if !allReady || adoptUnmanagedReadyService || assignmentChanged {
			// A managed systemd unit is the lifecycle owner. During a bounded
			// restart or warm-up, let its declared restart policy converge rather
			// than launching a second process from the reconciliation caller.
			if managedUnitActive && !assignmentChanged && !adoptUnmanagedReadyService {
				result["starting"] = true
			} else {
				// A process can be alive while its declared endpoints are between
				// hot-reload generations or still warming up. Do not let a second
				// assignment identity launch another copy during that interval:
				// generic source runtimes commonly use a port guard that terminates
				// the first owner when the duplicate starts. A later reconciliation
				// can retry once the owner exits or the endpoints become ready.
				pidAlive := servingProcessAlive(pidFile)
				if pidAlive && !adoptUnmanagedReadyService && (!assignmentChanged || !managedUnitActive) {
					result["starting"] = true
				} else if claimServingLaunch(args) {
					if !pidAlive {
						_ = os.Remove(pidFile)
					}
					if adoptUnmanagedReadyService && preStart != "" {
						if err := s.deps.RunAgentShell(preStart, onData); err != nil {
							releaseServingLaunch(args)
							return nil, fmt.Errorf("source artifact pre-start command failed: %w", err)
						}
					}
					// Run the caller-declared command in the host user's normal login
					// environment. This keeps generic source assignments usable when the
					// host runtime is installed through the user's profile rather than
					// the system PATH.
					// Execute the caller-declared environment in the same shell as the
					// process. The assignment is the generic service boundary; losing
					// these values here would make a valid service appear configured while
					// its child observes a different runtime contract.
					// The user-systemd unit is the generic lifecycle owner. Its
					// control-group kill mode stops the complete assignment tree and it
					// survives a Host Agent process restart while retaining
					// assignment-scoped replacement semantics.
					launch := servingLaunchCommand(pidFile, args.AssignmentID, command, restartPolicy)
					if err := s.deps.RunAgentShell(launch, onData); err != nil {
						releaseServingLaunch(args)
						return nil, err
					}
					if err := recordServingAssignmentState(args); err != nil {
						releaseServingLaunch(args)
						return nil, fmt.Errorf("record serving assignment state: %w", err)
					}
					result["started"] = true
				} else {
					result["starting"] = true
				}
			}
		}
	}

	if args.Runtime == "process" && args.ServiceUnit != "" {
		serviceResult, err := s.deps.SetHostServiceState(args.ServiceUnit, args.DesiredState, "user", onData)
		if err != nil {
			return nil, err
		}
		result["service"] = serviceResult
	}
	readiness := make([]map[string]any, 0, len(args.Endpoints))
	for _, raw := range args.Endpoints {
		endpoint, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("endpoint entries must be objects")
		}
		name := servingString(endpoint["name"])
		protocol := servingString(endpoint["protocol"])
		port := servingPort(endpoint["port"])
		path := "/"
		for _, rawCheck := range args.Readiness {
			check, checkOK := rawCheck.(map[string]any)
			if checkOK && servingString(check["endpointName"]) == name && servingString(check["path"]) != "" {
				path = servingString(check["path"])
			}
		}
		ready, message := probeServingEndpoint(protocol, port, path)
		readiness = append(readiness, map[string]any{"name": name, "ready": ready, "message": message})
	}
	result["readiness"] = readiness
	result["readinessSource"] = "host-agent-local-probe"
	return result, nil
}
