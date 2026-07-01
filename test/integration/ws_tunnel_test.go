package integration_test

import (
	"testing"

	"github.com/opute-io/host-agents/internal/transport"
)

func TestBuildTunnelURL(t *testing.T) {
	got := transport.BuildTunnelURL("ws://127.0.0.1:9091/mcp-agent/foo", "agent-1")
	want := "ws://127.0.0.1:9091/mcp-agent/agent-1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
