package hostruntime

import "testing"

func TestWorkloadUnitNameIsStableForScopedCommand(t *testing.T) {
	first := workloadUnitName("operation-1", []string{"bash", "-lc", "echo ok"})
	second := workloadUnitName("operation-1", []string{"bash", "-lc", "echo ok"})
	if first == "" || first != second {
		t.Fatalf("workload unit name = %q, %q; want stable non-empty name", first, second)
	}
}

func TestWorkloadUnitNameDiffersAcrossCommandIdentity(t *testing.T) {
	first := workloadUnitName("operation-1", []string{"bash", "-lc", "echo one"})
	second := workloadUnitName("operation-1", []string{"bash", "-lc", "echo two"})
	if first == second {
		t.Fatalf("different workload commands shared unit name %q", first)
	}
}
