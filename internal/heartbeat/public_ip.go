package heartbeat

import (
	"net"
	"runtime"
)

// PrimaryLANIPv4 returns the primary non-loopback IPv4 address for heartbeat metadata.
func PrimaryLANIPv4() string {
	if runtime.GOOS == "windows" {
		return primaryLANIPv4Windows()
	}
	return primaryLANIPv4Unix()
}

// IncusBridgeIPv4 returns the first usable address on the Incus bridge. It is
// published as host metadata so a gateway VM can be admitted to the narrow
// relay listener without binding the relay to every host interface.
func IncusBridgeIPv4() string {
	iface, err := net.InterfaceByName("incusbr0")
	if err != nil || iface.Flags&net.FlagUp == 0 {
		return ""
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP.To4()
		if ip != nil && !ip.IsLoopback() {
			return ip.String()
		}
	}
	return ""
}

func primaryLANIPv4Unix() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil || ipNet.IP.IsLoopback() {
				continue
			}
			return ipNet.IP.String()
		}
	}
	return ""
}

func primaryLANIPv4Windows() string {
	// Same interface walk works on Windows; dedicated hook for test stubs.
	return primaryLANIPv4Unix()
}
