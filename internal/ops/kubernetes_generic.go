package ops

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ApplyManifestArgs describes an arbitrary Kubernetes manifest supplied by a
// downstream application. The host agent treats it as opaque application
// data; it contains no Opute-specific assumptions.
type ApplyManifestArgs struct {
	VMName   string `json:"vmName"`
	Manifest string `json:"manifest"`
}

type K8sResourceArgs struct {
	VMName       string `json:"vmName"`
	Kind         string `json:"kind"`
	ResourceKind string `json:"resourceKind,omitempty"`
	ResourceName string `json:"resourceName"`
	Namespace    string `json:"namespace,omitempty"`
}

func (s *HostOperationsService) DeleteK8sResource(args K8sResourceArgs, onData func(string)) (map[string]any, error) {
	kind := strings.TrimSpace(args.Kind)
	name := strings.TrimSpace(args.ResourceName)
	namespace := strings.TrimSpace(args.Namespace)
	if strings.TrimSpace(args.VMName) == "" || kind == "" || name == "" {
		return nil, errors.New("vmName, kind, and resourceName are required")
	}
	if err := validateK8sIdentifier(kind, "kind"); err != nil {
		return nil, err
	}
	if err := validateK8sIdentifier(name, "resourceName"); err != nil {
		return nil, err
	}
	command := []string{"delete", kind, name}
	if namespace != "" {
		if err := validateK8sIdentifier(namespace, "namespace"); err != nil {
			return nil, err
		}
		command = append(command, "-n", namespace)
	}
	command = append(command, "--ignore-not-found=true")
	if _, err := s.runKubernetesKubectl(args.VMName, command, "delete Kubernetes resource"); err != nil {
		return nil, err
	}
	return map[string]any{"vmName": args.VMName, "kind": kind, "resourceName": name, "namespace": namespace, "deleted": true}, nil
}

var k8sIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)

func validateK8sIdentifier(value, field string) error {
	value = strings.TrimSpace(value)
	if value == "" || !k8sIdentifier.MatchString(value) {
		return fmt.Errorf("%s contains invalid characters", field)
	}
	return nil
}

func (s *HostOperationsService) ApplyManifest(args ApplyManifestArgs, onData func(string)) (map[string]any, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	manifest := strings.TrimSpace(args.Manifest)
	if manifest == "" {
		return nil, errors.New("manifest is required")
	}
	// Preserve literal dollar signs through shell-oriented MCP/Incus transport.
	// Application manifests can use __OPUTE_DOLLAR__ as a transport-safe
	// spelling for Nginx/Kubernetes runtime variables.
	manifest = strings.ReplaceAll(manifest, "__OPUTE_DOLLAR__", "$")
	if len(manifest) > 4*1024*1024 {
		return nil, errors.New("manifest exceeds the 4 MiB limit")
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(manifest))
	remote := fmt.Sprintf("/tmp/opute-manifest-%d.yaml", time.Now().UnixNano())
	write := fmt.Sprintf("printf %%s %s | base64 -d > %s", shellEscape(encoded), shellEscape(remote))
	if res, err := s.runVMExec(vmName, []string{"bash", "-lc", write}, onData, defaultDiscoveryTimeout); err != nil {
		return nil, err
	} else if res.ExitCode != 0 {
		return nil, fmt.Errorf("write manifest failed: %s", firstNonEmpty(res.Stderr, res.Stdout))
	}
	defer func() { _, _ = s.runVMExec(vmName, []string{"rm", "-f", remote}, nil, 30*time.Second) }()
	if _, err := s.runKubernetesKubectlTimed(vmName, []string{"apply", "-f", remote}, "apply manifest", 2*time.Minute); err != nil {
		return nil, err
	}
	return map[string]any{"vmName": vmName, "applied": true}, nil
}

func (s *HostOperationsService) GetK8sResource(args K8sResourceArgs) (map[string]any, error) {
	vmName := strings.TrimSpace(args.VMName)
	kind := strings.TrimSpace(args.Kind)
	if kind == "" {
		kind = strings.TrimSpace(args.ResourceKind)
	}
	name := strings.TrimSpace(args.ResourceName)
	if vmName == "" || kind == "" || name == "" {
		return nil, errors.New("vmName, kind, and resourceName are required")
	}
	if err := validateK8sIdentifier(kind, "kind"); err != nil {
		return nil, err
	}
	if err := validateK8sIdentifier(name, "resourceName"); err != nil {
		return nil, err
	}
	if namespace := strings.TrimSpace(args.Namespace); namespace != "" {
		if err := validateK8sIdentifier(namespace, "namespace"); err != nil {
			return nil, err
		}
	}
	kubectl := []string{"get", kind, name, "-o", "json"}
	if strings.TrimSpace(args.Namespace) != "" {
		kubectl = append(kubectl, "-n", strings.TrimSpace(args.Namespace))
	}
	raw, err := s.runKubernetesKubectl(vmName, kubectl, "get Kubernetes resource")
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		return nil, fmt.Errorf("invalid Kubernetes resource response: %w", err)
	}
	return map[string]any{"vmName": vmName, "kind": kind, "resourceName": name, "namespace": strings.TrimSpace(args.Namespace), "resource": object, "json": raw}, nil
}

func (s *HostOperationsService) GetK8sResourceStatus(args K8sResourceArgs) (map[string]any, error) {
	resource := args
	if strings.TrimSpace(resource.Kind) == "" {
		resource.Kind = resource.ResourceKind
	}
	out, err := s.GetK8sResource(resource)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return map[string]any{"status": "missing", "message": err.Error()}, nil
		}
		return nil, err
	}
	object, _ := out["resource"].(map[string]any)
	status := "pending"
	kind := strings.ToLower(resource.Kind)
	if kind == "service" {
		status = "ready"
	} else if kind == "pod" {
		if phase, _ := objectPathString(object, "status", "phase"); phase == "Succeeded" || phase == "Running" {
			status = "ready"
		}
	} else if kind == "deployment" {
		desired := objectPathNumber(object, "spec", "replicas")
		ready := objectPathNumber(object, "status", "readyReplicas")
		if desired == 0 {
			desired = 1
		}
		if ready >= desired {
			status = "ready"
		}
	} else if kind == "persistentvolumeclaim" || kind == "pvc" {
		if phase, _ := objectPathString(object, "status", "phase"); phase == "Bound" {
			status = "ready"
		}
	}
	return map[string]any{"vmName": resource.VMName, "resourceKind": resource.Kind, "resourceName": resource.ResourceName, "namespace": resource.Namespace, "status": status, "message": "Kubernetes resource inspected", "resource": object}, nil
}

func objectPathString(object map[string]any, path ...string) (string, bool) {
	var current any = object
	for _, key := range path {
		next, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = next[key]
		if !ok {
			return "", false
		}
	}
	value, ok := current.(string)
	return value, ok
}

func objectPathNumber(object map[string]any, path ...string) float64 {
	var current any = object
	for _, key := range path {
		next, ok := current.(map[string]any)
		if !ok {
			return 0
		}
		current, ok = next[key]
		if !ok {
			return 0
		}
	}
	switch value := current.(type) {
	case float64:
		return value
	case int:
		return float64(value)
	}
	return 0
}
