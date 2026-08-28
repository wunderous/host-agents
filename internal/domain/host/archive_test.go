package host

import "testing"

func TestSafeArchiveEntryName(t *testing.T) {
	for _, value := range []string{"bin/ollama", "lib/ollama/llama-server", "./lib/file"} {
		if _, err := safeArchiveEntryName(value); err != nil {
			t.Fatalf("%q should be accepted: %v", value, err)
		}
	}
	for _, value := range []string{"../outside", "/etc/passwd", "lib/../outside", `lib\\outside`, ".."} {
		if _, err := safeArchiveEntryName(value); err == nil {
			t.Fatalf("%q should be rejected", value)
		}
	}
}

func TestSafeArchiveLinkTarget(t *testing.T) {
	destination := "/home/test/runtime"
	target := destination + "/lib/ollama/libfoo.so"
	if got, err := safeArchiveLinkTarget(destination, target, "libfoo.so.1"); err != nil || got != "libfoo.so.1" {
		t.Fatalf("relative link should be accepted: got %q, err %v", got, err)
	}
	for _, value := range []string{"/etc/passwd", "../../outside", `..\\outside`} {
		if _, err := safeArchiveLinkTarget(destination, target, value); err == nil {
			t.Fatalf("link target %q should be rejected", value)
		}
	}
}
