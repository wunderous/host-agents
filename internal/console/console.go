package console

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type CommandFactory func(vmName string) (*exec.Cmd, error)

type streamSession struct {
	operationID string
	vmName      string
	pty         *os.File
	cmd         *exec.Cmd
	mu          sync.Mutex
}

// Runtime manages active VM console streams.
type Runtime struct {
	mu sync.Mutex
	// operationId -> active PTY session
	streams map[string]*streamSession
	// vmName -> active PTY session for VM-scoped exclusivity
	byVM           map[string]*streamSession
	commandFactory CommandFactory
}

func NewRuntime(commandFactory ...CommandFactory) *Runtime {
	var factory CommandFactory
	if len(commandFactory) > 0 {
		factory = commandFactory[0]
	}
	return &Runtime{
		streams:        make(map[string]*streamSession),
		byVM:           make(map[string]*streamSession),
		commandFactory: factory,
	}
}

func (r *Runtime) AbortAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, session := range r.streams {
		r.closeSessionLocked(id, session)
		delete(r.streams, id)
		delete(r.byVM, session.vmName)
	}
}

func (r *Runtime) closeSessionLocked(_ string, session *streamSession) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.pty != nil {
		_ = session.pty.Close()
	}
	if session.cmd != nil && session.cmd.Process != nil {
		_ = session.cmd.Process.Kill()
	}
}

func (r *Runtime) open(vmName, operationID string, onData, onClose func(string)) error {
	if r.commandFactory == nil {
		return errors.New("host console PTY command factory is not configured")
	}
	cmd, err := r.commandFactory(vmName)
	if err != nil {
		return err
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("start VM console PTY: %w", err)
	}
	session := &streamSession{operationID: operationID, vmName: vmName, pty: ptmx, cmd: cmd}
	r.mu.Lock()
	if prior, ok := r.streams[operationID]; ok {
		r.closeSessionLocked(operationID, prior)
	}
	if prior, ok := r.byVM[vmName]; ok {
		delete(r.streams, prior.operationID)
		r.closeSessionLocked(prior.operationID, prior)
	}
	r.streams[operationID] = session
	r.byVM[vmName] = session
	r.mu.Unlock()

	go func() {
		buffer := make([]byte, 4096)
		for {
			count, readErr := ptmx.Read(buffer)
			if count > 0 && onData != nil {
				onData(string(buffer[:count]))
			}
			if readErr != nil {
				r.mu.Lock()
				if r.streams[operationID] == session {
					delete(r.streams, operationID)
					if r.byVM[session.vmName] == session {
						delete(r.byVM, session.vmName)
					}
				}
				r.mu.Unlock()
				_ = cmd.Wait()
				_ = ptmx.Close()
				if onClose != nil {
					onClose("pty_closed")
				}
				return
			}
		}
	}()
	return nil
}

// OpenVMStream starts a host-worker-owned PTY and forwards output to onData.
func (r *Runtime) OpenVMStream(vmName, operationID string, onData func(string)) error {
	if vmName == "" {
		return fmt.Errorf("vmName is required")
	}
	if operationID == "" {
		return fmt.Errorf("operationId is required")
	}
	return r.open(vmName, operationID, onData, nil)
}

// OpenVMStreamWithClose starts a PTY stream and reports EOF/process closure.
func (r *Runtime) OpenVMStreamWithClose(vmName, operationID string, onData, onClose func(string)) error {
	if vmName == "" {
		return fmt.Errorf("vmName is required")
	}
	if operationID == "" {
		return fmt.Errorf("operationId is required")
	}
	return r.open(vmName, operationID, onData, onClose)
}

// CloseStream terminates an active PTY stream. It is idempotent for callers
// handling browser disconnects and HWP reconnect cleanup.
func (r *Runtime) CloseStream(operationID string) {
	r.mu.Lock()
	session := r.streams[operationID]
	if session != nil {
		delete(r.streams, operationID)
		if r.byVM[session.vmName] == session {
			delete(r.byVM, session.vmName)
		}
		r.closeSessionLocked(operationID, session)
	}
	r.mu.Unlock()
}

// StreamVMConsole starts a real Incus-backed PTY stream.
func (r *Runtime) StreamVMConsole(vmName, operationID string) (*mcp.CallToolResult, error) {
	if vmName == "" {
		return nil, fmt.Errorf("vmName is required")
	}
	if operationID == "" {
		operationID = fmt.Sprintf("console-%s", vmName)
	}
	if err := r.open(vmName, operationID, nil, nil); err != nil {
		return nil, err
	}
	payload := map[string]any{
		"vmName":      vmName,
		"status":      "started",
		"operationId": operationID,
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Console stream started for VM '%s'.", vmName)}},
		StructuredContent: payload,
	}, nil
}

func (r *Runtime) SendConsoleInput(operationID, data string) (*mcp.CallToolResult, error) {
	if operationID == "" {
		return nil, fmt.Errorf("operationId is required")
	}
	r.mu.Lock()
	session := r.streams[operationID]
	r.mu.Unlock()
	if session == nil {
		return nil, fmt.Errorf("console stream not found: %s", operationID)
	}
	session.mu.Lock()
	_, err := session.pty.Write([]byte(data))
	session.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("write console input: %w", err)
	}
	payload := map[string]any{"status": "sent", "operationId": operationID}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: "Input sent."}},
		StructuredContent: payload,
	}, nil
}

func (r *Runtime) ResizeConsole(operationID string, width, height int) (*mcp.CallToolResult, error) {
	if operationID == "" {
		return nil, fmt.Errorf("operationId is required")
	}
	r.mu.Lock()
	session := r.streams[operationID]
	r.mu.Unlock()
	if session == nil {
		return nil, fmt.Errorf("console stream not found: %s", operationID)
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("console size must be positive")
	}
	session.mu.Lock()
	err := pty.Setsize(session.pty, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
	session.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("resize console: %w", err)
	}
	payload := map[string]any{"status": "resized", "operationId": operationID, "width": width, "height": height}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Resized console to %dx%d.", width, height)}},
		StructuredContent: payload,
	}, nil
}
