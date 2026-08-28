package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

func TestRenderOllamaSystemdUnitUsesSharedConcurrencyPolicy(t *testing.T) {
	unit, err := renderOllamaSystemdUnit(OllamaRuntimeConfig{
		Port:            11434,
		BinaryPath:      "/usr/local/bin/ollama",
		ModelRef:        "hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M",
		ModelsDirectory: "/var/lib/opute/ollama/models",
		ContextSize:     32768,
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
		"Environment=OLLAMA_CONTEXT_LENGTH=32768",
		"Environment=OLLAMA_MODELS=/var/lib/opute/ollama/models",
		"Restart=on-failure",
	} {
		if !strings.Contains(unit, expected) {
			t.Fatalf("unit missing %q:\n%s", expected, unit)
		}
	}
}

func TestOllamaContextDefaultIs32K(t *testing.T) {
	t.Setenv("OPUTE_OLLAMA_CONTEXT_SIZE", "")
	t.Setenv("OLLAMA_CONTEXT_LENGTH", "")
	if got := ollamaContextSizeFromEnvironment(); got != 32768 {
		t.Fatalf("ollamaContextSizeFromEnvironment() = %d, want 32768", got)
	}
}

func TestOllamaModelNamesMatchTags(t *testing.T) {
	for _, test := range []struct {
		left  string
		right string
		want  bool
	}{
		{"hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M", "hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M", true},
		{"hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M", "hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M:latest", true},
		{"hf.co/mradermacher/granite-embedding-small-english-r2-GGUF:Q4_K_M", "hf.co/mradermacher/granite-embedding-small-english-r2-GGUF:Q4_K_M", true},
		{"other-model", "hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M", false},
	} {
		if got := ollamaModelNamesMatch(test.left, test.right); got != test.want {
			t.Fatalf("ollamaModelNamesMatch(%q, %q) = %v, want %v", test.left, test.right, got, test.want)
		}
	}
}

func TestConfigureOllamaModelContextPersistsGenericModelMapping(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	created := 0
	managedContext := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Model      string         `json:"model"`
			Parameters map[string]int `json:"parameters"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		switch request.URL.Path {
		case "/api/show":
			contextSize := 4096
			if configured, ok := managedContext[payload.Model]; ok {
				contextSize = configured
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(writer, `{"parameters":"num_ctx %d\n"}`, contextSize)
		case "/api/create":
			created++
			managedContext[payload.Model] = payload.Parameters["num_ctx"]
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{"status":"success"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	portText := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPUTE_OLLAMA_PORT", strconv.Itoa(port))

	service := testService()
	first, err := service.ConfigureOllamaModelContext(t.Context(), ConfigureOllamaModelContextArgs{
		ModelRef:    "arbitrary/provider-model:latest",
		ContextSize: 32768,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Persisted || !first.Changed || first.ContextSize != 32768 {
		t.Fatalf("unexpected first result: %+v", first)
	}
	if first.EffectiveModelRef == first.ModelRef || !strings.HasPrefix(first.EffectiveModelRef, "opute/context-") {
		t.Fatalf("expected generic managed model reference, got %+v", first)
	}
	if created != 1 {
		t.Fatalf("create calls = %d, want 1", created)
	}

	second, err := service.ConfigureOllamaModelContext(t.Context(), ConfigureOllamaModelContextArgs{
		ModelRef:    first.ModelRef,
		ContextSize: 32768,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed || second.EffectiveModelRef != first.EffectiveModelRef {
		t.Fatalf("expected idempotent second result, first=%+v second=%+v", first, second)
	}
	third, err := service.ConfigureOllamaModelContext(t.Context(), ConfigureOllamaModelContextArgs{
		ModelRef:    first.ModelRef,
		ContextSize: 16384,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !third.Changed || third.ContextSize != 16384 || third.EffectiveModelRef != first.EffectiveModelRef {
		t.Fatalf("expected stable managed reference across context update, first=%+v third=%+v", first, third)
	}
	if created != 2 {
		t.Fatalf("create calls after context update = %d, want 2", created)
	}

	configPath := filepath.Join(home, ".config", "opute", "ollama-runtime.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config OllamaRuntimeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	persisted := config.ModelContexts[first.ModelRef]
	if persisted.EffectiveModelRef != third.EffectiveModelRef || persisted.ContextSize != 16384 {
		t.Fatalf("unexpected persisted mapping: %+v", persisted)
	}
}

func TestConfigureOllamaModelContextRejectsAbove32K(t *testing.T) {
	_, err := (testService()).ConfigureOllamaModelContext(t.Context(), ConfigureOllamaModelContextArgs{
		ModelRef:    "hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M",
		ContextSize: 65536,
	})
	if err == nil {
		t.Fatal("ConfigureOllamaModelContext accepted a context larger than the 32K maximum")
	}
}

func TestGetOllamaModelContextFallsBackToRunningContextLength(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	serviceDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(serviceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(serviceDir, "ollama.service"), []byte("Environment=OLLAMA_CONTEXT_LENGTH=32768\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/show":
			_, _ = fmt.Fprint(writer, `{"parameters":"","details":{"parent_model":""}}`)
		case "/api/ps":
			_, _ = fmt.Fprint(writer, `{"models":[{"name":"hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M","context_length":32768}]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	port, err := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPUTE_OLLAMA_PORT", strconv.Itoa(port))

	result, err := (testService()).GetOllamaModelContext(t.Context(), "hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M")
	if err != nil {
		t.Fatal(err)
	}
	if result.ContextSize != 32768 || result.ContextSource != "ollama-runtime" {
		t.Fatalf("unexpected running context fallback: %+v", result)
	}
}

func TestParseOllamaContextSize(t *testing.T) {
	if got := parseOllamaContextSize("temperature 0.2\nnum_ctx 32768\nstop <eos>"); got != 32768 {
		t.Fatalf("parseOllamaContextSize() = %d, want 32768", got)
	}
	if got := parseOllamaContextSize("temperature 0.2"); got != 0 {
		t.Fatalf("parseOllamaContextSize() = %d, want 0", got)
	}
}

func TestWarmOllamaModelUsesSelectedReferenceAndKeepAlive(t *testing.T) {
	var received struct {
		Model     string `json:"model"`
		Prompt    string `json:"prompt"`
		Stream    bool   `json:"stream"`
		KeepAlive any    `json:"keep_alive"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/generate" {
			http.NotFound(writer, request)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprint(writer, `{"done":true}`)
	}))
	defer server.Close()
	port, err := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	if err := warmOllamaModel(t.Context(), OllamaRuntimeConfig{Port: port}, "opute/context-managed-32768"); err != nil {
		t.Fatal(err)
	}
	if received.Model != "opute/context-managed-32768" || received.Stream || received.Prompt != "." {
		t.Fatalf("unexpected warm request: %+v", received)
	}
	if keepAlive, ok := received.KeepAlive.(float64); !ok || keepAlive != -1 {
		t.Fatalf("keep_alive = %#v, want -1", received.KeepAlive)
	}
}

func TestShouldWarmOllamaModelSkipsEmbeddingModels(t *testing.T) {
	if shouldWarmOllamaModel("embedding") {
		t.Fatal("embedding models must not be warmed through /api/generate")
	}
	if !shouldWarmOllamaModel("language") || !shouldWarmOllamaModel("") {
		t.Fatal("language models must retain the chat warm-up path")
	}
}

func TestRetireOllamaUnitRacedByExternalOwner(t *testing.T) {
	unitDir := filepath.Join(t.TempDir(), ".config", "systemd", "user")
	t.Setenv("HOME", filepath.Dir(filepath.Dir(filepath.Dir(unitDir))))
	if err := os.MkdirAll(unitDir, 0o700); err != nil {
		t.Fatal(err)
	}

	original := runSystemctlUser
	t.Cleanup(func() { runSystemctlUser = original })

	t.Run("without generated unit", func(t *testing.T) {
		var calls [][]string
		runSystemctlUser = func(_ context.Context, args ...string) (string, error) {
			calls = append(calls, args)
			return "", nil
		}
		if err := retireOllamaUnitRacedByExternalOwner(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(calls) != 0 {
			t.Fatalf("expected no systemctl calls, got %v", calls)
		}
	})

	t.Run("keeps unit that serves the port", func(t *testing.T) {
		unitPath := filepath.Join(unitDir, ollamaServiceName)
		if err := os.WriteFile(unitPath, []byte("[Unit]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Remove(unitPath) }()
		var calls [][]string
		runSystemctlUser = func(_ context.Context, args ...string) (string, error) {
			calls = append(calls, args)
			if args[0] == "is-active" {
				return "", nil
			}
			t.Fatalf("unexpected disable of serving unit: %v", args)
			return "", nil
		}
		if err := retireOllamaUnitRacedByExternalOwner(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(calls) != 1 || calls[0][0] != "is-active" {
			t.Fatalf("expected only is-active probe, got %v", calls)
		}
	})

	t.Run("disables raced unit", func(t *testing.T) {
		unitPath := filepath.Join(unitDir, ollamaServiceName)
		if err := os.WriteFile(unitPath, []byte("[Unit]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Remove(unitPath) }()
		var disabled [][]string
		runSystemctlUser = func(_ context.Context, args ...string) (string, error) {
			if args[0] == "is-active" {
				return "activating", fmt.Errorf("unit is auto-restarting")
			}
			disabled = append(disabled, args)
			return "", nil
		}
		if err := retireOllamaUnitRacedByExternalOwner(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(disabled) != 1 || !slices.Equal(disabled[0], []string{"disable", "--now", ollamaServiceName}) {
			t.Fatalf("expected disable --now %s, got %v", ollamaServiceName, disabled)
		}
	})

	t.Run("surfaces disable failure", func(t *testing.T) {
		unitPath := filepath.Join(unitDir, ollamaServiceName)
		if err := os.WriteFile(unitPath, []byte("[Unit]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Remove(unitPath) }()
		runSystemctlUser = func(_ context.Context, args ...string) (string, error) {
			if args[0] == "is-active" {
				return "", fmt.Errorf("inactive")
			}
			return "reload failed", fmt.Errorf("exit status 1")
		}
		if err := retireOllamaUnitRacedByExternalOwner(t.Context()); err == nil {
			t.Fatal("expected error when disable fails")
		}
	})
}

// testService builds the domain with deps that fail loudly. Ollama model
// context handling is local to this domain -- it never publishes anything into
// a cluster -- so a call into any dep means the boundary moved.
func testService() *Service {
	return New(&hostruntime.Shared{}, Deps{
		KubernetesTargetURI: func(string) (string, error) {
			panic("ollama tests must not reach the kubernetes domain")
		},
		ApplyManifest: func(string, string, func(string)) (map[string]any, error) {
			panic("ollama tests must not reach the kubernetes domain")
		},
		DeleteK8sResource: func(string, string, string, string, func(string)) (map[string]any, error) {
			panic("ollama tests must not reach the kubernetes domain")
		},
	}, "", "")
}
