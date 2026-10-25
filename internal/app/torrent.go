package app

import (
	"context"
	"errors"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

type TorrentEngine struct {
	app    *App
	mu     sync.Mutex
	client *torrent.Client
}

func (t *TorrentEngine) get() (*torrent.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client != nil {
		return t.client, nil
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = filepath.Join(t.app.cfg.DataDir, "torrent")
	cfg.NoUpload = true
	cfg.Seed = false
	cfg.ListenPort = 0
	cfg.DisableUTP = true
	cfg.DisableWebtorrent = true
	c, e := torrent.NewClient(cfg)
	if e == nil {
		t.client = c
	}
	return c, e
}
func (t *TorrentEngine) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client != nil {
		t.client.Close()
		t.client = nil
	}
}
func (t *TorrentEngine) Download(ctx context.Context, j Obj) error {
	var spec *torrent.TorrentSpec
	var err error
	if str(j["sourceType"]) == "magnet" {
		spec, err = torrent.TorrentSpecFromMagnetUri(str(j["sourceUrl"]))
	} else {
		req, e := http.NewRequestWithContext(ctx, "GET", str(j["sourceUrl"]), nil)
		if e != nil {
			return e
		}
		res, e := t.app.http.Do(req)
		if e != nil {
			return e
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return errors.New("获取种子失败")
		}
		mi, e := metainfo.Load(io.LimitReader(res.Body, 16<<20))
		if e != nil {
			return e
		}
		spec, err = torrent.TorrentSpecFromMetaInfoErr(mi)
	}
	if err != nil {
		return err
	}
	disk := storage.NewFile(str(j["directory"]))
	defer disk.Close()
	spec.Storage = disk
	c, err := t.get()
	if err != nil {
		return err
	}
	download, added, err := c.AddTorrentSpec(spec)
	if err != nil {
		return err
	}
	if !added {
		return errors.New("同一 BT 任务已经在下载")
	}
	defer download.Drop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-download.GotInfo():
	}
	download.DownloadAll()
	last := time.Now()
	var previous int64
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		done := download.BytesCompleted()
		total := download.Length()
		files := []Obj{}
		primary := ""
		var largest int64
		for _, f := range download.Files() {
			path := filepath.Join(str(j["directory"]), filepath.FromSlash(f.Path()))
			if !inside(str(j["directory"]), path) {
				return errors.New("种子包含越界路径")
			}
			files = append(files, Obj{"path": path, "size": f.Length()})
			if f.Length() > largest {
				largest = f.Length()
				primary = path
			}
		}
		_, err = t.app.Store.Patch("downloads", str(j["gid"]), Obj{"totalLength": total, "completedLength": done, "downloadSpeed": int64(float64(done-previous) / time.Since(last).Seconds()), "files": files, "filePath": primary})
		if err != nil {
			return err
		}
		previous = done
		last = time.Now()
		if total > 0 && done >= total {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
