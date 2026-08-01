//go:build unix

package exec

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunCommandContextCancelsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		close(started)
		_, err := RunCommandContext(ctx, []string{"bash", "-lc", "sleep 60"}, nil, 0)
		errCh <- err
	}()

	<-started
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunCommandContext returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunCommandContext did not return after cancel")
	}
}

func TestConfigureCommandEnvironmentAddsUserSystemdBus(t *testing.T) {
	cmd := exec.Command("systemctl", "--user", "daemon-reload")
	cmd.Env = []string{"PATH=/usr/bin"}
	configureCommandEnvironment(cmd)

	uid := strconv.Itoa(os.Getuid())
	if got := environmentValue(cmd.Env, "XDG_RUNTIME_DIR"); got != "/run/user/"+uid {
		t.Fatalf("XDG_RUNTIME_DIR = %q, want /run/user/%s", got, uid)
	}
	if got := environmentValue(cmd.Env, "DBUS_SESSION_BUS_ADDRESS"); got != "unix:path=/run/user/"+uid+"/bus" {
		t.Fatalf("DBUS_SESSION_BUS_ADDRESS = %q", got)
	}
}

func TestConfigureCommandEnvironmentPreservesExplicitUserSystemdBus(t *testing.T) {
	cmd := exec.Command("/usr/bin/systemctl", "--user", "is-active", "opute-platform-postgres.service")
	cmd.Env = []string{
		"XDG_RUNTIME_DIR=/custom/runtime",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/custom/runtime/bus",
	}
	configureCommandEnvironment(cmd)
	if got := strings.Join(cmd.Env, "\n"); !strings.Contains(got, "XDG_RUNTIME_DIR=/custom/runtime") || !strings.Contains(got, "DBUS_SESSION_BUS_ADDRESS=unix:path=/custom/runtime/bus") {
		t.Fatalf("explicit systemd environment was not preserved: %s", got)
	}
}
