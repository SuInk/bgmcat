package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	driveForgedHost          = "g.cn"
	driveSecondaryForgedHost = "dl.google.com"
	defaultCloudflareECHB64  = "AEX+DQBBpAAgACBHuUO4jcWFJGRp/T+zPR9ulI0WFNlNrMAJRO/wMcZMOAAEAAEAAQASY2xvdWRmbGFyZS1lY2guY29tAAA="
	echCacheTTL              = 30 * time.Minute
)

type cachedECHEntry struct {
	configList []byte
	expiresAt  time.Time
}

var (
	echCacheMu    sync.Mutex
	echCache      = map[string]cachedECHEntry{}
	dohECHParamRE = regexp.MustCompile(`(?i)(?:^|\s)ech="?([A-Za-z0-9+/=_-]+)"?(?:\s|$)`)
)

func extractDoHECHConfigList(raw string) []byte {
	m := dohECHParamRE.FindStringSubmatch(raw)
	if len(m) < 2 {
		return nil
	}
	val := strings.TrimSpace(m[1])
	decoded, err := base64.StdEncoding.DecodeString(val)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(val, "="))
	}
	if err != nil || len(decoded) < 32 {
		return nil
	}
	return decoded
}

func defaultECHConfigList() []byte {
	decoded, err := base64.StdEncoding.DecodeString(defaultCloudflareECHB64)
	if err != nil || len(decoded) < 32 {
		return nil
	}
	return decoded
}

func updateCachedECHConfigList(host string, retryConfigList []byte) {
	if len(retryConfigList) == 0 {
		return
	}
	key := strings.ToLower(strings.TrimSpace(host))
	if key == "" {
		return
	}
	copied := append([]byte(nil), retryConfigList...)
	echCacheMu.Lock()
	echCache[key] = cachedECHEntry{
		configList: copied,
		expiresAt:  time.Now().Add(echCacheTTL),
	}
	echCacheMu.Unlock()
}

func deleteCachedECHConfigList(host string) {
	key := strings.ToLower(strings.TrimSpace(host))
	if key == "" {
		return
	}
	echCacheMu.Lock()
	delete(echCache, key)
	echCacheMu.Unlock()
}

func queryDoHECHConfigList(ctx context.Context, host string, bypassTun bool) []byte {
	type dohAnswer struct {
		Type int    `json:"type"`
		Data string `json:"data"`
	}
	type dohResp struct {
		Answer []dohAnswer `json:"Answer"`
	}
	endpoints := []struct {
		ip  string
		url string
	}{
		{"223.5.5.5", "https://223.5.5.5/resolve?name=" + url.QueryEscape(host) + "&type=65"},
		{"1.12.12.12", "https://1.12.12.12/resolve?name=" + url.QueryEscape(host) + "&type=65"},
	}
	resCh := make(chan []byte, len(endpoints))
	var wg sync.WaitGroup
	for _, ep := range endpoints {
		ep := ep
		wg.Add(1)
		go func() {
			defer wg.Done()
			qCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
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
			client := &http.Client{Transport: tr, Timeout: 2 * time.Second}
			req, err := http.NewRequestWithContext(qCtx, http.MethodGet, ep.url, nil)
			if err != nil {
				return
			}
			req.Header.Set("Accept", "application/dns-json")
			res, err := client.Do(req)
			if err != nil {
				return
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusOK {
				return
			}
			var parsed dohResp
			if json.NewDecoder(io.LimitReader(res.Body, 32768)).Decode(&parsed) != nil {
				return
			}
			for _, ans := range parsed.Answer {
				if ans.Type == 65 {
					if cfg := extractDoHECHConfigList(ans.Data); len(cfg) > 0 {
						resCh <- cfg
						return
					}
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(resCh)
	}()
	for cfg := range resCh {
		if len(cfg) > 0 {
			return cfg
		}
	}
	return nil
}

func resolveECHConfigList(ctx context.Context, host string, bypassTun bool) []byte {
	key := strings.ToLower(strings.TrimSpace(host))
	nowAt := time.Now()
	echCacheMu.Lock()
	if entry, ok := echCache[key]; ok && nowAt.Before(entry.expiresAt) && len(entry.configList) > 0 {
		copied := append([]byte(nil), entry.configList...)
		echCacheMu.Unlock()
		return copied
	}
	echCacheMu.Unlock()

	var cfg []byte
	if key != "" && ctx.Err() == nil {
		cfg = queryDoHECHConfigList(ctx, key, bypassTun)
		if len(cfg) == 0 && !strings.EqualFold(key, "cloudflare-ech.com") && ctx.Err() == nil {
			echCacheMu.Lock()
			if entry, ok := echCache["cloudflare-ech.com"]; ok && time.Now().Before(entry.expiresAt) && len(entry.configList) > 0 {
				cfg = append([]byte(nil), entry.configList...)
			}
			echCacheMu.Unlock()
			if len(cfg) == 0 {
				cfg = queryDoHECHConfigList(ctx, "cloudflare-ech.com", bypassTun)
				if len(cfg) > 0 {
					updateCachedECHConfigList("cloudflare-ech.com", cfg)
				}
			}
		}
	}
	if len(cfg) == 0 {
		cfg = defaultECHConfigList()
	}
	if len(cfg) > 0 && key != "" {
		updateCachedECHConfigList(key, cfg)
	}
	return append([]byte(nil), cfg...)
}

func isTrustedGoogleSNI(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "g.cn" ||
		host == "google.cn" ||
		strings.HasSuffix(host, ".google.cn") ||
		host == "google.com" ||
		strings.HasSuffix(host, ".google.com") ||
		strings.HasSuffix(host, ".gstatic.cn") ||
		strings.HasSuffix(host, ".gstatic.com") ||
		strings.HasSuffix(host, ".googleapis.cn")
}

type driveTLSPolicy struct{ Mode, ServerName string }

func drivePolicy(state Obj) driveTLSPolicy {
	mode := str(state["sniMode"])
	if mode == "" {
		mode = "original"
	}
	return driveTLSPolicy{mode, str(state["sniHost"])}
}

func effectiveDrivePolicy(state Obj, rowSNI string) driveTLSPolicy {
	policy := drivePolicy(state)
	if policy.Mode == "original" && rowSNI == "ech" {
		return driveTLSPolicy{Mode: "ech"}
	}
	if policy.Mode == "original" && (rowSNI == "fragment" || strings.HasPrefix(rowSNI, "mirror:")) {
		return driveTLSPolicy{Mode: "fragment"}
	}
	if policy.Mode == "original" && rowSNI != "" {
		return driveTLSPolicy{Mode: "custom", ServerName: rowSNI}
	}
	return policy
}
func (p driveTLSPolicy) key() string { return p.Mode + ":" + p.ServerName }
func (p driveTLSPolicy) validate() error {
	if p.Mode == "original" || p.Mode == "ech" || p.Mode == "fragment" || p.Mode == "none" {
		return nil
	}
	if p.Mode != "custom" {
		return errors.New("未知 SNI 模式")
	}
	host := p.ServerName
	if len(host) == 0 || len(host) > 253 || net.ParseIP(host) != nil {
		return errors.New("SNI 必须是有效域名")
	}
	for _, part := range strings.Split(host, ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return errors.New("SNI 必须是有效域名")
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return errors.New("SNI 仅支持 ASCII 域名（国际化域名请用 punycode）")
			}
		}
	}
	return nil
}

func hostTLSConfig(realHost, forgedFallbackHost string, policy driveTLSPolicy, roots *x509.CertPool) *tls.Config {
	return hostTLSConfigWithECH(realHost, forgedFallbackHost, policy, nil, roots)
}

func hostTLSConfigWithECH(realHost, forgedFallbackHost string, policy driveTLSPolicy, echConfigList []byte, roots *x509.CertPool) *tls.Config {
	sni := realHost
	minVer := uint16(tls.VersionTLS12)
	var echList []byte
	if policy.Mode == "none" {
		sni = ""
	} else if policy.Mode == "custom" {
		sni = policy.ServerName
	} else if policy.Mode == "ech" && len(echConfigList) > 0 {
		minVer = tls.VersionTLS13
		echList = echConfigList
	}
	return &tls.Config{
		MinVersion:                     minVer,
		ServerName:                     sni,
		NextProtos:                     []string{"http/1.1"},
		CurvePreferences:               []tls.CurveID{tls.X25519, tls.CurveP256},
		RootCAs:                        roots,
		EncryptedClientHelloConfigList: echList,
		// Go normally ties certificate verification to ServerName. Replace that check
		// below with full chain + original DNS name verification, on every handshake.
		// This flag alone is unsafe: VerifyConnection MUST remain installed.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("TLS 服务端未提供证书")
			}
			intermediates := x509.NewCertPool()
			for _, cert := range cs.PeerCertificates[1:] {
				intermediates.AddCert(cert)
			}
			_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{
				DNSName: realHost, Roots: roots, Intermediates: intermediates,
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			})
			if err != nil && forgedFallbackHost != "" && sni != "" && (sni == forgedFallbackHost || (forgedFallbackHost == driveForgedHost && isTrustedGoogleSNI(sni))) {
				_, err = cs.PeerCertificates[0].Verify(x509.VerifyOptions{
					DNSName: sni, Roots: roots, Intermediates: intermediates,
					KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
				})
			}
			return err
		},
	}
}

func driveTLSConfig(realHost string, policy driveTLSPolicy, roots *x509.CertPool) *tls.Config {
	return hostTLSConfig(realHost, driveForgedHost, policy, roots)
}

// tlsClientHelloFragmentConn splits the initial TLS ClientHello record into
// multiple small TLS Handshake records and sends each in its own TCP_NODELAY
// segment so DPI middleboxes inspecting single packets cannot match the SNI.
type tlsClientHelloFragmentConn struct {
	net.Conn
	firstWrite bool
}

func newTLSClientHelloFragmentConn(conn net.Conn) net.Conn {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}
	return &tlsClientHelloFragmentConn{Conn: conn, firstWrite: true}
}

func (c *tlsClientHelloFragmentConn) Write(b []byte) (int, error) {
	if !c.firstWrite {
		return c.Conn.Write(b)
	}
	c.firstWrite = false
	defer func() {
		if tcpConn, ok := c.Conn.(*net.TCPConn); ok {
			_ = tcpConn.SetNoDelay(false)
		}
	}()
	if len(b) > 5 && b[0] == 0x16 && b[1] == 0x03 && (int(b[3])<<8|int(b[4])) == len(b)-5 && b[5] == 0x01 {
		payload := b[5:]
		verMajor, verMinor := b[1], b[2]
		sizes := []int{2, 13, 19, 29, 41}
		pos := 0
		for _, sz := range sizes {
			if pos >= len(payload) {
				break
			}
			end := min(pos+sz, len(payload))
			part := payload[pos:end]
			rec := make([]byte, 5+len(part))
			rec[0], rec[1], rec[2] = 0x16, verMajor, verMinor
			rec[3], rec[4] = byte(len(part)>>8), byte(len(part))
			copy(rec[5:], part)
			if _, err := c.Conn.Write(rec); err != nil {
				return 0, err
			}
			pos = end
		}
		if pos < len(payload) {
			part := payload[pos:]
			rec := make([]byte, 5+len(part))
			rec[0], rec[1], rec[2] = 0x16, verMajor, verMinor
			rec[3], rec[4] = byte(len(part)>>8), byte(len(part))
			copy(rec[5:], part)
			if _, err := c.Conn.Write(rec); err != nil {
				return 0, err
			}
		}
		return len(b), nil
	}
	return c.Conn.Write(b)
}

func configureHostTLS(tr *http.Transport, allowedHosts map[string]bool, forgedFallbackHost string, policy driveTLSPolicy, roots *x509.CertPool) {
	configureHostTLSWithBypass(tr, allowedHosts, forgedFallbackHost, false, policy, roots)
}

func configureHostTLSWithBypass(tr *http.Transport, allowedHosts map[string]bool, forgedFallbackHost string, bypassTun bool, policy driveTLSPolicy, roots *x509.CertPool) {
	// Explicit HTTP/1.1 avoids ALPN/Transport protocol mismatches with a custom dialer.
	tr.ForceAttemptHTTP2 = false
	tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	tr.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if err := policy.validate(); err != nil {
			return nil, err
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "443" || !allowedHosts[strings.ToLower(host)] {
			return nil, errors.New("TLS 直连仅允许已配置的目标 HTTPS 地址")
		}
		if policy.Mode == "ech" {
			echList := resolveECHConfigList(ctx, host, bypassTun)
			dialAndHandshake := func(cfgList []byte) (*tls.Conn, error) {
				conn, dErr := tr.DialContext(ctx, network, address)
				if dErr != nil {
					return nil, dErr
				}
				secured := tls.Client(conn, hostTLSConfigWithECH(host, forgedFallbackHost, policy, cfgList, roots))
				handshake, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				if hErr := secured.HandshakeContext(handshake); hErr != nil {
					conn.Close()
					return nil, hErr
				}
				return secured, nil
			}
			secured, hErr := dialAndHandshake(echList)
			if hErr != nil {
				var echRej *tls.ECHRejectionError
				if errors.As(hErr, &echRej) && len(echRej.RetryConfigList) > 0 && ctx.Err() == nil {
					updateCachedECHConfigList(host, echRej.RetryConfigList)
					return dialAndHandshake(echRej.RetryConfigList)
				}
				return nil, hErr
			}
			return secured, nil
		}
		conn, err := tr.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		wireConn := conn
		if policy.Mode == "fragment" || policy.Mode == "none" {
			wireConn = newTLSClientHelloFragmentConn(conn)
		}
		secured := tls.Client(wireConn, hostTLSConfig(host, forgedFallbackHost, policy, roots))
		handshake, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		if err := secured.HandshakeContext(handshake); err != nil {
			conn.Close()
			return nil, err
		}
		return secured, nil
	}
}

// Install a TLS dialer only for a selected Drive route. HTTP Host and certificate
// identity always remain the URL's original host; OAuth never uses this transport.
func configureDriveTLS(tr *http.Transport, policy driveTLSPolicy, roots *x509.CertPool) {
	configureHostTLS(tr, map[string]bool{
		"www.googleapis.com":           true,
		"drive.usercontent.google.com": true,
	}, driveForgedHost, policy, roots)
}
