package mcphttp

import (
	"context"
	"strings"
)

// ResourceDelegationMetaKey is private Host Agent request metadata used to
// carry an in-flight, signed resource-admission delegation across a trusted
// provider callback. It is never a tool argument or a client authorization
// token.
const ResourceDelegationMetaKey = "io.opute/host-resource-delegation"

type resourceDelegationContextKey struct{}

// WithResourceDelegation carries an opaque Host Agent-issued delegation for a
// provider callback. Callers cannot create a valid delegation; the receiving
// Host Agent verifies its signature and active invocation before using it.
func WithResourceDelegation(ctx context.Context, delegation string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	delegation = strings.TrimSpace(delegation)
	if delegation == "" {
		return ctx
	}
	return context.WithValue(ctx, resourceDelegationContextKey{}, delegation)
}

// ResourceDelegationFromContext returns the opaque delegation carried by ctx.
func ResourceDelegationFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	delegation, _ := ctx.Value(resourceDelegationContextKey{}).(string)
	return strings.TrimSpace(delegation)
}

// ResourceDelegationFromMeta extracts only the reserved delegation field from
// an incoming MCP metadata map.
func ResourceDelegationFromMeta(meta map[string]any) string {
	delegation, _ := meta[ResourceDelegationMetaKey].(string)
	return strings.TrimSpace(delegation)
}
