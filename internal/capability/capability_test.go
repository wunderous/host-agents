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

func TestAdapterOwnsInputAndOutputSchemaValidation(t *testing.T) {
	value := NewAdapter(testDescriptor(), func(context.Context, RawArguments, ExecutionSink) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"uri": "vm:tenant:one"}}, nil
	}, nil)
	if _, err := value.Invoke(context.Background(), RawArguments{"name": "one"}, nil); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if _, err := value.Invoke(context.Background(), RawArguments{}, nil); err == nil {
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
