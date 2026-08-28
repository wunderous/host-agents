package oci

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
)

type EnsureOciBuilderArgs struct {
	Builder string `json:"builder,omitempty"`
}

// EnsureOciBuilder makes the host-side OCI image builder available. Podman is
// the only runtime adapter currently implemented. Buildah and BuildKit remain
// accepted as legacy build-only values for existing callers.
func (s *Service) EnsureOciBuilder(args EnsureOciBuilderArgs, onData func(string)) (map[string]any, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("ensure_oci_builder is unsupported on %s host agents", runtime.GOOS)
	}
	builder := strings.ToLower(strings.TrimSpace(args.Builder))
	if builder == "" || builder == "auto" {
		if path, err := s.shared.ContainerLookPath("podman"); err == nil {
			adapter := &podmanRuntimeAdapter{service: s, path: path}
			if readyErr := adapter.Ready(context.Background()); readyErr == nil {
				return ociBuilderResult("podman", path, true), nil
			}
		}
		for _, candidate := range []string{"buildah", "buildkitd"} {
			if path, err := s.shared.ContainerLookPath(candidate); err == nil {
				if candidate == "buildkitd" {
					return ociBuilderResult("buildkit", path, true), nil
				}
				return ociBuilderResult(candidate, path, true), nil
			}
		}
		builder = "podman"
	}
	if !ociBuilderNames[builder] {
		return nil, errors.New("builder must be one of auto, podman, buildah, or buildkit")
	}
	if builder == "podman" {
		if path, err := s.shared.ContainerLookPath("podman"); err == nil {
			adapter := &podmanRuntimeAdapter{service: s, path: path}
			if readyErr := adapter.Ready(context.Background()); readyErr != nil {
				return nil, readyErr
			}
			return ociBuilderResult("podman", path, true), nil
		}
	}
	commandName := builder
	if builder == "buildkit" {
		commandName = "buildkitd"
	}
	if path, err := s.shared.ContainerLookPath(commandName); err == nil {
		return ociBuilderResult(builder, path, true), nil
	}
	packageName := builder
	if builder == "buildkit" {
		packageName = "moby-buildkit"
	}
	if _, err := s.shared.ContainerLookPath("apt-get"); err != nil {
		return nil, fmt.Errorf("%s is not installed and apt-get is unavailable; install package %q through the host OS package manager", builder, packageName)
	}
	if onData != nil {
		onData(fmt.Sprintf("Installing host OCI builder package %s...", packageName))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := hostexec.RunPrivilegedPackage(ctx, "apt-get", "update"); err != nil {
		return nil, fmt.Errorf("update apt package indexes: %w", err)
	}
	if err := hostexec.RunPrivilegedPackage(ctx, "apt-get", "install", "-y", packageName); err != nil {
		return nil, fmt.Errorf("install OCI builder %s: %w", builder, err)
	}
	path, err := s.shared.ContainerLookPath(commandName)
	if err != nil {
		return nil, fmt.Errorf("OCI builder %s was installed but %s is still unavailable: %w", builder, commandName, err)
	}
	return ociBuilderResult(builder, path, false), nil
}

var ociBuilderNames = map[string]bool{"podman": true, "buildah": true, "buildkit": true}

func ociBuilderResult(builder, path string, alreadyAvailable bool) map[string]any {
	return map[string]any{"builder": builder, "runtime": builder, "path": path, "available": true, "alreadyAvailable": alreadyAvailable}
}
