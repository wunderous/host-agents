// Package hostplatform detects the operating system and CPU the Host Agent
// process is running on. It is a read-only observation library shared by the
// in-process neutral capability and by out-of-process provider plugins, so it
// lives outside internal/ and takes no dependency on the agent runtime.
package hostplatform

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// ContractVersion pins the observation shape carried in durable evidence.
const ContractVersion = "host-platform.v1"

// Operating-system families. These are the coarse product-neutral values; the
// finer host kind lives in Platform.Kind.
const (
	OSLinux   = "linux"
	OSMacOS   = "macos"
	OSWindows = "windows"
)

// Host kinds. WSL is reported as its own kind because a Linux userland under
// a Windows kernel behaves differently from native Linux for service
// supervision, networking, and filesystem paths, and callers must be able to
// fail closed on that distinction rather than infer it from the OS alone.
const (
	KindLinux         = "linux"
	KindMacOS         = "macos"
	KindWindowsNative = "windows-native"
	KindWSL1          = "wsl1"
	KindWSL2          = "wsl2"
)

// CPU families.
const (
	FamilyX86_64       = "x86-64"
	FamilyX86          = "x86"
	FamilyARM64        = "arm64"
	FamilyARM          = "arm"
	FamilyAppleSilicon = "apple-silicon"
	FamilyUnknown      = "unknown"
)

// CPU is the observed processor identity.
type CPU struct {
	Architecture string `json:"architecture"`
	Family       string `json:"family"`
	Vendor       string `json:"vendor,omitempty"`
	Model        string `json:"model,omitempty"`
	Series       string `json:"series,omitempty"`
	Variant      string `json:"variant,omitempty"`
	LogicalCores int    `json:"logicalCores,omitempty"`
}

// WSL is present only when the host kind is a WSL kind.
type WSL struct {
	Version int    `json:"version"`
	Distro  string `json:"distro,omitempty"`
	Interop bool   `json:"interop"`
}

// Platform is the provider-neutral host platform observation.
type Platform struct {
	ContractVersion     string   `json:"contractVersion"`
	OS                  string   `json:"os"`
	Kind                string   `json:"kind"`
	Kernel              string   `json:"kernel,omitempty"`
	KernelVersion       string   `json:"kernelVersion,omitempty"`
	Distribution        string   `json:"distribution,omitempty"`
	DistributionVersion string   `json:"distributionVersion,omitempty"`
	WSL                 *WSL     `json:"wsl,omitempty"`
	CPU                 CPU      `json:"cpu"`
	Evidence            []string `json:"evidence,omitempty"`
}

// Signals are the raw host readings classification is derived from. Keeping
// collection and classification separate lets every branch — including the
// Windows and macOS branches that cannot run on a Linux CI host — be covered
// by tests.
type Signals struct {
	GOOS          string
	GOARCH        string
	KernelRelease string
	KernelVersion string
	OSRelease     map[string]string
	CPUVendor     string
	CPUModel      string
	LogicalCores  int
	WSLDistro     string
	WSLInterop    bool
	Sources       []string
}

var appleSiliconPattern = regexp.MustCompile(`(?i)\bApple\s+(M\d+)(?:\s+(Pro|Max|Ultra))?`)

// Detect reads the live host and returns its platform observation.
func Detect() Platform {
	return Classify(Collect())
}

// Collect gathers the raw signals for the running host.
func Collect() Signals {
	signals := Signals{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, LogicalCores: runtime.NumCPU()}
	switch runtime.GOOS {
	case "linux":
		collectLinux(&signals)
	case "darwin":
		collectDarwin(&signals)
	case "windows":
		collectWindows(&signals)
	}
	return signals
}

func collectLinux(signals *Signals) {
	if release, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		signals.KernelRelease = strings.TrimSpace(string(release))
		signals.Sources = append(signals.Sources, "/proc/sys/kernel/osrelease")
	}
	if version, err := os.ReadFile("/proc/version"); err == nil {
		signals.KernelVersion = strings.TrimSpace(string(version))
		signals.Sources = append(signals.Sources, "/proc/version")
	}
	if osRelease, err := os.Open("/etc/os-release"); err == nil {
		defer osRelease.Close()
		signals.OSRelease = parseOSRelease(osRelease)
		signals.Sources = append(signals.Sources, "/etc/os-release")
	}
	if cpuinfo, err := os.Open("/proc/cpuinfo"); err == nil {
		defer cpuinfo.Close()
		signals.CPUVendor, signals.CPUModel = parseCPUInfo(cpuinfo)
		signals.Sources = append(signals.Sources, "/proc/cpuinfo")
	}
	signals.WSLDistro = strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME"))
	// WSL_INTEROP names the interop socket; its presence, and the interop
	// binfmt registration, are the two independent markers that this Linux
	// userland is hosted by Windows rather than merely built by Microsoft.
	if strings.TrimSpace(os.Getenv("WSL_INTEROP")) != "" {
		signals.WSLInterop = true
		signals.Sources = append(signals.Sources, "env:WSL_INTEROP")
	}
	if _, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop"); err == nil {
		signals.WSLInterop = true
		signals.Sources = append(signals.Sources, "/proc/sys/fs/binfmt_misc/WSLInterop")
	}
}

func collectDarwin(signals *Signals) {
	signals.KernelRelease = commandOutput("uname", "-r")
	signals.KernelVersion = commandOutput("uname", "-v")
	signals.CPUModel = commandOutput("sysctl", "-n", "machdep.cpu.brand_string")
	signals.CPUVendor = commandOutput("sysctl", "-n", "machdep.cpu.vendor")
	darwinDistribution(signals)
	signals.Sources = append(signals.Sources, "uname", "sysctl", "sw_vers")
}

// darwinDistribution fills the macOS product name and version.
func darwinDistribution(s *Signals) {
	if s.OSRelease == nil {
		s.OSRelease = map[string]string{}
	}
	if name := commandOutput("sw_vers", "-productName"); name != "" {
		s.OSRelease["NAME"] = name
	}
	if version := commandOutput("sw_vers", "-productVersion"); version != "" {
		s.OSRelease["VERSION_ID"] = version
	}
}

func collectWindows(signals *Signals) {
	signals.CPUModel = strings.TrimSpace(os.Getenv("PROCESSOR_IDENTIFIER"))
	signals.CPUVendor = strings.TrimSpace(os.Getenv("PROCESSOR_ARCHITECTURE"))
	signals.KernelRelease = strings.TrimSpace(os.Getenv("OS"))
	signals.Sources = append(signals.Sources, "env:PROCESSOR_IDENTIFIER", "env:OS")
}

func commandOutput(name string, args ...string) string {
	output, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func parseOSRelease(reader io.Reader) map[string]string {
	values := map[string]string{}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		key, value, found := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if !found || strings.HasPrefix(key, "#") {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values
}

func parseCPUInfo(reader io.Reader) (vendor string, model string) {
	scanner := bufio.NewScanner(reader)
	var implementer, part string
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "model name", "Model", "Hardware", "cpu":
			if model == "" {
				model = value
			}
		case "vendor_id":
			if vendor == "" {
				vendor = value
			}
		case "CPU implementer":
			implementer = value
		case "CPU part":
			part = value
		}
	}
	// ARM cores publish numeric implementer/part identifiers instead of a
	// brand string. Report them verbatim rather than guessing a marketing name.
	if vendor == "" && implementer != "" {
		vendor = armImplementer(implementer)
	}
	if model == "" && part != "" {
		model = "ARM part " + part
	}
	return vendor, model
}

func armImplementer(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "0x41":
		return "ARM"
	case "0x61":
		return "Apple"
	case "0x4e":
		return "NVIDIA"
	case "0x51":
		return "Qualcomm"
	case "0xc0":
		return "Ampere"
	}
	return code
}

// Classify converts raw signals into the neutral platform observation. It is
// pure: every branch is decided from the passed signals only.
func Classify(signals Signals) Platform {
	platform := Platform{
		ContractVersion: ContractVersion,
		Kernel:          strings.TrimSpace(signals.KernelRelease),
		KernelVersion:   strings.TrimSpace(signals.KernelVersion),
		CPU:             classifyCPU(signals),
		Evidence:        append([]string(nil), signals.Sources...),
	}
	platform.Distribution = firstNonEmpty(signals.OSRelease["NAME"], signals.OSRelease["ID"])
	platform.DistributionVersion = firstNonEmpty(signals.OSRelease["VERSION_ID"], signals.OSRelease["VERSION"])

	switch signals.GOOS {
	case "darwin":
		platform.OS = OSMacOS
		platform.Kind = KindMacOS
		if platform.Distribution == "" {
			platform.Distribution = "macOS"
		}
	case "windows":
		platform.OS = OSWindows
		platform.Kind = KindWindowsNative
		if platform.Distribution == "" {
			platform.Distribution = "Windows"
		}
	case "linux":
		platform.OS = OSLinux
		platform.Kind = KindLinux
		if version, isWSL := wslVersion(signals); isWSL {
			platform.Kind = KindWSL2
			if version == 1 {
				platform.Kind = KindWSL1
			}
			platform.WSL = &WSL{Version: version, Distro: signals.WSLDistro, Interop: signals.WSLInterop}
		}
	default:
		platform.OS = strings.TrimSpace(signals.GOOS)
		platform.Kind = platform.OS
	}
	return platform
}

// wslVersion reports whether this Linux userland runs under WSL and, when it
// does, which WSL generation. WSL2 kernels carry an explicit WSL2 marker in
// their release string; a Microsoft-built kernel without that marker is the
// WSL1 translation layer.
func wslVersion(signals Signals) (int, bool) {
	haystack := strings.ToLower(signals.KernelRelease + " " + signals.KernelVersion)
	microsoft := strings.Contains(haystack, "microsoft")
	if !microsoft && !signals.WSLInterop && signals.WSLDistro == "" {
		return 0, false
	}
	if strings.Contains(haystack, "wsl2") {
		return 2, true
	}
	if microsoft && (strings.Contains(haystack, "microsoft-standard") || signals.WSLInterop) {
		return 2, true
	}
	if microsoft {
		return 1, true
	}
	// Interop or the distro variable without any Microsoft kernel marker is
	// ambiguous; report WSL with an unknown generation rather than guessing.
	return 0, true
}

func classifyCPU(signals Signals) CPU {
	cpu := CPU{
		Architecture: strings.TrimSpace(signals.GOARCH),
		Vendor:       strings.TrimSpace(signals.CPUVendor),
		Model:        strings.TrimSpace(signals.CPUModel),
		LogicalCores: signals.LogicalCores,
	}
	switch cpu.Architecture {
	case "amd64":
		cpu.Family = FamilyX86_64
	case "386":
		cpu.Family = FamilyX86
	case "arm64":
		cpu.Family = FamilyARM64
	case "arm":
		cpu.Family = FamilyARM
	default:
		cpu.Family = FamilyUnknown
	}
	if match := appleSiliconPattern.FindStringSubmatch(cpu.Model); match != nil {
		cpu.Family = FamilyAppleSilicon
		cpu.Series = strings.ToUpper(match[1])
		cpu.Variant = titleCase(match[2])
		if cpu.Variant == "" {
			cpu.Variant = "base"
		}
		if cpu.Vendor == "" {
			cpu.Vendor = "Apple"
		}
	}
	return cpu
}

func titleCase(value string) string {
	lowered := strings.ToLower(strings.TrimSpace(value))
	if lowered == "" {
		return ""
	}
	return strings.ToUpper(lowered[:1]) + lowered[1:]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
