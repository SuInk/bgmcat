package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

var builtinWebTorrentTrackers = []string{
	"wss://tracker.openwebtorrent.com",
	"wss://tracker.btorrent.xyz",
	"wss://tracker.webtorrent.dev",
}

type activeBTStream struct {
	gid        string
	torrent    *torrent.Torrent
	mediaFile  *torrent.File
	mediaPath  string
	fileLen    int64
	fileOffset int64
	pieceLen   int64
	numPieces  int
	startTime  time.Time
	ready      atomic.Bool
	completed  atomic.Bool
	dropped    atomic.Bool
	boostMu    sync.Mutex
	boostGen   atomic.Uint64
}

func (s *activeBTStream) checkReady() bool {
	if s == nil || s.mediaFile == nil {
		return false
	}
	if s.ready.Load() {
		return true
	}
	if s.fileLen == 0 {
		s.ready.Store(true)
		return true
	}
	begin := s.mediaFile.BeginPieceIndex()
	if begin >= 0 && begin < s.numPieces && s.torrent.PieceState(begin).Complete {
		s.ready.Store(true)
		return true
	}
	return false
}

func (s *activeBTStream) boostOffset(fileRelOffset int64) {
	if s == nil || s.pieceLen <= 0 || s.numPieces <= 0 || s.dropped.Load() || s.completed.Load() {
		return
	}
	if fileRelOffset < 0 {
		fileRelOffset = 0
	}
	absOffset := s.fileOffset + fileRelOffset
	targetPiece := int(absOffset / s.pieceLen)
	if targetPiece < 0 || targetPiece >= s.numPieces {
		return
	}
	// 拖动进度后，新位置必须压过开播时给片头/索引区设的静态优先级，也不能和后面一大片
	// 预读区平分带宽，否则低速时要卡几十秒（实测 256 KB/s 下拖动从 ~48s 降到 ~16s）。分两步：
	//   1. 只要目标后 ~1 MiB（Now），其余未完成片段暂设 None，让引擎撤回不急的请求；
	//   2. 这 1 MiB 下完（或超时、又拖动了）后，其余恢复 Normal 继续全量下载；紧接着的内容由
	//      播放器 Reader 的预读按顺序要。
	// 播放器正在读的区域由 torrent.Reader 的 readahead 维持优先级，不受这里影响。
	nowEnd := min(s.numPieces, targetPiece+max(int((1<<20)/s.pieceLen), 2))
	gen := s.boostGen.Add(1)
	s.applyBoost(targetPiece, nowEnd, torrent.PiecePriorityNone)
	go func() {
		deadline := time.Now().Add(15 * time.Second)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if s.boostGen.Load() != gen || s.dropped.Load() || s.completed.Load() {
				return
			}
			if time.Now().After(deadline) || s.piecesComplete(targetPiece, nowEnd) {
				break
			}
		}
		if s.boostGen.Load() == gen && !s.dropped.Load() {
			// 不再给后面整片设 Next：同优先级的一大片会被并行下载，紧挨播放位置的字节反而最后才到。
			// 往后的顺序由播放器 Reader 的自适应预读（随连续播放增长）负责。
			s.applyBoost(targetPiece, nowEnd, torrent.PiecePriorityNormal)
		}
	}()
}

func (s *activeBTStream) piecesComplete(from, to int) bool {
	for idx := from; idx < to; idx++ {
		if !s.torrent.PieceState(idx).Complete {
			return false
		}
	}
	return true
}

// applyBoost sets [targetPiece, nowEnd) to PiecePriorityNow and every other
// incomplete piece to rest.
func (s *activeBTStream) applyBoost(targetPiece, nowEnd int, rest torrent.PiecePriority) {
	s.boostMu.Lock()
	defer s.boostMu.Unlock()
	if s.dropped.Load() {
		return
	}
	for idx := 0; idx < s.numPieces; idx++ {
		if s.torrent.PieceState(idx).Complete {
			continue
		}
		want := rest
		if idx >= targetPiece && idx < nowEnd {
			want = torrent.PiecePriorityNow
		}
		s.torrent.Piece(idx).SetPriority(want)
	}
}

func (s *activeBTStream) newReader(ctx context.Context) (io.ReadSeekCloser, bool) {
	if s == nil || s.mediaFile == nil {
		return nil, false
	}
	if s.completed.Load() {
		f, err := os.Open(s.mediaPath)
		if err != nil {
			return nil, false
		}
		return f, true
	}
	if s.dropped.Load() {
		return nil, false
	}
	tr := s.mediaFile.NewReader()
	tr.SetContext(ctx)
	tr.SetResponsive()
	// 预读随连续播放增长：刚拖动时只要 1 MiB，别让 8 MiB 预读和拖动目标抢带宽；
	// 连续往下播后逐步放大到 16 MiB，保证顺序播放的缓冲。
	tr.SetReadaheadFunc(func(rc torrent.ReadaheadContext) int64 {
		return min(max(rc.CurrentPos-rc.ContiguousReadStartPos, 1<<20), 16<<20)
	})
	return &btStreamReader{
		ctx:       ctx,
		stream:    s,
		tr:        tr,
		pos:       0,
		readerPos: 0,
	}, true
}

type btStreamReader struct {
	ctx       context.Context
	stream    *activeBTStream
	tr        torrent.Reader
	fallback  *os.File
	pos       int64
	readerPos int64
}

func (r *btStreamReader) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = r.pos + offset
	case io.SeekEnd:
		next = r.stream.fileLen + offset
	default:
		return r.pos, errors.New("无效 Seek 基准")
	}
	if next < 0 {
		return r.pos, errors.New("无效负数 Seek 偏移")
	}
	r.pos = next
	return r.pos, nil
}

func (r *btStreamReader) readFromFallback(p []byte) (int, error) {
	if r.fallback == nil {
		f, err := os.Open(r.stream.mediaPath)
		if err != nil {
			return 0, err
		}
		r.fallback = f
		if r.tr != nil {
			_ = r.tr.Close()
			r.tr = nil
		}
	}
	maxRead := r.stream.fileLen - r.pos
	if maxRead <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > maxRead {
		p = p[:maxRead]
	}
	n, err := r.fallback.ReadAt(p, r.pos)
	r.pos += int64(n)
	if err == nil && r.pos >= r.stream.fileLen {
		err = io.EOF
	}
	return n, err
}

func (r *btStreamReader) Read(p []byte) (int, error) {
	if r.pos >= r.stream.fileLen {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.fallback != nil || r.stream.completed.Load() {
		return r.readFromFallback(p)
	}
	if r.tr == nil {
		return 0, io.EOF
	}
	if r.pos != r.readerPos {
		r.stream.boostOffset(r.pos)
		if _, err := r.tr.Seek(r.pos, io.SeekStart); err != nil {
			if r.stream.completed.Load() {
				return r.readFromFallback(p)
			}
			return 0, err
		}
		r.readerPos = r.pos
	}
	n, err := r.tr.Read(p)
	if n > 0 {
		r.pos += int64(n)
		r.readerPos += int64(n)
	}
	if err != nil && !errors.Is(err, io.EOF) && r.stream.completed.Load() {
		if n > 0 {
			return n, nil
		}
		return r.readFromFallback(p)
	}
	return n, err
}

func (r *btStreamReader) Close() error {
	var err error
	if r.tr != nil {
		err = r.tr.Close()
		r.tr = nil
	}
	if r.fallback != nil {
		if fErr := r.fallback.Close(); err == nil {
			err = fErr
		}
		r.fallback = nil
	}
	return err
}

func (e *NativeEngine) acquireBTClient(loopbackOnly bool) (cl *torrent.Client, closeClient func(), err error) {
	if e.isClosed() || e.app.ctx.Err() != nil {
		return nil, nil, errors.New("下载服务已停止")
	}
	silentLogger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 10}))

	if loopbackOnly {
		btLoopDir := e.app.btLoopbackStateDir()
		if err = os.MkdirAll(btLoopDir, 0700); err != nil {
			return nil, nil, err
		}
		cfg := torrent.NewDefaultClientConfig()
		cfg.DataDir = btLoopDir
		cfg.Slogger = silentLogger
		cfg.NoDHT = true
		cfg.DisableUTP = true
		cfg.DisableWebtorrent = true
		cfg.DisableIPv6 = true
		cfg.DisablePEX = false
		cfg.NoDefaultPortForwarding = true
		cfg.NoUpload = false
		cfg.Seed = false
		cfg.ListenHost = func(string) string { return "127.0.0.1" }
		cfg.ListenPort = 0
		cfg.DownloadRateLimiter = e.btLimiter
		loopCl, cErr := torrent.NewClient(cfg)
		if cErr != nil {
			return nil, nil, cErr
		}
		return loopCl, func() { loopCl.Close() }, nil
	}

	e.btInitMu.Lock()
	defer e.btInitMu.Unlock()
	if e.isClosed() || e.app.ctx.Err() != nil {
		return nil, nil, errors.New("下载服务已停止")
	}
	if e.btClient != nil {
		return e.btClient, func() {}, nil
	}
	btStateDir := e.app.btClientStateDir()
	if err = os.MkdirAll(btStateDir, 0700); err != nil {
		return nil, nil, err
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = btStateDir
	cfg.Slogger = silentLogger
	cfg.NoDHT = false
	cfg.DisableUTP = false
	cfg.DisableTCP = false
	cfg.DisablePEX = false
	cfg.DisableIPv6 = false
	cfg.DisableWebtorrent = false
	cfg.NoUpload = false // Reciprocal upload prevents tit-for-tat choking by seeders/peers.
	cfg.Seed = false
	cfg.ListenPort = 0
	cfg.EstablishedConnsPerTorrent = 80
	cfg.HalfOpenConnsPerTorrent = 40
	cfg.TorrentPeersHighWater = 500
	cfg.TorrentPeersLowWater = 100
	cfg.PieceHashersPerTorrent = 4
	cfg.DownloadRateLimiter = e.btLimiter
	prodCl, cErr := torrent.NewClient(cfg)
	if cErr != nil {
		return nil, nil, cErr
	}
	e.btClient = prodCl
	return prodCl, func() {}, nil
}

// buildParallelTrackerTiers puts every unique valid tracker into its own
// single-element tier so anacrolix/torrent announces to ALL trackers in parallel
// at t=0ms instead of stalling sequentially behind empty or dead trackers.
func (e *NativeEngine) buildParallelTrackerTiers(own []string, loopbackOnly bool) [][]string {
	userExtra := strings.Split(btTrackerOption(e.app.Settings()["btTrackers"]), ",")
	var groups [][]string
	if loopbackOnly {
		groups = [][]string{own, userExtra}
	} else {
		groups = [][]string{own, userExtra, builtinBTTrackers, builtinWebTorrentTrackers}
	}
	seen := map[string]bool{}
	tiers := make([][]string, 0, len(own)+len(userExtra)+len(builtinBTTrackers))
	for _, grp := range groups {
		for _, raw := range grp {
			tr := strings.TrimSpace(raw)
			if tr == "" || seen[tr] || !validTracker(tr) {
				continue
			}
			if loopbackOnly && strings.HasPrefix(strings.ToLower(tr), "wss://") {
				continue
			}
			seen[tr] = true
			tiers = append(tiers, []string{tr})
		}
	}
	return tiers
}

func (e *NativeEngine) fetchTorrentData(ctx context.Context, source, stateDir, legacyDir, expectedHash string, hasFallback bool) ([]byte, error) {
	if cached := cachedTorrent(stateDir, expectedHash); len(cached) > 0 {
		return cached, nil
	}
	if legacyDir != "" && legacyDir != stateDir {
		if cached := cachedTorrent(legacyDir, expectedHash); len(cached) > 0 {
			return cached, nil
		}
	}
	maxAttempts := 3
	perAttemptTimeout := 8 * time.Second
	if hasFallback {
		maxAttempts = 1
		perAttemptTimeout = 4 * time.Second
	}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, perAttemptTimeout)
		req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, source, nil)
		if err != nil {
			cancelAttempt()
			return nil, err
		}
		res, err := e.app.http.Do(req)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
			res.Body.Close()
			cancelAttempt()
			if readErr == nil && res.StatusCode == http.StatusOK && len(data) > 0 && len(data) <= 16<<20 {
				if hash, _, parseErr := torrentTrackers(data); parseErr == nil {
					_ = os.MkdirAll(stateDir, 0700)
					_ = os.WriteFile(filepath.Join(stateDir, strings.ToLower(hash.HexString())+".torrent"), data, 0600)
				}
				return data, nil
			}
			if readErr != nil {
				lastErr = readErr
			} else {
				lastErr = errors.New("获取种子失败或种子超过 16 MiB")
			}
		} else {
			cancelAttempt()
			lastErr = err
		}
		if ctx.Err() != nil || attempt == maxAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil, lastErr
}

func isPlayableMediaExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return strings.Contains("|.mp4|.mkv|.webm|.avi|.ts|.mov|.m4v|.mp3|.flac|", "|"+ext+"|")
}

func prioritizeMediaForStreaming(t *torrent.Torrent, mediaTF *torrent.File) {
	t.DownloadAll()
	if mediaTF == nil || mediaTF.Length() <= 0 {
		return
	}
	info := t.Info()
	if info == nil || info.PieceLength <= 0 {
		return
	}
	pieceLen := info.PieceLength
	numPieces := t.NumPieces()
	fileStart := mediaTF.Offset()
	fileEnd := fileStart + mediaTF.Length()

	setRangePriority := func(byteStart, byteEnd int64, prio torrent.PiecePriority) {
		if byteStart < fileStart {
			byteStart = fileStart
		}
		if byteEnd > fileEnd {
			byteEnd = fileEnd
		}
		if byteEnd <= byteStart {
			return
		}
		pStart := max(int(byteStart/pieceLen), 0)
		pEnd := min(int((byteEnd-1)/pieceLen)+1, numPieces)
		for idx := pStart; idx < pEnd; idx++ {
			if t.PieceState(idx).Priority < prio {
				t.Piece(idx).SetPriority(prio)
			}
		}
	}

	// 1. First 1 MiB at PiecePriorityNow (container magic + initial video/audio headers).
	setRangePriority(fileStart, fileStart+(1<<20), torrent.PiecePriorityNow)
	// 2. 1 MiB .. 4 MiB and last 2 MiB at PiecePriorityNext (MP4 moov atom / MKV Cues index).
	setRangePriority(fileStart+(1<<20), fileStart+(4<<20), torrent.PiecePriorityNext)
	setRangePriority(fileEnd-(2<<20), fileEnd, torrent.PiecePriorityNext)
	// 3. 4 MiB .. 16 MiB at PiecePriorityHigh for smooth opening playback buffer.
	//    注意 anacrolix 的顺序是 Normal < High < Readahead < Next < Now，开播缓冲必须低于索引区。
	setRangePriority(fileStart+(4<<20), fileStart+(16<<20), torrent.PiecePriorityHigh)
}

func shouldVerifyExistingDiskData(dir string, t *torrent.Torrent) bool {
	files := t.Files()
	if len(files) == 0 {
		return false
	}
	completed := t.BytesCompleted()
	if completed == 0 {
		// If BoltDB has 0 completed bytes recorded yet non-empty payload files already exist on disk
		// (e.g. unindexed partial download recovery), hash them once to reuse valid pieces!
		for _, tf := range files {
			full := filepath.Join(dir, filepath.FromSlash(tf.Path()))
			if info, err := os.Stat(full); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
				return true
			}
		}
		return false
	}
	// If BoltDB thinks pieces are completed, verify only if an expected file vanished from disk.
	for _, tf := range files {
		if tf.BytesCompleted() > 0 {
			full := filepath.Join(dir, filepath.FromSlash(tf.Path()))
			if _, err := os.Stat(full); os.IsNotExist(err) {
				return true
			}
		}
	}
	return false
}

func (e *NativeEngine) downloadBT(ctx context.Context, j Obj) error {
	dir, err := filepath.Abs(str(j["directory"]))
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	gid := str(j["gid"])
	source := strings.TrimSpace(str(j["sourceUrl"]))
	fallback := strings.TrimSpace(str(j["fallbackUrl"]))
	kind := strings.TrimSpace(str(j["sourceType"]))
	if kind == "" {
		kind = inferSource(source)
	}
	knownHash := strings.ToLower(strings.TrimSpace(str(j["infoHash"])))

	recordHash := func(hash metainfo.Hash) {
		h := strings.ToLower(hash.HexString())
		if h != "" && h != knownHash {
			knownHash = h
			j["infoHash"] = h
			if gid != "" {
				_, _ = e.app.Store.Patch("downloads", gid, Obj{"infoHash": h})
			}
		}
	}

	stateDir := dir
	if gid != "" {
		stateDir = e.app.btTaskStateDir(gid)
		if err := os.MkdirAll(stateDir, 0700); err != nil {
			stateDir = dir
		}
	}

	var (
		spec *torrent.TorrentSpec
		own  []string
	)

	if kind == "torrent-url" {
		if fallback == "" {
			fallback = jobFallbackMagnet(j)
		}
		hasFallback := strings.HasPrefix(fallback, "magnet:?")
		data, fetchErr := e.fetchTorrentData(ctx, source, stateDir, dir, knownHash, hasFallback)
		if fetchErr != nil {
			if !hasFallback {
				return fetchErr
			}
			e.app.Log("info", "种子获取失败，自动切换磁力链接："+str(j["feedItemTitle"]))
			source = fallback
			kind = "magnet"
			if gid != "" {
				_, _ = e.app.Store.Patch("downloads", gid, Obj{"sourceUrl": source, "sourceType": "magnet"})
				e.app.notifyDownloads()
			}
		} else {
			mi, loadErr := metainfo.Load(bytes.NewReader(data))
			if loadErr != nil {
				return fmt.Errorf("解析种子失败：%w", loadErr)
			}
			tSpec, specErr := torrent.TorrentSpecFromMetaInfoErr(mi)
			if specErr != nil {
				return fmt.Errorf("无效种子元数据：%w", specErr)
			}
			spec = tSpec
			hash, trList, parseErr := torrentTrackers(data)
			if parseErr == nil {
				recordHash(hash)
				own = trList
			}
		}
	}

	if kind == "magnet" {
		mHash, mOwn, mErr := magnetTrackers(source)
		if mErr != nil {
			return fmt.Errorf("无效磁力链接：%w", mErr)
		}
		recordHash(mHash)
		own = append(own, mOwn...)

		data := cachedTorrent(stateDir, knownHash)
		if len(data) == 0 && stateDir != dir {
			data = cachedTorrent(dir, knownHash)
		}
		if len(data) > 0 {
			if mi, loadErr := metainfo.Load(bytes.NewReader(data)); loadErr == nil {
				if tSpec, specErr := torrent.TorrentSpecFromMetaInfoErr(mi); specErr == nil {
					spec = tSpec
					if hash, cOwn, parseErr := torrentTrackers(data); parseErr == nil {
						recordHash(hash)
						own = append(cOwn, own...)
					}
				}
			}
		}
		if spec == nil {
			mSpec, specErr := torrent.TorrentSpecFromMagnetUri(source)
			if specErr != nil {
				return fmt.Errorf("解析磁力链接失败：%w", specErr)
			}
			spec = mSpec
		}
	}

	if spec == nil {
		return errors.New("无法解析 BT 下载源")
	}

	if knownHash != "" && e.app.hasOtherActiveInfoHash(gid, knownHash) {
		return errors.New("相同种子的下载任务已在运行中")
	}

	loopbackOnly := hasLoopbackTracker(own)
	cl, closeCl, err := e.acquireBTClient(loopbackOnly)
	if err != nil {
		return err
	}
	defer closeCl()

	taskPC, pcErr := storage.NewBoltPieceCompletion(stateDir)
	if pcErr != nil {
		taskPC = storage.NewMapPieceCompletion()
	}
	silentLogger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 10}))
	taskStorage := storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:   dir,
		PieceCompletion: taskPC,
		UsePartFiles:    g.Some(false),
		Logger:          silentLogger,
	})
	defer func() { _ = taskStorage.Close() }()

	spec.Storage = taskStorage
	spec.Trackers = e.buildParallelTrackerTiers(own, loopbackOnly)

	t, _, err := cl.AddTorrentSpec(spec)
	if err != nil {
		return err
	}
	if len(spec.Trackers) > 0 {
		t.AddTrackers(spec.Trackers)
	}

	var stream *activeBTStream
	defer func() {
		if gid != "" {
			e.mu.Lock()
			delete(e.activeBT, gid)
			e.mu.Unlock()
		}
		if stream != nil {
			stream.dropped.Store(true)
		}
		t.Drop()
	}()

	// Wait for magnet metadata (instant if .torrent or cached .torrent was loaded).
	metaTicker := time.NewTicker(300 * time.Millisecond)
	var lastMetaConns int64 = -1
waitMeta:
	for {
		select {
		case <-t.GotInfo():
			break waitMeta
		case <-ctx.Done():
			metaTicker.Stop()
			return ctx.Err()
		case <-metaTicker.C:
			if gid != "" {
				st := t.Stats()
				conns := int64(st.ActivePeers)
				if conns != lastMetaConns {
					lastMetaConns = conns
					_, _ = e.app.Store.Patch("downloads", gid, Obj{"connectionCount": conns})
					e.app.notifyDownloads()
				}
			}
		}
	}
	metaTicker.Stop()

	recordHash(t.InfoHash())
	// Persist <infohash>.torrent in the task state directory so magnet pauses resume with 0s metadata wait
	// without cluttering the user's downloads/ folder.
	if knownHash != "" {
		torrentCachePath := filepath.Join(stateDir, knownHash+".torrent")
		if _, statErr := os.Stat(torrentCachePath); os.IsNotExist(statErr) {
			mi := t.Metainfo()
			var buf bytes.Buffer
			if mi.Write(&buf) == nil && buf.Len() > 0 {
				_ = os.MkdirAll(stateDir, 0700)
				_ = os.WriteFile(torrentCachePath, buf.Bytes(), 0600)
			}
		}
	}

	tFiles := t.Files()
	if len(tFiles) == 0 {
		return errors.New("种子中没有可下载文件")
	}

	filesMeta := make([]Obj, 0, len(tFiles))
	primaryPath := ""
	var largestSize int64 = -1
	var mediaTF *torrent.File
	var mediaFullPath string
	var largestMediaSize int64 = -1

	for _, tf := range tFiles {
		full := filepath.Join(dir, filepath.FromSlash(tf.Path()))
		if !inside(dir, full) {
			return errors.New("种子包含越界文件路径")
		}
		sz := tf.Length()
		filesMeta = append(filesMeta, Obj{"path": full, "size": sz})
		if primaryPath == "" || sz > largestSize {
			primaryPath, largestSize = full, sz
		}
		if isPlayableMediaExt(full) && sz > largestMediaSize {
			mediaTF = tf
			mediaFullPath = full
			largestMediaSize = sz
		}
	}

	if shouldVerifyExistingDiskData(dir, t) {
		if vErr := t.VerifyDataContext(ctx); vErr != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}

	prioritizeMediaForStreaming(t, mediaTF)

	if gid != "" && mediaTF != nil {
		info := t.Info()
		pieceLen := int64(0)
		if info != nil {
			pieceLen = info.PieceLength
		}
		stream = &activeBTStream{
			gid:        gid,
			torrent:    t,
			mediaFile:  mediaTF,
			mediaPath:  mediaFullPath,
			fileLen:    mediaTF.Length(),
			fileOffset: mediaTF.Offset(),
			pieceLen:   pieceLen,
			numPieces:  t.NumPieces(),
			startTime:  time.Now(),
		}
		stream.checkReady()
		e.mu.Lock()
		e.activeBT[gid] = stream
		e.mu.Unlock()
	}

	totalLen := t.Length()
	initCompleted := t.BytesCompleted()
	if gid != "" {
		_, _ = e.app.Store.Patch("downloads", gid, Obj{
			"totalLength":     totalLen,
			"completedLength": initCompleted,
			"files":           filesMeta,
			"filePath":        primaryPath,
		})
		e.app.notifyDownloads()
	}

	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()

	initTime := time.Now()
	initStats := t.Stats()
	initWireRead := initStats.BytesReadUsefulData.Int64()
	initWireWrite := initStats.BytesWrittenData.Int64()
	downMeter := newSpeedWindow(3 * time.Second)
	upMeter := newSpeedWindow(3 * time.Second)
	downMeter.Observe(initTime, initWireRead)
	upMeter.Observe(initTime, initWireWrite)

	lastVerified := initCompleted
	wireAtLastVerified := initWireRead
	lastCompleted := initCompleted
	pieceCap := int64(256 << 10)
	if info := t.Info(); info != nil && info.PieceLength > pieceCap {
		pieceCap = info.PieceLength
	}
	var lastDownSpeed, lastUpSpeed, lastConns int64 = -1, -1, -1
	wasStreamReady := stream != nil && stream.ready.Load()

	for {
		if t.BytesMissing() == 0 {
			if stream != nil {
				stream.completed.Store(true)
			}
			for _, f := range filesMeta {
				p := str(f["path"])
				wantSize := num(f["size"])
				if wantSize == 0 {
					if _, sErr := os.Stat(p); os.IsNotExist(sErr) {
						_ = os.MkdirAll(filepath.Dir(p), 0700)
						if emptyF, cErr := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0600); cErr == nil {
							_ = emptyF.Close()
						}
					}
				}
				info, sErr := os.Stat(p)
				if sErr != nil || !info.Mode().IsRegular() || info.Size() != wantSize {
					return errors.New("BT 下载完成后的文件校验失败")
				}
			}
			if gid != "" {
				_, _ = e.app.Store.Patch("downloads", gid, Obj{
					"totalLength":     totalLen,
					"completedLength": totalLen,
					"downloadSpeed":   0,
					"uploadSpeed":     0,
					"connectionCount": 0,
					"files":           filesMeta,
					"filePath":        primaryPath,
				})
				e.app.notifyDownloads()
			}
			return nil
		}

		select {
		case <-ctx.Done():
			if gid != "" {
				_, _ = e.app.Store.Patch("downloads", gid, Obj{
					"totalLength":     totalLen,
					"completedLength": t.BytesCompleted(),
					"downloadSpeed":   0,
					"uploadSpeed":     0,
					"connectionCount": 0,
					"files":           filesMeta,
					"filePath":        primaryPath,
				})
			}
			return ctx.Err()
		case <-tick.C:
		}

		nowTime := time.Now()
		st := t.Stats()
		curWireRead := st.BytesReadUsefulData.Int64()
		curWireWrite := st.BytesWrittenData.Int64()
		verified := t.BytesCompleted()

		if verified > lastVerified {
			lastVerified = verified
			wireAtLastVerified = curWireRead
		}
		inFlightPieceBytes := min(max(curWireRead-wireAtLastVerified, 0), pieceCap)
		completed := lastVerified + inFlightPieceBytes
		if totalLen > 0 && completed >= totalLen {
			completed = totalLen - 1
		}
		if verified > completed {
			completed = verified
		}
		if completed < lastCompleted {
			completed = lastCompleted
		}

		// Speed uses pure block-level wire read (falling back to verified bytes only if
		// wire counters are 0) over a 3s rolling window so piece-hash completions never spike speed.
		downCumulative := curWireRead
		if downCumulative <= 0 && verified > initCompleted {
			downCumulative = verified - initCompleted
		}
		downSpeed := downMeter.Observe(nowTime, downCumulative)
		upSpeed := upMeter.Observe(nowTime, curWireWrite)
		conns := int64(st.ActivePeers)
		nowReady := stream != nil && stream.checkReady()

		if completed != lastCompleted || downSpeed != lastDownSpeed || upSpeed != lastUpSpeed || conns != lastConns || nowReady != wasStreamReady {
			lastCompleted, lastDownSpeed, lastUpSpeed, lastConns, wasStreamReady = completed, downSpeed, upSpeed, conns, nowReady
			if gid != "" {
				if _, pErr := e.app.Store.Patch("downloads", gid, Obj{
					"totalLength":     totalLen,
					"completedLength": completed,
					"downloadSpeed":   downSpeed,
					"uploadSpeed":     upSpeed,
					"connectionCount": conns,
					"files":           filesMeta,
					"filePath":        primaryPath,
				}); pErr != nil {
					return pErr
				}
				e.app.notifyDownloads()
			}
		}
	}
}
