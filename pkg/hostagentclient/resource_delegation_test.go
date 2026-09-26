package hostagentclient

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/mcphttp"
)

func TestForwardResourceDelegationCopiesOnlyReservedMetadata(t *testing.T) {
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Meta: mcp.Meta{mcphttp.ResourceDelegationMetaKey: "opaque.signed.token"},
	}}
	ctx := ForwardResourceDelegation(context.Background(), request)
	if got := mcphttp.ResourceDelegationFromContext(ctx); got != "opaque.signed.token" {
		t.Fatalf("forwarded delegation = %q", got)
	}

	unchanged := ForwardResourceDelegation(ctx, nil)
	if got := mcphttp.ResourceDelegationFromContext(unchanged); got != "opaque.signed.token" {
		t.Fatalf("nil request changed delegation = %q", got)
	}
}
