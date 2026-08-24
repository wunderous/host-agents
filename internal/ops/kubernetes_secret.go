package ops

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var regexpSecretKey = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type PutK8sSecretArgs struct {
	URI       string            `json:"uri,omitempty"`
	VMName    string            `json:"vmName,omitempty"` // compatibility input; never an execution target
	Namespace string            `json:"namespace,omitempty"`
	Name      string            `json:"name"`
	Data      map[string]string `json:"data"`
}

func (s *HostOperationsService) PutK8sSecret(args PutK8sSecretArgs, _ func(string)) (map[string]any, error) {
	if s.kubernetesExecutor == nil {
		return nil, errors.New("Kubernetes provider is required for Kubernetes secrets")
	}
	if strings.TrimSpace(args.URI) == "" || strings.TrimSpace(args.Name) == "" {
		return nil, errors.New("uri and name are required")
	}
	namespace := defaultString(args.Namespace, "default")
	if err := validateK8sIdentifier(namespace, "namespace"); err != nil {
		return nil, err
	}
	if err := validateK8sIdentifier(args.Name, "name"); err != nil {
		return nil, err
	}
	if len(args.Data) == 0 {
		return nil, errors.New("data is required")
	}
	for key := range args.Data {
		if !regexpSecretKey.MatchString(key) {
			return nil, fmt.Errorf("secret key %q is invalid", key)
		}
	}
	out, delegated, err := s.executeKubernetesProvider(KubernetesPutSecretOperation, args.URI, map[string]any{
		"namespace": namespace, "name": strings.TrimSpace(args.Name), "data": args.Data,
	})
	if !delegated {
		return nil, errors.New("Kubernetes provider is required for Kubernetes secrets")
	}
	return out, err
}
