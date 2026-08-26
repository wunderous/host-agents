package resourceid

import (
	"errors"
	"testing"
)

func TestParseAllowsColonsInResourceID(t *testing.T) {
	u, err := Parse("model:local:hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M")
	if err != nil {
		t.Fatal(err)
	}
	if got := u.String(); got != "model:local:hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M" {
		t.Fatalf("round trip = %q", got)
	}
}

func TestParseRejectsUnknownOrForeignShape(t *testing.T) {
	if _, err := Parse("unknown:local:id"); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("unknown type error = %v", err)
	}
	if _, err := Parse("vm:local:"); !errors.Is(err, ErrInvalidURI) {
		t.Fatalf("empty id error = %v", err)
	}
	if _, err := Parse("vm:tenant with space:id"); !errors.Is(err, ErrInvalidURI) {
		t.Fatalf("invalid tenant error = %v", err)
	}
}

func TestNewRejectsWhitespaceResourceID(t *testing.T) {
	if _, err := New(TypeVM, "local", "worker 01"); !errors.Is(err, ErrInvalidURI) {
		t.Fatalf("whitespace error = %v", err)
	}
}

func TestKnownTypesCoverSupportedEntityFamilies(t *testing.T) {
	for _, kind := range []string{TypeHost, TypeVM, TypeContainer, TypePod, TypeCluster, TypeDatabase, TypeService, TypeStorage, TypeModel, TypeTunnel} {
		if !IsKnownType(kind) {
			t.Fatalf("supported resource kind %q is not registered", kind)
		}
	}
	uri, err := PodURI("local", "cluster/ns/pod/uid-123")
	if err != nil {
		t.Fatal(err)
	}
	if got := uri.String(); got != "pod:local:cluster/ns/pod/uid-123" {
		t.Fatalf("pod URI = %q", got)
	}
}
