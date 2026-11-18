package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) qbitDownload(ctx context.Context, j Obj) error {
	s := a.Settings()
	base := strings.TrimRight(str(s["qbittorrentBaseUrl"]), "/")
	if !httpURL(base) {
		return errors.New("qBittorrent 地址无效")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	call := func(ctx context.Context, method, path string, data url.Values) ([]byte, error) {
		var input io.Reader
		if data != nil {
			input = strings.NewReader(data.Encode())
		}
		req, e := http.NewRequestWithContext(ctx, method, base+"/api/v2/"+path, input)
		if e != nil {
			return nil, e
		}
		if input != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		res, e := client.Do(req)
		if e != nil {
			return nil, e
		}
		defer res.Body.Close()
		if res.StatusCode >= 300 {
			return nil, fmt.Errorf("qBittorrent HTTP %d", res.StatusCode)
		}
		return io.ReadAll(io.LimitReader(res.Body, 8<<20))
	}
	auth, e := call(ctx, "POST", "auth/login", url.Values{"username": {str(s["qbittorrentUsername"])}, "password": {str(s["qbittorrentPassword"])}})
	if e != nil {
		return e
	}
	if strings.TrimSpace(string(auth)) != "Ok." {
		return errors.New("qBittorrent 登录失败")
	}
	tag := "bgmcat-" + str(j["gid"])
	remoteHash := str(j["qbitHash"])
	if remoteHash == "" {
		resBody, errAdd := call(ctx, "POST", "torrents/add", url.Values{"urls": {str(j["sourceUrl"])}, "savepath": {str(j["directory"])}, "tags": {tag}, "paused": {"false"}})
		if errAdd != nil || strings.EqualFold(strings.TrimSpace(string(resBody)), "Fails.") {
			if fb := jobFallbackMagnet(j); fb != "" {
				resBody, errAdd = call(ctx, "POST", "torrents/add", url.Values{"urls": {fb}, "savepath": {str(j["directory"])}, "tags": {tag}, "paused": {"false"}})
			}
		}
		if errAdd != nil {
			return errAdd
		}
		if strings.EqualFold(strings.TrimSpace(string(resBody)), "Fails.") {
			return errors.New("qBittorrent 添加任务失败")
		}
	} else {
		if _, e = call(ctx, "POST", "torrents/start", url.Values{"hashes": {remoteHash}}); e != nil {
			_, e = call(ctx, "POST", "torrents/resume", url.Values{"hashes": {remoteHash}})
			if e != nil {
				return e
			}
		}
	}
	defer func() {
		if remoteHash != "" {
			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, e := call(stopCtx, "POST", "torrents/stop", url.Values{"hashes": {remoteHash}}); e != nil {
				_, _ = call(stopCtx, "POST", "torrents/pause", url.Values{"hashes": {remoteHash}})
			}
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
		query := "torrents/info?tag=" + url.QueryEscape(tag)
		if remoteHash != "" {
			query = "torrents/info?hashes=" + url.QueryEscape(remoteHash)
		}
		raw, e := call(ctx, "GET", query, nil)
		if e != nil {
			return e
		}
		var items []Obj
		if e = json.Unmarshal(raw, &items); e != nil {
			return e
		}
		if len(items) == 0 {
			continue
		}
		item := items[0]
		remoteHash = str(item["hash"])
		_, _ = a.Store.Patch("downloads", str(j["gid"]), Obj{"qbitHash": remoteHash, "totalLength": item["size"], "completedLength": item["completed"], "downloadSpeed": item["dlspeed"], "uploadSpeed": item["upspeed"]})
		a.notifyDownloads()
		if num(item["size"]) > 0 && num(item["completed"]) >= num(item["size"]) {
			raw, e = call(ctx, "GET", "torrents/files?hash="+url.QueryEscape(remoteHash), nil)
			if e != nil {
				return e
			}
			var files []Obj
			if e = json.Unmarshal(raw, &files); e != nil {
				return e
			}
			out := []Obj{}
			for _, f := range files {
				if num(f["priority"]) == 0 {
					continue
				}
				path := filepath.Join(str(item["save_path"]), filepath.FromSlash(str(f["name"])))
				if !inside(str(j["directory"]), path) {
					return errors.New("qBittorrent 文件路径与本机目录不匹配")
				}
				out = append(out, Obj{"path": path, "size": f["size"]})
			}
			_, e = a.Store.Patch("downloads", str(j["gid"]), Obj{"files": out})
			return e
		}
	}
}

func (a *App) qbitOnline() bool {
	s := a.Settings()
	key := "qbit:" + hash(str(s["qbittorrentBaseUrl"])+str(s["qbittorrentUsername"])+str(s["qbittorrentPassword"]))
	cached, _ := a.Store.Get("cache", key)
	if num(cached["expires"]) > time.Now().Unix() {
		return flag(cached["online"])
	}
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Second)
	defer cancel()
	data := url.Values{"username": {str(s["qbittorrentUsername"])}, "password": {str(s["qbittorrentPassword"])}}
	req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(str(s["qbittorrentBaseUrl"]), "/")+"/api/v2/auth/login", strings.NewReader(data.Encode()))
	online := false
	if e == nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, e := a.http.Do(req)
		if e == nil {
			b, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
			res.Body.Close()
			online = res.StatusCode == 200 && strings.TrimSpace(string(b)) == "Ok."
		}
	}
	_ = a.Store.Put("cache", key, Obj{"online": online, "expires": time.Now().Add(10 * time.Second).Unix()})
	return online
}
