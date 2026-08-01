package ops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// OllamaConfig contains only validated, host-local configuration. Runtime
// orchestration is intentionally kept behind the host-agent operation layer.
type OllamaConfig struct {
	Port       int
	ModelsDir  string
	BinaryPath string
	Version    string
	Sha256     string
}

const (
	ollamaVersion     = "v0.30.8"
	ollamaAMD64SHA256 = "ffe2b2c2f2f5f5b30c081ec353c2e0bb2d9ead516064a8e22663b24b8fd8dca0"
	ollamaARM64SHA256 = "668a6f934b0b0455128bb4a76c9e50b9e5f274f9dc7710a066b7073e5bd36588"
)

type LocalLLMPrerequisitesResult struct {
	Supported             bool     `json:"supported"`
	SystemdUserAvailable  bool     `json:"systemdUserAvailable"`
	Architecture          string   `json:"architecture"`
	CPUCount              int      `json:"cpuCount"`
	ModelsDirectory       string   `json:"modelsDirectory"`
	MemoryBytes           uint64   `json:"memoryBytes,omitempty"`
	DiskAvailableBytes    uint64   `json:"diskAvailableBytes,omitempty"`
	GPU                   string   `json:"gpu,omitempty"`
	GpuMemoryTotalBytes   uint64   `json:"gpuMemoryTotalBytes,omitempty"`
	NvidiaSmiOk           bool     `json:"nvidiaSmiOk"`
	CudaLibraryPresent    bool     `json:"cudaLibraryPresent"`
	DxgDevicePresent      bool     `json:"dxgDevicePresent,omitempty"`
	OllamaBinaryPresent   bool     `json:"ollamaBinaryPresent"`
	OllamaServiceActive   bool     `json:"ollamaServiceActive"`
	RuntimeGpuAccelerated bool     `json:"runtimeGpuAccelerated,omitempty"`
	RuntimeSizeVramBytes  int64    `json:"runtimeSizeVramBytes,omitempty"`
	RuntimeLoadedModel    string   `json:"runtimeLoadedModel,omitempty"`
	ReadyForInstall       bool     `json:"readyForInstall"`
	ReadyForGpuInference  bool     `json:"readyForGpuInference"`
	Blockers              []string `json:"blockers,omitempty"`
	RemediationHints      []string `json:"remediationHints,omitempty"`
}

type LocalLLMModelResult struct {
	Name      string `json:"name"`
	Digest    string `json:"digest,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

type LocalLLMProbeResult struct {
	APIBaseURL        string                `json:"apiBaseUrl"`
	Version           string                `json:"version,omitempty"`
	Ready             bool                  `json:"ready"`
	Models            []LocalLLMModelResult `json:"models"`
	ChatReady         bool                  `json:"chatReady,omitempty"`
	OpenAIModelsReady bool                  `json:"openAiModelsReady,omitempty"`
	GpuAccelerated    bool                  `json:"gpuAccelerated,omitempty"`
	SizeVramBytes     int64                 `json:"sizeVramBytes,omitempty"`
	LoadedModel       string                `json:"loadedModel,omitempty"`
	LoadError         string                `json:"loadError,omitempty"`
	RemediationHints  []string              `json:"remediationHints,omitempty"`
	ContextLength     int                   `json:"contextLength,omitempty"`
}

// InstallLocalLLMModelArgs pulls an Ollama registry model and optionally creates
// a durable derived tag with Modelfile parameters (num_gpu / num_ctx). Full GPU
// offload (numGpu near layer count, e.g. 99) is required for some edge models
// that abort on hybrid CPU/GPU splits. Callers may pass modelPreset=gemma|qwen
// or an explicit modelRef. Optional Template rewrites the chat TEMPLATE.
type InstallLocalLLMModelArgs struct {
	ModelRef string
	CreateAs string
	NumGpu   *int
	NumCtx   *int
	Template string
}

// ConfigureLocalLLMModelArgs creates or replaces a local Ollama model tag from
// an already-pulled base model without re-downloading blobs.
type ConfigureLocalLLMModelArgs struct {
	ModelRef string
	FromRef  string
	NumGpu   *int
	NumCtx   *int
	Template string
}

// ProbeLocalLLMArgs optionally warm-loads a model to verify GPU inference.
type ProbeLocalLLMArgs struct {
	IncludeChat bool
	ModelRef    string
	NumGpu      *int
	NumCtx      *int
}

var ollamaModelRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)
var sha256Hex = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
var ollamaVersionRef = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// LocalLLMModelPresets maps standard chat presets to Ollama registry refs.
var LocalLLMModelPresets = map[string]string{
	"gemma": "gemma4:e2b",
	"qwen":  "qwen3.5:2b",
}

// localLLMFullGpuOffloadLayers requests full discrete-GPU offload for chat models.
const localLLMFullGpuOffloadLayers = 99

func withDefaultFullGpuOffload(numGpu *int) *int {
	if numGpu != nil {
		return numGpu
	}
	value := localLLMFullGpuOffloadLayers
	return &value
}

func validateGpuOffloadLayers(numGpu *int) error {
	if numGpu != nil && *numGpu == 0 {
		return fmt.Errorf("numGpu=0 (CPU inference) is not supported for Opute local LLM models; discrete GPU offload is required")
	}
	return nil
}

func (s *HostOperationsService) requireGpuInferenceReady() error {
	prereqs, err := s.CheckLocalLLMPrerequisites()
	if err != nil {
		return err
	}
	if prereqs == nil || !prereqs.ReadyForGpuInference {
		return fmt.Errorf("GPU inference is required for Opute local LLM models (gemma, qwen); resolve check_local_llm_prerequisites blockers before install or start")
	}
	if prereqs.OllamaServiceActive && prereqs.RuntimeLoadedModel != "" && !prereqs.RuntimeGpuAccelerated {
		return fmt.Errorf("Ollama is running on CPU (size_vram=0); call start_local_llm_runtime after the discrete GPU is healthy, then probe_local_llm and confirm sizeVramBytes > 0")
	}
	return nil
}

// ResolveLocalLLMModelRef returns modelRef, or expands modelPreset (gemma|qwen).
func ResolveLocalLLMModelRef(modelRef string, modelPreset string) (string, error) {
	ref := strings.TrimSpace(modelRef)
	if ref != "" {
		if err := ValidateOllamaModelRef(ref); err != nil {
			return "", err
		}
		return ref, nil
	}
	preset := strings.ToLower(strings.TrimSpace(modelPreset))
	if preset == "" {
		return "", fmt.Errorf("modelRef or modelPreset (gemma|qwen) is required")
	}
	resolved, ok := LocalLLMModelPresets[preset]
	if !ok {
		return "", fmt.Errorf("unknown modelPreset %q (use gemma|qwen or pass modelRef)", modelPreset)
	}
	return resolved, nil
}

func ValidateOllamaModelRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > 200 || !ollamaModelRef.MatchString(ref) {
		return fmt.Errorf("invalid Ollama model identifier")
	}
	return nil
}

func ValidateOllamaConfig(cfg OllamaConfig) error {
	if cfg.Port == 0 {
		cfg.Port = 11434
	}
	if cfg.Port < 1024 || cfg.Port > 65535 {
		return fmt.Errorf("invalid Ollama port")
	}
	if cfg.ModelsDir == "" || !path.IsAbs(cfg.ModelsDir) || !safeOllamaPath(cfg.ModelsDir) {
		return fmt.Errorf("models directory must be absolute")
	}
	if cfg.BinaryPath == "" || !path.IsAbs(cfg.BinaryPath) || !safeOllamaPath(cfg.BinaryPath) {
		return fmt.Errorf("binary path must be absolute")
	}
	if !ollamaVersionRef.MatchString(cfg.Version) {
		return fmt.Errorf("Ollama version is required")
	}
	if !sha256Hex.MatchString(cfg.Sha256) {
		return fmt.Errorf("Ollama checksum must be sha256 hex")
	}
	return nil
}

// Paths are rendered into systemd ExecStart/Environment directives and are
// never interpreted through a shell. Reject control and quoting/metacharacter
// bytes nevertheless so malformed user-provided configuration cannot alter a
// unit or a later fixed-argument command.
func safeOllamaPath(value string) bool {
	return !strings.ContainsAny(value, "\r\n\x00\"'`$;&|<>\\")
}

func OllamaLoopbackURL(port int) string {
	if port == 0 {
		port = 11434
	}
	return fmt.Sprintf("http://%s/v1", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
}

func RenderOllamaSystemdUnit(cfg OllamaConfig) (string, error) {
	if err := ValidateOllamaConfig(cfg); err != nil {
		return "", err
	}
	// Pin CUDA to the first (dedicated) NVIDIA GPU. On ASUS Optimus hosts the
	// discrete NVIDIA device is the only CUDA target once Eco mode is off; the
	// AMD iGPU is not exposed to CUDA/WSL, so device 0 is the dGPU. WSL needs
	// /usr/lib/wsl/lib (Windows NVIDIA driver GPU-PV) on LD_LIBRARY_PATH plus
	// Ollama's bundled cuda_v12 ggml libs beside the binary tree.
	libRoot := filepath.Join(filepath.Dir(filepath.Dir(cfg.BinaryPath)), "lib", "ollama")
	return fmt.Sprintf(`[Unit]
Description=Opute-managed Ollama runtime
After=network-online.target

[Service]
ExecStart=%s serve
Environment=OLLAMA_HOST=127.0.0.1:%d
Environment=OLLAMA_MODELS=%s
Environment=OLLAMA_NO_CLOUD=1
Environment=CUDA_VISIBLE_DEVICES=0
Environment=NVIDIA_VISIBLE_DEVICES=0
Environment=OLLAMA_INTEL_GPU=false
Environment=LD_LIBRARY_PATH=/usr/lib/wsl/lib:%s/cuda_v12:%s
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
`, cfg.BinaryPath, cfg.Port, cfg.ModelsDir, libRoot, libRoot), nil
}

func defaultOllamaConfig() (OllamaConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return OllamaConfig{}, fmt.Errorf("resolve home directory: %w", err)
	}
	sha := ollamaAMD64SHA256
	if runtime.GOARCH == "arm64" {
		sha = ollamaARM64SHA256
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return OllamaConfig{}, fmt.Errorf("unsupported Ollama architecture %q", runtime.GOARCH)
	}
	root := filepath.Join(home, ".local", "share", "opute", "ollama")
	return OllamaConfig{Port: 11434, ModelsDir: filepath.Join(root, "models"), BinaryPath: filepath.Join(root, "bin", "ollama"), Version: ollamaVersion, Sha256: sha}, nil
}

func (s *HostOperationsService) CheckLocalLLMPrerequisites() (*LocalLLMPrerequisitesResult, error) {
	cfg, err := defaultOllamaConfig()
	if err != nil {
		return nil, err
	}
	_, systemdErr := s.hostCommandRunner([]string{"systemctl", "--user", "show-environment"}, nil, 10*time.Second)
	result := &LocalLLMPrerequisitesResult{
		Supported:            runtime.GOOS == "linux",
		SystemdUserAvailable: systemdErr == nil,
		Architecture:         runtime.GOARCH,
		CPUCount:             runtime.NumCPU(),
		ModelsDirectory:      cfg.ModelsDir,
	}
	if data, readErr := os.ReadFile("/proc/meminfo"); readErr == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "MemTotal:" {
				if kb, parseErr := strconv.ParseUint(fields[1], 10, 64); parseErr == nil {
					result.MemoryBytes = kb * 1024
				}
			}
		}
	}
	if res, dfErr := s.hostCommandRunner([]string{"df", "-Pk", cfg.ModelsDir}, nil, 10*time.Second); dfErr == nil && res.ExitCode == 0 {
		lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[len(lines)-1])
			if len(fields) >= 4 {
				if kb, parseErr := strconv.ParseUint(fields[3], 10, 64); parseErr == nil {
					result.DiskAvailableBytes = kb * 1024
				}
			}
		}
	}
	if _, err := os.Stat(cfg.BinaryPath); err == nil {
		result.OllamaBinaryPresent = true
	}
	if res, svcErr := s.hostCommandRunner([]string{"systemctl", "--user", "is-active", "opute-ollama.service"}, nil, 5*time.Second); svcErr == nil {
		result.OllamaServiceActive = strings.TrimSpace(res.Stdout) == "active"
	}
	if res, gpuErr := s.hostCommandRunner(append(nvidiaSmiCommand(), "--query-gpu=name,memory.total", "--format=csv,noheader,nounits"), nil, 5*time.Second); gpuErr == nil && res.ExitCode == 0 {
		line := strings.TrimSpace(strings.Split(res.Stdout, "\n")[0])
		if line != "" && !strings.Contains(strings.ToLower(line), "failed") {
			parts := strings.Split(line, ",")
			gpuName := strings.TrimSpace(parts[0])
			if gpuName != "" {
				result.GPU = gpuName
				result.NvidiaSmiOk = true
			}
			if len(parts) >= 2 {
				if mb, parseErr := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64); parseErr == nil && mb > 0 {
					result.GpuMemoryTotalBytes = mb * 1024 * 1024
				}
			}
		}
	}
	result.CudaLibraryPresent = cudaUserLibraryPresent()
	if _, err := os.Stat("/dev/dxg"); err == nil {
		result.DxgDevicePresent = true
	}
	if result.OllamaServiceActive {
		if probe, probeErr := s.ProbeLocalLLM(context.Background(), ProbeLocalLLMArgs{}); probeErr == nil && probe != nil {
			result.RuntimeGpuAccelerated = probe.GpuAccelerated
			result.RuntimeSizeVramBytes = probe.SizeVramBytes
			result.RuntimeLoadedModel = probe.LoadedModel
		}
	}
	finalizeLocalLLMPrerequisites(result)
	return result, nil
}

func nvidiaSmiCommand() []string {
	if _, err := os.Stat("/usr/lib/wsl/lib/nvidia-smi"); err == nil {
		return []string{"/usr/lib/wsl/lib/nvidia-smi"}
	}
	return []string{"nvidia-smi"}
}

func cudaUserLibraryPresent() bool {
	candidates := []string{
		"/usr/lib/wsl/lib/libcuda.so.1",
		"/usr/lib/wsl/lib/libcuda.so",
		"/usr/lib/x86_64-linux-gnu/libcuda.so.1",
		"/usr/lib64/libcuda.so.1",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// finalizeLocalLLMPrerequisites derives readiness flags and user-facing blockers.
// It never mutates the host GPU/driver stack — operators prepare NVIDIA/Eco/MUX
// outside Opute; host tools only diagnose and then install/start Ollama.
func finalizeLocalLLMPrerequisites(result *LocalLLMPrerequisitesResult) {
	if result == nil {
		return
	}
	result.Blockers = nil
	result.RemediationHints = nil
	result.ReadyForGpuInference = result.NvidiaSmiOk && result.CudaLibraryPresent
	result.ReadyForInstall = result.Supported && result.SystemdUserAvailable && (result.Architecture == "amd64" || result.Architecture == "arm64") && result.ReadyForGpuInference

	if !result.Supported {
		result.Blockers = append(result.Blockers, "Host OS is not Linux. Opute-managed Ollama requires Linux or WSL2.")
		result.RemediationHints = append(result.RemediationHints, "Run the Opute host agent on Linux or inside WSL2, then retry check_local_llm_prerequisites.")
	}
	if result.Supported && !result.SystemdUserAvailable {
		result.Blockers = append(result.Blockers, "systemd --user is unavailable, so Ollama cannot be installed as an Opute-managed user service.")
		result.RemediationHints = append(result.RemediationHints, "Enable lingering user systemd (`loginctl enable-linger`) and ensure `systemctl --user` works for the host-agent user.")
	}
	if result.Supported && result.Architecture != "amd64" && result.Architecture != "arm64" {
		result.Blockers = append(result.Blockers, fmt.Sprintf("Unsupported CPU architecture %q for the Opute-managed Ollama package.", result.Architecture))
	}
	if !result.NvidiaSmiOk {
		result.Blockers = append(result.Blockers, "nvidia-smi did not report a usable NVIDIA GPU. Opute local LLM models require discrete GPU inference; CPU-only Ollama is not supported.")
		result.RemediationHints = append(result.RemediationHints,
			"Install or repair the NVIDIA driver on the host OS. On WSL2, that is the Windows NVIDIA driver with WSL support (not a separate Linux driver package).",
			"If this is a laptop with Optimus/MUX/Eco GPU modes, ensure the discrete NVIDIA GPU is powered on (not Eco/disabled), then re-run check_local_llm_prerequisites.",
		)
	}
	if result.NvidiaSmiOk && !result.CudaLibraryPresent {
		result.Blockers = append(result.Blockers, "NVIDIA GPU is visible to nvidia-smi, but no CUDA user-mode library (libcuda) was found for this process.")
		result.RemediationHints = append(result.RemediationHints,
			"On WSL2, confirm /usr/lib/wsl/lib/libcuda.so* exists after installing a current Windows NVIDIA driver; restart WSL if the driver was just installed.",
			"On native Linux, install the NVIDIA driver/CUDA userspace packages so libcuda.so.1 is on the library path.",
		)
	}
	if result.OllamaServiceActive && result.ReadyForGpuInference && result.RuntimeLoadedModel != "" && !result.RuntimeGpuAccelerated {
		result.Blockers = append(result.Blockers, "Ollama is running but reports CPU-only inference (size_vram=0) despite GPU prerequisites looking healthy.")
		result.RemediationHints = append(result.RemediationHints,
			"Call start_local_llm_runtime (rewrites the Opute Ollama unit with CUDA pin + WSL library path) after the GPU driver is healthy, then probe_local_llm and confirm sizeVramBytes > 0.",
		)
	}
	if result.ReadyForGpuInference && result.GpuMemoryTotalBytes > 0 && result.GpuMemoryTotalBytes < 6*1024*1024*1024 {
		result.RemediationHints = append(result.RemediationHints,
			"This GPU reports under 6 GiB VRAM. Some large or multimodal models can crash on hybrid CPU/GPU layer splits (GGML_SCHED_MAX_SPLIT_INPUTS). Use install_local_llm_model / configure_local_llm_model with numGpu=99 (full GPU offload) and a bounded numCtx, then probe_local_llm with that modelRef. Standard presets: modelPreset=gemma|qwen.",
		)
	}
	if !result.ReadyForGpuInference {
		result.Blockers = append(result.Blockers, "GPU inference prerequisites are not satisfied. Opute chat models (gemma, qwen) run exclusively on the discrete GPU.")
	}
}

func (s *HostOperationsService) InstallLocalLLMModel(ctx context.Context, args InstallLocalLLMModelArgs) (*LocalLLMProbeResult, error) {
	if err := s.requireSharedHostOwner("install_local_llm_model"); err != nil {
		return nil, err
	}
	if err := s.requireGpuInferenceReady(); err != nil {
		return nil, err
	}
	if err := validateGpuOffloadLayers(args.NumGpu); err != nil {
		return nil, err
	}
	args.NumGpu = withDefaultFullGpuOffload(args.NumGpu)
	if err := ValidateOllamaModelRef(args.ModelRef); err != nil {
		return nil, err
	}
	if args.CreateAs != "" {
		if err := ValidateOllamaModelRef(args.CreateAs); err != nil {
			return nil, fmt.Errorf("createAs: %w", err)
		}
	}
	cfg, err := defaultOllamaConfig()
	if err != nil {
		return nil, err
	}
	if err := s.ensureOllamaInstalled(ctx, cfg); err != nil {
		return nil, err
	}
	if err := s.startOllama(ctx, cfg); err != nil {
		return nil, err
	}
	if err := s.pullOllamaModel(ctx, cfg, args.ModelRef); err != nil {
		return nil, err
	}
	if args.CreateAs != "" || args.NumGpu != nil || args.NumCtx != nil || strings.TrimSpace(args.Template) != "" {
		createName := strings.TrimSpace(args.CreateAs)
		if createName == "" {
			createName = strings.TrimSpace(args.ModelRef)
		}
		if err := s.createOllamaModel(ctx, cfg, ConfigureLocalLLMModelArgs{
			ModelRef: createName,
			FromRef:  strings.TrimSpace(args.ModelRef),
			NumGpu:   args.NumGpu,
			NumCtx:   args.NumCtx,
			Template: args.Template,
		}); err != nil {
			return nil, err
		}
	}
	probeModel := strings.TrimSpace(args.CreateAs)
	if probeModel == "" {
		probeModel = strings.TrimSpace(args.ModelRef)
	}
	return s.ProbeLocalLLM(ctx, ProbeLocalLLMArgs{ModelRef: probeModel, NumGpu: args.NumGpu, NumCtx: args.NumCtx, IncludeChat: true})
}

func (s *HostOperationsService) ConfigureLocalLLMModel(ctx context.Context, args ConfigureLocalLLMModelArgs) (*LocalLLMProbeResult, error) {
	if err := s.requireSharedHostOwner("configure_local_llm_model"); err != nil {
		return nil, err
	}
	if err := s.requireGpuInferenceReady(); err != nil {
		return nil, err
	}
	if err := validateGpuOffloadLayers(args.NumGpu); err != nil {
		return nil, err
	}
	args.NumGpu = withDefaultFullGpuOffload(args.NumGpu)
	if err := ValidateOllamaModelRef(args.ModelRef); err != nil {
		return nil, err
	}
	from := strings.TrimSpace(args.FromRef)
	if from == "" {
		from = strings.TrimSpace(args.ModelRef)
	}
	if err := ValidateOllamaModelRef(from); err != nil {
		return nil, fmt.Errorf("fromRef: %w", err)
	}
	cfg, err := defaultOllamaConfig()
	if err != nil {
		return nil, err
	}
	if err := s.ensureOllamaInstalled(ctx, cfg); err != nil {
		return nil, err
	}
	if err := s.startOllama(ctx, cfg); err != nil {
		return nil, err
	}
	args.FromRef = from
	if err := s.createOllamaModel(ctx, cfg, args); err != nil {
		return nil, err
	}
	return s.ProbeLocalLLM(ctx, ProbeLocalLLMArgs{ModelRef: strings.TrimSpace(args.ModelRef), NumGpu: args.NumGpu, NumCtx: args.NumCtx, IncludeChat: true})
}

func (s *HostOperationsService) pullOllamaModel(ctx context.Context, cfg OllamaConfig, modelRef string) error {
	reqBody, err := json.Marshal(map[string]any{"name": strings.TrimSpace(modelRef), "stream": false})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/pull", cfg.Port), strings.NewReader(string(reqBody)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 30 * time.Minute}).Do(request)
	if err != nil {
		return fmt.Errorf("pull Ollama model: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("pull Ollama model: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func resolveOllamaChatTemplate(template string) (string, error) {
	raw := strings.TrimSpace(template)
	if raw == "" {
		return "", nil
	}
	if strings.Contains(raw, `"""`) {
		return "", fmt.Errorf("template must not contain triple quotes")
	}
	if len(raw) > 64*1024 {
		return "", fmt.Errorf("template exceeds 64 KiB")
	}
	return raw, nil
}

func renderOllamaModelfile(fromRef string, numGpu *int, numCtx *int, template string) (string, error) {
	if err := ValidateOllamaModelRef(fromRef); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s\n", strings.TrimSpace(fromRef))
	if numGpu != nil {
		if *numGpu < 0 || *numGpu > 999 {
			return "", fmt.Errorf("numGpu must be between 0 and 999")
		}
		fmt.Fprintf(&b, "PARAMETER num_gpu %d\n", *numGpu)
	}
	if numCtx != nil {
		if *numCtx < 128 || *numCtx > 262144 {
			return "", fmt.Errorf("numCtx must be between 128 and 262144")
		}
		fmt.Fprintf(&b, "PARAMETER num_ctx %d\n", *numCtx)
	}
	if strings.TrimSpace(template) != "" {
		fmt.Fprintf(&b, "TEMPLATE \"\"\"\n%s\n\"\"\"\n", template)
	}
	return b.String(), nil
}

func (s *HostOperationsService) createOllamaModel(ctx context.Context, cfg OllamaConfig, args ConfigureLocalLLMModelArgs) error {
	template, err := resolveOllamaChatTemplate(args.Template)
	if err != nil {
		return err
	}
	fromRef := strings.TrimSpace(args.FromRef)
	if fromRef == "" {
		fromRef = strings.TrimSpace(args.ModelRef)
	}
	if err := ValidateOllamaModelRef(fromRef); err != nil {
		return fmt.Errorf("fromRef: %w", err)
	}
	if err := ValidateOllamaModelRef(args.ModelRef); err != nil {
		return err
	}

	// Ollama ≥0.6 /api/create no longer accepts a Modelfile blob; it wants
	// structured from/template/parameters (CLI `ollama create -f` still parses
	// Modelfiles locally).
	payload := map[string]any{
		"model":  strings.TrimSpace(args.ModelRef),
		"from":   fromRef,
		"stream": false,
	}
	parameters := map[string]any{}
	numGpu := withDefaultFullGpuOffload(args.NumGpu)
	if *numGpu < 1 || *numGpu > 999 {
		return fmt.Errorf("numGpu must be between 1 and 999")
	}
	parameters["num_gpu"] = *numGpu
	if args.NumCtx != nil {
		if *args.NumCtx < 128 || *args.NumCtx > 262144 {
			return fmt.Errorf("numCtx must be between 128 and 262144")
		}
		parameters["num_ctx"] = *args.NumCtx
	}
	if len(parameters) > 0 {
		payload["parameters"] = parameters
	}
	if strings.TrimSpace(template) != "" {
		payload["template"] = template
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/create", cfg.Port), strings.NewReader(string(reqBody)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 10 * time.Minute}).Do(request)
	if err != nil {
		return fmt.Errorf("create Ollama model: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("create Ollama model: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (s *HostOperationsService) StartLocalLLMRuntime(ctx context.Context) (*LocalLLMProbeResult, error) {
	if err := s.requireSharedHostOwner("start_local_llm_runtime"); err != nil {
		return nil, err
	}
	if err := s.requireGpuInferenceReady(); err != nil {
		return nil, err
	}
	cfg, err := defaultOllamaConfig()
	if err != nil {
		return nil, err
	}
	if err := s.ensureOllamaInstalled(ctx, cfg); err != nil {
		return nil, err
	}
	if err := s.startOllama(ctx, cfg); err != nil {
		return nil, err
	}
	return s.ProbeLocalLLM(ctx, ProbeLocalLLMArgs{})
}

func (s *HostOperationsService) StopLocalLLMRuntime(ctx context.Context) error {
	if err := s.requireSharedHostOwner("stop_local_llm_runtime"); err != nil {
		return err
	}
	_, err := s.hostCommandRunnerContext(ctx, []string{"systemctl", "--user", "stop", "opute-ollama.service"}, nil, 30*time.Second)
	return err
}

func (s *HostOperationsService) RemoveLocalLLMModel(ctx context.Context, modelRef string, purge bool) error {
	if err := s.requireSharedHostOwner("remove_local_llm_model"); err != nil {
		return err
	}
	cfg, err := defaultOllamaConfig()
	if err != nil {
		return err
	}
	if purge {
		probe, probeErr := s.ProbeLocalLLM(ctx, ProbeLocalLLMArgs{})
		if probeErr == nil && probe != nil {
			for _, model := range probe.Models {
				_ = s.deleteOllamaModel(ctx, cfg, model.Name)
			}
		}
		// Best-effort wipe of leftover blobs/manifests under the managed models dir.
		if err := os.RemoveAll(cfg.ModelsDir); err != nil {
			return fmt.Errorf("purge Ollama models directory: %w", err)
		}
		if err := os.MkdirAll(cfg.ModelsDir, 0700); err != nil {
			return fmt.Errorf("recreate Ollama models directory: %w", err)
		}
		return nil
	}
	if err := ValidateOllamaModelRef(modelRef); err != nil {
		return err
	}
	return s.deleteOllamaModel(ctx, cfg, modelRef)
}

func (s *HostOperationsService) deleteOllamaModel(ctx context.Context, cfg OllamaConfig, modelRef string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("http://127.0.0.1:%d/api/delete", cfg.Port), strings.NewReader(fmt.Sprintf(`{"name":%q}`, strings.TrimSpace(modelRef))))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 2 * time.Minute}).Do(request)
	if err != nil {
		return fmt.Errorf("remove Ollama model: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("remove Ollama model: status %d", response.StatusCode)
	}
	return nil
}

func (s *HostOperationsService) ProbeLocalLLM(ctx context.Context, args ProbeLocalLLMArgs) (*LocalLLMProbeResult, error) {
	cfg, err := defaultOllamaConfig()
	if err != nil {
		return nil, err
	}
	result := &LocalLLMProbeResult{APIBaseURL: OllamaLoopbackURL(cfg.Port), Models: []LocalLLMModelResult{}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/version", cfg.Port), nil)
	if err != nil {
		return nil, err
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return result, nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, nil
	}
	var version struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&version); err != nil {
		return nil, fmt.Errorf("decode Ollama version: %w", err)
	}
	result.Version, result.Ready = version.Version, true
	modelsRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/tags", cfg.Port), nil)
	if err != nil {
		return nil, err
	}
	modelsResponse, err := (&http.Client{Timeout: 10 * time.Second}).Do(modelsRequest)
	if err != nil {
		return result, nil
	}
	defer modelsResponse.Body.Close()
	if modelsResponse.StatusCode == http.StatusOK {
		var tags struct {
			Models []struct {
				Name   string `json:"name"`
				Digest string `json:"digest"`
				Size   int64  `json:"size"`
			} `json:"models"`
		}
		if err := json.NewDecoder(io.LimitReader(modelsResponse.Body, 1<<20)).Decode(&tags); err == nil {
			for _, model := range tags.Models {
				result.Models = append(result.Models, LocalLLMModelResult{Name: model.Name, Digest: model.Digest, SizeBytes: model.Size})
			}
		}
	}
	openAIRequest, openAIErr := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1/models", cfg.Port), nil)
	if openAIErr == nil {
		openAIResponse, requestErr := (&http.Client{Timeout: 10 * time.Second}).Do(openAIRequest)
		if requestErr == nil {
			defer openAIResponse.Body.Close()
			result.OpenAIModelsReady = openAIResponse.StatusCode == http.StatusOK
		}
	}

	warmModel := strings.TrimSpace(args.ModelRef)
	if warmModel == "" && args.IncludeChat && len(result.Models) > 0 {
		warmModel = result.Models[0].Name
	}
	if warmModel != "" {
		options := map[string]any{"num_predict": 8}
		numGpu := withDefaultFullGpuOffload(args.NumGpu)
		options["num_gpu"] = *numGpu
		if args.NumCtx != nil {
			options["num_ctx"] = *args.NumCtx
		}
		payload, _ := json.Marshal(map[string]any{
			"model":    warmModel,
			"stream":   false,
			"think":    false,
			"messages": []map[string]string{{"role": "user", "content": "Reply with one word: ready"}},
			"options":  options,
		})
		chatRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/chat", cfg.Port), strings.NewReader(string(payload)))
		if requestErr == nil {
			chatRequest.Header.Set("Content-Type", "application/json")
			chatResponse, chatErr := (&http.Client{Timeout: 3 * time.Minute}).Do(chatRequest)
			if chatErr != nil {
				result.LoadError = chatErr.Error()
			} else {
				defer chatResponse.Body.Close()
				body, _ := io.ReadAll(io.LimitReader(chatResponse.Body, 1<<20))
				if chatResponse.StatusCode >= 200 && chatResponse.StatusCode < 300 {
					result.ChatReady = true
				} else {
					result.LoadError = strings.TrimSpace(string(body))
					if result.LoadError == "" {
						result.LoadError = fmt.Sprintf("chat status %d", chatResponse.StatusCode)
					}
				}
			}
		}
	} else if args.IncludeChat && result.Ready && len(result.Models) > 0 {
		payload, _ := json.Marshal(map[string]any{"model": result.Models[0].Name, "messages": []map[string]string{{"role": "user", "content": "Reply with one word: ready"}}, "stream": false, "options": map[string]any{"num_predict": 8}})
		chatRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", cfg.Port), strings.NewReader(string(payload)))
		if requestErr == nil {
			chatRequest.Header.Set("Content-Type", "application/json")
			chatResponse, chatErr := (&http.Client{Timeout: 2 * time.Minute}).Do(chatRequest)
			if chatErr == nil {
				defer chatResponse.Body.Close()
				result.ChatReady = chatResponse.StatusCode >= 200 && chatResponse.StatusCode < 300
			}
		}
	}

	psRequest, psErr := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/ps", cfg.Port), nil)
	if psErr == nil {
		psResponse, requestErr := (&http.Client{Timeout: 10 * time.Second}).Do(psRequest)
		if requestErr == nil {
			defer psResponse.Body.Close()
			if psResponse.StatusCode == http.StatusOK {
				var ps struct {
					Models []struct {
						Name          string `json:"name"`
						SizeVRAM      int64  `json:"size_vram"`
						SizeVram      int64  `json:"sizeVram"`
						ContextLength int    `json:"context_length"`
					} `json:"models"`
				}
				if err := json.NewDecoder(io.LimitReader(psResponse.Body, 1<<20)).Decode(&ps); err == nil {
					for _, model := range ps.Models {
						vram := model.SizeVRAM
						if vram == 0 {
							vram = model.SizeVram
						}
						if vram > result.SizeVramBytes {
							result.SizeVramBytes = vram
							result.LoadedModel = model.Name
							result.ContextLength = model.ContextLength
						}
					}
					result.GpuAccelerated = result.SizeVramBytes > 0
				}
			}
		}
	}
	if warmModel != "" && result.ChatReady && !result.GpuAccelerated {
		result.Ready = false
		result.ChatReady = false
		if result.LoadError == "" {
			result.LoadError = "model loaded with size_vram=0 (CPU path); Opute local LLM models require discrete GPU inference"
		}
	}
	result.RemediationHints = remediationHintsForLocalLLMProbe(result)
	return result, nil
}

func remediationHintsForLocalLLMProbe(result *LocalLLMProbeResult) []string {
	if result == nil {
		return nil
	}
	var hints []string
	errText := strings.ToLower(result.LoadError)
	if strings.Contains(errText, "ggml_sched_max_split_inputs") || strings.Contains(errText, "n_inputs") {
		hints = append(hints,
			"Model load crashed on a hybrid CPU/GPU scheduler split. Re-run configure_local_llm_model (or install_local_llm_model with createAs) using numGpu=99 for full discrete-GPU offload and a bounded numCtx, then probe_local_llm with that modelRef.",
		)
	}
	if result.LoadError != "" && result.SizeVramBytes == 0 {
		hints = append(hints,
			"Call check_local_llm_prerequisites and resolve nvidia-smi / libcuda blockers on the host OS before retrying. Opute does not install NVIDIA drivers or change laptop Eco/MUX modes.",
		)
	}
	if result.Ready && result.LoadedModel != "" && !result.GpuAccelerated {
		hints = append(hints,
			"Model is loaded on CPU (size_vram=0). Opute supports GPU-only inference for gemma/qwen — enable the discrete GPU, restart opute-ollama.service, and re-run with numGpu=99.",
		)
	}
	return hints
}

func zstdExtractorAvailable() bool {
	for _, name := range []string{"zstd", "unzstd"} {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	return false
}

func ensureZstdForOllamaExtract(ctx context.Context) error {
	if zstdExtractorAvailable() {
		return nil
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return fmt.Errorf("zstd or unzstd is required to extract the Ollama archive; install the zstd package through the host OS package manager")
	}
	if err := runPrivilegedPackageCommand(ctx, "apt-get", "update"); err != nil {
		return fmt.Errorf("install zstd prerequisite: %w", err)
	}
	if err := runPrivilegedPackageCommand(ctx, "apt-get", "install", "-y", "zstd"); err != nil {
		return fmt.Errorf("install zstd package: %w", err)
	}
	if !zstdExtractorAvailable() {
		return fmt.Errorf("zstd package installed but zstd/unzstd still not in PATH")
	}
	return nil
}

func ollamaTarExtractArgs(archivePath, rootDir string) []string {
	if _, err := exec.LookPath("unzstd"); err == nil {
		return []string{"tar", "--no-same-owner", "--use-compress-program=unzstd", "-xf", archivePath, "-C", rootDir}
	}
	return []string{"tar", "--no-same-owner", "--zstd", "-xf", archivePath, "-C", rootDir}
}

func (s *HostOperationsService) ensureOllamaInstalled(ctx context.Context, cfg OllamaConfig) error {
	if _, err := os.Stat(cfg.BinaryPath); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.BinaryPath), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.ModelsDir, 0700); err != nil {
		return err
	}
	rootDir := filepath.Dir(filepath.Dir(cfg.BinaryPath))
	archivePath := filepath.Join(rootDir, "ollama.tar.zst")
	url := fmt.Sprintf("https://github.com/ollama/ollama/releases/download/%s/ollama-linux-%s.tar.zst", cfg.Version, runtime.GOARCH)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 10 * time.Minute}).Do(request)
	if err != nil {
		return fmt.Errorf("download Ollama: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download Ollama: status %d", response.StatusCode)
	}
	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(file, hash), response.Body); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), cfg.Sha256) {
		_ = os.Remove(archivePath)
		return fmt.Errorf("Ollama checksum verification failed")
	}
	if err := ensureZstdForOllamaExtract(ctx); err != nil {
		return err
	}
	res, err := s.hostCommandRunnerContext(ctx, ollamaTarExtractArgs(archivePath, rootDir), nil, 5*time.Minute)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		stderr := strings.TrimSpace(res.Stderr)
		if stderr == "" {
			stderr = strings.TrimSpace(res.Stdout)
		}
		if stderr == "" {
			return fmt.Errorf("extract Ollama archive failed")
		}
		return fmt.Errorf("extract Ollama archive failed: %s", stderr)
	}
	if err := os.Remove(archivePath); err != nil {
		return err
	}
	info, err := os.Stat(cfg.BinaryPath)
	if err != nil {
		return fmt.Errorf("Ollama archive did not install expected binary: %w", err)
	}
	if info.Mode()&0111 == 0 {
		return fmt.Errorf("Ollama binary is not executable")
	}
	return nil
}

func ollamaReachable(ctx context.Context, port int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	url := fmt.Sprintf("http://127.0.0.1:%d/api/tags", port)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 500 {
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(200 * time.Millisecond):
		}
	}
	return false
}

func (s *HostOperationsService) startOllama(ctx context.Context, cfg OllamaConfig) error {
	if ollamaReachable(ctx, cfg.Port, 3*time.Second) {
		return nil
	}
	unit, err := RenderOllamaSystemdUnit(cfg)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", "opute-ollama.service")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0600); err != nil {
		return err
	}
	for _, command := range [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", "opute-ollama.service"}} {
		res, err := s.hostCommandRunnerContext(ctx, command, nil, 30*time.Second)
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			stderr := strings.TrimSpace(res.Stderr)
			if stderr == "" {
				stderr = strings.TrimSpace(res.Stdout)
			}
			if stderr == "" {
				return fmt.Errorf("Ollama systemd operation failed")
			}
			return fmt.Errorf("Ollama systemd operation failed: %s", stderr)
		}
	}
	return waitForOllamaReady(ctx, cfg.Port)
}

func waitForOllamaReady(ctx context.Context, port int) error {
	deadline := time.Now().Add(90 * time.Second)
	url := fmt.Sprintf("http://127.0.0.1:%d/api/tags", port)
	client := &http.Client{Timeout: 5 * time.Second}
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 500 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("Ollama did not become ready on 127.0.0.1:%d within 90s", port)
}
