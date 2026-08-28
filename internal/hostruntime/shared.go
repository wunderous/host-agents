package hostruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/resourceid"
)

// ResourceRegistry is the persistence seam every domain needs in order to
// record what it created. It names no domain type -- only `resourceid.Record`
// -- so it satisfies the membership rule in plan sec. 9.2.
type ResourceRegistry interface {
	UpsertResource(resourceid.Record) error
	GetResource(string) (resourceid.Record, bool, error)
	DeleteResource(string) error
	ListResources(resourceType, tenantID string) ([]resourceid.Record, error)
}

// Shared is the identity, configuration, and execution-handle surface that
// every domain package needs and no domain package owns. Membership here is
// governed by plan sec. 9.2: a member names no `domain/*` type, has at least two
// domain consumers, and is a handle rather than an operation. That last part is
// why `runVMExec` is absent -- it performs an incus ownership check, which
// makes it an operation and therefore incus-owned.
type Shared struct {
	Runtime                 *Runtime
	TenantID                string
	InstanceID              string
	AgentID                 string
	OwnershipMode           string
	SharedHostOwnerInstance string
	ResourceRegistry        ResourceRegistry
	ResourceSnapshot        func() map[string]any
	CommandRunnerFn         func(args []string, onData func(string), timeout time.Duration) (hostexec.Result, error)
	ContainerLookPathFn     func(string) (string, error)
}

// EffectiveTenantID resolves the tenant every record is written under, falling
// back to the single-tenant default.
func (s *Shared) EffectiveTenantID() string {
	if s == nil || strings.TrimSpace(s.TenantID) == "" {
		return "local"
	}
	return strings.TrimSpace(s.TenantID)
}

// CommandRunner runs an argv against the provider CLI, honouring a test double
// when one is installed.
func (s *Shared) CommandRunner(args []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	if s.CommandRunnerFn != nil {
		return s.CommandRunnerFn(args, onData, timeout)
	}
	return s.Runtime.RunProvider(args, onData, timeout)
}

// HostCommandRunner runs an argv directly on the host.
func (s *Shared) HostCommandRunner(command []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.Runtime.RunHost(command, onData, timeout)
}

// HostCommandRunnerContext is HostCommandRunner with caller-owned cancellation.
func (s *Shared) HostCommandRunnerContext(ctx context.Context, command []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.Runtime.RunHostContext(ctx, command, onData, timeout)
}

// VMExecArgv builds the provider argv that executes a guest command inside a VM.
func (s *Shared) VMExecArgv(vmName string, guestArgv []string) []string {
	return append([]string{"exec", vmName, "--"}, guestArgv...)
}

// ContainerLookPath resolves a container-runtime binary, honouring a test
// double when one is installed.
func (s *Shared) ContainerLookPath(command string) (string, error) {
	if s != nil && s.ContainerLookPathFn != nil {
		return s.ContainerLookPathFn(command)
	}
	return lookPath(command)
}

// lookPath keeps binary resolution replaceable in focused unit tests without
// changing the production adapter contract.
var lookPath = func(command string) (string, error) { return exec.LookPath(command) }

// SharedHostOwnershipError reports that an operation must be performed by the
// instance that owns the shared host.
type SharedHostOwnershipError struct {
	Code             string `json:"code"`
	ExpectedInstance string `json:"expectedInstance"`
	ActualInstance   string `json:"actualInstance"`
	Operation        string `json:"operation"`
	Remediation      string `json:"remediation"`
}

func (e *SharedHostOwnershipError) Error() string {
	encoded, err := json.Marshal(e)
	if err == nil {
		return string(encoded)
	}
	return fmt.Sprintf("shared_host_ownership_required: %s must be performed by %s", e.Operation, e.ExpectedInstance)
}

// RequireSharedHostOwner rejects an operation when this instance is not the
// configured owner of the shared host.
func (s *Shared) RequireSharedHostOwner(operation string) error {
	expected := strings.TrimSpace(s.SharedHostOwnerInstance)
	if expected == "" || strings.TrimSpace(s.InstanceID) == expected {
		return nil
	}
	return &SharedHostOwnershipError{
		Code:             "shared_host_ownership_required",
		ExpectedInstance: expected,
		ActualInstance:   s.InstanceID,
		Operation:        strings.TrimSpace(operation),
		Remediation:      "Select the shared-host owner instance or use the approved operator workflow.",
	}
}
