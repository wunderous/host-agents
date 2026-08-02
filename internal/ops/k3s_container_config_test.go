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

func TestContainerK3sInstallArgsAddsV131KubeletFeatureGate(t *testing.T) {
	got := containerK3sInstallArgs([]string{"server", "--write-kubeconfig-mode=644"})
	if got[len(got)-1] != containerK3sKubeletInstallArg {
		t.Fatalf("got %#v, want feature-gate arg appended", got)
	}
	if len(got) != 3 {
		t.Fatalf("got %#v, caller args were not preserved", got)
	}
}

func TestContainerK3sInstallArgsAddsServerCommandWhenOmitted(t *testing.T) {
	got := containerK3sInstallArgs(nil)
	if len(got) != 2 || got[0] != "server" || got[1] != containerK3sKubeletInstallArg {
		t.Fatalf("got %#v, want explicit server and feature-gate args", got)
	}
}

func TestContainerK3sInstallArgsDoesNotDuplicateFeatureGate(t *testing.T) {
	input := []string{"server", containerK3sKubeletInstallArg}
	got := containerK3sInstallArgs(input)
	if len(got) != len(input) {
		t.Fatalf("got %#v, feature-gate arg was duplicated", got)
	}
	if &got[0] == &input[0] {
		t.Fatal("helper returned caller-owned slice")
	}
}
