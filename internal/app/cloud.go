package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	msGraphBase = "https://graph.microsoft.com/v1.0"
	msAuthURL   = "https://login.microsoftonline.com/common/oauth2/v2.0/authorize"
	msTokenURL  = "https://login.microsoftonline.com/common/oauth2/v2.0/token"
)

var (
	DefaultOneDriveClientID     string
	DefaultOneDriveClientSecret string
	onedriveItemIDPattern       = regexp.MustCompile(`^[a-zA-Z0-9_!.:-]{1,240}$`)
)

var supportedCloudProviders = []string{"google", "onedrive", "webdav"}

func validCloudProvider(provider string) bool {
	for _, p := range supportedCloudProviders {
		if p == provider {
			return true
		}
	}
	return false
}

func cloudProviderDisplayName(provider string) string {
	switch provider {
	case "onedrive":
		return "Microsoft OneDrive"
	case "webdav":
		return "WebDAV"
	default:
		return "Google Drive"
	}
}

func (a *App) activeCloudProvider() string {
	p := strings.TrimSpace(str(a.Settings()["cloudProvider"]))
	if validCloudProvider(p) {
		return p
	}
	return "google"
}

func (a *App) cloudProviderFolderSettingKey(provider string) string {
	switch provider {
	case "onedrive":
		return "onedriveFolderId"
	case "webdav":
		return "webdavRootPath"
	default:
		return "cloudDriveFolderId"
	}
}

func (a *App) cloudProviderFolderID(provider string) string {
	s := a.Settings()
	switch provider {
	case "onedrive":
		v := strings.TrimSpace(str(s["onedriveFolderId"]))
		if v == "" || !onedriveItemIDPattern.MatchString(v) {
			return "root"
		}
		return v
	case "webdav":
		v := cleanWebDAVRelPath(str(s["webdavRootPath"]))
		if v == "" {
			return "root"
		}
		return v
	default:
		v := strings.TrimSpace(str(s["cloudDriveFolderId"]))
		if v == "" || !driveFolderID.MatchString(v) {
			return "root"
		}
		return v
	}
}

func (a *App) effectiveOneDriveCredentials() (clientID, clientSecret string, isBuiltin bool) {
	s := a.Settings()
	cid := strings.TrimSpace(str(s["onedriveClientId"]))
	csec := strings.TrimSpace(str(s["onedriveClientSecret"]))
	if cid != "" {
		return cid, csec, false
	}
	builtinID := firstNonEmpty(os.Getenv("BGMCAT_ONEDRIVE_CLIENT_ID"), os.Getenv("ONEDRIVE_CLIENT_ID"), DefaultOneDriveClientID)
	builtinSec := firstNonEmpty(os.Getenv("BGMCAT_ONEDRIVE_CLIENT_SECRET"), os.Getenv("ONEDRIVE_CLIENT_SECRET"), DefaultOneDriveClientSecret)
	if builtinID != "" {
		return builtinID, builtinSec, true
	}
	return "", "", false
}

func (a *App) onedriveConnected() bool {
	t, _ := a.Store.Get("secrets", "onedrive")
	return t != nil && str(t["refresh_token"]) != ""
}

func (a *App) webdavIdentityHash(s Obj) string {
	return hash(strings.TrimRight(strings.TrimSpace(str(s["webdavUrl"])), "/") + "|" + strings.TrimSpace(str(s["webdavUsername"])) + "|" + str(s["webdavPassword"]))
}

func (a *App) webdavConnected() bool {
	s := a.Settings()
	u := strings.TrimSpace(str(s["webdavUrl"]))
	if !httpURL(u) {
		return false
	}
	sec, _ := a.Store.Get("secrets", "webdav")
	return sec != nil && flag(sec["verified"]) && str(sec["identityHash"]) == a.webdavIdentityHash(s)
}

func (a *App) cloudProviderConnected(provider string) bool {
	switch provider {
	case "onedrive":
		return a.onedriveConnected()
	case "webdav":
		return a.webdavConnected()
	default:
		return a.driveConnected()
	}
}

func (a *App) activeCloudConnected() bool {
	return a.cloudProviderConnected(a.activeCloudProvider())
}

func (a *App) cloudProviderStatusObj(provider string) Obj {
	if !validCloudProvider(provider) {
		provider = a.activeCloudProvider()
	}
	switch provider {
	case "onedrive":
		cid, _, builtin := a.effectiveOneDriveCredentials()
		return Obj{
			"provider":    "onedrive",
			"displayName": "Microsoft OneDrive",
			"authKind":    "oauth",
			"connected":   a.onedriveConnected(),
			"configured":  cid != "",
			"builtin":     builtin,
			"folderId":    a.cloudProviderFolderID("onedrive"),
		}
	case "webdav":
		s := a.Settings()
		configured := httpURL(strings.TrimSpace(str(s["webdavUrl"])))
		return Obj{
			"provider":    "webdav",
			"displayName": "WebDAV",
			"authKind":    "basic",
			"connected":   a.webdavConnected(),
			"configured":  configured,
			"builtin":     false,
			"folderId":    a.cloudProviderFolderID("webdav"),
		}
	default:
		cid, _, builtin := a.effectiveGoogleCredentials()
		return Obj{
			"provider":    "google",
			"displayName": "Google Drive",
			"authKind":    "oauth",
			"connected":   a.driveConnected(),
			"configured":  cid != "",
			"builtin":     builtin,
			"folderId":    a.cloudProviderFolderID("google"),
		}
	}
}

/* ---------- Generic Cloud Media ID Encoding ---------- */

func encodeCloudMediaID(provider, fileID, fileName, mimeType string) string {
	if provider == "" || provider == "google" {
		return encodeDriveMediaID(fileID, fileName, mimeType)
	}
	safeName := strings.TrimSpace(fileName)
	safeName = strings.ReplaceAll(safeName, "\\", "_")
	safeName = strings.ReplaceAll(safeName, "/", "_")
	safeName = filepath.Base(safeName)
	if safeName == "" || safeName == "." || safeName == ".." {
		safeName = "video"
	}
	if !isPlayableExt(filepath.Ext(safeName)) {
		lowerMime := strings.ToLower(mimeType)
		switch {
		case strings.Contains(lowerMime, "matroska"):
			safeName += ".mkv"
		case strings.Contains(lowerMime, "webm"):
			safeName += ".webm"
		case strings.Contains(lowerMime, "flac"):
			safeName += ".flac"
		case strings.HasPrefix(lowerMime, "audio/"):
			safeName += ".mp3"
		default:
			safeName += ".mp4"
		}
	}
	return "cloud~" + provider + "~" + base64.RawURLEncoding.EncodeToString([]byte(fileID)) + "~" + base64.RawURLEncoding.EncodeToString([]byte(safeName))
}

func decodeCloudMediaID(gid string) (provider, fileID, safeName string, ok bool) {
	if fid, sname, dOk := decodeDriveMediaID(gid); dOk {
		return "google", fid, sname, true
	}
	if !strings.HasPrefix(gid, "cloud~") {
		return "", "", "", false
	}
	parts := strings.Split(gid, "~")
	if len(parts) != 4 || parts[0] != "cloud" || !validCloudProvider(parts[1]) {
		return "", "", "", false
	}
	rawID, err1 := base64.RawURLEncoding.DecodeString(parts[2])
	rawName, err2 := base64.RawURLEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(rawID) == 0 {
		return "", "", "", false
	}
	name := filepath.Base(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(string(rawName)), "\\", "_"), "/", "_"))
	if name == "" || name == "." || name == ".." || !isPlayableExt(filepath.Ext(name)) {
		return "", "", "", false
	}
	return parts[1], string(rawID), name, true
}

/* ---------- Microsoft OneDrive Implementation ---------- */

func (a *App) startOneDriveConnect(r *http.Request, payload Obj) (Obj, error) {
	s := a.Settings()
	clientID, _, _ := a.effectiveOneDriveCredentials()
	redirect := firstNonEmpty(str(payload["redirectUri"]), str(s["onedriveRedirectUri"]))
	if redirect == "" && r.Host != "" {
		scheme := "http"
		if isHTTPSRequest(r) {
			scheme = "https"
		}
		redirect = scheme + "://" + r.Host + "/api/cloud/callback"
	}
	if clientID == "" || redirect == "" {
		return nil, errors.New("请先填写 Microsoft 应用（客户端）ID")
	}
	u, err := url.Parse(redirect)
	if err != nil || !isAllowedOAuthRedirect(u) {
		return nil, errors.New("回调地址需为 HTTPS 或本机回环地址")
	}
	returnTo := sanitizeRelativeReturnTo(str(payload["returnTo"]))
	state := "onedrive:" + randomID(28)
	verifier := randomID(32)
	sum := sha256.Sum256([]byte(verifier))
	a.oauthMu.Lock()
	a.oauth[state] = Obj{
		"provider": "onedrive",
		"verifier": verifier,
		"redirect": redirect,
		"returnTo": returnTo,
		"expires":  time.Now().Add(10 * time.Minute).Unix(),
	}
	a.oauthMu.Unlock()

	values := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirect},
		"response_mode":         {"query"},
		"scope":                 {"Files.ReadWrite offline_access User.Read"},
		"prompt":                {"consent"},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	return Obj{"url": msAuthURL + "?" + values.Encode()}, nil
}

func (a *App) onedriveOAuthExchange(ctx context.Context, form url.Values) (Obj, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", msTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 Microsoft 认证服务失败：%w", err)
	}
	defer res.Body.Close()
	var parsed Obj
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&parsed)
	if res.StatusCode >= 300 {
		desc := firstNonEmpty(str(parsed["error_description"]), str(parsed["error"]), fmt.Sprintf("HTTP %d", res.StatusCode))
		return nil, errors.New("OneDrive 授权失败：" + desc)
	}
	return parsed, nil
}

func (a *App) completeOneDriveCallback(r *http.Request, pending Obj) error {
	if pending == nil || num(pending["expires"]) < time.Now().Unix() {
		return errors.New("登录请求已失效，请重新连接")
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		desc := r.URL.Query().Get("error_description")
		if desc != "" {
			return errors.New("OneDrive 授权未完成：" + desc)
		}
		return errors.New("OneDrive 授权被取消")
	}
	clientID, clientSecret, _ := a.effectiveOneDriveCredentials()
	form := url.Values{
		"client_id":     {clientID},
		"code":          {r.URL.Query().Get("code")},
		"code_verifier": {str(pending["verifier"])},
		"redirect_uri":  {str(pending["redirect"])},
		"grant_type":    {"authorization_code"},
		"scope":         {"Files.ReadWrite offline_access User.Read"},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	token, err := a.onedriveOAuthExchange(r.Context(), form)
	if err != nil {
		return err
	}
	if str(token["refresh_token"]) == "" {
		return errors.New("未取得 OneDrive 刷新凭据（请确保勾选 offline_access）")
	}
	token["expires_at"] = time.Now().Add(time.Duration(num(token["expires_in"])) * time.Second).Unix()
	if err := a.Store.Put("secrets", "onedrive", token); err != nil {
		return err
	}
	_, _ = a.Store.Patch("config", "settings", Obj{"cloudProvider": "onedrive"})
	a.triggerArchive()
	return nil
}

func (a *App) serveCloudOAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if !strings.HasPrefix(state, "onedrive:") {
		a.serveDriveCallback(w, r)
		return
	}
	a.oauthMu.Lock()
	pending := a.oauth[state]
	delete(a.oauth, state)
	a.oauthMu.Unlock()

	returnTo := sanitizeRelativeReturnTo(str(pending["returnTo"]))
	if err := a.completeOneDriveCallback(r, pending); err != nil {
		if returnTo != "" {
			http.Redirect(w, r, appendQueryParam(returnTo, "cloudError", err.Error()), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>OneDrive 连接失败</title><style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0e1015;color:#eef1f8;font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.card{max-width:420px;padding:28px;border-radius:16px;background:#161922;border:1px solid #262b38;text-align:center}h1{margin:0 0 8px;font-size:18px;color:#ff6b7a}p{margin:6px 0;color:#9aa3b5}</style><div class="card"><h1>OneDrive 连接失败</h1><p>`+html.EscapeString(err.Error())+`</p></div>`)
		return
	}
	if returnTo != "" {
		http.Redirect(w, r, returnTo, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>OneDrive 已连接</title><style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0e1015;color:#eef1f8;font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.card{max-width:420px;padding:28px;border-radius:16px;background:#161922;border:1px solid #262b38;text-align:center}h1{margin:0 0 8px;font-size:18px;color:#3dd68c}p{margin:6px 0;color:#9aa3b5}</style><div class="card"><h1>Microsoft OneDrive 已连接</h1><p>授权已完成，可以关闭此标签页返回猫追番。</p></div><script>try{window.close()}catch(e){}</script>`)
}

func (a *App) onedriveAccessToken(ctx context.Context) (string, error) {
	sec, err := a.Store.Get("secrets", "onedrive")
	if err != nil || sec == nil || str(sec["refresh_token"]) == "" {
		return "", errors.New("OneDrive 未连接")
	}
	if tok := str(sec["access_token"]); tok != "" && num(sec["expires_at"]) > time.Now().Add(60*time.Second).Unix() {
		return tok, nil
	}
	clientID, clientSecret, _ := a.effectiveOneDriveCredentials()
	if clientID == "" {
		return "", errors.New("缺少 OneDrive 客户端 ID")
	}
	form := url.Values{
		"client_id":     {clientID},
		"refresh_token": {str(sec["refresh_token"])},
		"grant_type":    {"refresh_token"},
		"scope":         {"Files.ReadWrite offline_access User.Read"},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	fresh, err := a.onedriveOAuthExchange(ctx, form)
	if err != nil {
		return "", err
	}
	if str(fresh["refresh_token"]) == "" {
		fresh["refresh_token"] = sec["refresh_token"]
	}
	fresh["expires_at"] = time.Now().Add(time.Duration(num(fresh["expires_in"])) * time.Second).Unix()
	_ = a.Store.Put("secrets", "onedrive", fresh)
	return str(fresh["access_token"]), nil
}

func (a *App) onedriveRequest(ctx context.Context, method, endpoint string, payload []byte, headers map[string]string, withAuth bool) (*http.Response, error) {
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return nil, err
	}
	if withAuth {
		tok, tErr := a.onedriveAccessToken(ctx)
		if tErr != nil {
			return nil, tErr
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	return a.http.Do(req)
}

func (a *App) onedriveJSON(ctx context.Context, method, endpoint string, payload any) (Obj, int, error) {
	var data []byte
	headers := map[string]string{}
	if payload != nil {
		var err error
		data, err = json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		headers["Content-Type"] = "application/json"
	}
	res, err := a.onedriveRequest(ctx, method, endpoint, data, headers, true)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	var parsed Obj
	_ = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&parsed)
	if res.StatusCode >= 300 {
		errObj := object(parsed["error"])
		msg := firstNonEmpty(str(errObj["message"]), fmt.Sprintf("OneDrive HTTP %d", res.StatusCode))
		return parsed, res.StatusCode, errors.New(msg)
	}
	return parsed, res.StatusCode, nil
}

func (a *App) onedriveListFolders(ctx context.Context, parent, pageToken string) (Obj, error) {
	endpoint := pageToken
	if endpoint == "" || !strings.HasPrefix(endpoint, "https://graph.microsoft.com/") {
		if parent == "" || parent == "root" {
			endpoint = msGraphBase + "/me/drive/root/children?$select=id,name,folder&$top=200"
		} else {
			if !onedriveItemIDPattern.MatchString(parent) {
				return nil, errors.New("无效的 OneDrive 目录 ID")
			}
			endpoint = msGraphBase + "/me/drive/items/" + url.PathEscape(parent) + "/children?$select=id,name,folder&$top=200"
		}
	}
	res, _, err := a.onedriveJSON(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	files := []Obj{}
	for _, raw := range list(res["value"]) {
		item := object(raw)
		if item["folder"] == nil {
			continue
		}
		files = append(files, Obj{"id": str(item["id"]), "name": str(item["name"])})
	}
	return Obj{"files": files, "nextPageToken": str(res["@odata.nextLink"])}, nil
}

func (a *App) onedriveCreateFolder(ctx context.Context, parent, name string) (Obj, error) {
	endpoint := msGraphBase + "/me/drive/root/children"
	if parent != "" && parent != "root" {
		if !onedriveItemIDPattern.MatchString(parent) {
			return nil, errors.New("无效的 OneDrive 目录 ID")
		}
		endpoint = msGraphBase + "/me/drive/items/" + url.PathEscape(parent) + "/children"
	}
	created, _, err := a.onedriveJSON(ctx, "POST", endpoint, Obj{
		"name":                              name,
		"folder":                            Obj{},
		"@microsoft.graph.conflictBehavior": "rename",
	})
	if err != nil {
		return nil, err
	}
	return Obj{"id": str(created["id"]), "name": str(created["name"])}, nil
}

func (a *App) onedriveUploadFile(ctx context.Context, j Obj, fileIndex int, file Obj, save func() error) error {
	gid := str(j["gid"])
	localPath := str(file["path"])
	size := num(file["size"])
	folderID := str(j["cloudFolderId"])
	if folderID == "" {
		folderID = "root"
	}
	fileName := str(file["name"])

	if id := str(file["remoteId"]); id != "" && onedriveItemIDPattern.MatchString(id) {
		meta, status, err := a.onedriveJSON(ctx, "GET", msGraphBase+"/me/drive/items/"+url.PathEscape(id)+"?$select=id,size", nil)
		if err == nil && num(meta["size"]) == size {
			file["verified"] = true
			file["remoteProvider"] = "onedrive"
			delete(file, "session")
			return save()
		}
		if status != 404 && err != nil {
			return err
		}
	}

	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	session := str(file["session"])
	if session == "" {
		var createURL string
		escapedName := url.PathEscape(fileName)
		if folderID == "root" {
			createURL = msGraphBase + "/me/drive/root:/" + escapedName + ":/createUploadSession"
		} else {
			createURL = msGraphBase + "/me/drive/items/" + url.PathEscape(folderID) + ":/" + escapedName + ":/createUploadSession"
		}
		sessObj, _, sErr := a.onedriveJSON(ctx, "POST", createURL, Obj{
			"item": Obj{
				"@microsoft.graph.conflictBehavior": "replace",
				"name":                              fileName,
			},
		})
		if sErr != nil {
			return sErr
		}
		session = str(sessObj["uploadUrl"])
		if !strings.HasPrefix(session, "https://") {
			return errors.New("OneDrive 未返回有效上传会话地址")
		}
		file["session"] = session
		if err := save(); err != nil {
			return err
		}
	}

	const odChunk = int64(10 * 1024 * 1024) // Multiple of 320 KiB required by Graph API
	var offset int64
	var finalMeta Obj
	var currentSpeed int64

	for offset < size {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := odChunk
		if size-offset < n {
			n = size - offset
		}
		chunkStart := time.Now()
		prog := &uploadProgressReader{
			r:          io.NewSectionReader(f, offset, n),
			app:        a,
			gid:        gid,
			fileIndex:  fileIndex,
			lastSample: time.Now(),
			ewmaSpeed:  currentSpeed,
		}
		req, rErr := http.NewRequestWithContext(ctx, "PUT", session, prog)
		if rErr != nil {
			return rErr
		}
		req.ContentLength = n
		req.Header.Set("Content-Length", strconv.FormatInt(n, 10))
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+n-1, size))
		res, doErr := a.http.Do(req)
		if doErr != nil {
			a.clearLiveTransfer(gid)
			return doErr
		}
		prevOffset := offset
		if res.StatusCode == 200 || res.StatusCode == 201 {
			_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&finalMeta)
			res.Body.Close()
			offset = size
		} else if res.StatusCode == 202 {
			res.Body.Close()
			offset += n
		} else {
			res.Body.Close()
			if res.StatusCode == 404 || res.StatusCode == 410 {
				delete(file, "session")
				_ = save()
			}
			a.clearLiveTransfer(gid)
			return fmt.Errorf("OneDrive 上传失败：HTTP %d", res.StatusCode)
		}
		if elapsed := time.Since(chunkStart); elapsed > 0 {
			sp := int64(float64(offset-prevOffset) / elapsed.Seconds())
			if currentSpeed <= 0 {
				currentSpeed = sp
			} else {
				currentSpeed = (currentSpeed + sp) / 2
			}
		}
		file["uploadedBytes"] = offset
		if err := save(); err != nil {
			a.clearLiveTransfer(gid)
			return err
		}
		a.setLiveTransfer(gid, fileIndex, 0, currentSpeed)
		a.notifyDownloads()
	}

	remoteID := str(finalMeta["id"])
	if remoteID == "" || num(finalMeta["size"]) != size {
		return errors.New("OneDrive 云端文件大小校验不匹配，保留本地文件")
	}
	if err := ensureLocalMD5(ctx, file, localPath); err != nil {
		return err
	}
	file["remoteId"] = remoteID
	file["remoteProvider"] = "onedrive"
	file["verified"] = true
	delete(file, "session")
	a.clearLiveTransfer(gid)
	return save()
}

/* ---------- WebDAV Implementation ---------- */

func cleanWebDAVRelPath(raw string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" || trimmed == "." || trimmed == "root" {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	cleanParts := make([]string, 0, len(parts))
	for _, seg := range parts {
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		cleanParts = append(cleanParts, seg)
	}
	return strings.Join(cleanParts, "/")
}

func buildWebDAVURL(baseRaw, relPath string, trailingSlash bool) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseRaw), "/")
	if !httpURL(base) {
		return "", errors.New("WebDAV 服务地址无效")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	clean := cleanWebDAVRelPath(relPath)
	if clean != "" {
		segs := strings.Split(clean, "/")
		escaped := make([]string, len(segs))
		for i, seg := range segs {
			escaped[i] = url.PathEscape(seg)
		}
		basePath := strings.TrimRight(u.EscapedPath(), "/")
		u.RawPath = basePath + "/" + strings.Join(escaped, "/")
		unEscBase := strings.TrimRight(u.Path, "/")
		u.Path = unEscBase + "/" + clean
	}
	if trailingSlash && !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
		if u.RawPath != "" && !strings.HasSuffix(u.RawPath, "/") {
			u.RawPath += "/"
		}
	}
	return u.String(), nil
}

func (a *App) webdavRequest(ctx context.Context, s Obj, method, relPath string, trailingSlash bool, body io.Reader, contentLength int64, headers map[string]string) (*http.Response, error) {
	targetURL, err := buildWebDAVURL(str(s["webdavUrl"]), relPath, trailingSlash)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		return nil, err
	}
	if contentLength >= 0 {
		req.ContentLength = contentLength
	}
	user := strings.TrimSpace(str(s["webdavUsername"]))
	pass := str(s["webdavPassword"])
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	return a.http.Do(req)
}

type webdavMultistatus struct {
	Responses []struct {
		Href     string `xml:"href"`
		Propstat []struct {
			Status string `xml:"status"`
			Prop   struct {
				DisplayName  string `xml:"displayname"`
				ContentLen   string `xml:"getcontentlength"`
				ContentType  string `xml:"getcontenttype"`
				LastModified string `xml:"getlastmodified"`
				ResourceType struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}

type webdavItem struct {
	Name    string
	RelPath string
	IsDir   bool
	Size    int64
	Mime    string
	ModTime string
}

func (a *App) webdavPropfind(ctx context.Context, s Obj, relDir string, depth string) ([]webdavItem, error) {
	res, err := a.webdavRequest(ctx, s, "PROPFIND", relDir, true, nil, 0, map[string]string{
		"Depth":        depth,
		"Content-Type": "application/xml; charset=utf-8",
	})
	if err != nil {
		return nil, fmt.Errorf("连接 WebDAV 失败：%w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return nil, errors.New("WebDAV 认证失败，请检查账号与密码")
	}
	if res.StatusCode == http.StatusNotFound {
		return nil, errors.New("WebDAV 目录不存在")
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("WebDAV HTTP %d", res.StatusCode)
	}
	if depth == "0" {
		return nil, nil
	}
	var ms webdavMultistatus
	if err := xml.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&ms); err != nil {
		return nil, fmt.Errorf("解析 WebDAV 目录响应失败：%w", err)
	}

	baseParsed, _ := url.Parse(strings.TrimRight(strings.TrimSpace(str(s["webdavUrl"])), "/"))
	baseURLPath := ""
	if baseParsed != nil {
		baseURLPath = strings.TrimRight(baseParsed.Path, "/")
	}
	reqRel := cleanWebDAVRelPath(relDir)

	var out []webdavItem
	for _, resp := range ms.Responses {
		rawHref := strings.TrimSpace(resp.Href)
		if hURL, pErr := url.Parse(rawHref); pErr == nil && hURL.Path != "" {
			rawHref = hURL.Path
		} else if unesc, uErr := url.PathUnescape(rawHref); uErr == nil {
			rawHref = unesc
		}
		rel := strings.TrimPrefix(rawHref, baseURLPath)
		rel = cleanWebDAVRelPath(rel)
		if rel == reqRel {
			continue // Skip the directory itself
		}
		isDir := false
		var sz int64
		var mimeStr, modStr, dispName string
		for _, ps := range resp.Propstat {
			if strings.Contains(ps.Status, "200") || len(resp.Propstat) == 1 {
				if ps.Prop.ResourceType.Collection != nil {
					isDir = true
				}
				sz, _ = strconv.ParseInt(strings.TrimSpace(ps.Prop.ContentLen), 10, 64)
				mimeStr = strings.TrimSpace(ps.Prop.ContentType)
				modStr = strings.TrimSpace(ps.Prop.LastModified)
				dispName = strings.TrimSpace(ps.Prop.DisplayName)
				break
			}
		}
		name := path.Base(rel)
		if name == "" || name == "." || name == "/" {
			name = dispName
		}
		if name == "" || shouldSkipSystemHiddenFile(name) {
			continue
		}
		if modStr != "" {
			if t, tErr := http.ParseTime(modStr); tErr == nil {
				modStr = t.UTC().Format(time.RFC3339)
			}
		}
		out = append(out, webdavItem{
			Name:    name,
			RelPath: rel,
			IsDir:   isDir,
			Size:    sz,
			Mime:    mimeStr,
			ModTime: modStr,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (a *App) webdavEnsureDir(ctx context.Context, s Obj, relDir string) error {
	clean := cleanWebDAVRelPath(relDir)
	if clean == "" {
		return nil
	}
	parts := strings.Split(clean, "/")
	acc := ""
	for _, seg := range parts {
		if acc == "" {
			acc = seg
		} else {
			acc = acc + "/" + seg
		}
		res, err := a.webdavRequest(ctx, s, "MKCOL", acc, true, nil, 0, nil)
		if err != nil {
			return err
		}
		res.Body.Close()
		// 201 Created, 405 Method Not Allowed (already exists), 301/200 on some servers
		if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
			return errors.New("WebDAV 权限不足，无法创建目录")
		}
	}
	return nil
}

func (a *App) connectWebDAV(ctx context.Context, overrides Obj) (Obj, error) {
	s := a.Settings()
	patch := Obj{"cloudProvider": "webdav"}
	for _, k := range []string{"webdavUrl", "webdavUsername", "webdavPassword", "webdavRootPath"} {
		if v, exists := overrides[k]; exists && v != nil {
			s[k] = strings.TrimSpace(str(v))
			if k == "webdavPassword" {
				s[k] = str(v)
			}
			patch[k] = s[k]
		}
	}
	if !httpURL(strings.TrimSpace(str(s["webdavUrl"]))) {
		return nil, errors.New("请填写有效的 WebDAV 地址（如 https://dav.jianguoyun.com/dav/）")
	}
	tCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := a.webdavPropfind(tCtx, s, "", "0"); err != nil {
		return nil, err
	}
	rootRel := cleanWebDAVRelPath(str(s["webdavRootPath"]))
	if rootRel != "" {
		if err := a.webdavEnsureDir(tCtx, s, rootRel); err != nil {
			return nil, err
		}
	}
	if _, err := a.Store.Patch("config", "settings", patch); err != nil {
		return nil, err
	}
	if err := a.Store.Put("secrets", "webdav", Obj{
		"verified":     true,
		"identityHash": a.webdavIdentityHash(s),
		"connectedAt":  now(),
	}); err != nil {
		return nil, err
	}
	a.triggerArchive()
	return a.cloudProviderStatusObj("webdav"), nil
}

func (a *App) webdavUploadFile(ctx context.Context, j Obj, fileIndex int, file Obj, save func() error) error {
	s := a.Settings()
	if !a.webdavConnected() {
		return errors.New("WebDAV 未连接")
	}
	gid := str(j["gid"])
	localPath := str(file["path"])
	size := num(file["size"])
	folderRel := cleanWebDAVRelPath(str(j["cloudFolderId"]))
	fileName := str(file["name"])
	remoteRel := fileName
	if folderRel != "" {
		remoteRel = folderRel + "/" + fileName
	}

	if existing := str(file["remoteId"]); existing != "" && str(file["remoteProvider"]) == "webdav" {
		hRes, hErr := a.webdavRequest(ctx, s, "HEAD", existing, false, nil, 0, nil)
		if hErr == nil {
			cl, _ := strconv.ParseInt(hRes.Header.Get("Content-Length"), 10, 64)
			hRes.Body.Close()
			if hRes.StatusCode == 200 && cl == size {
				file["verified"] = true
				return save()
			}
		}
	}

	if folderRel != "" {
		if err := a.webdavEnsureDir(ctx, s, folderRel); err != nil {
			return err
		}
	}
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	prog := &uploadProgressReader{
		r:          f,
		app:        a,
		gid:        gid,
		fileIndex:  fileIndex,
		lastSample: time.Now(),
	}
	res, err := a.webdavRequest(ctx, s, "PUT", remoteRel, false, prog, size, map[string]string{
		"Content-Type": mimeFor(localPath),
	})
	if err != nil {
		a.clearLiveTransfer(gid)
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		a.clearLiveTransfer(gid)
		return fmt.Errorf("WebDAV 上传失败：HTTP %d", res.StatusCode)
	}

	hRes, hErr := a.webdavRequest(ctx, s, "HEAD", remoteRel, false, nil, 0, nil)
	if hErr != nil {
		a.clearLiveTransfer(gid)
		return hErr
	}
	cl, _ := strconv.ParseInt(hRes.Header.Get("Content-Length"), 10, 64)
	hRes.Body.Close()
	if hRes.StatusCode >= 300 || (cl > 0 && cl != size) {
		a.clearLiveTransfer(gid)
		return errors.New("WebDAV 云端大小校验失败，保留本地文件")
	}

	if err := ensureLocalMD5(ctx, file, localPath); err != nil {
		return err
	}
	file["uploadedBytes"] = size
	file["remoteId"] = remoteRel
	file["remoteProvider"] = "webdav"
	file["verified"] = true
	a.clearLiveTransfer(gid)
	return save()
}

/* ---------- Unified Cloud Stream & Readability Helpers ---------- */

func (a *App) openCloudRangeResponse(ctx context.Context, provider, remoteID, method, rangeHeader string) (*http.Response, error) {
	if provider == "" {
		provider = "google"
	}
	switch provider {
	case "onedrive":
		if !a.onedriveConnected() {
			return nil, errors.New("OneDrive 未连接")
		}
		headers := map[string]string{"Accept-Encoding": "identity"}
		if rangeHeader != "" {
			headers["Range"] = rangeHeader
		}
		return a.onedriveRequest(ctx, method, msGraphBase+"/me/drive/items/"+url.PathEscape(remoteID)+"/content", nil, headers, true)
	case "webdav":
		if !a.webdavConnected() {
			return nil, errors.New("WebDAV 未连接")
		}
		headers := map[string]string{"Accept-Encoding": "identity"}
		if rangeHeader != "" {
			headers["Range"] = rangeHeader
		}
		return a.webdavRequest(ctx, a.Settings(), method, remoteID, false, nil, 0, headers)
	default:
		if !a.driveConnected() {
			return nil, errors.New("Google Drive 未连接")
		}
		return a.driveRequest(ctx, method, driveBase+"/files/"+url.PathEscape(remoteID)+"?alt=media&supportsAllDrives=true", nil, map[string]string{
			"Range":           rangeHeader,
			"Accept-Encoding": "identity",
		})
	}
}

/* ---------- Unified Cloud File Browser Handlers (OneDrive & WebDAV) ---------- */

func (a *App) listOneDriveBrowserEntries(ctx context.Context, rawParent, pageToken, search string) (Obj, error) {
	archiveFolderID := a.cloudProviderFolderID("onedrive")
	cid, _, _ := a.effectiveOneDriveCredentials()
	if !a.onedriveConnected() {
		return Obj{
			"provider":        "onedrive",
			"providerName":    "Microsoft OneDrive",
			"connected":       false,
			"configured":      cid != "",
			"archiveFolderId": archiveFolderID,
			"parent":          "root",
			"parentName":      "OneDrive",
			"entries":         []Obj{},
		}, nil
	}

	parent := rawParent
	parentName := "OneDrive"
	if parent == "" || parent == "default" {
		if archiveFolderID != "" && archiveFolderID != "root" {
			meta, code, err := a.onedriveJSON(ctx, "GET", msGraphBase+"/me/drive/items/"+url.PathEscape(archiveFolderID)+"?$select=id,name,folder", nil)
			if err == nil && code == 200 && meta["folder"] != nil {
				parent = archiveFolderID
				if n := strings.TrimSpace(str(meta["name"])); n != "" {
					parentName = n
				}
			} else {
				parent = "root"
			}
		} else {
			parent = "root"
		}
	} else {
		if parent != "root" && !onedriveItemIDPattern.MatchString(parent) {
			return nil, errors.New("无效的 OneDrive 目录 ID")
		}
		if parent != "root" && pageToken == "" {
			if meta, code, err := a.onedriveJSON(ctx, "GET", msGraphBase+"/me/drive/items/"+url.PathEscape(parent)+"?$select=id,name", nil); err == nil && code == 200 {
				if n := strings.TrimSpace(str(meta["name"])); n != "" {
					parentName = n
				}
			}
		}
	}

	listURL := pageToken
	if listURL == "" || !strings.HasPrefix(listURL, "https://graph.microsoft.com/") {
		if parent == "root" {
			listURL = msGraphBase + "/me/drive/root/children?$select=id,name,size,lastModifiedDateTime,folder,file&$top=100"
		} else {
			listURL = msGraphBase + "/me/drive/items/" + url.PathEscape(parent) + "/children?$select=id,name,size,lastModifiedDateTime,folder,file&$top=100"
		}
	}
	res, _, err := a.onedriveJSON(ctx, "GET", listURL, nil)
	if err != nil {
		return nil, err
	}

	var quotaObj Obj
	if pageToken == "" {
		qCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		if driveMeta, code, qErr := a.onedriveJSON(qCtx, "GET", msGraphBase+"/me/drive?$select=quota,owner", nil); qErr == nil && code == 200 {
			q := object(driveMeta["quota"])
			ownerUser := object(object(driveMeta["owner"])["user"])
			quotaObj = Obj{
				"usageBytes": num(q["used"]),
				"limitBytes": num(q["total"]),
				"userEmail":  firstNonEmpty(str(ownerUser["email"]), str(ownerUser["displayName"])),
			}
		}
		cancel()
	}

	rawItems := list(res["value"])
	entries := make([]Obj, 0, len(rawItems))
	lowerSearch := strings.ToLower(search)
	for _, raw := range rawItems {
		f := object(raw)
		name := str(f["name"])
		if lowerSearch != "" && !strings.Contains(strings.ToLower(name), lowerSearch) {
			continue
		}
		id := str(f["id"])
		isDir := f["folder"] != nil
		mimeType := str(object(f["file"])["mimeType"])
		ext := ""
		if !isDir {
			ext = strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
		}
		kind := classifyFileKind(isDir, ext, mimeType)
		playable := !isDir && (isPlayableExt(filepath.Ext(name)) || kind == "video" || kind == "audio")
		mediaID := ""
		if playable {
			mediaID = encodeCloudMediaID("onedrive", id, name, mimeType)
		}
		entryObj := Obj{
			"id":       id,
			"name":     name,
			"path":     id,
			"isDir":    isDir,
			"mimeType": mimeType,
			"size":     num(f["size"]),
			"modTime":  str(f["lastModifiedDateTime"]),
			"ext":      ext,
			"kind":     kind,
			"playable": playable,
			"mediaId":  mediaID,
		}
		if trimmed := stripGidPrefix(name); trimmed != "" {
			entryObj["displayName"] = trimmed
		}
		entries = append(entries, entryObj)
	}
	out := Obj{
		"provider":        "onedrive",
		"providerName":    "Microsoft OneDrive",
		"connected":       true,
		"configured":      cid != "",
		"archiveFolderId": archiveFolderID,
		"parent":          parent,
		"parentName":      parentName,
		"nextPageToken":   str(res["@odata.nextLink"]),
		"entries":         entries,
	}
	if quotaObj != nil {
		out["quota"] = quotaObj
	}
	return out, nil
}

func (a *App) listWebDAVBrowserEntries(ctx context.Context, rawParent, search string) (Obj, error) {
	s := a.Settings()
	archiveFolderID := a.cloudProviderFolderID("webdav")
	configured := httpURL(strings.TrimSpace(str(s["webdavUrl"])))
	if !a.webdavConnected() {
		return Obj{
			"provider":        "webdav",
			"providerName":    "WebDAV",
			"connected":       false,
			"configured":      configured,
			"archiveFolderId": archiveFolderID,
			"parent":          "root",
			"parentName":      "WebDAV",
			"entries":         []Obj{},
		}, nil
	}

	parent := rawParent
	if parent == "" || parent == "default" {
		if archiveFolderID != "root" && archiveFolderID != "" {
			_ = a.webdavEnsureDir(ctx, s, archiveFolderID)
			parent = archiveFolderID
		} else {
			parent = "root"
		}
	}
	relDir := cleanWebDAVRelPath(parent)
	parentID := "root"
	parentName := "WebDAV"
	if relDir != "" {
		parentID = relDir
		parentName = path.Base(relDir)
	}

	items, err := a.webdavPropfind(ctx, s, relDir, "1")
	if err != nil {
		return nil, err
	}
	lowerSearch := strings.ToLower(search)
	entries := make([]Obj, 0, len(items))
	for _, it := range items {
		if lowerSearch != "" && !strings.Contains(strings.ToLower(it.Name), lowerSearch) {
			continue
		}
		ext := ""
		if !it.IsDir {
			ext = strings.TrimPrefix(strings.ToLower(filepath.Ext(it.Name)), ".")
		}
		kind := classifyFileKind(it.IsDir, ext, it.Mime)
		playable := !it.IsDir && (isPlayableExt(filepath.Ext(it.Name)) || kind == "video" || kind == "audio")
		mediaID := ""
		if playable {
			mediaID = encodeCloudMediaID("webdav", it.RelPath, it.Name, it.Mime)
		}
		entryObj := Obj{
			"id":       it.RelPath,
			"name":     it.Name,
			"path":     it.RelPath,
			"isDir":    it.IsDir,
			"mimeType": it.Mime,
			"size":     it.Size,
			"modTime":  it.ModTime,
			"ext":      ext,
			"kind":     kind,
			"playable": playable,
			"mediaId":  mediaID,
		}
		if trimmed := stripGidPrefix(it.Name); trimmed != "" {
			entryObj["displayName"] = trimmed
		}
		entries = append(entries, entryObj)
	}
	return Obj{
		"provider":        "webdav",
		"providerName":    "WebDAV",
		"connected":       true,
		"configured":      configured,
		"archiveFolderId": archiveFolderID,
		"parent":          parentID,
		"parentName":      parentName,
		"nextPageToken":   "",
		"entries":         entries,
	}, nil
}
