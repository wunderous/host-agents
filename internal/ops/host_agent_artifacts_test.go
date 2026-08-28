package ops

import "testing"

func TestHostAgentArtifactDestAllowed(t *testing.T) {
	source := "/data/opute"
	if !hostAgentArtifactDestAllowed("/data/opute/.opute-host-agent-build", source) {
		t.Fatal("expected dest under source to be allowed")
	}
	if hostAgentArtifactDestAllowed("/etc/passwd", source) {
		t.Fatal("expected unrelated dest to be rejected")
	}
}
