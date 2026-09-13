package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/textutil"
)

// UninstallIncusStackArgs is the inverse of InstallIncusStackArgs. The product
// could bring a host from bare to virtualization-capable but not back, which
// left "return this host to a clean slate" with no typed answer and therefore
// no answer at all -- the alternative is a shell session, which is what typed
// capabilities exist to remove.
//
// It is fail-closed by construction: it refuses while any Incus instance still
// exists, because deleting guests belongs to delete_vm and a caller who has not
// done that is asking for something other than what they said.
type UninstallIncusStackArgs struct {
	Confirm bool `json:"confirm"`
	// RemoveState opts in to discarding /var/lib/incus -- images, storage pools
	// and the server certificate. Off by default: purging packages is
	// reversible by reinstalling, discarding state is not.
	RemoveState bool `json:"removeState,omitempty"`
	// KeepRepository leaves the Zabbly apt source and keyring in place, for a
	// host that is going to reinstall Incus shortly.
	KeepRepository bool `json:"keepRepository,omitempty"`
}

// incusInstalled is a seam rather than a direct call so both sides of the
// "is there anything to remove" branch are reachable in a test; on a developer
// machine one of them otherwise always is not.
var incusInstalled = func() bool { return commandAvailable("incus") }

func (s *Service) UninstallIncusStack(args UninstallIncusStackArgs, onData func(string)) (map[string]any, error) {
	if err := s.shared.RequireSharedHostOwner("uninstall_incus_stack"); err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("uninstall_incus_stack is unsupported on %s host agents", runtime.GOOS)
	}
	if !args.Confirm {
		return nil, errors.New("uninstall_incus_stack requires confirm=true; it purges the virtualization stack from this host")
	}

	result := map[string]any{"removeState": args.RemoveState, "keptRepository": args.KeepRepository}

	if !incusInstalled() {
		result["alreadyAbsent"] = true
		result["purgedPackages"] = []string{}
		return result, nil
	}
	remaining, err := s.remainingIncusInstances(onData)
	if err != nil {
		return nil, err
	}
	if len(remaining) > 0 {
		return nil, fmt.Errorf("refusing to uninstall Incus while %d instance(s) remain (%s); delete them first", len(remaining), strings.Join(remaining, ", "))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	if onData != nil {
		onData("Stopping Incus services...")
	}
	stopped := []string{}
	for _, unit := range []string{"incus.service", "incus.socket", "incus-user.service", "incus-user.socket", "incus-startup.service"} {
		if err := hostexec.RunPrivilegedPackage(ctx, "systemctl", "disable", "--now", unit); err == nil {
			stopped = append(stopped, unit)
		}
	}
	result["stoppedUnits"] = stopped

	packages := installedIncusPackages()
	if len(packages) > 0 {
		if _, err := exec.LookPath("apt-get"); err != nil {
			return nil, errors.New("apt-get is required to purge Incus packages")
		}
		if onData != nil {
			onData(fmt.Sprintf("Purging %d Incus package(s)...", len(packages)))
		}
		purge := append([]string{"apt-get", "purge", "-y"}, packages...)
		if err := hostexec.RunPrivilegedPackage(ctx, purge[0], purge[1:]...); err != nil {
			return nil, fmt.Errorf("purge Incus packages: %w", err)
		}
		if err := hostexec.RunPrivilegedPackage(ctx, "apt-get", "autoremove", "-y", "--purge"); err != nil {
			return nil, fmt.Errorf("autoremove Incus dependencies: %w", err)
		}
	}
	result["purgedPackages"] = packages

	if !args.KeepRepository {
		for _, path := range []string{"/etc/apt/sources.list.d/zabbly-incus.sources", "/etc/apt/keyrings/zabbly.asc"} {
			if err := hostexec.RunPrivilegedPackage(ctx, "rm", "-f", path); err != nil {
				return nil, fmt.Errorf("remove %s: %w", path, err)
			}
		}
		if err := hostexec.RunPrivilegedPackage(ctx, "apt-get", "update"); err != nil {
			return nil, fmt.Errorf("refresh package indexes after removing the Incus repository: %w", err)
		}
	}

	if args.RemoveState {
		if onData != nil {
			onData("Discarding /var/lib/incus...")
		}
		if err := hostexec.RunPrivilegedPackage(ctx, "rm", "-rf", "/var/lib/incus"); err != nil {
			return nil, fmt.Errorf("remove Incus state directory: %w", err)
		}
	}

	result["incusPresent"] = incusInstalled()
	return result, nil
}

// remainingIncusInstances asks Incus itself rather than the agent's own view,
// because the refusal has to hold against instances this agent never created.
func (s *Service) remainingIncusInstances(onData func(string)) ([]string, error) {
	listed, err := s.shared.CommandRunner([]string{"list", "--format", "json"}, onData, 60*time.Second)
	if err != nil || listed.ExitCode != 0 {
		// A daemon that is already gone has no instances to protect.
		if err != nil && !incusInstalled() {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect remaining Incus instances: %s", strings.TrimSpace(textutil.FirstNonEmpty(listed.Stderr, listed.Stdout, textutil.ErrString(err, "incus list failed"))))
	}
	var instances []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(listed.Stdout)), &instances); err != nil {
		return nil, fmt.Errorf("decode Incus instance list: %w", err)
	}
	names := make([]string, 0, len(instances))
	for _, instance := range instances {
		if name := strings.TrimSpace(instance.Name); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

func installedIncusPackages() []string {
	out, err := exec.Command("dpkg-query", "-W", "-f=${Package} ${Status}\n", "incus", "incus-base", "incus-client", "incus-agent", "incus-ui-canonical").CombinedOutput()
	if err != nil && len(out) == 0 {
		return nil
	}
	packages := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if fields[len(fields)-1] == "installed" {
			packages = append(packages, fields[0])
		}
	}
	return packages
}
