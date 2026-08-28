package postgres

import (
	"context"
	"time"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

// kubectlRunner is the shape the PostgreSQL tests fake: one function standing
// in for every kubectl variant, since the tests only care about the arguments.
type kubectlRunner func(ctx context.Context, vmName string, kubectlArgs []string, input []byte, label string, timeout time.Duration) (string, error)

// validResetService builds a Service with an owned host, matching the identity
// the reset and relay paths require.
func validResetService() *Service {
	return &Service{
		shared: &hostruntime.Shared{
			InstanceID:              "agent-a",
			OwnershipMode:           "enforce",
			SharedHostOwnerInstance: "agent-a",
		},
		relay: newPostgreSQLServiceRelayManager(),
	}
}

// setKubectlRunner points every kubectl seam at one fake.
func (s *Service) setKubectlRunner(runner kubectlRunner) {
	s.deps.RunKubectlContext = func(ctx context.Context, vmName string, args []string, label string, timeout time.Duration) (string, error) {
		return runner(ctx, vmName, args, nil, label, timeout)
	}
	s.deps.RunKubectlWithStdinContext = func(ctx context.Context, vmName string, args []string, input []byte, label string, timeout time.Duration) (string, error) {
		return runner(ctx, vmName, args, input, label, timeout)
	}
	s.deps.RunKubectlTimed = func(vmName string, args []string, label string, timeout time.Duration) (string, error) {
		return runner(context.Background(), vmName, args, nil, label, timeout)
	}
}
