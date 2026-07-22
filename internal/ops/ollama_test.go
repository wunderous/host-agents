package ops

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
)

func TestValidateOllamaModelRef(t *testing.T) {
	for _, ref := range []string{"smollm:135m", "llama3.2:1b", "../escape"} {
		err := ValidateOllamaModelRef(ref)
		if ref == "../escape" && err == nil {
			t.Fatalf("expected invalid model ref")
		}
		if ref != "../escape" && err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
	}
}

func TestRenderOllamaSystemdUnit(t *testing.T) {
	cfg := OllamaConfig{Port: 11434, ModelsDir: "/var/lib/opute/ollama", BinaryPath: "/usr/local/bin/ollama", Version: "v0.30.8", Sha256: "ffe2b2c2f2f5f5b30c081ec353c2e0bb2d9ead516064a8e22663b24b8fd8dca0"}
	unit, err := RenderOllamaSystemdUnit(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"OLLAMA_HOST=127.0.0.1:11434", "OLLAMA_NO_CLOUD=1", "ExecStart=/usr/local/bin/ollama serve"} {
		if !containsOllama(unit, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestOllamaConfigRejectsUnitInjectionPaths(t *testing.T) {
	cfg := OllamaConfig{Port: 11434, ModelsDir: "/var/lib/opute/\nmodels", BinaryPath: "/usr/local/bin/ollama", Version: "v0.30.8", Sha256: ollamaAMD64SHA256}
	if _, err := RenderOllamaSystemdUnit(cfg); err == nil {
		t.Fatal("expected newline path rejection")
	}
	for _, value := range []string{"/var/lib/opute/ollama;touch", "/var/lib/opute/ollama'quote"} {
		cfg.ModelsDir = value
		if _, err := RenderOllamaSystemdUnit(cfg); err == nil {
			t.Fatalf("expected unsafe path rejection for %q", value)
		}
	}
	cfg.ModelsDir = "/var/lib/opute/ollama"
	cfg.Version = "v0.30.8;touch"
	if _, err := RenderOllamaSystemdUnit(cfg); err == nil {
		t.Fatal("expected unsafe version rejection")
	}
}

func TestProbeLocalLLMWithFakeOllamaHTTP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:11434")
	if err != nil {
		t.Skipf("Ollama port is already occupied: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"fake-0.30.8"}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"smollm:135m","digest":"sha256:test","size":135000000}]}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"smollm:135m"}]}`))
		case "/v1/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "ready"}}}})
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Shutdown(context.Background())

	svc := NewHostOperationsService(Options{})
	result, err := svc.ProbeLocalLLM(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ready || !result.OpenAIModelsReady || !result.ChatReady || len(result.Models) != 1 || result.Models[0].Name != "smollm:135m" {
		t.Fatalf("unexpected fake Ollama probe result: %+v", result)
	}
}

func containsOllama(s, sub string) bool { return len(s) >= len(sub) && stringIndexOllama(s, sub) >= 0 }
func stringIndexOllama(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
