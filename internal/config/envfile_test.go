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

func TestValidateRejectsUnknownProfileValues(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "standalonee")
	if err := (Config{}).Validate(); err == nil {
		t.Fatal("expected invalid mode to fail")
	}

	t.Setenv("OPUTE_AGENT_MODE", "standalone")
	t.Setenv("OPUTE_TRANSPORT", "websocket")
	if err := (Config{}).Validate(); err == nil {
		t.Fatal("expected invalid transport to fail")
	}
}

func TestValidateRejectsStandalonePlatformSettings(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "standalone")
	t.Setenv("OPUTE_TRANSPORT", "stdio")
	t.Setenv("OPUTE_MCP_URL", "https://mcp.example/mcp")
	if err := (Config{AgentMode: "standalone", TransportMode: "stdio"}).Validate(); err == nil {
		t.Fatal("expected standalone platform URL to fail")
	}
}

func TestValidateAcceptsStandaloneStdioWithoutPlatformSettings(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "standalone")
	t.Setenv("OPUTE_TRANSPORT", "stdio")
	t.Setenv("OPUTE_MCP_URL", "")
	if err := (Config{AgentMode: "standalone", TransportMode: "stdio"}).Validate(); err != nil {
		t.Fatalf("expected valid standalone profile: %v", err)
	}
}

func TestValidateRejectsStandaloneHTTP(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "standalone")
	t.Setenv("OPUTE_TRANSPORT", "http")
	if err := (Config{AgentMode: "standalone", TransportMode: "http"}).Validate(); err == nil {
		t.Fatal("expected standalone HTTP transport to fail")
	}
}

func TestValidateRejectsStandaloneMCPAuthToken(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "standalone")
	t.Setenv("OPUTE_TRANSPORT", "stdio")
	t.Setenv("MCP_AUTH_TOKEN", "platform-token")
	if err := (Config{AgentMode: "standalone", TransportMode: "stdio"}).Validate(); err == nil {
		t.Fatal("expected standalone platform auth token to fail")
	}
}
