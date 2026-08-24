// Package capability defines the provider-neutral execution boundary for host
// capabilities. Implementations own tool-specific validation and observation
// extraction; callers consume only the public descriptor and observation
// envelope.
package capability

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/tools"
)

const ObservationSchemaVersion = "opute-capability-observation.v1"

// RawArguments preserves the exact decoded model/client arguments at the
// capability boundary. Capability implementations may validate them, but the
// orchestrator must not rewrite them before invocation.
type RawArguments map[string]any

// ExecutionSink receives bounded streaming output without exposing transport
// or provider details to the registry.
type ExecutionSink func(string)

// Capability is implemented by built-in capabilities and dynamically loaded
// provider operations. Definition is the public declarative contract;
// ValidateResult is the tool-owned semantic boundary. Invoke receives the
// unchanged raw arguments plus the typed execution binding; the orchestrator
// never rewrites arguments or hides routing facts inside them.
type Capability interface {
	Definition() tools.CapabilityDescriptor
	Invoke(context.Context, RawArguments, tools.ExecutionBinding, ExecutionSink) (*mcp.CallToolResult, error)
	ValidateResult(context.Context, *mcp.CallToolResult) (CapabilityObservation, error)
}

// CapabilityRegistry is the provider-neutral registry contract consumed by
// lifecycle code. Implementations publish immutable catalog revisions and
// never expose provider internals to callers.
type CapabilityRegistry interface {
	Register(Capability) error
	ReplaceGeneration(string, []Capability) error
	Resolve(string, string) (Capability, error)
	Snapshot() tools.CapabilityCatalogSnapshot
}

type CapabilityObservation struct {
	SchemaVersion     string                `json:"schemaVersion"`
	OperationID       string                `json:"operationId"`
	CapabilityVersion int                   `json:"capabilityVersion"`
	Status            string                `json:"status"`
	Structured        json.RawMessage       `json:"structured,omitempty"`
	Facts             []ObservationFact     `json:"facts,omitempty"`
	Resources         []ResourceObservation `json:"resources,omitempty"`
	Evidence          []EvidenceRecord      `json:"evidence,omitempty"`
	Retryability      string                `json:"retryability,omitempty"`
	CatalogRevision   string                `json:"catalogRevision,omitempty"`
	GenerationID      string                `json:"generationId,omitempty"`
}

type ObservationFact struct {
	Type       string          `json:"type"`
	SourcePath string          `json:"sourcePath,omitempty"`
	Value      json.RawMessage `json:"value"`
}

type ResourceObservation struct {
	URI          string `json:"uri"`
	ResourceType string `json:"resourceType"`
	SourcePath   string `json:"sourcePath,omitempty"`
}

type EvidenceRecord struct {
	Kind     string          `json:"kind"`
	Value    json.RawMessage `json:"value,omitempty"`
	Redacted bool            `json:"redacted,omitempty"`
}

// PassThroughObservation is a deliberately narrow compatibility adapter for
// legacy built-in handlers. New capabilities should provide their own
// validator. It records structured content but does not infer resources,
// identifiers, or semantic completion.
func PassThroughObservation(def tools.CapabilityDescriptor, result *mcp.CallToolResult) (CapabilityObservation, error) {
	observation := CapabilityObservation{
		SchemaVersion:     ObservationSchemaVersion,
		OperationID:       def.OperationID,
		CapabilityVersion: def.Version,
		Status:            "success",
	}
	if result == nil {
		observation.Status = "error"
		observation.Retryability = "unknown"
		return observation, nil
	}
	if result.IsError {
		observation.Status = "error"
		observation.Retryability = "capability"
	}
	if result.StructuredContent != nil {
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return CapabilityObservation{}, err
		}
		observation.Structured = encoded
	}
	return observation, nil
}
