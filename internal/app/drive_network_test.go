package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type driveProbeRoundTrip func(*http.Request) (*http.Response, error)

func (f driveProbeRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDriveIPFilteringAndSelection(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.0.1", "192.168.1.1", "100.64.1.2", "198.18.0.1", "198.19.1.1", "192.0.2.1", "2001:db8::1", "fc00::1", "fe80::1%en0", "224.0.0.1", "0.1.2.3", "255.255.255.255", "not-an-ip"} {
		if publicDriveIP(ip) {
			t.Errorf("accepted %s", ip)
		}
	}
	for _, ip := range []string{driveSeedIP, "8.8.8.8", "2001:4860:4860::8888"} {
		if !publicDriveIP(ip) {
			t.Errorf("rejected %s", ip)
		}
	}
	now := time.Now()
	rows := []Obj{
		{"ip": "8.8.8.8", "available": true, "verifiedAt": now.Add(-25 * time.Hour).Unix(), "latencyMs": 1},
		{"ip": "1.1.1.1", "available": true, "verifiedAt": now.Unix(), "latencyMs": 50},
		{"ip": driveSeedIP, "available": true, "verifiedAt": now.Unix(), "latencyMs": 20},
	}
	state := Obj{"enabled": true, "results": rows}
	if got := selectedDriveIP(state, now); got != driveSeedIP {
		t.Fatal(got)
	}
	rows[2]["available"] = false
	if got := selectedDriveIP(state, now); got != "1.1.1.1" {
		t.Fatal(got)
	}
	state["enabled"] = false
	if selectedDriveIP(state, now) != "" {
		t.Fatal("disabled route selected")
	}
}
func TestDriveProbeResponses(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		apiStatus              int
		apiBody                string
		downloadStatus         int
		downloadBody           string
		wantError, wantWarning bool
	}{
		{"success", 401, `{"error":{"code":401}}`, 200, "spam\n", false, false},
		{"wrong API", 200, `{}`, 200, "spam\n", true, false},
		{"fake 401", 401, `{"error":{"code":403}}`, 200, "spam\n", true, false},
		{"missing test file", 401, `{"error":{"code":401}}`, 404, "", false, true},
		{"changed test file", 401, `{"error":{"code":401}}`, 200, "HTML", false, true},
		{"redirect rejected", 401, `{"error":{"code":401}}`, 302, "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: driveProbeRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "" {
					t.Fatal("probe leaked authorization")
				}
				code, body := tc.apiStatus, tc.apiBody
				if r.URL.Host == "drive.usercontent.google.com" {
					code, body = tc.downloadStatus, tc.downloadBody
				} else if r.URL.Host != "www.googleapis.com" {
					t.Fatal(r.URL)
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
			})}
			warning, err := probeDrive(context.Background(), client)
			if (err != nil) != tc.wantError || (warning != "") != tc.wantWarning {
				t.Fatalf("warning=%q err=%v", warning, err)
			}
		})
	}
}
func TestDriveScanConcurrencyAndCancellation(t *testing.T) {
	ips := []string{}
	for i := 0; i < 24; i++ {
		ips = append(ips, fmt.Sprint(i))
	}
	var active, max atomic.Int32
	rows := scanDriveIPs(context.Background(), ips, func(ctx context.Context, ip string) (string, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := max.Load(); n > old; old = max.Load() {
			if max.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		if ip == "0" {
			return "", errors.New("unreachable")
		}
		return "", nil
	})
	if len(rows) != 24 || max.Load() > 8 || max.Load() < 2 {
		t.Fatalf("rows=%d concurrent=%d", len(rows), max.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows = scanDriveIPs(ctx, ips, func(context.Context, string) (string, error) { t.Error("probe after cancel"); return "", nil })
	if len(rows) != 0 {
		t.Fatal(rows)
	}
}
func TestDriveNetworkRoutesAndFailureFallback(t *testing.T) {
	a := testApp(t)
	if w := apiTest(a, "POST", "/api/cloud/network/scan", Obj{"candidates": "127.0.0.1"}, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := apiTest(a, "POST", "/api/cloud/network/scan", Obj{"candidates": "127.0.0.1"}, true); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := apiTest(a, "PATCH", "/api/cloud/network", Obj{"enabled": true}, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	state := a.driveNetworkStatus()
	state["results"] = []Obj{{"ip": driveSeedIP, "available": true, "verifiedAt": time.Now().Unix(), "latencyMs": 1}}
	if err := a.Store.Put("config", "drive-network", state); err != nil {
		t.Fatal(err)
	}
	tr, ip := a.driveTransport()
	if tr == nil || ip != driveSeedIP {
		t.Fatal("route not selected")
	}
	if tr2, _ := a.driveTransport(); tr2 != tr {
		t.Fatal("transport not reused")
	}
	transport := tr.(*http.Transport)
	if transport.Proxy != nil || (transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify) {
		t.Fatal("unsafe transport")
	}
	if _, err := transport.DialContext(context.Background(), "tcp", "example.com:443"); err == nil {
		t.Fatal("unrelated host accepted")
	}
	a.failDriveIP(ip)
	fallback, selected := a.driveTransport()
	if selected != "" || fallback != a.http.Transport {
		t.Fatal("no system fallback")
	}
	if flag(object(list(a.driveNetworkStatus()["results"])[0])["available"]) {
		t.Fatal("failed endpoint not persisted")
	}
	a.driveScanMu.Lock()
	w := apiTest(a, "POST", "/api/cloud/network/scan", Obj{}, true)
	a.driveScanMu.Unlock()
	if w.Code != 400 {
		t.Fatal("parallel scans accepted")
	}
}

func TestDriveFrontendRangeGenerationAndNeighbours(t *testing.T) {
	for _, ip := range []string{"142.250.196.100", "172.217.160.113", "74.125.204.155", "2001:4860:4860::8888"} {
		if !googleDriveIP(ip) {
			t.Fatalf("expected Google frontend IP %s to be accepted", ip)
		}
	}
	for _, poisoned := range []string{"88.191.249.183", "1.1.1.1", driveSeedIP, "127.0.0.1"} {
		if googleDriveIP(poisoned) {
			t.Fatalf("expected non-Google IP %s to be rejected by googleDriveIP", poisoned)
		}
	}
	neighbours := driveFrontendNeighbours("142.250.196.100")
	if len(neighbours) != len(driveFrontendOffsets)-1 {
		t.Fatalf("unexpected neighbour count: %d", len(neighbours))
	}
	for _, n := range neighbours {
		if !strings.HasPrefix(n, "142.250.196.") || n == "142.250.196.100" || !googleDriveIP(n) {
			t.Fatalf("invalid neighbour %s", n)
		}
	}
	sampled := sampleDriveFrontendIPs(4, nil)
	if len(sampled) != 4*len(driveFrontendOffsets) {
		t.Fatalf("sampled=%d", len(sampled))
	}
	for _, ip := range sampled {
		if !googleDriveIP(ip) {
			t.Fatalf("sampled non-Google IP %s", ip)
		}
	}
}

func TestDriveForgedGCNIdentityVerification(t *testing.T) {
	realHost := "www.googleapis.com"
	cert, roots := driveTestCertificate(t, driveForgedHost, false, x509.ExtKeyUsageServerAuth)
	policy := effectiveDrivePolicy(Obj{"sniMode": "original"}, driveForgedHost)
	if policy.Mode != "custom" || policy.ServerName != driveForgedHost {
		t.Fatalf("unexpected effective policy: %+v", policy)
	}
	cfg := driveTLSConfig(realHost, policy, roots)
	if cfg.ServerName != driveForgedHost {
		t.Fatalf("ServerName=%q want %q", cfg.ServerName, driveForgedHost)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}); err != nil {
		t.Fatalf("expected g.cn certificate to pass forged identity verification: %v", err)
	}
}

func TestDriveRequestMultiEndpointFailover(t *testing.T) {
	a := testApp(t)
	a.Store.Put("secrets", "google", Obj{"access_token": "tok", "refresh_token": "ref", "expires_at": time.Now().Add(time.Hour).Unix()})
	now := time.Now().Unix()
	state := Obj{
		"enabled":   true,
		"bypassTun": false,
		"results": []Obj{
			{"ip": "203.0.113.1", "available": true, "verifiedAt": now, "latencyMs": 5}, // blocked prefix, skipped
			{"ip": "142.250.196.100", "available": true, "verifiedAt": now, "latencyMs": 10, "sni": "g.cn"},
		},
	}
	if err := a.Store.Put("config", "drive-network", state); err != nil {
		t.Fatal(err)
	}
	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"user":{"emailAddress":"test@example.com"}}`)),
		}, nil
	})
	_, ip := a.driveTransport()
	if ip != "142.250.196.100" {
		t.Fatalf("selectedIP=%q", ip)
	}
	a.driveNetworkMu.Lock()
	a.driveIPHTTP.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, errors.New("simulated endpoint connection reset")
	}
	a.driveNetworkMu.Unlock()
	res, err := a.driveRequest(context.Background(), "GET", driveBase+"/about?fields=user", nil, nil)
	if err != nil {
		t.Fatalf("expected automatic failover to succeed, got: %v", err)
	}
	res.Body.Close()
	if got := str(a.driveNetworkStatus()["selectedIP"]); got != "" {
		t.Fatalf("expected dead primary IP to be marked unavailable, still selected %q", got)
	}
}

func TestMikanDirectMirrorRewrite(t *testing.T) {
	a := testApp(t)
	cert, roots := driveTestCertificate(t, "mikanime.tv", false, x509.ExtKeyUsageServerAuth)
	sniCh := make(chan string, 1)
	hostCh := make(chan string, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostCh <- r.Host
		_, _ = w.Write([]byte("<rss><channel><title>mikan</title></channel></rss>"))
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{cert},
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			sniCh <- hello.ServerName
			return nil, nil
		},
	}
	srv.StartTLS()
	defer srv.Close()

	state := Obj{
		"enabled":   true,
		"bypassTun": false,
		"results": []Obj{
			{"ip": "104.21.17.69", "available": true, "verifiedAt": time.Now().Unix(), "latencyMs": 120, "sni": "mirror:mikanime.tv"},
		},
	}
	if err := a.Store.Put("config", networkProbeStoreKey("mikan"), state); err != nil {
		t.Fatal(err)
	}

	rt, targetID, ip, rewriteHost := a.directTransportForHost("mikanani.me")
	if targetID != "mikan" || ip != "104.21.17.69" || rewriteHost != "mikanime.tv" {
		t.Fatalf("unexpected direct route: target=%q ip=%q rewrite=%q", targetID, ip, rewriteHost)
	}
	tr := rt.(*http.Transport)
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	configureHostTLS(tr, map[string]bool{"mikanani.me": true, "mikanime.tv": true}, "", driveTLSPolicy{Mode: "fragment"}, roots)

	client := &http.Client{Transport: a.http.Transport, Timeout: 3 * time.Second}
	res, err := client.Get("https://mikanani.me/RSS/Search?searchstr=test")
	if err != nil {
		t.Fatalf("direct mirror request failed: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "<rss>") {
		t.Fatalf("unexpected body: %q", string(body))
	}
	if gotSNI := <-sniCh; gotSNI != "mikanime.tv" {
		t.Fatalf("wire SNI = %q, want mikanime.tv", gotSNI)
	}
	if gotHost := <-hostCh; gotHost != "mikanime.tv" {
		t.Fatalf("HTTP Host = %q, want mikanime.tv", gotHost)
	}
}

func TestNyaaForgedDDoSGuardIdentity(t *testing.T) {
	a := testApp(t)
	spec, okSpec := a.findNetworkProbeTarget("nyaa")
	if !okSpec || spec.ForgedSNI != "ddos-guard.net" {
		t.Fatalf("unexpected nyaa target spec: ok=%v forged=%q", okSpec, spec.ForgedSNI)
	}

	cert, roots := driveTestCertificate(t, "ddos-guard.net", false, x509.ExtKeyUsageServerAuth)
	sniCh := make(chan string, 1)
	hostCh := make(chan string, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostCh <- r.Host
		_, _ = w.Write([]byte("<rss><channel><title>nyaa</title></channel></rss>"))
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{cert},
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			sniCh <- hello.ServerName
			return nil, nil
		},
	}
	srv.StartTLS()
	defer srv.Close()

	state := Obj{
		"enabled":   true,
		"bypassTun": false,
		"results": []Obj{
			{"ip": "186.2.163.20", "available": true, "verifiedAt": time.Now().Unix(), "latencyMs": 120, "sni": "ddos-guard.net"},
		},
	}
	if err := a.Store.Put("config", networkProbeStoreKey("nyaa"), state); err != nil {
		t.Fatal(err)
	}

	rt, targetID, ip, _ := a.directTransportForHost("nyaa.si")
	if targetID != "nyaa" || ip != "186.2.163.20" {
		t.Fatalf("unexpected nyaa direct route: target=%q ip=%q", targetID, ip)
	}
	tr := rt.(*http.Transport)
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	configureHostTLS(tr, map[string]bool{"nyaa.si": true}, spec.ForgedSNI, driveTLSPolicy{Mode: "custom", ServerName: "ddos-guard.net"}, roots)

	client := &http.Client{Transport: a.http.Transport, Timeout: 3 * time.Second}
	res, err := client.Get("https://nyaa.si/?page=rss&c=1_2&f=0&q=1080p")
	if err != nil {
		t.Fatalf("nyaa direct request failed: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "<rss>") {
		t.Fatalf("unexpected body: %q", string(body))
	}
	if gotSNI := <-sniCh; gotSNI != "ddos-guard.net" {
		t.Fatalf("wire SNI = %q, want ddos-guard.net", gotSNI)
	}
	if gotHost := <-hostCh; gotHost != "nyaa.si" {
		t.Fatalf("HTTP Host = %q, want nyaa.si", gotHost)
	}
}

func TestMikanInstantMagnetExtractionAndEnqueueFallback(t *testing.T) {
	a := testApp(t)
	const infoHash = "0123456789abcdef0123456789abcdef01234567"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><title>Mikan</title>
			<item>
				<guid>https://mikanani.me/Home/Episode/%s</guid>
				<link>https://mikanani.me/Home/Episode/%s</link>
				<title>[LoliHouse] Test Show - 01 [1080p]</title>
				<enclosure type="application/x-bittorrent" length="524288000" url="https://mikanani.me/Download/20261116/%s.torrent" />
			</item>
		</channel></rss>`, infoHash, infoHash, infoHash)
	}))
	defer srv.Close()

	if w := apiTest(a, "PUT", "/api/settings", Obj{"mikanBaseUrl": srv.URL, "providerMirrors": Obj{"mikan": srv.URL}}, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	items, err := a.fetchResources(context.Background(), "mikan", "Test")
	if err != nil || len(items) != 1 {
		t.Fatalf("fetchResources err=%v len=%d", err, len(items))
	}
	if str(items[0]["infoHash"]) != infoHash {
		t.Fatalf("infoHash=%q want %q", str(items[0]["infoHash"]), infoHash)
	}
	if str(items[0]["resourceKey"]) != infoHash {
		t.Fatalf("resourceKey=%q want %q", str(items[0]["resourceKey"]), infoHash)
	}
	mag := str(items[0]["magnetUrl"])
	if !strings.HasPrefix(mag, "magnet:?xt=urn:btih:"+infoHash) {
		t.Fatalf("magnetUrl=%q missing extracted infoHash", mag)
	}

	// Verify enqueue synthesizes magnet fallback even when only a bare Mikan .torrent URL is provided.
	torrentURL := "https://mikanani.me/Download/20261116/" + infoHash + ".torrent"
	job, err := a.enqueue(Obj{
		"url":        torrentURL,
		"sourceType": "torrent-url",
		"title":      "[LoliHouse] Test Show - 01 [1080p]",
	})
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	defer func() { _ = a.stopJob(str(job["gid"])) }()

	if str(job["infoHash"]) != infoHash {
		t.Fatalf("job infoHash=%q want %q", str(job["infoHash"]), infoHash)
	}
	if !strings.HasPrefix(str(job["fallbackUrl"]), "magnet:?xt=urn:btih:"+infoHash) {
		t.Fatalf("job fallbackUrl=%q want synthesized magnet", str(job["fallbackUrl"]))
	}
	if got := jobFallbackMagnet(Obj{"sourceType": "torrent-url", "sourceUrl": torrentURL, "title": "Ep01"}); !strings.HasPrefix(got, "magnet:?xt=urn:btih:"+infoHash) {
		t.Fatalf("jobFallbackMagnet=%q", got)
	}
}
