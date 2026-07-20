//go:build integration

package live_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/hostmcp"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/provider"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/internal/transport"
)

func requireIncus(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("incus"); err != nil {
		t.Skip("incus CLI not available")
	}
	res, err := exec.Command("incus", "list", "--format", "csv").CombinedOutput()
	if err != nil {
		t.Skipf("incus not usable: %v (%s)", err, strings.TrimSpace(string(res)))
	}
}

func newLiveServer(t *testing.T) (*hostmcp.Server, *httptest.Server) {
	t.Helper()
	svc := ops.NewHostOperationsService(ops.Options{
		ProviderID: provider.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	hs, err := hostmcp.NewServer(hostmcp.Options{ProviderID: "incus", Ops: svc})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	httpSrv := transport.NewHTTPServer(transport.HTTPOptions{
		HostServer: hs,
		BindHost:   "127.0.0.1",
		Port:       0,
	})
	ts := httptest.NewServer(httpSrv.Handler())
	t.Cleanup(ts.Close)
	return hs, ts
}

func connectClient(t *testing.T, baseURL string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "live-test", Version: "v1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: baseURL + "/mcp",
	}, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestLiveGetHostInfoIncus(t *testing.T) {
	requireIncus(t)
	_, ts := newLiveServer(t)
	session := connectClient(t, ts.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_host_info",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_host_info failed: %+v", res)
	}
	content, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected structuredContent map, got %T", res.StructuredContent)
	}
	if content["providerId"] != "incus" {
		t.Fatalf("providerId=%v want incus", content["providerId"])
	}
	binaryPath, _ := content["lxcBinaryPath"].(string)
	if !strings.Contains(binaryPath, "incus") {
		t.Fatalf("lxcBinaryPath=%q should point at incus", binaryPath)
	}
}

func TestLiveVMCreateListDelete(t *testing.T) {
	requireIncus(t)
	_, ts := newLiveServer(t)
	session := connectClient(t, ts.URL)

	vmName := fmt.Sprintf("go-live-%d", time.Now().Unix())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	create, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "create_vm",
		Arguments: map[string]any{
			"vmName": vmName,
			"image":  "ubuntu:22.04",
			"cpus":   1,
			"memory": "1GiB",
		},
	})
	if err != nil {
		t.Fatalf("create_vm: %v", err)
	}
	if create.IsError {
		t.Fatalf("create_vm failed: %+v", create)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cleanupCancel()
		_, _ = session.CallTool(cleanupCtx, &mcp.CallToolParams{
			Name:      "delete_vm",
			Arguments: map[string]any{"vmName": vmName},
		})
	})

	list, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_vms",
		Arguments: map[string]any{"fast": true},
	})
	if err != nil {
		t.Fatalf("list_vms: %v", err)
	}
	if list.IsError {
		t.Fatalf("list_vms failed: %+v", list)
	}
	listBody, ok := list.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("list_vms structuredContent type %T", list.StructuredContent)
	}
	vms, ok := listBody["vms"].([]any)
	if !ok {
		t.Fatalf("list_vms missing vms array: %+v", listBody)
	}
	found := false
	for _, entry := range vms {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if row["name"] == vmName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("VM %q not found in list_vms (%d rows)", vmName, len(vms))
	}

	info, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_vm_info",
		Arguments: map[string]any{"vmName": vmName, "fast": true},
	})
	if err != nil {
		t.Fatalf("get_vm_info: %v", err)
	}
	if info.IsError {
		t.Fatalf("get_vm_info failed: %+v", info)
	}
	infoBody, ok := info.StructuredContent.(map[string]any)
	if !ok || infoBody["name"] != vmName {
		t.Fatalf("get_vm_info unexpected payload: %+v", info.StructuredContent)
	}

	del, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "delete_vm",
		Arguments: map[string]any{"vmName": vmName},
	})
	if err != nil {
		t.Fatalf("delete_vm: %v", err)
	}
	if del.IsError {
		t.Fatalf("delete_vm failed: %+v", del)
	}
}

func TestMain(m *testing.M) {
	if err := provider.RequireLinux(); err != nil {
		os.Exit(0)
	}
	os.Exit(m.Run())
}
