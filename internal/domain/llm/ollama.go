package llm

import (
	"context"
	"crypto/sha256"
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

	"github.com/wunderous/host-agents/internal/textutil"
)

const (
	defaultOllamaPort     = 11434
	DefaultOllamaModel    = "hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M"
	defaultOllamaModel    = DefaultOllamaModel
	ollamaContextMinimum  = 512
	ollamaContextMaximum  = 32768
	ollamaServiceName     = "opute-ollama.service"
	ollamaNumParallel     = 1
	ollamaMaxLoadedModels = 2
)

var ollamaModelRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,199}$`)

type OllamaRuntimeConfig struct {
	Port            int                                 `json:"port"`
	ModelRef        string                              `json:"modelRef"`
	BinaryPath      string                              `json:"binaryPath"`
	ModelsDirectory string                              `json:"modelsDirectory,omitempty"`
	ContextSize     int                                 `json:"contextSize,omitempty"`
	ModelContexts   map[string]OllamaModelContextConfig `json:"modelContexts,omitempty"`
}

// OllamaModelContextConfig is the host-owned mapping from a caller's model
// reference to the Ollama model that carries its persistent context setting.
// The key is an arbitrary caller-supplied model reference; no model family is
// encoded in this contract.
type OllamaModelContextConfig struct {
	EffectiveModelRef string `json:"effectiveModelRef"`
	ContextSize       int    `json:"contextSize"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
}

// OllamaModelContextResult is returned by the generic model configuration
// capability and by probes for a selected model.
type OllamaModelContextResult struct {
	ModelRef          string `json:"modelRef"`
	EffectiveModelRef string `json:"effectiveModelRef"`
	ContextSize       int    `json:"contextSize,omitempty"`
	ContextSource     string `json:"contextSource,omitempty"`
	Persisted         bool   `json:"persisted"`
	Changed           bool   `json:"changed,omitempty"`
}

type ConfigureOllamaModelContextArgs struct {
	ModelRef    string
	ContextSize int
}

type ProbeOllamaArgs struct {
	IncludeChat bool
	ModelRef    string
}

type InstallOllamaModelArgs struct {
	ModelRef string
	Port     *int
	Role     string
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
		ModelRef:        textutil.FirstNonEmpty(strings.TrimSpace(os.Getenv("OPUTE_OLLAMA_MODEL")), defaultOllamaModel),
		BinaryPath:      binaryPath,
		ModelsDirectory: strings.TrimSpace(os.Getenv("OLLAMA_MODELS")),
		ContextSize:     ollamaContextSizeFromEnvironment(),
	}
}

func ollamaContextSizeFromEnvironment() int {
	for _, name := range []string{"OPUTE_OLLAMA_CONTEXT_SIZE", "OLLAMA_CONTEXT_LENGTH"} {
		if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed >= ollamaContextMinimum && parsed <= ollamaContextMaximum {
				return parsed
			}
		}
	}
	return ollamaContextMaximum
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
	if persisted.ContextSize >= ollamaContextMinimum && persisted.ContextSize <= ollamaContextMaximum {
		cfg.ContextSize = persisted.ContextSize
	}
	if len(persisted.ModelContexts) > 0 {
		cfg.ModelContexts = persisted.ModelContexts
	}
	if cfg.ContextSize <= 0 {
		cfg.ContextSize = readOllamaServiceContextSize()
	}
	return cfg
}

// readOllamaServiceContextSize is the fallback for a host that predates the
// per-model mapping. The running Ollama process remains the authority when a
// model is loaded; this only recovers the durable service default for an
// unloaded model.
func readOllamaServiceContextSize() int {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	base := filepath.Join(home, ".config", "systemd", "user")
	for _, name := range []string{"ollama.service", ollamaServiceName} {
		data, readErr := os.ReadFile(filepath.Join(base, name))
		if readErr != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "OLLAMA_CONTEXT_LENGTH=") {
				continue
			}
			value := strings.TrimSpace(strings.SplitN(line, "OLLAMA_CONTEXT_LENGTH=", 2)[1])
			value = strings.Trim(value, "\"'")
			if parsed, parseErr := strconv.Atoi(value); parseErr == nil && parsed >= ollamaContextMinimum && parsed <= ollamaContextMaximum {
				return parsed
			}
		}
	}
	return 0
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
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ollama-runtime-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func renderOllamaSystemdUnit(cfg OllamaRuntimeConfig) (string, error) {
	if cfg.ContextSize != 0 && (cfg.ContextSize < ollamaContextMinimum || cfg.ContextSize > ollamaContextMaximum) {
		return "", fmt.Errorf("contextSize must be between %d and %d tokens", ollamaContextMinimum, ollamaContextMaximum)
	}
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
		"StartLimitIntervalSec=60s",
		"StartLimitBurst=5",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=" + cfg.BinaryPath + " serve",
		"Environment=OLLAMA_HOST=127.0.0.1:" + strconv.Itoa(port),
		"Environment=OLLAMA_NUM_PARALLEL=" + strconv.Itoa(ollamaNumParallel),
		"Environment=OLLAMA_MAX_LOADED_MODELS=" + strconv.Itoa(ollamaMaxLoadedModels),
		"Environment=OLLAMA_KEEP_ALIVE=-1",
		"Slice=opute-workload.slice",
		"KillMode=control-group",
		"MemoryHigh=5G",
		"MemoryMax=6G",
		"MemorySwapMax=1G",
		"CPUQuota=600%",
		"CPUWeight=100",
		"TasksMax=4096",
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
	if cfg.ContextSize > 0 {
		insertAt := len(lines) - 3
		lines = append(lines[:insertAt], append([]string{"Environment=OLLAMA_CONTEXT_LENGTH=" + strconv.Itoa(cfg.ContextSize)}, lines[insertAt:]...)...)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func (s *Service) ensureOllamaRuntime(ctx context.Context, cfg OllamaRuntimeConfig) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("Ollama host runtime requires Linux or WSL2")
	}
	if cfg.BinaryPath == "" {
		return fmt.Errorf("ollama binary is not installed")
	}
	// A host may already have Ollama running under another user-service
	// manager (for example the provider bundle). Reusing a healthy API is
	// required: starting the generated unit in parallel would compete for the
	// same port and can put systemd into a restart loop.
	if err := probeOllamaAPI(ctx, cfg.Port); err == nil {
		return retireOllamaUnitRacedByExternalOwner(ctx)
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

// An external owner (for example the provider bundle's ollama.service) can
// serve the shared port while the generated unit is still enabled from an
// earlier install. The enabled-but-losing unit then races the owner for the
// bind at every boot and crash-loops. Disable it once the API is provably
// served elsewhere; re-enabling happens naturally on the next ensure that
// finds the port free.
func retireOllamaUnitRacedByExternalOwner(ctx context.Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", ollamaServiceName)
	if _, statErr := os.Stat(unitPath); statErr != nil {
		return nil
	}
	if _, err := runSystemctlUser(ctx, "is-active", "--quiet", ollamaServiceName); err == nil {
		return nil
	}
	if output, err := runSystemctlUser(ctx, "disable", "--now", ollamaServiceName); err != nil {
		return fmt.Errorf("retire raced Ollama unit %s: %w: %s", ollamaServiceName, err, output)
	}
	return nil
}

var runSystemctlUser = func(ctx context.Context, args ...string) (string, error) {
	output, err := systemctlUser(ctx, args...).CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func probeOllamaAPI(ctx context.Context, port int) error {
	if port <= 0 {
		port = defaultOllamaPort
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/tags", port), nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Ollama API returned HTTP %d", response.StatusCode)
	}
	return nil
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

func (s *Service) CheckOllamaPrerequisites() (*LocalLLMPrerequisitesResult, error) {
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
	if _, err := s.shared.HostCommandRunner([]string{"systemctl", "--user", "show-environment"}, nil, 10*time.Second); err == nil {
		result.SystemdUserAvailable = true
	}
	if cfg.BinaryPath != "" {
		if _, err := os.Stat(cfg.BinaryPath); err == nil {
			result.OllamaBinaryPresent = true
		}
	}
	if res, err := s.shared.HostCommandRunner([]string{"systemctl", "--user", "is-active", ollamaServiceName}, nil, 5*time.Second); err == nil {
		result.OllamaServiceActive = strings.TrimSpace(res.Stdout) == "active"
	}
	// The runtime may be healthy under a provider-owned unit rather than the
	// Host Agent-generated unit. The API is the authoritative readiness signal.
	if !result.OllamaServiceActive && probeOllamaAPI(context.Background(), cfg.Port) == nil {
		result.OllamaServiceActive = true
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

func (s *Service) InstallOllamaModel(ctx context.Context, args InstallOllamaModelArgs) (*LocalLLMProbeResult, error) {
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
	if _, err := s.shared.HostCommandRunnerContext(ctx, []string{cfg.BinaryPath, "pull", modelRef}, nil, 45*time.Minute); err != nil {
		return nil, fmt.Errorf("pull Ollama model %q: %w", modelRef, err)
	}
	effectiveModelRef := modelRef
	if configured, ok := cfg.ModelContexts[modelRef]; ok && configured.ContextSize >= ollamaContextMinimum && configured.ContextSize <= ollamaContextMaximum && strings.TrimSpace(configured.EffectiveModelRef) != "" {
		effectiveModelRef = strings.TrimSpace(configured.EffectiveModelRef)
	}
	if shouldWarmOllamaModel(args.Role) {
		if err := warmOllamaModel(ctx, cfg, effectiveModelRef); err != nil {
			return nil, fmt.Errorf("warm Ollama model %q: %w", effectiveModelRef, err)
		}
	}
	probe, err := s.ProbeOllama(ctx, ProbeOllamaArgs{IncludeChat: shouldWarmOllamaModel(args.Role), ModelRef: modelRef})
	if err != nil {
		return nil, err
	}
	if !probe.Ready || !probe.OpenAIModelsReady {
		return nil, fmt.Errorf("Ollama model %q did not become ready: %s", modelRef, probe.LoadError)
	}
	if shouldWarmOllamaModel(args.Role) && strings.TrimSpace(probe.LoadedModel) == "" {
		return nil, fmt.Errorf("Ollama model %q did not become resident after warm", effectiveModelRef)
	}
	return probe, nil
}

func shouldWarmOllamaModel(role string) bool {
	return strings.TrimSpace(role) != "embedding"
}

// warmOllamaModel asks Ollama to load the selected model without requiring a
// user turn. The host-wide keep-alive policy keeps it resident after this
// request, and the selected reference may be a host-managed context alias.
func warmOllamaModel(ctx context.Context, cfg OllamaRuntimeConfig, modelRef string) error {
	modelRef = strings.TrimSpace(modelRef)
	if modelRef == "" {
		return fmt.Errorf("model reference is empty")
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	root := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	return ollamaAPIRequest(ctx, client, root, http.MethodPost, "/api/generate", map[string]any{
		"model": modelRef,
		// An empty prompt can return without loading the model. A minimal
		// bounded generation makes residency observable through /api/ps.
		"prompt":     ".",
		"stream":     false,
		"keep_alive": -1,
		"options":    map[string]int{"num_predict": 1},
	}, nil)
}

func (s *Service) StartOllamaRuntime(ctx context.Context) (*LocalLLMProbeResult, error) {
	cfg := loadOllamaRuntimeConfig()
	if err := s.ensureOllamaRuntime(ctx, cfg); err != nil {
		return nil, err
	}
	return s.ProbeOllama(ctx, ProbeOllamaArgs{IncludeChat: true, ModelRef: cfg.ModelRef})
}

// StopOllamaRuntime intentionally does not stop the shared service. A local
// or public Platform instance must not take the host-wide runtime away from
// another instance that is using it.
func (s *Service) StopOllamaRuntime(context.Context) error { return nil }

func ollamaModelContextAlias(modelRef string, contextSize int) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(modelRef))))
	// Keep one stable managed definition per base model. Updating the
	// parameters in place makes the host-owned mapping observable to every
	// Opute instance through the same Ollama model reference; a context-sized
	// alias would leave older definitions indistinguishable to readers.
	_ = contextSize
	return fmt.Sprintf("opute/context-%x", digest[:8])
}

func parseOllamaContextSize(parameters string) int {
	fields := strings.Fields(parameters)
	for index := 0; index+1 < len(fields); index++ {
		if strings.TrimSpace(fields[index]) != "num_ctx" {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(fields[index+1]))
		if err == nil && value > 0 {
			return value
		}
	}
	return 0
}

type ollamaModelDetails struct {
	Parameters string `json:"parameters"`
}

func ollamaAPIRequest(ctx context.Context, client *http.Client, root string, method string, path string, body any, destination any) error {
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequestWithContext(ctx, method, root+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var message struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(response.Body).Decode(&message) == nil && strings.TrimSpace(message.Error) != "" {
			return fmt.Errorf("Ollama %s returned HTTP %d: %s", path, response.StatusCode, message.Error)
		}
		return fmt.Errorf("Ollama %s returned HTTP %d", path, response.StatusCode)
	}
	if destination == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(destination)
}

func (s *Service) readOllamaModelContext(ctx context.Context, cfg OllamaRuntimeConfig, modelRef string) (OllamaModelContextResult, error) {
	modelRef = strings.TrimSpace(modelRef)
	if !ollamaModelRefPattern.MatchString(modelRef) {
		return OllamaModelContextResult{}, fmt.Errorf("invalid Ollama model reference")
	}
	effectiveModelRef := modelRef
	persisted := false
	if configured, ok := cfg.ModelContexts[modelRef]; ok && strings.TrimSpace(configured.EffectiveModelRef) != "" {
		effectiveModelRef = configured.EffectiveModelRef
		persisted = true
	}
	client := &http.Client{Timeout: 15 * time.Second}
	root := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	var details ollamaModelDetails
	if err := ollamaAPIRequest(ctx, client, root, http.MethodPost, "/api/show", map[string]string{"model": effectiveModelRef}, &details); err != nil {
		return OllamaModelContextResult{}, err
	}
	contextSize := parseOllamaContextSize(details.Parameters)
	contextSource := ""
	if contextSize == 0 {
		var running struct {
			Models []struct {
				Name          string `json:"name"`
				ContextLength int    `json:"context_length"`
			} `json:"models"`
		}
		if err := ollamaAPIRequest(ctx, client, root, http.MethodGet, "/api/ps", nil, &running); err == nil {
			for _, model := range running.Models {
				if (ollamaModelNamesMatch(model.Name, effectiveModelRef) || ollamaModelNamesMatch(model.Name, modelRef)) && model.ContextLength > 0 {
					contextSize = model.ContextLength
					contextSource = "ollama-runtime"
					break
				}
			}
		}
	}
	if contextSize == 0 && cfg.ContextSize > 0 {
		contextSize = cfg.ContextSize
		contextSource = "ollama-service"
	}
	result := OllamaModelContextResult{
		ModelRef:          modelRef,
		EffectiveModelRef: effectiveModelRef,
		ContextSize:       contextSize,
		Persisted:         persisted,
	}
	if persisted {
		result.ContextSource = "managed-model"
	} else if contextSource != "" {
		result.ContextSource = contextSource
	} else if contextSize > 0 {
		result.ContextSource = "ollama-model"
	}
	return result, nil
}

// GetOllamaModelContext reads one model's persisted/effective context without
// probing readiness, residency, or the OpenAI-compatible surface.
func (s *Service) GetOllamaModelContext(ctx context.Context, modelRef string) (*OllamaModelContextResult, error) {
	cfg := loadOllamaRuntimeConfig()
	if strings.TrimSpace(modelRef) == "" {
		modelRef = cfg.ModelRef
	}
	result, err := s.readOllamaModelContext(ctx, cfg, modelRef)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// ConfigureOllamaModelContext persists a model-specific context setting in
// the host-owned runtime configuration. Ollama stores persistent parameters on
// a model definition, so the implementation creates a deterministic managed
// model reference and all callers can continue using the original reference.
func (s *Service) ConfigureOllamaModelContext(ctx context.Context, args ConfigureOllamaModelContextArgs) (*OllamaModelContextResult, error) {
	cfg := loadOllamaRuntimeConfig()
	modelRef := strings.TrimSpace(args.ModelRef)
	if !ollamaModelRefPattern.MatchString(modelRef) {
		return nil, fmt.Errorf("invalid Ollama model reference")
	}
	if args.ContextSize < ollamaContextMinimum || args.ContextSize > ollamaContextMaximum {
		return nil, fmt.Errorf("contextSize must be between %d and %d tokens", ollamaContextMinimum, ollamaContextMaximum)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	root := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	// Confirm the requested base model exists before writing host state.
	var baseDetails ollamaModelDetails
	if err := ollamaAPIRequest(ctx, client, root, http.MethodPost, "/api/show", map[string]string{"model": modelRef}, &baseDetails); err != nil {
		return nil, fmt.Errorf("inspect Ollama model %q: %w", modelRef, err)
	}

	effectiveModelRef := ollamaModelContextAlias(modelRef, args.ContextSize)
	if configured, ok := cfg.ModelContexts[modelRef]; ok && configured.ContextSize == args.ContextSize && configured.EffectiveModelRef == effectiveModelRef {
		if current, err := s.readOllamaModelContext(ctx, cfg, modelRef); err == nil && current.ContextSize == args.ContextSize {
			current.Changed = false
			return &current, nil
		}
	}

	var created struct {
		Status string `json:"status"`
	}
	if err := ollamaAPIRequest(ctx, client, root, http.MethodPost, "/api/create", map[string]any{
		"model": effectiveModelRef,
		"from":  modelRef,
		"parameters": map[string]int{
			"num_ctx": args.ContextSize,
		},
		"stream": false,
	}, &created); err != nil {
		return nil, fmt.Errorf("persist Ollama context for model %q: %w", modelRef, err)
	}
	if cfg.ModelContexts == nil {
		cfg.ModelContexts = make(map[string]OllamaModelContextConfig)
	}
	cfg.ModelContexts[modelRef] = OllamaModelContextConfig{
		EffectiveModelRef: effectiveModelRef,
		ContextSize:       args.ContextSize,
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	if err := saveOllamaRuntimeConfig(cfg); err != nil {
		return nil, fmt.Errorf("persist Ollama model context mapping: %w", err)
	}
	result := &OllamaModelContextResult{
		ModelRef:          modelRef,
		EffectiveModelRef: effectiveModelRef,
		ContextSize:       args.ContextSize,
		ContextSource:     "managed-model",
		Persisted:         true,
		Changed:           true,
	}
	return result, nil
}

func (s *Service) ProbeOllama(ctx context.Context, args ProbeOllamaArgs) (*LocalLLMProbeResult, error) {
	cfg := loadOllamaRuntimeConfig()
	if strings.TrimSpace(args.ModelRef) != "" {
		cfg.ModelRef = strings.TrimSpace(args.ModelRef)
	}
	root := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	result := &LocalLLMProbeResult{
		Runtime:           "ollama",
		APIBaseURL:        root + "/v1",
		ModelRef:          cfg.ModelRef,
		EffectiveModelRef: cfg.ModelRef,
		Models:            []LocalLLMModelResult{},
		MaxParallel:       ollamaNumParallel,
		MaxLoadedModels:   ollamaMaxLoadedModels,
	}
	contextResult, contextErr := s.readOllamaModelContext(ctx, cfg, cfg.ModelRef)
	if contextErr == nil {
		result.EffectiveModelRef = contextResult.EffectiveModelRef
		result.ContextLength = contextResult.ContextSize
		result.ContextSource = contextResult.ContextSource
		result.ContextPersisted = contextResult.Persisted
	}
	effectiveModelRef := result.EffectiveModelRef
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
		if ollamaModelNamesMatch(model.Name, cfg.ModelRef) || ollamaModelNamesMatch(model.Name, effectiveModelRef) {
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
			if ollamaModelNamesMatch(model.Name, cfg.ModelRef) || ollamaModelNamesMatch(model.Name, effectiveModelRef) {
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
	if contextErr != nil && result.LoadError == "" {
		result.LoadError = contextErr.Error()
	}
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
