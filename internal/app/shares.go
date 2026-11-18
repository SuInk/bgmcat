package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxShareItems       = 500
	maxShareTitleRunes  = 160
	maxPasscodeRunes    = 32
	sharePruneGraceDays = 30
)

var shareCodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,32}$`)
var shareEpisodeNumPattern = regexp.MustCompile(`(?i)(?:第\s*0*(\d{1,4}(?:\.\d+)?)\s*[话話集]|(?:\bep?|(?:^|\s)-\s*|\[\s*)0*(\d{1,3}(?:\.\d+)?)(?:\s*v\d+)?(?:\b|\]|\s|$))`)

func generateShareCode() (string, error) {
	b := make([]byte, 9)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func validateSharePasscode(raw string) (string, error) {
	code := strings.TrimSpace(raw)
	if code == "" {
		return "", nil
	}
	if utf8.RuneCountInString(code) > maxPasscodeRunes {
		return "", fmt.Errorf("提取码最多 %d 个字符", maxPasscodeRunes)
	}
	for _, r := range code {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", errors.New("提取码不能包含空格或控制字符")
		}
	}
	return code, nil
}

func (a *App) shareSecret() []byte {
	rec, err := a.Store.Get("secrets", "share_secret")
	if err == nil && rec != nil {
		if raw, dErr := hex.DecodeString(str(rec["key"])); dErr == nil && len(raw) >= 32 {
			return raw
		}
	}
	keyHex := randomID(32)
	_ = a.Store.Put("secrets", "share_secret", Obj{"key": keyHex, "createdAt": now()})
	raw, _ := hex.DecodeString(keyHex)
	return raw
}

func (a *App) computeShareUnlockToken(share Obj) string {
	passcode := str(share["passcode"])
	if passcode == "" {
		return ""
	}
	mac := hmac.New(sha256.New, a.shareSecret())
	_, _ = mac.Write([]byte(str(share["code"])))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(passcode))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(str(share["expiresAt"])))
	return hex.EncodeToString(mac.Sum(nil))
}

func shareCookieName(code string) string {
	return "bgmcat_share_" + code
}

func parseShareExpiry(share Obj) (time.Time, bool) {
	raw := strings.TrimSpace(str(share["expiresAt"]))
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func isShareExpired(share Obj, ref time.Time) bool {
	exp, hasExp := parseShareExpiry(share)
	if !hasExp {
		return false
	}
	return !ref.Before(exp)
}

func (a *App) verifyShareUnlocked(r *http.Request, share Obj) (bool, string) {
	if str(share["passcode"]) == "" {
		return true, ""
	}
	expected := a.computeShareUnlockToken(share)
	if expected == "" {
		return true, ""
	}
	candidates := []string{
		strings.TrimSpace(r.Header.Get("X-Share-Token")),
		strings.TrimSpace(r.URL.Query().Get("st")),
	}
	if c, err := r.Cookie(shareCookieName(str(share["code"]))); err == nil && c.Value != "" {
		candidates = append(candidates, strings.TrimSpace(c.Value))
	}
	for _, cand := range candidates {
		if cand != "" && subtle.ConstantTimeCompare([]byte(cand), []byte(expected)) == 1 {
			return true, expected
		}
	}
	return false, ""
}

func naturalLess(a, b string) bool {
	ra := []rune(strings.ToLower(a))
	rb := []rune(strings.ToLower(b))
	ia, ib := 0, 0
	for ia < len(ra) && ib < len(rb) {
		da := unicode.IsDigit(ra[ia])
		db := unicode.IsDigit(rb[ib])
		if da && db {
			ja, jb := ia, ib
			for ja < len(ra) && unicode.IsDigit(ra[ja]) {
				ja++
			}
			for jb < len(rb) && unicode.IsDigit(rb[jb]) {
				jb++
			}
			sa := strings.TrimLeft(string(ra[ia:ja]), "0")
			sb := strings.TrimLeft(string(rb[ib:jb]), "0")
			if len(sa) != len(sb) {
				return len(sa) < len(sb)
			}
			if sa != sb {
				return sa < sb
			}
			if ja-ia != jb-ib {
				return ja-ia < jb-ib
			}
			ia, ib = ja, jb
			continue
		}
		if ra[ia] != rb[ib] {
			return ra[ia] < rb[ib]
		}
		ia++
		ib++
	}
	return len(ra) < len(rb)
}

func inferShareItemLabel(fileName string, explicitLabel string, multi bool) string {
	if clean := strings.TrimSpace(explicitLabel); clean != "" {
		return clean
	}
	base := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if multi {
		if m := shareEpisodeNumPattern.FindStringSubmatch(base); len(m) >= 3 {
			ep := m[1]
			if ep == "" {
				ep = m[2]
			}
			if ep != "" {
				if n, err := strconv.Atoi(ep); err == nil {
					return fmt.Sprintf("第 %02d 集", n)
				}
				return "第 " + ep + " 集"
			}
		}
	}
	if base != "" {
		return base
	}
	return fileName
}

func selectDownloadShareFile(job Obj) (Obj, error) {
	if f, err := mediaFile(job); err == nil && f != nil {
		return f, nil
	}
	var best Obj
	var bestSize int64 = -1
	for _, raw := range list(job["files"]) {
		f := object(raw)
		if str(f["path"]) == "" && str(f["remoteId"]) == "" {
			continue
		}
		if sz := num(f["size"]); sz > bestSize {
			bestSize = sz
			best = f
		}
	}
	if best == nil {
		return nil, errors.New("该下载任务没有可分享的文件")
	}
	return best, nil
}

func sanitizeShareFileName(raw string) string {
	clean := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "_")
	clean = strings.ReplaceAll(clean, "/", "_")
	clean = filepath.Base(clean)
	if clean == "" || clean == "." || clean == ".." {
		return "file"
	}
	return clean
}

func (a *App) resolveShareTargets(ctx context.Context, rawTargets []any) ([]Obj, string, error) {
	if len(rawTargets) == 0 {
		return nil, "", errors.New("请至少选择一个要分享的项目")
	}
	var collected []Obj
	defaultTitle := ""

	for _, raw := range rawTargets {
		t := object(raw)
		kind := strings.TrimSpace(str(t["kind"]))
		explicitLabel := strings.TrimSpace(str(t["label"]))

		switch kind {
		case "download":
			gid := strings.TrimSpace(str(t["gid"]))
			if gid == "" {
				return nil, "", errors.New("下载任务标识无效")
			}
			job, err := a.Store.Get("downloads", gid)
			if err != nil || job == nil {
				return nil, "", errors.New("下载任务不存在")
			}
			f, err := selectDownloadShareFile(job)
			if err != nil {
				return nil, "", err
			}
			filePath := str(f["path"])
			fileName := sanitizeShareFileName(filepath.Base(filePath))
			size := num(f["size"])
			playable := isPlayableExt(filepath.Ext(fileName))
			// Verify either local file exists/streams or cloud copy is verified.
			localReady := false
			if filePath != "" {
				if fi, sErr := os.Stat(filePath); sErr == nil && fi.Mode().IsRegular() {
					localReady = true
					if size <= 0 {
						size = fi.Size()
					}
				} else if a.engine != nil && str(job["status"]) == "active" {
					if rc, _, ok := a.engine.OpenStream(ctx, gid, filePath); ok {
						_ = rc.Close()
						localReady = true
					}
				}
			}
			cloudReady := str(f["remoteId"]) != "" && flag(f["verified"])
			if !localReady && !cloudReady {
				return nil, "", fmt.Errorf("文件尚未准备就绪：%s", fileName)
			}
			if defaultTitle == "" {
				defaultTitle = strings.TrimSpace(str(job["feedItemTitle"]))
				if defaultTitle == "" {
					defaultTitle = strings.TrimSuffix(fileName, filepath.Ext(fileName))
				}
			}
			mediaID := ""
			if playable {
				mediaID = gid
			}
			collected = append(collected, Obj{
				"sourceKind": "download",
				"gid":        gid,
				"mediaId":    mediaID,
				"label":      explicitLabel,
				"fileName":   fileName,
				"size":       size,
				"playable":   playable,
			})

		case "local":
			root, cleanRel, realTarget, info, err := a.resolveExistingLocalPath(str(t["path"]))
			if err != nil {
				return nil, "", err
			}
			if info.IsDir() {
				if defaultTitle == "" {
					if cleanRel == "" {
						defaultTitle = "本地下载目录"
					} else {
						defaultTitle = filepath.Base(realTarget)
					}
				}
				entries, rErr := os.ReadDir(realTarget)
				if rErr != nil {
					return nil, "", rErr
				}
				type localChild struct {
					name string
					rel  string
					size int64
				}
				var children []localChild
				for _, de := range entries {
					name := de.Name()
					if shouldSkipSystemHiddenFile(name) || strings.HasPrefix(name, ".") || strings.HasSuffix(strings.ToLower(name), ".part") || strings.HasSuffix(strings.ToLower(name), ".aria2") {
						continue
					}
					childJoined := filepath.Join(realTarget, name)
					realChild, e := filepath.EvalSymlinks(childJoined)
					if e != nil || !isPathWithinRoot(root, realChild) {
						continue
					}
					fi, e := os.Stat(realChild)
					if e != nil || !fi.Mode().IsRegular() {
						continue
					}
					childRel := name
					if cleanRel != "" {
						childRel = cleanRel + "/" + name
					}
					children = append(children, localChild{name: name, rel: childRel, size: fi.Size()})
				}
				sort.Slice(children, func(i, j int) bool {
					return naturalLess(children[i].name, children[j].name)
				})
				for _, ch := range children {
					playable := isPlayableExt(filepath.Ext(ch.name))
					mediaID := ""
					if playable {
						mediaID = encodeLocalMediaID(ch.rel)
					}
					collected = append(collected, Obj{
						"sourceKind": "local",
						"relPath":    ch.rel,
						"mediaId":    mediaID,
						"label":      "",
						"fileName":   ch.name,
						"size":       ch.size,
						"playable":   playable,
					})
				}
			} else if info.Mode().IsRegular() {
				name := info.Name()
				if defaultTitle == "" {
					defaultTitle = strings.TrimSuffix(name, filepath.Ext(name))
				}
				playable := isPlayableExt(filepath.Ext(name))
				mediaID := ""
				if playable {
					mediaID = encodeLocalMediaID(cleanRel)
				}
				collected = append(collected, Obj{
					"sourceKind": "local",
					"relPath":    cleanRel,
					"mediaId":    mediaID,
					"label":      explicitLabel,
					"fileName":   name,
					"size":       info.Size(),
					"playable":   playable,
				})
			}

		case "cloud":
			provider := firstNonEmpty(strings.TrimSpace(str(t["provider"])), a.activeCloudProvider())
			if !validCloudProvider(provider) {
				provider = "google"
			}
			if !a.cloudProviderConnected(provider) {
				return nil, "", errors.New(cloudProviderDisplayName(provider) + " 未连接")
			}
			driveID := strings.TrimSpace(str(t["id"]))
			if provider == "google" && !driveFolderID.MatchString(driveID) {
				return nil, "", errors.New("云盘条目 ID 无效")
			}
			if provider == "onedrive" && !onedriveItemIDPattern.MatchString(driveID) {
				return nil, "", errors.New("OneDrive 条目 ID 无效")
			}
			if provider == "webdav" {
				driveID = cleanWebDAVRelPath(driveID)
				if driveID == "" && !flag(t["isDir"]) {
					return nil, "", errors.New("WebDAV 条目路径无效")
				}
			}
			name := sanitizeShareFileName(str(t["name"]))
			mimeType := strings.TrimSpace(str(t["mimeType"]))
			isDir := flag(t["isDir"]) || mimeType == "application/vnd.google-apps.folder"
			if isDir {
				if defaultTitle == "" && str(t["name"]) != "" {
					defaultTitle = strings.TrimSpace(str(t["name"]))
				}
				type cloudChild struct {
					id   string
					name string
					mime string
					size int64
				}
				var children []cloudChild
				switch provider {
				case "onedrive":
					resObj, lErr := a.listOneDriveBrowserEntries(ctx, driveID, "", "")
					if lErr != nil {
						return nil, "", errors.New("读取 OneDrive 文件夹内容失败")
					}
					for _, raw := range list(resObj["entries"]) {
						eo := object(raw)
						if flag(eo["isDir"]) {
							continue
						}
						children = append(children, cloudChild{
							id:   str(eo["id"]),
							name: sanitizeShareFileName(str(eo["name"])),
							mime: str(eo["mimeType"]),
							size: num(eo["size"]),
						})
					}
				case "webdav":
					items, lErr := a.webdavPropfind(ctx, a.Settings(), driveID, "1")
					if lErr != nil {
						return nil, "", errors.New("读取 WebDAV 文件夹内容失败")
					}
					for _, it := range items {
						if it.IsDir {
							continue
						}
						children = append(children, cloudChild{
							id:   it.RelPath,
							name: sanitizeShareFileName(it.Name),
							mime: it.Mime,
							size: it.Size,
						})
					}
				default:
					params := url.Values{
						"q":                         {fmt.Sprintf("trashed = false and '%s' in parents and mimeType != 'application/vnd.google-apps.folder'", driveID)},
						"fields":                    {"files(id,name,mimeType,size)"},
						"pageSize":                  {"200"},
						"orderBy":                   {"name"},
						"supportsAllDrives":         {"true"},
						"includeItemsFromAllDrives": {"true"},
					}
					listRes, statusCode, lErr := a.driveJSON(ctx, "GET", driveBase+"/files?"+params.Encode(), nil)
					if lErr != nil || statusCode >= 300 {
						return nil, "", errors.New("读取云盘文件夹内容失败")
					}
					for _, rf := range list(listRes["files"]) {
						fo := object(rf)
						cid := strings.TrimSpace(str(fo["id"]))
						cname := sanitizeShareFileName(str(fo["name"]))
						cmime := strings.TrimSpace(str(fo["mimeType"]))
						if !driveFolderID.MatchString(cid) || strings.HasPrefix(cmime, "application/vnd.google-apps.") {
							continue
						}
						csz, _ := strconv.ParseInt(str(fo["size"]), 10, 64)
						children = append(children, cloudChild{id: cid, name: cname, mime: cmime, size: csz})
					}
				}
				sort.Slice(children, func(i, j int) bool {
					return naturalLess(children[i].name, children[j].name)
				})
				for _, ch := range children {
					ext := strings.ToLower(filepath.Ext(ch.name))
					playable := isPlayableExt(ext) || strings.HasPrefix(strings.ToLower(ch.mime), "video/") || strings.HasPrefix(strings.ToLower(ch.mime), "audio/")
					mediaID := ""
					if playable {
						mediaID = encodeCloudMediaID(provider, ch.id, ch.name, ch.mime)
					}
					collected = append(collected, Obj{
						"sourceKind":    "cloud",
						"cloudProvider": provider,
						"driveId":       ch.id,
						"mimeType":      ch.mime,
						"mediaId":       mediaID,
						"label":         "",
						"fileName":      ch.name,
						"size":          ch.size,
						"playable":      playable,
					})
				}
			} else {
				if defaultTitle == "" {
					defaultTitle = strings.TrimSuffix(name, filepath.Ext(name))
				}
				size := num(t["size"])
				ext := strings.ToLower(filepath.Ext(name))
				playable := isPlayableExt(ext) || strings.HasPrefix(strings.ToLower(mimeType), "video/") || strings.HasPrefix(strings.ToLower(mimeType), "audio/")
				mediaID := ""
				if playable {
					mediaID = encodeCloudMediaID(provider, driveID, name, mimeType)
				}
				collected = append(collected, Obj{
					"sourceKind":    "cloud",
					"cloudProvider": provider,
					"driveId":       driveID,
					"mimeType":      mimeType,
					"mediaId":       mediaID,
					"label":         explicitLabel,
					"fileName":      name,
					"size":          size,
					"playable":      playable,
				})
			}

		default:
			return nil, "", errors.New("不支持的分享目标类型")
		}

		if len(collected) > maxShareItems {
			collected = collected[:maxShareItems]
			break
		}
	}

	if len(collected) == 0 {
		return nil, "", errors.New("所选目录中没有可分享的文件")
	}

	multi := len(collected) > 1
	for i := range collected {
		collected[i]["id"] = strconv.Itoa(i)
		collected[i]["label"] = inferShareItemLabel(str(collected[i]["fileName"]), str(collected[i]["label"]), multi)
	}

	return collected, defaultTitle, nil
}

func formatPublicShareItems(rawItems []any) ([]Obj, int64, int) {
	items := make([]Obj, 0, len(rawItems))
	var totalSize int64
	playableCount := 0
	for i, raw := range rawItems {
		it := object(raw)
		id := str(it["id"])
		if id == "" {
			id = strconv.Itoa(i)
		}
		sz := num(it["size"])
		totalSize += sz
		playable := flag(it["playable"]) && str(it["mediaId"]) != ""
		if playable {
			playableCount++
		}
		fileName := sanitizeShareFileName(str(it["fileName"]))
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
		items = append(items, Obj{
			"id":       id,
			"label":    str(it["label"]),
			"fileName": fileName,
			"ext":      ext,
			"kind":     classifyFileKind(false, ext, str(it["mimeType"])),
			"size":     sz,
			"playable": playable,
		})
	}
	return items, totalSize, playableCount
}

func formatAdminShareRecord(share Obj, ref time.Time) Obj {
	pubItems, totalSize, playableCount := formatPublicShareItems(list(share["items"]))
	passcode := str(share["passcode"])
	expiresAt := str(share["expiresAt"])
	var expiresOut any = nil
	if expiresAt != "" {
		expiresOut = expiresAt
	}
	return Obj{
		"code":          str(share["code"]),
		"title":         str(share["title"]),
		"hasPasscode":   passcode != "",
		"passcode":      passcode,
		"allowDownload": flag(share["allowDownload"]),
		"expiresAt":     expiresOut,
		"expired":       isShareExpired(share, ref),
		"createdAt":     str(share["createdAt"]),
		"viewCount":     num(share["viewCount"]),
		"itemCount":     len(pubItems),
		"playableCount": playableCount,
		"totalSize":     totalSize,
		"items":         pubItems,
	}
}

func formatPublicShareView(share Obj, unlocked bool, unlockToken string) Obj {
	rawItems := list(share["items"])
	pubItems, totalSize, playableCount := formatPublicShareItems(rawItems)
	expiresAt := str(share["expiresAt"])
	var expiresOut any = nil
	if expiresAt != "" {
		expiresOut = expiresAt
	}
	if !unlocked {
		return Obj{
			"code":          str(share["code"]),
			"title":         str(share["title"]),
			"locked":        true,
			"hasPasscode":   true,
			"allowDownload": flag(share["allowDownload"]),
			"expiresAt":     expiresOut,
			"createdAt":     str(share["createdAt"]),
			"itemCount":     len(pubItems),
			"playableCount": playableCount,
			"totalSize":     totalSize,
			"items":         []Obj{},
		}
	}
	out := Obj{
		"code":          str(share["code"]),
		"title":         str(share["title"]),
		"locked":        false,
		"hasPasscode":   str(share["passcode"]) != "",
		"allowDownload": flag(share["allowDownload"]),
		"expiresAt":     expiresOut,
		"createdAt":     str(share["createdAt"]),
		"itemCount":     len(pubItems),
		"playableCount": playableCount,
		"totalSize":     totalSize,
		"items":         pubItems,
	}
	if unlockToken != "" {
		out["unlockToken"] = unlockToken
	}
	return out
}

func findShareItem(share Obj, itemID string) (Obj, bool) {
	for i, raw := range list(share["items"]) {
		it := object(raw)
		id := str(it["id"])
		if id == "" {
			id = strconv.Itoa(i)
		}
		if id == itemID {
			return it, true
		}
	}
	return nil, false
}

func setAttachmentDisposition(w http.ResponseWriter, fileName string) {
	safe := sanitizeShareFileName(fileName)
	asciiFallback := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' || r == ';' {
			return '_'
		}
		return r
	}, safe)
	if asciiFallback == "" {
		asciiFallback = "download"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, asciiFallback, url.PathEscape(safe)))
}

func (a *App) serveCloudFileAttachment(w http.ResponseWriter, r *http.Request, provider, remoteID, fileName string) error {
	if provider == "" {
		provider = "google"
	}
	res, err := a.openCloudRangeResponse(r.Context(), provider, remoteID, r.Method, r.Header.Get("Range"))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 206 && res.StatusCode != 416 {
		return fmt.Errorf("云端读取失败 HTTP %d", res.StatusCode)
	}
	for _, k := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if value := res.Header.Get(k); value != "" {
			w.Header().Set(k, value)
		}
	}
	w.Header().Set("Content-Type", mimeFor(fileName))
	w.Header().Set("Cache-Control", "private, no-store")
	setAttachmentDisposition(w, fileName)
	w.WriteHeader(res.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, res.Body)
	}
	return nil
}

func (a *App) serveShareItemDownload(w http.ResponseWriter, r *http.Request, item Obj) error {
	fileName := sanitizeShareFileName(str(item["fileName"]))
	switch str(item["sourceKind"]) {
	case "download":
		gid := str(item["gid"])
		job, err := a.Store.Get("downloads", gid)
		if err != nil || job == nil {
			return errors.New("原下载任务不存在")
		}
		f, err := selectDownloadShareFile(job)
		if err != nil {
			return err
		}
		localPath := str(f["path"])
		if localPath != "" {
			if localFile, oErr := os.Open(localPath); oErr == nil {
				defer localFile.Close()
				if fi, sErr := localFile.Stat(); sErr == nil && fi.Mode().IsRegular() {
					setAttachmentDisposition(w, fileName)
					http.ServeContent(w, r, fileName, fi.ModTime(), localFile)
					return nil
				}
			}
		}
		remoteID := str(f["remoteId"])
		if remoteID != "" && flag(f["verified"]) {
			prov := firstNonEmpty(str(f["remoteProvider"]), str(job["cloudProvider"]), "google")
			return a.serveCloudFileAttachment(w, r, prov, remoteID, fileName)
		}
		return errors.New("源文件已不存在")

	case "local":
		_, _, realPath, info, err := a.resolveExistingLocalPath(str(item["relPath"]))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("不是可下载的普通文件")
		}
		f, err := os.Open(realPath)
		if err != nil {
			return err
		}
		defer f.Close()
		setAttachmentDisposition(w, fileName)
		http.ServeContent(w, r, fileName, info.ModTime(), f)
		return nil

	case "cloud":
		prov := firstNonEmpty(str(item["cloudProvider"]), "google")
		driveID := str(item["driveId"])
		if prov == "google" && !driveFolderID.MatchString(driveID) {
			return errors.New("云盘文件无效")
		}
		if driveID == "" {
			return errors.New("云盘文件无效")
		}
		return a.serveCloudFileAttachment(w, r, prov, driveID, fileName)

	default:
		return errors.New("不支持的源文件类型")
	}
}

func (a *App) loadAccessibleShare(w http.ResponseWriter, r *http.Request) (Obj, bool, string, bool) {
	code := strings.TrimSpace(r.PathValue("code"))
	if !shareCodePattern.MatchString(code) {
		failWithCode(w, http.StatusNotFound, "SHARE_NOT_FOUND", "分享链接不存在或已失效")
		return nil, false, "", false
	}
	share, err := a.Store.Get("shares", code)
	if err != nil || share == nil {
		failWithCode(w, http.StatusNotFound, "SHARE_NOT_FOUND", "分享链接不存在或已被取消")
		return nil, false, "", false
	}
	if isShareExpired(share, time.Now()) {
		failWithCode(w, http.StatusGone, "SHARE_EXPIRED", "该分享链接已过期")
		return nil, false, "", false
	}
	unlocked, unlockToken := a.verifyShareUnlocked(r, share)
	return share, unlocked, unlockToken, true
}

func (a *App) requireUnlockedShareItem(w http.ResponseWriter, r *http.Request) (Obj, Obj, string, bool) {
	share, unlocked, unlockToken, okShare := a.loadAccessibleShare(w, r)
	if !okShare {
		return nil, nil, "", false
	}
	if !unlocked {
		failWithCode(w, http.StatusUnauthorized, "SHARE_LOCKED", "需要输入提取码后才能访问")
		return nil, nil, "", false
	}
	item, found := findShareItem(share, r.PathValue("itemId"))
	if !found {
		failWithCode(w, http.StatusNotFound, "SHARE_ITEM_NOT_FOUND", "分享条目不存在")
		return nil, nil, "", false
	}
	return share, item, unlockToken, true
}

func (a *App) pruneOldExpiredShares() {
	a.Store.mu.Lock()
	rows, err := a.Store.db.Query("SELECT key,value FROM documents WHERE kind='shares'")
	if err != nil {
		a.Store.mu.Unlock()
		return
	}
	var staleKeys []string
	cutoff := time.Now().Add(-sharePruneGraceDays * 24 * time.Hour)
	for rows.Next() {
		var key, raw string
		if rows.Scan(&key, &raw) == nil {
			var s Obj
			if json.Unmarshal([]byte(raw), &s) == nil {
				if exp, hasExp := parseShareExpiry(s); hasExp && exp.Before(cutoff) {
					staleKeys = append(staleKeys, key)
				}
			}
		}
	}
	rows.Close()
	a.Store.mu.Unlock()
	for _, key := range staleKeys {
		_ = a.Store.Delete("shares", key)
	}
}

func (a *App) shareRoutes() {
	// ---------- 管理端接口（受 /api/ 管理员鉴权保护） ----------

	a.handle("GET /api/shares", func(w http.ResponseWriter, r *http.Request) error {
		docs, err := a.Store.List("shares")
		if err != nil {
			return err
		}
		ref := time.Now()
		out := make([]Obj, 0, len(docs))
		for _, doc := range docs {
			out = append(out, formatAdminShareRecord(doc, ref))
		}
		ok(w, out)
		return nil
	})

	a.handle("POST /api/shares", func(w http.ResponseWriter, r *http.Request) error {
		input, err := body(w, r)
		if err != nil {
			return err
		}
		passcode, err := validateSharePasscode(str(input["passcode"]))
		if err != nil {
			return err
		}
		allowDownload := true
		if input["allowDownload"] != nil {
			allowDownload = flag(input["allowDownload"])
		}
		expiresInHours := num(input["expiresInHours"])
		if expiresInHours < 0 || expiresInHours > 24*365*5 {
			return errors.New("有效期范围无效")
		}
		items, defaultTitle, err := a.resolveShareTargets(r.Context(), list(input["targets"]))
		if err != nil {
			return err
		}
		if !allowDownload {
			anyPlayable := false
			for _, it := range items {
				if flag(it["playable"]) && str(it["mediaId"]) != "" {
					anyPlayable = true
					break
				}
			}
			if !anyPlayable {
				return errors.New("非音视频文件不支持在线播放，请开启「允许下载原文件」")
			}
		}

		title := strings.TrimSpace(str(input["title"]))
		if title == "" {
			title = defaultTitle
		}
		if title == "" {
			title = "分享内容"
		}
		if utf8.RuneCountInString(title) > maxShareTitleRunes {
			runes := []rune(title)
			title = string(runes[:maxShareTitleRunes])
		}

		var code string
		for attempt := 0; attempt < 8; attempt++ {
			cand, gErr := generateShareCode()
			if gErr != nil {
				return gErr
			}
			existing, _ := a.Store.Get("shares", cand)
			if existing == nil {
				code = cand
				break
			}
		}
		if code == "" {
			return errors.New("生成分享码失败，请重试")
		}

		nowTime := time.Now().UTC()
		expiresAt := ""
		if expiresInHours > 0 {
			expiresAt = nowTime.Add(time.Duration(expiresInHours) * time.Hour).Format(time.RFC3339)
		}

		doc := Obj{
			"code":          code,
			"title":         title,
			"passcode":      passcode,
			"allowDownload": allowDownload,
			"expiresAt":     expiresAt,
			"createdAt":     nowTime.Format(time.RFC3339),
			"viewCount":     int64(0),
			"items":         items,
		}
		if err := a.Store.Put("shares", code, doc); err != nil {
			return err
		}
		ok(w, formatAdminShareRecord(doc, nowTime))
		return nil
	})

	a.handle("PATCH /api/shares/{code}", func(w http.ResponseWriter, r *http.Request) error {
		code := strings.TrimSpace(r.PathValue("code"))
		if !shareCodePattern.MatchString(code) {
			return errors.New("分享码无效")
		}
		existing, err := a.Store.Get("shares", code)
		if err != nil || existing == nil {
			fail(w, http.StatusNotFound, "分享不存在")
			return nil
		}
		input, err := body(w, r)
		if err != nil {
			return err
		}
		patch := Obj{}
		if input["title"] != nil {
			t := strings.TrimSpace(str(input["title"]))
			if t == "" {
				return errors.New("标题不能为空")
			}
			if utf8.RuneCountInString(t) > maxShareTitleRunes {
				runes := []rune(t)
				t = string(runes[:maxShareTitleRunes])
			}
			patch["title"] = t
		}
		if input["allowDownload"] != nil {
			nextAllow := flag(input["allowDownload"])
			if !nextAllow {
				anyPlayable := false
				for _, raw := range list(existing["items"]) {
					it := object(raw)
					if flag(it["playable"]) && str(it["mediaId"]) != "" {
						anyPlayable = true
						break
					}
				}
				if !anyPlayable {
					return errors.New("该分享不包含可在线播放的音视频，无法关闭原文件下载")
				}
			}
			patch["allowDownload"] = nextAllow
		}
		if input["passcode"] != nil {
			pc, pErr := validateSharePasscode(str(input["passcode"]))
			if pErr != nil {
				return pErr
			}
			patch["passcode"] = pc
		}
		if input["expiresInHours"] != nil {
			hrs := num(input["expiresInHours"])
			if hrs < 0 || hrs > 24*365*5 {
				return errors.New("有效期范围无效")
			}
			if hrs == 0 {
				patch["expiresAt"] = ""
			} else {
				patch["expiresAt"] = time.Now().UTC().Add(time.Duration(hrs) * time.Hour).Format(time.RFC3339)
			}
		}
		updated, err := a.Store.Patch("shares", code, patch)
		if err != nil {
			return err
		}
		ok(w, formatAdminShareRecord(updated, time.Now()))
		return nil
	})

	a.handle("DELETE /api/shares/{code}", func(w http.ResponseWriter, r *http.Request) error {
		code := strings.TrimSpace(r.PathValue("code"))
		if !shareCodePattern.MatchString(code) {
			return errors.New("分享码无效")
		}
		if err := a.Store.Delete("shares", code); err != nil {
			return err
		}
		ok(w, Obj{"deleted": true, "code": code})
		return nil
	})

	a.handle("DELETE /api/shares", func(w http.ResponseWriter, r *http.Request) error {
		if r.URL.Query().Get("expired") != "1" {
			return errors.New("请指定删除条件")
		}
		docs, err := a.Store.List("shares")
		if err != nil {
			return err
		}
		ref := time.Now()
		removed := 0
		for _, doc := range docs {
			if isShareExpired(doc, ref) {
				if err := a.Store.Delete("shares", str(doc["code"])); err == nil {
					removed++
				}
			}
		}
		ok(w, Obj{"removedCount": removed})
		return nil
	})

	// ---------- 访客公开接口（仅限 /api/public/shares/...，独立校验提取码与过期时间） ----------

	a.handle("GET /api/public/shares/{code}", func(w http.ResponseWriter, r *http.Request) error {
		share, unlocked, unlockToken, okShare := a.loadAccessibleShare(w, r)
		if !okShare {
			return nil
		}
		if unlocked && r.URL.Query().Get("silent") != "1" {
			nextViews := num(share["viewCount"]) + 1
			share["viewCount"] = nextViews
			_, _ = a.Store.Patch("shares", str(share["code"]), Obj{"viewCount": nextViews})
		}
		ok(w, formatPublicShareView(share, unlocked, unlockToken))
		return nil
	})

	a.handle("POST /api/public/shares/{code}/unlock", func(w http.ResponseWriter, r *http.Request) error {
		code := strings.TrimSpace(r.PathValue("code"))
		if !shareCodePattern.MatchString(code) {
			failWithCode(w, http.StatusNotFound, "SHARE_NOT_FOUND", "分享链接不存在或已失效")
			return nil
		}
		throttleKey := a.clientIP(r) + "|share|" + code
		if wait := a.throttle.check(throttleKey); wait > 0 {
			writeRateLimited(w, wait)
			return nil
		}
		share, err := a.Store.Get("shares", code)
		if err != nil || share == nil {
			failWithCode(w, http.StatusNotFound, "SHARE_NOT_FOUND", "分享链接不存在或已被取消")
			return nil
		}
		if isShareExpired(share, time.Now()) {
			failWithCode(w, http.StatusGone, "SHARE_EXPIRED", "该分享链接已过期")
			return nil
		}
		input, err := body(w, r)
		if err != nil {
			return err
		}
		submitted := strings.TrimSpace(str(input["passcode"]))
		expected := str(share["passcode"])
		if expected != "" && subtle.ConstantTimeCompare([]byte(submitted), []byte(expected)) != 1 {
			a.throttle.recordFailure(throttleKey)
			a.sleepOnAuthFailure()
			failWithCode(w, http.StatusUnauthorized, "SHARE_PASSCODE_INVALID", "提取码错误，请核对后重试")
			return nil
		}
		a.throttle.recordSuccess(throttleKey)
		unlockToken := a.computeShareUnlockToken(share)
		if unlockToken != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     shareCookieName(code),
				Value:    unlockToken,
				Path:     "/api/public/shares/" + code,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
				MaxAge:   int((7 * 24 * time.Hour).Seconds()),
			})
		}
		nextViews := num(share["viewCount"]) + 1
		share["viewCount"] = nextViews
		_, _ = a.Store.Patch("shares", code, Obj{"viewCount": nextViews})
		ok(w, formatPublicShareView(share, true, unlockToken))
		return nil
	})

	a.handle("GET /api/public/shares/{code}/items/{itemId}/playback", func(w http.ResponseWriter, r *http.Request) error {
		_, item, _, okItem := a.requireUnlockedShareItem(w, r)
		if !okItem {
			return nil
		}
		mediaID := str(item["mediaId"])
		if !flag(item["playable"]) || mediaID == "" {
			return errors.New("该文件不支持在线播放")
		}
		return a.media.servePlaybackByGID(w, r, mediaID, true)
	})

	a.handle("GET /api/public/shares/{code}/items/{itemId}/playback/progress", func(w http.ResponseWriter, r *http.Request) error {
		_, item, _, okItem := a.requireUnlockedShareItem(w, r)
		if !okItem {
			return nil
		}
		mediaID := str(item["mediaId"])
		if !flag(item["playable"]) || mediaID == "" {
			return errors.New("该文件不支持在线播放")
		}
		return a.media.serveProgressByGID(w, mediaID)
	})

	a.handle("GET /api/public/shares/{code}/items/{itemId}/stream", func(w http.ResponseWriter, r *http.Request) error {
		_, item, _, okItem := a.requireUnlockedShareItem(w, r)
		if !okItem {
			return nil
		}
		mediaID := str(item["mediaId"])
		if !flag(item["playable"]) || mediaID == "" {
			failWithCode(w, http.StatusForbidden, "SHARE_STREAM_UNSUPPORTED", "该文件不支持在线播放")
			return nil
		}
		w.Header().Set("Content-Disposition", "inline")
		return a.media.serveStreamByGID(w, r, mediaID)
	})

	a.handle("GET /api/public/shares/{code}/items/{itemId}/hls/{name}", func(w http.ResponseWriter, r *http.Request) error {
		_, item, unlockToken, okItem := a.requireUnlockedShareItem(w, r)
		if !okItem {
			return nil
		}
		mediaID := str(item["mediaId"])
		if !flag(item["playable"]) || mediaID == "" {
			return errors.New("该文件不支持在线播放")
		}
		suffix := ""
		if st := strings.TrimSpace(r.URL.Query().Get("st")); st != "" {
			suffix = "?st=" + url.QueryEscape(st)
		} else if unlockToken != "" {
			suffix = "?st=" + url.QueryEscape(unlockToken)
		}
		return a.media.serveHLSByGID(w, r, mediaID, r.PathValue("name"), suffix)
	})

	a.handle("GET /api/public/shares/{code}/items/{itemId}/subtitles/{track}", func(w http.ResponseWriter, r *http.Request) error {
		_, item, _, okItem := a.requireUnlockedShareItem(w, r)
		if !okItem {
			return nil
		}
		mediaID := str(item["mediaId"])
		if !flag(item["playable"]) || mediaID == "" {
			return errors.New("该文件不支持字幕提取")
		}
		return a.media.serveSubtitleByGID(w, r, mediaID, r.PathValue("track"))
	})

	a.handle("GET /api/public/shares/{code}/items/{itemId}/download", func(w http.ResponseWriter, r *http.Request) error {
		share, item, _, okItem := a.requireUnlockedShareItem(w, r)
		if !okItem {
			return nil
		}
		if !flag(share["allowDownload"]) {
			failWithCode(w, http.StatusForbidden, "SHARE_DOWNLOAD_DISABLED", "分享者未开启原文件下载权限")
			return nil
		}
		return a.serveShareItemDownload(w, r, item)
	})
}
