package app

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

var platformNames = map[string]string{"bangumi": "Bangumi", "anilist": "AniList", "mal": "MyAnimeList (Jikan)", "tmdb": "TMDB"}

func titleKey(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}
func (a *App) platformSearch(ctx context.Context, id, q string) ([]Obj, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if id == "bangumi" {
		o, e := a.bangumiSearch(ctx, q)
		if e != nil {
			return nil, e
		}
		out := []Obj{}
		for _, v := range list(o["items"]) {
			out = append(out, object(v))
		}
		return out, nil
	}
	var raw Obj
	var e error
	var entries []any
	switch id {
	case "anilist":
		raw, e = a.requestJSON(ctx, "POST", "https://graphql.anilist.co", Obj{"query": `query($q:String){Page(perPage:15){media(search:$q,type:ANIME,sort:SEARCH_MATCH){id idMal title{romaji native english} coverImage{large} averageScore siteUrl description(asHtml:false) episodes}}}`, "variables": Obj{"q": q}}, nil)
		entries = list(object(object(raw["data"])["Page"])["media"])
		if len(list(raw["errors"])) > 0 {
			return nil, errors.New("AniList 查询失败")
		}
	case "mal":
		raw, e = a.requestJSON(ctx, "GET", "https://api.jikan.moe/v4/anime?limit=15&q="+url.QueryEscape(q), nil, nil)
		entries = list(raw["data"])
	case "tmdb":
		key := str(a.Settings()["tmdbApiKey"])
		if key == "" {
			return nil, errors.New("请配置 TMDB API key")
		}
		raw, e = a.requestJSON(ctx, "GET", "https://api.themoviedb.org/3/search/tv?include_adult=false&api_key="+url.QueryEscape(key)+"&query="+url.QueryEscape(q), nil, nil)
		entries = list(raw["results"])
	default:
		return nil, errors.New("未知信息平台")
	}
	if e != nil {
		return nil, e
	}
	out := []Obj{}
	for _, v := range entries {
		o := object(v)
		key := str(o["id"])
		title := ""
		var image, score, summary, episodes any
		var link string
		max := 10
		ids := Obj{}
		switch id {
		case "anilist":
			titles := object(o["title"])
			title = str(titles["native"])
			if title == "" {
				title = str(titles["romaji"])
			}
			image = object(o["coverImage"])["large"]
			score = o["averageScore"]
			summary = o["description"]
			episodes = o["episodes"]
			link = str(o["siteUrl"])
			max = 100
			if o["idMal"] != nil {
				ids["mal"] = str(o["idMal"])
			}
		case "mal":
			key = str(o["mal_id"])
			title = str(o["title_japanese"])
			if title == "" {
				title = str(o["title"])
			}
			image = object(object(o["images"])["jpg"])["large_image_url"]
			score = o["score"]
			summary = o["synopsis"]
			episodes = o["episodes"]
			link = str(o["url"])
		case "tmdb":
			title = str(o["name"])
			if p := str(o["poster_path"]); p != "" {
				image = "https://image.tmdb.org/t/p/w780" + p
			}
			score = o["vote_average"]
			summary = o["overview"]
			link = "https://www.themoviedb.org/tv/" + key
		}
		if key == "" || title == "" {
			continue
		}
		ids[id] = key
		anime := Obj{"animeId": id + ":" + key, "bangumiId": nil, "title": title, "searchKeyword": title, "imageUrl": image, "airDay": nil, "isOnAir": false, "isRestricted": false, "rating": nil, "source": "bangumi", "externalIds": ids, "matchedPlatforms": []string{id}, "mikanId": nil, "officialSite": nil}
		a.enrichAnimeBroadcast(anime)
		platform := Obj{"id": id, "displayName": platformNames[id], "url": link, "score": score, "scoreMax": max}
		detail := Obj{"anime": anime, "titles": []string{title}, "summary": summary, "intro": summary, "episodes": []any{}, "mikanKeyword": title, "tags": []any{}, "totalEpisodes": episodes, "platforms": []Obj{platform}, "platformFailures": []any{}}
		_ = a.Store.Put("cache", "detail:"+id+":"+key, detail)
		out = append(out, anime)
	}
	return out, nil
}
func (a *App) searchAnime(ctx context.Context, q string) (Obj, error) {
	if strings.TrimSpace(q) == "" {
		return nil, errors.New("请输入搜索关键词")
	}
	ids := list(a.Settings()["enabledMetadataPlatforms"])
	if len(ids) == 0 {
		ids = []any{"bangumi"}
	}
	type result struct {
		id    string
		items []Obj
		err   error
	}
	results := make([]result, len(ids))
	var wg sync.WaitGroup
	for i, v := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			items, e := a.platformSearch(ctx, id, q)
			results[i] = result{id, items, e}
		}(i, str(v))
	}
	wg.Wait()
	items := []Obj{}
	failures := []Obj{}
	byTitle := map[string]Obj{}
	for _, r := range results {
		if r.err != nil {
			failures = append(failures, Obj{"id": r.id, "displayName": platformNames[r.id], "reason": r.err.Error()})
			continue
		}
		for _, x := range r.items {
			k := titleKey(str(x["title"]))
			if old := byTitle[k]; old != nil {
				old["matchedPlatforms"] = append(list(old["matchedPlatforms"]), r.id)
				ids := object(old["externalIds"])
				for key, value := range object(x["externalIds"]) {
					ids[key] = value
				}
				old["externalIds"] = ids
				continue
			}
			byTitle[k] = x
			items = append(items, x)
		}
	}
	return Obj{"items": items, "platformFailures": failures}, nil
}

const (
	resourceCacheTTL         = 15 * time.Minute
	resourceCacheStale       = 6 * time.Hour
	resourceNegativeTTL      = 45 * time.Second
	progressiveFirstHitGrace = 160 * time.Millisecond
	progressiveMaxBudget     = 950 * time.Millisecond
)

type resourceFlight struct {
	done      chan struct{}
	items     []Obj
	err       error
	elapsedMs int64
	finished  time.Time
}

func cloneObjSlice(in []Obj) []Obj {
	if len(in) == 0 {
		return []Obj{}
	}
	copied, err := cloneCachedObj(Obj{"items": in})
	if err != nil {
		out := make([]Obj, len(in))
		copy(out, in)
		return out
	}
	raw := list(copied["items"])
	out := make([]Obj, 0, len(raw))
	for _, v := range raw {
		out = append(out, object(v))
	}
	return out
}

func (a *App) externalDetail(ctx context.Context, id string, summaryOnly ...bool) (Obj, error) {
	basic := len(summaryOnly) > 0 && summaryOnly[0]
	return a.externalDetailQuery(ctx, id, basic, false)
}

func (a *App) externalDetailQuery(ctx context.Context, id string, basic bool, progressive bool) (Obj, error) {
	d, e := a.Store.Get("cache", "detail:"+id)
	if e != nil {
		return nil, e
	}
	if d == nil {
		return nil, errors.New("请重新搜索此番剧以加载信息")
	}
	if anime := object(d["anime"]); anime != nil {
		a.enrichAnimeBroadcast(anime)
	}
	d["streamingLinks"] = a.fastStreamingLinks(id, nil, str(d["mikanKeyword"]))
	if basic {
		d["downloads"] = []Obj{}
		d["providerStatuses"] = a.pendingResourceStatuses()
		d["detailPending"] = true
		return d, nil
	}
	d["detailPending"] = false
	var downloads, statuses []Obj
	if progressive {
		downloads, statuses = a.progressiveResources(ctx, str(d["mikanKeyword"]))
	} else {
		downloads, statuses = a.parallelResources(ctx, str(d["mikanKeyword"]))
	}
	d["downloads"] = downloads
	d["providerStatuses"] = statuses
	if anime := object(d["anime"]); anime != nil && str(anime["broadcastAt"]) == "" && len(downloads) > 0 {
		if bcast := deriveBroadcastFromDownloads(downloads); bcast != "" {
			anime["broadcastAt"] = bcast
		}
	}
	return d, nil
}

func (a *App) startOrPeekResource(provider, keyword string) *resourceFlight {
	urlStr := a.resourceURL(provider, keyword)
	if urlStr == "" || strings.TrimSpace(keyword) == "" {
		ch := make(chan struct{})
		close(ch)
		return &resourceFlight{done: ch, items: []Obj{}, finished: time.Now()}
	}
	cacheKey := "resource:v3:" + hash(urlStr)
	if cached, ok := a.peekDiscoveryCache(cacheKey, resourceCacheTTL); ok {
		items := make([]Obj, 0, len(list(cached["items"])))
		for _, v := range list(cached["items"]) {
			items = append(items, object(v))
		}
		ch := make(chan struct{})
		close(ch)
		return &resourceFlight{done: ch, items: items, finished: time.Now()}
	}

	a.resourceMu.Lock()
	if a.resourceFlights == nil {
		a.resourceFlights = map[string]*resourceFlight{}
	}
	if existing := a.resourceFlights[cacheKey]; existing != nil {
		select {
		case <-existing.done:
			age := time.Since(existing.finished)
			if (existing.err == nil && age < resourceCacheTTL) || (existing.err != nil && age < resourceNegativeTTL) {
				a.resourceMu.Unlock()
				return existing
			}
		default:
			a.resourceMu.Unlock()
			return existing
		}
	}
	if len(a.resourceFlights) > 256 {
		cutoff := time.Now().Add(-5 * time.Minute)
		for k, v := range a.resourceFlights {
			select {
			case <-v.done:
				if v.finished.Before(cutoff) {
					delete(a.resourceFlights, k)
				}
			default:
			}
		}
	}
	flight := &resourceFlight{done: make(chan struct{})}
	a.resourceFlights[cacheKey] = flight
	a.resourceMu.Unlock()

	go func() {
		start := time.Now()
		bgCtx := a.ctx
		if bgCtx == nil {
			bgCtx = context.Background()
		}
		items, err := a.searchResources(bgCtx, provider, keyword)
		flight.items = cloneObjSlice(items)
		flight.err = err
		flight.elapsedMs = time.Since(start).Milliseconds()
		flight.finished = time.Now()
		close(flight.done)
	}()
	return flight
}

func inspectResourceFlights(flights []*resourceFlight) (doneCount int, hasItems bool) {
	for _, f := range flights {
		select {
		case <-f.done:
			doneCount++
			if len(f.items) > 0 {
				hasItems = true
			}
		default:
		}
	}
	return doneCount, hasItems
}

func waitForResourceFlights(ctx context.Context, flights []*resourceFlight) {
	if doneCount, _ := inspectResourceFlights(flights); doneCount >= len(flights) {
		return
	}
	notify := make(chan struct{}, len(flights))
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	for _, f := range flights {
		select {
		case <-f.done:
		default:
			go func(done <-chan struct{}) {
				select {
				case <-done:
					select {
					case notify <- struct{}{}:
					default:
					}
				case <-stopWatch:
				}
			}(f.done)
		}
	}

	maxTimer := time.NewTimer(progressiveMaxBudget)
	defer maxTimer.Stop()
	var graceTimer *time.Timer
	var graceCh <-chan time.Time
	defer func() {
		if graceTimer != nil {
			graceTimer.Stop()
		}
	}()

	for {
		doneCount, hasItems := inspectResourceFlights(flights)
		if doneCount >= len(flights) {
			return
		}
		if hasItems && graceTimer == nil {
			graceTimer = time.NewTimer(progressiveFirstHitGrace)
			graceCh = graceTimer.C
		}
		select {
		case <-notify:
		case <-graceCh:
			return
		case <-maxTimer.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

func normalizeSearchKeywords(keywords []string) []string {
	seen := make(map[string]bool, len(keywords))
	out := make([]string, 0, len(keywords))
	for _, raw := range keywords {
		kw := strings.TrimSpace(raw)
		if kw == "" {
			continue
		}
		key := strings.ToLower(kw)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, kw)
	}
	return out
}

// progressiveResources starts all enabled providers concurrently in detached background
// flights and returns as soon as fast providers yield results (plus a brief 160ms grace
// window to batch fast peers, capped at 950ms total), marking stragglers as pending.
func (a *App) progressiveResources(ctx context.Context, keyword string) ([]Obj, []Obj) {
	providers := list(a.Settings()["enabledResourceProviders"])
	if len(providers) == 0 {
		return []Obj{}, []Obj{}
	}
	flights := make([]*resourceFlight, len(providers))
	for i, v := range providers {
		flights[i] = a.startOrPeekResource(str(v), keyword)
	}
	waitForResourceFlights(ctx, flights)

	downloads := []Obj{}
	statuses := make([]Obj, len(providers))
	for i, v := range providers {
		provider := str(v)
		displayName := resourceProviderDisplayName(provider)
		f := flights[i]
		select {
		case <-f.done:
			reason := ""
			if f.err != nil {
				reason = f.err.Error()
			}
			items := cloneObjSlice(f.items)
			downloads = append(downloads, items...)
			statuses[i] = Obj{
				"providerId":  provider,
				"displayName": displayName,
				"ok":          f.err == nil,
				"pending":     false,
				"count":       len(items),
				"error":       reason,
				"reason":      reason,
				"elapsedMs":   f.elapsedMs,
			}
		default:
			statuses[i] = Obj{
				"providerId":  provider,
				"displayName": displayName,
				"ok":          true,
				"pending":     true,
				"count":       0,
				"error":       "",
				"reason":      "",
				"elapsedMs":   int64(0),
			}
		}
	}
	return mergeResourceDownloads(downloads), statuses
}

var (
	btihHexRE     = regexp.MustCompile(`(?i)xt=urn:btih:([0-9a-f]{40})\b`)
	btihBase32RE  = regexp.MustCompile(`(?i)xt=urn:btih:([a-z2-7]{32})\b`)
	torrentHashRE = regexp.MustCompile(`(?i)(?:^|/|hash=)([0-9a-f]{40})(?:\.torrent|\b)`)
	plainHex40RE  = regexp.MustCompile(`(?i)^[0-9a-f]{40}$`)
)

func extractCanonicalBTIH(item Obj) string {
	if raw := strings.TrimSpace(str(item["infoHash"])); plainHex40RE.MatchString(raw) {
		return strings.ToLower(raw)
	}
	if mag := str(item["magnetUrl"]); mag != "" {
		if m := btihHexRE.FindStringSubmatch(mag); len(m) > 1 {
			return strings.ToLower(m[1])
		}
		if m := btihBase32RE.FindStringSubmatch(mag); len(m) > 1 {
			if decoded, err := base32.StdEncoding.DecodeString(strings.ToUpper(m[1])); err == nil && len(decoded) == 20 {
				return hex.EncodeToString(decoded)
			}
		}
	}
	if tor := str(item["torrentUrl"]); tor != "" {
		if m := torrentHashRE.FindStringSubmatch(tor); len(m) > 1 {
			return strings.ToLower(m[1])
		}
	}
	return ""
}

func canonicalReleaseTitleKey(raw string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(raw)))
	return strings.Join(fields, " ")
}

func appendUniqueProviderIDs(dst Obj, incoming Obj) {
	seen := map[string]bool{}
	merged := []string{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			merged = append(merged, id)
		}
	}
	for _, v := range list(dst["providerIds"]) {
		add(str(v))
	}
	add(str(dst["providerId"]))
	for _, v := range list(incoming["providerIds"]) {
		add(str(v))
	}
	add(str(incoming["providerId"]))
	dst["providerIds"] = merged
}

func mergeResourceItem(dst Obj, incoming Obj) {
	appendUniqueProviderIDs(dst, incoming)
	if str(dst["infoHash"]) == "" {
		if btih := extractCanonicalBTIH(incoming); btih != "" {
			dst["infoHash"] = btih
		}
	}
	if str(dst["magnetUrl"]) == "" && str(incoming["magnetUrl"]) != "" {
		dst["magnetUrl"] = incoming["magnetUrl"]
	}
	if str(dst["torrentUrl"]) == "" && str(incoming["torrentUrl"]) != "" {
		dst["torrentUrl"] = incoming["torrentUrl"]
	}
	if str(dst["subgroup"]) == "" && str(incoming["subgroup"]) != "" {
		dst["subgroup"] = incoming["subgroup"]
	}
	if num(dst["sizeBytes"]) <= 0 && num(incoming["sizeBytes"]) > 0 {
		dst["sizeBytes"] = incoming["sizeBytes"]
		if str(incoming["sizeText"]) != "" {
			dst["sizeText"] = incoming["sizeText"]
		}
	} else if str(dst["sizeText"]) == "" && str(incoming["sizeText"]) != "" {
		dst["sizeText"] = incoming["sizeText"]
	}
	if str(dst["publishedAt"]) == "" && str(incoming["publishedAt"]) != "" {
		dst["publishedAt"] = incoming["publishedAt"]
	}
	if dst["seeders"] == nil && incoming["seeders"] != nil {
		dst["seeders"] = incoming["seeders"]
	}
	if dst["leechers"] == nil && incoming["leechers"] != nil {
		dst["leechers"] = incoming["leechers"]
	}
}

// mergeResourceDownloads collapses identical torrents returned by multiple providers
// (matched by 40-char BTIH infoHash or identical full release title with compatible size),
// preserving provider ordering and combining .torrent + magnet: fallback URLs.
func mergeResourceDownloads(downloads []Obj) []Obj {
	if len(downloads) <= 1 {
		return downloads
	}
	out := make([]Obj, 0, len(downloads))
	byHash := make(map[string]int, len(downloads))
	byTitle := make(map[string]int, len(downloads))

	for _, raw := range downloads {
		item := cloneObjSlice([]Obj{raw})[0]
		btih := extractCanonicalBTIH(item)
		if btih != "" && str(item["infoHash"]) == "" {
			item["infoHash"] = btih
		}
		tKey := canonicalReleaseTitleKey(str(item["title"]))

		if btih != "" {
			if idx, ok := byHash[btih]; ok {
				mergeResourceItem(out[idx], item)
				if tKey != "" {
					if _, hasT := byTitle[tKey]; !hasT {
						byTitle[tKey] = idx
					}
				}
				continue
			}
		}

		if len([]rune(tKey)) >= 18 {
			if idx, ok := byTitle[tKey]; ok {
				existing := out[idx]
				existingHash := extractCanonicalBTIH(existing)
				// Only merge by title if hashes don't conflict and file sizes don't conflict.
				if existingHash == "" || btih == "" || existingHash == btih {
					s1, s2 := num(existing["sizeBytes"]), num(item["sizeBytes"])
					diff := s1 - s2
					if diff < 0 {
						diff = -diff
					}
					if s1 <= 0 || s2 <= 0 || diff <= 2*1024*1024 {
						mergeResourceItem(existing, item)
						if btih != "" && existingHash == "" {
							byHash[btih] = idx
						}
						continue
					}
				}
			}
		}

		idx := len(out)
		out = append(out, item)
		if btih != "" {
			byHash[btih] = idx
		}
		if len([]rune(tKey)) >= 18 {
			byTitle[tKey] = idx
		}
	}
	return out
}

func (a *App) parallelResources(ctx context.Context, keyword string) ([]Obj, []Obj) {
	providers := list(a.Settings()["enabledResourceProviders"])
	results := make([][]Obj, len(providers))
	statuses := make([]Obj, len(providers))
	var wg sync.WaitGroup
	for i, v := range providers {
		wg.Add(1)
		go func(i int, provider string) {
			defer wg.Done()
			start := time.Now()
			items, err := a.searchResources(ctx, provider, keyword)
			reason := ""
			if err != nil {
				reason = err.Error()
			}
			results[i] = items
			statuses[i] = Obj{"providerId": provider, "displayName": resourceProviderDisplayName(provider), "ok": err == nil, "count": len(items), "error": reason, "reason": reason, "elapsedMs": time.Since(start).Milliseconds()}
		}(i, str(v))
	}
	wg.Wait()
	downloads := []Obj{}
	for _, items := range results {
		downloads = append(downloads, items...)
	}
	return mergeResourceDownloads(downloads), statuses
}

func (a *App) fetchResourcesMulti(ctx context.Context, keywords []string, progressive bool) ([]Obj, []Obj) {
	kws := normalizeSearchKeywords(keywords)
	if len(kws) == 0 {
		return []Obj{}, []Obj{}
	}
	if len(kws) == 1 {
		if progressive {
			return a.progressiveResources(ctx, kws[0])
		}
		return a.parallelResources(ctx, kws[0])
	}

	providers := list(a.Settings()["enabledResourceProviders"])
	if len(providers) == 0 {
		return []Obj{}, []Obj{}
	}

	if progressive {
		flightsByProv := make([][]*resourceFlight, len(providers))
		allFlights := make([]*resourceFlight, 0, len(providers)*len(kws))
		for i, v := range providers {
			p := str(v)
			flights := make([]*resourceFlight, len(kws))
			for k, kw := range kws {
				f := a.startOrPeekResource(p, kw)
				flights[k] = f
				allFlights = append(allFlights, f)
			}
			flightsByProv[i] = flights
		}
		waitForResourceFlights(ctx, allFlights)

		allDownloads := []Obj{}
		statuses := make([]Obj, len(providers))
		for i, v := range providers {
			provider := str(v)
			displayName := resourceProviderDisplayName(provider)
			provItems := []Obj{}
			anyPending := false
			anyOK := false
			lastErr := ""
			var maxElapsed int64
			for _, f := range flightsByProv[i] {
				select {
				case <-f.done:
					if f.elapsedMs > maxElapsed {
						maxElapsed = f.elapsedMs
					}
					if f.err != nil {
						lastErr = f.err.Error()
					} else {
						anyOK = true
					}
					provItems = append(provItems, cloneObjSlice(f.items)...)
				default:
					anyPending = true
				}
			}
			mergedProv := mergeResourceDownloads(provItems)
			allDownloads = append(allDownloads, mergedProv...)
			if anyPending {
				statuses[i] = Obj{
					"providerId":  provider,
					"displayName": displayName,
					"ok":          true,
					"pending":     true,
					"count":       len(mergedProv),
					"error":       "",
					"reason":      "",
					"elapsedMs":   maxElapsed,
				}
				continue
			}
			okFlag := anyOK || lastErr == ""
			reason := ""
			if !okFlag {
				reason = lastErr
			}
			statuses[i] = Obj{
				"providerId":  provider,
				"displayName": displayName,
				"ok":          okFlag,
				"pending":     false,
				"count":       len(mergedProv),
				"error":       reason,
				"reason":      reason,
				"elapsedMs":   maxElapsed,
			}
		}
		return mergeResourceDownloads(allDownloads), statuses
	}

	type kwRes struct {
		items   []Obj
		err     error
		elapsed int64
	}
	results := make([][]kwRes, len(providers))
	var wg sync.WaitGroup
	for i, v := range providers {
		provider := str(v)
		results[i] = make([]kwRes, len(kws))
		for k, kw := range kws {
			wg.Add(1)
			go func(i, k int, provider, kw string) {
				defer wg.Done()
				start := time.Now()
				items, err := a.searchResources(ctx, provider, kw)
				results[i][k] = kwRes{items: items, err: err, elapsed: time.Since(start).Milliseconds()}
			}(i, k, provider, kw)
		}
	}
	wg.Wait()

	allDownloads := []Obj{}
	statuses := make([]Obj, len(providers))
	for i, v := range providers {
		provider := str(v)
		displayName := resourceProviderDisplayName(provider)
		provItems := []Obj{}
		anyOK := false
		lastErr := ""
		var maxElapsed int64
		for _, r := range results[i] {
			if r.elapsed > maxElapsed {
				maxElapsed = r.elapsed
			}
			if r.err != nil {
				lastErr = r.err.Error()
			} else {
				anyOK = true
			}
			provItems = append(provItems, r.items...)
		}
		mergedProv := mergeResourceDownloads(provItems)
		allDownloads = append(allDownloads, mergedProv...)
		okFlag := anyOK || lastErr == ""
		reason := ""
		if !okFlag {
			reason = lastErr
		}
		statuses[i] = Obj{
			"providerId":  provider,
			"displayName": displayName,
			"ok":          okFlag,
			"count":       len(mergedProv),
			"error":       reason,
			"reason":      reason,
			"elapsedMs":   maxElapsed,
		}
	}
	return mergeResourceDownloads(allDownloads), statuses
}
