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
)

func buildStandaloneBinary(t *testing.T) string {
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

func TestDeprecatedTransportFlagIsRejectOnly(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("standalone CLI validation is exercised on Linux/WSL")
	}
	binary := buildStandaloneBinary(t)
	baseEnv := []string{
		"OPUTE_AGENT_MODE=standalone",
		"OPUTE_INFRA_PROVIDER_ID=incus",
		"OPUTE_STANDALONE_STATE_DIR=" + t.TempDir(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	accepted := exec.CommandContext(ctx, binary, "--mode=standalone", "--transport=http", "--check")
	accepted.Env = append(os.Environ(), baseEnv...)
	if output, err := accepted.CombinedOutput(); err != nil {
		t.Fatalf("--transport=http --check failed: %v\n%s", err, output)
	}

	rejected := exec.CommandContext(ctx, binary, "--mode=standalone", "--transport=stdio", "--check")
	rejected.Env = append(os.Environ(), baseEnv...)
	output, err := rejected.CombinedOutput()
	if err == nil {
		t.Fatal("--transport=stdio unexpectedly succeeded")
	}
	if !strings.Contains(string(output), "Streamable HTTP") {
		t.Fatalf("stdio diagnostic = %q, want Streamable HTTP guidance", output)
	}
}
