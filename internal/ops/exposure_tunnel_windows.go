//go:build windows

package ops

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

const (
	cloudflaredInstallDir  = `C:\ProgramData\opute\cloudflared`
	cloudflaredDownloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe"
)

var (
	windowsTunnelMu      sync.Mutex
	windowsTunnelProcess = map[string]*exec.Cmd{}
)

func ensureWindowsCloudflaredTunnel(args EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	if strings.TrimSpace(args.RunToken) == "" {
		return nil, fmt.Errorf("runToken is required for cloudflared tunnel")
	}

	binaryPath, err := ensureCloudflaredBinary()
	if err != nil {
		return nil, err
	}

	if err := stopWindowsCloudflaredTunnel(args.BindingID); err != nil {
		return nil, err
	}

	cmd := exec.Command(binaryPath, "tunnel", "run")
	cmd.Env = append(os.Environ(), "TUNNEL_TOKEN="+args.RunToken)
	cmd.Dir = cloudflaredInstallDir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start cloudflared: %w", err)
	}

	windowsTunnelMu.Lock()
	windowsTunnelProcess[args.BindingID] = cmd
	windowsTunnelMu.Unlock()

	return &EnsureCloudflaredTunnelResult{
		BindingID:      args.BindingID,
		Hostname:       args.Hostname,
		LocalTarget:    args.LocalTarget,
		TunnelStatus:   "connected",
		ServiceRunning: true,
	}, nil
}

func stopWindowsCloudflaredTunnel(bindingID string) error {
	windowsTunnelMu.Lock()
	cmd := windowsTunnelProcess[bindingID]
	delete(windowsTunnelProcess, bindingID)
	windowsTunnelMu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func isWindowsTunnelConnected(bindingID string) bool {
	windowsTunnelMu.Lock()
	cmd := windowsTunnelProcess[bindingID]
	windowsTunnelMu.Unlock()
	return cmd != nil && cmd.Process != nil
}

func ensureNativeLinuxCloudflared(_ *HostOperationsService, _ EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	return nil, nil
}

func isNativeLinuxCloudflaredRunning(_ string) bool { return false }

func stopNativeLinuxCloudflaredTunnel(_ string) error { return nil }

func ensureCloudflaredBinary() (string, error) {
	if err := os.MkdirAll(cloudflaredInstallDir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(cloudflaredInstallDir, "cloudflared.exe")
	if _, err := os.Stat(target); err == nil {
		return target, nil
	}

	res, err := http.Get(cloudflaredDownloadURL)
	if err != nil {
		return "", fmt.Errorf("download cloudflared: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return "", fmt.Errorf("download cloudflared: HTTP %d", res.StatusCode)
	}

	tmp := target + ".download"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, res.Body); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, target); err != nil {
		return "", err
	}
	return target, nil
}
