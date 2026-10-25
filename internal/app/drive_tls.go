package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

type driveTLSPolicy struct{ Mode, ServerName string }

func drivePolicy(state Obj) driveTLSPolicy {
	mode := str(state["sniMode"])
	if mode == "" {
		mode = "original"
	}
	return driveTLSPolicy{mode, str(state["sniHost"])}
}
func (p driveTLSPolicy) key() string { return p.Mode + ":" + p.ServerName }
func (p driveTLSPolicy) validate() error {
	if p.Mode == "original" || p.Mode == "none" {
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

func driveTLSConfig(realHost string, policy driveTLSPolicy, roots *x509.CertPool) *tls.Config {
	sni := realHost
	if policy.Mode == "none" {
		sni = ""
	} else if policy.Mode == "custom" {
		sni = policy.ServerName
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12, ServerName: sni, NextProtos: []string{"http/1.1"},
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
			return err
		},
	}
}

// Install a TLS dialer only for a selected Drive route. HTTP Host and certificate
// identity always remain the URL's original host; OAuth never uses this transport.
func configureDriveTLS(tr *http.Transport, policy driveTLSPolicy, roots *x509.CertPool) {
	// Explicit HTTP/1.1 avoids ALPN/Transport protocol mismatches with a custom dialer.
	tr.ForceAttemptHTTP2 = false
	tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	tr.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if err := policy.validate(); err != nil {
			return nil, err
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "443" || (host != "www.googleapis.com" && host != "drive.usercontent.google.com") {
			return nil, errors.New("TLS 直连仅允许 Google Drive HTTPS 地址")
		}
		conn, err := tr.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		secured := tls.Client(conn, driveTLSConfig(host, policy, roots))
		handshake, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := secured.HandshakeContext(handshake); err != nil {
			conn.Close()
			return nil, err
		}
		return secured, nil
	}
}
