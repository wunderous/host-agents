package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertEnvFileAppliesAssignmentsAndRemovalsAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host-agent.env")
	if err := os.WriteFile(path, []byte("KEEP=one\nREMOVE=stale\nOPUTE_MCP_URL=https://legacy.example/mcp\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := upsertEnvFile(path, map[string]string{
		"MCP_AUTH_TOKEN": "oha_reconciled",
		"REMOVE":         "new-value",
	}, []string{"OPUTE_MCP_URL", "REMOVE"}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	if !strings.Contains(content, "KEEP=one\n") || !strings.Contains(content, "MCP_AUTH_TOKEN=oha_reconciled\n") {
		t.Fatalf("assignments were not persisted: %q", content)
	}
	if !strings.Contains(content, "REMOVE=new-value\n") {
		t.Fatalf("assignment should win over removal: %q", content)
	}
	if strings.Contains(content, "OPUTE_MCP_URL=") {
		t.Fatalf("removed environment key survived: %q", content)
	}
	if mode := mustFileMode(t, path); mode != 0o600 {
		t.Fatalf("env file mode = %o, want 600", mode)
	}
}

func mustFileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
