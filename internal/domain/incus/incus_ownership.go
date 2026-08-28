package incus

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/textutil"
)

const (
	oputeIncusOwnerLabel = "user.opute.host_agent_instance"
	oputeIncusAgentLabel = "user.opute.host_agent_id"
)

var hostAgentInstanceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func validateHostAgentInstanceID(value string) error {
	if !hostAgentInstanceIDPattern.MatchString(strings.TrimSpace(value)) {
		return fmt.Errorf("OPUTE_HOST_AGENT_INSTANCE %q is invalid: use [a-z0-9][a-z0-9-]{0,62}", value)
	}
	return nil
}

// IncusOwnershipMismatchError is deliberately structured so the MCP adapter
// can preserve the same diagnostic contract as the TypeScript Incus leaf.
// The provider mutation must not be attempted after this error is returned.
type IncusOwnershipMismatchError struct {
	Code             string `json:"code"`
	VMName           string `json:"vmName"`
	ExpectedInstance string `json:"expectedInstance"`
	ActualOwner      string `json:"actualOwner"`
	Operation        string `json:"operation"`
	Remediation      string `json:"remediation"`
}

// SharedHostOwnershipError is owned by hostruntime: shared-host ownership is
// an identity concern, and both incus and host operations report it.
type SharedHostOwnershipError = hostruntime.SharedHostOwnershipError

func (e *IncusOwnershipMismatchError) Error() string {
	if e == nil {
		return "incus ownership mismatch"
	}
	encoded, err := json.Marshal(e)
	if err == nil {
		return string(encoded)
	}
	return fmt.Sprintf("incus_ownership_mismatch: %s is owned by %s, expected %s", e.VMName, e.ActualOwner, e.ExpectedInstance)
}

func (s *Service) ownershipEnabled() bool {
	return strings.TrimSpace(s.shared.InstanceID) != ""
}

func (s *Service) readIncusOwner(vmName string) (string, error) {
	if !s.ownershipEnabled() {
		return "", nil
	}
	res, err := s.commandRunner([]string{"config", "get", vmName, oputeIncusOwnerLabel}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("read Incus ownership for %q: %s", vmName, textutil.FirstNonEmpty(res.Stderr, res.Stdout, "incus config get failed"))
	}
	return strings.TrimSpace(res.Stdout), nil
}

func (s *Service) assertIncusOwnership(vmName, operation string) error {
	vmName = strings.TrimSpace(vmName)
	if vmName == "" || !s.ownershipEnabled() || s.shared.OwnershipMode != "enforce" {
		return nil
	}
	owner, err := s.readIncusOwner(vmName)
	if err != nil {
		return err
	}
	if owner == s.shared.InstanceID {
		return nil
	}
	actual := owner
	if actual == "" {
		actual = "unowned-or-foreign"
	}
	return &IncusOwnershipMismatchError{
		Code:             "incus_ownership_mismatch",
		VMName:           vmName,
		ExpectedInstance: s.shared.InstanceID,
		ActualOwner:      actual,
		Operation:        strings.TrimSpace(operation),
		Remediation:      "Select the owning host agent or use the approved adoption workflow.",
	}
}

func (s *Service) ownedIncusItem(item incusListItem) bool {
	if !s.ownershipEnabled() || s.shared.OwnershipMode != "enforce" {
		return true
	}
	return pickIncusConfigValue(item, oputeIncusOwnerLabel) == s.shared.InstanceID
}

func (s *Service) ownerConfigValue() string {
	return strings.TrimSpace(s.shared.InstanceID)
}

func (s *Service) ownerAgentConfigValue() string {
	return strings.TrimSpace(s.shared.AgentID)
}

func (s *Service) requireSharedHostOwner(operation string) error {
	return s.shared.RequireSharedHostOwner(operation)
}
