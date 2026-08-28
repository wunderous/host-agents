package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/textutil"
)

// LlamaServerConfig is the host-owned, durable serving manifest. The host
// agent adopts verified GGUF artifacts; it does not silently download or
// convert model weights during inference setup.
type LlamaServerConfig struct {
	Port               int    `json:"port"`
	ModelRef           string `json:"modelRef"`
	ArtifactPath       string `json:"artifactPath"`
	ArtifactURI        string `json:"artifactUri,omitempty"`
	ArtifactSHA256     string `json:"artifactSha256"`
	BaseModel          string `json:"baseModel"`
	Revision           string `json:"revision"`
	TokenizerRevision  string `json:"tokenizerRevision"`
	ChatTemplateHash   string `json:"chatTemplateHash"`
	ChatTemplate       string `json:"chatTemplate"`
	ChatTemplateKwargs string `json:"chatTemplateKwargs"`
	Quantization       string `json:"quantization"`
	ContextSize        int    `json:"contextSize"`
	GpuLayers          int    `json:"gpuLayers"`
	BinaryPath         string `json:"binaryPath"`
	BinaryVersion      string `json:"binaryVersion"`
	BinarySHA256       string `json:"binarySha256"`
	BinarySource       string `json:"binarySource"`
	SourceURI          string `json:"sourceUri,omitempty"`
	SourceRevision     string `json:"sourceRevision"`
	SourceSHA256       string `json:"sourceSha256"`
	CudaEnabled        bool   `json:"cudaEnabled"`
	CudaArchitectures  string `json:"cudaArchitectures,omitempty"`
	RuntimeBuild       string `json:"runtimeBuild"`
}

type InstallLlamaServerModelArgs struct {
	ModelRef                string
	ArtifactPath            string
	ArtifactSHA256          string
	ArtifactURI             string
	BaseModel               string
	Revision                string
	TokenizerRevision       string
	ChatTemplateHash        string
	ChatTemplate            string
	ChatTemplateKwargs      string
	Quantization            string
	ContextSize             int
	GpuLayers               int
	BinaryPath              string
	BinaryVersion           string
	BinarySHA256            string
	BinarySource            string
	SourceRevision          string
	SourceSHA256            string
	CudaEnabled             bool
	CudaArchitectures       string
	BinaryBuildSourceURI    string
	BinaryBuildSourceSHA256 string
	BinaryBuildRevision     string
	BinaryURI               string
	Port                    *int
}

type ProbeLlamaServerArgs struct {
	IncludeChat bool
	ModelRef    string
}

const defaultLlamaServerPort = 8080
const defaultLlamaContext = 8192
const defaultLlamaGpuLayers = 999

// Empty means llama-server must use the checkpoint-provided Jinja template.
// Qwen3.5's template is not interchangeable with the older Qwen3 template.
const defaultLlamaTemplate = ""
const defaultLlamaTemplateKwargs = `{"enable_thinking":false}`

func llamaServerConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "opute", "llama-server.json"), nil
}

func defaultLlamaServerConfig() LlamaServerConfig {
	port := defaultLlamaServerPort
	if raw := strings.TrimSpace(os.Getenv("OPUTE_LLAMA_SERVER_PORT")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed < 65536 {
			port = parsed
		}
	}
	binaryPath := strings.TrimSpace(os.Getenv("OPUTE_LLAMA_SERVER_BINARY"))
	if binaryPath == "" {
		home, _ := os.UserHomeDir()
		binaryPath = filepath.Join(home, ".local", "share", "opute", "llama-server", "llama-server")
	}
	return LlamaServerConfig{
		Port:               port,
		ModelRef:           strings.TrimSpace(os.Getenv("OPUTE_LLAMA_SERVER_MODEL")),
		ArtifactPath:       strings.TrimSpace(os.Getenv("OPUTE_LLAMA_SERVER_ARTIFACT_PATH")),
		BaseModel:          strings.TrimSpace(os.Getenv("OPUTE_LLAMA_SERVER_BASE_MODEL")),
		ChatTemplate:       defaultLlamaTemplate,
		ChatTemplateKwargs: defaultLlamaTemplateKwargs,
		ContextSize:        defaultLlamaContext,
		GpuLayers:          defaultLlamaGpuLayers,
		BinaryPath:         binaryPath,
		BinaryVersion:      strings.TrimSpace(os.Getenv("OPUTE_LLAMA_SERVER_VERSION")),
		RuntimeBuild:       strings.TrimSpace(os.Getenv("OPUTE_LLAMA_SERVER_BUILD")),
	}
}

func loadLlamaServerConfig() LlamaServerConfig {
	cfg := defaultLlamaServerConfig()
	path, err := llamaServerConfigPath()
	if err != nil {
		return cfg
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	var persisted LlamaServerConfig
	if json.Unmarshal(data, &persisted) != nil {
		return cfg
	}
	if persisted.Port > 0 {
		cfg.Port = persisted.Port
	}
	if strings.TrimSpace(persisted.ModelRef) != "" {
		cfg.ModelRef = strings.TrimSpace(persisted.ModelRef)
	}
	if strings.TrimSpace(persisted.ArtifactPath) != "" {
		cfg.ArtifactPath = strings.TrimSpace(persisted.ArtifactPath)
	}
	if strings.TrimSpace(persisted.ArtifactURI) != "" {
		cfg.ArtifactURI = strings.TrimSpace(persisted.ArtifactURI)
	}
	if strings.TrimSpace(persisted.ArtifactSHA256) != "" {
		cfg.ArtifactSHA256 = strings.TrimSpace(persisted.ArtifactSHA256)
	}
	if strings.TrimSpace(persisted.BaseModel) != "" {
		cfg.BaseModel = strings.TrimSpace(persisted.BaseModel)
	}
	if strings.TrimSpace(persisted.Revision) != "" {
		cfg.Revision = strings.TrimSpace(persisted.Revision)
	}
	if strings.TrimSpace(persisted.TokenizerRevision) != "" {
		cfg.TokenizerRevision = strings.TrimSpace(persisted.TokenizerRevision)
	}
	if strings.TrimSpace(persisted.ChatTemplateHash) != "" {
		cfg.ChatTemplateHash = strings.TrimSpace(persisted.ChatTemplateHash)
	}
	if strings.TrimSpace(persisted.ChatTemplate) != "" {
		cfg.ChatTemplate = strings.TrimSpace(persisted.ChatTemplate)
	}
	if strings.TrimSpace(persisted.ChatTemplateKwargs) != "" {
		cfg.ChatTemplateKwargs = strings.TrimSpace(persisted.ChatTemplateKwargs)
	}
	if strings.TrimSpace(persisted.Quantization) != "" {
		cfg.Quantization = strings.TrimSpace(persisted.Quantization)
	}
	if persisted.ContextSize > 0 {
		cfg.ContextSize = persisted.ContextSize
	}
	if persisted.GpuLayers > 0 {
		cfg.GpuLayers = persisted.GpuLayers
	}
	if strings.TrimSpace(persisted.BinaryPath) != "" {
		cfg.BinaryPath = strings.TrimSpace(persisted.BinaryPath)
	}
	if strings.TrimSpace(persisted.BinaryVersion) != "" {
		cfg.BinaryVersion = strings.TrimSpace(persisted.BinaryVersion)
	}
	if strings.TrimSpace(persisted.BinarySHA256) != "" {
		cfg.BinarySHA256 = strings.TrimSpace(persisted.BinarySHA256)
	}
	if strings.TrimSpace(persisted.BinarySource) != "" {
		cfg.BinarySource = strings.TrimSpace(persisted.BinarySource)
	}
	if strings.TrimSpace(persisted.SourceRevision) != "" {
		cfg.SourceRevision = strings.TrimSpace(persisted.SourceRevision)
	}
	if strings.TrimSpace(persisted.SourceURI) != "" {
		cfg.SourceURI = strings.TrimSpace(persisted.SourceURI)
	}
	if strings.TrimSpace(persisted.SourceSHA256) != "" {
		cfg.SourceSHA256 = strings.TrimSpace(persisted.SourceSHA256)
	}
	if persisted.CudaEnabled {
		cfg.CudaEnabled = true
	}
	if strings.TrimSpace(persisted.CudaArchitectures) != "" {
		cfg.CudaArchitectures = strings.TrimSpace(persisted.CudaArchitectures)
	}
	if strings.TrimSpace(persisted.RuntimeBuild) != "" {
		cfg.RuntimeBuild = strings.TrimSpace(persisted.RuntimeBuild)
	}
	return cfg
}

func saveLlamaServerConfig(cfg LlamaServerConfig) error {
	path, err := llamaServerConfigPath()
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

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyLlamaArtifact(path, expected string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("llama-server GGUF artifact path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("llama-server GGUF artifact is unavailable: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("llama-server artifact must be a GGUF file: %s", path)
	}
	if strings.TrimSpace(expected) == "" {
		return fmt.Errorf("llama-server artifact SHA-256 is required")
	}
	actual, err := fileSHA256(path)
	if err != nil {
		return fmt.Errorf("hash llama-server artifact: %w", err)
	}
	if !strings.EqualFold(actual, strings.TrimSpace(expected)) {
		return fmt.Errorf("llama-server artifact checksum mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func downloadVerifiedLlamaFile(ctx context.Context, uri, destination, expected string, executable bool) error {
	uri = strings.TrimSpace(uri)
	if !strings.HasPrefix(uri, "https://") {
		return fmt.Errorf("llama-server artifact URI must use HTTPS")
	}
	if strings.TrimSpace(expected) == "" {
		return fmt.Errorf("SHA-256 is required when adopting a downloaded llama-server artifact")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 20 * time.Minute}).Do(request)
	if err != nil {
		return fmt.Errorf("download llama-server artifact: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download llama-server artifact: HTTP %d", response.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".llama-download-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temporary, hash), response.Body); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, strings.TrimSpace(expected)) {
		return fmt.Errorf("downloaded llama-server artifact checksum mismatch: expected %s, got %s", expected, actual)
	}
	if err := os.Chmod(temporaryPath, 0600); err != nil {
		return err
	}
	if executable {
		if err := os.Chmod(temporaryPath, 0700); err != nil {
			return err
		}
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	return nil
}

func validateLlamaServerConfig(cfg LlamaServerConfig) error {
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("invalid llama-server port %d", cfg.Port)
	}
	if strings.TrimSpace(cfg.ModelRef) == "" {
		return fmt.Errorf("llama-server modelRef is required")
	}
	for name, value := range map[string]string{
		"baseModel": cfg.BaseModel, "revision": cfg.Revision, "tokenizerRevision": cfg.TokenizerRevision,
		"chatTemplateHash": cfg.ChatTemplateHash, "binaryVersion": cfg.BinaryVersion, "binarySha256": cfg.BinarySHA256,
		"binarySource": cfg.BinarySource, "sourceRevision": cfg.SourceRevision, "sourceSha256": cfg.SourceSHA256,
		"chatTemplateKwargs": cfg.ChatTemplateKwargs,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("llama-server %s is required for a pinned serving manifest", name)
		}
	}
	if !isSHA256(cfg.ChatTemplateHash) || !isSHA256(cfg.BinarySHA256) {
		return fmt.Errorf("llama-server chat template and binary checksums must be SHA-256 hex digests")
	}
	var templateKwargs map[string]any
	if err := json.Unmarshal([]byte(cfg.ChatTemplateKwargs), &templateKwargs); err != nil {
		return fmt.Errorf("llama-server chatTemplateKwargs must be JSON: %w", err)
	}
	if thinking, ok := templateKwargs["enable_thinking"].(bool); !ok || thinking {
		return fmt.Errorf("llama-server Qwen3.5 serving requires chatTemplateKwargs.enable_thinking=false")
	}
	if err := verifyLlamaArtifact(cfg.ArtifactPath, cfg.ArtifactSHA256); err != nil {
		return err
	}
	if cfg.ContextSize < 512 {
		return fmt.Errorf("llama-server context size must be at least 512")
	}
	if cfg.GpuLayers != defaultLlamaGpuLayers {
		return fmt.Errorf("llama-server GPU-only execution requires --n-gpu-layers %d; got %d", defaultLlamaGpuLayers, cfg.GpuLayers)
	}
	if cfg.BinarySource != "host-build" {
		return fmt.Errorf("llama-server production binary must come from the host CUDA build")
	}
	if !cfg.CudaEnabled {
		return fmt.Errorf("llama-server binary is not verified as CUDA-enabled")
	}
	if cfg.Quantization != "Q4_K_M" {
		return fmt.Errorf("llama-server production quantization must be Q4_K_M (NF4 QLoRA-matched), got %q", cfg.Quantization)
	}
	if _, err := os.Stat(cfg.BinaryPath); err != nil {
		return fmt.Errorf("llama-server binary is unavailable: %w", err)
	}
	actual, err := fileSHA256(cfg.BinaryPath)
	if err != nil {
		return fmt.Errorf("hash llama-server binary: %w", err)
	}
	if !strings.EqualFold(actual, cfg.BinarySHA256) {
		return fmt.Errorf("llama-server binary checksum mismatch")
	}
	return nil
}

func isSHA256(value string) bool {
	if len(strings.TrimSpace(value)) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil
}

func llamaURL(cfg LlamaServerConfig, suffix string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", cfg.Port, suffix)
}

func llamaModelNameMatches(name, modelRef string) bool {
	normalize := func(value string) string {
		value = strings.TrimSuffix(strings.TrimSpace(value), ":latest")
		base := filepath.Base(value)
		base = strings.TrimSuffix(base, ".gguf")
		return strings.TrimSpace(base)
	}
	left, right := normalize(name), normalize(modelRef)
	return left != "" && right != "" && (left == right || strings.HasSuffix(left, "/"+right) || strings.HasSuffix(right, "/"+left))
}

func llamaReady(ctx context.Context, cfg LlamaServerConfig) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, llamaURL(cfg, "/v1/models"), nil)
	if err != nil {
		return false
	}
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return false
	}
	for _, model := range payload.Data {
		if llamaModelNameMatches(model.ID, cfg.ModelRef) {
			return true
		}
	}
	return false
}

func systemctlUser(ctx context.Context, args ...string) *exec.Cmd {
	commandArgs := append([]string{"--user"}, args...)
	command := exec.CommandContext(ctx, "systemctl", commandArgs...)
	if runtime.GOOS == "linux" {
		uid := strconv.Itoa(os.Getuid())
		command.Env = append(os.Environ(), "XDG_RUNTIME_DIR=/run/user/"+uid, "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/"+uid+"/bus")
	}
	return command
}

func renderLlamaSystemdUnit(cfg LlamaServerConfig) string {
	modelAliasArg := " --alias " + llamaSystemdQuote(cfg.ModelRef)
	templateArg := ""
	if strings.TrimSpace(cfg.ChatTemplate) != "" {
		templateArg = " --chat-template " + llamaSystemdQuote(cfg.ChatTemplate)
	}
	templateKwargsArg := " --chat-template-kwargs " + llamaSystemdQuote(cfg.ChatTemplateKwargs)
	return fmt.Sprintf(`[Unit]
Description=Opute llama-server runtime
After=network.target

[Service]
Type=simple
Environment=CUDA_VISIBLE_DEVICES=0
ExecStart=%s --model %s%s --host 127.0.0.1 --port %d --jinja%s%s --reasoning-budget 0 --ctx-size %d --n-gpu-layers %d --temp 0.1 --top-p 0.9 --seed 42
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
`, cfg.BinaryPath, cfg.ArtifactPath, modelAliasArg, cfg.Port, templateArg, templateKwargsArg, cfg.ContextSize, cfg.GpuLayers)
}

func llamaSystemdQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func (s *Service) startLlamaServerRuntime(ctx context.Context, cfg LlamaServerConfig) error {
	if err := validateLlamaServerConfig(cfg); err != nil {
		return err
	}
	// llama-server can resident only one generation model on this host. Stop
	// the managed service before changing its manifest so the previous model
	// is released from GPU memory before the next process starts. Tool
	// embeddings are served by a separate control-plane index and are not
	// affected by this generation-runtime transition.
	if err := s.StopLlamaServerRuntime(ctx); err != nil {
		return fmt.Errorf("unload current llama-server generation model: %w", err)
	}
	gpuCheck, gpuErr := s.shared.HostCommandRunnerContext(ctx, nvidiaSmiCommand(), nil, 10*time.Second)
	if gpuErr != nil || gpuCheck.ExitCode != 0 {
		if gpuErr != nil {
			return fmt.Errorf("llama-server GPU verification failed: %w", gpuErr)
		}
		return fmt.Errorf("llama-server GPU verification failed: %s", strings.TrimSpace(textutil.FirstNonEmpty(gpuCheck.Stderr, gpuCheck.Stdout)))
	}
	if err := saveLlamaServerConfig(cfg); err != nil {
		return err
	}
	releasedPort, err := s.releaseRecognizedLlamaPort(ctx, cfg)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", "opute-llama-server.service")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0700); err != nil {
		return err
	}
	unit := []byte(renderLlamaSystemdUnit(cfg))
	previous, readErr := os.ReadFile(unitPath)
	changed := readErr != nil || string(previous) != string(unit)
	if err := os.WriteFile(unitPath, unit, 0600); err != nil {
		return err
	}
	commands := [][]string{{"daemon-reload"}}
	if changed || releasedPort {
		commands = append(commands, []string{"enable", "opute-llama-server.service"}, []string{"restart", "opute-llama-server.service"})
	} else {
		commands = append(commands, []string{"enable", "--now", "opute-llama-server.service"})
	}
	for _, args := range commands {
		if output, err := systemctlUser(ctx, args...).CombinedOutput(); err != nil {
			return fmt.Errorf("llama-server systemd operation failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if llamaReady(ctx, cfg) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("llama-server did not become ready on 127.0.0.1:%d within 90s", cfg.Port)
}

// releaseRecognizedLlamaPort prevents a stale development runtime from
// masking a failed llama-server start. Only listeners that are themselves
// recognizable Opute local-LLM processes may be terminated; an unknown owner
// is a hard failure that requires explicit operator investigation.
func (s *Service) releaseRecognizedLlamaPort(ctx context.Context, cfg LlamaServerConfig) (bool, error) {
	command := fmt.Sprintf(`set -o pipefail
ss -ltnp '( sport = :%d )' 2>/dev/null | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' | sort -u | while read -r pid; do
	  [ -n "$pid" ] || continue
	  printf 'PID=%%s|' "$pid"
	  tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null || true
	  printf '\n'
done`, cfg.Port)
	result, err := s.shared.HostCommandRunnerContext(ctx, []string{"bash", "-lc", command}, nil, 10*time.Second)
	if err != nil {
		return false, fmt.Errorf("inspect llama-server port ownership: %w", err)
	}
	released := false
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			return false, fmt.Errorf("unable to identify llama-server port owner: %s", line)
		}
		pid := strings.TrimPrefix(parts[0], "PID=")
		cmdline := strings.TrimSpace(parts[1])
		managed := strings.Contains(cmdline, strings.TrimSpace(cfg.BinaryPath)) ||
			(strings.Contains(cmdline, "transformers serve") && strings.Contains(cmdline, "/home/opute/"))
		if !managed {
			return false, fmt.Errorf("llama-server port %d is occupied by an unowned process pid=%s cmd=%s", cfg.Port, pid, cmdline)
		}
		killCommand := fmt.Sprintf("kill -TERM %s 2>/dev/null || true", llamaShellQuote(pid))
		if _, killErr := s.shared.HostCommandRunnerContext(ctx, []string{"bash", "-lc", killCommand}, nil, 5*time.Second); killErr != nil {
			return false, fmt.Errorf("stop stale Opute local-LLM process pid=%s: %w", pid, killErr)
		}
		released = true
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return false, nil
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		check, checkErr := s.shared.HostCommandRunnerContext(ctx, []string{"bash", "-lc", fmt.Sprintf("ss -ltn '( sport = :%d )' 2>/dev/null | tail -n +2", cfg.Port)}, nil, 5*time.Second)
		if checkErr == nil && strings.TrimSpace(check.Stdout) == "" {
			return released, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return false, fmt.Errorf("stale Opute local-LLM process still owns port %d after termination request", cfg.Port)
}

func (s *Service) InstallLlamaServerModel(ctx context.Context, args InstallLlamaServerModelArgs) (*LocalLLMProbeResult, error) {
	cfg := loadLlamaServerConfig()
	cfg.ModelRef, cfg.ArtifactPath, cfg.ArtifactURI, cfg.ArtifactSHA256 = strings.TrimSpace(args.ModelRef), strings.TrimSpace(args.ArtifactPath), strings.TrimSpace(args.ArtifactURI), strings.TrimSpace(args.ArtifactSHA256)
	cfg.BaseModel, cfg.Revision, cfg.TokenizerRevision = strings.TrimSpace(args.BaseModel), strings.TrimSpace(args.Revision), strings.TrimSpace(args.TokenizerRevision)
	cfg.ChatTemplateHash, cfg.ChatTemplate, cfg.ChatTemplateKwargs, cfg.Quantization = strings.TrimSpace(args.ChatTemplateHash), strings.TrimSpace(args.ChatTemplate), strings.TrimSpace(args.ChatTemplateKwargs), strings.TrimSpace(args.Quantization)
	if cfg.ChatTemplateKwargs == "" {
		cfg.ChatTemplateKwargs = defaultLlamaTemplateKwargs
	}
	if cfg.ContextSize == 0 {
		cfg.ContextSize = defaultLlamaContext
	}
	if args.ContextSize > 0 {
		cfg.ContextSize = args.ContextSize
	}
	if args.GpuLayers != 0 {
		cfg.GpuLayers = args.GpuLayers
	}
	if args.Port != nil {
		cfg.Port = *args.Port
	}
	if strings.TrimSpace(args.BinaryPath) != "" {
		cfg.BinaryPath = strings.TrimSpace(args.BinaryPath)
	}
	if strings.TrimSpace(args.BinaryVersion) != "" {
		cfg.BinaryVersion = strings.TrimSpace(args.BinaryVersion)
	}
	if strings.TrimSpace(args.BinarySHA256) != "" {
		cfg.BinarySHA256 = strings.TrimSpace(args.BinarySHA256)
	}
	if strings.TrimSpace(args.BinarySource) != "" {
		cfg.BinarySource = strings.TrimSpace(args.BinarySource)
	}
	if strings.TrimSpace(args.BinaryBuildSourceURI) != "" {
		cfg.SourceURI = strings.TrimSpace(args.BinaryBuildSourceURI)
	}
	if strings.TrimSpace(args.BinaryBuildSourceSHA256) != "" {
		cfg.SourceSHA256 = strings.TrimSpace(args.BinaryBuildSourceSHA256)
	}
	if strings.TrimSpace(args.BinaryBuildRevision) != "" {
		cfg.SourceRevision = strings.TrimSpace(args.BinaryBuildRevision)
	}
	if strings.TrimSpace(args.SourceRevision) != "" {
		cfg.SourceRevision = strings.TrimSpace(args.SourceRevision)
	}
	if strings.TrimSpace(args.SourceSHA256) != "" {
		cfg.SourceSHA256 = strings.TrimSpace(args.SourceSHA256)
	}
	if args.CudaEnabled {
		cfg.CudaEnabled = true
	}
	if strings.TrimSpace(args.CudaArchitectures) != "" {
		cfg.CudaArchitectures = strings.TrimSpace(args.CudaArchitectures)
	}
	if strings.TrimSpace(args.BinaryURI) != "" {
		return nil, fmt.Errorf("binaryUri is not supported; build llama-server through ensure_local_llm_server_binary")
	}
	if cfg.BinarySource == "host-build" {
		binaryInfo, statErr := os.Stat(cfg.BinaryPath)
		if statErr != nil || binaryInfo.IsDir() {
			if cfg.SourceURI == "" || cfg.SourceSHA256 == "" || cfg.SourceRevision == "" {
				return nil, fmt.Errorf("host-built llama-server is missing source URI, checksum, or revision")
			}
			built, buildErr := s.EnsureLlamaServerBinary(ctx, BuildLlamaServerBinaryArgs{
				SourceURI: cfg.SourceURI, SourceSHA256: cfg.SourceSHA256, SourceRevision: cfg.SourceRevision,
				OutputPath: cfg.BinaryPath, CudaArchitectures: cfg.CudaArchitectures,
			}, nil)
			if buildErr != nil {
				return nil, buildErr
			}
			cfg.BinaryPath = built.BinaryPath
			cfg.BinaryVersion = built.BinaryVersion
			cfg.BinarySHA256 = built.BinarySHA256
			cfg.BinarySource = built.BinarySource
			cfg.SourceRevision = built.SourceRevision
			cfg.SourceSHA256 = built.SourceSHA256
			cfg.CudaEnabled = built.CudaEnabled
			cfg.CudaArchitectures = built.CudaArchitectures
			cfg.RuntimeBuild = built.RuntimeBuild
		}
	}
	if cfg.ArtifactPath == "" && strings.TrimSpace(args.ArtifactURI) != "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		cfg.ArtifactPath = filepath.Join(home, ".local", "share", "opute", "llama-models", cfg.ModelRef+".gguf")
	}
	if cfg.ArtifactPath == "" {
		return nil, fmt.Errorf("llama-server artifactPath or HTTPS artifactURI is required")
	}
	if _, err := os.Stat(cfg.ArtifactPath); os.IsNotExist(err) && strings.TrimSpace(args.ArtifactURI) != "" {
		if err := downloadVerifiedLlamaFile(ctx, args.ArtifactURI, cfg.ArtifactPath, cfg.ArtifactSHA256, false); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(cfg.BinaryPath); os.IsNotExist(err) && strings.TrimSpace(args.BinaryURI) != "" {
		if err := downloadVerifiedLlamaFile(ctx, args.BinaryURI, cfg.BinaryPath, cfg.BinarySHA256, true); err != nil {
			return nil, err
		}
	}
	// The binary may have been built by a previous host-agent operation and
	// therefore already exist before this install request. Re-derive the CUDA
	// capability from the managed executable instead of trusting a stale
	// persisted flag or caller-provided metadata.
	if cfg.BinarySource == "host-build" && !cfg.CudaEnabled {
		if _, statErr := os.Stat(cfg.BinaryPath); statErr != nil {
			return nil, fmt.Errorf("inspect managed llama-server binary: %w", statErr)
		}
		cudaEnabled, verifyErr := s.verifyLlamaServerCudaLinkage(ctx, cfg.BinaryPath)
		if verifyErr != nil {
			return nil, verifyErr
		}
		if !cudaEnabled {
			return nil, fmt.Errorf("managed llama-server binary is not verified as CUDA-enabled")
		}
		cfg.CudaEnabled = true
	}
	if err := s.startLlamaServerRuntime(ctx, cfg); err != nil {
		return nil, err
	}
	result, probeErr := s.ProbeLlamaServer(ctx, ProbeLlamaServerArgs{IncludeChat: true, ModelRef: cfg.ModelRef})
	if probeErr != nil {
		return nil, probeErr
	}
	if result == nil || !result.ChatReady || !result.GpuAccelerated || result.SizeVramBytes <= 0 {
		return result, fmt.Errorf("llama-server must be GPU-resident with nonzero VRAM; CPU fallback is rejected")
	}
	return result, nil
}

func (s *Service) StartLlamaServerRuntime(ctx context.Context) (*LocalLLMProbeResult, error) {
	cfg := loadLlamaServerConfig()
	if err := s.startLlamaServerRuntime(ctx, cfg); err != nil {
		return nil, err
	}
	result, probeErr := s.ProbeLlamaServer(ctx, ProbeLlamaServerArgs{IncludeChat: true, ModelRef: cfg.ModelRef})
	if probeErr != nil {
		return nil, probeErr
	}
	if result == nil || !result.ChatReady || !result.GpuAccelerated || result.SizeVramBytes <= 0 {
		return result, fmt.Errorf("llama-server must be GPU-resident with nonzero VRAM; CPU fallback is rejected")
	}
	return result, nil
}

func (s *Service) StopLlamaServerRuntime(ctx context.Context) error {
	output, err := systemctlUser(ctx, "disable", "--now", "opute-llama-server.service").CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		lowerMessage := strings.ToLower(message)
		if strings.Contains(lowerMessage, "not loaded") || strings.Contains(lowerMessage, "not found") || strings.Contains(lowerMessage, "does not exist") {
			return nil
		}
		return fmt.Errorf("stop llama-server systemd unit: %w: %s", err, message)
	}
	return nil
}

func (s *Service) ProbeLlamaServer(ctx context.Context, args ProbeLlamaServerArgs) (*LocalLLMProbeResult, error) {
	cfg := loadLlamaServerConfig()
	if strings.TrimSpace(args.ModelRef) != "" {
		cfg.ModelRef = strings.TrimSpace(args.ModelRef)
	}
	result := &LocalLLMProbeResult{
		APIBaseURL:         fmt.Sprintf("http://127.0.0.1:%d/v1", cfg.Port),
		ModelRef:           cfg.ModelRef,
		ArtifactURI:        cfg.ArtifactURI,
		ArtifactSHA256:     cfg.ArtifactSHA256,
		BaseModel:          cfg.BaseModel,
		Revision:           cfg.Revision,
		TokenizerRevision:  cfg.TokenizerRevision,
		ChatTemplateHash:   cfg.ChatTemplateHash,
		ChatTemplateKwargs: cfg.ChatTemplateKwargs,
		Quantization:       cfg.Quantization,
		GpuLayers:          cfg.GpuLayers,
		CudaArchitectures:  cfg.CudaArchitectures,
		RuntimeBuild:       cfg.RuntimeBuild,
		Version:            cfg.BinaryVersion,
		CudaEnabled:        cfg.CudaEnabled,
		BinarySource:       cfg.BinarySource,
		BinarySHA256:       cfg.BinarySHA256,
		SourceRevision:     cfg.SourceRevision,
		ContextLength:      cfg.ContextSize,
		Models:             []LocalLLMModelResult{},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, llamaURL(cfg, "/v1/models"), nil)
	if err != nil {
		return nil, err
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		result.LoadError = err.Error()
		return result, nil
	}
	defer response.Body.Close()
	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		result.LoadError = err.Error()
		return result, nil
	}
	for _, model := range payload.Data {
		result.Models = append(result.Models, LocalLLMModelResult{Name: model.ID})
	}
	result.Ready, result.OpenAIModelsReady = response.StatusCode >= 200 && response.StatusCode < 300, response.StatusCode >= 200 && response.StatusCode < 300
	for _, model := range payload.Data {
		if cfg.ModelRef == "" || llamaModelNameMatches(model.ID, cfg.ModelRef) {
			result.LoadedModel = model.ID
			break
		}
	}
	if result.Ready && result.LoadedModel != "" {
		if output, gpuErr := exec.CommandContext(ctx, nvidiaSmiCommand()[0], "--query-gpu=memory.used", "--format=csv,noheader,nounits").Output(); gpuErr == nil {
			if value, parseErr := strconv.ParseInt(strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0]), 10, 64); parseErr == nil {
				result.SizeVramBytes = value * 1024 * 1024
				result.GpuAccelerated = result.SizeVramBytes > 0
			}
		}
	}
	result.ChatReady = result.Ready && result.LoadedModel != "" && result.GpuAccelerated && result.SizeVramBytes > 0
	return result, nil
}
