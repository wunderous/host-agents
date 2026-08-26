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

func TestResolveLocalLLMModelArgUsesLFM26Default(t *testing.T) {
	var modelRef string
	var err error
	for _, preset := range []string{"", "lfm2-2.6b"} {
		modelRef, err = resolveLocalLLMModelArg(map[string]any{"modelPreset": preset})
		if err != nil {
			t.Fatalf("preset %q: %v", preset, err)
		}
		if modelRef != "hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M" {
			t.Fatalf("preset %q resolved to %q", preset, modelRef)
		}
	}
	modelRef, err = resolveLocalLLMModelArg(map[string]any{"modelPreset": "lfm2.5-thinking"})
	if err != nil || modelRef != "lfm2.5-thinking:1.2b" {
		t.Fatalf("explicit LFM2.5 Thinking 1.2B preset resolved to %q: %v", modelRef, err)
	}
	modelRef, err = resolveLocalLLMModelArg(map[string]any{"modelPreset": "qwen3.5"})
	if err != nil || modelRef != "qwen3.5:2b" {
		t.Fatalf("explicit Qwen3.5 2B Ollama preset resolved to %q: %v", modelRef, err)
	}
	modelRef, err = resolveLocalLLMModelArg(map[string]any{"modelPreset": "qwen3.5-0.8b"})
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
