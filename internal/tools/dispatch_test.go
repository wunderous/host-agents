package tools

import "testing"

func TestListClustersDefaultsToFastInventory(t *testing.T) {
	if !listClustersFastArg(map[string]any{}) {
		t.Fatal("list_clusters without fast must use the bounded inventory path")
	}
}

func TestListClustersHonorsExplicitDetailMode(t *testing.T) {
	if listClustersFastArg(map[string]any{"fast": false}) {
		t.Fatal("list_clusters fast=false must preserve the explicit detail request")
	}
}

func TestResolveLocalLLMModelArgUsesQwen35Default(t *testing.T) {
	for _, preset := range []string{"", "qwen3.5", "qwen3.5-0.8b"} {
		modelRef, err := resolveLocalLLMModelArg(map[string]any{"modelPreset": preset})
		if err != nil {
			t.Fatalf("preset %q: %v", preset, err)
		}
		if modelRef != "qwen3.5-0.8b/base-llama" {
			t.Fatalf("preset %q resolved to %q", preset, modelRef)
		}
	}
}

func TestResolveLocalLLMModelArgRejectsUnsupportedPreset(t *testing.T) {
	if _, err := resolveLocalLLMModelArg(map[string]any{"modelPreset": "legacy-model"}); err == nil {
		t.Fatal("expected unsupported model preset to be rejected")
	}
}
