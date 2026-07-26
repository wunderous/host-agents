package ops

import (
	"strings"
	"testing"
)

func TestRenderLiteRTLMSystemdUnit(t *testing.T) {
	unit := RenderLiteRTLMSystemdUnit(11436, litertLMHFRepoDefault)
	if !strings.Contains(unit, "litert-lm openai_server") {
		t.Fatalf("expected openai_server command, got %q", unit)
	}
	if !strings.Contains(unit, litertLMHFRepoDefault) {
		t.Fatalf("expected HF repo in unit")
	}
	if !strings.Contains(unit, "--port=11436") {
		t.Fatalf("expected port in unit")
	}
}
