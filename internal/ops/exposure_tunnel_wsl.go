//go:build linux

package ops

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
)

const wslCloudflaredDownloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe"

func useNativeWSLCloudflared() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("OPUTE_CLOUDFLARED_MODE")), "wsl") ||
		strings.TrimSpace(os.Getenv("OPUTE_CLOUDFLARED_BINARY_PATH")) != ""
}

func nativeWSLCloudflaredBinary() string {
	if path := strings.TrimSpace(os.Getenv("OPUTE_CLOUDFLARED_BINARY_PATH")); path != "" {
		return path
	}
	return "cloudflared"
}

var (
	nativeCloudflaredMu sync.Mutex
	nativeCloudflared   = map[string]*exec.Cmd{}
)

func ensureNativeWSLCloudflared(_ *HostOperationsService, args EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	binary := nativeWSLCloudflaredBinary()
	if _, err := exec.LookPath(binary); err != nil && filepath.Base(binary) == binary {
		return nil, fmt.Errorf("native cloudflared not found: %w", err)
	}

	nativeCloudflaredMu.Lock()
	if existing := nativeCloudflared[args.BindingID]; existing != nil && existing.Process != nil {
		nativeCloudflaredMu.Unlock()
		return &EnsureCloudflaredTunnelResult{BindingID: args.BindingID, Hostname: args.Hostname, LocalTarget: args.LocalTarget, TunnelStatus: "connected", ServiceRunning: true}, nil
	}
	nativeCloudflaredMu.Unlock()

	var command *exec.Cmd
	var output bytes.Buffer
	if args.Quick {
		command = exec.Command(binary, "tunnel", "--no-autoupdate", "--url", args.LocalTarget)
		command.Stdout = &output
		command.Stderr = &output
	} else {
		if strings.TrimSpace(args.RunToken) == "" {
			return nil, fmt.Errorf("runToken is required for named cloudflared tunnel")
		}
		command = exec.Command(binary, "tunnel", "run")
		command.Env = append(os.Environ(), "TUNNEL_TOKEN="+args.RunToken)
	}
	if !args.Quick {
		command.Stdout = io.Discard
		command.Stderr = io.Discard
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start native cloudflared: %w", err)
	}
	nativeCloudflaredMu.Lock()
	nativeCloudflared[args.BindingID] = command
	nativeCloudflaredMu.Unlock()
	go func() {
		_ = command.Wait()
		nativeCloudflaredMu.Lock()
		if nativeCloudflared[args.BindingID] == command {
			delete(nativeCloudflared, args.BindingID)
		}
		nativeCloudflaredMu.Unlock()
	}()

	publicURL := ""
	if args.Quick {
		pattern := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if match := pattern.FindString(output.String()); match != "" {
				publicURL = match
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	return &EnsureCloudflaredTunnelResult{BindingID: args.BindingID, Hostname: args.Hostname, LocalTarget: args.LocalTarget, TunnelStatus: "connected", ServiceRunning: true, PublicURL: publicURL}, nil
}

func isNativeWSLCloudflaredRunning(bindingID string) bool {
	nativeCloudflaredMu.Lock()
	command := nativeCloudflared[bindingID]
	nativeCloudflaredMu.Unlock()
	return command != nil && command.Process != nil
}

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
	wslQuickTunnels    = map[string]*exec.Cmd{}
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

func ensureQuickCloudflaredViaWSL(s *HostOperationsService, args EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	if strings.Contains(args.LocalTarget, "'") || strings.Contains(args.BindingID, "'") {
		return nil, fmt.Errorf("localTarget and bindingId cannot contain single quotes")
	}
	if err := os.MkdirAll(wslCloudflaredInstallDirPath(), 0o755); err != nil {
		return nil, fmt.Errorf("create cloudflared install dir: %w", err)
	}
	if _, err := ensureWindowsCloudflaredBinaryWSL(); err != nil {
		return nil, err
	}
	logPath := filepath.Join(wslCloudflaredInstallDirPath(), fmt.Sprintf("quick-%s.log", sanitizeBindingFileToken(args.BindingID)))
	errPath := filepath.Join(wslCloudflaredInstallDirPath(), fmt.Sprintf("quick-%s.err", sanitizeBindingFileToken(args.BindingID)))
	binary := wslRepoToWindowsPath(wslCloudflaredBinaryPath())
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open quick cloudflared log: %w", err)
	}
	errFile, err := os.OpenFile(errPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("open quick cloudflared error log: %w", err)
	}
	command := exec.Command(binary, "tunnel", "--no-autoupdate", "--url", args.LocalTarget)
	command.Stdout = logFile
	command.Stderr = errFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		_ = errFile.Close()
		return nil, fmt.Errorf("start quick cloudflared tunnel: %w", err)
	}
	_ = logFile.Close()
	_ = errFile.Close()
	wslWindowsTunnelMu.Lock()
	wslWindowsTunnels[args.BindingID] = true
	wslQuickTunnels[args.BindingID] = command
	wslWindowsTunnelMu.Unlock()
	go func() {
		_ = command.Wait()
		wslWindowsTunnelMu.Lock()
		if wslQuickTunnels[args.BindingID] == command {
			delete(wslQuickTunnels, args.BindingID)
			delete(wslWindowsTunnels, args.BindingID)
		}
		wslWindowsTunnelMu.Unlock()
	}()
	tunnelURL := ""
	pattern := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, outputPath := range []string{logPath, errPath} {
			if data, readErr := os.ReadFile(outputPath); readErr == nil {
				if match := pattern.FindString(string(data)); match != "" {
					tunnelURL = match
					break
				}
			}
		}
		if tunnelURL != "" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if tunnelURL == "" {
		_ = stopWSLWindowsCloudflaredTunnel(args.BindingID)
		return nil, fmt.Errorf("quick cloudflared tunnel did not publish a public URL within 30 seconds")
	}
	return &EnsureCloudflaredTunnelResult{
		BindingID: args.BindingID, Hostname: args.Hostname, LocalTarget: args.LocalTarget,
		TunnelStatus: "connected", ServiceRunning: true, PublicURL: tunnelURL,
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
	quick := wslQuickTunnels[bindingID]
	delete(wslQuickTunnels, bindingID)
	delete(wslWindowsTunnels, bindingID)
	wslWindowsTunnelMu.Unlock()
	if quick != nil && quick.Process != nil {
		_ = quick.Process.Kill()
	}

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

func stopNativeWSLCloudflaredTunnel(bindingID string) error {
	nativeCloudflaredMu.Lock()
	command := nativeCloudflared[bindingID]
	delete(nativeCloudflared, bindingID)
	nativeCloudflaredMu.Unlock()
	if command == nil || command.Process == nil {
		return nil
	}
	return command.Process.Kill()
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
