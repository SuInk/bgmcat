package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (a *App) getAny(ctx context.Context, endpoint string) (any, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "AnimeRSS-Go/"+Version+" (personal RSS reader)")
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
	return Obj{"animeId": id, "bangumiId": id, "title": title, "imageUrl": image, "searchKeyword": o["name"], "airDay": day % 7, "isOnAir": true, "isRestricted": flag(o["nsfw"]), "rating": object(o["rating"])["score"], "source": "bangumi", "mikanId": nil, "officialSite": nil, "externalIds": Obj{"bangumi": id}, "matchedPlatforms": []string{"bangumi"}}
}
func (a *App) homepage(ctx context.Context) ([]Obj, error) {
	// The key carries a version so a change to the cached item shape (e.g. cover URLs) takes effect immediately.
	result, err := a.discoveryCached(ctx, "home:v2", time.Hour, 7*24*time.Hour, func(ctx context.Context) (Obj, error) {
		data, err := a.getAny(ctx, "https://api.bgm.tv/calendar")
		if err != nil {
			return nil, err
		}
		out := []Obj{}
		for _, v := range list(data) {
			day := object(v)
			for _, item := range list(day["items"]) {
				out = append(out, animeFromBangumi(object(item), num(object(day["weekday"])["id"])))
			}
		}
		if len(out) == 0 {
			return nil, errors.New("Bangumi 返回空日历")
		}
		return Obj{"items": out}, nil
	})
	out := []Obj{}
	for _, v := range list(result["items"]) {
		out = append(out, object(v))
	}
	return out, err
}

func (a *App) bangumiSearch(ctx context.Context, keyword string) (Obj, error) {
	o, e := a.requestJSON(ctx, "POST", "https://api.bgm.tv/v0/search/subjects?limit=30", Obj{"keyword": keyword, "filter": Obj{"type": []int{2}}}, nil)
	if e != nil {
		return nil, e
	}
	items := []Obj{}
	for _, item := range list(o["data"]) {
		items = append(items, animeFromBangumi(object(item), 0))
	}
	return Obj{"items": items, "platformFailures": []any{}}, nil
}

// mikanBase returns the configured Mikan site. mikanime.tv used to be the
// mainland-friendly mirror but now only 302s to mikanani.me, so old saved values
// skip straight to the target instead of paying an extra TLS round trip.
func (a *App) mikanBase() string {
	base := strings.TrimRight(strings.TrimSpace(str(a.Settings()["mikanBaseUrl"])), "/")
	if base == "" || base == "https://mikanime.tv" || base == "http://mikanime.tv" {
		return "https://mikanani.me"
	}
	return base
}

func (a *App) resourceURL(provider, keyword string) string {
	switch provider {
	case "mikan":
		return a.mikanBase() + "/RSS/Search?searchstr=" + url.QueryEscape(keyword)
	case "nyaa":
		return "https://nyaa.si/?page=rss&c=1_2&f=0&q=" + url.QueryEscape(keyword)
	case "acgrip":
		return "https://acg.rip/.xml?term=" + url.QueryEscape(keyword)
	}
	return ""
}
func (a *App) searchResources(ctx context.Context, provider, keyword string) ([]Obj, error) {
	result, err := a.discoveryCached(ctx, "resource:"+hash(a.resourceURL(provider, keyword)), 2*time.Minute, 10*time.Minute, func(ctx context.Context) (Obj, error) {
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
func (a *App) fetchResources(ctx context.Context, provider, keyword string) ([]Obj, error) {
	endpoint := a.resourceURL(provider, keyword)
	if endpoint == "" {
		return nil, errors.New("未知资源来源")
	}
	feed, e := a.fetchFeed(ctx, endpoint)
	if e != nil {
		return nil, e
	}
	items := []Obj{}
	for _, item := range feed.Items {
		src, kind := feedSource(item)
		if src == "" {
			continue
		}
		published, size := feedItemMeta(item)
		var magnet, torrent any
		if kind == "magnet" {
			magnet = src
		} else if kind == "torrent-url" {
			torrent = src
		}
		items = append(items, Obj{"id": hash(src), "title": item.Title, "subgroup": subgroupOf(item.Title), "magnetUrl": magnet, "torrentUrl": torrent, "pageUrl": item.Link, "sizeText": sizeText(size), "publishedAt": published, "sourceType": kind, "providerId": provider, "displayName": provider, "resourceKey": hash(src), "infoHash": nil, "episode": episode(item.Title), "resolution": resolutionRE.FindString(item.Title), "providerIds": []string{provider}, "sizeBytes": sizeBytes(size), "seeders": nil, "leechers": nil, "sources": []Obj{{"providerId": provider, "pageUrl": item.Link}}})
	}
	return items, nil
}
func (a *App) animeDetail(ctx context.Context, id string, summaryOnly ...bool) (Obj, error) {
	basic := len(summaryOnly) > 0 && summaryOnly[0]
	if strings.Contains(id, ":") && !strings.HasPrefix(id, "bangumi:") {
		return a.externalDetail(ctx, id, basic)
	}
	id = strings.TrimPrefix(id, "bangumi:")
	o, e := a.discoveryCached(ctx, "subject:"+id, 6*time.Hour, 7*24*time.Hour, func(ctx context.Context) (Obj, error) {
		return a.requestJSON(ctx, "GET", "https://api.bgm.tv/v0/subjects/"+url.PathEscape(id), nil, nil)
	})
	if e != nil {
		return nil, e
	}
	anime := animeFromBangumi(o, 0)
	keyword := str(anime["title"])
	eps := []Obj{}
	downloads, statuses := []Obj{}, []Obj{}
	if !basic {
		type resourceResult struct{ downloads, statuses []Obj }
		resources := make(chan resourceResult, 1)
		go func() {
			downloads, statuses := a.parallelResources(ctx, keyword)
			resources <- resourceResult{downloads, statuses}
		}()
		epData, e := a.discoveryCached(ctx, "episodes:"+id, time.Hour, 7*24*time.Hour, func(ctx context.Context) (Obj, error) {
			return a.requestJSON(ctx, "GET", "https://api.bgm.tv/v0/episodes?subject_id="+url.QueryEscape(id)+"&type=0&limit=100", nil, nil)
		})
		if e == nil {
			for _, v := range list(epData["data"]) {
				ep := object(v)
				title := str(ep["name_cn"])
				if title == "" {
					title = str(ep["name"])
				}
				eps = append(eps, Obj{"episodeId": str(ep["id"]), "title": title, "episodeNumber": str(ep["sort"]), "airDate": ep["airdate"]})
			}
		}
		r := <-resources
		downloads, statuses = r.downloads, r.statuses
	} else {
		statuses = a.pendingResourceStatuses()
	}
	tags := []any{}
	for _, v := range list(o["tags"]) {
		tags = append(tags, object(v)["name"])
	}
	return Obj{"detailPending": basic, "anime": anime, "titles": []any{o["name"], o["name_cn"]}, "summary": o["summary"], "intro": o["summary"], "episodes": eps, "mikanKeyword": keyword, "downloads": downloads, "providerStatuses": statuses, "tags": tags, "rank": object(o["rating"])["rank"], "totalEpisodes": o["total_episodes"], "platforms": []Obj{{"id": "bangumi", "displayName": "Bangumi", "url": "https://bgm.tv/subject/" + id, "score": object(o["rating"])["score"], "scoreMax": 10}}, "platformFailures": []any{}}, nil
}
func (a *App) favorites() ([]Obj, error) {
	favorites, e := a.Store.List("favorites")
	if e != nil {
		return nil, e
	}
	subs, _ := a.Store.List("subscriptions")
	for _, f := range favorites {
		// Favorites saved before covers were resized still hold the original URL.
		if image := str(f["imageUrl"]); image != "" {
			f["imageUrl"] = bangumiCover(image)
		}
		related := []Obj{}
		f["subscriptionId"] = nil
		f["rssUrl"] = nil
		f["subscriptionAutoDownload"] = nil
		for _, s := range subs {
			if str(s["animeId"]) == str(f["animeId"]) {
				related = append(related, Obj{"id": s["id"], "providerId": s["providerId"], "providerName": s["providerId"], "name": s["name"], "url": s["url"], "enabled": s["enabled"], "autoDownload": object(s["rule"])["autoDownload"]})
				if f["subscriptionId"] == nil {
					f["subscriptionId"] = s["id"]
					f["rssUrl"] = s["url"]
					f["subscriptionName"] = s["name"]
					f["subscriptionAutoDownload"] = object(s["rule"])["autoDownload"]
				}
			}
		}
		f["subscriptions"] = related
	}
	return favorites, nil
}
func (a *App) discoveryRoutes() {
	a.handle("GET /api/discovery/home", func(w http.ResponseWriter, r *http.Request) error {
		o, e := a.homepage(r.Context())
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
		o, e := a.animeDetail(r.Context(), r.PathValue("id"), r.URL.Query().Get("view") == "summary")
		if e == nil {
			ok(w, o)
		}
		return e
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
		for _, id := range []string{"mikan", "nyaa", "acgrip"} {
			enabled := false
			for _, v := range list(a.Settings()["enabledResourceProviders"]) {
				if str(v) == id {
					enabled = true
				}
			}
			out = append(out, Obj{"id": id, "displayName": id, "enabled": enabled, "baseUrl": a.resourceURL(id, ""), "supportsSearch": true, "supportsSubscription": true})
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
		items, e := a.searchResources(r.Context(), r.PathValue("id"), str(o["keyword"]))
		reason := ""
		if e != nil {
			reason = e.Error()
		}
		ok(w, Obj{"providerId": r.PathValue("id"), "displayName": r.PathValue("id"), "ok": e == nil, "count": len(items), "error": reason, "reason": reason, "elapsedMs": time.Since(start).Milliseconds()})
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
		out := []Obj{}
		existing, _ := a.Store.List("subscriptions")
		for _, v := range providers {
			provider := str(v)
			var found Obj
			for _, s := range existing {
				if str(s["animeId"]) == id && str(s["providerId"]) == provider {
					found = s
					break
				}
			}
			if found != nil {
				out = append(out, found)
				continue
			}
			s, e := a.newSubscription(Obj{"name": str(anime["title"]) + " · " + provider, "url": a.resourceURL(provider, keyword), "animeId": id, "providerId": provider})
			if e != nil {
				return e
			}
			out = append(out, s)
		}
		anime["mikanKeyword"] = keyword
		anime["createdAt"] = now()
		anime["updatedAt"] = now()
		if e = a.Store.Put("favorites", id, anime); e != nil {
			return e
		}
		ok(w, out)
		return nil
	})
}

func (a *App) pendingResourceStatuses() []Obj {
	statuses := []Obj{}
	for _, provider := range list(a.Settings()["enabledResourceProviders"]) {
		statuses = append(statuses, Obj{"providerId": provider, "displayName": provider, "ok": true, "pending": true, "count": 0})
	}
	return statuses
}
