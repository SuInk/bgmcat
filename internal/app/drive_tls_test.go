package app

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func driveTestCertificate(t *testing.T, name string, expired bool, usage x509.ExtKeyUsage) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	rootDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{name}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	if expired {
		leaf.NotAfter = time.Now().Add(-time.Minute)
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	return tls.Certificate{Certificate: [][]byte{der, rootDER}, PrivateKey: leafKey}, roots
}
func TestDriveTLSWireSNIAndOriginalIdentity(t *testing.T) {
	for _, mode := range []string{"original", "none", "custom"} {
		for _, kind := range []string{"valid", "wrong-name", "expired", "untrusted", "wrong-usage"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				realHost := "www.googleapis.com"
				name := realHost
				usage := x509.ExtKeyUsageServerAuth
				if kind == "wrong-name" {
					name = "cover.example"
				}
				if kind == "wrong-usage" {
					usage = x509.ExtKeyUsageClientAuth
				}
				cert, roots := driveTestCertificate(t, name, kind == "expired", usage)
				if kind == "untrusted" {
					roots = x509.NewCertPool()
				}
				sni := make(chan string, 1)
				var requests atomic.Int32
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Host != realHost {
						t.Errorf("Host changed to %s", r.Host)
					}
					w.Write([]byte("verified"))
				}))
				server.Config.ErrorLog = log.New(io.Discard, "", 0)
				server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) { sni <- hello.ServerName; return nil, nil }}
				server.StartTLS()
				defer server.Close()
				policy := driveTLSPolicy{Mode: mode}
				expected := realHost
				if mode == "none" {
					expected = ""
				}
				if mode == "custom" {
					policy.ServerName = "cover.example"
					expected = policy.ServerName
				}
				transport := driveIPTransport("8.8.8.8", policy)
				transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				}
				if mode == "original" {
					transport.TLSClientConfig = &tls.Config{RootCAs: roots}
				} else {
					configureDriveTLS(transport, policy, roots)
				}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: time.Second}
				response, err := client.Get("https://" + realHost + "/test")
				if response != nil {
					response.Body.Close()
				}
				if got := <-sni; got != expected {
					t.Fatalf("wire SNI=%q want %q", got, expected)
				}
				if kind == "valid" {
					if err != nil || requests.Load() != 1 {
						t.Fatalf("valid real-host certificate rejected: %v", err)
					}
				} else {
					if err == nil || requests.Load() != 0 {
						t.Fatalf("invalid certificate accepted or HTTP sent: %v", err)
					}
				}
			})
		}
	}
}
func TestDriveTLSSettingsInvalidateRoutes(t *testing.T) {
	a := testApp(t)
	state := Obj{"enabled": true, "results": []Obj{{"ip": driveSeedIP, "available": true, "verifiedAt": time.Now().Unix(), "latencyMs": 1}}}
	a.Store.Put("config", "drive-network", state)
	old, _ := a.driveTransport()
	w := apiTest(a, "PATCH", "/api/cloud/network", Obj{"sniMode": "custom", "sniHost": " Cover.Example "}, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	state = a.driveNetworkStatus()
	if !flag(state["enabled"]) || str(state["sniHost"]) != "cover.example" || len(list(state["results"])) != 0 || str(state["selectedIP"]) != "" {
		t.Fatal(state)
	}
	state["results"] = []Obj{{"ip": driveSeedIP, "available": true, "verifiedAt": time.Now().Unix(), "latencyMs": 1}}
	a.Store.Put("config", "drive-network", state)
	next, _ := a.driveTransport()
	if old == next || next.(*http.Transport).DialTLSContext == nil {
		t.Fatal("TLS transport not replaced")
	}
	for _, host := range []string{"https://example.com", "example.com:443", "*.example.com", "a..b", "-bad.example", "127.0.0.1"} {
		if w := apiTest(a, "PATCH", "/api/cloud/network", Obj{"sniMode": "custom", "sniHost": host}, true); w.Code != 400 {
			t.Errorf("accepted %s", host)
		}
	}
	if str(a.driveNetworkStatus()["sniHost"]) != "cover.example" {
		t.Fatal("invalid patch changed state")
	}
	if w := apiTest(a, "PATCH", "/api/cloud/network", Obj{"sniMode": "none"}, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := apiTest(a, "PATCH", "/api/cloud/network", Obj{"sniMode": "unknown"}, true); w.Code != 400 {
		t.Fatal("invalid mode accepted")
	}
}

func TestDriveTLSCancellationAndHostRestriction(t *testing.T) {
	tr := driveIPTransport("8.8.8.8", driveTLSPolicy{Mode: "none"})
	if _, err := tr.DialTLSContext(context.Background(), "tcp", "oauth2.googleapis.com:443"); err == nil {
		t.Fatal("OAuth entered custom TLS transport")
	}
	client, server := net.Pipe()
	defer server.Close()
	closed := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, server); close(closed) }()
	tr.DialContext = func(context.Context, string, string) (net.Conn, error) { return client, nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tr.DialTLSContext(ctx, "tcp", "www.googleapis.com:443"); err == nil {
		t.Fatal("cancelled handshake succeeded")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("failed handshake leaked connection")
	}
}

func TestDriveTrustedGoogleCamouflageSNIs(t *testing.T) {
	realHost := "www.googleapis.com"
	for _, tc := range []struct {
		sni  string
		want bool
	}{
		{sni: "dl.google.com", want: true},
		{sni: "g.cn", want: true},
		{sni: "www.gstatic.cn", want: true},
		{sni: "services.googleapis.cn", want: true},
		{sni: "cover.example", want: false},
	} {
		t.Run(tc.sni, func(t *testing.T) {
			cert, roots := driveTestCertificate(t, tc.sni, false, x509.ExtKeyUsageServerAuth)
			policy := driveTLSPolicy{Mode: "custom", ServerName: tc.sni}
			cfg := driveTLSConfig(realHost, policy, roots)
			leaf, err := x509.ParseCertificate(cert.Certificate[0])
			if err != nil {
				t.Fatal(err)
			}
			verifyErr := cfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}})
			if tc.want && verifyErr != nil {
				t.Fatalf("expected Google SNI %q certificate to pass verification: %v", tc.sni, verifyErr)
			}
			if !tc.want && verifyErr == nil {
				t.Fatalf("expected non-Google SNI %q certificate to fail verification", tc.sni)
			}
		})
	}
}

func makeTestECHConfigAndKey(t *testing.T, id uint8, publicName string, sendAsRetry bool) ([]byte, []byte, tls.EncryptedClientHelloKey) {
	t.Helper()
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.PublicKey().Bytes()
	var body []byte
	body = append(body, id)
	body = binary.BigEndian.AppendUint16(body, 0x0020) // DHKEM(X25519, HKDF-SHA256)
	body = binary.BigEndian.AppendUint16(body, uint16(len(pub)))
	body = append(body, pub...)
	body = binary.BigEndian.AppendUint16(body, 4)      // cipher suites len
	body = binary.BigEndian.AppendUint16(body, 0x0001) // HKDF-SHA256
	body = binary.BigEndian.AppendUint16(body, 0x0001) // AES-128-GCM
	body = append(body, 64)                            // maximum_name_length
	body = append(body, byte(len(publicName)))
	body = append(body, publicName...)
	body = binary.BigEndian.AppendUint16(body, 0) // extensions len

	var cfg []byte
	cfg = binary.BigEndian.AppendUint16(cfg, 0xfe0d) // ECH draft-18 / RFC 9849
	cfg = binary.BigEndian.AppendUint16(cfg, uint16(len(body)))
	cfg = append(cfg, body...)

	var list []byte
	list = binary.BigEndian.AppendUint16(list, uint16(len(cfg)))
	list = append(list, cfg...)

	key := tls.EncryptedClientHelloKey{
		Config:      cfg,
		PrivateKey:  priv.Bytes(),
		SendAsRetry: sendAsRetry,
	}
	return cfg, list, key
}

func TestCloudflareECHHandshakeAndRetryRecovery(t *testing.T) {
	realHost := "mikanani.me"
	publicName := "cloudflare-ech.com"
	t.Cleanup(func() { deleteCachedECHConfigList(realHost) })

	rootKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	rootDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(rootCert)

	signLeaf := func(serial int64, name string) tls.Certificate {
		leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		leaf := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			DNSNames:     []string{name},
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
		}
		der, cErr := x509.CreateCertificate(rand.Reader, leaf, rootCert, &leafKey.PublicKey, rootKey)
		if cErr != nil {
			t.Fatal(cErr)
		}
		return tls.Certificate{Certificate: [][]byte{der, rootDER}, PrivateKey: leafKey}
	}

	outerCert := signLeaf(2, publicName)
	innerCert := signLeaf(3, realHost)

	_, staleList, _ := makeTestECHConfigAndKey(t, 1, publicName, false)
	_, activeList, activeKey := makeTestECHConfigAndKey(t, 2, publicName, true)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != realHost {
			t.Errorf("Host = %q, want %q", r.Host, realHost)
		}
		_, _ = w.Write([]byte("<rss>ech-ok</rss>"))
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.TLS = &tls.Config{
		MinVersion:               tls.VersionTLS13,
		Certificates:             []tls.Certificate{outerCert, innerCert},
		EncryptedClientHelloKeys: []tls.EncryptedClientHelloKey{activeKey},
	}
	srv.StartTLS()
	defer srv.Close()

	// 1. Direct ECH handshake with active config
	updateCachedECHConfigList(realHost, activeList)
	tr1 := driveIPTransport("162.159.135.42", driveTLSPolicy{Mode: "ech"})
	tr1.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	configureHostTLSWithBypass(tr1, map[string]bool{realHost: true}, "", false, driveTLSPolicy{Mode: "ech"}, roots)
	defer tr1.CloseIdleConnections()

	client1 := &http.Client{Transport: tr1, Timeout: 3 * time.Second}
	res1, err := client1.Get("https://" + realHost + "/RSS/Search")
	if err != nil {
		t.Fatalf("direct ECH handshake failed: %v", err)
	}
	body1, _ := io.ReadAll(res1.Body)
	res1.Body.Close()
	if res1.StatusCode != 200 || !strings.Contains(string(body1), "ech-ok") {
		t.Fatalf("unexpected direct ECH response: %d %q", res1.StatusCode, string(body1))
	}
	if res1.TLS == nil || !res1.TLS.ECHAccepted || res1.TLS.Version != tls.VersionTLS13 {
		t.Fatalf("expected TLS 1.3 with ECHAccepted=true, got %+v", res1.TLS)
	}

	// 2. Stale ECH config triggers ECHRejectionError -> cache update -> automatic retry -> 200 OK
	updateCachedECHConfigList(realHost, staleList)
	tr2 := driveIPTransport("162.159.135.42", driveTLSPolicy{Mode: "ech"})
	tr2.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	configureHostTLSWithBypass(tr2, map[string]bool{realHost: true}, "", false, driveTLSPolicy{Mode: "ech"}, roots)
	defer tr2.CloseIdleConnections()

	client2 := &http.Client{Transport: tr2, Timeout: 3 * time.Second}
	res2, err := client2.Get("https://" + realHost + "/RSS/Search")
	if err != nil {
		t.Fatalf("ECH retry recovery failed: %v", err)
	}
	body2, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if res2.StatusCode != 200 || !strings.Contains(string(body2), "ech-ok") {
		t.Fatalf("unexpected retry ECH response: %d %q", res2.StatusCode, string(body2))
	}
	if res2.TLS == nil || !res2.TLS.ECHAccepted {
		t.Fatalf("expected ECHAccepted=true after retry recovery")
	}
	gotCached := resolveECHConfigList(context.Background(), realHost, false)
	if len(gotCached) != len(activeList) || string(gotCached) != string(activeList) {
		t.Fatalf("expected echCache updated with RetryConfigList")
	}
}
