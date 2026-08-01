package transport

import "testing"

func TestBuildHostWorkerURL(t *testing.T) {
	got := BuildHostWorkerURL("wss://mcp.example.com/mcp-agent/foo")
	want := "wss://mcp.example.com/host/v1/connect"
	if got != want {
		t.Fatalf("BuildHostWorkerURL() = %q, want %q", got, want)
	}
}
