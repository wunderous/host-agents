package hostruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	HostCommandRunnerFn     func(command []string, onData func(string), timeout time.Duration) (hostexec.Result, error)
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
	if s != nil && s.HostCommandRunnerFn != nil {
		return s.HostCommandRunnerFn(command, onData, timeout)
	}
	return s.Runtime.RunHost(command, onData, timeout)
}

// HostCommandRunnerContext is HostCommandRunner with caller-owned cancellation.
func (s *Shared) HostCommandRunnerContext(ctx context.Context, command []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return hostexec.Result{}, ctx.Err()
		default:
		}
	}
	if s != nil && s.HostCommandRunnerFn != nil {
		return s.HostCommandRunnerFn(command, onData, timeout)
	}
	return s.Runtime.RunHostContext(ctx, command, onData, timeout)
}

// HostWorkloadRunnerContext is the bounded, killable host-workload seam. It
// is intentionally separate from HostCommandRunnerContext so lifecycle and
// diagnostic commands do not accidentally move into a killable cgroup.
func (s *Shared) HostWorkloadRunnerContext(ctx context.Context, scope string, command []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
	return s.Runtime.RunHostWorkloadContext(ctx, scope, command, onData, timeout)
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

// Coordinates is a resolved resource identity: the URI plus whatever the
// registry recorded about where the thing actually lives. It names only
// resourceid types, so it crosses domain boundaries without dragging any
// domain's model with it.
type Coordinates struct {
	URI          resourceid.URI
	ResourceType string
	TenantID     string
	ResourceID   string
	Values       map[string]any
}

// RegisterResource records a resource under the active tenant.
//
// This and the two below are registry bookkeeping, not resolution: they parse a
// URI, check the tenant, and touch the registry. Resolution is a different
// thing -- it may have to ASK a domain whether an instance exists -- so
// ResolveResource stays out of hostruntime under S9.2 rule 3.
func (s *Shared) RegisterResource(uri string, coordinates map[string]any) error {
	parsed, err := s.parseOwnedURI(uri)
	if err != nil {
		return err
	}
	if s.ResourceRegistry == nil {
		return errors.New("resource registry is not configured")
	}
	return s.ResourceRegistry.UpsertResource(resourceid.Record{
		URI: parsed.String(), ResourceType: parsed.ResourceType, TenantID: parsed.TenantID,
		ResourceID: parsed.ResourceID, Coordinates: coordinates, Status: "active",
	})
}

// DeregisterResource removes a resource owned by the active tenant.
func (s *Shared) DeregisterResource(uri string) error {
	parsed, err := s.parseOwnedURI(uri)
	if err != nil {
		return err
	}
	if s.ResourceRegistry == nil {
		return errors.New("resource registry is not configured")
	}
	return s.ResourceRegistry.DeleteResource(parsed.String())
}

// ResourceURIForProviderName reverse-maps a provider's own instance name back
// to the URI it was registered under.
func (s *Shared) ResourceURIForProviderName(providerName string) string {
	if s == nil || s.ResourceRegistry == nil {
		return ""
	}
	records, err := s.ResourceRegistry.ListResources("", s.TenantID)
	if err != nil {
		return ""
	}
	for _, record := range records {
		if value, ok := record.Coordinates["providerInstanceName"].(string); ok && value == providerName {
			return record.URI
		}
	}
	return ""
}

// parseOwnedURI parses a resource URI and rejects one belonging to another
// tenant. Every registry entry point needs both checks, in that order.
func (s *Shared) parseOwnedURI(uri string) (resourceid.URI, error) {
	parsed, err := resourceid.Parse(uri)
	if err != nil {
		return resourceid.URI{}, err
	}
	if tenant := strings.TrimSpace(s.TenantID); tenant != "" && parsed.TenantID != tenant {
		return resourceid.URI{}, fmt.Errorf("%w: active tenant %q", resourceid.ErrForeignTenant, tenant)
	}
	return parsed, nil
}

// EnvOr reads an environment variable, falling back when it is unset or blank.
//
// A hostruntime member under S9.2: process environment is configuration, every
// domain reads some of it, and reading a variable is not an operation.
func EnvOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// Adopter observes a resource that the registry does not yet know about and
// returns the coordinates to record for it, or nil to leave it unresolved.
//
// It exists so resolution can be split cleanly: parsing, tenant checking, and
// the registry lookup are hostruntime's, while OBSERVING that a VM or a systemd
// unit really exists is a domain operation and stays out (S9.2 rule 3).
type Adopter func(parsed resourceid.URI) (map[string]any, error)

// ResolveResource turns a URI into coordinates, consulting adopt only when the
// registry has no active record for it. A nil adopt means registry-only
// resolution, which is what a caller with no domain to ask should use.
func (s *Shared) ResolveResource(uri, wantType string, adopt Adopter) (Coordinates, error) {
	parsed, err := s.parseOwnedURI(uri)
	if err != nil {
		return Coordinates{}, err
	}
	if wantType != "" && parsed.ResourceType != wantType {
		return Coordinates{}, fmt.Errorf("%w: expected %q, got %q", resourceid.ErrInvalidURI, wantType, parsed.ResourceType)
	}
	if s.ResourceRegistry == nil {
		return Coordinates{}, errors.New("resource registry is not configured")
	}
	record, found, err := s.ResourceRegistry.GetResource(parsed.String())
	if err != nil {
		return Coordinates{}, fmt.Errorf("resolve resource %s: %w", parsed, err)
	}
	if (!found || record.Status != "active") && adopt != nil {
		coordinates, adoptErr := adopt(parsed)
		if adoptErr != nil {
			return Coordinates{}, adoptErr
		}
		if coordinates != nil {
			if registerErr := s.RegisterResource(parsed.String(), coordinates); registerErr != nil {
				return Coordinates{}, registerErr
			}
			if record, found, err = s.ResourceRegistry.GetResource(parsed.String()); err != nil {
				return Coordinates{}, err
			}
		}
	}
	if !found || record.Status != "active" {
		return Coordinates{}, fmt.Errorf("resource not found: %s", parsed)
	}
	return Coordinates{
		URI: parsed, ResourceType: parsed.ResourceType, TenantID: parsed.TenantID,
		ResourceID: parsed.ResourceID, Values: record.Coordinates,
	}, nil
}
