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
	"strings"
	"time"
)

const (
	litertLMDefaultPort    = 11436
	litertLMServiceName    = "opute-litert-lm.service"
	litertLMHFRepoDefault  = "litert-community/gemma-4-E2B-it-litert-lm"
	litertLMModelFile      = "gemma-4-E2B-it.litertlm"
	litertLMModelIDDefault = "gemma4-e2b"
	litertLMVersion        = "0.14.0"
)

type InstallLiteRTLMModelArgs struct {
	ModelRef        string
	HuggingFaceRepo string
	Port            int
}

func litertLMModelsDir() string {
	return filepath.Join(os.Getenv("HOME"), ".local", "share", "opute", "litert-lm")
}

func litertLMPort(port int) int {
	if port > 0 {
		return port
	}
	return litertLMDefaultPort
}

var litertLMModelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func validateLiteRTLMInstallArgs(args InstallLiteRTLMModelArgs) (string, string, error) {
	modelID := strings.TrimSpace(args.ModelRef)
	if modelID == "" {
		modelID = litertLMModelIDDefault
	}
	if !litertLMModelID.MatchString(modelID) {
		return "", "", fmt.Errorf("invalid LiteRT-LM model id")
	}
	// The Host Agent deliberately allowlists the exact artifact requested by
	// Opute. This prevents a caller from turning the managed operation into an
	// arbitrary remote download or an arbitrary command argument.
	repo := strings.TrimSpace(args.HuggingFaceRepo)
	if repo == "" {
		repo = litertLMHFRepoDefault
	}
	if repo != litertLMHFRepoDefault || modelID != litertLMModelIDDefault {
		return "", "", fmt.Errorf("only %s/%s is supported by the LiteRT-LM host operation", litertLMHFRepoDefault, litertLMModelIDDefault)
	}
	return modelID, repo, nil
}

func liteRTLMExecutable() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(home, ".local", "bin", "litert-lm"),
	}
	if path, lookErr := exec.LookPath("litert-lm"); lookErr == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("litert-lm is not installed; install litert-lm %s through the Host Agent before retrying", litertLMVersion)
}

func (s *HostOperationsService) ensureLiteRTLM(ctx context.Context) (string, error) {
	if binary, err := liteRTLMExecutable(); err == nil {
		return binary, nil
	}
	// Installation is deliberately performed by the Host Agent, with a fixed
	// package and version. Callers never send a shell command or package name.
	install, err := s.hostCommandRunnerContext(ctx, []string{
		"python3", "-m", "pip", "install", "--user", "--break-system-packages",
		"litert-lm==" + litertLMVersion,
	}, nil, 15*time.Minute)
	if err != nil {
		return "", err
	}
	if install.ExitCode != 0 {
		return "", fmt.Errorf("install LiteRT-LM failed: %s", firstNonEmpty(install.Stderr, install.Stdout))
	}
	return liteRTLMExecutable()
}

// RenderLiteRTLMSystemdUnit returns a user-systemd unit that serves OpenAI-compatible HTTP.
func RenderLiteRTLMSystemdUnit(port int, huggingFaceRepo string) string {
	listenPort := litertLMPort(port)
	modelsDir := litertLMModelsDir()
	binary := filepath.Join(os.Getenv("HOME"), ".local", "bin", "litert-lm")
	return fmt.Sprintf(`[Unit]
Description=Opute LiteRT-LM OpenAI server
After=network.target

[Service]
Type=simple
Environment=HOME=%s
WorkingDirectory=%s
ExecStart=%s serve --host 127.0.0.1 --port %d
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, os.Getenv("HOME"), modelsDir, binary, listenPort)
}

func (s *HostOperationsService) InstallLiteRTLMModel(ctx context.Context, args InstallLiteRTLMModelArgs) (*LocalLLMProbeResult, error) {
	port := litertLMPort(args.Port)
	modelID, repo, err := validateLiteRTLMInstallArgs(args)
	if err != nil {
		return nil, err
	}
	if _, err := s.ensureLiteRTLM(ctx); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(litertLMModelsDir(), 0o755); err != nil {
		return nil, fmt.Errorf("prepare litert-lm models dir: %w", err)
	}
	if err := s.importLiteRTLMModel(ctx, modelID, repo); err != nil {
		return nil, err
	}
	if err := s.startLiteRTLM(ctx, port, repo); err != nil {
		return nil, err
	}
	return s.ProbeLiteRTLM(ctx, ProbeLiteRTLMArgs{Port: port, IncludeChat: false, ModelRef: modelID})
}

type ProbeLiteRTLMArgs struct {
	Port        int
	IncludeChat bool
	ModelRef    string
}

func (s *HostOperationsService) ProbeLiteRTLM(ctx context.Context, args ProbeLiteRTLMArgs) (*LocalLLMProbeResult, error) {
	port := litertLMPort(args.Port)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d/v1", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &LocalLLMProbeResult{
			APIBaseURL:        baseURL,
			Ready:             false,
			OpenAIModelsReady: false,
		}, nil
	}
	defer resp.Body.Close()
	result := &LocalLLMProbeResult{
		APIBaseURL:        baseURL,
		Ready:             resp.StatusCode == http.StatusOK,
		OpenAIModelsReady: resp.StatusCode == http.StatusOK,
	}
	if resp.StatusCode == http.StatusOK {
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if jsonErr := json.NewDecoder(resp.Body).Decode(&payload); jsonErr == nil {
			for _, model := range payload.Data {
				result.Models = append(result.Models, LocalLLMModelResult{Name: model.ID})
				if strings.TrimSpace(args.ModelRef) != "" && model.ID == strings.TrimSpace(args.ModelRef) {
					result.ChatReady = true
				}
			}
			if strings.TrimSpace(args.ModelRef) == "" {
				result.ChatReady = len(result.Models) > 0
			}
		}
	}
	return result, nil
}

func (s *HostOperationsService) StartLiteRTLMRuntime(ctx context.Context) (*LocalLLMProbeResult, error) {
	if err := s.startLiteRTLM(ctx, litertLMDefaultPort, litertLMHFRepoDefault); err != nil {
		return nil, err
	}
	return s.ProbeLiteRTLM(ctx, ProbeLiteRTLMArgs{Port: litertLMDefaultPort, IncludeChat: false})
}

func (s *HostOperationsService) startLiteRTLM(ctx context.Context, port int, huggingFaceRepo string) error {
	if strings.TrimSpace(huggingFaceRepo) != "" && strings.TrimSpace(huggingFaceRepo) != litertLMHFRepoDefault {
		return fmt.Errorf("unsupported LiteRT-LM repository")
	}
	if _, err := s.ensureLiteRTLM(ctx); err != nil {
		return err
	}
	unit := RenderLiteRTLMSystemdUnit(port, litertLMHFRepoDefault)
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", litertLMServiceName)
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o600); err != nil {
		return err
	}
	for _, command := range [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "--now", litertLMServiceName},
	} {
		res, err := s.hostCommandRunnerContext(ctx, command, nil, 30*time.Second)
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("LiteRT-LM systemd operation failed")
		}
	}
	return nil
}

func (s *HostOperationsService) importLiteRTLMModel(ctx context.Context, modelID, repo string) error {
	binary, err := liteRTLMExecutable()
	if err != nil {
		return err
	}
	listed, err := s.hostCommandRunnerContext(ctx, []string{binary, "list"}, nil, 30*time.Second)
	if err == nil && listed.ExitCode == 0 {
		for _, line := range strings.Split(listed.Stdout, "\n") {
			if fields := strings.Fields(line); len(fields) > 0 && fields[0] == modelID {
				return nil
			}
		}
	}
	imported, err := s.hostCommandRunnerContext(ctx, []string{
		binary,
		"import",
		"--from-huggingface-repo",
		repo,
		litertLMModelFile,
		modelID,
	}, nil, 30*time.Minute)
	if err != nil {
		return err
	}
	if imported.ExitCode != 0 {
		return fmt.Errorf("LiteRT-LM model import failed: %s", firstNonEmpty(imported.Stderr, imported.Stdout))
	}
	return nil
}
