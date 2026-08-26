package hostmcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/internal/selectors"
	"github.com/wunderous/host-agents/internal/tools"
)

// validateProducedResources is the execution-side counterpart to catalog
// binding validation. A schema can prove that a field exists, but only the
// returned value can prove that it is a canonical, tenant-local identity of
// the declared kind. This keeps provider output admission fail-closed and
// leaves the URI opaque to the TUI.
func validateProducedResources(descriptor tools.CapabilityDescriptor, structured any, tenantID string) error {
	if len(descriptor.Produces) == 0 {
		return nil
	}
	encoded, err := json.Marshal(structured)
	if err != nil {
		return fmt.Errorf("marshal produced resource output: %w", err)
	}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		return fmt.Errorf("decode produced resource output: %w", err)
	}
	allowedByPath := make(map[string]map[string]bool)
	selectorByPath := make(map[string]string)
	for _, binding := range descriptor.Produces {
		path := strings.TrimSpace(binding.SourcePath)
		kind := strings.TrimSpace(binding.ResourceType)
		if path == "" || kind == "" {
			return fmt.Errorf("capability %q has an incomplete produced resource binding", descriptor.OperationID)
		}
		kinds := allowedByPath[path]
		if kinds == nil {
			kinds = make(map[string]bool)
			allowedByPath[path] = kinds
		}
		kinds[kind] = true
		if binding.SelectorID != "" {
			selectorByPath[path] = binding.SelectorID
		}
	}
	for path, allowedKinds := range allowedByPath {
		values, found := valuesAtPath(document, strings.Split(path, "."))
		if selectorID := selectorByPath[path]; selectorID != "" {
			candidates, err := selectors.Evaluate(document, descriptor.OutputType, selectorID, descriptor.ResultTypes)
			if err != nil {
				return fmt.Errorf("capability %q selector %q failed: %w", descriptor.OperationID, selectorID, err)
			}
			values = make([]any, 0, len(candidates))
			for _, candidate := range candidates {
				values = append(values, candidate.Value)
			}
			found = true
		}
		if !found || len(values) == 0 {
			return fmt.Errorf("capability %q did not return declared resource output %q", descriptor.OperationID, path)
		}
		for _, value := range values {
			uri, ok := value.(string)
			if !ok {
				return fmt.Errorf("capability %q produced non-string resource output %q", descriptor.OperationID, path)
			}
			parsed, err := resourceid.Parse(uri)
			if err != nil {
				return fmt.Errorf("capability %q produced invalid resource output %q: %w", descriptor.OperationID, path, err)
			}
			if parsed.TenantID != tenantID {
				return fmt.Errorf("capability %q produced foreign-tenant resource output %q", descriptor.OperationID, path)
			}
			if !allowedKinds[parsed.ResourceType] {
				return fmt.Errorf("capability %q produced resource kind %q at %q; declared kinds are %v", descriptor.OperationID, parsed.ResourceType, path, sortedKinds(allowedKinds))
			}
		}
	}
	return nil
}

func valuesAtPath(value any, segments []string) ([]any, bool) {
	if len(segments) == 0 {
		return []any{value}, true
	}
	segment := segments[0]
	if strings.HasSuffix(segment, "[]") {
		key := strings.TrimSuffix(segment, "[]")
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		items, ok := object[key].([]any)
		if !ok {
			return nil, false
		}
		var values []any
		for _, item := range items {
			found, itemOK := valuesAtPath(item, segments[1:])
			if !itemOK {
				return nil, false
			}
			values = append(values, found...)
		}
		return values, true
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	next, ok := object[segment]
	if !ok {
		return nil, false
	}
	return valuesAtPath(next, segments[1:])
}

func sortedKinds(kinds map[string]bool) []string {
	result := make([]string, 0, len(kinds))
	for kind := range kinds {
		result = append(result, kind)
	}
	// Resource kinds are small and stable; avoid adding a package-level sort
	// dependency to the hot dispatch path by using the canonical registry order.
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j] < result[j-1]; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result
}
