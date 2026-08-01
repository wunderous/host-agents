package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// HealthOnlyServer exposes GET /health while MCP runs over reverse tunnel.
type HealthOnlyServer struct {
	httpServer *http.Server
	logger     *slog.Logger
}

func NewHealthOnlyServer(bindHost string, port int, logger *slog.Logger, identity ...string) *HealthOnlyServer {
	if port <= 0 {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	instanceID := ""
	agentID := ""
	if len(identity) > 0 {
		instanceID = identity[0]
	}
	if len(identity) > 1 {
		agentID = identity[1]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		payload := map[string]any{
			"ok":              true,
			"isReverseTunnel": true,
		}
		if instanceID != "" {
			payload["instanceId"] = instanceID
		}
		if agentID != "" {
			payload["agentId"] = agentID
		}
		_ = json.NewEncoder(w).Encode(payload)
	})
	return &HealthOnlyServer{
		httpServer: &http.Server{
			Addr:              fmt.Sprintf("%s:%d", bindHost, port),
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
		logger: logger,
	}
}

func (h *HealthOnlyServer) Start() error {
	if h == nil {
		return nil
	}
	h.logger.Info("health-only transport listening", "addr", h.httpServer.Addr)
	return h.httpServer.ListenAndServe()
}

func (h *HealthOnlyServer) Shutdown(ctx context.Context) error {
	if h == nil {
		return nil
	}
	return h.httpServer.Shutdown(ctx)
}
