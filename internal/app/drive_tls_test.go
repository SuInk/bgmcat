package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
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
