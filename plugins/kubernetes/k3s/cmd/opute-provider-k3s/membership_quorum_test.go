package main

import "testing"

// remove-node is the one HA capability whose failure mode is destroying the
// cluster it was asked to shrink, so the quorum arithmetic is asserted directly
// rather than only through the manifest contract.
func TestQuorumAfterRemovalRefusesToBreakTheCluster(t *testing.T) {
	ready := map[string]any{"name": "server-c", "ready": true}
	notReady := map[string]any{"name": "server-c", "ready": false}

	cases := []struct {
		name        string
		observation membershipObservation
		member      map[string]any
		wantServers int
		wantReady   int
		wantErr     bool
	}{
		{
			name:        "three ready servers can lose one",
			observation: membershipObservation{ServerCount: 3, ReadyServers: 3},
			member:      ready,
			wantServers: 2,
			wantReady:   2,
		},
		{
			// The node being retired is already down, so removing it does not
			// change how many servers can still vote.
			name:        "an unready server can be retired from a healthy pair",
			observation: membershipObservation{ServerCount: 3, ReadyServers: 2},
			member:      notReady,
			wantServers: 2,
			wantReady:   2,
		},
		{
			name:        "the last server is never removable",
			observation: membershipObservation{ServerCount: 1, ReadyServers: 1},
			member:      ready,
			wantErr:     true,
		},
		{
			// 3 servers with only 2 ready: retiring a ready one leaves 1 of 2,
			// which is not a majority.
			name:        "removal that would lose the majority is refused",
			observation: membershipObservation{ServerCount: 3, ReadyServers: 2},
			member:      ready,
			wantErr:     true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			servers, readyServers, err := quorumAfterRemoval(testCase.observation, testCase.member)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("expected removal to be refused, got %d ready of %d", readyServers, servers)
				}
				return
			}
			if err != nil {
				t.Fatalf("quorumAfterRemoval: %v", err)
			}
			if servers != testCase.wantServers || readyServers != testCase.wantReady {
				t.Fatalf("got %d ready of %d server(s), want %d of %d", readyServers, servers, testCase.wantReady, testCase.wantServers)
			}
		})
	}
}
