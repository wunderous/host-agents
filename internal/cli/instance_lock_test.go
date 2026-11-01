package cli

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/instancelock"
)

// The agent processes below are this test binary re-run as the agent: the
// helper calls Run and reports an error exactly as cmd/opute-host-agent does
// (one stderr line, exit 1).
const agentHelperEnv = "OPUTE_TEST_INSTANCE_LOCK_AGENT"

func TestInstanceLockAgentHelper(t *testing.T) {
	if os.Getenv(agentHelperEnv) == "" {
		t.Skip("helper process")
	}
	args := strings.Fields(os.Getenv(agentHelperEnv))
	if err := Run(context.Background(), args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

type agentEnv struct {
	instanceRoot string
	stateDir     string
	port         int
}

func (e agentEnv) command(t *testing.T, args string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestInstanceLockAgentHelper$")
	home := t.TempDir()
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		agentHelperEnv + "=" + args,
		"OPUTE_AGENT_MODE=standalone",
		"OPUTE_REMOTE_AGENT_ID=instance-lock-test-agent",
		"OPUTE_INFRA_PROVIDER_ID=incus",
		"MCP_AUTH_TOKEN=instance-lock-test-token-0123456789abcdef",
		"OPUTE_HOST_AGENT_INSTANCE_ROOT=" + e.instanceRoot,
		"HOST_MCP_PORT=" + strconv.Itoa(e.port),
	}
	if e.stateDir != "" {
		cmd.Env = append(cmd.Env, "OPUTE_STANDALONE_STATE_DIR="+e.stateDir)
	}
	return cmd
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func portBound(port int) bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func refusal(path string) string {
	abs, _ := filepath.Abs(path)
	return "host agent instance is already in use: " + filepath.Join(abs, instancelock.FileName) +
		" is locked by another process"
}

// runRefused runs the agent and requires exit 1 with exactly the refusal line
// on stderr and nothing on stdout.
func runRefused(t *testing.T, cmd *exec.Cmd, lockedRoot string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 {
		t.Fatalf("agent exit = %v, want exit 1; stderr:\n%s", err, stderr.String())
	}
	if got, want := strings.TrimRight(stderr.String(), "\n"), refusal(lockedRoot); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

// entries lists what a refused agent left in dir.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(list))
	for _, entry := range list {
		names = append(names, entry.Name())
	}
	return names
}

func skipUnlessUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("agent process tests use the Unix serve path")
	}
}

func TestSecondAgentOnInstanceIsRefusedBeforeAnyStoreOrPort(t *testing.T) {
	skipUnlessUnix(t)
	env := agentEnv{instanceRoot: filepath.Join(t.TempDir(), "instance"), port: freePort(t)}
	held, err := instancelock.Acquire(env.instanceRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	runRefused(t, env.command(t, "serve"), env.instanceRoot)
	if got := entries(t, env.instanceRoot); len(got) != 1 || got[0] != instancelock.FileName {
		t.Fatalf("instance root after refusal = %v, want only %s", got, instancelock.FileName)
	}
	if portBound(env.port) {
		t.Fatalf("refused agent bound port %d", env.port)
	}
}

func TestSecondAgentIsRefusedWhileFirstKeepsServing(t *testing.T) {
	skipUnlessUnix(t)
	root := filepath.Join(t.TempDir(), "instance")
	first := agentEnv{instanceRoot: root, port: freePort(t)}
	cmd := first.command(t, "serve")
	var firstErr bytes.Buffer
	cmd.Stderr = &firstErr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		_ = cmd.Wait()
	}()
	deadline := time.Now().Add(20 * time.Second)
	for !portBound(first.port) {
		if time.Now().After(deadline) {
			t.Fatalf("first agent did not start serving; stderr:\n%s", firstErr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	second := agentEnv{instanceRoot: root, port: freePort(t)}
	runRefused(t, second.command(t, "serve"), root)
	if portBound(second.port) {
		t.Fatalf("refused agent bound port %d", second.port)
	}
	if !portBound(first.port) {
		t.Fatal("first agent stopped serving after the refusal")
	}
}

func TestStateDirectoryOutsideInstanceRootIsLocked(t *testing.T) {
	skipUnlessUnix(t)
	base := t.TempDir()
	env := agentEnv{instanceRoot: filepath.Join(base, "instance"), stateDir: filepath.Join(base, "state"),
		port: freePort(t)}
	held, err := instancelock.Acquire(env.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	runRefused(t, env.command(t, "serve"), env.stateDir)
	if got := entries(t, env.stateDir); len(got) != 1 || got[0] != instancelock.FileName {
		t.Fatalf("state directory after refusal = %v, want only %s", got, instancelock.FileName)
	}
	// The instance root is locked first and released on refusal; it keeps
	// only its lock file.
	if got := entries(t, env.instanceRoot); len(got) != 1 || got[0] != instancelock.FileName {
		t.Fatalf("instance root after refusal = %v, want only %s", got, instancelock.FileName)
	}
}

func TestCheckRefusesWhileInstanceIsLocked(t *testing.T) {
	skipUnlessUnix(t)
	env := agentEnv{instanceRoot: filepath.Join(t.TempDir(), "instance"), port: freePort(t)}
	held, err := instancelock.Acquire(env.instanceRoot)
	if err != nil {
		t.Fatal(err)
	}
	runRefused(t, env.command(t, "serve --check"), env.instanceRoot)
	held.Release()

	out, err := env.command(t, "serve --check").Output()
	if err != nil || strings.TrimSpace(string(out)) != "configuration ok" {
		t.Fatalf("check after release = %q, %v; want configuration ok", out, err)
	}
}
