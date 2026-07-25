package ops

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// StageBuildContextArgs writes a caller-provided build context onto the host so
// build_and_push_oci_image can run without an operator shell sync. Prefer small
// inline files for canaries; archiveUrl can be added later.
type StageBuildContextArgs struct {
	DestDir string            `json:"destDir"`
	Files   map[string]string `json:"files"`
	// FileEncoding is "utf8" (default) or "base64" for binary blobs.
	FileEncoding string `json:"fileEncoding,omitempty"`
}

// StageBuildContext materializes a host-local directory tree from MCP tool args.
func (s *HostOperationsService) StageBuildContext(args StageBuildContextArgs, onData func(string)) (map[string]any, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("stage_build_context is unsupported on %s host agents", runtime.GOOS)
	}
	destDir := strings.TrimSpace(args.DestDir)
	if destDir == "" {
		return nil, errors.New("destDir is required")
	}
	if len(args.Files) == 0 {
		return nil, errors.New("files is required and must be non-empty")
	}
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return nil, fmt.Errorf("resolve destDir: %w", err)
	}
	home, _ := os.UserHomeDir()
	allowedRoots := []string{}
	if home != "" {
		allowedRoots = append(allowedRoots, filepath.Join(home, ".opute", "build-contexts"))
		allowedRoots = append(allowedRoots, filepath.Join(home, ".opute", "standalone-bootstrap", "build-contexts"))
	}
	allowedRoots = append(allowedRoots, "/tmp/opute-build-contexts", "/var/tmp/opute-build-contexts")
	if !pathUnderAny(absDest, allowedRoots) {
		return nil, fmt.Errorf("destDir must be under an allowlisted build-contexts directory")
	}
	encoding := strings.ToLower(strings.TrimSpace(args.FileEncoding))
	if encoding == "" {
		encoding = "utf8"
	}
	if encoding != "utf8" && encoding != "base64" {
		return nil, errors.New("fileEncoding must be utf8 or base64")
	}
	if err := os.MkdirAll(absDest, 0o700); err != nil {
		return nil, fmt.Errorf("create destDir: %w", err)
	}
	written := make([]string, 0, len(args.Files))
	for rel, content := range args.Files {
		rel = strings.TrimSpace(rel)
		if rel == "" || strings.Contains(rel, "..") || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("invalid file path %q", rel)
		}
		target := filepath.Join(absDest, filepath.FromSlash(rel))
		if !strings.HasPrefix(target, absDest+string(os.PathSeparator)) && target != absDest {
			return nil, fmt.Errorf("file path escapes destDir: %q", rel)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, fmt.Errorf("create parent for %s: %w", rel, err)
		}
		var body []byte
		if encoding == "base64" {
			decoded, decErr := base64.StdEncoding.DecodeString(content)
			if decErr != nil {
				return nil, fmt.Errorf("decode base64 for %s: %w", rel, decErr)
			}
			body = decoded
		} else {
			body = []byte(content)
		}
		if err := os.WriteFile(target, body, 0o600); err != nil {
			return nil, fmt.Errorf("write %s: %w", rel, err)
		}
		written = append(written, rel)
		if onData != nil {
			onData(fmt.Sprintf("staged %s", rel))
		}
	}
	return map[string]any{
		"destDir":     absDest,
		"fileCount":   len(written),
		"files":       written,
		"fileEncoding": encoding,
	}, nil
}

func pathUnderAny(path string, roots []string) bool {
	clean := filepath.Clean(path)
	for _, root := range roots {
		root = filepath.Clean(root)
		if clean == root || strings.HasPrefix(clean, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}
