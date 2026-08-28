package hostplatform

import (
	"strings"
	"testing"
)

func TestClassifyDistinguishesHostKinds(t *testing.T) {
	cases := []struct {
		name     string
		signals  Signals
		wantOS   string
		wantKind string
	}{
		{
			name:     "native linux",
			signals:  Signals{GOOS: "linux", GOARCH: "amd64", KernelRelease: "6.11.0-19-generic", OSRelease: map[string]string{"NAME": "Ubuntu", "VERSION_ID": "24.04"}},
			wantOS:   OSLinux,
			wantKind: KindLinux,
		},
		{
			name:     "wsl2",
			signals:  Signals{GOOS: "linux", GOARCH: "amd64", KernelRelease: "6.18.33.2-microsoft-standard-WSL2", WSLDistro: "Ubuntu", WSLInterop: true},
			wantOS:   OSLinux,
			wantKind: KindWSL2,
		},
		{
			name:     "wsl1",
			signals:  Signals{GOOS: "linux", GOARCH: "amd64", KernelRelease: "4.4.0-19041-Microsoft", KernelVersion: "Linux version 4.4.0-19041-Microsoft (Microsoft@Microsoft.com)"},
			wantOS:   OSLinux,
			wantKind: KindWSL1,
		},
		{
			name:     "macos",
			signals:  Signals{GOOS: "darwin", GOARCH: "arm64", OSRelease: map[string]string{"NAME": "macOS", "VERSION_ID": "15.3"}},
			wantOS:   OSMacOS,
			wantKind: KindMacOS,
		},
		{
			name:     "windows native",
			signals:  Signals{GOOS: "windows", GOARCH: "amd64"},
			wantOS:   OSWindows,
			wantKind: KindWindowsNative,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			platform := Classify(testCase.signals)
			if platform.OS != testCase.wantOS || platform.Kind != testCase.wantKind {
				t.Fatalf("classify = %s/%s, want %s/%s", platform.OS, platform.Kind, testCase.wantOS, testCase.wantKind)
			}
			if platform.ContractVersion != ContractVersion {
				t.Fatalf("contract version = %q", platform.ContractVersion)
			}
			if testCase.wantKind == KindWSL2 || testCase.wantKind == KindWSL1 {
				if platform.WSL == nil {
					t.Fatal("expected WSL evidence")
				}
			} else if platform.WSL != nil {
				t.Fatalf("unexpected WSL evidence: %+v", platform.WSL)
			}
		})
	}
}

func TestClassifyWSLVersionFromKernelMarkers(t *testing.T) {
	if version, isWSL := wslVersion(Signals{KernelRelease: "5.15.167.4-microsoft-standard-WSL2"}); !isWSL || version != 2 {
		t.Fatalf("wsl2 kernel = %d/%v", version, isWSL)
	}
	if version, isWSL := wslVersion(Signals{KernelRelease: "6.11.0-19-generic"}); isWSL || version != 0 {
		t.Fatalf("generic kernel = %d/%v", version, isWSL)
	}
}

func TestClassifyCPUFamilies(t *testing.T) {
	cases := []struct {
		name        string
		signals     Signals
		wantFamily  string
		wantSeries  string
		wantVariant string
	}{
		{name: "apple m3 pro", signals: Signals{GOOS: "darwin", GOARCH: "arm64", CPUModel: "Apple M3 Pro"}, wantFamily: FamilyAppleSilicon, wantSeries: "M3", wantVariant: "Pro"},
		{name: "apple m1 base", signals: Signals{GOOS: "darwin", GOARCH: "arm64", CPUModel: "Apple M1"}, wantFamily: FamilyAppleSilicon, wantSeries: "M1", wantVariant: "base"},
		{name: "apple m2 ultra", signals: Signals{GOOS: "darwin", GOARCH: "arm64", CPUModel: "Apple M2 Ultra"}, wantFamily: FamilyAppleSilicon, wantSeries: "M2", wantVariant: "Ultra"},
		{name: "generic arm64", signals: Signals{GOOS: "linux", GOARCH: "arm64", CPUVendor: "Ampere"}, wantFamily: FamilyARM64},
		{name: "x86-64", signals: Signals{GOOS: "linux", GOARCH: "amd64", CPUVendor: "GenuineIntel", CPUModel: "13th Gen Intel(R) Core(TM) i9-13900K"}, wantFamily: FamilyX86_64},
		{name: "32-bit arm", signals: Signals{GOOS: "linux", GOARCH: "arm"}, wantFamily: FamilyARM},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cpu := Classify(testCase.signals).CPU
			if cpu.Family != testCase.wantFamily {
				t.Fatalf("family = %q, want %q", cpu.Family, testCase.wantFamily)
			}
			if cpu.Series != testCase.wantSeries || cpu.Variant != testCase.wantVariant {
				t.Fatalf("series/variant = %q/%q, want %q/%q", cpu.Series, cpu.Variant, testCase.wantSeries, testCase.wantVariant)
			}
		})
	}
}

func TestParseCPUInfoReadsBrandAndARMImplementer(t *testing.T) {
	vendor, model := parseCPUInfo(strings.NewReader("processor\t: 0\nvendor_id\t: AuthenticAMD\nmodel name\t: AMD Ryzen 9 7950X\n"))
	if vendor != "AuthenticAMD" || model != "AMD Ryzen 9 7950X" {
		t.Fatalf("x86 cpuinfo = %q/%q", vendor, model)
	}
	vendor, model = parseCPUInfo(strings.NewReader("processor\t: 0\nCPU implementer\t: 0x61\nCPU part\t: 0x023\n"))
	if vendor != "Apple" || model != "ARM part 0x023" {
		t.Fatalf("arm cpuinfo = %q/%q", vendor, model)
	}
}

func TestParseOSReleaseStripsQuotes(t *testing.T) {
	values := parseOSRelease(strings.NewReader("# comment\nNAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nID=ubuntu\n"))
	if values["NAME"] != "Ubuntu" || values["VERSION_ID"] != "24.04" || values["ID"] != "ubuntu" {
		t.Fatalf("os-release = %+v", values)
	}
}

func TestDetectReturnsUsableObservationOnThisHost(t *testing.T) {
	platform := Detect()
	if platform.OS == "" || platform.Kind == "" || platform.CPU.Architecture == "" {
		t.Fatalf("incomplete observation: %+v", platform)
	}
	if platform.CPU.LogicalCores <= 0 {
		t.Fatalf("logical cores = %d", platform.CPU.LogicalCores)
	}
}
