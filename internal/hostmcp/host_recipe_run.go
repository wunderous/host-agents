package hostmcp

import (
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/plan"
	"github.com/wunderous/host-agents/internal/recipe"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/internal/version"
)

// A host-local host recipe is how the first Kubernetes node of a cluster gets
// established when no Platform exists yet: an arbitrary new user has a Host
// Agent and nothing else, so the bootstrap cannot route through a Platform
// coordinator that is itself waiting to be installed onto the result.
//
// It is executed through the same durable host-plan runner every other recipe
// kind uses, so idempotency, readiness validation, recovery and resume are the
// existing ones rather than a second implementation. What this layer adds is the
// recipe envelope: sourcing with a pinned hash, input resolution, compatibility,
// and the refusal to execute anything the contract says belongs to the Platform.
func (s *Server) loadHostLocalRecipe(args map[string]any, requireHash bool) (recipe.HostLoaded, tools.CapabilityCatalogSnapshot, error) {
	sourced, err := recipe.LoadHost(recipeSourceRequest(args, requireHash))
	if err != nil {
		return recipe.HostLoaded{}, tools.CapabilityCatalogSnapshot{}, err
	}
	if !sourced.Document.Execution.IsHostLocal() {
		return recipe.HostLoaded{}, tools.CapabilityCatalogSnapshot{}, fmt.Errorf(
			"host recipe %q declares coordinator=%s/mode=%s; submit it to the Platform coordinator, which owns the wait fences, resume revisions and cross-host dispatch a Host Agent cannot provide",
			sourced.Document.RecipeID, sourced.Document.Execution.Coordinator, sourced.Document.Execution.Mode)
	}
	if err := recipe.ValidateHostAgentVersion(sourced.Document.Compatibility.MinHostAgentVersion, version.Version); err != nil {
		return recipe.HostLoaded{}, tools.CapabilityCatalogSnapshot{}, err
	}
	values, err := recipeInputValues(args)
	if err != nil {
		return recipe.HostLoaded{}, tools.CapabilityCatalogSnapshot{}, err
	}
	resolved, err := recipe.ResolveHostInputs(sourced.Document, values)
	if err != nil {
		return recipe.HostLoaded{}, tools.CapabilityCatalogSnapshot{}, err
	}
	resolved.Source = sourced.Source
	resolved.Raw = sourced.Raw
	snapshot := s.CatalogSnapshot()
	if err := resolved.Validate(planCapabilitiesFromSnapshot(snapshot), snapshot.Revision); err != nil {
		return recipe.HostLoaded{}, tools.CapabilityCatalogSnapshot{}, err
	}
	if err := s.assertHostLocalTargets(resolved); err != nil {
		return recipe.HostLoaded{}, tools.CapabilityCatalogSnapshot{}, err
	}
	return resolved, snapshot, nil
}

// The runner checks each target again at dispatch. This check exists because by
// then the earlier nodes have already mutated the host: a recipe addressed to a
// peer should be refused before the first of them runs, not halfway through.
func (s *Server) assertHostLocalTargets(loaded recipe.HostLoaded) error {
	agentID := strings.TrimSpace(s.agentID)
	if agentID == "" {
		return nil
	}
	for _, node := range loaded.ExpandedPlan.Nodes {
		if node.Target == nil {
			continue
		}
		resolved, err := plan.InterpolateArgs(map[string]any{"hostRef": node.Target.HostRef}, plan.EvalContext{Variables: loaded.ExpandedPlan.Variables})
		if err != nil {
			return fmt.Errorf("resolve host-local target for node %q: %w", node.ID, err)
		}
		hostRef, _ := resolved["hostRef"].(string)
		if strings.TrimSpace(hostRef) != agentID {
			return fmt.Errorf("host-local node %q targets Host Agent %q, not this one (%q); a host-local recipe may only act on the host executing it", node.ID, hostRef, agentID)
		}
	}
	return nil
}

func hostLocalRecipeMetadata(loaded recipe.HostLoaded) map[string]any {
	return map[string]any{
		"contractVersion": recipe.HostContractVersion,
		"recipeKind":      "host-local",
		"recipeId":        loaded.Document.RecipeID,
		"recipeVersion":   loaded.Document.RecipeVersion,
		"execution":       loaded.Document.Execution,
		"source":          loaded.Source,
		"inputs":          loaded.RedactedInputs(),
		"secretInputs":    loaded.SecretInputNames(),
		"recipeHash":      loaded.Source.RecipeHash,
		"recipeDocument":  loaded.Document,
		"outputMapping":   loaded.Document.OutputMapping,
		"expandedPlan":    loaded.ExpandedPlan,
	}
}

func (s *Server) handleValidateHostLocalRecipe(args map[string]any) (*mcp.CallToolResult, error) {
	loaded, snapshot, err := s.loadHostLocalRecipe(args, false)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	planResult, err := s.validateHostPlanWithSnapshot(loaded.ExpandedPlan, snapshot)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	return structuredResult(map[string]any{
		"valid":           true,
		"contractVersion": recipe.HostContractVersion,
		"recipeId":        loaded.Document.RecipeID,
		"recipeVersion":   loaded.Document.RecipeVersion,
		"execution":       loaded.Document.Execution,
		"hostAgentId":     s.agentID,
		"source":          loaded.Source,
		"inputs":          loaded.RedactedInputs(),
		"recipeHash":      loaded.Source.RecipeHash,
		"rawSha256":       loaded.Source.RawSHA256,
		"plan":            planResult,
	}, "host-local recipe is valid"), nil
}

func (s *Server) handleRunHostLocalRecipe(args map[string]any) (*mcp.CallToolResult, error) {
	loaded, _, err := s.loadHostLocalRecipe(args, true)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	return s.handleRunHostPlanWithMetadata(map[string]any{
		"plan":   loaded.ExpandedPlan,
		"resume": recipeBoolField(args, "resume"),
	}, hostLocalRecipeMetadata(loaded), "run_host_local_recipe", "Executing host-local recipe...")
}
