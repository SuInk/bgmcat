package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	playableExtList        = "|.mp4|.mkv|.webm|.avi|.ts|.m2ts|.mov|.m4v|.mp3|.flac|.m4a|.ogg|.opus|.wav|"
	localTrashDirName      = ".trash"
	localTrashManifestFile = ".manifest.json"
)

var fileInlineMediaTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".bmp": "image/bmp", ".avif": "image/avif", ".ico": "image/x-icon",
	".svg": "image/svg+xml",
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg", ".oga": "audio/ogg", ".opus": "audio/ogg",
	".m4a": "audio/mp4", ".aac": "audio/aac", ".flac": "audio/flac",
	".pdf": "application/pdf",
}

var fileSniffedInlineTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/bmp": true,
	"video/mp4": true, "video/webm": true, "audio/mpeg": true, "audio/wave": true, "audio/ogg": true,
	"application/pdf": true,
}

func isPlayableExt(ext string) bool {
	if ext == "" {
		return false
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return strings.Contains(playableExtList, "|"+strings.ToLower(ext)+"|")
}

func fileTextExtension(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".md", ".markdown", ".json", ".jsonl", ".yaml", ".yml", ".toml", ".ini", ".conf", ".cfg", ".env",
		".csv", ".tsv", ".log", ".nfo", ".ass", ".ssa", ".srt", ".vtt", ".lrc", ".cue", ".html", ".htm", ".css",
		".xml", ".sql", ".go", ".py", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".vue", ".sh", ".bash", ".zsh":
		return true
	default:
		return false
	}
}

func filePreviewContentType(name, mimeHint string, head []byte) (string, bool) {
	if media, ok := fileInlineMediaTypes[strings.ToLower(filepath.Ext(name))]; ok {
		return media, true
	}
	detected := http.DetectContentType(head)
	base := strings.TrimSpace(strings.SplitN(detected, ";", 2)[0])
	if fileSniffedInlineTypes[base] {
		return base, true
	}
	hintBase := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeHint, ";", 2)[0]))
	if fileSniffedInlineTypes[hintBase] {
		return hintBase, true
	}
	hasBinaryNul := bytes.IndexByte(head, 0) >= 0 &&
		!(len(head) >= 2 && ((head[0] == 0xFF && head[1] == 0xFE) || (head[0] == 0xFE && head[1] == 0xFF)))
	if fileTextExtension(name) || (!hasBinaryNul && (strings.HasPrefix(base, "text/") || strings.HasPrefix(hintBase, "text/") || hintBase == "application/json" || hintBase == "application/xml")) {
		return "text/plain; charset=utf-8", true
	}
	return "application/octet-stream", false
}

func writePreviewHeaders(w http.ResponseWriter, fileName, contentType string, inline, forceDownload bool) {
	w.Header().Set("Cache-Control", "no-store")
	disposition := "attachment"
	if inline && !forceDownload {
		disposition = "inline"
	}
	safeName := filepath.Base(strings.TrimSpace(fileName))
	if safeName == "" || safeName == "." || safeName == ".." {
		safeName = "download"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": safeName}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if contentType != "application/pdf" {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	}
}

func classifyFileKind(isDir bool, ext, mimeType string) string {
	if isDir {
		return "folder"
	}
	cleanExt := strings.TrimPrefix(strings.ToLower(ext), ".")
	lowerMime := strings.ToLower(mimeType)
	switch cleanExt {
	case "mp4", "mkv", "webm", "avi", "ts", "m2ts", "mov", "m4v", "wmv", "flv":
		return "video"
	case "mp3", "flac", "m4a", "aac", "ogg", "opus", "wav":
		return "audio"
	case "png", "jpg", "jpeg", "gif", "webp", "bmp", "avif", "ico", "svg":
		return "image"
	case "pdf":
		return "pdf"
	case "ass", "ssa", "srt", "vtt", "sup":
		return "subtitle"
	case "txt", "md", "markdown", "json", "jsonl", "yaml", "yml", "toml", "ini", "conf", "cfg", "csv", "tsv", "log", "nfo", "lrc", "cue", "xml":
		return "text"
	case "zip", "7z", "rar", "tar", "gz", "xz":
		return "archive"
	case "torrent":
		return "torrent"
	}
	switch {
	case strings.HasPrefix(lowerMime, "video/"):
		return "video"
	case strings.HasPrefix(lowerMime, "audio/"):
		return "audio"
	case strings.HasPrefix(lowerMime, "image/"):
		return "image"
	case lowerMime == "application/pdf" ||
		strings.HasPrefix(lowerMime, "application/vnd.google-apps.presentation") ||
		strings.HasPrefix(lowerMime, "application/vnd.google-apps.drawing"):
		return "pdf"
	case strings.HasPrefix(lowerMime, "text/") ||
		lowerMime == "application/json" ||
		lowerMime == "application/xml" ||
		strings.HasPrefix(lowerMime, "application/vnd.google-apps.document") ||
		strings.HasPrefix(lowerMime, "application/vnd.google-apps.spreadsheet") ||
		strings.HasPrefix(lowerMime, "application/vnd.google-apps.script"):
		return "text"
	}
	return "other"
}

func encodeLocalMediaID(relSlash string) string {
	return "local~" + base64.RawURLEncoding.EncodeToString([]byte(relSlash))
}

func decodeLocalMediaID(gid string) (string, bool) {
	encoded, ok := strings.CutPrefix(gid, "local~")
	if !ok || encoded == "" {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	clean, err := cleanRelSlashPath(string(raw))
	if err != nil || clean == "" {
		return "", false
	}
	return clean, true
}

func encodeDriveMediaID(fileID, fileName, mimeType string) string {
	safeName := strings.TrimSpace(fileName)
	safeName = strings.ReplaceAll(safeName, "\\", "_")
	safeName = strings.ReplaceAll(safeName, "/", "_")
	safeName = filepath.Base(safeName)
	if safeName == "" || safeName == "." || safeName == ".." {
		safeName = "video"
	}
	if !isPlayableExt(filepath.Ext(safeName)) {
		lowerMime := strings.ToLower(mimeType)
		switch {
		case strings.Contains(lowerMime, "matroska"):
			safeName += ".mkv"
		case strings.Contains(lowerMime, "webm"):
			safeName += ".webm"
		case strings.Contains(lowerMime, "flac"):
			safeName += ".flac"
		case strings.HasPrefix(lowerMime, "audio/"):
			safeName += ".mp3"
		default:
			safeName += ".mp4"
		}
	}
	return "drive~" + fileID + "~" + base64.RawURLEncoding.EncodeToString([]byte(safeName))
}

func decodeDriveMediaID(gid string) (fileID string, safeName string, ok bool) {
	if !strings.HasPrefix(gid, "drive~") {
		return "", "", false
	}
	parts := strings.Split(gid, "~")
	if len(parts) != 3 || parts[0] != "drive" {
		return "", "", false
	}
	id := parts[1]
	if !driveFolderID.MatchString(id) {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", false
	}
	name := strings.ReplaceAll(string(raw), "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." || !isPlayableExt(filepath.Ext(name)) {
		return "", "", false
	}
	return id, name, true
}

func cleanRelSlashPath(raw string) (string, error) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if strings.ContainsRune(trimmed, 0) {
		return "", errors.New("非法的路径字符")
	}
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "" || trimmed == "." {
		return "", nil
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == ".." {
			return "", errors.New("不允许访问上级目录")
		}
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == "/" {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("不允许访问上级目录")
	}
	return cleaned, nil
}

func validateSingleEntryName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || name == "." || name == ".." {
		return "", errors.New("请输入有效的文件夹名称")
	}
	if len(name) > 200 {
		return "", errors.New("名称过长（最多 200 个字符）")
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return "", errors.New("名称不能包含斜杠或控制字符")
	}
	return name, nil
}

func isPathWithinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (a *App) downloadRoot() (string, error) {
	dir := strings.TrimSpace(str(a.Settings()["downloadDir"]))
	if dir == "" {
		dir = filepath.Join(a.cfg.DataDir, "downloads")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return "", err
	}
	if realRoot, err := filepath.EvalSymlinks(abs); err == nil {
		return realRoot, nil
	}
	return abs, nil
}

func (a *App) resolveExistingLocalPath(relRaw string) (root string, cleanRel string, realTarget string, info os.FileInfo, err error) {
	root, err = a.downloadRoot()
	if err != nil {
		return "", "", "", nil, err
	}
	cleanRel, err = cleanRelSlashPath(relRaw)
	if err != nil {
		return "", "", "", nil, err
	}
	joined := root
	if cleanRel != "" {
		joined = filepath.Join(root, filepath.FromSlash(cleanRel))
	}
	if !isPathWithinRoot(root, joined) {
		return "", "", "", nil, errors.New("路径超出下载目录范围")
	}
	realTarget, err = filepath.EvalSymlinks(joined)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", "", nil, errors.New("目标文件或目录不存在")
		}
		return "", "", "", nil, err
	}
	if !isPathWithinRoot(root, realTarget) {
		return "", "", "", nil, errors.New("不允许访问指向下载目录外部的软链接")
	}
	info, err = os.Stat(realTarget)
	if err != nil {
		return "", "", "", nil, err
	}
	return root, cleanRel, realTarget, info, nil
}

func (a *App) isLocalTargetBusy(targetAbs, cleanRel string) bool {
	if a.media != nil && cleanRel != "" && a.media.Busy(encodeLocalMediaID(cleanRel)) {
		return true
	}
	jobs, err := a.Store.List("downloads")
	if err != nil {
		return false
	}
	for _, j := range jobs {
		st := str(j["status"])
		archiving := str(object(j["cloudArchive"])["status"]) == "uploading"
		if st != "active" && st != "waiting" && !archiving {
			continue
		}
		for _, rawFile := range list(j["files"]) {
			fp := str(object(rawFile)["path"])
			if fp == "" {
				continue
			}
			if absFP, e := filepath.Abs(fp); e == nil && isPathWithinRoot(targetAbs, absFP) {
				return true
			}
		}
	}
	return false
}

func shouldSkipSystemHiddenFile(name string) bool {
	switch strings.ToLower(name) {
	case ".ds_store", "thumbs.db", "desktop.ini":
		return true
	default:
		return false
	}
}

func escapeDriveQueryLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

func isHex24(s string) bool {
	if len(s) != 24 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func stripGidPrefix(name string) string {
	if len(name) > 25 && name[24] == '-' && isHex24(name[:24]) {
		return name[25:]
	}
	return ""
}

func (a *App) downloadTitlesByGid() map[string]string {
	jobs, err := a.Store.List("downloads")
	if err != nil || len(jobs) == 0 {
		return nil
	}
	out := make(map[string]string, len(jobs))
	for _, j := range jobs {
		gid := str(j["gid"])
		title := strings.TrimSpace(str(j["feedItemTitle"]))
		if gid != "" && title != "" {
			out[gid] = title
		}
	}
	return out
}

type localTrashManifestItem struct {
	OriginalPath string `json:"originalPath"`
	DeletedAt    string `json:"deletedAt"`
}

func readLocalTrashManifest(root string) map[string]localTrashManifestItem {
	manifestPath := filepath.Join(root, localTrashDirName, localTrashManifestFile)
	raw, err := os.ReadFile(manifestPath)
	if err != nil || len(raw) == 0 {
		return map[string]localTrashManifestItem{}
	}
	out := map[string]localTrashManifestItem{}
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]localTrashManifestItem{}
	}
	return out
}

func writeLocalTrashManifest(root string, manifest map[string]localTrashManifestItem) {
	trashDir := filepath.Join(root, localTrashDirName)
	manifestPath := filepath.Join(trashDir, localTrashManifestFile)
	if len(manifest) == 0 {
		_ = os.Remove(manifestPath)
		return
	}
	if data, err := json.Marshal(manifest); err == nil {
		_ = os.WriteFile(manifestPath, data, 0600)
	}
}

func localTrashStats(root string) (count int, totalBytes int64) {
	trashDir := filepath.Join(root, localTrashDirName)
	entries, err := os.ReadDir(trashDir)
	if err != nil {
		return 0, 0
	}
	for _, de := range entries {
		name := de.Name()
		if name == localTrashManifestFile || shouldSkipSystemHiddenFile(name) {
			continue
		}
		count++
		b, _ := measurePathUsage(filepath.Join(trashDir, name))
		totalBytes += b
	}
	return count, totalBytes
}

func moveLocalTargetToTrash(root, cleanRel, targetAbs string) (string, error) {
	trashDir := filepath.Join(root, localTrashDirName)
	if err := os.MkdirAll(trashDir, 0700); err != nil {
		return "", err
	}
	baseName := path.Base(cleanRel)
	destName := baseName
	if _, err := os.Lstat(filepath.Join(trashDir, destName)); err == nil {
		stamp := time.Now().Format("0102-150405")
		ext := filepath.Ext(baseName)
		stem := strings.TrimSuffix(baseName, ext)
		for i := 1; i <= 999; i++ {
			suffix := stamp
			if i > 1 {
				suffix = fmt.Sprintf("%s-%d", stamp, i)
			}
			candidate := fmt.Sprintf("%s (%s)%s", stem, suffix, ext)
			if _, e := os.Lstat(filepath.Join(trashDir, candidate)); os.IsNotExist(e) {
				destName = candidate
				break
			}
		}
	}
	destAbs := filepath.Join(trashDir, destName)
	if !isPathWithinRoot(root, destAbs) {
		return "", errors.New("回收站路径异常")
	}
	if err := os.Rename(targetAbs, destAbs); err != nil {
		return "", err
	}
	manifest := readLocalTrashManifest(root)
	manifest[destName] = localTrashManifestItem{
		OriginalPath: cleanRel,
		DeletedAt:    now(),
	}
	writeLocalTrashManifest(root, manifest)
	return localTrashDirName + "/" + destName, nil
}

func cleanupLocalTrashIfEmpty(root string) {
	count, _ := localTrashStats(root)
	if count == 0 {
		_ = os.RemoveAll(filepath.Join(root, localTrashDirName))
	}
}

func parsePreviewRangeLimit(rangeHeader string) int {
	h := strings.TrimSpace(rangeHeader)
	if !strings.HasPrefix(h, "bytes=0-") {
		return 0
	}
	endStr := strings.TrimSpace(strings.TrimPrefix(h, "bytes=0-"))
	if idx := strings.IndexByte(endStr, ','); idx >= 0 {
		return 0
	}
	end, err := strconv.Atoi(endStr)
	if err != nil || end < 0 || end > 8<<20 {
		return 0
	}
	return end + 1
}

func (a *App) fileRoutes() {
	a.handle("GET /api/files/local", func(w http.ResponseWriter, r *http.Request) error {
		reqPath := r.URL.Query().Get("path")
		cleanReq, reqErr := cleanRelSlashPath(reqPath)
		if reqErr == nil && cleanReq == localTrashDirName {
			if root, rErr := a.downloadRoot(); rErr == nil {
				if _, stErr := os.Stat(filepath.Join(root, localTrashDirName)); os.IsNotExist(stErr) {
					freeBytes, totalBytes := diskUsage(root)
					ok(w, Obj{
						"rootPath":   root,
						"path":       localTrashDirName,
						"freeBytes":  freeBytes,
						"totalBytes": totalBytes,
						"trashCount": 0,
						"trashBytes": int64(0),
						"entries":    []Obj{},
					})
					return nil
				}
			}
		}
		root, cleanRel, dirAbs, info, err := a.resolveExistingLocalPath(reqPath)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("目标路径不是目录")
		}
		dirEntries, err := os.ReadDir(dirAbs)
		if err != nil {
			return err
		}
		var titlesByGid map[string]string
		if cleanRel == "" {
			titlesByGid = a.downloadTitlesByGid()
		}
		var trashManifest map[string]localTrashManifestItem
		if cleanRel == localTrashDirName {
			trashManifest = readLocalTrashManifest(root)
		}
		type item struct {
			obj   Obj
			isDir bool
			lower string
		}
		items := make([]item, 0, len(dirEntries))
		for _, de := range dirEntries {
			name := de.Name()
			if shouldSkipSystemHiddenFile(name) {
				continue
			}
			if cleanRel == "" && name == localTrashDirName {
				continue
			}
			if cleanRel == localTrashDirName && name == localTrashManifestFile {
				continue
			}
			childRel := name
			if cleanRel != "" {
				childRel = cleanRel + "/" + name
			}
			childJoined := filepath.Join(dirAbs, name)
			realChild, e := filepath.EvalSymlinks(childJoined)
			if e != nil || !isPathWithinRoot(root, realChild) {
				// 跳过失效或越界的软链接，避免整目录报错
				continue
			}
			fi, e := os.Stat(realChild)
			if e != nil {
				continue
			}
			isDir := fi.IsDir()
			ext := ""
			if !isDir {
				ext = strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
			}
			playable := !isDir && fi.Mode().IsRegular() && isPlayableExt(filepath.Ext(name))
			mediaID := ""
			if playable {
				mediaID = encodeLocalMediaID(childRel)
			}
			var size int64
			if !isDir {
				size = fi.Size()
			} else if cleanRel == localTrashDirName {
				size, _ = measurePathUsage(realChild)
			}
			displayName := ""
			if isDir && titlesByGid != nil {
				displayName = titlesByGid[name]
			}
			var originalPath string
			if trashManifest != nil {
				if rec, ok := trashManifest[name]; ok && rec.OriginalPath != "" {
					originalPath = rec.OriginalPath
					if strings.Contains(rec.OriginalPath, "/") {
						displayName = rec.OriginalPath
					}
				}
			}
			sortKey := name
			if displayName != "" {
				sortKey = displayName
			}
			entryObj := Obj{
				"id":       "local:" + childRel,
				"name":     name,
				"path":     childRel,
				"isDir":    isDir,
				"size":     size,
				"modTime":  fi.ModTime().UTC().Format(time.RFC3339),
				"ext":      ext,
				"kind":     classifyFileKind(isDir, ext, ""),
				"playable": playable,
				"mediaId":  mediaID,
			}
			if displayName != "" {
				entryObj["displayName"] = displayName
			}
			if originalPath != "" {
				entryObj["originalPath"] = originalPath
			}
			items = append(items, item{
				isDir: isDir,
				lower: strings.ToLower(sortKey),
				obj:   entryObj,
			})
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].isDir != items[j].isDir {
				return items[i].isDir
			}
			return items[i].lower < items[j].lower
		})
		entries := make([]Obj, len(items))
		for i, it := range items {
			entries[i] = it.obj
		}
		freeBytes, totalBytes := diskUsage(dirAbs)
		trashCount, trashBytes := localTrashStats(root)
		ok(w, Obj{
			"rootPath":   root,
			"path":       cleanRel,
			"freeBytes":  freeBytes,
			"totalBytes": totalBytes,
			"trashCount": trashCount,
			"trashBytes": trashBytes,
			"entries":    entries,
		})
		return nil
	})

	a.handle("POST /api/files/local/mkdir", func(w http.ResponseWriter, r *http.Request) error {
		input, err := body(w, r)
		if err != nil {
			return err
		}
		name, err := validateSingleEntryName(str(input["name"]))
		if err != nil {
			return err
		}
		if name == localTrashDirName {
			return errors.New("保留名称，请换一个文件夹名称")
		}
		root, cleanParent, parentAbs, info, err := a.resolveExistingLocalPath(str(input["parent"]))
		if err != nil {
			return err
		}
		if cleanParent == localTrashDirName || strings.HasPrefix(cleanParent, localTrashDirName+"/") {
			return errors.New("不能在回收站内新建文件夹")
		}
		if !info.IsDir() {
			return errors.New("父路径不是目录")
		}
		target := filepath.Join(parentAbs, name)
		if !isPathWithinRoot(root, target) {
			return errors.New("目录路径越界")
		}
		if _, err := os.Lstat(target); err == nil {
			return errors.New("同名文件或文件夹已存在")
		}
		if err := os.Mkdir(target, 0755); err != nil {
			return err
		}
		newRel := name
		if cleanParent != "" {
			newRel = cleanParent + "/" + name
		}
		ok(w, Obj{"name": name, "path": newRel})
		return nil
	})

	a.handle("DELETE /api/files/local", func(w http.ResponseWriter, r *http.Request) error {
		cleanRel, err := cleanRelSlashPath(r.URL.Query().Get("path"))
		if err != nil {
			return err
		}
		if cleanRel == "" {
			return errors.New("不能直接删除下载根目录")
		}
		if cleanRel == localTrashDirName {
			return errors.New("请使用清空回收站操作")
		}
		root, err := a.downloadRoot()
		if err != nil {
			return err
		}
		parentSlash := path.Dir(cleanRel)
		if parentSlash == "." {
			parentSlash = ""
		}
		_, _, realParent, parentInfo, err := a.resolveExistingLocalPath(parentSlash)
		if err != nil {
			return err
		}
		if !parentInfo.IsDir() {
			return errors.New("父路径不是目录")
		}
		baseName := path.Base(cleanRel)
		target := filepath.Join(realParent, baseName)
		if !isPathWithinRoot(root, target) || target == root {
			return errors.New("不允许删除下载目录外的路径")
		}
		if _, err := os.Lstat(target); err != nil {
			if os.IsNotExist(err) {
				return errors.New("目标文件或目录不存在")
			}
			return err
		}
		if a.isLocalTargetBusy(target, cleanRel) {
			return errors.New("该文件正在下载、上传归档或播放中，请稍后再试")
		}
		inTrash := strings.HasPrefix(cleanRel, localTrashDirName+"/")
		permanent := r.URL.Query().Get("permanent") == "1" || inTrash
		if permanent {
			if err := os.RemoveAll(target); err != nil {
				return err
			}
			if inTrash && parentSlash == localTrashDirName {
				manifest := readLocalTrashManifest(root)
				delete(manifest, baseName)
				writeLocalTrashManifest(root, manifest)
				cleanupLocalTrashIfEmpty(root)
			}
			ok(w, Obj{"deleted": true, "path": cleanRel, "trashed": false})
			return nil
		}
		trashRel, err := moveLocalTargetToTrash(root, cleanRel, target)
		if err != nil {
			return err
		}
		ok(w, Obj{"deleted": true, "path": cleanRel, "trashed": true, "trashPath": trashRel})
		return nil
	})

	a.handle("POST /api/files/local/trash/restore", func(w http.ResponseWriter, r *http.Request) error {
		input, err := body(w, r)
		if err != nil {
			return err
		}
		cleanRel, err := cleanRelSlashPath(str(input["path"]))
		if err != nil {
			return err
		}
		parts := strings.Split(cleanRel, "/")
		if len(parts) != 2 || parts[0] != localTrashDirName || parts[1] == "" || parts[1] == localTrashManifestFile {
			return errors.New("只能从回收站顶层还原项目")
		}
		root, err := a.downloadRoot()
		if err != nil {
			return err
		}
		trashName := parts[1]
		srcAbs := filepath.Join(root, localTrashDirName, trashName)
		if _, err := os.Lstat(srcAbs); err != nil {
			if os.IsNotExist(err) {
				return errors.New("回收站中不存在该项目")
			}
			return err
		}
		manifest := readLocalTrashManifest(root)
		destRel := trashName
		if rec, exists := manifest[trashName]; exists && rec.OriginalPath != "" {
			if cleaned, cErr := cleanRelSlashPath(rec.OriginalPath); cErr == nil && cleaned != "" && !strings.HasPrefix(cleaned, localTrashDirName+"/") && cleaned != localTrashDirName {
				destRel = cleaned
			}
		}
		destParentRel := path.Dir(destRel)
		if destParentRel == "." {
			destParentRel = ""
		}
		destParentAbs := root
		if destParentRel != "" {
			destParentAbs = filepath.Join(root, filepath.FromSlash(destParentRel))
			if !isPathWithinRoot(root, destParentAbs) {
				return errors.New("还原目标路径越界")
			}
			if err := os.MkdirAll(destParentAbs, 0755); err != nil {
				return err
			}
		}
		destBase := path.Base(destRel)
		finalBase := destBase
		if _, err := os.Lstat(filepath.Join(destParentAbs, finalBase)); err == nil {
			ext := filepath.Ext(destBase)
			stem := strings.TrimSuffix(destBase, ext)
			for i := 1; i <= 999; i++ {
				suffix := "还原"
				if i > 1 {
					suffix = fmt.Sprintf("还原 %d", i)
				}
				candidate := fmt.Sprintf("%s (%s)%s", stem, suffix, ext)
				if _, e := os.Lstat(filepath.Join(destParentAbs, candidate)); os.IsNotExist(e) {
					finalBase = candidate
					break
				}
			}
		}
		destAbs := filepath.Join(destParentAbs, finalBase)
		if err := os.Rename(srcAbs, destAbs); err != nil {
			return err
		}
		delete(manifest, trashName)
		writeLocalTrashManifest(root, manifest)
		cleanupLocalTrashIfEmpty(root)
		finalRel := finalBase
		if destParentRel != "" {
			finalRel = destParentRel + "/" + finalBase
		}
		ok(w, Obj{"restored": true, "path": finalRel})
		return nil
	})

	a.handle("POST /api/files/local/trash/empty", func(w http.ResponseWriter, r *http.Request) error {
		root, err := a.downloadRoot()
		if err != nil {
			return err
		}
		count, bytes := localTrashStats(root)
		trashDir := filepath.Join(root, localTrashDirName)
		if err := os.RemoveAll(trashDir); err != nil && !os.IsNotExist(err) {
			return err
		}
		ok(w, Obj{"deletedCount": count, "deletedBytes": bytes})
		return nil
	})

	a.mux.HandleFunc("GET /api/files/local/content", func(w http.ResponseWriter, r *http.Request) {
		rawPath := r.URL.Query().Get("path")
		_, cleanRel, realTarget, info, err := a.resolveExistingLocalPath(rawPath)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		if cleanRel == "" || info.IsDir() || !info.Mode().IsRegular() {
			fail(w, http.StatusBadRequest, "只能预览或下载普通文件")
			return
		}
		f, err := os.Open(realTarget)
		if err != nil {
			fail(w, http.StatusNotFound, "无法打开目标文件")
			return
		}
		defer f.Close()
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			fail(w, http.StatusInternalServerError, "读取文件失败")
			return
		}
		contentType, inline := filePreviewContentType(info.Name(), "", head[:n])
		forceDownload := r.URL.Query().Get("download") == "1"
		writePreviewHeaders(w, info.Name(), contentType, inline, forceDownload)
		if limit := parsePreviewRangeLimit(r.Header.Get("Range")); limit > 0 && info.Size() > int64(limit) {
			w.Header().Set("X-Preview-Clipped", "1")
		}
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	})

	a.handle("GET /api/files/cloud", func(w http.ResponseWriter, r *http.Request) error {
		provider := a.activeCloudProvider()
		rawParent := strings.TrimSpace(r.URL.Query().Get("parent"))
		pageToken := strings.TrimSpace(r.URL.Query().Get("pageToken"))
		search := strings.TrimSpace(r.URL.Query().Get("q"))
		switch provider {
		case "onedrive":
			resp, err := a.listOneDriveBrowserEntries(r.Context(), rawParent, pageToken, search)
			if err != nil {
				return err
			}
			ok(w, resp)
			return nil
		case "webdav":
			resp, err := a.listWebDAVBrowserEntries(r.Context(), rawParent, search)
			if err != nil {
				return err
			}
			ok(w, resp)
			return nil
		}

		s := a.Settings()
		cid, _, _ := a.effectiveGoogleCredentials()
		configured := cid != ""
		connected := a.driveConnected()
		archiveFolderID := strings.TrimSpace(str(s["cloudDriveFolderId"]))
		if !driveFolderID.MatchString(archiveFolderID) {
			archiveFolderID = "root"
		}
		if !connected {
			ok(w, Obj{
				"provider":        "google",
				"providerName":    "Google Drive",
				"connected":       false,
				"configured":      configured,
				"archiveFolderId": archiveFolderID,
				"parent":          "root",
				"parentName":      "我的云盘",
				"entries":         []Obj{},
			})
			return nil
		}

		parent := rawParent
		parentName := "我的云盘"
		if parent == "" || parent == "default" {
			if archiveFolderID != "" && archiveFolderID != "root" {
				meta, statusCode, metaErr := a.driveJSON(r.Context(), "GET", driveBase+"/files/"+url.PathEscape(archiveFolderID)+"?fields=id,name,mimeType,trashed&supportsAllDrives=true", nil)
				if metaErr == nil && statusCode == 200 && !flag(meta["trashed"]) && str(meta["mimeType"]) == "application/vnd.google-apps.folder" {
					parent = archiveFolderID
					if n := strings.TrimSpace(str(meta["name"])); n != "" {
						parentName = n
					} else {
						parentName = "归档目录"
					}
				} else {
					parent = "root"
				}
			} else {
				parent = "root"
			}
		} else {
			if !driveFolderID.MatchString(parent) {
				return errors.New("无效的云盘目录 ID")
			}
			if parent != "root" && pageToken == "" {
				meta, statusCode, metaErr := a.driveJSON(r.Context(), "GET", driveBase+"/files/"+url.PathEscape(parent)+"?fields=id,name&supportsAllDrives=true", nil)
				if metaErr == nil && statusCode == 200 {
					if n := strings.TrimSpace(str(meta["name"])); n != "" {
						parentName = n
					}
				}
			}
		}

		qClauses := []string{
			"trashed = false",
			"'" + parent + "' in parents",
		}
		if search != "" {
			qClauses = append(qClauses, "name contains '"+escapeDriveQueryLiteral(search)+"'")
		}
		params := url.Values{
			"q":                         {strings.Join(qClauses, " and ")},
			"fields":                    {"nextPageToken,files(id,name,mimeType,size,modifiedTime)"},
			"pageSize":                  {"100"},
			"orderBy":                   {"folder,name"},
			"supportsAllDrives":         {"true"},
			"includeItemsFromAllDrives": {"true"},
		}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}

		var (
			listRes  Obj
			listErr  error
			quotaObj Obj
			wg       sync.WaitGroup
		)
		wg.Add(1)
		go func() {
			defer wg.Done()
			listRes, _, listErr = a.driveJSON(r.Context(), "GET", driveBase+"/files?"+params.Encode(), nil)
		}()
		if pageToken == "" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				qCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer cancel()
				about, code, err := a.driveJSON(qCtx, "GET", driveBase+"/about?fields=user(emailAddress,displayName),storageQuota(limit,usage)", nil)
				if err == nil && code == 200 && about != nil {
					sq := object(about["storageQuota"])
					user := object(about["user"])
					usage, _ := strconv.ParseInt(str(sq["usage"]), 10, 64)
					limit, _ := strconv.ParseInt(str(sq["limit"]), 10, 64)
					quotaObj = Obj{
						"usageBytes": usage,
						"limitBytes": limit,
						"userEmail":  str(user["emailAddress"]),
					}
				}
			}()
		}
		wg.Wait()
		if listErr != nil {
			return listErr
		}

		rawFiles := list(listRes["files"])
		entries := make([]Obj, 0, len(rawFiles))
		for _, v := range rawFiles {
			f := object(v)
			id := str(f["id"])
			if !driveFolderID.MatchString(id) {
				continue
			}
			name := str(f["name"])
			mimeType := str(f["mimeType"])
			isDir := mimeType == "application/vnd.google-apps.folder"
			size, _ := strconv.ParseInt(str(f["size"]), 10, 64)
			ext := ""
			if !isDir {
				ext = strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
			}
			kind := classifyFileKind(isDir, ext, mimeType)
			playable := !isDir && (isPlayableExt(filepath.Ext(name)) || kind == "video" || kind == "audio")
			mediaID := ""
			if playable {
				mediaID = encodeDriveMediaID(id, name, mimeType)
			}
			entryObj := Obj{
				"id":       id,
				"name":     name,
				"path":     id,
				"isDir":    isDir,
				"mimeType": mimeType,
				"size":     size,
				"modTime":  str(f["modifiedTime"]),
				"ext":      ext,
				"kind":     kind,
				"playable": playable,
				"mediaId":  mediaID,
			}
			if trimmed := stripGidPrefix(name); trimmed != "" {
				entryObj["displayName"] = trimmed
			}
			entries = append(entries, entryObj)
		}

		resp := Obj{
			"provider":        "google",
			"providerName":    "Google Drive",
			"connected":       true,
			"configured":      configured,
			"archiveFolderId": archiveFolderID,
			"parent":          parent,
			"parentName":      parentName,
			"nextPageToken":   str(listRes["nextPageToken"]),
			"entries":         entries,
		}
		if quotaObj != nil {
			resp["quota"] = quotaObj
		}
		ok(w, resp)
		return nil
	})

	a.handle("POST /api/files/cloud/mkdir", func(w http.ResponseWriter, r *http.Request) error {
		provider := a.activeCloudProvider()
		if !a.cloudProviderConnected(provider) {
			return errors.New("请先连接 " + cloudProviderDisplayName(provider))
		}
		input, err := body(w, r)
		if err != nil {
			return err
		}
		parent := strings.TrimSpace(str(input["parent"]))
		if parent == "" {
			parent = "root"
		}
		name, err := validateSingleEntryName(str(input["name"]))
		if err != nil {
			return err
		}
		switch provider {
		case "onedrive":
			created, err := a.onedriveCreateFolder(r.Context(), parent, name)
			if err != nil {
				return err
			}
			ok(w, created)
			return nil
		case "webdav":
			parentRel := cleanWebDAVRelPath(parent)
			targetRel := name
			if parentRel != "" {
				targetRel = parentRel + "/" + name
			}
			if err := a.webdavEnsureDir(r.Context(), a.Settings(), targetRel); err != nil {
				return err
			}
			ok(w, Obj{"id": targetRel, "name": name})
			return nil
		}
		if !driveFolderID.MatchString(parent) {
			return errors.New("无效的父文件夹 ID")
		}
		created, _, err := a.driveJSON(
			r.Context(),
			"POST",
			driveBase+"/files?fields=id,name,mimeType,modifiedTime&supportsAllDrives=true",
			Obj{
				"name":     name,
				"mimeType": "application/vnd.google-apps.folder",
				"parents":  []string{parent},
			},
		)
		if err != nil {
			return err
		}
		ok(w, created)
		return nil
	})

	deleteCloudItem := func(w http.ResponseWriter, r *http.Request, id string) error {
		provider := a.activeCloudProvider()
		if !a.cloudProviderConnected(provider) {
			return errors.New("请先连接 " + cloudProviderDisplayName(provider))
		}
		id = strings.TrimSpace(id)
		switch provider {
		case "onedrive":
			if id == "" || id == "root" || !onedriveItemIDPattern.MatchString(id) {
				return errors.New("无效的 OneDrive 文件 ID")
			}
			res, err := a.onedriveRequest(r.Context(), "DELETE", msGraphBase+"/me/drive/items/"+url.PathEscape(id), nil, nil, true)
			if err != nil {
				return err
			}
			res.Body.Close()
			if res.StatusCode >= 300 && res.StatusCode != 404 {
				return errors.New("删除 OneDrive 文件失败")
			}
			ok(w, Obj{"deleted": true, "id": id, "trashed": true})
			return nil
		case "webdav":
			rel := cleanWebDAVRelPath(id)
			if rel == "" {
				return errors.New("不能直接删除 WebDAV 根目录")
			}
			res, err := a.webdavRequest(r.Context(), a.Settings(), "DELETE", rel, false, nil, 0, nil)
			if err != nil {
				return err
			}
			res.Body.Close()
			if res.StatusCode >= 300 && res.StatusCode != 404 {
				return errors.New("删除 WebDAV 文件失败")
			}
			ok(w, Obj{"deleted": true, "id": rel, "trashed": false})
			return nil
		}
		if id == "" || id == "root" || !driveFolderID.MatchString(id) {
			return errors.New("无效的云盘文件 ID")
		}
		_, _, err := a.driveJSON(
			r.Context(),
			"PATCH",
			driveBase+"/files/"+url.PathEscape(id)+"?fields=id,trashed&supportsAllDrives=true",
			Obj{"trashed": true},
		)
		if err != nil {
			return err
		}
		ok(w, Obj{"deleted": true, "id": id, "trashed": true})
		return nil
	}

	a.handle("DELETE /api/files/cloud", func(w http.ResponseWriter, r *http.Request) error {
		return deleteCloudItem(w, r, r.URL.Query().Get("id"))
	})
	a.handle("DELETE /api/files/cloud/{id...}", func(w http.ResponseWriter, r *http.Request) error {
		return deleteCloudItem(w, r, r.PathValue("id"))
	})

	a.mux.HandleFunc("GET /api/files/cloud/content", func(w http.ResponseWriter, r *http.Request) {
		provider := strings.TrimSpace(r.URL.Query().Get("provider"))
		if !validCloudProvider(provider) {
			provider = a.activeCloudProvider()
		}
		if !a.cloudProviderConnected(provider) {
			fail(w, http.StatusBadRequest, "请先连接 "+cloudProviderDisplayName(provider))
			return
		}
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		forceDownload := r.URL.Query().Get("download") == "1"
		hintName := strings.TrimSpace(r.URL.Query().Get("name"))
		hintMime := strings.TrimSpace(r.URL.Query().Get("mime"))
		var (
			fileName  string
			mimeHint  string
			totalSize int64
		)
		switch provider {
		case "onedrive":
			if id == "" || id == "root" || !onedriveItemIDPattern.MatchString(id) {
				fail(w, http.StatusBadRequest, "无效的 OneDrive 文件 ID")
				return
			}
			if hintName != "" && hintMime != "folder" {
				fileName = hintName
				mimeHint = hintMime
			} else {
				meta, code, err := a.onedriveJSON(r.Context(), "GET", msGraphBase+"/me/drive/items/"+url.PathEscape(id)+"?$select=id,name,size,folder,file", nil)
				if err != nil || code >= 300 || meta == nil {
					fail(w, http.StatusBadRequest, "读取 OneDrive 文件信息失败")
					return
				}
				if meta["folder"] != nil {
					fail(w, http.StatusBadRequest, "不能直接预览或下载文件夹")
					return
				}
				fileName = firstNonEmpty(strings.TrimSpace(str(meta["name"])), id)
				mimeHint = strings.TrimSpace(str(object(meta["file"])["mimeType"]))
				totalSize = num(meta["size"])
			}
		case "webdav":
			rel := cleanWebDAVRelPath(id)
			if rel == "" {
				fail(w, http.StatusBadRequest, "无效的 WebDAV 文件路径")
				return
			}
			id = rel
			fileName = firstNonEmpty(hintName, path.Base(rel))
			mimeHint = hintMime
		default:
			if id == "" || id == "root" || !driveFolderID.MatchString(id) {
				fail(w, http.StatusBadRequest, "无效的云盘文件 ID")
				return
			}
			if hintName != "" && hintMime != "" {
				fileName = hintName
				mimeHint = hintMime
			} else {
				meta, code, err := a.driveJSON(r.Context(), "GET", driveBase+"/files/"+url.PathEscape(id)+"?fields=id,name,mimeType,size,trashed&supportsAllDrives=true", nil)
				if err != nil || code >= 300 || meta == nil {
					fail(w, http.StatusBadRequest, "读取 Google Drive 文件信息失败")
					return
				}
				mimeHint = strings.TrimSpace(str(meta["mimeType"]))
				fileName = firstNonEmpty(strings.TrimSpace(str(meta["name"])), id)
				totalSize, _ = strconv.ParseInt(str(meta["size"]), 10, 64)
			}
			if mimeHint == "application/vnd.google-apps.folder" {
				fail(w, http.StatusBadRequest, "不能直接预览或下载文件夹")
				return
			}
			if strings.HasPrefix(strings.ToLower(mimeHint), "application/vnd.google-apps.") {
				var exportMime, contentType, extSuffix string
				switch strings.ToLower(mimeHint) {
				case "application/vnd.google-apps.document":
					exportMime = "text/plain"
					contentType = "text/plain; charset=utf-8"
					extSuffix = ".txt"
				case "application/vnd.google-apps.spreadsheet":
					exportMime = "text/csv"
					contentType = "text/plain; charset=utf-8"
					if forceDownload {
						contentType = "text/csv; charset=utf-8"
					}
					extSuffix = ".csv"
				case "application/vnd.google-apps.presentation", "application/vnd.google-apps.drawing":
					exportMime = "application/pdf"
					contentType = "application/pdf"
					extSuffix = ".pdf"
				case "application/vnd.google-apps.script":
					exportMime = "application/vnd.google-apps.script+json"
					contentType = "text/plain; charset=utf-8"
					extSuffix = ".json"
				default:
					fail(w, http.StatusBadRequest, "该云端文档格式暂不支持直接导出")
					return
				}
				if forceDownload && extSuffix != "" && !strings.HasSuffix(strings.ToLower(fileName), extSuffix) {
					fileName += extSuffix
				}
				expRes, expErr := a.driveRequest(r.Context(), "GET", driveBase+"/files/"+url.PathEscape(id)+"/export?mimeType="+url.QueryEscape(exportMime), nil, map[string]string{
					"Accept-Encoding": "identity",
				})
				if expErr != nil {
					fail(w, http.StatusBadGateway, expErr.Error())
					return
				}
				defer expRes.Body.Close()
				if expRes.StatusCode >= 300 {
					fail(w, http.StatusBadGateway, fmt.Sprintf("导出云端文档失败（HTTP %d）", expRes.StatusCode))
					return
				}
				if limit := parsePreviewRangeLimit(r.Header.Get("Range")); limit > 0 && !forceDownload && contentType != "application/pdf" {
					buf, readErr := io.ReadAll(io.LimitReader(expRes.Body, int64(limit+1)))
					if readErr != nil {
						fail(w, http.StatusBadGateway, "读取导出内容失败")
						return
					}
					if len(buf) > limit {
						w.Header().Set("X-Preview-Clipped", "1")
						buf = buf[:limit]
					}
					writePreviewHeaders(w, fileName, contentType, true, false)
					w.Header().Set("Content-Length", strconv.Itoa(len(buf)))
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write(buf)
					return
				}
				writePreviewHeaders(w, fileName, contentType, true, forceDownload)
				w.WriteHeader(http.StatusOK)
				_, _ = io.Copy(w, expRes.Body)
				return
			}
		}

		upstream, err := a.openCloudRangeResponse(r.Context(), provider, id, "GET", r.Header.Get("Range"))
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		defer upstream.Body.Close()
		if upstream.StatusCode >= 300 {
			fail(w, http.StatusBadGateway, fmt.Sprintf("读取云端文件内容失败（HTTP %d）", upstream.StatusCode))
			return
		}
		head := make([]byte, 512)
		n, _ := io.ReadFull(upstream.Body, head)
		if mimeHint == "" {
			mimeHint = upstream.Header.Get("Content-Type")
		}
		contentType, inline := filePreviewContentType(fileName, mimeHint, head[:n])
		writePreviewHeaders(w, fileName, contentType, inline, forceDownload)
		for _, hdr := range []string{"Content-Range", "Accept-Ranges", "Content-Length"} {
			if val := upstream.Header.Get(hdr); val != "" {
				w.Header().Set(hdr, val)
			}
		}
		if limit := parsePreviewRangeLimit(r.Header.Get("Range")); limit > 0 && !forceDownload {
			if totalSize == 0 {
				if cr := upstream.Header.Get("Content-Range"); cr != "" {
					if slash := strings.LastIndexByte(cr, '/'); slash >= 0 {
						totalSize, _ = strconv.ParseInt(strings.TrimSpace(cr[slash+1:]), 10, 64)
					}
				}
			}
			if totalSize > int64(limit) {
				w.Header().Set("X-Preview-Clipped", "1")
			}
		}
		w.WriteHeader(upstream.StatusCode)
		if n > 0 {
			_, _ = w.Write(head[:n])
		}
		_, _ = io.Copy(w, upstream.Body)
	})
}
