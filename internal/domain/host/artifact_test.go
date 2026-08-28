package host

import "testing"

func TestValidateHostArtifactURI(t *testing.T) {
	valid := []string{"https://example.com/provider.tar.zst", "https://github.com/a/b/releases/download/v1/a"}
	for _, value := range valid {
		if err := validateHostArtifactURI(value); err != nil {
			t.Fatalf("%q should be accepted: %v", value, err)
		}
	}
	for _, value := range []string{"http://example.com/a", "file:///tmp/a", "https://user:pass@example.com/a", "/tmp/a"} {
		if err := validateHostArtifactURI(value); err == nil {
			t.Fatalf("%q should be rejected", value)
		}
	}
}

func TestNormalizeSHA256(t *testing.T) {
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got, err := normalizeSHA256("sha256:" + digest); err != nil || got != "sha256:"+digest {
		t.Fatalf("normalize valid digest: got %q, err %v", got, err)
	}
	for _, value := range []string{"", "sha256:short", "not-a-digest", digest + "0"} {
		if _, err := normalizeSHA256(value); err == nil {
			t.Fatalf("%q should be rejected", value)
		}
	}
}
