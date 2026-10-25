package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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
