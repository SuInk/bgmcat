package app

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDrive is an in-memory Google Drive that follows the resumable upload
// protocol the way the real one does: 308 + "Range: bytes=0-N" after each
// non-final chunk, 200 with file metadata after the last, "bytes */size"
// status queries, sessions that can expire, and access tokens that can expire.
type fakeDrive struct {
	t  *testing.T
	mu sync.Mutex

	token        string // currently valid access token
	refreshCount int

	files    map[string]*fakeDriveFile
	sessions map[string]*fakeDriveSession
	nextID   int

	bytesReceived int64 // every chunk byte the server accepted, duplicates included

	// fault injection
	failChunkAt   int           // abort the Nth chunk PUT at the transport (1-based), 0 = never
	chunkCount    int           // chunk PUTs seen
	expireSession bool          // next status query on an existing session answers 404
	chunkDelay    time.Duration // simulated slow upstream per chunk
}

type fakeDriveFile struct {
	name    string
	parents []string
	data    []byte
}

type fakeDriveSession struct {
	id      string
	name    string
	parents []string
	size    int64
	data    []byte
}

func newFakeDrive(t *testing.T) *fakeDrive {
	return &fakeDrive{t: t, token: "access-1", files: map[string]*fakeDriveFile{}, sessions: map[string]*fakeDriveSession{}}
}

func (d *fakeDrive) RoundTrip(r *http.Request) (*http.Response, error) {
	d.mu.Lock()
	if r.URL.Host == "oauth2.googleapis.com" {
		defer d.mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "refresh-1" {
			return d.reply(r, 400, "", nil), nil
		}
		d.refreshCount++
		d.token = fmt.Sprintf("access-%d", d.refreshCount+1)
		return d.reply(r, 200, fmt.Sprintf(`{"access_token":%q,"expires_in":3600}`, d.token), nil), nil
	}
	if r.URL.Host != "www.googleapis.com" {
		d.mu.Unlock()
		d.t.Errorf("request to unexpected host %s", r.URL.Host)
		return nil, errors.New("unexpected host")
	}
	if r.Header.Get("Authorization") != "Bearer "+d.token {
		d.mu.Unlock()
		return d.reply(r, 401, `{"error":"invalid_token"}`, nil), nil
	}
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/files/generateIds"):
		defer d.mu.Unlock()
		d.nextID++
		return d.reply(r, 200, fmt.Sprintf(`{"ids":["file-%d"]}`, d.nextID), nil), nil

	case r.Method == "POST" && path == "/upload/drive/v3/files" && r.URL.Query().Get("uploadType") == "resumable":
		defer d.mu.Unlock()
		var meta struct {
			ID      string   `json:"id"`
			Name    string   `json:"name"`
			Parents []string `json:"parents"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &meta); err != nil || meta.ID == "" {
			return d.reply(r, 400, "", nil), nil
		}
		size, _ := strconv.ParseInt(r.Header.Get("X-Upload-Content-Length"), 10, 64)
		key := fmt.Sprintf("/upload/drive/v3/files?upload_id=session-%d", len(d.sessions)+1)
		d.sessions[key] = &fakeDriveSession{id: meta.ID, name: meta.Name, parents: meta.Parents, size: size}
		return d.reply(r, 200, "", http.Header{"Location": {"https://www.googleapis.com" + key}}), nil

	case r.Method == "PUT" && path == "/upload/drive/v3/files":
		key := path + "?" + r.URL.RawQuery
		s := d.sessions[key]
		if s == nil {
			d.mu.Unlock()
			return d.reply(r, 404, "", nil), nil
		}
		contentRange := r.Header.Get("Content-Range")
		if strings.HasPrefix(contentRange, "bytes */") { // status query
			defer d.mu.Unlock()
			if d.expireSession {
				d.expireSession = false
				delete(d.sessions, key)
				return d.reply(r, 404, "", nil), nil
			}
			return d.progress(r, s), nil
		}
		d.chunkCount++
		if d.failChunkAt > 0 && d.chunkCount == d.failChunkAt {
			d.mu.Unlock()
			return nil, errors.New("connection reset by peer")
		}
		delay := d.chunkDelay
		d.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		var start, end, total int64
		if _, err := fmt.Sscanf(contentRange, "bytes %d-%d/%d", &start, &end, &total); err != nil || total != s.size {
			return d.reply(r, 400, "", nil), nil
		}
		if start != int64(len(s.data)) {
			d.t.Errorf("chunk starts at %d, server has %d bytes", start, len(s.data))
			return d.reply(r, 400, "", nil), nil
		}
		chunk, _ := io.ReadAll(r.Body)
		if int64(len(chunk)) != end-start+1 {
			return d.reply(r, 400, "", nil), nil
		}
		d.bytesReceived += int64(len(chunk))
		s.data = append(s.data, chunk...)
		if int64(len(s.data)) < s.size {
			return d.progress(r, s), nil
		}
		d.files[s.id] = &fakeDriveFile{name: s.name, parents: s.parents, data: s.data}
		delete(d.sessions, key)
		return d.reply(r, 200, d.metaJSON(s.id), nil), nil

	case r.Method == "GET" && strings.HasPrefix(path, "/drive/v3/files/"):
		defer d.mu.Unlock()
		id := strings.TrimPrefix(path, "/drive/v3/files/")
		f := d.files[id]
		if f == nil {
			return d.reply(r, 404, `{"error":"notFound"}`, nil), nil
		}
		if r.URL.Query().Get("alt") == "media" {
			w := httptest.NewRecorder()
			http.ServeContent(w, r, f.name, time.Time{}, bytes.NewReader(f.data))
			return w.Result(), nil
		}
		return d.reply(r, 200, d.metaJSON(id), nil), nil
	}
	d.mu.Unlock()
	d.t.Errorf("unhandled %s %s", r.Method, r.URL)
	return d.reply(r, 400, "", nil), nil
}

func (d *fakeDrive) progress(r *http.Request, s *fakeDriveSession) *http.Response {
	h := http.Header{}
	if len(s.data) > 0 {
		h.Set("Range", fmt.Sprintf("bytes=0-%d", len(s.data)-1))
	}
	return d.reply(r, 308, "", h)
}

func (d *fakeDrive) metaJSON(id string) string {
	f := d.files[id]
	sum := md5.Sum(f.data)
	b, _ := json.Marshal(Obj{"id": id, "size": fmt.Sprint(len(f.data)), "md5Checksum": hex.EncodeToString(sum[:])})
	return string(b)
}

func (d *fakeDrive) reply(r *http.Request, status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r, ContentLength: int64(len(body))}
}

// driveArchiveFixture writes a completed download of size bytes and wires the
// app to the fake Drive.
func driveArchiveFixture(t *testing.T, size int) (*App, *fakeDrive, Obj, []byte) {
	t.Helper()
	a := testApp(t)
	drive := newFakeDrive(t)
	a.http.Transport = drive
	a.Store.Patch("config", "settings", Obj{"cloudArchiveEnabled": true, "cloudDeleteLocal": false, "cloudDriveFolderId": "folder-anime", "googleClientId": "client", "googleClientSecret": "secret"})
	a.Store.Put("secrets", "google", Obj{"access_token": "access-1", "refresh_token": "refresh-1", "expires_at": time.Now().Add(time.Hour).Unix()})
	dir := filepath.Join(a.cfg.DataDir, "downloads", "job")
	os.MkdirAll(dir, 0700)
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i*7 + i/4096)
	}
	path := filepath.Join(dir, "[Group] Show - 04 [1080p].mkv")
	os.WriteFile(path, content, 0600)
	j := Obj{"gid": "job", "status": "complete", "directory": dir, "files": []Obj{{"path": path, "size": size}}}
	a.Store.Put("downloads", "job", j)
	stored, _ := a.Store.Get("downloads", "job")
	return a, drive, stored, content
}

func assertArchived(t *testing.T, a *App, drive *fakeDrive, content []byte) {
	t.Helper()
	stored, _ := a.Store.Get("downloads", "job")
	if s := str(object(stored["cloudArchive"])["status"]); s != "verified" {
		t.Fatalf("cloudArchive = %v", stored["cloudArchive"])
	}
	file := object(list(stored["files"])[0])
	if file["session"] != nil || !flag(file["verified"]) {
		t.Fatalf("file state after upload = %v", file)
	}
	remote := drive.files[str(file["remoteId"])]
	if remote == nil || !bytes.Equal(remote.data, content) {
		t.Fatal("cloud copy missing or different")
	}
	if remote.name != "job-[Group] Show - 04 [1080p].mkv" || len(remote.parents) != 1 || remote.parents[0] != "folder-anime" {
		t.Fatalf("cloud file %q in %v", remote.name, remote.parents)
	}
	if _, err := os.Stat(str(file["path"])); err != nil {
		t.Fatal("local file removed although cloudDeleteLocal is off", err)
	}
}

func TestDriveUploadMultiChunk(t *testing.T) {
	size := 20*1024*1024 + 12345 // three 8 MiB chunks, last one partial
	a, drive, j, content := driveArchiveFixture(t, size)
	if err := a.archiveJob(j); err != nil {
		t.Fatal(err)
	}
	assertArchived(t, a, drive, content)
	if drive.chunkCount != 3 || drive.bytesReceived != int64(size) {
		t.Fatalf("chunks=%d received=%d", drive.chunkCount, drive.bytesReceived)
	}
}

func TestDriveUploadResumesAfterInterruption(t *testing.T) {
	size := 20 * 1024 * 1024
	a, drive, j, content := driveArchiveFixture(t, size)
	drive.failChunkAt = 2
	if err := a.archiveJob(j); err == nil {
		t.Fatal("interrupted upload reported success")
	}
	stored, _ := a.Store.Get("downloads", "job")
	file := object(list(stored["files"])[0])
	if str(file["session"]) == "" || num(file["uploadedBytes"]) != 8*1024*1024 {
		t.Fatalf("progress not saved for resume: %v", file)
	}

	// Next archive round (e.g. after an app restart) picks up the saved session.
	drive.failChunkAt = 0
	if err := a.archiveJob(stored); err != nil {
		t.Fatal(err)
	}
	assertArchived(t, a, drive, content)
	if drive.bytesReceived != int64(size) {
		t.Fatalf("server received %d bytes for a %d byte file: first chunk was re-sent", drive.bytesReceived, size)
	}
}

func TestDriveUploadRestartsExpiredSession(t *testing.T) {
	size := 12 * 1024 * 1024
	a, drive, j, content := driveArchiveFixture(t, size)
	drive.failChunkAt = 2
	_ = a.archiveJob(j)
	stored, _ := a.Store.Get("downloads", "job")

	drive.failChunkAt = 0
	drive.expireSession = true
	if err := a.archiveJob(stored); err != nil {
		t.Fatal(err)
	}
	assertArchived(t, a, drive, content)
}

func TestDriveUploadRefreshesExpiredToken(t *testing.T) {
	a, drive, j, content := driveArchiveFixture(t, 3*1024*1024)
	drive.token = "access-rotated" // Google revoked access-1 early; refresh must kick in
	drive.refreshCount = 99
	if err := a.archiveJob(j); err != nil {
		t.Fatal(err)
	}
	assertArchived(t, a, drive, content)
	secret, _ := a.Store.Get("secrets", "google")
	if str(secret["access_token"]) != drive.token || str(secret["refresh_token"]) != "refresh-1" {
		t.Fatalf("refreshed token not stored: %v", secret)
	}
}

// A chunk on a slow uplink can take far longer than the 45 s API timeout.
// Upload PUTs must not inherit it, or every chunk dies and nothing progresses.
func TestDriveUploadChunkNotCutByAPITimeout(t *testing.T) {
	a, drive, j, content := driveArchiveFixture(t, 9*1024*1024)
	a.http.Timeout = 150 * time.Millisecond
	drive.chunkDelay = 400 * time.Millisecond
	if err := a.archiveJob(j); err != nil {
		t.Fatal(err)
	}
	assertArchived(t, a, drive, content)
}

// The connect flow the settings page drives: /connect hands out a Google
// consent URL with PKCE, Google redirects the browser (unauthenticated) to
// /callback with a code, and the code is exchanged for a refresh token.
func TestDriveOAuthConnectFlow(t *testing.T) {
	a := testApp(t)
	a.Store.Patch("config", "settings", Obj{"googleClientId": "client-1", "googleClientSecret": "secret-1", "googleRedirectUri": "http://127.0.0.1:3001/api/cloud/callback"})
	var exchanged url.Values
	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://oauth2.googleapis.com/token" {
			t.Errorf("unexpected %s", r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		exchanged, _ = url.ParseQuery(string(body))
		w := httptest.NewRecorder()
		io.WriteString(w, `{"access_token":"at","refresh_token":"rt","expires_in":3599}`)
		return w.Result(), nil
	})

	w := apiTest(a, "POST", "/api/cloud/connect", Obj{}, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var connect struct{ Data Obj }
	json.Unmarshal(w.Body.Bytes(), &connect)
	consent, err := url.Parse(str(connect.Data["url"]))
	if err != nil || consent.Host != "accounts.google.com" {
		t.Fatalf("consent url %v", connect.Data)
	}
	q := consent.Query()
	if q.Get("client_id") != "client-1" || q.Get("access_type") != "offline" || q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != "http://127.0.0.1:3001/api/cloud/callback" {
		t.Fatalf("consent query %v", q)
	}

	r := httptest.NewRequest("GET", "/api/cloud/callback?state="+url.QueryEscape(q.Get("state"))+"&code=auth-code", nil)
	cb := httptest.NewRecorder()
	a.Handler().ServeHTTP(cb, r)
	if cb.Code != 200 || !strings.Contains(cb.Body.String(), "已连接") {
		t.Fatalf("callback %d %s", cb.Code, cb.Body.String())
	}
	if exchanged.Get("code") != "auth-code" || exchanged.Get("code_verifier") == "" || exchanged.Get("client_secret") != "secret-1" {
		t.Fatalf("token exchange %v", exchanged)
	}
	secret, _ := a.Store.Get("secrets", "google")
	if str(secret["refresh_token"]) != "rt" {
		t.Fatalf("secret %v", secret)
	}
	if s := apiTest(a, "GET", "/api/cloud/status", nil, true); !strings.Contains(s.Body.String(), `"connected":true`) {
		t.Fatal(s.Body.String())
	}

	// A replayed callback (state already used) must not overwrite anything.
	again := httptest.NewRecorder()
	a.Handler().ServeHTTP(again, httptest.NewRequest("GET", "/api/cloud/callback?state="+url.QueryEscape(q.Get("state"))+"&code=other", nil))
	if again.Code == 200 || !strings.Contains(again.Body.String(), "连接失败") || !strings.Contains(again.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("replayed state: %d %s", again.Code, again.Body.String())
	}
}
