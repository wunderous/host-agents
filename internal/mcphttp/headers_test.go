package mcphttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModernRequestEnvelopeMatchesFixture(t *testing.T) {
	meta, err := ModernRequestEnvelope("1.2.3")
	if err != nil {
		t.Fatalf("ModernRequestEnvelope: %v", err)
	}
	if meta["io.modelcontextprotocol/protocolVersion"] != "2026-07-28" {
		t.Fatalf("protocolVersion = %#v", meta["io.modelcontextprotocol/protocolVersion"])
	}
}

func TestApplyMcpRouteHostUsesEnvRouteHost(t *testing.T) {
	t.Setenv("OPUTE_MCP_ROUTE_HOST", "mcp.opute.io")
	t.Setenv("OPUTE_MCP_URL", "http://10.0.100.129/mcp")
	req := httptest.NewRequest(http.MethodPost, "http://10.0.100.129/mcp", nil)
	ApplyMcpRouteHost(req)
	if req.Host != "mcp.opute.io" {
		t.Fatalf("req.Host = %q", req.Host)
	}
	if got := req.Header.Get("Host"); got != "mcp.opute.io" {
		t.Fatalf("Host header = %q", got)
	}
}

func TestApplyStreamableHTTPRequestHeadersMatchesFixture(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://mcp.example/mcp", nil)
	if err := ApplyStreamableHTTPRequestHeaders(req); err != nil {
		t.Fatalf("ApplyStreamableHTTPRequestHeaders: %v", err)
	}
	if got := req.Header.Get("Accept"); got != "application/json, text/event-stream" {
		t.Fatalf("Accept = %q", got)
	}
	if got := req.Header.Get("MCP-Protocol-Version"); got != "2026-07-28" {
		t.Fatalf("MCP-Protocol-Version = %q", got)
	}
}
