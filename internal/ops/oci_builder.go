
package ops

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type EnsureOciBuilderArgs struct {
	Builder string `json:"builder,omitempty"`
}

// EnsureOciBuilder makes the host-side OCI image builder available. This is
// deliberately host-scoped: the application and registry remain separate
// concerns, and the host agent does not know which application will be built.
func (s *HostOperationsService) EnsureOciBuilder(args EnsureOciBuilderArgs, onData func(string)) (map[string]any, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("ensure_oci_builder is unsupported on %s host agents", runtime.GOOS)
	}
	builder := strings.ToLower(strings.TrimSpace(args.Builder))
	if builder == "" || builder == "auto" {
		for _, candidate := range []string{"podman", "buildah", "buildkitd"} {
			if path, err := exec.LookPath(candidate); err == nil {
				return ociBuilderResult(candidate, path, true), nil
			}
		}
		builder = "podman"
	}
	if !ociBuilderNames[builder] {
		return nil, errors.New("builder must be one of auto, podman, buildah, or buildkit")
	}
	commandName := builder
	if builder == "buildkit" {
		commandName = "buildkitd"
	}
	if path, err := exec.LookPath(commandName); err == nil {
		return ociBuilderResult(builder, path, true), nil
	}

	packageName := builder
	if builder == "buildkit" {
		packageName = "moby-buildkit"
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return nil, fmt.Errorf("%s is not installed and apt-get is unavailable; install package %q through the host OS package manager", builder, packageName)
	}
	if onData != nil {
		onData(fmt.Sprintf("Installing host OCI builder package %s...", packageName))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := runPrivilegedPackageCommand(ctx, "apt-get", "update"); err != nil {
		return nil, fmt.Errorf("update apt package indexes: %w", err)
	}
	if err := runPrivilegedPackageCommand(ctx, "apt-get", "install", "-y", packageName); err != nil {
		return nil, fmt.Errorf("install OCI builder %s: %w", builder, err)
	}
	path, err := exec.LookPath(commandName)
	if err != nil {
		return nil, fmt.Errorf("OCI builder %s was installed but %s is still unavailable: %w", builder, commandName, err)
	}
	return ociBuilderResult(builder, path, false), nil
}

var ociBuilderNames = map[string]bool{"podman": true, "buildah": true, "buildkit": true}

func ociBuilderResult(builder, path string, alreadyAvailable bool) map[string]any {
	return map[string]any{"builder": builder, "path": path, "available": true, "alreadyAvailable": alreadyAvailable}
}

func runPrivilegedPackageCommand(ctx context.Context, command string, args ...string) error {
	argv := append([]string{"-n", command}, args...)
	command = "sudo"
	cmd := exec.CommandContext(ctx, command, argv...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}
