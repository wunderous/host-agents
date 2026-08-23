package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runDetachedTUI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	binary, err := findDetachedTUI()
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Stdin = os.Stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("detached TUI failed: %w", err)
	}
	return nil
}

func findDetachedTUI() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("OPUTE_HOST_AGENT_TUI_BIN")); configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("configured Host Agent TUI %q is unavailable: %w", configured, err)
		}
		return configured, nil
	}
	if binary, err := exec.LookPath("opute-host-agent-tui"); err == nil {
		return binary, nil
	}
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), "opute-host-agent-tui")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("opute-host-agent-tui is not installed; install the separate TUI client or set OPUTE_HOST_AGENT_TUI_BIN")
}

func waitForLocalEndpoint(ctx context.Context, endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("invalid local Host Agent endpoint %q", endpoint)
	}
	host := parsed.Hostname()
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	port := parsed.Port()
	if port == "" {
		return fmt.Errorf("local Host Agent endpoint %q has no port", endpoint)
	}
	address := net.JoinHostPort(host, port)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		connection, dialErr := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Host Agent endpoint %s did not become ready: %w", endpoint, dialErr)
		case <-time.After(50 * time.Millisecond):
		}
	}
}
