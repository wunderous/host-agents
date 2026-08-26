package selectors

import (
	"fmt"
	"strings"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
)

type Candidate struct {
	Value      any
	Label      string
	Indices    []int
	SourcePath string
}

func Validate(outputSchema map[string]any, outputType string, resultTypes []capabilitycontract.ResultType) error {
	if len(resultTypes) == 0 {
		if strings.TrimSpace(outputType) != "" {
			return fmt.Errorf("output type %q has no result type descriptor", outputType)
		}
		return nil
	}
	if strings.TrimSpace(outputType) == "" {
		return fmt.Errorf("result selectors require an output type")
	}
	seenTypes := make(map[string]bool, len(resultTypes))
	for _, resultType := range resultTypes {
		if strings.TrimSpace(resultType.ID) == "" || resultType.Version < 1 {
			return fmt.Errorf("result types require a non-empty id and positive version")
		}
		if seenTypes[resultType.ID] {
			return fmt.Errorf("duplicate result type %q", resultType.ID)
		}
		seenTypes[resultType.ID] = true
		seenSelectors := make(map[string]bool, len(resultType.Selectors))
		for _, selector := range resultType.Selectors {
			if strings.TrimSpace(selector.ID) == "" || strings.TrimSpace(selector.SourcePath) == "" {
				return fmt.Errorf("result type %q has an incomplete selector", resultType.ID)
			}
			if seenSelectors[selector.ID] {
				return fmt.Errorf("result type %q has duplicate selector %q", resultType.ID, selector.ID)
			}
			seenSelectors[selector.ID] = true
			cardinality := selector.NormalizedCardinality()
			if cardinality != capabilitycontract.CardinalityOne && cardinality != capabilitycontract.CardinalityMany {
				return fmt.Errorf("selector %q has unsupported cardinality %q", selector.ID, selector.Cardinality)
			}
			if !schemaPathExists(outputSchema, selector.SourcePath) {
				return fmt.Errorf("selector %q sourcePath %q is absent from the output schema", selector.ID, selector.SourcePath)
			}
			if selector.LabelPath != "" && !schemaPathExists(outputSchema, selector.LabelPath) {
				return fmt.Errorf("selector %q labelPath %q is absent from the output schema", selector.ID, selector.LabelPath)
			}
		}
	}
	if strings.TrimSpace(outputType) != "" && !seenTypes[outputType] {
		return fmt.Errorf("output type %q is not declared", outputType)
	}
	return nil
}

func Evaluate(value any, outputType, selectorID string, resultTypes []capabilitycontract.ResultType) ([]Candidate, error) {
	selector, ok := Find(outputType, selectorID, resultTypes)
	if !ok {
		return nil, fmt.Errorf("selector %q is not declared for result type %q", selectorID, outputType)
	}
	values := pathValues(value, strings.Split(selector.SourcePath, "."), nil)
	if selector.NormalizedCardinality() == capabilitycontract.CardinalityOne && len(values) > 1 {
		return nil, fmt.Errorf("selector %q expected one value but found %d", selector.ID, len(values))
	}
	result := make([]Candidate, 0, len(values))
	for _, item := range values {
		label := ""
		if selector.LabelPath != "" {
			labelValue := indexedValue(value, strings.Split(selector.LabelPath, "."), item.Indices)
			if text, ok := labelValue.(string); ok {
				label = text
			}
		}
		result = append(result, Candidate{Value: item.Value, Label: label, Indices: append([]int(nil), item.Indices...), SourcePath: selector.SourcePath})
	}
	return result, nil
}

func Find(outputType, selectorID string, resultTypes []capabilitycontract.ResultType) (capabilitycontract.ResultSelector, bool) {
	for _, resultType := range resultTypes {
		if resultType.ID != outputType {
			continue
		}
		for _, selector := range resultType.Selectors {
			if selector.ID == selectorID {
				return selector, true
			}
		}
	}
	return capabilitycontract.ResultSelector{}, false
}

type pathValue struct {
	Value   any
	Indices []int
}

func pathValues(value any, segments []string, indices []int) []pathValue {
	if len(segments) == 0 {
		return []pathValue{{Value: value, Indices: indices}}
	}
	segment := segments[0]
	if strings.HasSuffix(segment, "[]") {
		key := strings.TrimSuffix(segment, "[]")
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		items, ok := object[key].([]any)
		if !ok {
			return nil
		}
		result := make([]pathValue, 0, len(items))
		for index, item := range items {
			result = append(result, pathValues(item, segments[1:], append(indices, index))...)
		}
		return result
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	next, ok := object[segment]
	if !ok {
		return nil
	}
	return pathValues(next, segments[1:], indices)
}

func indexedValue(value any, segments []string, indices []int) any {
	indexOffset := 0
	for _, segment := range segments {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		if strings.HasSuffix(segment, "[]") {
			items, ok := object[strings.TrimSuffix(segment, "[]")].([]any)
			if !ok || indexOffset >= len(indices) || indices[indexOffset] < 0 || indices[indexOffset] >= len(items) {
				return nil
			}
			value = items[indices[indexOffset]]
			indexOffset++
			continue
		}
		next, ok := object[segment]
		if !ok {
			return nil
		}
		value = next
	}
	return value
}

func schemaPathExists(schema map[string]any, path string) bool {
	current := schema
	for _, segment := range strings.Split(path, ".") {
		segment = strings.TrimSuffix(segment, "[]")
		properties, ok := current["properties"].(map[string]any)
		if !ok {
			return false
		}
		child, ok := properties[segment].(map[string]any)
		if !ok {
			return false
		}
		current = child
		if items, ok := current["items"].(map[string]any); ok {
			current = items
		}
	}
	return true
}
