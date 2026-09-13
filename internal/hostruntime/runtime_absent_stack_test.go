package hostruntime

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A host with no virtualization stack is the state every new user's host agent
// is in, and list_vms is the first inventory read a bootstrap makes on it. Every
// provider CLI call passes through these entry points, so the absent stack must
// be one typed answer here rather than a shell diagnostic at each call site.
func TestRunProviderReportsAnAbsentVirtualizationStack(t *testing.T) {
	absent := NewRuntime(Config{ProviderID: IDIncus, ProviderBinary: "opute-provider-binary-that-does-not-exist"})

	_, err := absent.RunProvider([]string{"list", "--format", "json"}, nil, time.Second)
	assertAbsentStack(t, "RunProvider", err)

	_, err = absent.RunVMExecWithStdinContext(context.Background(), "vm-a", []string{"true"}, nil, nil, time.Second)
	assertAbsentStack(t, "RunVMExecWithStdinContext", err)

	_, err = absent.NewVMInteractiveCommand("vm-a")
	assertAbsentStack(t, "NewVMInteractiveCommand", err)

	// An unconfigured provider is a distinct condition but the same typed code:
	// the caller's next move is identical.
	unconfigured := NewRuntime(Config{ProviderID: IDIncus})
	_, err = unconfigured.RunProviderContext(context.Background(), []string{"list"}, nil, time.Second)
	if err == nil || !strings.Contains(err.Error(), "virtualization_stack_absent") {
		t.Fatalf("unconfigured provider binary: err = %v, want virtualization_stack_absent", err)
	}

	// The guard must not stand between the agent and a host that does have the
	// stack: a resolvable binary still runs.
	present := NewRuntime(Config{ProviderID: IDIncus, ProviderBinary: "true"})
	if _, err := present.RunProvider(nil, nil, 30*time.Second); err != nil {
		t.Fatalf("present provider binary rejected: %v", err)
	}
}

func assertAbsentStack(t *testing.T, entry string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s accepted an absent provider binary", entry)
	}
	if !strings.Contains(err.Error(), "virtualization_stack_absent") {
		t.Fatalf("%s err = %v, want a typed virtualization_stack_absent answer", entry, err)
	}
	if !strings.Contains(err.Error(), "install_incus_stack") {
		t.Fatalf("%s err = %v, want the typed capability that resolves it", entry, err)
	}
}
