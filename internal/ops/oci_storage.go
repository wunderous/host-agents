package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

// ConfigureOciStorageArgs configures the host-local Podman image storage
// budget. A zero MaxBytes disables enforcement. MinAgeSeconds protects newly
// built images from an immediate cleanup pass.
type ConfigureOciStorageArgs struct {
	MaxBytes      *int64 `json:"maxBytes,omitempty"`
	MinAgeSeconds *int64 `json:"minAgeSeconds,omitempty"`
	PruneNow      bool   `json:"pruneNow,omitempty"`
}

type ociStoragePolicy struct {
	MaxBytes      int64 `json:"maxBytes"`
	MinAgeSeconds int64 `json:"minAgeSeconds"`
}

type podmanStorageDFEntry struct {
	Type           string `json:"Type"`
	RawSize        int64  `json:"RawSize"`
	RawReclaimable int64  `json:"RawReclaimable"`
}

type podmanImage struct {
	ID         string `json:"Id"`
	Created    int64  `json:"Created"`
	Containers int    `json:"Containers"`
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
	return nil
}

func (s *HostOperationsService) loadOciStoragePolicy() (ociStoragePolicy, error) {
	policy := defaultOciStoragePolicy()
	path := strings.TrimSpace(s.ociStoragePolicyPath)
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

func (s *HostOperationsService) saveOciStoragePolicy(policy ociStoragePolicy) error {
	if err := validateOciStoragePolicy(policy); err != nil {
		return err
	}
	path := strings.TrimSpace(s.ociStoragePolicyPath)
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

// ConfigureOciStorage persists the Podman image budget and optionally runs an
// immediate safe prune. Enforcement is explicit and build-triggered; it is not
// placed in the heartbeat path.
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("podman"); err != nil {
		return nil, errors.New("podman is required for OCI storage retention")
	}

	s.ociStorageMu.Lock()
	defer s.ociStorageMu.Unlock()
	policy, err := s.loadOciStoragePolicy()
	if err != nil {
		return nil, err
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
	if err := s.saveOciStoragePolicy(policy); err != nil {
		return nil, err
	}
	return s.enforceOciStoragePolicyLocked(ctx, policy, onData, args.PruneNow)
}

func (s *HostOperationsService) enforceOciStoragePolicy(ctx context.Context, builder string, onData func(string)) (map[string]any, error) {
	if builder != "podman" || strings.TrimSpace(s.ociStoragePolicyPath) == "" {
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
	return s.enforceOciStoragePolicyLocked(ctx, policy, onData, true)
}

func (s *HostOperationsService) enforceOciStoragePolicyLocked(ctx context.Context, policy ociStoragePolicy, onData func(string), prune bool) (map[string]any, error) {
	status, err := podmanStorageStatus(ctx, policy)
	if err != nil {
		return nil, err
	}
	status["prunedImageCount"] = 0
	status["pruneAttempted"] = prune
	if !prune || policy.MaxBytes == 0 || status["imageBytes"].(int64) <= policy.MaxBytes {
		return status, nil
	}

	cutoff := time.Now().Unix() - policy.MinAgeSeconds
	images, err := listPodmanImages(ctx)
	if err != nil {
		return nil, err
	}
	candidates := selectOciPruneCandidates(images, cutoff)
	initialCandidateCount := len(candidates)
	prunedCount := 0
	for pass := 0; len(candidates) > 0; pass++ {
		progress := false
		for _, image := range candidates {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := removePodmanImage(ctx, image.ID); err != nil {
				if onData != nil {
					onData(fmt.Sprintf("Skipping OCI image %s: %v", image.ID, err))
				}
				continue
			}
			progress = true
			prunedCount++
			if onData != nil {
				onData(fmt.Sprintf("Pruned unused OCI image %s", image.ID))
			}
			status, err = podmanStorageStatus(ctx, policy)
			if err != nil {
				return nil, err
			}
			status["prunedImageCount"] = prunedCount
			status["pruneAttempted"] = true
			if status["imageBytes"].(int64) <= policy.MaxBytes {
				break
			}
		}
		if status["imageBytes"].(int64) <= policy.MaxBytes || !progress || pass >= initialCandidateCount {
			break
		}
		images, err = listPodmanImages(ctx)
		if err != nil {
			return nil, err
		}
		candidates = selectOciPruneCandidates(images, cutoff)
	}
	status["remainingOverLimitBytes"] = maxInt64(0, status["imageBytes"].(int64)-policy.MaxBytes)
	return status, nil
}

func podmanStorageStatus(ctx context.Context, policy ociStoragePolicy) (map[string]any, error) {
	output, err := runPodman(ctx, "system", "df", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("inspect Podman storage: %w", err)
	}
	var entries []podmanStorageDFEntry
	if err := json.Unmarshal(output, &entries); err != nil {
		return nil, fmt.Errorf("parse Podman storage usage: %w", err)
	}
	var imageBytes, reclaimableBytes int64
	for _, entry := range entries {
		if strings.EqualFold(entry.Type, "Images") {
			imageBytes = entry.RawSize
			reclaimableBytes = entry.RawReclaimable
			break
		}
	}
	remainingOverLimitBytes := int64(0)
	if policy.MaxBytes > 0 {
		remainingOverLimitBytes = maxInt64(0, imageBytes-policy.MaxBytes)
	}
	return map[string]any{
		"scope":                   "images-only",
		"policy":                  map[string]any{"maxBytes": policy.MaxBytes, "minAgeSeconds": policy.MinAgeSeconds},
		"imageBytes":              imageBytes,
		"imageReclaimableBytes":   reclaimableBytes,
		"remainingOverLimitBytes": remainingOverLimitBytes,
	}, nil
}

func listPodmanImages(ctx context.Context) ([]podmanImage, error) {
	output, err := runPodman(ctx, "images", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("list Podman images: %w", err)
	}
	var images []podmanImage
	if err := json.Unmarshal(output, &images); err != nil {
		return nil, fmt.Errorf("parse Podman images: %w", err)
	}
	return images, nil
}

func selectOciPruneCandidates(images []podmanImage, cutoff int64) []podmanImage {
	candidates := make([]podmanImage, 0, len(images))
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

func removePodmanImage(ctx context.Context, imageID string) error {
	_, err := runPodman(ctx, "image", "rm", "--force", imageID)
	return err
}

func runPodman(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "podman", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text != "" {
			return nil, fmt.Errorf("%w: %s", err, text)
		}
		return nil, err
	}
	return output, nil
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
