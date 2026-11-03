package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// StreamEngine 在边下边播时接管 BT 任务：aria2 只能按顺序挪优先范围，拖进度条到还没下的地方只能干等；
// anacrolix/torrent 可以按读取位置取块，读到哪先下到哪。平时下载仍用 aria2，用户点边下边播时才切过来，
// 接着用 aria2 已经下好的文件（先校验），下完为止。
type StreamEngine struct {
	app      *App
	mu       sync.Mutex
	client   *torrent.Client
	torrents map[string]*torrent.Torrent
}

func (s *StreamEngine) get() (*torrent.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	cfg := torrent.NewDefaultClientConfig()
	// 每个任务各自指定存储，这里的目录只放客户端自己的状态
	cfg.DataDir = filepath.Join(s.app.cfg.DataDir, "anacrolix")
	cfg.ListenPort = 0
	cfg.Seed = false
	cfg.DisableWebtorrent = true
	c, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	s.client = c
	s.torrents = map[string]*torrent.Torrent{}
	return c, nil
}

func (s *StreamEngine) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
}

func (s *StreamEngine) spec(ctx context.Context, j Obj) (*torrent.TorrentSpec, error) {
	source := str(j["sourceUrl"])
	if str(j["sourceType"]) == "magnet" {
		return torrent.TorrentSpecFromMagnetUri(source)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", source, nil)
	if err != nil {
		return nil, err
	}
	res, err := s.app.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("获取种子失败")
	}
	mi, err := metainfo.Load(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	return torrent.TorrentSpecFromMetaInfoErr(mi)
}

// Download 下载一个已经切到 anacrolix 的任务，直到下完或 ctx 取消。
func (s *StreamEngine) Download(ctx context.Context, j Obj) error {
	gid := str(j["gid"])
	dir, err := filepath.Abs(str(j["directory"]))
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	spec, err := s.spec(ctx, j)
	if err != nil {
		return err
	}
	// 不用 .part 文件、不另存完成记录：文件名和目录结构与 aria2 一致，才能接着用它下好的部分
	disk := storage.NewFileOpts(storage.NewFileClientOpts{ClientBaseDir: dir, UsePartFiles: g.Some(false), PieceCompletion: storage.NewMapPieceCompletion()})
	defer disk.Close()
	spec.Storage = disk
	client, err := s.get()
	if err != nil {
		return err
	}
	t, _, err := client.AddTorrentSpec(spec)
	if err != nil {
		return err
	}
	defer t.Drop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.GotInfo():
	}
	if err = t.VerifyDataContext(ctx); err != nil {
		return err
	}
	files := []Obj{}
	primary := ""
	var main *torrent.File
	for _, f := range t.Files() {
		path := filepath.Join(dir, f.Path())
		if !inside(dir, path) {
			return errors.New("种子里的文件路径越界")
		}
		files = append(files, Obj{"path": path, "size": f.Length()})
		if main == nil || f.Length() > main.Length() {
			primary, main = path, f
		}
	}
	s.mu.Lock()
	s.torrents[gid] = t
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.torrents, gid)
		s.mu.Unlock()
	}()
	t.DownloadAll()
	if main != nil {
		prioritizeEnds(t, main, streamHead)
	}
	last, lastAt := t.BytesCompleted(), time.Now()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		done := t.BytesCompleted()
		speed := int64(float64(done-last) / time.Since(lastAt).Seconds())
		last, lastAt = done, time.Now()
		update := Obj{"files": files, "filePath": primary, "totalLength": t.Length(), "completedLength": done, "downloadSpeed": max(0, speed), "uploadSpeed": 0}
		if main != nil {
			// 和 aria2 那边一样记「从头连续下好多少」，暂停后没有读取器时还能按它估算能播到哪
			update["playableBytes"] = filePrefix(t, main)
		}
		if _, err = s.app.Store.Patch("downloads", gid, update); err != nil {
			return err
		}
		if t.BytesMissing() == 0 {
			// aria2 留下的控制文件已经没用了
			_ = os.Remove(filepath.Join(dir, t.Info().BestName()) + ".aria2")
			return nil
		}
	}
}

// prioritizeEnds 让主文件开头和结尾各 size 字节先下：开头用来起播，mkv 的索引在文件尾，
// 有了它往后拖时 ffmpeg 才能直接定位。读取器正在读的位置优先级更高，不受影响。
func prioritizeEnds(t *torrent.Torrent, f *torrent.File, size int64) {
	pieceLength := t.Info().PieceLength
	if pieceLength <= 0 {
		return
	}
	begin, end := f.BeginPieceIndex(), f.EndPieceIndex()
	span := int(size/pieceLength) + 1
	for i := begin; i < end; i++ {
		if i < begin+span || i >= end-span {
			t.Piece(i).SetPriority(torrent.PiecePriorityHigh)
		}
	}
}

// filePrefix 是文件从开头起连续下好的字节数。
func filePrefix(t *torrent.Torrent, f *torrent.File) int64 {
	pieceLength := t.Info().PieceLength
	for i := f.BeginPieceIndex(); i < f.EndPieceIndex(); i++ {
		if !t.Piece(i).State().Complete {
			return max(0, min(f.Length(), int64(i)*pieceLength-f.Offset()))
		}
	}
	return f.Length()
}

// waitReader 等刚切过来的任务准备好（拿到种子信息、校验完已有数据）再开读取器，最多等 30 秒。
// 这段时间里磁盘上的文件还有空洞，直接读会读到垃圾。
func (s *StreamEngine) waitReader(ctx context.Context, gid, path string) (torrent.Reader, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		if r, _, ok := s.Reader(gid, path); ok {
			return r, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("正在切换到边下边播，请稍候再试")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Reader 给正在由 anacrolix 下载的任务里的某个文件开一个读取器：读到哪就优先下到哪，没下到的地方读的时候等着。
func (s *StreamEngine) Reader(gid, path string) (torrent.Reader, int64, bool) {
	s.mu.Lock()
	t := s.torrents[gid]
	s.mu.Unlock()
	if t == nil {
		return nil, 0, false
	}
	j, _ := s.app.Store.Get("downloads", gid)
	dir, _ := filepath.Abs(str(j["directory"]))
	for _, f := range t.Files() {
		if filepath.Join(dir, f.Path()) == path {
			r := f.NewReader()
			r.SetReadahead(8 << 20)
			r.SetResponsive()
			return r, f.Length(), true
		}
	}
	return nil, 0, false
}

// takeOver 把一个 aria2 正在下的 BT 任务切给 anacrolix：停掉 aria2 那边，标记引擎后重新排队。
func (a *App) takeOver(gid string) error {
	j, err := a.Store.Get("downloads", gid)
	if err != nil || j == nil {
		return fmt.Errorf("任务不存在")
	}
	kind := str(j["sourceType"])
	if str(j["engine"]) == "anacrolix" || str(j["status"]) == "complete" || (kind != "magnet" && kind != "torrent-url") || str(a.Settings()["downloadClient"]) == "qbittorrent" {
		return nil
	}
	if err = a.stopJob(gid); err != nil {
		return err
	}
	if _, err = a.Store.Patch("downloads", gid, Obj{"engine": "anacrolix", "status": "queued"}); err != nil {
		return err
	}
	a.startJob(gid)
	return nil
}
