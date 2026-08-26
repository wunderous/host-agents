package capability

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/plan"
	"github.com/wunderous/host-agents/internal/tools"
)

type adapter struct {
	definition   tools.CapabilityDescriptor
	invoke       func(context.Context, RawArguments, tools.ExecutionBinding, ExecutionSink) (*mcp.CallToolResult, error)
	validateArgs func(context.Context, RawArguments) error
	validate     func(context.Context, *mcp.CallToolResult) (CapabilityObservation, error)
}

func (a adapter) Definition() tools.CapabilityDescriptor { return a.definition }

func (a adapter) Invoke(ctx context.Context, args RawArguments, binding tools.ExecutionBinding, sink ExecutionSink) (*mcp.CallToolResult, error) {
	if a.validateArgs != nil {
		if err := a.validateArgs(ctx, args); err != nil {
			return nil, tools.NewCapabilityError("capability", "invalid_arguments", fmt.Errorf("invalid capability arguments: %w", err))
		}
	}
	return a.invoke(ctx, args, binding, sink)
}

func (a adapter) ValidateResult(ctx context.Context, result *mcp.CallToolResult) (CapabilityObservation, error) {
	if result == nil {
		return CapabilityObservation{}, fmt.Errorf("capability returned a nil result")
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
	invoke func(context.Context, RawArguments, tools.ExecutionBinding, ExecutionSink) (*mcp.CallToolResult, error),
) Capability {
	return adapter{
		definition: definition,
		invoke:     invoke,
		// Legacy handlers have not yet been split into native capability
		// modules. Keep their old declarative schema gate local to this
		// compatibility wrapper; the orchestrator never calls ValidateJSON.
		validateArgs: func(_ context.Context, args RawArguments) error {
			return plan.ValidateJSON(definition.InputSchema, argumentsForSchemaValidation(definition.InputSchema, args))
		},
		validate: func(_ context.Context, result *mcp.CallToolResult) (CapabilityObservation, error) {
			if result != nil && !result.IsError {
				structured, err := jsonRoundTrip(result.StructuredContent)
				if err != nil {
					return CapabilityObservation{}, fmt.Errorf("structured result is not JSON-serializable: %w", err)
				}
				if err := plan.ValidateJSON(definition.OutputSchema, structured); err != nil {
					return CapabilityObservation{}, fmt.Errorf("structured result does not match output schema: %w", err)
				}
			}
			return PassThroughObservation(definition, result)
		},
	}
}

// NewAdapter creates a capability with an implementation-owned validator.
// This is used by native capabilities and dynamic provider wrappers.
func NewAdapter(
	definition tools.CapabilityDescriptor,
	invoke func(context.Context, RawArguments, tools.ExecutionBinding, ExecutionSink) (*mcp.CallToolResult, error),
	validate func(context.Context, *mcp.CallToolResult) (CapabilityObservation, error),
) Capability {
	if validate == nil {
		// A native capability may deliberately accept provider-defined input
		// and output shapes. Its Invoke and ValidateResult methods remain the
		// owner of those rules; this fallback only preserves the old opaque
		// observation compatibility behavior.
		validate = func(_ context.Context, result *mcp.CallToolResult) (CapabilityObservation, error) {
			return PassThroughObservation(definition, result)
		}
	}
	return adapter{definition: definition, invoke: invoke, validate: validate}
}

// NewProviderAdapter wraps an operation whose input and output contracts are
// enforced by the provider MCP operation. The host performs the declared
// output-schema check inside the provider capability's ValidateResult hook so
// malformed structured content cannot become a recorded observation. The
// dispatch/orchestrator path remains schema-opaque.
func NewProviderAdapter(
	definition tools.CapabilityDescriptor,
	invoke func(context.Context, RawArguments, tools.ExecutionBinding, ExecutionSink) (*mcp.CallToolResult, error),
) Capability {
	return NewAdapter(definition, invoke, func(_ context.Context, result *mcp.CallToolResult) (CapabilityObservation, error) {
		if result == nil {
			return CapabilityObservation{}, fmt.Errorf("provider returned a nil result")
		}
		if !result.IsError {
			structured, err := jsonRoundTrip(result.StructuredContent)
			if err != nil {
				return CapabilityObservation{}, fmt.Errorf("provider structured result is not JSON-serializable: %w", err)
			}
			if err := plan.ValidateJSON(definition.OutputSchema, structured); err != nil {
				return CapabilityObservation{}, fmt.Errorf("provider structured result does not match output schema: %w", err)
			}
		}
		return PassThroughObservation(definition, result)
	})
}

// argumentsForSchemaValidation copies the product/control-plane `vmName`
// alias onto `name` when the capability schema requires an instance `name`
// and does not also require `vmName` as a distinct field (for example
// Kubernetes Secret `name`). Dispatch already accepts either spelling;
// this keeps the legacy JSON-schema gate aligned with that contract.
func argumentsForSchemaValidation(schema map[string]any, args RawArguments) map[string]any {
	cloned := cloneRawArguments(args)
	if !schemaRequires(schema, "name") || schemaRequires(schema, "vmName") {
		return cloned
	}
	if name, _ := cloned["name"].(string); strings.TrimSpace(name) != "" {
		return cloned
	}
	if vmName, ok := cloned["vmName"].(string); ok && strings.TrimSpace(vmName) != "" {
		cloned["name"] = vmName
	}
	return cloned
}

func schemaRequires(schema map[string]any, field string) bool {
	if schema == nil {
		return false
	}
	switch values := schema["required"].(type) {
	case []string:
		for _, item := range values {
			if item == field {
				return true
			}
		}
	case []any:
		for _, item := range values {
			if text, ok := item.(string); ok && text == field {
				return true
			}
		}
	}
	return false
}

func cloneRawArguments(args RawArguments) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
}
