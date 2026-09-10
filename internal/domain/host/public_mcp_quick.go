package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/mcpprobe"
)

const (
	publicMCPQuickServicePrefix = "opute-cloudflared-quick-"
	publicMCPQuickManagedMarker = "# Managed by Opute Host Agent: public MCP quick tunnel\n"
	publicMCPQuickLogLimit      = 256 * 1024
	publicMCPQuickPollDelay     = 500 * time.Millisecond
)

// EnsurePublicMcpQuickTunnelArgs describes an ephemeral, credential-free
// Cloudflare Quick Tunnel. The origin is deliberately loopback-only: the
// capability exposes this Host Agent's own MCP endpoint and never becomes a
// generic proxy for another host.
type EnsurePublicMcpQuickTunnelArgs struct {
	BindingID      string
	LocalTarget    string
	ArtifactURI    string
	ArtifactSHA256 string
	Scope          string
}

// RemovePublicMcpQuickTunnelArgs identifies one previously created Quick
// Tunnel. Paths are derived from the binding and scope; callers cannot choose
// arbitrary files or units to delete.
type RemovePublicMcpQuickTunnelArgs struct {
	BindingID      string
	ArtifactURI    string
	ArtifactSHA256 string
	Scope          string
	Confirm        bool
}

type publicMCPQuickPaths struct {
	scope          string
	bindingID      string
	artifactURI    string
	artifactSHA256 string
	artifactPath   string
	serviceName    string
	serviceFile    string
	logFile        string
	stateFile      string
	unitArtifact   string
	unitLogFile    string
}

type publicMCPQuickState struct {
	ContractVersion string `json:"contractVersion"`
	Mode            string `json:"mode"`
	Stable          bool   `json:"stable"`
	BindingID       string `json:"bindingId"`
	LocalTarget     string `json:"localTarget"`
	Endpoint        string `json:"endpoint"`
}

// EnsurePublicMcpQuickTunnel installs a pinned cloudflared connector using
// Cloudflare's account-free Quick Tunnel mode. Cloudflared creates a random
// HTTPS hostname; the returned endpoint is therefore ephemeral and must not
// be presented as a stable production binding.
func (s *Service) EnsurePublicMcpQuickTunnel(ctx context.Context, args EnsurePublicMcpQuickTunnelArgs, onData func(string)) (map[string]any, error) {
	if err := s.shared.RequireSharedHostOwner("ensure_public_mcp_quick_tunnel"); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	paths, localTarget, originBase, err := resolvePublicMCPQuickPaths(args.BindingID, args.LocalTarget, args.ArtifactURI, args.ArtifactSHA256, args.Scope, true)
	if err != nil {
		return nil, err
	}
	artifact, err := s.ensurePublicMCPQuickArtifact(paths, onData)
	if err != nil {
		return nil, fmt.Errorf("ensure public MCP quick connector artifact: %w", err)
	}
	if err := requireOwnedManagedFile(paths.serviceFile, publicMCPManagedFileMarker); err != nil {
		return nil, err
	}
	if err := requireOwnedManagedFile(paths.logFile, publicMCPQuickManagedMarker); err != nil {
		return nil, err
	}
	if err := requireOwnedManagedFile(paths.stateFile, publicMCPQuickManagedMarker); err != nil {
		return nil, err
	}
	if _, err := s.ensurePublicMCPQuickLog(paths); err != nil {
		return nil, fmt.Errorf("prepare public MCP quick tunnel log: %w", err)
	}

	unit := renderPublicMCPQuickUnit(paths, originBase)
	unitObservation, err := s.EnsureHostFile(EnsureHostFileArgs{Path: paths.serviceFile, Content: unit, Mode: 0o600, Scope: paths.scope})
	if err != nil {
		return nil, fmt.Errorf("write public MCP quick connector service: %w", err)
	}
	if _, err := s.EnsureHostServiceSupervisor(EnsureHostServiceSupervisorArgs{Scope: paths.scope}, onData); err != nil {
		return nil, fmt.Errorf("ensure public MCP quick connector supervisor: %w", err)
	}

	active := false
	if observed, inspectErr := s.InspectHostService(InspectHostServiceArgs{ServiceName: paths.serviceName, Scope: paths.scope}, onData); inspectErr == nil {
		active, _ = observed["active"].(bool)
	}
	unitChanged, _ := unitObservation["changed"].(bool)
	serviceState := SetHostServiceStateArgs{ServiceName: paths.serviceName, Scope: paths.scope}
	if active && unitChanged {
		serviceState.State = "restart"
	} else {
		serviceState.State = "start"
	}
	if _, err := s.SetHostServiceState(serviceState, onData); err != nil {
		return nil, fmt.Errorf("start public MCP quick connector service: %w", err)
	}

	origin, err := mcpprobe.ProbeAuthenticatedMCPEndpoint(ctx, localTarget, "")
	if err != nil {
		return nil, fmt.Errorf("probe public MCP quick origin: %w", err)
	}
	if origin == nil || origin["ready"] != true {
		return nil, fmt.Errorf("public MCP quick origin is not ready")
	}

	endpoint := ""
	if !unitChanged && active {
		endpoint = readPublicMCPQuickState(paths, args.BindingID, localTarget)
		if endpoint != "" {
			if public, probeErr := mcpprobe.ProbeAuthenticatedMCPEndpoint(ctx, endpoint, ""); probeErr == nil {
				return publicMCPQuickObservation(paths, localTarget, endpoint, artifact, origin, public), nil
			}
			endpoint = ""
		}
	}
	if endpoint == "" {
		endpoint = readPublicMCPQuickEndpoint(paths.logFile)
	}
	if endpoint == "" || unitChanged || !active {
		if endpoint != "" {
			// A log from a prior Quick Tunnel is not evidence for the current
			// process. Clear it before starting a replacement and wait for a
			// freshly emitted hostname.
			if err := truncatePublicMCPQuickLog(paths); err != nil {
				return nil, fmt.Errorf("reset public MCP quick tunnel log: %w", err)
			}
			serviceState.State = "restart"
			if _, err := s.SetHostServiceState(serviceState, onData); err != nil {
				return nil, fmt.Errorf("restart public MCP quick connector service: %w", err)
			}
		}
		var waitErr error
		endpoint, waitErr = waitForPublicMCPQuickEndpoint(ctx, paths.logFile)
		if waitErr != nil {
			return nil, waitErr
		}
	}
	public, err := probePublicMCPWithRetry(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("probe authenticated public MCP quick endpoint: %w", err)
	}
	if err := s.writePublicMCPQuickState(paths, publicMCPQuickState{
		ContractVersion: publicMCPContractVersion,
		Mode:            "quick",
		Stable:          false,
		BindingID:       strings.TrimSpace(args.BindingID),
		LocalTarget:     localTarget,
		Endpoint:        endpoint,
	}); err != nil {
		return nil, fmt.Errorf("record public MCP quick tunnel endpoint: %w", err)
	}
	return publicMCPQuickObservation(paths, localTarget, endpoint, artifact, origin, public), nil
}

func publicMCPQuickObservation(paths publicMCPQuickPaths, localTarget, endpoint string, artifact, origin, public map[string]any) map[string]any {
	return map[string]any{
		"contractVersion": publicMCPContractVersion,
		"ready":           true,
		"mode":            "quick",
		"stable":          false,
		"bindingId":       paths.bindingID,
		"endpoint":        endpoint,
		"localTarget":     localTarget,
		"scope":           paths.scope,
		"serviceName":     paths.serviceName,
		"serviceFile":     paths.serviceFile,
		"logFile":         paths.logFile,
		"stateFile":       paths.stateFile,
		"artifact":        artifact,
		"origin":          origin,
		"publicAuth":      public,
	}
}

// RemovePublicMcpQuickTunnel stops and removes only the Opute-owned files for
// one derived binding. It reports every attempt and leaves failures visible
// with ready=false; a failed cleanup is never represented as a pass.
func (s *Service) RemovePublicMcpQuickTunnel(ctx context.Context, args RemovePublicMcpQuickTunnelArgs, onData func(string)) (map[string]any, error) {
	if err := s.shared.RequireSharedHostOwner("remove_public_mcp_quick_tunnel"); err != nil {
		return nil, err
	}
	if !args.Confirm {
		return nil, errors.New("remove_public_mcp_quick_tunnel requires confirm=true")
	}
	paths, _, _, err := resolvePublicMCPQuickPaths(args.BindingID, "http://127.0.0.1:1/mcp", args.ArtifactURI, args.ArtifactSHA256, args.Scope, false)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	attempts := make([]map[string]any, 0, 5)
	failures := make([]string, 0)
	serviceCommand := []string{hostruntime.DefaultSystemctlPath}
	if paths.scope == "user" {
		serviceCommand = append(serviceCommand, "--user")
	} else {
		serviceCommand = append([]string{"sudo", "-n"}, serviceCommand...)
	}
	serviceCommand = append(serviceCommand, "disable", "--now", paths.serviceName)
	serviceResult, serviceErr := s.shared.HostCommandRunnerContext(ctx, serviceCommand, onData, 30*time.Second)
	serviceExists := fileExists(paths.serviceFile)
	if serviceErr != nil || (serviceResult.ExitCode != 0 && serviceExists) {
		failures = append(failures, fmt.Sprintf("stop service %s: %s", paths.serviceName, firstNonEmptyString(serviceResult.Stderr, serviceResult.Stdout, errorString(serviceErr))))
		attempts = append(attempts, map[string]any{"resource": "service", "path": paths.serviceName, "removed": false, "error": failures[len(failures)-1]})
	} else {
		attempts = append(attempts, map[string]any{"resource": "service", "path": paths.serviceName, "removed": serviceExists})
	}

	for _, item := range []struct {
		name   string
		path   string
		marker string
	}{
		{name: "serviceFile", path: paths.serviceFile, marker: publicMCPManagedFileMarker},
		{name: "logFile", path: paths.logFile, marker: publicMCPQuickManagedMarker},
		{name: "stateFile", path: paths.stateFile, marker: publicMCPQuickManagedMarker},
	} {
		removed, removeErr := removeOwnedQuickFile(item.path, item.marker)
		attempt := map[string]any{"resource": item.name, "path": item.path, "removed": removed}
		if removeErr != nil {
			failures = append(failures, fmt.Sprintf("remove %s: %v", item.name, removeErr))
			attempt["error"] = removeErr.Error()
		}
		attempts = append(attempts, attempt)
	}
	artifactRemoved, artifactErr := removeOwnedQuickArtifact(paths)
	artifactAttempt := map[string]any{"resource": "artifact", "path": paths.artifactPath, "removed": artifactRemoved}
	if artifactErr != nil {
		failures = append(failures, fmt.Sprintf("remove artifact: %v", artifactErr))
		artifactAttempt["error"] = artifactErr.Error()
	}
	attempts = append(attempts, artifactAttempt)

	result := map[string]any{
		"contractVersion": publicMCPContractVersion,
		"ready":           len(failures) == 0,
		"status":          "removed",
		"bindingId":       paths.bindingID,
		"scope":           paths.scope,
		"serviceName":     paths.serviceName,
		"attempts":        attempts,
	}
	if len(failures) > 0 {
		result["status"] = "cleanup_failed"
		result["failures"] = failures
	}
	return result, nil
}

func resolvePublicMCPQuickPaths(bindingID, localTarget, artifactURI, artifactSHA, scope string, requireOrigin bool) (publicMCPQuickPaths, string, string, error) {
	bindingID = strings.TrimSpace(bindingID)
	if !publicMCPBindingPattern.MatchString(bindingID) {
		return publicMCPQuickPaths{}, "", "", fmt.Errorf("bindingId must contain only letters, numbers, dots, colons, underscores, and hyphens")
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = "user"
	}
	if scope != "user" && scope != "system" {
		return publicMCPQuickPaths{}, "", "", fmt.Errorf("scope must be user or system")
	}
	if scope == "system" && os.Geteuid() != 0 {
		return publicMCPQuickPaths{}, "", "", fmt.Errorf("system-scoped public MCP quick connector requires root")
	}
	artifactURI = strings.TrimSpace(artifactURI)
	if artifactURI == "" {
		artifactURI = defaultCloudflaredArtifactURI
	}
	if err := validateHostArtifactURI(artifactURI); err != nil {
		return publicMCPQuickPaths{}, "", "", err
	}
	normalizedSHA, err := normalizeSHA256(firstNonEmptyString(artifactSHA, defaultCloudflaredArtifactSHA256))
	if err != nil {
		return publicMCPQuickPaths{}, "", "", err
	}
	instanceID := normalizePublicMCPInstanceID(bindingID)
	serviceName := publicMCPQuickServicePrefix + instanceID + ".service"
	paths := publicMCPQuickPaths{scope: scope, bindingID: bindingID, artifactURI: artifactURI, artifactSHA256: normalizedSHA, serviceName: serviceName}
	if scope == "user" {
		home, homeErr := hostHomeDir()
		if homeErr != nil {
			return publicMCPQuickPaths{}, "", "", fmt.Errorf("resolve home directory: %w", homeErr)
		}
		root := filepath.Join(home, ".local", "share", "opute", "tunnels", "quick", instanceID)
		paths.artifactPath = filepath.Join(root, "cloudflared")
		paths.logFile = filepath.Join(root, "quick.log")
		paths.stateFile = filepath.Join(root, "endpoint.json")
		paths.serviceFile = filepath.Join(home, ".config", "systemd", "user", serviceName)
		paths.unitArtifact = filepath.Join("%h", ".local", "share", "opute", "tunnels", "quick", instanceID, "cloudflared")
		paths.unitLogFile = filepath.Join("%h", ".local", "share", "opute", "tunnels", "quick", instanceID, "quick.log")
	} else {
		root := filepath.Join("/opt", "opute", "tunnels", "quick", instanceID)
		paths.artifactPath = filepath.Join(root, "cloudflared")
		paths.logFile = filepath.Join(root, "quick.log")
		paths.stateFile = filepath.Join(root, "endpoint.json")
		paths.serviceFile = filepath.Join("/etc", "systemd", "system", serviceName)
		paths.unitArtifact = paths.artifactPath
		paths.unitLogFile = paths.logFile
	}
	if !requireOrigin {
		return paths, "", "", nil
	}
	parsed, err := url.Parse(strings.TrimSpace(localTarget))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return publicMCPQuickPaths{}, "", "", fmt.Errorf("localTarget must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if strings.TrimRight(parsed.Path, "/") != "/mcp" {
		return publicMCPQuickPaths{}, "", "", fmt.Errorf("localTarget must end exactly with /mcp for a quick tunnel")
	}
	hostname := strings.TrimSpace(parsed.Hostname())
	if hostname != "localhost" && !strings.EqualFold(hostname, "ip6-localhost") {
		if ip := netParseIP(hostname); ip == "" || !isLoopbackIP(ip) {
			return publicMCPQuickPaths{}, "", "", fmt.Errorf("quick tunnel localTarget must resolve to loopback")
		}
	}
	parsed.Path = ""
	parsed.RawPath = ""
	return paths, strings.TrimSpace(localTarget), parsed.String(), nil
}

// These small wrappers keep the path validation above readable while using
// the standard library parser without introducing a second host abstraction.
func netParseIP(hostname string) string {
	if ip := net.ParseIP(hostname); ip != nil {
		return ip.String()
	}
	return ""
}

func isLoopbackIP(value string) bool {
	ip := net.ParseIP(value)
	return ip != nil && ip.IsLoopback()
}

func (s *Service) ensurePublicMCPQuickArtifact(paths publicMCPQuickPaths, onData func(string)) (map[string]any, error) {
	if paths.scope == "user" {
		home, err := hostHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home directory: %w", err)
		}
		owned, err := hostOwnedPath(home, paths.artifactPath)
		if err != nil || owned != filepath.Clean(paths.artifactPath) {
			return nil, fmt.Errorf("artifactPath must remain beneath the current user's Opute quick tunnel directory")
		}
	} else if !strings.HasPrefix(filepath.Clean(paths.artifactPath), "/opt/opute/tunnels/quick/") {
		return nil, fmt.Errorf("system artifactPath must remain beneath /opt/opute/tunnels/quick")
	}
	if info, err := os.Lstat(paths.artifactPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to use symlink at quick tunnel artifact path")
	}
	if info, err := os.Lstat(paths.artifactPath); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("refusing to use non-regular quick tunnel artifact path")
		}
		observed, hashErr := hostArtifactFileSHA256(paths.artifactPath)
		if hashErr != nil {
			return nil, hashErr
		}
		if !strings.EqualFold(observed, paths.artifactSHA256) {
			return nil, fmt.Errorf("quick tunnel artifact hash mismatch; refusing to overwrite existing file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect quick tunnel artifact path: %w", err)
	}
	return s.ensureHostArtifactAt(paths.artifactURI, paths.artifactPath, paths.artifactSHA256, true, onData)
}

func (s *Service) ensurePublicMCPQuickLog(paths publicMCPQuickPaths) (map[string]any, error) {
	if info, err := os.Lstat(paths.logFile); errors.Is(err, os.ErrNotExist) {
		content := publicMCPQuickManagedMarker
		if paths.scope == "user" {
			return s.EnsureHostFile(EnsureHostFileArgs{Path: paths.logFile, Content: content, Mode: 0o600, Scope: "user"})
		}
		return ensurePrivateManagedFile(paths.logFile, content, 0o600)
	} else if err != nil {
		return nil, fmt.Errorf("inspect quick tunnel log: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("quick tunnel log is not a regular file")
	}
	return map[string]any{"path": paths.logFile, "changed": false}, nil
}

func renderPublicMCPQuickUnit(paths publicMCPQuickPaths, originBase string) string {
	wantedBy := "default.target"
	if paths.scope == "system" {
		wantedBy = "multi-user.target"
	}
	command := fmt.Sprintf("exec %s tunnel --no-autoupdate --url %s >> %s 2>&1", shellQuote(paths.unitArtifact), shellQuote(originBase), shellQuote(paths.unitLogFile))
	return publicMCPManagedFileMarker + fmt.Sprintf("[Unit]\nDescription=Opute ephemeral authenticated public MCP quick tunnel\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nExecStart=/bin/sh -c %s\nRestart=always\nRestartSec=15\nKillMode=control-group\n\n[Install]\nWantedBy=%s\n", shellQuote(command), wantedBy)
}

func readPublicMCPQuickEndpoint(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > publicMCPQuickLogLimit {
		return ""
	}
	for _, field := range strings.Fields(string(data)) {
		field = strings.Trim(field, "\"'`()[]{}<>,.;")
		if endpoint := validatePublicMCPQuickEndpoint(field); endpoint != "" {
			return endpoint
		}
	}
	return ""
}

func validatePublicMCPQuickEndpoint(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".trycloudflare.com") || strings.TrimRight(parsed.Path, "/") != "" {
		return ""
	}
	parsed.Path = "/mcp"
	parsed.RawPath = ""
	endpoint, err := mcpprobe.ValidateEndpoint(parsed.String())
	if err != nil {
		return ""
	}
	return endpoint
}

func waitForPublicMCPQuickEndpoint(ctx context.Context, path string) (string, error) {
	deadline := time.Now().Add(publicMCPProbeRetryWindow)
	for {
		if endpoint := readPublicMCPQuickEndpoint(path); endpoint != "" {
			return endpoint, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("public MCP quick tunnel did not emit a trycloudflare.com endpoint within %s", publicMCPProbeRetryWindow)
		}
		timer := time.NewTimer(publicMCPQuickPollDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return "", ctx.Err()
		case <-timer.C:
		}
	}
}

func readPublicMCPQuickState(paths publicMCPQuickPaths, bindingID, localTarget string) string {
	data, err := os.ReadFile(paths.stateFile)
	if err != nil || len(data) > 16*1024 || !strings.HasPrefix(string(data), publicMCPQuickManagedMarker) {
		return ""
	}
	var state publicMCPQuickState
	if json.Unmarshal([]byte(strings.TrimPrefix(string(data), publicMCPQuickManagedMarker)), &state) != nil || state.ContractVersion != publicMCPContractVersion || state.Mode != "quick" || state.Stable || state.BindingID != strings.TrimSpace(bindingID) || state.LocalTarget != localTarget {
		return ""
	}
	return validatePublicMCPQuickEndpoint(state.Endpoint)
}

func (s *Service) writePublicMCPQuickState(paths publicMCPQuickPaths, state publicMCPQuickState) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	content := publicMCPQuickManagedMarker + string(encoded) + "\n"
	if paths.scope == "user" {
		_, err = s.EnsureHostFile(EnsureHostFileArgs{Path: paths.stateFile, Content: content, Mode: 0o600, Scope: "user"})
		return err
	}
	_, err = ensurePrivateManagedFile(paths.stateFile, content, 0o600)
	return err
}

func truncatePublicMCPQuickLog(paths publicMCPQuickPaths) error {
	if err := requireOwnedManagedFile(paths.logFile, publicMCPQuickManagedMarker); err != nil {
		return err
	}
	file, err := os.OpenFile(paths.logFile, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(publicMCPQuickManagedMarker)
	return err
}

func removeOwnedQuickFile(path, marker string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("refusing to remove non-regular managed path %q", path)
	}
	if err := requireOwnedManagedFile(path, marker); err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

func removeOwnedQuickArtifact(paths publicMCPQuickPaths) (bool, error) {
	info, err := os.Lstat(paths.artifactPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("refusing to remove non-regular quick tunnel artifact")
	}
	observed, err := hostArtifactFileSHA256(paths.artifactPath)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(observed, paths.artifactSHA256) {
		return false, fmt.Errorf("quick tunnel artifact hash mismatch; refusing removal")
	}
	if err := os.Remove(paths.artifactPath); err != nil {
		return false, err
	}
	return true, nil
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
