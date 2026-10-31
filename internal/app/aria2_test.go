package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAria2ResumeAuthAndCrashRecovery(t *testing.T) {
	a := testApp(t)
	content := bytes.Repeat([]byte("aria2 resume bytes "), 500000)
	var ranges atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			ranges.Add(1)
		}
		slow := &slowAriaWriter{ResponseWriter: w, ctx: r.Context()}
		http.ServeContent(slow, r, "episode.mp4", time.Time{}, bytes.NewReader(content))
	}))
	defer server.Close()
	j := Obj{"gid": "resume", "directory": t.TempDir(), "sourceUrl": server.URL + "/episode.mp4", "sourceType": "direct"}
	if err := a.Store.Put("downloads", "resume", j); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.aria2.Download(ctx, j) }()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		saved, _ := a.Store.Get("downloads", "resume")
		if num(saved["completedLength"]) > 1<<20 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("expected cancellation")
	}
	controls, _ := filepath.Glob(filepath.Join(str(j["directory"]), "*.aria2"))
	if len(controls) == 0 {
		t.Fatal("resume control file not saved")
	}
	endpoint, secret, err := a.aria2.ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = ariaCall(context.Background(), endpoint, "incorrect-secret", "getVersion", nil, nil); err == nil {
		t.Fatal("unauthenticated RPC accepted")
	}
	if strings.Contains(strings.Join(a.aria2.cmd.Args, " "), secret) {
		t.Fatal("secret in process arguments")
	}
	a.aria2.mu.Lock()
	_ = a.aria2.cmd.Process.Kill()
	exited := a.aria2.done
	a.aria2.mu.Unlock()
	<-exited
	resumed, cancelResume := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelResume()
	if err = a.aria2.Download(resumed, j); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(str(j["directory"]), "episode.mp4"))
	if err != nil || !bytes.Equal(data, content) {
		t.Fatal("resumed bytes mismatch", err)
	}
	if ranges.Load() == 0 {
		t.Fatal("no range requests")
	}
	var active []json.RawMessage
	if err = ariaCall(context.Background(), a.aria2.endpoint, a.aria2.secret, "tellActive", nil, &active); err != nil || len(active) != 0 {
		t.Fatal("download leaked", err, len(active))
	}
}

type slowAriaWriter struct {
	http.ResponseWriter
	ctx context.Context
}

func (w *slowAriaWriter) Write(p []byte) (int, error) {
	select {
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	case <-time.After(5 * time.Millisecond):
	}
	return w.ResponseWriter.Write(p)
}
func TestAria2ConcurrentJobsAndMissingExecutable(t *testing.T) {
	a := testApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, strings.Repeat("payload", 10000)) }))
	defer server.Close()
	results := make(chan error, 2)
	for _, id := range []string{"one", "two"} {
		j := Obj{"gid": id, "directory": t.TempDir(), "sourceType": "direct", "sourceUrl": server.URL + "/" + id}
		a.Store.Put("downloads", id, j)
		go func() { results <- a.aria2.Download(context.Background(), j) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("BGMCAT_ARIA2_PATH", filepath.Join(t.TempDir(), "missing"))
	if _, err := aria2Binary(); err == nil {
		t.Fatal("missing binary accepted")
	}
}

func TestAria2Shutdown(t *testing.T) {
	a := testApp(t)
	if _, _, err := a.aria2.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	dir, done := a.aria2.runDir, a.aria2.done
	a.aria2.Close()
	select {
	case <-done:
	default:
		t.Fatal("aria2 process still running")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("private config retained", err)
	}
	if _, _, err := a.aria2.ensure(context.Background()); err == nil {
		t.Fatal("closed engine restarted")
	}
}

func TestContiguousBytes(t *testing.T) {
	// 8 块、每块 10 字节：前 5 块已下好（f8 = 11111000），第 6 块缺
	if got := contiguousBytes("f8", 10, 0, 80); got != 50 {
		t.Fatal(got)
	}
	// 主文件从第 25 字节开始（第 3 块中间），连续到第 50 字节
	if got := contiguousBytes("f8", 10, 25, 40); got != 25 {
		t.Fatal(got)
	}
	if got := contiguousBytes("ff", 10, 0, 80); got != 80 {
		t.Fatal(got)
	}
	if got := contiguousBytes("", 10, 0, 80); got != 0 {
		t.Fatal(got)
	}
	// fb = 1111 1011，只缺第 5 块；b8 = 1011 1000，25..70 落在第 2～6 块，缺第 5、6 块
	if got := missingBytes("fb", 10, 0, 80); got != 10 {
		t.Fatal(got)
	}
	if got := missingBytes("b8", 10, 25, 70); got != 20 {
		t.Fatal(got)
	}
	if got := missingBytes("ff", 10, 30, 30); got != 0 {
		t.Fatal(got)
	}
	if streamStep(0) != 16<<20 || streamStep(1<<20) != 30<<20 || streamStep(10<<20) != 96<<20 {
		t.Fatal(streamStep(1 << 20))
	}
	if got := streamPriority(streamHead); got != "head=16M,tail=16M" {
		t.Fatal(got)
	}
}
