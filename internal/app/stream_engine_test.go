package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// aria2 下了一半的任务交给 anacrolix：已有的部分校验后接着用，播放时可以直接读文件后面还没下的位置。
func TestStreamEngineTakesOverPartialDownload(t *testing.T) {
	a := testApp(t)
	payload := bytes.Repeat([]byte("stream engine fixture "), 16000)
	torrentURL, _ := localSeed(t, payload)
	dir := t.TempDir()
	// 假装 aria2 已经下好了前一半
	half := len(payload) / 2
	partial := make([]byte, len(payload))
	copy(partial, payload[:half])
	if e := os.WriteFile(filepath.Join(dir, "fixture.mp4"), partial, 0600); e != nil {
		t.Fatal(e)
	}
	j := Obj{"gid": "stream-1", "directory": dir, "sourceType": "torrent-url", "sourceUrl": torrentURL, "status": "active", "engine": "anacrolix"}
	a.Store.Put("downloads", "stream-1", j)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.stream.Download(ctx, j) }()

	path := filepath.Join(dir, "fixture.mp4")
	var reader io.ReadSeekCloser
	for reader == nil {
		if r, size, ok := a.stream.Reader("stream-1", path); ok {
			if size != int64(len(payload)) {
				t.Fatal(size)
			}
			reader = r
		}
		select {
		case e := <-done:
			t.Fatal("下载提前结束", e)
		case <-time.After(50 * time.Millisecond):
		}
	}
	reader.Close()
	if _, e := a.Store.Patch("downloads", "stream-1", Obj{"files": []Obj{{"path": path, "size": len(payload)}}}); e != nil {
		t.Fatal(e)
	}
	// 从文件 3/4 处读一段，走播放接口的 Range 请求
	from := len(payload) * 3 / 4
	r := httptest.NewRequest("GET", "/api/downloads/stream-1/stream", nil)
	r.Header.Set("Authorization", "Bearer "+str(a.Settings()["token"]))
	r.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, from+4095))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), payload[from:from+4096]) {
		t.Fatal(w.Code, w.Body.Len())
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(got, payload) {
		t.Fatal("下完的文件不对", e)
	}
	if j, _ := a.Store.Get("downloads", "stream-1"); num(j["completedLength"]) != int64(len(payload)) || num(j["playableBytes"]) != int64(len(payload)) {
		t.Fatal(j["completedLength"], j["playableBytes"])
	}
}

// 接管的任务只有正在下时才能任意拖动；暂停后没有读取器，只能按连续下好的部分算
func TestSeekableStreamOnlyWhileRunning(t *testing.T) {
	file := Obj{"size": 1000}
	running := Obj{"engine": "anacrolix", "status": "active", "playableBytes": 500}
	if !seekableStream(running) || playableSeconds(running, file, 100) != 100 {
		t.Fatal(playableSeconds(running, file, 100))
	}
	paused := Obj{"engine": "anacrolix", "status": "paused", "playableBytes": 500}
	if seekableStream(paused) || playableSeconds(paused, file, 100) != 100*0.5*0.9-6 {
		t.Fatal(playableSeconds(paused, file, 100))
	}
	if seekableStream(Obj{"status": "active"}) {
		t.Fatal("aria2 的任务不能任意拖动")
	}
}

// 用 qBittorrent 或者不是 BT 的任务不切引擎
func TestTakeOverSkipsUnsupportedJobs(t *testing.T) {
	a := testApp(t)
	a.Store.Put("downloads", "http-1", Obj{"gid": "http-1", "sourceType": "direct", "status": "paused"})
	if e := a.takeOver("http-1"); e != nil {
		t.Fatal(e)
	}
	if j, _ := a.Store.Get("downloads", "http-1"); str(j["engine"]) != "" {
		t.Fatal(j["engine"])
	}
}
