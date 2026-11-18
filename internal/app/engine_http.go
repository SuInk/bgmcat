package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	httpMaxWorkers     = 16
	httpMinMultiLength = 512 << 10 // 512 KiB
	httpStateSuffix    = ".bgmcat-dl"
)

type httpResumeState struct {
	TotalLength  int64   `json:"totalLength"`
	ChunkSize    int64   `json:"chunkSize"`
	ETag         string  `json:"etag,omitempty"`
	LastModified string  `json:"lastModified,omitempty"`
	Written      []int64 `json:"written"`
}

func httpSegmentSize(total int64) int64 {
	switch {
	case total < 16<<20:
		return 256 << 10 // 256 KiB
	case total < 256<<20:
		return 1 << 20 // 1 MiB
	case total < 1<<30:
		return 2 << 20 // 2 MiB
	default:
		return 4 << 20 // 4 MiB
	}
}

// parseContentRangeAt parses "bytes <start>-<end>/<total>" and verifies <start> == expectedStart.
func parseContentRangeAt(raw string, expectedStart int64) (end int64, total int64, err error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(raw), "bytes ") {
		return 0, 0, fmt.Errorf("无效 Content-Range：%q", raw)
	}
	spec := strings.TrimSpace(raw[len("bytes "):])
	slash := strings.IndexByte(spec, '/')
	if slash <= 0 || slash == len(spec)-1 {
		return 0, 0, fmt.Errorf("无效 Content-Range：%q", raw)
	}
	rangePart := spec[:slash]
	totalPart := spec[slash+1:]
	dash := strings.IndexByte(rangePart, '-')
	if dash <= 0 || dash == len(rangePart)-1 {
		return 0, 0, fmt.Errorf("无效 Content-Range 范围：%q", raw)
	}
	start, sErr := strconv.ParseInt(rangePart[:dash], 10, 64)
	end, eErr := strconv.ParseInt(rangePart[dash+1:], 10, 64)
	if sErr != nil || eErr != nil || start != expectedStart || end < start {
		return 0, 0, fmt.Errorf("服务器返回越界或不匹配的 Content-Range：%q（期望起始 %d）", raw, expectedStart)
	}
	if totalPart == "*" {
		return end, -1, nil
	}
	total, tErr := strconv.ParseInt(totalPart, 10, 64)
	if tErr != nil || total <= end {
		return 0, 0, fmt.Errorf("无效 Content-Range 总长度：%q", raw)
	}
	return end, total, nil
}

func parseDispositionFilename(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	if _, params, err := mime.ParseMediaType(header); err == nil {
		if name := strings.TrimSpace(params["filename"]); name != "" {
			return name
		}
	}
	// Fallback parser for servers emitting slightly non-standard filename*=UTF-8''... or unquoted UTF-8 filename=...
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		lower := strings.ToLower(part)
		if strings.HasPrefix(lower, "filename*=") {
			val := strings.TrimSpace(part[len("filename*="):])
			val = strings.Trim(val, `"`)
			if idx := strings.Index(val, "''"); idx >= 0 {
				if decoded, err := url.PathUnescape(val[idx+2:]); err == nil && decoded != "" {
					return decoded
				}
			}
		}
	}
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		lower := strings.ToLower(part)
		if strings.HasPrefix(lower, "filename=") {
			val := strings.Trim(strings.TrimSpace(part[len("filename="):]), `"`)
			if val != "" {
				return val
			}
		}
	}
	return ""
}

func resolveHTTPTargetPath(dir string, j Obj, res *http.Response, rawSource string) (string, error) {
	if old := strings.TrimSpace(str(j["filePath"])); old != "" && inside(dir, old) {
		return old, nil
	}
	if cdName := parseDispositionFilename(res.Header.Get("Content-Disposition")); cdName != "" {
		p := filepath.Join(dir, safeName(cdName))
		if !inside(dir, p) {
			return "", errors.New("下载文件名越界")
		}
		return p, nil
	}
	pickFromURL := func(u *url.URL) string {
		if u == nil {
			return ""
		}
		cleaned := strings.TrimRight(u.Path, "/")
		if cleaned == "" {
			return ""
		}
		base := filepath.Base(cleaned)
		if unescaped, err := url.PathUnescape(base); err == nil && unescaped != "" {
			base = unescaped
		}
		if base == "" || base == "." || base == "/" {
			return ""
		}
		return base
	}
	name := ""
	if res != nil && res.Request != nil {
		name = pickFromURL(res.Request.URL)
	}
	if name == "" {
		if parsed, err := url.Parse(rawSource); err == nil {
			name = pickFromURL(parsed)
		}
	}
	if name == "" {
		name = "download.bin"
	}
	target := filepath.Join(dir, safeName(name))
	if !inside(dir, target) {
		return "", errors.New("下载目标路径越界")
	}
	return target, nil
}

func (e *NativeEngine) doHTTPWithRetry(ctx context.Context, urlStr, rangeHeader string) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "bgmcat/"+Version)
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		res, err := e.httpClient.Do(req)
		if err == nil {
			if res.StatusCode >= 500 && res.StatusCode != http.StatusNotImplemented && attempt < 2 {
				_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
				res.Body.Close()
				lastErr = fmt.Errorf("HTTP %d", res.StatusCode)
			} else {
				return res, nil
			}
		} else {
			lastErr = err
		}
		if ctx.Err() != nil || attempt == 2 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 150 * time.Millisecond):
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, lastErr
}

func (e *NativeEngine) downloadHTTP(ctx context.Context, j Obj) error {
	source := strings.TrimSpace(str(j["sourceUrl"]))
	if !httpURL(source) {
		return errors.New("无效 HTTP 下载链接")
	}
	dir, err := filepath.Abs(str(j["directory"]))
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	gid := str(j["gid"])

	// Probe with Range: bytes=0-0:
	// - On Range-capable servers (206): validates Content-Range before touching disk, discovers
	//   exact totalLength, ETag, Last-Modified, redirects, and Content-Disposition in 1 byte.
	// - On non-Range servers (200): streams the already-opened response body directly in 1 RTT.
	res, err := e.doHTTPWithRetry(ctx, source, "bytes=0-0")
	if err != nil {
		return err
	}

	if res.StatusCode == http.StatusRequestedRangeNotSatisfiable || res.StatusCode == http.StatusNotImplemented {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		res.Body.Close()
		res, err = e.doHTTPWithRetry(ctx, source, "")
		if err != nil {
			return err
		}
	}

	switch res.StatusCode {
	case http.StatusOK:
		defer res.Body.Close()
		targetPath, pathErr := resolveHTTPTargetPath(dir, j, res, source)
		if pathErr != nil {
			return pathErr
		}
		return e.streamSingleHTTP(ctx, gid, targetPath, res.Body, res.ContentLength)

	case http.StatusPartialContent:
		end, total, rangeErr := parseContentRangeAt(res.Header.Get("Content-Range"), 0)
		if rangeErr != nil || total <= 0 {
			res.Body.Close()
			if rangeErr != nil {
				return rangeErr
			}
			return errors.New("服务器未返回有效的 Content-Range 总大小")
		}
		targetPath, pathErr := resolveHTTPTargetPath(dir, j, res, source)
		if pathErr != nil {
			res.Body.Close()
			return pathErr
		}
		finalURL := source
		if res.Request != nil && res.Request.URL != nil {
			finalURL = res.Request.URL.String()
		}
		etag := strings.TrimSpace(res.Header.Get("ETag"))
		lastMod := strings.TrimSpace(res.Header.Get("Last-Modified"))

		// Tiny file where the 0-0 probe already returned the entire body (e.g. 1-byte file)
		if end == total-1 {
			defer res.Body.Close()
			return e.streamSingleHTTP(ctx, gid, targetPath, io.LimitReader(res.Body, total), total)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		res.Body.Close()

		if total < httpMinMultiLength {
			fullRes, fullErr := e.doHTTPWithRetry(ctx, finalURL, fmt.Sprintf("bytes=0-%d", total-1))
			if fullErr != nil {
				return fullErr
			}
			defer fullRes.Body.Close()
			if fullRes.StatusCode == http.StatusPartialContent {
				if _, gotTotal, crErr := parseContentRangeAt(fullRes.Header.Get("Content-Range"), 0); crErr != nil || gotTotal != total {
					return errors.New("单流 Range 校验失败")
				}
				return e.streamSingleHTTP(ctx, gid, targetPath, io.LimitReader(fullRes.Body, total), total)
			}
			if fullRes.StatusCode == http.StatusOK {
				return e.streamSingleHTTP(ctx, gid, targetPath, fullRes.Body, fullRes.ContentLength)
			}
			return fmt.Errorf("HTTP 下载失败：状态码 %d", fullRes.StatusCode)
		}

		return e.downloadHTTPSegmented(ctx, gid, targetPath, finalURL, total, etag, lastMod)

	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		res.Body.Close()
		return fmt.Errorf("HTTP 下载失败：状态码 %d", res.StatusCode)
	}
}

func (e *NativeEngine) streamSingleHTTP(ctx context.Context, gid, targetPath string, body io.Reader, expectedTotal int64) error {
	partPath := targetPath + ".part"
	statePath := targetPath + httpStateSuffix
	_ = os.Remove(statePath)

	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()

	filesList := []Obj{{"path": targetPath, "size": max(expectedTotal, 0)}}
	if gid != "" {
		_, _ = e.app.Store.Patch("downloads", gid, Obj{
			"totalLength":     max(expectedTotal, 0),
			"completedLength": int64(0),
			"connectionCount": 1,
			"files":           filesList,
			"filePath":        targetPath,
		})
		e.app.notifyDownloads()
	}

	buf := make([]byte, 128<<10)
	var written int64
	lastPatch := time.Now()
	singleMeter := newSpeedWindow(3 * time.Second)
	singleMeter.Observe(lastPatch, 0)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, rErr := body.Read(buf)
		if n > 0 {
			if rlErr := e.waitHTTPRate(ctx, n); rlErr != nil {
				return rlErr
			}
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				return wErr
			}
			written += int64(n)
			nowTime := time.Now()
			if gid != "" && nowTime.Sub(lastPatch) >= 250*time.Millisecond {
				speed := singleMeter.Observe(nowTime, written)
				lastPatch = nowTime
				totalReport := expectedTotal
				if totalReport < written {
					totalReport = written
				}
				_, _ = e.app.Store.Patch("downloads", gid, Obj{
					"totalLength":     totalReport,
					"completedLength": written,
					"downloadSpeed":   speed,
					"connectionCount": 1,
				})
				e.app.notifyDownloads()
			}
		}
		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				break
			}
			return rErr
		}
	}

	if expectedTotal >= 0 && written != expectedTotal {
		return fmt.Errorf("HTTP 下载长度不完整：%d / %d", written, expectedTotal)
	}
	if err = f.Sync(); err != nil {
		return err
	}
	closed = true
	if err = f.Close(); err != nil {
		return err
	}
	if err = replaceFile(partPath, targetPath); err != nil {
		return err
	}
	if gid != "" {
		_, _ = e.app.Store.Patch("downloads", gid, Obj{
			"totalLength":     written,
			"completedLength": written,
			"downloadSpeed":   0,
			"connectionCount": 0,
			"files":           []Obj{{"path": targetPath, "size": written}},
			"filePath":        targetPath,
		})
		e.app.notifyDownloads()
	}
	return nil
}

func loadHTTPResumeState(statePath, partPath string, total, chunkSize int64, etag, lastMod string, numChunks int) (*httpResumeState, bool) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return nil, false
	}
	info, err := os.Stat(partPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != total {
		return nil, false
	}
	var st httpResumeState
	if json.Unmarshal(raw, &st) != nil {
		return nil, false
	}
	if st.TotalLength != total || st.ChunkSize != chunkSize || len(st.Written) != numChunks {
		return nil, false
	}
	if st.ETag != "" && etag != "" && st.ETag != etag {
		return nil, false
	}
	if st.ETag == "" && st.LastModified != "" && lastMod != "" && st.LastModified != lastMod {
		return nil, false
	}
	for i, w := range st.Written {
		cStart := int64(i) * chunkSize
		cLen := min(chunkSize, total-cStart)
		if w < 0 || w > cLen {
			return nil, false
		}
	}
	return &st, true
}

func saveHTTPResumeState(statePath string, st *httpResumeState) {
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := statePath + ".tmp"
	if os.WriteFile(tmp, data, 0600) == nil {
		_ = replaceFile(tmp, statePath)
	}
}

func replaceFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	_ = os.Remove(dst)
	return os.Rename(src, dst)
}

func (e *NativeEngine) downloadHTTPSegmented(ctx context.Context, gid, targetPath, sourceURL string, total int64, etag, lastMod string) error {
	chunkSize := httpSegmentSize(total)
	numChunks := int((total + chunkSize - 1) / chunkSize)
	partPath := targetPath + ".part"
	statePath := targetPath + httpStateSuffix

	state, resumed := loadHTTPResumeState(statePath, partPath, total, chunkSize, etag, lastMod, numChunks)
	var partFile *os.File
	var err error
	if resumed {
		partFile, err = os.OpenFile(partPath, os.O_RDWR, 0600)
		if err != nil {
			resumed = false
		}
	}
	if !resumed {
		partFile, err = os.OpenFile(partPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		if err = partFile.Truncate(total); err != nil {
			_ = partFile.Close()
			return err
		}
		state = &httpResumeState{
			TotalLength:  total,
			ChunkSize:    chunkSize,
			ETag:         etag,
			LastModified: lastMod,
			Written:      make([]int64, numChunks),
		}
		saveHTTPResumeState(statePath, state)
	}

	var (
		stateMu        sync.Mutex
		completedBytes atomic.Int64
		activeWorkers  atomic.Int32
		pendingChunks  = make(chan int, numChunks)
	)
	for i := 0; i < numChunks; i++ {
		w := state.Written[i]
		completedBytes.Add(w)
		cStart := int64(i) * chunkSize
		cLen := min(chunkSize, total-cStart)
		if w < cLen {
			pendingChunks <- i
		}
	}
	close(pendingChunks)

	persistSnapshot := func() {
		stateMu.Lock()
		snap := &httpResumeState{
			TotalLength:  state.TotalLength,
			ChunkSize:    state.ChunkSize,
			ETag:         state.ETag,
			LastModified: state.LastModified,
			Written:      append([]int64(nil), state.Written...),
		}
		stateMu.Unlock()
		saveHTTPResumeState(statePath, snap)
	}

	filesMeta := []Obj{{"path": targetPath, "size": total}}
	if gid != "" {
		_, _ = e.app.Store.Patch("downloads", gid, Obj{
			"totalLength":     total,
			"completedLength": completedBytes.Load(),
			"files":           filesMeta,
			"filePath":        targetPath,
		})
		e.app.notifyDownloads()
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	workerCount := min(httpMaxWorkers, max(len(pendingChunks), 1))
	var (
		wg       sync.WaitGroup
		firstErr error
		errOnce  sync.Once
	)
	recordErr := func(wErr error) {
		if wErr == nil {
			return
		}
		errOnce.Do(func() {
			firstErr = wErr
			cancelRun()
		})
	}

	for wIdx := 0; wIdx < workerCount; wIdx++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 128<<10)
			for idx := range pendingChunks {
				if runCtx.Err() != nil {
					return
				}
				cStart := int64(idx) * chunkSize
				cEnd := min(cStart+chunkSize, total) - 1

				stateMu.Lock()
				already := state.Written[idx]
				stateMu.Unlock()

				var chunkErr error
				for attempt := 0; attempt < 4; attempt++ {
					curStart := cStart + already
					if curStart > cEnd {
						chunkErr = nil
						break
					}
					req, rErr := http.NewRequestWithContext(runCtx, http.MethodGet, sourceURL, nil)
					if rErr != nil {
						chunkErr = rErr
						break
					}
					req.Header.Set("User-Agent", "bgmcat/"+Version)
					req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", curStart, cEnd))

					activeWorkers.Add(1)
					res, doErr := e.httpClient.Do(req)
					if doErr != nil {
						activeWorkers.Add(-1)
						chunkErr = doErr
					} else {
						if res.StatusCode != http.StatusPartialContent {
							_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
							res.Body.Close()
							activeWorkers.Add(-1)
							chunkErr = fmt.Errorf("HTTP 分片状态码异常：%d", res.StatusCode)
						} else if _, gotTotal, crErr := parseContentRangeAt(res.Header.Get("Content-Range"), curStart); crErr != nil || gotTotal != total {
							res.Body.Close()
							activeWorkers.Add(-1)
							if crErr != nil {
								chunkErr = crErr
							} else {
								chunkErr = errors.New("HTTP 分片总长度变化")
							}
							attempt = 4 // Non-retryable protocol violation
						} else {
							wantBytes := cEnd - curStart + 1
							var gotInAttempt int64
							var rwErr error
							for gotInAttempt < wantBytes {
								toRead := int(min(int64(len(buf)), wantBytes-gotInAttempt))
								n, readErr := res.Body.Read(buf[:toRead])
								if n > 0 {
									if rlErr := e.waitHTTPRate(runCtx, n); rlErr != nil {
										rwErr = rlErr
										break
									}
									if _, writeErr := partFile.WriteAt(buf[:n], curStart+gotInAttempt); writeErr != nil {
										rwErr = writeErr
										attempt = 4 // Disk error is fatal
										break
									}
									gotInAttempt += int64(n)
									already += int64(n)
									completedBytes.Add(int64(n))
									stateMu.Lock()
									state.Written[idx] = already
									stateMu.Unlock()
								}
								if readErr != nil {
									if errors.Is(readErr, io.EOF) && gotInAttempt == wantBytes {
										break
									}
									if errors.Is(readErr, io.EOF) {
										rwErr = io.ErrUnexpectedEOF
									} else {
										rwErr = readErr
									}
									break
								}
							}
							res.Body.Close()
							activeWorkers.Add(-1)
							chunkErr = rwErr
						}
					}

					if chunkErr == nil {
						break
					}
					if runCtx.Err() != nil || attempt >= 3 {
						break
					}
					select {
					case <-runCtx.Done():
						chunkErr = runCtx.Err()
					case <-time.After(time.Duration(attempt+1) * 150 * time.Millisecond):
					}
				}
				if chunkErr != nil {
					recordErr(chunkErr)
					return
				}
			}
		}()
	}

	workersDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(workersDone)
	}()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	httpMeter := newSpeedWindow(3 * time.Second)
	httpMeter.Observe(time.Now(), completedBytes.Load())
	ticks := 0

loop:
	for {
		select {
		case <-workersDone:
			break loop
		case <-ticker.C:
			ticks++
			cur := completedBytes.Load()
			speed := httpMeter.Observe(time.Now(), cur)
			if ticks%2 == 0 {
				persistSnapshot()
			}
			if gid != "" {
				_, _ = e.app.Store.Patch("downloads", gid, Obj{
					"totalLength":     total,
					"completedLength": cur,
					"downloadSpeed":   speed,
					"connectionCount": int64(activeWorkers.Load()),
					"files":           filesMeta,
					"filePath":        targetPath,
				})
				e.app.notifyDownloads()
			}
		}
	}

	if ctx.Err() != nil || firstErr != nil {
		_ = partFile.Sync()
		_ = partFile.Close()
		persistSnapshot()
		if gid != "" {
			_, _ = e.app.Store.Patch("downloads", gid, Obj{
				"totalLength":     total,
				"completedLength": completedBytes.Load(),
				"downloadSpeed":   0,
				"connectionCount": 0,
				"files":           filesMeta,
				"filePath":        targetPath,
			})
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return firstErr
	}

	if completedBytes.Load() != total {
		_ = partFile.Sync()
		_ = partFile.Close()
		persistSnapshot()
		return fmt.Errorf("HTTP 分片未全部完成：%d / %d", completedBytes.Load(), total)
	}
	if err = partFile.Sync(); err != nil {
		_ = partFile.Close()
		return err
	}
	if err = partFile.Close(); err != nil {
		return err
	}
	info, err := os.Stat(partPath)
	if err != nil || info.Size() != total {
		return errors.New("HTTP 下载完成后的文件大小校验失败")
	}
	if err = replaceFile(partPath, targetPath); err != nil {
		return err
	}
	_ = os.Remove(statePath)

	if gid != "" {
		_, _ = e.app.Store.Patch("downloads", gid, Obj{
			"totalLength":     total,
			"completedLength": total,
			"downloadSpeed":   0,
			"connectionCount": 0,
			"files":           filesMeta,
			"filePath":        targetPath,
		})
		e.app.notifyDownloads()
	}
	return nil
}

func (a *App) httpDownload(ctx context.Context, j Obj) error {
	return a.engine.Download(ctx, j)
}
