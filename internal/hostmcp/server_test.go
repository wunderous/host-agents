package hostmcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	capabilitycatalog "github.com/wunderous/host-agents/internal/catalog"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/provider"
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
