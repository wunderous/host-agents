package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderLlamaSystemdUnitPinsQwenTemplateAndGPU(t *testing.T) {
	unit := renderLlamaSystemdUnit(LlamaServerConfig{
		Port: 8080, ModelRef: "qwen3.5-0.8b-opute-llama", ArtifactPath: "/models/qwen3.5-0.8b-q4_k_m.gguf", ContextSize: 8192, GpuLayers: 999, BinaryPath: "/usr/local/bin/llama-server",
	})
	for _, expected := range []string{
		"Description=Opute llama-server runtime",
		"StartLimitIntervalSec=60s",
		"StartLimitBurst=5",
		"Slice=opute-workload.slice",
		"KillMode=control-group",
		"MemoryHigh=5G",
		"MemoryMax=6G",
		"MemorySwapMax=1G",
		"CPUQuota=600%",
		"CPUWeight=100",
		"TasksMax=4096",
		"--model /models/qwen3.5-0.8b-q4_k_m.gguf --alias \"qwen3.5-0.8b-opute-llama\"",
		"--jinja",
		"--reasoning-budget 0",
		"--n-gpu-layers 999",
		"--ctx-size 8192",
		"CUDA_VISIBLE_DEVICES=0",
	} {
		if !strings.Contains(unit, expected) {
			t.Fatalf("unit missing %q:\n%s", expected, unit)
		}
	}
}

func TestLlamaModelNameMatchesCatalogRefAndGGUFPath(t *testing.T) {
	for _, test := range []struct {
		name, modelRef string
		want           bool
	}{
		{name: "/home/opute/.local/share/opute/llama-models/qwen3.5-0.8b-base-llama.gguf", modelRef: "qwen3.5-0.8b-base-llama", want: true},
		{name: "qwen3.5-0.8b-base-llama:latest", modelRef: "qwen3.5-0.8b-base-llama", want: true},
		{name: "other-model.gguf", modelRef: "qwen3.5-0.8b-base-llama", want: false},
	} {
		if got := llamaModelNameMatches(test.name, test.modelRef); got != test.want {
			t.Errorf("llamaModelNameMatches(%q, %q) = %v, want %v", test.name, test.modelRef, got, test.want)
		}
	}
}

func TestValidateLlamaServerConfigRequiresVerifiedArtifactAndBinary(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "qwen.gguf")
	binary := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(artifact, []byte("gguf-test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("binary-test"), 0700); err != nil {
		t.Fatal(err)
	}
	hash, err := fileSHA256(artifact)
	if err != nil {
		t.Fatal(err)
	}
	binaryHash, err := fileSHA256(binary)
	if err != nil {
		t.Fatal(err)
	}
	valid := LlamaServerConfig{Port: 8080, ModelRef: "qwen3.5-0.8b-base-llama", ArtifactPath: artifact, ArtifactURI: "https://example.com/qwen3.5.gguf", ArtifactSHA256: hash, BaseModel: "Qwen/Qwen3.5-0.8B", Revision: "revision-test", TokenizerRevision: "revision-test", ChatTemplateHash: strings.Repeat("1", 64), ChatTemplateKwargs: `{"enable_thinking":false}`, Quantization: "Q4_K_M", ContextSize: 8192, GpuLayers: 999, BinaryPath: binary, BinaryVersion: "llama-server-test", BinarySHA256: binaryHash, BinarySource: "host-build", SourceRevision: "qwen35-compatible", SourceSHA256: strings.Repeat("2", 64), CudaEnabled: true}
	if err := validateLlamaServerConfig(valid); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	invalidArtifact := valid
	invalidArtifact.ArtifactSHA256 = strings.Repeat("0", 64)
	if err := validateLlamaServerConfig(invalidArtifact); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	invalidGpu := valid
	invalidGpu.GpuLayers = 998
	if err := validateLlamaServerConfig(invalidGpu); err == nil {
		t.Fatal("partial GPU configuration was accepted")
	}
	invalidQuantization := valid
	invalidQuantization.Quantization = "Q8_0"
	if err := validateLlamaServerConfig(invalidQuantization); err == nil {
		t.Fatal("non-Q4_K_M configuration was accepted")
	}
}
