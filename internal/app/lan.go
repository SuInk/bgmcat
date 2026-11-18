package app

import (
	"context"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

type lanServerEntry struct {
	listener net.Listener
	server   *http.Server
}

func isTailscaleCGNAT(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	// 100.64.0.0/10 (Tailscale / ZeroTier / WireGuard overlay networks)
	return ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}

func lanIPPriority(ip string) int {
	switch {
	case strings.HasPrefix(ip, "192.168."):
		return 0
	case strings.HasPrefix(ip, "10."):
		return 1
	case strings.HasPrefix(ip, "172."):
		return 2
	case strings.HasPrefix(ip, "100."):
		return 3
	default:
		return 4
	}
}

func privateLANIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var ips []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			if !ip4.IsPrivate() && !isTailscaleCGNAT(ip4) {
				continue
			}
			text := ip4.String()
			if _, exists := seen[text]; !exists {
				seen[text] = struct{}{}
				ips = append(ips, text)
			}
		}
	}
	sort.Slice(ips, func(i, j int) bool {
		pi, pj := lanIPPriority(ips[i]), lanIPPriority(ips[j])
		if pi != pj {
			return pi < pj
		}
		return ips[i] < ips[j]
	})
	return ips
}

func isWildcardBindHost(host string) bool {
	h := strings.TrimSpace(host)
	return h == "" || h == "0.0.0.0" || h == "::" || h == "[::]"
}

func (a *App) syncLANListeners() {
	host, port, err := net.SplitHostPort(a.cfg.Addr)
	if err != nil || port == "" || port == "0" {
		return
	}

	a.lanMu.Lock()
	defer a.lanMu.Unlock()

	if a.lanServers == nil {
		a.lanServers = map[string]*lanServerEntry{}
	}

	enabled := flag(a.Settings()["lanAccessEnabled"])
	if !enabled || isWildcardBindHost(host) || a.ctx.Err() != nil {
		for addr, entry := range a.lanServers {
			delete(a.lanServers, addr)
			go shutdownLANEntry(entry)
		}
		return
	}

	desired := map[string]struct{}{}
	for _, ip := range privateLANIPv4s() {
		if ip == host {
			continue
		}
		desired[net.JoinHostPort(ip, port)] = struct{}{}
	}

	for addr, entry := range a.lanServers {
		if _, keep := desired[addr]; !keep {
			delete(a.lanServers, addr)
			go shutdownLANEntry(entry)
		}
	}

	handler := a.Handler()
	for addr := range desired {
		if _, exists := a.lanServers[addr]; exists {
			continue
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		srv := &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 15 * time.Second,
			IdleTimeout:       90 * time.Second,
		}
		a.lanServers[addr] = &lanServerEntry{listener: ln, server: srv}
		go func(s *http.Server, l net.Listener) {
			_ = s.Serve(l)
		}(srv, ln)
	}
}

func shutdownLANEntry(entry *lanServerEntry) {
	if entry == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = entry.server.Shutdown(ctx)
	_ = entry.listener.Close()
}

func (a *App) closeLANListeners() {
	a.lanMu.Lock()
	entries := make([]*lanServerEntry, 0, len(a.lanServers))
	for addr, entry := range a.lanServers {
		delete(a.lanServers, addr)
		entries = append(entries, entry)
	}
	a.lanMu.Unlock()
	for _, entry := range entries {
		shutdownLANEntry(entry)
	}
}

func (a *App) lanURLs() []string {
	host, port, err := net.SplitHostPort(a.cfg.Addr)
	if err != nil || port == "" || port == "0" {
		return []string{}
	}
	if isWildcardBindHost(host) {
		ips := privateLANIPv4s()
		urls := make([]string, 0, len(ips))
		for _, ip := range ips {
			urls = append(urls, "http://"+net.JoinHostPort(ip, port))
		}
		return urls
	}
	a.lanMu.Lock()
	addrs := make([]string, 0, len(a.lanServers))
	for addr := range a.lanServers {
		addrs = append(addrs, addr)
	}
	a.lanMu.Unlock()
	sort.Slice(addrs, func(i, j int) bool {
		hi, _, _ := net.SplitHostPort(addrs[i])
		hj, _, _ := net.SplitHostPort(addrs[j])
		pi, pj := lanIPPriority(hi), lanIPPriority(hj)
		if pi != pj {
			return pi < pj
		}
		return addrs[i] < addrs[j]
	})
	urls := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		urls = append(urls, "http://"+addr)
	}
	return urls
}
