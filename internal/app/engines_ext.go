package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var supportedDownloadClients = []string{"native", "qbittorrent", "aria2-rpc", "transmission"}

func validDownloadClient(client string) bool {
	for _, c := range supportedDownloadClients {
		if c == client {
			return true
		}
	}
	return false
}

func downloadClientDisplayName(client string) string {
	switch client {
	case "qbittorrent":
		return "qBittorrent"
	case "aria2-rpc":
		return "Aria2 RPC"
	case "transmission":
		return "Transmission"
	default:
		return "内置引擎"
	}
}

func (a *App) effectiveAria2RPCURL(s Obj) string {
	if raw := strings.TrimSpace(str(s["aria2RpcUrl"])); raw != "" {
		return raw
	}
	host := strings.TrimSpace(str(s["aria2RpcHost"]))
	if host == "" {
		host = "127.0.0.1"
	}
	port := num(s["aria2RpcPort"])
	if port <= 0 {
		port = 6800
	}
	return fmt.Sprintf("http://%s:%d/jsonrpc", host, port)
}

func (a *App) effectiveTransmissionRPCURL(s Obj) string {
	raw := strings.TrimSpace(str(s["transmissionRpcUrl"]))
	if raw == "" {
		return "http://127.0.0.1:9091/transmission/rpc"
	}
	return raw
}

func (a *App) aria2RPCCall(ctx context.Context, rpcURL, secret, method string, params ...any) (any, error) {
	if !httpURL(rpcURL) {
		return nil, errors.New("Aria2 RPC 地址无效")
	}
	fullParams := make([]any, 0, len(params)+1)
	if secret != "" {
		fullParams = append(fullParams, "token:"+secret)
	}
	fullParams = append(fullParams, params...)
	bodyBytes, err := json.Marshal(Obj{
		"jsonrpc": "2.0",
		"id":      "bgmcat",
		"method":  method,
		"params":  fullParams,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", rpcURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var parsed struct {
		Result any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&parsed); err != nil {
		if res.StatusCode >= 300 {
			return nil, fmt.Errorf("Aria2 RPC HTTP %d", res.StatusCode)
		}
		return nil, err
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("Aria2 RPC：%s", parsed.Error.Message)
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("Aria2 RPC HTTP %d", res.StatusCode)
	}
	return parsed.Result, nil
}

func (a *App) aria2RPCOnline() bool {
	s := a.Settings()
	rpcURL := a.effectiveAria2RPCURL(s)
	secret := str(s["aria2RpcSecret"])
	key := "aria2rpc:" + hash(rpcURL+"|"+secret)
	cached, _ := a.Store.Get("cache", key)
	if num(cached["expires"]) > time.Now().Unix() {
		return flag(cached["online"])
	}
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Second)
	defer cancel()
	_, err := a.aria2RPCCall(ctx, rpcURL, secret, "aria2.getVersion")
	online := err == nil
	_ = a.Store.Put("cache", key, Obj{"online": online, "expires": time.Now().Add(10 * time.Second).Unix()})
	return online
}

func (a *App) aria2RPCDownload(ctx context.Context, j Obj) error {
	s := a.Settings()
	rpcURL := a.effectiveAria2RPCURL(s)
	secret := str(s["aria2RpcSecret"])
	saveDir := str(j["directory"])

	remoteGID := str(j["aria2Gid"])
	if remoteGID == "" {
		opts := Obj{"dir": saveDir}
		if flag(s["aria2SeedEnabled"]) {
			opts["seed-ratio"] = "0.0"
		} else {
			opts["seed-time"] = "0"
		}
		if trackers := list(s["btTrackers"]); len(trackers) > 0 {
			strs := make([]string, 0, len(trackers))
			for _, tr := range trackers {
				if v := strings.TrimSpace(str(tr)); v != "" {
					strs = append(strs, v)
				}
			}
			if len(strs) > 0 {
				opts["bt-tracker"] = strings.Join(strs, ",")
			}
		}
		res, err := a.aria2RPCCall(ctx, rpcURL, secret, "aria2.addUri", []string{str(j["sourceUrl"])}, opts)
		if err != nil {
			if fb := jobFallbackMagnet(j); fb != "" {
				res, err = a.aria2RPCCall(ctx, rpcURL, secret, "aria2.addUri", []string{fb}, opts)
			}
		}
		if err != nil {
			return err
		}
		remoteGID = strings.TrimSpace(str(res))
		if remoteGID == "" {
			return errors.New("Aria2 未返回任务 GID")
		}
		_, _ = a.Store.Patch("downloads", str(j["gid"]), Obj{"aria2Gid": remoteGID})
	} else {
		_, _ = a.aria2RPCCall(ctx, rpcURL, secret, "aria2.unpause", remoteGID)
	}

	defer func() {
		if remoteGID != "" {
			stopCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			_, _ = a.aria2RPCCall(stopCtx, rpcURL, secret, "aria2.pause", remoteGID)
		}
	}()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		raw, err := a.aria2RPCCall(ctx, rpcURL, secret, "aria2.tellStatus", remoteGID)
		if err != nil {
			return err
		}
		st := object(raw)
		if followed := list(st["followedBy"]); len(followed) > 0 && str(followed[0]) != "" {
			remoteGID = str(followed[0])
			_, _ = a.Store.Patch("downloads", str(j["gid"]), Obj{"aria2Gid": remoteGID})
			continue
		}
		total, _ := strconv.ParseInt(str(st["totalLength"]), 10, 64)
		completed, _ := strconv.ParseInt(str(st["completedLength"]), 10, 64)
		dlSpeed, _ := strconv.ParseInt(str(st["downloadSpeed"]), 10, 64)
		ulSpeed, _ := strconv.ParseInt(str(st["uploadSpeed"]), 10, 64)
		patch := Obj{
			"totalLength":     total,
			"completedLength": completed,
			"downloadSpeed":   dlSpeed,
			"uploadSpeed":     ulSpeed,
		}
		if ih := strings.ToLower(strings.TrimSpace(str(st["infoHash"]))); ih != "" {
			patch["infoHash"] = ih
		}
		_, _ = a.Store.Patch("downloads", str(j["gid"]), patch)
		a.notifyDownloads()

		switch str(st["status"]) {
		case "error":
			msg := strings.TrimSpace(str(st["errorMessage"]))
			if msg == "" {
				msg = "Aria2 下载失败（错误码 " + str(st["errorCode"]) + "）"
			}
			return errors.New(msg)
		case "removed":
			return errors.New("任务已在 Aria2 中被移除")
		case "complete":
			rawFiles := list(st["files"])
			out := make([]Obj, 0, len(rawFiles))
			for _, rf := range rawFiles {
				fObj := object(rf)
				if str(fObj["selected"]) == "false" {
					continue
				}
				p := filepath.Clean(str(fObj["path"]))
				if strings.HasPrefix(filepath.Base(p), "[METADATA]") {
					continue
				}
				if !inside(saveDir, p) {
					localCandidate := filepath.Join(saveDir, filepath.Base(p))
					if fi, sErr := os.Stat(localCandidate); sErr == nil && fi.Mode().IsRegular() {
						p = localCandidate
					} else {
						return errors.New("Aria2 保存路径与本机下载目录不匹配")
					}
				}
				sz, _ := strconv.ParseInt(str(fObj["length"]), 10, 64)
				out = append(out, Obj{"path": p, "size": sz})
			}
			if len(out) == 0 {
				return errors.New("Aria2 任务完成但未返回有效文件")
			}
			_, err = a.Store.Patch("downloads", str(j["gid"]), Obj{"files": out})
			remoteGID = "" // Already completed; skip defer pause
			return err
		}
	}
}

func (a *App) transmissionCall(ctx context.Context, rpcURL, username, password, method string, arguments Obj, sessionID *string) (Obj, error) {
	if !httpURL(rpcURL) {
		return nil, errors.New("Transmission RPC 地址无效")
	}
	payload, err := json.Marshal(Obj{
		"method":    method,
		"arguments": arguments,
	})
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", rpcURL, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if sessionID != nil && *sessionID != "" {
			req.Header.Set("X-Transmission-Session-Id", *sessionID)
		}
		if username != "" || password != "" {
			req.SetBasicAuth(username, password)
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if res.StatusCode == http.StatusConflict {
			nextToken := res.Header.Get("X-Transmission-Session-Id")
			res.Body.Close()
			if nextToken != "" && sessionID != nil && attempt == 0 {
				*sessionID = nextToken
				continue
			}
			return nil, errors.New("Transmission 会话握手失败 (409)")
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusUnauthorized {
			return nil, errors.New("Transmission 认证失败，请检查用户名和密码")
		}
		if res.StatusCode >= 300 {
			return nil, fmt.Errorf("Transmission HTTP %d", res.StatusCode)
		}
		var parsed Obj
		if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&parsed); err != nil {
			return nil, err
		}
		if str(parsed["result"]) != "success" {
			return nil, fmt.Errorf("Transmission：%s", str(parsed["result"]))
		}
		return object(parsed["arguments"]), nil
	}
	return nil, errors.New("Transmission 请求失败")
}

func (a *App) transmissionOnline() bool {
	s := a.Settings()
	rpcURL := a.effectiveTransmissionRPCURL(s)
	user := str(s["transmissionUsername"])
	pass := str(s["transmissionPassword"])
	key := "trrpc:" + hash(rpcURL+"|"+user+"|"+pass)
	cached, _ := a.Store.Get("cache", key)
	if num(cached["expires"]) > time.Now().Unix() {
		return flag(cached["online"])
	}
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Second)
	defer cancel()
	sid := ""
	_, err := a.transmissionCall(ctx, rpcURL, user, pass, "session-get", Obj{"fields": []string{"version"}}, &sid)
	online := err == nil
	_ = a.Store.Put("cache", key, Obj{"online": online, "expires": time.Now().Add(10 * time.Second).Unix()})
	return online
}

func (a *App) transmissionDownload(ctx context.Context, j Obj) error {
	s := a.Settings()
	rpcURL := a.effectiveTransmissionRPCURL(s)
	user := str(s["transmissionUsername"])
	pass := str(s["transmissionPassword"])
	saveDir := str(j["directory"])
	sid := ""

	remoteHash := str(j["transmissionHash"])
	if remoteHash == "" {
		args, err := a.transmissionCall(ctx, rpcURL, user, pass, "torrent-add", Obj{
			"filename":     str(j["sourceUrl"]),
			"download-dir": saveDir,
			"paused":       false,
		}, &sid)
		if err != nil {
			if fb := jobFallbackMagnet(j); fb != "" {
				args, err = a.transmissionCall(ctx, rpcURL, user, pass, "torrent-add", Obj{
					"filename":     fb,
					"download-dir": saveDir,
					"paused":       false,
				}, &sid)
			}
		}
		if err != nil {
			return err
		}
		added := object(args["torrent-added"])
		if len(added) == 0 {
			added = object(args["torrent-duplicate"])
		}
		remoteHash = strings.ToLower(strings.TrimSpace(str(added["hashString"])))
		if remoteHash == "" {
			return errors.New("Transmission 未返回种子 Hash")
		}
		_, _ = a.Store.Patch("downloads", str(j["gid"]), Obj{"transmissionHash": remoteHash, "infoHash": remoteHash})
	} else {
		_, _ = a.transmissionCall(ctx, rpcURL, user, pass, "torrent-start", Obj{"ids": []string{remoteHash}}, &sid)
	}

	defer func() {
		if remoteHash != "" {
			stopCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			_, _ = a.transmissionCall(stopCtx, rpcURL, user, pass, "torrent-stop", Obj{"ids": []string{remoteHash}}, &sid)
		}
	}()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	fields := []string{"id", "hashString", "totalSize", "haveValid", "leftUntilDone", "percentDone", "rateDownload", "rateUpload", "status", "error", "errorString", "downloadDir", "files", "fileStats"}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		args, err := a.transmissionCall(ctx, rpcURL, user, pass, "torrent-get", Obj{
			"ids":    []string{remoteHash},
			"fields": fields,
		}, &sid)
		if err != nil {
			return err
		}
		torrents := list(args["torrents"])
		if len(torrents) == 0 {
			return errors.New("任务已在 Transmission 中被移除")
		}
		item := object(torrents[0])
		if num(item["error"]) > 0 && strings.TrimSpace(str(item["errorString"])) != "" {
			return errors.New("Transmission：" + str(item["errorString"]))
		}
		total := num(item["totalSize"])
		left := num(item["leftUntilDone"])
		completed := total - left
		if completed < 0 {
			completed = num(item["haveValid"])
		}
		_, _ = a.Store.Patch("downloads", str(j["gid"]), Obj{
			"totalLength":     total,
			"completedLength": completed,
			"downloadSpeed":   num(item["rateDownload"]),
			"uploadSpeed":     num(item["rateUpload"]),
		})
		a.notifyDownloads()

		if total > 0 && left == 0 {
			rawFiles := list(item["files"])
			fileStats := list(item["fileStats"])
			dlDir := str(item["downloadDir"])
			if dlDir == "" {
				dlDir = saveDir
			}
			out := make([]Obj, 0, len(rawFiles))
			for idx, rf := range rawFiles {
				if idx < len(fileStats) && !flag(object(fileStats[idx])["wanted"]) {
					continue
				}
				fObj := object(rf)
				p := filepath.Join(dlDir, filepath.FromSlash(str(fObj["name"])))
				if !inside(saveDir, p) {
					return errors.New("Transmission 保存路径与本机下载目录不匹配")
				}
				out = append(out, Obj{"path": p, "size": num(fObj["length"])})
			}
			if len(out) == 0 {
				return errors.New("Transmission 任务完成但未返回有效文件")
			}
			_, err = a.Store.Patch("downloads", str(j["gid"]), Obj{"files": out})
			return err
		}
	}
}

func (a *App) downloadClientOnline(client string) bool {
	switch client {
	case "qbittorrent":
		return a.qbitOnline()
	case "aria2-rpc":
		return a.aria2RPCOnline()
	case "transmission":
		return a.transmissionOnline()
	default:
		return a.engine.online()
	}
}

func (a *App) executeDownloadJob(ctx context.Context, j Obj) error {
	switch str(a.Settings()["downloadClient"]) {
	case "qbittorrent":
		return a.qbitDownload(ctx, j)
	case "aria2-rpc":
		return a.aria2RPCDownload(ctx, j)
	case "transmission":
		return a.transmissionDownload(ctx, j)
	default:
		return a.engine.Download(ctx, j)
	}
}

func (a *App) testDownloadEngine(ctx context.Context, client string, overrides Obj) Obj {
	s := a.Settings()
	for k, v := range overrides {
		if v != nil {
			s[k] = v
		}
	}
	if client == "" {
		client = str(s["downloadClient"])
	}
	if !validDownloadClient(client) {
		client = "native"
	}
	started := time.Now()
	tCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var versionStr string
	var err error
	switch client {
	case "native":
		if !a.engine.online() {
			err = errors.New("内置下载引擎未就绪")
		} else {
			versionStr = "Pure-Go BT/HTTP"
		}
	case "qbittorrent":
		base := strings.TrimRight(strings.TrimSpace(str(s["qbittorrentBaseUrl"])), "/")
		if !httpURL(base) {
			err = errors.New("qBittorrent 地址无效")
			break
		}
		jar, _ := cookiejar.New(nil)
		hc := &http.Client{Jar: jar, Timeout: 5 * time.Second}
		form := url.Values{"username": {str(s["qbittorrentUsername"])}, "password": {str(s["qbittorrentPassword"])}}
		req, rErr := http.NewRequestWithContext(tCtx, "POST", base+"/api/v2/auth/login", strings.NewReader(form.Encode()))
		if rErr != nil {
			err = rErr
			break
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, doErr := hc.Do(req)
		if doErr != nil {
			err = doErr
			break
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		res.Body.Close()
		if res.StatusCode != 200 || strings.TrimSpace(string(b)) != "Ok." {
			err = errors.New("登录失败，请检查 Web UI 地址与账号密码")
			break
		}
		if vReq, vErr := http.NewRequestWithContext(tCtx, "GET", base+"/api/v2/app/version", nil); vErr == nil {
			if vRes, vDoErr := hc.Do(vReq); vDoErr == nil {
				vb, _ := io.ReadAll(io.LimitReader(vRes.Body, 256))
				vRes.Body.Close()
				versionStr = strings.TrimSpace(string(vb))
			}
		}
	case "aria2-rpc":
		rpcURL := a.effectiveAria2RPCURL(s)
		secret := str(s["aria2RpcSecret"])
		var res any
		res, err = a.aria2RPCCall(tCtx, rpcURL, secret, "aria2.getVersion")
		if err == nil {
			versionStr = "aria2 " + str(object(res)["version"])
		}
	case "transmission":
		rpcURL := a.effectiveTransmissionRPCURL(s)
		sid := ""
		var args Obj
		args, err = a.transmissionCall(tCtx, rpcURL, str(s["transmissionUsername"]), str(s["transmissionPassword"]), "session-get", Obj{"fields": []string{"version"}}, &sid)
		if err == nil {
			versionStr = "Transmission " + str(args["version"])
		}
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	// Bust online status cache so topbar updates immediately after test
	_ = a.Store.Delete("cache", "qbit:"+hash(str(s["qbittorrentBaseUrl"])+str(s["qbittorrentUsername"])+str(s["qbittorrentPassword"])))
	_ = a.Store.Delete("cache", "aria2rpc:"+hash(a.effectiveAria2RPCURL(s)+"|"+str(s["aria2RpcSecret"])))
	_ = a.Store.Delete("cache", "trrpc:"+hash(a.effectiveTransmissionRPCURL(s)+"|"+str(s["transmissionUsername"])+"|"+str(s["transmissionPassword"])))

	return Obj{
		"client":      client,
		"displayName": downloadClientDisplayName(client),
		"ok":          err == nil,
		"version":     versionStr,
		"error":       errMsg,
		"latencyMs":   time.Since(started).Milliseconds(),
	}
}

func (a *App) registerEngineRoutes() {
	a.handle("GET /api/downloads/engines", func(w http.ResponseWriter, r *http.Request) error {
		active := str(a.Settings()["downloadClient"])
		items := make([]Obj, 0, len(supportedDownloadClients))
		for _, id := range supportedDownloadClients {
			items = append(items, Obj{
				"id":          id,
				"displayName": downloadClientDisplayName(id),
				"active":      id == active,
				"online":      id == active && a.downloadClientOnline(id),
			})
		}
		ok(w, items)
		return nil
	})
	a.handle("POST /api/downloads/engines/test", func(w http.ResponseWriter, r *http.Request) error {
		var input Obj
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
			_ = json.NewDecoder(r.Body).Decode(&input)
		}
		client := strings.TrimSpace(str(input["client"]))
		ok(w, a.testDownloadEngine(r.Context(), client, input))
		return nil
	})
}
