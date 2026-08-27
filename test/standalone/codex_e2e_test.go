package standalone_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/mcphttp"
)

func TestCodexWSLNonInteractiveE2E(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("WSL / Linux environment required")
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(t.TempDir(), "opute-host-agent")
	build := exec.Command("go", "build", "-o", binary, "./cmd/opute-host-agent")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build standalone binary: %v\n%s", err, output)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	stateDir := t.TempDir()
	env := make([]string, 0, len(os.Environ()))
	for _, assignment := range os.Environ() {
		key, _, _ := strings.Cut(assignment, "=")
		if strings.HasPrefix(key, "OPUTE_") || key == "MCP_AUTH_TOKEN" {
			continue
		}
		env = append(env, assignment)
	}
	authToken := "codex-e2e-token-12345"
	env = append(env,
		"OPUTE_REMOTE_AGENT_ID=codex-e2e-agent",
		"OPUTE_AGENT_MODE=standalone",
		"OPUTE_INFRA_PROVIDER_ID=incus",
		"OPUTE_STANDALONE_STATE_DIR="+stateDir,
		"HOST_MCP_BIND_HOST=127.0.0.1",
		fmt.Sprintf("HOST_MCP_PORT=%d", port),
		"MCP_AUTH_TOKEN="+authToken,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "--mode=standalone")
	cmd.Dir = root
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		t.Fatalf("start host agent: %v", err)
	}
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		_, _ = cmd.Process.Wait()
	}()

	endpoint := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	mcpClient := mcphttp.Client{Endpoint: endpoint, Token: authToken, Name: "codex-e2e-test", Version: "1"}

	// Wait for server ready by polling tools/list
	deadline := time.Now().Add(15 * time.Second)
	var listed map[string]any
	for time.Now().Before(deadline) {
		res, err := mcpClient.Call(ctx, "tools/list", "", map[string]any{})
		if err == nil {
			listed = res
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if listed == nil {
		t.Fatal("host agent failed to serve tools/list within deadline")
	}

	callTool := func(toolName string, args map[string]any) (map[string]any, error) {
		if args == nil {
			args = map[string]any{}
		}
		return mcpClient.Call(ctx, "tools/call", toolName, map[string]any{
			"name":      toolName,
			"arguments": args,
		})
	}

	// Step 1: server/discover
	discoverRes, err := mcpClient.Call(ctx, "server/discover", "", map[string]any{})
	if err != nil {
		t.Fatalf("server/discover failed: %v", err)
	}
	if discoverRes["resultType"] != "complete" {
		t.Fatalf("unexpected discovery result: %#v", discoverRes)
	}

	// Step 2: Validate tools/list returned tools
	toolsList, _ := listed["tools"].([]any)
	if len(toolsList) == 0 {
		t.Fatalf("tools/list returned empty list: %#v", listed)
	}

	// Step 3: Call get_host_info
	hostInfoRes, err := callTool("get_host_info", map[string]any{})
	if err != nil {
		t.Fatalf("tools/call get_host_info failed: %v", err)
	}
	if hostInfoRes == nil {
		t.Fatal("get_host_info returned nil result")
	}

	// Step 4: Call list_host_services
	servicesRes, err := callTool("list_host_services", map[string]any{"scope": "user"})
	if err != nil {
		t.Fatalf("tools/call list_host_services failed: %v", err)
	}
	if servicesRes == nil {
		t.Fatal("list_host_services returned nil result")
	}

	// Step 5: Call validate_host_plan
	planRes, err := callTool("validate_host_plan", map[string]any{
		"plan": map[string]any{
			"contractVersion": "host-plan.v1",
			"planId":          "e2e-test-plan",
			"generation":      1,
			"idempotencyKey":  "e2e-test-key-1",
			"nodes": []any{
				map[string]any{
					"id": "get-info",
					"action": map[string]any{
						"tool": "get_host_info",
						"args": map[string]any{},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("tools/call validate_host_plan failed: %v", err)
	}
	if planRes == nil {
		t.Fatal("validate_host_plan returned nil result")
	}
}
