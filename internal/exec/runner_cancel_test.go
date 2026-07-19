//go:build unix

package exec

import (
	"context"
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
