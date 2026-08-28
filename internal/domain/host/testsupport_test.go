package host

import (
	"time"

	"github.com/wunderous/host-agents/internal/contract/vminfo"
	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

// testService builds the domain over a caller-supplied shared seam, with deps
// that fail loudly. Every test in this package exercises the agent's own
// machine, so a call into any dep means the boundary moved.
func testService(shared hostruntime.Shared) *Service {
	return New(&shared, Deps{
		// Registry-only resolution. The production resolver can additionally
		// ADOPT an unregistered resource by asking incus or systemd whether it
		// exists; nothing in this package's tests should take that path, and
		// passing nil for the adopter means one silently cannot.
		ResolveResource: func(uri, wantType string) (hostruntime.Coordinates, error) {
			return shared.ResolveResource(uri, wantType, nil)
		},
		VMInventoryCapacity: func() (vminfo.VMInventoryCapacity, error) {
			panic("host tests must not reach the incus domain")
		},
		// The incus seam minus its ownership check. RunInstanceCommand's
		// contract is the argv it hands across the boundary, so the test needs
		// that argv to actually be built; ownership is incus's own test.
		RunVMExec: func(vmName string, guestArgv []string, onData func(string), timeout time.Duration) (hostexec.Result, error) {
			return shared.CommandRunner(shared.VMExecArgv(vmName, guestArgv), onData, timeout)
		},
		SupportedTools: func(string) []string { return nil },
	})
}
