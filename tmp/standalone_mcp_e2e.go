//go:build standalonee2e

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	output *bufio.Scanner
	debug  *os.File
	nextID atomic.Int64
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "standalone E2E FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("standalone E2E PASSED")
}

func run() error {
	repo, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	binary := filepath.Join(repo, "dist", "host-agent-linux-x64-standalone")
	wslBinary := wslPath(binary)
	stateDir := fmt.Sprintf("/tmp/opute-standalone-e2e-%d", os.Getpid())
	cmd := exec.Command("wsl.exe", "-u", "opute", "--", "env",
		"OPUTE_AGENT_MODE=standalone",
		"OPUTE_TRANSPORT=stdio",
		"OPUTE_STANDALONE_ALLOW_MUTATIONS=true",
		"OPUTE_STANDALONE_ALLOW_INSECURE_DOWNLOADS=true",
		"OPUTE_STANDALONE_STATE_DIR="+stateDir,
		wslBinary, "--mode=standalone", "--transport=stdio")
	debug, _ := os.OpenFile("tmp/e2e-debug.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	defer debug.Close()
	cmd.Stderr = debug
	fmt.Fprintln(debug, "starting wsl agent")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Fprintln(debug, "wsl agent started")
	c := &client{cmd: cmd, stdin: stdin, output: bufio.NewScanner(stdout), debug: debug}
	c.output.Buffer(make([]byte, 64*1024), 4*1024*1024)
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	if _, err := c.request("initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "standalone-e2e", "version": "0.1.0"},
	}); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if err := c.notify("notifications/initialized", map[string]any{}); err != nil {
		return err
	}
	tools, err := c.request("tools/list", map[string]any{})
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	for _, required := range []string{"provision_vm", "install_k3s", "get_k3s_status", "install_postgresql", "run_sql", "create_cloudflare_tunnel", "get_operation"} {
		if !containsTool(tools, required) {
			return fmt.Errorf("standalone catalog missing %s", required)
		}
	}

	if _, err := c.callTool("check_local_prerequisites", nil); err != nil {
		return fmt.Errorf("prerequisites: %w", err)
	}
	vmName := fmt.Sprintf("opute-standalone-e2e-%d", time.Now().Unix())
	python := exec.Command("wsl.exe", "-u", "opute", "--", "python3", "-m", "http.server", "18080", "--bind", "0.0.0.0", "--directory", "/tmp")
	if err := python.Start(); err != nil {
		return fmt.Errorf("start local tunnel target: %w", err)
	}
	defer func() { _ = python.Process.Kill(); _ = python.Wait() }()
	if os.Getenv("STANDALONE_TUNNEL_ONLY") == "true" {
		tunnel, err := c.callTask("create_cloudflare_tunnel", map[string]any{"bindingId": "standalone-e2e", "localTarget": "http://127.0.0.1:18080", "quick": true})
		if err != nil {
			return fmt.Errorf("create quick tunnel: %w", err)
		}
		publicURL, _ := tunnel["publicUrl"].(string)
		if publicURL == "" {
			return fmt.Errorf("quick tunnel did not return a public URL: %s", compact(tunnel))
		}
		response, err := (&http.Client{Timeout: 20 * time.Second}).Get(publicURL)
		if err != nil {
			return fmt.Errorf("request public tunnel URL: %w", err)
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 500 {
			return fmt.Errorf("public tunnel URL returned HTTP %d", response.StatusCode)
		}
		if _, err := c.callTask("delete_cloudflare_tunnel", map[string]any{"bindingId": "standalone-e2e"}); err != nil {
			return fmt.Errorf("delete quick tunnel: %w", err)
		}
		fmt.Printf("tunnel-only PASSED: %s HTTP %d\n", publicURL, response.StatusCode)
		return nil
	}

	cleanup := func() {
		if _, err := c.callTask("delete_cloudflare_tunnel", map[string]any{"bindingId": "standalone-e2e"}); err != nil {
			fmt.Fprintf(os.Stderr, "cleanup tunnel: %v\n", err)
		}
		if _, err := c.callTask("delete_postgresql", map[string]any{"vmName": vmName, "namespace": "opute-local-db"}); err != nil {
			fmt.Fprintf(os.Stderr, "cleanup postgres: %v\n", err)
		}
		if _, err := c.callTask("uninstall_k3s", map[string]any{"vmName": vmName, "target": "vm"}); err != nil {
			fmt.Fprintf(os.Stderr, "cleanup k3s: %v\n", err)
		}
		if _, err := c.callTask("delete_vm", map[string]any{"vmName": vmName}); err != nil {
			fmt.Fprintf(os.Stderr, "cleanup vm: %v\n", err)
		}
	}
	defer cleanup()

	if _, err := c.callTask("provision_vm", map[string]any{"vmName": vmName, "image": "images:ubuntu/22.04", "cpus": 2, "memory": "2GiB", "disk": "12GiB"}); err != nil {
		return fmt.Errorf("provision VM: %w", err)
	}
	if _, err := c.callTask("install_k3s", map[string]any{"vmName": vmName, "target": "vm"}); err != nil {
		return fmt.Errorf("install K3s: %w", err)
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		status, err := c.callTool("get_k3s_status", map[string]any{"vmName": vmName})
		if err != nil {
			return fmt.Errorf("K3s status: %w", err)
		}
		fmt.Printf("K3s status: %s\n", compact(status))
		if ready, _ := status["status"].(string); ready == "ready" {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("K3s did not report ready: %v", status)
		}
		time.Sleep(5 * time.Second)
	}

	if _, err := c.callTask("install_postgresql", map[string]any{"vmName": vmName, "namespace": "opute-local-db", "database": "app", "password": "StandaloneE2e-2026!"}); err != nil {
		return fmt.Errorf("install PostgreSQL: %w", err)
	}
	if _, err := c.callTool("get_postgresql_status", map[string]any{"vmName": vmName, "namespace": "opute-local-db"}); err != nil {
		return fmt.Errorf("PostgreSQL status: %w", err)
	}
	sqlResult, err := c.callTool("run_sql", map[string]any{"vmName": vmName, "namespace": "opute-local-db", "database": "app", "sql": "CREATE TABLE IF NOT EXISTS e2e_probe (id integer primary key, note text); INSERT INTO e2e_probe(id, note) VALUES (1, 'standalone-ok') ON CONFLICT (id) DO UPDATE SET note = EXCLUDED.note; SELECT id || ':' || note FROM e2e_probe WHERE id = 1;"})
	if err != nil {
		return fmt.Errorf("run SQL: %w", err)
	}
	fmt.Printf("SQL result: %s\n", compact(sqlResult))

	tunnel, err := c.callTask("create_cloudflare_tunnel", map[string]any{"bindingId": "standalone-e2e", "localTarget": "http://127.0.0.1:18080", "quick": true})
	if err != nil {
		return fmt.Errorf("create quick tunnel: %w", err)
	}
	fmt.Printf("Tunnel result: %s\n", compact(tunnel))
	publicURL, _ := tunnel["publicUrl"].(string)
	if publicURL == "" {
		return fmt.Errorf("quick tunnel did not return a public URL")
	}
	response, err := (&http.Client{Timeout: 20 * time.Second}).Get(publicURL)
	if err != nil {
		return fmt.Errorf("request public tunnel URL: %w", err)
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 500 {
		return fmt.Errorf("public tunnel URL returned HTTP %d", response.StatusCode)
	}
	if _, err := c.callTool("get_cloudflare_tunnel_status", map[string]any{"bindingId": "standalone-e2e", "localTarget": "http://127.0.0.1:18080"}); err != nil {
		return fmt.Errorf("tunnel status: %w", err)
	}
	return nil
}

func (c *client) notify(method string, params any) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	_, err := fmt.Fprintf(c.stdin, "%s\n", body)
	return err
}

func (c *client) request(method string, params any) (map[string]any, error) {
	id := c.nextID.Add(1)
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := fmt.Fprintf(c.stdin, "%s\n", body); err != nil {
		return nil, err
	}
	fmt.Fprintf(c.debug, "sent %s\n", body)
	for c.output.Scan() {
		line := c.output.Bytes()
		fmt.Fprintf(c.debug, "received %s\n", line)
		var response rpcResponse
		if json.Unmarshal(line, &response) != nil {
			continue
		}
		if string(response.ID) != fmt.Sprintf("%d", id) {
			continue
		}
		if response.Error != nil {
			return nil, errors.New(response.Error.Message)
		}
		if len(response.Result) == 0 || string(response.Result) == "null" {
			return map[string]any{}, nil
		}
		var result map[string]any
		if err := json.Unmarshal(response.Result, &result); err != nil {
			return nil, err
		}
		if structured, ok := result["structuredContent"].(map[string]any); ok {
			if isError, _ := result["isError"].(bool); isError {
				return nil, fmt.Errorf("tool error: %s", compact(structured))
			}
			return structured, nil
		}
		return result, nil
	}
	if err := c.output.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (c *client) callTool(name string, arguments map[string]any) (map[string]any, error) {
	return c.request("tools/call", map[string]any{"name": name, "arguments": arguments})
}

func (c *client) callTask(name string, arguments map[string]any) (map[string]any, error) {
	started, err := c.callTool(name, arguments)
	if err != nil {
		return nil, err
	}
	taskID, _ := started["taskId"].(string)
	if taskID == "" {
		return started, nil
	}
	deadline := time.Now().Add(25 * time.Minute)
	for time.Now().Before(deadline) {
		operation, err := c.callTool("get_operation", map[string]any{"operationId": taskID})
		if err != nil {
			return nil, err
		}
		status, _ := operation["status"].(string)
		fmt.Printf("%s %s: %s\n", name, status, compact(operation))
		switch status {
		case "completed":
			if result, ok := operation["result"].(map[string]any); ok {
				if structured, ok := result["StructuredContent"].(map[string]any); ok {
					return structured, nil
				}
				if structured, ok := result["structuredContent"].(map[string]any); ok {
					return structured, nil
				}
			}
			return operation, nil
		case "failed", "cancelled", "unknown":
			return nil, fmt.Errorf("operation %s ended %s: %s", taskID, status, compact(operation))
		}
		time.Sleep(3 * time.Second)
	}
	return nil, fmt.Errorf("operation %s timed out", taskID)
}

func containsTool(raw map[string]any, name string) bool {
	items, _ := raw["tools"].([]any)
	for _, item := range items {
		if tool, ok := item.(map[string]any); ok && tool["name"] == name {
			return true
		}
	}
	return false
}

func compact(value any) string {
	b, _ := json.Marshal(value)
	return string(bytes.TrimSpace(b))
}

func wslPath(path string) string {
	volume := filepath.VolumeName(path)
	if len(volume) != 2 {
		return path
	}
	return "/mnt/" + strings.ToLower(volume[:1]) + strings.ReplaceAll(filepath.ToSlash(strings.TrimPrefix(path, volume)), "\\", "/")
}
