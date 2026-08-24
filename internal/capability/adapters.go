package capability

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/plan"
	"github.com/wunderous/host-agents/internal/tools"
)

type adapter struct {
	definition tools.CapabilityDescriptor
	invoke     func(context.Context, RawArguments, ExecutionSink) (*mcp.CallToolResult, error)
	validate   func(context.Context, *mcp.CallToolResult) (CapabilityObservation, error)
}

func (a adapter) Definition() tools.CapabilityDescriptor { return a.definition }

func (a adapter) Invoke(ctx context.Context, args RawArguments, sink ExecutionSink) (*mcp.CallToolResult, error) {
	if err := plan.ValidateJSON(a.definition.InputSchema, map[string]any(args)); err != nil {
		return nil, fmt.Errorf("invalid capability arguments: %w", err)
	}
	return a.invoke(ctx, args, sink)
}

func (a adapter) ValidateResult(ctx context.Context, result *mcp.CallToolResult) (CapabilityObservation, error) {
	if result == nil {
		return CapabilityObservation{}, fmt.Errorf("capability returned a nil result")
	}
	if !result.IsError {
		structured, err := jsonRoundTrip(result.StructuredContent)
		if err != nil {
			return CapabilityObservation{}, fmt.Errorf("structured result is not JSON-serializable: %w", err)
		}
		if err := plan.ValidateJSON(a.definition.OutputSchema, structured); err != nil {
			return CapabilityObservation{}, fmt.Errorf("structured result does not match output schema: %w", err)
		}
	}
	return a.validate(ctx, result)
}

func jsonRoundTrip(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// NewLegacyAdapter gives an existing trusted host implementation the same
// capability boundary while it is migrated to a native capability module.
// The adapter owns result handling; the registry and orchestrator only see the
// interface.
func NewLegacyAdapter(
	definition tools.CapabilityDescriptor,
	invoke func(context.Context, RawArguments, ExecutionSink) (*mcp.CallToolResult, error),
) Capability {
	return adapter{
		definition: definition,
		invoke:     invoke,
		validate: func(_ context.Context, result *mcp.CallToolResult) (CapabilityObservation, error) {
			return PassThroughObservation(definition, result)
		},
	}
}

// NewAdapter creates a capability with an implementation-owned validator.
// This is used by native capabilities and dynamic provider wrappers.
func NewAdapter(
	definition tools.CapabilityDescriptor,
	invoke func(context.Context, RawArguments, ExecutionSink) (*mcp.CallToolResult, error),
	validate func(context.Context, *mcp.CallToolResult) (CapabilityObservation, error),
) Capability {
	if validate == nil {
		validate = func(_ context.Context, result *mcp.CallToolResult) (CapabilityObservation, error) {
			return PassThroughObservation(definition, result)
		}
	}
	return adapter{definition: definition, invoke: invoke, validate: validate}
}
