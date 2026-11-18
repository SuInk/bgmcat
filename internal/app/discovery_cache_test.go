package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

func TestDiscoveryCacheCoalescesAndIsolatesResults(t *testing.T) {
	a := testApp(t)
	var calls atomic.Int32
	fetch := func(context.Context) (Obj, error) {
		calls.Add(1)
		time.Sleep(40 * time.Millisecond)
		return Obj{"items": []Obj{{"title": "original"}}}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := a.discoveryCached(context.Background(), "test", time.Hour, 24*time.Hour, fetch)
			if e != nil {
				t.Error(e)
				return
			}
			object(list(r["items"])[0])["title"] = "changed"
		}()
	}
	wg.Wait()
	r, e := a.discoveryCached(context.Background(), "test", time.Hour, 24*time.Hour, fetch)
	if e != nil || calls.Load() != 1 || str(object(list(r["items"])[0])["title"]) != "original" {
		t.Fatalf("cache isolation/coalescing failed: %v calls=%d", e, calls.Load())
	}
}
func TestDiscoveryStaleFallbackAndExpiry(t *testing.T) {
	a := testApp(t)
	var calls atomic.Int32
	fetch := func(context.Context) (Obj, error) { calls.Add(1); return nil, errors.New("offline") }
	a.Store.Put("discovery-cache", "test", Obj{"value": Obj{"items": []string{"saved"}}, "savedAt": time.Now().Add(-2 * time.Hour).Unix()})
	r, e := a.discoveryCached(context.Background(), "test", time.Hour, 24*time.Hour, fetch)
	if e != nil || !flag(r["cacheStale"]) || len(list(r["items"])) != 1 {
		t.Fatal("stale fallback missing", r, e)
	}
	_, e = a.discoveryCached(context.Background(), "test", time.Hour, 24*time.Hour, fetch)
	if e != nil || calls.Load() != 1 {
		t.Fatal("stale retry cooldown not respected")
	}
	a.Store.Put("discovery-cache", "test", Obj{"value": Obj{}, "savedAt": time.Now().Add(-25 * time.Hour).Unix()})
	if _, e = a.discoveryCached(context.Background(), "test", time.Hour, 24*time.Hour, fetch); e == nil {
		t.Fatal("expired cache used")
	}
}
func TestDetailParallelSourcesAndWarmCache(t *testing.T) {
	a := testApp(t)
	a.Store.Patch("config", "settings", Obj{"enabledResourceProviders": []string{"mikan", "nyaa", "acgrip"}})
	var calls atomic.Int32
	var active, maxActive atomic.Int32
	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := maxActive.Load(); n > old; old = maxActive.Load() {
			if maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("no deadline")
		}
		time.Sleep(80 * time.Millisecond)
		body := `<rss version="2.0"><channel><title>fixture</title></channel></rss>`
		if strings.Contains(r.URL.Path, "subjects/") {
			body = `{"id":1,"name":"Fixture","images":{}}`
		}
		if strings.Contains(r.URL.Path, "episodes") {
			body = `{"data":[]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	start := time.Now()
	d, e := a.animeDetail(context.Background(), "1")
	cold := time.Since(start)
	if e != nil || len(list(d["providerStatuses"])) != 3 {
		t.Fatal(d, e)
	}
	if maxActive.Load() < 4 {
		t.Fatalf("expected concurrent episodes + sources, got %d", maxActive.Load())
	}
	count := calls.Load()
	start = time.Now()
	_, e = a.animeDetail(context.Background(), "1")
	if e != nil || calls.Load() != count {
		t.Fatal("warm detail fetched upstream", e)
	}
	t.Log(fmt.Sprintf("fixture cold=%s warm=%s upstream calls=%d", cold, time.Since(start), count))
}
func TestStaticAssetCacheHeaders(t *testing.T) {
	a := testApp(t)
	w := apiTest(a, "GET", "/", nil, false)
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("entry page must revalidate")
	}
	w = apiTest(a, "GET", "/assets/missing.js", nil, false)
	if strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("SPA fallback must not be immutable")
	}
}

func TestDetailSummaryDoesNotWaitForEpisodesOrResources(t *testing.T) {
	a := testApp(t)
	a.Store.Patch("config", "settings", Obj{"enabledResourceProviders": []string{"mikan"}})
	var calls atomic.Int32
	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if !strings.Contains(r.URL.Path, "/subjects/") {
			t.Errorf("summary fetched slow dependency: %s", r.URL)
			return nil, errors.New("blocked dependency")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":1,"name":"Fixture","summary":"Fast synopsis","images":{}}`))}, nil
	})
	w := apiTest(a, "GET", "/api/discovery/anime/1?view=summary", nil, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Fast synopsis") || !strings.Contains(w.Body.String(), `"detailPending":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected upstream calls", calls.Load())
	}
	a.Store.Put("cache", "detail:anilist:2", Obj{"anime": Obj{"animeId": "anilist:2"}, "mikanKeyword": "Fixture"})
	w = apiTest(a, "GET", "/api/discovery/anime/anilist:2?view=summary", nil, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"detailPending":true`) || calls.Load() != 1 {
		t.Fatal("external summary blocked on resources", w.Body.String())
	}
}

func TestBangumiCoverUsesResizedHTTPS(t *testing.T) {
	cases := map[string]string{
		"http://lain.bgm.tv/pic/cover/l/1c/b4/390200_Z9jPp.jpg":        "https://lain.bgm.tv/r/400/pic/cover/l/1c/b4/390200_Z9jPp.jpg",
		"https://lain.bgm.tv/pic/cover/l/a6/39/485936_y0bX8.jpg":       "https://lain.bgm.tv/r/400/pic/cover/l/a6/39/485936_y0bX8.jpg",
		"https://lain.bgm.tv/r/400/pic/cover/l/a6/39/485936_y0bX8.jpg": "https://lain.bgm.tv/r/400/pic/cover/l/a6/39/485936_y0bX8.jpg",
		"https://cdn.myanimelist.net/images/anime/1015/138006l.jpg":    "https://cdn.myanimelist.net/images/anime/1015/138006l.jpg",
		"": "",
	}
	for in, want := range cases {
		if got := bangumiCover(in); got != want {
			t.Errorf("bangumiCover(%q) = %q, want %q", in, got, want)
		}
	}
	anime := animeFromBangumi(Obj{"id": 1, "images": Obj{"large": "http://lain.bgm.tv/pic/cover/l/x.jpg"}}, 0)
	if anime["imageUrl"] != "https://lain.bgm.tv/r/400/pic/cover/l/x.jpg" {
		t.Fatalf("animeFromBangumi imageUrl = %v", anime["imageUrl"])
	}
	if image := animeFromBangumi(Obj{"id": 2}, 0)["imageUrl"]; image != nil {
		t.Fatalf("missing images should stay nil, got %v", image)
	}
}

func TestMikanBaseSkipsRedirectingMirror(t *testing.T) {
	a := testApp(t)
	for saved, want := range map[string]string{
		"https://mikanime.tv":   "https://mikanani.me",
		"http://mikanime.tv/":   "https://mikanani.me",
		"":                      "https://mikanani.me",
		"https://mikanani.me/":  "https://mikanani.me",
		"https://mikan.example": "https://mikan.example",
	} {
		s := a.Settings()
		s["mikanBaseUrl"] = saved
		if e := a.Store.Put("config", "settings", s); e != nil {
			t.Fatal(e)
		}
		if got := a.mikanBase(); got != want {
			t.Errorf("mikanBase() with %q = %q, want %q", saved, got, want)
		}
	}
	s := a.Settings()
	s["mikanBaseUrl"] = "https://mikan.example"
	if e := a.Store.Put("config", "settings", s); e != nil {
		t.Fatal(e)
	}
	if got := a.resourceURL("mikan", "葬送"); !strings.HasPrefix(got, "https://mikan.example/RSS/Search?searchstr=") {
		t.Errorf("resourceURL = %q", got)
	}
}

func TestSubgroupOfLeadingTag(t *testing.T) {
	cases := map[string]any{
		"[LoliHouse] 暗机 DARK MACHINE - 02 [WebRip 1080p HEVC-10bit AAC]": "LoliHouse",
		"【喵萌奶茶屋】★10月新番★[葬送的芙莉莲][01][1080p]":                              "喵萌奶茶屋",
		"★10月新番★【喵萌奶茶屋】[青之箱 第二季][06][1080p][简日双语]":                       "喵萌奶茶屋",
		"[10月新番][樱都字幕组] 青之箱 第二季 / Ao no Hako S2 [06][1080p]":             "樱都字幕组",
		"[2026.10][北宇治字幕组] 青之箱 [06][WebRip][HEVC_AAC]":                   "北宇治字幕组",
		"[三明治摆烂组&LoliHouse] 某新番 - 01 [WebRip 1080p]":                     "三明治摆烂组&LoliHouse",
		" [ANi] DARK MACHINE - 01 [1080P][Baha]":                         "ANi",
		"[1080p] Some Show - 01":                                         nil,
		"[简繁内封] Some Show - 01":                                          nil,
		"[01] Some Show":                                                 nil,
		"Some Show - 01 [1080p]":                                         nil,
	}
	for title, want := range cases {
		if got := subgroupOf(title); got != want {
			t.Errorf("subgroupOf(%q) = %v, want %v", title, got, want)
		}
	}
}

func TestFetchBangumiEpisodesSeasonNumberAndMergeResources(t *testing.T) {
	a := testApp(t)
	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[{"id":501,"ep":1,"sort":26,"name":"Start","name_cn":"开幕","airdate":"2026-10-02"},{"id":502,"ep":1,"sort":1,"name":"S1E1","airdate":"2024-10-03"}]}`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	eps := a.fetchBangumiEpisodes(context.Background(), "999")
	if len(eps) != 2 {
		t.Fatalf("expected 2 episodes, got %d", len(eps))
	}
	if str(eps[0]["episodeNumber"]) != "26" || str(eps[0]["seasonNumber"]) != "1" {
		t.Fatalf("expected episodeNumber=26 seasonNumber=1, got %v", eps[0])
	}
	if eps[1]["seasonNumber"] != nil {
		t.Fatalf("expected nil seasonNumber when ep == sort, got %v", eps[1]["seasonNumber"])
	}

	merged := mergeResourceDownloads([]Obj{
		{
			"id":          "1",
			"title":       "[LoliHouse] 青之箱 第二季 / Ao no Hako S2 - 06 [WebRip 1080p HEVC-10bit AAC]",
			"subgroup":    "LoliHouse",
			"torrentUrl":  "https://mikanani.me/Download/20261114/abcdef0123456789abcdef0123456789abcdef01.torrent",
			"providerId":  "mikan",
			"providerIds": []string{"mikan"},
			"sizeBytes":   int64(455000000),
			"sizeText":    "433.9 MB",
		},
		{
			"id":          "2",
			"title":       "[LoliHouse] 青之箱 第二季 / Ao no Hako S2 - 06 [WebRip 1080p HEVC-10bit AAC]",
			"subgroup":    "LoliHouse",
			"magnetUrl":   "magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&dn=test",
			"providerId":  "dmhy",
			"providerIds": []string{"dmhy"},
			"publishedAt": "2026-11-14T23:00:00+08:00",
			"seeders":     42,
		},
	})
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged item, got %d", len(merged))
	}
	if str(merged[0]["magnetUrl"]) == "" || str(merged[0]["torrentUrl"]) == "" {
		t.Fatalf("expected both magnetUrl and torrentUrl on merged item: %v", merged[0])
	}
	if len(list(merged[0]["providerIds"])) != 2 {
		t.Fatalf("expected 2 merged providerIds, got %v", merged[0]["providerIds"])
	}
}

func TestFeedItemMetaReadsMikanTorrentBlock(t *testing.T) {
	x := `<?xml version="1.0" encoding="utf-8"?><rss version="2.0"><channel><title>x</title>` +
		`<item><title>[LoliHouse] A - 02</title><link>https://mikanani.me/Home/Episode/1</link><torrent xmlns="https://mikanani.me/0.1/"><link>https://mikanani.me/Home/Episode/1</link><contentLength>695310720</contentLength><pubDate>2026-10-21T20:19:00</pubDate></torrent><enclosure type="application/x-bittorrent" length="695310720" url="https://mikanani.me/Download/1.torrent" /></item>` +
		`<item><title>B</title><pubDate>Tue, 21 Oct 2026 12:00:00 +0000</pubDate><enclosure type="application/x-bittorrent" length="1048576" url="https://nyaa.si/download/1.torrent" /></item>` +
		`</channel></rss>`
	f, e := gofeed.NewParser().Parse(strings.NewReader(x))
	if e != nil {
		t.Fatal(e)
	}
	published, size := feedItemMeta(f.Items[0])
	if published != "2026-10-21T20:19:00+08:00" || size != 695310720 {
		t.Fatalf("mikan item = %q, %d", published, size)
	}
	if got := sizeText(size); got != "663.1 MB" {
		t.Fatalf("sizeText = %v", got)
	}
	published, size = feedItemMeta(f.Items[1])
	if published != "2026-10-21T12:00:00Z" || size != 1048576 {
		t.Fatalf("standard item = %q, %d", published, size)
	}
	if sizeText(0) != nil || sizeBytes(0) != nil {
		t.Fatal("unknown size should stay nil")
	}
}

func TestExpandedResourceProvidersAndFeedParsing(t *testing.T) {
	a := testApp(t)
	for _, provider := range []string{"mikan", "animegarden", "dmhy", "bangumimoe", "acgnx", "acgrip", "nyaa", "kisssub", "comicat", "tokyotosho"} {
		if u := a.resourceURL(provider, "FX战士久留美"); u == "" {
			t.Fatalf("missing resourceURL for %s", provider)
		}
	}
	xmlFeed := `<?xml version="1.0" encoding="utf-8"?><rss version="2.0"><channel><title>multi</title>` +
		`<item><title>[LoliHouse] Ep 01 [1080p]</title><link>https://share.acgnx.se/show-b42e913651b0f46e7c43f29797ba0c5e412abadb.html</link><description>LoliHouse | 372.2MB | 動畫</description><enclosure url="magnet:?xt=urn:btih:b42e913651b0f46e7c43f29797ba0c5e412abadb" length="1" type="application/x-bittorrent" /></item>` +
		`<item><title>[KissSub] Ep 02 [1080p]</title><link>http://www.kisssub.org/show-b42e913651b0f46e7c43f29797ba0c5e412abadb.html</link><enclosure url="http://v2.uploadbt.com/?r=down&amp;hash=B42E913651B0F46E7C43F29797BA0C5E412ABADB" type="application/x-bittorrent" /></item>` +
		`</channel></rss>`
	f, err := gofeed.NewParser().Parse(strings.NewReader(xmlFeed))
	if err != nil {
		t.Fatal(err)
	}
	src0, kind0 := feedSource(f.Items[0])
	if kind0 != "magnet" || !strings.HasPrefix(src0, "magnet:?xt=urn:btih:") {
		t.Fatalf("magnet enclosure misclassified: src=%q kind=%q", src0, kind0)
	}
	_, size0 := feedItemMeta(f.Items[0])
	if got := sizeText(size0); got != "372.2 MB" {
		t.Fatalf("description size fallback = %v, want 372.2 MB", got)
	}
	src1, kind1 := feedSource(f.Items[1])
	if kind1 != "magnet" || src1 != "magnet:?xt=urn:btih:b42e913651b0f46e7c43f29797ba0c5e412abadb" {
		t.Fatalf("uploadbt hash conversion failed: src=%q kind=%q", src1, kind1)
	}
}

func TestProgressiveDetailReturnsFastProvidersWithoutWaitingForSlow(t *testing.T) {
	a := testApp(t)
	a.Store.Patch("config", "settings", Obj{"enabledResourceProviders": []string{"mikan", "nyaa"}})
	slowGate := make(chan struct{})
	defer func() {
		select {
		case <-slowGate:
		default:
			close(slowGate)
		}
	}()
	var mikanCalls, nyaaCalls atomic.Int32
	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "subjects/"):
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":42,"name":"FastShow","name_cn":"秒开测试番","summary":"Intro","images":{}}`))}, nil
		case strings.Contains(r.URL.Path, "episodes"):
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":101,"sort":1,"name_cn":"第一话","airdate":"2026-10-01"}]}`))}, nil
		case strings.Contains(r.URL.Host, "nyaa"):
			nyaaCalls.Add(1)
			select {
			case <-slowGate:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			body := `<rss version="2.0"><channel><title>nyaa</title><item><title>[Sub] 秒开测试番 - 01 [1080p]</title><enclosure url="magnet:?xt=urn:btih:1111111111111111111111111111111111111111" type="application/x-bittorrent" /></item></channel></rss>`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		default:
			mikanCalls.Add(1)
			body := `<rss version="2.0"><channel><title>mikan</title><item><title>[SFSub] 秒开测试番 - 01 [1080p]</title><enclosure url="https://mikanani.me/Download/fast.torrent" type="application/x-bittorrent" /></item></channel></rss>`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		}
	})

	start := time.Now()
	w := apiTest(a, "GET", "/api/discovery/anime/42?progressive=1&keyword=%E7%A7%92%E5%BC%80%E6%B5%8B%E8%AF%95%E7%95%AA", nil, true)
	elapsed := time.Since(start)
	if w.Code != 200 {
		t.Fatalf("unexpected status %d: %s", w.Code, w.Body.String())
	}
	if elapsed > 600*time.Millisecond {
		t.Fatalf("progressive detail waited too long for slow provider: %s", elapsed)
	}
	body := w.Body.String()
	if !strings.Contains(body, "fast.torrent") || !strings.Contains(body, `"pending":true`) {
		t.Fatalf("expected fast mikan download and pending nyaa status, got: %s", body)
	}

	// Let slow provider finish in the background and verify next poll gets both from cache instantly.
	close(slowGate)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w2 := apiTest(a, "GET", "/api/discovery/anime/42?progressive=1&keyword=%E7%A7%92%E5%BC%80%E6%B5%8B%E8%AF%95%E7%95%AA", nil, true)
		b2 := w2.Body.String()
		if !strings.Contains(b2, `"pending":true`) && strings.Contains(b2, "1111111111111111111111111111111111111111") {
			if mikanCalls.Load() != 1 || nyaaCalls.Load() != 1 {
				t.Fatalf("expected coalesced single upstream call per provider, got mikan=%d nyaa=%d", mikanCalls.Load(), nyaaCalls.Load())
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("background slow provider never settled into cache")
}

func TestLongRunningAnimeEpisodePaginationAndInfoboxAliases(t *testing.T) {
	a := testApp(t)
	a.Store.Patch("config", "settings", Obj{"enabledResourceProviders": []string{"mikan"}})

	var seenKeywordsMu sync.Mutex
	seenKeywords := map[string]bool{}

	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "subjects/"):
			subj := Obj{
				"id":             975,
				"name":           "ONE PIECE",
				"name_cn":        "航海王",
				"total_episodes": 260,
				"infobox": []Obj{
					{"key": "中文名", "value": "航海王"},
					{"key": "别名", "value": []Obj{{"v": "ワンピース"}, {"v": "海贼王"}}},
				},
				"images": Obj{},
			}
			b, _ := json.Marshal(subj)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}, nil

		case strings.Contains(r.URL.Path, "episodes"):
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			count := 100
			if offset == 200 {
				count = 50
			} else if offset > 200 {
				count = 0
			}
			data := make([]Obj, 0, count)
			for i := 0; i < count; i++ {
				epNum := offset + i + 1
				data = append(data, Obj{
					"id":      10000 + epNum,
					"ep":      epNum,
					"sort":    epNum,
					"name":    fmt.Sprintf("Ep %d", epNum),
					"name_cn": fmt.Sprintf("第%d话", epNum),
					"airdate": "2024-01-01",
				})
			}
			b, _ := json.Marshal(Obj{"total": 250, "limit": 100, "offset": offset, "data": data})
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}, nil

		default:
			kw := r.URL.Query().Get("searchstr")
			if kw == "" {
				if u, err := url.QueryUnescape(r.URL.RawQuery); err == nil {
					kw = u
				}
			}
			seenKeywordsMu.Lock()
			seenKeywords[kw] = true
			seenKeywordsMu.Unlock()

			var body string
			switch kw {
			case "航海王":
				body = `<rss version="2.0"><channel><title>mikan</title><item><title>[GroupA] 航海王 - 249 [1080p]</title><enclosure url="magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" type="application/x-bittorrent" /></item></channel></rss>`
			case "海贼王":
				body = `<rss version="2.0"><channel><title>mikan</title><item><title>[GroupB] 海贼王 - 250 [1080p]</title><enclosure url="magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" type="application/x-bittorrent" /></item></channel></rss>`
			default:
				body = `<rss version="2.0"><channel><title>mikan</title></channel></rss>`
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		}
	})

	d, err := a.animeDetail(context.Background(), "975")
	if err != nil {
		t.Fatal(err)
	}
	eps := d["episodes"].([]Obj)
	if len(eps) != 250 {
		t.Fatalf("expected 250 paginated episodes, got %d", len(eps))
	}
	if str(eps[0]["episodeNumber"]) != "1" || str(eps[249]["episodeNumber"]) != "250" {
		t.Fatalf("unexpected episode bounds: first=%v last=%v", eps[0], eps[249])
	}

	seenKeywordsMu.Lock()
	hasMain := seenKeywords["航海王"]
	hasAlias := seenKeywords["海贼王"]
	hasKana := seenKeywords["ワンピース"]
	seenKeywordsMu.Unlock()
	if !hasMain || !hasAlias || hasKana {
		t.Fatalf("unexpected alias search keywords: %+v", seenKeywords)
	}

	dls := d["downloads"].([]Obj)
	if len(dls) != 2 {
		t.Fatalf("expected 2 merged downloads across primary + alias, got %d (%+v)", len(dls), dls)
	}
	titles := d["titles"].([]string)
	foundAliasTitle := false
	for _, item := range titles {
		if item == "海贼王" {
			foundAliasTitle = true
		}
	}
	if !foundAliasTitle {
		t.Fatalf("expected infobox alias in titles, got %v", titles)
	}
}

func TestExtractBangumiStreamingLinks(t *testing.T) {
	subj := Obj{
		"name":    "罗小黑战记 学前篇",
		"name_cn": "罗小黑战记 学前篇",
		"infobox": []any{
			Obj{"key": "在线播放平台", "value": "bilibili、腾讯视频"},
			Obj{"key": "播放网站", "value": []any{
				Obj{"k": "bilibili", "v": "https://www.bilibili.com/bangumi/media/md28234567"},
				Obj{"v": "巴哈姆特动画疯"},
			}},
		},
	}
	links := extractBangumiStreamingLinks(subj, "罗小黑战记 学前篇")
	if len(links) != 3 {
		t.Fatalf("expected 3 deduplicated streaming links, got %d: %+v", len(links), links)
	}
	if str(links[0]["id"]) != "bilibili" || str(links[0]["label"]) != "哔哩哔哩" || str(links[0]["url"]) != "https://www.bilibili.com/bangumi/media/md28234567" {
		t.Fatalf("expected direct bilibili link override, got %+v", links[0])
	}
	if str(links[1]["id"]) != "qq" || str(links[1]["label"]) != "腾讯视频" {
		t.Fatalf("expected qq streaming link, got %+v", links[1])
	}
	if str(links[2]["id"]) != "bahamut" || str(links[2]["label"]) != "巴哈姆特动画疯" {
		t.Fatalf("expected bahamut streaming link, got %+v", links[2])
	}
}
