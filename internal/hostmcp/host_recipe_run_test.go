package hostmcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const hostLocalRecipeYAML = `contractVersion: host-recipe.v1
recipeId: bootstrap-first-node
recipeVersion: 1.0.0
execution:
  coordinator: host-agent
  mode: local
inputs:
  host:
    required: true
    schema:
      type: string
plan:
  contractVersion: host-plan.v1
  planId: bootstrap-first-node
  generation: 1
  idempotencyKey: bootstrap-${vars.inputs.host}
  nodes:
    - id: inventory
      target:
        hostRef: ${vars.inputs.host}
      action:
        tool: list_vms
        args: {}
`

func writeRecipe(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recipe.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write recipe: %v", err)
	}
	return path
}

// Establishing the first Kubernetes node of the cluster that will host the
// Platform has to work when no Platform exists, so the Host Agent needs an
// entry point of its own. Before this existed the host recipe parser was
// carried with no executor behind it, which is why every documented bootstrap
// began with a Platform that was itself waiting to be installed.
func TestHostLocalRecipeRunsOnTheOwningAgentAndRefusesOtherwise(t *testing.T) {
	server := newStandaloneTestServer(t, true)
	server.agentID = "host-under-test"
	path := writeRecipe(t, hostLocalRecipeYAML)

	result, err := server.handleValidateHostLocalRecipe(map[string]any{
		"source": path,
		"inputs": map[string]any{"host": "host-under-test"},
	})
	if err != nil {
		t.Fatalf("validate host-local recipe: %v", err)
	}
	if result.IsError {
		t.Fatalf("host-local recipe was rejected on its own host: %s", resultText(result))
	}

	// The runner checks the target again at dispatch, but by then earlier nodes
	// have already mutated the host. A recipe addressed to a peer has to be
	// refused before the first of them runs.
	foreign, err := server.handleValidateHostLocalRecipe(map[string]any{
		"source": path,
		"inputs": map[string]any{"host": "some-other-host"},
	})
	if err != nil {
		t.Fatalf("validate foreign-target recipe: %v", err)
	}
	if !foreign.IsError || !strings.Contains(resultText(foreign), "may only act on the host executing it") {
		t.Fatalf("a recipe targeting another Host Agent was accepted: %s", resultText(foreign))
	}
}

// The two envelopes are not interchangeable in either direction: the Platform
// refuses a host-local recipe because it would dispatch the leaves across the
// authenticated boundary as if the plan were distributed, and the Host Agent
// refuses a distributed one because it has none of the durable machinery the
// document is relying on.
func TestHostLocalEntryPointRefusesADistributedRecipe(t *testing.T) {
	server := newStandaloneTestServer(t, true)
	server.agentID = "host-under-test"
	distributed := strings.Replace(hostLocalRecipeYAML, "coordinator: host-agent", "coordinator: platform", 1)
	distributed = strings.Replace(distributed, "mode: local", "mode: distributed", 1)
	path := writeRecipe(t, distributed)

	result, err := server.handleRunHostLocalRecipe(context.Background(), map[string]any{
		"source": path,
		"inputs": map[string]any{"host": "host-under-test"},
	})
	if err != nil {
		t.Fatalf("run distributed recipe: %v", err)
	}
	if !result.IsError || !strings.Contains(resultText(result), "submit it to the Platform coordinator") {
		t.Fatalf("a distributed recipe was executed locally: %s", resultText(result))
	}
}
