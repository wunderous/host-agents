package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	minGuestImageAgeSeconds     int64 = 3600
	defaultGuestImageAgeSeconds       = minGuestImageAgeSeconds
)

type guestImage struct {
	ID           string
	Tags         []string
	Digests      []string
	SizeBytes    int64
	CreatedAt    time.Time
	HasCreatedAt bool
	Pinned       bool
}

type guestContainer struct {
	ID      string
	Image   string
	ImageID string
	State   string
}

type pruneCandidate struct {
	ID        string
	Tags      []string
	SizeBytes int64
	Reason    string
}

func inspectGuestStorage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return inspectGuestStorageWithRunner(ctx, args, runCommand)
}

func inspectGuestStorageWithRunner(ctx context.Context, args map[string]any, run providerCommandRunner) (*mcp.CallToolResult, error) {
	warnings := make([]string, 0)
	root, err := inspectRootFilesystem(ctx, args, run)
	if err != nil {
		warnings = append(warnings, err.Error())
		root = map[string]any{}
	}
	containerd, err := inspectGuestPaths(ctx, args, run, map[string]string{
		"containerdBytes": "/var/lib/rancher/k3s/agent/containerd",
		"contentBytes":    "/var/lib/rancher/k3s/agent/containerd/io.containerd.content.v1.content",
		"snapshotsBytes":  "/var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.overlayfs",
		"k3sStorageBytes": "/var/lib/rancher/k3s/storage",
	})
	if err != nil {
		warnings = append(warnings, err.Error())
	}
	pvcs, err := inspectPVCDirs(ctx, args, run)
	if err != nil {
		warnings = append(warnings, err.Error())
	}
	images, err := listGuestImages(ctx, args, run)
	if err != nil {
		return nil, err
	}
	keep, err := keepSetFromRunningPods(ctx, args, run)
	if err != nil {
		return nil, err
	}
	extraKeep := optionalStringSliceInput(args, "extraKeepTags")
	addKeepRefs(keep, extraKeep)
	candidates, skipped, missingCreated := planUnusedImages(images, keep, defaultGuestImageAgeSeconds, time.Now())
	if missingCreated {
		warnings = append(warnings, "crictl images did not report createdAt; unused images are treated as age-eligible")
	}
	reclaimable := int64(0)
	for _, candidate := range candidates {
		reclaimable += candidate.SizeBytes
	}
	report := map[string]any{
		"targetUri":             stringInput(args, "targetUri"),
		"rootFilesystem":        root,
		"containerd":            containerd,
		"persistentVolumes":     pvcs,
		"imageCount":            len(images),
		"keepSet":               sortedKeepSet(keep),
		"reclaimableImageBytes": reclaimable,
		"reclaimableImages":     pruneCandidatesAsMaps(candidates),
		"keptImages":            skipped,
	}
	if boolInput(args, "includeRegistry") {
		registry, registryErr := inspectRegistryInventory(ctx, args, run)
		if registryErr != nil {
			warnings = append(warnings, registryErr.Error())
		} else {
			report["registry"] = registry
		}
	}
	if len(warnings) > 0 {
		report["warnings"] = warnings
	}
	return structured(report)
}

func pruneUnusedImages(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return pruneUnusedImagesWithRunner(ctx, args, runCommand)
}

func pruneUnusedImagesWithRunner(ctx context.Context, args map[string]any, run providerCommandRunner) (*mcp.CallToolResult, error) {
	dryRun := boolInput(args, "dryRun")
	minAge := guestMinAgeSeconds(args)
	images, err := listGuestImages(ctx, args, run)
	if err != nil {
		return nil, err
	}
	containers, err := listGuestContainers(ctx, args, run)
	if err != nil {
		return nil, err
	}
	keep, err := keepSetFromRunningPods(ctx, args, run)
	if err != nil {
		return nil, err
	}
	addKeepRefs(keep, optionalStringSliceInput(args, "extraKeepTags"))
	for _, container := range containers {
		if isRunningContainer(container.State) {
			addKeepRefs(keep, []string{container.Image, container.ImageID})
		}
	}
	beforeBytes := sumImageBytes(images)
	exited := exitedContainers(containers)
	candidates, skipped, missingCreated := planUnusedImages(images, keep, minAge, time.Now())
	warnings := make([]string, 0)
	if missingCreated {
		warnings = append(warnings, "crictl images did not report createdAt; unused images are treated as age-eligible")
	}
	result := map[string]any{
		"targetUri":         stringInput(args, "targetUri"),
		"dryRun":            dryRun,
		"minAgeSeconds":     minAge,
		"keepSet":           sortedKeepSet(keep),
		"beforeImageBytes":  beforeBytes,
		"exitedContainers":  len(exited),
		"reclaimableImages": pruneCandidatesAsMaps(candidates),
		"skippedImages":     skipped,
	}
	if dryRun {
		result["afterImageBytes"] = beforeBytes
		result["removedImageCount"] = 0
		result["removedContainerCount"] = 0
		if len(warnings) > 0 {
			result["warnings"] = warnings
		}
		return structured(result)
	}
	removedContainers := 0
	for _, container := range exited {
		if _, err := runGuestCrictl(ctx, args, run, "rm", container.ID); err != nil {
			warnings = append(warnings, fmt.Sprintf("remove exited container %s: %v", container.ID, err))
			continue
		}
		removedContainers++
	}
	removedImages := 0
	reclaimed := int64(0)
	for _, candidate := range candidates {
		if _, err := runGuestCrictl(ctx, args, run, "rmi", candidate.ID); err != nil {
			warnings = append(warnings, fmt.Sprintf("remove image %s: %v", firstNonEmpty(strings.Join(candidate.Tags, ","), candidate.ID), err))
			continue
		}
		removedImages++
		reclaimed += candidate.SizeBytes
	}
	afterImages, err := listGuestImages(ctx, args, run)
	if err != nil {
		warnings = append(warnings, err.Error())
		result["afterImageBytes"] = beforeBytes - reclaimed
	} else {
		result["afterImageBytes"] = sumImageBytes(afterImages)
	}
	result["removedImageCount"] = removedImages
	result["removedContainerCount"] = removedContainers
	result["reclaimedImageBytes"] = reclaimed
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return structured(result)
}

func trimGuestStorage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return trimGuestStorageWithRunner(ctx, args, runCommand)
}

var hostFstrim = func(ctx context.Context) ([]byte, error) {
	command := exec.CommandContext(ctx, "sudo", "-n", "fstrim", "-v", "/")
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func trimGuestStorageWithRunner(ctx context.Context, args map[string]any, run providerCommandRunner) (*mcp.CallToolResult, error) {
	output, err := runGuest(ctx, args, run, "fstrim", "-v", "/")
	if err == nil {
		return structured(map[string]any{
			"targetUri": stringInput(args, "targetUri"),
			"trimmed":   true,
			"scope":     "guest",
			"output":    strings.TrimSpace(string(output)),
		})
	}
	if !fitrimDenied(err) {
		return nil, fmt.Errorf("fstrim guest root: %w", err)
	}
	hostOutput, hostErr := hostFstrim(ctx)
	if hostErr != nil {
		return nil, fmt.Errorf("fstrim guest root not permitted and host fstrim failed: %w", hostErr)
	}
	return structured(map[string]any{
		"targetUri":  stringInput(args, "targetUri"),
		"trimmed":    true,
		"scope":      "host",
		"guestError": err.Error(),
		"output":     strings.TrimSpace(string(hostOutput)),
	})
}

func fitrimDenied(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "operation not permitted") || strings.Contains(message, "fitrim")
}

func inspectRootFilesystem(ctx context.Context, args map[string]any, run providerCommandRunner) (map[string]any, error) {
	output, err := runGuest(ctx, args, run, "df", "-B1", "/")
	if err != nil {
		return nil, fmt.Errorf("df: %w", err)
	}
	return parseDFBytes(string(output))
}

func inspectGuestPaths(ctx context.Context, args map[string]any, run providerCommandRunner, paths map[string]string) (map[string]any, error) {
	report := map[string]any{}
	var firstErr error
	for key, path := range paths {
		output, err := runGuest(ctx, args, run, "du", "-sb", path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		size, _, ok := parseDULine(strings.TrimSpace(string(output)))
		if !ok {
			continue
		}
		report[key] = size
	}
	return report, firstErr
}

func inspectPVCDirs(ctx context.Context, args map[string]any, run providerCommandRunner) ([]map[string]any, error) {
	output, err := runGuest(ctx, args, run, "bash", "-lc", "du -sb /var/lib/rancher/k3s/storage/* 2>/dev/null || true")
	if err != nil {
		return nil, err
	}
	pvcs := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		size, path, ok := parseDULine(strings.TrimSpace(line))
		if !ok || path == "" {
			continue
		}
		pvcs = append(pvcs, map[string]any{"path": path, "bytes": size})
	}
	sort.Slice(pvcs, func(i, j int) bool {
		left, _ := pvcs[i]["path"].(string)
		right, _ := pvcs[j]["path"].(string)
		return left < right
	})
	return pvcs, nil
}

func listGuestImages(ctx context.Context, args map[string]any, run providerCommandRunner) ([]guestImage, error) {
	output, err := runGuestCrictl(ctx, args, run, "images", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("list guest images: %w", err)
	}
	return parseCRIImages(output)
}

func listGuestContainers(ctx context.Context, args map[string]any, run providerCommandRunner) ([]guestContainer, error) {
	output, err := runGuestCrictl(ctx, args, run, "ps", "-a", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("list guest containers: %w", err)
	}
	return parseCRIContainers(output)
}

func keepSetFromRunningPods(ctx context.Context, args map[string]any, run providerCommandRunner) (map[string]struct{}, error) {
	output, err := runGuestKubectl(ctx, args, run, "get", "pods", "-A", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("list pods for image keep-set: %w", err)
	}
	return keepSetFromPodJSON(output), nil
}

func keepSetFromPodJSON(raw []byte) map[string]struct{} {
	keep := map[string]struct{}{}
	var document struct {
		Items []struct {
			Spec struct {
				InitContainers []struct {
					Image string `json:"image"`
				} `json:"initContainers"`
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
				EphemeralContainers []struct {
					Image string `json:"image"`
				} `json:"ephemeralContainers"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return keep
	}
	for _, item := range document.Items {
		for _, container := range item.Spec.InitContainers {
			addKeepRefs(keep, []string{container.Image})
		}
		for _, container := range item.Spec.Containers {
			addKeepRefs(keep, []string{container.Image})
		}
		for _, container := range item.Spec.EphemeralContainers {
			addKeepRefs(keep, []string{container.Image})
		}
	}
	return keep
}

func addKeepRefs(keep map[string]struct{}, refs []string) {
	for _, ref := range refs {
		for _, alias := range imageAliases(ref) {
			keep[alias] = struct{}{}
		}
	}
}

func imageAliases(ref string) []string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	seen := map[string]struct{}{}
	aliases := make([]string, 0, 8)
	add := func(value string) {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		aliases = append(aliases, value)
	}
	add(ref)
	trimmed := strings.ToLower(ref)
	if digest := strings.Index(trimmed, "@sha256:"); digest >= 0 {
		add(trimmed[:digest])
		add(trimmed[digest+1:])
		add(trimmed[digest+len("@sha256:"):])
		trimmed = trimmed[:digest]
	}
	if repo, ok := stripRegistryHost(trimmed); ok {
		add(repo)
		trimmed = repo
	}
	if strings.HasPrefix(trimmed, "library/") {
		add(strings.TrimPrefix(trimmed, "library/"))
	}
	return aliases
}

func stripRegistryHost(ref string) (string, bool) {
	slash := strings.Index(ref, "/")
	if slash < 0 {
		return "", false
	}
	host := ref[:slash]
	if host == "localhost" || strings.Contains(host, ".") || strings.Contains(host, ":") {
		return ref[slash+1:], true
	}
	return "", false
}

func planUnusedImages(images []guestImage, keep map[string]struct{}, minAgeSeconds int64, now time.Time) ([]pruneCandidate, []map[string]any, bool) {
	candidates := make([]pruneCandidate, 0)
	skipped := make([]map[string]any, 0)
	missingCreated := false
	for _, image := range images {
		if image.Pinned || imageMatchesKeepSet(image, keep) {
			skipped = append(skipped, map[string]any{"id": image.ID, "tags": image.Tags, "sizeBytes": image.SizeBytes, "reason": "keep-set"})
			continue
		}
		if image.HasCreatedAt && now.Sub(image.CreatedAt) < time.Duration(minAgeSeconds)*time.Second {
			skipped = append(skipped, map[string]any{"id": image.ID, "tags": image.Tags, "sizeBytes": image.SizeBytes, "reason": "age-gate"})
			continue
		}
		if !image.HasCreatedAt {
			missingCreated = true
		}
		candidates = append(candidates, pruneCandidate{ID: image.ID, Tags: image.Tags, SizeBytes: image.SizeBytes, Reason: "unused"})
	}
	return candidates, skipped, missingCreated
}

func imageMatchesKeepSet(image guestImage, keep map[string]struct{}) bool {
	refs := append([]string{image.ID}, image.Tags...)
	refs = append(refs, image.Digests...)
	for _, ref := range refs {
		for _, alias := range imageAliases(ref) {
			if _, ok := keep[alias]; ok {
				return true
			}
		}
	}
	return false
}

func parseCRIImages(raw []byte) ([]guestImage, error) {
	var document struct {
		Images []struct {
			ID          string   `json:"id"`
			RepoTags    []string `json:"repoTags"`
			RepoDigests []string `json:"repoDigests"`
			Size        any      `json:"size"`
			CreatedAt   any      `json:"createdAt"`
			Pinned      bool     `json:"pinned"`
		} `json:"images"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("parse crictl images: %w", err)
	}
	images := make([]guestImage, 0, len(document.Images))
	for _, item := range document.Images {
		created, hasCreated := parseCRITime(item.CreatedAt)
		images = append(images, guestImage{
			ID:           strings.TrimSpace(item.ID),
			Tags:         cleanStringSlice(item.RepoTags),
			Digests:      cleanStringSlice(item.RepoDigests),
			SizeBytes:    anyToInt64(item.Size),
			CreatedAt:    created,
			HasCreatedAt: hasCreated,
			Pinned:       item.Pinned,
		})
	}
	return images, nil
}

func parseCRIContainers(raw []byte) ([]guestContainer, error) {
	var document struct {
		Containers []struct {
			ID    string `json:"id"`
			State string `json:"state"`
			Image struct {
				Image string `json:"image"`
			} `json:"image"`
			ImageRef string `json:"imageRef"`
		} `json:"containers"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("parse crictl ps: %w", err)
	}
	containers := make([]guestContainer, 0, len(document.Containers))
	for _, item := range document.Containers {
		containers = append(containers, guestContainer{
			ID:      strings.TrimSpace(item.ID),
			Image:   strings.TrimSpace(item.Image.Image),
			ImageID: strings.TrimSpace(item.ImageRef),
			State:   strings.TrimSpace(item.State),
		})
	}
	return containers, nil
}

func parseCRITime(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case string:
		text := strings.TrimSpace(typed)
		if text == "" || text == "0" {
			return time.Time{}, false
		}
		if unix, err := strconv.ParseInt(text, 10, 64); err == nil {
			return unixToTime(unix), unix > 0
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		return parsed, err == nil
	case float64:
		if typed <= 0 {
			return time.Time{}, false
		}
		return unixToTime(int64(typed)), true
	case json.Number:
		unix, err := typed.Int64()
		if err != nil || unix <= 0 {
			return time.Time{}, false
		}
		return unixToTime(unix), true
	default:
		return time.Time{}, false
	}
}

func unixToTime(value int64) time.Time {
	if value > 1e12 {
		return time.Unix(0, value)
	}
	return time.Unix(value, 0)
}

func parseDFBytes(output string) (map[string]any, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("df output was empty")
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 6 {
		return nil, fmt.Errorf("df output was malformed")
	}
	total, _ := strconv.ParseInt(fields[1], 10, 64)
	used, _ := strconv.ParseInt(fields[2], 10, 64)
	available, _ := strconv.ParseInt(fields[3], 10, 64)
	return map[string]any{
		"filesystem": fields[0],
		"totalBytes": total,
		"usedBytes":  used,
		"availBytes": available,
		"mount":      fields[len(fields)-1],
	}, nil
}

func parseDULine(line string) (int64, string, bool) {
	if line == "" {
		return 0, "", false
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, "", false
	}
	size, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return size, strings.Join(fields[1:], " "), true
}

func exitedContainers(containers []guestContainer) []guestContainer {
	exited := make([]guestContainer, 0)
	for _, container := range containers {
		if isRunningContainer(container.State) || strings.TrimSpace(container.ID) == "" {
			continue
		}
		exited = append(exited, container)
	}
	return exited
}

func isRunningContainer(state string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(state))
	return strings.Contains(normalized, "RUNNING") || normalized == "CONTAINER_CREATED"
}

func sumImageBytes(images []guestImage) int64 {
	var total int64
	for _, image := range images {
		total += image.SizeBytes
	}
	return total
}

func pruneCandidatesAsMaps(candidates []pruneCandidate) []map[string]any {
	out := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, map[string]any{"id": candidate.ID, "tags": candidate.Tags, "sizeBytes": candidate.SizeBytes, "reason": candidate.Reason})
	}
	return out
}

func sortedKeepSet(keep map[string]struct{}) []string {
	out := make([]string, 0, len(keep))
	for value := range keep {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func guestMinAgeSeconds(args map[string]any) int64 {
	value := int64(integerInput(args, "minAgeSeconds"))
	if value < minGuestImageAgeSeconds {
		return defaultGuestImageAgeSeconds
	}
	return value
}

func optionalStringSliceInput(args map[string]any, key string) []string {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return cleanStringSlice(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			text, ok := value.(string)
			if !ok {
				continue
			}
			text = strings.TrimSpace(text)
			if text == "" || strings.ContainsAny(text, "\x00\r\n") {
				continue
			}
			out = append(out, text)
		}
		return out
	default:
		return nil
	}
}

func cleanStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "<none>" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func anyToInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func runGuest(ctx context.Context, args map[string]any, run providerCommandRunner, argv ...string) ([]byte, error) {
	instance := stringInput(args, "providerInstanceName")
	return run(ctx, append([]string{"exec", instance, "--"}, argv...), nil)
}

func runGuestKubectl(ctx context.Context, args map[string]any, run providerCommandRunner, kubectlArgs ...string) ([]byte, error) {
	return runGuest(ctx, args, run, append([]string{"k3s", "kubectl"}, kubectlArgs...)...)
}

func runGuestCrictl(ctx context.Context, args map[string]any, run providerCommandRunner, crictlArgs ...string) ([]byte, error) {
	if err := refuseCRIAllDelete(crictlArgs); err != nil {
		return nil, err
	}
	return runGuest(ctx, args, run, append([]string{"k3s", "crictl"}, crictlArgs...)...)
}

func refuseCRIAllDelete(crictlArgs []string) error {
	if len(crictlArgs) == 0 || crictlArgs[0] != "rmi" {
		return nil
	}
	for _, arg := range crictlArgs[1:] {
		if arg == "--all" || arg == "-a" {
			return fmt.Errorf("refusing crictl rmi --all")
		}
	}
	return nil
}
