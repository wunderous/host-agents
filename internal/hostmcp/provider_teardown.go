package hostmcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/cordis"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/plan"
	"github.com/wunderous/host-agents/internal/tools"
)

const providerTeardownOperation = "opute.provider.teardown"

func (s *Server) handleProviderTeardown(args map[string]any) (*mcp.CallToolResult, error) {
	return s.handleProviderTeardownContext(context.Background(), args)
}

func (s *Server) handleProviderTeardownContext(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	providerID := recipeStringField(args, "provider")
	if providerID == "" {
		return tools.ErrorResult(fmt.Errorf("provider is required")), nil
	}
	if !recipeBoolField(args, "confirm") {
		return tools.ErrorResult(fmt.Errorf("provider teardown requires confirm=true")), nil
	}
	active, ok := s.providerLifecycle.Active(providerID)
	if !ok {
		return tools.ErrorResult(fmt.Errorf("provider %q has no active generation", providerID)), nil
	}
	if generation := recipeStringField(args, "generation"); generation != "" && generation != active.ID {
		return tools.ErrorResult(fmt.Errorf("provider generation %q is not active; active generation is %q", generation, active.ID)), nil
	}
	forced := recipeBoolField(args, "force")
	session, sessionErr := s.providerLifecycle.OpenSession(providerID)
	if sessionErr != nil {
		return tools.ErrorResult(sessionErr), nil
	}
	defer session.Close()
	providerInputs := s.providerTeardownInputs(args)
	doc, blocked := s.prepareProviderTeardownPlan(ctx, providerID, session.GenerationID(), args, providerInputs)
	if blocked != "" {
		if !forced {
			return tools.ErrorResult(errors.New(blocked)), nil
		}
		// The plan runner may finalize the provider before this handler
		// returns; release the preparation session before handing control to it.
		session.Close()
		return s.runForcedProviderReclaim(active, providerInputs, recipeBoolField(args, "resume"), blocked)
	}
	metadata := map[string]any{
		"providerTeardown":        true,
		"providerId":              active.Provider.ID,
		"providerVersion":         active.Provider.Version,
		"providerGenerationId":    active.ID,
		"teardownContractVersion": "provider-teardown.v1",
		"providerTeardownInputs":  redactTaskValue(providerInputs),
	}
	// The plan runner may finalize the provider before this handler returns;
	// release the preparation session before handing control to it.
	session.Close()
	return s.handleRunHostPlanWithMetadata(map[string]any{"plan": doc, "resume": recipeBoolField(args, "resume")}, metadata, "opute.provider.teardown", "Tearing down provider...")
}

// prepareProviderTeardownPlan runs the provider's own prepare phase and
// returns the host plan it declared. A non-empty second return value is the
// reason the provider could not declare one; it is a sentence rather than an
// error because a forced reclaim records it as evidence instead of raising it.
func (s *Server) prepareProviderTeardownPlan(ctx context.Context, providerID, generationID string, args, providerInputs map[string]any) (plan.Document, string) {
	adapter := s.providerGenerationAdapter(providerID, generationID)
	if adapter == nil {
		return plan.Document{}, fmt.Sprintf("provider %q is not connected", providerID)
	}
	prepareArgs := cloneProviderTeardownArgs(args)
	prepareArgs["phase"] = "prepare"
	prepareArgs["inputs"] = providerInputs
	result, err := adapter.CallSynchronousOnly(ctx, providerTeardownOperation, prepareArgs)
	if err != nil {
		return plan.Document{}, err.Error()
	}
	if result == nil || result.IsError {
		return plan.Document{}, fmt.Sprintf("provider %q teardown operation failed", providerID)
	}
	object, ok := structuredObject(result.StructuredContent)
	if !ok {
		return plan.Document{}, fmt.Sprintf("provider %q teardown returned no structured plan", providerID)
	}
	if version, _ := object["contractVersion"].(string); version != "host-plan.v1" {
		return plan.Document{}, fmt.Sprintf("provider teardown returned unsupported contract %q", version)
	}
	doc, err := plan.Decode(object["plan"])
	if err != nil {
		return plan.Document{}, fmt.Sprintf("decode provider teardown plan: %v", err)
	}
	return doc, ""
}

// runForcedProviderReclaim retires a generation whose provider can no longer
// take part in its own teardown.
//
// opute.provider.teardown dispatches the whole operation to the provider
// process, so a generation whose process is gone could not be torn down at
// all: prepare failed with "is not connected", nothing was retired, and the
// generation kept reporting active -- and because provider status derives
// `connected` from the same adapter lookup that just failed, there was no
// typed way out of that state. `force: true` is that way out. It is not a
// bypass of the provider contract: the provider is still asked first, and
// force only takes effect once prepare has actually failed, with the failure
// recorded in the run metadata as the reason the callback was skipped.
//
// The reclaim runs through the same durable plan runner as a cooperative
// teardown, so it yields the same runId/status/catalogRevision evidence and
// the same completion hook retires the generation. What it cannot do is
// finalize the provider's external resources -- only the provider knows those
// -- so anything it created beyond this host survives the reclaim and remains
// the operator's to remove.
func (s *Server) runForcedProviderReclaim(active cordis.ProviderGeneration, providerInputs map[string]any, resume bool, blocked string) (*mcp.CallToolResult, error) {
	doc, err := plan.Decode(forcedProviderReclaimPlan(active, providerInputs))
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("build forced provider reclaim plan: %w", err)), nil
	}
	metadata := map[string]any{
		"providerTeardown":             true,
		"providerTeardownForced":       true,
		"providerTeardownForcedReason": blocked,
		"providerId":                   active.Provider.ID,
		"providerVersion":              active.Provider.Version,
		"providerGenerationId":         active.ID,
		"teardownContractVersion":      "provider-teardown.v1",
		"providerTeardownInputs":       redactTaskValue(providerInputs),
	}
	return s.handleRunHostPlanWithMetadata(map[string]any{"plan": doc, "resume": resume}, metadata, "opute.provider.teardown", "Reclaiming orphaned provider generation...")
}

// forcedProviderReclaimPlan is the host's stand-in for the plan a reachable
// provider would have declared. It mirrors the shape providers actually
// return -- one read node recording the service identity being reclaimed,
// with the stop/disable/remove work left to cleanupProviderHostService once
// the run completes. The node continues on failure because the unit it reads
// is the one whose process is gone and may already be half-removed; the node
// is there for the evidence, not as a gate.
func forcedProviderReclaimPlan(active cordis.ProviderGeneration, providerInputs map[string]any) map[string]any {
	scope := recipeStringField(providerInputs, "scope")
	if scope == "" {
		scope = "user"
	}
	node := map[string]any{
		"id":                "list-host-services",
		"action":            map[string]any{"tool": "list_host_services", "args": map[string]any{"scope": scope}},
		"continueOnFailure": true,
	}
	// inspect_host_service selects its unit by canonical tenant-scoped URI, the
	// same one providerTeardownInputs resolves for the provider callback; a
	// display name is not a target.
	if serviceURI := recipeStringField(providerInputs, "serviceUri"); serviceURI != "" {
		node = map[string]any{
			"id":                "inspect-orphaned-provider-service",
			"action":            map[string]any{"tool": "inspect_host_service", "args": map[string]any{"uri": serviceURI, "scope": scope}},
			"continueOnFailure": true,
		}
	}
	return map[string]any{
		"contractVersion": "host-plan.v1",
		"planId":          "com.opute.host.provider-reclaim",
		"generation":      1,
		"idempotencyKey":  "com.opute.host.provider-reclaim-" + active.ID,
		"nodes":           []any{node},
	}
}

// providerTeardownInputs adds the host-resolved service URI to the neutral
// provider callback. Providers must execute service mutations through the
// canonical tenant-scoped URI; they must not reconstruct a target from a
// display name or assume the local tenant.
func (s *Server) providerTeardownInputs(args map[string]any) map[string]any {
	inputs := map[string]any{}
	if raw, ok := args["inputs"].(map[string]any); ok {
		for key, value := range raw {
			inputs[key] = value
		}
	}
	serviceName := recipeStringField(inputs, "serviceName")
	if serviceName == "" || s.agent == nil {
		return inputs
	}
	scope := recipeStringField(inputs, "scope")
	if scope == "" {
		scope = "user"
	}
	inputs["serviceUri"] = fmt.Sprintf("host-service:%s:%s/%s", s.agent.TenantID(), scope, serviceName)
	return inputs
}

func cloneProviderTeardownArgs(args map[string]any) map[string]any {
	cloned := make(map[string]any, len(args)+1)
	for key, value := range args {
		cloned[key] = value
	}
	return cloned
}

// cleanupProviderHostService performs the generic host-owned portion of
// provider teardown after the provider callback has finalized its external
// resources. Provider adapters must stay reachable until finalize returns;
// stopping their service from the prepare plan made the final callback fail
// with connection refused. The operation is ordered to preserve retryability:
// disable and remove the unit first, then stop the still-running process.
func (s *Server) cleanupProviderHostService(inputs map[string]any) error {
	if s == nil || s.agent == nil {
		return fmt.Errorf("host service cleanup requires an agent service")
	}
	serviceName := recipeStringField(inputs, "serviceName")
	if serviceName == "" {
		return nil
	}
	scope := recipeStringField(inputs, "scope")
	if scope == "" {
		scope = "user"
	}
	serviceFile := recipeStringField(inputs, "serviceFile")
	if serviceFile == "" {
		if scope == "system" {
			serviceFile = "/etc/systemd/system/" + serviceName
		} else {
			serviceFile = "~/.config/systemd/user/" + serviceName
		}
	}
	hostService := s.agent.Host()
	observed, err := hostService.InspectHostService(hostagent.InspectHostServiceArgs{ServiceName: serviceName, Scope: scope}, nil)
	if err != nil {
		return fmt.Errorf("inspect provider service %q: %w", serviceName, err)
	}
	active, activeOK := observed["active"].(bool)
	enabled, enabledOK := observed["enabled"].(bool)
	if !activeOK || !enabledOK {
		return fmt.Errorf("inspect provider service %q returned incomplete state", serviceName)
	}
	if enabled {
		if _, err := hostService.SetHostServiceState(hostagent.SetHostServiceStateArgs{ServiceName: serviceName, State: "disable", Scope: scope}, nil); err != nil {
			return fmt.Errorf("disable provider service %q: %w", serviceName, err)
		}
	}
	if _, err := hostService.RemoveHostFile(hostagent.RemoveHostFileArgs{Path: serviceFile, Confirm: true, Scope: scope}); err != nil {
		return fmt.Errorf("remove provider service unit %q: %w", serviceFile, err)
	}
	if err := hostService.ReloadHostServiceManager(scope); err != nil {
		return fmt.Errorf("reload provider service manager: %w", err)
	}
	if active {
		if _, err := hostService.SetHostServiceState(hostagent.SetHostServiceStateArgs{ServiceName: serviceName, State: "stop", Scope: scope}, nil); err != nil {
			return fmt.Errorf("stop provider service %q: %w", serviceName, err)
		}
	}
	return nil
}

func (s *Server) completeProviderTeardown(metadata map[string]any) error {
	return s.completeProviderTeardownContext(context.Background(), metadata)
}

func (s *Server) completeProviderTeardownContext(ctx context.Context, metadata map[string]any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	providerID := recipeStringField(metadata, "providerId")
	generationID := recipeStringField(metadata, "providerGenerationId")
	if providerID == "" || generationID == "" {
		return fmt.Errorf("provider teardown metadata is incomplete")
	}
	session, sessionErr := s.providerLifecycle.OpenSession(providerID)
	if sessionErr != nil {
		return fmt.Errorf("open provider teardown session: %w", sessionErr)
	}
	defer session.Close()
	if session.GenerationID() != generationID {
		session.Close()
		return fmt.Errorf("provider teardown generation %q is no longer active", generationID)
	}
	inputs, _ := metadata["providerTeardownInputs"].(map[string]any)
	if _, redacted := inputs["redacted"]; redacted {
		return fmt.Errorf("provider teardown inputs are redacted; resume requires the provider inputs to be supplied again")
	}
	// A forced reclaim has already established that the provider cannot answer
	// -- the reason is in providerTeardownForcedReason -- so there is nothing to
	// finalize and no adapter to require. Everything below it is host-owned and
	// runs identically either way.
	if !recipeBoolField(metadata, "providerTeardownForced") {
		adapter := s.providerGenerationAdapter(providerID, session.GenerationID())
		if adapter == nil {
			return fmt.Errorf("provider %q is not connected for teardown finalization", providerID)
		}
		finalizeInputs := make(map[string]any, len(inputs)+1)
		for key, value := range inputs {
			finalizeInputs[key] = value
		}
		finalizeInputs["phase"] = "finalize"
		finalize, err := adapter.CallSynchronousOnly(ctx, providerTeardownOperation, map[string]any{"phase": "finalize", "inputs": finalizeInputs})
		if err != nil {
			return fmt.Errorf("finalize provider teardown: %w", err)
		}
		if finalize == nil || finalize.IsError {
			return fmt.Errorf("provider %q teardown finalization failed", providerID)
		}
	}
	if err := s.cleanupProviderHostService(inputs); err != nil {
		return err
	}
	session.Close()
	s.emitProviderLifecycleEvent(ctx, ProviderEventDraining, providerID, generationID, "teardown")
	if err := s.providerLifecycle.Drain(ctx, generationID); err != nil {
		return fmt.Errorf("drain provider generation: %w", err)
	}
	if generation, ok := s.providerLifecycle.Get(generationID); ok {
		if err := s.persistProviderGeneration(generation); err != nil {
			return fmt.Errorf("persist stopped provider generation: %w", err)
		}
	}
	// Disposing the generation's fibers closes the adapter the mount owns;
	// there is no second close authority here.
	if err := s.unmountProviderGeneration(providerID, generationID); err != nil {
		return fmt.Errorf("close provider adapter: %w", err)
	}
	s.providerMu.Lock()
	delete(s.providerValidation, providerID)
	s.providerMu.Unlock()
	s.retireProviderCapabilities(providerID, generationID)
	s.emitProviderLifecycleEvent(ctx, ProviderEventStopped, providerID, generationID, "teardown")
	if s.state != nil {
		if err := s.state.RemoveActiveCapabilitiesForProvider(providerID); err != nil {
			return fmt.Errorf("clear active provider capabilities: %w", err)
		}
	}
	return nil
}
