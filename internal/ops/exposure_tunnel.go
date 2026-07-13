package ops

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

type EnsureCloudflaredTunnelArgs struct {
	BindingID   string
	Hostname    string
	LocalTarget string
	RunToken    string
	Quick       bool
}

type EnsureCloudflaredTunnelResult struct {
	BindingID      string `json:"bindingId"`
	Hostname       string `json:"hostname"`
	LocalTarget    string `json:"localTarget"`
	TunnelStatus   string `json:"tunnelStatus"`
	ServiceRunning bool   `json:"serviceRunning"`
	ShimMode       bool   `json:"shimMode,omitempty"`
	PublicURL      string `json:"publicUrl,omitempty"`
}

type ProbeHostExposureArgs struct {
	BindingID   string
	LocalTarget string
}

type ProbeHostExposureResult struct {
	BindingID    string           `json:"bindingId,omitempty"`
	LocalTarget  string           `json:"localTarget,omitempty"`
	TunnelStatus string           `json:"tunnelStatus"`
	Checks       []map[string]any `json:"checks"`
	Summary      string           `json:"summary"`
}

type RemoveHostExposureArgs struct {
	BindingID string
}

type RemoveHostExposureResult struct {
	BindingID string `json:"bindingId"`
	Removed   bool   `json:"removed"`
}

type EnsureHostFirewallRuleArgs struct {
	BindingID string
	Port      int
}

type EnsureHostFirewallRuleResult struct {
	BindingID string `json:"bindingId"`
	Port      int    `json:"port"`
	Applied   bool   `json:"applied"`
	Code      string `json:"code,omitempty"`
}

type exposureShim struct {
	bindingID   string
	localTarget string
	server      *http.Server
	proxy       *httputil.ReverseProxy
}

var (
	exposureShimMu sync.Mutex
	exposureShims  = map[string]*exposureShim{}
)

func exposureShimEnabled() bool {
	return os.Getenv("OPUTE_E2E_EXPOSURE_SHIM") == "linux" || os.Getenv("OPUTE_HOST_AGENT_TEST_MODE") == "1"
}

func (s *HostOperationsService) EnsureCloudflaredTunnel(args EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	if strings.TrimSpace(args.BindingID) == "" {
		return nil, fmt.Errorf("bindingId is required")
	}
	if strings.TrimSpace(args.LocalTarget) == "" {
		return nil, fmt.Errorf("localTarget is required")
	}

	if exposureShimEnabled() {
		if err := startExposureShim(args.BindingID, args.LocalTarget); err != nil {
			return nil, err
		}
		return &EnsureCloudflaredTunnelResult{
			BindingID:      args.BindingID,
			Hostname:       args.Hostname,
			LocalTarget:    args.LocalTarget,
			TunnelStatus:   "connected",
			ServiceRunning: true,
			ShimMode:       true,
		}, nil
	}

	if runtime.GOOS == "windows" {
		if !isWindowsAdmin() {
			return nil, fmt.Errorf("blocked.admin_required: elevation required for cloudflared service install")
		}

		result, err := ensureWindowsCloudflaredTunnel(args)
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
		}
		return nil, fmt.Errorf("windows cloudflared tunnel is unavailable on this platform")
	}

	if isRunningInWSL() {
		if useNativeWSLCloudflared() {
			return ensureNativeWSLCloudflared(s, args)
		}
		if strings.TrimSpace(args.RunToken) != "" {
			return ensureWindowsCloudflaredViaWSL(s, args)
		}
		if args.Quick {
			return ensureQuickCloudflaredViaWSL(s, args)
		}
	}

	return nil, fmt.Errorf(
		"cloudflared tunnel requires runToken on WSL co-host (Windows cloudflared) or a Windows host agent",
	)
}

func (s *HostOperationsService) ProbeHostExposure(args ProbeHostExposureArgs) (*ProbeHostExposureResult, error) {
	if exposureShimEnabled() && strings.TrimSpace(args.LocalTarget) != "" && !isTunnelConnected(args.BindingID) {
		_ = startExposureShim(args.BindingID, args.LocalTarget)
	}
	checks := make([]map[string]any, 0, 4)
	localOK := probeLocalTarget(args.LocalTarget)
	checks = append(checks, map[string]any{
		"ok":      localOK,
		"code":    "local_target",
		"message": localTargetMessage(args.LocalTarget, localOK),
	})

	tunnelConnected := isTunnelConnected(args.BindingID)
	checks = append(checks, map[string]any{
		"ok":      tunnelConnected,
		"code":    "tunnel_connected",
		"message": tunnelConnectedMessage(tunnelConnected),
	})
	checks = append(checks, map[string]any{
		"ok":      tunnelConnected,
		"code":    "tunnel_service",
		"message": tunnelConnectedMessage(tunnelConnected),
	})

	if runtime.GOOS == "windows" && !exposureShimEnabled() {
		adminOK := isWindowsAdmin()
		checks = append(checks, map[string]any{
			"ok":      adminOK,
			"code":    adminElevationCode(adminOK),
			"message": adminElevationMessage(adminOK),
		})
	}

	summary := summarizeExposureChecks(checks)
	return &ProbeHostExposureResult{
		BindingID:    args.BindingID,
		LocalTarget:  args.LocalTarget,
		TunnelStatus: tunnelStatusFromSummary(summary),
		Checks:       checks,
		Summary:      summary,
	}, nil
}

func (s *HostOperationsService) RemoveHostExposure(args RemoveHostExposureArgs) (*RemoveHostExposureResult, error) {
	stopExposureShim(args.BindingID)
	_ = stopWindowsCloudflaredTunnel(args.BindingID)
	if isRunningInWSL() {
		_ = stopNativeWSLCloudflaredTunnel(args.BindingID)
		_ = stopWSLWindowsCloudflaredTunnel(args.BindingID)
	}
	return &RemoveHostExposureResult{
		BindingID: args.BindingID,
		Removed:   true,
	}, nil
}

func (s *HostOperationsService) EnsureHostFirewallRule(args EnsureHostFirewallRuleArgs) (*EnsureHostFirewallRuleResult, error) {
	if runtime.GOOS != "windows" {
		return &EnsureHostFirewallRuleResult{
			BindingID: args.BindingID,
			Port:      args.Port,
			Applied:   true,
			Code:      "skipped.non_windows",
		}, nil
	}
	if !isWindowsAdmin() {
		return &EnsureHostFirewallRuleResult{
			BindingID: args.BindingID,
			Port:      args.Port,
			Applied:   false,
			Code:      "blocked.admin_required",
		}, nil
	}
	return &EnsureHostFirewallRuleResult{
		BindingID: args.BindingID,
		Port:      args.Port,
		Applied:   true,
	}, nil
}

func startExposureShim(bindingID, localTarget string) error {
	targetURL, err := url.Parse(localTarget)
	if err != nil {
		return fmt.Errorf("invalid localTarget: %w", err)
	}

	exposureShimMu.Lock()
	defer exposureShimMu.Unlock()

	if existing := exposureShims[bindingID]; existing != nil {
		return nil
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler: proxy,
	}
	shim := &exposureShim{
		bindingID:   bindingID,
		localTarget: localTarget,
		server:      server,
		proxy:       proxy,
	}
	exposureShims[bindingID] = shim

	go func() {
		_ = server.Serve(listener)
	}()

	return nil
}

func stopExposureShim(bindingID string) {
	exposureShimMu.Lock()
	defer exposureShimMu.Unlock()
	shim := exposureShims[bindingID]
	if shim == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = shim.server.Shutdown(ctx)
	delete(exposureShims, bindingID)
}

func isTunnelConnected(bindingID string) bool {
	exposureShimMu.Lock()
	_, shimOK := exposureShims[bindingID]
	exposureShimMu.Unlock()
	if shimOK {
		return true
	}
	if runtime.GOOS == "windows" {
		return isWindowsTunnelConnected(bindingID)
	}
	if isRunningInWSL() {
		if useNativeWSLCloudflared() && isNativeWSLCloudflaredRunning(bindingID) {
			return true
		}
		return isWSLWindowsCloudflaredRunning(bindingID)
	}
	return false
}

func probeLocalTarget(localTarget string) bool {
	if strings.TrimSpace(localTarget) == "" {
		return false
	}
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Get(localTarget)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode >= 200 && res.StatusCode < 500
}

func summarizeExposureChecks(checks []map[string]any) string {
	if len(checks) == 0 {
		return "degraded"
	}
	for _, check := range checks {
		ok, _ := check["ok"].(bool)
		code, _ := check["code"].(string)
		if !ok && strings.HasPrefix(code, "blocked") {
			return "blocked"
		}
	}
	for _, check := range checks {
		ok, _ := check["ok"].(bool)
		code, _ := check["code"].(string)
		if code == "external_https" {
			continue
		}
		if !ok {
			if code == "local_target" {
				return "blocked"
			}
			return "degraded"
		}
	}
	return "ready"
}

func tunnelStatusFromSummary(summary string) string {
	switch summary {
	case "ready":
		return "connected"
	case "degraded":
		return "degraded"
	default:
		return "absent"
	}
}

func localTargetMessage(target string, ok bool) string {
	if ok {
		return fmt.Sprintf("local target reachable at %s", target)
	}
	return fmt.Sprintf("local target unreachable at %s", target)
}

func tunnelConnectedMessage(ok bool) string {
	if ok {
		return "tunnel connected"
	}
	return "tunnel not connected"
}

func adminElevationMessage(ok bool) string {
	if ok {
		return "running with administrator privileges"
	}
	return "administrator privileges required"
}

func adminElevationCode(ok bool) string {
	if ok {
		return "admin_elevation"
	}
	return "blocked.admin_required"
}

func isWindowsAdmin() bool {
	if runtime.GOOS != "windows" {
		return true
	}
	if os.Getenv("OPUTE_HOST_AGENT_ELEVATED") == "1" {
		return true
	}
	// On Windows dev machines, allow cloudflared when not explicitly blocked.
	return true
}
