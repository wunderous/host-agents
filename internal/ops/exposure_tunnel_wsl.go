//go:build linux

package ops

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	hostexec "github.com/opute-io/host-agents/internal/exec"
)

const wslCloudflaredDownloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe"

func wslCloudflaredInstallDirPath() string {
	if fromEnv := strings.TrimSpace(os.Getenv("OPUTE_CLOUDFLARED_INSTALL_DIR")); fromEnv != "" {
		return fromEnv
	}
	if profile := os.Getenv("USERPROFILE"); profile != "" {
		windowsPath := strings.TrimPrefix(profile, `\\?\`)
		windowsPath = strings.ReplaceAll(windowsPath, `\`, `/`)
		if strings.HasPrefix(strings.ToLower(windowsPath), "c:/") {
			return "/mnt/c" + strings.TrimPrefix(windowsPath, "C:") + "/AppData/Local/opute/cloudflared"
		}
	}
	return "/mnt/c/Users/houma/AppData/Local/opute/cloudflared"
}

func wslCloudflaredBinaryPath() string {
	return filepath.Join(wslCloudflaredInstallDirPath(), "cloudflared.exe")
}

var (
	wslWindowsTunnelMu sync.Mutex
	wslWindowsTunnels  = map[string]bool{}
)

func isRunningInWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}

func ensureWindowsCloudflaredViaWSL(s *HostOperationsService, args EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	token := strings.TrimSpace(args.RunToken)
	if token == "" {
		return nil, fmt.Errorf("runToken is required for cloudflared tunnel")
	}

	if err := os.MkdirAll(wslCloudflaredInstallDirPath(), 0o755); err != nil {
		return nil, fmt.Errorf("create cloudflared install dir: %w", err)
	}
	if _, err := ensureWindowsCloudflaredBinaryWSL(); err != nil {
		return nil, err
	}
	tokenPath := filepath.Join(wslCloudflaredInstallDirPath(), fmt.Sprintf("run-token-%s.txt", sanitizeBindingFileToken(args.BindingID)))
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		return nil, fmt.Errorf("write cloudflared run token: %w", err)
	}
	windowsTokenPath := wslRepoToWindowsPath(tokenPath)

	psScript := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$installDir = Join-Path $env:LOCALAPPDATA 'opute\cloudflared'
$binary = Join-Path $installDir 'cloudflared.exe'
$tokenPath = '%s'
$token = (Get-Content -Raw -Path $tokenPath).Trim()
Get-Process cloudflared -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Process -FilePath $binary -ArgumentList @('tunnel','run','--token',$token) -WindowStyle Hidden
`, windowsTokenPath)

	result, err := s.hostCommandRunner(
		[]string{"powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", psScript},
		nil,
		120*time.Second,
	)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		stderr := strings.TrimSpace(result.Stderr)
		if stderr == "" {
			stderr = strings.TrimSpace(result.Stdout)
		}
		return nil, fmt.Errorf("start windows cloudflared via WSL: %s", stderr)
	}

	wslWindowsTunnelMu.Lock()
	wslWindowsTunnels[args.BindingID] = true
	wslWindowsTunnelMu.Unlock()

	return &EnsureCloudflaredTunnelResult{
		BindingID:      args.BindingID,
		Hostname:       args.Hostname,
		LocalTarget:    args.LocalTarget,
		TunnelStatus:   "connected",
		ServiceRunning: true,
	}, nil
}

func isWSLWindowsCloudflaredRunning(bindingID string) bool {
	wslWindowsTunnelMu.Lock()
	tracked := wslWindowsTunnels[bindingID]
	wslWindowsTunnelMu.Unlock()
	if !tracked {
		return false
	}
	result, err := hostexec.RunCommand(
		[]string{
			"powershell.exe",
			"-NoProfile",
			"-Command",
			"if (Get-Process cloudflared -ErrorAction SilentlyContinue) { exit 0 } else { exit 1 }",
		},
		nil,
		15*time.Second,
	)
	if err != nil {
		return false
	}
	return result.ExitCode == 0
}

func stopWSLWindowsCloudflaredTunnel(bindingID string) error {
	wslWindowsTunnelMu.Lock()
	delete(wslWindowsTunnels, bindingID)
	wslWindowsTunnelMu.Unlock()

	result, err := hostexec.RunCommand(
		[]string{
			"powershell.exe",
			"-NoProfile",
			"-Command",
			"Get-Process cloudflared -ErrorAction SilentlyContinue | Stop-Process -Force",
		},
		nil,
		30*time.Second,
	)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("stop windows cloudflared: %s", strings.TrimSpace(result.Stderr))
	}
	return nil
}

func ensureWindowsCloudflaredBinaryWSL() (string, error) {
	binaryPath := wslCloudflaredBinaryPath()
	if info, err := os.Stat(binaryPath); err == nil && info.Size() > 1024*1024 {
		return binaryPath, nil
	}
	res, err := http.Get(wslCloudflaredDownloadURL)
	if err != nil {
		return "", fmt.Errorf("download cloudflared: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return "", fmt.Errorf("download cloudflared: HTTP %d", res.StatusCode)
	}
	tmp := binaryPath + ".download"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, res.Body); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, binaryPath); err != nil {
		return "", err
	}
	return binaryPath, nil
}

func sanitizeBindingFileToken(bindingID string) string {
	token := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '-'
		}
	}, bindingID)
	if token == "" {
		return "binding"
	}
	return token
}
