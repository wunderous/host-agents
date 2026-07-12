package ops

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultPlatformOputeRepoRoot = "/mnt/c/Users/houma/code/opute"

type EnsurePlatformOputeStackArgs struct {
	RepoRoot string `json:"repoRoot,omitempty"`
}

type EnsurePlatformOputeStackResult struct {
	RepoRoot       string `json:"repoRoot"`
	StackReady     bool   `json:"stackReady"`
	ForwardReady   bool   `json:"forwardReady"`
	WebHealthURL   string `json:"webHealthUrl"`
	McpHealthURL   string `json:"mcpHealthUrl"`
	SkippedStackUp bool   `json:"skippedStackUp,omitempty"`
}

type ProvisionPlatformOputeTunnelArgs struct {
	RepoRoot string `json:"repoRoot,omitempty"`
}

type ProvisionPlatformOputeTunnelResult struct {
	RepoRoot   string `json:"repoRoot"`
	RunToken   string `json:"runToken"`
	TunnelName string `json:"tunnelName"`
}

func resolvePlatformOputeRepoRoot(explicit string) string {
	if trimmed := strings.TrimSpace(explicit); trimmed != "" {
		return trimmed
	}
	if fromEnv := strings.TrimSpace(os.Getenv("OPUTE_REPO_ROOT")); fromEnv != "" {
		return fromEnv
	}
	return defaultPlatformOputeRepoRoot
}

func platformOputeShellPrefix(repoRoot string) string {
	return fmt.Sprintf(
		`export PATH="$HOME/.bun/bin:/usr/local/bin:/usr/bin:/bin:$PATH"; cd %q`,
		repoRoot,
	)
}

func wslRepoToWindowsPath(repoRoot string) string {
	trimmed := strings.TrimPrefix(repoRoot, "/mnt/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		return repoRoot
	}
	drive := strings.ToUpper(parts[0])
	rest := strings.Join(parts[1:], `\`)
	return drive + `:\` + rest
}

func (s *HostOperationsService) EnsurePlatformOputeStack(args EnsurePlatformOputeStackArgs) (*EnsurePlatformOputeStackResult, error) {
	repoRoot := resolvePlatformOputeRepoRoot(args.RepoRoot)
	prefix := platformOputeShellPrefix(repoRoot)

	webHealthURL := "http://127.0.0.1:9190/"
	mcpHealthURL := "http://127.0.0.1:9191/health"
	stackAlreadyHealthy := probeHTTPStatus(webHealthURL) && probeHTTPStatus(mcpHealthURL)

	commands := []string{
		fmt.Sprintf(`mkdir -p %q/tmp/platform-opute-io`, repoRoot),
		fmt.Sprintf(
			`grep -q OPUTE_PLATFORM_OPUTE_SKIP_HOST_AGENT=1 %q/tmp/platform-opute-io/public-urls.env 2>/dev/null || echo 'OPUTE_PLATFORM_OPUTE_SKIP_HOST_AGENT=1' >> %q/tmp/platform-opute-io/public-urls.env`,
			repoRoot,
			repoRoot,
		),
	}
	if !stackAlreadyHealthy {
		commands = append(commands,
			fmt.Sprintf(`%s && export OPUTE_PLATFORM_OPUTE_SKIP_HOST_AGENT=1 && bun run platform-opute:stack:up`, prefix),
		)
	}
	ps1 := wslRepoToWindowsPath(repoRoot) + `\scripts\ensure-wsl-windows-localhost-forward.ps1`
	commands = append(commands, fmt.Sprintf(
		`powershell.exe -NoProfile -ExecutionPolicy Bypass -File %q -Ports 9190,9191,9193,9194 || true`,
		ps1,
	))
	for _, command := range commands {
		result, err := s.RunAgentShell(command, nil)
		if err != nil {
			return nil, err
		}
		if result.ExitCode != 0 && !strings.Contains(command, "ensure-wsl-windows-localhost-forward.ps1") {
			stderr := strings.TrimSpace(result.Stderr)
			if stderr == "" {
				stderr = strings.TrimSpace(result.Stdout)
			}
			return nil, fmt.Errorf("platform-opute stack step failed (exit %d): %s", result.ExitCode, stderr)
		}
	}

	stackReady := probeHTTPStatus(webHealthURL)
	forwardReady := probeHTTPStatus(mcpHealthURL)
	if !stackReady || !forwardReady {
		return nil, fmt.Errorf("platform-opute stack not healthy (web=%v mcp=%v)", stackReady, forwardReady)
	}

	return &EnsurePlatformOputeStackResult{
		RepoRoot:       repoRoot,
		StackReady:     stackReady,
		ForwardReady:   forwardReady,
		WebHealthURL:   webHealthURL,
		McpHealthURL:   mcpHealthURL,
		SkippedStackUp: stackAlreadyHealthy,
	}, nil
}

func (s *HostOperationsService) ProvisionPlatformOputeTunnel(args ProvisionPlatformOputeTunnelArgs) (*ProvisionPlatformOputeTunnelResult, error) {
	repoRoot := resolvePlatformOputeRepoRoot(args.RepoRoot)
	prefix := platformOputeShellPrefix(repoRoot)
	command := fmt.Sprintf(`%s && bun scripts/provision-platform-opute-tunnel.ts`, prefix)
	result, err := s.RunAgentShell(command, nil)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		stderr := strings.TrimSpace(result.Stderr)
		if stderr == "" {
			stderr = strings.TrimSpace(result.Stdout)
		}
		return nil, fmt.Errorf("provision platform-opute tunnel failed (exit %d): %s", result.ExitCode, stderr)
	}

	tokenPath := fmt.Sprintf("%s/tmp/platform-opute-tunnel-token.txt", repoRoot)
	tokenResult, err := s.RunAgentShell(fmt.Sprintf(`cat %q`, tokenPath), nil)
	if err != nil {
		return nil, err
	}
	if tokenResult.ExitCode != 0 {
		return nil, fmt.Errorf("read platform-opute tunnel token: %s", strings.TrimSpace(tokenResult.Stderr))
	}
	runToken := strings.TrimSpace(tokenResult.Stdout)
	if runToken == "" {
		return nil, fmt.Errorf("platform-opute tunnel token missing at %s", tokenPath)
	}

	return &ProvisionPlatformOputeTunnelResult{
		RepoRoot:   repoRoot,
		RunToken:   runToken,
		TunnelName: "opute-platform-opute-io",
	}, nil
}

func probeHTTPStatus(url string) bool {
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Get(url)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode >= 200 && res.StatusCode < 500
}
