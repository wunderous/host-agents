package plan

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

var referencePattern = regexp.MustCompile(`\$\{([^}]+)\}`)

type EvalContext struct {
	Variables  map[string]any
	NodeOutput map[string]any
	Item       map[string]any
}

func InterpolateArgs(args map[string]any, context EvalContext) (map[string]any, error) {
	value, err := interpolateValue(args, context)
	if err != nil {
		return nil, err
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("interpolated arguments are not an object")
	}
	return result, nil
}

func interpolateValue(value any, context EvalContext) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			interpolated, err := interpolateValue(child, context)
			if err != nil {
				return nil, err
			}
			result[key] = interpolated
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			interpolated, err := interpolateValue(child, context)
			if err != nil {
				return nil, err
			}
			result[index] = interpolated
		}
		return result, nil
	case string:
		matches := referencePattern.FindAllStringSubmatchIndex(typed, -1)
		if len(matches) == 0 {
			return typed, nil
		}
		if len(matches) == 1 && matches[0][0] == 0 && matches[0][1] == len(typed) {
			return resolveReference(typed[matches[0][2]:matches[0][3]], context)
		}
		var builder strings.Builder
		last := 0
		for _, match := range matches {
			builder.WriteString(typed[last:match[0]])
			resolved, err := resolveReference(typed[match[2]:match[3]], context)
			if err != nil {
				return nil, err
			}
			builder.WriteString(fmt.Sprint(resolved))
			last = match[1]
		}
		builder.WriteString(typed[last:])
		return builder.String(), nil
	default:
		return value, nil
	}
}

func resolveReference(reference string, context EvalContext) (any, error) {
	parts := strings.SplitN(reference, ".", 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid interpolation reference %q", reference)
	}
	var root any
	switch parts[0] {
	case "vars":
		root = context.Variables
	case "item":
		root = context.Item
	case "nodes":
		if len(parts) < 3 || parts[2] == "" {
			return nil, fmt.Errorf("node interpolation must include output: %q", reference)
		}
		node, ok := context.NodeOutput[parts[1]]
		if !ok {
			return nil, fmt.Errorf("unresolved node interpolation %q", reference)
		}
		root = node
		path := parts[2]
		if strings.HasPrefix(path, "output") {
			path = strings.TrimPrefix(path, "output")
			path = strings.TrimPrefix(path, ".")
			if path == "" {
				return root, nil
			}
		}
		return resolveDottedPath(root, path, reference)
	default:
		return nil, fmt.Errorf("unknown interpolation root %q", parts[0])
	}
	return resolveDottedPath(root, strings.Join(parts[1:], "."), reference)
}

func resolveDottedPath(root any, path, reference string) (any, error) {
	if path == "" {
		return root, nil
	}
	path = strings.TrimPrefix(path, "/")
	current := root
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '.' || r == '/' }) {
		switch typed := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = typed[part]
			if !ok {
				return nil, fmt.Errorf("unresolved interpolation reference %q", reference)
			}
		case []any:
			index := 0
			if _, err := fmt.Sscanf(part, "%d", &index); err != nil || index < 0 || index >= len(typed) {
				return nil, fmt.Errorf("unresolved interpolation reference %q", reference)
			}
			current = typed[index]
		default:
			return nil, fmt.Errorf("unresolved interpolation reference %q", reference)
		}
	}
	return current, nil
}

func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, child := range value {
		result[key] = child
	}
	return result
}

func isNil(value any) bool {
	return value == nil || (reflect.ValueOf(value).Kind() == reflect.Pointer && reflect.ValueOf(value).IsNil())
}
