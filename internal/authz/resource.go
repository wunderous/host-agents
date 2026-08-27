package authz

import (
	"net"
	"net/http"
	"strings"
)

func CanonicalMCPResource(r *http.Request) string {
	if r == nil {
		return ""
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" && r.URL != nil {
		host = strings.TrimSpace(r.URL.Host)
	}
	return scheme + "://" + host + "/mcp"
}

func IsLoopbackHost(host string) bool {
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	hostname = strings.Trim(hostname, "[]")
	if strings.EqualFold(hostname, "localhost") || hostname == "127.0.0.1" || hostname == "::1" {
		return true
	}
	ip := net.ParseIP(hostname)
	if ip != nil && ip.IsLoopback() {
		return true
	}
	if ip != nil {
		if ifaces, err := net.InterfaceAddrs(); err == nil {
			for _, addr := range ifaces {
				if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.Equal(ip) {
					return true
				}
			}
		}
	}
	return false
}

func OriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if IsLoopbackHost(r.Host) {
		return originIsLoopback(origin)
	}
	return originMatchesRequestHost(origin, r.Host, requestScheme(r))
}

func originIsLoopback(origin string) bool {
	origin = strings.TrimSuffix(origin, "/")
	for _, allowed := range []string{
		"http://127.0.0.1", "https://127.0.0.1",
		"http://localhost", "https://localhost",
		"http://[::1]", "https://[::1]",
	} {
		if origin == allowed || strings.HasPrefix(origin, allowed+":") {
			return true
		}
	}
	return false
}

func originMatchesRequestHost(origin, host, scheme string) bool {
	origin = strings.TrimSuffix(origin, "/")
	want := scheme + "://" + host
	if i := strings.Index(host, ":"); i >= 0 {
		hostname := host[:i]
		if origin == scheme+"://"+hostname || origin == want {
			return true
		}
	}
	return origin == want
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return "https"
	}
	return "http"
}

func ProtectedResourceMetadataPath(r *http.Request) string {
	return "/.well-known/oauth-protected-resource/mcp"
}

func AuthorizationServerMetadataPath() string {
	return "/.well-known/oauth-authorization-server"
}
