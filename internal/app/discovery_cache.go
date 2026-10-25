package app

import (
	"context"
	"encoding/json"
	"time"
)

// discoveryCached coalesces identical requests and persists successful responses.
// A failed refresh never replaces usable data. Keep this separate from credentials.
func (a *App) discoveryCached(ctx context.Context, key string, ttl, stale time.Duration, fetch func(context.Context) (Obj, error)) (Obj, error) {
	value, err, _ := a.discoveryGroup.Do(key, func() (any, error) {
		cached, _ := a.Store.Get("discovery-cache", key)
		age := time.Now().Unix() - num(cached["savedAt"])
		if cached != nil && age < int64(ttl.Seconds()) {
			return object(cached["value"]), nil
		}
		if cached != nil && age < int64(stale.Seconds()) && num(cached["retryAfter"]) > time.Now().Unix() {
			result := object(cached["value"])
			result["cacheStale"] = true
			result["cacheError"] = "上游暂不可用，显示上次成功结果"
			return result, nil
		}
		bounded, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		result, err := fetch(bounded)
		if err != nil {
			if cached != nil && age < int64(stale.Seconds()) {
				cached["retryAfter"] = time.Now().Add(30 * time.Second).Unix()
				_ = a.Store.Put("discovery-cache", key, cached)
				result = object(cached["value"])
				result["cacheStale"] = true
				result["cacheError"] = "上游暂不可用，显示上次成功结果"
				return result, nil
			}
			return nil, err
		}
		_ = a.Store.Put("discovery-cache", key, Obj{"value": result, "savedAt": time.Now().Unix()})
		// Bound storage even when users explore many titles and search terms.
		_, _ = a.Store.db.Exec("DELETE FROM documents WHERE kind='discovery-cache' AND rowid NOT IN (SELECT rowid FROM documents WHERE kind='discovery-cache' ORDER BY rowid DESC LIMIT 512)")
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	// singleflight shares its result: callers must not share mutable maps/slices.
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var copy Obj
	err = json.Unmarshal(data, &copy)
	return copy, err
}
