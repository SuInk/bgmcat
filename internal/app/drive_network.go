package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const driveSeedIP = "183.56.143.147"

var builtinDriveSeedIPs = []string{
	driveSeedIP,
	"142.251.41.132",
	"142.251.41.113",
	"142.250.196.100",
	"142.250.196.113",
	"142.251.42.100",
	"172.217.160.100",
	"142.250.66.100",
	"172.253.118.100",
	"173.194.202.100",
	"74.125.204.100",
}

// Host offsets within a /24 where Google's general frontends (serving Drive API
// and usercontent downloads) sit. Derived from mizorewww/gdirect.
var driveFrontendOffsets = []byte{100, 113, 155, 132, 138, 91, 123}

// Google's core frontend IPv4 CIDR blocks where general GFE edges live.
var driveCorePrefixes = []netip.Prefix{
	netip.MustParsePrefix("142.250.0.0/15"),
	netip.MustParsePrefix("172.217.0.0/16"),
	netip.MustParsePrefix("172.253.0.0/16"),
	netip.MustParsePrefix("173.194.0.0/16"),
	netip.MustParsePrefix("74.125.0.0/16"),
	netip.MustParsePrefix("192.178.0.0/15"),
	netip.MustParsePrefix("64.233.160.0/19"),
	netip.MustParsePrefix("66.102.0.0/20"),
	netip.MustParsePrefix("108.177.0.0/17"),
	netip.MustParsePrefix("209.85.128.0/17"),
	netip.MustParsePrefix("216.58.192.0/19"),
	netip.MustParsePrefix("216.239.32.0/19"),
	netip.MustParsePrefix("108.170.192.0/18"),
	netip.MustParsePrefix("72.14.192.0/18"),
}

var driveIPv6Prefixes = []netip.Prefix{
	netip.MustParsePrefix("2001:4860::/32"),
	netip.MustParsePrefix("2404:6800::/32"),
	netip.MustParsePrefix("2607:f8b0::/32"),
	netip.MustParsePrefix("2800:3f0::/32"),
	netip.MustParsePrefix("2a00:1450::/32"),
	netip.MustParsePrefix("2c0f:fb50::/32"),
}

var blockedDrivePrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func publicDriveIP(value string) bool {
	ip, err := netip.ParseAddr(value)
	if err != nil || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range blockedDrivePrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// googleDriveIP checks whether an IP falls within Google's frontend address space,
// filtering out DNS-poisoned answers (such as sinkhole IPs returned by ISP resolvers).
func googleDriveIP(value string) bool {
	if !publicDriveIP(value) {
		return false
	}
	ip, err := netip.ParseAddr(value)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if ip.Is4() {
		for _, prefix := range driveCorePrefixes {
			if prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
	for _, prefix := range driveIPv6Prefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// driveFrontendNeighbours returns the other known Google Frontend host offsets
// in the same /24 subnet as value.
func driveFrontendNeighbours(value string) []string {
	ip, err := netip.ParseAddr(value)
	if err != nil {
		return nil
	}
	ip = ip.Unmap()
	if !ip.Is4() {
		return nil
	}
	b := ip.As4()
	out := make([]string, 0, len(driveFrontendOffsets))
	for _, offset := range driveFrontendOffsets {
		if b[3] == offset {
			continue
		}
		cand := netip.AddrFrom4([4]byte{b[0], b[1], b[2], offset}).String()
		out = append(out, cand)
	}
	return out
}

func sampleDriveFrontendIPs(subnetLimit int, exclude map[string]bool) []string {
	var subnets [][3]byte
	for _, prefix := range driveCorePrefixes {
		addr := prefix.Masked().Addr().As4()
		bits := prefix.Bits()
		if bits > 24 {
			continue
		}
		count := 1 << (24 - bits)
		base := (uint32(addr[0]) << 16) | (uint32(addr[1]) << 8) | uint32(addr[2])
		for i := 0; i < count; i++ {
			cur := base + uint32(i)
			subnets = append(subnets, [3]byte{byte(cur >> 16), byte(cur >> 8), byte(cur)})
		}
	}
	rand.Shuffle(len(subnets), func(i, j int) {
		subnets[i], subnets[j] = subnets[j], subnets[i]
	})
	if subnetLimit > 0 && len(subnets) > subnetLimit {
		subnets = subnets[:subnetLimit]
	}
	out := make([]string, 0, len(subnets)*len(driveFrontendOffsets))
	for _, s := range subnets {
		for _, offset := range driveFrontendOffsets {
			ip := netip.AddrFrom4([4]byte{s[0], s[1], s[2], offset}).String()
			if exclude != nil && exclude[ip] {
				continue
			}
			out = append(out, ip)
		}
	}
	return out
}

func driveBypassTun(state Obj) bool {
	if state == nil || state["bypassTun"] == nil {
		return true
	}
	return flag(state["bypassTun"])
}

// Pin the TCP destination. Optional SNI policy is independent of certificate identity.
// OAuth uses a.http unchanged.
func driveIPTransport(ip string, policies ...driveTLSPolicy) *http.Transport {
	return driveIPTransportWithOptions(ip, false, policies...)
}

func hostIPTransportWithOptions(ip string, allowedHosts map[string]bool, forgedFallbackHost string, bypassTun bool, policies ...driveTLSPolicy) *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.MaxIdleConns = 32
	tr.MaxIdleConnsPerHost = 16
	tr.ReadBufferSize = 256 << 10
	tr.WriteBufferSize = 256 << 10
	dialer := &net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}
	if bypassTun {
		bindDialerToPhysicalInterface(dialer, ip)
	}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if port != "443" || !allowedHosts[strings.ToLower(host)] {
			return nil, errors.New("直连仅允许已配置的目标 HTTPS 地址")
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
	}
	tr.TLSHandshakeTimeout = 4 * time.Second
	tr.ResponseHeaderTimeout = 30 * time.Second
	if len(policies) > 0 && policies[0].Mode != "original" {
		configureHostTLSWithBypass(tr, allowedHosts, forgedFallbackHost, bypassTun, policies[0], nil)
	}
	return tr
}

func driveIPTransportWithOptions(ip string, bypassTun bool, policies ...driveTLSPolicy) *http.Transport {
	return hostIPTransportWithOptions(ip, map[string]bool{
		"www.googleapis.com":           true,
		"drive.usercontent.google.com": true,
	}, driveForgedHost, bypassTun, policies...)
}

func tcpSweepDriveIPs(ctx context.Context, ips []string, bypassTun bool) ([]string, bool) {
	if len(ips) == 0 {
		return nil, false
	}
	type hit struct {
		ip        string
		latencyMs int64
	}
	jobs := make(chan string, len(ips))
	for _, ip := range ips {
		jobs <- ip
	}
	close(jobs)
	hits := make(chan hit, len(ips))
	workers := min(128, len(ips))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				if ctx.Err() != nil {
					return
				}
				dialer := &net.Dialer{Timeout: 800 * time.Millisecond}
				if bypassTun {
					bindDialerToPhysicalInterface(dialer, ip)
				}
				started := time.Now()
				conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, "443"))
				if err == nil {
					_ = conn.Close()
					hits <- hit{ip: ip, latencyMs: time.Since(started).Milliseconds()}
				}
			}
		}()
	}
	wg.Wait()
	close(hits)
	collected := make([]hit, 0, len(hits))
	for h := range hits {
		collected = append(collected, h)
	}
	sort.Slice(collected, func(i, j int) bool {
		return collected[i].latencyMs < collected[j].latencyMs
	})
	open := make([]string, 0, len(collected))
	for _, h := range collected {
		open = append(open, h.ip)
	}
	tunIntercepted := len(ips) >= 32 && len(open)*10 >= len(ips)*9
	return open, tunIntercepted
}

func discoverDriveCandidates(ctx context.Context, bypassTun bool, seeds []string) ([]string, bool) {
	seen := map[string]bool{}
	initial := make([]string, 0, 256)
	addInitial := func(ip string) {
		if publicDriveIP(ip) && !seen[ip] {
			seen[ip] = true
			initial = append(initial, ip)
		}
	}
	for _, ip := range seeds {
		addInitial(ip)
		for _, n := range driveFrontendNeighbours(ip) {
			addInitial(n)
		}
	}
	for _, ip := range sampleDriveFrontendIPs(36, seen) {
		addInitial(ip)
	}
	open, tunIntercepted := tcpSweepDriveIPs(ctx, initial, bypassTun)
	for round := 0; round < 2 && len(open) < 12 && !tunIntercepted && ctx.Err() == nil; round++ {
		batch := sampleDriveFrontendIPs(48, seen)
		for _, ip := range batch {
			seen[ip] = true
		}
		more, intercepted := tcpSweepDriveIPs(ctx, batch, bypassTun)
		if intercepted {
			tunIntercepted = true
		}
		open = append(open, more...)
	}
	if len(open) > 0 && !tunIntercepted && ctx.Err() == nil {
		var neighbourBatch []string
		openSet := map[string]bool{}
		for _, ip := range open {
			openSet[ip] = true
		}
		for _, ip := range open {
			for _, n := range driveFrontendNeighbours(ip) {
				if !openSet[n] && !seen[n] && len(neighbourBatch) < 64 {
					seen[n] = true
					neighbourBatch = append(neighbourBatch, n)
				}
			}
		}
		if len(neighbourBatch) > 0 {
			more, _ := tcpSweepDriveIPs(ctx, neighbourBatch, bypassTun)
			open = append(open, more...)
		}
	}
	if len(open) == 0 {
		fallback := make([]string, 0, 16)
		for _, ip := range initial {
			if len(fallback) >= 16 {
				break
			}
			fallback = append(fallback, ip)
		}
		return fallback, tunIntercepted
	}
	if len(open) > 48 {
		open = open[:48]
	}
	return open, tunIntercepted
}

func probeDrive(ctx context.Context, client *http.Client) (string, error) {
	get := func(endpoint string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if err != nil {
			return nil, err
		}
		return client.Do(req)
	}
	res, err := get(driveBase + "/about?fields=user")
	if err != nil {
		return "", fmt.Errorf("Drive API 连接或 TLS 校验失败：%w", err)
	}
	var body struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&body)
	res.Body.Close()
	if err != nil || res.StatusCode != 401 || body.Error.Code != 401 {
		return "", errors.New("Drive API 未返回预期的 401 响应")
	}
	res, err = get("https://drive.usercontent.google.com/download?id=0B9P1L--7Wd2vU3VUVlFnbTgtS2c&export=download&confirm=t")
	if err != nil {
		return "", fmt.Errorf("Drive 下载端点连接或 TLS 校验失败：%w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == 403 || res.StatusCode == 404 {
		return fmt.Sprintf("测试文件 HTTP %d，仅通过 API 验证", res.StatusCode), nil
	}
	if res.StatusCode != 200 {
		return "", fmt.Errorf("下载探测 HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 32))
	if err != nil {
		return "", errors.New("读取测试文件失败")
	}
	if string(data) != "spam\n" {
		return "测试文件内容变化，仅通过 API 验证", nil
	}
	return "", nil
}

func probeDriveEndpoint(ctx context.Context, ip string, policy driveTLSPolicy, bypassTun bool) (string, string, error) {
	tryOnce := func(p driveTLSPolicy) (string, error) {
		tr := driveIPTransportWithOptions(ip, bypassTun, p)
		defer tr.CloseIdleConnections()
		client := &http.Client{
			Transport:     tr,
			Timeout:       5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		return probeDrive(ctx, client)
	}
	if policy.Mode == "original" {
		// Prioritize g.cn + TCP/TLS fragmentation first (fastest TTFB and avoids .google.com QoS),
		// then fall back to fragmented real SNI and dl.google.com.
		if w, err := tryOnce(driveTLSPolicy{Mode: "custom", ServerName: driveForgedHost}); err == nil {
			return w, driveForgedHost, nil
		}
		if ctx.Err() == nil {
			if w2, err2 := tryOnce(driveTLSPolicy{Mode: "fragment"}); err2 == nil {
				return w2, "fragment", nil
			}
		}
		if ctx.Err() == nil {
			if w3, err3 := tryOnce(driveTLSPolicy{Mode: "custom", ServerName: driveSecondaryForgedHost}); err3 == nil {
				return w3, driveSecondaryForgedHost, nil
			}
		}
		return "", "", errors.New("Drive API 连接或 TLS 握手被重置（已尝试 g.cn / dl.google.com 与 TCP/TLS 分片）")
	}
	warning, err := tryOnce(policy)
	if err == nil {
		usedSNI := policy.Mode
		if policy.Mode == "custom" {
			usedSNI = policy.ServerName
		}
		return warning, usedSNI, nil
	}
	return "", "", err
}

func selectedDriveRoute(state Obj, at time.Time) (string, string) {
	if !flag(state["enabled"]) {
		return "", ""
	}
	preferred := str(state["preferredIP"])
	bestIP, bestSNI, latency := "", "", int64(1<<62)
	for _, raw := range list(state["results"]) {
		row := object(raw)
		verified := num(row["verifiedAt"])
		ip := str(row["ip"])
		if !publicDriveIP(ip) || !flag(row["available"]) || verified <= at.Add(-24*time.Hour).Unix() || verified > at.Unix() {
			continue
		}
		if preferred != "" && ip == preferred {
			return ip, str(row["sni"])
		}
		if ms := num(row["latencyMs"]); ms >= 0 && ms < latency {
			bestIP, bestSNI, latency = ip, str(row["sni"]), ms
		}
	}
	return bestIP, bestSNI
}

func selectedDriveIP(state Obj, at time.Time) string {
	ip, _ := selectedDriveRoute(state, at)
	return ip
}

func (a *App) driveNetworkStatus() Obj {
	state, _ := a.Store.Get("config", "drive-network")
	if state == nil {
		state = Obj{"enabled": false, "results": []any{}}
	}
	policy := drivePolicy(state)
	state["sniMode"], state["sniHost"] = policy.Mode, policy.ServerName
	state["bypassTun"] = driveBypassTun(state)
	if iface := detectDriveInterface(); iface != nil {
		state["interface"] = iface.Name
	} else {
		state["interface"] = ""
	}
	ip, sni := selectedDriveRoute(state, time.Now())
	state["selectedIP"] = ip
	state["selectedSNI"] = sni
	state["preferredIP"] = str(state["preferredIP"])
	return state
}

func (a *App) driveTransport() (http.RoundTripper, string) {
	a.driveNetworkMu.Lock()
	defer a.driveNetworkMu.Unlock()
	state := a.driveNetworkStatus()
	ip := str(state["selectedIP"])
	selectedSNI := str(state["selectedSNI"])
	policy := effectiveDrivePolicy(state, selectedSNI)
	bypassTun := driveBypassTun(state)
	if ip == "" {
		return a.http.Transport, ""
	}
	cacheKey := fmt.Sprintf("%s|tun:%v", policy.key(), bypassTun)
	if a.driveIP != ip || a.driveTLSKey != cacheKey {
		if a.driveIPHTTP != nil {
			a.driveIPHTTP.CloseIdleConnections()
		}
		a.driveIP, a.driveIPHTTP = ip, driveIPTransportWithOptions(ip, bypassTun, policy)
		a.driveTLSKey = cacheKey
	}
	return a.driveIPHTTP, ip
}

func (a *App) failDriveIP(ip string) {
	forgetDriveInterface()
	a.driveNetworkMu.Lock()
	defer a.driveNetworkMu.Unlock()
	state := a.driveNetworkStatus()
	for _, raw := range list(state["results"]) {
		row := object(raw)
		if str(row["ip"]) == ip {
			row["available"] = false
			row["error"] = "实际传输连接失败，已自动切换其他可用 IP 或系统网络"
		}
	}
	if a.driveIP == ip {
		if a.driveIPHTTP != nil {
			a.driveIPHTTP.CloseIdleConnections()
		}
		a.driveIP = ""
		a.driveTLSKey = ""
		a.driveIPHTTP = nil
	}
	_ = a.Store.Put("config", "drive-network", state)
}

func scanDriveIPs(ctx context.Context, ips []string, probe func(context.Context, string) (string, error)) []Obj {
	return scanDriveEndpoints(ctx, ips, 0, func(ctx context.Context, ip string) (string, string, error) {
		warning, err := probe(ctx, ip)
		return warning, "", err
	})
}

func scanDriveEndpoints(ctx context.Context, ips []string, target int, probe func(context.Context, string) (string, string, error)) []Obj {
	jobs := make(chan string, len(ips))
	results := make(chan Obj, len(ips))
	for _, ip := range ips {
		jobs <- ip
	}
	close(jobs)
	var verifiedCount atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				if ctx.Err() != nil {
					return
				}
				if target > 0 && int(verifiedCount.Load()) >= target {
					return
				}
				started := time.Now()
				warning, usedSNI, err := probe(ctx, ip)
				row := Obj{
					"ip":         ip,
					"available":  err == nil,
					"latencyMs":  time.Since(started).Milliseconds(),
					"verifiedAt": int64(0),
					"warning":    warning,
					"error":      "",
					"sni":        usedSNI,
				}
				if err != nil {
					row["error"] = err.Error()
				} else {
					row["verifiedAt"] = time.Now().Unix()
					verifiedCount.Add(1)
				}
				results <- row
			}
		}()
	}
	wg.Wait()
	close(results)
	rows := []Obj{}
	for row := range results {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if flag(rows[i]["available"]) != flag(rows[j]["available"]) {
			return flag(rows[i]["available"])
		}
		return num(rows[i]["latencyMs"]) < num(rows[j]["latencyMs"])
	})
	return rows
}

func (a *App) driveNetworkRoutes() {
	a.handle("GET /api/cloud/network", func(w http.ResponseWriter, r *http.Request) error { ok(w, a.driveNetworkStatus()); return nil })
	a.handle("PATCH /api/cloud/network", func(w http.ResponseWriter, r *http.Request) error {
		var input struct {
			Enabled     *bool   `json:"enabled"`
			BypassTun   *bool   `json:"bypassTun"`
			SNIMode     *string `json:"sniMode"`
			SNIHost     *string `json:"sniHost"`
			PreferredIP *string `json:"preferredIP"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&input); err != nil {
			return err
		}
		a.driveNetworkMu.Lock()
		defer a.driveNetworkMu.Unlock()
		state := a.driveNetworkStatus()
		oldPolicy := drivePolicy(state)
		if input.Enabled != nil {
			state["enabled"] = *input.Enabled
		}
		if input.BypassTun != nil {
			state["bypassTun"] = *input.BypassTun
			forgetDriveInterface()
		}
		if input.SNIMode != nil {
			state["sniMode"] = *input.SNIMode
		}
		if input.SNIHost != nil {
			state["sniHost"] = strings.ToLower(strings.TrimSpace(*input.SNIHost))
		}
		if input.PreferredIP != nil {
			pref := strings.TrimSpace(*input.PreferredIP)
			if pref != "" && !publicDriveIP(pref) {
				return errors.New("首选节点必须为有效公网 IP")
			}
			if pref != "" {
				pref = netip.MustParseAddr(pref).Unmap().String()
			}
			state["preferredIP"] = pref
		}
		policy := drivePolicy(state)
		if policy.Mode != "custom" {
			state["sniHost"] = ""
			policy.ServerName = ""
		}
		if err := policy.validate(); err != nil {
			return err
		}
		if policy != oldPolicy {
			state["results"] = []any{}
			delete(state, "scannedAt")
			state["candidateCount"] = 0
			state["partial"] = false
		}
		if a.driveIPHTTP != nil {
			a.driveIPHTTP.CloseIdleConnections()
		}
		a.driveIPHTTP = nil
		a.driveIP = ""
		a.driveTLSKey = ""
		if err := a.Store.Put("config", "drive-network", state); err != nil {
			return err
		}
		ok(w, a.driveNetworkStatus())
		return nil
	})
	a.handle("POST /api/cloud/network/scan", func(w http.ResponseWriter, r *http.Request) error {
		if !a.driveScanMu.TryLock() {
			return errors.New("正在扫描，请稍后重试")
		}
		defer a.driveScanMu.Unlock()
		var input struct {
			Candidates string `json:"candidates"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 32768)).Decode(&input); err != nil {
			return err
		}
		forgetDriveInterface()
		candidates := []string{}
		seen := map[string]bool{}
		add := func(ip string) {
			if publicDriveIP(ip) && !seen[ip] && len(candidates) < 256 {
				seen[ip] = true
				candidates = append(candidates, ip)
			}
		}
		for _, line := range strings.Split(input.Candidates, "\n") {
			ip := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
			if ip == "" {
				continue
			}
			if !publicDriveIP(ip) {
				return fmt.Errorf("候选必须为公网 IP：%s", ip)
			}
			normalized := netip.MustParseAddr(ip).Unmap().String()
			if !seen[normalized] && len(candidates) >= 256 {
				return errors.New("最多允许 256 个候选 IP")
			}
			add(normalized)
		}
		if len(strings.Fields(input.Candidates)) > 1024 {
			return errors.New("候选列表过长")
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		stop := context.AfterFunc(a.ctx, cancel)
		defer stop()
		initialState := a.driveNetworkStatus()
		policy := drivePolicy(initialState)
		bypassTun := driveBypassTun(initialState)
		autoDiscovery := len(candidates) == 0
		tunIntercepted := false
		if autoDiscovery {
			var seeds []string
			seedSeen := map[string]bool{}
			addSeed := func(ip string) {
				if publicDriveIP(ip) && !seedSeen[ip] {
					seedSeen[ip] = true
					seeds = append(seeds, ip)
				}
			}
			for _, raw := range list(initialState["results"]) {
				row := object(raw)
				if num(row["verifiedAt"]) > time.Now().Add(-7*24*time.Hour).Unix() {
					addSeed(str(row["ip"]))
				}
			}
			for _, host := range []string{"www.googleapis.com", "drive.usercontent.google.com", "drive.google.com"} {
				dnsCtx, dnsCancel := context.WithTimeout(ctx, 2*time.Second)
				addresses, _ := net.DefaultResolver.LookupIPAddr(dnsCtx, host)
				dnsCancel()
				for _, address := range addresses {
					ipStr := address.IP.String()
					if googleDriveIP(ipStr) {
						addSeed(ipStr)
					}
				}
			}
			for _, seed := range builtinDriveSeedIPs {
				if googleDriveIP(seed) {
					addSeed(seed)
				}
			}
			candidates, tunIntercepted = discoverDriveCandidates(ctx, bypassTun, seeds)
		}
		target := 0
		if autoDiscovery {
			target = 8
		}
		rows := scanDriveEndpoints(ctx, candidates, target, func(ctx context.Context, ip string) (string, string, error) {
			return probeDriveEndpoint(ctx, ip, policy, bypassTun)
		})
		if r.Context().Err() != nil || a.ctx.Err() != nil {
			return errors.New("扫描已取消，保留原结果")
		}
		a.driveNetworkMu.Lock()
		defer a.driveNetworkMu.Unlock()
		state := a.driveNetworkStatus()
		if drivePolicy(state) != policy {
			return errors.New("扫描期间 SNI 设置已更改，请重新扫描")
		}
		state["results"] = rows
		state["scannedAt"] = now()
		state["partial"] = !autoDiscovery && len(rows) < len(candidates)
		state["candidateCount"] = len(candidates)
		if tunIntercepted {
			state["tunWarning"] = "检测到几乎所有随机 IP 均响应 TCP 443，可能被 TUN/透明代理接管，建议开启「绕过 TUN 绑定物理网卡」"
		} else {
			delete(state, "tunWarning")
		}
		if err := a.Store.Put("config", "drive-network", state); err != nil {
			return err
		}
		ok(w, a.driveNetworkStatus())
		return nil
	})
	a.handle("POST /api/cloud/network/probe-one", func(w http.ResponseWriter, r *http.Request) error {
		var input struct {
			IP string `json:"ip"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&input); err != nil {
			return err
		}
		rawIP := strings.TrimSpace(input.IP)
		if !publicDriveIP(rawIP) {
			return errors.New("请输入有效的公网 IP 地址")
		}
		ip := netip.MustParseAddr(rawIP).Unmap().String()
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		initialState := a.driveNetworkStatus()
		policy := drivePolicy(initialState)
		bypassTun := driveBypassTun(initialState)

		var tcpMs int64 = -1
		dialer := &net.Dialer{Timeout: 1500 * time.Millisecond}
		if bypassTun {
			bindDialerToPhysicalInterface(dialer, ip)
		}
		tcpStart := time.Now()
		if conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, "443")); err == nil {
			_ = conn.Close()
			tcpMs = time.Since(tcpStart).Milliseconds()
		}

		started := time.Now()
		warning, usedSNI, probeErr := probeDriveEndpoint(ctx, ip, policy, bypassTun)
		row := Obj{
			"ip":           ip,
			"available":    probeErr == nil,
			"tcpLatencyMs": tcpMs,
			"latencyMs":    time.Since(started).Milliseconds(),
			"verifiedAt":   int64(0),
			"warning":      warning,
			"error":        "",
			"sni":          usedSNI,
		}
		if probeErr != nil {
			row["error"] = probeErr.Error()
		} else {
			row["verifiedAt"] = time.Now().Unix()
		}

		a.driveNetworkMu.Lock()
		defer a.driveNetworkMu.Unlock()
		state := a.driveNetworkStatus()
		existing := list(state["results"])
		updated := make([]Obj, 0, len(existing)+1)
		replaced := false
		for _, item := range existing {
			obj := object(item)
			if str(obj["ip"]) == ip {
				updated = append(updated, row)
				replaced = true
			} else {
				updated = append(updated, obj)
			}
		}
		if !replaced {
			updated = append(updated, row)
		}
		sort.Slice(updated, func(i, j int) bool {
			if flag(updated[i]["available"]) != flag(updated[j]["available"]) {
				return flag(updated[i]["available"])
			}
			return num(updated[i]["latencyMs"]) < num(updated[j]["latencyMs"])
		})
		state["results"] = updated
		if err := a.Store.Put("config", "drive-network", state); err != nil {
			return err
		}
		ok(w, a.driveNetworkStatus())
		return nil
	})
}
