package contract

import (
	"testing"

	"github.com/wunderous/host-agents/internal/resource"
	"github.com/wunderous/host-agents/internal/tasks"
	"github.com/wunderous/host-agents/internal/tools"
)

// W8 collapsed four tool-name-keyed behavioural tables into fields on one
// dispatch registration. The tables still exist, shrunk to the names that have
// no registration: transport-owned and provider-owned tools. These tests keep
// them shrunk. A key that reappears for a registered capability is a second
// source of truth for a value the registration already carries, and the two
// can then disagree silently -- which is the failure mode W8 removed.

func registeredNames(t *testing.T) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, name := range tools.RegisteredToolNames() {
		names[name] = true
	}
	if len(names) == 0 {
		t.Fatal("no dispatch registrations; the tests below would pass vacuously")
	}
	return names
}

func TestResidualEffectTableHoldsOnlyUnregisteredNames(t *testing.T) {
	registered := registeredNames(t)
	for _, name := range tools.ResidualEffectTableNames() {
		if registered[name] {
			t.Errorf("%q has a dispatch registration; declare its effect there, not in capabilityEffects", name)
		}
	}
}

func TestResidualTaskTableHoldsOnlyUnregisteredNames(t *testing.T) {
	registered := registeredNames(t)
	for name := range tasks.TaskAwareTools {
		if registered[name] {
			t.Errorf("%q has a dispatch registration; declare tools.TaskAware there, not in tasks.TaskAwareTools", name)
		}
	}
}

func TestRegisteredCapabilitiesDoNotFallBackToNameInference(t *testing.T) {
	for _, name := range tools.RegisteredToolNames() {
		if _, ok := tools.RegisteredEffect(name); !ok {
			t.Errorf("%q is registered but declares no effect", name)
		}
		class, ok := tools.RegisteredAdmissionClass(name)
		if !ok {
			t.Errorf("%q is registered but declares no admission class", name)
			continue
		}
		// The name-based inference in resource.ClassifyTool must no longer be
		// what decides a registered capability's class. It stays only for the
		// unregistered residue.
		if class != resource.ClassControl && class != resource.ClassNormal && class != resource.ClassHeavy {
			t.Errorf("%q declares an unknown admission class %q", name, class)
		}
	}
}

func TestHostServingReconciliationUsesTaskContract(t *testing.T) {
	if !tools.IsTaskAware("run_host_command") {
		t.Fatal("run_host_command must use the MCP task contract")
	}
}
