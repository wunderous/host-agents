package standalone_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPackagedShapeStandaloneStdioContract(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("standalone Incus agent is Linux-only; Windows clients use WSL")
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
		"OPUTE_TRANSPORT=stdio",
		"OPUTE_INFRA_PROVIDER_ID=incus",
		"OPUTE_STANDALONE_STATE_DIR="+t.TempDir(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "standalone-contract-test", Version: "1"}, nil)
	cmd := exec.Command(binary)
	cmd.Env = env
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: 2 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
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
	for _, name := range []string{"check_local_prerequisites", "get_local_status", "list_vms", "create_vm", "get_operation"} {
		if !seen[name] {
			t.Fatalf("tools/list missing %q", name)
		}
	}
	for _, name := range []string{"register_host_agent", "host_agent_heartbeat", "dispatch_host_operation", "agent_shell"} {
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
