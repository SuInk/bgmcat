package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/mmcdole/gofeed"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func hash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func httpURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

var episodeRE = regexp.MustCompile(`(?i)(?:\s-\s|\[|第|EP?\s*)(\d{1,4})(?:[\]话話集\s.]|$)`)
var resolutionRE = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|4k)\b`)

// subgroupRE grabs the fansub tag that release names lead with: [LoliHouse] …, 【喵萌奶茶屋】★10月新番★ ….
var subgroupRE = regexp.MustCompile(`^\s*[\[【]([^\]】]{1,40})[\]】]`)

// subgroupOf returns the leading bracketed group, or nil when the title opens
// with something that is clearly not a group name (a resolution or episode tag).
func subgroupOf(title string) any {
	m := subgroupRE.FindStringSubmatch(title)
	if len(m) < 2 {
		return nil
	}
	name := strings.TrimSpace(m[1])
	if name == "" || resolutionRE.MatchString(name) || strings.Trim(name, "0123456789") == "" {
		return nil
	}
	return name
}

var (
	torrentPubDateRE = regexp.MustCompile(`<pubDate>\s*([^<]+?)\s*</pubDate>`)
	torrentLengthRE  = regexp.MustCompile(`<contentLength>\s*(\d+)\s*</contentLength>`)
	mikanTimeZone    = time.FixedZone("CST", 8*3600)
)

// feedItemMeta returns an RFC3339 publish time and byte size for a feed item.
// Mikan leaves the standard <pubDate> empty and puts both in its own
// <torrent xmlns="https://mikanani.me/0.1/"> block (local China time, no zone),
// which gofeed keeps as raw text in item.Custom["torrent"].
func feedItemMeta(item *gofeed.Item) (published string, size int64) {
	if item.PublishedParsed != nil {
		published = item.PublishedParsed.Format(time.RFC3339)
	}
	custom := item.Custom["torrent"]
	if published == "" {
		if m := torrentPubDateRE.FindStringSubmatch(custom); m != nil {
			if t, e := time.ParseInLocation("2006-01-02T15:04:05", m[1], mikanTimeZone); e == nil {
				published = t.Format(time.RFC3339)
			}
		}
	}
	if published == "" {
		published = item.Published
	}
	if m := torrentLengthRE.FindStringSubmatch(custom); m != nil {
		size, _ = strconv.ParseInt(m[1], 10, 64)
	}
	for _, enclosure := range item.Enclosures {
		if size > 0 {
			break
		}
		size, _ = strconv.ParseInt(enclosure.Length, 10, 64)
	}
	return published, size
}

func sizeBytes(size int64) any {
	if size <= 0 {
		return nil
	}
	return size
}

// sizeText renders bytes the way release sites do (663.1 MB); nil when unknown.
func sizeText(size int64) any {
	if size <= 0 {
		return nil
	}
	value, units := float64(size), []string{"B", "KB", "MB", "GB", "TB"}
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", size)
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

func episode(title string) any {
	m := episodeRE.FindStringSubmatch(title)
	if len(m) > 1 {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return nil
}
func inferSource(s string) string {
	if strings.HasPrefix(s, "magnet:") {
		return "magnet"
	}
	if strings.Contains(strings.ToLower(s), ".torrent") {
		return "torrent-url"
	}
	return "direct"
}
func matchRule(title string, rule Obj) (bool, string) {
	low := strings.ToLower(title)
	for _, v := range list(rule["excludeKeywords"]) {
		if str(v) != "" && strings.Contains(low, strings.ToLower(str(v))) {
			return false, "命中排除关键词"
		}
	}
	for _, v := range list(rule["includeKeywords"]) {
		if str(v) != "" && !strings.Contains(low, strings.ToLower(str(v))) {
			return false, "不匹配包含关键词"
		}
	}
	if flag(rule["skipBatch"]) {
		for _, k := range []string{"合集", "batch", "bdbox", "complete series"} {
			if strings.Contains(low, k) {
				return false, "跳过合集"
			}
		}
	}
	return true, "规则匹配"
}
func defaultRule() Obj {
	return Obj{"includeKeywords": []any{}, "excludeKeywords": []any{}, "preferredResolutions": []any{}, "preferredSubgroups": []any{}, "autoDownload": true, "skipBatch": true}
}
func (a *App) newSubscription(o Obj) (Obj, error) {
	if strings.TrimSpace(str(o["name"])) == "" || !httpURL(str(o["url"])) {
		return nil, errors.New("填写名称与有效 RSS 地址")
	}
	id, e := a.Store.NextID()
	if e != nil {
		return nil, e
	}
	s := Obj{"id": id, "name": o["name"], "url": o["url"], "enabled": true, "rule": defaultRule(), "downloadDirOverride": nil, "lastFetchedAt": nil, "lastError": nil, "createdAt": now(), "updatedAt": now(), "downloadedEpisodes": []any{}, "animeId": nil, "providerId": nil}
	for _, k := range []string{"enabled", "rule", "downloadDirOverride", "animeId", "providerId"} {
		if v, yes := o[k]; yes {
			s[k] = v
		}
	}
	return s, a.Store.Put("subscriptions", str(id), s)
}
func (a *App) subscriptionRoutes() {
	a.handle("GET /api/subscriptions", func(w http.ResponseWriter, r *http.Request) error {
		ss, e := a.Store.List("subscriptions")
		if e == nil {
			items, _ := a.Store.List("feed")
			for _, s := range ss {
				eps := []any{}
				for _, f := range items {
					if str(f["subscriptionId"]) == str(s["id"]) && str(f["downloadStatus"]) == "complete" && f["episodeNumber"] != nil {
						eps = append(eps, f["episodeNumber"])
					}
				}
				s["downloadedEpisodes"] = eps
			}
			ok(w, ss)
		}
		return e
	})
	a.handle("POST /api/subscriptions", func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		s, e := a.newSubscription(o)
		if e == nil {
			ok(w, s)
		}
		return e
	})
	a.handle("PUT /api/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		id := r.PathValue("id")
		s, e := a.Store.Get("subscriptions", id)
		if e != nil {
			return e
		}
		if s == nil {
			return errors.New("订阅不存在")
		}
		if u, yes := o["url"]; yes && !httpURL(str(u)) {
			return errors.New("RSS 地址无效")
		}
		delete(o, "id")
		delete(o, "createdAt")
		o["updatedAt"] = now()
		s, e = a.Store.Patch("subscriptions", id, o)
		if e == nil {
			ok(w, s)
		}
		return e
	})
	a.handle("DELETE /api/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) error {
		e := a.Store.Delete("subscriptions", r.PathValue("id"))
		if e == nil {
			ok(w, Obj{"deleted": true})
		}
		return e
	})
	a.handle("POST /api/subscriptions/{id}/refresh", func(w http.ResponseWriter, r *http.Request) error {
		s, e := a.Store.Get("subscriptions", r.PathValue("id"))
		if e != nil {
			return e
		}
		if s == nil {
			return errors.New("订阅不存在")
		}
		result, e := a.refreshSubscription(r.Context(), s)
		if e == nil {
			ok(w, result)
		}
		return e
	})
	a.handle("GET /api/feed-items", func(w http.ResponseWriter, r *http.Request) error {
		items, e := a.Store.List("feed")
		out := []Obj{}
		sid := r.URL.Query().Get("subscriptionId")
		for _, item := range items {
			if sid != "" && str(item["subscriptionId"]) != sid {
				continue
			}
			out = append(out, item)
			if len(out) >= 2000 {
				break
			}
		}
		if e == nil {
			ok(w, out)
		}
		return e
	})
}
func (a *App) fetchFeed(ctx context.Context, endpoint string) (*gofeed.Feed, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "AnimeRSS-Go/"+Version)
	res, e := a.http.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("RSS HTTP %d", res.StatusCode)
	}
	return gofeed.NewParser().Parse(io.LimitReader(res.Body, 16<<20))
}
func feedSource(item *gofeed.Item) (string, string) {
	for _, enc := range item.Enclosures {
		if strings.HasPrefix(enc.URL, "magnet:") || httpURL(enc.URL) {
			kind := inferSource(enc.URL)
			if strings.Contains(enc.Type, "bittorrent") {
				kind = "torrent-url"
			}
			return enc.URL, kind
		}
	}
	for _, link := range item.Links {
		if strings.HasPrefix(link, "magnet:") {
			return link, "magnet"
		}
	}
	if strings.HasPrefix(item.Link, "magnet:") || strings.Contains(item.Link, ".torrent") {
		return item.Link, inferSource(item.Link)
	}
	return "", ""
}

// fetchErrorMessage turns Go's transport errors (`Get "https://…": EOF`,
// `dial tcp: lookup host: no such host`) into a short Chinese reason with the
// host, which is what the subscription list and the log show.
func fetchErrorMessage(rawURL string, e error) string {
	host := rawURL
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		host = u.Host
	}
	var dnsErr *net.DNSError
	var netErr net.Error
	msg := e.Error()
	reason := ""
	switch {
	case errors.As(e, &dnsErr) || strings.Contains(msg, "no such host"):
		reason = "域名解析失败，检查网络或 DNS"
	case errors.As(e, &netErr) && netErr.Timeout():
		reason = "连接超时"
	case errors.Is(e, io.EOF) || errors.Is(e, io.ErrUnexpectedEOF) || strings.HasSuffix(msg, ": EOF"):
		reason = "连接被对方中断"
	case strings.Contains(msg, "connection refused"):
		reason = "连接被拒绝"
	case strings.Contains(msg, "connection reset"):
		reason = "连接被重置"
	case strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:"):
		reason = "TLS 握手失败"
	default:
		return msg
	}
	return host + " " + reason
}

func (a *App) refreshSubscription(ctx context.Context, s Obj) (Obj, error) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	feed, e := a.fetchFeed(ctx, str(s["url"]))
	if e != nil {
		e = errors.New(fetchErrorMessage(str(s["url"]), e))
		_, _ = a.Store.Patch("subscriptions", str(s["id"]), Obj{"lastError": e.Error(), "lastFetchedAt": now()})
		return nil, e
	}
	added, queued := 0, 0
	for _, item := range feed.Items {
		source, kind := feedSource(item)
		identity := item.GUID
		if identity == "" {
			identity = source
		}
		if identity == "" {
			identity = item.Link + item.Title
		}
		key := hash(str(s["id"]) + ":" + identity)
		existing, _ := a.Store.Get("feed_keys", key)
		if existing != nil {
			continue
		}
		id, e := a.Store.NextID()
		if e != nil {
			return nil, e
		}
		matched, reason := matchRule(item.Title, object(s["rule"]))
		status := "pending"
		if !matched {
			status = "skipped"
		}
		var image any
		if item.Image != nil {
			image = item.Image.URL
		}
		published, _ := feedItemMeta(item)
		f := Obj{"id": id, "subscriptionId": s["id"], "title": item.Title, "imageUrl": image, "guid": identity, "link": item.Link, "publishedAt": published, "sourceType": kind, "sourceUrl": source, "infoHash": nil, "normalizedTitle": strings.ToLower(item.Title), "dedupeKey": key, "matched": matched, "matchReason": reason, "downloadStatus": status, "createdAt": now(), "episodeNumber": episode(item.Title), "isBatch": strings.Contains(strings.ToLower(item.Title), "batch"), "subgroup": subgroupOf(item.Title), "resolution": resolutionRE.FindString(item.Title), "resourceKey": hash(source)}
		if e = a.Store.Put("feed", str(id), f); e != nil {
			return nil, e
		}
		_ = a.Store.Put("feed_keys", key, Obj{"id": id})
		added++
	}
	queued = a.queuePreferred(s)
	_, e = a.Store.Patch("subscriptions", str(s["id"]), Obj{"lastError": nil, "lastFetchedAt": now()})
	return Obj{"subscription": s, "fetchedCount": len(feed.Items), "newCount": added, "matchedCount": added, "queuedCount": queued}, e
}
func (a *App) pollSubscriptions() {
	ss, e := a.Store.List("subscriptions")
	if e != nil {
		return
	}
	for _, s := range ss {
		if !flag(s["enabled"]) {
			continue
		}
		t, _ := time.Parse(time.RFC3339Nano, str(s["lastFetchedAt"]))
		interval := num(a.Settings()["pollIntervalMinutes"])
		if interval < 1 {
			interval = 30
		}
		if time.Since(t) < time.Duration(interval)*time.Minute {
			continue
		}
		if _, e := a.refreshSubscription(a.ctx, s); e != nil {
			a.Log("warn", fmt.Sprintf("订阅「%s」刷新失败：%s", str(s["name"]), e.Error()))
		}
		if a.ctx.Err() != nil {
			return
		}
	}
}

func preferenceRank(title string, preferences any) int {
	values := list(preferences)
	for i, v := range values {
		if strings.Contains(strings.ToLower(title), strings.ToLower(str(v))) {
			return i
		}
	}
	return len(values)
}

var bracketRE = regexp.MustCompile(`\[[^\]]*\]|【[^】]*】`)

func episodeGroup(f, s Obj) string {
	if f["episodeNumber"] == nil {
		return "resource:" + str(f["resourceKey"])
	}
	scope := str(s["animeId"])
	if scope == "" {
		title := str(f["title"])
		if loc := episodeRE.FindStringIndex(title); loc != nil {
			title = title[:loc[0]]
		}
		scope = "title:" + titleKey(bracketRE.ReplaceAllString(title, ""))
		if scope == "title:" {
			scope = "sub:" + str(s["id"])
		}
	}
	return scope + ":" + str(f["episodeNumber"])
}
func (a *App) queuePreferred(s Obj) int {
	if !flag(object(s["rule"])["autoDownload"]) {
		return 0
	}
	items, _ := a.Store.List("feed")
	subs, _ := a.Store.List("subscriptions")
	byID := map[string]Obj{}
	for _, sub := range subs {
		byID[str(sub["id"])] = sub
	}
	groups := map[string][]Obj{}
	for _, f := range items {
		if !flag(f["matched"]) || str(f["sourceUrl"]) == "" || str(f["downloadStatus"]) == "ignored" {
			continue
		}
		sub := byID[str(f["subscriptionId"])]
		if str(sub["id"]) != str(s["id"]) && (str(s["animeId"]) == "" || str(sub["animeId"]) != str(s["animeId"])) {
			continue
		}
		key := episodeGroup(f, sub)
		groups[key] = append(groups[key], f)
	}
	queued := 0
	rule := object(s["rule"])
	settled := func(f Obj) bool {
		return str(f["downloadStatus"]) == "queued" || str(f["downloadStatus"]) == "complete"
	}
	for _, group := range groups {
		sort.SliceStable(group, func(i, j int) bool {
			x, y := group[i], group[j]
			if settled(x) != settled(y) {
				return settled(x)
			}
			for _, k := range []string{"preferredSubgroups", "preferredResolutions"} {
				a, b := preferenceRank(str(x["title"]), rule[k]), preferenceRank(str(y["title"]), rule[k])
				if a != b {
					return a < b
				}
			}
			return num(x["id"]) < num(y["id"])
		})
		winner := group[0]
		for _, f := range group[1:] {
			if !settled(f) {
				_, _ = a.Store.Patch("feed", str(f["id"]), Obj{"downloadStatus": "skipped", "matchReason": "同集已选用：" + str(winner["title"])})
			}
		}
		if !settled(winner) && str(winner["subscriptionId"]) == str(s["id"]) && str(winner["downloadStatus"]) != "error" {
			if _, e := a.enqueue(Obj{"feedItemId": winner["id"]}); e == nil {
				queued++
			} else {
				a.Log("warn", e.Error())
			}
		}
	}
	return queued
}
