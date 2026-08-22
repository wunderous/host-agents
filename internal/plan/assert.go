package plan

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type AssertionFailure struct {
	Assertion Assertion `json:"assertion"`
	Observed  any       `json:"observed,omitempty"`
	Expected  any       `json:"expected,omitempty"`
	Message   string    `json:"message"`
}

func EvaluateAssertions(value any, assertions []Assertion) (*AssertionFailure, bool) {
	for _, assertion := range assertions {
		observed, exists, err := resolveJSONPointer(value, assertion.Path)
		if err != nil {
			return &AssertionFailure{Assertion: assertion, Message: err.Error()}, false
		}
		if ok, message := evaluateAssertion(assertion, observed, exists); !ok {
			return &AssertionFailure{Assertion: assertion, Observed: observed, Expected: assertion.Value, Message: message}, false
		}
	}
	return nil, true
}

func resolveJSONPointer(value any, pointer string) (any, bool, error) {
	if pointer == "" || pointer == "/" {
		return value, true, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false, fmt.Errorf("JSON pointer must start with '/': %q", pointer)
	}
	current := value
	for _, raw := range strings.Split(pointer[1:], "/") {
		part := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[part]
			if !ok {
				return nil, false, nil
			}
			current = next
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false, nil
			}
			current = typed[index]
		default:
			return nil, false, nil
		}
	}
	return current, true, nil
}

func evaluateAssertion(assertion Assertion, observed any, exists bool) (bool, string) {
	switch assertion.Op {
	case "exists":
		return exists, "expected value to exist"
	case "notExists":
		return !exists, "expected value not to exist"
	case "empty":
		return !exists || isEmpty(observed), "expected value to be empty"
	case "notEmpty":
		return exists && !isEmpty(observed), "expected value to be non-empty"
	case "eq":
		return exists && jsonEqual(observed, assertion.Value), "values are not equal"
	case "ne":
		return !exists || !jsonEqual(observed, assertion.Value), "values are equal"
	case "gt", "gte", "lt", "lte":
		left, leftOK := number(observed)
		right, rightOK := number(assertion.Value)
		if !leftOK || !rightOK {
			return false, "comparison requires numeric values"
		}
		switch assertion.Op {
		case "gt":
			return left > right, "value is not greater than expected"
		case "gte":
			return left >= right, "value is less than expected"
		case "lt":
			return left < right, "value is not less than expected"
		default:
			return left <= right, "value is greater than expected"
		}
	case "contains":
		if text, ok := observed.(string); ok {
			needle, _ := assertion.Value.(string)
			return strings.Contains(text, needle), "string does not contain expected value"
		}
		if values, ok := observed.([]any); ok {
			for _, item := range values {
				if jsonEqual(item, assertion.Value) {
					return true, ""
				}
			}
		}
		return false, "collection does not contain expected value"
	case "matches":
		text, textOK := observed.(string)
		pattern, patternOK := assertion.Value.(string)
		if !textOK || !patternOK {
			return false, "matches requires string values"
		}
		matched, err := regexp.MatchString(pattern, text)
		return err == nil && matched, "value does not match expected pattern"
	case "all", "any":
		values, ok := observed.([]any)
		if !ok {
			return false, assertion.Op + " requires an array"
		}
		matched := 0
		for _, item := range values {
			if _, ok := EvaluateAssertions(item, assertion.Assertions); ok {
				matched++
			}
		}
		if assertion.Op == "all" {
			return matched == len(values), "not every array element matched"
		}
		return matched > 0, "no array element matched"
	default:
		return false, "unsupported assertion operator " + assertion.Op
	}
}

func isEmpty(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}
