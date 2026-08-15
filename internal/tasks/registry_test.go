package tasks

import "testing"

func TestHostServingReconciliationUsesTaskContract(t *testing.T) {
	if !TaskAwareTools["ensure_cloudflared_tunnel"] {
		t.Fatal("ensure_cloudflared_tunnel must use the MCP task contract")
	}
}
