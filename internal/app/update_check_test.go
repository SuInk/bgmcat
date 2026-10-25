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
	}{{"v0.10.0", "0.9.9", true}, {"v0.2.0", "0.2.0", false}, {"v0.1.99", "0.2.0", false}, {"v1.0.0-beta.1", "0.2.0", false}} {
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
