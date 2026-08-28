package kubernetes

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (s *Service) RunKubectl(vmName string, kubectlArgs []string, label string) (string, error) {
	return s.RunKubectlTimed(vmName, kubectlArgs, label, DefaultDiscoveryTimeout)
}

func (s *Service) RunKubectlTimed(vmName string, kubectlArgs []string, label string, timeout time.Duration) (string, error) {
	return s.runProviderCommand(context.Background(), vmName, kubectlArgs, nil, label, timeout)
}

func (s *Service) RunKubectlContext(ctx context.Context, vmName string, kubectlArgs []string, label string, timeout time.Duration) (string, error) {
	if s.kubectlRunner != nil {
		return s.kubectlRunner(ctx, vmName, kubectlArgs, nil, label, timeout)
	}
	return s.runProviderCommand(ctx, vmName, kubectlArgs, nil, label, timeout)
}

func (s *Service) RunKubectlWithStdinContext(ctx context.Context, vmName string, kubectlArgs []string, input []byte, label string, timeout time.Duration) (string, error) {
	if s.kubectlRunner != nil {
		return s.kubectlRunner(ctx, vmName, kubectlArgs, input, label, timeout)
	}
	return s.runProviderCommand(ctx, vmName, kubectlArgs, input, label, timeout)
}

func (s *Service) runProviderCommand(ctx context.Context, vmName string, kubectlArgs []string, input []byte, label string, timeout time.Duration) (string, error) {
	if s.executor == nil {
		return "", fmt.Errorf("the Kubernetes provider is required for %s; direct Host Agent kubectl execution is disabled", label)
	}
	targetURI, err := s.TargetURI(vmName)
	if err != nil {
		return "", err
	}
	args := map[string]any{"kubectlArgs": stringsToAny(kubectlArgs)}
	if input != nil {
		args["stdin"] = string(input)
	}
	out, delegated, err := s.ExecuteProvider(KubernetesExecCommandOperation, targetURI, args)
	if !delegated {
		return "", fmt.Errorf("the Kubernetes provider is required for %s", label)
	}
	if err != nil {
		return "", fmt.Errorf("failed to %s in %s: %w", label, vmName, err)
	}
	stdout, _ := out["stdout"].(string)
	return strings.TrimSpace(stdout), nil
}

func (s *Service) EnsureHelmNamespace(vmName, namespace string) error {
	if namespace == "" || namespace == "kube-system" {
		return nil
	}
	if _, err := s.RunKubectl(vmName, []string{"get", "namespace", namespace}, "get namespace"); err == nil {
		return nil
	}
	_, err := s.RunKubectl(vmName, []string{"create", "namespace", namespace}, "create namespace")
	return err
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}
