package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStandaloneCatalogAdvertisesOnlyLlamaServer(t *testing.T) {
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
		if !strings.Contains(text, "llama-cpp") && definition.Name != "check_local_llm_prerequisites" && definition.Name != "configure_local_llm_model" && definition.Name != "configure_local_llm_runtime" && definition.Name != "remove_local_llm_model" {
			t.Fatalf("llama-cpp runtime missing from %s", definition.Name)
		}
	}
}
