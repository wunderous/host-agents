package standalone_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/mcphttp"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/schemas"
)

func TestPackagedShapeStandaloneHTTPContract(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("standalone Incus agent is Linux-only; Windows clients use WSL")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := strings.TrimSpace(os.Getenv("OPUTE_STANDALONE_BINARY"))
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "opute-host-agent")
		build := exec.Command("go", "build", "-o", binary, "./cmd/opute-host-agent")
		build.Dir = root
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build standalone binary: %v\n%s", err, output)
		}
	} else if _, err := os.Stat(binary); err != nil {
		t.Fatalf("OPUTE_STANDALONE_BINARY: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	env := make([]string, 0, len(os.Environ()))
	for _, assignment := range os.Environ() {
		key, _, _ := strings.Cut(assignment, "=")
		if strings.HasPrefix(key, "OPUTE_") || key == "MCP_AUTH_TOKEN" {
			continue
		}
		env = append(env, assignment)
	}
	env = append(env,
		"OPUTE_REMOTE_AGENT_ID=test-host-agent",
		"OPUTE_AGENT_MODE=standalone",
		"OPUTE_INFRA_PROVIDER_ID=incus",
		"OPUTE_STANDALONE_STATE_DIR="+t.TempDir(),
		"HOST_MCP_BIND_HOST=127.0.0.1",
		fmt.Sprintf("HOST_MCP_PORT=%d", port),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "--mode=standalone")
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		_, _ = cmd.Process.Wait()
	}()

	endpoint := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	deadline := time.Now().Add(15 * time.Second)
	fixtureRaw, err := schemas.FS.ReadFile("streamable-http-client.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Accept                string `json:"accept"`
		MethodHeader          string `json:"methodHeader"`
		ProtocolVersionHeader string `json:"protocolVersionHeader"`
		ProtocolVersion       string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(fixtureRaw, &fixture); err != nil {
		t.Fatal(err)
	}
	meta, err := mcphttp.ModernRequestEnvelope("1")
	if err != nil {
		t.Fatal(err)
	}
	listBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
		"params": map[string]any{"_meta": meta},
	})
	if err != nil {
		t.Fatal(err)
	}
	var listResponse *http.Response
	for {
		listRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(listBody))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		listRequest.Header.Set("Content-Type", "application/json")
		if err := mcphttp.ApplyStreamableHTTPRequestHeaders(listRequest); err != nil {
			t.Fatal(err)
		}
		if fixture.MethodHeader != "" {
			listRequest.Header.Set(fixture.MethodHeader, "tools/list")
		}
		listResponse, err = http.DefaultClient.Do(listRequest)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture tools/list: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if listResponse.StatusCode != http.StatusOK {
		listResponse.Body.Close()
		t.Fatalf("fixture tools/list status = %d", listResponse.StatusCode)
	}
	listResponse.Body.Close()

	var session *mcp.ClientSession
	client := mcp.NewClient(&mcp.Implementation{Name: "standalone-contract-test", Version: "1"}, nil)
	for {
		session, err = client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Connect: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer session.Close()

	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range list.Tools {
		seen[tool.Name] = true
	}
	contract, err := tools.LoadStandaloneToolContract()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range contract.Smoke.RequiredTools {
		if !seen[name] {
			t.Fatalf("tools/list missing %q", name)
		}
	}
	for _, name := range contract.Smoke.ForbiddenTools {
		if seen[name] {
			t.Fatalf("platform or shell tool leaked into standalone tools/list: %q", name)
		}
	}
	for _, tool := range list.Tools {
		if tool.Meta == nil {
			t.Fatalf("standalone tool %q is missing contract metadata", tool.Name)
		}
	}
	if !seen["request_task_input"] {
		t.Fatal("tools/list missing request_task_input")
	}
	callRaw := func(id int, method string, params map[string]any) map[string]any {
		t.Helper()
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if err := mcphttp.ApplyStreamableHTTPRequestHeaders(req); err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Mcp-Method", method)
		if method == "tools/call" {
			if err := mcphttp.ApplyToolsCallRequestHeaders(req, "request_task_input"); err != nil {
				t.Fatal(err)
			}
		} else if method == "tasks/get" || method == "tasks/update" {
			if taskID, ok := params["taskId"].(string); ok {
				req.Header.Set("Mcp-Name", taskID)
			}
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var envelope map[string]any
		if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		if envelope["error"] != nil {
			t.Fatalf("%s error: %#v", method, envelope["error"])
		}
		result, ok := envelope["result"].(map[string]any)
		if !ok {
			t.Fatalf("%s result = %#v", method, envelope)
		}
		return result
	}
	created := callRaw(11, "tools/call", map[string]any{
		"name":      "request_task_input",
		"arguments": map[string]any{"prompt": "Continue?", "responseType": "boolean"},
		"_meta":     meta,
	})
	if created["resultType"] != "task" {
		t.Fatalf("request_task_input resultType = %#v, want task", created["resultType"])
	}
	taskID, ok := created["taskId"].(string)
	if !ok || taskID == "" {
		t.Fatalf("request_task_input task identity = %#v", created)
	}
	get := callRaw(12, "tasks/get", map[string]any{"taskId": taskID, "_meta": meta})
	if get["status"] != "input_required" || get["inputRequests"] == nil {
		t.Fatalf("input-required task = %#v", get)
	}
	callRaw(13, "tasks/update", map[string]any{"taskId": taskID, "inputResponses": map[string]any{"response": true}, "_meta": meta})
	completed := callRaw(14, "tasks/get", map[string]any{"taskId": taskID, "_meta": meta})
	if completed["status"] != "completed" || completed["resultType"] != "complete" {
		t.Fatalf("completed task = %#v", completed)
	}
	if _, ok := completed["result"].(map[string]any); !ok {
		t.Fatalf("completed task omitted inline result = %#v", completed)
	}

	readOnly := ""
	for _, name := range contract.Smoke.RequiredTools {
		if name == "create_vm" || name == "get_operation" {
			continue
		}
		readOnly = name
		break
	}
	if readOnly == "" {
		t.Fatal("standalone smoke.requiredTools has no read-only probe tool")
	}
	read, err := session.CallTool(ctx, &mcp.CallToolParams{Name: readOnly, Arguments: map[string]any{}})
	if err != nil || read == nil || read.IsError || read.StructuredContent == nil {
		t.Fatalf("read-only smoke call %s failed: result=%+v err=%v", readOnly, read, err)
	}
	denied, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_vm", Arguments: map[string]any{"vmName": "opute-standalone-contract-test"}})
	if err != nil || denied == nil || !denied.IsError {
		t.Fatalf("mutation was not denied: result=%+v err=%v", denied, err)
	}
}
