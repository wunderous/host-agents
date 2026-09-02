package host

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeHTTPEndpointAcceptsAuthenticationChallengeForReachability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	service := &Service{}
	withoutChallenge, err := service.ProbeHTTPEndpoint(t.Context(), ProbeHTTPEndpointArgs{Endpoint: server.URL})
	if err != nil {
		t.Fatalf("probe without challenge allowance: %v", err)
	}
	if withoutChallenge.Ready {
		t.Fatal("unauthenticated challenge should not be ready without explicit allowance")
	}

	withChallenge, err := service.ProbeHTTPEndpoint(t.Context(), ProbeHTTPEndpointArgs{
		Endpoint:                      server.URL,
		AcceptAuthenticationChallenge: true,
	})
	if err != nil {
		t.Fatalf("probe with challenge allowance: %v", err)
	}
	if !withChallenge.Ready || withChallenge.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected reachable authentication challenge, got %#v", withChallenge)
	}
}
