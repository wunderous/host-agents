package main

import (
	"strings"
	"testing"
)

// The admission policy is the whole safety story for a cluster reset, so it is
// tested as a unit rather than through a guest: every refusal below is a case
// where running the reset would destroy a cluster that did not need resetting.
func TestQuorumRecoveryAdmissible(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		embeddedEtcd      bool
		apiServerAnswered bool
		acknowledged      bool
		wantErrContains   string
	}{
		{
			name:         "wedged embedded-etcd cluster with an acknowledged reset is admitted",
			embeddedEtcd: true, apiServerAnswered: false, acknowledged: true,
			wantErrContains: "",
		},
		{
			name:         "an unacknowledged reset is refused",
			embeddedEtcd: true, apiServerAnswered: false, acknowledged: false,
			wantErrContains: "acknowledgeSingleMemberReset",
		},
		{
			name:         "a cluster that is not embedded-etcd is refused",
			embeddedEtcd: false, apiServerAnswered: false, acknowledged: true,
			wantErrContains: "embedded-etcd",
		},
		{
			// This is the case that matters most: a cluster that still answers
			// has a majority, and resetting it would throw away its peers.
			name:         "a cluster whose API server answered is refused and pointed at remove-node",
			embeddedEtcd: true, apiServerAnswered: true, acknowledged: true,
			wantErrContains: "remove-node",
		},
		{
			name:         "acknowledgement is required even when every other precondition fails",
			embeddedEtcd: false, apiServerAnswered: true, acknowledged: false,
			wantErrContains: "acknowledgeSingleMemberReset",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := quorumRecoveryAdmissible(tc.embeddedEtcd, tc.apiServerAnswered, tc.acknowledged)
			if tc.wantErrContains == "" {
				if err != nil {
					t.Fatalf("expected admission, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected refusal containing %q, got nil", tc.wantErrContains)
			}
			if !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Fatalf("expected refusal containing %q, got: %v", tc.wantErrContains, err)
			}
		})
	}
}

// A probe error is carried into the operation result for diagnosis, so it must
// stay bounded: kubectl emits one line per discovery retry and the untruncated
// text would dominate the response.
func TestTruncateProbeError(t *testing.T) {
	t.Parallel()

	if got := truncateProbeError("  boom  "); got != "boom" {
		t.Fatalf("expected trimmed message, got %q", got)
	}
	long := strings.Repeat("x", 900)
	got := truncateProbeError(long)
	if len(got) != 512 {
		t.Fatalf("expected truncation to 512 bytes, got %d", len(got))
	}
}
