package ops

import "testing"

func TestNormalizeIncusLaunchImage(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "images:ubuntu/22.04"},
		{"ubuntu:22.04", "images:ubuntu/22.04"},
		{"ubuntu:jammy", "images:ubuntu/jammy"},
		{"images:ubuntu/22.04", "images:ubuntu/22.04"},
		{"local:8897d81f9609", "local:8897d81f9609"},
		{"debian:12", "debian:12"},
	}
	for _, tc := range tests {
		got := normalizeIncusLaunchImage(tc.in)
		if got != tc.want {
			t.Fatalf("normalizeIncusLaunchImage(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}
