package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/resourceid"
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

// ExecKubernetesCommandArgs is the typed input for the public
// exec_kubernetes_command capability.
type ExecKubernetesCommandArgs struct {
	URI     string   `json:"uri,omitempty"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Stdin   string   `json:"stdin,omitempty"`
}

// ExecKubernetesCommand runs one kubectl sub-command against a bound cluster.
//
// The capability was published in the tool catalog with no dispatch handler
// behind it, so every call returned "tool not found" while tools/list kept
// advertising it. The operator health probe read that failure as evidence and
// reported a running cert-manager as "not installed"; nothing else surfaced
// the gap, because a phantom name only fails when something calls it.
//
// Execution stays with the Kubernetes provider: the Host Agent owns the URI
// and the argument contract, the provider owns the kubeconfig and the guest.
func (s *Service) ExecKubernetesCommand(args ExecKubernetesCommandArgs) (map[string]any, error) {
	if s.executor == nil {
		return nil, errors.New("Kubernetes provider is required for kubectl execution")
	}
	uri := strings.TrimSpace(args.URI)
	if uri == "" {
		return nil, errors.New("uri is required")
	}
	command := strings.TrimSpace(args.Command)
	if command == "" {
		return nil, errors.New("command is required")
	}
	kubectlArgs := make([]string, 0, len(args.Args)+1)
	kubectlArgs = append(kubectlArgs, command)
	for _, value := range args.Args {
		kubectlArgs = append(kubectlArgs, value)
	}
	payload := map[string]any{"kubectlArgs": stringsToAny(kubectlArgs)}
	if args.Stdin != "" {
		payload["stdin"] = args.Stdin
	}
	out, delegated, err := s.ExecuteProvider(KubernetesExecCommandOperation, uri, payload)
	if !delegated {
		return nil, errors.New("Kubernetes provider is required for kubectl execution")
	}
	if err != nil {
		return nil, err
	}
	stdout, _ := out["stdout"].(string)
	result := map[string]any{
		"clusterId": clusterIDFromURI(uri),
		"command":   command,
		"stdout":    stdout,
		"uri":       uri,
	}
	if exitCode, ok := out["exitCode"]; ok {
		result["exitCode"] = exitCode
	}
	if stderr, ok := out["stderr"].(string); ok && stderr != "" {
		result["stderr"] = stderr
	}
	return result, nil
}

// clusterIDFromURI reports the cluster's own identifier for a canonical URI.
// The output contract names the cluster, not the URI it was reached through.
func clusterIDFromURI(uri string) string {
	parsed, err := resourceid.Parse(uri)
	if err != nil {
		return uri
	}
	return parsed.ResourceID
}
