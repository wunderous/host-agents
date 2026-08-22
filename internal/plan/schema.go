package plan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.yaml.in/yaml/v3"
)

const ContractVersion = "host-plan.v1"

const (
	maxDocumentBytes = 512 * 1024
	maxNodes         = 256
	maxFanOut        = 64
	maxTotalAttempts = 256
	maxPasses        = 32
)

type Document struct {
	ContractVersion string         `json:"contractVersion" yaml:"contractVersion"`
	PlanID          string         `json:"planId" yaml:"planId"`
	Generation      int            `json:"generation" yaml:"generation"`
	IdempotencyKey  string         `json:"idempotencyKey" yaml:"idempotencyKey"`
	CatalogRevision string         `json:"catalogRevision,omitempty" yaml:"catalogRevision,omitempty"`
	Variables       map[string]any `json:"variables,omitempty" yaml:"variables,omitempty"`
	Defaults        Defaults       `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	Converge        Converge       `json:"converge,omitempty" yaml:"converge,omitempty"`
	Nodes           []Node         `json:"nodes" yaml:"nodes"`
}

type Defaults struct {
	TimeoutMs int   `json:"timeoutMs,omitempty" yaml:"timeoutMs,omitempty"`
	Retry     Retry `json:"retry,omitempty" yaml:"retry,omitempty"`
	MaxPasses int   `json:"maxPasses,omitempty" yaml:"maxPasses,omitempty"`
}

type Converge struct {
	MaxPasses         int  `json:"maxPasses,omitempty" yaml:"maxPasses,omitempty"`
	AbortOnExhaustion bool `json:"abortOnExhaustion,omitempty" yaml:"abortOnExhaustion,omitempty"`
	MaxConcurrency    int  `json:"maxConcurrency,omitempty" yaml:"maxConcurrency,omitempty"`
}

type Retry struct {
	MaxAttempts   int `json:"maxAttempts,omitempty" yaml:"maxAttempts,omitempty"`
	BackoffMs     int `json:"backoffMs,omitempty" yaml:"backoffMs,omitempty"`
	BackoffFactor int `json:"backoffFactor,omitempty" yaml:"backoffFactor,omitempty"`
}

type Node struct {
	ID                string      `json:"id" yaml:"id"`
	DependsOn         []string    `json:"dependsOn,omitempty" yaml:"dependsOn,omitempty"`
	When              []Assertion `json:"when,omitempty" yaml:"when,omitempty"`
	Action            *Action     `json:"action,omitempty" yaml:"action,omitempty"`
	Validate          *Validation `json:"validate,omitempty" yaml:"validate,omitempty"`
	Recover           *Recovery   `json:"recover,omitempty" yaml:"recover,omitempty"`
	Compensate        *Action     `json:"compensate,omitempty" yaml:"compensate,omitempty"`
	Retry             Retry       `json:"retry,omitempty" yaml:"retry,omitempty"`
	TimeoutMs         int         `json:"timeoutMs,omitempty" yaml:"timeoutMs,omitempty"`
	ContinueOnFailure bool        `json:"continueOnFailure,omitempty" yaml:"continueOnFailure,omitempty"`
	ForEach           *ForEach    `json:"forEach,omitempty" yaml:"forEach,omitempty"`
}

type Action struct {
	Tool string         `json:"tool" yaml:"tool"`
	Args map[string]any `json:"args,omitempty" yaml:"args,omitempty"`
}

type Recovery struct {
	Action
	MaxAttempts int `json:"maxAttempts,omitempty" yaml:"maxAttempts,omitempty"`
}

type Validation struct {
	Tool           string         `json:"tool" yaml:"tool"`
	Args           map[string]any `json:"args,omitempty" yaml:"args,omitempty"`
	Assert         []Assertion    `json:"assert,omitempty" yaml:"assert,omitempty"`
	PollIntervalMs int            `json:"pollIntervalMs,omitempty" yaml:"pollIntervalMs,omitempty"`
	TimeoutMs      int            `json:"timeoutMs,omitempty" yaml:"timeoutMs,omitempty"`
}

type ForEach struct {
	Source string      `json:"source" yaml:"source"`
	Path   string      `json:"path,omitempty" yaml:"path,omitempty"`
	As     string      `json:"as" yaml:"as"`
	Filter []Assertion `json:"filter,omitempty" yaml:"filter,omitempty"`
}

type Assertion struct {
	Path       string      `json:"path" yaml:"path"`
	Op         string      `json:"op" yaml:"op"`
	Value      any         `json:"value,omitempty" yaml:"value,omitempty"`
	Assertions []Assertion `json:"assertions,omitempty" yaml:"assertions,omitempty"`
}

type Capability struct {
	Name         string
	InputSchema  map[string]any
	OutputSchema map[string]any
	Effect       string
	Idempotent   bool
}

type NodeStatus string

const (
	StatusPending            NodeStatus = "pending"
	StatusSkipped            NodeStatus = "skipped"
	StatusSatisfied          NodeStatus = "satisfied"
	StatusApplied            NodeStatus = "applied"
	StatusFailed             NodeStatus = "failed"
	StatusUnknown            NodeStatus = "unknown"
	StatusCompensated        NodeStatus = "compensated"
	StatusCompensationFailed NodeStatus = "compensation_failed"
)

type NodeRunState struct {
	ID          string     `json:"id"`
	Status      NodeStatus `json:"status"`
	Attempts    int        `json:"attempts,omitempty"`
	Output      any        `json:"output,omitempty"`
	Observed    any        `json:"observed,omitempty"`
	Expected    any        `json:"expected,omitempty"`
	Error       string     `json:"error,omitempty"`
	StartedAt   string     `json:"startedAt,omitempty"`
	CompletedAt string     `json:"completedAt,omitempty"`
}

type RunState struct {
	RunID      string                  `json:"runId"`
	PlanID     string                  `json:"planId"`
	Generation int                     `json:"generation"`
	Status     string                  `json:"status"`
	Nodes      map[string]NodeRunState `json:"nodes"`
	Outputs    map[string]any          `json:"outputs,omitempty"`
	Error      string                  `json:"error,omitempty"`
}

type Dispatcher func(ctx context.Context, name string, args map[string]any, onData func(string)) (*mcp.CallToolResult, error)

// EventSink is called after each durable state transition. It must not mutate
// the state passed to it.
type EventSink func(RunState) error

func Decode(raw any) (Document, error) {
	var data []byte
	switch value := raw.(type) {
	case Document:
		return value, nil
	case []byte:
		data = append([]byte(nil), value...)
	case string:
		data = []byte(value)
	case map[string]any:
		encoded, err := json.Marshal(value)
		if err != nil {
			return Document{}, fmt.Errorf("encode plan: %w", err)
		}
		data = encoded
	default:
		return Document{}, fmt.Errorf("plan must be an object, JSON, YAML, or string")
	}
	if len(data) == 0 || len(data) > maxDocumentBytes {
		return Document{}, fmt.Errorf("plan document must be between 1 and %d bytes", maxDocumentBytes)
	}
	var doc Document
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal(data, &doc); err != nil {
			return Document{}, fmt.Errorf("decode JSON plan: %w", err)
		}
	} else if err := yaml.Unmarshal(data, &doc); err != nil {
		return Document{}, fmt.Errorf("decode YAML plan: %w", err)
	}
	return doc, nil
}

func CanonicalJSON(doc Document) ([]byte, error) {
	return json.Marshal(doc)
}

func DocumentHash(doc Document) (string, []byte, error) {
	encoded, err := CanonicalJSON(doc)
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), encoded, nil
}

func Validate(doc Document, capabilities map[string]Capability, catalogRevision string) error {
	if doc.ContractVersion != ContractVersion {
		return fmt.Errorf("unsupported contractVersion %q", doc.ContractVersion)
	}
	if strings.TrimSpace(doc.PlanID) == "" || strings.TrimSpace(doc.IdempotencyKey) == "" {
		return fmt.Errorf("planId and idempotencyKey are required")
	}
	if doc.Generation < 1 {
		return fmt.Errorf("generation must be at least 1")
	}
	if doc.CatalogRevision != "" && catalogRevision != "" && doc.CatalogRevision != catalogRevision {
		return fmt.Errorf("catalog revision mismatch: plan=%s current=%s", doc.CatalogRevision, catalogRevision)
	}
	if len(doc.Nodes) == 0 || len(doc.Nodes) > maxNodes {
		return fmt.Errorf("nodes must contain between 1 and %d entries", maxNodes)
	}
	passes := doc.Converge.MaxPasses
	if doc.Converge.MaxPasses < 0 || doc.Defaults.MaxPasses < 0 {
		return fmt.Errorf("maxPasses cannot be negative")
	}
	if passes <= 0 {
		passes = doc.Defaults.MaxPasses
	}
	if passes > maxPasses {
		return fmt.Errorf("converge maxPasses exceeds limit %d", maxPasses)
	}
	if doc.Converge.MaxConcurrency < 0 || doc.Converge.MaxConcurrency > maxFanOut {
		return fmt.Errorf("converge maxConcurrency must be between 0 and %d", maxFanOut)
	}
	if doc.Defaults.Retry.MaxAttempts < 0 || doc.Defaults.Retry.MaxAttempts > maxTotalAttempts {
		return fmt.Errorf("default retry maxAttempts must be between 0 and %d", maxTotalAttempts)
	}
	if _, _, err := DocumentHash(doc); err != nil {
		return fmt.Errorf("canonicalize plan: %w", err)
	}
	if err := ValidateGraph(doc); err != nil {
		return err
	}
	ancestors := graphAncestors(doc)
	totalAttempts := 0
	for _, node := range doc.Nodes {
		if strings.TrimSpace(node.ID) == "" {
			return fmt.Errorf("node id is required")
		}
		if node.Action == nil && node.Validate == nil && node.ForEach == nil {
			return fmt.Errorf("node %q must have an action, validation, or forEach", node.ID)
		}
		if node.Action != nil {
			capability, ok := capabilities[node.Action.Tool]
			if !ok {
				return fmt.Errorf("node %q references unknown action %q", node.ID, node.Action.Tool)
			}
			if node.Action.Tool == "run_host_plan" {
				return fmt.Errorf("node %q cannot recursively run a host plan", node.ID)
			}
			if err := validateInterpolations(node.Action.Args, doc, node, ancestors); err != nil {
				return fmt.Errorf("node %q action references: %w", node.ID, err)
			}
			if err := ValidateJSON(capability.InputSchema, node.Action.Args); err != nil {
				return fmt.Errorf("node %q action %s arguments: %w", node.ID, node.Action.Tool, err)
			}
			if capability.Effect != "read" && node.Validate == nil {
				return fmt.Errorf("mutating node %q requires a readiness validate block", node.ID)
			}
			if attemptsFor(node, doc) > 1 && capability.Effect != "read" && !capability.Idempotent {
				return fmt.Errorf("mutating node %q action %s is not declared idempotent; automatic retries are disabled", node.ID, node.Action.Tool)
			}
			if err := validateReferenceTypes(node.Action.Args, capability.InputSchema, doc, node, capabilities); err != nil {
				return fmt.Errorf("node %q action types: %w", node.ID, err)
			}
		}
		if node.Validate != nil {
			capability, ok := capabilities[node.Validate.Tool]
			if !ok {
				return fmt.Errorf("node %q references unknown validation tool %q", node.ID, node.Validate.Tool)
			}
			if capability.Effect != "read" {
				return fmt.Errorf("node %q validation tool %q is not read-only", node.ID, node.Validate.Tool)
			}
			if err := validateInterpolations(node.Validate.Args, doc, node, ancestors); err != nil {
				return fmt.Errorf("node %q validation references: %w", node.ID, err)
			}
			if err := ValidateJSON(capability.InputSchema, node.Validate.Args); err != nil {
				return fmt.Errorf("node %q validation %s arguments: %w", node.ID, node.Validate.Tool, err)
			}
			if err := validateReferenceTypes(node.Validate.Args, capability.InputSchema, doc, node, capabilities); err != nil {
				return fmt.Errorf("node %q validation types: %w", node.ID, err)
			}
		}
		if node.Recover != nil {
			capability, ok := capabilities[node.Recover.Tool]
			if !ok {
				return fmt.Errorf("node %q references unknown recovery tool %q", node.ID, node.Recover.Tool)
			}
			if node.Recover.MaxAttempts < 0 || node.Recover.MaxAttempts > maxTotalAttempts {
				return fmt.Errorf("node %q recovery maxAttempts is outside the bounded range", node.ID)
			}
			if err := validateInterpolations(node.Recover.Args, doc, node, ancestors); err != nil {
				return fmt.Errorf("node %q recovery references: %w", node.ID, err)
			}
			if err := ValidateJSON(capability.InputSchema, node.Recover.Args); err != nil {
				return fmt.Errorf("node %q recovery %s arguments: %w", node.ID, node.Recover.Tool, err)
			}
			if err := validateReferenceTypes(node.Recover.Args, capability.InputSchema, doc, node, capabilities); err != nil {
				return fmt.Errorf("node %q recovery types: %w", node.ID, err)
			}
		}
		if node.Compensate != nil {
			capability, ok := capabilities[node.Compensate.Tool]
			if !ok {
				return fmt.Errorf("node %q references unknown compensation tool %q", node.ID, node.Compensate.Tool)
			}
			if err := validateInterpolations(node.Compensate.Args, doc, node, ancestors); err != nil {
				return fmt.Errorf("node %q compensation references: %w", node.ID, err)
			}
			if err := ValidateJSON(capability.InputSchema, node.Compensate.Args); err != nil {
				return fmt.Errorf("node %q compensation %s arguments: %w", node.ID, node.Compensate.Tool, err)
			}
			if err := validateReferenceTypes(node.Compensate.Args, capability.InputSchema, doc, node, capabilities); err != nil {
				return fmt.Errorf("node %q compensation types: %w", node.ID, err)
			}
		}
		if node.ForEach != nil {
			if strings.TrimSpace(node.ForEach.Source) == "" || strings.TrimSpace(node.ForEach.As) == "" {
				return fmt.Errorf("node %q forEach requires source and as", node.ID)
			}
			if node.Action == nil {
				return fmt.Errorf("node %q forEach requires an action", node.ID)
			}
			if err := validateReference(node.ForEach.Source, doc, node, ancestors, true); err != nil {
				return fmt.Errorf("node %q forEach source: %w", node.ID, err)
			}
		}
		if node.Retry.MaxAttempts < 0 || node.Retry.MaxAttempts > maxTotalAttempts {
			return fmt.Errorf("node %q retry maxAttempts is outside the bounded range", node.ID)
		}
		attempts := attemptsFor(node, doc)
		attemptMultiplier := 1
		if node.ForEach != nil {
			attemptMultiplier = maxFanOut
		}
		recoveryAttempts := 0
		if node.Recover != nil {
			recoveryAttempts = node.Recover.MaxAttempts
			if recoveryAttempts <= 0 {
				recoveryAttempts = 1
			}
			capability := capabilities[node.Recover.Tool]
			if recoveryAttempts > 1 && capability.Effect != "read" && !capability.Idempotent {
				return fmt.Errorf("node %q recovery %s is not declared idempotent; automatic retries are disabled", node.ID, node.Recover.Tool)
			}
		}
		totalAttempts += attemptMultiplier * (attempts + recoveryAttempts)
	}
	if totalAttempts > maxTotalAttempts {
		return fmt.Errorf("plan attempts exceed limit %d", maxTotalAttempts)
	}
	return nil
}

func attemptsFor(node Node, doc Document) int {
	attempts := node.Retry.MaxAttempts
	if attempts <= 0 {
		attempts = doc.Defaults.Retry.MaxAttempts
	}
	if attempts <= 0 {
		return 1
	}
	return attempts
}

// validateReferenceTypes checks the part of the producer/consumer contract
// that is knowable before execution. Whole-value references are intentionally
// deferred by ValidateJSON, but a prior node with a typed output schema must
// still be compatible with the target input field. Unknown schemas are not
// guessed; the catalog must provide one before a typed reference is accepted.
func validateReferenceTypes(value any, targetSchema map[string]any, doc Document, node Node, capabilities map[string]Capability) error {
	return walkReferenceTypes(value, targetSchema, doc, node, capabilities)
}

func walkReferenceTypes(value any, schema map[string]any, doc Document, node Node, capabilities map[string]Capability) error {
	switch typed := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for key, child := range typed {
			childSchema, _ := properties[key].(map[string]any)
			if err := walkReferenceTypes(child, childSchema, doc, node, capabilities); err != nil {
				return fmt.Errorf("property %q: %w", key, err)
			}
		}
	case []any:
		itemSchema, _ := schema["items"].(map[string]any)
		for index, child := range typed {
			if err := walkReferenceTypes(child, itemSchema, doc, node, capabilities); err != nil {
				return fmt.Errorf("item %d: %w", index, err)
			}
		}
	case string:
		if !isWholeReference(typed) {
			return nil
		}
		match := referencePattern.FindStringSubmatch(typed)
		if len(match) != 2 || !strings.HasPrefix(match[1], "nodes.") {
			return nil
		}
		parts := strings.Split(match[1], ".")
		if len(parts) < 3 {
			return nil
		}
		producerID := parts[1]
		producer, ok := nodeByID(doc, producerID)
		if !ok {
			return fmt.Errorf("producer node %q is not declared", producerID)
		}
		producerSchema := map[string]any(nil)
		if producer.Action != nil {
			producerSchema = capabilities[producer.Action.Tool].OutputSchema
		}
		if producerSchema == nil && producer.Validate != nil {
			producerSchema = capabilities[producer.Validate.Tool].OutputSchema
		}
		path := parts[2:]
		if len(path) > 0 && path[0] == "output" {
			path = path[1:]
		}
		producedSchema, ok := schemaAt(producerSchema, path)
		if !ok {
			return fmt.Errorf("reference %q has no typed producer output schema", typed)
		}
		if len(schema) > 0 && !schemasCompatible(producedSchema, schema) {
			return fmt.Errorf("reference %q produces %s but target expects %s", typed, schemaType(producedSchema), schemaType(schema))
		}
	}
	return nil
}

func nodeByID(doc Document, id string) (Node, bool) {
	for _, candidate := range doc.Nodes {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return Node{}, false
}

func schemaAt(schema map[string]any, path []string) (map[string]any, bool) {
	current := schema
	for _, part := range path {
		properties, _ := current["properties"].(map[string]any)
		next, ok := properties[part].(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, len(current) > 0
}

func schemaType(schema map[string]any) string {
	if schema == nil {
		return "unknown"
	}
	if value, ok := schema["type"].(string); ok && value != "" {
		return value
	}
	if values, ok := schema["type"].([]any); ok {
		parts := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "|")
	}
	return "unknown"
}

func schemasCompatible(producer, consumer map[string]any) bool {
	producerType := schemaType(producer)
	consumerType := schemaType(consumer)
	if producerType == "unknown" || consumerType == "unknown" {
		return true
	}
	for _, candidate := range strings.Split(consumerType, "|") {
		if candidate == producerType {
			return true
		}
	}
	return false
}

func ValidateJSON(schema map[string]any, value any) error {
	if len(schema) == 0 {
		return nil
	}
	if isWholeReference(value) {
		// The runtime value is schema-checked after interpolation. This keeps
		// static validation honest for references whose type is not known until
		// a prior typed observation has been produced.
		return nil
	}
	if enum, ok := stringOrAnySlice(schema["enum"]); ok {
		matched := false
		for _, candidate := range enum {
			if jsonEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("value %v is not in enum", value)
		}
	}
	if constant, ok := schema["const"]; ok && !jsonEqual(constant, value) {
		return fmt.Errorf("value %v does not equal const %v", value, constant)
	}
	types := schemaTypes(schema["type"])
	if len(types) == 0 {
		return nil
	}
	if len(types) > 1 {
		var last error
		for _, typeName := range types {
			candidate := make(map[string]any, len(schema))
			for key, child := range schema {
				candidate[key] = child
			}
			candidate["type"] = typeName
			if err := ValidateJSON(candidate, value); err == nil {
				return nil
			} else {
				last = err
			}
		}
		return last
	}
	switch types[0] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object, got %T", value)
		}
		if required, ok := stringOrAnySlice(schema["required"]); ok {
			for _, raw := range required {
				if _, exists := object[raw]; !exists {
					return fmt.Errorf("missing required property %q", raw)
				}
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for key, child := range object {
			property, known := properties[key].(map[string]any)
			if known {
				if err := ValidateJSON(property, child); err != nil {
					return fmt.Errorf("property %q: %w", key, err)
				}
			}
		}
	case "array":
		array, ok := anySlice(value)
		if !ok {
			return fmt.Errorf("expected array, got %T", value)
		}
		if minimum, ok := number(schema["minItems"]); ok && float64(len(array)) < minimum {
			return fmt.Errorf("array has %d items, minimum is %g", len(array), minimum)
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for index, item := range array {
				if err := ValidateJSON(itemSchema, item); err != nil {
					return fmt.Errorf("item %d: %w", index, err)
				}
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected string, got %T", value)
		}
		if minimum, ok := number(schema["minLength"]); ok && float64(len(text)) < minimum {
			return fmt.Errorf("string is shorter than %g", minimum)
		}
		if pattern, ok := schema["pattern"].(string); ok {
			matched, err := regexp.MatchString(pattern, text)
			if err != nil {
				return fmt.Errorf("invalid schema pattern: %w", err)
			}
			if !matched {
				return fmt.Errorf("string does not match pattern")
			}
		}
	case "integer":
		if value, ok := number(value); !ok || math.Trunc(value) != value {
			return fmt.Errorf("expected integer, got %T", value)
		}
	case "number":
		if _, ok := number(value); !ok {
			return fmt.Errorf("expected number, got %T", value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", value)
		}
	}
	if minimum, ok := number(schema["minimum"]); ok {
		if actual, ok := number(value); !ok || actual < minimum {
			return fmt.Errorf("number is below minimum %g", minimum)
		}
	}
	return nil
}

func schemaTypes(value any) []string {
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return nil
		}
		return []string{typed}
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func stringOrAnySlice(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, text)
		}
		return out, true
	default:
		return nil, false
	}
}

func anySlice(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []string:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = item
		}
		return out, true
	default:
		return nil, false
	}
}

func isWholeReference(value any) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	match := referencePattern.FindStringSubmatch(text)
	return match != nil && match[0] == text
}

func graphAncestors(doc Document) map[string]map[string]bool {
	parents := make(map[string][]string, len(doc.Nodes))
	for _, node := range doc.Nodes {
		parents[node.ID] = append([]string(nil), node.DependsOn...)
	}
	ancestors := make(map[string]map[string]bool, len(doc.Nodes))
	var visit func(string, map[string]bool)
	visit = func(id string, seen map[string]bool) {
		for _, parent := range parents[id] {
			if seen[parent] {
				continue
			}
			seen[parent] = true
			visit(parent, seen)
		}
	}
	for _, node := range doc.Nodes {
		seen := make(map[string]bool)
		visit(node.ID, seen)
		ancestors[node.ID] = seen
	}
	return ancestors
}

func validateInterpolations(value any, doc Document, node Node, ancestors map[string]map[string]bool) error {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			if err := validateInterpolations(child, doc, node, ancestors); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := validateInterpolations(child, doc, node, ancestors); err != nil {
				return err
			}
		}
	case string:
		for _, match := range referencePattern.FindAllStringSubmatch(value.(string), -1) {
			if err := validateReference(match[1], doc, node, ancestors, node.ForEach != nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateReference(reference string, doc Document, node Node, ancestors map[string]map[string]bool, allowItem bool) error {
	if match := referencePattern.FindStringSubmatch(reference); len(match) == 2 && match[0] == reference {
		reference = match[1]
	}
	parts := strings.Split(reference, ".")
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return fmt.Errorf("invalid interpolation reference %q", reference)
	}
	switch parts[0] {
	case "vars":
		if doc.Variables == nil {
			return fmt.Errorf("variable %q is not declared", parts[1])
		}
		if _, ok := doc.Variables[parts[1]]; !ok {
			return fmt.Errorf("variable %q is not declared", parts[1])
		}
	case "item":
		if !allowItem {
			return fmt.Errorf("item reference %q is only valid inside forEach", reference)
		}
	case "nodes":
		if len(parts) < 3 || ancestors[node.ID] == nil || !ancestors[node.ID][parts[1]] {
			return fmt.Errorf("node reference %q must target a declared dependency", reference)
		}
	default:
		return fmt.Errorf("unknown interpolation root %q", parts[0])
	}
	return nil
}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func jsonEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}
