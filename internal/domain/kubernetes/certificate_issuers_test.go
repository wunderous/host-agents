package kubernetes

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

// issuerListFake answers `kubectl get <resource>` from a table, and records the
// argument vectors it was handed so a test can assert on scoping.
type issuerListFake struct {
	responses map[string]string
	errs      map[string]error
	calls     [][]string
}

func (f *issuerListFake) run(_ context.Context, _ string, args []string, _ []byte, _ string, _ time.Duration) (string, error) {
	f.calls = append(f.calls, args)
	resource := ""
	if len(args) >= 2 {
		resource = args[1]
	}
	if err, ok := f.errs[resource]; ok {
		return "", err
	}
	if body, ok := f.responses[resource]; ok {
		return body, nil
	}
	return `{"items":[]}`, nil
}

func newIssuerService(t *testing.T, fake *issuerListFake) *Service {
	t.Helper()
	svc := New(&hostruntime.Shared{}, Deps{})
	svc.SetKubectlRunner(fake.run)
	return svc
}

const clusterIssuerList = `{"items":[
	{"metadata":{"name":"letsencrypt-prod","creationTimestamp":"2026-09-01T00:00:00Z"},
	 "spec":{"acme":{"server":"https://acme-v02.api.letsencrypt.org/directory"}},
	 "status":{"conditions":[{"type":"Ready","status":"True"}]}},
	{"metadata":{"name":"selfsigned","creationTimestamp":"2026-09-01T00:00:00Z"},
	 "spec":{"selfSigned":{}},
	 "status":{"conditions":[{"type":"Ready","status":"False"}]}}
]}`

const namespacedIssuerList = `{"items":[
	{"metadata":{"name":"internal-ca","namespace":"opute-platform","creationTimestamp":"2026-09-01T00:00:00Z"},
	 "spec":{"ca":{"secretName":"root-ca"}},
	 "status":{"conditions":[{"type":"Ready","status":"True"}]}}
]}`

func TestListCertificateIssuersReadsBothKinds(t *testing.T) {
	fake := &issuerListFake{responses: map[string]string{
		"clusterissuers": clusterIssuerList,
		"issuers":        namespacedIssuerList,
	}}
	issuers, err := newIssuerService(t, fake).ListCertificateIssuers("opute-ha-a", "")
	if err != nil {
		t.Fatalf("ListCertificateIssuers: %v", err)
	}
	if len(issuers) != 3 {
		t.Fatalf("expected 3 issuers, got %d: %v", len(issuers), issuers)
	}
	first := issuers[0]
	if first["name"] != "letsencrypt-prod" || first["kind"] != "ClusterIssuer" {
		t.Fatalf("unexpected first issuer: %v", first)
	}
	if first["ready"] != true {
		t.Fatalf("expected letsencrypt-prod ready, got %v", first["ready"])
	}
	if first["type"] != "acme" {
		t.Fatalf("expected acme issuer type, got %v", first["type"])
	}
	if issuers[1]["ready"] != false {
		t.Fatalf("expected selfsigned not ready, got %v", issuers[1]["ready"])
	}
	if issuers[2]["kind"] != "Issuer" || issuers[2]["namespace"] != "opute-platform" {
		t.Fatalf("unexpected namespaced issuer: %v", issuers[2])
	}
}

// A namespace narrows the question, and a ClusterIssuer does not live in one.
func TestListCertificateIssuersWithNamespaceSkipsClusterScopedKind(t *testing.T) {
	fake := &issuerListFake{responses: map[string]string{
		"clusterissuers": clusterIssuerList,
		"issuers":        namespacedIssuerList,
	}}
	issuers, err := newIssuerService(t, fake).ListCertificateIssuers("opute-ha-a", "opute-platform")
	if err != nil {
		t.Fatalf("ListCertificateIssuers: %v", err)
	}
	if len(issuers) != 1 || issuers[0]["kind"] != "Issuer" {
		t.Fatalf("expected only the namespaced Issuer, got %v", issuers)
	}
	for _, args := range fake.calls {
		if len(args) >= 2 && args[1] == "clusterissuers" {
			t.Fatalf("cluster-scoped kind was queried for a namespaced request: %v", fake.calls)
		}
	}
	joined := strings.Join(fake.calls[0], " ")
	if !strings.Contains(joined, "-n opute-platform") {
		t.Fatalf("expected the namespace to be passed through, got %q", joined)
	}
}

// cert-manager is optional: without it the CRDs do not exist, and that is an
// empty answer rather than an error the operator has to interpret.
func TestListCertificateIssuersTreatsAbsentCertManagerAsEmpty(t *testing.T) {
	fake := &issuerListFake{errs: map[string]error{
		"clusterissuers": errors.New(`failed to list clusterissuers in opute-ha-a: error: the server doesn't have a resource type "clusterissuers"`),
		"issuers":        errors.New(`failed to list issuers in opute-ha-a: error: the server doesn't have a resource type "issuers"`),
	}}
	issuers, err := newIssuerService(t, fake).ListCertificateIssuers("opute-ha-a", "")
	if err != nil {
		t.Fatalf("expected an absent CRD to read as empty, got %v", err)
	}
	if len(issuers) != 0 {
		t.Fatalf("expected no issuers, got %v", issuers)
	}
}

// Anything else is still a failure: a cluster that cannot be reached must not
// be reported as a cluster with no issuers.
func TestListCertificateIssuersPropagatesRealFailures(t *testing.T) {
	fake := &issuerListFake{errs: map[string]error{
		"clusterissuers": errors.New("failed to list clusterissuers in opute-ha-a: connection refused"),
	}}
	if _, err := newIssuerService(t, fake).ListCertificateIssuers("opute-ha-a", ""); err == nil {
		t.Fatal("expected a connection failure to propagate")
	}
}
