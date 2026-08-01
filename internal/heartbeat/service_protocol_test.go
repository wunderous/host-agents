package heartbeat

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCallToolWithTokenSendsMcpProtocolVersion(t *testing.T) {
	var seenVersion string
	var seenMethod string
	var seenName string
	var seenMeta bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenVersion = r.Header.Get("MCP-Protocol-Version")
		seenMethod = r.Header.Get("Mcp-Method")
		seenName = r.Header.Get("Mcp-Name")
		var body struct {
			Params struct {
				Meta map[string]any `json:"_meta"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		seenMeta = body.Params.Meta != nil
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"ok":true}}}`)
	}))
	defer server.Close()

	s := &Service{
		MCPURL:       server.URL,
		BridgeToken:  "opha_test",
		AgentVersion: "test",
	}
	if _, err := s.callToolWithToken("host_agent_heartbeat", map[string]any{
		"heartbeat": map[string]any{"agentId": "host-test"},
	}, "opha_test"); err != nil {
		t.Fatalf("callToolWithToken: %v", err)
	}
	if seenVersion != "2026-07-28" {
		t.Fatalf("MCP-Protocol-Version = %q, want 2026-07-28", seenVersion)
	}
	if seenMethod != "tools/call" {
		t.Fatalf("Mcp-Method = %q, want tools/call", seenMethod)
	}
	if seenName != "host_agent_heartbeat" {
		t.Fatalf("Mcp-Name = %q, want host_agent_heartbeat", seenName)
	}
	if !seenMeta {
		t.Fatal("expected params._meta envelope on tools/call")
	}
}

func TestCallToolWithTokenBoundsStalledRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(time.Second)
	}))
	defer server.Close()

	previousTimeout := mcpRequestTimeout
	mcpRequestTimeout = 25 * time.Millisecond
	defer func() { mcpRequestTimeout = previousTimeout }()

	s := &Service{MCPURL: server.URL, BridgeToken: "opha_test", AgentVersion: "test"}
	_, err := s.callToolWithToken("host_agent_heartbeat", map[string]any{
		"heartbeat": map[string]any{"agentId": "host-test"},
	}, "opha_test")
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("callToolWithToken error = %v, want context deadline exceeded", err)
	}
}
