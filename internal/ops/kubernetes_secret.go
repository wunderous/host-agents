package ops

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var regexpSecretKey = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type PutK8sSecretArgs struct {
	VMName    string            `json:"vmName"`
	Namespace string            `json:"namespace,omitempty"`
	Name      string            `json:"name"`
	Data      map[string]string `json:"data"`
}

func (s *HostOperationsService) PutK8sSecret(args PutK8sSecretArgs, onData func(string)) (map[string]any, error) {
	namespace := defaultString(args.Namespace, "default")
	if strings.TrimSpace(args.VMName) == "" || strings.TrimSpace(args.Name) == "" {
		return nil, errors.New("vmName and name are required")
	}
	if err := validateK8sIdentifier(namespace, "namespace"); err != nil {
		return nil, err
	}
	if err := validateK8sIdentifier(args.Name, "name"); err != nil {
		return nil, err
	}
	if len(args.Data) == 0 {
		return nil, errors.New("data is required")
	}
	// Secret operations are also used as application bootstrap primitives. Make
	// them safe when the caller has not yet applied the application's Namespace
	// manifest; this keeps the generic MCP operation independently usable and
	// avoids an ordering race between namespace and credential creation.
	if _, err := s.runKubernetesKubectl(args.VMName, []string{"get", "namespace", namespace}, "check Kubernetes namespace"); err != nil {
		if _, createErr := s.runKubernetesKubectl(args.VMName, []string{"create", "namespace", namespace}, "create Kubernetes namespace"); createErr != nil {
			return nil, createErr
		}
	}

	// Merge with any existing Secret so callers can patch keys without wiping
	// unrelated bootstrap credentials (cpcToken, mcpAuthToken, …).
	merged := map[string]string{}
	if raw, err := s.runKubernetesKubectl(args.VMName, []string{"get", "secret", args.Name, "-n", namespace, "-o", "json"}, "read existing Kubernetes Secret"); err == nil {
		var object map[string]any
		if jsonErr := json.Unmarshal([]byte(raw), &object); jsonErr == nil {
			if data, ok := object["data"].(map[string]any); ok {
				for key, value := range data {
					encoded, ok := value.(string)
					if !ok || encoded == "" {
						continue
					}
					decoded, decErr := base64.StdEncoding.DecodeString(encoded)
					if decErr != nil {
						continue
					}
					merged[key] = string(decoded)
				}
			}
		}
	}
	for key, value := range args.Data {
		merged[key] = value
	}

	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	command := []string{"create", "secret", "generic", args.Name, "-n", namespace}
	for _, key := range keys {
		if !regexpSecretKey.MatchString(key) {
			return nil, fmt.Errorf("secret key %q is invalid", key)
		}
		command = append(command, "--from-literal="+key+"="+merged[key])
	}
	// Delete/create avoids kubectl's last-applied annotation, which would retain
	// the secret payload in object metadata. The operation is intentionally
	// atomic at the API boundary and intended for bootstrap credentials.
	if _, err := s.runKubernetesKubectl(args.VMName, []string{"delete", "secret", args.Name, "-n", namespace, "--ignore-not-found=true"}, "replace Kubernetes Secret"); err != nil {
		return nil, err
	}
	if _, err := s.runKubernetesKubectl(args.VMName, command, "create Kubernetes Secret"); err != nil {
		return nil, err
	}
	return map[string]any{"vmName": args.VMName, "namespace": namespace, "name": args.Name, "keys": keys, "configured": true}, nil
}
