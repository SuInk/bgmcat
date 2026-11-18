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
	mu        sync.RWMutex
	loaded    bool
	loadedAt  time.Time
	byBangumi map[string][]Obj
	byAniList map[string][]Obj
	byMAL     map[string][]Obj
}

var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

var streamingPlatformPriority = map[string]int{
	"bilibili":          10,
	"bilibili_hk_mo_tw": 12,
	"bilibili_tv":       14,
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
	"crunchyroll":       80,
	"prime":             85,
	"hidive":            88,
	"hulu":              90,
	"abema":             95,
	"danime":            100,
	"unext":             105,
	"nicovideo":         110,
	"viu":               115,
	"mytv":              120,
	"letv":              125,
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
		return "bilibili_hk_mo_tw", "哔哩哔哩（港澳台）", pick("https://www.bilibili.com/bangumi/media/md{{id}}/")
	case "gamer", "gamer_hk":
		return "bahamut", "巴哈姆特动画疯", pick("https://ani.gamer.com.tw/animeRef.php?sn={{id}}")
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
		return "muse", "木棉花 YouTube", pick("https://www.youtube.com/playlist?list={{id}}")
	case "ani_one", "ani_one_asia":
		return "ani_one", "Ani-One YouTube", pick("https://www.youtube.com/playlist?list={{id}}")
	case "tropics":
		return "tropics", "回归线 YouTube", pick("https://www.youtube.com/playlist?list={{id}}")
	case "mighty":
		return "mighty", "曼迪 YouTube", pick("https://www.youtube.com/playlist?list={{id}}")
	case "youtube":
		return "youtube", "YouTube", pick("https://www.youtube.com/playlist?list={{id}}")
	case "netflix":
		return "netflix", "Netflix", pick("https://www.netflix.com/title/{{id}}")
	case "disneyplus":
		return "disneyplus", "Disney+", pick("https://www.disneyplus.com/series/view/{{id}}")
	case "crunchyroll":
		return "crunchyroll", "Crunchyroll", pick("https://www.crunchyroll.com/series/{{id}}/")
	case "prime":
		return "prime", "Prime Video", pick("https://www.amazon.co.jp/gp/video/detail/{{id}}")
	case "abema":
		return "abema", "ABEMA", pick("https://abema.tv/video/title/{{id}}")
	case "danime":
		return "danime", "dアニメ", pick("https://animestore.docomo.ne.jp/animestore/ci_pc?workId={{id}}")
	case "unext":
		return "unext", "U-NEXT", pick("https://video.unext.jp/title/{{id}}")
	case "nicovideo":
		return "nicovideo", "Niconico", pick("https://ch.nicovideo.jp/{{id}}")
	case "viu":
		return "viu", "Viu", pick("https://www.viu.com/ott/hk/zh-hk/vod/{{id}}/")
	case "mytv":
		return "mytv", "myTV SUPER", pick("https://www.mytvsuper.com/tc/programme/{{id}}/")
	case "letv":
		return "letv", "乐视", pick("https://www.le.com/comic/{{id}}.html")
	default:
		return "", "", ""
	}
}

func parseBangumiDataPayload(raw Obj) (map[string][]Obj, map[string][]Obj, map[string][]Obj) {
	byBangumi := map[string][]Obj{}
	byAniList := map[string][]Obj{}
	byMAL := map[string][]Obj{}

	for _, rawItem := range list(raw["items"]) {
		item := object(rawItem)
		var bgmID, aniID, malID string
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
			default:
				pid, label, linkURL := bangumiDataSiteToLink(siteName, siteID, str(s["url"]))
				if pid != "" && linkURL != "" {
					links = append(links, Obj{"id": pid, "label": label, "url": linkURL, "direct": true})
				}
			}
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
	return byBangumi, byAniList, byMAL
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

func (a *App) loadBangumiDataFromDisk() bool {
	cached, ok := a.peekDiscoveryCache("bangumi-data:index:v1", 24*time.Hour)
	if !ok {
		return false
	}
	byBgm := toIndexedLinksMap(object(cached["bangumi"]))
	if len(byBgm) == 0 {
		return false
	}
	byAni := toIndexedLinksMap(object(cached["anilist"]))
	byMal := toIndexedLinksMap(object(cached["mal"]))

	a.bgmData.mu.Lock()
	a.bgmData.byBangumi = byBgm
	a.bgmData.byAniList = byAni
	a.bgmData.byMAL = byMal
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

	_, err := a.discoveryCached(ctx, "bangumi-data:index:v1", 24*time.Hour, 30*24*time.Hour, func(fetchCtx context.Context) (Obj, error) {
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
			byBgm, byAni, byMal := parseBangumiDataPayload(raw)
			if len(byBgm) == 0 {
				lastErr = fmt.Errorf("bangumi-data empty from %s", ep)
				continue
			}
			a.bgmData.mu.Lock()
			a.bgmData.byBangumi = byBgm
			a.bgmData.byAniList = byAni
			a.bgmData.byMAL = byMal
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
			return Obj{"bangumi": bgmObj, "anilist": aniObj, "mal": malObj}, nil
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

func (a *App) warmBangumiDataIndex() {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	a.ensureBangumiDataLoaded(ctx)
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
	if cached, ok := a.peekDiscoveryCache("streaming:v2:"+normID, 24*time.Hour); ok {
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
	case strings.Contains(s, "bilibili tv") || strings.Contains(u, "bilibili.tv"):
		return "bilibili_tv", "Bilibili Intl", u
	case strings.Contains(s, "bahamut") || strings.Contains(u, "gamer.com.tw"):
		return "bahamut", "巴哈姆特动画疯", u
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
		return "muse", "木棉花 YouTube", u
	case strings.Contains(s, "ani-one"):
		return "ani_one", "Ani-One YouTube", u
	case strings.Contains(s, "youtube"):
		return "youtube", "YouTube", u
	case strings.Contains(s, "netflix"):
		return "netflix", "Netflix", u
	case strings.Contains(s, "disney"):
		return "disneyplus", "Disney+", u
	case strings.Contains(s, "crunchyroll"):
		return "crunchyroll", "Crunchyroll", u
	case strings.Contains(s, "amazon") || strings.Contains(s, "prime"):
		return "prime", "Prime Video", u
	case strings.Contains(s, "hidive"):
		return "hidive", "HIDIVE", u
	case strings.Contains(s, "hulu"):
		return "hulu", "Hulu", u
	case strings.Contains(s, "abema"):
		return "abema", "ABEMA", u
	default:
		if strings.TrimSpace(site) != "" {
			return "ext:" + s, strings.TrimSpace(site), u
		}
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

	cached, err := a.discoveryCached(ctx, "streaming:v2:"+normID, 24*time.Hour, 14*24*time.Hour, func(workCtx context.Context) (Obj, error) {
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
