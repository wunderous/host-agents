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

type hwpStreamOpenFrame struct {
	Type      string         `json:"type"`
	RequestID string         `json:"requestId"`
	HostID    string         `json:"hostId"`
	Action    string         `json:"action"`
	Args      map[string]any `json:"args"`
}

type hwpStreamInputFrame struct {
	Type     string `json:"type"`
	StreamID string `json:"streamId"`
	Data     string `json:"data"`
}

type hwpStreamResizeFrame struct {
	Type     string `json:"type"`
	StreamID string `json:"streamId"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type hwpStreamCloseFrame struct {
	Type     string `json:"type"`
	StreamID string `json:"streamId"`
	Reason   string `json:"reason,omitempty"`
}

type hwpStreamOpenedFrame struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	StreamID  string `json:"streamId"`
}

type hwpStreamChunkFrame struct {
	Type     string `json:"type"`
	StreamID string `json:"streamId"`
	Data     string `json:"data"`
	EOF      bool   `json:"eof,omitempty"`
}

type hwpStreamErrorFrame struct {
	Type      string         `json:"type"`
	RequestID string         `json:"requestId,omitempty"`
	StreamID  string         `json:"streamId,omitempty"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
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
	streams map[string]hostWorkerStream
}

type hostWorkerStream struct {
	cancel context.CancelFunc
	vmName string
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
		streams: make(map[string]hostWorkerStream),
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
		session.cancelAllStreams()
		_ = conn.Close()
		if err == nil {
			return fmt.Errorf("host worker closed")
		}
		return err
	case <-ctx.Done():
		cancelHeartbeat()
		session.cancelAllAssigns()
		session.cancelAllStreams()
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

func (s *hostWorkerConn) cancelAllStreams() {
	s.mu.Lock()
	streams := make(map[string]hostWorkerStream, len(s.streams))
	for id, stream := range s.streams {
		streams[id] = stream
		delete(s.streams, id)
	}
	s.mu.Unlock()
	for id, stream := range streams {
		stream.cancel()
		s.host.CloseHostStream(id)
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
		case "stream_open":
			var frame hwpStreamOpenFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				return err
			}
			go s.handleStreamOpen(frame)
		case "stream_input":
			var frame hwpStreamInputFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				return err
			}
			s.handleStreamInput(frame)
		case "stream_resize":
			var frame hwpStreamResizeFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				return err
			}
			s.handleStreamResize(frame)
		case "stream_close":
			var frame hwpStreamCloseFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				return err
			}
			s.handleStreamClose(frame)
		default:
			s.logger.Warn("ignored host worker frame", "type", envelope.Type)
		}
	}
}

func (s *hostWorkerConn) handleStreamOpen(frame hwpStreamOpenFrame) {
	args := frame.Args
	if args == nil {
		args = map[string]any{}
	}
	vmName, _ := args["vmName"].(string)
	if vmName == "" {
		_ = s.writeJSON(hwpStreamErrorFrame{
			Type: "stream_error", RequestID: frame.RequestID,
			Code: "invalid_arguments", Message: "vmName is required",
		})
		return
	}
	_, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if priorID, ok := s.streamByVM(vmName); ok {
		if prior, exists := s.streams[priorID]; exists {
			prior.cancel()
			delete(s.streams, priorID)
			s.host.CloseHostStream(priorID)
		}
	}
	streamID := fmt.Sprintf("hwp-stream-%d", time.Now().UnixNano())
	s.streams[streamID] = hostWorkerStream{cancel: cancel, vmName: vmName}
	s.mu.Unlock()

	onData := func(chunk string) {
		if chunk == "" {
			return
		}
		_ = s.writeJSON(hwpStreamChunkFrame{Type: "stream_chunk", StreamID: streamID, Data: chunk})
	}
	onClose := func(reason string) {
		s.mu.Lock()
		if _, exists := s.streams[streamID]; exists {
			delete(s.streams, streamID)
		}
		s.mu.Unlock()
		_ = s.writeJSON(hwpStreamChunkFrame{Type: "stream_chunk", StreamID: streamID, EOF: true})
		_ = s.writeJSON(hwpStreamCloseFrame{Type: "stream_close", StreamID: streamID, Reason: reason})
	}

	if err := s.host.OpenHostStreamWithClose(streamID, frame.Action, args, onData, onClose); err != nil {
		s.mu.Lock()
		delete(s.streams, streamID)
		s.mu.Unlock()
		cancel()
		_ = s.writeJSON(hwpStreamErrorFrame{
			Type: "stream_error", RequestID: frame.RequestID,
			Code: "stream_open_failed", Message: err.Error(),
		})
		return
	}
	_ = s.writeJSON(hwpStreamOpenedFrame{Type: "stream_opened", RequestID: frame.RequestID, StreamID: streamID})
}

func (s *hostWorkerConn) streamByVM(vmName string) (string, bool) {
	for id, stream := range s.streams {
		if stream.vmName == vmName {
			return id, true
		}
	}
	return "", false
}

func (s *hostWorkerConn) findStream(streamID string) (hostWorkerStream, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[streamID]
	return stream, ok
}

func (s *hostWorkerConn) handleStreamInput(frame hwpStreamInputFrame) {
	if _, ok := s.findStream(frame.StreamID); !ok {
		_ = s.writeJSON(hwpStreamErrorFrame{
			Type: "stream_error", StreamID: frame.StreamID,
			Code: "session_not_found", Message: "host worker stream is not active",
		})
		return
	}
	if err := s.host.SendHostStreamInput(frame.StreamID, frame.Data); err != nil {
		_ = s.writeJSON(hwpStreamErrorFrame{
			Type: "stream_error", StreamID: frame.StreamID,
			Code: "session_not_found", Message: err.Error(),
		})
	}
}

func (s *hostWorkerConn) handleStreamResize(frame hwpStreamResizeFrame) {
	if _, ok := s.findStream(frame.StreamID); !ok {
		_ = s.writeJSON(hwpStreamErrorFrame{
			Type: "stream_error", StreamID: frame.StreamID,
			Code: "session_not_found", Message: "host worker stream is not active",
		})
		return
	}
	if err := s.host.ResizeHostStream(frame.StreamID, frame.Width, frame.Height); err != nil {
		_ = s.writeJSON(hwpStreamErrorFrame{
			Type: "stream_error", StreamID: frame.StreamID,
			Code: "session_not_found", Message: err.Error(),
		})
	}
}

func (s *hostWorkerConn) handleStreamClose(frame hwpStreamCloseFrame) {
	stream, ok := s.findStream(frame.StreamID)
	if !ok {
		_ = s.writeJSON(hwpStreamErrorFrame{
			Type: "stream_error", StreamID: frame.StreamID,
			Code: "session_not_found", Message: "host worker stream is not active",
		})
		return
	}
	stream.cancel()
	s.mu.Lock()
	delete(s.streams, frame.StreamID)
	s.mu.Unlock()
	s.host.CloseHostStream(frame.StreamID)
	_ = s.writeJSON(hwpStreamCloseFrame{Type: "stream_close", StreamID: frame.StreamID, Reason: frame.Reason})
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
		structured, err := structuredContentMap(result.StructuredContent)
		if err != nil {
			_ = s.writeJSON(map[string]any{
				"type":        "fail",
				"operationId": frame.OperationID,
				"message":     fmt.Sprintf("serialize structured result: %v", err),
			})
			return
		}
		payload = structured
	}
	_ = s.writeJSON(map[string]any{
		"type":        "complete",
		"operationId": frame.OperationID,
		"result":      payload,
	})
}

// structuredContentMap normalizes both map-based and typed MCP structured
// results before they cross the host-worker boundary.  The wire contract is
// JSON, so rejecting typed structs here would silently turn successful host
// operations into completed operations with an empty result.
func structuredContentMap(value any) (map[string]any, error) {
	if structured, ok := value.(map[string]any); ok {
		return structured, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	structured := map[string]any{}
	if err := json.Unmarshal(encoded, &structured); err != nil {
		return nil, err
	}
	return structured, nil
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
