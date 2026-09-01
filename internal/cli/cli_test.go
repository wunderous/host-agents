package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunServerLoadsModeFromEnvFileBeforeApplyingDefaults(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "")
	t.Setenv("OPUTE_TRANSPORT", "")
	t.Setenv("OPUTE_HOST_AGENT_ENV_FILE", "")
	t.Setenv("OPUTE_INFRA_PROVIDER_ID", "")
	envPath := filepath.Join(t.TempDir(), "agent.env")
	if err := os.WriteFile(envPath, []byte("OPUTE_AGENT_MODE=platform\nOPUTE_INFRA_PROVIDER_ID=incus\nOPUTE_REMOTE_AGENT_ID=test-host-agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"serve", "--check", "--env-file", envPath}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("serve --check with env file: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "configuration ok" {
		t.Fatalf("stdout = %q, want configuration ok", got)
	}
}

func TestRunServerRejectsUnsupportedTransportFromEnvOverride(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "")
	t.Setenv("OPUTE_TRANSPORT", "")
	t.Setenv("OPUTE_REMOTE_AGENT_ID", "test-host-agent")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"serve", "--check", "--env", "OPUTE_TRANSPORT=stdio"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "only Streamable HTTP") {
		t.Fatalf("unsupported transport error = %v", err)
	}
}

func TestServerOnlyCommandBoundaryRejectsLegacyClientRouting(t *testing.T) {
	if command, _ := splitCommand(nil); command != "serve" {
		t.Fatalf("bare command = %q, want serve", command)
	}
	if command, _ := splitCommand([]string{"--url", "http://127.0.0.1:3014/mcp"}); command != "serve" {
		t.Fatalf("--url command = %q, want serve for explicit rejection", command)
	}
	if err := Run(context.Background(), []string{"--url", "http://127.0.0.1:3014/mcp"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("legacy --url result = %v, want an explicit unknown-flag error", err)
	}
	if err := Run(context.Background(), []string{"legacy-client"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("legacy client result = %v, want an explicit unknown-command error", err)
	}
}
