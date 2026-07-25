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

type EnsureHostToolArgs struct {
	Tool string `json:"tool"`
}

// EnsureHostTool installs a small, explicitly allowlisted set of generic host
// build/runtime tools. Application-specific setup remains outside the agent.
func (s *HostOperationsService) EnsureHostTool(args EnsureHostToolArgs, onData func(string)) (map[string]any, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("ensure_host_tool is unsupported on %s host agents", runtime.GOOS)
	}
	tool := strings.ToLower(strings.TrimSpace(args.Tool))
	packageName, ok := hostToolPackages[tool]
	if !ok {
		return nil, errors.New("tool must be one of go, podman, buildah, buildkitd, or cloudflared")
	}
	if path, err := exec.LookPath(tool); err == nil {
		return map[string]any{"tool": tool, "path": path, "available": true, "alreadyAvailable": true}, nil
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return nil, fmt.Errorf("%s is not installed and apt-get is unavailable", tool)
	}
	if onData != nil {
		onData(fmt.Sprintf("Installing host tool package %s...", packageName))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := runPrivilegedPackageCommand(ctx, "apt-get", "update"); err != nil {
		return nil, fmt.Errorf("update apt package indexes: %w", err)
	}
	if err := runPrivilegedPackageCommand(ctx, "apt-get", "install", "-y", packageName); err != nil {
		return nil, fmt.Errorf("install host tool %s: %w", tool, err)
	}
	path, err := exec.LookPath(tool)
	if err != nil {
		return nil, fmt.Errorf("host tool %s was installed but remains unavailable: %w", tool, err)
	}
	return map[string]any{"tool": tool, "path": path, "available": true, "alreadyAvailable": false}, nil
}

var hostToolPackages = map[string]string{
	"go":          "golang-go",
	"podman":      "podman",
	"buildah":     "buildah",
	"buildkitd":   "moby-buildkit",
	"cloudflared": "cloudflared",
}
