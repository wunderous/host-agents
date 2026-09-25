package recipe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wunderous/host-agents/internal/plan"
)

func TestProviderInstallRecipesPassPinnedActivationProvenance(t *testing.T) {
	const revision = "0123456789abcdef0123456789abcdef01234567"
	const digest = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	const source = "https://raw.githubusercontent.com/wunderous/host-agents/" + revision + "/activation.yaml"

	tests := []struct {
		name        string
		path        string
		wantVersion string
	}{
		{name: "k3s", path: "../../plugins/kubernetes/k3s/recipes/install.yaml", wantVersion: "2.0.1"},
		{name: "tailscale", path: "../../plugins/tunneling/tailscale/recipes/install.yaml", wantVersion: "2.0.0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Clean(test.path))
			if err != nil {
				t.Fatal(err)
			}
			document, err := DecodeHost(raw)
			if err != nil {
				t.Fatalf("decode provider install recipe: %v", err)
			}
			if document.RecipeVersion != test.wantVersion {
				t.Fatalf("recipeVersion = %q, want %s", document.RecipeVersion, test.wantVersion)
			}
			revisionInput, ok := document.Inputs["activationRecipeRevision"]
			if !ok || !revisionInput.Required || revisionInput.Schema["pattern"] != "^[0-9a-f]{40}$" {
				t.Fatalf("activationRecipeRevision input = %#v, want a required 40-character lowercase commit", revisionInput)
			}

			loaded, err := ResolveHostInputs(document, map[string]any{
				"artifactUri":              "https://example.invalid/provider",
				"artifactSha256":           strings.Repeat("a", 64),
				"activationRecipeSource":   source,
				"activationRecipeRevision": revision,
				"activationRecipeSha256":   digest,
				"runNonce":                 "provenance-test",
			})
			if err != nil {
				t.Fatalf("resolve provider install recipe inputs: %v", err)
			}
			if test.name == "k3s" {
				var start *plan.Node
				for i := range loaded.ExpandedPlan.Nodes {
					node := &loaded.ExpandedPlan.Nodes[i]
					if node.ID == "start" && node.Action != nil && node.Action.Tool == "set_host_service_state" {
						start = node
						break
					}
				}
				if start == nil {
					t.Fatal("K3s install recipe has no typed provider start action")
				}
				eval := plan.EvalContext{
					Variables: loaded.ExpandedPlan.Variables,
					NodeOutput: map[string]any{
						"agent": map[string]any{
							"agent": map[string]any{"serviceScope": "user"},
						},
					},
				}
				stateArgs, err := plan.InterpolateArgs(start.Action.Args, eval)
				if err != nil {
					t.Fatalf("interpolate default provider service state: %v", err)
				}
				if got, _ := stateArgs["state"].(string); got != "start" {
					t.Fatalf("default provider service state = %q, want start", got)
				}

				inputs := map[string]any{
					"artifactUri":              "https://example.invalid/provider",
					"artifactSha256":           strings.Repeat("a", 64),
					"activationRecipeSource":   source,
					"activationRecipeRevision": revision,
					"activationRecipeSha256":   digest,
					"runNonce":                 "provenance-test-restart",
					"serviceState":             "restart",
				}
				restarted, err := ResolveHostInputs(document, inputs)
				if err != nil {
					t.Fatalf("resolve explicit provider restart: %v", err)
				}
				var explicitStart *plan.Node
				for i := range restarted.ExpandedPlan.Nodes {
					node := &restarted.ExpandedPlan.Nodes[i]
					if node.ID != "start" || node.Action == nil || node.Action.Tool != "set_host_service_state" {
						continue
					}
					eval := plan.EvalContext{
						Variables: restarted.ExpandedPlan.Variables,
						NodeOutput: map[string]any{
							"agent": map[string]any{
								"agent": map[string]any{"serviceScope": "user"},
							},
						},
					}
					args, err := plan.InterpolateArgs(node.Action.Args, eval)
					if err != nil {
						t.Fatalf("interpolate explicit provider restart: %v", err)
					}
					if got, _ := args["state"].(string); got != "restart" {
						t.Fatalf("provider service state = %q, want restart", got)
					}
					explicitStart = node
					break
				}
				if explicitStart == nil {
					t.Fatal("explicit provider restart did not resolve to the typed start action")
				}
				inputs["serviceState"] = "stop"
				if _, err := ResolveHostInputs(document, inputs); err == nil {
					t.Fatal("invalid provider service state was accepted")
				}
			}

			var install *plan.Node
			for i := range loaded.ExpandedPlan.Nodes {
				node := &loaded.ExpandedPlan.Nodes[i]
				if node.Action != nil && node.Action.Tool == "opute.provider.install" {
					install = node
					break
				}
			}
			if install == nil {
				t.Fatal("provider install recipe has no opute.provider.install action")
			}
			args, err := plan.InterpolateArgs(install.Action.Args, plan.EvalContext{Variables: loaded.ExpandedPlan.Variables})
			if err != nil {
				t.Fatalf("interpolate provider install action: %v", err)
			}
			for key, want := range map[string]string{
				"recipeSource": source,
				"revision":     revision,
				"sha256":       digest,
			} {
				if got, _ := args[key].(string); got != want {
					t.Errorf("provider install %s = %q, want %q", key, got, want)
				}
			}
		})
	}
}
