package ops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	k3sInstallerURL         = "https://get.k3s.io"
	k3sReleaseArtifactBase  = "https://github.com/k3s-io/k3s/releases/download"
	k3sInstallerMaxBytes    = 4 << 20
	k3sBinaryMaxBytes       = 256 << 20
	k3sArtifactDownloadWait = 15 * time.Minute
)

type k3sGuestArtifacts struct {
	Installer []byte
	Binary    []byte
}

func downloadK3sGuestArtifacts(ctx context.Context, version string) (k3sGuestArtifacts, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		return k3sGuestArtifacts{}, errors.New("K3s version is required for artifact staging")
	}
	versionPath := k3sVersionPath(version)
	base := strings.TrimRight(k3sReleaseArtifactBase, "/") + "/" + versionPath

	installer, err := downloadK3sArtifact(ctx, k3sInstallerURL, k3sInstallerMaxBytes)
	if err != nil {
		return k3sGuestArtifacts{}, fmt.Errorf("download K3s installer: %w", err)
	}
	hashFile, err := downloadK3sArtifact(ctx, base+"/sha256sum-amd64.txt", 1<<20)
	if err != nil {
		return k3sGuestArtifacts{}, fmt.Errorf("download K3s checksum manifest: %w", err)
	}
	expected, err := parseK3sArtifactSHA256(hashFile, "k3s")
	if err != nil {
		return k3sGuestArtifacts{}, err
	}
	binary, err := downloadK3sArtifact(ctx, base+"/k3s", k3sBinaryMaxBytes)
	if err != nil {
		return k3sGuestArtifacts{}, fmt.Errorf("download K3s binary: %w", err)
	}
	actual := sha256.Sum256(binary)
	if !strings.EqualFold(expected, hex.EncodeToString(actual[:])) {
		return k3sGuestArtifacts{}, fmt.Errorf("K3s binary checksum mismatch: expected %s, got %s", expected, hex.EncodeToString(actual[:]))
	}
	return k3sGuestArtifacts{Installer: installer, Binary: binary}, nil
}

func k3sVersionPath(version string) string {
	return strings.ReplaceAll(url.PathEscape(strings.TrimSpace(version)), "+", "%2B")
}

func downloadK3sArtifact(ctx context.Context, uri string, maxBytes int64) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	requestCtx, cancel := context.WithTimeout(request.Context(), k3sArtifactDownloadWait)
	defer cancel()
	request = request.WithContext(requestCtx)
	response, err := (&http.Client{Timeout: k3sArtifactDownloadWait}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if maxBytes <= 0 {
		return nil, errors.New("artifact size limit must be positive")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response exceeds %d-byte limit", maxBytes)
	}
	return body, nil
}

func parseK3sArtifactSHA256(contents []byte, artifact string) (string, error) {
	artifact = strings.TrimSpace(artifact)
	if artifact == "" {
		return "", errors.New("K3s artifact name is required")
	}
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.TrimPrefix(fields[1], "*") != artifact {
			continue
		}
		candidate := strings.TrimSpace(fields[0])
		if len(candidate) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(candidate); err != nil {
			continue
		}
		return strings.ToLower(candidate), nil
	}
	return "", fmt.Errorf("K3s checksum manifest does not contain a valid %s entry", artifact)
}
