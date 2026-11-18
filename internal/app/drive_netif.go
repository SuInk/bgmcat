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
		if iface, err := net.InterfaceByName(name); err == nil && interfaceHasGlobalIPv4(iface) {
			return &driveNetInterface{Name: iface.Name, Index: iface.Index}
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
		if isVirtualInterfaceName(iface.Name) || !interfaceHasGlobalIPv4(&iface) {
			continue
		}
		candidate := &driveNetInterface{Name: iface.Name, Index: iface.Index}
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
	if iface == nil {
		return false
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip4 := ip.To4(); ip4 != nil && ip4.IsGlobalUnicast() && !ip4.IsLoopback() && !ip4.IsLinkLocalUnicast() {
			return true
		}
	}
	return false
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
