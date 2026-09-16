package oci

import (
	"errors"
	"strings"
	"testing"
)

func TestDescribeSkippedImageNamesTheTagsThatBlockedIt(t *testing.T) {
	// A release tag beside the rollback tag it replaced is the common refusal.
	// The caller can act on the tag names; it cannot act on "image is in use".
	message := describeSkippedImage(containerImage{
		ID:    "sha256:abc",
		Names: []string{"registry/opute/platform:ha-13", "registry/opute/platform:ha-12"},
	}, errors.New("image is in use by another name"))

	if !strings.Contains(message, "ha-13") || !strings.Contains(message, "ha-12") {
		t.Fatalf("expected both tags in the warning, got %q", message)
	}
	if !strings.Contains(message, "never force-removes") {
		t.Fatalf("expected the refusal to state the boundary, got %q", message)
	}
}

func TestDescribeSkippedImageKeepsTheRuntimeErrorWhenNotSharedByTags(t *testing.T) {
	message := describeSkippedImage(containerImage{
		ID:    "sha256:def",
		Names: []string{"registry/opute/web:ha-13"},
	}, errors.New("image used by running container"))

	if !strings.Contains(message, "image used by running container") {
		t.Fatalf("expected the runtime reason to survive, got %q", message)
	}
}

func TestParseContainerImagesCarriesLocalNames(t *testing.T) {
	images, err := parseContainerImages([]byte(`[{"Id":"sha256:abc","Created":1,"Size":10,"Names":["a:1","b:2"]}]`))
	if err != nil {
		t.Fatalf("parse images: %v", err)
	}
	if len(images) != 1 || len(images[0].Names) != 2 {
		t.Fatalf("expected both names to survive parsing, got %#v", images)
	}
}
