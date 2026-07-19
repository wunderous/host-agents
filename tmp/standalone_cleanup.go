//go:build standalonee2e

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type cleanupRPC struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type cleanupClient struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	scan *bufio.Scanner
	next int
}

func main() {
	if err := cleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "standalone cleanup FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("standalone cleanup PASSED")
}

func cleanup() error {
	binary, _ := filepath.Abs("dist/host-agent-linux-x64-standalone")
	cmd := exec.Command("wsl.exe", "-u", "opute", "--", "env", "OPUTE_AGENT_MODE=standalone", "OPUTE_TRANSPORT=stdio", "OPUTE_STANDALONE_ALLOW_MUTATIONS=true", "OPUTE_STANDALONE_STATE_DIR=/tmp/opute-standalone-cleanup", wslPath(binary), "--mode=standalone", "--transport=stdio")
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	c := &cleanupClient{cmd: cmd, in: in, scan: bufio.NewScanner(out)}
	c.scan.Buffer(make([]byte, 64*1024), 4*1024*1024)
	defer func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if _, err := c.request("initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "cleanup", "version": "1"}}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(in, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`); err != nil {
		return err
	}
	list, err := c.call("list_vms", map[string]any{"fast": true})
	if err != nil {
		return err
	}
	vms, _ := list["vms"].([]any)
	for _, raw := range vms {
		vm, _ := raw.(map[string]any)
		name, _ := vm["name"].(string)
		if !strings.HasPrefix(name, "opute-standalone-e2e-") {
			continue
		}
		fmt.Printf("deleting %s\n", name)
		started, err := c.call("delete_vm", map[string]any{"vmName": name})
		if err != nil {
			return err
		}
		taskID, _ := started["taskId"].(string)
		if taskID == "" {
			continue
		}
		for deadline := time.Now().Add(5 * time.Minute); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
			operation, err := c.call("get_operation", map[string]any{"operationId": taskID})
			if err != nil {
				return err
			}
			status, _ := operation["status"].(string)
			if status == "completed" {
				break
			}
			if status == "failed" || status == "cancelled" || status == "unknown" {
				return fmt.Errorf("delete %s ended %s", name, status)
			}
		}
	}
	remaining, err := c.call("list_vms", map[string]any{"fast": true})
	if err != nil {
		return err
	}
	for _, raw := range remaining["vms"].([]any) {
		name, _ := raw.(map[string]any)["name"].(string)
		if strings.HasPrefix(name, "opute-standalone-e2e-") {
			return fmt.Errorf("disposable VM remains: %s", name)
		}
	}
	return nil
}

func (c *cleanupClient) request(method string, params any) (map[string]any, error) {
	c.next++
	id := c.next
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := fmt.Fprintf(c.in, "%s\n", body); err != nil {
		return nil, err
	}
	for c.scan.Scan() {
		var response cleanupRPC
		if json.Unmarshal(c.scan.Bytes(), &response) != nil || string(response.ID) != fmt.Sprintf("%d", id) {
			continue
		}
		if response.Error != nil {
			return nil, fmt.Errorf("%s", response.Error.Message)
		}
		var result map[string]any
		if err := json.Unmarshal(response.Result, &result); err != nil {
			return nil, err
		}
		if structured, ok := result["structuredContent"].(map[string]any); ok {
			return structured, nil
		}
		return result, nil
	}
	return nil, c.scan.Err()
}

func (c *cleanupClient) call(name string, args map[string]any) (map[string]any, error) {
	return c.request("tools/call", map[string]any{"name": name, "arguments": args})
}

func wslPath(path string) string {
	volume := filepath.VolumeName(path)
	return "/mnt/" + strings.ToLower(volume[:1]) + strings.ReplaceAll(filepath.ToSlash(strings.TrimPrefix(path, volume)), "\\", "/")
}
