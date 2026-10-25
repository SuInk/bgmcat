package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a, e := New(Config{DataDir: t.TempDir(), Web: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Go UI")}}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Close() })
	return a
}
func apiTest(a *App, method, path string, payload any, auth bool) *httptest.ResponseRecorder {
	var b bytes.Buffer
	if payload != nil {
		json.NewEncoder(&b).Encode(payload)
	}
	r := httptest.NewRequest(method, path, &b)
	if auth {
		r.Header.Set("Authorization", "Bearer "+str(a.Settings()["token"]))
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func TestAuthAndSPARoutes(t *testing.T) {
	a := testApp(t)
	for _, path := range []string{"/api/settings", "/api/downloads", "/mcp/sse"} {
		if w := apiTest(a, "GET", path, nil, false); w.Code != 401 {
			t.Fatalf("%s %d", path, w.Code)
		}
	}
	if w := apiTest(a, "GET", "/mcp", nil, false); w.Code != 200 || w.Body.String() != "Go UI" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := apiTest(a, "GET", "/api/settings", nil, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := apiTest(a, "GET", "/internal/media/test?sig=bad", nil, false); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestSettingsValidation(t *testing.T) {
	a := testApp(t)
	w := apiTest(a, "PUT", "/api/settings", Obj{"unknown": "x", "cloudArchiveEnabled": false}, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, ok := a.Settings()["unknown"]; ok {
		t.Fatal("unknown persisted")
	}
	for _, o := range []Obj{{"token": "short"}, {"downloadClient": "aria2"}, {"cloudDeleteLocal": "yes"}, {"downloadDir": "relative"}} {
		if w := apiTest(a, "PUT", "/api/settings", o, true); w.Code != 400 {
			t.Fatal(o, w.Code)
		}
	}
}
func TestRSSRefreshDedupAndRules(t *testing.T) {
	a := testApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<rss version="2.0"><channel><title>Test</title><item><guid>one</guid><title>[Test] Anime - 01 [1080p]</title><enclosure url="https://example.test/one.torrent" type="application/x-bittorrent" /></item><item><guid>two</guid><title>Anime batch</title><enclosure url="https://example.test/two.torrent" /></item></channel></rss>`)
	}))
	defer server.Close()
	rule := defaultRule()
	rule["autoDownload"] = false
	s, e := a.newSubscription(Obj{"name": "Fixture", "url": server.URL, "rule": rule})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if _, e = a.refreshSubscription(context.Background(), s); e != nil {
			t.Fatal(e)
		}
	}
	items, e := a.Store.List("feed")
	if e != nil || len(items) != 2 {
		t.Fatal(items, e)
	}
	for _, x := range items {
		if str(x["guid"]) == "one" && (!flag(x["matched"]) || num(x["episodeNumber"]) != 1) {
			t.Fatal(x)
		}
		if str(x["guid"]) == "two" && flag(x["matched"]) {
			t.Fatal("batch allowed")
		}
	}
}
func TestLegacyHTTPPartialRestartAndRangePlayback(t *testing.T) {
	a := testApp(t)
	content := bytes.Repeat([]byte("0123456789"), 10000)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "episode.mp4", time.Time{}, bytes.NewReader(content))
	}))
	defer server.Close()
	dir := filepath.Join(a.cfg.DataDir, "fixture")
	os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, "episode.mp4")
	os.WriteFile(path+".part", content[:1234], 0600)
	j := Obj{"gid": "fixture", "directory": dir, "sourceUrl": server.URL + "/episode.mp4", "filePath": path}
	a.Store.Put("downloads", "fixture", j)
	if e := a.httpDownload(context.Background(), j); e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(got, content) {
		t.Fatal("legacy partial restart failed", e)
	}
	r := httptest.NewRequest("GET", "/api/downloads/fixture/stream?token="+str(a.Settings()["token"]), nil)
	r.Header.Set("Range", "bytes=20-29")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != string(content[20:30]) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = apiTest(a, "GET", "/api/downloads/fixture/stream", nil, false); w.Code != 401 {
		t.Fatal("unprotected playback")
	}
}
func TestHTTPWrongRangeRejected(t *testing.T) {
	a := testApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 99-103/104")
		w.WriteHeader(206)
		io.WriteString(w, "xxxxx")
	}))
	defer server.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	os.WriteFile(path+".part", []byte("keep"), 0600)
	e := a.httpDownload(context.Background(), Obj{"gid": "bad", "directory": dir, "sourceUrl": server.URL + "/file"})
	if e == nil {
		t.Fatal("bad range accepted")
	}
	b, _ := os.ReadFile(path + ".part")
	if string(b) != "keep" {
		t.Fatal("partial corrupted")
	}
}
func TestBackupRoundTrip(t *testing.T) {
	a := testApp(t)
	s, e := a.newSubscription(Obj{"name": "Saved", "url": "https://example.test/rss"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := a.backup()
	if e != nil {
		t.Fatal(e)
	}
	a.Store.Delete("subscriptions", str(s["id"]))
	raw, e := os.ReadFile(str(b["path"]))
	if e != nil {
		t.Fatal(e)
	}
	if e = a.restore(raw); e != nil {
		t.Fatal(e)
	}
	restored, _ := a.Store.Get("subscriptions", str(s["id"]))
	if str(restored["name"]) != "Saved" {
		t.Fatal(restored)
	}
}
func TestOAuthStateAndMD5Safety(t *testing.T) {
	a := testApp(t)
	if w := apiTest(a, "GET", "/api/cloud/callback?state=forged&code=abc", nil, false); w.Code != 400 {
		t.Fatal(w.Code)
	}
	file := Obj{"size": 10, "md5": "expected"}
	if e := verifyDriveFile(file, Obj{"size": "10", "md5Checksum": "wrong"}); e == nil || flag(file["verified"]) {
		t.Fatal("corrupt remote accepted")
	}
	if e := verifyDriveFile(file, Obj{"size": "10", "md5Checksum": "expected"}); e != nil {
		t.Fatal(e)
	}
	v := publicDownloads(Obj{"files": []Obj{{"session": "secret", "remoteId": "id"}}})
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "secret") {
		t.Fatal("session leaked")
	}
}
func TestDownloadWorkerCompletionAndMCP(t *testing.T) {
	a := testApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "test download") }))
	defer server.Close()
	j, e := a.enqueue(Obj{"url": server.URL + "/fixture.bin"})
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, _ = a.Store.Get("downloads", str(j["gid"]))
		if str(j["status"]) == "complete" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if str(j["status"]) != "complete" {
		t.Fatal(j)
	}
	w := apiTest(a, "POST", "/mcp", Obj{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "create_download") {
		t.Fatal(w.Body.String())
	}
	if w = apiTest(a, "POST", "/api/downloads/missing/pause", nil, true); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDriveArchiveCleanupAndCloudPlayback(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			a := testApp(t)
			a.Store.Patch("config", "settings", Obj{"cloudArchiveEnabled": true, "cloudDeleteLocal": true})
			a.Store.Put("secrets", "google", Obj{"access_token": "test-access", "refresh_token": "test-refresh", "expires_at": time.Now().Add(time.Hour).Unix()})
			dir := filepath.Join(a.cfg.DataDir, "download")
			os.MkdirAll(dir, 0700)
			path := filepath.Join(dir, "episode.mp4")
			content := []byte("a test movie payload")
			os.WriteFile(path, content, 0600)
			sum, _, _ := digestFile(context.Background(), path)
			uploaded := false
			a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "www.googleapis.com" || r.Header.Get("Authorization") != "Bearer test-access" {
					t.Errorf("unexpected request %s", r.URL.Host)
				}
				w := httptest.NewRecorder()
				switch {
				case strings.HasSuffix(r.URL.Path, "/generateIds"):
					io.WriteString(w, `{"ids":["remote-file"]}`)
				case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/upload/"):
					w.Header().Set("Location", "https://www.googleapis.com/upload/session-test")
					w.WriteHeader(200)
				case r.Method == "PUT":
					b, _ := io.ReadAll(r.Body)
					if !bytes.Equal(b, content) {
						t.Errorf("upload bytes mismatch")
					}
					uploaded = true
					w.WriteHeader(200)
				case r.URL.Query().Get("alt") == "media":
					http.ServeContent(w, r, "episode.mp4", time.Time{}, bytes.NewReader(content))
				case !uploaded:
					w.WriteHeader(404)
				default:
					md5 := sum
					if corrupt {
						md5 = "corrupt"
					}
					json.NewEncoder(w).Encode(Obj{"id": "remote-file", "size": fmt.Sprint(len(content)), "md5Checksum": md5})
				}
				return w.Result(), nil
			})
			j := Obj{"gid": "cloud", "status": "complete", "directory": dir, "files": []Obj{{"path": path, "size": len(content)}}}
			a.Store.Put("downloads", "cloud", j)
			e := a.archiveJob(j)
			if corrupt {
				if e == nil {
					t.Fatal("corrupt cloud accepted")
				}
				if b, _ := os.ReadFile(path); !bytes.Equal(b, content) {
					t.Fatal("local lost")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(path); !os.IsNotExist(e) {
				t.Fatal("local not cleaned", e)
			}
			stored, _ := a.Store.Get("downloads", "cloud")
			if str(object(stored["cloudArchive"])["status"]) != "archived" {
				t.Fatal(stored)
			}
			r := httptest.NewRequest("GET", "/api/downloads/cloud/stream?token="+str(a.Settings()["token"]), nil)
			r.Header.Set("Range", "bytes=2-5")
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != 206 || w.Body.String() != string(content[2:6]) {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestEpisodePreferenceDoesNotDownloadBoth(t *testing.T) {
	a := testApp(t)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "payload") }))
	defer source.Close()
	rule := defaultRule()
	rule["preferredResolutions"] = []string{"1080p", "720p"}
	s, e := a.newSubscription(Obj{"name": "Anime", "url": source.URL, "animeId": "show", "rule": rule})
	if e != nil {
		t.Fatal(e)
	}
	for i, res := range []string{"720p", "1080p"} {
		id := i + 20
		a.Store.Put("feed", str(id), Obj{"id": id, "subscriptionId": s["id"], "title": "Anime - 01 [" + res + "]", "matched": true, "episodeNumber": 1, "sourceUrl": source.URL + "/" + res, "sourceType": "direct", "resourceKey": res, "downloadStatus": "pending"})
	}
	if n := a.queuePreferred(s); n != 1 {
		t.Fatal(n)
	}
	jobs, _ := a.Store.List("downloads")
	if len(jobs) != 1 || !strings.Contains(str(jobs[0]["sourceUrl"]), "1080p") {
		t.Fatal(jobs)
	}
	if n := a.queuePreferred(s); n != 0 {
		t.Fatal("duplicate episode", n)
	}
}
