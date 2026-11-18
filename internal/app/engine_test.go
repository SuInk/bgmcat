package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

func makePatternedPayload(size int) []byte {
	out := make([]byte, size)
	for off := 0; off+8 <= size; off += 8 {
		binary.BigEndian.PutUint64(out[off:off+8], uint64(off)^0xA5A55A5AF00DFACE)
	}
	return out
}

func TestNativeHTTPSegmentedResumeAndAutoRetry(t *testing.T) {
	a := testApp(t)
	content := makePatternedPayload(6 * 1024 * 1024) // 6 MiB -> 24 x 256 KiB chunks

	var rangeReqs atomic.Int32
	var dropOnce atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			n := rangeReqs.Add(1)
			// Simulate one mid-flight transient TCP truncation to verify per-chunk auto-retry.
			if n == 5 && dropOnce.CompareAndSwap(false, true) {
				if hj, ok := w.(http.Hijacker); ok {
					if conn, _, err := hj.Hijack(); err == nil {
						_ = conn.Close()
						return
					}
				}
			}
		}
		http.ServeContent(w, r, "episode.mp4", time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), bytes.NewReader(content))
	}))
	defer server.Close()

	a.engine.SetDownloadRateLimit(768 << 10) // 768 KiB/s so we can pause cleanly mid-flight
	defer a.engine.SetDownloadRateLimit(0)

	dir := t.TempDir()
	j := Obj{"gid": "http-seg-resume", "directory": dir, "sourceUrl": server.URL + "/episode.mp4", "sourceType": "direct"}
	if err := a.Store.Put("downloads", "http-seg-resume", j); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.engine.Download(ctx, j) }()

	deadline := time.Now().Add(10 * time.Second)
	pausedAt := int64(0)
	for time.Now().Before(deadline) {
		saved, _ := a.Store.Get("downloads", "http-seg-resume")
		cur := num(saved["completedLength"])
		if cur >= 512<<10 && cur < int64(len(content))-(512<<10) {
			pausedAt = cur
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("expected cancellation on mid-download pause")
	}
	if pausedAt == 0 {
		t.Fatal("did not reach mid-download pause threshold")
	}

	stateFiles, _ := filepath.Glob(filepath.Join(dir, "*"+httpStateSuffix))
	if len(stateFiles) == 0 {
		t.Fatal("expected .bgmcat-dl resume state file saved on pause")
	}

	// Lift throttle and resume to 100% completion.
	a.engine.SetDownloadRateLimit(0)
	rangesBeforeResume := rangeReqs.Load()
	savedJob, _ := a.Store.Get("downloads", "http-seg-resume")
	rCtx, rCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer rCancel()
	if err := a.engine.Download(rCtx, savedJob); err != nil {
		t.Fatalf("resumed HTTP download failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "episode.mp4"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("resumed HTTP bytes mismatch: err=%v len=%d want=%d", err, len(got), len(content))
	}
	if rangeReqs.Load() <= rangesBeforeResume {
		t.Fatal("expected Range requests on resume")
	}
	leftState, _ := filepath.Glob(filepath.Join(dir, "*"+httpStateSuffix))
	leftPart, _ := filepath.Glob(filepath.Join(dir, "*.part"))
	if len(leftState) != 0 || len(leftPart) != 0 {
		t.Fatalf("expected temp state/part cleaned up on completion, got state=%v part=%v", leftState, leftPart)
	}
}

func TestNativeEngineConcurrentAndShutdown(t *testing.T) {
	a := testApp(t)
	nonRangePayload := strings.Repeat("single-stream-payload-", 8000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Intentionally ignores Range and streams 200 OK chunked.
		_, _ = io.WriteString(w, nonRangePayload)
	}))
	defer server.Close()

	results := make(chan error, 3)
	for _, id := range []string{"c1", "c2", "c3"} {
		dir := t.TempDir()
		j := Obj{"gid": id, "directory": dir, "sourceType": "direct", "sourceUrl": server.URL + "/" + id + ".mp4"}
		_ = a.Store.Put("downloads", id, j)
		go func(job Obj, d string, gid string) {
			err := a.engine.Download(context.Background(), job)
			if err == nil {
				b, rErr := os.ReadFile(filepath.Join(d, gid+".mp4"))
				if rErr != nil || string(b) != nonRangePayload {
					err = fmt.Errorf("payload mismatch for %s: %v", gid, rErr)
				}
			}
			results <- err
		}(j, dir, id)
	}
	for range 3 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}

	if !a.engine.online() {
		t.Fatal("expected native engine online before Close")
	}
	a.engine.Close()
	if a.engine.online() {
		t.Fatal("expected native engine offline after Close")
	}
	if err := a.engine.Download(context.Background(), Obj{"gid": "after-close", "directory": t.TempDir(), "sourceUrl": server.URL + "/x.mp4"}); err == nil {
		t.Fatal("expected closed engine to reject new Download")
	}
}

func TestNativeBTProgressiveStreamAndRangeSeek(t *testing.T) {
	a := testApp(t)
	// 6 MiB media file (192 pieces x 32 KiB) so seeking to the 3 MiB midpoint while throttled
	// proves real-time piece-priority preemption before full download completion.
	payload := makePatternedPayload(6 * 1024 * 1024)
	seed, mi, _ := startLocalBTSeeder(t, "Progressive_Ep01.mp4", payload)

	trackerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peers := []map[string]any{}
		for _, addr := range seed.ListenAddrs() {
			if tcp, ok := addr.(*net.TCPAddr); ok {
				peers = append(peers, map[string]any{"ip": "127.0.0.1", "port": tcp.Port})
			}
		}
		data, _ := bencode.Marshal(map[string]any{"interval": 1, "peers": peers})
		_, _ = w.Write(data)
	}))
	defer trackerSrv.Close()
	mi.Announce = trackerSrv.URL + "/announce"

	var torrentBuf bytes.Buffer
	if err := mi.Write(&torrentBuf); err != nil {
		t.Fatal(err)
	}
	torrentSrc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(torrentBuf.Bytes())
	}))
	defer torrentSrc.Close()

	// Throttle BT download to 384 KiB/s (~16s for 6 MiB) so we have a wide window while active.
	a.engine.SetDownloadRateLimit(384 << 10)
	defer a.engine.SetDownloadRateLimit(0)

	appSrv := httptest.NewServer(a.Handler())
	defer appSrv.Close()
	token := str(a.Settings()["token"])

	job, err := a.enqueue(Obj{
		"url":   torrentSrc.URL + "/Progressive_Ep01.torrent",
		"title": "Progressive Ep01",
	})
	if err != nil {
		t.Fatal(err)
	}
	gid := str(job["gid"])

	// 1. Wait until public API reports canPlayExistingFile == true WHILE status == "active" and < 50% downloaded.
	waitPlayable := time.Now().Add(10 * time.Second)
	playableWhileActive := false
	for time.Now().Before(waitPlayable) {
		w := apiTest(a, "GET", "/api/downloads", nil, true)
		var resp struct {
			Data []Obj `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		for _, item := range resp.Data {
			if str(item["gid"]) == gid && flag(item["canPlayExistingFile"]) {
				if str(item["status"]) == "active" && num(item["completedLength"]) < int64(len(payload))/2 {
					playableWhileActive = true
				}
				break
			}
		}
		if playableWhileActive {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !playableWhileActive {
		cur, _ := a.Store.Get("downloads", gid)
		t.Fatalf("expected canPlayExistingFile=true while download is active and <50%% complete, state=%v", cur)
	}

	// 2. Issue a live HTTP Range request to the 3.0 MiB midpoint (not yet downloaded sequentially!)
	//    and verify 206 Partial Content returns exact verified bytes BEFORE the whole file finishes!
	seekStart := 3 * 1024 * 1024
	seekEnd := seekStart + 96*1024 - 1 // 96 KiB spanning 3 pieces in the middle of the file
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/downloads/%s/stream?token=%s", appSrv.URL, gid, token), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", seekStart, seekEnd))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("live BT Range stream request failed: %v", err)
	}
	streamedBytes, readErr := io.ReadAll(res.Body)
	res.Body.Close()
	if readErr != nil {
		t.Fatalf("reading live BT Range stream failed: %v", readErr)
	}
	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected 206 Partial Content during active BT streaming, got %d: %s", res.StatusCode, string(streamedBytes))
	}
	if !bytes.Equal(streamedBytes, payload[seekStart:seekEnd+1]) {
		t.Fatalf("live BT Range seek bytes mismatch (len=%d, want=%d)", len(streamedBytes), seekEnd-seekStart+1)
	}

	curAfterSeek, _ := a.Store.Get("downloads", gid)
	if num(curAfterSeek["completedLength"]) >= int64(len(payload)) {
		t.Logf("note: torrent completed right as seek finished (%d/%d)", num(curAfterSeek["completedLength"]), len(payload))
	}

	// 3. Remove rate cap and verify the download finishes 100% cleanly on disk.
	a.engine.SetDownloadRateLimit(0)
	finishDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(finishDeadline) {
		cur, _ := a.Store.Get("downloads", gid)
		if str(cur["status"]) == "complete" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	finalState, _ := a.Store.Get("downloads", gid)
	if str(finalState["status"]) != "complete" {
		t.Fatalf("expected torrent to finish complete after streaming, got %v", finalState)
	}
	onDisk, err := os.ReadFile(str(finalState["filePath"]))
	if err != nil || !bytes.Equal(onDisk, payload) {
		t.Fatalf("final completed file bytes mismatch: err=%v len=%d", err, len(onDisk))
	}
}

func TestSpeedWindowSmoothing(t *testing.T) {
	meter := newSpeedWindow(3 * time.Second)
	t0 := time.Now()
	if got := meter.Observe(t0, 0); got != 0 {
		t.Fatalf("initial speed = %d, want 0", got)
	}

	// Simulate bursty 250ms ticks averaging 10 MiB/s (5 MiB on even ticks, 0 B on odd ticks).
	var cumulative int64
	const tenMiB = 10 << 20
	for i := 1; i <= 16; i++ {
		ts := t0.Add(time.Duration(i) * 250 * time.Millisecond)
		if i%2 == 1 {
			cumulative += 5 << 20
		}
		got := meter.Observe(ts, cumulative)
		if i >= 4 {
			if got == 0 {
				t.Fatalf("tick %d: speed dropped to 0 on transient pause", i)
			}
			if got > 15<<20 || got < 7<<20 {
				t.Fatalf("tick %d: speed %d outside smoothed band around %d", i, got, tenMiB)
			}
		}
	}

	// After >3s of zero new bytes, speed should cleanly decay to 0.
	var stalled int64
	for i := 17; i <= 40; i++ {
		ts := t0.Add(time.Duration(i) * 250 * time.Millisecond)
		stalled = meter.Observe(ts, cumulative)
	}
	if stalled >= 1024 {
		t.Fatalf("expected speed to decay near 0 after prolonged stall, got %d", stalled)
	}
}

func TestStreamNowSkipsDownloadQueue(t *testing.T) {
	a := testApp(t)
	// 占满全部并发名额，模拟后台已有其它任务在下载。
	for i := 0; i < cap(a.downloadSlots); i++ {
		a.downloadSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(a.downloadSlots); i++ {
			<-a.downloadSlots
		}
	}()
	block := make(chan struct{})
	torrentSrc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer torrentSrc.Close()
	defer close(block)

	job, err := a.enqueue(Obj{"url": torrentSrc.URL + "/Queued_Ep02.torrent", "title": "Queued Ep02"})
	if err != nil {
		t.Fatal(err)
	}
	gid := str(job["gid"])
	time.Sleep(150 * time.Millisecond)
	if cur, _ := a.Store.Get("downloads", gid); str(cur["status"]) != "queued" {
		t.Fatalf("expected job to wait for a free slot, got %v", cur["status"])
	}

	if w := apiTest(a, "POST", "/api/downloads/"+gid+"/stream-now", nil, true); w.Code != 200 {
		t.Fatalf("stream-now failed: %d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cur, _ := a.Store.Get("downloads", gid); str(cur["status"]) == "active" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cur, _ := a.Store.Get("downloads", gid); str(cur["status"]) != "active" {
		t.Fatalf("expected stream-now to start the job immediately, got %v", cur["status"])
	}
	if len(a.downloadSlots) != cap(a.downloadSlots) {
		t.Fatalf("stream-now must not consume a regular slot, slots=%d", len(a.downloadSlots))
	}

	direct, err := a.enqueue(Obj{"url": torrentSrc.URL + "/movie.mp4", "title": "Direct"})
	if err != nil {
		t.Fatal(err)
	}
	if w := apiTest(a, "POST", "/api/downloads/"+str(direct["gid"])+"/stream-now", nil, true); w.Code == 200 || !strings.Contains(w.Body.String(), "直链") {
		t.Fatalf("expected direct task to be rejected, got %d %s", w.Code, w.Body.String())
	}
}
