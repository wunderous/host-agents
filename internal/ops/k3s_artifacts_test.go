package ops

import (
	"strings"
	"testing"
)

func TestParseK3sArtifactSHA256(t *testing.T) {
	got, err := parseK3sArtifactSHA256([]byte("abc k3s\n"+strings.Repeat("a", 64)+"  k3s\n"), "k3s")
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Repeat("a", 64) {
		t.Fatalf("checksum = %q", got)
	}
	if _, err := parseK3sArtifactSHA256([]byte("bad k3s\n"), "k3s"); err == nil {
		t.Fatal("expected malformed checksum to fail")
	}
}

func TestK3sReleaseVersionPathEscapesPlus(t *testing.T) {
	versionPath := k3sVersionPath("v1.31.8+k3s1")
	if versionPath != "v1.31.8%2Bk3s1" {
		t.Fatalf("version path = %q", versionPath)
	}
}
