package app

import (
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

func TestFFmpegMissingMessageNamesAndroid(t *testing.T) {
	if msg := ffmpegMissingMessage("android"); !strings.Contains(msg, "安卓版暂不支持") {
		t.Fatalf("android message = %q", msg)
	}
	if msg := ffmpegMissingMessage("linux"); !strings.Contains(msg, "FFmpeg 的发布包") {
		t.Fatalf("server message = %q", msg)
	}
}
