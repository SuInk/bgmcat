package app

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (a *App) enqueue(o Obj) (Obj, error) {
	a.enqueueMu.Lock()
	defer a.enqueueMu.Unlock()
	source := str(o["url"])
	title := str(o["title"])
	fid := str(o["feedItemId"])
	var feed Obj
	if fid != "" {
		var e error
		feed, e = a.Store.Get("feed", fid)
		if e != nil {
			return nil, e
		}
		if feed == nil {
			return nil, errors.New("RSS 条目不存在")
		}
		source = str(feed["sourceUrl"])
		title = str(feed["title"])
	}
	if !httpURL(source) && !strings.HasPrefix(source, "magnet:?") {
		return nil, errors.New("请输入 HTTP、种子或磁力链接")
	}
	if title == "" {
		u, _ := url.Parse(source)
		title = filepath.Base(u.Path)
		if title == "." || title == "/" || title == "" {
			title = "Download"
		}
	}
	existing, e := a.Store.List("downloads")
	if e != nil {
		return nil, e
	}
	for _, j := range existing {
		if str(j["sourceUrl"]) == source && str(j["status"]) != "removed" {
			return j, nil
		}
	}
	gid := randomID(12)
	id, e := a.Store.NextID()
	if e != nil {
		return nil, e
	}
	kind := str(o["sourceType"])
	if feed != nil {
		kind = str(feed["sourceType"])
	}
	if kind == "" {
		kind = inferSource(source)
	}
	dir := str(a.Settings()["downloadDir"])
	if feed != nil && feed["subscriptionId"] != nil {
		sub, _ := a.Store.Get("subscriptions", str(feed["subscriptionId"]))
		if sub != nil && str(sub["downloadDirOverride"]) != "" {
			dir = str(sub["downloadDirOverride"])
		}
	}
	dir = filepath.Join(dir, gid)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	job := Obj{"id": id, "gid": gid, "feedItemId": o["feedItemId"], "feedItemTitle": title, "feedItemImageUrl": o["imageUrl"], "feedItemSubscriptionId": nil, "feedItemSourceUrl": source, "sourceUrl": source, "sourceType": kind, "status": "queued", "totalLength": 0, "completedLength": 0, "downloadSpeed": 0, "uploadSpeed": 0, "errorMessage": nil, "createdAt": now(), "updatedAt": now(), "directory": dir, "files": []any{}, "cloudArchive": nil}
	if feed != nil {
		job["feedItemSubscriptionId"] = feed["subscriptionId"]
		job["feedItemImageUrl"] = feed["imageUrl"]
	}
	if e = a.Store.Put("downloads", gid, job); e != nil {
		return nil, e
	}
	if fid != "" {
		_, _ = a.Store.Patch("feed", fid, Obj{"downloadStatus": "queued"})
	}
	a.startJob(gid)
	return job, nil
}
func (a *App) startJob(gid string) {
	a.jobsMu.Lock()
	if _, exists := a.jobs[gid]; exists || a.ctx.Err() != nil {
		a.jobsMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.jobs[gid] = cancel
	a.wg.Add(1)
	a.jobsMu.Unlock()
	go func() {
		defer a.wg.Done()
		defer cancel()
		defer func() { a.jobsMu.Lock(); delete(a.jobs, gid); a.jobsMu.Unlock() }()
		select {
		case a.downloadSlots <- struct{}{}:
			defer func() { <-a.downloadSlots }()
		case <-ctx.Done():
			return
		}
		job, e := a.Store.Get("downloads", gid)
		if e != nil || job == nil {
			return
		}
		_, _ = a.Store.Patch("downloads", gid, Obj{"status": "active", "errorMessage": nil, "updatedAt": now()})
		if str(a.Settings()["downloadClient"]) == "qbittorrent" {
			e = a.qbitDownload(ctx, job)
		} else if str(job["sourceType"]) == "direct" {
			e = a.httpDownload(ctx, job)
		} else {
			e = a.aria2.Download(ctx, job)
		}
		status := "complete"
		var message any
		if ctx.Err() != nil {
			status = "paused"
		} else if e != nil {
			status = "error"
			message = e.Error()
			a.Log("warn", "下载失败："+e.Error())
		}
		_, _ = a.Store.Patch("downloads", gid, Obj{"status": status, "errorMessage": message, "downloadSpeed": 0, "updatedAt": now()})
		if fid := str(job["feedItemId"]); fid != "" {
			fs := "queued"
			if status == "complete" || status == "error" {
				fs = status
			}
			_, _ = a.Store.Patch("feed", fid, Obj{"downloadStatus": fs})
		}
		if status == "complete" {
			a.Log("info", "下载完成："+str(job["feedItemTitle"]))
		}
	}()
}
func (a *App) stopJob(gid string) error {
	a.jobsMu.Lock()
	cancel := a.jobs[gid]
	a.jobsMu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	for i := 0; i < 100; i++ {
		a.jobsMu.Lock()
		_, busy := a.jobs[gid]
		a.jobsMu.Unlock()
		if !busy {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("任务正在停止，请稍后重试")
}
func (a *App) resumeJobs() {
	jobs, _ := a.Store.List("downloads")
	for _, j := range jobs {
		switch str(j["status"]) {
		case "active", "queued", "waiting":
			a.startJob(str(j["gid"]))
		}
	}
}
func safeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"|?*`, r) {
			return '_'
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." {
		return "download.bin"
	}
	if len(name) > 180 {
		name = name[len(name)-180:]
	}
	return name
}
func inside(root, path string) bool {
	r, e := filepath.Rel(root, path)
	return e == nil && r != "." && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}
func (a *App) downloadRoutes() {
	create := func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		j, e := a.enqueue(o)
		if e == nil {
			ok(w, publicDownloads(j))
		}
		return e
	}
	a.handle("POST /api/downloads", create)
	a.handle("POST /api/discovery/downloads", create)
	a.handle("GET /api/downloads", func(w http.ResponseWriter, r *http.Request) error {
		j, e := a.Store.List("downloads")
		if e == nil {
			ok(w, publicDownloads(j))
		}
		return e
	})
	a.handle("POST /api/downloads/{gid}/pause", func(w http.ResponseWriter, r *http.Request) error {
		gid := r.PathValue("gid")
		current, err := a.Store.Get("downloads", gid)
		if err != nil {
			return err
		}
		if current == nil || str(current["status"]) == "complete" {
			return errors.New("任务不存在或已完成")
		}
		if e := a.stopJob(gid); e != nil {
			return e
		}
		j, e := a.Store.Patch("downloads", gid, Obj{"status": "paused"})
		if e == nil {
			ok(w, publicDownloads(j))
		}
		return e
	})
	// 边下边播：任务改成顺序下载并记在任务上，暂停后再继续也照样按顺序下
	a.handle("POST /api/downloads/{gid}/sequential", func(w http.ResponseWriter, r *http.Request) error {
		gid := r.PathValue("gid")
		current, err := a.Store.Get("downloads", gid)
		if err != nil {
			return err
		}
		if current == nil {
			return errors.New("任务不存在")
		}
		a.watch(gid)
		if str(current["status"]) == "complete" || flag(current["sequential"]) {
			ok(w, publicDownloads(current))
			return nil
		}
		// 下载循环每两秒看一次这个标记，自己去挪 aria2 的优先范围
		j, err := a.Store.Patch("downloads", gid, Obj{"sequential": true})
		if err != nil {
			return err
		}
		ok(w, publicDownloads(j))
		return nil
	})
	a.handle("POST /api/downloads/{gid}/resume", func(w http.ResponseWriter, r *http.Request) error {
		gid := r.PathValue("gid")
		j, e := a.Store.Get("downloads", gid)
		if e != nil {
			return e
		}
		if j == nil {
			return errors.New("任务不存在")
		}
		if str(j["status"]) == "complete" {
			return errors.New("已完成任务无需继续")
		}
		if e = a.stopJob(gid); e != nil {
			return e
		}
		a.startJob(gid)
		ok(w, publicDownloads(j))
		return nil
	})
	a.handle("DELETE /api/downloads/{gid}", func(w http.ResponseWriter, r *http.Request) error {
		a.driveMu.Lock()
		defer a.driveMu.Unlock()
		gid := r.PathValue("gid")
		j, e := a.Store.Get("downloads", gid)
		if e != nil {
			return e
		}
		if j == nil {
			return errors.New("任务不存在")
		}
		o, _ := body(w, r)
		if e = a.stopJob(gid); e != nil {
			return e
		}
		count := 0
		if flag(o["deleteLocalFiles"]) {
			for _, item := range list(j["files"]) {
				p := str(object(item)["path"])
				if inside(str(j["directory"]), p) {
					if e = os.Remove(p); e == nil {
						count++
					} else if !os.IsNotExist(e) {
						return e
					}
				}
			}
		}
		if fid := str(j["feedItemId"]); fid != "" {
			_, _ = a.Store.Patch("feed", fid, Obj{"downloadStatus": "ignored"})
		}
		e = a.Store.Delete("downloads", gid)
		if e == nil {
			ok(w, Obj{"deleted": true, "deletedLocalFileCount": count})
		}
		return e
	})
	a.handle("POST /api/downloads/clear-finished", func(w http.ResponseWriter, r *http.Request) error {
		a.driveMu.Lock()
		defer a.driveMu.Unlock()
		jobs, e := a.Store.List("downloads")
		if e != nil {
			return e
		}
		ids := []string{}
		for _, j := range jobs {
			status := str(j["status"])
			if (status == "complete" || status == "error" || status == "removed") && j["cloudArchive"] == nil && !flag(a.Settings()["cloudArchiveEnabled"]) {
				if e = a.Store.Delete("downloads", str(j["gid"])); e != nil {
					return e
				}
				ids = append(ids, str(j["gid"]))
			}
		}
		ok(w, Obj{"removedCount": len(ids), "gids": ids})
		return nil
	})
	a.handle("POST /api/downloads/{gid}/resolve-conflict", func(w http.ResponseWriter, r *http.Request) error {
		return errors.New("Go 下载器为每个任务使用独立目录；请删除失败任务后重新添加")
	})
	a.media.Routes()
}
func mimeFor(path string) string {
	m := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if m == "" {
		return "application/octet-stream"
	}
	return m
}
func parseInt(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }

// Upload session URLs are bearer credentials, never include them in API output.
func publicDownloads(v any) any {
	switch x := v.(type) {
	case []Obj:
		out := make([]Obj, 0, len(x))
		for _, j := range x {
			out = append(out, publicDownloads(j).(Obj))
		}
		return out
	case Obj:
		out := clone(x)
		for _, item := range list(out["files"]) {
			delete(object(item), "session")
		}
		return out
	}
	return v
}

// watch 记下这个任务刚有人在播，边下边播的下载循环据此判断还要不要按播放顺序挪优先范围。
func (a *App) watch(gid string) { a.watching.Store(gid, time.Now()) }

// watchedWithin 是这个任务最近 d 时间内有没有人在播。
func (a *App) watchedWithin(gid string, d time.Duration) bool {
	at, ok := a.watching.Load(gid)
	return ok && time.Since(at.(time.Time)) < d
}

// watched 包一层播放相关的接口，请求进来就记一次有人在看。
func (a *App) watched(fn func(http.ResponseWriter, *http.Request) error) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a.watch(r.PathValue("gid"))
		return fn(w, r)
	}
}
