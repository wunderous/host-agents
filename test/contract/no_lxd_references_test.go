package contract_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGoHostAgentHasNoExplicitLxdReferences(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	allowlist := map[string]bool{
		filepath.Join(root, "test", "contract", "no_lxd_references_test.go"): true,
	}
	lxdPattern := regexp.MustCompile(`(?i)\blxd\b`)

	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := info.Name()
			if base == ".git" || base == "dist" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if allowlist[path] {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".go", ".json", ".md":
		default:
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if lxdPattern.Match(raw) {
			violations = append(violations, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("explicit lxd references remain:\n%s", strings.Join(violations, "\n"))
	}
}
