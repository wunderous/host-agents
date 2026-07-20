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
		if strings.HasPrefix(key, "OPUTE_") || key == "MCP_AUTH_TOKEN" || key == "BRIDGE_TOKEN" {
			continue
		}
		env = append(env, assignment)
	}
	env = append(env,
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
		SessionHeader         string `json:"sessionHeader"`
		ProtocolVersionHeader string `json:"protocolVersionHeader"`
		ProtocolVersion       string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(fixtureRaw, &fixture); err != nil {
		t.Fatal(err)
	}
	initializeBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": fixture.ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "standalone-fixture-test", "version": "1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var rawResponse *http.Response
	for {
		rawRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(initializeBody))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		rawRequest.Header.Set("Accept", fixture.Accept)
		rawRequest.Header.Set("Content-Type", "application/json")
		rawRequest.Header.Set(fixture.ProtocolVersionHeader, fixture.ProtocolVersion)
		rawResponse, err = http.DefaultClient.Do(rawRequest)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture initialize: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if rawResponse.StatusCode != http.StatusOK {
		rawResponse.Body.Close()
		t.Fatalf("fixture initialize status = %d", rawResponse.StatusCode)
	}
	if rawResponse.Header.Get(fixture.SessionHeader) == "" {
		rawResponse.Body.Close()
		t.Fatalf("fixture initialize missing %s response header", fixture.SessionHeader)
	}
	rawResponse.Body.Close()

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

	read, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "check_local_prerequisites", Arguments: map[string]any{}})
	if err != nil || read == nil || read.IsError || read.StructuredContent == nil {
		t.Fatalf("read-only prerequisite check failed: result=%+v err=%v", read, err)
	}
	denied, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_vm", Arguments: map[string]any{"vmName": "opute-standalone-contract-test"}})
	if err != nil || denied == nil || !denied.IsError {
		t.Fatalf("mutation was not denied: result=%+v err=%v", denied, err)
	}
}
