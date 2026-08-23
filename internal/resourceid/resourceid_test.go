package resourceid

import (
	"errors"
	"testing"
)

func TestParseAllowsColonsInResourceID(t *testing.T) {
	u, err := Parse("model:local:qwen3.5:2b")
	if err != nil {
		t.Fatal(err)
	}
	if got := u.String(); got != "model:local:qwen3.5:2b" {
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
