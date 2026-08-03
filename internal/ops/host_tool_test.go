package ops

import "testing"

func TestIncusCandidateSatisfies(t *testing.T) {
	cases := []struct {
		candidate string
		requested string
		want      bool
	}{
		{"1:7.3-ubuntu26.04-202607310350", "7.2", true},
		{"7.2-ubuntu26.04", "7.2", true},
		{"1:6.2-ubuntu26.04", "7.2", false},
		{"1:7.3-ubuntu26.04", "6.0", false},
	}
	for _, c := range cases {
		if got := incusCandidateSatisfies(c.candidate, c.requested); got != c.want {
			t.Fatalf("incusCandidateSatisfies(%q, %q) = %v, want %v", c.candidate, c.requested, got, c.want)
		}
	}
}
