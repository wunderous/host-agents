package kubernetes

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/wunderous/host-agents/internal/contract/k8sname"
	"github.com/wunderous/host-agents/internal/textutil"
)

var regexpSecretKey = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type PutK8sSecretArgs struct {
	URI       string            `json:"uri,omitempty"`
	VMName    string            `json:"vmName,omitempty"` // compatibility input; never an execution target
	Namespace string            `json:"namespace,omitempty"`
	Name      string            `json:"name"`
	Data      map[string]string `json:"data"`
}

func (s *Service) PutK8sSecret(args PutK8sSecretArgs, _ func(string)) (map[string]any, error) {
	if s.executor == nil {
		return nil, errors.New("Kubernetes provider is required for Kubernetes secrets")
	}
	if strings.TrimSpace(args.URI) == "" || strings.TrimSpace(args.Name) == "" {
		return nil, errors.New("uri and name are required")
	}
	namespace := textutil.Default(args.Namespace, "default")
	if err := k8sname.Validate(namespace, "namespace"); err != nil {
		return nil, err
	}
	if err := k8sname.Validate(args.Name, "name"); err != nil {
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
	out, delegated, err := s.ExecuteProvider(KubernetesPutSecretOperation, args.URI, map[string]any{
		"namespace": namespace, "name": strings.TrimSpace(args.Name), "data": args.Data,
	})
	if !delegated {
		return nil, errors.New("Kubernetes provider is required for Kubernetes secrets")
	}
	return out, err
}
