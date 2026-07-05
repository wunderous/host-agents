package heartbeat

import "testing"

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
