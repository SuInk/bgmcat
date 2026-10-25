package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var documentKinds = []string{"config", "secrets", "subscriptions", "feed", "feed_keys", "downloads", "favorites", "logs", "cache", "sessions"}

func (a *App) backupDir() string { return filepath.Join(a.cfg.DataDir, "backups") }
func (a *App) listBackups() []Obj {
	entries, _ := os.ReadDir(a.backupDir())
	out := []Obj{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			continue
		}
		out = append(out, Obj{"name": entry.Name(), "path": filepath.Join(a.backupDir(), entry.Name()), "size": info.Size(), "createdAt": info.ModTime().UTC().Format(time.RFC3339), "reason": "manual", "appVersion": Version})
	}
	return out
}
func (a *App) backup() (Obj, error) {
	_ = os.MkdirAll(a.backupDir(), 0700)
	name := "anime-rss-go-" + time.Now().UTC().Format("20060102-150405") + "-" + randomID(3) + ".zip"
	path := filepath.Join(a.backupDir(), name)
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	z := zip.NewWriter(f)
	rows, e := a.Store.db.Query("SELECT kind,key,value FROM documents")
	if e != nil {
		f.Close()
		return nil, e
	}
	docs := []Obj{}
	for rows.Next() {
		var kind, key, value string
		if e = rows.Scan(&kind, &key, &value); e != nil {
			break
		}
		docs = append(docs, Obj{"kind": kind, "key": key, "value": json.RawMessage(value)})
	}
	rows.Close()
	if e != nil {
		z.Close()
		f.Close()
		return nil, e
	}
	w, e := z.Create("documents.json")
	if e == nil {
		e = json.NewEncoder(w).Encode(Obj{"format": "anime-rss-go", "version": 1, "documents": docs})
	}
	if ce := z.Close(); e == nil {
		e = ce
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return nil, e
	}
	info, _ := os.Stat(path)
	return Obj{"name": name, "path": path, "size": info.Size(), "createdAt": now()}, nil
}
func backupName(name string) bool {
	return name != "" && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\") && strings.HasSuffix(name, ".zip")
}
func (a *App) restore(raw []byte) error { return a.restoreResult(raw, nil) }
func (a *App) restoreResult(raw []byte, result Obj) error {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	a.enqueueMu.Lock()
	defer a.enqueueMu.Unlock()
	a.driveMu.Lock()
	defer a.driveMu.Unlock()
	a.jobsMu.Lock()
	busy := len(a.jobs) > 0
	a.jobsMu.Unlock()
	if busy {
		return errors.New("请先暂停所有下载再恢复备份")
	}
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		return e
	}
	var data Obj
	for _, f := range z.File {
		if f.Name != "documents.json" {
			continue
		}
		if f.UncompressedSize64 > 128<<20 {
			return errors.New("备份过大")
		}
		r, e := f.Open()
		if e != nil {
			return e
		}
		e = json.NewDecoder(io.LimitReader(r, 128<<20)).Decode(&data)
		r.Close()
		if e != nil {
			return e
		}
	}
	if str(data["format"]) != "anime-rss-go" || num(data["version"]) != 1 {
		return errors.New("请选择 Go 版备份；旧版 SQLite 请使用 migrate 命令导入")
	}
	docs := list(data["documents"])
	if len(docs) == 0 {
		return errors.New("空备份")
	}
	hasSettings := false
	for _, v := range docs {
		d := object(v)
		kind := str(d["kind"])
		allowed := false
		for _, k := range documentKinds {
			if kind == k {
				allowed = true
			}
		}
		if !allowed {
			return errors.New("备份包含未知数据类型")
		}
		if kind == "config" && str(d["key"]) == "settings" {
			hasSettings = true
			if len(str(object(d["value"])["token"])) < 24 {
				return errors.New("备份口令无效")
			}
		}
	}
	if !hasSettings {
		return errors.New("备份缺少设置")
	}
	backup, e := a.backup()
	if e != nil {
		return e
	}
	if result != nil {
		result["preRestoreBackupPath"] = backup["path"]
		result["createdAt"] = now()
	}
	tx, e := a.Store.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("DELETE FROM documents"); e != nil {
		return e
	}
	var maxID int64
	for _, v := range docs {
		d := object(v)
		value := object(d["value"])
		if id := num(value["id"]); id > maxID {
			maxID = id
		}
		if str(d["kind"]) == "downloads" && str(value["status"]) == "active" {
			value["status"] = "paused"
		}
		b, _ := json.Marshal(value)
		if _, e = tx.Exec("INSERT INTO documents(kind,key,value) VALUES(?,?,?)", str(d["kind"]), str(d["key"]), string(b)); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("INSERT OR IGNORE INTO ids(id) VALUES(?)", maxID); e != nil {
		return e
	}
	return tx.Commit()
}
func (a *App) backupRoutes() {
	a.handle("GET /api/system/backups", func(w http.ResponseWriter, r *http.Request) error {
		ok(w, Obj{"directory": a.backupDir(), "dataDir": a.cfg.DataDir, "backups": a.listBackups()})
		return nil
	})
	a.handle("POST /api/system/backups", func(w http.ResponseWriter, r *http.Request) error {
		o, e := a.backup()
		if e == nil {
			o["backups"] = a.listBackups()
			ok(w, o)
		}
		return e
	})
	a.handle("GET /api/system/backups/{name}/download", func(w http.ResponseWriter, r *http.Request) error {
		name := r.PathValue("name")
		if !backupName(name) {
			return errors.New("无效备份名称")
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
		http.ServeFile(w, r, filepath.Join(a.backupDir(), name))
		return nil
	})
	a.handle("DELETE /api/system/backups/{name}", func(w http.ResponseWriter, r *http.Request) error {
		name := r.PathValue("name")
		if !backupName(name) {
			return errors.New("无效备份名称")
		}
		if e := os.Remove(filepath.Join(a.backupDir(), name)); e != nil {
			return e
		}
		ok(w, Obj{"deleted": true, "backups": a.listBackups()})
		return nil
	})
	a.handle("POST /api/system/backups/{name}/restore", func(w http.ResponseWriter, r *http.Request) error {
		name := r.PathValue("name")
		if !backupName(name) {
			return errors.New("无效备份名称")
		}
		b, e := os.ReadFile(filepath.Join(a.backupDir(), name))
		if e != nil {
			return e
		}
		result := Obj{"restoredFrom": r.PathValue("name")}
		if result["restoredFrom"] == "" {
			result["restoredFrom"] = "导入的备份"
		}
		if e = a.restoreResult(b, result); e != nil {
			return e
		}
		ok(w, result)
		return nil
	})
	a.handle("POST /api/system/backups/import", func(w http.ResponseWriter, r *http.Request) error {
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<20))
		if e != nil {
			return e
		}
		result := Obj{"restoredFrom": r.PathValue("name")}
		if result["restoredFrom"] == "" {
			result["restoredFrom"] = "导入的备份"
		}
		if e = a.restoreResult(b, result); e != nil {
			return e
		}
		ok(w, result)
		return nil
	})
	a.handle("POST /api/system/open-data-dir", func(w http.ResponseWriter, r *http.Request) error {
		ok(w, Obj{"path": a.cfg.DataDir, "opened": false})
		return nil
	})
}
