package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	k3sBootstrapScriptURL = "https://get.k3s.io"
	k3sReleaseURL         = "https://github.com/k3s-io/k3s/releases/download/"
	maxK3sBootstrapScript = 1 << 20
	maxK3sBinaryBytes     = 256 << 20
)

var k3sBootstrapHTTPClient = &http.Client{Timeout: 5 * time.Minute}

// installK3sServer stages the versioned K3s binary on the Host Agent side,
// verifies its release checksum, and runs the official installer with download
// disabled. Fresh guests may be behind Gateway TLS inspection, so downloads
// must occur on the authenticated Host Agent network rather than weakening TLS
// verification inside the guest.
func installK3sServer(ctx context.Context, instance, version, installExec, address, token string) error {
	installer, binary, checksum, err := downloadK3sArtifacts(ctx, version)
	if err != nil {
		return err
	}

	temporary, err := os.CreateTemp("", "opute-k3s-")
	if err != nil {
		return fmt.Errorf("create staged K3s binary: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect staged K3s binary: %w", err)
	}
	if _, err := temporary.Write(binary); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("stage K3s binary: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close staged K3s binary: %w", err)
	}

	remotePath := "/tmp/opute-k3s-" + checksum[:16]
	if _, err := runCommand(ctx, []string{"file", "push", temporaryPath, instance + remotePath}, nil); err != nil {
		return fmt.Errorf("stage K3s binary in guest: %w", err)
	}
	defer func() {
		_, _ = runCommand(context.Background(), []string{"exec", instance, "--", "rm", "-f", remotePath}, nil)
	}()
	if _, err := runCommand(ctx, []string{"exec", instance, "--", "install", "-m", "0755", remotePath, "/usr/local/bin/k3s"}, nil); err != nil {
		return fmt.Errorf("install staged K3s binary: %w", err)
	}

	args := []string{
		"exec", instance,
		"--env", "INSTALL_K3S_SKIP_DOWNLOAD=true",
		"--env", "INSTALL_K3S_VERSION=" + version,
		"--env", "INSTALL_K3S_EXEC=" + installExec,
	}
	input := installer
	if token != "" {
		args = append(args, "--env", "K3S_URL="+address, "--", "bash", "-lc", "IFS= read -r K3S_TOKEN || exit 1; export K3S_TOKEN; exec bash -s")
		input = append([]byte(token+"\n"), installer...)
	} else {
		args = append(args, "--", "bash", "-s")
	}
	if _, err := runCommand(ctx, args, input); err != nil {
		diagnostics := k3sInstallDiagnostics(ctx, instance, token)
		if diagnostics != "" {
			return fmt.Errorf("run verified K3s installer: %w; guest diagnostics: %s", err, diagnostics)
		}
		return fmt.Errorf("run verified K3s installer: %w", err)
	}
	return nil
}

func k3sInstallDiagnostics(ctx context.Context, instance, token string) string {
	// The MCP request context is commonly cancelled as soon as the installer
	// exits non-zero. Keep a short, independent diagnostic window so the guest
	// failure survives the provider error path and disposable-guest cleanup.
	diagnosticCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := runCommand(diagnosticCtx, []string{"exec", instance, "--", "bash", "-lc", "systemctl status --no-pager --full k3s || true; journalctl -u k3s -n 120 --no-pager || true"}, nil)
	if err != nil {
		return ""
	}
	diagnostics := strings.TrimSpace(string(output))
	if token != "" {
		diagnostics = strings.ReplaceAll(diagnostics, token, "[REDACTED]")
	}
	return diagnostics
}

func downloadK3sArtifacts(ctx context.Context, version string) ([]byte, []byte, string, error) {
	if version == "" || strings.ContainsAny(version, "\x00\r\n ';&|`$()") {
		return nil, nil, "", fmt.Errorf("invalid K3s version")
	}
	escapedVersion := url.PathEscape(version)
	escapedVersion = strings.ReplaceAll(escapedVersion, "+", "%2B")
	base := k3sReleaseURL + escapedVersion
	checksumText, err := downloadK3sURL(ctx, base+"/sha256sum-amd64.txt", maxK3sBootstrapScript)
	if err != nil {
		return nil, nil, "", fmt.Errorf("download K3s release checksums: %w", err)
	}
	checksum, err := k3sChecksumForBinary(string(checksumText))
	if err != nil {
		return nil, nil, "", err
	}
	binary, err := downloadK3sURL(ctx, base+"/k3s", maxK3sBinaryBytes)
	if err != nil {
		return nil, nil, "", fmt.Errorf("download K3s binary: %w", err)
	}
	digest := sha256.Sum256(binary)
	observed := hex.EncodeToString(digest[:])
	if !strings.EqualFold(observed, checksum) {
		return nil, nil, "", fmt.Errorf("K3s binary checksum mismatch: expected %s, observed %s", checksum, observed)
	}
	installer, err := downloadK3sURL(ctx, k3sBootstrapScriptURL, maxK3sBootstrapScript)
	if err != nil {
		return nil, nil, "", fmt.Errorf("download K3s installer: %w", err)
	}
	return installer, binary, checksum, nil
}

func downloadK3sURL(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := k3sBootstrapHTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected HTTP status %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d-byte limit", limit)
	}
	return data, nil
}

func k3sChecksumForBinary(checksums string) (string, error) {
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || len(fields[0]) != sha256.Size*2 {
			continue
		}
		name := strings.TrimPrefix(filepath.Base(fields[1]), "*")
		if name == "k3s" {
			if _, err := hex.DecodeString(fields[0]); err != nil {
				return "", fmt.Errorf("K3s release checksum is not hexadecimal: %w", err)
			}
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("K3s release checksums do not contain the amd64 k3s binary")
}
