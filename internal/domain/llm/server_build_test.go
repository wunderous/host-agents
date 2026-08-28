package llm

import (
	"testing"

	"github.com/wunderous/host-agents/internal/textutil"
)

func TestLlamaServerVersionAcceptsStderrOutput(t *testing.T) {
	version := textutil.FirstNonEmpty("", "version: 0 (unknown)\nbuilt with GNU 15.2.0")
	if version == "" {
		t.Fatal("expected successful stderr version output to be retained")
	}
}
