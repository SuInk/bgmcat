package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Obj map[string]any

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
func num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}
func flag(v any) bool { b, _ := v.(bool); return b }
func object(v any) Obj {
	if m, ok := v.(map[string]any); ok {
		return Obj(m)
	}
	if m, ok := v.(Obj); ok {
		return m
	}
	return Obj{}
}
func list(v any) []any {
	switch values := v.(type) {
	case []Obj:
		result := make([]any, len(values))
		for i, x := range values {
			result[i] = x
		}
		return result
	case []string:
		result := make([]any, len(values))
		for i, x := range values {
			result[i] = x
		}
		return result
	}
	a, _ := v.([]any)
	if a == nil {
		return []any{}
	}
	return a
}
func now() string     { return time.Now().UTC().Format(time.RFC3339Nano) }
func clone(o Obj) Obj { b, _ := json.Marshal(o); var v Obj; _ = json.Unmarshal(b, &v); return v }

type Store struct {
	db   *sql.DB
	mu   sync.Mutex
	Path string
}

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS documents(kind TEXT NOT NULL,key TEXT NOT NULL,value TEXT NOT NULL,PRIMARY KEY(kind,key)); CREATE TABLE IF NOT EXISTS ids(id INTEGER PRIMARY KEY AUTOINCREMENT);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0600)
	return &Store{db: db, Path: path}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Get(kind, key string) (Obj, error) {
	var raw string
	err := s.db.QueryRow("SELECT value FROM documents WHERE kind=? AND key=?", kind, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var o Obj
	err = json.Unmarshal([]byte(raw), &o)
	return o, err
}
func (s *Store) Put(kind, key string, o Obj) error {
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO documents(kind,key,value) VALUES(?,?,?) ON CONFLICT(kind,key) DO UPDATE SET value=excluded.value", kind, key, string(raw))
	return err
}
func (s *Store) Patch(kind, key string, patch Obj) (Obj, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.Get(kind, key)
	if err != nil {
		return nil, err
	}
	if o == nil {
		o = Obj{}
	}
	for k, v := range patch {
		o[k] = v
	}
	return o, s.Put(kind, key, o)
}
func (s *Store) Delete(kind, key string) error {
	_, err := s.db.Exec("DELETE FROM documents WHERE kind=? AND key=?", kind, key)
	return err
}
func (s *Store) List(kind string) ([]Obj, error) {
	rows, err := s.db.Query("SELECT value FROM documents WHERE kind=? ORDER BY rowid DESC", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Obj{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var o Obj
		if err = json.Unmarshal([]byte(raw), &o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *Store) NextID() (int64, error) {
	r, e := s.db.Exec("INSERT INTO ids DEFAULT VALUES")
	if e != nil {
		return 0, e
	}
	return r.LastInsertId()
}
