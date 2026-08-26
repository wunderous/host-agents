package hostmcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/plan"
	"github.com/wunderous/host-agents/internal/tools"
)

const providerTeardownOperation = "opute.provider.teardown"

func (s *Server) handleProviderTeardown(args map[string]any) (*mcp.CallToolResult, error) {
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
	session, sessionErr := s.providerLifecycle.OpenSession(providerID)
	if sessionErr != nil {
		return tools.ErrorResult(sessionErr), nil
	}
	defer session.Close()
	adapter := s.providerGenerationAdapter(providerID, session.GenerationID())
	if adapter == nil {
		return tools.ErrorResult(fmt.Errorf("provider %q is not connected", providerID)), nil
	}
	prepareArgs := cloneProviderTeardownArgs(args)
	prepareArgs["phase"] = "prepare"
	providerInputs := s.providerTeardownInputs(args)
	prepareArgs["inputs"] = providerInputs
	result, err := adapter.CallSynchronousOnly(context.Background(), providerTeardownOperation, prepareArgs)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	if result == nil || result.IsError {
		return tools.ErrorResult(fmt.Errorf("provider %q teardown operation failed", providerID)), nil
	}
	object, ok := structuredObject(result.StructuredContent)
	if !ok {
		return tools.ErrorResult(fmt.Errorf("provider %q teardown returned no structured plan", providerID)), nil
	}
	if version, _ := object["contractVersion"].(string); version != "host-plan.v1" {
		return tools.ErrorResult(fmt.Errorf("provider teardown returned unsupported contract %q", version)), nil
	}
	doc, err := plan.Decode(object["plan"])
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("decode provider teardown plan: %w", err)), nil
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
	if serviceName == "" || s.ops == nil {
		return inputs
	}
	scope := recipeStringField(inputs, "scope")
	if scope == "" {
		scope = "user"
	}
	inputs["serviceUri"] = fmt.Sprintf("host-service:%s:%s/%s", s.ops.TenantID(), scope, serviceName)
	return inputs
}

func cloneProviderTeardownArgs(args map[string]any) map[string]any {
	cloned := make(map[string]any, len(args)+1)
	for key, value := range args {
		cloned[key] = value
	}
	return cloned
}

func (s *Server) completeProviderTeardown(metadata map[string]any) error {
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
	adapter := s.providerGenerationAdapter(providerID, session.GenerationID())
	if adapter == nil {
		return fmt.Errorf("provider %q is not connected for teardown finalization", providerID)
	}
	inputs, _ := metadata["providerTeardownInputs"].(map[string]any)
	if _, redacted := inputs["redacted"]; redacted {
		return fmt.Errorf("provider teardown inputs are redacted; resume requires the provider inputs to be supplied again")
	}
	finalizeInputs := make(map[string]any, len(inputs)+1)
	for key, value := range inputs {
		finalizeInputs[key] = value
	}
	finalizeInputs["phase"] = "finalize"
	finalize, err := adapter.CallSynchronousOnly(context.Background(), providerTeardownOperation, map[string]any{"phase": "finalize", "inputs": finalizeInputs})
	if err != nil {
		return fmt.Errorf("finalize provider teardown: %w", err)
	}
	if finalize == nil || finalize.IsError {
		return fmt.Errorf("provider %q teardown finalization failed", providerID)
	}
	session.Close()
	s.emitProviderLifecycleEvent(context.Background(), ProviderEventDraining, providerID, generationID, "teardown")
	if err := s.providerLifecycle.Drain(context.Background(), generationID); err != nil {
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
	s.emitProviderLifecycleEvent(context.Background(), ProviderEventStopped, providerID, generationID, "teardown")
	if s.state != nil {
		if err := s.state.RemoveActiveCapabilitiesForProvider(providerID); err != nil {
			return fmt.Errorf("clear active provider capabilities: %w", err)
		}
	}
	return nil
}
