package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/wunderous/host-agents/pkg/hostagentclient"
)

type OperationPollOptions struct {
	Timeout  time.Duration
	Interval time.Duration
	OnUpdate func(hostagentclient.OperationSnapshot)
}

// WaitOperation reconnects only for durable status reads. It never replays
// the mutation that produced operationID.
func (e *Executor) WaitOperation(ctx context.Context, operationID string, options OperationPollOptions) (hostagentclient.OperationSnapshot, error) {
	if e == nil || e.Client == nil {
		return hostagentclient.OperationSnapshot{}, fmt.Errorf("host agent client is required")
	}
	if strings.TrimSpace(operationID) == "" {
		return hostagentclient.OperationSnapshot{}, fmt.Errorf("operation id is required")
	}
	if options.Timeout <= 0 {
		options.Timeout = 30 * time.Minute
	}
	if options.Interval <= 0 {
		options.Interval = 500 * time.Millisecond
	}
	deadline := time.Now().Add(options.Timeout)
	for {
		if err := ctx.Err(); err != nil {
			return hostagentclient.OperationSnapshot{}, err
		}
		if time.Now().After(deadline) {
			return hostagentclient.OperationSnapshot{}, fmt.Errorf("operation polling deadline exceeded")
		}
		result, err := e.Client.OperationStatus(ctx, operationID)
		if err != nil {
			if reconnectErr := e.Client.Reconnect(ctx); reconnectErr != nil {
				return hostagentclient.OperationSnapshot{}, fmt.Errorf("status request failed and reconnect failed: %w", reconnectErr)
			}
			result, err = e.Client.OperationStatus(ctx, operationID)
		}
		if err != nil {
			return hostagentclient.OperationSnapshot{}, err
		}
		snapshot, err := decodeOperation(result)
		if err != nil {
			return hostagentclient.OperationSnapshot{}, err
		}
		if options.OnUpdate != nil {
			options.OnUpdate(snapshot)
		}
		switch snapshot.Status {
		case "completed", "succeeded":
			return snapshot, nil
		case "failed", "cancelled", "unknown", "input_required":
			return snapshot, fmt.Errorf("operation %s ended with status %s: %s", operationID, snapshot.Status, snapshot.Error)
		}
		timer := time.NewTimer(options.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return hostagentclient.OperationSnapshot{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func decodeOperation(result any) (hostagentclient.OperationSnapshot, error) {
	if result == nil {
		return hostagentclient.OperationSnapshot{}, fmt.Errorf("empty operation result")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return hostagentclient.OperationSnapshot{}, err
	}
	var snapshot hostagentclient.OperationSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return hostagentclient.OperationSnapshot{}, err
	}
	if snapshot.Status == "" {
		return hostagentclient.OperationSnapshot{}, fmt.Errorf("operation result has no status")
	}
	return snapshot, nil
}
