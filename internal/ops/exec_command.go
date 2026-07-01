package ops

import (
	"fmt"
	"strings"
	"time"
)

const defaultExecCommandTimeout = 30 * time.Second

type ExecCommandArgs struct {
	VMName     string
	Command    string
	Args       []string
	TimeoutMs  int
}

func (s *HostOperationsService) ExecCommand(args ExecCommandArgs, onData func(string)) (map[string]any, error) {
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
	res, err := s.runVMExec(vmName, guestArgv, onData, timeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, fmt.Sprintf("command failed with exit %d", res.ExitCode)))
	}

	output := res.Stdout
	if output == "" {
		output = res.Stderr
	}
	return map[string]any{"output": output}, nil
}
