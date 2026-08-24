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
	core := []string{"cmd", "internal/cli", "internal/cordis", "internal/hostmcp", "internal/recipe", "internal/plan", "internal/catalog", "internal/state", "internal/ops"}
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
