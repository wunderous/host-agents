package resource

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// CostArgumentBindings declares where an invocation's requested resource
// quantities live in its typed argument object. The descriptor owns these
// paths; admission never infers them from an operation name.
type CostArgumentBindings struct {
	CPUCores    string `json:"cpuCores,omitempty"`
	MemoryBytes string `json:"memoryBytes,omitempty"`
	DiskBytes   string `json:"diskBytes,omitempty"`
	Tasks       string `json:"tasks,omitempty"`
}

// ResolveArgumentCost applies descriptor-declared argument quantities to a
// static admission request. Missing optional arguments retain the descriptor's
// static default. An explicitly supplied malformed or non-positive quantity
// fails closed before the capability is invoked.
func ResolveArgumentCost(base AdmissionRequest, args map[string]any, bindings CostArgumentBindings) (AdmissionRequest, error) {
	for _, binding := range []struct {
		field string
		path  string
		set   func(*AdmissionRequest, float64) error
		parse func(any) (float64, error)
	}{
		{field: "cpuCores", path: bindings.CPUCores, parse: parsePositiveNumber, set: func(request *AdmissionRequest, value float64) error {
			request.CPUCores = value
			return nil
		}},
		{field: "memoryBytes", path: bindings.MemoryBytes, parse: parsePositiveCapacity, set: func(request *AdmissionRequest, value float64) error {
			request.MemoryBytes = int64(value)
			return nil
		}},
		{field: "diskBytes", path: bindings.DiskBytes, parse: parsePositiveCapacity, set: func(request *AdmissionRequest, value float64) error {
			request.DiskBytes = int64(value)
			return nil
		}},
		{field: "tasks", path: bindings.Tasks, parse: parsePositiveInteger, set: func(request *AdmissionRequest, value float64) error {
			request.Tasks = int64(value)
			return nil
		}},
	} {
		path := strings.TrimSpace(binding.path)
		if path == "" {
			continue
		}
		if err := validateArgumentPath(path); err != nil {
			return base, &RequestError{Code: "host_resource_binding_invalid", Field: binding.field, Reason: err.Error()}
		}
		value, present := argumentAtPath(args, path)
		if !present {
			continue
		}
		parsed, err := binding.parse(value)
		if err != nil {
			return base, &RequestError{Code: "host_resource_argument_invalid", Field: binding.field, Reason: fmt.Sprintf("argument %q: %v", path, err)}
		}
		if err := binding.set(&base, parsed); err != nil {
			return base, err
		}
	}
	return base, nil
}

func validateArgumentPath(path string) error {
	if strings.HasPrefix(path, ".") || strings.HasSuffix(path, ".") {
		return fmt.Errorf("argument binding path %q is not a non-empty object path", path)
	}
	for _, segment := range strings.Split(path, ".") {
		if segment == "" || strings.ContainsAny(segment, "[]/") {
			return fmt.Errorf("argument binding path %q is not a supported object path", path)
		}
	}
	return nil
}

func argumentAtPath(args map[string]any, path string) (any, bool) {
	var current any = args
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok := object[segment]
		if !ok {
			return nil, false
		}
		current = value
	}
	return current, true
}

func parsePositiveNumber(value any) (float64, error) {
	number, err := numberValue(value)
	if err != nil {
		return 0, err
	}
	if number <= 0 {
		return 0, fmt.Errorf("value must be greater than zero")
	}
	return number, nil
}

func parsePositiveInteger(value any) (float64, error) {
	number, err := parsePositiveNumber(value)
	if err != nil {
		return 0, err
	}
	if math.Trunc(number) != number {
		return 0, fmt.Errorf("value must be an integer")
	}
	if number > float64(maxInt64) {
		return 0, fmt.Errorf("value exceeds the supported integer range")
	}
	return number, nil
}

func parsePositiveCapacity(value any) (float64, error) {
	var bytes float64
	switch typed := value.(type) {
	case string:
		parsed, err := capacityStringBytes(typed)
		if err != nil {
			return 0, err
		}
		bytes = parsed
	default:
		parsed, err := numberValue(value)
		if err != nil {
			return 0, fmt.Errorf("must be a capacity string or integer byte count")
		}
		if math.Trunc(parsed) != parsed {
			return 0, fmt.Errorf("byte count must be an integer")
		}
		bytes = parsed
	}
	if bytes <= 0 {
		return 0, fmt.Errorf("value must be greater than zero")
	}
	if math.IsInf(bytes, 0) || bytes > float64(maxInt64) || math.Trunc(bytes) != bytes {
		return 0, fmt.Errorf("capacity exceeds the supported byte range")
	}
	return bytes, nil
}

func numberValue(value any) (float64, error) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int8:
		number = float64(typed)
	case int16:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint:
		number = float64(typed)
	case uint8:
		number = float64(typed)
	case uint16:
		number = float64(typed)
	case uint32:
		number = float64(typed)
	case uint64:
		number = float64(typed)
	case json.Number:
		parsed, err := strconv.ParseFloat(string(typed), 64)
		if err != nil {
			return 0, fmt.Errorf("must be numeric")
		}
		number = parsed
	default:
		return 0, fmt.Errorf("must be numeric")
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, fmt.Errorf("must be finite")
	}
	if number < 0 {
		return 0, fmt.Errorf("value cannot be negative")
	}
	return number, nil
}

const maxInt64 = int64(^uint64(0) >> 1)

var capacityUnits = []struct {
	suffix     string
	multiplier float64
}{
	{suffix: "TIB", multiplier: 1 << 40},
	{suffix: "TB", multiplier: 1e12},
	{suffix: "TI", multiplier: 1 << 40},
	{suffix: "T", multiplier: 1 << 40},
	{suffix: "GIB", multiplier: 1 << 30},
	{suffix: "GB", multiplier: 1e9},
	{suffix: "GI", multiplier: 1 << 30},
	{suffix: "G", multiplier: 1 << 30},
	{suffix: "MIB", multiplier: 1 << 20},
	{suffix: "MB", multiplier: 1e6},
	{suffix: "MI", multiplier: 1 << 20},
	{suffix: "M", multiplier: 1 << 20},
	{suffix: "KIB", multiplier: 1 << 10},
	{suffix: "KB", multiplier: 1e3},
	{suffix: "KI", multiplier: 1 << 10},
	{suffix: "K", multiplier: 1 << 10},
	{suffix: "B", multiplier: 1},
}

func capacityStringBytes(value string) (float64, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if normalized == "" {
		return 0, fmt.Errorf("must not be empty")
	}
	numberText := normalized
	multiplier := float64(1)
	for _, unit := range capacityUnits {
		if strings.HasSuffix(normalized, unit.suffix) {
			numberText = strings.TrimSpace(strings.TrimSuffix(normalized, unit.suffix))
			multiplier = unit.multiplier
			break
		}
	}
	number, err := strconv.ParseFloat(numberText, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, fmt.Errorf("must be a valid capacity")
	}
	if number <= 0 {
		return 0, fmt.Errorf("value must be greater than zero")
	}
	bytes := number * multiplier
	if math.IsInf(bytes, 0) || bytes > float64(maxInt64) || math.Trunc(bytes) != bytes {
		return 0, fmt.Errorf("capacity must resolve to an integer number of bytes")
	}
	return bytes, nil
}
