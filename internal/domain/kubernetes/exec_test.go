package kubernetes

import (
	"context"
	"testing"
)

type kubectlEchoExecutor struct {
	operation string
	request   KubernetesProviderRequest
}

func (e *kubectlEchoExecutor) Execute(_ context.Context, operation string, request KubernetesProviderRequest) (map[string]any, error) {
	e.operation = operation
	e.request = request
	return map[string]any{"stdout": "No resources found in cert-manager namespace.", "exitCode": 0}, nil
}

// exec_kubernetes_command was catalogued with no implementation behind it, so
// the operator health probe that depends on it could never see the cluster.
// This asserts the whole typed shape the probe relies on: the sub-command and
// its arguments reach the provider as one kubectl argument vector, and the
// result names the cluster it ran against.
func TestExecKubernetesCommandDelegatesKubectlArgumentVector(t *testing.T) {
	service := testService("tenant-a")
	if err := service.shared.RegisterResource("cluster:tenant-a:k3s", map[string]any{
		"providerInstanceName": "k3s-container",
		"instanceType":         "container",
	}); err != nil {
		t.Fatal(err)
	}
	executor := &kubectlEchoExecutor{}
	service.SetKubernetesProviderExecutor(executor)

	out, err := service.ExecKubernetesCommand(ExecKubernetesCommandArgs{
		URI:     "cluster:tenant-a:k3s",
		Command: "get",
		Args:    []string{"pods,deploy", "-n", "cert-manager", "-o", "wide"},
	})
	if err != nil {
		t.Fatalf("exec kubectl: %v", err)
	}
	if executor.operation != KubernetesExecCommandOperation {
		t.Fatalf("operation = %q", executor.operation)
	}
	kubectlArgs, _ := executor.request.Arguments["kubectlArgs"].([]any)
	want := []string{"get", "pods,deploy", "-n", "cert-manager", "-o", "wide"}
	if len(kubectlArgs) != len(want) {
		t.Fatalf("kubectlArgs = %#v", executor.request.Arguments["kubectlArgs"])
	}
	for index, value := range want {
		if kubectlArgs[index] != value {
			t.Fatalf("kubectlArgs[%d] = %#v, want %q", index, kubectlArgs[index], value)
		}
	}
	if out["clusterId"] != "k3s" || out["command"] != "get" || out["uri"] != "cluster:tenant-a:k3s" {
		t.Fatalf("result = %#v", out)
	}
	if out["stdout"] != "No resources found in cert-manager namespace." {
		t.Fatalf("stdout = %#v", out["stdout"])
	}

	if _, err := service.ExecKubernetesCommand(ExecKubernetesCommandArgs{URI: "cluster:tenant-a:k3s"}); err == nil {
		t.Fatal("a call with no kubectl sub-command was delegated")
	}
	if _, err := service.ExecKubernetesCommand(ExecKubernetesCommandArgs{Command: "get"}); err == nil {
		t.Fatal("a call with no cluster URI was delegated")
	}
}
