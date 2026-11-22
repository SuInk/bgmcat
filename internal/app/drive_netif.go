package app

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type driveNetInterface struct {
	Name  string
	Index int
	IPv4  string
	IPv6  string
}

var (
	driveNetifMu     sync.Mutex
	driveNetifCached *driveNetInterface
	driveNetifAt     time.Time
)

var virtualInterfacePrefixes = []string{
	"utun", "tun", "tap", "lo", "bridge", "awdl", "llw", "anpi", "ap",
	"gif", "stf", "xhc", "pktap", "ipsec", "ppp", "wg", "tailscale",
	"zt", "docker", "veth", "br-", "virbr", "vmnet", "vboxnet",
	"clash", "sing", "mihomo", "meta", "wintun",
}

func isVirtualInterfaceName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return true
	}
	for _, prefix := range virtualInterfacePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func forgetDriveInterface() {
	driveNetifMu.Lock()
	driveNetifCached = nil
	driveNetifAt = time.Time{}
	driveNetifMu.Unlock()
}

func detectDriveInterface() *driveNetInterface {
	driveNetifMu.Lock()
	defer driveNetifMu.Unlock()
	if time.Since(driveNetifAt) < 30*time.Second && driveNetifCached != nil {
		cp := *driveNetifCached
		return &cp
	}
	detected := findPhysicalInterface()
	driveNetifCached = detected
	driveNetifAt = time.Now()
	if detected == nil {
		return nil
	}
	cp := *detected
	return &cp
}

func findPhysicalInterface() *driveNetInterface {
	if name := routeDefaultInterfaceName(); name != "" && !isVirtualInterfaceName(name) {
		if iface, err := net.InterfaceByName(name); err == nil {
			if ip4, ip6 := interfaceGlobalIPs(iface); ip4 != "" {
				return &driveNetInterface{Name: iface.Name, Index: iface.Index, IPv4: ip4, IPv6: ip6}
			}
		}
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var fallback *driveNetInterface
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		if isVirtualInterfaceName(iface.Name) {
			continue
		}
		ip4, ip6 := interfaceGlobalIPs(&iface)
		if ip4 == "" {
			continue
		}
		candidate := &driveNetInterface{Name: iface.Name, Index: iface.Index, IPv4: ip4, IPv6: ip6}
		if strings.HasPrefix(iface.Name, "en") || strings.HasPrefix(iface.Name, "eth") || strings.HasPrefix(iface.Name, "wl") {
			return candidate
		}
		if fallback == nil {
			fallback = candidate
		}
	}
	return fallback
}

func interfaceHasGlobalIPv4(iface *net.Interface) bool {
	ip4, _ := interfaceGlobalIPs(iface)
	return ip4 != ""
}

func interfaceGlobalIPs(iface *net.Interface) (ipv4, ipv6 string) {
	if iface == nil {
		return "", ""
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", ""
	}
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if len(ip) == 0 || ip.IsLoopback() || ip.IsLinkLocalUnicast() || !ip.IsGlobalUnicast() {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil {
			if ipv4 == "" {
				ipv4 = ip4.String()
			}
			continue
		}
		if ipv6 == "" && !ip.IsPrivate() {
			ipv6 = ip.String()
		}
	}
	return ipv4, ipv6
}

func isDirectRoutablePeerAddr(address string) bool {
	host := strings.TrimSpace(address)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" || strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Domain names (such as HTTP/UDP tracker hostnames) should be routed via the physical interface.
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	// Fake-IP ranges (198.18.0.0/15) used by Clash/Mihomo/Sing-box TUN should not be bound to physical NIC
	// if a literal 198.18.x.x IP is dialed.
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 198 && (ip4[1]&0xfe) == 18 {
		return false
	}
	return true
}

func newBTDialer(timeout time.Duration) *net.Dialer {
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	}
	bindAnyPublicDialerToPhysicalInterface(dialer)
	return dialer
}

func dialBTContext(ctx context.Context, network, addr string) (net.Conn, error) {
	physicalDialer := newBTDialer(10 * time.Second)
	conn, err := physicalDialer.DialContext(ctx, network, addr)
	if err == nil || ctx.Err() != nil {
		return conn, err
	}
	fallbackDialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return fallbackDialer.DialContext(ctx, network, addr)
}

func listenBTPacket(network, addr string) (net.PacketConn, error) {
	var lc net.ListenConfig
	bindListenConfigToPhysicalInterface(&lc)
	pc, err := lc.ListenPacket(context.Background(), network, addr)
	if err == nil {
		return pc, nil
	}
	return net.ListenPacket(network, addr)
}

func physicalBTListenHost(network string) string {
	iface := detectDriveInterface()
	if iface == nil {
		return ""
	}
	if strings.HasSuffix(network, "6") {
		return iface.IPv6
	}
	if strings.HasSuffix(network, "4") && iface.IPv4 != "" {
		return iface.IPv4
	}
	return ""
}

func routeDefaultInterfaceName() string {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "route", "-n", "get", "1.1.1.1").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "interface:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
