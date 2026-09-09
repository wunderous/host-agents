package hostmcp

import (
	"context"

	"github.com/wunderous/host-agents/internal/mcpprobe"
)

const mcpExposureProtocolVersion = mcpprobe.ProtocolVersion

// probeAuthenticatedMCPEndpoint remains a small package-local adapter for
// existing recipe validation. The wire implementation lives in mcpprobe so
// host-owned public exposure uses the exact same OAuth and tools/list proof.
func probeAuthenticatedMCPEndpoint(ctx context.Context, endpoint, bearerToken string) (map[string]any, error) {
	return mcpprobe.ProbeAuthenticatedMCPEndpoint(ctx, endpoint, bearerToken)
}
