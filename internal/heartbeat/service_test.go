package heartbeat

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMcpResponseBodyJSON(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`)
	result, err := parseMcpResponseBody(raw)
	if err != nil {
		t.Fatalf("parseMcpResponseBody: %v", err)
	}
	if result["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", result)
	}
}

func TestParseMcpResponseBodySSE(t *testing.T) {
	raw := []byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"agentId\":\"local-bridge-host\"}}\n\n")
	result, err := parseMcpResponseBody(raw)
	if err != nil {
		t.Fatalf("parseMcpResponseBody: %v", err)
	}
	if result["agentId"] != "local-bridge-host" {
		t.Fatalf("expected agentId, got %#v", result)
	}
}

func TestBearerTokenForToolPrefersOnboardingTokenForRegister(t *testing.T) {
	s := &Service{
		BridgeToken:     "opha_host_token",
		OnboardingToken: "opit_install_token",
	}
	if got := s.bearerTokenForTool("register_host_agent"); got != "opit_install_token" {
		t.Fatalf("register_host_agent bearer = %q, want opit_install_token", got)
	}
	if got := s.bearerTokenForTool("host_agent_heartbeat"); got != "opha_host_token" {
		t.Fatalf("host_agent_heartbeat bearer = %q, want opha_host_token", got)
	}
}

func TestReconcileAuthTokenUpdatesRuntimeAndEnvFile(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "host-agent.env")
	if err := os.WriteFile(envPath, []byte("MCP_AUTH_TOKEN=opha_old\nOPUTE_BRIDGE_TOKEN=opha_old\nBRIDGE_TOKEN=opha_old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Service{BridgeToken: "opha_old", EnvFile: envPath, Logger: slog.Default()}
	s.reconcileAuthToken(map[string]any{
		"structuredContent": map[string]any{
			"hostAgent": map[string]any{"authToken": "opha_new"},
		},
	})
	if s.BridgeToken != "opha_new" {
		t.Fatalf("runtime token = %q, want opha_new", s.BridgeToken)
	}
	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(content), "=opha_new") != 3 {
		t.Fatalf("env file did not update all host token aliases: %s", content)
	}
}

func TestIsAuthorizationErrorRecognizesMcpScopedAuthFailure(t *testing.T) {
	if !isAuthorizationError(errors.New("MCP error -32600: Unauthorized agent tool 'host_agent_heartbeat'")) {
		t.Fatal("expected MCP scoped authorization failure to be recognized")
	}
}
