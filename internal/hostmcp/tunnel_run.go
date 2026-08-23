package hostmcp

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/plan"
	"github.com/wunderous/host-agents/internal/recipe"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/internal/version"
)

func (s *Server) loadTunnelRecipe(args map[string]any, requireHash bool) (recipe.LoadedTunnel, tools.CapabilityCatalogSnapshot, error) {
	loaded, err := recipe.LoadTunnel(recipeSourceRequest(args, requireHash))
	if err != nil {
		return recipe.LoadedTunnel{}, tools.CapabilityCatalogSnapshot{}, err
	}
	inputs, err := recipeInputValues(args)
	if err != nil {
		return recipe.LoadedTunnel{}, tools.CapabilityCatalogSnapshot{}, err
	}
	if err := loaded.ResolveInputs(inputs); err != nil {
		return recipe.LoadedTunnel{}, tools.CapabilityCatalogSnapshot{}, err
	}
	if err := recipe.ValidateHostAgentVersion(loaded.Document.Compatibility.MinHostAgentVersion, version.Version); err != nil {
		return recipe.LoadedTunnel{}, tools.CapabilityCatalogSnapshot{}, err
	}
	snapshot := s.CatalogSnapshot()
	if err := loaded.Validate(planCapabilitiesFromSnapshot(snapshot), snapshot.Revision); err != nil {
		return recipe.LoadedTunnel{}, tools.CapabilityCatalogSnapshot{}, err
	}
	return loaded, snapshot, nil
}

func tunnelRecipeMetadata(loaded recipe.LoadedTunnel, activate bool) map[string]any {
	metadata := map[string]any{
		"recipeKind":      "tunnel",
		"contractVersion": recipe.TunnelContractVersion,
		"recipeId":        loaded.Document.RecipeID,
		"recipeVersion":   loaded.Document.RecipeVersion,
		"provider":        loaded.Document.Provider,
		"providerId":      loaded.Document.Provider.ID,
		"bindings":        loaded.Document.Bindings,
		"source":          loaded.Source,
		"inputs":          loaded.RedactedInputs(),
		"secretInputs":    loaded.SecretInputNames(),
		"recipeHash":      loaded.Source.RecipeHash,
		"recipeDocument":  loaded.Document,
		"outputMapping":   loaded.Document.OutputMapping,
		"expandedPlan":    loaded.ExpandedPlan,
		"activate":        activate,
	}
	if loaded.Document.Activation != nil {
		activation := loaded.Document.Activation
		bindings := make(map[string]any, len(activation.InputBindings))
		inputNames := make(map[string]any, len(activation.InputBindings))
		for binding, inputName := range activation.InputBindings {
			bindings[binding] = loaded.Inputs[inputName]
			inputNames[binding] = inputName
		}
		metadata["activation"] = map[string]any{
			"capability":      activation.Capability,
			"servingContract": activation.ServingContract,
			"inputBindings":   bindings,
		}
		metadata["activationInputNames"] = inputNames
	}
	return metadata
}

func (s *Server) handleValidateTunnelRecipe(args map[string]any) (*mcp.CallToolResult, error) {
	loaded, snapshot, err := s.loadTunnelRecipe(args, false)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	planResult, err := s.validateHostPlanWithSnapshot(loaded.ExpandedPlan, snapshot)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	return structuredResult(map[string]any{
		"valid":           true,
		"contractVersion": recipe.TunnelContractVersion,
		"recipeId":        loaded.Document.RecipeID,
		"recipeVersion":   loaded.Document.RecipeVersion,
		"provider":        loaded.Document.Provider,
		"bindings":        loaded.Document.Bindings,
		"source":          loaded.Source,
		"inputs":          loaded.RedactedInputs(),
		"recipeHash":      loaded.Source.RecipeHash,
		"rawSha256":       loaded.Source.RawSHA256,
		"plan":            planResult,
	}, "tunnel recipe is valid"), nil
}

func (s *Server) handleRunTunnelRecipe(args map[string]any) (*mcp.CallToolResult, error) {
	resumeRunID := recipeStringField(args, "runId")
	if recipeStringField(args, "source") == "" {
		if resumeRunID == "" || s.state == nil || !recipeBoolField(args, "resume") {
			return tools.ErrorResult(fmt.Errorf("source is required unless resuming with runId")), nil
		}
		return s.resumeRecipeRun(args, resumeRunID, "tunnel", "run_tunnel_recipe", "Executing tunnel recipe...")
	}
	loaded, _, err := s.loadTunnelRecipe(args, true)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	activate := recipeBoolField(args, "activate")
	if activate && loaded.Document.Activation == nil {
		return tools.ErrorResult(fmt.Errorf("tunnel recipe does not declare an activation contract; activation cannot be requested")), nil
	}
	metadata := tunnelRecipeMetadata(loaded, activate)
	if providerID := recipeStringField(args, "providerId"); providerID != "" {
		metadata["providerId"] = providerID
		metadata["providerVersion"] = recipeStringField(args, "providerVersion")
		metadata["providerGenerationId"] = recipeStringField(args, "providerGenerationId")
		metadata["providerManifest"] = redactTaskValue(args["providerManifest"])
	}
	return s.handleRunHostPlanWithMetadata(map[string]any{
		"plan":   loaded.ExpandedPlan,
		"resume": recipeBoolField(args, "resume"),
	}, metadata, "run_tunnel_recipe", "Executing tunnel recipe...")
}

func (s *Server) resumeRecipeRun(args map[string]any, runID, kind, taskName, description string) (*mcp.CallToolResult, error) {
	record, found, err := s.state.GetPlan(runID)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("get %s run: %w", kind, err)), nil
	}
	if !found || record.RecipeJSON == "" {
		return tools.ErrorResult(fmt.Errorf("%s recipe run not found: %s", kind, runID)), nil
	}
	var document plan.Document
	if err := json.Unmarshal([]byte(record.PlanJSON), &document); err != nil {
		return tools.ErrorResult(fmt.Errorf("decode persisted %s recipe plan: %w", kind, err)), nil
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(record.RecipeJSON), &metadata); err != nil {
		return tools.ErrorResult(fmt.Errorf("decode persisted %s recipe metadata: %w", kind, err)), nil
	}
	if recipeBoolField(args, "activate") {
		metadata["activate"] = true
	}
	if persistedPlanHasRedactedSecret(document, metadata) {
		return tools.ErrorResult(fmt.Errorf("%s recipe resume requires secret inputs to be supplied through references; refusing to execute redacted values", kind)), nil
	}
	return s.handleRunHostPlanWithMetadata(map[string]any{"plan": document, "resume": true}, metadata, taskName, description)
}

func (s *Server) handleGetTunnelRun(args map[string]any) (*mcp.CallToolResult, error) {
	return s.handleGetHostPlanRun(args)
}
