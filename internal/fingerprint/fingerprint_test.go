package fingerprint

import (
	"runtime"
	"testing"
)

func TestReadIdentityUsesWindowsInstallationForWSL(t *testing.T) {
	if runtime.GOOS != "linux" || !isWSL() {
		t.Skip("requires a WSL runtime")
	}
	identity, err := ReadIdentity()
	if err != nil {
		t.Fatalf("ReadIdentity() error = %v", err)
	}
	if identity.FingerprintSource != SourceWindowsMachineGUIDViaWSL {
		t.Fatalf("fingerprint source = %q, want %q", identity.FingerprintSource, SourceWindowsMachineGUIDViaWSL)
	}
	if identity.ExecutionContext.Kind != ExecutionContextWSL || identity.ExecutionContext.ID == "" {
		t.Fatalf("execution context = %+v, want stable WSL context", identity.ExecutionContext)
	}
	if identity.ExecutionContext.DisplayName == "" {
		t.Log("WSL_DISTRO_NAME is unavailable; retaining unnamed context metadata")
	}
}

func TestCapabilitiesAreObservationsNotAuthorization(t *testing.T) {
	capabilities := DetectCapabilities()
	if capabilities.CanTerminateWSLDistribution && !capabilities.CanManageWSL {
		t.Fatal("terminate capability cannot be true without manage capability")
	}
	if capabilities.CanShutdownWSL && !capabilities.CanManageWSL {
		t.Fatal("shutdown capability cannot be true without manage capability")
	}
}
