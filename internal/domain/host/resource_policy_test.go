package host

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resourceid"
)

func TestResourcePolicyTargetRequiresExactHostAgentUnit(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantScope string
		wantUnit  string
		wantError bool
	}{
		{name: "templated user unit", raw: "user/opute-host-agent@agent-a.service", wantScope: "user", wantUnit: "opute-host-agent@agent-a.service"},
		{name: "explicit legacy system unit", raw: "system/opute-host-agent", wantScope: "system", wantUnit: "opute-host-agent.service"},
		{name: "arbitrary unit rejected", raw: "user/other.service", wantError: true},
		{name: "invalid instance rejected", raw: "user/opute-host-agent@AGENT.service", wantError: true},
		{name: "missing scope rejected", raw: "opute-host-agent", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, err := resourceid.New(resourceid.TypeHostService, "local", test.raw)
			if err != nil {
				t.Fatal(err)
			}
			scope, unit, targetErr := resourcePolicyTarget(target)
			if test.wantError {
				if targetErr == nil {
					t.Fatalf("target %q unexpectedly accepted as %s/%s", test.raw, scope, unit)
				}
				return
			}
			if targetErr != nil || scope != test.wantScope || unit != test.wantUnit {
				t.Fatalf("target = %q/%q/%v, want %q/%q", scope, unit, targetErr, test.wantScope, test.wantUnit)
			}
		})
	}
}

func TestRenderHostResourceSliceUnitsPreservesPolicyValues(t *testing.T) {
	units := renderHostResourceSliceUnits()
	protected := units[hostAgentProtectedSlice]
	if !strings.Contains(protected, "CPUWeight=1000") || !strings.Contains(protected, "TasksMax=1024") {
		t.Fatalf("protected slice missing control limits: %q", protected)
	}
	if strings.Contains(protected, "MemoryMax=") || strings.Contains(protected, "MemorySwapMax=") {
		t.Fatalf("protected slice has a hard memory boundary: %q", protected)
	}
	workload := units[hostWorkloadSlice]
	for _, marker := range []string{"MemoryHigh=5G", "MemoryMax=6G", "MemorySwapMax=1G", "CPUQuota=600%", "CPUWeight=100", "TasksMax=4096"} {
		if !strings.Contains(workload, marker) {
			t.Fatalf("workload slice missing %q: %q", marker, workload)
		}
	}
}

func TestReconcileHostResourcePolicyUsesExactSystemdUnit(t *testing.T) {
	var calls [][]string
	service := &Service{shared: &hostruntime.Shared{
		HostCommandRunnerFn: func(command []string, _ func(string), _ time.Duration) (exec.Result, error) {
			calls = append(calls, append([]string(nil), command...))
			if len(command) > 0 && command[len(command)-1] == "--property=Slice,CPUWeight,TasksMax,KillMode" {
				return exec.Result{ExitCode: 0, Stdout: "Slice=opute-host-agent-protected.slice\nCPUWeight=1000\nTasksMax=1024\nKillMode=control-group\n"}, nil
			}
			return exec.Result{ExitCode: 0}, nil
		},
	}}
	target, err := resourceid.New(resourceid.TypeHostService, "local", "system/opute-host-agent@agent-a.service")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileHostResourcePolicy(context.Background(), target); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("systemd calls = %d, want reload/set/show: %#v", len(calls), calls)
	}
	joined := strings.Join(calls[1], " ")
	if !strings.Contains(joined, "set-property") || !strings.Contains(joined, "opute-host-agent@agent-a.service") {
		t.Fatalf("set-property did not target the exact service: %#v", calls[1])
	}
	for _, call := range calls {
		joined = strings.Join(call, " ")
		if strings.Contains(joined, " restart ") || strings.Contains(joined, " start ") || strings.Contains(joined, " stop ") {
			t.Fatalf("reconciliation unexpectedly changed service lifecycle: %#v", call)
		}
	}
}

func TestWorkloadSystemdPropertiesRequireBoundedPolicy(t *testing.T) {
	valid := map[string]string{
		"ControlGroup":       "/user.slice/user-1000.slice/user@1000.service/opute-workload.slice",
		"MemoryHigh":         "5368709120",
		"MemoryMax":          "6442450944",
		"MemorySwapMax":      "1073741824",
		"CPUQuotaPerSecUSec": "6s",
		"CPUWeight":          "100",
		"TasksMax":           "4096",
	}
	if !workloadSystemdPropertiesEnforced(valid) {
		t.Fatal("expected policy-sized workload properties to be accepted")
	}

	for property, value := range map[string]string{
		"MemoryMax":          "infinity",
		"MemorySwapMax":      "2147483648",
		"CPUQuotaPerSecUSec": "7s",
		"TasksMax":           "4097",
	} {
		candidate := make(map[string]string, len(valid))
		for key, item := range valid {
			candidate[key] = item
		}
		candidate[property] = value
		if workloadSystemdPropertiesEnforced(candidate) {
			t.Fatalf("unbounded or over-budget %s=%s was accepted", property, value)
		}
	}
}

func TestParseSystemdLimit(t *testing.T) {
	for value, want := range map[string]int64{"5G": 5 << 30, "4096": 4096, "6000000": 6000000} {
		got, ok := parseSystemdLimit(value)
		if !ok || got != want {
			t.Fatalf("parseSystemdLimit(%q) = %d, %v; want %d, true", value, got, ok, want)
		}
	}
	if _, ok := parseSystemdLimit("max"); ok {
		t.Fatal("max must not count as an enforced finite limit")
	}
}

func TestPreservesOnlyStricterResourceUnits(t *testing.T) {
	protected := "[Slice]\nCPUWeight=1200\nTasksMax=512\n"
	if !preservesStricterResourceUnit(hostAgentProtectedSlice, protected) {
		t.Fatal("stricter protected-slice settings should be preserved")
	}
	if preservesStricterResourceUnit(hostAgentProtectedSlice, protected+"MemoryMax=1G\n") {
		t.Fatal("protected slice must not preserve a hard memory boundary")
	}

	workload := "[Slice]\nMemoryHigh=4G\nMemoryMax=5G\nMemorySwapMax=512M\nCPUQuota=400%\nCPUWeight=50\nTasksMax=2048\n"
	if !preservesStricterResourceUnit(hostWorkloadSlice, workload) {
		t.Fatal("stricter workload settings should be preserved")
	}
	if preservesStricterResourceUnit(hostWorkloadSlice, strings.Replace(workload, "MemoryMax=5G", "MemoryMax=infinity", 1)) {
		t.Fatal("unbounded workload memory must be repaired")
	}
}
