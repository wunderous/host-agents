package ops

import (
	"strings"
	"testing"
)

func TestExecCommandRequiresName(t *testing.T) {
	svc := &HostOperationsService{}
	_, err := svc.ExecCommand(ExecCommandArgs{Command: "true"}, nil)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name error, got %v", err)
	}
}

func TestExecCommandRequiresCommand(t *testing.T) {
	svc := &HostOperationsService{}
	_, err := svc.ExecCommand(ExecCommandArgs{VMName: "vm1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("expected command error, got %v", err)
	}
}
