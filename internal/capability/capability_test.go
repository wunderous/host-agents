package capability

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/tools"
)

func testDescriptor() tools.CapabilityDescriptor {
	return tools.CapabilityDescriptor{
		OperationID: "test.capability",
		Name:        "test.capability",
		Version:     1,
		InputSchema: map[string]any{
			"type": "object", "required": []any{"name"},
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
		},
		OutputSchema: map[string]any{
			"type": "object", "required": []any{"uri"},
			"properties": map[string]any{"uri": map[string]any{"type": "string"}},
		},
		Effect: "read", Provider: "test", Implementation: "test-v1",
	}
}

func TestLegacyAdapterOwnsCompatibilitySchemaValidation(t *testing.T) {
	value := NewLegacyAdapter(testDescriptor(), func(context.Context, RawArguments, tools.ExecutionBinding, ExecutionSink) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"uri": "vm:tenant:one"}}, nil
	})
	if _, err := value.Invoke(context.Background(), RawArguments{"name": "one"}, tools.ExecutionBinding{SchemaVersion: tools.ExecutionBindingSchemaVersion}, nil); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if _, err := value.Invoke(context.Background(), RawArguments{"vmName": "one"}, tools.ExecutionBinding{SchemaVersion: tools.ExecutionBindingSchemaVersion}, nil); err != nil {
		t.Fatalf("product vmName alias rejected: %v", err)
	}
	if _, err := value.Invoke(context.Background(), RawArguments{}, tools.ExecutionBinding{SchemaVersion: tools.ExecutionBindingSchemaVersion}, nil); err == nil {
		t.Fatal("missing required input accepted")
	}
	observation, err := value.ValidateResult(context.Background(), &mcp.CallToolResult{StructuredContent: map[string]any{"uri": "vm:tenant:one"}})
	if err != nil || observation.Status != "success" {
		t.Fatalf("valid output observation = %+v, err=%v", observation, err)
	}
	if _, err := value.ValidateResult(context.Background(), &mcp.CallToolResult{StructuredContent: map[string]any{"wrong": true}}); err == nil {
		t.Fatal("malformed structured output accepted")
	}
}

func TestLegacyAdapterDoesNotAliasVmNameWhenNameIsASiblingRequiredField(t *testing.T) {
	definition := tools.CapabilityDescriptor{
		OperationID: "test.secret",
		Name:        "put_k8s_secret",
		Version:     1,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"vmName", "name", "data"},
			"properties": map[string]any{
				"vmName": map[string]any{"type": "string"},
				"name":   map[string]any{"type": "string"},
				"data":   map[string]any{"type": "object"},
			},
		},
		OutputSchema: map[string]any{"type": "object"},
		Effect:       "write", Provider: "test", Implementation: "test-v1",
	}
	value := NewLegacyAdapter(definition, func(context.Context, RawArguments, tools.ExecutionBinding, ExecutionSink) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{}}, nil
	})
	if _, err := value.Invoke(context.Background(), RawArguments{"vmName": "guest-1", "data": map[string]any{"k": "v"}}, tools.ExecutionBinding{SchemaVersion: tools.ExecutionBindingSchemaVersion}, nil); err == nil {
		t.Fatal("missing secret name accepted via vmName alias")
	}
}

func TestNativeAdapterPreservesOpaqueArgumentsForCapabilityValidation(t *testing.T) {
	called := false
	value := NewAdapter(testDescriptor(), func(_ context.Context, args RawArguments, _ tools.ExecutionBinding, _ ExecutionSink) (*mcp.CallToolResult, error) {
		called = true
		if _, ok := args["providerSpecific"]; !ok {
			t.Fatal("native adapter changed opaque arguments")
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{"anything": true}}, nil
	}, func(_ context.Context, result *mcp.CallToolResult) (CapabilityObservation, error) {
		return PassThroughObservation(testDescriptor(), result)
	})
	if _, err := value.Invoke(context.Background(), RawArguments{"providerSpecific": map[string]any{"mode": "future"}}, tools.ExecutionBinding{}, nil); err != nil {
		t.Fatalf("opaque input rejected by generic adapter: %v", err)
	}
	if !called {
		t.Fatal("native capability was not invoked")
	}
}

func TestProviderAdapterLeavesInputToProviderAndGuardsStructuredOutput(t *testing.T) {
	definition := testDescriptor()
	called := false
	value := NewProviderAdapter(definition, func(_ context.Context, args RawArguments, _ tools.ExecutionBinding, _ ExecutionSink) (*mcp.CallToolResult, error) {
		called = true
		if _, ok := args["providerSpecific"]; !ok {
			t.Fatal("provider input was rewritten before the provider call")
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{"wrong": true}}, nil
	})
	if _, err := value.Invoke(context.Background(), RawArguments{"providerSpecific": true}, tools.ExecutionBinding{}, nil); err != nil {
		t.Fatalf("provider input was rejected by the host adapter: %v", err)
	}
	if !called {
		t.Fatal("provider operation was not invoked")
	}
	if _, err := value.ValidateResult(context.Background(), &mcp.CallToolResult{StructuredContent: map[string]any{"wrong": true}}); err == nil {
		t.Fatal("provider output outside its declared schema was recorded")
	}
}

func TestPassThroughObservationDoesNotInferResources(t *testing.T) {
	definition := testDescriptor()
	observation, err := PassThroughObservation(definition, &mcp.CallToolResult{
		StructuredContent: map[string]any{"uri": "vm:tenant:one", "status": "ready"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Resources) != 0 || len(observation.Facts) != 0 {
		t.Fatalf("compatibility observation inferred semantic data: %+v", observation)
	}
}
