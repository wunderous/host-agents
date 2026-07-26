package ops

import "testing"

func TestRestartClusterRequiresVmName(t *testing.T) {
	svc := &HostOperationsService{}
	_, err := svc.RestartCluster("", nil)
	if err == nil {
		t.Fatal("expected error for empty vmName")
	}
}
