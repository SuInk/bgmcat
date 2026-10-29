package app

import (
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFFmpegHLSManifestAndSegments(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("optional ffmpeg unavailable")
	}
	if _, e := exec.LookPath("ffprobe"); e != nil {
		t.Skip("optional ffprobe unavailable")
	}
	a := testApp(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "test.mkv")
	out, e := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=160x90:r=10", "-t", "13", "-c:v", "libx264", path).CombinedOutput()
	if e != nil {
		t.Fatalf("fixture: %v %s", e, out)
	}
	info, _ := os.Stat(path)
	a.Store.Put("downloads", "video", Obj{"gid": "video", "directory": dir, "files": []Obj{{"path": path, "size": info.Size()}}})
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	u, _ := url.Parse(server.URL)
	a.cfg.Addr = u.Host
	w := apiTest(a, "GET", "/api/downloads/video/hls/master.m3u8", nil, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "segment00002.ts") || !strings.Contains(w.Body.String(), "#EXT-X-ENDLIST") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiTest(a, "GET", "/api/downloads/video/hls/segment00000.ts", nil, true)
	if w.Code != 200 || w.Body.Len() == 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiTest(a, "GET", "/api/downloads/video/hls/segment00002.ts", nil, true)
	if w.Code != 200 || w.Body.Len() == 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestPartialDownloadWaitsForMissingSegments(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("optional ffmpeg unavailable")
	}
	if _, e := exec.LookPath("ffprobe"); e != nil {
		t.Skip("optional ffprobe unavailable")
	}
	a := testApp(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "test.mkv")
	out, e := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=160x90:r=10", "-t", "60", "-c:v", "libx264", path).CombinedOutput()
	if e != nil {
		t.Fatalf("fixture: %v %s", e, out)
	}
	info, _ := os.Stat(path)
	// 假装只连续下好了一半：能放 60*0.5*0.9-6 = 21 秒，也就是 3 个完整分片
	a.Store.Put("downloads", "video", Obj{"gid": "video", "status": "active", "directory": dir, "playableBytes": info.Size() / 2, "files": []Obj{{"path": path, "size": info.Size()}}})
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	u, _ := url.Parse(server.URL)
	a.cfg.Addr = u.Host
	defer func(old time.Duration) { streamWait = old }(streamWait)
	streamWait = 1500 * time.Millisecond
	// 列表照常是完整的 VOD，没下到的分片靠请求时等待
	w := apiTest(a, "GET", "/api/downloads/video/hls/master.m3u8", nil, true)
	if body := w.Body.String(); w.Code != 200 || !strings.Contains(body, "segment00009.ts") || !strings.Contains(body, "#EXT-X-ENDLIST") {
		t.Fatal(w.Code, body)
	}
	if w = apiTest(a, "GET", "/api/downloads/video/hls/segment00001.ts", nil, true); w.Code != 200 || w.Body.Len() == 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = apiTest(a, "GET", "/api/downloads/video/hls/segment00005.ts", nil, true); w.Code == 200 {
		t.Fatal("segment past the downloaded prefix must not be served before it is downloaded")
	}
	// 下载追上来以后，同一个分片就能拿到
	a.Store.Patch("downloads", "video", Obj{"playableBytes": info.Size()})
	if w = apiTest(a, "GET", "/api/downloads/video/hls/segment00005.ts", nil, true); w.Code != 200 || w.Body.Len() == 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.Store.Patch("downloads", "video", Obj{"playableBytes": info.Size() / 2})
	w = apiTest(a, "GET", "/api/downloads/video/playback", nil, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"complete":false`) || !strings.Contains(w.Body.String(), `"deliveryMode":"hls-transcode"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
