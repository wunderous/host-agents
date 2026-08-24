package hostmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	providercontract "github.com/wunderous/host-agents/contracts/provider"
	capabilitycatalog "github.com/wunderous/host-agents/internal/catalog"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/provider"
	"github.com/wunderous/host-agents/internal/tasks"
	"github.com/wunderous/host-agents/internal/tools"
)

func newStandaloneTestServer(t *testing.T, allowMutations bool) *Server {
	t.Helper()
	svc := ops.NewHostOperationsService(ops.Options{
		ProviderID: provider.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	server, err := NewServer(Options{
		ProviderID:     "incus",
		Ops:            svc,
		Standalone:     true,
		AllowMutations: allowMutations,
		StateDir:       t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func TestStandaloneServerDoesNotExposePlatformTools(t *testing.T) {
	server := newStandaloneTestServer(t, false)
	for _, def := range server.toolDefs {
		if tools.IsStandaloneMutation(def.Name) {
			continue
		}
		if def.Name == "register_host_agent" || def.Name == "host_agent_heartbeat" || def.Name == "dispatch_host_operation" {
			t.Fatalf("platform tool leaked into standalone catalog: %s", def.Name)
		}
	}
}

func TestTaskAugmentedResultUsesMCPCreateTaskShape(t *testing.T) {
	server := newStandaloneTestServer(t, true)
	result, err := server.createAsyncTask("run_host_command", map[string]any{"command": "true"})
	if err != nil {
		t.Fatal(err)
	}
	content, ok := result.StructuredContent.(map[string]any)
	if !ok || content["resultType"] != "task" {
		t.Fatalf("task result = %#v", result.StructuredContent)
	}
	for _, key := range []string{"taskId", "status", "createdAt", "lastUpdatedAt", "ttlMs", "pollIntervalMs"} {
		if _, exists := content[key]; !exists {
			t.Fatalf("task result missing %q: %#v", key, content)
		}
	}
}

func TestInputRequiredTaskUsesTasksUpdate(t *testing.T) {
	server := newStandaloneTestServer(t, true)
	result, err := server.createInputRequestTask(map[string]any{
		"prompt":       "Continue the validation?",
		"responseType": "boolean",
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, ok := result.StructuredContent.(map[string]any)
	if !ok || envelope["status"] != tasks.StatusInputRequired {
		t.Fatalf("create envelope = %#v", result.StructuredContent)
	}
	taskID, _ := envelope["taskId"].(string)
	getParams, _ := json.Marshal(map[string]string{"taskId": taskID})
	getResult, err := server.HandleExtensionMethod("tasks/get", getParams)
	if err != nil {
		t.Fatal(err)
	}
	getMap := getResult.(map[string]any)
	if getMap["status"] != tasks.StatusInputRequired || getMap["inputRequests"] == nil {
		t.Fatalf("tasks/get input projection = %#v", getMap)
	}
	updateParams, _ := json.Marshal(map[string]any{
		"taskId":         taskID,
		"inputResponses": map[string]any{"response": true},
	})
	if updated, err := server.HandleExtensionMethod("tasks/update", updateParams); err != nil || updated.(map[string]any)["resultType"] != "complete" {
		t.Fatalf("tasks/update = %#v, err=%v", updated, err)
	}
	updated, ok := server.Tasks().Get(taskID)
	if !ok || updated.Status != tasks.StatusCompleted {
		t.Fatalf("task after update = %#v", updated)
	}
	if updated.ToolResult == nil || updated.ToolResult.StructuredContent.(map[string]any)["response"] != true {
		t.Fatalf("task result = %#v", updated.ToolResult)
	}
}

func TestServerDiscoverAdvertisesModernRevisionAndTasks(t *testing.T) {
	server := newStandaloneTestServer(t, false)
	result, err := server.HandleExtensionMethod("server/discover", nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	versions, _ := payload["supportedVersions"].([]any)
	if len(versions) != 1 || versions[0] != "2026-07-28" {
		t.Fatalf("supported versions = %#v", payload["supportedVersions"])
	}
	if payload["resultType"] != "complete" {
		t.Fatalf("discovery resultType = %#v", payload["resultType"])
	}
	serverMeta, _ := payload["_meta"].(map[string]any)
	if _, ok := serverMeta["io.modelcontextprotocol/serverInfo"]; !ok {
		t.Fatalf("server info missing from discovery metadata: %#v", serverMeta)
	}
	capabilities := payload["capabilities"].(map[string]any)
	extensions := capabilities["extensions"].(map[string]any)
	if _, ok := extensions["io.modelcontextprotocol/tasks"]; !ok {
		t.Fatalf("tasks extension missing: %#v", capabilities)
	}
}

func TestLegacyEntityIdentityFailsClosedAtMCPBoundary(t *testing.T) {
	server := newStandaloneTestServer(t, false)
	result, err := server.DispatchTool(context.Background(), "get_vm_info", map[string]any{
		"vmName": "worker-01",
		"fast":   true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("legacy identity was not rejected: %#v", result)
	}
	if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "canonical resource uri") {
		t.Fatalf("unexpected legacy identity error: %#v", result.Content)
	}
}

func TestDispatchToolRoutesLifecycleCallsThroughProviderBoundary(t *testing.T) {
	server := newStandaloneTestServer(t, false)
	result, err := server.DispatchTool(context.Background(), "get_capability_catalog", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError {
		t.Fatalf("provider lifecycle dispatch failed: %#v", result)
	}
	payload, ok := result.StructuredContent.(tools.CapabilityCatalogSnapshot)
	if !ok || payload.Revision == "" {
		t.Fatalf("capability catalog result = %#v", result.StructuredContent)
	}
}

func TestRedactTaskValuePreservesSecretCollectionShape(t *testing.T) {
	value := map[string]any{
		"secretInputs": []any{"token", "password"},
		"token":        "super-secret",
		"nested":       map[string]any{"value": "safe"},
	}
	redacted := redactTaskValue(value).(map[string]any)
	secretInputs, ok := redacted["secretInputs"].([]any)
	if !ok || len(secretInputs) != 2 || secretInputs[0] != "[redacted]" || secretInputs[1] != "[redacted]" {
		t.Fatalf("secret collection shape/content was not redacted safely: %#v", redacted["secretInputs"])
	}
	if redacted["token"] != "[redacted]" {
		t.Fatalf("scalar token was not redacted: %#v", redacted["token"])
	}
	if redacted["nested"].(map[string]any)["value"] != "safe" {
		t.Fatalf("non-sensitive nested value was unexpectedly changed: %#v", redacted["nested"])
	}
}

func TestStandaloneServerCloseIsIdempotent(t *testing.T) {
	server := newStandaloneTestServer(t, false)
	if err := server.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestStandaloneMutationPolicyDeniesEveryMutatingTool(t *testing.T) {
	server := newStandaloneTestServer(t, false)
	for name := range tools.StandaloneToolNames {
		if !tools.IsStandaloneMutation(name) {
			continue
		}
		result, err := server.handleToolCall(context.Background(), &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{}`)},
		}, name)
		if err != nil {
			t.Fatalf("%s returned protocol error: %v", name, err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("%s was not denied while mutations were disabled: %+v", name, result)
		}
	}
}

func TestStandaloneContractIsValidated(t *testing.T) {
	if err := tools.ValidateStandaloneToolContract(); err != nil {
		t.Fatal(err)
	}
}

func TestHostPlanMCPValidationAndDurableRun(t *testing.T) {
	server := newStandaloneTestServer(t, true)
	planDocument := map[string]any{
		"contractVersion": "host-plan.v1",
		"planId":          "hostmcp-test",
		"generation":      1,
		"idempotencyKey":  "hostmcp-test-1",
		"nodes": []any{map[string]any{
			"id":     "host",
			"action": map[string]any{"tool": "get_host_info", "args": map[string]any{}},
		}},
	}
	validated, err := server.handleValidateHostPlan(map[string]any{"plan": planDocument})
	if err != nil || validated == nil || validated.IsError {
		t.Fatalf("validate host plan = %#v err=%v", validated, err)
	}
	started, err := server.handleRunHostPlan(map[string]any{"plan": planDocument})
	if err != nil || started == nil || started.IsError {
		t.Fatalf("run host plan = %#v err=%v", started, err)
	}
	var startedValue map[string]any
	encoded, _ := json.Marshal(started.StructuredContent)
	if err := json.Unmarshal(encoded, &startedValue); err != nil {
		t.Fatal(err)
	}
	runID, _ := startedValue["runId"].(string)
	if runID == "" {
		t.Fatalf("run result has no runId: %#v", startedValue)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		record, found, getErr := server.state.GetPlan(runID)
		if getErr != nil || !found {
			t.Fatalf("get durable plan: found=%v err=%v", found, getErr)
		}
		if record.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("plan did not complete: %#v", record)
		}
		time.Sleep(20 * time.Millisecond)
	}
	second, err := server.handleRunHostPlan(map[string]any{"plan": planDocument})
	if err != nil || second == nil || second.IsError {
		t.Fatalf("idempotent second run = %#v err=%v", second, err)
	}
}

func TestRuntimeRecipeUsesDurableHostPlanRunner(t *testing.T) {
	server := newStandaloneTestServer(t, true)
	recipePath := filepath.Join(t.TempDir(), "runtime.yaml")
	recipeDocument := `
contractVersion: runtime-recipe.v1
recipeId: generic-test-runtime
recipeVersion: 1.0.0
runtime:
  id: generic-test
  servingContract: openai-chat.v1
  capabilities: [chat]
plan:
  contractVersion: host-plan.v1
  planId: generic-test-runtime
  generation: 1
  idempotencyKey: generic-test-runtime-1
  nodes:
    - id: host
      action:
        tool: get_host_info
        args: {}
`
	if err := os.WriteFile(recipePath, []byte(recipeDocument), 0o600); err != nil {
		t.Fatal(err)
	}
	validated, err := server.handleValidateRuntimeRecipe(map[string]any{"source": recipePath})
	if err != nil || validated == nil || validated.IsError {
		t.Fatalf("validate runtime recipe = %#v err=%v", validated, err)
	}
	started, err := server.handleRunRuntimeRecipe(map[string]any{"source": recipePath})
	if err != nil || started == nil || started.IsError {
		t.Fatalf("run runtime recipe = %#v err=%v", started, err)
	}
	var startedValue map[string]any
	encoded, _ := json.Marshal(started.StructuredContent)
	if err := json.Unmarshal(encoded, &startedValue); err != nil {
		t.Fatal(err)
	}
	runID, _ := startedValue["runId"].(string)
	if runID == "" {
		t.Fatalf("recipe run result has no runId: %#v", startedValue)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		record, found, getErr := server.state.GetPlan(runID)
		if getErr != nil || !found {
			t.Fatalf("get durable recipe run: found=%v err=%v", found, getErr)
		}
		if record.Status == "completed" {
			if record.RecipeJSON == "" || !strings.Contains(record.RecipeJSON, "generic-test-runtime") {
				t.Fatalf("recipe provenance was not persisted: %s", record.RecipeJSON)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recipe did not complete: %#v", record)
		}
		time.Sleep(20 * time.Millisecond)
	}
	resumed, err := server.handleRunRuntimeRecipe(map[string]any{"runId": runID})
	if err != nil || resumed == nil || resumed.IsError {
		t.Fatalf("resume runtime recipe = %#v err=%v", resumed, err)
	}
}

func TestRuntimeRecipeActivationValidatesBeforeCommittingActiveRuntime(t *testing.T) {
	serverHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"model"}]}`))
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"READY\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer serverHTTP.Close()

	server := newStandaloneTestServer(t, true)
	recipePath := filepath.Join(t.TempDir(), "activate.yaml")
	recipeDocument := fmt.Sprintf(`
contractVersion: runtime-recipe.v1
recipeId: activation-test
recipeVersion: 1.0.0
runtime:
  id: fake-runtime
  servingContract: openai-chat.v1
activation:
  capability: llm
  servingContract: openai-chat.v1
  inputBindings:
    endpoint: endpoint
    modelRef: model
inputs:
  endpoint:
    default: %s
  model:
    default: model
plan:
  contractVersion: host-plan.v1
  planId: activation-test
  generation: 1
  idempotencyKey: activation-test-${vars.inputs.endpoint}-${vars.inputs.model}
  defaults:
    timeoutMs: 30000
    retry:
      maxAttempts: 1
  nodes:
    - id: probe
      action:
        tool: probe_openai_compatible_server
        args:
          endpoint: ${vars.inputs.endpoint}
          modelRef: ${vars.inputs.model}
          includeChat: true
`, serverHTTP.URL)
	if err := os.WriteFile(recipePath, []byte(recipeDocument), 0o600); err != nil {
		t.Fatal(err)
	}
	started, err := server.handleRunRuntimeRecipe(map[string]any{"source": recipePath, "activate": true})
	if err != nil || started == nil || started.IsError {
		t.Fatalf("run activating recipe = %#v err=%v", started, err)
	}
	var startedValue map[string]any
	encoded, _ := json.Marshal(started.StructuredContent)
	if err := json.Unmarshal(encoded, &startedValue); err != nil {
		t.Fatal(err)
	}
	runID, _ := startedValue["runId"].(string)
	if runID == "" {
		t.Fatalf("recipe run result has no runId: %#v", startedValue)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		record, found, getErr := server.state.GetPlan(runID)
		if getErr != nil || !found {
			t.Fatalf("get durable activating recipe: found=%v err=%v", found, getErr)
		}
		if record.Status == "completed" {
			active, activeFound, activeErr := server.state.GetActiveRuntime("llm")
			if activeErr != nil || !activeFound {
				t.Fatalf("active runtime was not committed: found=%v err=%v", activeFound, activeErr)
			}
			if active.RunID != runID || active.Runtime != "fake-runtime" {
				t.Fatalf("unexpected active runtime: %+v", active)
			}
			break
		}
		if record.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("activating recipe did not complete: %#v", record)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServerDynamicCapabilityRegistrationPublishesRevisionAndDispatchesTrustedImplementation(t *testing.T) {
	server := newStandaloneTestServer(t, true)
	before := server.CatalogSnapshot().Revision
	descriptor := tools.CapabilityDescriptor{
		OperationID:       "probe_registered_runtime",
		Name:              "probe_registered_runtime",
		Description:       "Test-only typed runtime probe",
		InputSchema:       map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []string{"name"}},
		OutputSchema:      map[string]any{"type": "object", "required": []string{"name"}},
		Effect:            "read",
		Provider:          "incus",
		Implementation:    "host-agent:incus",
		ResourceKinds:     []string{"host"},
		Idempotent:        true,
		SupportsReadiness: false,
	}
	if err := server.RegisterCapability(capabilitycatalog.Registration{
		Descriptor: descriptor, ProviderID: "incus", Implementation: "host-agent:incus",
	}, func(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return structuredResult(map[string]any{"name": args["name"]}, ""), nil
	}); err != nil {
		t.Fatalf("register capability: %v", err)
	}
	if server.CatalogSnapshot().Revision == before {
		t.Fatal("dynamic registration did not change catalog revision")
	}
	result, err := server.handleToolCall(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{"name":"runtime"}`)},
	}, descriptor.Name)
	if err != nil || result == nil || result.IsError {
		t.Fatalf("dynamic capability call = %#v err=%v", result, err)
	}
}

func TestDynamicTypedProducerAndConsumerComposeThroughMCPWithoutToolKnowledge(t *testing.T) {
	server := newStandaloneTestServer(t, true)
	if err := server.ops.RegisterResource("host:local:plugin-host", map[string]any{"displayName": "plugin-host"}); err != nil {
		t.Fatal(err)
	}
	producer := tools.CapabilityDescriptor{
		OperationID: "plugin_list_hosts", Name: "plugin_list_hosts", Version: 1,
		InputSchema: map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"hosts": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}, "required": []string{"uri"}}},
		}},
		Produces: []tools.ResourceBinding{{ResourceType: "host", SourcePath: "hosts[].uri"}},
		Effect:   "read", Provider: "incus", Implementation: "host-agent:incus",
	}
	consumer := tools.CapabilityDescriptor{
		OperationID: "plugin_inspect_host", Name: "plugin_inspect_host", Version: 1,
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}, "required": []string{"uri"}},
		OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"accepted": map[string]any{"type": "boolean"}}},
		Requires:     []tools.ResourceBinding{{Argument: "uri", ResourceType: "host", Required: true}},
		Effect:       "read", Provider: "incus", Implementation: "host-agent:incus",
	}
	for _, descriptor := range []tools.CapabilityDescriptor{producer, consumer} {
		d := descriptor
		if err := server.RegisterCapability(capabilitycatalog.Registration{Descriptor: d, ProviderID: d.Provider, Implementation: d.Implementation}, func(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
			if d.Name == producer.Name {
				return structuredResult(map[string]any{"hosts": []map[string]any{{"uri": "host:local:plugin-host"}}}, ""), nil
			}
			return structuredResult(map[string]any{"accepted": args["uri"] == "host:local:plugin-host"}, ""), nil
		}); err != nil {
			t.Fatalf("register %s: %v", d.Name, err)
		}
	}
	snapshot := server.CatalogSnapshot()
	var found bool
	for _, edge := range snapshot.Edges {
		if edge.SourceTool == producer.Name && edge.TargetTool == consumer.Name && edge.ResourceType == "host" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dynamic typed edge missing: %#v", snapshot.Edges)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "typed-edge-test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) == 0 {
		t.Fatal("MCP tools/list returned no tools")
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: consumer.Name, Arguments: map[string]any{"uri": "host:local:plugin-host"}})
	if err != nil || result.IsError {
		t.Fatalf("typed consumer call = %#v err=%v", result, err)
	}
	value, ok := result.StructuredContent.(map[string]any)
	if !ok || value["accepted"] != true {
		t.Fatalf("typed consumer result = %#v", result.StructuredContent)
	}
}

func TestProviderManifestServicesPublishOnlyThroughAuthorizedOverlay(t *testing.T) {
	server := newStandaloneTestServer(t, true)
	before := server.CatalogSnapshot().Revision
	manifest := providercontract.InstallManifest{
		Provider: providercontract.ProviderRef{ID: "com.opute.fake", Version: "1.0.0"},
		Services: []providercontract.ServiceDefinition{{
			ID: "com.opute.fake.service", Version: 1,
			Operations: []providercontract.Operation{{
				ID: "opute.capability.fake.validate", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, Effect: "read", Idempotent: true,
			}},
		}},
	}
	if err := server.registerProviderServices(manifest); err != nil {
		t.Fatal(err)
	}
	snapshot := server.CatalogSnapshot()
	if snapshot.Revision == before {
		t.Fatal("provider service registration did not change catalog revision")
	}
	for _, descriptor := range snapshot.Tools {
		if descriptor.OperationID == "opute.capability.fake.validate" {
			if descriptor.Provider != "com.opute.fake" || descriptor.Implementation != "provider:com.opute.fake" {
				t.Fatalf("provider descriptor identity = %+v", descriptor)
			}
			return
		}
	}
	t.Fatal("provider service operation was not published")
}
