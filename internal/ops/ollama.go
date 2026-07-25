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
	Supported              bool     `json:"supported"`
	SystemdUserAvailable   bool     `json:"systemdUserAvailable"`
	Architecture           string   `json:"architecture"`
	CPUCount               int      `json:"cpuCount"`
	ModelsDirectory        string   `json:"modelsDirectory"`
	MemoryBytes            uint64   `json:"memoryBytes,omitempty"`
	DiskAvailableBytes     uint64   `json:"diskAvailableBytes,omitempty"`
	GPU                    string   `json:"gpu,omitempty"`
	NvidiaSmiOk            bool     `json:"nvidiaSmiOk"`
	CudaLibraryPresent     bool     `json:"cudaLibraryPresent"`
	DxgDevicePresent       bool     `json:"dxgDevicePresent,omitempty"`
	OllamaBinaryPresent    bool     `json:"ollamaBinaryPresent"`
	OllamaServiceActive    bool     `json:"ollamaServiceActive"`
	RuntimeGpuAccelerated  bool     `json:"runtimeGpuAccelerated,omitempty"`
	RuntimeSizeVramBytes   int64    `json:"runtimeSizeVramBytes,omitempty"`
	ReadyForInstall        bool     `json:"readyForInstall"`
	ReadyForGpuInference   bool     `json:"readyForGpuInference"`
	Blockers               []string `json:"blockers,omitempty"`
	RemediationHints       []string `json:"remediationHints,omitempty"`
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
}

var ollamaModelRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)
var sha256Hex = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
var ollamaVersionRef = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

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
	if res, gpuErr := s.hostCommandRunner([]string{"nvidia-smi", "--query-gpu=name", "--format=csv,noheader"}, nil, 5*time.Second); gpuErr == nil && res.ExitCode == 0 {
		gpuName := strings.TrimSpace(res.Stdout)
		if gpuName != "" && !strings.Contains(strings.ToLower(gpuName), "failed") {
			result.GPU = gpuName
			result.NvidiaSmiOk = true
		}
	}
	result.CudaLibraryPresent = cudaUserLibraryPresent()
	if _, err := os.Stat("/dev/dxg"); err == nil {
		result.DxgDevicePresent = true
	}
	if result.OllamaServiceActive {
		if probe, probeErr := s.ProbeLocalLLM(context.Background(), false); probeErr == nil && probe != nil {
			result.RuntimeGpuAccelerated = probe.GpuAccelerated
			result.RuntimeSizeVramBytes = probe.SizeVramBytes
		}
	}
	finalizeLocalLLMPrerequisites(result)
	return result, nil
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
	result.ReadyForInstall = result.Supported && result.SystemdUserAvailable && (result.Architecture == "amd64" || result.Architecture == "arm64")
	result.ReadyForGpuInference = result.NvidiaSmiOk && result.CudaLibraryPresent

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
		result.Blockers = append(result.Blockers, "nvidia-smi did not report a usable NVIDIA GPU. GPU-accelerated inference is unavailable until a working NVIDIA driver exposes the discrete GPU to this environment.")
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
	if result.OllamaServiceActive && result.ReadyForGpuInference && !result.RuntimeGpuAccelerated {
		result.Blockers = append(result.Blockers, "Ollama is running but reports CPU-only inference (size_vram=0) despite GPU prerequisites looking healthy.")
		result.RemediationHints = append(result.RemediationHints,
			"Call start_local_llm_runtime (rewrites the Opute Ollama unit with CUDA pin + WSL library path) after the GPU driver is healthy, then probe_local_llm and confirm sizeVramBytes > 0.",
		)
	}
	if result.ReadyForInstall && !result.ReadyForGpuInference {
		result.RemediationHints = append(result.RemediationHints,
			"CPU-only Ollama install/start is still available via install_local_llm_model / start_local_llm_runtime; resolve GPU blockers above for dedicated-GPU acceleration.",
		)
	}
}

func (s *HostOperationsService) InstallLocalLLMModel(ctx context.Context, modelRef string) (*LocalLLMProbeResult, error) {
	if err := ValidateOllamaModelRef(modelRef); err != nil {
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
	reqBody, err := json.Marshal(map[string]any{"name": strings.TrimSpace(modelRef), "stream": false})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/pull", cfg.Port), strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 30 * time.Minute}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("pull Ollama model: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("pull Ollama model: status %d", response.StatusCode)
	}
	return s.ProbeLocalLLM(ctx, false)
}

func (s *HostOperationsService) StartLocalLLMRuntime(ctx context.Context) (*LocalLLMProbeResult, error) {
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
	return s.ProbeLocalLLM(ctx, false)
}

func (s *HostOperationsService) StopLocalLLMRuntime(ctx context.Context) error {
	_, err := s.hostCommandRunnerContext(ctx, []string{"systemctl", "--user", "stop", "opute-ollama.service"}, nil, 30*time.Second)
	return err
}

func (s *HostOperationsService) RemoveLocalLLMModel(ctx context.Context, modelRef string) error {
	if err := ValidateOllamaModelRef(modelRef); err != nil {
		return err
	}
	cfg, err := defaultOllamaConfig()
	if err != nil {
		return err
	}
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

func (s *HostOperationsService) ProbeLocalLLM(ctx context.Context, includeChat bool) (*LocalLLMProbeResult, error) {
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
	if includeChat && result.Ready && len(result.Models) > 0 {
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
						Name      string `json:"name"`
						SizeVRAM  int64  `json:"size_vram"`
						SizeVram  int64  `json:"sizeVram"`
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
						}
					}
					result.GpuAccelerated = result.SizeVramBytes > 0
				}
			}
		}
	}
	return result, nil
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
	res, err := s.hostCommandRunnerContext(ctx, []string{"tar", "--no-same-owner", "--use-compress-program=unzstd", "-xf", archivePath, "-C", rootDir}, nil, 5*time.Minute)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("extract Ollama archive failed")
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

func (s *HostOperationsService) startOllama(ctx context.Context, cfg OllamaConfig) error {
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
			return fmt.Errorf("Ollama systemd operation failed")
		}
	}
	return nil
}
