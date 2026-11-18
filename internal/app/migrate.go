package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func camel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}
func (a *App) ImportLegacy(path string) error {
	abs, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	if _, e = os.Stat(abs); e != nil {
		return e
	}
	if abs == a.Store.Path {
		return errors.New("源数据库不能是当前数据库")
	}
	u := url.URL{Scheme: "file", Path: abs}
	db, e := sql.Open("sqlite", u.String()+"?mode=ro")
	if e != nil {
		return e
	}
	defer db.Close()
	settings, e := db.Query("SELECT key,value FROM settings")
	if e != nil {
		return e
	}
	patch := Obj{}
	for settings.Next() {
		var k, raw string
		if e = settings.Scan(&k, &raw); e != nil {
			settings.Close()
			return e
		}
		var value any
		if json.Unmarshal([]byte(raw), &value) == nil {
			patch[k] = value
		}
	}
	settings.Close()
	patch["downloadClient"] = "native"
	patch["cloudArchiveEnabled"] = false
	patch["cloudDeleteLocal"] = false
	for _, k := range []string{"gdirectBinaryPath", "gdirectDbPath", "cloudMountDir"} {
		delete(patch, k)
	}
	existing, err := a.Store.List("subscriptions")
	if err != nil {
		return err
	}
	jobs, err := a.Store.List("downloads")
	if err != nil {
		return err
	}
	favs, err := a.Store.List("favorites")
	if err != nil {
		return err
	}
	if len(existing)+len(jobs)+len(favs) > 0 {
		return errors.New("请使用空的数据目录导入，避免覆盖已有内容")
	}
	config := a.Settings()
	for k, v := range patch {
		config[k] = v
	}
	docs := []Obj{{"kind": "config", "key": "settings", "value": config}}
	var max int64
	for table, kind := range map[string]string{"subscriptions": "subscriptions", "feed_items": "feed", "download_jobs": "downloads", "favorite_anime": "favorites"} {
		rows, e := db.Query("SELECT * FROM " + table)
		if e != nil {
			return e
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(cols))
			ptr := make([]any, len(cols))
			for i := range values {
				ptr[i] = &values[i]
			}
			if e = rows.Scan(ptr...); e != nil {
				rows.Close()
				return e
			}
			o := Obj{}
			for i, col := range cols {
				v := values[i]
				if b, yes := v.([]byte); yes {
					v = string(b)
				}
				key := camel(col)
				if key == "ruleJson" {
					key = "rule"
					var r any
					_ = json.Unmarshal([]byte(str(v)), &r)
					v = r
				}
				for _, boolKey := range []string{"enabled", "matched", "isOnAir", "isRestricted"} {
					if key == boolKey {
						v = num(v) != 0
					}
				}
				o[key] = v
			}
			id := str(o["id"])
			if num(o["id"]) > max {
				max = num(o["id"])
			}
			switch kind {
			case "subscriptions":
				o["downloadedEpisodes"] = []any{}
			case "downloads":
				id = str(o["gid"])
				if str(o["status"]) != "complete" {
					o["status"] = "paused"
				}
				o["files"] = []any{}
				p := str(o["filePath"])
				if info, e := os.Stat(p); e == nil && info.Mode().IsRegular() {
					o["files"] = []Obj{{"path": p, "size": info.Size()}}
					o["directory"] = filepath.Dir(p)
				}
			case "favorites":
				id = str(o["animeId"])
				o["subscriptions"] = []any{}
			}
			if id == "" {
				rows.Close()
				return fmt.Errorf("%s 缺少标识", table)
			}
			docs = append(docs, Obj{"kind": kind, "key": id, "value": o})
		}
		rows.Close()
	}
	feeds := map[string]Obj{}
	for _, d := range docs {
		if str(d["kind"]) == "feed" {
			f := object(d["value"])
			feeds[str(d["key"])] = f
			identity := str(f["guid"])
			if identity == "" {
				identity = str(f["sourceUrl"])
			}
			docs = append(docs, Obj{"kind": "feed_keys", "key": hash(str(f["subscriptionId"]) + ":" + identity), "value": Obj{"id": f["id"]}})
		}
	}
	for _, d := range docs {
		if str(d["kind"]) == "downloads" {
			j := object(d["value"])
			f := feeds[str(j["feedItemId"])]
			j["sourceUrl"] = f["sourceUrl"]
			j["sourceType"] = f["sourceType"]
			j["feedItemTitle"] = f["title"]
			j["feedItemImageUrl"] = f["imageUrl"]
			j["feedItemSubscriptionId"] = f["subscriptionId"]
			if str(j["status"]) != "complete" {
				j["directory"] = filepath.Join(str(config["downloadDir"]), str(j["gid"]))
				j["filePath"] = ""
				j["files"] = []any{}
			}
		}
	}
	tx, e := a.Store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, d := range docs {
		raw, err := json.Marshal(d["value"])
		if err != nil {
			return err
		}
		if _, e = tx.Exec("INSERT OR REPLACE INTO documents(kind,key,value) VALUES(?,?,?)", d["kind"], d["key"], string(raw)); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("INSERT OR IGNORE INTO ids(id) VALUES(?)", max); e != nil {
		return e
	}
	e = tx.Commit()
	a.Store.InvalidateCache()
	return e
}
