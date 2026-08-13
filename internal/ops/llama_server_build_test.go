package ops

import "testing"

func TestLlamaServerVersionAcceptsStderrOutput(t *testing.T) {
	version := firstNonEmpty("", "version: 0 (unknown)\nbuilt with GNU 15.2.0")
	if version == "" {
		t.Fatal("expected successful stderr version output to be retained")
	}
}
