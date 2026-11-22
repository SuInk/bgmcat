package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func TestOrderTrackersPutsPeersFirst(t *testing.T) {
	own := []string{"http://dead.example/announce", "http://empty.example/announce", "udp://busy.example:6969/announce", "http://dead.example/announce", "not a url"}
	extra := []string{"http://nyaa.example:7777/announce", "udp://busy.example:6969/announce"}
	peers := map[string]int{"http://nyaa.example:7777/announce": 68, "udp://busy.example:6969/announce": 5}
	got := orderTrackers(own, extra, peers)
	want := []string{"http://nyaa.example:7777/announce", "udp://busy.example:6969/announce", "http://dead.example/announce", "http://empty.example/announce"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orderTrackers = %v, want %v", got, want)
	}
}

func TestScrapeURL(t *testing.T) {
	var hash metainfo.Hash
	cases := map[string]string{
		"http://t.example/announce":          "http://t.example/scrape?info_hash=",
		"https://t.example/abc/announce.php": "https://t.example/abc/scrape.php?info_hash=",
		"http://t.example/tracker":           "",
	}
	for announce, prefix := range cases {
		got := scrapeURL(announce, hash)
		if prefix == "" {
			if got != "" {
				t.Errorf("scrapeURL(%q) = %q, want unsupported", announce, got)
			}
			continue
		}
		if len(got) < len(prefix) || got[:len(prefix)] != prefix {
			t.Errorf("scrapeURL(%q) = %q, want prefix %q", announce, got, prefix)
		}
	}
}

func TestScrapeTrackersReportsSwarmSize(t *testing.T) {
	hash := metainfo.Hash{1, 2, 3}
	reply := func(complete, incomplete int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/scrape" || r.URL.Query().Get("info_hash") != string(hash[:]) {
				http.NotFound(w, r)
				return
			}
			body, _ := bencode.Marshal(map[string]any{"files": map[string]any{
				string(hash[:]): map[string]any{"complete": complete, "incomplete": incomplete, "downloaded": 9, "name": "ep"},
			}})
			_, _ = w.Write(body)
		}
	}
	busy := httptest.NewServer(reply(66, 2))
	defer busy.Close()
	empty := httptest.NewServer(reply(0, 0))
	defer empty.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer broken.Close()

	trackers := []string{empty.URL + "/announce", busy.URL + "/announce", broken.URL + "/announce", "http://127.0.0.1:1/announce"}
	got := scrapeTrackers(context.Background(), http.DefaultClient, trackers, hash)
	want := map[string]int{busy.URL + "/announce": 68}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scrapeTrackers = %v, want %v", got, want)
	}
	if order := orderTrackers(trackers, nil, got); order[0] != busy.URL+"/announce" {
		t.Fatalf("busy tracker not first: %v", order)
	}
}

func TestTorrentAndMagnetTrackers(t *testing.T) {
	info, _ := bencode.Marshal(map[string]any{"name": "ep.mkv", "length": 1, "piece length": 16384, "pieces": string(make([]byte, 20))})
	mi := metainfo.MetaInfo{
		Announce:     "http://a.example/announce",
		AnnounceList: metainfo.AnnounceList{{"http://a.example/announce"}, {"udp://b.example:80/announce", "http://c.example/announce"}},
		InfoBytes:    info,
	}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		t.Fatal(err)
	}
	hash, trackers, err := torrentTrackers(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if hash != mi.HashInfoBytes() {
		t.Fatalf("hash = %s, want %s", hash.HexString(), mi.HashInfoBytes().HexString())
	}
	want := []string{"http://a.example/announce", "udp://b.example:80/announce", "http://c.example/announce"}
	if !reflect.DeepEqual(trackers, want) {
		t.Fatalf("torrent trackers = %v, want %v", trackers, want)
	}

	magnet := "magnet:?xt=urn:btih:" + hash.HexString() + "&tr=http%3A%2F%2Fa.example%2Fannounce&tr=udp%3A%2F%2Fb.example%3A80%2Fannounce"
	mHash, mTrackers, err := magnetTrackers(magnet)
	if err != nil {
		t.Fatal(err)
	}
	if mHash != hash || !reflect.DeepEqual(mTrackers, want[:2]) {
		t.Fatalf("magnet = %s %v", mHash.HexString(), mTrackers)
	}
}

func TestSyncPublicTrackersAndSeedingToggle(t *testing.T) {
	a := testApp(t)
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "udp://tracker.opentrackr.org:1337/announce\n\nhttp://nyaa.tracker.wf:7777/announce\nnot-a-tracker\nhttps://tr.bangumi.moe:9696/announce\n")
	}))
	defer feedSrv.Close()

	origFeeds := publicTrackerFeedSources
	publicTrackerFeedSources = [][]string{{feedSrv.URL}}
	defer func() { publicTrackerFeedSources = origFeeds }()

	w := apiTest(a, "POST", "/api/settings/trackers/sync", Obj{}, true)
	if w.Code != 200 {
		t.Fatalf("POST /api/settings/trackers/sync status=%d body=%s", w.Code, w.Body.String())
	}
	gotTrackers := list(a.Settings()["btTrackers"])
	if len(gotTrackers) != 3 {
		t.Fatalf("expected 3 synced trackers saved to btTrackers, got %d: %v", len(gotTrackers), gotTrackers)
	}
	if dyn := a.getDynamicTrackers(); len(dyn) != 3 {
		t.Fatalf("expected 3 dynamic trackers cached, got %d: %v", len(dyn), dyn)
	}

	// Verify aria2SeedEnabled setting can be toggled cleanly via PUT /api/settings.
	sw := apiTest(a, "PUT", "/api/settings", Obj{"aria2SeedEnabled": true}, true)
	if sw.Code != 200 {
		t.Fatalf("PUT /api/settings aria2SeedEnabled=true status=%d body=%s", sw.Code, sw.Body.String())
	}
	if !flag(a.Settings()["aria2SeedEnabled"]) {
		t.Fatal("expected aria2SeedEnabled to be true")
	}
	a.engine.SyncSeedTasks()
	a.engine.StopAllSeeding()
}

func TestFetchErrorMessage(t *testing.T) {
	feed := "https://mikanani.me/RSS/Search?searchstr=X"
	cases := map[string]error{
		"mikanani.me 连接被对方中断":          &url.Error{Op: "Get", URL: feed, Err: io.EOF},
		"mikanani.me 域名解析失败，检查网络或 DNS": &url.Error{Op: "Get", URL: feed, Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "mikanani.me", IsNotFound: true}}},
		"mikanani.me 连接被拒绝":            errors.New(`Get "` + feed + `": dial tcp 1.2.3.4:443: connect: connection refused`),
		"RSS 返回 HTTP 500":              errors.New("RSS 返回 HTTP 500"),
	}
	for want, e := range cases {
		if got := fetchErrorMessage(feed, e); got != want {
			t.Errorf("fetchErrorMessage(%v) = %q, want %q", e, got, want)
		}
	}
}
