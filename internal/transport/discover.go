package transport

import (
	"encoding/json"
	"net/http"
	"strings"
)

// This file is the transport's protocol contract: which MCP methods exist only
// in 2026-07-28, which pre-2026 methods may skip validation, and what a
// validated modern request must carry. `ha-k3` names `server/discover` and the
// `Mcp-Method` / `_meta` gating as parts of one edge (plan §6 W3); they lived
// as private helpers tangled into the HTTP handler, where the split had nothing
// named to carry across. They are one seam here.

// modernOnlyMethods and modernOnlyMethodPrefixes are the 2026-07-28 surface: a
// method no pre-2026 client can need, because it did not exist for them.
//
// This set had been stated twice -- once as the extension router's condition in
// handleMCP, and once as prose in legacyCompatibleMethods' comment explaining
// what is "notably absent" from the bypass. Two statements of one set drift:
// adding a fourth extension family to the router would silently leave the
// allowlist's reasoning stale. TestLegacyBypassNeverCoversModernOnlyMethods
// now checks the relationship the prose used to assert.
var modernOnlyMethods = map[string]struct{}{
	"server/discover": {},
}

// resources/* is here even though this server serves no resource method: it
// declares the resources capability, so allowlisting the family would only
// widen the bypass around requests that cannot succeed.
var modernOnlyMethodPrefixes = []string{"tasks/", "resources/"}

// isModernOnlyMethod reports whether a method belongs to the 2026-07-28-only
// surface. It is both the extension router's condition and the property the
// legacy bypass must never cover.
func isModernOnlyMethod(method string) bool {
	if _, ok := modernOnlyMethods[method]; ok {
		return true
	}
	for _, prefix := range modernOnlyMethodPrefixes {
		if strings.HasPrefix(method, prefix) {
			return true
		}
	}
	return false
}

// skipModernValidation reports whether this request may bypass the modern
// contract. The bypass is enumerated rather than implied.
//
// It used to read `!allowLegacyHandshake || (!isRetiredHandshake(m) &&
// isModernMCPRequest(...))`, which skipped validation for ANY method whose
// params omitted `_meta["io.modelcontextprotocol/protocolVersion"]`. That made
// conformance client-elective across the whole surface: a `tasks/get` could
// reach the handler with no Mcp-Method header and no protocol version simply by
// not asking to be checked. The flag is named for the handshake but was
// disabling the transport contract.
func skipModernValidation(allowLegacyHandshake bool, r *http.Request, method string, params json.RawMessage) bool {
	return allowLegacyHandshake &&
		isLegacyCompatibleMethod(method) &&
		// A legacy client cannot negotiate; one that does is held to the contract.
		(isRetiredHandshake(method) || !isModernMCPRequest(r, params))
}

// legacyCompatibleMethods is the pre-2026-07-28 MCP client surface: the methods
// a client such as Codex or Cursor IDE speaks before it can be expected to send
// `Mcp-Method` / `MCP-Protocol-Version` headers or the modern `_meta` keys.
//
// It is an allowlist on purpose. Adding an entry widens the set of requests that
// may skip contract validation, so each one needs a reason recorded in ADR 0011
// -- which is the property the previous `isModernMCPRequest`-only check did not
// have.
//
// What must never appear here is a modern-only method -- `server/discover` and
// the `tasks/*` extension, which carries its own client capability negotiation
// that the old bypass skipped wholesale. That used to be prose; it is now
// isModernOnlyMethod, asserted by TestLegacyBypassNeverCoversModernOnlyMethods.
//
// TestLegacyCompatibleMethodsAreServed keeps this list from drifting back into
// naming methods the server does not answer.
var legacyCompatibleMethods = map[string]struct{}{
	"initialize":                {},
	"notifications/initialized": {},
	"notifications/cancelled":   {},
	"ping":                      {},
	"tools/list":                {},
	"tools/call":                {},
	"prompts/list":              {},
	"prompts/get":               {},
}

func isLegacyCompatibleMethod(method string) bool {
	_, ok := legacyCompatibleMethods[method]
	return ok
}

func isRetiredHandshake(method string) bool {
	switch method {
	case "initialize", "notifications/initialized":
		return true
	default:
		return false
	}
}

// isModernMCPRequest checks if a JSON-RPC request explicitly targets the modern 2026-07-28 protocol.
// Standard MCP clients routinely send generic _meta fields (such as progress tokens or empty dictionaries).
// Only requests containing the explicit "io.modelcontextprotocol/protocolVersion" key inside _meta are
// subject to strict modern header and metadata verification.
func isModernMCPRequest(r *http.Request, raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return false
	}
	metaRaw, ok := params["_meta"]
	if !ok {
		return false
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return false
	}
	_, ok = meta["io.modelcontextprotocol/protocolVersion"]
	return ok
}

func validateModernMCPRequest(r *http.Request, method string, raw json.RawMessage) error {
	headerVersion := decodeRFC2047(strings.TrimSpace(r.Header.Get("MCP-Protocol-Version")))
	headerMethod := decodeRFC2047(strings.TrimSpace(r.Header.Get("Mcp-Method")))
	headerName := decodeRFC2047(strings.TrimSpace(r.Header.Get("Mcp-Name")))
	var params map[string]json.RawMessage
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &params) != nil {
		return &protocolRequestError{code: -32020, message: "HeaderMismatch"}
	}
	metaRaw, ok := params["_meta"]
	if !ok {
		return &protocolRequestError{code: -32020, message: "HeaderMismatch"}
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(metaRaw, &meta) != nil {
		return &protocolRequestError{code: -32020, message: "HeaderMismatch"}
	}
	var requestedVersion string
	if versionRaw, ok := meta["io.modelcontextprotocol/protocolVersion"]; ok {
		_ = json.Unmarshal(versionRaw, &requestedVersion)
	}
	if requestedVersion == "" {
		return &protocolRequestError{code: -32020, message: "HeaderMismatch"}
	}
	if requestedVersion != modernMCPVersion || (headerVersion != "" && headerVersion != modernMCPVersion) {
		return &protocolRequestError{code: -32022, message: "Unsupported protocol version", data: map[string]any{"supported": []string{modernMCPVersion}, "requested": requestedVersion}}
	}
	if headerVersion == "" || headerVersion != requestedVersion {
		return &protocolRequestError{code: -32020, message: "HeaderMismatch"}
	}
	if headerMethod == "" || headerMethod != method {
		return &protocolRequestError{code: -32020, message: "HeaderMismatch"}
	}
	for _, key := range []string{"io.modelcontextprotocol/clientInfo", "io.modelcontextprotocol/clientCapabilities"} {
		if _, ok := meta[key]; !ok {
			return &protocolRequestError{code: -32020, message: "HeaderMismatch"}
		}
	}
	if method == "tools/call" {
		var name string
		if nameRaw, ok := params["name"]; ok {
			_ = json.Unmarshal(nameRaw, &name)
		}
		if headerName == "" || headerName != name {
			return &protocolRequestError{code: -32020, message: "HeaderMismatch"}
		}
	}
	if strings.HasPrefix(method, "tasks/") {
		var capabilities map[string]json.RawMessage
		if err := json.Unmarshal(meta["io.modelcontextprotocol/clientCapabilities"], &capabilities); err != nil {
			return &protocolRequestError{code: -32003, message: "Missing required client capability", data: map[string]any{"requiredCapabilities": map[string]any{"extensions": map[string]any{"io.modelcontextprotocol/tasks": map[string]any{}}}}}
		}
		var extensions map[string]json.RawMessage
		if err := json.Unmarshal(capabilities["extensions"], &extensions); err != nil {
			return &protocolRequestError{code: -32003, message: "Missing required client capability", data: map[string]any{"requiredCapabilities": map[string]any{"extensions": map[string]any{"io.modelcontextprotocol/tasks": map[string]any{}}}}}
		}
		if _, ok := extensions["io.modelcontextprotocol/tasks"]; !ok {
			return &protocolRequestError{code: -32003, message: "Missing required client capability", data: map[string]any{"requiredCapabilities": map[string]any{"extensions": map[string]any{"io.modelcontextprotocol/tasks": map[string]any{}}}}}
		}
		if method == "tasks/get" || method == "tasks/update" || method == "tasks/cancel" {
			var taskID string
			if taskRaw, ok := params["taskId"]; !ok || json.Unmarshal(taskRaw, &taskID) != nil || strings.TrimSpace(taskID) == "" {
				return &protocolRequestError{code: -32602, message: method + " requires params.taskId"}
			}
			if headerName == "" || headerName != taskID {
				return &protocolRequestError{code: -32020, message: "HeaderMismatch"}
			}
		}
	}
	return nil
}

func validateModernExtensionRequest(r *http.Request, method string, raw json.RawMessage) error {
	return validateModernMCPRequest(r, method, raw)
}
