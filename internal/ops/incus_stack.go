package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ResetIncusStackArgs struct {
	InstanceNames               []string `json:"instanceNames,omitempty"`
	InstancePrefix              string   `json:"instancePrefix,omitempty"`
	Confirm                     bool     `json:"confirm,omitempty"`
	Reinstall                   bool     `json:"reinstall,omitempty"`
	DryRun                      bool     `json:"dryRun,omitempty"`
	DisposableHostFingerprint   string   `json:"disposableHostFingerprint,omitempty"`
	ExpectedHostFingerprint     string   `json:"expectedHostFingerprint,omitempty"`
	DisposableHostAuthorization string   `json:"disposableHostAuthorization,omitempty"`
}

type ResetIncusInventoryItem struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Type   string `json:"type"`
	Owner  string `json:"owner"`
}

type resetIncusCheckpoint struct {
	Version    string                    `json:"version"`
	InstanceID string                    `json:"instanceId"`
	Targets    []ResetIncusInventoryItem `json:"targets"`
	Deleted    []string                  `json:"deleted"`
	Phase      string                    `json:"phase"`
	Reconcile  map[string]any            `json:"reconcile,omitempty"`
	UpdatedAt  string                    `json:"updatedAt"`
}

func (s *HostOperationsService) readResetCheckpoint() (*resetIncusCheckpoint, error) {
	if strings.TrimSpace(s.resetCheckpointPath) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.resetCheckpointPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var checkpoint resetIncusCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return nil, errors.New("incus reset checkpoint is invalid")
	}
	if checkpoint.Version != "incus-reset.v1" || checkpoint.InstanceID != s.shared.InstanceID {
		return nil, errors.New("incus reset checkpoint owner or version mismatch")
	}
	return &checkpoint, nil
}

func (s *HostOperationsService) writeResetCheckpoint(checkpoint resetIncusCheckpoint) error {
	if strings.TrimSpace(s.resetCheckpointPath) == "" {
		return nil
	}
	checkpoint.Version = "incus-reset.v1"
	checkpoint.InstanceID = s.shared.InstanceID
	checkpoint.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.resetCheckpointPath), 0o700); err != nil {
		return err
	}
	tempPath := s.resetCheckpointPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tempPath, s.resetCheckpointPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func (s *HostOperationsService) clearResetCheckpoint() error {
	if strings.TrimSpace(s.resetCheckpointPath) == "" {
		return nil
	}
	if err := os.Remove(s.resetCheckpointPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *HostOperationsService) listAllIncusInstancesForReset() ([]incusListItem, error) {
	res, err := s.commandRunner([]string{"list", "--format", "json"}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, errors.New(firstNonEmpty(res.Stderr, res.Stdout, "incus list failed"))
	}
	var items []incusListItem
	if err := json.Unmarshal([]byte(res.Stdout), &items); err != nil {
		return nil, errors.New("incus list returned invalid JSON")
	}
	return items, nil
}

func resetInstanceSelected(item incusListItem, args ResetIncusStackArgs) bool {
	for _, name := range args.InstanceNames {
		if strings.TrimSpace(name) == item.Name {
			return true
		}
	}
	prefix := strings.TrimSpace(args.InstancePrefix)
	return prefix != "" && strings.HasPrefix(item.Name, prefix)
}

// validateResetInventoryOwnership refuses any selected instance that is not
// owned by the executing host-agent instance. Unowned and foreign instances
// are never deleted by a reset.
func validateResetInventoryOwnership(inventory []ResetIncusInventoryItem, instanceID string) error {
	for _, candidate := range inventory {
		if candidate.Owner != instanceID {
			return fmt.Errorf("reset_incus_stack refuses unowned instance %q (owner %q)", candidate.Name, firstNonEmpty(candidate.Owner, "unowned"))
		}
	}
	return nil
}

// verifyResetIncusStack probes the post-reinstall Incus runtime state: the
// default storage pool, the incusbr0 bridge, and the default-profile root
// disk. The reset is not evidence-complete until all three invariants hold.
func (s *HostOperationsService) verifyResetIncusStack() (map[string]any, error) {
	storage, err := s.commandRunner([]string{"storage", "list", "--format", "json"}, nil, defaultDiscoveryTimeout)
	if err != nil || storage.ExitCode != 0 {
		return nil, errors.New(firstNonEmpty(storage.Stderr, storage.Stdout, errString(err, "verify Incus storage pools failed")))
	}
	poolReady := strings.Contains(storage.Stdout, `"name":"default"`)
	network, err := s.commandRunner([]string{"network", "list", "--format", "json"}, nil, defaultDiscoveryTimeout)
	if err != nil || network.ExitCode != 0 {
		return nil, errors.New(firstNonEmpty(network.Stderr, network.Stdout, errString(err, "verify Incus networks failed")))
	}
	bridgeReady := strings.Contains(network.Stdout, `"name":"incusbr0"`)
	profile, err := s.commandRunner([]string{"profile", "device", "show", "default"}, nil, defaultDiscoveryTimeout)
	if err != nil || profile.ExitCode != 0 {
		return nil, errors.New(firstNonEmpty(profile.Stderr, profile.Stdout, errString(err, "verify Incus default profile failed")))
	}
	profileReady := strings.Contains(profile.Stdout, "root:")
	verified := poolReady && bridgeReady && profileReady
	if !verified {
		return nil, errors.New("Incus runtime verification failed after reset: default pool, incusbr0, or default-profile root disk missing")
	}
	return map[string]any{
		"poolReady":    poolReady,
		"bridgeReady":  bridgeReady,
		"profileReady": profileReady,
		"verified":     true,
	}, nil
}

func resetContainsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func resetInventory(items []incusListItem, args ResetIncusStackArgs) []ResetIncusInventoryItem {
	out := make([]ResetIncusInventoryItem, 0, len(items))
	for _, item := range items {
		if resetInstanceSelected(item, args) {
			out = append(out, ResetIncusInventoryItem{
				Name: item.Name, Status: item.Status, Type: item.Type,
				Owner: pickIncusConfigValue(item, oputeIncusOwnerLabel),
			})
		}
	}
	return out
}

func (s *HostOperationsService) validateResetIncusStack(args ResetIncusStackArgs) error {
	if !args.Confirm {
		return errors.New("reset_incus_stack requires confirm=true")
	}
	if !args.Reinstall {
		return errors.New("reset_incus_stack requires reinstall=true")
	}
	if s.shared.OwnershipMode != "enforce" || !s.ownershipEnabled() {
		return errors.New("reset_incus_stack requires enforced Incus ownership with a host-agent instance id")
	}
	if strings.TrimSpace(s.shared.SharedHostOwnerInstance) == "" {
		return errors.New("reset_incus_stack requires a configured shared host owner")
	}
	if err := s.requireSharedHostOwner("reset_incus_stack"); err != nil {
		return err
	}
	fingerprint := strings.TrimSpace(args.DisposableHostFingerprint)
	expected := strings.TrimSpace(args.ExpectedHostFingerprint)
	if fingerprint == "" || expected == "" || fingerprint != expected {
		return errors.New("reset_incus_stack requires a matching disposable host fingerprint")
	}
	if strings.TrimSpace(args.DisposableHostAuthorization) != "dispose:"+fingerprint {
		return errors.New("reset_incus_stack requires explicit disposable-host authorization")
	}
	if len(args.InstanceNames) == 0 && strings.TrimSpace(args.InstancePrefix) == "" {
		return errors.New("reset_incus_stack requires explicit instanceNames or instancePrefix")
	}
	return nil
}

func (s *HostOperationsService) ResetIncusStack(ctx context.Context, args ResetIncusStackArgs, onData func(string)) (map[string]any, error) {
	if err := s.validateResetIncusStack(args); err != nil {
		return nil, err
	}
	checkpoint, err := s.readResetCheckpoint()
	if err != nil {
		return nil, err
	}
	items, err := s.listAllIncusInstancesForReset()
	if err != nil {
		return nil, err
	}
	inventory := resetInventory(items, args)
	if len(inventory) == 0 {
		if checkpoint == nil || checkpoint.Phase == "reconciled" {
			if checkpoint != nil && checkpoint.Phase == "reconciled" {
				return map[string]any{
					"phase":       checkpoint.Phase,
					"dryRun":      args.DryRun,
					"reinstall":   true,
					"resumable":   true,
					"checkpoint":  checkpoint,
					"alreadyDone": true,
				}, nil
			}
			return nil, errors.New("reset_incus_stack inventory is empty; refusing to infer or broaden targets")
		}
		// A prior run may have deleted every selected instance before its
		// reconciliation phase completed. Resume from the durable checkpoint
		// rather than broadening the target set.
		inventory = append([]ResetIncusInventoryItem(nil), checkpoint.Targets...)
	}
	if err := validateResetInventoryOwnership(inventory, s.shared.InstanceID); err != nil {
		return nil, err
	}
	if checkpoint == nil {
		checkpoint = &resetIncusCheckpoint{
			Targets: inventory,
			Phase:   "validated",
			Deleted: []string{},
		}
	} else if len(checkpoint.Targets) == 0 {
		checkpoint.Targets = inventory
	}
	if err := s.writeResetCheckpoint(*checkpoint); err != nil {
		return nil, fmt.Errorf("write reset checkpoint: %w", err)
	}
	result := map[string]any{
		"phase":        "validated",
		"dryRun":       args.DryRun,
		"reinstall":    true,
		"inventory":    inventory,
		"owner":        s.shared.InstanceID,
		"resumable":    true,
		"nextPhase":    "delete",
		"evidenceMode": "redacted",
		"checkpoint":   checkpoint,
	}
	if args.DryRun {
		return result, nil
	}
	// Stop in-process relays before deleting their target guests. This is
	// revocation, not a best-effort cleanup after a destructive operation.
	if s.guestBridgeRelay != nil {
		s.guestBridgeRelay.stopAll()
	}
	if s.llmSvc != nil {
		s.llm().StopRelays()
	}
	if s.postgresqlServiceRelay != nil {
		s.postgresqlServiceRelay.stopAll()
	}
	for _, candidate := range inventory {
		if resetContainsString(checkpoint.Deleted, candidate.Name) {
			continue
		}
		if err := ctx.Err(); err != nil {
			checkpoint.Phase = "interrupted"
			_ = s.writeResetCheckpoint(*checkpoint)
			return nil, err
		}
		if err := s.assertIncusOwnership(candidate.Name, "reset_incus_stack pre-delete"); err != nil {
			checkpoint.Phase = "interrupted"
			_ = s.writeResetCheckpoint(*checkpoint)
			return nil, err
		}
		checkpoint.Phase = "deleting"
		if err := s.writeResetCheckpoint(*checkpoint); err != nil {
			return nil, fmt.Errorf("write reset checkpoint before delete: %w", err)
		}
		deleteResult, runErr := s.shared.Runtime.RunProviderContext(ctx, []string{"delete", candidate.Name, "--force"}, onData, 5*time.Minute)
		if runErr != nil || deleteResult.ExitCode != 0 {
			checkpoint.Phase = "interrupted"
			_ = s.writeResetCheckpoint(*checkpoint)
			return nil, fmt.Errorf("delete owned instance %q: %s", candidate.Name, firstNonEmpty(deleteResult.Stderr, deleteResult.Stdout, errString(runErr, "delete failed")))
		}
		checkpoint.Deleted = append(checkpoint.Deleted, candidate.Name)
		if err := s.writeResetCheckpoint(*checkpoint); err != nil {
			return nil, fmt.Errorf("write reset checkpoint after delete: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		checkpoint.Phase = "interrupted"
		_ = s.writeResetCheckpoint(*checkpoint)
		return nil, err
	}
	checkpoint.Phase = "reconciling"
	if err := s.writeResetCheckpoint(*checkpoint); err != nil {
		return nil, fmt.Errorf("write reset checkpoint before reconcile: %w", err)
	}
	reconciled, reinstallErr := s.InstallIncusStack(InstallIncusStackArgs{}, onData)
	if reinstallErr != nil {
		checkpoint.Phase = "interrupted"
		_ = s.writeResetCheckpoint(*checkpoint)
		return nil, fmt.Errorf("reconcile Incus runtime after reset: %w", reinstallErr)
	}
	verified, verifyErr := s.verifyResetIncusStack()
	if verifyErr != nil {
		checkpoint.Phase = "interrupted"
		_ = s.writeResetCheckpoint(*checkpoint)
		return nil, fmt.Errorf("verify Incus runtime after reset: %w", verifyErr)
	}
	checkpoint.Phase = "reconciled"
	checkpoint.Reconcile = reconciled
	if err := s.writeResetCheckpoint(*checkpoint); err != nil {
		return nil, fmt.Errorf("write reset checkpoint after reconcile: %w", err)
	}
	result["phase"] = "reconciled"
	result["nextPhase"] = "bootstrap"
	result["reconcileRequired"] = false
	result["incus"] = reconciled
	result["verify"] = verified
	result["checkpoint"] = checkpoint
	return result, nil
}
