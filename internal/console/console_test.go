package console

import (
	"strings"
	"testing"
)

func TestStaleStreamControlsReturnSessionNotFound(t *testing.T) {
	runtime := NewRuntime()

	if _, err := runtime.SendConsoleInput("stale-stream", "echo"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("SendConsoleInput() error = %v, want a not-found error", err)
	}
	if _, err := runtime.ResizeConsole("stale-stream", 80, 24); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("ResizeConsole() error = %v, want a not-found error", err)
	}
}
