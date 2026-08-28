package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
)

// containerRuntimeAdapter is the host-side abstraction for OCI runtime
// operations. The current release registers only the Podman implementation;
// Docker can be added later without changing the MCP storage contract.
type containerRuntimeAdapter interface {
	Name() string
	Path() string
	Ready(context.Context) error
	Inspect(context.Context, ociStoragePolicy) (containerStorageReport, error)
	ListImages(context.Context) ([]containerImage, error)
	RemoveImage(context.Context, string) error
	PruneBuildCache(context.Context, int64, int64, bool, func(string)) (bool, int64, error)
	Build(context.Context, string, string, string, string, map[string]string, func(string)) error
	Push(context.Context, string, bool, func(string)) error
	// Untag removes only the local tag reference for a pushed image. The
	// underlying image and its layers stay in the local store so the storage
	// policy decides when they are reclaimable; this prevents a stale local
	// tag from silently feeding a later --pull=never build.
	Untag(context.Context, string, func(string)) error
}

type podmanRuntimeAdapter struct {
	service *Service
	path    string
}

func (r *podmanRuntimeAdapter) Name() string { return "podman" }
func (r *podmanRuntimeAdapter) Path() string { return r.path }

func (r *podmanRuntimeAdapter) Ready(ctx context.Context) error {
	_, err := r.service.runContainerCommand(ctx, r.path, "info", "--format", "json")
	if err != nil {
		return fmt.Errorf("Podman is not ready: %w", err)
	}
	return nil
}

func (r *podmanRuntimeAdapter) Inspect(ctx context.Context, policy ociStoragePolicy) (containerStorageReport, error) {
	infoOutput, err := r.service.runContainerCommand(ctx, r.path, "info", "--format", "json")
	if err != nil {
		return containerStorageReport{}, fmt.Errorf("inspect Podman runtime: %w", err)
	}
	storageOutput, err := r.service.runContainerCommand(ctx, r.path, "system", "df", "--format", "json")
	if err != nil {
		return containerStorageReport{}, fmt.Errorf("inspect Podman storage: %w", err)
	}
	return normalizeContainerStorageReport("podman", r.path, infoOutput, storageOutput, nil, policy)
}

func (r *podmanRuntimeAdapter) ListImages(ctx context.Context) ([]containerImage, error) {
	output, err := r.service.runContainerCommand(ctx, r.path, "images", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("list Podman images: %w", err)
	}
	return parseContainerImages(output)
}

func (r *podmanRuntimeAdapter) RemoveImage(ctx context.Context, imageID string) error {
	// Do not force removal. Podman will refuse an image referenced by a
	// container, which is the safety boundary for stopped and running guests.
	_, err := r.service.runContainerCommand(ctx, r.path, "image", "rm", imageID)
	return err
}

func (r *podmanRuntimeAdapter) PruneBuildCache(_ context.Context, _ int64, _ int64, _ bool, _ func(string)) (bool, int64, error) {
	return false, 0, errors.New("Podman build-cache inspection and pruning is unavailable through a safe machine-readable operation")
}

func (r *podmanRuntimeAdapter) Build(ctx context.Context, dockerfile, image, platform, contextDir string, buildArgs map[string]string, onData func(string)) error {
	args := []string{"build", "-f", dockerfile, "-t", image}
	if strings.TrimSpace(platform) != "" {
		args = append(args, "--platform", platform)
	}
	args = appendOciBuildArgs(args, buildArgs)
	args = append(args, contextDir)
	return r.service.runContainerStreamingCommand(ctx, r.path, args, onData)
}

func appendOciBuildArgs(args []string, buildArgs map[string]string) []string {
	if len(buildArgs) == 0 {
		return args
	}
	keys := make([]string, 0, len(buildArgs))
	for key := range buildArgs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--build-arg", key+"="+buildArgs[key])
	}
	return args
}

func (r *podmanRuntimeAdapter) Push(ctx context.Context, image string, insecure bool, onData func(string)) error {
	args := []string{"push"}
	if insecure {
		args = append(args, "--tls-verify=false")
	}
	args = append(args, image)
	return r.service.runContainerStreamingCommand(ctx, r.path, args, onData)
}

// Untag removes the single local tag for the given reference. Podman keeps
// the image layers (usable by the storage policy's age-gated cleanup) and
// only drops the tag entry, which makes a later local build with the same tag
// pull from the registry again instead of silently reusing a stale digest.
// Digest-pinned references have no removable tag and are refused.
func (r *podmanRuntimeAdapter) Untag(ctx context.Context, ref string, onData func(string)) error {
	_, digestOnly := splitOciImageRef(ref)
	if digestOnly {
		return fmt.Errorf("cannot untag digest-pinned reference %q", ref)
	}
	return r.service.runContainerStreamingCommand(ctx, r.path, []string{"image", "untag", ref}, onData)
}

// splitOciImageRef splits an image reference into its repository and optional
// tag. A tag is the trailing :tag segment after the last slash when that
// segment contains no digest separator; digestOnly reports refs pinned by
// @digest (with or without a tag prefix), which carry no removable local tag
// of their own.
func splitOciImageRef(ref string) (repository string, digestOnly bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	if digestIndex := strings.Index(ref, "@"); digestIndex >= 0 {
		return ref[:digestIndex], true
	}
	lastSlash := strings.LastIndex(ref, "/")
	if lastSlash < 0 {
		lastSlash = -1
	}
	if colonIndex := strings.LastIndex(ref, ":"); colonIndex > lastSlash {
		return ref[:colonIndex], false
	}
	return ref, false
}

type containerStorageCategory struct {
	Supported        bool
	Bytes            int64
	ReclaimableBytes int64
	Reason           string
}

type containerStorageReport struct {
	Runtime               string
	Executable            string
	StoragePath           string
	StorageDriver         string
	Categories            map[string]containerStorageCategory
	TotalBytes            int64
	ReclaimableBytes      int64
	UnsupportedCategories []string
	Warnings              []string
	Policy                ociStoragePolicy
}

func (r containerStorageReport) toMap() map[string]any {
	categories := map[string]any{}
	for _, name := range []string{"images", "containers", "volumes", "buildCache"} {
		category := r.Categories[name]
		entry := map[string]any{
			"supported":        category.Supported,
			"bytes":            category.Bytes,
			"reclaimableBytes": category.ReclaimableBytes,
		}
		if category.Reason != "" {
			entry["reason"] = category.Reason
		}
		categories[name] = entry
	}
	return map[string]any{
		"runtime":               r.Runtime,
		"executable":            r.Executable,
		"storagePath":           r.StoragePath,
		"storageDriver":         r.StorageDriver,
		"accounting":            "runtime-reported",
		"categories":            categories,
		"totalBytes":            r.TotalBytes,
		"reclaimableBytes":      r.ReclaimableBytes,
		"unsupportedCategories": r.UnsupportedCategories,
		"warnings":              r.Warnings,
		"policy":                map[string]any{"maxBytes": r.Policy.MaxBytes, "minAgeSeconds": r.Policy.MinAgeSeconds},
	}
}

type containerJSONEntry map[string]any

func normalizeContainerStorageReport(runtimeName, executable string, infoOutput, storageOutput, cacheOutput []byte, policy ociStoragePolicy) (containerStorageReport, error) {
	info, err := parseJSONMap(infoOutput)
	if err != nil {
		return containerStorageReport{}, fmt.Errorf("parse %s runtime information: %w", runtimeName, err)
	}
	report := containerStorageReport{
		Runtime: runtimeName, Executable: executable, Categories: make(map[string]containerStorageCategory), Policy: policy,
	}
	report.StoragePath = firstString(info, "DockerRootDir", "GraphRoot", "graphRoot", "RootDir")
	report.StorageDriver = firstString(info, "Driver", "GraphDriverName", "graphDriverName")
	if store, ok := nestedRuntimeMap(info, "Store", "store"); ok {
		if report.StoragePath == "" {
			report.StoragePath = firstString(store, "GraphRoot", "graphRoot", "RunRoot", "runRoot")
		}
		if report.StorageDriver == "" {
			report.StorageDriver = firstString(store, "GraphDriverName", "graphDriverName", "Driver")
		}
	}
	entries, err := parseStorageEntries(storageOutput)
	if err != nil {
		return containerStorageReport{}, fmt.Errorf("parse %s storage usage: %w", runtimeName, err)
	}
	for _, entry := range entries {
		name := normalizeStorageCategory(firstString(entry, "Type", "type", "Name", "name"))
		if name == "" {
			continue
		}
		category := containerStorageCategory{Supported: true}
		category.Bytes = firstInt64(entry, "RawSize", "rawSize", "Size", "size", "Total", "total")
		category.ReclaimableBytes = firstInt64(entry, "RawReclaimable", "rawReclaimable", "Reclaimable", "reclaimable")
		report.Categories[name] = category
	}
	for _, name := range []string{"images", "containers", "volumes"} {
		if _, ok := report.Categories[name]; !ok {
			report.Categories[name] = containerStorageCategory{Supported: true}
		}
	}
	if _, ok := report.Categories["buildCache"]; !ok {
		report.Categories["buildCache"] = containerStorageCategory{Supported: false, Reason: "runtime does not expose a safe machine-readable build-cache operation"}
		report.UnsupportedCategories = []string{"buildCache"}
		report.Warnings = []string{"build-cache inspection and pruning is unavailable for this runtime"}
	}
	if len(cacheOutput) > 0 {
		cacheBytes, cacheReclaimable, cacheErr := parseBuildCacheUsage(cacheOutput)
		if cacheErr == nil {
			report.Categories["buildCache"] = containerStorageCategory{Supported: true, Bytes: cacheBytes, ReclaimableBytes: cacheReclaimable}
			report.UnsupportedCategories = nil
			report.Warnings = nil
		}
	}
	for _, category := range report.Categories {
		if category.Supported {
			report.TotalBytes += category.Bytes
			report.ReclaimableBytes += category.ReclaimableBytes
		}
	}
	return report, nil
}

func parseJSONMap(output []byte) (map[string]any, error) {
	var value map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(output))), &value); err != nil {
		return nil, err
	}
	return value, nil
}

func parseStorageEntries(output []byte) ([]containerJSONEntry, error) {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return nil, errors.New("empty storage usage output")
	}
	if strings.HasPrefix(trimmed, "[") {
		var entries []containerJSONEntry
		if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
			return nil, err
		}
		return entries, nil
	}
	if strings.HasPrefix(trimmed, "{") {
		var object containerJSONEntry
		if err := json.Unmarshal([]byte(trimmed), &object); err == nil {
			if _, ok := object["Type"]; ok {
				return []containerJSONEntry{object}, nil
			}
			entries := make([]containerJSONEntry, 0, len(object))
			for key, raw := range object {
				if entry, ok := raw.(map[string]any); ok {
					entry["Type"] = key
					entries = append(entries, entry)
				}
			}
			if len(entries) > 0 {
				return entries, nil
			}
		}
	}
	var entries []containerJSONEntry
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry containerJSONEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, err
		}
		if _, hasType := entry["Type"]; !hasType && len(entry) == 1 {
			for key, raw := range entry {
				if nested, ok := raw.(map[string]any); ok {
					nested["Type"] = key
					entry = nested
				}
			}
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return nil, errors.New("no storage usage entries")
	}
	return entries, nil
}

func parseBuildCacheUsage(output []byte) (int64, int64, error) {
	entries, err := parseStorageEntries(output)
	if err != nil {
		return 0, 0, err
	}
	var total, reclaimable int64
	for _, entry := range entries {
		size := firstInt64(entry, "Size", "size", "RawSize", "rawSize")
		total += size
		if boolValue(entry["Reclaimable"]) || boolValue(entry["reclaimable"]) {
			reclaimable += size
		} else if value := firstInt64(entry, "RawReclaimable", "rawReclaimable"); value > 0 {
			reclaimable += value
		}
	}
	return total, reclaimable, nil
}

func normalizeStorageCategory(value string) string {
	switch strings.ToLower(strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.TrimSpace(value))) {
	case "images", "image":
		return "images"
	case "containers", "container":
		return "containers"
	case "volumes", "volume", "localvolumes":
		return "volumes"
	case "buildcache", "cache":
		return "buildCache"
	default:
		return ""
	}
}

func nestedRuntimeMap(value map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		if raw, ok := value[key].(map[string]any); ok {
			return raw, true
		}
	}
	return nil, false
}

func firstString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

var byteMetricPattern = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*([kmgtpe]?i?b)?`)

func firstInt64(value map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if raw, ok := value[key]; ok {
			if parsed, ok := parseByteMetric(raw); ok {
				return parsed
			}
		}
	}
	return 0
}

func parseByteMetric(value any) (int64, bool) {
	switch raw := value.(type) {
	case int:
		return int64(raw), true
	case int64:
		return raw, true
	case float64:
		return int64(raw), true
	case json.Number:
		parsed, err := raw.Int64()
		return parsed, err == nil
	case string:
		match := byteMetricPattern.FindStringSubmatch(raw)
		if len(match) == 0 {
			return 0, false
		}
		number, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			return 0, false
		}
		multiplier := float64(1)
		switch strings.ToLower(match[2]) {
		case "kb":
			multiplier = 1e3
		case "kib":
			multiplier = 1 << 10
		case "mb":
			multiplier = 1e6
		case "mib":
			multiplier = 1 << 20
		case "gb":
			multiplier = 1e9
		case "gib":
			multiplier = 1 << 30
		case "tb":
			multiplier = 1e12
		case "tib":
			multiplier = 1 << 40
		case "pb":
			multiplier = 1e15
		case "pib":
			multiplier = 1 << 50
		}
		return int64(number * multiplier), true
	default:
		return 0, false
	}
}

func boolValue(value any) bool {
	parsed, ok := value.(bool)
	return ok && parsed
}

type containerImage struct {
	ID         string
	Created    int64
	Containers int
	Size       int64
}

func parseContainerImages(output []byte) ([]containerImage, error) {
	entries, err := parseStorageEntries(output)
	if err != nil {
		return nil, err
	}
	images := make([]containerImage, 0, len(entries))
	for _, entry := range entries {
		id := firstString(entry, "Id", "ID", "id")
		created := firstInt64(entry, "Created", "created")
		if created == 0 {
			created = parseImageTime(firstString(entry, "CreatedAt", "createdAt"))
		}
		images = append(images, containerImage{ID: id, Created: created, Containers: int(firstInt64(entry, "Containers", "containers")), Size: firstInt64(entry, "Size", "size")})
	}
	return images, nil
}

func parseImageTime(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05 -0700 MST", "2006-01-02 15:04:05 -0700"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Unix()
		}
	}
	return 0
}

func (s *Service) resolveContainerRuntime(ctx context.Context, requested string) (containerRuntimeAdapter, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		requested = "auto"
	}
	if requested != "auto" && requested != "podman" {
		return nil, errors.New("runtime must be one of auto or podman")
	}
	path, err := s.shared.ContainerLookPath("podman")
	if requested == "podman" && err != nil {
		return nil, errors.New("podman is not installed")
	}
	if err == nil {
		adapter := &podmanRuntimeAdapter{service: s, path: path}
		if readyErr := adapter.Ready(ctx); readyErr == nil {
			return adapter, nil
		} else if requested == "podman" {
			return nil, readyErr
		}
	}
	if requested == "auto" {
		return nil, errors.New("no ready OCI container runtime found; install and start Podman")
	}
	return nil, errors.New("podman is unavailable")
}

func (s *Service) runContainerCommand(ctx context.Context, command string, args ...string) ([]byte, error) {
	if s != nil && s.containerCommandFn != nil {
		return s.containerCommandFn(ctx, command, args...)
	}
	return runCommand(ctx, command, args...)
}

func (s *Service) runContainerStreamingCommand(ctx context.Context, command string, args []string, onData func(string)) error {
	if s != nil && s.containerStreamingCommandFn != nil {
		return s.containerStreamingCommandFn(ctx, command, args, onData)
	}
	return runStreamingCommand(execCommand(ctx, command, args...), onData)
}

// Command execution forwards to internal/exec, which owns the process
// primitives. The local spellings stay as vars so this package's adapter tests
// can still replace them.
var (
	runCommand  = hostexec.Run
	execCommand = hostexec.Command
)
