package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/SuInk/bgmcat/internal/version"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed defaults.json
var defaults []byte

var Version = version.Current()

type Config struct {
	DataDir, Addr, PublicURL string
	Web                      fs.FS
}
type App struct {
	Store          *Store
	cfg            Config
	mux            *http.ServeMux
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	http           *http.Client
	refreshMu      sync.Mutex
	enqueueMu      sync.Mutex
	jobsMu         sync.Mutex
	jobs           map[string]context.CancelFunc
	downloadSlots  chan struct{}
	driveNetworkMu sync.Mutex
	driveScanMu    sync.Mutex
	driveIP        string
	driveIPHTTP    *http.Transport
	driveMu        sync.Mutex
	oauthMu        sync.Mutex
	oauth          map[string]Obj
	mcpMu          sync.Mutex
	mcpSessions    map[string]chan Obj
	tunnel         Tunnel
	aria2          *Aria2Engine
	media          *Media
	start          time.Time
}

func New(cfg Config) (*App, error) {
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}
	abs, e := filepath.Abs(cfg.DataDir)
	if e != nil {
		return nil, e
	}
	cfg.DataDir = abs
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:3001"
	}
	if cfg.PublicURL == "" {
		cfg.PublicURL = "http://" + cfg.Addr
	}
	store, e := OpenStore(filepath.Join(cfg.DataDir, "app.db"))
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{Store: store, cfg: cfg, mux: http.NewServeMux(), ctx: ctx, cancel: cancel, http: &http.Client{Timeout: 45 * time.Second}, jobs: map[string]context.CancelFunc{}, downloadSlots: make(chan struct{}, 4), oauth: map[string]Obj{}, mcpSessions: map[string]chan Obj{}, start: time.Now()}
	s, e := store.Get("config", "settings")
	if e != nil {
		store.Close()
		cancel()
		return nil, e
	}
	if s == nil {
		_ = json.Unmarshal(defaults, &s)
		s["token"] = randomID(24)
		s["downloadDir"] = filepath.Join(cfg.DataDir, "downloads")
		s["downloadClient"] = "native"
		s["aria2Enabled"] = false
		s["cloudArchiveEnabled"] = false
		s["cloudDeleteLocal"] = false
		s["googleClientId"] = ""
		s["googleClientSecret"] = ""
		s["googleRedirectUri"] = cfg.PublicURL + "/api/cloud/callback"
		for _, key := range []string{"gdirectBinaryPath", "gdirectDbPath", "cloudMountDir"} {
			delete(s, key)
		}
		if e = store.Put("config", "settings", s); e != nil {
			store.Close()
			cancel()
			return nil, e
		}
	}
	_ = os.MkdirAll(str(s["downloadDir"]), 0700)
	a.aria2 = &Aria2Engine{app: a}
	a.media = NewMedia(a)
	a.routes()
	return a, nil
}
func randomID(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func (a *App) Settings() Obj {
	s, e := a.Store.Get("config", "settings")
	if e != nil {
		log.Printf("settings: %v", e)
	}
	if s == nil {
		return Obj{}
	}
	if _, exists := s["dianaMcpConfigPath"]; !exists {
		s["dianaMcpConfigPath"] = ""
	}
	host, port, _ := net.SplitHostPort(a.cfg.Addr)
	s["bindHost"] = host
	s["port"], _ = strconv.Atoi(port)
	s["lockedSettings"] = []string{"bindHost", "port"}
	return s
}
func (a *App) Log(level, msg string) {
	id, e := a.Store.NextID()
	if e != nil {
		return
	}
	_ = a.Store.Put("logs", strconv.FormatInt(id, 10), Obj{"id": id, "level": level, "message": msg, "createdAt": now()})
	log.Printf("[%s] %s", level, msg)
}
func ok(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(Obj{"ok": true, "data": v})
}
func fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	code := "REQUEST_FAILED"
	if status == 401 {
		code = "AUTH_INVALID"
	}
	_ = json.NewEncoder(w).Encode(Obj{"ok": false, "error": Obj{"code": code, "message": msg, "source": "go", "retryable": status >= 500}})
}
func body(w http.ResponseWriter, r *http.Request) (Obj, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var o Obj
	e := json.NewDecoder(r.Body).Decode(&o)
	if e != nil || o == nil {
		return nil, errors.New("无效 JSON 请求")
	}
	return o, nil
}
func (a *App) handle(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
	a.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if e := fn(w, r); e != nil {
			fail(w, 400, e.Error())
		}
	})
}
func (a *App) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.URL.Path == "/api/health" || r.URL.Path == "/api/system/status" || r.URL.Path == "/api/auth/token-login" || r.URL.Path == "/api/auth/github/start" || r.URL.Path == "/api/auth/github/exchange" || r.URL.Path == "/api/cloud/callback" || r.URL.Path == "/api/auth/github/callback" {
			a.mux.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || (r.URL.Path == "/mcp" && r.Method != "GET") || strings.HasPrefix(r.URL.Path, "/mcp/") {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token == "" && strings.HasPrefix(r.URL.Path, "/api/downloads/") && (strings.HasSuffix(r.URL.Path, "/stream") || strings.Contains(r.URL.Path, "/hls/") || strings.Contains(r.URL.Path, "/subtitles/") || strings.HasSuffix(r.URL.Path, "/thumbnail")) {
				token = r.URL.Query().Get("token")
			}
			if !a.validToken(token) {
				fail(w, 401, "需要有效的访问口令")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, _ := url.Parse(origin)
				if u == nil || !strings.EqualFold(u.Host, r.Host) {
					fail(w, 403, "不允许跨站访问")
					return
				}
			}
		}
		a.mux.ServeHTTP(w, r)
	})
}
func (a *App) validToken(token string) bool {
	if token == "" {
		return false
	}
	expected := str(a.Settings()["token"])
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1 || a.validSession(token)
}
func (a *App) Start() {
	if str(a.Settings()["downloadClient"]) != "qbittorrent" {
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			if _, _, err := a.aria2.ensure(a.ctx); err != nil && a.ctx.Err() == nil {
				a.Log("warn", err.Error())
			}
		}()
	}
	a.resumeJobs()
	if flag(a.Settings()["tunnelEnabled"]) {
		if e := a.startTunnel(); e != nil {
			a.Log("warn", e.Error())
		}
	}
	for _, work := range []func(){a.pollSubscriptions, a.archiveCompleted, a.housekeeping} {
		a.wg.Add(1)
		go func(work func()) {
			defer a.wg.Done()
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-a.ctx.Done():
					return
				case <-ticker.C:
					work()
				}
			}
		}(work)
	}
}
func (a *App) housekeeping() {
	a.media.Prune()
	a.oauthMu.Lock()
	for k, v := range a.oauth {
		if num(v["expires"]) < time.Now().Unix() {
			delete(a.oauth, k)
		}
	}
	a.oauthMu.Unlock()
	_, _ = a.Store.db.Exec("DELETE FROM documents WHERE kind='logs' AND rowid NOT IN (SELECT rowid FROM documents WHERE kind='logs' ORDER BY rowid DESC LIMIT 2000)")
}
func (a *App) Close() error {
	a.cancel()
	a.tunnel.stop()
	a.jobsMu.Lock()
	for _, cancel := range a.jobs {
		cancel()
	}
	a.jobsMu.Unlock()
	a.wg.Wait()
	a.media.Close()
	a.aria2.Close()
	a.driveNetworkMu.Lock()
	if a.driveIPHTTP != nil {
		a.driveIPHTTP.CloseIdleConnections()
	}
	a.driveNetworkMu.Unlock()
	return a.Store.Close()
}
func (a *App) status() Obj {
	s := a.Settings()
	host, port, _ := net.SplitHostPort(a.cfg.Addr)
	p, _ := strconv.Atoi(port)
	t := a.tunnelStatus()
	qbit := false
	if str(s["downloadClient"]) == "qbittorrent" {
		qbit = a.qbitOnline()
	}
	return Obj{"appVersion": Version, "activeHost": host, "activePort": p, "configuredHost": host, "configuredPort": p, "apiBaseUrl": a.cfg.PublicURL, "mcpSseUrl": a.cfg.PublicURL + "/mcp/sse", "tokenHeaderName": "Authorization", "tokenConfigured": true, "localAuthBypassEnabled": false, "aria2Online": a.aria2.online(), "qbittorrentOnline": qbit, "downloadClient": s["downloadClient"], "rssPollingEnabled": true, "restartRequired": false, "tunnelEnabled": t["enabled"], "tunnelState": t["state"], "tunnelPublicUrl": t["publicUrl"], "publicExposureActive": str(t["state"]) == "online"}
}
func (a *App) routes() {
	a.handle("GET /api/health", func(w http.ResponseWriter, r *http.Request) error {
		ok(w, Obj{"status": "ok", "runtime": "go", "version": Version})
		return nil
	})
	a.handle("GET /api/system/status", func(w http.ResponseWriter, r *http.Request) error {
		ok(w, Obj{"ok": true, "appVersion": Version, "githubOAuthAvailable": flag(a.Settings()["githubOAuthEnabled"])})
		return nil
	})
	a.handle("GET /api/system/status/full", func(w http.ResponseWriter, r *http.Request) error { ok(w, a.status()); return nil })
	a.handle("POST /api/auth/token-login", func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		if !a.validToken(str(o["token"])) {
			time.Sleep(300 * time.Millisecond)
			fail(w, 401, "口令错误")
			return nil
		}
		ok(w, a.status())
		return nil
	})
	a.handle("GET /api/settings", func(w http.ResponseWriter, r *http.Request) error { ok(w, a.Settings()); return nil })
	a.handle("PUT /api/settings", a.updateSettings)
	a.handle("POST /api/settings/rotate-token", func(w http.ResponseWriter, r *http.Request) error {
		s, e := a.Store.Patch("config", "settings", Obj{"token": randomID(24)})
		if e == nil {
			ok(w, s)
		}
		return e
	})
	a.handle("GET /api/logs/recent", func(w http.ResponseWriter, r *http.Request) error {
		o, e := a.Store.List("logs")
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 2000 {
			limit = 200
		}
		if len(o) > limit {
			o = o[:limit]
		}
		if e == nil {
			ok(w, o)
		}
		return e
	})
	a.handle("GET /api/logs/file-info", func(w http.ResponseWriter, r *http.Request) error { ok(w, Obj{"path": a.Store.Path}); return nil })
	a.subscriptionRoutes()
	a.downloadRoutes()
	a.discoveryRoutes()
	a.cloudRoutes()
	a.driveNetworkRoutes()
	a.integrationRoutes()
	a.backupRoutes()
	a.authRoutes()
	a.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "接口不存在") })
	a.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.Web == nil {
			http.Error(w, "frontend not built", 503)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		b, e := fs.ReadFile(a.cfg.Web, path)
		if e != nil {
			b, e = fs.ReadFile(a.cfg.Web, "index.html")
			path = "index.html"
		}
		if e != nil {
			http.Error(w, "frontend missing", 503)
			return
		}
		if strings.HasSuffix(path, ".html") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, path, time.Time{}, strings.NewReader(string(b)))
	})
}
func (a *App) updateSettings(w http.ResponseWriter, r *http.Request) error {
	o, e := body(w, r)
	if e != nil {
		return e
	}
	s := a.Settings()
	for k, v := range o {
		if k == "bindHost" || k == "port" || k == "lockedSettings" {
			delete(o, k)
			continue
		}
		old, exists := s[k]
		if !exists {
			delete(o, k)
			continue
		}
		switch old.(type) {
		case bool:
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("%s 必须是布尔值", k)
			}
		case string:
			if _, ok := v.(string); !ok {
				return fmt.Errorf("%s 必须是字符串", k)
			}
		case float64:
			if n, ok := v.(float64); !ok || n != float64(int64(n)) {
				return fmt.Errorf("%s 必须是整数", k)
			}
		case []any:
			values, ok := v.([]any)
			if !ok {
				return fmt.Errorf("%s 必须是列表", k)
			}
			for _, x := range values {
				if _, ok := x.(string); !ok {
					return fmt.Errorf("%s 必须是字符串列表", k)
				}
			}
		}
		if k == "token" && len(str(v)) < 24 {
			return errors.New("口令至少 24 位")
		}
		if k == "downloadClient" && str(v) != "native" && str(v) != "qbittorrent" {
			return errors.New("请选择托管 aria2 下载器或 qBittorrent")
		}
		if k == "pollIntervalMinutes" && (num(v) < 1 || num(v) > 1440) {
			return errors.New("轮询间隔为 1–1440 分钟")
		}
		if k == "downloadDir" && !filepath.IsAbs(str(v)) {
			return errors.New("下载目录必须是绝对路径")
		}
	}
	s, e = a.Store.Patch("config", "settings", o)
	if e == nil {
		ok(w, s)
	}
	return e
}
func (a *App) requestJSON(ctx context.Context, method, endpoint string, payload any, headers map[string]string) (Obj, error) {
	var input io.Reader
	if payload != nil {
		b, e := json.Marshal(payload)
		if e != nil {
			return nil, e
		}
		input = strings.NewReader(string(b))
	}
	req, e := http.NewRequestWithContext(ctx, method, endpoint, input)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "AnimeRSS-Go/"+Version)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, e := a.http.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("上游 HTTP %d", res.StatusCode)
	}
	var result Obj
	e = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&result)
	return result, e
}
