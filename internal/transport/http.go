package transport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/authz"
	"github.com/wunderous/host-agents/internal/hostmcp"
)

// HTTPServer serves /health and /mcp for Streamable HTTP MCP 2026-07-28.
type HTTPServer struct {
	host                        *hostmcp.Server
	mcpHandler                  *mcp.StreamableHTTPHandler
	authz                       *authz.Service
	instanceID                  string
	agentID                     string
	physicalFingerprint         string
	fingerprintVersion          string
	fingerprintSource           string
	executionContextID          string
	executionContextKind        string
	executionContextDisplayName string
	healthObserver              func() map[string]any
	logger                      *slog.Logger
	httpServer                  *http.Server
	allowLegacyHandshake        bool
	mu                          sync.Mutex
}

type HTTPOptions struct {
	HostServer                  *hostmcp.Server
	BindHost                    string
	Port                        int
	Authz                       *authz.Service
	InstanceID                  string
	AgentID                     string
	PhysicalFingerprint         string
	FingerprintVersion          string
	FingerprintSource           string
	ExecutionContextID          string
	ExecutionContextKind        string
	ExecutionContextDisplayName string
	HealthObserver              func() map[string]any
	Logger                      *slog.Logger
	AllowLegacyHandshake        bool
}

const modernMCPVersion = "2026-07-28"

type protocolRequestError struct {
	code    int
	message string
	data    any
}

func (e *protocolRequestError) Error() string { return e.message }

func NewHTTPServer(opts HTTPOptions) *HTTPServer {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	h := &HTTPServer{
		host:                        opts.HostServer,
		authz:                       opts.Authz,
		instanceID:                  opts.InstanceID,
		agentID:                     opts.AgentID,
		physicalFingerprint:         opts.PhysicalFingerprint,
		fingerprintVersion:          opts.FingerprintVersion,
		fingerprintSource:           opts.FingerprintSource,
		executionContextID:          opts.ExecutionContextID,
		executionContextKind:        opts.ExecutionContextKind,
		executionContextDisplayName: opts.ExecutionContextDisplayName,
		healthObserver:              opts.HealthObserver,
		logger:                      logger,
		allowLegacyHandshake:        opts.AllowLegacyHandshake,
	}
	h.mcpHandler = mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return opts.HostServer.MCP()
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true})
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/mcp", h.handleMCP)
	if opts.Authz != nil {
		opts.Authz.RegisterHTTP(mux)
	}
	h.httpServer = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", opts.BindHost, opts.Port),
		Handler: mux,
	}
	return h
}

func (h *HTTPServer) Handler() http.Handler {
	return h.httpServer.Handler
}

func (h *HTTPServer) Addr() string {
	return h.httpServer.Addr
}

func (h *HTTPServer) Start() error {
	if h == nil || h.httpServer == nil {
		return fmt.Errorf("HTTP transport is not configured")
	}
	if h.httpServer.Addr == ":0" || strings.HasSuffix(h.httpServer.Addr, ":0") {
		return fmt.Errorf("HOST_MCP_PORT must be positive for direct HTTP mode")
	}
	h.logger.Info("HTTP transport listening", "addr", h.httpServer.Addr)
	return h.httpServer.ListenAndServe()
}

func (h *HTTPServer) Shutdown(ctx context.Context) error {
	return h.httpServer.Shutdown(ctx)
}

func (h *HTTPServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	payload := map[string]any{"ok": true}
	if h.instanceID != "" {
		payload["instanceId"] = h.instanceID
	}
	if h.agentID != "" {
		payload["agentId"] = h.agentID
	}
	if h.physicalFingerprint != "" {
		payload["fingerprint"] = h.physicalFingerprint
		payload["fingerprintVersion"] = h.fingerprintVersion
		payload["fingerprintSource"] = h.fingerprintSource
	}
	if h.executionContextID != "" {
		payload["executionContext"] = map[string]any{
			"id": h.executionContextID, "kind": h.executionContextKind,
			"displayName": h.executionContextDisplayName,
		}
	}
	if h.healthObserver != nil {
		if extra := h.healthObserver(); extra != nil {
			// Keep capability evidence separate from volatile capacity metrics so
			// registration consumers can promote it to capabilitySummary without
			// confusing tested capabilities with resource measurements.
			if capabilities, ok := extra["capabilities"]; ok {
				payload["capabilities"] = capabilities
				delete(extra, "capabilities")
			}
			if len(extra) > 0 {
				payload["capacity"] = extra
			}
		}
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *HTTPServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodGet, http.MethodDelete:
		w.Header().Set("Allow", "POST, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "POST, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !authz.OriginAllowed(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	decision := h.authorize(r)
	if !decision.Allowed {
		if decision.WWWAuth != "" {
			w.Header().Set("WWW-Authenticate", decision.WWWAuth)
		}
		status := decision.Status
		if status == 0 {
			status = http.StatusUnauthorized
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}
	var envelope struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		ID     any             `json:"id"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		http.Error(w, "invalid json-rpc", http.StatusBadRequest)
		return
	}
	// When legacy handshake is permitted, allow initialize / notifications/initialized
	// so standard MCP clients (e.g., Codex, Cursor IDE) can connect over HTTP.
	if isRetiredHandshake(envelope.Method) && !h.allowLegacyHandshake {
		writeJSONRPCProtocolError(w, envelope.ID, &protocolRequestError{
			code:    -32601,
			message: "Method not found: " + envelope.Method,
			data:    map[string]any{"supported": []string{modernMCPVersion}},
		})
		return
	}
	// Strict modern MCP validation is the default. It is skipped only for a
	// request that satisfies ALL THREE of the conditions below -- see ADR 0011.
	//
	if !skipModernValidation(h.allowLegacyHandshake, r, envelope.Method, envelope.Params) {
		if err := validateModernMCPRequest(r, envelope.Method, envelope.Params); err != nil {
			writeJSONRPCProtocolError(w, envelope.ID, err)
			return
		}
	}
	if isModernOnlyMethod(envelope.Method) {
		result, err := h.host.HandleExtensionMethod(envelope.Method, envelope.Params)
		if err != nil {
			writeJSONRPCError(w, envelope.ID, err)
			return
		}
		writeJSONRPCResult(w, envelope.ID, result)
		return
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	if envelope.Method == "tools/call" {
		h.serveToolCall(w, r)
		return
	}
	h.mcpHandler.ServeHTTP(w, r)
}

func (h *HTTPServer) authorize(r *http.Request) authz.Decision {
	if h.authz == nil {
		return authz.Decision{Status: http.StatusUnauthorized}
	}
	return h.authz.Authorize(r)
}

func (h *HTTPServer) serveToolCall(w http.ResponseWriter, r *http.Request) {
	recorder := httptest.NewRecorder()
	h.mcpHandler.ServeHTTP(recorder, r)
	body := normalizeTaskCreationResponse(recorder.Body.Bytes())
	for key, values := range recorder.Header() {
		w.Header()[key] = append([]string(nil), values...)
	}
	w.Header().Del("Content-Length")
	w.WriteHeader(recorder.Code)
	_, _ = w.Write(body)
}

func normalizeTaskCreationResponse(body []byte) []byte {
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body
	}
	result, ok := envelope["result"].(map[string]any)
	if !ok {
		return body
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok || structured["resultType"] != "task" {
		return body
	}
	flat := make(map[string]any, len(structured))
	for key, value := range structured {
		flat[key] = value
	}
	if meta, ok := result["_meta"]; ok {
		flat["_meta"] = meta
	}
	envelope["result"] = flat
	normalized, err := json.Marshal(envelope)
	if err != nil {
		return body
	}
	return normalized
}

func decodeRFC2047(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "=?") || !strings.HasSuffix(value, "?=") {
		return value
	}
	parts := strings.Split(value, "?")
	if len(parts) != 5 {
		return value
	}
	charset, encoding, payload := strings.ToLower(parts[1]), strings.ToLower(parts[2]), parts[3]
	if charset != "utf-8" && charset != "us-ascii" {
		return value
	}
	switch encoding {
	case "b":
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return value
		}
		return string(decoded)
	case "q":
		decoded, err := decodeQuotedPrintable(payload)
		if err != nil {
			return value
		}
		return decoded
	default:
		return value
	}
}

func decodeQuotedPrintable(value string) (string, error) {
	var out []byte
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '_':
			out = append(out, ' ')
		case '=':
			if i+2 >= len(value) {
				return "", fmt.Errorf("truncated quoted-printable")
			}
			decoded, err := hexByte(value[i+1 : i+3])
			if err != nil {
				return "", err
			}
			out = append(out, decoded)
			i += 2
		default:
			out = append(out, value[i])
		}
	}
	return string(out), nil
}

func hexByte(value string) (byte, error) {
	var n byte
	for _, ch := range []byte(value) {
		n <<= 4
		switch {
		case ch >= '0' && ch <= '9':
			n |= ch - '0'
		case ch >= 'a' && ch <= 'f':
			n |= ch - 'a' + 10
		case ch >= 'A' && ch <= 'F':
			n |= ch - 'A' + 10
		default:
			return 0, fmt.Errorf("invalid hex")
		}
	}
	return n, nil
}

func writeJSONRPCResult(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func writeJSONRPCError(w http.ResponseWriter, id any, err error) {
	w.Header().Set("Content-Type", "application/json")
	code := -32603
	message := err.Error()
	status := http.StatusOK
	var data any
	var protocolErr *protocolRequestError
	if errors.As(err, &protocolErr) {
		code = protocolErr.code
		message = protocolErr.message
		data = protocolErr.data
		if code == -32601 {
			status = http.StatusNotFound
		}
	}
	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) {
		code = int(rpcErr.Code)
		message = rpcErr.Message
		if len(rpcErr.Data) > 0 {
			_ = json.Unmarshal(rpcErr.Data, &data)
		}
		if code == -32601 {
			status = http.StatusNotFound
		}
	}
	errorBody := map[string]any{
		"code":    code,
		"message": message,
	}
	if data != nil {
		errorBody["data"] = data
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   errorBody,
	})
}

func writeJSONRPCProtocolError(w http.ResponseWriter, id any, err error) {
	w.Header().Set("Content-Type", "application/json")
	protocolErr, ok := err.(*protocolRequestError)
	if !ok {
		protocolErr = &protocolRequestError{code: -32603, message: err.Error()}
	}
	status := http.StatusBadRequest
	if protocolErr.code == -32601 {
		status = http.StatusNotFound
	}
	w.WriteHeader(status)
	errorPayload := map[string]any{"code": protocolErr.code, "message": protocolErr.message}
	if protocolErr.data != nil {
		errorPayload["data"] = protocolErr.data
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": errorPayload})
}
