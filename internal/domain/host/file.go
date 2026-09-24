package host

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type EnsureHostFileArgs struct {
	Path    string
	Content string
	Mode    int
	Scope   string
}

type InspectHostFileArgs struct {
	Path            string
	Scope           string
	ExpectedSHA256  string
	ExpectedContent string
}

type RemoveHostFileArgs struct {
	Path           string
	ExpectedSHA256 string
	Confirm        bool
	Scope          string
}

// EnsureHostFile writes a caller-declared managed file atomically. User-scoped
// files remain below the current user's home directory; system scope is
// limited to systemd service units so a typed lifecycle recipe can own a
// system-scoped service without falling back to an untyped shell write.
func (s *Service) EnsureHostFile(args EnsureHostFileArgs) (map[string]any, error) {
	if err := s.shared.RequireSharedHostOwner("ensure_host_file"); err != nil {
		return nil, err
	}
	scope := strings.ToLower(strings.TrimSpace(args.Scope))
	if scope == "" {
		scope = "user"
	}
	var path string
	var err error
	switch scope {
	case "user":
		home, homeErr := hostHomeDir()
		if homeErr != nil {
			return nil, fmt.Errorf("resolve home directory: %w", homeErr)
		}
		path, err = hostOwnedPath(home, args.Path)
	case "system":
		path, err = systemdUnitPath(args.Path)
	default:
		return nil, fmt.Errorf("scope must be user or system")
	}
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
		"scope":         scope,
		"changed":       changed,
		"contentSha256": contentSHA256,
		"mode":          strconv.FormatUint(uint64(mode.Perm()), 8),
	}, nil
}

func systemdUnitPath(raw string) (string, error) {
	path := filepath.Clean(strings.TrimSpace(raw))
	if !strings.HasPrefix(path, "/etc/systemd/system/") {
		return "", fmt.Errorf("system-scoped managed files must be systemd units beneath /etc/systemd/system")
	}
	unit := filepath.Base(path)
	if !regexp.MustCompile(`^[A-Za-z0-9_.@:-]+\.service$`).MatchString(unit) {
		return "", fmt.Errorf("system-scoped managed files must name a .service unit")
	}
	return path, nil
}

// InspectHostFile reads back a file this agent manages. It resolves the path
// under the same scope rules as EnsureHostFile and RemoveHostFile: without
// that, a system-scoped unit could be written and deleted but never read, and
// the readiness check for the very file a recipe just wrote was impossible to
// express on a root agent whose unit directory is /etc/systemd/system.
func (s *Service) InspectHostFile(args InspectHostFileArgs) (map[string]any, error) {
	path, err := managedHostPath(args.Scope, args.Path)
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

// RemoveHostFile deletes one caller-owned regular file after an explicit
// confirmation and optional content hash match. User-scoped paths remain
// beneath the current user's home directory; system scope is limited to one
// systemd service unit. It is intentionally separate from ensure_host_file so
// a recipe cannot turn reconciliation into an implicit deletion.
func (s *Service) RemoveHostFile(args RemoveHostFileArgs) (map[string]any, error) {
	if err := s.shared.RequireSharedHostOwner("remove_host_file"); err != nil {
		return nil, err
	}
	if !args.Confirm {
		return nil, errors.New("remove_host_file requires confirm=true")
	}
	scope := strings.ToLower(strings.TrimSpace(args.Scope))
	if scope == "" {
		scope = "user"
	}
	var path string
	var err error
	switch scope {
	case "user":
		home, homeErr := hostHomeDir()
		if homeErr != nil {
			return nil, fmt.Errorf("resolve home directory: %w", homeErr)
		}
		path, err = hostOwnedPath(home, args.Path)
	case "system":
		path, err = systemdUnitPath(args.Path)
	default:
		return nil, fmt.Errorf("scope must be user or system")
	}
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{"path": path, "scope": scope, "exists": false, "removed": false}, nil
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
	return map[string]any{"path": path, "scope": scope, "exists": true, "removed": true, "sha256": observed}, nil
}

// managedHostPath resolves a caller-supplied path under the scope that owns it.
// User scope stays beneath this user's home; system scope is one systemd
// service unit. Every managed-file operation goes through here so the three
// agree on what this agent is allowed to name.
func managedHostPath(scope, raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "user":
		home, err := hostHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return hostOwnedPath(home, raw)
	case "system":
		return systemdUnitPath(raw)
	default:
		return "", fmt.Errorf("scope must be user or system")
	}
}

func hostOwnedPath(home, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("path is required")
	}
	homeAbsolute, err := filepath.Abs(filepath.Clean(home))
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		raw = filepath.Join(homeAbsolute, strings.TrimPrefix(raw, "~/"))
	} else if !filepath.IsAbs(raw) {
		raw = filepath.Join(homeAbsolute, raw)
	}
	absolute, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	relative, err := filepath.Rel(homeAbsolute, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must be beneath the current user's home directory")
	}
	// An existing symlink in any component can redirect a lexically in-home path outside the account home.
	current := homeAbsolute
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr != nil {
			return "", fmt.Errorf("inspect managed host file path: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("managed host file path must not traverse a symlink")
		}
	}
	return absolute, nil
}
