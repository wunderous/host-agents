package ops

import "testing"

func TestReadGuestBridgeListenHostPrefersEnv(t *testing.T) {
	t.Setenv("OPUTE_PLATFORM_GUEST_HOST", "172.22.75.18")
	if got := readGuestBridgeListenHost(); got != "172.22.75.18" {
		t.Fatalf("got %q", got)
	}
}
