//go:build !darwin && !linux

package app

import "net"

func bindDialerToPhysicalInterface(dialer *net.Dialer, targetIP string) {}

func bindAnyPublicDialerToPhysicalInterface(dialer *net.Dialer) {}

func bindListenConfigToPhysicalInterface(lc *net.ListenConfig) {}
