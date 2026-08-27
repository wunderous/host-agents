// Package authz is the Host Agent in-process OAuth 2.1 resource server and
// co-located authorization server for MCP 2026-07-28.
package authz

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

const (
	HostTokenPrefix = "oha_"
	MCPScope        = "mcp"
)

// ProductPassthroughPrefixes must never open host /mcp.
var ProductPassthroughPrefixes = []string{"opha_", "opit_", "opsess_"}

func IsProductPassthroughToken(token string) bool {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range ProductPassthroughPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func IsHostIssuedToken(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), HostTokenPrefix)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func constantEquals(a, b string) bool {
	if subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 {
		return true
	}
	return false
}
