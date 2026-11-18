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
	"golang.org/x/sync/singleflight"
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
	DataDir, Addr, PublicURL     string
	AdminUsername, AdminPassword string
	Desktop                      bool
	Web                          fs.FS
}
type liveTransferProgress struct {
	fileIndex     int
	inFlightBytes int64
	uploadSpeed   int64
}

type App struct {
	discoveryGroup  singleflight.Group
	bgmData         bangumiDataStore
	staticAssets    staticAssetStore
	resourceMu      sync.Mutex
	resourceFlights map[string]*resourceFlight
	Store           *Store
	cfg             Config
	mux             *http.ServeMux
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	http            *http.Client
	refreshMu       sync.Mutex
	enqueueMu       sync.Mutex
	jobsMu          sync.Mutex
	jobs            map[string]context.CancelFunc
	downloadSlots   chan struct{}
	archiveMu       sync.Mutex
	archiveJobs     map[string]context.CancelFunc
	archiveSlots    chan struct{}
	archiveWake     chan struct{}
	liveTransferMu  sync.RWMutex
	liveTransfers   map[string]liveTransferProgress
	dlSubMu         sync.Mutex
	dlSubs          map[chan struct{}]struct{}
	driveNetworkMu  sync.Mutex
	driveScanMu     sync.Mutex
	driveIP         string
	driveTLSKey     string
	driveIPHTTP     *http.Transport
	directRoutes    map[string]*cachedDirectRoute
	driveMu         sync.Mutex
	oauthMu         sync.Mutex
	oauth           map[string]Obj
	mcpMu           sync.Mutex
	mcpSessions     map[string]chan Obj
	lanMu           sync.Mutex
	lanServers      map[string]*lanServerEntry
	tunnel          Tunnel
	engine          *NativeEngine
	media           *Media
	throttle        *authThrottle
	authFailDelay   time.Duration
	bootstrap       AuthBootstrapResult
	start           time.Time
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
	baseTransport := http.DefaultTransport.(*http.Transport).Clone()
	baseTransport.MaxIdleConns = 128
	baseTransport.MaxIdleConnsPerHost = 32
	baseTransport.IdleConnTimeout = 90 * time.Second
	baseTransport.ReadBufferSize = 256 << 10
	baseTransport.WriteBufferSize = 256 << 10
	a := &App{
		Store:           store,
		cfg:             cfg,
		mux:             http.NewServeMux(),
		ctx:             ctx,
		cancel:          cancel,
		jobs:            map[string]context.CancelFunc{},
		downloadSlots:   make(chan struct{}, 6),
		archiveJobs:     map[string]context.CancelFunc{},
		archiveSlots:    make(chan struct{}, 3),
		archiveWake:     make(chan struct{}, 1),
		liveTransfers:   map[string]liveTransferProgress{},
		dlSubs:          map[chan struct{}]struct{}{},
		directRoutes:    map[string]*cachedDirectRoute{},
		oauth:           map[string]Obj{},
		mcpSessions:     map[string]chan Obj{},
		resourceFlights: map[string]*resourceFlight{},
		throttle:        newAuthThrottle(),
		authFailDelay:   defaultAuthFailureDelay,
		start:           time.Now(),
	}
	a.http = &http.Client{Timeout: 45 * time.Second, Transport: &smartDirectRoundTripper{app: a, base: baseTransport}}
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
		s["googleRedirectUri"] = ""
		for _, key := range []string{"gdirectBinaryPath", "gdirectDbPath", "cloudMountDir"} {
			delete(s, key)
		}
		if e = store.Put("config", "settings", s); e != nil {
			store.Close()
			cancel()
			return nil, e
		}
	} else if s, e = backfillSettings(store, cfg.DataDir, s); e != nil {
		store.Close()
		cancel()
		return nil, e
	}
	boot, e := a.BootstrapAdmin(cfg.AdminUsername, cfg.AdminPassword)
	if e != nil {
		store.Close()
		cancel()
		return nil, e
	}
	a.bootstrap = boot
	a.migrateDataDirLayout()
	_ = os.MkdirAll(a.downloadsDir(), 0700)
	a.engine = NewNativeEngine(a)
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

// removedSettings were dropped on purpose; never resurrect them from defaults.json.
var removedSettings = map[string]bool{"gdirectBinaryPath": true, "gdirectDbPath": true, "cloudMountDir": true}

// backfillSettings adds keys introduced after the settings row was first saved.
// updateSettings only accepts keys that already exist, so without this a new
// option (e.g. btTrackers) could never be edited on an upgraded install.
func backfillSettings(store *Store, dataDir string, s Obj) (Obj, error) {
	var base Obj
	if e := json.Unmarshal(defaults, &base); e != nil {
		return s, e
	}
	missing := Obj{}
	for k, v := range base {
		if _, exists := s[k]; !exists && !removedSettings[k] {
			missing[k] = v
		}
	}
	if curClient := str(s["downloadClient"]); curClient == "" || curClient == "aria2" {
		missing["downloadClient"] = "native"
	}
	if strings.TrimSpace(str(s["downloadDir"])) == "" && dataDir != "" {
		missing["downloadDir"] = filepath.Join(dataDir, "downloads")
	}
	// Clear legacy hardcoded loopback /api/cloud/callback default on unconnected installs
	// so Desktop OAuth clients resolve dynamically to http://127.0.0.1:<active_port>.
	if curRedirect := strings.TrimSpace(str(s["googleRedirectUri"])); strings.HasPrefix(curRedirect, "http://127.0.0.1:") && strings.HasSuffix(curRedirect, "/api/cloud/callback") {
		if secret, _ := store.Get("secrets", "google"); secret == nil {
			missing["googleRedirectUri"] = ""
		}
	}
	// Backfill newly added resource providers into resourceProviderOrder
	// and upgrade untouched legacy 1-provider default ("mikan" only with 3-item order).
	if rawOrder, exists := s["resourceProviderOrder"]; exists {
		curOrder := list(rawOrder)
		seen := map[string]bool{}
		mergedOrder := make([]string, 0, len(allResourceProviders))
		for _, item := range curOrder {
			id := str(item)
			if id != "" && !seen[id] {
				seen[id] = true
				mergedOrder = append(mergedOrder, id)
			}
		}
		addedNew := false
		for _, item := range list(base["resourceProviderOrder"]) {
			id := str(item)
			if id != "" && !seen[id] {
				seen[id] = true
				mergedOrder = append(mergedOrder, id)
				addedNew = true
			}
		}
		if addedNew {
			missing["resourceProviderOrder"] = mergedOrder
			curEnabled := list(s["enabledResourceProviders"])
			if len(curOrder) <= 3 && len(curEnabled) == 1 && str(curEnabled[0]) == "mikan" {
				missing["enabledResourceProviders"] = base["enabledResourceProviders"]
			}
		}
	}
	if len(missing) == 0 {
		return s, nil
	}
	return store.Patch("config", "settings", missing)
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
	code := "REQUEST_FAILED"
	if status == 401 {
		code = "AUTH_INVALID"
	} else if status == 429 {
		code = "AUTH_RATE_LIMITED"
	}
	failWithCode(w, status, code, msg)
}
func failWithCode(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Obj{"ok": false, "error": Obj{"code": code, "message": msg, "source": "go", "retryable": status >= 500 || status == 429}})
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
func (a *App) allowedCORSOrigin(origin string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(origin), "/")
	if pub := a.tunnelPublicURL(); pub != "" && strings.EqualFold(pub, trimmed) {
		return true
	}
	for _, item := range list(a.Settings()["corsOrigins"]) {
		if strings.EqualFold(strings.TrimRight(strings.TrimSpace(str(item)), "/"), trimmed) {
			return true
		}
	}
	return false
}
func isPublicAuthRoute(path string) bool {
	switch path {
	case "/api/health",
		"/api/bootstrap",
		"/api/system/status",
		"/api/auth/status",
		"/api/auth/login",
		"/api/auth/token-login",
		"/api/auth/github/start",
		"/api/auth/github/exchange",
		"/api/auth/google/start",
		"/api/auth/oidc/start",
		"/api/auth/oauth/exchange",
		"/api/auth/diana/login",
		"/api/auth/diana/pair",
		"/api/auth/diana/pair/status",
		"/api/auth/diana/pair/claim":
		return true
	}
	return strings.HasPrefix(path, "/api/public/shares/")
}
func (a *App) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			st := r.URL.Query().Get("state")
			if strings.HasPrefix(st, "gdrive:") || strings.HasPrefix(st, "onedrive:") {
				a.serveCloudOAuthCallback(w, r)
				return
			}
		}
		isAPIRoute := strings.HasPrefix(r.URL.Path, "/api/") || (r.URL.Path == "/mcp" && r.Method != "GET") || strings.HasPrefix(r.URL.Path, "/mcp/")
		isCallback := r.URL.Path == "/api/cloud/callback" ||
			r.URL.Path == "/api/auth/github/callback" ||
			r.URL.Path == "/api/auth/google/callback" ||
			r.URL.Path == "/api/auth/oidc/callback"
		if isAPIRoute && !isCallback {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, _ := url.Parse(origin)
				fwdHost := ""
				remoteHost, _, rErr := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
				if rErr != nil {
					remoteHost = strings.TrimSpace(r.RemoteAddr)
				}
				if isLoopbackIPStr(remoteHost) {
					fwdHost = strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
				}
				if u == nil || (!strings.EqualFold(u.Host, r.Host) && (fwdHost == "" || !strings.EqualFold(u.Host, fwdHost)) && !a.allowedCORSOrigin(origin)) {
					fail(w, 403, "不允许跨站访问")
					return
				}
			}
		}
		if isCallback || isPublicAuthRoute(r.URL.Path) {
			if isAPIRoute {
				serveMuxWithAPIGzip(a.mux, w, r)
			} else {
				a.mux.ServeHTTP(w, r)
			}
			return
		}
		if isAPIRoute {
			allowQueryToken := r.URL.Path == "/api/downloads/events" ||
				r.URL.Path == "/api/files/local/content" ||
				r.URL.Path == "/api/files/cloud/content" ||
				r.URL.Path == "/api/manga/image" ||
				(strings.HasPrefix(r.URL.Path, "/api/downloads/") && (strings.HasSuffix(r.URL.Path, "/stream") || strings.Contains(r.URL.Path, "/hls/") || strings.Contains(r.URL.Path, "/subtitles/") || strings.HasSuffix(r.URL.Path, "/thumbnail")))
			if authed, _ := a.authenticateRequest(r, allowQueryToken); !authed {
				fail(w, 401, "未登录或会话已过期，请重新登录")
				return
			}
			serveMuxWithAPIGzip(a.mux, w, r)
			return
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
	a.resumeJobs()
	a.syncLANListeners()
	go a.warmBangumiDataIndex()
	if flag(a.Settings()["tunnelEnabled"]) {
		if e := a.startTunnel(); e != nil {
			a.Log("warn", e.Error())
		}
	}
	for _, work := range []func(){a.pollSubscriptions, a.housekeeping} {
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
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-a.archiveWake:
				a.archiveCompleted()
			case <-ticker.C:
				a.archiveCompleted()
			}
		}
	}()
	a.triggerArchive()
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
	_, _ = a.Store.db.Exec("DELETE FROM ids WHERE id < (SELECT MAX(id) FROM ids)")
	a.pruneExpiredSessions()
	a.pruneOldExpiredShares()
}

func (a *App) pruneExpiredSessions() {
	a.Store.mu.Lock()
	rows, err := a.Store.db.Query("SELECT key,value FROM documents WHERE kind='sessions'")
	if err != nil {
		a.Store.mu.Unlock()
		return
	}
	var expired []string
	nowUnix := time.Now().Unix()
	for rows.Next() {
		var key, raw string
		if rows.Scan(&key, &raw) == nil {
			var s Obj
			if json.Unmarshal([]byte(raw), &s) == nil && num(s["expires"]) <= nowUnix {
				expired = append(expired, key)
			}
		}
	}
	rows.Close()
	a.Store.mu.Unlock()
	for _, key := range expired {
		_ = a.Store.Delete("sessions", key)
	}
	if flag(a.Settings()["lanAccessEnabled"]) {
		a.syncLANListeners()
	}
}
func (a *App) Close() error {
	a.cancel()
	a.closeLANListeners()
	a.tunnel.stop()
	a.jobsMu.Lock()
	for _, cancel := range a.jobs {
		cancel()
	}
	a.jobsMu.Unlock()
	a.archiveMu.Lock()
	for _, cancel := range a.archiveJobs {
		cancel()
	}
	a.archiveMu.Unlock()
	a.wg.Wait()
	a.media.Close()
	a.engine.Close()
	a.driveNetworkMu.Lock()
	if a.driveIPHTTP != nil {
		a.driveIPHTTP.CloseIdleConnections()
	}
	a.driveNetworkMu.Unlock()
	a.closeDirectRoutes()
	return a.Store.Close()
}
func (a *App) status() Obj {
	s := a.Settings()
	host, port, _ := net.SplitHostPort(a.cfg.Addr)
	p, _ := strconv.Atoi(port)
	t := a.tunnelStatus()
	client := str(s["downloadClient"])
	qbit := false
	if client == "qbittorrent" {
		qbit = a.qbitOnline()
	}
	clientOnline := a.downloadClientOnline(client)
	cloudProv := a.activeCloudProvider()
	lanURLs := a.lanURLs()
	return Obj{
		"appVersion":             Version,
		"activeHost":             host,
		"activePort":             p,
		"configuredHost":         host,
		"configuredPort":         p,
		"lanAccessEnabled":       flag(s["lanAccessEnabled"]) || isWildcardBindHost(host),
		"lanUrls":                lanURLs,
		"apiBaseUrl":             a.cfg.PublicURL,
		"mcpSseUrl":              a.cfg.PublicURL + "/mcp/sse",
		"tokenHeaderName":        "Authorization",
		"tokenConfigured":        true,
		"localAuthBypassEnabled": false,
		"aria2Online":            a.engine.online(),
		"qbittorrentOnline":      qbit,
		"downloadClient":         client,
		"downloadClientOnline":   clientOnline,
		"downloadClientName":     downloadClientDisplayName(client),
		"cloudProvider":          cloudProv,
		"cloudProviderName":      cloudProviderDisplayName(cloudProv),
		"cloudConnected":         a.cloudProviderConnected(cloudProv),
		"rssPollingEnabled":      true,
		"restartRequired":        false,
		"tunnelEnabled":          t["enabled"],
		"tunnelState":            t["state"],
		"tunnelPublicUrl":        t["publicUrl"],
		"publicExposureActive":   str(t["state"]) == "online",
	}
}
func (a *App) routes() {
	a.handle("GET /api/health", func(w http.ResponseWriter, r *http.Request) error {
		ok(w, Obj{"status": "ok", "runtime": "go", "version": Version})
		return nil
	})
	a.handle("GET /api/bootstrap", func(w http.ResponseWriter, r *http.Request) error {
		ok(w, a.buildBootstrapPayload(r))
		return nil
	})
	a.handle("GET /api/system/status", func(w http.ResponseWriter, r *http.Request) error {
		resp := Obj{"ok": true, "appVersion": Version}
		for k, v := range a.publicAuthFlags() {
			resp[k] = v
		}
		ok(w, resp)
		return nil
	})
	a.handle("GET /api/system/status/full", func(w http.ResponseWriter, r *http.Request) error { ok(w, a.status()); return nil })
	a.registerStorageRoutes()
	a.handle("POST /api/auth/token-login", func(w http.ResponseWriter, r *http.Request) error {
		ip := a.clientIP(r)
		if wait := a.throttle.check(ip); wait > 0 {
			writeRateLimited(w, wait)
			return nil
		}
		o, e := body(w, r)
		if e != nil {
			return e
		}
		if !a.validToken(str(o["token"])) {
			a.throttle.recordFailure(ip)
			a.sleepOnAuthFailure()
			fail(w, 401, "口令错误")
			return nil
		}
		a.throttle.recordSuccess(ip)
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
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 2000 {
			limit = 200
		}
		o, e := a.Store.ListLimit("logs", limit)
		if e == nil {
			ok(w, o)
		}
		return e
	})
	a.handle("GET /api/logs/file-info", func(w http.ResponseWriter, r *http.Request) error { ok(w, Obj{"path": a.Store.Path}); return nil })
	a.subscriptionRoutes()
	a.downloadRoutes()
	a.registerEngineRoutes()
	a.fileRoutes()
	a.shareRoutes()
	a.discoveryRoutes()
	a.mangaRoutes()
	a.cloudRoutes()
	a.driveNetworkRoutes()
	a.networkProbeRoutes()
	a.integrationRoutes()
	a.backupRoutes()
	a.authRoutes()
	a.remoteRoutes()
	a.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "接口不存在") })
	a.mux.HandleFunc("/", a.serveWebAsset)
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
		case map[string]any:
			m, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("%s 必须是键值映射", k)
			}
			cleaned := map[string]any{}
			for mk, mv := range m {
				sv, isStr := mv.(string)
				if !isStr {
					return fmt.Errorf("%s.%s 必须是字符串", k, mk)
				}
				sv = strings.TrimSpace(sv)
				if k == "providerMirrors" && sv != "" {
					sv = strings.TrimRight(sv, "/")
					if !httpURL(sv) {
						return fmt.Errorf("镜像地址无效：%s（需以 http:// 或 https:// 开头）", sv)
					}
				}
				if sv != "" {
					cleaned[mk] = sv
				}
			}
			o[k] = cleaned
		}
		if k == "token" && len(str(v)) < 24 {
			return errors.New("口令至少 24 位")
		}
		if k == "downloadClient" && !validDownloadClient(str(v)) {
			return errors.New("请选择受支持的下载引擎")
		}
		if k == "cloudProvider" && !validCloudProvider(str(v)) {
			return errors.New("请选择受支持的云盘服务")
		}
		if k == "pollIntervalMinutes" && (num(v) < 1 || num(v) > 1440) {
			return errors.New("轮询间隔为 1–1440 分钟")
		}
		if k == "defaultSaveTo" && str(v) != "local" && str(v) != "cloud" {
			return errors.New("保存位置只能是本机或云盘")
		}
		if k == "downloadDir" && !filepath.IsAbs(str(v)) {
			return errors.New("下载目录必须是绝对路径")
		}
		if k == "dianaAuthBaseUrl" && strings.TrimSpace(str(v)) != "" && !httpURL(strings.TrimSpace(str(v))) {
			return errors.New("Diana 服务地址无效（需以 http:// 或 https:// 开头）")
		}
		if k == "oidcIssuerUrl" && strings.TrimSpace(str(v)) != "" && !httpURL(strings.TrimSpace(str(v))) {
			return errors.New("OIDC Issuer 地址无效（需以 https:// 或 http:// 开头）")
		}
		if k == "btTrackers" {
			for _, tracker := range list(v) {
				if !validTracker(str(tracker)) {
					return fmt.Errorf("Tracker 地址无效：%s（需以 udp://、http://、https:// 或 wss:// 开头）", str(tracker))
				}
			}
		}
	}
	if o["providerMirrors"] != nil && o["mikanBaseUrl"] == nil {
		mirrors := object(o["providerMirrors"])
		if mikanMirror := strings.TrimSpace(str(mirrors["mikan"])); mikanMirror != "" {
			o["mikanBaseUrl"] = mikanMirror
		} else {
			o["mikanBaseUrl"] = "https://mikanani.me"
		}
	} else if o["mikanBaseUrl"] != nil && o["providerMirrors"] == nil {
		curMirrors := clone(object(s["providerMirrors"]))
		if curMirrors == nil {
			curMirrors = Obj{}
		}
		mb := strings.TrimRight(strings.TrimSpace(str(o["mikanBaseUrl"])), "/")
		if mb == "" || mb == "https://mikanani.me" || mb == "https://mikanime.tv" {
			delete(curMirrors, "mikan")
		} else {
			curMirrors["mikan"] = mb
		}
		o["providerMirrors"] = curMirrors
	}
	s, e = a.Store.Patch("config", "settings", o)
	if e == nil {
		if o["lanAccessEnabled"] != nil {
			a.syncLANListeners()
		}
		if o["cloudArchiveEnabled"] != nil || o["cloudDeleteLocal"] != nil || o["cloudDriveFolderId"] != nil || o["cloudProvider"] != nil || o["onedriveFolderId"] != nil || o["webdavRootPath"] != nil {
			a.triggerArchive()
		}
		ok(w, a.Settings())
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
	req.Header.Set("User-Agent", "bgmcat/"+Version)
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
