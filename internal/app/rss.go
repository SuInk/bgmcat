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

var (
	techDimensionsRE  = regexp.MustCompile(`(?i)\b\d{3,4}\s*[x×]\s*(\d{3,4})\b`)
	techCodecRE       = regexp.MustCompile(`(?i)\b[xh]\.?26[45]\b`)
	techBitsRE        = regexp.MustCompile(`(?i)\b\d{1,2}\s*bits?\b`)
	techFpsRE         = regexp.MustCompile(`(?i)\b\d{2,3}\s*fps\b`)
	techResTagRE      = regexp.MustCompile(`(?i)\b(2160|1440|1080|720|576|480)\s*[pi]\b`)
	tech4KRE          = regexp.MustCompile(`(?i)\b4k\b`)
	techDateRE        = regexp.MustCompile(`\b(?:19|20)\d{2}[.\-/年]\s*\d{1,2}(?:[.\-/月]\s*\d{1,2}日?)?\b`)
	techMultiDotRE    = regexp.MustCompile(`\b\d{1,4}(?:\.\d{1,4})+\b`)
	techCnSeasonRE    = regexp.MustCompile(`第\s*[一二三四五六七八九十\d]{1,2}\s*[季期部]`)
	techEnSeasonRE    = regexp.MustCompile(`(?i)\b(?:Season|Part|Cour)\s*0?\d{1,2}\b`)
	techOrdSeasonRE   = regexp.MustCompile(`(?i)\b\d{1,2}(?:st|nd|rd|th)\s+Season\b`)
	techStandaloneSRE = regexp.MustCompile(`(?i)\bS0?\d{1,2}\b`)
	episodePatterns   = []struct {
		re           *regexp.Regexp
		checkIgnored bool
		stripSeasonS bool
	}{
		{re: regexp.MustCompile(`(?i)\bS\d{1,2}\s*E0*(\d{1,4})\b`)},
		{re: regexp.MustCompile(`(?i)\bS\d{1,2}\s*[-–—]\s*0*(\d{1,4})(?:\s*[vV]\d+)?\b`)},
		{re: regexp.MustCompile(`第\s*0*(\d{1,4})\s*(?:話|话|集)`), stripSeasonS: true},
		{re: regexp.MustCompile(`(?i)\b(?:EP?|Episode)\s*0*(\d{1,4})\b`), stripSeasonS: true},
		{re: regexp.MustCompile(`(?i)[\[【]\s*0*(\d{1,4})(?:\s*[vV]\d+)?(?:\s*END)?\s*[\]】]`), checkIgnored: true, stripSeasonS: true},
		{re: regexp.MustCompile(`(?i)[-–—]\s*0*(\d{1,4})(?:\s*[vV]\d+)?(?:\s*END)?(?:$|[\s\[({\x{3010}\x{ff08}])`), stripSeasonS: true},
		{re: regexp.MustCompile(`(?:^|[\s._])0*(\d{1,3})(?:[vV]\d+)?(?:$|[\s._\]\)）】])`), checkIgnored: true, stripSeasonS: true},
	}
	episodeRE = regexp.MustCompile(`(?i)(?:\s[-–—]\s|[\[【]|第|EP?\s*|\bS\d{1,2}\s*E)(\d{1,4})(?:[\]】话話集\s.vV]|$)`)
	batchREs  = []*regexp.Regexp{
		regexp.MustCompile(`合集`),
		regexp.MustCompile(`全\s*\d{1,4}\s*[话話集]`),
		regexp.MustCompile(`全集`),
		regexp.MustCompile(`(?i)\bBD-?BOX\b`),
		regexp.MustCompile(`(?i)\bBatch\b`),
		regexp.MustCompile(`(?i)\bComplete(?:\s+Series)?\b`),
		regexp.MustCompile(`(?i)\b(?:Season|S)\s*0?\d{1,2}\s*(?:Complete|Full)\b`),
		regexp.MustCompile(`(?i)\bFull\s+Season\b`),
	}
	batchRangeREs = []*regexp.Regexp{
		regexp.MustCompile(`(?i)[\[【(]\s*0*\d{1,4}\s*[-–—~～]\s*0*\d{1,4}\s*(?:END|完)?\s*[\]】)]`),
		regexp.MustCompile(`第\s*0*\d{1,4}\s*[-–—~～]\s*0*\d{1,4}\s*[话話集]`),
		regexp.MustCompile(`(?i)\b(?:EP?|Episodes?)\s*0*\d{1,4}\s*[-–—~～]\s*(?:(?:EP?|Episodes?)\s*)?0*\d{1,4}\b`),
	}
	specialExtraREs = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:^|[\s\[【(])(?:NCOP|NCED|PV|CM|MENU|OVA|OAD|OAV)\s*\d{0,2}(?:$|[\s\]】)._])`),
		regexp.MustCompile(`(?i)[\[【(]\s*SP\s*\d{0,2}\s*[\]】)]`),
		regexp.MustCompile(`(?:映像特典|特典映像|无字OP|无字ED|先行PV|正式PV|预告篇|总集篇)`),
	}
	releaseVersionRE = regexp.MustCompile(`(?i)(?:\b\d{1,4}\s*|[\[【(\s])[vV]([2-9])\b`)
	chsLangRE        = regexp.MustCompile(`(?i)(?:简日|简体|简中|简繁|多国字幕|多语|\bchs\b|\bgb\b)`)
	chtLangRE        = regexp.MustCompile(`(?i)(?:繁日|繁体|繁中|简繁|\bcht\b|\bbig5\b)`)
	resolutionRE     = regexp.MustCompile(`(?i)\b(2160p|1440p|1080p|720p|576p|480p|4k)\b`)
)

// subgroupLeadingBannerRE strips decorative season/date banners that precede the fansub bracket,
// e.g. ★10月新番★【喵萌奶茶屋】, [10月新番][樱都字幕组], [2026.10][北宇治字幕组].
var subgroupLeadingBannerRE = regexp.MustCompile(`(?i)^\s*(?:[★☆][^★☆]{1,24}[★☆]\s*|(?:[\[【]\s*(?:(?:\d{2,4}\s*[年./-]\s*)?\d{1,2}\s*月?(?:新番)?|新番|连载|周[一二三四五六日天]|(?:19|20)\d{2}(?:[./-]\d{1,2})?)\s*[\]】]\s*))+`)

// subgroupRE grabs the fansub tag that release names lead with: [LoliHouse] …, 【喵萌奶茶屋】★10月新番★ ….
var subgroupRE = regexp.MustCompile(`^\s*[\[【]([^\]】]{1,48})[\]】]`)

var nonSubgroupTokenRE = regexp.MustCompile(`(?i)^(?:\d{1,4}|\d{3,4}[pi]|4k|8k|\d{3,4}\s*[x×]\s*\d{3,4}|x?26[45]|hevc|avc|av1|aac|flac|opus|mp4|mkv|web-?dl|web-?rip|bd-?rip|bluray|hdtv|tvrip|\d{1,2}\s*bits?|\d{2,3}\s*fps|(?:chs|cht|gb|big5|jpn|简体|繁体|简中|繁中|简繁|简日|繁日|双语|多语|内嵌|内封|外挂|软字|硬字|中文|字幕|无修|修正|合集|全集|完结|[\s_/+&-])+|[vV]\d+)$`)

// subgroupOf returns the leading bracketed group, or nil when the title opens
// with something that is clearly not a group name (a resolution, language, or episode tag).
func subgroupOf(title string) any {
	cleaned := subgroupLeadingBannerRE.ReplaceAllString(title, "")
	m := subgroupRE.FindStringSubmatch(cleaned)
	if len(m) < 2 {
		return nil
	}
	name := strings.TrimSpace(m[1])
	if name == "" || strings.Trim(name, "0123456789") == "" {
		return nil
	}
	if strings.ContainsAny(name, "|｜") {
		parts := strings.FieldsFunc(name, func(r rune) bool { return r == '|' || r == '｜' })
		if len(parts) >= 2 {
			tail := strings.TrimSpace(parts[len(parts)-1])
			if nonSubgroupTokenRE.MatchString(tail) || resolutionRE.MatchString(tail) {
				name = strings.TrimSpace(parts[0])
			}
		}
	}
	if name == "" || nonSubgroupTokenRE.MatchString(name) || resolutionRE.MatchString(name) {
		return nil
	}
	return name
}

var (
	torrentPubDateRE = regexp.MustCompile(`<pubDate>\s*([^<]+?)\s*</pubDate>`)
	torrentLengthRE  = regexp.MustCompile(`<contentLength>\s*(\d+)\s*</contentLength>`)
	descSizeRE       = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?)\s*(B|KB|KiB|MB|MiB|GB|GiB|TB|TiB)\b`)
	uploadbtHashRE   = regexp.MustCompile(`(?i)[?&]hash=([0-9a-f]{40})\b`)
	descMagnetRE     = regexp.MustCompile(`(?i)(magnet:\?xt=urn:btih:[0-9a-zA-Z&=:%._+-]+)`)
	mikanTimeZone    = time.FixedZone("CST", 8*3600)
)

func parseHumanSize(raw string) int64 {
	m := descSizeRE.FindStringSubmatch(raw)
	if len(m) < 3 {
		return 0
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil || val <= 0 {
		return 0
	}
	switch strings.ToUpper(m[2]) {
	case "B":
		return int64(val)
	case "KB", "KIB":
		return int64(val * 1024)
	case "MB", "MIB":
		return int64(val * 1024 * 1024)
	case "GB", "GIB":
		return int64(val * 1024 * 1024 * 1024)
	case "TB", "TIB":
		return int64(val * 1024 * 1024 * 1024 * 1024)
	}
	return 0
}

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
		if size > 1 {
			break
		}
		if parsed, _ := strconv.ParseInt(enclosure.Length, 10, 64); parsed > 1 {
			size = parsed
		}
	}
	if size <= 1 && item.Description != "" {
		size = parseHumanSize(item.Description)
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

var ignoredEpisodeNums = map[int]bool{
	264: true, 265: true, 480: true, 576: true, 720: true, 1080: true, 1440: true, 2160: true,
}

func stripTechnicalNumbers(raw string) string {
	s := techDimensionsRE.ReplaceAllString(raw, " ")
	s = techCodecRE.ReplaceAllString(s, " ")
	s = techBitsRE.ReplaceAllString(s, " ")
	s = techFpsRE.ReplaceAllString(s, " ")
	s = techResTagRE.ReplaceAllString(s, " ")
	s = techDateRE.ReplaceAllString(s, " ")
	s = techMultiDotRE.ReplaceAllString(s, " ")
	s = techCnSeasonRE.ReplaceAllString(s, " ")
	s = techEnSeasonRE.ReplaceAllString(s, " ")
	s = techOrdSeasonRE.ReplaceAllString(s, " ")
	return s
}

func isBatchRelease(title string) bool {
	for _, re := range batchREs {
		if re.MatchString(title) {
			return true
		}
	}
	stripped := stripTechnicalNumbers(title)
	for _, re := range batchRangeREs {
		if re.MatchString(stripped) {
			return true
		}
	}
	return false
}

func isSpecialOrExtraRelease(title string) bool {
	for _, re := range specialExtraREs {
		if re.MatchString(title) {
			return true
		}
	}
	return false
}

func normalizeResolution(title string) string {
	if m := techDimensionsRE.FindStringSubmatch(title); len(m) > 1 {
		switch m[1] {
		case "2160", "1440", "1080", "720", "576", "480":
			return m[1] + "p"
		}
	}
	if m := techResTagRE.FindStringSubmatch(title); len(m) > 1 {
		return m[1] + "p"
	}
	if tech4KRE.MatchString(title) {
		return "2160p"
	}
	return ""
}

func torrentReleaseVersion(title string) int {
	if m := releaseVersionRE.FindStringSubmatch(title); len(m) > 1 {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 2 {
			return n
		}
	}
	return 1
}

func classifySubtitleLanguage(title string) (hasCHS, hasCHT bool) {
	return chsLangRE.MatchString(title), chtLangRE.MatchString(title)
}

func episode(title string) any {
	if isBatchRelease(title) || isSpecialOrExtraRelease(title) {
		return nil
	}
	cleaned := stripTechnicalNumbers(title)
	cleanedNoS := techStandaloneSRE.ReplaceAllString(cleaned, " ")
	for _, pat := range episodePatterns {
		target := cleaned
		if pat.stripSeasonS {
			target = cleanedNoS
		}
		for _, m := range pat.re.FindAllStringSubmatch(target, -1) {
			if len(m) <= 1 {
				continue
			}
			n, err := strconv.Atoi(m[1])
			if err != nil || n <= 0 || n > 4999 {
				continue
			}
			if pat.checkIgnored && (ignoredEpisodeNums[n] || (n >= 1980 && n <= 2039)) {
				continue
			}
			return n
		}
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

func includeKeywordsWantSpecial(rule Obj) bool {
	for _, v := range list(rule["includeKeywords"]) {
		low := strings.ToLower(strings.TrimSpace(str(v)))
		for _, sp := range []string{"pv", "ncop", "nced", "sp", "ova", "oad", "特典"} {
			if low != "" && strings.Contains(low, sp) {
				return true
			}
		}
	}
	return false
}

func matchRuleItem(title string, subName any, ep any, publishedAt string, isInitialSync bool, rule Obj) (bool, string) {
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
		if isBatchRelease(title) {
			return false, "跳过合集"
		}
		if isSpecialOrExtraRelease(title) && !includeKeywordsWantSpecial(rule) {
			return false, "跳过特典或预告"
		}
	}
	if locked := strings.TrimSpace(str(rule["lockedSubgroup"])); locked != "" && locked != "all" {
		lockedLow := strings.ToLower(locked)
		subLow := strings.ToLower(str(subName))
		if !strings.Contains(subLow, lockedLow) && !strings.Contains(low, lockedLow) {
			return false, "非指定字幕组"
		}
	}
	langPref := strings.ToLower(strings.TrimSpace(str(rule["preferredLanguage"])))
	if langPref == "chs" || langPref == "cht" {
		hasCHS, hasCHT := classifySubtitleLanguage(title)
		if langPref == "chs" && hasCHT && !hasCHS {
			return false, "跳过纯繁中版本"
		}
		if langPref == "cht" && hasCHS && !hasCHT {
			return false, "跳过纯简中版本"
		}
	}
	if flag(rule["onlyNew"]) {
		baseEp := num(rule["baselineEpisode"])
		if baseEp > 0 && ep != nil && num(ep) <= baseEp {
			return false, "订阅前已出集数（仅追新集）"
		}
		if subAtRaw := str(rule["subscribedAt"]); subAtRaw != "" {
			if subAt, err := time.Parse(time.RFC3339Nano, subAtRaw); err == nil {
				if publishedAt != "" {
					if pubTime, pErr := time.Parse(time.RFC3339Nano, publishedAt); pErr == nil {
						if pubTime.Before(subAt.Add(-2 * time.Minute)) {
							return false, "订阅前旧资源（仅追新集）"
						}
					}
				} else if isInitialSync && time.Since(subAt) < 15*time.Minute {
					return false, "订阅前旧资源（仅追新集）"
				}
			}
		}
	}
	return true, "规则匹配"
}

func matchRule(title string, rule Obj) (bool, string) {
	return matchRuleItem(title, subgroupOf(title), episode(title), "", false, rule)
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
			subByID := make(map[string]Obj, len(ss))
			for _, s := range ss {
				subByID[str(s["id"])] = s
			}
			epsBySub := make(map[string]map[int]bool, len(ss))
			epsByAnime := make(map[string]map[int]bool)
			for _, f := range items {
				if str(f["downloadStatus"]) == "complete" && f["episodeNumber"] != nil {
					epNum := int(num(f["episodeNumber"]))
					if epNum <= 0 {
						continue
					}
					sid := str(f["subscriptionId"])
					if epsBySub[sid] == nil {
						epsBySub[sid] = map[int]bool{}
					}
					epsBySub[sid][epNum] = true
					if sub := subByID[sid]; sub != nil && str(sub["animeId"]) != "" {
						aid := str(sub["animeId"])
						if epsByAnime[aid] == nil {
							epsByAnime[aid] = map[int]bool{}
						}
						epsByAnime[aid][epNum] = true
					}
				}
			}
			for _, s := range ss {
				set := epsBySub[str(s["id"])]
				if aid := str(s["animeId"]); aid != "" && epsByAnime[aid] != nil {
					set = epsByAnime[aid]
				}
				sorted := make([]int, 0, len(set))
				for epNum := range set {
					sorted = append(sorted, epNum)
				}
				sort.Ints(sorted)
				eps := make([]any, len(sorted))
				for i, epNum := range sorted {
					eps[i] = epNum
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
			if _, hasRule := o["rule"]; hasRule {
				a.reapplySubscriptionRule(id, object(s["rule"]))
			}
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
		sid := r.URL.Query().Get("subscriptionId")
		if sid == "" {
			out, e := a.Store.ListLimit("feed", 2000)
			if e == nil {
				ok(w, out)
			}
			return e
		}
		items, e := a.Store.List("feed")
		out := []Obj{}
		for _, item := range items {
			if str(item["subscriptionId"]) != sid {
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
	req.Header.Set("User-Agent", "bgmcat/"+Version)
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
			if m := uploadbtHashRE.FindStringSubmatch(enc.URL); len(m) > 1 {
				return "magnet:?xt=urn:btih:" + strings.ToLower(m[1]), "magnet"
			}
			kind := inferSource(enc.URL)
			if kind != "magnet" && strings.Contains(enc.Type, "bittorrent") {
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
	if m := descMagnetRE.FindStringSubmatch(item.Description); len(m) > 1 {
		return strings.ReplaceAll(m[1], "&amp;", "&"), "magnet"
	}
	return "", ""
}

func extractFeedItemInfoHash(item *gofeed.Item, primarySource string) string {
	if item == nil {
		return extractSourceInfoHash(primarySource)
	}
	if ih := extractSourceInfoHash(primarySource, item.Link, item.GUID); ih != "" {
		return ih
	}
	if item.Extensions != nil {
		if nyaaExt, ok := item.Extensions["nyaa"]; ok {
			for _, ext := range nyaaExt["infoHash"] {
				val := strings.TrimSpace(ext.Value)
				if plainHex40RE.MatchString(val) {
					return strings.ToLower(val)
				}
			}
		}
	}
	return ""
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

func (a *App) bumpAnimeBaselineEpisode(animeID string, ep int) {
	if animeID == "" || ep <= 0 {
		return
	}
	subs, err := a.Store.List("subscriptions")
	if err != nil {
		return
	}
	for _, sub := range subs {
		if str(sub["animeId"]) != animeID {
			continue
		}
		rule := clone(object(sub["rule"]))
		if !flag(rule["onlyNew"]) || int(num(rule["baselineEpisode"])) >= ep {
			continue
		}
		rule["baselineEpisode"] = ep
		_, _ = a.Store.Patch("subscriptions", str(sub["id"]), Obj{"rule": rule, "updatedAt": now()})
	}
}

func (a *App) fetchSubscriptionItems(ctx context.Context, s Obj) (fetchedCount, addedCount, matchedCount int, err error) {
	if fresh, gErr := a.Store.Get("subscriptions", str(s["id"])); gErr == nil && fresh != nil {
		s = fresh
	}
	isInitialSync := str(s["lastFetchedAt"]) == ""
	feed, e := a.fetchFeed(ctx, str(s["url"]))
	if e != nil {
		e = errors.New(fetchErrorMessage(str(s["url"]), e))
		_, _ = a.Store.Patch("subscriptions", str(s["id"]), Obj{"lastError": e.Error(), "lastFetchedAt": now()})
		return 0, 0, 0, e
	}
	rule := object(s["rule"])
	maxPreExistingEp := int(num(rule["baselineEpisode"]))
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
			return len(feed.Items), addedCount, matchedCount, e
		}
		published, _ := feedItemMeta(item)
		ep := episode(item.Title)
		subName := subgroupOf(item.Title)
		resNorm := normalizeResolution(item.Title)
		var resVal any
		if resNorm != "" {
			resVal = resNorm
		} else if rawRes := resolutionRE.FindString(item.Title); rawRes != "" {
			resVal = strings.ToLower(rawRes)
		}
		matched, reason := matchRuleItem(item.Title, subName, ep, published, isInitialSync, rule)
		if !matched && strings.HasPrefix(reason, "订阅前") && ep != nil {
			if epInt := int(num(ep)); epInt > maxPreExistingEp {
				maxPreExistingEp = epInt
			}
		}
		status := "pending"
		if !matched {
			status = "skipped"
		} else {
			matchedCount++
		}
		var image any
		if item.Image != nil {
			image = item.Image.URL
		}
		ih := extractFeedItemInfoHash(item, source)
		var infoHashVal any
		resourceKey := hash(source)
		if ih != "" {
			infoHashVal = ih
			resourceKey = ih
		}
		f := Obj{"id": id, "subscriptionId": s["id"], "title": item.Title, "imageUrl": image, "guid": identity, "link": item.Link, "publishedAt": published, "sourceType": kind, "sourceUrl": source, "infoHash": infoHashVal, "normalizedTitle": strings.ToLower(item.Title), "dedupeKey": key, "matched": matched, "matchReason": reason, "downloadStatus": status, "createdAt": now(), "episodeNumber": ep, "isBatch": isBatchRelease(item.Title), "subgroup": subName, "resolution": resVal, "resourceKey": resourceKey}
		if kind == "torrent-url" && ih != "" {
			if mag := buildMagnetFromInfoHash(ih, item.Title); mag != "" {
				f["fallbackUrl"] = mag
			}
		}
		if e = a.Store.Put("feed", str(id), f); e != nil {
			return len(feed.Items), addedCount, matchedCount, e
		}
		_ = a.Store.Put("feed_keys", key, Obj{"id": id})
		addedCount++
	}
	if flag(rule["onlyNew"]) && maxPreExistingEp > int(num(rule["baselineEpisode"])) {
		if aid := str(s["animeId"]); aid != "" {
			a.bumpAnimeBaselineEpisode(aid, maxPreExistingEp)
		} else {
			nextRule := clone(rule)
			nextRule["baselineEpisode"] = maxPreExistingEp
			_, _ = a.Store.Patch("subscriptions", str(s["id"]), Obj{"rule": nextRule})
		}
	}
	_, e = a.Store.Patch("subscriptions", str(s["id"]), Obj{"lastError": nil, "lastFetchedAt": now()})
	return len(feed.Items), addedCount, matchedCount, e
}

func (a *App) refreshSubscription(ctx context.Context, s Obj) (Obj, error) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	fetched, added, matched, e := a.fetchSubscriptionItems(ctx, s)
	if e != nil {
		return nil, e
	}
	if fresh, gErr := a.Store.Get("subscriptions", str(s["id"])); gErr == nil && fresh != nil {
		s = fresh
	}
	queued := a.queuePreferred(s)
	return Obj{"subscription": s, "fetchedCount": fetched, "newCount": added, "createdCount": added, "matchedCount": matched, "queuedCount": queued}, nil
}

func (a *App) syncAnimeSubscriptions(ctx context.Context, animeID string) {
	if animeID == "" {
		return
	}
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	ss, err := a.Store.List("subscriptions")
	if err != nil {
		return
	}
	var siblings []Obj
	for _, s := range ss {
		if str(s["animeId"]) == animeID && flag(s["enabled"]) {
			siblings = append(siblings, s)
		}
	}
	if len(siblings) == 0 {
		return
	}
	for _, s := range siblings {
		if ctx.Err() != nil {
			return
		}
		fetchCtx, cancel := context.WithTimeout(ctx, 18*time.Second)
		_, _, _, err := a.fetchSubscriptionItems(fetchCtx, s)
		cancel()
		if err != nil {
			a.Log("warn", fmt.Sprintf("订阅「%s」同步失败：%s", str(s["name"]), err.Error()))
		}
	}
	if fresh, gErr := a.Store.Get("subscriptions", str(siblings[0]["id"])); gErr == nil && fresh != nil {
		a.queuePreferred(fresh)
	} else {
		a.queuePreferred(siblings[0])
	}
}

func (a *App) pollSubscriptions() {
	ss, e := a.Store.List("subscriptions")
	if e != nil {
		return
	}
	interval := num(a.Settings()["pollIntervalMinutes"])
	if interval < 1 {
		interval = 30
	}
	dueStandalone := []Obj{}
	dueByAnime := map[string][]Obj{}
	animeOrder := []string{}
	for _, s := range ss {
		if !flag(s["enabled"]) {
			continue
		}
		t, _ := time.Parse(time.RFC3339Nano, str(s["lastFetchedAt"]))
		if time.Since(t) < time.Duration(interval)*time.Minute {
			continue
		}
		if aid := str(s["animeId"]); aid != "" {
			if _, seen := dueByAnime[aid]; !seen {
				animeOrder = append(animeOrder, aid)
			}
			dueByAnime[aid] = append(dueByAnime[aid], s)
		} else {
			dueStandalone = append(dueStandalone, s)
		}
	}
	for _, aid := range animeOrder {
		if a.ctx.Err() != nil {
			return
		}
		a.refreshMu.Lock()
		group := dueByAnime[aid]
		for _, s := range group {
			if a.ctx.Err() != nil {
				break
			}
			fetchCtx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
			_, _, _, err := a.fetchSubscriptionItems(fetchCtx, s)
			cancel()
			if err != nil {
				a.Log("warn", fmt.Sprintf("订阅「%s」刷新失败：%s", str(s["name"]), err.Error()))
			}
		}
		if len(group) > 0 && a.ctx.Err() == nil {
			if fresh, gErr := a.Store.Get("subscriptions", str(group[0]["id"])); gErr == nil && fresh != nil {
				a.queuePreferred(fresh)
			} else {
				a.queuePreferred(group[0])
			}
		}
		a.refreshMu.Unlock()
	}
	for _, s := range dueStandalone {
		if a.ctx.Err() != nil {
			return
		}
		if _, e := a.refreshSubscription(a.ctx, s); e != nil {
			a.Log("warn", fmt.Sprintf("订阅「%s」刷新失败：%s", str(s["name"]), e.Error()))
		}
	}
}

func preferenceRank(title, extra string, preferences any) int {
	values := list(preferences)
	lowTitle := strings.ToLower(title)
	lowExtra := strings.ToLower(extra)
	for i, v := range values {
		needle := strings.ToLower(strings.TrimSpace(str(v)))
		if needle == "" {
			continue
		}
		if (lowExtra != "" && strings.Contains(lowExtra, needle)) || strings.Contains(lowTitle, needle) {
			return i
		}
	}
	return len(values)
}

func languagePreferenceRank(title, langPref string) int {
	hasCHS, hasCHT := classifySubtitleLanguage(title)
	switch strings.ToLower(strings.TrimSpace(langPref)) {
	case "chs":
		if hasCHS {
			return 0
		}
		if !hasCHT {
			return 1
		}
		return 2
	case "cht":
		if hasCHT {
			return 0
		}
		if !hasCHS {
			return 1
		}
		return 2
	default:
		if hasCHS {
			return 0
		}
		return 1
	}
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
	rule := object(s["rule"])
	if !flag(rule["autoDownload"]) {
		return 0
	}
	items, _ := a.Store.List("feed")
	subs, _ := a.Store.List("subscriptions")
	byID := map[string]Obj{}
	for _, sub := range subs {
		byID[str(sub["id"])] = sub
	}
	animeID := str(s["animeId"])
	settled := func(f Obj) bool {
		st := str(f["downloadStatus"])
		return st == "queued" || st == "complete"
	}

	// Track episodes already present in downloads (including manual one-click downloads)
	// and infer established subgroup for automatic series consistency.
	alreadyDownloadedGroup := map[string]string{}
	subgroupFreq := map[string]int{}
	if existingJobs, err := a.Store.List("downloads"); err == nil {
		for _, job := range existingJobs {
			if str(job["status"]) == "removed" {
				continue
			}
			jobAnimeID := str(job["animeId"])
			if jobAnimeID == "" && job["feedItemSubscriptionId"] != nil {
				if jobSub := byID[str(job["feedItemSubscriptionId"])]; jobSub != nil {
					jobAnimeID = str(jobSub["animeId"])
				}
			}
			if animeID == "" || jobAnimeID != animeID {
				continue
			}
			jobTitle := firstNonEmpty(str(job["feedItemTitle"]), str(job["title"]))
			if sg := str(subgroupOf(jobTitle)); sg != "" {
				subgroupFreq[strings.ToLower(sg)]++
			}
			if ep := episode(jobTitle); ep != nil {
				alreadyDownloadedGroup[fmt.Sprintf("%s:%v", animeID, ep)] = jobTitle
			}
		}
	}

	groups := map[string][]Obj{}
	for _, f := range items {
		if !flag(f["matched"]) || str(f["sourceUrl"]) == "" || str(f["downloadStatus"]) == "ignored" {
			continue
		}
		sub := byID[str(f["subscriptionId"])]
		if sub == nil {
			continue
		}
		isSelf := str(sub["id"]) == str(s["id"])
		isSameAnime := animeID != "" && str(sub["animeId"]) == animeID
		if !isSelf && !isSameAnime {
			continue
		}
		if !isSelf && !settled(f) && (!flag(sub["enabled"]) || !flag(object(sub["rule"])["autoDownload"])) {
			continue
		}
		if settled(f) {
			if sg := strings.ToLower(strings.TrimSpace(str(f["subgroup"]))); sg != "" {
				subgroupFreq[sg]++
			}
		}
		// Never blindly auto-download unnumbered releases (OSTs, packs, notices) on anime subscriptions.
		if animeID != "" && f["episodeNumber"] == nil {
			if !settled(f) && str(f["downloadStatus"]) == "pending" {
				_, _ = a.Store.Patch("feed", str(f["id"]), Obj{"downloadStatus": "skipped", "matchReason": "未识别单集集号，需手动下载"})
			}
			continue
		}
		key := episodeGroup(f, sub)
		groups[key] = append(groups[key], f)
	}

	establishedSubgroup := ""
	bestFreq := 0
	for sg, cnt := range subgroupFreq {
		if cnt > bestFreq {
			bestFreq = cnt
			establishedSubgroup = sg
		}
	}

	providerRank := map[string]int{}
	for idx, pid := range list(a.Settings()["resourceProviderOrder"]) {
		providerRank[str(pid)] = idx
	}
	resPrefs := rule["preferredResolutions"]
	if animeID != "" && len(list(resPrefs)) == 0 {
		resPrefs = []any{"1080p"}
	}
	langPref := str(rule["preferredLanguage"])

	queued := 0
	groupKeys := make([]string, 0, len(groups))
	for k := range groups {
		groupKeys = append(groupKeys, k)
	}
	sort.SliceStable(groupKeys, func(i, j int) bool {
		parseKey := func(k string) (scope string, hasEp bool, ep int) {
			idx := strings.LastIndex(k, ":")
			if idx < 0 {
				return k, false, 0
			}
			n, err := strconv.Atoi(k[idx+1:])
			if err != nil {
				return k[:idx], false, 0
			}
			return k[:idx], true, n
		}
		sa, ha, ea := parseKey(groupKeys[i])
		sb, hb, eb := parseKey(groupKeys[j])
		if sa != sb {
			return sa < sb
		}
		if ha != hb {
			return ha
		}
		if ha && ea != eb {
			return ea < eb
		}
		return groupKeys[i] < groupKeys[j]
	})

	for _, key := range groupKeys {
		group := groups[key]
		if manualTitle, exists := alreadyDownloadedGroup[key]; exists {
			hasSettledFeed := false
			for _, f := range group {
				if settled(f) {
					hasSettledFeed = true
					break
				}
			}
			if !hasSettledFeed {
				for _, f := range group {
					if str(f["downloadStatus"]) == "pending" {
						_, _ = a.Store.Patch("feed", str(f["id"]), Obj{"downloadStatus": "skipped", "matchReason": "同集已在下载队列：" + manualTitle})
					}
				}
				continue
			}
		}
		sort.SliceStable(group, func(i, j int) bool {
			x, y := group[i], group[j]
			if settled(x) != settled(y) {
				return settled(x)
			}
			if ra, rb := preferenceRank(str(x["title"]), str(x["subgroup"]), rule["preferredSubgroups"]), preferenceRank(str(y["title"]), str(y["subgroup"]), rule["preferredSubgroups"]); ra != rb {
				return ra < rb
			}
			if len(list(rule["preferredSubgroups"])) == 0 && establishedSubgroup != "" {
				xa := strings.ToLower(strings.TrimSpace(str(x["subgroup"]))) == establishedSubgroup
				ya := strings.ToLower(strings.TrimSpace(str(y["subgroup"]))) == establishedSubgroup
				if xa != ya {
					return xa
				}
			}
			if ra, rb := preferenceRank(str(x["title"]), str(x["resolution"]), resPrefs), preferenceRank(str(y["title"]), str(y["resolution"]), resPrefs); ra != rb {
				return ra < rb
			}
			if la, lb := languagePreferenceRank(str(x["title"]), langPref), languagePreferenceRank(str(y["title"]), langPref); la != lb {
				return la < lb
			}
			if va, vb := torrentReleaseVersion(str(x["title"])), torrentReleaseVersion(str(y["title"])); va != vb {
				return va > vb
			}
			subX, subY := byID[str(x["subscriptionId"])], byID[str(y["subscriptionId"])]
			px, okX := providerRank[str(subX["providerId"])]
			if !okX {
				px = 999
			}
			py, okY := providerRank[str(subY["providerId"])]
			if !okY {
				py = 999
			}
			if px != py {
				return px < py
			}
			return num(x["id"]) < num(y["id"])
		})
		winner := group[0]
		for _, f := range group[1:] {
			if !settled(f) {
				_, _ = a.Store.Patch("feed", str(f["id"]), Obj{"downloadStatus": "skipped", "matchReason": "同集已选用：" + str(winner["title"])})
			}
		}
		canEnqueue := str(winner["subscriptionId"]) == str(s["id"]) || animeID != ""
		if !settled(winner) && canEnqueue && str(winner["downloadStatus"]) != "error" {
			if _, e := a.enqueue(Obj{"feedItemId": winner["id"]}); e == nil {
				queued++
				if establishedSubgroup == "" {
					if sg := strings.ToLower(strings.TrimSpace(str(winner["subgroup"]))); sg != "" {
						establishedSubgroup = sg
					}
				}
			} else {
				a.Log("warn", e.Error())
			}
		}
	}
	return queued
}
