package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	stdhash "hash"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const driveBase = "https://www.googleapis.com/drive/v3"

var (
	DefaultGoogleClientID     string
	DefaultGoogleClientSecret string
)

func (a *App) effectiveGoogleCredentials() (clientID, clientSecret string, isBuiltin bool) {
	s := a.Settings()
	cid := strings.TrimSpace(str(s["googleClientId"]))
	csec := strings.TrimSpace(str(s["googleClientSecret"]))
	if cid != "" {
		return cid, csec, false
	}
	builtinID := firstNonEmpty(os.Getenv("BGMCAT_GOOGLE_CLIENT_ID"), os.Getenv("GOOGLE_CLIENT_ID"), DefaultGoogleClientID)
	builtinSec := firstNonEmpty(os.Getenv("BGMCAT_GOOGLE_CLIENT_SECRET"), os.Getenv("GOOGLE_CLIENT_SECRET"), DefaultGoogleClientSecret)
	if builtinID != "" {
		return builtinID, builtinSec, true
	}
	return "", "", false
}

func sanitizeRelativeReturnTo(raw string) string {
	rt := strings.TrimSpace(raw)
	if rt == "" || !strings.HasPrefix(rt, "/") || strings.HasPrefix(rt, "//") || strings.HasPrefix(rt, "/\\") || strings.ContainsAny(rt, "\r\n") {
		return ""
	}
	return rt
}

func appendQueryParam(pathWithQuery, key, val string) string {
	u, err := url.Parse(pathWithQuery)
	if err != nil {
		return pathWithQuery
	}
	q := u.Query()
	q.Set(key, val)
	u.RawQuery = q.Encode()
	return u.String()
}

func isLoopbackHost(hostPort string) bool {
	u, err := url.Parse("http://" + strings.TrimSpace(hostPort))
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func isAllowedOAuthRedirect(u *url.URL) bool {
	if u == nil || u.Host == "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		return host == "127.0.0.1" || host == "localhost" || host == "::1"
	}
	return false
}

func (a *App) cloudRoutes() {
	a.cloudSetupRoutes()
	a.handle("GET /api/cloud/providers", func(w http.ResponseWriter, r *http.Request) error {
		items := make([]Obj, 0, len(supportedCloudProviders))
		for _, p := range supportedCloudProviders {
			items = append(items, a.cloudProviderStatusObj(p))
		}
		ok(w, Obj{
			"activeProvider": a.activeCloudProvider(),
			"providers":      items,
		})
		return nil
	})
	a.handle("GET /api/cloud/status", func(w http.ResponseWriter, r *http.Request) error {
		provider := strings.TrimSpace(r.URL.Query().Get("provider"))
		ok(w, a.cloudProviderStatusObj(provider))
		return nil
	})
	a.handle("POST /api/cloud/connect", func(w http.ResponseWriter, r *http.Request) error {
		var payload Obj
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
				return errors.New("无效 JSON 请求")
			}
		}
		provider := firstNonEmpty(strings.TrimSpace(str(payload["provider"])), a.activeCloudProvider())
		switch provider {
		case "onedrive":
			res, err := a.startOneDriveConnect(r, payload)
			if err != nil {
				return err
			}
			ok(w, res)
			return nil
		case "webdav":
			res, err := a.connectWebDAV(r.Context(), payload)
			if err != nil {
				return err
			}
			ok(w, res)
			return nil
		}
		s := a.Settings()
		client, _, _ := a.effectiveGoogleCredentials()
		redirect := firstNonEmpty(str(payload["redirectUri"]), str(s["googleRedirectUri"]))
		if redirect == "" && r.Host != "" {
			if !isHTTPSRequest(r) && isLoopbackHost(r.Host) {
				redirect = "http://" + r.Host
			} else {
				scheme := "http"
				if isHTTPSRequest(r) {
					scheme = "https"
				}
				redirect = scheme + "://" + r.Host + "/api/cloud/callback"
			}
		}
		if client == "" || redirect == "" {
			return errors.New("请先填写 Google OAuth 客户端 ID")
		}
		u, e := url.Parse(redirect)
		if e != nil || !isAllowedOAuthRedirect(u) {
			return errors.New("回调地址需为 HTTPS 或本机回环地址")
		}
		returnTo := sanitizeRelativeReturnTo(str(payload["returnTo"]))
		state := "gdrive:" + randomID(28)
		verifier := randomID(32)
		sum := sha256.Sum256([]byte(verifier))
		a.oauthMu.Lock()
		a.oauth[state] = Obj{
			"provider": "google",
			"verifier": verifier,
			"redirect": redirect,
			"returnTo": returnTo,
			"expires":  time.Now().Add(10 * time.Minute).Unix(),
		}
		a.oauthMu.Unlock()
		values := url.Values{
			"client_id":             {client},
			"redirect_uri":          {redirect},
			"response_type":         {"code"},
			"scope":                 {"https://www.googleapis.com/auth/drive"},
			"access_type":           {"offline"},
			"prompt":                {"consent"},
			"state":                 {state},
			"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
			"code_challenge_method": {"S256"},
		}
		ok(w, Obj{"url": "https://accounts.google.com/o/oauth2/v2/auth?" + values.Encode()})
		return nil
	})
	a.handle("GET /api/cloud/callback", func(w http.ResponseWriter, r *http.Request) error {
		a.serveCloudOAuthCallback(w, r)
		return nil
	})
	a.handle("POST /api/cloud/disconnect", func(w http.ResponseWriter, r *http.Request) error {
		var payload Obj
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
			_ = json.NewDecoder(r.Body).Decode(&payload)
		}
		provider := firstNonEmpty(strings.TrimSpace(str(payload["provider"])), a.activeCloudProvider())
		if !validCloudProvider(provider) {
			provider = "google"
		}
		e := a.Store.Delete("secrets", provider)
		if provider == a.activeCloudProvider() {
			_, _ = a.Store.Patch("config", "settings", Obj{"cloudArchiveEnabled": false})
		}
		if e == nil {
			ok(w, Obj{"connected": false, "provider": provider})
		}
		return e
	})
}

func (a *App) serveDriveCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	a.oauthMu.Lock()
	pending := a.oauth[state]
	delete(a.oauth, state)
	a.oauthMu.Unlock()

	returnTo := sanitizeRelativeReturnTo(str(pending["returnTo"]))
	if e := a.completeDriveCallback(r, pending); e != nil {
		if returnTo != "" {
			http.Redirect(w, r, appendQueryParam(returnTo, "cloudError", e.Error()), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Google Drive 连接失败</title><style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0e1015;color:#eef1f8;font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.card{max-width:420px;padding:28px;border-radius:16px;background:#161922;border:1px solid #262b38;text-align:center}h1{margin:0 0 8px;font-size:18px;color:#ff6b7a}p{margin:6px 0;color:#9aa3b5}</style><div class="card"><h1>Google Drive 连接失败</h1><p>`+html.EscapeString(e.Error())+`</p><p>请回到 Bangumi Cat 的「设置 → 云盘归档」重新发起授权。</p></div>`)
		return
	}

	if returnTo != "" {
		http.Redirect(w, r, returnTo, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Google Drive 已连接</title><style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0e1015;color:#eef1f8;font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.card{max-width:420px;padding:28px;border-radius:16px;background:#161922;border:1px solid #262b38;text-align:center}h1{margin:0 0 8px;font-size:18px;color:#3dd68c}p{margin:6px 0;color:#9aa3b5}</style><div class="card"><h1>Google Drive 已连接</h1><p>授权已完成，可以关闭此标签页返回猫追番。</p></div><script>try{window.close()}catch(e){}</script>`)
}

func (a *App) completeDriveCallback(r *http.Request, pending Obj) error {
	if pending == nil || num(pending["expires"]) < time.Now().Unix() {
		return errors.New("登录请求已失效，请重新连接")
	}
	if r.URL.Query().Get("error") != "" {
		return errors.New("Google 授权被取消")
	}
	clientID, clientSecret, _ := a.effectiveGoogleCredentials()
	form := url.Values{
		"client_id":     {clientID},
		"code":          {r.URL.Query().Get("code")},
		"code_verifier": {str(pending["verifier"])},
		"redirect_uri":  {str(pending["redirect"])},
		"grant_type":    {"authorization_code"},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	token, e := a.oauthExchange(r.Context(), form)
	if e != nil {
		return e
	}
	if str(token["refresh_token"]) == "" {
		return errors.New("未取得可刷新凭据，请重新授权")
	}
	token["expires_at"] = time.Now().Add(time.Duration(num(token["expires_in"])) * time.Second).Unix()
	if e = a.Store.Put("secrets", "google", token); e != nil {
		return e
	}
	patch := Obj{"cloudProvider": "google"}
	if str(pending["returnTo"]) != "" {
		patch["cloudArchiveEnabled"] = true
		if strings.TrimSpace(str(a.Settings()["cloudDriveFolderId"])) == "" {
			patch["cloudDriveFolderId"] = "root"
		}
	}
	_, _ = a.Store.Patch("config", "settings", patch)
	a.triggerArchive()
	return nil
}
func (a *App) oauthExchange(ctx context.Context, values url.Values) (Obj, error) {
	req, e := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/token", strings.NewReader(values.Encode()))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, e := a.http.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("Google 授权失败（HTTP %d），请检查客户端配置或重新登录", res.StatusCode)
	}
	var token Obj
	e = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&token)
	return token, e
}
func (a *App) accessToken(ctx context.Context, force bool) (string, error) {
	a.oauthMu.Lock()
	defer a.oauthMu.Unlock()
	t, e := a.Store.Get("secrets", "google")
	if e != nil {
		return "", e
	}
	if t == nil {
		return "", errors.New("请先连接 Google Drive")
	}
	if !force && num(t["expires_at"]) > time.Now().Add(time.Minute).Unix() {
		return str(t["access_token"]), nil
	}
	clientID, clientSecret, _ := a.effectiveGoogleCredentials()
	form := url.Values{
		"client_id":     {clientID},
		"refresh_token": {str(t["refresh_token"])},
		"grant_type":    {"refresh_token"},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	next, e := a.oauthExchange(ctx, form)
	if e != nil {
		return "", e
	}
	next["refresh_token"] = t["refresh_token"]
	next["expires_at"] = time.Now().Add(time.Duration(num(next["expires_in"])) * time.Second).Unix()
	if e = a.Store.Put("secrets", "google", next); e != nil {
		return "", e
	}
	return str(next["access_token"]), nil
}
func (a *App) driveRequest(ctx context.Context, method, endpoint string, data []byte, headers map[string]string) (*http.Response, error) {
	var makeBody func() (io.ReadCloser, error)
	if len(data) > 0 {
		makeBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(data)), nil
		}
	}
	return a.driveStreamRequest(ctx, method, endpoint, int64(len(data)), makeBody, headers)
}

func (a *App) driveStreamRequest(ctx context.Context, method, endpoint string, contentLength int64, makeBody func() (io.ReadCloser, error), headers map[string]string) (*http.Response, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.Host != "www.googleapis.com" || u.User != nil {
		return nil, errors.New("无效 Drive 地址")
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, e := a.accessToken(ctx, attempt > 0)
		if e != nil {
			return nil, e
		}
		var lastErr error
		for routeAttempt := 0; routeAttempt < 4; routeAttempt++ {
			reqCtx, cancelReq := context.WithCancel(ctx)
			client := *a.http
			transport, selectedIP := a.driveTransport()
			client.Transport = transport
			if strings.Contains(endpoint, "alt=media") {
				client.Timeout = 0
			} else if method == "PUT" && strings.HasPrefix(u.Path, "/upload/") {
				// A chunk on a slow uplink easily outlasts the 45 s API timeout,
				// which used to kill every chunk so uploads never advanced. Give the
				// chunk a deadline that scales with its size instead.
				client.Timeout = 0
				cancelReq()
				reqCtx, cancelReq = context.WithTimeout(ctx, uploadChunkTimeout(contentLength))
			}
			var bodyReader io.Reader
			if makeBody != nil {
				rc, bodyErr := makeBody()
				if bodyErr != nil {
					cancelReq()
					return nil, bodyErr
				}
				bodyReader = rc
			}
			req, e := http.NewRequestWithContext(reqCtx, method, endpoint, bodyReader)
			if e != nil {
				cancelReq()
				return nil, e
			}
			if makeBody != nil {
				req.ContentLength = contentLength
				req.GetBody = makeBody
			}
			req.Header.Set("Authorization", "Bearer "+token)
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
			res, e := client.Do(req)
			if e != nil {
				cancelReq()
				lastErr = e
				if selectedIP != "" && ctx.Err() == nil {
					a.failDriveIP(selectedIP)
					continue
				}
				return nil, e
			}
			res.Body = cancelOnClose{res.Body, cancelReq}
			if res.StatusCode == 401 && attempt == 0 {
				res.Body.Close()
				lastErr = nil
				break
			}
			return res, nil
		}
		if lastErr != nil {
			return nil, lastErr
		}
	}
	return nil, errors.New("Google 认证失败")
}

// uploadChunkTimeout allows a chunk to crawl at ~16 KiB/s before giving up,
// but never less than two minutes (small final chunks, slow TLS handshakes).
func uploadChunkTimeout(size int64) time.Duration {
	return max(2*time.Minute, time.Duration(size/(16<<10))*time.Second)
}

func driveChunkSize(fileSize int64) int64 {
	switch {
	case fileSize <= 32<<20:
		return 8 << 20
	case fileSize <= 512<<20:
		return 32 << 20
	default:
		return 64 << 20
	}
}

// cancelOnClose releases a per-request context once the caller is done with
// the response body.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func (a *App) driveJSON(ctx context.Context, method, endpoint string, payload any) (Obj, int, error) {
	var data []byte
	var e error
	if payload != nil {
		data, e = json.Marshal(payload)
		if e != nil {
			return nil, 0, e
		}
	}
	res, e := a.driveRequest(ctx, method, endpoint, data, map[string]string{"Content-Type": "application/json"})
	if e != nil {
		return nil, 0, e
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, res.StatusCode, fmt.Errorf("Drive HTTP %d", res.StatusCode)
	}
	var o Obj
	e = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&o)
	return o, res.StatusCode, e
}
func digestFile(ctx context.Context, path string) (string, int64, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	h := md5.New()
	buf := make([]byte, 1<<20)
	var size int64
	for {
		if e = ctx.Err(); e != nil {
			return "", 0, e
		}
		n, re := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
			size += int64(n)
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			return "", 0, re
		}
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

func ensureLocalMD5(ctx context.Context, file Obj, path string) error {
	if str(file["md5"]) != "" {
		return nil
	}
	sum, size, err := digestFile(ctx, path)
	if err != nil {
		return err
	}
	if size != num(file["size"]) {
		return errors.New("本地文件大小变化")
	}
	file["md5"] = sum
	return nil
}

type uploadProgressReader struct {
	r          io.Reader
	app        *App
	gid        string
	fileIndex  int
	readBytes  int64
	lastSample time.Time
	lastBytes  int64
	ewmaSpeed  int64
}

func (p *uploadProgressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.readBytes += int64(n)
		nowTime := time.Now()
		if elapsed := nowTime.Sub(p.lastSample); elapsed >= 250*time.Millisecond {
			delta := p.readBytes - p.lastBytes
			instSpeed := int64(float64(delta) / elapsed.Seconds())
			if p.ewmaSpeed <= 0 {
				p.ewmaSpeed = instSpeed
			} else {
				p.ewmaSpeed = (p.ewmaSpeed*3 + instSpeed*2) / 5
			}
			p.lastSample = nowTime
			p.lastBytes = p.readBytes
			p.app.setLiveTransfer(p.gid, p.fileIndex, p.readBytes, p.ewmaSpeed)
			p.app.notifyDownloads()
		}
	}
	return n, err
}

func (a *App) uploadFile(ctx context.Context, j Obj, fileIndex int, file Obj, save func() error) error {
	gid := str(j["gid"])
	path := str(file["path"])
	id := str(file["remoteId"])
	freshID := false
	if id == "" {
		o, _, e := a.driveJSON(ctx, "GET", driveBase+"/files/generateIds?count=1&space=drive&type=files", nil)
		if e != nil {
			return e
		}
		ids := list(o["ids"])
		if len(ids) != 1 {
			return errors.New("未取得云端文件 ID")
		}
		id = str(ids[0])
		freshID = true
		file["remoteId"] = id
		if e = save(); e != nil {
			return e
		}
	}
	endpoint := driveBase + "/files/" + url.PathEscape(id) + "?fields=id,size,md5Checksum&supportsAllDrives=true"
	if !freshID {
		meta, status, e := a.driveJSON(ctx, "GET", endpoint, nil)
		if e == nil {
			if e := ensureLocalMD5(ctx, file, path); e != nil {
				return e
			}
			if e := verifyDriveFile(file, meta); e != nil {
				return e
			}
			delete(file, "session")
			return save()
		}
		if status != 404 {
			return e
		}
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	size := num(file["size"])
	session := str(file["session"])
	startSession := func() error {
		data, _ := json.Marshal(Obj{"id": id, "name": file["name"], "parents": []string{str(j["cloudFolderId"])}})
		res, e := a.driveRequest(ctx, "POST", "https://www.googleapis.com/upload/drive/v3/files?uploadType=resumable&supportsAllDrives=true&fields=id,size,md5Checksum", data, map[string]string{"Content-Type": "application/json; charset=utf-8", "X-Upload-Content-Length": fmt.Sprint(size), "X-Upload-Content-Type": mimeFor(path)})
		if e != nil {
			return e
		}
		defer res.Body.Close()
		if res.StatusCode >= 300 {
			return fmt.Errorf("创建上传会话失败：HTTP %d", res.StatusCode)
		}
		session = res.Header.Get("Location")
		u, e := url.Parse(session)
		if e != nil || u.Scheme != "https" || u.Host != "www.googleapis.com" {
			return errors.New("上传地址校验失败")
		}
		file["session"] = session
		return save()
	}
	var offset int64
	if session != "" {
		res, e := a.driveRequest(ctx, "PUT", session, nil, map[string]string{"Content-Range": fmt.Sprintf("bytes */%d", size)})
		if e != nil {
			return e
		}
		var resumeMeta Obj
		if res.StatusCode == 200 || res.StatusCode == 201 {
			_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&resumeMeta)
		}
		res.Body.Close()
		switch res.StatusCode {
		case 200, 201:
			if e := ensureLocalMD5(ctx, file, path); e != nil {
				return e
			}
			if str(resumeMeta["md5Checksum"]) == "" {
				resumeMeta, _, e = a.driveJSON(ctx, "GET", endpoint, nil)
				if e != nil {
					return e
				}
			}
			if e := verifyDriveFile(file, resumeMeta); e != nil {
				return e
			}
			delete(file, "session")
			return save()
		case 308:
			offset = acknowledged(res.Header.Get("Range"))
		case 404, 410:
			session = ""
		default:
			return fmt.Errorf("恢复上传失败：HTTP %d", res.StatusCode)
		}
	}
	if session == "" {
		if e = startSession(); e != nil {
			return e
		}
	}
	var streamMD5 stdhash.Hash
	var hashedUntil int64
	if offset == 0 && str(file["md5"]) == "" {
		streamMD5 = md5.New()
	}
	chunk := driveChunkSize(size)
	var finalMeta Obj
	var currentSpeed int64
	for offset < size {
		if e = ctx.Err(); e != nil {
			return e
		}
		n := chunk
		if size-offset < n {
			n = size - offset
		}
		chunkStart := time.Now()
		var md5Checkpoint []byte
		canStreamHash := streamMD5 != nil && hashedUntil == offset
		if canStreamHash {
			if m, ok := streamMD5.(encoding.BinaryMarshaler); ok {
				md5Checkpoint, _ = m.MarshalBinary()
			} else {
				canStreamHash = false
			}
		}
		makeChunkBody := func() (io.ReadCloser, error) {
			var reader io.Reader = io.NewSectionReader(f, offset, n)
			if canStreamHash && md5Checkpoint != nil {
				if u, ok := streamMD5.(encoding.BinaryUnmarshaler); ok && u.UnmarshalBinary(md5Checkpoint) == nil {
					reader = io.TeeReader(reader, streamMD5)
				}
			}
			prog := &uploadProgressReader{
				r:          reader,
				app:        a,
				gid:        gid,
				fileIndex:  fileIndex,
				lastSample: time.Now(),
				ewmaSpeed:  currentSpeed,
			}
			return io.NopCloser(prog), nil
		}
		res, e := a.driveStreamRequest(ctx, "PUT", session, n, makeChunkBody, map[string]string{
			"Content-Type":  "application/octet-stream",
			"Content-Range": fmt.Sprintf("bytes %d-%d/%d", offset, offset+n-1, size),
		})
		if e != nil {
			a.clearLiveTransfer(gid)
			return e
		}
		prevOffset := offset
		if res.StatusCode == 200 || res.StatusCode == 201 {
			_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&finalMeta)
			res.Body.Close()
			offset = size
		} else if res.StatusCode == 308 {
			res.Body.Close()
			next := acknowledged(res.Header.Get("Range"))
			if next <= offset || next > size {
				a.clearLiveTransfer(gid)
				return errors.New("上传偏移无效")
			}
			offset = next
		} else {
			res.Body.Close()
			a.clearLiveTransfer(gid)
			return fmt.Errorf("上传失败：HTTP %d", res.StatusCode)
		}
		if canStreamHash && offset == prevOffset+n {
			hashedUntil = offset
		} else {
			streamMD5 = nil
		}
		if chunkElapsed := time.Since(chunkStart); chunkElapsed > 0 {
			chunkSpeed := int64(float64(offset-prevOffset) / chunkElapsed.Seconds())
			if currentSpeed <= 0 {
				currentSpeed = chunkSpeed
			} else {
				currentSpeed = (currentSpeed + chunkSpeed) / 2
			}
		}
		file["uploadedBytes"] = offset
		if e = save(); e != nil {
			a.clearLiveTransfer(gid)
			return e
		}
		a.setLiveTransfer(gid, fileIndex, 0, currentSpeed)
		a.notifyDownloads()
	}
	if streamMD5 != nil && hashedUntil == size && str(file["md5"]) == "" {
		if info, statErr := f.Stat(); statErr == nil && info.Size() == size {
			file["md5"] = hex.EncodeToString(streamMD5.Sum(nil))
		}
	}
	if e = ensureLocalMD5(ctx, file, path); e != nil {
		return e
	}
	meta := finalMeta
	if str(meta["md5Checksum"]) == "" {
		meta, _, e = a.driveJSON(ctx, "GET", endpoint, nil)
		if e != nil {
			return e
		}
	}
	if e = verifyDriveFile(file, meta); e != nil {
		return e
	}
	delete(file, "session")
	a.clearLiveTransfer(gid)
	return save()
}
func acknowledged(s string) int64 {
	if !strings.HasPrefix(s, "bytes=0-") {
		return 0
	}
	return parseInt(strings.TrimPrefix(s, "bytes=0-")) + 1
}
func verifyDriveFile(file, meta Obj) error {
	if parseInt(str(meta["size"])) != num(file["size"]) || strings.ToLower(str(meta["md5Checksum"])) != str(file["md5"]) {
		return errors.New("云端大小或 MD5 不匹配，本地文件保留")
	}
	file["verified"] = true
	file["remoteProvider"] = "google"
	return nil
}
func (a *App) driveConnected() bool {
	t, _ := a.Store.Get("secrets", "google")
	return t != nil && str(t["refresh_token"]) != ""
}

// archiveWanted: tasks saved to Drive always upload; other finished tasks
// upload only when global auto-archive is on.
func (a *App) archiveWanted(j Obj) bool {
	return str(j["saveTo"]) == "cloud" || flag(a.Settings()["cloudArchiveEnabled"])
}

// deleteLocalAfterArchive: a task saved to Drive keeps no local copy once the
// cloud copy is verified; otherwise the global setting decides.
func (a *App) deleteLocalAfterArchive(j Obj) bool {
	return str(j["saveTo"]) == "cloud" || flag(a.Settings()["cloudDeleteLocal"])
}

func archiveRetryDelay(attempts int64) time.Duration {
	switch {
	case attempts <= 1:
		return 5 * time.Second
	case attempts == 2:
		return 15 * time.Second
	case attempts == 3:
		return 45 * time.Second
	case attempts == 4:
		return 2 * time.Minute
	default:
		return 5 * time.Minute
	}
}

func (a *App) triggerArchive() {
	select {
	case a.archiveWake <- struct{}{}:
	default:
	}
}

func (a *App) isArchiving(gid string) bool {
	a.archiveMu.Lock()
	_, busy := a.archiveJobs[gid]
	a.archiveMu.Unlock()
	return busy
}

func (a *App) stopArchiveJob(gid string) error {
	a.archiveMu.Lock()
	cancel := a.archiveJobs[gid]
	a.archiveMu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	for i := 0; i < 100; i++ {
		if !a.isArchiving(gid) {
			a.clearLiveTransfer(gid)
			return nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return errors.New("云盘转存正在停止，请稍后重试")
}

func (a *App) dispatchArchiveJob(gid string) {
	a.archiveMu.Lock()
	if _, busy := a.archiveJobs[gid]; busy || a.ctx.Err() != nil {
		a.archiveMu.Unlock()
		return
	}
	select {
	case a.archiveSlots <- struct{}{}:
	default:
		// All upload slots are busy; let archiveCompleted pick waiting tasks in episode order.
		a.archiveMu.Unlock()
		a.triggerArchive()
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.archiveJobs[gid] = cancel
	a.wg.Add(1)
	a.archiveMu.Unlock()

	go func() {
		defer a.wg.Done()
		defer cancel()
		defer func() {
			a.archiveMu.Lock()
			delete(a.archiveJobs, gid)
			a.archiveMu.Unlock()
			a.clearLiveTransfer(gid)
			<-a.archiveSlots
			if a.ctx.Err() == nil {
				a.triggerArchive()
			}
		}()
		j, err := a.Store.Get("downloads", gid)
		if err != nil || j == nil || str(j["status"]) != "complete" || !a.archiveWanted(j) {
			return
		}
		prevAttempts := num(object(j["cloudArchive"])["attempts"])
		if err := a.archiveJobWithContext(ctx, j); err != nil && ctx.Err() == nil && a.ctx.Err() == nil {
			attempts := prevAttempts + 1
			retryAt := time.Now().Add(archiveRetryDelay(attempts)).Unix()
			_, _ = a.Store.Patch("downloads", gid, Obj{
				"cloudArchive": Obj{"status": "error", "error": err.Error(), "retryAt": retryAt, "attempts": attempts},
			})
			a.notifyDownloads()
			a.Log("warn", "云盘归档："+err.Error())
		}
	}()
}

func (a *App) archiveCompleted() {
	if !a.driveMu.TryLock() {
		return
	}
	defer a.driveMu.Unlock()
	jobs, e := a.Store.List("downloads")
	if e != nil {
		return
	}
	eligible := make([]Obj, 0, len(jobs))
	nowUnix := time.Now().Unix()
	for _, j := range jobs {
		if str(j["status"]) != "complete" || !a.archiveWanted(j) {
			continue
		}
		archive := object(j["cloudArchive"])
		if str(archive["status"]) == "archived" || num(archive["retryAt"]) > nowUnix {
			continue
		}
		if str(archive["status"]) == "verified" && !a.deleteLocalAfterArchive(j) {
			continue
		}
		eligible = append(eligible, j)
	}
	sortJobsByEpisodeOrder(eligible)

	var batchWG sync.WaitGroup
archiveLoop:
	for _, j := range eligible {
		if a.ctx.Err() != nil {
			break
		}
		gid := str(j["gid"])
		a.archiveMu.Lock()
		if _, busy := a.archiveJobs[gid]; busy || a.ctx.Err() != nil {
			a.archiveMu.Unlock()
			continue
		}
		a.archiveMu.Unlock()

		// Acquire an upload slot BEFORE spawning the goroutine so jobs always
		// begin uploading in strict episode order (Ep 1 -> Ep 2 -> ...).
		select {
		case a.archiveSlots <- struct{}{}:
		case <-a.ctx.Done():
			break archiveLoop
		}

		a.archiveMu.Lock()
		if _, busy := a.archiveJobs[gid]; busy || a.ctx.Err() != nil {
			a.archiveMu.Unlock()
			<-a.archiveSlots
			continue
		}
		ctx, cancel := context.WithCancel(a.ctx)
		a.archiveJobs[gid] = cancel
		a.wg.Add(1)
		batchWG.Add(1)
		a.archiveMu.Unlock()

		jobCopy := j
		go func(gid string, job Obj, ctx context.Context, cancel context.CancelFunc) {
			defer a.wg.Done()
			defer batchWG.Done()
			defer cancel()
			defer func() {
				a.archiveMu.Lock()
				delete(a.archiveJobs, gid)
				a.archiveMu.Unlock()
				a.clearLiveTransfer(gid)
				<-a.archiveSlots
			}()
			prevAttempts := num(object(job["cloudArchive"])["attempts"])
			if err := a.archiveJobWithContext(ctx, job); err != nil && ctx.Err() == nil && a.ctx.Err() == nil {
				attempts := prevAttempts + 1
				retryAt := time.Now().Add(archiveRetryDelay(attempts)).Unix()
				_, _ = a.Store.Patch("downloads", gid, Obj{
					"cloudArchive": Obj{"status": "error", "error": err.Error(), "retryAt": retryAt, "attempts": attempts},
				})
				a.notifyDownloads()
				a.Log("warn", "云盘归档："+err.Error())
			}
		}(gid, jobCopy, ctx, cancel)
	}
	batchWG.Wait()
}

func (a *App) archiveJob(j Obj) error {
	return a.archiveJobWithContext(a.ctx, j)
}

func (a *App) ensureDriveChildFolder(ctx context.Context, parent, name string) (string, error) {
	if parent == "" {
		parent = "root"
	}
	if !driveFolderID.MatchString(parent) {
		return "", errors.New("无效的 Google Drive 父文件夹 ID")
	}
	q := fmt.Sprintf("name = '%s' and '%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", escapeDriveQueryLiteral(name), escapeDriveQueryLiteral(parent))
	params := url.Values{}
	params.Set("q", q)
	params.Set("fields", "files(id,name)")
	params.Set("pageSize", "1")
	params.Set("supportsAllDrives", "true")
	params.Set("includeItemsFromAllDrives", "true")
	res, _, err := a.driveJSON(ctx, "GET", driveBase+"/files?"+params.Encode(), nil)
	if err == nil {
		for _, raw := range list(res["files"]) {
			if id := str(object(raw)["id"]); id != "" {
				return id, nil
			}
		}
	}
	created, _, err := a.driveJSON(
		ctx,
		"POST",
		driveBase+"/files?fields=id,name&supportsAllDrives=true",
		Obj{
			"name":     name,
			"mimeType": "application/vnd.google-apps.folder",
			"parents":  []string{parent},
		},
	)
	if err != nil {
		return "", err
	}
	id := str(created["id"])
	if id == "" {
		return "", errors.New("创建 Google Drive 子文件夹失败")
	}
	return id, nil
}

func (a *App) ensureOneDriveChildFolder(ctx context.Context, parent, name string) (string, error) {
	escaped := url.PathEscape(name)
	var lookupURL string
	if parent == "" || parent == "root" {
		lookupURL = msGraphBase + "/me/drive/root:/" + escaped + "?$select=id,name,folder"
	} else {
		lookupURL = msGraphBase + "/me/drive/items/" + url.PathEscape(parent) + ":/" + escaped + "?$select=id,name,folder"
	}
	meta, code, err := a.onedriveJSON(ctx, "GET", lookupURL, nil)
	if err == nil && code == 200 && meta["folder"] != nil {
		if id := str(meta["id"]); id != "" {
			return id, nil
		}
	}
	created, err := a.onedriveCreateFolder(ctx, parent, name)
	if err != nil {
		return "", err
	}
	return str(created["id"]), nil
}

func (a *App) ensureCloudSubfolderPath(ctx context.Context, provider, baseFolderID string, segments []string) (string, error) {
	if len(segments) == 0 {
		return baseFolderID, nil
	}
	a.cloudFolderMu.Lock()
	defer a.cloudFolderMu.Unlock()

	switch provider {
	case "webdav":
		rel := cleanWebDAVRelPath(baseFolderID)
		for _, seg := range segments {
			if seg = sanitizeFolderName(seg); seg != "" {
				if rel == "" {
					rel = seg
				} else {
					rel = rel + "/" + seg
				}
			}
		}
		if err := a.webdavEnsureDir(ctx, a.Settings(), rel); err != nil {
			return "", err
		}
		return rel, nil
	case "onedrive":
		cur := baseFolderID
		if cur == "" {
			cur = "root"
		}
		for _, seg := range segments {
			if seg = sanitizeFolderName(seg); seg == "" {
				continue
			}
			next, err := a.ensureOneDriveChildFolder(ctx, cur, seg)
			if err != nil || next == "" {
				return "", err
			}
			cur = next
		}
		return cur, nil
	default: // google
		cur := baseFolderID
		if cur == "" {
			cur = "root"
		}
		for _, seg := range segments {
			if seg = sanitizeFolderName(seg); seg == "" {
				continue
			}
			next, err := a.ensureDriveChildFolder(ctx, cur, seg)
			if err != nil || next == "" {
				return "", err
			}
			cur = next
		}
		return cur, nil
	}
}

func (a *App) uploadSidecarSubtitle(ctx context.Context, provider, folderID, fileName, contentType string, data []byte) (string, error) {
	if len(data) == 0 || fileName == "" {
		return "", errors.New("空字幕文件")
	}
	switch provider {
	case "webdav":
		rel := cleanWebDAVRelPath(folderID)
		target := fileName
		if rel != "" {
			target = rel + "/" + fileName
		}
		res, err := a.webdavRequest(ctx, a.Settings(), "PUT", target, false, bytes.NewReader(data), int64(len(data)), map[string]string{
			"Content-Type": contentType + "; charset=utf-8",
		})
		if err != nil {
			return "", err
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return "", fmt.Errorf("WebDAV 字幕上传失败 (%d)", res.StatusCode)
		}
		return target, nil
	case "onedrive":
		escaped := url.PathEscape(fileName)
		var endpoint string
		if folderID == "" || folderID == "root" {
			endpoint = msGraphBase + "/me/drive/root:/" + escaped + ":/content"
		} else {
			endpoint = msGraphBase + "/me/drive/items/" + url.PathEscape(folderID) + ":/" + escaped + ":/content"
		}
		res, err := a.onedriveRequest(ctx, "PUT", endpoint, data, map[string]string{
			"Content-Type": contentType + "; charset=utf-8",
		}, true)
		if err != nil {
			return "", err
		}
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return "", fmt.Errorf("OneDrive 字幕上传失败 (%d)", res.StatusCode)
		}
		var meta Obj
		_ = json.Unmarshal(body, &meta)
		return str(meta["id"]), nil
	default: // google
		parent := folderID
		if parent == "" {
			parent = "root"
		}
		boundary := "bgmcat_sub_" + hash(fileName + now())[:16]
		metaBytes, _ := json.Marshal(Obj{"name": fileName, "parents": []string{parent}})
		var body bytes.Buffer
		body.WriteString("--" + boundary + "\r\nContent-Type: application/json; charset=UTF-8\r\n\r\n")
		body.Write(metaBytes)
		body.WriteString("\r\n--" + boundary + "\r\nContent-Type: " + contentType + "; charset=utf-8\r\n\r\n")
		body.Write(data)
		body.WriteString("\r\n--" + boundary + "--\r\n")
		res, err := a.driveRequest(ctx, "POST", "https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart&fields=id,name&supportsAllDrives=true", body.Bytes(), map[string]string{
			"Content-Type": "multipart/related; boundary=" + boundary,
		})
		if err != nil {
			return "", err
		}
		respBody, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return "", fmt.Errorf("Google Drive 字幕上传失败 (%d)", res.StatusCode)
		}
		var meta Obj
		_ = json.Unmarshal(respBody, &meta)
		return str(meta["id"]), nil
	}
}

func (a *App) uploadJobSidecarSubtitles(ctx context.Context, j Obj, provider, folderID, localMediaPath string) {
	gid := str(j["gid"])
	subs := a.media.EnsureLocalSubtitles(ctx, gid, localMediaPath)
	if len(subs) == 0 {
		return
	}
	existing := object(j["cloudSubtitles"])
	if existing == nil {
		existing = Obj{}
	}
	updated := false
	for _, sub := range subs {
		key := sub.TrackID + sub.Ext
		if prev := object(existing[key]); prev != nil && str(prev["remoteId"]) != "" {
			continue
		}
		upCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		rid, err := a.uploadSidecarSubtitle(upCtx, provider, folderID, sub.FileName, sub.ContentType, sub.Data)
		cancel()
		if err == nil && rid != "" {
			existing[key] = Obj{
				"remoteId": rid,
				"provider": provider,
				"fileName": sub.FileName,
			}
			updated = true
		}
	}
	if updated {
		j["cloudSubtitles"] = existing
		_, _ = a.Store.Patch("downloads", gid, Obj{"cloudSubtitles": existing})
	}
}

func (a *App) archiveJobWithContext(ctx context.Context, j Obj) error {
	gid := str(j["gid"])
	defer a.clearLiveTransfer(gid)
	files := list(j["files"])
	if len(files) == 0 {
		return errors.New("任务没有文件清单")
	}
	provider := firstNonEmpty(str(j["cloudProvider"]), a.activeCloudProvider())
	if !validCloudProvider(provider) {
		provider = "google"
	}
	j["cloudProvider"] = provider
	if str(j["cloudFolderId"]) == "" {
		baseFolder := a.cloudProviderFolderID(provider)
		folderMode := normalizeDownloadFolderMode(str(a.Settings()["downloadFolderMode"]))
		subfolders := buildDownloadSubfolders(folderMode, str(j["animeTitle"]), firstNonEmpty(str(j["feedItemTitle"]), str(j["title"])))
		if len(subfolders) > 0 {
			if targetFolder, err := a.ensureCloudSubfolderPath(ctx, provider, baseFolder, subfolders); err == nil && targetFolder != "" {
				j["cloudFolderId"] = targetFolder
			} else {
				j["cloudFolderId"] = baseFolder
			}
		} else {
			j["cloudFolderId"] = baseFolder
		}
	}
	save := func() error {
		_, e := a.Store.Patch("downloads", gid, Obj{"files": files, "cloudFolderId": j["cloudFolderId"], "cloudProvider": provider})
		return e
	}
	_, _ = a.Store.Patch("downloads", gid, Obj{"cloudArchive": Obj{"status": "uploading", "error": nil, "retryAt": 0}})
	a.notifyDownloads()
	for idx, v := range files {
		f := object(v)
		path := str(f["path"])
		if !inside(str(j["directory"]), path) {
			return errors.New("文件路径越界")
		}
		if !flag(f["verified"]) {
			info, e := os.Lstat(path)
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				return errors.New("只允许归档普通文件")
			}
			if info.Size() != num(f["size"]) {
				return errors.New("本地文件大小变化")
			}
			f["mtimeNano"] = info.ModTime().UnixNano()
			if str(f["name"]) == "" {
				f["name"] = gid + "-" + safeName(filepath.Base(path))
			}
		} else if str(f["name"]) == "" {
			f["name"] = gid + "-" + safeName(filepath.Base(path))
		}
		var uploadErr error
		switch provider {
		case "onedrive":
			uploadErr = a.onedriveUploadFile(ctx, j, idx, f, save)
		case "webdav":
			uploadErr = a.webdavUploadFile(ctx, j, idx, f, save)
		default:
			uploadErr = a.uploadFile(ctx, j, idx, f, save)
		}
		if uploadErr != nil {
			return uploadErr
		}
	}
	if mediaBest, err := mediaFile(Obj{"files": files}); err == nil {
		a.media.EnsureLocalThumbnail(ctx, gid, str(mediaBest["path"]))
		a.uploadJobSidecarSubtitles(ctx, j, provider, str(j["cloudFolderId"]), str(mediaBest["path"]))
	}
	_, _ = a.Store.Patch("downloads", gid, Obj{"cloudArchive": Obj{"status": "verified", "error": nil, "retryAt": 0}})
	a.notifyDownloads()
	if !a.deleteLocalAfterArchive(j) {
		return nil
	}
	if a.media.Busy(gid) {
		return errors.New("正在播放，稍后清理")
	}
	for _, v := range files {
		f := object(v)
		fProv := firstNonEmpty(str(f["remoteProvider"]), provider)
		res, e := a.openCloudRangeResponse(ctx, fProv, str(f["remoteId"]), "GET", "bytes=0-0")
		if e != nil {
			return e
		}
		_, readErr := io.CopyN(io.Discard, res.Body, 1)
		res.Body.Close()
		if (res.StatusCode != 206 && res.StatusCode != 200) || readErr != nil {
			return errors.New("云端文件暂不可读，保留本地文件")
		}
	}
	for _, v := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		latest, _ := a.Store.Get("downloads", gid)
		if latest != nil {
			j = latest
		}
		if !a.deleteLocalAfterArchive(j) || !a.archiveWanted(j) {
			return nil
		}
		f := object(v)
		path := str(f["path"])
		info, e := os.Lstat(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("清理前文件类型变化")
		}
		if info.Size() != num(f["size"]) {
			return errors.New("清理前文件校验失败")
		}
		recordedMtime := num(f["mtimeNano"])
		if recordedMtime == 0 || info.ModTime().UnixNano() != recordedMtime || str(f["md5"]) == "" {
			sum, size, e := digestFile(ctx, path)
			if e != nil {
				return e
			}
			if sum != str(f["md5"]) || size != num(f["size"]) {
				return errors.New("清理前文件校验失败")
			}
		}
		if a.media.Busy(gid) {
			return errors.New("正在播放，稍后清理")
		}
		if e = os.Remove(path); e != nil {
			return e
		}
	}
	removedPaths := make([]string, 0, len(files))
	for _, v := range files {
		if p := str(object(v)["path"]); p != "" {
			removedPaths = append(removedPaths, p)
		}
	}
	a.cleanupArchivedLocalStaging(gid, str(j["directory"]), removedPaths)
	_, e := a.Store.Patch("downloads", gid, Obj{"cloudArchive": Obj{"status": "archived", "error": nil, "retryAt": 0}})
	a.notifyDownloads()
	return e
}
