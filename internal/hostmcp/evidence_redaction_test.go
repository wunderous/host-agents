package hostmcp

import (
	"encoding/json"
	"strings"
	"testing"

	hostcapability "github.com/wunderous/host-agents/internal/capability"
	"github.com/wunderous/host-agents/internal/plan"
)

func TestRedactEvidenceBySchemaUsesWriteOnlyProjection(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"data": map[string]any{
				"type":      "object",
				"writeOnly": true,
			},
			"label": map[string]any{"type": "string"},
		},
	}
	encoded, err := redactEvidenceJSON(map[string]any{
		"data":  map[string]any{"password": "do-not-persist"},
		"label": "safe",
	}, schema)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "do-not-persist") {
		t.Fatalf("secret entered durable evidence: %s", encoded)
	}
	var projected map[string]any
	if err := json.Unmarshal([]byte(encoded), &projected); err != nil {
		t.Fatal(err)
	}
	if projected["data"] != redactedEvidenceValue || projected["label"] != "safe" {
		t.Fatalf("schema projection = %#v", projected)
	}
}

func TestCapabilityObservationDurableProjectionRedactsUntypedEvidence(t *testing.T) {
	projected := redactCapabilityObservation(hostcapability.CapabilityObservation{
		Structured: json.RawMessage(`{"secret":"provider-secret","ready":true}`),
		Facts:      []hostcapability.ObservationFact{{Type: "secret", Value: json.RawMessage(`"provider-secret"`)}},
		Evidence:   []hostcapability.EvidenceRecord{{Kind: "diagnostic", Value: json.RawMessage(`"provider-secret"`)}},
	}, map[string]any{"type": "object", "properties": map[string]any{"secret": map[string]any{"writeOnly": true}}})
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "provider-secret") {
		t.Fatalf("observation evidence leaked a secret: %s", encoded)
	}
}

func TestPlanEvidenceUsesCapabilitySchemas(t *testing.T) {
	server := newStandaloneTestServer(t, true)
	document := map[string]any{
		"contractVersion": "host-plan.v1",
		"planId":          "redaction-test",
		"generation":      1,
		"idempotencyKey":  "redaction-test",
		"nodes": []any{map[string]any{
			"id": "secret",
			"action": map[string]any{
				"tool": "put_k8s_secret",
				"args": map[string]any{
					"namespace": "default",
					"name":      "example",
					"data":      map[string]any{"token": "plan-secret"},
				},
			},
		}},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	projected := server.redactPlanDocument(encoded, nil, server.CatalogSnapshot())
	if strings.Contains(projected, "plan-secret") || !strings.Contains(projected, redactedEvidenceValue) {
		t.Fatalf("plan evidence leaked a schema-declared secret: %s", projected)
	}

	state := plan.RunState{
		RunID:   "run",
		Nodes:   map[string]plan.NodeRunState{"secret": {ID: "secret", Output: map[string]any{"ok": true}}},
		Outputs: map[string]any{"secret": map[string]any{"ok": true}, "activation": map[string]any{"secret": "derived"}},
	}
	doc := plan.Document{Nodes: []plan.Node{{ID: "secret", Action: &plan.Action{Tool: "put_k8s_secret"}}}}
	result := server.planRunStateResult(state, &doc)
	outputs := result["outputs"].(map[string]any)
	if _, ok := outputs["activation"].(map[string]any); !ok {
		t.Fatalf("unknown derived output was not fail-closed: %#v", outputs)
	}
}
