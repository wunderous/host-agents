package host

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type EnsureHostFileArgs struct {
	Path    string
	Content string
	Mode    int
}

type InspectHostFileArgs struct {
	Path            string
	ExpectedSHA256  string
	ExpectedContent string
}

type RemoveHostFileArgs struct {
	Path           string
	ExpectedSHA256 string
	Confirm        bool
}

// EnsureHostFile writes a caller-declared file beneath the current user's
// home directory. It is intentionally a narrow, atomic primitive for managed
// user configuration such as systemd units; recipes own the file contents.
func (s *Service) EnsureHostFile(args EnsureHostFileArgs) (map[string]any, error) {
	if err := s.shared.RequireSharedHostOwner("ensure_host_file"); err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	path, err := hostOwnedPath(home, args.Path)
	if err != nil {
		return nil, err
	}
	mode := os.FileMode(args.Mode)
	if args.Mode == 0 {
		mode = 0o600
	}
	if mode.Perm() < 0o600 || mode.Perm() > 0o755 {
		return nil, fmt.Errorf("mode must be between 0600 and 0755")
	}
	contentHash := sha256.Sum256([]byte(args.Content))
	contentSHA256 := "sha256:" + hex.EncodeToString(contentHash[:])
	changed := true
	if existing, readErr := os.ReadFile(path); readErr == nil {
		info, statErr := os.Stat(path)
		if statErr == nil && string(existing) == args.Content && info.Mode().Perm() == mode.Perm() {
			changed = false
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, fmt.Errorf("read managed host file: %w", readErr)
	}
	if changed {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create managed host file directory: %w", err)
		}
		temporary, err := os.CreateTemp(filepath.Dir(path), ".opute-host-file-*")
		if err != nil {
			return nil, fmt.Errorf("create managed host file temporary: %w", err)
		}
		temporaryPath := temporary.Name()
		defer os.Remove(temporaryPath)
		if err := temporary.Chmod(mode.Perm()); err != nil {
			_ = temporary.Close()
			return nil, fmt.Errorf("set managed host file mode: %w", err)
		}
		if _, err := temporary.WriteString(args.Content); err != nil {
			_ = temporary.Close()
			return nil, fmt.Errorf("write managed host file: %w", err)
		}
		if err := temporary.Close(); err != nil {
			return nil, fmt.Errorf("close managed host file: %w", err)
		}
		if err := os.Rename(temporaryPath, path); err != nil {
			return nil, fmt.Errorf("install managed host file: %w", err)
		}
	}
	return map[string]any{
		"path":          path,
		"changed":       changed,
		"contentSha256": contentSHA256,
		"mode":          strconv.FormatUint(uint64(mode.Perm()), 8),
	}, nil
}

func (s *Service) InspectHostFile(args InspectHostFileArgs) (map[string]any, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	path, err := hostOwnedPath(home, args.Path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{"path": path, "exists": false, "regular": false, "executable": false, "matches": false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect managed host file: %w", err)
	}
	result := map[string]any{
		"path":       path,
		"exists":     true,
		"regular":    info.Mode().IsRegular(),
		"executable": info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0,
		"mode":       strconv.FormatUint(uint64(info.Mode().Perm()), 8),
	}
	if info.Mode().IsRegular() {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("read inspected host file: %w", readErr)
		}
		hash := sha256.Sum256(content)
		observed := "sha256:" + hex.EncodeToString(hash[:])
		result["sha256"] = observed
		expected := strings.TrimSpace(args.ExpectedSHA256)
		matches := true
		if expected != "" {
			expected = "sha256:" + strings.TrimPrefix(expected, "sha256:")
			matches = strings.EqualFold(expected, observed)
		}
		if args.ExpectedContent != "" {
			desiredHash := sha256.Sum256([]byte(args.ExpectedContent))
			matches = matches && strings.EqualFold(observed, "sha256:"+hex.EncodeToString(desiredHash[:]))
		}
		result["matches"] = matches
	} else {
		result["matches"] = false
	}
	return result, nil
}

// RemoveHostFile deletes one caller-owned regular file beneath the current
// user's home directory after an explicit confirmation and optional content
// hash match. It is intentionally separate from ensure_host_file so a recipe
// cannot turn reconciliation into an implicit deletion.
func (s *Service) RemoveHostFile(args RemoveHostFileArgs) (map[string]any, error) {
	if err := s.shared.RequireSharedHostOwner("remove_host_file"); err != nil {
		return nil, err
	}
	if !args.Confirm {
		return nil, errors.New("remove_host_file requires confirm=true")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	path, err := hostOwnedPath(home, args.Path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{"path": path, "exists": false, "removed": false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect removable host file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("remove_host_file refuses non-regular path %q", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read removable host file: %w", err)
	}
	digest := sha256.Sum256(content)
	observed := "sha256:" + hex.EncodeToString(digest[:])
	if expected := strings.TrimSpace(args.ExpectedSHA256); expected != "" {
		expected = "sha256:" + strings.TrimPrefix(expected, "sha256:")
		if !strings.EqualFold(expected, observed) {
			return nil, fmt.Errorf("remove_host_file hash mismatch for %q", path)
		}
	}
	if err := os.Remove(path); err != nil {
		return nil, fmt.Errorf("remove host file: %w", err)
	}
	return map[string]any{"path": path, "exists": true, "removed": true, "sha256": observed}, nil
}

func hostOwnedPath(home, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("path is required")
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		raw = filepath.Join(home, strings.TrimPrefix(raw, "~/"))
	}
	absolute, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	relative, err := filepath.Rel(home, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must be beneath the current user's home directory")
	}
	if info, err := os.Lstat(absolute); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("managed host file path must not be a symlink")
	}
	return absolute, nil
}
