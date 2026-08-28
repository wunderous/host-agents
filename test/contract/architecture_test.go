package contract

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProviderAndClientBoundariesHaveNoConcreteCrossImports(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	core := []string{"cmd", "internal/cli", "internal/cordis", "internal/hostmcp", "internal/recipe", "internal/plan", "internal/catalog", "internal/state", "internal/hostagent", "internal/domain"}
	for _, relative := range core {
		assertImportsExclude(t, filepath.Join(root, relative), map[string]bool{
			"github.com/wunderous/host-agents/plugins/llm/ollama":           true,
			"github.com/wunderous/host-agents/plugins/tunneling/cloudflare": true,
		})
	}
	assertImportsExclude(t, filepath.Join(root, "plugins"), map[string]bool{
		"github.com/wunderous/host-agents/internal/hostmcp":            true,
		"github.com/wunderous/host-agents/internal/state":              true,
		"github.com/wunderous/host-agents/internal/plan":               true,
		"github.com/wunderous/host-agents/internal/resource/admission": true,
	})
	t.Log("ARCHITECTURE_BOUNDARY_PASS")
}

func TestKernelHasNoPhoneHomeOrProductHostnames(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	forbiddenFiles := []string{
		filepath.Join(root, "internal", "transport", "reverse_tunnel.go"),
		filepath.Join(root, "internal", "transport", "host_worker.go"),
		filepath.Join(root, "internal", "transport", "health_only.go"),
		filepath.Join(root, "internal", "heartbeat", "service.go"),
	}
	for _, path := range forbiddenFiles {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("retired phone-home file still present: %s", path)
		}
	}
	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"heartbeat"+string(filepath.Separator)) {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		source := string(raw)
		if strings.Contains(source, "mcp.opute.io") || strings.Contains(source, "opute.io") {
			t.Errorf("%s contains a product hostname in runtime code", path)
		}
		if strings.Contains(path, string(filepath.Separator)+"transport"+string(filepath.Separator)) && strings.Contains(source, "gorilla/websocket") {
			t.Errorf("%s still dials websocket", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertImportsExclude(t *testing.T, root string, forbidden map[string]bool) {
	t.Helper()
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("boundary root %s: %v", root, err)
	}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath := strings.Trim(spec.Path.Value, `"`)
			for prefix := range forbidden {
				if importPath == prefix || strings.HasPrefix(importPath, prefix) {
					t.Errorf("%s imports forbidden boundary %s", path, importPath)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
