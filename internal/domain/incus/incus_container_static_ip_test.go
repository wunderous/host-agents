package incus

import (
	"strings"
	"testing"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

func staticIPService(t *testing.T, responses map[string]hostexec.Result, seen *[][]string) *Service {
	t.Helper()
	return &Service{shared: &hostruntime.Shared{
		CommandRunnerFn: func(args []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
			*seen = append(*seen, append([]string(nil), args...))
			if result, ok := responses[strings.Join(args, " ")]; ok {
				return result, nil
			}
			return hostexec.Result{ExitCode: 0}, nil
		},
	}}
}

// A cluster member keeps its address across restarts only if the instance owns
// it. The NIC is inherited from the default profile, so pinning needs an
// instance-level override.
func TestEnsureContainerStaticIPv4OverridesProfileNIC(t *testing.T) {
	var seen [][]string
	service := staticIPService(t, map[string]hostexec.Result{
		"config device get opute-ha-a eth0 ipv4.address": {ExitCode: 1},
	}, &seen)

	if _, _, err := service.ensureContainerStaticIPv4("opute-ha-a", "10.0.100.66", nil); err != nil {
		t.Fatalf("ensureContainerStaticIPv4: %v", err)
	}
	last := strings.Join(seen[len(seen)-1], " ")
	if last != "config device override opute-ha-a eth0 ipv4.address=10.0.100.66" {
		t.Fatalf("unexpected pinning command: %q", last)
	}
}

// Re-provisioning an already-pinned container must not churn its device.
func TestEnsureContainerStaticIPv4IsIdempotent(t *testing.T) {
	var seen [][]string
	service := staticIPService(t, map[string]hostexec.Result{
		"config device get opute-ha-a eth0 ipv4.address": {ExitCode: 0, Stdout: "10.0.100.66\n"},
	}, &seen)

	if _, _, err := service.ensureContainerStaticIPv4("opute-ha-a", "10.0.100.66", nil); err != nil {
		t.Fatalf("ensureContainerStaticIPv4: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("expected only the read-back, got %d commands", len(seen))
	}
}

// An already-overridden device is moved rather than re-created.
func TestEnsureContainerStaticIPv4SetsExistingOverride(t *testing.T) {
	var seen [][]string
	service := staticIPService(t, map[string]hostexec.Result{
		"config device get opute-ha-a eth0 ipv4.address":                  {ExitCode: 0, Stdout: "10.0.100.9\n"},
		"config device override opute-ha-a eth0 ipv4.address=10.0.100.66": {ExitCode: 1, Stderr: `Device "eth0" already exists`},
	}, &seen)

	if _, _, err := service.ensureContainerStaticIPv4("opute-ha-a", "10.0.100.66", nil); err != nil {
		t.Fatalf("ensureContainerStaticIPv4: %v", err)
	}
	last := strings.Join(seen[len(seen)-1], " ")
	if last != "config device set opute-ha-a eth0 ipv4.address 10.0.100.66" {
		t.Fatalf("unexpected update command: %q", last)
	}
}

func TestEnsureContainerStaticIPv4RejectsNonIPv4(t *testing.T) {
	var seen [][]string
	service := staticIPService(t, nil, &seen)
	if _, _, err := service.ensureContainerStaticIPv4("opute-ha-a", "fd00::1", nil); err == nil {
		t.Fatal("expected an IPv6 address to be refused")
	}
	if _, _, err := service.ensureContainerStaticIPv4("opute-ha-a", "", nil); err != nil {
		t.Fatalf("an unset address is not a pin request: %v", err)
	}
}

// Only the incusd that created a bridge may hand out addresses on it. On every
// other host the refusal is the normal answer, not a failure, and the pin has
// to move into the guest -- so provisioning must survive it.
func TestEnsureContainerStaticIPv4DefersOnUnmanagedParentBridge(t *testing.T) {
	var seen [][]string
	service := staticIPService(t, map[string]hostexec.Result{
		"config device get opute-ha-b eth0 ipv4.address": {ExitCode: 1},
		"config device override opute-ha-b eth0 ipv4.address=10.0.100.88": {
			ExitCode: 1,
			Stderr:   `Invalid devices: Device validation failed for "eth0": Cannot use manually specified ipv4.address when using unmanaged parent bridge`,
		},
	}, &seen)

	deferred, changed, err := service.ensureContainerStaticIPv4("opute-ha-b", "10.0.100.88", nil)
	if err != nil {
		t.Fatalf("an unmanaged parent bridge must not fail provisioning: %v", err)
	}
	if !deferred || changed {
		t.Fatalf("expected the pin to be deferred to the guest, got deferred=%v changed=%v", deferred, changed)
	}
}

// The guest pin copies the prefix and gateway from the lease it already holds,
// because this host cannot ask the network it does not own.
func TestApplyGuestStaticIPv4WritesNetplanFromLease(t *testing.T) {
	var seen [][]string
	responses := map[string]hostexec.Result{
		"exec opute-ha-b -- ip -4 -o addr show dev eth0": {
			ExitCode: 0,
			Stdout:   "2: eth0    inet 10.0.100.231/24 brd 10.0.100.255 scope global dynamic eth0",
		},
		"exec opute-ha-b -- ip -4 route show default": {
			ExitCode: 0,
			Stdout:   "default via 10.0.100.1 dev eth0 proto dhcp src 10.0.100.231 metric 100",
		},
	}
	service := &Service{shared: &hostruntime.Shared{
		CommandRunnerFn: func(args []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
			seen = append(seen, append([]string(nil), args...))
			key := strings.Join(args, " ")
			if result, ok := responses[key]; ok {
				return result, nil
			}
			if strings.HasPrefix(key, "exec opute-ha-b -- sh -c") {
				// The apply moved the address, so the read-backs now agree.
				responses["exec opute-ha-b -- ip -4 -o addr show dev eth0"] = hostexec.Result{
					ExitCode: 0,
					Stdout:   "2: eth0    inet 10.0.100.88/24 brd 10.0.100.255 scope global eth0",
				}
			}
			return hostexec.Result{ExitCode: 0}, nil
		},
	}}

	if err := service.applyGuestStaticIPv4("opute-ha-b", "10.0.100.88", nil); err != nil {
		t.Fatalf("applyGuestStaticIPv4: %v", err)
	}
	var script string
	for _, args := range seen {
		if len(args) == 6 && args[0] == "exec" && args[3] == "sh" && args[4] == "-c" {
			script = args[5]
		}
	}
	if script == "" {
		t.Fatal("expected the guest to be configured through a script")
	}
	for _, want := range []string{"addresses: [10.0.100.88/24]", "via: 10.0.100.1", "dhcp4: false", guestStaticIPv4NetplanPath, "netplan apply"} {
		if !strings.Contains(script, want) {
			t.Fatalf("guest script is missing %q: %s", want, script)
		}
	}
}

// A guest already pinned by this file is left alone, but one that merely holds
// the right lease is still pinned -- the lease is what we do not trust.
func TestApplyGuestStaticIPv4IsIdempotentOnlyWhenPinned(t *testing.T) {
	lease := hostexec.Result{ExitCode: 0, Stdout: "2: eth0    inet 10.0.100.88/24 brd 10.0.100.255 scope global eth0"}
	route := hostexec.Result{ExitCode: 0, Stdout: "default via 10.0.100.1 dev eth0"}

	var pinnedSeen [][]string
	pinned := staticIPService(t, map[string]hostexec.Result{
		"exec opute-ha-b -- ip -4 -o addr show dev eth0":       lease,
		"exec opute-ha-b -- ip -4 route show default":          route,
		"exec opute-ha-b -- cat " + guestStaticIPv4NetplanPath: {ExitCode: 0, Stdout: "      addresses: [10.0.100.88/24]"},
	}, &pinnedSeen)
	if err := pinned.applyGuestStaticIPv4("opute-ha-b", "10.0.100.88", nil); err != nil {
		t.Fatalf("applyGuestStaticIPv4: %v", err)
	}
	for _, args := range pinnedSeen {
		if len(args) > 4 && args[4] == "-c" {
			t.Fatalf("an already-pinned guest was reconfigured: %v", args)
		}
	}

	var leasedSeen [][]string
	leased := staticIPService(t, map[string]hostexec.Result{
		"exec opute-ha-b -- ip -4 -o addr show dev eth0":       lease,
		"exec opute-ha-b -- ip -4 route show default":          route,
		"exec opute-ha-b -- cat " + guestStaticIPv4NetplanPath: {ExitCode: 1},
	}, &leasedSeen)
	if err := leased.applyGuestStaticIPv4("opute-ha-b", "10.0.100.88", nil); err != nil {
		t.Fatalf("applyGuestStaticIPv4: %v", err)
	}
	wrote := false
	for _, args := range leasedSeen {
		if len(args) > 4 && args[4] == "-c" {
			wrote = true
		}
	}
	if !wrote {
		t.Fatal("a guest holding only a lease must still be pinned")
	}
}
