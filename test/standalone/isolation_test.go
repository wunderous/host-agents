package standalone_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/state"
	"github.com/wunderous/host-agents/internal/tools"
)

func buildStandaloneIsolationBinary(t *testing.T) string {
	t.Helper()
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
	return binary
}

func standaloneCleanEnv(extra ...string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, assignment := range os.Environ() {
		key, _, _ := strings.Cut(assignment, "=")
		if strings.HasPrefix(key, "OPUTE_") || key == "MCP_AUTH_TOKEN" || key == "BRIDGE_TOKEN" {
			continue
		}
		env = append(env, assignment)
	}
	return append(env, extra...)
}

func freeStandalonePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestStandaloneHTTPIsolationAndShutdown(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("standalone Incus agent is Linux-only; Windows clients use WSL")
	}

	trap, err := net.Listen("tcp", "127.0.0.1:9091")
	if err != nil {
		t.Skipf("platform network trap port is unavailable: %v", err)
	}
	defer trap.Close()
	var trapHits atomic.Int64
	go func() {
		for {
			connection, acceptErr := trap.Accept()
			if acceptErr != nil {
				return
			}
			trapHits.Add(1)
			_ = connection.Close()
		}
	}()

	binary := buildStandaloneIsolationBinary(t)
	stateDir := t.TempDir()
	port := freeStandalonePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--mode=standalone")
	cmd.Env = standaloneCleanEnv(
		"OPUTE_AGENT_MODE=standalone",
		"OPUTE_INFRA_PROVIDER_ID=incus",
		"OPUTE_STANDALONE_STATE_DIR="+stateDir,
		"HOST_MCP_BIND_HOST=127.0.0.1",
		fmt.Sprintf("HOST_MCP_PORT=%d", port),
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "standalone-isolation-test", Version: "1"}, nil)
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	deadline := time.Now().Add(15 * time.Second)
	var session *mcp.ClientSession
	for {
		session, err = client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("standalone MCP connect: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	contract, err := tools.LoadStandaloneToolContract()
	if err != nil {
		t.Fatal(err)
	}
	readOnlySmoke := make([]string, 0, 3)
	for _, name := range contract.Smoke.RequiredTools {
		if name == "create_vm" || name == "get_operation" {
			continue
		}
		readOnlySmoke = append(readOnlySmoke, name)
		if len(readOnlySmoke) == 3 {
			break
		}
	}
	if len(readOnlySmoke) == 0 {
		t.Fatal("standalone smoke contract has no read-only required tools")
	}
	for _, name := range readOnlySmoke {
		result, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if callErr != nil || result == nil {
			t.Fatalf("read-only call %s did not produce an MCP response: result=%+v err=%v", name, result, callErr)
		}
		if result.IsError && name == "check_local_prerequisites" {
			t.Fatalf("prerequisite check returned an unexpected MCP error: %+v", result)
		}
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("standalone shutdown: %v", err)
	}

	reopened, err := state.Open(stateDir)
	if err != nil {
		t.Fatalf("reopen standalone state after shutdown: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	released, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("standalone listener was not released: %v", err)
	}
	_ = released.Close()
	if got := trapHits.Load(); got != 0 {
		t.Fatalf("standalone contacted platform network trap %d time(s)", got)
	}
}

func TestStandaloneInvalidConfigurationExitsBeforeMCP(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("standalone Incus agent is Linux-only; Windows clients use WSL")
	}

	binary := buildStandaloneIsolationBinary(t)
	cases := []struct {
		name  string
		extra []string
	}{
		{name: "invalid mode", extra: []string{"OPUTE_AGENT_MODE=standalonee"}},
		{name: "stdio transport", extra: []string{"OPUTE_AGENT_MODE=standalone", "OPUTE_TRANSPORT=stdio"}},
		{name: "reverse tunnel", extra: []string{"OPUTE_AGENT_MODE=standalone", "OPUTE_REVERSE_TUNNEL=true"}},
		{name: "platform URL", extra: []string{"OPUTE_AGENT_MODE=standalone", "OPUTE_MCP_URL=https://platform.example/mcp"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			port := freeStandalonePort(t)
			args := append([]string{}, testCase.extra...)
			args = append(args, "OPUTE_INFRA_PROVIDER_ID=incus", fmt.Sprintf("HOST_MCP_PORT=%d", port))
			cmd := exec.Command(binary, "--check")
			cmd.Env = standaloneCleanEnv(args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err == nil {
				t.Fatal("invalid configuration unexpectedly passed")
			}
			if stdout.Len() != 0 {
				t.Fatalf("invalid configuration emitted protocol/stdout data: %q", stdout.String())
			}
			if strings.TrimSpace(stderr.String()) == "" {
				t.Fatal("invalid configuration did not produce a diagnostic")
			}
			released, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				t.Fatalf("invalid configuration claimed listener port: %v", err)
			}
			_ = released.Close()
		})
	}
}
