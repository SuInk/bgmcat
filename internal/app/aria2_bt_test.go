package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestBTTrackerOption(t *testing.T) {
	got := btTrackerOption([]any{" udp://tracker.example:1337/announce ", "https://t.example/announce", "udp://tracker.example:1337/announce", "ftp://bad", "", "not a url"})
	want := "udp://tracker.example:1337/announce,https://t.example/announce"
	if got != want {
		t.Fatalf("btTrackerOption = %q, want %q", got, want)
	}
	if btTrackerOption(nil) != "" || btTrackerOption([]any{}) != "" {
		t.Fatal("empty list should produce no option")
	}
}

func TestBTTrackersSettingValidation(t *testing.T) {
	a := testApp(t)
	ok := Obj{"btTrackers": []string{"udp://tracker.example:1337/announce", "wss://tracker.example"}}
	if w := apiTest(a, "PUT", "/api/settings", ok, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if got := btTrackerOption(a.Settings()["btTrackers"]); got != "udp://tracker.example:1337/announce,wss://tracker.example" {
		t.Fatalf("saved trackers = %q", got)
	}
	if w := apiTest(a, "PUT", "/api/settings", Obj{"btTrackers": []string{"magnet:?xt=bad"}}, true); w.Code != 400 {
		t.Fatalf("invalid tracker accepted: %d", w.Code)
	}
}

func TestSettingsBackfillAddsNewKeysOnly(t *testing.T) {
	dir := t.TempDir()
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Go UI")}}
	a, e := New(Config{DataDir: dir, Web: fs.FS(web)})
	if e != nil {
		t.Fatal(e)
	}
	// Simulate a row saved by an older version: no btTrackers, a customised value kept.
	s := a.Settings()
	delete(s, "btTrackers")
	s["mikanBaseUrl"] = "https://mikan.example"
	if e = a.Store.Put("config", "settings", s); e != nil {
		t.Fatal(e)
	}
	a.Close()

	b, e := New(Config{DataDir: dir, Web: fs.FS(web)})
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	got := b.Settings()
	if _, exists := got["btTrackers"]; !exists {
		t.Fatal("btTrackers was not backfilled")
	}
	if got["mikanBaseUrl"] != "https://mikan.example" {
		t.Fatalf("existing value overwritten: %v", got["mikanBaseUrl"])
	}
	for key := range removedSettings {
		if _, exists := got[key]; exists {
			t.Fatalf("removed setting %s came back", key)
		}
	}
}

func TestDeleteWithFilesRemovesAria2Leftovers(t *testing.T) {
	a := testApp(t)
	gid := "0123456789abcdef01234567"
	dir := filepath.Join(str(a.Settings()["downloadDir"]), gid)
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	video := filepath.Join(dir, "episode.mkv")
	for _, name := range []string{"episode.mkv", "episode.mkv.aria2", "ea43743cde0cf627fa2ff1b02936ef6a2ab8a6a1.torrent"} {
		if e := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	job := Obj{"gid": gid, "status": "complete", "directory": dir, "files": []any{Obj{"path": video}}}
	if e := a.Store.Put("downloads", gid, job); e != nil {
		t.Fatal(e)
	}
	w := apiTest(a, "DELETE", "/api/downloads/"+gid, Obj{"deleteLocalFiles": true}, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		entries, _ := os.ReadDir(dir)
		t.Fatalf("task folder should be gone, still has %d entries", len(entries))
	}
}

func TestDeleteKeepsFolderWithUserFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "abc")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	keep := filepath.Join(dir, "notes.txt")
	if e := os.WriteFile(keep, []byte("mine"), 0600); e != nil {
		t.Fatal(e)
	}
	removeAria2Leftovers(dir, "abc")
	if _, e := os.Stat(keep); e != nil {
		t.Fatal("user file removed")
	}
	removeAria2Leftovers(filepath.Dir(dir), "abc")
	if _, e := os.Stat(dir); e != nil {
		t.Fatal("folder not named after the gid must be left alone")
	}
}
