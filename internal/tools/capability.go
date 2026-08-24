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
	OperationID         string              `json:"operationId"`
	Version             int                 `json:"version,omitempty"`
	Name                string              `json:"name"`
	Title               string              `json:"title,omitempty"`
	Description         string              `json:"description,omitempty"`
	InputSchema         map[string]any      `json:"inputSchema"`
	OutputSchema        map[string]any      `json:"outputSchema,omitempty"`
	Effect              string              `json:"effect"`
	Privilege           string              `json:"privilege,omitempty"`
	RequiresApproval    bool                `json:"requiresApproval"`
	Provider            string              `json:"provider"`
	Implementation      string              `json:"implementation"`
	ResourceKinds       []string            `json:"resourceKinds,omitempty"`
	RequiredFields      []string            `json:"requiredFields,omitempty"`
	ProducedObservables []string            `json:"producedObservables,omitempty"`
	ArgumentProducers   map[string][]string `json:"argumentProducers,omitempty"`
	DefaultLabels       map[string]string   `json:"defaultLabels,omitempty"`
	GateMessage         string              `json:"gateMessage,omitempty"`
	Consequence         string              `json:"consequence,omitempty"`
	Idempotent          bool                `json:"idempotent"`
	SupportsReadiness   bool                `json:"supportsReadiness"`
	ValidationSchema    string              `json:"validationSchema,omitempty"`
	GenerationID        string              `json:"generationId,omitempty"`
	Requires            []ResourceBinding   `json:"requires,omitempty"`
	Produces            []ResourceBinding   `json:"produces,omitempty"`
}

// ResourceBinding is a declarative public relationship owned by the
// capability. It is never inferred from generic field names or URI-shaped
// strings by the orchestrator.
type ResourceBinding struct {
	Argument     string `json:"argument,omitempty"`
	ResourceType string `json:"resourceType"`
	SourcePath   string `json:"sourcePath,omitempty"`
	Required     bool   `json:"required,omitempty"`
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

// CloneCapabilityCatalogSnapshot returns an ownership-safe copy. Catalog
// revisions are immutable contracts; callers must not be able to mutate the
// schema maps or binding slices behind a previously published revision.
func CloneCapabilityCatalogSnapshot(snapshot CapabilityCatalogSnapshot) CapabilityCatalogSnapshot {
	clone := CapabilityCatalogSnapshot{ProviderID: snapshot.ProviderID, Revision: snapshot.Revision, Tools: make([]CapabilityDescriptor, 0, len(snapshot.Tools))}
	for _, descriptor := range snapshot.Tools {
		clone.Tools = append(clone.Tools, CloneCapabilityDescriptor(descriptor))
	}
	return clone
}

func CloneCapabilityDescriptor(descriptor CapabilityDescriptor) CapabilityDescriptor {
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return descriptor
	}
	var clone CapabilityDescriptor
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return descriptor
	}
	return clone
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
	applyCatalogDefaults(descriptors)

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
	gateMessage := metaString(def.Meta, "gateMessage", "")
	consequence := metaString(def.Meta, "consequence", "")
	if effect != "read" {
		if gateMessage == "" {
			gateMessage = "Host approval is required before this capability can execute."
		}
		if consequence == "" {
			consequence = "This capability may change host state (effect: " + effect + ")."
		}
	}
	return CapabilityDescriptor{
		OperationID:         def.Name,
		Version:             1,
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
		ArgumentProducers:   metaStringListMap(def.Meta, "argumentProducers"),
		DefaultLabels:       metaStringMap(def.Meta, "defaultLabels"),
		GateMessage:         metaString(def.Meta, "gateMessage", ""),
		Consequence:         consequence,
		Idempotent:          effect == "read" || metaBool(def.Meta, "idempotent"),
		SupportsReadiness:   metaBool(def.Meta, "supportsReadiness") || effect != "read",
		Requires:            explicitResourceBindings(def.Meta, "requires"),
		Produces:            explicitResourceBindings(def.Meta, "produces"),
	}
}

// CapabilityDescriptorFromDefinition converts one public tool definition into
// the immutable descriptor used by the capability registry.
func CapabilityDescriptorFromDefinition(providerID string, def ToolDefinition) CapabilityDescriptor {
	return capabilityDescriptor(NormalizeProviderID(providerID), def)
}

func applyCatalogDefaults(descriptors []CapabilityDescriptor) {
	for index := range descriptors {
		if descriptors[index].DefaultLabels == nil {
			descriptors[index].DefaultLabels = defaultLabels(descriptors[index].InputSchema)
		}
	}
}

func schemaHasProperty(schema map[string]any, name string) bool {
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = properties[name]
	return ok
}

func defaultLabels(schema map[string]any) map[string]string {
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}
	labels := make(map[string]string)
	for name, raw := range properties {
		property, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, exists := property["default"]; exists {
			labels[name] = "default"
		}
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

func metaStringMap(meta map[string]any, key string) map[string]string {
	raw, ok := meta[key].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for field, value := range raw {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			out[field] = text
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func metaStringListMap(meta map[string]any, key string) map[string][]string {
	raw, ok := meta[key].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string][]string, len(raw))
	for field, value := range raw {
		var values []string
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					values = append(values, text)
				}
			}
		case []string:
			values = append(values, typed...)
		}
		if len(values) > 0 {
			out[field] = values
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

func explicitResourceBindings(meta map[string]any, key string) []ResourceBinding {
	if meta == nil {
		return nil
	}
	raw, ok := meta[key]
	if !ok {
		return nil
	}
	var items []any
	switch typed := raw.(type) {
	case []any:
		items = typed
	case []map[string]any:
		for _, item := range typed {
			items = append(items, item)
		}
	default:
		return nil
	}
	bindings := make([]ResourceBinding, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		resourceType, _ := object["resourceType"].(string)
		if strings.TrimSpace(resourceType) == "" {
			continue
		}
		binding := ResourceBinding{ResourceType: resourceType}
		binding.Argument, _ = object["argument"].(string)
		binding.SourcePath, _ = object["sourcePath"].(string)
		binding.Required, _ = object["required"].(bool)
		bindings = append(bindings, binding)
	}
	if len(bindings) == 0 {
		return nil
	}
	return bindings
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
