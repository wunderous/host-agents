package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultRegistryName      = "opute-registry"
	defaultRegistryNamespace = "registry-system"
)

var (
	registryScaleTimeout = 2 * time.Minute
	registryJobTimeout   = 10 * time.Minute
	registryReadyTimeout = 3 * time.Minute
	registryPollInterval = 2 * time.Second
)

type registryRef struct {
	Namespace   string
	Name        string
	Image       string
	Replicas    int
	ClaimName   string
	ConfigName  string
	ServiceIP   string
	ServicePort int
}

type registryTag struct {
	Repository string
	Tag        string
}

func garbageCollectRegistry(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return garbageCollectRegistryWithRunner(ctx, args, runCommand)
}

func garbageCollectRegistryWithRunner(ctx context.Context, args map[string]any, run providerCommandRunner) (*mcp.CallToolResult, error) {
	if !boolInput(args, "includeRegistry") {
		return structured(map[string]any{
			"targetUri": stringInput(args, "targetUri"),
			"skipped":   true,
			"reason":    "includeRegistry is false",
		})
	}
	dryRun := boolInput(args, "dryRun")
	registry, err := discoverRegistry(ctx, args, run)
	if err != nil {
		return nil, err
	}
	keep, err := keepSetFromRunningPods(ctx, args, run)
	if err != nil {
		return nil, err
	}
	addKeepRefs(keep, optionalStringSliceInput(args, "extraKeepTags"))
	tags, err := listRegistryTags(ctx, args, run, registry)
	if err != nil {
		return nil, err
	}
	deleteSet, retained := planRegistryDeletes(tags, keep)
	result := map[string]any{
		"targetUri":        stringInput(args, "targetUri"),
		"dryRun":           dryRun,
		"registry":         registry.Name,
		"namespace":        registry.Namespace,
		"tagCount":         len(tags),
		"tagsToDelete":     registryTagsAsMaps(deleteSet),
		"retainedTags":     registryTagsAsMaps(retained),
		"originalReplicas": registry.Replicas,
	}
	if dryRun {
		result["deletedManifests"] = 0
		result["garbageCollected"] = false
		result["restored"] = false
		return structured(result)
	}
	deleted := 0
	warnings := make([]string, 0)
	for _, tag := range deleteSet {
		if err := deleteRegistryManifest(ctx, args, run, registry, tag); err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		deleted++
	}
	if err := scaleRegistry(ctx, args, run, registry, 0); err != nil {
		return nil, err
	}
	if err := waitForRegistryReplicas(ctx, args, run, registry, 0); err != nil {
		_ = scaleRegistry(ctx, args, run, registry, restoreReplicas(registry.Replicas))
		return nil, err
	}
	if err := runRegistryGarbageCollectJob(ctx, args, run, registry); err != nil {
		_ = scaleRegistry(ctx, args, run, registry, restoreReplicas(registry.Replicas))
		return nil, err
	}
	if err := scaleRegistry(ctx, args, run, registry, restoreReplicas(registry.Replicas)); err != nil {
		return nil, err
	}
	if err := waitForRegistryReady(ctx, args, run, registry); err != nil {
		return nil, err
	}
	after, err := listRegistryTags(ctx, args, run, registry)
	if err != nil {
		warnings = append(warnings, err.Error())
	} else {
		result["retainedAfter"] = registryTagsAsMaps(after)
	}
	result["deletedManifests"] = deleted
	result["garbageCollected"] = true
	result["restored"] = true
	result["ready"] = true
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return structured(result)
}

func inspectRegistryInventory(ctx context.Context, args map[string]any, run providerCommandRunner) (map[string]any, error) {
	registry, err := discoverRegistry(ctx, args, run)
	if err != nil {
		return nil, err
	}
	tags, err := listRegistryTags(ctx, args, run, registry)
	if err != nil {
		return map[string]any{
			"name":      registry.Name,
			"namespace": registry.Namespace,
			"error":     err.Error(),
		}, nil
	}
	repos := map[string]int{}
	for _, tag := range tags {
		repos[tag.Repository]++
	}
	return map[string]any{
		"name":      registry.Name,
		"namespace": registry.Namespace,
		"tagCount":  len(tags),
		"repoCount": len(repos),
	}, nil
}

func discoverRegistry(ctx context.Context, args map[string]any, run providerCommandRunner) (registryRef, error) {
	wantName := firstNonEmpty(stringInput(args, "registryName"), defaultRegistryName)
	wantNamespace := stringInput(args, "registryNamespace")
	output, err := runGuestKubectl(ctx, args, run, "get", "deploy", "-A", "-o", "json")
	if err != nil {
		return registryRef{}, fmt.Errorf("list registry deployments: %w", err)
	}
	var document struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(output, &document); err != nil {
		return registryRef{}, fmt.Errorf("parse registry deployments: %w", err)
	}
	var match map[string]any
	for _, item := range document.Items {
		name := mapString(item, "metadata", "name")
		namespace := mapString(item, "metadata", "namespace")
		if wantNamespace != "" && namespace != wantNamespace {
			continue
		}
		if name == wantName || name == "registry" && namespace == defaultRegistryNamespace {
			match = item
			break
		}
	}
	if match == nil {
		return registryRef{}, fmt.Errorf("registry deployment %q was not found", wantName)
	}
	ref := registryRef{
		Namespace:  mapString(match, "metadata", "namespace"),
		Name:       mapString(match, "metadata", "name"),
		Image:      firstContainerImage(match),
		Replicas:   int(nestedNumber(match, "spec", "replicas")),
		ClaimName:  firstPVCName(match),
		ConfigName: firstConfigMapName(match),
	}
	if ref.Replicas < 0 {
		ref.Replicas = 1
	}
	service, err := runGuestKubectl(ctx, args, run, "-n", ref.Namespace, "get", "svc", ref.Name, "-o", "json")
	if err != nil {
		return registryRef{}, fmt.Errorf("get registry service: %w", err)
	}
	var svc map[string]any
	if err := json.Unmarshal(service, &svc); err != nil {
		return registryRef{}, fmt.Errorf("parse registry service: %w", err)
	}
	ref.ServiceIP = mapString(svc, "spec", "clusterIP")
	ref.ServicePort = registryServicePort(svc)
	if net.ParseIP(ref.ServiceIP) == nil || ref.ServicePort <= 0 {
		return registryRef{}, fmt.Errorf("registry service %s/%s has no cluster IP", ref.Namespace, ref.Name)
	}
	return ref, nil
}

func listRegistryTags(ctx context.Context, args map[string]any, run providerCommandRunner, registry registryRef) ([]registryTag, error) {
	catalogRaw, err := registryHTTP(ctx, args, run, registry, "GET", "/v2/_catalog", "")
	if err != nil {
		return nil, err
	}
	var catalog struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.Unmarshal(catalogRaw, &catalog); err != nil {
		return nil, fmt.Errorf("parse registry catalog: %w", err)
	}
	tags := make([]registryTag, 0)
	for _, repo := range catalog.Repositories {
		if repo == "" || strings.ContainsAny(repo, "\x00\r\n ") {
			continue
		}
		listRaw, err := registryHTTP(ctx, args, run, registry, "GET", "/v2/"+repo+"/tags/list", "")
		if err != nil {
			return nil, err
		}
		var listed struct {
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal(listRaw, &listed); err != nil {
			return nil, fmt.Errorf("parse tags for %s: %w", repo, err)
		}
		for _, tag := range listed.Tags {
			if strings.TrimSpace(tag) == "" {
				continue
			}
			tags = append(tags, registryTag{Repository: repo, Tag: tag})
		}
	}
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].Repository == tags[j].Repository {
			return tags[i].Tag < tags[j].Tag
		}
		return tags[i].Repository < tags[j].Repository
	})
	return tags, nil
}

func planRegistryDeletes(tags []registryTag, keep map[string]struct{}) ([]registryTag, []registryTag) {
	deleteSet := make([]registryTag, 0)
	retained := make([]registryTag, 0)
	for _, tag := range tags {
		if registryTagKept(tag, keep) {
			retained = append(retained, tag)
			continue
		}
		deleteSet = append(deleteSet, tag)
	}
	return deleteSet, retained
}

func registryTagKept(tag registryTag, keep map[string]struct{}) bool {
	refs := []string{tag.Repository + ":" + tag.Tag}
	for _, ref := range refs {
		for _, alias := range imageAliases(ref) {
			if _, ok := keep[alias]; ok {
				return true
			}
		}
	}
	return false
}

func deleteRegistryManifest(ctx context.Context, args map[string]any, run providerCommandRunner, registry registryRef, tag registryTag) error {
	digest, err := registryManifestDigest(ctx, args, run, registry, tag)
	if err != nil {
		return err
	}
	_, err = registryHTTP(ctx, args, run, registry, "DELETE", "/v2/"+tag.Repository+"/manifests/"+digest, "")
	return err
}

func registryManifestDigest(ctx context.Context, args map[string]any, run providerCommandRunner, registry registryRef, tag registryTag) (string, error) {
	script := fmt.Sprintf("curl -sI -H 'Accept: application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json' %s | tr -d '\\r' | awk -F': ' 'tolower($1)==\"docker-content-digest\"{print $2}'", registryURL(registry, "/v2/"+tag.Repository+"/manifests/"+tag.Tag))
	output, err := runGuest(ctx, args, run, "bash", "-lc", script)
	if err != nil {
		return "", fmt.Errorf("resolve digest for %s:%s: %w", tag.Repository, tag.Tag, err)
	}
	digest := strings.TrimSpace(string(output))
	if !strings.HasPrefix(digest, "sha256:") {
		return "", fmt.Errorf("registry did not return a digest for %s:%s", tag.Repository, tag.Tag)
	}
	return digest, nil
}

func registryHTTP(ctx context.Context, args map[string]any, run providerCommandRunner, registry registryRef, method, path, body string) ([]byte, error) {
	endpoint := registryURL(registry, path)
	script := fmt.Sprintf("curl -sf --max-time 30 -X %s %s", shellQuote(method), shellQuote(endpoint))
	if body != "" {
		script += " --data-binary " + shellQuote(body)
	}
	output, err := runGuest(ctx, args, run, "bash", "-lc", script)
	if err != nil {
		return nil, fmt.Errorf("registry %s %s: %w", method, path, err)
	}
	return output, nil
}

func registryURL(registry registryRef, path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return fmt.Sprintf("http://%s%s", net.JoinHostPort(registry.ServiceIP, fmt.Sprintf("%d", registry.ServicePort)), path)
}

func scaleRegistry(ctx context.Context, args map[string]any, run providerCommandRunner, registry registryRef, replicas int) error {
	_, err := runGuestKubectl(ctx, args, run, "-n", registry.Namespace, "scale", "deploy/"+registry.Name, fmt.Sprintf("--replicas=%d", replicas))
	if err != nil {
		return fmt.Errorf("scale registry to %d: %w", replicas, err)
	}
	return nil
}

func waitForRegistryReplicas(ctx context.Context, args map[string]any, run providerCommandRunner, registry registryRef, want int) error {
	deadline := time.Now().Add(registryScaleTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		output, err := runGuestKubectl(ctx, args, run, "-n", registry.Namespace, "get", "deploy", registry.Name, "-o", "json")
		if err != nil {
			lastErr = err
		} else {
			var deploy map[string]any
			if err := json.Unmarshal(output, &deploy); err != nil {
				lastErr = err
			} else if int(nestedNumber(deploy, "status", "replicas")) == want {
				return nil
			} else {
				lastErr = fmt.Errorf("registry replicas = %v, want %d", nestedNumber(deploy, "status", "replicas"), want)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(registryPollInterval):
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timed out waiting for registry replicas=%d", want)
	}
	return lastErr
}

func waitForRegistryReady(ctx context.Context, args map[string]any, run providerCommandRunner, registry registryRef) error {
	deadline := time.Now().Add(registryReadyTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		_, err := runGuestKubectl(ctx, args, run, "-n", registry.Namespace, "wait", "--for=condition=available", "deploy/"+registry.Name, "--timeout=15s")
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(registryPollInterval):
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timed out waiting for registry Ready")
	}
	return lastErr
}

func runRegistryGarbageCollectJob(ctx context.Context, args map[string]any, run providerCommandRunner, registry registryRef) error {
	_, _ = runGuestKubectl(ctx, args, run, "-n", registry.Namespace, "delete", "job", "opute-registry-gc", "--ignore-not-found=true")
	manifest := registryGCJobManifest(registry)
	_, err := run(ctx, []string{"exec", stringInput(args, "providerInstanceName"), "--", "k3s", "kubectl", "apply", "-f", "-"}, []byte(manifest))
	if err != nil {
		return fmt.Errorf("apply registry garbage-collect job: %w", err)
	}
	deadline := time.Now().Add(registryJobTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		_, err := runGuestKubectl(ctx, args, run, "-n", registry.Namespace, "wait", "--for=condition=complete", "job/opute-registry-gc", "--timeout=15s")
		if err == nil {
			return nil
		}
		lastErr = err
		failed, _ := runGuestKubectl(ctx, args, run, "-n", registry.Namespace, "get", "job", "opute-registry-gc", "-o", "jsonpath={.status.failed}")
		if strings.TrimSpace(string(failed)) != "" && strings.TrimSpace(string(failed)) != "0" {
			logs, _ := runGuestKubectl(ctx, args, run, "-n", registry.Namespace, "logs", "job/opute-registry-gc")
			return fmt.Errorf("registry garbage-collect job failed: %s", strings.TrimSpace(string(logs)))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(registryPollInterval):
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timed out waiting for registry garbage-collect job")
	}
	return lastErr
}

func registryGCJobManifest(registry registryRef) string {
	image := firstNonEmpty(registry.Image, "registry:3")
	claim := firstNonEmpty(registry.ClaimName, registry.Name)
	configMounts := ""
	configVolume := ""
	if name := strings.TrimSpace(registry.ConfigName); name != "" {
		configMounts = `
        - name: config
          mountPath: /etc/distribution`
		configVolume = fmt.Sprintf(`
      - name: config
        configMap:
          name: %s`, name)
	}
	return fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: opute-registry-gc
  namespace: %s
spec:
  ttlSecondsAfterFinished: 300
  backoffLimit: 1
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: gc
        image: %s
        command: ["registry", "garbage-collect", "--delete-untagged", "/etc/distribution/config.yml"]
        volumeMounts:
        - name: data
          mountPath: /var/lib/registry%s
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: %s%s
`, registry.Namespace, image, configMounts, claim, configVolume)
}

func restoreReplicas(original int) int {
	if original <= 0 {
		return 1
	}
	return original
}

func firstContainerImage(deploy map[string]any) string {
	containers, _ := nestedValue(deploy, "spec", "template", "spec", "containers").([]any)
	if len(containers) == 0 {
		return ""
	}
	container, _ := containers[0].(map[string]any)
	image, _ := container["image"].(string)
	return strings.TrimSpace(image)
}

func firstPVCName(deploy map[string]any) string {
	volumes, _ := nestedValue(deploy, "spec", "template", "spec", "volumes").([]any)
	for _, raw := range volumes {
		volume, _ := raw.(map[string]any)
		claim, _ := volume["persistentVolumeClaim"].(map[string]any)
		if claim == nil {
			continue
		}
		name, _ := claim["claimName"].(string)
		if strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

func firstConfigMapName(deploy map[string]any) string {
	volumes, _ := nestedValue(deploy, "spec", "template", "spec", "volumes").([]any)
	for _, raw := range volumes {
		volume, _ := raw.(map[string]any)
		config, _ := volume["configMap"].(map[string]any)
		if config == nil {
			continue
		}
		name, _ := config["name"].(string)
		if strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

func registryServicePort(svc map[string]any) int {
	ports, _ := nestedValue(svc, "spec", "ports").([]any)
	if len(ports) == 0 {
		return 5000
	}
	port, _ := ports[0].(map[string]any)
	if value := nestedNumber(port, "port"); value > 0 {
		return int(value)
	}
	return 5000
}

func nestedValue(object map[string]any, path ...string) any {
	var current any = object
	for _, part := range path {
		value, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = value[part]
	}
	return current
}

func mapString(object map[string]any, path ...string) string {
	text, _ := nestedString(object, path...)
	return strings.TrimSpace(text)
}

func registryTagsAsMaps(tags []registryTag) []map[string]any {
	out := make([]map[string]any, 0, len(tags))
	for _, tag := range tags {
		out = append(out, map[string]any{"repository": tag.Repository, "tag": tag.Tag})
	}
	return out
}
