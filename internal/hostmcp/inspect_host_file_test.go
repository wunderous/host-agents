package hostmcp

import (
	"os"
	"path/filepath"
	"testing"
)

// ensure_host_file writes its content byte for byte, so inspect_host_file has
// to compare the expectation byte for byte too. It did not: expectedContent was
// read through the trimming argument accessor, which silently dropped the
// trailing newline that every managed env file, unit file and config ends with.
// A recipe node that wrote a file and then asserted /matches on the exact same
// string could therefore never go green - that is what wedged the
// harness.opute.io recipe at its first node while the file on disk was correct.
func TestInspectHostFileMatchesContentEndingInANewline(t *testing.T) {
	server := newStandaloneTestServer(t, true)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home directory: %v", err)
	}
	dir, err := os.MkdirTemp(home, "hostmcp-inspect-content-")
	if err != nil {
		t.Fatalf("create managed directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "managed.env")
	const content = "OPUTE_ENDPOINT=https://example.invalid/mcp\n"

	if _, err := server.DispatchTool(t.Context(), "ensure_host_file", map[string]any{
		"path":    path,
		"content": content,
		"mode":    384,
	}, nil); err != nil {
		t.Fatalf("ensure managed file: %v", err)
	}

	matched := dispatchInspectHostFile(t, server, path, content)
	if matches, _ := matched["matches"].(bool); !matches {
		t.Fatalf("expected byte-identical content to match, got %#v", matched)
	}

	// The comparison still has to be exact in the other direction: a file that
	// differs only by that newline is not the file the caller asked for.
	mismatched := dispatchInspectHostFile(t, server, path, "OPUTE_ENDPOINT=https://example.invalid/mcp")
	if matches, _ := mismatched["matches"].(bool); matches {
		t.Fatalf("expected content missing the trailing newline to mismatch, got %#v", mismatched)
	}
}

func dispatchInspectHostFile(t *testing.T, server *Server, path, expected string) map[string]any {
	t.Helper()
	result, err := server.DispatchTool(t.Context(), "inspect_host_file", map[string]any{
		"path":            path,
		"expectedContent": expected,
	}, nil)
	if err != nil {
		t.Fatalf("inspect managed file: %v", err)
	}
	if result.IsError {
		t.Fatalf("inspect managed file reported an error: %#v", result.StructuredContent)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("unexpected structured result: %#v", result.StructuredContent)
	}
	return structured
}
