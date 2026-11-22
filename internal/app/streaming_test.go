package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestResolveAnimeStreamingLinksDirectUpgradeAndMerge(t *testing.T) {
	a := testApp(t)
	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body string
		switch {
		case strings.Contains(r.URL.Host, "bgm.tv") && strings.Contains(r.URL.Path, "subjects/593863"):
			body = `{"id":593863,"name":"罗小黑战记 学前篇","name_cn":"罗小黑战记 学前篇","infobox":[{"key":"在线播放平台","value":"bilibili"}]}`
		case strings.Contains(r.URL.Host, "unpkg.com"):
			body = `{"items":[{"title":"葬送のフリーレン","sites":[{"site":"bangumi","id":"400602"},{"site":"gamer","id":"137069"},{"site":"ani_one","id":"PL_ANIONE"},{"site":"youtube","id":"PL_YT"},{"site":"danime","id":"99999"},{"site":"unext","id":"88888"},{"site":"crunchyroll","id":"77777"},{"site":"netflix","id":"81726714"}]}]}`
		case strings.Contains(r.URL.Host, "api.bilibili.com"):
			body = `{"code":0,"data":{"result":[{"title":"<em class=\"keyword\">罗小黑战记</em>","url":"https://www.bilibili.com/bangumi/play/ss1733"}]}}`
		case strings.Contains(r.URL.Host, "graphql.anilist.co"):
			body = `{"data":{"Media":{"title":{"native":"罗小黑战记 学前篇"},"externalLinks":[{"site":"Tencent Video","url":"https://v.qq.com/detail/y/ygci7rbfq3celp8.html","type":"STREAMING"}]}}}`
		default:
			body = `{}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})

	links, err := a.resolveAnimeStreamingLinks(context.Background(), "593863")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("expected 2 merged direct links (bilibili + qq), got %d: %+v", len(links), links)
	}
	if str(links[0]["id"]) != "bilibili" || str(links[0]["url"]) != "https://www.bilibili.com/bangumi/play/ss1733" || !flag(links[0]["direct"]) {
		t.Fatalf("expected bilibili upgraded to direct ss1733, got %+v", links[0])
	}
	if str(links[1]["id"]) != "qq" || str(links[1]["url"]) != "https://v.qq.com/detail/y/ygci7rbfq3celp8.html" {
		t.Fatalf("expected qq direct link from AniList, got %+v", links[1])
	}

	// Verify bangumi-data lookup in memory works for 400602 immediately after index load,
	// dropping danime/unext/crunchyroll and redundant generic youtube when ani_one is present.
	frieren := a.fastStreamingLinks("400602", nil, "葬送的芙莉莲")
	if len(frieren) != 3 || str(frieren[0]["id"]) != "bahamut" || str(frieren[0]["label"]) != "动画疯" || str(frieren[1]["id"]) != "ani_one" || str(frieren[2]["id"]) != "netflix" {
		t.Fatalf("expected bahamut (动画疯) + ani_one + netflix from bangumi-data memory index, got %+v", frieren)
	}
}

func TestEnrichAnimeBroadcastFromBangumiDataAndBilibiliTimeline(t *testing.T) {
	a := testApp(t)
	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body string
		switch {
		case strings.Contains(r.URL.Host, "unpkg.com"):
			body = `{"items":[{"title":"魔法少女育成計画 restart","begin":"2026-10-05T17:00:00.000Z","broadcast":"R/2026-10-05T17:00:00.000Z/P7D","sites":[{"site":"bangumi","id":"418229"},{"site":"mikan","id":"4105"},{"site":"gamer","id":"140001","broadcast":"R/2026-10-05T17:30:00.000Z/P7D"}]}]}`
		case strings.Contains(r.URL.Host, "api.bilibili.com") && strings.Contains(r.URL.Path, "pgc/web/timeline"):
			body = `{"code":0,"result":[{"episodes":[{"title":"罗小黑战记","pub_time":"12:00","pub_ts":1794542400}]}]}`
		default:
			body = `{}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})

	a.ensureBangumiDataLoaded(context.Background())
	a.ensureBilibiliTimelineLoaded(context.Background())

	jpAnime := Obj{"animeId": "418229", "bangumiId": "418229", "title": "魔法少女育成计划 restart"}
	a.enrichAnimeBroadcast(jpAnime)
	if got := str(jpAnime["broadcastAt"]); got != "2026-10-05T17:00:00Z" {
		t.Fatalf("expected broadcastAt 2026-10-05T17:00:00Z, got %q", got)
	}
	if gotMikan := str(jpAnime["mikanId"]); gotMikan != "4105" {
		t.Fatalf("expected mikanId 4105, got %q", gotMikan)
	}

	cnAnime := Obj{"animeId": "593863", "bangumiId": "593863", "title": "罗小黑战记 学前篇"}
	a.enrichAnimeBroadcast(cnAnime)
	if got := str(cnAnime["broadcastAt"]); got != "2026-11-13T04:00:00Z" {
		t.Fatalf("expected Bilibili timeline broadcastAt 2026-11-13T04:00:00Z, got %q", got)
	}
}
