package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestRenderLocalLLMK3sProxyManifestAcceptsGenericApplicationNames(t *testing.T) {
	args := LocalLLMK3sProxyArgs{
		VMName: "other-vm", Namespace: "app-system", SecretName: "proxy-credentials",
		ConfigMapName: "proxy-config", DeploymentName: "proxy", ServiceName: "proxy-service",
		ContainerImage: "registry.local/proxy:dogfood", NodePort: 32114, RelayHost: "10.0.0.1",
		RelayPort: 41114, RelayToken: strings.Repeat("r", 40), BearerKey: strings.Repeat("b", 40),
	}
	manifest, err := RenderLocalLLMK3sProxyManifest(args)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"name: app-system", "name: proxy-credentials", "name: proxy-service", "registry.local/proxy:dogfood"} {
		if !strings.Contains(manifest, expected) {
			t.Fatalf("generic manifest missing %q", expected)
		}
	}
}

func TestValidateLocalLLMRelayRejectsUnspecifiedListener(t *testing.T) {
	args := LocalLLMRelayArgs{SessionID: "relay", ListenHost: "0.0.0.0", ListenPort: 11435, TargetHost: "127.0.0.1", TargetPort: 11434, RelayToken: strings.Repeat("r", 40), AllowedSourceIP: "10.0.0.8"}
	if err := ValidateLocalLLMRelayArgs(args); err == nil {
		t.Fatal("expected unspecified listener rejection")
	}
}

func TestLocalLLMRelayRequiresBearerForLlamaServer(t *testing.T) {
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
	resp, err := http.Get(base + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected llama-server bearer denial, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("r", 40))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected forwarded llama-server request, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	req, _ = http.NewRequest(http.MethodGet, base+"/v1/models", nil)
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

func TestLocalLLMRelayForwardsRuntimeResidencyEndpoint(t *testing.T) {
	psSeen := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			psSeen <- r.URL.Path
			_, _ = io.WriteString(w, `{"models":[{"name":"qwen3.5-0.8b-opute-llama:latest","size_vram":2147483648}]}`)
		case "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"qwen3.5-0.8b-opute-llama"}]}`)
		case "/api/embed":
			_, _ = io.WriteString(w, `{"embeddings":[[0.1,0.2]]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	parts := strings.Split(upstream.Listener.Addr().String(), ":")
	targetPort, _ := strconv.Atoi(parts[len(parts)-1])
	m := newLocalLLMRelayManager()
	result, err := m.start(context.Background(), LocalLLMRelayArgs{
		SessionID: "residency-relay", ListenHost: "127.0.0.1", ListenPort: 0,
		TargetHost: "127.0.0.1", TargetPort: targetPort,
		RelayToken: strings.Repeat("r", 40), AllowedSourceIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.stop("residency-relay")
	base := "http://127.0.0.1:" + strconv.Itoa(result["listenPort"].(int))

	req, _ := http.NewRequest(http.MethodGet, base+"/api/ps", nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("r", 40))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected forwarded /api/ps, got %d", resp.StatusCode)
	}
	select {
	case <-psSeen:
	case <-time.After(time.Second):
		t.Fatal("upstream never received /api/ps")
	}

	req, _ = http.NewRequest(http.MethodGet, base+"/api/ps", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected bearer denial for /api/ps, got %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, base+"/api/embed", strings.NewReader(`{"model":"granite","input":["hello"]}`))
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("r", 40))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected forwarded /api/embed, got %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, base+"/api/tags", nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("r", 40))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected unlisted native path rejection, got %d", resp.StatusCode)
	}
}

func TestLocalLLMRelayFlushesStreamingChatChunks(t *testing.T) {
	flushed := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("upstream ResponseWriter must flush")
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ready\"}}]}\n\n")
		flusher.Flush()
		select {
		case flushed <- struct{}{}:
		default:
		}
		// Keep the upstream open so a buffering proxy would hang the client.
		<-r.Context().Done()
	}))
	defer upstream.Close()
	parts := strings.Split(upstream.Listener.Addr().String(), ":")
	targetPort, _ := strconv.Atoi(parts[len(parts)-1])
	m := newLocalLLMRelayManager()
	result, err := m.start(context.Background(), LocalLLMRelayArgs{
		SessionID: "stream-relay", ListenHost: "127.0.0.1", ListenPort: 0,
		TargetHost: "127.0.0.1", TargetPort: targetPort,
		RelayToken: strings.Repeat("r", 40), AllowedSourceIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.stop("stream-relay")
	base := "http://127.0.0.1:" + strconv.Itoa(result["listenPort"].(int))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("r", 40))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	buf := make([]byte, 64)
	n, readErr := resp.Body.Read(buf)
	if readErr != nil && n == 0 {
		t.Fatalf("expected streamed chunk before upstream close: %v", readErr)
	}
	if !strings.Contains(string(buf[:n]), "ready") {
		t.Fatalf("unexpected chunk %q", string(buf[:n]))
	}
	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Fatal("upstream never flushed")
	}
}

func TestLocalLLMRelaysSerializeInferenceAcrossAgentProcesses(t *testing.T) {
	var inFlight int32
	var maxInFlight int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		current := atomic.AddInt32(&inFlight, 1)
		for {
			previous := atomic.LoadInt32(&maxInFlight)
			if current <= previous || atomic.CompareAndSwapInt32(&maxInFlight, previous, current) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		atomic.AddInt32(&inFlight, -1)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	parts := strings.Split(upstream.Listener.Addr().String(), ":")
	targetPort, _ := strconv.Atoi(parts[len(parts)-1])
	lockDir := t.TempDir()
	newManager := func() *localLLMRelayManager {
		return &localLLMRelayManager{sessions: map[string]*localLLMRelaySession{}, requestLock: newHostRequestLock(lockDir)}
	}
	first, second := newManager(), newManager()
	start := func(m *localLLMRelayManager, sessionID string) string {
		result, err := m.start(context.Background(), LocalLLMRelayArgs{
			SessionID: sessionID, ListenHost: "127.0.0.1", ListenPort: 0,
			TargetHost: "127.0.0.1", TargetPort: targetPort,
			RelayToken: strings.Repeat("r", 40), AllowedSourceIP: "127.0.0.1",
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { m.stop(sessionID) })
		return "http://127.0.0.1:" + strconv.Itoa(result["listenPort"].(int))
	}
	firstURL := start(first, "first-inference-relay")
	secondURL := start(second, "second-inference-relay")
	request := func(base string) error {
		req, err := http.NewRequest(http.MethodPost, base+"/v1/chat/completions", strings.NewReader(`{"stream":false}`))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("r", 40))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("inference returned HTTP %d", resp.StatusCode)
		}
		return nil
	}
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	wg.Add(1)
	go func() { defer wg.Done(); errors <- request(firstURL) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first inference never reached upstream")
	}
	wg.Add(1)
	go func() { defer wg.Done(); errors <- request(secondURL) }()
	select {
	case <-entered:
		t.Fatal("second inference bypassed the shared request lock")
	case <-time.After(100 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("second inference never reached upstream after first completed")
	}
	release <- struct{}{}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt32(&maxInFlight); got != 1 {
		t.Fatalf("upstream observed %d concurrent inference requests", got)
	}
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

func TestLocalLLMRelayReplacesTargetForExistingSession(t *testing.T) {
	firstUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"backend":"first"}`)
	}))
	defer firstUpstream.Close()
	secondUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"backend":"second"}`)
	}))
	defer secondUpstream.Close()

	address := func(server *httptest.Server) (string, int) {
		parts := strings.Split(server.Listener.Addr().String(), ":")
		port, _ := strconv.Atoi(parts[len(parts)-1])
		return "127.0.0.1", port
	}
	firstHost, firstPort := address(firstUpstream)
	secondHost, secondPort := address(secondUpstream)
	m := newLocalLLMRelayManager()
	first := LocalLLMRelayArgs{
		SessionID: "target-rotation-relay", ListenHost: "127.0.0.1", ListenPort: 0,
		TargetHost: firstHost, TargetPort: firstPort, RelayToken: strings.Repeat("r", 40),
		AllowedSourceIP: "127.0.0.1",
	}
	result, err := m.start(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	defer m.stop(first.SessionID)

	second := first
	second.TargetHost = secondHost
	second.TargetPort = secondPort
	rotated, err := m.start(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}

	base := "http://127.0.0.1:" + strconv.Itoa(rotated["listenPort"].(int))
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+first.RelayToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), `{"backend":"second"}`; got != want {
		t.Fatalf("relay returned %s, want %s", got, want)
	}
	_ = result
}

func TestLocalLLMRelayReclaimsTrackedStalePort(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	parts := strings.Split(upstream.Listener.Addr().String(), ":")
	targetPort, _ := strconv.Atoi(parts[len(parts)-1])
	m := newLocalLLMRelayManager()
	first := LocalLLMRelayArgs{SessionID: "stale-relay", ListenHost: "127.0.0.1", ListenPort: 0, TargetHost: "127.0.0.1", TargetPort: targetPort, RelayToken: strings.Repeat("s", 40), AllowedSourceIP: "127.0.0.1"}
	result, err := m.start(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	port := result["listenPort"].(int)
	second := first
	second.SessionID = "replacement-relay"
	second.ListenPort = port
	if _, err := m.start(context.Background(), second); err != nil {
		t.Fatalf("expected stale relay port to be reclaimed: %v", err)
	}
	defer m.stop(second.SessionID)
	if m.stop(first.SessionID) {
		t.Fatal("stale relay should already have been reclaimed")
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
