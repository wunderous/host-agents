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
