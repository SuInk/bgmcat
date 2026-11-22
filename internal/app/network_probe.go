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
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

var cloudflareCorePrefixes = []netip.Prefix{
	netip.MustParsePrefix("104.16.0.0/13"),
	netip.MustParsePrefix("104.24.0.0/14"),
	netip.MustParsePrefix("172.64.0.0/13"),
	netip.MustParsePrefix("162.159.0.0/16"),
	netip.MustParsePrefix("173.245.48.0/20"),
	netip.MustParsePrefix("188.114.96.0/20"),
	netip.MustParsePrefix("198.41.128.0/17"),
}

var builtinCloudflareSeeds = []string{
	"104.21.17.69",
	"172.67.223.55",
	"104.16.132.229",
	"104.18.32.7",
	"172.67.74.226",
	"104.21.48.1",
	"162.159.135.42",
	"172.64.155.209",
	"104.26.10.233",
	"188.114.96.3",
	"188.114.97.3",
}

var cloudflareHostOffsets = []byte{1, 3, 7, 25, 42, 100, 132, 209, 226, 229}
var genericHostOffsets = []byte{1, 2, 10, 20, 50, 100, 132, 200}

type networkProbeTargetSpec struct {
	ID          string
	Name        string
	Group       string
	PrimaryHost string
	MirrorHost  string
	ForgedSNI   string
	Hosts       []string
	ProbePath   string
	ProbeKind   string // "google" | "rss" | "json401" | "https"
	Strategy    string // "google" | "cloudflare" | "doh"
	Custom      bool
	PresetSeeds []string
}

type cachedDirectRoute struct {
	ip       string
	cacheKey string
	tr       *http.Transport
}

func isCloudflareIP(value string) bool {
	ip, err := netip.ParseAddr(value)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if !ip.Is4() {
		return false
	}
	for _, prefix := range cloudflareCorePrefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func samplePrefixIPs(prefixes []netip.Prefix, offsets []byte, subnetLimit int, exclude map[string]bool) []string {
	var subnets [][3]byte
	for _, prefix := range prefixes {
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
	out := make([]string, 0, len(subnets)*len(offsets))
	for _, s := range subnets {
		for _, offset := range offsets {
			ip := netip.AddrFrom4([4]byte{s[0], s[1], s[2], offset}).String()
			if exclude != nil && exclude[ip] {
				continue
			}
			out = append(out, ip)
		}
	}
	return out
}

func subnetNeighboursWithOffsets(value string, offsets []byte) []string {
	ip, err := netip.ParseAddr(value)
	if err != nil {
		return nil
	}
	ip = ip.Unmap()
	if !ip.Is4() {
		return nil
	}
	b := ip.As4()
	out := make([]string, 0, len(offsets))
	for _, offset := range offsets {
		if b[3] == offset {
			continue
		}
		out = append(out, netip.AddrFrom4([4]byte{b[0], b[1], b[2], offset}).String())
	}
	return out
}

func validASCIIHostname(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if len(host) == 0 || len(host) > 253 || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return false
	}
	for _, part := range strings.Split(host, ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, c := range part {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				return false
			}
		}
	}
	return true
}

func hostFromURLOrDefault(rawURL, fallback string) string {
	if u, err := url.Parse(strings.TrimSpace(rawURL)); err == nil && u.Hostname() != "" {
		return strings.ToLower(u.Hostname())
	}
	return fallback
}

func uniqueLowerHosts(hosts ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

func (a *App) customProbeTargetList() []Obj {
	doc, _ := a.Store.Get("config", "network-probe-custom")
	if doc == nil {
		return nil
	}
	raw := list(doc["items"])
	out := make([]Obj, 0, len(raw))
	for _, item := range raw {
		obj := object(item)
		host := strings.ToLower(strings.TrimSpace(str(obj["host"])))
		if validASCIIHostname(host) {
			out = append(out, obj)
		}
	}
	return out
}

func (a *App) networkProbeTargets() []networkProbeTargetSpec {
	mikanHost := hostFromURLOrDefault(a.providerBaseURL("mikan"), "mikanani.me")
	bangumiMoeHost := hostFromURLOrDefault(a.providerBaseURL("bangumimoe"), "bangumi.moe")
	dmhyHost := hostFromURLOrDefault(a.providerBaseURL("dmhy"), "share.dmhy.org")
	nyaaHost := hostFromURLOrDefault(a.providerBaseURL("nyaa"), "nyaa.si")

	googlePresets := []string{
		"142.251.41.132 # Google Frontend (亚太优选)",
		"142.251.41.113 # Google Frontend (亚太优选)",
		"142.250.196.100 # Google Frontend (东京)",
		"142.250.196.113 # Google Frontend (东京)",
		"172.217.160.100 # Google Frontend (台北)",
		"142.250.66.100 # Google Frontend (香港)",
		"172.253.118.100 # Google Frontend",
		"74.125.204.100 # Google Frontend",
	}
	cfPresets := []string{
		"104.21.17.69 # Cloudflare Anycast (Mikan)",
		"172.67.223.55 # Cloudflare Anycast (Mikan)",
		"104.16.132.229 # Cloudflare Anycast",
		"104.18.32.7 # Cloudflare Anycast",
		"172.67.74.226 # Cloudflare Anycast",
		"104.21.48.1 # Cloudflare Anycast",
		"162.159.135.42 # Cloudflare Anycast",
		"172.64.155.209 # Cloudflare Anycast",
		"188.114.96.3 # Cloudflare Anycast",
	}
	nyaaPresets := []string{
		"186.2.163.20 # DDoS-Guard (Nyaa 直连优选)",
	}

	mikanMirror := ""
	if mikanHost == "mikanani.me" {
		mikanMirror = "mikanime.tv"
	}
	nyaaForged := ""
	if nyaaHost == "nyaa.si" {
		nyaaForged = "ddos-guard.net"
	}

	specs := []networkProbeTargetSpec{
		{
			ID:          "google",
			Name:        "Google",
			Group:       "云盘与认证",
			PrimaryHost: "www.googleapis.com",
			Hosts:       []string{"www.googleapis.com", "drive.usercontent.google.com", "oauth2.googleapis.com"},
			ProbePath:   "/drive/v3/about?fields=user",
			ProbeKind:   "google",
			Strategy:    "google",
			PresetSeeds: googlePresets,
		},
		{
			ID:          "onedrive",
			Name:        "Microsoft OneDrive",
			Group:       "云盘与认证",
			PrimaryHost: "graph.microsoft.com",
			Hosts:       []string{"graph.microsoft.com", "login.microsoftonline.com"},
			ProbePath:   "/v1.0/me/drive",
			ProbeKind:   "json401",
			Strategy:    "doh",
		},
		{
			ID:          "mikan",
			Name:        "蜜柑计划",
			Group:       "数据源",
			PrimaryHost: mikanHost,
			MirrorHost:  mikanMirror,
			Hosts:       uniqueLowerHosts(mikanHost, "mikanani.me", "mikanime.tv"),
			ProbePath:   "/RSS/Search?searchstr=1080p",
			ProbeKind:   "rss",
			Strategy:    "cloudflare",
			PresetSeeds: cfPresets,
		},
		{
			ID:          "bangumimoe",
			Name:        "萌番组",
			Group:       "数据源",
			PrimaryHost: bangumiMoeHost,
			Hosts:       uniqueLowerHosts(bangumiMoeHost, "bangumi.moe"),
			ProbePath:   "/rss/latest",
			ProbeKind:   "rss",
			Strategy:    "cloudflare",
			PresetSeeds: cfPresets,
		},
		{
			ID:          "dmhy",
			Name:        "动漫花园",
			Group:       "数据源",
			PrimaryHost: dmhyHost,
			Hosts:       uniqueLowerHosts(dmhyHost, "share.dmhy.org"),
			ProbePath:   "/topics/rss/rss.xml",
			ProbeKind:   "rss",
			Strategy:    "cloudflare",
			PresetSeeds: cfPresets,
		},
		{
			ID:          "nyaa",
			Name:        "Nyaa",
			Group:       "数据源",
			PrimaryHost: nyaaHost,
			ForgedSNI:   nyaaForged,
			Hosts:       uniqueLowerHosts(nyaaHost, "nyaa.si"),
			ProbePath:   "/?page=rss&c=1_2&f=0&q=1080p",
			ProbeKind:   "rss",
			Strategy:    "doh",
			PresetSeeds: nyaaPresets,
		},
		{
			ID:          "tmdb",
			Name:        "TMDB",
			Group:       "数据源",
			PrimaryHost: "api.themoviedb.org",
			Hosts:       []string{"api.themoviedb.org", "image.tmdb.org"},
			ProbePath:   "/3/configuration",
			ProbeKind:   "json401",
			Strategy:    "doh",
		},
	}

	for _, item := range a.customProbeTargetList() {
		host := strings.ToLower(strings.TrimSpace(str(item["host"])))
		name := strings.TrimSpace(str(item["name"]))
		if name == "" {
			name = host
		}
		id := str(item["id"])
		if id == "" {
			id = "custom-" + host
		}
		specs = append(specs, networkProbeTargetSpec{
			ID:          id,
			Name:        name,
			Group:       "自定义域名",
			PrimaryHost: host,
			Hosts:       []string{host},
			ProbePath:   "/",
			ProbeKind:   "https",
			Strategy:    "doh",
			Custom:      true,
			PresetSeeds: cfPresets,
		})
	}
	return specs
}

func (a *App) findNetworkProbeTarget(id string) (networkProbeTargetSpec, bool) {
	id = strings.TrimSpace(id)
	for _, spec := range a.networkProbeTargets() {
		if spec.ID == id {
			return spec, true
		}
	}
	return networkProbeTargetSpec{}, false
}

func networkProbeStoreKey(targetID string) string {
	if targetID == "google" {
		return "drive-network"
	}
	return "network-probe:" + targetID
}

func (a *App) networkTargetStatus(spec networkProbeTargetSpec) Obj {
	if spec.ID == "google" {
		st := a.driveNetworkStatus()
		st["id"] = spec.ID
		st["name"] = spec.Name
		st["group"] = spec.Group
		st["hosts"] = spec.Hosts
		st["custom"] = false
		st["presetSeeds"] = spec.PresetSeeds
		return st
	}
	state, _ := a.Store.Get("config", networkProbeStoreKey(spec.ID))
	if state == nil {
		state = Obj{"enabled": true, "results": []any{}}
	} else if state["enabled"] == nil {
		state["enabled"] = true
	}
	policy := drivePolicy(state)
	state["id"] = spec.ID
	state["name"] = spec.Name
	state["group"] = spec.Group
	state["hosts"] = spec.Hosts
	state["custom"] = spec.Custom
	state["presetSeeds"] = spec.PresetSeeds
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

func resolveDoHIPv4(ctx context.Context, host string, bypassTun bool) []string {
	type dohAnswer struct {
		Type int    `json:"type"`
		Data string `json:"data"`
	}
	type dohResp struct {
		Answer []dohAnswer `json:"Answer"`
	}
	endpoints := []struct {
		ip   string
		host string
		url  string
	}{
		{"223.5.5.5", "dns.alidns.com", "https://223.5.5.5/resolve?name=" + url.QueryEscape(host) + "&type=1"},
		{"1.12.12.12", "doh.pub", "https://1.12.12.12/resolve?name=" + url.QueryEscape(host) + "&type=1"},
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	var out []string
	var wg sync.WaitGroup
	for _, ep := range endpoints {
		ep := ep
		wg.Add(1)
		go func() {
			defer wg.Done()
			qCtx, cancel := context.WithTimeout(ctx, 2200*time.Millisecond)
			defer cancel()
			tr := http.DefaultTransport.(*http.Transport).Clone()
			tr.Proxy = nil
			dialer := &net.Dialer{Timeout: 1500 * time.Millisecond}
			if bypassTun {
				bindDialerToPhysicalInterface(dialer, ep.ip)
			}
			tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, net.JoinHostPort(ep.ip, "443"))
			}
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr, Timeout: 2200 * time.Millisecond}
			req, err := http.NewRequestWithContext(qCtx, "GET", ep.url, nil)
			if err != nil {
				return
			}
			req.Header.Set("Accept", "application/dns-json")
			res, err := client.Do(req)
			if err != nil {
				return
			}
			defer res.Body.Close()
			if res.StatusCode != 200 {
				return
			}
			var parsed dohResp
			if json.NewDecoder(io.LimitReader(res.Body, 32768)).Decode(&parsed) != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, ans := range parsed.Answer {
				ip := strings.TrimSpace(ans.Data)
				if ans.Type == 1 && publicDriveIP(ip) && !seen[ip] {
					seen[ip] = true
					out = append(out, ip)
				}
			}
		}()
	}
	wg.Wait()
	return out
}

func discoverTargetCandidates(ctx context.Context, spec networkProbeTargetSpec, state Obj, bypassTun bool) ([]string, bool) {
	var seeds []string
	seedSeen := map[string]bool{}
	addSeed := func(ip string) {
		if publicDriveIP(ip) && !seedSeen[ip] {
			seedSeen[ip] = true
			seeds = append(seeds, ip)
		}
	}
	for _, raw := range list(state["results"]) {
		row := object(raw)
		if num(row["verifiedAt"]) > time.Now().Add(-7*24*time.Hour).Unix() {
			addSeed(str(row["ip"]))
		}
	}
	if spec.Strategy == "google" {
		for _, host := range spec.Hosts {
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
		return discoverDriveCandidates(ctx, bypassTun, seeds)
	}

	// Include preset seed IPs (stripping inline comments) before DoH/DNS lookups
	for _, rawSeed := range spec.PresetSeeds {
		ip := strings.TrimSpace(strings.SplitN(rawSeed, "#", 2)[0])
		if addr, pErr := netip.ParseAddr(ip); pErr == nil {
			addSeed(addr.Unmap().String())
		}
	}

	// Clean DoH resolution + system DNS fallback
	dohIPs := resolveDoHIPv4(ctx, spec.PrimaryHost, bypassTun)
	if spec.MirrorHost != "" && !strings.EqualFold(spec.MirrorHost, spec.PrimaryHost) {
		dohIPs = append(dohIPs, resolveDoHIPv4(ctx, spec.MirrorHost, bypassTun)...)
	}
	detectedCF := spec.Strategy == "cloudflare"
	for _, ip := range dohIPs {
		if detectedCF && !isCloudflareIP(ip) {
			continue
		}
		addSeed(ip)
		if isCloudflareIP(ip) {
			detectedCF = true
		}
	}
	for _, host := range spec.Hosts {
		dnsCtx, dnsCancel := context.WithTimeout(ctx, 1800*time.Millisecond)
		addresses, _ := net.DefaultResolver.LookupIPAddr(dnsCtx, host)
		dnsCancel()
		for _, address := range addresses {
			ipStr := address.IP.String()
			if publicDriveIP(ipStr) {
				if detectedCF && !isCloudflareIP(ipStr) && len(dohIPs) > 0 {
					// Skip likely DNS-poisoned non-Cloudflare answers when target is on Cloudflare
					continue
				}
				addSeed(ipStr)
			}
		}
	}

	seen := map[string]bool{}
	initial := make([]string, 0, 256)
	addInitial := func(ip string) {
		if publicDriveIP(ip) && !seen[ip] && len(initial) < 256 {
			seen[ip] = true
			initial = append(initial, ip)
		}
	}

	if detectedCF {
		for _, s := range builtinCloudflareSeeds {
			addSeed(s)
		}
		for _, ip := range seeds {
			addInitial(ip)
			for _, n := range subnetNeighboursWithOffsets(ip, cloudflareHostOffsets) {
				addInitial(n)
			}
		}
		for _, ip := range samplePrefixIPs(cloudflareCorePrefixes, cloudflareHostOffsets, 28, seen) {
			addInitial(ip)
		}
	} else {
		for _, ip := range seeds {
			addInitial(ip)
			for _, n := range subnetNeighboursWithOffsets(ip, genericHostOffsets) {
				addInitial(n)
			}
		}
	}

	if len(initial) == 0 {
		return nil, false
	}
	open, tunIntercepted := tcpSweepDriveIPs(ctx, initial, bypassTun)
	if detectedCF && len(open) < 10 && !tunIntercepted && ctx.Err() == nil {
		batch := samplePrefixIPs(cloudflareCorePrefixes, cloudflareHostOffsets, 36, seen)
		for _, ip := range batch {
			seen[ip] = true
		}
		more, intercepted := tcpSweepDriveIPs(ctx, batch, bypassTun)
		if intercepted {
			tunIntercepted = true
		}
		open = append(open, more...)
	}
	if len(open) == 0 {
		limit := min(16, len(initial))
		return initial[:limit], tunIntercepted
	}
	if len(open) > 48 {
		open = open[:48]
	}
	return open, tunIntercepted
}

func probeGenericHTTPTarget(ctx context.Context, spec networkProbeTargetSpec, client *http.Client) (string, error) {
	endpoint := "https://" + spec.PrimaryHost + spec.ProbePath
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "bgmcat/"+Version)
	req.Header.Set("Accept", "*/*")
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("连接或 TLS 校验失败：%w", err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 8192))
	text := strings.ToLower(string(data))

	switch spec.ProbeKind {
	case "rss":
		if res.StatusCode == 200 && (strings.Contains(text, "<rss") || strings.Contains(text, "<feed") || strings.Contains(text, "<?xml")) {
			return "", nil
		}
		if res.StatusCode == 403 || res.StatusCode == 503 {
			return "", fmt.Errorf("边缘节点拦截 (HTTP %d)", res.StatusCode)
		}
		return "", fmt.Errorf("未返回有效 RSS (HTTP %d)", res.StatusCode)
	case "json401":
		if res.StatusCode == 200 || res.StatusCode == 401 || res.StatusCode == 403 {
			return "", nil
		}
		return "", fmt.Errorf("API 探测异常 (HTTP %d)", res.StatusCode)
	default:
		if res.StatusCode > 0 && res.StatusCode < 500 {
			return "", nil
		}
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
}

func probeTargetEndpoint(ctx context.Context, spec networkProbeTargetSpec, ip string, policy driveTLSPolicy, bypassTun bool) (string, string, error) {
	if spec.ProbeKind == "google" {
		return probeDriveEndpoint(ctx, ip, policy, bypassTun)
	}
	allowed := make(map[string]bool, len(spec.Hosts)+1)
	for _, h := range spec.Hosts {
		allowed[strings.ToLower(h)] = true
	}
	if spec.MirrorHost != "" {
		allowed[strings.ToLower(spec.MirrorHost)] = true
	}
	trySpecHost := func(targetSpec networkProbeTargetSpec, p driveTLSPolicy) (string, error) {
		tr := hostIPTransportWithOptions(ip, allowed, spec.ForgedSNI, bypassTun, p)
		defer tr.CloseIdleConnections()
		client := &http.Client{
			Transport: tr,
			Timeout:   8 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 2 || !allowed[strings.ToLower(req.URL.Hostname())] {
					return http.ErrUseLastResponse
				}
				return nil
			},
		}
		return probeGenericHTTPTarget(ctx, targetSpec, client)
	}

	if policy.Mode == "original" {
		type probeAttempt struct {
			target networkProbeTargetSpec
			policy driveTLSPolicy
			label  string
		}
		var attempts []probeAttempt
		if spec.ForgedSNI != "" {
			attempts = append(attempts, probeAttempt{spec, driveTLSPolicy{Mode: "custom", ServerName: spec.ForgedSNI}, spec.ForgedSNI})
		} else if spec.Strategy == "cloudflare" || isCloudflareIP(ip) {
			attempts = append(attempts, probeAttempt{spec, driveTLSPolicy{Mode: "ech"}, "ech"})
			if spec.MirrorHost != "" && !strings.EqualFold(spec.PrimaryHost, spec.MirrorHost) {
				mirrorSpec := spec
				mirrorSpec.PrimaryHost = spec.MirrorHost
				attempts = append(attempts, probeAttempt{mirrorSpec, driveTLSPolicy{Mode: "fragment"}, "mirror:" + spec.MirrorHost})
			}
		}
		attempts = append(attempts, probeAttempt{spec, driveTLSPolicy{Mode: "fragment"}, "fragment"})
		if spec.MirrorHost != "" && !strings.EqualFold(spec.PrimaryHost, spec.MirrorHost) && spec.Strategy != "cloudflare" && !isCloudflareIP(ip) {
			mirrorSpec := spec
			mirrorSpec.PrimaryHost = spec.MirrorHost
			attempts = append(attempts, probeAttempt{mirrorSpec, driveTLSPolicy{Mode: "fragment"}, "mirror:" + spec.MirrorHost})
		}
		attempts = append(attempts, probeAttempt{spec, driveTLSPolicy{Mode: "none"}, "none"})
		for _, att := range attempts {
			if ctx.Err() != nil {
				break
			}
			if w, err := trySpecHost(att.target, att.policy); err == nil {
				return w, att.label, nil
			}
		}
		return "", "", errors.New("连接或 TLS 握手被重置（已尝试 TLS 1.3 ECH、伪装 SNI、镜像域名、TCP/TLS 分片与无 SNI）")
	}
	warning, err := trySpecHost(spec, policy)
	if err == nil {
		usedSNI := policy.Mode
		if policy.Mode == "custom" {
			usedSNI = policy.ServerName
		}
		return warning, usedSNI, nil
	}
	return "", "", err
}

func (a *App) directTransportForHost(host string) (http.RoundTripper, string, string, string) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return nil, "", "", ""
	}
	specs := a.networkProbeTargets()
	var matched *networkProbeTargetSpec
	for i := range specs {
		for _, h := range specs[i].Hosts {
			if h == host {
				matched = &specs[i]
				break
			}
		}
		if matched != nil {
			break
		}
	}
	if matched == nil {
		return nil, "", "", ""
	}
	if matched.ID == "google" && (host == "www.googleapis.com" || host == "drive.usercontent.google.com") {
		tr, ip := a.driveTransport()
		if ip == "" {
			return nil, "", "", ""
		}
		return tr, "google", ip, ""
	}

	a.driveNetworkMu.Lock()
	defer a.driveNetworkMu.Unlock()
	state := a.networkTargetStatus(*matched)
	ip := str(state["selectedIP"])
	if ip == "" {
		return nil, "", "", ""
	}
	selectedSNI := str(state["selectedSNI"])
	policy := effectiveDrivePolicy(state, selectedSNI)
	rewriteHost := ""
	if drivePolicy(state).Mode == "original" && strings.HasPrefix(selectedSNI, "mirror:") {
		cand := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(selectedSNI, "mirror:")))
		for _, h := range matched.Hosts {
			if strings.EqualFold(h, cand) {
				rewriteHost = h
				break
			}
		}
	}
	bypassTun := driveBypassTun(state)
	cacheKey := fmt.Sprintf("%s|%s|tun:%v|hosts:%s", ip, policy.key(), bypassTun, strings.Join(matched.Hosts, ","))

	if a.directRoutes == nil {
		a.directRoutes = map[string]*cachedDirectRoute{}
	}
	cur := a.directRoutes[matched.ID]
	if cur == nil || cur.ip != ip || cur.cacheKey != cacheKey {
		if cur != nil && cur.tr != nil {
			cur.tr.CloseIdleConnections()
		}
		allowed := make(map[string]bool, len(matched.Hosts))
		for _, h := range matched.Hosts {
			allowed[h] = true
		}
		forged := matched.ForgedSNI
		if matched.ID == "google" {
			forged = driveForgedHost
		}
		tr := hostIPTransportWithOptions(ip, allowed, forged, bypassTun, policy)
		cur = &cachedDirectRoute{ip: ip, cacheKey: cacheKey, tr: tr}
		a.directRoutes[matched.ID] = cur
	}
	return cur.tr, matched.ID, ip, rewriteHost
}

func (a *App) failNetworkTargetIP(targetID, ip string) {
	if targetID == "google" {
		a.failDriveIP(ip)
	}
	forgetDriveInterface()
	a.driveNetworkMu.Lock()
	defer a.driveNetworkMu.Unlock()
	if cur := a.directRoutes[targetID]; cur != nil && cur.ip == ip {
		if cur.tr != nil {
			cur.tr.CloseIdleConnections()
		}
		delete(a.directRoutes, targetID)
	}
	if targetID == "google" {
		return
	}
	key := networkProbeStoreKey(targetID)
	state, _ := a.Store.Get("config", key)
	if state == nil {
		return
	}
	for _, raw := range list(state["results"]) {
		row := object(raw)
		if str(row["ip"]) == ip {
			row["available"] = false
			row["error"] = "实际传输连接失败，已自动切换其他可用节点或系统网络"
		}
	}
	_ = a.Store.Put("config", key, state)
}

func (a *App) closeDirectRoutes() {
	a.driveNetworkMu.Lock()
	defer a.driveNetworkMu.Unlock()
	for id, route := range a.directRoutes {
		if route != nil && route.tr != nil {
			route.tr.CloseIdleConnections()
		}
		delete(a.directRoutes, id)
	}
}

type smartDirectRoundTripper struct {
	app  *App
	base *http.Transport
}

func (rt *smartDirectRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL != nil && strings.EqualFold(req.URL.Scheme, "https") {
		port := req.URL.Port()
		if port == "" || port == "443" {
			if directTR, targetID, ip, rewriteHost := rt.app.directTransportForHost(req.URL.Hostname()); directTR != nil && ip != "" {
				directReq := req
				if rewriteHost != "" && !strings.EqualFold(req.URL.Hostname(), rewriteHost) && (req.Body == nil || req.GetBody != nil) {
					directReq = req.Clone(req.Context())
					if port != "" {
						directReq.URL.Host = net.JoinHostPort(rewriteHost, port)
					} else {
						directReq.URL.Host = rewriteHost
					}
					directReq.Host = rewriteHost
					if req.GetBody != nil {
						if bodyCopy, bErr := req.GetBody(); bErr == nil {
							directReq.Body = bodyCopy
						}
					}
				}
				res, err := directTR.RoundTrip(directReq)
				if err == nil {
					return res, nil
				}
				if req.Context().Err() == nil {
					rt.app.failNetworkTargetIP(targetID, ip)
					if req.Body == nil || req.GetBody != nil {
						retryReq := req.Clone(req.Context())
						if req.GetBody != nil {
							if bodyCopy, bErr := req.GetBody(); bErr == nil {
								retryReq.Body = bodyCopy
							}
						}
						return rt.base.RoundTrip(retryReq)
					}
				}
				return nil, err
			}
		}
	}
	return rt.base.RoundTrip(req)
}

func (a *App) networkProbeRoutes() {
	a.handle("GET /api/network/targets", func(w http.ResponseWriter, r *http.Request) error {
		specs := a.networkProbeTargets()
		items := make([]Obj, 0, len(specs))
		nowAt := time.Now()
		for _, spec := range specs {
			st := a.networkTargetStatus(spec)
			avail := 0
			var fastest any
			var bestMs int64 = 1 << 62
			for _, raw := range list(st["results"]) {
				row := object(raw)
				if flag(row["available"]) && num(row["verifiedAt"]) > nowAt.Add(-24*time.Hour).Unix() {
					avail++
					if ms := num(row["latencyMs"]); ms >= 0 && ms < bestMs {
						bestMs = ms
						fastest = ms
					}
				}
			}
			items = append(items, Obj{
				"id":             spec.ID,
				"name":           spec.Name,
				"group":          spec.Group,
				"hosts":          spec.Hosts,
				"custom":         spec.Custom,
				"enabled":        flag(st["enabled"]),
				"selectedIP":     str(st["selectedIP"]),
				"availableCount": avail,
				"totalCount":     len(list(st["results"])),
				"fastestMs":      fastest,
			})
		}
		ok(w, items)
		return nil
	})

	a.handle("POST /api/network/targets", func(w http.ResponseWriter, r *http.Request) error {
		input, err := body(w, r)
		if err != nil {
			return err
		}
		rawHost := strings.TrimSpace(str(input["host"]))
		if strings.Contains(rawHost, "://") {
			if u, pErr := url.Parse(rawHost); pErr == nil && u.Hostname() != "" {
				rawHost = u.Hostname()
			}
		}
		rawHost = strings.ToLower(strings.TrimSuffix(rawHost, "."))
		if !validASCIIHostname(rawHost) {
			return errors.New("请输入有效的域名（如 bangumi.moe）")
		}
		for _, existing := range a.networkProbeTargets() {
			for _, h := range existing.Hosts {
				if h == rawHost {
					ok(w, a.networkTargetStatus(existing))
					return nil
				}
			}
		}
		name := strings.TrimSpace(str(input["name"]))
		if name == "" {
			name = rawHost
		}
		id := "custom-" + rawHost
		customs := a.customProbeTargetList()
		customs = append(customs, Obj{"id": id, "name": name, "host": rawHost})
		if err := a.Store.Put("config", "network-probe-custom", Obj{"items": customs}); err != nil {
			return err
		}
		spec, _ := a.findNetworkProbeTarget(id)
		ok(w, a.networkTargetStatus(spec))
		return nil
	})

	a.handle("DELETE /api/network/targets/{id}", func(w http.ResponseWriter, r *http.Request) error {
		id := strings.TrimSpace(r.PathValue("id"))
		spec, found := a.findNetworkProbeTarget(id)
		if !found {
			return errors.New("探测目标不存在")
		}
		if !spec.Custom {
			return errors.New("内置探测目标不可删除")
		}
		customs := a.customProbeTargetList()
		filtered := make([]Obj, 0, len(customs))
		for _, item := range customs {
			if str(item["id"]) != id {
				filtered = append(filtered, item)
			}
		}
		if err := a.Store.Put("config", "network-probe-custom", Obj{"items": filtered}); err != nil {
			return err
		}
		_ = a.Store.Delete("config", networkProbeStoreKey(id))
		a.driveNetworkMu.Lock()
		if cur := a.directRoutes[id]; cur != nil && cur.tr != nil {
			cur.tr.CloseIdleConnections()
		}
		delete(a.directRoutes, id)
		a.driveNetworkMu.Unlock()
		ok(w, Obj{"deleted": true, "id": id})
		return nil
	})

	a.handle("GET /api/network/targets/{id}", func(w http.ResponseWriter, r *http.Request) error {
		spec, found := a.findNetworkProbeTarget(r.PathValue("id"))
		if !found {
			return errors.New("探测目标不存在")
		}
		ok(w, a.networkTargetStatus(spec))
		return nil
	})

	a.handle("PATCH /api/network/targets/{id}", func(w http.ResponseWriter, r *http.Request) error {
		spec, found := a.findNetworkProbeTarget(r.PathValue("id"))
		if !found {
			return errors.New("探测目标不存在")
		}
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
		storeKey := networkProbeStoreKey(spec.ID)
		state, _ := a.Store.Get("config", storeKey)
		if state == nil {
			state = Obj{"enabled": false, "results": []any{}}
		}
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
		if spec.ID == "google" {
			if a.driveIPHTTP != nil {
				a.driveIPHTTP.CloseIdleConnections()
			}
			a.driveIPHTTP = nil
			a.driveIP = ""
			a.driveTLSKey = ""
		}
		if cur := a.directRoutes[spec.ID]; cur != nil && cur.tr != nil {
			cur.tr.CloseIdleConnections()
		}
		delete(a.directRoutes, spec.ID)
		if err := a.Store.Put("config", storeKey, state); err != nil {
			return err
		}
		ok(w, a.networkTargetStatus(spec))
		return nil
	})

	a.handle("POST /api/network/targets/{id}/scan", func(w http.ResponseWriter, r *http.Request) error {
		spec, found := a.findNetworkProbeTarget(r.PathValue("id"))
		if !found {
			return errors.New("探测目标不存在")
		}
		if !a.driveScanMu.TryLock() {
			return errors.New("正在扫描，请稍后重试")
		}
		defer a.driveScanMu.Unlock()
		var input struct {
			Candidates string `json:"candidates"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 32768)).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
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

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		stop := context.AfterFunc(a.ctx, cancel)
		defer stop()

		storeKey := networkProbeStoreKey(spec.ID)
		initialState := a.networkTargetStatus(spec)
		policy := drivePolicy(initialState)
		bypassTun := driveBypassTun(initialState)
		autoDiscovery := len(candidates) == 0
		tunIntercepted := false
		if autoDiscovery {
			candidates, tunIntercepted = discoverTargetCandidates(ctx, spec, initialState, bypassTun)
			if len(candidates) == 0 {
				return fmt.Errorf("未能解析 %s 的候选公网节点，可在下方手动填入候选 IP", spec.PrimaryHost)
			}
		}
		target := 0
		if autoDiscovery {
			target = 8
		}
		rows := scanDriveEndpoints(ctx, candidates, target, func(ctx context.Context, ip string) (string, string, error) {
			return probeTargetEndpoint(ctx, spec, ip, policy, bypassTun)
		})
		if r.Context().Err() != nil || a.ctx.Err() != nil {
			return errors.New("扫描已取消，保留原结果")
		}

		a.driveNetworkMu.Lock()
		defer a.driveNetworkMu.Unlock()
		state, _ := a.Store.Get("config", storeKey)
		if state == nil {
			state = Obj{"enabled": false, "results": []any{}}
		}
		if drivePolicy(state) != policy {
			return errors.New("扫描期间 TLS 策略已更改，请重新扫描")
		}
		state["results"] = rows
		state["scannedAt"] = now()
		state["partial"] = !autoDiscovery && len(rows) < len(candidates)
		state["candidateCount"] = len(candidates)
		// Auto-enable route when user scans and finds at least 1 working node for the first time
		for _, rObj := range rows {
			if flag(rObj["available"]) {
				if state["enabled"] == nil || !flag(state["enabled"]) {
					state["enabled"] = true
				}
				break
			}
		}
		if tunIntercepted {
			state["tunWarning"] = "检测到几乎所有随机 IP 均响应 TCP 443，可能被 TUN/透明代理接管，建议开启「绑定物理网卡」"
		} else {
			delete(state, "tunWarning")
		}
		if cur := a.directRoutes[spec.ID]; cur != nil && cur.tr != nil {
			cur.tr.CloseIdleConnections()
		}
		delete(a.directRoutes, spec.ID)
		if spec.ID == "google" && a.driveIPHTTP != nil {
			a.driveIPHTTP.CloseIdleConnections()
			a.driveIPHTTP = nil
			a.driveIP = ""
			a.driveTLSKey = ""
		}
		if err := a.Store.Put("config", storeKey, state); err != nil {
			return err
		}
		ok(w, a.networkTargetStatus(spec))
		return nil
	})

	a.handle("POST /api/network/targets/{id}/probe-one", func(w http.ResponseWriter, r *http.Request) error {
		spec, found := a.findNetworkProbeTarget(r.PathValue("id"))
		if !found {
			return errors.New("探测目标不存在")
		}
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
		initialState := a.networkTargetStatus(spec)
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
		warning, usedSNI, probeErr := probeTargetEndpoint(ctx, spec, ip, policy, bypassTun)
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
		storeKey := networkProbeStoreKey(spec.ID)
		state, _ := a.Store.Get("config", storeKey)
		if state == nil {
			state = Obj{"enabled": false, "results": []any{}}
		}
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
		if err := a.Store.Put("config", storeKey, state); err != nil {
			return err
		}
		ok(w, a.networkTargetStatus(spec))
		return nil
	})
}
