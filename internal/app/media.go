package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Media struct {
	a          *App
	mu         sync.Mutex
	readers    map[string]int
	sessions   map[string]*hlsSession
	probeCache map[string]Obj
	secret     []byte
}
type hlsSession struct {
	dir    string
	cancel context.CancelFunc
	done   chan struct{}
	last   time.Time
	err    error
	start  int
}

func NewMedia(a *App) *Media {
	return &Media{a: a, readers: map[string]int{}, sessions: map[string]*hlsSession{}, probeCache: map[string]Obj{}, secret: []byte(randomID(32))}
}
func (m *Media) Busy(gid string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.readers[gid] > 0 {
		return true
	}
	s := m.sessions[gid]
	if s == nil {
		return false
	}
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}
func (m *Media) hold(gid string) func() {
	m.mu.Lock()
	m.readers[gid]++
	m.mu.Unlock()
	return func() { m.mu.Lock(); m.readers[gid]--; m.mu.Unlock() }
}
func (m *Media) Close() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = map[string]*hlsSession{}
	m.mu.Unlock()
	for _, s := range sessions {
		s.cancel()
		<-s.done
		_ = os.RemoveAll(s.dir)
	}
}
func (m *Media) Prune() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for gid, s := range m.sessions {
		if time.Since(s.last) > 5*time.Minute {
			s.cancel()
			delete(m.sessions, gid)
			go func(s *hlsSession) { <-s.done; _ = os.RemoveAll(s.dir) }(s)
		}
	}
}
func (m *Media) signature(gid string) string {
	h := hmac.New(sha256.New, m.secret)
	_, _ = h.Write([]byte(gid))
	return hex.EncodeToString(h.Sum(nil))
}
func (m *Media) inputURL(gid string) string {
	_, port, _ := net.SplitHostPort(m.a.cfg.Addr)
	return "http://127.0.0.1:" + port + "/internal/media/" + url.PathEscape(gid) + "?sig=" + m.signature(gid)
}
func mediaFile(j Obj) (Obj, error) {
	var best Obj
	var size int64
	for _, v := range list(j["files"]) {
		f := object(v)
		ext := strings.ToLower(filepath.Ext(str(f["path"])))
		if !isPlayableExt(ext) {
			continue
		}
		if num(f["size"]) > size {
			size = num(f["size"])
			best = f
		}
	}
	if best == nil {
		return nil, errors.New("没有可播放的媒体文件")
	}
	return best, nil
}
func (m *Media) source(gid string) (Obj, Obj, error) {
	if relSlash, ok := decodeLocalMediaID(gid); ok {
		_, _, realPath, info, err := m.a.resolveExistingLocalPath(relSlash)
		if err != nil {
			return nil, nil, err
		}
		if !info.Mode().IsRegular() || !isPlayableExt(filepath.Ext(realPath)) {
			return nil, nil, errors.New("不是可播放的媒体文件")
		}
		return Obj{"status": "complete"}, Obj{"path": realPath, "size": info.Size()}, nil
	}
	if provider, fileID, safeName, ok := decodeCloudMediaID(gid); ok {
		if !m.a.cloudProviderConnected(provider) {
			return nil, nil, fmt.Errorf("%s 未连接", cloudProviderDisplayName(provider))
		}
		return Obj{"status": "complete", "cloudProvider": provider}, Obj{"path": safeName, "remoteId": fileID, "remoteProvider": provider, "verified": true, "remoteOnly": true}, nil
	}
	j, e := m.a.Store.Get("downloads", gid)
	if e != nil {
		return nil, nil, e
	}
	if j == nil {
		return nil, nil, errors.New("下载任务不存在")
	}
	f, e := mediaFile(j)
	return j, f, e
}
func (m *Media) stream(w http.ResponseWriter, r *http.Request) error {
	return m.serveStreamByGID(w, r, r.PathValue("gid"))
}
func (m *Media) serveStreamByGID(w http.ResponseWriter, r *http.Request, gid string) error {
	release := m.hold(gid)
	defer release()
	j, f, e := m.source(gid)
	if e != nil {
		return e
	}
	path := str(f["path"])
	if !flag(f["remoteOnly"]) && m.a.engine != nil {
		if rc, modTime, ok := m.a.engine.OpenStream(r.Context(), gid, path); ok {
			defer rc.Close()
			http.ServeContent(w, r, filepath.Base(path), modTime, rc)
			return nil
		}
	}
	if st := str(j["status"]); st != "" && st != "complete" {
		return errors.New("下载尚未完成，继续下载后即可边下边播")
	}
	if !flag(f["remoteOnly"]) {
		if local, e := os.Open(path); e == nil {
			defer local.Close()
			info, e := local.Stat()
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				return errors.New("不是普通媒体文件")
			}
			http.ServeContent(w, r, filepath.Base(path), info.ModTime(), local)
			return nil
		}
	}
	id := str(f["remoteId"])
	if id == "" || !flag(f["verified"]) {
		return errors.New("本地文件不存在，且没有已校验的云端副本")
	}
	provider := firstNonEmpty(str(f["remoteProvider"]), str(j["cloudProvider"]), "google")
	res, e := m.a.openCloudRangeResponse(r.Context(), provider, id, r.Method, r.Header.Get("Range"))
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 206 && res.StatusCode != 416 {
		return fmt.Errorf("云端播放 HTTP %d", res.StatusCode)
	}
	for _, k := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if value := res.Header.Get(k); value != "" {
			w.Header().Set(k, value)
		}
	}
	w.Header().Set("Content-Type", mimeFor(path))
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(res.StatusCode)
	if r.Method != "HEAD" {
		_, _ = io.Copy(w, res.Body)
	}
	return nil
}
func (m *Media) probe(ctx context.Context, gid string) (Obj, error) {
	m.mu.Lock()
	if cached, ok := m.probeCache[gid]; ok {
		m.mu.Unlock()
		return cached, nil
	}
	m.mu.Unlock()
	binary := m.ffprobe()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, e := exec.CommandContext(ctx, binary, "-v", "error", "-show_format", "-show_streams", "-of", "json", m.inputURL(gid)).Output()
	if e != nil {
		return nil, e
	}
	var o Obj
	if e = json.Unmarshal(out, &o); e != nil {
		return nil, e
	}
	m.mu.Lock()
	if len(m.probeCache) > 256 {
		m.probeCache = map[string]Obj{}
	}
	m.probeCache[gid] = o
	m.mu.Unlock()
	return o, nil
}

func (m *Media) EnsureLocalThumbnail(ctx context.Context, gid, localPath string) {
	if localPath == "" {
		return
	}
	if info, err := os.Stat(localPath); err != nil || !info.Mode().IsRegular() {
		return
	}
	dir := m.a.thumbnailsDir()
	_ = os.MkdirAll(dir, 0700)
	thumbPath := filepath.Join(dir, hash(gid)+".jpg")
	if _, err := os.Stat(thumbPath); err == nil {
		return
	}
	tCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = exec.CommandContext(tCtx, m.ffmpeg(), "-nostdin", "-y", "-v", "error", "-ss", "8", "-i", localPath, "-frames:v", "1", "-vf", "scale=640:-1", thumbPath).Run()
}
func (m *Media) playback(w http.ResponseWriter, r *http.Request) error {
	return m.servePlaybackByGID(w, r, r.PathValue("gid"), false)
}
func (m *Media) servePlaybackByGID(w http.ResponseWriter, r *http.Request, gid string, hideLocalPath bool) error {
	release := m.hold(gid)
	defer release()
	_, file, e := m.source(gid)
	if e != nil {
		return e
	}
	path := str(file["path"])
	ext := strings.ToLower(filepath.Ext(path))
	direct := strings.Contains("|.mp4|.webm|.m4v|.mp3|.flac|.m4a|.ogg|.opus|.wav|", "|"+ext+"|")
	probe, probeErr := m.probe(r.Context(), gid)
	tracks := []Obj{}
	var duration any
	if probeErr == nil {
		duration, _ = strconv.ParseFloat(str(object(probe["format"])["duration"]), 64)
		for _, v := range list(probe["streams"]) {
			s := object(v)
			codec := str(s["codec_name"])
			if str(s["codec_type"]) == "video" && codec != "h264" && codec != "vp8" && codec != "vp9" && codec != "av1" {
				direct = false
			}
			if str(s["codec_type"]) == "subtitle" {
				tag := object(s["tags"])
				label := str(tag["title"])
				if label == "" {
					label = "字幕 " + str(s["index"])
				}
				tracks = append(tracks, Obj{"id": str(s["index"]), "label": label, "codec": codec, "language": tag["language"]})
			}
		}
	}
	_, ffErr := exec.LookPath(m.ffmpeg())
	mode := "direct"
	if !direct {
		if ffErr != nil {
			return errors.New("此视频需要 FFmpeg 转码；请安装 FFmpeg 或在设置里填写 ffmpeg 路径（桌面版和 Docker 镜像已内置）")
		}
		mode = "hls-transcode"
	}
	exposedPath := path
	exposedGID := gid
	if hideLocalPath {
		exposedPath = filepath.Base(path)
		exposedGID = ""
	}
	ok(w, Obj{"gid": exposedGID, "filePath": exposedPath, "fileName": filepath.Base(path), "mimeType": mimeFor(path), "durationSeconds": duration, "deliveryMode": mode, "supportsInAppPlayback": true, "supportsTranscodePlayback": ffErr == nil, "subtitleTracks": tracks, "availablePlayers": []any{}})
	return nil
}
func (m *Media) hls(gid string, start int) (*hlsSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[gid]; s != nil {
		if start < 0 || start >= s.start && start < s.start+30 {
			s.last = time.Now()
			return s, nil
		}
		if _, e := os.Stat(filepath.Join(s.dir, fmt.Sprintf("segment%05d.ts", start))); e == nil {
			s.last = time.Now()
			return s, nil
		}
		s.cancel()
		<-s.done
		_ = os.RemoveAll(s.dir)
		delete(m.sessions, gid)
	}
	active := 0
	for _, s := range m.sessions {
		select {
		case <-s.done:
		default:
			active++
		}
	}
	if active >= 2 {
		return nil, errors.New("同时最多两路转码，请稍后再试")
	}
	if start < 0 {
		start = 0
	}
	hlsRoot := m.a.hlsCacheDir()
	if e := os.MkdirAll(hlsRoot, 0700); e != nil {
		return nil, e
	}
	dir, e := os.MkdirTemp(hlsRoot, "hls-")
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(m.a.ctx)
	s := &hlsSession{dir: dir, cancel: cancel, done: make(chan struct{}), last: time.Now(), start: start}
	args := []string{"-nostdin", "-v", "error", "-ss", fmt.Sprint(start * 6), "-i", m.inputURL(gid), "-t", "180", "-map", "0:v:0", "-map", "0:a:0?", "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-maxrate", "4M", "-bufsize", "8M", "-pix_fmt", "yuv420p", "-c:a", "aac", "-force_key_frames", "expr:gte(t,n_forced*6)", "-f", "hls", "-hls_time", "6", "-hls_list_size", "0", "-hls_flags", "independent_segments+temp_file", "-start_number", fmt.Sprint(start), "-hls_segment_filename", filepath.Join(dir, "segment%05d.ts"), filepath.Join(dir, "master.m3u8")}
	cmd := exec.CommandContext(ctx, m.ffmpeg(), args...)
	if e = cmd.Start(); e != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return nil, e
	}
	m.sessions[gid] = s
	go func() { s.err = cmd.Wait(); close(s.done) }()
	return s, nil
}
func waitFile(ctx context.Context, path string, done <-chan struct{}) error {
	timer := time.NewTimer(25 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, e := os.Stat(path); e == nil && info.Size() > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			if _, e := os.Stat(path); e != nil {
				return errors.New("媒体处理未生成输出")
			}
			return nil
		case <-timer.C:
			return errors.New("媒体处理超时，请重试")
		case <-ticker.C:
		}
	}
}
func (m *Media) serveProgressByGID(w http.ResponseWriter, gid string) error {
	m.mu.Lock()
	s := m.sessions[gid]
	produced := 0
	finished := false
	if s != nil {
		entries, _ := os.ReadDir(s.dir)
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".ts") {
				produced++
			}
		}
		select {
		case <-s.done:
			finished = true
		default:
		}
	}
	m.mu.Unlock()
	ok(w, Obj{"producedSeconds": produced * 6, "bufferedSeconds": produced * 6, "finished": finished})
	return nil
}

func (m *Media) serveHLSByGID(w http.ResponseWriter, r *http.Request, gid, name, querySuffix string) error {
	if name == "master.m3u8" {
		probe, e := m.probe(r.Context(), gid)
		if e != nil {
			return errors.New("无法读取视频时长，请检查 FFmpeg/ffprobe")
		}
		duration, _ := strconv.ParseFloat(str(object(probe["format"])["duration"]), 64)
		if duration <= 0 || duration > 7*24*3600 {
			return errors.New("视频时长无效")
		}
		var playlist strings.Builder
		playlist.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-INDEPENDENT-SEGMENTS\n")
		for i := 0; i < int(math.Ceil(duration/6)); i++ {
			fmt.Fprintf(&playlist, "#EXTINF:%.3f,\nsegment%05d.ts%s\n", math.Min(6, duration-float64(i)*6), i, querySuffix)
		}
		playlist.WriteString("#EXT-X-ENDLIST\n")
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
		_, e = io.WriteString(w, playlist.String())
		return e
	}
	if !strings.HasPrefix(name, "segment") || !strings.HasSuffix(name, ".ts") {
		return errors.New("无效分片")
	}
	n, e := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "segment"), ".ts"))
	if e != nil || n < 0 || n > 100000 {
		return errors.New("无效分片")
	}
	s, e := m.hls(gid, n)
	if e != nil {
		return e
	}
	path := filepath.Join(s.dir, name)
	if e = waitFile(r.Context(), path, s.done); e != nil {
		return e
	}
	http.ServeFile(w, r, path)
	return nil
}

func (m *Media) subtitleCacheDir() string {
	dir := filepath.Join(m.a.cacheDir(), "subtitles")
	_ = os.MkdirAll(dir, 0700)
	return dir
}

func (m *Media) subtitleCachePath(gid, id, ext string) string {
	key := gid
	if _, file, err := m.source(gid); err == nil {
		if rid := strings.TrimSpace(str(file["remoteId"])); rid != "" {
			prov := firstNonEmpty(str(file["remoteProvider"]), "google")
			key = "cloud:" + prov + ":" + rid
		}
	}
	return filepath.Join(m.subtitleCacheDir(), hash(key)+"-"+id+ext)
}

type extractedSubtitle struct {
	TrackID     string
	Ext         string
	ContentType string
	FileName    string
	Data        []byte
}

// EnsureLocalSubtitles extracts all embedded subtitle tracks from a local media file
// into ~/.bgmcat/cache/subtitles and returns them so archiveJobWithContext can also
// upload lightweight sidecar .ass/.vtt files alongside the video in cloud storage.
func (m *Media) EnsureLocalSubtitles(ctx context.Context, gid, localPath string) []extractedSubtitle {
	if gid == "" || localPath == "" {
		return nil
	}
	if info, err := os.Stat(localPath); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 15*time.Second)
	defer probeCancel()
	pCmd := exec.CommandContext(probeCtx, m.ffprobe(), "-v", "error", "-show_streams", "-select_streams", "s", "-of", "json", localPath)
	pOut, err := pCmd.Output()
	if err != nil {
		return nil
	}
	var parsed Obj
	if json.Unmarshal(pOut, &parsed) != nil {
		return nil
	}
	base := filepath.Base(localPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	var results []extractedSubtitle
	usedNames := map[string]bool{}
	for _, raw := range list(parsed["streams"]) {
		s := object(raw)
		idx := str(s["index"])
		if idx == "" {
			continue
		}
		codecName := strings.ToLower(str(s["codec_name"]))
		ext, format, codec, contentType := ".vtt", "webvtt", "webvtt", "text/vtt"
		if codecName == "ass" || codecName == "ssa" {
			ext, format, codec, contentType = ".ass", "ass", "ass", "text/x-ssa"
		}
		tag := object(s["tags"])
		label := sanitizeFolderName(firstNonEmpty(str(tag["title"]), str(tag["language"]), "track"+idx))
		if label == "" {
			label = "track" + idx
		}
		fileName := stem + "." + label + ext
		if usedNames[fileName] {
			fileName = stem + "." + label + "." + idx + ext
		}
		usedNames[fileName] = true

		cachePath := m.subtitleCachePath(gid, idx, ext)
		var data []byte
		if cached, readErr := os.ReadFile(cachePath); readErr == nil && len(cached) > 0 {
			data = cached
		} else {
			exCtx, exCancel := context.WithTimeout(ctx, 30*time.Second)
			cmd := exec.CommandContext(exCtx, m.ffmpeg(), "-nostdin", "-v", "error", "-i", localPath, "-vn", "-an", "-dn", "-map", "0:"+idx, "-c:s", codec, "-f", format, "pipe:1")
			var out bytes.Buffer
			cmd.Stdout = &out
			runErr := cmd.Run()
			exCancel()
			if runErr == nil && out.Len() > 0 {
				data = out.Bytes()
				tmpPath := cachePath + ".tmp"
				if os.WriteFile(tmpPath, data, 0600) == nil {
					_ = os.Rename(tmpPath, cachePath)
				}
			}
		}
		if len(data) > 0 {
			results = append(results, extractedSubtitle{
				TrackID:     idx,
				Ext:         ext,
				ContentType: contentType,
				FileName:    fileName,
				Data:        data,
			})
		}
	}
	return results
}

func (m *Media) serveSubtitleByGID(w http.ResponseWriter, r *http.Request, gid, track string) error {
	ext := filepath.Ext(track)
	id := strings.TrimSuffix(track, ext)
	n, e := strconv.Atoi(id)
	if e != nil || n < 0 || n > 100 || ext != ".ass" && ext != ".vtt" {
		return errors.New("字幕轨道无效")
	}
	format, codec, content := "webvtt", "webvtt", "text/vtt"
	if ext == ".ass" {
		format = "ass"
		codec = "ass"
		content = "text/x-ssa"
	}
	cachePath := m.subtitleCachePath(gid, id, ext)
	if cached, readErr := os.ReadFile(cachePath); readErr == nil && len(cached) > 0 {
		w.Header().Set("Content-Type", content+"; charset=utf-8")
		_, e = w.Write(cached)
		return e
	}
	// If a sidecar subtitle was uploaded to cloud storage alongside the video, fetch that ~80KB file
	// directly (~200ms) instead of downloading the entire 400MB MKV just to demux subtitles.
	if job, _, srcErr := m.source(gid); srcErr == nil && job != nil {
		if cloudSubs := object(job["cloudSubtitles"]); cloudSubs != nil {
			if subMeta := object(cloudSubs[id+ext]); subMeta != nil {
				rid := str(subMeta["remoteId"])
				prov := firstNonEmpty(str(subMeta["provider"]), str(job["cloudProvider"]), "google")
				if rid != "" {
					fetchCtx, fetchCancel := context.WithTimeout(r.Context(), 20*time.Second)
					res, fetchErr := m.a.openCloudRangeResponse(fetchCtx, prov, rid, "GET", "")
					if fetchErr == nil && res != nil {
						data, readErr := io.ReadAll(io.LimitReader(res.Body, 16<<20))
						res.Body.Close()
						fetchCancel()
						if readErr == nil && res.StatusCode >= 200 && res.StatusCode < 300 && len(data) > 0 {
							tmpPath := cachePath + ".tmp"
							if os.WriteFile(tmpPath, data, 0600) == nil {
								_ = os.Rename(tmpPath, cachePath)
							}
							w.Header().Set("Content-Type", content+"; charset=utf-8")
							_, e = w.Write(data)
							return e
						}
					} else {
						fetchCancel()
					}
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, m.ffmpeg(), "-nostdin", "-v", "error", "-i", m.inputURL(gid), "-vn", "-an", "-dn", "-map", "0:"+id, "-c:s", codec, "-f", format, "pipe:1")
	var out bytes.Buffer
	cmd.Stdout = &out
	if e = cmd.Run(); e != nil {
		return errors.New("字幕提取失败")
	}
	data := out.Bytes()
	if len(data) > 0 {
		tmpPath := cachePath + ".tmp"
		if os.WriteFile(tmpPath, data, 0600) == nil {
			_ = os.Rename(tmpPath, cachePath)
		}
	}
	w.Header().Set("Content-Type", content+"; charset=utf-8")
	_, e = w.Write(data)
	return e
}

func (m *Media) Routes() {
	a := m.a
	a.handle("GET /internal/media/{gid}", func(w http.ResponseWriter, r *http.Request) error {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() || !hmac.Equal([]byte(r.URL.Query().Get("sig")), []byte(m.signature(r.PathValue("gid")))) {
			fail(w, 403, "无效播放请求")
			return nil
		}
		return m.stream(w, r)
	})
	a.handle("GET /api/downloads/{gid}/stream", m.stream)
	a.handle("GET /api/downloads/{gid}/playback", m.playback)
	a.handle("GET /api/downloads/{gid}/playback/progress", func(w http.ResponseWriter, r *http.Request) error {
		return m.serveProgressByGID(w, r.PathValue("gid"))
	})
	a.handle("GET /api/downloads/{gid}/hls/{name}", func(w http.ResponseWriter, r *http.Request) error {
		tok := r.URL.Query().Get("token")
		suffix := ""
		if tok != "" {
			suffix = "?token=" + url.QueryEscape(tok)
		}
		return m.serveHLSByGID(w, r, r.PathValue("gid"), r.PathValue("name"), suffix)
	})
	a.handle("GET /api/downloads/{gid}/subtitles/{track}", func(w http.ResponseWriter, r *http.Request) error {
		return m.serveSubtitleByGID(w, r, r.PathValue("gid"), r.PathValue("track"))
	})
	a.handle("GET /api/downloads/{gid}/thumbnail", func(w http.ResponseWriter, r *http.Request) error {
		gid := r.PathValue("gid")
		if _, _, e := m.source(gid); e != nil {
			return e
		}
		dir := a.thumbnailsDir()
		_ = os.MkdirAll(dir, 0700)
		path := filepath.Join(dir, hash(gid)+".jpg")
		if _, e := os.Stat(path); e != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			if e = exec.CommandContext(ctx, m.ffmpeg(), "-nostdin", "-y", "-v", "error", "-ss", "8", "-i", m.inputURL(gid), "-frames:v", "1", "-vf", "scale=640:-1", path).Run(); e != nil {
				return errors.New("缩略图生成失败")
			}
		}
		http.ServeFile(w, r, path)
		return nil
	})
	a.handle("POST /api/downloads/playback-cache/clear", func(w http.ResponseWriter, r *http.Request) error {
		m.Close()
		var freedBytes int64
		count := 0
		for _, cacheTarget := range []string{a.hlsCacheDir(), a.thumbnailsDir()} {
			if entries, err := os.ReadDir(cacheTarget); err == nil {
				for _, entry := range entries {
					full := filepath.Join(cacheTarget, entry.Name())
					bytesUsed, _ := measurePathUsage(full)
					if err = os.RemoveAll(full); err != nil {
						return err
					}
					freedBytes += bytesUsed
					count++
				}
			}
		}
		if entries, err := os.ReadDir(a.cfg.DataDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() && strings.HasPrefix(entry.Name(), "hls-") {
					full := filepath.Join(a.cfg.DataDir, entry.Name())
					bytesUsed, _ := measurePathUsage(full)
					if err = os.RemoveAll(full); err != nil {
						return err
					}
					freedBytes += bytesUsed
					count++
				}
			}
		}
		_ = os.MkdirAll(a.hlsCacheDir(), 0700)
		_ = os.MkdirAll(a.thumbnailsDir(), 0700)
		ok(w, Obj{"removedCount": count, "freedBytes": freedBytes})
		return nil
	})
	a.handle("POST /api/downloads/{gid}/open-player", func(w http.ResponseWriter, r *http.Request) error {
		return errors.New("Go 服务在浏览器内播放；外部播放器请打开带授权的流地址")
	})
}
