package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

func TestNormalizeContainerStorageReportPodman(t *testing.T) {
	report, err := normalizeContainerStorageReport(
		"podman",
		"/usr/bin/podman",
		[]byte(`{"store":{"graphRoot":"/home/opute/.local/share/containers/storage","graphDriverName":"overlay"}}`),
		[]byte(`[{"Type":"Images","RawSize":1000,"RawReclaimable":400},{"Type":"Containers","RawSize":200,"RawReclaimable":50},{"Type":"Local Volumes","RawSize":300,"RawReclaimable":0}]`),
		nil,
		defaultOciStoragePolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.StoragePath != "/home/opute/.local/share/containers/storage" || report.StorageDriver != "overlay" {
		t.Fatalf("unexpected storage metadata: %#v", report)
	}
	if report.TotalBytes != 1500 || report.ReclaimableBytes != 450 {
		t.Fatalf("unexpected totals: %#v", report)
	}
	if report.Categories["buildCache"].Supported || !reflect.DeepEqual(report.UnsupportedCategories, []string{"buildCache"}) {
		t.Fatalf("expected explicit unsupported build cache: %#v", report)
	}
}

func TestParseFutureRuntimeJSONFormats(t *testing.T) {
	entries, err := parseStorageEntries([]byte("{\"Images\":{\"Size\":\"1.5GB\",\"Reclaimable\":\"1GB (66%)\"}}\n{\"Containers\":{\"Size\":\"2MB\",\"Reclaimable\":\"1MB (50%)\"}}"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || firstInt64(entries[0], "Size") != 1_500_000_000 {
		t.Fatalf("unexpected normalized entries: %#v", entries)
	}
}

func TestSelectOciPruneCandidatesProtectsAgeAndContainers(t *testing.T) {
	candidates := selectOciPruneCandidates([]containerImage{
		{ID: "old-unused", Created: 10, Containers: 0, Size: 100},
		{ID: "old-referenced", Created: 10, Containers: 1, Size: 100},
		{ID: "new-unused", Created: 90, Containers: 0, Size: 100},
	}, 50)
	if len(candidates) != 1 || candidates[0].ID != "old-unused" {
		t.Fatalf("unexpected candidates: %#v", candidates)
	}
}

type fakeContainerRuntime struct {
	report         containerStorageReport
	images         []containerImage
	removed        []string
	untagged       []string
	cachePruned    bool
	cacheSupported bool
}

func (f *fakeContainerRuntime) Name() string                { return "podman" }
func (f *fakeContainerRuntime) Path() string                { return "/fake/podman" }
func (f *fakeContainerRuntime) Ready(context.Context) error { return nil }
func (f *fakeContainerRuntime) Inspect(context.Context, ociStoragePolicy) (containerStorageReport, error) {
	return f.report, nil
}
func (f *fakeContainerRuntime) ListImages(context.Context) ([]containerImage, error) {
	return f.images, nil
}
func (f *fakeContainerRuntime) RemoveImage(_ context.Context, id string) error {
	for _, image := range f.images {
		if image.ID == id && image.Containers != 0 {
			return errors.New("image is referenced")
		}
	}
	f.removed = append(f.removed, id)
	f.report.Categories["images"] = containerStorageCategory{Supported: true, Bytes: 100, ReclaimableBytes: 0}
	f.report.TotalBytes = 100
	return nil
}
func (f *fakeContainerRuntime) PruneBuildCache(context.Context, int64, int64, bool, func(string)) (bool, int64, error) {
	if !f.cacheSupported {
		return false, 0, errors.New("build cache unsupported")
	}
	f.cachePruned = true
	return true, 50, nil
}
func (f *fakeContainerRuntime) Build(context.Context, string, string, string, string, map[string]string, func(string)) error {
	return nil
}
func (f *fakeContainerRuntime) Push(context.Context, string, bool, func(string)) error { return nil }
func (f *fakeContainerRuntime) Untag(_ context.Context, ref string, _ func(string)) error {
	f.untagged = append(f.untagged, ref)
	return nil
}

func TestCleanupContainerStorageDryRunAndSafety(t *testing.T) {
	now := time.Now().Unix()
	fake := &fakeContainerRuntime{
		report: containerStorageReport{
			Runtime: "podman", Categories: map[string]containerStorageCategory{
				"images": {Supported: true, Bytes: 200}, "containers": {Supported: true}, "volumes": {Supported: true},
				"buildCache": {Supported: false, Reason: "unsupported"},
			}, TotalBytes: 200, Warnings: []string{"build cache unsupported"},
		},
		images: []containerImage{{ID: "old-unused", Created: now - 120, Containers: 0, Size: 100}, {ID: "old-running", Created: now - 120, Containers: 1, Size: 100}, {ID: "new-unused", Created: now - 10, Containers: 0, Size: 100}},
	}
	service := &HostOperationsService{}
	result, err := service.cleanupContainerStorageLocked(context.Background(), fake, ociStoragePolicy{Runtime: "podman", MinAgeSeconds: 60}, ptrInt64(150), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.removed) != 0 || result["candidateImageCount"] != 1 {
		t.Fatalf("dry-run mutated or selected unsafe images: removed=%v result=%#v", fake.removed, result)
	}
	if !strings.Contains(strings.Join(result["warnings"].([]string), ","), "build cache") {
		t.Fatalf("missing unsupported-cache warning: %#v", result)
	}
}

func ptrInt64(value int64) *int64 { return &value }

func TestBuildAndPushPodmanAdapterArguments(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	service := &HostOperationsService{
		containerCommandFn: func(_ context.Context, command string, args ...string) ([]byte, error) {
			if command != "/fake/podman" || len(args) < 1 || args[0] != "info" {
				return nil, errors.New("unexpected readiness command")
			}
			return []byte(`{"store":{"graphRoot":"/store","graphDriverName":"overlay"}}`), nil
		},
		containerStreamingCommandFn: func(_ context.Context, command string, args []string, _ func(string)) error {
			calls = append(calls, append([]string{command}, args...))
			return nil
		},
		shared: hostruntime.Shared{
			ContainerLookPathFn: func(command string) (string, error) {
				if command == "podman" {
					return "/fake/podman", nil
				}
				return "", errors.New("not found")
			},
		},
	}
	out, err := service.BuildAndPushOciImage(context.Background(), BuildAndPushOciImageArgs{
		ContextDir: dir, Image: "registry.example/app:test", InsecureRegistry: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("expected build, push and untag calls, got %#v", calls)
	}
	if !reflect.DeepEqual(calls[0][1:], []string{"build", "-f", dockerfile, "-t", "registry.example/app:test", dir}) {
		t.Fatalf("unexpected build args: %#v", calls[0])
	}
	if !reflect.DeepEqual(calls[1][1:], []string{"push", "--tls-verify=false", "registry.example/app:test"}) {
		t.Fatalf("unexpected push args: %#v", calls[1])
	}
	if !reflect.DeepEqual(calls[2][1:], []string{"image", "untag", "registry.example/app:test"}) {
		t.Fatalf("unexpected untag args: %#v", calls[2])
	}
	if out["runtime"] != "podman" {
		t.Fatalf("unexpected build result: %#v", out)
	}
	if out["untagAfterPush"] != true || out["untaggedImage"] != "registry.example/app:test" {
		t.Fatalf("expected default untag-after-push result, got %#v", out)
	}
}

func TestBuildAndPushPodmanAdapterBuildArgs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	service := &HostOperationsService{
		containerCommandFn: func(_ context.Context, command string, args ...string) ([]byte, error) {
			if command != "/fake/podman" || len(args) < 1 || args[0] != "info" {
				return nil, errors.New("unexpected readiness command")
			}
			return []byte(`{"store":{"graphRoot":"/store","graphDriverName":"overlay"}}`), nil
		},
		containerStreamingCommandFn: func(_ context.Context, command string, args []string, _ func(string)) error {
			calls = append(calls, append([]string{command}, args...))
			return nil
		},
		shared: hostruntime.Shared{
			ContainerLookPathFn: func(command string) (string, error) {
				if command == "podman" {
					return "/fake/podman", nil
				}
				return "", errors.New("not found")
			},
		},
	}
	_, err := service.BuildAndPushOciImage(context.Background(), BuildAndPushOciImageArgs{
		ContextDir: dir, Image: "registry.example/app:test", BuildArgs: map[string]string{"ZED": "last", "ALPHA": "first"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("expected build, push and untag calls, got %#v", calls)
	}
	want := []string{"build", "-f", dockerfile, "-t", "registry.example/app:test", "--build-arg", "ALPHA=first", "--build-arg", "ZED=last", dir}
	if !reflect.DeepEqual(calls[0][1:], want) {
		t.Fatalf("unexpected build args: %#v", calls[0])
	}
}

func TestSplitOciImageRef(t *testing.T) {
	cases := []struct {
		ref        string
		repository string
		digestOnly bool
	}{
		{"registry.example/app:test", "registry.example/app", false},
		{"app:test", "app", false},
		{"app", "app", false},
		{"registry.example/app@sha256:abc123", "registry.example/app", true},
		{"app@sha256:abc123", "app", true},
		{"registry.example/ns/app:latest", "registry.example/ns/app", false},
		{"  registry.example/app:test  ", "registry.example/app", false},
		{"", "", false},
	}
	for _, tc := range cases {
		repository, digestOnly := splitOciImageRef(tc.ref)
		if repository != tc.repository || digestOnly != tc.digestOnly {
			t.Fatalf("splitOciImageRef(%q) = (%q, %v), want (%q, %v)", tc.ref, repository, digestOnly, tc.repository, tc.digestOnly)
		}
	}
}

func TestBuildAndPushPodmanAdapterUntagOptOutAndDigestRef(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	newService := func() *HostOperationsService {
		return &HostOperationsService{
			containerCommandFn: func(_ context.Context, command string, args ...string) ([]byte, error) {
				if command != "/fake/podman" || len(args) < 1 || args[0] != "info" {
					return nil, errors.New("unexpected readiness command")
				}
				return []byte(`{"store":{"graphRoot":"/store","graphDriverName":"overlay"}}`), nil
			},
			containerStreamingCommandFn: func(_ context.Context, command string, args []string, _ func(string)) error {
				calls = append(calls, append([]string{command}, args...))
				return nil
			},
			shared: hostruntime.Shared{
				ContainerLookPathFn: func(command string) (string, error) {
					if command == "podman" {
						return "/fake/podman", nil
					}
					return "", errors.New("not found")
				},
			},
		}
	}
	calls = nil
	service := newService()
	falseValue := false
	trueValue := true
	out, err := service.BuildAndPushOciImage(context.Background(), BuildAndPushOciImageArgs{
		ContextDir: dir, Image: "registry.example/app:test", UntagAfterPush: &falseValue,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected build and push only when untagAfterPush=false, got %#v", calls)
	}
	if out["untagAfterPush"] != false {
		t.Fatalf("expected untagAfterPush=false result, got %#v", out)
	}
	if _, present := out["untaggedImage"]; present {
		t.Fatalf("unexpected untaggedImage for explicit opt-out: %#v", out)
	}

	calls = nil
	out, err = service.BuildAndPushOciImage(context.Background(), BuildAndPushOciImageArgs{
		ContextDir: dir, Image: "registry.example/app@sha256:abc123", UntagAfterPush: &trueValue,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected no untag call for digest-pinned ref, got %#v", calls)
	}
	if out["untagSkippedReason"] != "digest-pinned reference has no tag to remove" {
		t.Fatalf("expected digest-pinned untag skip note, got %#v", out)
	}
}

func TestBuildAndPushPodmanAdapterUntagFailureIsWarning(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	service := &HostOperationsService{
		containerCommandFn: func(_ context.Context, command string, args ...string) ([]byte, error) {
			if command != "/fake/podman" || len(args) < 1 || args[0] != "info" {
				return nil, errors.New("unexpected readiness command")
			}
			return []byte(`{"store":{"graphRoot":"/store","graphDriverName":"overlay"}}`), nil
		},
		containerStreamingCommandFn: func(_ context.Context, command string, args []string, _ func(string)) error {
			calls = append(calls, append([]string{command}, args...))
			if len(args) >= 1 && args[0] == "image" && len(args) >= 2 && args[1] == "untag" {
				return errors.New("untag failed: store not empty")
			}
			return nil
		},
		shared: hostruntime.Shared{
			ContainerLookPathFn: func(command string) (string, error) {
				if command == "podman" {
					return "/fake/podman", nil
				}
				return "", errors.New("not found")
			},
		},
	}
	out, err := service.BuildAndPushOciImage(context.Background(), BuildAndPushOciImageArgs{
		ContextDir: dir, Image: "registry.example/app:test",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("expected build, push and untag calls, got %#v", calls)
	}
	if out["pushed"] != true {
		t.Fatalf("push result must survive untag failure, got %#v", out)
	}
	if out["untagWarning"] != "untag failed: store not empty" {
		t.Fatalf("expected untagWarning on untag failure, got %#v", out)
	}
	if _, present := out["untaggedImage"]; present {
		t.Fatalf("unexpected untaggedImage for failed untag: %#v", out)
	}
}
