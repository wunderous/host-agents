package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/pkg/hostagentclient"
)

// Entity is a server-returned entity with its original canonical fields. The
// client never fabricates an ID from a display name.
type Entity struct {
	Kind   string
	Name   string
	URI    string
	Fields map[string]any
}

type EntityBinding struct {
	Operation       string
	Argument        string
	EntityKind      string
	EntityName      string
	URI             string
	CatalogRevision string
}

type OutputBinding struct {
	Operation       string
	Output          string
	CatalogRevision string
	ObservationRev  string
	State           string
}

type DraftValue struct {
	Raw     string
	Typed   any
	Source  string
	Valid   bool
	Message string
}

type CommandDraft struct {
	Operation       string
	Arguments       map[string]any
	CatalogRevision string
	Bindings        []EntityBinding
	Outputs         []OutputBinding
}

type ExecutionReceipt struct {
	Operation       string
	Arguments       map[string]any
	CatalogRevision string
	Bindings        []EntityBinding
	Outputs         []OutputBinding
	Result          *mcp.CallToolResult
}

type Executor struct {
	Client  *hostagentclient.Client
	Catalog hostagentclient.CatalogSnapshot
}

func NewExecutor(client *hostagentclient.Client) *Executor {
	return &Executor{Client: client}
}

func (e *Executor) Refresh(ctx context.Context) error {
	if e == nil || e.Client == nil {
		return fmt.Errorf("host agent client is required")
	}
	catalog, err := e.Client.Catalog(ctx)
	if err != nil {
		return err
	}
	e.Catalog = catalog
	return nil
}

func (e *Executor) ListVMs(ctx context.Context) ([]Entity, error) {
	result, err := e.call(ctx, "list_vms", map[string]any{})
	if err != nil {
		return nil, err
	}
	return decodeEntities(result, "vm")
}

func (e *Executor) GetVMInfo(ctx context.Context, binding EntityBinding) (map[string]any, error) {
	if strings.TrimSpace(binding.URI) == "" || binding.EntityKind != "vm" {
		return nil, fmt.Errorf("a canonical vm entity binding is required")
	}
	if binding.CatalogRevision != "" && binding.CatalogRevision != e.Catalog.Revision {
		return nil, fmt.Errorf("entity binding uses stale catalog revision %q; current is %q", binding.CatalogRevision, e.Catalog.Revision)
	}
	result, err := e.call(ctx, "get_vm_info", map[string]any{"uri": binding.URI})
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(resultBytes(result), &object); err != nil {
		return nil, fmt.Errorf("decode get_vm_info result: %w", err)
	}
	return object, nil
}

func (e *Executor) ValidateDraft(draft CommandDraft) (hostagentclient.CapabilityDescriptor, error) {
	if e == nil || e.Client == nil {
		return hostagentclient.CapabilityDescriptor{}, fmt.Errorf("host agent client is required")
	}
	if strings.TrimSpace(draft.Operation) == "" {
		return hostagentclient.CapabilityDescriptor{}, fmt.Errorf("command operation is required")
	}
	if draft.CatalogRevision == "" || draft.CatalogRevision != e.Catalog.Revision {
		return hostagentclient.CapabilityDescriptor{}, fmt.Errorf("command uses stale catalog revision %q; current is %q", draft.CatalogRevision, e.Catalog.Revision)
	}
	descriptor, ok := e.Catalog.Find(draft.Operation)
	if !ok {
		return hostagentclient.CapabilityDescriptor{}, fmt.Errorf("operation %q is unavailable in catalog revision %q", draft.Operation, e.Catalog.Revision)
	}
	if descriptor.Effect == "" {
		return hostagentclient.CapabilityDescriptor{}, fmt.Errorf("operation %q has no effect classification", draft.Operation)
	}
	for _, binding := range draft.Bindings {
		if binding.CatalogRevision != "" && binding.CatalogRevision != e.Catalog.Revision {
			return hostagentclient.CapabilityDescriptor{}, fmt.Errorf("binding for %q uses stale catalog revision %q", binding.Argument, binding.CatalogRevision)
		}
		if strings.TrimSpace(binding.URI) == "" || strings.TrimSpace(binding.Argument) == "" {
			return hostagentclient.CapabilityDescriptor{}, fmt.Errorf("binding %q must contain a canonical entity and argument", binding.Argument)
		}
	}
	for _, output := range draft.Outputs {
		if output.CatalogRevision != "" && output.CatalogRevision != e.Catalog.Revision {
			return hostagentclient.CapabilityDescriptor{}, fmt.Errorf("output binding for %q uses stale catalog revision %q", output.Output, output.CatalogRevision)
		}
		if output.State == "unavailable" || output.State == "incompatible" || output.State == "stale" {
			return hostagentclient.CapabilityDescriptor{}, fmt.Errorf("output binding %q is %s", output.Output, output.State)
		}
	}
	for required := range requiredProperties(descriptor.InputSchema) {
		if _, ok := draft.Arguments[required]; !ok {
			return hostagentclient.CapabilityDescriptor{}, fmt.Errorf("operation %q is missing required argument %q", draft.Operation, required)
		}
	}
	return descriptor, nil
}

// ExecuteDraft performs exactly one MCP call after catalog and binding
// validation. Provenance stays in the receipt; unsupported metadata is never
// smuggled into a provider operation's argument schema.
func (e *Executor) ExecuteDraft(ctx context.Context, draft CommandDraft) (*ExecutionReceipt, error) {
	if _, err := e.ValidateDraft(draft); err != nil {
		return nil, err
	}
	result, err := e.Client.Call(ctx, draft.Operation, draft.Arguments)
	if err != nil {
		return nil, err
	}
	if result == nil || result.IsError {
		return nil, fmt.Errorf("Host Agent operation %q failed", draft.Operation)
	}
	return &ExecutionReceipt{Operation: draft.Operation, Arguments: cloneMap(draft.Arguments), CatalogRevision: e.Catalog.Revision, Bindings: append([]EntityBinding(nil), draft.Bindings...), Outputs: append([]OutputBinding(nil), draft.Outputs...), Result: result}, nil
}

func ParseValue(raw string) DraftValue {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DraftValue{Raw: raw, Valid: false, Message: "value is empty"}
	}
	if value, err := strconv.ParseBool(raw); err == nil {
		return DraftValue{Raw: raw, Typed: value, Source: "literal", Valid: true}
	}
	if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return DraftValue{Raw: raw, Typed: value, Source: "literal", Valid: true}
	}
	return DraftValue{Raw: raw, Typed: raw, Source: "literal", Valid: true}
}

func requiredProperties(schema map[string]any) map[string]struct{} {
	result := map[string]struct{}{}
	values, _ := schema["required"].([]any)
	for _, value := range values {
		if name, ok := value.(string); ok {
			result[name] = struct{}{}
		}
	}
	if names, ok := schema["required"].([]string); ok {
		for _, name := range names {
			result[name] = struct{}{}
		}
	}
	return result
}

func cloneMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func (e *Executor) call(ctx context.Context, operation string, arguments map[string]any) (any, error) {
	if e == nil || e.Client == nil {
		return nil, fmt.Errorf("host agent client is required")
	}
	result, err := e.Client.Call(ctx, operation, arguments)
	if err != nil {
		return nil, err
	}
	if result == nil || result.IsError {
		return nil, fmt.Errorf("Host Agent operation %q failed", operation)
	}
	return result.StructuredContent, nil
}

func decodeEntities(value any, kind string) ([]Entity, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Entities []map[string]any `json:"entities"`
		VMs      []map[string]any `json:"vms"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return nil, fmt.Errorf("decode %s entities: %w", kind, err)
	}
	raw := envelope.Entities
	if len(raw) == 0 {
		raw = envelope.VMs
	}
	result := make([]Entity, 0, len(raw))
	for _, fields := range raw {
		uri, _ := fields["uri"].(string)
		if strings.TrimSpace(uri) == "" {
			return nil, fmt.Errorf("%s result contained an entity without a canonical uri", kind)
		}
		name, _ := fields["name"].(string)
		if strings.TrimSpace(name) == "" {
			name, _ = fields["vmName"].(string)
		}
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("%s result contained an entity without a canonical name", kind)
		}
		result = append(result, Entity{Kind: kind, Name: name, URI: uri, Fields: fields})
	}
	return result, nil
}

func resultBytes(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}
