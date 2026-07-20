package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/hostmcp"
)

// HTTPServer serves /health and /mcp for direct HTTP MCP mode.
type HTTPServer struct {
	host       *hostmcp.Server
	mcpHandler *mcp.StreamableHTTPHandler
	tokens     []string
	instanceID string
	logger     *slog.Logger
	httpServer *http.Server
	mu         sync.Mutex
}

type HTTPOptions struct {
	HostServer *hostmcp.Server
	BindHost   string
	Port       int
	AuthTokens []string
	InstanceID string
	Logger     *slog.Logger
}

func NewHTTPServer(opts HTTPOptions) *HTTPServer {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	h := &HTTPServer{
		host:       opts.HostServer,
		tokens:     opts.AuthTokens,
		instanceID: opts.InstanceID,
		logger:     logger,
	}
	h.mcpHandler = mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return opts.HostServer.MCP()
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/mcp", h.handleMCP)
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
	h.logger.Info("HTTP transport listening", "addr", h.httpServer.Addr)
	return h.httpServer.ListenAndServe()
}

func (h *HTTPServer) Shutdown(ctx context.Context) error {
	return h.httpServer.Shutdown(ctx)
}

func (h *HTTPServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	payload := map[string]any{"ok": true, "isReverseTunnel": false}
	if h.instanceID != "" {
		payload["instanceId"] = h.instanceID
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *HTTPServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !h.authorize(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		h.mcpHandler.ServeHTTP(w, r)
		return
	}
	var envelope struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		ID     any             `json:"id"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		h.mcpHandler.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(envelope.Method, "tasks/") || envelope.Method == "resources/list" || envelope.Method == "resources/read" {
		result, err := h.host.HandleExtensionMethod(envelope.Method, envelope.Params)
		if err != nil {
			writeJSONRPCError(w, envelope.ID, err)
			return
		}
		writeJSONRPCResult(w, envelope.ID, result)
		return
	}
	if envelope.Method == "tools/call" {
		body = normalizeToolCallTaskAugmentation(body)
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	h.mcpHandler.ServeHTTP(w, r)
}

// normalizeToolCallTaskAugmentation maps normative params.task to _meta.task for go-sdk handlers.
func normalizeToolCallTaskAugmentation(body []byte) []byte {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body
	}
	paramsRaw, ok := envelope["params"]
	if !ok {
		return body
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		return body
	}
	taskRaw, hasTask := params["task"]
	if !hasTask {
		return body
	}
	meta := map[string]json.RawMessage{}
	if existing, ok := params["_meta"]; ok {
		_ = json.Unmarshal(existing, &meta)
	}
	if _, ok := meta["task"]; !ok {
		meta["task"] = taskRaw
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return body
	}
	params["_meta"] = metaBytes
	delete(params, "task")
	newParams, err := json.Marshal(params)
	if err != nil {
		return body
	}
	envelope["params"] = newParams
	out, err := json.Marshal(envelope)
	if err != nil {
		return body
	}
	return out
}

func (h *HTTPServer) authorize(r *http.Request) bool {
	if len(h.tokens) == 0 {
		return true
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	for _, allowed := range h.tokens {
		if token == allowed {
			return true
		}
	}
	return false
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
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    -32603,
			"message": err.Error(),
		},
	})
}
