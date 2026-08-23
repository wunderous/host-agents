package ops

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ProbeHTTPEndpointArgs struct {
	Endpoint string
}

// HTTPObservation is a provider-neutral readiness observation for an
// explicitly declared public or host-local HTTP endpoint.
type HTTPObservation struct {
	Endpoint   string `json:"endpoint"`
	StatusCode int    `json:"statusCode,omitempty"`
	Ready      bool   `json:"ready"`
	Error      string `json:"error,omitempty"`
}

func (s *HostOperationsService) ProbeHTTPEndpoint(ctx context.Context, args ProbeHTTPEndpointArgs) (*HTTPObservation, error) {
	endpoint := strings.TrimSpace(args.Endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || strings.ContainsAny(endpoint, "\r\n\x00") {
		return nil, fmt.Errorf("endpoint must be an absolute HTTP(S) URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		// Some WSL installations resolve public names to IPv6 first while the
		// kernel has no usable IPv6 route. Retry read-only readiness probes over
		// IPv4 so a reachable public endpoint is not reported unavailable solely
		// because of the host's address-family configuration.
		fallback := &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp4", address)
			}},
		}
		response, err = fallback.Do(request)
		if err != nil {
			return &HTTPObservation{Endpoint: endpoint, Error: err.Error()}, nil
		}
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	return &HTTPObservation{Endpoint: endpoint, StatusCode: response.StatusCode, Ready: response.StatusCode >= 200 && response.StatusCode < 400}, nil
}
