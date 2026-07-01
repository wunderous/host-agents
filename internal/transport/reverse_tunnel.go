package transport

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/opute-io/host-agents/internal/hostmcp"
)

type wsOutboundTransport struct {
	conn *websocket.Conn
}

func (t *wsOutboundTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	return &wsOutboundConnection{conn: t.conn}, nil
}

type wsOutboundConnection struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	closed bool
}

func (c *wsOutboundConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	for {
		if c.closed {
			return nil, fmt.Errorf("connection closed")
		}
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		msg, err := jsonrpc.DecodeMessage(data)
		if err != nil {
			return nil, err
		}
		return msg, nil
	}
}

func (c *wsOutboundConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("connection closed")
	}
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

func (c *wsOutboundConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return c.conn.Close()
}

func (c *wsOutboundConnection) SessionID() string { return "" }

// RunReverseTunnelLoop maintains outbound tunnel with reconnect backoff.
func RunReverseTunnelLoop(ctx context.Context, host *hostmcp.Server, wsURL, agentID, authToken, healthURL string, logger *slog.Logger) {
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
				logger.Warn("aggregator not healthy", "err", err)
				time.Sleep(2 * time.Second)
				continue
			}
		}
		err := connectOnce(ctx, host, wsURL, agentID, authToken, logger)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logger.Warn("tunnel disconnected", "attempt", attempt, "err", err)
			host.AbortAllConsoleStreams()
			attempt++
			time.Sleep(2 * time.Second)
			continue
		}
	}
}

func connectOnce(ctx context.Context, host *hostmcp.Server, wsURL, agentID, authToken string, logger *slog.Logger) error {
	tunnelURL := BuildTunnelURL(wsURL, agentID)
	header := http.Header{}
	if authToken != "" {
		header.Set("Authorization", "Bearer "+authToken)
	}
	dialer := websocket.Dialer{Subprotocols: []string{"mcp"}}
	conn, _, err := dialer.DialContext(ctx, tunnelURL, header)
	if err != nil {
		return err
	}
	logger.Info("reverse tunnel connected", "url", tunnelURL)

	transport := &wsOutboundTransport{conn: conn}
	session, err := host.MCP().Connect(ctx, transport, nil)
	if err != nil {
		_ = conn.Close()
		return err
	}

	<-ctx.Done()
	_ = session.Close()
	_ = conn.Close()
	return ctx.Err()
}

func waitForHealth(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		res, err := http.DefaultClient.Do(req)
		if err == nil && res.StatusCode == http.StatusOK {
			res.Body.Close()
			return nil
		}
		if res != nil {
			res.Body.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("health check timeout for %s", url)
}

func BuildTunnelURL(wsBase, agentID string) string {
	root := strings.TrimRight(strings.TrimSpace(wsBase), "/")
	if idx := strings.Index(strings.ToLower(root), "/mcp-agent"); idx >= 0 {
		root = root[:idx]
	}
	return fmt.Sprintf("%s/mcp-agent/%s", root, agentID)
}
