package app

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestLegacyMigrationAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, e = db.Exec(`CREATE TABLE settings(key TEXT,value TEXT);INSERT INTO settings VALUES('downloadClient','"aria2"');CREATE TABLE subscriptions(id INTEGER,name TEXT,url TEXT,rule_json TEXT,enabled INTEGER);INSERT INTO subscriptions VALUES(3,'old','https://example.test/rss','{"autoDownload":false}',1);CREATE TABLE feed_items(id INTEGER,subscription_id INTEGER,title TEXT,source_url TEXT,source_type TEXT,guid TEXT);INSERT INTO feed_items VALUES(5,3,'episode','https://example.test/episode.mp4','direct','entry');CREATE TABLE download_jobs(id INTEGER,gid TEXT,feed_item_id INTEGER,status TEXT);INSERT INTO download_jobs VALUES(6,'old-job',5,'active');`)
	if e != nil {
		t.Fatal(e)
	}
	a := testApp(t)
	if e = a.ImportLegacy(path); e == nil {
		t.Fatal("missing table accepted")
	}
	subs, _ := a.Store.List("subscriptions")
	if len(subs) != 0 {
		t.Fatal("partial import")
	}
	if _, e = db.Exec(`CREATE TABLE favorite_anime(anime_id TEXT,title TEXT)`); e != nil {
		t.Fatal(e)
	}
	if e = a.ImportLegacy(path); e != nil {
		t.Fatal(e)
	}
	j, _ := a.Store.Get("downloads", "old-job")
	if str(j["status"]) != "paused" || str(j["sourceUrl"]) != "https://example.test/episode.mp4" {
		t.Fatal(j)
	}
	s, _ := a.Store.Get("subscriptions", "3")
	if !flag(s["enabled"]) {
		t.Fatal(s)
	}
	var status string
	db.QueryRow("SELECT status FROM download_jobs").Scan(&status)
	if status != "active" {
		t.Fatal("source changed")
	}
	id, e := a.Store.NextID()
	if e != nil || id <= 6 {
		t.Fatal(id, e)
	}
}
