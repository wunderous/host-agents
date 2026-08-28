package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Large provider distributions can legitimately exceed 512 MiB (the pinned
// Ollama Linux archive is currently about 1.4 GiB). The digest requirement,
// HTTPS-only transport, home-owned destination, and this finite upper bound
// keep the generic primitive bounded without baking in a provider size.
const maxHostArtifactBytes = 4 * 1024 * 1024 * 1024

type EnsureHostArtifactArgs struct {
	URI         string
	Destination string
	SHA256      string
	Executable  bool
}

// EnsureHostArtifact downloads a caller-declared HTTPS artifact into the
// current user's home directory and verifies its SHA-256 before installation.
// It is deliberately unaware of the artifact's producer or purpose.
func (s *Service) EnsureHostArtifact(args EnsureHostArtifactArgs, onData func(string)) (map[string]any, error) {
	if err := s.shared.RequireSharedHostOwner("ensure_host_artifact"); err != nil {
		return nil, err
	}
	uri := strings.TrimSpace(args.URI)
	if err := validateHostArtifactURI(uri); err != nil {
		return nil, err
	}
	expected, err := normalizeSHA256(args.SHA256)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	destination, err := hostOwnedPath(home, args.Destination)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Stat(destination); statErr == nil && info.Mode().IsRegular() {
		observed, hashErr := hostArtifactFileSHA256(destination)
		if hashErr != nil {
			return nil, hashErr
		}
		if strings.EqualFold(observed, expected) {
			if args.Executable && info.Mode().Perm()&0o111 == 0 {
				if chmodErr := os.Chmod(destination, info.Mode().Perm()|0o755); chmodErr != nil {
					return nil, fmt.Errorf("make host artifact executable: %w", chmodErr)
				}
			}
			return map[string]any{"uri": uri, "destination": destination, "sha256": observed, "changed": false, "executable": args.Executable}, nil
		}
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect host artifact destination: %w", statErr)
	}
	if onData != nil {
		onData(fmt.Sprintf("Downloading verified host artifact to %s...", destination))
	}
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if req.URL.Scheme != "https" {
			return fmt.Errorf("host artifact redirect must remain HTTPS")
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("create host artifact request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download host artifact: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("download host artifact: HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxHostArtifactBytes {
		return nil, fmt.Errorf("host artifact exceeds %d bytes", maxHostArtifactBytes)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return nil, fmt.Errorf("create host artifact directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".opute-host-artifact-*")
	if err != nil {
		return nil, fmt.Errorf("create host artifact temporary: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hasher := sha256.New()
	limited := io.LimitReader(response.Body, maxHostArtifactBytes+1)
	if _, err := io.Copy(io.MultiWriter(temporary, hasher), limited); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("read host artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("close host artifact: %w", err)
	}
	observed := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(observed, expected) {
		return nil, fmt.Errorf("host artifact sha256 mismatch: expected %s, got %s", expected, observed)
	}
	mode := os.FileMode(0o644)
	if args.Executable {
		mode = 0o755
	}
	if err := os.Chmod(temporaryPath, mode); err != nil {
		return nil, fmt.Errorf("set host artifact mode: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return nil, fmt.Errorf("install host artifact: %w", err)
	}
	return map[string]any{"uri": uri, "destination": destination, "sha256": observed, "changed": true, "executable": args.Executable}, nil
}

func validateHostArtifactURI(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("host artifact URI must be an absolute HTTPS URL without credentials")
	}
	return nil
}

func normalizeSHA256(raw string) (string, error) {
	value := strings.TrimSpace(strings.TrimPrefix(raw, "sha256:"))
	if !regexp.MustCompile(`^[0-9a-fA-F]{64}$`).MatchString(value) {
		return "", fmt.Errorf("host artifact sha256 must be a 64-character hexadecimal digest")
	}
	return "sha256:" + strings.ToLower(value), nil
}

func hostArtifactFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open host artifact: %w", err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hash host artifact: %w", err)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}
