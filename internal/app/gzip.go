package app

import (
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var apiGzipWriterPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		return w
	},
}

type cachedStaticAsset struct {
	raw         []byte
	gz          []byte
	contentType string
	etag        string
}

type staticAssetStore struct {
	mu    sync.RWMutex
	items map[string]*cachedStaticAsset
}

func acceptsGzip(r *http.Request) bool {
	if r.Method == http.MethodHead || r.Header.Get("Range") != "" {
		return false
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if strings.EqualFold(enc, "gzip") {
			if strings.Contains(part, "q=0") && !strings.Contains(part, "q=0.") {
				return false
			}
			return true
		}
	}
	return false
}

func shouldBypassAPIGzip(path string) bool {
	if path == "/api/downloads/events" ||
		path == "/api/files/local/content" ||
		path == "/api/files/cloud/content" ||
		path == "/api/manga/image" ||
		strings.HasPrefix(path, "/mcp") {
		return true
	}
	if strings.HasPrefix(path, "/api/downloads/") &&
		(strings.HasSuffix(path, "/stream") ||
			strings.Contains(path, "/hls/") ||
			strings.Contains(path, "/subtitles/") ||
			strings.HasSuffix(path, "/thumbnail")) {
		return true
	}
	if strings.HasPrefix(path, "/api/public/shares/") &&
		(strings.Contains(path, "/stream") ||
			strings.Contains(path, "/hls/") ||
			strings.Contains(path, "/subtitles/")) {
		return true
	}
	return false
}

type apiGzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
	statusCode  int
	useGzip     bool
}

func (w *apiGzipResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = code
	ct := w.Header().Get("Content-Type")
	if code != http.StatusNoContent &&
		code != http.StatusNotModified &&
		w.Header().Get("Content-Encoding") == "" &&
		!strings.HasPrefix(ct, "text/event-stream") &&
		(strings.Contains(ct, "application/json") || strings.HasPrefix(ct, "text/")) {
		w.useGzip = true
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		gz := apiGzipWriterPool.Get().(*gzip.Writer)
		gz.Reset(w.ResponseWriter)
		w.gz = gz
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *apiGzipResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.useGzip && w.gz != nil {
		return w.gz.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *apiGzipResponseWriter) Flush() {
	if w.useGzip && w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *apiGzipResponseWriter) closeGzip() {
	if w.gz != nil {
		_ = w.gz.Close()
		apiGzipWriterPool.Put(w.gz)
		w.gz = nil
	}
}

func serveMuxWithAPIGzip(mux *http.ServeMux, w http.ResponseWriter, r *http.Request) {
	if !acceptsGzip(r) || shouldBypassAPIGzip(r.URL.Path) {
		mux.ServeHTTP(w, r)
		return
	}
	gw := &apiGzipResponseWriter{ResponseWriter: w}
	defer gw.closeGzip()
	mux.ServeHTTP(gw, r)
}

func isCompressibleAssetExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".html", ".js", ".mjs", ".css", ".json", ".svg", ".webmanifest", ".txt", ".xml":
		return true
	default:
		return false
	}
}

func detectAssetContentType(path string, raw []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".webmanifest":
		return "application/manifest+json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return http.DetectContentType(raw)
}

func (a *App) loadStaticAsset(path string) (*cachedStaticAsset, string, error) {
	a.staticAssets.mu.RLock()
	if a.staticAssets.items != nil {
		if existing, ok := a.staticAssets.items[path]; ok {
			a.staticAssets.mu.RUnlock()
			return existing, path, nil
		}
	}
	a.staticAssets.mu.RUnlock()

	raw, err := fs.ReadFile(a.cfg.Web, path)
	resolvedPath := path
	if err != nil {
		raw, err = fs.ReadFile(a.cfg.Web, "index.html")
		resolvedPath = "index.html"
		if err != nil {
			return nil, "", err
		}
		a.staticAssets.mu.RLock()
		if a.staticAssets.items != nil {
			if existing, ok := a.staticAssets.items["index.html"]; ok {
				a.staticAssets.mu.RUnlock()
				return existing, "index.html", nil
			}
		}
		a.staticAssets.mu.RUnlock()
	}

	sum := sha1.Sum(raw)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	ct := detectAssetContentType(resolvedPath, raw)

	var gzBytes []byte
	if len(raw) >= 256 && isCompressibleAssetExt(filepath.Ext(resolvedPath)) {
		var buf bytes.Buffer
		if zw, zErr := gzip.NewWriterLevel(&buf, gzip.DefaultCompression); zErr == nil {
			_, _ = zw.Write(raw)
			if zw.Close() == nil && buf.Len() < len(raw) {
				gzBytes = buf.Bytes()
			}
		}
	}

	entry := &cachedStaticAsset{
		raw:         raw,
		gz:          gzBytes,
		contentType: ct,
		etag:        etag,
	}

	a.staticAssets.mu.Lock()
	if a.staticAssets.items == nil {
		a.staticAssets.items = map[string]*cachedStaticAsset{}
	}
	a.staticAssets.items[resolvedPath] = entry
	a.staticAssets.mu.Unlock()
	return entry, resolvedPath, nil
}

func (a *App) serveWebAsset(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Web == nil {
		http.Error(w, "frontend not built", http.StatusServiceUnavailable)
		return
	}
	reqPath := strings.TrimPrefix(r.URL.Path, "/")
	if reqPath == "" {
		reqPath = "index.html"
	}
	asset, resolvedPath, err := a.loadStaticAsset(reqPath)
	if err != nil {
		http.Error(w, "frontend missing", http.StatusServiceUnavailable)
		return
	}

	if strings.HasSuffix(resolvedPath, ".html") {
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasPrefix(resolvedPath, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Header().Set("ETag", asset.etag)
	if asset.gz != nil {
		w.Header().Add("Vary", "Accept-Encoding")
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" && strings.Contains(inm, asset.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", asset.contentType)
	if asset.gz != nil && acceptsGzip(r) {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(asset.gz)
		return
	}
	http.ServeContent(w, r, resolvedPath, time.Time{}, bytes.NewReader(asset.raw))
}

func (a *App) buildBootstrapPayload(r *http.Request) Obj {
	flags := a.publicAuthFlags()
	authenticated, method := a.authenticateRequest(r, false)
	auth := Obj{
		"authRequired":  true,
		"authenticated": authenticated,
		"username":      "",
		"authMethod":    "",
	}
	for k, v := range flags {
		auth[k] = v
	}
	if !authenticated {
		auth["passwordConfigured"] = true
		return Obj{"auth": auth}
	}

	webuiAuth, _ := a.Store.Get("secrets", "webui_auth")
	username := strings.TrimSpace(str(webuiAuth["username"]))
	if username == "" {
		username = "admin"
	}
	auth["username"] = username
	auth["authMethod"] = method
	auth["passwordConfigured"] = !flag(webuiAuth["autoGenerated"])

	favs, _ := a.favorites()
	if favs == nil {
		favs = []Obj{}
	}
	hidden, _ := a.hiddenAnime()
	if hidden == nil {
		hidden = []Obj{}
	}
	subs, _ := a.Store.List("subscriptions")
	if subs == nil {
		subs = []Obj{}
	}
	rawDownloads, _ := a.Store.List("downloads")
	if rawDownloads == nil {
		rawDownloads = []Obj{}
	}

	var homeItems any
	homeFresh := false
	if cachedHome, ok := a.peekDiscoveryCache("home:v4", time.Hour); ok {
		rawItems := list(cachedHome["items"])
		enriched := make([]Obj, 0, len(rawItems))
		for _, v := range rawItems {
			item := object(v)
			a.enrichAnimeBroadcast(item)
			enriched = append(enriched, item)
		}
		homeItems = enriched
		homeFresh = true
	} else if staleHome, ok := a.peekDiscoveryCache("home:v4", 7*24*time.Hour); ok {
		rawItems := list(staleHome["items"])
		enriched := make([]Obj, 0, len(rawItems))
		for _, v := range rawItems {
			item := object(v)
			a.enrichAnimeBroadcast(item)
			enriched = append(enriched, item)
		}
		homeItems = enriched
		homeFresh = false
	}

	return Obj{
		"auth":          auth,
		"status":        a.status(),
		"settings":      a.Settings(),
		"favorites":     favs,
		"hidden":        hidden,
		"subscriptions": subs,
		"downloads":     a.publicDownloads(rawDownloads),
		"homepage":      homeItems,
		"homepageFresh": homeFresh,
	}
}
