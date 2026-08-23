package hostmcp

import (
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/session"
	"github.com/wunderous/host-agents/internal/tools"
)

func (s *Server) handleOpenAssistantSession(args map[string]any) (*mcp.CallToolResult, error) {
	snapshot := s.CatalogSnapshot()
	sessionID, _ := args["sessionId"].(string)
	if strings.TrimSpace(sessionID) == "" {
		return tools.ErrorResult(fmt.Errorf("sessionId is required")), nil
	}
	versions := stringValues(args["supportedContractVersions"])
	compatible := false
	for _, version := range versions {
		if version == session.ContractVersion {
			compatible = true
			break
		}
	}
	if !compatible {
		return tools.ErrorResult(fmt.Errorf("no compatible assistant session contract; current=%s", session.ContractVersion)), nil
	}
	if requested, _ := args["catalogRevision"].(string); strings.TrimSpace(requested) != "" && requested != snapshot.Revision {
		return tools.ErrorResult(fmt.Errorf("catalog revision mismatch: requested=%s current=%s", requested, snapshot.Revision)), nil
	}
	activeTenant := s.ops.TenantID()
	if requested, _ := args["tenantId"].(string); strings.TrimSpace(requested) != "" && requested != activeTenant {
		return tools.ErrorResult(fmt.Errorf("tenant mismatch: requested=%s active=%s", requested, activeTenant)), nil
	}
	return structuredResult(map[string]any{
		"contractVersion": session.ContractVersion,
		"sessionId":       sessionID,
		"catalogRevision": snapshot.Revision,
		"providerId":      snapshot.ProviderID,
		"tenantId":        activeTenant,
	}, ""), nil
}

func stringValues(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}
