package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeCloudflare struct {
	mu      sync.Mutex
	tunnels map[string]cloudflareTunnel
	ingress map[string][]cloudflareIngressRule
	tokens  map[string]string
	dns     map[string]cloudflareDNSRecord
}

func newFakeCloudflare() *fakeCloudflare {
	return &fakeCloudflare{
		tunnels: map[string]cloudflareTunnel{},
		ingress: map[string][]cloudflareIngressRule{},
		tokens:  map[string]string{},
		dns:     map[string]cloudflareDNSRecord{},
	}
}

func (f *fakeCloudflare) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		path := r.URL.Path
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/cfd_tunnel") && !strings.Contains(path, "/cfd_tunnel/"):
			name := r.URL.Query().Get("name")
			var listed []cloudflareTunnel
			for _, tunnel := range f.tunnels {
				if name == "" || tunnel.Name == name {
					listed = append(listed, tunnel)
				}
			}
			writeCloudflareJSON(w, listed)
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/cfd_tunnel"):
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			id := "tun-" + body.Name
			tunnel := cloudflareTunnel{ID: id, Name: body.Name}
			f.tunnels[id] = tunnel
			f.tokens[id] = "run-token-" + body.Name
			writeCloudflareJSON(w, tunnel)
		case strings.Contains(path, "/cfd_tunnel/") && strings.HasSuffix(path, "/configurations"):
			id := tunnelIDFromPath(path)
			if r.Method == http.MethodGet {
				writeCloudflareJSON(w, map[string]any{"config": map[string]any{"ingress": f.ingress[id]}})
				return
			}
			var body struct {
				Config struct {
					Ingress []cloudflareIngressRule `json:"ingress"`
				} `json:"config"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.ingress[id] = body.Config.Ingress
			writeCloudflareJSON(w, map[string]any{})
		case strings.Contains(path, "/cfd_tunnel/") && strings.HasSuffix(path, "/token"):
			id := tunnelIDFromPath(path)
			writeCloudflareJSON(w, f.tokens[id])
		case r.Method == http.MethodGet && strings.Contains(path, "/cfd_tunnel/"):
			id := tunnelIDFromPath(path)
			tunnel, ok := f.tunnels[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			writeCloudflareJSON(w, tunnel)
		case r.Method == http.MethodDelete && strings.Contains(path, "/cfd_tunnel/"):
			id := tunnelIDFromPath(path)
			delete(f.tunnels, id)
			delete(f.ingress, id)
			delete(f.tokens, id)
			writeCloudflareJSON(w, map[string]any{})
		case r.Method == http.MethodGet && strings.Contains(path, "/dns_records"):
			name := r.URL.Query().Get("name")
			var listed []cloudflareDNSRecord
			for _, record := range f.dns {
				if name == "" || record.Name == name {
					listed = append(listed, record)
				}
			}
			writeCloudflareJSON(w, listed)
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/dns_records"):
			var record cloudflareDNSRecord
			_ = json.NewDecoder(r.Body).Decode(&record)
			record.ID = "dns-" + record.Name
			f.dns[record.ID] = record
			writeCloudflareJSON(w, record)
		case r.Method == http.MethodPut && strings.Contains(path, "/dns_records/"):
			id := path[strings.LastIndex(path, "/")+1:]
			var record cloudflareDNSRecord
			_ = json.NewDecoder(r.Body).Decode(&record)
			record.ID = id
			f.dns[id] = record
			writeCloudflareJSON(w, record)
		case r.Method == http.MethodDelete && strings.Contains(path, "/dns_records/"):
			id := path[strings.LastIndex(path, "/")+1:]
			delete(f.dns, id)
			writeCloudflareJSON(w, map[string]any{})
		default:
			http.NotFound(w, r)
		}
	})
}

func writeCloudflareJSON(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
}

func tunnelIDFromPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "cfd_tunnel" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func withFakeCloudflare(t *testing.T, fake *fakeCloudflare) {
	t.Helper()
	server := httptest.NewServer(fake.handler())
	t.Cleanup(server.Close)
	t.Setenv("CLOUDFLARE_API_BASE", server.URL)
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account")
	t.Setenv("CLOUDFLARE_ZONE_ID", "zone")
	t.Setenv("CLOUDFLARE_API_TOKEN", "token")
}

func TestEnsureThenRemoveDedicatedTunnelLeavesNoCNAMEOrTunnel(t *testing.T) {
	fake := newFakeCloudflare()
	withFakeCloudflare(t, fake)
	args := map[string]any{
		"hostname":    "harness.opute.io",
		"localTarget": "http://127.0.0.1:3080",
		"tunnelName":  "opute-harness-opute-io",
	}
	created, err := ensureDedicatedTunnel(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	if created.TunnelID == "" || created.DNSRecordID == "" || created.RunToken == "" {
		t.Fatalf("incomplete dedicated provision: %#v", created)
	}
	fake.mu.Lock()
	if len(fake.tunnels) != 1 || len(fake.dns) != 1 {
		t.Fatalf("ensure did not persist tunnel and CNAME: tunnels=%d dns=%d", len(fake.tunnels), len(fake.dns))
	}
	ingress := fake.ingress[created.TunnelID]
	fake.mu.Unlock()
	if len(ingress) != 2 || ingress[0].Hostname != "harness.opute.io" || ingress[1].Service != "http_status:404" {
		t.Fatalf("dedicated ingress was not exclusive: %#v", ingress)
	}

	if err := unpublishDedicatedTunnel(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.tunnels) != 0 || len(fake.dns) != 0 {
		t.Fatalf("unpublish left Cloudflare state: tunnels=%d dns=%d", len(fake.tunnels), len(fake.dns))
	}
}

func TestUnpublishDedicatedTunnelIsIdempotent(t *testing.T) {
	fake := newFakeCloudflare()
	withFakeCloudflare(t, fake)
	args := map[string]any{"hostname": "harness.opute.io", "localTarget": "http://127.0.0.1:3080", "tunnelName": "opute-harness-opute-io"}
	if _, err := ensureDedicatedTunnel(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	if err := unpublishDedicatedTunnel(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	if err := unpublishDedicatedTunnel(t.Context(), args); err != nil {
		t.Fatalf("second unpublish should no-op, got %v", err)
	}
}

func TestRefusePlatformTunnelNameAndHostname(t *testing.T) {
	if err := refusePlatformTunnelName("opute-platform-opute-io"); err == nil {
		t.Fatal("expected platform tunnel name refusal")
	}
	if err := refusePlatformHostname("platform.opute.io"); err == nil {
		t.Fatal("expected platform hostname refusal")
	}
	if err := refusePlatformHostname("mcp.opute.io"); err == nil {
		t.Fatal("expected mcp hostname refusal")
	}
	fake := newFakeCloudflare()
	withFakeCloudflare(t, fake)
	if _, err := ensureDedicatedTunnel(t.Context(), map[string]any{
		"hostname":    "harness.opute.io",
		"localTarget": "http://127.0.0.1:3080",
		"tunnelName":  "opute-platform-opute-io",
	}); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("expected platform tunnel name refusal during ensure, got %v", err)
	}
	if err := unpublishDedicatedTunnel(t.Context(), map[string]any{
		"hostname":   "harness.opute.io",
		"tunnelName": "opute-platform-opute-io",
	}); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("expected platform tunnel name refusal during unpublish, got %v", err)
	}
}

func TestRefuseCNAMEPointingAtPlatformTunnel(t *testing.T) {
	fake := newFakeCloudflare()
	withFakeCloudflare(t, fake)
	fake.tunnels["plat"] = cloudflareTunnel{ID: "plat", Name: platformTunnelName}
	fake.ingress["plat"] = []cloudflareIngressRule{{Hostname: "platform.opute.io", Service: "http://127.0.0.1:8080"}}
	fake.dns["dns-harness.opute.io"] = cloudflareDNSRecord{
		ID: "dns-harness.opute.io", Type: "CNAME", Name: "harness.opute.io", Content: "plat.cfargotunnel.com",
	}
	err := unpublishDedicatedTunnel(t.Context(), map[string]any{
		"hostname":   "harness.opute.io",
		"tunnelName": "opute-harness-opute-io",
	})
	if err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("expected refusal when CNAME points at the platform tunnel, got %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if _, ok := fake.dns["dns-harness.opute.io"]; !ok {
		t.Fatal("platform CNAME was deleted")
	}
}

func TestRefuseExistingTunnelWithPlatformIngress(t *testing.T) {
	fake := newFakeCloudflare()
	withFakeCloudflare(t, fake)
	fake.tunnels["tun-opute-harness-opute-io"] = cloudflareTunnel{ID: "tun-opute-harness-opute-io", Name: "opute-harness-opute-io"}
	fake.ingress["tun-opute-harness-opute-io"] = []cloudflareIngressRule{
		{Hostname: "platform.opute.io", Service: "http://127.0.0.1:80"},
		{Service: "http_status:404"},
	}
	_, err := ensureDedicatedTunnel(t.Context(), map[string]any{
		"hostname":    "harness.opute.io",
		"localTarget": "http://127.0.0.1:3080",
		"tunnelName":  "opute-harness-opute-io",
	})
	if err == nil || !strings.Contains(err.Error(), "platform.opute.io") {
		t.Fatalf("expected platform ingress refusal, got %v", err)
	}
}

func TestEnsureDedicatedTunnelDoesNotCallHostAgent(t *testing.T) {
	fake := newFakeCloudflare()
	withFakeCloudflare(t, fake)
	t.Setenv("OPUTE_HOST_AGENT_ENDPOINT", "")
	result, err := ensureTunnel(t.Context(), map[string]any{
		"bindingId":   "dsh-opute-web",
		"hostname":    "harness.opute.io",
		"localTarget": "http://127.0.0.1:3080",
		"tunnelName":  "opute-harness-opute-io",
	})
	if err != nil {
		t.Fatal(err)
	}
	content, _ := result.StructuredContent.(map[string]any)
	if content == nil || content["ready"] != true {
		t.Fatalf("dedicated ensure did not report ready: %#v", result.StructuredContent)
	}
	if content["runToken"] != "run-token-opute-harness-opute-io" {
		t.Fatalf("recipe interpolation needs the minted runToken, got %#v", content["runToken"])
	}
	if content["tunnelId"] == "" || content["dnsRecordId"] == "" {
		t.Fatalf("dedicated ensure missing Cloudflare ids: %#v", content)
	}
}

func TestParseTunnelIDFromCNAME(t *testing.T) {
	if got := parseTunnelIDFromCNAME("abc.cfargotunnel.com"); got != "abc" {
		t.Fatalf("got %q", got)
	}
	if got := parseTunnelIDFromCNAME("not-a-tunnel.example"); got != "" {
		t.Fatalf("got %q", got)
	}
}
