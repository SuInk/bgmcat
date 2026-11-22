package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
	"golang.org/x/time/rate"
)

// NativeEngine is bgmcat's built-in pure-Go download engine:
//   - HTTP/HTTPS: 16-connection adaptive segmented Range downloader with
//     crash/pause-safe bitmap state (.bgmcat-dl) and single-stream fallback.
//   - BitTorrent/Magnet: anacrolix/torrent with persistent BoltDB piece state,
//     DHT + uTP + TCP + PEX + WebTorrent, parallel multi-tracker announcing,
//     fair reciprocal upload during download, and progressive piece-priority
//     streaming (边下边播) on active tasks.
type NativeEngine struct {
	app *App

	mu       sync.RWMutex
	closed   bool
	activeBT map[string]*activeBTStream

	seedMu    sync.Mutex
	seedTasks map[string]*btSeedTask

	httpTransport *http.Transport
	httpClient    *http.Client
	httpLimiter   *rate.Limiter

	btInitMu  sync.Mutex
	btClient  *torrent.Client
	btLimiter *rate.Limiter

	rateLimitBps atomic.Int64
}

func NewNativeEngine(a *App) *NativeEngine {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyFromEnvironment
	tr.MaxIdleConns = 256
	tr.MaxIdleConnsPerHost = 64
	tr.MaxConnsPerHost = 0
	tr.IdleConnTimeout = 90 * time.Second
	tr.TLSHandshakeTimeout = 10 * time.Second
	tr.ResponseHeaderTimeout = 25 * time.Second
	tr.ReadBufferSize = 256 << 10
	tr.WriteBufferSize = 64 << 10
	tr.DisableCompression = true

	return &NativeEngine{
		app:           a,
		activeBT:      map[string]*activeBTStream{},
		seedTasks:     map[string]*btSeedTask{},
		httpTransport: tr,
		httpClient:    &http.Client{Transport: tr},
		httpLimiter:   rate.NewLimiter(rate.Inf, 1<<20),
		btLimiter:     rate.NewLimiter(rate.Inf, 1<<20),
	}
}

func (e *NativeEngine) online() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return !e.closed
}

func (e *NativeEngine) isClosed() bool {
	return !e.online()
}

// SetDownloadRateLimit sets an overall download rate cap in bytes/sec across
// HTTP and BitTorrent tasks (<= 0 means unlimited).
func (e *NativeEngine) SetDownloadRateLimit(bytesPerSec int64) {
	e.rateLimitBps.Store(bytesPerSec)
	if bytesPerSec <= 0 {
		e.httpLimiter.SetLimit(rate.Inf)
		e.httpLimiter.SetBurst(1 << 20)
		e.btLimiter.SetLimit(rate.Inf)
		e.btLimiter.SetBurst(1 << 20)
		return
	}
	burst := int(max(bytesPerSec/2, 64<<10))
	if burst > 1<<20 {
		burst = 1 << 20
	}
	e.httpLimiter.SetBurst(burst)
	e.httpLimiter.SetLimit(rate.Limit(bytesPerSec))
	e.btLimiter.SetBurst(burst)
	e.btLimiter.SetLimit(rate.Limit(bytesPerSec))
}

type speedSample struct {
	at    time.Time
	bytes int64
}

// speedWindow computes a smooth transfer rate (bytes/sec) over a rolling window
// (default 3s) combined with EWMA damping so 200-250ms network bursts or brief
// scheduling gaps never make speed or ETA jump wildly in the UI.
type speedWindow struct {
	span     time.Duration
	samples  []speedSample
	smoothed float64
}

func newSpeedWindow(span time.Duration) *speedWindow {
	if span <= 0 {
		span = 3 * time.Second
	}
	return &speedWindow{
		span:    span,
		samples: make([]speedSample, 0, 24),
	}
}

func (w *speedWindow) Observe(now time.Time, cumulativeBytes int64) int64 {
	if len(w.samples) == 0 {
		w.samples = append(w.samples, speedSample{at: now, bytes: cumulativeBytes})
		return 0
	}
	cutoff := now.Add(-w.span)
	trim := 0
	for trim+1 < len(w.samples) && w.samples[trim+1].at.Before(cutoff) {
		trim++
	}
	if trim > 0 {
		w.samples = append(w.samples[:0], w.samples[trim:]...)
	}
	w.samples = append(w.samples, speedSample{at: now, bytes: cumulativeBytes})

	anchor := w.samples[0]
	dt := now.Sub(anchor.at).Seconds()
	if dt < 0.15 {
		return int64(w.smoothed)
	}
	delta := max(cumulativeBytes-anchor.bytes, 0)
	windowRate := float64(delta) / dt
	if delta == 0 && dt >= w.span.Seconds() {
		w.smoothed *= 0.45
		if w.smoothed < 512 {
			w.smoothed = 0
		}
		return int64(w.smoothed)
	}
	if w.smoothed <= 0 {
		w.smoothed = windowRate
	} else {
		w.smoothed = w.smoothed*0.75 + windowRate*0.25
	}
	return int64(w.smoothed)
}

func (e *NativeEngine) waitHTTPRate(ctx context.Context, n int) error {
	if n <= 0 || e.rateLimitBps.Load() <= 0 {
		return nil
	}
	burst := e.httpLimiter.Burst()
	for n > 0 {
		step := min(n, burst)
		if err := e.httpLimiter.WaitN(ctx, step); err != nil {
			return err
		}
		n -= step
	}
	return nil
}

func (e *NativeEngine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	e.activeBT = map[string]*activeBTStream{}
	e.mu.Unlock()

	e.StopAllSeeding()

	e.btInitMu.Lock()
	btClient := e.btClient
	e.btClient = nil
	e.btInitMu.Unlock()

	if btClient != nil {
		btClient.Close()
	}
	if e.httpTransport != nil {
		e.httpTransport.CloseIdleConnections()
	}
}

func (e *NativeEngine) Download(ctx context.Context, j Obj) error {
	if e.isClosed() || e.app.ctx.Err() != nil {
		return errors.New("下载服务已停止")
	}
	source := strings.TrimSpace(str(j["sourceUrl"]))
	if source == "" {
		return errors.New("下载链接不能为空")
	}
	kind := strings.TrimSpace(str(j["sourceType"]))
	if kind == "" {
		kind = inferSource(source)
	}
	switch kind {
	case "magnet", "torrent-url":
		return e.downloadBT(ctx, j)
	case "direct":
		return e.downloadHTTP(ctx, j)
	default:
		if strings.HasPrefix(source, "magnet:?") {
			return e.downloadBT(ctx, j)
		}
		return e.downloadHTTP(ctx, j)
	}
}

func (e *NativeEngine) CanStream(gid string) bool {
	if gid == "" {
		return false
	}
	e.mu.RLock()
	s := e.activeBT[gid]
	e.mu.RUnlock()
	return s != nil && s.ready.Load()
}

func (e *NativeEngine) OpenStream(ctx context.Context, gid, targetPath string) (io.ReadSeekCloser, time.Time, bool) {
	if gid == "" {
		return nil, time.Time{}, false
	}
	e.mu.RLock()
	s := e.activeBT[gid]
	e.mu.RUnlock()
	if s == nil || s.mediaFile == nil {
		return nil, time.Time{}, false
	}
	if targetPath != "" && s.mediaPath != "" && filepath.Clean(targetPath) != filepath.Clean(s.mediaPath) {
		return nil, time.Time{}, false
	}
	rc, ok := s.newReader(ctx)
	if !ok {
		return nil, time.Time{}, false
	}
	return rc, s.startTime, true
}

func validTracker(raw string) bool {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "udp", "http", "https", "wss":
		return true
	}
	return false
}

// btTrackerOption turns the btTrackers setting into a comma-separated list:
// trimmed, valid, de-duplicated, in the user's order.
func btTrackerOption(value any) string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range list(value) {
		tracker := strings.TrimSpace(str(v))
		if tracker == "" || seen[tracker] || !validTracker(tracker) {
			continue
		}
		seen[tracker] = true
		out = append(out, tracker)
	}
	return strings.Join(out, ",")
}

func hasLoopbackTracker(trackers []string) bool {
	for _, raw := range trackers {
		if u, err := url.Parse(strings.TrimSpace(raw)); err == nil {
			host := u.Hostname()
			if host == "127.0.0.1" || host == "localhost" || host == "::1" {
				return true
			}
		}
	}
	return false
}

func cachedTorrent(dir, expectedHash string) []byte {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".torrent") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err == nil && len(data) > 0 && len(data) <= 16<<20 {
			if hash, _, parseErr := torrentTrackers(data); parseErr == nil {
				if expectedHash == "" || strings.EqualFold(hash.HexString(), expectedHash) {
					return data
				}
			}
		}
	}
	return nil
}
