package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestAnimeFromBangumiAtEnrichesMetadata(t *testing.T) {
	refNow := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	finished := animeFromBangumiAt(Obj{
		"id":        10380,
		"name":      "STEINS;GATE",
		"name_cn":   "命运石之门",
		"date":      "2011-04-06",
		"platform":  "TV",
		"eps":       24,
		"meta_tags": []any{"科幻", "悬疑", "TV", "日本", "游戏改"},
		"rating":    Obj{"score": 8.8, "rank": 7},
		"images":    Obj{"large": "http://lain.bgm.tv/pic/cover/l/fb/10380.jpg"},
	}, 0, refNow)

	if finished["airStatus"] != "finished" || flag(finished["isOnAir"]) {
		t.Fatalf("expected finished status, got airStatus=%v isOnAir=%v", finished["airStatus"], finished["isOnAir"])
	}
	if finished["airDate"] != "2011-04-06" || finished["format"] != "TV" || num(finished["totalEpisodes"]) != 24 || num(finished["rank"]) != 7 {
		t.Fatalf("unexpected enriched fields: %+v", finished)
	}
	gotTags, _ := finished["tags"].([]string)
	if !slices.Equal(gotTags, []string{"科幻", "悬疑", "游戏改"}) {
		t.Fatalf("expected filtered genre tags, got %v", gotTags)
	}

	upcoming := animeFromBangumiAt(Obj{
		"id":       999999,
		"name":     "Future Show",
		"date":     "2027-01-10",
		"platform": "TV",
		"rating":   Obj{"score": 0, "rank": 0},
	}, 0, refNow)
	if upcoming["airStatus"] != "upcoming" || flag(upcoming["isOnAir"]) {
		t.Fatalf("expected upcoming status, got %+v", upcoming)
	}
	if upcoming["rating"] != nil {
		t.Fatalf("expected zero score to become nil rating, got %v", upcoming["rating"])
	}
}

func TestBuildBangumiCatalogPayloadFilters(t *testing.T) {
	refNow := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	q := url.Values{
		"format":    {"TV"},
		"region":    {"jp"},
		"year":      {"2024"},
		"season":    {"spring"},
		"genre":     {"恋爱"},
		"minRating": {"7.5"},
		"sort":      {"score"},
	}
	cq := parseCatalogQuery(q, refNow)
	payload, impossible, usedRank := buildBangumiCatalogPayload(cq, refNow, true)
	if impossible || !usedRank {
		t.Fatalf("expected valid ranked payload, got impossible=%v usedRank=%v", impossible, usedRank)
	}
	filter := object(payload["filter"])
	tags, _ := filter["tag"].([]string)
	if !slices.Equal(tags, []string{"TV", "日本", "恋爱"}) {
		t.Fatalf("unexpected tags: %v", tags)
	}
	airBounds, _ := filter["air_date"].([]string)
	if !slices.Equal(airBounds, []string{">=2024-04-01", "<2024-07-01"}) {
		t.Fatalf("unexpected air_date bounds: %v", airBounds)
	}
	ratingBounds, _ := filter["rating"].([]string)
	if !slices.Equal(ratingBounds, []string{">=7.5"}) {
		t.Fatalf("unexpected rating bounds: %v", ratingBounds)
	}
	rankBounds, _ := filter["rank"].([]string)
	if !slices.Equal(rankBounds, []string{">0"}) {
		t.Fatalf("expected rank >0 filter, got %v", rankBounds)
	}

	// Impossible combination: past year + upcoming
	impossibleQuery := parseCatalogQuery(url.Values{"year": {"2020"}, "status": {"upcoming"}}, refNow)
	_, impossible, _ = buildBangumiCatalogPayload(impossibleQuery, refNow, true)
	if !impossible {
		t.Fatal("expected 2020 + upcoming to be marked impossible")
	}

	// Quarter without year defaults to current year
	quarterOnly := parseCatalogQuery(url.Values{"year": {"all"}, "season": {"autumn"}}, refNow)
	qPayload, _, _ := buildBangumiCatalogPayload(quarterOnly, refNow, true)
	qAir, _ := object(qPayload["filter"])["air_date"].([]string)
	if !slices.Equal(qAir, []string{">=2026-10-01", "<2027-01-01"}) {
		t.Fatalf("expected current year autumn bounds, got %v", qAir)
	}
}

func TestCatalogEndpointFallbackAndDateSort(t *testing.T) {
	var callCount int
	mockBgm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var body Obj
		_ = json.NewDecoder(r.Body).Decode(&body)
		filter := object(body["filter"])
		if len(list(filter["rank"])) > 0 {
			// Simulate brand-new niche filter with no ranked (>50 votes) entries yet.
			_ = json.NewEncoder(w).Encode(Obj{"total": 0, "data": []any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(Obj{
			"total": 2,
			"data": []Obj{
				{"id": 101, "name": "Older Entry", "name_cn": "较早新番", "date": "2026-07-02", "platform": "TV", "eps": 12, "rating": Obj{"score": 7.8}},
				{"id": 102, "name": "Newer Entry", "name_cn": "最新开播新番", "date": "2026-10-05", "platform": "TV", "eps": 12, "rating": Obj{"score": 8.1}},
			},
		})
	}))
	defer mockBgm.Close()

	prevURL := bangumiCatalogSearchURL
	bangumiCatalogSearchURL = mockBgm.URL
	defer func() { bangumiCatalogSearchURL = prevURL }()

	dir := t.TempDir()
	// Pre-populate legacy settings with empty downloadDir to test backfill as well.
	preStore, err := OpenStore(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = preStore.Put("config", "settings", Obj{"token": "test-static-token", "downloadDir": "", "downloadClient": "aria2"})
	_ = preStore.Close()

	app, err := New(Config{DataDir: dir, Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	if gotDir := str(app.Settings()["downloadDir"]); gotDir != filepath.Join(dir, "downloads") {
		t.Fatalf("expected empty downloadDir to backfill, got %q", gotDir)
	}

	// 1. Test score sort triggering rank>0 fallback seamlessly
	req := httptest.NewRequest("GET", "/api/discovery/catalog?region=cn&year=2026&sort=score&limit=12", nil)
	req.Header.Set("Authorization", "Bearer test-static-token")
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if callCount != 2 {
		t.Fatalf("expected 2 upstream calls (strict rank then fallback), got %d", callCount)
	}

	// 2. Test sort=date orders newest airDate first
	req2 := httptest.NewRequest("GET", "/api/discovery/catalog?year=2026&sort=date&limit=12", nil)
	req2.Header.Set("Authorization", "Bearer test-static-token")
	rec2 := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("expected 200 for sort=date, got %d: %s", rec2.Code, rec2.Body.String())
	}
	var env Obj
	_ = json.Unmarshal(rec2.Body.Bytes(), &env)
	items := list(object(env["data"])["items"])
	if len(items) != 2 || str(object(items[0])["animeId"]) != "102" {
		t.Fatalf("expected newest item 102 first, got %+v", items)
	}
}
