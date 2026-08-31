package main

import (
	"testing"

	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/pkg/hostplatform"
)

func TestInstallManifestIsValid(t *testing.T) {
	manifest := installManifest()
	expected := providercontract.ProviderRef{ID: providerID, Version: providerVersion}
	if err := providercontract.ValidateInstallManifest(manifest, expected); err != nil {
		t.Fatalf("install manifest invalid: %v", err)
	}
}

func TestValidateHostFailsClosedOnMismatch(t *testing.T) {
	platform := hostplatform.Classify(hostplatform.Signals{GOOS: "linux", GOARCH: "amd64", KernelRelease: "6.18.33.2-microsoft-standard-WSL2"})
	result := validateHost(validateArgs{ExpectedKind: hostplatform.KindWSL2}, platform)
	if ready, _ := result["ready"].(bool); !ready {
		t.Fatalf("expected match to be ready: %+v", result)
	}
	result = validateHost(validateArgs{ExpectedKind: hostplatform.KindLinux}, platform)
	if ready, _ := result["ready"].(bool); ready {
		t.Fatalf("expected wsl2 host to fail a native-linux expectation: %+v", result)
	}
	result = validateHost(validateArgs{ExpectedCPUFamily: hostplatform.FamilyAppleSilicon}, platform)
	if ready, _ := result["ready"].(bool); ready {
		t.Fatalf("expected x86-64 host to fail an apple-silicon expectation: %+v", result)
	}
}

func TestNewServerBuilds(t *testing.T) {
	if newServer() == nil {
		t.Fatal("server is nil")
	}
}
