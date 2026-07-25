//go:build linux

package ops

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const nativeCloudflaredVersion = "2026.7.2"

var nativeCloudflaredSHA256 = map[string]string{
	"amd64": "ec905ea7b7e327ff8abdde8cb64697a2152de74dbcdbf6aec9db8364eb3886cd",
	"arm64": "405df476437e027fc6d18729a5a77155c0a33a6082aeee60a799a688f3052e66",
}

// Native Linux connectors are supervised as user services so they survive a
// host-agent restart. The token is kept in a mode-0600 EnvironmentFile and is
// never included in the durable operation result.
func ensureWindowsCloudflaredTunnel(_ EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	return nil, nil
}

func stopWindowsCloudflaredTunnel(_ string) error { return nil }

func isWindowsTunnelConnected(_ string) bool { return false }

func configuredNativeLinuxCloudflaredBinary() string {
	if configured := strings.TrimSpace(os.Getenv("OPUTE_CLOUDFLARED_BINARY_PATH")); configured != "" {
		return configured
	}
	return ""
}

func installedNativeLinuxCloudflaredPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "opute", "cloudflared"), nil
}

func ensureNativeLinuxCloudflaredBinary() (string, error) {
	if configured := configuredNativeLinuxCloudflaredBinary(); configured != "" {
		if info, err := os.Stat(configured); err != nil || info.Mode()&0111 == 0 {
			return "", fmt.Errorf("configured native cloudflared binary is not executable")
		}
		return configured, nil
	}
	if systemBinary, err := exec.LookPath("cloudflared"); err == nil {
		return systemBinary, nil
	}
	target, err := installedNativeLinuxCloudflaredPath()
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(target); statErr == nil && info.Mode()&0111 != 0 {
		return target, nil
	}
	expectedChecksum, ok := nativeCloudflaredSHA256[runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("automatic cloudflared install is unsupported on linux/%s", runtime.GOARCH)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return "", fmt.Errorf("create cloudflared install directory: %w", err)
	}
	downloadURL := fmt.Sprintf(
		"https://github.com/cloudflare/cloudflared/releases/download/%s/cloudflared-linux-%s",
		nativeCloudflaredVersion,
		runtime.GOARCH,
	)
	client := &http.Client{Timeout: 2 * time.Minute}
	response, err := client.Get(downloadURL)
	if err != nil {
		return "", fmt.Errorf("download cloudflared: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download cloudflared: HTTP %d", response.StatusCode)
	}
	temp, err := os.CreateTemp(filepath.Dir(target), "cloudflared-*")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temp, hash), response.Body); err != nil {
		_ = temp.Close()
		return "", fmt.Errorf("download cloudflared: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if fmt.Sprintf("%x", hash.Sum(nil)) != expectedChecksum {
		return "", fmt.Errorf("downloaded cloudflared checksum mismatch")
	}
	if err := os.Chmod(tempPath, 0700); err != nil {
		return "", err
	}
	if err := os.Rename(tempPath, target); err != nil {
		return "", err
	}
	return target, nil
}

func nativeLinuxCloudflaredPaths(bindingID string) (unitPath, envPath string, err error) {
	if strings.TrimSpace(bindingID) == "" || strings.ContainsAny(bindingID, "\r\n\x00") || strings.ContainsAny(bindingID, "/\\") {
		return "", "", fmt.Errorf("bindingId is invalid")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolve home directory: %w", err)
	}
	base := filepath.Join(home, ".config", "opute", "cloudflared")
	return filepath.Join(home, ".config", "systemd", "user", "opute-cloudflared-"+bindingID+".service"), filepath.Join(base, bindingID+".env"), nil
}

func nativeLinuxCloudflaredUnit(bindingID, envPath, binary, pidPath string) string {
	return fmt.Sprintf(`[Unit]
Description=Opute-managed Cloudflared connector (%s)
After=network-online.target

[Service]
EnvironmentFile=%s
ExecStart=%s tunnel --no-autoupdate --pidfile %s run
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`, bindingID, envPath, binary, pidPath)
}

func ensureNativeLinuxCloudflared(s *HostOperationsService, args EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	if args.Quick {
		return nil, fmt.Errorf("quick cloudflared tunnels are not supported by the native Linux service")
	}
	binary, err := ensureNativeLinuxCloudflaredBinary()
	if err != nil {
		return nil, err
	}
	unitPath, envPath, err := nativeLinuxCloudflaredPaths(args.BindingID)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(args.RunToken, "\r\n\x00") || strings.TrimSpace(args.RunToken) == "" {
		return nil, fmt.Errorf("runToken is invalid")
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(envPath), 0700); err != nil {
		return nil, err
	}
	// systemd EnvironmentFile accepts single-quoted values; escape the only
	// character that could terminate that quoting form.
	escapedToken := strings.ReplaceAll(args.RunToken, "'", "'\\''")
	if err := os.WriteFile(envPath, []byte("TUNNEL_TOKEN='"+escapedToken+"'\n"), 0600); err != nil {
		return nil, err
	}
	pidPath := envPath + ".pid"
	_ = os.Remove(pidPath)
	unit := nativeLinuxCloudflaredUnit(args.BindingID, envPath, binary, pidPath)
	if err := os.WriteFile(unitPath, []byte(unit), 0600); err != nil {
		return nil, err
	}
	for _, command := range [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", filepath.Base(unitPath)}, {"systemctl", "--user", "restart", filepath.Base(unitPath)}} {
		result, runErr := s.hostCommandRunner(command, nil, 30*time.Second)
		if runErr != nil {
			return nil, runErr
		}
		if result.ExitCode != 0 {
			return nil, fmt.Errorf("cloudflared systemd operation failed")
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(pidPath); statErr == nil && isNativeLinuxCloudflaredRunning(args.BindingID) {
			return &EnsureCloudflaredTunnelResult{BindingID: args.BindingID, Hostname: args.Hostname, LocalTarget: args.LocalTarget, TunnelStatus: "connected", ServiceRunning: true}, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, fmt.Errorf("cloudflared did not establish a tunnel connection within 30s")
}

func isNativeLinuxCloudflaredRunning(bindingID string) bool {
	unitPath, _, err := nativeLinuxCloudflaredPaths(bindingID)
	if err != nil {
		return false
	}
	err = exec.Command("systemctl", "--user", "is-active", "--quiet", filepath.Base(unitPath)).Run()
	return err == nil
}

func stopNativeLinuxCloudflaredTunnel(bindingID string) error {
	unitPath, envPath, err := nativeLinuxCloudflaredPaths(bindingID)
	if err != nil {
		return err
	}
	unit := filepath.Base(unitPath)
	_ = exec.Command("systemctl", "--user", "disable", "--now", unit).Run()
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	_ = os.Remove(unitPath)
	_ = os.Remove(envPath)
	_ = os.Remove(envPath + ".pid")
	return nil
}
