package modes_test

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
	"testing"
	"time"
)

func TestPackagedSingleBinaryAllModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the isolated Incus fixture uses a POSIX executable")
	}

	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Clean(filepath.Join(root, "..", ".."))
	binary := filepath.Join(t.TempDir(), "opute-host-agent")
	build := exec.Command("go", "build", "-o", binary, "./cmd/opute-host-agent")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build single binary: %v\n%s", err, output)
	}

	incusPath := writeIncusFixture(t)
	commands := strings.Join([]string{
		"/context",
		"get_vm_info vmName=worker-01 fast=true",
		"/exit",
		"",
	}, "\n")

	// The combined mode must not bind HTTP. Holding its configured port open
	// makes an accidental listener startup fail while the in-process MCP path
	// continues to exercise the real initialize/tools/list/tools/call protocol.
	occupiedListener, occupied := reservePort(t)
	defer occupiedListener.Close()
	combinedEnv := hostEnv(t, incusPath, t.TempDir(), t.TempDir())
	combinedEnv = append(combinedEnv, "HOST_MCP_PORT="+occupied)
	combinedOutput, err := runBinary(t, repoRoot, binary, combinedEnv,
		[]string{"--no-prompt", "--plan-dir", t.TempDir()}, commands)
	if err != nil {
		t.Fatalf("combined standalone mode: %v\n%s", err, combinedOutput)
	}
	assertModeOutput(t, "combined standalone", combinedOutput)

	serverPort := freePort(t)
	serverEnv := hostEnv(t, incusPath, t.TempDir(), t.TempDir())
	serverEnv = append(serverEnv,
		"HOST_MCP_BIND_HOST=127.0.0.1",
		"HOST_MCP_PORT="+serverPort,
	)
	serverCtx, cancelServer := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancelServer()
	serverCmd := exec.CommandContext(serverCtx, binary, "serve", "--mode", "standalone")
	serverCmd.Dir = repoRoot
	serverCmd.Env = serverEnv
	var serverOutput bytes.Buffer
	serverCmd.Stdout = &serverOutput
	serverCmd.Stderr = &serverOutput
	if err := serverCmd.Start(); err != nil {
		t.Fatalf("start server-only mode: %v", err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- serverCmd.Wait() }()
	t.Cleanup(func() {
		_ = serverCmd.Process.Signal(os.Interrupt)
		select {
		case <-serverDone:
		case <-time.After(5 * time.Second):
			_ = serverCmd.Process.Kill()
			<-serverDone
		}
	})
	endpoint := "http://127.0.0.1:" + serverPort + "/mcp"
	waitForTCP(t, serverCtx, "127.0.0.1:"+serverPort)

	attachedEnv := hostEnv(t, incusPath, t.TempDir(), t.TempDir())
	attachedOutput, err := runBinary(t, repoRoot, binary, attachedEnv,
		[]string{"tui", "--url", endpoint, "--no-prompt"}, commands)
	if err != nil {
		t.Fatalf("attached TUI mode: %v\n%s\nserver:\n%s", err, attachedOutput, serverOutput.String())
	}
	assertModeOutput(t, "attached TUI", attachedOutput)
	if !strings.Contains(serverOutput.String(), "HTTP transport listening") {
		t.Fatalf("server-only mode did not report its HTTP listener:\n%s", serverOutput.String())
	}
}

func writeIncusFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "incus")
	content := `#!/bin/sh
if [ "$1" = "list" ]; then
  printf '%s\n' '[{"name":"worker-01","status":"Running","type":"container","state":{"network":{"eth0":{"addresses":[{"address":"192.0.2.11","family":"inet","scope":"global"}]}}}},{"name":"worker-02","status":"Stopped","type":"container","state":{"network":{"eth0":{"addresses":[{"address":"192.0.2.12","family":"inet","scope":"global"}]}}}}]'
  exit 0
fi
printf '%s\n' 'unsupported isolated Incus fixture command' >&2
exit 1
`
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func hostEnv(t *testing.T, incusPath, stateDir, lockDir string) []string {
	t.Helper()
	env := make([]string, 0, len(os.Environ()))
	for _, assignment := range os.Environ() {
		key, _, _ := strings.Cut(assignment, "=")
		if strings.HasPrefix(key, "OPUTE_") || key == "MCP_AUTH_TOKEN" || key == "HOST_MCP_PORT" || key == "HOST_MCP_BIND_HOST" {
			continue
		}
		env = append(env, assignment)
	}
	return append(env,
		"OPUTE_INFRA_PROVIDER_ID=incus",
		"OPUTE_INCUS_BINARY_PATH="+incusPath,
		"OPUTE_STANDALONE_STATE_DIR="+stateDir,
		"OPUTE_HOST_RESOURCE_LOCK_DIR="+lockDir,
		"PATH="+filepath.Dir(incusPath)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
}

func runBinary(t *testing.T, repoRoot, binary string, env []string, args []string, input string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = repoRoot
	command.Env = env
	command.Stdin = strings.NewReader(input)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	return output.String(), err
}

func assertModeOutput(t *testing.T, mode, output string) {
	t.Helper()
	for _, expected := range []string{
		"opute-host-agent connected",
		"catalog revision:",
		"worker-01",
		"bye",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("%s output missing %q:\n%s", mode, expected, output)
		}
	}
}

func reservePort(t *testing.T) (net.Listener, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	return listener, fmt.Sprintf("%d", port)
}

func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return fmt.Sprintf("%d", listener.Addr().(*net.TCPAddr).Port)
}

func waitForTCP(t *testing.T, ctx context.Context, address string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: %v", address, err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %s: %v", address, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
