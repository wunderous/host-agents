package exec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Result captures process exit status and captured output.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// CommandRunner runs Incus and host CLI commands.
type CommandRunner func(args []string, onData func(string), timeout time.Duration) (Result, error)

// HostCommandRunner runs commands on the host OS (bash, systemctl, etc.).
type HostCommandRunner func(command []string, onData func(string), timeout time.Duration) (Result, error)

// RunCommand executes argv[0] with remaining args, optionally streaming stdout and enforcing a timeout.
func RunCommand(argv []string, onData func(string), timeout time.Duration) (Result, error) {
	return RunCommandContext(context.Background(), argv, onData, timeout)
}

// RunCommandContext is like RunCommand but honors caller cancellation. On Unix,
// children are started in a new process group so abort kills the full tree
// (e.g. `bash -lc 'curl | sh'`), not only the direct child.
func RunCommandContext(ctx context.Context, argv []string, onData func(string), timeout time.Duration) (Result, error) {
	if len(argv) == 0 {
		return Result{ExitCode: 1, Stderr: "empty command"}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	configureProcessGroup(cmd)

	var stdoutBuf, stderrBuf bytes.Buffer
	var stdoutWriters []io.Writer
	stdoutWriters = append(stdoutWriters, &stdoutBuf)
	if onData != nil {
		stdoutWriters = append(stdoutWriters, writerFunc(func(p []byte) (int, error) {
			onData(string(p))
			return len(p), nil
		}))
	}
	cmd.Stdout = io.MultiWriter(stdoutWriters...)
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return Result{ExitCode: 1, Stderr: err.Error()}, nil
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return finalizeCommandResult(ctx, timeout, stdoutBuf.String(), stderrBuf.String(), err)
	case <-ctx.Done():
		killProcessGroup(cmd)
		err := <-done
		res, _ := finalizeCommandResult(ctx, timeout, stdoutBuf.String(), stderrBuf.String(), err)
		if ctx.Err() == context.Canceled && res.ExitCode == 0 {
			res.ExitCode = 130
			if res.Stderr != "" {
				res.Stderr += "\n"
			}
			res.Stderr += "Error: Command cancelled"
		}
		return res, nil
	}
}

func finalizeCommandResult(ctx context.Context, timeout time.Duration, stdout, stderr string, err error) (Result, error) {
	res := Result{
		Stdout: stdout,
		Stderr: stderr,
	}

	if ctx.Err() == context.DeadlineExceeded {
		res.ExitCode = 124
		if res.Stderr != "" {
			res.Stderr += "\n"
		}
		res.Stderr += fmt.Sprintf("Error: Command timed out after %s", timeout)
		return res, nil
	}

	if err != nil {
		var exitErr *exec.ExitError
		if ok := asExitError(err, &exitErr); ok {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		res.ExitCode = 1
		if res.Stderr == "" {
			res.Stderr = err.Error()
		}
		return res, nil
	}

	res.ExitCode = 0
	return res, nil
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func asExitError(err error, target **exec.ExitError) bool {
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}
	*target = exitErr
	return true
}

// StreamMux fans out stdout chunks to multiple callbacks concurrently.
type StreamMux struct {
	mu        sync.Mutex
	callbacks []func(string)
}

func (m *StreamMux) Add(fn func(string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacks = append(m.callbacks, fn)
}

func (m *StreamMux) OnData(data string) {
	m.mu.Lock()
	cbs := append([]func(string){}, m.callbacks...)
	m.mu.Unlock()
	for _, cb := range cbs {
		cb(data)
	}
}
