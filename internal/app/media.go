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
	a        *App
	mu       sync.Mutex
	readers  map[string]int
	sessions map[string]*hlsSession
	secret   []byte
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
	return &Media{a: a, readers: map[string]int{}, sessions: map[string]*hlsSession{}, secret: []byte(randomID(32))}
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
		if !strings.Contains("|.mp4|.mkv|.webm|.avi|.ts|.mov|.m4v|.mp3|.flac|", "|"+ext+"|") {
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
	gid := r.PathValue("gid")
	release := m.hold(gid)
	defer release()
	job, f, e := m.source(gid)
	if e != nil {
		return e
	}
	path := str(f["path"])
	// 边下边播交给 anacrolix 的任务：从它的读取器读，读到还没下的地方会先去下、等着，所以能任意拖动
	// 只在它真的在跑时等；暂停或出错的任务等不来读取器，照常读磁盘上已有的部分
	if seekableStream(job) {
		reader, e := m.a.stream.waitReader(r.Context(), gid, path)
		if e != nil {
			return e
		}
		defer reader.Close()
		reader.SetContext(r.Context())
		w.Header().Set("Content-Type", mimeFor(path))
		http.ServeContent(w, r, filepath.Base(path), time.Time{}, reader)
		return nil
	}
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
	id := str(f["remoteId"])
	if id == "" || !flag(f["verified"]) {
		return errors.New("本地文件不存在，且没有已校验的云端副本")
	}
	res, e := m.a.driveRequest(r.Context(), r.Method, driveBase+"/files/"+url.PathEscape(id)+"?alt=media", nil, map[string]string{"Range": r.Header.Get("Range"), "Accept-Encoding": "identity"})
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
func (m *Media) ffmpeg() string {
	if p := str(m.a.Settings()["ffmpegPath"]); p != "" {
		return p
	}
	return "ffmpeg"
}
func (m *Media) probe(ctx context.Context, gid string) (Obj, error) {
	binary := "ffprobe"
	if p := str(m.a.Settings()["ffmpegPath"]); p != "" {
		binary = filepath.Join(filepath.Dir(p), "ffprobe")
		if strings.HasSuffix(p, ".exe") {
			binary += ".exe"
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, e := exec.CommandContext(ctx, binary, "-v", "error", "-show_format", "-show_streams", "-of", "json", m.inputURL(gid)).Output()
	if e != nil {
		return nil, e
	}
	var o Obj
	e = json.Unmarshal(out, &o)
	return o, e
}
func (m *Media) playback(w http.ResponseWriter, r *http.Request) error {
	gid := r.PathValue("gid")
	release := m.hold(gid)
	defer release()
	job, file, e := m.source(gid)
	if e != nil {
		return e
	}
	path := str(file["path"])
	ext := strings.ToLower(filepath.Ext(path))
	direct := ext == ".mp4" || ext == ".webm" || ext == ".mp3" || ext == ".m4v"
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
	complete := !downloading(job)
	if !complete && probeErr != nil {
		return errors.New("还没下载到能播放的部分，请稍候再试")
	}
	// 没下完的文件中间有空洞，只能走转码：HLS 列表只列已下好的分片，播放器不会去读空洞
	if !complete {
		direct = false
	}
	mode := "direct"
	if !direct {
		if ffErr != nil {
			return errors.New("此视频需要 FFmpeg 转码；请使用包含 FFmpeg 的发布包或 Docker 镜像")
		}
		mode = "hls-transcode"
	}
	seconds, _ := duration.(float64)
	ok(w, Obj{"gid": gid, "filePath": path, "fileName": filepath.Base(path), "mimeType": mimeFor(path), "durationSeconds": duration, "complete": complete, "seekable": seekableStream(job), "playableSeconds": playableSeconds(job, file, seconds), "deliveryMode": mode, "supportsInAppPlayback": true, "supportsTranscodePlayback": ffErr == nil, "subtitleTracks": tracks, "availablePlayers": []any{}})
	return nil
}

// playableSeconds 估算边下边播能放到第几秒：按连续下好的字节占比折算，再留一成余量和一个分片，
// 码率不均匀时也不至于读到还没下的地方。任务已完成返回 -1。
func playableSeconds(job, file Obj, duration float64) float64 {
	if !downloading(job) {
		return -1
	}
	// anacrolix 接管、正在下的任务读到哪下到哪，整段都能播；暂停了就只能播连续下好的部分
	if seekableStream(job) {
		return duration
	}
	size := num(file["size"])
	if size <= 0 || duration <= 0 {
		return 0
	}
	return max(0, duration*float64(num(job["playableBytes"]))/float64(size)*0.9-6)
}

// streamWait 是边下边播时，分片请求等下载追上来的最长时间；测试里会改短。
var streamWait = 60 * time.Second

// waitPlayable 等到边下边播能放到 need 秒，返回当时能放到的位置（下完了返回 -1）。
// 播放器预读到还没下的分片时就在这里挂着，下好了再转码，播放器那边只是缓冲。
func (m *Media) waitPlayable(ctx context.Context, gid string, file Obj, need float64) (float64, error) {
	probe, e := m.probe(ctx, gid)
	if e != nil {
		return 0, errors.New("还没下载到这里，请稍候")
	}
	duration, _ := strconv.ParseFloat(str(object(probe["format"])["duration"]), 64)
	deadline := time.Now().Add(streamWait)
	for {
		job, e := m.a.Store.Get("downloads", gid)
		if e != nil || job == nil {
			return 0, errors.New("下载任务不存在")
		}
		limit := playableSeconds(job, file, duration)
		if limit < 0 || limit >= need {
			return limit, nil
		}
		if time.Now().After(deadline) {
			return 0, errors.New("还没下载到这里，请稍候")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// seekableStream 判断任务是不是由 anacrolix 接管且正在下：这时读到哪下到哪，可以任意拖动。
func seekableStream(job Obj) bool {
	switch str(job["status"]) {
	case "active", "queued", "waiting":
		return str(job["engine"]) == "anacrolix"
	}
	return false
}

// downloading 判断文件是不是还没下完。只认明确的进行中状态，没写状态的旧任务、导入的任务按完整文件处理。
func downloading(job Obj) bool {
	switch str(job["status"]) {
	case "queued", "active", "waiting", "paused", "error":
		return true
	}
	return false
}

// hls 启动或复用一路转码。limit 是边下边播能放到的秒数（-1 表示文件完整），转码不会读过它。
func (m *Media) hls(gid string, start int, limit float64) (*hlsSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[gid]; s != nil {
		running := true
		select {
		case <-s.done:
			running = false
		default:
		}
		// 已经结束的会话（比如边下边播时只转到当时下好的位置）里没有的分片要重开一路
		if start < 0 || running && start >= s.start && start < s.start+30 {
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
	dir, e := os.MkdirTemp(filepath.Join(m.a.cfg.DataDir), "hls-")
	if e != nil {
		return nil, e
	}
	duration := 180.0
	if limit >= 0 {
		duration = min(duration, limit-float64(start*6))
		if duration < 6 {
			_ = os.RemoveAll(dir)
			return nil, errors.New("还没下载到这里，请稍候")
		}
	}
	ctx, cancel := context.WithCancel(m.a.ctx)
	s := &hlsSession{dir: dir, cancel: cancel, done: make(chan struct{}), last: time.Now(), start: start}
	args := []string{"-nostdin", "-v", "error", "-ss", fmt.Sprint(start * 6), "-i", m.inputURL(gid), "-t", strconv.FormatFloat(duration, 'f', 3, 64), "-map", "0:v:0", "-map", "0:a:0?", "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-maxrate", "4M", "-bufsize", "8M", "-pix_fmt", "yuv420p", "-c:a", "aac", "-force_key_frames", "expr:gte(t,n_forced*6)", "-f", "hls", "-hls_time", "6", "-hls_list_size", "0", "-hls_flags", "independent_segments+temp_file", "-start_number", fmt.Sprint(start), "-hls_segment_filename", filepath.Join(dir, "segment%05d.ts"), filepath.Join(dir, "master.m3u8")}
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
func waitFile(ctx context.Context, path string, done <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
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
	a.handle("GET /api/downloads/{gid}/stream", a.watched(m.stream))
	a.handle("GET /api/downloads/{gid}/playback", a.watched(m.playback))
	a.handle("GET /api/downloads/{gid}/playback/progress", func(w http.ResponseWriter, r *http.Request) error {
		m.mu.Lock()
		s := m.sessions[r.PathValue("gid")]
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
		ok(w, Obj{"producedSeconds": produced * 6, "finished": finished})
		return nil
	})
	a.handle("GET /api/downloads/{gid}/hls/{name}", a.watched(func(w http.ResponseWriter, r *http.Request) error {
		name := r.PathValue("name")
		job, file, e := m.source(r.PathValue("gid"))
		if e != nil {
			return e
		}
		if name == "master.m3u8" {
			probe, e := m.probe(r.Context(), r.PathValue("gid"))
			if e != nil {
				return errors.New("无法读取视频时长，请检查 FFmpeg/ffprobe")
			}
			duration, _ := strconv.ParseFloat(str(object(probe["format"])["duration"]), 64)
			if duration <= 0 || duration > 7*24*3600 {
				return errors.New("视频时长无效")
			}
			// 边下边播也给完整的 VOD 列表：EVENT 列表在 AVPlayer 那里按直播处理，
			// 下载一停、列表 9 秒不变就直接报错。没下到的分片由下面的请求等着补上
			if limit := playableSeconds(job, file, duration); limit >= 0 && limit < 6 {
				return errors.New("已下载的部分还不够播放，请稍候")
			}
			var playlist strings.Builder
			playlist.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-INDEPENDENT-SEGMENTS\n")
			for i := 0; i < int(math.Ceil(duration/6)); i++ {
				fmt.Fprintf(&playlist, "#EXTINF:%.3f,\nsegment%05d.ts?token=%s\n", math.Min(6, duration-float64(i)*6), i, url.QueryEscape(r.URL.Query().Get("token")))
			}
			playlist.WriteString("#EXT-X-ENDLIST\n")
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Cache-Control", "no-store")
			_, e = io.WriteString(w, playlist.String())
			return e
		}
		start := -1
		if name != "master.m3u8" {
			if !strings.HasPrefix(name, "segment") || !strings.HasSuffix(name, ".ts") {
				return errors.New("无效分片")
			}
			n, e := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "segment"), ".ts"))
			if e != nil || n < 0 || n > 100000 {
				return errors.New("无效分片")
			}
			start = n
		}
		limit := -1.0
		if downloading(job) {
			if limit, e = m.waitPlayable(r.Context(), r.PathValue("gid"), file, float64((start+1)*6)); e != nil {
				return e
			}
		}
		s, e := m.hls(r.PathValue("gid"), start, limit)
		if e != nil {
			return e
		}
		path := filepath.Join(s.dir, name)
		// anacrolix 接管的任务拖到还没下的地方时，转码要等那一段现下，给到和边下边播等数据一样长
		wait := 25 * time.Second
		if seekableStream(job) {
			wait = streamWait
		}
		if e = waitFile(r.Context(), path, s.done, wait); e != nil {
			return e
		}
		http.ServeFile(w, r, path)
		return nil
	}))
	a.handle("GET /api/downloads/{gid}/subtitles/{track}", func(w http.ResponseWriter, r *http.Request) error {
		track := r.PathValue("track")
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
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, m.ffmpeg(), "-nostdin", "-v", "error", "-i", m.inputURL(r.PathValue("gid")), "-map", "0:"+id, "-c:s", codec, "-f", format, "pipe:1")
		var out bytes.Buffer
		cmd.Stdout = &out
		if e = cmd.Run(); e != nil {
			return errors.New("字幕提取失败")
		}
		w.Header().Set("Content-Type", content+"; charset=utf-8")
		_, e = w.Write(out.Bytes())
		return e
	})
	a.handle("GET /api/downloads/{gid}/thumbnail", func(w http.ResponseWriter, r *http.Request) error {
		gid := r.PathValue("gid")
		if _, _, e := m.source(gid); e != nil {
			return e
		}
		dir := filepath.Join(a.cfg.DataDir, "thumbnails")
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
		entries, e := os.ReadDir(a.cfg.DataDir)
		if e != nil {
			return e
		}
		count := 0
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "hls-") {
				if e = os.RemoveAll(filepath.Join(a.cfg.DataDir, entry.Name())); e != nil {
					return e
				}
				count++
			}
		}
		ok(w, Obj{"removedCount": count, "freedBytes": 0})
		return nil
	})
	a.handle("POST /api/downloads/{gid}/open-player", func(w http.ResponseWriter, r *http.Request) error {
		return errors.New("Go 服务在浏览器内播放；外部播放器请打开带授权的流地址")
	})
}
