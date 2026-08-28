package llm

import "testing"

// A relay listener's bind identity is (host, port). Reclaiming a "stale"
// session by port alone tore down the live 127.0.0.1:11435 dev relay when the
// dogfood cell relay was ensured on 10.0.100.1:11435, leaving local chat with
// an unreachable runtime. These bindings never conflicted at the OS level.
func TestLocalLLMRelayBindingsConflict(t *testing.T) {
	cases := []struct {
		name         string
		existingHost string
		existingPort int
		desiredHost  string
		desiredPort  int
		want         bool
	}{
		{"distinct addresses same port do not conflict", "127.0.0.1", 11435, "10.0.100.1", 11435, false},
		{"identical binding conflicts", "127.0.0.1", 11435, "127.0.0.1", 11435, true},
		{"existing wildcard conflicts with any address", "0.0.0.0", 11435, "10.0.100.1", 11435, true},
		{"desired wildcard conflicts with any address", "127.0.0.1", 11435, "0.0.0.0", 11435, true},
		{"empty host is a wildcard", "", 11435, "127.0.0.1", 11435, true},
		{"ipv6 unspecified is a wildcard", "::", 11435, "127.0.0.1", 11435, true},
		{"different ports never conflict", "127.0.0.1", 11435, "127.0.0.1", 11436, false},
		{"unspecified desired port never reclaims", "127.0.0.1", 11435, "127.0.0.1", 0, false},
		{"equivalent ipv6 form of the same address conflicts", "127.0.0.1", 11435, "::ffff:127.0.0.1", 11435, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := localLLMRelayBindingsConflict(tc.existingHost, tc.existingPort, tc.desiredHost, tc.desiredPort); got != tc.want {
				t.Fatalf("localLLMRelayBindingsConflict(%q,%d,%q,%d) = %v, want %v", tc.existingHost, tc.existingPort, tc.desiredHost, tc.desiredPort, got, tc.want)
			}
		})
	}
}
