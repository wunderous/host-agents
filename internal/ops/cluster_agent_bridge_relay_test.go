package ops

import (
	"runtime"
	"testing"
)

func TestBridgeURLHost(t *testing.T) {
	if got := bridgeURLHost("http://172.23.112.1:9093"); got != "172.23.112.1" {
		t.Fatalf("got %q", got)
	}
	if got := bridgeURLHost("https://host.lan:9093/health"); got != "host.lan" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveHyperVDefaultSwitchIPv4(t *testing.T) {
	got := resolveHyperVDefaultSwitchIPv4()
	if runtime.GOOS == "windows" {
		if got == "" {
			t.Fatal("expected Hyper-V default switch IP on Windows")
		}
		if bridgeURLHost("http://"+got+":9093") != got {
			t.Fatalf("unexpected host %q", got)
		}
		return
	}
	if got != "" {
		t.Fatalf("expected empty off Windows, got %q", got)
	}
}
