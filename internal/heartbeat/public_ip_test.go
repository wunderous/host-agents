package heartbeat

import (
	"net"
	"runtime"
	"testing"
)

func TestPrimaryLANIPv4(t *testing.T) {
	ip := PrimaryLANIPv4()
	if runtime.GOOS == "windows" {
		if ip != "" && net.ParseIP(ip) == nil {
			t.Fatalf("expected valid IPv4 or empty on windows, got %q", ip)
		}
		return
	}
	if ip == "" {
		t.Skip("no LAN IPv4 available in test environment")
	}
	if net.ParseIP(ip) == nil {
		t.Fatalf("expected valid IPv4, got %q", ip)
	}
}
