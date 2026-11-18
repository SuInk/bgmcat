package app

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestGzipAndBootstrap(t *testing.T) {
	a := testApp(t)
	a.cfg.Web = fstest.MapFS{
		"index.html":        &fstest.MapFile{Data: []byte("<!doctype html><html><body>" + strings.Repeat("bgmcat ", 100) + "</body></html>")},
		"assets/app-123.js": &fstest.MapFile{Data: []byte("console.log('" + strings.Repeat("hello world ", 120) + "');")},
	}

	// 1. Unauthenticated /api/bootstrap returns auth.authenticated=false without leaking settings
	req := httptest.NewRequest("GET", "/api/bootstrap", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected 200 gzipped bootstrap, got code=%d enc=%q", w.Code, w.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	rawBody, _ := io.ReadAll(zr)
	_ = zr.Close()
	var unauthPayload Obj
	if err := json.Unmarshal(rawBody, &unauthPayload); err != nil {
		t.Fatal(err)
	}
	data := object(unauthPayload["data"])
	if flag(object(data["auth"])["authenticated"]) || data["settings"] != nil {
		t.Fatalf("unauthenticated bootstrap leaked state: %+v", data)
	}

	// 2. Authenticated /api/bootstrap hydrates all initial dashboard state in 1 RTT
	reqAuth := httptest.NewRequest("GET", "/api/bootstrap", nil)
	reqAuth.Header.Set("Accept-Encoding", "gzip")
	reqAuth.Header.Set("Authorization", "Bearer "+str(a.Settings()["token"]))
	wAuth := httptest.NewRecorder()
	a.Handler().ServeHTTP(wAuth, reqAuth)
	if wAuth.Code != 200 || wAuth.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected 200 gzipped authed bootstrap, got %d", wAuth.Code)
	}
	zr2, err := gzip.NewReader(wAuth.Body)
	if err != nil {
		t.Fatal(err)
	}
	authedRaw, _ := io.ReadAll(zr2)
	_ = zr2.Close()
	var authPayload Obj
	if err := json.Unmarshal(authedRaw, &authPayload); err != nil {
		t.Fatal(err)
	}
	authedData := object(authPayload["data"])
	if !flag(object(authedData["auth"])["authenticated"]) || authedData["settings"] == nil || authedData["status"] == nil || authedData["downloads"] == nil {
		t.Fatalf("authenticated bootstrap missing sections: %+v", authedData)
	}

	// 3. Static JS asset serves pre-gzipped immutable response + ETag 304
	reqJS := httptest.NewRequest("GET", "/assets/app-123.js", nil)
	reqJS.Header.Set("Accept-Encoding", "gzip")
	wJS := httptest.NewRecorder()
	a.Handler().ServeHTTP(wJS, reqJS)
	if wJS.Code != 200 || wJS.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(wJS.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("unexpected static JS headers: code=%d headers=%v", wJS.Code, wJS.Header())
	}
	etag := wJS.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag on static asset")
	}

	req304 := httptest.NewRequest("GET", "/assets/app-123.js", nil)
	req304.Header.Set("If-None-Match", etag)
	w304 := httptest.NewRecorder()
	a.Handler().ServeHTTP(w304, req304)
	if w304.Code != 304 {
		t.Fatalf("expected 304 Not Modified, got %d", w304.Code)
	}
}
