//go:build darwin

package app

import (
	"net"
	"net/netip"
	"strings"
	"syscall"
)

func bindDialerToPhysicalInterface(dialer *net.Dialer, targetIP string) {
	addr, err := netip.ParseAddr(targetIP)
	if err != nil || !publicDriveIP(targetIP) {
		return
	}
	iface := detectDriveInterface()
	if iface == nil || iface.Index <= 0 {
		return
	}
	idx := iface.Index
	isV6 := addr.Unmap().Is6()
	dialer.Control = func(network, address string, c syscall.RawConn) error {
		_ = c.Control(func(fd uintptr) {
			if isV6 || strings.HasSuffix(network, "6") {
				_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_BOUND_IF, idx)
			} else {
				_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_BOUND_IF, idx)
			}
		})
		return nil
	}
}
