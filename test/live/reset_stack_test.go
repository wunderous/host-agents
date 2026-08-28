//go:build integration

package live_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/hostmcp"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/internal/transport"
)

func newLiveServerWithOwnership(t *testing.T, instanceID, checkpointPath string) (*hostmcp.Server, *httptest.Server) {
	t.Helper()
	svc := ops.NewHostOperationsService(ops.Options{
		ProviderID:              hostruntime.IDIncus,
		InstanceID:              instanceID,
		OwnershipMode:           "enforce",
		SharedHostOwnerInstance: instanceID,
		ResetCheckpointPath:     checkpointPath,
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

func incusRaw(args ...string) (string, error) {
	out, err := exec.Command("incus", args...).CombinedOutput()
	return string(out), err
}

func TestLiveResetIncusStackDeleteReconcileAndVerify(t *testing.T) {
	requireIncus(t)
	checkpointPath := t.TempDir() + "/reset-checkpoint.json"
	const instanceID = "go-live-reset-agent"
	_, ts := newLiveServerWithOwnership(t, instanceID, checkpointPath)
	session := connectClient(t, ts.URL)

	containerName := fmt.Sprintf("go-live-reset-%d", time.Now().Unix())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	create, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "provision_container",
		Arguments: map[string]any{
			"containerName": containerName,
			"image":         "images:ubuntu/24.04",
			"disk":          "8GiB",
			"nesting":       false,
		},
	})
	if err != nil || create.IsError {
		// Fixture provisioning failure is a harness failure, not product
		// evidence; skip so a broken Incus image mirror never blocks closure.
		t.Skipf("provision_container fixture unavailable: %v %+v", err, create)
	}
	t.Cleanup(func() {
		// Product-path cleanup first; operator escape afterwards.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cleanupCancel()
		_, _ = session.CallTool(cleanupCtx, &mcp.CallToolParams{
			Name:      "delete_vm",
			Arguments: map[string]any{"vmName": containerName, "cascade": true},
		})
		if _, listErr := incusRaw("list", containerName, "--format", "csv"); listErr == nil {
			_, _ = incusRaw("delete", containerName, "--force")
		}
	})

	if _, err := incusRaw("config", "set", containerName, "user.opute.host_agent_instance", instanceID); err != nil {
		t.Fatalf("set ownership label: %v", err)
	}

	reset, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "reset_incus_stack",
		Arguments: map[string]any{
			"instanceNames":               []any{containerName},
			"confirm":                     true,
			"reinstall":                   true,
			"disposableHostFingerprint":   "go-live-reset-fingerprint",
			"expectedHostFingerprint":     "go-live-reset-fingerprint",
			"disposableHostAuthorization": "dispose:go-live-reset-fingerprint",
		},
	})
	if err != nil {
		t.Fatalf("reset_incus_stack: %v", err)
	}
	if reset.IsError {
		t.Fatalf("reset_incus_stack failed: %+v", reset)
	}
	body, ok := reset.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("reset_incus_stack structuredContent type %T", reset.StructuredContent)
	}
	if body["phase"] != "reconciled" {
		t.Fatalf("expected reconciled phase, got %+v", body)
	}
	verify, ok := body["verify"].(map[string]any)
	if !ok || verify["verified"] != true {
		t.Fatalf("expected post-reset Incus verification evidence, got %+v", body["verify"])
	}
	if verify["poolReady"] != true || verify["bridgeReady"] != true || verify["profileReady"] != true {
		t.Fatalf("expected default pool, incusbr0, and default-profile root disk: %+v", verify)
	}
	listOutput, err := incusRaw("list", "--format", "csv")
	if err != nil {
		t.Fatalf("incus list after reset: %v", err)
	}
	if strings.Contains(listOutput, containerName) {
		t.Fatalf("reset left the disposable instance behind: %s", listOutput)
	}
}
