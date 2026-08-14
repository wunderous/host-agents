package ops

import (
	"errors"
	"fmt"
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
	return "/tmp/serving-assignment-" + assignmentID + ".pid"
}

func servingTransientUnit(assignmentID string) string {
	// Assignment IDs are already bounded by validateServingAssignment. Keep the
	// derived unit name independently safe because it is passed to systemd.
	return "opute-serving-" + strings.NewReplacer("/", "-", ":", "-", "@", "-", ".", "-").Replace(assignmentID)
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
func (s *HostOperationsService) ReconcileServingAssignment(args ServingAssignmentArgs, onData func(string)) (map[string]any, error) {
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
					port, _ := endpoint["port"].(int)
					protocol := servingString(endpoint["protocol"])
					ready, _ := probeServingEndpoint(protocol, port, path)
					allReady = allReady && ready
				}
			}
		}
		// A healthy declared endpoint is already the desired state. This is
		// important for hot-reloading source services: a route reconciliation
		// must not start a second process and let its port guard terminate the
		// existing owner. Launch only when the declared surface is unavailable.
		if !allReady {
			pidFile := servingPidFile(args.AssignmentID)
			// A process can be alive while its declared endpoints are between
			// hot-reload generations or still warming up. Do not let a second
			// assignment identity launch another copy during that interval:
			// generic source runtimes commonly use a port guard that terminates
			// the first owner when the duplicate starts. A later reconciliation
			// can retry once the owner exits or the endpoints become ready.
			if servingProcessAlive(pidFile) {
				result["starting"] = true
			} else if claimServingLaunch(args) {
				_ = os.Remove(pidFile)
				if preStart := servingString(args.Artifact["preStartCommand"]); preStart != "" {
					if _, err := s.RunAgentShell(preStart, onData); err != nil {
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
				// Own a process session per assignment generation. Killing only the
				// launcher PID leaves a service's watcher/worker tree alive after a
				// source revision changes, so the next generation can keep serving old
				// code and occupy the same endpoints. `setsid` makes the recorded PID
				// the process-group leader; the group kill is generic and does not
				// depend on the caller's runtime or service implementation.
				// Keep the process outside the transient user-systemd manager. On
				// hosts where that manager is session-scoped, reconciling another
				// user service would otherwise terminate this unrelated serving
				// assignment. The pidfile and process group remain assignment-scoped
				// and are sufficient for idempotent restart/cleanup.
				launch := fmt.Sprintf("pidfile=%s; unit=%s; systemctl --user stop \"$unit\" >/dev/null 2>&1 || true; terminate_tree() { target=\"$1\"; for child in $(pgrep -P \"$target\" 2>/dev/null || true); do terminate_tree \"$child\"; done; kill -TERM \"$target\" 2>/dev/null || true; }; if test -s \"$pidfile\"; then old=$(cat \"$pidfile\"); kill -TERM -- -\"$old\" 2>/dev/null || true; terminate_tree \"$old\"; fi; nohup setsid bash -lic %s >/tmp/serving-assignment-%s.log 2>&1 < /dev/null & echo $! >\"$pidfile\"", shellQuoteServing(pidFile), shellQuoteServing(servingTransientUnit(args.AssignmentID)), shellQuoteServing(command), args.AssignmentID)
				if _, err := s.RunAgentShell(launch, onData); err != nil {
					releaseServingLaunch(args)
					return nil, err
				}
				result["started"] = true
			} else {
				result["starting"] = true
			}
		}
	}
	if args.Runtime == "process" && args.ServiceUnit != "" {
		serviceResult, err := s.SetHostServiceState(SetHostServiceStateArgs{
			ServiceName: args.ServiceUnit,
			State:       args.DesiredState,
			Scope:       "user",
		}, onData)
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
		port, ok := endpoint["port"].(float64)
		if !ok {
			if integer, integerOK := endpoint["port"].(int); integerOK {
				port = float64(integer)
			}
		}
		path := "/"
		for _, rawCheck := range args.Readiness {
			check, checkOK := rawCheck.(map[string]any)
			if checkOK && servingString(check["endpointName"]) == name && servingString(check["path"]) != "" {
				path = servingString(check["path"])
			}
		}
		ready, message := probeServingEndpoint(protocol, int(port), path)
		readiness = append(readiness, map[string]any{"name": name, "ready": ready, "message": message})
	}
	result["readiness"] = readiness
	result["readinessSource"] = "host-agent-local-probe"
	return result, nil
}
