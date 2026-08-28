package ops

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

func TestIncusOwnershipMismatchErrorUsesStableShape(t *testing.T) {
	err := (&IncusOwnershipMismatchError{
		Code:             "incus_ownership_mismatch",
		VMName:           "opute-clean-k3s",
		ExpectedInstance: "local-dev",
		ActualOwner:      "dogfood",
		Operation:        "delete_vm",
		Remediation:      "Select the owning host agent or use the approved adoption workflow.",
	}).Error()
	var parsed map[string]string
	if json.Unmarshal([]byte(err), &parsed) != nil {
		t.Fatalf("mismatch was not JSON: %s", err)
	}
	for key, want := range map[string]string{
		"code": "incus_ownership_mismatch", "vmName": "opute-clean-k3s",
		"expectedInstance": "local-dev", "actualOwner": "dogfood", "operation": "delete_vm",
	} {
		if parsed[key] != want {
			t.Fatalf("%s = %q, want %q", key, parsed[key], want)
		}
	}
}

func TestSharedHostOwnershipRejectsNonOwnerMutation(t *testing.T) {
	service := &HostOperationsService{
		shared: hostruntime.Shared{
			InstanceID:              "local-dev",
			SharedHostOwnerInstance: "dogfood",
		},
	}
	err := service.requireSharedHostOwner("start_local_llm_runtime")
	if err == nil || !strings.Contains(err.Error(), "shared_host_ownership_required") {
		t.Fatalf("expected shared-host mismatch, got %v", err)
	}
}

func TestSharedHostOwnershipBlocksHostServiceMutationBeforeCommand(t *testing.T) {
	service := &HostOperationsService{
		shared: hostruntime.Shared{
			InstanceID:              "local-dev",
			SharedHostOwnerInstance: "dogfood",
		},
	}
	if _, err := service.RestartHostService(RestartHostServiceArgs{ServiceName: "opute-host-agent"}, nil); err == nil || !strings.Contains(err.Error(), "shared_host_ownership_required") {
		t.Fatalf("expected host-service mutation guard, got %v", err)
	}
}

func TestPersistentRelayManagersUseSeparateDirectories(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	args := LocalLLMRelayArgs{
		SessionID:       "same-session",
		ListenHost:      "127.0.0.1",
		ListenPort:      0,
		TargetHost:      "127.0.0.1",
		TargetPort:      11434,
		RelayToken:      strings.Repeat("r", 40),
		AllowedSourceIP: "127.0.0.1",
	}
	first := newPersistentLocalLLMRelayManagerAt(firstDir)
	if err := first.persistLocalLLMRelayArgs(args); err != nil {
		t.Fatal(err)
	}
	second := newPersistentLocalLLMRelayManagerAt(secondDir)
	if len(second.sessions) != 0 {
		t.Fatalf("second instance restored first instance's relays: %d", len(second.sessions))
	}
	if err := second.persistLocalLLMRelayArgs(args); err != nil {
		t.Fatal(err)
	}
	firstAgain := newPersistentLocalLLMRelayManagerAt(firstDir)
	if len(firstAgain.sessions) != 1 {
		t.Fatalf("first instance did not restore its own relay: %d", len(firstAgain.sessions))
	}
	for id := range firstAgain.sessions {
		firstAgain.stop(id)
	}
	if _, err := localLLMRelayConfigPathInDir(firstDir, args.SessionID); err != nil {
		t.Fatal(err)
	}
}
