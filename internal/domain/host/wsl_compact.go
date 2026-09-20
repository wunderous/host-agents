package host

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/wunderous/host-agents/internal/fingerprint"
)

const (
	wslVHDXLockedMessage = "sibling WSL VM still attached — compact needs shutdown_wsl (kills this agent)"
	liveDistroMessage    = "cannot compact the live distro that hosts this agent; run compact_wsl_disk from a sibling distro after shutdown_wsl"
)

type CompactWSLDiskArgs struct {
	Distro string `json:"distro"`
	DryRun bool   `json:"dryRun,omitempty"`
}

func (s *Service) CompactWSLDisk(ctx context.Context, args CompactWSLDiskArgs, onData func(string)) (map[string]any, error) {
	distro := strings.TrimSpace(args.Distro)
	if distro == "" {
		return nil, fmt.Errorf("distro is required")
	}
	if !fingerprint.DetectCapabilities().CanManageWSL {
		return nil, fmt.Errorf("wsl disk compact is unavailable: canManageWsl is false")
	}
	if err := rejectLiveDistro(currentWSLDistro(), distro); err != nil {
		return nil, compactClosed(map[string]any{"distro": distro, "live": true, "sparse": false}, err.Error())
	}
	wslCommand, ok := fingerprint.WindowsInteropCommand("wsl.exe")
	if !ok {
		return nil, fmt.Errorf("wsl.exe is unavailable")
	}
	state, err := s.wslDistributionState(ctx, wslCommand, distro, onData)
	if err != nil {
		return nil, err
	}
	vhdxPath, err := s.wslDistributionVHDX(ctx, distro, onData)
	if err != nil {
		return nil, err
	}
	locked, err := s.wslVHDXLocked(ctx, vhdxPath, onData)
	if err != nil {
		return nil, err
	}
	beforeBytes, err := s.windowsFileSize(ctx, vhdxPath, onData)
	if err != nil {
		return nil, err
	}
	report := map[string]any{
		"distro":      distro,
		"state":       state,
		"vhdxPath":    vhdxPath,
		"locked":      locked,
		"beforeBytes": beforeBytes,
		"dryRun":      args.DryRun,
		"sparse":      false,
	}
	if err := compactPreconditions(state, locked); err != nil {
		return nil, compactClosed(report, err.Error())
	}
	if args.DryRun {
		report["wouldCompact"] = true
		report["compacted"] = false
		return report, nil
	}
	powershell, ok := fingerprint.WindowsInteropCommand("powershell.exe")
	if !ok {
		return nil, fmt.Errorf("powershell.exe is unavailable")
	}
	script := fmt.Sprintf("Import-Module Hyper-V -ErrorAction Stop; Optimize-VHD -Path %s -Mode Full", powershellLiteral(vhdxPath))
	result, err := s.shared.HostCommandRunnerContext(ctx, []string{powershell, "-NoProfile", "-NonInteractive", "-Command", script}, onData, 30*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("Optimize-VHD: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, optimizeVHDError(result.ExitCode, strings.TrimSpace(result.Stderr+" "+result.Stdout))
	}
	afterBytes, err := s.windowsFileSize(ctx, vhdxPath, onData)
	if err != nil {
		return nil, err
	}
	report["afterBytes"] = afterBytes
	report["reclaimedBytes"] = beforeBytes - afterBytes
	report["compacted"] = true
	return report, nil
}

func rejectLiveDistro(current, distro string) error {
	if strings.TrimSpace(current) != "" && strings.EqualFold(strings.TrimSpace(current), strings.TrimSpace(distro)) {
		return fmt.Errorf("%s", liveDistroMessage)
	}
	return nil
}

func currentWSLDistro() string {
	if name := strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME")); name != "" {
		return name
	}
	for _, path := range []string{"/proc/self/environ", "/proc/1/environ"} {
		if name := wslDistroFromEnvironFile(path); name != "" {
			return name
		}
	}
	return ""
}

func wslDistroFromEnvironFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, item := range strings.Split(string(raw), "\x00") {
		key, value, ok := strings.Cut(item, "=")
		if ok && key == "WSL_DISTRO_NAME" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func compactPreconditions(state string, locked bool) error {
	if !strings.EqualFold(strings.TrimSpace(state), "Stopped") {
		return fmt.Errorf("distro is %s; compact requires Stopped. terminate_wsl_distribution is not enough if a sibling WSL VM still holds the VHDX — compact needs shutdown_wsl (kills this agent)", state)
	}
	if locked {
		return fmt.Errorf("%s", wslVHDXLockedMessage)
	}
	return nil
}

func compactClosed(report map[string]any, message string) error {
	return NewCompactClosedError(report, message)
}

// NewCompactClosedError is the fail-closed compact_wsl_disk result: the VHDX was
// not compacted, and the report explains why (live distro, Running, or locked
// by a sibling WSL VM). MCP surfaces this as isError with structuredContent.
func NewCompactClosedError(report map[string]any, message string) *CompactClosedError {
	return &CompactClosedError{message: message, report: report}
}

// CompactClosedError is the fail-closed compact_wsl_disk result: the VHDX was
// not compacted, and the report explains why (live distro, Running, or locked
// by a sibling WSL VM). MCP surfaces this as isError with structuredContent.
type CompactClosedError struct {
	message string
	report  map[string]any
}

func (e *CompactClosedError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *CompactClosedError) StructuredReport() map[string]any {
	if e == nil {
		return map[string]any{"code": "wsl_compact_closed", "owner": "capability"}
	}
	out := map[string]any{
		"code":      "wsl_compact_closed",
		"owner":     "capability",
		"message":   e.message,
		"compacted": false,
		"sparse":    false,
	}
	for key, value := range e.report {
		out[key] = value
	}
	return out
}

func optimizeVHDError(exitCode int, combined string) error {
	message := fmt.Sprintf("Optimize-VHD failed with exit code %d: %s", exitCode, combined)
	lower := strings.ToLower(combined)
	if strings.Contains(lower, "access is denied") || strings.Contains(lower, "permission denied") || (strings.Contains(lower, "hyper-v") && strings.Contains(lower, "not")) {
		return fmt.Errorf("%s: Optimize-VHD needs an elevated Hyper-V Administrators token (UAC). Do not enable WSL sparse VHDX", message)
	}
	return fmt.Errorf("%s", message)
}

func (s *Service) wslDistributionState(ctx context.Context, wslCommand, distro string, onData func(string)) (string, error) {
	result, err := s.shared.HostCommandRunnerContext(ctx, []string{wslCommand, "-l", "-v"}, onData, 15*time.Second)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("wsl.exe -l -v failed with exit code %d", result.ExitCode)
	}
	state, ok := parseWSLDistributionState(decodeWindowsCLI(result.Stdout+"\n"+result.Stderr), distro)
	if !ok {
		return "", fmt.Errorf("WSL distribution %q was not found", distro)
	}
	return state, nil
}

func (s *Service) wslDistributionVHDX(ctx context.Context, distro string, onData func(string)) (string, error) {
	regCommand, ok := fingerprint.WindowsInteropCommand("reg.exe")
	if !ok {
		return "", fmt.Errorf("reg.exe is unavailable")
	}
	result, err := s.shared.HostCommandRunnerContext(ctx, []string{regCommand, "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss`, "/s"}, onData, 15*time.Second)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("reg.exe query failed with exit code %d", result.ExitCode)
	}
	path, ok := parseWSLVHDXPath(decodeWindowsCLI(result.Stdout+"\n"+result.Stderr), distro)
	if !ok {
		return "", fmt.Errorf("VHDX path for distro %q was not found", distro)
	}
	return path, nil
}

func (s *Service) wslVHDXLocked(ctx context.Context, path string, onData func(string)) (bool, error) {
	powershell, ok := fingerprint.WindowsInteropCommand("powershell.exe")
	if !ok {
		return false, fmt.Errorf("powershell.exe is unavailable")
	}
	script := fmt.Sprintf("try { $fs = [System.IO.File]::Open(%s, 'Open', 'ReadWrite', 'None'); $fs.Close(); 'unlocked' } catch { 'locked' }", powershellLiteral(path))
	result, err := s.shared.HostCommandRunnerContext(ctx, []string{powershell, "-NoProfile", "-NonInteractive", "-Command", script}, onData, 15*time.Second)
	if err != nil {
		return false, err
	}
	output := strings.ToLower(strings.TrimSpace(decodeWindowsCLI(result.Stdout)))
	return strings.Contains(output, "locked"), nil
}

func (s *Service) windowsFileSize(ctx context.Context, path string, onData func(string)) (int64, error) {
	powershell, ok := fingerprint.WindowsInteropCommand("powershell.exe")
	if !ok {
		return 0, fmt.Errorf("powershell.exe is unavailable")
	}
	script := fmt.Sprintf("(Get-Item -LiteralPath %s).Length", powershellLiteral(path))
	result, err := s.shared.HostCommandRunnerContext(ctx, []string{powershell, "-NoProfile", "-NonInteractive", "-Command", script}, onData, 15*time.Second)
	if err != nil {
		return 0, err
	}
	if result.ExitCode != 0 {
		return 0, fmt.Errorf("Get-Item %s failed with exit code %d", path, result.ExitCode)
	}
	text := strings.TrimSpace(decodeWindowsCLI(result.Stdout))
	size, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse VHDX size %q: %w", text, err)
	}
	return size, nil
}

func parseWSLDistributionState(output, distro string) (string, bool) {
	want := strings.ToLower(strings.TrimSpace(distro))
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		if line == "" || strings.HasPrefix(strings.ToLower(line), "name ") || strings.HasPrefix(line, "---") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.ToLower(fields[0]) == want {
			return fields[1], true
		}
	}
	return "", false
}

func parseWSLVHDXPath(output, distro string) (string, bool) {
	want := strings.ToLower(strings.TrimSpace(distro))
	blocks := strings.Split(output, "HKEY_")
	for _, block := range blocks {
		name := registryValue(block, "DistributionName")
		base := registryValue(block, "BasePath")
		if strings.ToLower(name) != want || strings.TrimSpace(base) == "" {
			continue
		}
		base = strings.Trim(base, `"`)
		base = strings.TrimRight(base, `\`)
		if strings.HasSuffix(strings.ToLower(base), "ext4.vhdx") {
			return base, true
		}
		return base + `\ext4.vhdx`, true
	}
	return "", false
}

func registryValue(block, name string) string {
	for _, line := range strings.Split(block, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 || !strings.EqualFold(fields[0], name) {
			continue
		}
		return strings.Join(fields[2:], " ")
	}
	return ""
}

func decodeWindowsCLI(raw string) string {
	b := []byte(raw)
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		return string(utf16.Decode(bytesToUTF16(b[2:])))
	}
	zeros := 0
	for i := 1; i < len(b); i += 2 {
		if b[i] == 0 {
			zeros++
		}
	}
	if len(b) > 4 && zeros > len(b)/4 {
		return string(utf16.Decode(bytesToUTF16(b)))
	}
	return strings.ReplaceAll(raw, "\x00", "")
}

func bytesToUTF16(b []byte) []uint16 {
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
	}
	out := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		out = append(out, binary.LittleEndian.Uint16(b[i:i+2]))
	}
	return out
}

func powershellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
