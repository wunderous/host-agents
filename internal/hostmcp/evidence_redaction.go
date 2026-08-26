package hostmcp

import (
	"encoding/json"
	"fmt"
	"strings"

	hostcapability "github.com/wunderous/host-agents/internal/capability"
	"github.com/wunderous/host-agents/internal/plan"
)

const redactedEvidenceValue = "[redacted]"

func redactCapabilityObservation(observation hostcapability.CapabilityObservation, schema map[string]any) hostcapability.CapabilityObservation {
	projected := observation
	if len(observation.Structured) > 0 {
		var value any
		if json.Unmarshal(observation.Structured, &value) == nil {
			if encoded, err := json.Marshal(redactEvidenceBySchema(value, schema)); err == nil {
				projected.Structured = encoded
			} else {
				projected.Structured = json.RawMessage(`{"redacted":true}`)
			}
		}
	}
	for index := range projected.Facts {
		if len(projected.Facts[index].Value) > 0 {
			projected.Facts[index].Value = json.RawMessage(`"[redacted]"`)
		}
	}
	for index := range projected.Evidence {
		if len(projected.Evidence[index].Value) > 0 {
			projected.Evidence[index].Value = json.RawMessage(`"[redacted]"`)
		}
	}
	return projected
}

// redactEvidenceBySchema projects execution evidence from the declared JSON
// schema. Secret handling is owned by writeOnly schema fields; it does not
// infer sensitivity from argument names.
func redactEvidenceBySchema(value any, schema map[string]any) any {
	if schema != nil {
		if writeOnly, ok := schema["writeOnly"].(bool); ok && writeOnly {
			return redactedEvidenceValue
		}
	}

	switch typed := value.(type) {
	case map[string]any:
		properties := schemaMap(schema, "properties")
		additional, _ := schema["additionalProperties"].(map[string]any)
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			childSchema := properties[key]
			if childSchema == nil {
				childSchema = additional
			}
			out[key] = redactEvidenceBySchema(child, childSchema)
		}
		return out
	case []any:
		items, _ := schema["items"].(map[string]any)
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = redactEvidenceBySchema(child, items)
		}
		return out
	default:
		return value
	}
}

func schemaMap(schema map[string]any, key string) map[string]map[string]any {
	if schema == nil {
		return nil
	}
	raw, ok := schema[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]map[string]any, len(raw))
	for name, value := range raw {
		child, ok := value.(map[string]any)
		if !ok {
			continue
		}
		out[name] = child
	}
	return out
}

func redactEvidenceJSON(value any, schema map[string]any) (string, error) {
	redacted, err := json.Marshal(redactEvidenceBySchema(value, schema))
	if err != nil {
		return "", fmt.Errorf("encode redacted evidence: %w", err)
	}
	return string(redacted), nil
}

// redactPlanEvidence projects both the executable arguments and the durable
// observations of a host plan through the capability catalog. A plan is
// validated before it reaches this function, but the fail-closed fallback is
// intentional: durable evidence must not become a second path around a
// capability's declared secret boundary.
func (s *Server) redactPlanEvidence(value any, secretNames map[string]struct{}, capabilities map[string]plan.Capability) any {
	root, ok := value.(map[string]any)
	if !ok {
		return map[string]any{"redacted": true}
	}
	projected := cloneEvidenceValue(root)
	projectedObject, _ := projected.(map[string]any)
	if variables, ok := projectedObject["variables"].(map[string]any); ok {
		if inputs, ok := variables["inputs"].(map[string]any); ok {
			for name := range secretNames {
				if _, exists := inputs[name]; exists {
					inputs[name] = redactedEvidenceValue
				}
			}
		}
	}
	if nodes, ok := projectedObject["nodes"].([]any); ok {
		for _, rawNode := range nodes {
			node, ok := rawNode.(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{"action", "validate", "compensate", "recover"} {
				if action, ok := node[key].(map[string]any); ok {
					redactPlanAction(action, capabilities)
				}
			}
		}
	}
	return projected
}

func redactPlanAction(action map[string]any, capabilities map[string]plan.Capability) {
	tool, _ := action["tool"].(string)
	args, _ := action["args"].(map[string]any)
	if strings.TrimSpace(tool) == "" || args == nil {
		return
	}
	capability, ok := capabilities[tool]
	if !ok {
		action["args"] = map[string]any{"redacted": true}
		return
	}
	action["args"] = redactEvidenceBySchema(args, capability.InputSchema)
}

func cloneEvidenceValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = cloneEvidenceValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = cloneEvidenceValue(child)
		}
		return out
	default:
		return value
	}
}

func (s *Server) redactPlanRunState(value plan.RunState, doc *plan.Document) map[string]any {
	result := map[string]any{
		"runId":      value.RunID,
		"planId":     value.PlanID,
		"generation": value.Generation,
		"status":     value.Status,
		"nodes":      map[string]any{},
		"outputs":    map[string]any{},
		"error":      value.Error,
	}
	nodeSchemas := map[string]map[string]any{}
	if doc != nil {
		for _, node := range doc.Nodes {
			if node.Action != nil {
				if descriptor, ok := s.taskCapabilityDescriptor(node.Action.Tool); ok {
					nodeSchemas[node.ID] = descriptor.OutputSchema
				}
			} else if node.Validate != nil {
				if descriptor, ok := s.taskCapabilityDescriptor(node.Validate.Tool); ok {
					nodeSchemas[node.ID] = descriptor.OutputSchema
				}
			}
		}
	}
	nodes, _ := result["nodes"].(map[string]any)
	for id, nodeValue := range value.Nodes {
		node := map[string]any{
			"id":     nodeValue.ID,
			"status": nodeValue.Status,
		}
		if nodeValue.Attempts != 0 {
			node["attempts"] = nodeValue.Attempts
		}
		if nodeValue.Output != nil {
			node["output"] = redactEvidenceBySchema(nodeValue.Output, nodeSchemas[id])
		}
		if nodeValue.Observed != nil {
			node["observed"] = redactEvidenceBySchema(nodeValue.Observed, nodeSchemas[id])
		}
		if nodeValue.Expected != nil {
			node["expected"] = cloneEvidenceValue(nodeValue.Expected)
		}
		if nodeValue.Error != "" {
			node["error"] = nodeValue.Error
		}
		if nodeValue.StartedAt != "" {
			node["startedAt"] = nodeValue.StartedAt
		}
		if nodeValue.CompletedAt != "" {
			node["completedAt"] = nodeValue.CompletedAt
		}
		nodes[id] = node
	}
	outputs, _ := result["outputs"].(map[string]any)
	for id, output := range value.Outputs {
		if schema, ok := nodeSchemas[id]; ok {
			outputs[id] = redactEvidenceBySchema(output, schema)
		} else {
			// Activation and recipe observations are derived state, not a
			// declared capability result. Keep the durable record useful only
			// through its typed node evidence, and fail closed here.
			outputs[id] = map[string]any{"redacted": true}
		}
	}
	return result
}
