package ops

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestRenderLocalLLMK3sProxyManifest(t *testing.T) {
	args := LocalLLMK3sProxyArgs{VMName: "opute-llm-gateway", NodePort: 32114, RelayHost: "10.0.0.1", RelayPort: 41114, RelayToken: strings.Repeat("r", 40), BearerKey: strings.Repeat("b", 40)}
	manifest, err := RenderLocalLLMK3sProxyManifest(args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(manifest, "nodePort: 32114") || !strings.Contains(manifest, "name: opute-llm") {
		t.Fatalf("manifest missing expected gateway resources")
	}
	if strings.Contains(manifest, strings.Repeat("r", 40)) || strings.Contains(manifest, strings.Repeat("b", 40)) || !strings.Contains(manifest, "proxy_buffering off") || !strings.Contains(manifest, "return 401") {
		t.Fatal("manifest must use secret references and enforce authenticated streaming proxying")
	}
	if _, err := RenderLocalLLMK3sProxyManifest(LocalLLMK3sProxyArgs{VMName: "../escape", NodePort: 32114, RelayHost: "10.0.0.1", RelayPort: 41114, RelayToken: strings.Repeat("r", 40), BearerKey: strings.Repeat("b", 40)}); err == nil {
		t.Fatal("expected invalid VM name")
	}
	materialized, err := RenderLocalLLMK3sProxyManifestWithSecrets(args)
	if err != nil || !strings.Contains(materialized, strings.Repeat("r", 40)) || !strings.Contains(materialized, strings.Repeat("b", 40)) {
		t.Fatal("host-side materialization must inject credentials only at execution time")
	}
}

func TestRenderLocalLLMK3sProxyManifestRejectsNonGatewayVM(t *testing.T) {
	args := LocalLLMK3sProxyArgs{VMName: "other-vm", NodePort: 32114, RelayHost: "10.0.0.1", RelayPort: 41114, RelayToken: strings.Repeat("r", 40), BearerKey: strings.Repeat("b", 40)}
	if _, err := RenderLocalLLMK3sProxyManifest(args); err == nil {
		t.Fatal("expected dedicated gateway VM ownership rejection")
	}
}

func TestValidateLocalLLMRelayRejectsUnspecifiedListener(t *testing.T) {
	args := LocalLLMRelayArgs{SessionID: "relay", ListenHost: "0.0.0.0", ListenPort: 11435, TargetHost: "127.0.0.1", TargetPort: 11434, RelayToken: strings.Repeat("r", 40), AllowedSourceIP: "10.0.0.8"}
	if err := ValidateLocalLLMRelayArgs(args); err == nil {
		t.Fatal("expected unspecified listener rejection")
	}
}

func TestLocalLLMRelayRequiresSourceAndBearerAndOnlyForwardsV1(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "public.example" {
			t.Fatalf("public Host header leaked to upstream: %s", r.Host)
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	port := strings.Split(upstream.Listener.Addr().String(), ":")
	targetPort, _ := strconv.Atoi(port[len(port)-1])
	m := newLocalLLMRelayManager()
	result, err := m.start(context.Background(), LocalLLMRelayArgs{SessionID: "test-relay", ListenHost: "127.0.0.1", ListenPort: 0, TargetHost: "127.0.0.1", TargetPort: targetPort, RelayToken: strings.Repeat("r", 40), AllowedSourceIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer m.stop("test-relay")
	base := "http://127.0.0.1:" + strconv.Itoa(result["listenPort"].(int))
	resp, err := http.Get(base + "/api/tags")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected /api denial, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected bearer denial, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	req, _ = http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	req.Host = "public.example"
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("r", 40))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected forwarded request, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestLocalLLMRelayRotatesCredentialsForExistingSession(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	parts := strings.Split(upstream.Listener.Addr().String(), ":")
	targetPort, _ := strconv.Atoi(parts[len(parts)-1])
	m := newLocalLLMRelayManager()
	oldArgs := LocalLLMRelayArgs{
		SessionID:       "rotating-relay",
		ListenHost:      "127.0.0.1",
		ListenPort:      0,
		TargetHost:      "127.0.0.1",
		TargetPort:      targetPort,
		RelayToken:      strings.Repeat("o", 40),
		AllowedSourceIP: "127.0.0.1",
	}
	result, err := m.start(context.Background(), oldArgs)
	if err != nil {
		t.Fatal(err)
	}
	defer m.stop(oldArgs.SessionID)
	base := "http://127.0.0.1:" + strconv.Itoa(result["listenPort"].(int))

	newArgs := oldArgs
	newArgs.RelayToken = strings.Repeat("n", 40)
	rotated, err := m.start(context.Background(), newArgs)
	if err != nil {
		t.Fatal(err)
	}
	base = "http://127.0.0.1:" + strconv.Itoa(rotated["listenPort"].(int))

	for _, token := range []string{oldArgs.RelayToken, newArgs.RelayToken} {
		req, _ := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, requestErr := http.DefaultClient.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		want := http.StatusUnauthorized
		if token == newArgs.RelayToken {
			want = http.StatusOK
		}
		if resp.StatusCode != want {
			t.Fatalf("token %q got status %d, want %d", token[:1], resp.StatusCode, want)
		}
		resp.Body.Close()
	}
}

func TestPersistentLocalLLMRelayRestoresAfterAgentRestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	parts := strings.Split(upstream.Listener.Addr().String(), ":")
	targetPort, _ := strconv.Atoi(parts[len(parts)-1])
	args := LocalLLMRelayArgs{
		SessionID:       "persistent-relay",
		ListenHost:      "127.0.0.1",
		ListenPort:      0,
		TargetHost:      "127.0.0.1",
		TargetPort:      targetPort,
		RelayToken:      strings.Repeat("p", 40),
		AllowedSourceIP: "127.0.0.1",
	}
	first := newPersistentLocalLLMRelayManager()
	if _, err := first.start(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	configPath, err := localLLMRelayConfigPath(args.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("expected mode 0600, got %o", info.Mode().Perm())
	}
	first.mu.Lock()
	firstSession := first.sessions[args.SessionID]
	delete(first.sessions, args.SessionID)
	first.mu.Unlock()
	if err := firstSession.server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	second := newPersistentLocalLLMRelayManager()
	second.mu.Lock()
	restored := second.sessions[args.SessionID]
	second.mu.Unlock()
	if restored == nil {
		t.Fatal("expected persisted relay to restore")
	}
	if !second.stop(args.SessionID) {
		t.Fatal("expected restored relay removal")
	}
	if _, err := os.Stat(filepath.Dir(configPath)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected persisted config removal, got %v", err)
	}
}
