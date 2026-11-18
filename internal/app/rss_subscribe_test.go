package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEpisodeParserFullwidthAndSpecials(t *testing.T) {
	cases := []struct {
		title string
		want  any
		batch bool
		spec  bool
	}{
		{"【悠哈璃羽字幕社】【FX戰士久留美】【01】【x264 1080p】【CHT】", 1, false, false},
		{"[北宇治字幕组] FX战士久留美 / FX Senshi Kurumi-chan [07][WebRip][HEVC_AAC][简日内嵌]", 7, false, false},
		{"[LoliHouse] Show S02E05 [WebRip 1080p HEVC-10bit AAC][简繁内封字幕]", 5, false, false},
		{"[Sub] Show - 06v2 [1920x1080][CHS]", 6, false, false},
		{"[Sub] Show [01-12][1080p][合集]", nil, true, false},
		{"[Sub] Show [01~12 END][1080p]", nil, true, false},
		{"[Sub] Show [NCOP 01][1080p]", nil, false, true},
		{"[Sub] Show [PV 02][1080p]", nil, false, true},
		{"[Sub] Show [SP01][1080p]", nil, false, true},
		{"[沸班亚马制作组] 航海王 - 1159 [IQIYI WebRip 2160p HEVC AAC]", 1159, false, false},
		{"[Nix-Raws] 海贼王 / ワンピース / One Piece S01E1180 [CR WEB-DL 1080p]", 1180, false, false},
		{"[Skymoon-Raws][One Piece 海賊王][1180][ViuTV][1080p]", 1180, false, false},
		{"[ANi] 海贼王 - 1080 [1080P]", 1080, false, false},
		{"[7³ACG] 航海王剧场版：红发歌姬/One Piece Film Red 2022 [1080p]", nil, false, false},
		{"[AnimeRG] 海贼王.One Piece (Episodes 001-746) [720p]", nil, true, false},
	}
	for _, tc := range cases {
		if got := episode(tc.title); got != tc.want {
			t.Fatalf("episode(%q) = %v, want %v", tc.title, got, tc.want)
		}
		if got := isBatchRelease(tc.title); got != tc.batch {
			t.Fatalf("isBatchRelease(%q) = %v, want %v", tc.title, got, tc.batch)
		}
		if got := isSpecialOrExtraRelease(tc.title); got != tc.spec {
			t.Fatalf("isSpecialOrExtraRelease(%q) = %v, want %v", tc.title, got, tc.spec)
		}
	}
}

func TestSubscribeOnlyNewAndLockedSubgroupDedupAcrossProviders(t *testing.T) {
	a := testApp(t)
	payloadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "video-bytes")
	}))
	defer payloadSrv.Close()

	var phase atomic.Int32 // 0 = initial (ep 1..7), 1 = ep 8 fast other group, 2 = ep 8 locked subgroup CHS+CHT across mirrors
	oldPub := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC1123Z)
	newPub := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC1123Z)

	feedMikan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var items strings.Builder
		for ep := 1; ep <= 7; ep++ {
			fmt.Fprintf(&items, `<item><guid>m-old-%d</guid><pubDate>%s</pubDate><title>[北宇治字幕组] FX战士久留美 [%02d][1080p][简日内嵌]</title><enclosure url="%s/m-ep%d.mp4" /></item>`, ep, oldPub, ep, payloadSrv.URL, ep)
		}
		p := phase.Load()
		if p >= 1 {
			fmt.Fprintf(&items, `<item><guid>m-ep8-other</guid><pubDate>%s</pubDate><title>【悠哈璃羽字幕社】【FX戰士久留美】【08】【1080P】【CHS】</title><enclosure url="%s/other-ep8.mp4" /></item>`, newPub, payloadSrv.URL)
		}
		if p >= 2 {
			fmt.Fprintf(&items, `<item><guid>m-ep8-cht</guid><pubDate>%s</pubDate><title>[北宇治字幕组] FX战士久留美 [08][1080p][繁日内嵌]</title><enclosure url="%s/cht-ep8.mp4" /></item>`, newPub, payloadSrv.URL)
			fmt.Fprintf(&items, `<item><guid>m-ep8-chs</guid><pubDate>%s</pubDate><title>[北宇治字幕组] FX战士久留美 [08][1080p][简日内嵌]</title><enclosure url="%s/chs-ep8-mikan.mp4" /></item>`, newPub, payloadSrv.URL)
		}
		fmt.Fprintf(w, `<rss version="2.0"><channel><title>Mikan</title>%s</channel></rss>`, items.String())
	}))
	defer feedMikan.Close()

	feedDmhy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var items strings.Builder
		for ep := 1; ep <= 7; ep++ {
			fmt.Fprintf(&items, `<item><guid>d-old-%d</guid><pubDate>%s</pubDate><title>【悠哈璃羽字幕社】【FX戰士久留美】【%02d】【1080P】【CHT】</title><enclosure url="%s/d-ep%d.mp4" /></item>`, ep, oldPub, ep, payloadSrv.URL, ep)
		}
		if phase.Load() >= 2 {
			fmt.Fprintf(&items, `<item><guid>d-ep8-chs</guid><pubDate>%s</pubDate><title>[北宇治字幕组] FX战士久留美 [08][1080p][简日内嵌]</title><enclosure url="%s/chs-ep8-dmhy.mp4" /></item>`, newPub, payloadSrv.URL)
		}
		fmt.Fprintf(w, `<rss version="2.0"><channel><title>DMHY</title>%s</channel></rss>`, items.String())
	}))
	defer feedDmhy.Close()

	rule := buildAnimeSubscriptionRule(Obj{
		"subgroup":        "北宇治字幕组",
		"resolution":      "1080p",
		"language":        "chs",
		"onlyNew":         true,
		"baselineEpisode": 7,
	}, now())

	if _, err := a.newSubscription(Obj{"name": "FX战士久留美 · 蜜柑计划", "url": feedMikan.URL, "animeId": "fx515", "providerId": "mikan", "rule": clone(rule)}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.newSubscription(Obj{"name": "FX战士久留美 · 动漫花园", "url": feedDmhy.URL, "animeId": "fx515", "providerId": "dmhy", "rule": clone(rule)}); err != nil {
		t.Fatal(err)
	}

	// Phase 0: initial sync with episodes 1..7 already in feed -> 0 downloads queued!
	a.syncAnimeSubscriptions(context.Background(), "fx515")
	jobs, _ := a.Store.List("downloads")
	if len(jobs) != 0 {
		t.Fatalf("expected 0 downloads for pre-existing episodes under onlyNew=true, got %d", len(jobs))
	}

	// Phase 1: fast non-matching subgroup drops ep 8 first -> must NOT be downloaded!
	phase.Store(1)
	a.syncAnimeSubscriptions(context.Background(), "fx515")
	jobs, _ = a.Store.List("downloads")
	if len(jobs) != 0 {
		t.Fatalf("expected 0 downloads when non-locked subgroup released ep 8, got %d", len(jobs))
	}

	// Phase 2: locked subgroup drops both CHT and CHS across both Mikan and DMHY -> strictly 1 CHS download queued!
	phase.Store(2)
	a.syncAnimeSubscriptions(context.Background(), "fx515")
	jobs, _ = a.Store.List("downloads")
	if len(jobs) != 1 {
		t.Fatalf("expected strictly 1 download for ep 8 across mirrors & languages, got %d: %+v", len(jobs), jobs)
	}
	gotURL := str(jobs[0]["sourceUrl"])
	if !strings.Contains(gotURL, "chs-ep8-") {
		t.Fatalf("expected CHS ep8 URL, got %s", gotURL)
	}
	if str(jobs[0]["animeId"]) != "fx515" || str(jobs[0]["animeTitle"]) != "FX战士久留美" {
		t.Fatalf("expected canonical animeId/animeTitle on download job, got animeId=%q animeTitle=%q", str(jobs[0]["animeId"]), str(jobs[0]["animeTitle"]))
	}

	// Test unsubscribe endpoint keeps favorite and deletes all sibling subscriptions
	_ = a.Store.Put("favorites", "fx515", Obj{"animeId": "fx515", "title": "FX战士久留美"})
	w := apiTest(a, "DELETE", "/api/favorites/fx515/subscriptions", nil, true)
	if w.Code != 200 {
		t.Fatalf("unsubscribe status=%d body=%s", w.Code, w.Body.String())
	}
	subs, _ := a.Store.List("subscriptions")
	if len(subs) != 0 {
		t.Fatalf("expected 0 subscriptions after unsubscribe, got %d", len(subs))
	}
	fav, _ := a.Store.Get("favorites", "fx515")
	if fav == nil {
		t.Fatal("expected favorite record to remain after cancelling subscriptions")
	}
}

func TestCatchUpAutoSubgroupSelectsOneVersionPerEpisode(t *testing.T) {
	a := testApp(t)
	payloadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "video-bytes")
	}))
	defer payloadSrv.Close()

	feedA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, fmt.Sprintf(`<rss version="2.0"><channel><title>A</title>
			<item><guid>a1</guid><title>【悠哈璃羽字幕社】【FX戰士久留美】【01】【1080P】【CHT】</title><enclosure url="%s/a1.mp4" /></item>
			<item><guid>a2</guid><title>[北宇治字幕组] FX战士久留美 [01][1080p][简日内嵌]</title><enclosure url="%s/a2.mp4" /></item>
			<item><guid>a3</guid><title>[北宇治字幕组] FX战士久留美 [02][1080p][简日内嵌]</title><enclosure url="%s/a3.mp4" /></item>
			<item><guid>a4</guid><title>[北宇治字幕组] FX战士久留美 [PV 01][1080p]</title><enclosure url="%s/pv.mp4" /></item>
		</channel></rss>`, payloadSrv.URL, payloadSrv.URL, payloadSrv.URL, payloadSrv.URL))
	}))
	defer feedA.Close()

	feedB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, fmt.Sprintf(`<rss version="2.0"><channel><title>B</title>
			<item><guid>b1</guid><title>[樱都字幕组] FX战士久留美 [01][720p][简日]</title><enclosure url="%s/b1.mp4" /></item>
			<item><guid>b2</guid><title>[樱都字幕组] FX战士久留美 [02][1080p][简日]</title><enclosure url="%s/b2.mp4" /></item>
		</channel></rss>`, payloadSrv.URL, payloadSrv.URL))
	}))
	defer feedB.Close()

	rule := buildAnimeSubscriptionRule(Obj{
		"subgroup":   "all",
		"resolution": "1080p",
		"language":   "chs",
		"onlyNew":    false,
	}, now())
	_, _ = a.newSubscription(Obj{"name": "FX战士久留美 · A", "url": feedA.URL, "animeId": "fx", "providerId": "mikan", "rule": clone(rule)})
	_, _ = a.newSubscription(Obj{"name": "FX战士久留美 · B", "url": feedB.URL, "animeId": "fx", "providerId": "dmhy", "rule": clone(rule)})

	a.syncAnimeSubscriptions(context.Background(), "fx")
	jobs, _ := a.Store.List("downloads")
	if len(jobs) != 2 {
		t.Fatalf("expected strictly 2 downloads (ep 1 and ep 2, 1 version each), got %d", len(jobs))
	}
}
