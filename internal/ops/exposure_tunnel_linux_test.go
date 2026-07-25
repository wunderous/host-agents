//go:build linux

package ops

import (
	"strings"
	"testing"
)

func TestNativeLinuxCloudflaredUnitUsesValidTunnelFlagOrdering(t *testing.T) {
	unit := nativeLinuxCloudflaredUnit(
		"host-1:llm.example.com",
		"/tmp/tunnel.env",
		"/tmp/cloudflared",
		"/tmp/tunnel.pid",
	)
	if !strings.Contains(unit, "ExecStart=/tmp/cloudflared tunnel --no-autoupdate --pidfile /tmp/tunnel.pid run") {
		t.Fatalf("unexpected cloudflared command:\n%s", unit)
	}
	if strings.Contains(unit, "tunnel run --no-autoupdate") {
		t.Fatalf("cloudflared global option appears after run:\n%s", unit)
	}
}
