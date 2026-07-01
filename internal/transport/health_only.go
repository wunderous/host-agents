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

func NewHealthOnlyServer(bindHost string, port int, logger *slog.Logger) *HealthOnlyServer {
	if logger == nil {
		logger = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":              true,
			"isReverseTunnel": true,
		})
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
	h.logger.Info("health-only transport listening", "addr", h.httpServer.Addr)
	return h.httpServer.ListenAndServe()
}

func (h *HealthOnlyServer) Shutdown(ctx context.Context) error {
	return h.httpServer.Shutdown(ctx)
}
