package host

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/fingerprint"
)

// TerminateWSLDistribution targets one named distribution. The caller's MCP
// capability is mutation/approval gated; this method additionally checks the
// locally tested capability before executing the exact argv.
func (s *Service) TerminateWSLDistribution(ctx context.Context, distro string, onData func(string)) (map[string]any, error) {
	distro = strings.TrimSpace(distro)
	if distro == "" {
		return nil, fmt.Errorf("distro is required")
	}
	if !fingerprint.DetectCapabilities().CanTerminateWSLDistribution {
		return nil, fmt.Errorf("wsl distribution termination is unavailable: canTerminateWslDistribution is false")
	}
	wslCommand, ok := fingerprint.WindowsInteropCommand("wsl.exe")
	if !ok {
		return nil, fmt.Errorf("wsl.exe is unavailable")
	}
	result, err := s.shared.HostCommandRunnerContext(ctx, []string{wslCommand, "--terminate", distro}, onData, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("wsl.exe --terminate failed with exit code %d", result.ExitCode)
	}
	return map[string]any{"distro": distro, "command": []string{"wsl.exe", "--terminate", distro}, "terminated": true}, nil
}

// ShutdownWSL targets the complete WSL2 environment. It is intentionally
// separate from distribution termination because it has a broader blast
// radius and is published as a destructive, approval-gated capability.
func (s *Service) ShutdownWSL(ctx context.Context, onData func(string)) (map[string]any, error) {
	if !fingerprint.DetectCapabilities().CanShutdownWSL {
		return nil, fmt.Errorf("wsl shutdown is unavailable: canShutdownWsl is false")
	}
	wslCommand, ok := fingerprint.WindowsInteropCommand("wsl.exe")
	if !ok {
		return nil, fmt.Errorf("wsl.exe is unavailable")
	}
	result, err := s.shared.HostCommandRunnerContext(ctx, []string{wslCommand, "--shutdown"}, onData, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("wsl.exe --shutdown failed with exit code %d", result.ExitCode)
	}
	return map[string]any{"command": []string{"wsl.exe", "--shutdown"}, "shutdown": true}, nil
}
