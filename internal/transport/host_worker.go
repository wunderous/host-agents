package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wunderous/host-agents/internal/hostmcp"
	"github.com/wunderous/host-agents/internal/mcphttp"
)

const hostWorkerProtocolVersion = "hwp/1"

type hwpServerFrame struct {
	Type string `json:"type"`
}

type hwpRegisterFrame struct {
	Type            string         `json:"type"`
	HostID          string         `json:"hostId"`
	ProtocolVersion string         `json:"protocolVersion"`
	AgentVersion    string         `json:"agentVersion,omitempty"`
	Capacity        map[string]any `json:"capacity,omitempty"`
}

type hwpHeartbeatFrame struct {
	Type     string         `json:"type"`
	HostID   string         `json:"hostId"`
	Capacity map[string]any `json:"capacity,omitempty"`
}

type hwpSyncCallFrame struct {
	Type      string         `json:"type"`
	RequestID string         `json:"requestId"`
	HostID    string         `json:"hostId"`
	Action    string         `json:"action"`
	Args      map[string]any `json:"args"`
	TimeoutMs int            `json:"timeoutMs,omitempty"`
}

type hwpAssignFrame struct {
	Type          string         `json:"type"`
	OperationID   string         `json:"operationId"`
	HostID        string         `json:"hostId"`
	Action        string         `json:"action"`
	Args          map[string]any `json:"args"`
	ProgressToken string         `json:"progressToken,omitempty"`
}

type hwpAssignCancelFrame struct {
	Type        string `json:"type"`
	OperationID string `json:"operationId"`
	HostID      string `json:"hostId"`
}

type hwpSyncResultFrame struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Result    any    `json:"result"`
}

type hwpSyncErrorFrame struct {
	Type      string         `json:"type"`
	RequestID string         `json:"requestId"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
}

type hostWorkerConn struct {
	conn    *websocket.Conn
	hostID  string
	logger  *slog.Logger
	host    *hostmcp.Server
	mu      sync.Mutex
	pending map[string]chan syncResult
	assigns map[string]context.CancelFunc
}

type syncResult struct {
	value any
	err   error
}

// BuildHostWorkerURL derives the HWP WebSocket endpoint from a host WS base URL.
func BuildHostWorkerURL(wsBase string) string {
	root := strings.TrimRight(strings.TrimSpace(wsBase), "/")
	if idx := strings.Index(strings.ToLower(root), "/mcp-agent"); idx >= 0 {
		root = root[:idx]
	}
	if idx := strings.Index(strings.ToLower(root), "/host/v1/connect"); idx >= 0 {
		root = root[:idx]
	}
	return root + "/host/v1/connect"
}

// RunHostWorkerLoop maintains the outbound Host Worker Protocol session.
func RunHostWorkerLoop(ctx context.Context, host *hostmcp.Server, wsURL, agentID, authToken, healthURL string, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	attempt := 1
	for {
		if ctx.Err() != nil {
			return
		}
		if healthURL != "" {
			if err := waitForHealth(ctx, healthURL, 30*time.Second); err != nil {
				logger.Warn("aggregator not healthy for host worker", "err", err)
				time.Sleep(2 * time.Second)
				continue
			}
		}
		err := connectHostWorkerOnce(ctx, host, wsURL, agentID, authToken, logger)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logger.Warn("host worker disconnected", "attempt", attempt, "err", err)
			host.AbortAllConsoleStreams()
			attempt++
			time.Sleep(2 * time.Second)
			continue
		}
	}
}

func connectHostWorkerOnce(ctx context.Context, host *hostmcp.Server, wsURL, agentID, authToken string, logger *slog.Logger) error {
	workerURL := BuildHostWorkerURL(wsURL)
	header := http.Header{}
	if authToken != "" {
		header.Set("Authorization", "Bearer "+authToken)
	}
	if routeHost := mcphttp.RouteHostFromEnv(); routeHost != "" {
		header.Set("Host", routeHost)
	}
	dialer := websocket.Dialer{}
	conn, _, err := dialer.DialContext(ctx, workerURL, header)
	if err != nil {
		return err
	}
	logger.Info("host worker connected", "url", workerURL)

	session := &hostWorkerConn{
		conn:    conn,
		hostID:  agentID,
		logger:  logger,
		host:    host,
		pending: make(map[string]chan syncResult),
		assigns: make(map[string]context.CancelFunc),
	}

	register := hwpRegisterFrame{
		Type:            "register",
		HostID:          agentID,
		ProtocolVersion: hostWorkerProtocolVersion,
		AgentVersion:    "go-host-agent",
	}
	if err := conn.WriteJSON(register); err != nil {
		_ = conn.Close()
		return err
	}

	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	defer cancelHeartbeat()
	go session.runHeartbeat(heartbeatCtx)

	readErrCh := make(chan error, 1)
	go func() { readErrCh <- session.readLoop(ctx) }()

	select {
	case err := <-readErrCh:
		cancelHeartbeat()
		session.cancelAllAssigns()
		_ = conn.Close()
		if err == nil {
			return fmt.Errorf("host worker closed")
		}
		return err
	case <-ctx.Done():
		cancelHeartbeat()
		session.cancelAllAssigns()
		_ = conn.Close()
		<-readErrCh
		return ctx.Err()
	}
}

func (s *hostWorkerConn) runHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			frame := hwpHeartbeatFrame{Type: "heartbeat", HostID: s.hostID}
			s.mu.Lock()
			err := s.conn.WriteJSON(frame)
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func (s *hostWorkerConn) cancelAllAssigns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, cancel := range s.assigns {
		cancel()
		delete(s.assigns, id)
	}
}

func (s *hostWorkerConn) readLoop(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			return err
		}
		var envelope hwpServerFrame
		if err := json.Unmarshal(data, &envelope); err != nil {
			return err
		}
		switch envelope.Type {
		case "registered", "heartbeat_ack":
			continue
		case "sync_call":
			var frame hwpSyncCallFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				return err
			}
			go s.handleSyncCall(frame)
		case "assign":
			var frame hwpAssignFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				return err
			}
			go s.handleAssign(frame)
		case "assign_cancel":
			var frame hwpAssignCancelFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				return err
			}
			s.handleAssignCancel(frame)
		default:
			s.logger.Warn("ignored host worker frame", "type", envelope.Type)
		}
	}
}

func (s *hostWorkerConn) handleSyncCall(frame hwpSyncCallFrame) {
	timeout := 30 * time.Second
	if frame.TimeoutMs > 0 {
		timeout = time.Duration(frame.TimeoutMs) * time.Millisecond
	}
	callCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := frame.Args
	if args == nil {
		args = map[string]any{}
	}
	result, err := s.host.DispatchTool(callCtx, frame.Action, args, func(string) {})
	if err != nil {
		_ = s.writeJSON(hwpSyncErrorFrame{
			Type:      "sync_error",
			RequestID: frame.RequestID,
			Code:      "dispatch_error",
			Message:   err.Error(),
		})
		return
	}
	_ = s.writeJSON(hwpSyncResultFrame{
		Type:      "sync_result",
		RequestID: frame.RequestID,
		Result:    result,
	})
}

func (s *hostWorkerConn) handleAssign(frame hwpAssignFrame) {
	assignCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if prior, ok := s.assigns[frame.OperationID]; ok {
		prior()
	}
	s.assigns[frame.OperationID] = cancel
	s.mu.Unlock()

	_ = s.writeJSON(map[string]any{
		"type":        "assign_ack",
		"operationId": frame.OperationID,
	})

	args := frame.Args
	if args == nil {
		args = map[string]any{}
	}

	onData := func(chunk string) {
		if chunk == "" {
			return
		}
		_ = s.writeJSON(map[string]any{
			"type":        "progress",
			"operationId": frame.OperationID,
			"message":     chunk,
		})
	}

	result, err := s.host.DispatchTool(assignCtx, frame.Action, args, onData)
	s.mu.Lock()
	delete(s.assigns, frame.OperationID)
	s.mu.Unlock()

	if assignCtx.Err() != nil {
		_ = s.writeJSON(map[string]any{
			"type":        "fail",
			"operationId": frame.OperationID,
			"message":     assignCtx.Err().Error(),
		})
		return
	}
	if err != nil {
		_ = s.writeJSON(map[string]any{
			"type":        "fail",
			"operationId": frame.OperationID,
			"message":     err.Error(),
		})
		return
	}
	if result != nil && result.IsError {
		msg := "assign failed"
		if len(result.Content) > 0 {
			msg = fmt.Sprintf("%v", result.Content[0])
		}
		payload := map[string]any{
			"type":        "fail",
			"operationId": frame.OperationID,
			"message":     msg,
		}
		if details, ok := result.StructuredContent.(map[string]any); ok && len(details) > 0 {
			payload["details"] = details
		}
		_ = s.writeJSON(payload)
		return
	}
	payload := map[string]any{}
	if result != nil && result.StructuredContent != nil {
		if structured, ok := result.StructuredContent.(map[string]any); ok {
			payload = structured
		}
	}
	_ = s.writeJSON(map[string]any{
		"type":        "complete",
		"operationId": frame.OperationID,
		"result":      payload,
	})
}

func (s *hostWorkerConn) handleAssignCancel(frame hwpAssignCancelFrame) {
	s.mu.Lock()
	cancel, ok := s.assigns[frame.OperationID]
	if ok {
		cancel()
		delete(s.assigns, frame.OperationID)
	}
	s.mu.Unlock()
}

func (s *hostWorkerConn) writeJSON(payload any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(payload)
}
