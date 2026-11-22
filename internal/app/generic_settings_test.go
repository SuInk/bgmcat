package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGenericNetworkProbeTargetsAndCustomDomains(t *testing.T) {
	a := testApp(t)

	w := apiTest(a, "GET", "/api/network/targets", nil, true)
	if w.Code != 200 {
		t.Fatalf("GET /api/network/targets: %d %s", w.Code, w.Body.String())
	}
	var listEnv struct {
		OK   bool  `json:"ok"`
		Data []Obj `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listEnv); err != nil || !listEnv.OK {
		t.Fatalf("decode targets: %v", err)
	}
	ids := map[string]bool{}
	for _, item := range listEnv.Data {
		ids[str(item["id"])] = true
	}
	for _, want := range []string{"google", "onedrive", "mikan", "dmhy", "nyaa", "tmdb"} {
		if !ids[want] {
			t.Fatalf("missing builtin probe target %q in %v", want, ids)
		}
	}

	// Add custom domain
	addW := apiTest(a, "POST", "/api/network/targets", Obj{"host": "https://sub.example.org/rss", "name": "自建镜像"}, true)
	if addW.Code != 200 {
		t.Fatalf("add custom target: %d %s", addW.Code, addW.Body.String())
	}

	// Patch mikan target state
	patchW := apiTest(a, "PATCH", "/api/network/targets/mikan", Obj{"enabled": true, "sniMode": "fragment"}, true)
	if patchW.Code != 200 {
		t.Fatalf("patch mikan target: %d %s", patchW.Code, patchW.Body.String())
	}
	stW := apiTest(a, "GET", "/api/network/targets/mikan", nil, true)
	if stW.Code != 200 || !strings.Contains(stW.Body.String(), `"sniMode":"fragment"`) {
		t.Fatalf("unexpected mikan state: %d %s", stW.Code, stW.Body.String())
	}

	// Delete custom domain
	delW := apiTest(a, "DELETE", "/api/network/targets/custom-sub.example.org", nil, true)
	if delW.Code != 200 {
		t.Fatalf("delete custom target: %d %s", delW.Code, delW.Body.String())
	}
}

func TestGenericDownloadEnginesAria2RPCAndTransmission(t *testing.T) {
	a := testApp(t)
	dlRoot := filepath.Join(a.cfg.DataDir, "downloads")
	_ = os.MkdirAll(dlRoot, 0700)

	// 1. Mock Aria2 JSON-RPC server
	var aria2Mu sync.Mutex
	var aria2Dir string
	aria2Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Params) == 0 || str(req.Params[0]) != "token:mysecret" {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(Obj{"jsonrpc": "2.0", "id": req.ID, "error": Obj{"code": 1, "message": "Unauthorized"}})
			return
		}
		aria2Mu.Lock()
		defer aria2Mu.Unlock()
		switch req.Method {
		case "aria2.getVersion":
			_ = json.NewEncoder(w).Encode(Obj{"jsonrpc": "2.0", "id": req.ID, "result": Obj{"version": "1.37.0"}})
		case "aria2.addUri":
			opts := object(req.Params[2])
			aria2Dir = str(opts["dir"])
			_ = os.MkdirAll(aria2Dir, 0700)
			_ = os.WriteFile(filepath.Join(aria2Dir, "ep01.mp4"), []byte("video-bytes"), 0600)
			_ = json.NewEncoder(w).Encode(Obj{"jsonrpc": "2.0", "id": req.ID, "result": "gid-aria2-1"})
		case "aria2.tellStatus":
			filePath := filepath.Join(aria2Dir, "ep01.mp4")
			_ = json.NewEncoder(w).Encode(Obj{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": Obj{
					"gid":             "gid-aria2-1",
					"status":          "complete",
					"totalLength":     "11",
					"completedLength": "11",
					"downloadSpeed":   "0",
					"uploadSpeed":     "0",
					"files": []Obj{
						{"path": filePath, "length": "11", "selected": "true"},
					},
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(Obj{"jsonrpc": "2.0", "id": req.ID, "result": "OK"})
		}
	}))
	defer aria2Srv.Close()

	putW := apiTest(a, "PUT", "/api/settings", Obj{
		"downloadClient": "aria2-rpc",
		"aria2RpcUrl":    aria2Srv.URL + "/jsonrpc",
		"aria2RpcSecret": "mysecret",
	}, true)
	if putW.Code != 200 {
		t.Fatalf("save aria2-rpc settings: %d %s", putW.Code, putW.Body.String())
	}

	testW := apiTest(a, "POST", "/api/downloads/engines/test", Obj{"client": "aria2-rpc"}, true)
	if testW.Code != 200 || !strings.Contains(testW.Body.String(), `"ok":true`) || !strings.Contains(testW.Body.String(), `1.37.0`) {
		t.Fatalf("test aria2-rpc failed: %d %s", testW.Code, testW.Body.String())
	}

	// 2. Mock Transmission RPC server with 409 X-Transmission-Session-Id handshake
	const wantSession = "tr-session-xyz"
	trSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, okAuth := r.BasicAuth()
		if !okAuth || user != "admin" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-Transmission-Session-Id") != wantSession {
			w.Header().Set("X-Transmission-Session-Id", wantSession)
			w.WriteHeader(http.StatusConflict)
			return
		}
		var req Obj
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch str(req["method"]) {
		case "session-get":
			_ = json.NewEncoder(w).Encode(Obj{"result": "success", "arguments": Obj{"version": "4.0.6"}})
		default:
			_ = json.NewEncoder(w).Encode(Obj{"result": "success", "arguments": Obj{}})
		}
	}))
	defer trSrv.Close()

	trTestW := apiTest(a, "POST", "/api/downloads/engines/test", Obj{
		"client":               "transmission",
		"transmissionRpcUrl":   trSrv.URL + "/transmission/rpc",
		"transmissionUsername": "admin",
		"transmissionPassword": "secret",
	}, true)
	if trTestW.Code != 200 || !strings.Contains(trTestW.Body.String(), `"ok":true`) || !strings.Contains(trTestW.Body.String(), `4.0.6`) {
		t.Fatalf("test transmission failed: %d %s", trTestW.Code, trTestW.Body.String())
	}
}

func TestGenericWebDAVCloudArchiveAndStream(t *testing.T) {
	a := testApp(t)

	var davMu sync.Mutex
	davFiles := map[string][]byte{}
	davDirs := map[string]bool{"": true}

	davSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, okAuth := r.BasicAuth()
		if !okAuth || u != "davuser" || p != "davpass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/dav"), "/")
		davMu.Lock()
		defer davMu.Unlock()

		switch r.Method {
		case "PROPFIND":
			if !davDirs[rel] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(207)
			var xmlBuf bytes.Buffer
			xmlBuf.WriteString(`<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:">`)
			selfHref := "/dav/"
			if rel != "" {
				selfHref = "/dav/" + rel + "/"
			}
			xmlBuf.WriteString(`<D:response><D:href>` + selfHref + `</D:href><D:propstat><D:status>HTTP/1.1 200 OK</D:status><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop></D:propstat></D:response>`)
			if r.Header.Get("Depth") == "1" {
				prefix := ""
				if rel != "" {
					prefix = rel + "/"
				}
				for k, data := range davFiles {
					if strings.HasPrefix(k, prefix) && !strings.Contains(strings.TrimPrefix(k, prefix), "/") {
						name := filepath.Base(k)
						xmlBuf.WriteString(`<D:response><D:href>/dav/` + url.PathEscape(prefix) + url.PathEscape(name) + `</D:href><D:propstat><D:status>HTTP/1.1 200 OK</D:status><D:prop><D:displayname>` + name + `</D:displayname><D:getcontentlength>` + jsonNumber(len(data)) + `</D:getcontentlength><D:getcontenttype>video/mp4</D:getcontenttype></D:prop></D:propstat></D:response>`)
					}
				}
			}
			xmlBuf.WriteString(`</D:multistatus>`)
			_, _ = w.Write(xmlBuf.Bytes())
		case "MKCOL":
			davDirs[rel] = true
			w.WriteHeader(http.StatusCreated)
		case "PUT":
			data, _ := io.ReadAll(r.Body)
			davFiles[rel] = data
			w.WriteHeader(http.StatusCreated)
		case "HEAD":
			data, okFile := davFiles[rel]
			if !okFile {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", jsonNumber(len(data)))
			w.WriteHeader(http.StatusOK)
		case "GET":
			data, okFile := davFiles[rel]
			if !okFile {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			http.ServeContent(w, r, filepath.Base(rel), time.Now(), bytes.NewReader(data))
		case "DELETE":
			delete(davFiles, rel)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer davSrv.Close()

	// Connect WebDAV
	connW := apiTest(a, "POST", "/api/cloud/connect", Obj{
		"provider":       "webdav",
		"webdavUrl":      davSrv.URL + "/dav",
		"webdavUsername": "davuser",
		"webdavPassword": "davpass",
		"webdavRootPath": "BangumiCat",
	}, true)
	if connW.Code != 200 || !strings.Contains(connW.Body.String(), `"connected":true`) {
		t.Fatalf("connect webdav failed: %d %s", connW.Code, connW.Body.String())
	}

	// Create local completed download and archive to WebDAV with cloudDeleteLocal=true
	_, _ = a.Store.Patch("config", "settings", Obj{"cloudArchiveEnabled": true, "cloudDeleteLocal": true, "downloadFolderMode": "flat"})
	jobDir := filepath.Join(a.downloadsDir(), "show1")
	_ = os.MkdirAll(jobDir, 0700)
	videoData := bytes.Repeat([]byte("WEBDAV_STREAM_DATA_"), 1024)
	localFile := filepath.Join(jobDir, "Episode01.mp4")
	if err := os.WriteFile(localFile, videoData, 0600); err != nil {
		t.Fatal(err)
	}
	job := Obj{
		"id":            1,
		"gid":           "aa11bb22cc33dd44ee55ff66",
		"feedItemTitle": "Episode 01",
		"status":        "complete",
		"directory":     jobDir,
		"saveTo":        "cloud",
		"files":         []Obj{{"path": localFile, "size": int64(len(videoData))}},
	}
	_ = a.Store.Put("downloads", "aa11bb22cc33dd44ee55ff66", job)
	if err := a.archiveJob(job); err != nil {
		t.Fatalf("archiveJob to webdav failed: %v", err)
	}

	// Local file should be deleted after verified WebDAV archive
	if _, err := os.Stat(localFile); !os.IsNotExist(err) {
		t.Fatalf("expected local file deleted after webdav archive, got err=%v", err)
	}

	// Cloud browser should list the uploaded file
	filesW := apiTest(a, "GET", "/api/files/cloud", nil, true)
	if filesW.Code != 200 || !strings.Contains(filesW.Body.String(), `"provider":"webdav"`) || !strings.Contains(filesW.Body.String(), "Episode01.mp4") {
		t.Fatalf("list webdav cloud files: %d %s", filesW.Code, filesW.Body.String())
	}

	// Range playback from WebDAV archived download should succeed
	streamReq := httptest.NewRequest("GET", "/api/downloads/aa11bb22cc33dd44ee55ff66/stream", nil)
	streamReq.Header.Set("Authorization", "Bearer "+str(a.Settings()["token"]))
	streamReq.Header.Set("Range", "bytes=0-18")
	streamW := httptest.NewRecorder()
	a.Handler().ServeHTTP(streamW, streamReq)
	if streamW.Code != 206 || streamW.Body.String() != "WEBDAV_STREAM_DATA_" {
		t.Fatalf("stream from webdav archive: %d %q", streamW.Code, streamW.Body.String())
	}
}

func TestProviderMirrorsAndOpenAICompatibleAI(t *testing.T) {
	a := testApp(t)

	w := apiTest(a, "PUT", "/api/settings", Obj{
		"providerMirrors": Obj{
			"dmhy":  "https://dmhy-mirror.example.com/",
			"mikan": "https://mikan-mirror.example.com",
		},
	}, true)
	if w.Code != 200 {
		t.Fatalf("save providerMirrors: %d %s", w.Code, w.Body.String())
	}
	if got := a.providerBaseURL("dmhy"); got != "https://dmhy-mirror.example.com" {
		t.Fatalf("dmhy mirror = %q", got)
	}
	if got := a.mikanBase(); got != "https://mikan-mirror.example.com" {
		t.Fatalf("mikan mirror = %q", got)
	}

	// Mock OpenAI-compatible server
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(Obj{"data": []Obj{{"id": "gpt-4o-mini"}, {"id": "deepseek-chat"}}})
		case "/v1/chat/completions":
			_ = json.NewEncoder(w).Encode(Obj{
				"choices": []Obj{{"message": Obj{"role": "assistant", "content": "OK from OpenAI API"}}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer aiSrv.Close()

	_, _ = a.Store.Patch("config", "settings", Obj{
		"localAiEnabled": true,
		"localAiBaseUrl": aiSrv.URL + "/v1",
		"localAiApiKey":  "sk-test-123",
		"localAiModel":   "gpt-4o-mini",
	})
	stW := apiTest(a, "GET", "/api/local-ai/status", nil, true)
	if stW.Code != 200 || !strings.Contains(stW.Body.String(), `"reachable":true`) || !strings.Contains(stW.Body.String(), "deepseek-chat") {
		t.Fatalf("ai status failed: %d %s", stW.Code, stW.Body.String())
	}
	chatW := apiTest(a, "POST", "/api/local-ai/chat", Obj{
		"messages": []Obj{{"role": "user", "content": "ping"}},
	}, true)
	if chatW.Code != 200 || !strings.Contains(chatW.Body.String(), "OK from OpenAI API") {
		t.Fatalf("ai chat failed: %d %s", chatW.Code, chatW.Body.String())
	}
}

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
