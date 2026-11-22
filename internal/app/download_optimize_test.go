package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func TestDriveAdaptiveChunkSizingAndBackoff(t *testing.T) {
	if got := driveChunkSize(16 << 20); got != 8<<20 {
		t.Fatalf("small file chunk = %d, want %d", got, 8<<20)
	}
	if got := driveChunkSize(256 << 20); got != 32<<20 {
		t.Fatalf("medium file chunk = %d, want %d", got, 32<<20)
	}
	if got := driveChunkSize(1536 << 20); got != 64<<20 {
		t.Fatalf("large file chunk = %d, want %d", got, 64<<20)
	}

	if d := archiveRetryDelay(1); d != 5*time.Second {
		t.Fatalf("attempt 1 delay = %v, want 5s", d)
	}
	if d := archiveRetryDelay(2); d != 15*time.Second {
		t.Fatalf("attempt 2 delay = %v, want 15s", d)
	}
	if d := archiveRetryDelay(99); d != 5*time.Minute {
		t.Fatalf("capped delay = %v, want 5m", d)
	}
}

func TestManualArchiveAndCancelEndpoints(t *testing.T) {
	a, drive, _, content := driveArchiveFixture(t, 4*1024*1024)
	// Start as a plain local completed download (saveTo != "cloud").
	a.Store.Patch("downloads", "job", Obj{"saveTo": "local"})

	w := apiTest(a, "POST", "/api/downloads/job/archive", Obj{}, true)
	if w.Code != 200 {
		t.Fatalf("POST /archive status=%d body=%s", w.Code, w.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stored, _ := a.Store.Get("downloads", "job")
		if str(object(stored["cloudArchive"])["status"]) == "archived" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	stored, _ := a.Store.Get("downloads", "job")
	if st := str(object(stored["cloudArchive"])["status"]); st != "archived" {
		t.Fatalf("expected manual archive to reach archived, got %v", stored["cloudArchive"])
	}
	file := object(list(stored["files"])[0])
	if !flag(file["verified"]) || drive.files[str(file["remoteId"])] == nil || !bytes.Equal(drive.files[str(file["remoteId"])].data, content) {
		t.Fatalf("expected verified remote copy in drive, got file=%v", file)
	}

	// Now create a second completed job with a slow upload and cancel it mid-flight.
	dir2 := filepath.Join(a.cfg.DataDir, "downloads", "job2")
	_ = os.MkdirAll(dir2, 0o700)
	path2 := filepath.Join(dir2, "ep02.mkv")
	_ = os.WriteFile(path2, content, 0o600)
	a.Store.Put("downloads", "job2", Obj{
		"gid":       "job2",
		"status":    "complete",
		"saveTo":    "local",
		"directory": dir2,
		"files":     []Obj{{"path": path2, "size": len(content)}},
	})
	drive.mu.Lock()
	drive.chunkDelay = 3 * time.Second
	drive.mu.Unlock()

	if w := apiTest(a, "POST", "/api/downloads/job2/archive", Obj{}, true); w.Code != 200 {
		t.Fatalf("POST /job2/archive status=%d body=%s", w.Code, w.Body.String())
	}
	waitStart := time.Now().Add(2 * time.Second)
	for time.Now().Before(waitStart) && !a.isArchiving("job2") {
		time.Sleep(15 * time.Millisecond)
	}

	startCancel := time.Now()
	cw := apiTest(a, "DELETE", "/api/downloads/job2/archive", nil, true)
	if cw.Code != 200 {
		t.Fatalf("DELETE /job2/archive status=%d body=%s", cw.Code, cw.Body.String())
	}
	if elapsed := time.Since(startCancel); elapsed > 500*time.Millisecond {
		t.Fatalf("cancel archive blocked for %v", elapsed)
	}
	stored2, _ := a.Store.Get("downloads", "job2")
	if str(stored2["saveTo"]) != "local" || stored2["cloudArchive"] != nil {
		t.Fatalf("unexpected job2 state after cancel: %v", stored2)
	}
	if _, err := os.Stat(path2); err != nil {
		t.Fatalf("local file deleted on cancelled archive: %v", err)
	}
}

func TestDeleteDownloadDoesNotBlockOnConcurrentCloudUpload(t *testing.T) {
	a, drive, j, _ := driveArchiveFixture(t, 4*1024*1024)
	drive.chunkDelay = 4 * time.Second

	otherDir := filepath.Join(a.cfg.DataDir, "downloads", "other")
	_ = os.MkdirAll(otherDir, 0o700)
	otherPath := filepath.Join(otherDir, "other.mp4")
	_ = os.WriteFile(otherPath, []byte("hello"), 0o600)
	a.Store.Put("downloads", "other", Obj{
		"gid":       "other",
		"status":    "complete",
		"saveTo":    "local",
		"directory": otherDir,
		"filePath":  otherPath,
	})

	a.dispatchArchiveJob(str(j["gid"]))
	defer a.stopArchiveJob(str(j["gid"]))

	waitUpload := time.Now().Add(2 * time.Second)
	for time.Now().Before(waitUpload) && !a.isArchiving("job") {
		time.Sleep(15 * time.Millisecond)
	}

	start := time.Now()
	w := apiTest(a, "DELETE", "/api/downloads/other", Obj{"deleteLocalFiles": true}, true)
	if w.Code != 200 {
		t.Fatalf("DELETE /other status=%d body=%s", w.Code, w.Body.String())
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("DELETE /other blocked on unrelated cloud upload for %v", elapsed)
	}
	if existing, _ := a.Store.Get("downloads", "other"); existing != nil {
		t.Fatal("other download still in store")
	}
}

func TestCreateDownloadsBatchAndCloudUpgrade(t *testing.T) {
	a := testApp(t)
	a.Store.Patch("config", "settings", Obj{
		"downloadClient":     "qbittorrent",
		"qbittorrentEnabled": false,
		"cloudDriveFolderId": "folder-1",
		"googleClientId":     "cid",
		"googleClientSecret": "sec",
	})
	a.Store.Put("secrets", "google", Obj{
		"access_token":  "tok",
		"refresh_token": "ref",
		"expires_at":    time.Now().Add(time.Hour).Unix(),
	})

	magnet1 := "magnet:?xt=urn:btih:1111111111111111111111111111111111111111&dn=Ep01"
	magnet2 := "magnet:?xt=urn:btih:2222222222222222222222222222222222222222&dn=Ep02"

	w := apiTest(a, "POST", "/api/downloads/batch", Obj{
		"saveTo": "local",
		"items": []Obj{
			{"url": magnet1, "title": "Show - 01"},
			{"url": magnet2, "title": "Show - 02"},
			{"url": magnet1, "title": "Show - 01 duplicate"},
		},
	}, true)
	if w.Code != 200 {
		t.Fatalf("batch status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			QueuedCount  int   `json:"queuedCount"`
			SkippedCount int   `json:"skippedCount"`
			Items        []Obj `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.QueuedCount != 2 || resp.Data.SkippedCount != 1 || len(resp.Data.Items) != 2 {
		t.Fatalf("unexpected batch result: %+v", resp.Data)
	}

	// Re-submitting magnet1 with saveTo="cloud" upgrades the existing task to cloud!
	uw := apiTest(a, "POST", "/api/downloads", Obj{
		"url":    magnet1,
		"saveTo": "cloud",
	}, true)
	if uw.Code != 200 {
		t.Fatalf("upgrade status=%d body=%s", uw.Code, uw.Body.String())
	}
	gid1 := str(resp.Data.Items[0]["gid"])
	stored1, _ := a.Store.Get("downloads", gid1)
	if str(stored1["saveTo"]) != "cloud" {
		t.Fatalf("expected saveTo=cloud after upgrade, got %v", stored1["saveTo"])
	}
}

func TestDownloadEventsSSESnapshotAndLivePush(t *testing.T) {
	a := testApp(t)
	a.Store.Put("downloads", "sse-1", Obj{
		"gid":    "sse-1",
		"status": "active",
	})
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	settings := a.Settings()
	token := str(settings["token"])

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/downloads/events?token="+token, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("SSE status = %d", res.StatusCode)
	}

	reader := bufio.NewReader(res.Body)
	readDownloadsEvent := func() []Obj {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("reading SSE stream: %v", err)
			}
			if strings.HasPrefix(line, "data: ") {
				raw := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
				var list []Obj
				if err := json.Unmarshal([]byte(raw), &list); err != nil {
					t.Fatalf("unmarshal SSE data: %v", err)
				}
				return list
			}
		}
	}

	initial := readDownloadsEvent()
	if len(initial) != 1 || str(initial[0]["gid"]) != "sse-1" {
		t.Fatalf("unexpected initial SSE payload: %v", initial)
	}

	// Mutate download + record live cloud upload speed, then notify SSE subscribers.
	a.setLiveTransfer("sse-1", 0, 4<<20, 12<<20)
	a.Store.Patch("downloads", "sse-1", Obj{
		"status":       "complete",
		"cloudArchive": Obj{"status": "uploading"},
		"files":        []Obj{{"path": "/tmp/ep1.mkv", "size": 8 << 20}},
	})
	a.notifyDownloads()

	updated := readDownloadsEvent()
	if len(updated) != 1 || str(updated[0]["status"]) != "complete" {
		t.Fatalf("unexpected updated SSE payload: %v", updated)
	}
	if speed := num(object(updated[0]["cloudArchive"])["uploadSpeed"]); speed != 12<<20 {
		t.Fatalf("expected overlaid cloudArchive.uploadSpeed=%d, got %d", 12<<20, speed)
	}
}

func startLocalBTSeeder(t *testing.T, filename string, payload []byte) (*torrent.Client, metainfo.MetaInfo, metainfo.Info) {
	t.Helper()
	seedDir := t.TempDir()
	path := filepath.Join(seedDir, filename)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 32 * 1024}
	if err := info.BuildFromFilePath(path); err != nil {
		t.Fatal(err)
	}
	rawInfo, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: rawInfo}
	cfg := localTorrentConfig(seedDir)
	cfg.Seed = true
	seed, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { seed.Close() })
	st, err := seed.AddTorrent(&mi)
	if err != nil {
		t.Fatal(err)
	}
	st.VerifyData()
	return seed, mi, info
}

func TestNativeTorrentFallbackAndMagnetResume(t *testing.T) {
	a := testApp(t)
	payload := bytes.Repeat([]byte("native-bt-fallback-magnet-bytes!"), 20000)
	seed, mi, info := startLocalBTSeeder(t, "Ep01_1080p.mkv", payload)

	trackerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peers := []map[string]any{}
		for _, addr := range seed.ListenAddrs() {
			if tcp, ok := addr.(*net.TCPAddr); ok {
				peers = append(peers, map[string]any{"ip": "127.0.0.1", "port": tcp.Port})
			}
		}
		data, _ := bencode.Marshal(map[string]any{"interval": 2, "peers": peers})
		_, _ = w.Write(data)
	}))
	defer trackerSrv.Close()
	mi.Announce = trackerSrv.URL + "/announce"

	badTorrent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer badTorrent.Close()

	fallbackMagnet := mi.Magnet(nil, &info).String()
	dir := t.TempDir()
	j := Obj{
		"gid":           "real-fb",
		"feedItemTitle": "Native Engine Fallback",
		"sourceType":    "torrent-url",
		"sourceUrl":     badTorrent.URL + "/broken.torrent",
		"fallbackUrl":   fallbackMagnet,
		"directory":     dir,
	}
	if err := a.Store.Put("downloads", "real-fb", j); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := a.engine.Download(ctx, j); err != nil {
		t.Fatalf("native engine fallback to magnet failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "Ep01_1080p.mkv"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("downloaded magnet payload mismatch: err=%v len=%d", err, len(got))
	}
	stored, _ := a.Store.Get("downloads", "real-fb")
	if str(stored["sourceType"]) != "magnet" || str(stored["sourceUrl"]) != fallbackMagnet {
		t.Fatalf("expected persisted magnet fallback, got %v", stored)
	}
	if str(stored["infoHash"]) != strings.ToLower(mi.HashInfoBytes().HexString()) {
		t.Fatalf("expected infoHash persisted, got %v", stored["infoHash"])
	}
	// Verify <infohash>.torrent metadata was saved in task state dir and cachedTorrent matches it.
	if cached := cachedTorrent(a.btTaskStateDir("real-fb"), str(stored["infoHash"])); len(cached) == 0 {
		t.Fatal("expected saved <infohash>.torrent metadata in task state dir")
	}

	// Verify magnet mid-download pause -> instant BoltDB resume + cached .torrent!
	largePayload := bytes.Repeat([]byte("real-magnet-pause-resume-stream!"), 200000) // ~6.4 MiB
	seed2, mi2, info2 := startLocalBTSeeder(t, "Ep01_Resume.mkv", largePayload)
	trackerSrv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peers := []map[string]any{}
		for _, addr := range seed2.ListenAddrs() {
			if tcp, ok := addr.(*net.TCPAddr); ok {
				peers = append(peers, map[string]any{"ip": "127.0.0.1", "port": tcp.Port})
			}
		}
		data, _ := bencode.Marshal(map[string]any{"interval": 2, "peers": peers})
		_, _ = w.Write(data)
	}))
	defer trackerSrv2.Close()
	mi2.Announce = trackerSrv2.URL + "/announce"
	mag2 := mi2.Magnet(nil, &info2).String()

	a.engine.SetDownloadRateLimit(512 << 10)
	defer a.engine.SetDownloadRateLimit(0)

	dir2 := t.TempDir()
	j2 := Obj{
		"gid":        "mag-resume",
		"sourceType": "magnet",
		"sourceUrl":  mag2,
		"directory":  dir2,
	}
	a.Store.Put("downloads", "mag-resume", j2)
	pCtx, pCancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() { doneCh <- a.engine.Download(pCtx, j2) }()

	waitMid := time.Now().Add(15 * time.Second)
	paused := false
	for time.Now().Before(waitMid) {
		cur, _ := a.Store.Get("downloads", "mag-resume")
		if num(cur["completedLength"]) > 128*1024 {
			paused = true
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	pCancel()
	<-doneCh
	if !paused {
		t.Fatal("magnet download did not reach mid-download pause threshold")
	}
	if cached := cachedTorrent(a.btTaskStateDir("mag-resume"), ""); len(cached) == 0 {
		t.Fatal("expected <infohash>.torrent metadata saved before pause")
	}
	a.engine.SetDownloadRateLimit(0)

	resumedJob, _ := a.Store.Get("downloads", "mag-resume")
	rCtx, rCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer rCancel()
	if err := a.engine.Download(rCtx, resumedJob); err != nil {
		t.Fatalf("magnet resume failed: %v", err)
	}
	got2, err := os.ReadFile(filepath.Join(dir2, "Ep01_Resume.mkv"))
	if err != nil || !bytes.Equal(got2, largePayload) {
		t.Fatalf("resumed magnet bytes mismatch: err=%v len=%d", err, len(got2))
	}
}

func TestNativeParallelTrackersWithEmptyDeadAndWSS(t *testing.T) {
	a := testApp(t)
	payload := bytes.Repeat([]byte("native-parallel-tracker-payload!"), 16000)
	seed, mi, _ := startLocalBTSeeder(t, "Ranked_Ep02.mkv", payload)
	hash := mi.HashInfoBytes()

	// Empty public tracker returning 0 peers on both /scrape and /announce (60s interval).
	emptyTracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/scrape" {
			body, _ := bencode.Marshal(map[string]any{
				"files": map[string]any{
					string(hash[:]): map[string]any{"complete": 0, "incomplete": 0, "downloaded": 0},
				},
			})
			_, _ = w.Write(body)
			return
		}
		body, _ := bencode.Marshal(map[string]any{"interval": 60, "peers": []map[string]any{}})
		_, _ = w.Write(body)
	}))
	defer emptyTracker.Close()

	// Fansub tracker that 404s /scrape yet returns the active seeder on /announce.
	ownAnnounceOnly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/scrape" {
			http.NotFound(w, r)
			return
		}
		peers := []map[string]any{}
		for _, addr := range seed.ListenAddrs() {
			if tcp, ok := addr.(*net.TCPAddr); ok {
				peers = append(peers, map[string]any{"ip": "127.0.0.1", "port": tcp.Port})
			}
		}
		body, _ := bencode.Marshal(map[string]any{"interval": 2, "peers": peers})
		_, _ = w.Write(body)
	}))
	defer ownAnnounceOnly.Close()

	a.Store.Patch("config", "settings", Obj{
		"btTrackers": []string{
			"wss://tracker.openwebtorrent.com:443/announce",
			"http://127.0.0.1:1/announce",
			emptyTracker.URL + "/announce",
		},
	})

	mi.Announce = ownAnnounceOnly.URL + "/announce"
	var torrentBuf bytes.Buffer
	if err := mi.Write(&torrentBuf); err != nil {
		t.Fatal(err)
	}
	torrentSrc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(torrentBuf.Bytes())
	}))
	defer torrentSrc.Close()

	dir := t.TempDir()
	j := Obj{
		"gid":        "rank-bt",
		"sourceType": "torrent-url",
		"sourceUrl":  torrentSrc.URL + "/ep02.torrent",
		"directory":  dir,
	}
	a.Store.Put("downloads", "rank-bt", j)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.engine.Download(ctx, j); err != nil {
		t.Fatalf("native parallel tracker BT download failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("expected fast parallel announce without stalling on wss/empty/dead trackers, took %v", elapsed)
	}
	got, err := os.ReadFile(filepath.Join(dir, "Ranked_Ep02.mkv"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("downloaded BT bytes mismatch: err=%v", err)
	}
}

func TestNativeHTTPFilenamesAndUnindexedBTIntegrity(t *testing.T) {
	a := testApp(t)
	httpBytes := bytes.Repeat([]byte("http-direct-compat-bytes!"), 8000)
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/files/Redirected_Ep01.mp4", http.StatusFound)
		case "/cd-utf8":
			w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''%E3%80%90%E5%96%B5%E8%90%8C%E3%80%91%E7%AC%AC02%E8%AF%9D.mkv")
			http.ServeContent(w, r, "raw", time.Time{}, bytes.NewReader(httpBytes))
		default:
			http.ServeContent(w, r, filepath.Base(r.URL.Path), time.Time{}, bytes.NewReader(httpBytes))
		}
	}))
	defer httpSrv.Close()

	for _, tc := range []struct {
		gid      string
		url      string
		wantName string
	}{
		{"http-redir", httpSrv.URL + "/redirect", "Redirected_Ep01.mp4"},
		{"http-cd", httpSrv.URL + "/cd-utf8?id=7", "【喵萌】第02话.mkv"},
		{"http-enc", httpSrv.URL + "/%5BNekomoe%5D%20Ep03%20%5B1080p%5D.mp4", "[Nekomoe] Ep03 [1080p].mp4"},
	} {
		dir := t.TempDir()
		j := Obj{"gid": tc.gid, "sourceType": "direct", "sourceUrl": tc.url, "directory": dir}
		a.Store.Put("downloads", tc.gid, j)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := a.engine.Download(ctx, j)
		cancel()
		if err != nil {
			t.Fatalf("%s failed: %v", tc.gid, err)
		}
		got, err := os.ReadFile(filepath.Join(dir, tc.wantName))
		if err != nil || !bytes.Equal(got, httpBytes) {
			t.Fatalf("%s expected file %s with matching bytes, err=%v", tc.gid, tc.wantName, err)
		}
	}

	// Verify unindexed partial BT file on disk triggers VerifyDataContext and completes cleanly.
	btPayload := bytes.Repeat([]byte("unindexed-bt-partial-recovery!"), 12000)
	seed, mi, _ := startLocalBTSeeder(t, "Recover_Ep04.mkv", btPayload)
	tr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peers := []map[string]any{}
		for _, addr := range seed.ListenAddrs() {
			if tcp, ok := addr.(*net.TCPAddr); ok {
				peers = append(peers, map[string]any{"ip": "127.0.0.1", "port": tcp.Port})
			}
		}
		b, _ := bencode.Marshal(map[string]any{"interval": 2, "peers": peers})
		_, _ = w.Write(b)
	}))
	defer tr.Close()
	mi.Announce = tr.URL + "/announce"
	var tbuf bytes.Buffer
	_ = mi.Write(&tbuf)
	tSrc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(tbuf.Bytes()) }))
	defer tSrc.Close()

	btDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(btDir, "Recover_Ep04.mkv"), btPayload[:64*1024], 0o600); err != nil {
		t.Fatal(err)
	}
	btJob := Obj{"gid": "bt-recover", "sourceType": "torrent-url", "sourceUrl": tSrc.URL + "/ep04.torrent", "directory": btDir}
	a.Store.Put("downloads", "bt-recover", btJob)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := a.engine.Download(ctx, btJob); err != nil {
		t.Fatalf("unindexed BT recovery failed: %v", err)
	}
	gotBT, err := os.ReadFile(filepath.Join(btDir, "Recover_Ep04.mkv"))
	if err != nil || !bytes.Equal(gotBT, btPayload) {
		t.Fatalf("unindexed BT recovery bytes mismatch: err=%v", err)
	}

	// Verify cross-format InfoHash deduplication in enqueue (.torrent URL + magnet:? with same infohash).
	a.Store.Patch("config", "settings", Obj{"downloadClient": "qbittorrent", "qbittorrentEnabled": false})
	hashHex := "98d6daad28e314c6e76eeb8158182613cb346fe8"
	j1, err := a.enqueue(Obj{"url": "https://mikanani.me/Download/20261110/" + hashHex + ".torrent", "title": "Ep05"})
	if err != nil {
		t.Fatal(err)
	}
	j2, err := a.enqueue(Obj{"url": "magnet:?xt=urn:btih:" + strings.ToUpper(hashHex) + "&dn=Ep05_other_tracker", "title": "Ep05 Magnet"})
	if err != nil {
		t.Fatal(err)
	}
	if str(j1["gid"]) != str(j2["gid"]) {
		t.Fatalf("expected cross-format same-infoHash enqueue to deduplicate, got gid1=%s gid2=%s", str(j1["gid"]), str(j2["gid"]))
	}
}

func TestAutoCancelRedundantDuplicateEpisodes(t *testing.T) {
	a := testApp(t)
	dir := filepath.Join(a.cfg.DataDir, "downloads", "Makeine")
	doneJob := Obj{
		"gid":             "done-v1",
		"animeId":         "464376",
		"animeTitle":      "败犬女主太多了！",
		"feedItemTitle":   "[北宇治字幕组] 败犬女主太多了！ [12][1080p][简日内嵌]",
		"status":          "complete",
		"completedLength": 389466910,
		"totalLength":     389466910,
		"directory":       dir,
	}
	deadSameEp := Obj{
		"gid":             "dead-v2",
		"animeId":         "464376",
		"animeTitle":      "败犬女主太多了！",
		"feedItemTitle":   "[Up to 21°C] 败北女角太多了！ - 12 (CR 1920x1080 AVC AAC MKV)",
		"status":          "active",
		"completedLength": 0,
		"downloadSpeed":   0,
		"totalLength":     1463077509,
		"directory":       dir,
	}
	progressingSameEp := Obj{
		"gid":             "prog-v3",
		"animeId":         "464376",
		"animeTitle":      "败犬女主太多了！",
		"feedItemTitle":   "【喵萌奶茶屋】[败犬女主太多了！][12END][1080p]",
		"status":          "active",
		"completedLength": 4096,
		"downloadSpeed":   0,
		"totalLength":     414696088,
		"directory":       dir,
	}
	otherEp := Obj{
		"gid":             "other-ep11",
		"animeId":         "464376",
		"animeTitle":      "败犬女主太多了！",
		"feedItemTitle":   "[北宇治字幕组] 败犬女主太多了！ [11][1080p]",
		"status":          "active",
		"completedLength": 0,
		"downloadSpeed":   0,
		"totalLength":     380000000,
		"directory":       dir,
	}
	for _, j := range []Obj{doneJob, deadSameEp, progressingSameEp, otherEp} {
		if err := a.Store.Put("downloads", str(j["gid"]), j); err != nil {
			t.Fatal(err)
		}
	}

	a.autoCancelRedundantDuplicateEpisodes("done-v1", doneJob)

	gotDead, _ := a.Store.Get("downloads", "dead-v2")
	if str(gotDead["status"]) != "paused" {
		t.Fatalf("expected 0%% dead duplicate same-episode task to be auto-paused, got %s", str(gotDead["status"]))
	}
	gotProg, _ := a.Store.Get("downloads", "prog-v3")
	if str(gotProg["status"]) != "active" {
		t.Fatalf("expected partially downloaded duplicate task to remain active, got %s", str(gotProg["status"]))
	}
	gotOtherEp, _ := a.Store.Get("downloads", "other-ep11")
	if str(gotOtherEp["status"]) != "active" {
		t.Fatalf("expected different episode task to remain active, got %s", str(gotOtherEp["status"]))
	}
}
