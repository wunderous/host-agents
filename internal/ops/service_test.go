package ops

import (
	"reflect"
	"testing"

	"github.com/wunderous/host-agents/internal/provider"
)

func TestRestartHostServiceCommandUsesUserManagerForOputeUnits(t *testing.T) {
	want := []string{provider.DefaultSystemctlPath, "--user", "--no-block", "restart", "opute-host-agent.service"}
	if got := restartServiceCommand("opute-host-agent.service"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if got := serviceStatusCommand("opute-host-agent.service"); !reflect.DeepEqual(got, []string{provider.DefaultSystemctlPath, "--user", "is-active", "opute-host-agent.service"}) {
		t.Fatalf("status command got %#v", got)
	}
}

func TestRestartHostServiceCommandKeepsSystemScopeForOtherUnits(t *testing.T) {
	if got := restartServiceCommand("ssh.service"); !reflect.DeepEqual(got, []string{provider.DefaultSystemctlPath, "restart", "ssh.service"}) {
		t.Fatalf("got %#v", got)
	}
}

func TestRestartHostServiceRejectsUnsafeUnitNames(t *testing.T) {
	if safeSystemdUnitName.MatchString("opute-host-agent.service;touch /tmp/pwned") {
		t.Fatal("unsafe systemd unit name matched")
	}
}
