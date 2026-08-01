package config

import (
	"os"
	"path/filepath"
	"strings"
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
	} else if !strings.Contains(err.Error(), "Streamable HTTP") {
		t.Fatalf("transport diagnostic = %q, want Streamable HTTP guidance", err)
	}
}

func TestValidateRejectsStandalonePlatformSettings(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "standalone")
	t.Setenv("OPUTE_MCP_URL", "https://mcp.example/mcp")
	if err := (Config{AgentMode: "standalone"}).Validate(); err == nil {
		t.Fatal("expected standalone platform URL to fail")
	}
}

func TestValidateAcceptsStandaloneHTTPWithoutPlatformSettings(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "standalone")
	t.Setenv("OPUTE_MCP_URL", "")
	t.Setenv("MCP_AUTH_TOKEN", "")
	if err := (Config{AgentMode: "standalone"}).Validate(); err != nil {
		t.Fatalf("expected valid standalone profile: %v", err)
	}
}

func TestValidateRejectsStandaloneStdio(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "standalone")
	t.Setenv("OPUTE_TRANSPORT", "stdio")
	err := (Config{AgentMode: "standalone"}).Validate()
	if err == nil {
		t.Fatal("expected standalone stdio transport to fail")
	}
	if !strings.Contains(err.Error(), "Streamable HTTP") {
		t.Fatalf("stdio diagnostic = %q, want Streamable HTTP guidance", err)
	}
}

func TestValidateRejectsPlatformStdio(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "platform")
	t.Setenv("OPUTE_TRANSPORT", "stdio")
	err := (Config{AgentMode: "platform"}).Validate()
	if err == nil {
		t.Fatal("expected platform stdio transport to fail")
	}
	if !strings.Contains(err.Error(), "Streamable HTTP") {
		t.Fatalf("stdio diagnostic = %q, want Streamable HTTP guidance", err)
	}
}

func TestValidateRejectsStandaloneMCPAuthToken(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "standalone")
	t.Setenv("MCP_AUTH_TOKEN", "platform-token")
	if err := (Config{AgentMode: "standalone"}).Validate(); err == nil {
		t.Fatal("expected standalone platform auth token to fail")
	}
}

func TestLoadStandaloneDefaultsPort3014(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "standalone")
	t.Setenv("HOST_MCP_PORT", "")
	t.Setenv("OPUTE_TRANSPORT", "")
	t.Setenv("OPUTE_MCP_URL", "")
	t.Setenv("MCP_AUTH_TOKEN", "")
	cfg := Load()
	if cfg.HostMCPPort != 3014 {
		t.Fatalf("standalone default port = %d, want 3014", cfg.HostMCPPort)
	}
}

func TestLoadPlatformDefaultsPort3004(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "platform")
	t.Setenv("HOST_MCP_PORT", "")
	cfg := Load()
	if cfg.HostMCPPort != 3004 {
		t.Fatalf("platform default port = %d, want 3004", cfg.HostMCPPort)
	}
}

func TestLoadNormalizesQuotedPlatformURLs(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "platform")
	t.Setenv("OPUTE_MCP_URL", "https://mcp.example/mcp")
	t.Setenv("OPUTE_HOST_WS_URL", "wss://mcp.example")
	t.Setenv("OPUTE_MCP_HEALTH_URL", "'https://mcp.example/health'")
	cfg := Load()
	if cfg.MCPHealthURL != "https://mcp.example/health" {
		t.Fatalf("health URL = %q", cfg.MCPHealthURL)
	}
	if cfg.HostWSURL != "wss://mcp.example" {
		t.Fatalf("websocket URL = %q", cfg.HostWSURL)
	}
}

func TestLoadResolvesExplicitInstanceRoots(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "platform")
	t.Setenv("OPUTE_HOST_AGENT_INSTANCE", "dogfood")
	t.Setenv("OPUTE_HOST_AGENT_INSTANCE_ROOT", "/tmp/opute-dogfood")
	t.Setenv("OPUTE_HOST_AGENT_RELAY_DIR", "")
	t.Setenv("OPUTE_STANDALONE_STATE_DIR", "")
	cfg := Load()
	if cfg.InstanceID != "dogfood" || cfg.InstanceRoot != "/tmp/opute-dogfood" {
		t.Fatalf("instance scope = %#v", cfg)
	}
	if cfg.RelayConfigDir != "/tmp/opute-dogfood/local-llm-relays" || cfg.StandaloneStateDir != "/tmp/opute-dogfood/state" {
		t.Fatalf("derived instance paths = %#v", cfg)
	}
}

func TestValidateRejectsInvalidInstanceID(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "standalone")
	if err := (Config{AgentMode: "standalone", InstanceID: "Dogfood"}).Validate(); err == nil {
		t.Fatal("expected invalid instance id to fail")
	}
}

func TestValidateRejectsDirectHttpPortZero(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "standalone")
	t.Setenv("HOST_MCP_PORT", "0")
	if err := Load().Validate(); err == nil {
		t.Fatal("expected direct HTTP port zero to fail")
	}
}

func TestValidateAllowsReverseTunnelPortZero(t *testing.T) {
	t.Setenv("OPUTE_AGENT_MODE", "platform")
	t.Setenv("OPUTE_REVERSE_TUNNEL", "true")
	t.Setenv("HOST_MCP_PORT", "0")
	if err := Load().Validate(); err != nil {
		t.Fatalf("expected reverse-tunnel port zero to pass: %v", err)
	}
}
