package app

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	DataDirName       = ".bgmcat"
	legacyDataDirName = ".anime-rss-go"
)

var (
	hexGID24RE          = regexp.MustCompile(`^[0-9a-f]{24}$`)
	firstGroupTagRE     = regexp.MustCompile(`^\s*(?:\[([^\]]{1,48})\]|【([^】]{1,48})】)`)
	leadingGroupTagRE   = regexp.MustCompile(`^(?:\s*(?:\[[^\]]{1,48}\]|【[^】]{1,48}】)\s*)+`)
	seriesEpisodeCutRE  = regexp.MustCompile(`(?i)(?:\s+[-–—]\s*(?:第?\s*)?\d{1,4}(?:\.\d+)?(?:\s*v\d)?(?:\b|\s|\[|\(|$)|\s*(?:\[|【|\()\s*(?:第?\s*\d{1,4}(?:\.\d+)?(?:\s*[-~]\s*\d{1,4}(?:\.\d+)?)?\s*(?:[话話集]|v\d)?|ep\.?\s*\d{1,4}|ova|oad|sp|fin|end|complete|全集|合集|\d{3,4}[pP]|4[kK]|2160[pP]|1080[pP]|720[pP]|bdrip|webrip|web-dl|hevc|avc|x264|x265|10bit|8bit|aac|flac|chs|cht|gb|big5|jp&cn|chs&jpn|简日|繁日|简繁)\b.*$|\s+第\s*\d{1,4}(?:\s*[-~]\s*\d{1,4})?\s*[话話集].*$)`)
	knownMediaFileExtRE = regexp.MustCompile(`(?i)\.(?:mp4|mkv|webm|avi|ts|m2ts|mov|m4v|mp3|flac|m4a|ogg|opus|wav|torrent|zip|7z|rar)$`)
	unsafeFolderCharsRE = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]+`)
	multiSpaceRE        = regexp.MustCompile(`\s+`)
)

// ResolveEnv returns the first non-empty environment variable among primary and legacy, or def.
func ResolveEnv(primary, legacy, def string) string {
	if v := strings.TrimSpace(os.Getenv(primary)); v != "" {
		return v
	}
	if legacy != "" {
		if v := strings.TrimSpace(os.Getenv(legacy)); v != "" {
			return v
		}
	}
	return def
}

// DefaultDataDir returns the default user data directory (~/.bgmcat) and migrates
// ~/.anime-rss-go in place on first launch after upgrading.
func DefaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "data"
	}
	target := filepath.Join(home, DataDirName)
	MigrateLegacyHomeDataDir(target)
	return target
}

// MigrateLegacyHomeDataDir atomically renames ~/.anime-rss-go to ~/.bgmcat when
// the target default directory does not yet exist.
func MigrateLegacyHomeDataDir(target string) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return
	}
	cleanTarget := filepath.Clean(target)
	expectedDefault := filepath.Join(filepath.Clean(home), DataDirName)
	if cleanTarget != expectedDefault {
		return
	}
	if _, err := os.Stat(cleanTarget); err == nil {
		return
	}
	legacy := filepath.Join(filepath.Clean(home), legacyDataDirName)
	info, err := os.Stat(legacy)
	if err != nil || !info.IsDir() {
		return
	}
	_ = os.Rename(legacy, cleanTarget)
}

func (a *App) downloadsDir() string {
	if d := strings.TrimSpace(str(a.Settings()["downloadDir"])); d != "" {
		return d
	}
	return filepath.Join(a.cfg.DataDir, "downloads")
}

func (a *App) cacheDir() string {
	return filepath.Join(a.cfg.DataDir, "cache")
}

func (a *App) hlsCacheDir() string {
	return filepath.Join(a.cfg.DataDir, "cache", "hls")
}

func (a *App) thumbnailsDir() string {
	return filepath.Join(a.cfg.DataDir, "cache", "thumbnails")
}

func (a *App) stateDir() string {
	return filepath.Join(a.cfg.DataDir, "state")
}

func (a *App) btClientStateDir() string {
	return filepath.Join(a.cfg.DataDir, "state", "bt")
}

func (a *App) btLoopbackStateDir() string {
	return filepath.Join(a.cfg.DataDir, "state", "bt-loopback")
}

func (a *App) btTaskStateDir(gid string) string {
	clean := safeName(strings.TrimSpace(gid))
	if clean == "" || clean == "file" && gid != "file" {
		clean = "default"
	}
	return filepath.Join(a.cfg.DataDir, "state", "bt-tasks", clean)
}

func sanitizeFolderName(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = unsafeFolderCharsRE.ReplaceAllString(s, " ")
	s = multiSpaceRE.ReplaceAllString(s, " ")
	s = strings.Trim(s, " .")
	if s == "" || s == "." || s == ".." {
		return ""
	}
	if utf8.RuneCountInString(s) > 96 {
		runes := []rune(s)
		s = strings.Trim(string(runes[:96]), " .")
	}
	return s
}

func extractSeriesFolderName(animeTitle, rawTitle string) string {
	if clean := sanitizeFolderName(animeTitle); clean != "" {
		return clean
	}
	s := strings.TrimSpace(rawTitle)
	if s == "" {
		return ""
	}
	s = knownMediaFileExtRE.ReplaceAllString(s, "")
	if stripped := strings.TrimSpace(leadingGroupTagRE.ReplaceAllString(s, "")); stripped != "" {
		s = stripped
	}
	if cut := strings.TrimSpace(seriesEpisodeCutRE.ReplaceAllString(s, "")); cut != "" {
		s = cut
	}
	if strings.Contains(s, " / ") {
		for _, part := range strings.Split(s, " / ") {
			if candidate := sanitizeFolderName(part); candidate != "" {
				return candidate
			}
		}
	}
	return sanitizeFolderName(s)
}

func validDownloadFolderMode(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "anime", "anime_subgroup", "subgroup", "subgroup_anime", "flat":
		return true
	default:
		return false
	}
}

func normalizeDownloadFolderMode(mode string) string {
	m := strings.TrimSpace(mode)
	if validDownloadFolderMode(m) {
		return m
	}
	return "anime_subgroup"
}

func extractSubgroupFolderName(rawTitle string) string {
	m := firstGroupTagRE.FindStringSubmatch(strings.TrimSpace(rawTitle))
	if len(m) < 3 {
		return ""
	}
	tag := strings.TrimSpace(m[1])
	if tag == "" {
		tag = strings.TrimSpace(m[2])
	}
	return sanitizeFolderName(tag)
}

func buildDownloadSubfolders(mode, animeTitle, rawTitle string) []string {
	m := normalizeDownloadFolderMode(mode)
	if m == "flat" {
		return nil
	}
	anime := extractSeriesFolderName(animeTitle, rawTitle)
	subgroup := extractSubgroupFolderName(rawTitle)
	switch m {
	case "anime_subgroup":
		var out []string
		if anime != "" {
			out = append(out, anime)
		}
		if subgroup != "" {
			out = append(out, subgroup)
		}
		return out
	case "subgroup":
		if subgroup != "" {
			return []string{subgroup}
		}
		if anime != "" {
			return []string{anime}
		}
		return nil
	case "subgroup_anime":
		var out []string
		if subgroup != "" {
			out = append(out, subgroup)
		}
		if anime != "" {
			out = append(out, anime)
		}
		return out
	default: // "anime"
		if anime != "" {
			return []string{anime}
		}
		return nil
	}
}

func isLegacyAnimeRSSPath(p string) bool {
	if p == "" {
		return false
	}
	slash := filepath.ToSlash(filepath.Clean(p))
	return strings.Contains(slash, "/"+legacyDataDirName+"/") || strings.HasSuffix(slash, "/"+legacyDataDirName)
}

func rewriteLegacyDataPath(p, newDataDir string) (string, bool) {
	if p == "" || newDataDir == "" {
		return p, false
	}
	clean := filepath.Clean(p)
	slash := filepath.ToSlash(clean)
	marker := "/" + legacyDataDirName + "/"
	if idx := strings.Index(slash, marker); idx >= 0 {
		rel := slash[idx+len(marker):]
		return filepath.Join(newDataDir, filepath.FromSlash(rel)), true
	}
	suffix := "/" + legacyDataDirName
	if strings.HasSuffix(slash, suffix) {
		return filepath.Clean(newDataDir), true
	}
	return p, false
}

// migrateDataDirLayout normalizes ~/.bgmcat into clean subdirectories (downloads,
// cache, state, backups), removes obsolete aria2 leftovers, rewrites legacy
// ~/.anime-rss-go paths in SQLite, and migrates cryptic 24-hex GID folders inside
// downloads/ into human-readable anime folders.
func (a *App) migrateDataDirLayout() {
	dataDir := a.cfg.DataDir
	if dataDir == "" {
		return
	}

	// 1. Clean root-level legacy runtime clutter and relocate cache/state directories.
	if entries, err := os.ReadDir(dataDir); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			full := filepath.Join(dataDir, name)
			switch {
			case name == "aria2" || strings.HasPrefix(name, "aria2-run-") || name == "bt-loopback" || (entry.IsDir() && strings.HasPrefix(name, "hls-")):
				_ = os.RemoveAll(full)
			case entry.IsDir() && name == "thumbnails":
				target := a.thumbnailsDir()
				_ = os.MkdirAll(filepath.Dir(target), 0700)
				if _, statErr := os.Stat(target); os.IsNotExist(statErr) {
					_ = os.Rename(full, target)
				} else {
					moveDirEntries(full, target)
					_ = os.RemoveAll(full)
				}
			case entry.IsDir() && name == "bt-state":
				target := a.btClientStateDir()
				_ = os.MkdirAll(filepath.Dir(target), 0700)
				if _, statErr := os.Stat(target); os.IsNotExist(statErr) {
					_ = os.Rename(full, target)
				} else {
					moveDirEntries(full, target)
					_ = os.RemoveAll(full)
				}
			}
		}
	}

	// 2. Ensure downloadDir in settings points to ~/.bgmcat/downloads if it was legacy or empty.
	s := a.Settings()
	curDL := strings.TrimSpace(str(s["downloadDir"]))
	defaultDL := filepath.Join(dataDir, "downloads")
	if curDL == "" || isLegacyAnimeRSSPath(curDL) {
		if rewritten, changed := rewriteLegacyDataPath(curDL, dataDir); changed {
			curDL = rewritten
		} else {
			curDL = defaultDL
		}
		_, _ = a.Store.Patch("config", "settings", Obj{"downloadDir": curDL})
	}
	_ = os.MkdirAll(curDL, 0700)
	_ = os.MkdirAll(a.hlsCacheDir(), 0700)
	_ = os.MkdirAll(a.thumbnailsDir(), 0700)
	_ = os.MkdirAll(a.stateDir(), 0700)

	// 3. Rewrite legacy paths on subscriptions if any downloadDirOverride referenced .anime-rss-go.
	subsByID := map[string]Obj{}
	if subs, err := a.Store.List("subscriptions"); err == nil {
		for _, sub := range subs {
			sid := str(sub["id"])
			subsByID[sid] = sub
			if ov := strings.TrimSpace(str(sub["downloadDirOverride"])); isLegacyAnimeRSSPath(ov) {
				if nextOv, ok := rewriteLegacyDataPath(ov, dataDir); ok {
					_, _ = a.Store.Patch("subscriptions", sid, Obj{"downloadDirOverride": nextOv})
				}
			}
		}
	}

	// 4. Rewrite legacy paths in downloads and tidy 24-hex GID folders into anime folders.
	jobs, err := a.Store.List("downloads")
	if err != nil {
		return
	}
	cleanDownloadRoot := filepath.Clean(curDL)
	for _, j := range jobs {
		gid := str(j["gid"])
		if gid == "" {
			continue
		}
		patch := Obj{}
		dir := str(j["directory"])
		if nextDir, changed := rewriteLegacyDataPath(dir, dataDir); changed {
			dir = nextDir
			patch["directory"] = dir
		}
		if fp := str(j["filePath"]); fp != "" {
			if nextFP, changed := rewriteLegacyDataPath(fp, dataDir); changed {
				patch["filePath"] = nextFP
			}
		}
		rawFiles := list(j["files"])
		filesChanged := false
		for i, rawF := range rawFiles {
			fObj := object(rawF)
			if fp := str(fObj["path"]); fp != "" {
				if nextFP, changed := rewriteLegacyDataPath(fp, dataDir); changed {
					fObj["path"] = nextFP
					rawFiles[i] = fObj
					filesChanged = true
				}
			}
		}

		// Relocate .torrent.bolt.db and <infohash>.torrent out of downloads/ into state/bt-tasks/<gid>/.
		if dir != "" {
			relocateTaskEngineFiles(dir, a.btTaskStateDir(gid))
		}

		// If job directory is <downloadRoot>/<24-hex-gid>, organize it into <downloadRoot>/<AnimeTitle>/.
		if dir != "" && hexGID24RE.MatchString(filepath.Base(dir)) && filepath.Clean(filepath.Dir(dir)) == cleanDownloadRoot {
			subTitle := ""
			if sid := str(j["feedItemSubscriptionId"]); sid != "" {
				subTitle = str(subsByID[sid]["name"])
			}
			animeTitle := firstNonEmpty(str(j["animeTitle"]), subTitle)
			fallbackTitle := firstNonEmpty(str(j["feedItemTitle"]), filepath.Base(str(j["filePath"])))
			folderName := extractSeriesFolderName(animeTitle, fallbackTitle)
			if folderName != "" && !hexGID24RE.MatchString(strings.ToLower(folderName)) {
				targetDir := filepath.Join(cleanDownloadRoot, folderName)
				if migrated, newPrimary, updatedFiles := migrateHexTaskFolder(dir, targetDir, str(firstNonEmptyAny(patch["filePath"], j["filePath"])), rawFiles); migrated {
					dir = targetDir
					patch["directory"] = targetDir
					if newPrimary != "" {
						patch["filePath"] = newPrimary
					}
					rawFiles = updatedFiles
					filesChanged = true
				}
			}
		}

		if filesChanged {
			patch["files"] = rawFiles
		}
		if len(patch) > 0 {
			_, _ = a.Store.Patch("downloads", gid, patch)
		}
	}
}

func firstNonEmptyAny(values ...any) any {
	for _, v := range values {
		if s := strings.TrimSpace(str(v)); s != "" {
			return s
		}
	}
	return nil
}

func moveDirEntries(srcDir, dstDir string) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
	}
	_ = os.MkdirAll(dstDir, 0700)
	for _, entry := range entries {
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(dstDir, entry.Name())
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			_ = os.Rename(src, dst)
		}
	}
}

func relocateTaskEngineFiles(downloadTaskDir, stateTaskDir string) {
	entries, err := os.ReadDir(downloadTaskDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == ".torrent.bolt.db" || infoHashTorrentRE.MatchString(name) {
			_ = os.MkdirAll(stateTaskDir, 0700)
			dst := filepath.Join(stateTaskDir, name)
			src := filepath.Join(downloadTaskDir, name)
			if _, statErr := os.Stat(dst); os.IsNotExist(statErr) {
				_ = os.Rename(src, dst)
			} else {
				_ = os.Remove(src)
			}
		}
	}
}

func migrateHexTaskFolder(oldDir, targetDir, oldPrimary string, rawFiles []any) (bool, string, []any) {
	oldClean := filepath.Clean(oldDir)
	targetClean := filepath.Clean(targetDir)
	if oldClean == targetClean {
		return false, oldPrimary, rawFiles
	}

	// Verify no destination collision before moving any tracked file.
	type movePlan struct {
		idx int
		obj Obj
		src string
		dst string
	}
	plans := make([]movePlan, 0, len(rawFiles))
	for i, rawF := range rawFiles {
		fObj := object(rawF)
		fp := str(fObj["path"])
		if fp == "" {
			continue
		}
		rel, err := filepath.Rel(oldClean, filepath.Clean(fp))
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false, oldPrimary, rawFiles
		}
		dst := filepath.Join(targetClean, rel)
		if _, srcErr := os.Stat(fp); srcErr == nil {
			if _, dstErr := os.Stat(dst); dstErr == nil {
				// Destination file already exists; skip moving to avoid overwriting another task's file.
				return false, oldPrimary, rawFiles
			}
		}
		plans = append(plans, movePlan{idx: i, obj: fObj, src: fp, dst: dst})
	}

	if _, statErr := os.Stat(oldClean); statErr == nil {
		if err := os.MkdirAll(targetClean, 0700); err != nil {
			return false, oldPrimary, rawFiles
		}
	}

	for _, p := range plans {
		if _, srcErr := os.Stat(p.src); srcErr == nil {
			if err := os.MkdirAll(filepath.Dir(p.dst), 0700); err != nil {
				return false, oldPrimary, rawFiles
			}
			if err := os.Rename(p.src, p.dst); err != nil {
				return false, oldPrimary, rawFiles
			}
		}
		p.obj["path"] = p.dst
		rawFiles[p.idx] = p.obj
	}

	newPrimary := oldPrimary
	if oldPrimary != "" {
		if rel, err := filepath.Rel(oldClean, filepath.Clean(oldPrimary)); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			newPrimary = filepath.Join(targetClean, rel)
		}
	}

	// Clean any stray control files and remove the old 24-hex folder if now empty.
	removeAria2Leftovers(oldClean, filepath.Base(oldClean))
	_ = os.Remove(oldClean)
	return true, newPrimary, rawFiles
}

func measurePathUsage(target string) (bytes int64, files int64) {
	info, err := os.Lstat(target)
	if err != nil {
		return 0, 0
	}
	if !info.IsDir() {
		if info.Mode().IsRegular() {
			return info.Size(), 1
		}
		return 0, 0
	}
	_ = filepath.WalkDir(target, func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if fi, e := d.Info(); e == nil && fi.Mode().IsRegular() {
			bytes += fi.Size()
			files++
		}
		return nil
	})
	return bytes, files
}

func measureSQLiteFiles(dbPath string) (bytes int64, files int64) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(dbPath + suffix); err == nil && info.Mode().IsRegular() {
			bytes += info.Size()
			files++
		}
	}
	return bytes, files
}

func (a *App) collectStorageUsage() Obj {
	dataDir := a.cfg.DataDir
	dlDir := a.downloadsDir()
	dbPath := filepath.Join(dataDir, "app.db")
	cacheDir := a.cacheDir()
	stateDir := a.stateDir()
	backupDir := a.backupDir()

	dlBytes, dlFiles := measurePathUsage(dlDir)
	dbBytes, dbFiles := measureSQLiteFiles(dbPath)
	cacheBytes, cacheFiles := measurePathUsage(cacheDir)
	stateBytes, stateFiles := measurePathUsage(stateDir)
	backupBytes, backupFiles := measurePathUsage(backupDir)

	appBytes := dbBytes + cacheBytes + stateBytes + backupBytes
	appFiles := dbFiles + cacheFiles + stateFiles + backupFiles
	if isPathWithinRoot(dataDir, dlDir) {
		appBytes += dlBytes
		appFiles += dlFiles
	} else {
		// Still include downloads in total managed bytes for clear user accounting.
		appBytes += dlBytes
		appFiles += dlFiles
	}

	freeBytes, totalBytes := diskUsage(dataDir)
	return Obj{
		"dataDir":        dataDir,
		"downloadDir":    dlDir,
		"diskTotalBytes": totalBytes,
		"diskFreeBytes":  freeBytes,
		"appBytes":       appBytes,
		"appFiles":       appFiles,
		"cacheBytes":     cacheBytes,
		"directories": []Obj{
			{"key": "downloads", "label": "番剧下载", "path": dlDir, "bytes": dlBytes, "files": dlFiles},
			{"key": "database", "label": "数据库", "path": dbPath, "bytes": dbBytes, "files": dbFiles},
			{"key": "cache", "label": "播放与缩略图缓存", "path": cacheDir, "bytes": cacheBytes, "files": cacheFiles},
			{"key": "state", "label": "做种与任务状态", "path": stateDir, "bytes": stateBytes, "files": stateFiles},
			{"key": "backups", "label": "数据备份", "path": backupDir, "bytes": backupBytes, "files": backupFiles},
		},
	}
}

func (a *App) registerStorageRoutes() {
	a.handle("GET /api/system/storage", func(w http.ResponseWriter, r *http.Request) error {
		ok(w, a.collectStorageUsage())
		return nil
	})
}
