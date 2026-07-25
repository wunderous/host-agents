package ops

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestValidateOllamaModelRef(t *testing.T) {
	for _, ref := range []string{"smollm:135m", "phi4-mini", "../escape"} {
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
	for _, want := range []string{
		"OLLAMA_HOST=127.0.0.1:11434",
		"OLLAMA_NO_CLOUD=1",
		"CUDA_VISIBLE_DEVICES=0",
		"NVIDIA_VISIBLE_DEVICES=0",
		"OLLAMA_INTEL_GPU=false",
		"LD_LIBRARY_PATH=/usr/lib/wsl/lib:/usr/local/lib/ollama/cuda_v12:/usr/local/lib/ollama",
		"ExecStart=/usr/local/bin/ollama serve",
	} {
		if !containsOllama(unit, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestFinalizeLocalLLMPrerequisitesGpuBlockers(t *testing.T) {
	result := &LocalLLMPrerequisitesResult{
		Supported:            true,
		SystemdUserAvailable: true,
		Architecture:         "amd64",
		NvidiaSmiOk:          false,
		CudaLibraryPresent:   false,
	}
	finalizeLocalLLMPrerequisites(result)
	if !result.ReadyForInstall {
		t.Fatal("expected readyForInstall when Linux+systemd+arch are ok")
	}
	if result.ReadyForGpuInference {
		t.Fatal("expected readyForGpuInference false without nvidia/cuda")
	}
	if len(result.Blockers) == 0 || len(result.RemediationHints) == 0 {
		t.Fatalf("expected blockers and remediation hints, got %+v", result)
	}
	foundDriver := false
	for _, hint := range result.RemediationHints {
		if containsOllama(hint, "NVIDIA driver") || containsOllama(hint, "Eco") {
			foundDriver = true
		}
	}
	if !foundDriver {
		t.Fatalf("expected GPU driver / Eco remediation hint, got %#v", result.RemediationHints)
	}

	result.NvidiaSmiOk = true
	result.CudaLibraryPresent = true
	result.OllamaServiceActive = true
	result.RuntimeGpuAccelerated = false
	finalizeLocalLLMPrerequisites(result)
	if !result.ReadyForGpuInference {
		t.Fatal("expected readyForGpuInference when nvidia+cuda ok")
	}
	foundRestart := false
	for _, blocker := range result.Blockers {
		if containsOllama(blocker, "size_vram=0") {
			foundRestart = true
		}
	}
	if !foundRestart {
		t.Fatalf("expected CPU-only runtime blocker, got %#v", result.Blockers)
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
		case "/api/ps":
			_, _ = w.Write([]byte(`{"models":[{"name":"smollm:135m","size_vram":123456789,"context_length":2048}]}`))
		case "/api/chat":
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ready"},"done":true}`))
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
	result, err := svc.ProbeLocalLLM(context.Background(), ProbeLocalLLMArgs{IncludeChat: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ready || !result.OpenAIModelsReady || !result.ChatReady || len(result.Models) != 1 || result.Models[0].Name != "smollm:135m" {
		t.Fatalf("unexpected fake Ollama probe result: %+v", result)
	}
	if !result.GpuAccelerated || result.SizeVramBytes != 123456789 {
		t.Fatalf("expected GPU probe fields, got %+v", result)
	}
}

func TestResolveLocalLLMModelRef(t *testing.T) {
	got, err := ResolveLocalLLMModelRef("", "phi")
	if err != nil || got != "phi4-mini" {
		t.Fatalf("phi preset: got %q err=%v", got, err)
	}
	got, err = ResolveLocalLLMModelRef("custom:tag", "phi")
	if err != nil || got != "custom:tag" {
		t.Fatalf("modelRef should win: got %q err=%v", got, err)
	}
	if _, err := ResolveLocalLLMModelRef("", ""); err == nil {
		t.Fatal("expected error when both empty")
	}
	if _, err := ResolveLocalLLMModelRef("", "nope"); err == nil {
		t.Fatal("expected unknown preset error")
	}
}

func TestRenderOllamaModelfileGpuCtx(t *testing.T) {
	gpu := 99
	ctx := 4096
	got, err := renderOllamaModelfile("phi4-mini", &gpu, &ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM phi4-mini", "PARAMETER num_gpu 99", "PARAMETER num_ctx 4096"} {
		if !containsOllama(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if _, err := renderOllamaModelfile("../escape", &gpu, nil, ""); err == nil {
		t.Fatal("expected invalid fromRef rejection")
	}
	tpl, err := resolveOllamaChatTemplate("", "phi-functools")
	if err != nil || !strings.Contains(tpl, "functools") {
		t.Fatalf("phi-functools template: err=%v len=%d", err, len(tpl))
	}
	withTpl, err := renderOllamaModelfile("phi4-mini", &gpu, &ctx, tpl)
	if err != nil {
		t.Fatal(err)
	}
	if !containsOllama(withTpl, "TEMPLATE") || !containsOllama(withTpl, "functools") {
		t.Fatalf("expected TEMPLATE with functools, got %q", withTpl[:min(200, len(withTpl))])
	}
}

func TestRemediationHintsForHybridSchedCrash(t *testing.T) {
	hints := remediationHintsForLocalLLMProbe(&LocalLLMProbeResult{
		LoadError: `llama-server process has terminated: GGML_ASSERT(n_inputs < GGML_SCHED_MAX_SPLIT_INPUTS) failed`,
	})
	if len(hints) == 0 || !containsOllama(hints[0], "numGpu=99") {
		t.Fatalf("expected hybrid-offload remediation, got %#v", hints)
	}
}

func TestFinalizeLocalLLMPrerequisitesLowVramHint(t *testing.T) {
	result := &LocalLLMPrerequisitesResult{
		Supported:            true,
		SystemdUserAvailable: true,
		Architecture:         "amd64",
		NvidiaSmiOk:          true,
		CudaLibraryPresent:   true,
		GpuMemoryTotalBytes:  4 * 1024 * 1024 * 1024,
	}
	finalizeLocalLLMPrerequisites(result)
	found := false
	for _, hint := range result.RemediationHints {
		if containsOllama(hint, "numGpu=99") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected low-VRAM numGpu hint, got %#v", result.RemediationHints)
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
