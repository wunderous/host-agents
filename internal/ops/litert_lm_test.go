package ops

import (
	"strings"
	"testing"
)

func TestRenderLiteRTLMSystemdUnit(t *testing.T) {
	unit := RenderLiteRTLMSystemdUnit(11436, litertLMHFRepoDefault)
	if !strings.Contains(unit, "litert-lm serve") {
		t.Fatalf("expected serve command, got %q", unit)
	}
	if !strings.Contains(unit, "--port 11436") {
		t.Fatalf("expected port in unit")
	}
	if strings.Contains(unit, "E4B") || !strings.Contains(unit, "127.0.0.1") {
		t.Fatalf("expected E2B registry-backed local server, got %q", unit)
	}
}
