//go:build !darwin && !linux

package app

import "net"

func bindDialerToPhysicalInterface(dialer *net.Dialer, targetIP string) {}
