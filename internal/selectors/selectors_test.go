package selectors

import (
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
)

func vmListContract() (map[string]any, []capabilitycontract.ResultType) {
	return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"vms": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"uri":  map[string]any{"type": "string"},
							"name": map[string]any{"type": "string"},
						},
					},
				},
			},
		}, []capabilitycontract.ResultType{{
			ID: "vm.uri", Version: 1,
			Selectors: []capabilitycontract.ResultSelector{{
				ID: "uri", SourcePath: "vms[].uri", Cardinality: capabilitycontract.CardinalityMany, LabelPath: "vms[].name",
			}},
		}}
}

func TestValidateAndEvaluateListSelector(t *testing.T) {
	schema, resultTypes := vmListContract()
	if err := Validate(schema, "vm.uri", resultTypes); err != nil {
		t.Fatalf("valid selector contract rejected: %v", err)
	}
	value := map[string]any{"vms": []any{
		map[string]any{"uri": "resource:vm:a", "name": "alpha"},
		map[string]any{"uri": "resource:vm:b", "name": "beta"},
	}}
	candidates, err := Evaluate(value, "vm.uri", "uri", resultTypes)
	if err != nil {
		t.Fatalf("selector evaluation failed: %v", err)
	}
	if len(candidates) != 2 || candidates[0].Value != "resource:vm:a" || candidates[0].Label != "alpha" || candidates[1].Indices[0] != 1 {
		t.Fatalf("unexpected selector candidates: %+v", candidates)
	}
}

func TestValidateRejectsInvalidPathsAndCardinality(t *testing.T) {
	schema, resultTypes := vmListContract()
	resultTypes[0].Selectors[0].SourcePath = "vms[].missing"
	if err := Validate(schema, "vm.uri", resultTypes); err == nil {
		t.Fatal("selector path absent from output schema was accepted")
	}

	schema, resultTypes = vmListContract()
	resultTypes[0].Selectors[0].Cardinality = capabilitycontract.CardinalityOne
	value := map[string]any{"vms": []any{
		map[string]any{"uri": "resource:vm:a", "name": "alpha"},
		map[string]any{"uri": "resource:vm:b", "name": "beta"},
	}}
	if _, err := Evaluate(value, "vm.uri", "uri", resultTypes); err == nil {
		t.Fatal("one-cardinality selector accepted multiple candidates")
	}
}
