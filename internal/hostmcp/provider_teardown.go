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
	s.providerMu.RLock()
	adapter := s.providerAdapters[providerID]
	s.providerMu.RUnlock()
	if adapter == nil {
		return tools.ErrorResult(fmt.Errorf("provider %q is not connected", providerID)), nil
	}
	prepareArgs := cloneProviderTeardownArgs(args)
	prepareArgs["phase"] = "prepare"
	result, err := adapter.Call(context.Background(), providerTeardownOperation, prepareArgs)
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
		"providerTeardownInputs":  redactTaskValue(args["inputs"]),
	}
	return s.handleRunHostPlanWithMetadata(map[string]any{"plan": doc, "resume": recipeBoolField(args, "resume")}, metadata, "opute.provider.teardown", "Tearing down provider...")
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
	s.providerMu.RLock()
	adapter := s.providerAdapters[providerID]
	s.providerMu.RUnlock()
	if adapter == nil {
		return fmt.Errorf("provider %q is not connected for teardown finalization", providerID)
	}
	inputs, _ := metadata["providerTeardownInputs"].(map[string]any)
	finalize, err := adapter.Call(context.Background(), providerTeardownOperation, map[string]any{"phase": "finalize", "inputs": inputs})
	if err != nil {
		return fmt.Errorf("finalize provider teardown: %w", err)
	}
	if finalize == nil || finalize.IsError {
		return fmt.Errorf("provider %q teardown finalization failed", providerID)
	}
	if err := s.providerLifecycle.Drain(context.Background(), generationID); err != nil {
		return fmt.Errorf("drain provider generation: %w", err)
	}
	if generation, ok := s.providerLifecycle.Get(generationID); ok {
		if err := s.persistProviderGeneration(generation); err != nil {
			return fmt.Errorf("persist stopped provider generation: %w", err)
		}
	}
	s.providerMu.Lock()
	delete(s.providerAdapters, providerID)
	delete(s.providerValidation, providerID)
	s.providerMu.Unlock()
	if adapter != nil {
		if err := adapter.Close(); err != nil {
			return fmt.Errorf("close provider adapter: %w", err)
		}
	}
	if s.state != nil {
		if err := s.state.RemoveActiveCapabilitiesForProvider(providerID); err != nil {
			return fmt.Errorf("clear active provider capabilities: %w", err)
		}
	}
	return nil
}
