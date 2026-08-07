package transport

import (
	"encoding/json"
	"testing"
)

func TestBuildHostWorkerURL(t *testing.T) {
	got := BuildHostWorkerURL("wss://mcp.example.com/mcp-agent/foo")
	want := "wss://mcp.example.com/host/v1/connect"
	if got != want {
		t.Fatalf("BuildHostWorkerURL() = %q, want %q", got, want)
	}
}

func TestStreamFramesRoundTrip(t *testing.T) {
	open := hwpStreamOpenFrame{
		Type:      "stream_open",
		RequestID: "request-1",
		HostID:    "host-1",
		Action:    "stream_vm_shell",
		Args:      map[string]any{"vmName": "k3s-vm"},
	}
	data, err := json.Marshal(open)
	if err != nil {
		t.Fatalf("marshal stream_open: %v", err)
	}
	var envelope hwpServerFrame
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("unmarshal stream_open envelope: %v", err)
	}
	if envelope.Type != "stream_open" {
		t.Fatalf("stream_open type = %q", envelope.Type)
	}

	chunk := hwpStreamChunkFrame{Type: "stream_chunk", StreamID: "stream-1", Data: "hello", EOF: false}
	data, err = json.Marshal(chunk)
	if err != nil {
		t.Fatalf("marshal stream_chunk: %v", err)
	}
	var decoded hwpStreamChunkFrame
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal stream_chunk: %v", err)
	}
	if decoded.StreamID != "stream-1" || decoded.Data != "hello" || decoded.EOF {
		t.Fatalf("decoded stream_chunk = %+v", decoded)
	}
}
