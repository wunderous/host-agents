package kubernetes

import (
	"errors"
	"strings"

	"github.com/wunderous/host-agents/internal/contract/k8sname"
)

// ApplyManifestArgs describes an arbitrary Kubernetes manifest supplied by a
// downstream application. Concrete control-plane execution belongs to the
// active Kubernetes provider.
type ApplyManifestArgs struct {
	URI      string `json:"uri,omitempty"`
	VMName   string `json:"vmName,omitempty"` // compatibility input; never an execution target
	Manifest string `json:"manifest"`
}

type K8sResourceArgs struct {
	URI          string `json:"uri,omitempty"`
	VMName       string `json:"vmName,omitempty"` // compatibility input; never an execution target
	Kind         string `json:"kind"`
	ResourceKind string `json:"resourceKind,omitempty"`
	ResourceName string `json:"resourceName"`
	Namespace    string `json:"namespace,omitempty"`
}

// K8sEventsArgs describes a bounded, read-only Kubernetes event query.
type K8sEventsArgs struct {
	URI       string `json:"uri,omitempty"`
	VMName    string `json:"vmName,omitempty"` // compatibility input; never an execution target
	Namespace string `json:"namespace,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

func (s *Service) DeleteK8sResource(args K8sResourceArgs, _ func(string)) (map[string]any, error) {
	if s.executor == nil {
		return nil, errors.New("Kubernetes provider is required for delete Kubernetes resource")
	}
	kind := strings.TrimSpace(args.Kind)
	name := strings.TrimSpace(args.ResourceName)
	namespace := strings.TrimSpace(args.Namespace)
	if strings.TrimSpace(args.URI) == "" || kind == "" || name == "" {
		return nil, errors.New("uri, kind, and resourceName are required")
	}
	if err := k8sname.Validate(kind, "kind"); err != nil {
		return nil, err
	}
	if err := k8sname.Validate(name, "resourceName"); err != nil {
		return nil, err
	}
	if namespace != "" {
		if err := k8sname.Validate(namespace, "namespace"); err != nil {
			return nil, err
		}
	}
	out, delegated, err := s.ExecuteProvider(KubernetesDeleteResourceOperation, args.URI, map[string]any{
		"kind": kind, "resourceName": name, "namespace": namespace,
	})
	if !delegated {
		return nil, errors.New("Kubernetes provider is required for delete Kubernetes resource")
	}
	return out, err
}

func (s *Service) ApplyManifest(args ApplyManifestArgs, _ func(string)) (map[string]any, error) {
	if s.executor == nil {
		return nil, errors.New("Kubernetes provider is required for apply manifest")
	}
	if strings.TrimSpace(args.URI) == "" {
		return nil, errors.New("uri is required")
	}
	manifest := strings.TrimSpace(args.Manifest)
	if manifest == "" {
		return nil, errors.New("manifest is required")
	}
	if len(manifest) > 4*1024*1024 {
		return nil, errors.New("manifest exceeds the 4 MiB limit")
	}
	out, delegated, err := s.ExecuteProvider(KubernetesApplyManifestOperation, args.URI, map[string]any{
		"manifest": manifest,
	})
	if !delegated {
		return nil, errors.New("Kubernetes provider is required for apply manifest")
	}
	return out, err
}

func (s *Service) GetK8sResource(args K8sResourceArgs) (map[string]any, error) {
	if s.executor == nil {
		return nil, errors.New("Kubernetes provider is required for get Kubernetes resource")
	}
	kind := strings.TrimSpace(args.Kind)
	if kind == "" {
		kind = strings.TrimSpace(args.ResourceKind)
	}
	name := strings.TrimSpace(args.ResourceName)
	namespace := strings.TrimSpace(args.Namespace)
	if strings.TrimSpace(args.URI) == "" || kind == "" || name == "" {
		return nil, errors.New("uri, kind, and resourceName are required")
	}
	if err := k8sname.Validate(kind, "kind"); err != nil {
		return nil, err
	}
	if err := k8sname.Validate(name, "resourceName"); err != nil {
		return nil, err
	}
	if namespace != "" {
		if err := k8sname.Validate(namespace, "namespace"); err != nil {
			return nil, err
		}
	}
	out, delegated, err := s.ExecuteProvider(KubernetesGetResourceOperation, args.URI, map[string]any{
		"kind": kind, "resourceKind": args.ResourceKind, "resourceName": name, "namespace": namespace,
	})
	if !delegated {
		return nil, errors.New("Kubernetes provider is required for get Kubernetes resource")
	}
	return out, err
}

func (s *Service) GetK8sResourceStatus(args K8sResourceArgs) (map[string]any, error) {
	if s.executor == nil {
		return nil, errors.New("Kubernetes provider is required for Kubernetes resource status")
	}
	resourceKind := strings.TrimSpace(args.ResourceKind)
	if resourceKind == "" {
		resourceKind = strings.TrimSpace(args.Kind)
	}
	if strings.TrimSpace(args.URI) == "" || resourceKind == "" || strings.TrimSpace(args.ResourceName) == "" {
		return nil, errors.New("uri, resourceKind, and resourceName are required")
	}
	if err := k8sname.Validate(resourceKind, "resourceKind"); err != nil {
		return nil, err
	}
	if err := k8sname.Validate(args.ResourceName, "resourceName"); err != nil {
		return nil, err
	}
	out, delegated, err := s.ExecuteProvider(KubernetesGetResourceStatusOperation, args.URI, map[string]any{
		"kind": resourceKind, "resourceKind": resourceKind, "resourceName": strings.TrimSpace(args.ResourceName), "namespace": strings.TrimSpace(args.Namespace),
	})
	if !delegated {
		return nil, errors.New("Kubernetes provider is required for Kubernetes resource status")
	}
	return out, err
}

// ListK8sEvents returns bounded provider-owned event evidence for an explicit
// canonical cluster target.
func (s *Service) ListK8sEvents(args K8sEventsArgs) (map[string]any, error) {
	if s.executor == nil {
		return nil, errors.New("Kubernetes provider is required for Kubernetes events")
	}
	if strings.TrimSpace(args.URI) == "" {
		return nil, errors.New("uri is required")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	out, delegated, err := s.ExecuteProvider(KubernetesListEventsOperation, args.URI, map[string]any{
		"namespace": strings.TrimSpace(args.Namespace), "limit": limit,
	})
	if !delegated {
		return nil, errors.New("Kubernetes provider is required for Kubernetes events")
	}
	return out, err
}
