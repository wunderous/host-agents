package ops

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	litertLMDefaultPort    = 11436
	litertLMServiceName    = "opute-litert-lm.service"
	litertLMHFRepoDefault  = "litert-community/gemma-4-E2B-it-litert-lm"
	litertLMModelFile      = "gemma-4-E4B-it.litertlm"
)

type InstallLiteRTLMModelArgs struct {
	ModelRef       string
	HuggingFaceRepo string
	Port           int
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

// RenderLiteRTLMSystemdUnit returns a user-systemd unit that serves OpenAI-compatible HTTP.
func RenderLiteRTLMSystemdUnit(port int, huggingFaceRepo string) string {
	repo := strings.TrimSpace(huggingFaceRepo)
	if repo == "" {
		repo = litertLMHFRepoDefault
	}
	listenPort := litertLMPort(port)
	modelsDir := litertLMModelsDir()
	return fmt.Sprintf(`[Unit]
Description=Opute LiteRT-LM OpenAI server
After=network.target

[Service]
Type=simple
Environment=HOME=%s
WorkingDirectory=%s
ExecStart=/usr/bin/env litert-lm openai_server --from-huggingface-repo=%s %s --host=127.0.0.1 --port=%d --backend=gpu
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, os.Getenv("HOME"), modelsDir, repo, litertLMModelFile, listenPort)
}

func (s *HostOperationsService) InstallLiteRTLMModel(ctx context.Context, args InstallLiteRTLMModelArgs) (*LocalLLMProbeResult, error) {
	port := litertLMPort(args.Port)
	repo := strings.TrimSpace(args.HuggingFaceRepo)
	if repo == "" {
		repo = litertLMHFRepoDefault
	}
	if err := os.MkdirAll(litertLMModelsDir(), 0o755); err != nil {
		return nil, fmt.Errorf("prepare litert-lm models dir: %w", err)
	}
	if err := s.startLiteRTLM(ctx, port, repo); err != nil {
		return nil, err
	}
	return s.ProbeLiteRTLM(ctx, ProbeLiteRTLMArgs{Port: port, IncludeChat: false, ModelRef: strings.TrimSpace(args.ModelRef)})
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
	if args.IncludeChat && resp.StatusCode == http.StatusOK && strings.TrimSpace(args.ModelRef) != "" {
		result.ChatReady = true
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
	unit := RenderLiteRTLMSystemdUnit(port, huggingFaceRepo)
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
