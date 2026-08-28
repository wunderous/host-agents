package llm

import (
	"strings"
	"testing"
)

func TestPersistentRelayManagersUseSeparateDirectories(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	args := LocalLLMRelayArgs{
		SessionID:       "same-session",
		ListenHost:      "127.0.0.1",
		ListenPort:      0,
		TargetHost:      "127.0.0.1",
		TargetPort:      11434,
		RelayToken:      strings.Repeat("r", 40),
		AllowedSourceIP: "127.0.0.1",
	}
	first := newPersistentLocalLLMRelayManagerAt(firstDir)
	if err := first.persistLocalLLMRelayArgs(args); err != nil {
		t.Fatal(err)
	}
	second := newPersistentLocalLLMRelayManagerAt(secondDir)
	if len(second.sessions) != 0 {
		t.Fatalf("second instance restored first instance's relays: %d", len(second.sessions))
	}
	if err := second.persistLocalLLMRelayArgs(args); err != nil {
		t.Fatal(err)
	}
	firstAgain := newPersistentLocalLLMRelayManagerAt(firstDir)
	if len(firstAgain.sessions) != 1 {
		t.Fatalf("first instance did not restore its own relay: %d", len(firstAgain.sessions))
	}
	for id := range firstAgain.sessions {
		firstAgain.stop(id)
	}
	if _, err := localLLMRelayConfigPathInDir(firstDir, args.SessionID); err != nil {
		t.Fatal(err)
	}
}
