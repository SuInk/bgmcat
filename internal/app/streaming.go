package app

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type bangumiDataStore struct {
	mu                 sync.RWMutex
	loaded             bool
	loadedAt           time.Time
	byBangumi          map[string][]Obj
	byAniList          map[string][]Obj
	byMAL              map[string][]Obj
	broadcastByBangumi map[string]string
	broadcastByAniList map[string]string
	broadcastByMAL     map[string]string
	mikanByBangumi     map[string]string
	biliTimelineAt     time.Time
	biliTimelineByKey  map[string]string
}

var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

var streamingPlatformPriority = map[string]int{
	"bilibili":          10,
	"bilibili_hk_mo_tw": 12,
	"bahamut":           20,
	"iqiyi":             30,
	"qq":                35,
	"youku":             40,
	"mgtv":              45,
	"acfun":             50,
	"muse":              55,
	"ani_one":           58,
	"tropics":           60,
	"mighty":            62,
	"youtube":           65,
	"netflix":           70,
	"disneyplus":        75,
}

func bangumiDataSiteToLink(siteKey, siteID, explicitURL string) (string, string, string) {
	siteID = strings.TrimSpace(siteID)
	explicitURL = strings.TrimSpace(explicitURL)
	if siteID == "" && explicitURL == "" {
		return "", "", ""
	}
	pick := func(tmpl string) string {
		if explicitURL != "" {
			return explicitURL
		}
		if siteID == "" {
			return ""
		}
		return strings.ReplaceAll(tmpl, "{{id}}", siteID)
	}
	switch siteKey {
	case "bilibili":
		return "bilibili", "哔哩哔哩", pick("https://www.bilibili.com/bangumi/media/md{{id}}/")
	case "bilibili_hk_mo_tw", "bilibili_hk_mo", "bilibili_tw":
		return "bilibili_hk_mo_tw", "B站港澳台", pick("https://www.bilibili.com/bangumi/media/md{{id}}/")
	case "gamer", "gamer_hk":
		return "bahamut", "动画疯", pick("https://ani.gamer.com.tw/animeRef.php?sn={{id}}")
	case "iqiyi":
		return "iqiyi", "爱奇艺", pick("https://www.iqiyi.com/{{id}}.html")
	case "qq":
		return "qq", "腾讯视频", pick("https://v.qq.com/detail/{{id}}.html")
	case "youku":
		return "youku", "优酷", pick("https://list.youku.com/show/id_z{{id}}.html")
	case "mgtv":
		return "mgtv", "芒果TV", pick("https://www.mgtv.com/h/{{id}}.html")
	case "acfun":
		return "acfun", "AcFun", pick("https://www.acfun.cn/bangumi/aa{{id}}")
	case "muse_hk", "muse_tw":
		return "muse", "木棉花", pick("https://www.youtube.com/playlist?list={{id}}")
	case "ani_one", "ani_one_asia":
		return "ani_one", "Ani-One", pick("https://www.youtube.com/playlist?list={{id}}")
	case "tropics":
		return "tropics", "回归线", pick("https://www.youtube.com/playlist?list={{id}}")
	case "mighty":
		return "mighty", "曼迪", pick("https://www.youtube.com/playlist?list={{id}}")
	case "youtube":
		return "youtube", "YouTube", pick("https://www.youtube.com/playlist?list={{id}}")
	case "netflix":
		return "netflix", "Netflix", pick("https://www.netflix.com/title/{{id}}")
	case "disneyplus":
		return "disneyplus", "Disney+", pick("https://www.disneyplus.com/series/view/{{id}}")
	default:
		return "", "", ""
	}
}

func extractISOInstantFromBroadcast(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	if strings.HasPrefix(s, "R/") {
		parts := strings.Split(s, "/")
		if len(parts) >= 2 {
			s = strings.TrimSpace(parts[1])
		}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return "", false
	}
	utc := t.UTC()
	isPlaceholder := utc.Hour() == 16 && utc.Minute() == 0 && utc.Second() == 0
	return utc.Format(time.RFC3339), !isPlaceholder
}

func resolveBangumiDataItemBroadcast(item Obj) string {
	if iso, exact := extractISOInstantFromBroadcast(str(item["broadcast"])); iso != "" && exact {
		return iso
	}
	var cnSiteISO, anySiteISO string
	for _, rawSite := range list(item["sites"]) {
		s := object(rawSite)
		iso, exact := extractISOInstantFromBroadcast(str(s["broadcast"]))
		if iso == "" || !exact {
			continue
		}
		if anySiteISO == "" {
			anySiteISO = iso
		}
		switch strings.TrimSpace(str(s["site"])) {
		case "bilibili", "bilibili_hk_mo_tw", "bilibili_hk_mo", "bilibili_tw", "gamer", "gamer_hk", "ani_one", "ani_one_asia", "muse_hk", "muse_tw", "iqiyi", "qq", "youku", "mgtv", "acfun":
			if cnSiteISO == "" {
				cnSiteISO = iso
			}
		}
	}
	if cnSiteISO != "" {
		return cnSiteISO
	}
	if anySiteISO != "" {
		return anySiteISO
	}
	if iso, exact := extractISOInstantFromBroadcast(str(item["begin"])); iso != "" && exact {
		return iso
	}
	if iso, _ := extractISOInstantFromBroadcast(str(item["broadcast"])); iso != "" {
		return iso
	}
	return ""
}

func parseBangumiDataPayload(raw Obj) (
	map[string][]Obj,
	map[string][]Obj,
	map[string][]Obj,
	map[string]string,
	map[string]string,
	map[string]string,
	map[string]string,
) {
	byBangumi := map[string][]Obj{}
	byAniList := map[string][]Obj{}
	byMAL := map[string][]Obj{}
	broadcastByBangumi := map[string]string{}
	broadcastByAniList := map[string]string{}
	broadcastByMAL := map[string]string{}
	mikanByBangumi := map[string]string{}

	for _, rawItem := range list(raw["items"]) {
		item := object(rawItem)
		var bgmID, aniID, malID, mikanID string
		links := []Obj{}
		for _, rawSite := range list(item["sites"]) {
			s := object(rawSite)
			siteName := strings.TrimSpace(str(s["site"]))
			siteID := strings.TrimSpace(str(s["id"]))
			switch siteName {
			case "bangumi":
				bgmID = siteID
			case "aniList":
				aniID = siteID
			case "mal":
				malID = siteID
			case "mikan":
				mikanID = siteID
			default:
				pid, label, linkURL := bangumiDataSiteToLink(siteName, siteID, str(s["url"]))
				if pid != "" && linkURL != "" {
					links = append(links, Obj{"id": pid, "label": label, "url": linkURL, "direct": true})
				}
			}
		}
		bcast := resolveBangumiDataItemBroadcast(item)
		if bgmID != "" {
			if bcast != "" {
				broadcastByBangumi[bgmID] = bcast
			}
			if mikanID != "" {
				mikanByBangumi[bgmID] = mikanID
			}
		}
		if aniID != "" && bcast != "" {
			broadcastByAniList[aniID] = bcast
		}
		if malID != "" && bcast != "" {
			broadcastByMAL[malID] = bcast
		}
		if len(links) == 0 {
			continue
		}
		merged := mergeStreamingLinks(links)
		if bgmID != "" {
			byBangumi[bgmID] = mergeStreamingLinks(append(byBangumi[bgmID], merged...))
		}
		if aniID != "" {
			byAniList[aniID] = mergeStreamingLinks(append(byAniList[aniID], merged...))
		}
		if malID != "" {
			byMAL[malID] = mergeStreamingLinks(append(byMAL[malID], merged...))
		}
	}
	return byBangumi, byAniList, byMAL, broadcastByBangumi, broadcastByAniList, broadcastByMAL, mikanByBangumi
}

func toIndexedLinksMap(raw Obj) map[string][]Obj {
	out := make(map[string][]Obj, len(raw))
	for k, v := range raw {
		arr := list(v)
		if len(arr) == 0 {
			continue
		}
		items := make([]Obj, 0, len(arr))
		for _, entry := range arr {
			if m := object(entry); m != nil {
				items = append(items, m)
			}
		}
		if len(items) > 0 {
			out[k] = items
		}
	}
	return out
}

func toStringMap(raw Obj) map[string]string {
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s := strings.TrimSpace(str(v)); s != "" {
			out[k] = s
		}
	}
	return out
}

func fromStringMap(in map[string]string) Obj {
	out := make(Obj, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (a *App) loadBangumiDataFromDisk() bool {
	cached, ok := a.peekDiscoveryCache("bangumi-data:index:v3", 24*time.Hour)
	if !ok {
		return false
	}
	byBgm := toIndexedLinksMap(object(cached["bangumi"]))
	if len(byBgm) == 0 {
		return false
	}
	byAni := toIndexedLinksMap(object(cached["anilist"]))
	byMal := toIndexedLinksMap(object(cached["mal"]))
	bcastBgm := toStringMap(object(cached["broadcastBangumi"]))
	bcastAni := toStringMap(object(cached["broadcastAniList"]))
	bcastMal := toStringMap(object(cached["broadcastMAL"]))
	mikanBgm := toStringMap(object(cached["mikanBangumi"]))

	a.bgmData.mu.Lock()
	a.bgmData.byBangumi = byBgm
	a.bgmData.byAniList = byAni
	a.bgmData.byMAL = byMal
	a.bgmData.broadcastByBangumi = bcastBgm
	a.bgmData.broadcastByAniList = bcastAni
	a.bgmData.broadcastByMAL = bcastMal
	a.bgmData.mikanByBangumi = mikanBgm
	a.bgmData.loaded = true
	a.bgmData.loadedAt = time.Now()
	a.bgmData.mu.Unlock()
	return true
}

func (a *App) ensureBangumiDataLoaded(ctx context.Context) {
	a.bgmData.mu.RLock()
	fresh := a.bgmData.loaded && time.Since(a.bgmData.loadedAt) < 24*time.Hour
	a.bgmData.mu.RUnlock()
	if fresh {
		return
	}
	if a.loadBangumiDataFromDisk() {
		return
	}

	_, err := a.discoveryCached(ctx, "bangumi-data:index:v3", 24*time.Hour, 30*24*time.Hour, func(fetchCtx context.Context) (Obj, error) {
		endpoints := []string{
			"https://unpkg.com/bangumi-data@latest/dist/data.json",
			"https://fastly.jsdelivr.net/npm/bangumi-data@latest/dist/data.json",
		}
		var lastErr error
		for _, ep := range endpoints {
			reqCtx, cancel := context.WithTimeout(fetchCtx, 12*time.Second)
			raw, e := a.requestJSON(reqCtx, "GET", ep, nil, nil)
			cancel()
			if e != nil {
				lastErr = e
				continue
			}
			byBgm, byAni, byMal, bcastBgm, bcastAni, bcastMal, mikanBgm := parseBangumiDataPayload(raw)
			if len(byBgm) == 0 {
				lastErr = fmt.Errorf("bangumi-data empty from %s", ep)
				continue
			}
			a.bgmData.mu.Lock()
			a.bgmData.byBangumi = byBgm
			a.bgmData.byAniList = byAni
			a.bgmData.byMAL = byMal
			a.bgmData.broadcastByBangumi = bcastBgm
			a.bgmData.broadcastByAniList = bcastAni
			a.bgmData.broadcastByMAL = bcastMal
			a.bgmData.mikanByBangumi = mikanBgm
			a.bgmData.loaded = true
			a.bgmData.loadedAt = time.Now()
			a.bgmData.mu.Unlock()

			bgmObj := Obj{}
			for k, v := range byBgm {
				bgmObj[k] = v
			}
			aniObj := Obj{}
			for k, v := range byAni {
				aniObj[k] = v
			}
			malObj := Obj{}
			for k, v := range byMal {
				malObj[k] = v
			}
			return Obj{
				"bangumi":          bgmObj,
				"anilist":          aniObj,
				"mal":              malObj,
				"broadcastBangumi": fromStringMap(bcastBgm),
				"broadcastAniList": fromStringMap(bcastAni),
				"broadcastMAL":     fromStringMap(bcastMal),
				"mikanBangumi":     fromStringMap(mikanBgm),
			}, nil
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("bangumi-data unavailable")
		}
		return nil, lastErr
	})
	if err == nil {
		a.bgmData.mu.RLock()
		loaded := a.bgmData.loaded
		a.bgmData.mu.RUnlock()
		if !loaded {
			a.loadBangumiDataFromDisk()
		}
	}
}

func (a *App) ensureBilibiliTimelineLoaded(ctx context.Context) {
	a.bgmData.mu.RLock()
	fresh := len(a.bgmData.biliTimelineByKey) > 0 && time.Since(a.bgmData.biliTimelineAt) < 2*time.Hour
	a.bgmData.mu.RUnlock()
	if fresh {
		return
	}
	if cached, ok := a.peekDiscoveryCache("bili:timeline:v1", 2*time.Hour); ok {
		byKey := toStringMap(object(cached["byKey"]))
		if len(byKey) > 0 {
			a.bgmData.mu.Lock()
			a.bgmData.biliTimelineByKey = byKey
			a.bgmData.biliTimelineAt = time.Now()
			a.bgmData.mu.Unlock()
			return
		}
	}
	_, _ = a.discoveryCached(ctx, "bili:timeline:v1", 2*time.Hour, 24*time.Hour, func(fetchCtx context.Context) (Obj, error) {
		headers := map[string]string{
			"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
			"Referer":    "https://www.bilibili.com/",
		}
		byKey := map[string]string{}
		for _, t := range []string{"1", "4"} {
			reqCtx, cancel := context.WithTimeout(fetchCtx, 3500*time.Millisecond)
			endpoint := "https://api.bilibili.com/pgc/web/timeline?types=" + t + "&before=7&after=7"
			raw, err := a.requestJSON(reqCtx, "GET", endpoint, nil, headers)
			cancel()
			if err != nil {
				continue
			}
			for _, dayRaw := range list(raw["result"]) {
				dayObj := object(dayRaw)
				for _, epRaw := range list(dayObj["episodes"]) {
					ep := object(epRaw)
					title := strings.TrimSpace(str(ep["title"]))
					ts := num(ep["pub_ts"])
					if title == "" || ts <= 0 {
						continue
					}
					iso := time.Unix(ts, 0).UTC().Format(time.RFC3339)
					if k := titleKey(title); k != "" {
						byKey[k] = iso
					}
				}
			}
		}
		if len(byKey) == 0 {
			return nil, fmt.Errorf("bilibili timeline empty")
		}
		a.bgmData.mu.Lock()
		a.bgmData.biliTimelineByKey = byKey
		a.bgmData.biliTimelineAt = time.Now()
		a.bgmData.mu.Unlock()
		return Obj{"byKey": fromStringMap(byKey)}, nil
	})
}

func (a *App) warmBangumiDataIndex() {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	a.ensureBangumiDataLoaded(ctx)
	a.ensureBilibiliTimelineLoaded(ctx)
}

func (a *App) enrichAnimeBroadcast(anime Obj) {
	if anime == nil {
		return
	}
	rawID := strings.TrimSpace(str(anime["animeId"]))
	bgmID := strings.TrimSpace(str(anime["bangumiId"]))
	if bgmID == "" && !strings.Contains(rawID, ":") {
		bgmID = rawID
	}

	a.bgmData.mu.RLock()
	loaded := a.bgmData.loaded
	a.bgmData.mu.RUnlock()
	if !loaded {
		a.loadBangumiDataFromDisk()
	}

	a.bgmData.mu.RLock()
	var bcast, mikanID string
	switch {
	case strings.HasPrefix(rawID, "anilist:"):
		bcast = a.bgmData.broadcastByAniList[strings.TrimPrefix(rawID, "anilist:")]
	case strings.HasPrefix(rawID, "mal:"):
		bcast = a.bgmData.broadcastByMAL[strings.TrimPrefix(rawID, "mal:")]
	case bgmID != "":
		bcast = a.bgmData.broadcastByBangumi[bgmID]
		mikanID = a.bgmData.mikanByBangumi[bgmID]
	}
	if bcast == "" && len(a.bgmData.biliTimelineByKey) > 0 {
		for _, candidate := range []string{str(anime["title"]), str(anime["searchKeyword"])} {
			ck := titleKey(candidate)
			if ck == "" {
				continue
			}
			if hit := a.bgmData.biliTimelineByKey[ck]; hit != "" {
				bcast = hit
				break
			}
			if len([]rune(ck)) >= 4 {
				for bk, iso := range a.bgmData.biliTimelineByKey {
					if len([]rune(bk)) >= 4 && (strings.HasPrefix(ck, bk) || strings.HasPrefix(bk, ck)) {
						bcast = iso
						break
					}
				}
			}
			if bcast != "" {
				break
			}
		}
	}
	a.bgmData.mu.RUnlock()

	if bcast != "" && str(anime["broadcastAt"]) == "" {
		anime["broadcastAt"] = bcast
	}
	if mikanID != "" && str(anime["mikanId"]) == "" {
		anime["mikanId"] = mikanID
	}
}

func deriveBroadcastFromDownloads(downloads []Obj) string {
	for _, dl := range downloads {
		pub := strings.TrimSpace(str(dl["publishedAt"]))
		if pub == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, pub); err == nil {
			utc := t.UTC()
			if !(utc.Hour() == 0 && utc.Minute() == 0 && utc.Second() == 0) {
				return utc.Format(time.RFC3339)
			}
		}
	}
	return ""
}

func (a *App) lookupBangumiDataMemory(animeID string) []Obj {
	a.bgmData.mu.RLock()
	loaded := a.bgmData.loaded
	a.bgmData.mu.RUnlock()
	if !loaded {
		a.loadBangumiDataFromDisk()
	}

	a.bgmData.mu.RLock()
	defer a.bgmData.mu.RUnlock()
	if !a.bgmData.loaded {
		return nil
	}
	switch {
	case strings.HasPrefix(animeID, "anilist:"):
		return cloneObjSlice(a.bgmData.byAniList[strings.TrimPrefix(animeID, "anilist:")])
	case strings.HasPrefix(animeID, "mal:"):
		return cloneObjSlice(a.bgmData.byMAL[strings.TrimPrefix(animeID, "mal:")])
	default:
		bgmID := strings.TrimPrefix(animeID, "bangumi:")
		return cloneObjSlice(a.bgmData.byBangumi[bgmID])
	}
}

func mergeStreamingLinks(lists ...[]Obj) []Obj {
	merged := []Obj{}
	idxByID := map[string]int{}
	seenURL := map[string]bool{}

	for _, group := range lists {
		for _, item := range group {
			if item == nil {
				continue
			}
			id := strings.TrimSpace(str(item["id"]))
			label := strings.TrimSpace(str(item["label"]))
			u := strings.TrimSpace(str(item["url"]))
			if id == "" || label == "" || u == "" {
				continue
			}
			isDirect := flag(item["direct"])
			entry := Obj{"id": id, "label": label, "url": u, "direct": isDirect}

			if existingIdx, ok := idxByID[id]; ok {
				prevDirect := flag(merged[existingIdx]["direct"])
				if isDirect && !prevDirect {
					delete(seenURL, str(merged[existingIdx]["url"]))
					merged[existingIdx] = entry
					seenURL[u] = true
				}
				continue
			}
			if seenURL[u] {
				continue
			}
			idxByID[id] = len(merged)
			seenURL[u] = true
			merged = append(merged, entry)
		}
	}

	hasSpecificYouTube := false
	hasMainlandBilibili := false
	for _, entry := range merged {
		switch str(entry["id"]) {
		case "muse", "ani_one", "tropics", "mighty":
			hasSpecificYouTube = true
		case "bilibili":
			if flag(entry["direct"]) {
				hasMainlandBilibili = true
			}
		}
	}
	if hasSpecificYouTube || hasMainlandBilibili {
		filtered := make([]Obj, 0, len(merged))
		for _, entry := range merged {
			id := str(entry["id"])
			if hasSpecificYouTube && id == "youtube" {
				continue
			}
			if hasMainlandBilibili && id == "bilibili_hk_mo_tw" {
				continue
			}
			filtered = append(filtered, entry)
		}
		merged = filtered
	}

	sort.SliceStable(merged, func(i, j int) bool {
		pi, oki := streamingPlatformPriority[str(merged[i]["id"])]
		if !oki {
			pi = 500
		}
		pj, okj := streamingPlatformPriority[str(merged[j]["id"])]
		if !okj {
			pj = 500
		}
		return pi < pj
	})
	return merged
}

func (a *App) fastStreamingLinks(animeID string, bgmSubject Obj, primaryTitle string) []Obj {
	normID := strings.TrimSpace(animeID)
	if !strings.Contains(normID, ":") {
		normID = "bangumi:" + normID
	}
	if cached, ok := a.peekDiscoveryCache("streaming:v3:"+normID, 24*time.Hour); ok {
		out := []Obj{}
		for _, v := range list(cached["links"]) {
			if m := object(v); m != nil {
				out = append(out, m)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	memLinks := a.lookupBangumiDataMemory(normID)
	var infoboxLinks []Obj
	if bgmSubject != nil {
		infoboxLinks = extractBangumiStreamingLinks(bgmSubject, primaryTitle)
	}
	return mergeStreamingLinks(memLinks, infoboxLinks)
}

func stripHTMLTags(s string) string {
	return strings.TrimSpace(htmlTagPattern.ReplaceAllString(s, ""))
}

func matchBilibiliResultURL(results []any, candidates []string, allowSeriesPrefix bool) string {
	candKeys := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if k := titleKey(c); k != "" {
			candKeys = append(candKeys, k)
		}
	}
	if len(candKeys) == 0 {
		return ""
	}

	var dubFallback string
	var prefixFallback string

	for _, raw := range results {
		row := object(raw)
		cleanTitle := stripHTMLTags(str(row["title"]))
		rowKey := titleKey(cleanTitle)
		linkURL := strings.TrimSpace(str(row["url"]))
		if linkURL == "" {
			linkURL = strings.TrimSpace(str(row["goto_url"]))
		}
		if rowKey == "" || linkURL == "" {
			continue
		}
		if strings.HasPrefix(linkURL, "http://") {
			linkURL = "https://" + strings.TrimPrefix(linkURL, "http://")
		}

		for _, ck := range candKeys {
			if rowKey == ck {
				return linkURL
			}
			if dubFallback == "" && (rowKey == ck+"中配版" || rowKey == ck+"日配版" || rowKey == ck+"粤配版") {
				dubFallback = linkURL
			}
			if allowSeriesPrefix && prefixFallback == "" && len([]rune(cleanTitle)) >= 3 && strings.HasPrefix(ck, rowKey) {
				prefixFallback = linkURL
			}
		}
	}
	if dubFallback != "" {
		return dubFallback
	}
	return prefixFallback
}

func (a *App) searchBilibiliDirectLink(ctx context.Context, candidates []string, allowSeriesPrefix bool) []Obj {
	var queryTitle string
	for _, c := range candidates {
		if trimmed := strings.TrimSpace(c); trimmed != "" {
			queryTitle = trimmed
			break
		}
	}
	if queryTitle == "" {
		return nil
	}
	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
		"Referer":    "https://www.bilibili.com/",
		"Cookie":     "buvid3=bgmcat",
	}
	searchTypes := []string{"media_bangumi"}
	if allowSeriesPrefix {
		searchTypes = append(searchTypes, "media_ft")
	}
	for _, st := range searchTypes {
		reqCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		endpoint := "https://api.bilibili.com/x/web-interface/search/type?search_type=" + st + "&keyword=" + url.QueryEscape(queryTitle)
		raw, err := a.requestJSON(reqCtx, "GET", endpoint, nil, headers)
		cancel()
		if err != nil {
			continue
		}
		results := list(object(raw["data"])["result"])
		if matchedURL := matchBilibiliResultURL(results, candidates, allowSeriesPrefix); matchedURL != "" {
			return []Obj{{"id": "bilibili", "label": "哔哩哔哩", "url": matchedURL, "direct": true}}
		}
	}
	return nil
}

func aniListExternalSiteToLink(site, rawURL string) (string, string, string) {
	u := strings.TrimSpace(rawURL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "", "", ""
	}
	s := strings.ToLower(strings.TrimSpace(site))
	switch {
	case s == "bilibili":
		return "bilibili", "哔哩哔哩", u
	case strings.Contains(s, "bahamut") || strings.Contains(u, "gamer.com.tw"):
		return "bahamut", "动画疯", u
	case strings.Contains(s, "iqiyi"):
		return "iqiyi", "爱奇艺", u
	case strings.Contains(s, "tencent") || strings.Contains(s, "wetv") || strings.Contains(u, "v.qq.com"):
		return "qq", "腾讯视频", u
	case strings.Contains(s, "youku"):
		return "youku", "优酷", u
	case strings.Contains(s, "mango") || strings.Contains(u, "mgtv.com"):
		return "mgtv", "芒果TV", u
	case strings.Contains(s, "acfun"):
		return "acfun", "AcFun", u
	case strings.Contains(s, "muse"):
		return "muse", "木棉花", u
	case strings.Contains(s, "ani-one"):
		return "ani_one", "Ani-One", u
	case strings.Contains(s, "tropics"):
		return "tropics", "回归线", u
	case strings.Contains(s, "mighty"):
		return "mighty", "曼迪", u
	case strings.Contains(s, "youtube"):
		return "youtube", "YouTube", u
	case strings.Contains(s, "netflix"):
		return "netflix", "Netflix", u
	case strings.Contains(s, "disney"):
		return "disneyplus", "Disney+", u
	default:
		return "", "", ""
	}
}

func aniListTitleMatches(mediaTitle Obj, candidates []string) bool {
	mediaKeys := []string{
		titleKey(str(mediaTitle["native"])),
		titleKey(str(mediaTitle["romaji"])),
		titleKey(str(mediaTitle["english"])),
	}
	for _, cand := range candidates {
		ck := titleKey(cand)
		if ck == "" {
			continue
		}
		for _, mk := range mediaKeys {
			if mk != "" && mk == ck {
				return true
			}
		}
	}
	return false
}

func (a *App) searchAniListStreamingLinks(ctx context.Context, aniListID string, searchTitle string, candidates []string) []Obj {
	reqCtx, cancel := context.WithTimeout(ctx, 3000*time.Millisecond)
	defer cancel()

	var payload Obj
	if strings.TrimSpace(aniListID) != "" {
		var numID int
		fmt.Sscanf(strings.TrimSpace(aniListID), "%d", &numID)
		if numID <= 0 {
			return nil
		}
		payload = Obj{
			"query":     `query($id:Int){Media(id:$id,type:ANIME){title{native romaji english} externalLinks{site url type}}}`,
			"variables": Obj{"id": numID},
		}
	} else if strings.TrimSpace(searchTitle) != "" {
		payload = Obj{
			"query":     `query($q:String){Media(search:$q,type:ANIME){title{native romaji english} externalLinks{site url type}}}`,
			"variables": Obj{"q": strings.TrimSpace(searchTitle)},
		}
	} else {
		return nil
	}

	raw, err := a.requestJSON(reqCtx, "POST", "https://graphql.anilist.co", payload, nil)
	if err != nil {
		return nil
	}
	media := object(object(raw["data"])["Media"])
	if media == nil {
		return nil
	}
	if strings.TrimSpace(aniListID) == "" && !aniListTitleMatches(object(media["title"]), candidates) {
		return nil
	}

	links := []Obj{}
	for _, rawLink := range list(media["externalLinks"]) {
		l := object(rawLink)
		if strings.ToUpper(strings.TrimSpace(str(l["type"]))) != "STREAMING" {
			continue
		}
		pid, label, u := aniListExternalSiteToLink(str(l["site"]), str(l["url"]))
		if pid != "" && u != "" {
			links = append(links, Obj{"id": pid, "label": label, "url": u, "direct": true})
		}
	}
	return links
}

func (a *App) resolveAnimeStreamingLinks(ctx context.Context, rawID string) ([]Obj, error) {
	normID := strings.TrimSpace(rawID)
	if normID == "" {
		return []Obj{}, nil
	}
	if !strings.Contains(normID, ":") {
		normID = "bangumi:" + normID
	}

	cached, err := a.discoveryCached(ctx, "streaming:v3:"+normID, 24*time.Hour, 14*24*time.Hour, func(workCtx context.Context) (Obj, error) {
		var primaryTitle, nativeTitle string
		var candidates []string
		var infoboxLinks []Obj
		var aniListID string
		hasInfoboxBilibili := false

		if strings.HasPrefix(normID, "bangumi:") {
			bgmID := strings.TrimPrefix(normID, "bangumi:")
			subj, e := a.discoveryCached(workCtx, "subject:"+bgmID, 6*time.Hour, 7*24*time.Hour, func(c context.Context) (Obj, error) {
				return a.requestJSON(c, "GET", "https://api.bgm.tv/v0/subjects/"+url.PathEscape(bgmID), nil, nil)
			})
			if e == nil && subj != nil {
				anime := animeFromBangumi(subj, 0)
				primaryTitle = strings.TrimSpace(str(anime["title"]))
				nativeTitle = strings.TrimSpace(str(subj["name"]))
				cnTitle := strings.TrimSpace(str(subj["name_cn"]))
				aliases := extractBangumiChineseAliases(subj, primaryTitle)
				candidates = normalizeSearchKeywords(append([]string{cnTitle, primaryTitle, nativeTitle}, aliases...))
				infoboxLinks = extractBangumiStreamingLinks(subj, primaryTitle)
				for _, l := range infoboxLinks {
					if str(l["id"]) == "bilibili" {
						hasInfoboxBilibili = true
					}
				}
			}
		} else {
			if strings.HasPrefix(normID, "anilist:") {
				aniListID = strings.TrimPrefix(normID, "anilist:")
			}
			if d, _ := a.Store.Get("cache", "detail:"+normID); d != nil {
				anime := object(d["anime"])
				primaryTitle = strings.TrimSpace(str(anime["title"]))
				nativeTitle = primaryTitle
				candidates = normalizeSearchKeywords([]string{primaryTitle, str(d["mikanKeyword"])})
			}
		}

		var (
			wg           sync.WaitGroup
			bgmDataLinks []Obj
			biliLinks    []Obj
			aniLinks     []Obj
		)

		wg.Add(1)
		go func() {
			defer wg.Done()
			a.ensureBangumiDataLoaded(workCtx)
			bgmDataLinks = a.lookupBangumiDataMemory(normID)
		}()

		if len(candidates) > 0 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				biliLinks = a.searchBilibiliDirectLink(workCtx, candidates, hasInfoboxBilibili)
			}()
		}

		aniQueryTitle := nativeTitle
		if aniQueryTitle == "" {
			aniQueryTitle = primaryTitle
		}
		if aniListID != "" || aniQueryTitle != "" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				aniLinks = a.searchAniListStreamingLinks(workCtx, aniListID, aniQueryTitle, candidates)
			}()
		}

		wg.Wait()

		// Direct links first so they always supersede infobox search fallbacks.
		merged := mergeStreamingLinks(bgmDataLinks, biliLinks, aniLinks, infoboxLinks)
		return Obj{"links": merged}, nil
	})
	if err != nil {
		return []Obj{}, err
	}
	out := []Obj{}
	for _, v := range list(cached["links"]) {
		if m := object(v); m != nil {
			out = append(out, m)
		}
	}
	return out, nil
}
