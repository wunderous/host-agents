package ops

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/wunderous/host-agents/internal/heartbeat"
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

func readGuestBridgeListenHost() string {
	if host := strings.TrimSpace(os.Getenv("OPUTE_PLATFORM_GUEST_HOST")); host != "" {
		return host
	}
	if bridge := heartbeat.IncusBridgeIPv4(); bridge != "" {
		return bridge
	}
	return strings.TrimSpace(heartbeat.PrimaryLANIPv4())
}

// resolveCpcContainerBridgeIPv4 returns the Incus bridge IPv4 for a CPC system container
// (e.g. opute-clean-k3s @ 10.0.100.131). QEMU guests reach platform hostPort on that IP,
// not the host gateway at 10.0.100.1 where the guest-bridge relay may be unreachable.
func (s *HostOperationsService) resolveCpcContainerBridgeIPv4() string {
	containerName := discoverCpcContainerName()
	if containerName == "" {
		return ""
	}
	instanceType, err := s.readIncusInstanceType(containerName)
	if err != nil || !strings.EqualFold(instanceType, "container") {
		return ""
	}
	info, err := s.GetVMInfo(containerName, true)
	if err != nil {
		return ""
	}
	return firstBridgeIPv4(info.IPv4)
}

func (s *HostOperationsService) resolveGuestBridgeListenHost() string {
	if host := strings.TrimSpace(os.Getenv("OPUTE_PLATFORM_GUEST_HOST")); host != "" {
		return host
	}
	if ip := s.resolveCpcContainerBridgeIPv4(); ip != "" {
		return ip
	}
	return readGuestBridgeListenHost()
}

func discoverCpcContainerName() string {
	for _, key := range []string{"OPUTE_K3S_VM", "OPUTE_DOGFOOD_GATEWAY_VM", "OPUTE_K3S_CONTAINER"} {
		if name := strings.TrimSpace(os.Getenv(key)); name != "" {
			return name
		}
	}
	return ""
}

func (s *HostOperationsService) ensureCpcContainerGuestBridgeProxy(containerName, listenHost string, port int, onData func(string)) error {
	containerName = strings.TrimSpace(containerName)
	listenHost = strings.TrimSpace(listenHost)
	if containerName == "" || listenHost == "" || port <= 0 {
		return fmt.Errorf("invalid CPC guest bridge proxy target")
	}
	instanceType, err := s.readIncusInstanceType(containerName)
	if err != nil {
		return err
	}
	if !strings.EqualFold(instanceType, "container") {
		return fmt.Errorf("instance %q is %q, not a system container", containerName, instanceType)
	}
	deviceName := fmt.Sprintf("platform-bridge-%d", port)
	proxy := fmt.Sprintf("listen=tcp:%s:%d,connect=tcp:127.0.0.1:%d", listenHost, port, port)
	if onData != nil {
		onData(fmt.Sprintf("ensuring CPC guest bridge proxy on %s (%s:%d -> 127.0.0.1:%d)", containerName, listenHost, port, port))
	}
	return s.ensureIncusDevice(containerName, deviceName, []string{
		"config", "device", "add", containerName, deviceName, "proxy", proxy,
	})
}

func (s *HostOperationsService) ensureGuestBridgeReachability(bridgeURL string, bridgePort int, onData func(string)) error {
	port := bridgePort
	if port <= 0 {
		port = defaultBridgePort()
	}
	listenHost := s.resolveGuestBridgeListenHost()
	if listenHost == "" {
		return fmt.Errorf("guest bridge listen host is unavailable")
	}

	if containerName := discoverCpcContainerName(); containerName != "" {
		if err := s.ensureCpcContainerGuestBridgeProxy(containerName, listenHost, port, onData); err == nil {
			return nil
		} else if onData != nil {
			onData(fmt.Sprintf("CPC container guest bridge proxy unavailable: %v", err))
		}
	}

	configuredHost := bridgeURLHost(bridgeURL)
	if configuredHost != "" && !isLoopbackBridgeURL(bridgeURL) && configuredHost != listenHost {
		targetHost := configuredHost
		targetPort := port
		if s.guestBridgeRelay == nil {
			return fmt.Errorf("guest bridge relay is not configured")
		}
		sessionID := fmt.Sprintf("guest-bridge:%s:%d->%s:%d", listenHost, port, targetHost, targetPort)
		_, err := s.guestBridgeRelay.startRelay(sessionID, listenHost, port, targetHost, targetPort)
		if err == nil {
			if onData != nil {
				onData(fmt.Sprintf("guest bridge relay listening on %s:%d -> %s:%d", listenHost, port, targetHost, targetPort))
			}
			return nil
		}
		msg := err.Error()
		if strings.Contains(msg, "already active") || strings.Contains(msg, "already in use") {
			return nil
		}
		return err
	}

	return s.ensureGuestBridgeRelay(listenHost, port, onData)
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
