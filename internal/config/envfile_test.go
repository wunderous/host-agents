package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFilePreservesProcessEnvironment(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "from-process")
	path := filepath.Join(t.TempDir(), "host-agent.env")
	if err := os.WriteFile(path, []byte("# comment\nexport CLOUDFLARE_API_TOKEN=from-file\nOPUTE_MCP_URL='https://mcp.example/mcp'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := LoadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("CLOUDFLARE_API_TOKEN"); got != "from-process" {
		t.Fatalf("process env was overridden: %q", got)
	}
	if got := os.Getenv("OPUTE_MCP_URL"); got != "https://mcp.example/mcp" {
		t.Fatalf("quoted env value = %q", got)
	}
}

func TestLoadEnvFileRejectsMalformedAssignment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host-agent.env")
	if err := os.WriteFile(path, []byte("not-an-assignment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadEnvFile(path); err == nil {
		t.Fatal("expected malformed env assignment to fail")
	}
}
