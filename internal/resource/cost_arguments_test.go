package resource

import (
	"errors"
	"testing"
)

func TestResolveArgumentCostUsesDeclaredMemoryAndCPU(t *testing.T) {
	base := AdmissionRequest{Class: ClassHeavy, CPUCores: 2, MemoryBytes: 2 << 30, Tasks: 8}
	resolved, err := ResolveArgumentCost(base, map[string]any{
		"cpus":   float64(1),
		"memory": "1760MiB",
	}, CostArgumentBindings{CPUCores: "cpus", MemoryBytes: "memory"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.CPUCores != 1 || resolved.MemoryBytes != 1760*(1<<20) {
		t.Fatalf("resolved cost = %#v", resolved)
	}
	if resolved.Tasks != base.Tasks {
		t.Fatalf("unbound cost changed: %#v", resolved)
	}
}

func TestResolveArgumentCostAcceptsCapacityAwareLifecycleMemoryLimits(t *testing.T) {
	for _, testCase := range []struct {
		name string
		text string
		want int64
	}{
		{name: "1.5GiB", text: "1.5GiB", want: 1536 * (1 << 20)},
		{name: "1.75GiB", text: "1.75GiB", want: 1792 * (1 << 20)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resolved, err := ResolveArgumentCost(AdmissionRequest{Class: ClassHeavy, MemoryBytes: 2 << 30}, map[string]any{
				"memory": testCase.text,
			}, CostArgumentBindings{MemoryBytes: "memory"})
			if err != nil {
				t.Fatal(err)
			}
			if resolved.MemoryBytes != testCase.want {
				t.Fatalf("memory cost = %d, want %d", resolved.MemoryBytes, testCase.want)
			}
		})
	}
}

func TestResolveArgumentCostRetainsStaticDefaultsWhenArgumentsAreOmitted(t *testing.T) {
	base := AdmissionRequest{Class: ClassHeavy, CPUCores: 2, MemoryBytes: 2 << 30, DiskBytes: 0, Tasks: 8}
	resolved, err := ResolveArgumentCost(base, map[string]any{}, CostArgumentBindings{
		CPUCores: "cpus", MemoryBytes: "memory", DiskBytes: "disk", Tasks: "tasks",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved != base {
		t.Fatalf("omitted arguments changed static defaults: got %#v want %#v", resolved, base)
	}
}

func TestResolveArgumentCostSupportsNestedDeclaredPathsAndDiskBytes(t *testing.T) {
	resolved, err := ResolveArgumentCost(AdmissionRequest{Class: ClassHeavy}, map[string]any{
		"resources": map[string]any{"disk": "4GiB"},
	}, CostArgumentBindings{DiskBytes: "resources.disk"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.DiskBytes != 4*(1<<30) {
		t.Fatalf("disk cost = %d", resolved.DiskBytes)
	}
}

func TestResolveArgumentCostRejectsInvalidValuesAndPaths(t *testing.T) {
	cases := []struct {
		name     string
		args     map[string]any
		bindings CostArgumentBindings
		code     string
	}{
		{name: "zero memory", args: map[string]any{"memory": "0MiB"}, bindings: CostArgumentBindings{MemoryBytes: "memory"}, code: "host_resource_argument_invalid"},
		{name: "fractional disk bytes", args: map[string]any{"disk": float64(1.5)}, bindings: CostArgumentBindings{DiskBytes: "disk"}, code: "host_resource_argument_invalid"},
		{name: "fractional tasks", args: map[string]any{"tasks": float64(1.5)}, bindings: CostArgumentBindings{Tasks: "tasks"}, code: "host_resource_argument_invalid"},
		{name: "invalid path", args: map[string]any{}, bindings: CostArgumentBindings{MemoryBytes: "memory[]"}, code: "host_resource_binding_invalid"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ResolveArgumentCost(AdmissionRequest{Class: ClassHeavy}, testCase.args, testCase.bindings)
			if err == nil {
				t.Fatal("invalid argument was accepted")
			}
			var requestErr *RequestError
			if !errors.As(err, &requestErr) || requestErr.Code != testCase.code {
				t.Fatalf("error = %T %v, want %s", err, err, testCase.code)
			}
		})
	}
}
