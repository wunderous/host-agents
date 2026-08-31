package hostmcp

import (
	"strings"

	"github.com/google/uuid"
)

// toolPrefixNamespaceName is the DNS name whose UUID v5 is the only namespace
// used to derive MCP wire prefixes. Fingerprint, hostname, and instance ID
// must not enter this derivation.
const toolPrefixNamespaceName = "opute.host-agent.mcp-tool-prefix"

// ToolNamePrefixNamespace is UUID v5(DNS, "opute.host-agent.mcp-tool-prefix").
var ToolNamePrefixNamespace = uuid.NewSHA1(uuid.NameSpaceDNS, []byte(toolPrefixNamespaceName))

// ToolNamePrefix returns the 8-hex MCP wire prefix derived solely from the
// canonical agent ID. Empty agent IDs produce an empty prefix.
func ToolNamePrefix(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	id := uuid.NewSHA1(ToolNamePrefixNamespace, []byte(agentID))
	hex := strings.ReplaceAll(id.String(), "-", "")
	if len(hex) < 8 {
		return hex
	}
	return hex[:8]
}

// WireToolName projects a catalog name onto the public MCP wire. An empty
// prefix leaves the catalog name unchanged.
func WireToolName(prefix, catalogName string) string {
	prefix = strings.TrimSpace(prefix)
	catalogName = strings.TrimSpace(catalogName)
	if prefix == "" || catalogName == "" {
		return catalogName
	}
	return prefix + "_" + catalogName
}

// CatalogNameFromWire strips a known agent prefix from a wire name. Names
// that do not carry the prefix are returned unchanged.
func CatalogNameFromWire(prefix, wireName string) string {
	prefix = strings.TrimSpace(prefix)
	wireName = strings.TrimSpace(wireName)
	if prefix == "" {
		return wireName
	}
	marker := prefix + "_"
	if strings.HasPrefix(wireName, marker) {
		return strings.TrimPrefix(wireName, marker)
	}
	return wireName
}

func implementationNameForPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "host-agent"
	}
	return "host-agent-" + prefix
}
