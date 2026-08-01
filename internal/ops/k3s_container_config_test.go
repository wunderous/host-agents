package ops

import (
	"strings"
	"testing"
)

func TestContainerK3sKubeletConfigScript(t *testing.T) {
	script := containerK3sKubeletConfigScript()
	if !strings.Contains(script, "KubeletInUserNamespace") {
		t.Fatalf("script missing KubeletInUserNamespace: %q", script)
	}
	if !strings.Contains(script, "/etc/rancher/k3s/config.yaml") {
		t.Fatalf("script missing config path: %q", script)
	}
}
