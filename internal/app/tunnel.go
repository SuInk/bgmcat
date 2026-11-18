package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Tunnel struct {
	mu               sync.Mutex
	cancel           context.CancelFunc
	done             chan struct{}
	state, url       string
	err              string
	restarts         int
	logs             []Obj
	nextLogID        int64
	pid              any
	startedAt        any
	conns            int
	cachedBinSetting string
	cachedBinPath    string
	cachedBinAt      time.Time
}

var quickURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

func (t *Tunnel) appendLogLocked(level, msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	t.nextLogID++
	t.logs = append(t.logs, Obj{
		"id":        t.nextLogID,
		"level":     level,
		"message":   msg,
		"createdAt": now(),
	})
	if len(t.logs) > 120 {
		t.logs = append([]Obj(nil), t.logs[len(t.logs)-120:]...)
	}
}

func classifyTunnelLogLevel(line string) string {
	low := strings.ToLower(line)
	switch {
	case strings.Contains(line, " ERR ") || strings.Contains(low, "error") || strings.Contains(low, "failed"):
		return "error"
	case strings.Contains(line, " WRN ") || strings.Contains(low, "warn"):
		return "warn"
	default:
		return "info"
	}
}

func (a *App) tunnelPublicURL() string {
	a.tunnel.mu.Lock()
	defer a.tunnel.mu.Unlock()
	return strings.TrimRight(strings.TrimSpace(a.tunnel.url), "/")
}

func (t *Tunnel) stop() {
	t.mu.Lock()
	cancel, done := t.cancel, t.done
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}
func (a *App) tunnelStatus() Obj {
	s := a.Settings()
	binPath := a.tunnelBinaryFor(str(s["tunnelBinaryPath"]))
	a.tunnel.mu.Lock()
	defer a.tunnel.mu.Unlock()
	state := a.tunnel.state
	if state == "" {
		state = "stopped"
	}
	return Obj{
		"state":            state,
		"enabled":          flag(s["tunnelEnabled"]),
		"mode":             s["tunnelMode"],
		"publicUrl":        a.tunnel.url,
		"lastError":        a.tunnel.err,
		"readyConnections": a.tunnel.conns,
		"restartCount":     a.tunnel.restarts,
		"binaryAvailable":  binPath != "",
		"binaryPath":       binPath,
		"binarySource":     "path",
		"hostname":         s["tunnelHostname"],
		"connectorId":      nil,
		"metricsPort":      nil,
		"pid":              a.tunnel.pid,
		"startedAt":        a.tunnel.startedAt,
		"nextRetryAt":      nil,
	}
}
func (a *App) tunnelBinary() string {
	return a.tunnelBinaryFor(str(a.Settings()["tunnelBinaryPath"]))
}
func (a *App) tunnelBinaryFor(configured string) string {
	path := strings.TrimSpace(configured)
	if path == "" {
		path = "cloudflared"
	}
	a.tunnel.mu.Lock()
	if a.tunnel.cachedBinSetting == path && time.Since(a.tunnel.cachedBinAt) < 60*time.Second {
		cached := a.tunnel.cachedBinPath
		a.tunnel.mu.Unlock()
		return cached
	}
	a.tunnel.mu.Unlock()

	resolved, _ := exec.LookPath(path)

	a.tunnel.mu.Lock()
	a.tunnel.cachedBinSetting = path
	a.tunnel.cachedBinPath = resolved
	a.tunnel.cachedBinAt = time.Now()
	a.tunnel.mu.Unlock()
	return resolved
}

// writeEmptyTunnelConfig writes an empty YAML file so Quick/Token tunnels do not
// auto-load the user's pre-existing ~/.cloudflared/config.yml ingress rules.
func (a *App) writeEmptyTunnelConfig() (string, error) {
	file := filepath.Join(a.cfg.DataDir, "tunnel-empty.yml")
	if err := os.WriteFile(file, []byte("{}\n"), 0600); err != nil {
		return "", err
	}
	return file, nil
}

func (a *App) startTunnel() error {
	a.tunnel.stop()
	path := a.tunnelBinary()
	if path == "" {
		return errors.New("未找到可选的 Cloudflare Tunnel 组件；本地与云盘功能不需要它")
	}
	s := a.Settings()
	_, port, _ := net.SplitHostPort(a.cfg.Addr)
	origin := "http://127.0.0.1:" + port
	var args []string
	if str(s["tunnelMode"]) == "named" {
		if str(s["tunnelCredentialMode"]) == "credentials-file" {
			if str(s["tunnelId"]) == "" || str(s["tunnelCredentialsFile"]) == "" {
				return errors.New("请填写隧道 ID 和凭据文件")
			}
			config := fmt.Sprintf("tunnel: %q\ncredentials-file: %q\n", str(s["tunnelId"]), str(s["tunnelCredentialsFile"]))
			if host := str(s["tunnelHostname"]); host != "" {
				config += fmt.Sprintf("ingress:\n  - hostname: %q\n    service: %q\n  - service: http_status:404\n", host, origin)
			} else {
				config += fmt.Sprintf("url: %q\n", origin)
			}
			file := filepath.Join(a.cfg.DataDir, "tunnel.yml")
			if e := os.WriteFile(file, []byte(config), 0600); e != nil {
				return e
			}
			args = []string{"--no-autoupdate", "--config", file, "tunnel", "run", str(s["tunnelId"])}
		} else {
			if str(s["tunnelToken"]) == "" {
				return errors.New("请填写隧道 token")
			}
			emptyCfg, e := a.writeEmptyTunnelConfig()
			if e != nil {
				return e
			}
			file := filepath.Join(a.cfg.DataDir, "tunnel-token")
			if e := os.WriteFile(file, []byte(str(s["tunnelToken"])), 0600); e != nil {
				return e
			}
			args = []string{"--no-autoupdate", "--config", emptyCfg, "tunnel", "run", "--token-file", file}
		}
	} else {
		emptyCfg, e := a.writeEmptyTunnelConfig()
		if e != nil {
			return e
		}
		args = []string{"--no-autoupdate", "--config", emptyCfg, "tunnel", "--url", origin}
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.tunnel.mu.Lock()
	a.tunnel.cancel = cancel
	a.tunnel.done = make(chan struct{})
	done := a.tunnel.done
	a.tunnel.state = "starting"
	a.tunnel.err = ""
	a.tunnel.restarts = 0
	a.tunnel.url = ""
	a.tunnel.pid = nil
	a.tunnel.startedAt = now()
	a.tunnel.conns = 0
	a.tunnel.appendLogLocked("info", "正在启动 Cloudflare Tunnel")
	if host := str(s["tunnelHostname"]); str(s["tunnelMode"]) == "named" && host != "" {
		a.tunnel.url = "https://" + host
	}
	a.tunnel.mu.Unlock()
	go func() {
		defer close(done)
		defer cancel()
		for attempt := 0; ; attempt++ {
			cmd := exec.CommandContext(ctx, path, args...)
			pipe, e := cmd.StderrPipe()
			cmd.Stdout = io.Discard
			if e == nil {
				e = cmd.Start()
			}
			if e == nil {
				a.tunnel.mu.Lock()
				if cmd.Process != nil {
					a.tunnel.pid = cmd.Process.Pid
				}
				a.tunnel.mu.Unlock()
				scanner := bufio.NewScanner(pipe)
				for scanner.Scan() {
					line := scanner.Text()
					a.tunnel.mu.Lock()
					if u := quickURL.FindString(line); u != "" {
						a.tunnel.url = u
						a.tunnel.state = "online"
					}
					if strings.Contains(line, "Registered tunnel connection") {
						a.tunnel.conns++
						a.tunnel.state = "online"
					}
					if strings.Contains(line, "Unregistered tunnel connection") && a.tunnel.conns > 0 {
						a.tunnel.conns--
					}
					a.tunnel.appendLogLocked(classifyTunnelLogLevel(line), line)
					a.tunnel.mu.Unlock()
				}
				e = cmd.Wait()
			}
			a.tunnel.mu.Lock()
			a.tunnel.pid = nil
			a.tunnel.conns = 0
			a.tunnel.url = ""
			a.tunnel.state = "stopped"
			if ctx.Err() == nil {
				a.tunnel.state = "error"
				a.tunnel.err = "隧道进程退出，请检查凭据及网络"
				a.tunnel.appendLogLocked("error", a.tunnel.err)
			} else {
				a.tunnel.appendLogLocked("info", "Cloudflare Tunnel 已停止")
			}
			a.tunnel.mu.Unlock()
			if ctx.Err() != nil || !flag(a.Settings()["tunnelAutoRestart"]) || attempt >= 8 {
				break
			}
			delay := time.Second * time.Duration(1<<min(attempt, 5))
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			a.tunnel.mu.Lock()
			a.tunnel.restarts = attempt + 1
			a.tunnel.state = "starting"
			if cur := a.Settings(); str(cur["tunnelMode"]) == "named" {
				if host := strings.TrimSpace(str(cur["tunnelHostname"])); host != "" {
					a.tunnel.url = "https://" + host
				}
			}
			a.tunnel.mu.Unlock()
		}
		a.tunnel.mu.Lock()
		a.tunnel.cancel = nil
		a.tunnel.mu.Unlock()
	}()

	return nil
}
func (a *App) tunnelRoutes() {
	a.handle("GET /api/tunnel/status", func(w http.ResponseWriter, r *http.Request) error { ok(w, a.tunnelStatus()); return nil })
	a.handle("GET /api/tunnel/preflight", func(w http.ResponseWriter, r *http.Request) error {
		issues := []Obj{}
		if a.tunnelBinary() == "" {
			issues = append(issues, Obj{"level": "block", "code": "MISSING_BINARY", "message": "未安装可选 cloudflared 组件", "autoFixable": false})
		}
		ok(w, issues)
		return nil
	})
	a.handle("GET /api/tunnel/logs", func(w http.ResponseWriter, r *http.Request) error {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 200 {
			limit = 100
		}
		a.tunnel.mu.Lock()
		n := len(a.tunnel.logs)
		start := 0
		if n > limit {
			start = n - limit
		}
		out := make([]Obj, n-start)
		copy(out, a.tunnel.logs[start:])
		a.tunnel.mu.Unlock()
		ok(w, out)
		return nil
	})
	a.handle("GET /api/tunnel/discovered", func(w http.ResponseWriter, r *http.Request) error {
		items := []Obj{}
		home, _ := os.UserHomeDir()
		cfDir := filepath.Join(home, ".cloudflared")
		hostByUUID := map[string]string{}
		var ymlFiles []string
		for _, pat := range []string{"*.yml", "*.yaml"} {
			if matched, _ := filepath.Glob(filepath.Join(cfDir, pat)); len(matched) > 0 {
				ymlFiles = append(ymlFiles, matched...)
			}
		}
		for _, cfgPath := range ymlFiles {
			raw, e := os.ReadFile(cfgPath)
			if e != nil {
				continue
			}
			var tid, host string
			for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "#") {
					continue
				}
				if strings.HasPrefix(trimmed, "tunnel:") {
					tid = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "tunnel:")), `"'`)
				} else if host == "" && strings.Contains(trimmed, "hostname:") {
					_, after, okSplit := strings.Cut(trimmed, "hostname:")
					if okSplit {
						h := strings.Trim(strings.TrimSpace(after), `"'`)
						if h != "" && !strings.Contains(h, "*") {
							host = h
						}
					}
				}
			}
			if tid != "" && host != "" && hostByUUID[tid] == "" {
				hostByUUID[tid] = host
			}
		}
		paths, _ := filepath.Glob(filepath.Join(cfDir, "*.json"))
		for _, path := range paths {
			raw, e := os.ReadFile(path)
			if e != nil {
				continue
			}
			var v Obj
			if json.Unmarshal(raw, &v) != nil || str(v["TunnelID"]) == "" {
				continue
			}
			uid := str(v["TunnelID"])
			var hostname any
			fromConfig := false
			if h := hostByUUID[uid]; h != "" {
				hostname = h
				fromConfig = true
			}
			items = append(items, Obj{"uuid": uid, "hostname": hostname, "credentialsFile": path, "fromConfig": fromConfig})
		}
		ok(w, items)
		return nil
	})
	for _, action := range []string{"start", "restart", "stop"} {
		a.handle("POST /api/tunnel/"+action, func(w http.ResponseWriter, r *http.Request) error {
			if strings.HasSuffix(r.URL.Path, "/stop") {
				a.tunnel.stop()
			} else if e := a.startTunnel(); e != nil {
				return e
			}
			ok(w, a.tunnelStatus())
			return nil
		})
	}
}
