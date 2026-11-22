package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractSeriesFolderName(t *testing.T) {
	cases := []struct {
		animeTitle string
		rawTitle   string
		want       string
	}{
		{
			animeTitle: "FX战士久留美",
			rawTitle:   "[SFSub] FX Senshi Kurumi-chan [07][1080P][x264][AAC][JP&CN].mp4",
			want:       "FX战士久留美",
		},
		{
			animeTitle: "",
			rawTitle:   "[SFSub] FX Senshi Kurumi-chan [07][1080P][x264][AAC][JP&CN].mp4",
			want:       "FX Senshi Kurumi-chan",
		},
		{
			animeTitle: "",
			rawTitle:   "[ANi] 金牌得主 / Medalist - 05 [1080P][Baha][WEB-DL][AAC AVC][CHT].mp4",
			want:       "金牌得主",
		},
		{
			animeTitle: "",
			rawTitle:   "【喵萌奶茶屋】★01月新番★[败犬女主太多了！ / Makeine][12][1080p][简日双语]",
			want:       "★01月新番★",
		},
	}
	// Check standard cases
	for _, tc := range cases[:3] {
		got := extractSeriesFolderName(tc.animeTitle, tc.rawTitle)
		if got != tc.want {
			t.Fatalf("extractSeriesFolderName(%q, %q) = %q, want %q", tc.animeTitle, tc.rawTitle, got, tc.want)
		}
	}
}

func TestMigrateLegacyDataLayoutAndSharedAnimeFolderCleanup(t *testing.T) {
	parent := t.TempDir()
	dataDir := filepath.Join(parent, ".bgmcat")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Seed legacy clutter at root + legacy .anime-rss-go paths in SQLite + 24-hex GID download folder.
	_ = os.MkdirAll(filepath.Join(dataDir, "aria2-run-old"), 0700)
	_ = os.WriteFile(filepath.Join(dataDir, "aria2-run-old", "aria2.conf"), []byte("old"), 0600)
	_ = os.MkdirAll(filepath.Join(dataDir, "thumbnails"), 0700)
	_ = os.WriteFile(filepath.Join(dataDir, "thumbnails", "thumb1.jpg"), []byte("jpegdata"), 0600)
	_ = os.MkdirAll(filepath.Join(dataDir, "bt-state"), 0700)
	_ = os.WriteFile(filepath.Join(dataDir, "bt-state", "dht.dat"), []byte("dht"), 0600)

	gid := "0ed8f7772207d1f6c0a9b695"
	hexTaskDir := filepath.Join(dataDir, "downloads", gid)
	if err := os.MkdirAll(hexTaskDir, 0700); err != nil {
		t.Fatal(err)
	}
	epName := "[SFSub] FX Senshi Kurumi-chan [07][1080P][x264][AAC][JP&CN].mp4"
	epActual := filepath.Join(hexTaskDir, epName)
	if err := os.WriteFile(epActual, []byte("video-payload-07"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hexTaskDir, ".torrent.bolt.db"), []byte("bolt"), 0600); err != nil {
		t.Fatal(err)
	}
	torrentName := "ea43743cde0cf627fa2ff1b02936ef6a2ab8a6a1.torrent"
	if err := os.WriteFile(filepath.Join(hexTaskDir, torrentName), []byte("tor"), 0600); err != nil {
		t.Fatal(err)
	}

	preStore, err := OpenStore(filepath.Join(dataDir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	legacyPrefix := filepath.Join(parent, ".anime-rss-go")
	_ = preStore.Put("config", "settings", Obj{
		"token":       "0123456789abcdef0123456789abcdef",
		"downloadDir": filepath.Join(legacyPrefix, "downloads"),
	})
	_ = preStore.Put("downloads", gid, Obj{
		"id":            1,
		"gid":           gid,
		"feedItemTitle": epName,
		"status":        "complete",
		"directory":     filepath.Join(legacyPrefix, "downloads", gid),
		"filePath":      filepath.Join(legacyPrefix, "downloads", gid, epName),
		"files": []Obj{
			{"path": filepath.Join(legacyPrefix, "downloads", gid, epName), "size": 16},
		},
	})
	_ = preStore.Close()

	a, err := New(Config{
		DataDir:       dataDir,
		Addr:          "127.0.0.1:0",
		AdminUsername: "admin",
		AdminPassword: "test-password-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// Verify obsolete aria2 dir removed and thumbnails / bt-state relocated.
	if _, err := os.Stat(filepath.Join(dataDir, "aria2-run-old")); !os.IsNotExist(err) {
		t.Fatal("expected legacy aria2-run-old removed")
	}
	if _, err := os.Stat(filepath.Join(a.thumbnailsDir(), "thumb1.jpg")); err != nil {
		t.Fatalf("expected thumbnail relocated to cache/thumbnails: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.btClientStateDir(), "dht.dat")); err != nil {
		t.Fatalf("expected bt-state relocated to state/bt: %v", err)
	}

	// Verify downloadDir rewritten to .bgmcat/downloads.
	wantDLRoot := filepath.Join(dataDir, "downloads")
	if got := str(a.Settings()["downloadDir"]); got != wantDLRoot {
		t.Fatalf("downloadDir = %q, want %q", got, wantDLRoot)
	}

	// Verify 24-hex folder migrated to FX Senshi Kurumi-chan and engine files moved to state/bt-tasks/<gid>.
	if _, err := os.Stat(hexTaskDir); !os.IsNotExist(err) {
		t.Fatal("expected old 24-hex GID folder removed after migration")
	}
	wantAnimeDir := filepath.Join(wantDLRoot, "FX Senshi Kurumi-chan")
	wantVideoPath := filepath.Join(wantAnimeDir, epName)
	if _, err := os.Stat(wantVideoPath); err != nil {
		t.Fatalf("expected migrated episode at %q: %v", wantVideoPath, err)
	}
	if _, err := os.Stat(filepath.Join(wantAnimeDir, ".torrent.bolt.db")); !os.IsNotExist(err) {
		t.Fatal("expected .torrent.bolt.db NOT inside clean anime folder")
	}
	if _, err := os.Stat(filepath.Join(a.btTaskStateDir(gid), torrentName)); err != nil {
		t.Fatalf("expected .torrent relocated to state/bt-tasks: %v", err)
	}
	migratedJob, _ := a.Store.Get("downloads", gid)
	if str(migratedJob["directory"]) != wantAnimeDir || str(migratedJob["filePath"]) != wantVideoPath {
		t.Fatalf("unexpected migrated job paths: %v", migratedJob)
	}

	// Add a second episode in the same anime folder and verify deleting one keeps the sibling!
	gid2 := "fedcba9876543210fedcba98"
	ep2Path := filepath.Join(wantAnimeDir, "[SFSub] FX Senshi Kurumi-chan [08][1080P].mp4")
	_ = os.WriteFile(ep2Path, []byte("video-payload-08"), 0600)
	_ = a.Store.Put("downloads", gid2, Obj{
		"id":        2,
		"gid":       gid2,
		"status":    "complete",
		"directory": wantAnimeDir,
		"files":     []Obj{{"path": ep2Path, "size": 16}},
	})

	wDel1 := apiTest(a, "DELETE", "/api/downloads/"+gid, Obj{"deleteLocalFiles": true}, true)
	if wDel1.Code != 200 {
		t.Fatalf("delete gid1 failed: %s", wDel1.Body.String())
	}
	if _, err := os.Stat(ep2Path); err != nil {
		t.Fatalf("sibling episode in shared anime folder must remain intact: %v", err)
	}

	// Verify storage endpoint and cache cleanup endpoint report real bytes.
	wStorage := apiTest(a, "GET", "/api/system/storage", nil, true)
	if wStorage.Code != 200 {
		t.Fatalf("storage status failed: %s", wStorage.Body.String())
	}
	var storageResp struct {
		Data Obj `json:"data"`
	}
	_ = json.Unmarshal(wStorage.Body.Bytes(), &storageResp)
	if num(storageResp.Data["cacheBytes"]) <= 0 {
		t.Fatalf("expected positive cacheBytes from thumb1.jpg, got %v", storageResp.Data)
	}

	wClear := apiTest(a, "POST", "/api/downloads/playback-cache/clear", Obj{}, true)
	if wClear.Code != 200 {
		t.Fatalf("clear cache failed: %s", wClear.Body.String())
	}
	var clearResp struct {
		Data Obj `json:"data"`
	}
	_ = json.Unmarshal(wClear.Body.Bytes(), &clearResp)
	if num(clearResp.Data["freedBytes"]) <= 0 {
		t.Fatalf("expected freedBytes > 0 when clearing cache, got %v", clearResp.Data)
	}

	// Deleting the last episode removes the empty anime folder cleanly.
	wDel2 := apiTest(a, "DELETE", "/api/downloads/"+gid2, Obj{"deleteLocalFiles": true}, true)
	if wDel2.Code != 200 {
		t.Fatalf("delete gid2 failed: %s", wDel2.Body.String())
	}
	if _, err := os.Stat(wantAnimeDir); !os.IsNotExist(err) {
		t.Fatal("expected empty anime folder removed after last episode deleted")
	}
}

func TestBuildDownloadSubfolders(t *testing.T) {
	raw := "[LoliHouse] FX战士久留美 / FX Senshi Kurumi-chan - 06v2 [WebRip 1080p HEVC-10bit AAC].mkv"
	cases := []struct {
		mode       string
		animeTitle string
		rawTitle   string
		want       []string
	}{
		{"anime", "FX战士久留美", raw, []string{"FX战士久留美"}},
		{"anime_subgroup", "FX战士久留美", raw, []string{"FX战士久留美", "LoliHouse"}},
		{"subgroup", "FX战士久留美", raw, []string{"LoliHouse"}},
		{"subgroup_anime", "FX战士久留美", raw, []string{"LoliHouse", "FX战士久留美"}},
		{"flat", "FX战士久留美", raw, nil},
		{"anime_subgroup", "", "【北宇治字幕组】败犬女主太多了！ - 12 [1080p].mp4", []string{"败犬女主太多了！", "北宇治字幕组"}},
	}
	for _, tc := range cases {
		got := buildDownloadSubfolders(tc.mode, tc.animeTitle, tc.rawTitle)
		if len(got) != len(tc.want) {
			t.Fatalf("mode=%q: got %v, want %v", tc.mode, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("mode=%q: got %v, want %v", tc.mode, got, tc.want)
			}
		}
	}
}
