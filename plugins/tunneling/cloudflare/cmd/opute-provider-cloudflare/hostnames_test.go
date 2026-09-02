package main

import "testing"

func TestHostnamesFromArgsAcceptsTwoZones(t *testing.T) {
	got := hostnamesFromArgs(map[string]any{
		"hostnames": []any{"agent.example.test", "mcp.other.test"},
	})
	if len(got) != 2 || got[0] != "agent.example.test" || got[1] != "mcp.other.test" {
		t.Fatalf("hostnames = %#v", got)
	}
	for _, name := range got {
		if name == "" || name == "*.opute.io" {
			t.Fatalf("product hostname special case leaked: %#v", got)
		}
	}
}

func TestValidateLocalTargetAcceptsHostAgentMCP(t *testing.T) {
	if err := validateLocalTarget("host-agent-mcp", "host", ""); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalTarget("http://127.0.0.1:3004/mcp", "host", ""); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalTarget("https://agent.example.test/mcp", "host", ""); err == nil {
		t.Fatal("expected non-loopback host connector to fail")
	}
	if err := validateLocalTarget("http://10.0.200.1:3005", "host", "host-opute-ha-b-b9234af4"); err != nil {
		t.Fatalf("declared remote host target rejected: %v", err)
	}
}
