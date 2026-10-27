package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// One private subprocess per App. Its RPC secret is in a mode-0600 config,
// never command-line arguments. Download data travels directly through aria2.
type Aria2Engine struct {
	app                      *App
	mu                       sync.Mutex
	cmd                      *exec.Cmd
	done                     chan struct{}
	endpoint, secret, runDir string
	closed                   bool
}

func aria2Binary() (string, error) {
	if path := os.Getenv("BGMCAT_ARIA2_PATH"); path != "" {
		return exec.LookPath(path)
	}
	name := "aria2c"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if f, err := os.Stat(p); err == nil && !f.IsDir() {
			return p, nil
		}
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", errors.New("未找到 aria2c，请安装 aria2 或设置 BGMCAT_ARIA2_PATH；无需手动启动 RPC 服务")
	}
	return path, nil
}

var ariaRPCClient = &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func ariaCall(ctx context.Context, endpoint, secret, method string, params []any, out any) error {
	args := append([]any{"token:" + secret}, params...)
	data, err := json.Marshal(Obj{"jsonrpc": "2.0", "id": "bgmcat", "method": "aria2." + method, "params": args})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := ariaRPCClient.Do(req)
	if err != nil {
		return errors.New("aria2 RPC 连接失败")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("aria2 RPC HTTP %d", res.StatusCode)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&envelope); err != nil {
		return err
	}
	if envelope.Error != nil {
		return fmt.Errorf("aria2 RPC 错误 %d：%s", envelope.Error.Code, envelope.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}
func (e *Aria2Engine) online() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done == nil {
		return false
	}
	select {
	case <-e.done:
		return false
	default:
		return true
	}
}
func (e *Aria2Engine) ensure(ctx context.Context) (string, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.app.ctx.Err() != nil {
		return "", "", errors.New("下载服务已停止")
	}
	if e.done != nil {
		select {
		case <-e.done:
			if e.runDir != "" {
				os.RemoveAll(e.runDir)
			}
		default:
			return e.endpoint, e.secret, nil
		}
	}
	binary, err := aria2Binary()
	if err != nil {
		return "", "", err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	dir, err := os.MkdirTemp(e.app.cfg.DataDir, "aria2-run-")
	if err != nil {
		return "", "", err
	}
	secret := randomID(32)
	config := fmt.Sprintf("enable-rpc=true\nrpc-listen-all=false\nrpc-listen-port=%d\nrpc-secret=%s\nrpc-allow-origin-all=false\nconsole-log-level=error\nquiet=true\nsummary-interval=0\nmax-concurrent-downloads=4\nfile-allocation=none\nauto-save-interval=1\nmax-download-result=1000\nseed-time=0\nenable-dht=true\nenable-dht6=false\nbt-enable-lpd=false\nlisten-port=49152-65535\n", port, secret)
	// The DHT routing table lives in the data dir, not the throwaway run dir: aria2
	// ships no bootstrap nodes, so a fresh table after every restart meant magnets
	// with few trackers could not find peers. The entry point seeds an empty table.
	dhtDir := filepath.Join(e.app.cfg.DataDir, "aria2")
	if err = os.MkdirAll(dhtDir, 0700); err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}
	config += fmt.Sprintf("stop-with-process=%d\ndht-file-path=%s\ndht-file-path6=%s\ndht-entry-point=%s\n", os.Getpid(), filepath.Join(dhtDir, "dht.dat"), filepath.Join(dhtDir, "dht6.dat"), dhtEntryPoint)
	configPath := filepath.Join(dir, "aria2.conf")
	if err = os.WriteFile(configPath, []byte(config), 0600); err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}
	cmd := exec.Command(binary, "--conf-path="+configPath)
	cmd.Dir = dir
	// aria2 diagnostics can include private source URLs; don't publish them as app logs.
	log, err := os.OpenFile(filepath.Join(dir, "process.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		log.Close()
		os.RemoveAll(dir)
		return "", "", err
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); log.Close(); close(done) }()
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/jsonrpc", port)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		var version Obj
		if err = ariaCall(ctx, endpoint, secret, "getVersion", nil, &version); err == nil {
			e.cmd, e.done, e.endpoint, e.secret, e.runDir = cmd, done, endpoint, secret, dir
			return endpoint, secret, nil
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-done
			os.RemoveAll(dir)
			return "", "", ctx.Err()
		case <-done:
			os.RemoveAll(dir)
			return "", "", errors.New("aria2 启动失败，请确认版本和可执行文件可用")
		case <-time.After(50 * time.Millisecond):
		}
	}
	_ = cmd.Process.Kill()
	<-done
	os.RemoveAll(dir)
	return "", "", errors.New("aria2 RPC 启动超时")
}
func (e *Aria2Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	if e.cmd == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = ariaCall(ctx, e.endpoint, e.secret, "shutdown", nil, nil)
	select {
	case <-e.done:
	case <-time.After(3 * time.Second):
		_ = e.cmd.Process.Kill()
		<-e.done
	}
	os.RemoveAll(e.runDir)
}
func (e *Aria2Engine) Download(ctx context.Context, j Obj) error {
	// Restart a crashed child and re-add this job once; aria2 control files retain progress.
	for attempt := 0; attempt < 2; attempt++ {
		err := e.download(ctx, j)
		if err == nil || ctx.Err() != nil || e.online() {
			return err
		}
		if attempt == 1 {
			return err
		}
	}
	return errors.New("aria2 下载失败")
}

// dhtEntryPoint bootstraps an empty DHT table (aria2 accepts a single host:port).
const dhtEntryPoint = "dht.transmissionbt.com:6881"

func validTracker(raw string) bool {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "udp", "http", "https", "wss":
		return true
	}
	return false
}

// btTrackerOption turns the btTrackers setting into aria2's comma-separated
// bt-tracker value: trimmed, valid, de-duplicated, in the user's order.
func btTrackerOption(value any) string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range list(value) {
		tracker := strings.TrimSpace(str(v))
		if tracker == "" || seen[tracker] || !validTracker(tracker) {
			continue
		}
		seen[tracker] = true
		out = append(out, tracker)
	}
	return strings.Join(out, ",")
}

// applyTrackers replaces the task's announce list with the torrent's own
// trackers plus the user's extra ones, ordered so trackers that report peers
// come first (see orderTrackers). Extra trackers are a per-task option, so
// editing the list applies to the next download without restarting aria2.
func (e *Aria2Engine) applyTrackers(ctx context.Context, options Obj, hash metainfo.Hash, own []string) {
	extra := strings.Split(btTrackerOption(e.app.Settings()["btTrackers"]), ",")
	all := orderTrackers(own, extra, nil)
	if len(all) == 0 {
		return
	}
	ordered := orderTrackers(all, nil, scrapeTrackers(ctx, e.app.http, all, hash))
	options["bt-exclude-tracker"] = "*"
	options["bt-tracker"] = strings.Join(ordered, ",")
	// A dead tracker otherwise holds the queue for aria2's 60 s defaults.
	options["bt-tracker-connect-timeout"] = "10"
	options["bt-tracker-timeout"] = "20"
}

func (e *Aria2Engine) download(ctx context.Context, j Obj) error {
	endpoint, secret, err := e.ensure(ctx)
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(str(j["directory"]))
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	options := Obj{"dir": dir, "continue": "true", "auto-file-renaming": "false", "allow-overwrite": "false", "file-allocation": "none", "split": "8", "max-connection-per-server": "8", "min-split-size": "1M", "seed-time": "0", "max-tries": "3", "retry-wait": "1", "connect-timeout": "10", "timeout": "30", "follow-torrent": "false", "follow-metalink": "false", "bt-save-metadata": "true", "check-integrity": "true"}
	source := str(j["sourceUrl"])
	kind := str(j["sourceType"])
	method := "addUri"
	params := []any{[]string{source}, options}
	if kind == "torrent-url" {
		req, err := http.NewRequestWithContext(ctx, "GET", source, nil)
		if err != nil {
			return err
		}
		res, err := e.app.http.Do(req)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
		res.Body.Close()
		if readErr != nil {
			return readErr
		}
		if res.StatusCode != 200 || len(data) > 16<<20 {
			return errors.New("获取种子失败或种子超过 16 MiB")
		}
		method = "addTorrent"
		params = []any{base64.StdEncoding.EncodeToString(data), []string{}, options}
		if hash, own, parseErr := torrentTrackers(data); parseErr == nil {
			e.applyTrackers(ctx, options, hash, own)
		}
	} else if kind == "magnet" {
		if hash, own, parseErr := magnetTrackers(source); parseErr == nil {
			e.applyTrackers(ctx, options, hash, own)
		}
	} else {
		u, err := url.Parse(source)
		if err != nil {
			return err
		}
		path := filepath.Join(dir, safeName(filepath.Base(u.Path)))
		if old := str(j["filePath"]); old != "" && inside(dir, old) {
			path = old
		}
		options["out"] = filepath.Base(path)
		// Legacy Go partials have no aria2 piece map. Keep them untouched and start
		// safely from zero rather than assuming arbitrary bytes are valid pieces.
	}
	var gid string
	if err = ariaCall(ctx, endpoint, secret, method, params, &gid); err != nil {
		return err
	}
	tracked := []string{gid}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		visited := map[string]bool{}
		var remove func(string)
		remove = func(id string) {
			if visited[id] || len(visited) > 16 {
				return
			}
			visited[id] = true
			var state Obj
			if ariaCall(cleanup, endpoint, secret, "tellStatus", []any{id}, &state) == nil {
				for _, child := range list(state["followedBy"]) {
					remove(str(child))
				}
				if str(state["status"]) != "complete" && str(state["status"]) != "error" && str(state["status"]) != "removed" {
					_ = ariaCall(cleanup, endpoint, secret, "forceRemove", []any{id}, nil)
					for cleanup.Err() == nil {
						if ariaCall(cleanup, endpoint, secret, "tellStatus", []any{id}, &state) != nil {
							break
						}
						status := str(state["status"])
						if status == "removed" || status == "error" || status == "complete" {
							break
						}
						select {
						case <-cleanup.Done():
						case <-time.After(50 * time.Millisecond):
						}
					}
				}
			}
			_ = ariaCall(cleanup, endpoint, secret, "removeDownloadResult", []any{id}, nil)
		}
		for _, id := range tracked {
			remove(id)
		}
	}()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		var state Obj
		if err = ariaCall(ctx, endpoint, secret, "tellStatus", []any{gid}, &state); err != nil {
			return err
		}
		children := list(state["followedBy"])
		if len(children) > 0 {
			gid = str(children[0])
			tracked = append(tracked, gid)
			continue
		}
		status := str(state["status"])
		if status == "error" {
			return fmt.Errorf("aria2 下载失败，错误码 %s：%s", str(state["errorCode"]), str(state["errorMessage"]))
		}
		if status == "removed" {
			return errors.New("aria2 任务已移除")
		}
		// Magnet metadata is a synthetic download with a placeholder path.
		// Its followedBy child is the actual torrent payload, not this file list.
		if kind == "magnet" && len(tracked) == 1 {
			continue
		}
		files := []Obj{}
		primary := ""
		var largest int64
		for _, raw := range list(state["files"]) {
			f := object(raw)
			path := str(f["path"])
			if path == "" {
				continue
			}
			if !inside(dir, path) {
				return errors.New("aria2 返回越界文件路径")
			}
			size := parseInt(str(f["length"]))
			files = append(files, Obj{"path": path, "size": size})
			if primary == "" || size > largest {
				primary, largest = path, size
			}
		}
		update := Obj{"totalLength": parseInt(str(state["totalLength"])), "completedLength": parseInt(str(state["completedLength"])), "downloadSpeed": parseInt(str(state["downloadSpeed"])), "uploadSpeed": parseInt(str(state["uploadSpeed"])), "aria2Gid": gid}
		if len(files) > 0 {
			update["files"] = files
			update["filePath"] = primary
		}
		if _, err = e.app.Store.Patch("downloads", str(j["gid"]), update); err != nil {
			return err
		}
		if status == "complete" {
			if len(files) == 0 {
				return errors.New("aria2 完成但没有文件")
			}
			for _, f := range files {
				info, err := os.Stat(str(f["path"]))
				if err != nil || !info.Mode().IsRegular() || info.Size() != num(f["size"]) {
					return errors.New("下载完成后的文件校验失败")
				}
			}
			return nil
		}
	}
}
func (a *App) httpDownload(ctx context.Context, j Obj) error { return a.aria2.Download(ctx, j) }
