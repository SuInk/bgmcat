//go:build linux

package app

import (
	"net"
	"syscall"
)

func bindDialerToPhysicalInterface(dialer *net.Dialer, targetIP string) {
	if !publicDriveIP(targetIP) {
		return
	}
	iface := detectDriveInterface()
	if iface == nil || iface.Name == "" {
		return
	}
	name := iface.Name
	dialer.Control = func(network, address string, c syscall.RawConn) error {
		_ = c.Control(func(fd uintptr) {
			_ = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, name)
		})
		return nil
	}
}
