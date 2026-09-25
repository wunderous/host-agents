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
		name string
		path string
	}{
		{name: "k3s", path: "../../plugins/kubernetes/k3s/recipes/install.yaml"},
		{name: "tailscale", path: "../../plugins/tunneling/tailscale/recipes/install.yaml"},
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
			if document.RecipeVersion != "2.0.0" {
				t.Fatalf("recipeVersion = %q, want 2.0.0 for the required input change", document.RecipeVersion)
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
