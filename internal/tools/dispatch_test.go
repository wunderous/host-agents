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
	for _, preset := range []string{"", "qwen3.5"} {
		modelRef, err := resolveLocalLLMModelArg(map[string]any{"modelPreset": preset})
		if err != nil {
			t.Fatalf("preset %q: %v", preset, err)
		}
		if modelRef != "qwen3.5:2b" {
			t.Fatalf("preset %q resolved to %q", preset, modelRef)
		}
	}
	modelRef, err := resolveLocalLLMModelArg(map[string]any{"modelPreset": "qwen3.5-0.8b"})
	if err != nil || modelRef != "qwen3.5:0.8b" {
		t.Fatalf("explicit 0.8B Ollama preset resolved to %q: %v", modelRef, err)
	}
	modelRef, err = resolveLocalLLMModelArg(map[string]any{"runtime": "llama-cpp", "modelPreset": "qwen3.5"})
	if err != nil || modelRef != "qwen3.5-0.8b/base-llama" {
		t.Fatalf("explicit llama-cpp preset resolved to %q: %v", modelRef, err)
	}
}

func TestResolveLocalLLMModelArgRejectsUnsupportedPreset(t *testing.T) {
	if _, err := resolveLocalLLMModelArg(map[string]any{"modelPreset": "legacy-model"}); err == nil {
		t.Fatal("expected unsupported model preset to be rejected")
	}
}

func TestResolveLocalLLMModelArgAcceptsGenericModelAlias(t *testing.T) {
	modelRef, err := resolveLocalLLMModelArg(map[string]any{"model": "nomic-embed-text", "role": "embedding"})
	if err != nil || modelRef != "nomic-embed-text" {
		t.Fatalf("generic model alias resolved to %q: %v", modelRef, err)
	}
	role, err := localLLMModelRole(map[string]any{"role": "embedding"})
	if err != nil || role != "embedding" {
		t.Fatalf("embedding role = %q: %v", role, err)
	}
	role, err = localLLMModelRole(map[string]any{})
	if err != nil || role != "language" {
		t.Fatalf("default role = %q: %v", role, err)
	}
}

func TestResolveLocalLLMModelArgRejectsConflictingAliasesAndRoles(t *testing.T) {
	if _, err := resolveLocalLLMModelArg(map[string]any{"model": "one", "modelRef": "two"}); err == nil {
		t.Fatal("conflicting model aliases were accepted")
	}
	if _, err := localLLMModelRole(map[string]any{"role": "reranker"}); err == nil {
		t.Fatal("unsupported generic model role was accepted")
	}
}
