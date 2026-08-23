package ops

import (
	"os"
	"path/filepath"
)

// LocalLLMPrerequisitesResult is the runtime-neutral capability report used by
// the host agent. llama-server remains available as an alternate runtime;
// Ollama is the default host-wide runtime.
type LocalLLMPrerequisitesResult struct {
	Supported                bool     `json:"supported"`
	SystemdUserAvailable     bool     `json:"systemdUserAvailable"`
	Architecture             string   `json:"architecture"`
	CPUCount                 int      `json:"cpuCount"`
	ModelsDirectory          string   `json:"modelsDirectory"`
	MemoryBytes              uint64   `json:"memoryBytes,omitempty"`
	DiskAvailableBytes       uint64   `json:"diskAvailableBytes,omitempty"`
	GPU                      string   `json:"gpu,omitempty"`
	GpuMemoryTotalBytes      uint64   `json:"gpuMemoryTotalBytes,omitempty"`
	NvidiaSmiOk              bool     `json:"nvidiaSmiOk"`
	CudaLibraryPresent       bool     `json:"cudaLibraryPresent"`
	CMakePresent             bool     `json:"cmakePresent"`
	NinjaPresent             bool     `json:"ninjaPresent"`
	CudaCompilerPresent      bool     `json:"cudaCompilerPresent"`
	BuildToolchainReady      bool     `json:"buildToolchainReady"`
	DxgDevicePresent         bool     `json:"dxgDevicePresent,omitempty"`
	LlamaServerBinaryPresent bool     `json:"llamaServerBinaryPresent"`
	LlamaServerCudaEnabled   bool     `json:"llamaServerCudaEnabled"`
	LlamaServerBinarySource  string   `json:"llamaServerBinarySource,omitempty"`
	LlamaServerBuildRevision string   `json:"llamaServerBuildRevision,omitempty"`
	LlamaServerBinarySHA256  string   `json:"llamaServerBinarySha256,omitempty"`
	LlamaServerServiceActive bool     `json:"llamaServerServiceActive"`
	OllamaBinaryPresent      bool     `json:"ollamaBinaryPresent"`
	OllamaServiceActive      bool     `json:"ollamaServiceActive"`
	OllamaAPIBaseURL         string   `json:"ollamaApiBaseUrl,omitempty"`
	OllamaModel              string   `json:"ollamaModel,omitempty"`
	OllamaNumParallel        int      `json:"ollamaNumParallel,omitempty"`
	OllamaMaxLoadedModels    int      `json:"ollamaMaxLoadedModels,omitempty"`
	RuntimeGpuAccelerated    bool     `json:"runtimeGpuAccelerated,omitempty"`
	RuntimeSizeVramBytes     int64    `json:"runtimeSizeVramBytes,omitempty"`
	RuntimeLoadedModel       string   `json:"runtimeLoadedModel,omitempty"`
	ReadyForInstall          bool     `json:"readyForInstall"`
	ReadyForGpuInference     bool     `json:"readyForGpuInference"`
	Blockers                 []string `json:"blockers,omitempty"`
	RemediationHints         []string `json:"remediationHints,omitempty"`
}

type LocalLLMModelResult struct {
	URI       string `json:"uri"`
	Name      string `json:"name"`
	Digest    string `json:"digest,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

type LocalLLMProbeResult struct {
	Runtime            string                `json:"runtime,omitempty"`
	APIBaseURL         string                `json:"apiBaseUrl"`
	ModelRef           string                `json:"modelRef,omitempty"`
	EffectiveModelRef  string                `json:"effectiveModelRef,omitempty"`
	ArtifactURI        string                `json:"artifactUri,omitempty"`
	ArtifactSHA256     string                `json:"artifactSha256,omitempty"`
	BaseModel          string                `json:"baseModel,omitempty"`
	Revision           string                `json:"revision,omitempty"`
	TokenizerRevision  string                `json:"tokenizerRevision,omitempty"`
	ChatTemplateHash   string                `json:"chatTemplateHash,omitempty"`
	ChatTemplateKwargs string                `json:"chatTemplateKwargs,omitempty"`
	Quantization       string                `json:"quantization,omitempty"`
	GpuLayers          int                   `json:"gpuLayers,omitempty"`
	CudaArchitectures  string                `json:"cudaArchitectures,omitempty"`
	RuntimeBuild       string                `json:"runtimeBuild,omitempty"`
	Version            string                `json:"version,omitempty"`
	Ready              bool                  `json:"ready"`
	Models             []LocalLLMModelResult `json:"models"`
	ChatReady          bool                  `json:"chatReady,omitempty"`
	OpenAIModelsReady  bool                  `json:"openAiModelsReady,omitempty"`
	GpuAccelerated     bool                  `json:"gpuAccelerated,omitempty"`
	CudaEnabled        bool                  `json:"cudaEnabled,omitempty"`
	BinarySource       string                `json:"binarySource,omitempty"`
	BinarySHA256       string                `json:"binarySha256,omitempty"`
	SourceRevision     string                `json:"sourceRevision,omitempty"`
	SizeVramBytes      int64                 `json:"sizeVramBytes,omitempty"`
	LoadedModel        string                `json:"loadedModel,omitempty"`
	LoadError          string                `json:"loadError,omitempty"`
	RemediationHints   []string              `json:"remediationHints,omitempty"`
	ContextLength      int                   `json:"contextLength,omitempty"`
	ContextSource      string                `json:"contextSource,omitempty"`
	ContextPersisted   bool                  `json:"contextPersisted,omitempty"`
	MaxParallel        int                   `json:"maxParallel,omitempty"`
	MaxLoadedModels    int                   `json:"maxLoadedModels,omitempty"`
}

func nvidiaSmiCommand() []string {
	if _, err := os.Stat("/usr/lib/wsl/lib/nvidia-smi"); err == nil {
		return []string{"/usr/lib/wsl/lib/nvidia-smi"}
	}
	return []string{"nvidia-smi"}
}

func cudaUserLibraryPresent() bool {
	for _, candidate := range []string{
		"/usr/lib/wsl/lib/libcuda.so.1",
		"/usr/lib/wsl/lib/libcuda.so",
		"/usr/lib/x86_64-linux-gnu/libcuda.so.1",
		"/usr/lib64/libcuda.so.1",
	} {
		if _, err := os.Stat(filepath.Clean(candidate)); err == nil {
			return true
		}
	}
	return false
}
