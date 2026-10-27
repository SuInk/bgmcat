package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const driveBase = "https://www.googleapis.com/drive/v3"

func (a *App) cloudRoutes() {
	a.cloudSetupRoutes()
	a.handle("GET /api/cloud/status", func(w http.ResponseWriter, r *http.Request) error {
		token, _ := a.Store.Get("secrets", "google")
		ok(w, Obj{"connected": token != nil && str(token["refresh_token"]) != "", "configured": str(a.Settings()["googleClientId"]) != ""})
		return nil
	})
	a.handle("POST /api/cloud/connect", func(w http.ResponseWriter, r *http.Request) error {
		s := a.Settings()
		client := str(s["googleClientId"])
		redirect := str(s["googleRedirectUri"])
		if client == "" || redirect == "" {
			return errors.New("请先填写 Google OAuth 客户端 ID 和回调地址")
		}
		u, e := url.Parse(redirect)
		if e != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"))) {
			return errors.New("回调地址需为 HTTPS 或本机回环地址")
		}
		state, verifier := randomID(32), randomID(32)
		sum := sha256.Sum256([]byte(verifier))
		a.oauthMu.Lock()
		a.oauth[state] = Obj{"verifier": verifier, "redirect": redirect, "expires": time.Now().Add(10 * time.Minute).Unix()}
		a.oauthMu.Unlock()
		values := url.Values{"client_id": {client}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {"https://www.googleapis.com/auth/drive"}, "access_type": {"offline"}, "prompt": {"consent"}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
		ok(w, Obj{"url": "https://accounts.google.com/o/oauth2/v2/auth?" + values.Encode()})
		return nil
	})
	a.handle("GET /api/cloud/callback", func(w http.ResponseWriter, r *http.Request) error {
		// Google sends the user's browser here, so failures get a page a person can
		// read instead of the API's JSON error body.
		if e := a.driveCallback(w, r); e != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>Google Drive 连接失败</title><h1>Google Drive 连接失败</h1><p>`+html.EscapeString(e.Error())+`</p><p>回到 Bangumi Cat 的「设置 → 云盘归档」重新点「连接 Google」。</p>`)
		}
		return nil
	})
	a.handle("POST /api/cloud/disconnect", func(w http.ResponseWriter, r *http.Request) error {
		e := a.Store.Delete("secrets", "google")
		_, _ = a.Store.Patch("config", "settings", Obj{"cloudArchiveEnabled": false})
		if e == nil {
			ok(w, Obj{"connected": false})
		}
		return e
	})
}
func (a *App) driveCallback(w http.ResponseWriter, r *http.Request) error {
	state := r.URL.Query().Get("state")
	a.oauthMu.Lock()
	pending := a.oauth[state]
	delete(a.oauth, state)
	a.oauthMu.Unlock()
	if pending == nil || num(pending["expires"]) < time.Now().Unix() {
		return errors.New("登录请求已失效，请重新连接")
	}
	if r.URL.Query().Get("error") != "" {
		return errors.New("Google 授权被取消")
	}
	s := a.Settings()
	token, e := a.oauthExchange(r.Context(), url.Values{"client_id": {str(s["googleClientId"])}, "client_secret": {str(s["googleClientSecret"])}, "code": {r.URL.Query().Get("code")}, "code_verifier": {str(pending["verifier"])}, "redirect_uri": {str(pending["redirect"])}, "grant_type": {"authorization_code"}})
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, e = io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>Google Drive 已连接</title><h1>Google Drive 已连接</h1><p>可以关闭此窗口，回到 Bangumi Cat 的「设置 → 云盘归档」选择保存目录。</p>`)
	return e
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
	s := a.Settings()
	next, e := a.oauthExchange(ctx, url.Values{"client_id": {str(s["googleClientId"])}, "client_secret": {str(s["googleClientSecret"])}, "refresh_token": {str(t["refresh_token"])}, "grant_type": {"refresh_token"}})
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
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.Host != "www.googleapis.com" || u.User != nil {
		return nil, errors.New("无效 Drive 地址")
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, e := a.accessToken(ctx, attempt > 0)
		if e != nil {
			return nil, e
		}
		reqCtx, cancelReq := context.WithCancel(ctx)
		client := *a.http
		transport, selectedIP := a.driveTransport()
		client.Transport = transport
		if strings.Contains(endpoint, "alt=media") {
			client.Timeout = 0
		} else if method == "PUT" && strings.HasPrefix(u.Path, "/upload/") {
			// An 8 MiB chunk on a slow uplink easily outlasts the 45 s API timeout,
			// which used to kill every chunk so uploads never advanced. Give the
			// chunk a deadline that scales with its size instead.
			client.Timeout = 0
			cancelReq()
			reqCtx, cancelReq = context.WithTimeout(ctx, uploadChunkTimeout(len(data)))
		}
		req, e := http.NewRequestWithContext(reqCtx, method, endpoint, bytes.NewReader(data))
		if e != nil {
			cancelReq()
			return nil, e
		}
		req.Header.Set("Authorization", "Bearer "+token)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
		res, e := client.Do(req)
		if e != nil {
			cancelReq()
			if selectedIP != "" && ctx.Err() == nil {
				a.failDriveIP(selectedIP)
			}
			return nil, e
		}
		res.Body = cancelOnClose{res.Body, cancelReq}
		if res.StatusCode == 401 && attempt == 0 {
			res.Body.Close()
			continue
		}
		return res, nil
	}
	return nil, errors.New("Google 认证失败")
}

// uploadChunkTimeout allows a chunk to crawl at ~16 KiB/s before giving up,
// but never less than two minutes (small final chunks, slow TLS handshakes).
func uploadChunkTimeout(size int) time.Duration {
	return max(2*time.Minute, time.Duration(size/(16<<10))*time.Second)
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
	buf := make([]byte, 128*1024)
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
func (a *App) uploadFile(ctx context.Context, j Obj, file Obj, save func() error) error {
	id := str(file["remoteId"])
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
		file["remoteId"] = id
		if e = save(); e != nil {
			return e
		}
	}
	endpoint := driveBase + "/files/" + url.PathEscape(id) + "?fields=id,size,md5Checksum&supportsAllDrives=true"
	meta, status, e := a.driveJSON(ctx, "GET", endpoint, nil)
	if e == nil {
		if e := verifyDriveFile(file, meta); e != nil {
			return e
		}
		delete(file, "session")
		return save()
	}
	if status != 404 {
		return e
	}
	path := str(file["path"])
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
		res.Body.Close()
		switch res.StatusCode {
		case 200, 201:
			meta, _, e = a.driveJSON(ctx, "GET", endpoint, nil)
			if e != nil {
				return e
			}
			if e := verifyDriveFile(file, meta); e != nil {
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
	const chunk = 8 * 1024 * 1024
	for offset < size {
		if e = ctx.Err(); e != nil {
			return e
		}
		n := int64(chunk)
		if size-offset < n {
			n = size - offset
		}
		data := make([]byte, n)
		if _, e = f.ReadAt(data, offset); e != nil {
			return e
		}
		res, e := a.driveRequest(ctx, "PUT", session, data, map[string]string{"Content-Type": "application/octet-stream", "Content-Range": fmt.Sprintf("bytes %d-%d/%d", offset, offset+n-1, size)})
		if e != nil {
			return e
		}
		res.Body.Close()
		if res.StatusCode == 200 || res.StatusCode == 201 {
			offset = size
		} else if res.StatusCode == 308 {
			next := acknowledged(res.Header.Get("Range"))
			if next <= offset || next > size {
				return errors.New("上传偏移无效")
			}
			offset = next
		} else {
			return fmt.Errorf("上传失败：HTTP %d", res.StatusCode)
		}
		file["uploadedBytes"] = offset
		if e = save(); e != nil {
			return e
		}
	}
	meta, _, e = a.driveJSON(ctx, "GET", endpoint, nil)
	if e != nil {
		return e
	}
	if e = verifyDriveFile(file, meta); e != nil {
		return e
	}
	delete(file, "session")
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
	return nil
}
func (a *App) archiveCompleted() {
	if !flag(a.Settings()["cloudArchiveEnabled"]) {
		return
	}
	if !a.driveMu.TryLock() {
		return
	}
	defer a.driveMu.Unlock()
	jobs, e := a.Store.List("downloads")
	if e != nil {
		return
	}
	for _, j := range jobs {
		if a.ctx.Err() != nil {
			return
		}
		if str(j["status"]) != "complete" {
			continue
		}
		archive := object(j["cloudArchive"])
		if str(archive["status"]) == "archived" || num(archive["retryAt"]) > time.Now().Unix() {
			continue
		}
		if str(archive["status"]) == "verified" && !flag(a.Settings()["cloudDeleteLocal"]) {
			continue
		}
		if e = a.archiveJob(j); e != nil && a.ctx.Err() == nil {
			_, _ = a.Store.Patch("downloads", str(j["gid"]), Obj{"cloudArchive": Obj{"status": "error", "error": e.Error(), "retryAt": time.Now().Add(5 * time.Minute).Unix()}})
			a.Log("warn", "云盘归档："+e.Error())
		}
	}
}
func (a *App) archiveJob(j Obj) error {
	ctx := a.ctx
	gid := str(j["gid"])
	files := list(j["files"])
	if len(files) == 0 {
		return errors.New("任务没有文件清单")
	}
	if str(j["cloudFolderId"]) == "" {
		folder := str(a.Settings()["cloudDriveFolderId"])
		if folder == "" {
			folder = "root"
		}
		j["cloudFolderId"] = folder
	}
	save := func() error {
		_, e := a.Store.Patch("downloads", gid, Obj{"files": files, "cloudFolderId": j["cloudFolderId"]})
		return e
	}
	_, _ = a.Store.Patch("downloads", gid, Obj{"cloudArchive": Obj{"status": "uploading", "error": nil, "retryAt": 0}})
	for _, v := range files {
		f := object(v)
		path := str(f["path"])
		if !inside(str(j["directory"]), path) {
			return errors.New("文件路径越界")
		}
		if str(f["md5"]) == "" {
			info, e := os.Lstat(path)
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				return errors.New("只允许归档普通文件")
			}
			sum, size, e := digestFile(ctx, path)
			if e != nil {
				return e
			}
			if size != num(f["size"]) {
				return errors.New("本地文件大小变化")
			}
			f["md5"] = sum
			f["name"] = gid + "-" + safeName(filepath.Base(path))
			if e = save(); e != nil {
				return e
			}
		}
		if e := a.uploadFile(ctx, j, f, save); e != nil {
			return e
		}
	}
	_, _ = a.Store.Patch("downloads", gid, Obj{"cloudArchive": Obj{"status": "verified", "error": nil, "retryAt": 0}})
	if !flag(a.Settings()["cloudDeleteLocal"]) {
		return nil
	}
	if a.media.Busy(gid) {
		return errors.New("正在播放，稍后清理")
	}
	for _, v := range files {
		f := object(v)
		res, e := a.driveRequest(ctx, "GET", driveBase+"/files/"+url.PathEscape(str(f["remoteId"]))+"?alt=media", nil, map[string]string{"Range": "bytes=0-0"})
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
		if !flag(a.Settings()["cloudDeleteLocal"]) || !flag(a.Settings()["cloudArchiveEnabled"]) {
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
		sum, size, e := digestFile(ctx, path)
		if e != nil {
			return e
		}
		if sum != str(f["md5"]) || size != num(f["size"]) {
			return errors.New("清理前文件校验失败")
		}
		if a.media.Busy(gid) {
			return errors.New("正在播放，稍后清理")
		}
		if e = os.Remove(path); e != nil {
			return e
		}
	}
	_, e := a.Store.Patch("downloads", gid, Obj{"cloudArchive": Obj{"status": "archived", "error": nil, "retryAt": 0}})
	return e
}
