package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
)

const driveSeedIP = "183.56.143.147"

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

// Pin the TCP destination. Optional SNI policy is independent of certificate identity.
// OAuth uses a.http unchanged.
func driveIPTransport(ip string, policies ...driveTLSPolicy) *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if port != "443" || (host != "www.googleapis.com" && host != "drive.usercontent.google.com") {
			return nil, errors.New("直连仅允许 Google Drive HTTPS 地址")
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
	}
	tr.TLSHandshakeTimeout = 3 * time.Second
	tr.ResponseHeaderTimeout = 30 * time.Second
	if len(policies) > 0 && policies[0].Mode != "original" {
		configureDriveTLS(tr, policies[0], nil)
	}
	return tr
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

func selectedDriveIP(state Obj, at time.Time) string {
	if !flag(state["enabled"]) {
		return ""
	}
	best, latency := "", int64(1<<62)
	for _, raw := range list(state["results"]) {
		row := object(raw)
		verified := num(row["verifiedAt"])
		if !publicDriveIP(str(row["ip"])) || !flag(row["available"]) || verified <= at.Add(-24*time.Hour).Unix() || verified > at.Unix() {
			continue
		}
		if ms := num(row["latencyMs"]); ms >= 0 && ms < latency {
			best, latency = str(row["ip"]), ms
		}
	}
	return best
}
func (a *App) driveNetworkStatus() Obj {
	state, _ := a.Store.Get("config", "drive-network")
	if state == nil {
		state = Obj{"enabled": false, "results": []any{}}
	}
	policy := drivePolicy(state)
	state["sniMode"], state["sniHost"] = policy.Mode, policy.ServerName
	state["selectedIP"] = selectedDriveIP(state, time.Now())
	return state
}
func (a *App) driveTransport() (http.RoundTripper, string) {
	a.driveNetworkMu.Lock()
	defer a.driveNetworkMu.Unlock()
	state := a.driveNetworkStatus()
	ip := str(state["selectedIP"])
	policy := drivePolicy(state)
	if ip == "" {
		return a.http.Transport, ""
	}
	if a.driveIP != ip || a.driveTLSKey != policy.key() {
		if a.driveIPHTTP != nil {
			a.driveIPHTTP.CloseIdleConnections()
		}
		a.driveIP, a.driveIPHTTP = ip, driveIPTransport(ip, policy)
		a.driveTLSKey = policy.key()
	}
	return a.driveIPHTTP, ip
}
func (a *App) failDriveIP(ip string) {
	a.driveNetworkMu.Lock()
	defer a.driveNetworkMu.Unlock()
	state := a.driveNetworkStatus()
	for _, raw := range list(state["results"]) {
		row := object(raw)
		if str(row["ip"]) == ip {
			row["available"] = false
			row["error"] = "实际传输连接失败，下次请求改用其他可用 IP 或系统网络"
		}
	}
	_ = a.Store.Put("config", "drive-network", state)
}

func scanDriveIPs(ctx context.Context, ips []string, probe func(context.Context, string) (string, error)) []Obj {
	jobs := make(chan string, len(ips))
	results := make(chan Obj, len(ips))
	for _, ip := range ips {
		jobs <- ip
	}
	close(jobs)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				if ctx.Err() != nil {
					return
				}
				started := time.Now()
				warning, err := probe(ctx, ip)
				row := Obj{"ip": ip, "available": err == nil, "latencyMs": time.Since(started).Milliseconds(), "verifiedAt": int64(0), "warning": warning, "error": ""}
				if err != nil {
					row["error"] = err.Error()
				} else {
					row["verifiedAt"] = time.Now().Unix()
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
			Enabled *bool   `json:"enabled"`
			SNIMode *string `json:"sniMode"`
			SNIHost *string `json:"sniHost"`
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
		if input.SNIMode != nil {
			state["sniMode"] = *input.SNIMode
		}
		if input.SNIHost != nil {
			state["sniHost"] = strings.ToLower(strings.TrimSpace(*input.SNIHost))
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
		if len(candidates) == 0 {
			add(driveSeedIP)
			for _, raw := range list(a.driveNetworkStatus()["results"]) {
				row := object(raw)
				if num(row["verifiedAt"]) > time.Now().Add(-7*24*time.Hour).Unix() {
					add(str(row["ip"]))
				}
			}
			for _, host := range []string{"www.googleapis.com", "drive.usercontent.google.com", "drive.google.com"} {
				dnsCtx, dnsCancel := context.WithTimeout(ctx, 2*time.Second)
				addresses, _ := net.DefaultResolver.LookupIPAddr(dnsCtx, host)
				dnsCancel()
				for _, address := range addresses {
					add(address.IP.String())
				}
			}
		}
		policy := drivePolicy(a.driveNetworkStatus())
		rows := scanDriveIPs(ctx, candidates, func(ctx context.Context, ip string) (string, error) {
			tr := driveIPTransport(ip, policy)
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			return probeDrive(ctx, client)
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
		state["partial"] = len(rows) < len(candidates)
		state["candidateCount"] = len(candidates)
		if err := a.Store.Put("config", "drive-network", state); err != nil {
			return err
		}
		ok(w, a.driveNetworkStatus())
		return nil
	})
}
