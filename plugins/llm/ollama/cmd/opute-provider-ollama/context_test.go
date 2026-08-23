package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderContextEnvironmentIsIdempotentAndPreservesService(t *testing.T) {
	unit := "[Service]\nEnvironment=OLLAMA_HOST=127.0.0.1:11434\nEnvironment=OLLAMA_NUM_PARALLEL=1\n"
	updated, changed := renderContextEnvironment(unit, 32768)
	if !changed || !strings.Contains(updated, "Environment=OLLAMA_CONTEXT_LENGTH=32768") {
		t.Fatalf("context environment was not added: changed=%v unit=%q", changed, updated)
	}
	again, changed := renderContextEnvironment(updated, 32768)
	if changed || again != updated {
		t.Fatalf("context environment was not idempotent: changed=%v unit=%q", changed, again)
	}
}

func TestRenderContextEnvironmentHandlesQuotedSystemdEnvironment(t *testing.T) {
	unit := "[Service]\nEnvironment=\"OLLAMA_HOST=127.0.0.1:11434\"\n"
	updated, changed := renderContextEnvironment(unit, 32768)
	if !changed || !strings.Contains(updated, "Environment=OLLAMA_CONTEXT_LENGTH=32768") {
		t.Fatalf("quoted host environment did not receive context setting: changed=%v unit=%q", changed, updated)
	}
}

func TestWriteContextSizePreservesSharedRuntimeConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ollamaContextConfigRelativePath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	original := map[string]any{"port": 11434, "modelRef": "qwen3.5:2b", "modelContexts": map[string]any{"demo": map[string]any{"contextSize": 4096}}}
	data, _ := json.Marshal(original)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	servicePath := filepath.Join(home, ".config", "systemd", "user", "ollama.service")
	if err := os.MkdirAll(filepath.Dir(servicePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(servicePath, []byte("[Service]\nEnvironment=OLLAMA_HOST=127.0.0.1:11434\n"), 0600); err != nil {
		t.Fatal(err)
	}

	result, err := writeContextSize(t.Context(), 32768)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Persisted || result.ContextSize != 32768 || len(result.ServiceNames) != 1 {
		t.Fatalf("unexpected context result: %+v", result)
	}
	persisted, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(persisted, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["contextSize"] != float64(32768) || decoded["port"] != float64(11434) || decoded["modelContexts"] == nil {
		t.Fatalf("shared runtime fields were not preserved: %s", persisted)
	}
	unit, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "OLLAMA_CONTEXT_LENGTH=32768") {
		t.Fatalf("service unit missing persistent context setting: %s", unit)
	}
}

func TestContextSizeFromUnit(t *testing.T) {
	if got := contextSizeFromUnit("Environment=OLLAMA_CONTEXT_LENGTH=32768\n"); got != 32768 {
		t.Fatalf("contextSizeFromUnit() = %d, want 32768", got)
	}
	if got := contextSizeFromUnit("Environment=OLLAMA_HOST=127.0.0.1:11434\n"); got != 0 {
		t.Fatalf("contextSizeFromUnit() = %d, want 0", got)
	}
}
