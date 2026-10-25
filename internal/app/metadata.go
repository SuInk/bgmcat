package app

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
)

var platformNames = map[string]string{"bangumi": "Bangumi", "anilist": "AniList", "mal": "MyAnimeList", "tmdb": "TMDB"}

func titleKey(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}
func (a *App) platformSearch(ctx context.Context, id, q string) ([]Obj, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
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
func (a *App) externalDetail(ctx context.Context, id string) (Obj, error) {
	d, e := a.Store.Get("cache", "detail:"+id)
	if e != nil {
		return nil, e
	}
	if d == nil {
		return nil, errors.New("请重新搜索此番剧以加载信息")
	}
	downloads := []Obj{}
	statuses := []Obj{}
	for _, v := range list(a.Settings()["enabledResourceProviders"]) {
		p := str(v)
		items, e := a.searchResources(ctx, p, str(d["mikanKeyword"]))
		var message any
		if e != nil {
			message = e.Error()
		}
		statuses = append(statuses, Obj{"providerId": p, "displayName": p, "ok": e == nil, "count": len(items), "error": message})
		downloads = append(downloads, items...)
	}
	d["downloads"] = downloads
	d["providerStatuses"] = statuses
	return d, nil
}
