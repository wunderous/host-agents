package ops

import (
	"strings"
	"testing"
)

func TestRenderOllamaSystemdUnitUsesSharedConcurrencyPolicy(t *testing.T) {
	unit, err := renderOllamaSystemdUnit(OllamaRuntimeConfig{
		Port:            11434,
		BinaryPath:      "/usr/local/bin/ollama",
		ModelRef:        "qwen3.5:0.8b",
		ModelsDirectory: "/var/lib/opute/ollama/models",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"Description=Opute shared Ollama runtime",
		"ExecStart=/usr/local/bin/ollama serve",
		"Environment=OLLAMA_HOST=127.0.0.1:11434",
		"Environment=OLLAMA_NUM_PARALLEL=1",
		"Environment=OLLAMA_MAX_LOADED_MODELS=2",
		"Environment=OLLAMA_MODELS=/var/lib/opute/ollama/models",
		"Restart=on-failure",
	} {
		if !strings.Contains(unit, expected) {
			t.Fatalf("unit missing %q:\n%s", expected, unit)
		}
	}
}

func TestOllamaModelNamesMatchTags(t *testing.T) {
	for _, test := range []struct {
		left  string
		right string
		want  bool
	}{
		{"qwen3.5:0.8b", "qwen3.5:0.8b", true},
		{"qwen3.5:0.8b", "qwen3.5:0.8b:latest", true},
		{"hf.co/mradermacher/granite-embedding-small-english-r2-GGUF:Q4_K_M", "hf.co/mradermacher/granite-embedding-small-english-r2-GGUF:Q4_K_M", true},
		{"other-model", "qwen3.5:0.8b", false},
	} {
		if got := ollamaModelNamesMatch(test.left, test.right); got != test.want {
			t.Fatalf("ollamaModelNamesMatch(%q, %q) = %v, want %v", test.left, test.right, got, test.want)
		}
	}
}
