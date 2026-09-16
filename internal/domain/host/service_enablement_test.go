package host

import (
	"errors"
	"strings"
	"testing"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

// unitListingService answers list-units and list-unit-files separately, which is
// the whole point: they are two different systemd commands answering two
// different questions, and ListHostServices used to ask only the first while
// reporting an answer that only the second could give.
func unitListingService(t *testing.T, units, unitFiles string, unitFilesErr error) (*Service, *[][]string) {
	t.Helper()
	ran := &[][]string{}
	shared := hostruntime.Shared{HostCommandRunnerFn: func(command []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
		*ran = append(*ran, command)
		joined := strings.Join(command, " ")
		switch {
		case strings.Contains(joined, "list-unit-files"):
			if unitFilesErr != nil {
				return hostexec.Result{ExitCode: 1}, unitFilesErr
			}
			return hostexec.Result{ExitCode: 0, Stdout: unitFiles}, nil
		case strings.Contains(joined, "list-units"):
			return hostexec.Result{ExitCode: 0, Stdout: units}, nil
		}
		t.Fatalf("unexpected command %v", command)
		return hostexec.Result{}, nil
	}}
	return testService(shared), ran
}

func serviceRow(t *testing.T, listing map[string]any, name string) map[string]any {
	t.Helper()
	rows, ok := listing["services"].([]map[string]any)
	if !ok {
		t.Fatalf("services: %#v", listing["services"])
	}
	for _, row := range rows {
		if row["serviceName"] == name {
			return row
		}
	}
	t.Fatalf("no row for %q in %#v", name, rows)
	return nil
}

const sampleUnits = `opute-host-agent.service loaded active running Opute Host Agent
cron.service loaded active running Regular background jobs
dbus.service loaded active running D-Bus
getty@tty1.service loaded inactive dead Getty on tty1
`

func TestListHostServicesReportsRealEnablement(t *testing.T) {
	service, ran := unitListingService(t, sampleUnits, `opute-host-agent.service enabled enabled
cron.service disabled enabled
dbus.service static -
getty@tty1.service masked masked
`, nil)

	listing, err := service.ListHostServices("system")
	if err != nil {
		t.Fatal(err)
	}
	if listing["total"] != 4 {
		t.Fatalf("total: %#v", listing["total"])
	}

	for _, want := range []struct {
		name    string
		enabled bool
		state   string
	}{
		{"opute-host-agent", true, "enabled"},
		{"cron", false, "disabled"},
		{"dbus", false, "static"},
		{"getty@tty1", false, "masked"},
	} {
		row := serviceRow(t, listing, want.name)
		if row["enabled"] != want.enabled || row["enablementState"] != want.state {
			t.Fatalf("%s: enabled=%#v state=%#v, want %v/%s", want.name, row["enabled"], row["enablementState"], want.enabled, want.state)
		}
	}

	// A disabled unit that is nonetheless running must still read as active:
	// the two fields answer different questions and neither may be derived
	// from the other.
	if serviceRow(t, listing, "cron")["active"] != true {
		t.Fatal("a disabled unit that is running is still active")
	}

	// Scope belongs to both commands. Asking list-unit-files without --user
	// while list-units had it would answer about a different manager entirely.
	for _, command := range *ran {
		if strings.Contains(strings.Join(command, " "), "--user") {
			t.Fatalf("system scope ran a user-manager command: %v", command)
		}
	}
}

func TestListHostServicesEnablementFollowsUserScope(t *testing.T) {
	service, ran := unitListingService(t, sampleUnits, "opute-host-agent.service enabled-runtime enabled\n", nil)

	listing, err := service.ListHostServices("")
	if err != nil {
		t.Fatal(err)
	}
	// enabled-runtime is a real enablement: the unit starts at boot until the
	// next reboot. Reporting it as disabled would be wrong in the other
	// direction from the bug this replaced.
	row := serviceRow(t, listing, "opute-host-agent")
	if row["enabled"] != true || row["enablementState"] != "enabled-runtime" {
		t.Fatalf("row: %#v", row)
	}

	var sawUnitFiles bool
	for _, command := range *ran {
		if strings.Contains(strings.Join(command, " "), "list-unit-files") {
			sawUnitFiles = true
			if !strings.Contains(strings.Join(command, " "), "--user") {
				t.Fatalf("user scope asked the system manager: %v", command)
			}
		}
	}
	if !sawUnitFiles {
		t.Fatal("enablement was never asked of systemd")
	}
}

// The listing is useful without enablement, so a failure to read it must not
// fail the call -- but it must not be silently reported as enabled either,
// which is exactly what the old hardcoded `true` did for every unit.
func TestListHostServicesSurvivesUnreadableEnablement(t *testing.T) {
	service, _ := unitListingService(t, sampleUnits, "", errors.New("systemctl: command not found"))

	listing, err := service.ListHostServices("system")
	if err != nil {
		t.Fatal(err)
	}
	if listing["total"] != 4 {
		t.Fatalf("total: %#v", listing["total"])
	}
	for _, name := range []string{"opute-host-agent", "cron", "dbus", "getty@tty1"} {
		row := serviceRow(t, listing, name)
		if row["enabled"] != false || row["enablementState"] != "unknown" {
			t.Fatalf("%s: %#v", name, row)
		}
	}
}

// A unit that list-units reports but list-unit-files does not is the ordinary
// case for a transient or generated unit. It has no enablement to report, and
// guessing one either way would be a claim systemd never made.
func TestListHostServicesMarksUnknownWhenUnitIsAbsentFromUnitFiles(t *testing.T) {
	service, _ := unitListingService(t, sampleUnits, "opute-host-agent.service enabled enabled\n", nil)

	listing, err := service.ListHostServices("system")
	if err != nil {
		t.Fatal(err)
	}
	row := serviceRow(t, listing, "cron")
	if row["enabled"] != false || row["enablementState"] != "unknown" {
		t.Fatalf("row: %#v", row)
	}
}
