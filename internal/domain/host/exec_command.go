package host

import (
	"fmt"
	"strings"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/internal/textutil"
)

const defaultExecCommandTimeout = 30 * time.Second

type ExecCommandArgs struct {
	VMName    string
	Command   string
	Args      []string
	TimeoutMs int
}

// RunInstanceCommandArgs is the neutral guest execution contract used by
// providers. The target is an opaque, tenant-scoped resource URI rather than
// a display name, so a provider cannot accidentally fall back from a system
// container to a VM with the same label.
type RunInstanceCommandArgs struct {
	URI     string
	Command string
	Args    []string
	// Stdin is transient input and is never included in the provider argv or
	// returned task observation. It is intended for provider-owned secret
	// enrollment flows.
	Stdin     string
	TimeoutMs int
}

func (s *Service) RunInstanceCommand(args RunInstanceCommandArgs, onData func(string)) (map[string]any, error) {
	uri, err := s.deps.ResolveResource(strings.TrimSpace(args.URI), "")
	if err != nil {
		return nil, err
	}
	if uri.ResourceType != resourceid.TypeVM && uri.ResourceType != resourceid.TypeContainer {
		return nil, fmt.Errorf("instance command requires vm or container URI, got %q", uri.ResourceType)
	}
	providerName, ok := uri.Values["providerInstanceName"].(string)
	if !ok || strings.TrimSpace(providerName) == "" {
		return nil, fmt.Errorf("resource %s has no provider instance coordinate", uri.URI)
	}
	command := strings.TrimSpace(args.Command)
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}
	timeout := defaultExecCommandTimeout
	if args.TimeoutMs > 0 {
		timeout = time.Duration(args.TimeoutMs) * time.Millisecond
	}
	argv := append([]string{command}, args.Args...)
	var res hostexec.Result
	if args.Stdin != "" {
		if s.deps.RunVMExecWithStdin == nil {
			return nil, fmt.Errorf("instance command stdin is unavailable")
		}
		res, err = s.deps.RunVMExecWithStdin(providerName, argv, []byte(args.Stdin), onData, timeout)
	} else {
		res, err = s.deps.RunVMExec(providerName, argv, onData, timeout)
	}
	if err != nil {
		return nil, err
	}
	output := res.Stdout
	if output == "" {
		output = res.Stderr
	}
	return map[string]any{
		"uri":          uri.URI.String(),
		"instanceType": uri.Values["instanceType"],
		"exitCode":     res.ExitCode,
		"stdout":       res.Stdout,
		"stderr":       res.Stderr,
		"output":       output,
	}, nil
}

func (s *Service) ExecCommand(args ExecCommandArgs, onData func(string)) (map[string]any, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, fmt.Errorf("name is required")
	}
	command := strings.TrimSpace(args.Command)
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}

	timeout := defaultExecCommandTimeout
	if args.TimeoutMs > 0 {
		timeout = time.Duration(args.TimeoutMs) * time.Millisecond
	}

	guestArgv := append([]string{command}, args.Args...)
	res, err := s.deps.RunVMExec(vmName, guestArgv, onData, timeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", textutil.FirstNonEmpty(res.Stderr, res.Stdout, fmt.Sprintf("command failed with exit %d", res.ExitCode)))
	}

	output := res.Stdout
	if output == "" {
		output = res.Stderr
	}
	return map[string]any{"output": output}, nil
}
