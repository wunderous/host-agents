package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CapabilityDescriptor is the stable, client-facing metadata for one host
// operation. The executable dispatch name remains Name; OperationID is a
// separate field so callers can persist a stable identity without treating
// presentation text as executable input.
type CapabilityDescriptor struct {
	OperationID         string         `json:"operationId"`
	Name                string         `json:"name"`
	Title               string         `json:"title,omitempty"`
	Description         string         `json:"description,omitempty"`
	InputSchema         map[string]any `json:"inputSchema"`
	OutputSchema        map[string]any `json:"outputSchema,omitempty"`
	Effect              string         `json:"effect"`
	Privilege           string         `json:"privilege,omitempty"`
	RequiresApproval    bool           `json:"requiresApproval"`
	Provider            string         `json:"provider"`
	Implementation      string         `json:"implementation"`
	ResourceKinds       []string       `json:"resourceKinds,omitempty"`
	RequiredFields      []string       `json:"requiredFields,omitempty"`
	ProducedObservables []string       `json:"producedObservables,omitempty"`
	Idempotent          bool           `json:"idempotent"`
	SupportsReadiness   bool           `json:"supportsReadiness"`
}

// CapabilityCatalogSnapshot is immutable once returned to a caller. The
// revision is derived from canonical descriptor JSON and is therefore safe to
// carry in plans and client caches; a dynamic registration publishes a new
// snapshot rather than mutating an existing one.
type CapabilityCatalogSnapshot struct {
	ProviderID string                 `json:"providerId"`
	Revision   string                 `json:"catalogRevision"`
	Tools      []CapabilityDescriptor `json:"tools"`
}

// BuildCapabilityCatalog creates a deterministic snapshot from the same
// definitions that are registered with MCP. No second command table is used.
func BuildCapabilityCatalog(providerID string, defs []ToolDefinition) CapabilityCatalogSnapshot {
	normalized := NormalizeProviderID(providerID)
	byName := make(map[string]ToolDefinition, len(defs))
	for _, def := range defs {
		if strings.TrimSpace(def.Name) == "" {
			continue
		}
		if _, exists := byName[def.Name]; !exists {
			byName[def.Name] = def
		}
	}
	descriptors := make([]CapabilityDescriptor, 0, len(byName))
	for _, def := range byName {
		descriptors = append(descriptors, capabilityDescriptor(normalized, def))
	}
	sort.Slice(descriptors, func(i, j int) bool {
		return descriptors[i].OperationID < descriptors[j].OperationID
	})

	canonical := struct {
		ProviderID string                 `json:"providerId"`
		Tools      []CapabilityDescriptor `json:"tools"`
	}{ProviderID: normalized, Tools: descriptors}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		// All inputs are JSON-compatible maps. Keep a non-empty deterministic
		// revision even if a future definition violates that invariant.
		encoded = []byte(fmt.Sprintf("%s:%d", normalized, len(descriptors)))
	}
	digest := sha256.Sum256(encoded)
	return CapabilityCatalogSnapshot{
		ProviderID: normalized,
		Revision:   "sha256:" + hex.EncodeToString(digest[:]),
		Tools:      descriptors,
	}
}

func capabilityDescriptor(providerID string, def ToolDefinition) CapabilityDescriptor {
	effect := "read"
	classification := standaloneClassification(def.Name)
	if classification == "read_only" {
		classification = "read"
	}
	if classification != "" && classification != "read" {
		effect = classification
	}
	if effect == "read" && IsStandaloneMutation(def.Name) {
		effect = "mutation"
	}
	return CapabilityDescriptor{
		OperationID:         def.Name,
		Name:                def.Name,
		Title:               def.Title,
		Description:         def.Description,
		InputSchema:         def.InputSchema,
		OutputSchema:        def.OutputSchema,
		Effect:              effect,
		Privilege:           metaString(def.Meta, "privilege", effect),
		RequiresApproval:    effect != "read",
		Provider:            providerID,
		Implementation:      "host-agent:" + providerID,
		ResourceKinds:       resourceKinds(def),
		RequiredFields:      requiredFields(def.InputSchema),
		ProducedObservables: producedObservables(def),
		Idempotent:          effect == "read" || metaBool(def.Meta, "idempotent"),
		SupportsReadiness:   metaBool(def.Meta, "supportsReadiness") || effect != "read",
	}
}

func metaString(meta map[string]any, key, fallback string) string {
	if value, ok := meta[key].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func metaBool(meta map[string]any, key string) bool {
	value, _ := meta[key].(bool)
	return value
}

func requiredFields(schema map[string]any) []string {
	if schema == nil {
		return nil
	}
	if values, ok := schema["required"].([]string); ok {
		return append([]string(nil), values...)
	}
	if values, ok := schema["required"].([]any); ok {
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

func standaloneClassification(name string) string {
	metadata := StandaloneToolMetadata(name)
	opute, ok := metadata["opute"].(map[string]any)
	if !ok {
		return ""
	}
	classification, _ := opute["classification"].(string)
	return strings.TrimSpace(classification)
}

// resourceKinds and producedObservables are deliberately conservative. They
// are optional hints for client UX; identity must still come from an explicit
// observation contract rather than a name-based inference.
func resourceKinds(def ToolDefinition) []string {
	if def.Meta == nil {
		return nil
	}
	return stringSlice(def.Meta["resourceKinds"])
}

func producedObservables(def ToolDefinition) []string {
	if def.Meta == nil {
		return nil
	}
	return stringSlice(def.Meta["producedObservables"])
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

// CapabilityMeta returns the MCP _meta payload shared by standalone and
// platform registrations. Existing metadata is preserved.
func CapabilityMeta(def ToolDefinition, snapshot CapabilityCatalogSnapshot) map[string]any {
	meta := make(map[string]any, len(def.Meta)+3)
	for key, value := range def.Meta {
		meta[key] = value
	}
	for key, value := range StandaloneToolMetadata(def.Name) {
		if _, exists := meta[key]; !exists {
			meta[key] = value
		}
	}
	if snapshot.Revision != "" {
		meta["catalogRevision"] = snapshot.Revision
	}
	for _, descriptor := range snapshot.Tools {
		if descriptor.OperationID != def.Name {
			continue
		}
		meta["capability"] = descriptor
		break
	}
	return meta
}
