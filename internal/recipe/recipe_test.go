package recipe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wunderous/host-agents/internal/plan"
)

const testRecipeYAML = `
contractVersion: runtime-recipe.v1
recipeId: test-runtime
recipeVersion: 1.0.0
runtime:
  id: test-runtime
  servingContract: openai-chat.v1
  capabilities: [chat, streaming]
inputs:
  endpoint:
    required: true
    schema:
      type: string
  model:
    default: test-model
    schema:
      type: string
plan:
  contractVersion: host-plan.v1
  planId: test-runtime
  generation: 1
  idempotencyKey: test-runtime-${vars.inputs.model}
  nodes:
    - id: inspect
      action:
        tool: inspect
        args:
          endpoint: ${vars.inputs.endpoint}
      validate:
        tool: inspect
        args:
          endpoint: ${vars.inputs.endpoint}
        assert:
          - path: /ready
            op: eq
            value: true
`

func TestLoadAndResolveYAMLRecipe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recipe.yaml")
	if err := os.WriteFile(path, []byte(testRecipeYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(SourceRequest{Source: path})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Source.Kind != "path" || !strings.HasPrefix(loaded.Source.RawSHA256, "sha256:") || loaded.Source.RecipeHash == "" {
		t.Fatalf("unexpected source metadata: %+v", loaded.Source)
	}
	if err := loaded.ResolveInputs(map[string]any{"endpoint": "http://127.0.0.1:11434/v1"}); err != nil {
		t.Fatal(err)
	}
	if loaded.Inputs["model"] != "test-model" {
		t.Fatalf("default model = %#v", loaded.Inputs["model"])
	}
	loaded.Document.Inputs["token"] = InputSpec{Secret: true}
	loaded.Inputs["token"] = "sensitive"
	if loaded.RedactedInputs()["token"] != "[redacted]" {
		t.Fatalf("secret input was not redacted: %#v", loaded.RedactedInputs())
	}
	if loaded.ExpandedPlan.Variables["inputs"].(map[string]any)["endpoint"] != "http://127.0.0.1:11434/v1" {
		t.Fatalf("resolved endpoint missing: %#v", loaded.ExpandedPlan.Variables)
	}
	if loaded.ExpandedPlan.IdempotencyKey != "test-runtime-test-model" {
		t.Fatalf("resolved idempotency key = %q", loaded.ExpandedPlan.IdempotencyKey)
	}
	capabilities := map[string]plan.Capability{
		"inspect": {Name: "inspect", InputSchema: map[string]any{"type": "object", "required": []any{"endpoint"}, "properties": map[string]any{"endpoint": map[string]any{"type": "string"}}}, OutputSchema: map[string]any{"type": "object"}, Effect: "read", Idempotent: true},
	}
	if err := loaded.Validate(capabilities, "catalog-test"); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeRejectsUnknownInputAndMissingRequiredInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recipe.yaml")
	if err := os.WriteFile(path, []byte(testRecipeYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(SourceRequest{Source: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.ResolveInputs(map[string]any{"unknown": "value"}); err == nil || !strings.Contains(err.Error(), "unknown recipe input") {
		t.Fatalf("unknown input error = %v", err)
	}
	if err := loaded.ResolveInputs(nil); err == nil || !strings.Contains(err.Error(), "required recipe input") {
		t.Fatalf("missing input error = %v", err)
	}
}

func TestTunnelRecipeResolvesBindingInputs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tunnel.yaml")
	contents := `
contractVersion: tunnel-recipe.v1
recipeId: test-tunnel
recipeVersion: 1.0.0
provider:
  id: test.provider
bindings:
  - id: local
    hostname: ${vars.inputs.hostname}
    localTarget: ${vars.inputs.localTarget}
inputs:
  endpoint:
    required: true
    schema: {type: string}
  hostname:
    default: example.invalid
    schema: {type: string}
  localTarget:
    default: http://127.0.0.1:8080
    schema: {type: string}
plan:
  contractVersion: host-plan.v1
  planId: test-tunnel
  generation: 1
  idempotencyKey: test-tunnel-${vars.inputs.endpoint}
  nodes:
    - id: inspect
      action:
        tool: inspect
        args: {endpoint: "${vars.inputs.endpoint}"}
      validate:
        tool: inspect
        args: {endpoint: "${vars.inputs.endpoint}"}
        assert: [{path: /ready, op: eq, value: true}]
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadTunnel(SourceRequest{Source: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.ResolveInputs(map[string]any{"endpoint": "http://127.0.0.1:11434"}); err != nil {
		t.Fatal(err)
	}
	if got := loaded.Document.Bindings[0].Hostname; got != "example.invalid" {
		t.Fatalf("resolved hostname = %q", got)
	}
	if got := loaded.Document.Bindings[0].LocalTarget; got != "http://127.0.0.1:8080" {
		t.Fatalf("resolved local target = %q", got)
	}
}

func TestRemoteRecipeSourcesRequirePinnedRevision(t *testing.T) {
	for _, source := range []string{
		"github:opute-io/runtime-recipes/ollama.yaml",
		"https://raw.githubusercontent.com/opute-io/runtime-recipes/main/ollama.yaml",
	} {
		if _, _, err := loadSource(SourceRequest{Source: source}); err == nil || !strings.Contains(err.Error(), "immutable") && !strings.Contains(err.Error(), "40-character") {
			t.Fatalf("source %q error = %v", source, err)
		}
	}
}

func TestRecipeRejectsHashMismatchAndSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recipe.yaml")
	if err := os.WriteFile(path, []byte(testRecipeYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadSource(SourceRequest{Source: path, SHA256: strings.Repeat("0", 64)}); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("hash mismatch error = %v", err)
	}
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadSource(SourceRequest{Source: link}); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestParseGitHubReference(t *testing.T) {
	commit := strings.Repeat("a", 40)
	parsed, err := parseGitHubReference("opute-io/runtime-recipes/ollama.yaml@"+commit, "")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Revision != commit || parsed.URL != "https://raw.githubusercontent.com/opute-io/runtime-recipes/"+commit+"/ollama.yaml" {
		t.Fatalf("parsed GitHub reference = %+v", parsed)
	}
}

func TestRemoteMutationRequiresExpectedHash(t *testing.T) {
	_, _, err := fetchRemote("https://raw.githubusercontent.com/example/repo/"+strings.Repeat("a", 40)+"/recipe.yaml", strings.Repeat("a", 40), "", true)
	if err == nil || !strings.Contains(err.Error(), "expected sha256") {
		t.Fatalf("missing remote mutation hash error = %v", err)
	}
}

func TestRecipeRejectsNestedPlanRunsAndChecksCompatibility(t *testing.T) {
	doc := Document{
		ContractVersion: ContractVersion,
		RecipeID:        "nested",
		RecipeVersion:   "1.0.0",
		Runtime:         RuntimeSpec{ID: "test", ServingContract: "openai-chat.v1"},
		Compatibility:   CompatibilitySpec{MinHostAgentVersion: "99.0.0"},
		Plan: plan.Document{
			ContractVersion: plan.ContractVersion,
			PlanID:          "nested",
			Generation:      1,
			IdempotencyKey:  "nested",
			Nodes:           []plan.Node{{ID: "nested", Action: &plan.Action{Tool: "run_runtime_recipe"}}},
		},
	}
	if err := ValidateHostAgentVersion(doc.Compatibility.MinHostAgentVersion, "dev"); err == nil {
		t.Fatal("development agent should not satisfy a release compatibility requirement")
	}
	if err := rejectNestedPlanRuns(doc.Plan); err == nil || !strings.Contains(err.Error(), "recursively") {
		t.Fatalf("nested recipe plan error = %v", err)
	}
}
