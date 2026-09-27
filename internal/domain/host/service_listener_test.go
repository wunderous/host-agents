package host

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

func TestInspectHostServiceRequiresListenerOwnedByItsProcess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	mainPID := os.Getpid()
	shared := hostruntime.Shared{HostCommandRunnerFn: func(command []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
		switch {
		case strings.Contains(strings.Join(command, " "), "is-active"):
			return hostexec.Result{ExitCode: 0, Stdout: "active\n"}, nil
		case strings.Contains(strings.Join(command, " "), "is-enabled"):
			return hostexec.Result{ExitCode: 0, Stdout: "enabled\n"}, nil
		case strings.Contains(strings.Join(command, " "), "show"):
			return hostexec.Result{ExitCode: 0, Stdout: fmt.Sprintf("FragmentPath=/tmp/provider.service\nExecStart={ path=/tmp/provider ; argv[]=/tmp/provider serve ; }\nMainPID=%d\n", mainPID)}, nil
		}
		return hostexec.Result{ExitCode: 1}, nil
	}}
	service := testService(shared)
	args := InspectHostServiceArgs{ServiceName: "provider.service", Scope: "user", ListenPort: port}
	owned, err := service.InspectHostService(args, nil)
	if err != nil || owned["listenerOwned"] != true {
		t.Fatalf("owning service listener = %#v, err=%v", owned, err)
	}
	mainPID = os.Getpid() + 1000000
	other, err := service.InspectHostService(args, nil)
	if err != nil || other["active"] != true || other["listenerOwned"] != false {
		t.Fatalf("another process's listener accepted = %#v, err=%v", other, err)
	}
}
