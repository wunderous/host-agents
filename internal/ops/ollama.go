package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultOllamaPort     = 11434
	defaultOllamaModel    = "qwen3.5:2b"
	ollamaServiceName     = "opute-ollama.service"
	ollamaNumParallel     = 1
	ollamaMaxLoadedModels = 2
)

var ollamaModelRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,199}$`)

type OllamaRuntimeConfig struct {
	Port            int    `json:"port"`
	ModelRef        string `json:"modelRef"`
	BinaryPath      string `json:"binaryPath"`
	ModelsDirectory string `json:"modelsDirectory,omitempty"`
}

type ProbeOllamaArgs struct {
	IncludeChat bool
	ModelRef    string
}

type InstallOllamaModelArgs struct {
	ModelRef string
	Port     *int
	// SetDefault controls which installed model a generic runtime start/probe
	// uses. Embedding models can be pulled without replacing the chat default.
	SetDefault bool
}

func ollamaRuntimeConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "opute", "ollama-runtime.json"), nil
}

func defaultOllamaRuntimeConfig() OllamaRuntimeConfig {
	port := defaultOllamaPort
	if raw := strings.TrimSpace(os.Getenv("OPUTE_OLLAMA_PORT")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed < 65536 {
			port = parsed
		}
	}
	binaryPath := strings.TrimSpace(os.Getenv("OPUTE_OLLAMA_BINARY"))
	if binaryPath == "" {
		if found, err := exec.LookPath("ollama"); err == nil {
			binaryPath = found
		}
	}
	return OllamaRuntimeConfig{
		Port:            port,
		ModelRef:        firstNonEmpty(strings.TrimSpace(os.Getenv("OPUTE_OLLAMA_MODEL")), defaultOllamaModel),
		BinaryPath:      binaryPath,
		ModelsDirectory: strings.TrimSpace(os.Getenv("OLLAMA_MODELS")),
	}
}

func loadOllamaRuntimeConfig() OllamaRuntimeConfig {
	cfg := defaultOllamaRuntimeConfig()
	path, err := ollamaRuntimeConfigPath()
	if err != nil {
		return cfg
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	var persisted OllamaRuntimeConfig
	if json.Unmarshal(data, &persisted) != nil {
		return cfg
	}
	if persisted.Port > 0 {
		cfg.Port = persisted.Port
	}
	if strings.TrimSpace(persisted.ModelRef) != "" {
		cfg.ModelRef = strings.TrimSpace(persisted.ModelRef)
	}
	if strings.TrimSpace(persisted.BinaryPath) != "" {
		cfg.BinaryPath = strings.TrimSpace(persisted.BinaryPath)
	}
	if strings.TrimSpace(persisted.ModelsDirectory) != "" {
		cfg.ModelsDirectory = strings.TrimSpace(persisted.ModelsDirectory)
	}
	return cfg
}

func saveOllamaRuntimeConfig(cfg OllamaRuntimeConfig) error {
	path, err := ollamaRuntimeConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

func renderOllamaSystemdUnit(cfg OllamaRuntimeConfig) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("Ollama host runtime requires Linux or WSL2")
	}
	if strings.TrimSpace(cfg.BinaryPath) == "" {
		return "", fmt.Errorf("ollama binary is not installed")
	}
	port := cfg.Port
	if port == 0 {
		port = defaultOllamaPort
	}
	lines := []string{
		"[Unit]",
		"Description=Opute shared Ollama runtime",
		"After=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=" + cfg.BinaryPath + " serve",
		"Environment=OLLAMA_HOST=127.0.0.1:" + strconv.Itoa(port),
		"Environment=OLLAMA_NUM_PARALLEL=" + strconv.Itoa(ollamaNumParallel),
		"Environment=OLLAMA_MAX_LOADED_MODELS=" + strconv.Itoa(ollamaMaxLoadedModels),
		"Environment=OLLAMA_KEEP_ALIVE=-1",
		"Restart=on-failure",
		"RestartSec=2",
		"",
		"[Install]",
		"WantedBy=default.target",
	}
	if strings.TrimSpace(cfg.ModelsDirectory) != "" {
		prefix := append(lines[:len(lines)-3], "Environment=OLLAMA_MODELS="+cfg.ModelsDirectory)
		lines = append(prefix, lines[len(lines)-3:]...)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func (s *HostOperationsService) ensureOllamaRuntime(ctx context.Context, cfg OllamaRuntimeConfig) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("Ollama host runtime requires Linux or WSL2")
	}
	if cfg.BinaryPath == "" {
		return fmt.Errorf("ollama binary is not installed")
	}
	unit, err := renderOllamaSystemdUnit(cfg)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", ollamaServiceName)
	old, readErr := os.ReadFile(unitPath)
	if readErr != nil || string(old) != unit {
		if err := os.MkdirAll(filepath.Dir(unitPath), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(unitPath, []byte(unit), 0600); err != nil {
			return err
		}
		if output, err := systemctlUser(ctx, "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("reload Ollama systemd unit: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	if output, err := systemctlUser(ctx, "enable", "--now", ollamaServiceName).CombinedOutput(); err != nil {
		return fmt.Errorf("start shared Ollama systemd unit: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return waitForOllamaAPI(ctx, cfg.Port)
}

func waitForOllamaAPI(ctx context.Context, port int) error {
	if port <= 0 {
		port = defaultOllamaPort
	}
	deadline := time.Now().Add(45 * time.Second)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/tags", port), nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode >= 200 && response.StatusCode < 300 {
					return nil
				}
				lastErr = fmt.Errorf("Ollama API returned HTTP %d", response.StatusCode)
			} else {
				lastErr = requestErr
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout waiting for Ollama API")
	}
	return fmt.Errorf("wait for shared Ollama API: %w", lastErr)
}

func (s *HostOperationsService) CheckOllamaPrerequisites() (*LocalLLMPrerequisitesResult, error) {
	cfg := loadOllamaRuntimeConfig()
	result := &LocalLLMPrerequisitesResult{
		Supported:             runtime.GOOS == "linux",
		SystemdUserAvailable:  false,
		Architecture:          runtime.GOARCH,
		CPUCount:              runtime.NumCPU(),
		ModelsDirectory:       cfg.ModelsDirectory,
		OllamaAPIBaseURL:      fmt.Sprintf("http://127.0.0.1:%d/v1", cfg.Port),
		OllamaModel:           cfg.ModelRef,
		OllamaNumParallel:     ollamaNumParallel,
		OllamaMaxLoadedModels: ollamaMaxLoadedModels,
	}
	if _, err := s.hostCommandRunner([]string{"systemctl", "--user", "show-environment"}, nil, 10*time.Second); err == nil {
		result.SystemdUserAvailable = true
	}
	if cfg.BinaryPath != "" {
		if _, err := os.Stat(cfg.BinaryPath); err == nil {
			result.OllamaBinaryPresent = true
		}
	}
	if res, err := s.hostCommandRunner([]string{"systemctl", "--user", "is-active", ollamaServiceName}, nil, 5*time.Second); err == nil {
		result.OllamaServiceActive = strings.TrimSpace(res.Stdout) == "active"
	}
	if result.OllamaServiceActive {
		if probe, err := s.ProbeOllama(context.Background(), ProbeOllamaArgs{ModelRef: cfg.ModelRef}); err == nil && probe != nil {
			result.RuntimeGpuAccelerated = probe.GpuAccelerated
			result.RuntimeSizeVramBytes = probe.SizeVramBytes
			result.RuntimeLoadedModel = probe.LoadedModel
		}
	}
	result.ReadyForInstall = result.Supported && result.SystemdUserAvailable && result.OllamaBinaryPresent
	result.ReadyForGpuInference = result.RuntimeGpuAccelerated
	if !result.Supported {
		result.Blockers = append(result.Blockers, "Ollama host runtime requires Linux or WSL2")
	}
	if !result.SystemdUserAvailable {
		result.Blockers = append(result.Blockers, "systemd --user is unavailable")
	}
	if !result.OllamaBinaryPresent {
		result.Blockers = append(result.Blockers, "ollama binary is not installed")
	}
	return result, nil
}

func (s *HostOperationsService) InstallOllamaModel(ctx context.Context, args InstallOllamaModelArgs) (*LocalLLMProbeResult, error) {
	cfg := loadOllamaRuntimeConfig()
	modelRef := strings.TrimSpace(args.ModelRef)
	if !ollamaModelRefPattern.MatchString(modelRef) {
		return nil, fmt.Errorf("invalid Ollama model reference")
	}
	if args.Port != nil && *args.Port > 0 {
		cfg.Port = *args.Port
	}
	if args.SetDefault {
		cfg.ModelRef = modelRef
	}
	if err := saveOllamaRuntimeConfig(cfg); err != nil {
		return nil, err
	}
	if err := s.ensureOllamaRuntime(ctx, cfg); err != nil {
		return nil, err
	}
	if cfg.BinaryPath == "" {
		return nil, fmt.Errorf("ollama binary is not installed")
	}
	if _, err := s.hostCommandRunnerContext(ctx, []string{cfg.BinaryPath, "pull", modelRef}, nil, 45*time.Minute); err != nil {
		return nil, fmt.Errorf("pull Ollama model %q: %w", modelRef, err)
	}
	probe, err := s.ProbeOllama(ctx, ProbeOllamaArgs{IncludeChat: true, ModelRef: modelRef})
	if err != nil {
		return nil, err
	}
	if !probe.Ready || !probe.OpenAIModelsReady {
		return nil, fmt.Errorf("Ollama model %q did not become ready: %s", modelRef, probe.LoadError)
	}
	return probe, nil
}

func (s *HostOperationsService) StartOllamaRuntime(ctx context.Context) (*LocalLLMProbeResult, error) {
	cfg := loadOllamaRuntimeConfig()
	if err := s.ensureOllamaRuntime(ctx, cfg); err != nil {
		return nil, err
	}
	return s.ProbeOllama(ctx, ProbeOllamaArgs{IncludeChat: true, ModelRef: cfg.ModelRef})
}

// StopOllamaRuntime intentionally does not stop the shared service. A local
// or public Platform instance must not take the host-wide runtime away from
// another instance that is using it.
func (s *HostOperationsService) StopOllamaRuntime(context.Context) error { return nil }

func (s *HostOperationsService) ProbeOllama(ctx context.Context, args ProbeOllamaArgs) (*LocalLLMProbeResult, error) {
	cfg := loadOllamaRuntimeConfig()
	if strings.TrimSpace(args.ModelRef) != "" {
		cfg.ModelRef = strings.TrimSpace(args.ModelRef)
	}
	root := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	result := &LocalLLMProbeResult{
		Runtime:         "ollama",
		APIBaseURL:      root + "/v1",
		ModelRef:        cfg.ModelRef,
		Models:          []LocalLLMModelResult{},
		MaxParallel:     ollamaNumParallel,
		MaxLoadedModels: ollamaMaxLoadedModels,
	}
	client := &http.Client{Timeout: 15 * time.Second}
	requestJSON := func(path string, destination any) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, root+path, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("Ollama %s returned HTTP %d", path, response.StatusCode)
		}
		return json.NewDecoder(response.Body).Decode(destination)
	}
	var tags struct {
		Models []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"models"`
	}
	if err := requestJSON("/api/tags", &tags); err != nil {
		result.LoadError = err.Error()
		return result, nil
	}
	for _, model := range tags.Models {
		result.Models = append(result.Models, LocalLLMModelResult{Name: model.Name, Digest: model.Digest, SizeBytes: model.Size})
	}
	installed := false
	for _, model := range result.Models {
		if ollamaModelNamesMatch(model.Name, cfg.ModelRef) {
			installed = true
			break
		}
	}
	var running struct {
		Models []struct {
			Name     string `json:"name"`
			SizeVRAM int64  `json:"size_vram"`
		} `json:"models"`
	}
	if err := requestJSON("/api/ps", &running); err == nil {
		for _, model := range running.Models {
			if ollamaModelNamesMatch(model.Name, cfg.ModelRef) {
				result.LoadedModel = model.Name
				result.SizeVramBytes = model.SizeVRAM
				result.GpuAccelerated = model.SizeVRAM > 0
				break
			}
		}
	}
	var openAI struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := requestJSON("/v1/models", &openAI); err == nil {
		result.OpenAIModelsReady = true
	}
	result.Ready = installed
	result.ChatReady = installed && result.OpenAIModelsReady
	if args.IncludeChat && !result.ChatReady {
		result.RemediationHints = append(result.RemediationHints, "pull the configured Ollama model and retry the readiness probe")
	}
	return result, nil
}

func ollamaModelNamesMatch(left, right string) bool {
	normalize := func(value string) string {
		value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(value, ":latest"), "@latest"))
		return strings.ToLower(value)
	}
	a, b := normalize(left), normalize(right)
	return a != "" && b != "" && (a == b || strings.HasSuffix(a, "/"+b) || strings.HasSuffix(b, "/"+a))
}
