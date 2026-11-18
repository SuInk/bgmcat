package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/tracker"
	"github.com/anacrolix/torrent/tracker/udp"
)

// aria2 announces to one tracker at a time, in announce-list order, and treats
// any well-formed reply as success — including "0 peers, come back in 30
// minutes". Fansub torrents carry long lists where the first live tracker often
// knows nobody, so a task with dozens of seeders elsewhere sat at 0 B/s for half
// an hour. Before handing a BT task to aria2 we scrape every tracker in
// parallel and move the ones that actually report peers to the front.

const (
	trackerScrapeTimeout    = 1200 * time.Millisecond
	trackerPerScrapeTimeout = 900 * time.Millisecond
	trackerScrapeWorkers    = 32
)

type trackerProbeOutcome int

const (
	trackerDead    trackerProbeOutcome = -2
	trackerEmpty   trackerProbeOutcome = -1
	trackerUnknown trackerProbeOutcome = 0
)

// builtinBTTrackers provides reliable public HTTP/HTTPS and UDP anime/general
// trackers as a fallback after the torrent's own trackers and user-configured
// trackers. Only schemes supported natively by aria2c (http, https, udp) belong here.
var builtinBTTrackers = []string{
	"http://nyaa.tracker.wf:7777/announce",
	"http://tracker.opentrackr.org:1337/announce",
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://open.demonii.com:1337/announce",
	"http://t.nyaatracker.com:80/announce",
	"https://tr.bangumi.moe:9696/announce",
	"http://tr.bangumi.moe:6969/announce",
	"http://open.acgtracker.com:1096/announce",
	"http://t.acg.rip:6699/announce",
	"https://tracker.tamersunion.org:443/announce",
	"https://tracker.gbitt.info:443/announce",
	"http://tracker.gbitt.info:80/announce",
	"https://tracker.renfei.net:443/announce",
}

// torrentTrackers returns the info hash and flattened announce list of a
// .torrent, in the torrent's own order.
func torrentTrackers(data []byte) (metainfo.Hash, []string, error) {
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return metainfo.Hash{}, nil, err
	}
	trackers := []string{}
	for _, tier := range mi.UpvertedAnnounceList() {
		trackers = append(trackers, tier...)
	}
	return mi.HashInfoBytes(), trackers, nil
}

// magnetTrackers returns the info hash and tr= list of a magnet link.
func magnetTrackers(uri string) (metainfo.Hash, []string, error) {
	m, err := metainfo.ParseMagnetUri(uri)
	if err != nil {
		return metainfo.Hash{}, nil, err
	}
	return m.InfoHash, m.Trackers, nil
}

// orderTrackers merges the torrent's trackers with the user's extra ones
// (de-duplicated, invalid entries dropped) and stable-sorts by scraped swarm
// size, so trackers that know peers come first and the rest keep their order.
func orderTrackers(own, extra []string, peers map[string]int) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, list := range [][]string{own, extra} {
		for _, raw := range list {
			t := strings.TrimSpace(raw)
			if t == "" || seen[t] || !validTracker(t) {
				continue
			}
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return peers[out[i]] > peers[out[j]] })
	return out
}

// scrapeURL derives the BEP 48 scrape URL; trackers whose announce path does
// not end in "announce…" do not support scraping.
func scrapeURL(announce string, hash metainfo.Hash) string {
	u, err := url.Parse(announce)
	if err != nil {
		return ""
	}
	dir, last := path.Split(u.Path)
	if !strings.HasPrefix(last, "announce") {
		return ""
	}
	u.Path = dir + "scrape" + strings.TrimPrefix(last, "announce")
	q := u.Query()
	q.Set("info_hash", string(hash[:]))
	u.RawQuery = q.Encode()
	return u.String()
}

func probeHTTPTracker(ctx context.Context, hc *http.Client, announce string, hash metainfo.Hash) trackerProbeOutcome {
	target := scrapeURL(announce, hash)
	if target == "" {
		return trackerUnknown
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return trackerUnknown
	}
	res, err := hc.Do(req)
	if err != nil {
		return trackerDead
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return trackerDead
	}
	if res.StatusCode != http.StatusOK {
		return trackerUnknown
	}
	var decoded struct {
		Files map[string]udp.ScrapeInfohashResult `bencode:"files"`
	}
	if bencode.Unmarshal(body, &decoded) != nil || decoded.Files == nil {
		return trackerUnknown
	}
	r := decoded.Files[string(hash[:])]
	if n := int(r.Seeders) + int(r.Leechers); n > 0 {
		return trackerProbeOutcome(n)
	}
	return trackerEmpty
}

func probeUDPTracker(ctx context.Context, announce string, hash metainfo.Hash) trackerProbeOutcome {
	client, err := tracker.NewClient(announce, tracker.NewClientOpts{})
	if err != nil {
		return trackerUnknown
	}
	defer client.Close()
	res, err := client.Scrape(ctx, []metainfo.Hash{hash})
	if err != nil {
		return trackerDead
	}
	if len(res) == 0 {
		return trackerUnknown
	}
	if n := int(res[0].Seeders) + int(res[0].Leechers); n > 0 {
		return trackerProbeOutcome(n)
	}
	return trackerEmpty
}

func probeTracker(ctx context.Context, hc *http.Client, raw string, hash metainfo.Hash) trackerProbeOutcome {
	if strings.HasPrefix(raw, "udp://") {
		return probeUDPTracker(ctx, raw, hash)
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return probeHTTPTracker(ctx, hc, raw, hash)
	}
	return trackerUnknown
}

// scrapeTrackers asks every tracker for the swarm size of hash, in parallel,
// and returns how many peers each one knows. Trackers that time out, refuse,
// or do not support scraping simply report 0.
func scrapeTrackers(ctx context.Context, hc *http.Client, trackers []string, hash metainfo.Hash) map[string]int {
	if len(trackers) == 0 {
		return map[string]int{}
	}
	ctx, cancel := context.WithTimeout(ctx, trackerScrapeTimeout)
	defer cancel()
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		peers = map[string]int{}
		jobs  = make(chan string, len(trackers))
	)
	for _, t := range trackers {
		jobs <- t
	}
	close(jobs)
	workers := min(trackerScrapeWorkers, len(trackers))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				if ctx.Err() != nil {
					return
				}
				tCtx, tCancel := context.WithTimeout(ctx, trackerPerScrapeTimeout)
				outcome := probeTracker(tCtx, hc, t, hash)
				tCancel()
				if outcome > 0 {
					mu.Lock()
					peers[t] = int(outcome)
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return peers
}
