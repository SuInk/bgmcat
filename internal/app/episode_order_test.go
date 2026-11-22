package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestSortJobsByEpisodeOrder(t *testing.T) {
	jobs := []Obj{
		{"gid": "g-misc", "feedItemTitle": "[Sub] Anime Special PV", "createdAt": int64(1)},
		{"gid": "g-10", "feedItemTitle": "[Sub] Anime - 10 [1080p]", "createdAt": int64(2)},
		{"gid": "g-2", "feedItemTitle": "[Sub] Anime - 02 [1080p]", "createdAt": int64(3)},
		{"gid": "g-12", "feedItemTitle": "[Sub] Anime [12][1080p]", "createdAt": int64(4)},
		{"gid": "g-1", "feedItemTitle": "[Sub] Anime 第01集 [1080p]", "createdAt": int64(5)},
	}
	sortJobsByEpisodeOrder(jobs)
	got := make([]string, 0, len(jobs))
	for _, j := range jobs {
		got = append(got, str(j["gid"]))
	}
	want := []string{"g-1", "g-2", "g-10", "g-12", "g-misc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortJobsByEpisodeOrder = %v, want %v", got, want)
	}
}

func TestScheduleDownloadsFollowsEpisodeOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		order = append(order, r.URL.Path)
		mu.Unlock()
		payload := []byte("episode-data-" + r.URL.Path)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	a := testApp(t)
	a.downloadSlots = make(chan struct{}, 1)

	seed := []struct {
		gid   string
		title string
		path  string
	}{
		{"gid-ep10", "[Sub] Kurumi [10][1080p]", "/ep10.mp4"},
		{"gid-ep02", "[Sub] Kurumi [02][1080p]", "/ep02.mp4"},
		{"gid-ep01", "[Sub] Kurumi [01][1080p]", "/ep01.mp4"},
	}
	for idx, item := range seed {
		dir := filepath.Join(a.cfg.DataDir, "downloads", item.gid)
		_ = a.Store.Put("downloads", item.gid, Obj{
			"gid":           item.gid,
			"feedItemTitle": item.title,
			"directory":     dir,
			"sourceUrl":     srv.URL + item.path,
			"sourceType":    "direct",
			"status":        "queued",
			"createdAt":     int64(idx + 1),
		})
	}

	a.scheduleDownloads()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		done := 0
		for _, item := range seed {
			j, _ := a.Store.Get("downloads", item.gid)
			if str(j["status"]) == "complete" {
				done++
			}
		}
		if done == len(seed) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	want := []string{"/ep01.mp4", "/ep02.mp4", "/ep10.mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("download order = %v, want %v", got, want)
	}
}

func TestArchiveCompletedFollowsEpisodeOrder(t *testing.T) {
	a := testApp(t)
	a.archiveSlots = make(chan struct{}, 1)

	var mu sync.Mutex
	var uploaded []string
	dav := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "MKCOL":
			w.WriteHeader(http.StatusCreated)
		case "PUT":
			mu.Lock()
			uploaded = append(uploaded, filepath.Base(r.URL.Path))
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		case "HEAD":
			w.Header().Set("Content-Length", "8")
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer dav.Close()

	_, _ = a.Store.Patch("config", "settings", Obj{
		"cloudProvider":       "webdav",
		"cloudArchiveEnabled": true,
		"cloudDeleteLocal":    false,
		"webdavUrl":           dav.URL,
		"webdavRootPath":      "/BangumiCat",
	})
	_ = a.Store.Put("secrets", "webdav", Obj{
		"verified":     true,
		"identityHash": a.webdavIdentityHash(a.Settings()),
	})

	seed := []struct {
		gid   string
		title string
		name  string
	}{
		{"gid-up10", "[Sub] Kurumi [10][1080p]", "ep10.mp4"},
		{"gid-up02", "[Sub] Kurumi [02][1080p]", "ep02.mp4"},
		{"gid-up01", "[Sub] Kurumi [01][1080p]", "ep01.mp4"},
	}
	for idx, item := range seed {
		dir := filepath.Join(a.cfg.DataDir, "downloads", item.gid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		filePath := filepath.Join(dir, item.name)
		if err := os.WriteFile(filePath, []byte("12345678"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = a.Store.Put("downloads", item.gid, Obj{
			"gid":           item.gid,
			"feedItemTitle": item.title,
			"directory":     dir,
			"status":        "complete",
			"saveTo":        "cloud",
			"createdAt":     int64(idx + 1),
			"files":         []Obj{{"path": filePath, "size": 8}},
		})
	}

	a.archiveCompleted()

	mu.Lock()
	got := append([]string(nil), uploaded...)
	mu.Unlock()
	want := []string{"gid-up01-ep01.mp4", "gid-up02-ep02.mp4", "gid-up10-ep10.mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("upload order = %v, want %v", got, want)
	}
}
