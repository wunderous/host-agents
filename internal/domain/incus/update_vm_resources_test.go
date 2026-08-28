package incus

import (
	"strings"
	"testing"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

func TestUpdateVMResourcesRequiresVMName(t *testing.T) {
	svc := &Service{shared: &hostruntime.Shared{}}
	_, err := svc.UpdateVMResources(UpdateVMResourcesArgs{CPUs: 3, Memory: "3GiB"}, nil)
	if err == nil || !strings.Contains(err.Error(), "vmName is required") {
		t.Fatalf("expected vmName gate, got %v", err)
	}
}

func TestUpdateVMResourcesRequiresAtLeastOneLimit(t *testing.T) {
	svc := &Service{shared: &hostruntime.Shared{}}
	_, err := svc.UpdateVMResources(UpdateVMResourcesArgs{VMName: "vm-a"}, nil)
	if err == nil || !strings.Contains(err.Error(), "at least one of cpus or memory") {
		t.Fatalf("expected limit gate, got %v", err)
	}
}
