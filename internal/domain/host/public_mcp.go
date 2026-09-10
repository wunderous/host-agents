package host

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/mcpprobe"
)

const (
	defaultCloudflaredArtifactURI    = "https://github.com/cloudflare/cloudflared/releases/download/2026.8.2/cloudflared-linux-amd64"
	defaultCloudflaredArtifactSHA256 = "sha256:fcfb02b575a52ca1af2e3267af4e1517bcdeb30ac48c834c69abaed3c0576ad2"
	publicMCPServicePrefix           = "opute-cloudflared-"
	publicMCPContractVersion         = "mcp-exposure.v1"
	publicMCPTokenEnv                = "OPUTE_CLOUDFLARED_TUNNEL_TOKEN"
	publicMCPManagedFileMarker       = "# Managed by Opute Host Agent: public MCP tunnel\n"
	publicMCPProbeRetryWindow        = 90 * time.Second
	publicMCPProbeRetryDelay         = 2 * time.Second
)

var (
	publicMCPBindingPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,254}$`)
	publicMCPInstanceSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)
)

// EnsurePublicMcpTunnelArgs is the Host Agent-owned portion of public MCP
// exposure. The edge provider still owns DNS, TLS, and issuance of the tunnel
// token; the token is consumed here only to configure the local connector.
type EnsurePublicMcpTunnelArgs struct {
	BindingID   string
	Endpoint    string
	LocalTarget string
	// OriginHostID is required when LocalTarget is not loopback. It keeps the
	// remote origin explicit at the Host Agent boundary instead of allowing a
	// provider callback to turn an arbitrary URL into an implicit proxy.
	OriginHostID   string
	TunnelToken    string
	ArtifactURI    string
	ArtifactSHA256 string
	ArtifactPath   string
	TokenFile      string
	ServiceName    string
	ServiceFile    string
	Scope          string
}

type publicMCPPaths struct {
	scope          string
	bindingID      string
	artifactURI    string
	artifactSHA256 string
	artifactPath   string
	tokenFile      string
	serviceName    string
	serviceFile    string
	unitArtifact   string
	unitTokenFile  string
}

// EnsurePublicMcpTunnel installs a pinned cloudflared connector, stores its
// provider-issued token in an Opute-owned 0600 file, starts the corresponding
// service, and proves the stable HTTPS /mcp endpoint with the same authenticated
// tools/list check used by the Host Agent's onboarding validators.
func (s *Service) EnsurePublicMcpTunnel(ctx context.Context, args EnsurePublicMcpTunnelArgs, onData func(string)) (map[string]any, error) {
	if err := s.shared.RequireSharedHostOwner("ensure_public_mcp_tunnel"); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	paths, endpoint, localTarget, err := resolvePublicMCPPaths(args)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(args.TunnelToken)
	if err := validatePublicMCPTunnelToken(token); err != nil {
		return nil, err
	}

	artifact, err := s.ensurePublicMCPArtifact(paths, onData)
	if err != nil {
		return nil, fmt.Errorf("ensure public MCP connector artifact: %w", err)
	}
	tokenContent := publicMCPManagedFileMarker + publicMCPTokenEnv + "=" + token + "\n"
	if err := requireOwnedManagedFile(paths.tokenFile, publicMCPManagedFileMarker); err != nil {
		return nil, err
	}
	tokenObservation, err := s.ensurePublicMCPTokenFile(paths, tokenContent)
	if err != nil {
		return nil, fmt.Errorf("store public MCP tunnel token: %w", err)
	}

	unit := renderPublicMCPUnit(paths)
	if err := requireOwnedManagedFile(paths.serviceFile, publicMCPManagedFileMarker); err != nil {
		return nil, err
	}
	if _, err := s.EnsureHostFile(EnsureHostFileArgs{
		Path: paths.serviceFile, Content: unit, Mode: 0o600, Scope: paths.scope,
	}); err != nil {
		return nil, fmt.Errorf("write public MCP connector service: %w", err)
	}
	if _, err := s.EnsureHostServiceSupervisor(EnsureHostServiceSupervisorArgs{Scope: paths.scope}, onData); err != nil {
		return nil, fmt.Errorf("ensure public MCP connector supervisor: %w", err)
	}
	serviceArgs := SetHostServiceStateArgs{ServiceName: paths.serviceName, Scope: paths.scope}
	serviceArgs.State = "enable"
	if _, err := s.SetHostServiceState(serviceArgs, onData); err != nil {
		return nil, fmt.Errorf("enable public MCP connector service: %w", err)
	}
	serviceArgs.State = "start"
	if _, err := s.SetHostServiceState(serviceArgs, onData); err != nil {
		return nil, fmt.Errorf("start public MCP connector service: %w", err)
	}

	// MCP resources intentionally reject GET with 405. Use the same typed
	// authenticated tools/list probe for the local origin that we use for the
	// public endpoint; an HTTP health-style GET would turn a healthy MCP
	// server into a false readiness failure.
	origin, err := mcpprobe.ProbeAuthenticatedMCPEndpoint(ctx, localTarget, "")
	if err != nil {
		return nil, fmt.Errorf("probe public MCP origin: %w", err)
	}
	if origin == nil || origin["ready"] != true {
		return nil, fmt.Errorf("public MCP origin is not ready")
	}
	public, err := probePublicMCPWithRetry(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("probe authenticated public MCP endpoint: %w", err)
	}

	return map[string]any{
		"contractVersion": publicMCPContractVersion,
		"ready":           true,
		"bindingId":       paths.bindingID,
		"endpoint":        endpoint,
		"localTarget":     localTarget,
		"originHostId":    strings.TrimSpace(args.OriginHostID),
		"scope":           paths.scope,
		"serviceName":     paths.serviceName,
		"serviceFile":     paths.serviceFile,
		"tokenFile":       tokenObservation,
		"artifact":        artifact,
		"origin":          origin,
		"publicAuth":      public,
	}, nil
}

// probePublicMCPWithRetry closes the publication race between the provider's
// DNS/CNAME write and recursive DNS visibility. The connector may already be
// running while the stable hostname is still returning a transient lookup or
// connection error, so a single probe would incorrectly fail a valid tunnel.
// The retry is bounded and the final typed MCP/authentication error remains
// visible to the caller.
func probePublicMCPWithRetry(ctx context.Context, endpoint string) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(publicMCPProbeRetryWindow)
	var lastErr error
	for {
		result, err := mcpprobe.ProbeAuthenticatedMCPEndpoint(ctx, endpoint, "")
		if err == nil {
			return result, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("public MCP endpoint did not become ready within %s: %w", publicMCPProbeRetryWindow, lastErr)
		}
		timer := time.NewTimer(publicMCPProbeRetryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func resolvePublicMCPPaths(args EnsurePublicMcpTunnelArgs) (publicMCPPaths, string, string, error) {
	bindingID := strings.TrimSpace(args.BindingID)
	if !publicMCPBindingPattern.MatchString(bindingID) {
		return publicMCPPaths{}, "", "", fmt.Errorf("bindingId must contain only letters, numbers, dots, colons, underscores, and hyphens")
	}
	endpoint, err := mcpprobe.ValidateEndpoint(strings.TrimSpace(args.Endpoint))
	if err != nil {
		return publicMCPPaths{}, "", "", err
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || parsedEndpoint.Scheme != "https" {
		return publicMCPPaths{}, "", "", fmt.Errorf("endpoint must be an absolute HTTPS URL ending in /mcp")
	}
	localTarget := strings.TrimSpace(args.LocalTarget)
	if err := validatePublicMCPOrigin(localTarget, args.OriginHostID); err != nil {
		return publicMCPPaths{}, "", "", err
	}
	scope := strings.ToLower(strings.TrimSpace(args.Scope))
	if scope == "" {
		scope = "user"
	}
	if scope != "user" && scope != "system" {
		return publicMCPPaths{}, "", "", fmt.Errorf("scope must be user or system")
	}
	if scope == "system" && os.Geteuid() != 0 {
		return publicMCPPaths{}, "", "", fmt.Errorf("system-scoped public MCP connector requires root")
	}

	instanceID := normalizePublicMCPInstanceID(bindingID)
	serviceName := publicMCPServicePrefix + instanceID + ".service"
	if provided := strings.TrimSpace(args.ServiceName); provided != "" && provided != serviceName {
		return publicMCPPaths{}, "", "", fmt.Errorf("serviceName must be the Opute-owned name %q", serviceName)
	}
	artifactURI := strings.TrimSpace(args.ArtifactURI)
	if artifactURI == "" {
		artifactURI = defaultCloudflaredArtifactURI
	}
	if err := validateHostArtifactURI(artifactURI); err != nil {
		return publicMCPPaths{}, "", "", err
	}
	artifactSHA, err := normalizeSHA256(firstNonEmptyString(args.ArtifactSHA256, defaultCloudflaredArtifactSHA256))
	if err != nil {
		return publicMCPPaths{}, "", "", err
	}

	var paths publicMCPPaths
	paths.scope, paths.bindingID, paths.serviceName = scope, bindingID, serviceName
	paths.artifactURI, paths.artifactSHA256 = artifactURI, artifactSHA
	if scope == "user" {
		home, homeErr := hostHomeDir()
		if homeErr != nil {
			return publicMCPPaths{}, "", "", fmt.Errorf("resolve home directory: %w", homeErr)
		}
		paths.artifactPath = filepath.Join(home, ".local", "share", "opute", "tunnels", instanceID, "cloudflared")
		paths.tokenFile = filepath.Join(home, ".config", "opute", "tunnels", instanceID+".env")
		paths.serviceFile = filepath.Join(home, ".config", "systemd", "user", serviceName)
		paths.unitArtifact = filepath.Join("%h", ".local", "share", "opute", "tunnels", instanceID, "cloudflared")
		paths.unitTokenFile = filepath.Join("%h", ".config", "opute", "tunnels", instanceID+".env")
		for label, provided := range map[string]string{
			"artifactPath": args.ArtifactPath, "tokenFile": args.TokenFile, "serviceFile": args.ServiceFile,
		} {
			if strings.TrimSpace(provided) != "" && filepath.Clean(provided) != pathsForUserArg(label, paths) {
				return publicMCPPaths{}, "", "", fmt.Errorf("%s must remain at the Opute-owned public MCP path", label)
			}
		}
	} else {
		paths.artifactPath = filepath.Join("/opt", "opute", "tunnels", instanceID, "cloudflared")
		paths.tokenFile = filepath.Join("/etc", "opute", "tunnels", instanceID+".env")
		paths.serviceFile = filepath.Join("/etc", "systemd", "system", serviceName)
		paths.unitArtifact = paths.artifactPath
		paths.unitTokenFile = paths.tokenFile
		for label, provided := range map[string]string{
			"artifactPath": args.ArtifactPath, "tokenFile": args.TokenFile, "serviceFile": args.ServiceFile,
		} {
			expected := pathsForUserArg(label, paths)
			if strings.TrimSpace(provided) != "" && filepath.Clean(provided) != expected {
				return publicMCPPaths{}, "", "", fmt.Errorf("%s must remain at the Opute-owned public MCP path", label)
			}
		}
	}
	return paths, endpoint, localTarget, nil
}

// normalizePublicMCPInstanceID mirrors the Platform-side service-name
// derivation. A short digest makes punctuation normalization injective so two
// provider bindings cannot silently share a token file or systemd unit.
func normalizePublicMCPInstanceID(bindingID string) string {
	raw := strings.ToLower(strings.TrimSpace(bindingID))
	normalized := strings.Trim(publicMCPInstanceSanitizer.ReplaceAllString(raw, "-"), "-")
	if normalized == "" {
		normalized = "host-agent"
	}
	base := normalized
	if len(base) > 63 {
		base = strings.TrimRight(base[:63], "-")
	}
	if raw == base {
		return base
	}
	digest := sha256.Sum256([]byte(raw))
	suffix := hex.EncodeToString(digest[:])[:10]
	baseLength := 63 - len(suffix) - 1
	if baseLength < 1 {
		baseLength = 1
	}
	base = strings.TrimRight(normalized[:minInt(baseLength, len(normalized))], "-")
	if base == "" {
		base = "host-agent"
	}
	result := base + "-" + suffix
	if len(result) > 63 {
		result = strings.TrimRight(result[:63], "-")
	}
	return result
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func pathsForUserArg(label string, paths publicMCPPaths) string {
	switch label {
	case "artifactPath":
		return paths.artifactPath
	case "tokenFile":
		return paths.tokenFile
	case "serviceFile":
		return paths.serviceFile
	default:
		return ""
	}
}

func validatePublicMCPOrigin(raw, originHostID string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("localTarget must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(path, "/mcp") {
		return fmt.Errorf("localTarget must end with /mcp")
	}
	hostname := strings.TrimSpace(parsed.Hostname())
	if hostname == "localhost" || strings.EqualFold(hostname, "ip6-localhost") {
		return nil
	}
	if ip := net.ParseIP(hostname); ip != nil && ip.IsLoopback() {
		return nil
	}
	if strings.TrimSpace(originHostID) == "" {
		return fmt.Errorf("non-loopback localTarget requires an explicit originHostId")
	}
	return nil
}

func validatePublicMCPTunnelToken(token string) error {
	if len(token) < 32 {
		return fmt.Errorf("tunnelToken must contain at least 32 characters")
	}
	if strings.IndexFunc(token, func(r rune) bool { return r == '\r' || r == '\n' || r == 0 || r == '\t' || r == ' ' }) >= 0 {
		return fmt.Errorf("tunnelToken must not contain whitespace or control characters")
	}
	return nil
}

func (s *Service) ensurePublicMCPArtifact(paths publicMCPPaths, onData func(string)) (map[string]any, error) {
	if paths.scope == "user" {
		home, err := hostHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home directory: %w", err)
		}
		owned, err := hostOwnedPath(home, paths.artifactPath)
		if err != nil || owned != filepath.Clean(paths.artifactPath) {
			return nil, fmt.Errorf("artifactPath must remain beneath the current user's Opute tunnel directory")
		}
	} else if !strings.HasPrefix(filepath.Clean(paths.artifactPath), "/opt/opute/tunnels/") {
		return nil, fmt.Errorf("system artifactPath must remain beneath /opt/opute/tunnels")
	}
	return s.ensureHostArtifactAt(paths.artifactURI, paths.artifactPath, paths.artifactSHA256, true, onData)
}

func requireOwnedManagedFile(path, marker string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Opute-owned public MCP file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to overwrite non-regular Opute public MCP path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Opute-owned public MCP file: %w", err)
	}
	if !bytes.HasPrefix(data, []byte(marker)) {
		return fmt.Errorf("refusing to overwrite non-Opute file at public MCP path")
	}
	return nil
}

func (s *Service) ensurePublicMCPTokenFile(paths publicMCPPaths, content string) (map[string]any, error) {
	if paths.scope == "user" {
		return s.EnsureHostFile(EnsureHostFileArgs{Path: paths.tokenFile, Content: content, Mode: 0o600, Scope: "user"})
	}
	return ensurePrivateManagedFile(paths.tokenFile, content, 0o600)
}

func ensurePrivateManagedFile(path, content string, mode os.FileMode) (map[string]any, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("managed public MCP secret path is not a regular file")
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("read managed public MCP secret: %w", readErr)
		}
		if string(data) == content && info.Mode().Perm() == mode.Perm() {
			return managedFileObservation(path, content, false, mode), nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect managed public MCP secret: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create managed public MCP secret directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".opute-public-mcp-*")
	if err != nil {
		return nil, fmt.Errorf("create managed public MCP secret temporary: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode.Perm()); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("set managed public MCP secret mode: %w", err)
	}
	if _, err := temporary.WriteString(content); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("write managed public MCP secret: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("close managed public MCP secret: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return nil, fmt.Errorf("install managed public MCP secret: %w", err)
	}
	return managedFileObservation(path, content, true, mode), nil
}

func managedFileObservation(path, content string, changed bool, mode os.FileMode) map[string]any {
	digest := sha256.Sum256([]byte(content))
	return map[string]any{
		"path":          path,
		"changed":       changed,
		"contentSha256": "sha256:" + hex.EncodeToString(digest[:]),
		"mode":          strconv.FormatUint(uint64(mode.Perm()), 8),
	}
}

func renderPublicMCPUnit(paths publicMCPPaths) string {
	wantedBy := "default.target"
	if paths.scope == "system" {
		wantedBy = "multi-user.target"
	}
	command := fmt.Sprintf("exec %s tunnel --no-autoupdate run --token \"$%s\"", shellQuote(paths.unitArtifact), publicMCPTokenEnv)
	return publicMCPManagedFileMarker + fmt.Sprintf("[Unit]\nDescription=Opute authenticated public MCP tunnel\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nEnvironmentFile=%s\nExecStart=/bin/sh -c %s\nRestart=always\nRestartSec=15\n\n[Install]\nWantedBy=%s\n", paths.unitTokenFile, shellQuote(command), wantedBy)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
