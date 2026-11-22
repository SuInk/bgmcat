package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

func (a *App) getAny(ctx context.Context, endpoint string) (any, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "bgmcat/"+Version+" (personal RSS reader)")
	res, e := a.http.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("数据源 HTTP %d", res.StatusCode)
	}
	var v any
	e = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&v)
	return v, e
}

// bangumiCover turns a Bangumi cover URL into the 400px-wide HTTPS rendition.
// The calendar API hands out http:// originals (often 200-500 KB, plus a 301 to
// HTTPS); cards and the detail thumbnail are at most ~180 CSS px wide, so the
// lain.bgm.tv /r/400/ resize is about 1/8 the bytes with no visible loss.
func bangumiCover(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "http://lain.bgm.tv/") {
		raw = "https://" + strings.TrimPrefix(raw, "http://")
	}
	if rest, found := strings.CutPrefix(raw, "https://lain.bgm.tv/pic/"); found {
		raw = "https://lain.bgm.tv/r/400/pic/" + rest
	}
	return raw
}

func animeFromBangumi(o Obj, day int64) Obj {
	return animeFromBangumiAt(o, day, time.Now())
}

var redundantMetaTags = map[string]bool{
	"TV": true, "OVA": true, "WEB": true, "剧场版": true, "其他": true,
	"日本": true, "中国": true, "欧美": true, "美国": true, "韩国": true, "中国香港": true, "中国台湾": true,
}

func animeFromBangumiAt(o Obj, day int64, refNow time.Time) Obj {
	id := str(o["id"])
	title := str(o["name_cn"])
	if title == "" {
		title = str(o["name"])
	}
	images := object(o["images"])
	var image any
	for _, size := range []string{"large", "common", "medium"} {
		if raw := str(images[size]); raw != "" {
			image = bangumiCover(raw)
			break
		}
	}
	airDateRaw := strings.TrimSpace(str(o["date"]))
	if airDateRaw == "" {
		airDateRaw = strings.TrimSpace(str(o["air_date"]))
	}
	var airDate any
	if airDateRaw != "" && airDateRaw != "0000-00-00" {
		airDate = airDateRaw
	}
	formatRaw := strings.TrimSpace(str(o["platform"]))
	var format any
	if formatRaw != "" && formatRaw != "其他" {
		format = formatRaw
	}
	eps := num(o["eps"])
	if eps <= 0 {
		eps = num(o["total_episodes"])
	}
	var totalEpisodes any
	if eps > 0 {
		totalEpisodes = eps
	}
	ratingObj := object(o["rating"])
	var rating any
	if rawScore, ok := ratingObj["score"]; ok && rawScore != nil {
		switch sv := rawScore.(type) {
		case float64:
			if sv > 0 {
				rating = sv
			}
		case int:
			if sv > 0 {
				rating = float64(sv)
			}
		default:
			if num(sv) > 0 {
				rating = sv
			}
		}
	}
	var rank any
	if r := num(ratingObj["rank"]); r > 0 {
		rank = r
	}

	tags := make([]string, 0, 4)
	seenTag := map[string]bool{}
	for _, rawTag := range list(o["meta_tags"]) {
		t := strings.TrimSpace(str(rawTag))
		if t != "" && !redundantMetaTags[t] && !seenTag[t] {
			seenTag[t] = true
			tags = append(tags, t)
			if len(tags) >= 4 {
				break
			}
		}
	}
	if len(tags) < 4 {
		for _, rawTag := range list(o["tags"]) {
			t := strings.TrimSpace(str(object(rawTag)["name"]))
			if t != "" && !redundantMetaTags[t] && !seenTag[t] && len([]rune(t)) <= 6 {
				seenTag[t] = true
				tags = append(tags, t)
				if len(tags) >= 4 {
					break
				}
			}
		}
	}

	var airDay any
	isOnAir := false
	airStatus := "finished"
	if day > 0 {
		airDay = day % 7
		isOnAir = true
		airStatus = "airing"
	} else if airDate != nil {
		if parsed, err := time.Parse("2006-01-02", airDateRaw); err == nil {
			airDay = int64(parsed.Weekday())
			today := time.Date(refNow.Year(), refNow.Month(), refNow.Day(), 0, 0, 0, 0, time.UTC)
			if parsed.After(today) {
				isOnAir = false
				airStatus = "upcoming"
			} else if formatRaw == "剧场版" {
				isOnAir = false
				airStatus = "finished"
			} else {
				runDays := 105
				if eps > 0 && eps <= 100 {
					runDays = int(eps)*7 + 14
					if eps >= 10 && runDays < 98 {
						runDays = 98
					}
				} else if eps > 100 {
					runDays = 365 * 2
				}
				if today.Before(parsed.AddDate(0, 0, runDays)) {
					isOnAir = true
					airStatus = "airing"
				} else {
					isOnAir = false
					airStatus = "finished"
				}
			}
		} else {
			airStatus = "upcoming"
		}
	} else {
		airStatus = "upcoming"
	}

	out := Obj{
		"animeId":          id,
		"bangumiId":        id,
		"title":            title,
		"imageUrl":         image,
		"searchKeyword":    o["name"],
		"airDay":           airDay,
		"isOnAir":          isOnAir,
		"airStatus":        airStatus,
		"airDate":          airDate,
		"format":           format,
		"totalEpisodes":    totalEpisodes,
		"rank":             rank,
		"isRestricted":     flag(o["nsfw"]),
		"rating":           rating,
		"source":           "bangumi",
		"mikanId":          nil,
		"officialSite":     nil,
		"externalIds":      Obj{"bangumi": id},
		"matchedPlatforms": []string{"bangumi"},
	}
	if len(tags) > 0 {
		out["tags"] = tags
	}
	return out
}
func (a *App) homepage(ctx context.Context) ([]Obj, error) {
	a.ensureBangumiDataLoaded(ctx)
	a.ensureBilibiliTimelineLoaded(ctx)
	// The key carries a version so a change to the cached item shape (e.g. cover URLs) takes effect immediately.
	result, err := a.discoveryCached(ctx, "home:v4", time.Hour, 7*24*time.Hour, func(ctx context.Context) (Obj, error) {
		data, err := a.getAny(ctx, "https://api.bgm.tv/calendar")
		if err != nil {
			return nil, err
		}
		out := []Obj{}
		for _, v := range list(data) {
			day := object(v)
			for _, item := range list(day["items"]) {
				anime := animeFromBangumi(object(item), num(object(day["weekday"])["id"]))
				a.enrichAnimeBroadcast(anime)
				out = append(out, anime)
			}
		}
		if len(out) == 0 {
			return nil, errors.New("Bangumi 返回空日历")
		}
		return Obj{"items": out}, nil
	})
	out := []Obj{}
	for _, v := range list(result["items"]) {
		item := object(v)
		a.enrichAnimeBroadcast(item)
		out = append(out, item)
	}
	return out, err
}

func (a *App) seedBangumiSubject(raw Obj) {
	id := str(raw["id"])
	if id == "" || raw["summary"] == nil {
		return
	}
	a.seedDiscoveryCacheIfAbsent("subject:"+id, raw, 6*time.Hour)
}

func (a *App) bangumiSearch(ctx context.Context, keyword string) (Obj, error) {
	o, e := a.requestJSON(ctx, "POST", "https://api.bgm.tv/v0/search/subjects?limit=30", Obj{"keyword": keyword, "filter": Obj{"type": []int{2}}}, nil)
	if e != nil {
		return nil, e
	}
	items := []Obj{}
	for _, item := range list(o["data"]) {
		raw := object(item)
		a.seedBangumiSubject(raw)
		anime := animeFromBangumi(raw, 0)
		a.enrichAnimeBroadcast(anime)
		items = append(items, anime)
	}
	return Obj{"items": items, "platformFailures": []any{}}, nil
}

func defaultProviderBaseURL(provider string) string {
	switch provider {
	case "mikan":
		return "https://mikanani.me"
	case "animegarden":
		return "https://api.animes.garden"
	case "dmhy":
		return "https://share.dmhy.org"
	case "bangumimoe":
		return "https://bangumi.moe"
	case "acgnx":
		return "https://share.acgnx.se"
	case "acgrip":
		return "https://acg.rip"
	case "nyaa":
		return "https://nyaa.si"
	case "kisssub":
		return "https://www.kisssub.org"
	case "comicat":
		return "https://www.comicat.org"
	case "tokyotosho":
		return "https://www.tokyotosho.info"
	}
	return ""
}

func (a *App) providerBaseURL(provider string) string {
	s := a.Settings()
	mirrors := object(s["providerMirrors"])
	custom := strings.TrimRight(strings.TrimSpace(str(mirrors[provider])), "/")
	if custom == "" && provider == "mikan" {
		custom = strings.TrimRight(strings.TrimSpace(str(s["mikanBaseUrl"])), "/")
	}
	if custom != "" && httpURL(custom) {
		if provider == "mikan" && (custom == "https://mikanime.tv" || custom == "http://mikanime.tv") {
			return "https://mikanani.me"
		}
		return custom
	}
	return defaultProviderBaseURL(provider)
}

func (a *App) mikanBase() string {
	return a.providerBaseURL("mikan")
}

var allResourceProviders = []string{
	"mikan",
	"animegarden",
	"dmhy",
	"bangumimoe",
	"acgnx",
	"acgrip",
	"nyaa",
	"kisssub",
	"comicat",
	"tokyotosho",
}

func resourceProviderDisplayName(provider string) string {
	switch provider {
	case "mikan":
		return "蜜柑计划 (Mikan)"
	case "animegarden":
		return "AnimeGarden (聚合)"
	case "dmhy":
		return "动漫花园 (DMHY)"
	case "bangumimoe":
		return "萌番组 (Bangumi.moe)"
	case "acgnx":
		return "末日动漫 (AcgnX)"
	case "acgrip":
		return "ACG.RIP"
	case "nyaa":
		return "Nyaa"
	case "kisssub":
		return "爱恋动漫 (KissSub)"
	case "comicat":
		return "漫猫动漫 (Comicat)"
	case "tokyotosho":
		return "Tokyo Toshokan"
	}
	return provider
}

func (a *App) resourceURL(provider, keyword string) string {
	escaped := url.QueryEscape(keyword)
	base := a.providerBaseURL(provider)
	switch provider {
	case "mikan":
		return base + "/RSS/Search?searchstr=" + escaped
	case "animegarden":
		return base + "/feed.xml?search=" + escaped
	case "dmhy":
		return base + "/topics/rss/rss.xml?keyword=" + escaped
	case "bangumimoe":
		if keyword == "" {
			return base + "/rss/latest"
		}
		if base != defaultProviderBaseURL("bangumimoe") {
			return base + "/rss/latest"
		}
		return a.providerBaseURL("animegarden") + "/feed.xml?include=moe&search=" + escaped
	case "acgnx":
		return base + "/rss.xml?keyword=" + escaped
	case "acgrip":
		return base + "/.xml?term=" + escaped
	case "nyaa":
		return base + "/?page=rss&c=1_2&f=0&q=" + escaped
	case "kisssub":
		if keyword == "" {
			return base + "/rss.xml"
		}
		return base + "/rss-" + escaped + ".xml"
	case "comicat":
		if keyword == "" {
			return base + "/rss.xml"
		}
		return base + "/rss-" + escaped + ".xml"
	case "tokyotosho":
		return base + "/rss.php?terms=" + escaped
	}
	return ""
}
func (a *App) searchResources(ctx context.Context, provider, keyword string) ([]Obj, error) {
	result, err := a.discoveryCached(ctx, "resource:v3:"+hash(a.resourceURL(provider, keyword)), resourceCacheTTL, resourceCacheStale, func(ctx context.Context) (Obj, error) {
		items, err := a.fetchResources(ctx, provider, keyword)
		return Obj{"items": items}, err
	})
	items := []Obj{}
	for _, v := range list(result["items"]) {
		items = append(items, object(v))
	}
	if flag(result["cacheStale"]) {
		err = errors.New(str(result["cacheError"]))
	}
	return items, err
}
func (a *App) fetchBangumiMoeResources(ctx context.Context, keyword string) ([]Obj, error) {
	base := a.providerBaseURL("bangumimoe")
	endpoint := base + "/api/v2/torrent/search?query=" + url.QueryEscape(keyword)
	o, err := a.requestJSON(ctx, "GET", endpoint, nil, nil)
	if err != nil {
		return nil, err
	}
	items := []Obj{}
	displayName := resourceProviderDisplayName("bangumimoe")
	for _, raw := range list(o["torrents"]) {
		t := object(raw)
		id := str(t["_id"])
		title := str(t["title"])
		magnet := str(t["magnet"])
		if title == "" || (magnet == "" && id == "") {
			continue
		}
		var torrentURL any
		var pageURL any
		if id != "" {
			torrentURL = base + "/download/torrent/" + id + "/torrent.torrent"
			pageURL = base + "/torrent/" + id
		}
		var magnetURL any
		sourceType := "torrent-url"
		srcKey := str(torrentURL)
		if magnet != "" {
			magnetURL = magnet
			sourceType = "magnet"
			srcKey = magnet
		}
		rawSize := str(t["size"])
		parsedBytes := parseHumanSize(rawSize)
		sText := sizeText(parsedBytes)
		if sText == nil && rawSize != "" {
			sText = rawSize
		}
		items = append(items, Obj{
			"id":          hash(srcKey),
			"title":       title,
			"subgroup":    subgroupOf(title),
			"magnetUrl":   magnetURL,
			"torrentUrl":  torrentURL,
			"pageUrl":     pageURL,
			"sizeText":    sText,
			"publishedAt": str(t["publish_time"]),
			"sourceType":  sourceType,
			"providerId":  "bangumimoe",
			"displayName": displayName,
			"resourceKey": hash(srcKey),
			"infoHash":    t["infoHash"],
			"episode":     episode(title),
			"resolution":  resolutionRE.FindString(title),
			"providerIds": []string{"bangumimoe"},
			"sizeBytes":   sizeBytes(parsedBytes),
			"seeders":     t["seeders"],
			"leechers":    t["leechers"],
			"sources":     []Obj{{"providerId": "bangumimoe", "pageUrl": pageURL}},
		})
	}
	return items, nil
}
func (a *App) fetchResources(ctx context.Context, provider, keyword string) ([]Obj, error) {
	if provider == "bangumimoe" && keyword != "" {
		if items, err := a.fetchBangumiMoeResources(ctx, keyword); err == nil {
			return items, nil
		}
	}
	endpoint := a.resourceURL(provider, keyword)
	if endpoint == "" {
		return nil, errors.New("未知资源来源")
	}
	feed, e := a.fetchFeed(ctx, endpoint)
	if e != nil {
		return nil, e
	}
	items := []Obj{}
	displayName := resourceProviderDisplayName(provider)
	for _, item := range feed.Items {
		src, kind := feedSource(item)
		if src == "" {
			continue
		}
		published, size := feedItemMeta(item)
		ih := extractFeedItemInfoHash(item, src)
		var infoHashVal any
		resourceKey := hash(src)
		if ih != "" {
			infoHashVal = ih
			resourceKey = ih
		}
		var magnet, torrent any
		if kind == "magnet" {
			magnet = src
		} else if kind == "torrent-url" {
			torrent = src
			if ih != "" {
				if m := buildMagnetFromInfoHash(ih, item.Title); m != "" {
					magnet = m
				}
			}
		}
		items = append(items, Obj{"id": hash(src), "title": item.Title, "subgroup": subgroupOf(item.Title), "magnetUrl": magnet, "torrentUrl": torrent, "pageUrl": item.Link, "sizeText": sizeText(size), "publishedAt": published, "sourceType": kind, "providerId": provider, "displayName": displayName, "resourceKey": resourceKey, "infoHash": infoHashVal, "episode": episode(item.Title), "resolution": resolutionRE.FindString(item.Title), "providerIds": []string{provider}, "sizeBytes": sizeBytes(size), "seeders": nil, "leechers": nil, "sources": []Obj{{"providerId": provider, "pageUrl": item.Link}}})
	}
	return items, nil
}
func (a *App) animeDetail(ctx context.Context, id string, summaryOnly ...bool) (Obj, error) {
	basic := len(summaryOnly) > 0 && summaryOnly[0]
	return a.animeDetailQuery(ctx, id, basic, false, "")
}

func parseBangumiEpisodeItems(rawList []any) []Obj {
	eps := make([]Obj, 0, len(rawList))
	for _, v := range rawList {
		ep := object(v)
		title := str(ep["name_cn"])
		if title == "" {
			title = str(ep["name"])
		}
		sortStr := str(ep["sort"])
		epStr := str(ep["ep"])
		var seasonNumber any
		if ep["ep"] != nil && epStr != "" && epStr != "0" && epStr != sortStr {
			seasonNumber = epStr
		}
		eps = append(eps, Obj{
			"episodeId":     str(ep["id"]),
			"title":         title,
			"episodeNumber": sortStr,
			"seasonNumber":  seasonNumber,
			"airDate":       ep["airdate"],
		})
	}
	return eps
}

func hasHanAndNoKana(s string) bool {
	hasHan := false
	for _, r := range s {
		if unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			return false
		}
		if unicode.Is(unicode.Han, r) {
			hasHan = true
		}
	}
	return hasHan
}

// extractBangumiChineseAliases reads Chinese aliases from Bangumi subject infobox
// (e.g. "海贼王" for "航海王") so resource search covers subgroup naming variants.
func extractBangumiChineseAliases(o Obj, primary string) []string {
	primaryTrim := strings.TrimSpace(primary)
	nameTrim := strings.TrimSpace(str(o["name"]))
	cnTrim := strings.TrimSpace(str(o["name_cn"]))
	seen := map[string]bool{}
	for _, base := range []string{primaryTrim, nameTrim, cnTrim} {
		if base != "" {
			seen[strings.ToLower(base)] = true
		}
	}
	aliases := []string{}
	addCandidate := func(raw string) {
		if len(aliases) >= 2 {
			return
		}
		cand := strings.TrimSpace(raw)
		if cand == "" {
			return
		}
		runes := utf8.RuneCountInString(cand)
		if runes < 2 || runes > 20 || !hasHanAndNoKana(cand) {
			return
		}
		key := strings.ToLower(cand)
		if seen[key] {
			return
		}
		seen[key] = true
		aliases = append(aliases, cand)
	}

	for _, rawBox := range list(o["infobox"]) {
		box := object(rawBox)
		k := strings.TrimSpace(str(box["key"]))
		if k != "别名" && k != "中文名" {
			continue
		}
		switch val := box["value"].(type) {
		case string:
			addCandidate(val)
		case []any:
			for _, item := range val {
				if s, ok := item.(string); ok {
					addCandidate(s)
				} else if m := object(item); m != nil {
					addCandidate(str(m["v"]))
				}
			}
		}
	}
	return aliases
}

func isStreamingInfoboxKey(k string) bool {
	switch k {
	case "在线播放平台", "播放网站", "播放平台", "放送网站", "网络播放", "配信サイト":
		return true
	default:
		return false
	}
}

func resolveStreamingPlatform(labelHint, raw string, searchTitle string) (string, string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", ""
	}
	isDirectURL := strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://")
	probe := strings.ToLower(strings.TrimSpace(labelHint + " " + trimmed))
	q := url.QueryEscape(searchTitle)

	switch {
	case strings.Contains(probe, "bilibili") || strings.Contains(probe, "哔哩") || strings.Contains(probe, "b站") || strings.Contains(probe, "b23.tv"):
		u := "https://search.bilibili.com/all?keyword=" + q
		if isDirectURL {
			u = trimmed
		}
		return "bilibili", "哔哩哔哩", u
	case strings.Contains(probe, "iqiyi") || strings.Contains(probe, "爱奇艺"):
		u := "https://so.iqiyi.com/so/q_" + url.PathEscape(searchTitle)
		if isDirectURL {
			u = trimmed
		}
		return "iqiyi", "爱奇艺", u
	case strings.Contains(probe, "腾讯") || strings.Contains(probe, "wetv") || strings.Contains(probe, "v.qq.com") || probe == "qq":
		u := "https://v.qq.com/x/search/?q=" + q
		if isDirectURL {
			u = trimmed
		}
		return "qq", "腾讯视频", u
	case strings.Contains(probe, "youku") || strings.Contains(probe, "优酷"):
		u := "https://so.youku.com/search_video/q_" + url.PathEscape(searchTitle)
		if isDirectURL {
			u = trimmed
		}
		return "youku", "优酷", u
	case strings.Contains(probe, "mgtv") || strings.Contains(probe, "芒果"):
		u := "https://so.mgtv.com/so?k=" + q
		if isDirectURL {
			u = trimmed
		}
		return "mgtv", "芒果TV", u
	case strings.Contains(probe, "acfun") || strings.Contains(probe, "a站"):
		u := "https://www.acfun.cn/search?keyword=" + q
		if isDirectURL {
			u = trimmed
		}
		return "acfun", "AcFun", u
	case strings.Contains(probe, "动画疯") || strings.Contains(probe, "巴哈") || strings.Contains(probe, "gamer.com.tw") || strings.Contains(probe, "bahamut"):
		u := "https://ani.gamer.com.tw/search.php?kw=" + q
		if isDirectURL {
			u = trimmed
		}
		return "bahamut", "动画疯", u
	case strings.Contains(probe, "ani-one") || strings.Contains(probe, "anione"):
		u := "https://www.youtube.com/results?search_query=" + q
		if isDirectURL {
			u = trimmed
		}
		return "ani_one", "Ani-One", u
	case strings.Contains(probe, "木棉花") || strings.Contains(probe, "muse"):
		u := "https://www.youtube.com/results?search_query=" + q
		if isDirectURL {
			u = trimmed
		}
		return "muse", "木棉花", u
	case strings.Contains(probe, "youtube") || strings.Contains(probe, "youtu.be"):
		u := "https://www.youtube.com/results?search_query=" + q
		if isDirectURL {
			u = trimmed
		}
		return "youtube", "YouTube", u
	case strings.Contains(probe, "netflix") || strings.Contains(probe, "网飞") || strings.Contains(probe, "奈飞"):
		u := "https://www.netflix.com/search?q=" + q
		if isDirectURL {
			u = trimmed
		}
		return "netflix", "Netflix", u
	case strings.Contains(probe, "disney"):
		u := "https://www.disneyplus.com/"
		if isDirectURL {
			u = trimmed
		}
		return "disneyplus", "Disney+", u
	default:
		return "", "", ""
	}
}

func extractBangumiStreamingLinks(o Obj, primaryTitle string) []Obj {
	searchTitle := strings.TrimSpace(primaryTitle)
	if searchTitle == "" {
		searchTitle = strings.TrimSpace(str(o["name_cn"]))
	}
	if searchTitle == "" {
		searchTitle = strings.TrimSpace(str(o["name"]))
	}
	if searchTitle == "" {
		return []Obj{}
	}

	links := []Obj{}
	indexByID := map[string]int{}

	addEntry := func(labelHint, raw string) {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return
		}
		isURL := strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://")
		parts := []string{trimmed}
		if !isURL {
			parts = strings.FieldsFunc(trimmed, func(r rune) bool {
				switch r {
				case '、', '，', ',', '/', '|', ';', '；', '\n':
					return true
				default:
					return false
				}
			})
		}
		for _, part := range parts {
			id, label, targetURL := resolveStreamingPlatform(labelHint, part, searchTitle)
			if id == "" || targetURL == "" {
				continue
			}
			entry := Obj{"id": id, "label": label, "url": targetURL, "direct": isURL}
			if idx, exists := indexByID[id]; exists {
				if isURL {
					links[idx] = entry
				}
				continue
			}
			indexByID[id] = len(links)
			links = append(links, entry)
		}
	}

	for _, rawBox := range list(o["infobox"]) {
		box := object(rawBox)
		if !isStreamingInfoboxKey(strings.TrimSpace(str(box["key"]))) {
			continue
		}
		switch val := box["value"].(type) {
		case string:
			addEntry("", val)
		case []any:
			for _, item := range val {
				if s, ok := item.(string); ok {
					addEntry("", s)
				} else if m := object(item); m != nil {
					addEntry(str(m["k"]), str(m["v"]))
				}
			}
		}
	}
	return links
}

func diffKeywords(target, already []string) []string {
	have := make(map[string]bool, len(already))
	for _, kw := range already {
		if trimmed := strings.TrimSpace(kw); trimmed != "" {
			have[strings.ToLower(trimmed)] = true
		}
	}
	missing := []string{}
	for _, kw := range target {
		if trimmed := strings.TrimSpace(kw); trimmed != "" && !have[strings.ToLower(trimmed)] {
			missing = append(missing, trimmed)
		}
	}
	return missing
}

func mergeProviderStatusesByProvider(base, extra []Obj, mergedDownloads []Obj) []Obj {
	if len(extra) == 0 {
		return base
	}
	extraMap := make(map[string]Obj, len(extra))
	for _, st := range extra {
		extraMap[str(st["providerId"])] = st
	}
	countsByProvider := map[string]int{}
	for _, dl := range mergedDownloads {
		pids := list(dl["providerIds"])
		if len(pids) == 0 {
			if pid := str(dl["providerId"]); pid != "" {
				countsByProvider[pid]++
			}
			continue
		}
		seen := map[string]bool{}
		for _, v := range pids {
			pid := str(v)
			if pid != "" && !seen[pid] {
				seen[pid] = true
				countsByProvider[pid]++
			}
		}
	}
	out := make([]Obj, len(base))
	for i, st := range base {
		merged := clone(st)
		pid := str(merged["providerId"])
		ex := extraMap[pid]
		pending := flag(merged["pending"]) || (ex != nil && flag(ex["pending"]))
		merged["pending"] = pending
		if ex != nil && !flag(merged["ok"]) && flag(ex["ok"]) {
			merged["ok"] = true
			merged["error"] = ""
			merged["reason"] = ""
		}
		if cnt, exists := countsByProvider[pid]; exists {
			merged["count"] = cnt
		}
		out[i] = merged
	}
	return out
}

func (a *App) fetchBangumiEpisodes(ctx context.Context, id string) []Obj {
	epData, e := a.discoveryCached(ctx, "episodes:v3:"+id, time.Hour, 7*24*time.Hour, func(ctx context.Context) (Obj, error) {
		firstPage, err := a.requestJSON(ctx, "GET", "https://api.bgm.tv/v0/episodes?subject_id="+url.QueryEscape(id)+"&type=0&limit=100", nil, nil)
		if err != nil {
			return nil, err
		}
		firstData := list(firstPage["data"])
		total := int(num(firstPage["total"]))
		if len(firstData) < 100 || total <= 100 {
			return firstPage, nil
		}
		maxTotal := total
		if maxTotal > 2500 {
			maxTotal = 2500
		}
		offsets := make([]int, 0, (maxTotal-1)/100)
		for off := 100; off < maxTotal; off += 100 {
			offsets = append(offsets, off)
		}
		pages := make([][]any, len(offsets))
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for idx, off := range offsets {
			wg.Add(1)
			go func(idx, off int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				endpoint := fmt.Sprintf("https://api.bgm.tv/v0/episodes?subject_id=%s&type=0&limit=100&offset=%d", url.QueryEscape(id), off)
				if pageObj, pErr := a.requestJSON(ctx, "GET", endpoint, nil, nil); pErr == nil {
					pages[idx] = list(pageObj["data"])
				}
			}(idx, off)
		}
		wg.Wait()
		combined := make([]any, 0, maxTotal)
		combined = append(combined, firstData...)
		for _, page := range pages {
			combined = append(combined, page...)
		}
		return Obj{"data": combined, "total": len(combined)}, nil
	})
	if e != nil {
		return []Obj{}
	}
	return parseBangumiEpisodeItems(list(epData["data"]))
}

func (a *App) animeDetailQuery(ctx context.Context, id string, basic bool, progressive bool, keywordHint string) (Obj, error) {
	if strings.Contains(id, ":") && !strings.HasPrefix(id, "bangumi:") {
		return a.externalDetailQuery(ctx, id, basic, progressive)
	}
	id = strings.TrimPrefix(id, "bangumi:")
	if basic {
		o, e := a.discoveryCached(ctx, "subject:"+id, 6*time.Hour, 7*24*time.Hour, func(ctx context.Context) (Obj, error) {
			return a.requestJSON(ctx, "GET", "https://api.bgm.tv/v0/subjects/"+url.PathEscape(id), nil, nil)
		})
		if e != nil {
			return nil, e
		}
		anime := animeFromBangumi(o, 0)
		a.enrichAnimeBroadcast(anime)
		keyword := str(anime["title"])
		aliases := extractBangumiChineseAliases(o, keyword)
		titles := normalizeSearchKeywords([]string{str(o["name"]), str(o["name_cn"])})
		titles = normalizeSearchKeywords(append(titles, aliases...))
		tags := []any{}
		for _, v := range list(o["tags"]) {
			tags = append(tags, object(v)["name"])
		}
		streamingLinks := a.fastStreamingLinks(id, o, keyword)
		return Obj{"detailPending": true, "anime": anime, "titles": titles, "summary": o["summary"], "intro": o["summary"], "episodes": []Obj{}, "mikanKeyword": keyword, "downloads": []Obj{}, "providerStatuses": a.pendingResourceStatuses(), "tags": tags, "rank": object(o["rating"])["rank"], "totalEpisodes": o["total_episodes"], "platforms": []Obj{{"id": "bangumi", "displayName": "Bangumi", "url": "https://bgm.tv/subject/" + id, "score": object(o["rating"])["score"], "scoreMax": 10}}, "streamingLinks": streamingLinks, "platformFailures": []any{}}, nil
	}

	earlyKeyword := strings.TrimSpace(keywordHint)
	var earlyKeywords []string
	if cachedSubj, ok := a.peekDiscoveryCache("subject:"+id, 6*time.Hour); ok {
		cachedTitle := strings.TrimSpace(str(animeFromBangumi(cachedSubj, 0)["title"]))
		if earlyKeyword == "" {
			earlyKeyword = cachedTitle
		}
		if earlyKeyword != "" && (strings.TrimSpace(keywordHint) == "" || strings.EqualFold(earlyKeyword, cachedTitle)) {
			earlyKeywords = normalizeSearchKeywords(append([]string{earlyKeyword}, extractBangumiChineseAliases(cachedSubj, earlyKeyword)...))
		}
	}
	if earlyKeyword != "" && len(earlyKeywords) == 0 {
		earlyKeywords = []string{earlyKeyword}
	}

	var episodesCh chan []Obj
	if earlyKeyword != "" || progressive {
		episodesCh = make(chan []Obj, 1)
		go func() {
			episodesCh <- a.fetchBangumiEpisodes(ctx, id)
		}()
	}

	type resourceResult struct{ downloads, statuses []Obj }
	var resourcesCh chan resourceResult
	if len(earlyKeywords) > 0 {
		resourcesCh = make(chan resourceResult, 1)
		go func(kws []string) {
			dl, st := a.fetchResourcesMulti(ctx, kws, progressive)
			resourcesCh <- resourceResult{dl, st}
		}(earlyKeywords)
	}

	o, e := a.discoveryCached(ctx, "subject:"+id, 6*time.Hour, 7*24*time.Hour, func(ctx context.Context) (Obj, error) {
		return a.requestJSON(ctx, "GET", "https://api.bgm.tv/v0/subjects/"+url.PathEscape(id), nil, nil)
	})
	if e != nil {
		return nil, e
	}
	anime := animeFromBangumi(o, 0)
	a.enrichAnimeBroadcast(anime)
	keyword := str(anime["title"])
	useAliases := strings.TrimSpace(keywordHint) == "" || strings.EqualFold(strings.TrimSpace(keywordHint), keyword)
	var aliases []string
	if useAliases {
		aliases = extractBangumiChineseAliases(o, keyword)
	}
	fullKeywords := normalizeSearchKeywords(append([]string{keyword}, aliases...))

	if episodesCh == nil {
		episodesCh = make(chan []Obj, 1)
		go func() {
			episodesCh <- a.fetchBangumiEpisodes(ctx, id)
		}()
	}

	var downloads, statuses []Obj
	if resourcesCh != nil {
		r := <-resourcesCh
		downloads, statuses = r.downloads, r.statuses
		if keyword != "" && keyword != earlyKeyword && len(downloads) == 0 {
			downloads, statuses = a.fetchResourcesMulti(ctx, fullKeywords, progressive)
		} else if useAliases && len(aliases) > 0 {
			if extra := diffKeywords(fullKeywords, earlyKeywords); len(extra) > 0 {
				exDl, exSt := a.fetchResourcesMulti(ctx, extra, progressive)
				downloads = mergeResourceDownloads(append(downloads, exDl...))
				statuses = mergeProviderStatusesByProvider(statuses, exSt, downloads)
			}
		}
	} else {
		downloads, statuses = a.fetchResourcesMulti(ctx, fullKeywords, progressive)
	}
	eps := <-episodesCh

	titles := normalizeSearchKeywords([]string{str(o["name"]), str(o["name_cn"])})
	titles = normalizeSearchKeywords(append(titles, aliases...))
	tags := []any{}
	for _, v := range list(o["tags"]) {
		tags = append(tags, object(v)["name"])
	}
	streamingLinks := a.fastStreamingLinks(id, o, keyword)
	if str(anime["broadcastAt"]) == "" && len(downloads) > 0 {
		if bcast := deriveBroadcastFromDownloads(downloads); bcast != "" {
			anime["broadcastAt"] = bcast
		}
	}
	return Obj{"detailPending": false, "anime": anime, "titles": titles, "summary": o["summary"], "intro": o["summary"], "episodes": eps, "mikanKeyword": keyword, "downloads": downloads, "providerStatuses": statuses, "tags": tags, "rank": object(o["rating"])["rank"], "totalEpisodes": o["total_episodes"], "platforms": []Obj{{"id": "bangumi", "displayName": "Bangumi", "url": "https://bgm.tv/subject/" + id, "score": object(o["rating"])["score"], "scoreMax": 10}}, "streamingLinks": streamingLinks, "platformFailures": []any{}}, nil
}
func (a *App) favorites() ([]Obj, error) {
	favorites, e := a.Store.List("favorites")
	if e != nil {
		return nil, e
	}
	subs, _ := a.Store.List("subscriptions")
	subsByAnime := make(map[string][]Obj, len(subs))
	for _, s := range subs {
		if aid := str(s["animeId"]); aid != "" {
			subsByAnime[aid] = append(subsByAnime[aid], s)
		}
	}
	for _, f := range favorites {
		// Favorites saved before covers were resized still hold the original URL.
		if image := str(f["imageUrl"]); image != "" {
			f["imageUrl"] = bangumiCover(image)
		}
		a.enrichAnimeBroadcast(f)
		matchedSubs := subsByAnime[str(f["animeId"])]
		related := make([]Obj, 0, len(matchedSubs))
		f["subscriptionId"] = nil
		f["rssUrl"] = nil
		f["subscriptionAutoDownload"] = nil
		f["subscriptionRule"] = nil
		for _, s := range matchedSubs {
			pid := str(s["providerId"])
			pname := pid
			if pid != "" {
				pname = resourceProviderDisplayName(pid)
			}
			related = append(related, Obj{"id": s["id"], "providerId": s["providerId"], "providerName": pname, "name": s["name"], "url": s["url"], "enabled": s["enabled"], "autoDownload": object(s["rule"])["autoDownload"]})
			if f["subscriptionId"] == nil {
				f["subscriptionId"] = s["id"]
				f["rssUrl"] = s["url"]
				f["subscriptionName"] = s["name"]
				f["subscriptionAutoDownload"] = object(s["rule"])["autoDownload"]
				f["subscriptionRule"] = object(s["rule"])
			}
		}
		f["subscriptions"] = related
	}
	return favorites, nil
}
func (a *App) hiddenAnime() ([]Obj, error) {
	items, e := a.Store.List("hidden_anime")
	if e != nil {
		return nil, e
	}
	for _, item := range items {
		if image := str(item["imageUrl"]); image != "" {
			item["imageUrl"] = bangumiCover(image)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return str(items[i]["hiddenAt"]) > str(items[j]["hiddenAt"])
	})
	return items, nil
}
func (a *App) discoveryRoutes() {
	a.handle("GET /api/discovery/home", func(w http.ResponseWriter, r *http.Request) error {
		o, e := a.homepage(r.Context())
		if e == nil {
			ok(w, o)
		}
		return e
	})
	a.handle("GET /api/discovery/catalog", func(w http.ResponseWriter, r *http.Request) error {
		o, e := a.catalogAnime(r.Context(), r.URL.Query())
		if e == nil {
			ok(w, o)
		}
		return e
	})
	a.handle("GET /api/discovery/search", func(w http.ResponseWriter, r *http.Request) error {
		o, e := a.searchAnime(r.Context(), r.URL.Query().Get("keyword"))
		if e == nil {
			ok(w, o)
		}
		return e
	})
	a.handle("GET /api/discovery/anime/{id}", func(w http.ResponseWriter, r *http.Request) error {
		q := r.URL.Query()
		o, e := a.animeDetailQuery(
			r.Context(),
			r.PathValue("id"),
			q.Get("view") == "summary",
			q.Get("progressive") == "1",
			q.Get("keyword"),
		)
		if e == nil {
			ok(w, o)
		}
		return e
	})
	a.handle("GET /api/discovery/streaming/{id}", func(w http.ResponseWriter, r *http.Request) error {
		links, e := a.resolveAnimeStreamingLinks(r.Context(), r.PathValue("id"))
		if e != nil {
			return e
		}
		ok(w, Obj{"streamingLinks": links})
		return nil
	})
	a.handle("GET /api/discovery/mikan/search", func(w http.ResponseWriter, r *http.Request) error {
		o, e := a.searchResources(r.Context(), "mikan", r.URL.Query().Get("keyword"))
		if e == nil {
			ok(w, o)
		}
		return e
	})
	a.handle("GET /api/discovery/providers", func(w http.ResponseWriter, r *http.Request) error {
		out := []Obj{}
		for _, id := range allResourceProviders {
			enabled := false
			for _, v := range list(a.Settings()["enabledResourceProviders"]) {
				if str(v) == id {
					enabled = true
				}
			}
			out = append(out, Obj{"id": id, "displayName": resourceProviderDisplayName(id), "enabled": enabled, "baseUrl": a.resourceURL(id, ""), "defaultBaseUrl": defaultProviderBaseURL(id), "supportsSearch": true, "supportsSubscription": true})
		}
		ok(w, out)
		return nil
	})
	a.handle("POST /api/discovery/providers/{id}/test", func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		start := time.Now()
		pid := r.PathValue("id")
		items, e := a.searchResources(r.Context(), pid, str(o["keyword"]))
		reason := ""
		if e != nil {
			reason = e.Error()
		}
		ok(w, Obj{"providerId": pid, "displayName": resourceProviderDisplayName(pid), "ok": e == nil, "count": len(items), "error": reason, "reason": reason, "elapsedMs": time.Since(start).Milliseconds()})
		return nil
	})
	a.handle("GET /api/discovery/metadata-platforms", func(w http.ResponseWriter, r *http.Request) error {
		out := []Obj{}
		for _, id := range []string{"bangumi", "anilist", "mal", "tmdb"} {
			enabled := false
			for _, v := range list(a.Settings()["enabledMetadataPlatforms"]) {
				if str(v) == id {
					enabled = true
				}
			}
			out = append(out, Obj{"id": id, "displayName": platformNames[id], "enabled": enabled, "configured": id != "tmdb" || str(a.Settings()["tmdbApiKey"]) != "", "requiresApiKey": id == "tmdb"})
		}
		ok(w, out)
		return nil
	})
	a.handle("POST /api/discovery/metadata-platforms/{id}/test", func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		start := time.Now()
		items, e := a.platformSearch(r.Context(), r.PathValue("id"), str(o["keyword"]))
		reason := ""
		if e != nil {
			reason = e.Error()
		}
		ok(w, Obj{"id": r.PathValue("id"), "displayName": platformNames[r.PathValue("id")], "ok": e == nil, "count": len(items), "error": reason, "reason": reason, "elapsedMs": time.Since(start).Milliseconds()})
		return nil
	})
	a.handle("GET /api/favorites", func(w http.ResponseWriter, r *http.Request) error {
		o, e := a.favorites()
		if e == nil {
			ok(w, o)
		}
		return e
	})
	a.handle("POST /api/favorites", func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		f := object(o["anime"])
		if str(f["animeId"]) == "" {
			return errors.New("番剧 ID 不能为空")
		}
		f["mikanKeyword"] = str(o["mikanKeyword"])
		if f["mikanKeyword"] == "" {
			f["mikanKeyword"] = f["title"]
		}
		f["createdAt"] = now()
		f["updatedAt"] = now()
		f["subscriptions"] = []any{}
		e = a.Store.Put("favorites", str(f["animeId"]), f)
		if e == nil {
			ok(w, f)
		}
		return e
	})
	a.handle("DELETE /api/favorites/{id}", func(w http.ResponseWriter, r *http.Request) error {
		e := a.Store.Delete("favorites", r.PathValue("id"))
		if e == nil {
			ok(w, Obj{"deleted": true})
		}
		return e
	})
	a.handle("GET /api/discovery/hidden", func(w http.ResponseWriter, r *http.Request) error {
		items, e := a.hiddenAnime()
		if e == nil {
			ok(w, items)
		}
		return e
	})
	a.handle("POST /api/discovery/hidden", func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		item := clone(object(o["anime"]))
		if item == nil {
			item = Obj{}
		}
		id := strings.TrimSpace(str(item["animeId"]))
		if id == "" {
			return errors.New("番剧 ID 不能为空")
		}
		item["animeId"] = id
		item["hiddenAt"] = now()
		if image := str(item["imageUrl"]); image != "" {
			item["imageUrl"] = bangumiCover(image)
		}
		if e = a.Store.Put("hidden_anime", id, item); e == nil {
			ok(w, item)
		}
		return e
	})
	a.handle("DELETE /api/discovery/hidden/{id}", func(w http.ResponseWriter, r *http.Request) error {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			return errors.New("番剧 ID 不能为空")
		}
		e := a.Store.Delete("hidden_anime", id)
		if e == nil {
			ok(w, Obj{"deleted": true})
		}
		return e
	})
	a.handle("DELETE /api/discovery/hidden", func(w http.ResponseWriter, r *http.Request) error {
		items, e := a.Store.List("hidden_anime")
		if e != nil {
			return e
		}
		deleted := 0
		for _, item := range items {
			if id := str(item["animeId"]); id != "" {
				if a.Store.Delete("hidden_anime", id) == nil {
					deleted++
				}
			}
		}
		ok(w, Obj{"deletedCount": deleted})
		return nil
	})
	a.handle("POST /api/favorites/subscribe", func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		anime := object(o["anime"])
		id := str(anime["animeId"])
		if id == "" {
			return errors.New("缺少番剧 ID")
		}
		keyword := str(o["mikanKeyword"])
		if keyword == "" {
			keyword = str(anime["title"])
		}
		providers := list(o["providerIds"])
		if len(providers) == 0 {
			providers = list(a.Settings()["enabledResourceProviders"])
		}
		opts := object(o["options"])
		rule := buildAnimeSubscriptionRule(opts, now())
		selectedProviders := map[string]bool{}
		for _, v := range providers {
			if p := strings.TrimSpace(str(v)); p != "" {
				selectedProviders[p] = true
			}
		}
		out := []Obj{}
		existing, _ := a.Store.List("subscriptions")
		for _, s := range existing {
			if str(s["animeId"]) == id && !selectedProviders[str(s["providerId"])] {
				_ = a.Store.Delete("subscriptions", str(s["id"]))
			}
		}
		for _, v := range providers {
			provider := strings.TrimSpace(str(v))
			if provider == "" {
				continue
			}
			var found Obj
			for _, s := range existing {
				if str(s["animeId"]) == id && str(s["providerId"]) == provider {
					found = s
					break
				}
			}
			subName := str(anime["title"]) + " · " + resourceProviderDisplayName(provider)
			subURL := a.resourceURL(provider, keyword)
			if found != nil {
				mergedRule := mergeExistingAnimeRule(object(found["rule"]), rule, opts)
				patched, pErr := a.Store.Patch("subscriptions", str(found["id"]), Obj{
					"name":      subName,
					"url":       subURL,
					"enabled":   true,
					"rule":      mergedRule,
					"updatedAt": now(),
				})
				if pErr != nil {
					return pErr
				}
				a.reapplySubscriptionRule(str(found["id"]), mergedRule)
				out = append(out, patched)
				continue
			}
			s, e := a.newSubscription(Obj{
				"name":       subName,
				"url":        subURL,
				"animeId":    id,
				"providerId": provider,
				"rule":       clone(rule),
			})
			if e != nil {
				return e
			}
			out = append(out, s)
		}
		prevFav, _ := a.Store.Get("favorites", id)
		createdAt := now()
		if prevFav != nil && str(prevFav["createdAt"]) != "" {
			createdAt = str(prevFav["createdAt"])
		}
		anime["mikanKeyword"] = keyword
		anime["createdAt"] = createdAt
		anime["updatedAt"] = now()
		if e = a.Store.Put("favorites", id, anime); e != nil {
			return e
		}
		go a.syncAnimeSubscriptions(a.ctx, id)
		ok(w, out)
		return nil
	})
	a.handle("DELETE /api/favorites/{id}/subscriptions", func(w http.ResponseWriter, r *http.Request) error {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			return errors.New("缺少番剧 ID")
		}
		existing, e := a.Store.List("subscriptions")
		if e != nil {
			return e
		}
		deleted := 0
		for _, s := range existing {
			if str(s["animeId"]) == id {
				if err := a.Store.Delete("subscriptions", str(s["id"])); err == nil {
					deleted++
				}
			}
		}
		ok(w, Obj{"deletedCount": deleted})
		return nil
	})
}

func buildAnimeSubscriptionRule(opts Obj, subscribedAt string) Obj {
	r := defaultRule()
	subgroup := strings.TrimSpace(str(opts["subgroup"]))
	if subgroup != "" && subgroup != "all" {
		r["lockedSubgroup"] = subgroup
		r["preferredSubgroups"] = []any{subgroup}
	} else {
		r["lockedSubgroup"] = nil
		r["preferredSubgroups"] = []any{}
	}

	switch strings.ToLower(strings.TrimSpace(str(opts["resolution"]))) {
	case "2160p", "4k":
		r["preferredResolutions"] = []any{"2160p", "4k", "1080p"}
	case "any":
		r["preferredResolutions"] = []any{}
	default:
		r["preferredResolutions"] = []any{"1080p"}
	}

	lang := strings.ToLower(strings.TrimSpace(str(opts["language"])))
	switch lang {
	case "cht", "any":
		r["preferredLanguage"] = lang
	default:
		r["preferredLanguage"] = "chs"
	}

	onlyNew := true
	if opts != nil {
		if rawOnlyNew, hasOnlyNew := opts["onlyNew"]; hasOnlyNew {
			onlyNew = flag(rawOnlyNew)
		}
	}
	r["onlyNew"] = onlyNew
	if onlyNew {
		r["subscribedAt"] = subscribedAt
		if baseEp := int(num(opts["baselineEpisode"])); baseEp > 0 {
			r["baselineEpisode"] = baseEp
		} else {
			r["baselineEpisode"] = 0
		}
	} else {
		r["subscribedAt"] = nil
		r["baselineEpisode"] = 0
	}
	return r
}

func mergeExistingAnimeRule(prev, next, opts Obj) Obj {
	merged := clone(next)
	if len(list(prev["includeKeywords"])) > 0 {
		merged["includeKeywords"] = prev["includeKeywords"]
	}
	if len(list(prev["excludeKeywords"])) > 0 {
		merged["excludeKeywords"] = prev["excludeKeywords"]
	}
	if flag(merged["onlyNew"]) && flag(prev["onlyNew"]) {
		if str(prev["subscribedAt"]) != "" {
			merged["subscribedAt"] = prev["subscribedAt"]
		}
		if prevBase := int(num(prev["baselineEpisode"])); prevBase > int(num(merged["baselineEpisode"])) {
			merged["baselineEpisode"] = prevBase
		}
	}
	_ = opts
	return merged
}

func (a *App) reapplySubscriptionRule(subID string, rule Obj) {
	items, err := a.Store.List("feed")
	if err != nil {
		return
	}
	for _, f := range items {
		if str(f["subscriptionId"]) != subID {
			continue
		}
		st := str(f["downloadStatus"])
		if st != "pending" && st != "skipped" {
			continue
		}
		matched, reason := matchRuleItem(str(f["title"]), f["subgroup"], f["episodeNumber"], str(f["publishedAt"]), false, rule)
		nextStatus := "skipped"
		if matched {
			nextStatus = "pending"
		}
		if flag(f["matched"]) != matched || str(f["matchReason"]) != reason || st != nextStatus {
			_, _ = a.Store.Patch("feed", str(f["id"]), Obj{
				"matched":        matched,
				"matchReason":    reason,
				"downloadStatus": nextStatus,
			})
		}
	}
}

func (a *App) pendingResourceStatuses() []Obj {
	statuses := []Obj{}
	for _, provider := range list(a.Settings()["enabledResourceProviders"]) {
		pid := str(provider)
		statuses = append(statuses, Obj{"providerId": pid, "displayName": resourceProviderDisplayName(pid), "ok": true, "pending": true, "count": 0})
	}
	return statuses
}
