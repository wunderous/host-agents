package ops

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpsertEnvFileMergesPlatformKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host-agent.env")
	if err := os.WriteFile(path, []byte("FOO=bar\nOPUTE_MCP_URL=http://127.0.0.1:3014/mcp\nOPUTE_STANDALONE_ALLOW_MUTATIONS=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := upsertEnvFile(path, map[string]string{
		"OPUTE_AGENT_MODE":              "platform",
		"OPUTE_REVERSE_TUNNEL":          "true",
		"OPUTE_MCP_URL":                 "https://mcp.opute.io/mcp",
		"OPUTE_REMOTE_AGENT_AUTH_TOKEN": "cpc-token",
		"MCP_AUTH_TOKEN":                "opha_token",
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "FOO=bar") {
		t.Fatalf("expected preserved FOO, got %q", body)
	}
	if !strings.Contains(body, "OPUTE_MCP_URL=https://mcp.opute.io/mcp") {
		t.Fatalf("expected mcp url update, got %q", body)
	}
	if strings.Contains(body, "OPUTE_STANDALONE_ALLOW_MUTATIONS") {
		t.Fatalf("standalone mutation flag should be removed, got %q", body)
	}
	if !strings.Contains(body, "MCP_AUTH_TOKEN=opha_token") {
		t.Fatalf("expected host auth token, got %q", body)
	}
}

func TestConfigurePlatformAgentRequiresLinuxAndArgs(t *testing.T) {
	svc := &HostOperationsService{}
	_, err := svc.ConfigurePlatformAgent(ConfigurePlatformAgentArgs{}, nil)
	if runtime.GOOS != "linux" {
		if err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("expected unsupported error on %s, got %v", runtime.GOOS, err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "mcpUrl") {
		t.Fatalf("expected mcpUrl validation error, got %v", err)
	}
}

func TestConfigurePlatformAgentWritesEnvWithoutRestart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "host-agent.env")
	restart := false
	svc := &HostOperationsService{}
	out, err := svc.ConfigurePlatformAgent(ConfigurePlatformAgentArgs{
		McpURL:               "https://mcp.example.com/mcp",
		RemoteAgentAuthToken: "remote-token",
		HostAuthToken:        "opha_test",
		RemoteAgentID:        "host-test-1",
		EnvFile:              path,
		Restart:              &restart,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["status"] != "env_written" {
		t.Fatalf("unexpected status: %#v", out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"OPUTE_AGENT_MODE=platform",
		"OPUTE_REVERSE_TUNNEL=true",
		"OPUTE_MCP_URL=https://mcp.example.com/mcp",
		"OPUTE_HOST_WS_URL=wss://mcp.example.com/mcp",
		"OPUTE_REMOTE_AGENT_ID=host-test-1",
		"OPUTE_REMOTE_AGENT_AUTH_TOKEN=remote-token",
		"MCP_AUTH_TOKEN=opha_test",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in %q", want, body)
		}
	}
}
