package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
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

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
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

func TestDriveOAuthDirectBrowserRedirectAndDesktopLoopback(t *testing.T) {
	t.Setenv("BGMCAT_GOOGLE_CLIENT_ID", "env-desktop-client")
	t.Setenv("BGMCAT_GOOGLE_CLIENT_SECRET", "env-desktop-secret")

	a := testApp(t)
	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://oauth2.googleapis.com/token" {
			t.Errorf("unexpected %s", r.URL)
		}
		w := httptest.NewRecorder()
		io.WriteString(w, `{"access_token":"at2","refresh_token":"rt2","expires_in":3599}`)
		return w.Result(), nil
	})

	status := apiTest(a, "GET", "/api/cloud/status", nil, true)
	if !strings.Contains(status.Body.String(), `"configured":true`) || !strings.Contains(status.Body.String(), `"builtin":true`) {
		t.Fatalf("expected builtin configured=true, got %s", status.Body.String())
	}

	w := apiTest(a, "POST", "/api/cloud/connect", Obj{
		"redirectUri": "http://127.0.0.1:3001",
		"returnTo":    "/service?cloud=connected",
	}, true)
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
	if q.Get("client_id") != "env-desktop-client" || q.Get("redirect_uri") != "http://127.0.0.1:3001" {
		t.Fatalf("consent query %v", q)
	}

	// Google Desktop OAuth redirects to root path http://127.0.0.1:3001/?state=gdrive:...&code=...
	cb := httptest.NewRecorder()
	a.Handler().ServeHTTP(cb, httptest.NewRequest("GET", "/?state="+url.QueryEscape(q.Get("state"))+"&code=desktop-code", nil))
	if cb.Code != http.StatusFound || cb.Header().Get("Location") != "/service?cloud=connected" {
		t.Fatalf("expected 302 to /service?cloud=connected, got %d loc=%q body=%s", cb.Code, cb.Header().Get("Location"), cb.Body.String())
	}
	if !flag(a.Settings()["cloudArchiveEnabled"]) {
		t.Fatal("expected cloudArchiveEnabled to be auto-enabled after direct browser redirect")
	}
}

func TestDriveOAuthDefaultLoopbackRootWhenRedirectEmpty(t *testing.T) {
	a := testApp(t)
	a.Store.Patch("config", "settings", Obj{"googleClientId": "desktop-id", "googleClientSecret": "desktop-sec"})

	req := httptest.NewRequest("POST", "http://127.0.0.1:3001/api/cloud/connect", strings.NewReader(`{"returnTo":"/service?cloud=connected"}`))
	req.Header.Set("Authorization", "Bearer "+str(a.Settings()["token"]))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("connect status=%d body=%s", w.Code, w.Body.String())
	}
	var connect struct{ Data Obj }
	json.Unmarshal(w.Body.Bytes(), &connect)
	consent, err := url.Parse(str(connect.Data["url"]))
	if err != nil {
		t.Fatal(err)
	}
	if got := consent.Query().Get("redirect_uri"); got != "http://127.0.0.1:3001" {
		t.Fatalf("expected loopback root http://127.0.0.1:3001, got %q", got)
	}
}

func TestSaveToResolution(t *testing.T) {
	a := testApp(t)
	if _, err := a.resolveSaveTo("cloud"); err == nil || !strings.Contains(err.Error(), "连接 Google Drive") {
		t.Fatalf("explicit cloud without Drive: %v", err)
	}
	a.Store.Patch("config", "settings", Obj{"defaultSaveTo": "cloud"})
	if got, err := a.resolveSaveTo(""); err != nil || got != "local" {
		t.Fatalf("default cloud without Drive should fall back to local, got %q %v", got, err)
	}
	a.Store.Put("secrets", "google", Obj{"refresh_token": "rt"})
	if got, _ := a.resolveSaveTo(""); got != "cloud" {
		t.Fatalf("default cloud with Drive = %q", got)
	}
	if got, _ := a.resolveSaveTo("local"); got != "local" {
		t.Fatalf("explicit local = %q", got)
	}
	if _, err := a.resolveSaveTo("ftp"); err == nil {
		t.Fatal("unknown target accepted")
	}

	custom := filepath.Join(a.cfg.DataDir, "elsewhere")
	w := apiTest(a, "POST", "/api/downloads", Obj{"url": "http://127.0.0.1:1/ep.mp4", "saveTo": "cloud", "directory": custom}, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var created struct{ Data Obj }
	json.Unmarshal(w.Body.Bytes(), &created)
	if str(created.Data["saveTo"]) != "cloud" || !strings.HasPrefix(str(created.Data["directory"]), custom+string(filepath.Separator)) {
		t.Fatalf("created %v", created.Data)
	}
	if w := apiTest(a, "POST", "/api/downloads", Obj{"url": "http://127.0.0.1:1/ep2.mp4", "directory": "relative/dir"}, true); w.Code == 200 {
		t.Fatal("relative directory accepted")
	}
}

// A task saved to Drive uploads even with global auto-archive off, and its
// local copy is removed once the cloud copy is verified.
func TestSaveToCloudArchivesAndRemovesLocal(t *testing.T) {
	a, drive, j, content := driveArchiveFixture(t, 1024*1024)
	a.Store.Patch("config", "settings", Obj{"cloudArchiveEnabled": false, "cloudDeleteLocal": false})
	a.Store.Patch("downloads", "job", Obj{"saveTo": "cloud"})
	a.archiveCompleted()
	stored, _ := a.Store.Get("downloads", "job")
	if s := str(object(stored["cloudArchive"])["status"]); s != "archived" {
		t.Fatalf("cloudArchive = %v", stored["cloudArchive"])
	}
	file := object(list(stored["files"])[0])
	if remote := drive.files[str(file["remoteId"])]; remote == nil || !bytes.Equal(remote.data, content) {
		t.Fatal("cloud copy missing")
	}
	if _, err := os.Stat(str(object(list(j["files"])[0])["path"])); !os.IsNotExist(err) {
		t.Fatal("local copy kept for a cloud task", err)
	}

	// A plain local task is left alone while global archive is off.
	b, drive2, _, _ := driveArchiveFixture(t, 1024)
	b.Store.Patch("config", "settings", Obj{"cloudArchiveEnabled": false})
	b.archiveCompleted()
	if len(drive2.files) != 0 {
		t.Fatal("local task uploaded with archive disabled")
	}
}

func TestBTDownloadSaveToCloudZeroLocalFootprint(t *testing.T) {
	a := testApp(t)
	drive := newFakeDrive(t)
	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "127.0.0.1" {
			return http.DefaultTransport.RoundTrip(r)
		}
		return drive.RoundTrip(r)
	})
	a.Store.Patch("config", "settings", Obj{
		"cloudArchiveEnabled": false,
		"cloudDeleteLocal":    false,
		"cloudDriveFolderId":  "folder-anime",
		"googleClientId":      "client",
		"googleClientSecret":  "secret",
	})
	a.Store.Put("secrets", "google", Obj{
		"access_token":  "access-1",
		"refresh_token": "refresh-1",
		"expires_at":    time.Now().Add(time.Hour).Unix(),
	})

	// Build a multi-file nested torrent (Season 1/Ep01_1080p.mkv + Season 1/Ep01_1080p.sc.ass).
	seedRoot := filepath.Join(t.TempDir(), "FXRelease")
	seasonDir := filepath.Join(seedRoot, "Season 1")
	if err := os.MkdirAll(seasonDir, 0o700); err != nil {
		t.Fatal(err)
	}
	videoBytes := bytes.Repeat([]byte("video-frame-payload-0123456789abcdef"), 8000)
	subBytes := []byte("[Script Info]\nTitle: FX Ep01\n")
	if err := os.WriteFile(filepath.Join(seasonDir, "Ep01_1080p.mkv"), videoBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seasonDir, "Ep01_1080p.sc.ass"), subBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	info := metainfo.Info{PieceLength: 32 * 1024}
	if err := info.BuildFromFilePath(seedRoot); err != nil {
		t.Fatal(err)
	}
	rawInfo, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: rawInfo}
	seedCfg := localTorrentConfig(filepath.Dir(seedRoot))
	seedCfg.Seed = true
	seed, err := torrent.NewClient(seedCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	st, err := seed.AddTorrent(&mi)
	if err != nil {
		t.Fatal(err)
	}
	st.VerifyData()

	trackerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peers := []map[string]any{}
		for _, addr := range seed.ListenAddrs() {
			if tcp, ok := addr.(*net.TCPAddr); ok {
				peers = append(peers, map[string]any{"ip": "127.0.0.1", "port": tcp.Port})
			}
		}
		data, _ := bencode.Marshal(map[string]any{"interval": 1, "peers": peers})
		_, _ = w.Write(data)
	}))
	defer trackerSrv.Close()
	mi.Announce = trackerSrv.URL + "/announce"

	var torrentBuf bytes.Buffer
	if err := mi.Write(&torrentBuf); err != nil {
		t.Fatal(err)
	}
	torrentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(torrentBuf.Bytes())
	}))
	defer torrentSrv.Close()

	// Run 1: Real enqueue -> BT download -> automatic upload + local cleanup.
	w := apiTest(a, "POST", "/api/downloads", Obj{
		"url":        torrentSrv.URL + "/ep01.torrent",
		"sourceType": "torrent-url",
		"title":      "[LoliHouse] FX战士久留美 - 01 [1080p]",
		"animeTitle": "FX战士久留美",
		"saveTo":     "cloud",
	}, true)
	if w.Code != 200 {
		t.Fatalf("POST /api/downloads: %d %s", w.Code, w.Body.String())
	}
	var created struct{ Data Obj }
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	gid := str(created.Data["gid"])

	deadline := time.Now().Add(25 * time.Second)
	archived := false
	for time.Now().Before(deadline) {
		cur, _ := a.Store.Get("downloads", gid)
		if cur != nil && str(cur["status"]) == "error" {
			t.Fatalf("download failed: %s", str(cur["errorMessage"]))
		}
		if cur != nil && str(object(cur["cloudArchive"])["status"]) == "archived" {
			archived = true
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if !archived {
		cur, _ := a.Store.Get("downloads", gid)
		t.Fatalf("timed out waiting for cloudArchive=archived, last=%v", cur)
	}

	// Wait briefly for post-cleanup RemoveAll on btTaskStateDir & verify 0 bytes and 0 subdirs in downloads/.
	var dlFiles, dlSubdirs int
	_ = filepath.WalkDir(a.downloadsDir(), func(p string, d fs.DirEntry, wErr error) error {
		if wErr != nil || p == a.downloadsDir() {
			return nil
		}
		if d.IsDir() {
			dlSubdirs++
		} else {
			dlFiles++
		}
		return nil
	})
	if dlFiles != 0 || dlSubdirs != 0 {
		t.Fatalf("expected 0 files and 0 subdirs in %s after cloud archive, got files=%d subdirs=%d", a.downloadsDir(), dlFiles, dlSubdirs)
	}
	if _, statErr := os.Stat(a.btTaskStateDir(gid)); !os.IsNotExist(statErr) {
		t.Fatalf("expected bt task state dir %s removed, stat=%v", a.btTaskStateDir(gid), statErr)
	}

	// Verify both nested files exist in fakeDrive and support byte-range reads.
	stored1, _ := a.Store.Get("downloads", gid)
	if len(list(stored1["files"])) != 2 {
		t.Fatalf("expected 2 archived files, got %v", stored1["files"])
	}
	for _, rawF := range list(stored1["files"]) {
		f := object(rawF)
		remote := drive.files[str(f["remoteId"])]
		if remote == nil || len(remote.data) == 0 {
			t.Fatalf("missing remote file for %v", f)
		}
		res, rErr := a.openCloudRangeResponse(context.Background(), "google", str(f["remoteId"]), "GET", "bytes=0-15")
		if rErr != nil {
			t.Fatalf("openCloudRangeResponse err=%v", rErr)
		}
		chunk, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 206 || len(chunk) == 0 {
			t.Fatalf("unexpected cloud range response: %d len=%d", res.StatusCode, len(chunk))
		}
	}

	// Run 2: Nested folder + Finder .DS_Store + sibling local episode in the same series folder.
	seriesDir := filepath.Join(a.downloadsDir(), "FX战士久留美")
	nestedDir := filepath.Join(seriesDir, "Season 1")
	if err := os.MkdirAll(nestedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ep1Path := filepath.Join(nestedDir, "Ep01_1080p.mkv")
	dsStorePath := filepath.Join(nestedDir, ".DS_Store")
	ep2LocalPath := filepath.Join(seriesDir, "Ep02_Local.mkv")
	ep1Data := []byte("ep1-cloud-payload-bytes")
	if err := os.WriteFile(ep1Path, ep1Data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dsStorePath, []byte("finder-junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ep2LocalPath, []byte("keep-local-ep2"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Put("downloads", "ep2-local", Obj{
		"gid":       "ep2-local",
		"status":    "complete",
		"saveTo":    "local",
		"directory": seriesDir,
		"files":     []Obj{{"path": ep2LocalPath, "size": len("keep-local-ep2")}},
	})
	_ = a.Store.Put("downloads", "ep1-cloud", Obj{
		"gid":       "ep1-cloud",
		"status":    "complete",
		"saveTo":    "cloud",
		"directory": seriesDir,
		"files":     []Obj{{"path": ep1Path, "size": len(ep1Data)}},
	})
	a.archiveCompleted()

	ep1Stored, _ := a.Store.Get("downloads", "ep1-cloud")
	if st1 := str(object(ep1Stored["cloudArchive"])["status"]); st1 != "archived" {
		t.Fatalf("ep1-cloud status=%q, want archived", st1)
	}
	if _, err := os.Stat(nestedDir); !os.IsNotExist(err) {
		t.Fatalf("expected nested Season 1 subdir + .DS_Store pruned, err=%v", err)
	}
	if gotEp2, err := os.ReadFile(ep2LocalPath); err != nil || string(gotEp2) != "keep-local-ep2" {
		t.Fatalf("sibling local episode was harmed: got=%q err=%v", string(gotEp2), err)
	}
}
