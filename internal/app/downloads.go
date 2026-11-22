package app

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	kind := str(o["sourceType"])
	if feed != nil {
		kind = str(feed["sourceType"])
	}
	if kind == "" {
		kind = inferSource(source)
	}
	fallback := strings.TrimSpace(str(o["fallbackUrl"]))
	if fallback == "" && feed != nil {
		fallback = strings.TrimSpace(str(feed["fallbackUrl"]))
	}
	if fallback != "" && !httpURL(fallback) && !strings.HasPrefix(fallback, "magnet:?") {
		fallback = ""
	}
	incomingHash := extractSourceInfoHash(source, fallback, str(feed["infoHash"]), str(feed["link"]))
	if fallback == "" && !strings.HasPrefix(source, "magnet:?") && incomingHash != "" && (kind == "torrent-url" || inferSource(source) == "torrent-url") {
		fallback = buildMagnetFromInfoHash(incomingHash, title)
	}
	for _, j := range existing {
		if str(j["status"]) == "removed" {
			continue
		}
		existingSource := str(j["sourceUrl"])
		sameSource := existingSource == source || (fallback != "" && existingSource == fallback)
		sameHash := incomingHash != "" && (strings.EqualFold(str(j["infoHash"]), incomingHash) || extractSourceInfoHash(existingSource, str(j["fallbackUrl"])) == incomingHash)
		if sameSource || sameHash {
			if str(o["saveTo"]) == "cloud" && str(j["saveTo"]) != "cloud" {
				if saveTo, err := a.resolveSaveTo("cloud"); err == nil {
					if updated, pErr := a.Store.Patch("downloads", str(j["gid"]), Obj{"saveTo": saveTo, "updatedAt": now()}); pErr == nil {
						j = updated
						if str(j["status"]) == "complete" {
							a.dispatchArchiveJob(str(j["gid"]))
						}
						a.notifyDownloads()
					}
				}
			}
			return j, nil
		}
	}
	gid := randomID(12)
	id, e := a.Store.NextID()
	if e != nil {
		return nil, e
	}
	saveTo, err := a.resolveSaveTo(str(o["saveTo"]))
	if err != nil {
		return nil, err
	}
	dir := a.downloadsDir()
	subTitle := ""
	animeID := strings.TrimSpace(str(o["animeId"]))
	if feed != nil && feed["subscriptionId"] != nil {
		sub, _ := a.Store.Get("subscriptions", str(feed["subscriptionId"]))
		if sub != nil {
			subTitle = strings.TrimSpace(str(sub["name"]))
			if idx := strings.LastIndex(subTitle, " · "); idx > 0 {
				subTitle = strings.TrimSpace(subTitle[:idx])
			}
			if animeID == "" {
				animeID = strings.TrimSpace(str(sub["animeId"]))
			}
			if str(sub["downloadDirOverride"]) != "" {
				dir = str(sub["downloadDirOverride"])
			}
		}
	}
	var favTitle, favCover string
	if animeID != "" {
		if fav, _ := a.Store.Get("favorites", animeID); fav != nil {
			favTitle = strings.TrimSpace(str(fav["title"]))
			favCover = strings.TrimSpace(str(fav["imageUrl"]))
		}
	}
	if custom := strings.TrimSpace(str(o["directory"])); custom != "" {
		if !filepath.IsAbs(custom) {
			return nil, errors.New("保存目录必须是绝对路径")
		}
		dir = custom
	}
	animeTitle := firstNonEmpty(str(o["animeTitle"]), favTitle, subTitle)
	folderMode := normalizeDownloadFolderMode(str(a.Settings()["downloadFolderMode"]))
	if folderMode != "flat" {
		subfolders := buildDownloadSubfolders(folderMode, animeTitle, title)
		if len(subfolders) == 0 {
			subfolders = []string{gid}
		}
		dir = filepath.Join(append([]string{dir}, subfolders...)...)
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	coverURL := firstNonEmpty(str(o["imageUrl"]), str(feed["imageUrl"]), favCover)
	var imageVal any
	if coverURL != "" {
		imageVal = coverURL
	}
	job := Obj{"id": id, "gid": gid, "feedItemId": o["feedItemId"], "feedItemTitle": title, "feedItemImageUrl": imageVal, "feedItemSubscriptionId": nil, "feedItemSourceUrl": source, "sourceUrl": source, "sourceType": kind, "status": "queued", "totalLength": 0, "completedLength": 0, "downloadSpeed": 0, "uploadSpeed": 0, "errorMessage": nil, "createdAt": now(), "updatedAt": now(), "directory": dir, "files": []any{}, "cloudArchive": nil, "saveTo": saveTo}
	if animeID != "" {
		job["animeId"] = animeID
		job["feedItemGuid"] = "anime:" + animeID
	}
	if animeTitle != "" {
		job["animeTitle"] = animeTitle
	}
	if incomingHash != "" {
		job["infoHash"] = incomingHash
	}
	if fallback != "" && fallback != source {
		job["fallbackUrl"] = fallback
	}
	if feed != nil {
		job["feedItemSubscriptionId"] = feed["subscriptionId"]
		if imageVal == nil && feed["imageUrl"] != nil {
			job["feedItemImageUrl"] = feed["imageUrl"]
		}
	}
	if e = a.Store.Put("downloads", gid, job); e != nil {
		return nil, e
	}
	if fid != "" {
		_, _ = a.Store.Patch("feed", fid, Obj{"downloadStatus": "queued"})
	}
	a.notifyDownloads()
	a.startJob(gid)
	return job, nil
}

// resolveSaveTo picks where a new task ends up. An explicit request for Google
// Drive fails fast when Drive is not connected; the default setting (used by
// subscriptions and one-click downloads) quietly falls back to local instead,
// so automatic downloads never stop because of a missing authorization.
func (a *App) resolveSaveTo(requested string) (string, error) {
	switch requested {
	case "local":
		return "local", nil
	case "cloud":
		if !a.activeCloudConnected() {
			return "", fmt.Errorf("请先在「设置 → 下载」连接 %s", cloudProviderDisplayName(a.activeCloudProvider()))
		}
		return "cloud", nil
	case "":
		if str(a.Settings()["defaultSaveTo"]) == "cloud" && a.activeCloudConnected() {
			return "cloud", nil
		}
		return "local", nil
	}
	return "", errors.New("保存位置只能是本机或云盘")
}

func downloadJobEpisodeSortKey(j Obj) (hasEp bool, ep int, createdAt int64, gid string) {
	title := firstNonEmpty(str(j["feedItemTitle"]), str(j["title"]))
	if title == "" {
		if files := list(j["files"]); len(files) > 0 {
			title = filepath.Base(str(object(files[0])["path"]))
		}
	}
	if title == "" {
		title = str(j["sourceUrl"])
	}
	if raw := episode(title); raw != nil {
		if n := int(num(raw)); n >= 0 {
			hasEp = true
			ep = n
		}
	}
	return hasEp, ep, num(j["createdAt"]), str(j["gid"])
}

func sortJobsByEpisodeOrder(jobs []Obj) {
	sort.SliceStable(jobs, func(i, j int) bool {
		ha, ea, ca, ga := downloadJobEpisodeSortKey(jobs[i])
		hb, eb, cb, gb := downloadJobEpisodeSortKey(jobs[j])
		if ha != hb {
			return ha
		}
		if ha && ea != eb {
			return ea < eb
		}
		if ca != cb {
			return ca < cb
		}
		return ga < gb
	})
}

func (a *App) startJob(gid string) {
	a.scheduleDownloads()
}

// scheduleDownloads fills available downloadSlots with queued/waiting tasks in
// ascending episode order (Ep 1 -> Ep 2 -> ...), so multi-episode batches and
// restarts always download earlier episodes first.
func (a *App) scheduleDownloads() {
	if a.ctx.Err() != nil {
		return
	}
	jobs, err := a.Store.List("downloads")
	if err != nil {
		return
	}
	candidates := make([]Obj, 0, len(jobs))
	for _, j := range jobs {
		switch str(j["status"]) {
		case "active", "queued", "waiting":
			candidates = append(candidates, j)
		}
	}
	if len(candidates) == 0 {
		return
	}
	sortJobsByEpisodeOrder(candidates)
	for _, j := range candidates {
		if a.ctx.Err() != nil {
			return
		}
		gid := str(j["gid"])
		if gid == "" {
			continue
		}
		a.jobsMu.Lock()
		if _, exists := a.jobs[gid]; exists || a.ctx.Err() != nil {
			a.jobsMu.Unlock()
			continue
		}
		select {
		case a.downloadSlots <- struct{}{}:
		default:
			a.jobsMu.Unlock()
			return
		}
		ctx, cancel := context.WithCancel(a.ctx)
		a.jobs[gid] = cancel
		a.wg.Add(1)
		a.jobsMu.Unlock()
		go a.runScheduledDownload(gid, ctx, cancel)
	}
}

func (a *App) runScheduledDownload(gid string, ctx context.Context, cancel context.CancelFunc) {
	defer a.wg.Done()
	defer cancel()
	defer func() {
		a.jobsMu.Lock()
		delete(a.jobs, gid)
		a.jobsMu.Unlock()
		<-a.downloadSlots
		if a.ctx.Err() == nil {
			a.scheduleDownloads()
		}
	}()
	job, e := a.Store.Get("downloads", gid)
	if e != nil || job == nil {
		return
	}
	_, _ = a.Store.Patch("downloads", gid, Obj{"status": "active", "errorMessage": nil, "updatedAt": now()})
	a.notifyDownloads()
	e = a.executeDownloadJob(ctx, job)
	status := "complete"
	var message any
	if a.ctx.Err() != nil {
		// The app is shutting down, not the user pausing: leave the task queued
		// so resumeJobs picks it up on the next start.
		status = "queued"
	} else if ctx.Err() != nil {
		status = "paused"
	} else if e != nil {
		status = "error"
		message = e.Error()
		a.Log("warn", "下载失败："+e.Error())
	}
	_, _ = a.Store.Patch("downloads", gid, Obj{"status": status, "errorMessage": message, "downloadSpeed": 0, "uploadSpeed": 0, "updatedAt": now()})
	if fid := str(job["feedItemId"]); fid != "" {
		fs := "queued"
		if status == "complete" || status == "error" {
			fs = status
		}
		_, _ = a.Store.Patch("feed", fid, Obj{"downloadStatus": fs})
	}
	a.notifyDownloads()
	if status == "complete" {
		a.Log("info", "下载完成："+str(job["feedItemTitle"]))
		a.autoCancelRedundantDuplicateEpisodes(gid, job)
		if latest, getErr := a.Store.Get("downloads", gid); getErr == nil && latest != nil && a.archiveWanted(latest) {
			a.dispatchArchiveJob(gid)
		} else {
			a.triggerArchive()
		}
	}
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
		if str(j["status"]) == "complete" {
			a.autoCancelRedundantDuplicateEpisodes(str(j["gid"]), j)
		}
	}
	a.scheduleDownloads()
}

func (a *App) autoCancelRedundantDuplicateEpisodes(completedGid string, completedJob Obj) {
	if completedJob == nil {
		return
	}
	epRaw := episode(firstNonEmpty(str(completedJob["feedItemTitle"]), str(completedJob["title"])))
	epNum := int(num(epRaw))
	if epNum <= 0 {
		return
	}
	animeID := strings.TrimSpace(str(completedJob["animeId"]))
	animeTitle := strings.TrimSpace(str(completedJob["animeTitle"]))
	dir := filepath.Clean(strings.TrimSpace(str(completedJob["directory"])))
	if animeID == "" && animeTitle == "" && (dir == "" || dir == ".") {
		return
	}

	jobs, err := a.Store.List("downloads")
	if err != nil {
		return
	}
	changed := false
	for _, j := range jobs {
		gid := str(j["gid"])
		if gid == "" || gid == completedGid {
			continue
		}
		st := str(j["status"])
		if st != "active" && st != "queued" && st != "waiting" {
			continue
		}
		if num(j["completedLength"]) > 0 || num(j["downloadSpeed"]) > 0 {
			continue
		}
		sameSeries := (animeID != "" && strings.TrimSpace(str(j["animeId"])) == animeID) ||
			(animeTitle != "" && strings.EqualFold(strings.TrimSpace(str(j["animeTitle"])), animeTitle)) ||
			(dir != "" && dir != "." && filepath.Clean(strings.TrimSpace(str(j["directory"]))) == dir)
		if !sameSeries {
			continue
		}
		otherEp := int(num(episode(firstNonEmpty(str(j["feedItemTitle"]), str(j["title"])))))
		if otherEp != epNum {
			continue
		}
		_ = a.stopJob(gid)
		_, _ = a.Store.Patch("downloads", gid, Obj{
			"status":        "paused",
			"downloadSpeed": 0,
			"uploadSpeed":   0,
			"updatedAt":     now(),
		})
		a.Log("info", fmt.Sprintf("同集另一版本已完成，自动暂停 0%% 死种任务：%s", str(j["feedItemTitle"])))
		changed = true
	}
	if changed {
		a.notifyDownloads()
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
	if name == "" || name == "." || name == ".." || name == "/" || name == `\` {
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

var (
	infoHashTorrentRE    = regexp.MustCompile(`^[0-9a-f]{40}\.torrent$`)
	urlInfoHashTorrentRE = regexp.MustCompile(`(?i)(?:^|/|hash=)([0-9a-f]{40})(?:\.torrent|[/?#&]|$)`)
)

func extractSourceInfoHash(sources ...string) string {
	for _, raw := range sources {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if plainHex40RE.MatchString(raw) {
			return strings.ToLower(raw)
		}
		if strings.HasPrefix(strings.ToLower(raw), "magnet:?") {
			if hash, _, err := magnetTrackers(raw); err == nil {
				return strings.ToLower(hash.HexString())
			}
			if m := btihHexRE.FindStringSubmatch(raw); len(m) > 1 {
				return strings.ToLower(m[1])
			}
			if m := btihBase32RE.FindStringSubmatch(raw); len(m) > 1 {
				if decoded, err := base32.StdEncoding.DecodeString(strings.ToUpper(m[1])); err == nil && len(decoded) == 20 {
					return hex.EncodeToString(decoded)
				}
			}
			continue
		}
		if m := urlInfoHashTorrentRE.FindStringSubmatch(raw); len(m) == 2 {
			return strings.ToLower(m[1])
		}
	}
	return ""
}

func buildMagnetFromInfoHash(infoHash, title string) string {
	infoHash = strings.ToLower(strings.TrimSpace(infoHash))
	if !plainHex40RE.MatchString(infoHash) {
		return ""
	}
	var b strings.Builder
	b.WriteString("magnet:?xt=urn:btih:")
	b.WriteString(infoHash)
	if t := strings.TrimSpace(title); t != "" {
		b.WriteString("&dn=")
		b.WriteString(url.QueryEscape(t))
	}
	limit := min(8, len(builtinBTTrackers))
	for _, tr := range builtinBTTrackers[:limit] {
		b.WriteString("&tr=")
		b.WriteString(url.QueryEscape(tr))
	}
	return b.String()
}

func jobFallbackMagnet(j Obj) string {
	if j == nil {
		return ""
	}
	fb := strings.TrimSpace(str(j["fallbackUrl"]))
	if strings.HasPrefix(fb, "magnet:?") {
		return fb
	}
	src := strings.TrimSpace(str(j["sourceUrl"]))
	kind := strings.TrimSpace(str(j["sourceType"]))
	if kind == "torrent-url" || inferSource(src) == "torrent-url" {
		if ih := extractSourceInfoHash(src, str(j["infoHash"])); ih != "" {
			return buildMagnetFromInfoHash(ih, firstNonEmpty(str(j["feedItemTitle"]), str(j["title"])))
		}
	}
	return ""
}

func (a *App) hasOtherActiveInfoHash(selfGid, infoHash string) bool {
	infoHash = strings.ToLower(strings.TrimSpace(infoHash))
	if infoHash == "" {
		return false
	}
	jobs, err := a.Store.List("downloads")
	if err != nil {
		return false
	}
	for _, j := range jobs {
		gid := str(j["gid"])
		if gid == selfGid || str(j["status"]) != "active" {
			continue
		}
		h := strings.ToLower(str(j["infoHash"]))
		if h == "" {
			h = extractSourceInfoHash(str(j["sourceUrl"]), str(j["fallbackUrl"]))
		}
		if h == infoHash {
			return true
		}
	}
	return false
}

// removeAria2Leftovers clears what aria2 leaves beside a deleted task's files: the
// metadata it saves as <infohash>.torrent (bt-save-metadata) and stray .aria2
// control files. The per-task folder (named after the gid) goes too, but only once
// it is empty, so nothing the user put there is ever touched.
func removeAria2Leftovers(dir, gid string) {
	if dir == "" || filepath.Base(dir) != gid {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && (infoHashTorrentRE.MatchString(name) ||
			name == ".torrent.bolt.db" ||
			strings.HasSuffix(name, ".aria2") ||
			strings.HasSuffix(name, ".part") ||
			strings.HasSuffix(name, httpStateSuffix) ||
			strings.HasSuffix(name, httpStateSuffix+".tmp")) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
	_ = os.Remove(dir)
}

func (a *App) removeEmptyTaskDir(dir string) {
	cleanDir := filepath.Clean(strings.TrimSpace(dir))
	if cleanDir == "" || cleanDir == "." || cleanDir == string(filepath.Separator) {
		return
	}
	dlRoot := filepath.Clean(a.downloadsDir())
	if cleanDir == dlRoot || cleanDir == filepath.Clean(a.cfg.DataDir) {
		return
	}
	_ = os.Remove(cleanDir)
}

func isTaskScratchArtifact(name string) bool {
	return name == ".DS_Store" ||
		name == "Thumbs.db" ||
		name == ".torrent.bolt.db" ||
		infoHashTorrentRE.MatchString(name) ||
		strings.HasSuffix(name, ".aria2") ||
		strings.HasSuffix(name, ".part") ||
		strings.HasSuffix(name, httpStateSuffix) ||
		strings.HasSuffix(name, httpStateSuffix+".tmp")
}

func (a *App) hasOtherActiveOrPendingJobInDir(selfGid, dir string) bool {
	target := filepath.Clean(strings.TrimSpace(dir))
	if target == "" || target == "." {
		return false
	}
	jobs, err := a.Store.List("downloads")
	if err != nil {
		return false
	}
	for _, j := range jobs {
		gid := str(j["gid"])
		if gid == selfGid || str(j["status"]) == "removed" {
			continue
		}
		jDir := filepath.Clean(strings.TrimSpace(str(j["directory"])))
		if jDir != target {
			continue
		}
		if str(j["status"]) != "complete" {
			return true
		}
		for _, raw := range list(j["files"]) {
			p := str(object(raw)["path"])
			if p != "" {
				if _, sErr := os.Stat(p); sErr == nil {
					return true
				}
			}
		}
	}
	return false
}

func (a *App) cleanupArchivedLocalStaging(gid, taskDir string, removedPaths []string) {
	if gid != "" && a.engine != nil {
		a.engine.StopSeeding(gid)
	}
	if gid != "" {
		_ = os.RemoveAll(a.btTaskStateDir(gid))
	}
	for _, p := range removedPaths {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "" || p == "." {
			continue
		}
		for _, suffix := range []string{".aria2", ".part", httpStateSuffix, httpStateSuffix + ".tmp"} {
			_ = os.Remove(p + suffix)
		}
	}

	cleanTaskDir := filepath.Clean(strings.TrimSpace(taskDir))
	dlRoot := filepath.Clean(a.downloadsDir())
	dataRoot := filepath.Clean(a.cfg.DataDir)
	allowedDir := func(d string) bool {
		if d == "" || d == "." || d == string(filepath.Separator) || d == dlRoot || d == dataRoot {
			return false
		}
		return inside(dlRoot, d) || (cleanTaskDir != "" && cleanTaskDir != "." && (d == cleanTaskDir || inside(cleanTaskDir, d)))
	}

	candSet := map[string]bool{}
	addAncestors := func(start string) {
		cur := filepath.Clean(strings.TrimSpace(start))
		for allowedDir(cur) {
			candSet[cur] = true
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}
	if allowedDir(cleanTaskDir) {
		addAncestors(cleanTaskDir)
	}
	for _, p := range removedPaths {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "" || p == "." {
			continue
		}
		addAncestors(filepath.Dir(p))
	}

	dirs := make([]string, 0, len(candSet))
	for d := range candSet {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool {
		di := strings.Count(dirs[i], string(filepath.Separator))
		dj := strings.Count(dirs[j], string(filepath.Separator))
		if di != dj {
			return di > dj
		}
		return len(dirs[i]) > len(dirs[j])
	})

	for _, d := range dirs {
		if a.hasOtherActiveOrPendingJobInDir(gid, d) {
			continue
		}
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		onlyScratch := true
		for _, entry := range entries {
			if entry.IsDir() || !isTaskScratchArtifact(entry.Name()) {
				onlyScratch = false
				break
			}
		}
		if onlyScratch {
			for _, entry := range entries {
				_ = os.Remove(filepath.Join(d, entry.Name()))
			}
		}
		_ = os.Remove(d)
	}

	removeAria2Leftovers(taskDir, gid)
	a.removeEmptyTaskDir(taskDir)
}

func (a *App) notifyDownloads() {
	a.dlSubMu.Lock()
	for ch := range a.dlSubs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	a.dlSubMu.Unlock()
}

func (a *App) downloadEvents(w http.ResponseWriter, r *http.Request) error {
	flusher, okFlush := w.(http.Flusher)
	if !okFlush {
		return errors.New("当前连接不支持实时事件流")
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan struct{}, 1)
	a.dlSubMu.Lock()
	a.dlSubs[ch] = struct{}{}
	a.dlSubMu.Unlock()
	defer func() {
		a.dlSubMu.Lock()
		delete(a.dlSubs, ch)
		a.dlSubMu.Unlock()
	}()

	sendSnapshot := func() error {
		jobs, err := a.Store.List("downloads")
		if err != nil {
			return err
		}
		payload, err := json.Marshal(a.publicDownloads(jobs))
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(w, "event: downloads\ndata: %s\n\n", payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := sendSnapshot(); err != nil {
		return nil
	}

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	var coalesce *time.Timer
	var coalesceC <-chan time.Time
	lastSent := time.Now()
	const minInterval = 220 * time.Millisecond

	for {
		select {
		case <-r.Context().Done():
			if coalesce != nil {
				coalesce.Stop()
			}
			return nil
		case <-a.ctx.Done():
			if coalesce != nil {
				coalesce.Stop()
			}
			return nil
		case <-ch:
			elapsed := time.Since(lastSent)
			if elapsed >= minInterval {
				if coalesce != nil {
					coalesce.Stop()
					coalesceC = nil
				}
				lastSent = time.Now()
				if sendSnapshot() != nil {
					return nil
				}
			} else if coalesceC == nil {
				wait := minInterval - elapsed
				if coalesce == nil {
					coalesce = time.NewTimer(wait)
				} else {
					coalesce.Reset(wait)
				}
				coalesceC = coalesce.C
			}
		case <-coalesceC:
			coalesceC = nil
			lastSent = time.Now()
			if sendSnapshot() != nil {
				return nil
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return nil
			}
			flusher.Flush()
		}
	}
}

func (a *App) downloadRoutes() {
	create := func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		j, e := a.enqueue(o)
		if e == nil {
			ok(w, a.publicDownloads(j))
		}
		return e
	}
	a.handle("POST /api/downloads", create)
	a.handle("POST /api/discovery/downloads", create)
	a.handle("POST /api/downloads/batch", func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		rawItems := list(o["items"])
		if len(rawItems) == 0 {
			return errors.New("请至少提供一条下载链接")
		}
		if len(rawItems) > 200 {
			return errors.New("单次批量添加最多支持 200 个任务")
		}
		defaultSaveTo := str(o["saveTo"])
		defaultDir := str(o["directory"])
		defaultAnimeTitle := str(o["animeTitle"])
		defaultAnimeId := str(o["animeId"])
		existingRows, _ := a.Store.List("downloads")
		prevSaveTo := make(map[string]string, len(existingRows)+len(rawItems))
		for _, row := range existingRows {
			prevSaveTo[str(row["gid"])] = str(row["saveTo"])
		}
		type batchEntry struct {
			idx  int
			item Obj
		}
		ordered := make([]batchEntry, 0, len(rawItems))
		for idx, raw := range rawItems {
			ordered = append(ordered, batchEntry{idx: idx, item: clone(object(raw))})
		}
		sort.SliceStable(ordered, func(i, j int) bool {
			ha, ea, _, _ := downloadJobEpisodeSortKey(ordered[i].item)
			hb, eb, _, _ := downloadJobEpisodeSortKey(ordered[j].item)
			if ha != hb {
				return ha
			}
			if ha && ea != eb {
				return ea < eb
			}
			return ordered[i].idx < ordered[j].idx
		})

		items := make([]Obj, 0, len(ordered))
		skippedCount := 0
		itemErrors := []Obj{}
		var firstErr error
		for _, entry := range ordered {
			idx := entry.idx
			item := entry.item
			if str(item["saveTo"]) == "" && defaultSaveTo != "" {
				item["saveTo"] = defaultSaveTo
			}
			if str(item["directory"]) == "" && defaultDir != "" {
				item["directory"] = defaultDir
			}
			if str(item["animeTitle"]) == "" && defaultAnimeTitle != "" {
				item["animeTitle"] = defaultAnimeTitle
			}
			if str(item["animeId"]) == "" && defaultAnimeId != "" {
				item["animeId"] = defaultAnimeId
			}
			j, err := a.enqueue(item)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				itemErrors = append(itemErrors, Obj{"index": idx, "url": str(item["url"]), "message": err.Error()})
				continue
			}
			gid := str(j["gid"])
			if prev, existed := prevSaveTo[gid]; existed && prev == str(j["saveTo"]) {
				skippedCount++
				continue
			}
			prevSaveTo[gid] = str(j["saveTo"])
			items = append(items, a.publicDownloads(j).(Obj))
		}
		if len(items) == 0 && skippedCount == 0 && firstErr != nil {
			return firstErr
		}
		ok(w, Obj{
			"items":        items,
			"queuedCount":  len(items),
			"createdCount": len(items),
			"skippedCount": skippedCount,
			"failedCount":  len(itemErrors),
			"errorCount":   len(itemErrors),
			"errors":       itemErrors,
		})
		return nil
	})
	a.handle("GET /api/downloads/events", a.downloadEvents)
	a.handle("GET /api/downloads", func(w http.ResponseWriter, r *http.Request) error {
		j, e := a.Store.List("downloads")
		if e == nil {
			ok(w, a.publicDownloads(j))
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
		j, e := a.Store.Patch("downloads", gid, Obj{"status": "paused", "downloadSpeed": 0, "uploadSpeed": 0, "updatedAt": now()})
		if e == nil {
			a.notifyDownloads()
			ok(w, a.publicDownloads(j))
		}
		return e
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
		if a.engine != nil {
			a.engine.StopSeeding(gid)
		}
		j, _ = a.Store.Patch("downloads", gid, Obj{"status": "queued", "errorMessage": nil, "updatedAt": now()})
		a.notifyDownloads()
		a.startJob(gid)
		ok(w, a.publicDownloads(j))
		return nil
	})
	a.handle("POST /api/downloads/{gid}/archive", func(w http.ResponseWriter, r *http.Request) error {
		if !a.activeCloudConnected() {
			return fmt.Errorf("请先在「设置 → 下载」连接 %s", cloudProviderDisplayName(a.activeCloudProvider()))
		}
		gid := r.PathValue("gid")
		j, e := a.Store.Get("downloads", gid)
		if e != nil {
			return e
		}
		if j == nil {
			return errors.New("任务不存在")
		}
		archive := object(j["cloudArchive"])
		archiveStatus := str(archive["status"])
		if archiveStatus == "archived" {
			ok(w, a.publicDownloads(j))
			return nil
		}
		patch := Obj{"saveTo": "cloud", "updatedAt": now()}
		if archiveStatus == "error" {
			patch["cloudArchive"] = Obj{"status": "uploading", "error": nil, "retryAt": 0, "attempts": 0}
		}
		j, e = a.Store.Patch("downloads", gid, patch)
		if e != nil {
			return e
		}
		a.notifyDownloads()
		if str(j["status"]) == "complete" {
			a.dispatchArchiveJob(gid)
		}
		ok(w, a.publicDownloads(j))
		return nil
	})
	a.handle("DELETE /api/downloads/{gid}/archive", func(w http.ResponseWriter, r *http.Request) error {
		gid := r.PathValue("gid")
		j, e := a.Store.Get("downloads", gid)
		if e != nil {
			return e
		}
		if j == nil {
			return errors.New("任务不存在")
		}
		if e = a.stopArchiveJob(gid); e != nil {
			return e
		}
		archive := object(j["cloudArchive"])
		archiveStatus := str(archive["status"])
		patch := Obj{"saveTo": "local", "updatedAt": now()}
		if archiveStatus != "verified" && archiveStatus != "archived" {
			patch["cloudArchive"] = nil
		}
		j, e = a.Store.Patch("downloads", gid, patch)
		if e != nil {
			return e
		}
		a.notifyDownloads()
		ok(w, a.publicDownloads(j))
		return nil
	})
	a.handle("DELETE /api/downloads/{gid}", func(w http.ResponseWriter, r *http.Request) error {
		gid := r.PathValue("gid")
		j, e := a.Store.Get("downloads", gid)
		if e != nil {
			return e
		}
		if j == nil {
			return errors.New("任务不存在")
		}
		o, _ := body(w, r)
		if e = a.stopArchiveJob(gid); e != nil {
			return e
		}
		if e = a.stopJob(gid); e != nil {
			return e
		}
		if a.engine != nil {
			a.engine.StopSeeding(gid)
		}
		j, _ = a.Store.Get("downloads", gid)
		if j == nil {
			ok(w, Obj{"deleted": true, "deletedLocalFileCount": 0})
			return nil
		}
		count := 0
		if flag(o["deleteLocalFiles"]) {
			dir := str(j["directory"])
			for _, item := range list(j["files"]) {
				p := str(object(item)["path"])
				if inside(dir, p) {
					if e = os.Remove(p); e == nil {
						count++
					} else if !os.IsNotExist(e) {
						return e
					}
					_ = os.Remove(p + ".aria2")
					_ = os.Remove(p + ".part")
					_ = os.Remove(p + httpStateSuffix)
					_ = os.Remove(p + httpStateSuffix + ".tmp")
				}
			}
			removeAria2Leftovers(dir, gid)
			a.removeEmptyTaskDir(dir)
		}
		_ = os.RemoveAll(a.btTaskStateDir(gid))
		if fid := str(j["feedItemId"]); fid != "" {
			_, _ = a.Store.Patch("feed", fid, Obj{"downloadStatus": "ignored"})
		}
		e = a.Store.Delete("downloads", gid)
		if e == nil {
			a.notifyDownloads()
			ok(w, Obj{"deleted": true, "deletedLocalFileCount": count})
		}
		return e
	})
	a.handle("POST /api/downloads/clear-finished", func(w http.ResponseWriter, r *http.Request) error {
		jobs, e := a.Store.List("downloads")
		if e != nil {
			return e
		}
		ids := []string{}
		for _, j := range jobs {
			gid := str(j["gid"])
			if a.isArchiving(gid) {
				continue
			}
			status := str(j["status"])
			if (status == "complete" || status == "error" || status == "removed") && j["cloudArchive"] == nil && !a.archiveWanted(j) {
				if e = a.Store.Delete("downloads", gid); e != nil {
					return e
				}
				ids = append(ids, gid)
			}
		}
		if len(ids) > 0 {
			a.notifyDownloads()
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

func (a *App) setLiveTransfer(gid string, fileIndex int, inFlightBytes, uploadSpeed int64) {
	a.liveTransferMu.Lock()
	a.liveTransfers[gid] = liveTransferProgress{fileIndex: fileIndex, inFlightBytes: inFlightBytes, uploadSpeed: uploadSpeed}
	a.liveTransferMu.Unlock()
}

func (a *App) clearLiveTransfer(gid string) {
	a.liveTransferMu.Lock()
	delete(a.liveTransfers, gid)
	a.liveTransferMu.Unlock()
}

func (a *App) getLiveTransfer(gid string) (liveTransferProgress, bool) {
	a.liveTransferMu.RLock()
	lt, ok := a.liveTransfers[gid]
	a.liveTransferMu.RUnlock()
	return lt, ok
}

func (a *App) publicDownloads(v any) any {
	sanitized := publicDownloads(v)
	applyOne := func(item Obj) Obj {
		gid := str(item["gid"])
		if a.engine != nil && a.engine.CanStream(gid) {
			item["canPlayExistingFile"] = true
		}
		lt, ok := a.getLiveTransfer(gid)
		if !ok {
			return item
		}
		files := list(item["files"])
		if lt.fileIndex >= 0 && lt.fileIndex < len(files) && lt.inFlightBytes > 0 {
			f := object(files[lt.fileIndex])
			base := num(f["uploadedBytes"])
			size := num(f["size"])
			live := base + lt.inFlightBytes
			if size > 0 && live > size {
				live = size
			}
			f["uploadedBytes"] = live
		}
		archive := object(item["cloudArchive"])
		if archive != nil && str(archive["status"]) == "uploading" {
			archive["uploadSpeed"] = lt.uploadSpeed
		}
		return item
	}
	switch x := sanitized.(type) {
	case []Obj:
		for i := range x {
			x[i] = applyOne(x[i])
		}
		return x
	case Obj:
		return applyOne(x)
	}
	return sanitized
}

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
