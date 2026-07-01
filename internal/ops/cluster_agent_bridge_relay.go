package ops

import (
	"fmt"
	"net"
	"runtime"
	"strings"
)

func bridgeURLHost(bridgeURL string) string {
	parsed := strings.TrimSpace(bridgeURL)
	if parsed == "" {
		return ""
	}
	if !strings.HasPrefix(parsed, "http://") && !strings.HasPrefix(parsed, "https://") {
		parsed = "http://" + parsed
	}
	hostPort := strings.TrimPrefix(strings.TrimPrefix(parsed, "https://"), "http://")
	host := hostPort
	if idx := strings.Index(hostPort, "/"); idx >= 0 {
		host = hostPort[:idx]
	}
	if idx := strings.Index(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	return strings.Trim(host, "[]")
}

func resolveHyperVDefaultSwitchIPv4() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if !strings.Contains(iface.Name, "Default Switch") {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			return ipnet.IP.String()
		}
	}
	return ""
}

func (s *HostOperationsService) ensureGuestBridgeRelay(listenHost string, port int, onData func(string)) error {
	listenHost = strings.TrimSpace(listenHost)
	if listenHost == "" || port <= 0 {
		return fmt.Errorf("invalid guest bridge relay listen address")
	}
	if s.guestBridgeRelay == nil {
		return fmt.Errorf("guest bridge relay is not configured")
	}

	sessionID := fmt.Sprintf("guest-bridge:%s:%d", listenHost, port)
	_, err := s.guestBridgeRelay.startRelay(sessionID, listenHost, port, "127.0.0.1", port)
	if err == nil {
		if onData != nil {
			onData(fmt.Sprintf("guest bridge relay listening on %s:%d -> 127.0.0.1:%d", listenHost, port, port))
		}
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "already active") || strings.Contains(msg, "already in use") {
		return nil
	}
	return err
}
