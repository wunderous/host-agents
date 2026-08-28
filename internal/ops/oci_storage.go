package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	defaultOciStorageMinAgeSeconds int64 = 24 * 60 * 60
	minOciStorageMinAgeSeconds     int64 = 60 * 60
	maxOciStorageMinAgeSeconds     int64 = 365 * 24 * 60 * 60
	minOciStorageBudgetBytes       int64 = 1 << 30
)

// ConfigureOciStorageArgs persists the OCI image storage budget. Runtime is
// optional and defaults to auto (currently Podman).
type ConfigureOciStorageArgs struct {
	Runtime       string `json:"runtime,omitempty"`
	MaxBytes      *int64 `json:"maxBytes,omitempty"`
	MinAgeSeconds *int64 `json:"minAgeSeconds,omitempty"`
	PruneNow      bool   `json:"pruneNow,omitempty"`
}

type InspectContainerStorageArgs struct {
	Runtime string `json:"runtime,omitempty"`
}

type CleanupContainerStorageArgs struct {
	Runtime       string `json:"runtime,omitempty"`
	MaxBytes      *int64 `json:"maxBytes,omitempty"`
	MinAgeSeconds *int64 `json:"minAgeSeconds,omitempty"`
	DryRun        bool   `json:"dryRun,omitempty"`
}

type ociStoragePolicy struct {
	Runtime       string `json:"runtime,omitempty"`
	MaxBytes      int64  `json:"maxBytes"`
	MinAgeSeconds int64  `json:"minAgeSeconds"`
}

func defaultOciStoragePolicy() ociStoragePolicy {
	return ociStoragePolicy{MinAgeSeconds: defaultOciStorageMinAgeSeconds}
}

func validateOciStoragePolicy(policy ociStoragePolicy) error {
	if policy.MaxBytes < 0 {
		return errors.New("maxBytes must be zero or positive")
	}
	if policy.MaxBytes != 0 && policy.MaxBytes < minOciStorageBudgetBytes {
		return fmt.Errorf("maxBytes must be zero or at least %d", minOciStorageBudgetBytes)
	}
	if policy.MinAgeSeconds < minOciStorageMinAgeSeconds || policy.MinAgeSeconds > maxOciStorageMinAgeSeconds {
		return fmt.Errorf("minAgeSeconds must be between %d and %d", minOciStorageMinAgeSeconds, maxOciStorageMinAgeSeconds)
	}
	if policy.Runtime != "" && policy.Runtime != "auto" && policy.Runtime != "podman" {
		return errors.New("runtime must be one of auto or podman")
	}
	return nil
}

func loadOciStoragePolicyAt(path string) (ociStoragePolicy, error) {
	policy := defaultOciStoragePolicy()
	path = strings.TrimSpace(path)
	if path == "" {
		return policy, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return policy, nil
	}
	if err != nil {
		return policy, fmt.Errorf("read OCI storage policy: %w", err)
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		return policy, fmt.Errorf("parse OCI storage policy: %w", err)
	}
	if policy.MinAgeSeconds == 0 {
		policy.MinAgeSeconds = defaultOciStorageMinAgeSeconds
	}
	if err := validateOciStoragePolicy(policy); err != nil {
		return policy, fmt.Errorf("invalid OCI storage policy: %w", err)
	}
	return policy, nil
}

func (s *HostOperationsService) loadOciStoragePolicy() (ociStoragePolicy, error) {
	return loadOciStoragePolicyAt(s.ociStoragePolicyPath)
}

func saveOciStoragePolicyAt(path string, policy ociStoragePolicy) error {
	if err := validateOciStoragePolicy(policy); err != nil {
		return err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("OCI storage policy path is not configured")
	}
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return fmt.Errorf("encode OCI storage policy: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create OCI storage policy directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".oci-storage-policy-*.tmp")
	if err != nil {
		return fmt.Errorf("create OCI storage policy temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("protect OCI storage policy: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write OCI storage policy: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close OCI storage policy: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install OCI storage policy: %w", err)
	}
	return nil
}

func (s *HostOperationsService) saveOciStoragePolicy(policy ociStoragePolicy) error {
	return saveOciStoragePolicyAt(s.ociStoragePolicyPath, policy)
}

// InspectContainerStorage reports runtime-reported storage categories without
// changing images, containers, volumes, networks, or policy state.
func (s *HostOperationsService) InspectContainerStorage(ctx context.Context, args InspectContainerStorageArgs) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("inspect_container_storage is unsupported on %s host agents", runtime.GOOS)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy, err := s.loadOciStoragePolicy()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Runtime) != "" {
		policy.Runtime = strings.ToLower(strings.TrimSpace(args.Runtime))
	}
	adapter, err := s.resolveContainerRuntime(ctx, policy.Runtime)
	if err != nil {
		return nil, err
	}
	report, err := adapter.Inspect(ctx, policy)
	if err != nil {
		return nil, err
	}
	return report.toMap(), nil
}

// CleanupContainerStorage removes only age-eligible unused images and asks the
// selected adapter to prune supported build cache. It never invokes a broad
// system prune and never force-removes image references.
func (s *HostOperationsService) CleanupContainerStorage(ctx context.Context, args CleanupContainerStorageArgs, onData func(string)) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("cleanup_container_storage is unsupported on %s host agents", runtime.GOOS)
	}
	if err := s.requireSharedHostOwner("cleanup_container_storage"); err != nil {
		return nil, err
	}
	policy, err := s.loadOciStoragePolicy()
	if err != nil {
		return nil, err
	}
	if args.Runtime != "" {
		policy.Runtime = strings.ToLower(strings.TrimSpace(args.Runtime))
	}
	if args.MinAgeSeconds != nil {
		policy.MinAgeSeconds = *args.MinAgeSeconds
	}
	if err := validateCleanupPolicy(policy, args.MaxBytes); err != nil {
		return nil, err
	}
	adapter, err := s.resolveContainerRuntime(ctx, policy.Runtime)
	if err != nil {
		return nil, err
	}
	s.ociStorageMu.Lock()
	defer s.ociStorageMu.Unlock()
	return s.cleanupContainerStorageLocked(ctx, adapter, policy, args.MaxBytes, args.DryRun, onData)
}

func validateCleanupPolicy(policy ociStoragePolicy, maxBytes *int64) error {
	if policy.MinAgeSeconds < minOciStorageMinAgeSeconds || policy.MinAgeSeconds > maxOciStorageMinAgeSeconds {
		return fmt.Errorf("minAgeSeconds must be between %d and %d", minOciStorageMinAgeSeconds, maxOciStorageMinAgeSeconds)
	}
	if maxBytes != nil {
		if *maxBytes < 0 {
			return errors.New("maxBytes must be zero or positive")
		}
		if *maxBytes != 0 && *maxBytes < minOciStorageBudgetBytes {
			return fmt.Errorf("maxBytes must be zero or at least %d", minOciStorageBudgetBytes)
		}
	}
	return nil
}

func (s *HostOperationsService) cleanupContainerStorageLocked(ctx context.Context, adapter containerRuntimeAdapter, policy ociStoragePolicy, maxBytes *int64, dryRun bool, onData func(string)) (map[string]any, error) {
	if maxBytes != nil {
		policy.MaxBytes = *maxBytes
	}
	before, err := adapter.Inspect(ctx, policy)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"runtime":                   adapter.Name(),
		"cleanupScope":              []string{"images", "buildCache"},
		"dryRun":                    dryRun,
		"pruneAttempted":            false,
		"prunedImageCount":          0,
		"estimatedReclaimableBytes": int64(0),
		"maxBytes":                  policy.MaxBytes,
		"minAgeSeconds":             policy.MinAgeSeconds,
		"before":                    before.toMap(),
	}
	warnings := append([]string{}, before.Warnings...)
	cutoff := time.Now().Unix() - policy.MinAgeSeconds
	images, err := adapter.ListImages(ctx)
	if err != nil {
		return nil, err
	}
	candidates := selectOciPruneCandidates(images, cutoff)
	result["candidateImageCount"] = len(candidates)
	var estimatedImageBytes int64
	for _, image := range candidates {
		estimatedImageBytes += image.Size
	}
	result["estimatedImageBytes"] = estimatedImageBytes
	budget := int64(0)
	if maxBytes != nil {
		budget = *maxBytes
	}
	current := before
	if dryRun {
		result["estimatedReclaimableBytes"] = estimatedImageBytes + before.Categories["buildCache"].ReclaimableBytes
	} else {
		for _, image := range candidates {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if budget > 0 && current.TotalBytes <= budget {
				break
			}
			if err := adapter.RemoveImage(ctx, image.ID); err != nil {
				warnings = append(warnings, fmt.Sprintf("skipped image %s: %v", image.ID, err))
				continue
			}
			result["pruneAttempted"] = true
			result["prunedImageCount"] = result["prunedImageCount"].(int) + 1
			if onData != nil {
				onData(fmt.Sprintf("Pruned unused OCI image %s", image.ID))
			}
			if refreshed, refreshErr := adapter.Inspect(ctx, policy); refreshErr == nil {
				current = refreshed
			} else {
				warnings = append(warnings, fmt.Sprintf("refresh storage after image %s: %v", image.ID, refreshErr))
				break
			}
			if budget > 0 && current.TotalBytes <= budget {
				break
			}
		}
	}
	afterImages := current
	if !dryRun {
		if refreshed, refreshErr := adapter.Inspect(ctx, policy); refreshErr == nil {
			afterImages = refreshed
		} else {
			warnings = append(warnings, fmt.Sprintf("refresh storage after image cleanup: %v", refreshErr))
		}
	}
	cacheBudget := int64(0)
	if budget > 0 {
		cacheBudget = maxInt64(0, budget-afterImages.Categories["images"].Bytes)
	}
	cacheSupported := afterImages.Categories["buildCache"].Supported
	if dryRun {
		result["buildCachePruneSupported"] = cacheSupported
	} else if cacheSupported || budget == 0 {
		attempted, reclaimed, cacheErr := adapter.PruneBuildCache(ctx, policy.MinAgeSeconds, cacheBudget, false, onData)
		if cacheErr != nil {
			warnings = append(warnings, cacheErr.Error())
		} else {
			result["pruneAttempted"] = result["pruneAttempted"].(bool) || attempted
			result["prunedBuildCache"] = attempted
			result["estimatedReclaimableBytes"] = result["estimatedReclaimableBytes"].(int64) + reclaimed
		}
	}
	if !dryRun {
		if refreshed, refreshErr := adapter.Inspect(ctx, policy); refreshErr == nil {
			afterImages = refreshed
		} else {
			warnings = append(warnings, fmt.Sprintf("refresh storage after cache cleanup: %v", refreshErr))
		}
	}
	result["after"] = afterImages.toMap()
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return result, nil
}

// ConfigureOciStorage persists the storage policy and optionally runs
// the same safe cleanup operation used by the explicit cleanup tool.
func (s *HostOperationsService) ConfigureOciStorage(ctx context.Context, args ConfigureOciStorageArgs, onData func(string)) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("configure_oci_storage is unsupported on %s host agents", runtime.GOOS)
	}
	if err := s.requireSharedHostOwner("configure_oci_storage"); err != nil {
		return nil, err
	}
	policy, err := s.loadOciStoragePolicy()
	if err != nil {
		return nil, err
	}
	if args.Runtime != "" {
		policy.Runtime = strings.ToLower(strings.TrimSpace(args.Runtime))
	}
	if args.MaxBytes != nil {
		policy.MaxBytes = *args.MaxBytes
	}
	if args.MinAgeSeconds != nil {
		policy.MinAgeSeconds = *args.MinAgeSeconds
	}
	if err := validateOciStoragePolicy(policy); err != nil {
		return nil, err
	}
	s.ociStorageMu.Lock()
	defer s.ociStorageMu.Unlock()
	if err := s.saveOciStoragePolicy(policy); err != nil {
		return nil, err
	}
	adapter, err := s.resolveContainerRuntime(ctx, policy.Runtime)
	if err != nil {
		return nil, err
	}
	if !args.PruneNow {
		report, inspectErr := adapter.Inspect(ctx, policy)
		if inspectErr != nil {
			return nil, inspectErr
		}
		out := report.toMap()
		out["policyUpdated"] = true
		return out, nil
	}
	maxBytes := policy.MaxBytes
	return s.cleanupContainerStorageLocked(ctx, adapter, policy, &maxBytes, false, onData)
}

// enforceOciStoragePolicy is called by image builds only. It is deliberately
// not called by heartbeat processing.
func (s *HostOperationsService) enforceOciStoragePolicy(ctx context.Context, builder string, onData func(string)) (map[string]any, error) {
	if strings.TrimSpace(s.ociStoragePolicyPath) == "" || (builder != "podman" && builder != "auto") {
		return nil, nil
	}
	s.ociStorageMu.Lock()
	defer s.ociStorageMu.Unlock()
	policy, err := s.loadOciStoragePolicy()
	if err != nil {
		return nil, err
	}
	if policy.MaxBytes == 0 {
		return nil, nil
	}
	adapter, err := s.resolveContainerRuntime(ctx, builder)
	if err != nil {
		return nil, err
	}
	return s.cleanupContainerStorageLocked(ctx, adapter, policy, &policy.MaxBytes, false, onData)
}

func selectOciPruneCandidates(images []containerImage, cutoff int64) []containerImage {
	candidates := make([]containerImage, 0, len(images))
	for _, image := range images {
		if strings.TrimSpace(image.ID) == "" || image.Containers != 0 || image.Created <= 0 || image.Created >= cutoff {
			continue
		}
		candidates = append(candidates, image)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Created < candidates[j].Created
	})
	return candidates
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
