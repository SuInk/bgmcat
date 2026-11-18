package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const mangaDexAPIBase = "https://api.mangadex.org"

var mangaIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

type mangaCacheEntry struct {
	data      Obj
	expiresAt time.Time
}

var (
	mangaCacheMu sync.Mutex
	mangaCache   = map[string]mangaCacheEntry{}
)

func mangaCacheGet(key string) (Obj, bool) {
	mangaCacheMu.Lock()
	defer mangaCacheMu.Unlock()
	ent, ok := mangaCache[key]
	if !ok || time.Now().After(ent.expiresAt) {
		delete(mangaCache, key)
		return nil, false
	}
	return ent.data, true
}

func mangaCacheSet(key string, data Obj, ttl time.Duration) {
	mangaCacheMu.Lock()
	defer mangaCacheMu.Unlock()
	if len(mangaCache) > 128 {
		nowTime := time.Now()
		for k, v := range mangaCache {
			if nowTime.After(v.expiresAt) {
				delete(mangaCache, k)
			}
		}
		if len(mangaCache) > 128 {
			mangaCache = map[string]mangaCacheEntry{}
		}
	}
	mangaCache[key] = mangaCacheEntry{data: data, expiresAt: time.Now().Add(ttl)}
}

func (a *App) mangaRoutes() {
	a.handle("GET /api/manga/discover", a.handleMangaDiscover)
	a.handle("GET /api/manga/progress", a.handleMangaProgressList)
	a.handle("POST /api/manga/progress", a.handleMangaProgressSave)
	a.handle("DELETE /api/manga/progress/{mangaId}", a.handleMangaProgressDelete)
	a.handle("GET /api/manga/image", a.handleMangaImageProxy)
	a.handle("GET /api/manga/chapters/{chapterId}/pages", a.handleMangaChapterPages)
	a.handle("GET /api/manga/{id}", a.handleMangaDetail)
}

func isAllowedMangaImageURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" {
		return nil, false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, false
	}
	if host == "uploads.mangadex.org" {
		return u, true
	}
	if strings.HasSuffix(host, ".mangadex.network") || host == "mangadex.network" {
		return u, true
	}
	return nil, false
}

func mangaImageProxyURL(upstream string) string {
	if upstream == "" {
		return ""
	}
	return "/api/manga/image?url=" + url.QueryEscape(upstream)
}

func pickLocalizedText(m Obj, prefer ...string) string {
	if len(m) == 0 {
		return ""
	}
	for _, k := range prefer {
		if v := strings.TrimSpace(str(m[k])); v != "" {
			return v
		}
	}
	for _, v := range m {
		if s := strings.TrimSpace(str(v)); s != "" {
			return s
		}
	}
	return ""
}

func pickMangaTitles(attrs Obj) (title string, altTitle string) {
	titleMap := object(attrs["title"])
	altList := list(attrs["altTitles"])
	preferred := []string{"zh", "zh-hk", "zh-ro", "en", "ja-ro", "ja"}

	for _, lang := range preferred {
		if v := strings.TrimSpace(str(titleMap[lang])); v != "" {
			title = v
			break
		}
		for _, item := range altList {
			if v := strings.TrimSpace(str(object(item)[lang])); v != "" {
				title = v
				break
			}
		}
		if title != "" {
			break
		}
	}
	if title == "" {
		title = pickLocalizedText(titleMap, preferred...)
	}

	origTitle := pickLocalizedText(titleMap, "ja", "ja-ro", "en")
	if origTitle != "" && origTitle != title {
		altTitle = origTitle
	} else {
		for _, item := range altList {
			cand := pickLocalizedText(object(item), "ja", "en", "zh")
			if cand != "" && cand != title {
				altTitle = cand
				break
			}
		}
	}
	return title, altTitle
}

func normalizeMangaItem(raw Obj) Obj {
	id := str(raw["id"])
	attrs := object(raw["attributes"])
	title, altTitle := pickMangaTitles(attrs)
	desc := pickLocalizedText(object(attrs["description"]), "zh", "zh-hk", "en", "ja")

	var coverFile string
	var authors []string
	for _, rel := range list(raw["relationships"]) {
		rObj := object(rel)
		rType := str(rObj["type"])
		rAttrs := object(rObj["attributes"])
		switch rType {
		case "cover_art":
			if fn := strings.TrimSpace(str(rAttrs["fileName"])); fn != "" {
				coverFile = fn
			}
		case "author", "artist":
			if name := strings.TrimSpace(str(rAttrs["name"])); name != "" {
				authors = append(authors, name)
			}
		}
	}
	coverUpstream := ""
	if id != "" && coverFile != "" {
		coverUpstream = fmt.Sprintf("https://uploads.mangadex.org/covers/%s/%s.512.jpg", url.PathEscape(id), url.PathEscape(coverFile))
	}

	tags := []string{}
	for _, t := range list(attrs["tags"]) {
		tName := pickLocalizedText(object(object(t)["attributes"])["name"].(Obj), "zh", "zh-hk", "en")
		if tName != "" {
			tags = append(tags, tName)
		}
	}
	_ = tags

	return normalizeMangaItemWithTags(id, attrs, title, altTitle, desc, coverUpstream, authors)
}

func normalizeMangaItemWithTags(id string, attrs Obj, title, altTitle, desc, coverUpstream string, authors []string) Obj {
	tags := make([]string, 0, 8)
	for _, t := range list(attrs["tags"]) {
		tObj := object(t)
		tAttrs := object(tObj["attributes"])
		if nameMap := object(tAttrs["name"]); len(nameMap) > 0 {
			if tName := pickLocalizedText(nameMap, "zh", "zh-hk", "en"); tName != "" {
				tags = append(tags, tName)
			}
		}
	}
	if len(tags) > 8 {
		tags = tags[:8]
	}

	// Deduplicate authors
	seenAuthors := map[string]bool{}
	uniqAuthors := make([]string, 0, len(authors))
	for _, au := range authors {
		if !seenAuthors[au] {
			seenAuthors[au] = true
			uniqAuthors = append(uniqAuthors, au)
		}
	}

	return Obj{
		"id":            id,
		"title":         title,
		"altTitle":      altTitle,
		"description":   desc,
		"coverUrl":      mangaImageProxyURL(coverUpstream),
		"status":        str(attrs["status"]),
		"year":          num(attrs["year"]),
		"lastChapter":   str(attrs["lastChapter"]),
		"contentRating": str(attrs["contentRating"]),
		"authors":       uniqAuthors,
		"tags":          tags,
		"updatedAt":     str(attrs["updatedAt"]),
	}
}

func normalizeMangaObject(raw Obj) Obj {
	id := str(raw["id"])
	attrs := object(raw["attributes"])
	title, altTitle := pickMangaTitles(attrs)
	desc := pickLocalizedText(object(attrs["description"]), "zh", "zh-hk", "en", "ja")

	var coverFile string
	var authors []string
	for _, rel := range list(raw["relationships"]) {
		rObj := object(rel)
		rType := str(rObj["type"])
		rAttrs := object(rObj["attributes"])
		switch rType {
		case "cover_art":
			if fn := strings.TrimSpace(str(rAttrs["fileName"])); fn != "" {
				coverFile = fn
			}
		case "author", "artist":
			if name := strings.TrimSpace(str(rAttrs["name"])); name != "" {
				authors = append(authors, name)
			}
		}
	}
	coverUpstream := ""
	if id != "" && coverFile != "" {
		coverUpstream = fmt.Sprintf("https://uploads.mangadex.org/covers/%s/%s.512.jpg", url.PathEscape(id), url.PathEscape(coverFile))
	}
	return normalizeMangaItemWithTags(id, attrs, title, altTitle, desc, coverUpstream, authors)
}

func (a *App) mangaDexGetJSON(ctx context.Context, rawURL string) (Obj, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "bgmcat/"+Version)
	req.Header.Set("Accept", "application/json")

	res, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("MangaDex 请求失败（HTTP %d）", res.StatusCode)
	}
	var out Obj
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *App) handleMangaDiscover(w http.ResponseWriter, r *http.Request) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	sortMode := strings.TrimSpace(r.URL.Query().Get("sort"))
	if sortMode != "latest" && sortMode != "popular" {
		sortMode = "latest"
	}
	lang := strings.TrimSpace(r.URL.Query().Get("lang"))
	if lang != "all" {
		lang = "zh"
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 40 {
		limit = 24
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	cacheKey := fmt.Sprintf("manga:discover:%s:%s:%s:%d:%d", q, sortMode, lang, limit, offset)
	if cached, hit := mangaCacheGet(cacheKey); hit {
		ok(w, cached)
		return nil
	}

	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))
	params.Add("includes[]", "cover_art")
	params.Add("includes[]", "author")
	params.Add("contentRating[]", "safe")
	params.Add("contentRating[]", "suggestive")
	params.Set("hasAvailableChapters", "true")
	if lang == "zh" {
		params.Add("availableTranslatedLanguage[]", "zh")
		params.Add("availableTranslatedLanguage[]", "zh-hk")
	}
	if q != "" {
		params.Set("title", q)
		params.Set("order[relevance]", "desc")
	} else if sortMode == "popular" {
		params.Set("order[followedCount]", "desc")
	} else {
		params.Set("order[latestUploadedChapter]", "desc")
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	payload, err := a.mangaDexGetJSON(ctx, mangaDexAPIBase+"/manga?"+params.Encode())
	if err != nil {
		return err
	}

	rawItems := list(payload["data"])
	items := make([]Obj, 0, len(rawItems))
	for _, entry := range rawItems {
		items = append(items, normalizeMangaObject(object(entry)))
	}
	result := Obj{
		"items":  items,
		"total":  num(payload["total"]),
		"limit":  limit,
		"offset": offset,
	}
	mangaCacheSet(cacheKey, result, 90*time.Second)
	ok(w, result)
	return nil
}

func (a *App) handleMangaDetail(w http.ResponseWriter, r *http.Request) error {
	id := strings.TrimSpace(r.PathValue("id"))
	if !mangaIDPattern.MatchString(id) {
		fail(w, 400, "无效漫画 ID")
		return nil
	}

	lang := strings.TrimSpace(r.URL.Query().Get("lang"))
	if lang != "all" {
		lang = "zh"
	}
	cacheKey := fmt.Sprintf("manga:detail:%s:%s", id, lang)
	if cached, hit := mangaCacheGet(cacheKey); hit {
		ok(w, cached)
		return nil
	}

	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()

	detailParams := url.Values{}
	detailParams.Add("includes[]", "cover_art")
	detailParams.Add("includes[]", "author")
	detailParams.Add("includes[]", "artist")
	detailPayload, err := a.mangaDexGetJSON(ctx, mangaDexAPIBase+"/manga/"+url.PathEscape(id)+"?"+detailParams.Encode())
	if err != nil {
		return err
	}

	feedParams := url.Values{}
	feedParams.Set("limit", "100")
	feedParams.Add("includes[]", "scanlation_group")
	feedParams.Add("contentRating[]", "safe")
	feedParams.Add("contentRating[]", "suggestive")
	feedParams.Add("contentRating[]", "erotica")
	feedParams.Set("order[chapter]", "desc")
	feedParams.Set("order[publishAt]", "desc")
	if lang == "zh" {
		feedParams.Add("translatedLanguage[]", "zh")
		feedParams.Add("translatedLanguage[]", "zh-hk")
	} else {
		feedParams.Add("translatedLanguage[]", "zh")
		feedParams.Add("translatedLanguage[]", "zh-hk")
		feedParams.Add("translatedLanguage[]", "en")
		feedParams.Add("translatedLanguage[]", "ja")
	}
	feedPayload, err := a.mangaDexGetJSON(ctx, mangaDexAPIBase+"/manga/"+url.PathEscape(id)+"/feed?"+feedParams.Encode())
	if err != nil {
		return err
	}

	rawChapters := list(feedPayload["data"])
	chapters := make([]Obj, 0, len(rawChapters))
	for _, rawCh := range rawChapters {
		chObj := object(rawCh)
		chAttrs := object(chObj["attributes"])
		pages := int(num(chAttrs["pages"]))
		extURL := strings.TrimSpace(str(chAttrs["externalUrl"]))
		if pages <= 0 || extURL != "" {
			continue
		}
		var groupName string
		for _, rel := range list(chObj["relationships"]) {
			rObj := object(rel)
			if str(rObj["type"]) == "scanlation_group" {
				if gName := strings.TrimSpace(str(object(rObj["attributes"])["name"])); gName != "" {
					groupName = gName
					break
				}
			}
		}
		chNum := strings.TrimSpace(str(chAttrs["chapter"]))
		chTitle := strings.TrimSpace(str(chAttrs["title"]))
		vol := strings.TrimSpace(str(chAttrs["volume"]))
		label := ""
		if chNum != "" {
			label = "第 " + chNum + " 话"
		} else if vol != "" {
			label = "第 " + vol + " 卷"
		} else {
			label = "单篇"
		}
		if chTitle != "" {
			label += " · " + chTitle
		}

		chapters = append(chapters, Obj{
			"id":              str(chObj["id"]),
			"chapter":         chNum,
			"volume":          vol,
			"title":           chTitle,
			"label":           label,
			"language":        str(chAttrs["translatedLanguage"]),
			"pages":           pages,
			"scanlationGroup": groupName,
			"publishedAt":     str(chAttrs["publishAt"]),
		})
	}

	result := Obj{
		"manga":    normalizeMangaObject(object(detailPayload["data"])),
		"chapters": chapters,
		"total":    num(feedPayload["total"]),
	}
	mangaCacheSet(cacheKey, result, 90*time.Second)
	ok(w, result)
	return nil
}

func (a *App) handleMangaChapterPages(w http.ResponseWriter, r *http.Request) error {
	chapterID := strings.TrimSpace(r.PathValue("chapterId"))
	if !mangaIDPattern.MatchString(chapterID) {
		fail(w, 400, "无效章节 ID")
		return nil
	}

	cacheKey := "manga:pages:" + chapterID
	if cached, hit := mangaCacheGet(cacheKey); hit {
		ok(w, cached)
		return nil
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	payload, err := a.mangaDexGetJSON(ctx, mangaDexAPIBase+"/at-home/server/"+url.PathEscape(chapterID))
	if err != nil {
		return err
	}

	baseURL := strings.TrimRight(str(payload["baseUrl"]), "/")
	ch := object(payload["chapter"])
	hashVal := str(ch["hash"])
	if baseURL == "" || hashVal == "" {
		return errors.New("未获取到章节图片地址")
	}

	buildPages := func(mode string, files []any) []string {
		out := make([]string, 0, len(files))
		for _, f := range files {
			name := strings.TrimSpace(str(f))
			if name == "" {
				continue
			}
			upstream := fmt.Sprintf("%s/%s/%s/%s", baseURL, mode, url.PathEscape(hashVal), url.PathEscape(name))
			out = append(out, mangaImageProxyURL(upstream))
		}
		return out
	}

	result := Obj{
		"chapterId":  chapterID,
		"pages":      buildPages("data", list(ch["data"])),
		"saverPages": buildPages("data-saver", list(ch["dataSaver"])),
	}
	mangaCacheSet(cacheKey, result, 10*time.Minute)
	ok(w, result)
	return nil
}

func (a *App) handleMangaImageProxy(w http.ResponseWriter, r *http.Request) error {
	rawURL := r.URL.Query().Get("url")
	u, allowed := isAllowedMangaImageURL(rawURL)
	if !allowed {
		fail(w, 403, "不允许的图片源地址")
		return nil
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "bgmcat/"+Version)
	req.Header.Set("Accept", "image/*,*/*;q=0.8")

	res, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		return fmt.Errorf("图片拉取失败（HTTP %d）", res.StatusCode)
	}
	ct := res.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(ct), "image/") {
		ct = "image/jpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if cl := res.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, io.LimitReader(res.Body, 25<<20))
	return nil
}

func (a *App) handleMangaProgressList(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.Store.List("manga_progress")
	if err != nil {
		return err
	}
	sort.Slice(rows, func(i, j int) bool {
		return str(rows[i]["updatedAt"]) > str(rows[j]["updatedAt"])
	})
	ok(w, rows)
	return nil
}

func (a *App) handleMangaProgressSave(w http.ResponseWriter, r *http.Request) error {
	o, err := body(w, r)
	if err != nil {
		return err
	}
	mangaID := strings.TrimSpace(str(o["mangaId"]))
	chapterID := strings.TrimSpace(str(o["chapterId"]))
	if !mangaIDPattern.MatchString(mangaID) || !mangaIDPattern.MatchString(chapterID) {
		fail(w, 400, "无效的漫画或章节 ID")
		return nil
	}
	rec := Obj{
		"mangaId":      mangaID,
		"title":        strings.TrimSpace(str(o["title"])),
		"coverUrl":     strings.TrimSpace(str(o["coverUrl"])),
		"chapterId":    chapterID,
		"chapterLabel": strings.TrimSpace(str(o["chapterLabel"])),
		"pageIndex":    int(num(o["pageIndex"])),
		"totalPages":   int(num(o["totalPages"])),
		"updatedAt":    now(),
	}
	if err := a.Store.Put("manga_progress", mangaID, rec); err != nil {
		return err
	}
	ok(w, rec)
	return nil
}

func (a *App) handleMangaProgressDelete(w http.ResponseWriter, r *http.Request) error {
	mangaID := strings.TrimSpace(r.PathValue("mangaId"))
	if !mangaIDPattern.MatchString(mangaID) {
		fail(w, 400, "无效漫画 ID")
		return nil
	}
	if err := a.Store.Delete("manga_progress", mangaID); err != nil {
		return err
	}
	ok(w, Obj{"deleted": true})
	return nil
}
