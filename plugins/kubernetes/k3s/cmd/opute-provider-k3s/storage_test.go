package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestKeepSetFromPodJSONIncludesInitAndEphemeral(t *testing.T) {
	keep := keepSetFromPodJSON([]byte(`{
		"items": [{
			"spec": {
				"initContainers": [{"image": "docker.io/library/busybox:1.36"}],
				"containers": [{"image": "10.0.100.66:30500/opute/platform:abc"}],
				"ephemeralContainers": [{"image": "registry:3"}]
			}
		}]
	}`))
	for _, want := range []string{"busybox:1.36", "opute/platform:abc", "registry:3"} {
		if _, ok := keep[want]; !ok {
			t.Fatalf("keep-set missing %q: %#v", want, keep)
		}
	}
}

func TestPlanUnusedImagesHonoursKeepSetAndAgeGate(t *testing.T) {
	keep := map[string]struct{}{"nginx:latest": {}}
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	images := []guestImage{
		{ID: "sha256:keep", Tags: []string{"nginx:latest"}, SizeBytes: 10},
		{ID: "sha256:fresh", Tags: []string{"stale:old"}, SizeBytes: 20, HasCreatedAt: true, CreatedAt: now.Add(-30 * time.Minute)},
		{ID: "sha256:old", Tags: []string{"stale:older"}, SizeBytes: 40, HasCreatedAt: true, CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "sha256:undated", Tags: []string{"untagged"}, SizeBytes: 5},
	}
	candidates, skipped, missing := planUnusedImages(images, keep, 3600, now)
	if !missing {
		t.Fatal("undated unused image should warn about missing createdAt")
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %#v", candidates)
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped = %#v", skipped)
	}
}

func TestPruneUnusedImagesDryRunDoesNotMutate(t *testing.T) {
	var commands []string
	run := recordingGuestRunner(&commands, guestStorageFixtures())
	result, err := pruneUnusedImagesWithRunner(context.Background(), clusterArgs(map[string]any{"dryRun": true}), run)
	if err != nil {
		t.Fatal(err)
	}
	object, _ := result.StructuredContent.(map[string]any)
	if object["dryRun"] != true || object["removedImageCount"] != 0 {
		t.Fatalf("dry-run result = %#v", object)
	}
	for _, command := range commands {
		if strings.Contains(command, " crictl rmi ") || strings.Contains(command, " crictl rm ") {
			t.Fatalf("dry-run mutated guest: %s", command)
		}
	}
}

func TestPruneUnusedImagesRemovesExitedThenUnused(t *testing.T) {
	var commands []string
	run := recordingGuestRunner(&commands, guestStorageFixtures())
	result, err := pruneUnusedImagesWithRunner(context.Background(), clusterArgs(nil), run)
	if err != nil {
		t.Fatal(err)
	}
	object, _ := result.StructuredContent.(map[string]any)
	if object["removedContainerCount"] != 1 || object["removedImageCount"] != 1 {
		t.Fatalf("prune result = %#v", object)
	}
	joined := strings.Join(commands, "\n")
	if !strings.Contains(joined, "k3s crictl rm exited-1") {
		t.Fatalf("did not remove exited container: %s", joined)
	}
	if !strings.Contains(joined, "k3s crictl rmi sha256:unused") {
		t.Fatalf("did not remove unused image: %s", joined)
	}
	if strings.Contains(joined, "k3s crictl rmi sha256:keep") || strings.Contains(joined, "rmi --all") {
		t.Fatalf("removed a keep-set image or used --all: %s", joined)
	}
}

func TestGarbageCollectRegistrySkippedWithoutFlag(t *testing.T) {
	var commands []string
	run := recordingGuestRunner(&commands, map[string]string{})
	result, err := garbageCollectRegistryWithRunner(context.Background(), clusterArgs(nil), run)
	if err != nil {
		t.Fatal(err)
	}
	object, _ := result.StructuredContent.(map[string]any)
	if object["skipped"] != true {
		t.Fatalf("result = %#v", object)
	}
	if len(commands) != 0 {
		t.Fatalf("skipped GC still executed: %#v", commands)
	}
}

func TestPlanRegistryDeletesUsesKeepSet(t *testing.T) {
	keep := keepSetFromPodJSON([]byte(`{"items":[{"spec":{"containers":[{"image":"10.0.100.66:30500/opute/platform:abc"}]}}]}`))
	addKeepRefs(keep, []string{"opute/web:keep"})
	deleteSet, retained := planRegistryDeletes([]registryTag{
		{Repository: "opute/platform", Tag: "abc"},
		{Repository: "opute/web", Tag: "keep"},
		{Repository: "opute/old", Tag: "dead"},
	}, keep)
	if len(deleteSet) != 1 || deleteSet[0].Repository != "opute/old" {
		t.Fatalf("deleteSet = %#v", deleteSet)
	}
	if len(retained) != 2 {
		t.Fatalf("retained = %#v", retained)
	}
}

func TestGarbageCollectRegistryDryRunDoesNotScale(t *testing.T) {
	var commands []string
	run := recordingGuestRunner(&commands, registryFixtures())
	result, err := garbageCollectRegistryWithRunner(context.Background(), clusterArgs(map[string]any{"includeRegistry": true, "dryRun": true}), run)
	if err != nil {
		t.Fatal(err)
	}
	object, _ := result.StructuredContent.(map[string]any)
	if object["garbageCollected"] != false || object["dryRun"] != true {
		t.Fatalf("result = %#v", object)
	}
	for _, command := range commands {
		if strings.Contains(command, " scale ") || strings.Contains(command, "garbage-collect") || strings.Contains(command, " -X DELETE ") {
			t.Fatalf("dry-run mutated registry: %s", command)
		}
	}
}

func TestTrimGuestStorageRunsFstrim(t *testing.T) {
	var commands []string
	run := recordingGuestRunner(&commands, map[string]string{"fstrim -v /": "/: 68 GiB trimmed"})
	result, err := trimGuestStorageWithRunner(context.Background(), clusterArgs(nil), run)
	if err != nil {
		t.Fatal(err)
	}
	object, _ := result.StructuredContent.(map[string]any)
	if object["trimmed"] != true || object["scope"] != "guest" {
		t.Fatalf("result = %#v", object)
	}
	if !strings.Contains(strings.Join(commands, "\n"), "fstrim -v /") {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestTrimGuestStorageFallsBackToHostWhenFITRIMDenied(t *testing.T) {
	original := hostFstrim
	t.Cleanup(func() { hostFstrim = original })
	hostFstrim = func(context.Context) ([]byte, error) { return []byte("/: 12 GiB trimmed"), nil }
	var commands []string
	run := recordingGuestRunner(&commands, map[string]string{"fstrim -v /": "ERR:fstrim: /: FITRIM ioctl failed: Operation not permitted"})
	result, err := trimGuestStorageWithRunner(context.Background(), clusterArgs(nil), run)
	if err != nil {
		t.Fatal(err)
	}
	object, _ := result.StructuredContent.(map[string]any)
	if object["trimmed"] != true || object["scope"] != "host" {
		t.Fatalf("result = %#v", object)
	}
}

func TestRefuseCRIAllDelete(t *testing.T) {
	if err := refuseCRIAllDelete([]string{"rmi", "--all"}); err == nil {
		t.Fatal("expected refusal")
	}
	if err := refuseCRIAllDelete([]string{"ps", "-a"}); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryGCJobOmitsConfigMapWhenDeployHasNone(t *testing.T) {
	withConfig := registryGCJobManifest(registryRef{Namespace: "opute-registry", Name: "opute-registry", Image: "registry:3", ClaimName: "opute-registry-data", ConfigName: "opute-registry"})
	if !strings.Contains(withConfig, "configMap:") || !strings.Contains(withConfig, "name: opute-registry") {
		t.Fatalf("expected configMap when ConfigName is set:\n%s", withConfig)
	}
	without := registryGCJobManifest(registryRef{Namespace: "opute-registry", Name: "opute-registry", Image: "registry:3", ClaimName: "opute-registry-data"})
	if strings.Contains(without, "configMap:") || strings.Contains(without, "mountPath: /etc/distribution") {
		t.Fatalf("default image config must be used when the deploy has no configMap:\n%s", without)
	}
	if !strings.Contains(without, "claimName: opute-registry-data") {
		t.Fatalf("pvc: %s", without)
	}
}

func TestStorageReclaimRecipeOrchestratesInspectPruneGCTrim(t *testing.T) {
	raw, err := os.ReadFile("../../recipes/storage-reclaim.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		"contractVersion: host-recipe.v1",
		"inspect_guest_storage",
		"prune_unused_cluster_images",
		"garbage_collect_cluster_registry",
		"trim_guest_storage",
		"dryRun: true",
		"shutdown_wsl",
		"FITRIM",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("recipe missing %q", want)
		}
	}
	if strings.Contains(text, "tool: compact_wsl_disk") {
		t.Fatal("compact_wsl_disk must stay outside the host-local recipe because it can require shutdown_wsl")
	}
	for _, node := range []string{"prune-dry-run", "prune", "registry-gc", "trim"} {
		if !strings.Contains(text, "id: "+node) || !strings.Contains(text, "validate:") {
			t.Fatalf("mutating node %q must declare a readiness validate block", node)
		}
	}
}

func TestInspectGuestStorageReportsReclaimable(t *testing.T) {
	var commands []string
	run := recordingGuestRunner(&commands, guestStorageFixtures())
	result, err := inspectGuestStorageWithRunner(context.Background(), clusterArgs(nil), run)
	if err != nil {
		t.Fatal(err)
	}
	object, _ := result.StructuredContent.(map[string]any)
	if object["reclaimableImageBytes"] != int64(40) {
		t.Fatalf("reclaimable = %#v", object["reclaimableImageBytes"])
	}
}

func clusterArgs(extra map[string]any) map[string]any {
	args := map[string]any{
		"targetUri":            "cluster:local:opute-ha-b",
		"providerInstanceName": "opute-ha-b",
		"instanceType":         "container",
	}
	for key, value := range extra {
		args[key] = value
	}
	return args
}

func recordingGuestRunner(commands *[]string, responses map[string]string) providerCommandRunner {
	return func(_ context.Context, args []string, input []byte) ([]byte, error) {
		joined := strings.Join(args, " ")
		*commands = append(*commands, joined)
		haystack := joined
		if len(input) > 0 {
			haystack += "\n" + string(input)
		}
		bestNeedle := ""
		bestOutput := ""
		for needle, output := range responses {
			if strings.Contains(haystack, needle) && len(needle) >= len(bestNeedle) {
				bestNeedle = needle
				bestOutput = output
			}
		}
		if bestNeedle == "" {
			return nil, fmt.Errorf("unexpected command: %s", joined)
		}
		if strings.HasPrefix(bestOutput, "ERR:") {
			return nil, fmt.Errorf("%s", strings.TrimPrefix(bestOutput, "ERR:"))
		}
		return []byte(bestOutput), nil
	}
}

func guestStorageFixtures() map[string]string {
	return map[string]string{
		"df -B1 /": "Filesystem     1B-blocks       Used Available Use% Mounted on\n/dev/sda1 105576759296 62212374528 38900000000  62% /",
		"du -sb /var/lib/rancher/k3s/agent/containerd":                                        "6800000000\t/var/lib/rancher/k3s/agent/containerd",
		"du -sb /var/lib/rancher/k3s/agent/containerd/io.containerd.content.v1.content":       "1000\t/var/lib/rancher/k3s/agent/containerd/io.containerd.content.v1.content",
		"du -sb /var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.overlayfs": "2000\t/var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.overlayfs",
		"du -sb /var/lib/rancher/k3s/storage":                                                 "22000000000\t/var/lib/rancher/k3s/storage",
		"du -sb /var/lib/rancher/k3s/storage/*":                                               "22000000000\t/var/lib/rancher/k3s/storage/pvc-registry",
		"k3s crictl images -o json":                                                           `{"images":[{"id":"sha256:keep","repoTags":["nginx:latest"],"size":"10"},{"id":"sha256:unused","repoTags":["stale:old"],"size":"40"}]}`,
		"k3s crictl ps -a -o json":                                                            `{"containers":[{"id":"running-1","state":"CONTAINER_RUNNING","image":{"image":"nginx:latest"},"imageRef":"sha256:keep"},{"id":"exited-1","state":"CONTAINER_EXITED","image":{"image":"stale:old"},"imageRef":"sha256:unused"}]}`,
		"k3s kubectl get pods -A -o json":                                                     `{"items":[{"spec":{"containers":[{"image":"nginx:latest"}]}}]}`,
		"k3s crictl rm exited-1":                                                              "",
		"k3s crictl rmi sha256:unused":                                                        "",
	}
}

func registryFixtures() map[string]string {
	fixtures := guestStorageFixtures()
	fixtures["k3s kubectl get deploy -A -o json"] = `{"items":[{"metadata":{"name":"opute-registry","namespace":"registry-system"},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"registry:3"}],"volumes":[{"name":"data","persistentVolumeClaim":{"claimName":"opute-registry"}},{"name":"config","configMap":{"name":"opute-registry"}}]}}}}]}`
	fixtures["k3s kubectl -n registry-system get svc opute-registry -o json"] = `{"spec":{"clusterIP":"10.43.0.10","ports":[{"port":5000}]}}`
	fixtures["/v2/_catalog"] = `{"repositories":["opute/platform","opute/old"]}`
	fixtures["/v2/opute/platform/tags/list"] = `{"name":"opute/platform","tags":["abc"]}`
	fixtures["/v2/opute/old/tags/list"] = `{"name":"opute/old","tags":["dead"]}`
	fixtures["k3s kubectl get pods -A -o json"] = `{"items":[{"spec":{"containers":[{"image":"10.43.0.10:5000/opute/platform:abc"}]}}]}`
	return fixtures
}
