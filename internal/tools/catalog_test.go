package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStandaloneCatalogAdvertisesOllamaDefaultAndLlamaAlternate(t *testing.T) {
	localNames := map[string]bool{
		"check_local_llm_prerequisites": true,
		"list_local_llm_models":         true,
		"probe_local_llm":               true,
		"install_local_llm_model":       true,
		"configure_local_llm_model":     true,
		"start_local_llm_runtime":       true,
		"configure_local_llm_runtime":   true,
		"stop_local_llm_runtime":        true,
		"remove_local_llm_model":        true,
	}
	for _, definition := range StandaloneToolDefinitions() {
		if !localNames[definition.Name] {
			continue
		}
		encoded, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(encoded))
		if strings.Contains(text, "transformers") {
			t.Fatalf("retired runtime advertised for %s: %s", definition.Name, text)
		}
		if !strings.Contains(text, "ollama") && !strings.Contains(text, "llama-cpp") && definition.Name != "check_local_llm_prerequisites" && definition.Name != "configure_local_llm_model" && definition.Name != "configure_local_llm_runtime" && definition.Name != "remove_local_llm_model" {
			t.Fatalf("runtime contract missing from %s", definition.Name)
		}
	}
}

func TestCanonicalizeToolDefinitionsSeparatesCreationNamesFromEntityURIs(t *testing.T) {
	defs := []ToolDefinition{
		{
			Name: "create_vm",
			InputSchema: map[string]any{
				"type": "object", "required": []string{"vmName"},
				"properties": map[string]any{"vmName": map[string]any{"type": "string"}},
			},
		},
		{
			Name: "start_vm",
			InputSchema: map[string]any{
				"type": "object", "required": []string{"vmName"},
				"properties": map[string]any{"vmName": map[string]any{"type": "string"}, "force": map[string]any{"type": "boolean"}},
			},
			OutputSchema: map[string]any{
				"type": "object", "properties": map[string]any{"status": map[string]any{"type": "string"}},
			},
		},
	}
	got := CanonicalizeToolDefinitions(defs)
	createProps := got[0].InputSchema["properties"].(map[string]any)
	if _, ok := createProps["vmName"]; !ok {
		t.Fatalf("creation name was removed: %#v", createProps)
	}
	startProps := got[1].InputSchema["properties"].(map[string]any)
	if _, ok := startProps["vmName"]; ok {
		t.Fatalf("lifecycle name remained in schema: %#v", startProps)
	}
	if _, ok := startProps["uri"]; !ok {
		t.Fatalf("canonical uri missing: %#v", startProps)
	}
	if required := got[1].InputSchema["required"].([]string); len(required) != 1 || required[0] != "uri" {
		t.Fatalf("unexpected lifecycle required fields: %#v", got[1].InputSchema["required"])
	}
	outputRequired := got[1].OutputSchema["properties"].(map[string]any)
	if _, ok := outputRequired["uri"]; !ok {
		t.Fatalf("output uri missing: %#v", outputRequired)
	}
}
