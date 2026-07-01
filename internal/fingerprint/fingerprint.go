package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const Version = "v1"

type Source string

const (
	SourceLinuxMachineID      Source = "linux-machine-id"
	SourceWindowsMachineGUID  Source = "windows-machine-guid"
	SourceMacOSPlatformUUID   Source = "macos-io-platform-uuid"
)

type Identity struct {
	Fingerprint        string `json:"fingerprint"`
	FingerprintVersion string `json:"fingerprintVersion"`
	FingerprintSource  Source `json:"fingerprintSource"`
}

func ReadIdentity() (Identity, error) {
	switch runtime.GOOS {
	case "linux":
		raw, err := os.ReadFile("/etc/machine-id")
		if err != nil {
			return Identity{}, err
		}
		v := strings.TrimSpace(string(raw))
		if v == "" {
			return Identity{}, fmt.Errorf("empty /etc/machine-id")
		}
		return format(SourceLinuxMachineID, v), nil
	case "windows":
		guid, err := readWindowsMachineGUID()
		if err != nil {
			return Identity{}, err
		}
		return format(SourceWindowsMachineGUID, guid), nil
	case "darwin":
		uuid, err := readMacPlatformUUID()
		if err != nil {
			return Identity{}, err
		}
		return format(SourceMacOSPlatformUUID, uuid), nil
	default:
		return Identity{}, fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
}

func format(source Source, value string) Identity {
	normalized := strings.ToLower(strings.TrimSpace(value))
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s", Version, source, normalized)))
	return Identity{
		Fingerprint:        fmt.Sprintf("host:%s:%s", Version, hex.EncodeToString(digest[:])),
		FingerprintVersion: Version,
		FingerprintSource:  source,
	}
}

func readWindowsMachineGUID() (string, error) {
	cmd := exec.Command("reg", "query", `HKLM\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "MachineGuid") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				return parts[len(parts)-1], nil
			}
		}
	}
	return "", fmt.Errorf("MachineGuid not found")
}

func readMacPlatformUUID() (string, error) {
	cmd := exec.Command("/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	const key = `"IOPlatformUUID" = "`
	for _, line := range strings.Split(string(out), "\n") {
		if idx := strings.Index(line, key); idx >= 0 {
			rest := line[idx+len(key):]
			if end := strings.Index(rest, `"`); end >= 0 {
				return rest[:end], nil
			}
		}
	}
	return "", fmt.Errorf("IOPlatformUUID not found")
}
