package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCloudSetupCheck(t *testing.T) {
	for _, scenario := range []string{"success", "upload-failed", "mismatch", "cleanup-failed"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			a.Store.Put("secrets", "google", Obj{"access_token": "test", "refresh_token": "test", "expires_at": time.Now().Add(time.Hour).Unix()})
			var uploaded string
			deleted := false
			a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				code, data := 200, "{}"
				if r.Header.Get("Authorization") != "Bearer test" {
					t.Fatal("missing authentication")
				}
				switch r.Method {
				case "POST":
					var payload Obj
					json.NewDecoder(r.Body).Decode(&payload)
					if str(payload["mimeType"]) != "text/plain" || !strings.HasPrefix(str(payload["name"]), ".bgmcat-connection-test-") {
						t.Fatal(payload)
					}
					data = `{"id":"new-test-file"}`
				case "PATCH":
					if r.URL.Query().Get("uploadType") != "media" {
						t.Fatal(r.URL)
					}
					b, _ := io.ReadAll(r.Body)
					uploaded = string(b)
					if scenario == "upload-failed" {
						code = 403
					}
				case "GET":
					if r.URL.Query().Get("alt") != "media" {
						t.Fatal(r.URL)
					}
					data = uploaded
					if scenario == "mismatch" {
						data = "incorrect"
					}
				case "DELETE":
					if r.URL.Path != "/drive/v3/files/new-test-file" {
						t.Fatal("unsafe deletion", r.URL)
					}
					deleted = true
					code = 204
					if scenario == "cleanup-failed" {
						code = 403
					}
				default:
					t.Fatal(r.Method)
				}
				return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(data))}, nil
			})
			w := apiTest(a, "POST", "/api/cloud/check", Obj{"folderId": "root"}, true)
			if !deleted {
				t.Fatal("temporary file not cleaned")
			}
			if (w.Code == 200) != (scenario == "success") {
				t.Fatal(w.Code, w.Body.String())
			}
			if scenario == "cleanup-failed" && !strings.Contains(w.Body.String(), "清理失败") {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestCloudSetupFolders(t *testing.T) {
	a := testApp(t)
	a.Store.Put("secrets", "google", Obj{"access_token": "test", "expires_at": time.Now().Add(time.Hour).Unix()})
	calls := 0
	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		q := r.URL.Query()
		if !strings.Contains(q.Get("q"), "'parent-folder' in parents") || q.Get("pageToken") != "next-page" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"files":[{"id":"child","name":"Anime"}],"nextPageToken":"last-page"}`))}, nil
	})
	w := apiTest(a, "GET", "/api/cloud/folders?parent=parent-folder&pageToken=next-page", nil, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "last-page") {
		t.Fatal(w.Body.String())
	}
	for _, route := range []string{"/api/cloud/folders", "/api/cloud/check"} {
		method := "POST"
		if route == "/api/cloud/folders" {
			method = "GET"
		}
		if w := apiTest(a, method, route, nil, false); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	if w := apiTest(a, "GET", "/api/cloud/folders?parent=bad%27id", nil, true); w.Code == 200 {
		t.Fatal("invalid ID accepted")
	}
	if calls != 1 {
		t.Fatal("invalid requests reached Google")
	}
}
