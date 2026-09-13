package host

import (
	"reflect"
	"testing"

	"github.com/wunderous/host-agents/internal/hostruntime"

	hostexec "github.com/wunderous/host-agents/internal/exec"
	"strings"
	"time"
)

func TestRestartHostServiceCommandUsesUserManagerForOputeUnits(t *testing.T) {
	want := []string{hostruntime.DefaultSystemctlPath, "--user", "--no-block", "restart", "opute-host-agent.service"}
	if got := restartServiceCommand("opute-host-agent.service"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if got := serviceStatusCommand("opute-host-agent.service"); !reflect.DeepEqual(got, []string{hostruntime.DefaultSystemctlPath, "--user", "is-active", "opute-host-agent.service"}) {
		t.Fatalf("status command got %#v", got)
	}
}

func TestRestartHostServiceCommandKeepsSystemScopeForOtherUnits(t *testing.T) {
	if got := restartServiceCommand("ssh.service"); !reflect.DeepEqual(got, []string{hostruntime.DefaultSystemctlPath, "restart", "ssh.service"}) {
		t.Fatalf("got %#v", got)
	}
}

func TestRestartHostServiceRejectsUnsafeUnitNames(t *testing.T) {
	if safeSystemdUnitName.MatchString("opute-host-agent.service;touch /tmp/pwned") {
		t.Fatal("unsafe systemd unit name matched")
	}
}

func TestServiceStateRestartUsesUserManagerForNonAgentUnits(t *testing.T) {
	want := []string{hostruntime.DefaultSystemctlPath, "--user", "restart", "bootstrap.service"}
	if got := serviceStateCommand("bootstrap.service", "restart", "user"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestServiceStateRestartWaitsForNonAgentUserUnit(t *testing.T) {
	want := []string{hostruntime.DefaultSystemctlPath, "--user", "restart", "opute-harness-dsh.service"}
	if got := serviceStateCommand("opute-harness-dsh.service", "restart", "user"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

// ensure_host_service_supervisor is a mutating node, and the plan contract will
// not accept one without a read-only readiness check. Reporting the same state
// without changing it is what makes the supervisor step expressible in a
// recipe at all -- under system scope there is no file to inspect instead.
func TestInspectHostServiceSupervisorObservesWithoutChanging(t *testing.T) {
	var ran [][]string
	shared := hostruntime.Shared{HostCommandRunnerFn: func(command []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
		ran = append(ran, command)
		switch {
		case strings.Contains(strings.Join(command, " "), "is-system-running"):
			return hostexec.Result{ExitCode: 0, Stdout: "running\n"}, nil
		case strings.Contains(strings.Join(command, " "), "show-user"):
			return hostexec.Result{ExitCode: 0, Stdout: "Linger=yes\n"}, nil
		case strings.Contains(strings.Join(command, " "), "show-environment"):
			return hostexec.Result{ExitCode: 0, Stdout: "LANG=C\n"}, nil
		}
		return hostexec.Result{ExitCode: 1}, nil
	}}
	service := testService(shared)

	system, err := service.InspectHostServiceSupervisor(EnsureHostServiceSupervisorArgs{Scope: "system"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if system["status"] != "ready" || system["persistent"] != true || system["state"] != "running" {
		t.Fatalf("system supervisor: %#v", system)
	}

	t.Setenv("USER", "someone")
	user, err := service.InspectHostServiceSupervisor(EnsureHostServiceSupervisorArgs{Scope: ""}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if user["scope"] != "user" || user["linger"] != true || user["userBus"] != true || user["persistent"] != true {
		t.Fatalf("user supervisor: %#v", user)
	}

	// Nothing it ran may change the host: enable-linger is the mutation this
	// read exists to avoid.
	for _, command := range ran {
		if strings.Contains(strings.Join(command, " "), "enable-linger") {
			t.Fatalf("read-only inspection ran %v", command)
		}
	}

	if _, err := service.InspectHostServiceSupervisor(EnsureHostServiceSupervisorArgs{Scope: "machine"}, nil); err == nil {
		t.Fatal("unknown scope must be refused")
	}
}
