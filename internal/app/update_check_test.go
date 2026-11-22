package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestReleaseCheckStates(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-token")
	for _, tc := range []struct {
		name, body, kind string
		code             int
	}{
		{"empty", "[]", "no_release", 200},
		{"new", `[{"tag_name":"v999.0.0","body":"release notes","published_at":"2026-10-01T00:00:00Z"},{"tag_name":"v1000.0.0-beta.1","prerelease":true}]`, "available", 200},
		{"private", "{}", "error", 404},
		{"invalid", "not json", "error", 200},
		{"prerelease-only", `[{"tag_name":"v999.0.0-beta.1","prerelease":true}]`, "no_release", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Fatal("wrong release request")
				}
				return &http.Response{StatusCode: tc.code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			s := a.checkRelease(context.Background())
			if str(s["kind"]) != tc.kind || flag(s["supported"]) || str(s["checkedAt"]) == "" {
				t.Fatal(s)
			}
			if str(a.updateState()["kind"]) != tc.kind {
				t.Fatal("state not retained")
			}
			if tc.kind == "error" && str(s["lastError"]) == "" {
				t.Fatal("missing diagnostic")
			}
			if tc.name == "new" {
				h, _ := a.Store.Get("updates", "history")
				if len(list(h["items"])) != 2 {
					t.Fatal(h)
				}
			}
		})
	}
}
func TestReleaseVersionCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"v0.10.0", "0.9.9", true}, {"v0.2.0", "0.2.0", false}, {"v0.1.99", "0.2.0", false},
		{"v1.0.0-beta.1", "0.2.0", true},
		// 正式版高于同号的预发布，Canary 序号按数字比较。
		{"v0.2.2", "0.2.2-canary.9", true}, {"v0.2.2-canary.9", "0.2.2", false},
		{"v0.2.2-canary.10", "0.2.2-canary.9", true}, {"v0.2.2-canary.1", "0.2.1", true},
		{"v0.2.2-rc.1", "0.2.2-canary.3", true}, {"v0.2.2-canary.3", "0.2.2-canary.3", false},
		{"v0.2.2-canary.3", "abc", false}, {"main", "0.2.1", false},
	} {
		if newerRelease(tc.a, tc.b) != tc.want {
			t.Fatal(tc)
		}
	}
}

func TestReleaseCheckFindsStableBeyondRecentHistory(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-token")
	a := testApp(t)
	calls := 0
	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body := `[` + strings.TrimSuffix(strings.Repeat(`{"tag_name":"v999.0.0-beta.1","prerelease":true},`, 30), ",") + `]`
		if strings.HasSuffix(r.URL.Path, "/latest") {
			body = `{"tag_name":"v998.0.0","published_at":"2026-10-01T00:00:00Z"}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	s := a.checkRelease(context.Background())
	if calls != 2 || str(s["kind"]) != "available" || str(s["latestVersion"]) != "v998.0.0" {
		t.Fatal(s, calls)
	}
}

func TestReleaseCheckChannels(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-token")
	body := `[{"tag_name":"v0.2.2-canary.3","prerelease":true},{"tag_name":"v0.2.1","published_at":"2026-10-01T00:00:00Z"}]`
	for _, tc := range []struct {
		channel, current, latest, kind string
	}{
		{"stable", "0.2.1", "v0.2.1", "up_to_date"},
		{"canary", "0.2.1", "v0.2.2-canary.3", "available"},
		{"canary", "0.2.2-canary.3", "v0.2.2-canary.3", "up_to_date"},
		// 装着 Canary 切回稳定版：等正式版追上来再提示，不当成可升级。
		{"stable", "0.2.2-canary.3", "v0.2.1", "up_to_date"},
		{"bogus", "0.2.1", "v0.2.1", "up_to_date"},
	} {
		t.Run(tc.channel+"-"+tc.current, func(t *testing.T) {
			old := Version
			Version = tc.current
			t.Cleanup(func() { Version = old })
			a := testApp(t)
			if _, e := a.Store.Patch("config", "settings", Obj{"updateChannel": tc.channel}); e != nil {
				t.Fatal(e)
			}
			a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			s := a.checkRelease(context.Background())
			if str(s["latestVersion"]) != tc.latest || str(s["kind"]) != tc.kind {
				t.Fatal(s)
			}
		})
	}
}

func TestBuildChannel(t *testing.T) {
	for v, want := range map[string]string{"0.2.1": "stable", "0.2.2-canary.4": "canary", "0.3.0-beta.1": "beta", "dev": "stable"} {
		if got := buildChannel(v); got != want {
			t.Fatalf("%s: got %s want %s", v, got, want)
		}
	}
}

func TestUpdatePolicyChannel(t *testing.T) {
	a := testApp(t)
	if w := apiTest(a, "PUT", "/api/system/update/policy", Obj{"channel": "canary"}, true); w.Code != 200 || !strings.Contains(w.Body.String(), `"channel":"canary"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// 只改通道时其他更新设置保持原值。
	s := a.Settings()
	if s["updateChannel"] != "canary" || s["updateAutoDownload"] != true || s["updateMirror"] != "auto" {
		t.Fatal(s)
	}
	if w := apiTest(a, "PUT", "/api/system/update/policy", Obj{"channel": "nightly"}, true); w.Code == 200 {
		t.Fatal("invalid channel accepted")
	}
	if w := apiTest(a, "GET", "/api/system/update/policy", nil, true); !strings.Contains(w.Body.String(), `"channel":"canary"`) {
		t.Fatal(w.Body.String())
	}
}
