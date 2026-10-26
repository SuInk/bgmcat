package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
		" [ANi] DARK MACHINE - 01 [1080P][Baha]":                         "ANi",
		"[1080p] Some Show - 01":                                         nil,
		"[01] Some Show":                                                 nil,
		"Some Show - 01 [1080p]":                                         nil,
	}
	for title, want := range cases {
		if got := subgroupOf(title); got != want {
			t.Errorf("subgroupOf(%q) = %v, want %v", title, got, want)
		}
	}
}
