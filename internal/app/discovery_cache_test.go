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
